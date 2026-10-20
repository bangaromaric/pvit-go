package pvit

import "net/netip"

// octetsPVit sont les quatre adresses depuis lesquelles la passerelle émet ses
// notifications.
//
// Elles sont écrites en octets et non en texte pour une raison précise : construire une
// adresse depuis quatre octets ne peut pas échouer, là où analyser une chaîne rend une
// erreur qu'il faudrait soit ignorer, soit transformer en panique au chargement du
// paquet. Un paquet de fondation ne fait paniquer personne.
var octetsPVit = [...][4]byte{
	{176, 31, 65, 18},
	{176, 31, 65, 20},
	{176, 31, 65, 21},
	{13, 59, 249, 167},
}

// IPsAutorisees rend les adresses depuis lesquelles la passerelle émet ses notifications.
//
// C'est le filtrage ENTRANT, le seul contrôle d'authenticité dont dispose le callback de
// transaction : celui-ci n'est ni signé, ni horodaté, ni muni d'un identifiant de remise.
// À ne pas confondre avec [CodeIPMarchandInvalide], qui sanctionne le filtrage SORTANT et
// se règle dans l'espace marchand, pas ici.
//
// Une fonction et non une variable, parce qu'une tranche exportée serait modifiable par
// n'importe quel appelant, et qu'une liste d'adresses de confiance qu'un tiers peut
// allonger ne protège plus de rien.
func IPsAutorisees() []netip.Addr {
	liste := make([]netip.Addr, 0, len(octetsPVit))
	for _, o := range octetsPVit {
		liste = append(liste, netip.AddrFrom4(o))
	}
	return liste
}

// EstAutorisee dit si une adresse est celle de la passerelle.
//
// Elle démappe d'abord l'adresse : un serveur HTTP qui écoute en double pile rend
// volontiers la forme ::ffff:176.31.65.18, qui est la même machine et doit être reconnue.
//
// Une adresse IPv6 VRAIE est refusée, et c'est une conséquence à connaître : les quatre
// adresses publiées sont en IPv4, et le tutoriel de la passerelle ne parle que d'IPv4. Si
// elle se mettait à émettre en IPv6, ce filtrage refuserait tout, et il faudrait relever les
// nouvelles adresses auprès d'elle plutôt que d'élargir ce prédicat au hasard.
//
// Elle ne décide pas QUELLE adresse interroger. Derrière un proxy, la vraie adresse du
// client dépend des proxies de confiance déclarés, et c'est une question que le projet
// appelant a déjà tranchée. Un intergiciel livré ici devinerait, et donnerait une
// impression de protection plutôt qu'une protection.
func EstAutorisee(ip netip.Addr) bool {
	if !ip.IsValid() {
		return false
	}
	nue := ip.Unmap()
	if !nue.Is4() {
		return false
	}
	quatre := nue.As4()
	for _, o := range octetsPVit {
		if quatre == o {
			return true
		}
	}
	return false
}
