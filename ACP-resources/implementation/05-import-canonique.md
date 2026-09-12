# Lot 5 — La forme canonique de l'`ImportTx` P-Chain

[← Lot 4](04-autorisation.md) · [Plan](README.md) · [Lot suivant : les gardes →](06-gardes.md)

---

## Pourquoi ce lot existe

Le lot 4 a construit un chemin complet : un contrat émet un message Warp,
quelqu'un le relaie, la P-Chain vérifie et exécute. C'est lourd — agrégation BLS,
relayeur à l'écoute des logs, `expiry` à dimensionner. Et pour la moitié des
opérations, c'est inutile.

Le critère qui décide n'est pas la direction du transfert mais **ce que
l'opération fait de la valeur** :

> Une autorisation est nécessaire pour **dépenser**. Elle ne l'est jamais pour
> **recevoir**.

Un transfert C↔P se décompose toujours en deux segments, et un seul dépense :

| Segment | Ce qu'il fait | Autorisation |
| :--- | :--- | :--- |
| `Export` C-Chain | débite un solde EVM | l'EVM : signature de l'EOA, ou `msg.sender` |
| `ImportTx` P-Chain | matérialise un UTXO **déjà** détenu par le propriétaire | **aucune** |
| `ExportTx` P-Chain | consomme les UTXOs du propriétaire | message Warp |
| `Import` C-Chain | crédite un solde EVM **déjà** dû | **aucune** |

Les deux imports sont à valeur neutre : ils consomment un UTXO qui porte déjà le
nom de son propriétaire et le lui restituent. Il n'y a rien à autoriser — **il
n'y a qu'à interdire au transporteur d'en dévier quoi que ce soit**.

D'où la notion de **transaction canonique** : une transaction dont la forme est
*dérivée* des UTXOs qu'elle consomme, et non *rédigée* par un auteur. Personne ne
la signe, personne n'émet de message pour elle, et n'importe qui la reconstruit à
partir de ce qu'il lit en shared memory. Alimenter un `WarpOwner` devient
entièrement gratuit en autorisations : un EOA signe un export atomique désignant
le `WarpOwner` comme destinataire — ce qui ne demande aucune modification du
consensus C-Chain (lot 10b) — puis n'importe qui soumet l'import canonique.
Aucun message Warp, aucune agrégation BLS, aucun relayeur, aucune `expiry`.

Ce lot écrit les contraintes qui remplacent la signature. Il en faut trois, et
une quatrième qui n'a l'air de rien mais qui est indispensable : une **borne
inférieure sur ce qui doit être restitué**.

---

## 5.1 Reconnaître la branche canonique

Trois conditions, **ensemble**. C'est leur conjonction qui autorise le
court-circuit du Fx, et le prochain encadré explique pourquoi cette précision
n'est pas de la pédanterie.

**Où** : `vms/platformvm/txs/executor/canonical.go` *(nouveau)*, appelé depuis
`standard_tx_executor.go` dans `ImportTx`.

**Quoi** — `isCanonicalImport(tx *platform.ImportTx, creds []verify.Verifiable, utxos []*avax.UTXO) bool` :

1. aucun credential porteur (`fxCtx.Authorization == nil`) ;
2. `len(tx.Ins) == 0` — elle ne consomme aucun UTXO résidant déjà sur la P-Chain ;
3. tous les UTXOs importés sont des `*warpfx.TransferOutput`.

**Pourquoi `Ins` doit être vide.** C'est ce qui rend la forme *canonique* : le
contenu est entièrement déterminé par les UTXOs consommés en shared memory. Si on
autorisait des `Ins` P-Chain, la forme cesserait d'être dérivable de ce que le
soumetteur lit, et il faudrait ré-inventer des règles pour encadrer ce qu'il
ajoute.

**Vérifier** : les cas limites — `Ins` non vide, mélange `warpfx`/secp dans
`ImportedInputs`, credential porteur présent — doivent tous rendre `false`.

---

## 5.2 Les contraintes de forme

Une fois la branche reconnue, tout se vérifie **une seule fois pour la
transaction**, après la boucle par input plutôt que dedans.

**Où** : même fichier, `verifyCanonicalImport(...)`.

**Quoi** — dans cet ordre, avec une erreur nommée par condition :

| Condition | Erreur |
| :--- | :--- |
| chaque slot de credential face à un input `warpfx` porte un `warpfx.Credential{}` **vide** | `ErrCanonicalImportCredential` |
| tous les UTXOs appartiennent au **même** `WarpOwner` | `ErrCanonicalImportMixedOwners` |
| tous les assets sont l'AVAX | `ErrCanonicalImportAsset` |
| chaque input est un `secp256k1fx.TransferInput` à `SigIndices` **vide** | `ErrCanonicalImportInput` |
| `in.In.Amount() == utxo.Out.Amt` pour chaque input | `ErrCanonicalImportAmount` |
| `len(tx.Outs) == 1` | `ErrCanonicalImportOutputCount` |
| cette sortie est un `warpfx.TransferOutput` au **même** propriétaire | `ErrCanonicalImportWrongOutput` |
| le montant est dans la fourchette (5.3) | `warpfx.ErrInsufficientFee` / `ErrExcessiveBurn` |

**Pourquoi le contrôle de montant est ici.** Parce que le Fx n'est pas appelé, et
que c'est **lui** qui le ferait normalement (`out.Amt != in.Amt` →
`ErrMismatchedAmounts`, lot 1.7). En court-circuitant le Fx, on reprend à sa
charge ce qu'il faisait — sans quoi le contrôle serait purement et simplement
perdu.

**La ligne `ErrCanonicalImportInput` relève du même principe**, et elle ne
figurait pas dans la première rédaction de ce tableau. `warpfx.Fx` vérifie que
l'input est un `secp256k1fx.TransferInput` à `SigIndices` vide (lot 1.7) ; sans
la reprendre, le soumetteur choisirait le type d'input, et deux soumetteurs
honnêtes produiraient pour un même lot d'UTXOs des transactions **distinctes**,
donc non déduplicables par le mempool. La forme canonique est justement censée
lui retirer ce choix.

**Ce que ces contraintes laissent au soumetteur** : soumettre ou non, quand, et
le montant exact dans la fourchette. Rien d'autre. Il ne peut ni dévier de fonds,
ni les fragmenter, ni les geler.

⚠️ **Le lot partage un sort commun.** Si l'un des UTXOs a déjà été consommé, la
lecture de shared memory échoue et la transaction entière est rejetée. Rien n'est
perdu : il suffit de reconstruire avec les survivants. La soumission étant
ouverte à tous, **plusieurs soumetteurs reconstruisant la même transaction est le
cas normal**, pas un incident.

**Vérifier** : un test par ligne du tableau, en partant d'un cas valide et en
mutant un champ à la fois.

---

## 5.3 La fourchette de frais

C'est la contrainte qui n'a pas d'équivalent dans le monde signé, et celle dont
l'absence coûterait des fonds.

**Où** : `vms/warpfx/fee.go` (écrit au lot 1.6), appelée depuis
`verifyCanonicalImport`.

**Quoi** :

```
Σ inputs − fee(bloc)   ≥   montant de la sortie   ≥   Σ inputs − k × fee(bloc)
```

avec `k = warpfx.MaxFeeOverpaymentFactor = 2`.

**Pourquoi une borne inférieure, et c'est l'essentiel.** Les deux flow checkers
ne testent que `produced ≤ consumed` et brûlent tout surplus **sans plafond**.
Pour une transaction signée, cette souplesse est sans risque : le propriétaire
signe, et nul ne se vole soi-même. **Ici personne ne signe.** Sous une simple
inégalité, n'importe qui pourrait consommer un UTXO de 1 000 AVAX, en restituer
1 nAVAX et brûler le reste. L'attaquant n'y gagne rien — c'est de la destruction
pure — et **cela ne lui coûte rien non plus**, une transaction atomique n'ayant
pas de payeur de gas.

**Pourquoi pas une égalité stricte.** Une égalité rend la transaction valide pour
une seule valeur de frais : elle casse à la baisse comme à la hausse. Or les
frais bougent vite. Les paramètres ACP-103 du mainnet
(`TargetPerSecond = 50 000`, `MaxPerSecond = 100 000`,
`ExcessConversionConstant = 2 164 043`) donnent un **doublement du prix du gas
toutes les 30 s** sous congestion maximale soutenue, et une division par deux à
la même vitesse quand la chaîne est vide. Une égalité stricte ne survivrait pas à
la seule latence de diffusion.

**Ce que la fourchette donne, et rien de plus** :

- la validité ne casse plus qu'à la hausse : la transaction passe si et seulement
  si l'estimation du soumetteur est ≥ au frais réel ;
- la destruction est bornée par `(k − 1) × fee`, soit un frais supplémentaire au
  plus avec `k = 2` ;
- la marge temporelle vaut un doublement complet, soit 30 s de validité même sous
  congestion maximale — et bien davantage en pratique, la capacité étant plafonnée
  à 1 M de gas, un pic ne se maintient pas.

**Il n'y a pas de circularité**, contrairement à ce qu'on croit au premier abord :
le frais dépend de la taille de la transaction, qui contient le montant de la
sortie — mais un `uint64` sérialisé occupe 8 octets quelle que soit sa valeur. La
complexité, donc le frais, est **invariante par rapport au montant qu'on cherche
à calculer**.

**Le coût absolu est négligeable** : une `ImportTx` est dominée par ses lectures
et écritures de base (poids 1 000 chacune), soit de l'ordre de 10⁴ de gas, donc
~10⁻⁵ AVAX au prix plancher. Tolérer le double, c'est du bruit devant une
transaction qui casserait en boucle.

> ⚠️ **Le degré de liberté que la forme canonique laisse à un tiers, et qu'il
> faut assumer.**
> N'importe qui déclenche l'import quand il veut, et le frais est prélevé sur le
> montant importé. Un tiers malveillant peut donc **importer au pic tarifaire et
> brûler jusqu'à `k × fee` d'AVAX du propriétaire**. C'est borné (un import par
> lot d'UTXOs, un frais de l'ordre de 10⁻⁵ AVAX) mais c'est un transfert de
> contrôle réel, qui n'existe pas dans le chemin autorisé. Un propriétaire qui
> tient à ce contrôle utilise l'`ImportTx` autorisée. **Relever `k` au-delà du
> nécessaire rouvre proportionnellement ce vecteur.**

**Une convention d'outillage, non normative.** La fourchette a une contrepartie :
deux soumetteurs honnêtes qui choisissent des estimations différentes produisent
des transactions **distinctes** en compétition pour les mêmes UTXOs, là où une
égalité les rendait byte-identiques et donc déduplicables par le mempool. Il est
recommandé que l'outillage prenne `F = fee(état courant)` **exactement**, sans
marge, lorsqu'il construit juste avant de soumettre. Les soumetteurs honnêtes
convergent alors dans le cas courant.

**Vérifier** : test 6 du lot 12 — un nAVAX en dessous et au-dessus de chaque
borne.

---

## 5.4 Le court-circuit du Fx

C'est le point le plus délicat du lot. Il est correct, et il le reste
**uniquement** parce qu'il est déclenché par la conjonction du 5.1 — pas par
l'absence de credential porteur en général.

**Où** : `standard_tx_executor.go`, `ImportTx`, à l'intérieur de la garde de
bootstrap, après la lecture de shared memory et la désérialisation des UTXOs.

**Quoi** :

```go
if isCanonicalImport(tx, e.tx.Creds, utxos) {
    if err := verifyCanonicalImport(tx, e.tx.Creds, utxos, fee); err != nil {
        return err
    }
} else {
    if err := e.backend.FlowChecker.VerifySpendUTXOsWithContext(fxCtx, tx, utxos, ins, outs, e.tx.Creds, ...); err != nil {
        return err
    }
}
```

**Pourquoi le Fx n'est pas appelé.** Une transaction canonique ne présente
aucune autorisation, donc le test de provenance **n'a pas d'opérande**. Le
vérifieur reconnaît une sortie `warpfx` par son type et court-circuite l'appel,
exactement comme le fait la règle miroir côté saevm (lot 10c).

> ⚠️ **Le court-circuit appartient à cette branche, et à elle seule.**
> Généralisé en « pas de credential porteur → pas d'appel au Fx », il rendrait
> **tout UTXO `warpfx` dépensable par n'importe qui, depuis n'importe quelle
> transaction**. C'est, avec la rémanence d'autorisation, la **seconde surface
> de bug à consensus** de la proposition.
>
> Une `ImportTx` à `Ins` non vides, ou mêlant un input signé à des inputs
> `warpfx`, n'est **pas** canonique : elle retombe sur le chemin ordinaire, où
> l'UTXO `warpfx` atteint le Fx avec un contexte nul et se fait refuser sur
> `ErrNoAuthorization`. **Échouer dans ce sens est tout l'intérêt.**
>
> Test 2 du lot 12, et il doit être écrit avant le code, pas après.

**L'`ImportTx` autorisée subsiste**, et c'est elle qu'il faut employer dès qu'on
veut faire autre chose qu'une simple entrée de fonds : consommer en plus des
UTXOs déjà présents sur la P-Chain, payer les frais depuis un solde P-Chain
plutôt que sur le montant importé, ou produire plusieurs sorties. **Les deux
formes ne se distinguent que par une chose : la présence ou l'absence d'un
credential porteur.** En particulier, cela ne dépend **pas** de la façon dont
l'UTXO est arrivé en shared memory — qu'il ait été déposé par un export atomique
signé par un EOA ou par un appel au précompile, son origine est oubliée dès qu'il
y est. Les deux formes se soumettent d'ailleurs par le même `platform.issueTx`.

**Consolidation gratuite.** Si un export ne suffit pas, il suffit d'en émettre un
second : `ImportedInputs` est une collection **sans plafond**, donc N exports
successifs vers le même `WarpOwner` se regroupent dans un seul import canonique.

**Vérifier** :

```bash
go test ./vms/platformvm/txs/executor/ -run Canonical
```

---

## Ce qui reste incertain

🔴 **À trancher avant d'écrire**

- **La valeur de `k`.** `k = 2` est proposé et justifié (période de doublement du
  prix du gas P-Chain). L'ACP la marque comme question ouverte : reste à
  confirmer que la fenêtre est confortable sur le mainnet réel, où la chaîne est
  loin de la saturation. `k` est un **paramètre de consensus** : le changer après
  activation demanderait un upgrade.
- **Faut-il autoriser l'import canonique à consommer aussi des `Ins` P-Chain ?**
  Cela permettrait de payer les frais depuis un solde existant sans entamer le
  montant importé, et de consolider en une passe des UTXOs des deux origines. Le
  coût est que la forme cesserait d'être déterminée par les seuls UTXOs consommés
  en shared memory — donc de mériter le nom de canonique. **Probablement à
  refuser** : l'`ImportTx` autorisée couvre déjà ce cas, et c'est sa raison
  d'être. Mais la question est ouverte dans l'ACP.

✅ **Vérifié**

- **La place du bloc `if isCanonicalImport`** : à l'intérieur de la garde
  `Bootstrapped`, après la lecture de shared memory — les UTXOs sont nécessaires
  pour trancher. C'est bien l'inverse de la résolution d'autorisation (lot 4c),
  qui est **avant** la garde parce qu'elle ne lit aucun état. Les deux placements
  diffèrent pour de bonnes raisons, et le code le dit sur place.
- **Le calcul du `fee`** est passé à `CalculateFeeWithCredentials` au lot 9. Pour
  une transaction canonique la valeur est la même — elle n'a pas de credential
  porteur à tarifer — mais l'uniformité évite d'avoir à se rappeler laquelle des
  deux formes s'applique où.
- **La première condition se lit sur `fxCtx.Authorization`**, pas sur un second
  balayage de `Creds` : `resolveAuthorization` a déjà tranché, et le relire
  serait une seconde source de vérité.

⚠️ **Le lot 9 était un préalable non annoncé.** Sans la tarification des types
`warpfx` (9.1), `CalculateFee` échoue sur `errUnsupportedOutput` et l'import
canonique n'est pas exécutable avec le calculateur dynamique — donc pas testable
de bout en bout. `TestCanonicalImportPricedByComplexity` referme ce trou et
confirme au passage l'absence de circularité : le frais est identique avec une
sortie de 0 ou de `Σ inputs − fee`, un `uint64` occupant huit octets quelle que
soit sa valeur.

🟡 **À mesurer**

- **La marge temporelle réelle** de la fourchette sur le mainnet, où le prix du
  gas est loin de son régime de congestion maximale. C'est ce qui décide si
  `k = 2` est généreux ou juste.
