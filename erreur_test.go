package pvit

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// Le discriminant est le code applicatif, pas le code HTTP : un seul 403 couvre
// vingt-cinq cas distincts. Ce test épingle chaque code nommé sur sa conduite, et
// empêche par exemple un EstRejouable qui dirait oui sur un montant hors bornes.
func TestLesCodesNommesTombentSurLaBonneConduite(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom             string
		code            int
		wantMotif       error
		wantRejouable   bool
		wantPredicatNom string
	}{
		{nom: "ip marchand", code: CodeIPMarchandInvalide, wantMotif: ErrConfiguration, wantPredicatNom: "configuration"},
		{nom: "environnement melange", code: CodeEnvironnementInvalide, wantMotif: ErrConfiguration, wantPredicatNom: "configuration"},
		{nom: "solde insuffisant", code: CodeSoldeInsuffisant, wantMotif: ErrSoldeInsuffisant, wantRejouable: true, wantPredicatNom: "solde"},
		{nom: "plafond atteint", code: CodePlafondAtteint, wantMotif: ErrSoldeInsuffisant, wantRejouable: true, wantPredicatNom: "solde"},
		{nom: "client bloque", code: CodeClientBloque, wantMotif: ErrRefusClient, wantPredicatNom: "client"},
		{nom: "client inconnu", code: CodeClientInconnu, wantMotif: ErrRefusClient, wantPredicatNom: "client"},
		{nom: "appariement compte et operateur", code: CodeOperateurCompteDesaccord, wantMotif: ErrConfiguration, wantPredicatNom: "configuration"},
		{nom: "montant trop faible", code: CodeMontantTropFaible, wantMotif: ErrHorsBornes, wantPredicatNom: "bornes"},
		{nom: "montant trop eleve", code: CodeMontantMaxDepasse, wantMotif: ErrHorsBornes, wantPredicatNom: "bornes"},
		{nom: "authentification", code: CodeAuthentificationEchouee, wantMotif: ErrAuthentification, wantPredicatNom: "authentification"},
		{nom: "reference deja utilisee", code: CodeReferenceDejaUtilisee, wantMotif: ErrDejaInitiee, wantPredicatNom: "deja initiee"},
		{nom: "transaction expiree", code: CodeTransactionExpiree, wantMotif: ErrRequeteInvalide},
		{nom: "contrainte violee", code: CodeContrainteViolee, wantMotif: ErrRequeteInvalide, wantPredicatNom: "contrainte"},
		{nom: "maintenance", code: CodeMaintenance, wantMotif: ErrIndisponible, wantRejouable: true},
		{nom: "service indisponible", code: CodeServiceIndisponible, wantMotif: ErrIndisponible, wantRejouable: true},
		{nom: "envoi de cle echoue", code: CodeEnvoiCleEchoue, wantMotif: ErrIndisponible, wantRejouable: true},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()

			e := Erreur{Code: c.code, motif: motifDuCode(c.code)}
			if !errors.Is(e, c.wantMotif) {
				t.Errorf("motifDuCode(%d) = %v, want %v", c.code, e.motif, c.wantMotif)
			}
			if got := e.EstRejouable(); got != c.wantRejouable {
				t.Errorf("Erreur{Code: %d}.EstRejouable() = %v, want %v", c.code, got, c.wantRejouable)
			}

			// Un seul prédicat de famille doit répondre vrai, pour que l'appelant
			// n'ait jamais deux branches concurrentes sur la même erreur.
			predicats := map[string]bool{
				"deja initiee":     e.EstDejaInitiee(),
				"client":           e.EstRefusDuClient(),
				"bornes":           e.EstHorsBornes(),
				"configuration":    e.EstFauteDeConfiguration(),
				"authentification": e.EstAuthentification(),
				"solde":            e.EstSoldeInsuffisant(),
				"contrainte":       e.EstContrainteViolee(),
			}
			for nom, vrai := range predicats {
				if want := nom == c.wantPredicatNom; vrai != want {
					t.Errorf("Erreur{Code: %d} predicat %q = %v, want %v", c.code, nom, vrai, want)
				}
			}
		})
	}
}

// Le défaut est le rejouable, et c'est la décision la moins coûteuse : un rejeu différé
// sur une erreur de contrat se voit dans les journaux, tandis qu'un refus définitif
// prononcé sur une panne passagère perd un encaissement.
func TestUnCodeInconnuEstTraiteCommeIndisponible(t *testing.T) {
	t.Parallel()

	e := classer("paiement", OperateurAirtel, http.StatusInternalServerError,
		[]byte(`{"status_code":7777,"error":"QUELQUE_CHOSE_DE_NEUF","message":"inconnu"}`))

	if !errors.Is(e, ErrIndisponible) {
		t.Errorf("classer(code 7777) motif = %v, want ErrIndisponible", e.motif)
	}
	if !e.EstRejouable() {
		t.Errorf("classer(code 7777).EstRejouable() = false, want true")
	}
	if got, want := e.Code, 7777; got != want {
		t.Errorf("classer(code 7777).Code = %d, want %d", got, want)
	}
	if got, want := e.Nom, "QUELQUE_CHOSE_DE_NEUF"; got != want {
		t.Errorf("classer(code 7777).Nom = %q, want %q", got, want)
	}
}

// Le corps uniforme de la passerelle est lu en entier, et comparé en entier : un diff
// champ par champ dirait « le chemin diffère » sans montrer le reste.
func TestLeCorpsDErreurEstLuEnEntier(t *testing.T) {
	t.Parallel()

	corps := []byte(`{
		"date": "24102023-143000",
		"status_code": 8000,
		"error": "CONSTRAINT_VIOLATION",
		"message": "la requete porte des champs invalides",
		"messages": ["reference: trop longue", "amount: obligatoire"],
		"path": "/v2/abc/rest",
		"errors": {"reference": "trop longue", "amount": "obligatoire"}
	}`)

	got := classer("paiement", OperateurMoov, http.StatusUnprocessableEntity, corps)

	want := Erreur{
		Operation: "paiement",
		Operateur: OperateurMoov,
		Code:      CodeContrainteViolee,
		HTTP:      http.StatusUnprocessableEntity,
		Nom:       "CONSTRAINT_VIOLATION",
		Message:   "la requete porte des champs invalides",
		Messages:  []string{"reference: trop longue", "amount: obligatoire"},
		Chemin:    "/v2/abc/rest",
		Champs:    map[string]string{"reference": "trop longue", "amount": "obligatoire"},
		Date:      time.Date(2023, time.October, 24, 14, 30, 0, 0, time.UTC),
	}

	// Le motif est privé, donc hors du diff : errors.Is le vérifie à part.
	if diff := cmp.Diff(want, got, cmpopts.IgnoreUnexported(Erreur{})); diff != "" {
		t.Errorf("classer() a rendu un diff (-want +got):\n%s", diff)
	}
	if !errors.Is(got, ErrRequeteInvalide) {
		t.Errorf("classer(8000) motif = %v, want ErrRequeteInvalide", got.motif)
	}
	if !got.EstContrainteViolee() {
		t.Errorf("classer(8000).EstContrainteViolee() = false, want true")
	}
}

// La version 2 de encoding/json refuse un nom dupliqué et un UTF-8 invalide. Sur une
// charge utile qu'on ne contrôle pas, c'est un échec dur : le classement doit alors
// retomber sur le code HTTP plutôt que de perdre la décision.
func TestUnCorpsQueLaVersionDeuxRefuseRetombeSurLeCodeHTTP(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom       string
		statut    int
		corps     string
		wantMotif error
	}{
		{
			nom:       "nom duplique",
			statut:    http.StatusForbidden,
			corps:     `{"status_code":3018,"status_code":3019}`,
			wantMotif: ErrAuthentification,
		},
		{
			nom:       "utf8 invalide",
			statut:    http.StatusServiceUnavailable,
			corps:     "{\"message\":\"\xff\xfe\"}",
			wantMotif: ErrIndisponible,
		},
		{
			nom:       "page html d un proxy",
			statut:    http.StatusBadGateway,
			corps:     "<html><body>502 Bad Gateway</body></html>",
			wantMotif: ErrIndisponible,
		},
		{
			nom:       "corps vide",
			statut:    http.StatusNotFound,
			corps:     "",
			wantMotif: ErrRequeteInvalide,
		},
		{
			nom:       "corps tronque",
			statut:    http.StatusTooManyRequests,
			corps:     `{"status_code":`,
			wantMotif: ErrIndisponible,
		},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()
			got := classer("statut", "", c.statut, []byte(c.corps))
			if !errors.Is(got, c.wantMotif) {
				t.Errorf("classer(http %d, %q) motif = %v, want %v",
					c.statut, c.corps, got.motif, c.wantMotif)
			}
			if got.Code != 0 {
				t.Errorf("classer(http %d, corps illisible).Code = %d, want 0",
					c.statut, got.Code)
			}
		})
	}
}

// Une page d'erreur HTML d'un proxy entrerait entière dans chaque ligne de journal qui
// reprend le message : elle est bornée.
func TestLeCorpsDErreurEstTronque(t *testing.T) {
	t.Parallel()

	corps := "<html>" + strings.Repeat("A", 10_000) + "</html>"
	got := classer("paiement", OperateurAirtel, http.StatusBadGateway, []byte(corps))

	if n := len(got.Message); n > longueurMaxRaison+3 {
		t.Errorf("classer(10 Ko de html).Message pese %d octets, want au plus %d",
			n, longueurMaxRaison+3)
	}
	if !strings.HasSuffix(got.Message, "...") {
		t.Errorf("classer(10 Ko de html).Message = %q, want une marque de troncature", got.Message)
	}
}

// Les retours à la ligne sont aplatis : un message multiligne casserait l'alignement
// d'un journal structuré, où chaque entrée tient sur une ligne.
func TestUnMessageMultiligneEstAplati(t *testing.T) {
	t.Parallel()

	got := classer("solde", "", http.StatusInternalServerError,
		[]byte("erreur\nsur\r\nplusieurs lignes"))

	if strings.ContainsAny(got.Message, "\r\n") {
		t.Errorf("classer().Message = %q, want aucun retour a la ligne", got.Message)
	}
}

func TestLeMessageDUneErreurNommeLOperationEtLeCode(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom  string
		err  Erreur
		want string
	}{
		{
			nom: "complet",
			err: Erreur{
				Operation: "paiement", Operateur: OperateurAirtel, HTTP: 403,
				Code: 3018, Nom: "TRANSACTION_AMOUNT_TOO_LOW", Message: "montant trop faible",
			},
			want: "passerelle pvit paiement AIRTEL_MONEY: http 403, code 3018 TRANSACTION_AMOUNT_TOO_LOW: montant trop faible",
		},
		{
			nom:  "sans operateur",
			err:  Erreur{Operation: "pays", HTTP: 500, Code: 500, Nom: "SOMETHING_WENT_WRONG"},
			want: "passerelle pvit pays: http 500, code 500 SOMETHING_WENT_WRONG",
		},
		{
			nom:  "corps illisible, le motif prend le relais",
			err:  Erreur{Operation: "statut", HTTP: 502, motif: ErrIndisponible},
			want: "passerelle pvit statut: http 502: la passerelle est indisponible",
		},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()
			if got := c.err.Error(); got != c.want {
				t.Errorf("Erreur.Error() = %q, want %q", got, c.want)
			}
		})
	}
}

// La rotation impossible emballe la configuration et non l'authentification, et c'est la
// distinction utile : le geste correctif est de renseigner un mot de passe, pas de
// réessayer. Une boucle de réconciliation ne doit donc pas s'acharner dessus.
func TestLaRotationImpossibleNEstPasRejouable(t *testing.T) {
	t.Parallel()

	if !errors.Is(ErrRotationImpossible, ErrConfiguration) {
		t.Errorf("ErrRotationImpossible n emballe pas ErrConfiguration")
	}
	if errors.Is(ErrRotationImpossible, ErrAuthentification) {
		t.Errorf("ErrRotationImpossible emballe ErrAuthentification, want non")
	}

	e := Erreur{Operation: "renouvellement", Operateur: OperateurAirtel, motif: ErrRotationImpossible}
	if e.EstRejouable() {
		t.Errorf("Erreur{motif: ErrRotationImpossible}.EstRejouable() = true, want false")
	}
}

// Le repli sur le code HTTP doit garder le 4xx non rejouable et tout le reste rejouable.
func TestLeRepliSurLeCodeHTTP(t *testing.T) {
	t.Parallel()

	cas := []struct {
		statut int
		want   error
	}{
		{statut: http.StatusUnauthorized, want: ErrAuthentification},
		{statut: http.StatusForbidden, want: ErrAuthentification},
		{statut: http.StatusPaymentRequired, want: ErrSoldeInsuffisant},
		{statut: http.StatusTooManyRequests, want: ErrIndisponible},
		{statut: http.StatusBadRequest, want: ErrRequeteInvalide},
		{statut: http.StatusNotFound, want: ErrRequeteInvalide},
		{statut: http.StatusConflict, want: ErrRequeteInvalide},
		{statut: http.StatusUnprocessableEntity, want: ErrRequeteInvalide},
		{statut: http.StatusInternalServerError, want: ErrIndisponible},
		{statut: http.StatusBadGateway, want: ErrIndisponible},
		{statut: http.StatusServiceUnavailable, want: ErrIndisponible},
		{statut: http.StatusOK, want: ErrIndisponible},
	}

	for _, c := range cas {
		t.Run(http.StatusText(c.statut), func(t *testing.T) {
			t.Parallel()
			if got := motifDuStatut(c.statut); !errors.Is(got, c.want) {
				t.Errorf("motifDuStatut(%d) = %v, want %v", c.statut, got, c.want)
			}
		})
	}
}
