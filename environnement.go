package pvit

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"
)

// PrefixeEnvironnementParDefaut est le préfixe des variables que [LireEnvironnement] lit
// quand on ne lui en donne pas d'autre.
const PrefixeEnvironnementParDefaut = "PVIT_"

// Les trois suffixes qui décrivent un compte d'opération.
const (
	suffixeCompteCode       = "_CODE"
	suffixeCompteSecret     = "_SECRET"
	suffixeCompteMotDePasse = "_PASSWORD"
	suffixeCompteExpiration = "_SECRET_EXPIRES_AT"
	segmentCompte           = "ACCOUNT_"
)

// NomVariableCompte rend le nom de la variable qui porte le code d'un compte d'opération.
func NomVariableCompte(prefixe string, operateur Operateur) string {
	return prefixeOuDefaut(prefixe) + segmentCompte + string(operateur) + suffixeCompteCode
}

// NomVariableSecret rend le nom de la variable qui porte la clé d'amorce d'un compte.
func NomVariableSecret(prefixe string, operateur Operateur) string {
	return prefixeOuDefaut(prefixe) + segmentCompte + string(operateur) + suffixeCompteSecret
}

// NomVariableMotDePasse rend le nom de la variable qui porte le mot de passe de rotation.
//
// Ce paquet ne lit pas l'environnement de lui-même, mais il NOMME cette variable dans le
// message de [ErrRotationImpossible] : une erreur qui dit quel geste la corrige vaut mieux
// qu'une erreur exacte et muette, surtout lue à six heures du matin.
func NomVariableMotDePasse(prefixe string, operateur Operateur) string {
	return prefixeOuDefaut(prefixe) + segmentCompte + string(operateur) + suffixeCompteMotDePasse
}

// prefixeOuDefaut rend le préfixe donné, ou celui du paquet s'il est vide.
func prefixeOuDefaut(prefixe string) string {
	if prefixe == "" {
		return PrefixeEnvironnementParDefaut
	}
	return prefixe
}

// codesLus associe le suffixe de variable au champ de [CodesURL] qu'il renseigne.
//
// Les noms sont en anglais, comme toute variable d'environnement de ce dépôt, et ils
// reprennent le vocabulaire de la passerelle plutôt que celui de ce paquet : c'est celui
// que l'espace marchand affiche à côté de chaque code à recopier.
var codesLus = []struct {
	suffixe string
	champ   func(*CodesURL) *string
}{
	{"URL_CODE_SECRET", func(c *CodesURL) *string { return &c.Secret }},
	{"URL_CODE_PAYMENT", func(c *CodesURL) *string { return &c.Paiement }},
	{"URL_CODE_STATUS", func(c *CodesURL) *string { return &c.Statut }},
	{"URL_CODE_FEES", func(c *CodesURL) *string { return &c.Frais }},
	{"URL_CODE_BALANCE", func(c *CodesURL) *string { return &c.Solde }},
	{"URL_CODE_KYC", func(c *CodesURL) *string { return &c.KYC }},
	{"URL_CODE_COUNTRIES", func(c *CodesURL) *string { return &c.Pays }},
	{"URL_CODE_OPERATORS", func(c *CodesURL) *string { return &c.Operateurs }},
	{"URL_CODE_LINK", func(c *CodesURL) *string { return &c.Lien }},
	{"URL_CODE_QRCODE", func(c *CodesURL) *string { return &c.QRCode }},
	{"URL_CODE_REPORT", func(c *CodesURL) *string { return &c.Rapport }},
}

// LireEnvironnement compose des réglages depuis l'environnement, sous le préfixe donné.
//
// Elle est OFFERTE ET NON IMPOSÉE. Une bibliothèque réutilisable ne doit pas décider des
// noms de variables du programme qui l'emploie, et [Nouveau] reste le seul point d'entrée :
// un projet qui lit sa configuration autrement remplit [Reglages] à la main et n'appelle
// jamais cette fonction. Elle existe parce que c'est la glu que tout le monde recopie.
//
// Le préfixe est un paramètre pour la même raison. Vide, il vaut
// [PrefixeEnvironnementParDefaut].
//
// # Les variables lues
//
//	PVIT_BASE_URL                              defaut https://api.mypvit.pro
//	PVIT_CALLBACK_URL_CODE
//	PVIT_URL_CODE_SECRET                       les onze codes, un par API
//	PVIT_URL_CODE_PAYMENT, _STATUS, _FEES, _BALANCE, _KYC, _COUNTRIES, _OPERATORS
//	PVIT_URL_CODE_LINK, _QRCODE, _REPORT       pour la trappe de sortie
//	PVIT_ACCOUNT_<OPERATEUR>_CODE              le triplet d'un compte
//	PVIT_ACCOUNT_<OPERATEUR>_SECRET
//	PVIT_ACCOUNT_<OPERATEUR>_PASSWORD
//	PVIT_ACCOUNT_<OPERATEUR>_SECRET_EXPIRES_AT facultatif, en RFC 3339
//
// # La règle de découverte
//
// Les comptes ne sont pas une liste fermée : la fonction parcourt l'environnement et
// reconnaît tout PVIT_ACCOUNT_<OPERATEUR>_CODE. Ajouter une famille d'opérateur est donc
// trois variables et ZÉRO ligne de Go, ce qui est le seul moyen de tenir la promesse que la
// configuration est une table et non trois constantes.
//
// # Ce qu'elle ne vérifie pas
//
// Elle ne juge pas la COMPLÉTUDE des réglages : c'est [Nouveau] qui le fait, en un seul
// endroit, et dédoubler cette validation ferait diverger les deux. Elle ne rapporte que ce
// qu'elle seule peut voir : un nom d'opérateur mal formé, une date illisible.
func LireEnvironnement(prefixe string) (Reglages, error) {
	prefixe = prefixeOuDefaut(prefixe)

	r := Reglages{
		BaseURL:      os.Getenv(prefixe + "BASE_URL"),
		CodeCallback: os.Getenv(prefixe + "CALLBACK_URL_CODE"),
		Comptes:      make(map[Operateur]Compte),
	}
	for _, lu := range codesLus {
		*lu.champ(&r.Codes) = strings.TrimSpace(os.Getenv(prefixe + lu.suffixe))
	}

	var manques []error
	for _, operateur := range operateursDeLEnvironnement(prefixe) {
		compte := Compte{
			CodeOperation: strings.TrimSpace(os.Getenv(NomVariableCompte(prefixe, operateur))),
			CleAmorce:     strings.TrimSpace(os.Getenv(NomVariableSecret(prefixe, operateur))),
			MotDePasse:    strings.TrimSpace(os.Getenv(NomVariableMotDePasse(prefixe, operateur))),
		}
		nomExpiration := prefixe + segmentCompte + string(operateur) + suffixeCompteExpiration
		if brut := strings.TrimSpace(os.Getenv(nomExpiration)); brut != "" {
			instant, err := time.Parse(time.RFC3339, brut)
			if err != nil {
				manques = append(manques, fmt.Errorf("%s: attendu un instant RFC 3339, recu %q", nomExpiration, brut))
			} else {
				compte.ExpirationAmorce = instant
			}
		}
		r.Comptes[operateur] = compte
	}

	slices.SortFunc(manques, func(a, b error) int { return strings.Compare(a.Error(), b.Error()) })
	if err := errors.Join(manques...); err != nil {
		return Reglages{}, fmt.Errorf("%w: %w", ErrConfiguration, err)
	}
	return r, nil
}

// operateursDeLEnvironnement relève les opérateurs qu'une variable de code déclare.
//
// L'ordre est trié, pour qu'un message d'erreur de [Nouveau] ne change pas d'une exécution
// à l'autre : l'itération de l'environnement, comme celle d'une carte, n'offre aucun ordre.
func operateursDeLEnvironnement(prefixe string) []Operateur {
	debut := prefixe + segmentCompte
	var operateurs []Operateur
	for _, entree := range os.Environ() {
		nom, _, trouve := strings.Cut(entree, "=")
		if !trouve {
			continue
		}
		reste, ok := strings.CutPrefix(nom, debut)
		if !ok {
			continue
		}
		brut, ok := strings.CutSuffix(reste, suffixeCompteCode)
		if !ok || brut == "" {
			continue
		}
		// Le segment doit être DÉJÀ en capitales, et un segment en minuscules est ignoré
		// plutôt que normalisé. Le normaliser serait un piège : les noms des deux autres
		// variables du compte se composent à partir de l'opérateur, et un PVIT_ACCOUNT_
		// airtel_money_CODE deviendrait AIRTEL_MONEY, dont le code serait alors cherché
		// sous un nom qui n'existe pas. Le compte semblerait découvert et resterait vide.
		if brut != strings.ToUpper(brut) {
			continue
		}
		operateur, err := NouvelOperateur(brut)
		if err != nil {
			continue
		}
		operateurs = append(operateurs, operateur)
	}
	slices.Sort(operateurs)
	return slices.Compact(operateurs)
}

// NouveauDepuisEnvironnement lit l'environnement puis câble le client.
//
// C'est le raccourci des deux appels, et la validation reste celle de [Nouveau] : un seul
// endroit juge la complétude, et les manques y sont rassemblés et triés.
func NouveauDepuisEnvironnement(prefixe string, options ...Option) (*Client, error) {
	r, err := LireEnvironnement(prefixe)
	if err != nil {
		return nil, err
	}
	return Nouveau(r, options...)
}
