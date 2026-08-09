package cli

import (
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/index"
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
			"--no-lens bypasses). Tags are free-form; any tag is a legal lens value.\n" +
			"Setting a tag not yet used on any ticket in the scope still applies the lens\n" +
			"and emits on stderr (soft; exit 0):\n" +
			"  tag_unknown: \"<t>\" is not used on any ticket in this scope",
		Args: usageArgs(cobra.ArbitraryArgs),
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
	case len(args) == 0:
		stdoutln(c, strings.Join(e.reg.Lens[scope], " "))
		return nil
	default:
		tags := dedupeSorted(args)
		// Refresh index without printing integrity tokens — lens set's soft
		// stderr surface is tag_unknown: only (not board-verb noise).
		if _, err := e.reconcileResult(map[string]string{scope: resolved.Entry.Dir}); err != nil {
			return err
		}
		rows, err := e.db.ScopeTickets(scope)
		if err != nil {
			return err
		}
		inUse := index.TagMembership(rows)
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
	reg, err := store.Load()
	if err != nil {
		return err
	}
	if reg.Lens == nil {
		reg.Lens = map[string][]string{}
	}
	if len(tags) == 0 {
		delete(reg.Lens, scope)
	} else {
		reg.Lens[scope] = tags
	}
	return store.WriteLens(reg.Lens)
}

// passesLens: empty lens shows all; untagged tickets are never hidden.
func passesLens(p *index.Ticket, lens []string) bool {
	if len(lens) == 0 || len(p.Tags) == 0 {
		return true
	}
	set := map[string]bool{}
	for _, t := range lens {
		set[t] = true
	}
	for _, t := range p.Tags {
		if set[t] {
			return true
		}
	}
	return false
}

// lensEcho rides stderr only — never a TSV stdout field.
func lensEcho(lens []string) string {
	return "lens: " + lensBracket(lens)
}

func dedupeSorted(items []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range items {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
