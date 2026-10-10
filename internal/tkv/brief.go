package tkv

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"sort"
	"time"

	"github.com/p3bot/tk/internal/design"
	"github.com/p3bot/tk/internal/id"
	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/reconcile"
	"github.com/p3bot/tk/internal/status"
)

const briefBacklogShown = 8

type briefPage struct {
	Title        string
	Chrome       chrome
	Scope        string
	Todo         int
	Now          []briefTicket
	Waiting      []briefWait
	SVG          template.HTML
	Drafts       []briefTicket
	Backlog      []briefTicket
	BacklogTotal int
	BacklogShown int
	Tags         []briefTag
	Designs      []briefTicket
}

type briefTicket struct {
	ID     string
	Title  string
	Status string
	Age    string
	Href   string
	Note   string
}

type briefWait struct {
	FromID     string
	FromTitle  string
	FromStatus string
	FromHref   string
	ToID       string
	ToTitle    string
	ToStatus   string
	ToHref     string
}

type briefTag struct {
	Tag string
	N   int
}

func (p briefPage) DependsHref() string {
	if p.Scope == "" {
		return "/graphs/depends"
	}
	return "/graphs/depends?scope=" + url.QueryEscape(p.Scope)
}

func (s *Server) brief(w http.ResponseWriter, r *http.Request) error {
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
	res, err := s.rec.Reconcile(allTargets(reg), registeredSet(reg), nowNS())
	if err != nil {
		return err
	}
	ch, err := s.pageChrome(reg, selected, "", navGraphs, r)
	if err != nil {
		return err
	}
	page := briefPage{Title: "brief", Chrome: ch, Scope: selected}
	if selected == "" {
		return s.render(w, "brief", page)
	}
	if err := s.fillBrief(&page, selected, res); err != nil {
		return err
	}
	return s.render(w, "brief", page)
}

func (s *Server) fillBrief(page *briefPage, scope string, res *reconcile.Result) error {
	custom := res.Schema(scope).CustomStatuses()
	page.BacklogShown = briefBacklogShown
	tickets, err := s.db.ScopeTickets(scope)
	if err != nil {
		return err
	}
	byID := map[string]*index.Ticket{}
	for _, t := range tickets {
		if t == nil || t.ParseError {
			continue
		}
		if _, ok := byID[t.ID]; !ok {
			byID[t.ID] = t
		}
	}
	edges, err := s.db.DependsFromScopes([]string{scope})
	if err != nil {
		return err
	}
	var missing []string
	seenMissing := map[string]bool{}
	for _, e := range edges {
		if e.ToID == "" || byID[e.ToID] != nil || seenMissing[e.ToID] {
			continue
		}
		seenMissing[e.ToID] = true
		missing = append(missing, e.ToID)
	}
	extra, err := s.db.TicketsByFullIDs(missing)
	if err != nil {
		return err
	}
	for _, t := range extra {
		if t == nil || t.ParseError {
			continue
		}
		if _, ok := byID[t.ID]; !ok {
			byID[t.ID] = t
		}
	}

	now := time.Now()
	openFrom := map[string]bool{}
	var open []index.Edge
	for _, e := range edges {
		// A finished ticket stays off the brief, whether it owns the arrow
		// or is the ticket being waited on. Finished follows that ticket's
		// own scope. A missing ticket has no status, so the arrow stays.
		if briefFinished(res, byID[e.FromID]) || briefFinished(res, byID[e.ToID]) {
			continue
		}
		open = append(open, e)
		openFrom[e.FromID] = true
	}
	sort.Slice(open, func(i, j int) bool {
		if open[i].FromID != open[j].FromID {
			return open[i].FromID < open[j].FromID
		}
		return open[i].ToID < open[j].ToID
	})
	for _, e := range open {
		page.Waiting = append(page.Waiting, briefWaitFrom(e, byID))
	}
	if len(open) > 0 {
		focused := append([]*index.Ticket{}, tickets...)
		focused = append(focused, extra...)
		page.SVG = renderDepSVG(buildDependsGraph(scope, true, custom, focused, open, nil))
	}

	var drafts, backlog []*index.Ticket
	for _, t := range byID {
		if t.Scope != scope || t.Archived {
			continue
		}
		// Todo is the queue count and draft is its own list. Every other
		// active status, including a custom one, is current work. A backlog
		// category joins the backlog list. Finished and unknown statuses stay
		// off; doctor reports an unknown name.
		cat, known := status.CategoryOf(t.Status, custom)
		switch {
		case t.Status == status.Todo:
			page.Todo++
		case t.Status == status.Draft:
			drafts = append(drafts, t)
		case known && cat == status.CategoryBacklog:
			backlog = append(backlog, t)
		case known && cat == status.CategoryActive:
			row := briefTicketFrom(t, now, true)
			if t.Status == status.Blocked && !openFrom[t.ID] {
				row.Note = "no open depends"
			}
			page.Now = append(page.Now, row)
		}
	}
	sort.Slice(page.Now, func(i, j int) bool {
		return nowLess(page.Now[i], page.Now[j])
	})
	sort.Slice(drafts, func(i, j int) bool { return briefOlder(drafts[i], drafts[j]) })
	sort.Slice(backlog, func(i, j int) bool { return briefOlder(backlog[i], backlog[j]) })
	for _, t := range drafts {
		page.Drafts = append(page.Drafts, briefTicketFrom(t, now, false))
	}
	page.BacklogTotal = len(backlog)
	if len(backlog) > page.BacklogShown {
		backlog = backlog[:page.BacklogShown]
	}
	for _, t := range backlog {
		page.Backlog = append(page.Backlog, briefTicketFrom(t, now, false))
	}

	counts := map[string]int{}
	for _, t := range byID {
		if t.Scope != scope || t.Archived {
			continue
		}
		for _, tag := range t.Tags {
			counts[tag]++
		}
	}
	for tag, n := range counts {
		page.Tags = append(page.Tags, briefTag{Tag: tag, N: n})
	}
	sort.Slice(page.Tags, func(i, j int) bool {
		if page.Tags[i].N != page.Tags[j].N {
			return page.Tags[i].N > page.Tags[j].N
		}
		return page.Tags[i].Tag < page.Tags[j].Tag
	})

	designs, err := s.db.ScopeDesigns(scope)
	if err != nil {
		return err
	}
	for _, d := range designs {
		if d == nil || d.ParseError || !design.DefaultListed(d.Status) {
			continue
		}
		prod, err := s.db.EdgesFromPath(d.Path)
		if err != nil {
			return err
		}
		if producesAny(prod) {
			continue
		}
		age, _ := statusDwell(d.Created, now)
		page.Designs = append(page.Designs, briefTicket{
			ID: d.ID, Title: d.Title, Status: d.Status, Age: age, Href: designHref(d.ID),
		})
	}
	sort.Slice(page.Designs, func(i, j int) bool {
		if page.Designs[i].Status != page.Designs[j].Status {
			return designBriefRank(page.Designs[i].Status) < designBriefRank(page.Designs[j].Status)
		}
		return page.Designs[i].ID < page.Designs[j].ID
	})
	return nil
}

func producesAny(edges []index.Edge) bool {
	for _, e := range edges {
		if e.Kind == index.EdgeProduces {
			return true
		}
	}
	return false
}

func briefWaitFrom(e index.Edge, byID map[string]*index.Ticket) briefWait {
	w := briefWait{FromID: e.FromID, ToID: e.ToID}
	if t := byID[e.FromID]; t != nil {
		w.FromTitle = t.Title
		w.FromStatus = t.Status
		w.FromHref = inspectHref(t.ID)
	} else {
		w.FromTitle = e.FromID
		w.FromHref = inspectHref(e.FromID)
	}
	if t := byID[e.ToID]; t != nil {
		w.ToTitle = t.Title
		w.ToStatus = t.Status
		w.ToHref = inspectHref(t.ID)
	} else {
		w.ToTitle = e.ToID
		w.ToStatus = "missing"
		w.ToHref = inspectHref(e.ToID)
	}
	return w
}

func briefTicketFrom(t *index.Ticket, now time.Time, preferChanged bool) briefTicket {
	stamp := t.Created
	if preferChanged && t.Changed != "" {
		stamp = t.Changed
	}
	age, _ := statusDwell(stamp, now)
	return briefTicket{
		ID: t.ID, Title: t.Title, Status: t.Status, Age: age, Href: inspectHref(t.ID),
	}
}

func designBriefRank(statusName string) int {
	switch statusName {
	case design.StatusDraft:
		return 0
	case design.StatusAccepted:
		return 1
	default:
		return 2
	}
}

func briefFinished(res *reconcile.Result, t *index.Ticket) bool {
	if t == nil {
		return false
	}
	return status.IsTerminal(t.Status, res.Schema(t.Scope).CustomStatuses())
}

func nowLess(a, b briefTicket) bool {
	ra, rb := nowRank(a.Status), nowRank(b.Status)
	if ra != rb {
		return ra < rb
	}
	if a.Status != b.Status {
		return a.Status < b.Status
	}
	return a.ID < b.ID
}

func nowRank(statusName string) int {
	switch statusName {
	case status.InProgress:
		return 0
	case status.Review:
		return 1
	case status.Blocked:
		return 2
	default:
		return 3
	}
}

// briefOlder reports whether a was created before b. A created value that is
// not RFC3339 sorts after a real instant.
func briefOlder(a, b *index.Ticket) bool {
	ta, aerr := time.Parse(time.RFC3339, a.Created)
	tb, berr := time.Parse(time.RFC3339, b.Created)
	aok, bok := aerr == nil, berr == nil
	if aok != bok {
		return aok
	}
	if aok && !ta.Equal(tb) {
		return ta.Before(tb)
	}
	return a.ID < b.ID
}
