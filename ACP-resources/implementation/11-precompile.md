# Lot 11 — Le précompile d'export

[← Lot 10](10-saevm.md) · [Plan](README.md) · [Lot suivant : les tests →](12-tests.md)

---

> ## ✅ Ce lot est fait
>
> Le précompile existe, il est testé, et l'aller-retour complet tourne en e2e
> pour un EOA **et** pour un contrat
> (`tests/e2e/p/warp_export_precompile.go`).
>
> Ce guide reste écrit au conditionnel par endroits ; ce qui compte est la
> section [Ce que l'écriture a trouvé](#ce-que-lécriture-a-trouvé), en bas, qui
> corrige quatre points où l'analyse était fausse ou incomplète.

---

## Pourquoi ce lot existe

Tout le reste du plan fonctionne déjà pour un EOA. Le lot 10b l'a montré : un EOA
signe un export atomique désignant un `WarpOwner`, et le chemin s'ouvre sans une
seule modification du consensus C-Chain. Il peut même désigner **une adresse
tierce**, donc financer un contrat sans que celui-ci ait à agir.

Ce qui manque est le cas où le contrat veut agir lui-même : **un contrat n'a pas
de clé**, et ne peut donc pas signer un export atomique. Pire, une transaction
atomique n'est pas une transaction EVM — elle est gossipée à part, incluse dans
l'extra data du bloc, et vit entièrement hors de toute frame d'appel. Il n'y a
aucun endroit d'où un contrat pourrait en émettre une.

Un précompile résout ça en changeant de mécanisme plutôt qu'en contournant le
problème : il vit **dans** une transaction EVM, donc dans une frame où
`msg.sender` n'est pas usurpable. Le propriétaire de la sortie est forcé à
l'appelant, exactement comme le précompile Warp force le `SourceAddress` des
`AddressedCall`.

```solidity
// Exporte msg.value vers la P-Chain, au profit du WarpOwner de l'appelant.
function exportAVAX() external payable;
```

**Un seul point d'entrée, sans paramètre.** Le précompile débite, dépose un UTXO
en shared memory, et s'arrête là. Il n'a ni à connaître les montants d'autres
UTXOs, ni à fixer un frais P-Chain, ni à encoder la transaction d'import, ni à
émettre de message Warp : l'`ImportTx` étant canonique (lot 5), n'importe qui la
construit ensuite à partir de ce qu'il lit en shared memory.

**C'est le lot le plus délicat du plan**, pour trois raisons qui n'ont rien à
voir entre elles :

- il touche un morceau de coreth, le seul du plan, et il faut savoir pourquoi
  c'est inévitable (11a, 11c) ;
- il introduit un identifiant d'UTXO qui n'a pas de conteneur naturel, et les
  trois façons de le dériver ont chacune un défaut (11d) ;
- **son calibrage en gas est le seul endroit du plan où une erreur est
  durable** (11f). Une règle mal placée se corrige au prochain upgrade ; un
  précompile sous-tarifié devient une subvention permanente à la croissance
  d'état.

C'est pour ça qu'il est à faire **en dernier**, contre un chemin déjà en place et
donc mesurable.

---

## 11a. Où il vit, et ce qu'on touche de coreth

Le *framework* de précompiles vit sous `graft/coreth/precompile/` et **saevm le
réutilise intégralement** — `vms/saevm/cchain/genesis.go:25` importe
`graft/coreth/precompile/contracts/warp`. La question n'est donc pas « coreth ou
saevm » mais « où poser le nouveau paquet sans écrire de règle de VM coreth ».

**Où** : `vms/saevm/cchain/precompile/nativeexport/` *(nouveau)*.

**Pourquoi c'est possible.** `modules.RegisterModule`
(`graft/coreth/precompile/modules/registerer.go`) est un **registre global**
peuplé par les `init()`. Le paquet `graft/coreth/precompile/registry` ne fait rien
d'autre que des imports blancs pour déclencher ces `init()` — et
`vms/saevm/cchain/genesis.go` ne passe **pas** par lui : il référence le paquet du
précompile Warp directement. Un paquet hébergé sous `vms/saevm/` et importé par
`genesis.go` s'enregistrera donc exactement de la même façon.

**L'adresse** : à prendre dans une plage réservée
(`0x0100…`, `0x0200…` ou `0x0300…`, cf. `reservedRanges` du registerer), le
précompile Warp occupant `0x0200000000000000000000000000000000000005`. ⚠️ À
reporter dans l'ACP et dans l'interface Solidity une fois figée.

**L'activation** : une seconde entrée dans
`extras.UpgradeConfig.PrecompileUpgrades` (`vms/saevm/cchain/genesis.go:124-132`),
calée sur `HeliconTime` — au même endroit et de la même façon que l'entrée
existante cale le précompile Warp sur `DurangoTime`.

> ⚠️ **À vérifier au premier build : le registre est global, l'activation ne
> l'est pas.**
> Enregistrer un module rend son `ConfigKey` **parsable** dans un JSON d'upgrade,
> y compris côté coreth. L'activation, elle, passe uniquement par
> `PrecompileUpgrades`, que seul `saevm/cchain/genesis.go` alimente. Le
> raisonnement tient, mais il n'est pas exécuté. Si un couplage apparaît, le repli
> est d'héberger le paquet sous `graft/coreth/precompile/contracts/` et de ne
> l'activer que dans la config saevm — ce qui ne change rien au fond, seulement
> à l'emplacement du fichier.

---

## 11b. Dériver les champs de l'UTXO

Un seul paramètre porte de la valeur ; tout le reste vient du contexte d'appel,
et c'est ce qui rend le précompile sûr : il n'y a presque rien que l'appelant
puisse mentir.

**Où** : le `Run` du nouveau précompile, via `contract.AccessibleState`.

**Quoi** :

| Champ | Source |
| :--- | :--- |
| `DestinationChain` | `constants.PlatformChainID`, **forcé** |
| `Owner.SourceChainID` | `ctx.ChainID` (via `GetSnowContext()`) |
| `Owner.SourceAddress` | `caller`, **forcé — non usurpable** |
| `AssetID` | `ctx.AVAXAssetID`, **forcé** |
| `Amt` | `msg.value / X2CRate` |
| `UTXOID.TxID` | `stateDB.TxHash()` |
| `UTXOID.OutputIndex` | compteur intra-transaction, voir 11d |

**Validation de `msg.value`**, dans cet ordre :

1. `msg.value % X2CRate != 0` → **rejet**, pas de troncature ;
2. le quotient doit tenir dans un `uint64` ;
3. `Amt == 0` → rejet, par parité avec `secp256k1fx.ErrNoValueOutput`.

**Pourquoi rejeter plutôt que tronquer.** Une troncature perd silencieusement
jusqu'à 1 nAVAX par appel — un montant dérisoire, mais une perte silencieuse dans
un chemin de transfert de valeur est exactement ce qu'on ne veut pas avoir à
expliquer plus tard. Le rejet est visible et corrigeable par l'appelant.

**Pourquoi il n'y a pas de nonce, et ce n'est pas un oubli.** `Export.Input.Nonce`
n'existe que parce qu'une transaction atomique est un objet autonome, hors EVM,
avec son propre mempool : elle a besoin de son propre anti-rejeu. Un appel de
précompile vit dans une transaction EVM qui a déjà le sien, et ne produit
d'ailleurs aucun `Input`.

S'en servir pour l'unicité de l'`UTXOID` serait par ailleurs **incorrect** : le
nonce d'un contrat ne s'incrémente que sur `CREATE`, donc plusieurs appels
successifs collisionneraient. Et l'incrémenter en effet de bord serait
**activement nuisible** — cela décalerait la suite des adresses `CREATE` du
contrat, cassant tout ce qui les prédit.

**Vérifier** : test 16 du lot 12 — un `msg.value` non multiple de `X2CRate`
rejeté.

---

## 11c. Débiter, et la ligne de coreth qu'on ne peut pas éviter

C'est le seul endroit du plan qui touche `graft/coreth/`, et il vaut mieux
comprendre exactement pourquoi avant de l'écrire.

**Où** : `graft/coreth/precompile/contract/interfaces.go`, interface `StateDB`
(l.29-56), plus la regénération de `mocks.go`.

**Quoi** : ajouter `SubBalance(common.Address, *uint256.Int)`.

**Pourquoi c'est inévitable.** L'interface exposée aux précompiles offre
`AddBalance`, `GetBalance` et `SubBalanceMultiCoin` — mais **pas `SubBalance`**,
que l'état sous-jacent possède pourtant. La capacité existe ; seule l'interface
est plus étroite. Brûler de l'AVAX sur la C-Chain n'est pas une primitive : c'est
`SubBalance`, ce qu'appelle déjà l'exécution d'un export atomique.

**Pourquoi la forme `payable` ne dispense pas de l'ajouter.** `payable` déplace
la cible du débit : l'EVM transfère `msg.value` du caller vers l'adresse du
précompile, avec vérification de solde et `revert` propre. Mais cette valeur doit
ensuite **quitter le bilan de l'EVM**, puisqu'elle est désormais matérialisée en
UTXO sur la P-Chain. La laisser s'accumuler à l'adresse du précompile créerait de
l'AVAX des deux côtés.

**Ce que `payable` apporte quand même**, et pourquoi on la garde : la
vérification de solde et le `revert` propre par l'EVM, et la sémantique
`call{value: …}` que tout outillage connaît.

**Un point agréable, à ne pas rater** : `worstcase.State` couvre déjà ce débit
**sans modification**. `txToOp` (`worstcase/state.go:239`) pose
`MinBalance = gas × gasFeeCap + value`, et `value` est ici précisément le
`msg.value` envoyé au précompile. C'est un argument de plus en faveur de la forme
`payable` : la comptabilité worst-case de SAE la voit gratuitement.

**Vérifier** : `go test ./graft/coreth/precompile/...` après regénération du
mock.

---

## 11d. L'`OutputIndex`, et pourquoi les trois options ont un défaut

C'est la seule vraie décision de design du lot, et l'ACP la laisse explicitement
ouverte.

**Le problème** : l'index d'une sortie exportée est son rang dans la slice
`ExportedOutputs` — ce que permet le caractère auto-contenu d'une transaction
atomique. Un précompile n'a pas ce conteneur : plusieurs appels, potentiellement
depuis plusieurs contrats, peuvent coexister dans une même transaction EVM, et
chacun doit produire un `UTXOID` distinct.

**Les trois options, et leur défaut** :

| | Mécanisme | Défaut |
| :--- | :--- | :--- |
| **(1)** | Compter les logs déjà émis par le précompile dans la transaction courante, via `stateDB.Logs()` | ⚠️ `Logs()` est de portée **bloc**, pas transaction |
| **(2)** | Un compteur dans le stockage du précompile | un `SSTORE` par export |
| **(3)** | Un `salt` fourni par l'appelant, `UTXOID = keccak256(txHash ‖ caller ‖ salt)` | collision d'un appelant avec lui-même |

**Recommandation : (2)**, avec un **seul slot** portant `(lastTxHash, count)` —
remis à zéro dès que `stateDB.TxHash()` diffère du `lastTxHash` stocké,
incrémenté sinon.

- ✅ lecture et écriture en **O(1)**, donc un coût en gas prévisible et
  facturable honnêtement ;
- ✅ `SSTORE` est **journalisé**, donc correct au `revert` exactement comme
  `AddLog` — un frame annulé restaure le compteur ;
- ✅ **borné** : un slot, jamais un index qui croît ;
- ❌ un `SSTORE` par export, à intégrer au calibrage du 11f.

> ⚠️ **Pourquoi (1) est écartée, et c'est un vrai vecteur de déni de service.**
> `StateDB.Logs()` est de portée **bloc**. Le coût du filtrage dépend donc du
> nombre de logs déjà émis par les **transactions précédentes du bloc**, que
> l'appelant ne contrôle ni ne prévoit. Un appel identique ne coûte pas la même
> chose selon sa position dans le bloc, ce qui dégrade l'estimation de gas — et
> facturer un forfait tout en exécutant un parcours de coût variable ouvre la
> porte à quelqu'un qui remplit le bloc de logs pour rendre le précompile
> ruineux.

**Pour (3)**, s'il faut y revenir : une collision entre appelants distincts
devient impossible (le `caller` est dans le hash), mais il resterait à décider du
comportement d'un appelant qui collisionne **avec lui-même** — une clé dupliquée
dans un même `Apply` de shared memory ne devant pas pouvoir invalider un bloc.

⚠️ **Collision d'`UTXOID`, à énoncer même si elle est négligeable.** La clé de
shared memory est `TxID.Prefix(index)`. Un précompile prenant le hash de la
transaction EVM comme `TxID`, une collision avec un `txID` de transaction
atomique est cryptographiquement négligeable — mais **l'espace de nommage est
désormais partagé**, et ça mérite d'être dit dans un commentaire.

**Vérifier** : test 15 du lot 12 — plusieurs `exportAVAX` dans une même
transaction EVM, depuis plusieurs contrats, doivent produire des `UTXOID`
distincts ; deux transactions distinctes ne doivent pas partager de compteur.

---

## 11e. Du log à la shared memory

Le précompile ne dépose rien lui-même : il **émet un log**, et la plomberie de
bloc en dérive les opérations de shared memory. C'est ce qui donne la sûreté au
`revert` sans structure nouvelle.

> **Règle.** Les opérations de shared memory d'un bloc issues du précompile sont
> dérivées des **logs** émis par lui, et d'eux seuls.

**Pourquoi les logs.** `AddLog` est journalisé (`core/state/statedb.go`) et le
`revert` d'un frame efface ses logs (`core/state/journal.go`). Une transaction en
échec est incluse au bloc avec `status = 0` — elle a consommé du gas — mais ses
logs ont été rembobinés avant construction du receipt. **Tout canal latéral au
journal laisserait au contraire fuiter un dépôt depuis un appel annulé.**

**Où** : `vms/saevm/cchain/nativeexport/` (ou dans le paquet du précompile) pour
la dérivation, puis `vms/saevm/cchain/hooks.go` `AfterExecutingBlock` (l.277), et
`vms/saevm/cchain/state/state.go` `Apply` (l.134).

**Quoi** — le précédent est exact, et il est juste à côté. `AfterExecutingBlock`
fait déjà :

```go
if err := h.state.Apply(b.NumberU64(), txs); err != nil { ... }

messages, err := warp.FromReceipts(receipts)   // ← même forme, même endroit
```

On ajoute une fonction de la même forme :

```go
func FromReceipts(rs types.Receipts) (map[ids.ID]*chainsatomic.Requests, error)
```

…qui filtre les logs sur l'adresse du précompile, décode chacun en
`(owner, amount, outputIndex)`, reconstruit l'`avax.UTXO`, le sérialise avec
`tx.MarshalUTXO` et produit un `chainsatomic.Element` avec ses `Traits`.

Puis `State.Apply` reçoit ces `Requests` en plus des transactions, et
`atomicRequests(txs)` (`state.go:146`) les fusionne avant `applyTrie`. **La
plomberie est déjà paramétrée par des `Requests` et non par des transactions**, et
le trie atomique suit gratuitement.

⚠️ **Un trou d'API assumé.** `State.Apply` indexe aussi les transactions par ID
(`writeTx`, `state.go:173`). Une opération sans transaction n'a rien à y
enregistrer. Cet index sert l'API (`GetAtomicTx`) et le reprocessing, **pas le
consensus** : l'effet d'une absence est un trou d'API, pas une divergence. Dis-le
dans un commentaire, sinon quelqu'un tentera de fabriquer une fausse transaction
pour combler le trou.

**Vérifier** : test 14 du lot 12 — un `exportAVAX` dans un frame qui *revert* ne
doit produire **aucun** `PutRequest`.

---

## 11f. La tarification — le seul point où une erreur est durable

**Le modèle retenu : gas EVM seul, sans burn AVAX additionnel.** Empiler deux
mécanismes pour la même opération serait arbitraire, et pour un contrat le
précompile n'est pas une alternative moins chère mais **la seule voie possible**.
La parité avec un export atomique équivalent se cherche par le **calibrage du
coût en gas**, pas par un second prélèvement.

> ⚠️ **Le coût à facturer n'est pas un coût de calcul.**
> C'est un coût de **croissance d'état permanente** : sérialisation de l'UTXO,
> `PutRequest` en shared memory, alimentation du trie atomique. Un forfait aligné
> sur le seul calcul — ce qui est le réflexe quand on écrit un précompile —
> **sous-tarifierait durablement**, et une sous-tarification d'écriture d'état ne
> se rattrape pas : elle produit de l'état permanent que personne n'a payé.
> C'est le seul endroit du plan où une erreur de calibrage est réellement
> dommageable.

**La cible** : à `baseFee` égale, un `exportAVAX` doit se situer dans le **même
ordre de grandeur** qu'un `Export` atomique équivalent. Rappel du coût d'un
`Export` atomique (`tx.go:165-200`) :
`intrinsicGas (= ap5.AtomicTxIntrinsicGas, 10 000) + 1 × taille + 1 000 × nbSignatures`.

À mesurer avant de figer les constantes : le coût d'écriture dans le trie
atomique rapporté à celui d'un `SSTORE`, plus le `SSTORE` du compteur (11d).

**Le changement de régime tarifaire est à assumer explicitement.** La C-Chain
fait coexister deux appareils de facturation, et un export par précompile fait
passer l'opération du second au premier :

| | Transaction EVM | Transaction atomique |
| :--- | :--- | :--- |
| Facturation | `gasUsed × baseFee` | montant brûlé implicite |
| Espace de bloc | gas | `extDataGasUsed` |
| Priorisation | pourboire | `BlockFeeContribution` |

Sous le régime du gas, chacun de ces rôles trouve son équivalent ou devient sans
objet. L'espace de bloc reste rationné par le gas, **à condition de tarifer le
précompile pour ce qu'il coûte réellement**. Le brûlage subsiste sous une autre
forme, la base fee du gas consommé étant brûlée. Et `extDataGasUsed` comme
`BlockFeeContribution` ne sont plus alimentés par cet export, sans conséquence :
ces deux mécanismes arbitrent un objet doté de son propre mempool et de sa propre
file d'attente, ce qu'un précompile n'est pas — il est déjà ordonnancé par la
transaction EVM qui le contient.

**Vérifier** : test 17 du lot 12, qui est un bench plutôt qu'un test.

---

## 11g. Ce que la forme apporte, et sa limite

Pas de code : de quoi savoir ce qu'on a gagné, et ce qu'on n'a pas.

**Ce qui disparaît.** Il n'y a plus d'`Export` à vérifier : la récupération de clé
publique par input, l'appariement `len(Ins) == len(Creds)` et le nonce de compte
n'ont plus d'objet — l'autorité vient du frame d'appel, où `msg.sender` n'est pas
usurpable. La détection de conflit tombe également : un export ne produit que des
`PutRequests`, il ne consomme aucun UTXO de shared memory.

**Ce qui s'ouvre.** Le chemin devient accessible à **tout outillage capable
d'émettre une transaction EVM**, ce qui recouvre les smart accounts et les
signataires matériels sans support Avalanche natif : un EOA n'a plus besoin de
savoir signer une transaction atomique pour alimenter un `WarpOwner`.

> ⚠️ **La limite assumée : on ne peut pas financer un tiers.**
> L'adresse source étant forcée à l'appelant, le précompile ne permet pas de
> créditer le `WarpOwner` d'une **autre** adresse. Le chemin par EOA (lot 10b) le
> permet toujours, mais le cas « factory qui approvisionne ses coffres » reste
> sans réponse native. Une variante à destinataire explicite serait possible ;
> elle n'est pas dans ce plan.

---

## Ce qui reste incertain

🔴 **À trancher avant d'écrire**

- **La dérivation de l'`OutputIndex`** (11d). Je recommande (2), le compteur en
  stockage. L'ACP laisse les trois options ouvertes et dit « à trancher au
  bench ». Écrire (1) puis migrer coûterait un changement de règle de consensus.
- **L'adresse du précompile**, dans une plage réservée. À figer une fois pour
  toutes, puis reporter dans l'ACP et l'interface Solidity.
- **L'upgrade d'activation.** Le plan l'aligne sur `HeliconTime`, mais l'ACP
  qualifie ce composant de « différable » et prévoit qu'il puisse suivre à un
  upgrade ultérieur. Si tu le diffères, la config de `genesis.go` change et le
  lot devient indépendant du reste.

🟠 **À vérifier au premier build**

- **Que le registre global de modules ne couple pas coreth** (11a). Raisonnement
  non exécuté.
- **L'élargissement de `State.Apply`** pour accepter des `Requests` hors
  transaction. La signature est `Apply(height uint64, txs []*tx.Tx)`
  (`state.go:134`) ; l'élargissement paraît mécanique mais touche l'indexation par
  `txID` et la logique des *bonus blocks* (l.156, l.187).
- **La forme exacte du log émis.** Il doit contenir de quoi reconstruire l'UTXO
  entier — `owner`, `amount`, `outputIndex` — et être décodable sans ambiguïté.
  Prends `corethwarp.UnpackSendWarpEventDataToMessage` comme modèle de ce à quoi
  ressemble un décodage de log dans ce dépôt.
- **`GetSnowContext()` rend-il un contexte dont `ChainID` est bien celui de la
  C-Chain ?** C'est la prémisse du `Owner.SourceChainID`. À confirmer.

🟡 **À mesurer**

- **Le coût en gas** (11f). C'est *la* mesure du lot, et elle conditionne la
  qualité de tout le reste. Trois grandeurs : le coût d'écriture dans le trie
  atomique, le `SSTORE` du compteur, et le point de comparaison qu'est un
  `Export` atomique équivalent.
- **La part déjà couverte** si le précompile réutilise la tarification d'émission
  d'un message Warp — le précompile Warp facture déjà une écriture de log, et il
  n'y a pas de raison de payer deux fois la même chose.

---

## Ce que l'écriture a trouvé

Quatre choses que l'analyse n'avait pas vues, dont deux auraient fait perdre du temps à quiconque suit ce guide à la lettre.

**Coreth n'a pas eu à être touché.** Le 11c pose comme inévitable d'élargir `contract.StateDB` pour y ajouter `SubBalance`. C'est faux : le `*state.StateDB` concret de libevm l'expose déjà (`statedb.go:391`), seule l'interface est plus étroite. Une assertion de type dans le précompile suffit, et la propriété « ce plan ne touche pas coreth » — celle qui rend inutile une garde d'activation côté C-Chain — tient donc pour la **totalité** de la proposition.

```go
type balanceDebitor interface {
    SubBalance(common.Address, *uint256.Int)
}
```

**saevm n'applique aucune activation de précompile, et c'est le vrai piège du lot.** `core.ApplyUpgrades` n'existe que dans le processeur de coreth. Le précompile Warp fonctionne sous saevm uniquement parce que Durango l'a activé bien avant la bascule et que son code arrive dans l'état hérité — rien n'active jamais un précompile programmé à Helicon ou après. Le symptôme est muet : un `revert` nu depuis le `extcodesize` de Solidity, sans trace côté nœud. La solution suit l'idiome déjà présent, `hooks.StartExecutingBlock` activant Warp au premier bloc Durango :

```go
if isFirstHeliconBlock := rulesExtra.IsHelicon && !config.IsHelicon(parent.Time); isFirstHeliconBlock {
    activatePrecompile(statedb, nativeexport.ContractAddress)
}
```

**Le sélecteur se dérive, il ne s'écrit pas.** Un sélecteur recopié à la main est un `revert` silencieux en attente ; `crypto.Keccak256([]byte(ExportAVAXSignature))[:4]` et un test qui compare les deux règlent la question une fois pour toutes.

**Le préfixe du compteur est une heuristique, pas une garantie.** Le 11d recommande un slot unique portant `(lastTxHash, count)`, ce qui tronque le hachage à 28 octets. C'est sans conséquence, mais pour une raison qu'il faut énoncer : l'unicité d'un `UTXOID` vient du hachage **entier**, pas du préfixe. Deux transactions partageant leurs 28 premiers octets se contentent de ne pas remettre le compteur à zéro.

### Ce qui reste à mesurer

`ExportAVAXGas = 32_000` est un alignement d'ordre de grandeur sur un `Export` atomique équivalent, **pas une mesure**. Le 11f dit pourquoi ça compte : c'est le seul endroit du plan où une erreur de calibrage est durable, une écriture d'état sous-tarifée produisant de l'état permanent que personne n'a payé. Le bench reste à faire, et l'adresse `0x02…06` à figer dans l'ACP.
