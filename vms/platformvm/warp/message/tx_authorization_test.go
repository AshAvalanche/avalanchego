// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package message

import (
	"encoding/binary"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ava-labs/avalanchego/utils"
)

func TestTxAuthorization(t *testing.T) {
	require := require.New(t)

	txBytes := utils.RandomBytes(128)
	msg, err := NewTxAuthorization(
		rand.Uint64(), //#nosec G404
		txBytes,
	)
	require.NoError(err)

	parsed, err := ParseTxAuthorization(msg.Bytes())
	require.NoError(err)
	require.Equal(msg, parsed)
}

// TestTxAuthorizationBytes pins the wire format, field by field:
//
//	+---------------+----------+-------------------------+
//	|       codecID :   uint16 |                 2 bytes |
//	|        typeID :   uint32 |                 4 bytes |
//	|        expiry :   uint64 |                 8 bytes |
//	|       txBytes :   []byte |  4 + len(txBytes) bytes |
//	+---------------+----------+-------------------------+
//
// The Solidity side of this proposal builds the same bytes by hand, so the
// layout is an interface, not an implementation detail. The typeID in
// particular is the position in codec.go, and it moves if a payload is ever
// inserted above.
func TestTxAuthorizationBytes(t *testing.T) {
	require := require.New(t)

	txBytes := []byte{0xde, 0xad, 0xbe, 0xef}
	msg, err := NewTxAuthorization(0x0102030405060708, txBytes)
	require.NoError(err)

	require.Equal([]byte{
		// codecID
		0x00, 0x00,
		// typeID
		0x00, 0x00, 0x00, 0x04,
		// expiry
		0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
		// txBytes length
		0x00, 0x00, 0x00, 0x04,
		// txBytes
		0xde, 0xad, 0xbe, 0xef,
	}, msg.Bytes())

	// An empty payload still has to round-trip: nothing in this type forbids
	// it, because the commitment check downstream is what rejects it - a real
	// transaction's bytes are never empty.
	empty, err := NewTxAuthorization(0, nil)
	require.NoError(err)
	require.Len(empty.Bytes(), 18)
	require.Zero(binary.BigEndian.Uint32(empty.Bytes()[14:18]))
}

// TestTxAuthorizationTypeID keeps the payload from silently changing position
// in the registry, which would invalidate every authorization already emitted
// on chain.
func TestTxAuthorizationTypeID(t *testing.T) {
	msg, err := NewTxAuthorization(0, nil)
	require.NoError(t, err)
	require.Equal(t, uint32(4), binary.BigEndian.Uint32(msg.Bytes()[2:6]))
}
