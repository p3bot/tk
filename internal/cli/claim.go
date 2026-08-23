package cli

import (
	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/writeengine"
)

// claimReporter sends every syncengine line to stderr so claim stdout stays the path.
type claimReporter struct{ c *cobra.Command }

func (r claimReporter) Out(line string) { stderrln(r.c, line) }
func (r claimReporter) Err(line string) { stderrln(r.c, line) }

func runClaim(app *App, c *cobra.Command, scopeFlag string, noLens bool) error {
	e, err := app.openEngine(c)
	if err != nil {
		return err
	}
	defer e.close()

	resolved, err := e.resolveAmbient(scopeFlag)
	if err != nil {
		return err
	}
	res, err := writeengine.Claim(e.writeDeps(c.Context()), claimReporter{c: c}, writeengine.ClaimInput{
		Kind:   writeengine.ClaimNext,
		Scope:  resolved.Name,
		Dir:    resolved.Entry.Dir,
		NoLens: noLens,
	})
	return emitWriteResult(c, res, err)
}
