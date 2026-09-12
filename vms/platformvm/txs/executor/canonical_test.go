// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package executor

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ava-labs/avalanchego/chains/atomic"
	"github.com/ava-labs/avalanchego/database/prefixdb"
	"github.com/ava-labs/avalanchego/genesis"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/upgrade/upgradetest"
	"github.com/ava-labs/avalanchego/utils/crypto/secp256k1"
	"github.com/ava-labs/avalanchego/utils/hashing"
	"github.com/ava-labs/avalanchego/utils/units"
	"github.com/ava-labs/avalanchego/vms/components/avax"
	"github.com/ava-labs/avalanchego/vms/components/verify"
	"github.com/ava-labs/avalanchego/vms/platformvm/fx"
	"github.com/ava-labs/avalanchego/vms/platformvm/platform"
	"github.com/ava-labs/avalanchego/vms/platformvm/state"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"
	"github.com/ava-labs/avalanchego/vms/warpfx"

	txfee "github.com/ava-labs/avalanchego/vms/platformvm/txs/fee"
)

var (
	canonicalAssetID = ids.GenerateTestID()
	canonicalOwner   = warpfx.Owner{
		SourceChainID: ids.GenerateTestID(),
		SourceAddress: ids.GenerateTestShortID().Bytes(),
	}
)

// canonicalImport builds a valid canonical import consuming [amounts] and
// returning [returned] to the same owner, together with its credentials and
// the UTXOs shared memory would have handed the executor.
func canonicalImport(returned uint64, amounts ...uint64) (
	*platform.ImportTx,
	[]verify.Verifiable,
	[]*avax.UTXO,
) {
	var (
		tx = &platform.ImportTx{
			BaseTx: platform.BaseTx{BaseTx: avax.BaseTx{
				Outs: []*avax.TransferableOutput{{
					Asset: avax.Asset{ID: canonicalAssetID},
					Out: &warpfx.TransferOutput{
						Amt:   returned,
						Owner: canonicalOwner,
					},
				}},
			}},
			SourceChain: ids.GenerateTestID(),
		}
		creds = make([]verify.Verifiable, len(amounts))
		utxos = make([]*avax.UTXO, len(amounts))
	)
	for i, amount := range amounts {
		utxoID := avax.UTXOID{
			TxID:        ids.GenerateTestID(),
			OutputIndex: uint32(i),
		}
		tx.ImportedInputs = append(tx.ImportedInputs, &avax.TransferableInput{
			UTXOID: utxoID,
			Asset:  avax.Asset{ID: canonicalAssetID},
			In:     &secp256k1fx.TransferInput{Amt: amount},
		})
		creds[i] = &warpfx.Credential{}
		utxos[i] = &avax.UTXO{
			UTXOID: utxoID,
			Asset:  avax.Asset{ID: canonicalAssetID},
			Out: &warpfx.TransferOutput{
				Amt:   amount,
				Owner: canonicalOwner,
			},
		}
	}
	return tx, creds, utxos
}

// TestIsCanonicalImport covers the conjunction, one broken condition at a time.
//
// Every false here sends the transaction down the ordinary path, where the
// warpfx UTXO meets the Fx with a nil context and is refused. Failing in that
// direction is what makes the short-circuit safe.
func TestIsCanonicalImport(t *testing.T) {
	secpUTXO := &avax.UTXO{Out: &secp256k1fx.TransferOutput{Amt: 1}}

	tests := []struct {
		name     string
		fxCtx    *fx.Context
		mutate   func(*platform.ImportTx, []*avax.UTXO) []*avax.UTXO
		expected bool
	}{
		{
			name:     "canonical",
			fxCtx:    &fx.Context{},
			expected: true,
		},
		{
			// Several UTXOs of one owner consolidate for free: ImportedInputs
			// has no cap, so N successive exports gather into one import.
			name:  "several utxos",
			fxCtx: &fx.Context{},
			mutate: func(tx *platform.ImportTx, u []*avax.UTXO) []*avax.UTXO {
				tx.ImportedInputs = append(tx.ImportedInputs, tx.ImportedInputs[0])
				return append(u, u[0])
			},
			expected: true,
		},
		{
			name:     "an authorization was resolved",
			fxCtx:    &fx.Context{Authorization: &warpfx.Authorization{}},
			expected: false,
		},
		{
			// A caller that resolved nothing at all has no business taking
			// this branch either.
			name:     "no context at all",
			fxCtx:    nil,
			expected: false,
		},
		{
			name:  "consumes a P-chain input as well",
			fxCtx: &fx.Context{},
			mutate: func(tx *platform.ImportTx, u []*avax.UTXO) []*avax.UTXO {
				tx.Ins = []*avax.TransferableInput{{In: &secp256k1fx.TransferInput{Amt: 1}}}
				return append([]*avax.UTXO{secpUTXO}, u...)
			},
			expected: false,
		},
		{
			name:  "mixes a secp256k1 utxo in",
			fxCtx: &fx.Context{},
			mutate: func(_ *platform.ImportTx, u []*avax.UTXO) []*avax.UTXO {
				return append(u, secpUTXO)
			},
			expected: false,
		},
		{
			name:  "no utxo at all",
			fxCtx: &fx.Context{},
			mutate: func(*platform.ImportTx, []*avax.UTXO) []*avax.UTXO {
				return nil
			},
			expected: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tx, _, utxos := canonicalImport(900, 1000)
			if test.mutate != nil {
				utxos = test.mutate(tx, utxos)
			}

			require.Equal(t, test.expected, isCanonicalImport(test.fxCtx, tx, utxos))
		})
	}
}

// TestVerifyCanonicalImport walks the constraints that stand in for a
// signature, one mutation at a time from a valid transaction.
func TestVerifyCanonicalImport(t *testing.T) {
	const fee = 100

	otherOwner := warpfx.Owner{
		SourceChainID: ids.GenerateTestID(),
		SourceAddress: ids.GenerateTestShortID().Bytes(),
	}

	tests := []struct {
		name        string
		returned    uint64
		amounts     []uint64
		mutate      func(*platform.ImportTx, []verify.Verifiable, []*avax.UTXO)
		expectedErr error
	}{
		{
			name:     "valid",
			returned: 900,
			amounts:  []uint64{1000},
		},
		{
			name:     "valid with several utxos",
			returned: 2900,
			amounts:  []uint64{1000, 2000},
		},
		{
			name:     "a credential carries a message",
			returned: 900,
			amounts:  []uint64{1000},
			mutate: func(_ *platform.ImportTx, creds []verify.Verifiable, _ []*avax.UTXO) {
				creds[0] = &warpfx.Credential{WarpMessage: []byte{0x01}}
			},
			expectedErr: ErrCanonicalImportCredential,
		},
		{
			name:     "a credential is a secp256k1 one",
			returned: 900,
			amounts:  []uint64{1000},
			mutate: func(_ *platform.ImportTx, creds []verify.Verifiable, _ []*avax.UTXO) {
				creds[0] = &secp256k1fx.Credential{}
			},
			expectedErr: ErrCanonicalImportCredential,
		},
		{
			name:     "wrong number of credentials",
			returned: 900,
			amounts:  []uint64{1000},
			mutate: func(tx *platform.ImportTx, _ []verify.Verifiable, _ []*avax.UTXO) {
				tx.ImportedInputs = append(tx.ImportedInputs, tx.ImportedInputs[0])
			},
			expectedErr: ErrCanonicalImportCredential,
		},
		{
			name:     "utxos of different owners",
			returned: 2900,
			amounts:  []uint64{1000, 2000},
			mutate: func(_ *platform.ImportTx, _ []verify.Verifiable, utxos []*avax.UTXO) {
				utxos[1].Out.(*warpfx.TransferOutput).Owner = otherOwner
			},
			expectedErr: ErrCanonicalImportMixedOwners,
		},
		{
			name:     "a utxo of another asset",
			returned: 900,
			amounts:  []uint64{1000},
			mutate: func(_ *platform.ImportTx, _ []verify.Verifiable, utxos []*avax.UTXO) {
				utxos[0].Asset = avax.Asset{ID: ids.GenerateTestID()}
			},
			expectedErr: ErrCanonicalImportAsset,
		},
		{
			name:     "an output of another asset",
			returned: 900,
			amounts:  []uint64{1000},
			mutate: func(tx *platform.ImportTx, _ []verify.Verifiable, _ []*avax.UTXO) {
				tx.Outs[0].Asset = avax.Asset{ID: ids.GenerateTestID()}
			},
			expectedErr: ErrCanonicalImportAsset,
		},
		{
			name:     "an input presenting signature indices",
			returned: 900,
			amounts:  []uint64{1000},
			mutate: func(tx *platform.ImportTx, _ []verify.Verifiable, _ []*avax.UTXO) {
				tx.ImportedInputs[0].In.(*secp256k1fx.TransferInput).SigIndices = []uint32{0}
			},
			expectedErr: ErrCanonicalImportInput,
		},
		{
			name:     "an input claiming less than its utxo",
			returned: 900,
			amounts:  []uint64{1000},
			mutate: func(tx *platform.ImportTx, _ []verify.Verifiable, _ []*avax.UTXO) {
				tx.ImportedInputs[0].In.(*secp256k1fx.TransferInput).Amt = 999
			},
			expectedErr: ErrCanonicalImportAmount,
		},
		{
			name:     "no output",
			returned: 900,
			amounts:  []uint64{1000},
			mutate: func(tx *platform.ImportTx, _ []verify.Verifiable, _ []*avax.UTXO) {
				tx.Outs = nil
			},
			expectedErr: ErrCanonicalImportOutputCount,
		},
		{
			name:     "two outputs",
			returned: 900,
			amounts:  []uint64{1000},
			mutate: func(tx *platform.ImportTx, _ []verify.Verifiable, _ []*avax.UTXO) {
				tx.Outs = append(tx.Outs, tx.Outs[0])
			},
			expectedErr: ErrCanonicalImportOutputCount,
		},
		{
			// The whole point: the carrier cannot divert the funds.
			name:     "the output goes to somebody else",
			returned: 900,
			amounts:  []uint64{1000},
			mutate: func(tx *platform.ImportTx, _ []verify.Verifiable, _ []*avax.UTXO) {
				tx.Outs[0].Out.(*warpfx.TransferOutput).Owner = otherOwner
			},
			expectedErr: ErrCanonicalImportWrongOutput,
		},
		{
			name:     "the output is a secp256k1 one",
			returned: 900,
			amounts:  []uint64{1000},
			mutate: func(tx *platform.ImportTx, _ []verify.Verifiable, _ []*avax.UTXO) {
				tx.Outs[0].Out = &secp256k1fx.TransferOutput{Amt: 900}
			},
			expectedErr: ErrCanonicalImportWrongOutput,
		},
		{
			name:     "a malformed imported utxo",
			returned: 900,
			amounts:  []uint64{1000},
			mutate: func(_ *platform.ImportTx, _ []verify.Verifiable, utxos []*avax.UTXO) {
				utxos[0].Out.(*warpfx.TransferOutput).SourceAddress = []byte{0x01}
			},
			expectedErr: warpfx.ErrWrongAddressLength,
		},

		// The fee band, one nAVAX either side of each bound.
		{
			name:        "one nAVAX short of the fee",
			returned:    901,
			amounts:     []uint64{1000},
			expectedErr: warpfx.ErrInsufficientFee,
		},
		{
			name:     "exactly the fee",
			returned: 900,
			amounts:  []uint64{1000},
		},
		{
			name:     "exactly k times the fee",
			returned: 800,
			amounts:  []uint64{1000},
		},
		{
			name:        "one nAVAX past k times the fee",
			returned:    799,
			amounts:     []uint64{1000},
			expectedErr: warpfx.ErrExcessiveBurn,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tx, creds, utxos := canonicalImport(test.returned, test.amounts...)
			if test.mutate != nil {
				test.mutate(tx, creds, utxos)
			}

			err := verifyCanonicalImport(tx, creds, utxos, canonicalAssetID, fee)
			require.ErrorIs(t, err, test.expectedErr)
		})
	}
}

// warpFundedSharedMemory puts one warpfx UTXO per amount into shared memory,
// as an atomic export from [peerChain] would have.
func warpFundedSharedMemory(
	t *testing.T,
	env *environment,
	peerChain ids.ID,
	owner warpfx.Owner,
	amounts ...uint64,
) (atomic.SharedMemory, []*avax.UTXO) {
	t.Helper()

	fundedSharedMemoryCalls++
	m := atomic.NewMemory(prefixdb.New([]byte{fundedSharedMemoryCalls}, env.baseDB))

	var (
		sm               = m.NewSharedMemory(env.ctx.ChainID)
		peerSharedMemory = m.NewSharedMemory(peerChain)
		utxos            = make([]*avax.UTXO, len(amounts))
		elems            = make([]*atomic.Element, len(amounts))
	)
	for i, amount := range amounts {
		utxo := &avax.UTXO{
			UTXOID: avax.UTXOID{
				TxID:        ids.GenerateTestID(),
				OutputIndex: uint32(i),
			},
			Asset: avax.Asset{ID: env.ctx.AVAXAssetID},
			Out: &warpfx.TransferOutput{
				Amt:   amount,
				Owner: owner,
			},
		}
		utxoBytes, err := platform.Codec.Marshal(platform.CodecVersion, utxo)
		require.NoError(t, err)

		inputID := utxo.InputID()
		utxos[i] = utxo
		elems[i] = &atomic.Element{
			Key:   inputID[:],
			Value: utxoBytes,
			// The discovery trait is the source address as-is; see
			// warpfx.TransferOutput.Addresses.
			Traits: [][]byte{owner.SourceAddress},
		}
	}

	require.NoError(t, peerSharedMemory.Apply(map[ids.ID]*atomic.Requests{
		env.ctx.ChainID: {PutRequests: elems},
	}))
	return sm, utxos
}

// TestCanonicalImportExecution runs the branch through the real executor.
//
// The fee is injected rather than computed: pricing a warpfx output is a later
// lot, and vms/platformvm/txs/fee currently answers errUnsupportedOutput for
// one. What is under test here is the branch and its constraints, not the
// price.
func TestCanonicalImportExecution(t *testing.T) {
	const (
		sourceAmount = 10 * units.Avax
		fee          = 1000
	)

	owner := warpfx.Owner{
		SourceChainID: ids.GenerateTestID(),
		SourceAddress: ids.GenerateTestShortID().Bytes(),
	}

	tests := []struct {
		name        string
		returned    uint64
		mutate      func(*platform.ImportTx)
		expectedErr error
	}{
		{
			name:     "canonical",
			returned: sourceAmount - fee,
		},
		{
			name:     "burning one extra fee is still within the band",
			returned: sourceAmount - 2*fee,
		},
		{
			name:        "burning two extra fees is not",
			returned:    sourceAmount - 3*fee,
			expectedErr: warpfx.ErrExcessiveBurn,
		},
		{
			name:     "the carrier cannot divert the funds",
			returned: sourceAmount - fee,
			mutate: func(tx *platform.ImportTx) {
				tx.Outs[0].Out.(*warpfx.TransferOutput).Owner = warpfx.Owner{
					SourceChainID: ids.GenerateTestID(),
					SourceAddress: ids.GenerateTestShortID().Bytes(),
				}
			},
			expectedErr: ErrCanonicalImportWrongOutput,
		},
		{
			name:     "nor split them",
			returned: sourceAmount - fee,
			mutate: func(tx *platform.ImportTx) {
				out := *tx.Outs[0].Out.(*warpfx.TransferOutput)
				out.Amt /= 2
				tx.Outs[0].Out.(*warpfx.TransferOutput).Amt -= out.Amt
				tx.Outs = append(tx.Outs, &avax.TransferableOutput{
					Asset: tx.Outs[0].Asset,
					Out:   &out,
				})
			},
			expectedErr: ErrCanonicalImportOutputCount,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require := require.New(t)

			env := newEnvironment(t, upgradetest.Latest)
			sharedMemory, utxos := warpFundedSharedMemory(t, env, env.ctx.XChainID, owner, sourceAmount)
			env.msm.SharedMemory = sharedMemory

			unsigned := &platform.ImportTx{
				BaseTx: platform.BaseTx{BaseTx: avax.BaseTx{
					NetworkID:    env.ctx.NetworkID,
					BlockchainID: env.ctx.ChainID,
					Outs: []*avax.TransferableOutput{{
						Asset: avax.Asset{ID: env.ctx.AVAXAssetID},
						Out: &warpfx.TransferOutput{
							Amt:   test.returned,
							Owner: owner,
						},
					}},
				}},
				SourceChain: env.ctx.XChainID,
				ImportedInputs: []*avax.TransferableInput{{
					UTXOID: utxos[0].UTXOID,
					Asset:  utxos[0].Asset,
					In:     &secp256k1fx.TransferInput{Amt: sourceAmount},
				}},
			}
			if test.mutate != nil {
				test.mutate(unsigned)
			}

			// Nobody signs a canonical import: every slot carries an empty
			// credential.
			tx := &platform.Tx{
				Unsigned: unsigned,
				Creds:    []verify.Verifiable{&warpfx.Credential{}},
			}
			require.NoError(tx.Initialize(platform.Codec))

			diff, err := state.NewDiff(lastAcceptedID, env, state.StakerAdditionAfterDeletionForbidden)
			require.NoError(err)

			_, _, _, err = StandardTx(&env.backend, txfee.NewSimpleCalculator(fee), tx, diff)
			require.ErrorIs(err, test.expectedErr)
			if test.expectedErr != nil {
				return
			}

			// The funds came back to their owner, and to nobody else.
			producedID := avax.UTXOID{
				TxID:        tx.ID(),
				OutputIndex: 0,
			}
			produced, err := diff.GetUTXO(producedID.InputID())
			require.NoError(err)
			require.Equal(&warpfx.TransferOutput{
				Amt:   test.returned,
				Owner: owner,
			}, produced.Out)
		})
	}
}

// TestNonCanonicalImportRefusesWarpUTXOs is the counterpart, and the reason the
// short-circuit is safe.
//
// An import mixing a signed input with a warpfx one is not canonical. It falls
// back on the ordinary path, where the warpfx UTXO reaches the Fx with a nil
// authorization and is refused. Generalized to "no carrier credential,
// therefore no Fx call", the short-circuit would instead make every warpfx UTXO
// spendable by anyone.
func TestNonCanonicalImportRefusesWarpUTXOs(t *testing.T) {
	require := require.New(t)

	const (
		warpAmount = 10 * units.Avax
		secpAmount = 5 * units.Avax
	)

	env := newEnvironment(t, upgradetest.Latest)

	sourceKey, err := secp256k1.NewPrivateKey()
	require.NoError(err)

	owner := warpfx.Owner{
		SourceChainID: ids.GenerateTestID(),
		SourceAddress: ids.GenerateTestShortID().Bytes(),
	}

	// Both kinds of UTXO in the same shared memory, exported by the same peer.
	fundedSharedMemoryCalls++
	m := atomic.NewMemory(prefixdb.New([]byte{fundedSharedMemoryCalls}, env.baseDB))
	var (
		sm               = m.NewSharedMemory(env.ctx.ChainID)
		peerSharedMemory = m.NewSharedMemory(env.ctx.XChainID)
		warpUTXO         = &avax.UTXO{
			UTXOID: avax.UTXOID{TxID: ids.GenerateTestID()},
			Asset:  avax.Asset{ID: env.ctx.AVAXAssetID},
			Out:    &warpfx.TransferOutput{Amt: warpAmount, Owner: owner},
		}
		secpUTXO = &avax.UTXO{
			UTXOID: avax.UTXOID{TxID: ids.GenerateTestID()},
			Asset:  avax.Asset{ID: env.ctx.AVAXAssetID},
			Out: &secp256k1fx.TransferOutput{
				Amt: secpAmount,
				OutputOwners: secp256k1fx.OutputOwners{
					Threshold: 1,
					Addrs:     []ids.ShortID{sourceKey.Address()},
				},
			},
		}
		elems []*atomic.Element
	)
	for _, utxo := range []*avax.UTXO{warpUTXO, secpUTXO} {
		utxoBytes, err := platform.Codec.Marshal(platform.CodecVersion, utxo)
		require.NoError(err)

		inputID := utxo.InputID()
		elems = append(elems, &atomic.Element{Key: inputID[:], Value: utxoBytes})
	}
	require.NoError(peerSharedMemory.Apply(map[ids.ID]*atomic.Requests{
		env.ctx.ChainID: {PutRequests: elems},
	}))
	env.msm.SharedMemory = sm

	// Imported inputs must be sorted by UTXOID, and the credentials stay
	// parallel to them.
	type entry struct {
		in     *avax.TransferableInput
		isWarp bool
	}
	entries := []entry{
		{
			in: &avax.TransferableInput{
				UTXOID: warpUTXO.UTXOID,
				Asset:  warpUTXO.Asset,
				In:     &secp256k1fx.TransferInput{Amt: warpAmount},
			},
			isWarp: true,
		},
		{
			in: &avax.TransferableInput{
				UTXOID: secpUTXO.UTXOID,
				Asset:  secpUTXO.Asset,
				In: &secp256k1fx.TransferInput{
					Amt:   secpAmount,
					Input: secp256k1fx.Input{SigIndices: []uint32{0}},
				},
			},
		},
	}
	slices.SortFunc(entries, func(a, b entry) int {
		return a.in.Compare(b.in)
	})

	unsigned := &platform.ImportTx{
		BaseTx: platform.BaseTx{BaseTx: avax.BaseTx{
			NetworkID:    env.ctx.NetworkID,
			BlockchainID: env.ctx.ChainID,
			Outs: []*avax.TransferableOutput{{
				Asset: avax.Asset{ID: env.ctx.AVAXAssetID},
				Out: &warpfx.TransferOutput{
					Amt:   warpAmount + secpAmount,
					Owner: owner,
				},
			}},
		}},
		SourceChain: env.ctx.XChainID,
	}
	for _, e := range entries {
		unsigned.ImportedInputs = append(unsigned.ImportedInputs, e.in)
	}

	// The signed slot carries a real signature, so the refusal below can only
	// come from the warpfx UTXO. Marshalled through the interface, exactly as
	// platform.Tx.Sign does, or the type prefix would be missing and the hash would
	// not be the one the verifier recomputes.
	var unsignedIntf platform.UnsignedTx = unsigned
	unsignedBytes, err := platform.Codec.Marshal(platform.CodecVersion, &unsignedIntf)
	require.NoError(err)
	sig, err := sourceKey.SignHash(hashing.ComputeHash256(unsignedBytes))
	require.NoError(err)

	tx := &platform.Tx{Unsigned: unsigned}
	for _, e := range entries {
		if e.isWarp {
			tx.Creds = append(tx.Creds, &warpfx.Credential{})
			continue
		}
		tx.Creds = append(tx.Creds, &secp256k1fx.Credential{
			Sigs: [][secp256k1.SignatureLen]byte{[secp256k1.SignatureLen]byte(sig)},
		})
	}
	require.NoError(tx.Initialize(platform.Codec))

	require.False(isCanonicalImport(&fx.Context{}, unsigned, []*avax.UTXO{warpUTXO, secpUTXO}))

	diff, err := state.NewDiff(lastAcceptedID, env, state.StakerAdditionAfterDeletionForbidden)
	require.NoError(err)

	_, _, _, err = StandardTx(&env.backend, txfee.NewSimpleCalculator(0), tx, diff)
	require.ErrorIs(err, warpfx.ErrNoAuthorization)
}

// TestCanonicalImportPricedByComplexity closes the loop the lot order left
// open: until warpfx outputs were priced, the complexity calculator answered
// errUnsupportedOutput, and a canonical import could not be executed at all
// with the dynamic fees the chain actually charges.
func TestCanonicalImportPricedByComplexity(t *testing.T) {
	require := require.New(t)

	const sourceAmount = 10 * units.Avax

	env := newEnvironment(t, upgradetest.Latest)
	owner := warpfx.Owner{
		SourceChainID: ids.GenerateTestID(),
		SourceAddress: ids.GenerateTestShortID().Bytes(),
	}
	sharedMemory, utxos := warpFundedSharedMemory(t, env, env.ctx.XChainID, owner, sourceAmount)
	env.msm.SharedMemory = sharedMemory

	diff, err := state.NewDiff(lastAcceptedID, env, state.StakerAdditionAfterDeletionForbidden)
	require.NoError(err)

	// The dynamic calculator built directly rather than through
	// PickFeeCalculator: this environment's genesis timestamp predates Etna, so
	// picking would hand back the flat calculator and test nothing. What is
	// under test is the complexity path.
	feeCalculator := txfee.NewDynamicCalculator(
		genesis.LocalParams.DynamicFeeConfig.Weights,
		genesis.LocalParams.DynamicFeeConfig.MinPrice,
	)

	newTx := func(returned uint64) *platform.Tx {
		tx := &platform.Tx{
			Unsigned: &platform.ImportTx{
				BaseTx: platform.BaseTx{BaseTx: avax.BaseTx{
					NetworkID:    env.ctx.NetworkID,
					BlockchainID: env.ctx.ChainID,
					Outs: []*avax.TransferableOutput{{
						Asset: avax.Asset{ID: env.ctx.AVAXAssetID},
						Out:   &warpfx.TransferOutput{Amt: returned, Owner: owner},
					}},
				}},
				SourceChain: env.ctx.XChainID,
				ImportedInputs: []*avax.TransferableInput{{
					UTXOID: utxos[0].UTXOID,
					Asset:  utxos[0].Asset,
					In:     &secp256k1fx.TransferInput{Amt: sourceAmount},
				}},
			},
			Creds: []verify.Verifiable{&warpfx.Credential{}},
		}
		require.NoError(tx.Initialize(platform.Codec))
		return tx
	}

	// There is no circularity: the amount is eight serialized bytes whatever
	// its value, so the fee does not move when the output does.
	fee, err := feeCalculator.CalculateFeeWithCredentials(newTx(0))
	require.NoError(err)
	require.Positive(fee)

	againstFinal, err := feeCalculator.CalculateFeeWithCredentials(newTx(sourceAmount - fee))
	require.NoError(err)
	require.Equal(fee, againstFinal)

	_, _, _, err = StandardTx(&env.backend, feeCalculator, newTx(sourceAmount-fee), diff)
	require.NoError(err)
}

// TestWarpUTXOUnspendableWithoutAuthorization is the second consensus-bug
// surface of the proposal, and the two cases it names are exactly what the
// canonical short-circuit must *not* catch.
//
// Generalized to "no carrier credential, therefore no Fx call", the
// short-circuit would make every warpfx UTXO spendable by anyone from any
// transaction. Both cases here have no carrier credential and both must fail:
// the UTXO reaches the Fx with a nil authorization and is refused.
func TestWarpUTXOUnspendableWithoutAuthorization(t *testing.T) {
	const (
		utxoAmount = 10 * units.Avax
		fee        = 1000
	)

	owner := warpfx.Owner{
		SourceChainID: ids.GenerateTestID(),
		SourceAddress: ids.GenerateTestShortID().Bytes(),
	}

	t.Run("from a BaseTx", func(t *testing.T) {
		require := require.New(t)

		env := newEnvironment(t, upgradetest.Latest)

		// A warpfx UTXO already resident on this chain.
		spent := &avax.UTXO{
			UTXOID: avax.UTXOID{TxID: ids.GenerateTestID()},
			Asset:  avax.Asset{ID: env.ctx.AVAXAssetID},
			Out:    &warpfx.TransferOutput{Amt: utxoAmount, Owner: owner},
		}
		env.state.AddUTXO(spent)
		require.NoError(env.state.Commit())

		tx := &platform.Tx{
			Unsigned: &platform.BaseTx{BaseTx: avax.BaseTx{
				NetworkID:    env.ctx.NetworkID,
				BlockchainID: env.ctx.ChainID,
				Ins: []*avax.TransferableInput{{
					UTXOID: spent.UTXOID,
					Asset:  spent.Asset,
					In:     &secp256k1fx.TransferInput{Amt: utxoAmount},
				}},
				Outs: []*avax.TransferableOutput{{
					Asset: avax.Asset{ID: env.ctx.AVAXAssetID},
					Out: &warpfx.TransferOutput{
						Amt:   utxoAmount - fee,
						Owner: owner,
					},
				}},
			}},
			// No carrier: every slot facing a warpfx input is empty.
			Creds: []verify.Verifiable{&warpfx.Credential{}},
		}
		require.NoError(tx.Initialize(platform.Codec))

		diff, err := state.NewDiff(lastAcceptedID, env, state.StakerAdditionAfterDeletionForbidden)
		require.NoError(err)

		_, _, _, err = StandardTx(&env.backend, txfee.NewSimpleCalculator(fee), tx, diff)
		require.ErrorIs(err, warpfx.ErrNoAuthorization)
	})

	t.Run("from an ImportTx with P-chain inputs", func(t *testing.T) {
		require := require.New(t)

		env := newEnvironment(t, upgradetest.Latest)

		// One warpfx UTXO in shared memory, and one already on this chain. The
		// non-empty Ins is what makes the import non-canonical: its content is
		// no longer derivable from what a submitter reads in shared memory.
		sharedMemory, imported := warpFundedSharedMemory(t, env, env.ctx.XChainID, owner, utxoAmount)
		env.msm.SharedMemory = sharedMemory

		resident := &avax.UTXO{
			UTXOID: avax.UTXOID{TxID: ids.GenerateTestID()},
			Asset:  avax.Asset{ID: env.ctx.AVAXAssetID},
			Out:    &warpfx.TransferOutput{Amt: utxoAmount, Owner: owner},
		}
		env.state.AddUTXO(resident)
		require.NoError(env.state.Commit())

		unsigned := &platform.ImportTx{
			BaseTx: platform.BaseTx{BaseTx: avax.BaseTx{
				NetworkID:    env.ctx.NetworkID,
				BlockchainID: env.ctx.ChainID,
				Ins: []*avax.TransferableInput{{
					UTXOID: resident.UTXOID,
					Asset:  resident.Asset,
					In:     &secp256k1fx.TransferInput{Amt: utxoAmount},
				}},
				Outs: []*avax.TransferableOutput{{
					Asset: avax.Asset{ID: env.ctx.AVAXAssetID},
					Out: &warpfx.TransferOutput{
						Amt:   2*utxoAmount - fee,
						Owner: owner,
					},
				}},
			}},
			SourceChain: env.ctx.XChainID,
			ImportedInputs: []*avax.TransferableInput{{
				UTXOID: imported[0].UTXOID,
				Asset:  imported[0].Asset,
				In:     &secp256k1fx.TransferInput{Amt: utxoAmount},
			}},
		}
		require.False(isCanonicalImport(&fx.Context{}, unsigned, []*avax.UTXO{resident, imported[0]}))

		tx := &platform.Tx{
			Unsigned: unsigned,
			Creds: []verify.Verifiable{
				&warpfx.Credential{},
				&warpfx.Credential{},
			},
		}
		require.NoError(tx.Initialize(platform.Codec))

		diff, err := state.NewDiff(lastAcceptedID, env, state.StakerAdditionAfterDeletionForbidden)
		require.NoError(err)

		_, _, _, err = StandardTx(&env.backend, txfee.NewSimpleCalculator(fee), tx, diff)
		require.ErrorIs(err, warpfx.ErrNoAuthorization)
	})
}
