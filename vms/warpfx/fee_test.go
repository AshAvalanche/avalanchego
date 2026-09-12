// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package warpfx

import (
	"math"
	"testing"

	"github.com/holiman/uint256"
	"github.com/stretchr/testify/require"
)

// TestVerifyFeeBand covers the P-Chain side, where the burned amount is a fee.
//
// The lower bound is what has no equivalent in the signed world: both flow
// checkers only test produced <= consumed and burn any surplus with no ceiling,
// which is harmless when the owner signs and nobody robs themselves. Here
// nobody signs.
func TestVerifyFeeBand(t *testing.T) {
	tests := []struct {
		name        string
		consumed    uint64
		produced    uint64
		fee         uint64
		expectedErr error
	}{
		{
			name:     "exactly the fee",
			consumed: 1000,
			produced: 900,
			fee:      100,
		},
		{
			name:     "exactly k times the fee",
			consumed: 1000,
			produced: 800,
			fee:      100,
		},
		{
			name:        "one below the fee",
			consumed:    1000,
			produced:    901,
			fee:         100,
			expectedErr: ErrInsufficientFee,
		},
		{
			name:        "one past k times the fee",
			consumed:    1000,
			produced:    799,
			fee:         100,
			expectedErr: ErrExcessiveBurn,
		},
		{
			name:        "producing more than consumed",
			consumed:    1000,
			produced:    1001,
			fee:         100,
			expectedErr: ErrInsufficientFee,
		},
		{
			name:     "a zero fee demands exact conservation",
			consumed: 1000,
			produced: 1000,
		},
		{
			name:        "a zero fee refuses any burn",
			consumed:    1000,
			produced:    999,
			expectedErr: ErrExcessiveBurn,
		},
		{
			// k * fee must not wrap around into an accidental permission.
			name:        "a fee that would overflow k times over",
			consumed:    math.MaxUint64,
			produced:    0,
			fee:         math.MaxUint64/MaxFeeOverpaymentFactor + 1,
			expectedErr: ErrExcessiveBurn,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.ErrorIs(
				t,
				VerifyFeeBand(test.consumed, test.produced, test.fee),
				test.expectedErr,
			)
		})
	}
}

// TestVerifyCanonicalBid covers the C-Chain side, where the burned amount is
// not a fee but a bid - converted into an offered gas price, from which the fee
// mechanism decides inclusion.
//
// The displacement makes the attack more attractive, not less: where the burn
// is a fee, destroying everything gains its author nothing; where it is a bid,
// it maximizes inclusion priority.
func TestVerifyCanonicalBid(t *testing.T) {
	tests := []struct {
		name        string
		gasFeeCap   *uint256.Int
		baseFee     *uint256.Int
		minimumBid  *uint256.Int
		expectedErr error
	}{
		{
			name:      "bidding the base fee",
			gasFeeCap: uint256.NewInt(100),
			baseFee:   uint256.NewInt(100),
		},
		{
			name:      "bidding below it",
			gasFeeCap: uint256.NewInt(50),
			baseFee:   uint256.NewInt(100),
		},
		{
			name:      "bidding exactly k times it",
			gasFeeCap: uint256.NewInt(MaxFeeOverpaymentFactor * 100),
			baseFee:   uint256.NewInt(100),
		},
		{
			name:        "bidding one above",
			gasFeeCap:   uint256.NewInt(MaxFeeOverpaymentFactor*100 + 1),
			baseFee:     uint256.NewInt(100),
			expectedErr: ErrBidTooHigh,
		},
		{
			// The floor. A burn is quantized at one nAVAX, so below this the
			// ceiling would forbid every bid anyone can express - and it would
			// do so exactly when the chain is cheapest.
			name:       "the minimum expressible bid, far above k times a floor base fee",
			gasFeeCap:  uint256.NewInt(97_087),
			baseFee:    uint256.NewInt(1),
			minimumBid: uint256.NewInt(97_087),
		},
		{
			name:        "one above the minimum expressible bid",
			gasFeeCap:   uint256.NewInt(97_088),
			baseFee:     uint256.NewInt(1),
			minimumBid:  uint256.NewInt(97_087),
			expectedErr: ErrBidTooHigh,
		},
		{
			// And the floor never lowers the ceiling.
			name:       "the floor does not shrink a healthy ceiling",
			gasFeeCap:  uint256.NewInt(MaxFeeOverpaymentFactor * 100),
			baseFee:    uint256.NewInt(100),
			minimumBid: uint256.NewInt(1),
		},
		{
			name:        "a nil bid",
			baseFee:     uint256.NewInt(100),
			expectedErr: ErrNilFee,
		},
		{
			name:        "a nil base fee",
			gasFeeCap:   uint256.NewInt(100),
			expectedErr: ErrNilFee,
		},
		{
			name:        "a nil minimum bid",
			gasFeeCap:   uint256.NewInt(100),
			baseFee:     uint256.NewInt(100),
			expectedErr: ErrNilFee,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			minimumBid := test.minimumBid
			if minimumBid == nil && test.expectedErr != ErrNilFee {
				minimumBid = uint256.NewInt(0)
			}
			require.ErrorIs(
				t,
				VerifyCanonicalBid(test.gasFeeCap, test.baseFee, minimumBid),
				test.expectedErr,
			)
		})
	}
}
