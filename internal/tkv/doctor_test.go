package tkv

import (
	"bytes"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/token"
)

func pinAlphaToOmega(t *testing.T, path string) {
	t.Helper()
	const from, to = "# Alpha\n", "# Omega\n"
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	old := st.ModTime()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := bytes.Replace(body, []byte(from), []byte(to), 1)
	if len(updated) != len(body) {
		t.Fatalf("replacement must keep size (%q -> %q)", from, to)
	}
	if err := os.WriteFile(path, updated, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
}

func indexTitles(t *testing.T, s *Server) map[string][]string {
	t.Helper()
	rows, err := s.db.AllTickets()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	for _, p := range rows {
		out[p.Scope] = append(out[p.Scope], p.Title)
	}
	return out
}

func TestDoctorPageDiagnosesAllScopes(t *testing.T) {
	app := newTestApp(t)
	clean := initScope(t, app, "wc")
	dirty := initScope(t, app, "zz")
	addTicket(t, clean, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "")
	addTicket(t, dirty, "zz-ab2c", "alpha", "todo", "a0", "# Alpha\n", false, "")
	addTicket(t, dirty, "zz-ab2c", "beta", "todo", "a1", "# Beta\n", false, "")
	app.Cwd = clean
	app.ScopeFlag = "wc"
	app.EnvScope = "wc"
	s := mustServer(t, app)

	for _, path := range []string{"/doctor", "/doctor?scope=wc"} {
		w := do(s, path)
		if w.Code != 200 {
			t.Fatalf("%s = %d %s", path, w.Code, w.Body.String())
		}
		body := w.Body.String()
		if !strings.Contains(body, `class="current">Doctor</a>`) {
			t.Fatalf("%s missing Doctor current: %s", path, body)
		}
		if strings.Contains(body, "Maintenance") || strings.Contains(body, "/maintenance") {
			t.Fatalf("%s still names maintenance: %s", path, body)
		}
		if !strings.Contains(body, token.DuplicateID) {
			t.Fatalf("%s must show zz duplicate_id despite selected wc: %s", path, body)
		}
		if !strings.Contains(body, "zz-ab2c") {
			t.Fatalf("%s must name the colliding id: %s", path, body)
		}
		if !strings.Contains(body, "tk repair") {
			t.Fatalf("%s must name tk repair: %s", path, body)
		}
		if strings.Contains(body, "tk doctor --repair") {
			t.Fatalf("%s must not teach doctor --repair: %s", path, body)
		}
		if strings.Contains(body, `action="/doctor/repair"`) || strings.Contains(body, ">Repair</button>") {
			t.Fatalf("%s must not offer a repair control: %s", path, body)
		}
	}

	scoped := do(s, "/doctor?scope=wc").Body.String()
	if !strings.Contains(scoped, `action="/doctor/reindex"`) || !strings.Contains(scoped, `action="/doctor/sync"`) {
		t.Fatalf("doctor forms must stay machine-wide: %s", scoped)
	}
	if strings.Contains(scoped, `action="/doctor/reindex?`) || strings.Contains(scoped, `action="/doctor/sync?`) {
		t.Fatalf("reindex and sync all must not carry a scope query: %s", scoped)
	}
}

func TestOldMaintenancePathsAreGone(t *testing.T) {
	app := newTestApp(t)
	initScope(t, app, "wc")
	s := mustServer(t, app)
	for _, path := range []string{"/maintenance", "/maintenance/sync", "/maintenance/reindex"} {
		w := do(s, path)
		if w.Code != http.StatusNotFound {
			t.Fatalf("GET %s = %d, want 404", path, w.Code)
		}
		w = doPost(s, path, url.Values{})
		if w.Code == http.StatusSeeOther {
			t.Fatalf("POST %s redirected: %s", path, w.Header().Get("Location"))
		}
		if w.Code != http.StatusNotFound && w.Code != http.StatusMethodNotAllowed {
			t.Fatalf("POST %s = %d, want 404", path, w.Code)
		}
	}
}

func TestGETDoctorDoesNotRebuild(t *testing.T) {
	app := newTestApp(t)
	wc := initScope(t, app, "wc")
	zz := initScope(t, app, "zz")
	wcPath := filepath.Join(wc, "wc-ab2c-x.md")
	zzPath := filepath.Join(zz, "zz-cd34-y.md")
	addTicket(t, wc, "wc-ab2c", "x", "todo", "a0", "# Alpha\n", false, "")
	addTicket(t, zz, "zz-cd34", "y", "todo", "a0", "# Alpha\n", false, "")
	s := mustServer(t, app)

	if w := do(s, "/doctor"); w.Code != 200 {
		t.Fatalf("seed GET /doctor = %d %s", w.Code, w.Body.String())
	}
	pinAlphaToOmega(t, wcPath)
	pinAlphaToOmega(t, zzPath)

	if w := do(s, "/doctor"); w.Code != 200 {
		t.Fatalf("GET /doctor after pin = %d %s", w.Code, w.Body.String())
	}
	got := indexTitles(t, s)
	for _, scope := range []string{"wc", "zz"} {
		if titles := got[scope]; len(titles) != 1 || titles[0] != "Alpha" {
			t.Fatalf("GET must not rebuild %s, got %v", scope, titles)
		}
	}
}

func TestPOSTDoctorReindexIsMachineWide(t *testing.T) {
	app := newTestApp(t)
	wc := initScope(t, app, "wc")
	zz := initScope(t, app, "zz")
	wcPath := filepath.Join(wc, "wc-ab2c-x.md")
	zzPath := filepath.Join(zz, "zz-cd34-y.md")
	addTicket(t, wc, "wc-ab2c", "x", "todo", "a0", "# Alpha\n", false, "")
	addTicket(t, zz, "zz-cd34", "y", "todo", "a0", "# Alpha\n", false, "")
	addTicket(t, zz, "zz-ab2c", "alpha", "todo", "a1", "# DupA\n", false, "")
	addTicket(t, zz, "zz-ab2c", "beta", "todo", "a2", "# DupB\n", false, "")
	s := mustServer(t, app)

	if w := do(s, "/doctor"); w.Code != 200 {
		t.Fatalf("seed GET /doctor = %d %s", w.Code, w.Body.String())
	}
	pinAlphaToOmega(t, wcPath)
	pinAlphaToOmega(t, zzPath)
	beforeWC, err := os.ReadFile(wcPath)
	if err != nil {
		t.Fatal(err)
	}

	w := doPost(s, "/doctor/reindex?scope=wc", url.Values{})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("want 303, got %d %s", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); loc != "/doctor" {
		t.Fatalf("Location = %q", loc)
	}

	page := mustFollow(t, s, w)
	if page.Code != 200 {
		t.Fatalf("dashboard after reindex = %d %s", page.Code, page.Body.String())
	}
	body := page.Body.String()
	if strings.Contains(body, "no such table") {
		t.Fatalf("stuck schema after reindex: %s", body)
	}
	if !strings.Contains(body, token.DuplicateID) {
		t.Fatalf("reindex must not repair collisions: %s", body)
	}
	if _, err := os.Stat(filepath.Join(zz, "zz-ab2c-alpha.md")); err != nil {
		t.Fatalf("reindex mutated tickets: %v", err)
	}
	if _, err := os.Stat(filepath.Join(zz, "zz-ab2c-beta.md")); err != nil {
		t.Fatalf("reindex mutated tickets: %v", err)
	}
	afterWC, err := os.ReadFile(wcPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeWC, afterWC) {
		t.Fatal("reindex must not touch ticket files")
	}

	got := indexTitles(t, s)
	for _, scope := range []string{"wc", "zz"} {
		found := false
		for _, title := range got[scope] {
			if title == "Omega" {
				found = true
			}
		}
		if !found {
			t.Fatalf("reindex must refill %s from files, got %v", scope, got[scope])
		}
	}

	home := do(s, "/")
	if home.Code != 200 {
		t.Fatalf("overview after reindex = %d %s", home.Code, home.Body.String())
	}
}

func TestPOSTDoctorReindexDoesNotDropPinnedHandle(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "x", "todo", "a0", "# Alpha\n", false, "")
	s := mustServer(t, app)
	if w := do(s, "/doctor"); w.Code != 200 {
		t.Fatalf("seed = %d %s", w.Code, w.Body.String())
	}

	h, err := s.withIndexPin(func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	w := doPost(s, "/doctor/reindex", url.Values{})
	if w.Code != http.StatusServiceUnavailable {
		s.releaseHandle(h)
		t.Fatalf("want 503 while a write pin is held, got %d %s", w.Code, w.Body.String())
	}
	rows, err := h.db.AllTickets()
	if err != nil {
		s.releaseHandle(h)
		t.Fatalf("pinned handle after refused reindex: %v", err)
	}
	if len(rows) != 1 || rows[0].Title != "Alpha" {
		s.releaseHandle(h)
		t.Fatalf("pinned snapshot lost, got %+v", rows)
	}
	s.releaseHandle(h)

	path := filepath.Join(dir, "wc-ab2c-x.md")
	pinAlphaToOmega(t, path)
	w = doPost(s, "/doctor/reindex", url.Values{})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("want 303 after pin release, got %d %s", w.Code, w.Body.String())
	}
	got := indexTitles(t, s)
	if titles := got["wc"]; len(titles) != 1 || titles[0] != "Omega" {
		t.Fatalf("reindex after drain must refill, got %v", titles)
	}
}

func TestPOSTDoctorReindexRefusesForeignOrigin(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "")
	s := mustServer(t, app)
	if w := do(s, "/doctor"); w.Code != 200 {
		t.Fatalf("seed = %d", w.Code)
	}

	w := doPostHeader(s, "/doctor/reindex", url.Values{}, http.Header{"Origin": {"https://evil.example"}})
	if w.Code != http.StatusForbidden {
		t.Fatalf("foreign origin: want 403, got %d %s", w.Code, w.Body.String())
	}
	w = doPostHeader(s, "/doctor/reindex", url.Values{}, http.Header{"Sec-Fetch-Site": {"cross-site"}})
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-site: want 403, got %d %s", w.Code, w.Body.String())
	}
}

func TestDoctorReindexOpensFreshIndex(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "")
	s := mustServer(t, app)
	if w := do(s, "/doctor"); w.Code != 200 {
		t.Fatalf("seed = %d", w.Code)
	}
	stale, err := index.Open(app.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stale.Close() })
	if err := stale.Rebuild(); err != nil {
		t.Fatal(err)
	}

	w := doPost(s, "/doctor/reindex", url.Values{})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("want 303, got %d %s", w.Code, w.Body.String())
	}
	page := mustFollow(t, s, w)
	if page.Code != 200 {
		t.Fatalf("after competing rebuild = %d %s", page.Code, page.Body.String())
	}
	if !strings.Contains(page.Body.String(), "wc") {
		t.Fatalf("refilled doctor missing scope: %s", page.Body.String())
	}
}
