// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package fee

import (
	"github.com/ava-labs/avalanchego/vms/components/gas"
	"github.com/ava-labs/avalanchego/vms/components/verify"
	"github.com/ava-labs/avalanchego/vms/platformvm/platform"
	"github.com/ava-labs/avalanchego/vms/warpfx"
)

// CredentialComplexity returns the complexity a transaction's credentials add
// on top of its unsigned complexity.
//
// It exists because a Warp authorization cannot be seen from the unsigned
// transaction: the Warp messages ACP-77 prices are *fields* of it, while an
// authorization contains its very bytes. Left alone, verifying an aggregated
// BLS signature over a thousand signers would be free - the kind of hole nobody
// notices, because nothing breaks and only the fees are too low.
//
// secp256k1 credentials are deliberately not priced here: their inputs'
// SigIndices already charge them, and counting twice would be a regression.
//
// The message is priced once, exactly one credential carrying it - which is
// also why it must not be repeated in every slot, N copies being gossiped and
// stored for the price of one.
func CredentialComplexity(creds []verify.Verifiable) (gas.Dimensions, error) {
	for _, cred := range creds {
		warpCred, ok := cred.(*warpfx.Credential)
		if !ok || len(warpCred.WarpMessage) == 0 {
			continue
		}

		// The cost model is the one ACP-77 already applies to a Warp message,
		// and there is nothing new to invent: bandwidth linear in the message,
		// a flat state-read allowance for the canonical set, and compute linear
		// in the number of signers.
		return WarpComplexity(warpCred.WarpMessage)
	}
	return gas.Dimensions{}, nil
}

// SignedTxComplexity returns the complexity of a signed transaction: what its
// unsigned form costs, plus what its credentials add.
func SignedTxComplexity(tx *platform.Tx) (gas.Dimensions, error) {
	complexity, err := TxComplexity(tx.Unsigned)
	if err != nil {
		return gas.Dimensions{}, err
	}

	credentialComplexity, err := CredentialComplexity(tx.Creds)
	if err != nil {
		return gas.Dimensions{}, err
	}

	return complexity.Add(&credentialComplexity)
}
