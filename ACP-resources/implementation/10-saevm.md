# Lot 10 — Volet saevm

[← Lot 9](09-tarification.md) · [Plan](README.md) · [Lot suivant : le précompile →](11-precompile.md)

---

## Pourquoi ce lot existe

Sans volet C-Chain, la proposition est inerte : aucun flux entrant ni sortant
n'existe. Mais avant d'écrire une ligne, il faut savoir **quel VM** on programme,
et la réponse n'est pas celle que l'ACP donne.

`node/node.go:1248` enregistre la C-Chain comme un `transitionvm` dont la
fabrique pré-transition est coreth et la post-transition `vms/saevm/cchain`, la
bascule ayant lieu **dix secondes avant `HeliconTime`**. L'ACP visant Helicon,
les règles qu'elle écrit pour coreth portent sur un VM qui ne tourne jamais
pendant que `warpfx` est licite. **Après Helicon, la C-Chain est saevm.**

`vms/saevm/cchain/tx/` est une réécriture du même mécanisme, pas une architecture
différente, et les invariants sur lesquels la proposition s'appuie tiennent tous :
une sortie exportée est un `avax.TransferableOutput` ; le trait est posé si la
sortie est `Addressable` ; l'import appelle un Fx par input ; ce Fx est un
`secp256k1fx` en dur ; le crédit se fait sans exécution de code. Le point de
court-circuit existe donc **au même endroit et sous la même forme**.

Trois choses changent, et il faut les avoir en tête avant de commencer.

**1. Le modèle de vérification.** saevm ne vérifie pas un bloc reçu champ par
champ : il le **reconstruit et compare les empreintes** (`sae/blocks.go`,
`VerifyBlock` → `rebuild` → `errHashMismatch`). Un bloc qu'un constructeur
honnête n'aurait pas produit ne se reconstruit pas à l'identique.

> **Toute règle du constructeur est une règle de consensus.**

C'est la propriété structurante de saevm, et elle a deux conséquences qu'un
lecteur venant de coreth n'anticipe pas. D'abord, ⚠️ **`EndOfBlockOps` ne vérifie
rien** — le lire seul induit en erreur : il se contente d'un `tx.ParseSlice` puis
`t.AsOp()`. `SanityCheck` et `VerifyCredentials` ne sont appelées que dans
`builder.PotentialEndOfBlockOps`, atteinte à la vérification **par le
reconstructeur** (`rebuild` → `BlockRebuilderFrom` → le builder ensemencé des
transactions du bloc lui-même). Ensuite, le contrôle de prix que le constructeur
applique aux opérations est, lui aussi, une règle de consensus — c'est ce qui rend
la borne d'enchère du 10d énonçable là où elle est posée.

**2. Le modèle de frais.** Chez coreth, une transaction atomique doit brûler *au
moins* un frais calculé à la vérification : le frais est un **plancher de
validité**. Chez saevm, le montant brûlé n'est pas un frais mais une
**enchère** — converti en prix par gas offert, c'est le mécanisme de frais de SAE
qui décide de l'inclusion. Cela change la forme de la fourchette du lot 5, et pas
dans le sens qu'on attend (voir 10d).

**3. Le décalage du codec.** saevm n'enregistre ni `secp256k1fx.Input` ni
`OutputOwners`, que coreth réserve. Le `SkipRegistrations` n'est donc pas le
même, et un test d'alignement écrit contre coreth ne dit **rien** de saevm.

Enfin, **aucune garde d'activation n'est nécessaire ici** — pour une raison qui
n'est pas « saevm est le VM post-Helicon » (il est installé dix secondes avant).
Voir 10e.

---

## 10a. Aligner le codec atomique

Le décompte d'abord, comme au lot 2 : c'est la seule façon d'être sûr.

**Où** : `vms/saevm/cchain/tx/codec.go`.

**Le décompte actuel** : `Import`→0, `Export`→1, `SkipRegistrations(3)`→2-4,
`secp256k1fx.TransferInput`→5, `SkipRegistrations(1)`→6,
`secp256k1fx.TransferOutput`→7, `SkipRegistrations(1)`→8,
`secp256k1fx.Credential`→9. **Dernier index occupé : 9.**

Les positions 5, 7 et 9 coïncident volontairement avec celles du codec de la
PlatformVM (lot 2.1) : c'est tout l'objet des `SkipRegistrations` déjà présents,
et le commentaire du fichier (l.27-29) le dit.

**Quoi** — pour amener `warpfx.TransferOutput` sur **44**, il faut sauter de 10 à
43, soit :

```go
lc.SkipRegistrations(34)
errs.Add(lc.RegisterType(&warpfx.TransferOutput{}))   // → 44
```

**Seul ce type y figure.** Un `Owner` n'apparaît qu'embarqué dans la sortie, donc
sans identifiant propre ; et un credential `warpfx` n'a pas cours sur le chemin
atomique — voir 10c, où le credential vide est un `secp256k1fx.Credential`.

⚠️ **Ce décalage n'est pas celui de coreth.** Le codec atomique de coreth s'arrête
à 11 (il enregistre `secp256k1fx.Input` et `OutputOwners`) et demanderait donc
`SkipRegistrations(32)`. Ce plan ne touche pas coreth, mais si tu lis un jour du
code qui parle de 32, ce n'est pas une erreur — c'est l'autre VM.

**Le test, qui comble un trou réel** :

> ⚠️ **Il n'existait aucun test d'alignement entre saevm et la PlatformVM.**
> Aucun fichier sous `vms/saevm/` n'importait `vms/platformvm/txs` ; la seule
> occurrence du mot « typeID » dans tout `vms/saevm/` était le commentaire de
> `codec.go`. La garantie reposait sur ce commentaire et sur les tests de
> compatibilité binaire avec **coreth**. Rien n'aurait cassé automatiquement si
> un `RegisterType` avait décalé `TransferInput` / `TransferOutput` /
> `Credential` par rapport à la PlatformVM.
>
> ✅ `TestCodecAlignedWithPlatformVM` comble ce trou, et couvre **aussi** le cas
> `secp256k1fx` — l'invariant que le commentaire prétendait garantir depuis
> toujours.

Écris `TestWarpCodecAlignedWithPlatformVM` : construis un `avax.UTXO` portant un
`warpfx.TransferOutput`, sérialise-le avec `tx.MarshalUTXO` (saevm) et avec
`platform.Codec.Marshal(platform.CodecVersion, utxo)` (PlatformVM), et compare les octets.
Profite-en pour faire de même sur un UTXO `secp256k1fx` — c'est l'invariant que le
commentaire prétend garantir depuis toujours.

**Vérifier** : `go test ./vms/saevm/cchain/tx/ -run Codec`

---

## 10b. Forcer la destination d'export

Le symétrique du lot 6.1, côté C-Chain. Deux lignes, mais l'endroit compte : le
refus doit intervenir **avant tout débit**.

**Où** : `vms/saevm/cchain/tx/export.go`, `Export.sanityCheck` (l.116).

**Quoi** : dans la boucle sur `e.ExportedOutputs`, dès qu'une sortie est un
`*warpfx.TransferOutput`, exiger
`e.DestinationChain == constants.PlatformChainID`, sinon
`errWarpOutputWrongDestination`.

`sanityCheck` est appelée par le txpool à l'admission (`txpool.go:188`) **et** par
`PotentialEndOfBlockOps` à la construction (`hooks.go:469`) — donc, par le modèle
reconstruire-et-comparer, à la vérification. C'est le bon endroit.

**Ce qu'il n'y a rien d'autre à faire à l'export.** `Export.verifyCredentials`
(l.174) est une récupération de clé secp **en dur**, sans dispatch de Fx : un
export `warpfx` reste signé par le détenteur du solde EVM débité, comme n'importe
quel autre. Et les traits partent gratuitement — `atomicRequests` (l.254) fait
déjà :

```go
if o, ok := utxo.Out.(avax.Addressable); ok {
    elem.Traits = o.Addresses()
}
```

…ce qui pose la `SourceAddress` du propriétaire comme trait dès lors que le
lot 1 a implémenté `Addresses()`. Vingt octets, donc lisibles par
`avax.GetAtomicUTXOs` sans aucune adaptation (lot 8).

> ⚠️ **Conséquence remarquable, et qui vaut d'être dite : pour un EOA, alimenter
> un `WarpOwner` ne demande aucune modification du consensus C-Chain.**
> L'EOA signe son export atomique avec sa clé, comme aujourd'hui, et désigne un
> `WarpOwner` comme propriétaire de la sortie. Il peut d'ailleurs désigner **une
> adresse tierce**, ce qui permet de financer un contrat sans que celui-ci ait à
> agir. C'est ce chemin que le lot 12 doit tester en premier — il fonctionne sans
> le précompile du lot 11.

**Vérifier** : test 11 du lot 12 — une sortie `warpfx` vers la X-Chain refusée.

---

## 10c. La règle miroir à l'import

C'est le pendant exact du lot 5, avec les mêmes pièges et une différence
d'encodage qu'il ne faut pas rater.

**Où** : `vms/saevm/cchain/tx/warp_canonical.go` *(nouveau)*, appelé depuis
`Import.verifyCredentials` (`import.go:157`).

**Quoi** — la boucle actuelle désérialise et vérifie input par input. Restructure :

1. désérialiser **tous** les UTXOs d'abord (la boucle actuelle le fait déjà, mais
   entrelacé avec la vérification) ;
2. `isCanonicalImport(utxos)` — tous les UTXOs sont des `*warpfx.TransferOutput` ;
3. si oui → `verifyCanonicalImport(...)` **et court-circuit de la boucle** ;
4. sinon → la boucle ordinaire `fx.VerifyTransfer(fxTx, in.In, creds[j], utxo.Out)`,
   inchangée.

`verifyCanonicalImport` vérifie :

| Condition | Erreur |
| :--- | :--- |
| tous du même `WarpOwner` | `errCanonicalMixedOwners` |
| `Owner.SourceChainID == ctx.ChainID` | `errCanonicalWrongSource` |
| `in.In.Amount() == out.Amt` pour chaque input | `errCanonicalAmount` |
| `len(i.Outs) == 1` | `errCanonicalOutputCount` |
| cette sortie est en AVAX | `errNonAVAXOutput` (déjà couvert par `sanityCheck`) |
| `Outs[0].Address == Owner.SourceAddress` | `errCanonicalWrongOutput` |
| credential vide pour chaque input | `errCanonicalCredential` |

> ⚠️ **Le credential vide est ici un `secp256k1fx.Credential` sans signature, pas
> un `warpfx.Credential`.**
> C'est le seul encodage de « cet input ne présente rien » sur lequel les deux
> codecs s'accordent : l'interface `tx.Credential` de saevm est
> `Self() *secp256k1fx.Credential` (`tx.go:103`), et `warpfx.Credential` n'est
> même pas enregistré dans le codec atomique (10a). Et c'est ce qui garde la
> transaction **byte-identique** pour deux soumetteurs qui la reconstruisent
> indépendamment — donc déduplicable par le mempool.
>
> Côté P-Chain (lot 5), le credential vide est au contraire un
> `warpfx.Credential{}`. Les deux sont corrects, chacun chez soi. Ne les confonds
> pas.

> ⚠️ **Le court-circuit lit la forme du lot entier, jamais l'absence de
> signature.**
> Un lot mêlant une sortie `warpfx` et une sortie signée n'est pas canonique : il
> retombe sur la boucle ordinaire, où la première atteint `secp256k1fx` et se
> fait refuser sur `ErrWrongUTXOType`. **Échouer dans ce sens est l'intérêt même
> de la règle.** Test 10 du lot 12.

**Rendre la canonicité à l'appelant.** `verifyCredentials` doit **retourner** le
fait que le lot était canonique (et de préférence l'`Owner`), au lieu de laisser
l'appelant le recalculer. Le 10d en a besoin pour borner l'enchère, et
déterminer la canonicité demande de lire les UTXOs consommés — une seconde
lecture de shared memory pour apprendre ce qu'on sait déjà serait du travail et
**une source de divergence**.

La signature change donc deux fois, et il vaut mieux le savoir avant de
commencer :

| Aujourd'hui | Ce qu'il faut |
| :--- | :--- |
| `Unsigned.verifyCredentials(sm, creds) error` (`tx.go:82`) | `+ ctx *snow.Context`, `→ (canonical bool, err error)` |
| `Tx.VerifyCredentials(sm) error` (`tx.go:246`) | idem |

Le `snow.Context` est indispensable : la règle canonique compare
`Owner.SourceChainID` à `ctx.ChainID`, et cette méthode ne reçoit aujourd'hui
qu'une `SharedMemory`. `Export.verifyCredentials` doit suivre le même
élargissement sans rien en faire.

⚠️ **Les deux appelants sont nommés, et il n'y en a que deux** :
`vms/saevm/cchain/txpool/txpool.go:219` (admission au mempool) et
`vms/saevm/cchain/hooks.go:481` (`PotentialEndOfBlockOps`). C'est **le second**
qui a besoin de la valeur de retour, pour appliquer la borne d'enchère du 10d
juste après.

⚠️ **Le crédit se fait sans exécution de code**, ni `receive()` ni `fallback`,
comme un `selfdestruct` : `Import.asOp` produit une map `Mint` appliquée hors
EVM. La comptabilité d'un contrat destinataire doit donc fonctionner en mode
*pull* — il ne sera pas notifié, il doit constater. Un solde EVM étant additif,
la contrainte de sortie unique ne coûte en revanche rien de ce côté, contrairement
au sens P→C où chaque import produit un UTXO de plus.

**Note de forme** : `Import.sanityCheck` impose déjà que tous les inputs et
sorties soient de l'AVAX (l.122, l.131) — ce que la version coreth ne faisait
qu'à partir de Banff. La contrainte d'asset de la forme canonique y est donc
**redondante**. Garde-la pour la lisibilité, pas pour la sûreté.

⚠️ **Tout nouvel `errXxx` doit être ré-exporté dans
`vms/saevm/cchain/tx/identifiers_test.go`** — les tests vivent dans le paquet
externe `tx_test` et ne voient pas les identifiants privés.

**Vérifier** : `go test ./vms/saevm/cchain/tx/`

---

## 10d. La borne d'enchère

C'est le point qui demande une décision plutôt qu'une transposition, et le seul
endroit où le volet saevm diverge vraiment de ce que l'ACP décrit pour coreth.

### Ce qui change, et pourquoi

**Chez coreth**, une transaction atomique doit brûler *au moins*
`CalculateDynamicFee(GasUsed, baseFee)`, produit dans le flow check. Le frais est
un **plancher de validité** : brûler trop peu rend la transaction invalide. La
fourchette du lot 5 a donc ses deux bornes — la haute est le flow check, la basse
est la règle anti-destruction.

**Chez saevm**, `Import.sanityCheck` ne produit **aucun** terme de frais : le
flow check est simplement `produced ≤ consumed`. Le montant brûlé
(`Σ inputs − Σ sorties`) est converti en **prix par gas offert** :

```go
// Tx.AsOp, tx.go:159
GasFeeCap: gasPrice(burned, gas),   // ScaleAVAX(burned) / gas
```

…et c'est le mécanisme de frais de SAE qui décide de l'inclusion.

| | coreth | saevm |
| :--- | :--- | :--- |
| Brûler trop peu | **Invalide** | **Valide**, mais l'enchère ne passe pas : jamais incluse |
| Brûler tout | Valide | Valide, et incluse **immédiatement** |

**La borne haute disparaît** : elle était le flow check majoré du frais, et saevm
ne majore rien. Ce qu'elle protégeait — qu'une transaction couvre son coût — est
déjà assuré, et par une règle de consensus : `o.GasFeeCap.Lt(s.baseFee)` →
`core.ErrFeeCapTooLow` (`worstcase/state.go:298`).

> ⚠️ **Le déplacement rend l'attaque plus attrayante, pas moins.**
> Là où le brûlage est un frais, tout brûler est de la destruction pure, sans
> bénéfice pour son auteur. Là où il est une enchère, tout brûler **maximise la
> priorité d'inclusion** : un soumetteur pressé y a un **intérêt direct**, et le
> mécanisme de frais le sert en priorité. La borne basse n'est donc pas une
> précaution théorique de ce côté — c'est la seule chose qui empêche un tiers de
> détruire les fonds du propriétaire pour accélérer sa propre transaction.

### Où la poser

> **Règle.** `VerifyCanonicalBid(op.GasFeeCap, building.BaseFee, minimumBid)` →
> `ErrBidTooHigh` au-delà de `k × baseFee`, **jamais en dessous de l'enchère
> minimale exprimable**.

### Le plancher, sans lequel la règle est insatisfiable

⚠️ **Ce paragraphe vient du PoC, et il corrige la règle ci-dessus plutôt qu'il ne
la commente.** Écrite sans plancher, elle n'est pas seulement serrée : elle est
**impossible à satisfaire au prix plancher de la chaîne**, et le PoC échoue
dessus.

Le brûlage est **quantifié** : le plus petit qu'un import canonique puisse
exprimer est **1 nAVAX**, ce qui, étalé sur les ~10 300 de gas d'un import à un
input, donne déjà une enchère de ~**97 000 aAVAX/gas**. Le `baseFee`, lui, n'est
pas quantifié — et sous ACP-283 le prix minimum du gas C-Chain **démarre à
1 wei** (`dynamic.InitialPriceExponent = 0`) et y revient dès que la chaîne est
inactive.

`bid ≤ k × baseFee` avec `k = 2` place donc le plafond à **2 aAVAX/gas**, soit
**quatre à cinq ordres de grandeur sous l'enchère la moins chère qu'un
soumetteur puisse formuler**. Aucun import canonique n'est incluable — et il ne
l'est pas précisément quand la chaîne est la moins chère.

D'où le troisième paramètre : le plafond ne descend jamais sous
`ScaleAVAX(1) / op.Gas`, l'enchère qu'un brûlage d'un quantum produit. Le
constructeur le calcule depuis le gas de l'opération, qu'il tient déjà.

**Ce que le plancher ne concède pas.** Au plancher, un tiers peut brûler **un
quantum**, soit 1 nAVAX du propriétaire. C'est le minimum indivisible : la borne
ne pouvait pas protéger en-deçà.

> 🔴 **Le plancher est une règle de consensus au même titre que le plafond qu'il
> répare, et il est à reporter dans l'ACP.** Il ne tranche pas `k` pour autant :
> le PoC montre que le plancher est nécessaire, pas que 2 est juste.

**Où** : `vms/saevm/cchain/hooks.go`, `builder.PotentialEndOfBlockOps` (l.438),
juste après `t.tx.VerifyCredentials(...)` et avant le `yield(t)`.

Trois raisons désignent cet endroit, et la troisième est décisive :

1. **C'est le seul endroit qui connaisse à la fois le type et le prix.**
   `sanityCheck(ctx)` et `verifyCredentials(sm)` ne reçoivent pas le `baseFee`.
   `PotentialEndOfBlockOps` reçoit un `building *types.Header` dont le `BaseFee`
   est déjà posé par `worstcase.State.StartBlock` (`state.go:128,137`) depuis la
   **même horloge à gas** que le plancher utilise.
2. **Les deux bornes se répondent.** Elles portent sur la même grandeur,
   `o.GasFeeCap`, contre la même référence, `s.baseFee`. Une borne exprimée
   contre un frais absolu n'aurait pas cette propriété.
3. **Elle ne doit pas s'appliquer aux transactions EVM ordinaires.**
   `worstcase.State.ApplyTx` convertit une transaction EVM en opération puis
   appelle `s.Apply` — qui est donc le chemin **commun**. Un plafond posé là
   changerait le comportement de transactions dont l'auteur peut légitimement
   surpayer.

Et c'est le modèle reconstruire-et-comparer qui fait de cette règle du
constructeur une règle de consensus : un bloc contenant une opération dont
l'enchère dépasse le plafond ne se reconstruit pas, donc son empreinte diverge.

**Vérifier** : test 6 du lot 12, côté saevm — un aAVAX au-delà de `k × baseFee`,
et la symétrie explicite avec le plancher.

---

## 10e. Pourquoi il n'y a aucune garde d'activation ici

Rien à écrire. Mais c'est un raisonnement à comprendre, parce qu'il est le
fondement d'une **absence** — et qu'une absence non justifiée finit toujours par
être « corrigée » par quelqu'un.

La transition n'a pas lieu à `HeliconTime` mais **dix secondes avant**
(`node/node.go:1248`). Le décalage est délibéré : coreth impose un temps de bloc
minimal, et il faut lui garantir de pouvoir construire le bloc de transition
avant `HeliconTime`. Il ouvre donc une fenêtre où saevm est bien le VM installé
alors que l'upgrade n'est pas encore actif. **Un raisonnement fondé sur « saevm
ne tourne qu'après Helicon » serait faux.**

La garde existe pourtant, un niveau au-dessus, et elle est structurelle :

```go
// builder.BuildHeader, hooks.go:379-383 — première ligne
if !b.ctx.NetworkUpgrades.IsHeliconActivated(now) {
    return nil, errHeliconUnactivated
}
```

`buildWithTxs` appelle `BuildHeader` avant toute autre chose, et **`rebuild`
emprunte le même chemin** : un bloc horodaté avant l'activation ne se reconstruit
pas, donc n'est pas acceptable. `BlockRebuilderFrom` (`hooks.go:118-124`) fixe
d'ailleurs le `now` du reconstructeur à l'horodatage du bloc examiné, si bien que
la question posée à la vérification est **exactement** celle posée à la
construction.

> **Le premier bloc saevm est donc nécessairement à ou après `HeliconTime`.**
> Aucun `warpfx.TransferOutput` ne peut y naître avant, et aucune garde
> temporelle n'a lieu d'être dans le volet C-Chain — ni à l'export, ni à
> l'import.

Le test `TestPreHeliconBlocksDisallowed` (`vms/saevm/cchain/vm_test.go:1899`)
verrouille déjà ce comportement : `BuildBlock` et `VerifyBlock` rendent
`errHeliconUnactivated`, mais `ParseBlock` réussit encore. C'est le test 12 du
lot 12 — il existe, il faut juste savoir que c'est **lui** qui porte la dispense.

**Aucun changement de tarification non plus** : `gasUsed` (`tx.go:176`) compte des
octets et des signatures, sans connaître les types. Un nouveau type de sortie n'y
demande rien — contrairement au calculateur de complexité de la PlatformVM
(lot 9), qui dispatche par type et refuse ce qu'il ne connaît pas.

---

## Ce qui reste incertain

🔴 **Ouvert**

- **La valeur de `k` côté saevm.** `k = 2` est repris du lot 5, mais ici il borne
  une **enchère**, donc un multiple du prix courant du gas, et non un frais
  absolu. Une seule constante est retenue, `warpfx.MaxFeeOverpaymentFactor`,
  parce que l'écart va dans le **sens sûr** : le prix du gas C-Chain bouge
  nettement plus lentement que celui de la P-Chain, donc un facteur 2 y achète
  une fenêtre plus longue pour la même tolérance de destruction. La scinder est
  une décision de calibrage, et il n'y a rien contre quoi calibrer pour l'instant.
- **Le plancher d'enchère est à reporter dans l'ACP** (voir 10d). C'est une règle
  de consensus, découverte en exécutant.

✅ **Vérifié**

- **Le `SkipRegistrations(34)`** — confirmé empiriquement, pas seulement
  recompté : `TestCodecAlignedWithPlatformVM` sérialise le même UTXO des deux
  côtés et compare les octets. Il reste dépendant des positions du lot 2 : si
  43/44/45 bouge, ce nombre bouge avec, et `TestWarpUTXOsCodecTypeIDs` le dit
  d'abord.
- **`building.BaseFee` est bien posé** quand `PotentialEndOfBlockOps` est
  appelée : `buildWithTxs` pose `hdr.BaseFee` **avant** la boucle des
  end-of-block ops. La prémisse de tout le 10d tient.
- **L'élargissement de `verifyCredentials`** : `(ctx *snow.Context, sm, creds) →
  (canonical bool, err error)` sur l'interface `Unsigned`, donc sur `Export`
  aussi, qui rend `false`. Le paramètre `sm` est **gardé séparé** plutôt que tiré
  du contexte : les tests du paquet construisent leur propre `SharedMemory` sans
  contexte, et les fusionner aurait forcé une réécriture pour rien.
- **`ctx.ChainID` est bien l'ID de la C-Chain** : `Import.sanityCheck` compare
  déjà `i.BlockchainID != ctx.ChainID`.

🟡 **À mesurer**

- Rien dans ce lot. La tarification saevm ne change pas ; celle du précompile,
  si — voir [lot 11](11-precompile.md).
