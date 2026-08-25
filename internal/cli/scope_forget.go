package cli

import "github.com/spf13/cobra"

func newScopeForgetCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "forget <name>",
		Short: "Unregister a scope (registry, lens, me, and note entries only)",
		Long: "Drop a scope's registry, lens, me, and note entries so this machine forgets it.\n" +
			"It never touches the scope's files or repo — they simply become unknown until\n" +
			"re-registered with tk scope import. Dropping any index rows is out of\n" +
			"scope for this verb; forget here removes registry, lens, me, and note only. A\n" +
			"merely unreachable dir stays registered until forget.",
		Args: exactArgs("<name>"),
		RunE: func(_ *cobra.Command, args []string) error {
			return app.admin().Forget(args[0])
		},
	}
}
