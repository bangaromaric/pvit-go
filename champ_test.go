package pvit

import (
	"errors"
	"strings"
	"testing"
)

// La référence porte l'idempotence : la valider avant l'appel évite un aller-retour et un
// corps d'erreur qui ne nomme pas toujours le champ fautif.
func TestNouvelleReferenceTientLesBornesDuServeur(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom        string
		valeur     string
		want       Reference
		wantChamp  string
		wantRefuse bool
	}{
		{nom: "forme publiee par la passerelle", valeur: "REF1234567890", want: "REF1234567890"},
		{nom: "vingt caracteres exactement", valeur: "A2345678901234567890", want: "A2345678901234567890"},
		{nom: "un seul caractere", valeur: "A", want: "A"},
		{nom: "espaces en trop retires", valeur: "  REF123  ", want: "REF123"},
		{nom: "vingt et un refuses", valeur: "A23456789012345678901", wantRefuse: true, wantChamp: "reference"},
		{nom: "tiret refuse", valeur: "REF-123", wantRefuse: true, wantChamp: "reference"},
		{nom: "souligne refuse", valeur: "REF_123", wantRefuse: true, wantChamp: "reference"},
		{nom: "accent refuse", valeur: "RÉF123", wantRefuse: true, wantChamp: "reference"},
		{nom: "vide refuse", valeur: "", wantRefuse: true, wantChamp: "reference"},
		{nom: "espaces seuls refuses", valeur: "   ", wantRefuse: true, wantChamp: "reference"},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()
			got, err := NouvelleReference(c.valeur)

			if !c.wantRefuse {
				if err != nil {
					t.Errorf("NouvelleReference(%q) err = %v, want nil", c.valeur, err)
				}
				if got != c.want {
					t.Errorf("NouvelleReference(%q) = %q, want %q", c.valeur, got, c.want)
				}
				return
			}

			if !errors.Is(err, ErrRequeteInvalide) {
				t.Errorf("NouvelleReference(%q) err = %v, want ErrRequeteInvalide", c.valeur, err)
			}
			var champ ChampInvalideError
			if !errors.As(err, &champ) {
				t.Fatalf("NouvelleReference(%q) err = %v, want une ChampInvalideError", c.valeur, err)
			}
			if champ.Champ != c.wantChamp {
				t.Errorf("NouvelleReference(%q) champ = %q, want %q", c.valeur, champ.Champ, c.wantChamp)
			}
		})
	}
}

// Une référence devinable laisserait un tiers sonder le statut des transactions d'autrui :
// elle est donc tirée de crypto/rand, et jamais de math/rand.
func TestReferenceAleatoireRemplitLesVingtCaracteres(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom        string
		prefixe    string
		wantRefuse bool
	}{
		{nom: "sans prefixe", prefixe: ""},
		{nom: "prefixe court", prefixe: "KAYE"},
		{nom: "prefixe minuscule mis en capitales", prefixe: "kaye"},
		{nom: "prefixe de douze", prefixe: "ABCDEFGHIJKL"},
		{nom: "prefixe de treize ne laisse pas huit caracteres", prefixe: "ABCDEFGHIJKLM", wantRefuse: true},
		{nom: "prefixe avec tiret refuse", prefixe: "KA-YE", wantRefuse: true},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()
			got, err := ReferenceAleatoire(c.prefixe)

			if c.wantRefuse {
				if !errors.Is(err, ErrRequeteInvalide) {
					t.Errorf("ReferenceAleatoire(%q) err = %v, want ErrRequeteInvalide", c.prefixe, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ReferenceAleatoire(%q) err = %v, want nil", c.prefixe, err)
			}
			if _, err := NouvelleReference(string(got)); err != nil {
				t.Errorf("ReferenceAleatoire(%q) = %q, qui ne passe pas NouvelleReference: %v", c.prefixe, got, err)
			}
			if n := len(got); n != maxReference {
				t.Errorf("ReferenceAleatoire(%q) = %q, longueur %d, want %d", c.prefixe, got, n, maxReference)
			}
			if !strings.HasPrefix(string(got), strings.ToUpper(c.prefixe)) {
				t.Errorf("ReferenceAleatoire(%q) = %q, want un prefixe %q", c.prefixe, got, strings.ToUpper(c.prefixe))
			}
		})
	}
}

// Deux tirages de suite ne doivent pas se ressembler : c'est tout ce qu'un test peut dire
// honnêtement d'un générateur, et c'est déjà ce qui attraperait un compteur déguisé en
// aléa.
func TestDeuxReferencesAleatoiresDifferent(t *testing.T) {
	t.Parallel()

	vues := make(map[Reference]bool, 64)
	for range 64 {
		r, err := ReferenceAleatoire("T")
		if err != nil {
			t.Fatalf("ReferenceAleatoire(T) err = %v, want nil", err)
		}
		if vues[r] {
			t.Errorf("ReferenceAleatoire(T) a rendu %q deux fois sur 64 tirages", r)
		}
		vues[r] = true
	}
}

// La passerelle compte des caractères, pas des octets : « Épicerie » pèse neuf octets pour
// huit caractères, et compter en octets refuserait une valeur que le serveur accepte.
func TestUnChampLibreSeCompteEnCaracteresEtNonEnOctets(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom        string
		valeur     string
		max        int
		wantRefuse bool
	}{
		{nom: "quinze caracteres ascii", valeur: "ABCDEFGHIJKLMNO", max: maxInfoLibre},
		{nom: "seize caracteres ascii refuses", valeur: "ABCDEFGHIJKLMNOP", max: maxInfoLibre, wantRefuse: true},
		{nom: "huit caracteres accentues", valeur: "Épicerie", max: maxInfoLibre},
		{nom: "quinze caracteres accentues", valeur: "Épicerieéééééée", max: maxInfoLibre},
		{nom: "seize caracteres accentues refuses", valeur: "Épicerieééééééee", max: maxInfoLibre, wantRefuse: true},
		{nom: "vide accepte", valeur: "", max: maxInfoLibre},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()
			err := texteBorne("free_info", c.valeur, c.max)
			if c.wantRefuse {
				if !errors.Is(err, ErrRequeteInvalide) {
					t.Errorf("texteBorne(free_info, %q, %d) err = %v, want ErrRequeteInvalide", c.valeur, c.max, err)
				}
				return
			}
			if err != nil {
				t.Errorf("texteBorne(free_info, %q, %d) err = %v, want nil", c.valeur, c.max, err)
			}
		})
	}
}

func TestLeMessageDUnChampInvalideNommeLeChamp(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom  string
		err  ChampInvalideError
		want string
	}{
		{
			nom:  "longueur",
			err:  ChampInvalideError{Champ: "reference", Longueur: 24, Max: 20},
			want: "champ reference: 24 caracteres, maximum 20",
		},
		{
			nom:  "raison avec valeur",
			err:  ChampInvalideError{Champ: "reference", Valeur: "REF-1", Raison: "attendu des lettres et des chiffres seulement"},
			want: `champ reference: attendu des lettres et des chiffres seulement: "REF-1"`,
		},
		{
			nom:  "raison seule",
			err:  ChampInvalideError{Champ: "operator_code", Raison: "vide"},
			want: "champ operator_code: vide",
		},
		{
			nom:  "sans detail",
			err:  ChampInvalideError{Champ: "amount"},
			want: "champ amount: invalide",
		},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()
			if got := c.err.Error(); got != c.want {
				t.Errorf("ChampInvalideError.Error() = %q, want %q", got, c.want)
			}
		})
	}
}

// Une valeur fautive entre dans un message d'erreur, donc dans chaque ligne de journal qui
// le reprend : elle est bornée pour que ce ne soit pas une page entière.
func TestUneValeurFautiveEstBorneeDansLeMessage(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("A", 200)
	err := ChampInvalideError{Champ: "free_info", Valeur: borner(long), Raison: "trop long"}
	if got, want := len(err.Error()), 128; got > want {
		t.Errorf("ChampInvalideError.Error() sur 200 caracteres pese %d octets, want au plus %d", got, want)
	}
	if !strings.Contains(err.Error(), "...") {
		t.Errorf("ChampInvalideError.Error() = %q, want une marque de troncature", err.Error())
	}
}
