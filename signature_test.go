package pvit_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	json "encoding/json/v2"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	pvit "github.com/bangaromaric/pvit-go"
)

// signer calcule la signature attendue, de façon explicite.
//
// Le test refait le calcul au lieu de porter un condensat en dur, et c'est volontaire : ce
// qui varie d'une implémentation à l'autre n'est pas la fonction de hachage mais l'ORDRE de
// la concaténation, le séparateur et la casse de l'hexadécimal. Les écrire ici les épingle.
func signer(t *testing.T, cle, horodatage string, corps []byte) string {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(cle))
	mac.Write([]byte(horodatage + "." + string(corps)))
	return "v1=" + hex.EncodeToString(mac.Sum(nil))
}

const (
	corpsRapport = `{"event":"report.ready","report_id":"0f3c9c02","format":"CSV"}`
	cleRapport   = "sk_live_cle_de_rapport"
)

func horodatageDe(instant time.Time) string {
	return strconv.FormatInt(instant.Unix(), 10)
}

func TestLaSignatureDeReferenceEstAcceptee(t *testing.T) {
	t.Parallel()

	maintenant := time.Date(2026, time.October, 20, 8, 0, 0, 0, time.UTC)
	horodatage := horodatageDe(maintenant)
	anneau := pvit.NouvelAnneau(cleRapport)

	signature := signer(t, cleRapport, horodatage, []byte(corpsRapport))
	if err := anneau.Verifier(horodatage, []byte(corpsRapport), signature, maintenant, pvit.FenetreSignature); err != nil {
		t.Errorf("Verifier() err = %v, want nil", err)
	}

	// Le préfixe de version est accepté présent ou absent : un intermédiaire peut l'avoir
	// retiré, et le condensat nu reste vérifiable.
	nu := strings.TrimPrefix(signature, "v1=")
	if err := anneau.Verifier(horodatage, []byte(corpsRapport), nu, maintenant, pvit.FenetreSignature); err != nil {
		t.Errorf("Verifier() sur un condensat sans prefixe err = %v, want nil", err)
	}
}

// L'ORDRE de la concaténation est ce qui se trompe le plus facilement, et une signature
// calculée dans l'autre sens doit être refusée.
func TestLOrdreDeLaConcatenationEstEpingle(t *testing.T) {
	t.Parallel()

	maintenant := time.Date(2026, time.October, 20, 8, 0, 0, 0, time.UTC)
	horodatage := horodatageDe(maintenant)
	anneau := pvit.NouvelAnneau(cleRapport)

	// Le corps avant l'horodatage, c'est à dire l'inverse de la règle.
	mac := hmac.New(sha256.New, []byte(cleRapport))
	mac.Write([]byte(corpsRapport + "." + horodatage))
	inverse := "v1=" + hex.EncodeToString(mac.Sum(nil))

	err := anneau.Verifier(horodatage, []byte(corpsRapport), inverse, maintenant, pvit.FenetreSignature)
	if !errors.Is(err, pvit.ErrSignatureInvalide) {
		t.Errorf("Verifier() sur une concatenation inversee err = %v, want ErrSignatureInvalide", err)
	}
}

// LA RAISON D'ÊTRE DE L'ANNEAU.
//
// La passerelle impose une rotation horaire, rejoue ses notifications jusqu'à six heures, et
// publie pourtant des exemples qui lisent une clé statique. Une notification signée avant la
// dernière rotation doit rester vérifiable, sans quoi tout rejeu serait refusé.
func TestUneSignatureDUneCleAncienneEstAcceptee(t *testing.T) {
	t.Parallel()

	maintenant := time.Date(2026, time.October, 20, 8, 0, 0, 0, time.UTC)
	horodatage := horodatageDe(maintenant)

	// La plus récente d'abord, comme l'anneau les range.
	anneau := pvit.NouvelAnneau("sk_live_trois", "sk_live_deux", "sk_live_un")

	for _, cle := range []string{"sk_live_trois", "sk_live_deux", "sk_live_un"} {
		t.Run(cle, func(t *testing.T) {
			t.Parallel()
			signature := signer(t, cle, horodatage, []byte(corpsRapport))
			if err := anneau.Verifier(horodatage, []byte(corpsRapport), signature, maintenant, pvit.FenetreSignature); err != nil {
				t.Errorf("Verifier() avec la cle %q err = %v, want nil", cle, err)
			}
		})
	}

	t.Run("une cle hors de l anneau est refusee", func(t *testing.T) {
		t.Parallel()
		signature := signer(t, "sk_live_zero", horodatage, []byte(corpsRapport))
		err := anneau.Verifier(horodatage, []byte(corpsRapport), signature, maintenant, pvit.FenetreSignature)
		if !errors.Is(err, pvit.ErrSignatureInvalide) {
			t.Errorf("Verifier() avec une cle hors anneau err = %v, want ErrSignatureInvalide", err)
		}
	})
}

// La fenêtre s'applique DANS LES DEUX SENS : n'accepter que le passé laisserait passer une
// notification forgée avec une horloge en avance.
func TestLaFenetreSAppliqueDansLesDeuxSens(t *testing.T) {
	t.Parallel()

	maintenant := time.Date(2026, time.October, 20, 8, 0, 0, 0, time.UTC)
	anneau := pvit.NouvelAnneau(cleRapport)

	cas := []struct {
		nom        string
		decalage   time.Duration
		wantRefuse bool
	}{
		{nom: "a l instant", decalage: 0},
		{nom: "299 secondes dans le passe", decalage: -299 * time.Second},
		{nom: "299 secondes dans le futur", decalage: 299 * time.Second},
		{nom: "301 secondes dans le passe", decalage: -301 * time.Second, wantRefuse: true},
		{nom: "301 secondes dans le futur", decalage: 301 * time.Second, wantRefuse: true},
		{nom: "six heures dans le passe", decalage: -6 * time.Hour, wantRefuse: true},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()
			emis := maintenant.Add(c.decalage)
			horodatage := horodatageDe(emis)
			signature := signer(t, cleRapport, horodatage, []byte(corpsRapport))

			err := anneau.Verifier(horodatage, []byte(corpsRapport), signature, maintenant, pvit.FenetreSignature)
			if c.wantRefuse {
				if !errors.Is(err, pvit.ErrHorodatageHorsFenetre) {
					t.Errorf("Verifier() a %v err = %v, want ErrHorodatageHorsFenetre", c.decalage, err)
				}
				return
			}
			if err != nil {
				t.Errorf("Verifier() a %v err = %v, want nil", c.decalage, err)
			}
		})
	}
}

// Le corps BRUT est signé, et non sa re-sérialisation : désérialiser puis re-sérialiser
// changerait l'ordre des clés et l'espacement, et toute signature deviendrait fausse.
func TestLeCorpsBrutEstSigneEtNonSaReserialisation(t *testing.T) {
	t.Parallel()

	maintenant := time.Date(2026, time.October, 20, 8, 0, 0, 0, time.UTC)
	horodatage := horodatageDe(maintenant)
	anneau := pvit.NouvelAnneau(cleRapport)

	brut := []byte(corpsRapport)
	signature := signer(t, cleRapport, horodatage, brut)

	// Le même document, passé par une désérialisation et une re-sérialisation.
	var carte map[string]any
	if err := json.Unmarshal(brut, &carte); err != nil {
		t.Fatalf("json.Unmarshal() err = %v, want nil", err)
	}
	reserialise, err := json.Marshal(carte)
	if err != nil {
		t.Fatalf("json.Marshal() err = %v, want nil", err)
	}

	if err := anneau.Verifier(horodatage, brut, signature, maintenant, pvit.FenetreSignature); err != nil {
		t.Errorf("Verifier() sur le corps brut err = %v, want nil", err)
	}
	if string(reserialise) == string(brut) {
		t.Skip("la re-serialisation a rendu les memes octets, ce test ne prouve rien ici")
	}
	if err := anneau.Verifier(horodatage, reserialise, signature, maintenant, pvit.FenetreSignature); err == nil {
		t.Errorf("Verifier() sur un corps re-serialise err = nil, want une erreur")
	}
}

// Les deux formes d'horodatage sont acceptées, parce que la documentation nomme ce champ
// sans en publier le format : en deviner une seule ferait refuser toutes les notifications.
func TestLesDeuxFormesDHorodatageSontAcceptees(t *testing.T) {
	t.Parallel()

	maintenant := time.Date(2026, time.October, 20, 8, 0, 0, 0, time.UTC)
	anneau := pvit.NouvelAnneau(cleRapport)

	cas := []struct {
		nom        string
		horodatage string
		wantRefuse bool
	}{
		{nom: "secondes depuis l epoque", horodatage: horodatageDe(maintenant)},
		{nom: "forme rfc 3339", horodatage: maintenant.Format(time.RFC3339)},
		{nom: "vide refuse", horodatage: "", wantRefuse: true},
		{nom: "texte libre refuse", horodatage: "hier", wantRefuse: true},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()
			signature := signer(t, cleRapport, c.horodatage, []byte(corpsRapport))
			err := anneau.Verifier(c.horodatage, []byte(corpsRapport), signature, maintenant, pvit.FenetreSignature)
			if c.wantRefuse {
				if !errors.Is(err, pvit.ErrHorodatageHorsFenetre) {
					t.Errorf("Verifier() sur %q err = %v, want ErrHorodatageHorsFenetre", c.horodatage, err)
				}
				return
			}
			if err != nil {
				t.Errorf("Verifier() sur %q err = %v, want nil", c.horodatage, err)
			}
		})
	}
}

// Un anneau vide dit « je ne peux pas vérifier », et non « c'est faux » : confondre les deux
// enverrait chercher un attaquant là où il manque un réglage.
func TestUnAnneauVideNeDitPasQueLaSignatureEstFausse(t *testing.T) {
	t.Parallel()

	maintenant := time.Date(2026, time.October, 20, 8, 0, 0, 0, time.UTC)
	horodatage := horodatageDe(maintenant)
	signature := signer(t, cleRapport, horodatage, []byte(corpsRapport))

	var vide pvit.Anneau
	err := vide.Verifier(horodatage, []byte(corpsRapport), signature, maintenant, pvit.FenetreSignature)

	if !errors.Is(err, pvit.ErrAnneauVide) {
		t.Errorf("Verifier() sur un anneau vide err = %v, want ErrAnneauVide", err)
	}
	if !errors.Is(err, pvit.ErrConfiguration) {
		t.Errorf("Verifier() sur un anneau vide err = %v, want aussi ErrConfiguration", err)
	}
	if errors.Is(err, pvit.ErrSignatureInvalide) {
		t.Errorf("Verifier() sur un anneau vide err = %v, want PAS ErrSignatureInvalide", err)
	}
	if !vide.Vide() {
		t.Errorf("Anneau zero Vide() = false, want true")
	}
}

func TestUnCondensatIllisibleEstRefuse(t *testing.T) {
	t.Parallel()

	maintenant := time.Date(2026, time.October, 20, 8, 0, 0, 0, time.UTC)
	horodatage := horodatageDe(maintenant)
	anneau := pvit.NouvelAnneau(cleRapport)

	cas := []struct {
		nom       string
		signature string
	}{
		{nom: "vide", signature: ""},
		{nom: "pas de l hexadecimal", signature: "v1=pas-un-condensat"},
		{nom: "trop court", signature: "v1=abcd"},
		{nom: "trop long", signature: "v1=" + strings.Repeat("ab", 40)},
		{nom: "prefixe seul", signature: "v1="},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()
			err := anneau.Verifier(horodatage, []byte(corpsRapport), c.signature, maintenant, pvit.FenetreSignature)
			if !errors.Is(err, pvit.ErrSignatureInvalide) {
				t.Errorf("Verifier() sur %q err = %v, want ErrSignatureInvalide", c.signature, err)
			}
		})
	}
}

// L'anneau écarte les clés vides et les doublons : vérifier deux fois contre la même clé ne
// change rien et coûte un calcul de condensat.
func TestLAnneauEcarteLesCleVidesEtLesDoublons(t *testing.T) {
	t.Parallel()

	anneau := pvit.NouvelAnneau("  ", "sk_un", "", "sk_un", "sk_deux", "  sk_deux  ")
	got := anneau.Cles()
	want := []string{"sk_un", "sk_deux"}

	if len(got) != len(want) {
		t.Fatalf("NouvelAnneau().Cles() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Cles()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// Cles rend une copie : une tranche rendue telle quelle laisserait un appelant réécrire les
// clés avec lesquelles les signatures sont vérifiées.
func TestLesClesDeLAnneauNeSontPasModifiablesParLAppelant(t *testing.T) {
	t.Parallel()

	maintenant := time.Date(2026, time.October, 20, 8, 0, 0, 0, time.UTC)
	horodatage := horodatageDe(maintenant)
	anneau := pvit.NouvelAnneau(cleRapport)

	cles := anneau.Cles()
	cles[0] = "sk_live_de_lattaquant"

	signature := signer(t, "sk_live_de_lattaquant", horodatage, []byte(corpsRapport))
	if err := anneau.Verifier(horodatage, []byte(corpsRapport), signature, maintenant, pvit.FenetreSignature); err == nil {
		t.Errorf("Verifier() a accepte une cle ecrite dans la tranche rendue, want un refus")
	}
}

// Le chemin par le client applique l'anneau du compte, son horloge et sa fenêtre : c'est
// celui à préférer, puisqu'il n'y a rien à tenir à jour côté appelant.
func TestLaVerificationParLeClientUtiliseLAnneauDuCompte(t *testing.T) {
	t.Parallel()

	client, b := nouveauBanc(t)
	b.programmer("renew-secret", reponse{statut: http.StatusOK, corps: `{"secret":"sk_live_courante","expires_in":3600}`})

	if err := client.Rafraichir(t.Context(), pvit.OperateurAirtel); err != nil {
		t.Fatalf("Rafraichir() err = %v, want nil", err)
	}

	horodatage := horodatageDe(b.horloge.maintenant())
	signature := signer(t, "sk_live_courante", horodatage, []byte(corpsRapport))

	if err := client.VerifierSignatureRapport(pvit.OperateurAirtel, horodatage, []byte(corpsRapport), signature); err != nil {
		t.Errorf("VerifierSignatureRapport() err = %v, want nil", err)
	}

	// Un compte sans clé obtenue n'a pas d'anneau, et le dit sans crier à la fraude.
	err := client.VerifierSignatureRapport(pvit.OperateurMoov, horodatage, []byte(corpsRapport), signature)
	if !errors.Is(err, pvit.ErrAnneauVide) {
		t.Errorf("VerifierSignatureRapport(moov sans cle) err = %v, want ErrAnneauVide", err)
	}
}
