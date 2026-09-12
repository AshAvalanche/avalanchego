// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package warpfx

import (
	"bytes"

	"github.com/ava-labs/avalanchego/ids"
)

// Authorization is a verified Warp authorization reduced to what provenance
// needs.
//
// It is never serialized, never persisted, and never stored in a struct: it is
// passed as an argument of the verification it authorizes, and forgotten.
type Authorization struct {
	SourceChainID ids.ID
	SourceAddress []byte
}

// Authorizes reports whether this authorization was issued by o.
func (a *Authorization) Authorizes(o *Owner) bool {
	if a == nil || o == nil ||
		a.SourceChainID == ids.Empty ||
		len(a.SourceAddress) != AddressLen {
		return false
	}
	return a.SourceChainID == o.SourceChainID &&
		bytes.Equal(a.SourceAddress, o.SourceAddress)
}
