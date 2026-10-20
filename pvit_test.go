package pvit_test

import (
	"net/http"
	"net/http/httptest"
	"path"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	pvit "github.com/bangaromaric/pvit-go"
)

// Les tests de ce fichier parlent au niveau HTTP, contre un serveur simulé, et JAMAIS à un
// simulacre du client HTTP.
//
// La distinction n'est pas théorique. Simuler le client vérifierait que le paquet appelle
// bien le simulacre, ce qui est toujours vrai et n'apprend rien. Ce qu'on veut éprouver est
// la REQUÊTE RÉELLEMENT ÉMISE : un champ manquant que le serveur exige, un préfixe de
// version posé sur un chemin qui n'en veut pas, une clé secrète envoyée à un point d'entrée
// qui n'en demande pas, un mot de passe recopié dans une URL.
//
// Un seul serveur sert les onze routes, et il compte ses appels. C'est ce qui permet de
// répondre à des questions comme « combien de renouvellements pour dix paiements », qui est
// la seule façon de prouver qu'un cache de clé fonctionne.

// Les codes d'URL du faux marchand. Onze valeurs, parce que la passerelle en veut onze.
const (
	codeSecret     = "CODESECRET"
	codePaiement   = "CODEPAY"
	codeStatut     = "CODESTAT"
	codeFrais      = "CODEFEES"
	codeSolde      = "CODEBAL"
	codeKYC        = "CODEKYC"
	codePays       = "CODEPAYS"
	codeOperateurs = "CODEOPS"
	codeLien       = "CODELINK"
	codeQRCode     = "CODEQR"
	codeRapport    = "CODEREP"
	codeCallback   = "CB1234567890"
)

// Les deux comptes de production, un par famille d'opérateur.
const (
	compteAirtel = "ACC_1AIRTEL"
	compteMoov   = "ACC_2MOOV"
	motDePasse   = "mot-de-passe-api"
	cleAmorce    = "sk_live_amorce"
)

// recue est une requête telle que le serveur l'a vue.
//
// Les champs sont exportés pour que cmp.Diff les compare : une comparaison champ par champ
// dirait « le type de contenu diffère » sans montrer le reste de la requête, là où un diff
// de la structure entière met l'erreur en contexte.
type recue struct {
	Methode           string
	Chemin            string
	Query             string
	Secret            string
	TypeContenu       string
	MediaTypeCallback string
	Accept            string
	Agent             string
	Corps             string
}

// reponse est une réponse programmée.
type reponse struct {
	statut int
	corps  string
}

// horloge est une source de temps que le test avance à la main.
//
// Aucune mise en sommeil : les règles de test du dépôt l'interdisent comme moyen de
// synchronisation, et un test qui attend cinquante minutes pour voir une clé expirer ne
// serait pas un test.
type horloge struct {
	mu      sync.Mutex
	instant time.Time
}

func nouvelleHorloge() *horloge {
	return &horloge{instant: time.Date(2026, time.October, 20, 8, 0, 0, 0, time.UTC)}
}

func (h *horloge) maintenant() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.instant
}

func (h *horloge) avancer(d time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.instant = h.instant.Add(d)
}

// banc est le faux marchand : un serveur, des réponses programmées, et le relevé des
// requêtes vues.
type banc struct {
	srv     *httptest.Server
	horloge *horloge

	mu     sync.Mutex
	recues []recue
	files  map[string][]reponse
}

// nouveauBanc monte un client branché sur un serveur simulé.
func nouveauBanc(t *testing.T, options ...pvit.Option) (*pvit.Client, *banc) {
	t.Helper()
	return nouveauBancAvecReglages(t, reglagesComplets(), options...)
}

// reglagesComplets rend la configuration des deux comptes de production.
func reglagesComplets() pvit.Reglages {
	return pvit.Reglages{
		Codes: pvit.CodesURL{
			Secret: codeSecret, Paiement: codePaiement, Statut: codeStatut,
			Frais: codeFrais, Solde: codeSolde, KYC: codeKYC, Pays: codePays,
			Operateurs: codeOperateurs, Lien: codeLien, QRCode: codeQRCode, Rapport: codeRapport,
		},
		CodeCallback: codeCallback,
		Comptes: map[pvit.Operateur]pvit.Compte{
			pvit.OperateurAirtel: {CodeOperation: compteAirtel, MotDePasse: motDePasse},
			pvit.OperateurMoov:   {CodeOperation: compteMoov, MotDePasse: motDePasse},
		},
	}
}

// nouveauBancAvecReglages monte un client sur des réglages donnés.
func nouveauBancAvecReglages(t *testing.T, r pvit.Reglages, options ...pvit.Option) (*pvit.Client, *banc) {
	t.Helper()

	b := &banc{horloge: nouvelleHorloge(), files: make(map[string][]reponse)}
	b.srv = httptest.NewServer(http.HandlerFunc(b.servir))
	t.Cleanup(b.srv.Close)

	r.BaseURL = b.srv.URL
	options = append([]pvit.Option{
		pvit.AvecClientHTTP(b.srv.Client()),
		pvit.AvecHorloge(b.horloge.maintenant),
	}, options...)

	client, err := pvit.Nouveau(r, options...)
	if err != nil {
		t.Fatalf("pvit.Nouveau() err = %v, want nil", err)
	}
	return client, b
}

// servir enregistre la requête, puis rend la réponse programmée pour ce point d'entrée.
func (b *banc) servir(w http.ResponseWriter, req *http.Request) {
	corps := make([]byte, 0, 512)
	if req.Body != nil {
		tampon := make([]byte, 4096)
		for {
			n, err := req.Body.Read(tampon)
			corps = append(corps, tampon[:n]...)
			if err != nil {
				break
			}
		}
	}

	nom := path.Base(req.URL.Path)
	b.mu.Lock()
	b.recues = append(b.recues, recue{
		Methode:           req.Method,
		Chemin:            req.URL.Path,
		Query:             req.URL.RawQuery,
		Secret:            req.Header.Get("X-Secret"),
		TypeContenu:       req.Header.Get("Content-Type"),
		MediaTypeCallback: req.Header.Get("X-Callback-MediaType"),
		Accept:            req.Header.Get("Accept"),
		Agent:             req.Header.Get("User-Agent"),
		Corps:             string(corps),
	})
	rep := b.suivante(nom)
	b.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(rep.statut)
	_, _ = w.Write([]byte(rep.corps))
}

// suivante consomme la réponse programmée pour un point d'entrée. La dernière se répète,
// pour qu'un test qui boucle n'ait pas à programmer autant de réponses que de tours.
func (b *banc) suivante(nom string) reponse {
	file := b.files[nom]
	switch len(file) {
	case 0:
		return reponse{statut: http.StatusOK, corps: `{"status_code":"200","status":"PENDING","reference_id":"PAY0001"}`}
	case 1:
		return file[0]
	default:
		b.files[nom] = file[1:]
		return file[0]
	}
}

// programmer pose la suite des réponses d'un point d'entrée, désigné par son dernier
// segment de chemin.
func (b *banc) programmer(nom string, reponses ...reponse) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.files[nom] = reponses
}

// vues rend le relevé des requêtes, dans l'ordre.
func (b *banc) vues() []recue {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]recue(nil), b.recues...)
}

// comptePour rend le nombre d'appels vus sur un point d'entrée.
func (b *banc) comptePour(nom string) int {
	n := 0
	for _, r := range b.vues() {
		if path.Base(r.Chemin) == nom {
			n++
		}
	}
	return n
}

// derniere rend la dernière requête vue sur un point d'entrée.
func (b *banc) derniere(t *testing.T, nom string) recue {
	t.Helper()
	vues := b.vues()
	for _, vue := range slices.Backward(vues) {
		if path.Base(vue.Chemin) == nom {
			return vue
		}
	}
	t.Fatalf("aucune requete vue sur %q, vues = %v", nom, cheminsVus(vues))
	return recue{}
}

// cheminsVus rend la liste des chemins appelés, pour un message d'échec lisible.
func cheminsVus(vues []recue) []string {
	chemins := make([]string, 0, len(vues))
	for _, r := range vues {
		chemins = append(chemins, r.Chemin)
	}
	return chemins
}

// Les réponses que la passerelle rend vraiment, telles que sa documentation les publie.
const (
	corpsRenouvellement = `{"operation_account_code":"ACC_1AIRTEL","secret":"sk_live_87e1f459","expires_in":3600}`
	corpsPaiement       = `{"status_code":"200","status":"PENDING","reference_id":"PAY23102400001","message":"transaction initiee"}`
	corpsAuthRefusee    = `{"date":"24102023-143000","status_code":3100,"error":"AUTHENTICATION_FAILED","message":"cle refusee","path":"/v2/CODEPAY/rest"}`
	corpsDejaInitiee    = `{"date":"24102023-143000","status_code":5000,"error":"TRANSACTION_REFERENCE_ALREADY_EXISTS","message":"reference deja utilisee","path":"/v2/CODEPAY/rest"}`
)

// demandeType rend un paiement valide, à surcharger au besoin.
func demandeType() pvit.DemandePaiement {
	return pvit.DemandePaiement{
		Reference:    "REF1234567890",
		Montant:      150,
		Operateur:    pvit.OperateurAirtel,
		NumeroClient: "074111111",
	}
}

// contientTout dit si la chaîne porte tous les fragments donnés.
func contientTout(s string, fragments ...string) bool {
	for _, f := range fragments {
		if !strings.Contains(s, f) {
			return false
		}
	}
	return true
}
