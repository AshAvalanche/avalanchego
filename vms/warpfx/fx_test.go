// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package warpfx

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ava-labs/avalanchego/codec"
	"github.com/ava-labs/avalanchego/codec/linearcodec"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/avalanchego/vms/platformvm/fx"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"
)

func TestInitializeWrongVM(t *testing.T) {
	f := &Fx{}
	require.ErrorIs(t, f.Initialize(struct{}{}), ErrWrongVMType)
}

// TestInitializeRegistersTypes checks that Initialize registers exactly what
// Types() claims. The PlatformVM's Fx table is built from the same list, so a
// divergence between the two would silently route a type to the default Fx.
func TestInitializeRegistersTypes(t *testing.T) {
	require := require.New(t)

	c := linearcodec.NewDefault()
	f := &Fx{}
	require.NoError(f.Initialize(&TestVM{Codec: c, Log: logging.NoLog{}}))

	// Registering the same types again must fail: they are already taken.
	for _, typ := range Types() {
		require.ErrorIs(c.RegisterType(typ), codec.ErrDuplicateType)
	}
}

// TestVerifyTransferAlwaysFails locks the enforcement mechanism.
//
// The context-free entry point is what every unrouted verification path calls.
// It must refuse even a triple that is otherwise perfectly valid, so that
// forgetting to route a transaction closes a door instead of opening one.
func TestVerifyTransferAlwaysFails(t *testing.T) {
	f := testFx()
	out, in, cred := testSpend(1000)

	require.ErrorIs(t, f.VerifyTransfer(nil, in, cred, out), ErrNoAuthorization)
}

func TestVerifyTransferWithContext(t *testing.T) {
	validOut, validIn, validCred := testSpend(1000)

	tests := []struct {
		name        string
		fxCtx       *fx.Context
		in          interface{}
		cred        interface{}
		utxo        interface{}
		expectedErr error
	}{
		{
			name:  "valid",
			fxCtx: &fx.Context{Authorization: testAuthorization(1)},
			in:    validIn,
			cred:  validCred,
			utxo:  validOut,
		},
		{
			name:        "wrong utxo type",
			fxCtx:       &fx.Context{Authorization: testAuthorization(1)},
			in:          validIn,
			cred:        validCred,
			utxo:        &secp256k1fx.TransferOutput{},
			expectedErr: ErrWrongUTXOType,
		},
		{
			name:        "wrong input type",
			fxCtx:       &fx.Context{Authorization: testAuthorization(1)},
			in:          &secp256k1fx.Input{},
			cred:        validCred,
			utxo:        validOut,
			expectedErr: ErrWrongInputType,
		},
		{
			name:  "unexpected signature indices",
			fxCtx: &fx.Context{Authorization: testAuthorization(1)},
			in: &secp256k1fx.TransferInput{
				Amt:   1000,
				Input: secp256k1fx.Input{SigIndices: []uint32{0}},
			},
			cred:        validCred,
			utxo:        validOut,
			expectedErr: ErrUnexpectedSigIndices,
		},
		{
			name:        "wrong credential type",
			fxCtx:       &fx.Context{Authorization: testAuthorization(1)},
			in:          validIn,
			cred:        &secp256k1fx.Credential{},
			utxo:        validOut,
			expectedErr: ErrWrongCredentialType,
		},
		{
			name:        "invalid utxo",
			fxCtx:       &fx.Context{Authorization: testAuthorization(1)},
			in:          &secp256k1fx.TransferInput{Amt: 0},
			cred:        validCred,
			utxo:        &TransferOutput{Amt: 0, Owner: testOwner(1)},
			expectedErr: ErrAmtZero,
		},
		{
			name:        "mismatched amounts",
			fxCtx:       &fx.Context{Authorization: testAuthorization(1)},
			in:          &secp256k1fx.TransferInput{Amt: 999},
			cred:        validCred,
			utxo:        validOut,
			expectedErr: ErrMismatchedAmounts,
		},
		{
			name:        "nil context",
			fxCtx:       nil,
			in:          validIn,
			cred:        validCred,
			utxo:        validOut,
			expectedErr: ErrNoAuthorization,
		},
		{
			name:        "context without authorization",
			fxCtx:       &fx.Context{},
			in:          validIn,
			cred:        validCred,
			utxo:        validOut,
			expectedErr: ErrNoAuthorization,
		},
		{
			name:        "authorization of another type",
			fxCtx:       &fx.Context{Authorization: &secp256k1fx.Credential{}},
			in:          validIn,
			cred:        validCred,
			utxo:        validOut,
			expectedErr: ErrNoAuthorization,
		},
		{
			name:        "authorization of another owner",
			fxCtx:       &fx.Context{Authorization: testAuthorization(2)},
			in:          validIn,
			cred:        validCred,
			utxo:        validOut,
			expectedErr: ErrWrongOwner,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := testFx()
			err := f.VerifyTransferWithContext(test.fxCtx, nil, test.in, test.cred, test.utxo)
			require.ErrorIs(t, err, test.expectedErr)
		})
	}
}

// TestAuthorizationDoesNotPersist is the test that matters in this package.
//
// It looks redundant - the Fx holds no authorization, so the property is true
// by construction. That is exactly the point: it locks the construction against
// a later "optimization" that would cache the parsed authorization next to the
// VM field. Such a cache would authorize a transaction the owner never
// approved, and no test written transaction by transaction would reveal it.
func TestAuthorizationDoesNotPersist(t *testing.T) {
	require := require.New(t)

	f := testFx()

	// A first, fully authorized verification succeeds.
	out, in, cred := testSpend(1000)
	fxCtx := &fx.Context{Authorization: testAuthorization(1)}
	require.NoError(f.VerifyTransferWithContext(fxCtx, nil, in, cred, out))

	// On the very same Fx instance, a second UTXO of the same owner reached
	// through the context-free entry point must not inherit anything.
	otherOut, otherIn, otherCred := testSpend(2000)
	require.ErrorIs(
		f.VerifyTransfer(nil, otherIn, otherCred, otherOut),
		ErrNoAuthorization,
	)

	// Nor through the contextual entry point with no authorization resolved.
	require.ErrorIs(
		f.VerifyTransferWithContext(nil, nil, otherIn, otherCred, otherOut),
		ErrNoAuthorization,
	)

	// Nor on the permission path, which shares the same Fx instance and would
	// otherwise be a second door onto a cached authorization.
	owner := testOwner(1)
	require.ErrorIs(
		f.VerifyPermissionWithContext(nil, nil, &secp256k1fx.Input{}, &Credential{}, &owner),
		ErrNoAuthorization,
	)
}

// TestVerifyPermissionAlwaysFails locks the subnet side of the design.
//
// Subnet authorization reaches the context-free entry point and only that one,
// so this refusal is what keeps a warp owner from becoming a subnet's control
// group - structurally, not by a rule someone has to remember to write.
func TestVerifyPermissionAlwaysFails(t *testing.T) {
	f := testFx()
	owner := testOwner(1)

	require.ErrorIs(
		t,
		f.VerifyPermission(nil, &secp256k1fx.Input{}, &Credential{}, &owner),
		ErrPermissionUnsupported,
	)
}

func TestVerifyPermissionWithContext(t *testing.T) {
	owner := testOwner(1)

	tests := []struct {
		name         string
		fxCtx        *fx.Context
		auth         interface{}
		cred         interface{}
		controlGroup interface{}
		expectedErr  error
	}{
		{
			name:         "valid",
			fxCtx:        &fx.Context{Authorization: testAuthorization(1)},
			auth:         &secp256k1fx.Input{},
			cred:         &Credential{},
			controlGroup: &owner,
		},
		{
			name:         "wrong control group type",
			fxCtx:        &fx.Context{Authorization: testAuthorization(1)},
			auth:         &secp256k1fx.Input{},
			cred:         &Credential{},
			controlGroup: &secp256k1fx.OutputOwners{},
			expectedErr:  ErrWrongOwnerType,
		},
		{
			name:         "wrong auth type",
			fxCtx:        &fx.Context{Authorization: testAuthorization(1)},
			auth:         &secp256k1fx.TransferInput{Amt: 1},
			cred:         &Credential{},
			controlGroup: &owner,
			expectedErr:  ErrWrongInputType,
		},
		{
			name:         "unexpected signature indices",
			fxCtx:        &fx.Context{Authorization: testAuthorization(1)},
			auth:         &secp256k1fx.Input{SigIndices: []uint32{0}},
			cred:         &Credential{},
			controlGroup: &owner,
			expectedErr:  ErrUnexpectedSigIndices,
		},
		{
			name:         "wrong credential type",
			fxCtx:        &fx.Context{Authorization: testAuthorization(1)},
			auth:         &secp256k1fx.Input{},
			cred:         &secp256k1fx.Credential{},
			controlGroup: &owner,
			expectedErr:  ErrWrongCredentialType,
		},
		{
			name:         "nil context",
			fxCtx:        nil,
			auth:         &secp256k1fx.Input{},
			cred:         &Credential{},
			controlGroup: &owner,
			expectedErr:  ErrNoAuthorization,
		},
		{
			name:         "context without authorization",
			fxCtx:        &fx.Context{},
			auth:         &secp256k1fx.Input{},
			cred:         &Credential{},
			controlGroup: &owner,
			expectedErr:  ErrNoAuthorization,
		},
		{
			name:         "authorization of another owner",
			fxCtx:        &fx.Context{Authorization: testAuthorization(2)},
			auth:         &secp256k1fx.Input{},
			cred:         &Credential{},
			controlGroup: &owner,
			expectedErr:  ErrWrongOwner,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := testFx()
			err := f.VerifyPermissionWithContext(test.fxCtx, nil, test.auth, test.cred, test.controlGroup)
			require.ErrorIs(t, err, test.expectedErr)
		})
	}
}

func TestCreateOutput(t *testing.T) {
	require := require.New(t)

	f := testFx()
	owner := testOwner(1)

	outIntf, err := f.CreateOutput(1000, &owner)
	require.NoError(err)

	out, ok := outIntf.(*TransferOutput)
	require.True(ok)
	require.Equal(uint64(1000), out.Amt)
	require.Equal(owner, out.Owner)
	require.NoError(out.Verify())
}

// TestCreateOutputWrongOwnerType locks the failure mode of the reward path.
//
// Crossing a secp owner with this Fx must fail loudly rather than silently
// produce an output of the wrong type - the mistake would only surface at spend
// time, months later, on real funds.
func TestCreateOutputWrongOwnerType(t *testing.T) {
	f := testFx()

	_, err := f.CreateOutput(1000, &secp256k1fx.OutputOwners{})
	require.ErrorIs(t, err, ErrWrongOwnerType)
}
