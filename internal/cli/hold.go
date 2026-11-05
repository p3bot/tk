package cli

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/scopeconfig"
	"github.com/p3bot/tk/internal/token"
)

// warnHold prints the scope_hold line when <dir>'s evaluated tk.cue has a hold.
// A second call for the same scope in this command is quiet. Load failure
// prints nothing: config_unparseable owns that scope. Inside a hold batch the
// row waits for warnHoldSet to print the block.
func (e *engine) warnHold(c *cobra.Command, scope, dir string) {
	if c == nil || e == nil || scope == "" || dir == "" {
		return
	}
	if _, seen := e.holdSeen[scope]; seen {
		return
	}
	schema, err := scopeconfig.Load(e.app.Ctx, dir)
	if err != nil {
		return
	}
	h := scopeconfig.HoldOf(schema)
	if h == nil {
		return
	}
	if e.holdSeen == nil {
		e.holdSeen = map[string]scopeconfig.Hold{}
	}
	e.holdSeen[scope] = *h
	if e.holdBatch {
		e.holdPending = append(e.holdPending, token.HoldRow{Scope: scope, Type: h.Type, Reason: h.Reason})
		return
	}
	stderrln(c, token.FormatScopeHold(h.Type, h.Reason))
}

// warnHoldTargets warns for each scope in targets. See warnHoldSet.
func (e *engine) warnHoldTargets(c *cobra.Command, targets map[string]string) {
	e.warnHoldSet(c, scopeNames(targets), targets)
}

// warnHoldSet warns for names. One scope prints the single line. More than one
// prints the block when at least one is held.
func (e *engine) warnHoldSet(c *cobra.Command, names []string, dirs map[string]string) {
	e.holdBatch = len(names) > 1
	e.holdMulti = e.holdMulti || e.holdBatch
	for _, name := range names {
		e.warnHold(c, name, dirs[name])
	}
	e.holdBatch = false
	if len(e.holdPending) == 0 {
		return
	}
	stderrln(c, token.FormatScopeHolds(e.holdPending))
	e.holdPending = nil
}

// holdExit maps a hold refusal to exit 1. The line is printed here only when
// this command has not already reported that state of that scope. A hold that
// landed or changed after resolve has not been reported, and it is why this failed.
// A multi-scope command prints it as the block, so the held scope is named.
func holdExit(e *engine, err error) error {
	var hold *scopeconfig.HoldError
	if !errors.As(err, &hold) {
		return err
	}
	warned := false
	var out error = hold
	if e != nil {
		seen, ok := e.holdSeen[hold.Scope]
		warned = ok && seen == hold.Hold
		if e.holdMulti {
			out = &holdBlockError{hold: hold}
		}
	}
	return &ExitError{Code: exitFailure, Plain: true, AlreadyPrinted: warned, Err: out}
}

// holdBlockError renders a hold refusal as the one-row scope_hold: block.
type holdBlockError struct {
	hold *scopeconfig.HoldError
}

func (e *holdBlockError) Error() string {
	return token.FormatScopeHolds([]token.HoldRow{{Scope: e.hold.Scope, Type: e.hold.Hold.Type, Reason: e.hold.Hold.Reason}})
}

func (e *holdBlockError) Unwrap() error { return e.hold }

// refuseEditorHold stops tk edit and tk design edit on a hard scope. They hold
// no lock while the editor runs, so this check cannot live in an engine write.
// An unusable tk.cue does not refuse: the editor may be how it gets repaired.
func (e *engine) refuseEditorHold(scope, dir string) error {
	schema, cfgErr := e.rec.SchemaOrError(dir)
	if cfgErr != nil {
		return nil
	}
	return holdExit(e, scopeconfig.RefuseHard(scope, schema))
}

// warnRebindHold warns once when the scope's current directory or the directory
// being bound is on hold. One scope, so this is the single line. An unknown name
// still warns from dir when that directory is held.
func warnRebindHold(app *App, c *cobra.Command, name, dir string) error {
	e, err := app.openEngine(c)
	if err != nil {
		return err
	}
	defer e.close()
	if entry, ok := e.reg.Scopes[name]; ok {
		e.warnHold(c, name, entry.Dir)
	}
	e.warnHold(c, name, dir)
	return nil
}

// warnRegisteredHold prints scope_hold when name is a registered held scope.
// An unknown name stays quiet so the verb can report its own error.
func warnRegisteredHold(app *App, c *cobra.Command, name string) error {
	e, err := app.openEngine(c)
	if err != nil {
		return err
	}
	defer e.close()
	if entry, ok := e.reg.Scopes[name]; ok {
		e.warnHold(c, name, entry.Dir)
	}
	return nil
}
