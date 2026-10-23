package pvit

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Les en-têtes que la passerelle lit.
const (
	// enteteSecret porte la clé secrète. Huit points d'entrée sur onze l'exigent.
	enteteSecret = "X-Secret"

	// enteteMediaTypeCallback demande le format des notifications. Il ne vaut que pour
	// les paiements, lien de paiement et carte bancaire compris, et jamais pour un
	// remboursement, qui est synchrone et n'en émet aucune.
	enteteMediaTypeCallback = "X-Callback-MediaType"
)

// tailleMaxReponse borne le corps lu d'une réponse.
//
// Un mégaoctet, là où une réponse de transaction en pèse quelques centaines : la borne
// existe pour qu'une page d'erreur d'un intermédiaire, ou une réponse inattendue, ne
// remplisse pas la mémoire du service. Elle n'est pas réglable, parce qu'aucun point
// d'entrée typé de ce paquet ne rend de flux.
const tailleMaxReponse = 1 << 20

// tailleDrainage borne ce qu'on lit d'un corps avant de le fermer.
const tailleDrainage = 1 << 16

// requete décrit un appel à émettre, avant toute considération de clé secrète.
type requete struct {
	operation   string
	operateur   Operateur
	endpoint    endpoint
	methode     string
	query       url.Values
	corps       []byte
	typeContenu string
	entetes     map[string]string
}

// resultat est ce qu'une réponse laisse une fois lue et fermée.
type resultat struct {
	statut  int
	corps   []byte
	entetes http.Header
}

// emettre envoie une requête, et rejoue une fois si la clé a été refusée.
//
// Le rejeu est CONDITIONNEL et borné : seulement sur un refus de clé, seulement sur un
// point d'entrée qui en porte une, et seulement autant de fois que l'option l'autorise, une
// par défaut. S'acharner au delà ferait d'une faute de configuration une tempête de
// requêtes, et le code 10008, qui sanctionne l'échec d'envoi de clé, finirait par tomber.
func (c *Client) emettre(ctx context.Context, r requete) (resultat, error) {
	base, err := c.urlDe(r.endpoint)
	if err != nil {
		return resultat{}, err
	}
	cible := base
	if len(r.query) > 0 {
		cible += "?" + r.query.Encode()
	}

	var cle string
	if r.endpoint.secret {
		if cle, err = c.cleValide(ctx, r.operation, r.operateur, ""); err != nil {
			return resultat{}, err
		}
	}

	res, err := c.unCoup(ctx, r, cible, cle)
	if err == nil || !r.endpoint.secret {
		return res, err
	}

	// Seul un refus de clé se rejoue, et seulement autant de fois que l'option l'autorise.
	// Un refus de contrat, lui, échouerait à l'identique : le rejouer ferait d'une faute
	// d'appel une tempête de requêtes.
	for essai := 0; essai < c.opts.rejeuAuthentification; essai++ {
		if !errors.Is(err, ErrAuthentification) {
			return res, err
		}
		neuve, errCle := c.cleValide(ctx, r.operation, r.operateur, cle)
		if errCle != nil {
			return res, errCle
		}
		cle = neuve
		if res, err = c.unCoup(ctx, r, cible, cle); err == nil {
			return res, nil
		}
	}
	return res, err
}

// unCoup émet exactement une requête et lit sa réponse.
func (c *Client) unCoup(ctx context.Context, r requete, cible, cle string) (resultat, error) {
	// Le corps est rejoué depuis les octets, et jamais depuis un io.Reader gardé d'un
	// essai à l'autre : un lecteur déjà consommé enverrait un corps VIDE au second coup,
	// ce qui donnerait un refus de contrat incompréhensible.
	var lecteur io.Reader
	if r.corps != nil {
		lecteur = bytes.NewReader(r.corps)
	}

	req, err := http.NewRequestWithContext(ctx, r.methode, cible, lecteur)
	if err != nil {
		return resultat{}, erreurTransport(r.operation, r.operateur, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.opts.agentUtilisateur)
	if r.typeContenu != "" {
		req.Header.Set("Content-Type", r.typeContenu)
	}
	if cle != "" {
		req.Header.Set(enteteSecret, cle)
	}
	for nom, valeur := range r.entetes {
		req.Header.Set(nom, valeur)
	}

	reponse, err := c.http.Do(req)
	if err != nil {
		// Une annulation demandée par l'appelant n'est PAS une erreur de la passerelle,
		// et la ranger parmi les indisponibilités la rendrait rejouable : une boucle de
		// réconciliation relancerait alors ce que l'appelant venait d'abandonner. Elle
		// remonte donc nue.
		if errCtx := ctx.Err(); errCtx != nil {
			return resultat{}, errCtx
		}
		return resultat{}, erreurTransport(r.operation, r.operateur, err)
	}
	defer fermer(reponse)

	brut, err := io.ReadAll(io.LimitReader(reponse.Body, tailleMaxReponse))
	if err != nil {
		if errCtx := ctx.Err(); errCtx != nil {
			return resultat{}, errCtx
		}
		return resultat{}, erreurTransport(r.operation, r.operateur, err)
	}

	res := resultat{statut: reponse.StatusCode, corps: brut, entetes: reponse.Header.Clone()}
	if reponse.StatusCode >= http.StatusBadRequest {
		return res, classer(r.operation, r.operateur, reponse.StatusCode, brut)
	}
	return res, nil
}

// fermer draine le corps d'une réponse, borné, puis le ferme.
//
// Le drainage n'est pas une politesse : abandonner un corps à moitié lu fait jeter la
// connexion, et chaque appel suivant repaie alors une poignée de main TLS.
//
// Les deux erreurs sont écartées sciemment, et c'est la seule entorse du paquet à la règle
// qui l'interdit : à ce point, le corps utile est déjà lu, et il n'existe aucune conduite
// qu'une erreur de drainage ou de fermeture pourrait changer. C'est aussi ce que fait le
// defer resp.Body.Close() de la bibliothèque standard.
func fermer(reponse *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(reponse.Body, tailleDrainage))
	_ = reponse.Body.Close()
}

// Appel décrit une requête arbitraire sur la passerelle.
//
// C'est LA TRAPPE DE SORTIE du paquet. Les méthodes typées couvrent l'encaissement, mobile
// money et carte bancaire comprises, et trois API restent dehors : le paiement fractionné,
// le QR code et les exports. Elles sont joignables par ici, avec la rotation de clé et le
// classement d'erreurs, qui sont la vraie valeur de ce paquet, sans qu'il faille le forker.
//
// Ce qui passe par ici n'a pas de type de retour : l'appelant décode lui-même. Les
// méthodes typées restent la surface tenue.
type Appel struct {
	// Methode est la méthode HTTP, par exemple [http.MethodPost].
	Methode string

	// CodeURL est le segment opaque de cette API, tel que l'espace marchand le donne.
	CodeURL string

	// Chemin est le dernier segment du chemin, par exemple reports ou generate-qr-code.
	Chemin string

	// Versionne dit si le chemin porte le préfixe de version. IL N'EST PAS GLOBAL :
	// status et balance, par exemple, n'en portent pas, là où le QR code et les exports en
	// portent un. La table interne des points d'entrée fait autorité pour les API typées.
	Versionne bool

	// Operateur choisit le compte dont la clé secrète part dans l'en-tête. La valeur zéro
	// n'envoie AUCUNE clé, et ne déclenche donc aucune rotation.
	Operateur Operateur

	// Query porte les paramètres d'URL.
	Query url.Values

	// Corps est sérialisé en JSON s'il n'est pas nil.
	Corps any
}

// Reponse est ce qu'un [Appel] rapporte.
//
// Le corps est rendu en octets déjà lus et bornés, et non en flux : c'est ce paquet qui
// garde la charge de borner, drainer et fermer, parce qu'un appelant l'oublierait, et
// qu'une connexion jetée à chaque appel coûte une poignée de main TLS.
type Reponse struct {
	// HTTP est le code de réponse.
	HTTP int

	// Corps est la charge utile, lue et bornée.
	Corps []byte

	// Entetes sont les en-têtes de la réponse, utiles à qui lit un ETag ou une
	// signature.
	Entetes http.Header
}

// Appeler exécute une requête arbitraire, rotation de clé et classement d'erreurs compris.
//
// Voir [Appel] pour ce que cette trappe de sortie sert, et pourquoi elle existe.
func (c *Client) Appeler(ctx context.Context, a Appel) (Reponse, error) {
	a.Methode = strings.ToUpper(strings.TrimSpace(a.Methode))
	a.CodeURL = strings.TrimSpace(a.CodeURL)
	a.Chemin = strings.Trim(strings.TrimSpace(a.Chemin), "/")

	var manques []string
	if a.Methode == "" {
		manques = append(manques, "methode")
	}
	if a.CodeURL == "" {
		manques = append(manques, "code url")
	}
	if a.Chemin == "" {
		manques = append(manques, "chemin")
	}
	if len(manques) > 0 {
		return Reponse{}, fmt.Errorf("%w: appel: %s", ErrConfiguration, strings.Join(manques, ", "))
	}
	if a.Operateur != "" {
		if _, connu := c.comptes[a.Operateur]; !connu {
			return Reponse{}, fmt.Errorf("%w: aucun compte d operation pour %s", ErrConfiguration, a.Operateur)
		}
	}

	var corps []byte
	typeContenu := ""
	if a.Corps != nil {
		var err error
		if corps, err = json.Marshal(a.Corps); err != nil {
			return Reponse{}, ChampInvalideError{Champ: "corps", Raison: "serialisation json impossible"}
		}
		typeContenu = "application/json"
	}

	res, err := c.emettre(ctx, requete{
		operation: "appel",
		operateur: a.Operateur,
		endpoint: endpoint{
			nom:       a.Chemin,
			code:      func(CodesURL) string { return a.CodeURL },
			versionne: a.Versionne,
			secret:    a.Operateur != "",
		},
		methode:     a.Methode,
		query:       a.Query,
		corps:       corps,
		typeContenu: typeContenu,
	})
	return Reponse{HTTP: res.statut, Corps: res.corps, Entetes: res.entetes}, err
}
