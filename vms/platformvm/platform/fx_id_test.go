// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package platform

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/snow/snowtest"
	"github.com/ava-labs/avalanchego/vms/components/avax"
	"github.com/ava-labs/avalanchego/vms/platformvm/stakeable"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"
	"github.com/ava-labs/avalanchego/vms/warpfx"
)

// TestInitCtxSetsOutputFxID pins the fxID a warpfx output is displayed under.
//
// No consensus rides on this - the field is serialize:"false" and only feeds
// JSON - but the wrong answer sends the first person investigating a warpfx
// UTXO reading "secp256k1fx" in every API and explorer, and looking in the
// wrong place.
func TestInitCtxSetsOutputFxID(t *testing.T) {
	warpOut := func() *avax.TransferableOutput {
		return &avax.TransferableOutput{
			Out: &warpfx.TransferOutput{
				Amt: 1,
				Owner: warpfx.Owner{
					SourceChainID: ids.GenerateTestID(),
					SourceAddress: ids.GenerateTestShortID().Bytes(),
				},
			},
		}
	}
	secpOut := func() *avax.TransferableOutput {
		return &avax.TransferableOutput{Out: &secp256k1fx.TransferOutput{Amt: 1}}
	}
	lockedWarpOut := func() *avax.TransferableOutput {
		return &avax.TransferableOutput{
			Out: &stakeable.LockOut{
				Locktime:        1,
				TransferableOut: warpOut().Out,
			},
		}
	}

	ctx := snowtest.Context(t, snowtest.PChainID)

	t.Run("base tx outputs", func(t *testing.T) {
		tx := &BaseTx{BaseTx: avax.BaseTx{
			Outs: []*avax.TransferableOutput{secpOut(), warpOut(), lockedWarpOut()},
			Ins:  []*avax.TransferableInput{{In: &secp256k1fx.TransferInput{Amt: 1}}},
		}}
		tx.InitCtx(ctx)

		require.Equal(t, secp256k1fx.ID, tx.Outs[0].FxID)
		require.Equal(t, warpfx.ID, tx.Outs[1].FxID)
		require.Equal(t, warpfx.ID, tx.Outs[2].FxID)

		// warpfx declares no input type, so an input spending a warpfx UTXO
		// really is a secp256k1fx one. The asymmetry is deliberate.
		require.Equal(t, secp256k1fx.ID, tx.Ins[0].FxID)
	})

	t.Run("exported outputs", func(t *testing.T) {
		tx := &ExportTx{ExportedOutputs: []*avax.TransferableOutput{secpOut(), warpOut()}}
		tx.InitCtx(ctx)

		require.Equal(t, secp256k1fx.ID, tx.ExportedOutputs[0].FxID)
		require.Equal(t, warpfx.ID, tx.ExportedOutputs[1].FxID)
	})

	t.Run("stake outputs", func(t *testing.T) {
		tx := &AddPermissionlessValidatorTx{
			StakeOuts:             []*avax.TransferableOutput{secpOut(), warpOut()},
			ValidatorRewardsOwner: &secp256k1fx.OutputOwners{},
			DelegatorRewardsOwner: &secp256k1fx.OutputOwners{},
		}
		tx.InitCtx(ctx)

		require.Equal(t, secp256k1fx.ID, tx.StakeOuts[0].FxID)
		require.Equal(t, warpfx.ID, tx.StakeOuts[1].FxID)
	})
}

// TestSubnetOwnerMustBeSecp256k1 pins the refusal that keeps subnet
// authorization secp256k1-only by construction.
//
// A warp owner would leave the subnet permanently unmanageable: subnet
// authorization goes through the context-free permission entry point, where
// warpfx answers ErrPermissionUnsupported. Pricing warpfx owners (they are
// legitimate rewards owners, and legitimate validator authorities) made such a
// transaction calculable, hence acceptable, so the rule has to be written
// rather than left to the fee calculator to refuse by accident.
func TestSubnetOwnerMustBeSecp256k1(t *testing.T) {
	ctx := snowtest.Context(t, snowtest.PChainID)

	warpOwner := &warpfx.Owner{
		SourceChainID: ids.GenerateTestID(),
		SourceAddress: ids.GenerateTestShortID().Bytes(),
	}
	secpOwner := &secp256k1fx.OutputOwners{
		Threshold: 1,
		Addrs:     []ids.ShortID{ids.GenerateTestShortID()},
	}
	baseTx := avax.BaseTx{
		NetworkID:    ctx.NetworkID,
		BlockchainID: ctx.ChainID,
	}

	t.Run("create subnet refuses a warp owner", func(t *testing.T) {
		tx := &CreateSubnetTx{BaseTx: BaseTx{BaseTx: baseTx}, Owner: warpOwner}
		require.ErrorIs(t, tx.SyntacticVerify(ctx), ErrWarpOwnerCannotOwnSubnet)
	})

	t.Run("create subnet accepts a secp256k1 owner", func(t *testing.T) {
		tx := &CreateSubnetTx{BaseTx: BaseTx{BaseTx: baseTx}, Owner: secpOwner}
		require.NoError(t, tx.SyntacticVerify(ctx))
	})

	// The symmetric case ends the other way: a warp owner is legitimate as a
	// validator authority, because SetAutoRenewedValidatorConfigTx reaches the
	// Fx *with* a context. What differs is the entry point, not the owner type.
	t.Run("a validator authority may be a warp owner", func(t *testing.T) {
		require.NoError(t, warpOwner.Verify())
	})
}
