// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package warpfx

import (
	"bytes"

	"github.com/ava-labs/avalanchego/codec/linearcodec"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"
)

var (
	sourceChainID = ids.ID{'c', '-', 'c', 'h', 'a', 'i', 'n'}
	otherChainID  = ids.ID{'x', '-', 'c', 'h', 'a', 'i', 'n'}
)

func testAddress(b byte) []byte {
	return bytes.Repeat([]byte{b}, AddressLen)
}

func testOwner(b byte) Owner {
	return Owner{
		SourceChainID: sourceChainID,
		SourceAddress: testAddress(b),
	}
}

// testAuthorization returns the authorization that [testOwner] would issue.
func testAuthorization(b byte) *Authorization {
	return &Authorization{
		SourceChainID: sourceChainID,
		SourceAddress: testAddress(b),
	}
}

func testFx() *Fx {
	f := &Fx{}
	vm := &TestVM{
		Codec: linearcodec.NewDefault(),
		Log:   logging.NoLog{},
	}
	if err := f.Initialize(vm); err != nil {
		panic(err)
	}
	return f
}

// testSpend returns a well-formed (utxo, input, credential) triple spending
// [amt]. Cases that need a mismatch vary the authorization, not the owner.
func testSpend(amt uint64) (*TransferOutput, *secp256k1fx.TransferInput, *Credential) {
	out := &TransferOutput{
		Amt:   amt,
		Owner: testOwner(1),
	}
	in := &secp256k1fx.TransferInput{
		Amt: amt,
	}
	return out, in, &Credential{}
}
