# PoC — le flux C ↔ P, avant tout le reste

[Plan](README.md) · précédé du [lot 1](01-warpfx-types.md)

---

## Ce que ce PoC prouve, et ce qu'il ne prouve pas

L'objet est de **confirmer la base** : qu'un UTXO détenu par une adresse C-Chain
traverse la frontière dans les deux sens, soit lisible de l'autre côté, et soit
retrouvable. Tout le reste de la proposition — autorisation, staking,
attestations — repose là-dessus, et aucune de ces briques ne vaut d'être écrite
si l'aller-retour ne tient pas.

Trois choses seulement sont réellement en jeu :

| Ce qui est prouvé | Pourquoi ça ne se prouve qu'ici |
| :--- | :--- |
| **L'alignement des codecs** entre saevm et la PlatformVM | Un décalage produit un UTXO créable et illisible, **silencieusement du point de vue de l'export, qui a déjà débité**. Aucun test unitaire d'un seul VM ne le voit. |
| **Le trait de découverte** | `Addresses()` n'est vérifié par rien : un UTXO sans trait est déposé, dépensable, et introuvable. Le symptôme n'apparaît qu'au moment où l'outillage doit lister. |
| **La transition de VM** | Un UTXO déposé par une P-Chain post-Helicon doit être consommable par saevm. Ni la suite P-Chain ni la suite saevm ne le couvre seule. |

⚠️ **Ce PoC ne prouve rien sur l'autorisation.** La phase A ne fait passer aucun
message Warp, et c'est délibéré : elle isole la plomberie inter-chaînes de la
cryptographie qui viendra dessus.

> **Tout ce qui suit se fait avec le dépôt seul.** Pas d'Ansible, pas de
> Terraform, pas de genesis écrit à la main, pas de fichier d'upgrade à
> distribuer. `tests/fixture/tmpnet` fait le réseau, `vms/saevm/cchain.Client` et
> `vms/platformvm.Client` font les appels, et `tests/e2e/p/l1.go` contient déjà le
> motif d'agrégation ACP-118 dont la phase B a besoin. Ce qui reste à écrire
> tient en **trois constructeurs de transaction**.

---

## Le découpage, et ce qu'il permet de ne pas écrire

C'est le point important. **La phase A n'a pas besoin du lot 3**, le refactor
d'aiguillage des Fx — celui qui touche le chemin de vérification de *toutes* les
transactions de la P-Chain. La forme canonique court-circuite le Fx, donc rien
n'a besoin d'être aiguillé pour qu'un UTXO `warpfx` entre sur la P-Chain.

### Phase A — l'aller : C → P, sans une seule signature nouvelle

| Lot | Ce qu'il faut, exactement |
| :--- | :--- |
| [1](01-warpfx-types.md) | **fait** |
| [2](02-codecs.md) | en entier — 43/44/45, transactions **et** blocs |
| [5](05-import-canonique.md) | **forme réduite** : voir ci-dessous |
| [9.1](09-tarification.md) | `outputComplexity` et `OwnerComplexity` seulement. **Bloquant** : sans lui l'import échoue au *calcul de frais* sur `errUnsupportedOutput`, avec un message qui ne parle pas du type. |
| [10a](10-saevm.md) | `SkipRegistrations(34)` + `warpfx.TransferOutput` → 44 |

**Pas nécessaires** : lots 3, 4, 6, 7, 8, 9.2, 10c, 11.

> **Le lot 5 en forme réduite.** Sans le lot 4, il n'y a pas de
> `resolveAuthorization`, donc pas de notion de « credential porteur ». La
> première condition de `isCanonicalImport` devient simplement : **aucun
> `warpfx.Credential` dans `tx.Creds`**. Les deux autres — `Ins` vide, tous les
> UTXOs importés `warpfx` — sont inchangées, et `verifyCanonicalImport` est
> identique.
>
> ⚠️ Écris-la de façon à ce que le passage à la forme complète soit un
> remplacement de condition, pas une réécriture : le court-circuit du Fx doit
> déjà être **dans la branche**, jamais avant elle. C'est la seconde surface de
> bug à consensus de la proposition, et elle ne devient pas moins dangereuse
> parce qu'on est en PoC.

⚠️ **Le lot 8 n'est pas dans la liste, et ce n'est pas un oubli.** Le trait fait
vingt octets (lot 1.2), donc `platform.getUTXOs` sert un `WarpOwner` **tel
quel** : il suffit d'encoder l'adresse EVM en Bech32. C'est le premier endroit
où ce choix se paie. (Le lot 8 ajoute `platform.getWarpOwnerUTXOs`, qui **filtre
sur la paire complète** là où `getUTXOs` répond sur les vingt octets seuls — utile
à l'outillage, inutile ici où un seul propriétaire existe.)

### Phase B — le retour : P → C, avec autorisation

À n'entamer qu'une fois la phase A verte.

| Lot | Ce qu'il faut |
| :--- | :--- |
| [3](03-aiguillage-fx.md) | en entier — c'est ici que le refactor devient inévitable |
| [4](04-autorisation.md) | en entier, mais la liste peut se limiter à `ExportTx` |
| [6](06-gardes.md) | 6.1 (destination) et 6.3 (verrou) ; 6.2 seulement si tu testes l'activation |
| [9.2](09-tarification.md) | tarification du credential porteur |
| [10b](10-saevm.md), [10c](10-saevm.md) | destination d'export, et la règle miroir à l'import |

---

## Le réseau : `tmpnet`, et rien d'autre

Le PoC a besoin de trois choses qu'un réseau standard ne donne pas : un
`network-id` qui autorise une configuration d'upgrade, un genesis listant les
nœuds, et un `heliconTime` **dans le futur proche**. `tests/fixture/tmpnet` fait
les trois, et il les fait mieux qu'un playbook parce qu'il calcule la valeur
**une fois** et la pousse identique.

### Les trois contraintes, et qui les satisfait

| Contrainte | Ce qui la satisfait |
| :--- | :--- |
| Un `network-id` hors `mainnet`/`fuji`/`local`, sans quoi toute configuration d'upgrade est **refusée** (`config/config.go:906-911`) | tmpnet utilise **88888** par construction (`tests/fixture/tmpnet/network.go:60`) |
| Un genesis listant les cinq nœuds comme *initial stakers*, produit **après** leurs certificats | `NewTestGenesis` (`tests/fixture/tmpnet/genesis.go:48`), appelé par tmpnet dans le bon ordre |
| `heliconTime` dans le futur, identique à la seconde sur les cinq nœuds | `tmpnet.UpgradeConfig(d)` (`network.go:377`) |

`upgradetest.Latest` **est** Helicon, donc `UpgradeConfig(10 * time.Minute)`
programme exactement ce que ce PoC demande : Helicon dans dix minutes, tous les
upgrades antérieurs à `InitiallyActiveTime`, et `GraniteEpochDuration` posée.

> ✅ **Les deux 🔴 de la version précédente de ce guide disparaissent.**
> `UpgradeFlags` (`network.go:398`) sérialise la configuration **une fois** et la
> passe en `config.UpgradeFileContentKey` — du base64 en ligne, pas un fichier.
> Il n'y a donc ni fichier à distribuer, ni `now + 10m` évalué par hôte, ni
> `graniteEpochDuration` à écrire en nanosecondes à la main. La divergence de
> `heliconTime` entre nœuds n'est plus un piège : elle n'est plus exprimable.

### Lancer

```bash
./scripts/build.sh                       # → build/avalanchego

# soit un réseau piloté à la main :
./bin/tmpnetctl start-network --node-count=5

# soit — recommandé — le PoC écrit comme un test ginkgo :
./bin/ginkgo -v ./tests/e2e -- \
    --avalanchego-path=./build/avalanchego \
    --node-count=5 \
    --activate-latest-after=10m
```

`--activate-latest-after` est câblé à `tmpnet.UpgradeConfig` par
`tests/e2e/e2e_test.go:48`, et `--node-count` par
`tests/fixture/e2e/flags.go:80`.

### Pourquoi écrire le PoC comme un test ginkgo

> **Parce que c'est le même fichier que l'e2e du [lot 12](12-tests.md).**
> Un fichier `tests/e2e/p/warp_utxos.go` te donne gratuitement le réseau, les
> clés pré-financées, `env.GetRandomNodeURI()`, `tc.Eventually`, la
> configuration d'upgrade par flag — et le motif ACP-118 de `tests/e2e/p/l1.go`
> est juste à côté. Quand la phase B est verte, tu n'as pas un PoC **et** un
> e2e : tu as l'e2e.

### 🔴 tranché : `tmpnet`, pas Terraform

Les trois choses que ce PoC prouve — alignement des codecs, trait de découverte,
bascule de VM — ne gagnent **rien** à ce que les nœuds soient sur des machines
distinctes. Cinq processus séparés les prouvent aussi bien. Garde une
infrastructure réelle pour ce qui en a besoin (latence, partition réseau,
supervision), c'est-à-dire pas ce PoC.

---

## Le scénario de test

### Ce qui existe déjà, et ce qu'il reste à écrire

⚠️ **Sous saevm, la C-Chain n'a pas d'API `avax.export` / `avax.import`.** Son
service expose `avax.getUTXOs`, `avax.issueTx`, `avax.getAtomicTx` et
`avax.getAtomicTxStatus` (`vms/saevm/cchain/api.go`) : la transaction atomique se
**construit côté client** puis s'émet.

Les clients existent, et ce sont les bons :

| Besoin | Ce qui le sert |
| :--- | :--- |
| Émettre une transaction atomique C-Chain | `vms/saevm/cchain.Client.IssueTx(ctx, *tx.Tx)` (`api.go:387`) |
| Lister les UTXOs en shared memory côté C | `Client.GetUTXOs` (`api.go:334`) — rend des `*avax.UTXO` déjà désérialisés par le codec saevm |
| Relire une transaction atomique acceptée | `Client.GetTx` (`api.go:411`) |
| Signer un `tx.Export` à la main | `tx.UnsignedBytes` est **exportée** ; `txtest.Sign` si tu es dans un `*testing.T` |
| Émettre et relire côté P | `vms/platformvm.Client` — `IssueTx(ctx, txBytes)` (`client.go:411`), `GetTxStatus`, `GetUTXOs` |
| Sérialiser l'`ImportTx` P-Chain | `platform.Codec.Marshal(platform.CodecVersion, &tx)` |
| Une transaction EVM ordinaire | `e2e.NewEthClient`, `e2e.SendEthTransaction`, `e2e.SuggestGasPrice` |

**Ce qu'il reste à écrire, ce sont trois constructeurs de transaction** — du
harnais, pas du code de nœud :

1. le `tx.Export` C→P dont la sortie exportée est un `warpfx.TransferOutput` ;
2. l'`ImportTx` P-Chain canonique ;
3. l'`ExportTx` P→C autorisée, et son payload `TxAuthorization` (phase B).

> ⚠️ **Le wallet ne sert à rien ici, et la raison est plus large que « il ne sait
> pas construire une sortie `warpfx` ».**
> `wallet/chain/c` est bâti **entièrement sur `graft/coreth/plugin/evm/atomic`**
> — les six fichiers du paquet l'importent. Or après Helicon la C-Chain est
> saevm. Les octets sont compatibles (le `tx_test.go` de saevm porte des
> transactions golden qui affirment que `atomic.Tx` et `tx.Tx` se sérialisent à
> l'identique — c'est toute la raison d'être des `SkipRegistrations`), donc les
> flux secp existants continuent probablement de passer. Mais c'est une
> hypothèse, pas un fait vérifié.
>
> Passer par `vms/saevm/cchain.Client` directement rend la question sans objet.
> Et si `cWallet.IssueExportTx` casse après la bascule, ce n'est pas un incident
> du PoC : c'est un signal, à remonter.

### Phase A — l'aller

| # | Étape | Ce qu'on vérifie |
| ---: | :--- | :--- |
| 1 | Attendre la bascule de VM | Dans les logs, le passage à saevm à `heliconTime − 10 s`. Les cinq nœuds doivent basculer au **même bloc**. |
| 2 | Depuis un EOA pré-financé (`env.PreFundedKey`), construire un `tx.Export` C→P dont la sortie exportée est un `warpfx.TransferOutput{Amt, Owner{ctx.ChainID, addr}}`, le signer avec `tx.UnsignedBytes`, et l'émettre par `Client.IssueTx` | Accepté, puis `avax.getAtomicTxStatus` → `Accepted` |
| 3 | ⚠️ **Faire pointer l'`Owner` vers une adresse EOA *tierce*** | Que financer un tiers ne demande rien de lui — c'est le [lot 10b](10-saevm.md), et c'est ce qui rendra le précompile différable |
| 4 | `platform.getUTXOs` avec l'adresse EVM encodée en Bech32 (`P-<hrp>1…`) | **Le trait.** L'UTXO doit apparaître. S'il n'apparaît pas alors que l'étape 2 a réussi, c'est `Addresses()` — pas l'API, pas la shared memory |
| 5 | Décoder l'UTXO retourné | **L'alignement des codecs.** Le type doit se décoder en `warpfx.TransferOutput`, pas échouer ni se décoder en autre chose |
| 6 | Construire l'`ImportTx` canonique : `Ins` vide, un input par UTXO à `SigIndices` vide, un credential vide par input, **une** sortie `warpfx` au même propriétaire, montant dans la fourchette. Marshaller avec `platform.Codec` et émettre par `platform.issueTx` | Accepté, **sans aucune signature** |
| 7 | `platform.getTxStatus` puis `platform.getUTXOs` | L'UTXO P-Chain existe, sous le même trait |
| 8 | Rejouer l'étape 6 en parallèle depuis un **second nœud** (`env.GetNetwork().GetNodeURIs()`) | Deux soumetteurs reconstruisant la même transaction est le **cas normal** : l'un passe, l'autre échoue proprement sur la shared memory |

⚠️ **Le credential vide de l'étape 6 est un `warpfx.Credential{}`**, côté
P-Chain. Côté saevm (phase B, import miroir) c'est au contraire un
`secp256k1fx.Credential{}` sans signature — voir [10c](10-saevm.md). Les deux
sont corrects, chacun chez soi.

**Critère de fin de phase A** : les étapes 4, 5 et 7 passent sur les cinq nœuds,
et aucun ne diverge.

### Phase B — le retour

⚠️ **Il n'y a pas besoin de contrat Solidity.** Le précompile Warp force
`sourceAddress = caller` et `sourceChainID = ctx.ChainID`
(`graft/coreth/precompile/contracts/warp/contract.go:302-308`), donc **un EOA qui
appelle `sendWarpMessage` directement** produit exactement la paire qu'un
`warpfx.Owner` nomme. `PackSendWarpMessage(payload)` construit l'appel,
`ContractAddress` est `0x0200…0005` (`module.go:23`), et saevm réutilise le même
précompile à la même adresse.

⚠️ **L'agrégation ACP-118 est déjà écrite dans le dépôt**, et pas dans un VM :
`tests/e2e/p/l1.go` monte un pair P2P avec `peer.StartTestPeer` (l.213), envoie
la requête avec `wrapWarpSignatureRequest` (l.846), et récupère la signature avec
`findMessage(genesisPeerMessages, unwrapWarpSignature)`. Sur un réseau où le pair
de genesis porte tout le poids, c'est **une seule signature**
(`set.NewBits(0)`). Copie ce motif ; ne monte pas de service d'agrégation.
`acp118.NewSignatureAggregator` existe mais n'est câblé qu'à l'intérieur des VM.

| # | Étape | Ce qu'on vérifie |
| ---: | :--- | :--- |
| 9 | Construire l'`ExportTx` P→C non signée, en calculer les octets, et appeler `sendWarpMessage` depuis l'EOA propriétaire avec un `TxAuthorization{expiry, txBytes}` | Le chemin du soumetteur : lire le log, agréger via ACP-118 |
| 10 | Soumettre l'`ExportTx` autorisée par `platform.issueTx` | Quorum, engagement, provenance — les trois vérifications du [lot 4](04-autorisation.md) |
| 11 | Attacher le **même** credential à une autre transaction | Doit échouer sur l'engagement. C'est le test 1 du [lot 12](12-tests.md), et il vaut d'être rejoué sur un vrai réseau |
| 12 | Import canonique côté saevm (`Client.IssueTx`) | Solde EVM crédité, **sans exécution de code** — constaté par `ethClient.BalanceAt`, pas par un événement |

### ⚠️ Ce que la phase B a trouvé, et qu'aucun test unitaire n'atteignait

**La borne d'enchère du [lot 10d](10-saevm.md) était insatisfiable**, et la phase
B échoue dessus au premier essai.

Le brûlage est **quantifié** à 1 nAVAX ; le `baseFee` ne l'est pas, et sous
ACP-283 il démarre à 1 wei. Étalé sur les ~10 300 de gas d'un import à un input,
un quantum vaut déjà ~97 000 aAVAX/gas, soit **quatre à cinq ordres de grandeur
au-dessus** de `k × 1`. Aucun import canonique n'était donc incluable au prix
plancher de la chaîne — c'est-à-dire précisément quand elle est la moins chère.

`VerifyCanonicalBid` prend désormais l'enchère minimale exprimable et ne laisse
jamais le plafond descendre en dessous. **Un test unitaire n'y menait pas** : il
aurait fallu deviner la question.

Mesuré sur l'exécution qui passe : brûlage 1 nAVAX, enchère 97 789 aAVAX/gas,
`baseFee` 1 aAVAX/gas.

---

## Les pièges, par ordre de probabilité

La liste a changé : `tmpnet` en supprime deux, et l'usage des bons clients en
supprime un troisième.

1. **L'import échoue au calcul de frais** (`errUnsupportedOutput`) : c'est le lot
   9.1 manquant, pas un problème de vérification. Le message n'y fait aucune
   allusion. → **Le piège n° 1 du PoC.**
2. **L'UTXO n'apparaît pas dans `getUTXOs`** alors que l'export a réussi :
   `Addresses()`. Il est déposé et dépensable, seulement introuvable.
3. **Le décodage échoue à l'étape 5** : `SkipRegistrations(34)` du lot 10a. Si le
   lot 2 a déplacé 43/44/45, ce nombre a bougé avec — `TestWarpUTXOsCodecTypeIDs`
   te l'aura dit avant.
4. **Le wallet C-Chain se comporte bizarrement post-bascule** : il vise coreth
   (voir plus haut). Ne l'utilise pas ; s'il casse, note-le et continue.
5. **La bascule n'arrive jamais** : `--activate-latest-after` oublié, ou
   `heliconTime` laissé à `UnscheduledActivationTime`. Vérifie dans les logs que
   la configuration d'upgrade rendue est bien celle attendue.

> ✅ **Ce qui n'est plus un piège** : `--upgrade-file` ignoré parce que le
> `network-id` est resté `local`, `heliconTime` divergent entre nœuds,
> `graniteEpochDuration` en chaîne au lieu de nanosecondes, genesis produit avant
> les certificats. tmpnet rend les quatre inexprimables.

---

## Ce qui reste incertain

🟠 **À vérifier au premier déploiement**

- **Le comportement exact de la bascule sur un réseau neuf.** Le raisonnement
  sur le bloc de transition vient de
  [`vms/transitionvm/README.md`](../../vms/transitionvm/README.md) ; je ne l'ai
  pas exécuté. C'est la raison de programmer Helicon dans le futur plutôt qu'au
  genesis — et `UpgradeConfig(d)` avec `d > 0` le fait pour toi.
- **Que le wallet C-Chain survive à la bascule.** `wallet/chain/c` vise coreth ;
  les octets sont compatibles par construction, mais l'affirmation n'est vérifiée
  par aucun test qui traverse la transition. Le PoC ne s'appuie pas dessus, mais
  ce qu'il en observe est une information utile à remonter.
✅ **Réglé — l'agrégation à cinq nœuds.** Le motif de `l1.go` prend la signature
du seul pair de genesis, qui porte tout le poids *dans son réseau*. Le PoC
interroge chaque validateur à son tour et agrège : 5 signatures sur 5, largement
au-dessus des 67 % requis. Le bitset indexe l'ensemble **canonique**, ordonné par
clé publique non compressée, donc chaque signature est placée au rang de son
validateur et non dans l'ordre des réponses.

🔴 **À trancher**

- Rien. Le choix `tmpnet` / infrastructure réelle est tranché ci-dessus, et le
  reste découle des lots.

🟡 **À mesurer**

- Rien dans ce PoC. Les mesures bloquantes sont au [lot 9](09-tarification.md)
  (forfait `intrinsicWarpDBReads` à l'échelle du Primary Network) et au
  [lot 11](11-precompile.md), différé.
