package cli

import (
	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/id"
	"github.com/p3bot/tk/internal/scopeadmin"
)

func newScopeInitCmd(app *App) *cobra.Command {
	var (
		name       string
		autoName   bool
		codeRoot   string
		autoCommit bool
	)
	cmd := &cobra.Command{
		Use:   "init <dir> (--name <name> | --auto-name) [--code-root <path>] [--auto-commit]",
		Short: "Create and register a new scope",
		Long: "Create a scope directory (if needed), write a minimal tk.cue and a\n" +
			".gitignore covering .tk.lock, and register the scope on this machine.\n\n" +
			"Exactly one of --name or --auto-name is required — the name is never\n" +
			"silently defaulted. The code-root defaults to the enclosing git repo root\n" +
			"inside a repo, else the dir; --code-root overrides it (any path: ambient\n" +
			"cwd match only — git durability always follows the scope dir). In a\n" +
			"dedicated tk repo, pass --auto-commit (omitting it registers repo-driven).\n" +
			"init never prompts and never runs git.",
		Args: exactArgs("<dir>"),
		RunE: func(c *cobra.Command, args []string) error {
			switch {
			case name != "" && autoName:
				return usageErrorf("--name and --auto-name are mutually exclusive")
			case name == "" && !autoName:
				return usageErrorf("provide exactly one of --name or --auto-name")
			case name != "" && !id.IsScopeName(name):
				return usageErrorf("--name %q is not a legal scope name (^[a-z0-9]{1,12}$)", name)
			}

			dir, err := absPath(args[0])
			if err != nil {
				return err
			}
			p := scopeadmin.InitParams{
				Dir:             dir,
				Name:            name,
				AutoName:        autoName,
				AutoCommit:      autoCommit,
				AutoCommitGiven: c.Flags().Changed("auto-commit"),
			}
			if c.Flags().Changed("code-root") {
				cr, err := absPath(codeRoot)
				if err != nil {
					return err
				}
				p.CodeRoot = cr
				p.CodeRootGiven = true
			}

			registered, err := app.admin().Init(p)
			if err != nil {
				return err
			}
			stdoutln(c, registered)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "scope name (^[a-z0-9]{1,12}$)")
	cmd.Flags().BoolVar(&autoName, "auto-name", false, "derive the scope name from the code-root basename")
	cmd.Flags().StringVar(&codeRoot, "code-root", "", "code-root under which the scope is ambient")
	cmd.Flags().BoolVar(&autoCommit, "auto-commit", false, "write autoCommit: true (tk-driven)")
	return cmd
}
