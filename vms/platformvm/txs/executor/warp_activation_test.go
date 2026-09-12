// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package executor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/upgrade/upgradetest"
	"github.com/ava-labs/avalanchego/utils/crypto/secp256k1"
	"github.com/ava-labs/avalanchego/vms/components/avax"
	"github.com/ava-labs/avalanchego/vms/components/verify"
	"github.com/ava-labs/avalanchego/vms/platformvm/platform"
	"github.com/ava-labs/avalanchego/vms/platformvm/stakeable"
	"github.com/ava-labs/avalanchego/vms/platformvm/state"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"
	"github.com/ava-labs/avalanchego/vms/warpfx"

	txfee "github.com/ava-labs/avalanchego/vms/platformvm/txs/fee"
)

func warpTestOwner() warpfx.Owner {
	return warpfx.Owner{
		SourceChainID: ids.GenerateTestID(),
		SourceAddress: ids.GenerateTestShortID().Bytes(),
	}
}

func warpTestOutput() *avax.TransferableOutput {
	return &avax.TransferableOutput{
		Out: &warpfx.TransferOutput{Amt: 1, Owner: warpTestOwner()},
	}
}

func secpTestOutput() *avax.TransferableOutput {
	return &avax.TransferableOutput{
		Out: &secp256k1fx.TransferOutput{Amt: 1},
	}
}

func lockedTestOutput(inner *avax.TransferableOutput) *avax.TransferableOutput {
	return &avax.TransferableOutput{
		Out: &stakeable.LockOut{
			Locktime:        1,
			TransferableOut: inner.Out,
		},
	}
}

// TestVerifyWarpUTXOsActivated walks the places a warpfx type can be mentioned,
// and checks each one against the same transaction on both sides of the
// upgrade.
//
// The rule is about *mentioning*, not about spending: receiving creates an
// undecodable type just as surely, and a node on an older binary cannot parse
// the block at all. What this guard prevents is a fork, not early use.
func TestVerifyWarpUTXOsActivated(t *testing.T) {
	var (
		upgrades      = upgradetest.GetConfig(upgradetest.Helicon)
		beforeUpgrade = upgrades.HeliconTime.Add(-time.Second)
		afterUpgrade  = upgrades.HeliconTime
	)

	tests := []struct {
		name      string
		tx        *platform.Tx
		mentioned bool
	}{
		{
			name: "an ordinary secp256k1 transaction",
			tx: &platform.Tx{Unsigned: &platform.BaseTx{BaseTx: avax.BaseTx{
				Outs: []*avax.TransferableOutput{secpTestOutput()},
			}}},
		},
		{
			name: "an output",
			tx: &platform.Tx{Unsigned: &platform.BaseTx{BaseTx: avax.BaseTx{
				Outs: []*avax.TransferableOutput{secpTestOutput(), warpTestOutput()},
			}}},
			mentioned: true,
		},
		{
			name: "an exported output",
			tx: &platform.Tx{Unsigned: &platform.ExportTx{
				ExportedOutputs: []*avax.TransferableOutput{warpTestOutput()},
			}},
			mentioned: true,
		},
		{
			name: "a stake output",
			tx: &platform.Tx{Unsigned: &platform.AddPermissionlessValidatorTx{
				StakeOuts: []*avax.TransferableOutput{warpTestOutput()},
			}},
			mentioned: true,
		},
		{
			name: "a rewards owner",
			tx: &platform.Tx{Unsigned: &platform.AddPermissionlessValidatorTx{
				ValidatorRewardsOwner: &warpfx.Owner{},
			}},
			mentioned: true,
		},
		{
			// Creds is why the guard takes the signed transaction.
			name: "a credential",
			tx: &platform.Tx{
				Unsigned: &platform.BaseTx{},
				Creds: []verify.Verifiable{
					&secp256k1fx.Credential{},
					&warpfx.Credential{},
				},
			},
			mentioned: true,
		},
		{
			name: "an output hidden inside a stakeable lock",
			tx: &platform.Tx{Unsigned: &platform.BaseTx{BaseTx: avax.BaseTx{
				Outs: []*avax.TransferableOutput{lockedTestOutput(warpTestOutput())},
			}}},
			mentioned: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require := require.New(t)

			err := VerifyWarpUTXOsActivated(upgrades, beforeUpgrade, test.tx)
			if test.mentioned {
				require.ErrorIs(err, ErrWarpUTXOsNotActivated)
			} else {
				require.NoError(err)
			}

			// Past the upgrade the guard returns on its first line, whatever
			// the transaction holds.
			require.NoError(VerifyWarpUTXOsActivated(upgrades, afterUpgrade, test.tx))
		})
	}
}

// TestVerifyWarpOutputsNotLocked covers the locktime that comes back through
// the side door.
//
// warpfx.TransferOutput has no Locktime of its own because nobody can enforce
// one end to end - the UTXO verifier reads the node's local clock, everything
// else in warpfx reads the chain time. But stakeable.LockOut wraps an
// interface, and a third party can build the combination: producing locked
// funds out of unlocked ones is allowed, and receiving needs no authorization.
func TestVerifyWarpOutputsNotLocked(t *testing.T) {
	tests := []struct {
		name   string
		tx     *platform.Tx
		locked bool
	}{
		{
			name: "a bare warpfx output",
			tx: &platform.Tx{Unsigned: &platform.BaseTx{BaseTx: avax.BaseTx{
				Outs: []*avax.TransferableOutput{warpTestOutput()},
			}}},
		},
		{
			// Non-regression: locking secp256k1 funds is what LockOut is for.
			name: "a locked secp256k1 output",
			tx: &platform.Tx{Unsigned: &platform.BaseTx{BaseTx: avax.BaseTx{
				Outs: []*avax.TransferableOutput{lockedTestOutput(secpTestOutput())},
			}}},
		},
		{
			name: "a locked warpfx output",
			tx: &platform.Tx{Unsigned: &platform.BaseTx{BaseTx: avax.BaseTx{
				Outs: []*avax.TransferableOutput{secpTestOutput(), lockedTestOutput(warpTestOutput())},
			}}},
			locked: true,
		},
		{
			name: "a locked warpfx exported output",
			tx: &platform.Tx{Unsigned: &platform.ExportTx{
				ExportedOutputs: []*avax.TransferableOutput{lockedTestOutput(warpTestOutput())},
			}},
			locked: true,
		},
		{
			name: "a locked warpfx stake output",
			tx: &platform.Tx{Unsigned: &platform.AddPermissionlessValidatorTx{
				StakeOuts: []*avax.TransferableOutput{lockedTestOutput(warpTestOutput())},
			}},
			locked: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := verifyWarpOutputsNotLocked(test.tx)
			if test.locked {
				require.ErrorIs(t, err, ErrWarpOutputNotLockable)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestWarpGuardsRunFromStandardTx checks the two guards are actually reached,
// and reached before anything is debited.
func TestWarpGuardsRunFromStandardTx(t *testing.T) {
	t.Run("before the upgrade", func(t *testing.T) {
		require := require.New(t)

		env := newEnvironment(t, upgradetest.Granite)
		diff, err := state.NewDiff(lastAcceptedID, env, state.StakerAdditionAfterDeletionForbidden)
		require.NoError(err)

		tx := &platform.Tx{Unsigned: &platform.BaseTx{BaseTx: avax.BaseTx{
			Outs: []*avax.TransferableOutput{warpTestOutput()},
		}}}
		_, _, _, err = StandardTx(&env.backend, txfee.NewSimpleCalculator(0), tx, diff)
		require.ErrorIs(err, ErrWarpUTXOsNotActivated)
	})

	t.Run("after the upgrade", func(t *testing.T) {
		require := require.New(t)

		env := newEnvironment(t, upgradetest.Latest)
		diff, err := state.NewDiff(lastAcceptedID, env, state.StakerAdditionAfterDeletionForbidden)
		require.NoError(err)

		tx := &platform.Tx{Unsigned: &platform.BaseTx{BaseTx: avax.BaseTx{
			Outs: []*avax.TransferableOutput{lockedTestOutput(warpTestOutput())},
		}}}
		_, _, _, err = StandardTx(&env.backend, txfee.NewSimpleCalculator(0), tx, diff)
		require.ErrorIs(err, ErrWarpOutputNotLockable)
	})
}

// BenchmarkVerifyWarpUTXOsActivated measures the reflective traversal, which
// is the one cost this guard adds.
//
// It only runs while the upgrade is inactive - afterwards the function returns
// on its first line - but during that window it runs on every transaction, so
// the number is worth having.
func BenchmarkVerifyWarpUTXOsActivated(b *testing.B) {
	const numIO = 16

	unsigned := &platform.BaseTx{BaseTx: avax.BaseTx{
		Ins:  make([]*avax.TransferableInput, numIO),
		Outs: make([]*avax.TransferableOutput, numIO),
	}}
	tx := &platform.Tx{
		Unsigned: unsigned,
		Creds:    make([]verify.Verifiable, numIO),
	}
	for i := range numIO {
		unsigned.Ins[i] = &avax.TransferableInput{
			In: &secp256k1fx.TransferInput{
				Amt:   1,
				Input: secp256k1fx.Input{SigIndices: []uint32{0}},
			},
		}
		unsigned.Outs[i] = &avax.TransferableOutput{
			Out: &secp256k1fx.TransferOutput{
				Amt: 1,
				OutputOwners: secp256k1fx.OutputOwners{
					Threshold: 1,
					Addrs:     []ids.ShortID{ids.GenerateTestShortID()},
				},
			},
		}
		tx.Creds[i] = &secp256k1fx.Credential{
			Sigs: make([][secp256k1.SignatureLen]byte, 1),
		}
	}

	upgrades := upgradetest.GetConfig(upgradetest.Helicon)

	b.Run("before the upgrade", func(b *testing.B) {
		chainTime := upgrades.HeliconTime.Add(-time.Second)
		for b.Loop() {
			_ = VerifyWarpUTXOsActivated(upgrades, chainTime, tx)
		}
	})

	b.Run("after the upgrade", func(b *testing.B) {
		for b.Loop() {
			_ = VerifyWarpUTXOsActivated(upgrades, upgrades.HeliconTime, tx)
		}
	})

	b.Run("outputs not locked", func(b *testing.B) {
		for b.Loop() {
			_ = verifyWarpOutputsNotLocked(tx)
		}
	})
}
