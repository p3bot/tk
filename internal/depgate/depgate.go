// Package depgate is the in-memory depends gate and next picker shared by
// tk and tkv. Waiting-on, Held, next eligibility, and next selection live
// here so list, next, claim, status, and tkv cannot fork. Gating is Go, not
// SQL; callers own reconcile closure and presentation.
package depgate

import (
	"sort"

	"github.com/p3bot/tk/internal/id"
	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/reconcile"
	"github.com/p3bot/tk/internal/registry"
	"github.com/p3bot/tk/internal/scopeconfig"
	"github.com/p3bot/tk/internal/status"
	"github.com/p3bot/tk/internal/token"
)

// Deps are the machine-local services needed to load a Gate from the index.
type Deps struct {
	DB  *index.DB
	Rec *reconcile.Reconciler
	Reg *registry.Registry
}

// Gate is the path-keyed board depends graph for a set of home scopes plus
// resolved depend-target tickets. It is not the id-neighbourhood walker used
// by tk deps.
type Gate struct {
	rec     *reconcile.Reconciler
	reg     *registry.Registry
	byID    map[string][]*index.Ticket
	depends map[string][]string
	schemas map[string]*scopeconfig.Schema
	dupSet  map[string]bool
}

// Load builds a Gate from homeScopes plus resolved depend-target tickets
// (and the depends edges those subject tickets need), not the machine-wide dump.
func Load(deps Deps, res *reconcile.Result, homeScopes []string) (*Gate, error) {
	tickets, err := deps.DB.TicketsInScopes(homeScopes)
	if err != nil {
		return nil, err
	}
	edges, err := deps.DB.DependsFromScopes(homeScopes)
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
	extra, err := deps.DB.TicketsByFullIDs(missing)
	if err != nil {
		return nil, err
	}
	dup, err := deps.DB.DuplicateIDSet(homeScopes)
	if err != nil {
		return nil, err
	}

	g := &Gate{
		rec:     deps.Rec,
		reg:     deps.Reg,
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

// Eval is the waiting-on set and diagnostic tokens for one ticket.
type Eval struct {
	WaitingOn   []string
	Tokens      []string
	SchemaError bool
}

// Held reports whether the ticket is blocked from next: unmet depends or a schema error.
func (e Eval) Held() bool { return len(e.WaitingOn) > 0 || e.SchemaError }

// EvalDepends returns waiting-on ids. Same-scope missing is depends_dangling; cross-scope is depends_unresolvable.
func (g *Gate) EvalDepends(p *index.Ticket) Eval {
	var ds Eval
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
			if id.ScopeOfFullID(target) == p.Scope {
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
func (g *Gate) allTerminal(rows []*index.Ticket) bool {
	for _, r := range rows {
		if !status.IsTerminal(r.Status, g.schema(r.Scope).CustomStatuses()) {
			return false
		}
	}
	return len(rows) > 0
}

// NextEligible reports whether p can be chosen as next given ds.
func (g *Gate) NextEligible(p *index.Ticket, ds Eval) bool {
	if !status.IsNextEligible(p.Status) || p.Archived || p.ParseError {
		return false
	}
	if g.isDuplicate(p) || ds.Held() {
		return false
	}
	return true
}

func (g *Gate) isDuplicate(p *index.Ticket) bool {
	return g.dupSet[p.Scope+"\x00"+p.ID]
}

// schema caches nil for unusable/unregistered scopes so cross-scope targets are not re-resolved.
func (g *Gate) schema(scope string) *scopeconfig.Schema {
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

// Selection is the next walk result. Callers own empty-queue policy
// (tk next refuses; tk status emits next\t; tkv shows no badge).
type Selection struct {
	Chosen           *index.Ticket
	Tokens           []string
	Blocked          int
	ReadyOutsideLens int
	ApplyLens        bool
	Lens             []string
}

// SelectNext walks candidates in (order_key, id), applying the lens unless noLens.
// candidates are already SQL-filtered (todo, not archived, not parse_error).
func (g *Gate) SelectNext(candidates []*index.Ticket, lens []string, noLens bool) Selection {
	index.SortTickets(candidates)
	applyLens := !noLens && len(lens) > 0

	tokens := NewTokenSet()
	var chosen *index.Ticket
	blocked, readyOutsideLens := 0, 0
	for _, p := range candidates {
		ds := g.EvalDepends(p)
		tokens.Add(ds.Tokens)
		if !g.NextEligible(p, ds) {
			// Held drives blocked diagnostic; duplicate-id skip does not.
			if ds.Held() {
				blocked++
			}
			continue
		}
		if applyLens && !passesLens(p, lens) {
			readyOutsideLens++
			continue
		}
		if chosen == nil {
			chosen = p
		}
	}
	return Selection{
		Chosen:           chosen,
		Tokens:           tokens.Lines(),
		Blocked:          blocked,
		ReadyOutsideLens: readyOutsideLens,
		ApplyLens:        applyLens,
		Lens:             lens,
	}
}

// passesLens: empty lens shows all; untagged tickets are never hidden.
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

// TokenSet de-dupes diagnostic lines in first-seen order.
type TokenSet struct {
	seen  map[string]bool
	order []string
}

// NewTokenSet builds an empty first-seen collector.
func NewTokenSet() *TokenSet { return &TokenSet{seen: map[string]bool{}} }

// Add appends lines that have not been seen yet.
func (t *TokenSet) Add(lines []string) {
	for _, l := range lines {
		if !t.seen[l] {
			t.seen[l] = true
			t.order = append(t.order, l)
		}
	}
}

// Lines returns the collected lines in first-seen order.
func (t *TokenSet) Lines() []string { return t.order }
