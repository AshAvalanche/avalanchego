# Lot 7 — Les attestations de retour

[← Lot 6](06-gardes.md) · [Plan](README.md) · [Lot suivant : la découverte →](08-decouverte.md)

---

## Pourquoi ce lot existe

Jusqu'ici l'information circule dans un seul sens : des octets de transaction
vont de la C-Chain vers la P-Chain. Ce lot écrit le retour — un message Warp
**signé par la P-Chain** qui atteste du sort d'une autorisation, et que n'importe
qui peut faire lire par un contrat C-Chain. C'est ce qui referme la boucle et
rend un protocole autonome possible.

Il serait tentant de croire qu'un propriétaire qui s'engage sur les octets d'une
transaction sait déjà tout ce qu'elle fera. C'est faux sur deux points, et les
deux sont structurels.

**1. Le `txID` est malléable.** Le propriétaire s'engage sur les octets **non
signés**. Le `txID`, lui, est le hash des octets **signés** — credential
compris — et le credential contient la signature BLS agrégée et le bitset des
signataires, qui dépendent du sous-ensemble de validateurs que le soumetteur a
effectivement joint. **Deux soumissions de la même autorisation produisent deux
`txID` distincts.** Une seule peut être acceptée, puisqu'elles consomment les
mêmes inputs — il n'y a pas de double dépense — mais le propriétaire ne peut pas
prédire laquelle. Or un UTXO est nommé par `(txID, index)` : sans attestation,
**un contrat ne peut donc pas nommer les UTXOs que sa propre transaction vient de
créer**, ni son change, ni les référencer dans l'autorisation suivante.

**2. La récompense de staking n'est prévisible ni en montant, ni en position.**
Le remboursement du stake est déterministe une fois le `txID` connu. La
récompense, non : son versement dépend de l'issue du `RewardValidatorTx`,
elle-même fonction de l'uptime mesuré, et son montant du `PotentialReward`
calculé par la P-Chain. Plus subtil encore, **son `OutputIndex` dépend de cette
même issue** — la récompense de délégataire est placée à un rang différent selon
que la récompense de validation a été versée ou non. Aucun engagement pris à
l'avance ne peut contenir cela.

Ces deux manques sont exactement ce qui sépare l'adresse pilotée par des
opérateurs du protocole autonome.

**Le point remarquable est ce que ce lot n'exige pas** : ni file d'attente, ni
message à produire dans un bloc, ni structure à conserver. **Rien n'est émis.
Tout est dérivé à la demande de l'état accepté**, exactement comme l'ACP-77 — avec
pour conséquence directe que la question de la rétention ou de la purge ne se
pose pas. Une attestation est un **constat recalculable**, pas une donnée
stockée. L'invariant « aucun état ajouté à la P-Chain » est conservé, et c'est ce
qui rend ce lot beaucoup plus léger qu'il n'en a l'air.

---

## 7.1 Les deux payloads

Deux `case` de plus dans un registre existant. Mêmes conventions que les payloads
ACP-77 : big-endian, `AddressedCall` à `SourceAddress` **vide**, chaîne source =
P-Chain.

**Où** : `vms/platformvm/warp/message/tx_executed.go`, `stake_settled.go` et
`cycle_settled.go` *(nouveaux)*, enregistrés dans `message/codec.go` → positions
**5**, **6** et **7** (la position 4 étant l'autorisation du lot 4a).

**Quoi** :

```text
TxExecuted
+---------------+------------+----------+
|       codecID :     uint16 |  2 bytes |
|        typeID :     uint32 |  4 bytes |
|          txID :   [32]byte | 32 bytes |
|      authHash :   [32]byte | 32 bytes |
+---------------+------------+----------+

StakeSettled
+-----------------+------------+--------------------------+
|         codecID :     uint16 |                  2 bytes |
|          typeID :     uint32 |                  4 bytes |
|     stakingTxID :   [32]byte |                 32 bytes |
|   sourceChainID :   [32]byte |                 32 bytes |
|   sourceAddress :   [20]byte |                 20 bytes |
|      numRewards :     uint32 |                  4 bytes |
|     outputIndex :     uint32 |    4 bytes  ⎫            |
|          amount :     uint64 |    8 bytes  ⎭× numRewards|
+-----------------+------------+--------------------------+
```

> **Le message nomme le propriétaire par sa paire, pas par un hash.** Vingt
> octets de plus, et une reproduction de codec en moins **du côté Solidity** :
> un contrat vérifie qu'une attestation le concerne par
> `sourceAddress == address(this)`, au lieu de recalculer
> `sha256(version‖chainID‖len‖addr)` — c'est-à-dire de réimplémenter un
> `linearcodec` Go dans un autre langage, pour une valeur sans autre usage. Voir
> [1.2](01-warpfx-types.md).
>
> C'est aussi exactement la paire que porte le `warpfx.Owner` d'un UTXO, donc le
> vérifieur compare des structures plutôt que des empreintes.

`authHash = sha256(txBytes)`, où `txBytes` est **exactement** le champ du message
d'autorisation. Le contrat l'a émis : il peut le recalculer d'un appel au
précompile `sha256`, ou l'avoir mémorisé. C'est la clé qui relie l'attestation à
l'autorisation. **Le `txID` est l'information nouvelle.**

`StakeSettled.Verify()` doit refuser une liste non triée par `outputIndex`
croissant (`ErrRewardsNotSorted`).

**Pourquoi `StakeSettled` nomme les UTXOs au lieu d'en donner la somme.** Trois
champs sont délibérément absents, et leur absence se justifie :

- **pas de `txID` par entrée** : tous les UTXOs de récompense d'un staking portent
  `TxID = stakingTxID`, déjà en tête du message. Seul l'`outputIndex` varie.
- **pas d'`owner` par entrée** : le message est cadré par la paire en tête, et la règle
  de signature ne retient que les UTXOs de ce propriétaire. Un
  `AddPermissionlessValidatorTx` peut désigner des propriétaires de récompenses
  de validation et de délégation distincts, potentiellement deux `WarpOwner`
  différents : chacun demande alors **son** message et reçoit exactement ses
  UTXOs.
- **pas de somme agrégée** : elle est le total de la liste. Une donnée redondante
  dans un message signé est un piège — deux sources de vérité que rien n'oblige à
  concorder.

**Et pourquoi le montant par UTXO est requis**, au-delà de la comptabilité : pour
dépenser un UTXO, il faut construire un `TransferableInput` dont l'`Amt` égale
**exactement** celui de la sortie. Une somme agrégée décrirait un solde que son
propriétaire ne peut pas dépenser.

`numRewards = 0` est une valeur légitime : « clos sans récompense ». Elle n'est
jamais confondue avec « pas encore clos », le message n'étant signé qu'une fois
le staker sorti de l'ensemble.

### `CycleSettled`

```text
+-----------------+------------+--------------------------+
|         codecID :     uint16 |                  2 bytes |
|          typeID :     uint32 |                  4 bytes |
|      rewardTxID :   [32]byte |                 32 bytes |
|   sourceChainID :   [32]byte |                 32 bytes |
|   sourceAddress :   [20]byte |                 20 bytes |
|      numRewards :     uint32 |                  4 bytes |
|     outputIndex :     uint32 |    4 bytes  ⎫            |
|          amount :     uint64 |    8 bytes  ⎭× numRewards|
+-----------------+------------+--------------------------+
```

« Le cycle clos par `rewardTxID` a produit pour ce propriétaire les UTXOs que
voici. »

**Pourquoi un troisième message plutôt qu'un `StakeSettled` élargi.** Les deux
familles de staking n'indexent pas leurs récompenses sous la même clé, et c'est
une propriété du code existant, pas un choix :

| | Clé de `GetRewardUTXOs` | Cardinalité |
| :--- | :--- | :--- |
| `AddPermissionlessValidatorTx` / `…Delegator` | le `txID` **de staking** (`validator.TxID`) | une fois, à la clôture |
| `AddAutoRenewedValidatorTx` | le `txID` **du `RewardAutoRenewedValidatorTx`** (`e.tx.ID()`) | une fois **par cycle** |

⚠️ **`GetRewardUTXOs(stakingTxID)` rend donc toujours une liste vide pour un
validateur auto-renouvelé.** Un `StakeSettled` signé après sa sortie annoncerait
`numRewards = 0` à un propriétaire qui a pourtant été payé à chaque cycle :
cohérent avec la définition du message, et parfaitement trompeur. Les deux
messages nomment la même chose — des UTXOs de récompense — mais pas sous la même
clé, et confondre les deux clés est le piège que cette séparation évite.

**Ce que `CycleSettled` n'a pas besoin de dire** :

- **pas de `stakingTxID`** : `GetTx(rewardTxID)` rend un
  `*platform.RewardAutoRenewedValidatorTx`, dont le champ `TxID` **est** le `txID` de
  staking. Redonner une donnée dérivable dans un message signé, c'est deux
  sources de vérité que rien n'oblige à concorder.
- **pas de « le staker est sorti »** : un `RewardAutoRenewedValidatorTx`
  `Committed` est définitif **pour son cycle**, que le validateur continue ou
  non. La règle de signature en est plus simple que celle de `StakeSettled`, pas
  plus compliquée.

⚠️ **Le remboursement du principal n'est dans aucun des deux messages**, et n'a
pas à y être. `unstakeUTXOs` n'appelle que `AddUTXO`, sous le `txID` **de
staking**, aux index `len(outputs) + i`. Un contrat qui tient le `txID` de
staking — que `TxExecuted` lui a donné — calcule ces index lui-même. La part
auto-composée, elle, ne devient jamais un UTXO : elle augmente le poids.

`CycleSettled.Verify()` impose le même tri par `outputIndex` croissant, et pour
la même raison : sans octets identiques, aucun quorum ne se forme.

**Vérifier** : golden tests des offsets pour les trois messages, plus un test de
`Verify()` sur une liste volontairement désordonnée.

---

## 7.2 Les règles de signature

Le handler ACP-118 existe déjà et sert les messages ACP-77. On lui ajoute deux
`case`. Aucune règle ne demande d'index nouveau : toutes s'appuient sur des
accesseurs existants.

**Où** : `vms/platformvm/network/warp_utxos.go` *(nouveau)*, plus deux `case`
dans le `switch` de `signatureRequestVerifier.Verify` (`network/warp.go:82`).

**`verifyTxExecuted`** — le handler signe si et seulement si :

1. `GetTx(txID)` retourne une transaction — sinon `ErrTxDoesNotExist` ;
2. son statut est `Committed` — posé à l'acceptation du bloc — sinon
   `ErrTxNotCommitted` ;
3. `sha256(tx.Unsigned.Bytes()) == msg.AuthHash` — sinon `ErrMismatchedAuthHash` ;
4. la transaction porte au moins un `*warpfx.Credential` — sinon
   `ErrTxHasNoWarpCredential`.

> ⚠️ **La condition 4 n'est pas une vérification de sécurité, c'est une borne de
> périmètre — et il faut la garder pour cette raison-là.**
> Sans elle, la P-Chain deviendrait un **oracle d'acceptation de transactions à
> usage général** : utile, sans doute, et probablement une proposition à part
> entière. Ce n'est pas celle-ci. Ne la retire pas au motif qu'elle « ne protège
> rien » : elle borne la surface que cette proposition demande de maintenir.

**`verifyStakeSettled`** :

1. `msg.Verify()` — la liste est triée ;
2. `GetTx(stakingTxID)` rend un `AddPermissionlessValidatorTx` ou un
   `AddPermissionlessDelegatorTx` `Committed` — sinon `ErrNotAStakingTx` ;
3. portant un `*warpfx.Credential` ;
4. le staker correspondant n'est **ni** dans l'ensemble courant, **ni** dans
   l'ensemble pending — sinon `ErrStakingNotSettled` ;
5. la liste contient exactement les `GetRewardUTXOs(stakingTxID)` dont la sortie
   est un `*warpfx.TransferOutput` dont l'`Owner` égale la paire du message —
   `SourceChainID` **et** `SourceAddress` —, chacun réduit à son
   `(outputIndex, amount)` — sinon `ErrMismatchedRewards`.

**Pourquoi la condition 4 rend `numRewards = 0` non ambigu.** C'est le même
raisonnement que le « is not and can never become » de `L1ValidatorRegistration` :
le staking est clos et ne peut plus rien produire.

**`verifyCycleSettled`** — plus court, parce qu'un cycle réglé l'est
définitivement :

1. `msg.Verify()` — la liste est triée ;
2. `GetTx(rewardTxID)` rend un `*platform.RewardAutoRenewedValidatorTx` `Committed` —
   sinon `ErrNotACycleTx` ;
3. `GetTx(rewardTx.TxID)` rend un `*platform.AddAutoRenewedValidatorTx` portant un
   `*warpfx.Credential` — sinon `ErrTxHasNoWarpCredential` ;
4. la liste contient exactement les `GetRewardUTXOs(rewardTxID)` dont la sortie
   est un `*warpfx.TransferOutput` d'`Owner` égal à la paire du message — sinon
   `ErrMismatchedRewards`.

⚠️ **La condition de périmètre porte sur la transaction de staking, pas sur la
transaction de récompense.** Un `RewardAutoRenewedValidatorTx` n'a **aucun**
credential — `RewardAutoRenewedValidatorTx` refuse explicitement
`len(e.tx.Creds) != 0` — donc y chercher un `*warpfx.Credential` refuserait
toujours. C'est le `AddAutoRenewedValidatorTx` qu'il nomme qui porte
l'autorisation, et c'est lui qu'il faut interroger.

⚠️ **Les transactions de proposition sont bien dans `GetTx`.** C'est ce qui rend
la condition 2 possible : `block/executor/verifier.go:438-439` fait
`onCommitState.AddTx(tx, status.Committed)` et
`onAbortState.AddTx(tx, status.Aborted)`. Un cycle *aborted* existe donc aussi
dans l'état, avec ses propres UTXOs de récompense — et il ne doit **pas** être
attestable comme s'il était commité, d'où le statut exigé.

> ⚠️ **Le tri de la liste n'est pas cosmétique — c'est ce qui permet au quorum
> d'exister.**
> `GetRewardUTXOs` retourne une slice dont l'ordre vient de la base, et
> l'agrégation BLS exige que tous les validateurs signent des **octets
> identiques**. Sans tri normatif, deux nœuds produisent deux messages
> différents et **aucun quorum ne se forme jamais**. C'est un échec silencieux :
> tout compile, chaque nœud répond correctement à la requête, et le relayeur
> n'atteint simplement jamais 67 %. Le diagnostic est pénible parce que rien
> n'échoue — ça n'aboutit pas.

**Deux points d'implémentation qui coûtent chacun un détour** :

- **`GetRewardUTXOs` n'appartient pas à `state.Chain`**, l'interface que reçoit le
  handler (elle ne déclare que `AddRewardUTXO`). L'accesseur existe sur l'état
  concret ; il faut l'exposer via une **interface locale au paquet réseau**, par
  exemple `network.Chain` qui embarque `state.Chain` et ajoute la méthode.
- **Déterminer qu'un staking est clos passe par le `nodeID`.** L'état n'indexe pas
  les stakers par transaction : la disposition disque est
  `validators/current|pending/{…}/list/txID`, mais toutes les API mémoire sont
  typées `(subnetID, nodeID)`. La transaction de staking porte cependant
  elle-même son `subnetID` et son `nodeID`, que `GetTx(stakingTxID)` rend donc
  directement. Pour un **validateur**, la recherche est en **O(1)** :
  `GetCurrentValidator(subnetID, nodeID)` puis `GetPendingValidator`, avec
  comparaison du `TxID`. Pour un **délégateur**, elle itère sur les délégateurs
  **de ce seul validateur**, pas sur l'ensemble des stakers.

Aucune `justification` n'est nécessaire dans les deux cas : le message porte assez
d'information pour que le vérifieur retrouve seul l'état à consulter.

**Vérifier** : test 7 du lot 12 — un staking complet jusqu'au
`RewardValidatorTx`, sur les **deux** branches commit et abort.

---

## 7.3 Ce qui rend le lot 7 correct : l'index des récompenses

Ce n'est pas du code à écrire, c'est la lecture à faire avant d'écrire le test 7.
Sans elle, on ne comprend pas pourquoi `StakeSettled` est nécessaire.

**Où lire** : `vms/platformvm/txs/executor/proposal_tx_executor.go`,
`rewardValidatorTx`, `rewardDelegatorTx`, `newUTXO`, `unstakeUTXOs`.

Ce qu'on y trouve :

- `unstakeUTXOs` recrée chaque sortie de stake à
  `OutputIndex = len(stakerTx.Outputs()) + i`, et **n'appelle que `AddUTXO`,
  jamais `AddRewardUTXO`** : le remboursement du stake **n'est pas** dans
  `GetRewardUTXOs`.
- La récompense de validation est à `len(outputs) + len(stake)`, avec un
  `utxosOffset++`.
- La récompense de délégataire est à `len(outputs) + len(stake) + offset` sur la
  branche **commit**, et à `len(outputs) + len(stake)` sur la branche **abort** —
  où elle **remonte** dans le rang que la récompense de validation aurait occupé.

> ⚠️ **C'est de là que vient toute la nécessité du message.**
> L'`OutputIndex` d'un UTXO de récompense dépend de l'issue commit/abort,
> elle-même fonction de l'uptime mesuré. Ce n'est pas « difficile à calculer » :
> c'est **impossible à contenir dans un engagement pris à l'avance**. Le contrat
> ne peut pas le déduire ; il faut le lui dire.

Note aussi que sur la branche abort le code construit l'`avax.UTXO`
**directement**, sans repasser par `newUTXO` / `CreateOutput`. Vérifie que ton
lot 3.5 n'a pas manqué ce chemin — s'il produit un `secp256k1fx.TransferOutput`
en dur, une récompense vers un `WarpOwner` y serait mal typée.

---

## 7.4 La lecture côté C-Chain : zéro ligne à écrire

Rien à implémenter, mais quatre pièges d'intégration à documenter — parce que
c'est le contrat destinataire qui les subira, et qu'ils sont contre-intuitifs.

Le contrat lit l'attestation par le précompile Warp standard,
`getVerifiedWarpMessage(index)`. La vérification du quorum a lieu **dans le
prédicat**, avant exécution, et le cas « message venant de la P-Chain » est déjà
traité nommément : le set de signataires retenu est celui du subnet de la chaîne
réceptrice, soit le Primary Network. **Une attestation est donc exactement aussi
sûre que la P-Chain elle-même.**

Les quatre pièges :

- **Un contrat ne peut pas aller chercher une attestation lui-même.** Le prédicat
  appartient à la **transaction**, pas à l'appel : il est vérifié avant exécution,
  et son résultat est un bitset indexé par `(hash de transaction, adresse du
  précompile)`. La livraison est donc nécessairement une transaction EVM de
  premier niveau construite par le livreur. **Le point d'entrée du contrat
  destinataire doit être appelable par n'importe qui** — cohérent avec la gratuité
  du rôle de livreur, mais ça doit être écrit.
- **Un prédicat absent ou invalide ne provoque pas de `revert`** : l'appel réussit
  et renvoie un drapeau de validité à `false`. Un contrat qui ne teste pas ce
  drapeau accepte un message non vérifié.
- `sourceChainID` vaut **`bytes32(0)`** : l'ID de la P-Chain est `ids.Empty`.
- `originSenderAddress` vaut **`address(0)`**, l'adresse source de
  l'`AddressedCall` étant vide et convertie telle quelle.

> ✅ **Ce qu'un contrat n'a *pas* à faire.** Vérifier qu'un `StakeSettled` le
> concerne se réduit à `payload.sourceAddress == address(this)` : pas de
> `sha256`, pas de sérialisation de `linearcodec` à reproduire. ⚠️ Ne pas
> confondre le `sourceChainID` du **payload** — l'ID de la C-Chain, à comparer à
> `getBlockchainID()` — avec celui de l'**enveloppe** Warp, qui vaut
> `bytes32(0)` parce que l'émetteur est la P-Chain.

> ⚠️ **Les deux derniers sont des valeurs nulles *significatives*.**
> Un contrat qui teste « chaîne source non nulle » ou « expéditeur non nul »
> — le réflexe défensif habituel — rejette **toutes** les attestations. Le test
> correct est `sourceChainID == bytes32(0) && originSenderAddress == address(0)`,
> puis le `typeID` du payload. Ce couple est non usurpable : aucune adresse
> C-Chain ne peut émettre depuis `address(0)`, et aucune chaîne autre que la
> P-Chain ne porte l'ID nul.

Et une discipline, côté contrat, qui ne relève pas du protocole : **une
attestation ne périme pas et n'a pas d'`expiry`** — ce serait sans objet, un fait
accepté reste vrai. C'est donc au contrat de marquer `authHash` (resp.
`stakingTxID`) comme consommé à la première présentation. Une ligne de `mapping`,
et la même discipline que pour n'importe quelle preuve ICM. L'omettre, c'est
créditer deux fois une récompense rejouée.

---

## Ce qui reste incertain

🔴 **À trancher avant d'écrire**

- **Les `typeID` définitifs** des **trois** payloads — 5, 6 et 7, à la suite de
  l'autorisation en 4. À confirmer avec l'allocation officielle du registre. Les
  golden tests les épinglent, donc un décalage se voit tout de suite.

✅ **Vérifié**

- **L'interface locale est `network.Chain`** : `state.Chain` élargie de
  `GetRewardUTXOs`. Plus mécanique qu'annoncé — les deux appelants de
  `network.New` passent déjà un `*state.State`, qui la satisfait, donc zéro
  changement chez eux.
- **Le chemin délégateur existe des deux côtés** : `GetCurrentDelegatorIterator`
  et `GetPendingDelegatorIterator`, tous deux typés `(subnetID, nodeID)`. Un
  itérateur vide couvre aussi le cas du validateur lui-même sorti, un délégateur
  ne survivant pas au sien.
- **`GetTx` rend bien le statut** — `(*platform.Tx, status.Status, error)` est sur
  `state.Chain`, et `platform.Parse` initialise les octets, donc `tx.Unsigned.Bytes()`
  est exploitable après lecture.

⚠️ **Le piège de `StakeSettled` sur un auto-renouvelé se referme plus fort que
prévu.** Ce guide annonce un message *signable* avec `numRewards = 0`, cohérent
et trompeur. Ce n'est pas ce qui se passe : la condition 2 de `verifyStakeSettled`
restreint le type aux deux stakings permissionless, donc un tel message est
**refusé d'emblée** sur `ErrNotAStakingTx`. Il n'est pas seulement trompeur, il
est indemandable.

✅ **Le piège de 7.3 est vérifié, et il se termine dans l'autre sens.** La branche
*abort* de `rewardValidatorTx` construit bien l'`avax.UTXO` en direct, sans
repasser par `newUTXO` — mais elle **réutilise `onCommitUtxo.Out`**, la sortie
déjà produite par le dispatch du lot 3.5. La récompense est donc correctement
typée sur les deux branches. Rien à corriger, mais la lecture valait d'être
faite : le raccourci saute aux yeux et la conclusion est l'inverse de ce qu'on
craint.

🟡 **À mesurer**

- **La sollicitation du handler.** Chaque demande d'attestation coûte quelques
  lectures d'état en O(1) et une signature BLS. ⚠️ Ce handler n'est **ni limité
  en débit, ni doté d'un cache de signatures** — `acp118.NewHandler` délègue à
  `NewCachedHandler(&cache.Empty{})`, donc chaque requête re-exécute `Verify`
  **et** `Sign`. C'est le comportement existant hérité d'ACP-77, et cette
  proposition ne l'aggrave pas ; mais c'est la borne en O(1) des règles de
  signature qui tient lieu de protection, et elle est à préserver si le périmètre
  s'élargit.
