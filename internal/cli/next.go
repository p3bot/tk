package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/reconcile"
	"github.com/p3bot/tk/internal/status"
)

func newNextCmd(app *App) *cobra.Command {
	var (
		scope  string
		noLens bool
		claim  bool
	)
	cmd := &cobra.Command{
		Use:   "next [--scope S] [--no-lens] [--claim]",
		Short: "Print the path of the first runnable ticket",
		Long: "Select the first runnable ticket by (order, id): built-in todo, every\n" +
			"depends terminal, honouring the lens, file at the dir root (never archive/),\n" +
			"and not a duplicate-id collision. Print its absolute path. An empty queue is\n" +
			"diagnosed distinctly: blocked-by-deps vs genuinely empty vs lens-emptied.\n" +
			"Without --claim it is a pure read that never runs git. With --claim it is the\n" +
			"start-work write: the first still-eligible candidate is set to in-progress under\n" +
			"the scope flock and self-committed on an auto-commit scope with a git-root.",
		Args: usageArgs(cobra.NoArgs),
		RunE: func(c *cobra.Command, _ []string) error {
			if claim {
				return runClaim(app, c, scope, noLens)
			}
			return runNext(app, c, scope, noLens)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "ambient inventory scope")
	cmd.Flags().BoolVar(&noLens, "no-lens", false, "ignore the active lens for this invocation")
	cmd.Flags().BoolVar(&claim, "claim", false, "claim the selected ticket (set in-progress)")
	return cmd
}

func runNext(app *App, c *cobra.Command, scopeFlag string, noLens bool) error {
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

	res, targets, err := e.reconcileClosure(c, scope, resolved.Entry.Dir)
	if err != nil {
		return err
	}
	gate, err := e.buildGate(res, targets)
	if err != nil {
		return err
	}

	rows, err := e.db.ScopeTickets(scope)
	if err != nil {
		return err
	}
	sel := selectNext(gate, rows, e.reg.Lens[scope], noLens)
	sel.writeDiagnostics(c)

	if sel.Chosen != nil {
		stdoutln(c, sel.Chosen.Path)
		return nil
	}
	return emptyQueueError(sel.ApplyLens, sel.Lens, sel.Blocked, sel.ReadyOutsideLens)
}

// nextSelection is shared by status and next so eligibility/tokens cannot fork.
type nextSelection struct {
	Chosen           *index.Ticket
	Tokens           []string
	Blocked          int
	ReadyOutsideLens int
	ApplyLens        bool
	Lens             []string
}

// selectNext: callers own empty-queue policy (next refuses; status emits next\t).
func selectNext(gate *gate, rows []*index.Ticket, lens []string, noLens bool) nextSelection {
	candidates := nextCandidates(rows)
	sortTickets(candidates)
	applyLens := !noLens && len(lens) > 0

	tokens := newTokenSet()
	var chosen *index.Ticket
	blocked, readyOutsideLens := 0, 0
	for _, p := range candidates {
		ds := gate.evalDepends(p)
		tokens.add(ds.Tokens)
		if !gate.nextEligible(p, ds) {
			// Held drives blocked diagnostic; duplicate-id skip does not.
			if ds.Held() {
				blocked++
			}
			continue
		}
		if applyLens && !passesLens(p, lens) {
			readyOutsideLens++
			continue
		}
		if chosen == nil {
			chosen = p
		}
	}
	return nextSelection{
		Chosen:           chosen,
		Tokens:           tokens.lines(),
		Blocked:          blocked,
		ReadyOutsideLens: readyOutsideLens,
		ApplyLens:        applyLens,
		Lens:             lens,
	}
}

func (s nextSelection) writeDiagnostics(c *cobra.Command) {
	if s.ApplyLens {
		stderrln(c, lensEcho(s.Lens))
	}
	for _, line := range s.Tokens {
		stderrln(c, line)
	}
}

func nextCandidates(rows []*index.Ticket) []*index.Ticket {
	var out []*index.Ticket
	for _, p := range rows {
		if status.IsNextEligible(p.Status) && !p.Archived && !p.ParseError {
			out = append(out, p)
		}
	}
	return out
}

func emptyQueueError(applyLens bool, lens []string, blocked, readyOutsideLens int) error {
	switch {
	case applyLens && readyOutsideLens > 0:
		return plainDiagnostic("nothing ready under lens %s; %d ready outside it", lensBracket(lens), readyOutsideLens)
	case blocked > 0:
		return plainDiagnostic("nothing ready; %d todo(s) waiting on unmet deps", blocked)
	default:
		return plainDiagnostic("nothing ready")
	}
}

func plainDiagnostic(format string, a ...any) error {
	return &ExitError{Code: exitFailure, Err: fmt.Errorf(format, a...), Plain: true}
}

func lensBracket(lens []string) string {
	return "[" + strings.Join(lens, ", ") + "]"
}

// reconcileClosure refreshes ambient plus transitive depends scopes (single-pass aggregates).
func (e *engine) reconcileClosure(c *cobra.Command, ambient, dir string) (*reconcile.Result, []string, error) {
	merged, names, err := e.reconcileClosureResult(ambient, dir)
	if err != nil {
		return nil, nil, err
	}
	e.printWarnings(c, merged.Warnings)
	return merged, names, nil
}

// reconcileClosureResult omits printing so claim can refuse without double-echoing.
func (e *engine) reconcileClosureResult(ambient, dir string) (*reconcile.Result, []string, error) {
	targets := map[string]string{ambient: dir}
	done := map[string]bool{}
	merged := reconcile.NewResult()
	var reconciled []string
	for {
		pending := map[string]string{}
		for name, d := range targets {
			if !done[name] {
				pending[name] = d
			}
		}
		if len(pending) == 0 {
			break
		}
		res, batch, err := e.rec.ReconcileRows(pending, e.registeredSet(), nowNS())
		if err != nil {
			return nil, nil, err
		}
		merged.Merge(res)
		reconciled = append(reconciled, batch...)
		for name := range pending {
			done[name] = true
		}
		edges, err := e.db.AllEdges()
		if err != nil {
			return nil, nil, err
		}
		for _, ed := range edges {
			if ed.Kind != index.EdgeDepends || !done[ed.FromScope] {
				continue
			}
			if entry, ok := e.reg.Scopes[ed.ToScope]; ok && !done[ed.ToScope] {
				targets[ed.ToScope] = entry.Dir
			}
		}
	}

	if err := e.rec.AppendAggregates(reconciled, merged); err != nil {
		return nil, nil, err
	}

	names := make([]string, 0, len(targets))
	for name := range targets {
		names = append(names, name)
	}
	return merged, names, nil
}
