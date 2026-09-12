# `BaseTx` P-Chain

## Ce qu'elle fait

Un transfert P-Chain nu : consomme des UTXOs, en produit d'autres, brûle les frais. C'est l'outil de consolidation et de transfert intra-chaîne.

## Variantes d'acteur

| Acteur | Possible ? |
| :--- | :--- |
| **EOA secp** | Oui |
| **`WarpOwner`** | Oui, sous forme autorisée. Il peut aussi **recevoir** sans rien autoriser : les sorties ne passent jamais par le Fx |

## Construction

`BaseTx{avax.BaseTx}` — `NetworkID`, `BlockchainID`, `Outs`, `Ins`, `Memo`. Rien d'autre.

Inputs triés par `UTXOID`, sorties triées sur `(assetID, octets marshallés de la sortie)`.

## Autorisation

Une signature secp256k1 par input.

## Vérification, dans l'ordre du code

`standardTxExecutor.BaseTx` :

1. ⚠️ **`if !upgrades.IsDurangoActivated(currentTimestamp) { return ErrDurangoUpgradeNotActive }`** — c'est la **première** chose testée, avant même `SyntacticVerify`. `BaseTx` n'existe qu'à partir de Durango.
2. **`e.tx.SyntacticVerify(e.backend.Ctx)`**.
3. **`avax.VerifyMemoFieldLength(tx.Memo, true)`** — mémo vide exigé, sans condition puisque Durango est acquis au point 1.
4. **`resolveAuthorization(e.tx, chainTime)`** → `fxCtx`.
5. **`utxo.GetInputOutputs(tx)`**.
6. **`e.feeCalculator.CalculateFeeWithCredentials(e.tx)`** puis `producedAVAX += fee` — depuis la transaction **signée**, un credential porteur étant invisible depuis la forme non signée.
7. **`e.backend.FlowChecker.VerifySpendWithContext(fxCtx, tx, e.state, ins, outs, e.tx.Creds, {AVAX: producedAVAX})`**.
8. **`avax.Consume`** / **`avax.Produce`**.

⚠️ **Aucune garde de bootstrap.** `BaseTx` est, avec l'`ExportTx`, l'une des rares transactions P-Chain dont le flow check tourne dans tous les cas.

## Effets d'état

Inputs consommés, sorties produites et indexées par adresse via `avax.Produce` → `utxoState.PutUTXO`, qui n'indexe que si la sortie implémente `avax.Addressable`.

**Une `BaseTx` signée en secp256k1 peut donc produire un `warpfx.TransferOutput`**, indexé sous la `SourceAddress` de son propriétaire : n'importe qui peut alimenter n'importe quel `WarpOwner`, et recevoir n'est pas dépenser. ⚠️ Avant l'activation de Helicon, `StandardTx` refuse la transaction — voir la garde d'activation dans [`00-acteurs.md`](00-acteurs.md).

⚠️ **Elle ne peut pas le produire enveloppé dans un `stakeable.LockOut`.** Rien dans les types ne s'y oppose — le champ `TransferableOut` est une interface, `LockOut.Verify()` ne refuse que l'imbrication, et `VerifySpendUTXOs` autorise explicitement de produire du verrouillé à partir de fonds **déverrouillés**. C'est donc une règle et non une conséquence : `verifyWarpOutputsNotLocked` rejette le montage sur `ErrWarpOutputNotLockable`, inconditionnellement, en tête de `StandardTx`. Sans elle, un tiers verrouillerait des fonds au profit d'un `WarpOwner` qui n'a rien demandé, avec un `Locktime` évalué contre `h.clk.Time()` — **l'horloge locale du nœud** — alors que tout le reste de `warpfx` s'évalue contre le `chainTime`.

## Ce qui est répercuté ailleurs

Rien : `BaseTx` ne touche pas la shared memory et ne pose aucun `atomicRequests`.

## Note de périmètre

Une `BaseTx` ne fait rien qu'une `ExportTx` ne puisse faire : celle-ci embarque `avax.BaseTx`, n'exige qu'une sortie exportée non nulle, et ses `Outs` peuvent nommer n'importe quel propriétaire. Une `ExportTx` qui exporte une poussière et met le reste en change réalise déjà un transfert intra-P arbitraire. `BaseTx` rend l'opération **directe** plutôt que possible.

Ce qu'elle apporte en propre : la **consolidation à la demande**, sans franchissement de frontière ni immobilisation, et une dépense vers soi-même — la façon la plus économique d'invalider délibérément une autorisation en attente sans attendre son `expiry`.

## Symboles concernés

- `vms/platformvm/platform/base_tx.go`
- `vms/platformvm/txs/executor/standard_tx_executor.go` — `standardTxExecutor.BaseTx`
- `vms/platformvm/utxo/verifier.go` — `VerifySpend`
