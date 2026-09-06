package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepairSelfCommitsAutoCommit(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	t.Setenv("TK_SCOPE", "wc")
	dir, repo := initGitScope(t, app, "wc", true)
	addTicket(t, dir, "wc-ab2c", "alpha", "todo", "a0", "# A\n", false, "")
	addTicket(t, dir, "wc-ab2c", "beta", "todo", "a1", "# B\n", false, "")

	if _, _, err := run(t, app, "repair"); err != nil {
		t.Fatalf("repair: %v", err)
	}
	log := gitLog(t, repo)
	if len(log) == 0 || log[0] != "tk: repair duplicate id wc-ab2c -> wc-ab2ca" {
		t.Fatalf("repair should self-commit with the fixed message, got %v", log)
	}
}

func TestRepairMultiLoserCommitMessageCoversBatch(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	t.Setenv("TK_SCOPE", "wc")
	dir, repo := initGitScope(t, app, "wc", true)
	addTicket(t, dir, "wc-ab2c", "alpha", "todo", "a0", "# A\n", false, "")
	addTicket(t, dir, "wc-ab2c", "beta", "todo", "a1", "# B\n", false, "")
	addTicket(t, dir, "wc-ab2c", "gamma", "todo", "a2", "# G\n", false, "")

	if _, _, err := run(t, app, "repair"); err != nil {
		t.Fatalf("repair: %v", err)
	}
	log := gitLog(t, repo)
	if len(log) == 0 || log[0] != "tk: repair duplicate id wc-ab2c -> wc-ab2ca, wc-ab2cb" {
		t.Fatalf("multi-loser repair message must name the collided id and every rename, got %v", log)
	}
}

func TestRepairPlannedRidesSyncDisabled(t *testing.T) {
	app := newApp(t)
	t.Setenv("TK_SCOPE", "wc")
	dir := filepath.Join(t.TempDir(), "wc")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, app, "scope", "init", dir, "--name", "wc", "--auto-commit"); err != nil {
		t.Fatalf("init planned auto-commit: %v", err)
	}
	addTicket(t, dir, "wc-ab2c", "alpha", "todo", "a0", "# A\n", false, "")
	addTicket(t, dir, "wc-ab2c", "beta", "todo", "a1", "# B\n", false, "")

	_, errOut, err := run(t, app, "repair")
	if err != nil {
		t.Fatalf("planned repair should land files: %v", err)
	}
	if !strings.Contains(errOut, "sync_disabled:") {
		t.Errorf("planned auto-commit repair should ride sync_disabled, got %q", errOut)
	}
	if !fileExists(dir, "wc-ab2ca-beta.md") {
		t.Errorf("planned repair must still write the files")
	}
}

func TestRepairRefusesMidRebase(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	t.Setenv("TK_SCOPE", "wc")
	dir, repo := initGitScope(t, app, "wc", true)
	addTicket(t, dir, "wc-ab2c", "alpha", "todo", "a0", "# A\n", false, "")
	addTicket(t, dir, "wc-ab2c", "beta", "todo", "a1", "# B\n", false, "")
	if err := os.MkdirAll(filepath.Join(repo, ".git", "rebase-merge"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, _, err := run(t, app, "repair"); ExitCodeFromError(err) != exitFailure {
		t.Errorf("mid-rebase repair should refuse non-zero, got %v", err)
	}
	if _, _, err := run(t, app, "doctor"); err != nil {
		t.Errorf("bare doctor must still run mid-rebase, got %v", err)
	}
}
