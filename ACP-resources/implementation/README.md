# ACP Warp UTXOs — plan d'implémentation (P-Chain + saevm)

> Ce fichier est **le plan**, mot pour mot. Chaque lot renvoie vers son guide
> détaillé dans ce même dossier : le plan dit *quoi*, les guides disent *comment*
> et *pourquoi*, et signalent où se méfier.
>
> **Par où commencer** : lis ce README en entier une fois — c'est la carte — puis
> attaque le [guide du lot 1](01-warpfx-types.md). Les guides se lisent dans
> l'ordre d'exécution donné en fin de page, pas dans l'ordre des numéros.

## Comment lire ce dossier

**Ce sont des guides à suivre à la main, pas du code à copier.** Les extraits Go
sont là pour lever une ambiguïté de forme, jamais pour être collés tels quels :
ils n'ont pas été compilés, et les signatures exactes se découvrent au premier
build.

Chaque guide suit la même trame — *Pourquoi ce lot existe*, l'ordre d'écriture
recommandé, une section par fichier (**Où** / **Quoi** / **Pourquoi** /
**Vérifier**), puis *Ce qui reste incertain*. Cette dernière section est celle
qu'on lit en premier quand on reprend le travail après une pause.

**Les trois marqueurs d'incertitude**, employés partout et dans ce sens
exactement :

| | Sens | Ce que ça demande |
| :--- | :--- | :--- |
| 🔴 | **Une décision de design est ouverte.** Écrire du code avant de trancher, c'est risquer de le jeter — ou pire, de figer une règle de consensus par défaut. | Trancher, puis reporter dans l'ACP |
| 🟠 | **Une affirmation déduite de la lecture du code, non exécutée.** Le raisonnement est écrit pour être vérifiable, mais il ne l'a pas été. | Confirmer au premier build |
| 🟡 | **Un calibrage à mesurer.** Aucun de ces chiffres n'est déductible d'une lecture. | Bencher avant de figer |

Et dans le corps du texte, ⚠️ signale un piège : un comportement qui surprend à
la lecture du code, ou une invariante silencieuse que rien ne protège.

### Le jalon de PoC

→ **[`POC-c-p.md`](POC-c-p.md)** — confirmer la base avant d'écrire le reste.

Un aller-retour C↔P sur cinq nœuds locaux, qui prouve les trois choses qu'aucun
test d'un seul VM ne peut prouver : l'alignement des codecs entre saevm et la
PlatformVM, le trait de découverte, et la bascule de VM de la C-Chain. Son
intérêt est autant ce qu'il **n'exige pas** — sa phase A se passe du lot 3, le
refactor d'aiguillage — que ce qu'il valide. Il porte aussi les changements de
déploiement (`network-id` personnalisé, genesis, fichier d'upgrade).

### Les douze guides

| Lot | Guide | Ce qu'il pose | Décisions ouvertes |
| ---: | :--- | :--- | :--- |
| 1 | [`01-warpfx-types.md`](01-warpfx-types.md) | Le paquet `vms/warpfx` : types, Fx, fabrique | 🔴 `k` |
| 2 | [`02-codecs.md`](02-codecs.md) | Positions 43/44/45, transactions **et** blocs | — |
| 3 | [`03-aiguillage-fx.md`](03-aiguillage-fx.md) | La collection de Fx — **le refactor structurel** | 🔴 surface de `Verifier` |
| 4 | [`04-autorisation.md`](04-autorisation.md) | Payload, quorum, engagement, expiry, liste autorisée | 🔴 `typeID`, `expiry` |
| 5 | [`05-import-canonique.md`](05-import-canonique.md) | L'`ImportTx` P-Chain sans signature, et sa fourchette de frais | 🔴 `k`, `Ins` non vides |
| 6 | [`06-gardes.md`](06-gardes.md) | Destination d'export, garde d'activation, verrou `stakeable` | 🔴 upgrade cible |
| 7 | [`07-attestations.md`](07-attestations.md) | `TxExecuted`, `StakeSettled`, `CycleSettled`, dérivés de l'état accepté | 🔴 `typeID` |
| 8 | [`08-decouverte.md`](08-decouverte.md) | API de confort, et le `fxID` d'affichage | 🔴 utilité de l'API dédiée |
| 9 | [`09-tarification.md`](09-tarification.md) | Tarifer les types `warpfx` **et** le credential porteur | — |
| 10 | [`10-saevm.md`](10-saevm.md) | Codec, destination, règle miroir, borne d'enchère | 🔴 `k` côté saevm |
| 11 | [`11-precompile.md`](11-precompile.md) | `exportAVAX()`, pour qu'un contrat déplace son propre solde | 🟡 le calibrage du gas |
| 12 | [`12-tests.md`](12-tests.md) | Les invariants que rien d'autre ne protège | — |

**Le dossier voisin.** [`../workflow/`](../workflow/) décrit le **parcours** de
chaque transaction — ce qui est vérifié, dans quel ordre, et ce que chaque échec
laisse derrière lui. Il répond à « que se passe-t-il ? », là où ce dossier-ci
répond à « qu'est-ce que j'écris ? ». Les deux doivent rester d'accord : une
divergence est un bug de l'un des deux.

## Contexte

L'ACP permet à une **adresse C-Chain** (contrat ou EOA) de détenir des AVAX sur
la P-Chain et d'y staker, via un nouveau type de propriétaire d'UTXO authentifié
par un **message Warp portant les octets de la transaction non signée** au lieu
d'une signature secp256k1.

### Les sources, et laquelle fait foi

| Source | Génération | Statut |
| :--- | :--- | :--- |
| `ACP-resources/ACP-Long.md` | G2 | **Référence normative** |
| `ACP-resources/ACPxxx-saevm.md` | G2 | Complément C-Chain — acte l'abandon de coreth |
| `ACP-resources/workflow/*.md` (12 fichiers) | G2 | Parcours par transaction, à jour |
| `ACP-resources/acp-workspace/plans/` + `BACKLOG.md` | **G1** | **Obsolète — ne pas s'en servir** |
| `ACPxx.md`, `ACPxx-en.md`, `OldACP.md`, `warp-utxos-contracts/` | G1 | Obsolète |

G1 reposait sur des **intentions** décrites par le message (4 payloads
d'intention, registre de nonces `minNonce`, plafond `maxFee`, invariants de
conservation I1–I3, *intent matching* par type de transaction, `Locktime` dans
la sortie). G2 fait porter au message **les octets exacts de la transaction**,
ce qui supprime tout cela d'un coup.

### Pourquoi ce plan abandonne coreth

`node/node.go:1248` enregistre la C-Chain comme un `transitionvm` :

```go
TransitionTime: n.Config.UpgradeConfig.HeliconTime.Add(-10 * time.Second),
```

L'ACP vise Helicon, qui est précisément l'upgrade où coreth passe la main à
`vms/saevm/cchain`. Les règles écrites pour coreth portent donc sur un VM qui ne
tourne jamais pendant que `warpfx` est licite.

**Ne rien toucher du tout dans coreth est sûr, et rend même sa garde
d'activation inutile.** `linearcodec.PackPrefix` refuse de marshaller un type
non enregistré (`codec/linearcodec/codec.go:90`). Sans enregistrement de
`warpfx.TransferOutput` dans `graft/coreth/plugin/evm/atomic/codec.go`, coreth
ne peut ni construire ni parser un export portant ce type : le rejet est
structurel plutôt que conditionnel. Symétriquement, aucun UTXO `warpfx` ne peut
lui parvenir par la shared memory — seule une P-Chain post-Helicon peut en
produire, et à ce moment la C-Chain est déjà saevm.

> **Conséquence à reporter dans l'ACP** : la garde d'activation de coreth
> disparaît. Il ne reste **qu'une** garde d'activation, celle de la P-Chain.

**Coreth n'est pas modifié, lot 11 compris.** L'analyse tenait pour inévitable
d'élargir `contract.StateDB` avec `SubBalance` ; le `*state.StateDB` concret
l'expose déjà, seule l'interface est plus étroite, et une assertion de type
suffit. Le *framework* de précompiles vit sous `graft/coreth/precompile/`
(`contract/`, `modules/`, `precompileconfig/`) et **saevm le réutilise
intégralement** : `vms/saevm/cchain/genesis.go:25` importe
`graft/coreth/precompile/contracts/warp`. Or `contract.StateDB`
(`graft/coreth/precompile/contract/interfaces.go:29-56`) expose `AddBalance`,
`GetBalance` et `SubBalanceMultiCoin` mais **pas `SubBalance`**, que l'état
possède pourtant. Sans elle, aucun précompile ne peut débiter (lot 11). C'est
une ligne dans une interface partagée, exécutée sous saevm — pas une règle du VM
coreth. Tout le reste du lot 11 vit sous `vms/saevm/`.

### État

Branche `acp-warp-owner`. **Les lots 1 à 10 et 12 sont écrits, et le PoC tourne
en phases A et B.** Le lot 11 reste différé.

| Lot | État |
| ---: | :--- |
| 1 | ✅ `vms/warpfx/` — types, Fx, fabrique |
| 2 | ✅ 43/44/45, transactions **et** blocs, épinglés par test |
| 3 | ✅ `fx.Fxs`, points d'entrée contextuels, mock regénéré |
| 4 | ✅ payload 4, quorum, engagement, expiry, liste des sept |
| 5 | ✅ forme canonique complète, fourchette de frais |
| 6 | ✅ destination d'export, activation, verrou `stakeable` |
| 7 | ✅ payloads 5/6/7 et leurs trois règles de signature |
| 8 | ✅ `platform.getWarpOwnerUTXOs`, `fxID` d'affichage |
| 9 | ✅ types `warpfx` tarifés, credential porteur, capacité de bloc |
| 10 | ✅ codec aligné, destination, règle miroir, borne d'enchère **planchée** |
| 11 | ✅ précompile `exportAVAX()`, activé au premier bloc Helicon |
| 12 | ✅ les tests, plus **six** specs e2e (EOA, contrat, mixte, précompile ×2, staking) |

Le paquet suit l'idiome Fx du dépôt : `factory.go` implémente `vms/fx.Factory` et
déclare `ID = ids.ID{'w','a','r','p','f','x'}`, comme `secp256k1fx`, `nftfx` et
`propertyfx` ; `vm.go` reprend l'interface `VM` de `secp256k1fx`.

Une implémentation antérieure existe sur la branche locale `acp-warpfx`
(29 commits, 87 fichiers, coreth inclus) ; elle **n'a pas été consultée**. Le
plan, `ACP-Long.md`, `ACPxxx-saevm.md` et `ACP-resources/workflow/` ont été la
seule source.

### Ce qui reste

**À reporter dans l'ACP** — cinq décisions que le code a tranchées et que le
texte ne dit pas :

| Quoi | D'où |
| :--- | :--- |
| **Le plancher de la borne d'enchère** | [10d](10-saevm.md). La règle sans lui est *insatisfiable* au prix plancher de la chaîne |
| `ErrWarpOutputNotLockable` | [6.3](06-gardes.md), absent de l'ACP, atteignable par un tiers |
| `ErrWarpOwnerCannotOwnSubnet` | [9.1](09-tarification.md) |
| La garde d'activation de coreth disparaît | [6.4](06-gardes.md), désormais **vérifié** |
| La forme de l'input dans l'import canonique | [5.2](05-import-canonique.md) |

**Décisions ouvertes** : les `typeID` des payloads (4, 5, 6, 7), `k`, et
l'upgrade cible.

**Mesures bloquantes** : le forfait `intrinsicWarpDBReads` **à froid** pour un
ensemble de plus de 1 000 signataires, et la latence de
`GetCanonicalValidatorSetFromChainID` à la même échelle ([lot 9](09-tarification.md)).

**Dette** : aucune. Les `BUILD.bazel` sont régénérés (`bazelisk run //:gazelle`),
`//:gazelle_test` passe.

> Ni nix ni `task` ne sont nécessaires pour ça : `nix_run.sh` exec directement
> une commande qu'il trouve sur le PATH, et `run_task.sh` bootstrappe `task` via
> `go tool`. Le seul prérequis est **bazelisk**, qui s'installe par
> `go install github.com/bazelbuild/bazelisk@latest`.
>
> ⚠️ Le gazelle amont installé depuis Go ne le remplace **pas** : il émet
> `:go_default_library` au lieu de la convention du dépôt, et il **supprime
> silencieusement toutes les dépendances libevm** — il ne résout pas un module
> qui se déclare `github.com/ethereum/go-ethereum` alors qu'il est requis comme
> `github.com/ava-labs/libevm`. Sur ce dépôt, il réécrit 542 fichiers. Le
> gazelle du dépôt est construit depuis le graphe de modules Bazel, qui porte
> ce mapping.

## Périmètre

| Dans le périmètre | Hors périmètre |
| :--- | :--- |
| `vms/warpfx` (nouveau paquet) | **`graft/coreth/**` — pas une ligne** |
| Aiguillage des Fx sur la PlatformVM | Contrats Solidity (les existants sont G1, à refaire) |
| Autorisation, formes canoniques, activation, tarification | X-Chain, subnet-evm |
| Attestations de retour (`TxExecuted`, `StakeSettled`, `CycleSettled`) | |
| Staking auto-renouvelé (Helicon) | |
| Volet saevm (`vms/saevm/cchain/**`) | Précompile `exportAVAX()` (lot 11) — **différé** |

> **coreth n'est pas touché du tout.** La seule ligne que le plan y prévoyait —
> `SubBalance` dans `graft/coreth/precompile/contract/interfaces.go` —
> n'appartenait qu'au lot 11, différé. Le volet C-Chain se réduit donc à
> `vms/saevm/cchain/**`, et l'argument du *Contexte* ci-dessus s'en trouve
> renforcé : coreth ne connaît pas `warpfx`, ne peut pas le marshaller, et n'a
> besoin d'aucune garde.

---

## Lot 1 — Le paquet `vms/warpfx`

→ **[Guide détaillé : 01-warpfx-types.md](01-warpfx-types.md)**

Nouveau paquet à côté de `vms/secp256k1fx`, et **pas** sous `vms/platformvm/` :
saevm doit enregistrer le type de sortie dans son codec atomique.

| Fichier | Contenu |
| :--- | :--- |
| `owner.go` | `Owner{verify.IsNotState; SourceChainID ids.ID; SourceAddress []byte}`. `Verify()` exige **exactement 20 octets** (`AddressLen = ids.ShortIDLen`). **Pas de méthode `ID()`** — voir [1.2](01-warpfx-types.md). Implémente `fx.Owner` (`verify.IsNotState`, `verify.Verifiable`, `snow.ContextInitializable`) — requis pour servir de `RewardsOwner`. ⚠️ Le marqueur s'**embarque** comme champ, il ne se déclare pas en méthode : voir [1.1](01-warpfx-types.md). |
| `transfer_output.go` | `TransferOutput{Amt uint64; Owner}`. **Pas de `Locktime`.** Implémente `verify.State`, `avax.TransferableOut` (`Amount()`), `fx.Owned` (`Owners()`), et `avax.Addressable` : `Addresses() [][]byte` → `[]{SourceAddress}`, vingt octets bruts. |
| `credential.go` | `Credential{WarpMessage []byte}`, `verify.Verifiable`. Vide pour tous les slots sauf un. |
| `authorization.go` | `Authorization{SourceChainID; SourceAddress}` + `Authorizes(*Owner) bool`. **Jamais sérialisé, jamais persisté.** |
| `fx.go` | Voir ci-dessous. Plus `Types()`, la liste unique des types revendiqués. |
| `fee.go` | `MaxFeeOverpaymentFactor = 2`, `VerifyFeeBand(consumed, produced, fee)`, `VerifyCanonicalBid(gasFeeCap, baseFee)`. Partagé entre P-Chain et saevm. |
| `factory.go` | `Name`, `ID = ids.ID{'w','a','r','p','f','x'}`, `Factory` — `warpfx` se range dans le point d'extension `vms/fx.Factory`, comme `secp256k1fx`, `nftfx` et `propertyfx`. |
| `vm.go` | L'interface `VM` que le Fx exige, sur le modèle de `secp256k1fx.VM`. |

**`SourceAddress` fait exactement 20 octets** parce que le propriétaire doit
pouvoir **recevoir** au retour : l'import C-Chain crédite un `Output.Address` de
20 octets, et une sortie à 32 octets serait constructible mais **définitivement
non importable** — fonds bloqués sans recours.

**Il n'y a ni `ownerID` ni codec local.** `Addresses()` rend `SourceAddress`
tel quel : le trait n'est **jamais** une donnée de consensus — `updateChecksum`
ne hache que l'`utxoID`, et la provenance lit toujours l'`Owner` complet depuis
l'UTXO. Vingt octets, c'est ce que toute la découverte d'avalanchego sait déjà
lire (`set.Set[ids.ShortID]`, `ParseServiceAddress`), donc le lot 8 n'a plus rien
à généraliser ; et c'est ce qui évite à un contrat Solidity de reproduire un
`linearcodec` Go pour vérifier qu'une attestation le concerne (lot 7). Détail en
[1.2](01-warpfx-types.md).

⚠️ À ne pas confondre avec la contrainte d'alignement des codecs (lots 2 et 10),
qui est réelle et sans rapport : elle porte sur `TransferOutput`, stocké dans un
champ d'interface (`avax.TransferableOutput.Out`) et qui reçoit donc bien un
identifiant de type.

**`warpfx.Fx`** :

- `VerifyTransfer` (sans contexte) → **toujours** `ErrNoAuthorization`. C'est le
  mécanisme d'*enforcement* : oublier de router une transaction par un point
  d'entrée contextuel **ferme** une porte, n'en ouvre jamais une.
- `VerifyTransferWithContext` → provenance, une fois par UTXO :
  `ErrWrongUTXOType`, `ErrWrongInputType` / `ErrUnexpectedSigIndices`
  (`SigIndices` doit être **vide**), `ErrWrongCredentialType`, `verify.All` +
  `out.Amt == in.Amt` (`ErrMismatchedAmounts`), `ErrNoAuthorization` si `fxCtx`
  nul, `authorization.Authorizes(&out.Owner)` sinon `ErrWrongOwner`.
- `VerifyPermission` → `ErrPermissionUnsupported` (un `WarpOwner` ne peut pas
  être groupe de contrôle de subnet).
- `CreateOutput(amount, *Owner)` → `*TransferOutput` — c'est ce qui matérialise
  une récompense de staking, des mois après.

> ⚠️ **La structure `Fx` ne retient aucune autorisation** — elle ne porte que le
> `VM` que lui donne `Initialize`. C'est ce qui rend structurellement impossible
> qu'une autorisation survive d'une vérification à la suivante, la **principale
> surface de bug à consensus** de la proposition.

**Aucun type d'input n'est créé** :
`secp256k1fx.TransferInput{Amt, Input{SigIndices: nil}}` est réutilisé, valide en
l'état (`vms/secp256k1fx/input.go`, `transfer_input.go`) et déjà au même index
dans le codec de la PlatformVM et dans celui de saevm.

---

## Lot 2 — Enregistrement dans les codecs de la PlatformVM

→ **[Guide détaillé : 02-codecs.md](02-codecs.md)**

Positions dérivées et vérifiées contre le code actuel —
`vms/platformvm/platform/codec.go` : `SkipRegistrations(5)` (0-4), Apricot 5→22,
Banff 23→28, `SkipRegistrations(4)` (29-32), Durango 33→34, Etna 35→39,
Helicon 40→42. **Premier index libre : 43.**

→ `Owner` = **43**, `TransferOutput` = **44**, `Credential` = **45**.

Nouvelle fonction `platform.RegisterWarpUTXOsTypes(targetCodec)`, appelée une
fois dans l'`init()` de `vms/platformvm/platform/codec.go`. Blocs et
transactions partagent ce flux unique, si bien qu'il n'y a plus deux côtés à
désaligner.

⚠️ Ces positions **bougent** si un type de transaction est ajouté à la
PlatformVM avant l'activation. Aucune signature de fonction ne protège cet
invariant : d'où `TestWarpUTXOsTypeIDs` (épingle 43/44/45) et un test
d'alignement bloc ↔ transaction.

L'enregistrement est **inconditionnel** : le gating d'avalanchego se fait à la
vérification, jamais au décodage.

---

## Lot 3 — L'aiguillage des Fx (le refactor structurel)

→ **[Guide détaillé : 03-aiguillage-fx.md](03-aiguillage-fx.md)**

Seul travail structurel de la proposition. Il touche le chemin de vérification
de **toutes** les transactions de la P-Chain.

`vms/platformvm/vm.go:130` fixe `vm.fx = &secp256k1fx.Fx{}` et `vm.go:152`
l'injecte dans `utxo.NewVerifier` ; `verifier.go:202` appelle
`h.fx.VerifyTransfer(...)`. Go ne dispatche pas sur le type d'un argument : le
codec détermine l'**argument**, jamais le **receveur**. Une sortie `warpfx` y
serait rejetée par `secp256k1fx` sur `ErrWrongUTXOType`.

> **Enregistrer les types dans le codec est nécessaire mais pas suffisant : sans
> aiguillage, tout UTXO `warpfx` est indépensable.**

Mécanisme à porter depuis la X-Chain (`vms/avm/vm.go` `typeToFxIndex`,
`vms/avm/tx_init.go` `getFx`).

| Fichier | Changement |
| :--- | :--- |
| `vms/platformvm/fx/fx.go` | Ajoute `Claim{ID, Fx, Types []any}`, `Context{Authorization interface{}}` — **pas de `ChainTime`**, `warpfx` n'a aucune règle temporelle —, et `ContextualFx{ Fx; VerifyTransferWithContext(*Context, tx, in, cred, utxo interface{}) error }`. |
| `vms/platformvm/fx/fxs.go` *(nouveau)* | `Fxs{ fxs []*Claim; typeToFxIndex map[reflect.Type]int }`, `NewFxs(claims...)` qui remplit la table **depuis `Claim.Types`**, `Get(val) Fx` (repli sur `fxs[0]`, le défaut), `Default()`, `VerifyTransfer(fxCtx, tx, in, cred, utxo)`. |
| `vms/platformvm/vm.go` | Ordre `[secp256k1fx, warpfx]`, **le premier est le défaut**. La revendication est explicite (`Types: warpfx.Types()`), donc la table est remplie par le constructeur `NewFxs` : plus de `codec.Registry` enveloppant, plus de `throwawayCodec` à faire circuler, et plus d'ordre d'appels à respecter sous peine d'une table silencieusement vide. `Initialize` reste appelé pour le cycle de vie du Fx, plus pour l'aiguillage. |
| `vms/platformvm/utxo/verifier.go` | `NewVerifier(ctx, clk, fxs)`. Résolution **par le type de la sortie consommée, dépliée de `stakeable.LockOut`**, jamais par celui de l'input. Nouveaux points d'entrée `VerifySpendWithContext` / `VerifySpendUTXOsWithContext` ; les deux historiques y délèguent avec un `fxCtx` **nul**. |
| `vms/platformvm/txs/executor/backend.go` | Gagne `Fxs *fx.Fxs` **à côté** de `Fx` (le défaut, conservé pour l'autorisation de subnet, secp-only par construction — `subnet_tx_verification.go`). Un `Fxs` nul fait échouer `newUTXO` sur `ErrNoFxForRewardsOwner` plutôt que de se rabattre silencieusement. |
| `proposal_tx_executor.go` (`newUTXO`, l.955-979) | `e.backend.Fx.CreateOutput` devient `e.backend.Fxs.Get(owner)` puis `CreateOutput` — résolution **par le type du propriétaire des récompenses**. Croiser secp/warp échoue sur `ErrWrongOwnerType` plutôt que de produire silencieusement la mauvaise sortie. C'est le **seul** appelant de `CreateOutput`. |

**Critère de fin de lot** : la suite `vms/platformvm/...` complète passe, sans
changement de comportement (`warpfx` pas encore atteignable, tout résout vers le
défaut).

---

## Lot 4 — L'autorisation

→ **[Guide détaillé : 04-autorisation.md](04-autorisation.md)**

### 4a. Le payload

`vms/platformvm/warp/message/tx_authorization.go`, enregistré à la suite des
quatre payloads ACP-77 (`message/codec.go`) → **position 4**.

```text
codecID uint16 | typeID uint32 | expiry uint64 | txBytes []byte (préfixé uint32)
```

`txBytes` = la sérialisation de la transaction P-Chain **non signée**, exactement
les octets que `Tx.Sign` fait signer à un EOA. `expiry` est le **seul** garde-fou
temporel du design. Rien d'autre : ni nonce, ni plafond de frais, ni
destinataire — tout est déjà dans `txBytes`, et une donnée redonnée ici serait
une seconde source de vérité.

**Pourquoi la préimage et non `sha256(txBytes)`** : équivalent en sécurité, mais
le soumetteur ne peut pas reconstruire une transaction à partir de 32 octets. En
portant la préimage, le log C-Chain contient la transaction entière : aucun canal
hors-bande, et la logique du soumetteur est **un chemin unique, agnostique au
type de transaction**.

**Il voyage dans `Creds`** : le message contient `txBytes`, l'y placer exigerait
que `txBytes` se contienne lui-même. `Creds` est déjà `[]verify.Verifiable`,
aucune modification de `platform.Tx`.

### 4b. Le vérifieur Warp voit la transaction signée

`vms/platformvm/txs/executor/warp_verifier.go` : `VerifyWarpMessages` est typé
`tx platform.UnsignedTx` et n'a donc **aucun accès à `Creds`**, où le message réside
nécessairement. Son paramètre devient `*platform.Tx`. Les trois points d'appel
tiennent déjà la transaction signée et lui passent `tx.Unsigned` — changement
mécanique : `block/executor/block.go:40`, `block/executor/manager.go:151`,
`block/builder/builder.go:545` (plus le passe-plat
`block/executor/warp_verifier.go:24`).

Le vérifieur fait **le quorum, et rien d'autre** : balayage de `Creds`, puis
`warp.ParseMessage` →
`GetCanonicalValidatorSetFromChainID(pChainHeight, msg.SourceChainID)` →
`Signature.Verify(…, 67, 100)`. Le `NetworkID` étant dans le message signé,
l'anti-rejeu inter-réseaux est gratuit. Sept méthodes du visiteur appellent la
nouvelle vérification ; `RegisterL1ValidatorTx` et `SetL1ValidatorWeightTx`
gardent la leur ; le reste continue de renvoyer `nil`.

### 4c. La résolution déterministe

`vms/platformvm/txs/executor/authorization.go` *(nouveau)* —
`resolveAuthorization(tx *platform.Tx, chainTime uint64) (*fx.Context, error)` :

1. `findWarpAuthorization` : balayage unique de `Creds` pour l'unique credential
   à `WarpMessage` non vide. **Le slot porteur n'est pas nécessairement le
   premier** — `Creds` est parallèle à `Ins ‖ ImportedInputs`.
   `ErrMultipleWarpAuthorizations` si deux.
2. Zéro porteur → `fx.Context{}` sans autorisation (cas secp ordinaire).
3. Porteur sur une transaction hors liste → `ErrWarpAuthorizationNotAccepted`
   (garde ergonomique, cf. 4d).
4. **Engagement** : `bytes.Equal(msg.TxBytes, tx.Unsigned.Bytes())` sinon
   `ErrAuthorizationMismatch`.
5. **Expiry** : `chainTime > expiry` → `ErrAuthorizationExpired` (l'égalité est
   valide).
6. → `fx.Context{Authorization: &warpfx.Authorization{…}}`. ⚠️ Le `chainTime` est un **paramètre** de la résolution, pas un champ du contexte : il sert à comparer l'`expiry`, puis il est oublié.

> ⚠️ **`chainTime`, jamais l'horloge locale.** `secp256k1fx` teste son locktime
> contre `h.clk.Time()` (`verifier.go:149`) ; ce comportement est à **ne pas**
> reproduire : l'`expiry` est une règle de consensus.

> ⚠️ **Règle : la résolution est appelée avant toute garde de bootstrap** de
> l'exécuteur qui la résout. Les deux vérifieurs de staking retournent `nil`
> avant tout dès que le nœud n'est pas bootstrappé, **flow check compris** ; la
> branche shared memory de l'`ImportTx` est gardée
> (`standard_tx_executor.go:337`) ; l'`ExportTx` ne garde que `verify.SameSubnet`
> (l.424) et la `BaseTx` ne saute rien. Placée à l'endroit naturel, à côté du
> flow check, la résolution serait sautée précisément sur les transactions qui en
> ont le plus besoin. La règle est sans coût — la résolution ne lit aucun état —
> mais elle est invisible à la lecture d'un exécuteur pris isolément.

**Pourquoi les trois vérifications sont réparties ainsi** — ce qui dépend du
contexte réseau va dans le vérifieur Warp, ce qui est fonction pure de la
transaction et du temps de chaîne va dans le chemin déterministe :

| Vérification | Où | Fréquence | Toujours exécutée ? |
| :--- | :--- | :--- | :--- |
| quorum BLS | `warp_verifier.go` | 1×/tx | **non** (sauté au bootstrap, caché par `pChainHeight`) |
| engagement + expiry | `authorization.go` | 1×/tx | **oui** |
| provenance | `warpfx.Fx` | 1×/UTXO | **oui** |

⚠️ **L'engagement n'est pas redondant avec la provenance.** La provenance répond
« ce message vient-il du propriétaire de cet UTXO ? » mais pas « ce message
autorise-t-il *cette* transaction ? ». Un message Warp est **public** — il figure
dans un log C-Chain que tout relayeur observe. Sans l'engagement, il deviendrait
un **jeton au porteur sur l'intégralité des fonds du propriétaire**.

### 4d. La liste autorisée

Sept transactions : `ImportTx`, `ExportTx`, `BaseTx`,
`AddPermissionlessValidatorTx`, `AddPermissionlessDelegatorTx`,
`AddAutoRenewedValidatorTx`, `SetAutoRenewedValidatorConfigTx`. Elles seules
appellent les points d'entrée `…WithContext` du vérifieur d'UTXOs.

`SetAutoRenewedValidatorConfigTx` est la seule à en demander **deux** : elle
dépense, et elle prouve l'assentiment du `ValidatorAuthority` du validateur, par
`VerifyPermissionWithContext`. Un seul message autorise les deux — il s'engage
sur les octets de la transaction entière.

Appliquée **de deux façons** :

- **structurellement** — un UTXO `warpfx` atteint par un chemin sans contexte
  reçoit `nil` et échoue sur `ErrNoAuthorization`. Oublier d'ajouter une
  transaction à la liste ne peut donc **rien ouvrir**.
- **ergonomiquement** — `acceptsWarpAuthorization`, switch à liste positive,
  défaut `false`, uniquement pour que l'échec soit lisible.

`TestAcceptsWarpAuthorization` énumère **tous** les types et fige la réponse pour
chacun : c'est ce qui rend visible en revue qu'un type ajouté plus tard hérite du
refus.

---

## Lot 5 — La forme canonique de l'`ImportTx` P-Chain

→ **[Guide détaillé : 05-import-canonique.md](05-import-canonique.md)**

`vms/platformvm/txs/executor/canonical.go` *(nouveau)*.

`isCanonicalImport` — trois conditions **ensemble** : aucun credential porteur,
`Ins` vide, tous les UTXOs importés détenus par un `WarpOwner`.

`verifyCanonicalImport` vérifie ensuite **une seule fois pour la transaction** :
credential vide par input (`ErrCanonicalImportCredential`), même `WarpOwner`
(`…MixedOwners`), AVAX seulement (`…Asset`), `in.Amount() == utxo.Amt`
(`…Amount` — *ce que le Fx aurait fait, et qui serait sinon perdu*),
**exactement une** sortie (`…OutputCount`) au même propriétaire
(`…WrongOutput`), et la fourchette de frais.

> ```
> Σ inputs − fee(bloc)  ≥  montant de la sortie  ≥  Σ inputs − k × fee(bloc)
> ```
> avec `k = warpfx.MaxFeeOverpaymentFactor = 2`.

La borne haute est le flow check lui-même. **La borne basse n'existe nulle part
ailleurs** : les flow checkers ne testent que `produced ≤ consumed` et brûlent
tout surplus sans plafond. Personne ne signant une transaction canonique,
n'importe qui consommerait sinon un UTXO de 1 000 AVAX, en restituerait 1 nAVAX
et brûlerait le reste — sans gain, mais **sans coût non plus**, une transaction
atomique n'ayant pas de payeur de gas. Pas une égalité stricte parce que les
paramètres ACP-103 du mainnet font doubler ou diviser par deux le prix du gas en
~30 s.

⚠️ **Le seul degré de liberté laissé à un tiers** : importer au pic tarifaire et
brûler jusqu'à `k × fee` d'AVAX du propriétaire. Borné (~10⁻⁵ AVAX par lot) mais
réel. Un propriétaire qui tient à ce contrôle utilise l'`ImportTx` autorisée.

> ⚠️ **Le court-circuit du Fx appartient à cette branche, et à elle seule.** Il
> n'est pas déclenché par l'absence de credential porteur en général. Généralisé
> en « pas de credential porteur → pas d'appel au Fx », il rendrait **tout UTXO
> `warpfx` dépensable par n'importe qui**. C'est la **seconde surface de bug à
> consensus** (lot 12, test 2).

---

## Lot 6 — Gardes

→ **[Guide détaillé : 06-gardes.md](06-gardes.md)**

**Destination d'export (P-Chain)** — `warp_export.go` *(nouveau)* :
`verifyWarpExportDestination(cChainID, tx)` → `ErrWarpOutputWrongDestination`.
Appelée **hors** de la garde de bootstrap, avant tout débit.
`warpfx.TransferOutput` n'est enregistré ni dans le codec de la X-Chain ni dans
celui de coreth : un export ailleurs produirait un UTXO définitivement
indécodable. ⚠️ Aujourd'hui, le seul type explicitement rejeté à l'export est
`stakeable.LockOut` (`export_tx.go:65`) — un contrôle sur un **type nommé**, pas
sur une notion.

**Activation** — `warp_activation.go` *(nouveau)* :
`VerifyWarpUTXOsActivated(upgrades, chainTime, tx)`, appelée **une seule fois, en
tête de `StandardTx`** (`standard_tx_executor.go:78`), point de passage de toutes
les transactions standard, transactions de staking comprises depuis Durango.

> Une transaction qui **mentionne** un type `warpfx` (sortie, sortie exportée,
> sortie de stake, propriétaire de récompenses, credential) est invalide tant que
> Helicon n'est pas activé.

⚠️ La garde **parcourt la transaction par réflexion** plutôt que d'énumérer les
emplacements : une liste manuelle serait périmée au premier nouveau champ. Sans
cette garde, une sortie créée avant l'activation serait indécodable par les nœuds
non mis à jour — donc un **fork**, pas un inconvénient.

C'est désormais la **seule** garde d'activation du design (cf. *Contexte*).

**Verrou `stakeable`** — `verifyWarpOutputsNotLocked(tx)` *(nouveau, même
fichier)* : une sortie dont le type déplié appartient à `warpfx` ne peut pas être
enveloppée dans un `stakeable.LockOut` → `ErrWarpOutputNotLockable`. Appelée
**inconditionnellement** en tête de `StandardTx`, à côté de la garde
d'activation — c'est le seul parcours qui subsiste après l'upgrade. Règle absente
de l'ACP, à y reporter ; voir [6.3](06-gardes.md).

---

## Lot 7 — Les attestations de retour

→ **[Guide détaillé : 07-attestations.md](07-attestations.md)**

Trois payloads ajoutés au registre existant → positions **5**, **6** et **7**.
`AddressedCall` à `SourceAddress` **vide**, chaîne source = P-Chain.

```text
TxExecuted   : codecID | typeID | txID [32] | authHash [32]
StakeSettled : codecID | typeID | stakingTxID [32]
               | sourceChainID [32] | sourceAddress [20]
               | numRewards uint32 | { outputIndex uint32 | amount uint64 } × numRewards
CycleSettled : codecID | typeID | rewardTxID [32]
               | sourceChainID [32] | sourceAddress [20]
               | numRewards uint32 | { outputIndex uint32 | amount uint64 } × numRewards
```

⚠️ **`StakeSettled` et `CycleSettled` nomment la même chose sous deux clés
différentes**, parce que le code les indexe ainsi : la clé de `GetRewardUTXOs`
est le `txID` **de staking** pour un staker à durée fixe, et le `txID` **du
`RewardAutoRenewedValidatorTx`** — un par cycle — pour un auto-renouvelé. Un
`StakeSettled` sur un validateur auto-renouvelé rendrait donc toujours
`numRewards = 0`.

`authHash = sha256(txBytes)`, exactement le champ du message d'autorisation : le
`txID` est l'information nouvelle.

**Pourquoi elles sont nécessaires**, en deux points structurels :

1. **Le `txID` est malléable.** Le propriétaire s'engage sur les octets *non
   signés* ; le `txID` est le hash des octets *signés*, credential compris, donc
   fonction du sous-ensemble de validateurs que le soumetteur a joint. Deux
   soumissions de la même autorisation → deux `txID` distincts (une seule peut
   être acceptée, mais il ne peut pas prédire laquelle). Un UTXO étant nommé par
   `(txID, index)`, **un contrat ne peut sinon pas nommer les UTXOs que sa propre
   transaction vient de créer**, ni son change.
2. **La récompense n'est prévisible ni en montant ni en position.** Son
   `OutputIndex` dépend de l'issue commit/abort du `RewardValidatorTx`, elle-même
   fonction de l'uptime : la récompense de délégataire est à
   `len(outputs)+len(stake)+offset` sur *commit* et à `len(outputs)+len(stake)`
   sur *abort*. ⚠️ Noter aussi que `unstakeUTXOs` n'appelle que `AddUTXO` — le
   **remboursement du stake n'est pas dans `GetRewardUTXOs`**.

`StakeSettled` **nomme** les UTXOs plutôt que d'en donner la somme : le montant
par UTXO est requis parce que dépenser demande un `TransferableInput` dont
l'`Amt` égale **exactement** celui de la sortie — une somme agrégée décrirait un
solde non dépensable. `numRewards = 0` signifie « clos sans récompense », jamais
confondu avec « pas encore clos », le message n'étant signé qu'une fois le staker
sorti.

⚠️ **L'ordre est normatif** (`outputIndex` croissant). `GetRewardUTXOs` retourne
l'ordre de la base, et l'agrégation BLS exige des **octets identiques** : sans
tri, deux nœuds produisent deux messages et **aucun quorum ne se forme jamais** —
un échec silencieux qui n'apparaît qu'en intégration.

`vms/platformvm/network/warp_utxos.go` *(nouveau)* + deux `case` dans le `switch`
de `signatureRequestVerifier.Verify` (`network/warp.go:82`). **Rien n'est émis ni
stocké** : tout est dérivé à la demande de l'état accepté, donc l'invariant
« aucun état ajouté à la P-Chain » tient.

Règles de signature :

- `TxExecuted` : `GetTx(txID)` existe, statut `Committed`,
  `sha256(tx.Unsigned.Bytes()) == authHash`, et la transaction porte au moins un
  `*warpfx.Credential`. ⚠️ Cette dernière condition **borne volontairement le
  périmètre** : sans elle la P-Chain deviendrait un oracle d'acceptation de
  transactions à usage général.
- `StakeSettled` : liste triée, transaction de staking `Committed` portant un
  `*warpfx.Credential`, staker **ni courant ni pending**, et liste exactement
  égale aux `GetRewardUTXOs` filtrés sur la paire `(sourceChainID, sourceAddress)`.
  Le message nomme le propriétaire par sa paire plutôt que par un hash : un
  contrat vérifie qu'il est concerné par `sourceAddress == address(this)`, sans
  reproduire de `linearcodec` en Solidity.

Deux points d'implémentation :

- ⚠️ **`GetRewardUTXOs` n'appartient pas à `state.Chain`** (qui ne déclare que
  `AddRewardUTXO`) → interface locale `network.Chain` qui l'élargit.
- L'état **n'indexe pas les stakers par transaction**. `GetTx(stakingTxID)` rend
  le `(subnetID, nodeID)` : validateur en **O(1)** via `GetCurrentValidator` /
  `GetPendingValidator` + comparaison du `TxID` ; délégateur par itération bornée
  aux délégateurs de **ce seul** validateur. Aucun index nouveau, aucune
  `justification` nécessaire.

**Lecture côté C-Chain : zéro ligne à écrire.** Le prédicat Warp traite déjà
nommément `SourceChainID == ids.Empty` en retenant le set du subnet de la chaîne
réceptrice. À documenter pour les intégrateurs : `sourceChainID == bytes32(0)` et
`originSenderAddress == address(0)` (un contrat qui teste « non nul » rejette
**toutes** les attestations) ; un prédicat absent ne *revert* pas, il renvoie
`Valid: false` ; le point d'entrée doit être appelable par n'importe qui, le
prédicat appartenant à la transaction et non à l'appel.

---

## Lot 8 — Découverte hors chaîne

→ **[Guide détaillé : 08-decouverte.md](08-decouverte.md)**

Indispensable, pas un confort : le propriétaire doit construire la transaction
complète avant de pouvoir l'autoriser.

**Le lot est court, parce que le trait fait vingt octets** (lot 1.2) : toute la
machinerie existante le lit sans modification — `avax.GetAtomicUTXOs`
(`set.Set[ids.ShortID]`), `state.State.UTXOIDs(addr []byte, …)`, et même
`avax.ParseServiceAddress`. Rien à généraliser.

- `vms/platformvm/service_warp_utxos.go` + `client.go` :
  `platform.getWarpOwnerUTXOs`, qui prend la paire `(sourceChainID,
  sourceAddress)` au format `0x…` et choisit, via `atomicSourceChain`, entre
  l'index P-Chain et la shared memory. ⚠️ **Cette méthode est désormais une
  commodité, pas une nécessité** : `platform.getUTXOs` sait servir un `WarpOwner`
  tel quel, l'adresse EVM s'encodant en Bech32 comme n'importe quels vingt
  octets. Ce qu'elle apporte est de ne pas faire passer une adresse EVM pour une
  adresse P-Chain dont l'utilisateur croirait avoir la clé.

Détail visible en API, sans effet sur le consensus : `import_tx.go:38-43` et
`export_tx.go:38-44` forcent `FxID = secp256k1fx.ID`. Champ `serialize:"false"`,
mais un `fxID` erroné s'affichera dans les explorateurs tant qu'il n'est pas
conditionné au type.

---

## Lot 9 — Tarification (P-Chain)

→ **[Guide détaillé : 09-tarification.md](09-tarification.md)**

Le calculateur de complexité **dispatche par type** et refuse ce qu'il ne
connaît pas : `outputComplexity` type-asserte `*secp256k1fx.TransferOutput`
(`complexity.go:305`) → `errUnsupportedOutput` ; idem `OwnerComplexity`
(l.421-422). ⚠️ Un type oublié échoue au **calcul de frais**, pas à la
vérification, ce qui égare le diagnostic.

- `complexity.go` : `outputComplexity` devient un `switch` sur le type déballé,
  avec `intrinsicWarpFxOutputBandwidth` ; `OwnerComplexity` gagne un cas
  `*warpfx.Owner`.
- `credential_complexity.go` *(nouveau)* : la complexité est calculée sur
  `tx.Unsigned`, or un credential `warpfx` ne peut pas être un champ de la
  transaction non signée (il contient ces octets). Il **échapperait au
  calculateur**, et la vérification BLS d'un set de plus de 1 000 signataires
  serait gratuite. Appliquer `WarpComplexity` au message porteur, comptée **une
  fois**. Les signatures secp ne sont pas tarifées ici, déjà couvertes par les
  indices des inputs.
- `calculator.go` / `dynamic_calculator.go` / `simple_calculator.go` :
  `CalculateFeeWithCredentials(tx *platform.Tx)` à côté de
  `CalculateFee(tx platform.UnsignedTx)`, appelée par les sept transactions de la
  liste.

⚠️ **`SignedTxComplexity(tx)` doit remplacer `TxComplexity(tx.Unsigned)` aux
trois endroits qui rationnent l'espace de bloc** — `block/executor/verifier.go`,
`block/executor/manager.go` (admission mempool), `block/builder/builder.go`.
Sans cela le frais serait juste mais la **capacité fausse**.

---

## Lot 10 — Volet saevm

→ **[Guide détaillé : 10-saevm.md](10-saevm.md)**

### 10a. Alignement du codec

`vms/saevm/cchain/tx/codec.go` : Import→0, Export→1, skip 3 (2-4),
TransferInput→5, skip 1 (6), TransferOutput→7, skip 1 (8), Credential→9.
**Dernier index occupé : 9.**

→ `SkipRegistrations(34)` (10→43) puis `RegisterType(&warpfx.TransferOutput{})`
qui tombe sur **44**. Seul ce type y figure : un `Owner` n'apparaît qu'embarqué,
et un credential `warpfx` n'a pas cours sur le chemin atomique.

⚠️ Ce décalage n'est **pas** celui de coreth (dernier index 11, donc 32) : coreth
enregistre `secp256k1fx.Input` et `OutputOwners`, saevm non. Un test écrit contre
l'un ne dit rien de l'autre — et ici on n'écrit que celui de saevm.

⚠️ **Il n'existe aujourd'hui aucun test d'alignement entre saevm et la
PlatformVM.** Aucun fichier sous `vms/saevm/` n'importe `vms/platformvm/txs` ; la
garantie repose sur un commentaire (`codec.go:27-29`) et sur les tests de
compatibilité binaire avec coreth. C'est un vrai trou de couverture, que ce lot
comble.

### 10b. Destination d'export

`Export.sanityCheck` (`export.go:116`) : une sortie `warpfx` force
`DestinationChain == constants.PlatformChainID`, sinon
`errWarpOutputWrongDestination` — **avant tout débit**, l'export débitant le
solde EVM avant que la chaîne cible n'ait rien à dire.

Rien d'autre à l'export : `Export.verifyCredentials` est une récupération de clé
secp en dur sans dispatch (un export `warpfx` reste signé par le détenteur du
solde débité), et les traits partent gratuitement — `atomicRequests` fait déjà
`if o, ok := utxo.Out.(avax.Addressable); ok { elem.Traits = o.Addresses() }`
(`export.go:254`).

⚠️ **Conséquence remarquable : pour un EOA, alimenter un `WarpOwner` ne demande
aucune modification du consensus C-Chain.** L'EOA signe son export atomique comme
aujourd'hui et désigne un `WarpOwner` — y compris **une adresse tierce**, ce qui
permet de financer un contrat sans que celui-ci ait à agir.

### 10c. La règle miroir à l'import

`Import.verifyCredentials` (`import.go:157`) appelle
`fx.VerifyTransfer(fxTx, in.In, creds[j], utxo.Out)` par input, avec un
`secp256k1fx.Fx` de paquet en dur (`tx/fx.go`). ⚠️ **La C-Chain ne tient aucune
table de Fx, et c'est délibéré** : porter une table ici pour appeler une méthode
qui n'aurait rien à faire serait du travail sans contrepartie.

Nouveau `vms/saevm/cchain/tx/warp_canonical.go`. Tous les UTXOs sont désérialisés
**d'abord**, puis `isCanonicalImport(utxos)` déclenche `verifyCanonicalImport`
**et le court-circuit de la boucle** :

- tous du même `WarpOwner` (`errCanonicalMixedOwners`) ;
- `Owner.SourceChainID == ctx.ChainID` (`errCanonicalWrongSource`) ;
- `in.In.Amount() == out.Amt` (`errCanonicalAmount`) ;
- **exactement une** sortie (`errCanonicalOutputCount`), en AVAX
  (`errNonAVAXOutput`), d'adresse `== SourceAddress` (`errCanonicalWrongOutput`) ;
- credential vide par input (`errCanonicalCredential`).

⚠️ **Le credential vide est ici un `secp256k1fx.Credential` sans signature, pas
un `warpfx.Credential`** — c'est le seul encodage de « cet input ne présente
rien » sur lequel les deux codecs s'accordent (l'interface `tx.Credential` est
`Self() *secp256k1fx.Credential`), et il garde la transaction **byte-identique**
pour deux soumetteurs qui la reconstruisent.

⚠️ **Le court-circuit lit la forme du lot entier, jamais l'absence de
signature.** Un lot mêlant un UTXO `warpfx` et un UTXO signé retombe sur la
boucle ordinaire, où le premier atteint `secp256k1fx` et se fait refuser. Échouer
dans ce sens est tout l'intérêt.

⚠️ **`verifyCredentials` rend la canonicité du lot à son appelant** : la
déterminer demande de lire les UTXOs consommés, ce que cette méthode fait déjà.
Une seconde lecture de shared memory serait du travail et une source de
divergence.

⚠️ Le crédit se fait **sans exécution de code** (ni `receive()` ni `fallback`,
comme un `selfdestruct`) : la comptabilité d'un contrat destinataire doit
fonctionner en mode *pull*. Un solde EVM étant additif, la contrainte de sortie
unique ne coûte rien de ce côté.

⚠️ Tout nouvel `errXxx` doit être ré-exporté dans
`vms/saevm/cchain/tx/identifiers_test.go`, les tests vivant dans le paquet
externe `tx_test`.

### 10d. La borne d'enchère

Chez saevm le montant brûlé n'est pas un frais mais une **enchère** :
`Import.sanityCheck` ne produit aucun terme de frais, et `Tx.AsOp` pose
`GasFeeCap: gasPrice(burned, gas)` (`tx.go:159`). Brûler trop peu n'est donc
**pas invalide** — la transaction attend simplement que le prix descende, le
plancher `o.GasFeeCap.Lt(s.baseFee) → core.ErrFeeCapTooLow`
(`worstcase/state.go:298`) s'en chargeant. **La borne haute de la fourchette
disparaît**, parce qu'elle était le flow check majoré du frais et que saevm ne
majore rien.

⚠️ **La borne basse devient plus nécessaire, pas moins.** Là où le brûlage est un
frais, tout brûler est de la destruction pure sans bénéfice. Là où il est une
enchère, tout brûler **maximise la priorité d'inclusion** aux frais du
propriétaire, et le mécanisme de frais sert un soumetteur pressé en priorité.

> **Règle.** `VerifyCanonicalBid(op.GasFeeCap, building.BaseFee)` →
> `errBidTooHigh` au-delà de `k × baseFee` — le symétrique exact du plancher, sur
> la même grandeur contre la même référence.

**Emplacement** : `builder.PotentialEndOfBlockOps` (`cchain/hooks.go:438`), seul
endroit qui voie à la fois le type des transactions et un `building *types.Header`
dont le `BaseFee` est déjà posé par `worstcase.State.StartBlock`
(`state.go:128,137`) depuis la même horloge à gas que le plancher.
`worstcase.State.Apply` serait incorrect : `ApplyTx` y converge, c'est le chemin
**commun** avec les transactions EVM ordinaires, dont l'auteur peut légitimement
surpayer.

### 10e. Aucune garde d'activation côté saevm

`builder.BuildHeader` (`hooks.go:379-383`) refuse de construire tant que
`IsHeliconActivated(now)` est faux, et `VerifyBlock` **reconstruit et compare les
empreintes** (`sae/blocks.go:78-90`, `errHashMismatch`). `BlockRebuilderFrom`
(`hooks.go:118-124`) fixe le `now` du reconstructeur à l'horodatage du bloc
examiné : la question posée à la vérification est exactement celle posée à la
construction. Le premier bloc saevm est donc nécessairement à ou après
`HeliconTime`, malgré une bascule de VM dix secondes plus tôt.

> **Toute règle du constructeur est une règle de consensus.** C'est ce qui rend
> la borne d'enchère énonçable là où elle est posée, et ce qui dispense le volet
> C-Chain de toute garde par transaction.

⚠️ Corollaire piégeux : **`EndOfBlockOps` ne vérifie rien** (`hooks.go:233`, un
simple `ParseSlice` + `AsOp`). `SanityCheck` et `VerifyCredentials` ne sont
appelées que dans `PotentialEndOfBlockOps`, atteinte à la vérification **par le
reconstructeur**. Lire `EndOfBlockOps` seul induit en erreur.

**Aucun changement de tarification** : `gasUsed` compte des octets et des
signatures sans connaître les types.

---

## Lot 11 — Le précompile d'export *(différé)*

→ **[Guide détaillé : 11-precompile.md](11-precompile.md)**

> **Ce lot ne fait pas partie de la première livraison.** La proposition est
> complète et utile sans lui, et il concentre à lui seul les trois risques que
> le reste du plan n'a pas.
>
> **Ce qui marche sans.** Le lot 10b établit qu'un EOA signe un export atomique
> désignant un `WarpOwner` — **y compris celui d'une adresse tierce**. Financer
> un contrat ne demande donc aucune action du contrat, et ne demande pas le
> précompile. Tout le cycle est atteignable : alimentation, import canonique,
> staking autorisé, attestations, retour.
>
> **Ce qui manque.** Un contrat ne peut pas déplacer son **propre** solde AVAX
> EVM vers la P-Chain de sa propre initiative. C'est une capacité réelle, et
> c'est la seule.
>
> **Pourquoi le différer plutôt que le faire en dernier.** Trois raisons qui ne
> se recoupent pas : c'est le **seul lot qui touche coreth** (`SubBalance` dans
> une interface partagée) ; c'est le **seul dont une erreur est durable** — une
> sous-tarification d'écriture d'état produit de l'état permanent que personne
> n'a payé, et ça ne se rattrape pas au prochain upgrade ; et c'est le seul qui
> introduise un identifiant d'UTXO **sans conteneur naturel**, dont les trois
> dérivations ont chacune un défaut. Un composant à la fois différable et risqué
> se différencie.
>
> L'ACP le qualifie déjà de « différable ». Le guide reste écrit et à jour : le
> différer, c'est reporter son écriture, pas jeter son analyse.

Un contrat n'a pas de clé et ne peut donc pas signer un export atomique. Un
précompile lui permet d'initier un export débitant son propre solde AVAX EVM, le
propriétaire de la sortie étant forcé à l'appelant — exactement comme le
précompile Warp force le `SourceAddress` des `AddressedCall`.

```solidity
// Exporte msg.value vers la P-Chain, au profit du WarpOwner de l'appelant.
function exportAVAX() external payable;
```

**Un seul point d'entrée, sans paramètre.** Le précompile débite, dépose un UTXO
en shared memory, et s'arrête là : l'`ImportTx` P-Chain étant canonique,
n'importe qui la construit ensuite à partir de ce qu'il lit en shared memory.

### 11a. Où il vit

`modules.RegisterModule` (`graft/coreth/precompile/modules/registerer.go`) est un
registre global peuplé par les `init()`, et `vms/saevm/cchain/genesis.go`
référence le paquet du précompile **directement**, pas via
`graft/coreth/precompile/registry`. Le nouveau paquet peut donc vivre sous
**`vms/saevm/cchain/precompile/nativeexport/`**.

Adresse à prendre dans une plage réservée (`0x0200…00` – `0x0200…ff`, le
précompile Warp occupant `…0005`). ⚠️ À reporter dans l'ACP et dans l'interface
Solidity une fois figée.

⚠️ **À vérifier au premier build** : enregistrer un module dans le registre
global ne doit pas le rendre activable côté coreth. L'activation passe par
`extras.UpgradeConfig.PrecompileUpgrades`, que seule `saevm/cchain/genesis.go`
alimente — aujourd'hui d'une seule entrée, le précompile Warp calé sur
`DurangoTime` (`genesis.go:124-132`) ; on y ajoute la nôtre, calée sur
`HeliconTime`. Mais le `ConfigKey` devient parsable dans un JSON d'upgrade. Si le couplage s'avère réel, le repli
est d'héberger le paquet sous `graft/coreth/precompile/contracts/` et de ne
l'activer que dans la config saevm.

### 11b. Dérivation des champs

Un seul paramètre porte de la valeur ; tout le reste vient du contexte d'appel.

| Champ | Source |
| :--- | :--- |
| `DestinationChain` | `constants.PlatformChainID`, forcé |
| `Owner.SourceChainID` | `ctx.ChainID` (via `GetSnowContext()`) |
| `Owner.SourceAddress` | `caller`, forcé — **non usurpable** |
| `AssetID` | `ctx.AVAXAssetID`, forcé |
| `Amt` | `msg.value / X2CRate` |
| `UTXOID.TxID` | `stateDB.TxHash()` |
| `UTXOID.OutputIndex` | compteur intra-transaction, voir 11d |

**Validation de `msg.value`** : les non-multiples de `X2CRate` sont **rejetés**
plutôt que tronqués (une troncature perdrait silencieusement jusqu'à 1 nAVAX par
appel) ; le quotient doit tenir dans un `uint64` ; `Amt == 0` est rejeté, par
parité avec `secp256k1fx.ErrNoValueOutput`.

**Il n'y a pas de nonce**, et ce n'est pas un oubli. `Export.Input.Nonce`
n'existe que parce qu'une transaction atomique est un objet autonome, hors EVM,
avec son propre mempool. Un appel de précompile vit dans une transaction EVM qui
a déjà le sien. S'en servir pour l'unicité de l'`UTXOID` serait d'ailleurs
incorrect : le nonce d'un contrat ne s'incrémente que sur `CREATE`, donc
plusieurs appels successifs collisionneraient — et l'incrémenter en effet de bord
décalerait la suite des adresses `CREATE` du contrat.

### 11c. Le débit

La forme `payable` déplace la cible du débit vers l'adresse du précompile, et
apporte la vérification de solde, le `revert` propre par l'EVM et la sémantique
`call{value: …}` que tout outillage connaît. Elle ne dispense pas d'ajouter
`SubBalance` à `contract.StateDB` : la valeur reçue doit **quitter** le bilan de
l'EVM, puisqu'elle est désormais matérialisée en UTXO P-Chain.

Un point agréable : `worstcase.State` couvre déjà ce débit sans modification —
`txToOp` pose `MinBalance = gas × gasFeeCap + value`, et `value` est ici le
`msg.value` envoyé au précompile.

### 11d. Dérivation de l'`OutputIndex`

Aucun compteur n'existe : l'index d'une sortie exportée est son rang dans
`ExportedOutputs`, ce que permet le caractère auto-contenu d'une transaction
atomique. Un précompile n'a pas ce conteneur — plusieurs appels, depuis plusieurs
contrats, peuvent coexister dans une même transaction EVM.

L'ACP laisse le choix ouvert entre trois dérivations. **Recommandation : (2), un
compteur dans le stockage du précompile**, un seul slot portant
`(lastTxHash, count)` — remis à zéro dès que `stateDB.TxHash()` diffère,
incrémenté sinon.

- ✅ lecture et écriture en **O(1)**, donc un coût en gas prévisible ;
- ✅ `SSTORE` est journalisé, donc **correct au `revert`** au même titre que
  `AddLog` ;
- ✅ borné : un slot, jamais un index qui croît.
- ❌ un `SSTORE` par export.

L'option (1) — filtrer `stateDB.Logs()` — est écartée : `Logs()` est de portée
**bloc**, donc le coût du parcours dépend des transactions **précédentes** du
bloc, que l'appelant ne contrôle ni ne prévoit. Facturer un forfait tout en
exécutant un parcours de coût variable ouvrirait un vecteur de déni de service.
L'option (3) — `salt` fourni par l'appelant — laisse ouverte la question d'un
appelant qui collisionne avec lui-même, une clé dupliquée dans un même `Apply` ne
devant pas pouvoir invalider un bloc.

### 11e. Les opérations de shared memory

> **Règle.** Les opérations de shared memory d'un bloc issues du précompile sont
> dérivées des **logs** émis par lui, et d'eux seuls.

C'est ce qui donne la sûreté au `revert` sans structure nouvelle : `AddLog` est
journalisé et le `revert` d'un frame efface ses logs. Une transaction en échec
est incluse au bloc avec `status = 0` (elle a consommé du gas) mais ses logs ont
été rembobinés avant construction du receipt. Tout canal latéral au journal
laisserait au contraire fuiter un dépôt depuis un appel annulé.

Le point d'ancrage existe déjà, et le précédent est exact :
`hooks.AfterExecutingBlock(b, receipts)` (`cchain/hooks.go:277`) appelle
`warp.FromReceipts(receipts)` juste après `h.state.Apply(b.NumberU64(), txs)`. On
ajoute une fonction de même forme — `nativeexport.FromReceipts(receipts)
(map[ids.ID]*chainsatomic.Requests, error)` — et `State.Apply` reçoit ces
`Requests` en plus des transactions, `atomicRequests(txs)` (`state.go:146`) les
fusionnant avant `applyTrie`. La plomberie est déjà paramétrée par des `Requests`
et non par des transactions, et le trie atomique suit gratuitement.

⚠️ `State.Apply` indexe les transactions par ID (`writeTx`, `state.go:173`). Une
opération sans transaction n'a rien à y enregistrer. Cet index sert l'API et le
reprocessing, **pas le consensus** : l'effet d'une absence est un trou d'API, pas
une divergence.

### 11f. Tarification

**Gas EVM seul, sans burn AVAX additionnel** : empiler deux mécanismes pour la
même opération serait arbitraire, et pour un contrat le précompile n'est pas une
alternative moins chère mais la seule voie possible. La parité avec un export
atomique équivalent se cherche par le **calibrage du coût en gas**, pas par un
second prélèvement.

⚠️ Ce coût n'est pas un coût de calcul mais de **croissance d'état permanente**
(sérialisation de l'UTXO, `PutRequest` en shared memory, alimentation du trie
atomique) : un forfait aligné sur le seul calcul sous-tarifierait durablement.
**C'est le seul point du plan où une erreur de calibrage est réellement
dommageable** — à mesurer avant de figer les constantes (coût d'écriture dans le
trie atomique rapporté à celui d'un `SSTORE`).

Le changement de régime tarifaire est à assumer : `extDataGasUsed` et
`BlockFeeContribution` ne sont plus alimentés par cet export, sans conséquence —
ces deux mécanismes arbitrent un objet doté de son propre mempool, ce qu'un
précompile n'est pas, étant déjà ordonnancé par la transaction EVM qui le
contient.

### 11g. Ce que la forme apporte

Il n'y a plus d'`Export` à vérifier : la récupération de clé publique par input,
l'appariement `len(Ins) == len(Creds)` et le nonce de compte n'ont plus d'objet,
l'autorité venant du frame d'appel où `msg.sender` n'est pas usurpable. La
détection de conflit tombe également — un export ne produit que des
`PutRequests`, il ne consomme aucun UTXO de shared memory. Et le chemin devient
accessible à **tout outillage capable d'émettre une transaction EVM**, smart
accounts et signataires matériels sans support Avalanche natif compris.

⚠️ **Limite assumée** : l'adresse source étant forcée à l'appelant, le précompile
ne permet pas de créditer le `WarpOwner` d'une **autre** adresse. Le chemin par
EOA (lot 10b) le permet toujours.

---

## Lot 12 — Tests d'invariant

→ **[Guide détaillé : 12-tests.md](12-tests.md)**

Par ordre de valeur. Les quatre premiers sont bloquants pour la revue.

1. **Rejeu d'autorisation entre transactions** — autoriser A, attacher le même
   credential à B. Doit échouer sur l'engagement. *Principale surface de bug à
   consensus.*
2. **Portée du court-circuit canonique (P-Chain)** — consommer un UTXO `warpfx`
   sans credential porteur depuis une `BaseTx`, puis depuis une `ImportTx` à
   `Ins` non vides. Doit échouer sur `ErrNoAuthorization`. *Seconde surface.*
3. **Rémanence d'autorisation** — deux vérifications successives sur la même
   instance de `Fx` : la seconde, sans contexte, doit échouer.
4. **Alignement des codecs** — `TestWarpUTXOsCodecTypeIDs` (43/44/45) ;
   `platform.Codec` ↔ `vms/saevm/cchain/tx` sur un même `avax.UTXO`.
5. **Dépense hors liste autorisée** — un chemin sans contexte doit échouer.
6. **Fourchette de frais** — un nAVAX en dessous et au-dessus des deux bornes
   côté P-Chain ; côté saevm, un aAVAX au-delà du plafond d'enchère, avec la
   symétrie explicite : les deux refus doivent porter sur la même grandeur contre
   la même référence.
7. **Récompense vers un `WarpOwner`** — staking complet jusqu'au
   `RewardValidatorTx`, sur les **deux** branches commit et abort, puis
   `StakeSettled` nommant les bons index dans le bon ordre.
8. **Activation** — la même transaction avant et après `HeliconTime`.
9. **Tarification du credential** — deux autorisations différant par le nombre de
   signataires donnent deux frais différents ; et la capacité de bloc en tient
   compte.
10. **Portée du court-circuit saevm** — un lot mêlant un UTXO `warpfx` et un UTXO
    signé doit retomber sur la boucle ordinaire et s'y faire refuser.
11. **Destination d'export** — une sortie `warpfx` vers la X-Chain, refusée des
    deux côtés, avant tout débit.
12. **Refus de bloc saevm avant activation** — un bloc horodaté dans la fenêtre
    de transition ne doit pas se reconstruire. C'est ce sur quoi repose la
    dispense de garde du volet C-Chain.
13. **Non-régression de l'aiguillage** — la suite `vms/platformvm` complète.
    Plus deux tests pour les règles ajoutées : un `stakeable.LockOut`
    enveloppant une sortie `warpfx` doit échouer, et un `warpfx.Owner` en
    propriétaire de subnet aussi.
14. **Sûreté du `revert` du précompile** — un `exportAVAX` dans un frame qui
    *revert* ne doit produire **aucun** `PutRequest`, et ne doit pas consommer
    d'`OutputIndex`. Une transaction en échec est incluse au bloc avec
    `status = 0` : c'est le cas exact à couvrir.
15. **Unicité de l'`UTXOID` du précompile** — plusieurs `exportAVAX` dans une même
    transaction EVM, depuis plusieurs contrats, doivent produire des `UTXOID`
    distincts ; deux transactions distinctes ne doivent pas partager de compteur.
16. **`msg.value` non multiple de `X2CRate`** — rejeté, jamais tronqué.
17. **Parité tarifaire** — à `baseFee` égale, `exportAVAX` doit se situer dans le
    même ordre de grandeur qu'un `Export` atomique équivalent (bench, lot 11f).

**E2E**, trois specs qui partagent leurs helpers dans `tests/e2e/p/` :

- `warp_utxos.go` — le parcours complet avec un **EOA** pour propriétaire :
  export EOA signé → import canonique P-Chain → staking autorisé par Warp →
  `TxExecuted` → `ExportTx` autorisée → import canonique saevm → solde EVM
  crédité, constaté en mode *pull*. La seconde branche d'alimentation, un `exportAVAX()`
  depuis un contrat, est arrivée avec le lot 11 ;
- `warp_utxos_contract.go` — le même parcours avec un **contrat Solidity**
  (`warp_owner.sol`) : il possède, mise et récupère de l'AVAX sans clé, et
  reçoit l'attestation `TxExecuted` par un prédicat livré par un relayeur ;
- `warp_utxos_mixed.go` — ce que le parcours ne montre pas : un input signé et
  un input autorisé **dans une même transaction**, et le refus de deux
  propriétaires `warpfx` sous une seule autorisation ;
- `warp_export_precompile.go` — l'aller-retour par le **précompile**, joué deux
  fois : un EOA et un contrat déplacent chacun leur *propre* solde ;
- `warp_staking_family.go` — la **délégation** et le validateur
  **auto-renouvelé** dont l'autorité est un propriétaire warp.

⚠️ **Le cas qui n'existe dans aucun VM pris isolément** : un UTXO déposé par la
P-Chain post-Helicon doit être consommable par saevm, et aucun export coreth
pré-transition ne doit pouvoir en produire.

⚠️ **Le staking passe en dernier.** Le quorum d'une transaction autorisée est
vérifié à une hauteur P-Chain qui n'avance qu'à l'acceptation d'un bloc, alors
que l'agrégateur signe contre le jeu courant : juste après un
`AddPermissionlessValidatorTx`, les index du bitset BLS se décalent et rien ne
converge tant qu'aucun autre bloc n'est produit. Détail dans
[`12-tests.md`](12-tests.md).

---

## Vérification

```bash
# Par lot
go test ./vms/warpfx/...
go test ./vms/platformvm/...          # non-régression de l'aiguillage (lot 3)
go test ./vms/saevm/cchain/...

# Les trois invariants silencieux, sans garde de compilateur
go test ./vms/platformvm/txs/   -run Codec   # 43/44/45
go test ./vms/platformvm/block/ -run Codec   # bloc ↔ tx
go test ./vms/saevm/cchain/tx/  -run Codec   # saevm ↔ platformvm

# Complet
./scripts/build.sh && go test ./...
```

E2E : `./bin/ginkgo -v --focus="Warp UTXOs" ./tests/e2e --
--avalanchego-path=$PWD/build/avalanchego --node-count=5
--activate-latest-after=90s` — `HeliconTime` programmé dans le futur proche, pas
au genesis, pour que la transition de VM soit observable.

Sur un réseau déployé, voir [`POC-c-p.md`](POC-c-p.md) — notamment la contrainte
qui oblige à un `network-id` personnalisé pour qu'un fichier d'upgrade soit
seulement pris en compte.

---

## Ordre d'exécution

> ⏱ **Si tu passes par le [PoC](POC-c-p.md)**, l'ordre change en tête : sa
> phase A demande **1 → 2 → 5 (réduit) → 9.1 → 10a**, et se passe du lot 3. Le
> lot 3 redevient un préalable strict au moment de la phase B, c'est-à-dire dès
> qu'une transaction doit être *autorisée* plutôt que canonique.

Sans le PoC, lots **1 → 2 → 3** sont un préalable strict. Ensuite :

- **4 → 5 → 6** : le cœur du consensus P-Chain, en séquence.
- **10** : indépendant à partir du lot 2, peut avancer en parallèle de 4-6.
- **7, 8, 9** : indépendants entre eux, après le lot 6.
- **12** : au fil de l'eau, chaque test avec le lot qu'il protège.

**Le lot 11 est hors de cette séquence** : il est différé (voir ci-dessus). S'il
revient, il ne dépend que des lots 1 et 10a — il produit un UTXO `warpfx` en
shared memory, que la forme canonique du lot 5 consomme déjà — et il se fait en
dernier, benché contre un chemin en place.

## Points à trancher

Chacun renvoie au guide qui l'explique. Les deux premiers sont **bloquants pour
l'écriture** : ce sont des règles de consensus qu'on ne peut pas ajouter après
activation.

| Décision | Guide | Recommandation |
| :--- | :--- | :--- |
| Dérivation de l'`OutputIndex` du précompile | [11d](11-precompile.md) | **(2)**, compteur en stockage |
| Import canonique à `Ins` non vides | [5](05-import-canonique.md) | **Refuser** |
| Surface de l'interface `Verifier` | [3](03-aiguillage-fx.md) | Exposer `…WithContext` |
| Sources fusionnées dans `getWarpOwnerUTXOs` | [8](08-decouverte.md) | Deux appels |
| Variante à destinataire explicite du précompile | [11g](11-precompile.md) | Hors périmètre |

Et les paramètres, qui se calibrent plutôt qu'ils ne se décident :

- **Calibrage de `k`.** `k = 2` couvre la période de doublement du prix du gas
  P-Chain sous congestion maximale. ⚠️ **La question se dédouble** : côté P-Chain
  `k` borne un **frais absolu**, côté saevm il borne une **enchère**, donc un
  multiple du prix courant. La même valeur peut convenir, mais elle ne mesure
  plus la même chose et se calibre contre la volatilité du prix du gas.
- **Valeur d'`expiry` par défaut** recommandée à l'outillage : trop courte, une
  autorisation de multisig expire avant d'avoir réuni ses signatures ; trop
  longue, la fenêtre d'exécution différée s'allonge d'autant.
- **Allocation définitive des `typeID` Warp** (4, 5, 6, 7 proposés) et de
  l'**adresse du précompile** dans la plage `0x0200…`.
- **Calibrage du coût en gas du précompile** — sans objet tant que le lot 11 est
  différé, et la première question à rouvrir s'il revient. Cible : parité
  d'ordre de grandeur avec un `Export` atomique équivalent, à `baseFee` égale.
- **Bench** : latence à froid de `GetCanonicalValidatorSetFromChainID` à
  l'échelle du Primary Network, adéquation du forfait `intrinsicWarpDBReads`, et
  coût d'écriture dans le trie atomique rapporté à celui d'un `SSTORE`.
- **Financer un tiers depuis un contrat** : question du lot 11, donc différée
  avec lui. L'adresse source y étant forcée à l'appelant, le cas « factory qui
  approvisionne ses coffres » resterait sans réponse native.
- **Réintégrer le lot 11, et quand.** Le différer est la recommandation de ce
  plan, pas une exclusion définitive. La question se rouvre une fois les lots
  1-10 livrés et le chemin EOA mesuré.

## Le staking auto-renouvelé, inclus

Les trois transactions de staking introduites par Helicon sont **dans le
périmètre**. C'est ce qui permet à un contrat de staker indéfiniment sans
opérateur — le cas d'usage qui justifie le mieux toute la proposition.

| Transaction | Rôle | Traitement |
| :--- | :--- | :--- |
| `AddAutoRenewedValidatorTx` | ouvre le validateur | liste autorisée (lot 4d) |
| `SetAutoRenewedValidatorConfigTx` | le pilote, et **l'arrête** (`Period = 0`) | liste autorisée **+ permission** |
| `RewardAutoRenewedValidatorTx` | verse le cycle | proposition, aucune autorisation ; attestée par `CycleSettled` (lot 7) |

Trois faits du code qui rendent cela possible, et que le plan affirmait
autrement :

- **Un validateur auto-renouvelé sort de l'ensemble.**
  `SetAutoRenewedValidatorConfigTx.Period = 0` veut dire *« stop at the end of the
  current cycle and unlock funds »*, et le `RewardAutoRenewedValidatorTx` fait
  alors `DeleteCurrentValidator` sur la branche commit
  (`proposal_tx_executor.go:475`) ; la branche abort le retire dans **tous** les
  cas (l.448).
- **Les récompenses ne sont pas indexées sous le `txID` de staking.**
  `mintRewards` écrit `AddRewardUTXO(e.tx.ID(), utxo)` — le `txID` du
  `RewardAutoRenewedValidatorTx` du cycle. `StakeSettled` ne peut donc pas les
  nommer, d'où `CycleSettled`.
- **`ValidatorAuthority` est un `fx.Owner`**, vérifié par `fx.VerifyPermission`.
  C'est la seule porte de sortie du stake, donc `warpfx` doit pouvoir y prouver
  son assentiment : d'où `VerifyPermissionWithContext`.

> ⚠️ **Le refus côté subnet reste structurel, et il faut le garder ainsi.**
> `verifySubnetAuthorization` emprunte le point d'entrée **sans** contexte, donc
> un `*warpfx.Owner` nommé propriétaire de subnet continue de tomber sur
> `ErrPermissionUnsupported`. Ce n'est pas le type du propriétaire qui distingue
> les deux cas, c'est le point d'entrée que l'appelant emprunte. Router
> `verifySubnetAuthorization` vers la variante contextuelle rouvrirait le subnet
> ingérable que le lot 9.1 refuse.

---

## Deux règles à ajouter à l'ACP

Absentes de l'ACP, découvertes en confrontant le plan au code. Elles ne changent
pas l'architecture : elles ferment deux portes qu'aucun lot ne fermait, et
**toutes deux sont tranchées — on refuse.**

| Règle | Pourquoi elle manquait | Où |
| :--- | :--- | :--- |
| Une sortie `warpfx` ne peut pas être enveloppée dans un `stakeable.LockOut` → `ErrWarpOutputNotLockable` | Le champ `TransferableOut` est une interface, et `VerifySpendUTXOs` autorise de produire du verrouillé depuis des fonds déverrouillés. Le `Locktime` que le lot 1 refuse revient donc par la bande, évalué contre l'**horloge locale**. N'importe qui peut l'infliger à n'importe quel `WarpOwner`, recevoir étant libre. | [6.3](06-gardes.md) |
| Un `warpfx.Owner` ne peut pas être propriétaire de subnet → `ErrWarpOwnerCannotOwnSubnet` | `CreateSubnetTx.Owner` est typé `fx.Owner`. Aujourd'hui la transaction échoue au calcul de frais ; le lot 9 la rend tarifable, donc acceptable — et le subnet devient définitivement ingérable, `VerifyPermission` refusant toujours. | [9.1](09-tarification.md) |
