package cli

import (
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/depgate"
	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/scopeconfig"
	"github.com/p3bot/tk/internal/status"
)

func newListCmd(app *App) *cobra.Command {
	var (
		scope  string
		tags   []string
		all    bool
		noLens bool
	)
	cmd := &cobra.Command{
		Use:   "list [status...] [--scope S] [--tag T]... [--all] [--no-lens]",
		Short: "Board / inventory for one scope as parse-stable TSV",
		Long: "Print one scope's tickets, sorted (order, id), one TSV line each:\n" +
			"  <full-id>\\t<status>\\t<title>\\t<waiting-on>\n" +
			"Headerless TSV (no header row). Summary is not a list column — use\n" +
			"`tk meta get <id>`. Bare list is the default active set. Status positionals\n" +
			"union-filter (an unknown status exits 2) and include matching rows under\n" +
			"archive/ — so `list done` shows done tickets without --all. --tag repeats\n" +
			"as OR among themselves and is always a hard membership filter (ticket must\n" +
			"carry at least one listed tag; untagged rows are out). Any --tag ignores the\n" +
			"lens for that invocation (no echo). Without --tag the lens applies unless\n" +
			"--no-lens. A --tag value not used on any ticket still filters (possibly empty)\n" +
			"and emits on stderr (soft; exit 0):\n" +
			"  tag_unknown: \"<t>\" is not used on any ticket in this scope\n" +
			"--all expands the unfiltered board to every non-quarantined status, including\n" +
			"archive/. Lens echo and integrity tokens ride stderr only, never the TSV.\n" +
			"Pure read.",
		Args: anyArgs(),
		RunE: func(c *cobra.Command, args []string) error {
			return runList(app, c, listParams{statuses: args, scope: scope, tags: tags, all: all, noLens: noLens})
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "scope to list (defaults to ambient; wins over ambient)")
	cmd.Flags().StringArrayVar(&tags, "tag", nil, "match any of these tags (repeatable; OR; hard filter; ignores lens)")
	cmd.Flags().BoolVar(&all, "all", false, "with no status filter: include done/backlog and archive/")
	cmd.Flags().BoolVar(&noLens, "no-lens", false, "ignore the active lens for this invocation")
	return cmd
}

type listParams struct {
	statuses []string
	scope    string
	tags     []string
	all      bool
	noLens   bool
}

func runList(app *App, c *cobra.Command, p listParams) error {
	e, err := app.openEngine(c)
	if err != nil {
		return err
	}
	defer e.close()

	resolved, err := e.resolveAmbient(p.scope)
	if err != nil {
		return err
	}
	scope := resolved.Name

	res, err := e.reconcile(c, map[string]string{scope: resolved.Entry.Dir})
	if err != nil {
		return err
	}
	// Unreachable still lists indexed rows (stale ok); warning already rode from reconcile.
	schema := res.Schema(scope)

	statusFilter, err := parseStatusFilter(p.statuses, schema)
	if err != nil {
		return err
	}

	gate, err := depgate.Load(e.gateDeps(), res, []string{scope})
	if err != nil {
		return err
	}

	inUse, err := e.db.ScopeTagMembership(scope)
	if err != nil {
		return err
	}
	if len(p.tags) > 0 {
		warnUnknownTags(c, p.tags, inUse)
	}

	lens := e.reg.Lens[scope]
	// --tag is a hard membership filter and supersedes the lens for this invocation.
	applyLens := !p.noLens && len(lens) > 0 && len(p.tags) == 0

	filter := index.BoardFilter{Scope: scope, All: p.all, Tags: p.tags}
	if len(statusFilter) > 0 {
		filter.Statuses = statusNames(statusFilter)
	} else if !p.all {
		filter.DefaultStatuses = status.DefaultListNames(schema.CustomStatuses())
	}
	if applyLens {
		filter.Lens = lens
	}
	kept, err := e.db.BoardTickets(filter)
	if err != nil {
		return err
	}
	index.SortTickets(kept)

	tokens := depgate.NewTokenSet()
	for _, row := range kept {
		ds := gate.EvalDepends(row)
		tokens.Add(ds.Tokens)
		stdoutln(c, tsvLine(row.ID, row.Status, row.Title, strings.Join(ds.WaitingOn, " ")))
	}

	if applyLens {
		stderrln(c, lensEcho(lens))
	}
	for _, line := range tokens.Lines() {
		stderrln(c, line)
	}
	return nil
}

// parseStatusFilter: unknown status → exit 2; empty set means no filter.
func parseStatusFilter(names []string, schema *scopeconfig.Schema) (map[string]bool, error) {
	if len(names) == 0 {
		return nil, nil
	}
	custom := schema.CustomStatuses()
	out := map[string]bool{}
	for _, n := range names {
		if !status.IsKnown(n, custom) {
			return nil, usageErrorf("unknown status %q for this scope", n)
		}
		out[n] = true
	}
	return out, nil
}

func statusNames(set map[string]bool) []string {
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// tsvLine flattens tab/CR/LF in fields so one ticket stays one TSV record.
func tsvLine(fields ...string) string {
	cleaned := make([]string, len(fields))
	for i, f := range fields {
		cleaned[i] = tsvSanitize(f)
	}
	return strings.Join(cleaned, "\t")
}

func tsvSanitize(field string) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' || r == '\r' || r == '\n' {
			return ' '
		}
		return r
	}, field)
}
