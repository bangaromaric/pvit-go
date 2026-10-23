package pvit_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	pvit "github.com/bangaromaric/pvit-go"
)

// Comment câbler le client à partir de ce qu'un espace marchand donne : un jeu de codes
// d'URL, et un compte d'opération par famille d'opérateur.
func ExampleNouveau() {
	client, err := pvit.Nouveau(pvit.Reglages{
		Codes: pvit.CodesURL{
			Secret:   "le-code-de-renew-secret",
			Paiement: "le-code-de-rest",
			Statut:   "le-code-de-status",
		},
		CodeCallback: "CB1234567890",
		Comptes: map[pvit.Operateur]pvit.Compte{
			pvit.OperateurAirtel: {CodeOperation: "ACC_1AIRTEL", MotDePasse: "mot-de-passe-api"},
			pvit.OperateurMoov:   {CodeOperation: "ACC_2MOOV", MotDePasse: "mot-de-passe-api"},
		},
	})
	if err != nil {
		fmt.Println("configuration refusee:", err)
		return
	}

	fmt.Println(client.OperateursConfigures())
	// Output: [AIRTEL_MONEY MOOV_MONEY]
}

// Un encaissement de bout en bout, et les deux choses qu'il faut en retenir : la réponse ne
// dit pas que le paiement est fait, et une référence déjà vue n'est pas un échec.
func ExampleClient_Payer() {
	// Un faux marchand, pour que l'exemple tourne sans réseau.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status_code":"200","status":"PENDING","reference_id":"PAY23102400001"}`))
	}))
	defer srv.Close()

	client, err := pvit.Nouveau(pvit.Reglages{
		BaseURL:      srv.URL,
		Codes:        pvit.CodesURL{Paiement: "cpay", Statut: "cstat"},
		CodeCallback: "CB1234567890",
		Comptes: map[pvit.Operateur]pvit.Compte{
			pvit.OperateurAirtel: {CodeOperation: "ACC_1AIRTEL", CleAmorce: "sk_live_amorce"},
		},
	}, pvit.AvecClientHTTP(srv.Client()))
	if err != nil {
		fmt.Println(err)
		return
	}

	// La référence porte l'idempotence : elle se persiste AVANT l'appel, et ne se
	// régénère jamais pour un rejeu.
	initiation, err := client.Payer(context.Background(), pvit.DemandePaiement{
		Reference:    "REF1234567890",
		Montant:      4500,
		Operateur:    pvit.OperateurAirtel,
		NumeroClient: "074111111",
	})
	switch {
	case errors.Is(err, pvit.ErrDejaInitiee):
		// Déjà engagée : on interroge son statut, on ne relance SURTOUT pas.
		fmt.Println("deja initiee, interroger le statut de", initiation.Reference)
		return
	case err != nil:
		fmt.Println("refus:", err)
		return
	}

	fmt.Println(initiation.TransactionID, initiation.Statut, initiation.Statut.EstIncertain())
	// Output: PAY23102400001 PENDING true
}

// La boucle de réconciliation que l'appelant doit écrire lui-même.
//
// Ce paquet n'a pas de stockage, donc il ne peut pas la tenir : il donne le barème et les
// prédicats, et l'appelant y branche sa persistance. Sans cette boucle, la première
// notification perdue laisse un paiement dans un état inconnu pour toujours.
func ExampleAInterroger() {
	// La tentative ne porte PAS l'identifiant de la passerelle : il n'entre dans aucune
	// décision de cette boucle. C'est la référence qui interroge le statut.
	type tentative struct {
		reference pvit.Reference
		statut    pvit.Statut
		initiee   time.Time
		essais    int
	}

	maintenant := time.Date(2026, time.October, 20, 8, 10, 0, 0, time.UTC)
	enCours := []tentative{
		{reference: "REF1", statut: pvit.StatutEnAttente, initiee: maintenant.Add(-10 * time.Minute)},
		{reference: "REF2", statut: pvit.StatutSucces, initiee: maintenant.Add(-10 * time.Minute)},
		{reference: "REF3", statut: pvit.StatutEnAttente, initiee: maintenant.Add(-1 * time.Minute)},
		{reference: "REF4", statut: pvit.StatutAmbigu, initiee: maintenant.Add(-2 * time.Hour), essais: 4},
	}

	for _, t := range enCours {
		if !pvit.AInterroger(t.statut, t.initiee, maintenant) {
			fmt.Println(t.reference, "rien a faire")
			continue
		}
		attente := pvit.ProchaineTentative(t.essais)
		if attente == 0 {
			fmt.Println(t.reference, "abandonnee, a traiter a la main")
			continue
		}
		// Ici, client.Statut(ctx, pvit.DemandeStatut{Reference: t.reference, ...}), puis
		// pvit.TransitionPermise(ancien, nouveau) avant d'ecrire le resultat.
		fmt.Println(t.reference, "a redemander, prochaine tentative dans", attente)
	}
	// Output:
	// REF1 a redemander, prochaine tentative dans 5m0s
	// REF2 rien a faire
	// REF3 rien a faire
	// REF4 abandonnee, a traiter a la main
}

// La grille des transitions, et les deux cases qui valent de l'argent : une notification
// remise six heures après coup ne doit ni annuler un encaissement acquis, ni ressusciter un
// échec déjà expliqué au commerçant.
func ExampleTransitionPermise() {
	fmt.Println(pvit.TransitionPermise(pvit.StatutEnAttente, pvit.StatutSucces))
	fmt.Println(pvit.TransitionPermise(pvit.StatutSucces, pvit.StatutEchec))
	fmt.Println(pvit.TransitionPermise(pvit.StatutEchec, pvit.StatutSucces))
	// Output:
	// true
	// false
	// false
}

// La notification de transaction, et l'écho dynamique de son code.
//
// Écrire 200 en dur marque la transaction « échec de notification » chez la passerelle, et
// sa documentation en fait une condition de validation des tests d'intégration.
func ExampleDecoderCallback() {
	corps := []byte(`{
		"transactionId": "PAY23102400001",
		"merchantReferenceId": "REF1234567890",
		"status": "SUCCESS",
		"amount": 150,
		"fees": 3.5,
		"operatorFees": 1.5,
		"amountCredited": 150,
		"code": 200
	}`)

	notification, err := pvit.DecoderCallback(corps)
	if err != nil {
		fmt.Println(err)
		return
	}

	accuse, err := notification.Accuse()
	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println(notification.Reference, notification.Statut)
	fmt.Println("les deux etages de frais:", notification.Frais, "et", notification.FraisOperateur)
	fmt.Println(string(accuse))
	// Output:
	// REF1234567890 SUCCESS
	// les deux etages de frais: 3,5 XAF et 1,5 XAF
	// {"transactionId":"PAY23102400001","responseCode":200}
}

// enregistrer tient la place de l'ecriture en base de l'appelant.
func enregistrer(pvit.Reference, pvit.Statut) error { return nil }

// La route qui reçoit les notifications, pour qui n'a pas de routeur à lui.
//
// Un projet qui en a un garde le sien et appelle [pvit.DecoderCallback] puis
// [pvit.Accuser] : le second prend un http.ResponseWriter, que le writer d'un contexte Gin
// satisfait déjà.
func ExampleGestionnaireCallback() {
	gestionnaire := pvit.GestionnaireCallback(func(_ context.Context, n pvit.Callback) error {
		// Ici, l'ecriture en base, sous la reference du marchand. Rendre une erreur
		// empeche l'accuse et fait reessayer la passerelle : acquitter un traitement en
		// echec perdrait l'argent annonce.
		return enregistrer(n.Reference, n.Statut)
	})

	w := httptest.NewRecorder()
	corps := strings.NewReader(`{"transactionId":"PAY1","merchantReferenceId":"REF1","status":"SUCCESS","code":200}`)
	gestionnaire.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/pvit/callback", corps))

	fmt.Println(w.Code)
	fmt.Println(strings.TrimSpace(w.Body.String()))
	// Output:
	// 200
	// {"transactionId":"PAY1","responseCode":200}
}

// La lecture d'environnement, offerte et non imposée, et sa règle de découverte : ajouter
// une famille d'opérateur est trois variables et zéro ligne de Go.
func ExampleLireEnvironnement() {
	// Dans un vrai programme, ces variables viennent de l'environnement du service.
	fmt.Println(pvit.NomVariableCompte("", pvit.OperateurAirtel))
	fmt.Println(pvit.NomVariableSecret("", pvit.OperateurAirtel))
	fmt.Println(pvit.NomVariableMotDePasse("", pvit.OperateurAirtel))
	fmt.Println(pvit.NomVariableMotDePasse("CAISSE_", pvit.OperateurMoov))
	// Output:
	// PVIT_ACCOUNT_AIRTEL_MONEY_CODE
	// PVIT_ACCOUNT_AIRTEL_MONEY_SECRET
	// PVIT_ACCOUNT_AIRTEL_MONEY_PASSWORD
	// CAISSE_ACCOUNT_MOOV_MONEY_PASSWORD
}

// Les sommes, et pourquoi il y a deux types : la passerelle prescrit de rejeter tout
// flottant non entier, puis publie elle-même des frais de 3,5 francs.
func ExampleMontant() {
	montant, err := pvit.NouveauMontant(4500)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(montant)
	fmt.Println(montant.EnFCFA())

	var frais pvit.Frais = 3500 // millièmes de franc
	fmt.Println(frais, "arrondi a", frais.ArrondiSup())
	// Output:
	// 4 500 XAF
	// 4 500 FCFA
	// 3,5 XAF arrondi a 4 XAF
}

// Un encaissement par CARTE BANCAIRE, et les trois choses qu'il faut en retenir : il passe
// par l'API de lien, l'adresse rendue mène au formulaire que la passerelle héberge, et le
// SUCCESS de la réponse concerne la création du lien et jamais le paiement.
func ExampleClient_PayerParCarte() {
	// Un faux marchand, pour que l'exemple tourne sans réseau. Noter la casse mêlée et le
	// code de retour ENTIER, qui sont la forme de ce point d'entrée.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"url":"https://checkout.mypvit.pro/PAY23102400001",` +
			`"merchantReferenceId":"CMD20261023001","status":"SUCCESS","statusCode":200}`))
	}))
	defer srv.Close()

	client, err := pvit.Nouveau(pvit.Reglages{
		BaseURL: srv.URL,
		Codes:   pvit.CodesURL{Paiement: "cpay", Statut: "cstat", Lien: "clink"},
		// Les trois codes que la passerelle exige, tous déclarés dans l'espace marchand.
		// Ce sont des CODES, et non des URL.
		CodeCallback:          "CB1234567890",
		CodeRedirectionSucces: "SUCCESS123",
		CodeRedirectionEchec:  "FAILURE123",
		Comptes: map[pvit.Operateur]pvit.Compte{
			pvit.OperateurAirtel: {CodeOperation: "ACC_1AIRTEL", CleAmorce: "sk_live_amorce"},
		},
	}, pvit.AvecClientHTTP(srv.Client()))
	if err != nil {
		fmt.Println(err)
		return
	}

	// La référence est persistée AVANT l'appel : elle porte l'idempotence à l'aller, et
	// l'interrogation du statut au retour.
	lien, err := client.PayerParCarte(context.Background(), pvit.DemandeCarte{
		Reference:    "CMD20261023001",
		Montant:      4500,
		NumeroClient: "074111111",
		// L'opérateur ne part PAS sur le fil : il choisit le compte d'opération et la clé
		// secrète. Pour une carte, c'est la famille que l'espace marchand a ouverte.
		Operateur: pvit.OperateurAirtel,
	})
	if err != nil {
		// Une tentative qui existe déjà, référence vue ou lien consommé, n'est pas un
		// échec : il faut interroger son statut, et surtout pas régénérer un lien.
		if errors.Is(err, pvit.ErrDejaInitiee) {
			fmt.Println("tentative existante, interroger le statut de", lien.Reference)
			return
		}
		fmt.Println(err)
		return
	}

	// Rediriger le client vers cette adresse. Le formulaire bancaire est derrière.
	fmt.Println("rediriger vers:", lien.URL)

	// ET NE RIEN CRÉDITER ICI. Ce statut est celui de la CRÉATION du lien. Le client n'a
	// pas encore vu le formulaire, et son retour sur la page de succès ne prouvera rien
	// non plus : seuls la notification et [pvit.Client.Statut] tranchent.
	fmt.Println("creation du lien:", lien.StatutCreation)
	fmt.Println("paiement:", pvit.StatutEnAttente, "- attendre la notification")

	// Output:
	// rediriger vers: https://checkout.mypvit.pro/PAY23102400001
	// creation du lien: SUCCESS
	// paiement: PENDING - attendre la notification
}
