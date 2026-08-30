package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/registry"
	"github.com/p3bot/tk/internal/xdg"
)

func newLensCmd(app *App) *cobra.Command {
	var (
		scope     string
		clearLens bool
	)
	cmd := &cobra.Command{
		Use:   "lens [tags...] | --clear [--scope S]",
		Short: "Set, show, or clear the machine-local default tag view for a scope",
		Long: "A lens is a per-scope, machine-local default tag view. With tags, it sets the\n" +
			"lens; with --clear it removes it; with no arguments it shows the current lens.\n" +
			"list and next apply the lens by default (an untagged ticket is never hidden;\n" +
			"list --no-lens / next --no-lens bypass for one invocation without clearing).\n" +
			"On list, --tag is a hard membership filter and ignores the lens for that\n" +
			"invocation. Tags are free-form; any tag is a legal lens value. Setting a tag\n" +
			"not yet used on any ticket in the scope still applies the lens and emits on\n" +
			"stderr (soft; exit 0):\n" +
			"  tag_unknown: \"<t>\" is not used on any ticket in this scope",
		Args: anyArgs(),
		RunE: func(c *cobra.Command, args []string) error {
			return runLens(app, c, args, scope, clearLens)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "scope to set the lens for (defaults to ambient)")
	cmd.Flags().BoolVar(&clearLens, "clear", false, "clear the lens for the scope")
	return cmd
}

func runLens(app *App, c *cobra.Command, args []string, scopeFlag string, clearLens bool) error {
	if clearLens && len(args) > 0 {
		return usageErrorf("--clear takes no tags")
	}

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

	switch {
	case clearLens:
		return e.writeLens(scope, nil)
	default:
		tags := registry.CompactTags(args)
		if len(tags) == 0 {
			stdoutln(c, strings.Join(e.reg.Lens[scope], " "))
			return nil
		}
		// Refresh index without printing integrity tokens — lens set's soft
		// stderr surface is tag_unknown: only (not board-verb noise).
		if _, err := e.reconcileResult(map[string]string{scope: resolved.Entry.Dir}); err != nil {
			return err
		}
		inUse, err := e.db.ScopeTagMembership(scope)
		if err != nil {
			return err
		}
		if err := e.writeLens(scope, tags); err != nil {
			return err
		}
		// Soft only after a successful write (same as meta tag_new:).
		warnUnknownTags(c, tags, inUse)
		return nil
	}
}

// writeLens: machine-global flock spans load-modify-write.
func (e *engine) writeLens(scope string, tags []string) error {
	lock, err := xdg.AcquireConfigLock(e.app.ConfigDir)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()

	store := registry.NewStore(e.app.Ctx, e.app.ConfigDir)
	return store.SetLens(scope, tags)
}

// lensEcho rides stderr only — never a TSV stdout field.
func lensEcho(lens []string) string {
	return "lens: " + lensBracket(lens)
}
