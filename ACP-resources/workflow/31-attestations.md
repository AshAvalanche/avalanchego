# Les attestations de retour

## Ce qu'elles font

L'aller porte des octets de transaction de la C-Chain vers la P-Chain. Le retour est son exact symétrique : un message Warp **signé par la P-Chain**, attestant du sort d'une autorisation, que n'importe qui peut faire lire par un contrat C-Chain. C'est ce qui referme la boucle et rend un protocole autonome possible.

## Variantes d'acteur

| Acteur | Rôle |
| :--- | :--- |
| **Le demandeur** | N'importe qui. Il demande la signature au handler ACP-118 de chaque validateur et agrège |
| **Le livreur** | N'importe qui, pas nécessairement le demandeur. Il construit la transaction EVM qui porte l'attestation |
| **Le contrat destinataire** | Lit l'attestation par `getVerifiedWarpMessage` et met son état à jour |

⚠️ **Rien n'est émis.** Il n'y a ni file d'attente, ni message produit dans un bloc, ni structure à conserver. **Tout est dérivé à la demande de l'état accepté**, exactement comme l'ACP-77 — donc la question de la rétention ou de la purge ne se pose pas, et l'invariant « aucun état ajouté à la P-Chain » tient.

## Pourquoi elles sont nécessaires

Un propriétaire engagé sur les octets d'une transaction semble savoir déjà tout ce qu'elle fera. C'est faux sur deux points, tous deux structurels.

**1. Le `txID` est malléable.** Le propriétaire s'engage sur les octets **non signés**. Le `txID` est le hash des octets **signés**, credential compris, et le credential porte la signature BLS agrégée et le bitset des signataires — qui dépendent du sous-ensemble de validateurs que le soumetteur a effectivement joint. Deux soumissions de la même autorisation produisent donc **deux `txID` distincts**. Une seule peut être acceptée, puisqu'elles consomment les mêmes inputs, mais le propriétaire ne peut pas prédire laquelle. Or un UTXO est nommé par `(txID, index)` : sans attestation, **un contrat ne peut pas nommer les UTXOs que sa propre transaction vient de créer**, ni son change, donc ne peut pas les référencer dans l'autorisation suivante.

**2. La récompense n'est prévisible ni en montant ni en position.** Le remboursement du stake est déterministe une fois le `txID` connu. La récompense, non : son versement dépend de l'issue du `RewardValidatorTx`, fonction de l'uptime mesuré, et **son `OutputIndex` dépend de cette même issue** — voir [`22-reward.md`](22-reward.md).

## Les trois messages

Enregistrés en **positions 5, 6 et 7** du registre `vms/platformvm/warp/message/codec.go`, à la suite de l'autorisation. `AddressedCall` à `SourceAddress` **vide**, chaîne source = P-Chain.

### `TxExecuted`

```
codecID uint16 | typeID uint32 | txID [32]byte | authHash [32]byte
```

« La transaction `txID`, dont les octets non signés ont pour empreinte `authHash`, est acceptée. »

`authHash = sha256(txBytes)`, exactement le champ du message d'autorisation. Le contrat l'a émis, donc il peut le recalculer ou l'avoir mémorisé : c'est la clé qui relie l'attestation à l'autorisation. **Le `txID` est l'information nouvelle.**

### `StakeSettled`

```
codecID uint16 | typeID uint32 | stakingTxID [32]byte
| sourceChainID [32]byte | sourceAddress [20]byte
| numRewards uint32 | { outputIndex uint32 | amount uint64 } × numRewards
```

« Le staking ouvert par `stakingTxID` est clos, et il a produit pour `(sourceChainID, sourceAddress)` les UTXOs de récompense que voici. »

⚠️ **Le propriétaire est nommé par sa paire, pas par un hash.** Vingt octets de plus dans le message, et une reproduction de codec en moins côté Solidity : un contrat vérifie qu'il est concerné par `sourceAddress == address(this)`, au lieu de réimplémenter la sérialisation d'un `linearcodec` Go pour recalculer une empreinte. C'est aussi exactement la paire que porte le `warpfx.Owner` d'un UTXO, donc le vérifieur compare des structures.

Le message **nomme** les UTXOs au lieu d'en donner la somme, et trois champs sont absents parce qu'ils seraient redondants : pas de `txID` par entrée — tous portent `TxID = stakingTxID` ; pas d'`owner` par entrée — le message est cadré par la paire en tête ; pas de somme agrégée — une donnée redondante dans un message signé, c'est deux sources de vérité que rien n'oblige à concorder.

⚠️ **Le montant par UTXO est requis pour une raison plus forte que la comptabilité** : dépenser un UTXO demande un `TransferableInput` dont l'`Amt` égale **exactement** celui de la sortie. Une somme agrégée décrirait un solde que son propriétaire ne peut pas dépenser.

`numRewards = 0` signifie « clos sans récompense ». Ce n'est jamais confondu avec « pas encore clos », le message n'étant signé qu'une fois le staker sorti de l'ensemble. La liste reste minuscule : au plus deux entrées pour un validateur, une pour un délégateur.

⚠️ **L'ordre est normatif, et ce n'est pas cosmétique.** `GetRewardUTXOs` retourne l'ordre de la base, et l'agrégation BLS exige que tous les validateurs signent des **octets identiques**. Sans tri, deux nœuds produisent deux messages différents et **aucun quorum ne se forme jamais** — un échec silencieux qui n'apparaît qu'en intégration. `StakeSettled.Verify()` refuse une liste non triée plutôt que de la retrier en silence — le tri est **strictement** croissant, deux récompenses ne pouvant partager un `outputIndex` — et `verifyRewardsMatch` trie la liste qu'il reconstruit depuis l'état avant de la comparer.

### `CycleSettled`

```
codecID uint16 | typeID uint32 | rewardTxID [32]byte
| sourceChainID [32]byte | sourceAddress [20]byte
| numRewards uint32 | { outputIndex uint32 | amount uint64 } × numRewards
```

« Le cycle clos par `rewardTxID` a produit pour ce propriétaire les UTXOs que voici. »

⚠️ **`StakeSettled` et `CycleSettled` nomment la même chose sous deux clés différentes**, et ce n'est pas un choix : c'est la clé sous laquelle le code indexe les récompenses.

| Staker | Clé de `GetRewardUTXOs` | Fréquence |
| :--- | :--- | :--- |
| `AddPermissionlessValidatorTx` / `…Delegator` | le `txID` **de staking** | une fois, à la clôture |
| `AddAutoRenewedValidatorTx` | le `txID` **du `RewardAutoRenewedValidatorTx`** | une fois **par cycle** |

Un `StakeSettled` demandé sur un validateur auto-renouvelé annoncerait donc toujours `numRewards = 0` — cohérent avec sa propre définition, et parfaitement trompeur, à un propriétaire pourtant payé à chaque cycle. C'est précisément le piège que la séparation évite.

⚠️ **Et la séparation le referme plus fort que ça.** La règle de signature de `StakeSettled` restreint le type de la transaction aux deux stakings permissionless : un tel message n'est donc pas seulement trompeur, il est **impossible à demander** — le handler refuse sur `ErrNotAStakingTx` avant même de regarder les récompenses.

Deux champs sont absents de `CycleSettled`, et leur absence se justifie comme celle des autres :

- **pas de `stakingTxID`** : `GetTx(rewardTxID)` rend un `*platform.RewardAutoRenewedValidatorTx` dont le champ `TxID` **est** le `txID` de staking ;
- **pas de « le staker est sorti »** : un `RewardAutoRenewedValidatorTx` `Committed` est définitif **pour son cycle**, que le validateur continue ou non. La règle de signature en est plus courte que celle de `StakeSettled`, pas plus longue.

⚠️ **Le remboursement du principal n'est dans aucun des trois messages.** `unstakeUTXOs` n'appelle que `AddUTXO`, sous le `txID` **de staking**, aux index `len(outputs) + i`. Un contrat qui tient ce `txID` — que `TxExecuted` lui a donné — les calcule lui-même.

## Règles de signature

Le handler signe si et seulement si les conditions tiennent contre l'**état accepté**. Aucune ne demande d'index nouveau.

### `TxExecuted` — `verifyTxExecuted`

1. `GetTx(txID)` existe → sinon `ErrTxDoesNotExist`.
2. Statut `Committed` — posé à l'acceptation du bloc → sinon `ErrTxNotCommitted`.
3. `sha256(tx.Unsigned.Bytes()) == msg.AuthHash` → sinon `ErrMismatchedAuthHash`.
4. La transaction porte au moins un `*warpfx.Credential` → sinon `ErrTxHasNoWarpCredential`.

⚠️ **La condition 4 borne volontairement le périmètre.** Sans elle, la P-Chain deviendrait un oracle d'acceptation de transactions à usage général : utile, sans doute, mais c'est une autre proposition, et une surface que celle-ci devrait alors maintenir.

### `StakeSettled` — `verifyStakeSettled`

1. `msg.Verify()` — la liste doit déjà être triée → sinon `ErrRewardsNotSorted`.
2. `GetTx(stakingTxID)` `Committed`, et de type `AddPermissionlessValidatorTx` ou `AddPermissionlessDelegatorTx` → sinon `ErrNotAStakingTx`. ⚠️ C'est ce point qui rend indemandable un `StakeSettled` sur un `AddAutoRenewedValidatorTx`, dont les récompenses ne sont pas indexées sous ce `txID`.
3. Porte un `*warpfx.Credential` → sinon `ErrTxHasNoWarpCredential`.
4. **Le staker n'est plus ni courant ni pending** → sinon `ErrStakingNotSettled`.
5. La liste égale exactement les récompenses de ce propriétaire → sinon `ErrMismatchedRewards`.

### `CycleSettled` — `verifyCycleSettled`

1. `msg.Verify()` — la liste doit déjà être triée.
2. `GetTx(rewardTxID)` rend un `*platform.RewardAutoRenewedValidatorTx` `Committed` → sinon `ErrNotACycleTx`.
3. `GetTx(rewardTx.TxID)` rend un `*platform.AddAutoRenewedValidatorTx` portant un `*warpfx.Credential` → sinon `ErrTxHasNoWarpCredential`.
4. La liste égale exactement les `GetRewardUTXOs(rewardTxID)` de ce propriétaire → sinon `ErrMismatchedRewards`.

⚠️ **La condition de périmètre porte sur la transaction de *staking*, pas sur celle de récompense.** Un `RewardAutoRenewedValidatorTx` n'a aucun credential — il refuse explicitement `len(Creds) != 0` — donc y chercher un `*warpfx.Credential` refuserait toujours. C'est l'`AddAutoRenewedValidatorTx` qu'il nomme qui porte l'autorisation.

⚠️ **Les transactions de proposition sont bien dans `GetTx`** : `block/executor/verifier.go` fait `onCommitState.AddTx(tx, Committed)` et `onAbortState.AddTx(tx, Aborted)`. Un cycle *aborted* existe donc aussi dans l'état, avec ses propres UTXOs de récompense, et le statut exigé au point 2 est ce qui les sépare.

**Comment on établit qu'un staking est clos.** L'état n'indexe pas les stakers par transaction — toutes les API de lecture sont typées `(subnetID, nodeID)` — mais la transaction de staking **porte les deux**. `GetTx(stakingTxID)` les rend donc directement, et le reste suit :

| Cas | Accès | Coût |
| :--- | :--- | :--- |
| Validateur | `GetCurrentValidator(subnetID, nodeID)` puis `GetPendingValidator`, comparaison de `staker.TxID` | **O(1)** |
| Délégateur | `GetCurrentDelegatorIterator(subnetID, nodeID)` puis `GetPendingDelegatorIterator` | itération bornée aux délégateurs **d'un** validateur |

Aucune `justification` n'est nécessaire : dans les deux cas le message porte assez d'information pour que le vérifieur retrouve seul l'état à consulter.

**La sélection des récompenses** — `verifyRewardsMatch` filtre `GetRewardUTXOs(stakingTxID)` sur les sorties `*warpfx.TransferOutput` dont l'`Owner` égale la paire du message — `SourceChainID` **et** `SourceAddress` —, les réduit à `(outputIndex, amount)`, puis trie. Un validateur qui désigne deux propriétaires distincts demande donc **deux messages**, chacun cohérent, et aucun ne voit les récompenses de l'autre.

⚠️ **`GetRewardUTXOs` ne fait pas partie de `state.Chain`**, l'interface que le handler recevait. Le paquet réseau définit donc une interface locale `network.Chain` qui l'élargit. `state.Chain` déclare `AddRewardUTXO` et aucun getter.

⚠️ **Le handler n'est ni limité en débit ni caché.** `vms/platformvm/network/network.go` monte `acp118.NewHandler(verifier, signer)` sans throttler, et `acp118.NewHandler` délègue à `NewCachedHandler(&cache.Empty{})` : chaque requête re-exécute `Verify` **et** `signer.Sign`. C'est le comportement existant, hérité des messages ACP-77 servis par le même handler ; l'algorithme O(1) ci-dessus garde le surcoût du même ordre.

## Lecture côté C-Chain

**Zéro ligne à écrire côté C-Chain.** Le prédicat Warp traite déjà nommément le cas P-Chain : pour `SourceChainID == ids.Empty`, le set de signataires retenu est celui du subnet de la chaîne réceptrice, soit le Primary Network. Une attestation est donc **exactement aussi sûre que la P-Chain elle-même**.

⚠️ **Le message transite par l'access list de la transaction de livraison.** Il est complété d'un délimiteur `0xff`, zero-paddé à un multiple de 32 octets, découpé en chunks, et placé dans un access tuple `(adresse du précompile Warp, storageKeys)`. `predicate.FromAccessList` traite **chaque occurrence de l'adresse comme un prédicat distinct** — c'est ce qu'indexe `getVerifiedWarpMessage(index)`.

Quatre conséquences pour un contrat destinataire :

- **Il ne peut pas aller chercher une attestation lui-même.** Le prédicat appartient à la **transaction**, pas à l'appel : il est vérifié dans `CheckTxPredicates` avant exécution, et le résultat est un bitset indexé par `(txHash, adresse du précompile)`. Son point d'entrée doit donc être appelable par n'importe qui.
- **`Valid` n'est pas une exception.** Prédicat absent ou invalide → `Valid: false`, **pas** un revert. Un contrat qui ne teste pas ce booléen accepte un message non vérifié.
- **`sourceChainID` vaut `bytes32(0)`** — l'ID de la P-Chain est `ids.Empty`.
- **`originSenderAddress` vaut `address(0)`** — l'adresse source de l'`AddressedCall` est vide.

Un contrat qui teste « chaîne source non nulle » ou « expéditeur non nul » rejette **toutes** les attestations. Il doit au contraire vérifier `sourceChainID == bytes32(0) && originSenderAddress == address(0)`, puis le `typeID` du payload. Ce couple est non usurpable : aucune adresse C-Chain ne peut émettre depuis `address(0)`, et aucune chaîne autre que la P-Chain ne porte l'ID nul.

**Coût.** `VerifyPredicateBase + PerWarpMessageChunk × nChunks + PerWarpSigner × N`, soit `125 000 + 512 × nChunks + 250 × N` depuis Granite. `PredicateGas` **remplace** le coût EIP-2930 standard au lieu de s'y ajouter. Le terme dominant est `PerWarpSigner` — c'est un coût pour le livreur, pas pour le protocole.

## Ce qu'elles débloquent

Avec le `txID` accepté et l'identité des UTXOs de récompense, un contrat tient dans son propre stockage la liste de ses UTXOs P-Chain vivants. À la livraison d'un `TxExecuted` il **ajoute** les sorties de la transaction qu'il a autorisée — dont il connaît montants et propriétaires, puisqu'il en a écrit les octets — et **retire** celles qu'elle consommait. À la livraison d'un `StakeSettled` il ajoute les récompenses.

⚠️ **Le registre est une borne inférieure, jamais un inventaire.** N'importe qui peut alimenter n'importe quel `WarpOwner`, et recevoir ne produit aucune attestation. Un contrat est **autonome sur les fonds qu'il a causés, aveugle à ceux qu'il a reçus**. C'est une propriété à assumer : l'écart joue toujours en sa faveur, puisqu'il détient au moins ce qu'il a compté.

⚠️ **Le rejeu est neutralisé côté contrat, pas côté protocole.** Une attestation est éternelle comme tout message Warp et n'a pas d'`expiry` — ce qui serait sans objet, un fait accepté restant vrai. C'est au contrat de marquer `authHash`, respectivement `stakingTxID`, comme consommé à la première présentation, faute de quoi une attestation de récompense rejouée crédite deux fois.

⚠️ **Un contrat ne peut pas parser `txBytes`.** S'il se contente d'autoriser des octets qu'un opérateur lui présente, l'attestation ne lui apprend que « ces octets-là sont passés ». Un protocole autonome doit **construire lui-même les octets**.

## Ce qui n'existe pas encore

- **Une récompense en `warpfx.TransferOutput` n'est utile qu'une fois le volet C-Chain en place** : la boucle est complète côté P-Chain, et le retour des fonds dans un solde EVM passe par l'export P→C puis l'import canonique.
- **Le staking auto-renouvelé est couvert par `CycleSettled`**, pas par `StakeSettled` — les deux ne partagent pas la clé de leurs récompenses. Voir [`24-add-auto-renewed-validator.md`](24-add-auto-renewed-validator.md) et [`22-reward.md`](22-reward.md).

## Symboles concernés

- `vms/platformvm/warp/message/tx_executed.go` — `TxExecuted`
- `vms/platformvm/warp/message/stake_settled.go` — `StakeSettled`, `Reward`, `Verify`, `verifyRewardsSorted`
- `vms/platformvm/warp/message/cycle_settled.go` — `CycleSettled`
- `vms/platformvm/warp/message/codec.go` — positions 5 et 6
- `vms/platformvm/network/warp_utxos.go` — `verifyTxExecuted`, `verifyStakeSettled`, `verifyCycleSettled`, `verifyRewardsMatch`, `isStakingSettled`, `hasWarpCredential`, `Chain`
- `vms/platformvm/network/warp.go` — `Chain`, le `switch` par type de payload
- `vms/platformvm/state/state.go` — `GetRewardUTXOs`
- `graft/coreth/precompile/contracts/warp/` — lecture par le précompile Warp, inchangé
