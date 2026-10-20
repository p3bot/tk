package cli

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/scopeconfig"
	"github.com/p3bot/tk/internal/token"
	"github.com/p3bot/tk/internal/writeengine"
)

// warnHold prints one scope_hold line when <dir>'s evaluated tk.cue has hold
// true. A second call for the same scope in this command is quiet. Load failure
// prints nothing: config_unparseable owns that scope. Inside a hold batch the
// line is held until finishHoldBatch.
func (e *engine) warnHold(c *cobra.Command, scope, dir string) {
	if c == nil || e == nil || scope == "" || dir == "" {
		return
	}
	if e.holdSeen[scope] {
		return
	}
	schema, _, err := scopeconfig.LoadWithClosure(e.app.Ctx, dir)
	if err != nil || !scopeconfig.Held(schema) {
		return
	}
	if e.holdSeen == nil {
		e.holdSeen = map[string]bool{}
	}
	e.holdSeen[scope] = true
	if e.holdBatch {
		e.holdPending = append(e.holdPending, scope)
		return
	}
	name := ""
	if e.holdNamed {
		name = scope
	}
	stderrln(c, token.FormatScopeHold(name))
}

// warnHoldTargets warns for each scope in targets. More than one scope names
// each held scope. Two or more held scopes share one line.
func (e *engine) warnHoldTargets(c *cobra.Command, targets map[string]string) {
	e.warnHoldSet(c, scopeNames(targets), targets)
}

// warnHoldSet warns for names in walk order. One scope in the set stays
// unnamed. More than one scope names each held scope, and two or more held
// scopes share one line.
func (e *engine) warnHoldSet(c *cobra.Command, names []string, dirs map[string]string) {
	if len(names) > 1 {
		e.holdNamed = true
		e.holdBatch = true
	}
	for _, name := range names {
		e.warnHold(c, name, dirs[name])
	}
	e.finishHoldBatch(c)
}

// finishHoldBatch emits the collected scope_hold line. One held scope keeps
// the named sentence. Two or more share one line.
func (e *engine) finishHoldBatch(c *cobra.Command) {
	if e == nil {
		return
	}
	e.holdBatch = false
	if len(e.holdPending) == 0 {
		return
	}
	names := e.holdPending
	e.holdPending = nil
	stderrln(c, token.FormatScopeHolds(names))
}

// holdExit maps a hold refusal to exit 1. The line is printed here only when
// this command has not already printed scope_hold. A hold that was already on
// at resolve time was warned then; a hold that landed during refresh was not.
func holdExit(e *engine, scope string, err error) error {
	var hold *writeengine.HoldError
	if !errors.As(err, &hold) {
		return err
	}
	warned := e != nil && e.holdSeen[scope]
	return &ExitError{Code: exitFailure, Plain: true, AlreadyPrinted: warned, Err: hold}
}

// warnRebindHold warns once when the scope's current directory or the directory
// being bound is on hold. One scope, so the line is unnamed. An unknown name
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
