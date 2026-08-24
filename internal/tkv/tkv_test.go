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
	"net/url"
	"os"
	"path/filepath"
	"slices"
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
	"github.com/p3bot/tk/internal/status"
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

func TestRunDrainsInFlightWrite(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "")

	started := make(chan struct{})
	unblock := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(unblock) }) }
	app.afterIndexUnlock = func() {
		close(started)
		<-unblock
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		release()
		cancel()
	})
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

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.TrimSpace(out.String()) != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if strings.TrimSpace(out.String()) == "" {
		t.Fatal("Run did not print URL")
	}

	claimCh := make(chan error, 1)
	go func() {
		client := &http.Client{
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
		resp, err := client.PostForm(base+"/scope/wc/claim", url.Values{"return": {"board"}})
		if err != nil {
			claimCh <- err
			return
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusSeeOther {
			claimCh <- fmt.Errorf("claim = %d", resp.StatusCode)
			return
		}
		claimCh <- nil
	}()

	select {
	case <-started:
	case err := <-claimCh:
		t.Fatalf("claim finished before index unlock: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("claim did not drop the index mutex")
	}

	cancel()
	select {
	case err := <-errCh:
		t.Fatalf("Run returned while write in flight: %v", err)
	case <-time.After(200 * time.Millisecond):
	}

	release()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after write finished")
	}
	if err := <-claimCh; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ticketBody(t, dir, "wc-ab2c"), "status: in-progress") {
		t.Fatalf("write must stand: %s", ticketBody(t, dir, "wc-ab2c"))
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
	blPos := strings.Index(b, "<h2>blocked ")
	dPos := strings.Index(b, "<h2>draft ")
	tPos := strings.Index(b, "<h2>todo ")
	iPos := strings.Index(b, "<h2>in-progress ")
	rPos := strings.Index(b, "<h2>review ")
	if blPos < 0 || dPos < 0 || tPos < 0 || iPos < 0 || rPos < 0 ||
		!(blPos < dPos && dPos < tPos && tPos < iPos && iPos < rPos) {
		t.Fatalf("kanban column order want blocked draft todo in-progress review: %s", b)
	}
	if strings.Contains(b, "<h2>backlog ") || strings.Contains(b, "<h2>done ") || strings.Contains(b, "<h2>cancelled ") {
		t.Fatalf("default board showed backlog or archived columns: %s", b)
	}
	if !strings.Contains(b, "waiting") || !strings.Contains(b, "wc-ab2c") {
		t.Fatalf("kanban missing waiting-on: %s", b)
	}
	if strings.Contains(b, `aria-label="pulse"`) || strings.Contains(b, "claimed") {
		t.Fatalf("kanban still has pulse strip: %s", b)
	}
	if !strings.Contains(b, `data-board-filter`) || !strings.Contains(b, `/static/board.js`) {
		t.Fatalf("kanban missing board filter: %s", b)
	}
	if !strings.Contains(b, `next <a href="/scope/wc/wc-ab2c">wc-ab2c</a>`) {
		t.Fatalf("kanban missing next control: %s", b)
	}
	if !strings.Contains(b, "Backlog") || !strings.Contains(b, "Archived") ||
		strings.Count(b, `data-board-switch`) != 2 {
		t.Fatalf("default board missing layer switches: %s", b)
	}
	if strings.Contains(b, "All tickets") || strings.Contains(b, `all=1`) {
		t.Fatalf("old all-tickets control still present: %s", b)
	}
	if !strings.Contains(b, `href="/scope/wc?backlog=1"`) || !strings.Contains(b, `href="/scope/wc?archived=1"`) {
		t.Fatalf("layer switches should add one query each: %s", b)
	}
	if ai, ni := strings.Index(b, `data-board-switch`), strings.Index(b, `class="next"`); ai < 0 || ni < 0 || ai > ni {
		t.Fatalf("layer switches should sit left of next: %s", b)
	}
	if !strings.Contains(b, `<span class="id">ab2c <span class="next-badge">next</span></span>`) {
		t.Fatalf("next badge should sit on the id row: %s", b)
	}
	if !strings.Contains(b, `data-filter="wc-ab2c ab2c Network redesign frontend"`) {
		t.Fatalf("card filter text: %s", b)
	}
	if strings.Contains(b, "Old work") {
		t.Fatalf("default board showed archived done: %s", b)
	}

	archived := do(s, "/scope/wc?archived=1")
	ab := archived.Body.String()
	if !strings.Contains(ab, "Old work") {
		t.Fatalf("archived switch missing done: %s", ab)
	}
	if !strings.Contains(ab, "<h2>done ") || strings.Contains(ab, "<h2>backlog ") {
		t.Fatalf("archived-only should add done, not backlog: %s", ab)
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

func TestKanbanColumns(t *testing.T) {
	custom := map[string]status.Category{
		"triaged": status.CategoryActive,
		"icebox":  status.CategoryBacklog,
		"shipped": status.CategoryDone,
	}
	got := kanbanColumns(custom, false, false, []string{"weird"})
	want := []string{"blocked", "draft", "todo", "in-progress", "review", "triaged"}
	if !slices.Equal(got, want) {
		t.Fatalf("default = %v want %v", got, want)
	}
	got = kanbanColumns(custom, true, false, nil)
	want = []string{"backlog", "icebox", "blocked", "draft", "todo", "in-progress", "review", "triaged"}
	if !slices.Equal(got, want) {
		t.Fatalf("backlog = %v want %v", got, want)
	}
	got = kanbanColumns(custom, false, true, nil)
	want = []string{"blocked", "draft", "todo", "in-progress", "review", "triaged", "done", "cancelled", "shipped"}
	if !slices.Equal(got, want) {
		t.Fatalf("archived = %v want %v", got, want)
	}
	got = kanbanColumns(custom, true, true, []string{"weird", "todo"})
	want = []string{"backlog", "icebox", "blocked", "draft", "todo", "in-progress", "review", "triaged", "done", "cancelled", "shipped", "weird"}
	if !slices.Equal(got, want) {
		t.Fatalf("both = %v want %v", got, want)
	}
}

func TestKanbanLayerSwitches(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "")
	addTicket(t, dir, "wc-de34", "later", "backlog", "a1", "# Later\n", false, "")
	addTicket(t, dir, "wc-gh56", "old", "done", "a2", "# Old\n", true, "")
	addTicket(t, dir, "wc-jk78", "nope", "cancelled", "a3", "# Nope\n", true, "")
	s := mustServer(t, app)

	home := do(s, "/scope/wc").Body.String()
	if strings.Contains(home, "Later") || strings.Contains(home, "Old") || strings.Contains(home, "Nope") {
		t.Fatalf("default showed hidden layers: %s", home)
	}

	backlog := do(s, "/scope/wc?backlog=1").Body.String()
	if !strings.Contains(backlog, "Later") || strings.Contains(backlog, "Old") {
		t.Fatalf("backlog layer = %s", backlog)
	}
	if strings.Index(backlog, "<h2>backlog ") > strings.Index(backlog, "<h2>blocked ") {
		t.Fatalf("backlog column should lead: %s", backlog)
	}
	if !strings.Contains(backlog, `aria-checked="true"`) || !strings.Contains(backlog, `href="/scope/wc"`) {
		t.Fatalf("backlog switch should be on and link off: %s", backlog)
	}
	if !strings.Contains(backlog, `href="/scope/wc?archived=1&amp;backlog=1"`) {
		t.Fatalf("backlog page should offer archived+backlog: %s", backlog)
	}

	archived := do(s, "/scope/wc?archived=1").Body.String()
	if !strings.Contains(archived, "Old") || !strings.Contains(archived, "Nope") || strings.Contains(archived, "Later") {
		t.Fatalf("archived layer = %s", archived)
	}
	doneAt := strings.Index(archived, "<h2>done ")
	cancelAt := strings.Index(archived, "<h2>cancelled ")
	reviewAt := strings.Index(archived, "<h2>review ")
	if doneAt < 0 || cancelAt < 0 || reviewAt < 0 || !(reviewAt < doneAt && doneAt < cancelAt) {
		t.Fatalf("archived columns after review: %s", archived)
	}
	if !strings.Contains(archived, `href="/scope/wc?archived=1&amp;backlog=1"`) {
		t.Fatalf("archived page should offer both layers: %s", archived)
	}

	both := do(s, "/scope/wc?archived=1&backlog=1").Body.String()
	if !strings.Contains(both, "Later") || !strings.Contains(both, "Old") || !strings.Contains(both, "Nope") {
		t.Fatalf("both layers = %s", both)
	}
	if strings.Contains(both, "All tickets") {
		t.Fatalf("both still has all-tickets copy: %s", both)
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
	if strings.Contains(hb, ">Search</a>") {
		t.Fatalf("search results should not have Search in primary nav: %s", hb)
	}
	if !strings.Contains(hb, `name="q" placeholder="search" value="network"`) {
		t.Fatalf("chrome search box should keep the query: %s", hb)
	}

	bad := do(s, `/search?q=foo"`)
	if bad.Code != 400 {
		t.Fatalf("malformed FTS = %d %s", bad.Code, bad.Body.String())
	}
}

func TestSearchDefaultsToAllScopes(t *testing.T) {
	app := newTestApp(t)
	wc := initScope(t, app, "wc")
	fm := initScope(t, app, "fm")
	addTicket(t, wc, "wc-ab2c", "network", "todo", "a0", "# Network redesign\n\nmux\n", false, "summary: net\n")
	addTicket(t, fm, "fm-de34", "router", "todo", "a0", "# Router mux\n\nmux\n", false, "")
	s := mustServer(t, app)

	kanban := do(s, "/scope/fm").Body.String()
	if strings.Contains(kanban, `name="scope"`) {
		t.Fatalf("chrome search box must not bind the selected scope: %s", kanban)
	}

	all := do(s, "/search?q=mux")
	if all.Code != 200 {
		t.Fatalf("all-scopes search = %d %s", all.Code, all.Body.String())
	}
	ab := all.Body.String()
	if !strings.Contains(ab, "wc-ab2c") || !strings.Contains(ab, "fm-de34") {
		t.Fatalf("unbounded search should hit both scopes: %s", ab)
	}
	if !strings.Contains(ab, `class="tag on" href="/search?q=mux">all</a>`) {
		t.Fatalf("all chip should be on: %s", ab)
	}
	if !strings.Contains(ab, `href="/search?q=mux&amp;scope=fm"`) || !strings.Contains(ab, `href="/search?q=mux&amp;scope=wc"`) {
		t.Fatalf("scope chips should keep q: %s", ab)
	}
	if !strings.Contains(ab, `href="/search?q=mux"`) {
		t.Fatalf("all chip should drop scope: %s", ab)
	}

	bound := do(s, "/search?q=mux&scope=fm")
	if bound.Code != 200 {
		t.Fatalf("bound search = %d %s", bound.Code, bound.Body.String())
	}
	bb := bound.Body.String()
	if !strings.Contains(bb, "fm-de34") || strings.Contains(bb, "wc-ab2c") {
		t.Fatalf("scope=fm should hide the other scope: %s", bb)
	}
	if !strings.Contains(bb, `class="tag on" href="/search?q=mux&amp;scope=fm">fm</a>`) {
		t.Fatalf("fm chip should be on: %s", bb)
	}
	if !strings.Contains(bb, `name="scope" value="fm"`) {
		t.Fatalf("results form should keep the chip bound: %s", bb)
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
	css := w.Body.String()
	if !strings.Contains(css, ".card[hidden]") || !strings.Contains(css, ".col[hidden]") {
		t.Fatalf("hidden cards/columns must override display:block: %s", css)
	}
	if !strings.Contains(css, "--ticket: #fff;") ||
		!strings.Contains(css, ".card {\n  display: block;") ||
		!strings.Contains(css, "background: var(--ticket);") ||
		!strings.Contains(css, ".body { background: var(--ticket);") {
		t.Fatalf("inspect body and board cards must share --ticket white: %s", css)
	}
	js := do(s, "/static/board.js")
	if js.Code != 200 {
		t.Fatalf("board.js = %d", js.Code)
	}
	jsBody := js.Body.String()
	if !strings.Contains(jsBody, "data-board-filter") || !strings.Contains(jsBody, "data-board-switch") {
		t.Fatalf("board.js = %s", jsBody)
	}

	logo := do(s, "/static/tk-logo.svg")
	if logo.Code != 200 {
		t.Fatalf("logo = %d", logo.Code)
	}
	ct = logo.Header().Get("Content-Type")
	if !strings.Contains(ct, "image/svg+xml") && !strings.Contains(ct, "text/xml") {
		t.Fatalf("logo content-type = %q", ct)
	}
	if !strings.Contains(logo.Body.String(), `viewBox="0 0 512 512"`) {
		t.Fatalf("logo body = %s", logo.Body.String())
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
		{"/", "Board"},
		{"/scope/wc", "Board"},
		{"/search", ""},
		{"/graphs", "Graphs"},
		{"/maintenance", "Maintenance"},
	}
	for _, c := range cases {
		w := do(s, c.path)
		if w.Code != 200 {
			t.Fatalf("%s = %d %s", c.path, w.Code, w.Body.String())
		}
		body := w.Body.String()
		if strings.Contains(body, ">Overview</a>") || strings.Contains(body, ">Search</a>") {
			t.Errorf("%s still has Overview or Search in primary nav", c.path)
		}
		for _, label := range []string{"Board", "Graphs", "Maintenance"} {
			if !strings.Contains(body, ">"+label+"</a>") {
				t.Errorf("%s missing primary nav %s", c.path, label)
			}
		}
		if c.current == "" {
			if strings.Contains(body, `class="current"`) {
				t.Errorf("%s should not mark a primary nav item current: %s", c.path, body)
			}
		} else if !strings.Contains(body, `class="current">`+c.current+"</a>") {
			t.Errorf("%s current want %s", c.path, c.current)
		}
		if !strings.Contains(body, `href="/scope/wc"`) {
			t.Errorf("%s missing scope switcher", c.path)
		}
		if !strings.Contains(body, `aria-label="sections"`) {
			t.Errorf("%s missing sections nav", c.path)
		}
	}

	home := do(s, "/").Body.String()
	if !strings.Contains(home, `rel="icon" href="/static/tk-logo.svg"`) {
		t.Errorf("missing favicon: %s", home)
	}
	if !strings.Contains(home, `class="brand" href="/" aria-label="tkv home"`) {
		t.Errorf("brand should be the logo link, not text: %s", home)
	}
	if !strings.Contains(home, `src="/static/tk-logo.svg"`) {
		t.Errorf("brand missing logo img: %s", home)
	}
	if strings.Contains(home, `class="brand" href="/">tkv</a>`) {
		t.Errorf("brand still uses tkv text: %s", home)
	}
	if !strings.Contains(home, `href="/" class="current">Board</a>`) {
		t.Errorf("board on summary should stay on /: %s", home)
	}
	kanban := do(s, "/scope/wc").Body.String()
	if !strings.Contains(kanban, `href="/" class="current">Board</a>`) {
		t.Errorf("board on kanban should navigate back to the scope summary: %s", kanban)
	}
	if strings.Contains(kanban, `name="scope"`) {
		t.Errorf("kanban chrome search must not send scope")
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
	if !strings.Contains(kb, `href="/">Board</a>`) {
		t.Errorf("graphs with scope: board should still go to the scope summary")
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
