package tkv

import (
	"errors"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/p3bot/tk/internal/flock"
	"github.com/p3bot/tk/internal/tkvconf"
)

func mustBind(s string) bind {
	return bind{netip.MustParseAddrPort(s)}
}

func TestParseListen(t *testing.T) {
	ok := []struct {
		in, host string
		port     int
	}{
		{"0.0.0.0", "0.0.0.0", 0},
		{"::", "::", 0},
		{"[::]", "::", 0},
		{"[::]:9000", "::", 9000},
		{"127.0.0.1:9002", "127.0.0.1", 9002},
		{"localhost", "localhost", 0},
		{"LocalHost:80", "localhost", 80},
		{"::1", "::1", 0},
		{"192.168.1.5:8736", "192.168.1.5", 8736},
		{"2001:DB8::1", "2001:db8::1", 0},
	}
	for _, tc := range ok {
		spec, err := parseListen(tc.in)
		if err != nil || spec.host != tc.host || spec.port != tc.port {
			t.Errorf("parseListen(%q) = %+v %v", tc.in, spec, err)
		}
	}
	bad := map[string]string{
		"nas.lan":         "tkv hosts nas.lan",
		"nas.lan:9000":    "tkv hosts nas.lan",
		"fe80::1%eth0":    "zoned",
		"[fe80::1%eth0]":  "zoned",
		"::ffff:10.0.0.1": "IPv4 form 10.0.0.1",
		"1.2.3.4:0":       "1–65535",
		"1.2.3.4:x":       "1–65535",
		":9000":           "needs a host",
		"":                "needs a host",
	}
	for in, want := range bad {
		if _, err := parseListen(in); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("parseListen(%q) err = %v, want %q", in, err, want)
		}
	}
}

func TestResolveBind(t *testing.T) {
	spec := func(s string) *listenSpec {
		sp, err := parseListen(s)
		if err != nil {
			t.Fatal(err)
		}
		return &sp
	}
	cases := []struct {
		name         string
		req          listenRequest
		stored       tkvconf.Listen
		want         string
		hostX, portX bool
	}{
		{"defaults", listenRequest{}, tkvconf.Listen{}, "127.0.0.1:8736", false, false},
		{"stored", listenRequest{}, tkvconf.Listen{Host: "::", Port: 9000}, "[::]:9000", false, false},
		{"stored localhost", listenRequest{}, tkvconf.Listen{Host: "localhost"}, "127.0.0.1:8736", false, false},
		{"port keeps stored host", listenRequest{port: 9001}, tkvconf.Listen{Host: "0.0.0.0", Port: 9000}, "0.0.0.0:9001", false, true},
		{"bare listen keeps stored port", listenRequest{listen: spec("::1")}, tkvconf.Listen{Host: "0.0.0.0", Port: 9000}, "[::1]:9000", true, false},
		{"listen port wins", listenRequest{listen: spec("0.0.0.0:9002")}, tkvconf.Listen{Port: 9000}, "0.0.0.0:9002", true, true},
		{"equal ports", listenRequest{listen: spec("0.0.0.0:9002"), port: 9002}, tkvconf.Listen{}, "0.0.0.0:9002", true, true},
		{"listen plus port", listenRequest{listen: spec("0.0.0.0"), port: 9003}, tkvconf.Listen{}, "0.0.0.0:9003", true, true},
	}
	for _, tc := range cases {
		r, err := resolveBind(tc.req, tc.stored)
		if err != nil || r.String() != tc.want || r.hostExplicit != tc.hostX || r.portExplicit != tc.portX {
			t.Errorf("%s: %+v %v", tc.name, r, err)
		}
	}
	if _, err := resolveBind(listenRequest{listen: spec("0.0.0.0:9000"), port: 9001}, tkvconf.Listen{}); err == nil || !strings.Contains(err.Error(), "disagree") {
		t.Errorf("mismatched ports err = %v", err)
	}
	if _, err := resolveBind(listenRequest{}, tkvconf.Listen{Host: "nas.lan"}); err == nil || !strings.Contains(err.Error(), "tkv.cue listen.host") {
		t.Errorf("stored name err = %v", err)
	}
}

func TestBindModeFamilyAndURLs(t *testing.T) {
	cases := []struct {
		addr, mode, family, probe, origin string
	}{
		{"127.0.0.1:8736", "loopback", "tcp4", "127.0.0.1:8736", "http://127.0.0.1:8736"},
		{"127.0.0.2:8736", "loopback", "tcp4", "127.0.0.2:8736", "http://127.0.0.2:8736"},
		{"[::1]:8736", "loopback", "tcp6", "[::1]:8736", "http://[::1]:8736"},
		{"0.0.0.0:8736", "network", "tcp4", "127.0.0.1:8736", "https://localhost:8736"},
		{"[::]:9000", "network", "tcp6", "[::1]:9000", "https://localhost:9000"},
		{"192.168.1.5:8736", "network", "tcp4", "192.168.1.5:8736", "https://192.168.1.5:8736"},
		{"[2001:db8::1]:8736", "network", "tcp6", "[2001:db8::1]:8736", "https://[2001:db8::1]:8736"},
	}
	for _, tc := range cases {
		b := mustBind(tc.addr)
		if b.modeName() != tc.mode || b.family() != tc.family || b.probeAddr().String() != tc.probe || b.origin() != tc.origin {
			t.Errorf("%s: mode %s family %s probe %s origin %s", tc.addr, b.modeName(), b.family(), b.probeAddr(), b.origin())
		}
	}
	if u := mustBind("0.0.0.0:1").probeURL(); u != "https://127.0.0.1:1/static/style.css" {
		t.Errorf("probe url = %q", u)
	}
	if c := mustBind("127.0.0.1:1").probeClient(nil); c.Transport != nil {
		t.Error("loopback probe must be plain HTTP")
	}
}

func TestStableNames(t *testing.T) {
	cases := []struct {
		addr     string
		hostname string
		extra    []string
		want     []string
	}{
		{"127.0.0.1:1", "gdc", []string{"nas.lan"}, []string{"127.0.0.1", "::1", "localhost"}},
		{"127.0.0.2:1", "gdc", nil, []string{"127.0.0.1", "127.0.0.2", "::1", "localhost"}},
		{"0.0.0.0:1", "gdc", []string{"nas.lan"}, []string{"127.0.0.1", "::1", "gdc", "localhost", "nas.lan"}},
		{"192.168.1.5:1", "", nil, []string{"127.0.0.1", "192.168.1.5", "::1", "localhost"}},
	}
	for _, tc := range cases {
		if got := stableNames(mustBind(tc.addr), tc.hostname, tc.extra); !slices.Equal(got, tc.want) {
			t.Errorf("%s: %v", tc.addr, got)
		}
	}
}

func TestRemoteURLs(t *testing.T) {
	ifaces := []netip.Addr{
		netip.MustParseAddr("127.0.0.1"),
		netip.MustParseAddr("192.168.1.5"),
		netip.MustParseAddr("169.254.1.1"),
		netip.MustParseAddr("::1"),
		netip.MustParseAddr("fe80::1"),
		netip.MustParseAddr("2001:db8::5"),
	}
	got := remoteURLs(mustBind("0.0.0.0:8736"), "gdc", []string{"nas.lan"}, ifaces)
	want := []string{"https://gdc:8736/", "https://nas.lan:8736/", "https://192.168.1.5:8736/"}
	if !slices.Equal(got, want) {
		t.Errorf("v4 = %v", got)
	}
	got = remoteURLs(mustBind("[::]:8736"), "", nil, ifaces)
	if !slices.Equal(got, []string{"https://[2001:db8::5]:8736/"}) {
		t.Errorf("v6 = %v", got)
	}
	got = remoteURLs(mustBind("192.168.1.5:8736"), "gdc", nil, ifaces)
	if !slices.Equal(got, []string{"https://gdc:8736/"}) {
		t.Errorf("specific = %v", got)
	}
}

func ipv6Loopback(t *testing.T) {
	t.Helper()
	ln, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("no IPv6 loopback: %v", err)
	}
	_ = ln.Close()
}

func dialable(network, addr string) bool {
	c, err := net.DialTimeout(network, addr, 300*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// A wildcard bind serves the one family it names.
func TestWildcardBindIsOneFamily(t *testing.T) {
	ipv6Loopback(t)
	port := freePort(t)
	ln, err := listen(mustBind("0.0.0.0:" + strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	if !dialable("tcp4", "127.0.0.1:"+strconv.Itoa(port)) {
		t.Fatal("0.0.0.0 must serve 127.0.0.1")
	}
	if dialable("tcp6", "[::1]:"+strconv.Itoa(port)) {
		t.Fatal("0.0.0.0 must not serve [::1]")
	}
	_ = ln.Close()

	ln, err = listen(mustBind("[::]:" + strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	if !dialable("tcp6", "[::1]:"+strconv.Itoa(port)) {
		t.Fatal(":: must serve [::1]")
	}
	if dialable("tcp4", "127.0.0.1:"+strconv.Itoa(port)) {
		t.Fatal(":: must not serve 127.0.0.1")
	}
}

func TestMetaRecordRoundTrip(t *testing.T) {
	for _, addr := range []string{
		"127.0.0.1:8736",
		"0.0.0.0:1",
		"[::1]:8736",
		"[ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff]:65535",
	} {
		b := mustBind(addr)
		rec := formatMeta(1234567, b)
		if len(rec) != metaSize {
			t.Fatalf("%s: record is %d bytes: %q", addr, len(rec), rec)
		}
		p := filepath.Join(t.TempDir(), "lock")
		if err := os.WriteFile(p, rec, 0o644); err != nil {
			t.Fatal(err)
		}
		h, ok, legacy, err := readMeta(p)
		if err != nil || !ok || legacy || h.pid != 1234567 || h.bind != b {
			t.Fatalf("%s: %+v ok=%v legacy=%v err=%v", addr, h, ok, legacy, err)
		}
	}
	if got := string(formatMeta(42, mustBind("0.0.0.0:8736"))); !strings.HasPrefix(got, "https\n0000000042\n0.0.0.0:8736 ") {
		t.Fatalf("network record = %q", got)
	}
	if got := string(formatMeta(42, mustBind("127.0.0.1:8736"))); !strings.HasPrefix(got, "http \n0000000042\n127.0.0.1:8736 ") {
		t.Fatalf("loopback record = %q", got)
	}
	p := filepath.Join(t.TempDir(), "lock")
	bad := []byte(strings.Replace(string(formatMeta(42, mustBind("0.0.0.0:8736"))), "https", "http ", 1))
	if err := os.WriteFile(p, bad, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok, _, _ := readMeta(p); ok {
		t.Fatal("a scheme that disagrees with the address is not a record")
	}
}

func TestLegacyLockRecordNamesTheFix(t *testing.T) {
	path := filepath.Join(t.TempDir(), lockFileName)
	lk, acquired, err := flock.TryAcquire(path)
	if err != nil || !acquired {
		t.Fatalf("acquired=%v err=%v", acquired, err)
	}
	defer func() { _ = lk.Release() }()
	if err := lk.WriteHead([]byte("0000012345\n08736\n")); err != nil {
		t.Fatal(err)
	}
	// A second descriptor is another holder to the probe in awaitHolder.
	_, err = awaitHolder(path, 50*time.Millisecond)
	if !errors.Is(err, errLegacyHolder) || !strings.Contains(err.Error(), "pkill -f 'tkv foreground'") {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenLogTightensOldLog(t *testing.T) {
	p := filepath.Join(t.TempDir(), logFileName)
	if err := os.WriteFile(p, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := openLog(p)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	fresh := filepath.Join(t.TempDir(), logFileName)
	g, err := openLog(fresh)
	if err != nil {
		t.Fatal(err)
	}
	_ = g.Close()
	for _, path := range []string{p, fresh} {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %v", path, fi.Mode())
		}
	}
}
