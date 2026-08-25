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
		Short: "Snapshot, fetch, integrate, repair, and push an auto-commit git-root",
		Long: "Sync snapshots allowlisted dirty files, fetches and rebases the remote\n" +
			"in, resolves frontmatter conflicts, runs the sync-time integrity repairs, and\n" +
			"pushes if ahead. It applies only to auto-commit scopes. It is not tk's only\n" +
			"push: on a tk-driven git-root with an upstream, `tk next --claim` and\n" +
			"`tk mark` todo → in-progress refresh that root and push after the write.\n" +
			"Never host-push an auto-commit root. With no flag it targets the ambient\n" +
			"scope's whole git-root; --all (which wins over --scope/TK_SCOPE) syncs every\n" +
			"auto-commit git-root, each an independent unit whose failure never strands\n" +
			"the others. A non-auto-commit scope is refused (ambient) or skipped (--all);\n" +
			"an empty auto-commit set exits 0.",
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
