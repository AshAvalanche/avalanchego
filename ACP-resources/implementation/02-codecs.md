# Lot 2 — Enregistrement dans les codecs de la PlatformVM

[← Lot 1](01-warpfx-types.md) · [Plan](README.md) · [Lot suivant : l'aiguillage →](03-aiguillage-fx.md)

---

## Pourquoi ce lot existe

Un `linearcodec` n'a pas de noms de types : il a des **positions**. Quand il
sérialise un champ déclaré par une interface — par exemple
`avax.TransferableOutput.Out`, qui est un `avax.TransferableOut` — il écrit
devant la valeur un `uint32` qui est *le rang auquel ce type a été enregistré*.
Au décodage, il lit ce rang et retrouve le type concret.

Il en découle une propriété que rien dans le langage ne protège :
**deux codecs qui enregistrent le même type à des rangs différents produisent
des octets mutuellement illisibles.** Et comme un UTXO déposé en shared memory
est sérialisé par le codec de la chaîne d'origine et désérialisé par celui de la
chaîne de destination, un désalignement se traduit par un UTXO qu'on peut créer
mais jamais lire — **silencieusement du point de vue de l'export, qui aura déjà
débité**.

C'est la raison d'être des `SkipRegistrations` qui parsèment ces fichiers. Ils
ne « sautent » rien d'utile : ils **réservent** des positions, pour que les types
partagés tombent au même rang de chaque côté. Le codec de la PlatformVM fait foi,
et les autres s'alignent sur lui.

Ce lot fait donc deux choses. Il attribue aux trois types `warpfx` leurs
positions — **43, 44, 45** — et il s'assure qu'elles sont les mêmes dans le codec
des **transactions** et dans celui des **blocs**, qui sont deux flux linéaires
distincts entrelacés l'un dans l'autre. Le lot 10 fera le troisième alignement,
celui de saevm.

L'enregistrement est **inconditionnel** : il n'est pas gaté par l'upgrade. C'est
la règle d'avalanchego depuis toujours — le gating se fait à la vérification,
jamais au décodage, sinon un nœud ne pourrait pas seulement *lire* un bloc qu'il
doit rejeter. Le lot 6 pose la garde qui va avec.

---

## 2.1 Recompter les positions soi-même

Avant d'écrire quoi que ce soit, refais le décompte à la main. C'est cinq
minutes, et c'est la seule façon d'être sûr que 43/44/45 est toujours juste sur
ta branche.

**Où** : `vms/platformvm/platform/codec.go`, fonction `init()` puis les cinq
`RegisterXxxTypes`.

**Le décompte** :

| Bloc | Positions | Source |
| :--- | :--- | :--- |
| `SkipRegistrations(5)` | 0 → 4 | `codec.go:39` — réservées aux blocs Apricot |
| `RegisterApricotTypes` | 5 → 22 | `codec.go:68-97` |
| `RegisterBanffTypes` | 23 → 28 | `codec.go:101-111` |
| `SkipRegistrations(4)` | 29 → 32 | `codec.go:46` — réservées aux blocs Banff |
| `RegisterDurangoTypes` | 33 → 34 | `codec.go:115-120` |
| `RegisterEtnaTypes` | 35 → 39 | `codec.go:124-132` |
| `RegisterHeliconTypes` | 40 → 42 | `codec.go:136-142` |
| **libre** | **43** | |

Le détail d'Apricot, où se trouvent les deux `SkipRegistrations(1)` qui étonnent
au premier coup d'œil : `TransferInput`→5, *skip*→6 (`MintOutput`, réservé pour
l'AVM), `TransferOutput`→7, *skip*→8 (`MintOperation`, idem), `Credential`→9,
`Input`→10, `OutputOwners`→11, puis les neuf transactions Apricot 12→20, puis
`stakeable.LockIn`→21 et `LockOut`→22.

→ **`Owner` = 43, `TransferOutput` = 44, `Credential` = 45.**

**Pourquoi trois types et pas seulement la sortie.** `Owner` doit être enregistré
parce qu'il sert de `RewardsOwner` : le champ `ValidatorRewardsOwner` d'un
`AddPermissionlessValidatorTx` est typé `fx.Owner`, donc une interface, donc le
codec y écrit un identifiant de type. `Credential` doit l'être parce qu'il vit
dans `Creds []verify.Verifiable`, également une interface.

---

## 2.2 Une fonction d'enregistrement, appelée une fois

Blocs et transactions partagent **un seul flux linéaire**, dans lequel les types
de blocs occupent les trous que les types de transaction réservent. Ils sont
enregistrés ensemble dans `vms/platformvm/platform/codec.go`, si bien que
l'alignement n'a plus à être maintenu : il n'y a plus qu'un côté.

**Où** : `vms/platformvm/platform/codec.go`.

**Quoi** :

```go
func RegisterWarpUTXOsTypes(targetCodec linearcodec.Codec) error {
    return errors.Join(
        targetCodec.RegisterType(&warpfx.Owner{}),
        targetCodec.RegisterType(&warpfx.TransferOutput{}),
        targetCodec.RegisterType(&warpfx.Credential{}),
    )
}
```

Appelée dans `init()` **après** `registerHeliconTxTypes(c)`, dans la même boucle
`for _, c := range []linearcodec.Codec{c, gc}`.

**Pourquoi une fonction plutôt que trois lignes dans les types Helicon.**
Les deux marchent. La fonction séparée dit ce qu'elle fait : ces trois types
n'appartiennent pas à Helicon en tant qu'upgrade, ils appartiennent à cette
proposition. Si la proposition glisse à un upgrade ultérieur, la fonction se
déplace sans qu'on ait à démêler quoi que ce soit.

> **Ce que la fusion des paquets a supprimé comme risque.**
> Quand blocs et transactions avaient chacun leur fichier, enregistrer d'un seul
> côté donnait un nœud qui acceptait une transaction au mempool puis n'arrivait
> pas à parser le bloc où elle atterrissait — un symptôme qui ne ressemblait pas
> à sa cause. Un test d'alignement le rendait impossible à livrer ; il n'a plus
> d'objet, puisqu'il n'y a plus deux côtés à désaligner.

---

## 2.3 Les tests, qui sont la seule garde

Aucune signature de fonction ne protège l'invariant des positions. Un
`RegisterType` ajouté ailleurs dans le fichier décale tout ce qui suit, sans
erreur de compilation ni test rouge. Ces tests sont donc le mécanisme, pas une
formalité.

**Où** : `vms/platformvm/platform/codec_test.go`.

**Quoi** :

- `TestWarpUTXOsCodecTypeIDs` — épingle **43**, **44**, **45** en dur. Pour
  obtenir le `typeID` d'un type depuis un `codec.Manager`, sérialise-le dans un
  champ d'interface et lis les octets 2 à 5 (après la version sur 2 octets), ou
  passe par le `linearcodec` directement si tu le gardes accessible.

**Vérifier** :

```bash
go test ./vms/platformvm/txs/   -run Codec
go test ./vms/platformvm/block/ -run Codec
```

> ⚠️ **Ce test cassera un jour, et ce sera une bonne nouvelle.**
> Le jour où quelqu'un ajoute un type de transaction à la PlatformVM avant que
> cette proposition ne soit activée, 43/44/45 devient 44/45/46 et le test rouge
> t'oblige à mettre à jour **les deux** valeurs pinées **et** le
> `SkipRegistrations` de saevm (lot 10a). Sans le test, la même situation
> produirait un UTXO indécodable par la chaîne cible, en production.

---

## Ce qui reste incertain

✅ **Vérifié**

- **Les positions 43/44/45** — le décompte de la section 2.1 est refait à la main
  *puis* exécuté. `TestWarpUTXOsCodecTypeIDs` les épingle sur `Codec` **et** sur
  `GenesisCodec`.
- **La lecture d'un `typeID`** passe par la première des deux options :
  sérialiser dans un champ d'interface et lire les octets 2 à 6. Elle n'exige
  rien d'exporté, contrairement à la seconde.
- **Le test d'alignement bloc ↔ transaction mord** : en retirant l'appel du côté
  blocs, il échoue sur `can't marshal unregistered type "*warpfx.TransferOutput"`.
  Le mode de défaillance que ce lot décrit — « la transaction se décode, le bloc
  non » — est donc réellement couvert.

⚠️ **Le nom du test compte.** Il s'appelle `TestWarpUTXOsCodecTypeIDs` et non
`TestWarpUTXOsTypeIDs`, parce que la commande de vérification de ce lot est
`go test ./vms/platformvm/txs/ -run Codec` : sous l'ancien nom elle ne
correspondait à rien **et rendait un succès**. Un filtre qui ne sélectionne rien
ne dit rien.

🔴 **À trancher avant d'écrire**

- Rien dans ce lot. Les positions découlent du décompte, elles ne se décident
  pas.

🟡 **À mesurer**

- Rien dans ce lot.

---

## Ce que le portage sur master a trouvé

**Les positions ont survécu à une fusion de paquets.** Master a vidé
`vms/platformvm/txs/` et `vms/platformvm/block/` dans un `vms/platformvm/platform/`
unique, réunissant les deux séquences d'enregistrement en une seule. Les trois
types tombent toujours sur **43, 44 et 45** : la séquence fusionnée occupe
exactement les trous que l'ancienne réservait, ce qui était précisément la
prémisse du raisonnement de la 2.2.

C'est `TestWarpUTXOsCodecTypeIDs` qui l'a établi, sans une ligne de modification.
Un invariant qu'aucun compilateur ne protège a traversé un refactor de 257
fichiers, et on l'a su en une seconde.

**Un test est devenu sans objet.** `TestWarpUTXOsCodecAlignedWithTxs` comparait
le codec des blocs à celui des transactions. Ce qu'il empêchait — enregistrer
d'un seul côté — ne peut plus se produire, puisqu'il n'y a plus deux côtés. Il
est supprimé plutôt que réécrit : un test qui ne peut plus échouer est du bruit.
