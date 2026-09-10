package gitstate

import (
	"context"
	"fmt"
	"strings"

	"github.com/p3bot/tk/internal/git"
	"github.com/p3bot/tk/internal/scopefile"
)

// MidRebaseError refuses auto-commit writes on a mid-rebase git-root.
type MidRebaseError struct {
	Scope, Root, Where string
}

func (e *MidRebaseError) Error() string {
	return fmt.Sprintf("%s is mid-sync-conflict in shared repo %s — resolve %s then run tk sync",
		e.Scope, e.Root, e.Where)
}

// CheckMidRebase refuses auto-commit writes on a mid-rebase git-root (repo-granular).
// Repo-driven mutators stay allowed: autoCommit false is a quiet no-op.
func CheckMidRebase(ctx context.Context, scope string, autoCommit bool, root string, hasRoot bool) error {
	if !autoCommit {
		return nil
	}
	return CheckGitRootMidRebase(ctx, scope, root, hasRoot)
}

// CheckGitRootMidRebase refuses when the git-root is mid-rebase, regardless of
// autoCommit. Callers that must not become tk-driven onto a paused rebase use
// this instead of passing a fake true into CheckMidRebase.
func CheckGitRootMidRebase(ctx context.Context, scope, root string, hasRoot bool) error {
	if !hasRoot {
		return nil
	}
	if !git.MidRebase(ctx, root) {
		return nil
	}
	where := "the conflicted file"
	if files := git.UnmergedFiles(ctx, root); len(files) > 0 {
		where = strings.Join(files, ", ")
	}
	return &MidRebaseError{Scope: scope, Root: root, Where: where}
}

// SyncNeededReason is at most one catalogue reason after a tk-driven write.
// Priority: push failed, then dirty, then unpushed.
func SyncNeededReason(ctx context.Context, stateDir, dir, root string) string {
	if _, present := ReadLastPushError(stateDir, root); present {
		return "push failed"
	}
	if n := scopefile.CountAllowlistedDirty(ctx, dir, root, true); n > 0 {
		return "dirty"
	}
	if n, err := git.UnpushedCount(ctx, root); err == nil && n > 0 {
		return "unpushed"
	}
	return ""
}
