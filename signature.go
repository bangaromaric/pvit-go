package pvit

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Les en-têtes du webhook de reporting, tels que la documentation les nomme.
//
// Ce webhook est l'exact contraire du callback de transaction : il est signé, horodaté,
// muni d'un identifiant de remise constant sur les rejeux et d'une politique de reprise
// publiée. Toutes les garanties sont posées sur celui qui ne transporte qu'un avis de
// fichier disponible, et AUCUNE sur celui qui porte l'argent.
const (
	// EnteteSignature porte la signature, sous la forme v1 suivie du condensat en
	// hexadécimal.
	EnteteSignature = "X-PVit-Signature"

	// EnteteRemise porte l'identifiant de remise, constant sur les rejeux. C'est la clé
	// d'idempotence que le callback de transaction, lui, n'a pas.
	EnteteRemise = "X-PVit-Delivery-Id"

	// EnteteEvenement porte le nom de l'événement.
	EnteteEvenement = "X-PVit-Event"
)

// prefixeSignature est la version de l'algorithme, en tête de l'en-tête de signature.
const prefixeSignature = "v1="

// FenetreSignature est la tolérance d'horloge par défaut d'une notification de reporting.
//
// Cinq minutes, ce que la documentation de la passerelle publie. Elle s'applique DANS LES
// DEUX SENS : n'accepter que le passé laisserait passer une notification forgée avec une
// horloge en avance. Voir [AvecFenetreSignature] pour la régler.
const FenetreSignature = 300 * time.Second

// Les refus de vérification d'une signature.
var (
	// ErrSignatureInvalide signale un condensat qui ne correspond à aucune clé de
	// l'anneau. La notification doit être REFUSÉE, et non traitée avec méfiance.
	ErrSignatureInvalide = errors.New("signature du webhook invalide")

	// ErrHorodatageHorsFenetre signale un horodatage trop ancien ou trop avancé. C'est la
	// garde contre le rejeu d'une notification authentique capturée plus tôt.
	ErrHorodatageHorsFenetre = errors.New("horodatage du webhook hors de la fenetre de tolerance")

	// ErrAnneauVide signale qu'aucune clé n'est disponible pour vérifier. Ce n'est pas un
	// refus de signature : c'est une configuration incomplète, ou un compte dont la clé
	// n'a jamais été obtenue.
	ErrAnneauVide = fmt.Errorf("%w: aucune cle pour verifier une signature", ErrConfiguration)
)

// Anneau porte les clés successives d'un compte, la plus récente d'abord.
//
// Il existe pour une incohérence de la documentation de la passerelle : elle impose une
// rotation horaire, reconnaît qu'une rotation « invalide les notifications en vol », rejoue
// ses notifications jusqu'à six heures, et publie pourtant des exemples de code qui lisent
// une clé STATIQUE dans une variable d'environnement. Ses propres exemples ne tiennent pas
// sa propre contrainte.
//
// La valeur zéro est un anneau vide, et [Anneau.Verifier] le dit par [ErrAnneauVide] plutôt
// que de refuser la signature : confondre « je ne peux pas vérifier » et « c'est faux »
// enverrait chercher un attaquant là où il manque un réglage.
type Anneau struct {
	cles []string
}

// NouvelAnneau compose un anneau, la clé la plus récente en premier.
//
// Les clés vides sont écartées, et les doublons aussi : vérifier deux fois contre la même
// clé ne change rien et coûte un calcul de condensat.
func NouvelAnneau(cles ...string) Anneau {
	var gardees []string
	vues := make(map[string]bool, len(cles))
	for _, cle := range cles {
		cle = strings.TrimSpace(cle)
		if cle == "" || vues[cle] {
			continue
		}
		vues[cle] = true
		gardees = append(gardees, cle)
	}
	return Anneau{cles: gardees}
}

// Cles rend une copie des clés de l'anneau, la plus récente d'abord.
//
// Une copie, parce qu'une tranche rendue telle quelle laisserait un appelant réécrire les
// clés avec lesquelles les signatures sont vérifiées.
func (a Anneau) Cles() []string { return append([]string(nil), a.cles...) }

// Vide dit si l'anneau ne porte aucune clé.
func (a Anneau) Vide() bool { return len(a.cles) == 0 }

// Avec rend un anneau augmenté d'une clé, placée en tête.
func (a Anneau) Avec(cle string) Anneau {
	return NouvelAnneau(append([]string{cle}, a.cles...)...)
}

// Verifier contrôle la signature d'une notification de reporting.
//
// Le condensat est un HMAC-SHA256 en hexadécimal minuscule, calculé sur l'horodatage, un
// point, puis le CORPS BRUT. Brut est le mot important : désérialiser puis re-sérialiser
// changerait l'ordre des clés et l'espacement, et toute signature deviendrait fausse. Le
// corps doit donc arriver en octets, exactement comme il a été reçu.
//
// La fenêtre s'applique DANS LES DEUX SENS : un horodatage trop avancé est aussi suspect
// qu'un horodatage trop ancien, et n'accepter que le passé laisserait passer une
// notification forgée avec une horloge en avance.
//
// La comparaison est en temps constant, et chaque clé de l'anneau est essayée dans l'ordre,
// de la plus récente à la plus ancienne.
func (a Anneau) Verifier(horodatage string, corps []byte, signature string, maintenant time.Time, fenetre time.Duration) error {
	if a.Vide() {
		return ErrAnneauVide
	}

	emis, err := lireHorodatage(horodatage)
	if err != nil {
		return err
	}
	if ecart := maintenant.Sub(emis); ecart > fenetre || ecart < -fenetre {
		return fmt.Errorf("%w: ecart de %v, tolerance %v", ErrHorodatageHorsFenetre, ecart, fenetre)
	}

	attendu, err := condensatAttendu(signature)
	if err != nil {
		return err
	}

	signe := []byte(horodatage + "." + string(corps))
	for _, cle := range a.cles {
		mac := hmac.New(sha256.New, []byte(cle))
		mac.Write(signe)
		if hmac.Equal(mac.Sum(nil), attendu) {
			return nil
		}
	}
	return ErrSignatureInvalide
}

// condensatAttendu lit le condensat porté par l'en-tête de signature.
func condensatAttendu(signature string) ([]byte, error) {
	signature = strings.TrimSpace(signature)
	if signature == "" {
		return nil, fmt.Errorf("%w: en-tete de signature absent", ErrSignatureInvalide)
	}
	// Le préfixe de version est accepté présent ou absent : un intermédiaire peut l'avoir
	// retiré, et le condensat nu reste vérifiable.
	hexa := strings.TrimPrefix(signature, prefixeSignature)
	brut, err := hex.DecodeString(hexa)
	if err != nil || len(brut) != sha256.Size {
		return nil, fmt.Errorf("%w: condensat illisible", ErrSignatureInvalide)
	}
	return brut, nil
}

// lireHorodatage interprète l'horodatage d'une notification.
//
// Deux formes sont acceptées, les secondes depuis l'époque et la forme RFC 3339, parce que
// la documentation nomme ce champ sans en publier le format. Accepter les deux coûte six
// lignes, là où en deviner une seule ferait refuser toutes les notifications.
func lireHorodatage(brut string) (time.Time, error) {
	brut = strings.TrimSpace(brut)
	if brut == "" {
		return time.Time{}, fmt.Errorf("%w: horodatage absent", ErrHorodatageHorsFenetre)
	}
	if secondes, err := strconv.ParseInt(brut, 10, 64); err == nil {
		return time.Unix(secondes, 0), nil
	}
	if instant, err := time.Parse(time.RFC3339, brut); err == nil {
		return instant, nil
	}
	return time.Time{}, fmt.Errorf("%w: horodatage %q illisible", ErrHorodatageHorsFenetre, brut)
}

// Anneau rend les clés vivantes d'un compte, la plus récente d'abord.
//
// Elle sert à vérifier une signature soi-même. Pour le cas courant, voir
// [Client.VerifierSignatureRapport], qui applique en plus l'horloge et la fenêtre du client.
func (c *Client) Anneau(operateur Operateur) Anneau {
	vivant, connu := c.comptes[operateur]
	if !connu {
		return Anneau{}
	}
	vivant.mu.Lock()
	defer vivant.mu.Unlock()
	return NouvelAnneau(append([]string{vivant.cle}, vivant.anneau...)...)
}

// VerifierSignatureRapport contrôle la signature d'une notification de reporting.
//
// Elle applique l'anneau du compte, l'horloge et la fenêtre du client, et c'est le chemin
// à préférer : il n'y a rien à tenir à jour côté appelant.
//
// Le corps doit arriver en OCTETS BRUTS, tels que reçus. Avec un routeur qui désérialise
// pour vous, il faut lire le corps avant lui.
//
// # Une réserve, et elle est assumée
//
// Le nom de l'en-tête qui porte l'horodatage n'est pas publié dans la documentation lue
// pour écrire ce paquet. Il n'est donc PAS inventé ici : l'horodatage est un paramètre, et
// c'est à l'appelant de le lire dans l'en-tête que la passerelle emploie réellement. Les
// trois en-têtes dont le nom est connu sont [EnteteSignature], [EnteteRemise] et
// [EnteteEvenement].
func (c *Client) VerifierSignatureRapport(operateur Operateur, horodatage string, corps []byte, signature string) error {
	return c.Anneau(operateur).Verifier(horodatage, corps, signature, c.maintenant(), c.opts.fenetreSignature)
}
