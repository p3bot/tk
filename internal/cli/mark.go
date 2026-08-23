package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/writeengine"
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
			"git-root exists. todo → in-progress on a tk-driven git-root with an upstream is a\n" +
			"claim: refresh that root, re-check the ticket is still todo, write, then push.\n" +
			"A quarantined or duplicate-id ticket is refused with no write.\n" +
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
	lu, err := e.writeLookup(scope, idArg, form)
	if err != nil {
		return err
	}
	res, err := writeengine.Mark(e.writeDeps(c.Context()), claimReporter{c: c}, writeengine.MarkInput{
		Scope:     scope,
		Dir:       entry.Dir,
		Lookup:    lu,
		NewStatus: newStatus,
	})
	return emitWriteResult(c, res, err)
}
