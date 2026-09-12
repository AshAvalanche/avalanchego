# `RewardValidatorTx` et la distribution des récompenses

## Ce qu'elle fait

Sort un staker de l'ensemble courant, rembourse son stake, et verse — ou non — sa récompense. C'est une **transaction de proposition** : le consensus tranche entre une branche *commit* et une branche *abort*, selon l'uptime mesuré.

## Variantes d'acteur

Aucune : cette transaction n'est pas émise par un utilisateur. Elle est construite par le builder de blocs quand un staker atteint sa fin de période.

Ce qui varie, c'est le **type du propriétaire des récompenses**, transporté par la transaction de staking d'origine et typé `fx.Owner`.

## Autorisation

Aucune. Elle n'a pas d'inputs à autoriser.

## Vérification, dans l'ordre du code

`proposalTxExecutor.RewardValidatorTx` puis, selon le staker, `rewardValidatorTx` ou `rewardDelegatorTx`.

### `rewardValidatorTx`

```
txID       = validator.TxID          ← le txID de la transaction de STAKING, pas celui du RewardValidatorTx
stake      = uValidatorTx.Stake()
outputs    = uValidatorTx.Outputs()
stakeAsset = stake[0].Asset          ← invariant : asset staké == asset de récompense
```

1. **`unstakeUTXOs(uValidatorTx, txID, e.onCommitState)`** et la même chose sur `onAbortState` : chaque sortie de stake est recréée à `OutputIndex = uint32(len(stakerTx.Outputs()) + i)`. ⚠️ **`unstakeUTXOs` n'appelle que `AddUTXO`, jamais `AddRewardUTXO`** — le remboursement du stake **n'est pas** dans `GetRewardUTXOs`.
2. `utxosOffset := 0`.
3. Si `validator.PotentialReward > 0` : `newUTXO(reward, uValidatorTx.ValidationRewardsOwner(), txID, uint32(len(outputs)+len(stake)), stakeAsset)` → `AddUTXO` **et** `AddRewardUTXO` sur `onCommitState` ; puis `utxosOffset++`.
4. `GetStakingInfo(subnetID, nodeID).DelegateeReward` ; si nul, fin.
5. Sinon `newUTXO(delegateeReward, uValidatorTx.DelegationRewardsOwner(), txID, uint32(len(outputs)+len(stake)+utxosOffset), stakeAsset)` sur `onCommitState`.
6. ⚠️ **Sur la branche *abort*, l'index est `uint32(len(outputs)+len(stake))`, sans `offset`** — la récompense de validation n'ayant pas été versée. Le code y construit l'`avax.UTXO` **directement**, en réutilisant l'`Out` déjà créé, sans repasser par `newUTXO`/`CreateOutput`.

> **L'`OutputIndex` de la récompense de délégataire dépend donc de l'issue commit/abort**, elle-même fonction de l'uptime. Aucun engagement pris à l'avance ne peut le contenir.

⚠️ **C'est précisément ce que `StakeSettled` doit dire à un contrat**, et l'arithmétique est verrouillée par un test qui exécute un staking complet jusqu'à son `RewardValidatorTx` : sur la branche *commit* la récompense de validation occupe le premier rang libre et celle de délégataire le suivant ; sur la branche *abort* la seconde **remonte** dans le rang que la première aurait occupé, et rien ne la suit. Les deux assertions sont mutuellement exclusives, donc le décalage est réellement observé et pas seulement décrit.

### `rewardDelegatorTx`

Même ossature. `reward.Split(delegator.PotentialReward, vdrTx.Shares())` sépare `delegateeReward` et `delegatorReward`.

- Récompense du délégateur, si `> 0` : `newUTXO(reward, uDelegatorTx.RewardsOwner(), txID, uint32(len(outputs)+len(stake)), stakeAsset)`, puis `utxosOffset++`.
- Part du délégataire : **si `IsCortinaActivated(validator.StartTime)`, elle n'est pas versée en UTXO** mais accumulée dans `stakingInfo.DelegateeReward`. Sinon (validateurs pré-Cortina) elle est émise immédiatement à `uint32(len(outputs)+len(stake)+utxosOffset)`.
- Les deux branches n'écrivent que sur `e.onCommitState`.

### Le point de dispatch

`proposalTxExecutor.newUTXO` est le **seul** appelant de `CreateOutput` :

```go
resolvedFx := e.backend.Fxs.Get(owner)   // sinon ErrNoFxForRewardsOwner
outIntf, err := resolvedFx.CreateOutput(amount, owner)
out, ok := outIntf.(verify.State)        // sinon ErrInvalidState
return &avax.UTXO{UTXOID{TxID: txID, OutputIndex: outputIndex}, asset, out}
```

**L'extension est résolue sur le type du propriétaire des récompenses.** C'est le second site de résolution, et le seul qui ne puisse pas s'appuyer sur une sortie consommée : à ce moment la transaction de staking est acceptée depuis des mois, et il ne reste que le propriétaire qu'elle transportait.

| Propriétaire | Extension atteinte | Sortie produite |
| :--- | :--- | :--- |
| `*secp256k1fx.OutputOwners` | `secp256k1fx`, **par repli** (le type n'est revendiqué par personne) | `secp256k1fx.TransferOutput` |
| `*warpfx.Owner` | `warpfx`, par revendication | `warpfx.TransferOutput` |

⚠️ **Croiser les deux échoue plutôt que de produire silencieusement la mauvaise sortie** : `CreateOutput` type-asserte son propriétaire et renvoie `ErrWrongOwnerType` sinon.

⚠️ **Un `Fxs` nul fait échouer `newUTXO` sur `ErrNoFxForRewardsOwner`**, sans repli sur `Backend.Fx`. Le repli existerait, il masquerait une injection incomplète du VM sur un chemin de consensus.

⚠️ **`newUTXO` exige que la sortie créée satisfasse `verify.State`.** Tout nouveau type de sortie de récompense doit donc embarquer `verify.IsState`.

### `RewardAutoRenewedValidatorTx`

Un cycle de validateur auto-renouvelé se clôt par cette transaction de proposition, qu'un `Timestamp` — l'heure de fin de cycle — rend distincte d'un cycle à l'autre pour le même validateur. Elle ne porte **aucun** credential : `len(e.tx.Creds) != 0` la refuse.

Les trois issues :

| Issue | Ce qui se passe |
| :--- | :--- |
| **commit**, `NextPeriod > 0` | Les récompenses sont partagées selon `AutoCompoundRewardShares` : la part restakée augmente le poids du validateur (plafonné à `MaxValidatorStake`), la part retirée devient des UTXOs. Le cycle suivant démarre. |
| **commit**, `NextPeriod == 0` | **Sortie gracieuse** : `DeleteCurrentValidator`, principal rendu, toutes les récompenses du cycle versées |
| **abort** | Le validateur est retiré **dans tous les cas**, le principal rendu, la récompense potentielle du cycle abandonnée ; seules les récompenses accumulées sont versées |

> ⚠️ **Un validateur auto-renouvelé sort donc bel et bien de l'ensemble** — gracieusement via `Period = 0`, ou sur un défaut d'uptime. Il est tentant de croire l'inverse ; c'est faux, et c'est ce qui rend son attestation de cycle signable.

Toutes les issues passent par `mintRewards`, y compris la part retirée du cycle continu (`restakeAutoRenewedValidatorOnCommit`) et la branche abort (`mintRewardOnAbort`). Deux différences avec le cas à durée fixe :

- ⚠️ **le `txID` est celui du `RewardAutoRenewedValidatorTx` lui-même** (`e.tx.ID()`), pas celui du staking. `AddRewardUTXO` est donc keyée dessus, et **`GetRewardUTXOs(stakingTxID)` rend toujours une liste vide** pour un auto-renouvelé ;
- l'asset est codé en dur à `AVAXAssetID`, et l'offset part de `len(e.tx.Unsigned.Outputs())`.

⚠️ Le commentaire du code pose une invariante explicite : `mintRewards` **doit être appelée au plus une fois par diff**, les index étant dérivés de `len(Outputs())` sans tenir compte des UTXOs qu'un appel précédent aurait déjà ajoutés — un second appel collisionnerait sur les mêmes `(txID, outputIndex)`.

⚠️ **La part auto-composée ne devient jamais un UTXO.** Elle augmente le poids du validateur et les récompenses accumulées. Un contrat qui compte ses UTXOs ne la verra donc pas ; elle lui revient au remboursement du principal, à la sortie.

**Ce qui est répercuté ailleurs** : un `CycleSettled` par cycle, keyé sur le `txID` de *cette* transaction. Le principal, lui, revient sous le `txID` **de staking**, aux index `len(outputs) + i`, et n'est dans aucun message — un contrat qui tient ce `txID` (via `TxExecuted`) les calcule seul. Voir [`31-attestations.md`](31-attestations.md).

## Effets d'état

- Stake remboursé, en autant d'UTXOs que de sorties de stake.
- Récompenses ajoutées via `AddUTXO` **et** `AddRewardUTXO`.
- Staker retiré de l'ensemble courant.

## Ce qui est répercuté ailleurs

Une récompense versée à un `WarpOwner` devient attestable : le handler ACP-118 signe un `StakeSettled` qui la nomme, une fois le staker sorti de l'ensemble (voir [`31-attestations.md`](31-attestations.md)). C'est indispensable, parce que l'`OutputIndex` d'une récompense de délégataire **dépend de l'issue commit/abort**, donc de l'uptime mesuré : aucun engagement pris à l'avance ne peut le contenir.

`GetRewardUTXOs(txID)` existe **sur le `*state.State` concret uniquement** : ni l'interface `state.Chain` ni `*state.Diff` ne l'exposent, alors que `Chain` déclare bien `AddRewardUTXO`. Le paquet réseau contourne cela par une interface locale `network.Chain` qui l'élargit. L'API `platform.getRewardUTXOs` y accède par l'état concret, et son client est marqué `Deprecated: GetRewardUTXOs should be fetched from a dedicated indexer.`

## Cas d'échec

| Échec | Ce qu'il laisse |
| :--- | :--- |
| `CreateOutput` refuse le type du propriétaire | La transaction de récompense échoue **après** que le stake a été immobilisé pendant toute sa période |

## Symboles concernés

- `vms/platformvm/txs/executor/proposal_tx_executor.go` — `rewardValidatorTx`, `rewardDelegatorTx`, `newUTXO`, `unstakeUTXOs`, `mintRewards`, `mintRewardOnAbort`
- `vms/platformvm/platform/reward_validator_tx.go`, `reward_auto_renewed_validator_tx.go`
- `vms/secp256k1fx/fx.go` — `CreateOutput`
- `vms/warpfx/fx.go` — `CreateOutput`
- `vms/platformvm/fx/fxs.go` — `Fxs.Get`
- `vms/platformvm/state/state.go` — `GetRewardUTXOs`
- `vms/platformvm/service.go` — `GetRewardUTXOs`
