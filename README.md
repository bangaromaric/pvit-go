# pvit-go

Client Go de la passerelle de paiement mobile money **MyPVit**, écrit pour être réutilisé
tel quel dans un autre projet.

Zéro dépendance hors bibliothèque standard dans le code de production. `go-cmp` est une
dépendance de test, et l'élagage de graphe de Go la garde hors du build de qui importe ce
module.

```bash
go get github.com/bangaromaric/pvit-go
```

## Ce que le module fait, et ce qu'il laisse à l'appelant

| Il fait | Il ne fait pas |
|---|---|
| La rotation de la clé secrète, en cache, sous verrou, un compte à la fois | Lancer une goroutine. La rotation est paresseuse ; `Rafraichir` est là pour préchauffer |
| Le paiement, le remboursement, le statut, le KYC, les frais, le solde, les opérateurs, les pays | La boucle de réconciliation. Il donne le barème, l'appelant a le stockage |
| Le **lien de paiement** et le **paiement par carte Visa ou Mastercard** | Héberger le formulaire bancaire. C'est la passerelle qui le sert, derrière l'adresse rendue |
| Le décodage de la notification et son accusé à **écho dynamique** | Journaliser. Il rend des erreurs, l'appelant a sa politique de trace |
| Le classement des quatre-vingts codes d'erreur derrière dix sentinelles | Traduire en codes HTTP. C'est au projet appelant de le faire, en un seul endroit |
| La vérification HMAC du webhook de reporting, anneau de clés compris | Lire l'environnement de lui-même. `LireEnvironnement` est offerte, pas imposée |

## Les secrets à réunir, et où ils se trouvent

C'est le point le plus surprenant de cette passerelle, et il décide de la forme de la
configuration : **les codes d'URL vont par API, les comptes vont par opérateur.**

| Quoi | Combien | Où |
|---|---|---|
| Codes d'URL | **onze**, un par API, et par environnement | espace marchand, menu des API |
| Code de compte d'opération | un **par famille d'opérateur** (Airtel, Moov, …) | espace marchand |
| Clé secrète | une par compte, **valable 3 600 secondes** | rendue par `renew-secret` |
| Mot de passe de rotation | un par compte | espace marchand, menu des API. **Ce n'est pas celui du compte marchand** |
| Code de callback | un | espace marchand, menu des URL, après enregistrement de votre route |
| Codes de redirection | **deux**, succès et échec, exigés par un lien ou une carte | espace marchand, menu des URL. Ce sont des **codes**, pas des URL |

Tous les codes d'URL ne sont pas exigés au démarrage : `Nouveau` réclame ceux du paiement et
du statut, plus celui de rotation dès qu'un mot de passe est présent. Les autres sont
vérifiés au moment de l'appel, pour qu'un projet qui n'utilise que trois API n'ait pas à
réunir les onze.

## Démarrer

```go
client, err := pvit.Nouveau(pvit.Reglages{
    Codes: pvit.CodesURL{
        Secret:   "…", // renew-secret
        Paiement: "…", // rest
        Statut:   "…", // status
    },
    CodeCallback: "CB1234567890",
    Comptes: map[pvit.Operateur]pvit.Compte{
        pvit.OperateurAirtel: {CodeOperation: "ACC_1…", MotDePasse: "…"},
        pvit.OperateurMoov:   {CodeOperation: "ACC_2…", MotDePasse: "…"},
    },
})
```

Ou depuis l'environnement, sous le préfixe de votre choix :

```go
client, err := pvit.NouveauDepuisEnvironnement("") // "" vaut PVIT_
```

```
PVIT_BASE_URL                              defaut https://api.mypvit.pro
PVIT_CALLBACK_URL_CODE
PVIT_SUCCESS_REDIRECTION_URL_CODE          exiges par un lien ou une carte
PVIT_FAILED_REDIRECTION_URL_CODE
PVIT_URL_CODE_SECRET / _PAYMENT / _STATUS / _FEES / _BALANCE / _KYC / _COUNTRIES / _OPERATORS
PVIT_URL_CODE_LINK                         exige par un lien ou une carte
PVIT_URL_CODE_QRCODE / _REPORT             facultatifs, pour la trappe de sortie
PVIT_ACCOUNT_<OPERATEUR>_CODE
PVIT_ACCOUNT_<OPERATEUR>_SECRET
PVIT_ACCOUNT_<OPERATEUR>_PASSWORD
PVIT_ACCOUNT_<OPERATEUR>_SECRET_EXPIRES_AT facultatif, RFC 3339
```

**Règle de découverte** : les comptes ne sont pas une liste fermée. Ajouter une famille
d'opérateur est trois variables et **zéro ligne de Go**.

### Si vous n'avez qu'un code et une clé, sans mot de passe

Cela fonctionne : la clé d'amorce sert telle quelle, et aucun appel de rotation n'est émis.
Mais une clé vit 3 600 secondes, et le jour où elle expire, l'appel rend
`ErrRotationImpossible`, qui emballe `ErrConfiguration` et nomme la variable à définir.
`EstRejouable()` rend alors faux, pour qu'une boucle ne s'acharne pas sur un compte qu'elle
ne peut pas réparer.

## Les trois mécanismes indissociables

La documentation de la passerelle les donne ensemble, et il y a une raison.

1. **La rotation de clé.** Elle est automatique, proactive dix minutes avant le terme, et
   réactive par un unique rejeu quand une clé est refusée.
2. **L'écho dynamique du callback.** On lit `code` dans la notification et on le renvoie sous
   le nom `responseCode`. Écrire `200` en dur marque la transaction « échec de notification »
   et **invalide la recette d'intégration**, donc ferme le compte de production.
3. **L'interrogation du statut.** Ce n'est pas un filet de sécurité, c'est le mécanisme de
   vérité : le callback de transaction n'est ni signé, ni horodaté, ni muni d'un identifiant
   de remise, et sa politique de reprise n'est pas documentée. **Sa clé est la référence du
   marchand**, voir plus bas.

Les deux premiers sans le troisième laissent des paiements dans un état inconnu dès la
première remise perdue. Voir l'exemple de `AInterroger` pour la boucle, qui tient en quinze
lignes.

## Recevoir les notifications

Deux niveaux, et l'appelant choisit.

**Avec son propre routeur** (Gin, Chi, autre). Lire le **corps brut**, et non passer par le
décodeur du routeur : la signature du webhook de reporting porte sur les octets, et une
re-sérialisation la rendrait fausse.

```go
func recevoir(c *gin.Context) {
    corps, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, pvit.TailleMaxCallback))
    if err != nil {
        c.Status(http.StatusRequestEntityTooLarge)
        return
    }
    notification, err := pvit.DecoderCallback(corps)
    if err != nil {
        c.Status(http.StatusBadRequest)
        return
    }
    if err := enregistrer(c.Request.Context(), notification); err != nil {
        // PAS d'accusé : la passerelle doit réessayer plutôt que de tenir la
        // notification pour remise. L'acquitter perdrait l'argent annoncé.
        c.Status(http.StatusInternalServerError)
        return
    }
    _ = pvit.Accuser(c.Writer, notification) // gin.Context.Writer est un http.ResponseWriter
}
```

`c.Copy()` avant toute goroutine : le contexte Gin vient d'un `sync.Pool`.

**Sans routeur à soi** :

```go
mux.Handle("/pvit/callback", client.GestionnaireCallback(enregistrer))
```

### Filtrer l'origine

Le seul contrôle d'authenticité du callback de transaction est l'adresse d'origine :

```go
if !pvit.EstAutorisee(adresse) { /* refuser */ }
```

Le module ne fournit **aucun intergiciel** : quelle adresse interroger derrière un proxy est
une question que votre projet a déjà tranchée, et un intergiciel qui devine donnerait une
impression de protection plutôt qu'une protection.

## Paramétrage

Trois niveaux, et aucune option n'est obligatoire. Une valeur absurde empêche `Nouveau`
d'aboutir au lieu de se replier en silence.

```go
pvit.Nouveau(reglages,
    pvit.AvecDelais(5*time.Second, 15*time.Second),
    pvit.AvecMargeRotation(10*time.Minute),
    pvit.AvecDureeDeVieParDefaut(time.Hour),
    pvit.AvecRejeuAuthentification(1),
    pvit.AvecTailleAnneau(3),
    pvit.AvecService(pvit.ServiceRestful),
    pvit.AvecMediaTypeCallback("application/json"),
    pvit.AvecProprietaireFrais(pvit.ProprietaireClient),
    pvit.AvecProprietaireFraisOperateur(pvit.ProprietaireClient),
    pvit.AvecTailleMaxCallback(pvit.TailleMaxCallback),
    pvit.AvecFenetreSignature(pvit.FenetreSignature),
    pvit.AvecAgentUtilisateur("mon-service/1.0"),
    pvit.AvecRenouvellementEnQuery(), // repli, voir plus bas
    pvit.AvecFraisOperateurEnOperatorOwnerCharge(), // repli du lien, voir plus bas
    pvit.AvecHorloge(monHorloge),
)
```

`AvecClientHTTP` et `AvecDelais` sont **incompatibles**, et `Nouveau` les refuse ensemble : un
transport fourni porte déjà ses délais, et les accepter toutes deux ferait croire que vous
avez borné ce que vous n'avez pas borné.

Et par appel : `DemandePaiement` porte `CompteOperation`, `CodeCallback`, `Frais`,
`FraisOperateur`, `Agent`, `Produit` et `InfoLibre`. `DemandeLien` et `DemandeCarte` y ajoutent
`CodeRedirectionSucces` et `CodeRedirectionEchec`. Chacun vide prend le défaut du client,
renseigné le surcharge. C'est ce qui permet au même client de servir deux politiques de frais
sans être reconstruit, et une boutique en ligne et un comptoir avec deux jeux de pages de
retour.

`AvecService` **ne vaut que pour l'API de transaction**. Un lien prend son service dans sa
demande, et pour une raison dirimante : le défaut de cette option est `RESTFUL`, soit
justement le seul des quatre services que l'API de lien refuse.

## La trappe de sortie

Trois API sont hors du périmètre typé : le paiement fractionné, le QR code et les exports.
Elles restent joignables, avec la rotation de clé et le classement d'erreurs, sans forker le
module :

```go
rep, err := client.Appeler(ctx, pvit.Appel{
    Methode:   http.MethodPost,
    CodeURL:   codeDuPaiement,
    Chemin:    "rest",
    Versionne: true,
    Operateur: pvit.OperateurAirtel,
    Corps:     maChargeUtileFractionnee, // beneficiaries, sharing_type…
})
```

## Encaisser par carte bancaire

**Il n'existe aucun point d'entrée carte.** Un paiement Visa ou Mastercard est un **lien de
paiement** avec le service `VISA_MASTERCARD` : la passerelle rend une adresse, le formulaire
bancaire est hébergé derrière, et l'authentification forte se joue hors de portée du module.

```go
lien, err := client.PayerParCarte(ctx, pvit.DemandeCarte{
    Reference:    "CMD20261023001", // persistée AVANT l'appel
    Montant:      4500,
    NumeroClient: "074111111",
    Operateur:    pvit.OperateurAirtel, // ne part PAS sur le fil, voir plus bas
})
if err != nil {
    if errors.Is(err, pvit.ErrDejaInitiee) {
        // Référence déjà vue, ou lien déjà consommé : interroger le statut,
        // et surtout pas régénérer un lien, ce qui ferait payer deux fois.
    }
    return err
}
http.Redirect(w, r, lien.URL, http.StatusSeeOther)
```

Le parcours tient en trois temps, et **rien ne se crédite avant le troisième** :

1. `PayerParCarte` rend une adresse. Le client n'a encore rien vu.
2. Il remplit le formulaire, puis revient sur l'une de vos deux pages de retour. **Son
   retour sur la page de succès ne prouve rien** : c'est le client qui revient, pas la
   passerelle qui conclut.
3. Le statut définitif arrive par **la même notification que le mobile money**, sous la même
   référence et avec le même accusé à écho dynamique, ou se redemande par `client.Statut`.

Quatre points qui ne se devinent pas :

- **`Lien.StatutCreation` vaut `SUCCESS` dès la création.** Le champ ne s'appelle pas
  `Statut` exprès : `lien.Statut.EstSucces()` se lirait comme « le paiement a réussi » et
  créditerait un client qui n'a pas ouvert le formulaire.
- **L'opérateur ne part pas sur le fil**, mais il reste obligatoire : il choisit le compte
  d'opération *et* la clé secrète. Pour une carte, déclarez le compte de la famille que
  l'espace marchand vous a ouverte, sous sa clé, par exemple
  `PVIT_ACCOUNT_GIMAC_CODE`. **Zéro ligne de Go** : les comptes sont découverts.
- **Les deux codes de redirection sont obligatoires** côté passerelle, et facultatifs au
  démarrage : `CreerLien` les réclame au moment où ils servent, et nomme les deux d'un coup
  s'ils manquent.
- **La réponse du lien est en casse mêlée avec un code entier**, là où l'API de transaction
  répond en soulignés avec la chaîne `"200"`. Les deux formes sont lues, y compris par le
  classement d'erreurs : un refus formulé dans un corps de `200` est classé par son code
  applicatif, et non rangé en vrac derrière `ErrReponseIllisible`. Un code que la passerelle
  n'a jamais publié reste inexploitable, et surtout **pas rejouable**.

`CreerLien` sert les trois parcours du lien. `WEB` ouvre une page de paiement et rend le
numéro du client facultatif ; `VISA_MASTERCARD` et `RESTLINK` l'exigent. `RESTFUL` y est
**refusé avant tout appel** : c'est le parcours de `Payer`.

### Le désaccord sur le nom du second étage de frais

Les deux sources publiées par la passerelle se contredisent, sur ce seul champ et sur ce
seul point d'entrée. Le tableau des champs du lien écrit `owner_charge_operator`, comme
l'API de transaction ; la collection de requêtes écrit `operator_owner_charge`, mots
inversés. Le module émet **le premier** par défaut, parce que c'est le nom éprouvé contre le
serveur, et `AvecFraisOperateurEnOperatorOwnerCharge()` bascule sur le second sans nouvelle
livraison. Un seul des deux part jamais.

Ce que coûte le mauvais choix n'est pas un refus : un champ que la passerelle ne lit pas
laisse le second étage de frais prendre **son** défaut, et le marchand le découvre dans un
export.

## Traiter les erreurs

Le discriminant est le code applicatif, pas le code HTTP : un seul 403 couvre vingt-cinq cas.

```go
switch {
case errors.Is(err, pvit.ErrDejaInitiee):
    // PAS un échec : la tentative existe. Interroger son statut, ne jamais relancer
    // sous une référence neuve, ce qui créerait un double paiement.
case errors.Is(err, pvit.ErrRefusClient):
    // Le numéro est bloqué ou inconnu chez l'opérateur.
case errors.Is(err, pvit.ErrHorsBornes):
    // Montant sous le minimum ou au-dessus du plafond de l'opérateur.
case errors.Is(err, pvit.ErrRotationImpossible):
    // Clé périmée, aucun mot de passe pour la renouveler. Le message nomme la variable.
case errors.Is(err, pvit.ErrIndisponible):
    // Rejouable plus tard.
}

var refus pvit.Erreur
if errors.As(err, &refus) {
    log.Print(refus.Code, refus.Nom, refus.EstRejouable())
}
```

`context.Canceled` et `context.DeadlineExceeded` remontent **nus** : ce ne sont pas des
erreurs de la passerelle, et les ranger parmi les indisponibilités les rendrait rejouables.

## Interroger le statut : la clé est la référence

```go
etat, err := client.Statut(ctx, pvit.DemandeStatut{
    Reference: tentative.Reference, // PAS l'identifiant de la passerelle
    Operateur: tentative.Operateur,
})
```

**Et non l'identifiant que la passerelle attribue**, bien que le paramètre s'appelle
`transactionId` sur le fil et que la documentation de la passerelle affirme l'inverse. Mesuré
le 22 octobre 2026 sur une seule et même transaction réussie :

```
reference   ABOMYED6GURXUKV5YLBS -> SUCCESS, montant 4500, frais 112,5
identifiant PAY2210261373646     -> 4006 TRANSACTION_NOT_FOUND
```

Conséquence : `Initiation.TransactionID` est **informatif** et peut être vide. Ce qui se
persiste avant l'appel et ce qui réconcilie après, c'est la référence.

## Quatre réserves assumées

**La table des préfixes gabonais est incomplète, par construction.** `pvit/gabon` porte cinq
entrées, chacune confirmée par un appel `kyc` sur un numéro réel du préfixe : `74`, `76` et
`77` vers `AIRTEL_MONEY`, `62` et `66` vers `MOOV_MONEY`. Le plan de numérotation va plus
loin, et deviner coûterait cher : un mauvais appariement entre le compte et l'opérateur est
refusé au comptoir. `get-operators` donne le vocabulaire des opérateurs et rien sur les
préfixes ; c'est `kyc` qui transforme une présomption en entrée de table, et
`gabon.NouvelleTable` valide les clés que vous ajoutez.

**Le nom de l'en-tête d'horodatage du webhook de reporting n'est pas publié** dans la
documentation lue pour écrire ce module. Il n'est donc pas inventé : l'horodatage est un
paramètre de `VerifierSignatureRapport`, à lire dans l'en-tête que la passerelle emploie
réellement.

**Ce que `customer_account_number` porte sur un parcours carte n'est pas publié** au delà
de « requis ». Ce n'est jamais un numéro de carte, et ce module le traite comme le numéro du
client, comme partout ailleurs. Aucune donnée de carte ne traverse le module, et un test en
épingle le jeu de champs exact.

**Quelle famille de compte d'opération une carte exige n'est pas publié**, et la durée de vie
d'un lien non plus, bien que le code 6000 prouve qu'il en a une. D'où le type `Operateur`
ouvert et `AInterrogerApres`, à qui vous donnez le délai de votre propre tunnel : le défaut du
module, trois minutes, est calibré pour un parcours sans navigateur et il est trop court pour
un formulaire bancaire.

## Vérifier

```bash
bash verifier.sh
```

Format, vet, modernizer, tests sous détecteur de course, exemples, absence de dépendance
tierce en production, `go mod tidy`, et govulncheck s'il est installé.

## Changements

### v0.2.0, le paiement par carte bancaire

Ajout de surface, et **aucune rupture de compilation**.

Deux **correctifs relevés par audit** touchent des chemins antérieurs à la carte, et valent
d'être lus avant de monter :

- **la référence part désormais TAILLÉE** sur `rest` comme sur `link`. Elle était jusqu'ici
  taillée pour être *jugée* par la validation, puis la valeur brute partait sur le fil,
  tandis que `Statut`, qui taille elle aussi, la redemandait sans. Une référence recopiée
  avec une espace ou un retour à la ligne partait donc sous une forme et se redemandait sous
  une autre : les deux moitiés d'une même tentative ne se rejoignaient plus. `Initiation`,
  `Remboursement` et `Lien` rendent maintenant la référence telle qu'elle est partie, qui
  est celle qu'il faut persister ;
- **le code applicatif se lit dans les deux casses.** Les corps d'erreur publiés sont en
  soulignés, et le classement ne déclarait que ceux là. L'API de lien répondant en casse
  mêlée, un refus formulé dans sa propre casse rendait `Erreur.Code` à zéro et retombait sur
  la plage du code HTTP, dont le défaut est l'indisponibilité **rejouable** : une faute de
  configuration pouvait donc nourrir une boucle de rejeu.

- `Client.CreerLien` et `Client.PayerParCarte`, avec `DemandeLien`, `DemandeCarte` et `Lien`.
  Le lien de paiement quitte la trappe de sortie, qui passe de quatre API à trois.
- `Reglages.CodeRedirectionSucces` et `Reglages.CodeRedirectionEchec`, plus
  `PVIT_SUCCESS_REDIRECTION_URL_CODE` et `PVIT_FAILED_REDIRECTION_URL_CODE`. Non exigés au
  démarrage.
- `AvecFraisOperateurEnOperatorOwnerCharge()`, pour le désaccord de nommage ci-dessus.
- `AInterrogerApres`, qui est `AInterroger` avec un délai d'incertitude choisi par l'appelant.
  `AInterroger` délègue, à comportement identique.
- Cinq codes nommés : `CodeRedirectionInvalide` (1003), `CodeRedirectionEchecInvalide` (1004),
  `CodeRedirectionInactive` (3012), `CodeRedirectionEchecInactive` (3013), `CodeLienConsomme`
  (5002).

Deux **changements de comportement** sur le classement des erreurs, tous deux des correctifs :

- les codes **3012** et **3013** rendent désormais `ErrConfiguration` au lieu de
  `ErrRequeteInvalide`, et `EstFauteDeConfiguration()` répond vrai. Une URL de redirection
  éteinte se rallume dans l'espace marchand, pas en corrigeant l'appel. Même traitement que
  3017, déjà sorti de sa plage pour la même raison ;
- le code **5002** rend `ErrDejaInitiee` au lieu de `ErrRequeteInvalide`, et
  `EstDejaInitiee()` répond vrai. Un lien consommé veut dire « cette tentative existe,
  interroge son statut », exactement comme une référence déjà vue. `Erreur.Code` distingue
  toujours les deux diagnostics.

### v0.1.1, rupture d'API assumée

`DemandeStatut.TransactionID string` devient `DemandeStatut.Reference Reference`. Le
remplacement est **incompatible, et c'est le correctif** : un champ qui accepte les deux
valeurs est exactement ce qui a laissé l'erreur atteindre une production. La confusion ne
compile plus.

Deux conséquences pour qui monte depuis `v0.1.0` :

- renseigner `Reference` là où `TransactionID` était passé ;
- `Payer` ne rend plus d'erreur sur un accusé sans identifiant de transaction. Si votre code
  comptait sur ce refus, c'est la référence qui le remplace.

`v0.1.0` reste servie par le proxy Go, et reste fausse sur ce point : elle interroge le
statut avec une clé que la passerelle ne connaît pas, et ne tranche donc aucune tentative.

## Licence

MIT, voir [LICENSE](LICENSE).

## Avertissement sur la mise en production

La passerelle impose une recette avant d'ouvrir un compte de production : deux simulations de
succès sous 1 000 XAF, deux d'échec au-dessus, le callback **correctement acquitté à chaque
fois**, et les API de statut et de KYC implémentées et fonctionnelles. Un montant réel
supérieur à 1 000 XAF tombera donc en échec en bac à sable : le montant doit être
paramétrable, jamais une constante. Le module n'expose rien de ce seuil, et un test épingle
cette absence : le coder ferait refuser côté client des montants que la production accepte.

Pour un lien, la lecture retenue est que la **création** aboutit quel que soit le montant, et
que c'est la transaction qui se résout selon le seuil. Cette lecture n'est pas explicitement
publiée pour ce point d'entrée.
