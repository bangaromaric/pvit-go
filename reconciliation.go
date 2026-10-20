package pvit

import "time"

// DelaiIncertitude est le délai au delà duquel l'absence de notification rend un statut
// douteux.
//
// Trois minutes, et ce n'est pas une estimation : c'est le « délai raisonnable » que la
// documentation de la passerelle retient dans son propre exemple. Passé ce délai, le
// statut d'une tentative sans notification doit être redemandé.
const DelaiIncertitude = 3 * time.Minute

// connu dit si le statut est une des quatre valeurs que la passerelle publie.
func (s Statut) connu() bool {
	switch s {
	case StatutEnAttente, StatutSucces, StatutEchec, StatutAmbigu:
		return true
	default:
		return false
	}
}

// TransitionPermise dit si un statut nouvellement observé peut remplacer celui qu'on
// avait déjà.
//
// Ce paquet n'a pas de stockage, donc il ne peut pas SAVOIR l'état précédent : poser une
// garde ici serait un mensonge. Il expose la grille, qui est une connaissance de la
// passerelle, et laisse l'appelant décider quoi faire d'un refus, qu'il journalise,
// alerte ou ignore.
//
// Les deux cases qui justifient cette fonction sont les retours en arrière entre états
// finaux. Un callback remis six heures après coup ne doit ni annuler un encaissement
// acquis, ni ressusciter un échec déjà expliqué au commerçant. Le retour de l'ambiguïté
// vers l'attente est refusé pour la même raison : on ne recule pas dans l'incertitude.
//
// Un statut inconnu, la valeur zéro comprise, accepte tout en partance, et n'est accepté
// en arrivée que depuis un autre inconnu : une réponse illisible ne doit pas effacer un
// état déjà su.
func TransitionPermise(ancien, nouveau Statut) bool {
	switch ancien {
	case StatutSucces:
		return nouveau == StatutSucces
	case StatutEchec:
		return nouveau == StatutEchec
	case StatutEnAttente:
		return nouveau.connu()
	case StatutAmbigu:
		return nouveau == StatutSucces || nouveau == StatutEchec || nouveau == StatutAmbigu
	default:
		return true
	}
}

// AInterroger dit s'il est temps de redemander le statut d'une tentative.
//
// Elle rend faux pour un statut final, qui ne bougera plus, et vrai dès que
// [DelaiIncertitude] s'est écoulé depuis l'initiation. Un instant d'initiation nul vaut
// « on ne sait pas quand », et la réponse est alors de demander : ne rien faire laisserait
// la tentative en suspens pour toujours.
func AInterroger(s Statut, initiee, maintenant time.Time) bool {
	if s.EstFinal() {
		return false
	}
	if initiee.IsZero() {
		return true
	}
	return !maintenant.Before(initiee.Add(DelaiIncertitude))
}

// barèmeTentatives est l'attente avant chaque réessai, puis l'abandon.
//
// Ce sont les intervalles que la passerelle applique à ses propres remises de webhook de
// reporting. Les reprendre pour interroger le statut aligne la boucle de l'appelant sur
// le rythme auquel la passerelle elle même considère qu'une remise est perdue.
var baremeTentatives = [...]time.Duration{
	5 * time.Minute,
	15 * time.Minute,
	time.Hour,
	6 * time.Hour,
}

// ProchaineTentative rend l'attente avant le réessai suivant, ou zéro pour abandonner.
//
// Le paramètre est le nombre de tentatives DÉJÀ faites. Une valeur négative est traitée
// comme zéro : c'est une faute d'appel, et un paquet de fondation ne fait pas paniquer le
// programme qui l'utilise. Au delà du barème, elle rend zéro, et c'est à l'appelant de
// décider ce qu'une tentative abandonnée devient.
func ProchaineTentative(essais int) time.Duration {
	if essais < 0 {
		essais = 0
	}
	if essais >= len(baremeTentatives) {
		return 0
	}
	return baremeTentatives[essais]
}
