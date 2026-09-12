// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package warpfx

import "github.com/ava-labs/avalanchego/vms/components/verify"

var _ verify.Verifiable = (*Credential)(nil)

// Credential carries the Warp message authorizing a transaction.
//
// It lives in the transaction's credentials rather than in the unsigned
// transaction because the message contains the unsigned transaction's bytes:
// placing it inside would require those bytes to contain themselves.
//
// Exactly one credential of a transaction carries a non-empty message; every
// other slot facing a warpfx input carries an empty one.
type Credential struct {
	WarpMessage []byte `serialize:"true" json:"warpMessage"`
}

// Verify is deliberately permissive: it is called on every credential of a
// transaction before the carrier is known. The message itself is checked once
// per transaction, on the deterministic execution path.
func (*Credential) Verify() error {
	return nil
}
