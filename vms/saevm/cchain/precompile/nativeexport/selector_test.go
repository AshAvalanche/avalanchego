// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package nativeexport

import (
	"testing"

	"github.com/ava-labs/libevm/crypto"
	"github.com/stretchr/testify/require"
)

// TestSelectorMatchesSignature guards the one thing a Solidity caller and this
// package must agree on. Getting it wrong reverts silently at the call site,
// with nothing in the precompile to point at.
func TestSelectorMatchesSignature(t *testing.T) {
	require.Equal(t, "exportAVAX()", ExportAVAXSignature)
	require.Equal(t, crypto.Keccak256([]byte(ExportAVAXSignature))[:4], exportAVAXSelector)
}
