package gabon_test

import (
	"strings"
	"testing"

	pvit "github.com/bangaromaric/pvit-go"
	"github.com/bangaromaric/pvit-go/gabon"
)

// Un numéro arrive sous quatre écritures au moins, selon qu'il vient d'une saisie, d'un
// export ou d'un formulaire web. La normalisation est donc la moitié du travail, et
// l'autre moitié est de ne JAMAIS deviner : un AIRTEL_MONEY supposé sur un numéro Moov
// rend le code 3017 au comptoir, devant un client qui attend.
func TestOperateurDuNumeroNormaliseEtNeDevineJamais(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom       string
		msisdn    string
		want      pvit.Operateur
		wantConnu bool
	}{
		{nom: "national avec zero d acheminement", msisdn: "074111111", want: pvit.OperateurAirtel, wantConnu: true},
		{nom: "international avec plus et zero", msisdn: "+241074111111", want: pvit.OperateurAirtel, wantConnu: true},
		{nom: "international sans zero", msisdn: "24174111111", want: pvit.OperateurAirtel, wantConnu: true},
		{nom: "international espace par deux", msisdn: "241 74 11 11 11", want: pvit.OperateurAirtel, wantConnu: true},
		{nom: "significatif nu", msisdn: "74111111", want: pvit.OperateurAirtel, wantConnu: true},
		{nom: "avec tirets et points", msisdn: "+241-07.41-11.111", want: pvit.OperateurAirtel, wantConnu: true},

		{nom: "76 confirme par kyc", msisdn: "076289878", want: pvit.OperateurAirtel, wantConnu: true},
		{nom: "77 confirme par kyc", msisdn: "+24177289878", want: pvit.OperateurAirtel, wantConnu: true},
		{nom: "62 confirme par kyc", msisdn: "062123456", want: pvit.OperateurMoov, wantConnu: true},
		{nom: "66 confirme par kyc", msisdn: "066123456", want: pvit.OperateurMoov, wantConnu: true},

		{nom: "prefixe absent de la table verifiee", msisdn: "065123456", wantConnu: false},
		{nom: "autre prefixe absent de la table verifiee", msisdn: "+24111289878", wantConnu: false},

		{nom: "trop court", msisdn: "7411111", wantConnu: false},
		{nom: "trop long", msisdn: "741111111", wantConnu: false},
		{nom: "vide", msisdn: "", wantConnu: false},
		{nom: "sans chiffre", msisdn: "pas un numero", wantConnu: false},
		{nom: "indicatif d un autre pays", msisdn: "+22507411111", wantConnu: false},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()
			got, connu := gabon.OperateurDuNumero(c.msisdn)
			if connu != c.wantConnu {
				t.Errorf("OperateurDuNumero(%q) connu = %v, want %v", c.msisdn, connu, c.wantConnu)
			}
			if connu && got != c.want {
				t.Errorf("OperateurDuNumero(%q) = %q, want %q", c.msisdn, got, c.want)
			}
			if !connu && got != "" {
				t.Errorf("OperateurDuNumero(%q) = %q sur un prefixe inconnu, want la chaine vide", c.msisdn, got)
			}
		})
	}
}

// La table livrée ne porte que du vérifié, donc elle est incomplète par construction : le
// plan de numérotation gabonais va plus loin que cinq préfixes. Ce test montre comment
// l'appelant la complète le jour où il a relevé un préfixe de plus, et que l'ajout cohabite
// avec les entrées livrées.
func TestUneTableCompleteeParLAppelantEstPriseEnCompte(t *testing.T) {
	t.Parallel()

	table := gabon.TableConnue()
	table["65"] = pvit.OperateurMoov
	table["631"] = pvit.OperateurMoov

	cas := []struct {
		nom       string
		msisdn    string
		want      pvit.Operateur
		wantConnu bool
	}{
		{nom: "les entrees verifiees restent", msisdn: "074111111", want: pvit.OperateurAirtel, wantConnu: true},
		{nom: "une autre entree verifiee reste", msisdn: "066123456", want: pvit.OperateurMoov, wantConnu: true},
		{nom: "un prefixe de deux chiffres ajoute", msisdn: "065123456", want: pvit.OperateurMoov, wantConnu: true},
		{nom: "un prefixe de trois chiffres ajoute", msisdn: "063123456", want: pvit.OperateurMoov, wantConnu: true},
		{nom: "le voisin du prefixe de trois reste inconnu", msisdn: "063223456", wantConnu: false},
		{nom: "un prefixe toujours absent", msisdn: "011123456", wantConnu: false},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()
			got, connu := table.Operateur(c.msisdn)
			if connu != c.wantConnu {
				t.Errorf("Table.Operateur(%q) connu = %v, want %v", c.msisdn, connu, c.wantConnu)
			}
			if connu && got != c.want {
				t.Errorf("Table.Operateur(%q) = %q, want %q", c.msisdn, got, c.want)
			}
		})
	}
}

// Le préfixe le plus précis gagne : une table peut mêler deux et trois chiffres, et
// l'entrée de trois doit l'emporter sur celle de deux.
func TestLePrefixeLePlusPrecisGagne(t *testing.T) {
	t.Parallel()

	table := gabon.Table{
		"74":  pvit.OperateurAirtel,
		"741": pvit.OperateurMoov,
	}

	if got, _ := table.Operateur("074111111"); got != pvit.OperateurMoov {
		t.Errorf("Table.Operateur(074111111) = %q, want %q (l entree de trois chiffres)", got, pvit.OperateurMoov)
	}
	if got, _ := table.Operateur("074211111"); got != pvit.OperateurAirtel {
		t.Errorf("Table.Operateur(074211111) = %q, want %q (l entree de deux chiffres)", got, pvit.OperateurAirtel)
	}
}

// TableConnue rend une COPIE : sans cela, deux projets du même binaire se marcheraient
// dessus, et le premier qui complète la table changerait le comportement du second.
func TestTableConnueRendUneCopie(t *testing.T) {
	t.Parallel()

	premiere := gabon.TableConnue()
	premiere["65"] = pvit.OperateurMoov

	seconde := gabon.TableConnue()
	if _, present := seconde["65"]; present {
		t.Errorf("TableConnue() porte l entree 65 ajoutee a une autre copie, want une copie independante")
	}
	if got, connu := gabon.OperateurDuNumero("065123456"); connu {
		t.Errorf("OperateurDuNumero(065123456) = %q, connu apres ecriture dans une copie, want inconnu", got)
	}
}

// Les cinq entrées livrées sont une AFFIRMATION, et ce test la fige : chacune a été
// confirmée le 22 octobre 2026 par un appel kyc sur un numéro réel du préfixe, qui a rendu
// le titulaire du compte. En ajouter une sans cette preuve fait tomber ce test, et c'est
// exactement ce qu'on veut : un préfixe deviné rend le code 3017 au comptoir.
func TestLesCinqPrefixesVerifiesSontLivres(t *testing.T) {
	t.Parallel()

	want := gabon.Table{
		"74": pvit.OperateurAirtel,
		"76": pvit.OperateurAirtel,
		"77": pvit.OperateurAirtel,
		"62": pvit.OperateurMoov,
		"66": pvit.OperateurMoov,
	}

	got := gabon.TableConnue()
	if len(got) != len(want) {
		t.Errorf("TableConnue() porte %d entrees, want %d: %v", len(got), len(want), got)
	}
	for prefixe, operateur := range want {
		if got[prefixe] != operateur {
			t.Errorf("TableConnue()[%q] = %q, want %q", prefixe, got[prefixe], operateur)
		}
	}
}

// Une clé mal formée ne produit aucune erreur à l'usage, seulement un opérateur jamais
// reconnu, et le symptôme n'apparaît qu'au comptoir. La validation rend la faute bruyante
// au démarrage, et le message dit quelle clé écrire à la place.
func TestNouvelleTableRefuseUneCleMalFormee(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom        string
		prefixes   map[string]pvit.Operateur
		wantRefuse bool
		wantDans   string
	}{
		{
			nom:      "deux et trois chiffres acceptes",
			prefixes: map[string]pvit.Operateur{"74": pvit.OperateurAirtel, "621": pvit.OperateurMoov},
		},
		{
			nom:        "la forme composee est refusee et corrigee",
			prefixes:   map[string]pvit.Operateur{"074": pvit.OperateurAirtel},
			wantRefuse: true,
			wantDans:   `attendu "74"`,
		},
		{
			nom:        "un seul chiffre refuse",
			prefixes:   map[string]pvit.Operateur{"7": pvit.OperateurAirtel},
			wantRefuse: true,
			wantDans:   "deux ou trois chiffres",
		},
		{
			nom:        "quatre chiffres refuses",
			prefixes:   map[string]pvit.Operateur{"7411": pvit.OperateurAirtel},
			wantRefuse: true,
			wantDans:   "deux ou trois chiffres",
		},
		{
			nom:        "une lettre refusee",
			prefixes:   map[string]pvit.Operateur{"7A": pvit.OperateurAirtel},
			wantRefuse: true,
			wantDans:   "des chiffres seulement",
		},
		{
			nom:        "un operateur vide refuse",
			prefixes:   map[string]pvit.Operateur{"74": ""},
			wantRefuse: true,
			wantDans:   "operateur vide",
		},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()
			got, err := gabon.NouvelleTable(c.prefixes)

			if !c.wantRefuse {
				if err != nil {
					t.Errorf("NouvelleTable(%v) err = %v, want nil", c.prefixes, err)
				}
				if len(got) != len(c.prefixes) {
					t.Errorf("NouvelleTable(%v) a rendu %d entrees, want %d", c.prefixes, len(got), len(c.prefixes))
				}
				return
			}
			if err == nil {
				t.Fatalf("NouvelleTable(%v) err = nil, want une erreur", c.prefixes)
			}
			if got != nil {
				t.Errorf("NouvelleTable(%v) a rendu une table malgre l erreur, want nil", c.prefixes)
			}
			if !strings.Contains(err.Error(), c.wantDans) {
				t.Errorf("NouvelleTable(%v) err = %q, want un message contenant %q", c.prefixes, err, c.wantDans)
			}
		})
	}
}

// Les manques sont rassemblés et triés : au démarrage on veut la liste complète de ce qui
// cloche, pas le premier problème suivi de quatre redémarrages.
func TestNouvelleTableRassembleTousLesManques(t *testing.T) {
	t.Parallel()

	_, err := gabon.NouvelleTable(map[string]pvit.Operateur{
		"074":  pvit.OperateurAirtel,
		"7":    pvit.OperateurMoov,
		"7A":   pvit.OperateurMoov,
		"7411": pvit.OperateurAirtel,
	})
	if err == nil {
		t.Fatalf("NouvelleTable(quatre cles fautives) err = nil, want une erreur")
	}
	for _, attendu := range []string{`"074"`, `"7"`, `"7A"`, `"7411"`} {
		if !strings.Contains(err.Error(), attendu) {
			t.Errorf("NouvelleTable(quatre cles fautives) err = %q, want un message citant %s", err, attendu)
		}
	}
}
