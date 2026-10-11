package syncengine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/git"
	"github.com/p3bot/tk/internal/registry"
	"github.com/p3bot/tk/internal/testgit"
)

func TestRefreshRootDoesNotResumePause(t *testing.T) {
	if !git.Available() {
		t.Skip("git not on PATH")
	}
	testgit.Hermetic(t)
	repo := t.TempDir()
	testgit.Run(t, repo, "init")
	testgit.Run(t, repo, "config", "user.email", "a@b.c")
	testgit.Run(t, repo, "config", "user.name", "tk-test")
	if err := os.WriteFile(filepath.Join(repo, ".git", "MERGE_HEAD"), []byte("0123456789abcdef0123456789abcdef01234567\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	deps := Deps{Ctx: context.Background(), Reg: &registry.Registry{}, StateDir: t.TempDir()}
	var rep capture
	err := RefreshRoot(deps, &rep, Target{Root: repo})
	if !errors.Is(err, ErrRootFailed) {
		t.Fatalf("mid-merge refresh must fail before fetch, got %v", err)
	}
	got := strings.Join(rep.err, "\n")
	if !strings.Contains(got, "mid-merge") || !strings.Contains(got, "will not resume") {
		t.Fatalf("stderr = %q", got)
	}
	if _, err := os.Stat(filepath.Join(repo, ".git", "MERGE_HEAD")); err != nil {
		t.Fatal("refresh must leave the merge in place")
	}

	if err := os.Remove(filepath.Join(repo, ".git", "MERGE_HEAD")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".git", "rebase-merge"), 0o755); err != nil {
		t.Fatal(err)
	}
	rep.err = nil
	err = RefreshRoot(deps, &rep, Target{Root: repo})
	if !errors.Is(err, ErrRootFailed) {
		t.Fatalf("mid-rebase refresh must fail before fetch, got %v", err)
	}
	got = strings.Join(rep.err, "\n")
	if !strings.Contains(got, "mid-rebase") || !strings.Contains(got, "will not resume") {
		t.Fatalf("stderr = %q", got)
	}
}
