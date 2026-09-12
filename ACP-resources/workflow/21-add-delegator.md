# `AddPermissionlessDelegatorTx`

## Ce qu'elle fait

Immobilise un montant d'AVAX au profit d'un validateur existant, dont il augmente le poids ; le stake est remboursé à la fin de la période et une récompense est versée selon l'uptime du validateur.

## Variantes d'acteur

Identiques à [`20-add-validator.md`](20-add-validator.md) : EOA secp ou `WarpOwner` sous forme autorisée, propriétaire de récompenses typé `fx.Owner`.

Un délégateur n'a **qu'un** propriétaire de récompenses, `RewardsOwner`, là où un validateur en a deux.

## Construction

`AddPermissionlessDelegatorTx{BaseTx, Validator, Subnet, StakeOuts []*avax.TransferableOutput, DelegationRewardsOwner fx.Owner}`.

## Autorisation

Une signature secp256k1 par input.

## Vérification, dans l'ordre du code

`verifyAddPermissionlessDelegatorTx` — même ossature que celle du validateur, avec quatre différences :

1. `sTx.SyntacticVerify` puis `avax.VerifyMemoFieldLength`.
2. **`resolveAuthorization(sTx, chainTime)`** → `fxCtx`, avant la garde suivante.
3. ⚠️ **`if !backend.Bootstrapped.Get() { return nil }`** — même garde large.
4. `verifyStakerStartTime`, puis `getDelegatorRules` et le `switch` : `Wght < minDelegatorStake` → `ErrWeightTooSmall` ; `duration < minStakeDuration` → `ErrStakeTooShort` ; `duration > maxStakeDuration` → `ErrStakeTooLong` ; `stakedAssetID != rules.assetID` → `ErrWrongStakedAssetID`. **Pas de `maxDelegatorStake` ni de contrôle de frais de délégation.**
5. **`GetValidator(chainState, tx.Subnet, tx.Validator.NodeID)`** — ici le validateur **doit** exister, à l'inverse du cas validateur.
6. **`platform.BoundedBy(...)`** — la période de délégation doit être incluse dans celle du validateur, sinon `ErrPeriodMismatch`.
7. **`overDelegated(...)`** — le poids cumulé validateur + délégations ne doit pas dépasser le plafond, sinon `ErrOverDelegated`.
8. Hors Primary Network, `validator.Priority.IsPermissionedValidator()` → `ErrDelegateToPermissionedValidator`.
9. `utxo.GetInputOutputs`, `CalculateFeeWithCredentials(sTx)` — depuis la transaction **signée** —, puis `FlowChecker.VerifySpendWithContext(fxCtx, …)` → `ErrFlowCheckFailed`.

## Effets d'état

- Inputs consommés, `Outs` et `StakeOuts` produits.
- Un `Staker` délégateur inscrit, atteignable par `GetCurrentDelegatorIterator(subnetID, nodeID)` ou `GetPendingDelegatorIterator(subnetID, nodeID)`.

⚠️ **Il n'existe pas d'accesseur qui rende un délégateur par son `txID`.** Déterminer si un délégateur donné est sorti de l'ensemble demande d'itérer sur les délégateurs de **son** validateur et de comparer les `TxID` — une itération bornée à un validateur, pas un balayage global.

## Ce qui est répercuté ailleurs

Rien vers une autre chaîne. Le versement passe par `rewardDelegatorTx`, voir [`22-reward.md`](22-reward.md).

La récompense d'une délégation atteint le propriétaire par `CreateOutput`, le **second site d'aiguillage** du design : l'extension est choisie sur le *type* du propriétaire de récompenses, des mois plus tard, sans personne pour autoriser quoi que ce soit. Le validateur prélève d'abord sa part de `DelegationShares` ; le reste est ce qui prend la forme `warpfx`.

## Symboles concernés

- `vms/platformvm/platform/add_permissionless_delegator_tx.go`
- `vms/platformvm/txs/executor/staker_tx_verification.go` — `verifyAddPermissionlessDelegatorTx`, `getDelegatorRules`, `overDelegated`
- `vms/platformvm/state/stakers.go` — `GetCurrentDelegatorIterator`, `GetPendingDelegatorIterator`
- `vms/platformvm/txs/executor/warp_delegator_test.go` — la récompense d'une délégation revient bien au propriétaire du délégateur
