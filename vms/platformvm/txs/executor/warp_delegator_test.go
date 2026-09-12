// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package executor

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ava-labs/avalanchego/database"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/upgrade/upgradetest"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/vms/components/avax"
	"github.com/ava-labs/avalanchego/vms/platformvm/genesis/genesistest"
	"github.com/ava-labs/avalanchego/vms/platformvm/platform"
	"github.com/ava-labs/avalanchego/vms/platformvm/reward"
	"github.com/ava-labs/avalanchego/vms/platformvm/signer"
	"github.com/ava-labs/avalanchego/vms/platformvm/state"
	"github.com/ava-labs/avalanchego/vms/platformvm/status"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"
	"github.com/ava-labs/avalanchego/vms/warpfx"
)

// TestWarpDelegatorRewardsGoToItsOwner covers the delegation leg of the
// proposal, which the validation tests do not reach.
//
// It matters because rewards are minted through the *second* dispatch site of
// the design: CreateOutput resolves on the type of the rewards owner, so a
// warpfx.Owner must produce a warpfx.TransferOutput months after the fact,
// with nobody around to authorize anything. A delegation reaches that site by
// its own path - unstakeUTXOs then the delegator's reward - and nothing else
// checks it.
func TestWarpDelegatorRewardsGoToItsOwner(t *testing.T) {
	const (
		validationReward = 2_000_000
		delegationReward = 700_000
	)

	// Deliberately distinct owners: a delegation's reward must reach the
	// delegator's owner, never the validator's.
	validatorOwner := warpfx.Owner{
		SourceChainID: ids.GenerateTestID(),
		SourceAddress: ids.GenerateTestShortID().Bytes(),
	}
	delegatorOwner := warpfx.Owner{
		SourceChainID: validatorOwner.SourceChainID,
		SourceAddress: ids.GenerateTestShortID().Bytes(),
	}
	require.False(t, validatorOwner.Equals(&delegatorOwner))

	env := newEnvironment(t, upgradetest.Latest)

	var (
		nodeID    = ids.GenerateTestNodeID()
		startTime = genesistest.DefaultValidatorStartTime
		endTime   = startTime.Add(defaultMinStakingDuration)
	)

	validatorTx := &platform.Tx{Unsigned: &platform.AddPermissionlessValidatorTx{
		BaseTx: platform.BaseTx{BaseTx: avax.BaseTx{
			NetworkID:    env.ctx.NetworkID,
			BlockchainID: env.ctx.ChainID,
		}},
		Validator: platform.Validator{
			NodeID: nodeID,
			Start:  uint64(startTime.Unix()),
			End:    uint64(endTime.Unix()),
			Wght:   env.config.MinValidatorStake,
		},
		Subnet: constants.PrimaryNetworkID,
		Signer: &signer.Empty{},
		StakeOuts: []*avax.TransferableOutput{{
			Asset: avax.Asset{ID: env.ctx.AVAXAssetID},
			Out:   &secp256k1fx.TransferOutput{Amt: env.config.MinValidatorStake},
		}},
		ValidatorRewardsOwner: &validatorOwner,
		DelegatorRewardsOwner: &validatorOwner,
		DelegationShares:      reward.PercentDenominator / 4,
	}}
	require.NoError(t, validatorTx.Initialize(platform.Codec))

	// The delegation. Its stake returns to a warpfx output, and its reward is
	// owned by an address that signed nothing and holds no key.
	delegatorUnsigned := &platform.AddPermissionlessDelegatorTx{
		BaseTx: platform.BaseTx{BaseTx: avax.BaseTx{
			NetworkID:    env.ctx.NetworkID,
			BlockchainID: env.ctx.ChainID,
			Outs: []*avax.TransferableOutput{{
				Asset: avax.Asset{ID: env.ctx.AVAXAssetID},
				Out:   &warpfx.TransferOutput{Amt: 1, Owner: delegatorOwner},
			}},
		}},
		Validator: platform.Validator{
			NodeID: nodeID,
			Start:  uint64(startTime.Unix()),
			End:    uint64(endTime.Unix()),
			Wght:   env.config.MinDelegatorStake,
		},
		Subnet: constants.PrimaryNetworkID,
		StakeOuts: []*avax.TransferableOutput{{
			Asset: avax.Asset{ID: env.ctx.AVAXAssetID},
			Out:   &warpfx.TransferOutput{Amt: env.config.MinDelegatorStake, Owner: delegatorOwner},
		}},
		DelegationRewardsOwner: &delegatorOwner,
	}
	delegatorTx := &platform.Tx{Unsigned: delegatorUnsigned}
	require.NoError(t, delegatorTx.Initialize(platform.Codec))

	validator, err := state.NewCurrentStaker(
		validatorTx.ID(),
		validatorTx.Unsigned.(*platform.AddPermissionlessValidatorTx),
		startTime,
		endTime,
		env.config.MinValidatorStake,
		validationReward,
	)
	require.NoError(t, err)
	delegator, err := state.NewCurrentStaker(
		delegatorTx.ID(),
		delegatorUnsigned,
		startTime,
		endTime,
		env.config.MinDelegatorStake,
		delegationReward,
	)
	require.NoError(t, err)

	require.NoError(t, env.state.PutCurrentValidator(validator))
	require.NoError(t, env.state.PutCurrentDelegator(delegator))
	env.state.AddTx(validatorTx, status.Committed)
	env.state.AddTx(delegatorTx, status.Committed)
	env.state.SetTimestamp(endTime)
	env.state.SetHeight(1)
	require.NoError(t, env.state.Commit())

	rewardTx, err := newRewardValidatorTx(t, delegatorTx.ID())
	require.NoError(t, err)

	onCommitState, err := state.NewDiff(lastAcceptedID, env, state.StakerAdditionAfterDeletionForbidden)
	require.NoError(t, err)
	onAbortState, err := state.NewDiff(lastAcceptedID, env, state.StakerAdditionAfterDeletionForbidden)
	require.NoError(t, err)

	require.NoError(t, ProposalTx(
		&env.backend,
		state.PickFeeCalculator(env.config, onCommitState),
		rewardTx,
		onCommitState,
		onAbortState,
	))

	base := uint32(len(delegatorUnsigned.Outputs()) + len(delegatorUnsigned.Stake()))

	for _, test := range []struct {
		name string
		diff *state.Diff
	}{
		{"commit", onCommitState},
		{"abort", onAbortState},
	} {
		t.Run(test.name, func(t *testing.T) {
			require := require.New(t)

			// The stake returns first, rebuilt by unstakeUTXOs from the stake
			// outputs, so it keeps the type it was staked with.
			for i, out := range delegatorUnsigned.Stake() {
				utxoID := avax.UTXOID{
					TxID:        delegatorTx.ID(),
					OutputIndex: uint32(len(delegatorUnsigned.Outputs()) + i), //#nosec G115
				}
				utxo, err := test.diff.GetUTXO(utxoID.InputID())
				require.NoError(err)
				require.Equal(out.Out, utxo.Out)
			}

			// Then the reward, minted through CreateOutput - the dispatch that
			// resolves on the *type* of the rewards owner, months after the
			// fact, with nobody around to authorize anything.
			utxoID := avax.UTXOID{TxID: delegatorTx.ID(), OutputIndex: base}
			utxo, err := test.diff.GetUTXO(utxoID.InputID())
			if test.name == "abort" {
				// An aborted delegation is paid nothing; only its stake returns.
				require.ErrorIs(err, database.ErrNotFound)
				return
			}
			require.NoError(err)

			// The validator keeps its DelegationShares cut - a quarter here -
			// and the rest reaches the delegator. The split is the existing
			// staking rule; what this test pins is where the remainder lands.
			const delegateeCut = delegationReward / 4
			require.Equal(&warpfx.TransferOutput{
				Amt:   delegationReward - delegateeCut,
				Owner: delegatorOwner,
			}, utxo.Out, "the reward belongs to the delegator's owner, not the validator's")
		})
	}
}
