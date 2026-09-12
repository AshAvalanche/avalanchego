// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package p

import (
	"context"
	"math/big"
	"time"

	"github.com/ava-labs/libevm/common"
	"github.com/ava-labs/libevm/core/types"
	"github.com/ava-labs/libevm/params"
	"github.com/holiman/uint256"
	"github.com/onsi/ginkgo/v2"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/ava-labs/avalanchego/api/info"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/network/peer"
	"github.com/ava-labs/avalanchego/snow/networking/router"
	"github.com/ava-labs/avalanchego/snow/validators"
	"github.com/ava-labs/avalanchego/tests/fixture/e2e"
	"github.com/ava-labs/avalanchego/tests/fixture/tmpnet"
	"github.com/ava-labs/avalanchego/upgrade"
	"github.com/ava-labs/avalanchego/utils/buffer"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/crypto/bls"
	"github.com/ava-labs/avalanchego/utils/crypto/secp256k1"
	"github.com/ava-labs/avalanchego/utils/set"
	"github.com/ava-labs/avalanchego/utils/units"
	"github.com/ava-labs/avalanchego/vms/components/avax"
	"github.com/ava-labs/avalanchego/vms/components/verify"
	"github.com/ava-labs/avalanchego/vms/platformvm"
	"github.com/ava-labs/avalanchego/vms/platformvm/platform"
	"github.com/ava-labs/avalanchego/vms/platformvm/status"
	"github.com/ava-labs/avalanchego/vms/platformvm/warp/message"
	"github.com/ava-labs/avalanchego/vms/saevm/cchain"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"
	"github.com/ava-labs/avalanchego/vms/warpfx"

	warpcontract "github.com/ava-labs/avalanchego/graft/coreth/precompile/contracts/warp"
	p2pmessage "github.com/ava-labs/avalanchego/message"
	txexecutor "github.com/ava-labs/avalanchego/vms/platformvm/txs/executor"
	txfee "github.com/ava-labs/avalanchego/vms/platformvm/txs/fee"
	avalanchewarp "github.com/ava-labs/avalanchego/vms/platformvm/warp"
	saevmtx "github.com/ava-labs/avalanchego/vms/saevm/cchain/tx"
)

// Warp UTXOs, phase A: a C-Chain address owns AVAX on the P-Chain.
//
// This is the plumbing the rest of the proposal stands on, and the three things
// it proves are things no single-VM test can:
//
//   - the codecs of saevm and the PlatformVM agree, so a UTXO written by one is
//     readable by the other. A mismatch is silent from the exporting side,
//     which has already debited;
//   - the discovery trait is laid, so the UTXO can be found. Without it the
//     UTXO is deposited and spendable but invisible;
//   - the VM transition happened, and a UTXO deposited by a post-Helicon
//     P-Chain is consumable.
//
// No Warp message passes here, deliberately: this isolates the cross-chain
// plumbing from the cryptography that comes on top of it.
var _ = e2e.DescribePChain("[Warp UTXOs]", func() {
	tc := e2e.NewTestContext()
	require := require.New(tc)

	const (
		// Exported to the warp owner, in nAVAX.
		exportedAmount = 10 * units.Avax
		// Burned by the export. On saevm this is not a fee but a bid: it
		// becomes the offered gas price, and the fee mechanism decides
		// inclusion from it.
		exportBid = units.MilliAvax
	)

	ginkgo.It("should let a C-Chain address own AVAX on the P-Chain", func() {
		env := e2e.GetEnv(tc)
		nodeURI := env.GetRandomNodeURI()

		infoClient := info.NewClient(nodeURI.URI)

		upgrades, err := infoClient.Upgrades(tc.DefaultContext())
		require.NoError(err)
		if upgrades.HeliconTime.Equal(upgrade.UnscheduledActivationTime) {
			ginkgo.Skip("skipping test because Helicon isn't scheduled")
		}

		// Only *one* assertion of this spec needs to precede the transition:
		// that coreth cannot parse a warpfx export. Whichever warp spec runs
		// first activates Helicon, so that assertion is skipped rather than the
		// whole spec - the round trip below is worth running either way.
		//
		// Run this spec alone to be sure of exercising it.
		canObserveTransition := !upgrades.IsHeliconActivated(time.Now())
		if !canObserveTransition {
			tc.Log().Warn("Helicon is already active; skipping the pre-transition assertion",
				zap.Time("heliconTime", upgrades.HeliconTime),
			)
		}

		var (
			cClient   = cchain.NewClient(nodeURI.URI)
			pClient   = platformvm.NewClient(nodeURI.URI)
			ethClient = e2e.NewEthClient(tc, nodeURI)

			senderKey        = env.PreFundedKey
			senderEthAddress = senderKey.EthAddress()
		)

		tc.By("gathering chain identifiers")
		cChainID, err := infoClient.GetBlockchainID(tc.DefaultContext(), "C")
		require.NoError(err)

		wallet := e2e.NewWallet(tc, secp256k1fx.NewKeychain(senderKey), nodeURI)
		pContext := wallet.P().Builder().Context()
		avaxAssetID := pContext.AVAXAssetID

		// The owner is a *third-party* address, which is the point: funding a
		// warp owner asks nothing of the owner itself. An EOA signs an ordinary
		// atomic export naming it, and that is the whole C-Chain side.
		ownerKey := e2e.NewPrivateKey(tc)
		ownerAddress := ownerKey.EthAddress()
		owner := warpfx.Owner{
			SourceChainID: cChainID,
			SourceAddress: ownerAddress[:],
		}
		tc.Log().Info("using a third-party warp owner",
			zap.Stringer("address", ownerAddress),
		)

		// The C-Chain only transitions when it *accepts a block* whose timestamp
		// is at or after the transition time (transitionvm/vm_block.go:151). An
		// idle chain therefore never switches, whatever the clock says, so the
		// test has to give it traffic - once before, so coreth has built
		// something, and once after, to produce the transition block itself.
		sendEth := func(from *secp256k1.PrivateKey, to common.Address, amount *big.Int) {
			fromAddress := from.EthAddress()
			gasPrice := e2e.SuggestGasPrice(tc, ethClient)
			nonce := nextNonce(tc, ethClient, fromAddress)

			chainID, err := ethClient.ChainID(tc.DefaultContext())
			require.NoError(err)

			signedTx, err := types.SignTx(
				types.NewTransaction(nonce, to, amount, e2e.DefaultGasLimit, gasPrice, nil),
				types.NewEIP155Signer(chainID),
				from.ToECDSA(),
			)
			require.NoError(err)

			receipt := e2e.SendEthTransaction(tc, ethClient, signedTx)
			require.Equal(types.ReceiptStatusSuccessful, receipt.Status)
		}

		// The transition block is produced by a separate key, so that the
		// export's nonce - fixed when it is signed, below - is not moved by the
		// traffic that triggers the transition.
		nudgeKey := e2e.NewPrivateKey(tc)

		tc.By("making coreth build a block before the transition", func() {
			sendEth(senderKey, nudgeKey.EthAddress(), big.NewInt(int64(units.Avax)))
		})

		var (
			exportTxID ids.ID
			exportTx   *saevmtx.Tx
		)
		tc.By("building the export from the C-Chain to a warp owner on the P-Chain", func() {
			nonce := nextNonce(tc, ethClient, senderEthAddress)

			export := &saevmtx.Export{
				NetworkID:        pContext.NetworkID,
				BlockchainID:     cChainID,
				DestinationChain: constants.PlatformChainID,
				Ins: []saevmtx.Input{{
					Address: senderEthAddress,
					Amount:  exportedAmount + exportBid,
					AssetID: avaxAssetID,
					Nonce:   nonce,
				}},
				ExportedOutputs: []*avax.TransferableOutput{{
					Asset: avax.Asset{ID: avaxAssetID},
					Out: &warpfx.TransferOutput{
						Amt:   exportedAmount,
						Owner: owner,
					},
				}},
			}

			// Signed by whoever holds the debited balance, exactly as any other
			// export: nothing on the C-Chain side changes for a warpfx output.
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
			exportTxID = exportTx.ID()
		})

		if canObserveTransition {
			tc.By("checking coreth cannot even parse it before the transition", func() {
				// The refusal is structural, not conditional:
				// warpfx.TransferOutput is not registered in coreth's atomic
				// codec, so coreth can neither build nor parse an export
				// carrying it. That is why this proposal needs no activation
				// guard on the C-Chain side - and this is the only place the
				// claim is checked rather than reasoned.
				err := cClient.IssueTx(tc.DefaultContext(), exportTx)
				require.ErrorContains(err, "unknown type ID") //nolint:forbidigo // the error crosses a JSON-RPC boundary as a string
				tc.Log().Info("coreth refused the warpfx export, as it must",
					zap.Error(err),
				)
			})
		}

		requireHeliconActivated(tc, infoClient)

		tc.By("producing the transition block", func() {
			sendEth(nudgeKey, common.Address(ids.GenerateTestShortID()), big.NewInt(1))
		})

		tc.By("waiting for the C-Chain to become saevm, then exporting", func() {
			// The same bytes, refused a moment ago and accepted now: the
			// transition is what changed, and nothing else.
			tc.Eventually(func() bool {
				err := cClient.IssueTx(tc.DefaultContext(), exportTx)
				if err == nil {
					return true
				}
				tc.Log().Info("export not yet accepted", zap.Error(err))
				return false
			}, e2e.DefaultTimeout, e2e.DefaultPollingInterval, "saevm did not accept the export before timeout")

			tc.Eventually(func() bool {
				_, _, err := cClient.GetTx(tc.DefaultContext(), exportTxID)
				return err == nil
			}, e2e.DefaultTimeout, e2e.DefaultPollingInterval, "export was not accepted before timeout")
		})

		var utxos []*avax.UTXO
		tc.By("finding the UTXO by the owner's source address", func() {
			// The trait. GetAtomicUTXOs indexes on twenty raw bytes, which is
			// exactly what warpfx.TransferOutput.Addresses returns - so every
			// discovery accessor in the repo serves a warp owner unchanged. If
			// this comes back empty while the export succeeded, the fault is
			// Addresses(), not the API and not shared memory.
			tc.Eventually(func() bool {
				utxoBytes, _, _, err := pClient.GetAtomicUTXOs(
					tc.DefaultContext(),
					[]ids.ShortID{ids.ShortID(ownerAddress)},
					"C",
					100,
					ids.ShortEmpty,
					ids.Empty,
				)
				require.NoError(err)
				if len(utxoBytes) == 0 {
					return false
				}

				// And the codec alignment: bytes written by saevm, read by the
				// PlatformVM. A mismatch here would have been invisible to the
				// export, which already debited.
				utxos = make([]*avax.UTXO, len(utxoBytes))
				for i, b := range utxoBytes {
					utxo := &avax.UTXO{}
					_, err := platform.Codec.Unmarshal(b, utxo)
					require.NoError(err)
					utxos[i] = utxo
				}
				return true
			}, e2e.DefaultTimeout, e2e.DefaultPollingInterval, "exported UTXO was not discoverable before timeout")

			require.Len(utxos, 1)
			require.Equal(&warpfx.TransferOutput{
				Amt:   exportedAmount,
				Owner: owner,
			}, utxos[0].Out)
		})

		var (
			importTxID    ids.ID
			importTxBytes []byte
		)
		tc.By("importing it with a canonical transaction, which nobody signs", func() {
			// Canonical: no P-Chain inputs, every imported UTXO held by one
			// warp owner, an empty credential per input, and exactly one output
			// back to that same owner. Its whole content is derived from the
			// UTXOs it consumes, so anyone can rebuild it.
			imp := &platform.ImportTx{
				BaseTx: platform.BaseTx{BaseTx: avax.BaseTx{
					NetworkID:    pContext.NetworkID,
					BlockchainID: constants.PlatformChainID,
				}},
				SourceChain: cChainID,
			}
			creds := make([]verify.Verifiable, len(utxos))
			consumed := uint64(0)
			for i, utxo := range utxos {
				out := utxo.Out.(*warpfx.TransferOutput)
				imp.ImportedInputs = append(imp.ImportedInputs, &avax.TransferableInput{
					UTXOID: utxo.UTXOID,
					Asset:  utxo.Asset,
					In:     &secp256k1fx.TransferInput{Amt: out.Amt},
				})
				// On the P-Chain the empty credential is a warpfx one. On saevm
				// it is a secp256k1fx one with no signatures - the encoding the
				// atomic codec agrees on. Both are right at home.
				creds[i] = &warpfx.Credential{}
				consumed += out.Amt
			}

			importTx := &platform.Tx{Unsigned: imp, Creds: creds}

			// There is no circularity in pricing the output we are about to
			// size: an amount is eight serialized bytes whatever its value, so
			// the fee does not move with it.
			calculator := txfee.NewDynamicCalculator(pContext.ComplexityWeights, pContext.GasPrice)
			imp.Outs = []*avax.TransferableOutput{{
				Asset: avax.Asset{ID: avaxAssetID},
				Out:   &warpfx.TransferOutput{Amt: consumed, Owner: owner},
			}}
			require.NoError(importTx.Initialize(platform.Codec))

			fee, err := calculator.CalculateFeeWithCredentials(importTx)
			require.NoError(err)
			require.Less(fee, consumed)

			imp.Outs[0].Out.(*warpfx.TransferOutput).Amt = consumed - fee
			imp.SyntacticallyVerified = false
			require.NoError(importTx.Initialize(platform.Codec))

			importTxBytes = importTx.Bytes()
			importTxID, err = pClient.IssueTx(tc.DefaultContext(), importTxBytes)
			require.NoError(err)

			tc.By("waiting for the canonical import to be committed", func() {
				tc.Eventually(func() bool {
					resp, err := pClient.GetTxStatus(tc.DefaultContext(), importTxID)
					require.NoError(err)
					return resp.Status == status.Committed
				}, e2e.DefaultTimeout, e2e.DefaultPollingInterval, "canonical import was not committed before timeout")
			})
		})

		tc.By("checking the funds are now a P-Chain UTXO of the same owner", func() {
			utxoBytes, _, _, err := pClient.GetUTXOs(
				tc.DefaultContext(),
				[]ids.ShortID{ids.ShortID(ownerAddress)},
				100,
				ids.ShortEmpty,
				ids.Empty,
			)
			require.NoError(err)
			require.Len(utxoBytes, 1)

			utxo := &avax.UTXO{}
			_, err = platform.Codec.Unmarshal(utxoBytes[0], utxo)
			require.NoError(err)

			out, ok := utxo.Out.(*warpfx.TransferOutput)
			require.True(ok)
			require.Equal(owner, out.Owner)
			require.Equal(importTxID, utxo.TxID)
			require.Positive(out.Amt)

			tc.Log().Info("a C-Chain address now owns AVAX on the P-Chain",
				zap.Stringer("address", ownerAddress),
				zap.Uint64("amount", out.Amt),
			)
		})

		tc.By("checking a second submitter of the same import fails cleanly", func() {
			// Submission is open to anyone and needs no funds, so several
			// submitters rebuilding the same canonical transaction is the
			// normal case rather than an incident. One of them wins; the others
			// find the shared-memory UTXO already consumed and fail without
			// consequence.
			//
			// Issued against a different node than the one that accepted it, so
			// the refusal comes from state rather than from a mempool cache.
			uris := env.GetNetwork().GetNodeURIs()
			require.Greater(len(uris), 1)

			var otherURI string
			for _, uri := range uris {
				if uri.URI != nodeURI.URI {
					otherURI = uri.URI
					break
				}
			}
			require.NotEmpty(otherURI)

			_, err := platformvm.NewClient(otherURI).IssueTx(tc.DefaultContext(), importTxBytes)
			require.Error(err) //nolint:forbidigo // the error crosses a JSON-RPC boundary as a string
			tc.Log().Info("the second submitter was refused, as it must be",
				zap.Error(err),
			)
		})

		// --- Phase B: the return leg, this time authorized ---------------
		//
		// Everything above needed no authorization, because receiving needs
		// none. Spending does, and this is where the Warp message earns its
		// place: the owner is a C-Chain address with no P-Chain key, so it
		// authorizes by committing to the exact bytes of the transaction.

		tc.By("funding the warp owner on the C-Chain so it can emit a message", func() {
			// It has to pay for its own EVM transaction. Note this is the first
			// thing asked of the owner at all - everything before this point
			// happened without it.
			sendEth(senderKey, ownerAddress, big.NewInt(params.Ether/10))
		})

		var (
			pChainUTXO  *avax.UTXO
			pExportTx   *platform.Tx
			exportedAmt uint64
			exportFee   = units.MilliAvax // generous; asserted below
		)
		tc.By("building the P-Chain export back to the C-Chain", func() {
			utxoBytes, _, _, err := pClient.GetUTXOs(
				tc.DefaultContext(),
				[]ids.ShortID{ids.ShortID(ownerAddress)},
				100,
				ids.ShortEmpty,
				ids.Empty,
			)
			require.NoError(err)
			require.Len(utxoBytes, 1)

			pChainUTXO = &avax.UTXO{}
			_, err = platform.Codec.Unmarshal(utxoBytes[0], pChainUTXO)
			require.NoError(err)

			held := pChainUTXO.Out.(*warpfx.TransferOutput).Amt
			require.Greater(held, exportFee)
			exportedAmt = held - exportFee

			pExportTx = &platform.Tx{
				Unsigned: &platform.ExportTx{
					BaseTx: platform.BaseTx{BaseTx: avax.BaseTx{
						NetworkID:    pContext.NetworkID,
						BlockchainID: constants.PlatformChainID,
						Ins: []*avax.TransferableInput{{
							UTXOID: pChainUTXO.UTXOID,
							Asset:  pChainUTXO.Asset,
							In:     &secp256k1fx.TransferInput{Amt: held},
						}},
					}},
					DestinationChain: cChainID,
					ExportedOutputs: []*avax.TransferableOutput{{
						Asset: avax.Asset{ID: avaxAssetID},
						Out: &warpfx.TransferOutput{
							Amt:   exportedAmt,
							Owner: owner,
						},
					}},
				},
				// One slot per input; the carrier is filled in once the message
				// is signed. The message commits to the unsigned bytes, so
				// attaching it cannot change them.
				Creds: []verify.Verifiable{&warpfx.Credential{}},
			}
			require.NoError(pExportTx.Initialize(platform.Codec))

			calculator := txfee.NewDynamicCalculator(pContext.ComplexityWeights, pContext.GasPrice)
			fee, err := calculator.CalculateFee(pExportTx.Unsigned)
			require.NoError(err)
			require.Less(fee, exportFee)
		})

		var unsignedWarpMsg *avalanchewarp.UnsignedMessage
		tc.By("authorizing it from the owner's C-Chain address", func() {
			// The payload carries the transaction's bytes, not a description of
			// it and not a digest: the submitter has to be able to rebuild the
			// transaction from what it reads in the log, and a digest would
			// need an out-of-band channel.
			authorization, err := message.NewTxAuthorization(
				uint64(time.Now().Add(time.Hour).Unix()),
				pExportTx.Unsigned.Bytes(),
			)
			require.NoError(err)

			// The precompile forces sourceAddress = caller and
			// sourceChainID = this chain, which is exactly the pair the warp
			// owner names. No contract is needed for that.
			input, err := warpcontract.PackSendWarpMessage(authorization.Bytes())
			require.NoError(err)

			gasPrice := e2e.SuggestGasPrice(tc, ethClient)
			nonce := nextNonce(tc, ethClient, ownerAddress)
			chainID, err := ethClient.ChainID(tc.DefaultContext())
			require.NoError(err)

			signedTx, err := types.SignTx(
				types.NewTransaction(
					nonce,
					warpcontract.ContractAddress,
					big.NewInt(0),
					1_000_000,
					gasPrice,
					input,
				),
				types.NewEIP155Signer(chainID),
				ownerKey.ToECDSA(),
			)
			require.NoError(err)

			receipt := e2e.SendEthTransaction(tc, ethClient, signedTx)
			require.Equal(types.ReceiptStatusSuccessful, receipt.Status)
			require.Len(receipt.Logs, 1)

			unsignedWarpMsg, err = warpcontract.UnpackSendWarpEventDataToMessage(receipt.Logs[0].Data)
			require.NoError(err)
			require.Equal(cChainID, unsignedWarpMsg.SourceChainID)
		})

		tc.By("gathering BLS signatures from the validators", func() {
			signedBytes := aggregateWarpSignatures(tc, env, unsignedWarpMsg)
			pExportTx.Creds[0] = &warpfx.Credential{WarpMessage: signedBytes}
			require.NoError(pExportTx.Initialize(platform.Codec))
		})

		tc.By("checking the authorization does not carry over to another transaction", func() {
			// A Warp message is public - it sits in a C-Chain log every relayer
			// watches - so without the commitment it would be a bearer token
			// over everything its owner holds.
			other := &platform.Tx{
				Unsigned: &platform.BaseTx{BaseTx: avax.BaseTx{
					NetworkID:    pContext.NetworkID,
					BlockchainID: constants.PlatformChainID,
					Ins: []*avax.TransferableInput{{
						UTXOID: pChainUTXO.UTXOID,
						Asset:  pChainUTXO.Asset,
						In:     &secp256k1fx.TransferInput{Amt: pChainUTXO.Out.(*warpfx.TransferOutput).Amt},
					}},
					Outs: []*avax.TransferableOutput{{
						Asset: avax.Asset{ID: avaxAssetID},
						Out: &warpfx.TransferOutput{
							Amt:   exportedAmt,
							Owner: owner,
						},
					}},
				}},
				Creds: []verify.Verifiable{pExportTx.Creds[0]},
			}
			require.NoError(other.Initialize(platform.Codec))

			_, err := pClient.IssueTx(tc.DefaultContext(), other.Bytes())
			require.ErrorContains(err, "does not commit to this transaction") //nolint:forbidigo // the error crosses a JSON-RPC boundary as a string
			tc.Log().Info("the replayed authorization was refused, as it must be",
				zap.Error(err),
			)
		})

		var pExportTxID ids.ID
		tc.By("submitting the authorized export", func() {
			pExportTxID, err = pClient.IssueTx(tc.DefaultContext(), pExportTx.Bytes())
			require.NoError(err)

			tc.Eventually(func() bool {
				resp, err := pClient.GetTxStatus(tc.DefaultContext(), pExportTxID)
				require.NoError(err)
				return resp.Status == status.Committed
			}, e2e.DefaultTimeout, e2e.DefaultPollingInterval, "authorized export was not committed before timeout")
		})

		tc.By("importing it back onto the C-Chain, canonically", func() {
			startingBalance, err := ethClient.BalanceAt(tc.DefaultContext(), ownerAddress, nil)
			require.NoError(err)

			var utxo *avax.UTXO
			tc.Eventually(func() bool {
				utxos, _, _, err := cClient.GetUTXOs(
					tc.DefaultContext(),
					[]ids.ShortID{ids.ShortID(ownerAddress)},
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
			}, e2e.DefaultTimeout, e2e.DefaultPollingInterval, "exported UTXO did not reach the C-Chain before timeout")

			out, ok := utxo.Out.(*warpfx.TransferOutput)
			require.True(ok)
			require.Equal(owner, out.Owner)

			// Here the burned amount is not a fee but a bid: it becomes the
			// offered gas price. It must clear the base fee, and the builder
			// refuses more than k times it, so aim between the two.
			header, err := ethClient.HeaderByNumber(tc.DefaultContext(), nil)
			require.NoError(err)
			baseFee := uint256.MustFromBig(header.BaseFee)

			newImport := func(burn uint64) *saevmtx.Tx {
				return &saevmtx.Tx{
					Unsigned: &saevmtx.Import{
						NetworkID:    pContext.NetworkID,
						BlockchainID: cChainID,
						SourceChain:  constants.PlatformChainID,
						ImportedInputs: []*avax.TransferableInput{{
							UTXOID: utxo.UTXOID,
							Asset:  utxo.Asset,
							In:     &secp256k1fx.TransferInput{Amt: out.Amt},
						}},
						Outs: []saevmtx.Output{{
							Address: ownerAddress,
							Amount:  out.Amt - burn,
							AssetID: avaxAssetID,
						}},
					},
					// On saevm the empty credential is a secp256k1fx one with no
					// signatures - the encoding the atomic codec agrees on. On
					// the P-Chain it was a warpfx one.
					Creds: []saevmtx.Credential{&secp256k1fx.Credential{}},
				}
			}

			// The gas does not move with the amount, so one pass suffices to
			// size the bid.
			probe, err := newImport(1).AsOp(avaxAssetID)
			require.NoError(err)

			// Burns are quantized at one nAVAX, which over this transaction's
			// gas is already ~1e9/gas aAVAX/gas - four to five orders of
			// magnitude above the ACP-283 floor base fee of one wei. So the
			// smallest expressible burn is both the cheapest bid available and,
			// on an idle chain, far above k times the base fee: the ceiling has
			// to be floored at it or nothing is includable.
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
			tc.Log().Info("canonical import bid",
				zap.Uint64("burnedNAVAX", burn.Uint64()),
				zap.Stringer("bid", &op.GasFeeCap),
				zap.Stringer("baseFee", baseFee),
			)

			require.NoError(cClient.IssueTx(tc.DefaultContext(), importTx))

			tc.By("checking the EVM balance grew, with no code executed", func() {
				// The credit is applied outside the EVM, like a selfdestruct:
				// no receive(), no fallback. A recipient contract has to notice,
				// it will not be notified.
				tc.Eventually(func() bool {
					balance, err := ethClient.BalanceAt(tc.DefaultContext(), ownerAddress, nil)
					require.NoError(err)
					return balance.Cmp(startingBalance) > 0
				}, e2e.DefaultTimeout, e2e.DefaultPollingInterval, "the owner's EVM balance was not credited before timeout")

				tc.Log().Info("the round trip is closed",
					zap.Stringer("address", ownerAddress),
				)
			})
		})
	})
})

// aggregateWarpSignatures collects an ACP-118 signature from every validator of
// the network and returns the signed Warp message.
//
// This is the submitter's job, and it needs no privilege and no funds: it reads
// the message from a C-Chain log, asks each validator to sign it, and
// aggregates. The signature request is a plain AppRequest addressed to the
// message's source chain, so it reaches that chain's ACP-118 handler - the same
// one ACP-77 uses.
//
// The bitset indexes the *canonical* validator set, which is ordered by
// uncompressed public key, so each signature has to be placed at its
// validator's rank rather than at the order the answers arrived in.
func aggregateWarpSignatures(
	tc *e2e.GinkgoTestContext,
	env *e2e.TestEnvironment,
	unsignedMsg *avalanchewarp.UnsignedMessage,
) []byte {
	require := require.New(tc)

	var (
		network   = env.GetNetwork()
		networkID = network.GetNetworkID()
		nodes     = network.Nodes
	)

	// The canonical set is read from the chain, never rebuilt from the nodes
	// this suite happens to have started.
	//
	// That distinction is not academic: any spec here may *add* a validator
	// - staking is what this proposal is for - and a set built from the
	// configured nodes would then be missing one. The bitset indexes the
	// canonical ordering, so a missing member shifts every index above it and
	// the aggregate signature verifies against nothing.
	pClient := platformvm.NewClient(network.GetNodeURIs()[0].URI)
	currentValidators, err := pClient.GetCurrentValidators(
		tc.DefaultContext(),
		constants.PrimaryNetworkID,
		nil, // all of them
	)
	require.NoError(err)

	vdrSet := make(map[ids.NodeID]*validators.GetValidatorOutput, len(currentValidators))
	for _, vdr := range currentValidators {
		require.NotNil(vdr.Signer, "validator %s registered no BLS key", vdr.NodeID)

		pk, err := bls.PublicKeyFromCompressedBytes(vdr.Signer.PublicKey[:])
		require.NoError(err)

		vdrSet[vdr.NodeID] = &validators.GetValidatorOutput{
			NodeID:    vdr.NodeID,
			PublicKey: pk,
			Weight:    vdr.Weight,
		}
	}
	canonical, err := validators.FlattenValidatorSet(vdrSet)
	require.NoError(err)

	indexOf := make(map[ids.NodeID]int, len(canonical.Validators))
	for i, vdr := range canonical.Validators {
		for _, nodeID := range vdr.NodeIDs {
			indexOf[nodeID] = i
		}
	}

	var (
		signatures   = make([]*bls.Signature, 0, len(nodes))
		signers      = set.NewBits()
		signedWeight uint64
	)
	for _, node := range nodes {
		index, ok := indexOf[node.NodeID]
		if !ok {
			// A node this suite started that is not a Primary Network
			// validator has no say in the quorum.
			continue
		}
		sig, ok := requestWarpSignature(tc, node, networkID, unsignedMsg)
		if !ok {
			tc.Log().Info("validator did not sign", zap.Stringer("nodeID", node.NodeID))
			continue
		}
		signatures = append(signatures, sig)
		signers.Add(index)
		signedWeight += canonical.Validators[index].Weight
	}

	// A quorum is 67% of the *weight*, not of the count - which is why the
	// weights are read from the chain above rather than assumed equal. A
	// validator this suite cannot reach (one it did not start, such as one a
	// staking spec just created) still counts against the total.
	require.GreaterOrEqual(100*signedWeight, uint64(txexecutor.WarpQuorumNumerator)*canonical.TotalWeight,
		"signed weight %d is short of the quorum over %d", signedWeight, canonical.TotalWeight)
	tc.Log().Info("aggregated warp signatures",
		zap.Int("signers", len(signatures)),
		zap.Uint64("signedWeight", signedWeight),
		zap.Uint64("totalWeight", canonical.TotalWeight),
	)

	aggregated, err := bls.AggregateSignatures(signatures)
	require.NoError(err)

	signedMsg, err := avalanchewarp.NewMessage(unsignedMsg, &avalanchewarp.BitSetSignature{
		Signers:   signers.Bytes(),
		Signature: [bls.SignatureLen]byte(bls.SignatureToBytes(aggregated)),
	})
	require.NoError(err)
	return signedMsg.Bytes()
}

// requestWarpSignature asks one node to sign [unsignedMsg] over ACP-118.
func requestWarpSignature(
	tc *e2e.GinkgoTestContext,
	node *tmpnet.Node,
	networkID uint32,
	unsignedMsg *avalanchewarp.UnsignedMessage,
) (*bls.Signature, bool) {
	require := require.New(tc)

	responses := buffer.NewUnboundedBlockingDeque[*p2pmessage.InboundMessage](1)
	stakingAddress, cancel, err := node.GetAccessibleStakingAddress(tc.DefaultContext())
	require.NoError(err)
	defer cancel()

	testPeer, err := peer.StartTestPeer(
		tc.DefaultContext(),
		stakingAddress,
		networkID,
		router.InboundHandlerFunc(func(_ context.Context, m *p2pmessage.InboundMessage) {
			responses.PushRight(m)
		}),
	)
	require.NoError(err)
	defer func() {
		testPeer.StartClose()
		require.NoError(testPeer.AwaitClosed(tc.DefaultContext()))
	}()

	request, err := wrapWarpSignatureRequest(unsignedMsg, nil)
	require.NoError(err)
	require.True(testPeer.Send(tc.DefaultContext(), request))

	var signature *bls.Signature
	tc.Eventually(func() bool {
		sig, ok, err := findMessage(responses, unwrapWarpSignature)
		if err != nil {
			tc.Log().Info("signature request refused",
				zap.Stringer("nodeID", node.NodeID),
				zap.Error(err),
			)
			return true
		}
		if !ok {
			return false
		}
		signature = sig
		return true
	}, e2e.DefaultTimeout, e2e.DefaultPollingInterval, "no answer to the signature request")

	return signature, signature != nil
}
