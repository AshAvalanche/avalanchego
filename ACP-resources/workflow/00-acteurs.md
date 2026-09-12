# Acteurs

## Les trois façons de posséder des AVAX sur la P-Chain

| Acteur | Identité | Comment il dépense |
| :--- | :--- | :--- |
| **EOA secp** | Une ou plusieurs clés secp256k1, adressées en Bech32 (`P-avax1…`) | Il signe les octets non signés de la transaction ; chaque input porte un `secp256k1fx.Credential` |
| **`WarpOwner`-EOA** | `warpfx.Owner{SourceChainID, SourceAddress}`, la paire `(C-Chain, adresse 20 octets de l'EOA)` | Un message Warp portant les octets de la transaction, dans un `warpfx.Credential` — voir [`30-autorisation.md`](30-autorisation.md) |
| **`WarpOwner`-contrat** | Idem, l'adresse portant du code | Idem |

⚠️ **Du point de vue de la P-Chain, les deux sont strictement identiques.** La paire `(chaîne source, adresse source)` ne dit pas si l'adresse porte du code, et le `SourceAddress` de l'`AddressedCall` est forcé à l'appelant du précompile Warp. La seule différence est en amont : qui construit les octets de la transaction.

**Recevoir ne demande aucune autorisation.** `warpfx.TransferOutput` est enregistré dans le codec des transactions P-Chain, dans celui de ses blocs et dans le codec atomique de saevm — décodable partout où il peut légitimement arriver : il peut figurer dans les `Outs` de n'importe quelle transaction, y compris une `BaseTx` signée en secp256k1. Les **sorties** ne passent jamais par le Fx, seuls les inputs le font.

### La garde d'activation

> Une transaction qui **mentionne** un type `warpfx` — sortie, sortie exportée, sortie de stake, propriétaire de récompenses, ou credential — est invalide tant que Helicon n'est pas activé.

L'enregistrement au codec est **inconditionnel**, comme celui de toutes les époques précédentes : le gating se fait à la vérification, jamais au décodage. Il serait tentant d'en conclure que recevoir des fonds ne demande aucune règle nouvelle. C'est vrai *après* l'activation et faux avant : un nœud exécutant un binaire antérieur ne connaît pas ces types et ne sait décoder ni la transaction, ni le bloc qui la contient. Une sortie créée avant l'activation ne serait pas un inconvénient, **ce serait un fork**.

⚠️ **La garde ne liste pas les endroits où un type `warpfx` peut se cacher : elle parcourt la transaction.** Une liste écrite à la main devrait être tenue à jour à chaque nouveau type de transaction, et une entrée oubliée ouvrirait précisément le trou qu'elle garde. Le parcours par réflexion est total par construction, et ne coûte que tant que l'upgrade est inactif — une fois activé, `VerifyWarpUTXOsActivated` retourne à sa première ligne.

| Volet | Point d'application |
| :--- | :--- |
| P-Chain | `StandardTx`, en tête, avant le `Visit` — `VerifyWarpUTXOsActivated`, puis `verifyWarpOutputsNotLocked` |
| saevm | **aucune, et il n'en faut pas** — voir ci-dessous |
| coreth | **aucune, et il n'en faut pas** — voir ci-dessous |

**Il n'existe qu'une garde d'activation dans tout le design, celle de la P-Chain.** Elle vit dans `StandardTx`, point de passage de toutes les transactions standard, transactions de staking comprises depuis Durango. Les transactions de proposition n'y passent pas et n'ont pas à le faire : `RewardValidatorTx` n'est pas construite par un utilisateur mais par le builder de blocs, à partir d'un staking déjà accepté, donc déjà passé par la garde.

⚠️ **saevm n'en a pas besoin, et ce n'est pas parce qu'il est le VM post-Helicon** — il est installé dix secondes avant. La garde vit un niveau au-dessus, dans `builder.BuildHeader`, qui refuse de poser un en-tête tant que l'upgrade n'est pas actif ; et le chemin de reconstruction qui sert à vérifier un bloc passe par le même code, avec le `now` fixé à l'horodatage du bloc examiné. **Le premier bloc saevm est donc nécessairement à ou après le timestamp d'activation.**

⚠️ **coreth n'en a pas besoin non plus, et pour une raison structurelle plutôt que temporelle.** `warpfx.TransferOutput` n'est enregistré ni dans son codec atomique ni ailleurs chez lui : `linearcodec.PackPrefix` refuse de marshaller un type non enregistré, donc coreth ne peut **ni construire ni parser** un export portant ce type. Dans l'autre sens, aucun UTXO `warpfx` ne peut lui parvenir par la shared memory — seule une P-Chain post-activation peut en produire, et à ce moment la C-Chain est déjà saevm.

> ⚠️ **Enregistrement et garde vont ensemble, ou aucun des deux.** Enregistrer `warpfx.TransferOutput` dans le codec de coreth « par alignement défensif » rendrait constructible exactement ce que la garde retirée interdisait. Le design choisit **aucun des deux** ; toucher l'un sans l'autre est une régression, pas une précaution.

⚠️ **`AtomicTx` n'est pas gardée**, et c'est délibéré : c'est le chemin pré-AP5, atteint uniquement par un `ApricotAtomicBlock`, que les règles de blocs rejettent depuis Banff. Helicon étant très postérieur, une transaction `warpfx` ne peut pas l'emprunter.

⚠️ **La C-Chain n'est pas servie par un VM fixe.** Elle est enregistrée comme un VM de transition dont la fabrique pré-transition est **coreth** et la fabrique post-transition **saevm**, la bascule ayant lieu **dix secondes avant** l'activation (`node/node.go`, `TransitionTime`). Le décalage est délibéré : coreth impose un temps de bloc minimal, et il faut lui garantir de pouvoir construire le bloc de transition avant l'activation.

**Les deux volets doivent s'activer au même timestamp.** Sans le volet C-Chain aucun flux entrant n'existe et la fonctionnalité est inerte ; sans la P-Chain une sortie atomique `WarpOwner` est illisible par la chaîne cible, ce qui est pire qu'inerte. Ce n'est pas le VM qui bascule au même instant que la P-Chain — c'est la **règle**.

⚠️ **Du point de vue de la P-Chain, un `WarpOwner`-EOA et un `WarpOwner`-contrat seront strictement identiques** : la paire `(chaîne source, adresse source)` ne dit pas si l'adresse porte du code. La seule différence est en amont, sur la C-Chain — qui appelle le précompile Warp et qui construit les octets de la transaction.

## Les rôles auxiliaires

**Le soumetteur.** Aujourd'hui, l'auteur d'une transaction est aussi celui qui la soumet et celui qui la signe : les trois rôles sont confondus dans l'EOA. Un tiers peut techniquement relayer des octets déjà signés, mais rien dans le protocole ne le prévoit ni ne l'encourage.

**Le livreur d'attestation.** N'existe pas : la P-Chain ne signe aujourd'hui, sur requête ACP-118, que les messages ACP-77 relatifs aux validateurs L1 (`SubnetToL1Conversion`, `RegisterL1Validator`, `L1ValidatorRegistration`, `L1ValidatorWeight`).

## Découverte des fonds

| Où | Accesseur | Clé |
| :--- | :--- | :--- |
| Index P-Chain | `state.State.UTXOIDs(addr []byte, start, limit)` → `avax.GetPaginatedUTXOs` | Adresse de 20 octets |
| Shared memory | `avax.GetAtomicUTXOs(sharedMemory, codec, chainID, addrs, …)` | Trait, typé `set.Set[ids.ShortID]` donc **20 octets** |
| API, propriétaire warp | `platform.getWarpOwnerUTXOs` | La paire `(sourceChainID, sourceAddress)`, au format `0x…` |
| API | `platform.getUTXOs`, args `api.GetUTXOsArgs` **partagés avec la X-Chain** | Liste d'adresses Bech32, au moins une exigée |

⚠️ **Un UTXO déposé en shared memory sans trait est introuvable.** `elem.Traits` n'est renseigné que si la sortie implémente `avax.Addressable` — quatre sites seulement font ce test, tous avec le même idiome `elem.Traits = out.Addresses()` :

- `vms/platformvm/txs/executor/standard_tx_executor.go` (`ExportTx`)
- `vms/avm/txs/executor/executor.go`
- `graft/coreth/plugin/evm/atomic/export_tx.go` (`AtomicOps`)
- `vms/saevm/cchain/tx/export.go`

L'UTXO est bien déposé et reste consommable si l'on connaît son `(txID, index)`, mais `sharedMemory.Indexed` ne le rendra jamais, donc `GetAtomicUTXOs` ne le verra pas. La perte est une perte de **découvrabilité**, pas de fonds.

`warpfx.TransferOutput` implémente `avax.Addressable` et renvoie `[][]byte{SourceAddress}` — **vingt octets bruts, sans hachage**. Le trait est donc de la même forme que celui d'une sortie secp, et toute la machinerie de découverte le lit sans modification : `avax.GetAtomicUTXOs` (`set.Set[ids.ShortID]`), `State.UTXOIDs(addr []byte, …)`, et `avax.ParseServiceAddress`.

⚠️ **Le trait n'est jamais une donnée de consensus.** `utxoState.updateChecksum` ne hache que l'`utxoID`, `chains/atomic` type ses `Traits [][]byte` sans contrainte de longueur, et la provenance lit toujours l'`Owner` **complet** depuis l'UTXO. Un trait est un indice de découverte, et rien d'autre — il n'a jamais eu à être injectif.

Conséquence : deux propriétaires ne différant que par leur `SourceChainID` partagent un trait. C'est sans objet dans ce périmètre — la C-Chain est forcée à l'export comme à l'import canonique — et si le périmètre s'élargit, la découverte rend les deux et l'appelant filtre en décodant l'UTXO.

### L'accesseur d'API

**`platform.getWarpOwnerUTXOs`** prend la paire `(sourceChainID, sourceAddress)` au format `0x…`, et un paramètre `atomicSourceChain` choisit entre l'index P-Chain et la shared memory. ⚠️ **C'est une commodité, pas une nécessité** : `platform.getUTXOs` sait servir un `WarpOwner` tel quel, l'adresse EVM s'encodant en Bech32 comme n'importe quels vingt octets. Ce que la méthode dédiée apporte est de ne pas faire passer une adresse EVM pour une adresse P-Chain dont l'utilisateur croirait avoir la clé.

## L'aiguillage des Fx

La PlatformVM tient une **collection** de Fx, `vm.fxs *fx.Fxs`, résolvable par le type concret d'une valeur qu'une extension a revendiquée. La collection est construite dans l'ordre, et **le premier ajouté est le défaut** :

```go
// vms/platformvm/vm.go
vm.fxs = fx.NewFxs(
    fx.Claim{ID: secp256k1fx.ID, Fx: &secp256k1fx.Fx{}},              // défaut
    fx.Claim{ID: warpfx.ID, Fx: &warpfx.Fx{}, Types: warpfx.Types()},
)
```

⚠️ **La revendication est une donnée, pas un effet de bord.** La X-Chain peuple sa table en *observant* les `RegisterType` que chaque Fx émet dans son `Initialize`, à travers un `codec.Registry` enveloppant — elle n'a pas le choix, sa liste de Fx venant de `chainParams.FxIDs` et son codec se construisant au même moment. La P-Chain n'a ni l'une ni l'autre de ces contraintes : sa liste est compilée en dur et ses types sont enregistrés dans `platform.Codec` directement. La table est donc remplie par le constructeur, depuis `Claim.Types`. `Initialize` reste appelé — c'est le cycle de vie du Fx — mais il ne porte plus l'aiguillage, et il n'y a plus d'ordre à respecter entre deux appels sous peine d'une table silencieusement vide.

⚠️ **`warpfx.Types()` est la seule liste**, lue à la fois par `Initialize` et par la revendication.

**Ce que chaque extension revendique**, par sa méthode `Initialize` :

| Extension | Types revendiqués |
| :--- | :--- |
| `secp256k1fx` | `TransferInput`, `MintOutput`, `TransferOutput`, `MintOperation`, `Credential` |
| `warpfx` | `Owner`, `TransferOutput`, `Credential` |

⚠️ **`secp256k1fx.OutputOwners` n'est revendiqué par personne** : il résout vers le défaut, qui se trouve être `secp256k1fx`. Le résultat est correct, mais par repli et non par revendication. Il en va de même de `stakeable.LockOut`, enregistré directement dans `txs/codec.go` — et que le vérifieur déplie de toute façon avant de résoudre.

### Où la résolution a lieu

**Sur le type de la sortie consommée, jamais sur celui de l'input ni du credential.** C'est la sémantique en vigueur : l'UTXO porte sa condition de dépense, l'input ne fait que la référencer.

| Site | Résolu sur | Quand |
| :--- | :--- | :--- |
| `vms/platformvm/utxo/verifier.go`, boucle par input | la sortie consommée, **dépliée** de son `stakeable.LockOut` éventuel | à la vérification |
| `proposal_tx_executor.go:newUTXO` | le **propriétaire des récompenses** (`fx.Owner`) | des mois plus tard, à la sortie de l'ensemble |

Le credential reste choisi positionnellement (`creds[index]`).

### Les deux familles de points d'entrée

`utxo.Verifier` en expose quatre, par paires :

| Historique | Avec contexte |
| :--- | :--- |
| `VerifySpend` | `VerifySpendWithContext` |
| `VerifySpendUTXOs` | `VerifySpendUTXOsWithContext` |

Les historiques délèguent aux secondes avec un `fxCtx` **nul**. Une extension qui implémente `fx.ContextualFx` est appelée par `VerifyTransferWithContext`, les autres par `VerifyTransfer` — inchangées.

> **C'est le mécanisme d'application, pas une commodité.** Une transaction qui n'a pas résolu d'autorisation passe par les points d'entrée historiques, donc transmet un contexte nul, et toute sortie dont l'extension en exige un échoue. **Oublier de router une transaction vers un point d'entrée contextuel peut donc fermer une porte, jamais en ouvrir une.**

**Sept exécuteurs appellent les points d'entrée contextuels** : `ImportTx`, `ExportTx`, `BaseTx`, `AddPermissionlessValidatorTx`, `AddPermissionlessDelegatorTx`, `AddAutoRenewedValidatorTx`, `SetAutoRenewedValidatorConfigTx`. Tous les autres passent par les historiques et reçoivent donc un `fxCtx` nul.

### Ce que `warpfx.Fx` répond

- `VerifyTransfer`, sans contexte : **toujours** `ErrNoAuthorization`.
- `VerifyTransferWithContext` : la **provenance**, détaillée dans [`30-autorisation.md`](30-autorisation.md).
- `VerifyPermission`, sans contexte : toujours `ErrPermissionUnsupported`. C'est le point d'entrée qu'emprunte `verifySubnetAuthorization`, et c'est ce qui interdit structurellement qu'un `WarpOwner` soit propriétaire de subnet.
- `VerifyPermissionWithContext` : la même provenance, contre le groupe de contrôle. Un seul appelant, `SetAutoRenewedValidatorConfigTx`, pour prouver l'assentiment du `ValidatorAuthority` d'un validateur auto-renouvelé — la seule façon d'en récupérer le stake. ⚠️ **C'est le point d'entrée qui distingue les deux cas, pas le type du propriétaire.**
- `CreateOutput` : matérialise une récompense en `warpfx.TransferOutput`.

⚠️ **`warpfx.Fx` est une structure sans champ.** C'est ce qui rend structurellement impossible qu'une autorisation survive d'une vérification à la suivante.

⚠️ Le `Backend` de l'exécuteur porte **les deux** : `Fx` (le défaut, pour l'autorisation de subnet, secp-only par construction) et `Fxs` (la collection). Un `Fxs` nul fait échouer `newUTXO` sur `ErrNoFxForRewardsOwner` plutôt que de se rabattre silencieusement.

## Ce qui est sauté pendant le bootstrap

⚠️ Le bootstrap ne saute pas les mêmes choses selon la transaction. C'est une asymétrie du code existant, à connaître avant d'y ajouter quoi que ce soit.

| Chemin | Garde | Ce qui est sauté |
| :--- | :--- | :--- |
| `secp256k1fx.Fx.VerifyCredentials` | `!fx.bootstrapped` | La **vérification de signature** seule ; les tests de seuil et de cardinalité restent |
| `ImportTx` P-Chain | `Bootstrapped.Get() && !PartialSyncPrimaryNetwork` | Lecture de shared memory, résolution des UTXOs **et flow check** |
| `ExportTx` P-Chain | `Bootstrapped.Get()` | `verify.SameSubnet` seul ; le flow check tourne toujours |
| `BaseTx` P-Chain | aucune | Rien |
| Staking P-Chain | `!backend.Bootstrapped.Get() → return nil` | **Toute** la vérification, flow check compris |
| `ImportTx` coreth | `!backend.Bootstrapped → return nil` | Lecture de shared memory et appel au Fx ; **le flow check tourne avant la garde**. Chemin pré-transition ; saevm ne garde pas ainsi |
| Vérification des messages Warp | `txExecutorBackend.Bootstrapped.Get()` | Le quorum BLS, et en plus mise en cache par `pChainHeight` (`blockState.verifiedHeights`) |

## Les types warp

`vms/warpfx/` porte trois types sérialisables et deux qui ne le sont pas.

| Type | Rôle | `typeID` |
| :--- | :--- | ---: |
| `Owner{SourceChainID ids.ID, SourceAddress []byte}` | Identité. `Verify()` exige **exactement 20 octets** | 43 |
| `TransferOutput{Amt uint64, Owner}` | L'UTXO. **Pas de `Locktime`** | 44 |
| `Credential{WarpMessage []byte}` | Porte le message d'autorisation | 45 |
| `Authorization{SourceChainID, SourceAddress}` | Message vérifié, réduit à ce que la provenance demande. **Jamais sérialisé, jamais persisté** | — |
| `Fx` | L'extension elle-même | — |

**Aucun type d'input n'est créé** : `secp256k1fx.TransferInput` à `SigIndices` vide est réutilisé — `Input.Verify()` n'exige qu'une slice triée-unique, ce qu'une slice vide satisfait, et `TransferInput.Verify()` n'ajoute que `Amt != 0`. Le type est déjà au même index dans les deux codecs.

Un input `warpfx` **coexiste** avec des inputs `secp256k1` dans une même transaction : les deux credentials s'engagent sur les mêmes octets — `tx.Unsigned.Bytes()` —, donc aucun n'invalide l'autre, et `Creds` reste parallèle à `Ins` (le tri par `UTXOID` décide seul quel slot porte une signature et lequel porte une autorisation). Tous les inputs `warpfx` doivent en revanche appartenir au **même propriétaire** : il n'y a qu'un porteur, et la provenance est vérifiée contre *chaque* UTXO consommé. Aucune règle n'énonce « un propriétaire par transaction » ; ça en découle.

> ✅ Les trois points sont vérifiés de bout en bout par `tests/e2e/p/warp_utxos_mixed.go` — le seul test unitaire à inputs mixtes, `TestNonCanonicalImportRefusesWarpUTXOs`, est celui qui doit **échouer**, donc rien n'y montrait la coexistence réussie.

⚠️ **Pourquoi `SourceAddress` fait exactement 20 octets.** Le champ est typé `[]byte` pour rester agnostique au format d'adresse, mais un propriétaire doit pouvoir **recevoir** ses fonds au retour, et l'`ImportTx` C-Chain crédite un `tx.Output.Address` de 20 octets (un `common.Address`). Une sortie dont le propriétaire porterait 32 octets serait parfaitement constructible et **définitivement non importable**.

### L'alignement des codecs

Les trois types sont enregistrés **inconditionnellement**, aux positions 43, 44 et 45, par `platform.RegisterWarpUTXOsTypes`, appelée une fois dans l'`init()` de `vms/platformvm/platform/codec.go`. Blocs et transactions partagent ce flux linéaire unique — les types de blocs occupent les trous que les types de transaction réservent — donc il n'y a pas deux côtés à tenir alignés.

Côté C-Chain, **`warpfx.TransferOutput` seul** est enregistré dans le codec atomique et doit y tomber sur **44**. `Owner` n'y apparaît qu'embarqué, et un credential `warpfx` n'a pas cours sur le chemin atomique.

⚠️ **Le décalage à écrire dépend du VM, et un seul des deux est écrit.** Le codec de saevm (`vms/saevm/cchain/tx/codec.go`) s'arrête à 9 et demande donc `SkipRegistrations(34)`. Celui de coreth (`graft/coreth/plugin/evm/atomic/codec.go`) s'arrête à 11 et demanderait `SkipRegistrations(32)` — il enregistre `secp256k1fx.Input` et `OutputOwners`, que saevm n'enregistre pas. **Un test d'alignement écrit contre l'un ne dit rien de l'autre**, et c'est utile à savoir en lisant du code existant ; mais **coreth n'est pas modifié**, et son absence d'enregistrement est ce qui rend sa garde d'activation inutile (voir *La garde d'activation*).

⚠️ Cet alignement est un invariant de consensus qu'aucune signature de fonction ne protège, et que **toute transaction ajoutée à la PlatformVM avant ces enregistrements décale**. Deux tests le tiennent : `TestWarpUTXOsCodecTypeIDs` épingle les trois positions, et `TestCodecAlignedWithPlatformVM` (saevm) sérialise le même UTXO avec les deux codecs et compare les octets — pour un UTXO `warpfx` **et** pour un `secp256k1fx`.

⚠️ **Ce second test comble un trou qui existait depuis toujours.** Avant lui, aucun fichier sous `vms/saevm/` n'importait le paquet de la PlatformVM : la garantie d'alignement reposait sur un commentaire dans `vms/saevm/cchain/tx/codec.go` et sur des tests de compatibilité binaire avec **coreth**, qui ne disent rien de la P-Chain.

### Pas d'`ownerID`, et pas de codec local

`Owner` ne porte **pas** de méthode `ID()`, et le paquet n'a **pas** de codec local. Un hash de l'`Owner` aurait dû être identique chez trois lecteurs qui ne partagent aucun codec — P-Chain, saevm, outillage —, ce qui n'est vrai que tant qu'`Owner` ne contient aucun champ typé par une interface : une précondition invisible dans la structure, qu'il aurait fallu verrouiller par un test réflexif. Il aurait aussi fallu décider quoi faire de l'erreur de `Marshal` dans une méthode qui ne peut pas la rendre, et un contrat Solidity aurait dû reproduire la sérialisation d'un `linearcodec` Go pour vérifier qu'une attestation le concerne.

Rien de tout cela n'est nécessaire : le trait est `SourceAddress`, et les attestations nomment le propriétaire par sa paire.

⚠️ **À ne pas confondre avec la contrainte d'alignement ci-dessus**, qui est réelle et sans rapport : elle porte sur `TransferOutput`, stocké dans un champ d'interface (`avax.TransferableOutput.Out`) et qui reçoit donc bien un identifiant de type.

## Symboles concernés

- `vms/warpfx/` — `owner.go`, `transfer_output.go`, `credential.go`, `authorization.go`, `fee.go`, `fx.go`, `factory.go`, `vm.go`
- `vms/platformvm/platform/codec.go` — `RegisterWarpUTXOsTypes`
- `vms/saevm/cchain/tx/codec.go` — `SkipRegistrations(34)`, le seul codec atomique modifié
- `vms/platformvm/vm.go`
- `vms/platformvm/fx/fx.go` — `Fx`, `Owner`, `Owned`, `Claim`, `Context`, `ContextualFx`
- `vms/platformvm/fx/fxs.go` — `Fxs`, `Add`, `Get`, `Default`, `VerifyTransfer`
- `vms/platformvm/utxo/verifier.go`
- `vms/platformvm/txs/executor/backend.go` — `Fx`, `Fxs`
- `vms/platformvm/txs/executor/proposal_tx_executor.go` — `newUTXO`
- `vms/secp256k1fx/fx.go`, `input.go`, `transfer_input.go`, `transfer_output.go`, `output_owners.go`
- `vms/components/avax/state.go` (`Addressable`), `atomic_utxos.go`, `utxo_state.go` — **aucun des trois n'est modifié**
- `vms/avm/vm.go`, `vms/avm/txs/codec.go`, `vms/avm/txs/parser.go`, `vms/avm/txs/executor/semantic_verifier.go`
- `tests/e2e/p/warp_utxos_mixed.go` — `newMixedBaseTx`, `authorizeAs`, `importOne` : la coexistence des credentials, et le refus de deux propriétaires
- `vms/saevm/cchain/precompile/nativeexport/` — le précompile `exportAVAX()`, par lequel un contrat déplace son propre solde
- `tests/e2e/p/warp_staking_family.go` — la délégation et le validateur auto-renouvelé, autorisés
