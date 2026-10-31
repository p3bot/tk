package tkv

import (
	"bytes"
	"context"
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/base64"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/p3bot/tk/internal/passhash"
	"github.com/p3bot/tk/internal/tkvconf"
)

// testNetHost is the Host the network-mode helpers send.
const testNetHost = "gdc:8736"

// cheapHash is a stored hash at one iteration. Verification reads the work
// factor from the string, so it verifies like a production hash, quickly.
func cheapHash(t *testing.T, password string) string {
	t.Helper()
	salt := []byte("0123456789abcdef")
	key, err := pbkdf2.Key(sha256.New, password, salt, 1, 32)
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.RawStdEncoding
	return "pbkdf2-sha256$1$" + enc.EncodeToString(salt) + "$" + enc.EncodeToString(key)
}

func setUsers(t *testing.T, app *App, users map[string]string) {
	t.Helper()
	if err := tkvconf.Update(app.Ctx, app.ConfigDir, func(c *tkvconf.Config) error {
		c.Users = users
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

type netServer struct {
	*Server
	h   http.Handler
	log *concBuf
}

// newNetServer is a network-mode server for a 0.0.0.0 bind with user grant.
func newNetServer(t *testing.T, app *App) *netServer {
	t.Helper()
	setUsers(t, app, map[string]string{"grant": cheapHash(t, "password1")})
	log := &concBuf{}
	au := newAuth(tkvconf.Path(app.ConfigDir), bytes.Repeat([]byte{7}, 32), slog.New(slog.NewTextHandler(log, nil)))
	names := stableNames(mustBind("0.0.0.0:8736"), "gdc", nil)
	s, err := app.newServer(newHostAllowlist(names), au)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return &netServer{Server: s, h: s.Handler(), log: log}
}

func netRequest(method, path, body string) *http.Request {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, "https://"+testNetHost+path, nil)
	} else {
		r = httptest.NewRequest(method, "https://"+testNetHost+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	r.Host = testNetHost
	r.RemoteAddr = "192.168.1.9:50000"
	return r
}

func (n *netServer) serve(r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	n.h.ServeHTTP(w, r)
	return w
}

func (n *netServer) login(t *testing.T, user, password, next string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"username": {user}, "password": {password}, "next": {next}}
	r := netRequest(http.MethodPost, "/login", form.Encode())
	r.Header.Set("Origin", "https://"+testNetHost)
	return n.serve(r)
}

func sessionFrom(t *testing.T, w *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie {
			return c
		}
	}
	t.Fatalf("no session cookie: %v", w.Header())
	return nil
}

func (n *netServer) get(path string, c *http.Cookie) *httptest.ResponseRecorder {
	r := netRequest(http.MethodGet, path, "")
	if c != nil {
		r.AddCookie(c)
	}
	return n.serve(r)
}

var wildcard = regexp.MustCompile(`\{[^}]*\}`)

// concretePath turns a mux pattern's path into a request path.
func concretePath(pattern string) string {
	_, path, _ := strings.Cut(pattern, " ")
	path = strings.ReplaceAll(path, "{$}", "")
	return wildcard.ReplaceAllStringFunc(path, func(w string) string {
		switch w {
		case "{name}":
			return "wc"
		case "{id}":
			return "wc-ab2c"
		default:
			return "x"
		}
	})
}

func TestNetworkGateCoversEveryRoute(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "")
	n := newNetServer(t, app)
	n.onRoute = func(r *http.Request) {
		if !gateExempt(r.URL.Path) {
			t.Errorf("handler ran unauthenticated: %s %s", r.Method, r.URL)
		}
	}
	routes := n.routes()
	if len(routes) < 30 {
		t.Fatalf("only %d routes", len(routes))
	}
	for _, rt := range routes {
		path := concretePath(rt.pattern)
		if gateExempt(path) {
			continue
		}
		for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost} {
			r := netRequest(method, path, "x=1")
			r.Header.Set("Origin", "https://"+testNetHost)
			w := n.serve(r)
			if method == http.MethodGet {
				if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login?next="+url.QueryEscape(path) {
					t.Errorf("GET %s = %d %q", path, w.Code, w.Header().Get("Location"))
				}
				continue
			}
			if w.Code != http.StatusUnauthorized {
				t.Errorf("%s %s = %d", method, path, w.Code)
			}
		}
	}
	if !strings.Contains(ticketBody(t, dir, "wc-ab2c"), "status: todo") {
		t.Fatal("an unauthenticated request wrote")
	}
	for _, path := range []string{"/login", "/static/style.css"} {
		if w := n.get(path, nil); w.Code != http.StatusOK {
			t.Errorf("GET %s = %d", path, w.Code)
		}
	}
	if w := n.get("/scope/wc?tag=a&b=1", nil); w.Header().Get("Location") != "/login?next="+url.QueryEscape("/scope/wc?tag=a&b=1") {
		t.Errorf("next keeps the query: %q", w.Header().Get("Location"))
	}
}

// The /static/ exemption must not open a path to a gated page.
func TestStaticExemptionIsNotATraversal(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Secret work\n", false, "")
	n := newNetServer(t, app)
	for _, p := range []string{
		"/static/../scope/wc",
		"/static/%2e%2e/scope/wc",
		"/static/..%2fscope/wc",
		"/static//../doctor",
		"/static/%2e%2e/scope/wc/wc-ab2c",
	} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			r := netRequest(method, "/", "status=done&id=wc-ab2c")
			u, err := r.URL.Parse(p)
			if err != nil {
				t.Fatal(err)
			}
			r.URL = u
			w := n.serve(r)
			if w.Code == http.StatusOK || strings.Contains(w.Body.String(), "Secret work") {
				t.Errorf("%s %s = %d reached a gated page", method, p, w.Code)
			}
		}
	}
	if !strings.Contains(ticketBody(t, dir, "wc-ab2c"), "status: todo") {
		t.Fatal("a traversal request wrote")
	}
}

// SameSite=Lax sends the cookie on a cross-site top-level GET, which is safe
// only while no GET route writes.
func TestNoGETRouteWrites(t *testing.T) {
	app := newTestApp(t)
	for _, s := range []*Server{mustServer(t, app), newNetServer(t, app).Server} {
		for _, rt := range s.routes() {
			method, _, _ := strings.Cut(rt.pattern, " ")
			if rt.engine && method != http.MethodPost {
				t.Errorf("%s dispatches to wrapEngine", rt.pattern)
			}
			if method == http.MethodPost && !rt.engine && rt.pattern != "POST /login" && rt.pattern != "POST /logout" {
				t.Errorf("%s is a POST outside wrapEngine", rt.pattern)
			}
			if method != http.MethodGet && method != http.MethodPost {
				t.Errorf("%s: only GET and POST patterns", rt.pattern)
			}
		}
	}
}

func TestLoopbackHasNoLogin(t *testing.T) {
	app := newTestApp(t)
	initScope(t, app, "wc")
	s := mustServer(t, app)
	w := do(s, "/scope/wc")
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), `action="/logout"`) {
		t.Fatalf("loopback board = %d logout=%v", w.Code, strings.Contains(w.Body.String(), "/logout"))
	}
	if w := do(s, "/login"); w.Code != http.StatusNotFound {
		t.Fatalf("loopback /login = %d", w.Code)
	}
}

func TestLoginSetsSessionCookie(t *testing.T) {
	app := newTestApp(t)
	initScope(t, app, "wc")
	n := newNetServer(t, app)

	w := n.login(t, "grant", "password1", "/scope/wc?x=1")
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/scope/wc?x=1" {
		t.Fatalf("login = %d %q %s", w.Code, w.Header().Get("Location"), w.Body.String())
	}
	c := sessionFrom(t, w)
	if !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.Domain != "" || c.MaxAge != 400*24*3600 {
		t.Fatalf("cookie = %+v", c)
	}
	if !strings.HasPrefix(c.Value, "grant.") || strings.Contains(c.Value[len("grant."):], ".") {
		t.Fatalf("cookie value = %q", c.Value)
	}

	page := n.get("/scope/wc", c)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `action="/logout"`) {
		t.Fatalf("board = %d logout=%v", page.Code, strings.Contains(page.Body.String(), "/logout"))
	}
	if again := sessionFrom(t, page); again.Value != c.Value || again.MaxAge != c.MaxAge {
		t.Fatalf("re-issued cookie = %+v", again)
	}

	r := netRequest(http.MethodPost, "/logout", "")
	r.Header.Set("Origin", "https://"+testNetHost)
	r.AddCookie(c)
	out := n.serve(r)
	if out.Code != http.StatusSeeOther || out.Header().Get("Location") != "/login" || sessionFrom(t, out).MaxAge >= 0 {
		t.Fatalf("logout = %d %v", out.Code, out.Header())
	}
	if strings.Contains(n.log.String(), "password1") {
		t.Fatal("password logged")
	}
}

func TestLoginRefusals(t *testing.T) {
	app := newTestApp(t)
	n := newNetServer(t, app)
	n.auth.throttle = newLoginThrottle(func() time.Time { return time.Time{} })

	w := n.login(t, "grant", "wrong-password", "/")
	if w.Code != http.StatusUnauthorized || len(w.Result().Cookies()) != 0 {
		t.Fatalf("wrong password = %d", w.Code)
	}
	n.auth.throttle = newLoginThrottle(func() time.Time { return time.Time{} })
	if w := n.login(t, "hunter2hunter2", "password1", "/"); w.Code != http.StatusUnauthorized {
		t.Fatalf("unknown user = %d", w.Code)
	}
	logged := n.log.String()
	if !strings.Contains(logged, `msg="login failed" client=192.168.1.9 user=grant`) ||
		!strings.Contains(logged, `msg="login failed" client=192.168.1.9 user=unknown`) {
		t.Fatalf("log = %q", logged)
	}
	if strings.Contains(logged, "hunter2") || strings.Contains(logged, "wrong-password") || strings.Contains(logged, "pbkdf2") {
		t.Fatalf("secret logged: %q", logged)
	}

	r := netRequest(http.MethodPost, "/login", url.Values{"username": {"grant"}, "password": {"password1"}}.Encode())
	r.Header.Set("Origin", "http://"+testNetHost)
	if w := n.serve(r); w.Code != http.StatusForbidden {
		t.Fatalf("http origin on a TLS login = %d", w.Code)
	}
	r = netRequest(http.MethodPost, "/login", url.Values{"username": {"grant"}, "password": {"password1"}}.Encode())
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	if w := n.serve(r); w.Code != http.StatusForbidden {
		t.Fatalf("cross-site login = %d", w.Code)
	}
}

func TestLocalNext(t *testing.T) {
	for in, want := range map[string]string{
		"":                  "/",
		"/":                 "/",
		"/scope/wc?a=1":     "/scope/wc?a=1",
		"//evil.example":    "/",
		`/\evil.example`:    "/",
		"https://evil":      "/",
		"scope/wc":          "/",
		"/scope/wc//double": "/scope/wc//double",
		"/\t/evil.example":  "/",
		"/\n/evil.example":  "/",
		"/\r/evil.example":  "/",
		"/\x7f/evil":        "/",
		"/scope/a\\b":       "/",
		"/%09/evil.example": "/%09/evil.example",
		"/scope/wc#frag":    "/scope/wc#frag",
	} {
		if got := localNext(in); got != want {
			t.Errorf("localNext(%q) = %q", in, got)
		}
	}
}

func TestCookieRevocationIsLive(t *testing.T) {
	app := newTestApp(t)
	initScope(t, app, "wc")
	n := newNetServer(t, app)
	setUsers(t, app, map[string]string{"grant": cheapHash(t, "password1"), "a.b": cheapHash(t, "password2")})

	grant := sessionFrom(t, n.login(t, "grant", "password1", "/"))
	dotted := sessionFrom(t, n.login(t, "a.b", "password2", "/"))
	for _, c := range []*http.Cookie{grant, dotted} {
		if w := n.get("/", c); w.Code != http.StatusOK {
			t.Fatalf("%s: GET / = %d", c.Value, w.Code)
		}
	}

	// Same length, so the size is unchanged; the atomic rename changes the inode.
	setUsers(t, app, map[string]string{"grant": cheapHash(t, "password9"), "a.b": cheapHash(t, "password2")})
	if w := n.get("/", grant); w.Code != http.StatusSeeOther {
		t.Fatalf("changed password: GET / = %d", w.Code)
	}
	if w := n.get("/", dotted); w.Code != http.StatusOK {
		t.Fatalf("other user: GET / = %d", w.Code)
	}
	setUsers(t, app, map[string]string{"grant": cheapHash(t, "password9")})
	if w := n.get("/", dotted); w.Code != http.StatusSeeOther {
		t.Fatalf("deleted user: GET / = %d", w.Code)
	}

	grant = sessionFrom(t, n.login(t, "grant", "password9", "/"))
	if err := os.WriteFile(tkvconf.Path(app.ConfigDir), []byte("tkv: {"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/", "/login", "/scope/wc"} {
		if w := n.get(path, grant); w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "tkv.cue") {
			t.Fatalf("broken tkv.cue: GET %s = %d", path, w.Code)
		}
	}
	if w := n.get("/static/style.css", nil); w.Code != http.StatusOK {
		t.Fatalf("static stays up for the 503 page: %d", w.Code)
	}
	if !strings.Contains(n.log.String(), "tkv.cue reload failed") {
		t.Fatalf("log = %q", n.log.String())
	}
	if err := os.Remove(tkvconf.Path(app.ConfigDir)); err != nil {
		t.Fatal(err)
	}
	setUsers(t, app, map[string]string{"grant": cheapHash(t, "password9")})
	if w := n.get("/", grant); w.Code != http.StatusOK {
		t.Fatalf("fixed tkv.cue: GET / = %d", w.Code)
	}
}

func TestSessionValueForgery(t *testing.T) {
	key := bytes.Repeat([]byte{1}, 32)
	users := map[string]string{"grant": "h1", "a.b": "h2"}
	good := sessionValue(key, "grant", "h1")
	if u, ok := sessionUser(key, users, good); !ok || u != "grant" {
		t.Fatal("own cookie must verify")
	}
	for _, v := range []string{
		"", "grant", "grant.", good + "x", "a.b" + good[len("grant"):],
		sessionValue(bytes.Repeat([]byte{2}, 32), "grant", "h1"),
		sessionValue(key, "grant", "h0"),
		sessionValue(key, "nobody", "h1"),
	} {
		if _, ok := sessionUser(key, users, v); ok {
			t.Errorf("%q verified", v)
		}
	}
	if u, ok := sessionUser(key, users, sessionValue(key, "a.b", "h2")); !ok || u != "a.b" {
		t.Fatal("a dotted username splits on the last dot")
	}
}

func TestBackoffSequence(t *testing.T) {
	want := []time.Duration{0, 250 * time.Millisecond, 500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second, 5 * time.Second, 5 * time.Second, 5 * time.Second}
	for n, w := range want {
		if got := backoff(n); got != w {
			t.Errorf("backoff(%d) = %v, want %v", n, got, w)
		}
	}
	if backoff(1000) != 5*time.Second {
		t.Error("large n must cap without overflow")
	}
}

func TestThrottleWaitsAndResets(t *testing.T) {
	now := time.Unix(0, 0)
	th := newLoginThrottle(func() time.Time { return now })
	for n := 1; n <= 4; n++ {
		ok, _ := th.admit()
		if !ok {
			t.Fatalf("attempt %d refused", n)
		}
		th.finish(false)
		if ok, wait := th.admit(); ok || wait != backoff(n) {
			t.Fatalf("after %d failures: ok=%v wait=%v", n, ok, wait)
		}
		now = now.Add(backoff(n) - time.Millisecond)
		if ok, _ := th.admit(); ok {
			t.Fatalf("after %d failures admitted early", n)
		}
		now = now.Add(time.Millisecond)
	}
	ok, _ := th.admit()
	if !ok {
		t.Fatal("refused after the wait")
	}
	th.finish(true)
	if ok, _ := th.admit(); !ok {
		t.Fatal("a success resets the counter")
	}
	th.finish(false)
	if _, wait := th.admit(); wait != backoff(1) {
		t.Fatalf("after reset, wait = %v", wait)
	}
}

func TestConcurrentLoginIsRefusedWithoutHashing(t *testing.T) {
	app := newTestApp(t)
	n := newNetServer(t, app)
	release := make(chan struct{})
	entered := make(chan struct{})
	var mu sync.Mutex
	calls := 0
	n.auth.verify = func(stored, pw string) (bool, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		close(entered)
		<-release
		return passhash.Verify(stored, pw)
	}
	first := make(chan int, 1)
	go func() { first <- n.login(t, "grant", "password1", "/").Code }()
	<-entered
	w := n.login(t, "grant", "password1", "/")
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Fatalf("concurrent attempt = %d", w.Code)
	}
	close(release)
	if code := <-first; code != http.StatusSeeOther {
		t.Fatalf("first attempt = %d", code)
	}
	if calls != 1 {
		t.Fatalf("verify ran %d times", calls)
	}
}

func TestEventSummaryFoldsPerMinute(t *testing.T) {
	var buf concBuf
	var flush func()
	l := newEventSummary(slog.New(slog.NewTextHandler(&buf, nil)), "login attempts refused by throttle", func(d time.Duration, f func()) *time.Timer {
		if d != time.Minute || flush != nil {
			t.Errorf("armed %v twice=%v", d, flush != nil)
		}
		flush = f
		return nil
	})
	for _, ip := range []string{"10.0.0.2", "10.0.0.1", "10.0.0.2"} {
		l.note(ip)
	}
	if buf.String() != "" {
		t.Fatal("events log once per window, not per event")
	}
	flush()
	got := buf.String()
	if strings.Count(got, "\n") != 1 || !strings.Contains(got, `msg="login attempts refused by throttle" count=3 clients=10.0.0.1,10.0.0.2`) {
		t.Fatalf("summary = %q", got)
	}
	flush = nil
	for i := range summaryClientsMax + 5 {
		l.note(net.IPv4(10, 1, 0, byte(i)).String())
	}
	flush()
	if !strings.Contains(buf.String(), "clients_truncated=true") {
		t.Fatalf("summary = %q", buf.String())
	}
}

func TestServerErrorLogFoldsHandshakes(t *testing.T) {
	var buf concBuf
	var flush func()
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	hs := newEventSummary(logger, "TLS handshakes failed", func(_ time.Duration, f func()) *time.Timer {
		flush = f
		return nil
	})
	el := serverErrorLog(logger, hs)
	for i := range 50 {
		el.Printf("http: TLS handshake error from 192.168.1.9:%d: remote error: tls: unknown certificate", 50000+i)
	}
	el.Printf("http: TLS handshake error from [2001:db8::1]:443: EOF")
	el.Printf("http: Accept error: too many open files")
	got := buf.String()
	if strings.Contains(got, "handshake") || !strings.Contains(got, "Accept error") {
		t.Fatalf("before flush = %q", got)
	}
	flush()
	if !strings.Contains(buf.String(), `msg="TLS handshakes failed" count=51 clients=192.168.1.9,2001:db8::1`) {
		t.Fatalf("summary = %q", buf.String())
	}
}

func TestHostGuardLocalAddressRule(t *testing.T) {
	app := newTestApp(t)
	n := newNetServer(t, app)
	local := &net.TCPAddr{IP: net.ParseIP("192.168.1.5"), Port: 8736}
	req := func(host string) *httptest.ResponseRecorder {
		r := netRequest(http.MethodGet, "/", "")
		r.Host = host
		r = r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, net.Addr(local)))
		return n.serve(r)
	}
	if w := req("192.168.1.5:8736"); w.Code != http.StatusSeeOther {
		t.Fatalf("local IP Host = %d", w.Code)
	}
	if w := req("192.168.1.5:9999"); w.Code != http.StatusSeeOther {
		t.Fatalf("port is ignored: %d", w.Code)
	}
	if w := req("GDC"); w.Code != http.StatusSeeOther {
		t.Fatalf("short hostname = %d", w.Code)
	}
	for _, host := range []string{"192.168.1.6:8736", "nas.lan:8736"} {
		w := req(host)
		if w.Code != http.StatusMisdirectedRequest || !strings.Contains(w.Body.String(), host) || !strings.Contains(w.Body.String(), "tkv hosts") {
			t.Fatalf("%s = %d %q", host, w.Code, w.Body.String())
		}
	}
}

// The guard stays outermost: a refused Host gets 421 on every path, signed
// in or not, the login page and static files included.
func TestHostGuardWrapsTheGate(t *testing.T) {
	app := newTestApp(t)
	n := newNetServer(t, app)
	c := sessionFrom(t, n.login(t, "grant", "password1", "/"))
	for _, path := range []string{"/", "/login", "/static/style.css"} {
		for _, cookie := range []*http.Cookie{nil, c} {
			r := netRequest(http.MethodGet, path, "")
			r.Host = "rebind.evil.example"
			if cookie != nil {
				r.AddCookie(cookie)
			}
			if w := n.serve(r); w.Code != http.StatusMisdirectedRequest {
				t.Fatalf("%s cookie=%v = %d", path, cookie != nil, w.Code)
			}
		}
	}
}

func TestCheckWriteOriginFollowsScheme(t *testing.T) {
	cases := []struct {
		tls    bool
		origin string
		ok     bool
	}{
		{false, "http://h:1", true},
		{false, "https://h:1", false},
		{true, "https://h:1", true},
		{true, "http://h:1", false},
		{true, "https://other:1", false},
		{true, "", true},
	}
	for _, tc := range cases {
		target := "http://h:1/x"
		if tc.tls {
			target = "https://h:1/x"
		}
		r := httptest.NewRequest(http.MethodPost, target, nil)
		r.Host = "h:1"
		if tc.origin != "" {
			r.Header.Set("Origin", tc.origin)
		}
		if err := checkWriteOrigin(r); (err == nil) != tc.ok {
			t.Errorf("tls=%v origin=%q err=%v", tc.tls, tc.origin, err)
		}
	}
}
