// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package fx_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ava-labs/avalanchego/vms/platformvm/fx"
	"github.com/ava-labs/avalanchego/vms/platformvm/stakeable"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"
	"github.com/ava-labs/avalanchego/vms/warpfx"
)

// TestFxsGet pins the resolution over the collection the VM actually builds.
//
// The fallback cases matter as much as the hits: they are what makes this
// collection a pure refactor rather than a behavior change. Every type nobody
// claims has to keep landing on the extension that used to answer for all of
// them.
func TestFxsGet(t *testing.T) {
	var (
		secpFx = &secp256k1fx.Fx{}
		warpFx = &warpfx.Fx{}
		fxs    = fx.NewFxs(
			fx.Claim{ID: secp256k1fx.ID, Fx: secpFx},
			fx.Claim{ID: warpfx.ID, Fx: warpFx, Types: warpfx.Types()},
		)
	)

	tests := []struct {
		name     string
		value    any
		expected fx.Fx
	}{
		{
			name:     "warpfx transfer output",
			value:    &warpfx.TransferOutput{},
			expected: warpFx,
		},
		{
			name:     "warpfx owner",
			value:    &warpfx.Owner{},
			expected: warpFx,
		},
		{
			name:     "warpfx credential",
			value:    &warpfx.Credential{},
			expected: warpFx,
		},
		{
			name:     "secp256k1fx transfer output",
			value:    &secp256k1fx.TransferOutput{},
			expected: secpFx,
		},
		{
			// Nobody claims OutputOwners, and it still has to resolve.
			name:     "unclaimed output owners",
			value:    &secp256k1fx.OutputOwners{},
			expected: secpFx,
		},
		{
			// The verifier unwraps this one before resolving, so it should
			// never reach Get - and if it ever does, it must not panic.
			name:     "unclaimed stakeable lock",
			value:    &stakeable.LockOut{},
			expected: secpFx,
		},
		{
			name:     "nil",
			value:    nil,
			expected: secpFx,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Same(t, test.expected, fxs.Get(test.value))
		})
	}

	require.Same(t, secpFx, fxs.Default())
}

// TestFxsNoCollection covers the caller holding no collection at all - a
// Backend built by a test, typically.
//
// It must get nil rather than a default, so that it fails at the call site
// instead of quietly producing an output of the wrong type.
func TestFxsNoCollection(t *testing.T) {
	var fxs *fx.Fxs

	require.Nil(t, fxs.Get(&warpfx.TransferOutput{}))
	require.Nil(t, fxs.Default())
	require.Empty(t, fxs.All())
	require.ErrorIs(t, fxs.VerifyTransfer(nil, nil, nil, nil, nil), fx.ErrNoFx)
}

// TestFxsVerifyTransferEntryPoint checks which of the two entry points the
// collection uses, and that the context reaches the contextual one untouched.
func TestFxsVerifyTransferEntryPoint(t *testing.T) {
	var (
		plain      = &recordingFx{}
		contextual = &contextualRecordingFx{}
		fxs        = fx.NewFxs(
			fx.Claim{Fx: plain},
			fx.Claim{Fx: contextual, Types: []any{&claimedOutput{}}},
		)
		fxCtx = &fx.Context{Authorization: "an authorization"}
	)

	require.NoError(t, fxs.VerifyTransfer(fxCtx, nil, nil, nil, &claimedOutput{}))
	require.Equal(t, 1, contextual.withContext)
	require.Same(t, fxCtx, contextual.lastFxCtx)
	require.Zero(t, contextual.contextFree)

	// An extension that cannot accept a context is reached through the
	// historic method, context or not: the collection changes nothing for it.
	require.NoError(t, fxs.VerifyTransfer(fxCtx, nil, nil, nil, &unclaimedOutput{}))
	require.Equal(t, 1, plain.contextFree)

	// And a contextual extension reached with no context still gets called -
	// refusing is its own decision to make, not the collection's.
	require.NoError(t, fxs.VerifyTransfer(nil, nil, nil, nil, &claimedOutput{}))
	require.Equal(t, 2, contextual.withContext)
	require.Nil(t, contextual.lastFxCtx)
}

type (
	claimedOutput   struct{}
	unclaimedOutput struct{}
)

// recordingFx is a Fx that is not contextual.
type recordingFx struct {
	contextFree int
}

func (*recordingFx) Initialize(interface{}) error {
	return nil
}

func (*recordingFx) Bootstrapping() error {
	return nil
}

func (*recordingFx) Bootstrapped() error {
	return nil
}

func (f *recordingFx) VerifyTransfer(_, _, _, _ interface{}) error {
	f.contextFree++
	return nil
}

func (*recordingFx) VerifyPermission(_, _, _, _ interface{}) error {
	return nil
}

func (*recordingFx) CreateOutput(uint64, interface{}) (interface{}, error) {
	return nil, nil
}

// contextualRecordingFx is the same, plus the two contextual entry points.
type contextualRecordingFx struct {
	recordingFx

	withContext int
	lastFxCtx   *fx.Context
}

func (f *contextualRecordingFx) VerifyTransferWithContext(fxCtx *fx.Context, _, _, _, _ interface{}) error {
	f.withContext++
	f.lastFxCtx = fxCtx
	return nil
}

func (*contextualRecordingFx) VerifyPermissionWithContext(_ *fx.Context, _, _, _, _ interface{}) error {
	return nil
}
