// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package warpfx

import (
	"errors"

	"github.com/ava-labs/avalanchego/vms/components/verify"
	"github.com/ava-labs/avalanchego/vms/platformvm/fx"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"
)

var (
	_ fx.Fx           = (*Fx)(nil)
	_ fx.ContextualFx = (*Fx)(nil)

	ErrWrongVMType           = errors.New("wrong vm type")
	ErrWrongUTXOType         = errors.New("wrong utxo type")
	ErrWrongInputType        = errors.New("wrong input type")
	ErrWrongCredentialType   = errors.New("wrong credential type")
	ErrWrongOwnerType        = errors.New("wrong owner type")
	ErrUnexpectedSigIndices  = errors.New("unexpected signature indices")
	ErrMismatchedAmounts     = errors.New("utxo amount and input amount are not equal")
	ErrNoAuthorization       = errors.New("no authorization provided")
	ErrWrongOwner            = errors.New("authorization does not cover this owner")
	ErrPermissionUnsupported = errors.New("warp owners cannot control a subnet")
)

// Types returns the types this extension claims.
//
// It is the single list: Initialize registers it in the VM's codec registry,
// and the PlatformVM's Fx table claims it. Duplicating either side would be an
// invisible divergence.
func Types() []any {
	return []any{
		&Owner{},
		&TransferOutput{},
		&Credential{},
	}
}

// Fx authorizes spending a [TransferOutput] with a Warp message committing to
// the transaction's unsigned bytes, in place of a secp256k1 signature.
//
// It holds the VM given to Initialize and nothing else. In particular it never
// retains an authorization: one surviving from a verification to the next would
// authorize a transaction it never approved, which no test written transaction
// by transaction would reveal.
type Fx struct {
	VM VM
}

func (f *Fx) Initialize(vmIntf interface{}) error {
	vm, ok := vmIntf.(VM)
	if !ok {
		return ErrWrongVMType
	}
	f.VM = vm

	f.VM.Logger().Debug("initializing warp fx")

	c := f.VM.CodecRegistry()
	errs := make([]error, 0, len(Types()))
	for _, t := range Types() {
		errs = append(errs, c.RegisterType(t))
	}
	return errors.Join(errs...)
}

// Bootstrapping and Bootstrapped are no-ops: unlike secp256k1fx, this extension
// skips nothing while the node catches up. Every check it performs is a pure
// function of the transaction.
func (*Fx) Bootstrapping() error {
	return nil
}

func (*Fx) Bootstrapped() error {
	return nil
}

// VerifyTransfer always fails.
//
// This is the enforcement mechanism, not an unimplemented method. Only the
// transactions that resolve an authorization reach the contextual entry point;
// every other path calls this one. Forgetting to route a transaction therefore
// *closes* a door - it can never open one.
func (*Fx) VerifyTransfer(_, _, _, _ interface{}) error {
	return ErrNoAuthorization
}

// VerifyTransferWithContext checks provenance: that the authorization resolved
// for this transaction was issued by the owner of this UTXO.
//
// It is called once per consumed UTXO. Whether the authorization covers *this*
// transaction is a separate check, made once per transaction on the
// deterministic execution path.
func (*Fx) VerifyTransferWithContext(fxCtx *fx.Context, _, inIntf, credIntf, utxoIntf interface{}) error {
	out, ok := utxoIntf.(*TransferOutput)
	if !ok {
		return ErrWrongUTXOType
	}

	// No input type of its own: a secp256k1fx.TransferInput with no signature
	// indices already means "this input presents nothing", and it sits at the
	// same codec position in every chain that matters.
	in, ok := inIntf.(*secp256k1fx.TransferInput)
	if !ok {
		return ErrWrongInputType
	}
	if len(in.SigIndices) != 0 {
		return ErrUnexpectedSigIndices
	}

	cred, ok := credIntf.(*Credential)
	if !ok {
		return ErrWrongCredentialType
	}

	if err := verify.All(out, in, cred); err != nil {
		return err
	}

	if out.Amt != in.Amt {
		return ErrMismatchedAmounts
	}

	if fxCtx == nil || fxCtx.Authorization == nil {
		return ErrNoAuthorization
	}
	authorization, ok := fxCtx.Authorization.(*Authorization)
	if !ok {
		return ErrNoAuthorization
	}
	if !authorization.Authorizes(&out.Owner) {
		return ErrWrongOwner
	}
	return nil
}

// VerifyPermission always fails, for the same reason VerifyTransfer does: a
// caller that resolved no authorization has nothing to prove assent with.
//
// Subnet authorization reaches this method and only this one, so a warp owner
// can never be a subnet's control group - which is what keeps the default Fx
// enough for that path.
func (*Fx) VerifyPermission(_, _, _, _ interface{}) error {
	return ErrPermissionUnsupported
}

// VerifyPermissionWithContext checks that the authorization resolved for this
// transaction was issued by the control group it claims to act for.
//
// This is what lets a warp owner manage an auto-renewed validator: the
// ValidatorAuthority of AddAutoRenewedValidatorTx is an fx.Owner, and only a
// SetAutoRenewedValidatorConfigTx proving assent for it can stop the validator
// and unlock its stake.
func (*Fx) VerifyPermissionWithContext(fxCtx *fx.Context, _, authIntf, credIntf, controlGroupIntf interface{}) error {
	owner, ok := controlGroupIntf.(*Owner)
	if !ok {
		return ErrWrongOwnerType
	}

	// Same reuse as on the transfer path: no input type of our own, and no
	// signature indices to present.
	auth, ok := authIntf.(*secp256k1fx.Input)
	if !ok {
		return ErrWrongInputType
	}
	if len(auth.SigIndices) != 0 {
		return ErrUnexpectedSigIndices
	}

	cred, ok := credIntf.(*Credential)
	if !ok {
		return ErrWrongCredentialType
	}

	if err := verify.All(owner, auth, cred); err != nil {
		return err
	}

	if fxCtx == nil || fxCtx.Authorization == nil {
		return ErrNoAuthorization
	}
	authorization, ok := fxCtx.Authorization.(*Authorization)
	if !ok {
		return ErrNoAuthorization
	}
	if !authorization.Authorizes(owner) {
		return ErrWrongOwner
	}
	return nil
}

// CreateOutput materializes a staking reward, months after the staking
// transaction was accepted, when the rewards owner is all that is left of it.
func (*Fx) CreateOutput(amount uint64, ownerIntf interface{}) (interface{}, error) {
	owner, ok := ownerIntf.(*Owner)
	if !ok {
		return nil, ErrWrongOwnerType
	}
	return &TransferOutput{
		Amt:   amount,
		Owner: *owner,
	}, nil
}
