package tkv

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// hostAllowlist is the set of lower-case Host hostnames tkv serves.
// DNS rebinding always presents the attacker's name in Host, so the name
// alone is the guard; the port is ignored so a forward on another local
// port (ssh -L 9000:127.0.0.1:8736) still works.
type hostAllowlist map[string]struct{}

// newHostAllowlist builds the allowlist from the stable name set.
func newHostAllowlist(names []string) hostAllowlist {
	hosts := make(hostAllowlist, len(names))
	for _, name := range names {
		hosts[strings.ToLower(name)] = struct{}{}
	}
	return hosts
}

// allows reports whether the Host header value is a stable name, or an IP
// literal equal to local, the address the connection was accepted on.
// An IP literal is safe against rebinding, which always presents a DNS name,
// and it lets a wildcard bind serve a LAN address gained after start.
func (h hostAllowlist) allows(host string, local netip.Addr) bool {
	name := hostName(host)
	if name == "" {
		return false
	}
	if _, ok := h[strings.ToLower(name)]; ok {
		return true
	}
	addr, err := netip.ParseAddr(name)
	if err != nil || !local.IsValid() {
		return false
	}
	return addr.WithZone("").Unmap() == local.WithZone("").Unmap()
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

// localAddr is the address r arrived on, or the zero Addr when unknown.
func localAddr(r *http.Request) netip.Addr {
	a, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if !ok {
		return netip.Addr{}
	}
	ap, err := netip.ParseAddrPort(a.String())
	if err != nil {
		return netip.Addr{}
	}
	return ap.Addr()
}

// guard refuses a request whose Host is outside the allowlist before next runs.
func (h hostAllowlist) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.allows(r.Host, localAddr(r)) {
			msg := fmt.Sprintf("tkv: host %q not allowed", r.Host)
			if name := hostName(r.Host); name != "" {
				msg += fmt.Sprintf("; add %s with tkv hosts on the tkv machine, then restart tkv", name)
			}
			http.Error(w, msg, http.StatusMisdirectedRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
}
