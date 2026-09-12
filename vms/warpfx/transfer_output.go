// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package warpfx

import (
	"errors"

	"github.com/ava-labs/avalanchego/vms/components/avax"
	"github.com/ava-labs/avalanchego/vms/components/verify"
	"github.com/ava-labs/avalanchego/vms/platformvm/fx"
)

var (
	_ verify.State         = (*TransferOutput)(nil)
	_ avax.TransferableOut = (*TransferOutput)(nil)
	_ avax.Addressable     = (*TransferOutput)(nil)
	_ fx.Owned             = (*TransferOutput)(nil)

	ErrAmtZero = errors.New("amount is zero")
)

// TransferOutput is an amount of an asset held by an [Owner].
//
// It carries no Locktime. A warpfx output is spent against the block's
// chainTime, whereas the P-Chain evaluates locktimes against the node's local
// clock; a locked warpfx output would mix two time bases for one field. See
// also the rule forbidding a stakeable.LockOut around this type.
type TransferOutput struct {
	verify.IsState `json:"-"`

	Amt   uint64 `serialize:"true" json:"amount"`
	Owner `serialize:"true" json:"warpOwner"`
}

func (out *TransferOutput) Amount() uint64 {
	return out.Amt
}

func (out *TransferOutput) Verify() error {
	if out.Amt == 0 {
		return ErrAmtZero
	}
	return out.Owner.Verify()
}

func (out *TransferOutput) Owners() interface{} {
	return &out.Owner
}

// Addresses returns the keys this output is indexed under, both in the P-Chain
// UTXO index and as shared memory traits.
//
// The key is SourceAddress as-is. A trait is never consensus data - the UTXO
// checksum covers only utxoIDs, and provenance always reads the full Owner back
// from the UTXO - so it does not have to be injective, and hashing the Owner
// into a wider key would buy nothing while forcing every reader, Solidity
// included, to reproduce this package's serialization.
func (out *TransferOutput) Addresses() [][]byte {
	return [][]byte{out.SourceAddress}
}
