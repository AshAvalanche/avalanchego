// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package platform

import (
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/vms/components/avax"
	"github.com/ava-labs/avalanchego/vms/platformvm/stakeable"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"
	"github.com/ava-labs/avalanchego/vms/warpfx"
)

// outputFxID returns the feature extension an output belongs to, for display.
//
// FxID is serialize:"false" and only feeds JSON rendering, so getting it wrong
// costs no consensus - it costs diagnosis. The first person investigating a
// warpfx UTXO would read "secp256k1fx" in every API and explorer, and go
// looking in the wrong place.
//
// There is no matching helper for inputs, and that asymmetry is not an
// oversight: warpfx declares no input type of its own. An input spending a
// warpfx UTXO really is a secp256k1fx.TransferInput, with no signature indices,
// so secp256k1fx.ID is the honest answer for it.
func outputFxID(out *avax.TransferableOutput) ids.ID {
	outIntf := out.Out
	if lockOut, ok := outIntf.(*stakeable.LockOut); ok {
		outIntf = lockOut.TransferableOut
	}
	if _, ok := outIntf.(*warpfx.TransferOutput); ok {
		return warpfx.ID
	}
	return secp256k1fx.ID
}
