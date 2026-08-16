package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/scopefile"
	"github.com/p3bot/tk/internal/token"
)

func writeNoteFile(t *testing.T, dir, name, content string) {
	t.Helper()
	notes := filepath.Join(dir, scopefile.NoteDir)
	if err := os.MkdirAll(notes, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(notes, name+".md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSyncNoteSnapshotSubject(t *testing.T) {
	requireGit(t)
	remote := newBareRemote(t)
	m := cloneMachine(t, remote)
	dir := m.initScopeAutoCommit(t)
	if _, _, err := m.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("initial sync: %v", err)
	}

	writeNoteFile(t, dir, "decisions", "keep this\n")
	if _, _, err := m.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("note sync: %v", err)
	}
	if got := topCommit(t, m.clone); got != "tk: note wc decisions" {
		t.Errorf("lone note snapshot = %q", got)
	}
}

func TestSyncNoteConflictPausesThenResume(t *testing.T) {
	requireGit(t)
	a, b, _ := twoMachines(t)

	writeNoteFile(t, a.scopeDir(), "default", "shared\n")
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A add note: %v", err)
	}
	if _, _, err := b.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("B pull note: %v", err)
	}

	writeNoteFile(t, a.scopeDir(), "default", "a-edit\n")
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A edit note: %v", err)
	}

	writeNoteFile(t, b.scopeDir(), "default", "b-edit\n")
	_, errOut, err := b.sync(t, "--scope", "wc")
	if ExitCodeFromError(err) != exitFailure {
		t.Fatalf("conflicted note must pause non-zero, got %v (stderr %q)", err, errOut)
	}
	if !strings.Contains(errOut, "conflicted note:") {
		t.Errorf("want conflicted note:, got %q", errOut)
	}
	if strings.Contains(errOut, "conflicted .gitignore") {
		t.Errorf("must not say conflicted .gitignore, got %q", errOut)
	}
	if strings.Contains(errOut, "body conflict:") || strings.Contains(errOut, token.StatusConflict) {
		t.Errorf("note must not go through the ticket merge driver, got %q", errOut)
	}

	// Setext underline is ordinary markdown, not a leftover conflict separator.
	writeNoteFile(t, b.scopeDir(), "default", "Decision\n=======\nShip it.\n")
	if _, _, err := b.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("resume after clearing note markers: %v", err)
	}
}

func TestSyncNoteDeleteEditThenAct(t *testing.T) {
	requireGit(t)
	a, b, remote := twoMachines(t)

	writeNoteFile(t, a.scopeDir(), "default", "shared\n")
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A add note: %v", err)
	}
	if _, _, err := b.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("B pull note: %v", err)
	}

	if err := os.Remove(defaultNotePath(a.scopeDir())); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A delete note: %v", err)
	}

	writeNoteFile(t, b.scopeDir(), "default", "b-kept\n")
	_, firstOut, err := b.sync(t, "--scope", "wc")
	if ExitCodeFromError(err) != exitFailure {
		t.Fatalf("note delete/edit must pause non-zero, got %v (stderr %q)", err, firstOut)
	}
	assertConfigDeleteEditHandoff(t, firstOut, "wc/notes/default.md", true)
	if strings.Contains(firstOut, "conflicted note:") {
		t.Errorf("delete/edit must use the generic line, got %q", firstOut)
	}

	if err := os.Remove(defaultNotePath(b.scopeDir())); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("acting on note delete/edit should complete: %v", err)
	}
	if remoteHas(t, remote, "wc/notes/default.md") {
		t.Errorf("deletion should be recorded on the remote")
	}
}

func TestSyncConflictedCueDoesNotSuppressNote(t *testing.T) {
	requireGit(t)
	a, b, _ := twoMachines(t)

	writeNoteFile(t, a.scopeDir(), "default", "shared\n")
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A add note: %v", err)
	}
	if _, _, err := b.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("B pull note: %v", err)
	}

	writeCue(t, a.scopeDir(), "name: \"wc\"\nautoCommit: true\nfields: {a: {type: \"string\"}}\n")
	writeNoteFile(t, a.scopeDir(), "default", "a-note\n")
	setStatusLine(t, mustSeedTicket(t, a.scopeDir()), "in-progress")
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A cue+note: %v", err)
	}

	writeCue(t, b.scopeDir(), "name: \"wc\"\nautoCommit: true\nfields: {b: {type: \"string\"}}\n")
	writeNoteFile(t, b.scopeDir(), "default", "b-note\n")
	pB := mustSeedTicket(t, b.scopeDir())
	setStatusLine(t, pB, "review")
	_, errOut, err := b.sync(t, "--scope", "wc")
	if ExitCodeFromError(err) != exitFailure {
		t.Fatalf("cue+note conflict must pause, got %v (stderr %q)", err, errOut)
	}
	if !strings.Contains(errOut, token.ConfigUnparseable) {
		t.Errorf("conflicted tk.cue should ride config_unparseable, got %q", errOut)
	}
	if !strings.Contains(errOut, "conflicted note:") {
		t.Errorf("conflicted tk.cue must not suppress conflicted note:, got %q", errOut)
	}
	if strings.Contains(errOut, "conflicted .gitignore") {
		t.Errorf("must not say conflicted .gitignore, got %q", errOut)
	}
}
