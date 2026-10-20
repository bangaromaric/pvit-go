package pvit

import (
	"net/netip"
	"testing"
)

// Le callback de transaction n'est ni signé, ni horodaté, ni muni d'un identifiant de
// remise : ces quatre adresses sont le seul contrôle d'authenticité disponible.
func TestEstAutoriseeReconnaitLesAdressesDeLaPasserelle(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom  string
		ip   string
		want bool
	}{
		{nom: "premiere adresse publiee", ip: "176.31.65.18", want: true},
		{nom: "deuxieme adresse publiee", ip: "176.31.65.20", want: true},
		{nom: "troisieme adresse publiee", ip: "176.31.65.21", want: true},
		{nom: "quatrieme adresse publiee", ip: "13.59.249.167", want: true},

		{nom: "forme ipv4 mappee en ipv6", ip: "::ffff:176.31.65.18", want: true},

		{nom: "voisine dans le meme reseau refusee", ip: "176.31.65.19", want: false},
		{nom: "voisine juste apres refusee", ip: "176.31.65.22", want: false},
		{nom: "adresse privee refusee", ip: "10.0.0.1", want: false},
		{nom: "boucle locale refusee", ip: "127.0.0.1", want: false},
		{nom: "ipv6 refusee", ip: "2001:db8::1", want: false},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()
			ip, err := netip.ParseAddr(c.ip)
			if err != nil {
				t.Fatalf("netip.ParseAddr(%q) err = %v, want nil", c.ip, err)
			}
			if got := EstAutorisee(ip); got != c.want {
				t.Errorf("EstAutorisee(%s) = %v, want %v", c.ip, got, c.want)
			}
		})
	}
}

// Une adresse invalide ne doit ni passer, ni faire paniquer : c'est ce qu'un appelant
// obtient quand il transmet un en-tête absent ou mal formé.
func TestUneAdresseInvalideEstRefusee(t *testing.T) {
	t.Parallel()

	var zero netip.Addr
	if got := EstAutorisee(zero); got {
		t.Errorf("EstAutorisee(adresse zero) = true, want false")
	}
}

// Une tranche exportée serait modifiable par n'importe quel appelant, et une liste
// d'adresses de confiance qu'un tiers peut allonger ne protège plus de rien.
func TestLaListeDAdressesNEstPasModifiableParLAppelant(t *testing.T) {
	t.Parallel()

	liste := IPsAutorisees()
	if got, want := len(liste), 4; got != want {
		t.Fatalf("IPsAutorisees() a rendu %d adresses, want %d", got, want)
	}

	intruse := netip.AddrFrom4([4]byte{203, 0, 113, 1})
	liste[0] = intruse

	if EstAutorisee(intruse) {
		t.Errorf("EstAutorisee(%s) = true apres ecriture dans la tranche rendue, want false", intruse)
	}
	if !EstAutorisee(netip.AddrFrom4([4]byte{176, 31, 65, 18})) {
		t.Errorf("EstAutorisee(176.31.65.18) = false apres ecriture dans la tranche rendue, want true")
	}
}
