# Lot 3 — L'aiguillage des Fx

[← Lot 2](02-codecs.md) · [Plan](README.md) · [Lot suivant : l'autorisation →](04-autorisation.md)

---

## Pourquoi ce lot existe

C'est le lot le plus important du plan, et le seul dont le titre ne dit rien de
ce qu'il évite. Voici le problème, en trois temps.

**Premier temps : Go ne dispatche pas sur le type d'un argument.** Le lot 2 a
appris au codec à décoder une sortie en `*warpfx.TransferOutput`. On pourrait
croire le travail fait. Il ne l'est pas : cela détermine le **type de
l'argument**, pas le **receveur de la méthode**. Or la PlatformVM appelle
`h.fx.VerifyTransfer(tx, in, creds[index], out)` où `h.fx` est une **instance
unique**, fixée au démarrage par `vm.fx = &secp256k1fx.Fx{}`
(`vms/platformvm/vm.go:130`). L'appel résout donc *toujours* vers
`secp256k1fx.Fx.VerifyTransfer`, qui écarte l'argument à sa première assertion de
type et rend `ErrWrongUTXOType`.

> **Enregistrer les types dans le codec est nécessaire mais pas suffisant.
> Sans aiguillage, tout UTXO `warpfx` est indépensable — définitivement.**

**Deuxième temps : la X-Chain a déjà résolu ça, pas la P-Chain.** L'AVM fait
cohabiter `secp256k1fx`, `nftfx` et `propertyfx` depuis toujours, au moyen d'une
table `reflect.Type → index de Fx` peuplée au moment où chaque Fx enregistre ses
types (`vms/avm/vm.go`, `typeToFxIndex` ; `vms/avm/tx_init.go`, `getFx`). Le
travail de ce lot est de porter ce mécanisme sur la PlatformVM. Ce n'est pas une
invention : c'est un rattrapage.

**Troisième temps : c'est un refactor qui touche tout.** L'appel
`fx.VerifyTransfer` est sur le chemin de vérification de **toutes** les
transactions de la P-Chain, sans exception. Tant que `warpfx` n'est pas
atteignable, la table ne contient qu'une entrée et tout retombe sur le Fx par
défaut — le comportement est rigoureusement inchangé. Mais « rigoureusement
inchangé » est une affirmation à prouver, pas à supposer : le critère de fin de
ce lot est que la suite `vms/platformvm/...` complète passe sans modification.

Le lot ajoute aussi, parce qu'il faut bien qu'ils naissent quelque part, les deux
déclarations sur lesquelles tout le lot 4 s'appuie : le `fx.Context`, qui
transporte l'autorisation, et l'interface `ContextualFx` qui l'accepte. Elles sont ici et pas au lot 4 parce que le vérifieur d'UTXOs doit les
connaître pour offrir ses nouveaux points d'entrée.

---

## Ordre d'écriture recommandé

`fx/fx.go` (les déclarations) → `fx/fxs.go` (la collection) →
`utxo/verifier.go` (les points d'entrée) → `txs/executor/backend.go` →
`vm.go` (le câblage) → `proposal_tx_executor.go` (les récompenses).

Le compilateur te guidera : chaque étape casse la suivante jusqu'à ce qu'elle
soit faite.

---

## 3.1 `fx/fx.go` — deux déclarations, et rien d'autre

Le paquet `vms/platformvm/fx` doit rester générique : il ne connaît pas
`warpfx`. C'est pourquoi `Authorization` y est typée `interface{}` — c'est
`warpfx` qui l'assertit.

**Où** : `vms/platformvm/fx/fx.go`.

**Quoi** — trois ajouts, l'interface `Fx` existante **ne change pas** :

```go
type Claim struct {
    ID    ids.ID
    Fx    Fx
    Types []any // les types que cette extension revendique ; nil pour le défaut
}

// Context est l'information de portée transaction dont un Fx peut avoir besoin
// en plus du triplet (input, credential, utxo). Toujours passée en argument,
// jamais retenue.
type Context struct {
    Authorization interface{} // résolue une fois par transaction, ou nil
}

type ContextualFx interface {
    Fx
    VerifyTransferWithContext(fxCtx *Context, tx, in, cred, utxo interface{}) error
    VerifyPermissionWithContext(fxCtx *Context, tx, in, cred, controlGroup interface{}) error
}
```

⚠️ **Les deux méthodes vont par paire avec les deux historiques**, et pour la
même raison. `VerifyTransfer` sert les chemins de dépense non routés,
`VerifyPermission` sert l'autorisation de subnet : dans les deux cas l'appelant
n'a résolu aucune autorisation, et dans les deux cas `warpfx` refuse. Ce sont
ces refus qui ferment respectivement la dépense non autorisée et le subnet
ingérable, **structurellement**.

**Pourquoi un `Context` à un seul champ plutôt qu'un paramètre nu.** Deux
raisons. D'abord `vms/platformvm/fx` ne peut pas importer `warpfx` — c'est
`warpfx` qui importe `fx`, pour son assertion `fx.Owner` —, donc le champ doit
rester `interface{}` et mérite d'être nommé. Ensuite, la structure est le point
où une information de portée transaction se rajoute sans toucher la signature du
vérifieur : c'est un point d'extension, pas un emballage.

> ⚠️ **Il n'y a pas de `ChainTime` dans ce contexte, et c'est une décision.**
> On serait tenté de l'y mettre « pour que le Fx ne consulte pas l'horloge
> locale » — le défaut que `secp256k1fx` a pour son locktime
> (`fx.VM.Clock().Unix()`). Mais `warpfx` n'a **aucune** règle temporelle : pas de
> `Locktime` sur la sortie (lot 1.3), pas de `stakeable.LockOut` autour d'elle
> (lot 6.3), et l'`expiry` est vérifiée en amont par `resolveAuthorization`
> (lot 4c), qui reçoit le `chainTime` en paramètre. Un champ que personne ne lit
> est une invitation à le lire un jour pour une mauvaise raison. Si un futur Fx
> a besoin du temps, il s'ajoutera ici — avec le cas d'usage sous les yeux.

**Vérifier** : rien encore, ce sont des déclarations.

---

## 3.2 `fx/fxs.go` — la collection

Une liste ordonnée de Fx, plus une table qui associe chaque type Go concret au Fx
qui le revendique. Le premier de la liste est le **défaut** : tout type non
revendiqué retombe sur lui.

**Où** : `vms/platformvm/fx/fxs.go` *(nouveau)*.

**Quoi** :

```go
type Fxs struct {
    fxs           []*Claim
    typeToFxIndex map[reflect.Type]int
}

func NewFxs(claims ...Claim) *Fxs   // remplit la table depuis Claim.Types
func (f *Fxs) Get(val interface{}) Fx   // repli sur f.fxs[0]
func (f *Fxs) Default() Fx
func (f *Fxs) VerifyTransfer(fxCtx *Context, tx, in, cred, utxo interface{}) error
```

`VerifyTransfer` résout `Get(utxo)`, puis :
- si le Fx implémente `ContextualFx` → `VerifyTransferWithContext(fxCtx, ...)` ;
- sinon → `VerifyTransfer(...)`, inchangé.

**Pourquoi la revendication est une donnée et non un effet de bord.** L'AVM
peuple sa table (`vms/avm/vm.go`, `typeToFxIndex`) en **observant** les
`RegisterType` que chaque Fx émet dans son `Initialize`, à travers un
`codec.Registry` enveloppant. Il n'a pas le choix : sa liste de Fx est dynamique
— elle vient de `chainParams.FxIDs`, résolue à la création de la chaîne via la
table de fabriques de `chains/manager.go` — et son codec se construit au même
moment.

**La P-Chain n'a ni l'un ni l'autre de ces contraintes.** Sa liste de Fx est
compilée en dur, et ses types sont enregistrés dans `platform.Codec` par le lot 2,
directement. Le registry enveloppant n'aurait donc servi qu'à *deviner* une liste
qu'on peut simplement écrire :

```go
// vms/platformvm/vm.go
vm.fxs = fx.NewFxs(
    fx.Claim{ID: secp256k1fx.ID, Fx: &secp256k1fx.Fx{}},              // défaut
    fx.Claim{ID: warpfx.ID, Fx: &warpfx.Fx{}, Types: warpfx.Types()},
)
```

> ⚠️ **Ce que ça élimine, et pourquoi ça valait le détour.**
> Avec le mécanisme par effet de bord, ajouter un Fx à la collection **sans
> l'initialiser à travers le registry rendu** laisse la table vide : tout
> compile, tous les tests existants passent, et l'aiguillage n'aiguille **rien**
> — le symptôme n'apparaît qu'au lot 4, sous la forme d'un `ErrWrongUTXOType` de
> `secp256k1fx` qu'on met un moment à relier à sa cause. Avec la revendication
> explicite, la table est remplie par le constructeur : il n'y a plus d'ordre à
> respecter, donc plus de piège.
>
> `Initialize` continue d'être appelé — c'est le cycle de vie du Fx, et c'est lui
> qui fixe le `VM` — mais il n'est **plus porteur de l'aiguillage**.

⚠️ **`warpfx.Types()` est la seule liste.** Elle sert à `Initialize` *et* à la
revendication. Ne recopie pas les trois types dans `vm.go` : la duplication
serait exactement le genre d'écart qui ne se voit pas.

**Vérifier** : `fxs_test.go` — `Get(&warpfx.TransferOutput{})` rend le Fx
`warpfx`, `Get(&secp256k1fx.TransferOutput{})` le défaut, et
`Get(&secp256k1fx.OutputOwners{})` — que personne ne revendique — rend le défaut
sans paniquer. Plus un test qui compare `warpfx.Types()` aux types que
`Initialize` enregistre réellement, pour que la liste unique le reste.

---

## 3.3 `utxo/verifier.go` — la résolution par sortie consommée

Le vérifieur passe d'une instance à une collection, et gagne deux points
d'entrée qui acceptent un `*fx.Context`. Les deux points d'entrée historiques
restent, et délèguent avec un contexte **nul** — c'est ce qui rend l'omission
sûre.

**Où** : `vms/platformvm/utxo/verifier.go`.

**Quoi** :

1. `NewVerifier(ctx, clk, fxs *fx.Fxs)` et le champ `fxs` à la place de `fx`.
2. L'interface `Verifier` gagne `VerifySpendWithContext` et
   `VerifySpendUTXOsWithContext`, de mêmes signatures que les deux existantes
   plus un premier paramètre `fxCtx *fx.Context`.
3. Les deux méthodes historiques deviennent des passe-plats :
   `return h.VerifySpendUTXOsWithContext(nil, tx, ...)`.
4. À la ligne **202**, `h.fx.VerifyTransfer(tx, in, creds[index], out)` devient
   `h.fxs.VerifyTransfer(fxCtx, tx, in, creds[index], out)`.
5. Régénérer le mock : `vms/platformvm/utxo/utxomock/verifier.go`.

**Pourquoi la résolution se fait sur `out` et jamais sur `in`.** C'est l'UTXO qui
porte sa condition de dépense ; l'input ne fait que la référencer. C'est
d'ailleurs pour ça que le lot 1 ne crée aucun type d'input et réutilise
`secp256k1fx.TransferInput` à `SigIndices` vide.

⚠️ **`out` est la sortie déjà dépliée de `stakeable.LockOut`.** Regarde les
lignes 177-180 : le déballage a lieu avant. Passe `out`, pas `utxo.Out`, sinon
un UTXO verrouillé résoudrait vers le mauvais Fx.

**Vérifier** :

```bash
go test ./vms/platformvm/utxo/...
```

---

## 3.4 `backend.go` et `vm.go` — le câblage

Le `Backend` porte désormais **les deux** : le Fx par défaut, qui sert encore à
l'autorisation de subnet, et la collection. Ce n'est pas de la redondance :
`VerifyPermission` est secp-only par construction et n'a rien à aiguiller.

**Où** : `vms/platformvm/txs/executor/backend.go` puis `vms/platformvm/vm.go`.

**Quoi** — `Backend` gagne `Fxs *fx.Fxs` à côté de `Fx fx.Fx`. Dans `vm.go`, aux
alentours de la ligne 128, `vm.fx = &secp256k1fx.Fx{}` devient :

```go
vm.fxs = fx.NewFxs(
    fx.Claim{ID: secp256k1fx.ID, Fx: &secp256k1fx.Fx{}},              // défaut
    fx.Claim{ID: warpfx.ID, Fx: &warpfx.Fx{}, Types: warpfx.Types()},
)
for _, claim := range vm.fxs.All() {
    if err := claim.Fx.Initialize(vm); err != nil {
        return err
    }
}
```

Puis `utxo.NewVerifier(vm.ctx, &vm.clock, vm.fxs)` et, dans le `Backend`,
`Fx: vm.fxs.Default()` et `Fxs: vm.fxs`.

**`vm.codecRegistry` ne bouge pas.** Il reste le `linearcodec.NewDefault()`
existant, dont le commentaire dit déjà « this codec is never used to serialize
anything » : les types sont dans `platform.Codec` depuis le lot 2. `Initialize` y
enregistre les siens sans conséquence — il est appelé pour le cycle de vie du Fx
(fixer le `VM`, préparer les caches de `secp256k1fx`), plus pour peupler quoi que
ce soit.

⚠️ **L'ordre compte toujours** : `secp256k1fx` en premier, donc défaut. Un type
non revendiqué — `secp256k1fx.OutputOwners`, que personne ne liste, ou
`stakeable.LockOut`, que le vérifieur déplie de toute façon — doit retomber sur
lui.

**Vérifier** : le nœud démarre. `go test ./vms/platformvm/...` en entier.

---

## 3.5 `proposal_tx_executor.go` — les récompenses

C'est le second site de dispatch, et il est facile à oublier parce qu'il ne
ressemble pas au premier : il ne résout pas sur une sortie consommée mais sur un
**propriétaire de récompenses**, et il s'exécute des mois après la transaction de
staking.

**Où** : `vms/platformvm/txs/executor/proposal_tx_executor.go`, `newUTXO`
(l.955-979).

**Quoi** — `outIntf, err := e.backend.Fx.CreateOutput(amount, owner)` devient :

```go
resolved := e.backend.Fxs.Get(owner)   // nil si Fxs est nil
if resolved == nil {
    return nil, ErrNoFxForRewardsOwner
}
outIntf, err := resolved.CreateOutput(amount, owner)
```

**Pourquoi c'est ce qui rend le staking depuis un `WarpOwner` utile.** Sans ce
dispatch, on peut staker (le lot 4 l'autorisera) mais la récompense se
matérialiserait en `secp256k1fx.TransferOutput` — ou plus probablement échouerait
sur `ErrWrongOwnerType` au fond de `secp256k1fx.CreateOutput`. C'est le **seul**
appelant de `CreateOutput` dans tout le dépôt : il n'y en a pas d'autre à
corriger.

> ⚠️ **Ne fais pas un repli silencieux sur le défaut si `Fxs` est nil.**
> Un `Backend` construit dans un test sans collection produirait alors une sortie
> du mauvais type, à un endroit où l'erreur ne se voit qu'à la dépense — c'est-à-
> dire des mois plus tard, sur des fonds réels. `ErrNoFxForRewardsOwner` fait
> échouer le test au bon endroit.

**Vérifier** : `go test ./vms/platformvm/txs/executor/...`

---

## Critère de fin de lot

```bash
go test ./vms/platformvm/...
```

Tout doit passer **sans qu'aucun test existant n'ait été modifié pour l'occasion**
— hormis la construction des helpers de test qui doivent maintenant fournir une
collection au lieu d'une instance (`block/builder/helpers_test.go`,
`block/executor/helpers_test.go`, `txs/executor/helpers_test.go`).

Si tu dois assouplir une assertion existante pour faire passer la suite, c'est
que le refactor a changé un comportement. Cherche ce qui a changé plutôt que
d'adapter le test.

---

## Ce qui reste incertain

✅ **Vérifié**

- **`warpfx.Types()` et `Initialize` ne divergent pas** — même liste par
  construction, et `TestInitializeRegistersTypes` la verrouille.
- **Les helpers cassés : cinq, pas trois.** Aux trois `helpers_test.go` prévus
  s'ajoutent `block/executor/verifier_test.go` et cinq blocs de
  `txs/executor/standard_tx_executor_test.go`. Ces deux-là construisent leur Fx
  avec `InitializeVM` sur un `secp256k1fx.TestVM` **sans registry**, donc ils
  reçoivent une collection **secp seule** : `warpfx.Fx.Initialize` n'aurait rien
  où enregistrer.
- **`warpfx.VM` est structurellement identique à `secp256k1fx.VM`**
  (`CodecRegistry` / `Clock` / `Logger`), et `*platformvm.VM` satisfait les deux.
  Aucune interface plus étroite n'est nécessaire.

🔴 **Tranché**

- **`VerifySpendWithContext` est exposée sur l'interface `Verifier`**, comme le
  recommandait le plan : le mock est explicite plutôt qu'un type-assert chez
  l'appelant.

⚠️ **Le critère de fin de lot ne prouve rien sur l'aiguillage**, et c'est le
piège de ce lot. La suite complète passe aussi bien avec une table vide.
`TestVerifySpendUTXOsDispatchesOnTheConsumedOutput` couvre ce que le critère ne
voit pas : en vidant `Types` de la revendication `warpfx`, il échoue **seul**,
tout le reste restant vert.

🟡 **À mesurer**

- Rien dans ce lot. Le surcoût d'une lecture de map par input est négligeable
  devant une vérification de signature, mais si tu veux t'en assurer, le
  benchmark de `VerifySpendUTXOs` existe déjà.
