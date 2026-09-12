// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package platform

import (
	"errors"
	"fmt"

	"github.com/ava-labs/avalanchego/snow"
	"github.com/ava-labs/avalanchego/vms/platformvm/fx"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"
)

var (
	_ UnsignedTx = (*CreateSubnetTx)(nil)

	// ErrWarpOwnerCannotOwnSubnet refuses any subnet owner that is not a
	// secp256k1fx one.
	//
	// A warp owner would leave the subnet permanently unmanageable: subnet
	// authorization takes the context-free entry point, where warpfx answers
	// ErrPermissionUnsupported, so no CreateChainTx and no ownership transfer
	// could ever be authorized again. Self-inflicted, but the repo already
	// guards the equivalent in OutputOwners.Verify, and this states in writing
	// what the Fx dispatch relies on: subnet authorization is secp256k1-only.
	//
	// Do not generalize into "a warp owner is never a permission owner".
	// AddAutoRenewedValidatorTx.ValidatorAuthority is the symmetric case and
	// ends the other way, SetAutoRenewedValidatorConfigTx reaching the Fx *with*
	// a context. What differs is the entry point, not the owner's type.
	ErrWarpOwnerCannotOwnSubnet = errors.New("a subnet owner must be a secp256k1fx owner")
)

// verifySubnetOwner refuses a subnet owner that could never authorize anything.
//
// The rule is syntactic, so before the upgrade such a transaction is already
// refused by the activation guard for merely mentioning a warpfx type. This is
// what holds afterwards.
func verifySubnetOwner(owner fx.Owner) error {
	if _, ok := owner.(*secp256k1fx.OutputOwners); !ok {
		return fmt.Errorf("%w: %T", ErrWarpOwnerCannotOwnSubnet, owner)
	}
	return nil
}

// CreateSubnetTx is an unsigned proposal to create a new subnet
type CreateSubnetTx struct {
	// Metadata, inputs and outputs
	BaseTx `serialize:"true"`
	// Who is authorized to manage this subnet
	Owner fx.Owner `serialize:"true" json:"owner"`
}

// InitCtx sets the FxID fields in the inputs and outputs of this
// [CreateSubnetTx]. Also sets the [ctx] to the given [vm.ctx] so that
// the addresses can be json marshalled into human readable format
func (tx *CreateSubnetTx) InitCtx(ctx *snow.Context) {
	tx.BaseTx.InitCtx(ctx)
	tx.Owner.InitCtx(ctx)
}

// SyntacticVerify verifies that this transaction is well-formed
func (tx *CreateSubnetTx) SyntacticVerify(ctx *snow.Context) error {
	switch {
	case tx == nil:
		return ErrNilTx
	case tx.SyntacticallyVerified: // already passed syntactic verification
		return nil
	}

	if err := tx.BaseTx.SyntacticVerify(ctx); err != nil {
		return err
	}
	if err := tx.Owner.Verify(); err != nil {
		return err
	}
	if err := verifySubnetOwner(tx.Owner); err != nil {
		return err
	}

	tx.SyntacticallyVerified = true
	return nil
}

func (tx *CreateSubnetTx) Visit(visitor TxVisitor) error {
	return visitor.CreateSubnetTx(tx)
}
