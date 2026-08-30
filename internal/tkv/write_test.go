package tkv

import (
	"context"
	"errors"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/p3bot/tk/internal/git"
	"github.com/p3bot/tk/internal/order"
	"github.com/p3bot/tk/internal/registry"
	"github.com/p3bot/tk/internal/scopeconfig"
	"github.com/p3bot/tk/internal/status"
	"github.com/p3bot/tk/internal/testgit"
	"github.com/p3bot/tk/internal/token"
	"github.com/p3bot/tk/internal/writeengine"
)

func doPost(s *Server, path string, form url.Values) *httptest.ResponseRecorder {
	return doPostHeader(s, path, form, nil)
}

func doPostHeader(s *Server, path string, form url.Values, hdr http.Header) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, vs := range hdr {
		if http.CanonicalHeaderKey(k) == "Host" {
			req.Host = vs[len(vs)-1]
			continue
		}
		for _, v := range vs {
			req.Header.Set(k, v)
		}
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	return w
}

func mustFollow(t *testing.T, s *Server, w *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	t.Helper()
	if w.Code != http.StatusSeeOther {
		t.Fatalf("want 303, got %d %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if loc == "" {
		t.Fatal("missing Location")
	}
	got := do(s, loc)
	if got.Code != http.StatusOK {
		t.Fatalf("GET %s = %d %s", loc, got.Code, got.Body.String())
	}
	return got
}

func ticketBody(t *testing.T, dir, id string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, id+"-*.md"))
	if err != nil {
		t.Fatal(err)
	}
	arch, err := filepath.Glob(filepath.Join(dir, "archive", id+"-*.md"))
	if err != nil {
		t.Fatal(err)
	}
	matches = append(matches, arch...)
	if len(matches) != 1 {
		t.Fatalf("ticket %s files = %v", id, matches)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func colSection(body, status string) string {
	start := strings.Index(body, "<h2>"+status+" ")
	if start < 0 {
		return ""
	}
	rest := body[start:]
	next := strings.Index(rest[4:], "<h2>")
	if next < 0 {
		return rest
	}
	return rest[:4+next]
}

func setLens(t *testing.T, app *App, scope string, tags []string) {
	t.Helper()
	store := registry.NewStore(app.Ctx, app.ConfigDir)
	if err := store.SetLens(scope, tags); err != nil {
		t.Fatal(err)
	}
}

func lensFile(t *testing.T, app *App) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(app.ConfigDir, "lens.cue"))
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatal(err)
	}
	return string(data)
}

func loadLens(t *testing.T, app *App) map[string][]string {
	t.Helper()
	store := registry.NewStore(app.Ctx, app.ConfigDir)
	reg, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	return reg.Lens
}

func initDrivenScope(t *testing.T, app *App, name string) (dir, repo string) {
	t.Helper()
	if !git.Available() {
		t.Skip("git not on PATH")
	}
	testgit.Hermetic(t)
	repo = t.TempDir()
	testgit.Run(t, repo, "init", "-b", "main")
	testgit.Run(t, repo, "config", "user.email", "a@b.c")
	testgit.Run(t, repo, "config", "user.name", "tk-test")
	testgit.Run(t, repo, "config", "commit.gpgsign", "false")
	dir = filepath.Join(repo, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tk.cue"), []byte("name: \""+name+"\"\nautoCommit: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	registerScope(t, app, name, dir, repo)
	return dir, repo
}

func pushOrigin(t *testing.T, repo string) string {
	t.Helper()
	bare := filepath.Join(t.TempDir(), "bare.git")
	testgit.Run(t, filepath.Dir(bare), "init", "--bare", "-b", "main", filepath.Base(bare))
	testgit.Run(t, repo, "add", "-A")
	testgit.Run(t, repo, "commit", "-m", "seed")
	testgit.Run(t, repo, "remote", "add", "origin", bare)
	testgit.Run(t, repo, "push", "-u", "origin", "HEAD:main")
	return bare
}

func TestNoCLIImport(t *testing.T) {
	matches, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range matches {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		needle := "github.com/p3bot/tk/internal/" + "cli"
		if strings.Contains(string(data), needle) {
			t.Errorf("%s imports %s", f, needle)
		}
	}
}

func TestGETDoesNotWrite(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "")
	s := mustServer(t, app)

	before := ticketBody(t, dir, "wc-ab2c")
	setLens(t, app, "wc", []string{"frontend"})
	beforeLens := lensFile(t, app)
	for _, path := range []string{"/scope/wc", "/scope/wc/ab2c", "/scope/wc/mark", "/scope/wc/claim", "/scope/wc/create", "/scope/wc/lens", "/scope/wc/lens/clear"} {
		w := do(s, path)
		if w.Code == http.StatusSeeOther {
			t.Fatalf("GET %s redirected as a write: %s", path, w.Header().Get("Location"))
		}
	}
	// GET /scope/{name}/mark, /claim, and /create collide with inspect {id}; they must 404, not hit POST.
	if code := do(s, "/scope/wc/mark").Code; code != http.StatusNotFound {
		t.Fatalf("GET /scope/wc/mark = %d, want inspect 404", code)
	}
	if code := do(s, "/scope/wc/claim").Code; code != http.StatusNotFound {
		t.Fatalf("GET /scope/wc/claim = %d, want inspect 404", code)
	}
	if code := do(s, "/scope/wc/create").Code; code != http.StatusNotFound {
		t.Fatalf("GET /scope/wc/create = %d, want inspect 404", code)
	}
	head := httptest.NewRequest(http.MethodHead, "/scope/wc/ab2c", nil)
	hw := httptest.NewRecorder()
	s.Handler().ServeHTTP(hw, head)
	if hw.Code == http.StatusSeeOther {
		t.Fatalf("HEAD inspect wrote: %s", hw.Header().Get("Location"))
	}
	after := ticketBody(t, dir, "wc-ab2c")
	if after != before {
		t.Fatalf("GET/HEAD mutated ticket:\n%s\n---\n%s", before, after)
	}
	if lensFile(t, app) != beforeLens {
		t.Fatal("GET/HEAD mutated lens.cue")
	}
}

func TestPOSTMarkDoneArchivesAndRedirects(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "")
	s := mustServer(t, app)

	w := doPost(s, "/scope/wc/mark", url.Values{
		"id":     {"wc-ab2c"},
		"status": {status.Done},
		"return": {"board"},
	})
	page := mustFollow(t, s, w)
	if strings.Contains(page.Body.String(), `class="title">Work</span>`) {
		t.Fatalf("default board still shows done card: %s", page.Body.String())
	}
	arch := filepath.Join(dir, "archive", "wc-ab2c-work.md")
	if _, err := os.Stat(arch); err != nil {
		t.Fatalf("archive file: %v", err)
	}
	if !strings.Contains(ticketBody(t, dir, "wc-ab2c"), "status: done") {
		t.Fatalf("file status: %s", ticketBody(t, dir, "wc-ab2c"))
	}

	archived := do(s, "/scope/wc?archived=1")
	if !strings.Contains(archived.Body.String(), "Work") {
		t.Fatalf("archived board missing card: %s", archived.Body.String())
	}
}

func TestPOSTClaimNextMovesTodo(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "first", "todo", "a0", "# First\n", false, "")
	addTicket(t, dir, "wc-de34", "second", "todo", "a1", "# Second\n", false, "")
	s := mustServer(t, app)

	w := doPost(s, "/scope/wc/claim", url.Values{"return": {"board"}})
	page := mustFollow(t, s, w)
	body := page.Body.String()
	if !strings.Contains(ticketBody(t, dir, "wc-ab2c"), "status: in-progress") {
		t.Fatalf("next ticket not claimed: %s", ticketBody(t, dir, "wc-ab2c"))
	}
	if !strings.Contains(ticketBody(t, dir, "wc-de34"), "status: todo") {
		t.Fatalf("other todo must stay: %s", ticketBody(t, dir, "wc-de34"))
	}
	if !strings.Contains(colSection(body, status.InProgress), "First") {
		t.Fatalf("claimed card not in in-progress column: %s", body)
	}
	if strings.Contains(colSection(body, status.Todo), "First") {
		t.Fatalf("claimed card still in todo: %s", body)
	}
}

func TestPOSTClaimNextHonoursLens(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "earlier", "todo", "a0", "# Earlier\n", false, "tags: [backend]\n")
	addTicket(t, dir, "wc-de34", "tagged", "todo", "a1", "# Tagged\n", false, "tags: [frontend]\n")
	setLens(t, app, "wc", []string{"frontend"})
	s := mustServer(t, app)

	w := doPost(s, "/scope/wc/claim", url.Values{"return": {"board"}})
	page := mustFollow(t, s, w)
	if !strings.Contains(ticketBody(t, dir, "wc-de34"), "status: in-progress") {
		t.Fatalf("lens next not claimed: %s", ticketBody(t, dir, "wc-de34"))
	}
	if !strings.Contains(ticketBody(t, dir, "wc-ab2c"), "status: todo") {
		t.Fatalf("outside-lens todo was claimed: %s", ticketBody(t, dir, "wc-ab2c"))
	}
	body := page.Body.String()
	if !strings.Contains(colSection(body, status.InProgress), "Tagged") {
		t.Fatalf("lens claim not in in-progress: %s", body)
	}
}

func TestPOSTMarkInProgressClaims(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "")
	s := mustServer(t, app)

	w := doPost(s, "/scope/wc/mark", url.Values{
		"id":     {"wc-ab2c"},
		"status": {status.InProgress},
		"return": {"inspect"},
	})
	page := mustFollow(t, s, w)
	if !strings.Contains(ticketBody(t, dir, "wc-ab2c"), "status: in-progress") {
		t.Fatalf("file: %s", ticketBody(t, dir, "wc-ab2c"))
	}
	if !strings.Contains(page.Body.String(), "<dd>in-progress</dd>") {
		t.Fatalf("inspect status: %s", page.Body.String())
	}
	if strings.Contains(page.Body.String(), ">Claim</button>") {
		t.Fatalf("claimed ticket still offers claim: %s", page.Body.String())
	}
}

func locationTicketID(t *testing.T, loc string) string {
	t.Helper()
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 3 || parts[0] != "scope" || parts[2] == "" {
		t.Fatalf("location path = %s", u.Path)
	}
	return parts[2]
}

func mdTicketCount(t *testing.T, dir string) int {
	t.Helper()
	root, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	arch, err := filepath.Glob(filepath.Join(dir, "archive", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	return len(root) + len(arch)
}

func TestPOSTCreateDraftInspectsAndIndexes(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	s := mustServer(t, app)

	w := doPost(s, "/scope/wc/create", url.Values{"title": {"Network redesign"}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("want 303, got %d %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	id := locationTicketID(t, loc)
	if !strings.HasPrefix(loc, inspectHref(id)) {
		t.Fatalf("location = %s, want inspect of %s", loc, id)
	}
	page := mustFollow(t, s, w)
	body := page.Body.String()
	if !strings.Contains(body, "<h1>Network redesign</h1>") {
		t.Fatalf("inspect missing H1: %s", body)
	}
	if !strings.Contains(body, "<dd>draft</dd>") {
		t.Fatalf("inspect status: %s", body)
	}
	raw := ticketBody(t, dir, id)
	if !strings.Contains(raw, "status: draft") {
		t.Fatalf("file status: %s", raw)
	}
	if !strings.HasSuffix(raw, "# Network redesign\n") {
		t.Fatalf("scaffold body: %q", raw)
	}
	if strings.Contains(raw, "tags:") {
		t.Fatalf("no-tag create must omit tags: %s", raw)
	}
	matches, err := filepath.Glob(filepath.Join(dir, id+"-network-redesign.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("frozen slug path = %v", matches)
	}

	board := do(s, "/scope/wc").Body.String()
	if !strings.Contains(colSection(board, status.Draft), "Network redesign") {
		t.Fatalf("draft not on board: %s", board)
	}
}

func TestPOSTCreateDoneArchivesAndInspects(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	s := mustServer(t, app)

	w := doPost(s, "/scope/wc/create", url.Values{
		"title":  {"Already done"},
		"status": {status.Done},
	})
	page := mustFollow(t, s, w)
	id := locationTicketID(t, w.Header().Get("Location"))
	arch := filepath.Join(dir, "archive", id+"-already-done.md")
	if _, err := os.Stat(arch); err != nil {
		t.Fatalf("archive file: %v", err)
	}
	if !strings.Contains(ticketBody(t, dir, id), "status: done") {
		t.Fatalf("file: %s", ticketBody(t, dir, id))
	}
	if !strings.Contains(page.Body.String(), "<h1>Already done</h1>") {
		t.Fatalf("inspect: %s", page.Body.String())
	}
	if !strings.Contains(page.Body.String(), "<dd>yes</dd>") {
		t.Fatalf("inspect archived: %s", page.Body.String())
	}

	board := do(s, "/scope/wc").Body.String()
	if strings.Contains(board, "Already done") {
		t.Fatalf("default board shows terminal create: %s", board)
	}
	archived := do(s, "/scope/wc?archived=1").Body.String()
	if !strings.Contains(colSection(archived, status.Done), "Already done") {
		t.Fatalf("archived board missing card: %s", archived)
	}
}

func TestPOSTCreateEmptyTitleIs400(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	s := mustServer(t, app)
	before := mdTicketCount(t, dir)

	for _, title := range []string{"", "   "} {
		w := doPost(s, "/scope/wc/create", url.Values{"title": {title}})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("title %q: want 400, got %d %s", title, w.Code, w.Body.String())
		}
	}
	w := doPost(s, "/scope/wc/create", url.Values{})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing title: want 400, got %d %s", w.Code, w.Body.String())
	}
	if mdTicketCount(t, dir) != before {
		t.Fatal("empty title minted a file")
	}
}

func TestPOSTCreateToolbarFormEmptyTagSucceeds(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	s := mustServer(t, app)

	w := doPost(s, "/scope/wc/create", url.Values{
		"title":  {"From toolbar"},
		"status": {status.Draft},
		"tag":    {""},
	})
	page := mustFollow(t, s, w)
	id := locationTicketID(t, w.Header().Get("Location"))
	raw := ticketBody(t, dir, id)
	if !strings.Contains(raw, "status: draft") {
		t.Fatalf("file status: %s", raw)
	}
	if strings.Contains(raw, "tags:") {
		t.Fatalf("empty tag field must omit tags: %s", raw)
	}
	if !strings.Contains(page.Body.String(), "<h1>From toolbar</h1>") {
		t.Fatalf("inspect: %s", page.Body.String())
	}

	w = doPost(s, "/scope/wc/create", url.Values{
		"title":  {"Whitespace tag"},
		"status": {status.Draft},
		"tag":    {"   "},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("whitespace tag: want 303, got %d %s", w.Code, w.Body.String())
	}
	id = locationTicketID(t, w.Header().Get("Location"))
	if strings.Contains(ticketBody(t, dir, id), "tags:") {
		t.Fatalf("whitespace tag field must omit tags: %s", ticketBody(t, dir, id))
	}
}

func TestPOSTCreateTagsAndTagNew(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "old", "todo", "a0", "# Old\n", false, "tags: [legacy]\n")
	s := mustServer(t, app)

	w := doPost(s, "/scope/wc/create", url.Values{
		"title": {"Tagged"},
		"tag":   {"alpha, legacy", "beta", "alpha"},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("want 303, got %d %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "tag_new=") {
		t.Fatalf("location missing tag_new: %s", loc)
	}
	id := locationTicketID(t, loc)
	raw := ticketBody(t, dir, id)
	if !strings.Contains(raw, "tags: [alpha, legacy, beta]") && !strings.Contains(raw, "tags: [alpha,legacy,beta]") {
		t.Fatalf("tags: %s", raw)
	}
	page := do(s, loc)
	if page.Code != 200 {
		t.Fatalf("GET loc = %d", page.Code)
	}
	banners := page.Body.String()
	if !strings.Contains(banners, html.EscapeString(token.FormatTagNew("alpha"))) {
		t.Fatalf("missing alpha tag_new: %s", banners)
	}
	if !strings.Contains(banners, html.EscapeString(token.FormatTagNew("beta"))) {
		t.Fatalf("missing beta tag_new: %s", banners)
	}
	if strings.Contains(banners, html.EscapeString(token.FormatTagNew("legacy"))) {
		t.Fatalf("board-existing tag must not be tag_new: %s", banners)
	}
}

func TestPOSTCreateNeverSelfCommits(t *testing.T) {
	app := newTestApp(t)
	dir, repo := initDrivenScope(t, app, "wc")
	pushOrigin(t, repo)
	before := testgit.Combined(t, repo, "rev-parse", "HEAD")
	s := mustServer(t, app)

	w := doPost(s, "/scope/wc/create", url.Values{"title": {"Work"}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("want 303, got %d %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "sync_needed=") {
		t.Fatalf("location missing sync_needed: %s", loc)
	}
	after := testgit.Combined(t, repo, "rev-parse", "HEAD")
	if after != before {
		t.Fatalf("create self-committed: %s -> %s", before, after)
	}
	page := do(s, loc)
	if page.Code != 200 {
		t.Fatalf("GET loc = %d", page.Code)
	}
	if !strings.Contains(page.Body.String(), "sync_needed:") {
		t.Fatalf("banner: %s", page.Body.String())
	}
	id := locationTicketID(t, loc)
	if _, err := os.Stat(filepath.Join(dir, id+"-work.md")); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
}

func TestPOSTCreateEngineRefuses(t *testing.T) {
	t.Run("unknown status", func(t *testing.T) {
		app := newTestApp(t)
		dir := initScope(t, app, "wc")
		s := mustServer(t, app)
		w := doPost(s, "/scope/wc/create", url.Values{"title": {"X"}, "status": {"nope"}})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("want 400, got %d %s", w.Code, w.Body.String())
		}
		if mdTicketCount(t, dir) != 0 {
			t.Fatal("unknown status minted a file")
		}
	})
	t.Run("unusable", func(t *testing.T) {
		app := newTestApp(t)
		dir := initScope(t, app, "wc")
		if err := os.WriteFile(filepath.Join(dir, "tk.cue"), []byte("name: \"wc\"\nthis is not cue {\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		s := mustServer(t, app)
		w := doPost(s, "/scope/wc/create", url.Values{"title": {"X"}})
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("want 503, got %d %s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), token.ConfigUnparseable) {
			t.Fatalf("message: %s", w.Body.String())
		}
		if mdTicketCount(t, dir) != 0 {
			t.Fatal("unusable minted a file")
		}
	})
	t.Run("mid-rebase", func(t *testing.T) {
		app := newTestApp(t)
		dir, repo := initDrivenScope(t, app, "wc")
		if err := os.MkdirAll(filepath.Join(repo, ".git", "rebase-merge"), 0o755); err != nil {
			t.Fatal(err)
		}
		s := mustServer(t, app)
		w := doPost(s, "/scope/wc/create", url.Values{"title": {"X"}})
		if w.Code != http.StatusConflict {
			t.Fatalf("want 409, got %d %s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "mid-sync-conflict") {
			t.Fatalf("message: %s", w.Body.String())
		}
		if mdTicketCount(t, dir) != 0 {
			t.Fatal("mid-rebase minted a file")
		}
	})
}

func TestPOSTCreateFormListsKnownStatuses(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	if err := os.WriteFile(filepath.Join(dir, "tk.cue"), []byte(
		"name: \"wc\"\nautoCommit: false\nstatuses: { parked: { category: \"backlog\" } }\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	s := mustServer(t, app)
	board := do(s, "/scope/wc").Body.String()
	if !strings.Contains(board, `<option value="draft" selected>draft</option>`) {
		t.Fatalf("draft not default: %s", board)
	}
	if !strings.Contains(board, `<option value="parked">parked</option>`) {
		t.Fatalf("custom status missing: %s", board)
	}
}

func TestPOSTClaimNoLongerTodo(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "work", "review", "a0", "# Work\n", false, "")
	s := mustServer(t, app)

	w := doPost(s, "/scope/wc/claim", url.Values{
		"id":     {"wc-ab2c"},
		"return": {"inspect"},
	})
	if w.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "no longer todo") {
		t.Fatalf("message: %s", w.Body.String())
	}
	if !strings.Contains(ticketBody(t, dir, "wc-ab2c"), "status: review") {
		t.Fatalf("must not write: %s", ticketBody(t, dir, "wc-ab2c"))
	}
}

func TestMapWriteErrorCreateMetaOrderClasses(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"usage", &writeengine.UsageError{Msg: "create tag must be non-empty"}, http.StatusBadRequest},
		{"depends self", &writeengine.DependsSelfError{ID: "wc-ab2c"}, http.StatusConflict},
		{"depends dangling", &writeengine.DependsDanglingError{ID: "wc-ab2c", Target: "wc-zz99"}, http.StatusConflict},
		{"depends unresolvable", &writeengine.DependsUnresolvableError{ID: "wc-ab2c", Target: "zz-aa22"}, http.StatusConflict},
		{"neighbour order", &writeengine.NeighbourOrderError{Arg: "wc-de34"}, http.StatusConflict},
		{"no legal order", &writeengine.NoLegalOrderError{ID: "wc-ab2c", Err: order.ErrEqualKeys}, http.StatusConflict},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mapWriteError(writeengine.Result{}, c.err)
			var he *httpError
			if !errors.As(got, &he) {
				t.Fatalf("want httpError, got %T %v", got, got)
			}
			if he.status != c.want {
				t.Errorf("status = %d, want %d", he.status, c.want)
			}
			if he.message == "" {
				t.Error("empty message")
			}
		})
	}
}

func TestPOSTMarkUnknownStatus(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "")
	s := mustServer(t, app)

	w := doPost(s, "/scope/wc/mark", url.Values{
		"id":     {"wc-ab2c"},
		"status": {"nope"},
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(ticketBody(t, dir, "wc-ab2c"), "status: todo") {
		t.Fatalf("must not write: %s", ticketBody(t, dir, "wc-ab2c"))
	}
}

func TestPOSTMarkSoftDependsOpen(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "base", "todo", "a0", "# Base\n", false, "")
	addTicket(t, dir, "wc-de34", "hang", "todo", "a1", "# Hang\n", false, "depends: [wc-ab2c]\n")
	s := mustServer(t, app)

	w := doPost(s, "/scope/wc/mark", url.Values{
		"id":     {"wc-de34"},
		"status": {status.Review},
		"return": {"board"},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("soft warn must 303, got %d %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "depends_open=") {
		t.Fatalf("location missing depends_open: %s", loc)
	}
	page := do(s, loc)
	if page.Code != 200 {
		t.Fatalf("GET loc = %d", page.Code)
	}
	if !strings.Contains(page.Body.String(), "depends_open:") || !strings.Contains(page.Body.String(), "wc-ab2c") {
		t.Fatalf("banner: %s", page.Body.String())
	}
	if !strings.Contains(ticketBody(t, dir, "wc-de34"), "status: review") {
		t.Fatalf("file: %s", ticketBody(t, dir, "wc-de34"))
	}
	body := page.Body.String()
	if !strings.Contains(colSection(body, status.Review), "Hang") {
		t.Fatalf("card not in review column: %s", body)
	}
	if strings.Contains(colSection(body, status.Todo), "Hang") {
		t.Fatalf("card still in todo: %s", body)
	}
}

func TestPOSTMarkSoftSyncNeeded(t *testing.T) {
	app := newTestApp(t)
	dir, repo := initDrivenScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "")
	pushOrigin(t, repo)
	s := mustServer(t, app)

	w := doPost(s, "/scope/wc/mark", url.Values{
		"id":     {"wc-ab2c"},
		"status": {status.Review},
		"return": {"board"},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("soft warn must 303, got %d %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "sync_needed=") {
		t.Fatalf("location missing sync_needed: %s", loc)
	}
	page := do(s, loc)
	if page.Code != 200 {
		t.Fatalf("GET loc = %d", page.Code)
	}
	if !strings.Contains(page.Body.String(), "sync_needed:") {
		t.Fatalf("banner: %s", page.Body.String())
	}
	if !strings.Contains(ticketBody(t, dir, "wc-ab2c"), "status: review") {
		t.Fatalf("file: %s", ticketBody(t, dir, "wc-ab2c"))
	}
}

func TestPOSTMarkSoftSyncDisabled(t *testing.T) {
	app := newTestApp(t)
	dir := filepath.Join(t.TempDir(), "wc")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := scopeconfig.WriteMinimal(dir, "wc", true); err != nil {
		t.Fatal(err)
	}
	registerScope(t, app, "wc", dir, dir)
	addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "")
	s := mustServer(t, app)

	w := doPost(s, "/scope/wc/mark", url.Values{
		"id":     {"wc-ab2c"},
		"status": {status.Review},
		"return": {"board"},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("soft warn must 303, got %d %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "sync_disabled=") {
		t.Fatalf("location missing sync_disabled: %s", loc)
	}
	page := do(s, loc)
	if page.Code != 200 {
		t.Fatalf("GET loc = %d", page.Code)
	}
	if !strings.Contains(page.Body.String(), "sync_disabled:") {
		t.Fatalf("banner: %s", page.Body.String())
	}
	if !strings.Contains(ticketBody(t, dir, "wc-ab2c"), "status: review") {
		t.Fatalf("file: %s", ticketBody(t, dir, "wc-ab2c"))
	}
}

func TestPOSTMarkRidesWarnings(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "")
	if err := os.WriteFile(filepath.Join(dir, "wc-abcd-x.md"), []byte("---\nid: wc-abcd\nstatus: [unterminated\n---\n# broke\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := mustServer(t, app)

	w := doPost(s, "/scope/wc/mark", url.Values{
		"id":     {"wc-ab2c"},
		"status": {status.Review},
		"return": {"board"},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("soft warn must 303, got %d %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "warning=") {
		t.Fatalf("location missing warning: %s", loc)
	}
	page := do(s, loc)
	if !strings.Contains(page.Body.String(), token.ParseError) {
		t.Fatalf("banner: %s", page.Body.String())
	}
}

func TestPOSTMarkSoftRequiredMissing(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	if err := os.WriteFile(filepath.Join(dir, "tk.cue"), []byte(
		"name: \"wc\"\nautoCommit: false\nfields: { jira: { type: \"string\", required: true } }\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "")
	s := mustServer(t, app)

	w := doPost(s, "/scope/wc/mark", url.Values{
		"id":     {"wc-ab2c"},
		"status": {status.Done},
		"return": {"inspect"},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("soft warn must 303, got %d %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "required_missing=") {
		t.Fatalf("location: %s", loc)
	}
	page := do(s, loc)
	if !strings.Contains(page.Body.String(), "required_missing:") || !strings.Contains(page.Body.String(), "jira") {
		t.Fatalf("banner: %s", page.Body.String())
	}
}

func TestFormsWorkWithoutBoardJS(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "")
	s := mustServer(t, app)

	board := do(s, "/scope/wc").Body.String()
	if !strings.Contains(board, `<div class="board-controls">`) {
		t.Fatalf("kanban toolbar must be a div so Claim next can sit in it: %s", board)
	}
	if strings.Contains(board, `<p class="board-controls">`) {
		t.Fatalf("kanban toolbar still a p (form would split it): %s", board)
	}
	if !strings.Contains(board, `method="post" action="/scope/wc/claim"`) {
		t.Fatalf("kanban missing claim form: %s", board)
	}
	if !strings.Contains(board, `method="post" action="/scope/wc/mark"`) {
		t.Fatalf("kanban missing mark form: %s", board)
	}
	if !strings.Contains(board, `method="post" action="/scope/wc/create"`) {
		t.Fatalf("kanban missing create form: %s", board)
	}
	if !strings.Contains(board, `name="title" required`) {
		t.Fatalf("create form missing required title: %s", board)
	}
	if !strings.Contains(board, `placeholder="tags, comma-separated"`) {
		t.Fatalf("create tags field must disclose comma-splitting: %s", board)
	}
	if strings.Contains(board, `action="/scope/wc/create"`) && strings.Contains(board, "<textarea") {
		t.Fatalf("create form must not have a body textarea: %s", board)
	}
	if !strings.Contains(board, "Claim next") {
		t.Fatalf("kanban missing claim next: %s", board)
	}
	if !strings.Contains(board, `onsubmit="if(this.dataset.submitted)return false;`) {
		t.Fatalf("claim next form missing double-submit guard: %s", board)
	}
	if strings.Contains(colSection(board, status.Todo), `option value="in-progress"`) {
		t.Fatalf("todo card must not mark in-progress: %s", colSection(board, status.Todo))
	}
	if !strings.Contains(board, `aria-label="Claim wc-ab2c"`) {
		t.Fatalf("kanban claim missing named control: %s", board)
	}
	if !strings.Contains(board, `aria-label="Mark wc-ab2c"`) {
		t.Fatalf("kanban mark missing named control: %s", board)
	}

	ins := do(s, "/scope/wc/ab2c").Body.String()
	if !strings.Contains(ins, `method="post" action="/scope/wc/claim"`) {
		t.Fatalf("inspect missing claim form: %s", ins)
	}
	if !strings.Contains(ins, `method="post" action="/scope/wc/mark"`) {
		t.Fatalf("inspect missing mark form: %s", ins)
	}
	if !strings.Contains(board, `method="post" action="/scope/wc/lens"`) {
		t.Fatalf("kanban missing chrome lens form: %s", board)
	}
	if !strings.Contains(board, `method="post" action="/scope/wc/lens/clear"`) {
		t.Fatalf("kanban missing chrome lens clear: %s", board)
	}
	if !strings.Contains(ins, `method="post" action="/scope/wc/lens"`) {
		t.Fatalf("inspect missing chrome lens form: %s", ins)
	}
}

func TestWriteControlsHiddenWhenEngineWillRefuse(t *testing.T) {
	t.Run("unusable", func(t *testing.T) {
		app := newTestApp(t)
		dir := initScope(t, app, "wc")
		addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "")
		if err := os.WriteFile(filepath.Join(dir, "tk.cue"), []byte("name: \"wc\"\nthis is not cue {\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		s := mustServer(t, app)
		board := do(s, "/scope/wc").Body.String()
		if strings.Contains(board, `action="/scope/wc/claim"`) || strings.Contains(board, `action="/scope/wc/mark"`) || strings.Contains(board, `action="/scope/wc/create"`) {
			t.Fatalf("unusable schema still offers ticket writes: %s", board)
		}
		if strings.Contains(board, "Claim next") {
			t.Fatalf("unusable schema still offers claim next: %s", board)
		}
		if !strings.Contains(board, `action="/scope/wc/lens"`) {
			t.Fatalf("unusable schema must still offer chrome lens: %s", board)
		}
		ins := do(s, "/scope/wc/ab2c").Body.String()
		if strings.Contains(ins, `action="/scope/wc/claim"`) || strings.Contains(ins, `action="/scope/wc/mark"`) {
			t.Fatalf("inspect still offers ticket writes: %s", ins)
		}
		if !strings.Contains(ins, `action="/scope/wc/lens"`) {
			t.Fatalf("inspect must still offer chrome lens: %s", ins)
		}
	})
	t.Run("parse", func(t *testing.T) {
		app := newTestApp(t)
		dir := initScope(t, app, "wc")
		if err := os.WriteFile(filepath.Join(dir, "wc-abcd-x.md"), []byte("---\nid: wc-abcd\nstatus: [unterminated\n---\n# broke\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		s := mustServer(t, app)
		board := do(s, "/scope/wc").Body.String()
		if strings.Contains(board, `action="/scope/wc/claim"`) || strings.Contains(board, `action="/scope/wc/mark"`) {
			t.Fatalf("parse-quarantined card still offers writes: %s", board)
		}
		if !strings.Contains(board, `action="/scope/wc/create"`) {
			t.Fatalf("parse-quarantined board must still offer create: %s", board)
		}
		ins := do(s, "/scope/wc/abcd").Body.String()
		if strings.Contains(ins, `action="/scope/wc/claim"`) || strings.Contains(ins, `action="/scope/wc/mark"`) {
			t.Fatalf("inspect still offers ticket writes: %s", ins)
		}
		if !strings.Contains(ins, `action="/scope/wc/lens"`) {
			t.Fatalf("parse-error inspect must still offer chrome lens: %s", ins)
		}
	})
}

func TestPOSTWriteRefusesForeignOrigin(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "")
	s := mustServer(t, app)
	form := url.Values{"id": {"wc-ab2c"}, "status": {status.Done}}

	w := doPostHeader(s, "/scope/wc/mark", form, http.Header{"Origin": {"https://evil.example"}})
	if w.Code != http.StatusForbidden {
		t.Fatalf("foreign origin: want 403, got %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(ticketBody(t, dir, "wc-ab2c"), "status: todo") {
		t.Fatalf("must not write: %s", ticketBody(t, dir, "wc-ab2c"))
	}

	w = doPostHeader(s, "/scope/wc/mark", form, http.Header{"Sec-Fetch-Site": {"cross-site"}})
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-site: want 403, got %d %s", w.Code, w.Body.String())
	}

	w = doPostHeader(s, "/scope/wc/mark", form, http.Header{
		"Host":   {"127.0.0.1:8736"},
		"Origin": {"http://127.0.0.1:3000"},
	})
	if w.Code != http.StatusForbidden {
		t.Fatalf("other-port origin: want 403, got %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(ticketBody(t, dir, "wc-ab2c"), "status: todo") {
		t.Fatalf("must not write: %s", ticketBody(t, dir, "wc-ab2c"))
	}

	w = doPostHeader(s, "/scope/wc/mark", form, http.Header{
		"Host":   {"127.0.0.1:8736"},
		"Origin": {"http://127.0.0.1:8736"},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("matching origin: want 303, got %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(ticketBody(t, dir, "wc-ab2c"), "status: done") {
		t.Fatalf("matching origin must write: %s", ticketBody(t, dir, "wc-ab2c"))
	}
}

func TestResponsesDenyFraming(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "")
	s := mustServer(t, app)
	page := do(s, "/scope/wc")
	if page.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("GET X-Frame-Options = %q", page.Header().Get("X-Frame-Options"))
	}
	w := doPostHeader(s, "/scope/wc/mark", url.Values{"id": {"wc-ab2c"}, "status": {status.Done}}, http.Header{"Origin": {"https://evil.example"}})
	if w.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("POST X-Frame-Options = %q", w.Header().Get("X-Frame-Options"))
	}
}

func TestPOSTMarkEngineRefuses(t *testing.T) {
	t.Run("duplicate", func(t *testing.T) {
		app := newTestApp(t)
		dir := initScope(t, app, "wc")
		addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "")
		data, err := os.ReadFile(filepath.Join(dir, "wc-ab2c-work.md"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "wc-ab2c-dup.md"), data, 0o644); err != nil {
			t.Fatal(err)
		}
		s := mustServer(t, app)
		w := doPost(s, "/scope/wc/mark", url.Values{"id": {"wc-ab2c"}, "status": {status.Done}})
		if w.Code != http.StatusConflict {
			t.Fatalf("want 409, got %d %s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "claimed by") {
			t.Fatalf("message: %s", w.Body.String())
		}
	})
	t.Run("parse", func(t *testing.T) {
		app := newTestApp(t)
		dir := initScope(t, app, "wc")
		if err := os.WriteFile(filepath.Join(dir, "wc-abcd-x.md"), []byte("---\nid: wc-abcd\nstatus: [unterminated\n---\n# broke\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		s := mustServer(t, app)
		w := doPost(s, "/scope/wc/mark", url.Values{"id": {"wc-abcd"}, "status": {status.Done}})
		if w.Code != http.StatusConflict {
			t.Fatalf("want 409, got %d %s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), token.ParseError) {
			t.Fatalf("message: %s", w.Body.String())
		}
	})
	t.Run("unusable", func(t *testing.T) {
		app := newTestApp(t)
		dir := initScope(t, app, "wc")
		addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "")
		if err := os.WriteFile(filepath.Join(dir, "tk.cue"), []byte("name: \"wc\"\nthis is not cue {\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		s := mustServer(t, app)
		w := doPost(s, "/scope/wc/mark", url.Values{"id": {"wc-ab2c"}, "status": {status.Done}})
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("want 503, got %d %s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), token.ConfigUnparseable) {
			t.Fatalf("message: %s", w.Body.String())
		}
		if !strings.Contains(ticketBody(t, dir, "wc-ab2c"), "status: todo") {
			t.Fatalf("must not write: %s", ticketBody(t, dir, "wc-ab2c"))
		}
	})
	t.Run("mid-rebase", func(t *testing.T) {
		app := newTestApp(t)
		dir, repo := initDrivenScope(t, app, "wc")
		addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "")
		if err := os.MkdirAll(filepath.Join(repo, ".git", "rebase-merge"), 0o755); err != nil {
			t.Fatal(err)
		}
		s := mustServer(t, app)
		w := doPost(s, "/scope/wc/mark", url.Values{"id": {"wc-ab2c"}, "status": {status.Done}})
		if w.Code != http.StatusConflict {
			t.Fatalf("want 409, got %d %s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "mid-sync-conflict") {
			t.Fatalf("message: %s", w.Body.String())
		}
		if !strings.Contains(ticketBody(t, dir, "wc-ab2c"), "status: todo") {
			t.Fatalf("must not write: %s", ticketBody(t, dir, "wc-ab2c"))
		}
	})
}

func TestBeginWriteStripsCancel(t *testing.T) {
	app := newTestApp(t)
	initScope(t, app, "wc")
	s := mustServer(t, app)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sess, release, err := s.beginWrite(ctx, "wc")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := sess.deps.Ctx.Err(); err != nil {
		t.Fatalf("write context must not carry request cancel: %v", err)
	}
	select {
	case <-sess.deps.Ctx.Done():
		t.Fatal("write context already done")
	default:
	}
}

func TestWriteDepsIsolatesCue(t *testing.T) {
	app := newTestApp(t)
	s := mustServer(t, app)
	reg := &registry.Registry{Scopes: map[string]registry.Entry{}}
	deps, err := s.writeDeps(context.Background(), reg, s.cur)
	if err != nil {
		t.Fatal(err)
	}
	if deps.Cue == s.app.cue() {
		t.Fatal("write must not share the process CUE context")
	}
	if deps.Rec == s.rec {
		t.Fatal("write must not share the process reconciler")
	}
	if deps.DB != s.cur.db {
		t.Fatal("write must use the pinned index")
	}
}

func TestClaimUnlocksIndexMutex(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "")
	s := mustServer(t, app)

	started := make(chan struct{})
	unblock := make(chan struct{})
	s.afterIndexUnlock = func() {
		close(started)
		<-unblock
	}

	claimCh := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		claimCh <- doPost(s, "/scope/wc/claim", url.Values{"return": {"board"}})
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("claim did not drop the index mutex")
	}

	homeCh := make(chan *httptest.ResponseRecorder, 1)
	go func() { homeCh <- do(s, "/") }()
	select {
	case home := <-homeCh:
		if home.Code != 200 {
			t.Fatalf("overview during claim = %d", home.Code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("overview blocked; claim still holding Server.mu")
	}
	close(unblock)
	w := <-claimCh
	if w.Code != http.StatusSeeOther {
		t.Fatalf("claim = %d %s", w.Code, w.Body.String())
	}
}

func TestWriteSurvivesReopen(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "")
	s := mustServer(t, app)

	started := make(chan struct{})
	unblock := make(chan struct{})
	s.afterIndexUnlock = func() {
		close(started)
		<-unblock
	}

	claimCh := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		claimCh <- doPost(s, "/scope/wc/claim", url.Values{"return": {"board"}})
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("claim did not drop the index mutex")
	}

	s.mu.Lock()
	if err := s.reopen(); err != nil {
		s.mu.Unlock()
		close(unblock)
		t.Fatalf("reopen: %v", err)
	}
	s.mu.Unlock()
	close(unblock)

	w := <-claimCh
	if w.Code != http.StatusSeeOther {
		t.Fatalf("claim after reopen = %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(ticketBody(t, dir, "wc-ab2c"), "status: in-progress") {
		t.Fatalf("write must stand: %s", ticketBody(t, dir, "wc-ab2c"))
	}
}

func TestClaimRefreshFailedDoesNotWrite(t *testing.T) {
	app := newTestApp(t)
	dir, repo := initDrivenScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "")
	bare := pushOrigin(t, repo)
	if err := os.RemoveAll(bare); err != nil {
		t.Fatal(err)
	}

	s := mustServer(t, app)
	w := doPost(s, "/scope/wc/claim", url.Values{"id": {"wc-ab2c"}})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "refresh did not complete") {
		t.Fatalf("message: %s", w.Body.String())
	}
	if !strings.Contains(ticketBody(t, dir, "wc-ab2c"), "status: todo") {
		t.Fatalf("must stay todo: %s", ticketBody(t, dir, "wc-ab2c"))
	}
}

func TestClaimPushFailedLeavesInProgress(t *testing.T) {
	app := newTestApp(t)
	dir, repo := initDrivenScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "")
	pushOrigin(t, repo)

	hooks := filepath.Join(repo, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	testgit.Run(t, repo, "config", "core.hooksPath", hooks)
	if err := os.WriteFile(filepath.Join(hooks, "pre-push"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	s := mustServer(t, app)
	w := doPost(s, "/scope/wc/claim", url.Values{"id": {"wc-ab2c"}})
	if w.Code == http.StatusSeeOther {
		t.Fatalf("push failure must not look like success: %s", w.Header().Get("Location"))
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "in-progress") {
		t.Fatalf("must say in-progress: %s", w.Body.String())
	}
	if !strings.Contains(ticketBody(t, dir, "wc-ab2c"), "status: in-progress") {
		t.Fatalf("write must stand: %s", ticketBody(t, dir, "wc-ab2c"))
	}
}

func TestStripNoticeQueryMatchesReader(t *testing.T) {
	q := url.Values{}
	q.Set(noticeDependsOpen, "wc-ab2c")
	q.Set(noticeRequiredMissing, "jira")
	q.Set(noticeSyncNeeded, "unpushed")
	q.Set(noticeSyncDisabled, "plain-files")
	q.Add(noticeTagNew, "orphan")
	q.Add(noticeWarning, token.FormatTagUnknown("ghost"))
	q.Set("backlog", "1")
	q.Set("archived", "1")
	q.Add("tag", "frontend")
	dirty := "/scope/wc?" + q.Encode()
	if noticesFromQuery(q) == nil {
		t.Fatal("reader must see notice keys")
	}
	cleaned := stripNoticeQuery(dirty)
	u, err := url.Parse(cleaned)
	if err != nil {
		t.Fatal(err)
	}
	if noticesFromQuery(u.Query()) != nil {
		t.Fatalf("strip left displayable notices: %s", cleaned)
	}
	if u.Path != "/scope/wc" || u.Query().Get("backlog") != "1" || u.Query().Get("archived") != "1" {
		t.Fatalf("board query lost: %s", cleaned)
	}
	if got := u.Query()["tag"]; len(got) != 1 || got[0] != "frontend" {
		t.Fatalf("tag chip lost: %s", cleaned)
	}
}

func TestPOSTLensSetClearHonoursBoardAndOtherScope(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	initScope(t, app, "bb")
	addTicket(t, dir, "wc-ab2c", "earlier", "todo", "a0", "# Earlier\n", false, "tags: [backend]\n")
	addTicket(t, dir, "wc-de34", "tagged", "todo", "a1", "# Tagged\n", false, "tags: [frontend]\n")
	setLens(t, app, "bb", []string{"keep"})
	s := mustServer(t, app)

	home := do(s, "/").Body.String()
	if strings.Contains(home, `/lens"`) {
		t.Fatalf("overview must not offer lens write: %s", home)
	}

	w := doPost(s, "/scope/wc/lens", url.Values{
		"tag":    {"frontend"},
		"return": {"/scope/wc"},
	})
	page := mustFollow(t, s, w)
	body := page.Body.String()
	if !strings.Contains(colSection(body, status.Todo), "Tagged") {
		t.Fatalf("lensed board missing matching card: %s", body)
	}
	if strings.Contains(colSection(body, status.Todo), "Earlier") {
		t.Fatalf("lensed board still shows outside-lens tagged card: %s", body)
	}
	got := loadLens(t, app)
	if len(got["wc"]) != 1 || got["wc"][0] != "frontend" {
		t.Fatalf("wc lens = %v", got["wc"])
	}
	if len(got["bb"]) != 1 || got["bb"][0] != "keep" {
		t.Fatalf("bb lens dropped: %v", got["bb"])
	}

	claim := doPost(s, "/scope/wc/claim", url.Values{"return": {"board"}})
	mustFollow(t, s, claim)
	if !strings.Contains(ticketBody(t, dir, "wc-de34"), "status: in-progress") {
		t.Fatalf("claim-next must honour new lens: %s", ticketBody(t, dir, "wc-de34"))
	}
	if !strings.Contains(ticketBody(t, dir, "wc-ab2c"), "status: todo") {
		t.Fatalf("outside-lens todo was claimed: %s", ticketBody(t, dir, "wc-ab2c"))
	}

	wClear := doPost(s, "/scope/wc/lens/clear", url.Values{"return": {"/scope/wc"}})
	cleared := mustFollow(t, s, wClear)
	if !strings.Contains(colSection(cleared.Body.String(), status.Todo), "Earlier") {
		t.Fatalf("cleared board missing outside-lens card: %s", cleared.Body.String())
	}
	got = loadLens(t, app)
	if _, ok := got["wc"]; ok {
		t.Fatalf("wc lens still set: %v", got["wc"])
	}
	if len(got["bb"]) != 1 || got["bb"][0] != "keep" {
		t.Fatalf("clear wc dropped bb: %v", got["bb"])
	}
}

func TestPOSTLensEmptySetDoesNotWrite(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "tags: [frontend]\n")
	setLens(t, app, "wc", []string{"frontend"})
	s := mustServer(t, app)
	before := lensFile(t, app)

	w := doPost(s, "/scope/wc/lens", url.Values{
		"tag":    {""},
		"return": {"/scope/wc"},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("empty set want 303, got %d %s", w.Code, w.Body.String())
	}
	if lensFile(t, app) != before {
		t.Fatalf("empty set wrote lens.cue:\n%s", lensFile(t, app))
	}
	got := loadLens(t, app)
	if len(got["wc"]) != 1 || got["wc"][0] != "frontend" {
		t.Fatalf("empty set must not clear: %v", got["wc"])
	}

	w = doPost(s, "/scope/wc/lens", url.Values{"return": {"/scope/wc"}})
	mustFollow(t, s, w)
	if lensFile(t, app) != before {
		t.Fatal("no-tag POST wrote lens.cue")
	}
}

func TestPOSTLensFromInspectReturnsThereAndListsScopeTags(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "one", "todo", "a0", "# One\n", false, "tags: [alpha]\n")
	addTicket(t, dir, "wc-de34", "two", "todo", "a1", "# Two\n", false, "tags: [beta]\n")
	s := mustServer(t, app)

	ins := do(s, "/scope/wc/ab2c").Body.String()
	if !strings.Contains(ins, `value="beta"`) {
		t.Fatalf("inspect chrome picker missing other ticket tag: %s", ins)
	}
	if !strings.Contains(ins, `value="alpha"`) {
		t.Fatalf("inspect chrome picker missing own tag: %s", ins)
	}

	w := doPost(s, "/scope/wc/lens", url.Values{
		"tag":    {"beta"},
		"return": {"/scope/wc/ab2c"},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("want 303, got %d %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "/scope/wc/ab2c") {
		t.Fatalf("inspect POST must 303 to inspect, got %s", loc)
	}
	page := mustFollow(t, s, w)
	if !strings.Contains(page.Body.String(), "<dd>wc-ab2c</dd>") {
		t.Fatalf("follow is not inspect: %s", page.Body.String())
	}

	wClear := doPost(s, "/scope/wc/lens/clear", url.Values{"return": {"/scope/wc/ab2c"}})
	if wClear.Code != http.StatusSeeOther {
		t.Fatalf("clear want 303, got %d %s", wClear.Code, wClear.Body.String())
	}
	clearLoc := wClear.Header().Get("Location")
	if !strings.HasPrefix(clearLoc, "/scope/wc/ab2c") {
		t.Fatalf("inspect clear must 303 to inspect, got %s", clearLoc)
	}
	if strings.HasPrefix(clearLoc, "/scope/wc?") || clearLoc == "/scope/wc" {
		t.Fatalf("inspect clear 303d to board: %s", clearLoc)
	}
	cleared := mustFollow(t, s, wClear)
	if !strings.Contains(cleared.Body.String(), "<dd>wc-ab2c</dd>") {
		t.Fatalf("clear follow is not inspect: %s", cleared.Body.String())
	}
	if _, ok := loadLens(t, app)["wc"]; ok {
		t.Fatal("inspect clear left wc lens set")
	}
}

func TestPOSTLensPreservesBoardQuery(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "tags: [frontend]\n")
	addTicket(t, dir, "wc-de34", "drafty", "draft", "a1", "# Drafty\n", false, "tags: [frontend]\n")
	s := mustServer(t, app)

	ret := "/scope/wc?archived=1&backlog=1&tag=frontend"
	w := doPost(s, "/scope/wc/lens", url.Values{
		"tag":    {"frontend"},
		"return": {ret},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("want 303, got %d %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatal(err)
	}
	if u.Path != "/scope/wc" {
		t.Fatalf("path = %s", loc)
	}
	q := u.Query()
	if q.Get("backlog") != "1" || q.Get("archived") != "1" {
		t.Fatalf("board switches dropped: %s", loc)
	}
	if got := q["tag"]; len(got) != 1 || got[0] != "frontend" {
		t.Fatalf("tag chips dropped: %s", loc)
	}
	page := mustFollow(t, s, w)
	body := page.Body.String()
	if !strings.Contains(body, `class="switch on"`) {
		t.Fatalf("followed board missing on switches: %s", body)
	}
	if !strings.Contains(body, `class="tag on"`) {
		t.Fatalf("followed board missing active tag chip: %s", body)
	}
}

func TestValidLensReturn(t *testing.T) {
	tests := []struct {
		loc  string
		name string
		ok   bool
	}{
		{"/scope/wc?archived=1&tag=rel..notes", "wc", true},
		{"/search?scope=wc&q=..", "wc", true},
		{"/graphs?scope=wc", "wc", true},
		{"/graphs/depends?scope=wc", "wc", true},
		{"/maintenance?scope=wc", "wc", true},
		{"/scope/wc/ab2c", "wc", true},
		{"/scope/wc/../bb", "wc", false},
		{"/scope/wc/%2e%2e/bb", "wc", false},
		{"/scope/bb", "wc", false},
		{"//evil.example", "wc", false},
		{"https://evil.example/search", "wc", false},
		{"/scope/wc/foo/bar", "wc", false},
		{"", "wc", false},
	}
	for _, tc := range tests {
		if got := validLensReturn(tc.loc, tc.name); got != tc.ok {
			t.Errorf("validLensReturn(%q, %q) = %v, want %v", tc.loc, tc.name, got, tc.ok)
		}
	}
}

func TestPOSTLensReturnAllowsDotDotInQuery(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "tags: [rel..notes]\n")
	s := mustServer(t, app)

	w := doPost(s, "/scope/wc/lens", url.Values{
		"tag":    {"rel..notes"},
		"return": {"/scope/wc?archived=1&tag=rel..notes"},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("board: want 303, got %d %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatal(err)
	}
	if u.Path != "/scope/wc" {
		t.Fatalf("board path = %s", loc)
	}
	if u.Query().Get("archived") != "1" || u.Query().Get("tag") != "rel..notes" {
		t.Fatalf("board query dropped: %s", loc)
	}

	w = doPost(s, "/scope/wc/lens", url.Values{
		"tag":    {"rel..notes"},
		"return": {"/search?scope=wc&q=.."},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("search: want 303, got %d %s", w.Code, w.Body.String())
	}
	loc = w.Header().Get("Location")
	u, err = url.Parse(loc)
	if err != nil {
		t.Fatal(err)
	}
	if u.Path != "/search" {
		t.Fatalf("search dumped to board: %s", loc)
	}
	if u.Query().Get("scope") != "wc" || u.Query().Get("q") != ".." {
		t.Fatalf("search query dropped: %s", loc)
	}
}

func TestPOSTLensUnknownTagBannerOnNonBoard(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "tags: [frontend]\n")
	s := mustServer(t, app)

	for _, ret := range []string{"/search?scope=wc", "/graphs?scope=wc", "/maintenance?scope=wc"} {
		w := doPost(s, "/scope/wc/lens", url.Values{
			"tag":    {"ghost"},
			"return": {ret},
		})
		if w.Code != http.StatusSeeOther {
			t.Fatalf("%s: unknown tag must still 303, got %d %s", ret, w.Code, w.Body.String())
		}
		loc := w.Header().Get("Location")
		if !strings.HasPrefix(loc, strings.Split(ret, "?")[0]) || !strings.Contains(loc, "warning=") {
			t.Fatalf("%s location: %s", ret, loc)
		}
		page := do(s, loc)
		if page.Code != 200 {
			t.Fatalf("GET %s = %d", loc, page.Code)
		}
		body := page.Body.String()
		if !strings.Contains(body, "tag_unknown:") || !strings.Contains(body, "ghost") {
			t.Fatalf("banner missing on %s: %s", ret, body)
		}
		if strings.Count(body, "tag_unknown:") != 1 {
			t.Fatalf("notice printed twice on %s: %s", ret, body)
		}
	}
	got := loadLens(t, app)
	if len(got["wc"]) != 1 || got["wc"][0] != "ghost" {
		t.Fatalf("unknown tag must still write: %v", got["wc"])
	}

	board := doPost(s, "/scope/wc/lens", url.Values{
		"tag":    {"ghost"},
		"return": {"/scope/wc"},
	})
	boardPage := mustFollow(t, s, board)
	if strings.Count(boardPage.Body.String(), "tag_unknown:") != 1 {
		t.Fatalf("board notice not once: %s", boardPage.Body.String())
	}
	if strings.Contains(boardPage.Body.String(), `name="return" value="/scope/wc?`) {
		t.Fatalf("lens return must not keep notice query: %s", boardPage.Body.String())
	}

	wClear := doPost(s, "/scope/wc/lens/clear", url.Values{
		"return": {board.Header().Get("Location")},
	})
	if wClear.Code != http.StatusSeeOther {
		t.Fatalf("clear want 303, got %d %s", wClear.Code, wClear.Body.String())
	}
	clearLoc := wClear.Header().Get("Location")
	if strings.Contains(clearLoc, "warning=") || strings.Contains(clearLoc, "tag_unknown") {
		t.Fatalf("clear must not replay banner query: %s", clearLoc)
	}
	cleared := mustFollow(t, s, wClear)
	if strings.Contains(cleared.Body.String(), "tag_unknown:") {
		t.Fatalf("stale banner after clear: %s", cleared.Body.String())
	}
}

func TestPOSTLensNameDriftRefuses(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "tags: [frontend]\n")
	if err := scopeconfig.WriteMinimal(dir, "other", false); err != nil {
		t.Fatal(err)
	}
	s := mustServer(t, app)
	board := do(s, "/scope/wc").Body.String()
	if strings.Contains(board, `action="/scope/wc/lens"`) {
		t.Fatalf("name-drift still offers lens write: %s", board)
	}
	before := lensFile(t, app)
	w := doPost(s, "/scope/wc/lens", url.Values{"tag": {"frontend"}, "return": {"/scope/wc"}})
	if w.Code != http.StatusConflict {
		t.Fatalf("drift POST want 409, got %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), token.NameDrift) && !strings.Contains(w.Body.String(), "name_drift") {
		t.Fatalf("message: %s", w.Body.String())
	}
	if lensFile(t, app) != before {
		t.Fatal("drift POST wrote lens.cue")
	}
}

func TestPOSTLensUnparseableConfigStillWrites(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "earlier", "todo", "a0", "# Earlier\n", false, "tags: [backend]\n")
	addTicket(t, dir, "wc-de34", "tagged", "todo", "a1", "# Tagged\n", false, "tags: [frontend]\n")
	if err := os.WriteFile(filepath.Join(dir, "tk.cue"), []byte("name: \"wc\"\nthis is not cue {\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := mustServer(t, app)
	board := do(s, "/scope/wc").Body.String()
	if !strings.Contains(board, `action="/scope/wc/lens"`) {
		t.Fatalf("unparseable must offer lens: %s", board)
	}
	w := doPost(s, "/scope/wc/lens", url.Values{"tag": {"frontend"}, "return": {"/scope/wc"}})
	page := mustFollow(t, s, w)
	body := page.Body.String()
	if strings.Contains(colSection(body, status.Todo), "Earlier") {
		t.Fatalf("unparseable board must honour lens: %s", body)
	}
	if !strings.Contains(colSection(body, status.Todo), "Tagged") {
		t.Fatalf("unparseable lens hid matching card: %s", body)
	}

	wClear := doPost(s, "/scope/wc/lens/clear", url.Values{"return": {"/scope/wc"}})
	cleared := mustFollow(t, s, wClear)
	if !strings.Contains(colSection(cleared.Body.String(), status.Todo), "Earlier") {
		t.Fatalf("unparseable clear must restore outside-lens card: %s", cleared.Body.String())
	}
	if _, ok := loadLens(t, app)["wc"]; ok {
		t.Fatal("unparseable clear left wc lens set")
	}
}

func TestPOSTLensUnknownScope(t *testing.T) {
	app := newTestApp(t)
	s := mustServer(t, app)
	w := doPost(s, "/scope/ghost/lens", url.Values{"tag": {"x"}})
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d %s", w.Code, w.Body.String())
	}
}

func TestPOSTLensRefusesForeignOrigin(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "")
	s := mustServer(t, app)
	before := lensFile(t, app)
	w := doPostHeader(s, "/scope/wc/lens", url.Values{"tag": {"frontend"}}, http.Header{"Origin": {"https://evil.example"}})
	if w.Code != http.StatusForbidden {
		t.Fatalf("foreign origin: want 403, got %d %s", w.Code, w.Body.String())
	}
	if lensFile(t, app) != before {
		t.Fatal("foreign origin wrote lens.cue")
	}
}
