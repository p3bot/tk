package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/depgate"
	"github.com/p3bot/tk/internal/reconcile"
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
			"start-work write: on a tk-driven git-root with an upstream the board is refreshed\n" +
			"first, then the first still-eligible candidate is set to in-progress, self-committed,\n" +
			"and pushed. Without an upstream the claim stays a local write and self-commit.",
		Args: noArgs(),
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
	gate, err := depgate.Load(e.gateDeps(), res, targets)
	if err != nil {
		return err
	}

	candidates, err := e.db.NextCandidates(scope)
	if err != nil {
		return err
	}
	sel := gate.SelectNext(candidates, e.reg.Lens[scope], noLens)
	writeNextDiagnostics(c, sel)

	if sel.Chosen != nil {
		stdoutln(c, sel.Chosen.Path)
		return nil
	}
	return &ExitError{Code: exitFailure, Plain: true, Err: sel.EmptyQueueError()}
}

func writeNextDiagnostics(c *cobra.Command, s depgate.Selection) {
	if s.ApplyLens {
		stderrln(c, lensEcho(s.Lens))
	}
	for _, line := range s.Tokens {
		stderrln(c, line)
	}
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

// reconcileClosureResult omits printing so callers can refuse without double-echoing.
func (e *engine) reconcileClosureResult(ambient, dir string) (*reconcile.Result, []string, error) {
	return e.rec.ReconcileClosure(e.reg, ambient, dir, nowNS())
}
