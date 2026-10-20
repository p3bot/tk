package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/token"
	"github.com/p3bot/tk/internal/writeengine"
)

func TestScopeHoldReadSetAndWarn(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	t.Chdir(dir)

	out, errOut, err := run(t, app, "scope", "hold")
	if err != nil {
		t.Fatalf("read: %v (%s)", err, errOut)
	}
	if strings.TrimSpace(out) != "false" {
		t.Fatalf("omitted hold = %q", out)
	}
	if errOut != "" {
		t.Fatalf("false hold must not warn, got %q", errOut)
	}

	out, errOut, err = run(t, app, "scope", "hold", "true")
	if err != nil {
		t.Fatalf("set true: %v (%s)", err, errOut)
	}
	if !strings.HasSuffix(strings.TrimSpace(out), "tk.cue") {
		t.Fatalf("set should print tk.cue, got %q", out)
	}
	if strings.Contains(errOut, token.ScopeHold) {
		t.Fatalf("setting hold true from false must not warn yet, got %q", errOut)
	}

	out, errOut, err = run(t, app, "scope", "hold")
	if err != nil {
		t.Fatalf("read true: %v", err)
	}
	if strings.TrimSpace(out) != "true" {
		t.Fatalf("hold = %q", out)
	}
	if !strings.Contains(errOut, token.FormatScopeHold("")) {
		t.Fatalf("read of a held scope must warn, got %q", errOut)
	}
	if strings.Contains(errOut, "wc:") {
		t.Fatalf("single-scope warn must not name the scope, got %q", errOut)
	}

	_, errOut, err = run(t, app, "scope", "hold", "true")
	if err != nil {
		t.Fatalf("ensure: %v (%s)", err, errOut)
	}
	if !strings.Contains(errOut, "hold is already true") {
		t.Fatalf("ensure stderr = %q", errOut)
	}

	list, _, err := run(t, app, "scope", "list")
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Split(strings.TrimRight(list, "\n"), "\t")
	if len(fields) != 5 || fields[4] != "true" {
		t.Fatalf("list hold column = %q", list)
	}

	pulse, pulseErr, err := run(t, app, "pulse", "hold")
	if err != nil {
		t.Fatalf("pulse hold: %v (%s)", err, pulseErr)
	}
	if strings.TrimSpace(pulse) != "true" {
		t.Fatalf("pulse hold = %q", pulse)
	}
	if !strings.Contains(pulseErr, token.ScopeHold) {
		t.Fatalf("pulse must warn, got %q", pulseErr)
	}
}

func TestHeldScopeBlocksInProgressAndClaim(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "")
	t.Chdir(dir)
	if _, _, err := run(t, app, "scope", "hold", "true"); err != nil {
		t.Fatal(err)
	}

	out, errOut, err := run(t, app, "next")
	if err != nil {
		t.Fatalf("next read: %v (%s)", err, errOut)
	}
	if !strings.Contains(out, "wc-ab2c") {
		t.Fatalf("next should still print a path, got %q", out)
	}
	if !strings.Contains(errOut, token.FormatScopeHold("")) {
		t.Fatalf("next stderr = %q", errOut)
	}

	out, errOut, err = run(t, app, "next", "--claim")
	if ExitCodeFromError(err) != exitFailure {
		t.Fatalf("claim exit = %d want 1 (%v)", ExitCodeFromError(err), err)
	}
	if out != "" {
		t.Fatalf("claim must not print a path, got %q", out)
	}
	if strings.Count(errOut, token.ScopeHold) != 1 {
		t.Fatalf("claim should warn once, got %q", errOut)
	}
	body, _ := os.ReadFile(filepath.Join(dir, "wc-ab2c-work.md"))
	if !strings.Contains(string(body), "status: todo") {
		t.Fatalf("claim wrote the ticket: %s", body)
	}

	out, errOut, err = run(t, app, "mark", "in-progress", "wc-ab2c")
	if ExitCodeFromError(err) != exitFailure {
		t.Fatalf("mark in-progress exit = %d want 1 (%v)", ExitCodeFromError(err), err)
	}
	if out != "" {
		t.Fatalf("mark in-progress must not print a path, got %q", out)
	}
	if strings.Count(errOut, token.ScopeHold) != 1 {
		t.Fatalf("mark in-progress should warn once, got %q", errOut)
	}
	body, _ = os.ReadFile(filepath.Join(dir, "wc-ab2c-work.md"))
	if !strings.Contains(string(body), "status: todo") {
		t.Fatalf("mark in-progress wrote the ticket: %s", body)
	}

	out, errOut, err = run(t, app, "mark", "done", "ab2c")
	if err != nil {
		t.Fatalf("mark done: %v (%s)", err, errOut)
	}
	if !strings.Contains(errOut, token.ScopeHold) {
		t.Fatalf("mark done must still warn, got %q", errOut)
	}
	if !strings.Contains(out, "archive") {
		t.Fatalf("mark done path = %q", out)
	}
}

func TestHeldScopeNamesEachScopeOnDoctor(t *testing.T) {
	app := newApp(t)
	held := initScope(t, app, "aa")
	other := initScope(t, app, "bb")
	if err := os.WriteFile(filepath.Join(held, "tk.cue"), []byte("name: \"aa\"\nautoCommit: false\nhold: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())

	_, errOut, err := run(t, app, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v (%s)", err, errOut)
	}
	if !strings.Contains(errOut, token.FormatScopeHold("aa")) {
		t.Fatalf("doctor should name the held scope, got %q", errOut)
	}
	if strings.Contains(errOut, token.FormatScopeHold("bb")) || strings.Contains(errOut, "bb: "+token.ScopeHoldSentence) {
		t.Fatalf("doctor warned the open scope: %q", errOut)
	}
	_ = other
}

func TestHeldScopesShareOneLine(t *testing.T) {
	app := newApp(t)
	aa := initScope(t, app, "aa")
	bb := initScope(t, app, "bb")
	for _, spec := range []struct{ dir, name string }{{aa, "aa"}, {bb, "bb"}} {
		body := "name: \"" + spec.name + "\"\nautoCommit: false\nhold: true\n"
		if err := os.WriteFile(filepath.Join(spec.dir, "tk.cue"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(t.TempDir())

	_, errOut, err := run(t, app, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v (%s)", err, errOut)
	}
	want := token.FormatScopeHolds([]string{"bb", "aa"})
	if strings.Count(errOut, token.ScopeHold) != 1 || !strings.Contains(errOut, want) {
		t.Fatalf("doctor stderr = %q, want one %q", errOut, want)
	}
	if strings.Contains(errOut, token.ScopeHoldSentence) {
		t.Fatalf("plural line repeated the singular sentence: %q", errOut)
	}
}

func TestScopeHoldAutoCommitDurability(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	_, repo := initGitScope(t, app, "wc", true)

	_, errOut, err := run(t, app, "scope", "hold", "true", "--scope", "wc")
	if err != nil {
		t.Fatalf("set: %v (%s)", err, errOut)
	}
	log := gitLog(t, repo)
	if len(log) != 1 || log[0] != "tk: scope hold true" {
		t.Fatalf("hold set should self-commit once, got %v", log)
	}
}

func TestImportAndRebindWarnHeldDir(t *testing.T) {
	app := newApp(t)
	held := filepath.Join(t.TempDir(), "held")
	if err := os.MkdirAll(held, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(held, "tk.cue"), []byte("name: \"wc\"\nautoCommit: false\nhold: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, errOut, err := run(t, app, "scope", "import", held)
	if err != nil {
		t.Fatalf("import: %v (%s)", err, errOut)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatal("import printed no path")
	}
	if errOut != token.FormatScopeHold("")+"\n" {
		t.Fatalf("import stderr = %q", errOut)
	}

	plain := filepath.Join(t.TempDir(), "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plain, "tk.cue"), []byte("name: \"ok\"\nautoCommit: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, errOut, err = run(t, app, "scope", "import", plain)
	if err != nil {
		t.Fatalf("import plain: %v (%s)", err, errOut)
	}
	if strings.Contains(errOut, token.ScopeHold) {
		t.Fatalf("plain import warned: %q", errOut)
	}

	app = newApp(t)
	old := initScope(t, app, "wc")
	next := filepath.Join(t.TempDir(), "next")
	if err := os.MkdirAll(next, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(next, "tk.cue"), []byte("name: \"wc\"\nautoCommit: false\nhold: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, errOut, err = run(t, app, "scope", "rebind", next, "--name", "wc")
	if err != nil {
		t.Fatalf("rebind onto held dir: %v (%s)", err, errOut)
	}
	if errOut != token.FormatScopeHold("")+"\n" {
		t.Fatalf("rebind stderr = %q", errOut)
	}
	_ = old

	app = newApp(t)
	old = initScope(t, app, "wc")
	if err := os.WriteFile(filepath.Join(old, "tk.cue"), []byte("name: \"wc\"\nautoCommit: false\nhold: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	next = filepath.Join(t.TempDir(), "next")
	if err := os.MkdirAll(next, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(next, "tk.cue"), []byte("name: \"wc\"\nautoCommit: false\nhold: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, errOut, err = run(t, app, "scope", "rebind", next, "--name", "wc")
	if err != nil {
		t.Fatalf("rebind both held: %v (%s)", err, errOut)
	}
	if strings.Count(errOut, token.ScopeHold) != 1 || strings.Contains(errOut, "wc: "+token.ScopeHoldSentence) {
		t.Fatalf("rebind both held stderr = %q", errOut)
	}
}

func TestLateHoldRefusalPrintsTheLine(t *testing.T) {
	e := &engine{}
	late := mapWriteErr(holdExit(e, "wc", &writeengine.HoldError{}))
	var buf bytes.Buffer
	fprintError(&buf, late, true)
	if strings.TrimSpace(buf.String()) != token.FormatScopeHold("") {
		t.Fatalf("late hold stderr = %q", buf.String())
	}
	if ExitCodeFromError(late) != exitFailure {
		t.Fatalf("late hold exit = %d", ExitCodeFromError(late))
	}

	e.holdSeen = map[string]bool{"wc": true}
	warned := mapWriteErr(holdExit(e, "wc", &writeengine.HoldError{}))
	buf.Reset()
	fprintError(&buf, warned, true)
	if buf.Len() != 0 {
		t.Fatalf("hold already warned must stay quiet, got %q", buf.String())
	}
	if ExitCodeFromError(warned) != exitFailure {
		t.Fatalf("warned hold exit = %d", ExitCodeFromError(warned))
	}
	var hold *writeengine.HoldError
	if !errors.As(warned, &hold) || !errors.As(late, &hold) {
		t.Fatal("hold refusal must still be a hold error")
	}
}
