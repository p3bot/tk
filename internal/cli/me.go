package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/registry"
	"github.com/p3bot/tk/internal/xdg"
)

func newMeCmd(app *App) *cobra.Command {
	var (
		scope   string
		clearMe bool
	)
	cmd := &cobra.Command{
		Use:   "me [id] | --clear [--scope S]",
		Short: "Set, show, or clear the machine-local current-ticket pointer",
		Long: "A per-scope, machine-local current-ticket pointer. With an id, it sets the\n" +
			"pointer to that ticket's full id; with --clear it removes it; with no arguments\n" +
			"it prints that stored full id. A missing or deleted ticket is unknown (same as\n" +
			"get). The reserved word me then resolves as that pointer in every command that\n" +
			"accepts a ticket id. Unset show is empty stdout, exit 0. The pointer is XDG\n" +
			"only: it never writes tk.cue, never self-commits, and is not touched by claim,\n" +
			"mark, or create. --clear takes no id.",
		Args: usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(c *cobra.Command, args []string) error {
			return runMe(app, c, args, scope, clearMe)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "scope to set the pointer for (defaults to ambient)")
	cmd.Flags().BoolVar(&clearMe, "clear", false, "clear the pointer for the scope")
	return cmd
}

func runMe(app *App, c *cobra.Command, args []string, scopeFlag string, clearMe bool) error {
	if clearMe && len(args) > 0 {
		return usageErrorf("--clear takes no id")
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
	case clearMe:
		return e.writeMe(scope, "")
	case len(args) == 0:
		if e.reg.Me[scope] == "" {
			return nil
		}
		return showMe(c, e, scopeFlag)
	default:
		return e.setMe(c, scope, scopeFlag, args[0])
	}
}

func (e *engine) setMe(c *cobra.Command, scope, scopeFlag, idArg string) error {
	form, ok := parseIDArg(idArg)
	if !ok {
		return usageErrorf("%q is not a valid ticket id", idArg)
	}
	if form == idMe {
		return usageErrorf("cannot set me to %q", reservedMe)
	}

	r, err := e.resolveTicket(c, idArg, scopeFlag)
	if err != nil {
		return err
	}
	if len(r.rows) > 1 {
		return duplicateRefusal(r.rows)
	}
	p := r.rows[0]
	if err := ensureFileExists(p); err != nil {
		return err
	}
	if r.scope != scope {
		return fmt.Errorf("ticket %s is not in scope %s", p.ID, scope)
	}
	return e.writeMe(scope, p.ID)
}

func showMe(c *cobra.Command, e *engine, scopeFlag string) error {
	r, err := e.resolveTicket(c, reservedMe, scopeFlag)
	if err != nil {
		return err
	}
	if len(r.rows) > 1 {
		return duplicateRefusal(r.rows)
	}
	p := r.rows[0]
	if err := ensureFileExists(p); err != nil {
		return err
	}
	stdoutln(c, p.ID)
	return nil
}

// writeMe: machine-global flock spans load-modify-write.
func (e *engine) writeMe(scope, fullID string) error {
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
	if reg.Me == nil {
		reg.Me = map[string]string{}
	}
	if fullID == "" {
		delete(reg.Me, scope)
	} else {
		reg.Me[scope] = fullID
	}
	return store.WriteMe(reg.Me)
}
