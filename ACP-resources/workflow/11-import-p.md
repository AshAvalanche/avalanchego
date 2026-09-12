# `ImportTx` P-Chain

## Ce qu'elle fait

Consomme un ou plusieurs UTXOs déposés en shared memory par une autre chaîne du Primary Network, et les matérialise en UTXOs P-Chain.

## Variantes d'acteur

| Acteur | Possible ? |
| :--- | :--- |
| **EOA secp** | Oui. Il signe, et chaque input importé porte un `secp256k1fx.Credential` |
| **`WarpOwner`** | Deux formes. **Canonique** : personne ne l'autorise, n'importe qui la soumet. **Autorisée** : un `warpfx.Credential` porteur, pour tout ce qui dépasse une simple entrée de fonds |

⚠️ **Les deux formes ne se distinguent que par la présence ou l'absence d'un credential porteur.** Cela ne dépend pas de la façon dont l'UTXO est arrivé en shared memory : son origine est oubliée dès qu'il y est. Les deux se soumettent par le même `platform.issueTx`.

**Deux voies déposent ces UTXOs**, et l'import ne les distingue pas : l'export atomique signé par un EOA, qui peut désigner **une adresse tierce** et donc financer un contrat sans que celui-ci agisse, et le précompile `exportAVAX()`, par lequel un EOA ou un contrat déplace son **propre** solde. Voir [`10-export-c-vers-p.md`](10-export-c-vers-p.md).

## Construction

`ImportTx{BaseTx, SourceChain ids.ID, ImportedInputs []*avax.TransferableInput}`.

Les `Creds` sont parallèles à la concaténation **`Ins ‖ ImportedInputs`** : le slot d'indice `i` fait face à `Ins[i]` tant que `i < len(Ins)`, puis à `ImportedInputs[i - len(Ins)]`. C'est l'ordre que `utxo.GetInputOutputs` reproduit.

Inputs triés par `UTXOID`, sorties triées sur `(assetID, octets marshallés de la sortie)` — cette seconde règle demande de sérialiser chaque sortie avant de pouvoir ordonner.

## Autorisation

Une signature secp256k1 par input, locale comme importé.

## Soumission

Par l'auteur, via `platform.issueTx`.

## Vérification, dans l'ordre du code

`standardTxExecutor.ImportTx` :

1. **`e.tx.SyntacticVerify(e.backend.Ctx)`**.
2. **`avax.VerifyMemoFieldLength(tx.Memo, isDurangoActive)`** — le mémo doit être vide depuis Durango.
3. **`resolveAuthorization(e.tx, chainTime)`** → `fxCtx`. Placée **avant** la garde de bootstrap du point 5, parce que l'engagement qu'elle vérifie est l'authentification elle-même.
4. **Construction de `e.inputs` et de `utxoIDs`** — `in.UTXOID.InputID()` pour chaque `ImportedInputs`. Fait **inconditionnellement**, avant toute garde.
5. ⚠️ **Garde `e.backend.Bootstrapped.Get() && !e.backend.Config.PartialSyncPrimaryNetwork`.** Tout ce qui suit dans ce point est **sauté** quand la garde est fausse :
   - `verify.SameSubnet(ctx, tx.SourceChain)`
   - `e.backend.Ctx.SharedMemory.Get(tx.SourceChain, utxoIDs)` — **un seul appel groupé**
   - résolution des `Ins` locaux par `e.state.GetUTXO(input.InputID())` dans `utxos[0:len(tx.Ins)]`
   - désérialisation des octets de shared memory avec **`platform.Codec`** dans `utxos[len(tx.Ins):]`
   - `utxo.GetInputOutputs(tx)` → `(ins, outs, producedAVAX)`
   - `e.feeCalculator.CalculateFeeWithCredentials(e.tx)` puis `producedAVAX += fee` — depuis la transaction **signée**, un credential porteur étant invisible depuis la forme non signée
   - **si la forme est canonique** — `isCanonicalImport(fxCtx, tx, utxos)` — alors `verifyCanonicalImport(...)`, **et l'appel au Fx est court-circuité**
   - sinon `e.backend.FlowChecker.VerifySpendUTXOsWithContext(fxCtx, tx, utxos, ins, outs, e.tx.Creds, {AVAX: producedAVAX})`
6. **`avax.Consume(e.state, tx.Ins)`** puis **`avax.Produce(e.state, txID, tx.Outs)`** — inconditionnels.
7. **`e.atomicRequests = {SourceChain: {RemoveRequests: utxoIDs}}`** — inconditionnel.

⚠️ **Le point 5 est le seul endroit où les UTXOs importés sont lus et contrôlés, et il est sautable.** Les `RemoveRequests` sont émis dans tous les cas — le commentaire du code l'assume explicitement : « We apply atomic requests even if we are not verifying atomic requests to ensure the shared state will be correct if we later start verifying the requests. » L'émission d'une `ImportTx` pendant un `PartialSyncPrimaryNetwork` est bloquée en amont, à `manager.VerifyTx`, par `ErrImportTxWhilePartialSyncing`.

À l'intérieur de `VerifySpendUTXOs` (`vms/platformvm/utxo/verifier.go`), par input :

1. `len(ins) == len(creds)` et `len(ins) == len(utxos)`.
2. `cred.Verify()` pour tous les credentials, en boucle séparée et **avant** la boucle par input.
3. `now := uint64(h.clk.Time().Unix())` — ⚠️ **l'horloge locale du nœud**, pas le `chainTime`.
4. Par input : `utxo.AssetID() == input.AssetID()` ; dépliage éventuel de `stakeable.LockOut` / `stakeable.LockIn` ; puis **`h.fxs.VerifyTransfer(fxCtx, tx, in, creds[index], out)`** — l'extension est résolue sur `out`, la sortie **dépliée** ; le credential reste positionnel.

   ⚠️ **Un UTXO `warpfx` échoue ici, sur `warpfx.ErrNoAuthorization`.** La résolution le mène bien à `warpfx.Fx`, mais dépenser demande une autorisation de portée transaction que le triplet `(input, credential, utxo)` ne transporte pas, et l'`ImportTx` passe par le point d'entrée historique, qui transmet un `fxCtx` nul.
5. Comptabilité déverrouillé/verrouillé. ⚠️ Cette branche calcule sa **propre** clé de propriétaire, `hashing.ComputeHash256Array(platform.Codec.Marshal(owner))` — sans rapport avec le trait d'indexation, et de toute façon inatteignable pour `warpfx` : elle n'est empruntée que par les sorties enveloppées dans un `stakeable.LockOut`, que `verifyWarpOutputsNotLocked` interdit (voir [`00-acteurs.md`](00-acteurs.md)).

## La forme canonique

> Une **transaction canonique** est une transaction dont la forme est *dérivée* des UTXOs qu'elle consomme, et non *rédigée*. Personne ne la signe, personne n'émet de message pour elle, et n'importe qui la reconstruit à partir de ce qu'il lit en shared memory.

Une `ImportTx` est reconnue canonique par `isCanonicalImport` si, **et seulement si**, les trois conditions tiennent ensemble :

1. **aucun credential porteur** — lu sur `fxCtx.Authorization`, que `resolveAuthorization` a déjà rempli au point 3 : une seconde inspection de `Creds` serait une seconde source de vérité ;
2. **`Ins` vide** — elle ne consomme rien qui réside déjà sur la P-Chain, donc sa forme est entièrement déterminée par ce qu'elle lit en shared memory ;
3. **tous les UTXOs importés détenus par un `WarpOwner`** — aucune signature n'est attendue nulle part.

`verifyCanonicalImport` vérifie ensuite, **une fois pour la transaction entière** :

| Contrôle | Erreur |
| :--- | :--- |
| un credential vide par input, ni porteur ni secp | `ErrCanonicalImportCredential` |
| tous les UTXOs du **même** `WarpOwner` | `ErrCanonicalImportMixedOwners` |
| AVAX uniquement, en entrée comme en sortie | `ErrCanonicalImportAsset` |
| l'input est un `secp256k1fx.TransferInput` à `SigIndices` **vide** | `ErrCanonicalImportInput` |
| `in.Amount() == utxo.Amt` — ce que le Fx aurait fait | `ErrCanonicalImportAmount` |
| **exactement une** sortie | `ErrCanonicalImportOutputCount` |
| un `warpfx.TransferOutput` au **même** propriétaire | `ErrCanonicalImportWrongOutput` |
| montant dans la fourchette de frais | `warpfx.ErrInsufficientFee` / `warpfx.ErrExcessiveBurn` |

⚠️ **Le contrôle de la forme de l'input relève du même principe que celui du montant** : court-circuiter le Fx, c'est reprendre à sa charge ce qu'il faisait. Sans lui, le soumetteur choisirait le type d'input et produirait des transactions distinctes pour un même lot d'UTXOs — donc non déduplicables — là où la forme canonique est censée lui retirer ce choix.

Ce que ces contraintes laissent au soumetteur : soumettre ou non, quand, et le montant exact dans la fourchette. Il ne peut ni dévier de fonds, ni les fragmenter, ni les geler.

⚠️ **Le court-circuit du Fx appartient à cette branche et à elle seule.** Il n'est pas déclenché par « pas de credential porteur » en général, mais par la branche ayant déjà établi les trois conditions ci-dessus. Une `ImportTx` à `Ins` non vides, ou mêlant un input signé à des inputs `warpfx`, **n'est pas canonique** : elle retombe sur le chemin ordinaire, où l'UTXO `warpfx` atteint le Fx avec un contexte nul et se fait refuser. Échouer dans ce sens est tout l'intérêt — généralisé, le court-circuit rendrait **tout UTXO `warpfx` dépensable par n'importe qui**.

### La fourchette de frais

> ```
> Σ inputs − fee(bloc)   ≥   montant de la sortie   ≥   Σ inputs − k × fee(bloc)
> ```
>
> avec `k = warpfx.MaxFeeOverpaymentFactor = 2`.

La borne **haute** est le flow check lui-même : produire plus, c'est ne pas couvrir le frais. La borne **basse** n'existe nulle part ailleurs, et c'est elle qui justifie la fonction : les flow checkers ne testent qu'une inégalité et brûlent tout surplus **sans plafond**. Pour une transaction signée cette souplesse est sans risque — le propriétaire signe, et nul ne se vole soi-même. Une transaction canonique n'est signée par personne : sous une simple inégalité, n'importe qui pourrait consommer un UTXO de 1 000 AVAX, en restituer 1 nAVAX et brûler le reste, sans y gagner quoi que ce soit et **sans que cela lui coûte rien**, une transaction atomique n'ayant pas de payeur de gas.

Ce n'est pas une égalité parce que les frais bougent : sous les paramètres ACP-103 du mainnet, le prix du gas double — ou est divisé par deux — en une trentaine de secondes, et une égalité stricte ne survivrait pas à la seule latence de diffusion.

⚠️ **Le propriétaire perd la maîtrise du moment.** N'importe qui déclenche l'import quand il veut, et le frais est prélevé sur le montant importé : un tiers malveillant peut donc importer au pic tarifaire et brûler jusqu'à `k × fee` d'AVAX du propriétaire. C'est borné — un import par lot d'UTXOs, un frais de l'ordre de 10⁻⁵ AVAX — mais c'est un transfert de contrôle réel, et c'est **le seul degré de liberté que la forme canonique laisse à un tiers**. Qui tient à ce contrôle utilise l'`ImportTx` autorisée.

⚠️ **La forme canonique vit du mauvais côté de la garde de bootstrap.** Elle dépend de `Σ inputs`, donc de la shared memory, donc elle hérite du saut décrit au point 5. C'est cohérent avec le comportement existant du flow check, pas une régression.

**Alimenter un `WarpOwner` ne coûte donc aucune autorisation** : un EOA signe un export atomique le désignant comme destinataire, puis n'importe qui soumet l'import. Aucun message Warp, aucune agrégation BLS, aucun relayeur, aucune `expiry` à dimensionner. `ImportedInputs` étant une collection sans plafond, N exports vers le même propriétaire se regroupent en un seul import — de la consolidation gratuite au passage de la frontière.

## Effets d'état

- Les `Ins` locaux sont consommés dans l'état P-Chain.
- Les `Outs` sont produits, indexés par adresse via `avax.Produce` → `utxoState.PutUTXO`, qui n'indexe que si `utxo.Out` implémente `avax.Addressable`.
- Les UTXOs importés disparaissent de la shared memory à l'acceptation.

## Ce qui est répercuté ailleurs

`e.atomicRequests` est appliqué à la shared memory par `vms/platformvm/block/executor/acceptor.go` (`a.ctx.SharedMemory.Apply(blkState.atomicRequests, batch)`), **atomiquement avec le batch d'état**.

## Cas d'échec

| Échec | Ce qu'il laisse |
| :--- | :--- |
| Un UTXO déjà consommé | `SharedMemory.Get` échoue → **toute** la transaction est rejetée. Rien n'est perdu, on reconstruit avec les survivants |
| Flow check | Rien |
| Credential invalide | Rien |

## Symboles concernés

- `vms/platformvm/txs/executor/standard_tx_executor.go` — `standardTxExecutor.ImportTx`
- `vms/platformvm/platform/import_tx.go`
- `vms/platformvm/utxo/verifier.go` — `VerifySpendUTXOs`, `GetInputOutputs`
- `vms/platformvm/block/executor/acceptor.go`
- `vms/platformvm/block/executor/manager.go` — `VerifyTx`, `ErrImportTxWhilePartialSyncing`
- `vms/platformvm/txs/executor/canonical.go` — `isCanonicalImport`, `verifyCanonicalImport`
- `vms/platformvm/txs/fee/credential_complexity.go` — `CalculateFeeWithCredentials`, `SignedTxComplexity`
- `vms/warpfx/fee.go` — `MaxFeeOverpaymentFactor`, `VerifyFeeBand`
- `vms/platformvm/txs/executor/atomic_tx_executor.go` — chemin pré-AP5
