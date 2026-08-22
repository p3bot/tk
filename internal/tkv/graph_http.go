package tkv

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"

	"github.com/p3bot/tk/internal/id"
	"github.com/p3bot/tk/internal/index"
)

type dependsPage struct {
	Title    string
	Chrome   chrome
	Scope    string
	All      bool
	Lead     string
	SVG      template.HTML
	Isolated int
	Nodes    int
	Edges    int
}

func (s *Server) dependsGraph(w http.ResponseWriter, r *http.Request) error {
	reg, err := s.loadRegistry()
	if err != nil {
		return err
	}
	qScope := r.URL.Query().Get("scope")
	if qScope != "" {
		if !id.IsScopeName(qScope) {
			return errNotFound("unknown scope")
		}
		if _, ok := reg.Scopes[qScope]; !ok {
			return errNotFound(fmt.Sprintf("unknown scope %q", qScope))
		}
	}
	selected := registeredScope(reg, qScope)
	targets := allTargets(reg)
	if selected != "" {
		targets = map[string]string{selected: reg.Scopes[selected].Dir}
	}
	res, err := s.rec.Reconcile(targets, registeredSet(reg), nowNS())
	if err != nil {
		return err
	}
	ch, err := s.chromeFor(reg, selected, "", navGraphs)
	if err != nil {
		return err
	}
	page := dependsPage{
		Title:  "depends",
		Chrome: ch,
		Scope:  selected,
		All:    r.URL.Query().Get("board") != "1",
		Lead:   "Arrows point at what a ticket is waiting on. Left is unblocked; right depends on the left. Archive is included unless you switch to the working board.",
	}
	if selected == "" {
		return s.render(w, "depends", page)
	}
	tickets, err := s.db.ScopeTickets(selected)
	if err != nil {
		return err
	}
	outEdges, err := s.db.DependsFromScopes([]string{selected})
	if err != nil {
		return err
	}
	inAll, err := s.db.EdgesToScope(selected)
	if err != nil {
		return err
	}
	var inEdges []index.Edge
	for _, e := range inAll {
		if e.Kind == index.EdgeDepends {
			inEdges = append(inEdges, e)
		}
	}
	var extraIDs []string
	seen := map[string]bool{}
	for _, t := range tickets {
		if t != nil {
			seen[t.ID] = true
		}
	}
	for _, e := range append(append([]index.Edge{}, outEdges...), inEdges...) {
		for _, full := range []string{e.FromID, e.ToID} {
			if full == "" || seen[full] {
				continue
			}
			seen[full] = true
			extraIDs = append(extraIDs, full)
		}
	}
	extra, err := s.db.TicketsByFullIDs(extraIDs)
	if err != nil {
		return err
	}
	tickets = append(tickets, extra...)

	layout := buildDependsGraph(selected, page.All, schemaCustom(res.Schema(selected)), tickets, outEdges, inEdges)
	page.SVG = renderDepSVG(layout)
	page.Isolated = countIsolated(selected, page.All, schemaCustom(res.Schema(selected)), tickets, layout)
	page.Nodes = len(layout.Nodes)
	page.Edges = len(layout.Edges)
	return s.render(w, "depends", page)
}

func (p dependsPage) AllHref() string {
	v := url.Values{}
	if p.Scope != "" {
		v.Set("scope", p.Scope)
	}
	if p.All {
		v.Set("board", "1")
	}
	enc := v.Encode()
	if enc == "" {
		return "/graphs/depends"
	}
	return "/graphs/depends?" + enc
}
