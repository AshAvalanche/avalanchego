// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package fx

import (
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/snow"
	"github.com/ava-labs/avalanchego/vms/components/verify"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"
)

var (
	_ Fx    = (*secp256k1fx.Fx)(nil)
	_ Owner = (*secp256k1fx.OutputOwners)(nil)
	_ Owned = (*secp256k1fx.TransferOutput)(nil)
)

// Fx is the interface a feature extension must implement to support the
// Platform Chain.
type Fx interface {
	// Initialize this feature extension to be running under this VM. Should
	// return an error if the VM is incompatible.
	Initialize(vm interface{}) error

	// Notify this Fx that the VM is in bootstrapping
	Bootstrapping() error

	// Notify this Fx that the VM is bootstrapped
	Bootstrapped() error

	// VerifyTransfer verifies that the specified transaction can spend the
	// provided utxo with no restrictions on the destination. If the transaction
	// can't spend the output based on the input and credential, a non-nil error
	// should be returned.
	VerifyTransfer(tx, in, cred, utxo interface{}) error

	// VerifyPermission returns nil iff [cred] proves that [controlGroup]
	// assents to [tx]
	VerifyPermission(tx, in, cred, controlGroup interface{}) error

	// CreateOutput creates a new output with the provided control group worth
	// the specified amount
	CreateOutput(amount uint64, controlGroup interface{}) (interface{}, error)
}

// Claim is one feature extension of a [Fxs] collection, together with the
// concrete types it claims.
//
// Types is nil for the default extension: it is what every unclaimed type
// resolves to anyway, so listing its types would only create a second place to
// keep them.
type Claim struct {
	ID    ids.ID
	Fx    Fx
	Types []any
}

// Context is the transaction-scoped information a Fx may need in addition to
// the (input, credential, utxo) triple.
//
// It is always passed as an argument and never retained by a Fx: an
// authorization that survived from one verification to the next would authorize
// a transaction it never approved.
type Context struct {
	// Authorization is resolved once per transaction, or nil when the
	// transaction carries none. It is typed as an interface because this
	// package must not depend on any particular Fx.
	Authorization interface{}
}

// ContextualFx is a Fx whose conditions cannot be decided from the
// (input, credential, utxo) triple alone.
//
// Fxs that do not implement it keep being called through [Fx.VerifyTransfer]
// and [Fx.VerifyPermission].
type ContextualFx interface {
	Fx

	// VerifyTransferWithContext is [Fx.VerifyTransfer] with the additional
	// transaction-scoped context. A nil fxCtx means the caller reached this
	// utxo through an entry point that resolves no authorization.
	VerifyTransferWithContext(fxCtx *Context, tx, in, cred, utxo interface{}) error

	// VerifyPermissionWithContext is [Fx.VerifyPermission] with the same
	// context.
	//
	// Callers that have no authorization to offer keep using
	// [Fx.VerifyPermission], which is what confines a Fx to the control groups
	// it can actually prove assent for.
	VerifyPermissionWithContext(fxCtx *Context, tx, in, cred, controlGroup interface{}) error
}

type Owner interface {
	verify.IsNotState

	verify.Verifiable
	snow.ContextInitializable
}

type Owned interface {
	Owners() interface{}
}
