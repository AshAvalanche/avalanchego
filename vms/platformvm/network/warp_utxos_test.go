// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package network

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/snow/engine/common"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/hashing"
	"github.com/ava-labs/avalanchego/utils/timer/mockable"
	"github.com/ava-labs/avalanchego/vms/components/avax"
	"github.com/ava-labs/avalanchego/vms/components/verify"
	"github.com/ava-labs/avalanchego/vms/platformvm/platform"
	"github.com/ava-labs/avalanchego/vms/platformvm/signer"
	"github.com/ava-labs/avalanchego/vms/platformvm/state"
	"github.com/ava-labs/avalanchego/vms/platformvm/state/statetest"
	"github.com/ava-labs/avalanchego/vms/platformvm/status"
	"github.com/ava-labs/avalanchego/vms/platformvm/warp"
	"github.com/ava-labs/avalanchego/vms/platformvm/warp/message"
	"github.com/ava-labs/avalanchego/vms/platformvm/warp/payload"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"
	"github.com/ava-labs/avalanchego/vms/warpfx"
)

var (
	testWarpChainID = ids.GenerateTestID()
	testWarpAddress = ids.GenerateTestShortID()
)

func testWarpOwner() warpfx.Owner {
	return warpfx.Owner{
		SourceChainID: testWarpChainID,
		SourceAddress: testWarpAddress[:],
	}
}

// addCommittedTx stores [unsigned] as a committed transaction, with the
// credentials given, and returns its ID.
func addCommittedTx(
	t *testing.T,
	s *state.State,
	unsigned platform.UnsignedTx,
	creds ...verify.Verifiable,
) *platform.Tx {
	t.Helper()

	tx := &platform.Tx{Unsigned: unsigned, Creds: creds}
	require.NoError(t, tx.Initialize(platform.Codec))
	s.AddTx(tx, status.Committed)
	return tx
}

func testValidatorTx(nodeID ids.NodeID) *platform.AddPermissionlessValidatorTx {
	return &platform.AddPermissionlessValidatorTx{
		Validator:             platform.Validator{NodeID: nodeID, Wght: 1},
		Subnet:                constants.PrimaryNetworkID,
		Signer:                &signer.Empty{},
		StakeOuts:             []*avax.TransferableOutput{{Out: &secp256k1fx.TransferOutput{Amt: 1}}},
		ValidatorRewardsOwner: &secp256k1fx.OutputOwners{},
		DelegatorRewardsOwner: &secp256k1fx.OutputOwners{},
	}
}

func testAutoRenewedTx(nodeID ids.NodeID) *platform.AddAutoRenewedValidatorTx {
	return &platform.AddAutoRenewedValidatorTx{
		ValidatorNodeID:       nodeID[:],
		Signer:                &signer.Empty{},
		StakeOuts:             []*avax.TransferableOutput{{Out: &secp256k1fx.TransferOutput{Amt: 1}}},
		ValidatorRewardsOwner: &secp256k1fx.OutputOwners{},
		DelegatorRewardsOwner: &secp256k1fx.OutputOwners{},
		ValidatorAuthority:    &secp256k1fx.OutputOwners{},
		Period:                1,
	}
}

func addWarpRewardUTXO(s *state.State, txID ids.ID, index uint32, amount uint64, owner warpfx.Owner) {
	s.AddRewardUTXO(txID, &avax.UTXO{
		UTXOID: avax.UTXOID{TxID: txID, OutputIndex: index},
		Out:    &warpfx.TransferOutput{Amt: amount, Owner: owner},
	})
}

func TestVerifyTxExecuted(t *testing.T) {
	var (
		chainState = statetest.New(t, statetest.Config{})
		s          = signatureRequestVerifier{
			stateLock: &sync.Mutex{},
			state:     chainState,
		}
	)

	authorized := addCommittedTx(t, chainState, &platform.BaseTx{}, &warpfx.Credential{})
	unauthorized := addCommittedTx(t, chainState, &platform.BaseTx{BaseTx: avax.BaseTx{
		Memo: []byte("secp only"),
	}}, &secp256k1fx.Credential{})

	aborted := &platform.Tx{Unsigned: &platform.BaseTx{BaseTx: avax.BaseTx{Memo: []byte("aborted")}}, Creds: []verify.Verifiable{&warpfx.Credential{}}}
	require.NoError(t, aborted.Initialize(platform.Codec))
	chainState.AddTx(aborted, status.Aborted)

	authHashOf := func(tx *platform.Tx) ids.ID {
		return hashing.ComputeHash256Array(tx.Unsigned.Bytes())
	}

	tests := []struct {
		name         string
		txID         ids.ID
		authHash     ids.ID
		expectedCode int
	}{
		{
			name:     "valid",
			txID:     authorized.ID(),
			authHash: authHashOf(authorized),
		},
		{
			name:         "unknown transaction",
			txID:         ids.GenerateTestID(),
			authHash:     authHashOf(authorized),
			expectedCode: ErrTxDoesNotExist,
		},
		{
			name:         "not committed",
			txID:         aborted.ID(),
			authHash:     authHashOf(aborted),
			expectedCode: ErrTxNotCommitted,
		},
		{
			// The commitment is the link back to the authorization. An
			// attestation naming a transaction the authorization did not commit
			// to says nothing.
			name:         "the authorization was for another transaction",
			txID:         authorized.ID(),
			authHash:     authHashOf(unauthorized),
			expectedCode: ErrMismatchedAuthHash,
		},
		{
			// A scope boundary, not a security check: without it the P-chain
			// becomes a general-purpose acceptance oracle.
			name:         "no warp credential",
			txID:         unauthorized.ID(),
			authHash:     authHashOf(unauthorized),
			expectedCode: ErrTxHasNoWarpCredential,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			msg, err := message.NewTxExecuted(test.txID, test.authHash)
			require.NoError(t, err)

			requireAppErrorCode(t, s.verifyTxExecuted(msg), test.expectedCode)
		})
	}
}

func TestVerifyStakeSettled(t *testing.T) {
	var (
		chainState = statetest.New(t, statetest.Config{})
		s          = signatureRequestVerifier{
			stateLock: &sync.Mutex{},
			state:     chainState,
		}
		owner  = testWarpOwner()
		nodeID = ids.GenerateTestNodeID()
	)

	settled := addCommittedTx(t, chainState, testValidatorTx(nodeID), &warpfx.Credential{})
	addWarpRewardUTXO(chainState, settled.ID(), 3, 100, owner)
	addWarpRewardUTXO(chainState, settled.ID(), 5, 200, owner)
	// Somebody else's reward under the same staking transaction: an
	// AddPermissionlessValidatorTx may name two different owners for its
	// validation and delegation rewards, and each gets only its own.
	addWarpRewardUTXO(chainState, settled.ID(), 4, 999, warpfx.Owner{
		SourceChainID: testWarpChainID,
		SourceAddress: ids.GenerateTestShortID().Bytes(),
	})
	// And a secp256k1 reward, which is not this message's business either.
	chainState.AddRewardUTXO(settled.ID(), &avax.UTXO{
		UTXOID: avax.UTXOID{TxID: settled.ID(), OutputIndex: 6},
		Out:    &secp256k1fx.TransferOutput{Amt: 7},
	})

	// A staking that has not left the set yet.
	runningNodeID := ids.GenerateTestNodeID()
	running := addCommittedTx(t, chainState, testValidatorTx(runningNodeID), &warpfx.Credential{})
	require.NoError(t, chainState.PutCurrentValidator(&state.Staker{
		TxID:     running.ID(),
		EndTime:  mockable.MaxTime,
		SubnetID: constants.PrimaryNetworkID,
		NodeID:   runningNodeID,
	}))

	// A staking with no warp credential at all.
	unauthorized := addCommittedTx(t, chainState, testValidatorTx(ids.GenerateTestNodeID()), &secp256k1fx.Credential{})

	// Something that is not a staking transaction at all.
	notStaking := addCommittedTx(t, chainState, &platform.BaseTx{BaseTx: avax.BaseTx{
		Memo: []byte("not staking"),
	}}, &warpfx.Credential{})

	// An auto-renewed validator, whose rewards are never indexed under its
	// staking txID.
	autoRenewed := addCommittedTx(t, chainState, testAutoRenewedTx(ids.GenerateTestNodeID()), &warpfx.Credential{})

	tests := []struct {
		name         string
		stakingTxID  ids.ID
		address      ids.ShortID
		rewards      []message.Reward
		expectedCode int
	}{
		{
			name:        "valid",
			stakingTxID: settled.ID(),
			address:     testWarpAddress,
			rewards: []message.Reward{
				{OutputIndex: 3, Amount: 100},
				{OutputIndex: 5, Amount: 200},
			},
		},
		{
			// "settled with no reward", which is unambiguous precisely because
			// the staker is gone and can produce nothing more.
			name:        "an owner with no reward",
			stakingTxID: settled.ID(),
			address:     ids.GenerateTestShortID(),
		},
		{
			name:         "unknown transaction",
			stakingTxID:  ids.GenerateTestID(),
			address:      testWarpAddress,
			expectedCode: ErrTxDoesNotExist,
		},
		{
			name:         "not a staking transaction",
			stakingTxID:  notStaking.ID(),
			address:      testWarpAddress,
			expectedCode: ErrNotAStakingTx,
		},
		{
			// The trap CycleSettled exists to avoid, closed harder than
			// expected. An auto-renewed validator indexes its rewards under the
			// txID of each RewardAutoRenewedValidatorTx, so
			// GetRewardUTXOs(stakingTxID) is always empty for one: a
			// StakeSettled signed on that key would announce "settled with no
			// reward" to an owner paid every cycle - consistent with the
			// message's definition and thoroughly misleading. Restricting the
			// type to the two permissionless stakings means such a message
			// cannot even be requested.
			name:         "an auto-renewed validator's staking transaction",
			stakingTxID:  autoRenewed.ID(),
			address:      testWarpAddress,
			expectedCode: ErrNotAStakingTx,
		},
		{
			name:         "no warp credential",
			stakingTxID:  unauthorized.ID(),
			address:      testWarpAddress,
			expectedCode: ErrTxHasNoWarpCredential,
		},
		{
			name:         "still staking",
			stakingTxID:  running.ID(),
			address:      testWarpAddress,
			expectedCode: ErrStakingNotSettled,
		},
		{
			name:        "a reward that is not there",
			stakingTxID: settled.ID(),
			address:     testWarpAddress,
			rewards: []message.Reward{
				{OutputIndex: 3, Amount: 100},
				{OutputIndex: 5, Amount: 200},
				{OutputIndex: 8, Amount: 1},
			},
			expectedCode: ErrMismatchedRewards,
		},
		{
			name:        "a reward with the wrong amount",
			stakingTxID: settled.ID(),
			address:     testWarpAddress,
			rewards: []message.Reward{
				{OutputIndex: 3, Amount: 101},
				{OutputIndex: 5, Amount: 200},
			},
			expectedCode: ErrMismatchedRewards,
		},
		{
			// Somebody else's reward, claimed under this owner's name.
			name:        "a reward belonging to another owner",
			stakingTxID: settled.ID(),
			address:     testWarpAddress,
			rewards: []message.Reward{
				{OutputIndex: 3, Amount: 100},
				{OutputIndex: 4, Amount: 999},
				{OutputIndex: 5, Amount: 200},
			},
			expectedCode: ErrMismatchedRewards,
		},
		{
			// Without a normative order two nodes sign different bytes and no
			// quorum ever forms.
			name:        "rewards out of order",
			stakingTxID: settled.ID(),
			address:     testWarpAddress,
			rewards: []message.Reward{
				{OutputIndex: 5, Amount: 200},
				{OutputIndex: 3, Amount: 100},
			},
			expectedCode: ErrRewardsNotSorted,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			msg, err := message.NewStakeSettled(
				test.stakingTxID,
				testWarpChainID,
				test.address,
				test.rewards,
			)
			require.NoError(t, err)

			requireAppErrorCode(t, s.verifyStakeSettled(msg), test.expectedCode)
		})
	}
}

func TestVerifyCycleSettled(t *testing.T) {
	var (
		chainState = statetest.New(t, statetest.Config{})
		s          = signatureRequestVerifier{
			stateLock: &sync.Mutex{},
			state:     chainState,
		}
		owner = testWarpOwner()
	)

	staking := addCommittedTx(t, chainState, testAutoRenewedTx(ids.GenerateTestNodeID()), &warpfx.Credential{})

	// A reward transaction carries no credential of its own: its executor
	// refuses len(Creds) != 0, which is why the scope condition looks at the
	// staking transaction instead.
	cycle := addCommittedTx(t, chainState, &platform.RewardAutoRenewedValidatorTx{
		TxID:      staking.ID(),
		Timestamp: 1,
	})
	addWarpRewardUTXO(chainState, cycle.ID(), 0, 42, owner)

	// The same cycle on the abort branch exists in the state too, with reward
	// UTXOs of its own, and must not be attestable as though committed.
	abortedCycle := &platform.Tx{Unsigned: &platform.RewardAutoRenewedValidatorTx{
		TxID:      staking.ID(),
		Timestamp: 2,
	}}
	require.NoError(t, abortedCycle.Initialize(platform.Codec))
	chainState.AddTx(abortedCycle, status.Aborted)

	unauthorizedStaking := addCommittedTx(t, chainState, testAutoRenewedTx(ids.GenerateTestNodeID()), &secp256k1fx.Credential{})
	unauthorizedCycle := addCommittedTx(t, chainState, &platform.RewardAutoRenewedValidatorTx{
		TxID:      unauthorizedStaking.ID(),
		Timestamp: 3,
	})

	tests := []struct {
		name         string
		rewardTxID   ids.ID
		rewards      []message.Reward
		expectedCode int
	}{
		{
			name:       "valid",
			rewardTxID: cycle.ID(),
			rewards:    []message.Reward{{OutputIndex: 0, Amount: 42}},
		},
		{
			name:         "unknown transaction",
			rewardTxID:   ids.GenerateTestID(),
			expectedCode: ErrTxDoesNotExist,
		},
		{
			name:         "the aborted branch of the same cycle",
			rewardTxID:   abortedCycle.ID(),
			expectedCode: ErrTxNotCommitted,
		},
		{
			name:         "not a cycle transaction",
			rewardTxID:   staking.ID(),
			expectedCode: ErrNotACycleTx,
		},
		{
			name:         "the staking transaction carries no warp credential",
			rewardTxID:   unauthorizedCycle.ID(),
			expectedCode: ErrTxHasNoWarpCredential,
		},
		{
			name:         "the wrong amount",
			rewardTxID:   cycle.ID(),
			rewards:      []message.Reward{{OutputIndex: 0, Amount: 43}},
			expectedCode: ErrMismatchedRewards,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			msg, err := message.NewCycleSettled(
				test.rewardTxID,
				testWarpChainID,
				testWarpAddress,
				test.rewards,
			)
			require.NoError(t, err)

			requireAppErrorCode(t, s.verifyCycleSettled(msg), test.expectedCode)
		})
	}
}

// TestVerifyDispatchesAttestations checks the three payloads reach their rule
// through the handler's switch, rather than falling into the unsupported-type
// default.
func TestVerifyDispatchesAttestations(t *testing.T) {
	var (
		chainState = statetest.New(t, statetest.Config{})
		s          = signatureRequestVerifier{
			stateLock: &sync.Mutex{},
			state:     chainState,
		}
	)

	tests := []struct {
		name         string
		payload      []byte
		expectedCode int
	}{
		{
			name:         "TxExecuted",
			payload:      must[*message.TxExecuted](t)(message.NewTxExecuted(ids.GenerateTestID(), ids.GenerateTestID())).Bytes(),
			expectedCode: ErrTxDoesNotExist,
		},
		{
			name:         "StakeSettled",
			payload:      must[*message.StakeSettled](t)(message.NewStakeSettled(ids.GenerateTestID(), testWarpChainID, testWarpAddress, nil)).Bytes(),
			expectedCode: ErrTxDoesNotExist,
		},
		{
			name:         "CycleSettled",
			payload:      must[*message.CycleSettled](t)(message.NewCycleSettled(ids.GenerateTestID(), testWarpChainID, testWarpAddress, nil)).Bytes(),
			expectedCode: ErrTxDoesNotExist,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := s.Verify(
				t.Context(),
				must[*warp.UnsignedMessage](t)(warp.NewUnsignedMessage(
					constants.UnitTestID,
					constants.PlatformChainID,
					must[*payload.AddressedCall](t)(payload.NewAddressedCall(nil, test.payload)).Bytes(),
				)),
				nil,
			)
			requireAppErrorCode(t, err, test.expectedCode)
		})
	}
}

func requireAppErrorCode(t *testing.T, err *common.AppError, expectedCode int) {
	t.Helper()

	if expectedCode == 0 {
		require.Nil(t, err)
		return
	}
	require.NotNil(t, err)
	require.Equal(t, int32(expectedCode), err.Code, err.Message)
}
