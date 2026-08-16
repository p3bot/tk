package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/scopefile"
	"github.com/p3bot/tk/internal/token"
)

func defaultNotePath(dir string) string {
	return filepath.Join(dir, scopefile.NoteDir, scopefile.NoteDefaultSlug+".md")
}

func namedNotePath(dir, name string) string {
	return filepath.Join(dir, scopefile.NoteDir, name+".md")
}

func writeEditorScript(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "body")
	if err := os.WriteFile(src, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "ed")
	content := "#!/bin/sh\ncp '" + src + "' \"$1\"\n"
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return script
}

func assertUsageEmpty(t *testing.T, out string, err error) {
	t.Helper()
	if ExitCodeFromError(err) != exitUsage {
		t.Fatalf("exit = %d want %d (err=%v)", ExitCodeFromError(err), exitUsage, err)
	}
	if out != "" {
		t.Errorf("usage must leave stdout empty, got %q", out)
	}
}

func TestNoteMissingCatAndList(t *testing.T) {
	app := newApp(t)
	initScope(t, app, "wc")

	out, _, err := run(t, app, "note", "--scope", "wc")
	if err != nil {
		t.Fatalf("missing cat: %v", err)
	}
	if out != "" {
		t.Errorf("missing cat must be empty stdout, got %q", out)
	}
	out, _, err = run(t, app, "note", "list", "--scope", "wc")
	if err != nil {
		t.Fatalf("missing list: %v", err)
	}
	if out != "" {
		t.Errorf("missing list must be empty stdout, got %q", out)
	}
}

func TestNoteAddAppendDeleteDefault(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")

	out, errOut, err := run(t, app, "note", "add", "first line", "--scope", "wc")
	if err != nil {
		t.Fatalf("add: %v stderr=%q", err, errOut)
	}
	wantPath := defaultNotePath(dir)
	if strings.TrimSpace(out) != wantPath {
		t.Errorf("add path = %q want %q", out, wantPath)
	}
	if strings.Contains(errOut, token.SyncNeeded) {
		t.Errorf("add must not emit sync_needed:, got %q", errOut)
	}

	out, _, err = run(t, app, "note", "--scope", "wc")
	if err != nil {
		t.Fatalf("cat: %v", err)
	}
	if out != "first line\n" {
		t.Errorf("cat after first add = %q", out)
	}

	if _, _, err := run(t, app, "note", "add", "second line", "--scope", "wc"); err != nil {
		t.Fatalf("second add: %v", err)
	}
	out, _, err = run(t, app, "note", "--scope", "wc")
	if err != nil {
		t.Fatalf("cat after second add: %v", err)
	}
	if out != "first line\nsecond line\n" {
		t.Errorf("cat after two adds = %q", out)
	}

	out, errOut, err = run(t, app, "note", "delete", "--name", "default", "--scope", "wc")
	if err != nil {
		t.Fatalf("delete: %v stderr=%q", err, errOut)
	}
	if out != "" {
		t.Errorf("delete must print nothing, got %q", out)
	}
	if strings.Contains(errOut, token.SyncNeeded) {
		t.Errorf("delete must not emit sync_needed:, got %q", errOut)
	}
	out, _, err = run(t, app, "note", "--scope", "wc")
	if err != nil {
		t.Fatalf("cat after delete: %v", err)
	}
	if out != "" {
		t.Errorf("cat after delete must be empty, got %q", out)
	}
	if _, err := os.Stat(wantPath); !os.IsNotExist(err) {
		t.Errorf("default note file should be gone, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, scopefile.NoteDir)); !os.IsNotExist(err) {
		t.Errorf("empty notes/ should be removed, stat err=%v", err)
	}
}

func TestNoteAddGlueAndEmptyCreate(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")

	if err := os.MkdirAll(filepath.Join(dir, scopefile.NoteDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(defaultNotePath(dir), []byte("no-nl"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, app, "note", "add", "next", "--scope", "wc"); err != nil {
		t.Fatalf("glue add: %v", err)
	}
	got, err := os.ReadFile(defaultNotePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "no-nl\nnext\n" {
		t.Errorf("glue write = %q", got)
	}

	if err := os.WriteFile(defaultNotePath(dir), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, app, "note", "add", "text", "--scope", "wc"); err != nil {
		t.Fatalf("zero-byte add: %v", err)
	}
	got, err = os.ReadFile(defaultNotePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "text\n" {
		t.Errorf("zero-byte add must not insert a leading blank line, got %q", got)
	}
}

func TestNoteAddSetDeleteUsage(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")

	out, _, err := run(t, app, "note", "add", "--scope", "wc")
	assertUsageEmpty(t, out, err)
	if _, statErr := os.Stat(filepath.Join(dir, scopefile.NoteDir)); !os.IsNotExist(statErr) {
		t.Error("add with no text must not create notes/")
	}

	out, _, err = run(t, app, "note", "add", "", "--scope", "wc")
	assertUsageEmpty(t, out, err)

	out, _, err = run(t, app, "note", "set", "--scope", "wc")
	assertUsageEmpty(t, out, err)
	if _, statErr := os.Stat(filepath.Join(dir, scopefile.NoteDir)); !os.IsNotExist(statErr) {
		t.Error("set with no text must not create notes/")
	}

	out, _, err = run(t, app, "note", "set", "", "--scope", "wc")
	assertUsageEmpty(t, out, err)

	out, _, err = run(t, app, "note", "delete", "--scope", "wc")
	assertUsageEmpty(t, out, err)
}

func TestNoteNamedCatAndMixUsage(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")

	out, _, err := run(t, app, "note", "add", "--name", "decisions", "hello", "--scope", "wc")
	if err != nil {
		t.Fatalf("named add: %v", err)
	}
	want := namedNotePath(dir, "decisions")
	if strings.TrimSpace(out) != want {
		t.Errorf("named add path = %q want %q", out, want)
	}

	pos, _, err := run(t, app, "note", "decisions", "--scope", "wc")
	if err != nil {
		t.Fatalf("positional cat: %v", err)
	}
	flag, _, err := run(t, app, "note", "--name", "decisions", "--scope", "wc")
	if err != nil {
		t.Fatalf("flag cat: %v", err)
	}
	if pos != "hello\n" || flag != "hello\n" {
		t.Errorf("positional=%q flag=%q", pos, flag)
	}

	out, _, err = run(t, app, "note", "decisions", "--name", "decisions", "--scope", "wc")
	assertUsageEmpty(t, out, err)

	out, _, err = run(t, app, "note", "--name", "default.md", "--scope", "wc")
	assertUsageEmpty(t, out, err)
}

func TestNoteSetReplaceAndStdin(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")

	out, _, err := run(t, app, "note", "set", "first", "--scope", "wc")
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	if strings.TrimSpace(out) != defaultNotePath(dir) {
		t.Errorf("set path = %q", out)
	}
	if _, _, err := run(t, app, "note", "set", "replaced body", "--scope", "wc"); err != nil {
		t.Fatalf("set replace: %v", err)
	}
	got, err := os.ReadFile(defaultNotePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "replaced body\n" {
		t.Errorf("set replace = %q", got)
	}

	out, _, err = runIn(t, app, "from stdin", "note", "set", "-", "--scope", "wc")
	if err != nil {
		t.Fatalf("set stdin: %v", err)
	}
	if strings.TrimSpace(out) != defaultNotePath(dir) {
		t.Errorf("set - path = %q", out)
	}
	got, err = os.ReadFile(defaultNotePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "from stdin\n" {
		t.Errorf("set stdin = %q", got)
	}

	out, _, err = runIn(t, app, "", "note", "set", "-", "--scope", "wc")
	assertUsageEmpty(t, out, err)
}

func TestNoteDeleteMissingIsSuccess(t *testing.T) {
	app := newApp(t)
	initScope(t, app, "wc")

	out, _, err := run(t, app, "note", "delete", "--name", "default", "--scope", "wc")
	if err != nil {
		t.Fatalf("delete missing: %v", err)
	}
	if out != "" {
		t.Errorf("delete missing must be silent, got %q", out)
	}
}

func TestNoteReservedNamesAreUsage(t *testing.T) {
	app := newApp(t)
	initScope(t, app, "wc")

	for _, name := range []string{"list", "add", "set", "edit", "delete", "help"} {
		out, _, err := run(t, app, "note", "--name", name, "--scope", "wc")
		assertUsageEmpty(t, out, err)
	}

	out, _, err := run(t, app, "note", "list", "--scope", "wc")
	if err != nil {
		t.Fatalf("list verb: %v", err)
	}
	if out != "" {
		t.Errorf("list of empty notes = %q", out)
	}
}

func TestNoteListAlphabeticalAndResidue(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")

	if _, _, err := run(t, app, "note", "add", "--name", "zeta", "z", "--scope", "wc"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, app, "note", "add", "--name", "alpha", "a", "--scope", "wc"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, app, "note", "add", "d", "--scope", "wc"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(namedNotePath(dir, "list"), []byte("residue\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(namedNotePath(dir, "Not A Slug"), []byte("bad\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, _, err := run(t, app, "note", "list", "--scope", "wc")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if out != "alpha\ndefault\nzeta\n" {
		t.Errorf("list = %q", out)
	}
}

func TestNoteListScopeFlag(t *testing.T) {
	app := newApp(t)
	initScope(t, app, "wc")
	initScope(t, app, "other")
	if _, _, err := run(t, app, "note", "add", "--name", "only-other", "x", "--scope", "other"); err != nil {
		t.Fatal(err)
	}

	out, _, err := run(t, app, "note", "list", "--scope", "other")
	if err != nil {
		t.Fatalf("list --scope other: %v", err)
	}
	if out != "only-other\n" {
		t.Errorf("list other = %q", out)
	}
	out, _, err = run(t, app, "note", "list", "--scope", "wc")
	if err != nil {
		t.Fatalf("list --scope wc: %v", err)
	}
	if out != "" {
		t.Errorf("list wc should be empty, got %q", out)
	}

	// Flags before the verb bind the child, same as after.
	out, _, err = run(t, app, "note", "--scope", "other", "list")
	if err != nil {
		t.Fatalf("list with --scope before verb: %v", err)
	}
	if out != "only-other\n" {
		t.Errorf("list --scope other before verb = %q", out)
	}
}

func TestNoteEditRefusesWithoutEditor(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "alpha", "todo", "a0", "# Alpha\n", false, "")
	t.Setenv("EDITOR", "")

	out, _, err := run(t, app, "note", "edit", "--scope", "wc")
	if err == nil {
		t.Fatal("edit without EDITOR must fail")
	}
	if out != "" {
		t.Errorf("edit failure must print no stdout, got %q", out)
	}
	if !strings.Contains(err.Error(), "$EDITOR") || !strings.Contains(err.Error(), "tk note edit") {
		t.Errorf("refusal should name tk note edit, got %v", err)
	}

	_, _, ticketErr := run(t, app, "edit", "wc-ab2c")
	if ticketErr == nil {
		t.Fatal("tk edit without EDITOR must fail")
	}
	if !strings.Contains(ticketErr.Error(), "$EDITOR") || !strings.Contains(ticketErr.Error(), "tk edit") {
		t.Errorf("tk edit refusal should name tk edit, got %v", ticketErr)
	}
}

func TestNoteEditWriteAndQuitWithoutWrite(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")

	t.Setenv("EDITOR", writeEditorScript(t, "from editor\n"))
	out, _, err := run(t, app, "note", "edit", "--scope", "wc")
	if err != nil {
		t.Fatalf("edit write: %v", err)
	}
	if strings.TrimSpace(out) != defaultNotePath(dir) {
		t.Errorf("edit path = %q", out)
	}
	got, _, err := run(t, app, "note", "--scope", "wc")
	if err != nil {
		t.Fatalf("cat after edit: %v", err)
	}
	if got != "from editor\n" {
		t.Errorf("cat after edit = %q", got)
	}

	if _, _, err := run(t, app, "note", "delete", "--name", "default", "--scope", "wc"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EDITOR", "true")
	out, _, err = run(t, app, "note", "edit", "--name", "scratch", "--scope", "wc")
	if err != nil {
		t.Fatalf("edit quit: %v", err)
	}
	if strings.TrimSpace(out) != namedNotePath(dir, "scratch") {
		t.Errorf("edit missing path = %q", out)
	}
	if _, err := os.Stat(namedNotePath(dir, "scratch")); !os.IsNotExist(err) {
		t.Errorf("quit without write must leave no file, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, scopefile.NoteDir)); !os.IsNotExist(err) {
		t.Errorf("empty notes/ after quit must be removed, stat err=%v", err)
	}
}

func TestNoteListAndReconcileIgnoreNotes(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "alpha", "todo", "a0", "# Alpha\n", false, "")

	before, _, err := run(t, app, "list", "--scope", "wc")
	if err != nil {
		t.Fatalf("list before: %v", err)
	}
	if _, _, err := run(t, app, "note", "add", "session", "--scope", "wc"); err != nil {
		t.Fatal(err)
	}
	after, _, err := run(t, app, "list", "--scope", "wc")
	if err != nil {
		t.Fatalf("list after: %v", err)
	}
	if before != after {
		t.Errorf("list TSV changed when a note was added:\nbefore=%q\nafter=%q", before, after)
	}
	if !strings.Contains(before, "wc-ab2c") {
		t.Fatalf("expected ticket row, got %q", before)
	}
}

func TestNoteDoctorAllowlist(t *testing.T) {
	app := newApp(t)
	t.Setenv("TK_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	if _, _, err := run(t, app, "note", "add", "ok", "--scope", "wc"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "note.md"), []byte("root\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(namedNotePath(dir, "list"), []byte("verb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(namedNotePath(dir, "Not A Slug"), []byte("bad\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, _, err := run(t, app, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if strings.Contains(out, defaultNotePath(dir)) {
		t.Errorf("allowlisted default note must not be residue, got %q", out)
	}
	if !strings.Contains(out, token.NonAllowlist) {
		t.Errorf("doctor should report residue, got %q", out)
	}
	if !strings.Contains(out, filepath.Join(dir, "note.md")) {
		t.Errorf("root note.md should be residue, got %q", out)
	}
	if !strings.Contains(out, namedNotePath(dir, "list")) {
		t.Errorf("notes/list.md should be residue, got %q", out)
	}
	if !strings.Contains(out, namedNotePath(dir, "Not A Slug")) {
		t.Errorf("invalid slug should be residue, got %q", out)
	}
}

func TestNoteStatusKey(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")

	out, _, err := run(t, app, "status", "note", "--scope", "wc")
	if err != nil {
		t.Fatalf("status note: %v", err)
	}
	if strings.TrimSpace(out) != defaultNotePath(dir) {
		t.Errorf("status note = %q want %q", out, defaultNotePath(dir))
	}
	full, _, err := run(t, app, "status", "--scope", "wc")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if parsePulse(full)["note"] != defaultNotePath(dir) {
		t.Errorf("pulse note = %q", parsePulse(full)["note"])
	}
}

func TestNoteSkillUnchanged(t *testing.T) {
	app := newApp(t)
	out, _, err := run(t, app, "skill")
	if err != nil {
		t.Fatalf("skill: %v", err)
	}
	if strings.Contains(out, "tk note") {
		t.Errorf("skill must not document tk note")
	}
}

func TestNoteUnreachableScope(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	_, errOut, err := run(t, app, "note", "--scope", "wc")
	if err == nil {
		t.Fatal("missing scope dir must fail")
	}
	msg := err.Error() + errOut
	if !strings.Contains(msg, token.UnreachableScope) {
		t.Errorf("want unreachable_scope:, got %q / %v", errOut, err)
	}
}

func TestNoteWritesDoNotSelfCommit(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	dir, repo := initGitScope(t, app, "wc", true)
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-m", "init")
	head := gitIn(t, repo, "rev-parse", "HEAD")

	out, errOut, err := run(t, app, "note", "add", "--name", "decisions", "hello", "--scope", "wc")
	if err != nil {
		t.Fatalf("add: %v stderr=%q", err, errOut)
	}
	if strings.TrimSpace(out) != namedNotePath(dir, "decisions") {
		t.Errorf("add path = %q", out)
	}
	if !strings.Contains(errOut, token.SyncNeeded+" dirty") {
		t.Errorf("tk-driven add should ride sync_needed: dirty, got %q", errOut)
	}
	if got := gitIn(t, repo, "rev-parse", "HEAD"); got != head {
		t.Errorf("add must not commit, HEAD %s -> %s", head, got)
	}

	if _, errOut, err := run(t, app, "note", "set", "--name", "decisions", "replaced", "--scope", "wc"); err != nil {
		t.Fatalf("set: %v", err)
	} else if !strings.Contains(errOut, token.SyncNeeded+" dirty") {
		t.Errorf("tk-driven set should ride sync_needed: dirty, got %q", errOut)
	}
	t.Setenv("EDITOR", "true")
	if _, errOut, err := run(t, app, "note", "edit", "--name", "decisions", "--scope", "wc"); err != nil {
		t.Fatalf("edit: %v", err)
	} else if strings.Contains(errOut, token.SyncNeeded) {
		t.Errorf("edit must not emit sync_needed:, got %q", errOut)
	}
	if _, errOut, err := run(t, app, "note", "delete", "--name", "decisions", "--scope", "wc"); err != nil {
		t.Fatalf("delete: %v", err)
	} else if strings.Contains(errOut, token.SyncNeeded) {
		// Untracked add+delete leaves the tree clean; no hint.
		t.Errorf("delete of an untracked note must not invent sync_needed:, got %q", errOut)
	}
	if got := gitIn(t, repo, "rev-parse", "HEAD"); got != head {
		t.Errorf("writers must not commit, HEAD %s -> %s", head, got)
	}

	if _, _, err := run(t, app, "note", "add", "--name", "decisions", "tracked", "--scope", "wc"); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-m", "track note")
	head = gitIn(t, repo, "rev-parse", "HEAD")
	_, errOut, err = run(t, app, "note", "delete", "--name", "decisions", "--scope", "wc")
	if err != nil {
		t.Fatalf("delete tracked: %v", err)
	}
	if !strings.Contains(errOut, token.SyncNeeded+" dirty") {
		t.Errorf("tk-driven delete of a tracked note should ride sync_needed: dirty, got %q", errOut)
	}
	if got := gitIn(t, repo, "rev-parse", "HEAD"); got != head {
		t.Errorf("delete must not commit, HEAD %s -> %s", head, got)
	}
}

func TestNoteRepoDrivenUncommitted(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	_, repo := initGitScope(t, app, "rd", false)
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-m", "init")

	if _, errOut, err := run(t, app, "note", "add", "hello", "--scope", "rd"); err != nil {
		t.Fatalf("add: %v", err)
	} else if strings.Contains(errOut, token.SyncNeeded) {
		t.Errorf("repo-driven add must not emit sync_needed:, got %q", errOut)
	}
	out, _, err := run(t, app, "status", "uncommitted", "--scope", "rd")
	if err != nil {
		t.Fatalf("status uncommitted: %v", err)
	}
	if strings.TrimSpace(out) == "" || strings.TrimSpace(out) == "0" {
		t.Errorf("repo-driven dirty note should count as uncommitted, got %q", out)
	}

	t.Setenv("TK_SCOPE", "rd")
	doc, _, err := run(t, app, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if !strings.Contains(doc, token.Uncommitted) {
		t.Errorf("doctor should ride uncommitted: for a dirty note, got %q", doc)
	}
}

func TestNoteRefuseNonRegular(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	if err := os.MkdirAll(filepath.Join(dir, scopefile.NoteDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(defaultNotePath(dir), 0o755); err != nil {
		t.Fatal(err)
	}

	out, _, err := run(t, app, "note", "--scope", "wc")
	if err == nil {
		t.Fatal("cat of a directory must fail")
	}
	if out != "" {
		t.Errorf("directory cat must leave stdout empty, got %q", out)
	}
	if !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("directory cat: %v", err)
	}
	if _, _, err := run(t, app, "note", "add", "x", "--scope", "wc"); err == nil {
		t.Fatal("add onto a directory must fail")
	}
	t.Setenv("EDITOR", "true")
	if _, _, err := run(t, app, "note", "edit", "--scope", "wc"); err == nil {
		t.Fatal("edit of a directory must fail")
	}

	if err := os.Remove(defaultNotePath(dir)); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target.md")
	if err := os.WriteFile(target, []byte("via link\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, defaultNotePath(dir)); err != nil {
		t.Fatal(err)
	}
	out, _, err = run(t, app, "note", "--scope", "wc")
	if err == nil {
		t.Fatal("cat of a symlink must fail")
	}
	if out != "" {
		t.Errorf("symlink cat must leave stdout empty, got %q", out)
	}
	if !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("symlink cat: %v", err)
	}
	t.Setenv("EDITOR", "true")
	if _, _, err := run(t, app, "note", "edit", "--scope", "wc"); err == nil {
		t.Fatal("edit of a symlink must fail")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "via link\n" {
		t.Errorf("edit must not write through the symlink, target = %q", got)
	}
}

func TestNoteEditUnlinksZeroByte(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	script := filepath.Join(t.TempDir(), "ed")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n: > \"$1\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EDITOR", script)

	out, _, err := run(t, app, "note", "edit", "--scope", "wc")
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if strings.TrimSpace(out) != defaultNotePath(dir) {
		t.Errorf("edit path = %q", out)
	}
	if _, err := os.Stat(defaultNotePath(dir)); !os.IsNotExist(err) {
		t.Errorf("zero-byte file should be unlinked, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, scopefile.NoteDir)); !os.IsNotExist(err) {
		t.Errorf("empty notes/ should be removed after zero-byte unlink, stat err=%v", err)
	}
}

func TestNoteIgnoresUnparseableCue(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	dir, repo := initGitScope(t, app, "wc", true)
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-m", "init")
	bad := "name: \"wc\"\nautoCommit: false\nfields: {x: {type: \"float\"}}\n"
	if err := os.WriteFile(filepath.Join(dir, "tk.cue"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}

	out, errOut, err := run(t, app, "note", "add", "still works", "--scope", "wc")
	if err != nil {
		t.Fatalf("add against unparseable tk.cue: %v", err)
	}
	if strings.TrimSpace(out) != defaultNotePath(dir) {
		t.Errorf("add path = %q", out)
	}
	if strings.Contains(errOut, token.SyncNeeded) {
		t.Errorf("unusable schema must stay quiet, got %q", errOut)
	}
	got, _, err := run(t, app, "note", "--scope", "wc")
	if err != nil {
		t.Fatalf("cat: %v", err)
	}
	if got != "still works\n" {
		t.Errorf("cat = %q", got)
	}
}

func TestNoteLastDeleteRemovesEmptyDir(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	if _, _, err := run(t, app, "note", "add", "--name", "keep", "x", "--scope", "wc"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, app, "note", "add", "y", "--scope", "wc"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, app, "note", "delete", "--name", "default", "--scope", "wc"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, scopefile.NoteDir)); err != nil {
		t.Fatalf("notes/ should remain while keep.md exists: %v", err)
	}
	if _, _, err := run(t, app, "note", "delete", "--name", "keep", "--scope", "wc"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, scopefile.NoteDir)); !os.IsNotExist(err) {
		t.Errorf("last delete should rmdir notes/, stat err=%v", err)
	}
}

func TestNoteCleanupDoesNotUnlinkNotesSymlink(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	store := t.TempDir()
	if err := os.WriteFile(filepath.Join(store, "decisions.md"), []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	notes := filepath.Join(dir, scopefile.NoteDir)
	if err := os.Symlink(store, notes); err != nil {
		t.Fatal(err)
	}

	t.Setenv("EDITOR", "true")
	if _, _, err := run(t, app, "note", "edit", "--name", "scratch", "--scope", "wc"); err != nil {
		t.Fatalf("edit quit: %v", err)
	}
	st, err := os.Lstat(notes)
	if err != nil {
		t.Fatalf("notes symlink should remain after edit quit: %v", err)
	}
	if st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("notes should still be a symlink, mode=%v", st.Mode())
	}
	if _, err := os.Stat(filepath.Join(store, "decisions.md")); err != nil {
		t.Fatalf("store file should remain: %v", err)
	}

	if _, _, err := run(t, app, "note", "delete", "--name", "default", "--scope", "wc"); err != nil {
		t.Fatalf("delete missing: %v", err)
	}
	if _, err := os.Lstat(notes); err != nil {
		t.Fatalf("notes symlink should remain after delete of a missing default: %v", err)
	}
}
