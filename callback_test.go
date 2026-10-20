package pvit_test

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pvit "github.com/bangaromaric/pvit-go"
	"github.com/google/go-cmp/cmp"
)

// La charge utile que la documentation de la passerelle publie, verbatim.
const corpsCallbackReel = `{
	"transactionId": "PAY23102400001",
	"merchantReferenceId": "REF1234567890",
	"status": "SUCCESS",
	"amount": 150,
	"customerID": "074111111",
	"fees": 3.5,
	"totalAmount": 155,
	"chargeOwner": "CUSTOMER",
	"freeInfo": "Test payment",
	"transactionOperation": "PAYMENT",
	"code": 200,
	"operator": "AIRTEL_MONEY",
	"accountOperationCode": "ACC_1XXXXXXXX",
	"operatorOwnerCharge": "CUSTOMER",
	"operatorFees": 1.5,
	"amountCredited": 150
}`

// La notification réelle doit se lire en entier, casse mêlée et champ à deux majuscules
// compris. Elle porte aussi les DEUX étages de frais, que le calculateur de frais ne donne
// jamais ensemble.
func TestLaNotificationReelleEstLueEnEntier(t *testing.T) {
	t.Parallel()

	got, err := pvit.DecoderCallback([]byte(corpsCallbackReel))
	if err != nil {
		t.Fatalf("DecoderCallback() err = %v, want nil", err)
	}

	want := pvit.Callback{
		TransactionID:              "PAY23102400001",
		Reference:                  "REF1234567890",
		Statut:                     pvit.StatutSucces,
		Montant:                    150,
		NumeroClient:               "074111111",
		Frais:                      3500,
		FraisOperateur:             1500,
		Total:                      155000,
		MontantCredite:             150,
		ProprietaireFrais:          pvit.ProprietaireClient,
		ProprietaireFraisOperateur: pvit.ProprietaireClient,
		InfoLibre:                  "Test payment",
		Operation:                  pvit.OperationPaiement,
		Code:                       200,
		Operateur:                  pvit.OperateurAirtel,
		CompteOperation:            "ACC_1XXXXXXXX",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("DecoderCallback() a rendu un diff (-want +got):\n%s", diff)
	}

	// Les frais fractionnaires sont conservés au millième, et non arrondis en silence.
	if gotFrais, wantFrais := got.Frais.String(), "3,5 XAF"; gotFrais != wantFrais {
		t.Errorf("Callback.Frais = %q, want %q", gotFrais, wantFrais)
	}
	if gotFrais, wantFrais := got.FraisOperateur.String(), "1,5 XAF"; gotFrais != wantFrais {
		t.Errorf("Callback.FraisOperateur = %q, want %q", gotFrais, wantFrais)
	}
}

// LE TEST QUI DÉCIDE DE L'OUVERTURE DU COMPTE DE PRODUCTION.
//
// L'accusé réémet le code REÇU, sous un autre nom. Écrire 200 en dur marque la transaction
// « échec de notification » chez la passerelle, et sa documentation en fait une condition de
// validation des tests d'intégration. Ce test envoie donc exprès un code qui n'est pas 200.
func TestLEchoDuCodeEstDynamiqueEtNonDeuxCentsEnDur(t *testing.T) {
	t.Parallel()

	cas := []int{200, 201, 202, 409, 500}

	for _, code := range cas {
		t.Run(http.StatusText(code), func(t *testing.T) {
			t.Parallel()

			corps := strings.Replace(corpsCallbackReel, `"code": 200`, `"code": `+itoa(code), 1)
			notification, err := pvit.DecoderCallback([]byte(corps))
			if err != nil {
				t.Fatalf("DecoderCallback() err = %v, want nil", err)
			}
			if got := notification.Code; got != code {
				t.Fatalf("Callback.Code = %d, want %d", got, code)
			}

			accuse, err := notification.Accuse()
			if err != nil {
				t.Fatalf("Accuse() err = %v, want nil", err)
			}

			var lu pvit.AccuseReception
			if err := json.Unmarshal(accuse, &lu); err != nil {
				t.Fatalf("json.Unmarshal(%s) err = %v, want nil", accuse, err)
			}
			if got, want := lu.ResponseCode, code; got != want {
				t.Errorf("Accuse() responseCode = %d, want %d: l echo doit etre dynamique", got, want)
			}
			if got, want := lu.TransactionID, "PAY23102400001"; got != want {
				t.Errorf("Accuse() transactionId = %q, want %q", got, want)
			}
			// Le renommage est la seconde moitié du piège : « code » à l'aller,
			// « responseCode » au retour.
			if !strings.Contains(string(accuse), `"responseCode"`) {
				t.Errorf("Accuse() = %s, want un champ responseCode", accuse)
			}
			if strings.Contains(string(accuse), `"code"`) {
				t.Errorf("Accuse() = %s, want aucun champ code: il se renomme au retour", accuse)
			}
		})
	}
}

// itoa évite d'importer strconv pour un seul appel dans un nom de sous-test.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var chiffres []byte
	for n > 0 {
		chiffres = append([]byte{byte('0' + n%10)}, chiffres...)
		n /= 10
	}
	return string(chiffres)
}

// Accuser écrit sur un http.ResponseWriter, interface de la bibliothèque standard : c'est
// la preuve que le writer d'un contexte Gin suffira, sans adaptateur ni réflexion.
func TestAccuserEcritSurUnResponseWriterStandard(t *testing.T) {
	t.Parallel()

	notification, err := pvit.DecoderCallback([]byte(corpsCallbackReel))
	if err != nil {
		t.Fatalf("DecoderCallback() err = %v, want nil", err)
	}

	w := httptest.NewRecorder()
	if err := pvit.Accuser(w, notification); err != nil {
		t.Fatalf("Accuser() err = %v, want nil", err)
	}

	if got, want := w.Code, http.StatusOK; got != want {
		t.Errorf("Accuser() statut = %d, want %d", got, want)
	}
	if got, want := w.Header().Get("Content-Type"), "application/json"; got != want {
		t.Errorf("Accuser() type de contenu = %q, want %q", got, want)
	}
	want := `{"transactionId":"PAY23102400001","responseCode":200}`
	if got := strings.TrimSpace(w.Body.String()); got != want {
		t.Errorf("Accuser() corps = %s, want %s", got, want)
	}
}

// Acquitter un traitement qui a échoué perdrait la notification pour toujours, et donc
// l'argent qu'elle annonce. Le gestionnaire doit donc répondre en erreur, SANS accusé.
func TestLeGestionnaireNAcquittePasUnTraitementEnEchec(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom        string
		wantStatut int
		wantAccuse bool
	}{
		{nom: "traitement reussi", wantStatut: http.StatusOK, wantAccuse: true},
		{nom: "traitement en echec", wantStatut: http.StatusInternalServerError, wantAccuse: false},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()

			var vue pvit.Callback
			gestionnaire := pvit.GestionnaireCallback(func(_ context.Context, n pvit.Callback) error {
				vue = n
				if !c.wantAccuse {
					return errors.New("le stockage est indisponible")
				}
				return nil
			})

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/pvit/callback", strings.NewReader(corpsCallbackReel))
			gestionnaire.ServeHTTP(w, req)

			if got := w.Code; got != c.wantStatut {
				t.Errorf("le gestionnaire a rendu %d, want %d", got, c.wantStatut)
			}
			if got, want := vue.TransactionID, "PAY23102400001"; got != want {
				t.Errorf("le traitement a recu l identifiant %q, want %q", got, want)
			}
			porteAccuse := strings.Contains(w.Body.String(), "responseCode")
			if porteAccuse != c.wantAccuse {
				t.Errorf("le gestionnaire a rendu un accuse = %v, want %v (corps %q)",
					porteAccuse, c.wantAccuse, w.Body.String())
			}
		})
	}
}

func TestLeGestionnaireRefuseToutCeQuiNEstPasUnPost(t *testing.T) {
	t.Parallel()

	gestionnaire := pvit.GestionnaireCallback(nil)

	for _, methode := range []string{http.MethodGet, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		t.Run(methode, func(t *testing.T) {
			t.Parallel()
			w := httptest.NewRecorder()
			gestionnaire.ServeHTTP(w, httptest.NewRequest(methode, "/pvit/callback", nil))
			if got, want := w.Code, http.StatusMethodNotAllowed; got != want {
				t.Errorf("le gestionnaire a rendu %d sur %s, want %d", got, methode, want)
			}
			if got, want := w.Header().Get("Allow"), http.MethodPost; got != want {
				t.Errorf("en-tete Allow = %q, want %q", got, want)
			}
		})
	}
}

// Une notification malformée ne doit ni faire paniquer, ni être acquittée : la passerelle
// doit réessayer plutôt que de tenir pour remis ce qu'on n'a pas compris.
func TestUneNotificationMalformeeNeFaitPasPaniquerEtNEstPasAcquittee(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom   string
		corps string
	}{
		{nom: "json tronque", corps: `{"transactionId":"PAY1","code":`},
		{nom: "objet vide", corps: `{}`},
		{nom: "valeur nulle", corps: `null`},
		{nom: "tableau", corps: `[{"transactionId":"PAY1"}]`},
		{nom: "sans identifiant", corps: `{"code":200,"status":"SUCCESS"}`},
		{nom: "sans code", corps: `{"transactionId":"PAY1","status":"SUCCESS"}`},
		{nom: "texte libre", corps: `pas du json`},
		{nom: "nom duplique, refuse par la version deux", corps: `{"code":200,"code":201,"transactionId":"PAY1"}`},
		{nom: "utf8 invalide", corps: "{\"transactionId\":\"\xff\xfe\",\"code\":200}"},
		{nom: "corps vide", corps: ``},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()

			if _, err := pvit.DecoderCallback([]byte(c.corps)); err == nil {
				t.Errorf("DecoderCallback(%q) err = nil, want une erreur", c.corps)
			}

			appele := false
			gestionnaire := pvit.GestionnaireCallback(func(context.Context, pvit.Callback) error {
				appele = true
				return nil
			})
			w := httptest.NewRecorder()
			gestionnaire.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/cb", strings.NewReader(c.corps)))

			if appele {
				t.Errorf("le traitement a ete appele sur une notification malformee, want non")
			}
			if got, want := w.Code, http.StatusBadRequest; got != want {
				t.Errorf("le gestionnaire a rendu %d, want %d", got, want)
			}
			if strings.Contains(w.Body.String(), "responseCode") {
				t.Errorf("le gestionnaire a acquitte une notification malformee: %q", w.Body.String())
			}
		})
	}
}

// Le corps est borné : une route exposée au monde ne doit pas pouvoir remplir la mémoire du
// service.
func TestUnCorpsDeNotificationTropGrosEstRefuse(t *testing.T) {
	t.Parallel()

	gestionnaire := pvit.GestionnaireCallback(func(context.Context, pvit.Callback) error { return nil })

	enorme := `{"transactionId":"PAY1","code":200,"freeInfo":"` + strings.Repeat("A", pvit.TailleMaxCallback+1024) + `"}`
	w := httptest.NewRecorder()
	gestionnaire.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/cb", strings.NewReader(enorme)))

	if got, want := w.Code, http.StatusRequestEntityTooLarge; got != want {
		t.Errorf("le gestionnaire a rendu %d sur un corps de %d octets, want %d", got, len(enorme), want)
	}
}

// Un accusé ne s'invente pas : sans identifiant ni code, il n'y a rien à réémettre, et en
// forger un invaliderait la recette autant que d'écrire 200 en dur.
func TestUnAccuseNeSInventePas(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom        string
		callback   pvit.Callback
		wantRefuse bool
	}{
		{nom: "complet", callback: pvit.Callback{TransactionID: "PAY1", Code: 200}},
		{nom: "sans identifiant", callback: pvit.Callback{Code: 200}, wantRefuse: true},
		{nom: "sans code", callback: pvit.Callback{TransactionID: "PAY1"}, wantRefuse: true},
		{nom: "vide", callback: pvit.Callback{}, wantRefuse: true},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()
			_, err := c.callback.Accuse()
			if c.wantRefuse {
				if !errors.Is(err, pvit.ErrAccuseImpossible) {
					t.Errorf("Accuse() err = %v, want ErrAccuseImpossible", err)
				}
				return
			}
			if err != nil {
				t.Errorf("Accuse() err = %v, want nil", err)
			}
		})
	}
}

// L'option de borne doit être respectée par le gestionnaire du client, puisque le
// paramétrage est la raison d'être de ces options.
func TestLaBorneDuGestionnaireSuitLOptionDuClient(t *testing.T) {
	t.Parallel()

	client, _ := nouveauBancAvecReglages(t, reglagesAmorceSeule(), pvit.AvecTailleMaxCallback(2048))
	gestionnaire := client.GestionnaireCallback(func(context.Context, pvit.Callback) error { return nil })

	corps := `{"transactionId":"PAY1","code":200,"freeInfo":"` + strings.Repeat("A", 4096) + `"}`
	w := httptest.NewRecorder()
	gestionnaire.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/cb", strings.NewReader(corps)))

	if got, want := w.Code, http.StatusRequestEntityTooLarge; got != want {
		t.Errorf("le gestionnaire borne a 2048 a rendu %d sur %d octets, want %d", got, len(corps), want)
	}
}

// LE DÉFAUT LE PLUS GRAVE QUE L'AUDIT A TROUVÉ.
//
// Une somme illisible rendait zéro en silence, sur une notification qui annonce un succès :
// l'appelant aurait crédité zéro franc et marqué la transaction payée. Elle rend désormais
// une erreur, le gestionnaire n'acquitte pas, la passerelle réessaie, et la vérité se
// reprend par l'interrogation de statut.
func TestUneSommeIllisibleDansUneNotificationNEstPasAcquittee(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom       string
		corps     string
		wantErr   error
		wantMoney pvit.Montant
	}{
		{
			nom:     "montant a demi franc",
			corps:   `{"transactionId":"PAY1","status":"SUCCESS","code":200,"amount":150.5}`,
			wantErr: pvit.ErrMontantFractionnaire,
		},
		{
			nom:     "montant en texte libre",
			corps:   `{"transactionId":"PAY1","status":"SUCCESS","code":200,"amount":"beaucoup"}`,
			wantErr: pvit.ErrReponseIllisible,
		},
		{
			nom:     "montant credite a demi franc",
			corps:   `{"transactionId":"PAY1","status":"SUCCESS","code":200,"amountCredited":149.5}`,
			wantErr: pvit.ErrMontantFractionnaire,
		},
		{
			nom:     "frais a quatre decimales",
			corps:   `{"transactionId":"PAY1","status":"SUCCESS","code":200,"fees":3.5001}`,
			wantErr: pvit.ErrReponseIllisible,
		},
		{
			nom:     "frais operateur illisibles",
			corps:   `{"transactionId":"PAY1","status":"SUCCESS","code":200,"operatorFees":{"x":1}}`,
			wantErr: pvit.ErrReponseIllisible,
		},
		{
			nom:   "sommes absentes, ce qui est legitime",
			corps: `{"transactionId":"PAY1","status":"SUCCESS","code":200}`,
		},
		{
			nom:   "sommes nulles, la passerelle dit pas de valeur",
			corps: `{"transactionId":"PAY1","status":"SUCCESS","code":200,"amount":null,"fees":null}`,
		},
		{
			nom:       "somme entiere, le chemin nominal",
			corps:     `{"transactionId":"PAY1","status":"SUCCESS","code":200,"amount":150}`,
			wantMoney: 150,
		},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()

			notification, err := pvit.DecoderCallback([]byte(c.corps))

			if c.wantErr != nil {
				if !errors.Is(err, c.wantErr) {
					t.Errorf("DecoderCallback(%s) err = %v, want %v", c.corps, err, c.wantErr)
				}
			} else {
				if err != nil {
					t.Errorf("DecoderCallback(%s) err = %v, want nil", c.corps, err)
				}
				if notification.Montant != c.wantMoney {
					t.Errorf("DecoderCallback(%s) Montant = %d, want %d", c.corps, notification.Montant, c.wantMoney)
				}
			}

			// Et le gestionnaire ne doit ni appeler le traitement, ni acquitter.
			appele := false
			gestionnaire := pvit.GestionnaireCallback(func(context.Context, pvit.Callback) error {
				appele = true
				return nil
			})
			w := httptest.NewRecorder()
			gestionnaire.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/cb", strings.NewReader(c.corps)))

			wantAcquitte := c.wantErr == nil
			if appele != wantAcquitte {
				t.Errorf("le traitement a ete appele = %v, want %v", appele, wantAcquitte)
			}
			if porteAccuse := strings.Contains(w.Body.String(), "responseCode"); porteAccuse != wantAcquitte {
				t.Errorf("le gestionnaire a acquitte = %v, want %v (statut %d)", porteAccuse, wantAcquitte, w.Code)
			}
		})
	}
}

// Les énumérations sont normalisées en capitales : sans cela, un « payment » en minuscules
// ne serait égal à aucune constante du paquet, et la comparaison échouerait en silence.
func TestLesEnumerationsDuCallbackSontNormalisees(t *testing.T) {
	t.Parallel()

	corps := `{
		"transactionId":"PAY1","code":200,
		"status":"success",
		"transactionOperation":"payment",
		"operator":"airtel_money",
		"chargeOwner":"customer",
		"operatorOwnerCharge":" merchant "
	}`

	notification, err := pvit.DecoderCallback([]byte(corps))
	if err != nil {
		t.Fatalf("DecoderCallback() err = %v, want nil", err)
	}

	cas := []struct {
		nom  string
		got  any
		want any
	}{
		{nom: "statut", got: notification.Statut, want: pvit.StatutSucces},
		{nom: "operation", got: notification.Operation, want: pvit.OperationPaiement},
		{nom: "operateur", got: notification.Operateur, want: pvit.OperateurAirtel},
		{nom: "proprietaire des frais", got: notification.ProprietaireFrais, want: pvit.ProprietaireClient},
		{nom: "proprietaire des frais operateur", got: notification.ProprietaireFraisOperateur, want: pvit.ProprietaireMarchand},
	}
	for _, c := range cas {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.nom, c.got, c.want)
		}
	}
	if !notification.Statut.EstSucces() {
		t.Errorf("Statut.EstSucces() = false sur un success en minuscules, want true")
	}
}

// Un traitement absent est une faute d'appel, et acquitter sans rien enregistrer perdrait la
// notification, donc l'argent qu'elle annonce, sans que personne le voie. Refuser fait
// réessayer la passerelle, et finit par se voir.
func TestUnGestionnaireSansTraitementNAcquittePas(t *testing.T) {
	t.Parallel()

	gestionnaire := pvit.GestionnaireCallback(nil)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/cb", strings.NewReader(corpsCallbackReel))
	gestionnaire.ServeHTTP(w, req)

	if got, want := w.Code, http.StatusInternalServerError; got != want {
		t.Errorf("le gestionnaire sans traitement a rendu %d, want %d", got, want)
	}
	if strings.Contains(w.Body.String(), "responseCode") {
		t.Errorf("le gestionnaire sans traitement a acquitte: %q", w.Body.String())
	}
}
