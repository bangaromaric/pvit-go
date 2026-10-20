package pvit

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"strconv"
	"strings"
	"time"
)

// Les fonctions de ce fichier interprètent ce que la passerelle rend, et ont toutes le
// droit de RENONCER plutôt que d'échouer.
//
// La raison est mesurée, et elle vient d'une autre passerelle du même dépôt : un champ
// de durée déclaré entier recevait 3599.995884, et le décodage de la structure ENTIÈRE
// échouait, si bien que le jeton n'était jamais lu. Un identifiant déclaré chaîne
// recevait un entier, et un envoi parti était rapporté en échec. La leçon tient en une
// règle : tout champ dont le type du fournisseur est incertain entre en valeur brute, et
// une fonction d'ici décide si elle sait le lire.
//
// Tolérance sur le TYPE, sévérité sur la VALEUR. Accepter 150 et "150" ne coûte rien ;
// accepter 150,5 pour une devise sans subdivision perdrait un demi-franc à chaque
// transaction, et c'est lireMontant qui refuse.

// nombre interprète une valeur brute en nombre, qu'elle arrive nue ou entre guillemets.
func nombre(brut jsontext.Value) (float64, bool) {
	s := strings.TrimSpace(string(brut))
	if s == "" || s == "null" {
		return 0, false
	}
	if s[0] == '"' {
		var dedans string
		if err := json.Unmarshal(brut, &dedans); err != nil {
			return 0, false
		}
		s = strings.TrimSpace(dedans)
		if s == "" {
			return 0, false
		}
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// texte interprète une valeur brute en chaîne, qu'elle arrive en chaîne ou en nombre.
func texte(brut jsontext.Value) (string, bool) {
	s := strings.TrimSpace(string(brut))
	if s == "" || s == "null" {
		return "", false
	}
	if s[0] == '"' {
		var dedans string
		if err := json.Unmarshal(brut, &dedans); err != nil {
			return "", false
		}
		return dedans, true
	}
	if s[0] == '{' || s[0] == '[' {
		return "", false
	}
	return s, true
}

// codeApplicatif lit le code de retour applicatif de la passerelle.
//
// Il s'écrit de quatre façons selon l'endroit : la chaîne "200" dans une réponse de
// transaction, l'entier 200 dans une réponse de lien, l'entier 200 dans un callback sous
// un autre nom, et l'entier 8000 dans un corps d'erreur. C'est le DISCRIMINANT de toute
// la passerelle, puisqu'un même code HTTP 403 couvre vingt-cinq cas distincts : une
// seule fonction le lit, et elle est éprouvée contre les quatre écritures.
func codeApplicatif(brut jsontext.Value) (int, bool) {
	v, ok := nombre(brut)
	if !ok {
		return 0, false
	}
	n := int(v)
	if float64(n) != v {
		return 0, false
	}
	return n, true
}

// formatDatePVit est l'horodatage des corps d'erreur, ddMMyyyy-HHmmss.
const formatDatePVit = "02012006-150405"

// datePVit lit l'horodatage d'un corps d'erreur, et renonce sans bruit s'il est illisible.
//
// Renoncer est le bon comportement : une date mal formée ne doit pas faire échouer la
// lecture du code d'erreur, qui est la seule information dont l'appelant a besoin pour
// décider.
func datePVit(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(formatDatePVit, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// presente dit si un champ était dans la charge utile, avec une valeur autre que nulle.
//
// Cette distinction n'est pas cosmétique, elle décide d'une conduite. Un champ ABSENT est
// une réponse qui ne porte pas cette information, ce qui est fréquent et légitime. Un champ
// PRÉSENT mais illisible est un désaccord de contrat, et sur une somme d'argent il doit
// faire échouer la lecture : rendre zéro en silence ferait créditer zéro franc sur une
// transaction réussie.
//
// La valeur nulle compte comme absente : c'est la passerelle qui dit « pas de valeur », et
// non une valeur qu'on ne sait pas lire.
func presente(brut jsontext.Value) bool {
	s := strings.TrimSpace(string(brut))
	return s != "" && s != "null"
}
