package cli

import (
	"sort"

	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/reconcile"
	"github.com/p3bot/tk/internal/scopeconfig"
	"github.com/p3bot/tk/internal/status"
	"github.com/p3bot/tk/internal/token"
)

type gate struct {
	e       *engine
	byID    map[string][]*index.Ticket
	depends map[string][]string
	schemas map[string]*scopeconfig.Schema
	dupSet  map[string]bool
}

// buildGate loads homeScopes plus resolved depend-target tickets (and the depends
// edges those subject tickets need), not the machine-wide dump.
func (e *engine) buildGate(res *reconcile.Result, homeScopes []string) (*gate, error) {
	tickets, err := e.db.TicketsInScopes(homeScopes)
	if err != nil {
		return nil, err
	}
	edges, err := e.db.DependsFromScopes(homeScopes)
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
	extra, err := e.db.TicketsByFullIDs(missing)
	if err != nil {
		return nil, err
	}
	dup, err := e.db.DuplicateIDSet(homeScopes)
	if err != nil {
		return nil, err
	}

	g := &gate{
		e:       e,
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
	for name, s := range res.Schemas {
		g.schemas[name] = s
	}
	return g, nil
}

type depStatus struct {
	WaitingOn   []string
	Tokens      []string
	SchemaError bool
}

func (d depStatus) Held() bool { return len(d.WaitingOn) > 0 || d.SchemaError }

// evalDepends: same-scope missing is depends_dangling; cross-scope is depends_unresolvable.
func (g *gate) evalDepends(p *index.Ticket) depStatus {
	var ds depStatus
	if p.SchemaError {
		ds.SchemaError = true
		ds.Tokens = append(ds.Tokens, token.Line(token.SchemaError,
			p.ID+": a depends/related entry is not a legal full ticket id"))
	}

	seen := map[string]bool{}
	for _, target := range g.depends[p.Path] {
		if seen[target] {
			continue
		}
		seen[target] = true

		rows := g.byID[target]
		if len(rows) == 0 {
			if scopeOfFullID(target) == p.Scope {
				ds.Tokens = append(ds.Tokens, token.Line(token.DependsDangling,
					p.ID+" depends on "+target+" which has no ticket in this scope"))
			} else {
				ds.Tokens = append(ds.Tokens, token.Line(token.DependsUnresolvable,
					p.ID+" depends on "+target+" which cannot be resolved here"))
			}
			ds.WaitingOn = append(ds.WaitingOn, target)
			continue
		}
		if !g.allTerminal(rows) {
			ds.WaitingOn = append(ds.WaitingOn, target)
		}
	}
	sort.Strings(ds.WaitingOn)
	return ds
}

// allTerminal holds rather than falsely satisfying when any row is non-terminal or ambiguous.
func (g *gate) allTerminal(rows []*index.Ticket) bool {
	for _, r := range rows {
		if !status.IsTerminal(r.Status, schemaCustom(g.schema(r.Scope))) {
			return false
		}
	}
	return len(rows) > 0
}

func (g *gate) nextEligible(p *index.Ticket, ds depStatus) bool {
	if !status.IsNextEligible(p.Status) || p.Archived || p.ParseError {
		return false
	}
	if g.isDuplicate(p) || ds.Held() {
		return false
	}
	return true
}

func (g *gate) isDuplicate(p *index.Ticket) bool {
	return g.dupSet[p.Scope+"\x00"+p.ID]
}

// schema caches nil for unusable/unregistered scopes so cross-scope targets are not re-resolved.
func (g *gate) schema(scope string) *scopeconfig.Schema {
	if s, ok := g.schemas[scope]; ok {
		return s
	}
	var s *scopeconfig.Schema
	if entry, ok := g.e.reg.Scopes[scope]; ok {
		s = g.e.rec.SchemaCached(scope, entry.Dir)
	}
	g.schemas[scope] = s
	return s
}

func schemaCustom(s *scopeconfig.Schema) map[string]status.Category {
	if s == nil {
		return nil
	}
	return s.Statuses
}
