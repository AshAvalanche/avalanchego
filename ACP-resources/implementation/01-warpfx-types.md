# Lot 1 — Le paquet `vms/warpfx`

[← Plan](README.md) · [Lot suivant : les codecs →](02-codecs.md)

---

## Pourquoi ce lot existe

Sur la P-Chain, un UTXO ne porte pas une adresse : il porte une **condition de
dépense**. Aujourd'hui il n'en existe qu'une seule forme, celle de
`secp256k1fx` — « présente `threshold` signatures parmi ces `Addrs` ». Toute la
proposition tient dans l'ajout d'une seconde forme : « présente un message Warp
signé par un quorum de la chaîne source, qui s'engage exactement sur ces
octets-ci de transaction ».

Ce lot écrit cette seconde forme, et **rien d'autre**. À la fin, les types
existent, ils se sérialisent, ils se vérifient en isolation — mais aucune
transaction de la P-Chain ne peut encore les atteindre. C'est voulu : les lots 2
et 3 branchent la plomberie, et tant qu'elle n'est pas branchée un UTXO `warpfx`
est simplement indépensable. Un état inerte est un bon état intermédiaire ;
l'inverse (un type atteignable mais mal vérifié) serait un désastre.

Le paquet vit à **`vms/warpfx`**, à côté de `vms/secp256k1fx`, et **pas** sous
`vms/platformvm/`. La raison est concrète : saevm devra enregistrer le type de
sortie dans son propre codec atomique (lot 10), et il ne peut pas importer un
paquet interne à la PlatformVM. C'est exactement la position qu'occupe déjà
`secp256k1fx`, partagé entre la X-Chain, la P-Chain et la C-Chain.

Deux propriétés du paquet portent l'essentiel de la sûreté du design, et il vaut
mieux les avoir en tête avant d'écrire la première ligne :

1. **La structure `Fx` ne retient aucune autorisation.** Elle porte le `VM` que
   lui donne `Initialize`, et rien d'autre : pas de cache, pas de dernière
   autorisation vue. C'est la parade structurelle à la principale surface de bug
   à consensus de la proposition — une autorisation rémanente qui validerait la
   transaction *suivante*, celle qu'elle n'a jamais approuvée.
2. **`VerifyTransfer` sans contexte échoue toujours.** L'ancienne méthode de
   l'interface `fx.Fx` n'est pas implémentée « au mieux » : elle refuse. Un
   chemin de vérification qu'on aurait oublié de router correctement **ferme**
   une porte au lieu d'en ouvrir une. Ce choix rend une omission bruyante plutôt
   que silencieuse, et c'est le contraire du réflexe habituel.

---

## Ordre d'écriture recommandé

`owner.go` → `transfer_output.go` → `credential.go` → `authorization.go` →
`fee.go` → `fx.go`.

Le Fx en dernier : il assemble tout le reste, et l'écrire d'abord te ferait
travailler contre des types qui n'existent pas encore.

---

## 1.1 `owner.go` — l'identité

Le `WarpOwner` remplace la paire « seuil + liste d'adresses » de `secp256k1fx`
par une paire `(chaîne source, adresse source)`. Ce n'est pas une adresse : il
n'a pas de représentation Bech32 et ne peut pas figurer dans une liste de
destinataires secp. C'est une entité identifiée par ses données, au même sens
qu'un `validationID` de l'ACP-77.

**Où** : `vms/warpfx/owner.go` *(nouveau)*.

**Quoi** :

```go
const AddressLen = ids.ShortIDLen // 20

type Owner struct {
    verify.IsNotState `json:"-"`

    SourceChainID ids.ID `serialize:"true" json:"sourceChainID"`
    SourceAddress []byte `serialize:"true" json:"sourceAddress"`
}
```

- `Verify() error` — `SourceChainID` non vide, et `len(SourceAddress)` **exactement**
  `AddressLen`. Erreurs nommées (`ErrEmptySourceChain`, `ErrWrongAddressLength`).
- `InitCtx(*snow.Context)` — no-op, mais requis par `snow.ContextInitializable`.
- `ID() ids.ID` — `hashing.ComputeHash256Array(warpfx.Codec.Marshal(CodecVersion, o))`.

Le tout doit satisfaire `vms/platformvm/fx.Owner`, qui est
`verify.IsNotState + verify.Verifiable + snow.ContextInitializable`. Ajoute
l'assertion de compilation `var _ fx.Owner = (*Owner)(nil)`.

> ⚠️ **`verify.IsNotState` s'*embarque*, il ne s'*implémente* pas — et écrire la
> méthode à la main ne compile pas.**
> Ouvre `vms/components/verify/verification.go` : `IsState` et `IsNotState` sont
> deux **interfaces**, `isState()` et `isState() error`. Le marqueur se pose en
> embarquant l'interface comme champ (valeur nil, jamais appelée), exactement
> comme le fait `secp256k1fx.OutputOwners`.
>
> La raison tient à la profondeur de promotion des méthodes en Go, et elle est
> invisible tant qu'on n'a pas essayé. `TransferOutput` (§1.3) embarque
> `verify.IsState` **et** `Owner`. Si `Owner` **embarque** `verify.IsNotState`,
> le `isState()` de `IsState` est promu à la profondeur 1 et le
> `isState() error` d'`IsNotState` à la profondeur 2 : le plus court gagne,
> `*TransferOutput` satisfait `verify.State`. Si `Owner` **déclare** au contraire
> une méthode `isState() error`, les deux sont à la profondeur 1, le sélecteur
> devient **ambigu**, aucune des deux n'est promue, et `*TransferOutput` ne
> satisfait plus **rien** : `var _ verify.State = (*TransferOutput)(nil)` ne
> compile pas, avec un message qui parle d'une méthode manquante et non d'une
> ambiguïté.

**Pourquoi 20 octets exactement.** Le champ pourrait être de longueur libre. Il
ne l'est pas parce que le propriétaire doit pouvoir **recevoir** ses fonds au
retour : l'import C-Chain crédite un `tx.Output.Address`, qui est un
`common.Address` de 20 octets. Une sortie dont le propriétaire porterait 32
octets serait parfaitement constructible — n'importe qui peut alimenter n'importe
quel `WarpOwner` — mais **définitivement non importable**.

> ⚠️ **Ce qui se passe si tu relâches la contrainte.**
> Des fonds bloqués sans recours, sans aucun message d'erreur au moment où le
> mal est fait. La sortie se crée, l'UTXO existe, la P-Chain le voit — et le
> jour où on veut le ramener sur la C-Chain, il n'y a pas d'adresse EVM à
> créditer. La contrainte est gratuite dans ce périmètre ; elle sera relâchée en
> même temps que le périmètre s'élargira.

**Vérifier** : un test qui construit un `Owner` à 19, 20 et 21 octets et attend
`nil` uniquement au milieu.

---

## 1.2 Pas de codec local, et pas d'`ownerID`

Il y a une version de ce paquet où `Owner` porte une méthode `ID()` qui hache sa
sérialisation, et où un `codec.go` local existe pour produire ces octets. **Ce
n'est pas celle-ci**, et le détour vaut d'être expliqué, parce que l'idée revient
naturellement dès qu'on cherche « une clé pour désigner un propriétaire ».

**Le besoin réel.** Un UTXO doit être **retrouvable** par son propriétaire :
`avax.Addressable.Addresses()` fournit les clés sous lesquelles il sera indexé,
côté P-Chain comme en shared memory. La question est donc seulement : quelle clé ?

**La réponse : `SourceAddress`, tel quel.** Vingt octets, déjà présents dans la
structure, sans transformation.

> **Le trait n'est jamais une donnée de consensus.** `utxoState.updateChecksum`
> ne hache que l'`utxoID`, jamais les clés d'index ; `chains/atomic` type ses
> `Traits [][]byte` sans contrainte de longueur ; et la provenance
> (`Authorizes`) lit toujours l'`Owner` **complet** depuis l'UTXO, jamais le
> trait. Un trait est un **indice de découverte**, et rien d'autre.

**Ce que ce choix supprime**, et c'est la raison de le faire :

- **Un codec local et sa condition de validité.** Un hash d'`Owner` doit être
  identique chez trois lecteurs qui ne partagent aucun codec (P-Chain, saevm,
  outillage). Cela tient tant qu'`Owner` ne contient aucun champ typé par une
  interface — un `linearcodec` n'écrit un identifiant de type que devant
  ceux-là. C'est vrai aujourd'hui, invisible dans la structure, et cassé
  silencieusement au premier champ ajouté. Il fallait
  `TestOwnerHasNoSerializedInterfaceField` pour verrouiller une précondition que
  personne ne penserait à relire.
- **Une reproduction de codec en Solidity.** Un contrat qui reçoit un
  `StakeSettled` (lot 7) doit vérifier que l'attestation le concerne. Avec un
  `ownerID`, cela veut dire recalculer
  `sha256(version‖chainID‖len‖addr)` en Solidity — reproduire un codec Go dans un
  autre langage, exactement le genre de chose qui casse en silence. Avec la paire
  brute dans le message : `sourceAddress == address(this)`.
- **Toute l'adaptation de la découverte.** `avax.GetAtomicUTXOs` type ses traits
  en `set.Set[ids.ShortID]` — 20 octets — et `ParseServiceAddress` rend un
  `ids.ShortID`. Un trait de 32 octets obligeait à généraliser l'accesseur et à
  écrire une API dédiée ; un trait de 20 octets **passe dans la machinerie
  existante sans une ligne** (lot 8).
- **Un mode d'échec silencieux.** Une méthode `ID()` doit décider quoi faire de
  l'erreur de `Marshal`. La rendre, c'est propager une erreur dans
  `Addresses()`, qui ne peut pas en renvoyer. L'avaler, c'est retourner
  `ids.Empty` — et **indexer sous une clé nulle partagée** tout owner qui ne
  marshalle pas. Il n'y a pas de bonne réponse ; il y a une bonne question, qui
  est de ne pas se la poser.

**Ce que ce choix coûte.** Deux propriétaires ne différant que par leur
`SourceChainID` partagent un trait. C'est impossible dans ce périmètre — la
C-Chain est forcée aux deux bouts, à l'export (lot 6.1) et à l'import canonique
(lot 10c) — et si le périmètre s'élargit, la découverte rendra les deux et
l'outillage filtrera en décodant l'UTXO. Un trait n'a jamais eu à être injectif.

⚠️ **À ne pas confondre avec l'alignement des codecs du lot 2**, qui est réel et
sans rapport : il porte sur `TransferOutput`, stocké dans un champ d'interface
(`avax.TransferableOutput.Out`) et qui reçoit donc bien un identifiant de type —
lequel doit valoir la même chose côté P-Chain et côté saevm.

> ⚠️ **Si tu reprends du code déjà écrit, c'est ce qu'il faut retirer :**
> `codec.go`, la méthode `Owner.ID()`, `TestOwnerIDLayout` et
> `TestOwnerHasNoSerializedInterfaceField`. Ils sont corrects ; ils n'ont plus
> d'objet. *(Fait.)*

---

## 1.3 `transfer_output.go` — la sortie

C'est le type qui rend un UTXO « détenu par une adresse C-Chain ». Il remplace
`secp256k1fx.TransferOutput`, et il en diffère sur un point visible — pas de
`Locktime` — et un point invisible mais critique : ce qu'il déclare comme
adresses pour l'indexation.

**Où** : `vms/warpfx/transfer_output.go` *(nouveau)*.

**Quoi** :

```go
type TransferOutput struct {
    verify.IsState `json:"-"`

    Amt   uint64 `serialize:"true" json:"amount"`
    Owner        `serialize:"true" json:"warpOwner"`
}
```

Le champ `Owner` est **embarqué**, comme `OutputOwners` l'est dans
`secp256k1fx.TransferOutput` : c'est ce qui promeut `InitCtx` à la profondeur 1
et complète `verify.State` sans une ligne de plus.

Méthodes : `Amount() uint64`, `Verify() error` (`Amt != 0` puis `Owner.Verify()`),
`Owners() interface{}` (rend `&out.Owner`, pour satisfaire `fx.Owned` — c'est ce
pointeur que `Fxs.Get` résoudra au lot 3.5), et surtout :

```go
func (out *TransferOutput) Addresses() [][]byte {
    return [][]byte{out.SourceAddress}
}
```

Vingt octets, sans transformation — voir [1.2](#12-pas-de-codec-local-et-pas-downerid)
pour ce que cette absence de hachage supprime.

Assertions de compilation : `verify.State`, `avax.TransferableOut`,
`avax.Addressable`, `fx.Owned`.

**Pourquoi `Addresses()` n'est pas optionnel.** Regarde
`vms/platformvm/txs/executor/standard_tx_executor.go:489` et
`vms/saevm/cchain/tx/export.go:254` — le trait d'un élément déposé en shared
memory n'est renseigné **que si** la sortie implémente `avax.Addressable`. Une
sortie sans trait est déposée, elle reste dépensable par qui connaît son
`(txID, index)`, mais elle est **introuvable** par `sharedMemory.Indexed`, donc
par toute la découverte du lot 8.

> ⚠️ **L'oubli le plus discret du lot.**
> Rien ne casse. Aucun test de sérialisation ne le voit. Tu ne t'en aperçois
> qu'au moment où l'outillage n'arrive pas à lister les UTXOs importables, et
> alors le diagnostic part dans la mauvaise direction — on soupçonne l'API, la
> shared memory, le trait, jamais l'absence d'une méthode sur un type de sortie.

**Pourquoi pas de `Locktime`.** Trois raisons convergent, et la première est
décisive :

1. **Personne ne peut l'appliquer correctement de bout en bout.** Une sortie
   verrouillée exportée vers la C-Chain y serait évaluée sur le modèle
   `secp256k1fx`, c'est-à-dire contre le timestamp **local du nœud**, qui n'est
   pas une valeur de consensus. `warpfx` évalue au contraire ses règles
   temporelles contre le `chainTime` du bloc. Deux bases de temps pour un même
   champ est une incohérence à ne pas créer.
2. `ExportTx.SyntacticVerify` ne rejette aujourd'hui que `stakeable.LockOut` : il
   faudrait écrire une règle de rejet spécifique.
3. Un `WarpOwner` est **par construction piloté par du code** sur la chaîne
   source. Un contrat impose sa temporalité côté C-Chain, là où il a de la
   logique, du stockage et des événements.

Le locktime reste une extension compatible : un futur type de sortie pourra le
réintroduire sans toucher à celui-ci.

> 🔴 **Sauf qu'il revient par la bande, et que rien dans le plan ne l'arrête.**
> `stakeable.LockOut` porte son propre `Locktime` et embarque un
> `avax.TransferableOut` — un **champ d'interface**. Rien n'interdit d'y loger un
> `warpfx.TransferOutput` : `LockOut.Verify()` ne refuse que l'imbrication de
> deux `LockOut` et délègue le reste, et `LockOut.Addresses()` délègue aussi,
> donc l'UTXO serait même correctement indexé sous sa `SourceAddress`.
>
> Et c'est **atteignable par n'importe qui** : `VerifySpendUTXOs` autorise
> explicitement de produire du verrouillé à partir de fonds déverrouillés
> (`verifier.go`, branche `increase > unlockedConsumedAsset`). Une `BaseTx`
> ordinaire, signée en secp, peut donc créer un `warpfx.TransferOutput`
> verrouillé au profit de n'importe quel `WarpOwner` — recevoir étant libre.
>
> La conséquence est exactement celle que la raison 1 ci-dessus voulait éviter :
> à la dépense, `verifier.go` compare ce locktime à `h.clk.Time()`, **l'horloge
> locale du nœud**, alors que tout le reste de `warpfx` s'évalue contre le
> `chainTime`. Voir la décision à prendre au [lot 6.3](06-gardes.md).

**Vérifier** : `Addresses()` rend une seule clé de 20 octets égale à
`SourceAddress`, et `Verify()` refuse `Amt == 0`.

---

## 1.4 `credential.go` — le porteur du message

Le credential est le véhicule de l'autorisation. Il ne contient rien d'autre que
le message Warp complet, et il est **vide dans tous les slots sauf un** par
transaction.

**Où** : `vms/warpfx/credential.go` *(nouveau)*.

**Quoi** :

```go
type Credential struct {
    WarpMessage []byte `serialize:"true" json:"warpMessage"`
}

func (*Credential) Verify() error { return nil }
```

`Verify()` est volontairement permissif : il est appelé en boucle sur **tous**
les credentials en tête de `VerifySpendUTXOs` (`verifier.go:142`), avant qu'on
sache lequel est le porteur. Le vrai travail se fait au lot 4.

**Pourquoi le message voyage ici et pas dans la transaction.** L'ACP-77 place ses
messages Warp *dans* la transaction non signée, ce qui rend leur vérification
triviale. Impossible ici : le message **contient les octets de la transaction non
signée**. L'y placer exigerait que `txBytes` se contienne lui-même. C'est la
même impossibilité qui fait qu'une signature ne vit jamais à l'intérieur de ce
qu'elle signe. Le champ `Creds` de `platform.Tx` étant déjà typé `[]verify.Verifiable`,
aucune modification de `Tx` n'est requise.

**Pourquoi un seul exemplaire.** Répéter le message dans chaque slot serait plus
simple à décrire, mais le coût est **quadratique** : le message contient
`txBytes`, dont la taille croît avec le nombre d'inputs. Avec un
`TransferableInput` à ~88 octets, le plafond `MaxTxSize` de 64 KiB serait atteint
autour de 25 inputs, contre plusieurs centaines avec un exemplaire unique — ce
qui étranglerait la consolidation, l'outil de défense contre la fragmentation.

**Vérifier** : rien de plus qu'un round-trip de sérialisation pour l'instant.

---

## 1.5 `authorization.go` — ce qui traverse, sans jamais se poser

`Authorization` est le résultat de la résolution du lot 4, transporté jusqu'au Fx
par le contexte de la vérification en cours. Il n'est **jamais sérialisé, jamais
persisté, jamais stocké dans une structure**.

**Où** : `vms/warpfx/authorization.go` *(nouveau)*.

**Quoi** :

```go
type Authorization struct {
    SourceChainID ids.ID
    SourceAddress []byte
}

func (a *Authorization) Authorizes(o *Owner) bool
```

Pas de tag `serialize`. `Authorizes` compare `SourceChainID` et fait un
`bytes.Equal` sur `SourceAddress`.

> ⚠️ **La règle que ce type existe pour rendre lisible.**
> L'autorisation est **passée en paramètre** de la vérification et n'est
> **jamais** conservée dans la structure `Fx`. Une autorisation qui survivrait
> d'une vérification à la suivante autoriserait une transaction qu'elle n'a
> jamais approuvée : un vol silencieux, que ne révèle **aucun test unitaire écrit
> transaction par transaction**. C'est la principale surface de bug à consensus
> de toute la proposition. Le test 3 du lot 12 existe pour ça, et pour rien
> d'autre.

**Vérifier** : `Authorizes` sur des paires égales / différentes de chaîne / de
longueur d'adresse différente.

---

## 1.6 `fee.go` — les deux bornes, partagées

Ce fichier n'appartient logiquement ni à la P-Chain ni à saevm : les deux en ont
besoin, avec la même constante mais deux formules différentes. Le mettre ici
évite qu'un `k` diverge entre les deux chaînes.

**Où** : `vms/warpfx/fee.go` *(nouveau)*.

**Quoi** :

- `const MaxFeeOverpaymentFactor = 2`
- `VerifyFeeBand(consumed, produced, fee uint64) error` — la fourchette du
  lot 5, côté P-Chain : `consumed − fee ≥ produced ≥ consumed − k×fee`. Erreurs
  `ErrInsufficientFee` et `ErrExcessiveBurn`.
- `VerifyCanonicalBid(gasFeeCap, baseFee *uint256.Int) error` — la borne du
  lot 10d, côté saevm : `gasFeeCap ≤ k × baseFee`. Erreur `ErrBidTooHigh`.

**Pourquoi les deux ne sont pas la même fonction.** Elles portent le même `k` et
la même intention — « personne ne signe cette transaction, donc il faut borner ce
qu'un tiers peut détruire » — mais pas la même grandeur. Côté P-Chain, `k` borne
un **frais absolu**. Côté saevm, il borne une **enchère**, c'est-à-dire un
multiple du prix courant du gas. La même valeur peut convenir aux deux ; elle ne
mesure pas la même chose.

**Vérifier** : les tests 6 du lot 12, un nAVAX de part et d'autre de chaque
borne.

---

## 1.7 `fx.go` — l'extension

Le Fx est le composant qui répond à « que faut-il fournir pour dépenser cet
UTXO ? ». Il implémente l'interface `vms/platformvm/fx.Fx` **et** la nouvelle
`ContextualFx` du lot 3. Il est écrit en dernier parce qu'il assemble tout le
reste.

**Où** : `vms/warpfx/fx.go` *(nouveau)*, `vms/warpfx/vm.go` (l'interface `VM` que
le Fx exige, sur le modèle de `secp256k1fx.VM`) et `vms/warpfx/factory.go` (un
`Name`, un `ID` et une `Factory`, sur le modèle de
`vms/secp256k1fx/factory.go`).

⚠️ **`fx.Context` et `fx.ContextualFx` doivent exister avant que ce fichier ne
compile.** Ce sont les deux déclarations du [lot 3.1](03-aiguillage-fx.md), à
poser dans `vms/platformvm/fx/fx.go` — rien d'autre du lot 3 n'est requis ici.

⚠️ **`factory.go` n'est pas décoratif.** `vms/fx.Factory` est le point
d'extension du dépôt : `chains/manager.go:112-114` mappe `secp256k1fx.ID`,
`nftfx.ID` et `propertyfx.ID` vers leurs fabriques. `warpfx` s'y range, avec un
`ID = ids.ID{'w','a','r','p','f','x'}` dans la même convention que les trois
autres. C'est ce qui fait de `warpfx` une extension de plein droit plutôt qu'un
cas particulier câblé à la main dans la P-Chain.

**Quoi** — une structure **sans aucun champ d'autorisation** :

```go
type Fx struct {
    VM VM   // fixé une fois par Initialize
}

// Types rend les types que cette extension revendique. Une seule liste, lue à
// la fois par Initialize et par la table d'aiguillage du lot 3.
func Types() []any {
    return []any{&Owner{}, &TransferOutput{}, &Credential{}}
}
```

Méthodes :

| Méthode                                                | Comportement                                                                                                                                         |
| :----------------------------------------------------- | :--------------------------------------------------------------------------------------------------------------------------------------------------- |
| `Initialize(vm interface{}) error`                     | Type-asserte `VM`, puis enregistre `Types()` dans son `codec.Registry`.                                                                              |
| `Bootstrapping()` / `Bootstrapped()`                   | `nil`. Le Fx n'a rien à basculer — il ne saute aucun contrôle au bootstrap.                                                                          |
| `VerifyTransfer(tx, in, cred, utxo)`                   | **Toujours `ErrNoAuthorization`.**                                                                                                                   |
| `VerifyTransferWithContext(fxCtx, tx, in, cred, utxo)` | La provenance. Détail ci-dessous.                                                                                                                    |
| `VerifyPermission(...)`                                | Toujours `ErrPermissionUnsupported`.                                                                                                                 |
| `VerifyPermissionWithContext(fxCtx, tx, auth, cred, controlGroup)` | La même provenance, contre le groupe de contrôle.                                                                                        |
| `CreateOutput(amount, owner)`                          | `owner.(*Owner)` sinon `ErrWrongOwnerType`, puis `&TransferOutput{Amt: amount, Owner: *o}`.                                                          |

> ⚠️ **`Types()` existe pour que la revendication soit une donnée, pas un effet
> de bord.** Le lot 3 en a besoin pour construire sa table d'aiguillage. Si la
> table était peuplée en observant les `RegisterType` d'`Initialize` — ce que
> fait l'AVM, parce qu'il construit son codec au même moment — oublier
> d'initialiser laisserait la table vide **sans que rien n'échoue**. La P-Chain
> n'a pas ce problème : ses types sont enregistrés dans `platform.Codec` par le lot 2,
> directement. Une seule liste, lue explicitement des deux côtés.

⚠️ **La structure ne porte ni autorisation ni cache d'autorisation.** Le champ
`VM` est fixé une fois par `Initialize` et n'a rien à voir avec la vérification
en cours. C'est cette absence-là, et elle seule, qui rend la rémanence
impossible.

**`VerifyTransferWithContext`, dans l'ordre** :

1. `utxo.(*TransferOutput)` sinon `ErrWrongUTXOType`.
2. `in.(*secp256k1fx.TransferInput)` sinon `ErrWrongInputType` ; puis
   `len(in.SigIndices) != 0` → `ErrUnexpectedSigIndices`.
3. `cred.(*Credential)` sinon `ErrWrongCredentialType`.
4. `verify.All(out, in, cred)`.
5. `out.Amt != in.Amt` → `ErrMismatchedAmounts`.
6. `fxCtx == nil || fxCtx.Authorization == nil` → `ErrNoAuthorization`.
7. `fxCtx.Authorization.(*Authorization)` puis `.Authorizes(&out.Owner)` sinon
   `ErrWrongOwner`.

⚠️ **Aucune de ces sept étapes ne consulte l'heure.** L'`expiry` est vérifiée en
amont, dans `resolveAuthorization` (lot 4c), et `warpfx` n'a **aucune** règle
temporelle : pas de `Locktime` sur la sortie (1.3), et pas de `stakeable.LockOut`
autour d'elle (lot 6.3). C'est ce qui permet au `fx.Context` du lot 3 de ne
porter que l'autorisation.

**Pourquoi `VerifyTransfer` refuse au lieu de faire de son mieux.** C'est le
mécanisme d'*enforcement* du design. Au lot 4, seules sept transactions
appelleront les points d'entrée contextuels du vérifieur ; toutes les autres
passent par les points d'entrée historiques, qui transmettent un contexte nul.
Un UTXO `warpfx` atteint par un chemin qu'on aurait oublié de router reçoit donc
`nil` et **échoue mécaniquement**.

> ⚠️ **La charge de la preuve est inversée, et c'est intentionnel.**
> Oublier d'ajouter une transaction à la liste autorisée **ferme** une porte.
> Un design où `VerifyTransfer` aurait tenté de faire quelque chose de
> raisonnable aurait l'effet exactement inverse : une transaction non routée
> aurait dépensé l'UTXO sans jamais vérifier d'autorisation. Ne « répare » pas
> cette méthode si elle te paraît inutile.

**Pourquoi `VerifyPermission` refuse, et pourquoi sa jumelle contextuelle
n'est pas une contradiction.** Les deux points d'entrée servent deux appelants :

| Appelant | Point d'entrée | Résultat |
| :--- | :--- | :--- |
| `verifySubnetAuthorization` | `VerifyPermission` (sans contexte) | `ErrPermissionUnsupported` |
| `SetAutoRenewedValidatorConfigTx` | `VerifyPermissionWithContext` | provenance contre le `ValidatorAuthority` |

C'est **exactement** le mécanisme des transferts : le refus vient de ce que
l'appelant n'a résolu aucune autorisation, pas d'une liste de types. Un
`WarpOwner` reste donc structurellement incapable d'être groupe de contrôle d'un
subnet, sans qu'aucune règle n'ait à le dire — tandis qu'il peut piloter un
validateur auto-renouvelé, ce qui est la seule façon d'en récupérer le stake
(`Period = 0`).

Les étapes de `VerifyPermissionWithContext` sont celles du transfert, moins les
montants : `controlGroup.(*Owner)` → `ErrWrongOwnerType` ; `auth` est un
`*secp256k1fx.Input` à `SigIndices` vide → `ErrWrongInputType` /
`ErrUnexpectedSigIndices` ; `cred.(*Credential)` → `ErrWrongCredentialType` ;
`verify.All` ; puis contexte nul → `ErrNoAuthorization`, et
`Authorizes(controlGroup)` → `ErrWrongOwner`.

**Pourquoi `CreateOutput` est indispensable.** C'est le seul chemin par lequel
une récompense de staking se matérialise
(`proposal_tx_executor.go:962`, `newUTXO`). L'appel a lieu **des mois** après la
transaction de staking, et il doit alors dispatcher sur le type du propriétaire
des récompenses. Sans lui, on peut staker depuis un `WarpOwner` mais jamais en
toucher la récompense.

**Vérifier** :

```bash
go test ./vms/warpfx/...
go test ./vms/platformvm/...   # non-régression : rien n'atteint encore warpfx
```

Le test qui compte à ce stade est celui de la **rémanence** : appelle
`VerifyTransferWithContext` avec une autorisation valide, puis rappelle
`VerifyTransfer` (sans contexte) sur la même instance de `Fx`. Le second appel
doit échouer. Comme la structure ne retient rien de la vérification, il ne peut
pas en être autrement — mais le test verrouille cette propriété contre une
future « optimisation » qui ajouterait un cache d'autorisation à côté du champ
`VM`.

---

## Ce qui reste incertain

🔴 **À trancher avant d'écrire**

- **La valeur de `k`.** `MaxFeeOverpaymentFactor = 2` est la valeur proposée par
  l'ACP, justifiée par la période de doublement du prix du gas P-Chain sous
  congestion maximale (~30 s avec les paramètres ACP-103 du mainnet). Elle est
  posée dans `fee.go` maintenant, mais elle **borne deux grandeurs différentes**
  selon la chaîne (frais absolu côté P, multiple de prix côté saevm). Si tu veux
  deux constantes distinctes plus tard, c'est ici qu'il faudra scinder.

✅ **Vérifié à la compilation** — ce qui suit n'est plus à confirmer :

- **Le jeu d'assertions d'interface.** `verify.State`, `avax.TransferableOut`,
  `avax.Addressable` et `fx.Owned` pour `TransferOutput`, `fx.Owner` pour
  `Owner`, `fx.Fx` et `fx.ContextualFx` pour `Fx`. Toutes tiennent.
- **Le marqueur embarqué.** `Owner` embarquant `verify.IsNotState` et
  `TransferOutput` embarquant `verify.IsState` **plus** `Owner`, la promotion à
  la profondeur la plus courte donne bien `verify.State` à `TransferOutput`.
- **La signature de `Fx.Initialize`.** L'interface `VM` retenue est celle de
  `secp256k1fx.VM` — `CodecRegistry`, `Clock`, `Logger` —, dupliquée dans
  `vms/warpfx/vm.go` comme le fait `secp256k1fx`. `warpfx` n'utilise que
  `CodecRegistry` et `Logger` ; garder `Clock` évite d'avoir à raffiner
  l'interface exigée depuis `vms/platformvm/vm.go`, qui satisfait déjà celle-ci.

🟠 **À vérifier au premier build**

- **La `Factory` dans la table de `chains/manager.go`.** `warpfx.Factory`
  implémente `vms/fx.Factory` et déclare son `ID`, mais rien ne l'enregistre
  encore dans la table `{secp256k1fx.ID, nftfx.ID, propertyfx.ID}`. Ce n'est pas
  nécessaire au lot 3, qui câble la PlatformVM en dur ; c'est nécessaire si on
  veut que `warpfx` soit nommable depuis une `CreateChainTx`. À trancher au
  moment du câblage.

🟡 **À mesurer**

- Rien dans ce lot. Aucun calibrage n'y est décidé.
