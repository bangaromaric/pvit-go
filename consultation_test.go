package pvit_test

import (
	"errors"
	"net/http"
	"net/url"
	"testing"

	pvit "github.com/bangaromaric/pvit-go"
	"github.com/google/go-cmp/cmp"
)

// query relit les paramètres d'une requête vue.
func query(t *testing.T, brut string) url.Values {
	t.Helper()
	valeurs, err := url.ParseQuery(brut)
	if err != nil {
		t.Fatalf("url.ParseQuery(%q) err = %v, want nil", brut, err)
	}
	return valeurs
}

// L'interrogation de statut est le MÉCANISME DE VÉRITÉ, pas un filet : le callback n'offre
// aucune garantie de remise. Elle n'accepte que l'identifiant de la passerelle, et son
// chemin n'est pas versionné.
func TestLInterrogationDeStatutEnvoieLesTroisParametres(t *testing.T) {
	t.Parallel()

	client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule())
	b.programmer("status", reponse{statut: http.StatusOK, corps: `{
		"transaction_id":"PAY23102400001",
		"reference":"REF1234567890",
		"status":"SUCCESS",
		"amount":150,
		"fees":3.5
	}`})

	etat, err := client.Statut(t.Context(), pvit.DemandeStatut{
		TransactionID: "PAY23102400001",
		Operateur:     pvit.OperateurAirtel,
	})
	if err != nil {
		t.Fatalf("Statut() err = %v, want nil", err)
	}

	want := pvit.EtatTransaction{
		TransactionID: "PAY23102400001",
		Reference:     "REF1234567890",
		Statut:        pvit.StatutSucces,
		Montant:       150,
		Frais:         3500,
		Operation:     pvit.OperationPaiement,
	}
	if diff := cmp.Diff(want, etat); diff != "" {
		t.Errorf("Statut() a rendu un diff (-want +got):\n%s", diff)
	}

	vue := b.derniere(t, "status")
	if got, want := vue.Chemin, "/"+codeStatut+"/status"; got != want {
		t.Errorf("Statut() a appele %q, want %q sans prefixe de version", got, want)
	}
	if got, want := vue.Methode, http.MethodGet; got != want {
		t.Errorf("Statut() methode = %q, want %q", got, want)
	}

	params := query(t, vue.Query)
	attendus := map[string]string{
		"transactionId":        "PAY23102400001",
		"accountOperationCode": compteAirtel,
		"transactionOperation": "PAYMENT",
	}
	for nom, want := range attendus {
		if got := params.Get(nom); got != want {
			t.Errorf("Statut() parametre %s = %q, want %q", nom, got, want)
		}
	}
	if vue.Secret == "" {
		t.Errorf("Statut() n a pas porte de cle secrete, want celle du compte")
	}
}

func TestLInterrogationDeStatutRefuseUneDemandeIncomplete(t *testing.T) {
	t.Parallel()

	client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule())

	cas := []struct {
		nom       string
		demande   pvit.DemandeStatut
		wantChamp string
	}{
		{
			nom:       "sans identifiant",
			demande:   pvit.DemandeStatut{Operateur: pvit.OperateurAirtel},
			wantChamp: "transactionId",
		},
		{
			nom:       "operation inconnue",
			demande:   pvit.DemandeStatut{TransactionID: "PAY1", Operateur: pvit.OperateurAirtel, Operation: "REFUND"},
			wantChamp: "transactionOperation",
		},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()
			_, err := client.Statut(t.Context(), c.demande)
			var champ pvit.ChampInvalideError
			if !errors.As(err, &champ) {
				t.Fatalf("Statut() err = %v, want une ChampInvalideError", err)
			}
			if champ.Champ != c.wantChamp {
				t.Errorf("Statut() champ fautif = %q, want %q", champ.Champ, c.wantChamp)
			}
		})
	}
	if got := len(b.vues()); got != 0 {
		t.Errorf("Statut() a emis %d requetes sur des demandes invalides, want 0", got)
	}
}

// Le KYC n'envoie PAS de clé secrète, et ne déclenche donc aucune rotation : il fonctionne
// avec un client qui n'a aucun compte configuré. La passerelle en fait par ailleurs une
// condition d'ouverture du compte de production.
func TestLeKYCFonctionneSansAucunCompteConfigure(t *testing.T) {
	t.Parallel()

	r := reglagesComplets()
	// Un compte reste exige au demarrage, mais celui-ci n'a pas de mot de passe : si le
	// KYC demandait une cle, il echouerait.
	r.Comptes = map[pvit.Operateur]pvit.Compte{
		pvit.OperateurAirtel: {CodeOperation: compteAirtel, CleAmorce: cleAmorce},
	}
	client, b := nouveauBancAvecReglages(t, r)
	b.programmer("kyc", reponse{statut: http.StatusOK, corps: `{"data":{"firstname":"John Doe","is_active":true}}`})

	rep, err := client.KYC(t.Context(), "074111111", pvit.OperateurMoov)
	if err != nil {
		t.Fatalf("KYC() err = %v, want nil", err)
	}

	vue := b.derniere(t, "kyc")
	if vue.Secret != "" {
		t.Errorf("KYC() a porte la cle secrete %q, want aucune", vue.Secret)
	}
	if got := b.comptePour("renew-secret"); got != 0 {
		t.Errorf("KYC() a declenche %d renouvellements, want 0", got)
	}
	if got, want := vue.Chemin, "/v2/"+codeKYC+"/kyc"; got != want {
		t.Errorf("KYC() a appele %q, want %q", got, want)
	}

	params := query(t, vue.Query)
	if got, want := params.Get("customerAccountNumber"), "074111111"; got != want {
		t.Errorf("KYC() customerAccountNumber = %q, want %q", got, want)
	}
	if got, want := params.Get("operatorCode"), string(pvit.OperateurMoov); got != want {
		t.Errorf("KYC() operatorCode = %q, want %q", got, want)
	}

	// L'enveloppe de données est aplatie : la version deux de cette API emballe, la
	// version un mettait tout à plat.
	if got, ok := rep.Texte("firstname"); !ok || got != "John Doe" {
		t.Errorf("ReponseKYC.Texte(firstname) = %q, %v, want %q, true", got, ok, "John Doe")
	}
	actif, connu := rep.Actif()
	if !connu || !actif {
		t.Errorf("ReponseKYC.Actif() = %v, %v, want true, true", actif, connu)
	}
}

// Le second retour de Actif est le plus important : faux veut dire « la passerelle ne l'a
// pas dit », ce qui n'est PAS « le compte est inactif ». Engager une transaction sur cette
// confusion irait au devant d'un refus pour compte bloqué ou inconnu.
func TestActifDistingueLInconnuDuFaux(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom       string
		corps     string
		wantActif bool
		wantConnu bool
	}{
		{nom: "booleen vrai", corps: `{"is_active":true}`, wantActif: true, wantConnu: true},
		{nom: "booleen faux", corps: `{"is_active":false}`, wantActif: false, wantConnu: true},
		{nom: "casse melee", corps: `{"isActive":true}`, wantActif: true, wantConnu: true},
		{nom: "nom court", corps: `{"active":true}`, wantActif: true, wantConnu: true},
		{nom: "chaine vraie", corps: `{"is_active":"true"}`, wantActif: true, wantConnu: true},
		{nom: "chaine fausse", corps: `{"is_active":"false"}`, wantActif: false, wantConnu: true},
		{nom: "absent, donc inconnu", corps: `{"firstname":"John"}`, wantActif: false, wantConnu: false},
		{nom: "illisible, donc inconnu", corps: `{"is_active":{"x":1}}`, wantActif: false, wantConnu: false},
		{nom: "la version deux, sans is_active", corps: `{"data":{"firstname":"John Doe"}}`, wantActif: false, wantConnu: false},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()
			client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule())
			b.programmer("kyc", reponse{statut: http.StatusOK, corps: c.corps})

			rep, err := client.KYC(t.Context(), "074111111", pvit.OperateurAirtel)
			if err != nil {
				t.Fatalf("KYC() err = %v, want nil", err)
			}
			actif, connu := rep.Actif()
			if actif != c.wantActif || connu != c.wantConnu {
				t.Errorf("Actif() sur %s = %v, %v, want %v, %v", c.corps, actif, connu, c.wantActif, c.wantConnu)
			}
		})
	}
}

// Le calculateur de frais est l'appel qui tranche une question de PRIX : deux appels à deux
// montants disent si une cadence d'encaissement est tenable, et le seuil minimum de chaque
// opérateur n'est publié nulle part ailleurs.
func TestLeCalculateurDeFraisEtSonTotalIncomplet(t *testing.T) {
	t.Parallel()

	client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule())
	// Les chiffres publies par la passerelle pour 150 francs sur Airtel.
	b.programmer("get-fees", reponse{statut: http.StatusOK, corps: `{"amount":150,"fees":3.5,"totalAmount":153.5}`})

	frais, err := client.FraisDe(t.Context(), 150, pvit.OperationPaiement, pvit.OperateurAirtel)
	if err != nil {
		t.Fatalf("FraisDe() err = %v, want nil", err)
	}

	want := pvit.ReponseFrais{Montant: 150, Frais: 3500, TotalAnnonce: 153500}
	if diff := cmp.Diff(want, frais); diff != "" {
		t.Errorf("FraisDe() a rendu un diff (-want +got):\n%s", diff)
	}

	// LE PIÈGE : ce total ignore les frais de l'opérateur. La notification du même
	// paiement annonce 155, pas 153,5. Le paquet ne calcule donc aucun total lui-même.
	if got, want := frais.TotalAnnonce.String(), "153,5 XAF"; got != want {
		t.Errorf("TotalAnnonce = %q, want %q", got, want)
	}

	params := query(t, b.derniere(t, "get-fees").Query)
	attendus := map[string]string{
		"accountOperationCode": compteAirtel,
		"transactionType":      "PAYMENT",
		"amount":               "150",
		"operatorCode":         "AIRTEL_MONEY",
	}
	for nom, want := range attendus {
		if got := params.Get(nom); got != want {
			t.Errorf("FraisDe() parametre %s = %q, want %q", nom, got, want)
		}
	}
}

func TestLeCalculateurDeFraisRefuseUnMontantNul(t *testing.T) {
	t.Parallel()

	client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule())

	_, err := client.FraisDe(t.Context(), 0, pvit.OperationPaiement, pvit.OperateurAirtel)
	if !errors.Is(err, pvit.ErrMontantInvalide) {
		t.Errorf("FraisDe(0) err = %v, want ErrMontantInvalide", err)
	}
	if got := len(b.vues()); got != 0 {
		t.Errorf("FraisDe(0) a emis %d requetes, want 0", got)
	}
}

func TestLeSoldeSeLitSousPlusieursNoms(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom   string
		corps string
		want  pvit.Montant
	}{
		{nom: "balance", corps: `{"balance":125000}`, want: 125000},
		{nom: "sous enveloppe de donnees", corps: `{"data":{"balance":4500}}`, want: 4500},
		{nom: "amount", corps: `{"amount":"999"}`, want: 999},
		{nom: "available_balance", corps: `{"available_balance":1}`, want: 1},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()
			client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule())
			b.programmer("balance", reponse{statut: http.StatusOK, corps: c.corps})

			solde, err := client.Solde(t.Context(), pvit.OperateurAirtel)
			if err != nil {
				t.Fatalf("Solde() err = %v, want nil", err)
			}
			if solde.Solde != c.want {
				t.Errorf("Solde() = %d, want %d", solde.Solde, c.want)
			}
			if got, want := solde.CompteOperation, compteAirtel; got != want {
				t.Errorf("Solde() compte = %q, want %q", got, want)
			}
			if got, want := b.derniere(t, "balance").Chemin, "/"+codeSolde+"/balance"; got != want {
				t.Errorf("Solde() a appele %q, want %q sans prefixe de version", got, want)
			}
		})
	}
}

func TestUnSoldeIllisibleEstUneErreur(t *testing.T) {
	t.Parallel()

	client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule())
	b.programmer("balance", reponse{statut: http.StatusOK, corps: `{"message":"ok"}`})

	_, err := client.Solde(t.Context(), pvit.OperateurAirtel)
	if !errors.Is(err, pvit.ErrReponseIllisible) {
		t.Errorf("Solde() sur une reponse sans solde err = %v, want ErrReponseIllisible", err)
	}
}

// La liste des opérateurs est L'AUTORITÉ sur la correspondance entre un numéro et un
// opérateur. Son paramètre de pays s'écrit en tirets là où tout le reste de l'API est en
// casse mêlée : ce n'est pas une coquille.
func TestLaListeDesOperateursEtSonParametreEnTirets(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom   string
		corps string
	}{
		{nom: "tableau nu", corps: `[{"operator_code":"AIRTEL_MONEY","name":"Airtel Money","country_code":"GA"}]`},
		{nom: "sous enveloppe de donnees", corps: `{"data":[{"operatorCode":"AIRTEL_MONEY","label":"Airtel Money","country":"GA"}]}`},
		{nom: "sous la cle operators", corps: `{"operators":[{"code":"AIRTEL_MONEY","nom":"Airtel Money","pays":"GA"}]}`},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()
			client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule())
			b.programmer("get-operators", reponse{statut: http.StatusOK, corps: c.corps})

			liste, err := client.Operateurs(t.Context(), "ga")
			if err != nil {
				t.Fatalf("Operateurs() err = %v, want nil", err)
			}
			want := []pvit.OperateurPays{{Code: pvit.OperateurAirtel, Nom: "Airtel Money", Pays: "GA"}}
			if diff := cmp.Diff(want, liste); diff != "" {
				t.Errorf("Operateurs() a rendu un diff (-want +got):\n%s", diff)
			}

			vue := b.derniere(t, "get-operators")
			if vue.Secret != "" {
				t.Errorf("Operateurs() a porte la cle secrete %q, want aucune", vue.Secret)
			}
			params := query(t, vue.Query)
			if got, want := params.Get("country-code"), "GA"; got != want {
				t.Errorf("Operateurs() parametre country-code = %q, want %q (en tirets, pas en casse melee)", got, want)
			}
			if params.Get("countryCode") != "" {
				t.Errorf("Operateurs() a emis countryCode, want country-code")
			}
		})
	}
}

func TestLaListeDesPays(t *testing.T) {
	t.Parallel()

	client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule())
	b.programmer("get-countries", reponse{statut: http.StatusOK, corps: `{"data":[
		{"country_code":"GA","name":"Gabon","dial_code":"+241"},
		{"country_code":"CM","name":"Cameroun","dial_code":"+237"}
	]}`})

	liste, err := client.Pays(t.Context())
	if err != nil {
		t.Fatalf("Pays() err = %v, want nil", err)
	}

	want := []pvit.Pays{
		{Code: "GA", Nom: "Gabon", Indicatif: "+241"},
		{Code: "CM", Nom: "Cameroun", Indicatif: "+237"},
	}
	if diff := cmp.Diff(want, liste); diff != "" {
		t.Errorf("Pays() a rendu un diff (-want +got):\n%s", diff)
	}
	if got := b.derniere(t, "get-countries").Secret; got != "" {
		t.Errorf("Pays() a porte la cle secrete %q, want aucune", got)
	}
}

func TestUneListeIllisibleEstUneErreur(t *testing.T) {
	t.Parallel()

	client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule())
	b.programmer("get-countries", reponse{statut: http.StatusOK, corps: `{"message":"rien a lister"}`})

	_, err := client.Pays(t.Context())
	if !errors.Is(err, pvit.ErrReponseIllisible) {
		t.Errorf("Pays() sur une reponse sans liste err = %v, want ErrReponseIllisible", err)
	}
}

// Un montant hors des bornes de l'opérateur est la découverte la plus probable des premiers
// essais, puisque ces seuils ne sont publiés nulle part.
func TestUnMontantHorsBornesEstReconnu(t *testing.T) {
	t.Parallel()

	client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule())
	b.programmer("rest", reponse{statut: http.StatusForbidden, corps: `{
		"date":"20102026-080000",
		"status_code":3018,
		"error":"TRANSACTION_AMOUNT_TOO_LOW",
		"message":"le montant est inferieur au minimum de l operateur"
	}`})

	_, err := client.Payer(t.Context(), demandeType())

	if !errors.Is(err, pvit.ErrHorsBornes) {
		t.Errorf("Payer() err = %v, want ErrHorsBornes", err)
	}
	var refus pvit.Erreur
	if !errors.As(err, &refus) {
		t.Fatalf("Payer() err = %v, want une pvit.Erreur", err)
	}
	if !refus.EstHorsBornes() {
		t.Errorf("Erreur.EstHorsBornes() = false, want true")
	}
	if refus.EstRejouable() {
		t.Errorf("Erreur.EstRejouable() = true sur un montant hors bornes, want false")
	}
	if got, want := refus.Nom, "TRANSACTION_AMOUNT_TOO_LOW"; got != want {
		t.Errorf("Erreur.Nom = %q, want %q", got, want)
	}
	// Le refus ne se rejoue pas : il echouerait a l'identique.
	if got, want := b.comptePour("rest"), 1; got != want {
		t.Errorf("un montant hors bornes a declenche %d appels, want %d", got, want)
	}
}

// L'opérateur est exigé MÊME quand le compte d'opération est imposé, parce qu'il ne choisit
// pas seulement le compte : il choisit aussi la clé secrète à envoyer. Avant ce correctif,
// l'appel partait pour échouer plus loin sur un message que personne ne savait lire.
func TestLOperateurEstExigeMemeQuandLeCompteEstImpose(t *testing.T) {
	t.Parallel()

	client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule())

	appels := []struct {
		nom     string
		appeler func() error
	}{
		{
			nom: "statut",
			appeler: func() error {
				_, err := client.Statut(t.Context(), pvit.DemandeStatut{
					TransactionID:   "PAY1",
					CompteOperation: compteAirtel, // compte impose, mais aucun operateur
				})
				return err
			},
		},
		{
			nom: "frais",
			appeler: func() error {
				_, err := client.FraisDe(t.Context(), 150, pvit.OperationPaiement, "")
				return err
			},
		},
		{
			nom: "solde",
			appeler: func() error {
				_, err := client.Solde(t.Context(), "")
				return err
			},
		},
		{
			nom: "remboursement",
			appeler: func() error {
				_, err := client.Rembourser(t.Context(), pvit.DemandeRemboursement{
					Reference:       "REF1234567890",
					Montant:         150,
					NumeroClient:    "074111111",
					CompteOperation: compteAirtel,
				})
				return err
			},
		},
	}

	for _, a := range appels {
		t.Run(a.nom, func(t *testing.T) {
			t.Parallel()
			err := a.appeler()

			var champ pvit.ChampInvalideError
			if !errors.As(err, &champ) {
				t.Fatalf("%s() err = %v, want une ChampInvalideError", a.nom, err)
			}
			if got, want := champ.Champ, "operator_code"; got != want {
				t.Errorf("%s() champ fautif = %q, want %q", a.nom, got, want)
			}
			if !errors.Is(err, pvit.ErrRequeteInvalide) {
				t.Errorf("%s() err = %v, want ErrRequeteInvalide", a.nom, err)
			}
		})
	}

	if got := len(b.vues()); got != 0 {
		t.Errorf("les appels sans operateur ont emis %d requetes, want 0", got)
	}
}

// Une somme illisible sur une réponse de statut rendait zéro en silence : l'appelant
// aurait crédité zéro franc sur une transaction que la passerelle annonce réussie.
func TestUneSommeIllisibleDansUnStatutEstUneErreur(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom     string
		corps   string
		wantErr error
		wantMnt pvit.Montant
	}{
		{
			nom:     "montant a demi franc",
			corps:   `{"status":"SUCCESS","amount":150.5}`,
			wantErr: pvit.ErrMontantFractionnaire,
		},
		{
			nom:     "frais illisibles",
			corps:   `{"status":"SUCCESS","amount":150,"fees":"beaucoup"}`,
			wantErr: pvit.ErrReponseIllisible,
		},
		{
			nom:     "montant absent, ce qui est legitime",
			corps:   `{"status":"SUCCESS"}`,
			wantMnt: 0,
		},
		{
			nom:     "chemin nominal",
			corps:   `{"status":"SUCCESS","amount":150,"fees":3.5}`,
			wantMnt: 150,
		},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()
			client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule())
			b.programmer("status", reponse{statut: http.StatusOK, corps: c.corps})

			etat, err := client.Statut(t.Context(), pvit.DemandeStatut{
				TransactionID: "PAY1",
				Operateur:     pvit.OperateurAirtel,
			})
			if c.wantErr != nil {
				if !errors.Is(err, c.wantErr) {
					t.Errorf("Statut() sur %s err = %v, want %v", c.corps, err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Statut() sur %s err = %v, want nil", c.corps, err)
			}
			if etat.Montant != c.wantMnt {
				t.Errorf("Statut() Montant = %d, want %d", etat.Montant, c.wantMnt)
			}
		})
	}
}

// Même règle sur le calculateur de frais : un barème illisible ne doit pas se lire comme un
// barème nul, qui ferait croire l'encaissement gratuit.
func TestUnBaremeIllisibleEstUneErreur(t *testing.T) {
	t.Parallel()

	client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule())
	b.programmer("get-fees", reponse{statut: http.StatusOK, corps: `{"amount":150,"fees":"gratuit"}`})

	_, err := client.FraisDe(t.Context(), 150, pvit.OperationPaiement, pvit.OperateurAirtel)
	if !errors.Is(err, pvit.ErrReponseIllisible) {
		t.Errorf("FraisDe() sur un bareme illisible err = %v, want ErrReponseIllisible", err)
	}
}
