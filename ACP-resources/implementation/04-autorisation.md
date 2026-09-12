# Lot 4 — L'autorisation

[← Lot 3](03-aiguillage-fx.md) · [Plan](README.md) · [Lot suivant : l'import canonique →](05-import-canonique.md)

---

## Pourquoi ce lot existe

Les lots 1 à 3 ont posé la plomberie : un type de sortie, des positions de codec,
un aiguillage. Rien n'autorise encore quoi que ce soit. Ce lot écrit
l'authentification elle-même, et c'est celui où une erreur coûte des fonds.

Le principe tient en une phrase : **le message Warp porte les octets exacts de la
transaction non signée**. Pas une description de ce qu'elle doit faire, pas son
empreinte — la transaction elle-même, telle que le propriétaire l'a construite.

La raison est un cas d'école qu'il faut avoir en tête, parce que tout le design
en découle. Supposons que le message décrive une *intention* (« stake 2 000 AVAX
sur ce nœud ») et laisse au soumetteur le soin de composer la transaction. Il
consomme un UTXO de 5 000 AVAX du propriétaire, stake bien les 2 000 attendus, et
place les 3 000 de change **sur sa propre adresse**. La comptabilité est parfaite
(`5000 = 2000 + 3000 + frais`), la transaction est valide, et le vol ne laisse
aucune trace anormale. Le rattraper demanderait un empilement de règles de
consensus — invariants de conservation par propriétaire, limitation du nombre et
du locktime des sorties de change, registre de nonces, plafond de contribution
aux frais — dont chacune n'existerait que pour compenser une liberté laissée au
soumetteur.

**Faire porter au message la transaction elle-même supprime la cause plutôt que
les symptômes.** La transaction est figée *avant* l'autorisation ; le
propriétaire voit et approuve chaque sortie, change compris. Le soumetteur
retrouve exactement le statut qu'il a face à une transaction signée par un EOA :
un transporteur sans pouvoir. C'est aussi ce qui explique la génération abandonnée
(G1) et sa disparition complète : nonces, `maxFee`, *intent matching*, invariants
I1-I3 étaient tous des réponses à un problème que ce choix fait disparaître.

Trois vérifications en découlent, et **aucune n'est facultative** : le **quorum**
(le message est-il signé par 67 % du poids de la chaîne source ?), l'**engagement**
(ce message autorise-t-il *cette* transaction ?), la **provenance** (vient-il du
propriétaire de *cet* UTXO ?). Elles ne vivent pas au même endroit, et le lot
consiste largement à comprendre pourquoi.

---

## 4a. Le payload d'autorisation

Un message Warp transporte un `AddressedCall`, l'enveloppe standard qui nomme la
chaîne et l'adresse émettrices. Le payload ajouté ici est le quatrième du
registre P-Chain, à la suite des trois d'ACP-77.

**Où** : `vms/platformvm/warp/message/tx_authorization.go` *(nouveau)*, enregistré
dans `vms/platformvm/warp/message/codec.go` → **position 4** (les positions 0-3
sont `SubnetToL1Conversion`, `RegisterL1Validator`, `L1ValidatorRegistration`,
`L1ValidatorWeight`).

**Quoi** — mêmes conventions que les payloads ACP-77 : entiers big-endian,
`[]byte` préfixés par leur longueur sur `uint32`.

```text
+---------------+----------+-------------------------+
|       codecID :   uint16 |                 2 bytes |
|        typeID :   uint32 |                 4 bytes |
|        expiry :   uint64 |                 8 bytes |
|       txBytes :   []byte |  4 + len(txBytes) bytes |
+---------------+----------+-------------------------+
```

Prends `l1_validator_registration.go` comme modèle : même structure de fichier,
`Verify()`, `initialize()`, `Bytes()`.

**Pourquoi la préimage et non `sha256(txBytes)`.** Un payload de 32 octets serait
équivalent en sécurité — c'est exactement ce qu'un EOA signe — et bien plus
compact. Il n'est pourtant pas retenu : **le soumetteur ne peut pas reconstruire
une transaction à partir d'une empreinte**. Il faudrait un canal hors-bande entre
l'opérateur et lui, donc de l'infrastructure nouvelle, et cela casserait le
modèle ICM/ACP-77 où le relayeur observe les logs `SendWarpMessage` et se
débrouille seul. En portant la préimage, le log C-Chain contient la transaction
complète, et la logique du soumetteur devient **un chemin unique, agnostique au
type de transaction** — plus simple que l'intégration ACP-77 déjà en production.

**Pourquoi rien d'autre dans le payload.** Ni nonce, ni plafond de frais, ni
description du destinataire : tout est déjà dans `txBytes`. Une donnée redonnée
ici serait une **seconde source de vérité** que rien n'obligerait à concorder.

**Pourquoi `expiry` est nécessaire.** Un message Warp ne périme jamais de
lui-même. Une autorisation émise pour une transaction jamais soumise resterait
exécutable tant que ses inputs existent. `expiry` est le **seul** garde-fou
temporel du design.

**Vérifier** : golden test des offsets, comme pour les payloads ACP-77.

---

## 4b. Le vérifieur Warp doit voir la transaction signée

`VerifyWarpMessages` est aujourd'hui typé `tx platform.UnsignedTx`. Il n'a donc aucun
accès à `Creds`, où le message réside nécessairement. Le changement est mécanique
mais indispensable : sans lui, aucun des tests spécifiés ici n'est atteignable.

**Où** : `vms/platformvm/txs/executor/warp_verifier.go`, plus quatre points
d'appel.

**Quoi** :

1. `VerifyWarpMessages(ctx, networkID, validatorState, pChainHeight, tx *platform.Tx)`.
   Le visiteur `warpVerifier` gagne un champ `tx *platform.Tx`, et
   `tx.Unsigned.Visit(&warpVerifier{...})` remplace `tx.Visit(...)`.
2. Sept méthodes du visiteur — `ImportTx`, `ExportTx`, `BaseTx`,
   `AddPermissionlessValidatorTx`, `AddPermissionlessDelegatorTx`,
   `AddAutoRenewedValidatorTx`, `SetAutoRenewedValidatorConfigTx` — appellent une
   nouvelle `verifyAuthorization()` qui balaie `w.tx.Creds`, trouve l'unique
   `*warpfx.Credential` à `WarpMessage` non vide, et appelle `w.verify(msg)` —
   la fonction existante, inchangée.
3. `RegisterL1ValidatorTx` et `SetL1ValidatorWeightTx` gardent leur
   `w.verify(tx.Message)` d'ACP-77.
4. Les quatre points d'appel passent `tx` au lieu de `tx.Unsigned` :
   `block/executor/block.go:40`, `block/executor/warp_verifier.go:24` (le
   passe-plat de bloc), `block/executor/manager.go:151`,
   `block/builder/builder.go:545`. **Les quatre tiennent déjà la transaction
   signée.**

**Pourquoi ce vérifieur ne fait que le quorum.** Deux propriétés du code le
dictent, et il vaut mieux les connaître avant de vouloir y mettre plus :

- **Il ne connaît pas le temps.** Sa signature ne porte que
  `(ctx, networkID, validatorState, pChainHeight, tx)`. Ni le timestamp du bloc
  ni l'état ne lui sont passés, et ses points d'appel n'ont pas tous un bloc sous
  la main — la vérification d'une transaction issue du gossip a lieu hors de tout
  bloc. `chainTime <= expiry` n'y est **pas évaluable**.
- **Il n'est pas toujours exécuté.** Il n'est appelé que si le nœud est
  bootstrappé et que les messages n'ont pas déjà été vérifiés à cette hauteur.
  C'est le modèle ACP-77, et il est correct pour un quorum : pendant le
  bootstrap, le nœud fait confiance à la chaîne acceptée, et re-vérifier un
  quorum à une hauteur déjà validée n'apporte rien.

> ⚠️ **Ne mets pas l'engagement ici, malgré la tentation.**
> L'engagement **est** l'authentification. Le placer dans un vérifieur sauté
> pendant le bootstrap reviendrait à ne pas le vérifier du tout pendant le
> bootstrap. C'est la différence entre « re-vérifier une preuve déjà validée » et
> « ne jamais vérifier la preuve ».

**Vérifier** : `go test ./vms/platformvm/txs/executor/ -run Warp` et
`./vms/platformvm/platform/...`

---

## 4c. La résolution déterministe — le cœur du lot

C'est ici que vivent l'engagement et l'expiry, sur le chemin d'exécution qui,
lui, est toujours parcouru. La fonction ne lit aucun état : elle est une fonction
pure de la transaction et du `chainTime`.

**Où** : `vms/platformvm/txs/executor/authorization.go` *(nouveau)*.

**Quoi** — `resolveAuthorization(tx *platform.Tx, chainTime uint64) (*fx.Context, error)` :

| Étape | Détail | Erreur |
| :--- | :--- | :--- |
| 1 | `findWarpAuthorization(tx.Creds)` — balayage unique, rend l'unique credential à `WarpMessage` non vide | `ErrMultipleWarpAuthorizations` si deux |
| 2 | Zéro porteur → `&fx.Context{}`, `Authorization` nil | — |
| 3 | Porteur sur une transaction hors liste | `ErrWarpAuthorizationNotAccepted` |
| 4 | `warp.ParseMessage` → `payload.ParseAddressedCall` → `message.Parse` | erreurs de parsing |
| 5 | **Engagement** : `bytes.Equal(auth.TxBytes, tx.Unsigned.Bytes())` | `ErrAuthorizationMismatch` |
| 6 | **Expiry** : `chainTime > auth.Expiry` | `ErrAuthorizationExpired` (l'égalité est **valide**) |
| 7 | → `&fx.Context{Authorization: &warpfx.Authorization{SourceChainID: msg.SourceChainID, SourceAddress: call.SourceAddress}}` | — |

**Pourquoi le slot porteur se trouve par balayage et non par position.** `Creds`
est parallèle à la concaténation `Ins ‖ ImportedInputs`. Dans une transaction
mêlant des inputs secp256k1, le slot d'indice 0 est un credential secp. C'est le
balayage qui identifie le porteur, **jamais sa position**.

**Pourquoi un seul porteur, et ce qui en découle gratuitement.** Il en découle,
sans qu'aucune règle n'ait à le dire, que tous les UTXOs `warpfx` consommés par
une transaction autorisée appartiennent au **même** `WarpOwner` : il n'y a qu'une
autorisation, et le test de provenance la compare au propriétaire de *chaque*
UTXO. En revanche les **sorties** ne sont pas contraintes — une transaction
autorisée peut créditer plusieurs `WarpOwner`, des adresses secp, ou un mélange.
Le propriétaire s'est engagé sur les octets entiers : il a vu et approuvé chaque
destinataire.

> ⚠️ **Pourquoi l'engagement n'est pas redondant avec la provenance.**
> Il est tentant de le croire : les deux disent, semble-t-il, « ce message vient
> bien du propriétaire ». C'est faux, et l'erreur serait fatale. La provenance
> seule répondrait à « le message vient-il du propriétaire de cet UTXO ? », mais
> **pas** à « ce message autorise-t-il *cette* transaction ? ». Or un message
> Warp est **public** par construction — il figure dans un log C-Chain que tout
> relayeur observe. Sans l'engagement, n'importe qui pourrait le ramasser et
> l'attacher à une transaction de son choix consommant les UTXOs de ce
> propriétaire. Le message cesserait d'être une autorisation pour devenir un
> **jeton au porteur sur l'intégralité de ses fonds**.
> Test 1 du lot 12.

> ⚠️ **`chainTime`, jamais l'horloge locale.**
> `secp256k1fx` teste son locktime contre `h.clk.Time()`
> (`utxo/verifier.go:149`). Ne reproduis pas ce comportement : l'`expiry` est une
> **règle de consensus**, et le timestamp local n'est pas une valeur de
> consensus. Deux nœuds qui liraient leur horloge au même instant pourraient
> accepter et rejeter la même transaction.

### La règle de placement, qui est la moins intuitive du lot

> **L'autorisation est résolue avant toute garde de bootstrap de l'exécuteur qui
> la résout.**

Les exécuteurs de la P-Chain ne sautent **pas uniformément** pendant le
bootstrap, et c'est ce qui rend la règle nécessaire :

| Transaction | Ce qui est sauté quand le nœud n'est pas bootstrappé |
| :--- | :--- |
| `AddPermissionlessValidatorTx` / `DelegatorTx` | **tout**, `return nil` avant même le flow check |
| `ImportTx` | shared memory **et** flow check (`standard_tx_executor.go:337`) |
| `ExportTx` | seulement `verify.SameSubnet` (l.424) |
| `BaseTx` | rien |

Placée à l'endroit naturel — à côté du flow check — la résolution serait donc
sautée **précisément sur les transactions qui en ont le plus besoin**, les deux
transactions de staking. La règle est sans coût : la résolution ne lit aucun
état. Mais elle est invisible à la lecture d'un exécuteur pris isolément, et une
revue qui ignore cette asymétrie ne la verra pas.

**Vérifier** : `authorization_test.go` — engagement sur une transaction A, puis
le même credential attaché à une transaction B : doit échouer.

---

## 4d. Le câblage et la liste autorisée

**Sept** transactions acceptent un credential porteur. La liste est appliquée
**structurellement** d'abord, et déclarativement seulement pour la lisibilité des
erreurs.

| Transaction | Ce qu'elle permet | Chemin |
| :--- | :--- | :--- |
| `ImportTx` | entrée de fonds au-delà de la forme canonique | dépense |
| `ExportTx` | retour vers la C-Chain | dépense |
| `BaseTx` | consolidation, invalidation d'une autorisation | dépense |
| `AddPermissionlessValidatorTx` | staking à durée fixe | dépense |
| `AddPermissionlessDelegatorTx` | délégation | dépense |
| `AddAutoRenewedValidatorTx` | staking auto-renouvelé | dépense |
| `SetAutoRenewedValidatorConfigTx` | piloter et **arrêter** ce validateur | dépense **+ permission** |

**Où** : `vms/platformvm/txs/executor/standard_tx_executor.go` (`ImportTx`,
`ExportTx`, `BaseTx`) et `staker_tx_verification.go` (les quatre transactions de
staking : `verifyAddPermissionlessValidatorTx`, `…DelegatorTx`,
`verifyAddAutoRenewedValidatorTx` l.875, `verifySetAutoRenewedValidatorConfigTx`
l.960). ⚠️ Les quatre retournent `nil` très tôt sur `!backend.Bootstrapped.Get()`
(l.542, 671, 895, 1000) : la règle de placement ci-dessus vaut pour les quatre.

**Quoi** — dans chacune des cinq, avant toute garde de bootstrap :

```go
fxCtx, err := resolveAuthorization(e.tx, uint64(currentTimestamp.Unix()))
if err != nil {
    return err
}
```

⚠️ `currentTimestamp` est un `time.Time` (`e.state.GetTimestamp()`), et
l'`expiry` du payload est un `uint64` Unix : la conversion est à faire **une
fois**, chez l'appelant.

⚠️ **Le `chainTime` s'arrête ici.** Il est un paramètre de `resolveAuthorization`
et rien de plus : il sert à comparer l'`expiry`, puis il est oublié. Il ne
voyage **pas** dans le `fx.Context` — `warpfx` n'a aucune règle temporelle à
évaluer (lot 3.1).

…puis remplacer l'appel `e.backend.FlowChecker.VerifySpend(...)` ou
`VerifySpendUTXOs(...)` par sa variante `…WithContext(fxCtx, ...)`.

**Les deux applications de la liste** :

- **Structurelle** — seules ces cinq appellent les points d'entrée contextuels.
  Toutes les autres continuent d'appeler les points d'entrée historiques, qui
  transmettent `nil`. Un UTXO `warpfx` atteint par un chemin non routé reçoit
  donc un contexte nul et échoue sur `ErrNoAuthorization`.
- **Ergonomique** — `acceptsWarpAuthorization(tx platform.UnsignedTx) bool`, un switch
  à **liste positive** avec `default: false`, consulté à l'étape 3 de
  `resolveAuthorization`. Il ne protège rien de nouveau ; il transforme un
  « ErrNoAuthorization » déroutant en un « cette transaction n'accepte pas
  d'autorisation Warp » lisible.

> ⚠️ **Un `platform.TxVisitor` a `nil` pour comportement par défaut.**
> C'est pour ça que la liste n'est pas un visiteur. Ajouter un type de
> transaction à la PlatformVM plus tard, ce serait hériter silencieusement de
> l'autorisation. Le mécanisme retenu inverse la charge de la preuve : **oublier
> d'ajouter une transaction à la liste ne peut rien ouvrir.** Ne « simplifie »
> pas en remplaçant le switch positif par un visiteur.

**Pourquoi `BaseTx` en fait partie.** On pourrait vouloir l'exclure au motif que
cela fermerait les transferts intra-P. Ce n'est pas le cas : `ExportTx` embarque
`avax.BaseTx`, et ses `Outs` (le change) peuvent nommer **n'importe quel**
propriétaire. Une `ExportTx` autorisée qui exporte une poussière et met tout le
reste en change réalise déjà un transfert intra-P arbitraire. L'exclure ne
fermerait rien, cela rendrait l'opération maladroite. En revanche `BaseTx`
apporte deux choses réelles : la **consolidation à la demande** (regrouper des
UTXOs déjà sur la P-Chain sans aller-retour ni immobilisation), et une
**primitive d'invalidation explicite** — dépenser vers soi-même tue une
autorisation en attente sans attendre son `expiry`.

Écris `TestAcceptsWarpAuthorization` qui énumère **tous** les types de
transaction et fige la réponse pour chacun : c'est ce qui rend visible en revue
qu'un type nouvellement ajouté hérite du refus.

### `SetAutoRenewedValidatorConfigTx`, le seul cas à deux autorisations

Cette transaction est la seule de la liste qui demande, en plus de dépenser, de
**prouver l'assentiment d'un propriétaire** : son `Auth` est vérifié contre le
`ValidatorAuthority` du validateur, par `verifyAuthorization`
(`subnet_tx_verification.go:82`) → `fx.VerifyPermission`.

C'est ce qui rend le staking auto-renouvelé utile à un contrat, et c'est aussi
la seule porte de sortie : **seule une `SetAutoRenewedValidatorConfigTx` peut
poser `Period = 0`**, ce qui signifie *« stop at the end of the current cycle and
unlock funds »*. Sans elle, le stake ne revient jamais.

**Un seul message autorise les deux.** Le message s'engage sur les octets de la
transaction entière ; il n'y a donc rien à dédoubler. Concrètement :

- `verifyAuthorization` prend le **dernier** credential de `tx.Creds` comme
  credential d'autorisation et rend les autres pour le reste de la transaction.
  Le porteur du message, lui, est trouvé par balayage — il peut être ce
  dernier slot comme n'importe quel autre.
- Le chemin de permission appelle `VerifyPermissionWithContext(fxCtx, …)` avec le
  **même** `fx.Context` que le flow check. Le Fx y refait exactement le test de
  provenance : `authorization.Authorizes(controlGroup)`.
- `tx.Auth` est un `secp256k1fx.Input` à `SigIndices` **vide**, comme l'input
  d'un UTXO `warpfx` — aucun type d'auth nouveau, par le même raisonnement qu'au
  lot 1.

> ⚠️ **Le dédoublement `VerifyPermission` / `VerifyPermissionWithContext` est ce
> qui garde le refus côté subnet.**
> `verifySubnetAuthorization` passe par le point d'entrée **sans** contexte, donc
> un `*warpfx.Owner` nommé propriétaire de subnet continue de tomber sur
> `ErrPermissionUnsupported` — structurellement, sans règle à écrire ni à se
> rappeler. C'est le même mécanisme que pour les transferts : **c'est la nullité
> du contexte qui ferme les portes**, jamais une liste.
>
> Corollaire à ne pas rater : si tu « simplifies » en faisant appeler le point
> d'entrée contextuel par `verifySubnetAuthorization`, tu rouvres le cas du
> subnet ingérable que le lot 9.1 refuse.

> ⚠️ **Pourquoi une version antérieure de ce plan excluait ces deux
> transactions, et pourquoi c'était faux.**
> L'argument était : « un validateur auto-renouvelé ne sort jamais de l'ensemble,
> donc son attestation de clôture ne serait jamais signable ». Le code dit le
> contraire. `Period = 0` déclenche `DeleteCurrentValidator` sur la branche
> **commit** (`proposal_tx_executor.go:475`), et la branche **abort** retire le
> validateur dans tous les cas (l.448). Il sort donc, gracieusement ou sur un
> défaut d'uptime.
>
> Ce qui était réel dans l'objection, et que le lot 7 traite : les récompenses ne
> sont **pas** indexées sous le `txID` de staking. `mintRewards` écrit
> `AddRewardUTXO(e.tx.ID(), utxo)` — le `txID` du `RewardAutoRenewedValidatorTx`
> du cycle. `StakeSettled` ne peut donc pas les nommer ; c'est `CycleSettled`
> qui le fait.

**Vérifier** :

```bash
go test ./vms/platformvm/txs/executor/...
```

---

## Le rôle du soumetteur, pour comprendre ce qu'on construit

Ce n'est pas du code à écrire dans ce lot, mais ça éclaire les décisions. Le
soumetteur observe les logs `SendWarpMessage` de la C-Chain, exactement comme un
relayeur ICM. Pour chaque message portant le `typeID` de cette proposition :

1. extraire `txBytes` du payload, le désérialiser en `platform.UnsignedTx` ;
2. collecter les signatures BLS via le handler ACP-118 ;
3. construire `Tx{Unsigned: …, Creds: […]}` avec un slot par input dans l'ordre
   `Ins ‖ ImportedInputs` — un seul porte le message, les autres slots `warpfx`
   portent un `warpfx.Credential{}` vide ;
4. soumettre via `platform.issueTx`.

Il n'a besoin d'**aucun fonds P-Chain** : la transaction est intégralement
auto-financée par les UTXOs du propriétaire. La barrière d'entrée au rôle est
nulle, ce qui est favorable à la vivacité — personne ne peut censurer seul une
autorisation.

---

## Ce qui reste incertain

🔴 **À trancher avant d'écrire**

- **Le `typeID` définitif du payload.** La position 4 est la suivante libre dans
  `warp/message/codec.go`, et c'est ce que le plan retient. Elle doit être
  confirmée avec l'allocation officielle du registre des payloads Warp, et
  reportée dans l'ACP et dans les contrats Solidity.
  `TestTxAuthorizationTypeID` l'épingle, donc un décalage se voit — mais il faut
  trancher **avant** qu'une autorisation soit émise sur une chaîne réelle.
- **La valeur d'`expiry` recommandée par défaut à l'outillage.** Ce n'est pas une
  règle de consensus — le protocole accepte n'importe quelle valeur — mais c'est
  un paramètre opérationnel important : trop court, une autorisation de multisig
  expire avant d'avoir réuni ses signatures ; trop long, la fenêtre d'exécution
  différée s'allonge d'autant.

✅ **Vérifié**

- **Les points d'appel de `VerifyWarpMessages` tenaient bien tous la transaction
  signée** : le changement s'est réduit à supprimer `.Unsigned`. Ils sont **trois**
  au niveau transaction — le passe-plat de bloc, le manager, le builder —,
  `block/executor/block.go` passant un bloc et n'ayant pas bougé.
- **`resolveAuthorization` tient dans les vérifieurs de staking**, au-dessus de
  leur `return nil` de bootstrap : rien à remonter dans l'appelant. Sept sites au
  total, trois dans `standard_tx_executor.go` et quatre dans
  `staker_tx_verification.go`, tous placés de la même façon.
- **Le précompile Warp force bien `sourceAddress = caller`** — vérifié dans son
  code (`sourceAddress = caller`, sans échappatoire) *et* sur un message réel au
  PoC : un EOA qui appelle `sendWarpMessage` directement produit exactement la
  paire qu'un `warpfx.Owner` nomme, sans contrat intermédiaire.

⚠️ **Le code n'ajoute aucune règle sur la longueur de `SourceAddress`**, et n'en
a pas besoin : une adresse d'une autre longueur ne peut correspondre à aucun
`WarpOwner`, `Authorizes` exigeant exactement 20 octets.

🟡 **À mesurer**

- **Le coût de la vérification BLS** pour un ensemble canonique à l'échelle du
  Primary Network (plus de 1 000 signataires), et l'adéquation du forfait de
  lectures d'état `intrinsicWarpDBReads` à cette taille. Voir lot 9, qui doit
  tarifer ce coût.
