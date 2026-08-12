package cli

import (
	"sort"
	"strings"

	"github.com/spf13/cobra"

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
			"as OR among themselves. The lens applies unless --no-lens; with both lens and\n" +
			"--tag, a row is kept if it passes the lens or matches any --tag (union\n" +
			"expand — also-see). A --tag value not used on any ticket still filters\n" +
			"(possibly empty) and emits on stderr (soft; exit 0):\n" +
			"  tag_unknown: \"<t>\" is not used on any ticket in this scope\n" +
			"--all expands the unfiltered board to every non-quarantined status, including\n" +
			"archive/. Lens echo and integrity tokens ride stderr only, never the TSV.\n" +
			"Pure read.",
		Args: usageArgs(cobra.ArbitraryArgs),
		RunE: func(c *cobra.Command, args []string) error {
			return runList(app, c, listParams{statuses: args, scope: scope, tags: tags, all: all, noLens: noLens})
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "scope to list (defaults to ambient; wins over ambient)")
	cmd.Flags().StringArrayVar(&tags, "tag", nil, "match any of these tags (repeatable; OR; with a lens, also keeps lens matches)")
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

	rows, err := e.db.ScopeTickets(scope)
	if err != nil {
		return err
	}
	gate, err := e.buildGate(res, []string{scope})
	if err != nil {
		return err
	}

	if len(p.tags) > 0 {
		warnUnknownTags(c, p.tags, index.TagMembership(rows))
	}

	lens := e.reg.Lens[scope]
	applyLens := !p.noLens && len(lens) > 0

	var kept []*index.Ticket
	for _, row := range rows {
		if !listVisible(row, statusFilter, p.all, schema) {
			continue
		}
		if !listTagVisible(row, p.tags, applyLens, lens) {
			continue
		}
		kept = append(kept, row)
	}
	sortTickets(kept)

	tokens := newTokenSet()
	for _, row := range kept {
		ds := gate.evalDepends(row)
		tokens.add(ds.Tokens)
		stdoutln(c, tsvLine(row.ID, row.Status, row.Title, strings.Join(ds.WaitingOn, " ")))
	}

	if applyLens {
		stderrln(c, lensEcho(lens))
	}
	for _, line := range tokens.lines() {
		stderrln(c, line)
	}
	return nil
}

// parseStatusFilter: unknown status → exit 2; empty set means no filter.
func parseStatusFilter(names []string, schema *scopeconfig.Schema) (map[string]bool, error) {
	if len(names) == 0 {
		return nil, nil
	}
	custom := schemaCustom(schema)
	out := map[string]bool{}
	for _, n := range names {
		if !status.IsKnown(n, custom) {
			return nil, usageErrorf("unknown status %q for this scope", n)
		}
		out[n] = true
	}
	return out, nil
}

// listVisible: status positionals ignore layout; archive/ only under --all when unfiltered.
func listVisible(p *index.Ticket, statusFilter map[string]bool, all bool, schema *scopeconfig.Schema) bool {
	// parse_error rows are never board rows (get/search locate them).
	if p.ParseError {
		return false
	}
	if len(statusFilter) > 0 {
		return statusFilter[p.Status]
	}
	if p.Archived && !all {
		return false
	}
	if all {
		return true
	}
	return status.InDefaultList(p.Status, schemaCustom(schema))
}

// listTagVisible: no tags → lens only (if any); tags only → hard match any;
// lens + tags → union (pass lens or match any --tag). Untagged still pass the
// lens alone; they do not match a bare --tag filter.
func listTagVisible(p *index.Ticket, tags []string, applyLens bool, lens []string) bool {
	hasTags := len(tags) > 0
	switch {
	case applyLens && hasTags:
		return passesLens(p, lens) || matchesAnyTag(p, tags)
	case hasTags:
		return matchesAnyTag(p, tags)
	case applyLens:
		return passesLens(p, lens)
	default:
		return true
	}
}

func matchesAnyTag(p *index.Ticket, tags []string) bool {
	for _, want := range tags {
		for _, have := range p.Tags {
			if want == have {
				return true
			}
		}
	}
	return false
}

func sortTickets(rows []*index.Ticket) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].OrderKey != rows[j].OrderKey {
			return rows[i].OrderKey < rows[j].OrderKey
		}
		return rows[i].ID < rows[j].ID
	})
}

// tokenSet de-dupes diagnostic lines in first-seen order.
type tokenSet struct {
	seen  map[string]bool
	order []string
}

func newTokenSet() *tokenSet { return &tokenSet{seen: map[string]bool{}} }

func (t *tokenSet) add(lines []string) {
	for _, l := range lines {
		if !t.seen[l] {
			t.seen[l] = true
			t.order = append(t.order, l)
		}
	}
}

func (t *tokenSet) lines() []string { return t.order }

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
