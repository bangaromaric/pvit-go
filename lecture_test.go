package pvit

import (
	"encoding/json/jsontext"
	"testing"
	"time"
)

// Le code de retour applicatif s'écrit de quatre façons selon l'endroit de l'API, et
// c'est le discriminant de tout le paquet. Une seule fonction le lit, et ce test est la
// preuve qu'elle les couvre toutes, y compris les formes qu'elle doit refuser.
func TestCodeApplicatifLitLesQuatreEcritures(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom      string
		brut     string
		wantCode int
		wantOK   bool
	}{
		{nom: "chaine de la reponse de transaction", brut: `"200"`, wantCode: 200, wantOK: true},
		{nom: "entier de la reponse de lien", brut: `200`, wantCode: 200, wantOK: true},
		{nom: "entier du corps d erreur", brut: `8000`, wantCode: 8000, wantOK: true},
		{nom: "entier du callback", brut: `201`, wantCode: 201, wantOK: true},
		{nom: "entier ecrit en flottant", brut: `200.0`, wantCode: 200, wantOK: true},
		{nom: "espaces autour", brut: `  "403"  `, wantCode: 403, wantOK: true},
		{nom: "flottant non entier refuse", brut: `200.5`, wantOK: false},
		{nom: "texte refuse", brut: `"abc"`, wantOK: false},
		{nom: "nul refuse", brut: `null`, wantOK: false},
		{nom: "objet refuse", brut: `{}`, wantOK: false},
		{nom: "tableau refuse", brut: `[]`, wantOK: false},
		{nom: "absent refuse", brut: ``, wantOK: false},
		{nom: "chaine vide refusee", brut: `""`, wantOK: false},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()
			got, ok := codeApplicatif(jsontext.Value(c.brut))
			if ok != c.wantOK {
				t.Errorf("codeApplicatif(%s) ok = %v, want %v", c.brut, ok, c.wantOK)
			}
			if ok && got != c.wantCode {
				t.Errorf("codeApplicatif(%s) = %d, want %d", c.brut, got, c.wantCode)
			}
		})
	}
}

func TestNombreTolereLeTypeEtRenonceProprement(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom     string
		brut    string
		wantVal float64
		wantOK  bool
	}{
		{nom: "entier nu", brut: `150`, wantVal: 150, wantOK: true},
		{nom: "flottant nu", brut: `3.5`, wantVal: 3.5, wantOK: true},
		{nom: "entier entre guillemets", brut: `"150"`, wantVal: 150, wantOK: true},
		{nom: "flottant entre guillemets", brut: `"3.5"`, wantVal: 3.5, wantOK: true},
		{nom: "negatif", brut: `-1`, wantVal: -1, wantOK: true},
		{nom: "exponentielle", brut: `1.5e2`, wantVal: 150, wantOK: true},
		{nom: "nul", brut: `null`, wantOK: false},
		{nom: "objet", brut: `{"a":1}`, wantOK: false},
		{nom: "texte", brut: `"deux cents"`, wantOK: false},
		{nom: "vide", brut: ``, wantOK: false},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()
			got, ok := nombre(jsontext.Value(c.brut))
			if ok != c.wantOK {
				t.Errorf("nombre(%s) ok = %v, want %v", c.brut, ok, c.wantOK)
			}
			if ok && got != c.wantVal {
				t.Errorf("nombre(%s) = %v, want %v", c.brut, got, c.wantVal)
			}
		})
	}
}

func TestTexteLitUneChaineCommeUnNombre(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom    string
		brut   string
		want   string
		wantOK bool
	}{
		{nom: "chaine", brut: `"REF123"`, want: "REF123", wantOK: true},
		{nom: "chaine avec echappement", brut: `"a\"b"`, want: `a"b`, wantOK: true},
		{nom: "nombre rendu en texte", brut: `9168`, want: "9168", wantOK: true},
		{nom: "chaine vide refusee", brut: `""`, want: "", wantOK: true},
		{nom: "nul refuse", brut: `null`, wantOK: false},
		{nom: "objet refuse", brut: `{"a":1}`, wantOK: false},
		{nom: "tableau refuse", brut: `[1]`, wantOK: false},
		{nom: "absent refuse", brut: ``, wantOK: false},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()
			got, ok := texte(jsontext.Value(c.brut))
			if ok != c.wantOK {
				t.Errorf("texte(%s) ok = %v, want %v", c.brut, ok, c.wantOK)
			}
			if ok && got != c.want {
				t.Errorf("texte(%s) = %q, want %q", c.brut, got, c.want)
			}
		})
	}
}

// Une date illisible ne doit pas faire échouer la lecture du code d'erreur, qui est la
// seule information dont l'appelant a besoin pour décider.
func TestLaDateDePVitEstLueOuAbandonneeSansBruit(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom    string
		brut   string
		want   time.Time
		wantOK bool
	}{
		{
			nom:    "forme publiee",
			brut:   "24102023-143000",
			want:   time.Date(2023, time.October, 24, 14, 30, 0, 0, time.UTC),
			wantOK: true,
		},
		{nom: "espaces autour", brut: "  24102023-143000  ", want: time.Date(2023, time.October, 24, 14, 30, 0, 0, time.UTC), wantOK: true},
		{nom: "forme iso refusee", brut: "2023-10-24T14:30:00Z", wantOK: false},
		{nom: "texte libre refuse", brut: "n importe quoi", wantOK: false},
		{nom: "vide refuse", brut: "", wantOK: false},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()
			got, ok := datePVit(c.brut)
			if ok != c.wantOK {
				t.Errorf("datePVit(%q) ok = %v, want %v", c.brut, ok, c.wantOK)
			}
			if ok && !got.Equal(c.want) {
				t.Errorf("datePVit(%q) = %v, want %v", c.brut, got, c.want)
			}
		})
	}
}
