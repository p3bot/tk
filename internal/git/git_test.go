package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/testgit"
)

func requireGit(t *testing.T) {
	t.Helper()
	if !Available() {
		t.Skip("git not on PATH")
	}
	testgit.Hermetic(t)
}

func gitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	testgit.Run(t, dir, args...)
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

func TestAddCommitAndStagedChanges(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := newRepo(t)
	p := filepath.Join(repo, "wc", "wc-ab2c-x.md")
	write(t, p, "# x\n")

	staged, err := HasStagedChanges(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if staged {
		t.Error("nothing should be staged before add")
	}
	if err := Add(ctx, repo, []string{p}); err != nil {
		t.Fatal(err)
	}
	staged, err = HasStagedChanges(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if !staged {
		t.Error("the added path should be staged")
	}
	if err := Commit(ctx, repo, "tk: wc-ab2c -> todo", []string{p}); err != nil {
		t.Fatal(err)
	}
	if !Tracked(ctx, repo, p) {
		t.Error("committed path must be tracked")
	}
}

func TestUnstageDropsIndexKeepsWorktree(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := newRepo(t)
	p := filepath.Join(repo, "f")
	write(t, p, "old\n")
	if err := Add(ctx, repo, []string{p}); err != nil {
		t.Fatal(err)
	}
	if err := Commit(ctx, repo, "seed", []string{p}); err != nil {
		t.Fatal(err)
	}
	write(t, p, "new\n")
	if err := Add(ctx, repo, []string{p}); err != nil {
		t.Fatal(err)
	}
	if err := Unstage(ctx, repo, []string{p}); err != nil {
		t.Fatalf("Unstage: %v", err)
	}
	staged, err := HasStagedChanges(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if staged {
		t.Error("index must match HEAD after Unstage")
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new\n" {
		t.Errorf("working tree = %q, want new", data)
	}
}

func TestUnstageEmptyRepoDropsStagedAdd(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := newRepo(t)
	p := filepath.Join(repo, "f")
	write(t, p, "x\n")
	if err := Add(ctx, repo, []string{p}); err != nil {
		t.Fatal(err)
	}
	if err := Unstage(ctx, repo, []string{p}); err != nil {
		t.Fatalf("Unstage with no HEAD: %v", err)
	}
	staged, err := HasStagedChanges(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if staged {
		t.Error("empty-repo Unstage must not leave the path staged")
	}
}

func TestCommitPathspecLeavesOtherStaged(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := newRepo(t)
	keep := filepath.Join(repo, "keep")
	other := filepath.Join(repo, "other")
	write(t, keep, "seed\n")
	if err := Add(ctx, repo, []string{keep}); err != nil {
		t.Fatal(err)
	}
	if err := Commit(ctx, repo, "seed", []string{keep}); err != nil {
		t.Fatal(err)
	}
	write(t, keep, "tk\n")
	write(t, other, "host\n")
	if err := Add(ctx, repo, []string{keep, other}); err != nil {
		t.Fatal(err)
	}
	ours, err := HasStagedChanges(ctx, repo, keep)
	if err != nil {
		t.Fatal(err)
	}
	if !ours {
		t.Fatal("named path should be staged")
	}
	if err := Commit(ctx, repo, "tk only", []string{keep}); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	names := testgit.Combined(t, repo, "show", "--name-only", "--pretty=format:", "HEAD")
	if !strings.Contains(names, "keep") || strings.Contains(names, "other") {
		t.Errorf("commit must be keep only, names:\n%s", names)
	}
	staged, err := HasStagedChanges(ctx, repo, other)
	if err != nil {
		t.Fatal(err)
	}
	if !staged {
		t.Error("unrelated path must stay staged")
	}
}

func TestTrackedFalseForUntracked(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := newRepo(t)
	p := filepath.Join(repo, "wc", "wc-ab2c-x.md")
	write(t, p, "# x\n")
	if Tracked(ctx, repo, p) {
		t.Error("a never-added path must not be tracked")
	}
}

func TestDirtyPathsRenameReportsDestination(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := newRepo(t)
	// -z porcelain emits "R  <new>\0<old>\0": report destination, consume source.
	oldPath := filepath.Join(repo, "wc", "wc-ab2c-x.md")
	write(t, oldPath, "# x\n")
	gitCmd(t, repo, "add", "wc/wc-ab2c-x.md")
	gitCmd(t, repo, "commit", "-m", "seed")
	gitCmd(t, repo, "mv", "wc/wc-ab2c-x.md", "wc/wc-ab2c-y.md")

	dir := filepath.Join(repo, "wc")
	paths, err := DirtyPaths(ctx, repo, dir)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, p := range paths {
		found[filepath.Base(p)] = true
	}
	if !found["wc-ab2c-y.md"] {
		t.Errorf("a rename must report its destination path, got %v", paths)
	}
	if found["wc-ab2c-x.md"] {
		t.Errorf("the rename source field must be consumed, not reported, got %v", paths)
	}
}

func TestMidRebaseDetection(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := newRepo(t)
	if MidRebase(ctx, repo) {
		t.Error("a fresh repo is not mid-rebase")
	}
	if err := os.MkdirAll(filepath.Join(repo, ".git", "rebase-apply"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !MidRebase(ctx, repo) {
		t.Error("a rebase-apply dir should read as mid-rebase")
	}
}

func TestDirtyPathsScopedAndExpanded(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := newRepo(t)
	// Untracked scope dir must expand to individual files; paths outside dir must not.
	write(t, filepath.Join(repo, "wc", "tk.cue"), "name: \"wc\"\n")
	write(t, filepath.Join(repo, "wc", "wc-ab2c-x.md"), "# x\n")
	write(t, filepath.Join(repo, "other", "unrelated.md"), "# y\n")

	dir := filepath.Join(repo, "wc")
	paths, err := DirtyPaths(ctx, repo, dir)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, p := range paths {
		found[filepath.Base(p)] = true
	}
	if !found["tk.cue"] || !found["wc-ab2c-x.md"] {
		t.Errorf("scoped dirty paths should list the scope's files, got %v", paths)
	}
	if found["unrelated.md"] {
		t.Errorf("dirty paths must stay scoped to dir, got %v", paths)
	}
}
