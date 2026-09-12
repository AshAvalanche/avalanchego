// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package warpfx

import (
	"errors"
	"math"

	"github.com/holiman/uint256"
)

const MaxFeeOverpaymentFactor = 2

var (
	ErrInsufficientFee = errors.New("insufficient fee")
	ErrExcessiveBurn   = errors.New("excessive burn fee")
	ErrBidTooHigh      = errors.New("bid too high")
	ErrNilFee          = errors.New("nil fee parameter")
)

func VerifyFeeBand(consumed, produced, fee uint64) error {
	if consumed < produced {
		return ErrInsufficientFee
	}

	actualFee := consumed - produced
	if actualFee < fee {
		return ErrInsufficientFee
	}

	if fee > math.MaxUint64/MaxFeeOverpaymentFactor {
		return ErrExcessiveBurn
	}

	if actualFee > MaxFeeOverpaymentFactor*fee {
		return ErrExcessiveBurn
	}

	return nil
}

// VerifyCanonicalBid refuses a bid above k times the base fee.
//
// [minimumBid] is the smallest bid the submitter is able to express, and the
// ceiling is never allowed below it. Burns are quantized - one nAVAX on the
// C-chain, which is 1e9 aAVAX spread over the transaction's gas - while the
// base fee is not: under ACP-283 the C-chain's minimum gas price starts at one
// wei and returns there whenever the chain is idle. Without this floor the
// ceiling would sit four to five orders of magnitude below the cheapest bid
// anyone can offer, and no canonical import would be includable at all -
// precisely when the chain is cheapest.
//
// The floor costs nothing it was protecting: a third party can then burn one
// quantum, which is one nAVAX.
func VerifyCanonicalBid(gasFeeCap, baseFee, minimumBid *uint256.Int) error {
	if gasFeeCap == nil || baseFee == nil || minimumBid == nil {
		return ErrNilFee
	}

	maxAllowedBid := new(uint256.Int).Mul(uint256.NewInt(MaxFeeOverpaymentFactor), baseFee)
	if maxAllowedBid.Lt(minimumBid) {
		maxAllowedBid = minimumBid
	}
	if gasFeeCap.Cmp(maxAllowedBid) > 0 {
		return ErrBidTooHigh
	}

	return nil
}
