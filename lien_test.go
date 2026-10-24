package pvit_test

import (
	"errors"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	pvit "github.com/bangaromaric/pvit-go"
	"github.com/google/go-cmp/cmp"
)

// LE TEST LE PLUS RENTABLE DU LIEN.
//
// Il compare la charge utile émise à celle que la passerelle publie, champ par champ et en
// une fois, et il épingle du même coup les trois faits de ce point d'entrée qui ne se
// devinent pas : le chemin n'est PAS versionné, il porte pourtant la clé secrète, et son
// corps n'a AUCUN champ operator_code alors que l'opérateur était renseigné.
func TestLaChargeUtileDuLienEstCelleQuePublieLaPasserelle(t *testing.T) {
	t.Parallel()

	client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule())
	b.programmer("link", reponse{statut: http.StatusOK, corps: corpsLien})

	d := demandeLienType()
	d.Agent = "AGENT123"
	d.Produit = "PRODUCT123"
	d.InfoLibre = "Free Info"

	if _, err := client.CreerLien(t.Context(), d); err != nil {
		t.Fatalf("CreerLien() err = %v, want nil", err)
	}

	vue := b.derniere(t, "link")

	// Le chemin, sans préfixe de version, et la clé secrète malgré cette absence.
	if got, want := vue.Chemin, "/"+codeLien+"/link"; got != want {
		t.Errorf("CreerLien() a appele %q, want %q: le lien n est PAS versionne", got, want)
	}
	if vue.Secret == "" {
		t.Errorf("CreerLien() n a porte aucune cle secrete, want celle du compte: un chemin non versionne en exige une quand meme")
	}
	if got, want := vue.Methode, http.MethodPost; got != want {
		t.Errorf("CreerLien() methode = %q, want %q", got, want)
	}

	// La charge utile, en entier.
	want := map[string]any{
		"agent":                           "AGENT123",
		"amount":                          float64(150),
		"product":                         "PRODUCT123",
		"reference":                       "REF1234567890",
		"service":                         "WEB",
		"callback_url_code":               codeCallback,
		"customer_account_number":         "074111111",
		"merchant_operation_account_code": compteAirtel,
		"transaction_type":                "PAYMENT",
		"owner_charge":                    "CUSTOMER",
		"owner_charge_operator":           "CUSTOMER",
		"free_info":                       "Free Info",
		"success_redirection_url_code":    codeRedirectionSucces,
		"failed_redirection_url_code":     codeRedirectionEchec,
	}
	if diff := cmp.Diff(want, decoder(t, vue.Corps)); diff != "" {
		t.Errorf("CreerLien() charge utile, -want +got:\n%s", diff)
	}

	// Le montant part en entier NU, et non en flottant, malgré un schéma qui le type
	// double : la devise n'a pas de subdivision, et 150.0 dans un journal de la passerelle
	// est une question de plus à poser au support.
	if !strings.Contains(vue.Corps, `"amount":150`) {
		t.Errorf("CreerLien() corps = %s, want un montant entier nu", vue.Corps)
	}
}

// Le corps du lien n'a AUCUN champ operator_code, et c'est le fait le plus surprenant de ce
// point d'entrée.
//
// L'opérateur est pourtant exigé côté client, parce qu'il choisit le compte d'opération ET
// la clé secrète. Ce test prouve les deux moitiés : le champ ne part pas, et le compte émis
// est bien celui que l'opérateur désigne. Un copier-coller depuis la structure du paiement
// ferait partir un champ que la passerelle ne connaît pas sur ce chemin.
func TestLeCorpsDuLienNePorteAucunCodeOperateur(t *testing.T) {
	t.Parallel()

	// Les deux comptes, pour que l'opérateur ait quelque chose à choisir.
	client, b := nouveauBanc(t)
	b.programmer("renew-secret", reponse{statut: http.StatusOK, corps: corpsRenouvellement})
	b.programmer("link", reponse{statut: http.StatusOK, corps: corpsLien})

	d := demandeLienType()
	d.Operateur = pvit.OperateurMoov

	if _, err := client.CreerLien(t.Context(), d); err != nil {
		t.Fatalf("CreerLien() err = %v, want nil", err)
	}

	corps := decoder(t, b.derniere(t, "link").Corps)
	if _, present := corps["operator_code"]; present {
		t.Errorf("CreerLien() corps porte operator_code, want absent: le lien n a pas ce champ")
	}
	if got, want := corps["merchant_operation_account_code"], compteMoov; got != want {
		t.Errorf("CreerLien() compte = %v, want %q: l operateur choisit le compte sans partir sur le fil", got, want)
	}
}

// Le numéro du client est CONDITIONNEL, et le test couvre les deux sens.
//
// Pour le parcours web il est facultatif, et son absence doit être une ABSENCE DE CHAMP et
// non un champ vide, que la validation du serveur pourrait refuser. Pour la carte et le
// lien direct il est exigé, et le refus doit tomber AVANT toute requête.
func TestLeNumeroClientEstConditionnelAuServiceDuLien(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom          string
		service      pvit.Service
		numero       string
		wantErreur   bool
		wantPresence bool
	}{
		{nom: "web sans numero: champ absent", service: pvit.ServiceWeb},
		{nom: "web avec numero: champ present", service: pvit.ServiceWeb, numero: "074111111", wantPresence: true},
		{nom: "carte sans numero: refuse", service: pvit.ServiceVisaMastercard, wantErreur: true},
		{nom: "carte avec numero: accepte", service: pvit.ServiceVisaMastercard, numero: "074111111", wantPresence: true},
		{nom: "lien direct sans numero: refuse", service: pvit.ServiceRestlink, wantErreur: true},
		{nom: "lien direct avec numero: accepte", service: pvit.ServiceRestlink, numero: "074111111", wantPresence: true},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()

			client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule())
			b.programmer("link", reponse{statut: http.StatusOK, corps: corpsLien})

			d := demandeLienType()
			d.Service, d.NumeroClient = c.service, c.numero

			_, err := client.CreerLien(t.Context(), d)

			if c.wantErreur {
				if !errors.Is(err, pvit.ErrRequeteInvalide) {
					t.Fatalf("CreerLien() err = %v, want ErrRequeteInvalide", err)
				}
				champ, _ := errors.AsType[pvit.ChampInvalideError](err)
				if champ.Champ != "customer_account_number" {
					t.Errorf("CreerLien() err = %v, want une faute sur customer_account_number", err)
				}
				if got := b.comptePour("link"); got != 0 {
					t.Errorf("CreerLien() a emis %d requetes sur une demande invalide, want 0", got)
				}
				return
			}

			if err != nil {
				t.Fatalf("CreerLien() err = %v, want nil", err)
			}
			corps := decoder(t, b.derniere(t, "link").Corps)
			_, present := corps["customer_account_number"]
			if present != c.wantPresence {
				t.Errorf("CreerLien() customer_account_number present = %v, want %v: une absence doit etre une absence de champ, pas un champ vide",
					present, c.wantPresence)
			}
		})
	}
}

// RESTFUL est le défaut de [pvit.AvecService], et le seul des quatre services que l'API de
// lien n'accepte pas. C'est la faute la plus probable de ce chemin.
//
// Le refus est LOCAL, nommé, et sans requête : la passerelle le sanctionnerait d'un code
// qui ne dirait pas que le parcours demandé appartient à un autre point d'entrée.
func TestUnServiceAbsentOuEtrangerAuLienEstRefuseAvantLAppel(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom     string
		service pvit.Service
	}{
		{nom: "absent", service: ""},
		{nom: "restful appartient a l api de transaction", service: pvit.ServiceRestful},
		{nom: "inconnu", service: "QUELQUE_CHOSE_DE_NEUF"},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()

			client, b := nouveauBanc(t)
			d := demandeLienType()
			d.Service = c.service

			_, err := client.CreerLien(t.Context(), d)
			if !errors.Is(err, pvit.ErrRequeteInvalide) {
				t.Fatalf("CreerLien(service %q) err = %v, want ErrRequeteInvalide", c.service, err)
			}
			champ, _ := errors.AsType[pvit.ChampInvalideError](err)
			if champ.Champ != "service" {
				t.Errorf("CreerLien(service %q) err = %v, want une faute sur service", c.service, err)
			}
			if !contientTout(err.Error(), "WEB", "VISA_MASTERCARD", "RESTLINK") {
				t.Errorf("CreerLien(service %q) err = %v, want un message nommant les trois services attendus", c.service, err)
			}
			if got := b.comptePour("link"); got != 0 {
				t.Errorf("CreerLien(service %q) a emis %d requetes, want 0", c.service, got)
			}
		})
	}
}

// Le service du lien ne vient JAMAIS de l'option du client, et ce test en est la garde.
//
// Le défaut de cette option est RESTFUL, que ce point d'entrée refuse ; et un client réglé
// sur VISA_MASTERCARD ne doit pas transformer en lien carte un lien web demandé
// explicitement. Dans les deux sens, c'est la demande qui décide.
func TestLeServiceDuLienNeVientPasDeLOptionDuClient(t *testing.T) {
	t.Parallel()

	for _, option := range []pvit.Service{pvit.ServiceRestful, pvit.ServiceVisaMastercard} {
		t.Run(string(option), func(t *testing.T) {
			t.Parallel()

			client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule(), pvit.AvecService(option))
			b.programmer("link", reponse{statut: http.StatusOK, corps: corpsLien})

			if _, err := client.CreerLien(t.Context(), demandeLienType()); err != nil {
				t.Fatalf("CreerLien() err = %v, want nil", err)
			}

			got := decoder(t, b.derniere(t, "link").Corps)["service"]
			if want := "WEB"; got != want {
				t.Errorf("AvecService(%q) puis CreerLien(Service: WEB) a emis service = %v, want %q", option, got, want)
			}
		})
	}
}

// L'accusé du lien est en CASSE MÊLÉE avec un code ENTIER, là où celui de la transaction est
// en soulignés avec une CHAÎNE. Les deux écritures sont lues.
//
// Ce n'est pas de la prudence décorative : un décodeur qui ne déclarerait qu'un seul nom
// échouerait ENTIÈREMENT sur une variation de forme, et l'appelant perdrait la référence qui
// est le seul moyen de réconcilier un lien réellement créé.
func TestLAccuseDuLienSeLitEnCasseMeleeCommeEnSoulignes(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom        string
		corps      string
		wantURL    string
		wantCode   int
		wantStatut pvit.Statut
	}{
		{
			nom:        "la forme publiee: casse melee et code entier",
			corps:      corpsLien,
			wantURL:    "https://checkout.mypvit.pro/PAY23102400001",
			wantCode:   200,
			wantStatut: pvit.StatutSucces,
		},
		{
			nom:        "la forme de l api de transaction: soulignes et code en chaine",
			corps:      `{"url":"https://pay.test/abc","merchant_reference_id":"REF1234567890","status":"SUCCESS","status_code":"200"}`,
			wantURL:    "https://pay.test/abc",
			wantCode:   200,
			wantStatut: pvit.StatutSucces,
		},
		{
			nom:        "un code entier ecrit en chaine",
			corps:      `{"url":"https://pay.test/abc","statusCode":"200"}`,
			wantURL:    "https://pay.test/abc",
			wantCode:   200,
			wantStatut: "",
		},
		{
			nom:        "sans code ni statut: l adresse suffit",
			corps:      `{"url":"https://pay.test/abc"}`,
			wantURL:    "https://pay.test/abc",
			wantCode:   0,
			wantStatut: "",
		},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()

			client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule())
			b.programmer("link", reponse{statut: http.StatusOK, corps: c.corps})

			lien, err := client.CreerLien(t.Context(), demandeLienType())
			if err != nil {
				t.Fatalf("CreerLien() err = %v, want nil", err)
			}
			if got := lien.URL; got != c.wantURL {
				t.Errorf("CreerLien() URL = %q, want %q", got, c.wantURL)
			}
			if got := lien.Code; got != c.wantCode {
				t.Errorf("CreerLien() Code = %d, want %d", got, c.wantCode)
			}
			if got := lien.StatutCreation; got != c.wantStatut {
				t.Errorf("CreerLien() StatutCreation = %q, want %q", got, c.wantStatut)
			}
			// LA RÉFÉRENCE SURVIT À TOUTE FORME : c'est elle qui réconcilie, et elle vient
			// de la demande, jamais de la réponse.
			if got, want := lien.Reference, pvit.Reference("REF1234567890"); got != want {
				t.Errorf("CreerLien() Reference = %q, want %q", got, want)
			}
			if !lien.Utilisable() {
				t.Errorf("CreerLien() Utilisable() = false, want true")
			}
		})
	}
}

// LA FAUTE QUI COÛTERAIT DE L'ARGENT : la passerelle répond SUCCESS à un lien qu'elle vient
// de fabriquer, et ce SUCCESS ne concerne PAS le paiement.
//
// Le champ s'appelle donc [pvit.Lien.StatutCreation] et non Statut, pour qu'il ne se lise
// jamais comme « le paiement a réussi ». Ce test épingle la distinction du côté de la
// conduite : la transaction est encore en attente, elle doit être interrogée, et elle a le
// droit de finir en échec.
func TestUnLienCreeNeDitPasQuUnPaiementAEuLieu(t *testing.T) {
	t.Parallel()

	client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule())
	b.programmer("link", reponse{statut: http.StatusOK, corps: corpsLien})

	lien, err := client.CreerLien(t.Context(), demandeLienType())
	if err != nil {
		t.Fatalf("CreerLien() err = %v, want nil", err)
	}

	// L'accusé dit SUCCESS, et il ne parle que de la création du lien.
	if got, want := lien.StatutCreation, pvit.StatutSucces; got != want {
		t.Fatalf("CreerLien() StatutCreation = %q, want %q", got, want)
	}

	// La transaction, elle, est en attente, et tout le reste en découle.
	creation := b.horloge.maintenant()
	if !pvit.AInterroger(pvit.StatutEnAttente, creation, creation.Add(pvit.DelaiIncertitude)) {
		t.Errorf("AInterroger(attente) = false, want true: un lien cree n est pas un paiement acquis")
	}
	if !pvit.TransitionPermise(pvit.StatutEnAttente, pvit.StatutEchec) {
		t.Errorf("TransitionPermise(attente, echec) = false, want true: un lien cree peut tres bien finir en echec")
	}
}

// Une réponse sans adresse présentable est un ÉCHEC, malgré le code de succès.
//
// C'est l'asymétrie assumée avec l'accusé de paiement, qui reste utilisable sans
// identifiant : un paiement est réellement engagé et se réconcilie par sa référence, tandis
// qu'un lien sans adresse n'a rien à montrer au client, donc il ne se passera jamais rien.
//
// Les schémas inattendus sont refusés pour une seconde raison, et elle est de sécurité :
// cette adresse finit dans un en-tête de redirection chez l'appelant, et un javascript
// recopié là ouvrirait une redirection ouverte.
func TestUnAccuseDeLienSansAdressePresentableEchoue(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom   string
		corps string
	}{
		{nom: "accuse vide", corps: `{}`},
		{nom: "adresse vide", corps: `{"url":"","statusCode":200}`},
		{nom: "adresse nulle", corps: `{"url":null,"statusCode":200}`},
		{nom: "adresse relative", corps: `{"url":"/checkout/PAY1","statusCode":200}`},
		{nom: "adresse sans schema", corps: `{"url":"checkout.mypvit.pro/PAY1","statusCode":200}`},
		{nom: "schema javascript", corps: `{"url":"javascript:alert(1)","statusCode":200}`},
		{nom: "schema de donnees", corps: `{"url":"data:text/html,<h1>paye</h1>","statusCode":200}`},
		{nom: "schema http mais sans hote", corps: `{"url":"https:///PAY1","statusCode":200}`},
		{nom: "hote maquille par des identifiants", corps: `{"url":"https://checkout.mypvit.pro@ailleurs.test/PAY1","statusCode":200}`},
		{nom: "corps illisible", corps: `pas du json`},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()

			client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule())
			b.programmer("link", reponse{statut: http.StatusOK, corps: c.corps})

			lien, err := client.CreerLien(t.Context(), demandeLienType())
			if !errors.Is(err, pvit.ErrReponseIllisible) {
				t.Fatalf("CreerLien(corps %q) err = %v, want ErrReponseIllisible", c.corps, err)
			}
			if lien.Utilisable() {
				t.Errorf("CreerLien(corps %q) Utilisable() = true, want false", c.corps)
			}
			// La référence revient quand même : la passerelle a pu consommer la tentative
			// avant de répondre de travers, et l'appelant doit pouvoir aller le vérifier.
			if got, want := lien.Reference, pvit.Reference("REF1234567890"); got != want {
				t.Errorf("CreerLien(corps %q) Reference = %q, want %q: la tentative reste interrogeable", c.corps, got, want)
			}
		})
	}
}

// Une tentative qui existe déjà rend LES DEUX : la référence, et l'erreur.
//
// Le danger est identique pour les deux codes, et il coûte de l'argent : régénérer un lien
// sous une référence neuve ferait payer deux fois un client qui vient peut-être d'aller au
// bout du formulaire bancaire. L'appelant interroge le statut, et pour cela il lui faut la
// référence, même dans l'échec.
func TestUneTentativeQuiExisteDejaRendLaReferenceEtLErreur(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom      string
		statut   int
		corps    string
		wantCode int
	}{
		{nom: "reference deja vue", statut: http.StatusConflict, corps: corpsDejaInitiee, wantCode: pvit.CodeReferenceDejaUtilisee},
		{nom: "lien deja consomme", statut: http.StatusConflict, corps: corpsLienConsomme, wantCode: pvit.CodeLienConsomme},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()

			client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule())
			b.programmer("link", reponse{statut: c.statut, corps: c.corps})

			lien, err := client.CreerLien(t.Context(), demandeLienType())
			if !errors.Is(err, pvit.ErrDejaInitiee) {
				t.Fatalf("CreerLien() err = %v, want ErrDejaInitiee", err)
			}
			if got, want := lien.Reference, pvit.Reference("REF1234567890"); got != want {
				t.Errorf("CreerLien() Reference = %q, want %q: sans elle, l appelant ne peut pas interroger le statut", got, want)
			}
			if lien.Utilisable() {
				t.Errorf("CreerLien() Utilisable() = true, want false: la passerelle n a rendu aucune adresse")
			}

			e, ok := errors.AsType[pvit.Erreur](err)
			if !ok {
				t.Fatalf("CreerLien() err = %v, want une pvit.Erreur", err)
			}
			if got := e.Code; got != c.wantCode {
				t.Errorf("CreerLien() Erreur.Code = %d, want %d: le diagnostic reste separable de la conduite", got, c.wantCode)
			}
			if !e.EstDejaInitiee() {
				t.Errorf("CreerLien() EstDejaInitiee() = false, want true")
			}
			if e.EstRejouable() {
				t.Errorf("CreerLien() EstRejouable() = true, want false: rejouer a l identique ne rendrait rien de neuf")
			}
			// UN SEUL appel : un 409 ne se rejoue pas.
			if got, want := b.comptePour("link"), 1; got != want {
				t.Errorf("CreerLien() a appele link %d fois, want %d", got, want)
			}
		})
	}
}

// Un lien expiré n'est PAS rejouable, et une URL de redirection éteinte est une faute de
// configuration.
//
// Les deux refus passent par la méthode typée, et non par classer en direct : c'est ce qui
// prouve que le classement de erreur.go est réellement atteint depuis la porte qui produit
// ces codes.
func TestLesRefusPropresAuLienSontBienClasses(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom                string
		statut             int
		corps              string
		wantMotif          error
		wantConfiguration  bool
		wantRejouable      bool
		wantCodeApplicatif int
	}{
		{
			nom: "lien expire", statut: http.StatusGone, corps: corpsLienExpire,
			wantMotif: pvit.ErrRequeteInvalide, wantCodeApplicatif: pvit.CodeTransactionExpiree,
		},
		{
			nom: "url de redirection eteinte", statut: http.StatusForbidden, corps: corpsRedirectionInactive,
			wantMotif: pvit.ErrConfiguration, wantConfiguration: true, wantCodeApplicatif: pvit.CodeRedirectionInactive,
		},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()

			client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule())
			b.programmer("link", reponse{statut: c.statut, corps: c.corps})

			_, err := client.CreerLien(t.Context(), demandeLienType())
			if !errors.Is(err, c.wantMotif) {
				t.Fatalf("CreerLien() err = %v, want %v", err, c.wantMotif)
			}

			e, ok := errors.AsType[pvit.Erreur](err)
			if !ok {
				t.Fatalf("CreerLien() err = %v, want une pvit.Erreur", err)
			}
			if got := e.Code; got != c.wantCodeApplicatif {
				t.Errorf("CreerLien() Erreur.Code = %d, want %d", got, c.wantCodeApplicatif)
			}
			if got := e.EstFauteDeConfiguration(); got != c.wantConfiguration {
				t.Errorf("CreerLien() EstFauteDeConfiguration() = %v, want %v", got, c.wantConfiguration)
			}
			if got := e.EstRejouable(); got != c.wantRejouable {
				t.Errorf("CreerLien() EstRejouable() = %v, want %v", got, c.wantRejouable)
			}
			if got, want := b.comptePour("link"), 1; got != want {
				t.Errorf("CreerLien() a appele link %d fois, want %d", got, want)
			}
		})
	}
}

// Les trois codes suivent la même règle que celui du callback : la surcharge de l'appel,
// puis le défaut des réglages, puis le refus.
//
// Le dernier cas porte la vraie valeur du test : les manques sont RASSEMBLÉS, et l'erreur
// nomme les deux champs absents d'un coup. Les rendre un par un demanderait autant
// d'allers-retours qu'il y a de codes à déclarer.
func TestLesCodesDeRedirectionPrennentLeDefautPuisLaSurcharge(t *testing.T) {
	t.Parallel()

	t.Run("le defaut des reglages s applique", func(t *testing.T) {
		t.Parallel()

		client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule())
		b.programmer("link", reponse{statut: http.StatusOK, corps: corpsLien})

		if _, err := client.CreerLien(t.Context(), demandeLienType()); err != nil {
			t.Fatalf("CreerLien() err = %v, want nil", err)
		}
		corps := decoder(t, b.derniere(t, "link").Corps)
		if got, want := corps["success_redirection_url_code"], codeRedirectionSucces; got != want {
			t.Errorf("code de succes emis = %v, want %q", got, want)
		}
		if got, want := corps["failed_redirection_url_code"], codeRedirectionEchec; got != want {
			t.Errorf("code d echec emis = %v, want %q", got, want)
		}
	})

	t.Run("la surcharge par appel gagne", func(t *testing.T) {
		t.Parallel()

		client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule())
		b.programmer("link", reponse{statut: http.StatusOK, corps: corpsLien})

		d := demandeLienType()
		d.CodeRedirectionSucces = "AUTRESUCCES"
		d.CodeRedirectionEchec = "AUTREECHEC"

		if _, err := client.CreerLien(t.Context(), d); err != nil {
			t.Fatalf("CreerLien() err = %v, want nil", err)
		}
		corps := decoder(t, b.derniere(t, "link").Corps)
		if got, want := corps["success_redirection_url_code"], "AUTRESUCCES"; got != want {
			t.Errorf("code de succes emis = %v, want %q: la surcharge de l appel doit gagner", got, want)
		}
		if got, want := corps["failed_redirection_url_code"], "AUTREECHEC"; got != want {
			t.Errorf("code d echec emis = %v, want %q: la surcharge de l appel doit gagner", got, want)
		}
	})

	t.Run("absents partout: les trois manques sont nommes d un coup", func(t *testing.T) {
		t.Parallel()

		r := reglagesAmorceSeule()
		r.CodeCallback = ""
		r.CodeRedirectionSucces = ""
		r.CodeRedirectionEchec = ""
		client, b := nouveauBancAvecReglages(t, r)

		_, err := client.CreerLien(t.Context(), demandeLienType())
		if !errors.Is(err, pvit.ErrRequeteInvalide) {
			t.Fatalf("CreerLien() err = %v, want ErrRequeteInvalide", err)
		}
		if !contientTout(err.Error(),
			"callback_url_code", "success_redirection_url_code", "failed_redirection_url_code") {
			t.Errorf("CreerLien() err = %v, want les trois champs nommes dans la meme erreur", err)
		}
		if got := b.comptePour("link"); got != 0 {
			t.Errorf("CreerLien() a emis %d requetes sans code de redirection, want 0", got)
		}
	})
}

// Les longueurs dures sont refusées AVANT l'appel, et par la porte du lien aussi.
//
// Le validateur est partagé avec le paiement, et ce test prouve qu'il est réellement
// atteint depuis ici : une refonte qui le contournerait en silence laisserait partir des
// valeurs que le serveur refuse par un 8000 dont le corps ne nomme pas toujours le champ.
func TestLesLongueursDuresDuLienSontRefuseesAvantLAppel(t *testing.T) {
	t.Parallel()

	long := func(n int) string { return strings.Repeat("x", n) }

	cas := []struct {
		nom       string
		modifier  func(*pvit.DemandeLien)
		wantChamp string
	}{
		{nom: "reference de 21", modifier: func(d *pvit.DemandeLien) { d.Reference = pvit.Reference(long(21)) }, wantChamp: "reference"},
		{nom: "reference non alphanumerique", modifier: func(d *pvit.DemandeLien) { d.Reference = "REF-123" }, wantChamp: "reference"},
		{nom: "reference vide", modifier: func(d *pvit.DemandeLien) { d.Reference = "" }, wantChamp: "reference"},
		{nom: "montant nul", modifier: func(d *pvit.DemandeLien) { d.Montant = 0 }, wantChamp: "amount"},
		{nom: "montant negatif", modifier: func(d *pvit.DemandeLien) { d.Montant = -1 }, wantChamp: "amount"},
		{nom: "operateur vide", modifier: func(d *pvit.DemandeLien) { d.Operateur = "" }, wantChamp: "operator_code"},
		{nom: "numero de 24", modifier: func(d *pvit.DemandeLien) { d.NumeroClient = long(24) }, wantChamp: "customer_account_number"},
		{nom: "agent de 16", modifier: func(d *pvit.DemandeLien) { d.Agent = long(16) }, wantChamp: "agent"},
		{nom: "produit de 16", modifier: func(d *pvit.DemandeLien) { d.Produit = long(16) }, wantChamp: "product"},
		{nom: "info libre de 16", modifier: func(d *pvit.DemandeLien) { d.InfoLibre = long(16) }, wantChamp: "free_info"},
		{nom: "compte de 31", modifier: func(d *pvit.DemandeLien) { d.CompteOperation = long(31) }, wantChamp: "merchant_operation_account_code"},
		{nom: "code de callback de 13", modifier: func(d *pvit.DemandeLien) { d.CodeCallback = long(13) }, wantChamp: "callback_url_code"},
		{nom: "proprietaire de frais inconnu", modifier: func(d *pvit.DemandeLien) { d.Frais = "QUELQU_UN" }, wantChamp: "owner_charge"},
		{nom: "proprietaire de frais operateur inconnu", modifier: func(d *pvit.DemandeLien) { d.FraisOperateur = "QUELQU_UN" }, wantChamp: "owner_charge_operator"},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()

			client, b := nouveauBanc(t)
			d := demandeLienType()
			c.modifier(&d)

			_, err := client.CreerLien(t.Context(), d)
			if !errors.Is(err, pvit.ErrRequeteInvalide) {
				t.Fatalf("CreerLien() err = %v, want ErrRequeteInvalide", err)
			}
			if !strings.Contains(err.Error(), c.wantChamp) {
				t.Errorf("CreerLien() err = %v, want un message nommant %q", err, c.wantChamp)
			}
			if got := len(b.vues()); got != 0 {
				t.Errorf("CreerLien() a emis %d requetes sur une demande invalide, want 0", got)
			}

			// Et Valider doit dire la même chose, sans client.
			if err := d.Valider(); !errors.Is(err, pvit.ErrRequeteInvalide) {
				t.Errorf("DemandeLien.Valider() err = %v, want ErrRequeteInvalide", err)
			}
		})
	}
}

// Valider relève TOUTES les violations d'un coup, et n'émet aucune requête.
//
// Toutes et non la première : un formulaire se corrige alors en un passage, là où une
// validation qui s'arrête au premier défaut demande autant d'allers-retours qu'il y a de
// fautes.
func TestValiderReleveToutesLesViolationsDuLienDUnCoup(t *testing.T) {
	t.Parallel()

	client, b := nouveauBanc(t)

	// Une demande vide : référence, montant, opérateur, numéro et service manquent tous.
	_, err := client.CreerLien(t.Context(), pvit.DemandeLien{})
	if err == nil {
		t.Fatalf("CreerLien(demande vide) err = nil, want une erreur")
	}
	if !contientTout(err.Error(), "reference", "amount", "operator_code", "customer_account_number", "service") {
		t.Errorf("CreerLien(demande vide) err = %v, want les cinq champs nommes d un coup", err)
	}
	if got := len(b.vues()); got != 0 {
		t.Errorf("CreerLien(demande vide) a emis %d requetes, want 0", got)
	}

	// Chaque violation reste retrouvable par son type.
	if _, ok := errors.AsType[pvit.ChampInvalideError](err); !ok {
		t.Errorf("CreerLien(demande vide) err = %v, want au moins une ChampInvalideError retrouvable", err)
	}
}

// Le code d'URL du lien est exigé À L'APPEL, et non au démarrage.
//
// La sévérité est graduée : exiger les onze codes dès [pvit.Nouveau] ferait refuser de
// démarrer un projet qui n'en utilise que trois. Le test passe par la méthode typée, là où
// codes_test.go l'éprouve au niveau de la composition d'URL.
func TestLeCodeDURLDuLienEstExigeALAppelEtNonAuDemarrage(t *testing.T) {
	t.Parallel()

	r := reglagesAmorceSeule()
	r.Codes.Lien = ""
	client, b := nouveauBancAvecReglages(t, r)

	_, err := client.CreerLien(t.Context(), demandeLienType())
	if !errors.Is(err, pvit.ErrConfiguration) {
		t.Fatalf("CreerLien() err = %v, want ErrConfiguration", err)
	}
	if !strings.Contains(err.Error(), "link") {
		t.Errorf("CreerLien() err = %v, want un message nommant link, ce qui suffit a savoir quel code chercher", err)
	}
	if got := len(b.vues()); got != 0 {
		t.Errorf("CreerLien() a emis %d requetes sans code d URL, want 0", got)
	}
}

// Le compte d'opération vient de l'opérateur, et un opérateur non configuré rend une erreur
// qui NOMME les opérateurs configurés.
//
// C'est la garde contre le code 3017, et elle est côté client : envoyer le compte d'Airtel
// sur un parcours Moov est refusé au comptoir, devant un client qui attend.
func TestLeCompteDuLienVientDeLOperateurEtNommeLesManques(t *testing.T) {
	t.Parallel()

	t.Run("la surcharge du compte gagne", func(t *testing.T) {
		t.Parallel()

		client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule())
		b.programmer("link", reponse{statut: http.StatusOK, corps: corpsLien})

		d := demandeLienType()
		d.CompteOperation = "ACC_9IMPOSE"

		if _, err := client.CreerLien(t.Context(), d); err != nil {
			t.Fatalf("CreerLien() err = %v, want nil", err)
		}
		got := decoder(t, b.derniere(t, "link").Corps)["merchant_operation_account_code"]
		if want := "ACC_9IMPOSE"; got != want {
			t.Errorf("compte emis = %v, want %q", got, want)
		}
	})

	t.Run("un operateur non configure nomme ceux qui le sont", func(t *testing.T) {
		t.Parallel()

		client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule())

		d := demandeLienType()
		d.Operateur = "GIMAC"

		_, err := client.CreerLien(t.Context(), d)
		if !errors.Is(err, pvit.ErrConfiguration) {
			t.Fatalf("CreerLien() err = %v, want ErrConfiguration", err)
		}
		if !contientTout(err.Error(), "GIMAC", string(pvit.OperateurAirtel)) {
			t.Errorf("CreerLien() err = %v, want un message nommant l operateur demande et ceux configures", err)
		}
		if got := len(b.vues()); got != 0 {
			t.Errorf("CreerLien() a emis %d requetes, want 0", got)
		}
	})
}

// Le lien porte l'en-tête de type de notification, puisque c'est un PAYMENT.
//
// Cet en-tête ne vaut que pour les paiements, et un lien en est un : l'omettre priverait
// l'appelant du format qu'il a demandé pour ses notifications, sans aucun signe d'erreur.
func TestLeLienPorteLeMediaTypeDeNotification(t *testing.T) {
	t.Parallel()

	client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule(), pvit.AvecMediaTypeCallback("application/xml"))
	b.programmer("link", reponse{statut: http.StatusOK, corps: corpsLien})

	if _, err := client.CreerLien(t.Context(), demandeLienType()); err != nil {
		t.Fatalf("CreerLien() err = %v, want nil", err)
	}
	if got, want := b.derniere(t, "link").MediaTypeCallback, "application/xml"; got != want {
		t.Errorf("CreerLien() X-Callback-MediaType = %q, want %q", got, want)
	}
}

// Dix liens ne font qu'un renouvellement de clé.
//
// C'est la seule façon de prouver que la méthode typée passe bien par emettre, et hérite
// donc du cache de clé sous son verrou par compte, au lieu de réimplémenter la rotation.
func TestDixLiensNeFontQuUnRenouvellement(t *testing.T) {
	t.Parallel()

	client, b := nouveauBanc(t)
	b.programmer("renew-secret", reponse{statut: http.StatusOK, corps: corpsRenouvellement})
	b.programmer("link", reponse{statut: http.StatusOK, corps: corpsLien})

	for range 10 {
		if _, err := client.CreerLien(t.Context(), demandeLienType()); err != nil {
			t.Fatalf("CreerLien() err = %v, want nil", err)
		}
	}

	if got, want := b.comptePour("link"), 10; got != want {
		t.Errorf("link appele %d fois, want %d", got, want)
	}
	if got, want := b.comptePour("renew-secret"), 1; got != want {
		t.Errorf("renew-secret appele %d fois, want %d: la cle doit etre mise en cache", got, want)
	}
}

// Un refus de clé sur le lien déclenche un seul rejeu, et le corps du second coup est
// IDENTIQUE à celui du premier.
//
// La seconde moitié est la vraie valeur du test : un corps rejoué depuis un io.Reader déjà
// consommé partirait VIDE au second coup, ce qui donnerait un refus de contrat
// incompréhensible sur une requête parfaitement valide.
func TestUnRefusDeCleSurLeLienDeclencheUnSeulRejeu(t *testing.T) {
	t.Parallel()

	client, b := nouveauBanc(t)
	b.programmer("renew-secret",
		reponse{statut: http.StatusOK, corps: `{"secret":"sk_live_premiere","expires_in":3600}`},
		reponse{statut: http.StatusOK, corps: `{"secret":"sk_live_seconde","expires_in":3600}`})
	b.programmer("link",
		reponse{statut: http.StatusForbidden, corps: corpsAuthRefusee},
		reponse{statut: http.StatusOK, corps: corpsLien})

	if _, err := client.CreerLien(t.Context(), demandeLienType()); err != nil {
		t.Fatalf("CreerLien() err = %v, want nil apres un rejeu", err)
	}

	if got, want := b.comptePour("link"), 2; got != want {
		t.Errorf("link appele %d fois, want %d", got, want)
	}
	if got, want := b.comptePour("renew-secret"), 2; got != want {
		t.Errorf("renew-secret appele %d fois, want %d", got, want)
	}

	var corps []string
	for _, vue := range b.vues() {
		if strings.HasSuffix(vue.Chemin, "/link") {
			corps = append(corps, vue.Corps)
		}
	}
	if len(corps) != 2 {
		t.Fatalf("%d corps vus sur link, want 2", len(corps))
	}
	if corps[0] != corps[1] {
		t.Errorf("le corps du rejeu differe du premier:\npremier = %s\nsecond  = %s\nun lecteur deja consomme enverrait un corps vide", corps[0], corps[1])
	}
	if got, want := b.derniere(t, "link").Secret, "sk_live_seconde"; got != want {
		t.Errorf("le rejeu a porte la cle %q, want %q: la cle refusee ne doit pas etre reutilisee", got, want)
	}
}

// AUCUNE DONNÉE DE CARTE NE TRAVERSE CE PAQUET, et c'est ce qui tient l'appelant hors du
// périmètre PCI.
//
// La carte s'entre sur le formulaire que la passerelle héberge, derrière l'adresse rendue.
// Ce test épingle le jeu de clés exact de la charge utile, et attrape donc le jour où
// quelqu'un ajoute serviablement un champ de numéro, de cryptogramme ou d'échéance.
func TestLeLienNeTransporteAucuneDonneeDeCarte(t *testing.T) {
	t.Parallel()

	client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule())
	b.programmer("link", reponse{statut: http.StatusOK, corps: corpsLien})

	if _, err := client.PayerParCarte(t.Context(), demandeCarteType()); err != nil {
		t.Fatalf("PayerParCarte() err = %v, want nil", err)
	}

	corps := decoder(t, b.derniere(t, "link").Corps)
	for _, interdit := range []string{
		"pan", "card", "card_number", "cardNumber", "numero_carte",
		"cvv", "cvc", "cvv2", "security_code",
		"expiry", "expiration", "exp_month", "exp_year", "holder", "cardholder",
	} {
		if _, present := corps[interdit]; present {
			t.Errorf("la charge utile porte %q, want absent: aucune donnee de carte ne doit traverser ce paquet", interdit)
		}
	}

	// Et le jeu de clés en entier, pour attraper un champ qu'on n'aurait pas pensé à
	// interdire par son nom.
	want := []string{
		"amount", "callback_url_code", "customer_account_number",
		"failed_redirection_url_code", "merchant_operation_account_code",
		"owner_charge", "owner_charge_operator", "reference", "service",
		"success_redirection_url_code", "transaction_type",
	}
	got := slices.Sorted(maps.Keys(corps))
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("le jeu de cles de la charge utile a change, -want +got:\n%s", diff)
	}
}

// Le seuil du bac à sable est une propriété du SERVEUR DE TEST, et ce paquet n'en sait rien.
//
// La recette de la passerelle force le succès sous mille francs et l'échec au dessus. Coder
// ce seuil ici ferait refuser côté client des montants que la production accepte, et le
// montant d'un abonnement réel passerait sous une borne de test. Ce test épingle l'ABSENCE
// de cette connaissance : aucun écrêtage, aucune branche, aucun refus.
func TestLeSeuilDuBacASableNeChangeRienAuCorpsEmis(t *testing.T) {
	t.Parallel()

	for _, montant := range []pvit.Montant{1, 999, 1000, 1001, 4500, 50000} {
		t.Run(montant.String(), func(t *testing.T) {
			t.Parallel()

			client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule())
			b.programmer("link", reponse{statut: http.StatusOK, corps: corpsLien})

			d := demandeCarteType()
			d.Montant = montant

			if _, err := client.PayerParCarte(t.Context(), d); err != nil {
				t.Fatalf("PayerParCarte(%v) err = %v, want nil", montant, err)
			}
			got := decoder(t, b.derniere(t, "link").Corps)["amount"]
			if want := float64(montant); got != want {
				t.Errorf("PayerParCarte(%v) amount emis = %v, want %v: aucun ecretage ne doit exister", montant, got, want)
			}
		})
	}
}

// PayerParCarte envoie VISA_MASTERCARD sans qu'on ait eu à le dire, et c'est tout l'objet
// de ce type de demande.
//
// [pvit.DemandeCarte] n'expose AUCUN champ de service : la valeur dangereuse ne peut donc
// être ni oubliée, ni mal écrite, ni écrasée par l'option du client, dont le défaut est
// justement le seul service que ce point d'entrée refuse.
func TestPayerParCarteEnvoieVisaMastercardSansQuOnAitALeDire(t *testing.T) {
	t.Parallel()

	// Avec l'option réglée sur un autre service, pour prouver qu'elle ne passe pas devant.
	client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule(), pvit.AvecService(pvit.ServiceRestful))
	b.programmer("link", reponse{statut: http.StatusOK, corps: corpsLien})

	lien, err := client.PayerParCarte(t.Context(), demandeCarteType())
	if err != nil {
		t.Fatalf("PayerParCarte() err = %v, want nil", err)
	}

	got := decoder(t, b.derniere(t, "link").Corps)["service"]
	if want := string(pvit.ServiceVisaMastercard); got != want {
		t.Errorf("PayerParCarte() service emis = %v, want %q", got, want)
	}
	if !lien.Utilisable() {
		t.Errorf("PayerParCarte() Utilisable() = false, want true")
	}
	if got, want := lien.URL, "https://checkout.mypvit.pro/PAY23102400001"; got != want {
		t.Errorf("PayerParCarte() URL = %q, want %q", got, want)
	}
}

// La carte exige TOUJOURS le numéro du client, sans condition de service à vérifier.
//
// C'est la seconde faute que [pvit.DemandeCarte] met hors de portée : là où
// [pvit.DemandeLien] doit en faire une règle que seule l'exécution évalue, ici elle tient
// par construction, puisque le service ne peut pas être autre chose.
func TestUnPaiementParCarteSansNumeroClientEstRefuseAvantLAppel(t *testing.T) {
	t.Parallel()

	client, b := nouveauBanc(t)

	d := demandeCarteType()
	d.NumeroClient = ""

	_, err := client.PayerParCarte(t.Context(), d)
	if !errors.Is(err, pvit.ErrRequeteInvalide) {
		t.Fatalf("PayerParCarte() err = %v, want ErrRequeteInvalide", err)
	}
	champ, _ := errors.AsType[pvit.ChampInvalideError](err)
	if champ.Champ != "customer_account_number" {
		t.Errorf("PayerParCarte() err = %v, want une faute sur customer_account_number", err)
	}
	if got := len(b.vues()); got != 0 {
		t.Errorf("PayerParCarte() a emis %d requetes, want 0", got)
	}

	// Et Valider dit la même chose, sans client ni réseau.
	if err := d.Valider(); !errors.Is(err, pvit.ErrRequeteInvalide) {
		t.Errorf("DemandeCarte.Valider() err = %v, want ErrRequeteInvalide", err)
	}
}

// Les deux portes émettent LE MÊME CORPS, à l'octet près.
//
// C'est ce qui prouve que la porte carte n'est qu'un raccourci, et non une seconde
// implémentation qui divergera : un champ ajouté à la demande de carte et oublié dans la
// conversion vers la demande de lien ferait échouer ce test, et lui seul.
func TestLesDeuxPortesDuLienEmettentLeMemeCorps(t *testing.T) {
	t.Parallel()

	// Tous les champs facultatifs sont renseignés, pour qu'aucun ne puisse se perdre en
	// silence dans la conversion.
	carte := pvit.DemandeCarte{
		Reference:             "REF1234567890",
		Montant:               4500,
		Operateur:             pvit.OperateurAirtel,
		NumeroClient:          "074111111",
		CompteOperation:       "ACC_9IMPOSE",
		CodeCallback:          "CBAUTRE",
		CodeRedirectionSucces: "AUTRESUCCES",
		CodeRedirectionEchec:  "AUTREECHEC",
		Frais:                 pvit.ProprietaireMarchand,
		FraisOperateur:        pvit.ProprietaireMarchand,
		Agent:                 "AGENT123",
		Produit:               "PRODUCT123",
		InfoLibre:             "Free Info",
	}

	// L'équivalent explicite, par la porte générale.
	lien := pvit.DemandeLien{
		Service:               pvit.ServiceVisaMastercard,
		Reference:             carte.Reference,
		Montant:               carte.Montant,
		Operateur:             carte.Operateur,
		NumeroClient:          carte.NumeroClient,
		CompteOperation:       carte.CompteOperation,
		CodeCallback:          carte.CodeCallback,
		CodeRedirectionSucces: carte.CodeRedirectionSucces,
		CodeRedirectionEchec:  carte.CodeRedirectionEchec,
		Frais:                 carte.Frais,
		FraisOperateur:        carte.FraisOperateur,
		Agent:                 carte.Agent,
		Produit:               carte.Produit,
		InfoLibre:             carte.InfoLibre,
	}

	corpsDe := func(t *testing.T, emettre func(*pvit.Client) error) string {
		t.Helper()
		client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule())
		b.programmer("link", reponse{statut: http.StatusOK, corps: corpsLien})
		if err := emettre(client); err != nil {
			t.Fatalf("emission err = %v, want nil", err)
		}
		return b.derniere(t, "link").Corps
	}

	parLaCarte := corpsDe(t, func(c *pvit.Client) error {
		_, err := c.PayerParCarte(t.Context(), carte)
		return err
	})
	parLeLien := corpsDe(t, func(c *pvit.Client) error {
		_, err := c.CreerLien(t.Context(), lien)
		return err
	})

	if parLaCarte != parLeLien {
		t.Errorf("les deux portes emettent des corps differents:\ncarte = %s\nlien  = %s\nun champ s est perdu dans la conversion", parLaCarte, parLeLien)
	}
}

// Le nom d'opération distingue les deux portes, alors qu'elles partent au MÊME chemin.
//
// Une ligne de journal doit rendre l'INTENTION de l'appelant, et non la route servie :
// c'est la même règle qui distingue déjà le paiement du remboursement, tous deux au chemin
// rest. Sans cela, un marchand qui encaisse par carte depuis sa boutique en ligne et par
// lien depuis sa caisse ne saurait pas, en lisant ses journaux, laquelle a échoué.
func TestLOperationDUnRefusDistingueLaCarteDuLien(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom           string
		emettre       func(*testing.T, *pvit.Client) error
		wantOperation string
	}{
		{
			nom:           "la carte",
			wantOperation: "carte",
			emettre: func(t *testing.T, c *pvit.Client) error {
				_, err := c.PayerParCarte(t.Context(), demandeCarteType())
				return err
			},
		},
		{
			nom:           "le lien",
			wantOperation: "lien",
			emettre: func(t *testing.T, c *pvit.Client) error {
				_, err := c.CreerLien(t.Context(), demandeLienType())
				return err
			},
		},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()

			client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule())
			b.programmer("link", reponse{statut: http.StatusGone, corps: corpsLienExpire})

			err := c.emettre(t, client)

			e, ok := errors.AsType[pvit.Erreur](err)
			if !ok {
				t.Fatalf("err = %v, want une pvit.Erreur", err)
			}
			if got := e.Operation; got != c.wantOperation {
				t.Errorf("Erreur.Operation = %q, want %q", got, c.wantOperation)
			}
			// Et le message porte ce mot, puisque c'est ce qu'un journal recopie.
			if !strings.Contains(err.Error(), c.wantOperation) {
				t.Errorf("err = %v, want un message nommant %q", err, c.wantOperation)
			}
		})
	}
}

// LA BOUCLE COMPLÈTE : un lien créé, puis la notification qui tranche la tentative.
//
// C'est la recette que la passerelle impose avant la production, et elle vaut pour la carte
// comme pour le mobile money : un paiement par carte finit sur LE MÊME webhook, sous LA
// MÊME référence, avec LE MÊME accusé de réception à écho dynamique. Ce test prouve les
// trois, et donc qu'aucun code neuf n'était nécessaire du côté du callback.
func TestUnLienCreePuisUneNotificationTrancheLaTentative(t *testing.T) {
	t.Parallel()

	client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule())
	b.programmer("link", reponse{statut: http.StatusOK, corps: corpsLien})

	lien, err := client.PayerParCarte(t.Context(), demandeCarteType())
	if err != nil {
		t.Fatalf("PayerParCarte() err = %v, want nil", err)
	}

	// La notification arrive plus tard, sur la route que l'appelant expose, et porte la
	// même référence marchande que celle qui est partie.
	notification := `{"transactionId":"PAY23102400001","merchantReferenceId":"REF1234567890",` +
		`"status":"FAILED","amount":150,"customerID":"074111111","fees":3.5,"totalAmount":155,` +
		`"chargeOwner":"CUSTOMER","transactionOperation":"PAYMENT","code":200,` +
		`"operator":"AIRTEL_MONEY","accountOperationCode":"ACC_1AIRTEL"}`

	callback, err := pvit.DecoderCallback([]byte(notification))
	if err != nil {
		t.Fatalf("DecoderCallback() err = %v, want nil", err)
	}

	// Un lien créé avec succès peut parfaitement finir en échec.
	if got, want := callback.Statut, pvit.StatutEchec; got != want {
		t.Errorf("notification statut = %q, want %q", got, want)
	}
	if !pvit.TransitionPermise(pvit.StatutEnAttente, callback.Statut) {
		t.Errorf("TransitionPermise(attente, %q) = false, want true", callback.Statut)
	}
	if !lien.StatutCreation.EstSucces() {
		t.Errorf("Lien.StatutCreation = %q, want SUCCESS: la creation a bien reussi", lien.StatutCreation)
	}
	if callback.Statut.EstSucces() {
		t.Errorf("le succes de la creation du lien ne doit rien dire du paiement, qui a echoue ici")
	}

	// Et l'accusé de réception est celui du mobile money : un écho DYNAMIQUE, jamais 200 en
	// dur, faute de quoi la passerelle marque la transaction en echec de notification.
	accuse, err := callback.Accuse()
	if err != nil {
		t.Fatalf("Accuse() err = %v, want nil", err)
	}
	if !contientTout(string(accuse), `"transactionId":"PAY23102400001"`, `"responseCode":200`) {
		t.Errorf("Accuse() = %s, want l echo du transactionId et du code recus", accuse)
	}
}

// Le délai d'incertitude du paquet est calibré pour un parcours SANS NAVIGATEUR, et il est
// trop court pour une carte bancaire.
//
// Devant un formulaire bancaire puis une authentification forte, une tentative encore en
// attente au bout de trois minutes est NORMALE. Ce paquet ne fabrique pas de second seuil,
// dont rien ne publie la valeur : il laisse l'appelant donner celui de son tunnel. Ce test
// prouve que la généralisation ne change RIEN au comportement existant, et qu'elle donne
// bien le levier.
func TestAInterrogerApresDonneSonDelaiALAppelant(t *testing.T) {
	t.Parallel()

	initiee := time.Date(2026, time.October, 20, 8, 0, 0, 0, time.UTC)

	// La délégation : avec le délai du paquet, c'est exactement l'ancien comportement.
	for _, statut := range []pvit.Statut{pvit.StatutEnAttente, pvit.StatutAmbigu, pvit.StatutSucces, pvit.StatutEchec, ""} {
		for _, ecoule := range []time.Duration{0, time.Minute, pvit.DelaiIncertitude, time.Hour} {
			maintenant := initiee.Add(ecoule)
			got := pvit.AInterrogerApres(statut, initiee, maintenant, pvit.DelaiIncertitude)
			if want := pvit.AInterroger(statut, initiee, maintenant); got != want {
				t.Errorf("AInterrogerApres(%q, +%v, DelaiIncertitude) = %v, want %v, comme AInterroger",
					statut, ecoule, got, want)
			}
		}
	}

	// Le levier : un tunnel carte qui laisse une demi-heure au client ne doute pas au bout
	// de quatre minutes, là où le défaut du paquet douterait déjà.
	const delaiCarte = 30 * time.Minute
	quatreMinutes := initiee.Add(4 * time.Minute)

	if !pvit.AInterroger(pvit.StatutEnAttente, initiee, quatreMinutes) {
		t.Errorf("AInterroger(attente, +4min) = false, want true: le defaut du paquet doute a trois minutes")
	}
	if pvit.AInterrogerApres(pvit.StatutEnAttente, initiee, quatreMinutes, delaiCarte) {
		t.Errorf("AInterrogerApres(attente, +4min, 30min) = true, want false: un formulaire bancaire prend plus de quatre minutes")
	}
	if !pvit.AInterrogerApres(pvit.StatutEnAttente, initiee, initiee.Add(delaiCarte), delaiCarte) {
		t.Errorf("AInterrogerApres(attente, +30min, 30min) = false, want true")
	}

	// Un délai négatif est une faute d'appel, et ne fait pas paniquer le programme.
	if !pvit.AInterrogerApres(pvit.StatutEnAttente, initiee, initiee, -time.Hour) {
		t.Errorf("AInterrogerApres(attente, delai negatif) = false, want true: traite comme nul")
	}
}

// LE DÉSACCORD DES DEUX SOURCES PUBLIÉES, épinglé dans les deux sens.
//
// Le tableau des champs du lien écrit owner_charge_operator, comme l'API de transaction ;
// la collection de requêtes écrit operator_owner_charge, mots inversés. Personne n'a
// tranché, et le défaut suit le tableau des champs parce que ce nom là est celui que l'API
// de transaction emploie et qu'il a été éprouvé contre le serveur.
//
// Ce que le test prouve en plus, et c'est le point qui compte : UN SEUL des deux noms part
// jamais. Les émettre tous les deux vaudrait un 8000 pour champ inconnu, et laisserait la
// passerelle choisir lequel lire sur un réglage qui décide qui paie.
func TestUnSeulNomDuSecondEtageDeFraisDuLienPartJamais(t *testing.T) {
	t.Parallel()

	const (
		nomDocumente  = "owner_charge_operator"
		nomCollection = "operator_owner_charge"
	)

	cas := []struct {
		nom        string
		options    []pvit.Option
		wantNom    string
		wantAbsent string
	}{
		{
			nom:        "par defaut, le nom du tableau des champs",
			wantNom:    nomDocumente,
			wantAbsent: nomCollection,
		},
		{
			nom:        "avec l option, le nom de la collection de requetes",
			options:    []pvit.Option{pvit.AvecFraisOperateurEnOperatorOwnerCharge()},
			wantNom:    nomCollection,
			wantAbsent: nomDocumente,
		},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()

			client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule(), c.options...)
			b.programmer("link", reponse{statut: http.StatusOK, corps: corpsLien})

			d := demandeCarteType()
			d.FraisOperateur = pvit.ProprietaireMarchand

			if _, err := client.PayerParCarte(t.Context(), d); err != nil {
				t.Fatalf("PayerParCarte() err = %v, want nil", err)
			}

			corps := decoder(t, b.derniere(t, "link").Corps)

			got, present := corps[c.wantNom]
			if !present {
				t.Errorf("la charge utile ne porte pas %q, want present: corps = %v", c.wantNom, corps)
			}
			if want := string(pvit.ProprietaireMarchand); got != want {
				t.Errorf("%s = %v, want %q", c.wantNom, got, want)
			}
			if _, present := corps[c.wantAbsent]; present {
				t.Errorf("la charge utile porte AUSSI %q, want absent: deux noms pour un meme reglage laisseraient la passerelle choisir", c.wantAbsent)
			}
		})
	}
}

// L'option est bornée au LIEN, et ne renomme rien sur l'API de transaction.
//
// Le désaccord des sources n'existe que sur le chemin du lien. Laisser l'option déborder
// renommerait un champ sur un chemin qui marche, ce qui est le genre de régression qu'aucun
// refus visible ne signale : la passerelle ignorerait le champ inconnu, le second étage de
// frais prendrait son propre défaut, et le marchand le découvrirait dans un export.
func TestLOptionDeNomDeFraisNeTouchePasLAPIDeTransaction(t *testing.T) {
	t.Parallel()

	client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule(), pvit.AvecFraisOperateurEnOperatorOwnerCharge())
	b.programmer("rest", reponse{statut: http.StatusOK, corps: corpsPaiement})

	if _, err := client.Payer(t.Context(), demandeType()); err != nil {
		t.Fatalf("Payer() err = %v, want nil", err)
	}

	corps := decoder(t, b.derniere(t, "rest").Corps)
	if _, present := corps["owner_charge_operator"]; !present {
		t.Errorf("Payer() ne porte plus owner_charge_operator, want present: l option du lien ne doit pas deborder")
	}
	if _, present := corps["operator_owner_charge"]; present {
		t.Errorf("Payer() porte operator_owner_charge, want absent: le desaccord n existe que sur le chemin du lien")
	}
}

// LA RÉFÉRENCE QUI PART EST CELLE QUI INTERROGE LE STATUT, espaces compris.
//
// Relevé par audit, et c'est la classe de panne la plus coûteuse de ce paquet :
// NouvelleReference taillait la référence pour la JUGER, puis la valeur brute partait sur
// le fil, tandis que Client.Statut, qui taille elle aussi, la redemandait sans. Une
// référence recopiée avec un retour à la ligne partait donc sous une forme et se
// redemandait sous une autre, et les deux moitiés de la tentative ne se rejoignaient plus.
//
// Le test couvre les QUATRE chemins qui portent une référence, et vérifie aussi que la
// valeur rendue à l'appelant est bien celle qui est partie : persister autre chose que ce
// que la passerelle a reçu rouvrirait le même écart d'un cran plus loin.
// Les sous tests ne sont PAS parallèles entre eux, contrairement à la règle du dépôt : ils
// partagent un seul banc, et deux d'entre eux lisent la dernière requête vue sur rest.
func TestLaReferenceQuiPartEstCelleQuiInterrogeLeStatut(t *testing.T) {
	t.Parallel()

	const (
		sale   = "  REF1234567890\n"
		propre = "REF1234567890"
	)

	client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule())
	b.programmer("link", reponse{statut: http.StatusOK, corps: corpsLien})
	b.programmer("rest", reponse{statut: http.StatusOK, corps: corpsPaiement})
	b.programmer("status", reponse{statut: http.StatusOK, corps: `{"status":"SUCCESS","amount":150,"fees":3.5}`})

	t.Run("le lien par carte", func(t *testing.T) {
		d := demandeCarteType()
		d.Reference = pvit.Reference(sale)

		lien, err := client.PayerParCarte(t.Context(), d)
		if err != nil {
			t.Fatalf("PayerParCarte() err = %v, want nil", err)
		}
		if got := decoder(t, b.derniere(t, "link").Corps)["reference"]; got != propre {
			t.Errorf("reference emise = %q, want %q: la valeur brute ne doit pas partir sur le fil", got, propre)
		}
		if got := lien.Reference; got != propre {
			t.Errorf("Lien.Reference = %q, want %q: l appelant doit persister ce qui est parti", got, propre)
		}
	})

	t.Run("le paiement", func(t *testing.T) {
		p := demandeType()
		p.Reference = pvit.Reference(sale)

		init, err := client.Payer(t.Context(), p)
		if err != nil {
			t.Fatalf("Payer() err = %v, want nil", err)
		}
		if got := decoder(t, b.derniere(t, "rest").Corps)["reference"]; got != propre {
			t.Errorf("reference emise = %q, want %q", got, propre)
		}
		if got := init.Reference; got != propre {
			t.Errorf("Initiation.Reference = %q, want %q", got, propre)
		}
	})

	t.Run("le remboursement", func(t *testing.T) {
		r := pvit.DemandeRemboursement{
			Reference:    pvit.Reference(sale),
			Montant:      150,
			Operateur:    pvit.OperateurAirtel,
			NumeroClient: "074111111",
		}

		rb, err := client.Rembourser(t.Context(), r)
		if err != nil {
			t.Fatalf("Rembourser() err = %v, want nil", err)
		}
		if got := decoder(t, b.derniere(t, "rest").Corps)["reference"]; got != propre {
			t.Errorf("reference emise = %q, want %q", got, propre)
		}
		if got := rb.Reference; got != propre {
			t.Errorf("Remboursement.Reference = %q, want %q", got, propre)
		}
	})

	t.Run("l interrogation de statut", func(t *testing.T) {
		if _, err := client.Statut(t.Context(), pvit.DemandeStatut{
			Reference: pvit.Reference(sale),
			Operateur: pvit.OperateurAirtel,
		}); err != nil {
			t.Fatalf("Statut() err = %v, want nil", err)
		}
		query := b.derniere(t, "status").Query
		if !strings.Contains(query, "transactionId="+propre) {
			t.Errorf("query = %q, want transactionId=%s: les deux bouts doivent parler de la meme reference", query, propre)
		}
	})
}

// UN REFUS PEUT ARRIVER SOUS UN CODE DE SUCCÈS, et son code applicatif ne doit pas se
// perdre.
//
// L'API de transaction répond déjà « status PENDING, status_code 200 » dans un corps de
// 2xx : le statut et le code vivent dans le corps, et rien ne garantit que le lien réserve
// ses refus aux codes 4xx. Relevé par audit : un 200 portant 3012 ressortait en « charge
// utile inexploitable », avec Code à zéro, ce qui ne disait pas au marchand d'aller
// rallumer son URL de redirection dans l'espace marchand.
func TestUnRefusDansUnCorpsDeSuccesEstClasseParSonCode(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom               string
		corps             string
		wantMotif         error
		wantCode          int
		wantConfiguration bool
	}{
		{
			nom:               "casse melee, la forme de ce point d entree",
			corps:             `{"status":"FAILED","statusCode":3012,"error":"REDIRECTION_URL_NOT_ACTIVE","message":"url eteinte"}`,
			wantMotif:         pvit.ErrConfiguration,
			wantCode:          pvit.CodeRedirectionInactive,
			wantConfiguration: true,
		},
		{
			nom:       "soulignes, la forme des corps d erreur publies",
			corps:     `{"status":"FAILED","status_code":5002,"error":"TRANSACTION_LINK_USED","message":"deja utilise"}`,
			wantMotif: pvit.ErrDejaInitiee,
			wantCode:  pvit.CodeLienConsomme,
		},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()

			client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule())
			b.programmer("link", reponse{statut: http.StatusOK, corps: c.corps})

			lien, err := client.CreerLien(t.Context(), demandeLienType())
			if !errors.Is(err, c.wantMotif) {
				t.Fatalf("CreerLien() err = %v, want %v", err, c.wantMotif)
			}

			e, ok := errors.AsType[pvit.Erreur](err)
			if !ok {
				t.Fatalf("CreerLien() err = %v, want une pvit.Erreur", err)
			}
			if got := e.Code; got != c.wantCode {
				t.Errorf("Erreur.Code = %d, want %d: le code applicatif est le discriminant, il ne doit pas se perdre", got, c.wantCode)
			}
			if got := e.EstFauteDeConfiguration(); got != c.wantConfiguration {
				t.Errorf("EstFauteDeConfiguration() = %v, want %v", got, c.wantConfiguration)
			}
			if e.EstRejouable() {
				t.Errorf("EstRejouable() = true, want false: aucun de ces refus ne se repare en rejouant")
			}
			if lien.Utilisable() {
				t.Errorf("Lien.Utilisable() = true, want false")
			}
			if got, want := lien.Reference, pvit.Reference("REF1234567890"); got != want {
				t.Errorf("Lien.Reference = %q, want %q: la tentative reste interrogeable", got, want)
			}
		})
	}
}

// UN CORPS DE SUCCÈS SANS CODE CONNU RESTE INEXPLOITABLE, et surtout PAS rejouable.
//
// C'est le piège que le classement conditionnel évite. Classer un 2xx à l'aveugle le ferait
// passer par motifDuStatut, dont le défaut est ErrIndisponible, donc rejouable : une boucle
// de réconciliation régénérerait alors des liens de paiement sur une réponse dont personne
// ne sait rien tirer.
func TestUnCorpsDeSuccesSansCodeConnuResteInexploitableEtNonRejouable(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom   string
		corps string
	}{
		{nom: "aucun code", corps: `{"status":"FAILED","message":"quelque chose"}`},
		{nom: "le code du succes", corps: `{"status":"SUCCESS","statusCode":200}`},
		{nom: "un code que la passerelle n a jamais publie", corps: `{"statusCode":7777}`},
		{nom: "corps vide", corps: `{}`},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()

			client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule())
			b.programmer("link", reponse{statut: http.StatusOK, corps: c.corps})

			_, err := client.CreerLien(t.Context(), demandeLienType())
			if !errors.Is(err, pvit.ErrReponseIllisible) {
				t.Fatalf("CreerLien(corps %q) err = %v, want ErrReponseIllisible", c.corps, err)
			}
			if errors.Is(err, pvit.ErrIndisponible) {
				t.Errorf("CreerLien(corps %q) err = %v, want pas ErrIndisponible: un 2xx classe a l aveugle deviendrait rejouable", c.corps, err)
			}
			e, ok := errors.AsType[pvit.Erreur](err)
			if !ok {
				t.Fatalf("CreerLien() err = %v, want une pvit.Erreur", err)
			}
			if e.EstRejouable() {
				t.Errorf("CreerLien(corps %q) EstRejouable() = true, want false", c.corps)
			}
		})
	}
}
