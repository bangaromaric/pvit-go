package pvit

import (
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// Le lien de paiement, et avec lui LE PAIEMENT PAR CARTE BANCAIRE.
//
// IL N'EXISTE AUCUN POINT D'ENTRÉE CARTE chez cette passerelle, et c'est le fait que ce
// fichier existe pour porter. Encaisser par Visa ou Mastercard, c'est demander un lien avec
// le service VISA_MASTERCARD, recevoir une adresse, et y envoyer le client : le formulaire
// bancaire est hébergé par la passerelle, et l'authentification forte se joue derrière, hors
// de portée de ce paquet.
//
// Le chemin du lien tend deux pièges que la table des points d'entrée et la lecture défensive
// d'ici désarment :
//
//   - il N'EST PAS VERSIONNÉ, alors qu'il attend la charge utile de la version deux ;
//   - il répond en CASSE MÊLÉE avec un code de retour ENTIER, là où l'API de transaction
//     répond en soulignés avec la chaîne « 200 ».
//
// Et il n'a PAS de champ operator_code, alors que l'opérateur reste exigé côté client :
// voir [DemandeLien.Operateur].

// requeteLien est la charge utile de l'API de lien de paiement.
//
// Une structure PROPRE au lien, et non [requetePaiement] réemployée. Trois faits l'imposent,
// et chacun se paierait par un champ de trop ou de moins :
//
//   - le corps du lien ne porte PAS operator_code ;
//   - les deux codes de redirection y sont obligatoires et n'existent nulle part ailleurs.
//     Les rendre facultatifs par une balise d'omission laisserait un jour partir un lien
//     dont le client ne reviendrait sur aucune page ;
//   - le numéro du client y est CONDITIONNEL, et son absence doit être une absence de champ
//     et non un champ vide, que la validation du serveur pourrait refuser.
type requeteLien struct {
	Agent          string        `json:"agent,omitempty"`
	Montant        Montant       `json:"amount"`
	Produit        string        `json:"product,omitempty"`
	Reference      Reference     `json:"reference"`
	Service        Service       `json:"service"`
	CodeCallback   string        `json:"callback_url_code"`
	NumeroClient   string        `json:"customer_account_number,omitempty"`
	CompteMarchand string        `json:"merchant_operation_account_code"`
	TypeOperation  TypeOperation `json:"transaction_type"`
	Frais          Proprietaire  `json:"owner_charge"`

	// FraisOperateur et FraisOperateurPostman portent la MÊME information sous les deux
	// noms que les deux sources publiées emploient, et UN SEUL DES DEUX part jamais.
	//
	// Deux champs plutôt qu'un marshaleur sur mesure, et jamais les deux remplis : un
	// champ inconnu vaut un 8000, et deux noms pour un même réglage laisseraient la
	// passerelle choisir lequel lire. Voir
	// [AvecFraisOperateurEnOperatorOwnerCharge] pour le désaccord des sources.
	FraisOperateur        Proprietaire `json:"owner_charge_operator,omitempty"`
	FraisOperateurPostman Proprietaire `json:"operator_owner_charge,omitempty"`

	InfoLibre         string `json:"free_info,omitempty"`
	RedirectionEchec  string `json:"failed_redirection_url_code"`
	RedirectionSucces string `json:"success_redirection_url_code"`
}

// reponseLien est l'accusé que rend l'API de lien de paiement.
//
// Chaque champ entre en valeur brute, et le code de retour est lu sous SES DEUX NOMS. Ce
// n'est pas de la prudence décorative : cette passerelle n'est pas cohérente d'un point
// d'entrée à l'autre, le lien répond en casse mêlée avec un code ENTIER là où l'API de
// transaction répond en soulignés avec une CHAÎNE, et déclarer un seul nom ferait échouer le
// décodage ENTIER sur une variation de forme, c'est à dire précisément quand il ne faut pas
// échouer.
//
// LA RÉFÉRENCE N'EST PAS LUE ICI, et son champ n'est donc pas déclaré. La passerelle en
// renvoie bien un écho, sous merchantReferenceId, mais celui qui fait autorité est celui que
// l'appelant a persisté AVANT l'appel : c'est lui qui réconcilie, et le relire de la réponse
// ne ferait que donner deux sources à une même vérité. Déclarer un champ qu'on décode sans
// jamais le consulter laisserait croire qu'il sert.
//
// Une structure propre, et non [reponseTransaction] réemployée : celle-ci résoudrait un
// identifiant de transaction que ce point d'entrée ne rend PAS, et le champ resterait vide
// à vie en invitant la question « pourquoi ».
type reponseLien struct {
	URL     jsontext.Value `json:"url"`
	Statut  jsontext.Value `json:"status"`
	CodeCC  jsontext.Value `json:"statusCode"`
	CodeSC  jsontext.Value `json:"status_code"`
	Message jsontext.Value `json:"message"`
}

// code rend le code applicatif, quel que soit le nom et le type sous lequel il arrive.
func (r reponseLien) code() int {
	for _, brut := range []jsontext.Value{r.CodeCC, r.CodeSC} {
		if valeur, ok := codeApplicatif(brut); ok {
			return valeur
		}
	}
	return 0
}

// Lien est le lien de paiement engagé.
//
// IL NE DIT PAS QU'UN PAIEMENT A EU LIEU. Il dit où envoyer le client, et rien de plus : à
// ce stade, le payeur n'a pas encore ouvert le formulaire. Le résultat arrive par
// notification, ou se redemande par [Client.Statut] sous la RÉFÉRENCE.
type Lien struct {
	// URL est l'adresse à présenter au client, telle que la passerelle l'a écrite.
	//
	// Elle est rendue en chaîne et non analysée, parce que c'est une chaîne qui part dans
	// un en-tête de redirection ou dans un gabarit, et qu'une adresse reconstruite depuis
	// une forme analysée n'est pas garantie identique à l'octet près : celle-ci porte un
	// jeton opaque. Elle a toutefois été VÉRIFIÉE utilisable, voir [Client.CreerLien].
	URL string

	// Reference est la référence du marchand. ELLE PORTE TOUT : l'idempotence à l'aller,
	// et l'interrogation du statut au retour.
	//
	// Elle est renseignée MÊME quand l'appel échoue, parce que la passerelle a pu consommer
	// la tentative avant de refuser.
	Reference Reference

	// StatutCreation est l'état que la passerelle rapporte pour la CRÉATION DU LIEN, et
	// jamais pour le paiement.
	//
	// LE NOM EST LE GARDE-FOU. La passerelle répond « SUCCESS » à un lien qu'elle vient
	// tout juste de fabriquer, et un champ nommé Statut rendrait
	// lien.Statut.EstSucces() compilable, lisible comme « le paiement a réussi », et
	// capable de créditer un client qui n'a pas même ouvert le formulaire bancaire. Le
	// statut de la TRANSACTION, lui, vaut l'attente par construction, et se redemande par
	// [Client.Statut].
	StatutCreation Statut

	// Code est le code applicatif de l'accusé.
	Code int

	// Message est la phrase de la passerelle, s'il y en a une.
	Message string
}

// Utilisable dit si ce lien peut être présenté à un client.
func (l Lien) Utilisable() bool { return l.URL != "" }

// DemandeLien décrit un lien de paiement à engager.
//
// Les champs vides prennent le défaut du client, à une exception près et elle est voulue :
// [DemandeLien.Service] n'en a pas.
type DemandeLien struct {
	// Service choisit le parcours, et il est OBLIGATOIRE SANS DÉFAUT.
	//
	// Trois valeurs seulement : [ServiceWeb], [ServiceVisaMastercard] et
	// [ServiceRestlink]. [ServiceRestful] est refusé, parce qu'il appartient à l'API de
	// transaction.
	//
	// Il ne vient PAS de [AvecService], et le laisser prendre ce défaut serait la faute la
	// plus coûteuse de ce fichier : le défaut de cette option est RESTFUL, soit justement
	// la seule des quatre valeurs que ce point d'entrée n'accepte pas. Un défaut muet
	// enverrait par ailleurs sur une page web un marchand qui croyait émettre un lien
	// carte, ce qu'aucun test ne verrait et que le client découvrirait seul.
	Service Service

	// Reference porte l'idempotence, et doit être persistée AVANT l'appel. Voir
	// [Reference].
	Reference Reference

	// Montant est la somme à encaisser, en francs entiers.
	Montant Montant

	// Operateur choisit le compte d'opération ET LA CLÉ SECRÈTE, et NE PART PAS SUR LE FIL.
	//
	// C'est le fait le plus surprenant de ce point d'entrée : sa charge utile n'a aucun
	// champ operator_code, et l'opérateur reste pourtant exigé ici. La raison est celle que
	// porte déjà compteMarchand : il ne choisit pas seulement le compte, il choisit aussi
	// la clé qui part dans l'en-tête. Sans lui, l'appel partirait sans clé pour échouer
	// plus loin sur un message que personne ne sait lire.
	//
	// Pour une carte, c'est la famille que l'espace marchand a ouverte au marchand qui le
	// désigne. Ce paquet n'en nomme aucune constante : le jeton exact n'est publié nulle
	// part, [Operateur] est un type ouvert pour cette raison, et [Client.Operateurs] fait
	// autorité. Déclarer une famille carte au petit bonheur coûterait un refus sur les trois
	// autres API où ce même code part vraiment sur le fil.
	Operateur Operateur

	// NumeroClient est le numéro mobile money du payeur, vingt-trois caractères au plus.
	//
	// Il est CONDITIONNEL : exigé par [ServiceVisaMastercard] et [ServiceRestlink],
	// facultatif par [ServiceWeb], dont la page le demande elle-même au client.
	//
	// CE N'EST JAMAIS UN NUMÉRO DE CARTE. Les données de la carte s'entrent sur le
	// formulaire que la passerelle héberge, derrière l'adresse rendue, et aucun champ de ce
	// paquet n'en transporte : c'est ce qui tient l'appelant hors du périmètre PCI. Ce que
	// ce champ porte exactement pour un parcours carte n'est pas publié au delà de
	// « requis » ; ce paquet le traite comme le numéro du client, comme partout ailleurs,
	// et n'invente rien.
	NumeroClient string

	// CompteOperation surcharge le compte déduit de l'opérateur. Vide, il est déduit.
	CompteOperation string

	// CodeCallback surcharge le code de callback des réglages. Vide, il est repris.
	CodeCallback string

	// CodeRedirectionSucces et CodeRedirectionEchec surchargent les codes des réglages.
	// Vides, ils sont repris. Voir [Reglages.CodeRedirectionSucces].
	CodeRedirectionSucces string
	CodeRedirectionEchec  string

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
// Elle ne juge PAS la présence des codes de callback et de redirection : l'appelant a le
// droit de s'en remettre aux réglages du client, et c'est [Client.CreerLien] qui tranche,
// après la fusion. Même partage que [DemandePaiement.Valider], qui ne contrôle ici que la
// longueur.
func (d DemandeLien) Valider() error {
	return errors.Join(manquesLien(d)...)
}

// manquesLien relève ce qu'un lien valide de plus, et de moins, qu'un paiement.
//
// Elle appuie sur manquesCommuns pour les neuf contrôles que les deux chemins partagent à
// l'identique, et n'ajoute que ce qui est propre au lien. Une copie de ces neuf contrôles
// serait quarante-cinq lignes qui divergeraient : l'asymétrie avec le couple
// [requetePaiement] et [requeteRemboursement] est délibérée, car là bas les deux
// structures diffèrent par un champ dont la présence EST la propriété de sûreté, tandis
// qu'ici le lien n'ajoute aucune règle qui contredise un paiement.
//
// Les longueurs dures du chemin de transaction sont appliquées ici alors que la
// documentation du lien ne les republie pas : même passerelle, mêmes noms de champ, et
// l'alternative est un 8000 dont le corps ne nomme pas toujours le champ fautif. C'est une
// reprise raisonnée, et non une certitude.
func manquesLien(d DemandeLien) []error {
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

		// Le parcours web est le seul à s'en passer, et un service inconnu ou absent est
		// traité comme exigeant : c'est le sens prudent, et la violation du service, qui
		// suit, dira la vraie faute.
		NumeroFacultatif: d.Service == ServiceWeb,
	})

	if !d.Service.estUnLien() {
		raison := "attendu WEB, VISA_MASTERCARD ou RESTLINK"
		if d.Service == ServiceRestful {
			raison += ": RESTFUL est le parcours de l API de transaction, voir Client.Payer"
		}
		manques = append(manques, ChampInvalideError{
			Champ: "service", Valeur: borner(string(d.Service)), Raison: raison,
		})
	}

	if d.CodeCallback != "" && len(d.CodeCallback) > maxCodeCallback {
		manques = append(manques, ChampInvalideError{
			Champ: "callback_url_code", Valeur: borner(d.CodeCallback),
			Longueur: len(d.CodeCallback), Max: maxCodeCallback,
		})
	}
	return manques
}

// DemandeCarte décrit un encaissement par carte Visa ou Mastercard.
//
// Une structure PROPRE à la carte, et non [DemandeLien] avec un service à renseigner. Deux
// fautes quittent ainsi le domaine du possible, et c'est le raisonnement qui a déjà séparé
// la charge utile du paiement de celle du remboursement :
//
//   - le service ne peut être ni oublié ni mal écrit, puisqu'il n'y a pas de champ ;
//   - le numéro du client ne peut pas être omis, puisqu'il est exigé SANS CONDITION ici, là
//     où [DemandeLien] doit en faire une règle que seule l'exécution vérifie.
//
// Pour tout le reste, voir les champs de même nom sur [DemandeLien], et notamment
// [DemandeLien.Operateur], qui ne part pas sur le fil, et [DemandeLien.NumeroClient], qui
// n'est jamais un numéro de carte.
type DemandeCarte struct {
	Reference             Reference
	Montant               Montant
	Operateur             Operateur
	NumeroClient          string
	CompteOperation       string
	CodeCallback          string
	CodeRedirectionSucces string
	CodeRedirectionEchec  string
	Frais                 Proprietaire
	FraisOperateur        Proprietaire
	Agent                 string
	Produit               string
	InfoLibre             string
}

// versLien rend la demande de lien équivalente, service imposé.
//
// Elle recopie champ par champ plutôt que d'incorporer [DemandeLien] : une structure
// incorporée exposerait son champ Service, et c'est exactement ce que ce type existe pour
// rendre inatteignable.
func (d DemandeCarte) versLien() DemandeLien {
	return DemandeLien{
		Service:               ServiceVisaMastercard,
		Reference:             d.Reference,
		Montant:               d.Montant,
		Operateur:             d.Operateur,
		NumeroClient:          d.NumeroClient,
		CompteOperation:       d.CompteOperation,
		CodeCallback:          d.CodeCallback,
		CodeRedirectionSucces: d.CodeRedirectionSucces,
		CodeRedirectionEchec:  d.CodeRedirectionEchec,
		Frais:                 d.Frais,
		FraisOperateur:        d.FraisOperateur,
		Agent:                 d.Agent,
		Produit:               d.Produit,
		InfoLibre:             d.InfoLibre,
	}
}

// Valider relève toutes les violations d'un coup, et n'émet aucune requête.
func (d DemandeCarte) Valider() error { return d.versLien().Valider() }

// CreerLien engage un lien de paiement, dans l'un de ses trois parcours.
//
// Pour une carte bancaire, préférer [Client.PayerParCarte], qui impose le service et rend
// le numéro du client obligatoire.
//
// ELLE NE RAPPORTE PAS UN PAIEMENT FAIT, ni même commencé. Elle rend une adresse où envoyer
// le client, et le statut qu'elle rapporte est celui de la CRÉATION du lien : voir
// [Lien.StatutCreation]. Le résultat de la transaction arrive par notification, sur le même
// webhook que le mobile money, ou se redemande par [Client.Statut] sous la RÉFÉRENCE.
//
// # LE CAS QUI COMPTE : UNE TENTATIVE QUI EXISTE DÉJÀ
//
// Si la référence a déjà servi (code 5000) ou si le lien a déjà été consommé (code 5002),
// cette méthode rend ALORS LES DEUX : un [Lien] portant la référence, sans adresse, et une
// erreur qui vérifie errors.Is(err, [ErrDejaInitiee]). L'appelant interroge alors le statut.
// Régénérer un lien sous une référence neuve, en revanche, ferait PAYER DEUX FOIS un client
// qui vient peut-être d'aller au bout du formulaire bancaire.
//
// Un lien expire, le code 6000 le dit, et sa durée de vie n'est publiée nulle part. Rien ne
// se déduit donc d'un silence : voir [AInterrogerApres] pour choisir son propre délai
// d'incertitude, celui de ce paquet étant calibré pour un parcours sans navigateur.
func (c *Client) CreerLien(ctx context.Context, d DemandeLien) (Lien, error) {
	if err := d.Valider(); err != nil {
		return Lien{}, err
	}
	return c.emettreLien(ctx, "lien", d)
}

// PayerParCarte encaisse par carte Visa ou Mastercard.
//
// # LE PARCOURS, EN TROIS TEMPS
//
//  1. cette méthode rend une adresse. La carte n'est PAS entrée ici : le formulaire
//     bancaire est hébergé par la passerelle, derrière cette adresse, et ce paquet ne
//     transporte aucune donnée de carte ;
//  2. le client est redirigé vers cette adresse, remplit le formulaire, et revient sur
//     l'une des deux pages de retour déclarées dans l'espace marchand. SON RETOUR SUR LA
//     PAGE DE SUCCÈS N'EST PAS UNE PREUVE DE PAIEMENT : c'est le client qui revient, pas
//     la passerelle qui conclut ;
//  3. le statut définitif arrive par notification, sur le même webhook que le mobile
//     money, ou se redemande par [Client.Statut] sous la RÉFÉRENCE.
//
// RIEN NE SE CRÉDITE AVANT LE TEMPS TROIS. Ni sur l'accusé rendu ici, ni sur le retour du
// client, quelle que soit la page.
//
// Voir [Client.CreerLien] pour le cas d'une tentative qui existe déjà, et
// [DemandeLien.Operateur] pour le compte d'opération qu'une carte demande.
func (c *Client) PayerParCarte(ctx context.Context, d DemandeCarte) (Lien, error) {
	if err := d.Valider(); err != nil {
		return Lien{}, err
	}
	return c.emettreLien(ctx, "carte", d.versLien())
}

// emettreLien compose et émet un lien, sur une demande DÉJÀ validée.
//
// Le nom d'opération vient de l'appelant, et non du point d'entrée : « carte » et « lien »
// partent tous deux au chemin link, comme le paiement et le remboursement partent tous deux
// au chemin rest. Ce qu'une ligne de journal doit rendre est l'INTENTION.
func (c *Client) emettreLien(ctx context.Context, operation string, d DemandeLien) (Lien, error) {
	compte, err := c.compteMarchand(strings.TrimSpace(d.CompteOperation), d.Operateur)
	if err != nil {
		return Lien{}, err
	}
	// La référence part TAILLÉE, et c'est la même valeur qui revient dans le [Lien] : voir
	// Reference.taillee pour la panne que cette ligne ferme.
	reference := d.Reference.taillee()

	// Les trois codes suivent la même règle : la surcharge de l'appel, puis le défaut des
	// réglages, puis le refus. La passerelle les donne pour facultatifs ou obligatoires
	// selon le champ, mais aucun des trois ne se laisse omettre en pratique : sans code de
	// callback, aucune notification n'est jamais émise, et sans page de retour le client
	// reste sur le formulaire de la passerelle, sans savoir où il en est.
	codeCallback := codeOuDefaut(d.CodeCallback, c.reglages.CodeCallback)
	succes := codeOuDefaut(d.CodeRedirectionSucces, c.reglages.CodeRedirectionSucces)
	echec := codeOuDefaut(d.CodeRedirectionEchec, c.reglages.CodeRedirectionEchec)

	// Les trois manques sont rassemblés, et non rendus un par un : corriger une
	// configuration demande de la voir en entier, et trois allers-retours pour trois
	// codes absents est précisément ce que la validation de ce paquet refuse ailleurs.
	//
	// Aucune LONGUEUR maximale n'est imposée aux deux codes de redirection. La passerelle
	// publie douze caractères pour le code de callback et ne publie rien pour ceux là :
	// fabriquer une borne ferait refuser côté client une valeur que le serveur accepte, ce
	// qui est la faute exacte contre laquelle texteBorne argumente. Les codes 1003 et 1004
	// disent la vérité, et une borne s'ajoutera en une ligne le jour où elle sera publiée.
	var manques []error
	if codeCallback == "" {
		manques = append(manques, ChampInvalideError{
			Champ:  "callback_url_code",
			Raison: "vide, or sans lui aucune notification n est emise",
		})
	}
	if succes == "" {
		manques = append(manques, ChampInvalideError{
			Champ:  "success_redirection_url_code",
			Raison: "vide, a declarer dans l espace marchand puis dans Reglages ou dans la demande",
		})
	}
	if echec == "" {
		manques = append(manques, ChampInvalideError{
			Champ:  "failed_redirection_url_code",
			Raison: "vide, a declarer dans l espace marchand puis dans Reglages ou dans la demande",
		})
	}
	if err := errors.Join(manques...); err != nil {
		return Lien{}, err
	}

	requeteCorps := requeteLien{
		Agent:          d.Agent,
		Montant:        d.Montant,
		Produit:        d.Produit,
		Reference:      reference,
		Service:        d.Service,
		CodeCallback:   codeCallback,
		NumeroClient:   strings.TrimSpace(d.NumeroClient),
		CompteMarchand: compte,
		// PAYMENT sans condition. La passerelle accepte aussi GIVE_CHANGE sur ce chemin,
		// soit un remboursement par lien, qui n'est pas du périmètre de ces deux méthodes :
		// [Client.Rembourser] le fait de façon SYNCHRONE, sans page de retour ni
		// notification à attendre. Exposer le choix ici inviterait à produire un lien de
		// remboursement dont personne n'a éprouvé le parcours.
		TypeOperation:     OperationPaiement,
		Frais:             d.Frais.ouDefaut(c.opts.proprietaireFrais),
		InfoLibre:         d.InfoLibre,
		RedirectionEchec:  echec,
		RedirectionSucces: succes,
	}

	// UN SEUL des deux noms du second étage de frais part. Voir
	// [AvecFraisOperateurEnOperatorOwnerCharge].
	fraisOperateur := d.FraisOperateur.ouDefaut(c.opts.proprietaireFraisOperateur)
	if c.opts.fraisOperateurPostman {
		requeteCorps.FraisOperateurPostman = fraisOperateur
	} else {
		requeteCorps.FraisOperateur = fraisOperateur
	}

	corps, err := json.Marshal(requeteCorps)
	if err != nil {
		return Lien{}, ChampInvalideError{Champ: "corps", Raison: "serialisation json impossible"}
	}

	res, err := c.emettre(ctx, requete{
		operation:   operation,
		operateur:   d.Operateur,
		endpoint:    epLien,
		methode:     http.MethodPost,
		corps:       corps,
		typeContenu: "application/json",
		entetes:     map[string]string{enteteMediaTypeCallback: c.opts.mediaTypeCallback},
	})
	if err != nil {
		// La tentative existe déjà, sous sa référence ou sous son lien : l'appelant doit
		// pouvoir interroger le statut, donc la référence revient avec l'erreur.
		if errors.Is(err, ErrDejaInitiee) {
			return Lien{Reference: reference}, err
		}
		return Lien{}, err
	}

	return lireLien(operation, d.Operateur, res, reference)
}

// codeOuDefaut rend le code imposé, taillé, ou le défaut quand il est absent.
func codeOuDefaut(impose, defaut string) string {
	if taille := strings.TrimSpace(impose); taille != "" {
		return taille
	}
	return defaut
}

// lireLien décode un accusé de lien, et REFUSE une adresse inutilisable.
//
// Le refus est l'asymétrie assumée avec lireInitiation, qui accepte un accusé de paiement
// sans identifiant. La raison tient en une phrase : un paiement sans identifiant est
// RÉELLEMENT ENGAGÉ et se réconcilie par sa référence, tandis qu'un lien sans adresse
// présentable n'a rien à montrer au client, donc il ne se passera jamais rien. Rendre un
// succès dans ce cas laisserait un marchand attendre une notification qui ne viendra pas.
//
// La référence revient MÊME dans ce refus : la passerelle a pu consommer la tentative avant
// de répondre de travers, et l'appelant doit pouvoir aller le vérifier.
//
// # UN REFUS PEUT ARRIVER SOUS UN CODE DE SUCCÈS
//
// L'API de transaction répond déjà « status PENDING, status_code 200 » dans un corps de
// 2xx : le statut et le code applicatif vivent donc dans le corps, et rien ne garantit que
// ce chemin réserve ses refus aux codes 4xx. Un corps sans adresse mais porteur d'un code
// connu est donc classé par ce code, et non rangé en vrac derrière
// [ErrReponseIllisible] : le discriminant de cette passerelle est le code applicatif, et un
// 3012 rendu « charge utile inexploitable » ne dirait pas au marchand d'aller rallumer son
// URL de redirection.
//
// Le classement est CONDITIONNÉ à un code que motifDuCode reconnaît, et ce n'est pas un
// détail : classer un 2xx à l'aveugle le ferait passer par motifDuStatut, dont le défaut est
// [ErrIndisponible], donc REJOUABLE. Une boucle de réconciliation régénérerait alors des
// liens sur une faute de configuration qu'aucun rejeu ne répare.
func lireLien(operation string, operateur Operateur, res resultat, reference Reference) (Lien, error) {
	var rep reponseLien
	if err := json.Unmarshal(res.corps, &rep); err != nil {
		return Lien{Reference: reference}, Erreur{
			Operation: operation, Operateur: operateur,
			HTTP: res.statut, motif: ErrReponseIllisible, cause: err,
		}
	}

	adresse, _ := texte(rep.URL)
	if err := adresseUtilisable(adresse); err != nil {
		if refus, connu := refusDansUnSucces(operation, operateur, res); connu {
			return Lien{Reference: reference}, refus
		}
		return Lien{Reference: reference}, Erreur{
			Operation: operation, Operateur: operateur,
			HTTP: res.statut, motif: ErrReponseIllisible, cause: err,
		}
	}

	statut, _ := texte(rep.Statut)
	message, _ := texte(rep.Message)
	return Lien{
		URL:            adresse,
		Reference:      reference,
		StatutCreation: LireStatut(statut),
		Code:           rep.code(),
		Message:        tronquer(message),
	}, nil
}

// refusDansUnSucces rend le refus que porte un corps de succès, et dit s'il en portait un.
//
// Elle ne s'appuie sur [classer] que lorsque le corps livre un code que motifDuCode sait
// ranger. Sans cette garde, un corps de 2xx sans code reconnaissable retomberait sur
// motifDuStatut, qui rend [ErrIndisponible] pour tout ce qui n'est pas un 4xx, donc un refus
// RÉPUTÉ REJOUABLE là où il n'y a rien à rejouer.
func refusDansUnSucces(operation string, operateur Operateur, res resultat) (Erreur, bool) {
	e := classer(operation, operateur, res.statut, res.corps)
	if e.Code == 0 || motifDuCode(e.Code) == nil {
		return Erreur{}, false
	}
	return e, true
}

// adresseUtilisable refuse une adresse qu'on ne peut pas présenter à un client.
//
// Le contrôle de schéma n'est pas une coquetterie : l'adresse rendue par la passerelle finit
// dans un en-tête de redirection ou dans un attribut de lien, chez l'appelant. Une valeur de
// schéma javascript ou data recopiée là exécuterait du script dans la page du marchand, et
// une valeur relative ou sans hôte enverrait le client sur une route du marchand qui
// n'existe pas.
//
// CE QU'ELLE NE FAIT PAS, ET IL FAUT LE SAVOIR : elle n'épingle AUCUN hôte. Une adresse
// https vers un domaine quelconque passe, parce que le domaine de la page de paiement n'est
// publié nulle part comme une valeur fixe, et qu'y coder checkout.mypvit.pro ferait tomber
// l'encaissement le jour où la passerelle en change. La confiance accordée ici est donc
// celle du lien TLS vers la passerelle, et pas davantage. Un appelant qui veut la restreindre
// a l'adresse en clair dans [Lien.URL] et peut la juger avant de rediriger.
func adresseUtilisable(adresse string) error {
	adresse = strings.TrimSpace(adresse)
	if adresse == "" {
		return errors.New("la reponse ne porte aucune adresse de paiement")
	}
	analysee, err := url.Parse(adresse)
	if err != nil {
		return errors.New("adresse de paiement inanalysable: " + borner(adresse))
	}
	if analysee.Scheme != "http" && analysee.Scheme != "https" {
		return errors.New("adresse de paiement de schema inattendu, attendu http ou https: " + borner(adresse))
	}
	if analysee.Host == "" {
		return errors.New("adresse de paiement sans hote: " + borner(adresse))
	}
	// Une adresse de paiement ne porte JAMAIS d'identifiants, et celle qui en porte est une
	// anomalie à deux titres : la forme https://checkout.mypvit.pro@ailleurs.test/ a pour
	// hôte réel ailleurs.test, ce qu'un client qui survole la barre d'adresse ne verra pas,
	// et des identifiants recopiés dans un gabarit finiraient dans les journaux du marchand.
	// Le refus ne peut rien casser : aucune réponse légitime n'en met.
	if analysee.User != nil {
		return errors.New("adresse de paiement portant des identifiants: " + borner(adresse))
	}
	return nil
}
