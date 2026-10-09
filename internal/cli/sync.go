package cli

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/resolve"
	"github.com/p3bot/tk/internal/syncengine"
)

// newSyncCmd pushes an auto-commit git-root. Claim (todo→in-progress on a
// tk-driven root with an upstream) also refreshes and pushes that root.
func newSyncCmd(app *App) *cobra.Command {
	var scope string
	var all bool
	cmd := &cobra.Command{
		Use:   "sync [--scope S] [--all]",
		Short: "Fetch, integrate, and push an auto-commit git-root",
		Long: "Sync fetches, then classifies the checkout against that upstream. When\n" +
			"the checkout is already based on it, including equal, sync does not rebase.\n" +
			"A strictly-behind checkout fast-forwards, keeping unrelated local edits.\n" +
			"Diverged history rebases in place. `tk sync` and `tk sync --scope S` commit\n" +
			"that scope's allowlisted dirty files: after the fast-forward, before a\n" +
			"diverged rebase, or instead of a rebase when already based on upstream.\n" +
			"`tk sync --all`, and a bare sync with no ambient scope, commits nothing and\n" +
			"still integrates and pushes each auto-commit root. --all wins over\n" +
			"--scope/TK_SCOPE. Each root is an independent unit. It applies only to\n" +
			"auto-commit scopes. On a tk-driven git-root with an upstream, `tk next --claim`\n" +
			"and `tk mark` todo → in-progress refresh that root without committing and push\n" +
			"after the write. Never host-push an auto-commit root. A non-auto-commit scope\n" +
			"is refused (ambient) or skipped (--all); an empty auto-commit set exits 0.",
		Args: noArgs(),
		RunE: func(c *cobra.Command, _ []string) error {
			return runSync(app, c, scope, all)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "ambient scope whose git-root is targeted (ignored under --all)")
	cmd.Flags().BoolVar(&all, "all", false, "sync every auto-commit git-root (wins over --scope/TK_SCOPE)")
	return cmd
}

func runSync(app *App, c *cobra.Command, scopeFlag string, all bool) error {
	e, err := app.openEngine(c)
	if err != nil {
		return err
	}
	defer e.close()

	in, err := e.syncInput(scopeFlag, all)
	if err != nil {
		return err
	}

	result, err := syncengine.Run(e.syncDeps(c), cobraReporter{c: c}, in)
	if err != nil {
		return err
	}
	if result.NeedsAttention {
		return &ExitError{Code: exitFailure, Plain: true, Err: syncengine.ErrNeedsAttention}
	}
	return nil
}

// syncInput maps the three CLI invocation shapes onto the two package inputs.
// Ambient success → ambient; --all or resolve.ErrNoScope → all-registered;
// other resolve errors fail here before the package runs.
func (e *engine) syncInput(scopeFlag string, all bool) (syncengine.Input, error) {
	if all {
		return syncengine.Input{AllRegistered: true}, nil
	}
	resolved, err := e.resolveAmbient(scopeFlag)
	switch {
	case err == nil:
		return syncengine.Input{Ambient: &syncengine.AmbientScope{
			Name: resolved.Name,
			Dir:  resolved.Entry.Dir,
		}}, nil
	case errors.Is(err, resolve.ErrNoScope):
		return syncengine.Input{AllRegistered: true}, nil
	default:
		return syncengine.Input{}, err
	}
}
