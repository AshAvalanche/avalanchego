Specs
Le warpfx est la premiere feature extension  a créer un owner. Dans tous les autre ont embarque toujours un `secp256k1fx.OutputOwners` et pour les authentification on utilise toujours `secp256k1fx.Credential`. Il a donc sont propre `Owner` (respectant l'interface `fx.Owner` prévue a cet effet), ca propre méthode de dérivation `ID()` et donc son propre codec local (un `linearcodec`) qui ne register aucun type car aucun champ d'interface (pas d'adhérence complexe avec `platform.Codec`.

```go
type Owner struct {
	verify.IsNotState `json:"-"`
	
	SourceChainID ids.ID `serialize:"true" json:"sourceChainID"`
	SourceAddress []byte `serialize:"true" json:"sourceAddress"`
}
```
Ce Owner est constitué de la même paire utilisé pour l'identification de la source d'un message ICM a savoir `SourceChainID` et `SourceAddress`. Une contrainte imposé dans cette iteration de l'ACP est la taille de l'address qui est fixé a 20 bytes qui impose donc des échanges entre une chaine EVM et la P-Chain mais nous irons moins loin ici en ne rendant possible l'utilisation ce ce Fx qu'entre la C-Chain et la P-Chain (nous verrons ce qui fixe cette contrainte et a quels endroit).

Cette Fx sera utilisé dans un `TransferOutput` (un `UTXO`) également déclaré dans le même package:
```go
type TransferOutput struct {
	verify.IsState `json:"-"`

	Amt uint64 `serialize:"true" json:"amount"`
	Owner      `serialize:"true" json:"warpOwner"`
}
```
Pas de `Locktime` ici (contrairement a un UTXO sekp256). C'est un champ qui est utilisé pour blocker un UTXO j'usqu'au timestamp dans le `Locktime`. Contrairement à une clé privée secp256k1 passive, un `WarpOwner` est **piloté par un contrat intelligent** sur la C-Chain. Si une règle temporelle est nécessaire (ex: _« déblocage le 1er janvier »_), c'est le contrat Solidity qui l'applique. Pas besoin de dupliquer cette logique dans l'`UTXO` P-Chain. (Cela élude un certain nombre de questions technique (pas par flemmardise mais par une logique providentiel ))

Maintenant que nous avons les structure suffisante pour décrie qui peut dépensé quoi, il nous faut celle qui nous permettra d'autoriser cette dépense. 
```go
type Credential struct {
	WarpMessage []byte `serialize:"true" json:"warpMessage"`
}
```
Voila l'entité qui sera dans une `SignedTx` d'Import ou de staking. La subtilisé deriere cette structure est qu'il n'y aura qu'une seul verification de dépence d'UTXO pour le premier d'entre eux (pour une question d'optimisation de cout de la tx) en utilisant un contexte de validation pour valider les suivent sur la base unique du warpOwner (SourceChainID et SourceAddress) dont l'ownerID doit être identique a celui utilisé pour la verification.
Cela introduit une contrainte de plus dans le scope de cet ACP, une tx d'un warp owner ne peut dépenser que ses propre fonds.

Nous reparlerons du moment ou la validation de la dépense de la première UTXO intervient mais pour avoir lieu il nous faut la structure qui portera cette authorisation pour les autres UTXO de la Tx:
```go
type Authorization struct {
	SourceChainID ids.ID
	SourceAddress []byte
}
```
Seul deux chose a vérifier, toutes deux validant la provenance de celui a qui appartiens la Tx.

Dans une transaction classique, le signataire contrôle ce qu'il paye en frais.
Dans une transaction `warpfx`, un tiers (relayer/indexer) soumet la transaction pour le compte d'une address, Et dans cet ACP il y a deux cas de figures qui y sont introduit. 
- Le plus simple utilise un message warp dans le quel l'address émettrice a figé les UTXO en entrée et en sortie et c'est donc lui qui décide la marge qu'il alloue au gas et donc la souplesse nécessaire vis a vis des frai dynamique du réseau. Une `ResendTx` est prévu pour les situation de blocage et qui réécrira le message warp [!!! A Corriger !!!]
- Dans le second cas il s'agit d'une `ImportTx` et il n'y a donc pas de message ICM car la valeur importée est dans la shared memory, c'est donc le relayer qui met les UTXO de sortie, Il faut donc **borner strictement les frais** pour éviter qu'un relayer malveillant ne brûle tous les fonds du propriétaire en frais de transaction. Pour ce faire et pour laisser une fenêtre de temps raisonnable pour l'execution de la transaction, la somme de la valeur stocké dans les UTXOs de sortie (determinant la quantité de gas brûlé) ne pourra pas être inférieur a la valeur des fee (rien ne change) et supérieur a deux fois les fee effectif (lors de l'execution de la Tx) ce qui donne en pratique une fenêtre sûr de 30 seconde pour son execution. Au-dela, il est possible que les frais ai fluctué dans le mauvais sens et le relayer devra donc adapter les UTXOs de sortie en conséquence. 

