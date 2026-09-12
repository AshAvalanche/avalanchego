// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package network

import (
	"fmt"
	"slices"

	"github.com/ava-labs/avalanchego/database"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/snow/engine/common"
	"github.com/ava-labs/avalanchego/utils/hashing"
	"github.com/ava-labs/avalanchego/utils/iterator"
	"github.com/ava-labs/avalanchego/vms/components/avax"
	"github.com/ava-labs/avalanchego/vms/platformvm/platform"
	"github.com/ava-labs/avalanchego/vms/platformvm/state"
	"github.com/ava-labs/avalanchego/vms/platformvm/status"
	"github.com/ava-labs/avalanchego/vms/platformvm/warp/message"
	"github.com/ava-labs/avalanchego/vms/warpfx"
)

// verifyTxExecuted signs an attestation that a transaction authorized by a Warp
// message was accepted, and under which ID.
//
// Nothing is emitted or stored for this: the answer is derived from accepted
// state on request, exactly as ACP-77 does, which is why "no state added to the
// P-chain" still holds and why retention never comes up.
func (s signatureRequestVerifier) verifyTxExecuted(
	msg *message.TxExecuted,
) *common.AppError {
	s.stateLock.Lock()
	defer s.stateLock.Unlock()

	tx, txStatus, err := s.state.GetTx(msg.TxID)
	if err == database.ErrNotFound {
		return &common.AppError{
			Code:    ErrTxDoesNotExist,
			Message: fmt.Sprintf("transaction %q does not exist", msg.TxID),
		}
	}
	if err != nil {
		return &common.AppError{
			Code:    common.ErrUndefined.Code,
			Message: "failed to get transaction: " + err.Error(),
		}
	}
	if txStatus != status.Committed {
		return &common.AppError{
			Code:    ErrTxNotCommitted,
			Message: fmt.Sprintf("transaction %q is %s", msg.TxID, txStatus),
		}
	}

	if authHash := ids.ID(hashing.ComputeHash256Array(tx.Unsigned.Bytes())); authHash != msg.AuthHash {
		return &common.AppError{
			Code:    ErrMismatchedAuthHash,
			Message: fmt.Sprintf("provided authHash %q != expected authHash %q", msg.AuthHash, authHash),
		}
	}

	if !hasWarpCredential(tx) {
		return errNoWarpCredential(msg.TxID)
	}
	return nil
}

// verifyStakeSettled signs an attestation naming the reward UTXOs a settled
// staking transaction produced for one owner.
func (s signatureRequestVerifier) verifyStakeSettled(
	msg *message.StakeSettled,
) *common.AppError {
	if err := msg.Verify(); err != nil {
		return &common.AppError{
			Code:    ErrRewardsNotSorted,
			Message: err.Error(),
		}
	}

	s.stateLock.Lock()
	defer s.stateLock.Unlock()

	tx, txStatus, err := s.state.GetTx(msg.StakingTxID)
	if err == database.ErrNotFound {
		return &common.AppError{
			Code:    ErrTxDoesNotExist,
			Message: fmt.Sprintf("transaction %q does not exist", msg.StakingTxID),
		}
	}
	if err != nil {
		return &common.AppError{
			Code:    common.ErrUndefined.Code,
			Message: "failed to get transaction: " + err.Error(),
		}
	}
	if txStatus != status.Committed {
		return &common.AppError{
			Code:    ErrTxNotCommitted,
			Message: fmt.Sprintf("transaction %q is %s", msg.StakingTxID, txStatus),
		}
	}

	var isDelegator bool
	switch tx.Unsigned.(type) {
	case *platform.AddPermissionlessValidatorTx:
	case *platform.AddPermissionlessDelegatorTx:
		isDelegator = true
	default:
		return &common.AppError{
			Code:    ErrNotAStakingTx,
			Message: fmt.Sprintf("transaction %q is a %T", msg.StakingTxID, tx.Unsigned),
		}
	}

	if !hasWarpCredential(tx) {
		return errNoWarpCredential(msg.StakingTxID)
	}

	// The staker must be gone from both sets. That is what makes an empty
	// reward list unambiguous - the same reasoning as the "is not and can never
	// become" of L1ValidatorRegistration: the staking is closed and can produce
	// nothing more.
	staker := tx.Unsigned.(platform.Staker)
	settled, appErr := s.isStakingSettled(staker.SubnetID(), staker.NodeID(), msg.StakingTxID, isDelegator)
	if appErr != nil {
		return appErr
	}
	if !settled {
		return &common.AppError{
			Code:    ErrStakingNotSettled,
			Message: fmt.Sprintf("staking %q is still in the staker set", msg.StakingTxID),
		}
	}

	return s.verifyRewardsMatch(msg.StakingTxID, msg.SourceChainID, msg.SourceAddress, msg.Rewards)
}

// verifyCycleSettled signs an attestation naming the reward UTXOs one cycle of
// an auto-renewed validator produced for one owner.
//
// It is shorter than verifyStakeSettled because a settled cycle is settled for
// good: a committed RewardAutoRenewedValidatorTx is final *for its cycle*,
// whether or not the validator carries on.
func (s signatureRequestVerifier) verifyCycleSettled(
	msg *message.CycleSettled,
) *common.AppError {
	if err := msg.Verify(); err != nil {
		return &common.AppError{
			Code:    ErrRewardsNotSorted,
			Message: err.Error(),
		}
	}

	s.stateLock.Lock()
	defer s.stateLock.Unlock()

	rewardTx, txStatus, err := s.state.GetTx(msg.RewardTxID)
	if err == database.ErrNotFound {
		return &common.AppError{
			Code:    ErrTxDoesNotExist,
			Message: fmt.Sprintf("transaction %q does not exist", msg.RewardTxID),
		}
	}
	if err != nil {
		return &common.AppError{
			Code:    common.ErrUndefined.Code,
			Message: "failed to get transaction: " + err.Error(),
		}
	}

	// Proposal transactions live in the state on both branches -
	// onCommitState.AddTx(tx, Committed) and onAbortState.AddTx(tx, Aborted) -
	// so an aborted cycle exists too, with reward UTXOs of its own. It must not
	// be attestable as though it had been committed.
	if txStatus != status.Committed {
		return &common.AppError{
			Code:    ErrTxNotCommitted,
			Message: fmt.Sprintf("transaction %q is %s", msg.RewardTxID, txStatus),
		}
	}

	rewardUnsigned, ok := rewardTx.Unsigned.(*platform.RewardAutoRenewedValidatorTx)
	if !ok {
		return &common.AppError{
			Code:    ErrNotACycleTx,
			Message: fmt.Sprintf("transaction %q is a %T", msg.RewardTxID, rewardTx.Unsigned),
		}
	}

	// The scope condition is on the *staking* transaction, not the reward one.
	// A RewardAutoRenewedValidatorTx carries no credential at all - its
	// executor refuses len(Creds) != 0 - so looking for a warpfx credential
	// there would always refuse. The AddAutoRenewedValidatorTx it names is what
	// carries the authorization.
	stakingTx, _, err := s.state.GetTx(rewardUnsigned.TxID)
	if err != nil {
		return &common.AppError{
			Code:    ErrTxDoesNotExist,
			Message: fmt.Sprintf("staking transaction %q does not exist: %s", rewardUnsigned.TxID, err),
		}
	}
	if _, ok := stakingTx.Unsigned.(*platform.AddAutoRenewedValidatorTx); !ok {
		return &common.AppError{
			Code:    ErrNotAStakingTx,
			Message: fmt.Sprintf("transaction %q is a %T", rewardUnsigned.TxID, stakingTx.Unsigned),
		}
	}
	if !hasWarpCredential(stakingTx) {
		return errNoWarpCredential(rewardUnsigned.TxID)
	}

	return s.verifyRewardsMatch(msg.RewardTxID, msg.SourceChainID, msg.SourceAddress, msg.Rewards)
}

// verifyRewardsMatch checks that [rewards] is exactly the reward UTXOs indexed
// under [txID] that belong to the named owner.
//
// Callers must hold the state lock.
func (s signatureRequestVerifier) verifyRewardsMatch(
	txID ids.ID,
	sourceChainID ids.ID,
	sourceAddress ids.ShortID,
	rewards []message.Reward,
) *common.AppError {
	utxos, err := s.state.GetRewardUTXOs(txID)
	if err != nil {
		return &common.AppError{
			Code:    common.ErrUndefined.Code,
			Message: "failed to get reward UTXOs: " + err.Error(),
		}
	}

	owner := warpfx.Owner{
		SourceChainID: sourceChainID,
		SourceAddress: sourceAddress[:],
	}
	expected := make([]message.Reward, 0, len(utxos))
	for _, utxo := range utxos {
		out, ok := utxo.Out.(*warpfx.TransferOutput)
		if !ok || !out.Owner.Equals(&owner) {
			continue
		}
		expected = append(expected, message.Reward{
			OutputIndex: utxo.OutputIndex,
			Amount:      out.Amt,
		})
	}
	slices.SortFunc(expected, func(a, b message.Reward) int {
		return int(a.OutputIndex) - int(b.OutputIndex)
	})

	if !slices.Equal(expected, rewards) {
		return &common.AppError{
			Code:    ErrMismatchedRewards,
			Message: fmt.Sprintf("provided rewards %v != expected rewards %v", rewards, expected),
		}
	}
	return nil
}

// isStakingSettled reports whether the staker created by [stakingTxID] has left
// both the current and the pending sets.
//
// The state does not index stakers by transaction: the on-disk layout is keyed
// by txID, but every in-memory accessor is typed (subnetID, nodeID). The
// staking transaction carries both, so the lookup is O(1) for a validator, and
// for a delegator it iterates the delegators of that one validator rather than
// every staker.
//
// Callers must hold the state lock.
func (s signatureRequestVerifier) isStakingSettled(
	subnetID ids.ID,
	nodeID ids.NodeID,
	stakingTxID ids.ID,
	isDelegator bool,
) (bool, *common.AppError) {
	if !isDelegator {
		for _, get := range []func(ids.ID, ids.NodeID) (*state.Staker, error){
			s.state.GetCurrentValidator,
			s.state.GetPendingValidator,
		} {
			staker, err := get(subnetID, nodeID)
			switch {
			case err == database.ErrNotFound:
			case err != nil:
				return false, &common.AppError{
					Code:    common.ErrUndefined.Code,
					Message: "failed to look up validator: " + err.Error(),
				}
			case staker.TxID == stakingTxID:
				return false, nil
			}
		}
		return true, nil
	}

	// A delegator only exists while its validator does, so an empty iterator
	// here also covers the case of a validator that has itself left.
	for _, iterate := range []func(ids.ID, ids.NodeID) (iterator.Iterator[*state.Staker], error){
		s.state.GetCurrentDelegatorIterator,
		s.state.GetPendingDelegatorIterator,
	} {
		it, err := iterate(subnetID, nodeID)
		if err != nil {
			return false, &common.AppError{
				Code:    common.ErrUndefined.Code,
				Message: "failed to iterate delegators: " + err.Error(),
			}
		}
		for it.Next() {
			if it.Value().TxID == stakingTxID {
				it.Release()
				return false, nil
			}
		}
		it.Release()
	}
	return true, nil
}

// hasWarpCredential reports whether [tx] carries a warpfx credential.
//
// This is not a security check, it is a scope boundary, and it is worth keeping
// for that reason. Without it the P-chain would become a general-purpose oracle
// for transaction acceptance - useful, probably a proposal of its own, and not
// this one. It bounds the surface this proposal asks anyone to maintain.
func hasWarpCredential(tx *platform.Tx) bool {
	for _, cred := range tx.Creds {
		if _, ok := cred.(*warpfx.Credential); ok {
			return true
		}
	}
	return false
}

func errNoWarpCredential(txID ids.ID) *common.AppError {
	return &common.AppError{
		Code:    ErrTxHasNoWarpCredential,
		Message: fmt.Sprintf("transaction %q carries no warp credential", txID),
	}
}

// Chain is the state a signature request verifier reads.
//
// It is state.Chain widened with GetRewardUTXOs, which the shared interface
// does not declare - it only has AddRewardUTXO. The accessor exists on the
// concrete state, so this widens what the verifier asks for rather than adding
// a capability.
type Chain interface {
	state.Chain

	GetRewardUTXOs(txID ids.ID) ([]*avax.UTXO, error)
}
