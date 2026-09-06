package cli

import (
	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/integrity"
)

func newRepairCmd(app *App) *cobra.Command {
	var reSpaceOrder, all bool
	cmd := &cobra.Command{
		Use:   "repair [--re-space-order] [--all]",
		Short: "Repair integrity issues across scopes",
		Long: "Repair id collisions, equal order keys, and archive layout drift. Needs a\n" +
			"scope (ambient, TK_SCOPE, or --all) and never mutates silently machine-wide.\n" +
			"Refuses on a mid-rebase auto-commit git-root unless --all, which skips that\n" +
			"root. --re-space-order also shortens a band of over-long order keys (additive;\n" +
			"default classes still run). There is no --scope flag. Does not diagnose — run\n" +
			"tk doctor afterwards for remaining issues.",
		Args: noArgs(),
		RunE: func(c *cobra.Command, _ []string) error {
			return runRepair(app, c, reSpaceOrder, all)
		},
	}
	cmd.Flags().BoolVar(&reSpaceOrder, "re-space-order", false, "also re-space a band of pathologically long order keys")
	cmd.Flags().BoolVar(&all, "all", false, "act on every registered scope")
	return cmd
}

func runRepair(app *App, c *cobra.Command, reSpaceOrder, all bool) error {
	e, err := app.openEngine(c)
	if err != nil {
		return err
	}
	defer e.close()

	scopes, err := e.repairScopes(all)
	if err != nil {
		return err
	}

	deps := e.integrityDeps(c)
	rep := cobraReporter{c: c}
	return integrity.RepairScopes(deps, rep, scopes, integrity.Flags{
		Repair:       true,
		ReSpaceOrder: reSpaceOrder,
		All:          all,
	})
}

// repairScopes: mutate needs ambient, TK_SCOPE, or --all (never silent machine-wide).
func (e *engine) repairScopes(all bool) ([]string, error) {
	name, ok, err := e.ambientScope()
	if err != nil {
		return nil, err
	}
	switch {
	case all:
		return e.sortedRegistered(), nil
	case ok:
		return []string{name}, nil
	default:
		return nil, usageErrorf("tk repair needs a scope to act on: run inside a registered code-root, set TK_SCOPE=<name>, or pass --all")
	}
}
