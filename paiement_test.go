package pvit_test

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"net/http"
	"strings"
	"testing"

	pvit "github.com/bangaromaric/pvit-go"
	"github.com/google/go-cmp/cmp"
)

// decoder lit un corps JSON en carte, pour comparer une charge utile sans dépendre de
// l'ordre des clés, que la sérialisation ne garantit pas.
func decoder(t *testing.T, corps string) map[string]any {
	t.Helper()
	var carte map[string]any
	if err := json.Unmarshal([]byte(corps), &carte); err != nil {
		t.Fatalf("json.Unmarshal(%q) err = %v, want nil", corps, err)
	}
	return carte
}

// LE TEST LE PLUS RENTABLE DU PAQUET.
//
// Il compare la charge utile émise à celle que la passerelle publie dans sa collection de
// requêtes, champ par champ et en une fois. C'est ce qui attrape les fautes qui coûtent une
// journée : un champ que le schéma donne optionnel et que le serveur exige, un montant
// sérialisé en flottant, un en-tête oublié, un nom de champ à l'ordre des mots inversé.
func TestLaChargeUtileDePaiementEstCelleDeLaCollection(t *testing.T) {
	t.Parallel()

	client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule())
	b.programmer("rest", reponse{statut: http.StatusOK, corps: corpsPaiement})

	demande := pvit.DemandePaiement{
		Reference:    "REF1234567890",
		Montant:      150,
		Operateur:    pvit.OperateurAirtel,
		NumeroClient: "074111111",
		Agent:        "AGENT123",
		Produit:      "PRODUCT123",
		InfoLibre:    "Free Info",
	}
	if _, err := client.Payer(t.Context(), demande); err != nil {
		t.Fatalf("Payer() err = %v, want nil", err)
	}

	vue := b.derniere(t, "rest")

	// La charge utile publiée par la passerelle, verbatim, les variables substituées.
	want := decoder(t, `{
		"agent": "AGENT123",
		"amount": 150,
		"product": "PRODUCT123",
		"reference": "REF1234567890",
		"service": "RESTFUL",
		"callback_url_code": "`+codeCallback+`",
		"customer_account_number": "074111111",
		"merchant_operation_account_code": "`+compteAirtel+`",
		"transaction_type": "PAYMENT",
		"owner_charge": "CUSTOMER",
		"owner_charge_operator": "CUSTOMER",
		"free_info": "Free Info",
		"operator_code": "AIRTEL_MONEY"
	}`)

	if diff := cmp.Diff(want, decoder(t, vue.Corps)); diff != "" {
		t.Errorf("la charge utile de paiement differe de celle publiee (-want +got):\n%s", diff)
	}

	// Le montant est un ENTIER NU dans le JSON émis, et non 150.0 : la devise n'a pas de
	// subdivision, et un flottant finirait par rendre 149,99999.
	if !strings.Contains(vue.Corps, `"amount":150`) {
		t.Errorf("le corps emis = %s, want un montant entier nu", vue.Corps)
	}

	entetes := map[string]string{
		"methode":              vue.Methode,
		"X-Secret":             vue.Secret,
		"Content-Type":         vue.TypeContenu,
		"X-Callback-MediaType": vue.MediaTypeCallback,
	}
	wantEntetes := map[string]string{
		"methode":              http.MethodPost,
		"X-Secret":             cleAmorce,
		"Content-Type":         "application/json",
		"X-Callback-MediaType": "application/json",
	}
	if diff := cmp.Diff(wantEntetes, entetes); diff != "" {
		t.Errorf("les en-tetes du paiement different (-want +got):\n%s", diff)
	}
	if got, want := vue.Chemin, "/v2/"+codePaiement+"/rest"; got != want {
		t.Errorf("le paiement a appele %q, want %q", got, want)
	}
}

// Le remboursement est SYNCHRONE : aucune notification ne suit, et son champ de code de
// callback n'existe pas. Il n'est pas vide ni omis, il est absent du type, de sorte qu'on ne
// puisse pas l'émettre par distraction.
func TestLeRemboursementNaNiCodeDeCallbackNiEnTeteDeNotification(t *testing.T) {
	t.Parallel()

	client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule())
	b.programmer("rest", reponse{statut: http.StatusOK, corps: `{"status_code":"200","status":"SUCCESS","reference_id":"GIV0001"}`})

	rembours, err := client.Rembourser(t.Context(), pvit.DemandeRemboursement{
		Reference:    "REF1234567890",
		Montant:      150,
		Operateur:    pvit.OperateurAirtel,
		NumeroClient: "074111111",
		Agent:        "AGENT123",
		Produit:      "PRODUCT123",
		InfoLibre:    "Free Info",
	})
	if err != nil {
		t.Fatalf("Rembourser() err = %v, want nil", err)
	}

	vue := b.derniere(t, "rest")
	want := decoder(t, `{
		"agent": "AGENT123",
		"amount": 150,
		"product": "PRODUCT123",
		"reference": "REF1234567890",
		"service": "RESTFUL",
		"customer_account_number": "074111111",
		"merchant_operation_account_code": "`+compteAirtel+`",
		"transaction_type": "GIVE_CHANGE",
		"owner_charge": "MERCHANT",
		"owner_charge_operator": "CUSTOMER",
		"free_info": "Free Info",
		"operator_code": "AIRTEL_MONEY"
	}`)
	if diff := cmp.Diff(want, decoder(t, vue.Corps)); diff != "" {
		t.Errorf("la charge utile de remboursement differe de celle publiee (-want +got):\n%s", diff)
	}

	if strings.Contains(vue.Corps, "callback_url_code") {
		t.Errorf("le remboursement a emis un code de callback: %s", vue.Corps)
	}
	if vue.MediaTypeCallback != "" {
		t.Errorf("le remboursement a pose X-Callback-MediaType = %q, want aucun", vue.MediaTypeCallback)
	}

	// Son statut est FINAL : il n'y a rien à réconcilier.
	if !rembours.Statut.EstFinal() {
		t.Errorf("Rembourser() statut = %q, EstFinal() = false, want true", rembours.Statut)
	}
}

// LE CONTRAT D'IDEMPOTENCE. Une référence déjà vue n'est pas un échec : c'est « cette
// tentative existe, va interroger son statut ». Relancer sous une référence neuve créerait
// un double paiement, et c'est pourquoi la méthode rend LES DEUX.
func TestUneReferenceDejaVueRendLaReferenceEtErrDejaInitiee(t *testing.T) {
	t.Parallel()

	client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule())
	b.programmer("rest", reponse{statut: http.StatusConflict, corps: corpsDejaInitiee})

	initiation, err := client.Payer(t.Context(), demandeType())

	if !errors.Is(err, pvit.ErrDejaInitiee) {
		t.Fatalf("Payer() err = %v, want ErrDejaInitiee", err)
	}
	if got, want := initiation.Reference, pvit.Reference("REF1234567890"); got != want {
		t.Errorf("Payer() Initiation.Reference = %q, want %q: elle sert a interroger le statut", got, want)
	}
	if !initiation.Statut.EstIncertain() {
		t.Errorf("Payer() Initiation.Statut = %q, EstIncertain() = false, want true", initiation.Statut)
	}

	var refus pvit.Erreur
	if !errors.As(err, &refus) {
		t.Fatalf("Payer() err = %v, want une pvit.Erreur", err)
	}
	if !refus.EstDejaInitiee() {
		t.Errorf("Erreur.EstDejaInitiee() = false, want true")
	}
	if got, want := refus.Code, pvit.CodeReferenceDejaUtilisee; got != want {
		t.Errorf("Erreur.Code = %d, want %d", got, want)
	}
	if got, want := b.comptePour("rest"), 1; got != want {
		t.Errorf("un 409 a declenche %d appels, want %d: il ne se rejoue pas", got, want)
	}
}

// Les longueurs valident côté serveur : les laisser passer coûte un aller-retour et rend un
// corps d'erreur qui ne nomme pas toujours le champ fautif. Aucune requête ne doit partir.
func TestLesLongueursDuresSontRefuseesAvantLAppel(t *testing.T) {
	t.Parallel()

	long := func(n int) string { return strings.Repeat("A", n) }

	cas := []struct {
		nom       string
		modifier  func(*pvit.DemandePaiement)
		wantChamp string
	}{
		{
			nom:       "reference de vingt et un",
			modifier:  func(d *pvit.DemandePaiement) { d.Reference = pvit.Reference(long(21)) },
			wantChamp: "reference",
		},
		{
			nom:       "reference avec un tiret",
			modifier:  func(d *pvit.DemandePaiement) { d.Reference = "REF-123" },
			wantChamp: "reference",
		},
		{
			nom:       "numero de vingt-quatre",
			modifier:  func(d *pvit.DemandePaiement) { d.NumeroClient = long(24) },
			wantChamp: "customer_account_number",
		},
		{
			nom:       "agent de seize",
			modifier:  func(d *pvit.DemandePaiement) { d.Agent = long(16) },
			wantChamp: "agent",
		},
		{
			nom:       "produit de seize",
			modifier:  func(d *pvit.DemandePaiement) { d.Produit = long(16) },
			wantChamp: "product",
		},
		{
			nom:       "info libre de seize",
			modifier:  func(d *pvit.DemandePaiement) { d.InfoLibre = long(16) },
			wantChamp: "free_info",
		},
		{
			nom:       "code de compte de trente et un",
			modifier:  func(d *pvit.DemandePaiement) { d.CompteOperation = long(31) },
			wantChamp: "merchant_operation_account_code",
		},
		{
			nom:       "code de callback de treize",
			modifier:  func(d *pvit.DemandePaiement) { d.CodeCallback = long(13) },
			wantChamp: "callback_url_code",
		},
		{
			nom:       "montant nul",
			modifier:  func(d *pvit.DemandePaiement) { d.Montant = 0 },
			wantChamp: "amount",
		},
		{
			nom:       "montant negatif",
			modifier:  func(d *pvit.DemandePaiement) { d.Montant = -150 },
			wantChamp: "amount",
		},
		{
			nom:       "operateur vide",
			modifier:  func(d *pvit.DemandePaiement) { d.Operateur = "" },
			wantChamp: "operator_code",
		},
		{
			nom:       "numero vide",
			modifier:  func(d *pvit.DemandePaiement) { d.NumeroClient = "" },
			wantChamp: "customer_account_number",
		},
		{
			nom:       "proprietaire de frais inconnu",
			modifier:  func(d *pvit.DemandePaiement) { d.Frais = "BOTH" },
			wantChamp: "owner_charge",
		},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()
			client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule())

			demande := demandeType()
			c.modifier(&demande)

			_, err := client.Payer(t.Context(), demande)
			if !errors.Is(err, pvit.ErrRequeteInvalide) {
				t.Errorf("Payer() err = %v, want ErrRequeteInvalide", err)
			}

			var champ pvit.ChampInvalideError
			if !errors.As(err, &champ) {
				t.Fatalf("Payer() err = %v, want une ChampInvalideError", err)
			}
			if champ.Champ != c.wantChamp {
				t.Errorf("Payer() champ fautif = %q, want %q", champ.Champ, c.wantChamp)
			}
			if got := len(b.vues()); got != 0 {
				t.Errorf("Payer() a emis %d requetes sur une demande invalide, want 0", got)
			}
		})
	}
}

// Toutes les violations d'un coup, et non la première : un formulaire se corrige alors en
// un passage, là où une validation qui s'arrête au premier défaut demande autant
// d'allers-retours qu'il y a de fautes.
func TestValiderRendToutesLesViolationsEnUneFois(t *testing.T) {
	t.Parallel()

	demande := pvit.DemandePaiement{
		Reference:    "REF-INVALIDE",
		Montant:      0,
		Operateur:    "",
		NumeroClient: "",
		Agent:        strings.Repeat("A", 16),
	}

	err := demande.Valider()
	if err == nil {
		t.Fatalf("Valider() err = nil, want une erreur")
	}

	var jointes interface{ Unwrap() []error }
	if !errors.As(err, &jointes) {
		t.Fatalf("Valider() err = %v, want un errors.Join", err)
	}
	if got, want := len(jointes.Unwrap()), 5; got != want {
		t.Errorf("Valider() a releve %d violations, want %d: %v", got, want, err)
	}

	for _, champ := range []string{"reference", "amount", "operator_code", "customer_account_number", "agent"} {
		if !strings.Contains(err.Error(), champ) {
			t.Errorf("Valider() err = %q, want un message citant %q", err, champ)
		}
	}
}

// Une annulation demandée par l'appelant n'est PAS une panne de la passerelle. La ranger
// parmi les indisponibilités la rendrait rejouable, et une boucle de réconciliation
// relancerait ce que l'appelant venait d'abandonner.
func TestUnContexteAnnuleRemonteNu(t *testing.T) {
	t.Parallel()

	client, _ := nouveauBancAvecReglages(t, reglagesAmorceSeule())

	ctx, annuler := context.WithCancel(t.Context())
	annuler()

	_, err := client.Payer(ctx, demandeType())
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Payer(contexte annule) err = %v, want context.Canceled", err)
	}
	if errors.Is(err, pvit.ErrIndisponible) {
		t.Errorf("Payer(contexte annule) err = %v, want PAS ErrIndisponible: ce n est pas rejouable", err)
	}
}

// Un accusé sans identifiant RESTE EXPLOITABLE, et la version 0.1.0 en rendait une erreur.
//
// Elle croyait que l'interrogation de statut n'acceptait que l'identifiant de la passerelle,
// donc qu'une tentative sans lui était irréconciliable. C'est la référence qui réconcilie, et
// l'appelant l'a persistée avant l'appel : refuser ici rangeait du côté de l'échec un
// paiement réellement engagé, qui allait débiter le client.
func TestUnAccuseSansIdentifiantResteExploitable(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom        string
		corps      string
		wantStatut pvit.Statut
	}{
		{nom: "aucun identifiant", corps: `{"status_code":"200","status":"PENDING"}`, wantStatut: pvit.StatutEnAttente},
		{nom: "identifiant vide", corps: `{"status_code":"200","status":"PENDING","reference_id":""}`, wantStatut: pvit.StatutEnAttente},
		{nom: "identifiant nul", corps: `{"status_code":"200","status":"PENDING","reference_id":null}`, wantStatut: pvit.StatutEnAttente},
		{nom: "corps vide", corps: `{}`},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()
			client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule())
			b.programmer("rest", reponse{statut: http.StatusOK, corps: c.corps})

			initiation, err := client.Payer(t.Context(), demandeType())
			if err != nil {
				t.Fatalf("Payer() sur %s err = %v, want nil", c.corps, err)
			}
			if got, want := initiation.Reference, pvit.Reference("REF1234567890"); got != want {
				t.Errorf("Payer() Reference = %q, want %q: c est la cle de la reconciliation", got, want)
			}
			if got := initiation.TransactionID; got != "" {
				t.Errorf("Payer() TransactionID = %q, want vide sur un accuse qui n en porte pas", got)
			}
			if got := initiation.Statut; got != c.wantStatut {
				t.Errorf("Payer() Statut = %q, want %q", got, c.wantStatut)
			}
		})
	}
}

// L'identifiant arrive sous plusieurs noms selon le point d'entrée, et le code de retour
// sous quatre formes. Le décodage doit tenir contre les variations, pas contre un seul nom.
func TestLAccuseSeLitQuelQueSoitLeNomDeLIdentifiant(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom      string
		corps    string
		wantID   string
		wantCode int
	}{
		{nom: "reference_id", corps: `{"status_code":"200","status":"PENDING","reference_id":"PAY1"}`, wantID: "PAY1", wantCode: 200},
		{nom: "transaction_id", corps: `{"status_code":200,"status":"PENDING","transaction_id":"PAY2"}`, wantID: "PAY2", wantCode: 200},
		{nom: "transactionId", corps: `{"statusCode":200,"status":"PENDING","transactionId":"PAY3"}`, wantID: "PAY3", wantCode: 200},
		{nom: "identifiant numerique", corps: `{"status_code":"200","status":"PENDING","reference_id":9168}`, wantID: "9168", wantCode: 200},
		{nom: "statut inconnu", corps: `{"status_code":"200","status":"WEIRD","reference_id":"PAY4"}`, wantID: "PAY4", wantCode: 200},
		{nom: "sans code", corps: `{"status":"PENDING","reference_id":"PAY5"}`, wantID: "PAY5", wantCode: 0},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()
			client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule())
			b.programmer("rest", reponse{statut: http.StatusOK, corps: c.corps})

			initiation, err := client.Payer(t.Context(), demandeType())
			if err != nil {
				t.Fatalf("Payer() sur %s err = %v, want nil", c.corps, err)
			}
			if got := initiation.TransactionID; got != c.wantID {
				t.Errorf("Payer() TransactionID = %q, want %q", got, c.wantID)
			}
			if got := initiation.Code; got != c.wantCode {
				t.Errorf("Payer() Code = %d, want %d", got, c.wantCode)
			}
		})
	}
}

// Un statut inconnu ne doit pas casser le décodage, et doit rester incertain : c'est le cas
// où il ne faut surtout pas conclure.
func TestUnStatutInconnuDansLAccuseResteIncertain(t *testing.T) {
	t.Parallel()

	client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule())
	b.programmer("rest", reponse{statut: http.StatusOK, corps: `{"status_code":"200","status":"WEIRD","reference_id":"PAY1"}`})

	initiation, err := client.Payer(t.Context(), demandeType())
	if err != nil {
		t.Fatalf("Payer() err = %v, want nil", err)
	}
	if !initiation.Statut.EstIncertain() {
		t.Errorf("Payer() statut %q EstIncertain() = false, want true", initiation.Statut)
	}
	if got, want := initiation.Statut.String(), "WEIRD"; got != want {
		t.Errorf("Payer() statut = %q, want %q conserve pour le journal", got, want)
	}
}

// Sans code de callback, aucune notification n'est jamais émise : la passerelle donne ce
// champ pour facultatif, mais c'est un optionnel de syntaxe et un obligatoire de
// fonctionnement.
func TestUnPaiementSansCodeDeCallbackEstRefuse(t *testing.T) {
	t.Parallel()

	r := reglagesAmorceSeule()
	r.CodeCallback = ""
	client, b := nouveauBancAvecReglages(t, r)

	_, err := client.Payer(t.Context(), demandeType())
	if !errors.Is(err, pvit.ErrRequeteInvalide) {
		t.Errorf("Payer() sans code de callback err = %v, want ErrRequeteInvalide", err)
	}
	if !strings.Contains(err.Error(), "callback_url_code") {
		t.Errorf("Payer() err = %q, want un message nommant callback_url_code", err)
	}
	if got := len(b.vues()); got != 0 {
		t.Errorf("Payer() a emis %d requetes, want 0", got)
	}
}

// Le paramétrage à deux niveaux : le défaut du client, et la surcharge par appel. C'est ce
// qui permet au même client de servir deux politiques de frais sans être reconstruit.
func TestLesFraisPrennentLeDefautDuClientPuisLaSurchargeParAppel(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom                string
		options            []pvit.Option
		surcharge          pvit.Proprietaire
		surchargeOperateur pvit.Proprietaire
		wantFrais          string
		wantFraisOperateur string
	}{
		{nom: "defaut du paquet", wantFrais: "CUSTOMER", wantFraisOperateur: "CUSTOMER"},
		{
			nom:                "defaut impose par option",
			options:            []pvit.Option{pvit.AvecProprietaireFrais(pvit.ProprietaireMarchand)},
			wantFrais:          "MERCHANT",
			wantFraisOperateur: "CUSTOMER",
		},
		{
			nom:                "les deux etages par option",
			options:            []pvit.Option{pvit.AvecProprietaireFrais(pvit.ProprietaireMarchand), pvit.AvecProprietaireFraisOperateur(pvit.ProprietaireMarchand)},
			wantFrais:          "MERCHANT",
			wantFraisOperateur: "MERCHANT",
		},
		{
			nom:                "la surcharge par appel gagne sur l option",
			options:            []pvit.Option{pvit.AvecProprietaireFrais(pvit.ProprietaireMarchand)},
			surcharge:          pvit.ProprietaireClient,
			wantFrais:          "CUSTOMER",
			wantFraisOperateur: "CUSTOMER",
		},
		{
			nom:                "la surcharge par appel sur les deux etages",
			surcharge:          pvit.ProprietaireMarchand,
			surchargeOperateur: pvit.ProprietaireMarchand,
			wantFrais:          "MERCHANT",
			wantFraisOperateur: "MERCHANT",
		},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()
			client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule(), c.options...)
			b.programmer("rest", reponse{statut: http.StatusOK, corps: corpsPaiement})

			demande := demandeType()
			demande.Frais = c.surcharge
			demande.FraisOperateur = c.surchargeOperateur
			if _, err := client.Payer(t.Context(), demande); err != nil {
				t.Fatalf("Payer() err = %v, want nil", err)
			}

			corps := decoder(t, b.derniere(t, "rest").Corps)
			if got := corps["owner_charge"]; got != c.wantFrais {
				t.Errorf("owner_charge = %v, want %q", got, c.wantFrais)
			}
			if got := corps["owner_charge_operator"]; got != c.wantFraisOperateur {
				t.Errorf("owner_charge_operator = %v, want %q", got, c.wantFraisOperateur)
			}
		})
	}
}

// Le compte d'opération se déduit de l'opérateur, et c'est la garde contre le code 3017 :
// envoyer le compte d'Airtel sur un numéro Moov est refusé au comptoir.
func TestLeCompteDOperationSeDeduitDeLOperateur(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom       string
		operateur pvit.Operateur
		impose    string
		want      string
	}{
		{nom: "airtel", operateur: pvit.OperateurAirtel, want: compteAirtel},
		{nom: "moov", operateur: pvit.OperateurMoov, want: compteMoov},
		{nom: "un compte impose gagne", operateur: pvit.OperateurAirtel, impose: "ACC_9AUTRE", want: "ACC_9AUTRE"},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()
			r := reglagesComplets()
			r.Comptes = map[pvit.Operateur]pvit.Compte{
				pvit.OperateurAirtel: {CodeOperation: compteAirtel, CleAmorce: cleAmorce},
				pvit.OperateurMoov:   {CodeOperation: compteMoov, CleAmorce: cleAmorce},
			}
			client, b := nouveauBancAvecReglages(t, r)
			b.programmer("rest", reponse{statut: http.StatusOK, corps: corpsPaiement})

			demande := demandeType()
			demande.Operateur = c.operateur
			demande.CompteOperation = c.impose
			if _, err := client.Payer(t.Context(), demande); err != nil {
				t.Fatalf("Payer() err = %v, want nil", err)
			}

			corps := decoder(t, b.derniere(t, "rest").Corps)
			if got := corps["merchant_operation_account_code"]; got != c.want {
				t.Errorf("merchant_operation_account_code = %v, want %q", got, c.want)
			}
			if got := corps["operator_code"]; got != string(c.operateur) {
				t.Errorf("operator_code = %v, want %q", got, c.operateur)
			}
		})
	}
}
