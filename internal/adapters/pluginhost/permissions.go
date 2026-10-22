package pluginhost

import (
	"fmt"
	"net/netip"
	"regexp"
	"strings"

	"github.com/lein3000zzz/polythought-go/pkg/plugin"
)

// hostname is a DNS name a fetch permission may name: labels of letters, digits and
// dashes, not starting or ending with a dash.
var hostname = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)*[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// permissions are what a plugin's manifest asks for, read: each call is checked against
// them.
type permissions struct {
	fetchAny   bool
	fetchHosts map[string]bool
	wallets    bool
	telegram   bool
	orders     bool
}

// permissionsOf reads a manifest's permissions, refusing any it does not know, so a typo
// is not a permission quietly missing.
func permissionsOf(asked []string) (permissions, error) {
	p := permissions{fetchHosts: map[string]bool{}}

	for _, perm := range asked {
		switch host, fetch := strings.CutPrefix(perm, plugin.PermFetch); {
		case perm == plugin.PermFetchAny:
			p.fetchAny = true
		case perm == plugin.PermWallets:
			p.wallets = true
		case perm == plugin.PermTelegram:
			p.telegram = true
		case perm == plugin.PermOrders:
			p.orders = true
		case fetch && validHost(host):
			p.fetchHosts[host] = true
		case fetch:
			return p, fmt.Errorf("permission %q: fetch: takes a host, lowercase, like fetch:api.example.com, or *", perm)
		default:
			return p, fmt.Errorf("permission %q is none a plugin may ask for", perm)
		}
	}

	return p, nil
}

func validHost(host string) bool {
	if addr, err := netip.ParseAddr(host); err == nil {
		return addr.String() == host && addr.Zone() == ""
	}

	return len(host) <= 253 && hostname.MatchString(host)
}

// fetches says whether fetch may reach host, a URL's, as url.Hostname gives it.
func (p permissions) fetches(host string) bool {
	if p.fetchAny {
		return true
	}

	host = strings.TrimSuffix(strings.ToLower(host), ".")

	return host != "" && p.fetchHosts[host]
}
