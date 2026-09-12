// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package p

import (
	"math/big"
	"slices"

	"github.com/ava-labs/libevm/common"
	"github.com/onsi/ginkgo/v2"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/ava-labs/avalanchego/api/info"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/tests/fixture/e2e"
	"github.com/ava-labs/avalanchego/upgrade"
	"github.com/ava-labs/avalanchego/utils"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/crypto/secp256k1"
	"github.com/ava-labs/avalanchego/utils/hashing"
	"github.com/ava-labs/avalanchego/utils/units"
	"github.com/ava-labs/avalanchego/vms/components/avax"
	"github.com/ava-labs/avalanchego/vms/components/verify"
	"github.com/ava-labs/avalanchego/vms/platformvm"
	"github.com/ava-labs/avalanchego/vms/platformvm/platform"
	"github.com/ava-labs/avalanchego/vms/saevm/cchain"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"
	"github.com/ava-labs/avalanchego/vms/warpfx"
	"github.com/ava-labs/avalanchego/wallet/chain/p/builder"

	warpcontract "github.com/ava-labs/avalanchego/graft/coreth/precompile/contracts/warp"
	warpmessage "github.com/ava-labs/avalanchego/vms/platformvm/warp/message"
	saevmtx "github.com/ava-labs/avalanchego/vms/saevm/cchain/tx"
)

// What a transaction may consume, when some of its inputs are owned by a
// C-Chain address and others by a key.
//
// Three claims, none of which any unit test reaches. The existing mixed-input
// test is the one that must *fail* - a warpfx UTXO with no authorization - so
// nothing shows that the two kinds coexist when the authorization is there.
//
//  1. warpfx declares no input type: a secp256k1fx.TransferInput with empty
//     SigIndices references the consumed output and carries its exact amount.
//  2. Signed secp256k1 inputs coexist with warpfx ones in a single
//     transaction, each with its own kind of credential.
//  3. But every warpfx input must belong to the same owner, because there is
//     only one authorization and provenance is checked against each UTXO.
var _ = e2e.DescribePChain("[Warp UTXOs Mixed]", func() {
	tc := e2e.NewTestContext()
	require := require.New(tc)

	const (
		fundedAmount = 5 * units.Avax
		exportBid    = units.MilliAvax
		expiry       = uint64(1 << 62)
	)

	ginkgo.It("should spend warp and signed inputs together, from one owner only", func() {
		env := e2e.GetEnv(tc)
		nodeURI := env.GetRandomNodeURI()
		infoClient := info.NewClient(nodeURI.URI)

		// Nothing here observes the transition, so this only needs Helicon to
		// happen - see the note in the contract spec.
		upgrades, err := infoClient.Upgrades(tc.DefaultContext())
		require.NoError(err)
		if upgrades.HeliconTime.Equal(upgrade.UnscheduledActivationTime) {
			ginkgo.Skip("skipping test because Helicon isn't scheduled")
		}

		var (
			cClient   = cchain.NewClient(nodeURI.URI)
			pClient   = platformvm.NewClient(nodeURI.URI)
			ethClient = e2e.NewEthClient(tc, nodeURI)

			senderKey        = env.PreFundedKey
			senderEthAddress = senderKey.EthAddress()
		)

		cChainID, err := infoClient.GetBlockchainID(tc.DefaultContext(), "C")
		require.NoError(err)
		evmChainID, err := ethClient.ChainID(tc.DefaultContext())
		require.NoError(err)

		wallet := e2e.NewWallet(tc, secp256k1fx.NewKeychain(senderKey), nodeURI)
		pContext := wallet.P().Builder().Context()
		avaxAssetID := pContext.AVAXAssetID

		evm := &evmSender{tc: tc, client: ethClient, chainID: evmChainID}

		// Two warp owners, each an EOA's C-Chain address: an EOA calling
		// sendWarpMessage produces the same (chain, address) pair a contract
		// does, because the precompile forces the source address to its caller.
		ownerAKey := e2e.NewPrivateKey(tc)
		ownerBKey := e2e.NewPrivateKey(tc)
		ownerA := warpOwnerOf(cChainID, ownerAKey)
		ownerB := warpOwnerOf(cChainID, ownerBKey)

		nudgeKey := e2e.NewPrivateKey(tc)

		tc.By("funding both owners on the C-Chain so they can emit messages", func() {
			for _, key := range []*secp256k1.PrivateKey{ownerAKey, ownerBKey, nudgeKey} {
				addr := key.EthAddress()
				evm.send(senderKey, &addr, oneAvaxWei, nil, nil)
			}
		})

		var exportTx *saevmtx.Tx
		requireHeliconActivated(tc, infoClient)

		// Built *after* activation, so the nonce it captures cannot go stale
		// while the suite waits - unlike the EOA spec, nothing here needs the
		// export to exist before the transition.
		tc.By("building one export that funds both owners", func() {
			nonce := nextNonce(tc, ethClient, senderEthAddress)

			outs := []*avax.TransferableOutput{
				{
					Asset: avax.Asset{ID: avaxAssetID},
					Out:   &warpfx.TransferOutput{Amt: fundedAmount, Owner: ownerA},
				},
				{
					Asset: avax.Asset{ID: avaxAssetID},
					Out:   &warpfx.TransferOutput{Amt: fundedAmount, Owner: ownerB},
				},
			}
			// Sorted with the P-Chain's codec, which the saevm codec is aligned
			// with by construction - that alignment is what
			// TestCodecAlignedWithPlatformVM pins, and saevm's own codec is
			// unexported.
			avax.SortTransferableOutputs(outs, platform.Codec)

			export := &saevmtx.Export{
				NetworkID:        pContext.NetworkID,
				BlockchainID:     cChainID,
				DestinationChain: constants.PlatformChainID,
				Ins: []saevmtx.Input{{
					Address: senderEthAddress,
					Amount:  2*fundedAmount + exportBid,
					AssetID: avaxAssetID,
					Nonce:   nonce,
				}},
				ExportedOutputs: outs,
			}
			unsignedBytes, err := saevmtx.UnsignedBytes(export)
			require.NoError(err)
			sig, err := senderKey.Sign(unsignedBytes)
			require.NoError(err)

			exportTx = &saevmtx.Tx{
				Unsigned: export,
				Creds: []saevmtx.Credential{&secp256k1fx.Credential{
					Sigs: [][secp256k1.SignatureLen]byte{[secp256k1.SignatureLen]byte(sig)},
				}},
			}
		})

		tc.By("producing a block, then exporting to both owners", func() {
			recipient := common.Address(ids.GenerateTestShortID())
			evm.send(nudgeKey, &recipient, common.Big1, nil, nil)

			tc.Eventually(func() bool {
				err := cClient.IssueTx(tc.DefaultContext(), exportTx)
				if err == nil {
					return true
				}
				tc.Log().Info("export not yet accepted", zap.Error(err))
				return false
			}, e2e.DefaultTimeout, e2e.DefaultPollingInterval, "saevm did not accept the export before timeout")
		})

		var utxoA, utxoB *avax.UTXO
		tc.By("importing each owner's funds canonically, one transaction each", func() {
			// Canonical requires a single owner per import, which is exactly
			// claim 3 seen from the other side.
			utxoA = importOne(tc, pClient, pContext, cChainID, ownerA)
			utxoB = importOne(tc, pClient, pContext, cChainID, ownerB)
		})

		var secpUTXO *avax.UTXO
		tc.By("finding a signed UTXO of the sender on the P-Chain", func() {
			utxoBytes, _, _, err := pClient.GetUTXOs(
				tc.DefaultContext(),
				[]ids.ShortID{senderKey.Address()},
				10,
				ids.ShortEmpty,
				ids.Empty,
			)
			require.NoError(err)
			require.NotEmpty(utxoBytes)

			for _, b := range utxoBytes {
				utxo := &avax.UTXO{}
				_, err := platform.Codec.Unmarshal(b, utxo)
				require.NoError(err)

				out, ok := utxo.Out.(*secp256k1fx.TransferOutput)
				if !ok || out.Threshold != 1 || len(out.Addrs) != 1 || out.Locktime != 0 {
					continue
				}
				secpUTXO = utxo
				break
			}
			require.NotNil(secpUTXO, "the sender holds no simple secp256k1 UTXO")
		})

		tc.By("spending a warp input and a signed input in one transaction", func() {
			// Claim 2, and the point of this spec. The two credentials commit
			// to the same bytes - a signature and an authorization both bear on
			// tx.Unsigned.Bytes() - so neither invalidates the other.
			warpAmt := utxoA.Out.(*warpfx.TransferOutput).Amt
			secpAmt := secpUTXO.Out.(*secp256k1fx.TransferOutput).Amt

			inputs := []inputSpec{
				{utxo: utxoA, warp: true},
				{utxo: secpUTXO},
			}
			unsigned, order := newMixedBaseTx(tc, pContext, inputs, []*avax.TransferableOutput{{
				Asset: avax.Asset{ID: avaxAssetID},
				Out:   &warpfx.TransferOutput{Amt: warpAmt + secpAmt - exportBid, Owner: ownerA},
			}})

			// Marshalled through the interface, exactly as Tx.Sign does, so the
			// type prefix is present. Initialize cannot be used yet: it
			// marshals the credentials too, and they are what we are about to
			// build from these very bytes.
			unsignedBytes := unsignedBytesOf(tc, unsigned)

			tx := &platform.Tx{Unsigned: unsigned, Creds: make([]verify.Verifiable, len(order))}

			// The signed input's credential: an ordinary secp256k1 signature.
			hash := hashing.ComputeHash256(unsignedBytes)
			sig, err := senderKey.SignHash(hash)
			require.NoError(err)

			// The warp input's credential: the owner's authorization, which
			// commits to the very same bytes.
			signedMessage := authorizeAs(tc, env, evm, ownerAKey, expiry, unsignedBytes)

			for i, spec := range order {
				if spec.warp {
					tx.Creds[i] = &warpfx.Credential{WarpMessage: signedMessage}
					continue
				}
				tx.Creds[i] = &secp256k1fx.Credential{
					Sigs: [][secp256k1.SignatureLen]byte{[secp256k1.SignatureLen]byte(sig)},
				}
			}
			require.NoError(tx.Initialize(platform.Codec))

			txID, err := pClient.IssueTx(tc.DefaultContext(), tx.Bytes())
			require.NoError(err)
			awaitCommitted(tc, pClient, txID, "mixed transaction")

			tc.Log().Info("a signed input and an authorized input were spent together",
				zap.Stringer("txID", txID),
				zap.Uint64("signed", secpAmt),
				zap.Uint64("authorized", warpAmt),
			)
		})

		tc.By("refusing two warp inputs of different owners", func() {
			// Claim 3. There is exactly one authorization, and provenance is
			// checked against *each* consumed UTXO - so a second owner's UTXO
			// has nothing that covers it. No rule says "one owner per
			// transaction"; it follows from there being one carrier.
			// A's change from the previous step, plus B's untouched UTXO.
			remainingA, _, _, err := pClient.GetUTXOs(
				tc.DefaultContext(),
				[]ids.ShortID{ids.ShortID(ownerAKey.EthAddress())},
				10,
				ids.ShortEmpty,
				ids.Empty,
			)
			require.NoError(err)
			require.NotEmpty(remainingA)

			changeA := &avax.UTXO{}
			_, err = platform.Codec.Unmarshal(remainingA[0], changeA)
			require.NoError(err)

			var (
				amtA   = changeA.Out.(*warpfx.TransferOutput).Amt
				amtB   = utxoB.Out.(*warpfx.TransferOutput).Amt
				inputs = []inputSpec{
					{utxo: changeA, warp: true},
					{utxo: utxoB, warp: true},
				}
			)

			unsigned, order := newMixedBaseTx(tc, pContext, inputs, []*avax.TransferableOutput{{
				Asset: avax.Asset{ID: avaxAssetID},
				Out:   &warpfx.TransferOutput{Amt: amtA + amtB - exportBid, Owner: ownerA},
			}})

			tx := &platform.Tx{Unsigned: unsigned, Creds: make([]verify.Verifiable, len(order))}

			// Owner A authorizes, so B's UTXO is the one nothing covers.
			signedMessage := authorizeAs(tc, env, evm, ownerAKey, expiry, unsignedBytesOf(tc, unsigned))
			for i, spec := range order {
				if spec.utxo.UTXOID == changeA.UTXOID {
					tx.Creds[i] = &warpfx.Credential{WarpMessage: signedMessage}
					continue
				}
				// Every other warpfx slot carries an empty message: exactly one
				// credential is the carrier.
				tx.Creds[i] = &warpfx.Credential{}
			}
			require.NoError(tx.Initialize(platform.Codec))

			_, err = pClient.IssueTx(tc.DefaultContext(), tx.Bytes())
			require.ErrorContains(err, "authorization does not cover this owner") //nolint:forbidigo // the error crosses a JSON-RPC boundary as a string
			tc.Log().Info("a second owner's input was refused, as it must be",
				zap.Error(err),
			)
		})
	})
})

// inputSpec pairs a UTXO with the kind of credential it expects.
type inputSpec struct {
	utxo *avax.UTXO
	warp bool
}

// newMixedBaseTx builds a BaseTx consuming [inputs], and returns them in the
// sorted order the credentials must follow.
//
// Creds is parallel to Ins, and Ins is sorted by UTXOID - so which slot holds a
// signature and which holds an authorization is decided by that sort, not by
// the order they were listed in.
func newMixedBaseTx(
	tc *e2e.GinkgoTestContext,
	pContext *builder.Context,
	inputs []inputSpec,
	outs []*avax.TransferableOutput,
) (*platform.BaseTx, []inputSpec) {
	require := require.New(tc)

	slices.SortFunc(inputs, func(a, b inputSpec) int {
		return a.utxo.UTXOID.Compare(&b.utxo.UTXOID)
	})

	tx := &platform.BaseTx{BaseTx: avax.BaseTx{
		NetworkID:    pContext.NetworkID,
		BlockchainID: constants.PlatformChainID,
		Outs:         outs,
	}}
	for _, spec := range inputs {
		in := &secp256k1fx.TransferInput{Amt: amountOf(tc, spec.utxo)}
		if !spec.warp {
			// The one place the two differ: a signed input names which of the
			// output's addresses signs. A warpfx input names nothing.
			in.SigIndices = []uint32{0}
		}
		tx.Ins = append(tx.Ins, &avax.TransferableInput{
			UTXOID: spec.utxo.UTXOID,
			Asset:  spec.utxo.Asset,
			In:     in,
		})
	}
	require.True(utils.IsSortedAndUnique(tx.Ins))
	return tx, inputs
}

func amountOf(tc *e2e.GinkgoTestContext, utxo *avax.UTXO) uint64 {
	out, ok := utxo.Out.(avax.TransferableOut)
	require.True(tc, ok)
	return out.Amount()
}

// authorizeAs has [key] emit a Warp authorization over [txBytes] and gathers
// the validators' signatures.
//
// field of the message and belongs in the signature.
//
//nolint:unparam // every spec passes the same far-future expiry, but it is a
func authorizeAs(
	tc *e2e.GinkgoTestContext,
	env *e2e.TestEnvironment,
	evm *evmSender,
	key *secp256k1.PrivateKey,
	expiry uint64,
	txBytes []byte,
) []byte {
	require := require.New(tc)

	authorization, err := warpmessage.NewTxAuthorization(expiry, txBytes)
	require.NoError(err)

	input, err := warpcontract.PackSendWarpMessage(authorization.Bytes())
	require.NoError(err)

	receipt := evm.send(key, &warpcontract.ContractAddress, big.NewInt(0), input, nil)
	return aggregateFromReceipt(tc, env, receipt)
}

// warpOwnerOf is the (chain, address) pair a key's C-Chain address names.
func warpOwnerOf(cChainID ids.ID, key *secp256k1.PrivateKey) warpfx.Owner {
	addr := key.EthAddress()
	return warpfx.Owner{
		SourceChainID: cChainID,
		SourceAddress: addr[:],
	}
}

// importOne waits for [owner]'s exported UTXO and imports it canonically.
func importOne(
	tc *e2e.GinkgoTestContext,
	client *platformvm.Client,
	pContext *builder.Context,
	cChainID ids.ID,
	owner warpfx.Owner,
) *avax.UTXO {
	require := require.New(tc)

	var utxo *avax.UTXO
	tc.Eventually(func() bool {
		utxoBytes, _, _, err := client.GetAtomicUTXOs(
			tc.DefaultContext(),
			[]ids.ShortID{ids.ShortID(owner.SourceAddress)},
			"C",
			10,
			ids.ShortEmpty,
			ids.Empty,
		)
		require.NoError(err)
		if len(utxoBytes) == 0 {
			return false
		}
		utxo = &avax.UTXO{}
		_, err = platform.Codec.Unmarshal(utxoBytes[0], utxo)
		require.NoError(err)
		return true
	}, e2e.DefaultTimeout, e2e.DefaultPollingInterval, "an exported UTXO was not discoverable before timeout")

	return issueCanonicalImport(tc, client, pContext, cChainID, owner, utxo)
}

// unsignedBytesOf returns the bytes an authorization commits to and a signature
// signs.
//
// Marshalled through the interface, as Tx.Sign does, so the type prefix is
// there. Tx.Initialize cannot serve here: it marshals the credentials as well,
// and those are built *from* these bytes.
func unsignedBytesOf(tc *e2e.GinkgoTestContext, unsigned platform.UnsignedTx) []byte {
	unsignedIntf := unsigned
	b, err := platform.Codec.Marshal(platform.CodecVersion, &unsignedIntf)
	require.NoError(tc, err)

	unsigned.SetBytes(b)
	return b
}
