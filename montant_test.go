package pvit

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"testing"
)

// La documentation de la passerelle prescrit de rejeter tout flottant non entier, puis
// publie elle même des frais de 3,5 francs. Ce test épingle la résolution : sévérité sur
// les sommes transactables, millième accepté sur les sommes calculées. Appliquer la règle
// à la lettre aurait cassé la lecture du calculateur de frais et du callback.
func TestLeMontantRefuseUnFractionnaireQueLesFraisAcceptent(t *testing.T) {
	t.Parallel()

	t.Run("montant", func(t *testing.T) {
		t.Parallel()
		cas := []struct {
			nom     string
			brut    string
			want    Montant
			wantErr error
		}{
			{nom: "entier nu", brut: `150`, want: 150},
			{nom: "entier ecrit en flottant", brut: `150.0`, want: 150},
			{nom: "entier entre guillemets", brut: `"150"`, want: 150},
			{nom: "zero accepte en lecture", brut: `0`, want: 0},
			{nom: "demi franc refuse", brut: `150.5`, wantErr: ErrMontantFractionnaire},
			{nom: "millieme refuse", brut: `150.001`, wantErr: ErrMontantFractionnaire},
			{nom: "quatre decimales illisible", brut: `150.0001`, wantErr: ErrReponseIllisible},
			{nom: "nul illisible", brut: `null`, wantErr: ErrReponseIllisible},
			{nom: "texte illisible", brut: `"beaucoup"`, wantErr: ErrReponseIllisible},
		}
		for _, c := range cas {
			t.Run(c.nom, func(t *testing.T) {
				t.Parallel()
				got, err := lireMontant("amount", jsontext.Value(c.brut))
				if c.wantErr != nil {
					if !errors.Is(err, c.wantErr) {
						t.Errorf("lireMontant(amount, %s) err = %v, want %v", c.brut, err, c.wantErr)
					}
					return
				}
				if err != nil {
					t.Errorf("lireMontant(amount, %s) err = %v, want nil", c.brut, err)
				}
				if got != c.want {
					t.Errorf("lireMontant(amount, %s) = %d, want %d", c.brut, got, c.want)
				}
			})
		}
	})

	t.Run("frais", func(t *testing.T) {
		t.Parallel()
		cas := []struct {
			nom     string
			brut    string
			want    Frais
			wantErr error
		}{
			{nom: "les frais pvit publies", brut: `3.5`, want: 3500},
			{nom: "les frais operateur publies", brut: `1.5`, want: 1500},
			{nom: "entier", brut: `5`, want: 5000},
			{nom: "entre guillemets", brut: `"3.5"`, want: 3500},
			{nom: "millieme", brut: `0.001`, want: 1},
			{nom: "zero", brut: `0`, want: 0},
			{nom: "quatre decimales refusees", brut: `3.5001`, wantErr: ErrReponseIllisible},
			{nom: "nul illisible", brut: `null`, wantErr: ErrReponseIllisible},
		}
		for _, c := range cas {
			t.Run(c.nom, func(t *testing.T) {
				t.Parallel()
				got, err := lireFrais("fees", jsontext.Value(c.brut))
				if c.wantErr != nil {
					if !errors.Is(err, c.wantErr) {
						t.Errorf("lireFrais(fees, %s) err = %v, want %v", c.brut, err, c.wantErr)
					}
					return
				}
				if err != nil {
					t.Errorf("lireFrais(fees, %s) err = %v, want nil", c.brut, err)
				}
				if got != c.want {
					t.Errorf("lireFrais(fees, %s) = %d millimes, want %d", c.brut, got, c.want)
				}
			})
		}
	})
}

// Le chemin nominal de millimes ne passe pas par le flottant, et ce test en est la raison
// d'être : 0,1 fois mille rend 100,00000000000001 en binaire.
func TestMillimesEstExactSansPasserParLeFlottant(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom    string
		brut   string
		want   int64
		wantOK bool
	}{
		{nom: "un dixieme", brut: "0.1", want: 100, wantOK: true},
		{nom: "deux dixiemes", brut: "0.2", want: 200, wantOK: true},
		{nom: "sept centiemes", brut: "0.07", want: 70, wantOK: true},
		{nom: "trois decimales", brut: "1.234", want: 1234, wantOK: true},
		{nom: "sans partie entiere", brut: ".5", want: 500, wantOK: true},
		{nom: "signe plus", brut: "+2.5", want: 2500, wantOK: true},
		{nom: "negatif", brut: "-2.5", want: -2500, wantOK: true},
		{nom: "exponentielle entiere", brut: "1.5e2", want: 150000, wantOK: true},
		{nom: "quatre decimales refusees", brut: "1.2345", wantOK: false},
		{nom: "deux points refuses", brut: "1.2.3", wantOK: false},
		{nom: "lettre refusee", brut: "12a", wantOK: false},
		{nom: "vide refuse", brut: "", wantOK: false},
		{nom: "signe seul refuse", brut: "-", wantOK: false},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()
			got, ok := millimes(c.brut)
			if ok != c.wantOK {
				t.Errorf("millimes(%q) ok = %v, want %v", c.brut, ok, c.wantOK)
			}
			if ok && got != c.want {
				t.Errorf("millimes(%q) = %d, want %d", c.brut, got, c.want)
			}
		})
	}
}

func TestNouveauMontantRefuseCeQuiNeCirculePas(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom     string
		xaf     int64
		wantErr error
	}{
		{nom: "abonnement mensuel", xaf: 4500},
		{nom: "encaissement quotidien", xaf: 150},
		{nom: "un franc", xaf: 1},
		{nom: "zero refuse", xaf: 0, wantErr: ErrMontantInvalide},
		{nom: "negatif refuse", xaf: -150, wantErr: ErrMontantInvalide},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()
			got, err := NouveauMontant(c.xaf)
			if c.wantErr != nil {
				if !errors.Is(err, c.wantErr) {
					t.Errorf("NouveauMontant(%d) err = %v, want %v", c.xaf, err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Errorf("NouveauMontant(%d) err = %v, want nil", c.xaf, err)
			}
			if int64(got) != c.xaf {
				t.Errorf("NouveauMontant(%d) = %d, want %d", c.xaf, got, c.xaf)
			}
		})
	}
}

// Un montant est sérialisé en entier nu, ce qu'un schema « number » accepte : la
// conversion annoncée par la documentation est donc gratuite, et on n'écrit jamais 150.0.
func TestUnMontantSeSerialiseEnEntierNu(t *testing.T) {
	t.Parallel()

	corps, err := json.Marshal(struct {
		Montant Montant `json:"amount"`
	}{Montant: 4500})
	if err != nil {
		t.Fatalf("json.Marshal err = %v, want nil", err)
	}
	if got, want := string(corps), `{"amount":4500}`; got != want {
		t.Errorf("json.Marshal(Montant(4500)) = %s, want %s", got, want)
	}
}

func TestLesSommesSAffichentPourUneMachineEtPourUnePersonne(t *testing.T) {
	t.Parallel()

	t.Run("montant", func(t *testing.T) {
		t.Parallel()
		cas := []struct {
			montant   Montant
			wantISO   string
			wantUsage string
		}{
			{montant: 150, wantISO: "150 XAF", wantUsage: "150 FCFA"},
			{montant: 4500, wantISO: "4 500 XAF", wantUsage: "4 500 FCFA"},
			{montant: 1234567, wantISO: "1 234 567 XAF", wantUsage: "1 234 567 FCFA"},
			{montant: 0, wantISO: "0 XAF", wantUsage: "0 FCFA"},
			{montant: -150, wantISO: "-150 XAF", wantUsage: "-150 FCFA"},
		}
		for _, c := range cas {
			t.Run(c.wantISO, func(t *testing.T) {
				t.Parallel()
				if got := c.montant.String(); got != c.wantISO {
					t.Errorf("Montant(%d).String() = %q, want %q", int64(c.montant), got, c.wantISO)
				}
				if got := c.montant.EnFCFA(); got != c.wantUsage {
					t.Errorf("Montant(%d).EnFCFA() = %q, want %q", int64(c.montant), got, c.wantUsage)
				}
			})
		}
	})

	t.Run("frais", func(t *testing.T) {
		t.Parallel()
		cas := []struct {
			frais Frais
			want  string
		}{
			{frais: 3500, want: "3,5 XAF"},
			{frais: 1500, want: "1,5 XAF"},
			{frais: 5000, want: "5 XAF"},
			{frais: 1, want: "0,001 XAF"},
			{frais: 1234567, want: "1 234,567 XAF"},
			{frais: 0, want: "0 XAF"},
			{frais: -1500, want: "-1,5 XAF"},
		}
		for _, c := range cas {
			t.Run(c.want, func(t *testing.T) {
				t.Parallel()
				if got := c.frais.String(); got != c.want {
					t.Errorf("Frais(%d).String() = %q, want %q", int64(c.frais), got, c.want)
				}
			})
		}
	})
}

func TestLesFraisSArrondissentVersLeFrancQuiLesCouvre(t *testing.T) {
	t.Parallel()

	cas := []struct {
		frais Frais
		want  Montant
	}{
		{frais: 3500, want: 4},
		{frais: 1500, want: 2},
		{frais: 5000, want: 5},
		{frais: 1, want: 1},
		{frais: 0, want: 0},
		{frais: -1500, want: -1},
	}

	for _, c := range cas {
		t.Run(c.frais.String(), func(t *testing.T) {
			t.Parallel()
			if got := c.frais.ArrondiSup(); got != c.want {
				t.Errorf("Frais(%d).ArrondiSup() = %d, want %d", int64(c.frais), got, c.want)
			}
		})
	}
}

// Sans codec dédié, un appelant qui re-sérialise un Callback vers sa propre API publierait
// 3500 là où la passerelle a dit 3,5 : l'unité de stockage interne ne doit pas fuir dans un
// contrat de sortie.
func TestLesFraisSeSerialisentEnDecimalEtSeRelisent(t *testing.T) {
	t.Parallel()

	cas := []struct {
		frais Frais
		want  string
	}{
		{frais: 3500, want: "3.5"},
		{frais: 1500, want: "1.5"},
		{frais: 5000, want: "5"},
		{frais: 1, want: "0.001"},
		{frais: 1234567, want: "1234.567"},
		{frais: 0, want: "0"},
		{frais: -1500, want: "-1.5"},
	}

	for _, c := range cas {
		t.Run(c.want, func(t *testing.T) {
			t.Parallel()

			if got := c.frais.Decimal(); got != c.want {
				t.Errorf("Frais(%d).Decimal() = %q, want %q", int64(c.frais), got, c.want)
			}

			corps, err := json.Marshal(struct {
				Frais Frais `json:"fees"`
			}{Frais: c.frais})
			if err != nil {
				t.Fatalf("json.Marshal err = %v, want nil", err)
			}
			if gotCorps, wantCorps := string(corps), `{"fees":`+c.want+`}`; gotCorps != wantCorps {
				t.Errorf("json.Marshal(Frais(%d)) = %s, want %s", int64(c.frais), gotCorps, wantCorps)
			}

			// Et le type doit se relire apres s'etre ecrit : une serialisation asymetrique
			// finirait par mordre quelqu un.
			var relu struct {
				Frais Frais `json:"fees"`
			}
			if err := json.Unmarshal(corps, &relu); err != nil {
				t.Fatalf("json.Unmarshal(%s) err = %v, want nil", corps, err)
			}
			if relu.Frais != c.frais {
				t.Errorf("aller-retour de Frais(%d) = %d, want %d", int64(c.frais), int64(relu.Frais), int64(c.frais))
			}
		})
	}
}

// La distinction entre « absent » et « illisible » décide d'une conduite : l'absence laisse
// zéro, l'illisible fait échouer la lecture d'une somme d'argent.
func TestPresenteDistingueLAbsentDeLIllisible(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom  string
		brut string
		want bool
	}{
		{nom: "champ absent", brut: ``, want: false},
		{nom: "valeur nulle", brut: `null`, want: false},
		{nom: "espaces seuls", brut: `   `, want: false},
		{nom: "zero est une valeur", brut: `0`, want: true},
		{nom: "chaine vide est une valeur", brut: `""`, want: true},
		{nom: "nombre", brut: `150`, want: true},
		{nom: "texte illisible", brut: `"beaucoup"`, want: true},
		{nom: "objet", brut: `{}`, want: true},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()
			if got := presente(jsontext.Value(c.brut)); got != c.want {
				t.Errorf("presente(%q) = %v, want %v", c.brut, got, c.want)
			}
		})
	}
}

func TestUneSommeAbsenteLaisseZeroSansErreur(t *testing.T) {
	t.Parallel()

	montant, err := montantSiPresent("amount", jsontext.Value(``))
	if err != nil || montant != 0 {
		t.Errorf("montantSiPresent(absent) = %d, %v, want 0, nil", montant, err)
	}
	frais, err := fraisSiPresent("fees", jsontext.Value(`null`))
	if err != nil || frais != 0 {
		t.Errorf("fraisSiPresent(nul) = %d, %v, want 0, nil", frais, err)
	}
	if _, err := montantSiPresent("amount", jsontext.Value(`150.5`)); !errors.Is(err, ErrMontantFractionnaire) {
		t.Errorf("montantSiPresent(150.5) err = %v, want ErrMontantFractionnaire", err)
	}
}
