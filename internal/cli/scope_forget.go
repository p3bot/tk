package cli

import "github.com/spf13/cobra"

func newScopeForgetCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "forget <name>",
		Short: "Unregister a scope (registry, lens, and me entries only)",
		Long: "Drop a scope's registry, lens, and me entries so this machine forgets it. It\n" +
			"never touches the scope's files or repo — they simply become unknown until\n" +
			"re-registered with tk scope import. Dropping any index rows is out of\n" +
			"scope for this verb; forget here removes registry, lens, and me only. A merely\n" +
			"unreachable dir stays registered until forget.",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(_ *cobra.Command, args []string) error {
			return app.admin().Forget(args[0])
		},
	}
}
