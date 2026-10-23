package pvit

import (
	"errors"
	"testing"
)

// AMBIGUOUS manque de l'énumération du schéma officiel alors que la prose le liste. Un
// décodeur strict casserait donc sur le cas où il ne faut surtout pas casser, et ce test
// épingle la règle inverse : tout jeton inconnu est incertain, et il est conservé.
func TestUnStatutInconnuEstIncertainEtResteLisible(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom           string
		brut          string
		want          Statut
		wantFinal     bool
		wantIncertain bool
		wantSucces    bool
	}{
		{nom: "en attente", brut: "PENDING", want: StatutEnAttente, wantIncertain: true},
		{nom: "succes", brut: "SUCCESS", want: StatutSucces, wantFinal: true, wantSucces: true},
		{nom: "echec", brut: "FAILED", want: StatutEchec, wantFinal: true},
		{nom: "ambigu absent du schema", brut: "AMBIGUOUS", want: StatutAmbigu, wantIncertain: true},
		{nom: "minuscules normalisees", brut: "success", want: StatutSucces, wantFinal: true, wantSucces: true},
		{nom: "espaces retires", brut: "  PENDING  ", want: StatutEnAttente, wantIncertain: true},
		{nom: "jeton inconnu conserve", brut: "WEIRD", want: Statut("WEIRD"), wantIncertain: true},
		{nom: "vide incertain", brut: "", want: Statut(""), wantIncertain: true},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()
			got := LireStatut(c.brut)
			if got != c.want {
				t.Errorf("LireStatut(%q) = %q, want %q", c.brut, got, c.want)
			}
			if gotFinal := got.EstFinal(); gotFinal != c.wantFinal {
				t.Errorf("LireStatut(%q).EstFinal() = %v, want %v", c.brut, gotFinal, c.wantFinal)
			}
			if gotIncertain := got.EstIncertain(); gotIncertain != c.wantIncertain {
				t.Errorf("LireStatut(%q).EstIncertain() = %v, want %v", c.brut, gotIncertain, c.wantIncertain)
			}
			if gotSucces := got.EstSucces(); gotSucces != c.wantSucces {
				t.Errorf("LireStatut(%q).EstSucces() = %v, want %v", c.brut, gotSucces, c.wantSucces)
			}
		})
	}
}

// La valeur zéro d'un Statut doit être exploitable, et du bon côté : un statut dont on ne
// sait rien se redemande, il ne se conclut pas.
func TestLaValeurZeroDUnStatutSeRedemande(t *testing.T) {
	t.Parallel()

	var s Statut
	if s.EstFinal() {
		t.Errorf("Statut zero EstFinal() = true, want false")
	}
	if !s.EstIncertain() {
		t.Errorf("Statut zero EstIncertain() = false, want true")
	}
	if s.EstSucces() {
		t.Errorf("Statut zero EstSucces() = true, want false")
	}
}

// Le type reste ouvert : refuser un opérateur que la passerelle vient d'ajouter coûterait
// une livraison pour rien.
func TestNouvelOperateurAccepteToutCodeDeLaPasserelle(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom        string
		code       string
		want       Operateur
		wantRefuse bool
	}{
		{nom: "airtel", code: "AIRTEL_MONEY", want: OperateurAirtel},
		{nom: "moov", code: "MOOV_MONEY", want: OperateurMoov},
		{nom: "une famille non nommee ici", code: "GIMAC", want: Operateur("GIMAC")},
		{nom: "minuscules mises en capitales", code: "airtel_money", want: OperateurAirtel},
		{nom: "espaces retires", code: "  MOOV_MONEY  ", want: OperateurMoov},
		{nom: "vide refuse", code: "", wantRefuse: true},
		{nom: "tiret refuse", code: "AIRTEL-MONEY", wantRefuse: true},
		{nom: "commence par un chiffre refuse", code: "1AIRTEL", wantRefuse: true},
		{nom: "un seul caractere refuse", code: "A", wantRefuse: true},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()
			got, err := NouvelOperateur(c.code)
			if c.wantRefuse {
				if !errors.Is(err, ErrRequeteInvalide) {
					t.Errorf("NouvelOperateur(%q) err = %v, want ErrRequeteInvalide", c.code, err)
				}
				return
			}
			if err != nil {
				t.Errorf("NouvelOperateur(%q) err = %v, want nil", c.code, err)
			}
			if got != c.want {
				t.Errorf("NouvelOperateur(%q) = %q, want %q", c.code, got, c.want)
			}
		})
	}
}

// La valeur zéro d'un Proprietaire vaut « prendre le défaut », et ce défaut n'est pas le
// même pour un paiement et pour un remboursement : on ne fait pas payer au client les
// frais d'un remboursement.
func TestLaValeurZeroDUnProprietairePrendLeDefautDeLOperation(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom    string
		porte  Proprietaire
		defaut Proprietaire
		want   Proprietaire
	}{
		{nom: "zero prend le client sur un paiement", porte: "", defaut: ProprietaireClient, want: ProprietaireClient},
		{nom: "zero prend le marchand sur un remboursement", porte: "", defaut: ProprietaireMarchand, want: ProprietaireMarchand},
		{nom: "une valeur posee gagne sur le defaut", porte: ProprietaireMarchand, defaut: ProprietaireClient, want: ProprietaireMarchand},
		{nom: "l autre sens aussi", porte: ProprietaireClient, defaut: ProprietaireMarchand, want: ProprietaireClient},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()
			if got := c.porte.ouDefaut(c.defaut); got != c.want {
				t.Errorf("Proprietaire(%q).ouDefaut(%q) = %q, want %q", c.porte, c.defaut, got, c.want)
			}
		})
	}
}

func TestSeulesLesDeuxValeursDeLaPasserelleSontValides(t *testing.T) {
	t.Parallel()

	cas := []struct {
		porte Proprietaire
		want  bool
	}{
		{porte: ProprietaireClient, want: true},
		{porte: ProprietaireMarchand, want: true},
		{porte: "", want: false},
		{porte: "BOTH", want: false},
		{porte: "customer", want: false},
	}

	for _, c := range cas {
		t.Run(string(c.porte)+"/"+map[bool]string{true: "valide", false: "refuse"}[c.want], func(t *testing.T) {
			t.Parallel()
			if got := c.porte.valide(); got != c.want {
				t.Errorf("Proprietaire(%q).valide() = %v, want %v", c.porte, got, c.want)
			}
		})
	}
}

func TestSeulsLesDeuxSensDeMouvementSontValides(t *testing.T) {
	t.Parallel()

	cas := []struct {
		operation TypeOperation
		want      bool
	}{
		{operation: OperationPaiement, want: true},
		{operation: OperationRemboursement, want: true},
		{operation: "", want: false},
		{operation: "REFUND", want: false},
	}

	for _, c := range cas {
		t.Run(string(c.operation)+"/"+map[bool]string{true: "valide", false: "refuse"}[c.want], func(t *testing.T) {
			t.Parallel()
			if got := c.operation.valide(); got != c.want {
				t.Errorf("TypeOperation(%q).valide() = %v, want %v", c.operation, got, c.want)
			}
		})
	}
}

// Le piège du lien de paiement tient en une ligne : [ServiceRestful] est le DÉFAUT du
// client, et c'est le seul des quatre services que l'API de lien n'accepte pas.
//
// L'envoyer au chemin link se ferait refuser par un code que rien n'explique, alors que la
// faute est locale et vérifiable sans réseau. Ce test est la garde de cette exclusion, et
// il couvre les quatre services pour qu'un cinquième, ajouté un jour, ne tombe pas dans le
// lien par inadvertance.
func TestSeulsTroisServicesSurQuatreSontDesServicesDeLien(t *testing.T) {
	t.Parallel()

	cas := []struct {
		service Service
		want    bool
	}{
		{service: ServiceRestful, want: false},
		{service: ServiceWeb, want: true},
		{service: ServiceVisaMastercard, want: true},
		{service: ServiceRestlink, want: true},
		{service: "", want: false},
		{service: "QUELQUE_CHOSE_DE_NEUF", want: false},
	}

	for _, c := range cas {
		t.Run(string(c.service), func(t *testing.T) {
			t.Parallel()
			if got := c.service.estUnLien(); got != c.want {
				t.Errorf("Service(%q).estUnLien() = %v, want %v", c.service, got, c.want)
			}
		})
	}
}

// Le numéro du client est CONDITIONNEL sur le lien, et la condition est le service.
//
// La page web le demande elle-même au client, les deux autres parcours en ont besoin pour
// joindre le payeur. Tenir la règle en une fonction nommée évite que la validation et la
// documentation divergent, et ce test en épingle les deux sens : ce qui l'exige, et ce qui
// n'a pas le droit de l'exiger.
func TestSeulsLaCarteEtLeLienDirectExigentLeNumeroClient(t *testing.T) {
	t.Parallel()

	cas := []struct {
		service Service
		want    bool
	}{
		{service: ServiceWeb, want: false},
		{service: ServiceVisaMastercard, want: true},
		{service: ServiceRestlink, want: true},
		{service: ServiceRestful, want: false},
	}

	for _, c := range cas {
		t.Run(string(c.service), func(t *testing.T) {
			t.Parallel()
			if got := c.service.exigeLeNumeroClient(); got != c.want {
				t.Errorf("Service(%q).exigeLeNumeroClient() = %v, want %v", c.service, got, c.want)
			}
		})
	}
}
