package cli

import (
	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/scopeadmin"
)

func newScopeRebindCmd(app *App) *cobra.Command {
	var (
		name     string
		codeRoot string
	)
	cmd := &cobra.Command{
		Use:   "rebind <dir> --name <name> [--code-root <path>]",
		Short: "Rewrite the machine-local registry paths for a registered scope",
		Long: "Move a registered scope's paths after a clone or folder move. The\n" +
			"positional <dir> always updates the registry dir; --name selects the entry\n" +
			"(required); --code-root updates root when given and leaves it unchanged when\n" +
			"omitted. It preserves the lens, is idempotent, and refuses a wrong tree\n" +
			"(the post-rebind tk.cue name must equal --name). It is not name repair —\n" +
			"a drifted name still needs forget + import.",
		Args: exactArgs("<dir>"),
		RunE: func(c *cobra.Command, args []string) error {
			if name == "" {
				return usageErrorf("--name is required")
			}
			dir, err := absPath(args[0])
			if err != nil {
				return err
			}
			p := scopeadmin.RebindParams{Dir: dir, Name: name}
			if c.Flags().Changed("code-root") {
				cr, err := absPath(codeRoot)
				if err != nil {
					return err
				}
				p.CodeRoot = cr
				p.CodeRootGiven = true
			}

			registered, changed, err := app.admin().Rebind(p)
			if err != nil {
				return err
			}
			if !changed {
				stderrln(c, "nothing changed: scope already bound to these paths")
			}
			stdoutln(c, registered)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "scope name selecting the registry entry (required)")
	cmd.Flags().StringVar(&codeRoot, "code-root", "", "new code-root (omit to leave root unchanged)")
	return cmd
}
