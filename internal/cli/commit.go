package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/token"
	"github.com/p3bot/tk/internal/writeengine"
)

func checkMidRebase(ctx context.Context, scope string, autoCommit bool, root string, hasRoot bool) error {
	return writeengine.CheckMidRebase(ctx, scope, autoCommit, root, hasRoot)
}

func (e *engine) completeStateDurability(ctx context.Context, c *cobra.Command, scope, dir string, autoCommit bool, message, newPath, oldPath, root string, hasRoot bool) error {
	disabled, needed, err := writeengine.CompleteState(e.writeDeps(ctx), scope, dir, autoCommit, message, newPath, oldPath, root, hasRoot)
	if err != nil {
		return err
	}
	if disabled != "" {
		stderrln(c, token.Line(token.SyncDisabled, disabled))
	}
	if needed != "" {
		stderrln(c, token.Line(token.SyncNeeded, needed))
	}
	return nil
}

func (e *engine) createDurability(ctx context.Context, c *cobra.Command, dir string, autoCommit, terminal bool, fullID, root string, hasRoot bool) {
	if terminal {
		stderrln(c, fmt.Sprintf("note: %s scaffolded under archive/ — a terminal create is not git-durable until tk sync (auto-commit) or a host commit", fullID))
	}
	if !autoCommit || !hasRoot {
		return
	}
	e.tkDrivenSyncNeeded(ctx, c, dir, root)
}

func (e *engine) tkDrivenSyncNeeded(ctx context.Context, c *cobra.Command, dir, root string) {
	if reason := writeengine.SyncNeededReason(ctx, e.app.StateDir, dir, root); reason != "" {
		stderrln(c, token.Line(token.SyncNeeded, reason))
	}
}
