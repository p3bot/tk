package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/writeengine"
)

type orderDest struct {
	before string
	after  string
	first  bool
	last   bool
}

func (d orderDest) count() int {
	n := 0
	if d.before != "" {
		n++
	}
	if d.after != "" {
		n++
	}
	if d.first {
		n++
	}
	if d.last {
		n++
	}
	return n
}

func newOrderCmd(app *App) *cobra.Command {
	var (
		scope string
		dest  orderDest
	)
	cmd := &cobra.Command{
		Use:   "order <id> (--before <id> | --after <id> | --first | --last) [--scope S]",
		Short: "Rewrite a ticket's order key to move it in the board",
		Long: "Move a ticket by writing a new order key strictly between its target\n" +
			"neighbours — a single-file write that never renumbers a band. --first/--last\n" +
			"place against the scope-wide minimum/maximum valid order (every status, dir root\n" +
			"and archive/); --before/--after name an in-scope neighbour that must exist and\n" +
			"carry a valid order. Exactly one destination is required. An auto-commit scope\n" +
			"self-commits the change when a git-root exists.",
		Args: exactArgs("<id>"),
		RunE: func(c *cobra.Command, args []string) error {
			return runOrder(app, c, args[0], dest, scope)
		},
	}
	cmd.Flags().StringVar(&dest.before, "before", "", "place immediately before this neighbour id")
	cmd.Flags().StringVar(&dest.after, "after", "", "place immediately after this neighbour id")
	cmd.Flags().BoolVar(&dest.first, "first", false, "place at the scope-wide front")
	cmd.Flags().BoolVar(&dest.last, "last", false, "place at the scope-wide back")
	cmd.Flags().StringVar(&scope, "scope", "", "ambient scope for a short id")
	return cmd
}

func runOrder(app *App, c *cobra.Command, idArg string, dest orderDest, scopeFlag string) error {
	if dest.count() != 1 {
		return usageErrorf("order needs exactly one of --before, --after, --first, --last")
	}
	form, ok := parseIDArg(idArg)
	if !ok {
		return usageErrorf("%q is not a valid ticket id", idArg)
	}

	e, err := app.openEngine(c)
	if err != nil {
		return err
	}
	defer e.close()

	scope, err := e.scopeForID(idArg, form, scopeFlag)
	if err != nil {
		return err
	}
	entry, registered := e.reg.Scopes[scope]
	if !registered {
		return fmt.Errorf("unknown ticket id %q: scope %q is not registered here", idArg, scope)
	}
	lu, err := e.writeLookup(scope, idArg, form)
	if err != nil {
		return err
	}

	wd := writeengine.Dest{First: dest.first, Last: dest.last}
	if dest.before != "" {
		nform, ok := parseIDArg(dest.before)
		if !ok {
			return usageErrorf("%q is not a valid ticket id", dest.before)
		}
		nlu, err := e.writeLookup(scope, dest.before, nform)
		if err != nil {
			return err
		}
		wd.Before = nlu
	}
	if dest.after != "" {
		nform, ok := parseIDArg(dest.after)
		if !ok {
			return usageErrorf("%q is not a valid ticket id", dest.after)
		}
		nlu, err := e.writeLookup(scope, dest.after, nform)
		if err != nil {
			return err
		}
		wd.After = nlu
	}

	res, err := writeengine.Order(e.writeDeps(c.Context()), writeengine.OrderInput{
		Scope:  scope,
		Dir:    entry.Dir,
		Lookup: lu,
		Dest:   wd,
	})
	return emitWriteResult(c, res, err)
}
