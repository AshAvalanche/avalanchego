// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package warpfx

import (
	"bytes"
	"errors"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/snow"
	"github.com/ava-labs/avalanchego/vms/components/verify"
	"github.com/ava-labs/avalanchego/vms/platformvm/fx"
)

// AddressLen is the exact length an [Owner.SourceAddress] must have.
//
// The field is typed []byte to stay agnostic of the source chain's address
// format, but the length is pinned because the owner must be able to *receive*
// its funds back: a C-Chain import credits a 20-byte address. An owner holding
// a longer address would produce outputs that are perfectly constructible and
// definitively un-importable.
const AddressLen = ids.ShortIDLen

var (
	_ fx.Owner = (*Owner)(nil)

	ErrEmptySourceChain   = errors.New("empty source chain")
	ErrWrongAddressLength = errors.New("wrong address length")
)

// Owner identifies the holder of a [TransferOutput] by the chain and address it
// lives on, rather than by a set of public keys.
//
// It is the pair an AddressedCall names, and the Warp precompile forces the
// address to its caller, so the pair says nothing about whether the address
// holds code.
type Owner struct {
	verify.IsNotState `json:"-"`

	SourceChainID ids.ID `serialize:"true" json:"sourceChainID"`
	SourceAddress []byte `serialize:"true" json:"sourceAddress"`
}

func (o *Owner) Verify() error {
	switch {
	case o.SourceChainID == ids.Empty:
		return ErrEmptySourceChain
	case len(o.SourceAddress) != AddressLen:
		return ErrWrongAddressLength
	default:
		return nil
	}
}

// Equals reports whether o and other name the same holder.
func (o *Owner) Equals(other *Owner) bool {
	return o != nil && other != nil &&
		o.SourceChainID == other.SourceChainID &&
		bytes.Equal(o.SourceAddress, other.SourceAddress)
}

// InitCtx is a no-op. Unlike a secp256k1fx owner, there is no Bech32 rendering
// to prepare: SourceAddress is displayed as-is.
func (*Owner) InitCtx(*snow.Context) {}
