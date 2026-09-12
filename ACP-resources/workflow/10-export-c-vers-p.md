# `ExportTx` atomique C-Chain → P-Chain

## Ce qu'elle fait

Débite un solde AVAX de l'EVM et dépose un UTXO en shared memory, à destination de la P-Chain.

⚠️ **La C-Chain n'est pas servie par un VM fixe.** Elle est enregistrée comme un VM de transition dont la fabrique pré-transition est **coreth** et la fabrique post-transition **saevm**, la bascule ayant lieu dix secondes avant l'activation de l'upgrade cible (`node/node.go`, `TransitionTime`). **Le parcours décrit ici est celui de saevm.** Coreth n'est pas modifié : ne pas enregistrer `warpfx.TransferOutput` dans son codec atomique lui interdit structurellement de construire ou de parser un tel export, ce qui rend toute garde d'activation superflue de son côté.

⚠️ **Le refus de coreth est observé, pas seulement déduit.** Soumis à la C-Chain avant la bascule, un export portant une sortie `warpfx` est rejeté par le parsing même — `couldn't unmarshal interface: unknown type ID 44` — et les **mêmes octets** sont acceptés une fois saevm installé. C'est l'argument entier de l'absence de garde côté C-Chain, et `tests/e2e/p/warp_utxos.go` est le seul endroit où il est vérifié.

⚠️ **La bascule demande du trafic.** Le VM de transition ne bascule pas sur l'horloge : il bascule quand il **accepte un bloc** dont l'horodatage atteint le seuil. Une C-Chain inactive reste donc sur coreth indéfiniment, quelle que soit l'heure. Ça n'a aucune conséquence en production — la chaîne n'est jamais inactive — mais c'est déterminant pour tout test qui traverse la transition.

## Variantes d'acteur

Deux mécanismes distincts mènent de la C-Chain à la P-Chain, et ils ne se recouvrent pas.

| Acteur | Transaction atomique | Précompile `exportAVAX()` |
| :--- | :--- | :--- |
| **EOA secp** | Oui. Il signe avec la clé du compte débité | Oui. C'est une transaction EVM ordinaire |
| **Contrat** | **Non.** Pas de clé, et une transaction atomique n'est pas une transaction EVM : gossipée à part, incluse dans les données annexes du bloc, hors de toute frame d'appel | **Oui** |
| **Financer un tiers** | **Oui** — le destinataire est un champ | **Non** — le propriétaire est forcé à l'appelant |

Le précompile ne contourne pas le problème, il change de mécanisme : il s'exécute **dans** une transaction EVM, donc dans une frame où `msg.sender` n'est pas usurpable, et force le propriétaire de la sortie à l'appelant — exactement comme le précompile Warp force la `SourceAddress` d'un `AddressedCall`. Son parcours est décrit plus bas, section *Par le précompile*.

## Construction

`tx.Export{NetworkID, BlockchainID, DestinationChain, Ins []Input, ExportedOutputs []*avax.TransferableOutput}`.

- `Ins` : des `tx.Input{Address, Amount, AssetID, Nonce}`. Le `Nonce` doit égaler celui du compte au moment de l'exécution — c'est l'anti-rejeu.
- `ExportedOutputs` : des `avax.TransferableOutput` dont l'`Out` est un `secp256k1fx.TransferOutput` ou un `warpfx.TransferOutput`, les deux étant enregistrés dans le codec atomique. Le champ est typé par une interface.

⚠️ **Un EOA peut donc désigner un `WarpOwner` comme destinataire de son export, y compris une adresse tierce** — alimenter un `WarpOwner` ne demande aucune modification du consensus C-Chain, ce qui permet de financer un contrat sans que celui-ci ait à agir. L'UTXO ainsi déposé est ensuite consommé par une `ImportTx` P-Chain **canonique**, que n'importe qui soumet, sans autorisation ni message Warp.

- Le montant exporté est en nAVAX ; le débit EVM vaut `ScaleAVAX(Amount)`, soit 1 nAVAX = 1 gwei.
- Sorties **triées** (mais pas forcément uniques : chaque sortie donne un UTXO distinct, clé par `txID` et index) ; inputs triés et uniques.

## Autorisation

La signature secp256k1 du détenteur de chaque solde débité, un credential par input.

⚠️ **L'export ne passe pas par l'extension de fonctionnalité.** `Export.verifyCredentials` exige **exactement une** signature par input, récupère la clé publique par `sigCache.RecoverPublicKey(fxTx.Bytes(), cred.Sigs[0][:])` et compare `pk.EthAddress()` à `in.Address`. C'est un chemin secp en dur, sans dispatch — et il n'a rien à changer, un export `warpfx` restant signé par le détenteur du solde débité.

⚠️ **Il retourne toujours `canonical = false`** : la forme canonique n'existe qu'à l'import.

## Soumission

Par l'auteur, dans le mempool atomique de la C-Chain. Les transactions retenues sont assemblées par `builder.PotentialEndOfBlockOps` au moment de la construction du bloc.

## Vérification, dans l'ordre du code

⚠️ **Le modèle de vérification de saevm est « reconstruire et comparer ».** `VerifyBlock` rebâtit le bloc par le même chemin que le constructeur et compare les hachages : **toute règle du constructeur est une règle de consensus**.

`builder.PotentialEndOfBlockOps`, pour chaque transaction candidate :

1. **Conflit d'ascendance** — `ancestorInputIDs` écarte toute transaction dont les inputs recoupent ceux d'un ancêtre non encore réglé. Pour un export, l'identifiant d'input est `AccountInputID(address, nonce)`, qui ne peut pas entrer en collision avec un `UTXOID`.
2. **`tx.SanityCheck(ctx)`** → `Export.sanityCheck` :
   1. `NetworkID`, `BlockchainID`, puis `DestinationChain ∈ {P-Chain, X-Chain}` → `errNotSameSubnet`.
   2. `Ins` et `ExportedOutputs` non vides.
   3. Par input : montant non nul, **AVAX uniquement** → `errNonAVAXInput` ; `fc.Consume`.
   4. Par sortie : `out.Verify()`, **AVAX uniquement** → `errNonAVAXOutput` ; puis la **garde de destination** ci-dessous ; `fc.Produce`.
   5. `fc.Verify()` → `errFlowCheckFailed`.
   6. Inputs triés-uniques ; sorties triées.
3. **`tx.VerifyCredentials(ctx, sm)`** → `Export.verifyCredentials` : voir *Autorisation*. La shared memory n'est pas lue.
4. **`worstcase.State.Apply`**, côté SAE : refuse une opération dont `GasFeeCap` est sous le `baseFee` du bloc, et contrôle solde et nonce.

### La garde de destination

> Une sortie `warpfx` ne peut franchir la frontière que vers la P-Chain.

`errWarpOutputWrongDestination` si `DestinationChain != constants.PlatformChainID`. C'est le miroir exact de la règle P-Chain, et pour la même raison : le type n'est enregistré que dans ce codec et dans celui de la PlatformVM. Déposé en shared memory C↔X, l'UTXO serait parfaitement constructible et **définitivement indécodable** par la chaîne censée le consommer — et l'export débite le solde EVM avant que celle-ci n'ait rien à dire, donc l'échec serait silencieux et irréversible. Que seul le propriétaire puisse émettre une telle transaction ne rend pas la perte acceptable.

⚠️ Pour les sorties secp, la destination n'est contrainte que par l'appartenance au subnet : **la X-Chain reste une destination valide** depuis la C-Chain.

⚠️ **Aucun volet C-Chain ne porte de garde d'activation, et il n'en faut pas.** Chez saevm, le constructeur pose l'en-tête avant toute autre chose et refuse de le poser tant que l'upgrade n'est pas actif ; le chemin de reconstruction qui sert à vérifier un bloc passe par le même code, avec le `now` fixé à l'horodatage du bloc examiné. Le premier bloc saevm est donc nécessairement à ou après le timestamp d'activation. Chez coreth, qui construit encore les blocs pendant la fenêtre de dix secondes qui précède la bascule, le refus est **structurel** : `linearcodec.PackPrefix` refuse de marshaller un type non enregistré, et `warpfx.TransferOutput` ne l'est pas dans son codec atomique. Coreth ne peut donc ni construire ni parser un tel export.

## Effets d'état

`Tx.AsOp` traduit la transaction en une opération SAE :

| Champ | Valeur |
| :--- | :--- |
| `Gas` | `intrinsicGas + 1 × taille + CostPerSignature × nbSignatures` |
| `GasFeeCap` | `gasPrice(burned, gas)` — **le brûlage est une enchère**, pas un frais |
| `Burn` | par adresse, `AccountDebit{Amount: Σ ScaleAVAX(in.Amount), Nonce, MinBalance}` |

⚠️ **Un input non-AVAX incrémente quand même le nonce**, et deux nonces différents pour la même adresse dans une seule transaction sont refusés (`errMultipleNonces`).

- Solde EVM débité, nonce incrémenté une fois par adresse débitée.
- Un UTXO déposé en shared memory par sortie exportée.

## Ce qui est répercuté ailleurs

`atomicRequests(txID)` construit un `avax.UTXO{UTXOID{TxID: txID, OutputIndex: uint32(i)}, …}` par sortie exportée, le sérialise avec le codec atomique, et produit un `atomic.Element{Key: utxo.InputID(), Value: utxoBytes, Traits: out.Addresses()}` — **le trait n'étant renseigné que si `utxo.Out` implémente `avax.Addressable`**. Retourne `(DestinationChain, &atomic.Requests{PutRequests: elems})`.

⚠️ **Sans trait, l'UTXO est introuvable en shared memory.** C'est ce qui impose `warpfx.TransferOutput.Addresses()`, qui renvoie la `SourceAddress` du propriétaire — vingt octets bruts, donc la même forme de clé qu'une sortie secp, lisible par `avax.GetAtomicUTXOs` sans aucune adaptation.

⚠️ **L'UTXO est sérialisé par le codec atomique de la C-Chain et sera désérialisé par celui de la PlatformVM.** Les deux sont des `linearcodec` : le `typeID` d'un type est **sa position d'enregistrement**. Une divergence produirait un UTXO illisible par la chaîne cible, **silencieusement du point de vue de l'export, qui a déjà débité le solde EVM**.

C'est la raison d'être des `SkipRegistrations`. `warpfx.TransferOutput` doit tomber sur **44** des deux côtés, et `TestCodecAlignedWithPlatformVM` sérialise le même UTXO avec les deux codecs puis compare les octets — pour un UTXO `warpfx` **et** pour un `secp256k1fx`.

⚠️ **Le décalage à écrire dépend du VM, et un seul est écrit.** Le codec atomique de saevm s'arrête à 9 et demande donc `SkipRegistrations(34)` ; celui de coreth s'arrête à 11 et demanderait `SkipRegistrations(32)`, car il enregistre `secp256k1fx.Input` et `OutputOwners`. **Un test d'alignement écrit contre l'un ne dit rien de l'autre** — mais coreth n'est pas modifié, et son absence d'enregistrement est précisément ce qui le dispense de garde d'activation.

## Cas d'échec

| Échec | Ce qu'il laisse |
| :--- | :--- |
| `sanityCheck` ou flow check | Rien : la transaction n'entre pas dans un bloc |
| Nonce erroné, solde insuffisant | Rien ; refusé par l'état pire-cas |
| Enchère sous le `baseFee` | Rien ; la transaction attend que le prix descende |
| Destination indécodable par la chaîne cible | **Solde EVM débité, UTXO déposé, fonds irrécupérables.** C'est précisément ce que la garde de destination prévient |
| Export d'un `warpfx.TransferOutput` vers la X-Chain | `errWarpOutputWrongDestination` avant tout débit. Rien n'est perdu |
| Export d'un `warpfx.TransferOutput` sous coreth, avant la bascule de VM | Impossible à construire : le codec atomique de coreth ne connaît pas le type et refuse de le marshaller |

## Par le précompile

`INativeExport.exportAVAX() payable`, à `0x0200000000000000000000000000000000000006`, activé à Helicon.

**Un seul point d'entrée, sans paramètre.** Il débite `msg.value`, enregistre le dépôt, et s'arrête là : il n'a ni montant d'autre UTXO à connaître, ni frais P-Chain à fixer, ni import à encoder, ni message Warp à émettre. L'`ImportTx` étant canonique, n'importe qui la reconstruit depuis ce qu'il lit en shared memory.

| Champ de l'UTXO | Source |
| :--- | :--- |
| `DestinationChain` | `constants.PlatformChainID`, **forcé** |
| `Owner.SourceChainID` | `ctx.ChainID` |
| `Owner.SourceAddress` | `caller`, **forcé, non usurpable** |
| `AssetID` | `ctx.AVAXAssetID`, **forcé** |
| `Amt` | `msg.value / X2CRate` |
| `UTXOID.TxID` | le hachage de la transaction **EVM** |
| `UTXOID.OutputIndex` | un compteur en stockage, remis à zéro à chaque transaction |

⚠️ **Un `msg.value` non multiple de `X2CRate` est refusé, jamais tronqué.** Une troncature perdrait jusqu'à 1 nAVAX par appel — dérisoire, mais une perte silencieuse sur un chemin de transfert de valeur est ce qu'on ne veut pas avoir à expliquer.

⚠️ **Le débit est indispensable.** `payable` déplace la valeur vers l'adresse du précompile, avec la vérification de solde et le `revert` propre de l'EVM ; elle doit ensuite **quitter le bilan de l'EVM**, puisqu'elle existe désormais en UTXO sur la P-Chain. L'y laisser créerait de l'AVAX des deux côtés.

⚠️ **La shared memory est dérivée des logs, et d'eux seuls.** Le précompile ne dépose rien lui-même : il émet un log, et `nativeexport.FromReceipts` en dérive les opérations dans `AfterExecutingBlock`. `AddLog` est journalisé et le `revert` d'une frame efface ses logs, donc une transaction en échec — incluse au bloc, gaz consommé — ne dépose rien. Tout canal latéral au journal laisserait au contraire fuiter un dépôt depuis un appel annulé.

⚠️ **L'espace de nommage des `UTXOID` est partagé.** Le `TxID` étant le hachage d'une transaction EVM, une collision avec un `txID` de transaction atomique est cryptographiquement négligeable — mais le partage mérite d'être su. Le préfixe de 28 octets du compteur est une **heuristique de remise à zéro**, pas ce qui fait l'unicité : celle-ci vient du hachage entier.

⚠️ **saevm n'applique aucune activation de précompile.** `core.ApplyUpgrades` n'existe que dans le processeur de coreth. Le précompile Warp survit parce que Durango l'a activé bien avant la bascule et que son code arrive dans l'état hérité ; tout précompile programmé à Helicon ou après doit être activé par `hooks.StartExecutingBlock`, au premier bloc de l'upgrade, comme Warp l'est à Durango. Sans ça, l'appelant rencontre un `revert` nu depuis le `extcodesize` de Solidity, sans rien à quoi se raccrocher.

**L'index d'une opération sans transaction.** `State.Apply` reçoit ces requêtes en plus des transactions et les fusionne avant d'écrire le trie. Elle indexe aussi les transactions par ID ; une opération sans transaction n'a rien à y enregistrer. Cet index sert l'API et le reprocessing, **pas le consensus** : son absence est un trou d'API, pas une divergence.

## Symboles concernés

- `vms/saevm/cchain/tx/export.go` — `Export`, `Input`, `AccountInputID`, `sanityCheck`, `errWarpOutputWrongDestination`, `verifyCredentials`, `burned`, `asOp`, `atomicRequests`
- `vms/saevm/cchain/tx/tx.go` — `Tx.SanityCheck`, `Tx.VerifyCredentials`, `Tx.AsOp`, `gasUsed`, `ScaleAVAX`
- `vms/saevm/cchain/tx/codec.go` — `SkipRegistrations(34)` puis `warpfx.TransferOutput` → **44**
- `vms/saevm/cchain/tx/warp_codec_test.go` — `TestCodecAlignedWithPlatformVM`
- `vms/saevm/cchain/tx/warp_canonical_test.go` — `TestWarpExportDestination`, `TestCanonicalImport`, `TestNonCanonicalImportRefusesWarpUTXOs`
- `tests/e2e/p/warp_utxos.go` — le parcours C↔P complet, seul endroit où le refus de coreth est observé
- `tests/e2e/p/warp_utxos_contract.go`, `warp_owner.sol` — le même parcours avec un contrat pour propriétaire
- `tests/e2e/p/warp_utxos_mixed.go` — un export atomique unique alimentant **deux** propriétaires
- `vms/saevm/cchain/hooks.go` — `builder.PotentialEndOfBlockOps`, `ancestorInputIDs`
- `vms/saevm/sae/blocks.go` — `VerifyBlock` → `rebuild` → comparaison de hachages
- `vms/warpfx/transfer_output.go` — `Addresses()`, qui rend l'UTXO découvrable
- `vms/saevm/cchain/precompile/nativeexport/` — `contract.go` (`exportAVAX`, `toNAVAX`, `nextOutputIndex`), `receipts.go` (`FromReceipts`), `module.go`, `config.go`
- `vms/saevm/cchain/hooks.go` — `StartExecutingBlock`, qui active le précompile au premier bloc Helicon ; `AfterExecutingBlock`, qui dérive les dépôts
- `vms/saevm/cchain/state/state.go` — `Apply`, élargie aux requêtes hors transaction, et `mergeRequests`
- `tests/e2e/p/warp_export_precompile.go` — l'aller-retour par le précompile, pour un EOA **et** pour un contrat
- `graft/coreth/plugin/evm/atomic/` — chemin pré-transition, **non modifié** : c'est l'absence d'enregistrement dans `codec.go` qui interdit structurellement l'export
