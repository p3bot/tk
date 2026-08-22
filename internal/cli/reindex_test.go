package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/token"
)

func assertReindexQuiet(t *testing.T, out, errOut string) {
	t.Helper()
	if out != "" {
		t.Errorf("reindex stdout must be empty, got %q", out)
	}
	if !strings.Contains(errOut, "tk reindex: rebuilt index from files") {
		t.Errorf("reindex stderr must confirm rebuild, got %q", errOut)
	}
	if strings.Contains(errOut, "tk doctor:") {
		t.Errorf("reindex must not print doctor integrity lines, stderr=%q", errOut)
	}
	for _, line := range append(lines(out), lines(errOut)...) {
		if token.HasKnownPrefix(line) {
			t.Errorf("reindex must not emit doctor token %q", line)
		}
	}
}

func pinSameSizeTitle(t *testing.T, path, from, to string) {
	t.Helper()
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

func TestReindexRebuilds(t *testing.T) {
	app := newApp(t)
	t.Setenv("TK_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	path := filepath.Join(dir, "wc-ab2c-x.md")
	addTicket(t, dir, "wc-ab2c", "x", "todo", "a0", "# X\n", false, "")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	out, errOut, err := run(t, app, "reindex")
	if err != nil {
		t.Fatalf("reindex: %v", err)
	}
	assertReindexQuiet(t, out, errOut)
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("reindex must not touch ticket files")
	}

	addTicket(t, dir, "wc-de34", "ghost", "todo", "a1", "# Ghost\n", false, "")
	if _, _, err := run(t, app, "list"); err != nil {
		t.Fatalf("seed index: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, "wc-de34-ghost.md")); err != nil {
		t.Fatal(err)
	}
	out, errOut, err = run(t, app, "reindex")
	if err != nil {
		t.Fatalf("reindex: %v", err)
	}
	assertReindexQuiet(t, out, errOut)
	list, _, err := run(t, app, "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if strings.Contains(list, "wc-de34") {
		t.Errorf("rebuild must drop the row for a removed file, got %q", list)
	}
	if !strings.Contains(list, "wc-ab2c") {
		t.Errorf("rebuild must keep the surviving ticket, got %q", list)
	}
}

func TestReindexRebuildsMtimePreservingCopy(t *testing.T) {
	// Incremental reconcile skips a same-size, same-mtime rewrite; Rebuild must not.
	// TK_SCOPE is wc so a regression that only refills the ambient scope leaves ab stale.
	app := newApp(t)
	t.Setenv("TK_SCOPE", "wc")
	wc := initScope(t, app, "wc")
	ab := initScope(t, app, "ab")
	wcPath := filepath.Join(wc, "wc-ab2c-x.md")
	abPath := filepath.Join(ab, "ab-cd34-y.md")
	addTicket(t, wc, "wc-ab2c", "x", "todo", "a0", "# Alpha\n", false, "")
	addTicket(t, ab, "ab-cd34", "y", "todo", "a0", "# Alpha\n", false, "")

	if _, _, err := run(t, app, "list", "--scope", "wc"); err != nil {
		t.Fatalf("seed wc: %v", err)
	}
	if _, _, err := run(t, app, "list", "--scope", "ab"); err != nil {
		t.Fatalf("seed ab: %v", err)
	}

	pinSameSizeTitle(t, wcPath, "# Alpha\n", "# Omega\n")
	pinSameSizeTitle(t, abPath, "# Alpha\n", "# Omega\n")

	stale, _, err := run(t, app, "list", "--scope", "ab")
	if err != nil {
		t.Fatalf("list ab after pin: %v", err)
	}
	if strings.Contains(stale, "Omega") || !strings.Contains(stale, "Alpha") {
		t.Fatalf("precondition: incremental reconcile must miss the pinned edit, got %q", stale)
	}

	out, errOut, err := run(t, app, "reindex")
	if err != nil {
		t.Fatalf("reindex: %v", err)
	}
	assertReindexQuiet(t, out, errOut)

	// Read the cache directly: tk list would reconcile and hide a Rebuild-without-refill.
	got := scopeTitles(t, app.StateDir)
	for _, scope := range []string{"wc", "ab"} {
		if titles := got[scope]; len(titles) != 1 || titles[0] != "Omega" {
			t.Errorf("reindex must refill %s from the pinned copy, got %v", scope, titles)
		}
	}
}

func scopeTitles(t *testing.T, stateDir string) map[string][]string {
	t.Helper()
	db, err := index.Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	rows, err := db.AllTickets()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	for _, p := range rows {
		out[p.Scope] = append(out[p.Scope], p.Title)
	}
	return out
}

func TestReindexDoesNotReportCollision(t *testing.T) {
	app := newApp(t)
	t.Setenv("TK_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "alpha", "todo", "a0", "# Alpha\n", false, "")
	addTicket(t, dir, "wc-ab2c", "beta", "todo", "a1", "# Beta\n", false, "")

	out, errOut, err := run(t, app, "reindex")
	if err != nil {
		t.Fatalf("reindex: %v", err)
	}
	assertReindexQuiet(t, out, errOut)
	if !fileExists(dir, "wc-ab2c-alpha.md") || !fileExists(dir, "wc-ab2c-beta.md") {
		t.Errorf("reindex must not repair the collision, files=%v", ticketFiles(t, dir))
	}

	dout, _, err := run(t, app, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if !strings.Contains(dout, token.DuplicateID) {
		t.Fatalf("fixture must be a collision doctor would report, got %q", dout)
	}
}

func TestDoctorHelpOmitsReindex(t *testing.T) {
	app := newApp(t)
	out, _, err := run(t, app, "doctor", "--help")
	if err != nil {
		t.Fatalf("doctor --help: %v", err)
	}
	if strings.Contains(out, "--reindex") {
		t.Errorf("doctor --help must not mention --reindex:\n%s", out)
	}
	if !strings.Contains(out, "tk reindex") {
		t.Errorf("doctor --help should point at tk reindex:\n%s", out)
	}
}

func TestSkillListsReindex(t *testing.T) {
	app := newApp(t)
	out, _, err := run(t, app, "skill")
	if err != nil {
		t.Fatalf("skill: %v", err)
	}
	if !strings.Contains(out, "tk reindex") {
		t.Errorf("tk skill must list tk reindex")
	}
	if strings.Contains(out, "tk doctor --reindex") || strings.Contains(out, "[--reindex]") {
		t.Errorf("tk skill must not list --reindex on doctor")
	}
}

func TestEnsureFileExistsHintsReindex(t *testing.T) {
	err := ensureFileExists(&index.Ticket{ID: "wc-ab2c", Path: filepath.Join(t.TempDir(), "gone.md")})
	if err == nil {
		t.Fatal("expected missing-file error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "tk reindex") {
		t.Errorf("hint must name tk reindex, got %q", msg)
	}
	if strings.Contains(msg, "tk doctor --reindex") {
		t.Errorf("hint must not name doctor --reindex, got %q", msg)
	}
}

func TestReindexDoesNotCommit(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	t.Setenv("TK_SCOPE", "wc")
	dir, repo := initGitScope(t, app, "wc", true)
	addTicket(t, dir, "wc-ab2c", "x", "todo", "a0", "# X\n", false, "")
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-m", "seed")
	before := gitLog(t, repo)

	if _, _, err := run(t, app, "reindex"); err != nil {
		t.Fatalf("reindex: %v", err)
	}
	after := gitLog(t, repo)
	if !reflect.DeepEqual(before, after) {
		t.Errorf("reindex must not git-commit: before=%v after=%v", before, after)
	}
}
