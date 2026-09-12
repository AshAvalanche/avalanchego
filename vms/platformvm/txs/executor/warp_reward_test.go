// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package executor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ava-labs/avalanchego/database"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/upgrade/upgradetest"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/units"
	"github.com/ava-labs/avalanchego/vms/components/avax"
	"github.com/ava-labs/avalanchego/vms/components/verify"
	"github.com/ava-labs/avalanchego/vms/platformvm/genesis/genesistest"
	"github.com/ava-labs/avalanchego/vms/platformvm/platform"
	"github.com/ava-labs/avalanchego/vms/platformvm/reward"
	"github.com/ava-labs/avalanchego/vms/platformvm/signer"
	"github.com/ava-labs/avalanchego/vms/platformvm/state"
	"github.com/ava-labs/avalanchego/vms/platformvm/status"
	"github.com/ava-labs/avalanchego/vms/platformvm/warp/message"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"
	"github.com/ava-labs/avalanchego/vms/warpfx"

	txfee "github.com/ava-labs/avalanchego/vms/platformvm/txs/fee"
)

// TestWarpRewardIndices is the property that justifies StakeSettled existing.
//
// A staking reward is predictable neither in amount nor in position, and the
// position is the subtle half: on the commit branch the delegatee reward sits
// after the validation reward, and on the abort branch it *moves up* into the
// rank the validation reward would have occupied. That outcome is a function of
// measured uptime, so no commitment made in advance can contain it - the
// contract has to be told.
//
// A test covering only the commit branch would say nothing about that.
func TestWarpRewardIndices(t *testing.T) {
	const (
		validationReward = 2_000_000
		delegateeReward  = 500_000
	)

	owner := warpfx.Owner{
		SourceChainID: ids.GenerateTestID(),
		SourceAddress: ids.GenerateTestShortID().Bytes(),
	}

	env := newEnvironment(t, upgradetest.Latest)

	var (
		nodeID    = ids.GenerateTestNodeID()
		startTime = genesistest.DefaultValidatorStartTime
		endTime   = startTime.Add(defaultMinStakingDuration)
	)

	// Both rewards go to the same warp owner: an AddPermissionlessValidatorTx
	// may name two different ones, and each then asks for its own message.
	unsigned := &platform.AddPermissionlessValidatorTx{
		BaseTx: platform.BaseTx{BaseTx: avax.BaseTx{
			NetworkID:    env.ctx.NetworkID,
			BlockchainID: env.ctx.ChainID,
			Outs: []*avax.TransferableOutput{{
				Asset: avax.Asset{ID: env.ctx.AVAXAssetID},
				Out:   &secp256k1fx.TransferOutput{Amt: 1},
			}},
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
		ValidatorRewardsOwner: &owner,
		DelegatorRewardsOwner: &owner,
		DelegationShares:      reward.PercentDenominator / 4,
	}
	stakingTx := &platform.Tx{Unsigned: unsigned, Creds: nil}
	require.NoError(t, stakingTx.Initialize(platform.Codec))

	staker, err := state.NewCurrentStaker(
		stakingTx.ID(),
		unsigned,
		startTime,
		endTime,
		unsigned.Weight(),
		validationReward,
	)
	require.NoError(t, err)

	require.NoError(t, env.state.PutCurrentValidator(staker))
	env.state.AddTx(stakingTx, status.Committed)
	require.NoError(t, env.state.SetStakingInfo(
		constants.PrimaryNetworkID,
		nodeID,
		state.StakingInfo{DelegateeReward: delegateeReward},
	))
	env.state.SetTimestamp(endTime)
	env.state.SetHeight(1)
	require.NoError(t, env.state.Commit())

	rewardTx, err := newRewardValidatorTx(t, stakingTx.ID())
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

	// The rank the rewards are placed after: the transaction's own outputs,
	// then its stake outputs, which unstakeUTXOs recreates.
	base := uint32(len(unsigned.Outputs()) + len(unsigned.Stake()))

	tests := []struct {
		name    string
		diff    *state.Diff
		rewards []message.Reward
	}{
		{
			// Validation reward first, delegatee reward behind it.
			name: "commit",
			diff: onCommitState,
			rewards: []message.Reward{
				{OutputIndex: base, Amount: validationReward},
				{OutputIndex: base + 1, Amount: delegateeReward},
			},
		},
		{
			// The validation reward is forfeited, and the delegatee reward
			// moves up into its rank. This is the whole point.
			name: "abort",
			diff: onAbortState,
			rewards: []message.Reward{
				{OutputIndex: base, Amount: delegateeReward},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require := require.New(t)

			// Every reward is correctly typed on both branches - the abort path
			// builds its UTXO directly rather than through newUTXO, but reuses
			// the output the Fx dispatch produced.
			for _, want := range test.rewards {
				utxoID := avax.UTXOID{TxID: stakingTx.ID(), OutputIndex: want.OutputIndex}
				utxo, err := test.diff.GetUTXO(utxoID.InputID())
				require.NoError(err)
				require.Equal(&warpfx.TransferOutput{
					Amt:   want.Amount,
					Owner: owner,
				}, utxo.Out)
			}

			// And nothing beyond them.
			utxoID := avax.UTXOID{
				TxID:        stakingTx.ID(),
				OutputIndex: base + uint32(len(test.rewards)),
			}
			_, err := test.diff.GetUTXO(utxoID.InputID())
			require.ErrorIs(err, database.ErrNotFound)
		})
	}

	// The stake refund is not a reward UTXO: unstakeUTXOs only calls AddUTXO,
	// never AddRewardUTXO. A contract holding the staking txID computes those
	// indices itself, which is why the message does not carry them.
	require.NoError(t, onCommitState.Apply(env.state))
	require.NoError(t, env.state.Commit())

	rewardUTXOs, err := env.state.GetRewardUTXOs(stakingTx.ID())
	require.NoError(t, err)
	require.Len(t, rewardUTXOs, 2)
	for _, utxo := range rewardUTXOs {
		require.GreaterOrEqual(t, utxo.OutputIndex, base)
	}
}

// TestAutoRenewedRewardsAreNotIndexedUnderTheStakingTx pins the difference in
// key that makes two settlement messages necessary rather than one.
//
// A permissionless staking has its reward UTXOs indexed under the staking txID,
// once. An auto-renewed validator has them under the txID of each
// RewardAutoRenewedValidatorTx, once per cycle. The key, not the owner, is what
// differs - so this holds whatever the rewards owner is.
func TestAutoRenewedRewardsAreNotIndexedUnderTheStakingTx(t *testing.T) {
	require := require.New(t)

	env := newEnvironment(t, upgradetest.Latest)
	cfg := defaultAutoRenewedValidatorConfig

	stakingTx := newAddAutoRenewedValidatorTx(
		t,
		env,
		cfg.weight,
		cfg.delegationRewardShares,
		cfg.autoCompoundRewardShares,
	)
	addAutoRenewedValidator(t, env, stakingTx, cfg)

	staker, err := env.state.GetCurrentValidator(
		constants.PrimaryNetworkID,
		stakingTx.Unsigned.(*platform.AddAutoRenewedValidatorTx).NodeID(),
	)
	require.NoError(err)

	env.state.SetTimestamp(staker.EndTime)
	env.state.SetHeight(2)
	require.NoError(env.state.Commit())

	rewardTx := newRewardAutoRenewedValidatorTx(t, stakingTx.ID(), staker.EndTime)

	onCommitState, err := state.NewDiff(lastAcceptedID, env, state.StakerAdditionAfterDeletionAllowed)
	require.NoError(err)
	onAbortState, err := state.NewDiff(lastAcceptedID, env, state.StakerAdditionAfterDeletionAllowed)
	require.NoError(err)

	require.NoError(ProposalTx(
		&env.backend,
		state.PickFeeCalculator(env.config, onCommitState),
		rewardTx,
		onCommitState,
		onAbortState,
	))
	require.NoError(onCommitState.Apply(env.state))
	require.NoError(env.state.Commit())

	// The cycle produced rewards, under the reward transaction's own ID.
	cycleRewards, err := env.state.GetRewardUTXOs(rewardTx.ID())
	require.NoError(err)
	require.NotEmpty(cycleRewards)

	// And nothing at all under the staking transaction's. A StakeSettled asked
	// on that key would say "settled with no reward" to an owner who has in
	// fact been paid - which is why the handler refuses to sign one for this
	// transaction type at all.
	stakingRewards, err := env.state.GetRewardUTXOs(stakingTx.ID())
	require.NoError(err)
	require.Empty(stakingRewards)
}

// TestWarpOwnerCanStopItsAutoRenewedValidator is the acceptance half of the
// boundary the proposal draws, and the counterpart of the subnet refusal.
//
// The two only make sense together. Taken alone, the subnet refusal would also
// pass if somebody had made VerifyPermission universally refusing, and this one
// would also pass if somebody had routed the subnet path through the contextual
// variant. It is their conjunction that says the boundary is the *entry point*,
// not the type of the owner: subnet authorization goes through the context-free
// method and is refused, while SetAutoRenewedValidatorConfigTx goes through the
// contextual one and is served.
//
// It also happens to be the only way out of an auto-renewed stake: only this
// transaction can set Period = 0, which stops the validator at the end of its
// cycle and unlocks the funds. Without it a contract's stake never comes back.
func TestWarpOwnerCanStopItsAutoRenewedValidator(t *testing.T) {
	require := require.New(t)

	const (
		utxoAmount = 10 * units.Avax
		fee        = 1000
		expiry     = uint64(1 << 62)
	)

	env := newEnvironment(t, upgradetest.Latest)

	owner := warpfx.Owner{
		SourceChainID: ids.GenerateTestID(),
		SourceAddress: ids.GenerateTestShortID().Bytes(),
	}

	// An auto-renewed validator whose authority - and whose rewards - belong to
	// a warp owner.
	cfg := defaultAutoRenewedValidatorConfig
	stakingUnsigned := &platform.AddAutoRenewedValidatorTx{
		BaseTx: platform.BaseTx{BaseTx: avax.BaseTx{
			NetworkID:    env.ctx.NetworkID,
			BlockchainID: env.ctx.ChainID,
		}},
		ValidatorNodeID: ids.GenerateTestNodeID().Bytes(),
		Signer:          newProofOfPossession(t),
		StakeOuts: []*avax.TransferableOutput{{
			Asset: avax.Asset{ID: env.ctx.AVAXAssetID},
			Out:   &warpfx.TransferOutput{Amt: cfg.weight, Owner: owner},
		}},
		ValidatorRewardsOwner: &owner,
		DelegatorRewardsOwner: &owner,
		ValidatorAuthority:    &owner,
		DelegationShares:      cfg.delegationRewardShares,
		Period:                uint64(env.config.MinStakeDuration / time.Second),
	}
	stakingTx := &platform.Tx{Unsigned: stakingUnsigned}
	require.NoError(stakingTx.Initialize(platform.Codec))

	staker, err := state.NewCurrentStaker(
		stakingTx.ID(),
		stakingUnsigned,
		env.state.GetTimestamp(),
		env.state.GetTimestamp().Add(env.config.MinStakeDuration),
		stakingUnsigned.Weight(),
		0,
	)
	require.NoError(err)
	require.NoError(env.state.PutCurrentValidator(staker))
	env.state.AddTx(stakingTx, status.Committed)
	require.NoError(env.state.SetStakingInfo(
		constants.PrimaryNetworkID,
		stakingUnsigned.NodeID(),
		state.StakingInfo{
			AutoCompoundRewardShares: cfg.autoCompoundRewardShares,
			NextPeriod:               stakingUnsigned.Period,
		},
	))

	// Funds to pay the fee with, held by the same owner: one authorization
	// covers both the spend and the assent, since it commits to the bytes of
	// the whole transaction.
	spent := &avax.UTXO{
		UTXOID: avax.UTXOID{TxID: ids.GenerateTestID()},
		Asset:  avax.Asset{ID: env.ctx.AVAXAssetID},
		Out:    &warpfx.TransferOutput{Amt: utxoAmount, Owner: owner},
	}
	env.state.AddUTXO(spent)
	env.state.SetHeight(1)
	require.NoError(env.state.Commit())

	// Period = 0: stop at the end of the current cycle and unlock the funds.
	unsigned := &platform.SetAutoRenewedValidatorConfigTx{
		BaseTx: platform.BaseTx{BaseTx: avax.BaseTx{
			NetworkID:    env.ctx.NetworkID,
			BlockchainID: env.ctx.ChainID,
			Ins: []*avax.TransferableInput{{
				UTXOID: spent.UTXOID,
				Asset:  spent.Asset,
				In:     &secp256k1fx.TransferInput{Amt: utxoAmount},
			}},
			Outs: []*avax.TransferableOutput{{
				Asset: avax.Asset{ID: env.ctx.AVAXAssetID},
				Out:   &warpfx.TransferOutput{Amt: utxoAmount - fee, Owner: owner},
			}},
		}},
		TxID:                     stakingTx.ID(),
		Auth:                     &secp256k1fx.Input{},
		AutoCompoundRewardShares: cfg.autoCompoundRewardShares,
		Period:                   0,
	}

	// Creds is parallel to Ins, plus the authorization credential at the end.
	// The carrier may be either slot - it is found by scanning - and here it is
	// the last one, which is also the one verifyAuthorization consumes.
	tx := &platform.Tx{
		Unsigned: unsigned,
		Creds: []verify.Verifiable{
			&warpfx.Credential{},
			&warpfx.Credential{},
		},
	}
	require.NoError(tx.Initialize(platform.Codec))

	tx.Creds[1] = &warpfx.Credential{
		WarpMessage: newAuthorization(
			t,
			owner.SourceChainID,
			owner.SourceAddress,
			expiry,
			tx.Unsigned.Bytes(),
		),
	}
	require.NoError(tx.Initialize(platform.Codec))

	diff, err := state.NewDiff(lastAcceptedID, env, state.StakerAdditionAfterDeletionAllowed)
	require.NoError(err)

	_, _, _, err = StandardTx(&env.backend, txfee.NewSimpleCalculator(fee), tx, diff)
	require.NoError(err)

	// The validator is configured to stop at the end of its cycle.
	stakingInfo, err := diff.GetStakingInfo(constants.PrimaryNetworkID, stakingUnsigned.NodeID())
	require.NoError(err)
	require.Zero(stakingInfo.NextPeriod)
}
