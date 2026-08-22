package tkv

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"cuelang.org/go/cue/cuecontext"

	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/registry"
	"github.com/p3bot/tk/internal/resolve"
	"github.com/p3bot/tk/internal/scopeconfig"
)

func newTestApp(t *testing.T) *App {
	t.Helper()
	return &App{
		Ctx:       cuecontext.New(),
		ConfigDir: t.TempDir(),
		StateDir:  t.TempDir(),
		Cwd:       t.TempDir(),
		Port:      DefaultPort,
		NoOpen:    true,
		Stdout:    io.Discard,
		Stderr:    io.Discard,
		LookupEnv: func(string) string { return "" },
		OpenBrowser: func(string) error {
			t.Fatal("browser opened")
			return nil
		},
	}
}

func registerScope(t *testing.T, app *App, name, dir, root string) {
	t.Helper()
	store := registry.NewStore(app.Ctx, app.ConfigDir)
	reg, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if root == "" {
		root = dir
	}
	reg.Scopes[name] = registry.Entry{Dir: dir, Root: root}
	if err := store.WriteRegistry(reg.Scopes); err != nil {
		t.Fatal(err)
	}
}

func initScope(t *testing.T, app *App, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := scopeconfig.WriteMinimal(dir, name, false); err != nil {
		t.Fatal(err)
	}
	registerScope(t, app, name, dir, dir)
	return dir
}

func addTicket(t *testing.T, dir, id, slug, status, order, body string, archived bool, extraFM string) {
	t.Helper()
	name := id + "-" + slug + ".md"
	target := dir
	if archived {
		target = filepath.Join(dir, "archive")
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	fm := "---\nid: " + id + "\nstatus: " + status + "\norder: \"" + order + "\"\ncreated: 2026-01-01T00:00:00Z\n" + extraFM + "---\n"
	if err := os.WriteFile(filepath.Join(target, name), []byte(fm+body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustServer(t *testing.T, app *App) *Server {
	t.Helper()
	s, err := app.NewServer()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func do(s *Server, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	return w
}

func TestLandingAmbientScope(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	app.Cwd = dir
	path, err := app.LandingPath()
	if err != nil {
		t.Fatal(err)
	}
	if path != "/scope/wc" {
		t.Fatalf("landing = %q", path)
	}
	url, err := app.LandingURL(8736)
	if err != nil {
		t.Fatal(err)
	}
	if url != "http://127.0.0.1:8736/scope/wc" {
		t.Fatalf("url = %q", url)
	}
}

func TestLandingNoAmbient(t *testing.T) {
	app := newTestApp(t)
	initScope(t, app, "wc")
	path, err := app.LandingPath()
	if err != nil {
		t.Fatal(err)
	}
	if path != "/" {
		t.Fatalf("landing = %q", path)
	}
}

func TestLandingScopeFlagAndEnv(t *testing.T) {
	app := newTestApp(t)
	initScope(t, app, "aa")
	initScope(t, app, "bb")
	app.EnvScope = "bb"
	path, err := app.LandingPath()
	if err != nil {
		t.Fatal(err)
	}
	if path != "/scope/bb" {
		t.Fatalf("env landing = %q", path)
	}
	app.ScopeFlag = "aa"
	path, err = app.LandingPath()
	if err != nil {
		t.Fatal(err)
	}
	if path != "/scope/aa" {
		t.Fatalf("flag landing = %q", path)
	}
}

func TestLandingNameDriftFailsClosed(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	if err := scopeconfig.WriteMinimal(dir, "other", false); err != nil {
		t.Fatal(err)
	}
	app.Cwd = dir
	_, err := app.LandingPath()
	if err == nil {
		t.Fatal("expected name-drift")
	}
	var de *resolve.DriftError
	if !errors.As(err, &de) {
		t.Fatalf("err = %v", err)
	}
}

func TestBrowserCmdDetachesFromTerminal(t *testing.T) {
	cmd, err := browserCmd("http://127.0.0.1:8736/")
	if err != nil {
		t.Fatal(err)
	}
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
		t.Fatalf("browser must start in a new session so Ctrl-C on tkv cannot kill it, got %+v", cmd.SysProcAttr)
	}
}

func TestMaybeOpenHonoursNoOpen(t *testing.T) {
	app := newTestApp(t)
	opened := 0
	app.OpenBrowser = func(string) error { opened++; return nil }
	app.NoOpen = true
	if err := app.maybeOpen("http://127.0.0.1:8736/"); err != nil {
		t.Fatal(err)
	}
	if opened != 0 {
		t.Fatalf("opened %d", opened)
	}
	app.NoOpen = false
	if err := app.maybeOpen("http://127.0.0.1:8736/"); err != nil {
		t.Fatal(err)
	}
	if opened != 1 {
		t.Fatalf("opened %d", opened)
	}
}

func TestListenBindFailureNamesPort(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Close() }()
	port := held.Addr().(*net.TCPAddr).Port
	_, err = listen(port)
	if err == nil {
		t.Fatal("expected bind failure")
	}
	msg := err.Error()
	if !strings.Contains(msg, "--port") || !strings.Contains(msg, "127.0.0.1") {
		t.Fatalf("bind error = %q", msg)
	}
	if strings.Contains(msg, "0.0.0.0") {
		t.Fatalf("must not mention 0.0.0.0: %q", msg)
	}
}

func TestRunDoesNotAdvertiseWhenIndexFails(t *testing.T) {
	app := newTestApp(t)
	blocked := filepath.Join(t.TempDir(), "notdir")
	if err := os.WriteFile(blocked, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	app.StateDir = blocked
	app.NoOpen = false
	opened := 0
	app.OpenBrowser = func(string) error { opened++; return nil }
	var out bytes.Buffer
	app.Stdout = &out
	err := app.Run(nil)
	if err == nil {
		t.Fatal("expected index open failure")
	}
	if opened != 0 {
		t.Fatalf("opened browser %d times", opened)
	}
	if strings.Contains(out.String(), "http://") {
		t.Fatalf("printed URL before index open: %q", out.String())
	}
}

func TestRunPrintsURLAfterServing(t *testing.T) {
	app := newTestApp(t)
	initScope(t, app, "wc")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app.ServeCtx = ctx
	app.NoOpen = true
	out := &concBuf{}
	app.Stdout = out

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	errCh := make(chan error, 1)
	go func() {
		errCh <- app.Run([]string{"--port", strconv.Itoa(port), "--no-open"})
	}()

	deadline := time.Now().Add(3 * time.Second)
	var printed string
	for time.Now().Before(deadline) {
		printed = strings.TrimSpace(out.String())
		if printed != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if printed != fmt.Sprintf("http://127.0.0.1:%d/", port) {
		cancel()
		t.Fatalf("printed %q", printed)
	}
	resp, err := http.Get(printed)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		cancel()
		t.Fatalf("GET landing after URL print = %d", resp.StatusCode)
	}
	cancel()
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
}

type concBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (c *concBuf) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.b.Write(p)
}

func (c *concBuf) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.b.String()
}

func TestListenBindsLoopback(t *testing.T) {
	ln, err := listen(0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	addr := ln.Addr().String()
	if !strings.HasPrefix(addr, "127.0.0.1:") {
		t.Fatalf("addr = %q", addr)
	}
}

func TestOverviewAndKanbanAndInspect(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "network", "todo", "a0", "# Network redesign\n\nbody from **file**\n", false, "summary: net\ntags: [frontend]\n")
	addTicket(t, dir, "wc-de34", "auth", "todo", "a1", "# Auth flow\n", false, "depends: [wc-ab2c]\n")
	addTicket(t, dir, "wc-gh56", "old", "done", "a2", "# Old work\n", true, "")

	s := mustServer(t, app)

	home := do(s, "/")
	if home.Code != 200 {
		t.Fatalf("GET / = %d %s", home.Code, home.Body.String())
	}
	body := home.Body.String()
	if !strings.Contains(body, `href="/scope/wc"`) || !strings.Contains(body, "wc-ab2c") {
		t.Fatalf("overview missing scope/next: %s", body)
	}

	board := do(s, "/scope/wc")
	if board.Code != 200 {
		t.Fatalf("kanban = %d %s", board.Code, board.Body.String())
	}
	b := board.Body.String()
	if !strings.Contains(b, "ab2c") || !strings.Contains(b, "Network redesign") {
		t.Fatalf("kanban missing card: %s", b)
	}
	if !strings.Contains(b, "waiting") || !strings.Contains(b, "wc-ab2c") {
		t.Fatalf("kanban missing waiting-on: %s", b)
	}
	if strings.Contains(b, "Old work") {
		t.Fatalf("default board showed archived done: %s", b)
	}

	all := do(s, "/scope/wc?all=1")
	if !strings.Contains(all.Body.String(), "Old work") {
		t.Fatalf("--all missing done: %s", all.Body.String())
	}

	tagged := do(s, "/scope/wc?tag=frontend")
	tb := tagged.Body.String()
	if !strings.Contains(tb, "Network redesign") || strings.Contains(tb, "Auth flow") {
		t.Fatalf("tag filter = %s", tb)
	}

	missingScope := do(s, "/scope/zz")
	if missingScope.Code != 404 {
		t.Fatalf("unknown scope = %d", missingScope.Code)
	}

	ins := do(s, "/scope/wc/ab2c")
	if ins.Code != 200 {
		t.Fatalf("inspect = %d %s", ins.Code, ins.Body.String())
	}
	ib := ins.Body.String()
	if !strings.Contains(ib, "body from") || !strings.Contains(ib, "<strong>file</strong>") {
		t.Fatalf("inspect body not goldmark from file: %s", ib)
	}
	if !strings.Contains(ib, "wc-ab2c") || !strings.Contains(ib, "todo") {
		t.Fatalf("inspect sidebar: %s", ib)
	}

	full := do(s, "/scope/wc/wc-ab2c")
	if full.Code != 200 {
		t.Fatalf("full-id inspect = %d", full.Code)
	}
	wrongScope := do(s, "/scope/wc/aa-ab2c")
	if wrongScope.Code != 404 {
		t.Fatalf("foreign full id = %d", wrongScope.Code)
	}
	missing := do(s, "/scope/wc/zz99")
	if missing.Code != 404 {
		t.Fatalf("missing id = %d", missing.Code)
	}

	dep := do(s, "/scope/wc/de34")
	db := dep.Body.String()
	if !strings.Contains(db, "depends on") || !strings.Contains(db, "wc-ab2c") {
		t.Fatalf("depends neighbourhood: %s", db)
	}
}

func TestKanbanNextRefreshesDependsTargetScope(t *testing.T) {
	app := newTestApp(t)
	wc := initScope(t, app, "wc")
	other := initScope(t, app, "other")
	addTicket(t, other, "other-ab2c", "prereq", "todo", "a0", "# Prereq\n", false, "")
	addTicket(t, wc, "wc-de34", "auth", "todo", "a0", "# Auth\n", false, "depends: [other-ab2c]\n")
	s := mustServer(t, app)

	held := do(s, "/scope/wc")
	if held.Code != 200 {
		t.Fatalf("kanban = %d %s", held.Code, held.Body.String())
	}
	hb := held.Body.String()
	if !strings.Contains(hb, "waiting") || !strings.Contains(hb, "other-ab2c") {
		t.Fatalf("expected waiting on the other-scope prereq: %s", hb)
	}
	if strings.Contains(hb, `href="/scope/wc/wc-de34"`) {
		t.Fatalf("next should be empty while the prereq is open: %s", hb)
	}

	if err := os.Remove(filepath.Join(other, "other-ab2c-prereq.md")); err != nil {
		t.Fatal(err)
	}
	addTicket(t, other, "other-ab2c", "prereq", "done", "a0", "# Prereq\n", true, "")

	ready := do(s, "/scope/wc")
	if ready.Code != 200 {
		t.Fatalf("kanban after prereq done = %d %s", ready.Code, ready.Body.String())
	}
	rb := ready.Body.String()
	if strings.Contains(rb, "waiting") {
		t.Fatalf("kanban should refresh the other scope and clear waiting: %s", rb)
	}
	if !strings.Contains(rb, `href="/scope/wc/wc-de34"`) {
		t.Fatalf("next should be wc-de34 once the prereq is done: %s", rb)
	}
}

func TestSchemaErrorHoldsFromNextAndSurfaces(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-de34", "broken", "todo", "a0", "# Broken\n", false, "depends: [bogus]\n")
	addTicket(t, dir, "wc-ab2c", "clean", "todo", "a1", "# Clean\n", false, "")
	s := mustServer(t, app)

	home := do(s, "/")
	if home.Code != 200 {
		t.Fatalf("overview = %d %s", home.Code, home.Body.String())
	}
	hb := home.Body.String()
	if !strings.Contains(hb, "wc-ab2c") {
		t.Fatalf("overview next should skip schema_error and pick the clean todo: %s", hb)
	}
	if strings.Contains(hb, "wc-de34") {
		t.Fatalf("overview next picked the schema_error todo: %s", hb)
	}

	board := do(s, "/scope/wc")
	if board.Code != 200 {
		t.Fatalf("kanban = %d %s", board.Code, board.Body.String())
	}
	b := board.Body.String()
	if !strings.Contains(b, `href="/scope/wc/wc-ab2c"`) {
		t.Fatalf("kanban next should be the clean todo: %s", b)
	}
	if strings.Contains(b, `next <a href="/scope/wc/wc-de34"`) {
		t.Fatalf("kanban next picked the schema_error todo: %s", b)
	}
	if !strings.Contains(b, "schema_error:") || !strings.Contains(b, "Broken") {
		t.Fatalf("kanban should surface schema_error on the broken card: %s", b)
	}

	ins := do(s, "/scope/wc/de34")
	if ins.Code != 200 {
		t.Fatalf("inspect = %d %s", ins.Code, ins.Body.String())
	}
	ib := ins.Body.String()
	if !strings.Contains(ib, "schema_error: a depends/related entry is not a legal full ticket id") {
		t.Fatalf("inspect missing schema_error banner: %s", ib)
	}
}

func TestDuplicateIDRefuse(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "one", "todo", "a0", "# One\n", false, "")
	addTicket(t, dir, "wc-ab2c", "two", "todo", "a1", "# Two\n", false, "")
	s := mustServer(t, app)
	w := do(s, "/scope/wc/ab2c")
	if w.Code != http.StatusConflict {
		t.Fatalf("duplicate code = %d %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "claimed by") || !strings.Contains(body, "wc-ab2c-one.md") || !strings.Contains(body, "wc-ab2c-two.md") {
		t.Fatalf("duplicate body = %s", body)
	}
}

func TestSearchEmptyAndMalformed(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "network", "todo", "a0", "# Network redesign\n\nsockets\n", false, "summary: net\n")
	s := mustServer(t, app)

	empty := do(s, "/search?q=zzzznotfoundtoken")
	if empty.Code != 200 {
		t.Fatalf("empty search = %d %s", empty.Code, empty.Body.String())
	}
	if !strings.Contains(empty.Body.String(), "No matches.") {
		t.Fatalf("empty list copy: %s", empty.Body.String())
	}

	hit := do(s, "/search?q=network")
	if hit.Code != 200 {
		t.Fatalf("search = %d", hit.Code)
	}
	hb := hit.Body.String()
	if !strings.Contains(hb, "wc-ab2c") || !strings.Contains(hb, "Network redesign") || !strings.Contains(hb, "/scope/wc/wc-ab2c") {
		t.Fatalf("hit = %s", hb)
	}

	bad := do(s, `/search?q=foo"`)
	if bad.Code != 400 {
		t.Fatalf("malformed FTS = %d %s", bad.Code, bad.Body.String())
	}
}

func TestGoldmarkDoesNotRenderRawHTML(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	body := "# Safe title\n\n**bold**\n\n<script>alert(1)</script>\n\n<img src=x onerror=alert(1)>\n"
	addTicket(t, dir, "wc-ab2c", "xss", "todo", "a0", body, false, "")
	s := mustServer(t, app)
	w := do(s, "/scope/wc/ab2c")
	if w.Code != 200 {
		t.Fatalf("inspect = %d %s", w.Code, w.Body.String())
	}
	got := w.Body.String()
	if !strings.Contains(got, "<strong>bold</strong>") {
		t.Fatalf("expected goldmark strong: %s", got)
	}
	if strings.Contains(got, "<script") || strings.Contains(got, "onerror") {
		t.Fatalf("raw HTML leaked: %s", got)
	}
}

func TestStaticCSS(t *testing.T) {
	app := newTestApp(t)
	s := mustServer(t, app)
	w := do(s, "/static/style.css")
	if w.Code != 200 {
		t.Fatalf("css = %d", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/css") {
		t.Fatalf("content-type = %q", ct)
	}
}

func TestSchemaShapedReopenRetries(t *testing.T) {
	app := newTestApp(t)
	initScope(t, app, "wc")
	s := mustServer(t, app)
	n := 0
	err := s.runRequest(func() error {
		n++
		if n == 1 {
			return fmt.Errorf("no such table: tickets")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("calls = %d, want 2", n)
	}

	other, err := index.Open(app.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Rebuild(); err != nil {
		t.Fatal(err)
	}
	_ = other.Close()
	w := do(s, "/scope/wc")
	if w.Code != 200 {
		t.Fatalf("after reindex = %d %s", w.Code, w.Body.String())
	}
}

func TestIsSchemaShapedAndBusy(t *testing.T) {
	if !isSchemaShaped(fmt.Errorf("no such table: tickets")) {
		t.Fatal("no such table should be schema-shaped")
	}
	if isSchemaShaped(nil) || isBusy(nil) {
		t.Fatal("nil")
	}
}

func TestPrimaryNav(t *testing.T) {
	app := newTestApp(t)
	initScope(t, app, "wc")
	s := mustServer(t, app)

	cases := []struct {
		path, current string
	}{
		{"/", "Overview"},
		{"/scope/wc", "Board"},
		{"/search", "Search"},
		{"/graphs", "Graphs"},
		{"/maintenance", "Maintenance"},
	}
	for _, c := range cases {
		w := do(s, c.path)
		if w.Code != 200 {
			t.Fatalf("%s = %d %s", c.path, w.Code, w.Body.String())
		}
		body := w.Body.String()
		for _, label := range []string{"Overview", "Board", "Search", "Graphs", "Maintenance"} {
			if !strings.Contains(body, ">"+label+"</a>") {
				t.Errorf("%s missing primary nav %s", c.path, label)
			}
		}
		if !strings.Contains(body, `class="current">`+c.current+"</a>") {
			t.Errorf("%s current want %s", c.path, c.current)
		}
		if !strings.Contains(body, `href="/scope/wc"`) {
			t.Errorf("%s missing scope switcher", c.path)
		}
		if !strings.Contains(body, `aria-label="sections"`) {
			t.Errorf("%s missing sections nav", c.path)
		}
	}

	graphs := do(s, "/graphs").Body.String()
	if !strings.Contains(graphs, "Depends") || !strings.Contains(graphs, `href="/graphs/depends"`) {
		t.Errorf("graphs hub: %s", graphs)
	}
	maint := do(s, "/maintenance").Body.String()
	if !strings.Contains(maint, "integrity") || !strings.Contains(maint, "wc") {
		t.Errorf("maintenance hub: %s", maint)
	}

	board := do(s, "/scope/wc").Body.String()
	if !strings.Contains(board, `href="/graphs?scope=wc"`) || !strings.Contains(board, `href="/maintenance?scope=wc"`) {
		t.Errorf("board should carry scope onto machine pages: %s", board)
	}
	kept := do(s, "/graphs?scope=wc")
	if kept.Code != 200 {
		t.Fatalf("graphs?scope=wc = %d", kept.Code)
	}
	kb := kept.Body.String()
	if !strings.Contains(kb, `href="/scope/wc"`) {
		t.Errorf("graphs with scope should keep a board href")
	}
	if !strings.Contains(kb, `class="current">Graphs</a>`) {
		t.Errorf("graphs?scope=wc should keep Graphs current")
	}
}

func TestDependsGraphPage(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "network", "todo", "a0", "# Network\n", false, "")
	addTicket(t, dir, "wc-de34", "auth", "todo", "a1", "# Auth\n", false, "depends: [wc-ab2c]\n")
	addTicket(t, dir, "wc-gh56", "lone", "todo", "a2", "# Lone\n", false, "")
	s := mustServer(t, app)

	pick := do(s, "/graphs/depends")
	if pick.Code != 200 {
		t.Fatalf("picker = %d %s", pick.Code, pick.Body.String())
	}
	if !strings.Contains(pick.Body.String(), "/graphs/depends?scope=wc") {
		t.Fatalf("picker missing scope: %s", pick.Body.String())
	}

	missing := do(s, "/graphs/depends?scope=zz")
	if missing.Code != 404 {
		t.Fatalf("unknown scope = %d", missing.Code)
	}

	w := do(s, "/graphs/depends?scope=wc")
	if w.Code != 200 {
		t.Fatalf("graph = %d %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `class="current">Graphs</a>`) {
		t.Errorf("section should stay Graphs")
	}
	if !strings.Contains(body, "<svg") || !strings.Contains(body, "wc-ab2c") || !strings.Contains(body, "wc-de34") {
		t.Fatalf("missing svg nodes: %s", body)
	}
	if !strings.Contains(body, `href="/scope/wc/wc-de34"`) {
		t.Errorf("nodes should link to inspect")
	}
	if strings.Contains(body, "Lone") {
		t.Errorf("isolated ticket should be omitted: %s", body)
	}
	if !strings.Contains(body, "1 ticket") || !strings.Contains(body, "omitted") {
		t.Errorf("isolated copy: %s", body)
	}
}

func TestParseFlags(t *testing.T) {
	app := newTestApp(t)
	if err := app.parseFlags([]string{"--no-open", "--port", "9001", "--scope", "wc"}); err != nil {
		t.Fatal(err)
	}
	if !app.NoOpen || app.Port != 9001 || app.ScopeFlag != "wc" {
		t.Fatalf("%+v", app)
	}
	if err := app.parseFlags([]string{"--port", "0"}); err == nil {
		t.Fatal("port 0 should fail")
	}
	app2 := newTestApp(t)
	var errBuf bytes.Buffer
	app2.Stderr = &errBuf
	err := app2.parseFlags([]string{"--help"})
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("help err = %v", err)
	}
	if !strings.Contains(errBuf.String(), "tkv is a local") {
		t.Fatalf("usage = %q", errBuf.String())
	}
}
