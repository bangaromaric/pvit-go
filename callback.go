package pvit

import (
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"strings"
)

// TailleMaxCallback est la borne par défaut du corps d'une notification entrante.
//
// Une notification de transaction en pèse quelques centaines d'octets : la borne existe
// pour qu'un corps inattendu ne remplisse pas la mémoire du service exposé au monde.
const TailleMaxCallback = 1 << 16

// Callback est une notification de transaction, telle que la passerelle l'envoie.
//
// Elle est en casse mêlée alors que le reste de l'API est en soulignés, et son champ
// d'identifiant client porte deux majuscules. Ce ne sont pas des coquilles de ce paquet.
//
// # Ce qu'elle N'EST PAS
//
// Elle n'est pas une preuve. Ce canal n'est ni signé, ni horodaté, ni muni d'un identifiant
// de remise, et sa politique de reprise n'est pas documentée : son seul contrôle
// d'authenticité est le filtrage des adresses de [IPsAutorisees]. Rien ne doit se créditer
// sur sa seule foi, et [Client.Statut] reste le mécanisme de vérité.
type Callback struct {
	// TransactionID est l'identifiant de la passerelle. Il doit être réémis dans
	// l'accusé.
	TransactionID string

	// Reference est la référence du marchand, celle qui porte l'idempotence et qui
	// permet de retrouver la tentative dans son propre stockage.
	Reference Reference

	// Statut est l'état rapporté.
	Statut Statut

	// Montant est la somme de la transaction.
	Montant Montant

	// NumeroClient est le numéro du payeur.
	NumeroClient string

	// Frais sont ceux de la passerelle, et FraisOperateur ceux de l'opérateur. Les deux
	// étages, que le calculateur de frais ne montre pas ensemble.
	Frais          Frais
	FraisOperateur Frais

	// Total est ce que le payeur a effectivement débité.
	Total Frais

	// MontantCredite est ce que le marchand a effectivement reçu.
	MontantCredite Montant

	// ProprietaireFrais et ProprietaireFraisOperateur disent qui a porté chaque étage.
	ProprietaireFrais          Proprietaire
	ProprietaireFraisOperateur Proprietaire

	// InfoLibre est le champ libre transmis à l'initiation.
	InfoLibre string

	// Operation est le sens du mouvement.
	Operation TypeOperation

	// Code est le code applicatif de la notification.
	//
	// C'EST CELUI QU'IL FAUT RÉÉMETTRE, sous un autre nom, dans l'accusé de réception.
	// Voir [Callback.Accuse].
	Code int

	// Operateur et CompteOperation disent par quel compte la transaction a passé.
	Operateur       Operateur
	CompteOperation string
}

// notificationRecue est la forme sur le fil, lue défensivement.
type notificationRecue struct {
	TransactionID       jsontext.Value `json:"transactionId"`
	MerchantReferenceID jsontext.Value `json:"merchantReferenceId"`
	Statut              jsontext.Value `json:"status"`
	Montant             jsontext.Value `json:"amount"`
	CustomerID          jsontext.Value `json:"customerID"`
	Frais               jsontext.Value `json:"fees"`
	FraisOperateur      jsontext.Value `json:"operatorFees"`
	Total               jsontext.Value `json:"totalAmount"`
	Credite             jsontext.Value `json:"amountCredited"`
	ChargeOwner         jsontext.Value `json:"chargeOwner"`
	OperatorOwnerCharge jsontext.Value `json:"operatorOwnerCharge"`
	FreeInfo            jsontext.Value `json:"freeInfo"`
	Operation           jsontext.Value `json:"transactionOperation"`
	Code                jsontext.Value `json:"code"`
	Operateur           jsontext.Value `json:"operator"`
	CompteOperation     jsontext.Value `json:"accountOperationCode"`
}

// AccuseReception est le corps que la passerelle attend en réponse à une notification.
type AccuseReception struct {
	// TransactionID est l'identifiant reçu, réémis tel quel.
	TransactionID string `json:"transactionId"`

	// ResponseCode est le code reçu, réémis SOUS UN AUTRE NOM. Noter le renommage : il se
	// lit « code » à l'aller et « responseCode » au retour.
	ResponseCode int `json:"responseCode"`
}

// ErrAccuseImpossible signale une notification dont on ne peut pas composer l'accusé.
//
// C'est le cas quand l'identifiant ou le code manquent : l'accusé doit les réémettre, et
// en inventer un invaliderait la recette d'intégration autant que d'écrire 200 en dur.
var ErrAccuseImpossible = errors.New("notification sans identifiant ni code, accuse impossible")

// DecoderCallback lit une notification de transaction.
//
// Fonction PURE : elle ne touche pas au réseau, et c'est ce qui la rend utilisable depuis
// n'importe quel routeur. Le corps doit arriver en octets, tels que reçus.
//
// Elle ne fait paniquer sur rien : un corps tronqué, un tableau, une valeur nulle ou un
// champ mal typé rendent une erreur exploitable. Une SOMME illisible en est une aussi : la
// laisser passer à zéro ferait créditer zéro franc sur une transaction réussie.
//
// La version deux de encoding/json refuse par ailleurs un nom dupliqué et un UTF-8
// invalide, ce qui est un échec dur sur une charge utile qu'on ne contrôle pas : mieux vaut
// ne pas acquitter et laisser la passerelle réessayer que d'acquitter ce qu'on n'a pas
// compris.
func DecoderCallback(corps []byte) (Callback, error) {
	var recue notificationRecue
	if err := json.Unmarshal(corps, &recue); err != nil {
		return Callback{}, Erreur{
			Operation: "callback", motif: ErrReponseIllisible, cause: err,
		}
	}

	// Les énumérations sont normalisées en capitales, comme la passerelle les publie : sans
	// cela, un « payment » en minuscules ne serait égal à aucune constante de ce paquet, et
	// la comparaison échouerait en silence.
	c := Callback{
		TransactionID:              premierTexte(recue.TransactionID),
		Reference:                  Reference(premierTexte(recue.MerchantReferenceID)),
		Statut:                     LireStatut(premierTexte(recue.Statut)),
		NumeroClient:               premierTexte(recue.CustomerID),
		ProprietaireFrais:          Proprietaire(majuscules(premierTexte(recue.ChargeOwner))),
		ProprietaireFraisOperateur: Proprietaire(majuscules(premierTexte(recue.OperatorOwnerCharge))),
		InfoLibre:                  premierTexte(recue.FreeInfo),
		Operation:                  TypeOperation(majuscules(premierTexte(recue.Operation))),
		Operateur:                  Operateur(majuscules(premierTexte(recue.Operateur))),
		CompteOperation:            premierTexte(recue.CompteOperation),
	}
	if code, ok := codeApplicatif(recue.Code); ok {
		c.Code = code
	}

	// Les sommes sont lues en PROPAGEANT l'erreur, et c'est le point le plus important de
	// cette fonction. Écarter l'erreur laisserait un montant illisible devenir zéro, sur une
	// notification qui annonce un succès : l'appelant créditerait zéro franc et marquerait la
	// transaction payée. Mieux vaut ne pas acquitter, laisser la passerelle réessayer, et
	// reprendre la vérité par [Client.Statut].
	var err error
	if c.Montant, err = montantSiPresent("amount", recue.Montant); err != nil {
		return c, err
	}
	if c.MontantCredite, err = montantSiPresent("amountCredited", recue.Credite); err != nil {
		return c, err
	}
	if c.Frais, err = fraisSiPresent("fees", recue.Frais); err != nil {
		return c, err
	}
	if c.FraisOperateur, err = fraisSiPresent("operatorFees", recue.FraisOperateur); err != nil {
		return c, err
	}
	if c.Total, err = fraisSiPresent("totalAmount", recue.Total); err != nil {
		return c, err
	}

	if c.TransactionID == "" || c.Code == 0 {
		return c, Erreur{
			Operation: "callback",
			Message:   "notification sans identifiant de transaction ou sans code",
			motif:     ErrAccuseImpossible,
		}
	}
	return c, nil
}

// Accuse compose le corps de l'accusé de réception, avec l'écho DYNAMIQUE du code reçu.
//
// C'EST LA SEULE RÈGLE DURE DE CE FICHIER, et elle décide de l'ouverture du compte de
// production. On lit le code dans la notification et on le renvoie sous le nom
// responseCode. Écrire 200 en dur marque la transaction « échec de notification » chez la
// passerelle, et sa documentation en fait une CONDITION DE VALIDATION des tests
// d'intégration, pas une bonne pratique.
func (c Callback) Accuse() ([]byte, error) {
	if c.TransactionID == "" || c.Code == 0 {
		return nil, ErrAccuseImpossible
	}
	return json.Marshal(AccuseReception{TransactionID: c.TransactionID, ResponseCode: c.Code})
}

// Accuser écrit l'accusé de réception sur une réponse HTTP.
//
// Le paramètre est un [http.ResponseWriter], interface de la bibliothèque standard : le
// writer d'un contexte Gin en est un, et c'est ce qui permet à ce paquet de servir un
// projet Gin sans connaître Gin, sans adaptateur et sans réflexion.
func Accuser(w http.ResponseWriter, c Callback) error {
	corps, err := c.Accuse()
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, err = w.Write(corps)
	return err
}

// GestionnaireCallback rend une route prête à recevoir les notifications.
//
// Elle sert qui n'a pas de routeur à lui. Un projet qui en a un garde le sien et appelle
// [DecoderCallback] puis [Accuser], ce qui lui laisse ses intergiciels, sa politique de
// proxies de confiance et sa traduction d'erreurs.
//
// # Ce qu'elle décide, et c'est la seule politique de ce paquet
//
// Elle n'acquitte QUE si le traitement rend nil. Sur erreur, et aussi quand le traitement
// est nil, elle répond 500 sans corps, pour que la passerelle considère la remise manquée et
// réessaie. Acquitter un traitement qui a échoué, ou qui n'existe pas, perdrait la
// notification pour toujours, et donc l'argent qu'elle annonce.
//
// Elle ne journalise rien : l'erreur du traitement est celle de l'appelant, qui a déjà sa
// politique de trace.
func GestionnaireCallback(traiter func(context.Context, Callback) error) http.Handler {
	return gestionnaireBorne(traiter, TailleMaxCallback)
}

// GestionnaireCallback rend une route prête, bornée par l'option du client.
//
// Même comportement que la fonction de paquet du même nom, à ceci près qu'elle respecte
// [AvecTailleMaxCallback].
func (c *Client) GestionnaireCallback(traiter func(context.Context, Callback) error) http.Handler {
	return gestionnaireBorne(traiter, c.opts.tailleMaxCallback)
}

// gestionnaireBorne porte le comportement commun des deux gestionnaires.
func gestionnaireBorne(traiter func(context.Context, Callback) error, max int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		corps, err := io.ReadAll(http.MaxBytesReader(w, req.Body, max))
		if err != nil {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}

		notification, err := DecoderCallback(corps)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// Un traitement absent est une faute d'appel, et la conduite la plus sûre est de
		// NE PAS acquitter. Acquitter sans rien enregistrer perdrait la notification, donc
		// l'argent qu'elle annonce, et personne ne le verrait ; refuser fait réessayer la
		// passerelle et finit par se voir. Un paquet de fondation ne peut pas faire
		// paniquer le programme qui l'utilise, donc c'est ainsi qu'il le dit.
		if traiter == nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if err := traiter(req.Context(), notification); err != nil {
			// Pas d'accusé : la passerelle doit réessayer plutôt que de tenir la
			// notification pour remise.
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		if err := Accuser(w, notification); err != nil {
			// L'en-tête est déjà parti : il n'y a plus rien à dire au client, et ce
			// paquet ne journalise pas. La passerelle traitera l'absence d'accusé
			// complet comme une remise manquée, ce qui est le comportement voulu.
			return
		}
	})
}

// majuscules normalise une énumération reçue, espaces retirés.
func majuscules(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }
