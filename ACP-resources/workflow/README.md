# `workflow/` — les parcours de transaction

Ce dossier décrit, transaction par transaction, **ce qui se passe réellement** : ce qui est construit, par qui, ce qui est vérifié, dans quel ordre, et ce que cela change dans l'état.

## Ce que ce dossier n'est pas

**Ce n'est pas un historique.** Aucune section « modifications », aucun « auparavant c'était X », aucune date, aucun numéro de commit. Présent de l'indicatif, un seul état du monde : celui du code au moment où on lit.

Quand une implémentation change ou qu'un bug est corrigé, **le fichier concerné est corrigé sur place**, dans le même lot que le code. Un lot qui modifie un chemin de vérification sans corriger le parcours correspondant est incomplet.

## Le gabarit

Chaque parcours suit la même trame, pour que deux parcours soient comparables et qu'une divergence saute aux yeux.

| Section | Contenu |
| :--- | :--- |
| Ce qu'elle fait | Une phrase, en termes de valeur |
| Variantes d'acteur | secp / `WarpOwner`-EOA / `WarpOwner`-contrat, et ce qui change **réellement** |
| Construction | Qui construit, à partir de quoi, contraintes d'encodage |
| Autorisation | Signature, message Warp, ou aucune |
| Soumission | Par qui, par quel endpoint |
| **Vérification, dans l'ordre du code** | Les symboles réels, dans l'ordre réel d'exécution |
| Effets d'état | UTXOs consommés/produits, shared memory, solde EVM, index |
| Ce qui est répercuté ailleurs | Traits, `atomicRequests`, attestations rendues signables |
| Cas d'échec | Ce que chaque échec laisse derrière lui |
| Symboles concernés | Chemins de fichiers |

La section **« Vérification, dans l'ordre du code »** porte la valeur du document, et c'est elle qui périme le plus vite. Une revue la confronte au diff.

## Index

| Fichier | Couvre |
| :--- | :--- |
| [`00-acteurs.md`](00-acteurs.md) | EOA secp, `WarpOwner`-EOA, `WarpOwner`-contrat, soumetteur, livreur |
| [`10-export-c-vers-p.md`](10-export-c-vers-p.md) | `ExportTx` atomique C-Chain → P-Chain |
| [`11-import-p.md`](11-import-p.md) | `ImportTx` P-Chain |
| [`12-export-p-vers-c.md`](12-export-p-vers-c.md) | `ExportTx` P-Chain → C-Chain |
| [`13-import-c.md`](13-import-c.md) | `ImportTx` atomique C-Chain |
| [`20-add-validator.md`](20-add-validator.md) | `AddPermissionlessValidatorTx` |
| [`21-add-delegator.md`](21-add-delegator.md) | `AddPermissionlessDelegatorTx` |
| [`22-reward.md`](22-reward.md) | `RewardValidatorTx` et la distribution des récompenses |
| [`23-base-tx.md`](23-base-tx.md) | `BaseTx` |
| [`24-add-auto-renewed-validator.md`](24-add-auto-renewed-validator.md) | `AddAutoRenewedValidatorTx` |
| [`25-set-auto-renewed-config.md`](25-set-auto-renewed-config.md) | `SetAutoRenewedValidatorConfigTx`, la seule porte de sortie |
| [`30-autorisation.md`](30-autorisation.md) | Émission et vérification du message Warp d'autorisation |
| [`31-attestations.md`](31-attestations.md) | `TxExecuted`, `StakeSettled` et `CycleSettled` |

Un fichier absent de l'arborescence signifie que le mécanisme qu'il décrirait n'est pas implémenté.

## Conventions

- Les chemins sont relatifs à la racine du repo. Le volet C-Chain vit à `vms/saevm/cchain/…` ; `graft/coreth/…` est le chemin **pré-transition**, qui ne s'exécute plus une fois le type `warpfx` licite.
- Un symbole est écrit `paquet.Symbole` ou `fichier.go:Fonction` quand la désambiguïsation le demande.
- **Pas de numéros de ligne** : ils périment au premier ajout. Les symboles, eux, se retrouvent au `grep`.
- ⚠️ signale un piège : un comportement qui surprend à la lecture du code, ou une invariante silencieuse.

## Référence normative

L'ACP est [`../ACP-Long.md`](../ACP-Long.md). Quand ce dossier et l'ACP divergent, **ce dossier décrit le code** et l'ACP décrit l'intention : la divergence est un fait à remonter, pas une erreur à masquer.
