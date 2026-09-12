# Lot 12 — Tests d'invariant

[← Lot 11](11-precompile.md) · [Plan](README.md)

---

## Pourquoi ce lot existe

Ce lot n'est pas « écrire des tests à la fin ». C'est la liste des propriétés que
**rien d'autre ne protège** — ni le compilateur, ni une signature de fonction, ni
une revue attentive.

Trois familles de risques justifient chaque entrée :

**Les bugs à consensus silencieux.** Deux surfaces, identifiées nommément dans
l'ACP : la **rémanence d'autorisation** (une autorisation qui survivrait d'une
vérification à la suivante autoriserait une transaction qu'elle n'a jamais
approuvée) et la **portée du court-circuit** (généralisé, il rendrait tout UTXO
`warpfx` dépensable par n'importe qui). Aucune des deux ne se voit dans un test
écrit transaction par transaction : la première demande **deux** vérifications
successives, la seconde demande de tester un cas qui **doit échouer**.

**Les invariants sans garde de compilateur.** L'alignement des `typeID` entre
trois codecs, et le tri normatif de la liste `StakeSettled`. Dans les deux cas,
une erreur ne produit pas une exception mais un comportement muet : un UTXO
illisible par la chaîne cible, ou un quorum qui ne se forme jamais.

**Les cas qui n'existent dans aucun composant pris isolément.** Le passage de la
transition de VM, notamment : un UTXO déposé par la P-Chain post-Helicon doit
être consommable par saevm, et aucun export coreth pré-transition ne doit pouvoir
en produire. Ni la suite P-Chain ni la suite saevm ne le couvre seule.

**Écris les quatre premiers avant leur code**, pas après. Ils décrivent des
échecs attendus : les écrire après revient à vérifier que le code fait ce qu'il
fait, pas ce qu'il doit faire.

## État

Les treize tests non différés sont écrits, la plupart avec le lot qu'ils
protègent. Le parcours de bout en bout existe aussi, en deux phases, dans
`tests/e2e/p/warp_utxos.go` — voir la fin de ce document.

⚠️ **Une correction de méthode, qui vaut pour tout ce dossier.** La commande de
vérification du test 4, `go test ./vms/platformvm/txs/ -run Codec`, ne
correspondait à **aucun** test — et rendait un succès. Le seul test épinglant
43/44/45 s'appelait `TestWarpUTXOsTypeIDs`. Il s'appelle désormais
`TestWarpUTXOsCodecTypeIDs`. Un filtre qui ne sélectionne rien ne dit rien, et
c'est exactement le vide silencieux dont ce lot traite.

---

## Les tests, par ordre de valeur

### 1. Rejeu d'autorisation entre transactions 🔴 bloquant

**Ce qu'il protège** : l'engagement. C'est le test qui garde la principale
surface de bug à consensus.

**Le scénario** : construire une transaction A, l'autoriser (message Warp portant
`A.Unsigned.Bytes()`). Construire une transaction B différente. Attacher le
credential de A à B. `resolveAuthorization(B, chainTime)` doit rendre
`ErrAuthorizationMismatch`.

**Pourquoi ce cas précis** : un message Warp est **public** — il figure dans un
log C-Chain que tout relayeur observe. Sans l'engagement, n'importe qui pourrait
le ramasser et l'attacher à une transaction de son choix consommant les UTXOs de
ce propriétaire. Le message cesserait d'être une autorisation pour devenir un
**jeton au porteur sur l'intégralité de ses fonds**.

**Où** : `vms/platformvm/txs/executor/authorization_test.go`. Lot [04](04-autorisation.md).

---

### 2. Portée du court-circuit canonique (P-Chain) 🔴 bloquant

**Ce qu'il protège** : la seconde surface de bug à consensus.

**Le scénario** — deux cas, tous deux **doivent échouer** :

- consommer un UTXO `warpfx` sans credential porteur depuis une **`BaseTx`** ;
- consommer un UTXO `warpfx` sans credential porteur depuis une **`ImportTx` à
  `Ins` non vides**.

Attendu : `ErrNoAuthorization` — l'UTXO atteint le Fx avec un contexte nul.

**Pourquoi ces deux cas** : ils sont exactement ce que le court-circuit ne doit
**pas** attraper. Généralisé en « pas de credential porteur → pas d'appel au
Fx », il rendrait tout UTXO `warpfx` dépensable par n'importe qui. Le test dit :
le court-circuit appartient à la branche canonique, et à elle seule.

**Où** : `vms/platformvm/txs/executor/canonical_test.go`. Lot [05](05-import-canonique.md).

---

### 3. Rémanence d'autorisation 🔴 bloquant

**Ce qu'il protège** : que le Fx ne retienne rien.

**Le scénario** : sur **la même instance** de `warpfx.Fx`, appeler
`VerifyTransferWithContext` avec une autorisation valide (succès attendu), puis
appeler `VerifyTransfer` **sans contexte** sur un autre UTXO du même
propriétaire. Le second appel doit rendre `ErrNoAuthorization`.

**Pourquoi il paraît inutile et ne l'est pas** : la structure `Fx` ne retient
rien de la vérification — elle ne porte que le `VM` fixé par `Initialize` —, donc
la propriété est vraie par construction, aujourd'hui. Le test verrouille cette
construction contre une future « optimisation » qui ajouterait un cache
d'autorisation à côté de ce champ pour éviter de re-parser le message. Ce serait
un vol silencieux, que ne révèle aucun test écrit transaction par transaction.

✅ Écrit, et **plus large que demandé** : il couvre aussi le point d'entrée
contextuel sans autorisation, et le chemin *permission*, qui partage la même
instance de `Fx` et serait sinon une seconde porte sur un cache d'autorisation.

**Où** : `vms/warpfx/fx_test.go`. Lot [01](01-warpfx-types.md).

---

### 4. Alignement des codecs 🔴 bloquant

**Ce qu'il protège** : la lisibilité inter-chaînes d'un UTXO.

**Le scénario** — trois assertions :

- `TestWarpUTXOsCodecTypeIDs` : `Owner` = 43, `TransferOutput` = 44,
  `Credential` = 45, dans `platform.Codec` **et** dans `platform.GenesisCodec` ;
- `TestCodecAlignedWithPlatformVM` : un même `avax.UTXO` sérialisé par
  `tx.MarshalUTXO` (saevm) et par `platform.Codec.Marshal` (PlatformVM) doit produire
  **les mêmes octets** — pour un UTXO `warpfx` **et** pour un `secp256k1fx`,
  l'invariant que le commentaire de `codec.go` prétendait garantir depuis
  toujours.

**Pourquoi un test par codec** : un test écrit contre l'un ne dit **rien** de
l'autre. Les `SkipRegistrations` diffèrent (34 pour saevm, 32 pour coreth), et
seul le troisième test attrape un décalage entre P-Chain et saevm — décalage qui
produirait un UTXO créable mais définitivement illisible, **silencieusement du
point de vue de l'export, qui aura déjà débité**.

**Où** : `vms/platformvm/platform/codec_test.go`, `vms/platformvm/platform/codec_test.go`,
`vms/saevm/cchain/tx/warp_codec_test.go`. Lots [02](02-codecs.md) et
[10](10-saevm.md).

⚠️ `TestOwnerIDLayout` et `TestOwnerHasNoSerializedInterfaceField` ne font
**pas** partie de cette liste : sans `ownerID` ni codec local (lot 1.2), ils
n'ont plus d'objet.

---

### 4 bis. Le refus de subnet, et sa frontière

**Ce qu'il protège** : que le dédoublement `VerifyPermission` /
`VerifyPermissionWithContext` sépare bien les deux appelants.

**Le scénario** — deux moitiés, et c'est leur **paire** qui a du sens :

- un `*warpfx.Owner` nommé propriétaire de subnet, via `CreateSubnetTx` puis via
  `TransferSubnetOwnershipTx` → refusé (`ErrWarpOwnerCannotOwnSubnet` en
  syntaxique, `ErrPermissionUnsupported` si on atteint le Fx) ;
- un `*warpfx.Owner` nommé `ValidatorAuthority` d'un
  `AddAutoRenewedValidatorTx`, puis une `SetAutoRenewedValidatorConfigTx`
  autorisée par message Warp → **acceptée**.

**Pourquoi les deux ensemble** : pris isolément, le premier test passerait aussi
si quelqu'un avait rendu `VerifyPermission` universellement refusante, et le
second passerait aussi si quelqu'un avait routé le chemin subnet vers la
variante contextuelle. C'est leur conjonction qui dit que **la frontière est le
point d'entrée, pas le type du propriétaire**.

**Où** : `vms/platformvm/txs/executor/`. Lots [04](04-autorisation.md) et
[09](09-tarification.md).

---

### 5. Dépense hors liste autorisée

**Le scénario** : consommer un UTXO `warpfx` depuis une transaction qui n'est pas
dans la liste des cinq (par exemple `CreateSubnetTx`). Doit échouer.

**Ce qu'il protège** : la garde structurelle du lot 4d — un chemin qui n'a pas
résolu d'autorisation transmet un contexte nul, et le Fx refuse. C'est ce qui
fait qu'**oublier d'ajouter une transaction à la liste ne peut rien ouvrir**.

Ajoute `TestAcceptsWarpAuthorization` qui énumère **tous** les types de
transaction et fige la réponse pour chacun — sept `true`, tout le reste `false`.
C'est ce qui rend visible en revue qu'un type ajouté plus tard hérite du refus.

**Où** : `vms/platformvm/txs/executor/`. Lot [04](04-autorisation.md).

---

### 6. Les bornes de frais, des deux côtés

**Le scénario** — côté P-Chain, quatre cas : un nAVAX **en dessous** et
**au-dessus** de chacune des deux bornes de la fourchette. Côté saevm, un aAVAX
au-delà du plafond d'enchère.

**Ce qu'il protège** : côté P-Chain, qu'un tiers ne puisse pas restituer une
fraction dérisoire et brûler le reste. Côté saevm, la même chose — mais l'attaque
y est **plus attrayante**, puisque tout brûler maximise la priorité d'inclusion.

**Le point à écrire explicitement dans le test saevm** : la symétrie. Le refus
haut (`ErrBidTooHigh`) et le refus bas (`core.ErrFeeCapTooLow`) doivent porter sur
**la même grandeur** (`o.GasFeeCap`) contre **la même référence** (`s.baseFee`).
Une borne exprimée dans le vocabulaire d'un frais absolu n'aurait pas cette
propriété, et le test est le bon endroit pour le rendre visible.

⚠️ **Et un troisième cas, que seul le PoC a fait apparaître** : l'enchère
**minimale exprimable**. Le brûlage est quantifié à 1 nAVAX, le `baseFee` ne
l'est pas, donc `k × baseFee` peut tomber quatre à cinq ordres de grandeur sous
la plus petite enchère qu'un soumetteur puisse formuler — auquel cas la règle
n'est pas serrée, elle est **insatisfiable**. Le plafond est planché à cette
valeur, et le test doit couvrir les deux côtés du plancher. Voir
[10d](10-saevm.md).

**Où** : `vms/platformvm/txs/executor/canonical_test.go` et
`vms/saevm/cchain/hooks_test.go`. Lots [05](05-import-canonique.md) et
[10](10-saevm.md).

---

### 7. Récompense vers un `WarpOwner`

**Le scénario** : un staking complet depuis un `WarpOwner` jusqu'au
`RewardValidatorTx`, puis vérification que le `StakeSettled` signé nomme les bons
`outputIndex` dans le bon ordre.

⚠️ **Sur les deux branches, commit et abort.** C'est tout l'intérêt : la
récompense de délégataire est à `len(outputs)+len(stake)+offset` sur commit et à
`len(outputs)+len(stake)` sur abort, où elle **remonte** dans le rang que la
récompense de validation aurait occupé. Un test qui ne couvre que commit ne dit
rien de la propriété qui justifie l'existence du message.

Vérifie aussi que le remboursement du stake **n'apparaît pas** dans la liste :
`unstakeUTXOs` n'appelle que `AddUTXO`, jamais `AddRewardUTXO`.

**Où** : `vms/platformvm/txs/executor/warp_reward_test.go`. Lot [07](07-attestations.md).

---

### 7 bis. Cycle auto-renouvelé vers un `WarpOwner`

**Le scénario** : un `AddAutoRenewedValidatorTx` autorisé par message Warp, dont
les propriétaires de récompenses **et** le `ValidatorAuthority` sont le même
`WarpOwner`. Puis, dans l'ordre :

1. un premier cycle **commit** avec `AutoCompoundRewardShares` partiel → un
   `CycleSettled` nommant la part retirée, la part restakée n'apparaissant nulle
   part en UTXO ;
2. un cycle **abort** → un `CycleSettled` sur *ce* cycle, avec les récompenses
   accumulées, et le validateur retiré de l'ensemble ;
3. une `SetAutoRenewedValidatorConfigTx` autorisée posant `Period = 0`, puis le
   cycle final → sortie gracieuse, `CycleSettled` du dernier cycle, et le
   principal rendu **sous le `txID` de staking**, pas dans le message.

⚠️ **L'assertion qui compte** : ce qu'un `StakeSettled` demandé sur le `txID` de
staking d'un auto-renouvelé produit — parce que `GetRewardUTXOs(stakingTxID)` est
vide pour lui. C'est le piège que `CycleSettled` existe pour éviter, et un test
qui ne le vérifie pas laisse croire que les deux messages sont interchangeables.

✅ **Et il se referme plus fort que ce guide ne l'annonçait.** Le message n'est
pas *signable avec `numRewards = 0`* : il est **refusé d'emblée**, sur
`ErrNotAStakingTx`, parce que la règle de signature restreint le type aux deux
stakings permissionless. Épinglé des deux côtés — sur l'état, où
`GetRewardUTXOs(stakingTxID)` est vide après un cycle qui a pourtant payé, et sur
le handler.

**Où** : `vms/platformvm/txs/executor/warp_reward_test.go` et
`vms/platformvm/network/`. Lots [04](04-autorisation.md) et
[07](07-attestations.md).

---

### 8. Activation

**Le scénario** : la même transaction, mentionnant un type `warpfx`, soumise
avant et après `HeliconTime`. Rejetée puis acceptée.

**Ce qu'il protège** : pas une fonctionnalité utilisée trop tôt — un **fork**. Un
nœud non mis à jour ne sait pas décoder les types 43/44/45, donc ne sait pas
parser le bloc qui les contient.

Couvre les cinq emplacements possibles : `Outs`, `ExportedOutputs`, `StakeOuts`,
un `RewardsOwner`, et un `Credential`. C'est ce qui justifie le parcours réflexif
plutôt qu'une liste.

**Où** : `vms/platformvm/txs/executor/warp_activation_test.go`. Lot [06](06-gardes.md).

---

### 9. Tarification du credential

**Le scénario** : deux autorisations identiques sauf par le **nombre de
signataires** BLS. Les frais doivent différer.

Et un second volet, plus facile à oublier : la **capacité de bloc** doit en tenir
compte. Construis un bloc plein de transactions autorisées et compare le gas
consommé à la cible.

**Ce qu'il protège** : que la vérification BLS d'un set de 1 000+ signataires ne
soit pas gratuite. Sans le test, rien ne casse — les frais sont simplement trop
bas, et le bloc trop lourd.

**Où** : `vms/platformvm/txs/fee/warp_utxos_complexity_test.go`. Lot [09](09-tarification.md).

---

### 10. Portée du court-circuit saevm

**Le scénario** : un lot d'import mêlant un UTXO `warpfx` et un UTXO signé. Doit
retomber sur la boucle ordinaire et s'y faire refuser (`ErrWrongUTXOType` de
`secp256k1fx`).

**Ce qu'il protège** : le pendant saevm du test 2. Le court-circuit lit la
**forme du lot entier**, jamais l'absence de signature.

**Où** : `vms/saevm/cchain/tx/warp_canonical_test.go`. Lot [10](10-saevm.md).

---

### 11. Destination d'export

**Le scénario** : une sortie `warpfx` exportée vers la X-Chain, refusée **des
deux côtés** — depuis la P-Chain (`ErrWarpOutputWrongDestination`) et depuis
saevm (`errWarpOutputWrongDestination`).

**Ce qu'il protège** : qu'aucun UTXO indécodable ne naisse. ⚠️ Le refus doit
intervenir **avant tout débit** : l'export débite avant que la chaîne cible n'ait
rien à dire, donc un refus tardif ne répare rien.

**Où** : `vms/platformvm/txs/executor/warp_export_test.go` et
`vms/saevm/cchain/tx/warp_canonical_test.go`. Lots [06](06-gardes.md) et
[10](10-saevm.md).

---

### 12. Refus de bloc saevm avant activation

**Le scénario** : un bloc saevm horodaté dans la fenêtre de dix secondes qui
précède `HeliconTime` ne doit pas se reconstruire.

**Ce qu'il protège** : c'est **ce sur quoi repose la dispense de garde du volet
C-Chain**. Le lot 10e n'écrit aucune garde d'activation parce que
`builder.BuildHeader` refuse de construire, et que `rebuild` emprunte le même
chemin. Ce test est le seul endroit où cette affirmation est vérifiée.

`TestPreHeliconBlocksDisallowed` (`vms/saevm/cchain/vm_test.go`) le fait
déjà : `BuildBlock` et `VerifyBlock` rendent `errHeliconUnactivated`, `ParseBlock`
réussit encore. **Il existe — il faut juste savoir que c'est lui qui porte la
dispense**, et ne pas l'affaiblir. ✅ Son commentaire le dit désormais sur place,
pour que personne n'ait à le redécouvrir.

**Où** : existant. Lot [10](10-saevm.md).

---

### 12 bis. Les deux montages refusés

Deux règles ajoutées au plan après relecture ([6.3](06-gardes.md) et
[9.1](09-tarification.md)), toutes deux **atteignables par un tiers ou par
inadvertance**, et toutes deux sans garde ailleurs.

**Le verrou.** Une `BaseTx` ordinaire, signée en secp, produisant un
`stakeable.LockOut{Locktime: X, TransferableOut: &warpfx.TransferOutput{…}}` au
profit d'un `WarpOwner` tiers → `ErrWarpOutputNotLockable`. Idem depuis une
`ExportTx` et depuis les `StakeOuts` d'une transaction de staking. Plus une
non-régression : `stakeable.LockOut{secp256k1fx.TransferOutput}` doit continuer
de passer.

Vérifie au passage que `verifyWarpExportDestination` (lot 6.1) **déplie** le
`stakeable.LockOut` avant de tester le type — sinon la garde de destination
passerait à côté d'une sortie `warpfx` enveloppée, et les deux règles se
manqueraient mutuellement.

**Le propriétaire de subnet.** Un `CreateSubnetTx` et un
`TransferSubnetOwnershipTx` nommant un `*warpfx.Owner` →
`ErrWarpOwnerCannotOwnSubnet`. ⚠️ Écris-le **après** le lot 9 : avant, la
transaction échoue déjà au calcul de frais, et le test passerait pour la
mauvaise raison.

**Où** : `vms/platformvm/txs/executor/warp_activation_test.go` et
`vms/platformvm/platform/transfer_subnet_ownership_tx_test.go`. Lots [06](06-gardes.md) et
[09](09-tarification.md).

---

### 13. Non-régression de l'aiguillage

**Le scénario** : `go test ./vms/platformvm/...` en entier.

**Ce qu'il protège** : le lot 3 touche le chemin de vérification de **toutes** les
transactions de la P-Chain. Tant que `warpfx` n'est pas atteignable, la table ne
contient qu'une entrée et le comportement doit être rigoureusement inchangé.

⚠️ **Le critère est que rien ne soit modifié pour faire passer la suite** — hormis
la construction des helpers de test, qui doivent fournir une collection au lieu
d'une instance. Si tu dois assouplir une assertion, cherche ce qui a changé
plutôt que d'adapter le test.

⚠️ **Mais ce critère ne prouve rien sur l'aiguillage lui-même**, et c'est le
piège : la suite complète passe **aussi bien avec une table vide**. C'est le
test 2 bis ci-dessous qui couvre ce que le critère ne voit pas.

### 13 bis. L'aiguillage aiguille réellement

`TestVerifySpendUTXOsDispatchesOnTheConsumedOutput` : sur un même triplet
(UTXO, input, credential), sans contexte → `warpfx.ErrNoAuthorization` (donc
`warpfx` est bien atteint, et pas `secp256k1fx.ErrWrongUTXOType`) ; avec
l'autorisation d'un tiers → `ErrWrongOwner` ; avec celle du propriétaire → `nil`.

En vidant `Types` de la revendication `warpfx`, il échoue **seul**, tout le reste
restant vert. C'est précisément la défaillance silencieuse que le lot 3 décrit.

**Où** : la suite existante. Lot [03](03-aiguillage-fx.md).

---

> **Les quatre tests qui suivent appartiennent au [lot 11](11-precompile.md).**
> Les trois premiers sont écrits
> (`vms/saevm/cchain/precompile/nativeexport/contract_test.go`) ; le quatrième
> est le bench de calibrage, qui reste à faire.

---

### 14. Sûreté du `revert` du précompile

**Le scénario** : un `exportAVAX` appelé dans un frame qui *revert*. Ne doit
produire **aucun** `PutRequest`, et ne doit pas consommer d'`OutputIndex`.

⚠️ Le cas exact à couvrir : une transaction en échec est **incluse au bloc** avec
`status = 0` — elle a consommé du gas. Ce n'est pas une transaction absente. Ses
logs ont été rembobinés avant construction du receipt, et c'est cela qu'on
vérifie.

**Où** : `vms/saevm/cchain/precompile/nativeexport/`. Lot [11](11-precompile.md).

---

### 15. Unicité de l'`UTXOID` du précompile

**Le scénario** : plusieurs `exportAVAX` dans une même transaction EVM, depuis
**plusieurs contrats différents**, doivent produire des `UTXOID` distincts. Et
deux transactions EVM distinctes ne doivent **pas** partager de compteur.

**Ce qu'il protège** : la dérivation du 11d. Une clé dupliquée dans un même
`Apply` de shared memory ne doit pas pouvoir invalider un bloc.

**Où** : idem. Lot [11](11-precompile.md).

---

### 16. `msg.value` non multiple de `X2CRate`

**Le scénario** : rejeté, jamais tronqué. Plus les deux autres validations :
quotient qui déborde d'un `uint64`, et `Amt == 0`.

**Où** : idem. Lot [11](11-precompile.md).

---

### 17. Parité tarifaire du précompile 🟡 bench

**Le scénario** : à `baseFee` égale, `exportAVAX` doit se situer dans le même
ordre de grandeur qu'un `Export` atomique équivalent.

Ce n'est pas un test au sens strict — c'est une mesure, et c'est celle qui
conditionne la qualité du lot 11. Une sous-tarification d'écriture d'état ne se
rattrape pas.

**Où** : un benchmark. Lot [11](11-precompile.md).

---

## Les tests de bout en bout

**Où** : `tests/e2e/p/`, trois specs qui partagent leurs helpers.

| Spec | Fichier | Ce qu'elle seule couvre |
| --- | --- | --- |
| `[Warp UTXOs]` | `warp_utxos.go` | le parcours complet, propriétaire **EOA** ; la transition de VM observée |
| `[Warp UTXOs Contract]` | `warp_utxos_contract.go` | le même parcours, propriétaire **contrat Solidity** ; l'attestation relivrée au contrat |
| `[Warp UTXOs Mixed]` | `warp_utxos_mixed.go` | inputs signés et autorisés **dans une même transaction**, et le refus de deux propriétaires |
| `[Warp Export Precompile]` ×2 | `warp_export_precompile.go` | l'aller-retour par le **précompile**, pour un EOA et pour un contrat |
| `[Warp Staking Family]` | `warp_staking_family.go` | la **délégation**, et le validateur **auto-renouvelé** dont l'autorité est un propriétaire warp |

Six specs au total, l'une des cinq entrées étant jouée deux fois. Elles tournent ensemble en ordre aléatoire.

### Le parcours complet

Le parcours, dans l'ordre :

1. alimenter un `WarpOwner` par un **export atomique signé par un EOA**
   (lot 10b) — désignant, tant qu'à faire, l'adresse d'un contrat, pour couvrir
   le financement d'un tiers. La seconde branche, un `exportAVAX()` depuis un
   contrat, est couverte par sa propre spec ;
2. import canonique P-Chain (lot 5) ;
3. staking via une autorisation Warp (lot 4) ;
4. `TxExecuted` demandé au handler ACP-118 et livré à la C-Chain (lot 7) ;
5. clôture du staking, puis `StakeSettled` (lot 7) ;
5 bis. en parallèle, un validateur **auto-renouvelé** ouvert par le même
   propriétaire : un cycle, son `CycleSettled`, puis une
   `SetAutoRenewedValidatorConfigTx` autorisée posant `Period = 0` et la sortie
   gracieuse ;
6. `ExportTx` P-Chain autorisée vers la C-Chain (lots 4 et 6) ;
7. import canonique saevm (lot 10c) ;
8. solde EVM crédité, **constaté en mode pull** — le crédit se fait sans
   exécution de code, ni `receive()` ni `fallback`.

Lance-les avec `HeliconTime` **programmé dans le futur proche**, pas au genesis —
`[Warp UTXOs]` observe la transition, donc il doit commencer avant :

```bash
./scripts/build.sh && ./scripts/build_xsvm.sh
./bin/ginkgo -v --focus="Warp UTXOs" ./tests/e2e -- \
    --avalanchego-path=$PWD/build/avalanchego \
    --node-count=5 --activate-latest-after=90s
```

Ils se **skippent** proprement si Helicon n'est pas programmé. Seule
`[Warp UTXOs]` a besoin de démarrer *avant* la bascule ; elle se contente de
sauter son assertion pré-transition si elle arrive trop tard, pour que les trois
specs restent lançables ensemble — la première qui tourne active Helicon pour
les suivantes.

### La variante contrat

`[Warp UTXOs Contract]` rejoue le parcours avec, pour propriétaire, le contrat
`warp_owner.sol` (compilé en dur dans `warp_owner_contract.go`). Un contrat
possède, mise et récupère de l'AVAX **sans clé**, et le spec ferme la boucle que
l'EOA n'a pas besoin de fermer : `recordExecution` reçoit l'attestation
`TxExecuted` et enregistre un `txID` que le contrat ne pouvait pas prédire —
il s'engage sur les octets **non signés**, alors que le `txID` hache les octets
signés, credential compris.

Deux détails du harnais qui n'ont rien d'évident :

- la livraison est **forcément une transaction EVM de premier niveau**, le
  prédicat appartenant à la transaction et non à l'appel. D'où un
  `recordExecution` appelable par n'importe qui, et un relayeur dans le spec ;
- le crédit d'un import canonique est appliqué **hors EVM**, comme un
  `selfdestruct` : `receive()` n'est pas appelé. Le contrat doit le constater,
  il n'en est pas notifié.

### La variante mixte

`[Warp UTXOs Mixed]` ne rejoue rien : elle vérifie ce qu'aucun test unitaire
n'atteint, parce que le seul test unitaire à inputs mixtes est celui qui doit
**échouer**.

1. `warpfx` ne déclare aucun type d'input : un `secp256k1fx.TransferInput` à
   `SigIndices` vide référence la sortie consommée et en reprend le montant ;
2. un input signé et un input autorisé **coexistent** dans une transaction, avec
   chacun son genre de credential — les deux s'engagent sur les mêmes octets,
   donc aucun n'invalide l'autre ;
3. mais tous les inputs `warpfx` appartiennent au **même propriétaire** : il n'y
   a qu'un porteur, et la provenance est vérifiée contre *chaque* UTXO
   consommé. Aucune règle ne dit « un propriétaire par transaction » ; ça en
   découle.

Un export atomique unique alimente les deux propriétaires ; il faut ensuite
**deux imports canoniques**, un par propriétaire — ce qui est le point 3 vu de
l'autre côté.

> ⚠️ **Le cas qui n'existe dans aucun VM pris isolément.**
> Un UTXO déposé par la P-Chain **post**-Helicon doit être consommable par saevm,
> et aucun export coreth **pré**-transition ne doit pouvoir en produire. C'est le
> seul endroit où le raisonnement du [lot 6.4](06-gardes.md) — « ne pas toucher
> coreth est sûr » — est vérifié plutôt que déduit. Si tu ne devais garder qu'un
> seul test e2e, ce serait celui-là.
>
> ✅ **Et il l'est.** Coreth refuse l'export au parsing même
> (`unknown type ID 44`), et les **mêmes octets** passent une fois saevm
> installé.

### Ce que ce parcours a trouvé, et qu'aucun test unitaire n'atteignait

**La borne d'enchère du [lot 10d](10-saevm.md) était insatisfiable.** Le brûlage
est quantifié à 1 nAVAX, le `baseFee` non, donc `k × baseFee` tombait quatre à
cinq ordres de grandeur sous la plus petite enchère exprimable : aucun import
canonique n'était incluable au prix plancher de la chaîne. La règle est
maintenant planchée. Un test unitaire n'y menait pas — il aurait fallu deviner la
question.

**La transition de VM demande du trafic.** Le VM de transition bascule quand il
**accepte un bloc** dont l'horodatage atteint le seuil, pas sur l'horloge : une
chaîne inactive reste sur coreth indéfiniment. Sans conséquence en production,
déterminant pour tout test qui traverse la transition — et absent de la première
rédaction du [PoC](POC-c-p.md).

**Et le nonce de l'export bouge** si la même clé produit le bloc de transition.
Une clé séparée pour la relance, sinon la transaction signée avant la bascule est
invalide après.

**`AcceptedNonceAt` retarde sur le reçu.** Le nonce est lu dans le dernier bloc
**accepté**, alors qu'un reçu est disponible dès le bloc *traité* : deux envois
consécutifs peuvent recevoir le même nonce, et le second est refusé —
`replacement transaction underpriced`, les deux portant le même prix suggéré. Un
nonce **réservé localement** règle la course, la valeur acceptée ne servant plus
qu'à faire avancer la réservation — ce qui reste nécessaire, les exports
atomiques tirant sur le même compte hors EVM.

Le même retard rend **invisible une écriture qu'on vient de faire** : le reçu de
`recordExecution` est disponible alors que `CallContract` lit encore l'état
accepté, et la lecture qui suit renvoie zéro. Toute lecture qui suit une
écriture doit être *pollée*, pas faite une fois.

**Le verrou du jeu de validateurs est permanent, pas lent.** Le point noté plus
haut a été poussé jusqu'au bout : après un `AddAutoRenewedValidatorTx`, des
blocs P-Chain ont été produits **exprès**, à chaque tentative, et le jeu du
vérifieur est resté court d'un membre sur plus de 170 secondes. Ce n'est donc
pas une convergence lente qu'un délai réglerait. `SetAutoRenewedValidatorConfigTx`
est pour cette raison couverte en unitaire et **pas** en e2e : le spec constate
le refus et vérifie qu'il porte bien sur le quorum, jamais sur l'autorité, la
disposition des credentials ou la dépense.

**`platform.getCurrentValidators` échouait entièrement** — pour tous les
validateurs, pas seulement le concerné — dès qu'un `ValidatorAuthority` était un
`warpfx.Owner`. Les propriétaires de récompenses dégradaient déjà proprement
(`if ok`, champ laissé nul) ; l'autorité, seule, renvoyait une erreur. Trouvé
seulement en e2e, parce que l'agrégateur de signatures lit le jeu canonique par
cet endpoint : toute autorisation postérieure devenait impossible.

**Payer un contrat coûte plus que 21 000 de gaz.** Le forfait d'un transfert nu
ne couvre pas l'exécution de `receive()`, même vide. Sans surcharge explicite,
la dotation d'un contrat revient avec `status = 0`.

**Une transaction autorisée ne survit pas au changement de jeu de validateurs
qu'elle vient elle-même de provoquer.** Le vérifieur de la P-Chain contrôle le
quorum à `preferredCtx.PChainHeight`, à défaut `GetMinimumHeight()` : dans les
deux cas une hauteur qui n'avance **que lorsqu'un bloc P-Chain est accepté**.
L'agrégateur, lui, signe contre le jeu courant. Juste après un
`AddPermissionlessValidatorTx`, les deux divergent d'un membre, et les index du
bitset BLS se décalent :

```
unknown validator: NumIndices (5) >= NumFilteredValidators (5)
```

Ce n'est pas transitoire : si la transaction refusée est la seule activité en
attente, aucun bloc n'est accepté, donc la hauteur n'avance pas, donc la
transaction reste refusée. Réessayer ne converge pas.

Conséquence pour les specs : le **staking passe en dernier**, après l'aller-retour
et l'attestation. Conséquence hors test : un relayeur qui enchaîne deux
transactions autorisées dont la première touche au jeu de validateurs doit
attendre que la P-Chain produise un bloc entre les deux — attendre la seule
acceptation de la première ne suffit pas.

---

## Récapitulatif des commandes

```bash
# Par lot
go test ./vms/warpfx/...
go test ./vms/platformvm/...          # non-régression de l'aiguillage (test 13)
go test ./vms/saevm/cchain/...
go test ./vms/components/avax/...

# Les trois invariants silencieux (test 4)
go test ./vms/platformvm/txs/   -run Codec
go test ./vms/platformvm/block/ -run Codec
go test ./vms/saevm/cchain/tx/  -run Codec

# Complet
./scripts/build.sh && go test ./...
```

---

## Ce qui reste incertain

🟠 **À vérifier au premier build**

- **Les helpers de test à adapter au lot 3.** J'ai identifié
  `block/builder/helpers_test.go`, `block/executor/helpers_test.go` et
  `txs/executor/helpers_test.go` comme construisant un `Backend` avec un `Fx`
  unique. La liste peut être plus longue.
- **La façon de forcer une branche abort** dans le test 7. Elle dépend de
  l'uptime mesuré ; les harnais de test de la PlatformVM ont probablement de quoi
  la piloter, mais je ne l'ai pas vérifié.
- **Le harnais e2e existant pour les transactions atomiques C↔P.** `tests/e2e/`
  couvre déjà des transferts C↔P ; s'appuyer dessus plutôt que repartir de zéro
  fera gagner beaucoup, mais l'ampleur de l'adaptation reste à évaluer.

🔴 **À trancher avant d'écrire**

- Rien dans ce lot. Les tests découlent des décisions prises ailleurs ; si l'un
  d'eux te paraît impossible à écrire, c'est une décision d'un autre lot qui est
  à revoir.

🟡 **À mesurer**

- **Le test 17**, qui est un bench et pas un test. C'est la seule mesure
  bloquante du plan.
