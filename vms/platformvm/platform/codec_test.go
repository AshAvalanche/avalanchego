// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package platform

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ava-labs/avalanchego/codec"
	"github.com/ava-labs/avalanchego/vms/warpfx"
)

// TestWarpUTXOsCodecTypeIDs pins the positions at which the warpfx types are
// registered.
//
// Nothing in the language protects them. A RegisterType added anywhere above
// shifts every position that follows, with neither a compilation error nor
// another failing test. And a shifted position is not a local matter: saevm
// reserves these exact slots with a SkipRegistrations, so an UTXO written on
// one side at 44 and read on the other at 45 is created, debited, and then
// unreadable for good.
//
// When this goes red, updating the numbers here is therefore only half of the
// fix: saevm's reservation moves with them.
func TestWarpUTXOsCodecTypeIDs(t *testing.T) {
	tests := []struct {
		name   string
		value  any
		typeID uint32
	}{
		{
			name:   "owner",
			value:  &warpfx.Owner{},
			typeID: 43,
		},
		{
			name:   "transfer output",
			value:  &warpfx.TransferOutput{},
			typeID: 44,
		},
		{
			name:   "credential",
			value:  &warpfx.Credential{},
			typeID: 45,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.typeID, typeIDOf(t, Codec, test.value))
			require.Equal(t, test.typeID, typeIDOf(t, GenesisCodec, test.value))
		})
	}
}

// typeIDOf returns the position at which [value] is registered in [manager].
//
// codec.Manager exposes no type-to-position table, so the position is read back
// the only way it is observable from the outside: by marshalling the value into
// an interface field, which is precisely where the codec writes it.
func typeIDOf(t *testing.T, manager codec.Manager, value any) uint32 {
	t.Helper()

	bytes, err := manager.Marshal(CodecVersion, &struct {
		Value any `serialize:"true"`
	}{
		Value: value,
	})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(bytes), 6)

	// Two bytes of codec version, then the four-byte type prefix of the
	// interface field.
	return binary.BigEndian.Uint32(bytes[2:6])
}
