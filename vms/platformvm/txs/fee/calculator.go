// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package fee

import (
	"errors"

	"github.com/ava-labs/avalanchego/vms/platformvm/platform"
)

var ErrUnsupportedTx = errors.New("unsupported transaction type")

// Calculator calculates the minimum required fee, in nAVAX, that a transaction
// must pay for valid inclusion into a block.
type Calculator interface {
	CalculateFee(tx platform.UnsignedTx) (uint64, error)

	// CalculateFeeWithCredentials is CalculateFee for a caller holding the
	// signed transaction, and it is what prices a Warp authorization - which
	// lives in the credentials and is invisible from the unsigned form.
	//
	// A second method rather than a wider signature on the first: CalculateFee
	// has callers that legitimately have no signed transaction to hand -
	// construction, wallet-side estimation - and making them fabricate one
	// would be worse than the duplication.
	CalculateFeeWithCredentials(tx *platform.Tx) (uint64, error)
}
