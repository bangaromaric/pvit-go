// Package pvit parle le dialecte de la passerelle de paiement mobile money MyPVit.
//
// Il traduit un montant, un numéro et un opérateur en requêtes HTTP, garde la clé
// secrète qui expire toutes les heures, et rend ce que la passerelle répond. Il ne
// décide de rien d'applicatif : il ne journalise pas, ne lit pas l'environnement de
// lui-même, ne crée aucune goroutine et ne convertit aucune erreur en code HTTP.
// Celui qui le câble garde ces trois politiques.
//
// # Cinq faits qu'on ne devine pas
//
// Ils coûtent une journée chacun quand on les apprend par l'erreur, et le quatrième a coûté
// six heures de production à qui l'a appris ainsi.
//
//   - Les codes URL sont PAR API, les comptes sont PAR OPÉRATEUR. La passerelle place
//     un segment opaque dans chaque chemin, et il y en a onze, un par API. Le code de
//     compte d'opération, la clé secrète et le mot de passe de rotation, eux, vont par
//     trois et se répètent pour chaque famille d'opérateur. Voir [CodesURL] et [Compte].
//   - Le préfixe de version n'est pas global. La plupart des chemins sont sous /v2,
//     mais status, balance et link ne le sont pas. Le lien de paiement pousse l'écart
//     jusqu'au bout : chemin de la version un, charge utile de la version deux. La table
//     interne des points d'entrée en est la seule source de vérité, et [Appel.Versionne]
//     l'expose à qui passe par [Client.Appeler].
//   - Le champ reference porte l'idempotence, et un 409 n'est pas une erreur. Rejouer une
//     référence déjà vue rend le code 5000, qui veut dire « déjà initié, va interroger le
//     statut ». Le traiter comme un échec crée un double paiement. Voir [ErrDejaInitiee].
//   - L'INTERROGATION DE STATUT PREND CETTE MÊME RÉFÉRENCE, et non l'identifiant que la
//     passerelle attribue, bien que son paramètre s'appelle transactionId et que sa
//     documentation affirme le contraire. La version 0.1.0 de ce paquet a recopié cette
//     affirmation : aucune tentative ne pouvait plus être tranchée, ni par notification ni
//     par réconciliation. Voir [DemandeStatut.Reference], qui porte la mesure.
//   - IL N'EXISTE AUCUN POINT D'ENTRÉE CARTE BANCAIRE. Encaisser par Visa ou Mastercard,
//     c'est demander un lien de paiement avec le service VISA_MASTERCARD, puis rediriger le
//     client vers l'adresse rendue : le formulaire bancaire est hébergé par la passerelle.
//     Ce point d'entrée répond par ailleurs en casse mêlée avec un code de retour ENTIER, là
//     où l'API de transaction répond en soulignés avec la chaîne « 200 », et le SUCCESS qu'il
//     rend concerne la CRÉATION DU LIEN et jamais le paiement. Voir [Client.PayerParCarte] et
//     [Lien.StatutCreation].
//
// # Ce que l'appelant doit écrire lui-même
//
// La réponse d'un paiement vaut PENDING et jamais un résultat. Le callback de
// transaction n'est ni signé, ni horodaté, ni muni d'un identifiant de remise, et sa
// politique de reprise n'est pas documentée. Rien ne se crédite donc sur sa seule foi :
// il faut une boucle qui reprenne toute tentative restée incertaine et redemande son
// statut. Ce paquet n'a pas de stockage, donc il ne peut pas tenir cette boucle ; il
// donne le barème que l'appelant ne peut pas deviner, [AInterroger],
// [ProchaineTentative] et [TransitionPermise], et l'exemple de [AInterroger] en montre
// une de quinze lignes.
//
// Un paiement par carte met en plus un HUMAIN dans la boucle, devant un formulaire bancaire
// puis l'authentification forte de sa banque : le délai d'incertitude de ce paquet, calibré
// pour un parcours sans navigateur, y est trop court, et la durée de vie d'un lien n'est
// publiée nulle part. Le délai est donc un paramètre de l'appelant, voir
// [AInterrogerApres].
//
// # Le callback, et sa seule règle dure
//
// L'accusé de réception réémet le code reçu dans la notification, sous un autre nom.
// Écrire 200 en dur marque la transaction « échec de notification » chez la passerelle
// et invalide la recette d'intégration, donc ferme le compte de production. Voir
// [Callback.Accuse].
package pvit
