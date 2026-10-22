// Package gabon porte la correspondance entre un numéro gabonais et son opérateur de
// mobile money.
//
// C'est une donnée du régulateur et non de la passerelle, et elle change par décision
// administrative, à un rythme sans rapport avec les versions d'une API. Elle vit donc dans
// son paquet, et un projet qui sert un autre pays n'emporte pas la table gabonaise.
//
// # D'où viennent les cinq entrées livrées, et pourquoi il n'y en a que cinq
//
// CHACUNE EST VÉRIFIÉE, AUCUNE N'EST DEVINÉE. Les cinq ont été confirmées le 22 octobre
// 2026 par un appel kyc sur un numéro réel de chaque préfixe : la passerelle a rendu le
// titulaire du compte, ce qui établit l'appariement. Le préfixe 74 était en outre déjà
// publié par la passerelle, dans sa collection de requêtes d'exemple.
//
//	74, 76, 77 -> AIRTEL_MONEY
//	62, 66     -> MOOV_MONEY
//
// Le reste du plan de numérotation attend une source, et deviner coûterait cher : un
// mauvais appariement entre le compte d'opération et l'opérateur est sanctionné par le
// code 3017, au comptoir, devant un client qui attend. Un préfixe inconnu rend donc faux,
// jamais un opérateur au hasard.
//
// L'autorité sur le vocabulaire des opérateurs est l'appel get-operators de la passerelle,
// qui ne dit RIEN des préfixes ; c'est kyc qui transforme une présomption de préfixe en
// entrée de table. Un préfixe relevé ailleurs se pose en une ligne :
//
//	table := gabon.TableConnue()
//	table["65"] = pvit.OperateurMoov // part significative, et non "065"
//	operateur, connu := table.Operateur(numero)
package gabon

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	pvit "github.com/bangaromaric/pvit-go"
)

// indicatifGabon est l'indicatif pays, sans le plus.
const indicatifGabon = "241"

// longueurSignificative est le nombre de chiffres d'un numéro gabonais, indicatif et zéro
// d'acheminement retirés. Le plan est passé à huit chiffres, l'ancien est clos depuis le
// 6 avril 2024.
const longueurSignificative = 8

// Table associe un préfixe de numéro, deux ou trois chiffres, à un opérateur.
//
// LES CLÉS PORTENT SUR LA PART SIGNIFICATIVE, zéro d'acheminement retiré. Le numéro
// 074111111 a pour part significative 74111111, et se reconnaît donc par la clé "74" ou
// par la clé "741", jamais par "074". C'est le piège de ce type, parce qu'un tableau de
// régulateur écrit volontiers la forme composée : [NouvelleTable] refuse une clé qui
// commence par zéro, plutôt que de la laisser ne jamais correspondre en silence.
//
// La recherche essaie d'abord trois chiffres, puis deux : une table peut mêler les deux
// granularités, et l'entrée la plus précise gagne.
type Table map[string]pvit.Operateur

// NouvelleTable valide une table de préfixes avant de s'en servir.
//
// Elle existe pour une raison mesurable : une clé mal formée ne produit aucune erreur à
// l'usage, seulement un opérateur jamais reconnu, et le symptôme n'apparaît qu'au
// comptoir. Mieux vaut refuser de démarrer que de ne jamais reconnaître un numéro Moov.
func NouvelleTable(prefixes map[string]pvit.Operateur) (Table, error) {
	var manques []error
	table := make(Table, len(prefixes))
	for prefixe, operateur := range prefixes {
		switch {
		case len(prefixe) < 2 || len(prefixe) > 3:
			manques = append(manques, fmt.Errorf("prefixe %q: attendu deux ou trois chiffres", prefixe))
			continue
		case !chiffresSeulement(prefixe):
			manques = append(manques, fmt.Errorf("prefixe %q: attendu des chiffres seulement", prefixe))
			continue
		case prefixe[0] == '0':
			manques = append(manques, fmt.Errorf("prefixe %q: le zero d acheminement ne fait pas partie de la part significative, attendu %q", prefixe, prefixe[1:]))
			continue
		case operateur == "":
			manques = append(manques, fmt.Errorf("prefixe %q: operateur vide", prefixe))
			continue
		}
		table[prefixe] = operateur
	}
	slices.SortFunc(manques, func(a, b error) int { return strings.Compare(a.Error(), b.Error()) })
	if err := errors.Join(manques...); err != nil {
		return nil, err
	}
	return table, nil
}

// tableConnue ne porte que ce qui est vérifié, préfixe par préfixe, par un appel kyc sur un
// numéro réel. Voir le commentaire du paquet pour la date et la méthode.
var tableConnue = Table{
	"74": pvit.OperateurAirtel,
	"76": pvit.OperateurAirtel,
	"77": pvit.OperateurAirtel,
	"62": pvit.OperateurMoov,
	"66": pvit.OperateurMoov,
}

// TableConnue rend une copie de la correspondance vérifiée, à compléter par l'appelant.
//
// Une copie, et non la table elle même : une carte exportée serait modifiable par
// n'importe quel appelant, et deux projets du même binaire se marcheraient dessus.
func TableConnue() Table {
	copie := make(Table, len(tableConnue))
	maps.Copy(copie, tableConnue)
	return copie
}

// Operateur déduit l'opérateur d'un numéro, et dit si le préfixe est connu.
//
// Elle accepte les quatre écritures qu'un commerçant ou une base de données produisent :
// 074111111, +241074111111, 24174111111 et 241 74 11 11 11, espaces, points et tirets
// compris.
func (t Table) Operateur(msisdn string) (pvit.Operateur, bool) {
	significatifs, ok := chiffresSignificatifs(msisdn)
	if !ok {
		return "", false
	}
	if operateur, ok := t[significatifs[:3]]; ok {
		return operateur, true
	}
	if operateur, ok := t[significatifs[:2]]; ok {
		return operateur, true
	}
	return "", false
}

// OperateurDuNumero déduit l'opérateur par la table vérifiée livrée avec ce paquet.
//
// Elle rend faux pour tout préfixe que cette table ne porte pas : voir le commentaire du
// paquet pour ce qui y est, et [TableConnue] pour compléter.
func OperateurDuNumero(msisdn string) (pvit.Operateur, bool) {
	return tableConnue.Operateur(msisdn)
}

// chiffresSignificatifs réduit un numéro écrit de n'importe quelle façon à ses huit
// chiffres significatifs.
//
// Les deux retraits sont CONDITIONNELS, et c'est ce qui les rend sûrs. L'indicatif ne
// tombe que s'il laisse derrière lui un numéro de longueur plausible, et le zéro
// d'acheminement que s'il laisse exactement huit chiffres. Sans cette condition, un numéro
// local dont la part significative commencerait par 241 serait amputé.
func chiffresSignificatifs(msisdn string) (string, bool) {
	var chiffres []byte
	for i := 0; i < len(msisdn); i++ {
		if c := msisdn[i]; c >= '0' && c <= '9' {
			chiffres = append(chiffres, c)
		}
	}
	s := string(chiffres)

	if reste, ok := strings.CutPrefix(s, indicatifGabon); ok {
		if len(reste) == longueurSignificative || len(reste) == longueurSignificative+1 {
			s = reste
		}
	}
	if reste, ok := strings.CutPrefix(s, "0"); ok && len(reste) == longueurSignificative {
		s = reste
	}
	if len(s) != longueurSignificative {
		return "", false
	}
	return s, true
}

// chiffresSeulement dit si la chaîne ne porte que des chiffres décimaux, et n'est pas vide.
func chiffresSeulement(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
