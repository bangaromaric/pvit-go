package pvit

import (
	"testing"
	"time"
)

// La grille entière, case par case, parce que deux d'entre elles valent de l'argent : un
// callback remis six heures après coup ne doit ni annuler un encaissement acquis, ni
// ressusciter un échec déjà expliqué au commerçant.
func TestTransitionPermiseCouvreTouteLaGrille(t *testing.T) {
	t.Parallel()

	const inconnu = Statut("WEIRD")

	cas := []struct {
		nom     string
		ancien  Statut
		nouveau Statut
		want    bool
	}{
		{nom: "inconnu vers en attente", ancien: inconnu, nouveau: StatutEnAttente, want: true},
		{nom: "inconnu vers succes", ancien: inconnu, nouveau: StatutSucces, want: true},
		{nom: "inconnu vers echec", ancien: inconnu, nouveau: StatutEchec, want: true},
		{nom: "inconnu vers ambigu", ancien: inconnu, nouveau: StatutAmbigu, want: true},
		{nom: "inconnu vers inconnu", ancien: inconnu, nouveau: inconnu, want: true},

		{nom: "en attente vers en attente", ancien: StatutEnAttente, nouveau: StatutEnAttente, want: true},
		{nom: "en attente vers succes", ancien: StatutEnAttente, nouveau: StatutSucces, want: true},
		{nom: "en attente vers echec", ancien: StatutEnAttente, nouveau: StatutEchec, want: true},
		{nom: "en attente vers ambigu", ancien: StatutEnAttente, nouveau: StatutAmbigu, want: true},
		{nom: "en attente vers inconnu refuse", ancien: StatutEnAttente, nouveau: inconnu, want: false},

		{nom: "ambigu vers en attente refuse", ancien: StatutAmbigu, nouveau: StatutEnAttente, want: false},
		{nom: "ambigu vers succes", ancien: StatutAmbigu, nouveau: StatutSucces, want: true},
		{nom: "ambigu vers echec", ancien: StatutAmbigu, nouveau: StatutEchec, want: true},
		{nom: "ambigu vers ambigu", ancien: StatutAmbigu, nouveau: StatutAmbigu, want: true},
		{nom: "ambigu vers inconnu refuse", ancien: StatutAmbigu, nouveau: inconnu, want: false},

		{nom: "succes vers succes", ancien: StatutSucces, nouveau: StatutSucces, want: true},
		{nom: "succes vers echec refuse", ancien: StatutSucces, nouveau: StatutEchec, want: false},
		{nom: "succes vers en attente refuse", ancien: StatutSucces, nouveau: StatutEnAttente, want: false},
		{nom: "succes vers ambigu refuse", ancien: StatutSucces, nouveau: StatutAmbigu, want: false},
		{nom: "succes vers inconnu refuse", ancien: StatutSucces, nouveau: inconnu, want: false},

		{nom: "echec vers echec", ancien: StatutEchec, nouveau: StatutEchec, want: true},
		{nom: "echec vers succes refuse", ancien: StatutEchec, nouveau: StatutSucces, want: false},
		{nom: "echec vers en attente refuse", ancien: StatutEchec, nouveau: StatutEnAttente, want: false},
		{nom: "echec vers ambigu refuse", ancien: StatutEchec, nouveau: StatutAmbigu, want: false},
		{nom: "echec vers inconnu refuse", ancien: StatutEchec, nouveau: inconnu, want: false},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()
			if got := TransitionPermise(c.ancien, c.nouveau); got != c.want {
				t.Errorf("TransitionPermise(%q, %q) = %v, want %v", c.ancien, c.nouveau, got, c.want)
			}
		})
	}
}

// La valeur zéro d'un Statut doit se comporter comme l'inconnu, puisque c'est ce qu'elle
// est : un état dont on ne sait rien, qui accepte d'être renseigné par n'importe quoi.
func TestLaValeurZeroSeComporteCommeUnStatutInconnu(t *testing.T) {
	t.Parallel()

	var zero Statut
	for _, nouveau := range []Statut{StatutEnAttente, StatutSucces, StatutEchec, StatutAmbigu} {
		if got := TransitionPermise(zero, nouveau); !got {
			t.Errorf("TransitionPermise(zero, %q) = false, want true", nouveau)
		}
	}
	if got := TransitionPermise(StatutSucces, zero); got {
		t.Errorf("TransitionPermise(SUCCESS, zero) = true, want false")
	}
}

func TestAInterrogerAttendLeDelaiDIncertitude(t *testing.T) {
	t.Parallel()

	initiee := time.Date(2026, time.October, 20, 8, 0, 0, 0, time.UTC)

	cas := []struct {
		nom        string
		statut     Statut
		initiee    time.Time
		maintenant time.Time
		want       bool
	}{
		{
			nom: "en attente avant trois minutes", statut: StatutEnAttente, initiee: initiee,
			maintenant: initiee.Add(2 * time.Minute), want: false,
		},
		{
			nom: "en attente a trois minutes pile", statut: StatutEnAttente, initiee: initiee,
			maintenant: initiee.Add(DelaiIncertitude), want: true,
		},
		{
			nom: "en attente apres trois minutes", statut: StatutEnAttente, initiee: initiee,
			maintenant: initiee.Add(10 * time.Minute), want: true,
		},
		{
			nom: "ambigu apres trois minutes", statut: StatutAmbigu, initiee: initiee,
			maintenant: initiee.Add(10 * time.Minute), want: true,
		},
		{
			nom: "inconnu apres trois minutes", statut: Statut("WEIRD"), initiee: initiee,
			maintenant: initiee.Add(10 * time.Minute), want: true,
		},
		{
			nom: "succes jamais redemande", statut: StatutSucces, initiee: initiee,
			maintenant: initiee.Add(24 * time.Hour), want: false,
		},
		{
			nom: "echec jamais redemande", statut: StatutEchec, initiee: initiee,
			maintenant: initiee.Add(24 * time.Hour), want: false,
		},
		{
			nom: "initiation inconnue se redemande tout de suite", statut: StatutEnAttente,
			initiee: time.Time{}, maintenant: initiee, want: true,
		},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()
			got := AInterroger(c.statut, c.initiee, c.maintenant)
			if got != c.want {
				t.Errorf("AInterroger(%q, %v, %v) = %v, want %v",
					c.statut, c.initiee, c.maintenant, got, c.want)
			}
		})
	}
}

// Le barème est celui que la passerelle applique à ses propres remises, et zéro veut dire
// abandonner. Un nombre négatif est une faute d'appel, qu'un paquet de fondation traite
// sans faire paniquer le programme.
func TestProchaineTentativeSuitLeBaremeDeLaPasserelle(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom    string
		essais int
		want   time.Duration
	}{
		{nom: "aucune tentative encore", essais: 0, want: 5 * time.Minute},
		{nom: "apres la premiere", essais: 1, want: 15 * time.Minute},
		{nom: "apres la deuxieme", essais: 2, want: time.Hour},
		{nom: "apres la troisieme", essais: 3, want: 6 * time.Hour},
		{nom: "apres la quatrieme on abandonne", essais: 4, want: 0},
		{nom: "bien au dela on abandonne", essais: 1000, want: 0},
		{nom: "negatif traite comme zero", essais: -5, want: 5 * time.Minute},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()
			if got := ProchaineTentative(c.essais); got != c.want {
				t.Errorf("ProchaineTentative(%d) = %v, want %v", c.essais, got, c.want)
			}
		})
	}
}
