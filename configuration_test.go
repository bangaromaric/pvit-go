package pvit_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pvit "github.com/bangaromaric/pvit-go"
)

// serveurMuet rend un serveur qui compte ses appels et ne devrait jamais en voir.
func serveurMuet(t *testing.T) (*httptest.Server, func() int) {
	t.Helper()
	// Un compteur atomique, et non un entier nu : il est écrit depuis la goroutine du
	// serveur et lu depuis celle du test. Ces tests n'attendent aucun appel, donc le
	// détecteur de course ne verrait jamais le défaut ; il serait là quand même.
	var appels atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		appels.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, func() int { return int(appels.Load()) }
}

// Nouveau n'émet AUCUNE requête, et ce test le prouve plutôt que de le supposer.
//
// Se connecter pour découvrir qu'une clé est vide enverrait des identifiants vides sur le
// réseau, et le journal de la passerelle en garderait la trace. C'est aussi ce qui permet à
// un binaire de refuser de démarrer plutôt que d'échouer devant le premier commerçant.
func TestUneConfigurationIncompleteNEmetAucuneRequete(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom      string
		modifier func(*pvit.Reglages)
		wantDans string
	}{
		{
			nom:      "aucun compte",
			modifier: func(r *pvit.Reglages) { r.Comptes = nil },
			wantDans: "aucun compte d operation",
		},
		{
			nom: "compte sans code d operation",
			modifier: func(r *pvit.Reglages) {
				r.Comptes = map[pvit.Operateur]pvit.Compte{
					pvit.OperateurAirtel: {MotDePasse: motDePasse},
				}
			},
			wantDans: "code d operation absent",
		},
		{
			nom: "compte sans cle ni mot de passe",
			modifier: func(r *pvit.Reglages) {
				r.Comptes = map[pvit.Operateur]pvit.Compte{
					pvit.OperateurAirtel: {CodeOperation: compteAirtel},
				}
			},
			wantDans: "ni cle d amorce ni mot de passe de rotation",
		},
		{
			nom: "code d operation trop long",
			modifier: func(r *pvit.Reglages) {
				r.Comptes = map[pvit.Operateur]pvit.Compte{
					pvit.OperateurAirtel: {CodeOperation: strings.Repeat("A", 31), CleAmorce: cleAmorce},
				}
			},
			wantDans: "maximum 30",
		},
		{
			nom: "compte indexe par un operateur vide",
			modifier: func(r *pvit.Reglages) {
				r.Comptes = map[pvit.Operateur]pvit.Compte{
					"": {CodeOperation: compteAirtel, CleAmorce: cleAmorce},
				}
			},
			wantDans: "operateur vide",
		},
		{
			nom:      "code url du paiement absent",
			modifier: func(r *pvit.Reglages) { r.Codes.Paiement = "" },
			wantDans: "code url manquant: rest",
		},
		{
			nom:      "code url du statut absent",
			modifier: func(r *pvit.Reglages) { r.Codes.Statut = "" },
			wantDans: "code url manquant: status",
		},
		{
			nom:      "code url de rotation absent alors qu un mot de passe est pose",
			modifier: func(r *pvit.Reglages) { r.Codes.Secret = "" },
			wantDans: "code url manquant: renew-secret",
		},
		{
			nom:      "code de callback trop long",
			modifier: func(r *pvit.Reglages) { r.CodeCallback = strings.Repeat("A", 13) },
			wantDans: "maximum 12",
		},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()
			srv, appels := serveurMuet(t)

			r := reglagesComplets()
			r.BaseURL = srv.URL
			c.modifier(&r)

			client, err := pvit.Nouveau(r, pvit.AvecClientHTTP(srv.Client()))

			if !errors.Is(err, pvit.ErrConfiguration) {
				t.Errorf("Nouveau() err = %v, want ErrConfiguration", err)
			}
			if client != nil {
				t.Errorf("Nouveau() a rendu un client malgre l erreur, want nil")
			}
			if err == nil || !strings.Contains(err.Error(), c.wantDans) {
				t.Errorf("Nouveau() err = %v, want un message contenant %q", err, c.wantDans)
			}
			if got := appels(); got != 0 {
				t.Errorf("Nouveau() a emis %d requetes, want 0", got)
			}
		})
	}
}

// Les manques sont RASSEMBLÉS et triés : au démarrage, on veut la liste complète de ce qui
// est absent, pas le premier problème suivi de quatre redémarrages. Le tri rend le message
// reproductible, puisque l'itération d'une carte ne l'est pas.
func TestLesManquesDeConfigurationSontTousRassembles(t *testing.T) {
	t.Parallel()

	_, err := pvit.Nouveau(pvit.Reglages{
		Codes:        pvit.CodesURL{},
		CodeCallback: strings.Repeat("A", 13),
		Comptes: map[pvit.Operateur]pvit.Compte{
			pvit.OperateurAirtel: {},
		},
	})
	if err == nil {
		t.Fatalf("Nouveau() err = nil, want une erreur")
	}

	for _, attendu := range []string{
		"code url manquant: rest",
		"code url manquant: status",
		"code d operation absent",
		"ni cle d amorce ni mot de passe",
		"maximum 12",
	} {
		if !strings.Contains(err.Error(), attendu) {
			t.Errorf("Nouveau() err = %q, want un message contenant %q", err, attendu)
		}
	}

	// Le message est reproductible d'une exécution à l'autre.
	for range 5 {
		_, autre := pvit.Nouveau(pvit.Reglages{
			Codes:   pvit.CodesURL{},
			Comptes: map[pvit.Operateur]pvit.Compte{pvit.OperateurAirtel: {}, pvit.OperateurMoov: {}},
		})
		_, premier := pvit.Nouveau(pvit.Reglages{
			Codes:   pvit.CodesURL{},
			Comptes: map[pvit.Operateur]pvit.Compte{pvit.OperateurAirtel: {}, pvit.OperateurMoov: {}},
		})
		if autre.Error() != premier.Error() {
			t.Fatalf("deux appels identiques ont rendu deux messages differents:\n%v\n%v", premier, autre)
		}
	}
}

// Un client sans aucune option doit se comporter comme documenté : chaque défaut tient
// seul, et aucun n'est obligatoire.
func TestChaqueDefautDOptionTientSeul(t *testing.T) {
	t.Parallel()

	client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule())
	b.programmer("rest", reponse{statut: http.StatusOK, corps: corpsPaiement})

	if _, err := client.Payer(t.Context(), demandeType()); err != nil {
		t.Fatalf("Payer() err = %v, want nil", err)
	}

	vue := b.derniere(t, "rest")
	corps := decoder(t, vue.Corps)

	cas := []struct {
		nom  string
		got  any
		want any
	}{
		{nom: "service", got: corps["service"], want: "RESTFUL"},
		{nom: "proprietaire des frais", got: corps["owner_charge"], want: "CUSTOMER"},
		{nom: "proprietaire des frais operateur", got: corps["owner_charge_operator"], want: "CUSTOMER"},
		{nom: "type de notification", got: vue.MediaTypeCallback, want: "application/json"},
		{nom: "agent", got: vue.Agent, want: "pvit-go/" + pvit.Version},
		{nom: "accept", got: vue.Accept, want: "application/json"},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("defaut de %s = %v, want %v", c.nom, c.got, c.want)
			}
		})
	}

	if got, want := pvit.TailleMaxCallback, 1<<16; got != want {
		t.Errorf("TailleMaxCallback = %d, want %d", got, want)
	}
	if got, want := pvit.FenetreSignature, 300*time.Second; got != want {
		t.Errorf("FenetreSignature = %v, want %v", got, want)
	}
}

// Aucune option ne se replie en silence sur son défaut : une valeur absurde est une faute de
// programmation, et un repli muet ferait tourner le service avec un réglage que personne n'a
// voulu. Comme un paquet de fondation ne fait paniquer personne, la construction échoue.
func TestUneOptionAbsurdeEmpecheLaConstruction(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom      string
		option   pvit.Option
		wantDans string
	}{
		{nom: "client http nil", option: pvit.AvecClientHTTP(nil), wantDans: "AvecClientHTTP"},
		{nom: "horloge nil", option: pvit.AvecHorloge(nil), wantDans: "AvecHorloge"},
		{nom: "delais nuls", option: pvit.AvecDelais(0, 0), wantDans: "strictement positifs"},
		{nom: "delai negatif", option: pvit.AvecDelais(-time.Second, time.Second), wantDans: "strictement positifs"},
		{nom: "connexion plus longue que le total", option: pvit.AvecDelais(10*time.Second, 5*time.Second), wantDans: "superieur au delai total"},
		{nom: "agent vide", option: pvit.AvecAgentUtilisateur("   "), wantDans: "agent vide"},
		{nom: "marge negative", option: pvit.AvecMargeRotation(-time.Minute), wantDans: "marge negative"},
		{nom: "marge demesuree", option: pvit.AvecMargeRotation(48 * time.Hour), wantDans: "duree de vie maximale"},
		{nom: "duree de vie nulle", option: pvit.AvecDureeDeVieParDefaut(0), wantDans: "hors de la plage"},
		{nom: "duree de vie demesuree", option: pvit.AvecDureeDeVieParDefaut(48 * time.Hour), wantDans: "hors de la plage"},
		{nom: "anneau vide", option: pvit.AvecTailleAnneau(0), wantDans: "hors de la plage"},
		{nom: "anneau demesure", option: pvit.AvecTailleAnneau(9), wantDans: "hors de la plage"},
		{nom: "rejeu negatif", option: pvit.AvecRejeuAuthentification(-1), wantDans: "hors de la plage"},
		{nom: "rejeu demesure", option: pvit.AvecRejeuAuthentification(4), wantDans: "hors de la plage"},
		{nom: "service inconnu", option: pvit.AvecService("TELEPATHIE"), wantDans: "inconnu"},
		{nom: "type de notification non supporte", option: pvit.AvecMediaTypeCallback("text/plain"), wantDans: "non supporte"},
		{nom: "proprietaire de frais inconnu", option: pvit.AvecProprietaireFrais("BOTH"), wantDans: "inconnue"},
		{nom: "proprietaire de frais operateur inconnu", option: pvit.AvecProprietaireFraisOperateur("NOBODY"), wantDans: "inconnue"},
		{nom: "borne de callback trop basse", option: pvit.AvecTailleMaxCallback(10), wantDans: "trop basse"},
		{nom: "fenetre de signature nulle", option: pvit.AvecFenetreSignature(0), wantDans: "non strictement positive"},
		{nom: "option nil", option: nil, wantDans: "option nil"},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()
			client, err := pvit.Nouveau(reglagesComplets(), c.option)

			if !errors.Is(err, pvit.ErrConfiguration) {
				t.Errorf("Nouveau(%s) err = %v, want ErrConfiguration", c.nom, err)
			}
			if client != nil {
				t.Errorf("Nouveau(%s) a rendu un client malgre l erreur, want nil", c.nom)
			}
			if err == nil || !strings.Contains(err.Error(), c.wantDans) {
				t.Errorf("Nouveau(%s) err = %v, want un message contenant %q", c.nom, err, c.wantDans)
			}
		})
	}
}

// LA TRAPPE DE SORTIE. Les quatre API hors périmètre restent joignables, avec la rotation de
// clé et le classement d'erreurs, sans qu'il faille forker le paquet.
func TestLaTrappeDeSortieJointUnePIHorsPerimetre(t *testing.T) {
	t.Parallel()

	client, b := nouveauBanc(t)
	b.programmer("renew-secret", reponse{statut: http.StatusOK, corps: corpsRenouvellement})
	b.programmer("link", reponse{statut: http.StatusOK, corps: `{"statusCode":200,"url":"https://pay.test/abc"}`})

	rep, err := client.Appeler(t.Context(), pvit.Appel{
		Methode:   http.MethodPost,
		CodeURL:   codeLien,
		Chemin:    "link",
		Versionne: false, // le lien est au chemin NON versionne, avec la charge utile v2
		Operateur: pvit.OperateurAirtel,
		Corps: map[string]any{
			"amount":                          150,
			"reference":                       "REF1234567890",
			"merchant_operation_account_code": compteAirtel,
			"transaction_type":                "PAYMENT",
			"owner_charge":                    "CUSTOMER",
			"operator_owner_charge":           "MERCHANT",
			"service":                         "WEB",
			"failed_redirection_url_code":     "FAILURE123",
			"success_redirection_url_code":    "SUCCESS123",
		},
	})
	if err != nil {
		t.Fatalf("Appeler() err = %v, want nil", err)
	}

	if got, want := rep.HTTP, http.StatusOK; got != want {
		t.Errorf("Appeler() HTTP = %d, want %d", got, want)
	}
	if !strings.Contains(string(rep.Corps), "pay.test") {
		t.Errorf("Appeler() corps = %s, want la charge utile du lien", rep.Corps)
	}

	vue := b.derniere(t, "link")
	if got, want := vue.Chemin, "/"+codeLien+"/link"; got != want {
		t.Errorf("Appeler() a appele %q, want %q sans prefixe de version", got, want)
	}
	if vue.Secret == "" {
		t.Errorf("Appeler() n a pas porte de cle secrete, want celle du compte: la rotation doit servir aussi ici")
	}
	// L'asymétrie de nommage de la passerelle est passée telle quelle : le lien écrit
	// operator_owner_charge là où le paiement écrit owner_charge_operator.
	if !strings.Contains(vue.Corps, "operator_owner_charge") {
		t.Errorf("Appeler() corps emis = %s, want le nom de champ du lien tel quel", vue.Corps)
	}
}

func TestLaTrappeDeSortieValideSesParametres(t *testing.T) {
	t.Parallel()

	client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule())

	cas := []struct {
		nom      string
		appel    pvit.Appel
		wantDans string
	}{
		{nom: "methode absente", appel: pvit.Appel{CodeURL: "C", Chemin: "x"}, wantDans: "methode"},
		{nom: "code url absent", appel: pvit.Appel{Methode: http.MethodGet, Chemin: "x"}, wantDans: "code url"},
		{nom: "chemin absent", appel: pvit.Appel{Methode: http.MethodGet, CodeURL: "C"}, wantDans: "chemin"},
		{
			nom:      "operateur sans compte",
			appel:    pvit.Appel{Methode: http.MethodGet, CodeURL: "C", Chemin: "x", Operateur: pvit.OperateurMoov},
			wantDans: "aucun compte d operation pour MOOV_MONEY",
		},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()
			_, err := client.Appeler(t.Context(), c.appel)
			if !errors.Is(err, pvit.ErrConfiguration) {
				t.Errorf("Appeler() err = %v, want ErrConfiguration", err)
			}
			if err == nil || !strings.Contains(err.Error(), c.wantDans) {
				t.Errorf("Appeler() err = %v, want un message contenant %q", err, c.wantDans)
			}
		})
	}
	if got := len(b.vues()); got != 0 {
		t.Errorf("Appeler() a emis %d requetes sur des appels invalides, want 0", got)
	}
}

// La règle de découverte : ajouter une famille d'opérateur est trois variables
// d'environnement et ZÉRO ligne de Go. C'est le seul moyen de tenir la promesse que la
// configuration est une table et non trois constantes.
func TestLaLectureDEnvironnementDecouvreLesComptes(t *testing.T) {
	// Pas de t.Parallel : t.Setenv l'interdit.
	t.Setenv("PVIT_BASE_URL", "https://exemple.test")
	t.Setenv("PVIT_CALLBACK_URL_CODE", codeCallback)
	t.Setenv("PVIT_URL_CODE_SECRET", codeSecret)
	t.Setenv("PVIT_URL_CODE_PAYMENT", codePaiement)
	t.Setenv("PVIT_URL_CODE_STATUS", codeStatut)
	t.Setenv("PVIT_URL_CODE_FEES", codeFrais)
	t.Setenv("PVIT_ACCOUNT_AIRTEL_MONEY_CODE", compteAirtel)
	t.Setenv("PVIT_ACCOUNT_AIRTEL_MONEY_SECRET", cleAmorce)
	t.Setenv("PVIT_ACCOUNT_AIRTEL_MONEY_PASSWORD", motDePasse)
	t.Setenv("PVIT_ACCOUNT_MOOV_MONEY_CODE", compteMoov)
	t.Setenv("PVIT_ACCOUNT_MOOV_MONEY_PASSWORD", motDePasse)
	// Une troisieme famille, sans une ligne de Go.
	t.Setenv("PVIT_ACCOUNT_GIMAC_CODE", "ACC_3GIMAC")
	t.Setenv("PVIT_ACCOUNT_GIMAC_SECRET", "sk_live_gimac")

	r, err := pvit.LireEnvironnement("")
	if err != nil {
		t.Fatalf("LireEnvironnement() err = %v, want nil", err)
	}

	if got, want := r.BaseURL, "https://exemple.test"; got != want {
		t.Errorf("BaseURL = %q, want %q", got, want)
	}
	if got, want := r.CodeCallback, codeCallback; got != want {
		t.Errorf("CodeCallback = %q, want %q", got, want)
	}
	if got, want := r.Codes.Paiement, codePaiement; got != want {
		t.Errorf("Codes.Paiement = %q, want %q", got, want)
	}
	if r.Codes.Lien != "" {
		t.Errorf("Codes.Lien = %q, want vide: les codes hors perimetre sont facultatifs", r.Codes.Lien)
	}
	if got, want := len(r.Comptes), 3; got != want {
		t.Errorf("LireEnvironnement() a decouvert %d comptes, want %d: %v", got, want, r.Comptes)
	}

	gimac, connu := r.Comptes["GIMAC"]
	if !connu {
		t.Fatalf("la famille GIMAC n a pas ete decouverte, comptes = %v", r.Comptes)
	}
	if got, want := gimac.CodeOperation, "ACC_3GIMAC"; got != want {
		t.Errorf("GIMAC code d operation = %q, want %q", got, want)
	}
	if got, want := gimac.CleAmorce, "sk_live_gimac"; got != want {
		t.Errorf("GIMAC cle d amorce = %q, want %q", got, want)
	}

	// Et la configuration lue doit câbler un client.
	client, err := pvit.NouveauDepuisEnvironnement("")
	if err != nil {
		t.Fatalf("NouveauDepuisEnvironnement() err = %v, want nil", err)
	}
	if got, want := len(client.OperateursConfigures()), 3; got != want {
		t.Errorf("OperateursConfigures() = %v, want %d entrees", client.OperateursConfigures(), want)
	}
}

// Le préfixe est un paramètre : une bibliothèque réutilisable ne doit pas décider des noms
// de variables du programme qui l'emploie.
func TestUnPrefixePersonnaliseNeLitAucuneVariableParDefaut(t *testing.T) {
	t.Setenv("PVIT_BASE_URL", "https://pas-celui-la.test")
	t.Setenv("PVIT_ACCOUNT_AIRTEL_MONEY_CODE", "ACC_PVIT")
	t.Setenv("CAISSE_BASE_URL", "https://celui-la.test")
	t.Setenv("CAISSE_ACCOUNT_MOOV_MONEY_CODE", "ACC_CAISSE")
	t.Setenv("CAISSE_ACCOUNT_MOOV_MONEY_SECRET", "sk_live_caisse")

	r, err := pvit.LireEnvironnement("CAISSE_")
	if err != nil {
		t.Fatalf("LireEnvironnement(CAISSE_) err = %v, want nil", err)
	}

	if got, want := r.BaseURL, "https://celui-la.test"; got != want {
		t.Errorf("BaseURL = %q, want %q", got, want)
	}
	if _, present := r.Comptes[pvit.OperateurAirtel]; present {
		t.Errorf("LireEnvironnement(CAISSE_) a lu un compte sous le prefixe PVIT_, want aucun")
	}
	if got, want := len(r.Comptes), 1; got != want {
		t.Errorf("LireEnvironnement(CAISSE_) a decouvert %d comptes, want %d", got, want)
	}

	// Les noms de variables se composent avec le même préfixe.
	if got, want := pvit.NomVariableMotDePasse("CAISSE_", pvit.OperateurMoov), "CAISSE_ACCOUNT_MOOV_MONEY_PASSWORD"; got != want {
		t.Errorf("NomVariableMotDePasse(CAISSE_) = %q, want %q", got, want)
	}
	if got, want := pvit.NomVariableCompte("", pvit.OperateurAirtel), "PVIT_ACCOUNT_AIRTEL_MONEY_CODE"; got != want {
		t.Errorf("NomVariableCompte() = %q, want %q", got, want)
	}
	if got, want := pvit.NomVariableSecret("", pvit.OperateurMoov), "PVIT_ACCOUNT_MOOV_MONEY_SECRET"; got != want {
		t.Errorf("NomVariableSecret() = %q, want %q", got, want)
	}
}

func TestUneDateDExpirationIllisibleEstSignalee(t *testing.T) {
	t.Setenv("PVIT_ACCOUNT_AIRTEL_MONEY_CODE", compteAirtel)
	t.Setenv("PVIT_ACCOUNT_AIRTEL_MONEY_SECRET", cleAmorce)
	t.Setenv("PVIT_ACCOUNT_AIRTEL_MONEY_SECRET_EXPIRES_AT", "demain matin")

	_, err := pvit.LireEnvironnement("")
	if !errors.Is(err, pvit.ErrConfiguration) {
		t.Errorf("LireEnvironnement() err = %v, want ErrConfiguration", err)
	}
	if err == nil || !strings.Contains(err.Error(), "RFC 3339") {
		t.Errorf("LireEnvironnement() err = %v, want un message citant le format attendu", err)
	}
}

func TestUneDateDExpirationValideEstLue(t *testing.T) {
	t.Setenv("PVIT_URL_CODE_PAYMENT", codePaiement)
	t.Setenv("PVIT_URL_CODE_STATUS", codeStatut)
	t.Setenv("PVIT_ACCOUNT_AIRTEL_MONEY_CODE", compteAirtel)
	t.Setenv("PVIT_ACCOUNT_AIRTEL_MONEY_SECRET", cleAmorce)
	t.Setenv("PVIT_ACCOUNT_AIRTEL_MONEY_SECRET_EXPIRES_AT", "2026-10-20T09:00:00Z")

	r, err := pvit.LireEnvironnement("")
	if err != nil {
		t.Fatalf("LireEnvironnement() err = %v, want nil", err)
	}
	want := time.Date(2026, time.October, 20, 9, 0, 0, 0, time.UTC)
	if got := r.Comptes[pvit.OperateurAirtel].ExpirationAmorce; !got.Equal(want) {
		t.Errorf("ExpirationAmorce = %v, want %v", got, want)
	}
}

// Les options de délai doivent effectivement borner le client HTTP construit par défaut.
func TestLesDelaisBornentLeClientParDefaut(t *testing.T) {
	t.Parallel()

	lent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(lent.Close)

	r := reglagesAmorceSeule()
	r.BaseURL = lent.URL

	// Pas de AvecClientHTTP : c'est le client interne qu'on veut eprouver.
	client, err := pvit.Nouveau(r, pvit.AvecDelais(20*time.Millisecond, 30*time.Millisecond))
	if err != nil {
		t.Fatalf("Nouveau() err = %v, want nil", err)
	}

	_, err = client.Payer(t.Context(), demandeType())
	if !errors.Is(err, pvit.ErrIndisponible) {
		t.Errorf("Payer() sur un serveur lent err = %v, want ErrIndisponible", err)
	}
	var refus pvit.Erreur
	if errors.As(err, &refus) && !refus.EstRejouable() {
		t.Errorf("Erreur.EstRejouable() = false sur un delai depasse, want true")
	}
	// La cause racine reste lisible : sans elle, le diagnostic se resume a
	// « indisponible », ce qui ne dit pas si c est un delai, un nom ou un certificat.
	if _, ok := errors.AsType[*url.Error](err); !ok {
		t.Errorf("Payer() err = %v, want une cause racine *url.Error accessible", err)
	}
}

// Un segment d'opérateur en minuscules est IGNORÉ, et non normalisé.
//
// Le normaliser serait un piège : les noms des deux autres variables du compte se composent
// à partir de l'opérateur, et un PVIT_ACCOUNT_airtel_money_CODE deviendrait AIRTEL_MONEY,
// dont le code serait alors cherché sous un nom qui n'existe pas. Le compte aurait l'air
// découvert et resterait vide, et le diagnostic parlerait d'un code absent plutôt que d'un
// nom de variable mal écrit.
func TestUnSegmentDOperateurEnMinusculesEstIgnore(t *testing.T) {
	t.Setenv("PVIT_URL_CODE_PAYMENT", codePaiement)
	t.Setenv("PVIT_URL_CODE_STATUS", codeStatut)
	t.Setenv("PVIT_ACCOUNT_AIRTEL_MONEY_CODE", compteAirtel)
	t.Setenv("PVIT_ACCOUNT_AIRTEL_MONEY_SECRET", cleAmorce)
	// Celui-ci est mal ecrit, et ne doit pas produire un compte a moitie lu.
	t.Setenv("PVIT_ACCOUNT_moov_money_CODE", compteMoov)
	t.Setenv("PVIT_ACCOUNT_moov_money_SECRET", cleAmorce)

	r, err := pvit.LireEnvironnement("")
	if err != nil {
		t.Fatalf("LireEnvironnement() err = %v, want nil", err)
	}

	if got, want := len(r.Comptes), 1; got != want {
		t.Errorf("LireEnvironnement() a decouvert %d comptes, want %d: %v", got, want, r.Comptes)
	}
	if compte, present := r.Comptes[pvit.OperateurMoov]; present {
		t.Errorf("un segment en minuscules a produit le compte %+v, want aucun", compte)
	}
	if got := r.Comptes[pvit.OperateurAirtel].CodeOperation; got != compteAirtel {
		t.Errorf("le compte bien ecrit a le code %q, want %q", got, compteAirtel)
	}

	// Et le client se cable quand meme, sur le seul compte lisible.
	client, err := pvit.NouveauDepuisEnvironnement("")
	if err != nil {
		t.Fatalf("NouveauDepuisEnvironnement() err = %v, want nil", err)
	}
	if got, want := len(client.OperateursConfigures()), 1; got != want {
		t.Errorf("OperateursConfigures() = %v, want %d entree", client.OperateursConfigures(), want)
	}
}

// UN PIÈGE À FORTE PROBABILITÉ, trouvé par l'audit.
//
// La validation taillait les espaces pour JUGER, puis la valeur brute partait sur le réseau.
// Une clé secrète recopiée depuis l'espace marchand avec un retour à la ligne, ce que fait
// n'importe quel copier-coller ou n'importe quelle variable d'environnement mal terminée,
// produisait un en-tête HTTP invalide et une erreur de transport que rien n'expliquait. Un
// code de compte avec une espace finale partait, lui, dans la charge utile, et se faisait
// refuser pour un appariement incorrect.
func TestLesValeursDeConfigurationSontTailleesAvantDePartir(t *testing.T) {
	t.Parallel()

	r := reglagesComplets()
	r.Comptes = map[pvit.Operateur]pvit.Compte{
		pvit.OperateurAirtel: {
			CodeOperation: "  " + compteAirtel + "  ",
			CleAmorce:     cleAmorce + "\n",
		},
	}
	client, b := nouveauBancAvecReglages(t, r)
	b.programmer("rest", reponse{statut: http.StatusOK, corps: corpsPaiement})

	demande := demandeType()
	demande.NumeroClient = "  074111111  "

	if _, err := client.Payer(t.Context(), demande); err != nil {
		t.Fatalf("Payer() err = %v, want nil: une cle avec un retour a la ligne doit partir taillee", err)
	}

	vue := b.derniere(t, "rest")

	if got, want := vue.Secret, cleAmorce; got != want {
		t.Errorf("X-Secret = %q, want %q", got, want)
	}
	if strings.ContainsAny(vue.Secret, " \t\r\n") {
		t.Errorf("X-Secret = %q, want aucune espace: un en-tete invalide casse l appel", vue.Secret)
	}

	corps := decoder(t, vue.Corps)
	if got := corps["merchant_operation_account_code"]; got != compteAirtel {
		t.Errorf("merchant_operation_account_code = %v, want %q", got, compteAirtel)
	}
	if got := corps["customer_account_number"]; got != "074111111" {
		t.Errorf("customer_account_number = %v, want %q", got, "074111111")
	}

	// Et le compte rendu par l'accesseur est propre lui aussi : c'est ce qu'un appelant
	// relit pour verifier sa configuration avant d'encaisser.
	compte, connu := client.CompteDe(pvit.OperateurAirtel)
	if !connu {
		t.Fatalf("CompteDe(airtel) introuvable")
	}
	if got, want := compte.CodeOperation, compteAirtel; got != want {
		t.Errorf("CompteDe().CodeOperation = %q, want %q", got, want)
	}
	if got, want := compte.CleAmorce, cleAmorce; got != want {
		t.Errorf("CompteDe().CleAmorce = %q, want %q", got, want)
	}
}

// Le mot de passe de rotation subit la même taille : un retour à la ligne dans un corps de
// formulaire partirait encodé, et la passerelle refuserait l'authentification sans dire
// pourquoi.
func TestLeMotDePasseEstTailleAvantDePartir(t *testing.T) {
	t.Parallel()

	r := reglagesComplets()
	r.Comptes = map[pvit.Operateur]pvit.Compte{
		pvit.OperateurAirtel: {
			CodeOperation: compteAirtel,
			MotDePasse:    "  " + motDePasse + "\n",
		},
	}
	client, b := nouveauBancAvecReglages(t, r)
	b.programmer("renew-secret", reponse{statut: http.StatusOK, corps: corpsRenouvellement})
	b.programmer("rest", reponse{statut: http.StatusOK, corps: corpsPaiement})

	if _, err := client.Payer(t.Context(), demandeType()); err != nil {
		t.Fatalf("Payer() err = %v, want nil", err)
	}

	champs, err := url.ParseQuery(b.derniere(t, "renew-secret").Corps)
	if err != nil {
		t.Fatalf("url.ParseQuery err = %v, want nil", err)
	}
	if got, want := champs.Get("password"), motDePasse; got != want {
		t.Errorf("password = %q, want %q", got, want)
	}
	if got, want := champs.Get("operationAccountCode"), compteAirtel; got != want {
		t.Errorf("operationAccountCode = %q, want %q", got, want)
	}
}

// Deux options dont une annulerait l'autre ne se tolèrent pas : accepter les deux ferait
// croire à l'appelant qu'il a borné ce qu'il n'a pas borné, ce qui est exactement le repli
// silencieux que ce paquet refuse.
func TestDeuxOptionsIncompatiblesEmpechentLaConstruction(t *testing.T) {
	t.Parallel()

	srv, _ := serveurMuet(t)
	r := reglagesComplets()
	r.BaseURL = srv.URL

	client, err := pvit.Nouveau(r,
		pvit.AvecClientHTTP(srv.Client()),
		pvit.AvecDelais(time.Second, 2*time.Second),
	)
	if !errors.Is(err, pvit.ErrConfiguration) {
		t.Errorf("Nouveau(AvecClientHTTP + AvecDelais) err = %v, want ErrConfiguration", err)
	}
	if client != nil {
		t.Errorf("Nouveau() a rendu un client malgre l erreur, want nil")
	}
	if err == nil || !strings.Contains(err.Error(), "ses propres delais") {
		t.Errorf("Nouveau() err = %v, want un message qui dit pourquoi", err)
	}

	// Et chacune seule reste acceptee.
	if _, err := pvit.Nouveau(r, pvit.AvecClientHTTP(srv.Client())); err != nil {
		t.Errorf("Nouveau(AvecClientHTTP seule) err = %v, want nil", err)
	}
	if _, err := pvit.Nouveau(r, pvit.AvecDelais(time.Second, 2*time.Second)); err != nil {
		t.Errorf("Nouveau(AvecDelais seule) err = %v, want nil", err)
	}
}
