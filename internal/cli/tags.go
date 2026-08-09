package cli

import (
	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/index"
)

func newTagsCmd(app *App) *cobra.Command {
	var scope string
	cmd := &cobra.Command{
		Use:     "tags [--scope S]",
		Aliases: []string{"tag"},
		Short:   "List distinct tags in use on a scope (read-only inventory)",
		Long: "Print the distinct tags currently present on tickets in one scope, one tag\n" +
			"per line, sorted case-sensitively. Sourced from the machine-wide index after\n" +
			"reconcile. Full-scope inventory: includes archive and all statuses; ignores\n" +
			"the active lens and list's default board-status filter. Empty set prints\n" +
			"nothing and exits 0. Read-only inventory — not a mutator; tag a ticket with\n" +
			"meta add|rm. Alias: tag.",
		Args: usageArgs(cobra.NoArgs),
		RunE: func(c *cobra.Command, _ []string) error {
			return runTags(app, c, scope)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "scope to list tags for (defaults to ambient)")
	return cmd
}

func runTags(app *App, c *cobra.Command, scopeFlag string) error {
	e, err := app.openEngine(c)
	if err != nil {
		return err
	}
	defer e.close()

	resolved, err := e.resolveAmbient(scopeFlag)
	if err != nil {
		return err
	}
	scope := resolved.Name

	if _, err := e.reconcile(c, map[string]string{scope: resolved.Entry.Dir}); err != nil {
		return err
	}

	rows, err := e.db.ScopeTickets(scope)
	if err != nil {
		return err
	}
	for _, tag := range index.DistinctTags(rows) {
		stdoutln(c, tag)
	}
	return nil
}
