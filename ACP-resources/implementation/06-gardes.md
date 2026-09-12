# Lot 6 — Gardes

[← Lot 5](05-import-canonique.md) · [Plan](README.md) · [Lot suivant : les attestations →](07-attestations.md)

---

## Pourquoi ce lot existe

Deux règles qui n'ont rien à voir l'une avec l'autre, sauf qu'elles répondent à
la même famille de problèmes : **empêcher qu'un UTXO `warpfx` naisse dans un
endroit où personne ne saura le lire.**

La première concerne l'espace. Le lot 2 a enregistré `warpfx.TransferOutput` dans
le codec de la PlatformVM, et le lot 10 le fera dans celui de saevm. Nulle part
ailleurs — ni dans la X-Chain, ni dans coreth. Un export P-Chain vers la X-Chain
portant ce type produirait donc un UTXO **définitivement indécodable** par sa
chaîne cible. Et comme un export débite avant que la chaîne cible n'ait rien à
dire, l'erreur est irréversible.

La seconde concerne le temps. L'enregistrement dans le codec est
**inconditionnel** — c'est la règle d'avalanchego, le gating se fait à la
vérification jamais au décodage. Il serait tentant d'en conclure que recevoir des
fonds ne demande aucune règle nouvelle. C'est vrai *après* l'activation, et faux
avant : un nœud exécutant un binaire antérieur ne connaît tout simplement pas ces
types, ne sait pas décoder la transaction, ni donc le bloc qui la contient. Une
sortie `warpfx` créée avant l'activation ne serait pas un inconvénient, **ce
serait un fork**.

Ce lot pose donc :

> Une transaction qui **mentionne** un type `warpfx` — sortie, sortie exportée,
> sortie de stake, propriétaire de récompenses, ou credential — est invalide tant
> que l'upgrade cible n'est pas activé.

Et c'est, dans ce plan, la **seule** garde d'activation de tout le design.
L'ACP en prévoyait deux (P-Chain et coreth). La décision de ne pas toucher coreth
rend la sienne inutile : sans enregistrement dans son codec atomique, coreth ne
peut pas *marshaller* le type, donc pas construire l'export qu'on aurait voulu
lui interdire. Le refus devient structurel plutôt que conditionnel. Et saevm n'en
a jamais eu besoin, pour une raison qui lui est propre et qu'explique le
[lot 10e](10-saevm.md).

---

## 6.1 La destination d'export

Une sortie `warpfx` ne peut aller qu'à la C-Chain. La règle est courte à écrire ;
ce qui compte, c'est **où** on l'appelle.

**Où** : `vms/platformvm/txs/executor/warp_export.go` *(nouveau)*, appelé depuis
`standard_tx_executor.go`, `ExportTx`.

**Quoi** :

```go
func verifyWarpExportDestination(cChainID ids.ID, tx *platform.ExportTx) error
```

Parcourt `tx.ExportedOutputs` ; dès qu'une sortie est un `*warpfx.TransferOutput`
(après dépliage éventuel), exige `tx.DestinationChain == cChainID`, sinon
`ErrWarpOutputWrongDestination`.

L'appel se place **avant** la garde `if e.backend.Bootstrapped.Get()` de la ligne
424, donc inconditionnellement.

**Pourquoi hors de la garde de bootstrap.** La règle ne lit aucun état — elle
inspecte la transaction. La placer sous la garde signifierait qu'un nœud en cours
de bootstrap accepte un export qu'il rejettera une fois bootstrappé : deux
comportements pour la même transaction, ce qui est exactement ce qu'une règle de
consensus ne doit pas faire.

**Pourquoi la C-Chain et pas « n'importe quelle chaîne qui sait décoder ».**
Parce que la liste des chaînes qui savent décoder est exactement `{C-Chain}` dans
ce périmètre, et qu'une règle qui l'énonce en dur est vérifiable. Une règle
formulée en « toute chaîne compatible » demanderait à la P-Chain de connaître les
codecs des autres chaînes, ce qu'elle ne peut pas.

> ⚠️ **Le contrôle existant à côté duquel celui-ci s'installe est plus faible
> qu'il n'y paraît.**
> Aujourd'hui, `ExportTx.SyntacticVerify` (`export_tx.go:65`) rejette
> `*stakeable.LockOut` — un contrôle sur un **type nommé**, pas sur la notion de
> « sortie verrouillée ». Ce n'est pas un modèle à suivre : ta règle doit lister
> ce qu'elle autorise (la C-Chain), pas ce qu'elle interdit.

**Vérifier** : test 11 du lot 12 — une sortie `warpfx` vers la X-Chain doit être
refusée, et refusée **avant tout débit**.

---

## 6.2 La garde d'activation

Un seul point d'appel, choisi pour qu'aucun type de transaction ne puisse
l'oublier. `StandardTx` est le point de passage de **toutes** les transactions
standard de la P-Chain — transactions de staking comprises depuis Durango.

**Où** : `vms/platformvm/txs/executor/warp_activation.go` *(nouveau)*, appelé en
tête de `StandardTx` (`standard_tx_executor.go:78`).

**Quoi** :

```go
func VerifyWarpUTXOsActivated(upgrades upgrade.Config, chainTime time.Time, tx *platform.Tx) error
```

Si `upgrades.IsHeliconActivated(chainTime)` → `nil`, immédiatement. Sinon,
parcourir la transaction **par réflexion** à la recherche de n'importe quel type
du paquet `warpfx`, et rendre `ErrWarpUTXOsNotActivated` si on en trouve un.

Dans `StandardTx`, avant le `tx.Unsigned.Visit(&standardExecutor)` :

```go
if err := VerifyWarpUTXOsActivated(
    backend.Config.UpgradeConfig,
    state.GetTimestamp(),
    tx,
); err != nil {
    return nil, nil, nil, err
}
```

**Pourquoi par réflexion et pas une liste d'emplacements.** Les endroits où un
type `warpfx` peut apparaître sont nombreux et hétérogènes : `Outs`,
`ExportedOutputs`, `StakeOuts`, `ValidatorRewardsOwner`, `DelegatorRewardsOwner`,
et `Creds`. Une énumération manuelle serait exacte aujourd'hui et fausse au
premier champ ajouté à un type de transaction — et « fausse » veut dire ici
« laisse passer un type non gaté », donc un fork. Le parcours réflexif est plus
lent, mais il ne tourne **que tant que l'upgrade est inactif** : après
activation, la fonction rend `nil` à la première ligne et le coût est nul pour
toujours.

**Pourquoi `state.GetTimestamp()` et non l'horloge.** Même raison qu'au lot 4c :
c'est une règle de consensus, elle doit s'évaluer contre le `chainTime` du bloc.

> ⚠️ **Ce que la garde protège n'est pas ce qu'on croit.**
> Elle n'empêche pas « d'utiliser une fonctionnalité trop tôt ». Elle empêche un
> **fork** : un nœud non mis à jour ne sait pas décoder les types 43/44/45, donc
> ne sait pas parser le bloc qui les contient. Il ne rejetterait pas la
> transaction — il serait incapable de traiter la chaîne. C'est pour ça que la
> garde porte sur « mentionne » et pas sur « dépense » : **recevoir** aussi crée
> un type non décodable.

⚠️ **Ce que la garde ne couvre pas, et n'a pas à couvrir.** Elle vit dans
`StandardTx`. Les transactions de proposition (`RewardValidatorTx`) n'y passent
pas — mais elles ne sont pas construites par un utilisateur, elles sont
construites par le builder de blocs à partir d'un staking déjà accepté, donc déjà
passé par la garde. Vérifie néanmoins si ton implémentation de `CreateOutput`
peut produire une sortie `warpfx` dans un contexte où la garde n'est jamais
passée ; par construction ce n'est pas possible, mais c'est le genre de
raisonnement qui mérite d'être écrit dans un commentaire.

**Vérifier** : test 8 du lot 12 — la même transaction avant et après
`HeliconTime`, avec les deux résultats attendus.

---

## 6.3 Le verrou qui revient par la bande

Cette règle n'est **pas** dans l'ACP, et elle n'était dans aucune version de ce
plan. Elle vient de la relecture du lot 1.3 face au code existant. **Elle est
tranchée : on refuse.** À reporter dans l'ACP.

**Le problème.** Le lot 1.3 refuse un champ `Locktime` sur
`warpfx.TransferOutput`, et sa première raison est décisive : personne ne peut
l'appliquer correctement de bout en bout, parce que `verifier.go` évalue les
locktimes contre `h.clk.Time()` — **l'horloge locale du nœud** — alors que tout
le reste de `warpfx` s'évalue contre le `chainTime`.

Le champ n'existe pas. Le comportement, si :

```go
// vms/platformvm/stakeable/stakeable_lock.go
type LockOut struct {
    Locktime             uint64 `serialize:"true"`
    avax.TransferableOut `serialize:"true"`   // ← champ d'interface
}
```

`LockOut.Verify()` ne refuse que l'imbrication de deux `LockOut`. Un
`stakeable.LockOut{Locktime: X, TransferableOut: &warpfx.TransferOutput{…}}` est
donc parfaitement valide, et `LockOut.Addresses()` délègue à l'intérieur, si bien
que l'UTXO est même indexé sous sa `SourceAddress`.

**Et il est atteignable par n'importe qui.** `VerifySpendUTXOs` autorise
explicitement de produire du verrouillé à partir de fonds **déverrouillés**
(branche `increase > unlockedConsumedAsset`). Une `BaseTx` ordinaire, signée en
secp, peut donc verrouiller des fonds au profit d'un `WarpOwner` tiers — recevoir
ne demande aucune autorisation. Le propriétaire récupère ses fonds à l'échéance,
donc ce n'est pas un vol ; c'est un cadeau contraignant, évalué sur une base de
temps que le design a explicitement rejetée.

⚠️ Trois autres règles interagissent avec ce cas, et il vaut mieux les avoir en
tête avant de choisir :

- **`verifyWarpExportDestination` (6.1) doit déplier `stakeable.LockOut`** avant
  de tester le type. C'est déjà ce que dit la spécification du 6.1 ; ne le perds
  pas en écrivant.
- **`ExportTx.SyntacticVerify` rejette déjà `*stakeable.LockOut` à l'export**
  (`export_tx.go:65`), donc un tel UTXO ne peut pas quitter la P-Chain avant son
  échéance. Le fonds n'est pas perdu, il est immobilisé.
- **La forme canonique n'est pas concernée** : elle exige des
  `*warpfx.TransferOutput` nus, et `stakeable.LockOut` n'est de toute façon pas
  enregistré dans le codec atomique de saevm.

> **La règle.** Une sortie dont le type déplié appartient à `warpfx` ne peut pas
> être enveloppée dans un `stakeable.LockOut` → `ErrWarpOutputNotLockable`.

**Où l'écrire.** Le même parcours que la garde d'activation (6.2) sait déjà
descendre dans toute la transaction ; c'est le plus économique. Ajoute à
`warp_activation.go` une seconde fonction, `verifyWarpOutputsNotLocked(tx)`,
appelée **inconditionnellement** en tête de `StandardTx` — juste après
`VerifyWarpUTXOsActivated`, qui elle ne tourne que pré-activation. Elle parcourt
la transaction, et pour tout `*stakeable.LockOut` rencontré, refuse si le
`TransferableOut` déplié est d'un type `warpfx`.

⚠️ **Inconditionnelle, contrairement à la garde d'activation.** Celle-ci
s'éteint après l'upgrade ; celle-là doit tourner pour toujours. C'est le seul
parcours réflexif que la proposition laisse en régime nominal — il porte sur les
sorties d'une transaction, pas sur son graphe entier, et son coût est celui du
parcours des `Outs ‖ ExportedOutputs ‖ StakeOuts` que la tarification fait déjà.

Ce qui justifie qu'elle soit **une règle et non un contrôle de type** : le
montage est constructible par **un tiers** — `VerifySpendUTXOs` autorise de
produire du verrouillé depuis des fonds déverrouillés, et recevoir ne demande
aucune autorisation. Ce n'est donc pas de l'auto-mutilation qu'on laisserait à
son auteur.

**Vérifier** : une `BaseTx` signée en secp produisant un
`stakeable.LockOut{warpfx.TransferOutput}` doit échouer sur
`ErrWarpOutputNotLockable` ; idem pour une `ExportTx` et pour un `StakeOuts` de
transaction de staking. Et un test de non-régression : un
`stakeable.LockOut{secp256k1fx.TransferOutput}` doit continuer de passer.

---

## 6.4 Ce qu'on ne fait **pas**, et pourquoi c'est sûr

Ce n'est pas du code, mais c'est une décision qu'il faut pouvoir défendre en
revue, donc autant l'avoir écrite.

L'ACP prévoit une garde d'activation dans **coreth**, pour la fenêtre de dix
secondes qui précède la bascule de VM (`TransitionTime = HeliconTime − 10s`,
`node/node.go:1248`). Ce plan ne l'écrit pas, parce qu'elle devient sans objet
dès lors qu'on ne touche pas au codec atomique de coreth :

- `linearcodec.PackPrefix` refuse de marshaller un type non enregistré
  (`codec/linearcodec/codec.go`, `"can't marshal unregistered type"`). Coreth
  ne peut donc ni **construire** ni **parser** un export portant
  `warpfx.TransferOutput`. Le refus est structurel.

  ✅ **Et il est désormais observé.** Soumis à la C-Chain avant la bascule, un
  tel export est rejeté au parsing : `couldn't unmarshal interface: unknown type
  ID 44`. Les **mêmes octets** sont acceptés une fois saevm installé.
  `tests/e2e/p/warp_utxos.go` est le seul endroit où ce raisonnement est vérifié
  plutôt que déduit — c'est-à-dire le seul endroit qui justifie l'absence de
  garde côté C-Chain.
- Dans l'autre sens, aucun UTXO `warpfx` ne peut lui parvenir par la shared
  memory : seule une P-Chain **post**-Helicon peut en produire (c'est exactement
  ce que fait la garde 6.2), et à ce moment la C-Chain est déjà saevm.

> ⚠️ **Ce raisonnement s'effondre si quelqu'un enregistre le type dans coreth
> « par alignement défensif ».**
> L'ACP mentionne cette option comme « sans conséquence de consensus ». Elle ne
> l'est plus une fois la garde retirée : enregistrer le type sans la garde
> rendrait constructible ce que la garde interdisait. **Enregistrement et garde
> vont ensemble, ou aucun des deux.** Ce plan choisit aucun des deux ; écris-le
> dans un commentaire près du codec de saevm, sinon la prochaine personne
> « corrigera » l'asymétrie apparente.

---

## Ce qui reste incertain

✅ **Mesuré**

Le parcours réflexif est **benché**, pas estimé (`BenchmarkVerifyWarpUTXOsActivated`).
Sur une transaction à 16 inputs et 16 sorties :

| | ns/op |
| :--- | ---: |
| garde d'activation, **avant** l'upgrade | ~12 000 |
| garde d'activation, **après** | **7,7** |
| verrou `stakeable`, en permanence | ~5 400 |

Les 7,7 ns post-activation sont la comparaison de timestamp seule : le parcours
ne tourne plus jamais. Les ~5,4 µs permanents du verrou restent nettement sous
une seule vérification de signature secp, donc la forme réflexive est **gardée** —
la restreindre aux sorties rouvrirait le risque du « champ ajouté plus tard » qui
disqualifie l'énumération manuelle.

✅ **Vérifié**

- **`StandardTx` est bien le seul point de passage** — par lecture *et* par
  grep : `standardTxExecutor{}` n'est construit qu'à cet endroit, et `StandardTx`
  a quatre appelants (builder, manager, verifier de bloc, et l'exécuteur atomique
  des blocs Apricot).
- **La détection se fait sur le chemin de paquet**
  (`reflect.Type.PkgPath() == ".../vms/warpfx"`), pas sur une liste de types :
  un type ajouté à `warpfx` est couvert le jour où il l'est.
- **Le walker lit le `TransferableOut` d'un `LockOut` par réflexion**
  (`FieldByName`) plutôt que par assertion : la valeur peut se trouver dans un
  champ non exporté, où `reflect.Value.Interface()` panique.
- **`IsHeliconActivated` existe** et reste le seul appel à changer si la
  proposition glisse — plus le champ dans `upgrade.Config` et sa ligne dans
  `Validate()`.

🔴 **À trancher avant d'écrire**

- **L'upgrade cible.** Le plan suppose Helicon, qui est ce que vise l'ACP.
  `HeliconTime` est déjà `UnscheduledActivationTime` sur mainnet et
  `2026-07-28 15:00 UTC` sur Fuji (`upgrade/upgrade.go:41,65`). Si la proposition
  n'entre pas dans Helicon, il faut un nouveau champ dans `upgrade.Config`, son
  `IsXActivated`, et son entrée dans `Validate()` — et les positions de codec du
  lot 2 auront très probablement bougé entre-temps.

🟡 **À mesurer**

- Rien dans ce lot.
