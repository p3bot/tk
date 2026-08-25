package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/index"
)

func newSearchCmd(app *App) *cobra.Command {
	var scope string
	cmd := &cobra.Command{
		Use:     "search <terms> [--scope S]",
		Aliases: []string{"find"},
		Short:   "Full-text search over titles and bodies (FTS5, bm25)",
		Long: "Search every scope's titles and bodies, machine-wide by default or bounded by\n" +
			"--scope. Results are ranked bm25 (best first), tie-broken by full id, and\n" +
			"include archived terminals and parse_error rows. One TSV line per hit:\n" +
			"  <full-id>\\t<status>\\t<title>\\t<summary>\\t<absolute-path>\n" +
			"A parse_error hit has an empty status but a filled path so repair stays\n" +
			"discoverable. No lens, no status filter. Empty result exits 0. Pure read.\n" +
			"Alias: find.",
		Args: anyArgs(),
		RunE: func(c *cobra.Command, args []string) error {
			return runSearch(app, c, args, scope)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "bound the search to one scope")
	return cmd
}

func runSearch(app *App, c *cobra.Command, args []string, scope string) error {
	terms := strings.TrimSpace(strings.Join(args, " "))
	if terms == "" {
		return usageErrorf("search needs at least one term")
	}

	e, err := app.openEngine(c)
	if err != nil {
		return err
	}
	defer e.close()

	targets := e.allTargets()
	if scope != "" {
		entry, ok := e.reg.Scopes[scope]
		if !ok {
			return fmt.Errorf("unknown scope %q", scope)
		}
		targets = map[string]string{scope: entry.Dir}
	}
	if _, err := e.reconcile(c, targets); err != nil {
		return err
	}

	hits, err := e.db.Search(scope, terms)
	if err != nil {
		if errors.Is(err, index.ErrSearchQuery) {
			return fmt.Errorf("invalid search query %q: terms are FTS5 syntax — balance quotes and operators (e.g. \"exact phrase\", prefix*, a OR b)", terms)
		}
		return fmt.Errorf("search: %w", err)
	}
	for _, h := range hits {
		p := h.Ticket
		stdoutln(c, tsvLine(p.ID, p.Status, p.Title, p.Summary, p.Path))
	}
	return nil
}
