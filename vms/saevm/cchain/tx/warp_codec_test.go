// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package tx_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/vms/components/avax"
	"github.com/ava-labs/avalanchego/vms/saevm/cchain/tx"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"
	"github.com/ava-labs/avalanchego/vms/warpfx"

	platformtxs "github.com/ava-labs/avalanchego/vms/platformvm/platform"
)

// TestCodecAlignedWithPlatformVM checks that a UTXO serializes identically on
// both sides of shared memory.
//
// This fills a real hole. Nothing under vms/saevm imported vms/platformvm/txs
// before, and the only mention of "typeID" in the whole tree was the comment in
// codec.go: the alignment rested on that comment and on binary-compatibility
// tests against *coreth*. Nothing would have broken automatically if a future
// RegisterType shifted TransferInput, TransferOutput or Credential relative to
// the PlatformVM.
//
// A misalignment does not fail loudly. An export debits, writes a UTXO its
// destination cannot decode, and the funds are gone.
func TestCodecAlignedWithPlatformVM(t *testing.T) {
	tests := []struct {
		name string
		out  avax.TransferableOut
	}{
		{
			// The invariant the comment has claimed all along.
			name: "secp256k1fx output",
			out: &secp256k1fx.TransferOutput{
				Amt: 1000,
				OutputOwners: secp256k1fx.OutputOwners{
					Threshold: 1,
					Addrs:     []ids.ShortID{ids.GenerateTestShortID()},
				},
			},
		},
		{
			name: "warpfx output",
			out: &warpfx.TransferOutput{
				Amt: 1000,
				Owner: warpfx.Owner{
					SourceChainID: ids.GenerateTestID(),
					SourceAddress: ids.GenerateTestShortID().Bytes(),
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require := require.New(t)

			utxo := &avax.UTXO{
				UTXOID: avax.UTXOID{
					TxID:        ids.GenerateTestID(),
					OutputIndex: 7,
				},
				Asset: avax.Asset{ID: ids.GenerateTestID()},
				Out:   test.out,
			}

			saevmBytes, err := tx.MarshalUTXO(utxo)
			require.NoError(err)

			platformBytes, err := platformtxs.Codec.Marshal(platformtxs.CodecVersion, utxo)
			require.NoError(err)

			require.Equal(platformBytes, saevmBytes)

			// And back, so the check covers decoding as well as encoding.
			parsed, err := tx.ParseUTXO(platformBytes)
			require.NoError(err)
			require.Equal(utxo, parsed)
		})
	}
}
