package pvit

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

// Version est la version de ce paquet, telle qu'elle part dans l'en-tête d'agent.
const Version = "0.1.1"

// BaseURLProduction est l'hôte de la passerelle.
//
// IL N'Y A PAS D'HÔTE DE BAC À SABLE SÉPARÉ. Ce qui distingue les deux environnements,
// c'est le compte d'opération et les codes d'URL, et le code 1001 sanctionne le mélange
// des deux. Donner ici une URL de test n'aurait donc aucun sens.
//
// La valeur peut porter le suffixe de version, comme la documentation l'écrit : [Nouveau]
// le retire, parce que le préfixe se décide point d'entrée par point d'entrée.
const BaseURLProduction = "https://api.mypvit.pro"

// Les défauts de chaque option. Tous surchargeables, aucun obligatoire.
const (
	// delaiConnexionParDefaut borne l'établissement de la connexion.
	delaiConnexionParDefaut = 5 * time.Second

	// delaiTotalParDefaut borne l'appel entier.
	//
	// Quinze secondes, et non les huit qui suffisent à une API de messagerie : le
	// paiement déclenche une invite de code secret chez l'opérateur, et la latence d'un
	// réseau mobile money n'est pas celle d'un service web. Trop court, on abandonne une
	// transaction que la passerelle est en train d'accepter, et il faut alors la
	// réconcilier pour rien.
	delaiTotalParDefaut = 15 * time.Second

	// margeRotationParDefaut avance le renouvellement de la clé.
	//
	// La clé vit 3 600 secondes : dix minutes de marge la renouvellent vers la
	// cinquantième minute, ce que la documentation demande. Sans marge, une clé qui
	// expire pendant le vol d'une requête provoque un refus à chaque paiement.
	margeRotationParDefaut = 10 * time.Minute

	// dureeDeVieParDefaut sert quand la passerelle n'annonce pas de durée lisible.
	dureeDeVieParDefaut = time.Hour

	// dureeDeVieMax borne une durée annoncée invraisemblable. Au delà, on a mal lu, et
	// le repli vaut mieux qu'une clé réputée valide pendant un mois.
	dureeDeVieMax = 24 * time.Hour

	// tailleAnneauParDefaut est le nombre de clés gardées pour vérifier une signature.
	tailleAnneauParDefaut = 3

	// rejeuAuthentificationParDefaut est le nombre de rejeux après renouvellement.
	//
	// Un seul. Au delà, on s'acharne sur une clé que la passerelle refuse pour une autre
	// raison, et le code 10008, qui sanctionne l'échec d'envoi de clé, guette.
	rejeuAuthentificationParDefaut = 1

	// mediaTypeCallbackParDefaut est le format demandé pour les notifications.
	mediaTypeCallbackParDefaut = "application/json"
)

// Reglages porte ce qui change d'un déploiement à l'autre.
//
// La forme de ce type est dictée par un fait de la passerelle : les codes d'URL vont par
// API, les comptes vont par opérateur. Voir [CodesURL] et [Compte].
type Reglages struct {
	// BaseURL est l'hôte de la passerelle. Vide, il prend [BaseURLProduction].
	BaseURL string

	// Codes porte les onze segments opaques, un par API.
	Codes CodesURL

	// CodeCallback désigne l'API que ce projet expose pour recevoir les notifications.
	//
	// Elle s'enregistre dans l'espace marchand, qui refuse localhost et 127.0.0.1. Sans
	// elle, un paiement n'émet jamais de notification : le champ est facultatif ici, et
	// obligatoire au moment d'un paiement, où [DemandePaiement.CodeCallback] peut aussi
	// la porter.
	CodeCallback string

	// Comptes porte un compte d'opération par famille d'opérateur.
	//
	// Le parcours marchand de la passerelle demande un compte par famille, et le code
	// 3017 sanctionne l'appariement incorrect. Au moins un compte est exigé, sauf si le
	// client ne sert que les trois API qui n'ont pas besoin de clé secrète.
	Comptes map[Operateur]Compte
}

// Compte est un compte d'opération, avec de quoi obtenir sa clé secrète.
//
// La clé vit 3 600 secondes et se renouvelle par un mot de passe dédié, défini dans le
// menu des API de l'espace marchand. Une clé détenue à la main ne peut donc pas être
// permanente, et ce type accepte les deux, dont AU MOINS UN :
//
//   - les deux : la clé d'amorce sert jusqu'au premier refus, puis la rotation prend le
//     relais. C'est le cas nominal ;
//   - le mot de passe seul : une clé est obtenue au premier appel ;
//   - la clé seule : elle sert jusqu'à sa péremption, après quoi l'appel rend
//     [ErrRotationImpossible], qui nomme le geste correctif ;
//   - ni l'un ni l'autre : [Nouveau] refuse, avant toute requête.
type Compte struct {
	// CodeOperation est le code du compte, trente caractères au plus.
	CodeOperation string

	// CleAmorce est une clé secrète déjà en main, utilisable immédiatement.
	CleAmorce string

	// ExpirationAmorce est l'instant où la clé d'amorce cesse d'être valable, s'il est
	// connu. Nulle, la clé est tenue pour valable jusqu'au premier refus.
	ExpirationAmorce time.Time

	// MotDePasse est le mot de passe de l'API de rotation. CE N'EST PAS celui du compte
	// marchand.
	MotDePasse string
}

// compte est l'état vivant d'un compte d'opération.
//
// Un pointeur, jamais une valeur : il porte un [sync.Mutex], qui ne se copie pas.
type compte struct {
	reglage Compte

	// mu garde la clé, et le verrou est tenu PENDANT le renouvellement.
	//
	// Sans cela, un démarrage à froid sous trafic déclenche autant de renouvellements que
	// de requêtes, et si la passerelle n'admet qu'une clé active par compte, ces
	// renouvellements s'invalident mutuellement et aucun n'aboutit. Le contrôle du cache
	// et le renouvellement ont lieu sous le MÊME verrou : deux paiements qui essuient un
	// refus en même temps se présentent tous les deux, le premier renouvelle, et le
	// second trouve la clé neuve au lieu de renouveler à son tour.
	//
	// Un verrou PAR COMPTE, et non un pour le client : renouveler la clé d'Airtel ne doit
	// rien faire à celle de Moov.
	mu     sync.Mutex
	cle    string
	expire time.Time

	// anneau garde les clés précédentes, la plus récente d'abord, pour vérifier la
	// signature d'une notification de reporting partie avant la dernière rotation.
	anneau []string
}

// options porte les réglages de comportement, tous assortis d'un défaut qui tient seul.
type options struct {
	clientHTTP                 *http.Client
	horloge                    func() time.Time
	delaiConnexion             time.Duration
	delaiTotal                 time.Duration
	agentUtilisateur           string
	margeRotation              time.Duration
	dureeDeVie                 time.Duration
	renouvellementEnQuery      bool
	tailleAnneau               int
	rejeuAuthentification      int
	service                    Service
	mediaTypeCallback          string
	proprietaireFrais          Proprietaire
	proprietaireFraisOperateur Proprietaire
	tailleMaxCallback          int64
	fenetreSignature           time.Duration

	// delaisImposes et transportImpose se souviennent de ce que l'appelant a demandé, pour
	// que [Nouveau] refuse une combinaison dont une moitié serait sans effet.
	delaisImposes   bool
	transportImpose bool
}

// optionsParDefaut rend la configuration de comportement d'un client sans option.
func optionsParDefaut() options {
	return options{
		horloge:                    time.Now,
		delaiConnexion:             delaiConnexionParDefaut,
		delaiTotal:                 delaiTotalParDefaut,
		agentUtilisateur:           "pvit-go/" + Version,
		margeRotation:              margeRotationParDefaut,
		dureeDeVie:                 dureeDeVieParDefaut,
		tailleAnneau:               tailleAnneauParDefaut,
		rejeuAuthentification:      rejeuAuthentificationParDefaut,
		service:                    ServiceRestful,
		mediaTypeCallback:          mediaTypeCallbackParDefaut,
		proprietaireFrais:          ProprietaireClient,
		proprietaireFraisOperateur: ProprietaireClient,
		tailleMaxCallback:          TailleMaxCallback,
		fenetreSignature:           FenetreSignature,
	}
}

// Option règle un détail du comportement du client.
//
// Aucune n'est obligatoire, et chacune VALIDE sa valeur : une option absurde empêche
// [Nouveau] d'aboutir au lieu de se replier en silence sur le défaut. Un repli muet ferait
// tourner le service avec un réglage que personne n'a voulu, et c'est précisément le genre
// de surprise qu'on découvre en production.
type Option func(*options) error

// AvecClientHTTP impose le client HTTP, délais compris.
//
// Elle sert à pointer un serveur simulé en test, ou une passerelle sortante à adresse
// fixe en production, lorsque le filtrage entrant de la passerelle l'exige.
//
// Elle est INCOMPATIBLE avec [AvecDelais] : le transport fourni porte déjà ses délais, et
// accepter les deux ferait croire à l'appelant qu'il a borné ce qu'il n'a pas borné.
func AvecClientHTTP(h *http.Client) Option {
	return func(o *options) error {
		if h == nil {
			return errors.New("AvecClientHTTP: client nil")
		}
		o.clientHTTP = h
		o.transportImpose = true
		return nil
	}
}

// AvecHorloge impose la source du temps.
//
// L'expiration d'une clé s'éprouve ainsi sans attendre, ce que les règles de test
// interdisent de faire par une mise en sommeil.
func AvecHorloge(maintenant func() time.Time) Option {
	return func(o *options) error {
		if maintenant == nil {
			return errors.New("AvecHorloge: fonction nil")
		}
		o.horloge = maintenant
		return nil
	}
}

// AvecDelais impose les deux délais du client HTTP par défaut.
//
// Les deux sont obligatoires et strictement positifs. Sans délai, une passerelle lente
// n'est pas un appel lent : c'est un fil de traitement immobilisé, puis tous les fils.
//
// Elle est INCOMPATIBLE avec [AvecClientHTTP], qui fournit son propre transport et donc ses
// propres délais. Les accepter toutes deux reviendrait à ignorer celle-ci en silence, et
// l'appelant croirait avoir borné ce qu'il n'a pas borné.
func AvecDelais(connexion, total time.Duration) Option {
	return func(o *options) error {
		if connexion <= 0 || total <= 0 {
			return fmt.Errorf("AvecDelais: delais strictement positifs attendus, recu %v et %v", connexion, total)
		}
		if connexion > total {
			return fmt.Errorf("AvecDelais: delai de connexion %v superieur au delai total %v", connexion, total)
		}
		o.delaiConnexion, o.delaiTotal = connexion, total
		o.delaisImposes = true
		return nil
	}
}

// AvecAgentUtilisateur impose l'en-tête d'agent, pour se reconnaître dans les journaux de
// la passerelle.
func AvecAgentUtilisateur(agent string) Option {
	return func(o *options) error {
		agent = strings.TrimSpace(agent)
		if agent == "" {
			return errors.New("AvecAgentUtilisateur: agent vide")
		}
		o.agentUtilisateur = agent
		return nil
	}
}

// AvecMargeRotation avance le renouvellement de la clé secrète d'autant.
//
// Elle doit rester inférieure à la durée de vie annoncée par la passerelle, sans quoi
// chaque appel renouvellerait, ce que le code 10008 sanctionne.
func AvecMargeRotation(marge time.Duration) Option {
	return func(o *options) error {
		if marge < 0 {
			return fmt.Errorf("AvecMargeRotation: marge negative %v", marge)
		}
		if marge >= dureeDeVieMax {
			return fmt.Errorf("AvecMargeRotation: marge %v superieure a la duree de vie maximale %v", marge, dureeDeVieMax)
		}
		o.margeRotation = marge
		return nil
	}
}

// AvecDureeDeVieParDefaut impose la durée de vie retenue quand la passerelle n'en annonce
// pas de lisible.
func AvecDureeDeVieParDefaut(duree time.Duration) Option {
	return func(o *options) error {
		if duree <= 0 || duree > dureeDeVieMax {
			return fmt.Errorf("AvecDureeDeVieParDefaut: duree %v hors de la plage attendue, jusqu a %v", duree, dureeDeVieMax)
		}
		o.dureeDeVie = duree
		return nil
	}
}

// AvecRenouvellementEnQuery envoie les paramètres de rotation dans l'URL plutôt que dans
// un corps de formulaire.
//
// Le défaut est le corps, parce que la documentation d'intégration le prescrit et qu'il ne
// sème pas le mot de passe dans les journaux d'accès et les proxys. Mais la collection de
// requêtes publiée, elle, le met dans l'URL, et personne n'a tranché : cette option existe
// pour le jour où la réponse arrive, et évite alors une livraison.
func AvecRenouvellementEnQuery() Option {
	return func(o *options) error {
		o.renouvellementEnQuery = true
		return nil
	}
}

// AvecTailleAnneau impose le nombre de clés gardées pour vérifier une signature.
//
// Les notifications de reporting se rejouent jusqu'à six heures, et la clé tourne chaque
// heure : trois clés couvrent trois heures, ce qui est insuffisant en théorie. Allonger
// l'anneau garde en mémoire des clés vivantes plus longtemps, ce qui est un arbitrage de
// sécurité, et c'est pourquoi la valeur est bornée.
func AvecTailleAnneau(taille int) Option {
	return func(o *options) error {
		const tailleAnneauMax = 8
		if taille < 1 || taille > tailleAnneauMax {
			return fmt.Errorf("AvecTailleAnneau: taille %d hors de la plage 1 a %d", taille, tailleAnneauMax)
		}
		o.tailleAnneau = taille
		return nil
	}
}

// AvecRejeuAuthentification impose le nombre de rejeux après un renouvellement de clé.
//
// Zéro désactive le rejeu, ce qui convient à un appelant qui tient sa propre boucle.
func AvecRejeuAuthentification(rejeux int) Option {
	return func(o *options) error {
		const rejeuMax = 3
		if rejeux < 0 || rejeux > rejeuMax {
			return fmt.Errorf("AvecRejeuAuthentification: nombre %d hors de la plage 0 a %d", rejeux, rejeuMax)
		}
		o.rejeuAuthentification = rejeux
		return nil
	}
}

// AvecService impose le parcours de paiement demandé à la passerelle.
//
// Le défaut est [ServiceRestful], le seul parcours sans navigateur, donc le seul qui
// tienne à un étal.
func AvecService(s Service) Option {
	return func(o *options) error {
		switch s {
		case ServiceRestful, ServiceWeb, ServiceRestlink, ServiceVisaMastercard:
			o.service = s
			return nil
		default:
			return fmt.Errorf("AvecService: service %q inconnu", s)
		}
	}
}

// AvecMediaTypeCallback impose le format des notifications de transaction.
//
// La passerelle accepte le JSON, qui est le défaut, et le XML. Ce réglage ne vaut que pour
// les paiements : un remboursement est synchrone et n'émet aucune notification.
func AvecMediaTypeCallback(mediaType string) Option {
	return func(o *options) error {
		switch mediaType {
		case "application/json", "application/xml":
			o.mediaTypeCallback = mediaType
			return nil
		default:
			return fmt.Errorf("AvecMediaTypeCallback: type %q non supporte, attendu application/json ou application/xml", mediaType)
		}
	}
}

// AvecProprietaireFrais impose qui supporte les frais de la passerelle par défaut.
//
// C'est une décision de produit et non une préférence technique : en charge du marchand,
// l'encaissement rapporte moins que le tarif affiché ; en charge du client, le payeur
// débourse plus que ce tarif. Le défaut de ce paquet est le client, et chaque appel peut
// le surcharger par [DemandePaiement.Frais].
func AvecProprietaireFrais(p Proprietaire) Option {
	return func(o *options) error {
		if !p.valide() {
			return fmt.Errorf("AvecProprietaireFrais: valeur %q inconnue, attendu CUSTOMER ou MERCHANT", p)
		}
		o.proprietaireFrais = p
		return nil
	}
}

// AvecProprietaireFraisOperateur impose qui supporte les frais de l'opérateur par défaut.
//
// Il y a DEUX étages de frais, pilotés par deux champs distincts, et le calculateur de la
// passerelle n'en montre qu'un. Celui-ci est le second.
func AvecProprietaireFraisOperateur(p Proprietaire) Option {
	return func(o *options) error {
		if !p.valide() {
			return fmt.Errorf("AvecProprietaireFraisOperateur: valeur %q inconnue, attendu CUSTOMER ou MERCHANT", p)
		}
		o.proprietaireFraisOperateur = p
		return nil
	}
}

// AvecTailleMaxCallback borne le corps d'une notification entrante.
func AvecTailleMaxCallback(octets int64) Option {
	return func(o *options) error {
		const minimum = 1 << 10
		if octets < minimum {
			return fmt.Errorf("AvecTailleMaxCallback: borne %d octets trop basse, minimum %d", octets, minimum)
		}
		o.tailleMaxCallback = octets
		return nil
	}
}

// AvecFenetreSignature impose la tolérance d'horloge du webhook de reporting.
func AvecFenetreSignature(fenetre time.Duration) Option {
	return func(o *options) error {
		if fenetre <= 0 {
			return fmt.Errorf("AvecFenetreSignature: fenetre %v non strictement positive", fenetre)
		}
		o.fenetreSignature = fenetre
		return nil
	}
}

// Client parle à la passerelle, et garde la clé secrète de chaque compte d'opération.
//
// Il est sûr d'emploi concurrent, et il le doit : il est appelé depuis des requêtes HTTP.
// Il ne lance AUCUNE goroutine, ne journalise rien, et ne lit pas l'environnement : la
// rotation de clé est paresseuse, sous verrou, et [Client.Rafraichir] est là pour qui veut
// la préchauffer depuis son propre rythme.
type Client struct {
	http       *http.Client
	maintenant func() time.Time
	reglages   Reglages
	opts       options

	// comptes est construite une fois par [Nouveau] et JAMAIS mutée ensuite. C'est ce qui
	// permet de s'en passer de verrou : chaque entrée garde le sien.
	comptes map[Operateur]*compte
}

// Nouveau câble le client et valide tout ce qu'il peut valider sans réseau.
//
// Il n'émet AUCUNE requête. Se connecter pour découvrir qu'une clé est vide enverrait des
// identifiants vides sur le réseau, et le journal de la passerelle en garderait la trace.
// C'est aussi ce qui permet à un binaire de refuser de démarrer, plutôt que d'échouer
// devant le premier commerçant qui attend.
//
// Les manques sont RASSEMBLÉS et triés : au démarrage, on veut la liste complète de ce qui
// est absent, pas le premier problème suivi de quatre redémarrages.
func Nouveau(r Reglages, options ...Option) (*Client, error) {
	opts := optionsParDefaut()
	var manques []error
	for _, appliquer := range options {
		if appliquer == nil {
			manques = append(manques, errors.New("option nil"))
			continue
		}
		if err := appliquer(&opts); err != nil {
			manques = append(manques, err)
		}
	}

	if opts.delaisImposes && opts.transportImpose {
		manques = append(manques, errors.New(
			"AvecDelais et AvecClientHTTP ensemble: le transport fourni porte ses propres delais"))
	}

	r.BaseURL = normaliserBaseURL(r.BaseURL)
	r.CodeCallback = strings.TrimSpace(r.CodeCallback)
	r.Codes = normaliserCodes(r.Codes)
	r.Comptes = normaliserComptes(r.Comptes)

	manques = append(manques, manquesDesReglages(r)...)

	slices.SortFunc(manques, func(a, b error) int { return strings.Compare(a.Error(), b.Error()) })
	if err := errors.Join(manques...); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrConfiguration, err)
	}

	c := &Client{
		http:       opts.clientHTTP,
		maintenant: opts.horloge,
		reglages:   r,
		opts:       opts,
		comptes:    make(map[Operateur]*compte, len(r.Comptes)),
	}
	if c.http == nil {
		c.http = clientParDefaut(opts.delaiConnexion, opts.delaiTotal)
	}
	for operateur, reglage := range r.Comptes {
		vivant := &compte{reglage: reglage, cle: reglage.CleAmorce}
		if !reglage.ExpirationAmorce.IsZero() {
			vivant.expire = reglage.ExpirationAmorce
		}
		c.comptes[operateur] = vivant
	}
	return c, nil
}

// normaliserBaseURL retire le suffixe de version et la barre finale.
//
// Le suffixe tombe parce que la documentation donne le serveur comme se terminant par la
// version, alors que le préfixe se décide point d'entrée par point d'entrée : sans ce
// retrait, un copier-coller de la documentation produirait des chemins à double version.
func normaliserBaseURL(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return BaseURLProduction
	}
	return strings.TrimSuffix(base, prefixeVersion)
}

// normaliserCodes retire les espaces autour de chaque code d'URL.
func normaliserCodes(c CodesURL) CodesURL {
	for _, champ := range []*string{
		&c.Secret, &c.Paiement, &c.Statut, &c.Frais, &c.Solde,
		&c.KYC, &c.Pays, &c.Operateurs, &c.Lien, &c.QRCode, &c.Rapport,
	} {
		*champ = strings.TrimSpace(*champ)
	}
	return c
}

// normaliserComptes retire les espaces autour de chaque valeur de compte.
//
// Elle tourne AVANT la validation, et c'est ce qui empêche les deux de divergir. Avant ce
// correctif, la validation taillait les espaces pour JUGER, puis la valeur brute partait sur
// le réseau : une clé secrète recopiée depuis l'espace marchand avec un retour à la ligne
// produisait un en-tête HTTP invalide, et une erreur de transport que rien n'expliquait. Un
// code de compte avec une espace finale, lui, partait dans la charge utile et se faisait
// refuser pour un appariement incorrect.
func normaliserComptes(comptes map[Operateur]Compte) map[Operateur]Compte {
	if comptes == nil {
		return nil
	}
	propres := make(map[Operateur]Compte, len(comptes))
	for operateur, compte := range comptes {
		compte.CodeOperation = strings.TrimSpace(compte.CodeOperation)
		compte.CleAmorce = strings.TrimSpace(compte.CleAmorce)
		compte.MotDePasse = strings.TrimSpace(compte.MotDePasse)
		propres[Operateur(strings.TrimSpace(string(operateur)))] = compte
	}
	return propres
}

// manquesDesReglages relève tout ce qui empêche ce client de servir un paiement.
//
// La sévérité est graduée, et c'est voulu. Ce qui est toujours nécessaire est exigé ici ;
// les codes d'URL des API accessoires sont vérifiés au moment de l'appel, par
// [Client.urlDe], parce qu'exiger les onze ferait refuser de démarrer un projet qui n'en
// utilise que trois.
func manquesDesReglages(r Reglages) []error {
	var manques []error

	if r.CodeCallback != "" && len(r.CodeCallback) > maxCodeCallback {
		manques = append(manques, fmt.Errorf("code de callback: %d caracteres, maximum %d",
			len(r.CodeCallback), maxCodeCallback))
	}

	if len(r.Comptes) == 0 {
		manques = append(manques, errors.New("aucun compte d operation"))
	}

	rotationVoulue := false
	for operateur, compte := range r.Comptes {
		if operateur == "" {
			manques = append(manques, errors.New("un compte est indexe par un operateur vide"))
			continue
		}
		// Les valeurs arrivent déjà taillées par normaliserComptes : tailler une
		// seconde fois ici ferait croire que ce n'est pas le cas, et laisserait la porte
		// ouverte à la divergence que ce correctif a fermée.
		code := compte.CodeOperation
		switch {
		case code == "":
			manques = append(manques, fmt.Errorf("compte %s: code d operation absent", operateur))
		case len(code) > maxCodeOperation:
			manques = append(manques, fmt.Errorf("compte %s: code d operation de %d caracteres, maximum %d",
				operateur, len(code), maxCodeOperation))
		}
		if compte.CleAmorce == "" && compte.MotDePasse == "" {
			manques = append(manques, fmt.Errorf("compte %s: ni cle d amorce ni mot de passe de rotation", operateur))
		}
		if compte.MotDePasse != "" {
			rotationVoulue = true
		}
	}

	if r.Codes.Paiement == "" {
		manques = append(manques, errors.New("code url manquant: rest"))
	}
	if r.Codes.Statut == "" {
		manques = append(manques, errors.New("code url manquant: status"))
	}
	if rotationVoulue && r.Codes.Secret == "" {
		manques = append(manques, errors.New("code url manquant: renew-secret, exige par un mot de passe de rotation"))
	}

	return manques
}

// clientParDefaut construit un client HTTP aux deux délais bornés.
//
// Le même client sert tous les points d'entrée : il mutualise les connexions, et il permet
// à un unique serveur simulé d'intercepter la suite des appels dans l'ordre, ce qui est ce
// qui rend les tests crédibles.
func clientParDefaut(connexion, total time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: connexion}).DialContext
	return &http.Client{Timeout: total, Transport: transport}
}

// CompteDe rend le compte d'opération configuré pour un opérateur.
//
// C'est ce qui permet à un appelant de vérifier sa configuration avant d'encaisser, plutôt
// que de découvrir au comptoir que l'opérateur du client n'a pas de compte.
func (c *Client) CompteDe(operateur Operateur) (Compte, bool) {
	vivant, ok := c.comptes[operateur]
	if !ok {
		return Compte{}, false
	}
	return vivant.reglage, true
}

// OperateursConfigures rend les opérateurs pour lesquels un compte existe.
//
// L'ordre est stable, pour qu'un message de diagnostic ne change pas d'une exécution à
// l'autre.
func (c *Client) OperateursConfigures() []Operateur {
	liste := make([]Operateur, 0, len(c.comptes))
	for operateur := range c.comptes {
		liste = append(liste, operateur)
	}
	slices.Sort(liste)
	return liste
}
