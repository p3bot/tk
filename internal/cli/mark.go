package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/id"
	"github.com/p3bot/tk/internal/status"
	"github.com/p3bot/tk/internal/writeengine"
)

func newMarkCmd(app *App) *cobra.Command {
	var scope string
	cmd := &cobra.Command{
		Use:   "mark <status> <id> [id...] [--scope S]",
		Short: "Mark ticket status (blocked / done / in-progress / …)",
		Long: "Rewrite one or more tickets to the same status. The first positional is the\n" +
			"status; ids are the repeating tail (same shape as `tk list todo`). When the new\n" +
			"status crosses the terminal boundary (non-terminal ↔ terminal) each file is\n" +
			"renamed between the dir root and archive/ in the same write, and the post-move\n" +
			"absolute path is printed, one line per unique ticket in first-seen argv order.\n" +
			"Statuses are labels: any known status (built-in or CUE custom) is accepted; an\n" +
			"unknown first token is a usage error. The first id selects the scope; later ids\n" +
			"are looked up there. A later full id in another registered scope is a usage\n" +
			"error and writes nothing. Repeated names of the same ticket collapse to one\n" +
			"write. Mark never enforces depends (next/claim still gate on them); a soft\n" +
			"depends_open: warning is emitted per ticket when that ticket's status actually\n" +
			"changes into todo, in-progress, or review while depends remain unmet. When the\n" +
			"status actually changes into built-in done, a soft required_missing: warning is\n" +
			"emitted if any scope-declared required fields are absent or empty (same-status\n" +
			"re-mark and cancelled stay quiet). An auto-commit scope self-commits every\n" +
			"touched path in one commit when a git-root exists. todo → in-progress on a\n" +
			"tk-driven git-root with an upstream is a claim: one refresh of that root, re-check\n" +
			"each named todo is still todo, write every member, then one push. A quarantined\n" +
			"or duplicate-id ticket is refused with no writes.\n" +
			"For a scope pulse (counts, next, integrity), use `tk status`.",
		Args: arity(1, -1, []string{"<status>", "<id>"}),
		RunE: func(c *cobra.Command, args []string) error {
			return runMark(app, c, args[0], args[1:], scope)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "ambient scope for a short id")
	return cmd
}

func runMark(app *App, c *cobra.Command, newStatus string, idArgs []string, scopeFlag string) error {
	if len(idArgs) == 0 {
		return markOneArgUsage(app, c, newStatus, scopeFlag)
	}

	e, err := app.openEngine(c)
	if err != nil {
		return err
	}
	defer e.close()

	firstForm, firstOK := parseIDArg(idArgs[0])
	if unknownStatusUsage(newStatus, markCueStatuses(e, newStatus, idArgs[0], firstForm, firstOK, scopeFlag)) {
		return usageErrorf("%q is not a known status", newStatus)
	}
	if !firstOK {
		return usageErrorf("%q is not a valid ticket id", idArgs[0])
	}

	scope, err := e.scopeForID(idArgs[0], firstForm, scopeFlag)
	if err != nil {
		return err
	}
	entry, registered := e.reg.Scopes[scope]
	if !registered {
		return fmt.Errorf("unknown ticket id %q: scope %q is not registered here", idArgs[0], scope)
	}
	dir := entry.Dir

	lookups := make([]writeengine.Lookup, 0, len(idArgs))
	for i, idArg := range idArgs {
		form, ok := parseIDArg(idArg)
		if !ok {
			return usageErrorf("%q is not a valid ticket id", idArg)
		}
		if i > 0 && form == id.FormFull {
			prefix := id.ScopeOfFullID(idArg)
			if prefix != scope {
				if _, other := e.reg.Scopes[prefix]; other {
					return usageErrorf("ticket %q is in scope %q; mark applies to one scope (%q)", idArg, prefix, scope)
				}
			}
		}
		lu, err := e.writeLookup(scope, idArg, form)
		if err != nil {
			return err
		}
		lookups = append(lookups, lu)
	}

	res, err := writeengine.Mark(e.writeDeps(c.Context()), claimReporter{c: c}, writeengine.MarkInput{
		Scope:     scope,
		Dir:       dir,
		Lookups:   lookups,
		NewStatus: newStatus,
	})
	return emitWriteResult(c, res, err)
}

func markOneArgUsage(app *App, c *cobra.Command, newStatus, scopeFlag string) error {
	if status.IsBuiltin(newStatus) {
		return markMissingID(c, newStatus)
	}
	e, err := app.openEngine(c)
	if err != nil {
		return err
	}
	defer e.close()
	if unknownStatusUsage(newStatus, markCueStatuses(e, newStatus, "", 0, false, scopeFlag)) {
		return usageErrorf("%q is not a known status", newStatus)
	}
	return markMissingID(c, newStatus)
}

func markMissingID(c *cobra.Command, newStatus string) error {
	return usageErrorf("%s", arityMessage(c, []string{newStatus}, 2, []string{"<status>", "<id>"}))
}

type cueStatusLookup struct {
	custom   map[string]status.Category
	loaded   bool
	unusable bool
}

// unknownStatusUsage is the CLI unknown-status verdict. An unusable tk.cue is
// not a verdict: Mark refuses with config_unparseable:. No scope dir means
// non-built-ins are unknown. A loaded schema is the source of IsKnown.
func unknownStatusUsage(newStatus string, lu cueStatusLookup) bool {
	if lu.unusable {
		return false
	}
	if lu.loaded {
		return !status.IsKnown(newStatus, lu.custom)
	}
	return !status.IsBuiltin(newStatus)
}

// markCueStatuses is a best-effort CUE read so a first token can be accepted
// when it is a declared custom. It must not fail the command (old
// `tk mark <id> draft` would otherwise resolve draft as a short id and demand
// ambient). Prefer the first id's scope; fall back to the first token's full-id
// prefix so a mistaken id-first argv still loads that ticket's schema.
func markCueStatuses(e *engine, newStatus, firstID string, firstForm id.Form, firstOK bool, scopeFlag string) cueStatusLookup {
	dir := markCueDir(e, newStatus, firstID, firstForm, firstOK, scopeFlag)
	if dir.dir == "" {
		return cueStatusLookup{}
	}
	schema, cfgErr := e.rec.SchemaOrError(dir.scope, dir.dir)
	if cfgErr != nil {
		return cueStatusLookup{unusable: true}
	}
	if schema == nil {
		return cueStatusLookup{}
	}
	return cueStatusLookup{custom: schema.CustomStatuses(), loaded: true}
}

type cueDir struct {
	scope, dir string
}

func markCueDir(e *engine, newStatus, firstID string, firstForm id.Form, firstOK bool, scopeFlag string) cueDir {
	if firstOK {
		if firstForm == id.FormFull {
			if d := registeredDir(e, id.ScopeOfFullID(firstID)); d.dir != "" {
				return d
			}
		} else if resolved, err := e.resolveAmbient(scopeFlag); err == nil {
			return cueDir{scope: resolved.Name, dir: resolved.Entry.Dir}
		}
	} else if resolved, err := e.resolveAmbient(scopeFlag); err == nil {
		return cueDir{scope: resolved.Name, dir: resolved.Entry.Dir}
	}
	if form, ok := parseIDArg(newStatus); ok && form == id.FormFull {
		return registeredDir(e, id.ScopeOfFullID(newStatus))
	}
	return cueDir{}
}

func registeredDir(e *engine, scope string) cueDir {
	if entry, ok := e.reg.Scopes[scope]; ok {
		return cueDir{scope: scope, dir: entry.Dir}
	}
	return cueDir{}
}
