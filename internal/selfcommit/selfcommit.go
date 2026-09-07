// Package selfcommit is the single reusable self-commit step for auto-commit scopes.
// Mechanism only: auto-commit policy, git-root existence, and sync_disabled emission
// belong to the caller. Commit assumes a git-root and available git; it takes the
// commit lock, stages matchable paths, and commits.
package selfcommit

import (
	"context"
	"fmt"
	"os"

	"github.com/p3bot/tk/internal/git"
	"github.com/p3bot/tk/internal/gitstate"
)

// Request is one self-commit: fixed message, post-write path, and optional old path.
type Request struct {
	StateDir string
	GitRoot  string
	Message  string
	// NewPath is the post-write path; always staged.
	NewPath string
	// OldPath is a path the mutation removed (empty for in-place rewrite).
	// Staged only when matchable so a never-committed old path is omitted.
	OldPath string
}

// Commit acquires the git-root commit lock and delegates to CommitCore.
// Callers that already hold the lock must call CommitCore: re-acquiring the same
// flock in-process blocks forever.
func Commit(ctx context.Context, req Request) error {
	lock, err := gitstate.AcquireCommitLock(req.StateDir, req.GitRoot)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()
	return CommitCore(ctx, req)
}

// CommitCore stages matchable paths and commits under the fixed message — no push.
// Caller must hold the git-root commit lock. Byte-identical rewrite is a clean no-op.
// A failed staged-check or commit unstages those paths so the index is not left
// holding a write that did not land.
func CommitCore(ctx context.Context, req Request) error {
	paths := []string{req.NewPath}
	if req.OldPath != "" && req.OldPath != req.NewPath && matchable(ctx, req.GitRoot, req.OldPath) {
		paths = append(paths, req.OldPath)
	}
	if err := git.Add(ctx, req.GitRoot, paths); err != nil {
		return err
	}
	return commitStaged(ctx, req.GitRoot, req.Message, paths)
}

// BatchRequest is a multi-file self-commit: fixed message and every touched path.
type BatchRequest struct {
	StateDir string
	GitRoot  string
	Message  string
	// Paths are every touched path — new and removed. Each is staged only when matchable.
	Paths []string
}

// CommitPaths acquires the git-root commit lock and delegates to CommitPathsCore.
// Callers that already hold the lock must call CommitPathsCore.
func CommitPaths(ctx context.Context, req BatchRequest) error {
	lock, err := gitstate.AcquireCommitLock(req.StateDir, req.GitRoot)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()
	return CommitPathsCore(ctx, req)
}

// CommitPathsCore stages every matchable path and commits — no push.
// Caller must hold the git-root commit lock. A failed staged-check or commit
// unstages those paths so the index is not left holding a write that did not land.
func CommitPathsCore(ctx context.Context, req BatchRequest) error {
	var stage []string
	seen := map[string]bool{}
	for _, p := range req.Paths {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		if matchable(ctx, req.GitRoot, p) {
			stage = append(stage, p)
		}
	}
	if len(stage) == 0 {
		return nil
	}
	if err := git.Add(ctx, req.GitRoot, stage); err != nil {
		return err
	}
	return commitStaged(ctx, req.GitRoot, req.Message, stage)
}

// commitStaged commits paths already in the index. Any error after add unstages
// them. Byte-identical paths are a clean no-op.
func commitStaged(ctx context.Context, gitRoot, message string, paths []string) error {
	staged, err := git.HasStagedChanges(ctx, gitRoot, paths...)
	if err != nil {
		return unstageAfter(ctx, gitRoot, paths, err)
	}
	if !staged {
		return nil
	}
	if err := git.Commit(ctx, gitRoot, message, paths); err != nil {
		return unstageAfter(ctx, gitRoot, paths, fmt.Errorf("commit %s: %w", message, err))
	}
	return nil
}

func unstageAfter(ctx context.Context, gitRoot string, paths []string, err error) error {
	if uerr := git.Unstage(ctx, gitRoot, paths); uerr != nil {
		return fmt.Errorf("%w (also failed to unstage: %w)", err, uerr)
	}
	return err
}

// matchable reports whether git add can name path: still present, or tracked (deletion).
func matchable(ctx context.Context, gitRoot, path string) bool {
	if _, err := os.Stat(path); err == nil {
		return true
	}
	return git.Tracked(ctx, gitRoot, path)
}
