// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package fee

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/crypto/bls"
	"github.com/ava-labs/avalanchego/utils/crypto/bls/signer/localsigner"
	"github.com/ava-labs/avalanchego/utils/set"
	"github.com/ava-labs/avalanchego/utils/wrappers"
	"github.com/ava-labs/avalanchego/vms/components/avax"
	"github.com/ava-labs/avalanchego/vms/components/gas"
	"github.com/ava-labs/avalanchego/vms/components/verify"
	"github.com/ava-labs/avalanchego/vms/platformvm/fx"
	"github.com/ava-labs/avalanchego/vms/platformvm/platform"
	"github.com/ava-labs/avalanchego/vms/platformvm/stakeable"
	"github.com/ava-labs/avalanchego/vms/platformvm/warp"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"
	"github.com/ava-labs/avalanchego/vms/warpfx"
)

func testWarpOwner() warpfx.Owner {
	return warpfx.Owner{
		SourceChainID: ids.GenerateTestID(),
		SourceAddress: ids.GenerateTestShortID().Bytes(),
	}
}

// TestWarpFxBandwidthMatchesSerialization checks the pricing constants against
// what the codec actually writes, rather than against the arithmetic that
// produced them.
//
// This is exactly the kind of value one shifts by a length prefix, and getting
// it wrong under-prices bandwidth forever.
func TestWarpFxBandwidthMatchesSerialization(t *testing.T) {
	require := require.New(t)

	// The codec prepends a two-byte version once per message; the intrinsic
	// bandwidths do not include it.
	const codecVersionLen = 2

	out := &avax.TransferableOutput{
		Asset: avax.Asset{ID: ids.GenerateTestID()},
		Out: &warpfx.TransferOutput{
			Amt:   1,
			Owner: testWarpOwner(),
		},
	}
	outBytes, err := platform.Codec.Marshal(platform.CodecVersion, out)
	require.NoError(err)
	require.Len(
		outBytes,
		codecVersionLen+intrinsicOutputBandwidth+intrinsicWarpFxOutputBandwidth,
	)

	owner := testWarpOwner()
	var ownerIntf fx.Owner = &owner
	ownerBytes, err := platform.Codec.Marshal(platform.CodecVersion, &ownerIntf)
	require.NoError(err)
	require.Len(
		ownerBytes,
		// OwnerComplexity deliberately excludes the owner's own typeID, which
		// the interface field above does write.
		codecVersionLen+wrappers.IntLen+intrinsicWarpFxOwnerBandwidth,
	)
}

func TestWarpFxOutputComplexity(t *testing.T) {
	tests := []struct {
		name        string
		out         *avax.TransferableOutput
		expected    gas.Dimensions
		expectedErr error
	}{
		{
			name: "warp output",
			out: &avax.TransferableOutput{
				Out: &warpfx.TransferOutput{Owner: testWarpOwner()},
			},
			expected: gas.Dimensions{
				gas.Bandwidth: intrinsicOutputBandwidth + intrinsicWarpFxOutputBandwidth,
				gas.DBWrite:   1,
			},
		},
		{
			name: "locked warp output",
			out: &avax.TransferableOutput{
				Out: &stakeable.LockOut{
					Locktime:        1,
					TransferableOut: &warpfx.TransferOutput{Owner: testWarpOwner()},
				},
			},
			expected: gas.Dimensions{
				gas.Bandwidth: intrinsicOutputBandwidth + intrinsicStakeableLockedOutputBandwidth + intrinsicWarpFxOutputBandwidth,
				gas.DBWrite:   1,
			},
		},
		{
			// Non-regression: opening the dispatch must not change what a
			// secp256k1 output costs.
			name: "one secp256k1 owner",
			out: &avax.TransferableOutput{
				Out: &secp256k1fx.TransferOutput{
					OutputOwners: secp256k1fx.OutputOwners{
						Addrs: make([]ids.ShortID, 1),
					},
				},
			},
			expected: gas.Dimensions{
				gas.Bandwidth: 80,
				gas.DBWrite:   1,
			},
		},
		{
			name:        "still refuses an unknown type",
			out:         &avax.TransferableOutput{Out: &avax.TestTransferable{}},
			expectedErr: errUnsupportedOutput,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require := require.New(t)

			actual, err := outputComplexity(test.out)
			require.ErrorIs(err, test.expectedErr)
			require.Equal(test.expected, actual)
		})
	}
}

func TestWarpFxOwnerComplexity(t *testing.T) {
	require := require.New(t)

	owner := testWarpOwner()
	actual, err := OwnerComplexity(&owner)
	require.NoError(err)
	require.Equal(gas.Dimensions{gas.Bandwidth: intrinsicWarpFxOwnerBandwidth}, actual)
}

// newTestWarpMessage builds a signed Warp message with [numSigners] signers.
func newTestWarpMessage(t *testing.T, numSigners int) []byte {
	t.Helper()

	unsigned, err := warp.NewUnsignedMessage(constants.UnitTestID, ids.GenerateTestID(), []byte{0x01})
	require.NoError(t, err)

	sigs := make([]*bls.Signature, numSigners)
	signers := set.NewBits()
	for i := range numSigners {
		sk, err := localsigner.New()
		require.NoError(t, err)

		sigs[i], err = sk.Sign(unsigned.Bytes())
		require.NoError(t, err)
		signers.Add(i)
	}

	aggregated, err := bls.AggregateSignatures(sigs)
	require.NoError(t, err)

	msg, err := warp.NewMessage(unsigned, &warp.BitSetSignature{
		Signers:   signers.Bytes(),
		Signature: [bls.SignatureLen]byte(bls.SignatureToBytes(aggregated)),
	})
	require.NoError(t, err)
	return msg.Bytes()
}

// TestCredentialComplexity is the heart of the lot: an authorization lives in
// the credentials, so it cannot be seen from the unsigned transaction, and left
// unpriced the aggregated BLS verification of a set of more than a thousand
// signers would be free.
func TestCredentialComplexity(t *testing.T) {
	oneSigner := newTestWarpMessage(t, 1)
	fourSigners := newTestWarpMessage(t, 4)

	tests := []struct {
		name  string
		creds []verify.Verifiable
		empty bool
	}{
		{
			name:  "no credential",
			empty: true,
		},
		{
			// Already charged through their inputs' SigIndices; counting them
			// again would be a regression.
			name: "secp256k1 credentials are not priced here",
			creds: []verify.Verifiable{
				&secp256k1fx.Credential{Sigs: make([][65]byte, 3)},
			},
			empty: true,
		},
		{
			name:  "an empty warpfx credential carries nothing",
			creds: []verify.Verifiable{&warpfx.Credential{}},
			empty: true,
		},
		{
			// The carrier is found by scanning, exactly as the executor does.
			name: "the carrier behind other slots",
			creds: []verify.Verifiable{
				&secp256k1fx.Credential{},
				&warpfx.Credential{},
				&warpfx.Credential{WarpMessage: oneSigner},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require := require.New(t)

			actual, err := CredentialComplexity(test.creds)
			require.NoError(err)
			if test.empty {
				require.Equal(gas.Dimensions{}, actual)
				return
			}

			expected, err := WarpComplexity(oneSigner)
			require.NoError(err)
			require.Equal(expected, actual)
		})
	}

	// More signers, more compute: what the aggregated verification actually
	// costs.
	t.Run("the number of signers is priced", func(t *testing.T) {
		require := require.New(t)

		one, err := CredentialComplexity([]verify.Verifiable{&warpfx.Credential{WarpMessage: oneSigner}})
		require.NoError(err)
		four, err := CredentialComplexity([]verify.Verifiable{&warpfx.Credential{WarpMessage: fourSigners}})
		require.NoError(err)

		require.Greater(four[gas.Compute], one[gas.Compute])
		require.Equal(
			uint64(3),
			(four[gas.Compute]-one[gas.Compute])/intrinsicBLSAggregateCompute,
		)
	})
}

// TestSignedTxComplexity pins what the three block-capacity sites depend on:
// the signed complexity is the unsigned one plus exactly the credential's.
//
// Without it the fee would be right and the capacity wrong - a block could hold
// more authorized transactions than its gas target allows, and the message
// carries txBytes, so the gap is not marginal.
func TestSignedTxComplexity(t *testing.T) {
	require := require.New(t)

	unsigned := &platform.BaseTx{BaseTx: avax.BaseTx{
		Outs: []*avax.TransferableOutput{{
			Out: &warpfx.TransferOutput{Amt: 1, Owner: testWarpOwner()},
		}},
	}}
	message := newTestWarpMessage(t, 2)

	unsignedComplexity, err := TxComplexity(unsigned)
	require.NoError(err)

	credentialComplexity, err := WarpComplexity(message)
	require.NoError(err)

	signedComplexity, err := SignedTxComplexity(&platform.Tx{
		Unsigned: unsigned,
		Creds:    []verify.Verifiable{&warpfx.Credential{WarpMessage: message}},
	})
	require.NoError(err)

	expected, err := unsignedComplexity.Add(&credentialComplexity)
	require.NoError(err)
	require.Equal(expected, signedComplexity)
	require.NotEqual(unsignedComplexity, signedComplexity)
}

// TestCalculateFeeWithCredentials is test 9 of the plan: two authorizations
// differing only in their number of signers pay different fees.
func TestCalculateFeeWithCredentials(t *testing.T) {
	require := require.New(t)

	calculator := NewDynamicCalculator(
		gas.Dimensions{
			gas.Bandwidth: 1,
			gas.DBRead:    1,
			gas.DBWrite:   1,
			gas.Compute:   1,
		},
		1,
	)

	newTx := func(numSigners int) *platform.Tx {
		return &platform.Tx{
			Unsigned: &platform.BaseTx{},
			Creds: []verify.Verifiable{
				&warpfx.Credential{WarpMessage: newTestWarpMessage(t, numSigners)},
			},
		}
	}

	unsignedFee, err := calculator.CalculateFee(&platform.BaseTx{})
	require.NoError(err)

	oneSignerFee, err := calculator.CalculateFeeWithCredentials(newTx(1))
	require.NoError(err)
	fourSignerFee, err := calculator.CalculateFeeWithCredentials(newTx(4))
	require.NoError(err)

	require.Greater(oneSignerFee, unsignedFee)
	require.Greater(fourSignerFee, oneSignerFee)
}
