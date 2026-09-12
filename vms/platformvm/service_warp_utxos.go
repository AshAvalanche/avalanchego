// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package platformvm

import (
	"fmt"
	"net/http"

	"go.uber.org/zap"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/formatting"
	"github.com/ava-labs/avalanchego/utils/set"
	"github.com/ava-labs/avalanchego/vms/components/avax"
	"github.com/ava-labs/avalanchego/vms/platformvm/platform"
	"github.com/ava-labs/avalanchego/vms/warpfx"

	avajson "github.com/ava-labs/avalanchego/utils/json"
)

// WarpOwnerIndex is the pagination cursor of GetWarpOwnerUTXOs.
//
// It names the owner by its source address rather than by a Bech32 string on
// purpose: rendering an EVM address as P-avax1... would suggest a P-chain
// address whose key somebody holds.
type WarpOwnerIndex struct {
	SourceAddress string `json:"sourceAddress"`
	UTXO          string `json:"utxo"`
}

// GetWarpOwnerUTXOsArgs are the arguments to GetWarpOwnerUTXOs.
type GetWarpOwnerUTXOsArgs struct {
	// SourceChainID and SourceAddress identify the warp owner, in the form its
	// operator already knows: a chain ID and an 0x-prefixed 20-byte address.
	SourceChainID string `json:"sourceChainID"`
	SourceAddress string `json:"sourceAddress"`

	// AtomicSourceChain selects where to look. Empty means this chain's own
	// UTXO index; otherwise it is the chain the UTXOs were exported from, and
	// shared memory is searched instead.
	//
	// There is deliberately no "both at once" mode. An operator building a
	// transaction usually wants both, and two calls give that; a merged answer
	// would have to say which source each UTXO came from, or it misleads -
	// funds sitting in shared memory are not yet spendable on this chain.
	AtomicSourceChain string `json:"atomicSourceChain"`

	Limit      avajson.Uint32      `json:"limit"`
	StartIndex WarpOwnerIndex      `json:"startIndex"`
	Encoding   formatting.Encoding `json:"encoding"`
}

// GetWarpOwnerUTXOsReply is the response from GetWarpOwnerUTXOs.
type GetWarpOwnerUTXOsReply struct {
	NumFetched avajson.Uint64      `json:"numFetched"`
	UTXOs      []string            `json:"utxos"`
	EndIndex   WarpOwnerIndex      `json:"endIndex"`
	Encoding   formatting.Encoding `json:"encoding"`
}

// GetWarpOwnerUTXOs returns the UTXOs held by one warp owner.
//
// Off-chain discovery is not tooling comfort here, it is a functional
// dependency of the nominal path: the authorization carries the bytes of the
// transaction, so the owner has to build the whole transaction - which UTXOs to
// consume, at which exact amounts - before it can authorize anything.
//
// It filters as well as fetches, and that is why it exists rather than leaving
// the job to platform.getUTXOs. The shared memory trait and the UTXO index key
// are the source address alone, twenty bytes, which is what lets every existing
// accessor serve a warp owner unchanged - but those twenty bytes do not name an
// owner. A secp256k1 UTXO whose address happens to be the same bytes, or a
// warpfx UTXO of the same address on another source chain, both match the
// index. Only comparing the full pair answers the question the caller asked.
func (s *Service) GetWarpOwnerUTXOs(
	_ *http.Request,
	args *GetWarpOwnerUTXOsArgs,
	response *GetWarpOwnerUTXOsReply,
) error {
	s.vm.ctx.Log.Debug("API called",
		zap.String("service", "platform"),
		zap.String("method", "getWarpOwnerUTXOs"),
	)

	sourceChainID, err := ids.FromString(args.SourceChainID)
	if err != nil {
		return fmt.Errorf("problem parsing sourceChainID %q: %w", args.SourceChainID, err)
	}

	sourceAddress, err := parseWarpSourceAddress(args.SourceAddress)
	if err != nil {
		return err
	}

	atomicSourceChain := s.vm.ctx.ChainID
	if args.AtomicSourceChain != "" {
		atomicSourceChain, err = s.vm.ctx.BCLookup.Lookup(args.AtomicSourceChain)
		if err != nil {
			return fmt.Errorf("problem parsing atomicSourceChain %q: %w", args.AtomicSourceChain, err)
		}
	}

	startUTXO := ids.Empty
	if args.StartIndex.UTXO != "" {
		startUTXO, err = ids.FromString(args.StartIndex.UTXO)
		if err != nil {
			return fmt.Errorf("couldn't parse start index utxo: %w", err)
		}
	}
	startAddr := ids.ShortEmpty
	if args.StartIndex.SourceAddress != "" {
		startAddr, err = parseWarpSourceAddress(args.StartIndex.SourceAddress)
		if err != nil {
			return fmt.Errorf("couldn't parse start index address: %w", err)
		}
	}

	limit := int(args.Limit)
	if limit <= 0 || maxPageSize < limit {
		limit = maxPageSize
	}

	s.vm.ctx.Lock.Lock()
	defer s.vm.ctx.Lock.Unlock()

	var (
		utxos     []*avax.UTXO
		endUTXOID ids.ID
		addrs     = set.Of(sourceAddress)
	)
	if atomicSourceChain == s.vm.ctx.ChainID {
		utxos, _, endUTXOID, err = avax.GetPaginatedUTXOs(
			s.vm.state,
			addrs,
			startAddr,
			startUTXO,
			limit,
		)
	} else {
		utxos, _, endUTXOID, err = avax.GetAtomicUTXOs(
			s.vm.ctx.SharedMemory,
			platform.Codec,
			atomicSourceChain,
			addrs,
			startAddr,
			startUTXO,
			limit,
		)
	}
	if err != nil {
		return fmt.Errorf("problem retrieving UTXOs: %w", err)
	}

	// The cursor is the last UTXO *read*, not the last one kept: a page can
	// come back short because entries belonging to somebody else were dropped,
	// and the caller still has to resume from where the scan stopped.
	owner := warpfx.Owner{
		SourceChainID: sourceChainID,
		SourceAddress: sourceAddress[:],
	}
	response.UTXOs = make([]string, 0, len(utxos))
	for _, utxo := range utxos {
		out, ok := utxo.Out.(*warpfx.TransferOutput)
		if !ok || !out.Owner.Equals(&owner) {
			continue
		}

		bytes, err := platform.Codec.Marshal(platform.CodecVersion, utxo)
		if err != nil {
			return fmt.Errorf("couldn't serialize UTXO %q: %w", utxo.InputID(), err)
		}
		encoded, err := formatting.Encode(args.Encoding, bytes)
		if err != nil {
			return fmt.Errorf("couldn't encode UTXO %s as %s: %w", utxo.InputID(), args.Encoding, err)
		}
		response.UTXOs = append(response.UTXOs, encoded)
	}

	response.EndIndex.SourceAddress = args.SourceAddress
	response.EndIndex.UTXO = endUTXOID.String()
	response.NumFetched = avajson.Uint64(len(response.UTXOs))
	response.Encoding = args.Encoding
	return nil
}

// parseWarpSourceAddress decodes an 0x-prefixed, 20-byte source address.
func parseWarpSourceAddress(addr string) (ids.ShortID, error) {
	bytes, err := formatting.Decode(formatting.HexNC, addr)
	if err != nil {
		return ids.ShortEmpty, fmt.Errorf("problem parsing sourceAddress %q: %w", addr, err)
	}
	if len(bytes) != warpfx.AddressLen {
		return ids.ShortEmpty, fmt.Errorf(
			"%w: sourceAddress is %d bytes, expected %d",
			warpfx.ErrWrongAddressLength,
			len(bytes),
			warpfx.AddressLen,
		)
	}
	return ids.ShortID(bytes), nil
}
