// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package message

import (
	"fmt"

	"github.com/ava-labs/avalanchego/vms/types"
)

// TxAuthorization authorizes a P-chain transaction on behalf of the address
// that emitted it, in place of a secp256k1 signature.
//
// It carries the exact bytes of the unsigned transaction - the very bytes an
// EOA would sign - rather than a description of what the transaction should do.
// That is the whole design, and it is what removes the class of attacks a
// description invites: with an intent to fulfill, whoever assembles the
// transaction chooses where the change goes, and can send it to themselves
// while the accounting stays perfect. Committing to the bytes freezes the
// transaction before the authorization exists, so the owner sees and approves
// every output, and the submitter is left with exactly the power a submitter
// has over an EOA-signed transaction: none.
//
// Nothing else belongs here. No nonce, no fee cap, no recipient: they are all
// already in TxBytes, and repeating one would create a second source of truth
// that nothing forces to agree with the first.
type TxAuthorization struct {
	payload

	// Expiry is the last chain time at which this authorization may be
	// executed, in Unix seconds. Equality is still valid.
	//
	// It is the design's only temporal guard, and it is needed because a Warp
	// message never expires on its own: an authorization issued for a
	// transaction that was never submitted would stay executable for as long as
	// its inputs exist.
	Expiry uint64 `serialize:"true" json:"expiry"`

	// TxBytes is the serialization of the unsigned P-chain transaction this
	// authorization commits to.
	//
	// The preimage, not a hash of it. A 32-byte digest would be just as secure
	// - it is what an EOA signs - but the submitter cannot rebuild a
	// transaction from a digest. Carrying the preimage puts the whole
	// transaction in the C-chain log the submitter already watches, so it needs
	// no out-of-band channel and its logic stays a single path, agnostic to the
	// transaction type.
	TxBytes types.JSONByteSlice `serialize:"true" json:"txBytes"`
}

// NewTxAuthorization creates a new initialized TxAuthorization.
func NewTxAuthorization(
	expiry uint64,
	txBytes []byte,
) (*TxAuthorization, error) {
	msg := &TxAuthorization{
		Expiry:  expiry,
		TxBytes: txBytes,
	}
	return msg, Initialize(msg)
}

// ParseTxAuthorization parses bytes into an initialized TxAuthorization.
func ParseTxAuthorization(b []byte) (*TxAuthorization, error) {
	payloadIntf, err := Parse(b)
	if err != nil {
		return nil, err
	}
	payload, ok := payloadIntf.(*TxAuthorization)
	if !ok {
		return nil, fmt.Errorf("%w: %T", ErrWrongType, payloadIntf)
	}
	return payload, nil
}
