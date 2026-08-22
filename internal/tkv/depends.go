package tkv

import (
	"sort"

	"github.com/p3bot/tk/internal/id"
	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/reconcile"
	"github.com/p3bot/tk/internal/registry"
	"github.com/p3bot/tk/internal/scopeconfig"
	"github.com/p3bot/tk/internal/status"
)

// depGate is the same unmet-depends idea as tk list's waiting-on column and
// tk next's eligibility walk. Gating stays in Go; the index has no depends SQL.
type depGate struct {
	rec     *reconcile.Reconciler
	reg     *registry.Registry
	byID    map[string][]*index.Ticket
	depends map[string][]string
	schemas map[string]*scopeconfig.Schema
	dupSet  map[string]bool
}

func loadGate(db *index.DB, rec *reconcile.Reconciler, reg *registry.Registry, res *reconcile.Result, homeScopes []string) (*depGate, error) {
	tickets, err := db.TicketsInScopes(homeScopes)
	if err != nil {
		return nil, err
	}
	edges, err := db.DependsFromScopes(homeScopes)
	if err != nil {
		return nil, err
	}
	have := make(map[string]bool, len(tickets))
	for _, p := range tickets {
		have[p.ID] = true
	}
	var missing []string
	seen := map[string]bool{}
	for _, ed := range edges {
		if have[ed.ToID] || seen[ed.ToID] {
			continue
		}
		seen[ed.ToID] = true
		missing = append(missing, ed.ToID)
	}
	extra, err := db.TicketsByFullIDs(missing)
	if err != nil {
		return nil, err
	}
	dup, err := db.DuplicateIDSet(homeScopes)
	if err != nil {
		return nil, err
	}

	g := &depGate{
		rec:     rec,
		reg:     reg,
		byID:    map[string][]*index.Ticket{},
		depends: map[string][]string{},
		schemas: map[string]*scopeconfig.Schema{},
		dupSet:  dup,
	}
	for _, p := range tickets {
		g.byID[p.ID] = append(g.byID[p.ID], p)
	}
	for _, p := range extra {
		g.byID[p.ID] = append(g.byID[p.ID], p)
	}
	for _, ed := range edges {
		g.depends[ed.FromPath] = append(g.depends[ed.FromPath], ed.ToID)
	}
	if res != nil {
		for name, s := range res.Schemas {
			g.schemas[name] = s
		}
	}
	return g, nil
}

func (g *depGate) waitingOn(p *index.Ticket) []string {
	seen := map[string]bool{}
	var out []string
	for _, target := range g.depends[p.Path] {
		if seen[target] {
			continue
		}
		seen[target] = true
		rows := g.byID[target]
		if len(rows) == 0 || !g.allTerminal(rows) {
			out = append(out, target)
		}
	}
	sort.Strings(out)
	return out
}

func (g *depGate) allTerminal(rows []*index.Ticket) bool {
	for _, r := range rows {
		if !status.IsTerminal(r.Status, schemaCustom(g.schema(r.Scope))) {
			return false
		}
	}
	return len(rows) > 0
}

func (g *depGate) nextEligible(p *index.Ticket, waiting []string) bool {
	if !status.IsNextEligible(p.Status) || p.Archived || p.ParseError {
		return false
	}
	if g.dupSet[p.Scope+"\x00"+p.ID] || p.SchemaError || len(waiting) > 0 {
		return false
	}
	return true
}

func (g *depGate) schema(scope string) *scopeconfig.Schema {
	if s, ok := g.schemas[scope]; ok {
		return s
	}
	var s *scopeconfig.Schema
	if g.reg != nil && g.rec != nil {
		if entry, ok := g.reg.Scopes[scope]; ok {
			s = g.rec.SchemaCached(scope, entry.Dir)
		}
	}
	g.schemas[scope] = s
	return s
}

func (g *depGate) selectNext(candidates []*index.Ticket, lens []string) *index.Ticket {
	sortTickets(candidates)
	applyLens := len(lens) > 0
	for _, p := range candidates {
		waiting := g.waitingOn(p)
		if !g.nextEligible(p, waiting) {
			continue
		}
		if applyLens && !passesLens(p, lens) {
			continue
		}
		return p
	}
	return nil
}

func passesLens(p *index.Ticket, lens []string) bool {
	if len(lens) == 0 || len(p.Tags) == 0 {
		return true
	}
	set := map[string]bool{}
	for _, t := range lens {
		set[t] = true
	}
	for _, t := range p.Tags {
		if set[t] {
			return true
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

func schemaCustom(s *scopeconfig.Schema) map[string]status.Category {
	if s == nil {
		return nil
	}
	return s.Statuses
}

func scopeOfFullID(fullID string) string {
	i := 0
	for i < len(fullID) {
		if fullID[i] == '-' {
			return fullID[:i]
		}
		i++
	}
	return fullID
}

func inspectHref(fullID string) string {
	scope := scopeOfFullID(fullID)
	if !id.IsScopeName(scope) {
		return ""
	}
	return "/scope/" + scope + "/" + fullID
}
