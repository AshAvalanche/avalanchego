// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package executor

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/upgrade/upgradetest"
	"github.com/ava-labs/avalanchego/vms/components/avax"
	"github.com/ava-labs/avalanchego/vms/components/verify"
	"github.com/ava-labs/avalanchego/vms/platformvm/platform"
	"github.com/ava-labs/avalanchego/vms/platformvm/state"
	"github.com/ava-labs/avalanchego/vms/warpfx"

	txfee "github.com/ava-labs/avalanchego/vms/platformvm/txs/fee"
)

func TestVerifyWarpExportDestination(t *testing.T) {
	var (
		cChainID = ids.GenerateTestID()
		xChainID = ids.GenerateTestID()
	)

	tests := []struct {
		name        string
		destination ids.ID
		outs        []*avax.TransferableOutput
		expectedErr error
	}{
		{
			name:        "a secp256k1 output to the X-chain",
			destination: xChainID,
			outs:        []*avax.TransferableOutput{secpTestOutput()},
		},
		{
			name:        "a warpfx output to the C-chain",
			destination: cChainID,
			outs:        []*avax.TransferableOutput{warpTestOutput()},
		},
		{
			name:        "a warpfx output to the X-chain",
			destination: xChainID,
			outs:        []*avax.TransferableOutput{warpTestOutput()},
			expectedErr: ErrWarpOutputWrongDestination,
		},
		{
			name:        "a warpfx output among secp256k1 ones",
			destination: xChainID,
			outs: []*avax.TransferableOutput{
				secpTestOutput(),
				warpTestOutput(),
				secpTestOutput(),
			},
			expectedErr: ErrWarpOutputWrongDestination,
		},
		{
			// Unwrapping matters: on its wrapper's type alone this one would
			// slip past. It is refused here even though ExportTx.SyntacticVerify
			// separately rejects every stakeable lock at export.
			name:        "a warpfx output hidden inside a stakeable lock",
			destination: xChainID,
			outs:        []*avax.TransferableOutput{lockedTestOutput(warpTestOutput())},
			expectedErr: ErrWarpOutputWrongDestination,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := verifyWarpExportDestination(cChainID, &platform.ExportTx{
				DestinationChain: test.destination,
				ExportedOutputs:  test.outs,
			})
			require.ErrorIs(t, err, test.expectedErr)
		})
	}
}

// TestWarpExportDestinationRefusedBeforeAnyDebit runs the rule through the real
// executor, and checks it fires before anything is consumed.
//
// An export debits before its destination chain has any say, so a warpfx output
// heading somewhere that cannot decode it would be an irreversible loss.
func TestWarpExportDestinationRefusedBeforeAnyDebit(t *testing.T) {
	require := require.New(t)

	env := newEnvironment(t, upgradetest.Latest)

	// A P-chain UTXO to spend, so there is something that could be debited.
	owner := warpTestOwner()
	spent := &avax.UTXO{
		UTXOID: avax.UTXOID{TxID: ids.GenerateTestID()},
		Asset:  avax.Asset{ID: env.ctx.AVAXAssetID},
		Out:    &warpfx.TransferOutput{Amt: 1000, Owner: owner},
	}
	env.state.AddUTXO(spent)
	require.NoError(env.state.Commit())

	tx := &platform.Tx{
		Unsigned: &platform.ExportTx{
			BaseTx: platform.BaseTx{BaseTx: avax.BaseTx{
				NetworkID:    env.ctx.NetworkID,
				BlockchainID: env.ctx.ChainID,
			}},
			DestinationChain: env.ctx.XChainID,
			ExportedOutputs: []*avax.TransferableOutput{{
				Asset: avax.Asset{ID: env.ctx.AVAXAssetID},
				Out: &warpfx.TransferOutput{
					Amt:   1000,
					Owner: owner,
				},
			}},
		},
		Creds: []verify.Verifiable{},
	}
	require.NoError(tx.Initialize(platform.Codec))

	diff, err := state.NewDiff(lastAcceptedID, env, state.StakerAdditionAfterDeletionForbidden)
	require.NoError(err)

	_, _, _, err = StandardTx(&env.backend, txfee.NewSimpleCalculator(0), tx, diff)
	require.ErrorIs(err, ErrWarpOutputWrongDestination)

	// Nothing was consumed on the way to that refusal.
	_, err = diff.GetUTXO(spent.InputID())
	require.NoError(err)
}
