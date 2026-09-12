// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package tx

import (
	"errors"
	"fmt"

	"github.com/ava-labs/libevm/common"

	"github.com/ava-labs/avalanchego/snow"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/vms/components/avax"
	"github.com/ava-labs/avalanchego/vms/warpfx"
)

var (
	errWarpOutputWrongDestination = errors.New("warp output exported to a chain other than the P-Chain")

	errCanonicalMixedOwners = errors.New("canonical import consumes UTXOs of different owners")
	errCanonicalWrongSource = errors.New("canonical import consumes a UTXO owned on another chain")
	errCanonicalAmount      = errors.New("canonical import input amount does not match its UTXO")
	errCanonicalOutputCount = errors.New("canonical import does not produce exactly one output")
	errCanonicalWrongOutput = errors.New("canonical import does not return the funds to their owner")
	errCanonicalCredential  = errors.New("canonical import credential is not empty")
)

// verifyWarpExportDestination refuses to export a warpfx output anywhere but
// the P-Chain, which is the only chain that can decode one.
//
// The mirror of the P-Chain's own rule, and the placement matters for the same
// reason: an export debits before its destination has any say. sanityCheck runs
// at txpool admission and again from PotentialEndOfBlockOps, so under the
// rebuild-and-compare model it runs at verification too.
func verifyWarpExportDestination(e *Export) error {
	for i, out := range e.ExportedOutputs {
		if _, ok := out.Out.(*warpfx.TransferOutput); !ok {
			continue
		}
		if e.DestinationChain != constants.PlatformChainID {
			return fmt.Errorf(
				"%w (%d): %s",
				errWarpOutputWrongDestination,
				i,
				e.DestinationChain,
			)
		}
	}
	return nil
}

// isCanonicalImport reports whether every consumed UTXO is held by a warp
// owner, which is what licenses skipping the Fx in verifyCanonicalImport.
//
// It reads the shape of the whole batch, never the absence of a signature. A
// batch mixing a warpfx UTXO with a signed one is not canonical: it falls back
// on the ordinary loop, where the warpfx UTXO reaches secp256k1fx and is
// refused on ErrWrongUTXOType. Failing in that direction is the point of the
// rule.
func isCanonicalImport(utxos []*avax.UTXO) bool {
	if len(utxos) == 0 {
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
// An authorization is needed to spend, never to receive. This import consumes
// UTXOs that already bear their owner's name and credits that owner's C-Chain
// balance: there is nothing to authorize, only the carrier to stop from
// diverting anything.
//
// What the carrier is left with is whether to submit, when, and how much to
// burn - and that last freedom is bounded elsewhere, by the bid ceiling the
// block builder applies. Here the burn has no ceiling of its own, because on
// this VM the burned amount is not a fee but a bid: sanityCheck produces no fee
// term at all, only produced <= consumed.
func verifyCanonicalImport(
	ctx *snow.Context,
	i *Import,
	creds []Credential,
	utxos []*avax.UTXO,
) error {
	var owner *warpfx.Owner
	for j, in := range i.ImportedInputs {
		// An input that presents nothing is an empty secp256k1fx.Credential,
		// not a warpfx one - that is the encoding both codecs agree on, since
		// tx.Credential is Self() *secp256k1fx.Credential here and warpfx's
		// credential is not even registered in the atomic codec. On the P-Chain
		// the empty credential is a warpfx one instead; both are right at home.
		//
		// It is also what keeps the transaction byte-identical for two
		// submitters rebuilding it independently, hence deduplicable by the
		// mempool.
		if cred := creds[j].Self(); len(cred.Sigs) != 0 {
			return fmt.Errorf("%w (%d): %d signatures", errCanonicalCredential, j, len(cred.Sigs))
		}

		// Safe: isCanonicalImport established the type of every UTXO.
		out := utxos[j].Out.(*warpfx.TransferOutput)
		if err := out.Verify(); err != nil {
			return fmt.Errorf("%w (%d): %w", errInvalidOutput, j, err)
		}

		if owner == nil {
			owner = &out.Owner
		} else if !owner.Equals(&out.Owner) {
			return fmt.Errorf("%w (%d)", errCanonicalMixedOwners, j)
		}

		// The owner must live on this chain. A UTXO naming another chain's
		// address would be credited to a stranger holding the same twenty
		// bytes here.
		if out.Owner.SourceChainID != ctx.ChainID {
			return fmt.Errorf(
				"%w (%d): want %s, got %s",
				errCanonicalWrongSource,
				j,
				ctx.ChainID,
				out.Owner.SourceChainID,
			)
		}

		if in.In.Amount() != out.Amt {
			return fmt.Errorf(
				"%w (%d): input claims %d for a utxo of %d",
				errCanonicalAmount,
				j,
				in.In.Amount(),
				out.Amt,
			)
		}
	}

	if len(i.Outs) != 1 {
		return fmt.Errorf("%w: %d outputs", errCanonicalOutputCount, len(i.Outs))
	}
	// The asset constraint is redundant here - sanityCheck already requires
	// every input and output to be AVAX - and is kept out for that reason.
	if i.Outs[0].Address != common.Address(owner.SourceAddress) {
		return fmt.Errorf(
			"%w: want %s, got %s",
			errCanonicalWrongOutput,
			common.Address(owner.SourceAddress),
			i.Outs[0].Address,
		)
	}
	return nil
}
