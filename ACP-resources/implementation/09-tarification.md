# Lot 9 — Tarification (P-Chain)

[← Lot 8](08-decouverte.md) · [Plan](README.md) · [Lot suivant : le volet saevm →](10-saevm.md)

---

## Pourquoi ce lot existe

Deux problèmes distincts, qui n'ont en commun que d'échouer tous les deux **au
calcul de frais** plutôt qu'à la vérification — ce qui égare le diagnostic.

**Le premier est mécanique.** Le calculateur de complexité de la PlatformVM
dispatche par type Go, avec des assertions dures : `outputComplexity` type-asserte
`*secp256k1fx.TransferOutput` et rend `errUnsupportedOutput` sinon
(`complexity.go:305-308`) ; `OwnerComplexity` fait pareil avec
`*secp256k1fx.OutputOwners`. Un type de sortie ajouté sans y être tarifé ne se
fait pas rejeter par le vérifieur — il fait **échouer le calcul du frais**, donc
la transaction, avec un message qui parle de complexité et pas de type inconnu.
C'est une asymétrie utile à connaître : introduire un type de sortie demande de le
tarifer explicitement côté PlatformVM, et **rien** côté C-Chain, dont le gas se
compte en octets et en signatures sans connaître les types.

**Le second est structurel, et c'est le vrai sujet du lot.** La complexité d'une
transaction est calculée sur `tx.Unsigned` — `Calculator.CalculateFee(tx platform.UnsignedTx)`.
Les messages Warp que l'ACP-77 tarifie sont des **champs** de la transaction non
signée, donc visibles. Un credential `warpfx` ne peut pas l'être : il contient
précisément les octets non signés. Il est donc **invisible au calculateur**.

Laissé tel quel, cela veut dire que la vérification BLS agrégée d'un ensemble de
plus de 1 000 signataires — construction du set canonique, agrégation, pairing —
serait **gratuite**. C'est le genre de trou qu'on ne remarque pas parce qu'il ne
casse rien : les transactions passent, les frais sont juste trop bas.

Fermer ce point demande d'élargir l'interface de calcul de frais pour qu'elle voie
la transaction **signée**. Et cet élargissement a une conséquence qu'on oublie
facilement : il ne suffit pas de corriger le frais, il faut aussi corriger la
**capacité de bloc**, calculée aux mêmes endroits à partir de la même complexité.

---

## 9.1 Tarifer les types `warpfx`

Deux points de dispatch à ouvrir, tous les deux dans le même fichier. C'est la
partie mécanique du lot.

**Où** : `vms/platformvm/txs/fee/complexity.go`.

**Quoi** :

1. Deux constantes, à côté des `intrinsicSECP256k1Fx*` existantes :

   ```go
   intrinsicWarpFxOwnerBandwidth  = ids.IDLen + wrappers.IntLen + warpfx.AddressLen
   intrinsicWarpFxOutputBandwidth = wrappers.LongLen + intrinsicWarpFxOwnerBandwidth
   ```

   (`ids.IDLen` pour le `SourceChainID`, `wrappers.IntLen` pour le préfixe de
   longueur du `[]byte`, `warpfx.AddressLen` pour l'adresse ; plus
   `wrappers.LongLen` pour l'`Amt`.)

2. `outputComplexity` (l.293) : sortir `intrinsicSECP256k1FxOutputBandwidth` du
   littéral initial, et remplacer l'assertion dure par un
   `switch typedOut := outIntf.(type)` à trois branches — `*secp256k1fx.TransferOutput`
   (comportement actuel, adresses comprises), `*warpfx.TransferOutput`
   (`intrinsicWarpFxOutputBandwidth`, pas d'adresses variables), `default`
   (`errUnsupportedOutput`, inchangé).

3. `OwnerComplexity` (l.421) : court-circuiter `*warpfx.Owner` **avant**
   l'assertion secp.

> ⚠️ **Ce point 3 ouvre une porte qu'il faut refermer dans le même lot.**
> `OwnerComplexity` n'est pas appelée que pour les propriétaires de récompenses.
> Elle l'est aussi pour `CreateSubnetTx.Owner`, typé `fx.Owner`
> (`create_subnet_tx.go:18`), pour `TransferSubnetOwnershipTx`, et pour
> **`AddAutoRenewedValidatorTx.ValidatorAuthority`** (`complexity.go:842`).
> Aujourd'hui un `*warpfx.Owner` y ferait échouer le **calcul de frais** — donc
> la transaction — sur `errUnsupportedOwner`. Une fois le cas ajouté, la
> transaction devient tarifable, donc **acceptable**.
>
> Or `warpfx.Fx.VerifyPermission` rend toujours `ErrPermissionUnsupported`
> (lot 1.7). Un subnet dont le propriétaire est un `WarpOwner` serait donc
> **définitivement ingérable** : plus aucune `CreateChainTx`, plus aucun transfert
> de propriété. C'est de l'auto-mutilation, mais le dépôt s'en protège ailleurs —
> `secp256k1fx.OutputOwners.Verify()` refuse un propriétaire indépensable sur
> `ErrOutputUnspendable`.
>
> **Tranché : on refuse explicitement.** Dans `CreateSubnetTx.SyntacticVerify` et
> `TransferSubnetOwnershipTx.SyntacticVerify`, rejeter un propriétaire qui n'est
> pas un `*secp256k1fx.OutputOwners` (`ErrWarpOwnerCannotOwnSubnet`). Cela
> restaure explicitement l'invariant « l'autorisation de subnet est secp-only par
> construction » sur lequel le lot 3 s'appuie pour garder le Fx par défaut à
> côté de l'aiguillage.
>
> Ne **pas** se reposer sur le calcul de frais comme garde de fait : elle
> disparaîtrait au premier autre type d'`fx.Owner` ajouté, et le lien entre
> « j'ai ajouté un cas à `OwnerComplexity` » et « un subnet peut devenir
> ingérable » n'est visible de nulle part.
>
> ⚠️ La règle est **syntaxique** et donc soumise à la garde d'activation du
> lot 6.2 : avant l'upgrade, une telle transaction est de toute façon refusée
> parce qu'elle *mentionne* un type `warpfx`. La règle vaut pour après.

> ⚠️ **`AddAutoRenewedValidatorTx.ValidatorAuthority` est le cas symétrique, et
> il se termine dans l'autre sens.**
> `OwnerComplexity` y est appelée aussi (`complexity.go:842`), donc ce lot rend
> également ce montage tarifable. Mais celui-là est **dans le périmètre** : un
> `*warpfx.Owner` y est légitime, parce que `warpfx` implémente
> `VerifyPermissionWithContext` (lot 1.7) et que `SetAutoRenewedValidatorConfigTx`
> est dans la liste autorisée (lot 4d). C'est même le seul moyen pour un contrat
> de récupérer son stake, via `Period = 0`.
>
> ⚠️ **Ne généralise donc pas le refus du subnet en « `warpfx.Owner` n'est jamais
> un propriétaire de permission ».** La différence entre les deux n'est pas le
> type du propriétaire, c'est le **point d'entrée** que l'appelant emprunte :
> `verifySubnetAuthorization` passe sans contexte et se fait refuser,
> `SetAutoRenewedValidatorConfigTx` passe avec et est servi. Le refus du subnet
> reste structurel ; la règle syntaxique du paragraphe précédent ne fait que le
> rendre lisible plus tôt.

**Ce qu'il n'y a pas à faire** : `inputComplexity` ne change pas. Le lot 1 ne crée
aucun type d'input — on réutilise `secp256k1fx.TransferInput` à `SigIndices`
vide, qui est déjà tarifé et coûte alors zéro signature.

**Vérifier** : `go test ./vms/platformvm/txs/fee/...`

---

## 9.2 Tarifer le credential porteur

C'est le cœur du lot. Deux fichiers, une nouvelle méthode sur l'interface
`Calculator`, et cinq points d'appel.

**Où** : `vms/platformvm/txs/fee/credential_complexity.go` *(nouveau)*, plus
`calculator.go`, `dynamic_calculator.go`, `simple_calculator.go`.

**Quoi** :

```go
// credential_complexity.go
func CredentialComplexity(creds []verify.Verifiable) (gas.Dimensions, error)
func SignedTxComplexity(tx *platform.Tx) (gas.Dimensions, error)
```

`CredentialComplexity` balaie `creds`, trouve l'unique `*warpfx.Credential` à
`WarpMessage` non vide, et lui applique `WarpComplexity(msg)` — la fonction
existante (`complexity.go:492`), qui compte : bande passante = `len(message)`,
`DBRead` = `intrinsicWarpDBReads` (forfait constant), compute =
`numSigners × intrinsicBLSAggregateCompute + intrinsicBLSVerifyCompute`. Les
credentials secp ne sont **pas** tarifés ici : ils le sont déjà via les
`SigIndices` des inputs, et les compter deux fois serait une régression.

`SignedTxComplexity(tx)` = `TxComplexity(tx.Unsigned)` + `CredentialComplexity(tx.Creds)`.

L'interface gagne :

```go
// calculator.go
type Calculator interface {
    CalculateFee(tx platform.UnsignedTx) (uint64, error)
    CalculateFeeWithCredentials(tx *platform.Tx) (uint64, error)
}
```

Le calculateur simple ignore les credentials (frais fixe) ; le dynamique passe
par `SignedTxComplexity`.

Les sept transactions de la liste (lot 4d) appellent
`CalculateFeeWithCredentials(e.tx)` au lieu de `CalculateFee(tx)`.

**Pourquoi une seconde méthode plutôt que changer la signature de la première.**
`CalculateFee(platform.UnsignedTx)` a des appelants qui n'ont légitimement pas la
transaction signée sous la main (construction, estimation côté wallet). Les forcer
à en fabriquer une serait pire que la duplication.

**Pourquoi la complexité d'un message Warp convient telle quelle.** Le coût réel
est exactement celui que l'ACP-77 tarifie déjà : bande passante linéaire en la
taille du message, forfait de lectures d'état pour le set canonique, calcul
linéaire en nombre de signataires. Il n'y a pas de nouvelle grandeur à inventer.

**Pourquoi elle est comptée une seule fois.** Un seul credential porte le message
(lot 4c). C'est aussi la raison pour laquelle le message ne doit pas être répété
dans chaque slot : sinon N copies seraient diffusées et stockées **pour le prix
d'une**, la complexité n'étant comptée qu'une fois.

**Vérifier** : test 9 du lot 12 — deux autorisations différant par le nombre de
signataires donnent deux frais différents.

---

## 9.3 La capacité de bloc, qu'on oublie

C'est la moitié du lot qui ne ressemble pas à de la tarification, et c'est celle
dont l'omission se voit le plus tard.

**Où** : trois fichiers, tous appelants de `TxComplexity(tx.Unsigned)` :
`vms/platformvm/block/executor/verifier.go`,
`vms/platformvm/block/executor/manager.go` (admission au mempool),
`vms/platformvm/block/builder/builder.go`.

**Quoi** : y remplacer `TxComplexity(tx.Unsigned)` par `SignedTxComplexity(tx)`.

> ⚠️ **Sans ces trois lignes, le frais serait juste et la capacité fausse.**
> Ces trois endroits rationnent l'espace de bloc à partir de la complexité. Si la
> complexité qu'ils voient exclut le credential, un bloc peut accueillir plus de
> transactions autorisées que sa cible de gas ne le permet — et le message
> contenant `txBytes`, l'écart n'est pas marginal. Le symptôme serait un bloc plus
> lourd que prévu, pas une transaction rejetée : personne ne le relie au
> credential.

**Vérifier** : un test qui construit un bloc plein de transactions autorisées et
compare le gas consommé à la cible.

---

## Ce qui reste incertain

✅ **Vérifié**

- **Les constantes de bande passante** sont contrôlées contre une sérialisation
  réelle, pas contre l'arithmétique qui les a produites
  (`TestWarpFxBandwidthMatchesSerialization`) : **100 octets** pour un
  `TransferableOutput{warpfx.TransferOutput}` et **56** pour l'`Owner` embarqué.
  Le décompte du lot 1.2 était juste.
- **Les sites de rationnement sont quatre, pas trois.** Aux trois annoncés
  s'ajoute `vms/platformvm/txs/mempool/mempool.go`, qui métrologie le gas à
  l'admission et à l'éviction depuis la même complexité. Même famille de bug,
  donc migré aussi.
- **Les appelants de `CalculateFee` à migrer** sont les six sites correspondant
  aux sept transactions (`verifySpend` en sert deux). Les autres restent sur la
  forme historique, et c'est correct : le lot 4d garantit qu'elles ne peuvent pas
  porter de credential porteur.

⚠️ **`verifySpend` prend désormais la transaction signée *et* les credentials
séparément, et la distinction n'est pas cosmétique.** Elle recevait
`(tx platform.UnsignedTx, creds)` — or pour `SetAutoRenewedValidatorConfigTx` les
`creds` passés sont `baseTxCreds`, la liste **amputée** du credential
d'autorisation. Tarifer sur cette liste laisserait passer **gratuitement** un
message logé dans le dernier slot, qui est précisément celui que
`verifyAuthorization` consomme. Le frais se calcule donc sur `sTx.Creds`, la
liste entière, et le flow check sur `creds`, la liste autorisée.

⚠️ **Un test existant a changé**, et c'est la règle qui fonctionne plutôt qu'une
assertion assouplie : le cas de succès de `TestTransferSubnetOwnershipTxSyntacticVerify`
utilisait un `fxmock.Owner`, que le refus du 9.1 rejette. Il utilise désormais un
vrai propriétaire secp, et un cas « warp owner » a été ajouté à côté.

🟡 **À mesurer**

- **Le forfait `intrinsicWarpDBReads` à l'échelle du Primary Network.** Il est
  constant, et l'ACP le juge adéquat parce que le set canonique est servi depuis
  la mémoire. C'est **à confirmer au bench avec le coût à froid** pour un set de
  plus de 1 000 validateurs — c'est le cas C-Chain, donc le cas nominal de cette
  proposition, pas un cas limite.
- **La latence de construction du set canonique** à cette échelle
  (`GetCanonicalValidatorSetFromChainID`). Elle conditionne la partie `DBRead` du
  forfait ci-dessus.

🔴 **À trancher avant d'écrire**

- Rien dans ce lot. Le modèle de coût ne se décide pas : la complexité à
  appliquer est celle d'un message Warp ACP-77, il n'y a rien de nouveau à
  inventer. Et le refus du `warpfx.Owner` comme propriétaire de subnet (9.1,
  point 3) est tranché.
