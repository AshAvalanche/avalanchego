# L'autorisation par message Warp

## Ce qu'elle fait

Elle remplace la signature secp256k1 comme condition de dépense : un message Warp signé par quorum, portant les octets exacts de la transaction non signée, autorise cette transaction et elle seule.

## Variantes d'acteur

| Acteur | Comment il émet |
| :--- | :--- |
| **`WarpOwner`-EOA** | Il appelle `sendWarpMessage` du précompile Warp depuis une transaction EVM ordinaire |
| **`WarpOwner`-contrat** | Le contrat appelle le même précompile |

⚠️ **Aucune différence côté P-Chain.** Le `SourceAddress` de l'`AddressedCall` est forcé à l'appelant du précompile, inconditionnellement : la paire `(chaîne source, adresse source)` ne dit pas si l'adresse porte du code. Un EOA qui appelle le précompile directement est un `WarpOwner` au même titre qu'un contrat.

La seule différence est en amont : qui construit les octets. Un contrat autonome doit les **composer lui-même** à partir de paramètres structurés, sans quoi il ne sait pas ce qu'il autorise. Un smart account piloté par des humains n'en a pas besoin, ses signataires lisant la transaction dans leur outillage.

## Le format

Le payload est enregistré en **position 4** du registre `vms/platformvm/warp/message/codec.go`, à la suite des quatre payloads ACP-77.

```
codecID uint16 | typeID uint32 | expiry uint64 | txBytes []byte (préfixé uint32)
```

- **`txBytes`** : la sérialisation de la transaction P-Chain **non signée**, exactement les octets que `Tx.Sign` fait signer à un EOA.
- **`expiry`** : timestamp Unix au-delà duquel l'autorisation n'est plus acceptable, comparé au `chainTime` du bloc. C'est le **seul** garde-fou temporel du design.

Le payload ne porte rien d'autre : ni nonce, ni plafond de frais, ni description du destinataire. Tout est déjà dans `txBytes`, et une donnée redonnée ici serait une seconde source de vérité que rien n'obligerait à concorder.

**Pourquoi la transaction complète et non son empreinte.** `sha256(txBytes)` serait équivalent en sécurité — c'est exactement ce qu'un EOA signe — mais **le soumetteur ne peut pas reconstruire une transaction à partir de 32 octets**. En portant la préimage, le log C-Chain contient la transaction entière : aucun canal hors-bande, et la logique du soumetteur devient un chemin unique, agnostique au type de transaction.

## Où le message voyage

Dans **`Creds`**, comme le ferait une signature, et pour la même raison : le message **contient `txBytes`**, donc l'y placer exigerait que `txBytes` se contienne lui-même. `Creds` étant déjà typé `[]verify.Verifiable`, aucune modification de `platform.Tx` n'est requise.

> **Règle d'encodage.** Parmi les credentials d'une transaction, **un seul** est un `warpfx.Credential` à `WarpMessage` non vide. Tous les autres slots faisant face à un input `warpfx` portent un `warpfx.Credential{}` vide.

⚠️ **Le slot porteur n'est pas nécessairement le premier.** `Creds` est parallèle à `Ins ‖ ImportedInputs`, donc dans une transaction mêlant des inputs secp256k1 le slot d'indice 0 est un credential secp. `findWarpAuthorization` le localise par un **balayage**, jamais par sa position, et refuse deux porteurs sur `ErrMultipleWarpAuthorizations`.

**Il en découle, sans qu'aucune règle ne le dise, qu'une transaction autorisée ne consomme les UTXOs que d'un seul `WarpOwner`** : il n'y a qu'une autorisation, et la provenance la compare au propriétaire de *chaque* UTXO. Les **sorties**, elles, ne sont pas contraintes.

## Les trois vérifications, et où elles vivent

| Vérification | Où | Fréquence | Toujours exécutée ? |
| :--- | :--- | :--- | :--- |
| **quorum BLS** | `txs/executor/warp_verifier.go` | 1× par transaction | **non** — sautée pendant le bootstrap, mise en cache par `pChainHeight` |
| **engagement + expiry** | `txs/executor/authorization.go` | 1× par transaction | **oui** |
| **provenance** | `warpfx.Fx.VerifyTransferWithContext` | 1× par UTXO consommé | **oui** |

Le critère : ce qui dépend du contexte réseau va dans le vérifieur Warp, ce qui est une fonction pure de la transaction et du temps de chaîne va dans le chemin d'exécution déterministe.

⚠️ **Aucune des trois n'est facultative, et l'engagement est la plus importante.** Il est tentant de le croire redondant avec la provenance. La provenance seule répondrait à « ce message vient-il du propriétaire de cet UTXO ? », mais **pas** à « ce message autorise-t-il *cette* transaction ? ». Or un message Warp est public : il figure dans un log C-Chain que tout relayeur observe. Sans l'engagement, n'importe qui pourrait le ramasser et l'attacher à une transaction de son choix consommant les UTXOs de ce propriétaire — le message cesserait d'être une autorisation pour devenir un **jeton au porteur sur l'intégralité de ses fonds**.

### 1. Le quorum

`VerifyWarpMessages(ctx, networkID, validatorState, pChainHeight, tx *platform.Tx)` — le paramètre est la transaction **signée**, parce que le message vit dans `Creds`.

Le `warpVerifier` est un `platform.TxVisitor`. **Sept** méthodes appellent `verifyAuthorization()` : `ImportTx`, `ExportTx`, `BaseTx`, `AddPermissionlessValidatorTx`, `AddPermissionlessDelegatorTx`, `AddAutoRenewedValidatorTx`, `SetAutoRenewedValidatorConfigTx`. Deux gardent leur vérification ACP-77 (`RegisterL1ValidatorTx`, `SetL1ValidatorWeightTx`). Toutes les autres renvoient `nil`.

`verifyAuthorization` localise le credential porteur, et **ne fait rien s'il n'y en a pas** — les mêmes types de transaction sont dépensés par des propriétaires secp. Sinon : `warp.ParseMessage` → `GetCanonicalValidatorSetFromChainID(pChainHeight, msg.SourceChainID)` → `Signature.Verify(…, 67, 100)`. Le `NetworkID` fait partie du message signé, ce qui donne l'anti-rejeu inter-réseaux gratuitement.

⚠️ **Ce vérifieur ne connaît pas le temps** et n'est **pas toujours exécuté**. Ses paramètres ne portent ni bloc ni état, et l'un de ses points d'appel vérifie une transaction issue du gossip, hors de tout bloc. C'est correct pour un quorum et faux pour une authentification.

### 2. L'engagement et l'expiry

`resolveAuthorization(tx *platform.Tx, chainTime uint64) (*fx.Context, error)` :

1. `findWarpAuthorization(tx.Creds)` → 0 ou 1 porteur, sinon `ErrMultipleWarpAuthorizations`.
2. Aucun porteur → `&fx.Context{}`, sans autorisation. C'est le cas ordinaire d'une transaction signée en secp.
3. Un porteur sur une transaction hors liste → `ErrWarpAuthorizationNotAccepted`.
4. `warp.ParseMessage` → `payload.ParseAddressedCall` → `message.ParseTxAuthorization`.
5. **Engagement** : `bytes.Equal(authorization.TxBytes, tx.Unsigned.Bytes())`, sinon `ErrAuthorizationMismatch`.
6. **Expiry** : `chainTime > authorization.Expiry` → `ErrAuthorizationExpired`. L'égalité est valide.
7. → `fx.Context{Authorization: &warpfx.Authorization{SourceChainID, SourceAddress}}`.

⚠️ **Le `chainTime` ne voyage pas dans le contexte.** Il est un paramètre de `resolveAuthorization`, sert à comparer l'`expiry`, puis il est oublié : `warpfx` n'a **aucune** règle temporelle à évaluer — pas de `Locktime` sur la sortie, et pas de `stakeable.LockOut` autour d'elle.

L'engagement porte sur la **même donnée** que `secp256k1fx.Fx.VerifyCredentials`, qui calcule déjà `hashing.ComputeHash256(utx.Bytes())`, et l'interface `UnsignedTx` que le Fx reçoit n'expose que `Bytes()`. Là où secp vérifie qu'une signature sur ces octets provient d'une clé, `warpfx` vérifie que ces octets sont ceux qu'un quorum a approuvés.

⚠️ **`chainTime`, jamais l'horloge locale.** `secp256k1fx` teste son locktime contre le timestamp local du nœud, comportement à **ne pas** reproduire : l'`expiry` est une règle de consensus.

⚠️ **`resolveAuthorization` est appelée avant toute garde de bootstrap** de l'exécuteur qui l'appelle. Le chemin déterministe n'est pas uniformément exécuté — les deux vérifieurs de staking font `if !backend.Bootstrapped.Get() { return nil }` avant tout, et la branche shared memory de l'`ImportTx` est également gardée — donc placer l'appel à côté du flow check sauterait l'engagement précisément sur les transactions qui en ont le plus besoin.

### 3. La provenance

`warpfx.Fx.VerifyTransferWithContext(fxCtx, tx, in, cred, utxo)`, appelée **une fois par UTXO consommé** :

1. `utxo` doit être un `*warpfx.TransferOutput` → `ErrWrongUTXOType`.
2. `in` doit être un `*secp256k1fx.TransferInput` à `SigIndices` **vide** → `ErrWrongInputType`, `ErrUnexpectedSigIndices`.
3. `cred` doit être un `*warpfx.Credential`, porteur ou vide → `ErrWrongCredentialType`.
4. `verify.All(out, in, cred)` puis `out.Amt == in.Amt` → `ErrMismatchedAmounts`.
5. `fxCtx` nul, ou dont l'`Authorization` n'est pas un `*warpfx.Authorization` → `ErrNoAuthorization`.
6. `authorization.Authorizes(&out.Owner)` compare `(SourceChainID, SourceAddress)` → `ErrWrongOwner`.

> **L'autorisation est un paramètre et n'est jamais conservée.** `warpfx.Fx` est une structure **sans champ**, ce qui rend la rémanence structurellement impossible. Une autorisation qui survivrait d'une vérification à la suivante autoriserait une transaction qu'elle n'a jamais approuvée : un vol silencieux qu'aucun test écrit transaction par transaction ne révèle.

## La liste autorisée

| Transaction | Usage |
| :--- | :--- |
| `ImportTx` / `ExportTx` | flux C↔P |
| `AddPermissionlessValidatorTx` | staking Primary Network |
| `AddPermissionlessDelegatorTx` | délégation Primary Network |
| `BaseTx` | consolidation à la demande, invalidation d'une autorisation en attente |

Elle est appliquée **de deux façons, dont une seule est structurelle**.

**La structurelle** : seuls ces sept exécuteurs appellent les points d'entrée `…WithContext` du vérifieur d'UTXOs. Tout autre chemin passe par les points d'entrée historiques, qui transmettent un `fxCtx` **nul**, et un UTXO `warpfx` y échoue sur `ErrNoAuthorization`. **Oublier d'ajouter une transaction à la liste ne peut donc rien ouvrir.**

**L'ergonomique** : `acceptsWarpAuthorization` est un `switch` à liste positive et défaut `false`. Un type de transaction ajouté plus tard hérite du refus, jamais de l'autorisation. Sans elle, l'échec serait le même mais le message parlerait d'une autorisation manquante plutôt que du type de transaction.

⚠️ **`SetAutoRenewedValidatorConfigTx` est la seule à demander deux autorisations** : elle dépense, et elle prouve l'assentiment du `ValidatorAuthority` du validateur par `VerifyPermissionWithContext`. Un seul message couvre les deux, puisqu'il s'engage sur les octets de la transaction entière. Voir [`25-set-auto-renewed-config.md`](25-set-auto-renewed-config.md).

⚠️ **Le refus côté subnet reste structurel.** `verifySubnetAuthorization` emprunte le point d'entrée **sans** contexte, donc un `WarpOwner` propriétaire de subnet y tombe toujours sur `ErrPermissionUnsupported`. C'est le point d'entrée qui distingue les deux cas, jamais le type du propriétaire.

Le test `TestAcceptsWarpAuthorization` énumère tous les types et fige la réponse pour chacun : sept `true`, tout le reste `false`.

## Le rôle du soumetteur

Il observe les logs `SendWarpMessage` de la C-Chain, exactement comme un relayeur ICM. Pour chaque message portant le `typeID` de l'autorisation : extraire `txBytes` et le désérialiser, collecter les signatures BLS via le handler ACP-118, construire `Tx{Unsigned, Creds}` avec un slot par input dans l'ordre `Ins ‖ ImportedInputs` dont un seul porte le message, soumettre via `platform.issueTx`.

Ce chemin est **unique et agnostique au type de transaction** : aucune logique par transaction à écrire, là où l'ACP-77 demande une construction dédiée par type de message. Il n'a besoin d'**aucun fonds P-Chain**, la transaction étant auto-financée par les UTXOs du propriétaire.

⚠️ **Le soumetteur n'a aucune latitude.** La transaction est figée avant l'autorisation : il ne peut ni détourner le change, ni le fragmenter, ni le geler. Ses seuls degrés de liberté sont de soumettre ou non, et quand.

## La tarification du credential

La complexité d'une transaction est calculée sur `tx.Unsigned`, et les messages Warp que l'ACP-77 tarifie sont des **champs** de la transaction non signée. Un credential `warpfx` ne peut pas l'être — il contient précisément ces octets — donc il échapperait au calculateur. Laissé tel quel, la vérification BLS agrégée d'un ensemble de plus de 1 000 signataires serait **gratuite**.

`fee.Calculator` gagne donc `CalculateFeeWithCredentials(tx *platform.Tx)`, à côté de `CalculateFee(tx platform.UnsignedTx)`. Seules les sept transactions de la liste autorisée l'appellent ; les autres gardent la forme historique, qui reste correcte pour elles.

`CredentialComplexity` applique `WarpComplexity` au message porteur — bande passante linéaire en sa taille, forfait de lectures d'état, calcul linéaire en nombre de signataires — et **une seule fois**, une transaction ne portant qu'une autorisation. Les signatures secp ne sont pas tarifées ici : elles le sont déjà par les inputs qui les référencent, via le nombre d'indices déclarés.

⚠️ **La comptabilité de gas des blocs compte la même chose.** `SignedTxComplexity(tx)` remplace `TxComplexity(tx.Unsigned)` aux trois endroits qui rationnent l'espace de bloc — `block/executor/verifier.go`, `block/executor/manager.go` (admission au mempool), `block/builder/builder.go`. Sans cela le frais serait juste mais la capacité fausse : un credential qu'aucune dimension ne compte est de la bande passante diffusée et stockée, et du calcul dépensé à le vérifier, gratuitement.

## Ce qui n'existe pas encore

Rien de ce parcours.

## Symboles concernés

- `vms/platformvm/warp/message/tx_authorization.go` — `TxAuthorization`, `NewTxAuthorization`, `ParseTxAuthorization`
- `vms/platformvm/warp/message/codec.go` — position 4
- `vms/platformvm/txs/executor/authorization.go` — `resolveAuthorization`, `findWarpAuthorization`, `acceptsWarpAuthorization`
- `vms/platformvm/txs/executor/warp_verifier.go` — `VerifyWarpMessages`, `warpVerifier.verifyAuthorization`
- `vms/warpfx/fx.go` — `VerifyTransferWithContext`
- `vms/warpfx/authorization.go` — `Authorization.Authorizes`
- `vms/platformvm/utxo/verifier.go` — `VerifySpendWithContext`, `VerifySpendUTXOsWithContext`
