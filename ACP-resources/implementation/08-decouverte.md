# Lot 8 — Découverte hors chaîne

[← Lot 7](07-attestations.md) · [Plan](README.md) · [Lot suivant : la tarification →](09-tarification.md)

---

## Pourquoi ce lot existe

C'est le seul lot du plan qui ne touche à aucune règle de consensus, et c'est
aussi celui sans lequel rien ne fonctionne en pratique.

La raison tient au choix fondateur du design : **le message porte les octets de
la transaction**. Le propriétaire doit donc construire la transaction complète
*avant* de pouvoir l'autoriser, et pour la construire il doit savoir quels UTXOs
consommer, avec quels montants exacts. La découverte hors chaîne n'est pas un
confort d'outillage — c'est une **dépendance fonctionnelle** du chemin nominal.

**Et c'est un lot très court, parce que le lot 1 a fait le travail.**
`TransferOutput.Addresses()` rend `SourceAddress` tel quel — vingt octets. Or
toute la machinerie de découverte d'avalanchego est écrite pour des clés de vingt
octets :

| Accesseur | Type de clé | Verdict |
| :--- | :--- | :--- |
| `avax.GetAtomicUTXOs` | `set.Set[ids.ShortID]` | passe **sans modification** |
| `state.State.UTXOIDs(addr []byte, …)` | `[]byte` non contraint | passe **sans modification** |
| `avax.ParseServiceAddress` | rend un `ids.ShortID` | passe **sans modification** |

Une clé de 32 octets — un hash de l'`Owner` — aurait obligé à généraliser le
premier accesseur en `GetAtomicUTXOsByTrait(traits [][]byte, …)`, à retyper sa
pagination, et à écrire une API dédiée parce qu'un tel trait ne s'encode pas en
Bech32. Voir [1.2](01-warpfx-types.md) : c'est exactement ce que le choix du
trait supprime.

⚠️ **Ce lot suppose le lot 1 fait.** Sans `Addresses()`, l'UTXO est déposé en
shared memory **sans trait**, donc `sharedMemory.Indexed` ne le rendra jamais.
Il reste dépensable par qui connaît son `(txID, index)` ; il est simplement
introuvable.

---

## 8.1 L'API `platform.getWarpOwnerUTXOs`

Une méthode dédiée plutôt qu'un paramètre ajouté à l'existante. La raison est
structurelle, pas esthétique.

**Où** : `vms/platformvm/service_warp_utxos.go` *(nouveau)* et
`vms/platformvm/client.go`.

**Quoi** — arguments et réponse :

```go
type GetWarpOwnerUTXOsArgs struct {
    SourceChainID     string // la chaîne source du WarpOwner (la C-Chain)
    SourceAddress     string // 0x… , 20 octets
    AtomicSourceChain string // vide = index P-Chain ; sinon = shared memory
    Limit             json.Uint16
    StartIndex        Index
    Encoding          formatting.Encoding
}
```

Le service décode `SourceAddress` en vingt octets, puis :

- si `AtomicSourceChain` est vide → l'index P-Chain,
  `state.UTXOIDs(addr[:], …)` puis `avax.GetPaginatedUTXOs` ;
- sinon → `avax.GetAtomicUTXOs(…, set.Of(addr), …)`.

Plus la méthode correspondante sur le `Client`.

> ⚠️ **Cette méthode est une commodité, pas une nécessité — et c'est nouveau.**
> Tant que le trait faisait 32 octets, `platform.getUTXOs` était **structurellement
> incapable** de le servir : `api.GetUTXOsArgs` est partagée avec la X-Chain et
> exige des adresses Bech32, que `ParseServiceAddress` rend en `ids.ShortID`.
> Avec un trait de vingt octets, cette impossibilité disparaît : n'importe quels
> vingt octets s'encodent en Bech32, et `platform.getUTXOs` fonctionne **tel
> quel** sur un `WarpOwner`.
>
> Ce qui reste à `getWarpOwnerUTXOs` est de l'ergonomie, et elle est réelle :
> présenter une adresse EVM sous forme `P-avax1…` est trompeur pour l'utilisateur,
> qui croira tenir une adresse P-Chain dont il a la clé. La méthode dédiée prend
> la paire `(sourceChainID, sourceAddress)` au format que l'utilisateur connaît —
> `0x…` — et ne laisse aucun doute sur ce qu'elle interroge.
>
> **Si le lot doit être raccourci, c'est ici.** Rien d'autre du plan n'en dépend.

**Vérifier** : `service_warp_utxos_test.go` — un UTXO `warpfx` déposé en shared
memory doit être retrouvé par la paire, et un UTXO d'un autre propriétaire ne
doit pas l'être.

---

## 8.2 Le détail d'affichage qu'il vaut mieux corriger tout de suite

Sans effet sur le consensus, mais visible partout et pénible à diagnostiquer
après coup.

**Où** : `vms/platformvm/platform/import_tx.go:38-43` et `export_tx.go:38-44`.

**Quoi** — `InitCtx` force `in.FxID = secp256k1fx.ID` sur tous les inputs
importés, et `out.FxID = secp256k1fx.ID` sur toutes les sorties exportées. Le
champ est `serialize:"false"` et ne sert qu'à la sérialisation JSON. Conditionne
le forçage au type réel de la sortie / de l'input.

**Pourquoi le corriger.** Un input ou une sortie `warpfx` s'affichera sinon sous
un `fxID` **erroné** dans toutes les APIs et tous les explorateurs. Ce n'est pas
un bug de consensus, c'est un bug de diagnostic : la première personne qui
enquêtera sur un UTXO `warpfx` lira « secp256k1fx » et cherchera au mauvais
endroit.

**Vérifier** : sérialisation JSON d'une `ExportTx` portant une sortie `warpfx`.

---

## 8.3 Ce que la découverte ne remplace pas

Pas de code, mais une distinction à avoir en tête si tu écris de l'outillage ou
un contrat, parce qu'elle est facile à confondre.

Un contrat autonome tient son propre registre d'UTXOs, dans son stockage
C-Chain, alimenté par les attestations du lot 7 : à la livraison d'un
`TxExecuted` il **ajoute** les sorties de la transaction qu'il a autorisée
(`(txID, 0..len(Outs)-1)`, dont il connaît montants et propriétaires puisqu'il
les a écrits) et **retire** les UTXOs que cette transaction consommait.

> ⚠️ **Le registre du contrat est une borne inférieure, jamais un inventaire.**
> N'importe qui peut alimenter n'importe quel `WarpOwner`, et recevoir ne demande
> aucune autorisation, donc ne produit **aucune attestation**. Un contrat est
> **autonome sur les fonds qu'il a causés, aveugle à ceux qu'il a reçus**.
> L'écart est toujours en sa faveur — il détient au moins ce qu'il a compté —
> mais c'est exactement pourquoi la découverte hors chaîne de ce lot reste
> indispensable à l'**outillage** : le registre rend le contrat autonome, pas
> omniscient.

Corollaire pour un intégrateur : **un contrat ne doit jamais créditer quoi que ce
soit sur la foi d'un UTXO qu'on lui déclare.** Les `(txID, index, amount)` que
consomme une transaction autorisée sont des paramètres d'appel fournis par
l'opérateur, jamais dérivés d'une preuve. C'est sans danger — un montant faux ne
produit qu'une transaction P-Chain invalide, aucun fonds ne bouge — mais
l'omettre, c'est émettre des parts contre des dépôts imaginaires.

---

## Ce qui reste incertain

✅ **Vérifié — et la réponse change l'argument**

- **`platform.getUTXOs` sert bien un `WarpOwner`** avec l'adresse EVM encodée en
  Bech32 (`TestGetUTXOsServesWarpOwners`). L'impossibilité structurelle a bien
  disparu avec le trait de vingt octets.
- **Mais il sur-répond**, et c'est ce qui tranche le 🔴 ci-dessous : les vingt
  octets **ne nomment pas un propriétaire**. Un UTXO secp à la même adresse, et
  un UTXO `warpfx` de cette adresse sur une **autre chaîne source**, remontent
  aussi bien. `getUTXOs` répond sur l'index seul et les rend tous ; le test le
  pose explicitement.

🔴 **Tranché**

- **L'API dédiée est gardée, pour une raison plus solide que l'ergonomie.**
  `getWarpOwnerUTXOs` **filtre sur la paire complète**, donc elle répond à la
  question posée. L'argument ergonomique — ne pas faire passer une adresse EVM
  pour une adresse P-Chain — tient toujours, mais il n'est plus le seul.
- **Pas de mode « les deux sources à la fois ».** Deux appels donnent le
  résultat ; une réponse fusionnée devrait dire de quelle source vient chaque
  UTXO, sinon elle induit en erreur — des fonds en shared memory ne sont pas
  dépensables sur la P-Chain. C'est écrit sur le champ `atomicSourceChain`.
- **Le nom retenu est `platform.getWarpOwnerUTXOs`**, avec un curseur
  `WarpOwnerIndex{sourceAddress, utxo}` plutôt que l'`api.Index` partagée, dont
  le champ `address` est un Bech32 — exactement ce qu'on cherche à ne pas
  montrer. Reste à reporter dans l'ACP et la doc d'outillage.

⚠️ **Filtrer après paginer a une conséquence** : une page peut revenir **courte**
sans que le balayage soit fini, les entrées d'un autre propriétaire étant
écartées après lecture. Le curseur rendu est donc le dernier UTXO **lu**, pas le
dernier retenu.

⚠️ **L'asymétrie de 8.2 est délibérée.** Les **inputs** gardent `secp256k1fx.ID` :
`warpfx` ne déclare aucun type d'input, donc un input qui dépense un UTXO
`warpfx` *est* un `secp256k1fx.TransferInput` à `SigIndices` vide. Le code le dit
sur place, pour que personne ne « corrige » l'asymétrie.

🟡 **À mesurer**

- Rien dans ce lot. Aucune de ces lectures n'est sur un chemin de consensus.

---

## Ce que l'écriture a trouvé

**Un propriétaire warp ne doit jamais faire échouer un endpoint entier.**
`platform.getCurrentValidators` castait `ValidatorAuthority` en
`*secp256k1fx.OutputOwners` et renvoyait une erreur sinon — donc l'appel
échouait pour **tous** les validateurs dès qu'un seul avait une autorité warp.
Les propriétaires de récompenses, juste au-dessus, dégradaient déjà proprement :
`if ok`, champ laissé nul. L'autorité fait maintenant pareil.

C'est la règle à retenir pour tout endroit qui formate un propriétaire :
`getWarpOwnerUTXOs` est l'endpoint prévu pour ces propriétaires-là, les autres
se contentent de ne pas les rendre. Le défaut n'était atteignable qu'en e2e,
l'agrégateur de signatures lisant le jeu canonique par cet endpoint — une
autorité warp rendait donc toute autorisation ultérieure impossible.
