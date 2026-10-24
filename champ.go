package pvit

import (
	"crypto/rand"
	"fmt"
	"regexp"
	"strings"
)

// Les longueurs que la passerelle valide côté serveur.
//
// Elles ne sont pas des précautions de confort : les laisser passer coûte un
// aller-retour et rend un 8000 CONSTRAINT_VIOLATION dont le corps ne nomme pas toujours
// le champ fautif. Vérifier ici donne le nom du champ tout de suite, et sans requête.
const (
	maxReference     = 20
	maxCodeCallback  = 12
	maxAgent         = 15
	maxProduit       = 15
	maxInfoLibre     = 15
	maxNumeroClient  = 23
	maxCodeOperation = 30
)

// maxValeurJournalisee borne la valeur recopiée dans un message d'erreur.
const maxValeurJournalisee = 32

// ChampInvalideError signale une valeur que la passerelle refuserait.
//
// Elle est rendue AVANT toute requête, et emballe [ErrRequeteInvalide] : un rejeu
// échouerait à l'identique, puisque la faute est dans l'appel et non dans le réseau.
type ChampInvalideError struct {
	// Champ est le nom du champ tel que la passerelle l'écrit, donc en anglais.
	Champ string

	// Valeur est la valeur fautive, tronquée pour ne pas gonfler un journal.
	Valeur string

	// Raison dit ce qui cloche quand ce n'est pas la longueur.
	Raison string

	// Longueur et Max sont renseignés quand c'est la longueur qui cloche.
	Longueur int
	Max      int
}

// Error rend une phrase qui nomme le champ, puisque c'est la seule chose à corriger.
func (e ChampInvalideError) Error() string {
	switch {
	case e.Raison != "" && e.Valeur != "":
		return fmt.Sprintf("champ %s: %s: %q", e.Champ, e.Raison, e.Valeur)
	case e.Raison != "":
		return fmt.Sprintf("champ %s: %s", e.Champ, e.Raison)
	case e.Max > 0:
		return fmt.Sprintf("champ %s: %d caracteres, maximum %d", e.Champ, e.Longueur, e.Max)
	default:
		return "champ " + e.Champ + ": invalide"
	}
}

// Unwrap rattache la faute à la catégorie que l'appelant interroge.
func (e ChampInvalideError) Unwrap() error { return ErrRequeteInvalide }

// borner tronque une valeur destinée à un message d'erreur, en comptant les octets.
func borner(valeur string) string {
	if len(valeur) <= maxValeurJournalisee {
		return valeur
	}
	return valeur[:maxValeurJournalisee] + "..."
}

// texteBorne vérifie la longueur d'un champ libre, en caractères et non en octets.
//
// La distinction compte : la passerelle compte des caractères, et « Épicerie » pèse neuf
// octets pour huit caractères. Compter en octets refuserait une valeur que le serveur
// accepte.
func texteBorne(champ, valeur string, max int) error {
	if n := len([]rune(valeur)); n > max {
		return ChampInvalideError{Champ: champ, Valeur: borner(valeur), Longueur: n, Max: max}
	}
	return nil
}

// Reference est l'identifiant que le marchand donne à une tentative.
//
// C'EST LA CLÉ D'IDEMPOTENCE, et non un identifiant d'affichage. La passerelle refuse
// une référence déjà vue par le code 5000, ce qui veut dire « cette tentative existe
// déjà, va interroger son statut ». Conséquence pour l'appelant : une référence stable
// par tentative, persistée AVANT l'appel, et jamais régénérée pour un rejeu.
//
// C'EST AUSSI LA CLÉ DE L'INTERROGATION DE STATUT, voir [DemandeStatut.Reference]. Elle
// porte donc les deux bouts d'une tentative, et c'est ce qui rend sa persistance avant
// l'appel non négociable. L'identifiant que la passerelle attribue de son côté, lui, est
// informatif : il vit dans [Initiation.TransactionID].
type Reference string

// formeReference est la seule forme que la passerelle accepte.
var formeReference = regexp.MustCompile(`^[A-Za-z0-9]+$`)

// NouvelleReference valide une référence choisie par l'appelant.
func NouvelleReference(valeur string) (Reference, error) {
	valeur = strings.TrimSpace(valeur)
	if valeur == "" {
		return "", ChampInvalideError{Champ: "reference", Raison: "vide"}
	}
	if !formeReference.MatchString(valeur) {
		return "", ChampInvalideError{
			Champ:  "reference",
			Valeur: borner(valeur),
			Raison: "attendu des lettres et des chiffres seulement",
		}
	}
	if n := len(valeur); n > maxReference {
		return "", ChampInvalideError{
			Champ: "reference", Valeur: borner(valeur), Longueur: n, Max: maxReference,
		}
	}
	return Reference(valeur), nil
}

// ReferenceAleatoire fabrique une référence imprévisible, préfixe compris.
//
// Elle tire de crypto/rand et jamais de math/rand : une référence devinable laisserait
// un tiers sonder le statut des transactions d'autrui. Le préfixe sert à reconnaître ses
// propres tentatives dans l'espace marchand, et la partie aléatoire remplit le reste des
// vingt caractères disponibles.
//
// La longueur de crypto/rand.Text n'est pas garantie par son contrat : elle est donc
// tronquée, et une rallonge future de la bibliothèque standard ne cassera rien.
func ReferenceAleatoire(prefixe string) (Reference, error) {
	prefixe = strings.ToUpper(strings.TrimSpace(prefixe))
	if prefixe != "" && !formeReference.MatchString(prefixe) {
		return "", ChampInvalideError{
			Champ:  "reference",
			Valeur: borner(prefixe),
			Raison: "prefixe: attendu des lettres et des chiffres seulement",
		}
	}
	reste := maxReference - len(prefixe)
	if reste < 8 {
		return "", ChampInvalideError{
			Champ:    "reference",
			Valeur:   borner(prefixe),
			Longueur: len(prefixe),
			Max:      maxReference - 8,
			Raison:   "prefixe trop long, il ne reste pas huit caracteres d alea",
		}
	}
	alea := rand.Text()
	if len(alea) > reste {
		alea = alea[:reste]
	}
	return Reference(prefixe + alea), nil
}

// String rend la référence telle que la passerelle l'attend.
func (r Reference) String() string { return string(r) }

// taillee rend la référence débarrassée de ses espaces de bord.
//
// ELLE EXISTE POUR QUE LES DEUX BOUTS D'UNE TENTATIVE PARLENT DE LA MÊME CHOSE, et c'est
// la leçon que normaliserComptes a déjà apprise au prix d'une erreur de transport que rien
// n'expliquait : [NouvelleReference] taillait pour JUGER, puis la valeur brute partait sur
// le réseau.
//
// Relevé par audit, et non par une panne : une référence portant une espace ou un retour à
// la ligne passait la validation, partait sur le fil avec, et l'interrogation de statut,
// qui taille elle aussi, la redemandait sans. Les deux moitiés d'une même tentative ne se
// rejoignaient donc plus, ce qui est la forme exacte de la panne que ce paquet a déjà payée
// six heures en production par un autre chemin. Un test l'épingle maintenant des deux
// côtés.
//
// Toute émission et toute interrogation passent par ici. Les champs libres, eux, ne sont
// pas taillés : ils ne portent aucune identité, et une espace finale dans une information
// libre ne rend rien introuvable.
func (r Reference) taillee() Reference { return Reference(strings.TrimSpace(string(r))) }

// Absente dit si la référence n'est pas renseignée.
func (r Reference) Absente() bool { return r == "" }
