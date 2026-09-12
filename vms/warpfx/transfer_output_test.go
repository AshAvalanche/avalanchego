// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package warpfx

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ava-labs/avalanchego/ids"
)

func TestTransferOutputVerify(t *testing.T) {
	tests := []struct {
		name        string
		out         TransferOutput
		expectedErr error
	}{
		{
			name: "valid",
			out:  TransferOutput{Amt: 1, Owner: testOwner(1)},
		},
		{
			name:        "zero amount",
			out:         TransferOutput{Amt: 0, Owner: testOwner(1)},
			expectedErr: ErrAmtZero,
		},
		{
			name: "invalid owner",
			out: TransferOutput{
				Amt:   1,
				Owner: Owner{SourceChainID: ids.Empty, SourceAddress: testAddress(1)},
			},
			expectedErr: ErrEmptySourceChain,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.ErrorIs(t, test.out.Verify(), test.expectedErr)
		})
	}
}

// TestTransferOutputAddresses locks the discovery key.
//
// It is a single 20-byte key equal to SourceAddress, which is what lets
// avax.GetAtomicUTXOs, state.UTXOIDs and ParseServiceAddress serve a warp owner
// with no adaptation at all.
func TestTransferOutputAddresses(t *testing.T) {
	require := require.New(t)

	out := &TransferOutput{Amt: 1, Owner: testOwner(0x42)}

	addrs := out.Addresses()
	require.Len(addrs, 1)
	require.Len(addrs[0], AddressLen)
	require.Equal(out.SourceAddress, addrs[0])
}

func TestTransferOutputOwners(t *testing.T) {
	out := &TransferOutput{Amt: 1, Owner: testOwner(1)}
	require.Equal(t, &out.Owner, out.Owners())
}
