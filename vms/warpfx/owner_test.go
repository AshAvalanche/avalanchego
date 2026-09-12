// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package warpfx

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/vms/platformvm/fx"
)

func TestOwnerVerify(t *testing.T) {
	tests := []struct {
		name        string
		owner       Owner
		expectedErr error
	}{
		{
			name:  "valid",
			owner: testOwner(1),
		},
		{
			name: "empty source chain",
			owner: Owner{
				SourceChainID: ids.Empty,
				SourceAddress: testAddress(1),
			},
			expectedErr: ErrEmptySourceChain,
		},
		{
			name: "address too short",
			owner: Owner{
				SourceChainID: sourceChainID,
				SourceAddress: bytes.Repeat([]byte{1}, AddressLen-1),
			},
			expectedErr: ErrWrongAddressLength,
		},
		{
			name: "address too long",
			owner: Owner{
				SourceChainID: sourceChainID,
				SourceAddress: bytes.Repeat([]byte{1}, AddressLen+1),
			},
			expectedErr: ErrWrongAddressLength,
		},
		{
			name: "nil address",
			owner: Owner{
				SourceChainID: sourceChainID,
			},
			expectedErr: ErrWrongAddressLength,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.ErrorIs(t, test.owner.Verify(), test.expectedErr)
		})
	}
}

// TestOwnerIsAnFxOwner locks the interface a rewards owner must satisfy. It is
// what lets a warp owner be named ValidatorRewardsOwner months before
// CreateOutput is reached.
func TestOwnerIsAnFxOwner(t *testing.T) {
	var owner fx.Owner = &Owner{}
	require.NotNil(t, owner)
}
