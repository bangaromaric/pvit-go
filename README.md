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
| Code de callback | un | espace marchand, après enregistrement de votre route |

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
PVIT_URL_CODE_SECRET / _PAYMENT / _STATUS / _FEES / _BALANCE / _KYC / _COUNTRIES / _OPERATORS
PVIT_URL_CODE_LINK / _QRCODE / _REPORT     facultatifs, pour la trappe de sortie
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
    pvit.AvecHorloge(monHorloge),
)
```

`AvecClientHTTP` et `AvecDelais` sont **incompatibles**, et `Nouveau` les refuse ensemble : un
transport fourni porte déjà ses délais, et les accepter toutes deux ferait croire que vous
avez borné ce que vous n'avez pas borné.

Et par appel : `DemandePaiement` porte `CompteOperation`, `CodeCallback`, `Frais`,
`FraisOperateur`, `Agent`, `Produit` et `InfoLibre`. Chacun vide prend le défaut du client,
renseigné le surcharge. C'est ce qui permet au même client de servir deux politiques de frais
sans être reconstruit.

## La trappe de sortie

Quatre API sont hors du périmètre typé : le lien de paiement, le paiement fractionné, le QR
code et les exports. Elles restent joignables, avec la rotation de clé et le classement
d'erreurs, sans forker le module :

```go
rep, err := client.Appeler(ctx, pvit.Appel{
    Methode:   http.MethodPost,
    CodeURL:   codeDuLien,
    Chemin:    "link",
    Versionne: false, // le lien est au chemin NON versionné, avec la charge utile v2
    Operateur: pvit.OperateurAirtel,
    Corps:     maChargeUtile,
})
```

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

## Deux réserves assumées

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

## Vérifier

```bash
bash verifier.sh
```

Format, vet, modernizer, tests sous détecteur de course, exemples, absence de dépendance
tierce en production, `go mod tidy`, et govulncheck s'il est installé.

## Changements

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
paramétrable, jamais une constante.
