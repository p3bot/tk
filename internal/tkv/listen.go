package tkv

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/p3bot/tk/internal/tkvcert"
	"github.com/p3bot/tk/internal/tkvconf"
)

// defaultHost is the listen host when neither a flag nor tkv.cue names one.
var defaultHost = netip.AddrFrom4([4]byte{127, 0, 0, 1})

// bind is a resolved listen address. The host is always an unzoned IP
// literal (localhost has become 127.0.0.1) and the port is never 0, so it
// equals the listener's Addr().
type bind struct {
	netip.AddrPort
}

func defaultBind(port int) bind {
	return bind{netip.AddrPortFrom(defaultHost, uint16(port))}
}

// network reports network mode: any host outside loopback, the unspecified
// addresses included. The mode belongs to the listener, not the client.
func (b bind) network() bool {
	return !b.Addr().IsLoopback()
}

func (b bind) modeName() string {
	if b.network() {
		return "network"
	}
	return "loopback"
}

func (b bind) scheme() string {
	if b.network() {
		return "https"
	}
	return "http"
}

// family is the one socket family the host names. Plain "tcp" would turn an
// unspecified bind into one dual-stack socket that also serves every IPv6
// address, globally routable ones included.
func (b bind) family() string {
	if b.Addr().Is4() {
		return "tcp4"
	}
	return "tcp6"
}

// probeAddr is where this machine reaches the listener: the loopback address
// of the bound family for an unspecified bind, else the bound address.
func (b bind) probeAddr() netip.AddrPort {
	addr := b.Addr()
	if addr.IsUnspecified() {
		if addr.Is4() {
			addr = defaultHost
		} else {
			addr = netip.IPv6Loopback()
		}
	}
	return netip.AddrPortFrom(addr, b.Port())
}

// origin is the dashboard origin this machine opens, with no trailing slash.
// A network-mode unspecified bind uses localhost, a certificate name that
// resolves to both loopback families. The short hostname is not used: it
// often resolves to 127.0.1.1, which never reaches a specific-IP bind.
func (b bind) origin() string {
	if b.network() && b.Addr().IsUnspecified() {
		return "https://" + net.JoinHostPort("localhost", strconv.Itoa(int(b.Port())))
	}
	return b.scheme() + "://" + b.String()
}

func (b bind) probeURL() string {
	return b.scheme() + "://" + b.probeAddr().String() + "/static/style.css"
}

// probeClient fetches the readiness URL. In network mode it trusts exactly
// leaf, never InsecureSkipVerify, and names the dialled IP, which is always a
// certificate name.
func (b bind) probeClient(leaf *x509.Certificate) *http.Client {
	c := &http.Client{Timeout: 200 * time.Millisecond}
	if b.network() {
		c.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:    tkvcert.Pool(leaf),
				ServerName: b.probeAddr().Addr().String(),
				MinVersion: tls.VersionTLS12,
			},
			DisableKeepAlives: true,
		}
	}
	return c
}

// listen opens the one-family socket for b.
func listen(b bind) (net.Listener, error) {
	ln, err := net.Listen(b.family(), b.String())
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w (use --port or --listen)", b, err)
	}
	return ln, nil
}

// listenSpec is a parsed HOST or HOST:PORT. port is 0 when absent. host is
// the canonical spelling to store: "localhost" or the address.
type listenSpec struct {
	host string
	addr netip.Addr
	port int
}

// parseListen reads HOST or HOST:PORT. net.SplitHostPort runs first, so a
// bare "::" is a host; IPv6 with a port needs brackets.
func parseListen(s string) (listenSpec, error) {
	host, portStr, err := net.SplitHostPort(s)
	if err != nil {
		host, portStr = s, ""
		if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
			host = host[1 : len(host)-1]
		}
	}
	spec, err := parseListenHost(host)
	if err != nil {
		return listenSpec{}, err
	}
	if portStr != "" {
		n, err := strconv.Atoi(portStr)
		if err != nil || n < 1 || n > 65535 {
			return listenSpec{}, fmt.Errorf("listen port %q %w", portStr, errPortRange)
		}
		spec.port = n
	}
	return spec, nil
}

// parseListenHost accepts an unzoned IP literal or localhost. A name would
// need a DNS answer at bind time to pick the mode, family, and certificate.
func parseListenHost(host string) (listenSpec, error) {
	if host == "" {
		return listenSpec{}, fmt.Errorf("listen address needs a host")
	}
	if strings.EqualFold(host, "localhost") {
		return listenSpec{host: "localhost", addr: defaultHost}, nil
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return listenSpec{}, fmt.Errorf("listen host %q is not an IP address; bind the IP and add the name with: tkv hosts %s", host, host)
	}
	if addr.Zone() != "" {
		return listenSpec{}, fmt.Errorf("listen host %q: a zoned IPv6 address cannot be bound", host)
	}
	if addr.Is4In6() {
		return listenSpec{}, fmt.Errorf("listen host %q: use the IPv4 form %s", host, addr.Unmap())
	}
	return listenSpec{host: addr.String(), addr: addr}, nil
}

func (s listenSpec) String() string {
	if s.port == 0 {
		return s.host
	}
	return net.JoinHostPort(s.host, strconv.Itoa(s.port))
}

// listenRequest is what the command line asked for. A nil listen and a zero
// port were not given.
type listenRequest struct {
	listen *listenSpec
	port   int
}

// resolvedBind is the bind plus which halves the command line fixed. An
// explicit half that differs from a running dashboard refuses to attach.
type resolvedBind struct {
	bind
	hostExplicit bool
	portExplicit bool
}

// resolveBind resolves host and port separately: flags, then tkv.cue, then
// the defaults 127.0.0.1 and 8736.
func resolveBind(req listenRequest, stored tkvconf.Listen) (resolvedBind, error) {
	var r resolvedBind
	host := defaultHost
	port := DefaultPort
	if stored.Host != "" {
		spec, err := parseListenHost(stored.Host)
		if err != nil {
			return r, fmt.Errorf("%s listen.host: %w", tkvconf.FileName, err)
		}
		host = spec.addr
	}
	if stored.Port != 0 {
		port = stored.Port
	}
	if req.port != 0 {
		port = req.port
		r.portExplicit = true
	}
	if req.listen != nil {
		host = req.listen.addr
		r.hostExplicit = true
		if req.listen.port != 0 {
			if req.port != 0 && req.port != req.listen.port {
				return r, fmt.Errorf("--listen port %d and --port %d disagree", req.listen.port, req.port)
			}
			port = req.listen.port
			r.portExplicit = true
		}
	}
	r.bind = bind{netip.AddrPortFrom(host, uint16(port))}
	return r, nil
}

// stableNames is the one name set behind both the certificate and the Host
// allowlist. Interface addresses never enter it: they change with DHCP, VPNs,
// and prefix renewals, and each change would regenerate the certificate.
func stableNames(b bind, hostname string, extra []string) []string {
	names := []string{"localhost", "127.0.0.1", "::1"}
	if !b.Addr().IsUnspecified() {
		names = append(names, b.Addr().String())
	}
	if b.network() {
		if hostname != "" {
			names = append(names, hostname)
		}
		names = append(names, extra...)
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// shortHostname is os.Hostname up to the first dot, or "" when it is not a
// usable hostname.
func shortHostname() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	h, _, _ = strings.Cut(h, ".")
	name, err := tkvconf.CheckHostName(h)
	if err != nil {
		return ""
	}
	return name
}

// interfaceAddrs is the machine's current unicast addresses, for display.
func interfaceAddrs() []netip.Addr {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []netip.Addr
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		if addr, ok := netip.AddrFromSlice(ipnet.IP); ok {
			out = append(out, addr.Unmap())
		}
	}
	return out
}

// remoteURLs are the addresses another machine might use, for display only:
// the short hostname, each extra host, and for an unspecified bind the
// interface addresses of the bound family. Only global unicast addresses
// (private ranges included) are listed: no other machine reaches loopback or
// link-local by that URL.
func remoteURLs(b bind, hostname string, extra []string, ifaces []netip.Addr) []string {
	port := strconv.Itoa(int(b.Port()))
	var hosts []string
	if hostname != "" {
		hosts = append(hosts, hostname)
	}
	hosts = append(hosts, extra...)
	if b.Addr().IsUnspecified() {
		for _, a := range ifaces {
			if a.Is4() != b.Addr().Is4() || !a.IsGlobalUnicast() {
				continue
			}
			hosts = append(hosts, a.String())
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, h := range hosts {
		u := "https://" + net.JoinHostPort(h, port) + "/"
		if !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	return out
}
