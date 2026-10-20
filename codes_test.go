package pvit

import (
	"errors"
	"strings"
	"testing"
)

// clientDeTest câble un client sans réseau, pour éprouver la composition des URL.
func clientDeTest(t *testing.T, base string) *Client {
	t.Helper()
	c, err := Nouveau(Reglages{
		BaseURL: base,
		Codes: CodesURL{
			Secret: "CSEC", Paiement: "CPAY", Statut: "CSTA", Frais: "CFEE",
			Solde: "CBAL", KYC: "CKYC", Pays: "CPAY2", Operateurs: "COPS",
			Lien: "CLNK", QRCode: "CQR", Rapport: "CREP",
		},
		CodeCallback: "CB1234567890",
		Comptes: map[Operateur]Compte{
			OperateurAirtel: {CodeOperation: "ACC_1", CleAmorce: "sk_live_x"},
		},
	})
	if err != nil {
		t.Fatalf("Nouveau() err = %v, want nil", err)
	}
	return c
}

// Le préfixe de version N'EST PAS GLOBAL, et c'est la faute la plus probable de tout le
// paquet : la documentation donne le serveur comme se terminant par la version, alors que
// trois chemins ne la portent pas. Ce test est la seule garde contre un 404 à la première
// interrogation de statut.
func TestLesCheminsVersionnesEtNonVersionnes(t *testing.T) {
	t.Parallel()

	c := clientDeTest(t, "https://exemple.test")

	cas := []struct {
		nom  string
		ep   endpoint
		want string
	}{
		{nom: "renew-secret est versionne", ep: epSecret, want: "https://exemple.test/v2/CSEC/renew-secret"},
		{nom: "rest est versionne", ep: epPaiement, want: "https://exemple.test/v2/CPAY/rest"},
		{nom: "get-fees est versionne", ep: epFrais, want: "https://exemple.test/v2/CFEE/get-fees"},
		{nom: "kyc est versionne", ep: epKYC, want: "https://exemple.test/v2/CKYC/kyc"},
		{nom: "get-countries est versionne", ep: epPays, want: "https://exemple.test/v2/CPAY2/get-countries"},
		{nom: "get-operators est versionne", ep: epOperateurs, want: "https://exemple.test/v2/COPS/get-operators"},
		{nom: "generate-qr-code est versionne", ep: epQRCode, want: "https://exemple.test/v2/CQR/generate-qr-code"},
		{nom: "reports est versionne", ep: epRapport, want: "https://exemple.test/v2/CREP/reports"},

		{nom: "status n est PAS versionne", ep: epStatut, want: "https://exemple.test/CSTA/status"},
		{nom: "balance n est PAS versionne", ep: epSolde, want: "https://exemple.test/CBAL/balance"},
		{nom: "link n est PAS versionne", ep: epLien, want: "https://exemple.test/CLNK/link"},
	}

	for _, c2 := range cas {
		t.Run(c2.nom, func(t *testing.T) {
			t.Parallel()
			got, err := c.urlDe(c2.ep)
			if err != nil {
				t.Fatalf("urlDe(%s) err = %v, want nil", c2.ep.nom, err)
			}
			if got != c2.want {
				t.Errorf("urlDe(%s) = %q, want %q", c2.ep.nom, got, c2.want)
			}
		})
	}

	if got, want := len(endpoints), len(cas); got != want {
		t.Errorf("la table porte %d points d entree, et le test en couvre %d: want autant", got, want)
	}
}

// Quatre points d'entrée n'envoient pas de clé secrète, pour deux raisons distinctes.
//
// Trois ne la RÉCLAMENT PAS, et c'est utile au delà de l'économie d'un en-tête : ils
// fonctionnent sans aucun compte configuré, donc sans déclencher la moindre rotation. Le
// quatrième, le renouvellement, ne peut pas la porter puisque c'est lui qui l'obtient.
func TestQuiPorteLaCleSecreteEtQuiNon(t *testing.T) {
	t.Parallel()

	sansSecret := map[string]bool{
		"kyc": true, "get-countries": true, "get-operators": true,
		"renew-secret": true,
	}

	for _, e := range endpoints {
		t.Run(e.nom, func(t *testing.T) {
			t.Parallel()
			if got, want := e.secret, !sansSecret[e.nom]; got != want {
				t.Errorf("endpoint %s porte la cle secrete = %v, want %v", e.nom, got, want)
			}
		})
	}
}

// Le renouvellement de la clé ne porte pas de clé : c'est l'appel qui l'obtient. L'écrire
// autrement produirait une impasse au démarrage à froid, puisqu'aucune clé n'existe encore.
func TestLeRenouvellementNePorteJamaisDeCle(t *testing.T) {
	t.Parallel()

	if epSecret.secret {
		t.Errorf("epSecret.secret = true, want false: on ne peut pas signer la demande de la cle avec la cle")
	}
}

// La documentation écrit le serveur avec son suffixe de version. Un copier-coller doit donc
// produire les bonnes URL, sans double version.
func TestLaBaseURLEstNormalisee(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom  string
		base string
		want string
	}{
		{nom: "forme nue", base: "https://api.mypvit.pro", want: "https://api.mypvit.pro/v2/CPAY/rest"},
		{nom: "avec le suffixe de version de la documentation", base: "https://api.mypvit.pro/v2", want: "https://api.mypvit.pro/v2/CPAY/rest"},
		{nom: "avec une barre finale", base: "https://api.mypvit.pro/", want: "https://api.mypvit.pro/v2/CPAY/rest"},
		{nom: "avec version et barre finale", base: "https://api.mypvit.pro/v2/", want: "https://api.mypvit.pro/v2/CPAY/rest"},
		{nom: "avec des espaces", base: "  https://api.mypvit.pro  ", want: "https://api.mypvit.pro/v2/CPAY/rest"},
		{nom: "vide prend la production", base: "", want: BaseURLProduction + "/v2/CPAY/rest"},
	}

	for _, c2 := range cas {
		t.Run(c2.nom, func(t *testing.T) {
			t.Parallel()
			got, err := clientDeTest(t, c2.base).urlDe(epPaiement)
			if err != nil {
				t.Fatalf("urlDe(rest) err = %v, want nil", err)
			}
			if got != c2.want {
				t.Errorf("BaseURL %q donne %q, want %q", c2.base, got, c2.want)
			}
		})
	}
}

// Les codes des API accessoires sont vérifiés au moment de l'appel, et non au démarrage :
// exiger les onze ferait refuser de démarrer un projet qui n'en utilise que trois. Le
// message doit alors nommer l'API, ce qui suffit à savoir quel code aller chercher.
func TestUnCodeDURLManquantEstSignaleALAppelEtNommeLAPI(t *testing.T) {
	t.Parallel()

	c, err := Nouveau(Reglages{
		BaseURL: "https://exemple.test",
		// Seuls les deux codes exiges au demarrage sont poses.
		Codes:        CodesURL{Paiement: "CPAY", Statut: "CSTA"},
		CodeCallback: "CB1234567890",
		Comptes: map[Operateur]Compte{
			OperateurAirtel: {CodeOperation: "ACC_1", CleAmorce: "sk_live_x"},
		},
	})
	if err != nil {
		t.Fatalf("Nouveau() err = %v, want nil: les codes accessoires ne doivent pas bloquer le demarrage", err)
	}

	cas := []struct {
		ep      endpoint
		wantNom string
	}{
		{ep: epSolde, wantNom: "balance"},
		{ep: epFrais, wantNom: "get-fees"},
		{ep: epKYC, wantNom: "kyc"},
		{ep: epPays, wantNom: "get-countries"},
		{ep: epOperateurs, wantNom: "get-operators"},
		{ep: epLien, wantNom: "link"},
		{ep: epQRCode, wantNom: "generate-qr-code"},
		{ep: epRapport, wantNom: "reports"},
	}

	for _, c2 := range cas {
		t.Run(c2.wantNom, func(t *testing.T) {
			t.Parallel()
			_, err := c.urlDe(c2.ep)
			if !errors.Is(err, ErrConfiguration) {
				t.Errorf("urlDe(%s) err = %v, want ErrConfiguration", c2.ep.nom, err)
			}
			if err == nil || !strings.Contains(err.Error(), c2.wantNom) {
				t.Errorf("urlDe(%s) err = %v, want un message nommant %q", c2.ep.nom, err, c2.wantNom)
			}
		})
	}
}
