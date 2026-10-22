package pvit

import (
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// DemandeStatut interroge l'état d'une transaction.
type DemandeStatut struct {
	// Reference est la référence du marchand, celle qui a servi à engager la transaction.
	//
	// C'EST LA CLÉ QUE CETTE API ACCEPTE, et non l'identifiant que la passerelle attribue.
	// La version 0.1.0 de ce paquet affirmait l'inverse, d'après la documentation de la
	// passerelle, et cette croyance a rendu un encaissement inopérant six heures en
	// production. Mesuré le 22 octobre 2026, sur une seule et même transaction réussie :
	//
	//	reference ABOMYED6GURXUKV5YLBS -> SUCCESS, montant 4500, frais 112,5
	//	identifiant PAY2210261373646   -> 4006 TRANSACTION_NOT_FOUND
	//
	// Le champ porte donc un [Reference] et non un texte : y passer un identifiant de
	// transaction ne compile plus. C'est voulu. Un paramètre qui accepte les deux est
	// exactement ce qui a laissé l'erreur atteindre une production.
	Reference Reference

	// Operateur choisit la clé secrète, et à défaut le compte d'opération.
	Operateur Operateur

	// CompteOperation surcharge le compte déduit de l'opérateur.
	CompteOperation string

	// Operation est le sens du mouvement. Vide, elle vaut [OperationPaiement].
	Operation TypeOperation
}

// EtatTransaction est ce que la passerelle sait d'une transaction.
type EtatTransaction struct {
	// TransactionID est l'identifiant de la passerelle, s'il figure dans la réponse. Il
	// est INFORMATIF : il sert au support et au rapprochement avec un export, jamais à
	// interroger cette API, qui prend la référence.
	TransactionID string

	// Reference est la référence du marchand. À défaut d'être réécrite par la réponse,
	// c'est celle qui a été demandée.
	Reference Reference

	Statut  Statut
	Montant Montant

	// Frais ne porte QUE les frais de la passerelle. Cette API ne rend pas les frais de
	// l'opérateur, qui sont le second étage : seuls la notification et l'export les
	// donnent tous les deux.
	Frais Frais

	Operation TypeOperation
}

// reponseStatut est la réponse de l'API de statut, lue défensivement.
type reponseStatut struct {
	TransactionID   jsontext.Value `json:"transaction_id"`
	TransactionIDCC jsontext.Value `json:"transactionId"`
	ReferenceID     jsontext.Value `json:"reference_id"`
	Reference       jsontext.Value `json:"reference"`
	ReferenceCC     jsontext.Value `json:"merchantReferenceId"`
	Statut          jsontext.Value `json:"status"`
	Montant         jsontext.Value `json:"amount"`
	Frais           jsontext.Value `json:"fees"`
	Operation       jsontext.Value `json:"transactionOperation"`
	OperationSC     jsontext.Value `json:"transaction_operation"`
}

// Statut interroge l'état d'une transaction.
//
// C'EST LE MÉCANISME DE VÉRITÉ, et non un filet de sécurité. Le callback de transaction
// n'est ni signé, ni horodaté, ni muni d'un identifiant de remise, et sa politique de
// reprise n'est pas documentée : rien ne doit se créditer sur sa seule foi. Au delà d'un
// délai raisonnable sans notification, le statut est incertain et doit être redemandé par
// ici. Voir [AInterroger] et [ProchaineTentative] pour le rythme.
//
// LA CLÉ EST LA RÉFÉRENCE DU MARCHAND. Voir [DemandeStatut.Reference], qui porte la mesure
// et la date.
func (c *Client) Statut(ctx context.Context, d DemandeStatut) (EtatTransaction, error) {
	reference := strings.TrimSpace(d.Reference.String())
	if reference == "" {
		return EtatTransaction{}, ChampInvalideError{Champ: "reference", Raison: "vide"}
	}
	operation := d.Operation
	if operation == "" {
		operation = OperationPaiement
	}
	if !operation.valide() {
		return EtatTransaction{}, ChampInvalideError{
			Champ: "transactionOperation", Valeur: string(operation),
			Raison: "attendu PAYMENT ou GIVE_CHANGE",
		}
	}
	compte, err := c.compteMarchand(d.CompteOperation, d.Operateur)
	if err != nil {
		return EtatTransaction{}, err
	}

	query := url.Values{}
	// LE PARAMÈTRE S'APPELLE transactionId ET REÇOIT LA RÉFÉRENCE. Ce n'est pas une
	// coquille : c'est le nom que la passerelle emploie sur le fil, et la valeur qu'elle
	// accepte réellement. Ne pas « corriger » cette ligne en la lisant trop vite.
	query.Set("transactionId", reference)
	query.Set("accountOperationCode", compte)
	query.Set("transactionOperation", string(operation))

	res, err := c.emettre(ctx, requete{
		operation: "statut",
		operateur: d.Operateur,
		endpoint:  epStatut,
		methode:   http.MethodGet,
		query:     query,
	})
	if err != nil {
		return EtatTransaction{}, err
	}

	var rep reponseStatut
	if err := json.Unmarshal(res.corps, &rep); err != nil {
		return EtatTransaction{}, Erreur{
			Operation: "statut", HTTP: res.statut, motif: ErrReponseIllisible, cause: err,
		}
	}

	etat := EtatTransaction{Operation: operation}
	etat.TransactionID = premierTexte(rep.TransactionID, rep.TransactionIDCC, rep.ReferenceID)
	etat.Reference = Reference(premierTexte(rep.Reference, rep.ReferenceCC))
	if etat.Reference == "" {
		// La réponse n'a pas réécrit la référence, mais l'appelant vient de la donner :
		// la rendre vide ferait perdre la clé de l'état qu'on est en train de décrire.
		etat.Reference = Reference(reference)
	}
	etat.Statut = LireStatut(premierTexte(rep.Statut))
	// Les sommes propagent leur erreur : un montant illisible sur une réponse de statut
	// rendrait zéro, et l'appelant créditerait zéro franc sur une transaction réussie.
	if etat.Montant, err = montantSiPresent("amount", rep.Montant); err != nil {
		return EtatTransaction{}, err
	}
	if etat.Frais, err = fraisSiPresent("fees", rep.Frais); err != nil {
		return EtatTransaction{}, err
	}
	if op := premierTexte(rep.Operation, rep.OperationSC); op != "" {
		etat.Operation = TypeOperation(strings.ToUpper(op))
	}
	return etat, nil
}

// ReponseKYC porte ce que l'opérateur dit d'un numéro.
//
// Une carte et non une structure, et c'est la documentation qui l'impose : la version deux
// de cette API rend un objet de données SANS propriétés déclarées, avec pour seul exemple
// un prénom. La version un, elle, rendait le nom complet et un indicateur d'activité, donc
// la seule information qui serve vraiment, celle qui éviterait d'engager une transaction
// vers un compte bloqué ou inconnu. Déclarer des champs fermes ferait échouer le décodage
// sur une forme qu'on ne connaît pas.
type ReponseKYC struct {
	// Donnees porte les champs tels qu'ils sont arrivés, valeurs brutes comprises.
	Donnees map[string]jsontext.Value
}

// Texte lit un champ de la réponse, et dit s'il était lisible.
func (r ReponseKYC) Texte(cle string) (string, bool) {
	brut, present := r.Donnees[cle]
	if !present {
		return "", false
	}
	return texte(brut)
}

// Actif dit si le compte du client est actif, et si la passerelle l'a dit.
//
// Le second retour est le plus important : faux veut dire « la passerelle ne l'a pas dit »,
// ce qui n'est PAS « le compte est inactif ». Engager une transaction sur cette confusion
// irait au devant d'un refus pour compte bloqué ou inconnu.
//
// Trois écritures sont essayées, parce que la forme réelle n'est pas publiée.
func (r ReponseKYC) Actif() (actif bool, connu bool) {
	for _, cle := range []string{"is_active", "isActive", "active"} {
		brut, present := r.Donnees[cle]
		if !present {
			continue
		}
		var valeur bool
		if err := json.Unmarshal(brut, &valeur); err == nil {
			return valeur, true
		}
		if s, ok := texte(brut); ok {
			if b, err := strconv.ParseBool(strings.ToLower(s)); err == nil {
				return b, true
			}
		}
	}
	return false, false
}

// KYC demande à l'opérateur ce qu'il sait d'un numéro.
//
// Elle n'envoie PAS de clé secrète, et ne déclenche donc aucune rotation : elle fonctionne
// avec un client qui n'a aucun compte configuré. La passerelle en fait par ailleurs une
// condition d'ouverture du compte de production, au même titre que l'interrogation de
// statut.
func (c *Client) KYC(ctx context.Context, numero string, operateur Operateur) (ReponseKYC, error) {
	if strings.TrimSpace(numero) == "" {
		return ReponseKYC{}, ChampInvalideError{Champ: "customerAccountNumber", Raison: "vide"}
	}
	if err := texteBorne("customerAccountNumber", numero, maxNumeroClient); err != nil {
		return ReponseKYC{}, err
	}
	if operateur == "" {
		return ReponseKYC{}, ChampInvalideError{Champ: "operatorCode", Raison: "vide"}
	}

	query := url.Values{}
	query.Set("customerAccountNumber", numero)
	query.Set("operatorCode", string(operateur))

	res, err := c.emettre(ctx, requete{
		operation: "kyc",
		endpoint:  epKYC,
		methode:   http.MethodGet,
		query:     query,
	})
	if err != nil {
		return ReponseKYC{}, err
	}

	champs, err := champsEtDonnees(res.corps)
	if err != nil {
		return ReponseKYC{}, Erreur{
			Operation: "kyc", HTTP: res.statut, motif: ErrReponseIllisible, cause: err,
		}
	}
	return ReponseKYC{Donnees: champs}, nil
}

// ReponseFrais est ce que le calculateur de frais annonce.
type ReponseFrais struct {
	// Montant est la somme soumise au calcul.
	Montant Montant

	// Frais sont ceux de la passerelle.
	Frais Frais

	// TotalAnnonce est le total que la passerelle calcule.
	//
	// IL IGNORE LES FRAIS DE L'OPÉRATEUR, et l'afficher comme « ce que vous allez payer »
	// est donc faux par défaut. Sur l'exemple publié, ce total vaut 153,5 pour 150 francs,
	// alors que la notification du même paiement annonce 155 : il y a deux étages de
	// frais, et ce calculateur n'en montre qu'un. Seules la notification et l'export
	// donnent la vérité complète.
	//
	// ET LES DEUX ÉTAGES SONT DES POURCENTAGES, non les forfaits que les exemples publiés
	// laissent croire. Mesuré le 22 octobre 2026 sur un règlement réel de 4 500 francs :
	// 112,50 de frais de passerelle, soit 2,5 %, puis 46,12 de frais d'opérateur EN PLUS,
	// soit 4 658,62 débités pour 4 500 reversés. Un total estimé depuis les exemples
	// aurait annoncé 4 505, faux d'un facteur trente.
	TotalAnnonce Frais
}

// reponseFrais lit le calculateur, défensivement.
type reponseFrais struct {
	Montant jsontext.Value `json:"amount"`
	Frais   jsontext.Value `json:"fees"`
	Total   jsontext.Value `json:"totalAmount"`
	TotalSC jsontext.Value `json:"total_amount"`
}

// FraisDe demande le barème appliqué à un montant.
//
// C'est l'appel qui tranche une question de prix, et non une question de code : deux appels
// à deux montants disent si une cadence d'encaissement est tenable, et le seuil minimum de
// chaque opérateur n'est publié nulle part ailleurs. Voir [ErrHorsBornes].
func (c *Client) FraisDe(ctx context.Context, montant Montant, operation TypeOperation, operateur Operateur) (ReponseFrais, error) {
	if montant <= 0 {
		return ReponseFrais{}, MontantInvalideError{
			Champ: "amount", Brut: montant.String(), motif: ErrMontantInvalide,
		}
	}
	if operation == "" {
		operation = OperationPaiement
	}
	if !operation.valide() {
		return ReponseFrais{}, ChampInvalideError{
			Champ: "transactionType", Valeur: string(operation), Raison: "attendu PAYMENT ou GIVE_CHANGE",
		}
	}
	compte, err := c.compteMarchand("", operateur)
	if err != nil {
		return ReponseFrais{}, err
	}

	query := url.Values{}
	query.Set("accountOperationCode", compte)
	query.Set("transactionType", string(operation))
	query.Set("amount", strconv.FormatInt(int64(montant), 10))
	query.Set("operatorCode", string(operateur))

	res, err := c.emettre(ctx, requete{
		operation: "frais",
		operateur: operateur,
		endpoint:  epFrais,
		methode:   http.MethodGet,
		query:     query,
	})
	if err != nil {
		return ReponseFrais{}, err
	}

	var rep reponseFrais
	if err := json.Unmarshal(res.corps, &rep); err != nil {
		return ReponseFrais{}, Erreur{
			Operation: "frais", HTTP: res.statut, motif: ErrReponseIllisible, cause: err,
		}
	}
	sortie := ReponseFrais{Montant: montant}
	if lu, err := montantSiPresent("amount", rep.Montant); err != nil {
		return ReponseFrais{}, err
	} else if lu != 0 {
		sortie.Montant = lu
	}
	if sortie.Frais, err = fraisSiPresent("fees", rep.Frais); err != nil {
		return ReponseFrais{}, err
	}
	for _, brut := range []jsontext.Value{rep.Total, rep.TotalSC} {
		if !presente(brut) {
			continue
		}
		lu, err := lireFrais("totalAmount", brut)
		if err != nil {
			return ReponseFrais{}, err
		}
		sortie.TotalAnnonce = lu
		break
	}
	return sortie, nil
}

// ReponseSolde est le solde d'un compte d'opération.
type ReponseSolde struct {
	Solde           Montant
	CompteOperation string
}

// Solde lit le solde du compte d'opération d'un opérateur.
//
// Un solde insuffisant est sanctionné par un code que [Erreur.EstSoldeInsuffisant]
// reconnaît : surveiller ce solde évite de découvrir le manque au moment d'un remboursement.
func (c *Client) Solde(ctx context.Context, operateur Operateur) (ReponseSolde, error) {
	compte, err := c.compteMarchand("", operateur)
	if err != nil {
		return ReponseSolde{}, err
	}

	query := url.Values{}
	query.Set("accountOperationCode", compte)

	res, err := c.emettre(ctx, requete{
		operation: "solde",
		operateur: operateur,
		endpoint:  epSolde,
		methode:   http.MethodGet,
		query:     query,
	})
	if err != nil {
		return ReponseSolde{}, err
	}

	champs, err := champsEtDonnees(res.corps)
	if err != nil {
		return ReponseSolde{}, Erreur{
			Operation: "solde", HTTP: res.statut, motif: ErrReponseIllisible, cause: err,
		}
	}
	sortie := ReponseSolde{CompteOperation: compte}
	for _, cle := range []string{"balance", "solde", "amount", "available_balance"} {
		if brut, present := champs[cle]; present {
			if lu, err := lireMontant(cle, brut); err == nil {
				sortie.Solde = lu
				return sortie, nil
			}
		}
	}
	return sortie, Erreur{
		Operation: "solde", HTTP: res.statut,
		Message: "aucun solde lisible dans la reponse",
		motif:   ErrReponseIllisible,
	}
}

// OperateurPays est un opérateur de mobile money servi dans un pays.
type OperateurPays struct {
	Code Operateur
	Nom  string
	Pays string
}

// Operateurs liste les opérateurs servis dans un pays.
//
// C'EST L'AUTORITÉ sur la correspondance entre un numéro et un opérateur : la table de
// préfixes du sous-paquet par pays se remplit depuis ici, et non de mémoire, parce qu'un
// mauvais appariement entre le compte et l'opérateur est refusé au comptoir.
//
// Elle n'envoie pas de clé secrète, et le paramètre de pays s'écrit en tirets là où tout le
// reste de l'API est en casse mêlée. Ce n'est pas une coquille de ce paquet.
func (c *Client) Operateurs(ctx context.Context, codePays string) ([]OperateurPays, error) {
	codePays = strings.ToUpper(strings.TrimSpace(codePays))
	if codePays == "" {
		return nil, ChampInvalideError{Champ: "country-code", Raison: "vide"}
	}

	query := url.Values{}
	query.Set("country-code", codePays)

	res, err := c.emettre(ctx, requete{
		operation: "operateurs",
		endpoint:  epOperateurs,
		methode:   http.MethodGet,
		query:     query,
	})
	if err != nil {
		return nil, err
	}

	objets, err := listeDObjets(res.corps)
	if err != nil {
		return nil, Erreur{
			Operation: "operateurs", HTTP: res.statut, motif: ErrReponseIllisible, cause: err,
		}
	}
	liste := make([]OperateurPays, 0, len(objets))
	for _, objet := range objets {
		liste = append(liste, OperateurPays{
			Code: Operateur(valeurParmi(objet, "operator_code", "operatorCode", "code")),
			Nom:  valeurParmi(objet, "name", "nom", "label", "operator_name"),
			Pays: valeurParmi(objet, "country_code", "countryCode", "country", "pays"),
		})
	}
	return liste, nil
}

// Pays est un pays servi par la passerelle.
type Pays struct {
	Code      string
	Nom       string
	Indicatif string
}

// Pays liste les pays servis par la passerelle. Elle n'envoie pas de clé secrète.
func (c *Client) Pays(ctx context.Context) ([]Pays, error) {
	res, err := c.emettre(ctx, requete{
		operation: "pays",
		endpoint:  epPays,
		methode:   http.MethodGet,
	})
	if err != nil {
		return nil, err
	}

	objets, err := listeDObjets(res.corps)
	if err != nil {
		return nil, Erreur{
			Operation: "pays", HTTP: res.statut, motif: ErrReponseIllisible, cause: err,
		}
	}
	liste := make([]Pays, 0, len(objets))
	for _, objet := range objets {
		liste = append(liste, Pays{
			Code:      valeurParmi(objet, "country_code", "countryCode", "code", "iso"),
			Nom:       valeurParmi(objet, "name", "nom", "label", "country_name"),
			Indicatif: valeurParmi(objet, "dial_code", "dialCode", "indicatif", "phone_code", "prefix"),
		})
	}
	return liste, nil
}

// premierTexte rend la première valeur brute lisible en texte.
func premierTexte(bruts ...jsontext.Value) string {
	for _, brut := range bruts {
		if valeur, ok := texte(brut); ok && valeur != "" {
			return valeur
		}
	}
	return ""
}

// valeurParmi lit la première clé présente et lisible d'un objet.
//
// Plusieurs noms pour une même information, parce que la forme de ces listes n'est pas
// publiée : essayer quelques écritures coûte une ligne, là où en deviner une seule rendrait
// une liste de valeurs vides.
func valeurParmi(objet map[string]jsontext.Value, cles ...string) string {
	for _, cle := range cles {
		if brut, present := objet[cle]; present {
			if valeur, ok := texte(brut); ok && valeur != "" {
				return valeur
			}
		}
	}
	return ""
}

// champsEtDonnees lit un objet de réponse, qu'il soit à plat ou emballé dans un champ de
// données.
//
// Les deux formes existent dans cette API, et l'enveloppe gagne quand les deux sont là :
// c'est elle que la version deux de l'API de renseignement client emploie.
func champsEtDonnees(corps []byte) (map[string]jsontext.Value, error) {
	var racine map[string]jsontext.Value
	if err := json.Unmarshal(corps, &racine); err != nil {
		return nil, err
	}
	champs := make(map[string]jsontext.Value, len(racine))
	maps.Copy(champs, racine)
	if enveloppe, present := racine["data"]; present {
		var dedans map[string]jsontext.Value
		if err := json.Unmarshal(enveloppe, &dedans); err == nil {
			maps.Copy(champs, dedans)
		}
	}
	return champs, nil
}

// listeDObjets lit une liste de réponse, nue ou emballée dans un champ de données.
func listeDObjets(corps []byte) ([]map[string]jsontext.Value, error) {
	var nue []map[string]jsontext.Value
	if err := json.Unmarshal(corps, &nue); err == nil {
		return nue, nil
	}

	var racine map[string]jsontext.Value
	if err := json.Unmarshal(corps, &racine); err != nil {
		return nil, err
	}
	for _, cle := range []string{"data", "operators", "countries", "items", "results"} {
		brut, present := racine[cle]
		if !present {
			continue
		}
		var liste []map[string]jsontext.Value
		if err := json.Unmarshal(brut, &liste); err == nil {
			return liste, nil
		}
	}
	return nil, fmt.Errorf("aucune liste d objets dans la reponse")
}
