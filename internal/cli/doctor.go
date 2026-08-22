package cli

import (
	"errors"
	"sort"

	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/integrity"
	"github.com/p3bot/tk/internal/resolve"
)

// doctorFlags: bare doctor diagnoses only; --repair / --re-space-order mutate.
type doctorFlags struct {
	repair       bool
	reSpaceOrder bool
	all          bool
}

func (f doctorFlags) mutating() bool { return f.repair || f.reSpaceOrder }

func (f doctorFlags) integrityFlags() integrity.Flags {
	return integrity.Flags{Repair: f.repair, ReSpaceOrder: f.reSpaceOrder, All: f.all}
}

func newDoctorCmd(app *App) *cobra.Command {
	var f doctorFlags
	cmd := &cobra.Command{
		Use:   "doctor [--repair] [--re-space-order] [--all]",
		Short: "Diagnose integrity, and optionally repair, across scopes",
		Long: "Diagnose every integrity class over the ambient scope (or every registered\n" +
			"scope when there is none), reporting each with its stable token. Bare doctor\n" +
			"never mutates ticket files or tk.cue. --repair fixes id collisions, equal order\n" +
			"keys, and archive layout drift; --re-space-order shortens a band of over-long\n" +
			"order keys; both need a scope (ambient, TK_SCOPE, or --all) and refuse on a\n" +
			"mid-rebase auto-commit git-root. There is no --scope flag on doctor. Rebuild the\n" +
			"derived index with tk reindex.",
		Args: usageArgs(cobra.NoArgs),
		RunE: func(c *cobra.Command, _ []string) error {
			return runDoctor(app, c, f)
		},
	}
	cmd.Flags().BoolVar(&f.repair, "repair", false, "repair id collisions, equal order keys, and archive layout drift")
	cmd.Flags().BoolVar(&f.reSpaceOrder, "re-space-order", false, "re-space a band of pathologically long order keys")
	cmd.Flags().BoolVar(&f.all, "all", false, "act on every registered scope (mutating flags only)")
	return cmd
}

func runDoctor(app *App, c *cobra.Command, f doctorFlags) error {
	e, err := app.openEngine(c)
	if err != nil {
		return err
	}
	defer e.close()

	reportScopes, mutateScopes, err := e.doctorScopes(f.mutating(), f.all)
	if err != nil {
		return err
	}

	deps := e.integrityDeps(c)
	rep := cobraReporter{c: c}

	// Repairs first under flock so chosen rows cannot change between read and write.
	if f.mutating() {
		if err := integrity.RepairScopes(deps, rep, mutateScopes, f.integrityFlags()); err != nil {
			return err
		}
	}

	// Post-repair reconcile feeds the report; doctor prints its own report, so no ride-along echo.
	targets := e.targetsFor(reportScopes)
	res, err := e.reconcileResult(targets)
	if err != nil {
		return err
	}

	report, err := integrity.Diagnose(deps, reportScopes, res)
	if err != nil {
		return err
	}
	for _, line := range report {
		stdoutln(c, line)
	}
	if len(report) == 0 {
		stderrln(c, "tk doctor: no integrity issues found")
	}
	return nil
}

// cobraReporter maps integrity/sync engine progress lines onto cobra stdout/stderr.
type cobraReporter struct{ c *cobra.Command }

func (r cobraReporter) Out(line string) { stdoutln(r.c, line) }
func (r cobraReporter) Err(line string) { stderrln(r.c, line) }

func (e *engine) integrityDeps(c *cobra.Command) integrity.Deps {
	return integrity.Deps{
		Ctx:      c.Context(),
		Cue:      e.app.Ctx,
		StateDir: e.app.StateDir,
		Reg:      e.reg,
		DB:       e.db,
		Rec:      e.rec,
	}
}

// doctorScopes: report is ambient or all; mutate needs ambient/--all (never silent machine-wide).
func (e *engine) doctorScopes(mutating, all bool) (report, mutate []string, err error) {
	name, ok, err := e.ambientForDoctor()
	if err != nil {
		return nil, nil, err
	}
	allRegistered := e.sortedRegistered()

	if ok {
		report = []string{name}
	} else {
		report = allRegistered
	}

	if !mutating {
		return report, nil, nil
	}
	switch {
	case all:
		mutate = allRegistered
	case ok:
		mutate = []string{name}
	default:
		return nil, nil, usageErrorf("tk doctor --repair / --re-space-order needs a scope to act on: run inside a registered code-root, set TK_SCOPE=<name>, or pass --all")
	}
	return report, mutate, nil
}

func (e *engine) ambientForDoctor() (name string, ok bool, err error) {
	opts, err := ambientOptions("")
	if err != nil {
		return "", false, err
	}
	resolved, err := resolve.Resolve(e.app.Ctx, e.reg, opts)
	if err == nil {
		return resolved.Name, true, nil
	}
	var drift *resolve.DriftError
	if errors.As(err, &drift) {
		return drift.Key, true, nil
	}
	if errors.Is(err, resolve.ErrNoScope) {
		return "", false, nil
	}
	return "", false, err
}

func (e *engine) sortedRegistered() []string {
	names := make([]string, 0, len(e.reg.Scopes))
	for name := range e.reg.Scopes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (e *engine) targetsFor(scopes []string) map[string]string {
	out := make(map[string]string, len(scopes))
	for _, s := range scopes {
		if entry, ok := e.reg.Scopes[s]; ok {
			out[s] = entry.Dir
		}
	}
	return out
}
