// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package message

import (
	"fmt"

	"github.com/ava-labs/avalanchego/ids"
)

// TxExecuted attests that the transaction a Warp authorization committed to was
// accepted, and says under which ID.
//
// The txID is the new information, and it is information the owner could not
// have had. It commits to the *unsigned* bytes, while the txID is the hash of
// the *signed* ones - credential included, and the credential holds an
// aggregated BLS signature over whichever subset of validators the submitter
// actually gathered. Two submissions of one authorization therefore produce two
// different txIDs. Only one can be accepted, since they consume the same
// inputs, but the owner cannot predict which.
//
// A UTXO is named by (txID, index). Without this message a contract cannot name
// the UTXOs its own transaction just created - not its change, and not the
// inputs of its next authorization.
type TxExecuted struct {
	payload

	// TxID is the accepted transaction.
	TxID ids.ID `serialize:"true" json:"txID"`

	// AuthHash is sha256 of the authorization's TxBytes field, exactly as it
	// appeared in the message.
	//
	// It is the key tying this attestation back to the authorization. The
	// contract emitted those bytes: it can recompute the hash with one call to
	// the sha256 precompile, or have kept it.
	AuthHash ids.ID `serialize:"true" json:"authHash"`
}

// NewTxExecuted creates a new initialized TxExecuted.
func NewTxExecuted(txID ids.ID, authHash ids.ID) (*TxExecuted, error) {
	msg := &TxExecuted{
		TxID:     txID,
		AuthHash: authHash,
	}
	return msg, Initialize(msg)
}

// ParseTxExecuted parses bytes into an initialized TxExecuted.
func ParseTxExecuted(b []byte) (*TxExecuted, error) {
	payloadIntf, err := Parse(b)
	if err != nil {
		return nil, err
	}
	payload, ok := payloadIntf.(*TxExecuted)
	if !ok {
		return nil, fmt.Errorf("%w: %T", ErrWrongType, payloadIntf)
	}
	return payload, nil
}
