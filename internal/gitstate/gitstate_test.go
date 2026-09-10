package gitstate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/p3bot/tk/internal/git"
	"github.com/p3bot/tk/internal/testgit"
)

func TestKeyStableAndHex(t *testing.T) {
	k1 := Key("/repo/one")
	k2 := Key("/repo/one")
	if k1 != k2 {
		t.Error("Key must be stable for the same path")
	}
	if len(k1) != 64 {
		t.Errorf("Key must be 64 hex chars, got %d", len(k1))
	}
	if Key("/repo/one") == Key("/repo/two") {
		t.Error("distinct paths must key differently")
	}
	// Cleaning is applied: uncleaned path keys the same as its clean form.
	if Key("/repo/one/") != Key("/repo/one") {
		t.Error("Key must clean the path before hashing")
	}
}

func TestKeyResolvesSymlinkSpellings(t *testing.T) {
	real := t.TempDir()
	linkParent := t.TempDir()
	link := filepath.Join(linkParent, "repo-link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if Key(link) != Key(real) {
		t.Errorf("Key must collapse symlink spellings: link=%q real=%q", Key(link), Key(real))
	}
}

func TestDirUnderStateHome(t *testing.T) {
	got := Dir("/state/tk", "/repo/one")
	want := filepath.Join("/state/tk", "git-roots", Key("/repo/one"))
	if got != want {
		t.Errorf("Dir = %q want %q", got, want)
	}
}

func TestCommitLockCreatesDirAndSerialises(t *testing.T) {
	state := t.TempDir()
	repo := "/repo/one"
	lock, err := AcquireCommitLock(state, repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(Dir(state, repo), "sync.lock")); err != nil {
		t.Errorf("sync.lock should be created: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	lock2, err := AcquireCommitLock(state, repo)
	if err != nil {
		t.Fatal(err)
	}
	_ = lock2.Release()
}

func TestReadLastPushError(t *testing.T) {
	state := t.TempDir()
	repo := "/repo/one"
	if _, ok := ReadLastPushError(state, repo); ok {
		t.Error("no marker should mean ok=false")
	}
	dir := Dir(state, repo)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "last-push-error"), []byte("  push rejected\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	detail, ok := ReadLastPushError(state, repo)
	if !ok || detail != "push rejected" {
		t.Errorf("marker = %q ok=%v want trimmed detail", detail, ok)
	}
	if err := os.WriteFile(filepath.Join(dir, "last-push-error"), []byte("   \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadLastPushError(state, repo); ok {
		t.Error("an empty marker should read as ok=false")
	}
}

func TestWriteAndClearLastPushError(t *testing.T) {
	state := t.TempDir()
	repo := "/repo/two"

	if err := WriteLastPushError(state, repo, "  remote rejected: non-fast-forward\n"); err != nil {
		t.Fatal(err)
	}
	detail, ok := ReadLastPushError(state, repo)
	if !ok || detail != "remote rejected: non-fast-forward" {
		t.Errorf("round-trip = %q ok=%v", detail, ok)
	}

	if err := ClearLastPushError(state, repo); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadLastPushError(state, repo); ok {
		t.Error("marker must be gone after clear")
	}
	if err := ClearLastPushError(state, repo); err != nil {
		t.Errorf("clearing an absent marker must be idempotent: %v", err)
	}
}

func TestCheckGitRootMidRebaseIgnoresAutoCommit(t *testing.T) {
	if !git.Available() {
		t.Skip("git not on PATH")
	}
	testgit.Hermetic(t)
	repo := t.TempDir()
	testgit.Run(t, repo, "init", "-b", "main")
	if err := os.MkdirAll(filepath.Join(repo, ".git", "rebase-merge"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := CheckMidRebase(ctx, "wc", false, repo, true); err != nil {
		t.Fatalf("CheckMidRebase with autoCommit false must stay quiet, got %v", err)
	}
	var mid *MidRebaseError
	if err := CheckGitRootMidRebase(ctx, "wc", repo, true); !errors.As(err, &mid) {
		t.Fatalf("CheckGitRootMidRebase must refuse, got %v", err)
	}
}
