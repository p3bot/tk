package cli

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/token"
	"github.com/p3bot/tk/internal/writeengine"
)

func checkMidRebase(ctx context.Context, scope string, autoCommit bool, root string, hasRoot bool) error {
	return writeengine.CheckMidRebase(ctx, scope, autoCommit, root, hasRoot)
}

func (e *engine) tkDrivenSyncNeeded(ctx context.Context, c *cobra.Command, dir, root string) {
	if reason := writeengine.SyncNeededReason(ctx, e.app.StateDir, dir, root); reason != "" {
		stderrln(c, token.Line(token.SyncNeeded, reason))
	}
}
