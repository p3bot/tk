package tkv

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/notes"
	"github.com/p3bot/tk/internal/registry"
	"github.com/p3bot/tk/internal/scopefile"
	"github.com/p3bot/tk/internal/testgit"
)

func writeNote(t *testing.T, dir, slug, body string) string {
	t.Helper()
	path := filepath.Join(dir, scopefile.NoteDir, slug+".md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func readNote(t *testing.T, dir, slug string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, scopefile.NoteDir, slug+".md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestGETNotesIsNotInspect(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	writeNote(t, dir, "pad", "hello **world**\n")
	writeNote(t, dir, "alpha", "second note\n")
	s := mustServer(t, app)

	unknown := do(s, "/scope/zz/notes")
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("unknown scope = %d %s", unknown.Code, unknown.Body.String())
	}

	w := do(s, "/scope/wc/notes")
	if w.Code != http.StatusOK {
		t.Fatalf("list = %d %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if strings.Contains(body, `unknown ticket id "notes"`) {
		t.Fatalf("list treated as inspect: %s", body)
	}
	if !strings.Contains(body, `class="current">Notes</a>`) {
		t.Errorf("notes section: %s", body)
	}
	if !strings.Contains(body, ">pad</a>") || !strings.Contains(body, ">alpha</a>") {
		t.Errorf("missing slug: %s", body)
	}
	if !strings.Contains(body, "Default") || !strings.Contains(body, ">default</a>") {
		t.Errorf("effective default missing: %s", body)
	}
	if !strings.Contains(body, "(no file)") {
		t.Errorf("missing default file should be labelled: %s", body)
	}
	if strings.Contains(body, `action="/scope/wc/mark"`) || strings.Contains(body, `action="/scope/wc/claim"`) {
		t.Fatalf("list offered ticket writes: %s", body)
	}
	if strings.Contains(body, "/delete") {
		t.Fatalf("list offered delete: %s", body)
	}
	if strings.Contains(body, `name="return" value="inspect"`) {
		t.Fatalf("list use/clear must not return to inspect: %s", body)
	}

	bad := do(s, "/scope/wc/notes/list")
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("reserved slug = %d %s", bad.Code, bad.Body.String())
	}
	if !strings.Contains(bad.Body.String(), `&#34;list&#34; is a reserved note name`) {
		t.Fatalf("reserved copy: %s", bad.Body.String())
	}
	invalid := do(s, "/scope/wc/notes/Not_Valid")
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid slug = %d %s", invalid.Code, invalid.Body.String())
	}
	if !strings.Contains(invalid.Body.String(), `&#34;Not_Valid&#34; is not a valid note slug`) {
		t.Fatalf("invalid copy: %s", invalid.Body.String())
	}

	ins := do(s, "/scope/wc/notes/pad")
	if ins.Code != http.StatusOK {
		t.Fatalf("inspect = %d %s", ins.Code, ins.Body.String())
	}
	ib := ins.Body.String()
	if !strings.Contains(ib, "<strong>world</strong>") {
		t.Errorf("goldmark: %s", ib)
	}
	if strings.Contains(ib, `action="/scope/wc/mark"`) || strings.Contains(ib, `action="/scope/wc/claim"`) {
		t.Fatalf("note inspect offered ticket writes: %s", ib)
	}
	if !strings.Contains(ib, `name="base" value="`) {
		t.Fatalf("missing clobber base: %s", ib)
	}
	if !strings.Contains(ib, `onsubmit="if(this.dataset.submitted)return false;`) {
		t.Fatalf("save should ignore a second submit: %s", ib)
	}
	if !strings.Contains(ib, `name="slug" value="pad"`) || !strings.Contains(ib, `name="return" value="inspect"`) {
		t.Fatalf("non-default inspect should offer use and stay on inspect: %s", ib)
	}
	if !strings.Contains(ib, `action="/scope/wc/notes/pad/delete"`) {
		t.Fatalf("inspect should offer delete: %s", ib)
	}
	if !strings.Contains(ib, `onsubmit="return confirm('Delete pad?')"`) {
		t.Fatalf("delete should name the slug: %s", ib)
	}
	defIns := do(s, "/scope/wc/notes/default").Body.String()
	if strings.Contains(defIns, `name="slug" value="default"`) || strings.Contains(defIns, `name="clear" value="1"`) {
		t.Fatalf("built-in default inspect should not offer use or clear: %s", defIns)
	}
	if strings.Contains(defIns, "/delete") {
		t.Fatalf("missing inspect should not offer delete: %s", defIns)
	}
}

func TestGETNotesDoesNotWrite(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	path := writeNote(t, dir, "pad", "keep\n")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := mustServer(t, app)
	for _, p := range []string{
		"/scope/wc/notes",
		"/scope/wc/notes/pad",
		"/scope/wc/notes/default",
		"/scope/wc/notes/use",
		"/scope/wc/notes/pad/delete",
	} {
		w := do(s, p)
		if w.Code == http.StatusSeeOther {
			t.Fatalf("GET %s redirected as a write: %s", p, w.Header().Get("Location"))
		}
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(before) {
		t.Fatalf("GET mutated note: %q -> %q", before, got)
	}
	if _, err := os.Stat(filepath.Join(dir, scopefile.NoteDir, "default.md")); !os.IsNotExist(err) {
		t.Fatal("GET default created a file")
	}
}

func TestGETNoteEditFormMatchesDisk(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	body := "\nhello **world**\n"
	writeNote(t, dir, "pad", body)
	s := mustServer(t, app)
	ins := do(s, "/scope/wc/notes/pad")
	if ins.Code != http.StatusOK {
		t.Fatalf("inspect = %d %s", ins.Code, ins.Body.String())
	}
	page := ins.Body.String()
	if textareaBrowserValue(t, page) != body {
		t.Fatalf("body field = %q, want %q", textareaBrowserValue(t, page), body)
	}

	w := doPost(s, "/scope/wc/notes/pad", url.Values{
		"body": {textareaBrowserValue(t, page)},
		"base": {inspectBase(t, page)},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("want 303, got %d %s", w.Code, w.Body.String())
	}
	if readNote(t, dir, "pad") != body {
		t.Fatalf("save dropped leading blank line: %q", readNote(t, dir, "pad"))
	}
}

func TestPOSTNoteSetRoundTrip(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	writeNote(t, dir, "pad", "hello\n")
	s := mustServer(t, app)
	base := inspectBase(t, do(s, "/scope/wc/notes/pad").Body.String())

	w := doPost(s, "/scope/wc/notes/pad", url.Values{
		"body": {"replaced **md**\n"},
		"base": {base},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("want 303, got %d %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, noteHref("wc", "pad")) {
		t.Fatalf("location = %s", loc)
	}
	page := mustFollow(t, s, w)
	if !strings.Contains(page.Body.String(), "<strong>md</strong>") {
		t.Fatalf("inspect missing goldmark: %s", page.Body.String())
	}
	if readNote(t, dir, "pad") != "replaced **md**\n" {
		t.Fatalf("disk = %q", readNote(t, dir, "pad"))
	}
}

func TestPOSTNoteSetCRLFStoredAsLF(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	writeNote(t, dir, "pad", "hello\n")
	s := mustServer(t, app)
	base := inspectBase(t, do(s, "/scope/wc/notes/pad").Body.String())

	w := doPost(s, "/scope/wc/notes/pad", url.Values{
		"body": {"a\r\nb\rc\n"},
		"base": {base},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("want 303, got %d %s", w.Code, w.Body.String())
	}
	got := readNote(t, dir, "pad")
	if strings.Contains(got, "\r") {
		t.Fatalf("CR on disk: %q", got)
	}
	if got != "a\nb\nc\n" {
		t.Fatalf("body = %q", got)
	}

	base = inspectBase(t, do(s, "/scope/wc/notes/pad").Body.String())
	w = doPost(s, "/scope/wc/notes/pad", url.Values{
		"body": {"a\nb\nc\n"},
		"base": {base},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("LF no-op: %d %s", w.Code, w.Body.String())
	}
	got = readNote(t, dir, "pad")
	if strings.Contains(got, "\r") {
		t.Fatalf("LF no-op introduced CR: %q", got)
	}
	if got != "a\nb\nc\n" {
		t.Fatalf("LF no-op body = %q", got)
	}
}

func TestPOSTNoteSetEmptyIs400(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	writeNote(t, dir, "pad", "keep\n")
	s := mustServer(t, app)
	base := inspectBase(t, do(s, "/scope/wc/notes/pad").Body.String())
	w := doPost(s, "/scope/wc/notes/pad", url.Values{"body": {""}, "base": {base}})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d %s", w.Code, w.Body.String())
	}
	if readNote(t, dir, "pad") != "keep\n" {
		t.Fatal("empty set wrote")
	}
}

func TestPOSTNoteSetMissingBaseIs400(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	writeNote(t, dir, "pad", "keep\n")
	s := mustServer(t, app)
	w := doPost(s, "/scope/wc/notes/pad", url.Values{"body": {"clobber\n"}})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "set needs a clobber predicate") {
		t.Fatalf("missing-base copy: %s", w.Body.String())
	}
	if readNote(t, dir, "pad") != "keep\n" {
		t.Fatal("set without base wrote")
	}
}

func TestPOSTNoteCreateAndDeleteAndUse(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	s := mustServer(t, app)

	create := doPost(s, "/scope/wc/notes", url.Values{"slug": {"grant"}})
	if create.Code != http.StatusSeeOther {
		t.Fatalf("create: %d %s", create.Code, create.Body.String())
	}
	if loc := create.Header().Get("Location"); loc != noteHref("wc", "grant") {
		t.Fatalf("create loc = %s", loc)
	}
	if _, err := os.Stat(filepath.Join(dir, scopefile.NoteDir, "grant.md")); !os.IsNotExist(err) {
		t.Fatal("create must not write the file")
	}
	page := mustFollow(t, s, create)
	if !strings.Contains(page.Body.String(), "no file") {
		t.Errorf("missing file copy: %s", page.Body.String())
	}
	if strings.Contains(page.Body.String(), "/delete") {
		t.Fatalf("missing inspect should not offer delete: %s", page.Body.String())
	}
	base := inspectBase(t, page.Body.String())
	if base != notes.MissingClobberKey {
		t.Fatalf("missing base = %q", base)
	}

	reserved := doPost(s, "/scope/wc/notes", url.Values{"slug": {"list"}})
	if reserved.Code != http.StatusBadRequest {
		t.Fatalf("reserved create = %d", reserved.Code)
	}
	if !strings.Contains(reserved.Body.String(), `&#34;list&#34; is a reserved note name`) {
		t.Fatalf("reserved create copy: %s", reserved.Body.String())
	}
	bogus := doPost(s, "/scope/wc/notes", url.Values{"slug": {"Not_Valid"}})
	if bogus.Code != http.StatusBadRequest {
		t.Fatalf("invalid create = %d", bogus.Code)
	}
	if !strings.Contains(bogus.Body.String(), `&#34;Not_Valid&#34; is not a valid note slug`) {
		t.Fatalf("invalid create copy: %s", bogus.Body.String())
	}

	w := doPost(s, "/scope/wc/notes/grant", url.Values{
		"body": {"grant pad\n"},
		"base": {base},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("set: %d %s", w.Code, w.Body.String())
	}
	if readNote(t, dir, "grant") != "grant pad\n" {
		t.Fatalf("disk = %q", readNote(t, dir, "grant"))
	}

	use := doPost(s, "/scope/wc/notes/use", url.Values{"slug": {"grant"}})
	if use.Code != http.StatusSeeOther {
		t.Fatalf("use: %d %s", use.Code, use.Body.String())
	}
	if loc := use.Header().Get("Location"); strings.Contains(loc, "sync_needed=") {
		t.Fatalf("use emitted sync_needed: %s", loc)
	}
	list := mustFollow(t, s, use)
	lb := list.Body.String()
	if !strings.Contains(lb, `class="default"`) || !strings.Contains(lb, ">grant</a>") {
		t.Fatalf("default row: %s", lb)
	}
	grantIns := do(s, "/scope/wc/notes/grant").Body.String()
	if !strings.Contains(grantIns, `name="clear" value="1"`) || !strings.Contains(grantIns, `name="return" value="inspect"`) {
		t.Fatalf("stored-default inspect should offer clear and stay on inspect: %s", grantIns)
	}

	del := doPost(s, "/scope/wc/notes/grant/delete", nil)
	if del.Code != http.StatusSeeOther {
		t.Fatalf("delete: %d %s", del.Code, del.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, scopefile.NoteDir, "grant.md")); !os.IsNotExist(err) {
		t.Fatal("delete left the file")
	}
	after := do(s, "/scope/wc/notes")
	if after.Code != http.StatusOK {
		t.Fatalf("list after delete = %d", after.Code)
	}
	ab := after.Body.String()
	if !strings.Contains(ab, ">grant</a>") || !strings.Contains(ab, "(no file)") {
		t.Fatalf("default after delete: %s", ab)
	}
	if strings.Contains(ab, `class="default"`) {
		t.Fatalf("missing file should not mark a list row: %s", ab)
	}
	if !strings.Contains(ab, `name="clear" value="1"`) {
		t.Fatalf("stored default should offer clear: %s", ab)
	}

	clr := doPost(s, "/scope/wc/notes/use", url.Values{"clear": {"1"}})
	if clr.Code != http.StatusSeeOther {
		t.Fatalf("clear: %d %s", clr.Code, clr.Body.String())
	}
	if loc := clr.Header().Get("Location"); strings.Contains(loc, "sync_needed=") {
		t.Fatalf("clear emitted sync_needed: %s", loc)
	}
	cleared := mustFollow(t, s, clr)
	cb := cleared.Body.String()
	if !strings.Contains(cb, ">default</a>") || !strings.Contains(cb, "(no file)") {
		t.Fatalf("built-in default after clear: %s", cb)
	}
	if strings.Contains(cb, ">grant</a>") {
		t.Fatalf("cleared pointer still names grant: %s", cb)
	}
	if strings.Contains(cb, `name="clear" value="1"`) {
		t.Fatalf("built-in default still offers clear: %s", cb)
	}

	miss := doPost(s, "/scope/wc/notes/ghost/delete", nil)
	if miss.Code != http.StatusSeeOther {
		t.Fatalf("delete missing: %d %s", miss.Code, miss.Body.String())
	}
}

func TestPOSTNoteUseReturnInspect(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	writeNote(t, dir, "pad", "hello\n")
	s := mustServer(t, app)

	use := doPost(s, "/scope/wc/notes/use", url.Values{"slug": {"pad"}, "return": {"inspect"}})
	if use.Code != http.StatusSeeOther {
		t.Fatalf("use: %d %s", use.Code, use.Body.String())
	}
	if loc := use.Header().Get("Location"); loc != noteHref("wc", "pad") {
		t.Fatalf("use loc = %s", loc)
	}
	page := mustFollow(t, s, use)
	if !strings.Contains(page.Body.String(), `name="clear" value="1"`) {
		t.Fatalf("inspect after use should offer clear: %s", page.Body.String())
	}

	clr := doPost(s, "/scope/wc/notes/use", url.Values{"slug": {"pad"}, "clear": {"1"}, "return": {"inspect"}})
	if clr.Code != http.StatusSeeOther {
		t.Fatalf("clear: %d %s", clr.Code, clr.Body.String())
	}
	if loc := clr.Header().Get("Location"); loc != noteHref("wc", "pad") {
		t.Fatalf("clear loc = %s", loc)
	}

	list := doPost(s, "/scope/wc/notes/use", url.Values{"slug": {"pad"}})
	if list.Code != http.StatusSeeOther {
		t.Fatalf("list use: %d %s", list.Code, list.Body.String())
	}
	if loc := list.Header().Get("Location"); loc != notesListHref("wc") {
		t.Fatalf("list use loc = %s", loc)
	}

	bogus := doPost(s, "/scope/wc/notes/use", url.Values{"slug": {"pad"}, "return": {"https://evil.example/"}})
	if bogus.Code != http.StatusSeeOther {
		t.Fatalf("foreign return: %d %s", bogus.Code, bogus.Body.String())
	}
	if loc := bogus.Header().Get("Location"); loc != notesListHref("wc") {
		t.Fatalf("foreign return loc = %s", loc)
	}
}

func TestPOSTNoteNeverSelfCommits(t *testing.T) {
	app := newTestApp(t)
	dir, repo := initDrivenScope(t, app, "wc")
	writeNote(t, dir, "pad", "hello\n")
	pushOrigin(t, repo)
	before := testgit.Combined(t, repo, "rev-parse", "HEAD")
	s := mustServer(t, app)
	base := inspectBase(t, do(s, "/scope/wc/notes/pad").Body.String())

	w := doPost(s, "/scope/wc/notes/pad", url.Values{
		"body": {"edited\n"},
		"base": {base},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("want 303, got %d %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "sync_needed=") {
		t.Fatalf("location missing sync_needed: %s", loc)
	}
	after := testgit.Combined(t, repo, "rev-parse", "HEAD")
	if after != before {
		t.Fatalf("note save self-committed: %s -> %s", before, after)
	}
	page := do(s, loc)
	if !strings.Contains(page.Body.String(), "sync_needed:") {
		t.Fatalf("banner: %s", page.Body.String())
	}

	use := doPost(s, "/scope/wc/notes/use", url.Values{"slug": {"pad"}})
	if use.Code != http.StatusSeeOther {
		t.Fatalf("use: %d %s", use.Code, use.Body.String())
	}
	if strings.Contains(use.Header().Get("Location"), "sync_needed=") {
		t.Fatalf("use must not emit sync_needed: %s", use.Header().Get("Location"))
	}
	if testgit.Combined(t, repo, "rev-parse", "HEAD") != before {
		t.Fatal("use self-committed")
	}
}

func TestPOSTNoteRefusesMidRebase(t *testing.T) {
	app := newTestApp(t)
	dir, repo := initDrivenScope(t, app, "wc")
	writeNote(t, dir, "pad", "keep\n")
	if err := os.MkdirAll(filepath.Join(repo, ".git", "rebase-merge"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := mustServer(t, app)

	ins := do(s, "/scope/wc/notes/pad")
	if ins.Code != http.StatusOK {
		t.Fatalf("inspect mid-rebase = %d %s", ins.Code, ins.Body.String())
	}
	base := inspectBase(t, ins.Body.String())

	w := doPost(s, "/scope/wc/notes/pad", url.Values{
		"body": {"clobber\n"},
		"base": {base},
	})
	if w.Code != http.StatusConflict {
		t.Fatalf("set: want 409, got %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "mid-sync-conflict") {
		t.Fatalf("set message: %s", w.Body.String())
	}

	del := doPost(s, "/scope/wc/notes/pad/delete", nil)
	if del.Code != http.StatusConflict {
		t.Fatalf("delete: want 409, got %d %s", del.Code, del.Body.String())
	}
	if !strings.Contains(del.Body.String(), "mid-sync-conflict") {
		t.Fatalf("delete message: %s", del.Body.String())
	}
	if readNote(t, dir, "pad") != "keep\n" {
		t.Fatalf("mid-rebase wrote: %s", readNote(t, dir, "pad"))
	}
}

func TestNotesNotTicketsOrSearchHits(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "")
	writeNote(t, dir, "pad", "notefish zebra\n")
	s := mustServer(t, app)

	board := do(s, "/scope/wc").Body.String()
	if strings.Contains(board, "notefish") || strings.Contains(board, ">pad<") {
		t.Fatalf("kanban showed a note: %s", board)
	}

	hits := do(s, "/search?q=notefish")
	if hits.Code != http.StatusOK {
		t.Fatalf("search = %d %s", hits.Code, hits.Body.String())
	}
	if !strings.Contains(hits.Body.String(), "No matches.") {
		t.Fatalf("search hit a note: %s", hits.Body.String())
	}
}

func TestPOSTNoteRefusesForeignOrigin(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	writeNote(t, dir, "pad", "keep\n")
	s := mustServer(t, app)
	base := inspectBase(t, do(s, "/scope/wc/notes/pad").Body.String())
	form := url.Values{"body": {"hacked\n"}, "base": {base}}

	w := doPostHeader(s, "/scope/wc/notes/pad", form, http.Header{"Origin": {"https://evil.example"}})
	if w.Code != http.StatusForbidden {
		t.Fatalf("foreign origin: want 403, got %d %s", w.Code, w.Body.String())
	}
	if readNote(t, dir, "pad") != "keep\n" {
		t.Fatal("foreign origin wrote")
	}
}

func TestGETNoteSymlinkIsNotFollowed(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("notefish-secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, scopefile.NoteDir, "default.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, path); err != nil {
		t.Fatal(err)
	}
	s := mustServer(t, app)

	list := do(s, "/scope/wc/notes").Body.String()
	if strings.Contains(list, ">default</a>") && !strings.Contains(list, "(no file)") {
		t.Fatalf("list treated symlink as a note file: %s", list)
	}
	if strings.Contains(list, "notefish-secret") {
		t.Fatalf("list leaked target: %s", list)
	}

	ins := do(s, "/scope/wc/notes/default")
	if ins.Code != http.StatusConflict {
		t.Fatalf("inspect symlink = %d %s", ins.Code, ins.Body.String())
	}
	if strings.Contains(ins.Body.String(), "notefish-secret") {
		t.Fatalf("inspect followed symlink: %s", ins.Body.String())
	}
}

func TestNoteMarkdownRawHTMLOff(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	writeNote(t, dir, "pad", "**bold**\n\n<script>alert(1)</script>\n")
	s := mustServer(t, app)
	body := do(s, "/scope/wc/notes/pad").Body.String()
	if !strings.Contains(body, "<strong>bold</strong>") {
		t.Fatalf("expected goldmark strong: %s", body)
	}
	if strings.Contains(body, "<script>") || strings.Contains(body, "<script ") {
		t.Fatalf("raw script leaked: %s", body)
	}
	if !strings.Contains(body, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Fatalf("editor must show escaped source, not omit it: %s", body)
	}
}

func TestPOSTNoteSetStaleBaseIs409(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	path := writeNote(t, dir, "pad", "hello\n")
	s := mustServer(t, app)
	base := inspectBase(t, do(s, "/scope/wc/notes/pad").Body.String())
	if err := os.WriteFile(path, []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := doPost(s, "/scope/wc/notes/pad", url.Values{"body": {"clobber\n"}, "base": {base}})
	if w.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d %s", w.Code, w.Body.String())
	}
	if readNote(t, dir, "pad") != "changed\n" {
		t.Fatalf("stale POST wrote: %s", readNote(t, dir, "pad"))
	}
}

func TestPOSTNoteUseAndCreateWithoutIndex(t *testing.T) {
	app := newTestApp(t)
	initScope(t, app, "wc")
	s := mustServer(t, app)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	unknown := doPost(s, "/scope/zz/notes/use", url.Values{"slug": {"grant"}})
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("unknown scope use = %d %s", unknown.Code, unknown.Body.String())
	}

	create := doPost(s, "/scope/wc/notes", url.Values{"slug": {"grant"}})
	if create.Code != http.StatusSeeOther {
		t.Fatalf("create with closed index: %d %s", create.Code, create.Body.String())
	}

	w := doPost(s, "/scope/wc/notes/use", url.Values{"slug": {"grant"}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("use with closed index: %d %s", w.Code, w.Body.String())
	}
	store := registry.NewStore(app.Ctx, app.ConfigDir)
	reg, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if reg.Note["wc"] != "grant" {
		t.Fatalf("note pointer = %q", reg.Note["wc"])
	}

	set := doPost(s, "/scope/wc/notes/grant", url.Values{
		"body": {"x\n"},
		"base": {notes.MissingClobberKey},
	})
	if set.Code != http.StatusInternalServerError {
		t.Fatalf("set with closed index: %d %s", set.Code, set.Body.String())
	}
}
