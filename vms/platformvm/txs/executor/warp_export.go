// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package executor

import (
	"errors"
	"fmt"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/vms/platformvm/platform"
	"github.com/ava-labs/avalanchego/vms/platformvm/stakeable"
	"github.com/ava-labs/avalanchego/vms/warpfx"
)

var ErrWarpOutputWrongDestination = errors.New("warp output exported to a chain other than the C-chain")

// verifyWarpExportDestination refuses to export a warpfx output anywhere but
// the C-chain.
//
// warpfx.TransferOutput is registered in the PlatformVM's codec and in saevm's,
// nowhere else. An export carrying it elsewhere would produce a UTXO its
// destination can never decode, and an export debits before the destination has
// any say: the mistake is irreversible.
//
// The rule names the C-chain rather than "any chain that can decode this",
// because that set is exactly {C-chain} here and a rule saying so is checkable;
// the general phrasing would ask the P-chain to know other chains' codecs.
//
// Note the opposite shape from the check beside it: ExportTx.SyntacticVerify
// rejects a named type, *stakeable.LockOut. This one lists what it allows.
func verifyWarpExportDestination(cChainID ids.ID, tx *platform.ExportTx) error {
	for _, out := range tx.ExportedOutputs {
		outIntf := out.Out
		// Unwrapped first, or a locked warpfx output would slip past on its
		// wrapper's type.
		if lockOut, ok := outIntf.(*stakeable.LockOut); ok {
			outIntf = lockOut.TransferableOut
		}
		if _, ok := outIntf.(*warpfx.TransferOutput); !ok {
			continue
		}

		if tx.DestinationChain != cChainID {
			return fmt.Errorf(
				"%w: %s",
				ErrWarpOutputWrongDestination,
				tx.DestinationChain,
			)
		}
	}
	return nil
}
