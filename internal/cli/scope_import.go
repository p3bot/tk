package cli

import (
	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/scopeadmin"
)

func newScopeImportCmd(app *App) *cobra.Command {
	var codeRoot string
	cmd := &cobra.Command{
		Use:   "import <dir> [--code-root <path>]",
		Short: "Register an existing on-disk scope, files in place",
		Long: "Register a scope that already exists on disk (for example after a git\n" +
			"clone). Name and autoCommit come from the scope's tk.cue, so import takes\n" +
			"neither --name nor --auto-commit.\n\n" +
			"The code-root defaults to the enclosing git repo root inside a repo, else\n" +
			"the dir; --code-root overrides it (any path: ambient cwd match only — git\n" +
			"durability always follows the scope dir). It hard-fails on a scope-name\n" +
			"collision, on a tk.cue that will not compile or fails schema validation,\n" +
			"and on an autoCommit that disagrees with an existing sibling sharing its\n" +
			"git-root.",
		Args: exactArgs("<dir>"),
		RunE: func(c *cobra.Command, args []string) error {
			dir, err := absPath(args[0])
			if err != nil {
				return err
			}
			p := scopeadmin.ImportParams{Dir: dir}
			if c.Flags().Changed("code-root") {
				cr, err := absPath(codeRoot)
				if err != nil {
					return err
				}
				p.CodeRoot = cr
				p.CodeRootGiven = true
			}

			registered, err := app.admin().Import(p)
			if err != nil {
				return err
			}
			stdoutln(c, registered)
			return nil
		},
	}
	cmd.Flags().StringVar(&codeRoot, "code-root", "", "code-root under which the scope is ambient")
	return cmd
}
