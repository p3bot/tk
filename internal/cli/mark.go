package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/p3bot/tk/internal/scopefile"

	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/reconcile"
	"github.com/p3bot/tk/internal/status"
	"github.com/p3bot/tk/internal/token"
)

func newMarkCmd(app *App) *cobra.Command {
	var scope string
	cmd := &cobra.Command{
		Use:   "mark <id> <status> [--scope S]",
		Short: "Mark a ticket's status (blocked / done / in-progress / …)",
		Long: "Rewrite a ticket's status. When the new status crosses the terminal boundary\n" +
			"(non-terminal ↔ terminal) the file is renamed between the dir root and archive/\n" +
			"in the same write, and the post-move absolute path is printed. Statuses are\n" +
			"labels: any known status (built-in or CUE custom) is accepted; an unknown one is\n" +
			"a usage error. Mark never enforces depends (next/claim still gate on them); a soft\n" +
			"depends_open: warning is emitted when the status actually changes into todo,\n" +
			"in-progress, or review while depends remain unmet. When the status actually\n" +
			"changes into built-in done, a soft required_missing: warning is emitted if any\n" +
			"scope-declared required fields are absent or empty (same-status re-mark and\n" +
			"cancelled stay quiet). An auto-commit scope self-commits the change when a\n" +
			"git-root exists. A quarantined or duplicate-id ticket is refused with no write.\n" +
			"For a scope pulse (counts, next, integrity), use `tk status`.",
		Args: usageArgs(cobra.ExactArgs(2)),
		RunE: func(c *cobra.Command, args []string) error {
			return runMark(app, c, args[0], args[1], scope)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "ambient scope for a short id")
	return cmd
}

func runMark(app *App, c *cobra.Command, idArg, newStatus, scopeFlag string) error {
	form, ok := parseIDArg(idArg)
	if !ok {
		return usageErrorf("%q is not a valid ticket id", idArg)
	}

	e, err := app.openEngine(c)
	if err != nil {
		return err
	}
	defer e.close()

	scope, err := e.scopeForID(idArg, form, scopeFlag)
	if err != nil {
		return err
	}
	entry, registered := e.reg.Scopes[scope]
	if !registered {
		return fmt.Errorf("unknown ticket id %q: scope %q is not registered here", idArg, scope)
	}
	dir := entry.Dir

	lock, err := scopefile.AcquireLock(dir)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()

	ctx := c.Context()
	res, err := e.reconcileResult(single(scope, dir))
	if err != nil {
		return err
	}
	if err := refuseUnusableScope(res, scope, dir); err != nil {
		return err
	}
	schema := res.Schema(scope)
	custom := schemaCustom(schema)
	if !status.IsKnown(newStatus, custom) {
		return usageErrorf("%q is not a known status for scope %q", newStatus, scope)
	}
	autoCommit := schemaAutoCommit(schema)
	root, hasRoot := scopefile.GitRoot(dir)
	if err := checkMidRebase(ctx, scope, autoCommit, root, hasRoot); err != nil {
		return err
	}
	e.printWarnings(c, res.Warnings)

	p, err := e.resolveWriteRow(scope, idArg, form)
	if err != nil {
		return err
	}

	m, body, err := readTicketFile(p.Path)
	if err != nil {
		return err
	}
	oldStatus := m.Status
	wasTerminal := status.IsTerminal(oldStatus, custom)
	nowTerminal := status.IsTerminal(newStatus, custom)
	m.Status = newStatus

	newPath, oldPath := p.Path, ""
	if wasTerminal != nowTerminal {
		newPath, err = terminalLocation(dir, filepath.Base(p.Path), nowTerminal)
		if err != nil {
			return err
		}
		oldPath = p.Path
	}

	// Write then rename: crash never leaves two same-id files (layout drift, not a collision).
	if err := writeTicketFile(p.Path, m, body); err != nil {
		return err
	}
	if oldPath != "" && oldPath != newPath {
		if err := os.Rename(oldPath, newPath); err != nil {
			return fmt.Errorf("move %s to %s: %w", oldPath, newPath, err)
		}
	}
	if err := e.rec.SyncPaths(scope, writtenPaths(newPath, oldPath)); err != nil {
		return err
	}

	// Keep historical "-> status" commit subject shape.
	message := fmt.Sprintf("tk: %s -> %s", p.ID, newStatus)
	if err := e.completeStateDurability(ctx, c, scope, dir, autoCommit, message, newPath, oldPath, root, hasRoot); err != nil {
		return err
	}

	out, err := absPath(newPath)
	if err != nil {
		return err
	}
	stdoutln(c, out)
	// Soft only after success: never fail a completed mark if gate/index read fails.
	if oldStatus != newStatus && markReadyActive(newStatus) {
		// Edges are keyed by path; use post-move path so evalDepends still finds them.
		p.Path = newPath
		if line, werr := e.openDependsWarnLine(res, scope, p, newStatus); werr == nil && line != "" {
			stderrln(c, line)
		}
	}
	// Soft required gaps: only when status actually changes into built-in done.
	if oldStatus != newStatus && newStatus == status.Done {
		if missing := schema.MissingRequired(m); len(missing) > 0 {
			stderrln(c, token.FormatRequiredMissing(p.ID, missing))
		}
	}
	return nil
}

// markReadyActive is the closed set of built-in to-statuses that imply ready or active work.
func markReadyActive(s string) bool {
	switch s {
	case status.Todo, status.InProgress, status.Review:
		return true
	default:
		return false
	}
}

// openDependsWarnLine returns the depends_open: line when p has unmet depends (list waiting-on).
func (e *engine) openDependsWarnLine(res *reconcile.Result, scope string, p *index.Ticket, newStatus string) (string, error) {
	gate, err := e.buildGate(res, []string{scope})
	if err != nil {
		return "", err
	}
	ds := gate.evalDepends(p)
	if len(ds.WaitingOn) == 0 {
		return "", nil
	}
	return token.FormatDependsOpen(p.ID, newStatus, ds.WaitingOn), nil
}
