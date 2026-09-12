// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package executor

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/ava-labs/avalanchego/vms/components/verify"
	"github.com/ava-labs/avalanchego/vms/platformvm/fx"
	"github.com/ava-labs/avalanchego/vms/platformvm/platform"
	"github.com/ava-labs/avalanchego/vms/platformvm/warp"
	"github.com/ava-labs/avalanchego/vms/platformvm/warp/message"
	"github.com/ava-labs/avalanchego/vms/platformvm/warp/payload"
	"github.com/ava-labs/avalanchego/vms/warpfx"
)

var (
	ErrMultipleWarpAuthorizations   = errors.New("multiple warp authorizations")
	ErrWarpAuthorizationNotAccepted = errors.New("transaction does not accept a warp authorization")
	ErrAuthorizationMismatch        = errors.New("warp authorization does not commit to this transaction")
	ErrAuthorizationExpired         = errors.New("warp authorization expired")
)

// resolveAuthorization turns the Warp authorization a transaction carries, if
// it carries one, into the context the Fxs dispatch hands to warpfx.
//
// The commitment and the expiry are checked here, and the quorum is not. The
// quorum needs the network, and re-checking one already validated at an
// accepted height buys nothing. These two are the authentication itself, and
// the Warp verifier is skipped while a node bootstraps - checking them there
// would mean not checking them at all during bootstrap.
//
// Callers must place it above any bootstrap guard, not next to the flow check
// where it would naturally sit: executors skip unevenly while bootstrapping
// (the staking verifiers return nil outright, ImportTx drops its flow check,
// BaseTx skips nothing), so the natural placement would skip the resolution on
// precisely the transactions that need it most. It costs nothing to hoist -
// this function reads no state.
func resolveAuthorization(tx *platform.Tx, chainTime uint64) (*fx.Context, error) {
	cred, err := findWarpAuthorization(tx.Creds)
	if err != nil {
		return nil, err
	}
	if cred == nil {
		// The ordinary secp256k1 case: no authorization to resolve, and a
		// context that closes every warpfx path it reaches.
		return &fx.Context{}, nil
	}

	if !acceptsWarpAuthorization(tx.Unsigned) {
		return nil, fmt.Errorf("%w: %T", ErrWarpAuthorizationNotAccepted, tx.Unsigned)
	}

	msg, err := warp.ParseMessage(cred.WarpMessage)
	if err != nil {
		return nil, fmt.Errorf("parsing warp message: %w", err)
	}
	call, err := payload.ParseAddressedCall(msg.Payload)
	if err != nil {
		return nil, fmt.Errorf("parsing addressed call: %w", err)
	}
	authorization, err := message.ParseTxAuthorization(call.Payload)
	if err != nil {
		return nil, fmt.Errorf("parsing tx authorization: %w", err)
	}

	// The commitment. It is not redundant with the provenance check warpfx
	// makes per UTXO, and believing otherwise would be fatal: provenance
	// answers "does this message come from the owner of this UTXO?", never
	// "does this message authorize *this* transaction?". A Warp message is
	// public by construction - it sits in a C-chain log every relayer watches -
	// so without this check anyone could pick one up and attach it to a
	// transaction of their own choosing that spends that owner's UTXOs. The
	// message would stop being an authorization and become a bearer token over
	// everything the owner holds.
	if !bytes.Equal(authorization.TxBytes, tx.Unsigned.Bytes()) {
		return nil, ErrAuthorizationMismatch
	}

	// The chain time, never the node's local clock. secp256k1fx tests its
	// locktime against the wall clock; that is not reproducible here, because
	// the expiry is a consensus rule and a local timestamp is not a consensus
	// value. Equality is valid: the authorization is live through its expiry.
	if chainTime > authorization.Expiry {
		return nil, fmt.Errorf(
			"%w: chain time %d is past expiry %d",
			ErrAuthorizationExpired,
			chainTime,
			authorization.Expiry,
		)
	}

	return &fx.Context{
		Authorization: &warpfx.Authorization{
			SourceChainID: msg.SourceChainID,
			SourceAddress: call.SourceAddress,
		},
	}, nil
}

// findWarpAuthorization returns the single credential carrying a Warp message,
// or nil when there is none.
//
// The carrier is found by scanning, never by position. Creds is parallel to
// Ins ‖ ImportedInputs, so in a transaction mixing secp256k1 inputs the slot at
// index 0 is a secp256k1 credential; every other warpfx slot carries an empty
// message.
//
// Exactly one carrier is allowed, and something useful follows from that for
// free: since provenance compares the one authorization against the owner of
// *each* consumed UTXO, every warpfx UTXO a transaction spends necessarily
// belongs to the same owner - with no rule saying so. The outputs are not
// constrained in the same way, and deliberately: the owner committed to the
// whole byte string, so it has seen and approved every recipient.
func findWarpAuthorization(creds []verify.Verifiable) (*warpfx.Credential, error) {
	var found *warpfx.Credential
	for _, cred := range creds {
		warpCred, ok := cred.(*warpfx.Credential)
		if !ok || len(warpCred.WarpMessage) == 0 {
			continue
		}
		if found != nil {
			return nil, ErrMultipleWarpAuthorizations
		}
		found = warpCred
	}
	return found, nil
}

// acceptsWarpAuthorization reports whether [tx] may carry a Warp
// authorization.
//
// This list guards nothing that is not already guarded. What actually confines
// the authorization is structural: only these transactions call the contextual
// entry points of the UTXO verifier, and a warpfx UTXO reached through any
// other path gets a nil context and is refused. This switch only turns a
// puzzling ErrNoAuthorization into a legible error.
//
// It is a switch and not a platform.TxVisitor on purpose. A Visitor's default
// behaviour is nil, so a transaction type added to the PlatformVM later would
// silently inherit the authorization. A positive list with `default: false`
// inverts the burden: forgetting to add a transaction here cannot open
// anything.
func acceptsWarpAuthorization(tx platform.UnsignedTx) bool {
	switch tx.(type) {
	case *platform.ImportTx,
		*platform.ExportTx,
		*platform.BaseTx,
		*platform.AddPermissionlessValidatorTx,
		*platform.AddPermissionlessDelegatorTx,
		*platform.AddAutoRenewedValidatorTx,
		*platform.SetAutoRenewedValidatorConfigTx:
		return true
	default:
		return false
	}
}

// verifyAuthorizationWithContext is [verifyAuthorization] for a caller that
// resolved a transaction-scoped context. The last credential of [tx.Creds] is
// the authorization, as it is there.
//
// Subnet authorization keeps calling [verifyAuthorization], and must: its
// context-free entry point is what a warp owner refuses, which is what stops
// one from ever becoming a subnet's control group. Routing
// verifySubnetAuthorization through this function instead would reopen the
// unmanageable subnet.
func verifyAuthorizationWithContext(
	fxs *fx.Fxs,
	fxCtx *fx.Context,
	tx *platform.Tx,
	owner fx.Owner,
	auth verify.Verifiable,
) ([]verify.Verifiable, error) {
	if len(tx.Creds) == 0 {
		return nil, errWrongNumberOfCredentials
	}

	baseTxCredsLen := len(tx.Creds) - 1
	authCred := tx.Creds[baseTxCredsLen]

	if err := fxs.VerifyPermission(fxCtx, tx.Unsigned, auth, authCred, owner); err != nil {
		return nil, fmt.Errorf("%w: %w", errUnauthorizedModification, err)
	}

	return tx.Creds[:baseTxCredsLen], nil
}
