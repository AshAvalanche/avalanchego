// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package nativeexport

import (
	"testing"

	"github.com/ava-labs/libevm/common"
	"github.com/ava-labs/libevm/core/types"
	"github.com/holiman/uint256"
	"github.com/stretchr/testify/require"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/vms/components/avax"
	"github.com/ava-labs/avalanchego/vms/warpfx"
)

// TestValueMustBeWholeNAVAX pins the refusal rather than a truncation.
//
// Truncating would silently lose up to one nAVAX per call. Derisory, but a
// silent loss on a value-transfer path is exactly what should not have to be
// explained later.
func TestValueMustBeWholeNAVAX(t *testing.T) {
	tests := []struct {
		name    string
		value   *uint256.Int
		want    uint64
		wantErr error
	}{
		{"one nAVAX", uint256.NewInt(X2CRate), 1, nil},
		{"a whole number of them", uint256.NewInt(5 * X2CRate), 5, nil},
		{"one aAVAX over", uint256.NewInt(X2CRate + 1), 0, ErrValueNotWholeNAVAX},
		{"one aAVAX under", uint256.NewInt(X2CRate - 1), 0, ErrValueNotWholeNAVAX},
		{"a single aAVAX", uint256.NewInt(1), 0, ErrValueNotWholeNAVAX},
		{"nothing at all", uint256.NewInt(0), 0, ErrNoValue},
		{
			"more nAVAX than a uint64 holds",
			new(uint256.Int).Mul(uint256.NewInt(X2CRate), new(uint256.Int).Lsh(uint256.NewInt(1), 64)),
			0,
			ErrValueTooLarge,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := toNAVAX(tt.value)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

// TestOutputIndexIsPerTransaction is the reason the counter exists: several
// calls, possibly from several contracts, may share one EVM transaction and
// each must produce a distinct UTXOID - while two transactions must not share
// a counter.
func TestOutputIndexIsPerTransaction(t *testing.T) {
	db := newFakeStateDB()

	// Hashes that differ where the slot looks, as real ones do. HexToHash
	// left-pads, so "0xaa" and "0xbb" would share their first 28 bytes.
	txA := common.HexToHash("0xaa00000000000000000000000000000000000000000000000000000000000001")
	txB := common.HexToHash("0xbb00000000000000000000000000000000000000000000000000000000000002")

	db.txHash = txA
	require.Equal(t, uint32(0), nextOutputIndex(db))
	require.Equal(t, uint32(1), nextOutputIndex(db))
	require.Equal(t, uint32(2), nextOutputIndex(db))

	// A different transaction starts over, so its UTXOIDs are keyed by its own
	// hash and cannot collide with the previous one's.
	db.txHash = txB
	require.Equal(t, uint32(0), nextOutputIndex(db))
	require.Equal(t, uint32(1), nextOutputIndex(db))

	// Returning to the first transaction is not a real sequence, but it shows
	// the slot holds the hash and not just a count.
	db.txHash = txA
	require.Equal(t, uint32(0), nextOutputIndex(db))
}

// TestPrefixCollisionStaysUnique states the property the reset does not carry.
// Two transactions sharing the slot's 28-byte prefix keep counting, and that is
// harmless: a UTXOID is keyed by the whole hash.
func TestPrefixCollisionStaysUnique(t *testing.T) {
	db := newFakeStateDB()
	shared := "0x00000000000000000000000000000000000000000000000000000000000000"

	db.txHash = common.HexToHash(shared + "01")
	require.Equal(t, uint32(0), nextOutputIndex(db))

	db.txHash = common.HexToHash(shared + "02")
	// No reset, because the prefixes match - and it does not matter.
	require.Equal(t, uint32(1), nextOutputIndex(db))

	a := avax.UTXOID{TxID: ids.ID(common.HexToHash(shared + "01")), OutputIndex: 0}
	b := avax.UTXOID{TxID: ids.ID(common.HexToHash(shared + "02")), OutputIndex: 1}
	require.NotEqual(t, a.InputID(), b.InputID())
}

func TestOutputIndexCountsHigh(t *testing.T) {
	db := newFakeStateDB()
	db.txHash = common.HexToHash("0xcc")
	for i := range 300 {
		require.Equal(t, uint32(i), nextOutputIndex(db)) //#nosec G115
	}
}

// TestExportedUTXOsComeFromLogsAlone is the revert guarantee, seen from the
// derivation side: no log, no deposit.
func TestExportedUTXOsComeFromLogsAlone(t *testing.T) {
	avaxAssetID := ids.GenerateTestID()
	caller := common.HexToAddress("0x1234567890123456789012345678901234567890")
	sourceChain := ids.GenerateTestID()
	owner := warpfx.Owner{SourceChainID: sourceChain, SourceAddress: caller.Bytes()}

	data, err := packExport(owner, 7, 3)
	require.NoError(t, err)

	t.Run("no receipt at all", func(t *testing.T) {
		ops, err := FromReceipts(nil, avaxAssetID)
		require.NoError(t, err)
		require.Empty(t, ops)
	})

	t.Run("a receipt whose logs were rewound", func(t *testing.T) {
		// What a reverted frame leaves behind: the transaction is in the block,
		// it burned its gas, and it logged nothing.
		ops, err := FromReceipts(types.Receipts{{Logs: nil}}, avaxAssetID)
		require.NoError(t, err)
		require.Empty(t, ops, "a reverted export must deposit nothing")
	})

	t.Run("another contract's log", func(t *testing.T) {
		ops, err := FromReceipts(types.Receipts{{Logs: []*types.Log{{
			Address: common.HexToAddress("0xdead"),
			Topics:  []common.Hash{EventTopic},
			Data:    data,
		}}}}, avaxAssetID)
		require.NoError(t, err)
		require.Empty(t, ops)
	})

	t.Run("our address but another topic", func(t *testing.T) {
		ops, err := FromReceipts(types.Receipts{{Logs: []*types.Log{{
			Address: ContractAddress,
			Topics:  []common.Hash{common.HexToHash("0xfeed")},
			Data:    data,
		}}}}, avaxAssetID)
		require.NoError(t, err)
		require.Empty(t, ops)
	})

	t.Run("a real export", func(t *testing.T) {
		txHash := common.HexToHash("0xabc")
		ops, err := FromReceipts(types.Receipts{{Logs: []*types.Log{{
			Address: ContractAddress,
			Topics:  []common.Hash{EventTopic, common.BytesToHash(caller.Bytes())},
			Data:    data,
			TxHash:  txHash,
		}}}}, avaxAssetID)
		require.NoError(t, err)
		require.Len(t, ops, 1)

		req, ok := ops[constants.PlatformChainID]
		require.True(t, ok, "the destination is forced to the P-Chain")
		require.Len(t, req.PutRequests, 1)
		require.Empty(t, req.RemoveRequests, "an export consumes nothing")

		// The trait is what makes the UTXO discoverable by its owner.
		require.Equal(t, [][]byte{caller.Bytes()}, req.PutRequests[0].Traits)
	})

	t.Run("a truncated log", func(t *testing.T) {
		_, err := FromReceipts(types.Receipts{{Logs: []*types.Log{{
			Address: ContractAddress,
			Topics:  []common.Hash{EventTopic},
			Data:    data[:10],
		}}}}, avaxAssetID)
		require.ErrorIs(t, err, ErrMalformedLog)
	})
}

// TestExportRoundTripsThroughTheLog checks the log carries everything needed to
// rebuild the UTXO, and nothing ambiguous.
func TestExportRoundTripsThroughTheLog(t *testing.T) {
	owner := warpfx.Owner{
		SourceChainID: ids.GenerateTestID(),
		SourceAddress: common.HexToAddress("0xabcdef").Bytes(),
	}
	const (
		amount = uint64(123456789)
		index  = uint32(42)
	)

	data, err := packExport(owner, amount, index)
	require.NoError(t, err)
	require.Len(t, data, logDataLen)

	gotOwner, gotAmount, gotIndex, err := unpackExport(data)
	require.NoError(t, err)
	require.True(t, gotOwner.Equals(&owner))
	require.Equal(t, amount, gotAmount)
	require.Equal(t, index, gotIndex)
}

func TestDestinationIsForced(t *testing.T) {
	require.Equal(t, constants.PlatformChainID, DestinationChain)
}
