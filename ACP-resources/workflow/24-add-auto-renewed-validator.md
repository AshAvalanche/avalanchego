# `AddAutoRenewedValidatorTx`

## Ce qu'elle fait

Ouvre un validateur qui se renouvelle de cycle en cycle jusqu'à ce qu'on l'arrête, en restakant une part configurable de ses récompenses. C'est la seule forme de staking qu'un contrat peut tenir indéfiniment sans qu'un opérateur ait à réémettre quoi que ce soit.

## Variantes d'acteur

| Acteur | Possible ? |
| :--- | :--- |
| **EOA secp** | Oui |
| **`WarpOwner`** | Oui, sous forme autorisée — comme payeur, comme propriétaire de récompenses, **et comme `ValidatorAuthority`** |

⚠️ **`ValidatorAuthority` est le champ qui compte.** C'est le propriétaire autorisé à modifier le validateur, et donc le seul à pouvoir l'arrêter — voir [`25-set-auto-renewed-config.md`](25-set-auto-renewed-config.md). Un `WarpOwner` peut l'occuper parce que `warpfx` implémente `VerifyPermissionWithContext` ; s'il ne le pouvait pas, le stake serait immobilisé **indéfiniment**.

## Construction

`AddAutoRenewedValidatorTx{BaseTx, ValidatorNodeID, Signer, StakeOuts, ValidatorRewardsOwner fx.Owner, DelegatorRewardsOwner fx.Owner, ValidatorAuthority fx.Owner, DelegationShares, AutoCompoundRewardShares, Period}`.

- **Trois** champs `fx.Owner`, tous des champs d'interface, donc tous porteurs d'un identifiant de type au codec. Ils peuvent nommer des propriétaires distincts et de types distincts.
- `AutoCompoundRewardShares` ∈ [0, 1 000 000] : la part des récompenses restakée à la fin de chaque cycle. `1_000_000` = tout restaker, `0` = tout retirer.
- `Period` : la durée d'un cycle, en secondes.
- `SubnetID()` est câblé à `constants.PrimaryNetworkID`.

## Autorisation

Une signature secp256k1 par input, ou **un** message Warp portant les octets de la transaction non signée.

## Vérification, dans l'ordre du code

`verifyAddAutoRenewedValidatorTx` (`staker_tx_verification.go`) :

1. **`sTx.SyntacticVerify`** puis **`avax.VerifyMemoFieldLength`**.
2. **`resolveAuthorization(sTx, chainTime)`** → `fxCtx`. ⚠️ Placée **avant** la garde du point 3.
3. ⚠️ **`if !backend.Bootstrapped.Get() { return nil }`** (l.895) — toute la suite est sautée, flow check compris.
4. Les règles de validateur : poids, durée, `DelegationShares`, asset staké, `AutoCompoundRewardShares ≤ PercentDenominator`, `Period` non nulle, `Signer` présent.
5. **`GetValidator`** — le nœud ne doit pas déjà valider.
6. **`FlowChecker.VerifySpendWithContext(fxCtx, …)`** — `outs` agrège `Outs ‖ StakeOuts`.

⚠️ **Les trois propriétaires ne sont pas vérifiés ici**, au-delà de ce que le décodage impose. `ValidatorRewardsOwner` et `DelegatorRewardsOwner` ne sont consultés qu'à chaque fin de cycle, par `CreateOutput` ; `ValidatorAuthority` ne l'est que si quelqu'un émet une `SetAutoRenewedValidatorConfigTx`.

## Effets d'état

- Inputs consommés, `Outs` et `StakeOuts` produits.
- Un `Staker` inscrit, indexé par `(subnetID, nodeID)`.
- `SetStakingInfo` porte le `NextPeriod`, les `AccruedValidationRewards`, les `AccruedDelegateeRewards` et le `DelegateeReward`.

## Ce qui est répercuté ailleurs

Rien vers une autre chaîne. Un `TxExecuted` rend le `txID` de cette transaction attestable — indispensable, puisque c'est sous ce `txID` que le **principal** sera remboursé à la sortie, aux index `len(Outs) + i`.

⚠️ Ce n'est **pas** sous ce `txID` que les récompenses arrivent : voir [`22-reward.md`](22-reward.md).

## Cas d'échec

| Échec | Ce qu'il laisse |
| :--- | :--- |
| N'importe lequel | Rien ; aucun fonds n'est immobilisé tant que la transaction n'est pas acceptée |
| `ValidatorAuthority` que personne ne peut prouver | ⚠️ Transaction **valide**, validateur **ingérable**, stake immobilisé indéfiniment. Rien ne s'y oppose côté consensus |

⚠️ **Un `ValidatorAuthority` en `warpfx.Owner` n'est pas rendu par `platform.getCurrentValidators`.** Le champ est laissé vide, comme le sont déjà les propriétaires de récompenses que l'endpoint ne sait pas formater. Refuser à la place ferait échouer l'appel entier — tous les validateurs, pas seulement celui-ci — dès qu'un seul propriétaire warp apparaît ; `platform.getWarpOwnerUTXOs` est de toute façon l'endroit où l'on interroge un tel propriétaire.

## Symboles concernés

- `vms/platformvm/platform/add_auto_renewed_validator_tx.go`
- `vms/platformvm/txs/executor/staker_tx_verification.go` — `verifyAddAutoRenewedValidatorTx`
- `vms/platformvm/txs/executor/standard_tx_executor.go` — `standardTxExecutor.AddAutoRenewedValidatorTx` (l.1374)
- `vms/platformvm/txs/fee/complexity.go` — `OwnerComplexity` × 3, dont `ValidatorAuthority` (l.842)
- `vms/warpfx/fx.go` — `CreateOutput`, `VerifyPermissionWithContext`
