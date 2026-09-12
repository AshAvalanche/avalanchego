# `ExportTx` P-Chain → une autre chaîne du subnet

## Ce qu'elle fait

Consomme des UTXOs P-Chain et dépose les sorties exportées en shared memory, à destination d'une autre chaîne du même subnet.

## Variantes d'acteur

| Acteur | Possible ? |
| :--- | :--- |
| **EOA secp** | Oui |
| **`WarpOwner`** | Oui, sous forme autorisée. Il peut aussi **recevoir** une sortie exportée sans rien autoriser |

## Construction

`ExportTx{BaseTx, DestinationChain ids.ID, ExportedOutputs []*avax.TransferableOutput}`.

⚠️ `ExportTx` **embarque `avax.BaseTx`**, dont les `Outs` — le change — peuvent nommer **n'importe quel propriétaire**. Sa seule contrainte syntaxique propre est de porter au moins une sortie exportée non nulle. Une `ExportTx` qui exporte une poussière et met tout le reste en change réalise donc un transfert intra-P arbitraire.

Sorties exportées triées sur `(assetID, octets marshallés)`, comme les `Outs`.

## Autorisation

Une signature secp256k1 par input.

## Soumission

Par l'auteur, via `platform.issueTx`.

## Vérification, dans l'ordre du code

`standardTxExecutor.ExportTx` :

1. **`e.tx.SyntacticVerify(e.backend.Ctx)`**.
2. **`avax.VerifyMemoFieldLength(tx.Memo, isDurangoActive)`**.
3. **`resolveAuthorization(e.tx, chainTime)`** → `fxCtx`.
4. **`verifyWarpExportDestination(e.backend.Ctx.CChainID, tx)`** — si l'un des `ExportedOutputs` est un `warpfx.TransferOutput`, `DestinationChain` **doit** être la C-Chain, sinon `ErrWarpOutputWrongDestination`. Inconditionnel, hors de toute garde de bootstrap.
5. ⚠️ **`if e.backend.Bootstrapped.Get()` → `verify.SameSubnet(ctx, tx.DestinationChain)`.** La garde de bootstrap ne couvre **que** ce test, contrairement à l'`ImportTx`.
6. **`utxo.GetInputOutputs(tx)`** → `(ins, outs, producedAVAX)`. `outs` agrège `Outs ‖ ExportedOutputs`.
7. **`e.feeCalculator.CalculateFeeWithCredentials(e.tx)`** puis `producedAVAX += fee` — depuis la transaction **signée** : le message d'autorisation vit dans les credentials et serait invisible depuis la forme non signée, donc gratuit.
8. **`e.backend.FlowChecker.VerifySpendWithContext(fxCtx, tx, e.state, ins, outs, e.tx.Creds, {AVAX: producedAVAX})`** — résout chaque UTXO par `e.state.GetUTXO(input.InputID())` puis délègue à `VerifySpendUTXOsWithContext` (déroulé dans [`11-import-p.md`](11-import-p.md)).
9. **`avax.Consume`** / **`avax.Produce`**.
10. Construction des `atomic.Element` (voir ci-dessous).

⚠️ **Pour les sorties secp, `DestinationChain` n'est contrainte que par `verify.SameSubnet`**, et ce test est en outre sauté pendant le bootstrap. La X-Chain reste donc une destination valide depuis la P-Chain.

Une sortie `warpfx` fait exception, et c'est une règle de consensus des deux côtés plutôt qu'un confort d'outillage. `warpfx.TransferOutput` n'est enregistré que dans le codec de la PlatformVM et dans le codec atomique de la C-Chain : déposé en shared memory P↔X, il serait parfaitement constructible et **définitivement indécodable** par la chaîne censée le consommer. L'export débite avant que la chaîne cible n'ait rien à dire, donc l'échec serait silencieux et irréversible — et que seul le propriétaire puisse déclencher une telle transaction ne rend pas la perte acceptable.

⚠️ **Le seul type de sortie explicitement rejeté à l'export est `stakeable.LockOut`**, dans `ExportTx.SyntacticVerify`. Le contrôle vise un **type nommé**, pas la notion de verrou : une sortie d'un autre type portant son propre champ `Locktime` s'exporterait verrouillée.

## Effets d'état

- Les `Ins` sont consommés.
- Les `Outs` (le change) sont produits sur la P-Chain, aux indices `0 … len(Outs)-1`.
- Les `ExportedOutputs` deviennent des UTXOs en shared memory, aux indices **`len(tx.Outs) + i`**.

## Ce qui est répercuté ailleurs

Pour chaque sortie exportée, un `avax.UTXO{UTXOID{TxID: txID, OutputIndex: uint32(len(tx.Outs) + i)}, Asset, Out}`, sérialisé avec **`platform.Codec`**, donne un `atomic.Element{Key: utxo.InputID(), Value: utxoBytes}`, complété de `elem.Traits = out.Addresses()` **si et seulement si** `utxo.Out` implémente `avax.Addressable`. Le tout dans `e.atomicRequests = {DestinationChain: {PutRequests: elems}}`, appliqué par `acceptor.go`.

## Cas d'échec

| Échec | Ce qu'il laisse |
| :--- | :--- |
| Flow check, credential | Rien |
| Sortie **secp** exportée vers une chaîne qui ne sait pas décoder son type | **UTXO déposé, inconsommable, fonds perdus.** Aucun contrôle ne s'y oppose |
| Sortie **`warpfx`** vers une destination autre que la C-Chain | `ErrWarpOutputWrongDestination` avant tout débit. Rien n'est perdu |
| Sortie sans trait | UTXO déposé et consommable, mais **introuvable** par la découverte de la chaîne cible |

Une sortie exportée `warpfx.TransferOutput` échappe au dernier cas : elle implémente `avax.Addressable` et porte donc un trait, la `SourceAddress` de son propriétaire. Vingt octets, donc `avax.GetAtomicUTXOs` la lit sans modification — c'est tout l'intérêt de ne pas avoir choisi un hachage de 32 octets comme clé.

## Symboles concernés

- `vms/platformvm/txs/executor/standard_tx_executor.go` — `standardTxExecutor.ExportTx`
- `vms/platformvm/txs/executor/warp_export.go` — `verifyWarpExportDestination`
- `vms/platformvm/platform/export_tx.go` — `SyntacticVerify`, rejet de `stakeable.LockOut`
- `vms/platformvm/utxo/verifier.go` — `VerifySpend`, `GetInputOutputs`
- `vms/components/avax/state.go` — `Addressable`
- `vms/platformvm/block/executor/acceptor.go`
