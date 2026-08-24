package tkv

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/p3bot/tk/internal/git"
	"github.com/p3bot/tk/internal/registry"
	"github.com/p3bot/tk/internal/scopeconfig"
	"github.com/p3bot/tk/internal/status"
	"github.com/p3bot/tk/internal/testgit"
	"github.com/p3bot/tk/internal/token"
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
	if err := store.WriteLens(map[string][]string{scope: tags}); err != nil {
		t.Fatal(err)
	}
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
	for _, path := range []string{"/scope/wc", "/scope/wc/ab2c", "/scope/wc/mark", "/scope/wc/claim"} {
		w := do(s, path)
		if w.Code == http.StatusSeeOther {
			t.Fatalf("GET %s redirected as a write: %s", path, w.Header().Get("Location"))
		}
	}
	// GET /scope/{name}/mark and /claim collide with inspect {id}; they must 404, not hit POST.
	if code := do(s, "/scope/wc/mark").Code; code != http.StatusNotFound {
		t.Fatalf("GET /scope/wc/mark = %d, want inspect 404", code)
	}
	if code := do(s, "/scope/wc/claim").Code; code != http.StatusNotFound {
		t.Fatalf("GET /scope/wc/claim = %d, want inspect 404", code)
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
	if !strings.Contains(board, "Claim next") {
		t.Fatalf("kanban missing claim next: %s", board)
	}
	if !strings.Contains(board, `onsubmit="if(this.dataset.submitted)return false;`) {
		t.Fatalf("claim next form missing double-submit guard: %s", board)
	}
	if strings.Contains(board, `option value="in-progress"`) {
		t.Fatalf("todo card must not mark in-progress: %s", board)
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
		if strings.Contains(board, `method="post"`) {
			t.Fatalf("unusable schema still offers writes: %s", board)
		}
		if strings.Contains(board, "Claim next") {
			t.Fatalf("unusable schema still offers claim next: %s", board)
		}
		ins := do(s, "/scope/wc/ab2c").Body.String()
		if strings.Contains(ins, `method="post"`) {
			t.Fatalf("inspect still offers writes: %s", ins)
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
		ins := do(s, "/scope/wc/abcd").Body.String()
		if strings.Contains(ins, `method="post"`) {
			t.Fatalf("inspect still offers writes: %s", ins)
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
