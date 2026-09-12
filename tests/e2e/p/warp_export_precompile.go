// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package p

import (
	"math/big"
	"strings"

	"github.com/ava-labs/libevm/accounts/abi"
	"github.com/ava-labs/libevm/common"
	"github.com/ava-labs/libevm/core/types"
	"github.com/ava-labs/libevm/crypto"
	"github.com/onsi/ginkgo/v2"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/ava-labs/avalanchego/api/info"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/tests/fixture/e2e"
	"github.com/ava-labs/avalanchego/upgrade"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/units"
	"github.com/ava-labs/avalanchego/vms/components/avax"
	"github.com/ava-labs/avalanchego/vms/components/verify"
	"github.com/ava-labs/avalanchego/vms/platformvm"
	"github.com/ava-labs/avalanchego/vms/platformvm/platform"
	"github.com/ava-labs/avalanchego/vms/saevm/cchain"
	"github.com/ava-labs/avalanchego/vms/saevm/cchain/precompile/nativeexport"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"
	"github.com/ava-labs/avalanchego/vms/warpfx"
)

// The leg no EOA can perform on a contract's behalf: moving one's *own* AVAX
// from the C-Chain to the P-Chain, and back.
//
// Funding a warp owner never needed the owner's participation - anyone may name
// it as an atomic export's recipient - which is how every other spec funds its
// contracts. What that path cannot express is an address deciding, by itself,
// to move the balance it already holds. A contract has no key to sign an atomic
// export with, and an atomic transaction lives outside any call frame, so there
// is nowhere for it to emit one from.
//
// Both callers run the same course, because the precompile does not distinguish
// them: it forces the owner to msg.sender. An EOA calling it becomes a warp
// owner exactly as a contract does - and its UTXO is warpfx, not secp256k1, so
// spending it back needs an authorization either way.
var _ = e2e.DescribePChain("[Warp Export Precompile]", func() {
	tc := e2e.NewTestContext()
	require := require.New(tc)

	const (
		endowment  = 3 * units.Avax
		exportedTo = units.Avax
		exportBid  = units.MilliAvax
		expiry     = uint64(1 << 62)
	)

	for _, caller := range []string{"a contract", "an EOA"} {
		byContract := caller == "a contract"

		ginkgo.It("should let "+caller+" send its own AVAX to the P-Chain and back", func() {
			env := e2e.GetEnv(tc)
			nodeURI := env.GetRandomNodeURI()
			infoClient := info.NewClient(nodeURI.URI)

			upgrades, err := infoClient.Upgrades(tc.DefaultContext())
			require.NoError(err)
			if upgrades.HeliconTime.Equal(upgrade.UnscheduledActivationTime) {
				ginkgo.Skip("skipping test because Helicon isn't scheduled")
			}

			var (
				cClient   = cchain.NewClient(nodeURI.URI)
				pClient   = platformvm.NewClient(nodeURI.URI)
				ethClient = e2e.NewEthClient(tc, nodeURI)
				senderKey = env.PreFundedKey
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
			nudgeKey := e2e.NewPrivateKey(tc)
			nudgeAddress := nudgeKey.EthAddress()

			// The EOA that owns the funds in the second case. A fresh key, so
			// its P-Chain UTXOs can only have come from the precompile.
			ownerKey := e2e.NewPrivateKey(tc)

			var (
				ownerAddress common.Address
				// authorize has the owner commit to [txBytes] and returns the
				// aggregated Warp message. The two callers reach the same
				// precompile by different doors: a contract method, or
				// sendWarpMessage directly.
				authorize func(txBytes []byte) []byte
			)

			tc.By("setting up "+caller+" with AVAX of its own", func() {
				evm.send(senderKey, &nudgeAddress, oneAvaxWei, nil, nil)

				if !byContract {
					ownerAddress = ownerKey.EthAddress()
					// Enough for the endowment plus the gas of its own calls.
					evm.send(senderKey, &ownerAddress, avaxToWei(endowment+units.Avax), nil, nil)
					authorize = func(txBytes []byte) []byte {
						return authorizeAs(tc, env, evm, ownerKey, expiry, txBytes)
					}
					return
				}

				ctorArgs, err := contractABI.Pack("", senderKey.EthAddress())
				require.NoError(err)
				receipt := evm.send(
					senderKey, nil, common.Big0,
					append(common.FromHex(warpOwnerCompiledContract), ctorArgs...), nil,
				)
				ownerAddress = receipt.ContractAddress
				require.NotEqual(common.Address{}, ownerAddress)

				// A contract cannot pay itself, so it is endowed. 100_000 of
				// gas because paying a contract runs its receive().
				evm.send(senderKey, &ownerAddress, avaxToWei(endowment), nil, nil, 100_000)

				authorize = func(txBytes []byte) []byte {
					data, err := contractABI.Pack("authorize", expiry, txBytes)
					require.NoError(err)
					return aggregateFromReceipt(tc, env, evm.send(senderKey, &ownerAddress, common.Big0, data, nil))
				}
			})

			balance, err := ethClient.BalanceAt(tc.DefaultContext(), ownerAddress, nil)
			require.NoError(err)
			require.GreaterOrEqual(balance.Cmp(avaxToWei(endowment)), 0)

			owner := warpfx.Owner{SourceChainID: cChainID, SourceAddress: ownerAddress[:]}

			requireHeliconActivated(tc, infoClient)

			tc.By("producing the transition block, so saevm takes over", func() {
				// The C-Chain switches when it *accepts* a block at or after
				// the threshold, not on the clock: an idle chain stays on
				// coreth indefinitely, and coreth knows nothing of this
				// precompile. A separate key nudges so no signed nonce moves.
				recipient := common.Address(ids.GenerateTestShortID())
				evm.send(nudgeKey, &recipient, common.Big1, nil, nil)

				// Activation is observable: an active precompile has code, and
				// Solidity's high-level call checks exactly that.
				tc.Eventually(func() bool {
					code, err := ethClient.CodeAt(tc.DefaultContext(), nativeexport.ContractAddress, nil)
					require.NoError(err)
					if len(code) > 0 {
						return true
					}
					evm.send(nudgeKey, &recipient, common.Big1, nil, nil)
					return false
				}, e2e.DefaultTimeout, e2e.DefaultPollingInterval, "the export precompile did not activate before timeout")
			})

			var exportTxHash common.Hash
			tc.By("exporting its own AVAX, with no atomic transaction anywhere", func() {
				receipt := func() *types.Receipt {
					if byContract {
						data, err := contractABI.Pack("exportToPChain", avaxToWei(exportedTo))
						require.NoError(err)
						return evm.send(senderKey, &ownerAddress, common.Big0, data, nil)
					}
					// An EOA calls the precompile directly: no argument, the
					// value carries everything.
					return evm.send(
						ownerKey, &nativeexport.ContractAddress,
						avaxToWei(exportedTo), exportAVAXInput(), nil, 200_000,
					)
				}()
				exportTxHash = receipt.TxHash

				after, err := ethClient.BalanceAt(tc.DefaultContext(), ownerAddress, nil)
				require.NoError(err)
				require.Negative(after.Cmp(balance), "the exported amount must leave the EVM's books")

				var logs int
				for _, l := range receipt.Logs {
					if l.Address == nativeexport.ContractAddress {
						logs++
					}
				}
				require.Equal(1, logs, "exactly one export log, which is the whole deposit")

				tc.Log().Info(caller+" exported its own AVAX",
					zap.Stringer("owner", ownerAddress),
					zap.Uint64("exported", exportedTo),
				)
			})

			var funded *avax.UTXO
			tc.By("importing it canonically, which nobody signs", func() {
				var atomicUTXOs [][]byte
				tc.Eventually(func() bool {
					atomicUTXOs, _, _, err = pClient.GetAtomicUTXOs(
						tc.DefaultContext(),
						[]ids.ShortID{ids.ShortID(ownerAddress)},
						"C", 100, ids.ShortEmpty, ids.Empty,
					)
					require.NoError(err)
					return len(atomicUTXOs) > 0
				}, e2e.DefaultTimeout, e2e.DefaultPollingInterval, "the exported UTXO was not discoverable before timeout")

				utxo := &avax.UTXO{}
				_, err = platform.Codec.Unmarshal(atomicUTXOs[0], utxo)
				require.NoError(err)

				// Keyed by the EVM transaction's hash: there is no atomic
				// transaction to take an ID from.
				require.Equal(ids.ID(exportTxHash), utxo.TxID)
				out, ok := utxo.Out.(*warpfx.TransferOutput)
				require.True(ok, "the precompile produces a warpfx output, even for an EOA")
				require.Equal(exportedTo, out.Amt)
				require.True(out.Owner.Equals(&owner), "the owner is forced to the caller")

				funded = issueCanonicalImport(tc, pClient, pContext, cChainID, owner, utxo)
				require.Equal(avaxAssetID, funded.AssetID())
				tc.Log().Info(caller+" now owns AVAX on the P-Chain, having sent it itself",
					zap.Uint64("amount", funded.Out.(*warpfx.TransferOutput).Amt),
				)
			})

			tc.By("bringing it home, which needs an authorization", func() {
				held := funded.Out.(*warpfx.TransferOutput).Amt
				require.Greater(held, exportBid)

				pExportTx := &platform.Tx{
					Unsigned: &platform.ExportTx{
						BaseTx: platform.BaseTx{BaseTx: avax.BaseTx{
							NetworkID:    pContext.NetworkID,
							BlockchainID: constants.PlatformChainID,
							Ins: []*avax.TransferableInput{{
								UTXOID: funded.UTXOID,
								Asset:  funded.Asset,
								In:     &secp256k1fx.TransferInput{Amt: held},
							}},
						}},
						DestinationChain: cChainID,
						ExportedOutputs: []*avax.TransferableOutput{{
							Asset: avax.Asset{ID: avaxAssetID},
							Out:   &warpfx.TransferOutput{Amt: held - exportBid, Owner: owner},
						}},
					},
					Creds: []verify.Verifiable{&warpfx.Credential{}},
				}
				require.NoError(pExportTx.Initialize(platform.Codec))

				pExportTx.Creds[0] = &warpfx.Credential{
					WarpMessage: authorize(pExportTx.Unsigned.Bytes()),
				}
				require.NoError(pExportTx.Initialize(platform.Codec))

				txID, err := pClient.IssueTx(tc.DefaultContext(), pExportTx.Bytes())
				require.NoError(err)
				awaitCommitted(tc, pClient, txID, "authorized export")

				before, err := ethClient.BalanceAt(tc.DefaultContext(), ownerAddress, nil)
				require.NoError(err)

				issueCanonicalCChainImport(tc, cClient, ethClient, pContext.NetworkID, cChainID, avaxAssetID, ownerAddress)

				// Credited outside the EVM, like a selfdestruct: no receive(),
				// no fallback. The holder has to notice, it is not notified.
				tc.Eventually(func() bool {
					now, err := ethClient.BalanceAt(tc.DefaultContext(), ownerAddress, nil)
					require.NoError(err)
					return now.Cmp(before) > 0
				}, e2e.DefaultTimeout, e2e.DefaultPollingInterval, "the balance was not credited before timeout")

				tc.Log().Info(caller+" closed the round trip through the precompile",
					zap.Stringer("owner", ownerAddress),
				)
			})
		})
	}
})

// exportAVAXInput is the precompile's whole calldata: a selector and nothing
// else, the value carrying the amount.
func exportAVAXInput() []byte {
	return crypto.Keccak256([]byte(nativeexport.ExportAVAXSignature))[:4]
}

// avaxToWei converts nAVAX to the C-Chain's aAVAX.
func avaxToWei(n uint64) *big.Int {
	return new(big.Int).Mul(new(big.Int).SetUint64(n), big.NewInt(nativeexport.X2CRate))
}
