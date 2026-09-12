# `AddPermissionlessValidatorTx`

## Ce qu'elle fait

Immobilise un montant d'AVAX en sorties de stake et inscrit un validateur, dont la sortie de l'ensemble déclenchera le remboursement du stake et, selon l'uptime, le versement d'une récompense.

## Variantes d'acteur

| Acteur | Possible ? |
| :--- | :--- |
| **EOA secp** | Oui |
| **`WarpOwner`** | Oui, comme payeur sous forme autorisée, et comme **propriétaire de récompenses** — y compris désigné par une transaction signée en secp, recevoir étant libre |

Les propriétaires de récompenses sont typés `fx.Owner`, une **interface** : `ValidatorRewardsOwner` et `DelegatorRewardsOwner` peuvent donc être des types distincts, et peuvent nommer un propriétaire différent du payeur.

## Construction

`AddPermissionlessValidatorTx{BaseTx, Validator, Subnet, Signer, StakeOuts []*avax.TransferableOutput, ValidatorRewardsOwner fx.Owner, DelegatorRewardsOwner fx.Owner, DelegationShares uint32}`.

`StakeOuts` s'ajoute aux `Outs` du `BaseTx` embarqué : la transaction consomme N UTXOs et produit une sortie de stake plus une sortie de change. Le remboursement du stake recrée **un UTXO par sortie de stake**, donc un staking consolide réellement.

## Autorisation

Une signature secp256k1 par input.

## Soumission

Par l'auteur, via `platform.issueTx`.

## Vérification, dans l'ordre du code

`verifyAddPermissionlessValidatorTx` :

1. **`sTx.SyntacticVerify(backend.Ctx)`**.
2. **`avax.VerifyMemoFieldLength(tx.Memo, isDurangoActive)`**.
3. **`resolveAuthorization(sTx, chainTime)`** → `fxCtx`. ⚠️ Placée **avant** la garde du point 4, sans quoi l'engagement ne serait pas vérifié du tout pendant le bootstrap.
4. ⚠️ **`if !backend.Bootstrapped.Get() { return nil }`** — **toute** la suite, flow check compris, est sautée pendant le bootstrap. C'est la garde la plus large des transactions P-Chain.
5. `startTime` = `currentTimestamp` depuis Durango, sinon `tx.StartTime()` ; `duration = tx.EndTime() - startTime` ; `verifyStakerStartTime`.
6. **`getValidatorRules(backend, chainState, tx.Subnet)`**, puis un `switch` : `Wght < minValidatorStake` → `ErrWeightTooSmall` ; `Wght > maxValidatorStake` → `ErrWeightTooLarge` ; `DelegationShares < minDelegationFee` → `ErrInsufficientDelegationFee` ; `duration < minStakeDuration` → `ErrStakeTooShort` ; `duration > maxStakeDuration` → `ErrStakeTooLong` ; `stakedAssetID != rules.assetID` → `ErrWrongStakedAssetID`.
7. **`GetValidator(chainState, tx.Subnet, tx.Validator.NodeID)`** — s'il en existe déjà un, `ErrDuplicateValidator`. Toute erreur autre que `database.ErrNotFound` remonte.
8. Hors Primary Network, `verifySubnetValidatorPrimaryNetworkRequirements`.
9. **`utxo.GetInputOutputs(tx)`** — `outs` agrège `Outs ‖ StakeOuts`.
10. **`feeCalculator.CalculateFeeWithCredentials(sTx)`** puis `producedAVAX += fee` — depuis la transaction **signée**, un credential porteur étant invisible depuis la forme non signée.
11. **`backend.FlowChecker.VerifySpendWithContext(fxCtx, tx, chainState, ins, outs, sTx.Creds, {AVAX: producedAVAX})`** → `ErrFlowCheckFailed`. Le déroulé par input est dans [`11-import-p.md`](11-import-p.md).

⚠️ **Les propriétaires de récompenses ne sont pas vérifiés ici**, au-delà de ce que le décodage impose. Ils ne sont consultés que des mois plus tard, à la sortie de l'ensemble, par `CreateOutput` — voir [`22-reward.md`](22-reward.md).

## Effets d'état

- Inputs consommés, `Outs` et `StakeOuts` produits.
- Un `Staker` inscrit dans l'ensemble courant ou pending, indexé par **`(subnetID, nodeID)`**.
- `SetStakingInfo(subnetID, nodeID, …)` porte notamment le `DelegateeReward` accumulé.

⚠️ **L'état n'indexe pas les stakers par transaction.** `Staker` porte bien un champ `TxID`, et la disposition sur disque est `validators/current|pending/{validator,delegator,…}/list/txID → …`, mais toutes les API de lecture en mémoire sont typées `(subnetID, nodeID)`. Retrouver un staker à partir de son `txID` demande de passer par `GetTx(txID)` pour en extraire `Validator.NodeID` et `Subnet`, puis de comparer `staker.TxID`.

## Ce qui est répercuté ailleurs

Rien vers une autre chaîne. La suite du cycle est interne à la P-Chain : `RewardValidatorTx` à la sortie de l'ensemble.

## Cas d'échec

| Échec | Ce qu'il laisse |
| :--- | :--- |
| N'importe lequel | Rien ; aucun fonds n'est immobilisé tant que la transaction n'est pas acceptée |

## Symboles concernés

- `vms/platformvm/platform/add_permissionless_validator_tx.go`
- `vms/platformvm/txs/executor/staker_tx_verification.go` — `verifyAddPermissionlessValidatorTx`, `getValidatorRules`, `verifyStakerStartTime`, `GetValidator`
- `vms/platformvm/txs/executor/standard_tx_executor.go` — `standardTxExecutor.AddPermissionlessValidatorTx`
- `vms/platformvm/state/stakers.go` — `Stakers`, `baseStakers`
- `vms/platformvm/fx/fx.go` — `Owner`
