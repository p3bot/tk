package cli

import (
	"errors"
	"sort"

	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/integrity"
	"github.com/p3bot/tk/internal/resolve"
)

func newDoctorCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose integrity across scopes",
		Long: "Diagnose every integrity class over the ambient scope (or every registered\n" +
			"scope when there is none), reporting each with its stable token. Never mutates\n" +
			"ticket files or tk.cue. There is no --scope flag on doctor. Repair files with\n" +
			"tk repair. Rebuild the derived index with tk reindex.",
		Args: noArgs(),
		RunE: func(c *cobra.Command, _ []string) error {
			return runDoctor(app, c)
		},
	}
	return cmd
}

func runDoctor(app *App, c *cobra.Command) error {
	e, err := app.openEngine(c)
	if err != nil {
		return err
	}
	defer e.close()

	reportScopes, err := e.doctorScopes()
	if err != nil {
		return err
	}

	deps := e.integrityDeps(c)
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

// doctorScopes: ambient when present, otherwise every registered scope.
func (e *engine) doctorScopes() ([]string, error) {
	name, ok, err := e.ambientScope()
	if err != nil {
		return nil, err
	}
	if ok {
		return []string{name}, nil
	}
	return e.sortedRegistered(), nil
}

func (e *engine) ambientScope() (name string, ok bool, err error) {
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
