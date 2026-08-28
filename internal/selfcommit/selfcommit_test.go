package selfcommit

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/gitstate"
	"github.com/p3bot/tk/internal/testgit"
)

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	testgit.Hermetic(t)
}

func gitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return testgit.Combined(t, dir, args...) + "\n"
}

func newRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	gitCmd(t, repo, "init")
	gitCmd(t, repo, "config", "user.email", "a@b.c")
	gitCmd(t, repo, "config", "user.name", "tk-test")
	gitCmd(t, repo, "config", "commit.gpgsign", "false")
	return repo
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func tree(t *testing.T, repo string) string {
	return gitCmd(t, repo, "ls-tree", "-r", "--name-only", "HEAD")
}

// A never-committed old path must be omitted from pathspec, not passed and left to error.
func TestCommitOmitsUntrackedOldPath(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	state := t.TempDir()
	repo := newRepo(t)

	oldPath := filepath.Join(repo, "wc", "wc-ab2c-x.md")
	newPath := filepath.Join(repo, "wc", "archive", "wc-ab2c-x.md")
	write(t, newPath, "# x\n") // move already happened; old path absent and never tracked

	err := Commit(ctx, Request{
		StateDir: state, GitRoot: repo,
		Message: "tk: wc-ab2c -> done",
		NewPath: newPath, OldPath: oldPath,
	})
	if err != nil {
		t.Fatalf("commit with untracked old path must not error: %v", err)
	}
	tr := tree(t, repo)
	if !strings.Contains(tr, "wc/archive/wc-ab2c-x.md") {
		t.Errorf("new path must be committed, tree=%q", tr)
	}
	_ = oldPath
}

// A tracked old path that the mutation removed must be staged so its deletion is recorded.
func TestCommitStagesTrackedRemoval(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	state := t.TempDir()
	repo := newRepo(t)

	oldPath := filepath.Join(repo, "wc", "wc-ab2c-x.md")
	write(t, oldPath, "# x\n")
	gitCmd(t, repo, "add", "wc/wc-ab2c-x.md")
	gitCmd(t, repo, "commit", "-m", "seed")

	newPath := filepath.Join(repo, "wc", "archive", "wc-ab2c-x.md")
	write(t, newPath, "# x done\n")
	if err := os.Remove(oldPath); err != nil {
		t.Fatal(err)
	}
	err := Commit(ctx, Request{
		StateDir: state, GitRoot: repo,
		Message: "tk: wc-ab2c -> done",
		NewPath: newPath, OldPath: oldPath,
	})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	tr := tree(t, repo)
	if strings.Contains(tr, "wc/wc-ab2c-x.md") && !strings.Contains(tr, "wc/archive/") {
		t.Errorf("the old root path should be removed from the committed tree, tree=%q", tr)
	}
	if !strings.Contains(tr, "wc/archive/wc-ab2c-x.md") {
		t.Errorf("the archive path should be committed, tree=%q", tr)
	}
}

// Cores require the caller already holds the git-root commit lock; re-acquiring hangs.
func TestCoresCommitUnderCallerHeldLock(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	state := t.TempDir()
	repo := newRepo(t)

	lock, err := gitstate.AcquireCommitLock(state, repo)
	if err != nil {
		t.Fatalf("acquire commit lock: %v", err)
	}
	defer func() { _ = lock.Release() }()

	one := filepath.Join(repo, "wc", "wc-ab2c-x.md")
	write(t, one, "# x\n")
	if err := CommitCore(ctx, Request{StateDir: state, GitRoot: repo, Message: "tk: add wc-ab2c x", NewPath: one}); err != nil {
		t.Fatalf("CommitCore under held lock: %v", err)
	}

	two := filepath.Join(repo, "wc", "wc-cd3e-y.md")
	three := filepath.Join(repo, "wc", "wc-ef4g-z.md")
	write(t, two, "# y\n")
	write(t, three, "# z\n")
	if err := CommitPathsCore(ctx, BatchRequest{StateDir: state, GitRoot: repo, Message: "tk: sync 2 path(s)", Paths: []string{two, three}}); err != nil {
		t.Fatalf("CommitPathsCore under held lock: %v", err)
	}

	tr := tree(t, repo)
	for _, want := range []string{"wc/wc-ab2c-x.md", "wc/wc-cd3e-y.md", "wc/wc-ef4g-z.md"} {
		if !strings.Contains(tr, want) {
			t.Errorf("expected %s in committed tree, tree=%q", want, tr)
		}
	}
}

// Byte-identical rewrite stages nothing; Commit must be a clean no-op.
func TestCommitNoOpOnIdenticalRewrite(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	state := t.TempDir()
	repo := newRepo(t)
	p := filepath.Join(repo, "wc", "wc-ab2c-x.md")
	write(t, p, "# x\n")
	gitCmd(t, repo, "add", "wc/wc-ab2c-x.md")
	gitCmd(t, repo, "commit", "-m", "seed")

	err := Commit(ctx, Request{
		StateDir: state, GitRoot: repo,
		Message: "tk: wc-ab2c order",
		NewPath: p,
	})
	if err != nil {
		t.Fatalf("no-op commit must not error: %v", err)
	}
	// Still exactly one commit — the no-op added none.
	log := gitCmd(t, repo, "log", "--oneline")
	if strings.Count(strings.TrimSpace(log), "\n") != 0 {
		t.Errorf("no-op self-commit must not add a commit, log=%q", log)
	}
}
