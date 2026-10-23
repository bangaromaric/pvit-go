package pvit

import (
	"regexp"
	"strings"
)

// Operateur est le code d'un opérateur de mobile money, tel que la passerelle l'écrit.
//
// Le type reste ouvert plutôt que scellé : la passerelle en sert d'autres que les deux
// nommés ici, dont les familles GIMAC et Visa, et la liste vivante se lit par
// [Client.Operateurs]. La valeur zéro désigne l'absence d'opérateur, que
// [Client.Appeler] lit comme « aucune clé secrète à envoyer ».
type Operateur string

const (
	// OperateurAirtel est Airtel Money, autour de 55 % des comptes gabonais.
	OperateurAirtel Operateur = "AIRTEL_MONEY"

	// OperateurMoov est Moov Money, autour de 37 % des comptes gabonais.
	OperateurMoov Operateur = "MOOV_MONEY"
)

// formeOperateur est la forme qu'ont tous les codes publiés par la passerelle.
var formeOperateur = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,39}$`)

// NouvelOperateur valide un code d'opérateur venu de l'extérieur du programme.
//
// Il accepte toute forme que la passerelle emploie, en capitales et soulignés, sans
// tenir de liste fermée : refuser un opérateur que la passerelle vient d'ajouter
// coûterait une livraison pour rien.
func NouvelOperateur(code string) (Operateur, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return "", ChampInvalideError{Champ: "operator_code", Raison: "vide"}
	}
	if !formeOperateur.MatchString(code) {
		return "", ChampInvalideError{
			Champ:  "operator_code",
			Valeur: code,
			Raison: "attendu des capitales, des chiffres et des soulignes",
		}
	}
	return Operateur(code), nil
}

// String rend le code tel que la passerelle l'attend.
func (o Operateur) String() string { return string(o) }

// Statut est l'état d'une transaction chez la passerelle.
//
// Le type est ouvert, et c'est une précaution délibérée. La prose de la documentation
// liste quatre valeurs, son schéma n'en énumère que trois, et un décodeur strict
// casserait donc sur AMBIGUOUS, c'est à dire précisément sur le cas où il ne faut pas
// casser. Toute valeur inconnue, la chaîne vide comprise, est traitée comme incertaine
// par [Statut.EstIncertain], et conserve son jeton pour le journal.
type Statut string

const (
	// StatutEnAttente est le statut que rend toute initiation de paiement. Il ne dit
	// PAS que le paiement est fait.
	StatutEnAttente Statut = "PENDING"

	// StatutSucces est le seul statut sur lequel on crédite quoi que ce soit.
	StatutSucces Statut = "SUCCESS"

	// StatutEchec est un refus définitif.
	StatutEchec Statut = "FAILED"

	// StatutAmbigu signale que la passerelle elle-même ne sait pas conclure. Il manque
	// de l'énumération du schéma officiel, et c'est pourquoi ce type reste ouvert.
	StatutAmbigu Statut = "AMBIGUOUS"
)

// LireStatut interprète un statut reçu, sans jamais échouer.
//
// Les espaces tombent et la casse est normalisée en capitales, puisque l'énumération est
// publiée ainsi ; un jeton inconnu est rendu tel quel, en capitales, pour qu'il reste
// lisible dans un journal au lieu de se perdre.
func LireStatut(brut string) Statut {
	return Statut(strings.ToUpper(strings.TrimSpace(brut)))
}

// EstFinal dit si le statut est acquis et ne sera plus redemandé.
func (s Statut) EstFinal() bool { return s == StatutSucces || s == StatutEchec }

// EstIncertain dit s'il faut redemander ce statut à la passerelle.
//
// Elle rend vrai pour l'attente, pour l'ambiguïté, pour la valeur zéro et pour tout
// jeton inconnu : c'est le défaut prudent, celui qui fait reposer la question plutôt que
// de conclure à tort.
func (s Statut) EstIncertain() bool { return !s.EstFinal() }

// EstSucces dit si la transaction a abouti.
func (s Statut) EstSucces() bool { return s == StatutSucces }

// String rend le jeton de statut tel qu'il a été reçu.
func (s Statut) String() string { return string(s) }

// Proprietaire désigne qui supporte une couche de frais.
//
// La passerelle en empile deux, les siens et ceux de l'opérateur, pilotées par deux
// champs distincts. La valeur zéro vaut « prendre le défaut », et ce défaut n'est pas le
// même pour un paiement et pour un remboursement : voir [DemandePaiement.Frais] et
// [DemandeRemboursement.Frais].
type Proprietaire string

const (
	// ProprietaireClient met les frais à la charge de celui qui paie.
	ProprietaireClient Proprietaire = "CUSTOMER"

	// ProprietaireMarchand met les frais à la charge du marchand, qui encaisse donc
	// moins que le montant demandé.
	ProprietaireMarchand Proprietaire = "MERCHANT"
)

// ouDefaut rend le propriétaire, ou le défaut donné quand il n'est pas renseigné.
func (p Proprietaire) ouDefaut(defaut Proprietaire) Proprietaire {
	if p == "" {
		return defaut
	}
	return p
}

// valide dit si le propriétaire est une des deux valeurs que la passerelle accepte.
func (p Proprietaire) valide() bool {
	return p == ProprietaireClient || p == ProprietaireMarchand
}

// Service est le parcours de paiement demandé à la passerelle.
type Service string

const (
	// ServiceRestful est le parcours sans navigateur : le client reçoit l'invite de
	// code secret sur son téléphone. C'est le seul qui tienne à un étal, et le défaut.
	ServiceRestful Service = "RESTFUL"

	// ServiceWeb est le parcours par page de paiement, qui demande une redirection.
	ServiceWeb Service = "WEB"

	// ServiceRestlink est le parcours par lien, sans redirection préalable.
	ServiceRestlink Service = "RESTLINK"

	// ServiceVisaMastercard est le parcours par carte bancaire.
	//
	// Il n'existe AUCUN point d'entrée carte chez la passerelle : encaisser par Visa ou
	// Mastercard, c'est demander un lien avec ce service, et rediriger le client vers
	// l'URL rendue. Voir [Client.PayerParCarte].
	ServiceVisaMastercard Service = "VISA_MASTERCARD"
)

// estUnLien dit si ce service est un des trois que l'API de lien accepte.
//
// [ServiceRestful] en est EXCLU, et c'est tout l'intérêt de cette fonction : il est le
// parcours du chemin rest, celui qui pousse l'invite de code secret sur le téléphone sans
// navigateur. L'envoyer au chemin link se ferait refuser par un code que rien n'explique,
// alors que la faute est locale et se nomme ici.
func (s Service) estUnLien() bool {
	return s == ServiceWeb || s == ServiceVisaMastercard || s == ServiceRestlink
}

// exigeLeNumeroClient dit si ce service de lien réclame le numéro du payeur.
//
// La règle est celle du tableau de champs de la passerelle, qui donne
// customer_account_number pour CONDITIONNEL sur le lien : la carte et le lien direct en
// ont besoin, la page web non, puisqu'elle le demande elle-même au client. La tenir ici,
// en un seul endroit, évite que la validation et la documentation divergent.
func (s Service) exigeLeNumeroClient() bool {
	return s == ServiceVisaMastercard || s == ServiceRestlink
}

// TypeOperation est le sens d'un mouvement d'argent.
type TypeOperation string

const (
	// OperationPaiement encaisse, et son résultat arrive de façon asynchrone.
	OperationPaiement TypeOperation = "PAYMENT"

	// OperationRemboursement rend l'argent, et son résultat est SYNCHRONE : il figure
	// dans la réponse, et aucune notification ne suit.
	OperationRemboursement TypeOperation = "GIVE_CHANGE"
)

// valide dit si le type d'opération est un de ceux que la passerelle traite.
func (t TypeOperation) valide() bool {
	return t == OperationPaiement || t == OperationRemboursement
}
