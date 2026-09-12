// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package platformvm

import (
	"encoding/hex"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ava-labs/avalanchego/api"
	"github.com/ava-labs/avalanchego/chains/atomic"
	"github.com/ava-labs/avalanchego/database/memdb"
	"github.com/ava-labs/avalanchego/database/prefixdb"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/formatting"
	"github.com/ava-labs/avalanchego/vms/components/avax"
	"github.com/ava-labs/avalanchego/vms/platformvm/platform"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"
	"github.com/ava-labs/avalanchego/vms/warpfx"
)

func hexAddress(addr ids.ShortID) string {
	return "0x" + hex.EncodeToString(addr[:])
}

// TestGetWarpOwnerUTXOs covers both sources, and the filtering that is the
// reason this method exists.
//
// The shared memory trait and the UTXO index key are the source address alone -
// twenty bytes, which is what lets every existing accessor serve a warp owner
// unchanged. But twenty bytes do not name an owner: a secp256k1 UTXO at the
// same address, or a warpfx UTXO of that address on another source chain, match
// the index just as well.
func TestGetWarpOwnerUTXOs(t *testing.T) {
	r := require.New(t)

	service, mutableSharedMemory := defaultService(t)

	var (
		sourceChainID = ids.GenerateTestID()
		otherChainID  = ids.GenerateTestID()
		address       = ids.GenerateTestShortID()
		otherAddress  = ids.GenerateTestShortID()

		owner = warpfx.Owner{
			SourceChainID: sourceChainID,
			SourceAddress: address[:],
		}
	)

	// Four UTXOs indexed under the same twenty bytes, of which exactly one
	// belongs to the owner being asked about.
	utxos := []*avax.UTXO{
		{
			UTXOID: avax.UTXOID{TxID: ids.GenerateTestID()},
			Asset:  avax.Asset{ID: service.vm.ctx.AVAXAssetID},
			Out:    &warpfx.TransferOutput{Amt: 1000, Owner: owner},
		},
		{
			// Same address, another source chain.
			UTXOID: avax.UTXOID{TxID: ids.GenerateTestID()},
			Asset:  avax.Asset{ID: service.vm.ctx.AVAXAssetID},
			Out: &warpfx.TransferOutput{Amt: 2000, Owner: warpfx.Owner{
				SourceChainID: otherChainID,
				SourceAddress: address[:],
			}},
		},
		{
			// Same twenty bytes, but a secp256k1 owner.
			UTXOID: avax.UTXOID{TxID: ids.GenerateTestID()},
			Asset:  avax.Asset{ID: service.vm.ctx.AVAXAssetID},
			Out: &secp256k1fx.TransferOutput{
				Amt: 3000,
				OutputOwners: secp256k1fx.OutputOwners{
					Threshold: 1,
					Addrs:     []ids.ShortID{address},
				},
			},
		},
		{
			// Another owner entirely, which the index will not even return.
			UTXOID: avax.UTXOID{TxID: ids.GenerateTestID()},
			Asset:  avax.Asset{ID: service.vm.ctx.AVAXAssetID},
			Out: &warpfx.TransferOutput{Amt: 4000, Owner: warpfx.Owner{
				SourceChainID: sourceChainID,
				SourceAddress: otherAddress[:],
			}},
		},
	}

	// The P-chain index.
	service.vm.ctx.Lock.Lock()
	for _, utxo := range utxos {
		service.vm.state.AddUTXO(utxo)
	}
	r.NoError(service.vm.state.Commit())
	service.vm.ctx.Lock.Unlock()

	// And shared memory, as an atomic export would have left them.
	peerChainID := service.vm.ctx.XChainID
	m := atomic.NewMemory(prefixdb.New([]byte{'w'}, memdb.New()))
	peerSharedMemory := m.NewSharedMemory(peerChainID)
	elems := make([]*atomic.Element, len(utxos))
	for i, utxo := range utxos {
		utxoBytes, err := platform.Codec.Marshal(platform.CodecVersion, utxo)
		r.NoError(err)

		inputID := utxo.InputID()
		addressable, ok := utxo.Out.(avax.Addressable)
		r.True(ok)

		elems[i] = &atomic.Element{
			Key:    inputID[:],
			Value:  utxoBytes,
			Traits: addressable.Addresses(),
		}
	}
	r.NoError(peerSharedMemory.Apply(map[ids.ID]*atomic.Requests{
		service.vm.ctx.ChainID: {PutRequests: elems},
	}))
	mutableSharedMemory.SharedMemory = m.NewSharedMemory(service.vm.ctx.ChainID)

	encode := func(utxo *avax.UTXO) string {
		bytes, err := platform.Codec.Marshal(platform.CodecVersion, utxo)
		r.NoError(err)
		encoded, err := formatting.Encode(formatting.Hex, bytes)
		r.NoError(err)
		return encoded
	}

	tests := []struct {
		name              string
		sourceChainID     string
		sourceAddress     string
		atomicSourceChain string
		expectedUTXOs     []string
		expectedErr       error
	}{
		{
			name:          "the P-chain index",
			sourceChainID: sourceChainID.String(),
			sourceAddress: hexAddress(address),
			expectedUTXOs: []string{encode(utxos[0])},
		},
		{
			name:              "shared memory",
			sourceChainID:     sourceChainID.String(),
			sourceAddress:     hexAddress(address),
			atomicSourceChain: peerChainID.String(),
			expectedUTXOs:     []string{encode(utxos[0])},
		},
		{
			// The same twenty bytes on another source chain is a different
			// owner, and gets its own UTXO rather than the first one - the
			// filter discriminates in both directions.
			name:          "another source chain",
			sourceChainID: otherChainID.String(),
			sourceAddress: hexAddress(address),
			expectedUTXOs: []string{encode(utxos[1])},
		},
		{
			name:          "another owner",
			sourceChainID: sourceChainID.String(),
			sourceAddress: hexAddress(ids.GenerateTestShortID()),
			expectedUTXOs: []string{},
		},
		{
			name:          "a source address of the wrong length",
			sourceChainID: sourceChainID.String(),
			sourceAddress: "0x0102",
			expectedErr:   warpfx.ErrWrongAddressLength,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require := require.New(t)

			reply := GetWarpOwnerUTXOsReply{}
			err := service.GetWarpOwnerUTXOs(&http.Request{}, &GetWarpOwnerUTXOsArgs{
				SourceChainID:     test.sourceChainID,
				SourceAddress:     test.sourceAddress,
				AtomicSourceChain: test.atomicSourceChain,
				Encoding:          formatting.Hex,
			}, &reply)
			if test.expectedErr != nil {
				require.ErrorIs(err, test.expectedErr)
				return
			}
			require.NoError(err)

			require.Equal(test.expectedUTXOs, reply.UTXOs)
			require.Len(test.expectedUTXOs, int(reply.NumFetched))
			require.Equal(test.sourceAddress, reply.EndIndex.SourceAddress)
		})
	}
}

// TestGetUTXOsServesWarpOwners answers the question that decides whether the
// dedicated method above is a necessity or a convenience.
//
// It is a convenience. The trait is twenty bytes, and any twenty bytes encode
// as Bech32, so platform.getUTXOs serves a warp owner unchanged - which was not
// true while a 32-byte trait was on the table.
//
// What it cannot do is filter. It answers on the index alone, so it returns
// every UTXO sharing those twenty bytes: the secp256k1 one, and the warpfx one
// of another source chain. Presenting an EVM address as P-avax1... is the
// ergonomic argument for the dedicated method; this is the substantive one.
func TestGetUTXOsServesWarpOwners(t *testing.T) {
	require := require.New(t)

	service, _ := defaultService(t)

	var (
		sourceChainID = ids.GenerateTestID()
		otherChainID  = ids.GenerateTestID()
		address       = ids.GenerateTestShortID()
	)

	utxos := []*avax.UTXO{
		{
			UTXOID: avax.UTXOID{TxID: ids.GenerateTestID()},
			Asset:  avax.Asset{ID: service.vm.ctx.AVAXAssetID},
			Out: &warpfx.TransferOutput{Amt: 1000, Owner: warpfx.Owner{
				SourceChainID: sourceChainID,
				SourceAddress: address[:],
			}},
		},
		{
			UTXOID: avax.UTXOID{TxID: ids.GenerateTestID()},
			Asset:  avax.Asset{ID: service.vm.ctx.AVAXAssetID},
			Out: &warpfx.TransferOutput{Amt: 2000, Owner: warpfx.Owner{
				SourceChainID: otherChainID,
				SourceAddress: address[:],
			}},
		},
	}

	service.vm.ctx.Lock.Lock()
	for _, utxo := range utxos {
		service.vm.state.AddUTXO(utxo)
	}
	require.NoError(service.vm.state.Commit())
	service.vm.ctx.Lock.Unlock()

	bech32, err := service.addrManager.FormatLocalAddress(address)
	require.NoError(err)

	reply := api.GetUTXOsReply{}
	require.NoError(service.GetUTXOs(&http.Request{}, &api.GetUTXOsArgs{
		Addresses: []string{bech32},
		Encoding:  formatting.Hex,
	}, &reply))

	// Both come back, because the index cannot tell them apart.
	require.Len(reply.UTXOs, 2)
}
