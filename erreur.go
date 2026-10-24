package pvit

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Les motifs d'échec sur lesquels l'appelant a une décision à prendre.
//
// Des sentinelles plates, interrogeables par errors.Is, et non une hiérarchie de types :
// ce qui intéresse l'appelant est « puis-je réessayer » et « est-ce ma faute », pas la
// généalogie de l'erreur. La passerelle publie quatre-vingts codes, et ils se rangent
// tous derrière l'une de ces douze réponses.
//
// Aucune ne porte de code HTTP, et c'est volontaire : ce paquet est une fondation, il ne
// décide de rien d'applicatif. Le projet qui le câble tient son unique point de
// traduction vers ses propres catégories métier et vers ses codes de réponse.
var (
	// ErrConfiguration signale un réglage absent ou incohérent. Selon le moment, aucune
	// requête n'a été émise, ou la passerelle a refusé un code d'URL ou un appariement
	// compte et opérateur. Un rejeu ne répare rien : il faut compléter la configuration.
	ErrConfiguration = errors.New("configuration de la passerelle incomplete")

	// ErrRequeteInvalide signale un refus de contrat : champ hors bornes, ressource
	// introuvable, transaction expirée. NE JAMAIS REJOUER : un rejeu échouerait à
	// l'identique et transformerait une faute d'appel en tempête de requêtes.
	ErrRequeteInvalide = errors.New("la passerelle refuserait cette requete")

	// ErrAuthentification signale une clé secrète refusée, ou refusée deux fois de
	// suite malgré un renouvellement. Un troisième essai échouerait à l'identique.
	ErrAuthentification = errors.New("la passerelle a refuse la cle secrete")

	// ErrRotationImpossible signale une clé périmée qu'aucun mot de passe ne permet de
	// renouveler.
	//
	// ELLE EMBALLE [ErrConfiguration] et non [ErrAuthentification], et c'est la
	// distinction utile : le geste correctif est de renseigner un mot de passe de
	// rotation, pas de réessayer. [Erreur.EstRejouable] rend donc faux, et une boucle
	// de réconciliation ne s'acharnera pas sur un compte qu'elle ne peut pas réparer.
	ErrRotationImpossible = fmt.Errorf("%w: cle secrete expiree et aucun mot de passe de rotation configure", ErrConfiguration)

	// ErrDejaInitiee signale une tentative qui existe déjà.
	//
	// CE N'EST PAS UN ÉCHEC. Le champ reference porte l'idempotence, et ce refus veut
	// dire « cette tentative existe déjà, va interroger son statut ». La traiter comme
	// une erreur et relancer sous une nouvelle référence créerait un double paiement.
	//
	// Elle couvre DEUX refus, parce que la conduite est la même : une référence déjà
	// employée (code 5000) et un lien de paiement déjà consommé (code 5002). Dans le
	// second cas, le client est allé au bout du parcours bancaire, et régénérer un lien
	// sous une référence neuve le ferait payer deux fois. [Erreur.Code] distingue les
	// deux diagnostics pour qui veut le dire au marchand dans ses mots.
	ErrDejaInitiee = errors.New("transaction deja initiee sous cette reference")

	// ErrRefusClient signale un compte client bloqué ou inconnu chez l'opérateur. Le
	// commerçant doit corriger le numéro, ou le client appeler son opérateur.
	ErrRefusClient = errors.New("le compte du client a ete refuse")

	// ErrHorsBornes signale un montant sous le minimum ou au dessus du plafond de
	// l'opérateur. Ces seuils ne sont publiés nulle part et se découvrent ainsi.
	ErrHorsBornes = errors.New("montant hors des bornes de l operateur")

	// ErrSoldeInsuffisant signale un compte d'opération trop pauvre pour l'opération,
	// ou un plafond atteint. Rejouable plus tard, jamais tout de suite.
	ErrSoldeInsuffisant = errors.New("solde du compte d operation insuffisant")

	// ErrIndisponible signale une panne, une maintenance ou un délai dépassé. Rejouable.
	// C'est le défaut quand on ne sait pas classer, parce que c'est le moins coûteux :
	// un rejeu différé sur une erreur de contrat se voit dans les journaux, tandis qu'un
	// refus définitif prononcé sur une panne passagère perd un encaissement.
	ErrIndisponible = errors.New("la passerelle est indisponible")

	// ErrReponseIllisible signale une réponse dont on ne peut rien tirer, y compris
	// avec un code de succès. C'est un ÉCHEC malgré le 2xx : sans identifiant ni
	// statut, la transaction est intraçable, donc ni réconciliable ni opposable au
	// support.
	ErrReponseIllisible = errors.New("la passerelle a repondu une charge utile inexploitable")

	// ErrMontantInvalide signale une somme nulle ou négative, refusée avant l'appel.
	ErrMontantInvalide = errors.New("montant nul ou negatif")

	// ErrMontantFractionnaire signale une somme non entière reçue là où la devise n'a
	// pas de subdivision. Voir [Frais] pour les sommes où le millième est légitime.
	ErrMontantFractionnaire = errors.New("montant non entier pour une devise sans subdivision")
)

// Les codes applicatifs qui décident d'une conduite.
//
// Vingt-et-un noms pour quatre-vingts codes, et c'est une décision. Les autres se rangent par
// plage dans motifDuCode, et [Erreur.Nom] garde le nom du fournisseur verbatim pour le
// journal. Nommer quatre-vingts constantes dont cinquante-neuf ne pilotent aucune
// branche produirait du code mort, qui pourrit au premier code ajouté par la passerelle.
const (
	// CodeIPMarchandInvalide est le filtrage SORTANT : la passerelle ne reconnaît pas
	// l'adresse d'où part l'appel. Rien dans ce paquet ne le règle, cela se déclare
	// dans l'espace marchand. À ne pas confondre avec [IPsAutorisees], qui sert au
	// filtrage entrant.
	CodeIPMarchandInvalide = 1000

	// CodeEnvironnementInvalide sanctionne un mélange de bac à sable et de production,
	// typiquement un code d'URL d'un environnement avec un compte de l'autre.
	CodeEnvironnementInvalide = 1001

	// CodeRedirectionInvalide est un code d'URL de redirection de succès que la
	// passerelle ne connaît pas. Il se déclare dans le menu Urls de l'espace marchand,
	// et c'est son CODE, et non l'URL, qui part dans la charge utile du lien.
	CodeRedirectionInvalide = 1003

	// CodeRedirectionEchecInvalide est le même refus pour l'URL de redirection d'échec.
	// La passerelle en distingue deux codes, ce qui dit lequel des deux corriger.
	CodeRedirectionEchecInvalide = 1004

	// CodeSoldeInsuffisant est le compte d'opération trop pauvre.
	CodeSoldeInsuffisant = 2000

	// CodePlafondAtteint est le plafond du compte d'opération.
	CodePlafondAtteint = 2001

	// CodeRedirectionInactive est une URL de redirection de succès connue mais
	// désactivée. Le code existe, il est juste éteint dans l'espace marchand : la
	// distinction avec 1003 est celle d'un code faux et d'un code au repos.
	CodeRedirectionInactive = 3012

	// CodeRedirectionEchecInactive est le même refus pour l'URL de redirection d'échec.
	CodeRedirectionEchecInactive = 3013

	// CodeClientBloque est un compte client bloqué chez l'opérateur.
	CodeClientBloque = 3014

	// CodeOperateurCompteDesaccord sanctionne un compte d'opération qui n'est pas celui
	// de l'opérateur visé. C'est la faute que la résolution par opérateur évite.
	CodeOperateurCompteDesaccord = 3017

	// CodeMontantTropFaible est le minimum de l'opérateur, dont la valeur n'est publiée
	// nulle part.
	CodeMontantTropFaible = 3018

	// CodeMontantMaxDepasse est le plafond de l'opérateur, même remarque.
	CodeMontantMaxDepasse = 3019

	// CodeClientInconnu est un numéro que l'opérateur ne connaît pas.
	CodeClientInconnu = 3023

	// CodeAuthentificationEchouee est la clé secrète refusée. C'est lui, et lui seul,
	// qui déclenche un renouvellement suivi d'un unique rejeu.
	CodeAuthentificationEchouee = 3100

	// CodeReferenceDejaUtilisee est la référence déjà vue. Voir [ErrDejaInitiee].
	CodeReferenceDejaUtilisee = 5000

	// CodeLienConsomme est un lien de paiement déjà utilisé. Voir [ErrLienConsomme] pour
	// la conduite, qui n'est pas d'en régénérer un.
	CodeLienConsomme = 5002

	// CodeTransactionExpiree est une transaction trop ancienne pour être reprise. Pour un
	// lien de paiement, c'est sa durée de vie qui est écoulée, et la passerelle ne publie
	// cette durée nulle part.
	CodeTransactionExpiree = 6000

	// CodeContrainteViolee est la validation de champ, et son corps porte un tableau
	// messages qui nomme chaque faute.
	CodeContrainteViolee = 8000

	// CodeMaintenance est une interruption annoncée.
	CodeMaintenance = 10000

	// CodeServiceIndisponible est une panne.
	CodeServiceIndisponible = 10001

	// CodeEnvoiCleEchoue est l'échec d'envoi de la clé secrète. Il existe, et c'est
	// pourquoi on ne renouvelle pas la clé avant chaque paiement.
	CodeEnvoiCleEchoue = 10008
)

// longueurMaxRaison borne le texte recopié depuis un corps de réponse.
//
// Sans borne, une page d'erreur HTML d'un proxy intermédiaire entrerait entière dans un
// message d'erreur, puis dans chaque ligne de journal qui le reprend.
const longueurMaxRaison = 256

// Erreur est un refus de la passerelle, tel qu'elle l'a formulé.
//
// Le discriminant utile est [Erreur.Code], l'entier applicatif, et non le code HTTP : un
// seul 403 couvre vingt-cinq cas distincts. Les prédicats de ce type répondent aux
// questions de conduite, et errors.Is sur les sentinelles répond à la question « puis-je
// réessayer ».
//
// Ce type PORTE son contexte dans des champs au lieu de l'empiler dans son message. Un
// paquet de fondation ne doit pas emballer la cause racine derrière des préfixes
// successifs : [Erreur.Operation] dit ce qui était tenté, et le message reste une phrase.
type Erreur struct {
	// Operation dit ce qui était tenté, en un mot : paiement, carte, lien,
	// remboursement, statut, frais, solde, kyc, renouvellement, operateurs, pays, appel.
	//
	// La carte et le lien se distinguent alors qu'ils partent au même point d'entrée,
	// comme le paiement et le remboursement le font déjà : ce champ nomme l'INTENTION de
	// l'appelant, qui est ce qu'une ligne de journal doit rendre, et non la route servie,
	// que [Erreur.Chemin] porte.
	Operation string

	// Operateur est le compte concerné, quand l'appel en visait un.
	Operateur Operateur

	// Code est le status_code applicatif, le vrai discriminant. Zéro quand le corps est
	// illisible.
	Code int

	// HTTP est le code de réponse, utile au journal mais insuffisant pour décider.
	HTTP int

	// Nom est le nom du code chez la passerelle, recopié verbatim.
	Nom string

	// Message est la phrase de la passerelle, bornée.
	Message string

	// Messages porte le détail des violations de contrainte, pour le code 8000.
	Messages []string

	// Chemin est la route que la passerelle dit avoir servie.
	Chemin string

	// Champs nomme les champs fautifs, quand la passerelle les détaille.
	Champs map[string]string

	// Date est l'horodatage du corps d'erreur, nul s'il était illisible.
	Date time.Time

	motif error
	cause error
}

// Error rend une phrase qui nomme l'opération, le code applicatif et la raison.
//
// Elle n'empile pas le motif derrière le message de la passerelle : [Erreur.Nom] le dit
// déjà en clair, et ce dépôt refuse les messages à tiroirs. Le motif n'apparaît que
// lorsque le corps n'a rien donné à lire.
func (e Erreur) Error() string {
	var b strings.Builder
	b.WriteString("passerelle pvit")
	if e.Operation != "" {
		b.WriteString(" ")
		b.WriteString(e.Operation)
	}
	if e.Operateur != "" {
		b.WriteString(" ")
		b.WriteString(string(e.Operateur))
	}
	if e.HTTP != 0 {
		fmt.Fprintf(&b, ": http %d", e.HTTP)
		if e.Code != 0 {
			fmt.Fprintf(&b, ", code %d", e.Code)
		}
	}
	if e.Nom != "" {
		b.WriteString(" ")
		b.WriteString(e.Nom)
	}
	switch {
	case e.Message != "":
		b.WriteString(": ")
		b.WriteString(e.Message)
	case e.Nom == "" && e.motif != nil:
		b.WriteString(": ")
		b.WriteString(e.motif.Error())
	}
	if e.cause != nil {
		b.WriteString(": ")
		b.WriteString(e.cause.Error())
	}
	return b.String()
}

// Unwrap rattache le refus à son motif de conduite ET à sa cause racine.
//
// Deux erreurs et non une, parce qu'un paquet de fondation n'a pas le droit d'enterrer la
// cause : un appel abandonné par annulation de contexte doit rester reconnaissable par
// errors.Is(err, context.Canceled), en même temps que par errors.Is(err, ErrIndisponible).
// Rendre une tranche évite d'empiler les messages, ce que ce dépôt refuse, tout en gardant
// les deux chaînes interrogeables.
func (e Erreur) Unwrap() []error {
	switch {
	case e.motif != nil && e.cause != nil:
		return []error{e.motif, e.cause}
	case e.motif != nil:
		return []error{e.motif}
	case e.cause != nil:
		return []error{e.cause}
	default:
		return nil
	}
}

// EstRejouable dit si le même appel a une chance d'aboutir plus tard.
//
// Elle est vraie pour une indisponibilité et pour un solde insuffisant, fausse pour tout
// refus de contrat, d'authentification ou de configuration.
func (e Erreur) EstRejouable() bool {
	return errors.Is(e.motif, ErrIndisponible) || errors.Is(e.motif, ErrSoldeInsuffisant)
}

// EstDejaInitiee dit si la tentative existe déjà, donc s'il faut interroger le statut au
// lieu de relancer.
//
// Elle répond vrai pour les DEUX façons dont elle peut exister : une référence déjà
// employée, et un lien de paiement déjà consommé. Un seul prédicat pour les deux, parce
// qu'il n'y a qu'une conduite, et [Erreur.Code] reste là pour le diagnostic.
func (e Erreur) EstDejaInitiee() bool {
	return e.Code == CodeReferenceDejaUtilisee || e.Code == CodeLienConsomme
}

// EstRefusDuClient dit si c'est le compte du client qui est en cause, et non l'appel.
func (e Erreur) EstRefusDuClient() bool {
	return e.Code == CodeClientBloque || e.Code == CodeClientInconnu
}

// EstHorsBornes dit si le montant sort des seuils de l'opérateur.
func (e Erreur) EstHorsBornes() bool {
	return e.Code == CodeMontantTropFaible || e.Code == CodeMontantMaxDepasse
}

// EstFauteDeConfiguration dit si le refus se corrige dans l'espace marchand ou dans les
// réglages, et non dans l'appel.
func (e Erreur) EstFauteDeConfiguration() bool {
	switch {
	case e.Code >= 1000 && e.Code <= 1012:
		return true
	case e.Code >= 9000 && e.Code <= 9002:
		return true
	case e.Code == CodeOperateurCompteDesaccord:
		return true
	case e.Code == CodeRedirectionInactive, e.Code == CodeRedirectionEchecInactive:
		return true
	default:
		return false
	}
}

// EstAuthentification dit si la clé secrète a été refusée.
func (e Erreur) EstAuthentification() bool { return e.Code == CodeAuthentificationEchouee }

// EstSoldeInsuffisant dit si le compte d'opération manque de fonds, ou touche un plafond.
func (e Erreur) EstSoldeInsuffisant() bool {
	return e.Code == CodeSoldeInsuffisant || e.Code == CodePlafondAtteint
}

// EstContrainteViolee dit si un champ a été refusé par la validation, auquel cas
// [Erreur.Messages] et [Erreur.Champs] nomment lesquels.
func (e Erreur) EstContrainteViolee() bool { return e.Code == CodeContrainteViolee }

// corpsErreur est la forme uniforme d'un refus. Chaque champ dont le type peut varier
// entre en valeur brute, parce qu'un seul champ mal typé ne doit pas empêcher de lire le
// code, qui est la seule information dont l'appelant a besoin pour décider.
type corpsErreur struct {
	Date string `json:"date"`

	// StatusCode et StatusCodeCC portent LE MÊME code sous les deux casses que la
	// passerelle emploie.
	//
	// Les corps d'erreur publiés sont en soulignés, mais l'API de lien de paiement répond
	// en casse mêlée, code de retour compris. Ne déclarer que les soulignés rendait donc
	// le DISCRIMINANT DE TOUTE LA PASSERELLE illisible dès que ce point d'entrée refusait
	// quelque chose dans sa propre casse : le refus retombait alors sur la plage du code
	// HTTP, qui ne distingue pas vingt-cinq cas d'un 403, et une faute de configuration
	// ressortait en indisponibilité rejouable.
	StatusCode   jsontext.Value `json:"status_code"`
	StatusCodeCC jsontext.Value `json:"statusCode"`

	Nom      jsontext.Value            `json:"error"`
	Message  jsontext.Value            `json:"message"`
	Messages []jsontext.Value          `json:"messages"`
	Chemin   jsontext.Value            `json:"path"`
	Champs   map[string]jsontext.Value `json:"errors"`
}

// code rend le code applicatif, quelle que soit la casse sous laquelle il arrive.
func (c corpsErreur) code() (int, bool) {
	for _, brut := range []jsontext.Value{c.StatusCode, c.StatusCodeCC} {
		if valeur, ok := codeApplicatif(brut); ok {
			return valeur, true
		}
	}
	return 0, false
}

// classer traduit une réponse de refus en [Erreur].
//
// Elle prend un corps déjà lu et borné plutôt qu'une réponse HTTP : c'est ce qui la rend
// éprouvable sans serveur, et ce qui garde la lecture bornée en un seul endroit.
//
// Quand le corps est illisible, et la version 2 de encoding/json le refuse désormais sur
// un nom dupliqué ou un UTF-8 invalide, le classement retombe sur les plages de codes
// HTTP. On perd le détail, jamais la décision.
func classer(operation string, operateur Operateur, statut int, corps []byte) Erreur {
	e := Erreur{Operation: operation, Operateur: operateur, HTTP: statut}

	var c corpsErreur
	if err := json.Unmarshal(corps, &c); err == nil {
		if code, ok := c.code(); ok {
			e.Code = code
		}
		if nom, ok := texte(c.Nom); ok {
			e.Nom = tronquer(nom)
		}
		if msg, ok := texte(c.Message); ok {
			e.Message = tronquer(msg)
		}
		if chemin, ok := texte(c.Chemin); ok {
			e.Chemin = tronquer(chemin)
		}
		for _, brut := range c.Messages {
			if m, ok := texte(brut); ok {
				e.Messages = append(e.Messages, tronquer(m))
			}
		}
		for champ, brut := range c.Champs {
			if v, ok := texte(brut); ok {
				if e.Champs == nil {
					e.Champs = make(map[string]string, len(c.Champs))
				}
				e.Champs[champ] = tronquer(v)
			}
		}
		if d, ok := datePVit(c.Date); ok {
			e.Date = d
		}
	} else if brut := tronquer(string(corps)); brut != "" {
		e.Message = brut
	}

	e.motif = motifDuCode(e.Code)
	if e.motif == nil {
		e.motif = motifDuStatut(statut)
	}
	return e
}

// motifDuCode range un code applicatif derrière une sentinelle, ou renonce.
//
// Les cas nommés passent AVANT les plages, et trois d'entre eux sortent leur code de la
// conduite que sa plage lui donnerait : 3017 est une faute de configuration alors que le
// reste de la famille 3000 est un refus de contrat, 3012 et 3013 le sont aussi puisqu'une
// URL de redirection éteinte se rallume dans l'espace marchand, et 3100 est une
// authentification.
func motifDuCode(code int) error {
	switch {
	case code == 0:
		return nil

	case code == CodeAuthentificationEchouee:
		return ErrAuthentification
	case code == CodeReferenceDejaUtilisee, code == CodeLienConsomme:
		return ErrDejaInitiee
	case code == CodeClientBloque, code == CodeClientInconnu:
		return ErrRefusClient
	case code == CodeMontantTropFaible, code == CodeMontantMaxDepasse:
		return ErrHorsBornes
	case code == CodeSoldeInsuffisant, code == CodePlafondAtteint:
		return ErrSoldeInsuffisant
	case code == CodeOperateurCompteDesaccord:
		return ErrConfiguration
	case code == CodeRedirectionInactive, code == CodeRedirectionEchecInactive:
		return ErrConfiguration
	case code == CodeMaintenance, code == CodeServiceIndisponible, code == CodeEnvoiCleEchoue:
		return ErrIndisponible

	case code >= 1000 && code <= 1012:
		return ErrConfiguration
	case code >= 9000 && code <= 9002:
		return ErrConfiguration
	case code >= 3000 && code <= 3023:
		return ErrRequeteInvalide
	case code >= 4000 && code <= 4009:
		return ErrRequeteInvalide
	case code >= 5000 && code <= 5003:
		return ErrRequeteInvalide
	case code >= 5400 && code <= 5417:
		return ErrRequeteInvalide
	case code == CodeTransactionExpiree:
		return ErrRequeteInvalide
	case code == CodeContrainteViolee:
		return ErrRequeteInvalide
	case code == 500:
		return ErrIndisponible

	default:
		return nil
	}
}

// motifDuStatut est le repli quand le code applicatif manque.
//
// Le défaut est l'indisponibilité, donc le rejouable, pour la raison dite sur
// [ErrIndisponible] : c'est le défaut le moins coûteux.
func motifDuStatut(statut int) error {
	switch {
	case statut == http.StatusUnauthorized, statut == http.StatusForbidden:
		return ErrAuthentification
	case statut == http.StatusPaymentRequired:
		return ErrSoldeInsuffisant
	case statut == http.StatusTooManyRequests:
		return ErrIndisponible
	case statut >= 400 && statut < 500:
		return ErrRequeteInvalide
	default:
		return ErrIndisponible
	}
}

// tronquer borne un texte venu de la passerelle et aplatit ses retours à la ligne.
func tronquer(s string) string {
	s = strings.TrimSpace(strings.NewReplacer("\r", " ", "\n", " ").Replace(s))
	if len(s) > longueurMaxRaison {
		return s[:longueurMaxRaison] + "..."
	}
	return s
}

// erreurTransport range une panne de réseau ou de délai dépassé.
//
// Elle est RÉJOUABLE, parce que c'est le défaut le moins coûteux, et elle garde la cause
// racine : sans elle, un diagnostic se résume à « la passerelle est indisponible », ce qui
// ne dit ni le délai dépassé, ni la résolution de nom en échec, ni le certificat refusé.
func erreurTransport(operation string, operateur Operateur, cause error) Erreur {
	return Erreur{
		Operation: operation,
		Operateur: operateur,
		motif:     ErrIndisponible,
		cause:     cause,
	}
}
