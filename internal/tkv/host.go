package tkv

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// bindAddr is the only address tkv listens on.
var bindAddr = netip.AddrFrom4([4]byte{127, 0, 0, 1})

// bindHostPort is the listen address for port. Callers pass a port already
// validated to 1–65535.
func bindHostPort(port int) string {
	return netip.AddrPortFrom(bindAddr, uint16(port)).String()
}

// baseURL is the dashboard origin for port, with no trailing slash.
func baseURL(port int) string {
	return "http://" + bindHostPort(port)
}

// hostAllowlist is the set of lower-case Host hostnames tkv serves.
// DNS rebinding always presents the attacker's name in Host, so the name
// alone is the guard; the port is ignored so a forward on another local
// port (ssh -L 9000:127.0.0.1:8736) still works.
type hostAllowlist map[string]struct{}

// newHostAllowlist builds the name set for a server bound on bind.
func newHostAllowlist(bind netip.Addr) hostAllowlist {
	hosts := hostAllowlist{strings.ToLower(bind.String()): {}}
	if bind.IsLoopback() {
		for _, name := range []string{"localhost", "127.0.0.1", "::1"} {
			hosts[name] = struct{}{}
		}
	}
	return hosts
}

// allows reports whether the Host header value names an allowed host.
func (h hostAllowlist) allows(host string) bool {
	name := hostName(host)
	if name == "" {
		return false
	}
	_, ok := h[strings.ToLower(name)]
	return ok
}

// hostName is the hostname in a Host header value, or "" when the value is
// malformed. net.SplitHostPort handles only values that carry a port, and it
// does not require brackets to hold an IPv6 address.
func hostName(host string) string {
	bracketed := strings.HasPrefix(host, "[")
	name, _, err := net.SplitHostPort(host)
	if err != nil {
		name = host
		if bracketed && len(host) >= 2 && host[len(host)-1] == ']' {
			name = host[1 : len(host)-1]
		}
		if strings.ContainsAny(name, "[]") || (!bracketed && strings.Contains(name, ":")) {
			return ""
		}
	}
	if bracketed {
		if addr, err := netip.ParseAddr(name); err != nil || !addr.Is6() {
			return ""
		}
	}
	return name
}

// guard refuses a request whose Host is outside the allowlist before next runs.
func (h hostAllowlist) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.allows(r.Host) {
			http.Error(w, fmt.Sprintf("tkv: host %q not allowed", r.Host), http.StatusMisdirectedRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
}
