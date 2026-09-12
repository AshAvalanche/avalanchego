// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package tx_test

import (
	"testing"

	"github.com/ava-labs/libevm/common"
	"github.com/stretchr/testify/require"

	"github.com/ava-labs/avalanchego/chains/atomic"
	"github.com/ava-labs/avalanchego/database/memdb"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/snow"
	"github.com/ava-labs/avalanchego/utils"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/crypto/secp256k1"
	"github.com/ava-labs/avalanchego/vms/components/avax"
	"github.com/ava-labs/avalanchego/vms/saevm/cchain/tx"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"
	"github.com/ava-labs/avalanchego/vms/warpfx"
)

// TestWarpExportDestination is the C-Chain mirror of the P-Chain's rule: a
// warpfx output can only go where it can be decoded.
//
// An export debits before its destination has any say, so getting this wrong
// loses funds irreversibly - hence the refusal in sanityCheck, which runs at
// txpool admission and again from the block builder, and therefore, under the
// rebuild-and-compare model, at verification.
func TestWarpExportDestination(t *testing.T) {
	ctx := mainnetContext()

	warpOut := func() *avax.TransferableOutput {
		return &avax.TransferableOutput{
			Asset: avax.Asset{ID: ctx.AVAXAssetID},
			Out: &warpfx.TransferOutput{
				Amt: 100,
				Owner: warpfx.Owner{
					SourceChainID: ctx.ChainID,
					SourceAddress: ids.GenerateTestShortID().Bytes(),
				},
			},
		}
	}
	secpOut := func() *avax.TransferableOutput {
		return &avax.TransferableOutput{
			Asset: avax.Asset{ID: ctx.AVAXAssetID},
			Out: &secp256k1fx.TransferOutput{
				Amt: 100,
				OutputOwners: secp256k1fx.OutputOwners{
					Threshold: 1,
					Addrs:     []ids.ShortID{ids.GenerateTestShortID()},
				},
			},
		}
	}

	tests := []struct {
		name        string
		destination ids.ID
		outs        []*avax.TransferableOutput
		expectedErr error
	}{
		{
			name:        "a secp256k1 output to the X-Chain",
			destination: ctx.XChainID,
			outs:        []*avax.TransferableOutput{secpOut()},
		},
		{
			name:        "a warpfx output to the P-Chain",
			destination: constants.PlatformChainID,
			outs:        []*avax.TransferableOutput{warpOut()},
		},
		{
			name:        "a warpfx output to the X-Chain",
			destination: ctx.XChainID,
			outs:        []*avax.TransferableOutput{warpOut()},
			expectedErr: tx.ErrWarpOutputWrongDestination,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			export := &tx.Export{
				NetworkID:        ctx.NetworkID,
				BlockchainID:     ctx.ChainID,
				DestinationChain: test.destination,
				Ins: []tx.Input{{
					Address: common.Address{0x01},
					Amount:  100,
					AssetID: ctx.AVAXAssetID,
					Nonce:   1,
				}},
				ExportedOutputs: test.outs,
			}
			signed := &tx.Tx{Unsigned: export}
			require.ErrorIs(t, signed.SanityCheck(ctx), test.expectedErr)
		})
	}
}

// TestCanonicalImport is the mirror of the P-Chain's canonical form.
//
// An authorization is needed to spend, never to receive: this import consumes
// UTXOs that already bear their owner's name and credits that owner's balance.
// There is nothing to authorize, only the carrier to stop from diverting
// anything.
func TestCanonicalImport(t *testing.T) {
	ctx := mainnetContext()

	var (
		address      = ids.GenerateTestShortID()
		otherAddress = ids.GenerateTestShortID()
		owner        = warpfx.Owner{
			SourceChainID: ctx.ChainID,
			SourceAddress: address[:],
		}
	)

	// One UTXO per amount, all held by [utxoOwner].
	newImport := func(utxoOwner warpfx.Owner, credited ids.ShortID, produced uint64, amounts ...uint64) (*tx.Import, []*avax.UTXO) {
		imp := &tx.Import{
			NetworkID:    ctx.NetworkID,
			BlockchainID: ctx.ChainID,
			SourceChain:  constants.PlatformChainID,
			Outs: []tx.Output{{
				Address: common.Address(credited),
				Amount:  produced,
				AssetID: ctx.AVAXAssetID,
			}},
		}
		utxos := make([]*avax.UTXO, len(amounts))
		for i, amount := range amounts {
			utxo := &avax.UTXO{
				UTXOID: avax.UTXOID{TxID: ids.GenerateTestID(), OutputIndex: uint32(i)},
				Asset:  avax.Asset{ID: ctx.AVAXAssetID},
				Out:    &warpfx.TransferOutput{Amt: amount, Owner: utxoOwner},
			}
			utxos[i] = utxo
			imp.ImportedInputs = append(imp.ImportedInputs, &avax.TransferableInput{
				UTXOID: utxo.UTXOID,
				Asset:  utxo.Asset,
				In:     &secp256k1fx.TransferInput{Amt: amount},
			})
		}
		return imp, utxos
	}

	tests := []struct {
		name          string
		mutate        func(*tx.Import, []*avax.UTXO) []tx.Credential
		wantCanonical bool
		expectedErr   error
	}{
		{
			name:          "canonical",
			wantCanonical: true,
		},
		{
			name: "a signed credential",
			mutate: func(*tx.Import, []*avax.UTXO) []tx.Credential {
				return []tx.Credential{&secp256k1fx.Credential{
					Sigs: make([][secp256k1.SignatureLen]byte, 1),
				}}
			},
			expectedErr: tx.ErrCanonicalCredential,
		},
		{
			name: "an input claiming more than its UTXO",
			mutate: func(imp *tx.Import, _ []*avax.UTXO) []tx.Credential {
				imp.ImportedInputs[0].In.(*secp256k1fx.TransferInput).Amt++
				return nil
			},
			expectedErr: tx.ErrCanonicalAmount,
		},
		{
			name: "two outputs",
			mutate: func(imp *tx.Import, _ []*avax.UTXO) []tx.Credential {
				imp.Outs = append(imp.Outs, imp.Outs[0])
				return nil
			},
			expectedErr: tx.ErrCanonicalOutputCount,
		},
		{
			// The whole point: the carrier cannot divert the funds.
			name: "the balance goes to somebody else",
			mutate: func(imp *tx.Import, _ []*avax.UTXO) []tx.Credential {
				imp.Outs[0].Address = common.Address(otherAddress)
				return nil
			},
			expectedErr: tx.ErrCanonicalWrongOutput,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require := require.New(t)

			imp, utxos := newImport(owner, address, 90, 100)
			creds := []tx.Credential{&secp256k1fx.Credential{}}
			if test.mutate != nil {
				if mutated := test.mutate(imp, utxos); mutated != nil {
					creds = mutated
				}
			}

			canonical, err := verifyAgainstMemory(t, ctx, imp, creds, utxos)
			require.ErrorIs(err, test.expectedErr)
			require.Equal(test.wantCanonical, canonical)
		})
	}

	t.Run("several UTXOs of one owner", func(t *testing.T) {
		require := require.New(t)

		imp, utxos := newImport(owner, address, 290, 100, 200)
		creds := []tx.Credential{&secp256k1fx.Credential{}, &secp256k1fx.Credential{}}

		canonical, err := verifyAgainstMemory(t, ctx, imp, creds, utxos)
		require.NoError(err)
		require.True(canonical)
	})

	t.Run("a UTXO owned on another chain", func(t *testing.T) {
		require := require.New(t)

		imp, utxos := newImport(warpfx.Owner{
			SourceChainID: ids.GenerateTestID(),
			SourceAddress: address[:],
		}, address, 90, 100)
		creds := []tx.Credential{&secp256k1fx.Credential{}}

		_, err := verifyAgainstMemory(t, ctx, imp, creds, utxos)
		require.ErrorIs(err, tx.ErrCanonicalWrongSource)
	})
}

// TestNonCanonicalImportRefusesWarpUTXOs is the counterpart, and the reason the
// short-circuit is safe.
//
// The rule reads the shape of the whole batch, never the absence of a
// signature. A batch mixing a warpfx UTXO with a signed one is not canonical:
// it falls back on the ordinary loop, where the warpfx UTXO reaches secp256k1fx
// and is refused. Failing in that direction is the point.
func TestNonCanonicalImportRefusesWarpUTXOs(t *testing.T) {
	require := require.New(t)

	ctx := mainnetContext()
	address := ids.GenerateTestShortID()

	utxos := []*avax.UTXO{
		{
			UTXOID: avax.UTXOID{TxID: ids.GenerateTestID()},
			Asset:  avax.Asset{ID: ctx.AVAXAssetID},
			Out: &warpfx.TransferOutput{Amt: 100, Owner: warpfx.Owner{
				SourceChainID: ctx.ChainID,
				SourceAddress: address[:],
			}},
		},
		{
			// Any-can-spend, so an empty credential satisfies it and the loop
			// reaches the warpfx UTXO whichever order they sort in.
			UTXOID: avax.UTXOID{TxID: ids.GenerateTestID()},
			Asset:  avax.Asset{ID: ctx.AVAXAssetID},
			Out:    &secp256k1fx.TransferOutput{Amt: 200},
		},
	}

	imp := &tx.Import{
		NetworkID:    ctx.NetworkID,
		BlockchainID: ctx.ChainID,
		SourceChain:  constants.PlatformChainID,
		Outs: []tx.Output{{
			Address: common.Address(address),
			Amount:  290,
			AssetID: ctx.AVAXAssetID,
		}},
	}
	for _, utxo := range utxos {
		out, ok := utxo.Out.(avax.TransferableOut)
		require.True(ok)

		imp.ImportedInputs = append(imp.ImportedInputs, &avax.TransferableInput{
			UTXOID: utxo.UTXOID,
			Asset:  utxo.Asset,
			In:     &secp256k1fx.TransferInput{Amt: out.Amount()},
		})
	}
	utils.Sort(imp.ImportedInputs)

	creds := []tx.Credential{&secp256k1fx.Credential{}, &secp256k1fx.Credential{}}
	canonical, err := verifyAgainstMemory(t, ctx, imp, creds, utxos)
	require.False(canonical)
	require.ErrorIs(err, secp256k1fx.ErrWrongUTXOType)
}

// verifyAgainstMemory puts [utxos] into shared memory as the P-Chain would, and
// runs the transaction's credential verification against it.
func verifyAgainstMemory(
	t *testing.T,
	ctx *snow.Context,
	imp *tx.Import,
	creds []tx.Credential,
	utxos []*avax.UTXO,
) (bool, error) {
	t.Helper()

	memory := atomic.NewMemory(memdb.New())
	elems := make([]*atomic.Element, len(utxos))
	for i, utxo := range utxos {
		utxoBytes, err := tx.MarshalUTXO(utxo)
		require.NoError(t, err)

		utxoID := utxo.InputID()
		elems[i] = &atomic.Element{Key: utxoID[:], Value: utxoBytes}
	}
	require.NoError(t, memory.NewSharedMemory(imp.SourceChain).Apply(map[ids.ID]*atomic.Requests{
		ctx.ChainID: {PutRequests: elems},
	}))

	signed := &tx.Tx{Unsigned: imp, Creds: creds}
	return signed.VerifyCredentials(ctx, memory.NewSharedMemory(ctx.ChainID))
}
