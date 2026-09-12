// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package p

import (
	"math/big"
	"strings"
	"time"

	"github.com/ava-labs/libevm/accounts/abi"
	"github.com/ava-labs/libevm/common"
	"github.com/ava-labs/libevm/core/types"
	"github.com/holiman/uint256"
	"github.com/onsi/ginkgo/v2"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/ava-labs/avalanchego/api/info"
	"github.com/ava-labs/avalanchego/graft/coreth/ethclient"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/tests/fixture/e2e"
	"github.com/ava-labs/avalanchego/upgrade"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/crypto/bls/signer/localsigner"
	"github.com/ava-labs/avalanchego/utils/crypto/secp256k1"
	"github.com/ava-labs/avalanchego/utils/hashing"
	"github.com/ava-labs/avalanchego/utils/units"
	"github.com/ava-labs/avalanchego/vms/components/avax"
	"github.com/ava-labs/avalanchego/vms/components/verify"
	"github.com/ava-labs/avalanchego/vms/evm/predicate"
	"github.com/ava-labs/avalanchego/vms/platformvm"
	"github.com/ava-labs/avalanchego/vms/platformvm/platform"
	"github.com/ava-labs/avalanchego/vms/platformvm/reward"
	"github.com/ava-labs/avalanchego/vms/platformvm/signer"
	"github.com/ava-labs/avalanchego/vms/platformvm/status"
	"github.com/ava-labs/avalanchego/vms/saevm/cchain"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"
	"github.com/ava-labs/avalanchego/vms/warpfx"
	"github.com/ava-labs/avalanchego/wallet/chain/p/builder"

	warpcontract "github.com/ava-labs/avalanchego/graft/coreth/precompile/contracts/warp"
	txfee "github.com/ava-labs/avalanchego/vms/platformvm/txs/fee"
	avalanchewarp "github.com/ava-labs/avalanchego/vms/platformvm/warp"
	warpmessage "github.com/ava-labs/avalanchego/vms/platformvm/warp/message"
	warppayload "github.com/ava-labs/avalanchego/vms/platformvm/warp/payload"
	saevmtx "github.com/ava-labs/avalanchego/vms/saevm/cchain/tx"
	ethereum "github.com/ava-labs/libevm"
)

// Warp UTXOs driven by a contract rather than by an EOA.
//
// This is the case the proposal exists for, and it differs from the EOA spec in
// the one way that matters: the owner here has **no private key at all**. It is
// a C-Chain contract, its own address is the warp owner, and every P-Chain
// action it takes is authorized by a message it emits itself.
//
// In order: it is funded without lifting a finger, it imports, it stakes, it
// learns the txID of a transaction it could not have predicted, and it brings
// the rest of its funds home.
var _ = e2e.DescribePChain("[Warp UTXOs Contract]", func() {
	tc := e2e.NewTestContext()
	require := require.New(tc)

	const (
		// 2 KiloAvax is the local network's MinValidatorStake; the rest covers
		// fees and leaves change to bring home.
		stakeAmount  = 2 * units.KiloAvax
		exportedHome = 5 * units.Avax
		fundedAmount = stakeAmount + exportedHome + 10*units.Avax
		exportBid    = units.MilliAvax
		expiry       = uint64(1 << 62)
	)

	ginkgo.It("should let a contract own, stake and recover AVAX with no key", func() {
		env := e2e.GetEnv(tc)
		nodeURI := env.GetRandomNodeURI()
		infoClient := info.NewClient(nodeURI.URI)

		// Unlike the EOA spec, this one asserts nothing about the transition
		// itself, so it does not care whether Helicon is already active - it
		// only needs it to happen. That matters when both specs run in one
		// invocation: the first to run activates Helicon, and a spec that
		// insisted on starting before it would then skip.
		upgrades, err := infoClient.Upgrades(tc.DefaultContext())
		require.NoError(err)
		if upgrades.HeliconTime.Equal(upgrade.UnscheduledActivationTime) {
			ginkgo.Skip("skipping test because Helicon isn't scheduled")
		}

		var (
			cClient   = cchain.NewClient(nodeURI.URI)
			pClient   = platformvm.NewClient(nodeURI.URI)
			ethClient = e2e.NewEthClient(tc, nodeURI)

			senderKey        = env.PreFundedKey
			senderEthAddress = senderKey.EthAddress()
		)

		cChainID, err := infoClient.GetBlockchainID(tc.DefaultContext(), "C")
		require.NoError(err)
		evmChainID, err := ethClient.ChainID(tc.DefaultContext())
		require.NoError(err)

		wallet := e2e.NewWallet(tc, secp256k1fx.NewKeychain(senderKey), nodeURI)
		pContext := wallet.P().Builder().Context()
		avaxAssetID := pContext.AVAXAssetID

		contractABI, err := abi.JSON(strings.NewReader(warpOwnerABIJson))
		require.NoError(err)

		evm := &evmSender{tc: tc, client: ethClient, chainID: evmChainID}

		// A key whose only job is to make the chain produce blocks, so it never
		// moves the nonce of a transaction signed in advance.
		nudgeKey := e2e.NewPrivateKey(tc)
		nudgeAddress := nudgeKey.EthAddress()

		var contractAddress common.Address
		tc.By("deploying the WarpOwner contract", func() {
			evm.send(senderKey, &nudgeAddress, oneAvaxWei, nil, nil)

			ctorArgs, err := contractABI.Pack("", senderEthAddress)
			require.NoError(err)

			receipt := evm.send(
				senderKey,
				nil, // contract creation
				common.Big0,
				append(common.Hex2Bytes(warpOwnerCompiledContract), ctorArgs...),
				nil,
			)
			contractAddress = receipt.ContractAddress
			require.NotEqual(common.Address{}, contractAddress)

			// Deliberately left with a zero balance: the final assertion is
			// that a canonical import credits it, and starting from zero makes
			// that unambiguous.
			balance, err := ethClient.BalanceAt(tc.DefaultContext(), contractAddress, nil)
			require.NoError(err)
			require.Zero(balance.Sign())

			tc.Log().Info("deployed the warp owner contract",
				zap.Stringer("address", contractAddress),
			)
		})

		owner := warpfx.Owner{
			SourceChainID: cChainID,
			SourceAddress: contractAddress[:],
		}

		var exportTx *saevmtx.Tx
		tc.By("building the export that funds it, which asks nothing of it", func() {
			// An ordinary EOA-signed atomic export naming the contract. The
			// contract does not sign, does not run, and does not consent:
			// an authorization is needed to spend, never to receive.
			nonce := nextNonce(tc, ethClient, senderEthAddress)

			export := &saevmtx.Export{
				NetworkID:        pContext.NetworkID,
				BlockchainID:     cChainID,
				DestinationChain: constants.PlatformChainID,
				Ins: []saevmtx.Input{{
					Address: senderEthAddress,
					Amount:  fundedAmount + exportBid,
					AssetID: avaxAssetID,
					Nonce:   nonce,
				}},
				ExportedOutputs: []*avax.TransferableOutput{{
					Asset: avax.Asset{ID: avaxAssetID},
					Out:   &warpfx.TransferOutput{Amt: fundedAmount, Owner: owner},
				}},
			}
			unsignedBytes, err := saevmtx.UnsignedBytes(export)
			require.NoError(err)
			sig, err := senderKey.Sign(unsignedBytes)
			require.NoError(err)

			exportTx = &saevmtx.Tx{
				Unsigned: export,
				Creds: []saevmtx.Credential{&secp256k1fx.Credential{
					Sigs: [][secp256k1.SignatureLen]byte{[secp256k1.SignatureLen]byte(sig)},
				}},
			}
		})

		requireHeliconActivated(tc, infoClient)

		tc.By("producing the transition block, then exporting", func() {
			// The C-Chain only transitions when it accepts a block at or after
			// the transition time, so an idle chain never switches whatever the
			// clock says. Harmless if it already transitioned.
			//
			// The recipient is random rather than a constant like
			// common.Address{0x01}: 0x0100…00 onwards is the range coreth
			// reserves for precompiles, and a transfer there does not behave
			// like a transfer.
			recipient := common.Address(ids.GenerateTestShortID())
			evm.send(nudgeKey, &recipient, common.Big1, nil, nil)

			tc.Eventually(func() bool {
				return cClient.IssueTx(tc.DefaultContext(), exportTx) == nil
			}, e2e.DefaultTimeout, e2e.DefaultPollingInterval, "saevm did not accept the export before timeout")
		})

		var funded *avax.UTXO
		tc.By("importing canonically, which nobody signs", func() {
			var atomicUTXOs [][]byte
			tc.Eventually(func() bool {
				atomicUTXOs, _, _, err = pClient.GetAtomicUTXOs(
					tc.DefaultContext(),
					[]ids.ShortID{ids.ShortID(contractAddress)},
					"C",
					100,
					ids.ShortEmpty,
					ids.Empty,
				)
				require.NoError(err)
				return len(atomicUTXOs) > 0
			}, e2e.DefaultTimeout, e2e.DefaultPollingInterval, "the contract's UTXO was not discoverable before timeout")

			utxo := &avax.UTXO{}
			_, err = platform.Codec.Unmarshal(atomicUTXOs[0], utxo)
			require.NoError(err)

			funded = issueCanonicalImport(tc, pClient, pContext, cChainID, owner, utxo)
			tc.Log().Info("the contract owns AVAX on the P-Chain",
				zap.Uint64("amount", funded.Out.(*warpfx.TransferOutput).Amt),
			)
		})

		// --- The round trip, authorized by the contract -------------------
		//
		// Deliberately before the staking, which is the last quorum
		// operation this spec performs.
		//
		// The quorum is verified at a P-chain height that only advances when a
		// block is accepted, while the aggregator signs against the current
		// set. Right after an AddPermissionlessValidatorTx the two differ by a
		// member and the BLS bitset indices shift - and if the refused
		// transaction is the only thing pending, no block is accepted, so the
		// height never catches up. Retrying does not converge.

		var (
			pExportTx  *platform.Tx
			exportAuth ids.ID
			changeAmt  uint64
		)
		tc.By("having the contract authorize an export back to the C-Chain", func() {
			held := funded.Out.(*warpfx.TransferOutput).Amt
			require.Greater(held, stakeAmount+exportedHome+2*exportBid)
			changeAmt = held - exportedHome - exportBid

			// The contract commits to the exact unsigned bytes, so nobody can
			// alter the transaction afterwards - not the amount sent home, not
			// the change, not the destination.
			pExportTx = &platform.Tx{
				Unsigned: &platform.ExportTx{
					BaseTx: platform.BaseTx{BaseTx: avax.BaseTx{
						NetworkID:    pContext.NetworkID,
						BlockchainID: constants.PlatformChainID,
						Ins: []*avax.TransferableInput{{
							UTXOID: funded.UTXOID,
							Asset:  funded.Asset,
							In:     &secp256k1fx.TransferInput{Amt: held},
						}},
						Outs: []*avax.TransferableOutput{{
							Asset: avax.Asset{ID: avaxAssetID},
							Out:   &warpfx.TransferOutput{Amt: changeAmt, Owner: owner},
						}},
					}},
					DestinationChain: cChainID,
					ExportedOutputs: []*avax.TransferableOutput{{
						Asset: avax.Asset{ID: avaxAssetID},
						Out:   &warpfx.TransferOutput{Amt: exportedHome, Owner: owner},
					}},
				},
				Creds: []verify.Verifiable{&warpfx.Credential{}},
			}
			require.NoError(pExportTx.Initialize(platform.Codec))
			exportAuth = hashing.ComputeHash256Array(pExportTx.Unsigned.Bytes())

			data, err := contractABI.Pack("authorize", expiry, pExportTx.Unsigned.Bytes())
			require.NoError(err)

			receipt := evm.send(senderKey, &contractAddress, common.Big0, data, nil)
			pExportTx.Creds[0] = &warpfx.Credential{
				WarpMessage: aggregateFromReceipt(tc, env, receipt),
			}
			require.NoError(pExportTx.Initialize(platform.Codec))
		})

		tc.By("submitting the export, which nobody signed", func() {
			txID, err := pClient.IssueTx(tc.DefaultContext(), pExportTx.Bytes())
			require.NoError(err)
			awaitCommitted(tc, pClient, txID, "authorized export")

			issueCanonicalCChainImport(tc, cClient, ethClient, pContext.NetworkID, cChainID, avaxAssetID, contractAddress)

			// Applied outside the EVM, like a selfdestruct: receive() is not
			// called. A contract has to notice, it is not notified - which is
			// why its balance was left at zero until now.
			tc.Eventually(func() bool {
				balance, err := ethClient.BalanceAt(tc.DefaultContext(), contractAddress, nil)
				require.NoError(err)
				return balance.Sign() > 0
			}, e2e.DefaultTimeout, e2e.DefaultPollingInterval, "the contract's balance was not credited before timeout")

			tc.Log().Info("the contract's round trip is closed, with no code executed",
				zap.Stringer("contract", contractAddress),
				zap.Uint64("recovered", exportedHome),
			)
		})

		// --- The return leg: the contract learns its own txID -------------

		tc.By("delivering the TxExecuted attestation back to the contract", func() {
			// The txID is information the contract could not have had: it
			// commits to the *unsigned* bytes while the txID hashes the signed
			// ones, credential included. Without this attestation it cannot
			// name the UTXOs its own transaction just created - which is
			// exactly what the staking below needs.
			attestation, err := warpmessage.NewTxExecuted(pExportTx.ID(), exportAuth)
			require.NoError(err)

			signedBytes := signPChainAttestation(tc, env, pContext.NetworkID, attestation.Bytes())

			data, err := contractABI.Pack("recordExecution", uint32(0))
			require.NoError(err)

			// The predicate belongs to the *transaction*, not to the call, so
			// delivery is necessarily a top-level EVM transaction built by
			// whoever relays it - which is why recordExecution is callable by
			// anyone, and why the nudge key sends it here.
			receipt := evm.send(nudgeKey, &contractAddress, common.Big0, data, types.AccessList{{
				Address:     warpcontract.ContractAddress,
				StorageKeys: predicate.New(signedBytes),
			}})
			require.Equal(types.ReceiptStatusSuccessful, receipt.Status)

			// Polled, not read once: a receipt is available from the
			// *processed* block while CallContract reads the last *accepted*
			// one, so the write is briefly invisible to the read that follows
			// it - the same lag that makes AcceptedNonceAt unreliable above.
			tc.Eventually(func() bool {
				recorded := evm.call(contractAddress, contractABI, "executed", exportAuth)
				return ids.ID(recorded[0].([32]byte)) == pExportTx.ID()
			}, e2e.DefaultTimeout, e2e.DefaultPollingInterval, "the contract did not record the txID before timeout")

			tc.Log().Info("the contract recorded the txID it could not have predicted",
				zap.Stringer("txID", pExportTx.ID()),
			)
		})

		// --- Staking, authorized by the contract itself -------------------

		tc.By("staking the change, authorized by the contract", func() {
			// The change output of the export, which the contract can name
			// because it wrote it: index 0 of a txID it now holds.
			now := time.Now()
			stakingTx := &platform.Tx{
				Unsigned: &platform.AddPermissionlessValidatorTx{
					BaseTx: platform.BaseTx{BaseTx: avax.BaseTx{
						NetworkID:    pContext.NetworkID,
						BlockchainID: constants.PlatformChainID,
						Ins: []*avax.TransferableInput{{
							UTXOID: avax.UTXOID{TxID: pExportTx.ID(), OutputIndex: 0},
							Asset:  avax.Asset{ID: avaxAssetID},
							In:     &secp256k1fx.TransferInput{Amt: changeAmt},
						}},
						Outs: []*avax.TransferableOutput{{
							Asset: avax.Asset{ID: avaxAssetID},
							Out: &warpfx.TransferOutput{
								Amt:   changeAmt - stakeAmount - exportBid,
								Owner: owner,
							},
						}},
					}},
					Validator: platform.Validator{
						NodeID: ids.GenerateTestNodeID(),
						Start:  uint64(now.Unix()),
						End:    uint64(now.Add(stakingPeriod).Unix()),
						Wght:   stakeAmount,
					},
					Subnet: constants.PrimaryNetworkID,
					// A Primary Network validator must register a BLS key.
					Signer: newProofOfPossession(tc),
					StakeOuts: []*avax.TransferableOutput{{
						Asset: avax.Asset{ID: avaxAssetID},
						Out:   &warpfx.TransferOutput{Amt: stakeAmount, Owner: owner},
					}},
					// Both rewards owners are the contract. Months from now,
					// when this validator leaves, CreateOutput resolves on the
					// *type* of this owner to mint a warpfx output rather than
					// a secp one - the second dispatch site of the design.
					ValidatorRewardsOwner: &owner,
					DelegatorRewardsOwner: &owner,
					DelegationShares:      reward.PercentDenominator / 4,
				},
				Creds: []verify.Verifiable{&warpfx.Credential{}},
			}
			require.NoError(stakingTx.Initialize(platform.Codec))

			data, err := contractABI.Pack("authorize", expiry, stakingTx.Unsigned.Bytes())
			require.NoError(err)

			receipt := evm.send(senderKey, &contractAddress, common.Big0, data, nil)
			stakingTx.Creds[0] = &warpfx.Credential{
				WarpMessage: aggregateFromReceipt(tc, env, receipt),
			}
			require.NoError(stakingTx.Initialize(platform.Codec))

			txID, err := pClient.IssueTx(tc.DefaultContext(), stakingTx.Bytes())
			require.NoError(err)
			awaitCommitted(tc, pClient, txID, "staking transaction")

			tc.Log().Info("a contract is staking on the Primary Network, holding no key",
				zap.Stringer("txID", txID),
				zap.Stringer("contract", contractAddress),
				zap.Uint64("stake", stakeAmount),
			)
		})
	})
})

const (
	oneAvaxWeiStr = "1000000000000000000"
	stakingPeriod = 24 * time.Hour
)

var oneAvaxWei, _ = new(big.Int).SetString(oneAvaxWeiStr, 10)

// reservedNonces holds the next nonce each sender should use.
//
// AcceptedNonceAt reads the last *accepted* block, which trails the receipt
// a sender has already waited for. Two sends in a row can therefore be handed
// the same nonce, and the second is refused - "replacement transaction
// underpriced", the two carrying the same suggested price. Reserving locally
// removes the race; the accepted value only ever moves a reservation forward,
// which is how the nonces spent outside the EVM - by the atomic exports, which
// draw on the same account - are picked up.
//
// Shared across specs on purpose, since the same pre-funded key sends in all
// three, and they run serially in one process, so no lock is needed.
var reservedNonces = map[common.Address]uint64{}

func nextNonce(tc *e2e.GinkgoTestContext, client *ethclient.Client, addr common.Address) uint64 {
	accepted, err := client.AcceptedNonceAt(tc.DefaultContext(), addr)
	require.NoError(tc, err)

	nonce := max(accepted, reservedNonces[addr])
	reservedNonces[addr] = nonce + 1
	return nonce
}

// evmSender issues ordinary EVM transactions on behalf of a key.
type evmSender struct {
	tc      *e2e.GinkgoTestContext
	client  *ethclient.Client
	chainID *big.Int
}

func (e *evmSender) send(
	from *secp256k1.PrivateKey,
	to *common.Address,
	value *big.Int,
	data []byte,
	accessList types.AccessList,
	gasOverride ...uint64,
) *types.Receipt {
	require := require.New(e.tc)

	fromAddress := from.EthAddress()
	nonce := nextNonce(e.tc, e.client, fromAddress)

	// A plain value transfer needs the standard limit; a contract call needs
	// room, and unused gas is refunded either way. The override covers the case
	// in between: paying a contract runs its receive(), which 21000 does not.
	gasLimit := e2e.DefaultGasLimit
	if len(data) > 0 {
		gasLimit = 3_000_000
	}
	if len(gasOverride) > 0 {
		gasLimit = gasOverride[0]
	}
	gasPrice := e2e.SuggestGasPrice(e.tc, e.client)

	var inner types.TxData = &types.LegacyTx{
		Nonce:    nonce,
		To:       to,
		Value:    value,
		Gas:      gasLimit,
		GasPrice: gasPrice,
		Data:     data,
	}
	var txSigner types.Signer = types.NewEIP155Signer(e.chainID)
	if len(accessList) > 0 {
		inner = &types.AccessListTx{
			ChainID:    e.chainID,
			Nonce:      nonce,
			To:         to,
			Value:      value,
			Gas:        gasLimit,
			GasPrice:   gasPrice,
			Data:       data,
			AccessList: accessList,
		}
		txSigner = types.NewEIP2930Signer(e.chainID)
	}

	signedTx, err := types.SignTx(types.NewTx(inner), txSigner, from.ToECDSA())
	require.NoError(err)

	receipt := e2e.SendEthTransaction(e.tc, e.client, signedTx)
	require.Equal(types.ReceiptStatusSuccessful, receipt.Status)
	return receipt
}

// call makes a read-only call and unpacks the result.
func (e *evmSender) call(
	to common.Address,
	contractABI abi.ABI,
	method string,
	args ...interface{},
) []interface{} {
	require := require.New(e.tc)

	input, err := contractABI.Pack(method, args...)
	require.NoError(err)

	out, err := e.client.CallContract(e.tc.DefaultContext(), ethereum.CallMsg{
		To:   &to,
		Data: input,
	}, nil)
	require.NoError(err)

	values, err := contractABI.Unpack(method, out)
	require.NoError(err)
	return values
}

// aggregateFromReceipt pulls the unsigned Warp message out of the contract's
// SendWarpMessage log and gathers the validators' signatures over it.
//
// This is the submitter's whole job on the C-Chain side, and it needs no
// privilege: the message is in a public log.
func aggregateFromReceipt(
	tc *e2e.GinkgoTestContext,
	env *e2e.TestEnvironment,
	receipt *types.Receipt,
) []byte {
	require := require.New(tc)

	var unsignedMsg *avalanchewarp.UnsignedMessage
	for _, log := range receipt.Logs {
		if log.Address != warpcontract.ContractAddress {
			continue
		}
		msg, err := warpcontract.UnpackSendWarpEventDataToMessage(log.Data)
		require.NoError(err)
		unsignedMsg = msg
	}
	require.NotNil(unsignedMsg, "the contract emitted no warp message")

	return aggregateWarpSignatures(tc, env, unsignedMsg)
}

// signPChainAttestation asks the validators to sign an attestation the P-Chain
// derives from its own accepted state.
//
// Nothing is emitted for these: the handler recomputes the answer on request,
// which is why "no state added to the P-Chain" still holds. The AddressedCall
// carries no source address, and the source chain is the P-Chain - both are the
// meaningful zeroes a reading contract must expect rather than reject.
func signPChainAttestation(
	tc *e2e.GinkgoTestContext,
	env *e2e.TestEnvironment,
	networkID uint32,
	payload []byte,
) []byte {
	require := require.New(tc)

	call, err := warppayload.NewAddressedCall(nil, payload)
	require.NoError(err)

	unsignedMsg, err := avalanchewarp.NewUnsignedMessage(
		networkID,
		constants.PlatformChainID,
		call.Bytes(),
	)
	require.NoError(err)

	return aggregateWarpSignatures(tc, env, unsignedMsg)
}

func awaitCommitted(tc *e2e.GinkgoTestContext, client *platformvm.Client, txID ids.ID, what string) {
	tc.Eventually(func() bool {
		resp, err := client.GetTxStatus(tc.DefaultContext(), txID)
		require.NoError(tc, err)
		return resp.Status == status.Committed
	}, e2e.DefaultTimeout, e2e.DefaultPollingInterval, "the "+what+" was not committed before timeout")
}

// issueCanonicalImport builds, submits and awaits the canonical P-Chain import
// of [utxo], and returns the UTXO it produced.
//
// Nobody signs it: its whole content is derived from the UTXO it consumes, so
// anyone can rebuild it from what they read in shared memory.
func issueCanonicalImport(
	tc *e2e.GinkgoTestContext,
	client *platformvm.Client,
	pContext *builder.Context,
	sourceChainID ids.ID,
	owner warpfx.Owner,
	utxo *avax.UTXO,
) *avax.UTXO {
	require := require.New(tc)

	held := utxo.Out.(*warpfx.TransferOutput).Amt
	unsigned := &platform.ImportTx{
		BaseTx: platform.BaseTx{BaseTx: avax.BaseTx{
			NetworkID:    pContext.NetworkID,
			BlockchainID: constants.PlatformChainID,
		}},
		SourceChain: sourceChainID,
		ImportedInputs: []*avax.TransferableInput{{
			UTXOID: utxo.UTXOID,
			Asset:  utxo.Asset,
			In:     &secp256k1fx.TransferInput{Amt: held},
		}},
	}
	// On the P-Chain the empty credential is a warpfx one; on saevm it is a
	// secp256k1fx one with no signatures. Both are right at home.
	importTx := &platform.Tx{
		Unsigned: unsigned,
		Creds:    []verify.Verifiable{&warpfx.Credential{}},
	}

	// There is no circularity in pricing the output about to be sized: an
	// amount is eight serialized bytes whatever its value.
	calculator := txfee.NewDynamicCalculator(pContext.ComplexityWeights, pContext.GasPrice)
	unsigned.Outs = []*avax.TransferableOutput{{
		Asset: avax.Asset{ID: pContext.AVAXAssetID},
		Out:   &warpfx.TransferOutput{Amt: held, Owner: owner},
	}}
	require.NoError(importTx.Initialize(platform.Codec))

	fee, err := calculator.CalculateFeeWithCredentials(importTx)
	require.NoError(err)
	require.Less(fee, held)

	unsigned.Outs[0].Out.(*warpfx.TransferOutput).Amt = held - fee
	unsigned.SyntacticallyVerified = false
	require.NoError(importTx.Initialize(platform.Codec))

	txID, err := client.IssueTx(tc.DefaultContext(), importTx.Bytes())
	require.NoError(err)
	awaitCommitted(tc, client, txID, "canonical import")

	return &avax.UTXO{
		UTXOID: avax.UTXOID{TxID: txID, OutputIndex: 0},
		Asset:  avax.Asset{ID: pContext.AVAXAssetID},
		Out:    &warpfx.TransferOutput{Amt: held - fee, Owner: owner},
	}
}

// issueCanonicalCChainImport is the mirror on the C-Chain: it waits for the
// exported UTXO to appear, sizes a bid the builder will admit, and submits.
func issueCanonicalCChainImport(
	tc *e2e.GinkgoTestContext,
	cClient *cchain.Client,
	ethClient *ethclient.Client,
	networkID uint32,
	cChainID ids.ID,
	avaxAssetID ids.ID,
	recipient common.Address,
) {
	require := require.New(tc)

	var utxo *avax.UTXO
	tc.Eventually(func() bool {
		utxos, _, _, err := cClient.GetUTXOs(
			tc.DefaultContext(),
			[]ids.ShortID{ids.ShortID(recipient)},
			constants.PlatformChainID,
			100,
			ids.ShortEmpty,
			ids.Empty,
		)
		require.NoError(err)
		if len(utxos) == 0 {
			return false
		}
		utxo = utxos[0]
		return true
	}, e2e.DefaultTimeout, e2e.DefaultPollingInterval, "the exported UTXO did not reach the C-Chain before timeout")

	out, ok := utxo.Out.(*warpfx.TransferOutput)
	require.True(ok)

	newImport := func(burn uint64) *saevmtx.Tx {
		return &saevmtx.Tx{
			Unsigned: &saevmtx.Import{
				NetworkID:    networkID,
				BlockchainID: cChainID,
				SourceChain:  constants.PlatformChainID,
				ImportedInputs: []*avax.TransferableInput{{
					UTXOID: utxo.UTXOID,
					Asset:  utxo.Asset,
					In:     &secp256k1fx.TransferInput{Amt: out.Amt},
				}},
				Outs: []saevmtx.Output{{
					Address: recipient,
					Amount:  out.Amt - burn,
					AssetID: avaxAssetID,
				}},
			},
			Creds: []saevmtx.Credential{&secp256k1fx.Credential{}},
		}
	}

	// Here the burned amount is not a fee but a bid: it becomes the offered gas
	// price. It must clear the base fee, and the builder refuses more than k
	// times it - except that burns are quantized at one nAVAX while the base fee
	// is not, so the ceiling is floored at the smallest expressible bid.
	header, err := ethClient.HeaderByNumber(tc.DefaultContext(), nil)
	require.NoError(err)
	baseFee := uint256.MustFromBig(header.BaseFee)

	probe, err := newImport(1).AsOp(avaxAssetID)
	require.NoError(err)

	burn := new(uint256.Int).Mul(baseFee, uint256.NewInt(uint64(probe.Gas)))
	scale := saevmtx.ScaleAVAX(1)
	burn.Div(burn, &scale)
	burn.AddUint64(burn, 1) // round up, and never bid zero
	require.True(burn.IsUint64())

	importTx := newImport(burn.Uint64())
	op, err := importTx.AsOp(avaxAssetID)
	require.NoError(err)

	minimumBid := saevmtx.ScaleAVAX(1)
	minimumBid.Div(&minimumBid, uint256.NewInt(uint64(op.Gas)))
	require.NoError(warpfx.VerifyCanonicalBid(&op.GasFeeCap, baseFee, &minimumBid))

	require.NoError(cClient.IssueTx(tc.DefaultContext(), importTx))
}

// newProofOfPossession returns a BLS key registration for a fresh validator.
func newProofOfPossession(tc *e2e.GinkgoTestContext) *signer.ProofOfPossession {
	sk, err := localsigner.New()
	require.NoError(tc, err)

	pop, err := signer.NewProofOfPossession(sk)
	require.NoError(tc, err)
	return pop
}
