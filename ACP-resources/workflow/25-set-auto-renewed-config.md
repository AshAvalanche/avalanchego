# `SetAutoRenewedValidatorConfigTx`

## Ce qu'elle fait

Change la configuration d'un validateur auto-renouvelé pour le cycle suivant — la part restakée, et la durée. **`Period = 0` l'arrête** à la fin du cycle en cours et libère les fonds.

> C'est **la seule porte de sortie** d'un validateur auto-renouvelé. Sans elle, le stake ne revient jamais de lui-même.

## Variantes d'acteur

| Acteur | Possible ? |
| :--- | :--- |
| **EOA secp** | Oui, s'il est le `ValidatorAuthority` |
| **`WarpOwner`** | Oui, sous forme autorisée |

⚠️ **C'est la seule transaction du périmètre qui demande deux choses à la fois** : dépenser (les frais) *et* prouver l'assentiment d'un propriétaire. Un seul message Warp autorise les deux, puisqu'il s'engage sur les octets de la transaction entière.

## Construction

`SetAutoRenewedValidatorConfigTx{BaseTx, TxID ids.ID, Auth verify.Verifiable, AutoCompoundRewardShares uint32, Period uint64}`.

- `TxID` nomme l'`AddAutoRenewedValidatorTx` qui a créé le validateur.
- `Auth` est la preuve d'assentiment du `ValidatorAuthority`. Pour un `WarpOwner`, c'est un `secp256k1fx.Input` à `SigIndices` **vide** — aucun type d'auth nouveau n'est créé, exactement comme aucun type d'input ne l'est pour les UTXOs.

## Autorisation

⚠️ **`verifyAuthorization` prend le *dernier* credential de `tx.Creds`** comme credential d'autorisation, et rend les autres pour le reste de la transaction (`subnet_tx_verification.go`). Le porteur du message Warp, lui, est trouvé par **balayage** : il peut être ce dernier slot comme n'importe quel autre.

## Vérification, dans l'ordre du code

`verifySetAutoRenewedValidatorConfigTx` (`staker_tx_verification.go`) :

1. **`sTx.SyntacticVerify`** — dont `Auth.Verify()`.
2. **`resolveAuthorization(sTx, chainTime)`** → `fxCtx`, **avant** la garde suivante.
3. ⚠️ **`if !backend.Bootstrapped.Get() { return nil }`** (l.1000).
4. **`GetTx(tx.TxID)`** → l'`AddAutoRenewedValidatorTx`, dont on tire `ValidatorAuthority`.
5. **`verifyAuthorization(fx, sTx, ValidatorAuthority, tx.Auth)`** (l.1017) → `fx.VerifyPermissionWithContext(fxCtx, …)` pour un `*warpfx.Owner`, `fx.VerifyPermission` sinon.
6. `AutoCompoundRewardShares ≤ PercentDenominator`.
7. **`FlowChecker.VerifySpendWithContext(fxCtx, …)`** sur les credentials restants.

> ⚠️ **C'est le point d'entrée qui décide, pas le type du propriétaire.** `verifySubnetAuthorization` appelle le point d'entrée **sans** contexte, donc un `WarpOwner` propriétaire de subnet y tombe toujours sur `ErrPermissionUnsupported`. Ici l'appelant transmet le contexte, donc la provenance est vérifiable. Router le chemin subnet vers la variante contextuelle rouvrirait le subnet ingérable.

## Effets d'état

- Inputs consommés, `Outs` produits.
- `stakingInfo.NextPeriod` et la part auto-composée sont mis à jour. **Rien ne prend effet avant la fin du cycle en cours.**

## Cas d'échec

| Échec | Ce qu'il laisse |
| :--- | :--- |
| Autorisation absente ou d'un autre propriétaire | Rien ; le validateur continue son cycle |
| `Period = 0` accepté | Le validateur sortira à la fin du cycle en cours, et **pas avant** |

⚠️ **Une autorisation ne peut pas suivre un changement du jeu de validateurs.** Le quorum est vérifié à une hauteur P-Chain qui n'avance qu'à l'acceptation d'un bloc, alors que l'agrégateur signe contre le jeu courant : juste après l'`AddAutoRenewedValidatorTx` qui l'a créé, une configuration de ce validateur est refusée sur un jeu canonique décalé, et produire des blocs exprès ne suffit pas à faire converger. Un relayeur doit donc laisser la hauteur rattraper avant d'enchaîner.

## Symboles concernés

- `vms/platformvm/platform/set_auto_renewed_validator_config_tx.go`
- `vms/platformvm/txs/executor/staker_tx_verification.go` — `verifySetAutoRenewedValidatorConfigTx`, `verifyAuthorization`
- `vms/platformvm/txs/executor/subnet_tx_verification.go` — `verifyAuthorization` (l.82)
- `vms/warpfx/fx.go` — `VerifyPermissionWithContext`
