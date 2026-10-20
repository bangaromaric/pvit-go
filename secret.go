package pvit

import (
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// reponseRenouvellement est ce que rend l'API de rotation en version deux.
//
// La version deux est la seule utilisable : elle rend la clé DANS LA RÉPONSE, là où la
// version un la postait sur une URL de réception à exposer, de façon asynchrone. Un
// secret qui revient par un canal qu'il faut ouvrir au monde n'est pas un progrès.
//
// La durée de vie entre en valeur brute, et c'est une leçon payée ailleurs : un champ de
// durée déclaré entier qui reçoit 3599.995884 fait échouer le décodage de TOUTE la
// structure, et la clé n'est alors jamais lue.
type reponseRenouvellement struct {
	CodeOperation string         `json:"operation_account_code"`
	Secret        string         `json:"secret"`
	ExpireDans    jsontext.Value `json:"expires_in"`
}

// cleValide rend une clé secrète utilisable pour un compte, en renouvelant au besoin.
//
// Le paramètre perimee porte la clé qu'un refus vient de disqualifier, et c'est ce qui
// rend le renouvellement RÉACTIF en plus d'être proactif : sans lui, le contrôle du cache
// rendrait la même clé refusée, et le rejeu échouerait à l'identique.
//
// Le verrou est tenu PENDANT le renouvellement, et le contrôle du cache a lieu sous le
// même verrou. C'est ce qui évite deux choses : autant de renouvellements que de requêtes
// au démarrage à froid, et un second renouvellement inutile quand deux appels essuient un
// refus en même temps, le second trouvant la clé neuve du premier.
func (c *Client) cleValide(ctx context.Context, operation string, operateur Operateur, perimee string) (string, error) {
	vivant, connu := c.comptes[operateur]
	if !connu {
		return "", fmt.Errorf("%w: aucun compte d operation pour %s", ErrConfiguration, operateur)
	}

	vivant.mu.Lock()
	defer vivant.mu.Unlock()

	utilisable := vivant.cle != "" && vivant.cle != perimee
	if utilisable && !c.cleExpiree(vivant) {
		return vivant.cle, nil
	}

	if vivant.reglage.MotDePasse == "" {
		// Sans mot de passe, il n'y a RIEN à renouveler, et arriver ici veut dire que la
		// clé d'amorce ne sert plus : ou elle est absente, ou sa péremption est passée, ou
		// un refus vient de la disqualifier. Rendre quand même la clé périmée ferait
		// essuyer un refus à chaque appel, sans jamais dire pourquoi.
		//
		// Le cas de la clé d'amorce dont la péremption est INCONNUE est déjà traité plus
		// haut : cleExpiree la tient pour valable, et elle est servie tant qu'elle n'a pas
		// été refusée. C'est le compte configuré avec sa seule clé, et il fonctionne.
		return "", Erreur{
			Operation: operation,
			Operateur: operateur,
			Message:   "definir " + NomVariableMotDePasse(PrefixeEnvironnementParDefaut, operateur) + " ou Compte.MotDePasse",
			motif:     ErrRotationImpossible,
		}
	}

	return c.renouveler(ctx, operateur, vivant)
}

// cleExpiree dit si la clé en cache doit être renouvelée avant usage.
//
// Une expiration nulle veut dire « durée de vie inconnue », ce qui est le cas d'une clé
// d'amorce passée à la main : elle est alors tenue pour valable jusqu'au premier refus,
// faute de quoi on renouvellerait à chaque appel une clé parfaitement bonne.
func (c *Client) cleExpiree(vivant *compte) bool {
	if vivant.expire.IsZero() {
		return false
	}
	return !c.maintenant().Before(vivant.expire.Add(-c.opts.margeRotation))
}

// renouveler appelle l'API de rotation. Le verrou du compte doit être tenu.
func (c *Client) renouveler(ctx context.Context, operateur Operateur, vivant *compte) (string, error) {
	valeurs := url.Values{}
	valeurs.Set("operationAccountCode", vivant.reglage.CodeOperation)
	valeurs.Set("password", vivant.reglage.MotDePasse)

	r := requete{
		operation: "renouvellement",
		operateur: operateur,
		endpoint:  epSecret,
		methode:   http.MethodPost,
	}
	// Le corps de formulaire est le défaut, et c'est une précaution concrète : un mot de
	// passe en paramètre d'URL se retrouve dans les journaux d'accès du serveur, dans
	// ceux de chaque proxy traversé, et dans l'historique de tout outil de diagnostic.
	// La collection de requêtes publiée, elle, le met dans l'URL, et les deux formes
	// passent côté serveur : voir [AvecRenouvellementEnQuery].
	if c.opts.renouvellementEnQuery {
		r.query = valeurs
	} else {
		r.corps = []byte(valeurs.Encode())
		r.typeContenu = "application/x-www-form-urlencoded"
	}

	res, err := c.emettre(ctx, r)
	if err != nil {
		return "", err
	}

	var rep reponseRenouvellement
	if err := json.Unmarshal(res.corps, &rep); err != nil {
		return "", Erreur{
			Operation: "renouvellement",
			Operateur: operateur,
			HTTP:      res.statut,
			motif:     ErrReponseIllisible,
			cause:     err,
		}
	}
	secret := strings.TrimSpace(rep.Secret)
	if secret == "" {
		return "", Erreur{
			Operation: "renouvellement",
			Operateur: operateur,
			HTTP:      res.statut,
			Message:   "aucune cle secrete dans la reponse",
			motif:     ErrReponseIllisible,
		}
	}

	c.pousserDansLAnneau(vivant, secret)
	vivant.cle = secret
	vivant.expire = c.maintenant().Add(c.dureeDeVieAnnoncee(rep.ExpireDans))
	return secret, nil
}

// pousserDansLAnneau garde l'ancienne clé pour vérifier une signature en vol.
//
// La documentation de la passerelle impose une rotation horaire et reconnaît qu'une
// rotation « invalide les notifications en vol », tout en publiant des exemples de code qui
// lisent une clé statique dans une variable d'environnement. Ses propres exemples ne
// tiennent pas sa propre contrainte : l'anneau est la réponse à cette incohérence.
func (c *Client) pousserDansLAnneau(vivant *compte, neuve string) {
	if vivant.cle == "" || vivant.cle == neuve {
		return
	}
	vivant.anneau = append([]string{vivant.cle}, vivant.anneau...)
	if max := c.opts.tailleAnneau - 1; len(vivant.anneau) > max {
		vivant.anneau = vivant.anneau[:max]
	}
}

// dureeDeVieAnnoncee lit la durée de vie de la clé, et se replie si elle est douteuse.
//
// Le repli couvre trois cas : la passerelle n'annonce rien, elle annonce une valeur qu'on
// ne sait pas lire, ou elle annonce une durée invraisemblable. Ce dernier cas mérite le
// repli autant que les autres : tenir une clé pour valable un mois parce qu'on a mal lu
// un champ produirait un refus à chaque appel, sans jamais renouveler.
func (c *Client) dureeDeVieAnnoncee(brut jsontext.Value) time.Duration {
	secondes, ok := nombre(brut)
	if !ok || secondes <= 0 {
		return c.opts.dureeDeVie
	}
	duree := time.Duration(secondes * float64(time.Second))
	if duree <= 0 || duree > dureeDeVieMax {
		return c.opts.dureeDeVie
	}
	return duree
}

// Rafraichir renouvelle tout de suite la clé secrète d'un compte.
//
// Elle n'est pas nécessaire : la rotation est paresseuse, et le premier appel qui a besoin
// d'une clé la demande. Elle existe pour qui veut préchauffer au démarrage, ou renouveler
// depuis son propre rythme, puisque ce paquet ne lance aucune goroutine et n'en lancera
// pas : une bibliothèque dont l'appelant ne peut pas arrêter les goroutines lui impose une
// durée de vie qu'il n'a pas choisie.
func (c *Client) Rafraichir(ctx context.Context, operateur Operateur) error {
	vivant, connu := c.comptes[operateur]
	if !connu {
		return fmt.Errorf("%w: aucun compte d operation pour %s", ErrConfiguration, operateur)
	}

	vivant.mu.Lock()
	defer vivant.mu.Unlock()

	if vivant.reglage.MotDePasse == "" {
		return Erreur{
			Operation: "renouvellement",
			Operateur: operateur,
			Message:   "definir " + NomVariableMotDePasse(PrefixeEnvironnementParDefaut, operateur) + " ou Compte.MotDePasse",
			motif:     ErrRotationImpossible,
		}
	}
	_, err := c.renouveler(ctx, operateur, vivant)
	return err
}
