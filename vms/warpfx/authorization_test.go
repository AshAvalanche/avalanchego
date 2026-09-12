// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package warpfx

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ava-labs/avalanchego/ids"
)

func TestAuthorizes(t *testing.T) {
	owner := testOwner(1)

	tests := []struct {
		name          string
		authorization *Authorization
		owner         *Owner
		expected      bool
	}{
		{
			name:          "same pair",
			authorization: testAuthorization(1),
			owner:         &owner,
			expected:      true,
		},
		{
			name:          "different address",
			authorization: testAuthorization(2),
			owner:         &owner,
		},
		{
			name: "different source chain",
			authorization: &Authorization{
				SourceChainID: otherChainID,
				SourceAddress: testAddress(1),
			},
			owner: &owner,
		},
		{
			name: "empty source chain",
			authorization: &Authorization{
				SourceChainID: ids.Empty,
				SourceAddress: testAddress(1),
			},
			owner: &owner,
		},
		{
			name: "wrong address length",
			authorization: &Authorization{
				SourceChainID: sourceChainID,
				SourceAddress: bytes.Repeat([]byte{1}, AddressLen-1),
			},
			owner: &owner,
		},
		{
			name:          "nil authorization",
			authorization: nil,
			owner:         &owner,
		},
		{
			name:          "nil owner",
			authorization: testAuthorization(1),
			owner:         nil,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.expected, test.authorization.Authorizes(test.owner))
		})
	}
}
