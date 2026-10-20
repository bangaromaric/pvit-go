package pvit_test

import (
	"errors"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"testing"
	"time"

	pvit "github.com/bangaromaric/pvit-go"
)

// reglagesAmorceSeule configure un compte avec sa seule clé, sans mot de passe de rotation.
// C'est exactement ce dont dispose un marchand à qui l'espace marchand a montré un code et
// une clé.
func reglagesAmorceSeule() pvit.Reglages {
	r := reglagesComplets()
	r.Comptes = map[pvit.Operateur]pvit.Compte{
		pvit.OperateurAirtel: {CodeOperation: compteAirtel, CleAmorce: cleAmorce},
	}
	return r
}

// Le cas réel du marchand qui détient déjà une clé : elle doit servir telle quelle, sans
// appeler l'API de rotation. Renouveler à l'aveugle gaspillerait un appel, et le code 10008
// sanctionne l'échec d'envoi de clé.
func TestLaCleDAmorceEstUtiliseeSansRenouvellement(t *testing.T) {
	t.Parallel()

	client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule())
	b.programmer("rest", reponse{statut: http.StatusOK, corps: corpsPaiement})

	if _, err := client.Payer(t.Context(), demandeType()); err != nil {
		t.Fatalf("Payer() err = %v, want nil", err)
	}

	if got := b.comptePour("renew-secret"); got != 0 {
		t.Errorf("Payer() a declenche %d renouvellements, want 0", got)
	}
	if got := b.derniere(t, "rest").Secret; got != cleAmorce {
		t.Errorf("Payer() a envoye la cle %q, want %q", got, cleAmorce)
	}
}

// Le mot de passe part dans un CORPS de formulaire, et jamais dans l'URL : un secret en
// paramètre de requête se retrouve dans les journaux d'accès du serveur, dans ceux de
// chaque proxy traversé, et dans l'historique de tout outil de diagnostic.
func TestLeRenouvellementPosteUnFormulaireEtNonUneQuery(t *testing.T) {
	t.Parallel()

	client, b := nouveauBanc(t)
	b.programmer("renew-secret", reponse{statut: http.StatusOK, corps: corpsRenouvellement})
	b.programmer("rest", reponse{statut: http.StatusOK, corps: corpsPaiement})

	if _, err := client.Payer(t.Context(), demandeType()); err != nil {
		t.Fatalf("Payer() err = %v, want nil", err)
	}

	vue := b.derniere(t, "renew-secret")
	if got, want := vue.Methode, http.MethodPost; got != want {
		t.Errorf("renew-secret methode = %q, want %q", got, want)
	}
	if got, want := vue.TypeContenu, "application/x-www-form-urlencoded"; got != want {
		t.Errorf("renew-secret type de contenu = %q, want %q", got, want)
	}
	if vue.Query != "" {
		t.Errorf("renew-secret query = %q, want vide: le mot de passe ne doit pas entrer dans l URL", vue.Query)
	}
	if strings.Contains(vue.Chemin, motDePasse) || strings.Contains(vue.Query, motDePasse) {
		t.Errorf("renew-secret a laisse le mot de passe dans l URL %q?%q", vue.Chemin, vue.Query)
	}

	champs, err := url.ParseQuery(vue.Corps)
	if err != nil {
		t.Fatalf("url.ParseQuery(%q) err = %v, want nil", vue.Corps, err)
	}
	if got, want := champs.Get("operationAccountCode"), compteAirtel; got != want {
		t.Errorf("renew-secret operationAccountCode = %q, want %q", got, want)
	}
	if got, want := champs.Get("password"), motDePasse; got != want {
		t.Errorf("renew-secret password = %q, want %q", got, want)
	}
	if vue.Secret != "" {
		t.Errorf("renew-secret a porte une cle secrete %q, want aucune: c est lui qui l obtient", vue.Secret)
	}
}

// La collection de requêtes publiée met les paramètres dans l'URL, là où le guide
// d'intégration prescrit un corps. Personne n'a tranché, donc l'option existe, et elle doit
// marcher le jour où la réponse arrive.
func TestLOptionDeRenouvellementEnQueryBascule(t *testing.T) {
	t.Parallel()

	client, b := nouveauBanc(t, pvit.AvecRenouvellementEnQuery())
	b.programmer("renew-secret", reponse{statut: http.StatusOK, corps: corpsRenouvellement})
	b.programmer("rest", reponse{statut: http.StatusOK, corps: corpsPaiement})

	if _, err := client.Payer(t.Context(), demandeType()); err != nil {
		t.Fatalf("Payer() err = %v, want nil", err)
	}

	vue := b.derniere(t, "renew-secret")
	if vue.Corps != "" {
		t.Errorf("renew-secret corps = %q, want vide quand la query porte les parametres", vue.Corps)
	}
	champs, err := url.ParseQuery(vue.Query)
	if err != nil {
		t.Fatalf("url.ParseQuery(%q) err = %v, want nil", vue.Query, err)
	}
	if got, want := champs.Get("password"), motDePasse; got != want {
		t.Errorf("renew-secret password en query = %q, want %q", got, want)
	}
}

// Un refus de clé déclenche un renouvellement puis UN SEUL rejeu. Sans cette borne, une clé
// définitivement refusée transformerait une faute de configuration en tempête de requêtes.
func TestUnRefusDeCleDeclencheUnSeulRejeu(t *testing.T) {
	t.Parallel()

	client, b := nouveauBanc(t)
	b.programmer("renew-secret",
		reponse{statut: http.StatusOK, corps: `{"secret":"sk_live_premiere","expires_in":3600}`},
		reponse{statut: http.StatusOK, corps: `{"secret":"sk_live_seconde","expires_in":3600}`},
	)
	b.programmer("rest",
		reponse{statut: http.StatusForbidden, corps: corpsAuthRefusee},
		reponse{statut: http.StatusOK, corps: corpsPaiement},
	)

	if _, err := client.Payer(t.Context(), demandeType()); err != nil {
		t.Fatalf("Payer() err = %v, want nil apres un rejeu", err)
	}

	if got, want := b.comptePour("rest"), 2; got != want {
		t.Errorf("Payer() a tente %d paiements, want %d", got, want)
	}
	if got, want := b.comptePour("renew-secret"), 2; got != want {
		t.Errorf("Payer() a fait %d renouvellements, want %d: un au demarrage, un en reaction", got, want)
	}
	if got, want := b.derniere(t, "rest").Secret, "sk_live_seconde"; got != want {
		t.Errorf("le rejeu a porte la cle %q, want %q: la cle refusee ne doit pas etre reutilisee", got, want)
	}
}

func TestUneCleRefuseeDeuxFoisNEstPlusRejouee(t *testing.T) {
	t.Parallel()

	client, b := nouveauBanc(t)
	b.programmer("renew-secret", reponse{statut: http.StatusOK, corps: corpsRenouvellement})
	b.programmer("rest", reponse{statut: http.StatusForbidden, corps: corpsAuthRefusee})

	_, err := client.Payer(t.Context(), demandeType())
	if !errors.Is(err, pvit.ErrAuthentification) {
		t.Errorf("Payer() err = %v, want ErrAuthentification", err)
	}
	if got, want := b.comptePour("rest"), 2; got != want {
		t.Errorf("Payer() a tente %d paiements, want %d", got, want)
	}
	if got, want := b.comptePour("renew-secret"), 2; got != want {
		t.Errorf("Payer() a fait %d renouvellements, want %d", got, want)
	}

	var refus pvit.Erreur
	if !errors.As(err, &refus) {
		t.Fatalf("Payer() err = %v, want une pvit.Erreur", err)
	}
	if !refus.EstAuthentification() {
		t.Errorf("Erreur.EstAuthentification() = false, want true")
	}
	if refus.EstRejouable() {
		t.Errorf("Erreur.EstRejouable() = true sur un refus de cle, want false")
	}
}

// Dix paiements, un seul renouvellement : c'est la question à laquelle un cache de clé doit
// répondre, et un serveur qui compte ses appels est le seul moyen de la poser.
func TestDixPaiementsNeFontQuUnRenouvellement(t *testing.T) {
	t.Parallel()

	client, b := nouveauBanc(t)
	b.programmer("renew-secret", reponse{statut: http.StatusOK, corps: corpsRenouvellement})
	b.programmer("rest", reponse{statut: http.StatusOK, corps: corpsPaiement})

	for i := range 10 {
		if _, err := client.Payer(t.Context(), demandeType()); err != nil {
			t.Fatalf("Payer() numero %d err = %v, want nil", i+1, err)
		}
	}

	if got, want := b.comptePour("renew-secret"), 1; got != want {
		t.Errorf("dix paiements ont fait %d renouvellements, want %d", got, want)
	}
	if got, want := b.comptePour("rest"), 10; got != want {
		t.Errorf("dix paiements ont fait %d appels, want %d", got, want)
	}
}

// La clé vit 3 600 secondes, et la marge la renouvelle vers la cinquantième minute. Sans
// marge, une clé qui expire pendant le vol d'une requête donnerait un refus à chaque
// paiement.
func TestLaCleEstRenouveleeAvantSonTerme(t *testing.T) {
	t.Parallel()

	client, b := nouveauBanc(t)
	b.programmer("renew-secret", reponse{statut: http.StatusOK, corps: corpsRenouvellement})
	b.programmer("rest", reponse{statut: http.StatusOK, corps: corpsPaiement})

	if _, err := client.Payer(t.Context(), demandeType()); err != nil {
		t.Fatalf("premier Payer() err = %v, want nil", err)
	}

	b.horloge.avancer(45 * time.Minute)
	if _, err := client.Payer(t.Context(), demandeType()); err != nil {
		t.Fatalf("Payer() a 45 minutes err = %v, want nil", err)
	}
	if got, want := b.comptePour("renew-secret"), 1; got != want {
		t.Errorf("a 45 minutes, %d renouvellements, want %d: la marge est de dix minutes", got, want)
	}

	b.horloge.avancer(6 * time.Minute)
	if _, err := client.Payer(t.Context(), demandeType()); err != nil {
		t.Fatalf("Payer() a 51 minutes err = %v, want nil", err)
	}
	if got, want := b.comptePour("renew-secret"), 2; got != want {
		t.Errorf("a 51 minutes, %d renouvellements, want %d", got, want)
	}
}

// Renouveler la clé d'Airtel ne doit RIEN faire à celle de Moov. C'est la différence de
// fond avec un cache de jeton unique, et la raison du verrou par compte.
func TestRenouvelerUnCompteNeTouchePasLAutre(t *testing.T) {
	t.Parallel()

	client, b := nouveauBanc(t)
	b.programmer("renew-secret",
		reponse{statut: http.StatusOK, corps: `{"secret":"sk_live_airtel","expires_in":3600}`},
		reponse{statut: http.StatusOK, corps: `{"secret":"sk_live_moov","expires_in":3600}`},
	)
	b.programmer("rest", reponse{statut: http.StatusOK, corps: corpsPaiement})

	versAirtel := demandeType()
	if _, err := client.Payer(t.Context(), versAirtel); err != nil {
		t.Fatalf("Payer(airtel) err = %v, want nil", err)
	}
	if got, want := b.derniere(t, "rest").Secret, "sk_live_airtel"; got != want {
		t.Errorf("le paiement airtel a porte %q, want %q", got, want)
	}

	versMoov := demandeType()
	versMoov.Operateur = pvit.OperateurMoov
	if _, err := client.Payer(t.Context(), versMoov); err != nil {
		t.Fatalf("Payer(moov) err = %v, want nil", err)
	}
	if got, want := b.derniere(t, "rest").Secret, "sk_live_moov"; got != want {
		t.Errorf("le paiement moov a porte %q, want %q", got, want)
	}

	// Et le second paiement Airtel reprend SA clé, sans renouveler.
	if _, err := client.Payer(t.Context(), versAirtel); err != nil {
		t.Fatalf("second Payer(airtel) err = %v, want nil", err)
	}
	if got, want := b.derniere(t, "rest").Secret, "sk_live_airtel"; got != want {
		t.Errorf("le second paiement airtel a porte %q, want %q", got, want)
	}
	if got, want := b.comptePour("renew-secret"), 2; got != want {
		t.Errorf("%d renouvellements pour deux comptes, want %d", got, want)
	}
}

// LE CAS DE L'UTILISATEUR : un compte configuré avec sa seule clé, qui finit par expirer.
// La panne doit être bruyante et nommer le geste correctif, pas se traduire par un refus
// inexplicable à chaque appel.
func TestUneAmorceExpireeSansMotDePasseLeDitEtNommeLaVariable(t *testing.T) {
	t.Parallel()

	r := reglagesAmorceSeule()
	compte := r.Comptes[pvit.OperateurAirtel]
	compte.ExpirationAmorce = time.Date(2026, time.October, 20, 7, 0, 0, 0, time.UTC) // une heure avant l horloge
	r.Comptes[pvit.OperateurAirtel] = compte

	client, b := nouveauBancAvecReglages(t, r)

	_, err := client.Payer(t.Context(), demandeType())

	if !errors.Is(err, pvit.ErrRotationImpossible) {
		t.Errorf("Payer() err = %v, want ErrRotationImpossible", err)
	}
	if !errors.Is(err, pvit.ErrConfiguration) {
		t.Errorf("Payer() err = %v, want aussi ErrConfiguration: le geste correctif est un reglage", err)
	}
	if errors.Is(err, pvit.ErrAuthentification) {
		t.Errorf("Payer() err = %v, want PAS ErrAuthentification: reessayer ne repare rien", err)
	}

	var refus pvit.Erreur
	if errors.As(err, &refus) && refus.EstRejouable() {
		t.Errorf("Erreur.EstRejouable() = true, want false: une boucle ne doit pas s acharner")
	}

	nom := pvit.NomVariableMotDePasse("", pvit.OperateurAirtel)
	if got, want := nom, "PVIT_ACCOUNT_AIRTEL_MONEY_PASSWORD"; got != want {
		t.Errorf("NomVariableMotDePasse() = %q, want %q", got, want)
	}
	if !contientTout(err.Error(), nom, "Compte.MotDePasse") {
		t.Errorf("Payer() err = %q, want un message citant %q et Compte.MotDePasse", err, nom)
	}

	if got := len(b.vues()); got != 0 {
		t.Errorf("Payer() a emis %d requetes sur une cle inutilisable, want 0", got)
	}
}

// La rotation alimente l'anneau, qui est la réponse à une incohérence de la documentation
// de la passerelle : elle impose une rotation horaire, reconnaît qu'elle invalide les
// notifications en vol, et publie des exemples qui lisent une clé statique.
func TestLaRotationAlimenteLAnneauDeCles(t *testing.T) {
	t.Parallel()

	client, b := nouveauBanc(t)
	b.programmer("renew-secret",
		reponse{statut: http.StatusOK, corps: `{"secret":"sk_live_un","expires_in":3600}`},
		reponse{statut: http.StatusOK, corps: `{"secret":"sk_live_deux","expires_in":3600}`},
		reponse{statut: http.StatusOK, corps: `{"secret":"sk_live_trois","expires_in":3600}`},
	)

	for i, want := range []string{"sk_live_un", "sk_live_deux", "sk_live_trois"} {
		if err := client.Rafraichir(t.Context(), pvit.OperateurAirtel); err != nil {
			t.Fatalf("Rafraichir() numero %d err = %v, want nil", i+1, err)
		}
		if got := client.Anneau(pvit.OperateurAirtel).Cles()[0]; got != want {
			t.Errorf("apres le renouvellement %d, la cle courante est %q, want %q", i+1, got, want)
		}
	}

	got := client.Anneau(pvit.OperateurAirtel).Cles()
	want := []string{"sk_live_trois", "sk_live_deux", "sk_live_un"}
	if len(got) != len(want) {
		t.Fatalf("Anneau().Cles() a rendu %d cles, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Anneau().Cles()[%d] = %q, want %q (la plus recente d abord)", i, got[i], want[i])
		}
	}
}

// Rafraichir sur un compte sans mot de passe doit dire la même chose que le chemin
// paresseux, sans quoi le diagnostic dépendrait du moment où la panne est découverte.
func TestRafraichirSansMotDePasseRendLaMemeErreur(t *testing.T) {
	t.Parallel()

	client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule())

	err := client.Rafraichir(t.Context(), pvit.OperateurAirtel)
	if !errors.Is(err, pvit.ErrRotationImpossible) {
		t.Errorf("Rafraichir() err = %v, want ErrRotationImpossible", err)
	}
	if got := len(b.vues()); got != 0 {
		t.Errorf("Rafraichir() a emis %d requetes, want 0", got)
	}
}

func TestUnOperateurSansCompteNEmetAucuneRequete(t *testing.T) {
	t.Parallel()

	client, b := nouveauBancAvecReglages(t, reglagesAmorceSeule())

	demande := demandeType()
	demande.Operateur = pvit.OperateurMoov

	_, err := client.Payer(t.Context(), demande)
	if !errors.Is(err, pvit.ErrConfiguration) {
		t.Errorf("Payer(moov sans compte) err = %v, want ErrConfiguration", err)
	}
	if !strings.Contains(err.Error(), string(pvit.OperateurMoov)) {
		t.Errorf("Payer(moov sans compte) err = %q, want un message nommant l operateur", err)
	}
	if got := len(b.vues()); got != 0 {
		t.Errorf("Payer(moov sans compte) a emis %d requetes, want 0", got)
	}
	if err := client.Rafraichir(t.Context(), pvit.OperateurMoov); !errors.Is(err, pvit.ErrConfiguration) {
		t.Errorf("Rafraichir(moov sans compte) err = %v, want ErrConfiguration", err)
	}
}

// Ce paquet ne lance AUCUNE goroutine, et ne doit jamais en lancer : une bibliothèque dont
// l'appelant ne peut pas arrêter les goroutines lui impose une durée de vie qu'il n'a pas
// choisie. La rotation est donc paresseuse, sous verrou, et jamais en arrière-plan.
func TestLeClientNeLanceAucuneGoroutine(t *testing.T) {
	client, b := nouveauBanc(t)
	b.programmer("renew-secret", reponse{statut: http.StatusOK, corps: corpsRenouvellement})
	b.programmer("rest", reponse{statut: http.StatusOK, corps: corpsPaiement})

	if _, err := client.Payer(t.Context(), demandeType()); err != nil {
		t.Fatalf("Payer() de chauffe err = %v, want nil", err)
	}
	runtime.GC()
	avant := runtime.NumGoroutine()

	for i := range 20 {
		if _, err := client.Payer(t.Context(), demandeType()); err != nil {
			t.Fatalf("Payer() numero %d err = %v, want nil", i+1, err)
		}
	}
	runtime.GC()

	// Une tolérance, parce que le transport HTTP garde ses propres fils de connexion, qui
	// ne sont pas le fait de ce paquet. Ce que le test interdit, c'est une goroutine PAR
	// APPEL, qui ferait croître ce nombre de vingt.
	if got := runtime.NumGoroutine() - avant; got > 5 {
		t.Errorf("vingt paiements ont ajoute %d goroutines, want au plus 5", got)
	}
}
