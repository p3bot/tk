package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/token"
)

func TestDesignRehomePrintsDestAndStaysOffTheBoard(t *testing.T) {
	app := newApp(t)
	foo := initScope(t, app, "foo")
	bar := initScope(t, app, "bar")
	writeDesign(t, foo, "foo-ab2c", "shape", "accepted", "2026-01-01T00:00:00Z", "Shape")
	raw := "---\nid: foo-ab2c\nstatus: accepted\ncreated: 2026-01-01T00:00:00Z\nproduces: [zz-m4np]\n---\n# Shape\n"
	if err := os.WriteFile(filepath.Join(foo, "design", "foo-ab2c-shape.md"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	addTicket(t, bar, "bar-zzzz", "keep", "todo", "a0", "# Keep\n", false, "")

	out, errOut, err := run(t, app, "design", "rehome", "foo-ab2c", "bar")
	if err != nil {
		t.Fatalf("rehome: %v (%s)", err, errOut)
	}
	path := strings.TrimSpace(out)
	if !strings.HasSuffix(path, filepath.Join("bar", "design", "bar-ab2c-shape.md")) {
		t.Fatalf("stdout = %q", out)
	}
	if _, err := os.Stat(filepath.Join(foo, "design", "foo-ab2c-shape.md")); !os.IsNotExist(err) {
		t.Fatalf("source = %v", err)
	}
	moved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(moved), "id: bar-ab2c") || !strings.Contains(string(moved), "produces: [zz-m4np]") || strings.Contains(string(moved), "order:") {
		t.Fatalf("dest fence:\n%s", moved)
	}

	list, _, err := run(t, app, "list", "--scope", "bar", "--all")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(list, "bar-ab2c") || !strings.Contains(list, "bar-zzzz") {
		t.Fatalf("list = %q", list)
	}
	designs, _, err := run(t, app, "design", "list", "--scope", "bar", "--all")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(designs, "bar-ab2c") {
		t.Fatalf("design list = %q", designs)
	}
}

func TestDesignRehomeSameScopeExitsTwo(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "foo")
	writeDesign(t, dir, "foo-ab2c", "shape", "draft", "2026-01-01T00:00:00Z", "Shape")
	_, _, err := run(t, app, "design", "rehome", "foo-ab2c", "foo")
	if got := ExitCodeFromError(err); got != exitUsage {
		t.Fatalf("exit = %d err=%v", got, err)
	}
	if !fileExists(filepath.Join(dir, "design"), "foo-ab2c-shape.md") {
		t.Fatal("source moved")
	}
}

func TestDesignRehomeRefusesSharedShortID(t *testing.T) {
	app := newApp(t)
	foo := initScope(t, app, "foo")
	initScope(t, app, "bar")
	writeDesign(t, foo, "foo-ab2c", "one", "draft", "2026-01-01T00:00:00Z", "One")
	writeDesign(t, foo, "foo-ab2c", "two", "draft", "2026-01-01T00:00:00Z", "Two")
	out, errOut, err := run(t, app, "design", "rehome", "foo-ab2c", "bar")
	if err == nil {
		t.Fatal("shared short id must refuse")
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("stdout = %q", out)
	}
	if !strings.Contains(err.Error(), token.DesignID) && !strings.Contains(errOut, token.DesignID) {
		t.Fatalf("err=%v stderr=%q", err, errOut)
	}
	if !fileExists(filepath.Join(foo, "design"), "foo-ab2c-one.md") || !fileExists(filepath.Join(foo, "design"), "foo-ab2c-two.md") {
		t.Fatal("a source file moved")
	}
}

func TestDesignRehomeDoesNotMoveATicket(t *testing.T) {
	app := newApp(t)
	foo := initScope(t, app, "foo")
	initScope(t, app, "bar")
	addTicket(t, foo, "foo-ab2c", "work", "todo", "a0", "# Work\n", false, "")
	_, _, err := run(t, app, "design", "rehome", "foo-ab2c", "bar")
	if err == nil {
		t.Fatal("a ticket id must not design-rehome")
	}
	if !fileExists(foo, "foo-ab2c-work.md") {
		t.Fatal("ticket moved")
	}
}

func TestTicketRehomeDoesNotMoveADesign(t *testing.T) {
	app := newApp(t)
	foo := initScope(t, app, "foo")
	initScope(t, app, "bar")
	writeDesign(t, foo, "foo-ab2c", "shape", "draft", "2026-01-01T00:00:00Z", "Shape")
	_, _, err := run(t, app, "rehome", "foo-ab2c", "bar")
	if err == nil {
		t.Fatal("ticket rehome must not move a design")
	}
	if !fileExists(filepath.Join(foo, "design"), "foo-ab2c-shape.md") {
		t.Fatal("design moved")
	}
}

func TestDesignRehomeAutoCommitMismatch(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	foo, repo := initGitScope(t, app, "foo", true)
	barDir := filepath.Join(repo, "bar")
	if err := os.MkdirAll(barDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, app, "scope", "init", barDir, "--name", "bar", "--code-root", barDir, "--auto-commit"); err != nil {
		t.Fatalf("init bar: %v", err)
	}
	if err := os.WriteFile(filepath.Join(barDir, "tk.cue"), []byte("name: \"bar\"\nautoCommit: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeDesign(t, foo, "foo-ab2c", "shape", "draft", "2026-01-01T00:00:00Z", "Shape")
	out, errOut, err := run(t, app, "design", "rehome", "foo-ab2c", "bar")
	if err == nil || (!strings.Contains(err.Error(), token.AutoCommitMismatch) && !strings.Contains(errOut, token.AutoCommitMismatch)) {
		t.Fatalf("err=%v stderr=%q stdout=%q", err, errOut, out)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("stdout = %q", out)
	}
	if !fileExists(filepath.Join(foo, "design"), "foo-ab2c-shape.md") {
		t.Fatal("source moved")
	}
}
