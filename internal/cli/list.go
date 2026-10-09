package cli

import (
	"sort"
	"strconv"
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
		every  bool
		tags   []string
		all    bool
		open   bool
		noLens bool
		count  bool
	)
	cmd := &cobra.Command{
		Use:     "list [status...] [--scope S] [--every-scope] [--tag T]... [--all] [--open] [--no-lens] [--count]",
		Aliases: []string{"ls"},
		Short:   "Board / inventory as parse-stable TSV",
		Long: "Print tickets, sorted (order, id) inside the scope, one TSV line each:\n" +
			"  <full-id>\\t<status>\\t<title>\\t<waiting-on>\n" +
			"Reverse that order inside the scope when every status in the filter is terminal (including\n" +
			"custom terminal names), so `list done` and `list done cancelled` are highest\n" +
			"order key first (the live board, flipped). Empty filter, --all, --open, and\n" +
			"any mixed filter stay forward. The listing sort does not rewrite order keys.\n" +
			"Headerless TSV (no header row). Summary is not a list column — use\n" +
			"`tk meta get <id>`. Bare list is the default active set. For one scope, status\n" +
			"positionals union-filter (an unknown status exits 2) and include matching rows\n" +
			"under archive/ — so `list done` shows done tickets without --all. --tag repeats\n" +
			"as OR among themselves and is always a hard membership filter (ticket must\n" +
			"carry at least one listed tag; untagged rows are out). Any --tag ignores the\n" +
			"lens for that invocation (no echo). Without --tag, one scope applies the lens\n" +
			"unless --no-lens. A --tag value not used on any ticket still filters (possibly empty)\n" +
			"and emits on stderr (soft; exit 0):\n" +
			"  tag_unknown: \"<t>\" is not used on any ticket in this scope\n" +
			"--open expands the unfiltered board to every non-terminal status (active plus\n" +
			"backlog), including a non-terminal file under archive/. --all expands further\n" +
			"to every non-quarantined status, including terminal rows under archive/.\n" +
			"--all and --open together are a usage error. --every-scope lists every\n" +
			"registered scope. Scope names stay ascending, then each scope uses its own\n" +
			"order. A terminal-only filter reverses inside each scope and does not reverse\n" +
			"the scope groups. Across scopes, a positional is legal when it is a built-in or\n" +
			"declared in at least one scope being read. Scopes that do not declare a custom\n" +
			"name contribute no rows for it. --every-scope ignores the lens (no echo);\n" +
			"--no-lens changes nothing further. --scope stays one scope. --every-scope and\n" +
			"--scope together are a usage error. The scope name all, and TK_SCOPE=all, are\n" +
			"not every scope.\n" +
			"--count prints how many rows this invocation would have listed and prints no\n" +
			"ticket TSV. One scope prints a bare integer, including 0. --every-scope prints\n" +
			"headerless TSV, one line per registered scope, scope name then count, names\n" +
			"ascending, including zeros. A scope that contributes no rows still prints 0.\n" +
			"An empty registry stays empty stdout. Filters, the lens, and stderr tokens\n" +
			"are unchanged.\n" +
			"Lens echo and integrity tokens ride stderr only, never the TSV. Pure read.",
		Args: anyArgs(),
		RunE: func(c *cobra.Command, args []string) error {
			return runList(app, c, listParams{
				statuses: args, scope: scope, every: every, tags: tags, all: all, open: open, noLens: noLens, count: count,
			})
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "scope to list (defaults to ambient; wins over ambient)")
	cmd.Flags().BoolVar(&every, "every-scope", false, "list every registered scope (mutually exclusive with --scope)")
	cmd.Flags().StringArrayVar(&tags, "tag", nil, "match any of these tags (repeatable; OR; hard filter; ignores lens)")
	cmd.Flags().BoolVar(&all, "all", false, "with no status filter: every non-quarantined status, including terminal and archive/")
	cmd.Flags().BoolVar(&open, "open", false, "with no status filter: every non-terminal status (includes backlog)")
	cmd.Flags().BoolVar(&noLens, "no-lens", false, "ignore the active lens for this invocation")
	cmd.Flags().BoolVar(&count, "count", false, "print the row count instead of ticket TSV")
	return cmd
}

type listParams struct {
	statuses []string
	scope    string
	every    bool
	tags     []string
	all      bool
	open     bool
	noLens   bool
	count    bool
}

func runList(app *App, c *cobra.Command, p listParams) error {
	if p.all && p.open {
		return usageErrorf("--all and --open are mutually exclusive")
	}
	if p.every && p.scope != "" {
		return usageErrorf("--every-scope and --scope are mutually exclusive")
	}

	e, err := app.openEngine(c)
	if err != nil {
		return err
	}
	defer e.close()

	if p.every {
		return runListEvery(e, c, p)
	}

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

	filter := index.BoardFilter{Scope: scope, Tags: p.tags}
	switch {
	case len(statusFilter) > 0:
		filter.Statuses = statusNames(statusFilter)
	case p.all:
		filter.All = true
	case p.open:
		filter.Statuses = status.NonTerminalNames(schema.CustomStatuses())
	default:
		filter.DefaultStatuses = status.DefaultListNames(schema.CustomStatuses())
	}
	if applyLens {
		filter.Lens = lens
	}
	kept, err := e.db.BoardTickets(filter)
	if err != nil {
		return err
	}
	index.SortListing(kept, filter.Statuses, schema.CustomStatuses())

	tokens := depgate.NewTokenSet()
	emitBoard(c, kept, gate, tokens, p.count, "")

	if applyLens {
		stderrln(c, lensEcho(lens))
	}
	for _, line := range tokens.Lines() {
		stderrln(c, line)
	}
	return nil
}

// runListEvery prints one board per registered scope. Scope name order is the
// outer sort; order keys are not compared across scopes. The lens is not applied.
func runListEvery(e *engine, c *cobra.Command, p listParams) error {
	targets := e.allTargets()
	res, err := e.reconcile(c, targets)
	if err != nil {
		return err
	}
	scopes := scopeNames(targets)
	if err := checkLogStatuses(p.statuses, false, scopes, res); err != nil {
		return err
	}
	gate, err := depgate.Load(e.gateDeps(), res, scopes)
	if err != nil {
		return err
	}
	if len(p.tags) > 0 {
		inUse, err := tagMembership(e.db, scopes)
		if err != nil {
			return err
		}
		warnUnknownTags(c, p.tags, inUse)
	}

	tokens := depgate.NewTokenSet()
	for _, scope := range scopes {
		kept, err := scopeBoard(e.db, res.Schema(scope), scope, p)
		if err != nil {
			return err
		}
		emitBoard(c, kept, gate, tokens, p.count, scope)
	}
	for _, line := range tokens.Lines() {
		stderrln(c, line)
	}
	return nil
}

// scopeBoard is one scope's list filter. A status positional passes only the
// names that scope's schema knows. An empty name list is the default board, so
// a positional that matches nothing here returns no rows instead.
func scopeBoard(db *index.DB, schema *scopeconfig.Schema, scope string, p listParams) ([]*index.Ticket, error) {
	custom := schema.CustomStatuses()
	filter := index.BoardFilter{Scope: scope, Tags: p.tags}
	switch {
	case len(p.statuses) > 0:
		names := statusesThisScope(p.statuses, custom)
		if len(names) == 0 {
			return nil, nil
		}
		filter.Statuses = names
	case p.all:
		filter.All = true
	case p.open:
		filter.Statuses = status.NonTerminalNames(custom)
	default:
		filter.DefaultStatuses = status.DefaultListNames(custom)
	}
	kept, err := db.BoardTickets(filter)
	if err != nil {
		return nil, err
	}
	index.SortListing(kept, filter.Statuses, custom)
	return kept, nil
}

// statusesThisScope keeps built-ins and custom names this schema declares.
func statusesThisScope(names []string, custom map[string]status.Category) []string {
	out := make([]string, 0, len(names))
	seen := map[string]bool{}
	for _, n := range names {
		if seen[n] || !status.IsKnown(n, custom) {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

// emitBoard prints the board, or with count the cardinality of the same rows.
// scopeLabel is set for --every-scope --count (scope, count). An empty label
// prints one integer. Depends tokens are collected either way.
func emitBoard(c *cobra.Command, kept []*index.Ticket, gate *depgate.Gate, tokens *depgate.TokenSet, count bool, scopeLabel string) {
	if count {
		n := strconv.Itoa(len(kept))
		if scopeLabel != "" {
			stdoutln(c, tsvLine(scopeLabel, n))
		} else {
			stdoutln(c, n)
		}
	}
	for _, row := range kept {
		ds := gate.EvalDepends(row)
		tokens.Add(ds.Tokens)
		if count {
			continue
		}
		stdoutln(c, tsvLine(row.ID, row.Status, row.Title, strings.Join(ds.WaitingOn, " ")))
	}
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
