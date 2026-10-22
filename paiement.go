package pvit

import (
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// requetePaiement est la charge utile d'un encaissement.
//
// Une structure PROPRE au paiement, et non partagée avec le remboursement. La raison est
// dans le champ ci-dessous : le remboursement n'a pas de code de callback, et le rendre
// facultatif par une balise d'omission laisserait un jour partir un paiement sans lui, donc
// sans aucune notification. Deux structures coûtent dix lignes et ferment ce cas.
type requetePaiement struct {
	Agent          string        `json:"agent,omitempty"`
	Montant        Montant       `json:"amount"`
	Produit        string        `json:"product,omitempty"`
	Reference      Reference     `json:"reference"`
	Service        Service       `json:"service"`
	CodeCallback   string        `json:"callback_url_code"`
	NumeroClient   string        `json:"customer_account_number"`
	CompteMarchand string        `json:"merchant_operation_account_code"`
	TypeOperation  TypeOperation `json:"transaction_type"`
	Frais          Proprietaire  `json:"owner_charge"`
	FraisOperateur Proprietaire  `json:"owner_charge_operator"`
	InfoLibre      string        `json:"free_info,omitempty"`
	Operateur      Operateur     `json:"operator_code"`
}

// requeteRemboursement est la charge utile d'un remboursement.
//
// Elle N'A PAS de champ de code de callback, et c'est volontaire : le remboursement est
// SYNCHRONE, son statut final figure dans la réponse, et aucune notification ne suit.
type requeteRemboursement struct {
	Agent          string        `json:"agent,omitempty"`
	Montant        Montant       `json:"amount"`
	Produit        string        `json:"product,omitempty"`
	Reference      Reference     `json:"reference"`
	Service        Service       `json:"service"`
	NumeroClient   string        `json:"customer_account_number"`
	CompteMarchand string        `json:"merchant_operation_account_code"`
	TypeOperation  TypeOperation `json:"transaction_type"`
	Frais          Proprietaire  `json:"owner_charge"`
	FraisOperateur Proprietaire  `json:"owner_charge_operator"`
	InfoLibre      string        `json:"free_info,omitempty"`
	Operateur      Operateur     `json:"operator_code"`
}

// reponseTransaction est l'accusé que rend l'API de transaction.
//
// Chaque champ entre en valeur brute, et plusieurs noms sont lus pour une même
// information : la passerelle écrit son code de retour de quatre façons selon l'endroit, et
// son identifiant sous au moins deux noms. Déclarer un seul nom et un type ferme ferait
// échouer le décodage entier sur une variation de forme, c'est à dire précisément quand il
// ne faut pas échouer.
type reponseTransaction struct {
	ReferenceID     jsontext.Value `json:"reference_id"`
	TransactionID   jsontext.Value `json:"transaction_id"`
	TransactionIDCC jsontext.Value `json:"transactionId"`
	Statut          jsontext.Value `json:"status"`
	StatusCode      jsontext.Value `json:"status_code"`
	StatusCodeCC    jsontext.Value `json:"statusCode"`
	Message         jsontext.Value `json:"message"`
}

// identifiant rend le premier identifiant lisible parmi les noms connus.
func (r reponseTransaction) identifiant() (string, bool) {
	for _, brut := range []jsontext.Value{r.ReferenceID, r.TransactionID, r.TransactionIDCC} {
		if valeur, ok := texte(brut); ok && valeur != "" {
			return valeur, true
		}
	}
	return "", false
}

// code rend le code applicatif, quel que soit le nom et le type sous lequel il arrive.
func (r reponseTransaction) code() int {
	for _, brut := range []jsontext.Value{r.StatusCode, r.StatusCodeCC} {
		if valeur, ok := codeApplicatif(brut); ok {
			return valeur
		}
	}
	return 0
}

// Initiation est l'accusé d'un paiement engagé.
//
// ELLE NE DIT PAS QUE LE PAIEMENT EST FAIT. Le statut vaut l'attente, et le résultat
// arrive plus tard par notification, ou se redemande par [Client.Statut]. Rien ne doit se
// créditer sur la foi de cet accusé.
type Initiation struct {
	// TransactionID est l'identifiant que la passerelle attribue, s'il figure dans
	// l'accusé.
	//
	// IL EST INFORMATIF, et peut être vide. La version 0.1.0 de ce paquet le tenait pour
	// indispensable et rendait une erreur en son absence, parce qu'elle croyait que
	// l'interrogation de statut n'acceptait que lui. C'est la référence qui réconcilie :
	// voir [DemandeStatut.Reference]. Refuser un accusé sans identifiant rendait donc en
	// erreur un paiement réellement engagé, qui allait débiter le client.
	TransactionID string

	// Reference est la référence du marchand. ELLE PORTE TOUT : l'idempotence à l'aller,
	// et l'interrogation du statut au retour.
	Reference Reference

	// Statut est l'état rapporté, normalement l'attente.
	Statut Statut

	// Code est le code applicatif de l'accusé.
	Code int

	// Message est la phrase de la passerelle, s'il y en a une.
	Message string
}

// DemandePaiement décrit un encaissement à engager.
//
// Les champs vides ou nuls prennent le défaut du client : c'est ce qui permet au même
// client de servir deux politiques de frais sans être reconstruit, et de mesurer les frais
// à deux montants sans toucher à la configuration.
type DemandePaiement struct {
	// Reference porte l'idempotence, et doit être persistée AVANT l'appel. Voir
	// [Reference].
	Reference Reference

	// Montant est la somme à encaisser, en francs entiers.
	Montant Montant

	// Operateur choisit le compte d'opération, et doit correspondre au numéro du client :
	// le code 3017 sanctionne l'appariement incorrect.
	Operateur Operateur

	// NumeroClient est le numéro mobile money du payeur, vingt-trois caractères au plus.
	NumeroClient string

	// CompteOperation surcharge le compte déduit de l'opérateur. Vide, il est déduit.
	CompteOperation string

	// CodeCallback surcharge le code de callback des réglages. Vide, il est repris.
	CodeCallback string

	// Frais dit qui supporte les frais de la passerelle. Vide, il prend le défaut du
	// client, lui-même [ProprietaireClient] sauf option contraire.
	Frais Proprietaire

	// FraisOperateur dit qui supporte le second étage de frais. Même règle.
	FraisOperateur Proprietaire

	// Agent, Produit et InfoLibre sont des champs libres de quinze caractères au plus,
	// que la passerelle rend tels quels dans ses exports.
	Agent     string
	Produit   string
	InfoLibre string
}

// Valider relève TOUTES les violations d'un coup, et n'émet aucune requête.
//
// Toutes et non la première : un formulaire se corrige alors en un passage, là où une
// validation qui s'arrête au premier défaut demande autant d'allers-retours qu'il y a de
// fautes. Chaque violation est une [ChampInvalideError] retrouvable par errors.AsType.
func (d DemandePaiement) Valider() error {
	manques := manquesCommuns(champsCommuns{
		Reference:      string(d.Reference),
		Montant:        d.Montant,
		Operateur:      d.Operateur,
		NumeroClient:   d.NumeroClient,
		Compte:         d.CompteOperation,
		Agent:          d.Agent,
		Produit:        d.Produit,
		InfoLibre:      d.InfoLibre,
		Frais:          d.Frais,
		FraisOperateur: d.FraisOperateur,
	})

	if d.CodeCallback != "" && len(d.CodeCallback) > maxCodeCallback {
		manques = append(manques, ChampInvalideError{
			Champ: "callback_url_code", Valeur: borner(d.CodeCallback),
			Longueur: len(d.CodeCallback), Max: maxCodeCallback,
		})
	}
	return errors.Join(manques...)
}

// DemandeRemboursement décrit un remboursement, qui est synchrone.
type DemandeRemboursement struct {
	// Reference porte l'idempotence, comme pour un paiement.
	Reference Reference

	// Montant est la somme à rendre.
	Montant Montant

	// Operateur choisit le compte d'opération.
	Operateur Operateur

	// NumeroClient est le numéro du bénéficiaire.
	NumeroClient string

	// CompteOperation surcharge le compte déduit de l'opérateur.
	CompteOperation string

	// Frais dit qui supporte les frais. Vide, il vaut [ProprietaireMarchand] : on ne fait
	// pas payer au client les frais d'un remboursement, et c'est aussi ce que montre la
	// requête d'exemple de la passerelle.
	Frais Proprietaire

	// FraisOperateur dit qui supporte le second étage. Vide, il prend le défaut du client.
	FraisOperateur Proprietaire

	// Agent, Produit et InfoLibre sont des champs libres de quinze caractères au plus.
	Agent     string
	Produit   string
	InfoLibre string
}

// Valider relève toutes les violations d'un coup, et n'émet aucune requête.
func (d DemandeRemboursement) Valider() error {
	return errors.Join(manquesCommuns(champsCommuns{
		Reference:      string(d.Reference),
		Montant:        d.Montant,
		Operateur:      d.Operateur,
		NumeroClient:   d.NumeroClient,
		Compte:         d.CompteOperation,
		Agent:          d.Agent,
		Produit:        d.Produit,
		InfoLibre:      d.InfoLibre,
		Frais:          d.Frais,
		FraisOperateur: d.FraisOperateur,
	})...)
}

// Remboursement est le résultat d'un remboursement.
//
// Son statut est FINAL : l'opération est synchrone, et aucune notification ne suivra.
type Remboursement struct {
	TransactionID string
	Reference     Reference
	Statut        Statut
	Code          int
	Message       string
}

// champsCommuns porte ce que le paiement et le remboursement valident de la même façon.
//
// Une structure à champs NOMMÉS, et non dix paramètres positionnels dont six chaînes de
// suite : une inversion de deux d'entre eux compilerait sans bruit, et la validation
// nommerait alors le mauvais champ dans son message d'erreur.
type champsCommuns struct {
	Reference      string
	Montant        Montant
	Operateur      Operateur
	NumeroClient   string
	Compte         string
	Agent          string
	Produit        string
	InfoLibre      string
	Frais          Proprietaire
	FraisOperateur Proprietaire
}

// manquesCommuns relève les violations que le paiement et le remboursement partagent.
func manquesCommuns(c champsCommuns) []error {
	reference, montant, operateur := c.Reference, c.Montant, c.Operateur
	numero, compte := c.NumeroClient, c.Compte
	agent, produit, infoLibre := c.Agent, c.Produit, c.InfoLibre
	frais, fraisOperateur := c.Frais, c.FraisOperateur

	var manques []error

	if _, err := NouvelleReference(reference); err != nil {
		manques = append(manques, err)
	}
	if montant <= 0 {
		manques = append(manques, ChampInvalideError{
			Champ: "amount", Valeur: montant.String(), Raison: "nul ou negatif",
		})
	}
	if operateur == "" {
		manques = append(manques, ChampInvalideError{Champ: "operator_code", Raison: "vide"})
	}
	if strings.TrimSpace(numero) == "" {
		manques = append(manques, ChampInvalideError{Champ: "customer_account_number", Raison: "vide"})
	} else if err := texteBorne("customer_account_number", numero, maxNumeroClient); err != nil {
		manques = append(manques, err)
	}
	if compte != "" {
		if err := texteBorne("merchant_operation_account_code", compte, maxCodeOperation); err != nil {
			manques = append(manques, err)
		}
	}
	if err := texteBorne("agent", agent, maxAgent); err != nil {
		manques = append(manques, err)
	}
	if err := texteBorne("product", produit, maxProduit); err != nil {
		manques = append(manques, err)
	}
	if err := texteBorne("free_info", infoLibre, maxInfoLibre); err != nil {
		manques = append(manques, err)
	}
	if frais != "" && !frais.valide() {
		manques = append(manques, ChampInvalideError{
			Champ: "owner_charge", Valeur: string(frais), Raison: "attendu CUSTOMER ou MERCHANT",
		})
	}
	if fraisOperateur != "" && !fraisOperateur.valide() {
		manques = append(manques, ChampInvalideError{
			Champ: "owner_charge_operator", Valeur: string(fraisOperateur), Raison: "attendu CUSTOMER ou MERCHANT",
		})
	}
	return manques
}

// compteMarchand résout le compte d'opération à employer pour un opérateur.
//
// C'est la garde contre le code 3017, et elle est CÔTÉ CLIENT : le compte d'opération n'est
// pas une constante, la passerelle en demande un par famille d'opérateur, et envoyer celui
// d'Airtel sur un numéro Moov est refusé au comptoir, devant un client qui attend.
func (c *Client) compteMarchand(impose string, operateur Operateur) (string, error) {
	// L'opérateur est exigé MÊME quand le compte est imposé, parce qu'il ne choisit pas
	// seulement le compte : il choisit aussi la clé secrète à envoyer. Sans lui, l'appel
	// partirait pour échouer plus loin sur un « aucun compte d operation pour  », message
	// que personne ne sait lire.
	if operateur == "" {
		return "", ChampInvalideError{
			Champ:  "operator_code",
			Raison: "vide, or il choisit la cle secrete autant que le compte d operation",
		}
	}
	if impose != "" {
		return impose, nil
	}
	vivant, connu := c.comptes[operateur]
	if !connu {
		return "", fmt.Errorf("%w: aucun compte d operation pour %s, configures: %v",
			ErrConfiguration, operateur, c.OperateursConfigures())
	}
	return vivant.reglage.CodeOperation, nil
}

// Payer engage un encaissement.
//
// ELLE NE RAPPORTE PAS UN PAIEMENT FAIT. La réponse porte l'attente, et le résultat arrive
// par notification ou se redemande par [Client.Statut], sous la RÉFÉRENCE qui vient d'être
// employée.
//
// # LE CAS QUI COMPTE : UNE RÉFÉRENCE DÉJÀ VUE
//
// Si la référence a déjà servi, la passerelle refuse par le code 5000, et cette méthode
// rend ALORS LES DEUX : une [Initiation] portant la référence, et une erreur qui vérifie
// errors.Is(err, [ErrDejaInitiee]). L'appelant qui connaît ce cas ignore l'erreur et
// interroge le statut ; celui qui ne le connaît pas voit une erreur et ne crédite rien.
// Relancer sous une référence neuve, en revanche, créerait un DOUBLE PAIEMENT.
//
// Le statut rendu dans ce cas est la valeur zéro, donc incertain : la passerelle n'a rien
// dit de l'état de la tentative existante, et le prétendre serait inventer.
func (c *Client) Payer(ctx context.Context, d DemandePaiement) (Initiation, error) {
	if err := d.Valider(); err != nil {
		return Initiation{}, err
	}
	compte, err := c.compteMarchand(strings.TrimSpace(d.CompteOperation), d.Operateur)
	if err != nil {
		return Initiation{}, err
	}
	codeCallback := strings.TrimSpace(d.CodeCallback)
	if codeCallback == "" {
		codeCallback = c.reglages.CodeCallback
	}
	if codeCallback == "" {
		// La passerelle donne ce champ pour facultatif, mais sans lui aucune
		// notification n'est jamais émise : un optionnel de syntaxe et un obligatoire de
		// fonctionnement.
		return Initiation{}, ChampInvalideError{
			Champ:  "callback_url_code",
			Raison: "vide, or sans lui aucune notification n est emise",
		}
	}

	corps, err := json.Marshal(requetePaiement{
		Agent:          d.Agent,
		Montant:        d.Montant,
		Produit:        d.Produit,
		Reference:      d.Reference,
		Service:        c.opts.service,
		CodeCallback:   codeCallback,
		NumeroClient:   strings.TrimSpace(d.NumeroClient),
		CompteMarchand: compte,
		TypeOperation:  OperationPaiement,
		Frais:          d.Frais.ouDefaut(c.opts.proprietaireFrais),
		FraisOperateur: d.FraisOperateur.ouDefaut(c.opts.proprietaireFraisOperateur),
		InfoLibre:      d.InfoLibre,
		Operateur:      d.Operateur,
	})
	if err != nil {
		return Initiation{}, ChampInvalideError{Champ: "corps", Raison: "serialisation json impossible"}
	}

	res, err := c.emettre(ctx, requete{
		operation:   "paiement",
		operateur:   d.Operateur,
		endpoint:    epPaiement,
		methode:     http.MethodPost,
		corps:       corps,
		typeContenu: "application/json",
		entetes:     map[string]string{enteteMediaTypeCallback: c.opts.mediaTypeCallback},
	})
	if err != nil {
		if errors.Is(err, ErrDejaInitiee) {
			return Initiation{Reference: d.Reference}, err
		}
		return Initiation{}, err
	}

	return lireInitiation(res, d.Reference)
}

// lireInitiation décode un accusé de paiement.
func lireInitiation(res resultat, reference Reference) (Initiation, error) {
	var rep reponseTransaction
	if err := json.Unmarshal(res.corps, &rep); err != nil {
		return Initiation{}, Erreur{
			Operation: "paiement", HTTP: res.statut, motif: ErrReponseIllisible, cause: err,
		}
	}

	// UN ACCUSÉ SANS IDENTIFIANT N'EST PAS UN ÉCHEC, et la version 0.1.0 avait tort d'en
	// rendre une erreur. La tentative se réconcilie par sa référence, que l'appelant a
	// persistée avant l'appel ; refuser ici laisserait un paiement engagé, donc un client
	// prêt à être débité, du côté de l'échec.
	identifiant, _ := rep.identifiant()

	statut, _ := texte(rep.Statut)
	message, _ := texte(rep.Message)
	return Initiation{
		TransactionID: identifiant,
		Reference:     reference,
		Statut:        LireStatut(statut),
		Code:          rep.code(),
		Message:       tronquer(message),
	}, nil
}

// Rembourser rend de l'argent, et le fait de façon SYNCHRONE.
//
// Le statut rendu est final : aucune notification ne suivra, et il n'y a donc rien à
// réconcilier. Le champ de code de callback n'est pas même émis, comme la requête d'exemple
// de la passerelle le montre.
func (c *Client) Rembourser(ctx context.Context, d DemandeRemboursement) (Remboursement, error) {
	if err := d.Valider(); err != nil {
		return Remboursement{}, err
	}
	compte, err := c.compteMarchand(strings.TrimSpace(d.CompteOperation), d.Operateur)
	if err != nil {
		return Remboursement{}, err
	}

	corps, err := json.Marshal(requeteRemboursement{
		Agent:          d.Agent,
		Montant:        d.Montant,
		Produit:        d.Produit,
		Reference:      d.Reference,
		Service:        c.opts.service,
		NumeroClient:   strings.TrimSpace(d.NumeroClient),
		CompteMarchand: compte,
		TypeOperation:  OperationRemboursement,
		Frais:          d.Frais.ouDefaut(ProprietaireMarchand),
		FraisOperateur: d.FraisOperateur.ouDefaut(c.opts.proprietaireFraisOperateur),
		InfoLibre:      d.InfoLibre,
		Operateur:      d.Operateur,
	})
	if err != nil {
		return Remboursement{}, ChampInvalideError{Champ: "corps", Raison: "serialisation json impossible"}
	}

	// Aucun en-tête de type de notification : il ne vaut que pour les paiements.
	res, err := c.emettre(ctx, requete{
		operation:   "remboursement",
		operateur:   d.Operateur,
		endpoint:    epPaiement,
		methode:     http.MethodPost,
		corps:       corps,
		typeContenu: "application/json",
	})
	if err != nil {
		if errors.Is(err, ErrDejaInitiee) {
			return Remboursement{Reference: d.Reference}, err
		}
		return Remboursement{}, err
	}

	var rep reponseTransaction
	if err := json.Unmarshal(res.corps, &rep); err != nil {
		return Remboursement{}, Erreur{
			Operation: "remboursement", HTTP: res.statut, motif: ErrReponseIllisible, cause: err,
		}
	}
	identifiant, _ := rep.identifiant()
	statut, _ := texte(rep.Statut)
	message, _ := texte(rep.Message)
	return Remboursement{
		TransactionID: identifiant,
		Reference:     d.Reference,
		Statut:        LireStatut(statut),
		Code:          rep.code(),
		Message:       tronquer(message),
	}, nil
}
