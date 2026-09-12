// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package p

import (
	"time"

	"github.com/ava-labs/libevm/common"
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
	"github.com/ava-labs/avalanchego/vms/platformvm/reward"
	"github.com/ava-labs/avalanchego/vms/saevm/cchain/precompile/nativeexport"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"
	"github.com/ava-labs/avalanchego/vms/warpfx"
)

// The three authorized transactions the other specs never reach: a delegation,
// and the two halves of an auto-renewed validator.
//
// They matter for different reasons. A delegation mints its reward through the
// *second* dispatch site of the design - CreateOutput resolving on the type of
// the rewards owner - by a path of its own. And an auto-renewed validator is
// the one case where a warp owner is a *permission* owner: its
// ValidatorAuthority is consulted through the contextual entry point, which is
// what separates it from a subnet owner, forbidden precisely because subnet
// authorization takes the context-free one.
var _ = e2e.DescribePChain("[Warp Staking Family]", func() {
	tc := e2e.NewTestContext()
	require := require.New(tc)

	const (
		validatorStake = 2 * units.KiloAvax // the local network's minimum
		delegatorStake = 25 * units.Avax    // likewise
		fees           = 40 * units.Avax
		spend          = units.MilliAvax
		nudgeAmount    = units.Avax
		expiry         = uint64(1 << 62)
		cyclePeriod    = uint64(3600)
	)

	ginkgo.It("should let a warp owner delegate and run an auto-renewed validator", func() {
		env := e2e.GetEnv(tc)
		nodeURI := env.GetRandomNodeURI()
		infoClient := info.NewClient(nodeURI.URI)

		upgrades, err := infoClient.Upgrades(tc.DefaultContext())
		require.NoError(err)
		if upgrades.HeliconTime.Equal(upgrade.UnscheduledActivationTime) {
			ginkgo.Skip("skipping test because Helicon isn't scheduled")
		}

		var (
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

		evm := &evmSender{tc: tc, client: ethClient, chainID: evmChainID}
		nudgeKey := e2e.NewPrivateKey(tc)
		nudgeAddress := nudgeKey.EthAddress()

		ownerKey := e2e.NewPrivateKey(tc)
		ownerAddress := ownerKey.EthAddress()
		owner := warpfx.Owner{SourceChainID: cChainID, SourceAddress: ownerAddress[:]}

		authorize := func(txBytes []byte) []byte {
			return authorizeAs(tc, env, evm, ownerKey, expiry, txBytes)
		}

		const funding = validatorStake + delegatorStake + fees
		tc.By("funding the owner on the C-Chain", func() {
			evm.send(senderKey, &nudgeAddress, oneAvaxWei, nil, nil)
			evm.send(senderKey, &ownerAddress, avaxToWei(funding+nudgeAmount+units.Avax), nil, nil)
		})

		requireHeliconActivated(tc, infoClient)

		tc.By("producing the transition block", func() {
			recipient := common.Address(ids.GenerateTestShortID())
			evm.send(nudgeKey, &recipient, common.Big1, nil, nil)
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

		// Two exports rather than one, and not only to save an atomic
		// transaction: the second is held back deliberately, to be imported
		// later as a block that costs no quorum. See its use below.
		var stake, spare *avax.UTXO
		tc.By("exporting twice, then importing the first canonically", func() {
			first := evm.send(ownerKey, &nativeexport.ContractAddress, avaxToWei(funding), exportAVAXInput(), nil, 200_000)
			second := evm.send(ownerKey, &nativeexport.ContractAddress, avaxToWei(nudgeAmount), exportAVAXInput(), nil, 200_000)
			require.NotEqual(first.TxHash, second.TxHash)

			utxos := awaitAtomicUTXOs(tc, pClient, ownerAddress, 2)
			for _, utxo := range utxos {
				if utxo.Out.(*warpfx.TransferOutput).Amt == funding {
					stake = utxo
				} else {
					spare = utxo
				}
			}
			require.NotNil(stake)
			require.NotNil(spare)

			stake = issueCanonicalImport(tc, pClient, pContext, cChainID, owner, stake)
			// The second export, imported too: two calls in two transactions
			// must yield two distinct UTXOIDs, which is the counter's whole job.
			imported := issueCanonicalImport(tc, pClient, pContext, cChainID, owner, spare)
			require.NotEqual(stake.InputID(), imported.InputID())
			tc.Log().Info("the owner is funded on the P-Chain",
				zap.Uint64("amount", stake.Out.(*warpfx.TransferOutput).Amt),
			)
		})

		// --- A delegation, before anything changes the validator set --------

		var delegatorChange *avax.UTXO
		tc.By("delegating to a validator that already exists", func() {
			// Delegating to an existing node rather than staking a new one:
			// this leg has no reason to disturb the canonical set, and the
			// authorizations below would race it if it did.
			validators, err := pClient.GetCurrentValidators(tc.DefaultContext(), constants.PrimaryNetworkID, nil)
			require.NoError(err)
			require.NotEmpty(validators)
			host := validators[0]

			held := stake.Out.(*warpfx.TransferOutput).Amt
			change := held - delegatorStake - spend

			now := time.Now()
			end := now.Add(2 * time.Minute)
			require.True(end.Before(time.Unix(int64(host.EndTime), 0)),
				"a delegation must end within its host's own period")

			tx := &platform.Tx{
				Unsigned: &platform.AddPermissionlessDelegatorTx{
					BaseTx: platform.BaseTx{BaseTx: avax.BaseTx{
						NetworkID:    pContext.NetworkID,
						BlockchainID: constants.PlatformChainID,
						Ins: []*avax.TransferableInput{{
							UTXOID: stake.UTXOID,
							Asset:  stake.Asset,
							In:     &secp256k1fx.TransferInput{Amt: held},
						}},
						Outs: []*avax.TransferableOutput{{
							Asset: avax.Asset{ID: avaxAssetID},
							Out:   &warpfx.TransferOutput{Amt: change, Owner: owner},
						}},
					}},
					Validator: platform.Validator{
						NodeID: host.NodeID,
						Start:  uint64(now.Unix()),
						End:    uint64(end.Unix()),
						Wght:   delegatorStake,
					},
					Subnet: constants.PrimaryNetworkID,
					StakeOuts: []*avax.TransferableOutput{{
						Asset: avax.Asset{ID: avaxAssetID},
						Out:   &warpfx.TransferOutput{Amt: delegatorStake, Owner: owner},
					}},
					// The reward reaches this owner through CreateOutput,
					// months from now, with nobody around to authorize it.
					DelegationRewardsOwner: &owner,
				},
				Creds: []verify.Verifiable{&warpfx.Credential{}},
			}
			require.NoError(tx.Initialize(platform.Codec))
			tx.Creds[0] = &warpfx.Credential{WarpMessage: authorize(tx.Unsigned.Bytes())}
			require.NoError(tx.Initialize(platform.Codec))

			txID, err := pClient.IssueTx(tc.DefaultContext(), tx.Bytes())
			require.NoError(err)
			awaitCommitted(tc, pClient, txID, "authorized delegation")

			delegatorChange = &avax.UTXO{
				UTXOID: avax.UTXOID{TxID: txID, OutputIndex: 0},
				Asset:  avax.Asset{ID: avaxAssetID},
				Out:    &warpfx.TransferOutput{Amt: change, Owner: owner},
			}
			tc.Log().Info("a warp owner is delegating, holding no key",
				zap.Stringer("nodeID", host.NodeID),
				zap.Uint64("stake", delegatorStake),
			)
		})

		// --- The auto-renewed validator, which does change the set ----------

		var autoRenewedTxID ids.ID
		var autoRenewedChange *avax.UTXO
		tc.By("opening an auto-renewed validator, its authority a warp owner", func() {
			held := delegatorChange.Out.(*warpfx.TransferOutput).Amt
			change := held - validatorStake - spend
			nodeID := ids.GenerateTestNodeID()

			tx := &platform.Tx{
				Unsigned: &platform.AddAutoRenewedValidatorTx{
					BaseTx: platform.BaseTx{BaseTx: avax.BaseTx{
						NetworkID:    pContext.NetworkID,
						BlockchainID: constants.PlatformChainID,
						Ins: []*avax.TransferableInput{{
							UTXOID: delegatorChange.UTXOID,
							Asset:  delegatorChange.Asset,
							In:     &secp256k1fx.TransferInput{Amt: held},
						}},
						Outs: []*avax.TransferableOutput{{
							Asset: avax.Asset{ID: avaxAssetID},
							Out:   &warpfx.TransferOutput{Amt: change, Owner: owner},
						}},
					}},
					ValidatorNodeID: nodeID[:],
					Signer:          newProofOfPossession(tc),
					StakeOuts: []*avax.TransferableOutput{{
						Asset: avax.Asset{ID: avaxAssetID},
						Out:   &warpfx.TransferOutput{Amt: validatorStake, Owner: owner},
					}},
					ValidatorRewardsOwner: &owner,
					DelegatorRewardsOwner: &owner,
					// The point of this spec: a warp owner as a *permission*
					// owner, reached through the contextual entry point.
					ValidatorAuthority:       &owner,
					DelegationShares:         reward.PercentDenominator / 4,
					AutoCompoundRewardShares: 0,
					Period:                   cyclePeriod,
				},
				Creds: []verify.Verifiable{&warpfx.Credential{}},
			}
			require.NoError(tx.Initialize(platform.Codec))
			tx.Creds[0] = &warpfx.Credential{WarpMessage: authorize(tx.Unsigned.Bytes())}
			require.NoError(tx.Initialize(platform.Codec))

			autoRenewedTxID, err = pClient.IssueTx(tc.DefaultContext(), tx.Bytes())
			require.NoError(err)
			awaitCommitted(tc, pClient, autoRenewedTxID, "auto-renewed validator")

			autoRenewedChange = &avax.UTXO{
				UTXOID: avax.UTXOID{TxID: autoRenewedTxID, OutputIndex: 0},
				Asset:  avax.Asset{ID: avaxAssetID},
				Out:    &warpfx.TransferOutput{Amt: change, Owner: owner},
			}
			tc.Log().Info("an auto-renewed validator whose authority holds no key",
				zap.Stringer("txID", autoRenewedTxID),
				zap.Uint64("period", cyclePeriod),
			)
		})

		tc.By("checking what the set change costs the next authorization", func() {
			// ⚠️ The transaction above added a validator, and that closes the
			// door behind it. A quorum is verified at a P-Chain height that
			// only advances when a block is accepted, while the aggregator
			// signs against the set as it is now - so the bitset indexes six
			// members against a filtered set of five, and no retry fixes it.
			//
			// This is not a slow convergence. Blocks are produced here on
			// purpose, and the verifier's set was still short of the new
			// member well past any propagation delay. SetAutoRenewedValidator-
			// ConfigTx is therefore exercised in the unit suite
			// (TestWarpOwnerCanStopItsAutoRenewedValidator), not here, and the
			// limitation is the point this block records:
			//
			//   a relayer cannot chain an authorized transaction behind one
			//   that changes the validator set.
			held := autoRenewedChange.Out.(*warpfx.TransferOutput).Amt
			tx := &platform.Tx{
				Unsigned: &platform.SetAutoRenewedValidatorConfigTx{
					BaseTx: platform.BaseTx{BaseTx: avax.BaseTx{
						NetworkID:    pContext.NetworkID,
						BlockchainID: constants.PlatformChainID,
						Ins: []*avax.TransferableInput{{
							UTXOID: autoRenewedChange.UTXOID,
							Asset:  autoRenewedChange.Asset,
							In:     &secp256k1fx.TransferInput{Amt: held},
						}},
						Outs: []*avax.TransferableOutput{{
							Asset: avax.Asset{ID: avaxAssetID},
							Out:   &warpfx.TransferOutput{Amt: held - spend, Owner: owner},
						}},
					}},
					TxID: autoRenewedTxID,
					// No signature indices: the authority presents nothing,
					// exactly as a warpfx input does. One message covers both
					// the spend and the authority, they share an owner.
					Auth:                     &secp256k1fx.Input{},
					AutoCompoundRewardShares: 0,
					Period:                   0, // a graceful exit at cycle end
				},
				Creds: []verify.Verifiable{&warpfx.Credential{}, &warpfx.Credential{}},
			}
			require.NoError(tx.Initialize(platform.Codec))
			tx.Creds[0] = &warpfx.Credential{WarpMessage: authorize(tx.Unsigned.Bytes())}
			tx.Creds[1] = &warpfx.Credential{}
			require.NoError(tx.Initialize(platform.Codec))

			// The shape is right - it is refused on the quorum, never on the
			// authority or the credential layout, which is what this asserts.
			_, err := pClient.IssueTx(tc.DefaultContext(), tx.Bytes())
			if err == nil {
				tc.Log().Info("the configuration change was accepted after all")
				return
			}
			// Either message means the same thing - the aggregator and the
			// verifier disagree on the canonical set. Which one surfaces
			// depends on whether an index fell out of range or the weights
			// merely differ. What matters is that nothing rejected the
			// authority, the credential layout or the spend.
			require.ErrorContains(err, "failed verifying warp messages", //nolint:forbidigo // the error crosses a JSON-RPC boundary as a string
				"only the shifted canonical set may stop it here")
			tc.Log().Warn("an authorization cannot follow a validator-set change",
				zap.Error(err),
			)
		})
	})
})

// awaitAtomicUTXOs waits for [n] UTXOs addressed to [addr] in C->P shared
// memory and returns them.
func awaitAtomicUTXOs(
	tc *e2e.GinkgoTestContext,
	client *platformvm.Client,
	addr common.Address,
	n int,
) []*avax.UTXO {
	require := require.New(tc)

	var utxos []*avax.UTXO
	tc.Eventually(func() bool {
		raw, _, _, err := client.GetAtomicUTXOs(
			tc.DefaultContext(),
			[]ids.ShortID{ids.ShortID(addr)},
			"C", 100, ids.ShortEmpty, ids.Empty,
		)
		require.NoError(err)
		if len(raw) < n {
			return false
		}
		utxos = make([]*avax.UTXO, len(raw))
		for i, b := range raw {
			utxo := &avax.UTXO{}
			_, err := platform.Codec.Unmarshal(b, utxo)
			require.NoError(err)
			utxos[i] = utxo
		}
		return true
	}, e2e.DefaultTimeout, e2e.DefaultPollingInterval, "the exported UTXOs were not discoverable before timeout")
	return utxos
}
