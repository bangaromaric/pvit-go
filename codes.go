package pvit

import "fmt"

// CodesURL porte le segment opaque que la passerelle place dans le chemin de chaque API.
//
// IL Y EN A ONZE, UN PAR API, et c'est le fait de configuration le plus surprenant de
// cette passerelle. Ils se récupèrent dans l'espace marchand, ils diffèrent d'un
// environnement à l'autre, et ils n'ont rien à voir avec le compte d'opération : un jeu de
// codes sert tous les comptes, tandis que le code de compte, la clé et le mot de passe
// vont par trois et se répètent pour chaque famille d'opérateur. Voir [Compte].
//
// Tous ne sont pas exigés au démarrage. [Nouveau] réclame ceux dont le chemin du paiement
// a besoin, et les autres sont vérifiés au moment de l'appel : un projet qui n'utilise que
// trois API n'a pas à réunir les onze codes pour démarrer.
type CodesURL struct {
	// Secret est le code de l'API de renouvellement de la clé secrète.
	Secret string

	// Paiement est le code de l'API de transaction, qui sert le paiement et le
	// remboursement.
	Paiement string

	// Statut est le code de l'API d'interrogation d'une transaction.
	Statut string

	// Frais est le code du calculateur de frais.
	Frais string

	// Solde est le code de l'API de solde du compte d'opération.
	Solde string

	// KYC est le code de l'API d'information sur un client.
	KYC string

	// Pays est le code de l'API de liste des pays.
	Pays string

	// Operateurs est le code de l'API de liste des opérateurs, celle qui fait autorité
	// sur la correspondance entre un préfixe de numéro et un opérateur.
	Operateurs string

	// Lien est le code de l'API de lien de paiement, celle qui porte aussi LE PAIEMENT
	// PAR CARTE BANCAIRE. Voir [Client.CreerLien] et [Client.PayerParCarte].
	//
	// Il est vérifié au moment de l'appel, et non au démarrage : un projet qui n'encaisse
	// qu'en mobile money n'a pas à le réunir.
	Lien string

	// QRCode est le code de l'API de génération de QR code. Hors du périmètre des
	// méthodes typées de ce paquet, joignable par [Client.Appeler].
	QRCode string

	// Rapport est le code de l'API d'export. Même remarque que pour le QR code.
	Rapport string
}

// endpoint décrit un point d'entrée de la passerelle.
//
// Cette table est LA SEULE SOURCE DE VÉRITÉ du paquet sur deux faits qui ne se devinent
// pas et qui coûtent une journée chacun : quels chemins portent le préfixe de version, et
// lesquels reçoivent la clé secrète. Les tenir ici, en une table qui se relit d'un coup
// d'œil, vaut mieux que de les répéter dans onze fonctions d'appel.
type endpoint struct {
	// nom est le dernier segment du chemin, et sert aussi de nom d'opération dans les
	// messages d'erreur.
	nom string

	// code rend le code d'URL de cette API.
	code func(CodesURL) string

	// versionne dit si le chemin porte le préfixe de version.
	//
	// IL N'EST PAS GLOBAL : la plupart des chemins sont sous /v2, mais status, balance et
	// link ne le sont pas. Le serveur annoncé par la documentation porte pourtant le /v2,
	// ce qui rend la faute très facile à commettre. Le lien de paiement pousse l'écart
	// jusqu'au bout : chemin de la version un, charge utile de la version deux.
	versionne bool

	// secret dit si l'appel porte l'en-tête de clé secrète.
	//
	// Trois points d'entrée ne le portent pas, et c'est utile au delà de l'économie d'un
	// en-tête : ils fonctionnent sans aucun compte configuré, donc sans déclencher la
	// moindre rotation de clé.
	secret bool
}

// Les onze points d'entrée, dans l'ordre où la collection de requêtes de la passerelle
// les présente.
var (
	epSecret     = endpoint{nom: "renew-secret", code: func(c CodesURL) string { return c.Secret }, versionne: true}
	epPaiement   = endpoint{nom: "rest", code: func(c CodesURL) string { return c.Paiement }, versionne: true, secret: true}
	epStatut     = endpoint{nom: "status", code: func(c CodesURL) string { return c.Statut }, secret: true}
	epFrais      = endpoint{nom: "get-fees", code: func(c CodesURL) string { return c.Frais }, versionne: true, secret: true}
	epSolde      = endpoint{nom: "balance", code: func(c CodesURL) string { return c.Solde }, secret: true}
	epKYC        = endpoint{nom: "kyc", code: func(c CodesURL) string { return c.KYC }, versionne: true}
	epPays       = endpoint{nom: "get-countries", code: func(c CodesURL) string { return c.Pays }, versionne: true}
	epOperateurs = endpoint{nom: "get-operators", code: func(c CodesURL) string { return c.Operateurs }, versionne: true}
	epLien       = endpoint{nom: "link", code: func(c CodesURL) string { return c.Lien }, secret: true}
	epQRCode     = endpoint{nom: "generate-qr-code", code: func(c CodesURL) string { return c.QRCode }, versionne: true, secret: true}
	epRapport    = endpoint{nom: "reports", code: func(c CodesURL) string { return c.Rapport }, versionne: true, secret: true}
)

// endpoints sert au test qui épingle les deux régimes de version contre la collection de
// requêtes publiée.
var endpoints = []endpoint{
	epSecret, epPaiement, epStatut, epFrais, epSolde,
	epKYC, epPays, epOperateurs, epLien, epQRCode, epRapport,
}

// prefixeVersion est le segment de version des chemins qui en portent un.
const prefixeVersion = "/v2"

// urlDe compose l'URL d'un point d'entrée, et refuse un code d'URL absent.
//
// La vérification a lieu ici plutôt qu'au démarrage pour les API accessoires : exiger les
// onze codes dès [Nouveau] ferait refuser de démarrer un projet qui n'en utilise que
// trois. Le message nomme l'API, ce qui suffit à savoir quel code aller chercher dans
// l'espace marchand.
func (c *Client) urlDe(e endpoint) (string, error) {
	code := e.code(c.reglages.Codes)
	if code == "" {
		return "", fmt.Errorf("%w: code url manquant: %s", ErrConfiguration, e.nom)
	}
	if e.versionne {
		return c.reglages.BaseURL + prefixeVersion + "/" + code + "/" + e.nom, nil
	}
	return c.reglages.BaseURL + "/" + code + "/" + e.nom, nil
}
