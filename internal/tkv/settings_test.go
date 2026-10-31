package tkv

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/p3bot/tk/internal/passhash"
	"github.com/p3bot/tk/internal/tkvcert"
	"github.com/p3bot/tk/internal/tkvconf"
)

func loadConf(t *testing.T, app *App) tkvconf.Config {
	t.Helper()
	c, err := tkvconf.Load(app.Ctx, app.ConfigDir)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestListenCommand(t *testing.T) {
	app := newTestApp(t)
	if out, _, err := executeCapture(t, app, "listen"); err != nil || out != "" {
		t.Fatalf("empty show = %q %v", out, err)
	}
	_, stderr, err := executeCapture(t, app, "listen", "[::]:9000")
	if err != nil || !strings.Contains(stderr, "restarted") {
		t.Fatalf("store = %v %q", err, stderr)
	}
	if out, _, _ := executeCapture(t, app, "listen"); out != "[::]:9000\n" {
		t.Fatalf("show = %q", out)
	}
	if _, _, err := executeCapture(t, app, "listen", "LOCALHOST"); err != nil {
		t.Fatal(err)
	}
	if c := loadConf(t, app); c.Listen != (tkvconf.Listen{Host: "localhost"}) {
		t.Fatalf("a bare host replaces the whole address: %+v", c.Listen)
	}
	if _, _, err := executeCapture(t, app, "listen", "--clear"); err != nil {
		t.Fatal(err)
	}
	if out, _, _ := executeCapture(t, app, "listen"); out != "" {
		t.Fatalf("cleared show = %q", out)
	}
	fi, err := os.Stat(tkvconf.Path(app.ConfigDir))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("tkv.cue mode = %v %v", fi.Mode(), err)
	}
}

func TestHostsCommand(t *testing.T) {
	app := newTestApp(t)
	_, stderr, err := executeCapture(t, app, "hosts", "NAS.lan", "gdc.local", "nas.lan", "192.168.1.5")
	if err != nil || !strings.Contains(stderr, "restarted") {
		t.Fatalf("replace = %v %q", err, stderr)
	}
	if out, _, _ := executeCapture(t, app, "hosts"); out != "nas.lan\ngdc.local\n192.168.1.5\n" {
		t.Fatalf("show = %q", out)
	}
	if _, _, err := executeCapture(t, app, "hosts", "only"); err != nil {
		t.Fatal(err)
	}
	if c := loadConf(t, app); !slices.Equal(c.Hosts, []string{"only"}) {
		t.Fatalf("hosts replace the list: %v", c.Hosts)
	}
	if _, _, err := executeCapture(t, app, "hosts", "--clear"); err != nil {
		t.Fatal(err)
	}
	if out, _, _ := executeCapture(t, app, "hosts"); out != "" {
		t.Fatalf("cleared = %q", out)
	}
}

func TestUserCommands(t *testing.T) {
	app := newTestApp(t)
	app.Stdin = strings.NewReader("correct horse\r\n")
	if _, _, err := executeCapture(t, app, "user", "set", "grant"); err != nil {
		t.Fatal(err)
	}
	hash := loadConf(t, app).Users["grant"]
	if ok, err := passhash.Verify(hash, "correct horse"); !ok || err != nil {
		t.Fatalf("stored hash %q does not verify: %v", hash, err)
	}
	if !strings.HasPrefix(hash, "pbkdf2-sha256$600000$") {
		t.Fatalf("hash = %q", hash)
	}
	app.Stdin = strings.NewReader("short")
	if _, _, err := executeCapture(t, app, "user", "set", "other"); err == nil || !strings.Contains(err.Error(), "at least 8") {
		t.Fatalf("short password err = %v", err)
	}
	app.Stdin = strings.NewReader("")
	if _, _, err := executeCapture(t, app, "user", "set", "other"); err == nil || !strings.Contains(err.Error(), "no password") {
		t.Fatalf("empty stdin err = %v", err)
	}
	if out, _, _ := executeCapture(t, app, "user", "list"); out != "grant\n" {
		t.Fatalf("list = %q", out)
	}
	if _, _, err := executeCapture(t, app, "user", "delete", "nobody"); err == nil {
		t.Fatal("deleting an absent user must fail")
	}
	if _, _, err := executeCapture(t, app, "user", "delete", "grant"); err != nil {
		t.Fatal(err)
	}
	if out, _, _ := executeCapture(t, app, "user", "list"); out != "" {
		t.Fatalf("list after delete = %q", out)
	}
	b, err := os.ReadFile(tkvconf.Path(app.ConfigDir))
	if err != nil || strings.Contains(string(b), "correct horse") {
		t.Fatalf("tkv.cue = %q %v", b, err)
	}
}

func TestSettingUsageErrors(t *testing.T) {
	cases := []struct {
		args []string
		msg  string
	}{
		{[]string{"--listen", "nas.lan"}, "tkv hosts nas.lan"},
		{[]string{"start", "--listen", "nas.lan:9000"}, "tkv hosts nas.lan"},
		{[]string{"foreground", "--listen", "fe80::1%eth0"}, "zoned"},
		{[]string{"listen", "nas.lan"}, "tkv hosts nas.lan"},
		{[]string{"listen", "--clear", "::"}, "--clear takes no address"},
		{[]string{"listen", "a", "b"}, "too many arguments"},
		{[]string{"stop", "--listen", "0.0.0.0"}, "unknown flag: --listen"},
		{[]string{"status", "--listen", "0.0.0.0"}, "unknown flag: --listen"},
		{[]string{"listen", "--listen", "0.0.0.0"}, "unknown flag: --listen"},
		{[]string{"hosts", "--port", "1"}, "unknown flag: --port"},
		{[]string{"user", "list", "--listen", "::"}, "unknown flag: --listen"},
		{[]string{"hosts", "bad name"}, "not a hostname"},
		{[]string{"hosts", "--clear", "x"}, "--clear takes no names"},
		{[]string{"user", "set", "Grant"}, "must match"},
		{[]string{"user", "set"}, "want one NAME"},
		{[]string{"user", "delete", "a", "b"}, "want one NAME"},
		{[]string{"user", "bogus"}, `unknown command "bogus"`},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			_, _, err := executeCapture(t, newTestApp(t), tc.args...)
			var usage *UsageError
			if !errors.As(err, &usage) || !strings.Contains(err.Error(), tc.msg) {
				t.Fatalf("err = %v, want usage error with %q", err, tc.msg)
			}
		})
	}
}

func TestListenPortDisagreementIsRefused(t *testing.T) {
	app := newTestApp(t)
	_, _, err := executeCapture(t, app, "start", "--listen", "0.0.0.0:9000", "--port", "9001", "--no-open")
	if err == nil || !strings.Contains(err.Error(), "disagree") {
		t.Fatalf("err = %v", err)
	}
}

func TestLooseConfigWarns(t *testing.T) {
	app := newTestApp(t)
	setUsers(t, app, map[string]string{"grant": "x"})
	if err := os.Chmod(tkvconf.Path(app.ConfigDir), 0o644); err != nil {
		t.Fatal(err)
	}
	_, stderr, err := executeCapture(t, app, "user", "list")
	if err != nil || !strings.Contains(stderr, "group or other") || !strings.Contains(stderr, "chmod 600") {
		t.Fatalf("stderr = %q err=%v", stderr, err)
	}
}

func TestNetworkRunRefusesWithoutUsers(t *testing.T) {
	app := newTestApp(t)
	spec, _ := parseListen("0.0.0.0:" + strconv.Itoa(freePort(t)))
	app.Listen = &spec
	err := app.Run()
	if !errors.Is(err, errNoUsers) || !strings.Contains(err.Error(), "tkv user set NAME") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(app.StateDir, lockFileName)); !os.IsNotExist(err) {
		t.Fatal("refusal must come before the lock")
	}
}

// loopbackSocket keeps a test's socket on this machine. The bind still
// decides the mode, so a 0.0.0.0 bind runs network mode for real.
func loopbackSocket(b bind) (net.Listener, error) {
	return listen(bind{b.probeAddr()})
}

type runningNet struct {
	app    *App
	port   int
	stdout *concBuf
	stderr *concBuf
	cancel context.CancelFunc
	done   chan error
}

func startNetworkRun(t *testing.T, app *App, listenHost string) *runningNet {
	t.Helper()
	port := freePort(t)
	spec, err := parseListen(net.JoinHostPort(listenHost, strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	app.Listen = &spec
	app.listenFn = loopbackSocket
	app.hostname = func() string { return "gdc" }
	app.ifaceAddrs = func() []netip.Addr { return []netip.Addr{netip.MustParseAddr("192.168.1.5")} }
	ctx, cancel := context.WithCancel(context.Background())
	app.ServeCtx = ctx
	r := &runningNet{app: app, port: port, stdout: &concBuf{}, stderr: &concBuf{}, cancel: cancel, done: make(chan error, 1)}
	app.Stdout, app.Stderr = r.stdout, r.stderr
	go func() { r.done <- app.Run() }()
	deadline := time.Now().Add(5 * time.Second)
	for strings.TrimSpace(r.stdout.String()) == "" {
		select {
		case err := <-r.done:
			t.Fatalf("Run exited: %v\n%s", err, r.stderr.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("Run did not print a URL: %s", r.stderr.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Cleanup(func() {
		cancel()
		<-r.done
	})
	return r
}

func TestNetworkRunServesHTTPSWithLogin(t *testing.T) {
	app := newTestApp(t)
	setUsers(t, app, map[string]string{"grant": cheapHash(t, "password1")})
	r := startNetworkRun(t, app, "0.0.0.0")
	base := fmt.Sprintf("https://localhost:%d", r.port)
	if got := strings.TrimSpace(r.stdout.String()); got != base+"/" {
		t.Fatalf("landing = %q", got)
	}
	dir := tkvcert.Dir(app.StateDir)
	leaf, err := tkvcert.ReadLeaf(dir)
	if err != nil {
		t.Fatal(err)
	}
	fp := tkvcert.Fingerprint(leaf.Raw)
	banner := r.stderr.String()
	for _, want := range []string{"network mode on 0.0.0.0:" + strconv.Itoa(r.port), "login required", fp, fmt.Sprintf("https://gdc:%d/", r.port), fmt.Sprintf("https://192.168.1.5:%d/", r.port)} {
		if !strings.Contains(banner, want) {
			t.Errorf("banner lacks %q:\n%s", want, banner)
		}
	}
	for _, name := range []string{"cert.pem", "key.pem", "session.key"} {
		fi, err := os.Stat(filepath.Join(dir, name))
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("%s mode = %v %v", name, fi.Mode(), err)
		}
	}

	var status concBuf
	app.Stdout = &status
	if err := app.Status(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"mode network\n", "url " + base + "/\n", "fingerprint " + fp + "\n", fmt.Sprintf("remote https://gdc:%d/", r.port)} {
		if !strings.Contains(status.String(), want) {
			t.Errorf("status lacks %q:\n%s", want, status.String())
		}
	}
	if strings.Contains(status.String(), "pbkdf2") {
		t.Fatal("status leaks a hash")
	}

	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{
			RootCAs:    tkvcert.Pool(leaf),
			ServerName: "127.0.0.1",
		}},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	ip := fmt.Sprintf("https://127.0.0.1:%d", r.port)
	resp, err := client.Get(ip + "/")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(resp.Header.Get("Location"), "/login") {
		t.Fatalf("GET / = %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	attacher := newTestApp(t)
	attacher.ConfigDir, attacher.StateDir = app.ConfigDir, app.StateDir
	attacher.hostname = app.hostname
	attacher.ifaceAddrs = app.ifaceAddrs
	var attachOut, attachErr concBuf
	attacher.Stdout, attacher.Stderr = &attachOut, &attachErr
	if err := attacher.Start(); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(attachOut.String()) != base+"/" || !strings.Contains(attachErr.String(), fp) {
		t.Fatalf("attach = %q banner %q", attachOut.String(), attachErr.String())
	}
	other, _ := parseListen(fmt.Sprintf("127.0.0.1:%d", r.port))
	attacher.Listen = &other
	if err := attacher.Start(); err == nil || !strings.Contains(err.Error(), "already running on 0.0.0.0") {
		t.Fatalf("an explicit different host must be refused: %v", err)
	}
	if strings.Contains(r.stderr.String(), "pbkdf2") || strings.Contains(r.stderr.String(), "password1") {
		t.Fatal("a secret reached the server log")
	}

	plain, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", r.port))
	if err != nil {
		t.Fatal(err)
	}
	_ = plain.Body.Close()
	if plain.StatusCode != http.StatusBadRequest {
		t.Fatalf("plain HTTP on the TLS port = %d", plain.StatusCode)
	}
}

func TestLoopbackRunOnIPv6(t *testing.T) {
	ipv6Loopback(t)
	app := newTestApp(t)
	r := startNetworkRun(t, app, "::1")
	want := fmt.Sprintf("http://[::1]:%d/", r.port)
	if got := strings.TrimSpace(r.stdout.String()); got != want {
		t.Fatalf("landing = %q", got)
	}
	resp, err := http.Get(want)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Request.Host != fmt.Sprintf("[::1]:%d", r.port) {
		t.Fatalf("GET = %d host %q", resp.StatusCode, resp.Request.Host)
	}
	if strings.Contains(r.stderr.String(), "network mode") {
		t.Fatal("loopback prints no network banner")
	}
	if _, err := os.Stat(tkvcert.Dir(app.StateDir)); !os.IsNotExist(err) {
		t.Fatal("loopback mode makes no key material")
	}
}

// The certificate follows the stable name set across restarts.
func TestCertificateFollowsHosts(t *testing.T) {
	dir := t.TempDir()
	b := mustBind("0.0.0.0:8736")
	open := func(hosts []string) (string, bool) {
		m, regen, err := tkvcert.Open(dir, stableNames(b, "gdc", hosts), time.Now)
		if err != nil {
			t.Fatal(err)
		}
		return tkvcert.Fingerprint(m.Leaf().Raw), regen
	}
	fp1, _ := open(nil)
	if fp, regen := open(nil); regen || fp != fp1 {
		t.Fatal("restart with the same names keeps the certificate")
	}
	if fp, regen := open([]string{"nas.lan"}); !regen || fp == fp1 {
		t.Fatal("tkv hosts NAME regenerates on the next start")
	}
}
