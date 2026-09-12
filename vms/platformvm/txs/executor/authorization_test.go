// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package executor

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ava-labs/avalanchego/codec"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/vms/components/avax"
	"github.com/ava-labs/avalanchego/vms/components/verify"
	"github.com/ava-labs/avalanchego/vms/platformvm/fx"
	"github.com/ava-labs/avalanchego/vms/platformvm/platform"
	"github.com/ava-labs/avalanchego/vms/platformvm/warp"
	"github.com/ava-labs/avalanchego/vms/platformvm/warp/message"
	"github.com/ava-labs/avalanchego/vms/platformvm/warp/payload"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"
	"github.com/ava-labs/avalanchego/vms/warpfx"
)

// newAuthorizationTx builds a signed transaction whose unsigned bytes are
// initialized, so the commitment has something real to compare against.
func newAuthorizationTx(t *testing.T, unsigned platform.UnsignedTx) *platform.Tx {
	t.Helper()

	tx := &platform.Tx{Unsigned: unsigned}
	require.NoError(t, tx.Initialize(platform.Codec))
	return tx
}

// newAuthorization wraps a TxAuthorization committing to [txBytes] in the
// AddressedCall and Warp envelopes a real one travels in.
//
// The signature is left empty on purpose: resolveAuthorization checks the
// commitment and the expiry, never the quorum, which lives in the Warp verifier
// and does not run on this path.
func newAuthorization(
	t *testing.T,
	sourceChainID ids.ID,
	sourceAddress []byte,
	expiry uint64,
	txBytes []byte,
) []byte {
	t.Helper()

	authorization, err := message.NewTxAuthorization(expiry, txBytes)
	require.NoError(t, err)

	call, err := payload.NewAddressedCall(sourceAddress, authorization.Bytes())
	require.NoError(t, err)

	unsignedMsg, err := warp.NewUnsignedMessage(
		constants.UnitTestID,
		sourceChainID,
		call.Bytes(),
	)
	require.NoError(t, err)

	msg, err := warp.NewMessage(unsignedMsg, &warp.BitSetSignature{})
	require.NoError(t, err)

	return msg.Bytes()
}

func TestResolveAuthorization(t *testing.T) {
	var (
		sourceChainID = ids.GenerateTestID()
		sourceAddress = ids.GenerateTestShortID().Bytes()

		// Two distinct transactions. The commitment is what tells them apart.
		txA = newAuthorizationTx(t, &platform.BaseTx{BaseTx: avax.BaseTx{
			NetworkID: constants.UnitTestID,
			Memo:      []byte("a"),
		}})
		txB = newAuthorizationTx(t, &platform.BaseTx{BaseTx: avax.BaseTx{
			NetworkID: constants.UnitTestID,
			Memo:      []byte("b"),
		}})

		forA = newAuthorization(t, sourceChainID, sourceAddress, 100, txA.Unsigned.Bytes())
		forB = newAuthorization(t, sourceChainID, sourceAddress, 100, txB.Unsigned.Bytes())

		resolved = &fx.Context{Authorization: &warpfx.Authorization{
			SourceChainID: sourceChainID,
			SourceAddress: sourceAddress,
		}}
	)

	tests := []struct {
		name        string
		tx          *platform.Tx
		creds       []verify.Verifiable
		chainTime   uint64
		expected    *fx.Context
		expectedErr error
	}{
		{
			name:     "no credential at all",
			tx:       txA,
			expected: &fx.Context{},
		},
		{
			name:     "secp256k1 credentials only",
			tx:       txA,
			creds:    []verify.Verifiable{&secp256k1fx.Credential{}},
			expected: &fx.Context{},
		},
		{
			// Every warpfx slot but the carrier holds an empty message, so an
			// empty one must not be mistaken for an authorization.
			name:     "empty warpfx credential is not a carrier",
			tx:       txA,
			creds:    []verify.Verifiable{&warpfx.Credential{}},
			expected: &fx.Context{},
		},
		{
			// Creds is parallel to Ins ‖ ImportedInputs, so the carrier is at
			// whatever index its input sits at - found by scanning, never by
			// position.
			name: "carrier behind other credentials",
			tx:   txA,
			creds: []verify.Verifiable{
				&secp256k1fx.Credential{},
				&warpfx.Credential{},
				&warpfx.Credential{WarpMessage: forA},
			},
			expected: resolved,
		},
		{
			name: "two carriers",
			tx:   txA,
			creds: []verify.Verifiable{
				&warpfx.Credential{WarpMessage: forA},
				&warpfx.Credential{WarpMessage: forA},
			},
			expectedErr: ErrMultipleWarpAuthorizations,
		},
		{
			name:        "transaction not on the list",
			tx:          newAuthorizationTx(t, &platform.CreateSubnetTx{Owner: &secp256k1fx.OutputOwners{}}),
			creds:       []verify.Verifiable{&warpfx.Credential{WarpMessage: forA}},
			expectedErr: ErrWarpAuthorizationNotAccepted,
		},
		{
			name:        "unparsable warp message",
			tx:          txA,
			creds:       []verify.Verifiable{&warpfx.Credential{WarpMessage: []byte{0x01}}},
			expectedErr: codec.ErrCantUnpackVersion,
		},
		{
			// The heart of it. A Warp message is public - it sits in a C-chain
			// log every relayer watches - so an authorization that did not
			// commit to these exact bytes would be a bearer token over
			// everything its owner holds.
			name:        "authorization for another transaction",
			tx:          txA,
			creds:       []verify.Verifiable{&warpfx.Credential{WarpMessage: forB}},
			expectedErr: ErrAuthorizationMismatch,
		},
		{
			name:        "expired",
			tx:          txA,
			creds:       []verify.Verifiable{&warpfx.Credential{WarpMessage: forA}},
			chainTime:   101,
			expectedErr: ErrAuthorizationExpired,
		},
		{
			name:      "expiring exactly now is still valid",
			tx:        txA,
			creds:     []verify.Verifiable{&warpfx.Credential{WarpMessage: forA}},
			chainTime: 100,
			expected:  resolved,
		},
		{
			name:      "valid",
			tx:        txA,
			creds:     []verify.Verifiable{&warpfx.Credential{WarpMessage: forA}},
			chainTime: 99,
			expected:  resolved,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require := require.New(t)

			tx := &platform.Tx{Unsigned: test.tx.Unsigned, Creds: test.creds}
			fxCtx, err := resolveAuthorization(tx, test.chainTime)
			require.ErrorIs(err, test.expectedErr)
			require.Equal(test.expected, fxCtx)
		})
	}
}

// TestAcceptsWarpAuthorization enumerates every transaction type and pins the
// answer for each.
//
// Enumerating is the point: it is what makes it visible, in review, that a type
// added to the PlatformVM later inherits the refusal. A platform.TxVisitor would have
// been the idiomatic shape here and is exactly wrong - its default behaviour is
// nil, so a new type would silently inherit the authorization instead.
func TestAcceptsWarpAuthorization(t *testing.T) {
	tests := []struct {
		tx       platform.UnsignedTx
		accepted bool
	}{
		{tx: &platform.AddValidatorTx{}, accepted: false},
		{tx: &platform.AddSubnetValidatorTx{}, accepted: false},
		{tx: &platform.AddDelegatorTx{}, accepted: false},
		{tx: &platform.CreateChainTx{}, accepted: false},
		{tx: &platform.CreateSubnetTx{}, accepted: false},
		{tx: &platform.ImportTx{}, accepted: true},
		{tx: &platform.ExportTx{}, accepted: true},
		{tx: &platform.AdvanceTimeTx{}, accepted: false},
		{tx: &platform.RewardValidatorTx{}, accepted: false},
		{tx: &platform.RemoveSubnetValidatorTx{}, accepted: false},
		{tx: &platform.TransformSubnetTx{}, accepted: false},
		{tx: &platform.AddPermissionlessValidatorTx{}, accepted: true},
		{tx: &platform.AddPermissionlessDelegatorTx{}, accepted: true},
		{tx: &platform.TransferSubnetOwnershipTx{}, accepted: false},
		{tx: &platform.BaseTx{}, accepted: true},
		{tx: &platform.ConvertSubnetToL1Tx{}, accepted: false},
		{tx: &platform.RegisterL1ValidatorTx{}, accepted: false},
		{tx: &platform.SetL1ValidatorWeightTx{}, accepted: false},
		{tx: &platform.IncreaseL1ValidatorBalanceTx{}, accepted: false},
		{tx: &platform.DisableL1ValidatorTx{}, accepted: false},
		{tx: &platform.AddAutoRenewedValidatorTx{}, accepted: true},
		{tx: &platform.SetAutoRenewedValidatorConfigTx{}, accepted: true},
		{tx: &platform.RewardAutoRenewedValidatorTx{}, accepted: false},
	}

	seen := make(map[string]bool, len(tests))
	for _, test := range tests {
		name := fmt.Sprintf("%T", test.tx)
		t.Run(name, func(t *testing.T) {
			require.Equal(t, test.accepted, acceptsWarpAuthorization(test.tx))
		})
		seen[name] = true
	}

	// Every type the visitor knows about must appear above. Without this, a new
	// transaction type is simply missing from the table and nothing says so.
	visitorType := reflect.TypeOf((*platform.TxVisitor)(nil)).Elem()
	require.Len(t, seen, visitorType.NumMethod())
	for i := range visitorType.NumMethod() {
		method := visitorType.Method(i)
		argType := method.Type.In(0)
		require.True(t, seen[argType.String()], "%s is missing from the table", argType)
	}
}

// TestVerifyAuthorizationWithContext pins the split between the two permission
// entry points, which is what keeps a warp owner out of a subnet's control
// group.
//
// SetAutoRenewedValidatorConfigTx is the only transaction that must prove a
// control group's assent as well as spend, and one message authorizes both -
// it commits to the bytes of the whole transaction. Subnet authorization
// deliberately keeps calling the context-free verifyAuthorization, where a warp
// owner is refused with no rule written anywhere. Routing subnet authorization
// through the contextual function would reopen exactly that door.
func TestVerifyAuthorizationWithContext(t *testing.T) {
	require := require.New(t)

	secpFx := &secp256k1fx.Fx{}
	require.NoError(secpFx.InitializeVM(&secp256k1fx.TestVM{}))

	var (
		fxs = fx.NewFxs(
			fx.Claim{ID: secp256k1fx.ID, Fx: secpFx},
			fx.Claim{ID: warpfx.ID, Fx: &warpfx.Fx{}, Types: warpfx.Types()},
		)
		owner = &warpfx.Owner{
			SourceChainID: ids.GenerateTestID(),
			SourceAddress: ids.GenerateTestShortID().Bytes(),
		}
		auth = &secp256k1fx.Input{}
		tx   = &platform.Tx{
			Unsigned: &platform.BaseTx{},
			Creds: []verify.Verifiable{
				&warpfx.Credential{},
				&warpfx.Credential{},
			},
		}
	)

	// The owner's own authorization: assent proved, and the remaining
	// credentials handed back for the rest of the transaction.
	baseTxCreds, err := verifyAuthorizationWithContext(
		fxs,
		&fx.Context{Authorization: &warpfx.Authorization{
			SourceChainID: owner.SourceChainID,
			SourceAddress: owner.SourceAddress,
		}},
		tx,
		owner,
		auth,
	)
	require.NoError(err)
	require.Len(baseTxCreds, 1)

	// Somebody else's authorization proves nothing about this owner.
	_, err = verifyAuthorizationWithContext(
		fxs,
		&fx.Context{Authorization: &warpfx.Authorization{
			SourceChainID: owner.SourceChainID,
			SourceAddress: ids.GenerateTestShortID().Bytes(),
		}},
		tx,
		owner,
		auth,
	)
	require.ErrorIs(err, warpfx.ErrWrongOwner)

	// No authorization at all.
	_, err = verifyAuthorizationWithContext(fxs, &fx.Context{}, tx, owner, auth)
	require.ErrorIs(err, warpfx.ErrNoAuthorization)

	// And the door subnet authorization goes through: a single default Fx, no
	// context, and therefore no way for a warp owner to control a subnet. The
	// credentials are secp256k1 here so the refusal lands on the owner type
	// rather than stopping one assertion earlier.
	secpTx := &platform.Tx{
		Unsigned: &platform.BaseTx{},
		Creds:    []verify.Verifiable{&secp256k1fx.Credential{}},
	}
	_, err = verifyAuthorization(secpFx, secpTx, owner, auth)
	require.ErrorIs(err, secp256k1fx.ErrWrongOwnerType)
}
