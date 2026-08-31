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

	"github.com/p3bot/tk/internal/syncengine"
	"github.com/p3bot/tk/internal/testgit"
)

func hasHardBanner(body string) bool {
	return strings.Contains(body, `<p class="banner">`)
}

func hasSoftBanner(body string) bool {
	return strings.Contains(body, `<p class="banner soft">`)
}

func noticeKeysInLocation(loc string) []string {
	u, err := url.Parse(loc)
	if err != nil {
		return []string{loc}
	}
	q := u.Query()
	var hit []string
	for _, k := range noticeQueryKeys {
		if _, ok := q[k]; ok {
			hit = append(hit, k)
		}
	}
	return hit
}

func porcelain(t *testing.T, repo string) string {
	t.Helper()
	var keep []string
	for _, line := range strings.Split(testgit.Combined(t, repo, "status", "--porcelain"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, ".tk.lock") {
			continue
		}
		keep = append(keep, line)
	}
	return strings.Join(keep, "\n")
}

func unpushed(t *testing.T, repo string) string {
	t.Helper()
	return testgit.Combined(t, repo, "rev-list", "--count", "@{u}..HEAD")
}

func TestChromeSyncHiddenWithoutSelectedOrAutoCommit(t *testing.T) {
	app := newTestApp(t)
	plain := initScope(t, app, "pl")
	addTicket(t, plain, "pl-ab2c", "work", "todo", "a0", "# Work\n", false, "")
	dir, _ := initDrivenScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "")
	s := mustServer(t, app)

	home := do(s, "/").Body.String()
	if strings.Contains(home, "chrome-sync") {
		t.Fatalf("overview must not offer chrome Sync: %s", home)
	}

	plainBoard := do(s, "/scope/pl").Body.String()
	if strings.Contains(plainBoard, `action="/scope/pl/sync"`) {
		t.Fatalf("plain-files must not offer chrome Sync: %s", plainBoard)
	}

	driven := do(s, "/scope/wc").Body.String()
	if !strings.Contains(driven, `method="post" action="/scope/wc/sync"`) {
		t.Fatalf("tk-driven board missing chrome Sync: %s", driven)
	}
	if !strings.Contains(driven, `onsubmit="if(this.dataset.submitted)return false;`) {
		t.Fatalf("chrome Sync missing disable-on-submit: %s", driven)
	}

	maint := do(s, "/maintenance").Body.String()
	if !strings.Contains(maint, `method="post" action="/maintenance/sync"`) {
		t.Fatalf("maintenance missing Sync all: %s", maint)
	}
	if strings.Contains(maint, "tk sync,") || strings.Contains(maint, "tk sync)") {
		t.Fatalf("maintenance still says sync stays on the CLI: %s", maint)
	}
}

func TestChromeSyncPOSTAmbientPushesAndReturns(t *testing.T) {
	app := newTestApp(t)
	dir, repo := initDrivenScope(t, app, "wc")
	pushOrigin(t, repo)
	addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "")
	if porcelain(t, repo) == "" {
		t.Fatal("seed ticket should be dirty before sync")
	}

	s := mustServer(t, app)
	w := doPost(s, "/scope/wc/sync", url.Values{
		"return": {"/scope/wc"},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("want 303, got %d %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if loc != "/scope/wc" {
		t.Fatalf("Location = %q", loc)
	}
	if hit := noticeKeysInLocation(loc); len(hit) > 0 {
		t.Fatalf("sync output stuffed into Location %s: %v", loc, hit)
	}
	if porcelain(t, repo) != "" {
		t.Fatalf("dirty after chrome Sync: %s", porcelain(t, repo))
	}
	if n := unpushed(t, repo); n != "0" {
		t.Fatalf("unpushed after chrome Sync = %s", n)
	}

	page := mustFollow(t, s, w)
	body := page.Body.String()
	if hasHardBanner(body) {
		t.Fatalf("clean sync must not use issue banners: %s", body)
	}
	if !hasSoftBanner(body) {
		t.Fatalf("clean sync should keep reporter lines as soft notices: %s", body)
	}
}

func TestChromeSyncPOSTRefusesNoSelectedScope(t *testing.T) {
	app := newTestApp(t)
	dir, repo := initDrivenScope(t, app, "wc")
	pushOrigin(t, repo)
	addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "")
	s := mustServer(t, app)

	w := doPost(s, "/sync", url.Values{})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "selected scope") {
		t.Fatalf("refuse must name the missing scope: %s", w.Body.String())
	}
	if porcelain(t, repo) == "" {
		t.Fatal("no-scope chrome Sync must not become --all")
	}

	w = doPost(s, "/sync", url.Values{"scope": {"wc"}, "return": {"/scope/wc"}})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unscoped /sync with a form scope must still refuse, got %d %s", w.Code, w.Body.String())
	}
	if porcelain(t, repo) == "" {
		t.Fatal("POST /sync must not run ambient sync from a form field")
	}
}

func TestChromeSyncPOSTRefusesNonAutoCommit(t *testing.T) {
	app := newTestApp(t)
	plain := initScope(t, app, "pl")
	addTicket(t, plain, "pl-ab2c", "work", "todo", "a0", "# Work\n", false, "")
	dir, repo := initDrivenScope(t, app, "wc")
	pushOrigin(t, repo)
	addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "")
	s := mustServer(t, app)

	w := doPost(s, "/scope/pl/sync", url.Values{"return": {"/scope/pl"}})
	if w.Code == http.StatusSeeOther {
		t.Fatalf("non-auto-commit must not look like success: %s", w.Header().Get("Location"))
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "auto-commit") {
		t.Fatalf("refuse must name auto-commit: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `class="selected">pl</a>`) {
		t.Fatalf("refused chrome Sync must keep the selected scope: %s", w.Body.String())
	}
	if porcelain(t, repo) == "" {
		t.Fatal("non-auto-commit chrome Sync must not sync another root")
	}
}

func TestGETDoesNotSync(t *testing.T) {
	app := newTestApp(t)
	dir, repo := initDrivenScope(t, app, "wc")
	pushOrigin(t, repo)
	addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "")
	s := mustServer(t, app)

	before := porcelain(t, repo)
	for _, path := range []string{"/", "/scope/wc", "/maintenance", "/sync", "/scope/wc/sync", "/maintenance/sync"} {
		w := do(s, path)
		if w.Code == http.StatusSeeOther {
			t.Fatalf("GET %s redirected as a sync: %s", path, w.Header().Get("Location"))
		}
	}
	if porcelain(t, repo) != before {
		t.Fatalf("GET mutated git state:\n%s\n---\n%s", before, porcelain(t, repo))
	}
}

func TestMaintenanceSyncAllIsolatesPerRoot(t *testing.T) {
	app := newTestApp(t)
	goodDir, goodRepo := initDrivenScope(t, app, "aa")
	pushOrigin(t, goodRepo)
	addTicket(t, goodDir, "aa-ab2c", "work", "todo", "a0", "# Good\n", false, "")

	badDir, badRepo := initDrivenScope(t, app, "zz")
	bare := pushOrigin(t, badRepo)
	addTicket(t, badDir, "zz-ab2c", "work", "todo", "a0", "# Bad\n", false, "")
	if err := os.RemoveAll(bare); err != nil {
		t.Fatal(err)
	}

	s := mustServer(t, app)
	w := doPost(s, "/maintenance/sync", url.Values{})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("want 303, got %d %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if loc != "/maintenance" {
		t.Fatalf("Location = %q", loc)
	}
	if hit := noticeKeysInLocation(loc); len(hit) > 0 {
		t.Fatalf("sync output stuffed into Location %s: %v", loc, hit)
	}

	if porcelain(t, goodRepo) != "" {
		t.Fatalf("healthy root should still snapshot: %s", porcelain(t, goodRepo))
	}
	if n := unpushed(t, goodRepo); n != "0" {
		t.Fatalf("healthy root unpushed = %s", n)
	}

	page := mustFollow(t, s, w)
	body := page.Body.String()
	if !hasHardBanner(body) {
		t.Fatalf("needs-attention must use issue banners, not soft: %s", body)
	}
	if !strings.Contains(body, "zz") {
		t.Fatalf("banners must name the failed root: %s", body)
	}
	if !strings.Contains(body, "fetch failed") && !strings.Contains(body, "origin") {
		t.Fatalf("banners must say why zz failed: %s", body)
	}
	if porcelain(t, badRepo) == "" && unpushed(t, badRepo) == "0" {
		t.Fatal("failed root must not look fully synced")
	}
}

func TestSyncUnlocksIndexMutex(t *testing.T) {
	app := newTestApp(t)
	dir, repo := initDrivenScope(t, app, "wc")
	pushOrigin(t, repo)
	addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "")
	s := mustServer(t, app)

	started := make(chan struct{})
	unblock := make(chan struct{})
	s.afterIndexUnlock = func() {
		close(started)
		<-unblock
	}

	syncCh := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		syncCh <- doPost(s, "/scope/wc/sync", url.Values{"return": {"/scope/wc"}})
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("sync did not drop the index mutex")
	}

	homeCh := make(chan *httptest.ResponseRecorder, 1)
	go func() { homeCh <- do(s, "/") }()
	select {
	case home := <-homeCh:
		if home.Code != 200 {
			t.Fatalf("overview during sync = %d", home.Code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("overview blocked; sync still holding Server.mu")
	}
	close(unblock)
	w := <-syncCh
	if w.Code != http.StatusSeeOther {
		t.Fatalf("sync = %d %s", w.Code, w.Body.String())
	}
}

func TestBeginSyncStripsCancelAndIsolatesCue(t *testing.T) {
	app := newTestApp(t)
	initScope(t, app, "wc")
	s := mustServer(t, app)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	deps, release, err := s.beginSync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := deps.Ctx.Err(); err != nil {
		t.Fatalf("sync context must not carry request cancel: %v", err)
	}
	if deps.Cue == s.app.cue() {
		t.Fatal("sync must not share the process CUE context")
	}
	if deps.Rec == s.rec {
		t.Fatal("sync must not share the process reconciler")
	}
	if deps.DB != s.cur.db {
		t.Fatal("sync must use the pinned index")
	}
}

func TestPOSTSyncRefusesForeignOrigin(t *testing.T) {
	app := newTestApp(t)
	dir, repo := initDrivenScope(t, app, "wc")
	pushOrigin(t, repo)
	addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "")
	s := mustServer(t, app)

	w := doPostHeader(s, "/scope/wc/sync", url.Values{}, http.Header{"Origin": {"https://evil.example"}})
	if w.Code != http.StatusForbidden {
		t.Fatalf("chrome foreign origin: want 403, got %d %s", w.Code, w.Body.String())
	}
	w = doPostHeader(s, "/maintenance/sync", url.Values{}, http.Header{"Sec-Fetch-Site": {"cross-site"}})
	if w.Code != http.StatusForbidden {
		t.Fatalf("maintenance cross-site: want 403, got %d %s", w.Code, w.Body.String())
	}
	if porcelain(t, repo) == "" {
		t.Fatal("foreign origin must not sync")
	}
}

func TestChromeSyncNeedsAttentionBannersNotInQuery(t *testing.T) {
	app := newTestApp(t)
	dir, _ := initDrivenScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "")

	s := mustServer(t, app)
	w := doPost(s, "/scope/wc/sync", url.Values{
		"return": {"/scope/wc/ab2c"},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("paused/disabled sync must still 303, got %d %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if loc != "/scope/wc/ab2c" {
		t.Fatalf("must return to inspect, Location = %q", loc)
	}
	if hit := noticeKeysInLocation(loc); len(hit) > 0 {
		t.Fatalf("needs-attention stuffed into Location %s: %v", loc, hit)
	}
	page := mustFollow(t, s, w)
	body := page.Body.String()
	if !hasHardBanner(body) {
		t.Fatalf("needs-attention banners missing: %s", body)
	}
	if hasSoftBanner(body) {
		t.Fatalf("blocked sync must not share lens-warning style: %s", body)
	}
	if !strings.Contains(body, "sync_disabled:") && !strings.Contains(body, "no upstream") && !strings.Contains(body, "need attention") {
		t.Fatalf("banner must name follow-up: %s", body)
	}
}

func TestChromeSyncUnknownScope(t *testing.T) {
	app := newTestApp(t)
	initScope(t, app, "wc")
	s := mustServer(t, app)
	w := doPost(s, "/scope/zz/sync", url.Values{})
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d %s", w.Code, w.Body.String())
	}
}

func TestChromeSyncDoesNotTargetSiblingRoot(t *testing.T) {
	app := newTestApp(t)
	aDir, aRepo := initDrivenScope(t, app, "aa")
	pushOrigin(t, aRepo)
	addTicket(t, aDir, "aa-ab2c", "work", "todo", "a0", "# A\n", false, "")
	zDir, zRepo := initDrivenScope(t, app, "zz")
	pushOrigin(t, zRepo)
	addTicket(t, zDir, "zz-ab2c", "work", "todo", "a0", "# Z\n", false, "")
	s := mustServer(t, app)

	w := doPost(s, "/scope/aa/sync", url.Values{"return": {"/scope/aa"}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("want 303, got %d %s", w.Code, w.Body.String())
	}
	if porcelain(t, aRepo) != "" {
		t.Fatalf("ambient root still dirty: %s", porcelain(t, aRepo))
	}
	if porcelain(t, zRepo) == "" {
		t.Fatal("chrome Sync synced a sibling git-root")
	}
}

func TestCapturingReporterKeepsIndent(t *testing.T) {
	r := &capturingReporter{}
	r.Err("non_allowlist: 1 path(s)")
	r.Err("  leftover.md")
	r.Err("   ")
	r.Err("")
	got := r.lines()
	if len(got) != 2 || got[0] != "non_allowlist: 1 path(s)" || got[1] != "  leftover.md" {
		t.Fatalf("indent stripped or blanks kept: %#v", got)
	}
}

func TestGETInspectSyncIDIsNotAWrite(t *testing.T) {
	app := newTestApp(t)
	s := mustServer(t, app)
	w := do(s, "/scope/wc/sync")
	if w.Code == http.StatusSeeOther {
		t.Fatalf("GET inspect id=sync wrote: %s", w.Header().Get("Location"))
	}
}

func TestSyncNoticesConsumedOnReturnGET(t *testing.T) {
	app := newTestApp(t)
	initScope(t, app, "wc")
	s := mustServer(t, app)
	s.putSyncNotices("/scope/wc", syncFlash{lines: []string{"residue: leftover.md"}, attention: true})
	first := do(s, "/scope/wc").Body.String()
	if !strings.Contains(first, "residue: leftover.md") || !hasHardBanner(first) {
		t.Fatalf("first GET missing issue banner: %s", first)
	}
	second := do(s, "/scope/wc").Body.String()
	if strings.Contains(second, "residue: leftover.md") {
		t.Fatal("stashed banner replayed on second GET")
	}
}

func TestChromeSyncFormOnInspectAndMaintenanceWhenDriven(t *testing.T) {
	app := newTestApp(t)
	dir, _ := initDrivenScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "")
	s := mustServer(t, app)

	ins := do(s, "/scope/wc/ab2c").Body.String()
	if !strings.Contains(ins, `action="/scope/wc/sync"`) {
		t.Fatalf("inspect missing chrome Sync: %s", ins)
	}
	maint := do(s, "/maintenance?scope=wc").Body.String()
	if !strings.Contains(maint, `action="/scope/wc/sync"`) {
		t.Fatalf("maintenance with selected tk-driven scope missing chrome Sync: %s", maint)
	}
	if !strings.Contains(maint, `action="/maintenance/sync"`) {
		t.Fatalf("maintenance missing Sync all next to chrome Sync: %s", maint)
	}
}

func TestChromeSyncResumesPausedBodyConflict(t *testing.T) {
	appA := newTestApp(t)
	dirA, repoA := initDrivenScope(t, appA, "wc")
	addTicket(t, dirA, "wc-ab2c", "work", "todo", "a0", "# Work\n\nbody line\n", false, "")
	bare := pushOrigin(t, repoA)

	parent := t.TempDir()
	repoB := filepath.Join(parent, "b")
	testgit.Run(t, parent, "clone", bare, "b")
	testgit.Run(t, repoB, "config", "user.email", "a@b.c")
	testgit.Run(t, repoB, "config", "user.name", "tk-test")
	testgit.Run(t, repoB, "config", "commit.gpgsign", "false")
	dirB := filepath.Join(repoB, "wc")
	appB := newTestApp(t)
	registerScope(t, appB, "wc", dirB, repoB)
	pB := filepath.Join(dirB, "wc-ab2c-work.md")

	replaceOnce(t, filepath.Join(dirA, "wc-ab2c-work.md"), "body line", "A version of the body")
	sA := mustServer(t, appA)
	w := doPost(sA, "/scope/wc/sync", url.Values{"return": {"/scope/wc"}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("A sync: want 303, got %d %s", w.Code, w.Body.String())
	}

	replaceOnce(t, pB, "body line", "B version of the body")

	s := mustServer(t, appB)
	w = doPost(s, "/scope/wc/sync", url.Values{"return": {"/scope/wc"}})
	if w.Code == http.StatusConflict {
		t.Fatalf("chrome Sync must resume a mid-rebase, not refuse like claim: %s", w.Body.String())
	}
	if w.Code != http.StatusSeeOther {
		t.Fatalf("paused sync: want 303, got %d %s", w.Code, w.Body.String())
	}
	if hit := noticeKeysInLocation(w.Header().Get("Location")); len(hit) > 0 {
		t.Fatalf("pause stuffed into Location: %s", w.Header().Get("Location"))
	}
	page := mustFollow(t, s, w)
	body := page.Body.String()
	if !hasHardBanner(body) {
		t.Fatalf("paused rebase must use issue banners: %s", body)
	}
	if !strings.Contains(body, "body conflict") && !strings.Contains(body, "paused") {
		t.Fatalf("banner must name the pause: %s", body)
	}
	raw, err := os.ReadFile(pB)
	if err != nil {
		t.Fatal(err)
	}
	if !syncengine.HasConflictMarker(raw) {
		t.Fatalf("body should carry conflict markers:\n%s", raw)
	}

	if err := os.WriteFile(pB, []byte(stripConflictMarkers(string(raw), "resolved body")), 0o644); err != nil {
		t.Fatal(err)
	}
	w = doPost(s, "/scope/wc/sync", url.Values{"return": {"/scope/wc"}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("resume: want 303, got %d %s", w.Code, w.Body.String())
	}
	page = mustFollow(t, s, w)
	if hasHardBanner(page.Body.String()) {
		t.Fatalf("resolved resume must not stay in needs-attention: %s", page.Body.String())
	}
	if n := unpushed(t, repoB); n != "0" {
		t.Fatalf("B unpushed after resume = %s", n)
	}
}

func replaceOnce(t *testing.T, path, old, new string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, old) {
		t.Fatalf("%s: %q not found:\n%s", path, old, s)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(s, old, new, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func stripConflictMarkers(content, resolvedBody string) string {
	var out []string
	skip := false
	replaced := false
	for _, line := range strings.Split(content, "\n") {
		switch {
		case strings.HasPrefix(line, "<<<<<<<"):
			skip = true
			if !replaced {
				out = append(out, resolvedBody)
				replaced = true
			}
		case strings.HasPrefix(line, "======="):
		case strings.HasPrefix(line, ">>>>>>>"):
			skip = false
		case !skip:
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}
