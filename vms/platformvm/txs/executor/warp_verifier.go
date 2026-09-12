// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package executor

import (
	"context"

	"github.com/ava-labs/avalanchego/snow/validators"
	"github.com/ava-labs/avalanchego/vms/platformvm/platform"
	"github.com/ava-labs/avalanchego/vms/platformvm/warp"
)

const (
	WarpQuorumNumerator   = 67
	WarpQuorumDenominator = 100
)

var _ platform.TxVisitor = (*warpVerifier)(nil)

// VerifyWarpMessages verifies all warp messages in the tx. If any of the warp
// messages are invalid, an error is returned.
//
// It takes the *signed* transaction because a Warp authorization necessarily
// lives in Creds - the message contains the unsigned transaction's bytes, so
// putting it inside would require those bytes to contain themselves.
func VerifyWarpMessages(
	ctx context.Context,
	networkID uint32,
	validatorState validators.State,
	pChainHeight uint64,
	tx *platform.Tx,
) error {
	return tx.Unsigned.Visit(&warpVerifier{
		context:        ctx,
		networkID:      networkID,
		validatorState: validatorState,
		pChainHeight:   pChainHeight,
		tx:             tx,
	})
}

type warpVerifier struct {
	context        context.Context
	networkID      uint32
	validatorState validators.State
	pChainHeight   uint64
	tx             *platform.Tx
}

func (*warpVerifier) AddValidatorTx(*platform.AddValidatorTx) error {
	return nil
}

func (*warpVerifier) AddSubnetValidatorTx(*platform.AddSubnetValidatorTx) error {
	return nil
}

func (*warpVerifier) AddDelegatorTx(*platform.AddDelegatorTx) error {
	return nil
}

func (*warpVerifier) CreateChainTx(*platform.CreateChainTx) error {
	return nil
}

func (*warpVerifier) CreateSubnetTx(*platform.CreateSubnetTx) error {
	return nil
}

func (w *warpVerifier) ImportTx(*platform.ImportTx) error {
	return w.verifyAuthorization()
}

func (w *warpVerifier) ExportTx(*platform.ExportTx) error {
	return w.verifyAuthorization()
}

func (*warpVerifier) AdvanceTimeTx(*platform.AdvanceTimeTx) error {
	return nil
}

func (*warpVerifier) RewardValidatorTx(*platform.RewardValidatorTx) error {
	return nil
}

func (*warpVerifier) RemoveSubnetValidatorTx(*platform.RemoveSubnetValidatorTx) error {
	return nil
}

func (*warpVerifier) TransformSubnetTx(*platform.TransformSubnetTx) error {
	return nil
}

func (w *warpVerifier) AddPermissionlessValidatorTx(*platform.AddPermissionlessValidatorTx) error {
	return w.verifyAuthorization()
}

func (w *warpVerifier) AddPermissionlessDelegatorTx(*platform.AddPermissionlessDelegatorTx) error {
	return w.verifyAuthorization()
}

func (*warpVerifier) TransferSubnetOwnershipTx(*platform.TransferSubnetOwnershipTx) error {
	return nil
}

func (w *warpVerifier) BaseTx(*platform.BaseTx) error {
	return w.verifyAuthorization()
}

func (*warpVerifier) ConvertSubnetToL1Tx(*platform.ConvertSubnetToL1Tx) error {
	return nil
}

func (*warpVerifier) IncreaseL1ValidatorBalanceTx(*platform.IncreaseL1ValidatorBalanceTx) error {
	return nil
}

func (*warpVerifier) DisableL1ValidatorTx(*platform.DisableL1ValidatorTx) error {
	return nil
}

func (w *warpVerifier) RegisterL1ValidatorTx(tx *platform.RegisterL1ValidatorTx) error {
	return w.verify(tx.Message)
}

func (w *warpVerifier) SetL1ValidatorWeightTx(tx *platform.SetL1ValidatorWeightTx) error {
	return w.verify(tx.Message)
}

func (w *warpVerifier) AddAutoRenewedValidatorTx(*platform.AddAutoRenewedValidatorTx) error {
	return w.verifyAuthorization()
}

func (w *warpVerifier) SetAutoRenewedValidatorConfigTx(*platform.SetAutoRenewedValidatorConfigTx) error {
	return w.verifyAuthorization()
}

func (*warpVerifier) RewardAutoRenewedValidatorTx(*platform.RewardAutoRenewedValidatorTx) error {
	return nil
}

func (w *warpVerifier) verify(message []byte) error {
	msg, err := warp.ParseMessage(message)
	if err != nil {
		return err
	}

	validators, err := warp.GetCanonicalValidatorSetFromChainID(
		w.context,
		w.validatorState,
		w.pChainHeight,
		msg.SourceChainID,
	)
	if err != nil {
		return err
	}

	return msg.Signature.Verify(
		&msg.UnsignedMessage,
		w.networkID,
		validators,
		WarpQuorumNumerator,
		WarpQuorumDenominator,
	)
}

// verifyAuthorization checks the quorum on the Warp authorization the
// transaction carries, if it carries one.
//
// The quorum and nothing else. This verifier does not know the time - its
// signature carries neither the block timestamp nor the state, and one of its
// call sites verifies a gossiped transaction outside of any block - so the
// expiry is not evaluable here. And it is not always run: it is skipped while
// the node bootstraps, and skipped again at a height whose messages were
// already verified. That is correct for a quorum, and would be fatal for the
// commitment, which is why the commitment lives on the execution path instead.
func (w *warpVerifier) verifyAuthorization() error {
	cred, err := findWarpAuthorization(w.tx.Creds)
	if err != nil {
		return err
	}
	if cred == nil {
		return nil
	}
	return w.verify(cred.WarpMessage)
}
