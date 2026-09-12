// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package nativeexport

import (
	"errors"
	"fmt"

	"github.com/ava-labs/libevm/core/types"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/vms/components/avax"
	"github.com/ava-labs/avalanchego/vms/saevm/cchain/tx"
	"github.com/ava-labs/avalanchego/vms/warpfx"

	chainsatomic "github.com/ava-labs/avalanchego/chains/atomic"
)

var (
	ErrMalformedLog = errors.New("malformed export log")

	// logDataLen is chainID(32) + address(20) + amount(8) + index(4).
	logDataLen = 64
)

// FromReceipts turns this block's export logs into shared-memory requests.
//
// The precompile deposits nothing itself. It emits a log, and the block
// plumbing derives the operations - which is what makes revert safe without any
// new structure. AddLog is journaled and reverting a frame erases its logs, so
// a failed transaction, included with status 0 and having burned its gas, has
// had its logs rewound before the receipt was built. Any side channel around
// the journal would instead leak a deposit from a cancelled call.
//
// The rule, stated plainly: a block's shared-memory operations from this
// precompile are derived from its logs, and from them alone.
//
// ⚠️ The UTXOID's TxID is the EVM transaction's hash, so this namespace is now
// shared with atomic transaction IDs. A collision is cryptographically
// negligible, but the sharing is worth knowing about.
func FromReceipts(receipts types.Receipts, avaxAssetID ids.ID) (map[ids.ID]*chainsatomic.Requests, error) {
	var elems []*chainsatomic.Element
	for _, r := range receipts {
		for _, log := range r.Logs {
			if log.Address != ContractAddress {
				continue
			}
			if len(log.Topics) == 0 || log.Topics[0] != EventTopic {
				continue
			}

			owner, amount, index, err := unpackExport(log.Data)
			if err != nil {
				return nil, fmt.Errorf(
					"parsing export log (TxHash: %s, LogIndex: %d): %w",
					log.TxHash, log.Index, err,
				)
			}

			utxo := &avax.UTXO{
				UTXOID: avax.UTXOID{
					TxID:        ids.ID(log.TxHash),
					OutputIndex: index,
				},
				Asset: avax.Asset{ID: avaxAssetID},
				Out:   &warpfx.TransferOutput{Amt: amount, Owner: owner},
			}
			if err := utxo.Out.(*warpfx.TransferOutput).Verify(); err != nil {
				return nil, fmt.Errorf("verifying exported utxo: %w", err)
			}

			utxoBytes, err := tx.MarshalUTXO(utxo)
			if err != nil {
				return nil, fmt.Errorf("marshalling exported utxo: %w", err)
			}

			utxoID := utxo.InputID()
			elem := &chainsatomic.Element{
				Key:   utxoID[:],
				Value: utxoBytes,
			}
			if addressable, ok := utxo.Out.(avax.Addressable); ok {
				elem.Traits = addressable.Addresses()
			}
			elems = append(elems, elem)
		}
	}

	if len(elems) == 0 {
		return nil, nil
	}
	// The destination is forced, never read from the log: nothing an EVM caller
	// writes decides where the funds land.
	return map[ids.ID]*chainsatomic.Requests{
		DestinationChain: {PutRequests: elems},
	}, nil
}

func unpackExport(data []byte) (warpfx.Owner, uint64, uint32, error) {
	if len(data) != logDataLen {
		return warpfx.Owner{}, 0, 0, fmt.Errorf(
			"%w: %d bytes, want %d", ErrMalformedLog, len(data), logDataLen,
		)
	}

	owner := warpfx.Owner{
		SourceChainID: ids.ID(data[:32]),
		SourceAddress: data[32:52],
	}
	amount := uint64(data[52])<<56 | uint64(data[53])<<48 | uint64(data[54])<<40 |
		uint64(data[55])<<32 | uint64(data[56])<<24 | uint64(data[57])<<16 |
		uint64(data[58])<<8 | uint64(data[59])
	index := uint32(data[60])<<24 | uint32(data[61])<<16 | uint32(data[62])<<8 | uint32(data[63])
	return owner, amount, index, nil
}
