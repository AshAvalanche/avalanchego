// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package executor

import (
	"errors"
	"fmt"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/math"
	"github.com/ava-labs/avalanchego/vms/components/avax"
	"github.com/ava-labs/avalanchego/vms/components/verify"
	"github.com/ava-labs/avalanchego/vms/platformvm/fx"
	"github.com/ava-labs/avalanchego/vms/platformvm/platform"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"
	"github.com/ava-labs/avalanchego/vms/warpfx"
)

var (
	ErrCanonicalImportCredential  = errors.New("canonical import credential is not an empty warp credential")
	ErrCanonicalImportMixedOwners = errors.New("canonical import consumes UTXOs of different owners")
	ErrCanonicalImportAsset       = errors.New("canonical import handles an asset other than AVAX")
	ErrCanonicalImportInput       = errors.New("canonical import input presents something")
	ErrCanonicalImportAmount      = errors.New("canonical import input amount does not match its UTXO")
	ErrCanonicalImportOutputCount = errors.New("canonical import does not produce exactly one output")
	ErrCanonicalImportWrongOutput = errors.New("canonical import does not return the funds to their owner")
)

// isCanonicalImport reports whether [tx] is an import whose entire content is
// derived from the UTXOs it consumes, rather than composed by an author.
//
// An authorization is needed to *spend*, never to *receive*. An import is value
// neutral, so it needs no Warp message, no aggregation, no relayer and no
// expiry - which is what makes funding a warp owner free of authorizations end
// to end. Only the carrier must be stopped from diverting anything. Three
// conditions do that, and their conjunction is what licenses skipping the Fx
// in verifyCanonicalImport:
//
//  1. no carrier credential was resolved;
//  2. Ins is empty, so nothing already on the P-chain is consumed, and the
//     content stays derivable from what the submitter reads;
//  3. every imported UTXO is held by a warp owner.
//
// Failing any of them falls back on the ordinary path, where a warpfx UTXO
// reaches the Fx with a nil context and is refused on ErrNoAuthorization.
func isCanonicalImport(fxCtx *fx.Context, tx *platform.ImportTx, utxos []*avax.UTXO) bool {
	if fxCtx == nil || fxCtx.Authorization != nil {
		return false
	}
	if len(tx.Ins) != 0 || len(utxos) == 0 {
		return false
	}
	for _, utxo := range utxos {
		if _, ok := utxo.Out.(*warpfx.TransferOutput); !ok {
			return false
		}
	}
	return true
}

// verifyCanonicalImport checks the constraints that stand in for a signature.
//
// Callers must have established isCanonicalImport first. Generalized to "no
// carrier credential, therefore no Fx call", this would make every warpfx UTXO
// spendable by anyone from any transaction.
//
// Skipping the Fx means taking over what it did: the amount equality it
// enforces, the shape of the input it accepts, and the verification of the
// output it consumes. Left out, those checks would simply be lost.
//
// What is left to the submitter afterwards is whether to submit, when, and the
// exact amount within the fee band. Nothing else: funds can be neither
// diverted, nor split, nor frozen.
func verifyCanonicalImport(
	tx *platform.ImportTx,
	creds []verify.Verifiable,
	utxos []*avax.UTXO,
	avaxAssetID ids.ID,
	fee uint64,
) error {
	if len(creds) != len(tx.ImportedInputs) || len(utxos) != len(tx.ImportedInputs) {
		return fmt.Errorf(
			"%w: %d credentials and %d utxos for %d inputs",
			ErrCanonicalImportCredential,
			len(creds),
			len(utxos),
			len(tx.ImportedInputs),
		)
	}

	var (
		owner    *warpfx.Owner
		consumed uint64
	)
	for i, in := range tx.ImportedInputs {
		// Every slot facing a warpfx input carries an empty credential. A
		// non-empty one would be a carrier, and isCanonicalImport would have
		// sent this transaction down the authorized path.
		cred, ok := creds[i].(*warpfx.Credential)
		if !ok || len(cred.WarpMessage) != 0 {
			return fmt.Errorf("%w: credential %d", ErrCanonicalImportCredential, i)
		}

		// Safe: isCanonicalImport established the type of every UTXO.
		out := utxos[i].Out.(*warpfx.TransferOutput)
		if err := out.Verify(); err != nil {
			return fmt.Errorf("imported utxo %d failed verification: %w", i, err)
		}

		if owner == nil {
			owner = &out.Owner
		} else if !owner.Equals(&out.Owner) {
			return fmt.Errorf("%w: utxo %d", ErrCanonicalImportMixedOwners, i)
		}

		if utxos[i].AssetID() != avaxAssetID || in.AssetID() != avaxAssetID {
			return fmt.Errorf("%w: input %d", ErrCanonicalImportAsset, i)
		}

		// No input type of its own, exactly as on the authorized path: a
		// secp256k1fx.TransferInput with no signature indices already means
		// "this input presents nothing".
		transferIn, ok := in.In.(*secp256k1fx.TransferInput)
		if !ok || len(transferIn.SigIndices) != 0 {
			return fmt.Errorf("%w: input %d", ErrCanonicalImportInput, i)
		}

		if transferIn.Amt != out.Amt {
			return fmt.Errorf(
				"%w: input %d claims %d for a utxo of %d",
				ErrCanonicalImportAmount,
				i,
				transferIn.Amt,
				out.Amt,
			)
		}

		newConsumed, err := math.Add(consumed, out.Amt)
		if err != nil {
			return fmt.Errorf("adding consumed amounts: %w", err)
		}
		consumed = newConsumed
	}

	if len(tx.Outs) != 1 {
		return fmt.Errorf("%w: %d outputs", ErrCanonicalImportOutputCount, len(tx.Outs))
	}

	produced := tx.Outs[0]
	if produced.AssetID() != avaxAssetID {
		return fmt.Errorf("%w: output", ErrCanonicalImportAsset)
	}
	producedOut, ok := produced.Out.(*warpfx.TransferOutput)
	if !ok || !producedOut.Owner.Equals(owner) {
		return ErrCanonicalImportWrongOutput
	}

	// The fee band, which has no equivalent in the signed world and whose
	// absence would cost funds.
	//
	// Both flow checkers only test produced <= consumed and burn any surplus
	// with no ceiling. For a signed transaction that slack is harmless: the
	// owner signs, and nobody robs themselves. Here nobody signs, so under a
	// plain inequality anyone could consume a 1,000 AVAX UTXO, hand back one
	// nAVAX and burn the rest. The attacker gains nothing - it is pure
	// destruction - and it costs them nothing either, an atomic transaction
	// having no gas payer.
	//
	// A strict equality would be worse in the other direction: it makes a
	// transaction valid for exactly one fee value, and P-chain fees move fast
	// enough that it would not survive propagation latency alone. The band is
	// what leaves validity breaking only upwards, while bounding the
	// destruction to (k-1) x fee.
	return warpfx.VerifyFeeBand(consumed, producedOut.Amt, fee)
}
