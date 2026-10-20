package pvit

import (
	"encoding/json/jsontext"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// DeviseXAF est le franc CFA d'Afrique centrale, la seule devise que la passerelle sert.
//
// Elle n'a PAS de subdivision : il n'existe pas de centime de franc CFA. C'est ce fait,
// et non une préférence, qui commande les deux types ci-dessous.
const DeviseXAF = "XAF"

// millimesParXAF est l'échelle interne de [Frais].
const millimesParXAF = 1000

// Montant est une somme transactable, en francs CFA entiers.
//
// Un entier et non un flottant, parce que la devise n'a pas de subdivision et qu'un
// flottant finirait par rendre 149,99999 pour 150. La sérialisation sort un entier nu,
// ce qu'un schéma « number » accepte sans réserve : la conversion est donc gratuite.
//
// La valeur zéro vaut zéro franc, ce qui est une somme lisible mais pas un mouvement
// valide : [NouveauMontant] la refuse, tandis que la lecture d'une réponse l'accepte,
// puisqu'un montant crédité nul existe.
type Montant int64

// NouveauMontant valide une somme que l'on s'apprête à faire circuler.
//
// Le zéro et le négatif sont refusés : la passerelle n'encaisse ni l'un ni l'autre, et
// un remboursement se demande par [TypeOperation] et non par un montant négatif.
func NouveauMontant(xaf int64) (Montant, error) {
	if xaf <= 0 {
		return 0, MontantInvalideError{
			Champ: "amount",
			Brut:  strconv.FormatInt(xaf, 10),
			motif: ErrMontantInvalide,
		}
	}
	return Montant(xaf), nil
}

// String rend la somme avec le code ISO de la devise, pour ce qui reste entre machines.
func (m Montant) String() string { return grouper(int64(m)) + " " + DeviseXAF }

// EnFCFA rend la somme avec le nom d'usage de la devise, pour ce qui est lu par une personne.
//
// XAF est ce que la norme écrit, FCFA est ce que l'on dit au Gabon. Les deux formes sont
// justes, elles ne s'adressent pas aux mêmes lecteurs.
func (m Montant) EnFCFA() string { return grouper(int64(m)) + " FCFA" }

// Frais est une somme CALCULÉE par la passerelle, au millième de franc près.
//
// Un type distinct de [Montant], et c'est la contradiction la plus coûteuse de la
// documentation de la passerelle qui l'impose. Elle prescrit de rejeter tout flottant non
// entier, puis publie elle-même des frais de 3,5 et de 1,5 francs. Appliquer sa règle à
// la lettre ferait échouer la lecture du calculateur de frais et du callback sur une
// réponse parfaitement valide.
//
// La résolution tient en une phrase : la sévérité porte sur les sommes TRANSACTABLES, que
// l'on envoie, pas sur les sommes CALCULÉES, que l'on reçoit. On ne les arrondit pas non
// plus, on rapporte ce qui a été reçu, et [Frais.ArrondiSup] est là pour qui doit
// provisionner.
type Frais int64

// Millimes rend la somme en millièmes de franc, l'unité de stockage interne.
func (f Frais) Millimes() int64 { return int64(f) }

// EstEntiere dit si les frais tombent sur un franc juste.
func (f Frais) EstEntiere() bool { return int64(f)%millimesParXAF == 0 }

// ArrondiSup rend le premier franc entier qui couvre ces frais.
func (f Frais) ArrondiSup() Montant {
	n := int64(f)
	if n <= 0 {
		return Montant(n / millimesParXAF)
	}
	return Montant((n + millimesParXAF - 1) / millimesParXAF)
}

// String rend la somme en séparant les milliers, avec la virgule décimale française.
func (f Frais) String() string {
	n := int64(f)
	signe := ""
	if n < 0 {
		signe = "-"
		n = -n
	}
	entier := grouper(n / millimesParXAF)
	frac := strings.TrimRight(fmt.Sprintf("%03d", n%millimesParXAF), "0")
	if frac == "" {
		return signe + entier + " " + DeviseXAF
	}
	return signe + entier + "," + frac + " " + DeviseXAF
}

// Decimal rend la somme en notation décimale pointée, sans unité ni séparateur de milliers.
//
// C'est la forme que la passerelle emploie sur le fil, et celle que [Frais.MarshalJSON]
// écrit. [Frais.String] est pour un lecteur humain, celle-ci est pour une machine.
func (f Frais) Decimal() string {
	n := int64(f)
	signe := ""
	if n < 0 {
		signe = "-"
		n = -n
	}
	entier := strconv.FormatInt(n/millimesParXAF, 10)
	frac := strings.TrimRight(fmt.Sprintf("%03d", n%millimesParXAF), "0")
	if frac == "" {
		return signe + entier
	}
	return signe + entier + "." + frac
}

// MarshalJSON écrit la somme en nombre décimal, et NON en millièmes.
//
// Sans cette méthode, un appelant qui re-sérialise un [Callback] vers sa propre API
// publierait 3500 là où la passerelle a dit 3,5. L'unité de stockage interne ne doit pas
// fuir dans un contrat de sortie.
func (f Frais) MarshalJSON() ([]byte, error) {
	return []byte(f.Decimal()), nil
}

// UnmarshalJSON lit un nombre décimal, en acceptant la chaîne comme le nombre.
//
// Récepteur pointeur, là où tout le reste du type est en récepteur valeur : c'est
// l'exception que les codecs imposent, et que la bibliothèque standard fait elle-même. Elle
// est écrite ici pour que le type se relise après s'être écrit, sans quoi la sérialisation
// serait asymétrique.
func (f *Frais) UnmarshalJSON(brut []byte) error {
	lu, err := lireFrais("frais", jsontext.Value(brut))
	if err != nil {
		return err
	}
	*f = lu
	return nil
}

// MontantInvalideError signale une somme refusée, à l'aller comme au retour.
//
// Elle emballe soit [ErrMontantInvalide] pour une somme qu'on refuse d'envoyer, soit
// [ErrMontantFractionnaire] pour une somme reçue qui n'est pas un franc entier là où elle
// devrait l'être, soit [ErrReponseIllisible] pour une valeur qu'on ne sait pas lire du
// tout. Le motif est privé : seul ce paquet le pose, et l'appelant l'interroge par
// errors.Is.
type MontantInvalideError struct {
	// Champ est le nom du champ tel que la passerelle l'écrit.
	Champ string

	// Brut est la valeur telle qu'elle a été reçue, ou passée.
	Brut string

	motif error
}

// Error nomme le champ et la valeur, les deux seules choses utiles au diagnostic.
func (e MontantInvalideError) Error() string {
	return fmt.Sprintf("champ %s: montant %s: %v", e.Champ, e.Brut, e.motif)
}

// Unwrap rattache la somme refusée à son motif interrogeable.
func (e MontantInvalideError) Unwrap() error { return e.motif }

// montantSiPresent lit un montant quand le champ est là, et refuse une valeur illisible.
//
// C'est la forme à employer sur toute réponse de la passerelle : un champ absent laisse la
// valeur zéro sans erreur, un champ illisible en rend une. Écarter l'erreur ferait créditer
// zéro franc sur une transaction que la passerelle annonce réussie.
func montantSiPresent(champ string, brut jsontext.Value) (Montant, error) {
	if !presente(brut) {
		return 0, nil
	}
	return lireMontant(champ, brut)
}

// fraisSiPresent lit des frais quand le champ est là, et refuse une valeur illisible.
func fraisSiPresent(champ string, brut jsontext.Value) (Frais, error) {
	if !presente(brut) {
		return 0, nil
	}
	return lireFrais(champ, brut)
}

// lireMontant lit une somme transactable, et refuse ce qui n'est pas un franc entier.
//
// Tolérance sur le type : 150, 150.0 et "150" passent tous les trois. Sévérité sur la
// valeur : 150,5 est refusé, parce qu'un demi-franc n'existe pas et que l'accepter
// reviendrait à en perdre la moitié sans le dire.
func lireMontant(champ string, brut jsontext.Value) (Montant, error) {
	s, ok := texte(brut)
	if !ok {
		return 0, MontantInvalideError{Champ: champ, Brut: apercu(brut), motif: ErrReponseIllisible}
	}
	m, ok := millimes(s)
	if !ok {
		return 0, MontantInvalideError{Champ: champ, Brut: borner(s), motif: ErrReponseIllisible}
	}
	if m%millimesParXAF != 0 {
		return 0, MontantInvalideError{Champ: champ, Brut: borner(s), motif: ErrMontantFractionnaire}
	}
	return Montant(m / millimesParXAF), nil
}

// lireFrais lit une somme calculée, et accepte le millième.
func lireFrais(champ string, brut jsontext.Value) (Frais, error) {
	s, ok := texte(brut)
	if !ok {
		return 0, MontantInvalideError{Champ: champ, Brut: apercu(brut), motif: ErrReponseIllisible}
	}
	m, ok := millimes(s)
	if !ok {
		return 0, MontantInvalideError{Champ: champ, Brut: borner(s), motif: ErrReponseIllisible}
	}
	return Frais(m), nil
}

// millimes lit un nombre décimal en millièmes de franc, de façon EXACTE.
//
// Le chemin nominal ne passe pas par le flottant : 0,1 fois mille rend 100,00000000000001
// en binaire, et arrondir à l'aveugle masquerait une valeur à quatre décimales que l'on
// veut justement refuser, parce qu'un barème à quatre décimales est le signe qu'on a mal
// lu et non une bonne nouvelle. La notation exponentielle, que la passerelle n'emploie pas
// mais que JSON autorise, repasse par le flottant et exige le même nombre de décimales.
func millimes(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	if strings.ContainsAny(s, "eE") {
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, false
		}
		mis := v * millimesParXAF
		arrondi := math.Round(mis)
		if math.Abs(mis-arrondi) > 1e-6 || math.Abs(arrondi) > math.MaxInt64/2 {
			return 0, false
		}
		return int64(arrondi), true
	}

	signe := int64(1)
	switch s[0] {
	case '+':
		s = s[1:]
	case '-':
		signe = -1
		s = s[1:]
	}
	if s == "" {
		return 0, false
	}
	entier, frac, avaitPoint := strings.Cut(s, ".")
	if avaitPoint && frac == "" {
		return 0, false
	}
	if entier == "" {
		entier = "0"
	}
	if len(frac) > 3 || !tousChiffres(entier) || (frac != "" && !tousChiffres(frac)) {
		return 0, false
	}
	for len(frac) < 3 {
		frac += "0"
	}
	e, err := strconv.ParseInt(entier, 10, 64)
	if err != nil || e > math.MaxInt64/millimesParXAF {
		return 0, false
	}
	f, err := strconv.ParseInt(frac, 10, 64)
	if err != nil {
		return 0, false
	}
	return signe * (e*millimesParXAF + f), true
}

// tousChiffres dit si la chaîne ne porte que des chiffres décimaux, et n'est pas vide.
func tousChiffres(s string) bool {
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

// grouper insère une espace tous les trois chiffres, de la droite vers la gauche.
func grouper(v int64) string {
	signe := ""
	if v < 0 {
		signe = "-"
		v = -v
	}
	chiffres := strconv.FormatInt(v, 10)
	n := len(chiffres)
	if n <= 3 {
		return signe + chiffres
	}
	var sortie []byte
	for i := range n {
		if i > 0 && (n-i)%3 == 0 {
			sortie = append(sortie, ' ')
		}
		sortie = append(sortie, chiffres[i])
	}
	return signe + string(sortie)
}

// apercu rend une valeur brute tronquée, pour un message d'erreur.
func apercu(brut jsontext.Value) string { return borner(strings.TrimSpace(string(brut))) }
