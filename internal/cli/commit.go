package cli

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/gitstate"
	"github.com/p3bot/tk/internal/token"
)

func checkMidRebase(ctx context.Context, scope string, autoCommit bool, root string, hasRoot bool) error {
	return gitstate.CheckMidRebase(ctx, scope, autoCommit, root, hasRoot)
}

func checkGitRootMidRebase(ctx context.Context, scope, root string, hasRoot bool) error {
	return gitstate.CheckGitRootMidRebase(ctx, scope, root, hasRoot)
}

func (e *engine) tkDrivenSyncNeeded(ctx context.Context, c *cobra.Command, dir, root string) {
	if reason := gitstate.SyncNeededReason(ctx, e.app.StateDir, dir, root); reason != "" {
		stderrln(c, token.Line(token.SyncNeeded, reason))
	}
}
