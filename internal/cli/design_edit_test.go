package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/token"
)

func recordEditor(t *testing.T, extra string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	record := filepath.Join(dir, "args")
	script := filepath.Join(dir, "ed")
	content := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + record + "'\n"
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	if extra != "" {
		return script + " " + extra, record
	}
	return script, record
}

func recordedArgs(t *testing.T, record string) []string {
	t.Helper()
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("editor did not run: %v", err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

func TestDesignEditOpensPathAndRefusesSharedID(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	t.Setenv("TK_SCOPE", "wc")
	writeDesign(t, dir, "wc-ab2c", "shape", "draft", "2026-01-01T00:00:00Z", "Shape")
	path := filepath.Join(dir, "design", "wc-ab2c-shape.md")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	editor, record := recordEditor(t, "--wait")
	t.Setenv("EDITOR", editor)
	out, _, err := run(t, app, "design", "edit", "ab2c")
	if err != nil {
		t.Fatalf("design edit: %v", err)
	}
	if out != "" {
		t.Fatalf("design edit stdout = %q", out)
	}
	args := recordedArgs(t, record)
	if len(args) != 2 || args[0] != "--wait" || args[1] != path {
		t.Fatalf("editor args = %#v", args)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("design edit must not rewrite the file")
	}

	t.Setenv("EDITOR", "")
	out, _, err = run(t, app, "design", "edit", "wc-ab2c")
	if err == nil || out != "" {
		t.Fatalf("unset EDITOR out %q err %v", out, err)
	}
	if !strings.Contains(err.Error(), "$EDITOR") || !strings.Contains(err.Error(), "tk design edit") {
		t.Fatalf("unset EDITOR = %v", err)
	}

	t.Setenv("EDITOR", "false")
	out, _, err = run(t, app, "design", "edit", "ab2c")
	if err == nil || out != "" || !strings.Contains(err.Error(), "editor exited with an error") {
		t.Fatalf("failing editor out %q err %v", out, err)
	}

	writeDesign(t, dir, "wc-ab2c", "other", "draft", "2026-01-02T00:00:00Z", "Other")
	out, _, err = run(t, app, "design", "edit", "ab2c")
	if err == nil || out != "" {
		t.Fatalf("shared edit out %q err %v", out, err)
	}
	if !strings.Contains(err.Error(), token.DesignID) || strings.Contains(err.Error(), "editor exited") {
		t.Fatalf("shared edit = %v", err)
	}
}

func TestDesignEditOpensUnparseable(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	t.Setenv("TK_SCOPE", "wc")
	broken := filepath.Join(dir, "design", "wc-de34-broken.md")
	if err := os.MkdirAll(filepath.Dir(broken), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(broken, []byte("---\nid: [\n---\n# Broken\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	editor, record := recordEditor(t, "")
	t.Setenv("EDITOR", editor)
	out, errOut, err := run(t, app, "design", "edit", "de34")
	if err != nil {
		t.Fatalf("unparseable edit: %v", err)
	}
	if out != "" {
		t.Fatalf("stdout = %q", out)
	}
	args := recordedArgs(t, record)
	if len(args) != 1 || args[0] != broken {
		t.Fatalf("editor args = %#v", args)
	}
	if !strings.Contains(errOut, "parse_error: wc-de34:") {
		t.Fatalf("stderr = %q", errOut)
	}
}

func TestDesignCreateEdit(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")

	t.Run("help documents --edit", func(t *testing.T) {
		out, _, err := run(t, app, "design", "create", "--help")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "--edit") || !strings.Contains(out, "$EDITOR") {
			t.Fatalf("create help = %q", out)
		}
		out, _, err = run(t, app, "design", "edit", "--help")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "$EDITOR") {
			t.Fatalf("edit help = %q", out)
		}
	})

	t.Run("omitted --edit does not launch editor", func(t *testing.T) {
		t.Setenv("EDITOR", "false")
		out, _, err := run(t, app, "design", "create", "No editor", "--scope", "wc")
		if err != nil {
			t.Fatal(err)
		}
		path := strings.TrimSpace(out)
		if filepath.Dir(path) != filepath.Join(dir, "design") {
			t.Fatalf("path = %q", out)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("edit prints path and runs editor on it", func(t *testing.T) {
		editor, record := recordEditor(t, "")
		t.Setenv("EDITOR", editor)
		out, _, err := run(t, app, "design", "create", "Open me", "--edit", "--scope", "wc")
		if err != nil {
			t.Fatal(err)
		}
		path := strings.TrimSpace(out)
		if !filepath.IsAbs(path) || filepath.Dir(path) != filepath.Join(dir, "design") || strings.Count(out, "\n") != 1 {
			t.Fatalf("stdout = %q", out)
		}
		args := recordedArgs(t, record)
		if len(args) != 1 || args[0] != path {
			t.Fatalf("editor args = %#v want %s", args, path)
		}
	})

	t.Run("unset EDITOR leaves scaffold", func(t *testing.T) {
		t.Setenv("EDITOR", "")
		out, _, err := run(t, app, "design", "create", "No env", "--edit", "--scope", "wc")
		if err == nil {
			t.Fatal("unset EDITOR must be non-zero")
		}
		if ExitCodeFromError(err) == exitUsage {
			t.Fatalf("unset EDITOR is not usage: %v", err)
		}
		path := strings.TrimSpace(out)
		if !filepath.IsAbs(path) || filepath.Dir(path) != filepath.Join(dir, "design") {
			t.Fatalf("stdout = %q", out)
		}
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatal(statErr)
		}
		if !strings.Contains(err.Error(), "$EDITOR") || !strings.Contains(err.Error(), "tk design create --edit") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("editor non-zero leaves scaffold", func(t *testing.T) {
		t.Setenv("EDITOR", "false")
		out, _, err := run(t, app, "design", "create", "Editor fails", "--edit", "--scope", "wc")
		if err == nil || !strings.Contains(err.Error(), "editor exited with an error") {
			t.Fatalf("err = %v", err)
		}
		path := strings.TrimSpace(out)
		if !filepath.IsAbs(path) || filepath.Dir(path) != filepath.Join(dir, "design") {
			t.Fatalf("stdout = %q", out)
		}
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatal(statErr)
		}
	})

	t.Run("failed create does not launch editor", func(t *testing.T) {
		t.Setenv("EDITOR", "false")
		designDir := filepath.Join(dir, "design")
		before, err := os.ReadDir(designDir)
		if err != nil {
			t.Fatal(err)
		}
		out, _, err := run(t, app, "design", "create", "   ", "--edit", "--scope", "wc")
		if ExitCodeFromError(err) != exitUsage {
			t.Fatalf("empty title = %v", err)
		}
		if err != nil && strings.Contains(err.Error(), "editor exited") {
			t.Fatalf("editor launched: %v", err)
		}
		if strings.TrimSpace(out) != "" {
			t.Fatalf("stdout = %q", out)
		}
		after, err := os.ReadDir(designDir)
		if err != nil {
			t.Fatal(err)
		}
		if len(after) != len(before) {
			t.Fatalf("design dir %d -> %d", len(before), len(after))
		}
	})
}

func TestDesignCreateEditNeverSelfCommits(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	_, repo := initGitScope(t, app, "wc", true)

	t.Setenv("EDITOR", "true")
	out, _, err := run(t, app, "design", "create", "Edited shape", "--edit", "--scope", "wc")
	if err != nil {
		t.Fatal(err)
	}
	path := strings.TrimSpace(out)
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	if n := len(gitLog(t, repo)); n != 0 {
		t.Fatalf("create --edit committed %d", n)
	}

	t.Setenv("EDITOR", "false")
	out, _, err = run(t, app, "design", "create", "Failing editor", "--edit", "--scope", "wc")
	if err == nil {
		t.Fatal("failing editor must be non-zero")
	}
	path = strings.TrimSpace(out)
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatal(statErr)
	}
	if n := len(gitLog(t, repo)); n != 0 {
		t.Fatalf("failed editor committed %d", n)
	}
}
