package cli

import (
	"github.com/spf13/cobra"
)

func newReindexCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reindex",
		Short: "Rebuild the machine-wide index from ticket files",
		Long: "Drop the derived SQLite index and refill it from every registered scope's\n" +
			"ticket files. The index is a cache: reindex never mutates ticket files, tk.cue,\n" +
			"the registry, or git state, and it does not diagnose or repair integrity.\n" +
			"Machine-wide always — there is no --scope or --all. Use when incremental\n" +
			"reconcile may have missed a change (mtime-preserving copy, restore, clock skew)\n" +
			"or the cache looks wrong relative to files.",
		Args: noArgs(),
		RunE: func(c *cobra.Command, _ []string) error {
			return runReindex(app, c)
		},
	}
	return cmd
}

func runReindex(app *App, c *cobra.Command) error {
	e, err := app.openEngine(c)
	if err != nil {
		return err
	}
	defer e.close()

	if err := e.db.Rebuild(); err != nil {
		return err
	}
	if _, err := e.reconcileResult(e.allTargets()); err != nil {
		return err
	}
	stderrln(c, "tk reindex: rebuilt index from files")
	return nil
}
