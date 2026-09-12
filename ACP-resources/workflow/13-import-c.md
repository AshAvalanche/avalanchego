# `ImportTx` atomique C-Chain

## Ce qu'elle fait

Consomme des UTXOs déposés en shared memory par une autre chaîne du Primary Network, et crédite des soldes EVM.

⚠️ **La C-Chain n'est pas servie par un VM fixe.** Elle est enregistrée comme un VM de transition dont la fabrique pré-transition est **coreth** et la fabrique post-transition **saevm**, la bascule ayant lieu dix secondes avant l'activation de l'upgrade cible. Le type `warpfx` n'étant licite qu'à partir de cette activation, **le parcours décrit ici est celui de saevm** ; l'équivalent coreth (`graft/coreth/plugin/evm/atomic/`) n'est **pas modifié** et ne s'exécute jamais pendant que ce type est licite. Aucun UTXO `warpfx` ne peut lui parvenir : seule une P-Chain post-activation peut en produire, et à ce moment la C-Chain est déjà saevm.

## Variantes d'acteur

| Acteur | Possible ? |
| :--- | :--- |
| **EOA secp** | Oui, il signe la transaction atomique |
| **Contrat** | Oui, par la **forme canonique** : elle n'exige aucune signature et n'importe qui la soumet. Un contrat peut aussi recevoir une sortie sans rien faire — le crédit se fait sans exécution de code |
| **`WarpOwner`** | Forme canonique uniquement. Il n'y a pas de forme « autorisée » de ce côté : la vérification de quorum d'une autorisation est cantonnée à la P-Chain |

## Construction

`tx.Import{NetworkID, BlockchainID, SourceChain, ImportedInputs []*avax.TransferableInput, Outs []Output}`.

`tx.Output{Address common.Address, Amount uint64, AssetID ids.ID}` — ⚠️ **pas de `Nonce`**. `Compare` ne porte que sur `(Address, AssetID)`.

Les `Creds` sont parallèles aux **`ImportedInputs` seuls** : `len(Creds) == len(ImportedInputs)`, sans décalage, une transaction atomique n'ayant pas d'inputs EVM à l'import. Le type de credential est `secp256k1fx.Credential`.

⚠️ **Les inputs comme les sorties doivent être triés et uniques**, et `Outs` non vide.

## Autorisation

Une signature secp256k1 par input importé, vérifiée **par l'extension de fonctionnalité**. La forme canonique n'en présente aucune.

## Soumission

Par n'importe qui, dans le mempool atomique de la C-Chain (`vms/saevm/cchain/txpool`). Les transactions y sont contrôlées à l'entrée, puis à nouveau au moment de la construction du bloc.

## Vérification, dans l'ordre du code

⚠️ **Le modèle de vérification de saevm est « reconstruire et comparer ».** `VerifyBlock` rebâtit le bloc par le même chemin que le constructeur et compare les hachages. **Toute règle du constructeur est donc une règle de consensus** : un contrôle placé dans le hook de fin de bloc n'est pas une politique locale, il est aussi contraignant qu'un contrôle placé dans la vérification de la transaction.

`builder.PotentialEndOfBlockOps`, pour chaque transaction candidate :

1. **Conflit d'ascendance** — `ancestorInputIDs(building, settledHash, source)` remonte jusqu'au bloc réglé et écarte toute transaction dont les inputs recoupent ceux d'un ancêtre. C'est ce qui départage deux soumetteurs concurrents sur le même UTXO.
2. **`tx.SanityCheck(ctx)`** → `Import.sanityCheck` : réseau et chaîne, source ∈ {P-Chain, X-Chain}, inputs et sorties non vides, chaque input valide et **AVAX uniquement** (en entrée comme en sortie), montant de sortie non nul, **flow check** (`Consume` par input, `Produce` par sortie, `Verify`), puis inputs et sorties triés-uniques.
3. **`tx.VerifyCredentials(ctx, sharedMemory)`** → `Import.verifyCredentials`. Elle prend le `snow.Context`, dont la règle canonique a besoin pour comparer `Owner.SourceChainID` à `ctx.ChainID`, et rend la canonicité du lot. Deux appelants seulement : le txpool à l'admission, et `builder.PotentialEndOfBlockOps` à la construction.
   1. `len(ImportedInputs) != len(creds)` → `errIncorrectNumCredentials`.
   2. `sm.Get(SourceChain, utxoIDs)` — un seul appel groupé → `errFetchingUTXOs`.
   3. **Tous** les UTXOs sont désérialisés d'abord (`ParseUTXO`, codec atomique de saevm) → `errUnmarshallingUTXO`.
   4. **`isCanonicalImport(utxos)`** — si tout le lot est `warpfx`, `verifyCanonicalImport` est appelée **et la boucle par input est court-circuitée**. La fonction retourne alors `canonical = true` à son appelant.
   5. Sinon, par input : `utxo.Asset.ID != in.Asset.ID` → `errMismatchedAssetIDs` ; puis `fx.VerifyTransfer(fxTx, in.In, creds[j], utxo.Out)` → `errVerifyingTransfer`.
4. **Borne d'enchère** — si `canonical`, `VerifyCanonicalBid(op.GasFeeCap, building.BaseFee, minimumBid)` → `ErrBidTooHigh`, où `minimumBid = ScaleAVAX(1) / op.Gas`. Voir plus bas.
5. **`worstcase.State.Apply`**, côté SAE : refuse une opération dont `GasFeeCap` est **sous** le `baseFee` du bloc. C'est le plancher dont la borne d'enchère est le miroir.

⚠️ **`VerifyCredentials` rend la canonicité du lot à son appelant.** Déterminer qu'un lot est canonique demande de lire les UTXOs consommés, ce que la vérification des credentials fait déjà : recalculer l'information par une seconde lecture de shared memory serait du travail et une source de divergence.

⚠️ **L'extension de fonctionnalité est un `secp256k1fx` en dur** : la C-Chain ne tient **aucune table de Fx**. Porter celle de la P-Chain ici pour appeler une méthode qui n'aurait rien à faire serait du travail sans contrepartie — un UTXO `warpfx` qui atteint cette boucle est, par construction, un UTXO qui n'aurait pas dû l'atteindre, et le refus est la bonne réponse.

## Effets d'état

`Tx.AsOp` traduit la transaction en une opération SAE :

| Champ | Valeur |
| :--- | :--- |
| `Gas` | `intrinsicGas + 1 × taille + CostPerSignature × nbSignatures` |
| `GasFeeCap` | `gasPrice(burned, gas)` — **le brûlage est une enchère** |
| `Mint` | par sortie AVAX, `ScaleAVAX(out.Amount)` crédité à `out.Address` |

Les soldes non-AVAX sont crédités séparément par `TransferNonAVAX` (`AddBalanceMultiCoin`).

⚠️ **Le montant brûlé n'est pas un frais mais une offre.** `burned = Σ inputs − Σ sorties`, et `GasFeeCap = ScaleAVAX(burned) / gas`. Brûler trop peu ne rend pas la transaction invalide : elle attend simplement que le prix descende.

⚠️ **Le crédit se fait sans exécution de code** : ni `receive()` ni `fallback` du destinataire ne sont appelés, comme pour un `selfdestruct`. Un contrat destinataire n'est pas notifié : sa comptabilité doit fonctionner en mode *pull*.

## Ce qui est répercuté ailleurs

`AtomicRequests()` retourne `(SourceChain, &atomic.Requests{RemoveRequests: utxoIDs})` — **un import ne produit que des `RemoveRequests`, jamais de trait**. Appliqué à la shared memory à l'acceptation du bloc.

## Cas d'échec

| Échec | Ce qu'il laisse |
| :--- | :--- |
| Un UTXO déjà consommé | `sm.Get` échoue → toute la transaction est rejetée. Rien n'est perdu : on reconstruit avec les survivants |
| Deux soumetteurs sur le même UTXO | Le contrôle d'ascendance en écarte une ; l'autre passe |
| UTXO d'un type que le codec atomique ne connaît pas | `errUnmarshallingUTXO` → **UTXO inconsommable, fonds perdus**. C'est ce que la garde de destination à l'export prévient |
| Lot `warpfx` mal formé | Rejeté avant tout crédit. L'UTXO reste intact en shared memory |
| UTXO `warpfx` mêlé à un UTXO signé | Pas canonique → boucle ordinaire → refus par `secp256k1fx`. Échoue dans le bon sens |
| Enchère au-delà du plafond | `ErrBidTooHigh`, la transaction n'est jamais incluse |
| Enchère sous le `baseFee` | Refusée par `worstcase.State.Apply` ; la transaction attend que le prix descende |

## La forme canonique (règle miroir)

> Une `ImportTx` C-Chain consommant des UTXOs `warpfx` est valide si et seulement si **tous** appartiennent au **même** `WarpOwner`, qu'**aucun input signé** ne les accompagne, et qu'elle produit **exactement une** sortie créditant `SourceAddress`, dont l'enchère ne dépasse pas `k` fois le `baseFee` du bloc.

`verifyCanonicalImport` vérifie, une fois pour la transaction :

| Contrôle | Erreur |
| :--- | :--- |
| un credential par input, **vide** | `errCanonicalCredential` |
| tous les UTXOs du **même** `WarpOwner` | `errCanonicalMixedOwners` |
| `Owner.SourceChainID` == cette chaîne | `errCanonicalWrongSource` |
| `in.In.Amount() == out.Amt` | `errCanonicalAmount` |
| **exactement une** sortie | `errCanonicalOutputCount` |
| AVAX uniquement | `errNonAVAXOutput` |
| adresse créditée == `SourceAddress` | `errCanonicalWrongOutput` |

⚠️ **Le contrôle de montant est celui que l'extension de fonctionnalité aurait fait**, et que le court-circuit perdrait sinon.

⚠️ **Le credential vide est un `secp256k1fx.Credential` sans signature**, pas un `warpfx.Credential` : un credential `warpfx` n'a pas cours sur le chemin atomique, et le type secp est le seul encodage de « cet input ne présente rien » sur lequel les deux codecs s'accordent déjà. C'est aussi ce qui garde la transaction byte-identique pour deux soumetteurs qui la reconstruisent.

⚠️ **`Owner.SourceChainID` doit être cette chaîne.** Un UTXO possédé par une adresse d'une autre chaîne nommerait une adresse qui ne veut rien dire ici, et aucun solde EVM local n'y répond.

⚠️ **Le court-circuit lit la forme du lot entier**, jamais l'absence de signature. Clé sur « pas de signature présente », il rendrait **tout UTXO `warpfx` dépensable par n'importe qui**.

**Une remarque propre à ce sens** : un solde EVM est **additif**, crédité par `AddBalance`. Il n'y a donc aucune fragmentation possible et la contrainte de sortie unique ne coûte rien — contrairement au sens P→C, où chaque import produit un UTXO de plus.

### La borne est une borne d'enchère, pas une fourchette de frais

Côté P-Chain, le brûlage est un frais : la fourchette y a deux bornes, la haute étant le flow check majoré du frais et la basse la règle anti-destruction. **Ici la borne haute n'a pas d'objet** — rien ne majore, et une transaction qui brûle trop peu n'est pas invalide, elle n'est simplement jamais incluse. Ce qu'elle protégeait est déjà assuré par le plancher `GasFeeCap ≥ baseFee`.

⚠️ **Ce qui reste est plus dangereux, pas moins.** Là où le brûlage est un frais, tout brûler est de la destruction pure, sans bénéfice pour son auteur. Ici le montant brûlé **est** l'enchère : tout brûler maximise la priorité d'inclusion, aux frais du propriétaire. `VerifyCanonicalBid` refuse donc une enchère au-delà de `MaxFeeOverpaymentFactor × baseFee`.

⚠️ **Mais le plafond ne descend jamais sous l'enchère minimale exprimable, et c'est ce qui le rend applicable.** Le brûlage est **quantifié** — le plus petit qu'un import canonique puisse exprimer est 1 nAVAX — tandis que le `baseFee` ne l'est pas : sous ACP-283 le prix minimum du gas de cette chaîne démarre à 1 wei et y revient dès qu'elle est inactive. Un quantum étalé sur les ~10 300 de gas d'un import à un input vaut déjà ~97 000 aAVAX/gas, soit quatre à cinq ordres de grandeur au-dessus de `k × 1`. Sans plancher, **aucun import canonique n'est incluable au prix plancher de la chaîne** — c'est-à-dire précisément quand elle est la moins chère.

Le troisième paramètre de `VerifyCanonicalBid` est donc `ScaleAVAX(1) / op.Gas`, que le constructeur calcule depuis le gas de l'opération. Au plancher, un tiers peut brûler un quantum du propriétaire : c'est le minimum indivisible, la borne ne pouvait pas protéger en-deçà.

**Pourquoi dans le hook de fin de bloc.** C'est le seul endroit qui connaisse à la fois le type de la transaction et le `baseFee` du bloc en construction : ni `sanityCheck` ni `verifyCredentials` ne reçoivent le prix. Le chemin de l'état pire-cas, lui, est partagé avec les transactions EVM ordinaires, dont les auteurs peuvent légitimement surpayer. Et le modèle reconstruire-et-comparer en fait une règle de consensus, pas une politique de constructeur.

**Les deux bornes se répondent** : elles portent sur la même grandeur, `GasFeeCap`, contre la même référence, le `baseFee` du bloc. Une borne exprimée contre un frais absolu n'aurait pas cette propriété.

## Symboles concernés

- `vms/saevm/cchain/tx/import.go` — `Import`, `Output`, `sanityCheck`, `verifyCredentials`, `burned`, `asOp`, `atomicRequests`, `transferNonAVAX`
- `vms/saevm/cchain/tx/tx.go` — `Tx.SanityCheck`, `Tx.VerifyCredentials` (rend la canonicité), `Tx.AsOp`, `gasUsed`
- `vms/saevm/cchain/tx/warp_canonical.go` — `isCanonicalImport`, `verifyCanonicalImport`, `verifyWarpExportDestination`
- `vms/saevm/cchain/tx/codec.go` — alignement du `typeID` de `warpfx.TransferOutput` sur **44**
- `vms/saevm/cchain/hooks.go` — `builder.PotentialEndOfBlockOps`, `ancestorInputIDs`, calcul de l'enchère minimale
- `vms/saevm/cchain/dynamic/price.go` — `InitialPriceExponent`, le prix plancher du gas (ACP-283)
- `vms/saevm/cchain/txpool/txpool.go` — contrôle à l'entrée du mempool
- `vms/saevm/worstcase/state.go` — `Apply`, plancher d'enchère
- `vms/saevm/sae/blocks.go` — `VerifyBlock` → `rebuild` → comparaison de hachages
- `vms/warpfx/fee.go` — `VerifyCanonicalBid`, `MaxFeeOverpaymentFactor`, partagées avec la P-Chain
- `graft/coreth/plugin/evm/atomic/` — chemin pré-transition, **non modifié** : ni enregistrement au codec, ni garde d'activation
