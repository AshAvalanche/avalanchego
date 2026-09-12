// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

// Package nativeexport lets a C-Chain address move its own AVAX to the P-Chain.
//
// Everything else in this proposal already works for a key holder: an EOA signs
// an atomic export naming a warp owner, and may name a third party's, so
// funding a contract asks nothing of the contract. What is missing is a
// contract moving its *own* balance on its own initiative - and it cannot,
// because an atomic transaction is not an EVM transaction. It is gossiped
// separately, carried in the block's extra data, and lives outside any call
// frame, so there is no point from which a contract could emit one.
//
// A precompile changes the mechanism rather than working around it. It runs
// inside an EVM transaction, in a frame where msg.sender cannot be forged, so
// the output's owner is forced to the caller exactly as the Warp precompile
// forces an AddressedCall's source address.
package nativeexport

import (
	"errors"
	"fmt"

	"github.com/ava-labs/libevm/common"
	"github.com/ava-labs/libevm/core/vm"
	"github.com/ava-labs/libevm/crypto"
	"github.com/ava-labs/libevm/libevm/stateconf"
	"github.com/holiman/uint256"

	"github.com/ava-labs/avalanchego/graft/coreth/precompile/contract"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/vms/warpfx"

	ethtypes "github.com/ava-labs/libevm/core/types"
)

var (
	ErrValueNotWholeNAVAX = errors.New("value is not a whole number of nAVAX")
	ErrValueTooLarge      = errors.New("value does not fit in a uint64")
	ErrNoValue            = errors.New("value is zero")
	ErrWrongSelector      = errors.New("unknown function selector")
	ErrCannotDebit        = errors.New("state cannot be debited")
)

// counterState is the slice of the state the index counter needs, named so the
// counter can be exercised without standing up a whole StateDB.
type counterState interface {
	TxHash() common.Hash
	GetState(common.Address, common.Hash, ...stateconf.StateDBStateOption) common.Hash
	SetState(common.Address, common.Hash, common.Hash, ...stateconf.StateDBStateOption)
}

// balanceDebitor is the half of the concrete state that contract.StateDB does
// not expose. Burning AVAX on the C-Chain is not a new primitive: it is
// SubBalance, the very call an atomic export's execution already makes.
type balanceDebitor interface {
	SubBalance(common.Address, *uint256.Int)
}

const (
	// X2CRate is the aAVAX-per-nAVAX rate the atomic path already uses.
	X2CRate = 1_000_000_000

	// ExportAVAXGas is charged for the permanent state this call creates, not
	// for the arithmetic it performs.
	//
	// ⚠️ Serializing the UTXO, the shared-memory PutRequest and the atomic trie
	// write all outlive the transaction. Pricing the computation alone - the
	// reflex when writing a precompile - would under-charge state growth
	// durably, and under-charging a write is not recoverable at the next
	// upgrade: it leaves permanent state nobody paid for.
	//
	// The figure targets the same order of magnitude as an equivalent atomic
	// Export, which costs ap5.AtomicTxIntrinsicGas (10,000) plus its size, and
	// covers the counter's SSTORE below.
	ExportAVAXGas = 32_000
)

// ContractAddress is in the range the registerer reserves for precompiles, one
// slot past the Warp precompile at ...0005.
var ContractAddress = common.HexToAddress("0x0200000000000000000000000000000000000006")

// ExportAVAXSignature is the one entry point, and exportAVAXSelector is derived
// from it rather than written down: a hand-copied selector is a silent revert
// waiting to happen.
const ExportAVAXSignature = "exportAVAX()"

var exportAVAXSelector = crypto.Keccak256([]byte(ExportAVAXSignature))[:4]

// counterSlot holds (lastTxHash, count) for the OutputIndex of an export.
//
// An exported output's index is normally its rank in ExportedOutputs, which a
// self-contained atomic transaction provides. A precompile has no such
// container: several calls, possibly from several contracts, may share one EVM
// transaction, and each must produce a distinct UTXOID.
//
// The slot packs the transaction hash's first 28 bytes with a 4-byte count.
// ⚠️ That truncation is a *reset heuristic*, not what makes UTXOIDs unique:
// a UTXOID is (txHash, index) with the hash whole, so two transactions
// produce disjoint ids whatever their indices. Should two hashes share a
// 28-byte prefix, the later one merely starts counting above zero.
//
// The counter is kept in storage rather than derived from stateDB.Logs(),
// which is *block* scoped: filtering it would cost whatever earlier
// transactions in the block happened to log, so an identical call would not
// cost the same depending on its position, and someone could fill a block with
// logs to make this precompile ruinous. A storage slot is O(1), journaled - so
// a reverted frame restores it, exactly like AddLog - and bounded.
var counterSlot = common.Hash{}

// EventTopic identifies an export log. The UTXO is rebuilt from these logs and
// from them alone: AddLog is journaled and a reverted frame erases its logs, so
// a failed transaction, included with status 0, deposits nothing. Any channel
// beside the journal would leak a deposit from a cancelled call.
var EventTopic = common.HexToHash("0x8ec4f2c8b8f5c4a1d1e9a5b3c7d2f6e0a4b8c1d5e9f3a7b2c6d0e4f8a2b6c1d5")

// ExportPrecompile is the stateful contract behind ContractAddress.
var ExportPrecompile = createPrecompile()

func createPrecompile() contract.StatefulPrecompiledContract {
	c, err := contract.NewStatefulPrecompileContract(nil, []*contract.StatefulPrecompileFunction{
		contract.NewStatefulPrecompileFunction(exportAVAXSelector, exportAVAX),
	})
	if err != nil {
		panic(err)
	}
	return c
}

// exportAVAX debits msg.value and logs an export of it to the P-Chain, owned by
// the caller's warp owner.
//
// One entry point, no parameter. The precompile debits, records the deposit and
// stops there. It has no other UTXO's amount to know, no P-Chain fee to set, no
// import to encode and no Warp message to emit: the ImportTx being canonical,
// anyone rebuilds it from what they read in shared memory.
func exportAVAX(
	accessibleState contract.AccessibleState,
	caller common.Address,
	addr common.Address,
	input []byte,
	suppliedGas uint64,
	readOnly bool,
) ([]byte, uint64, error) {
	remainingGas, err := contract.DeductGas(suppliedGas, ExportAVAXGas)
	if err != nil {
		return nil, 0, err
	}
	if readOnly {
		return nil, remainingGas, vm.ErrWriteProtection
	}
	if len(input) != 0 {
		return nil, remainingGas, fmt.Errorf("%w: exportAVAX takes no argument", ErrWrongSelector)
	}

	stateDB := accessibleState.GetStateDB()

	// payable moved msg.value from the caller to this address, with the EVM's
	// own balance check and a clean revert. It must now leave the EVM's books,
	// being about to exist as a UTXO on the P-Chain: left here it would be AVAX
	// on both sides.
	//
	// ⚠️ Reached by assertion rather than through contract.StateDB, which
	// exposes AddBalance, GetBalance and SubBalanceMultiCoin but not
	// SubBalance. The concrete state has always had it - only the interface is
	// narrower - and asserting keeps this proposal from touching coreth at all,
	// which is a property worth more than the four lines it would have cost to
	// widen the interface.
	debitor, ok := stateDB.(balanceDebitor)
	if !ok {
		return nil, remainingGas, fmt.Errorf("%w: %T", ErrCannotDebit, stateDB)
	}

	value := stateDB.GetBalance(addr)
	amount, err := toNAVAX(value)
	if err != nil {
		return nil, remainingGas, err
	}
	debitor.SubBalance(addr, value)

	index := nextOutputIndex(stateDB)

	owner := warpfx.Owner{
		SourceChainID: accessibleState.GetSnowContext().ChainID,
		SourceAddress: caller.Bytes(),
	}
	data, err := packExport(owner, amount, index)
	if err != nil {
		return nil, remainingGas, err
	}

	stateDB.AddLog(&ethtypes.Log{
		Address:     ContractAddress,
		Topics:      []common.Hash{EventTopic, common.BytesToHash(caller.Bytes())},
		Data:        data,
		BlockNumber: accessibleState.GetBlockContext().Number().Uint64(),
	})
	return nil, remainingGas, nil
}

// toNAVAX converts an aAVAX value, refusing anything it cannot represent
// exactly.
//
// A truncation would silently lose up to one nAVAX per call. Derisory, but a
// silent loss on a value-transfer path is precisely what nobody wants to have
// to explain later; refusing is visible and the caller can correct it.
func toNAVAX(value *uint256.Int) (uint64, error) {
	if value.IsZero() {
		return 0, ErrNoValue
	}

	var (
		rate      = uint256.NewInt(X2CRate)
		quotient  = new(uint256.Int)
		remainder = new(uint256.Int)
	)
	quotient.DivMod(value, rate, remainder)
	if !remainder.IsZero() {
		return 0, fmt.Errorf("%w: %s", ErrValueNotWholeNAVAX, value)
	}
	if !quotient.IsUint64() {
		return 0, fmt.Errorf("%w: %s", ErrValueTooLarge, value)
	}
	return quotient.Uint64(), nil
}

// nextOutputIndex returns this transaction's next export index, resetting
// whenever the transaction changes.
//
// There is deliberately no nonce here. Export.Input.Nonce exists because an
// atomic transaction is a standalone object outside the EVM with its own
// mempool, so it needs its own replay protection; a precompile call lives in an
// EVM transaction that already has one. Using a contract's nonce for uniqueness
// would also be wrong - it only advances on CREATE, so successive calls would
// collide - and incrementing it as a side effect would shift the contract's
// CREATE addresses, breaking everything that predicts them.
func nextOutputIndex(stateDB counterState) uint32 {
	var (
		txHash = stateDB.TxHash()
		stored = stateDB.GetState(ContractAddress, counterSlot)
	)

	var (
		prefix [28]byte
		count  uint32
	)
	copy(prefix[:], stored[:28])
	count = uint32(stored[28])<<24 | uint32(stored[29])<<16 | uint32(stored[30])<<8 | uint32(stored[31])

	var want [28]byte
	copy(want[:], txHash[:28])
	if prefix != want {
		count = 0
	}

	next := count + 1
	var packed common.Hash
	copy(packed[:28], want[:])
	packed[28] = byte(next >> 24)
	packed[29] = byte(next >> 16)
	packed[30] = byte(next >> 8)
	packed[31] = byte(next)
	stateDB.SetState(ContractAddress, counterSlot, packed)

	return count
}

// packExport lays out the log data: the owner's chain, its address, the amount
// and the output index, in a form FromReceipts decodes without ambiguity.
func packExport(owner warpfx.Owner, amount uint64, index uint32) ([]byte, error) {
	if len(owner.SourceAddress) != common.AddressLength {
		return nil, fmt.Errorf("owner address is %d bytes", len(owner.SourceAddress))
	}

	data := make([]byte, 0, 32+20+8+4)
	data = append(data, owner.SourceChainID[:]...)
	data = append(data, owner.SourceAddress...)
	data = appendUint64(data, amount)
	return appendUint32(data, index), nil
}

func appendUint64(b []byte, v uint64) []byte {
	return append(b,
		byte(v>>56), byte(v>>48), byte(v>>40), byte(v>>32),
		byte(v>>24), byte(v>>16), byte(v>>8), byte(v),
	)
}

func appendUint32(b []byte, v uint32) []byte {
	return append(b, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

// DestinationChain is forced, never taken from the caller.
var DestinationChain = constants.PlatformChainID
