package tkv

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/p3bot/tk/internal/depgate"
	"github.com/p3bot/tk/internal/frontmatter"
	"github.com/p3bot/tk/internal/gitroot"
	"github.com/p3bot/tk/internal/id"
	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/reconcile"
	"github.com/p3bot/tk/internal/registry"
	"github.com/p3bot/tk/internal/scopeadmin"
	"github.com/p3bot/tk/internal/scopeconfig"
	"github.com/p3bot/tk/internal/status"
)

type overviewPage struct {
	Title  string
	Chrome chrome
	Rows   []overviewRow
}

type overviewRow struct {
	Name       string
	Mode       string
	Total      int
	Todo       int
	InProgress int
	Blocked    int
	Review     int
	Draft      int
	Backlog    int
	Done       int
	Cancelled  int
	Next       string
	NextHref   string
	Claimed    []idLink
	Dangling   int
	Integrity  string
	Note       string
}

type idLink struct {
	ID   string
	Href string
}

func (s *Server) overview(w http.ResponseWriter, r *http.Request) error {
	reg, err := s.loadRegistry()
	if err != nil {
		return err
	}
	res, err := s.rec.Reconcile(allTargets(reg), registeredSet(reg), nowNS())
	if err != nil {
		return err
	}
	ch, err := s.pageChrome(reg, "", "", navBoard, r)
	if err != nil {
		return err
	}
	names := scopeNames(reg)
	rows := make([]overviewRow, 0, len(names))
	for _, name := range names {
		row, err := s.overviewRow(reg, res, name)
		if err != nil {
			return err
		}
		rows = append(rows, row)
	}
	return s.render(w, "overview", overviewPage{Title: "scopes", Chrome: ch, Rows: rows})
}

func (s *Server) overviewRow(reg *registry.Registry, res *reconcile.Result, name string) (overviewRow, error) {
	entry := reg.Scopes[name]
	row := overviewRow{Name: name, Integrity: "ok"}
	if res.Unreachable[name] {
		row.Mode = scopeadmin.ModeUnknown
		row.Note = fmt.Sprintf("Directory is not reachable: %s", entry.Dir)
	}

	_, inRepo := gitroot.RepoRoot(entry.Dir)
	schema := res.Schema(name)
	cfgErr := res.ConfigErrs[name]
	if row.Note == "" {
		driftName := ""
		if cfgErr == nil && schema != nil {
			driftName = schema.Name
		} else if cueName, nameErr := scopeconfig.ReadName(s.app.Ctx, entry.Dir); nameErr == nil {
			driftName = cueName
		}
		if driftName != "" && driftName != name {
			row.Note = fmt.Sprintf("Name drift: registry key %q but tk.cue name is %q", name, driftName)
		}
	}
	if cfgErr != nil {
		if row.Mode == "" {
			row.Mode = scopeadmin.ModePlainFiles
		}
		msg := fmt.Sprintf("Config unparseable: %s", cfgErr.Reason)
		if row.Note == "" {
			row.Note = msg
		} else {
			row.Note = row.Note + " — " + msg
		}
	}
	if row.Mode == "" {
		row.Mode = statusMode(schema, cfgErr != nil, inRepo)
	}

	pulse, err := s.db.ScopePulse(name, reg.Lens[name])
	if err != nil {
		return row, err
	}
	row.Total = pulse.Total
	row.Todo = pulse.Todo
	row.InProgress = pulse.InProgress
	row.Blocked = pulse.Blocked
	row.Review = pulse.Review
	row.Draft = pulse.Draft
	row.Backlog = pulse.Backlog
	row.Done = pulse.Done
	row.Cancelled = pulse.Cancelled
	row.Claimed = ticketLinks(pulse.Claimed)

	dangling, err := s.db.SameScopeDanglingDependsCount(name)
	if err != nil {
		return row, err
	}
	row.Dangling = dangling

	integ, err := scopeIntegrity(s.db, name, schema)
	if err != nil {
		return row, err
	}
	row.Integrity = integ

	if !res.Unreachable[name] {
		gate, err := depgate.Load(s.gateDeps(reg), res, []string{name})
		if err != nil {
			return row, err
		}
		candidates, err := s.db.NextCandidates(name)
		if err != nil {
			return row, err
		}
		if next := gate.SelectNext(candidates, reg.Lens[name], false).Chosen; next != nil {
			row.Next = next.ID
			row.NextHref = inspectHref(next.ID)
		}
	}
	return row, nil
}

func ticketLinks(rows []*index.Ticket) []idLink {
	if len(rows) == 0 {
		return nil
	}
	out := make([]idLink, len(rows))
	for i, p := range rows {
		out[i] = idLink{ID: p.ID, Href: inspectHref(p.ID)}
	}
	return out
}

type kanbanPage struct {
	Title          string
	Chrome         chrome
	Name           string
	Backlog        bool
	Archived       bool
	Tags           []string
	Active         []string
	Next           string
	NextHref       string
	Cols           []kanbanCol
	CanWrite       bool
	CreateStatuses []string
}

type kanbanCol struct {
	Status string
	Cards  []kanbanCard
}

type kanbanCard struct {
	ID           string
	ShortID      string
	Title        string
	Href         string
	Tags         []string
	WaitingOn    []idLink
	SchemaError  bool
	Next         bool
	CanClaim     bool
	MarkStatuses []string
}

func (s *Server) kanban(w http.ResponseWriter, r *http.Request) error {
	name := r.PathValue("name")
	if !id.IsScopeName(name) {
		return errNotFound("unknown scope")
	}
	reg, err := s.loadRegistry()
	if err != nil {
		return err
	}
	if _, ok := reg.Scopes[name]; !ok {
		return errNotFound(fmt.Sprintf("unknown scope %q", name))
	}
	res, err := s.rec.Reconcile(allTargets(reg), registeredSet(reg), nowNS())
	if err != nil {
		return err
	}
	schema := res.Schema(name)
	custom := schema.CustomStatuses()
	backlog := r.URL.Query().Get("backlog") == "1"
	archived := r.URL.Query().Get("archived") == "1"
	tags := r.URL.Query()["tag"]
	lens := reg.Lens[name]
	filter := index.BoardFilter{Scope: name, Tags: tags}
	switch {
	case backlog && archived:
		filter.All = true
	case backlog:
		filter.Statuses = status.NonTerminalNames(custom)
	case archived:
		filter.Statuses = append(status.DefaultListNames(custom), status.TerminalNames(custom)...)
	default:
		filter.DefaultStatuses = status.DefaultListNames(custom)
	}
	if len(tags) == 0 && len(lens) > 0 {
		filter.Lens = lens
	}
	tickets, err := s.db.BoardTickets(filter)
	if err != nil {
		return err
	}
	gate, err := depgate.Load(s.gateDeps(reg), res, []string{name})
	if err != nil {
		return err
	}
	var nextID, nextHref string
	if !res.Unreachable[name] {
		candidates, err := s.db.NextCandidates(name)
		if err != nil {
			return err
		}
		if next := gate.SelectNext(candidates, lens, false).Chosen; next != nil {
			nextID = next.ID
			nextHref = inspectHref(next.ID)
		}
	}

	present := make([]string, 0, len(tickets))
	byStatus := map[string][]*index.Ticket{}
	for _, p := range tickets {
		byStatus[p.Status] = append(byStatus[p.Status], p)
		present = append(present, p.Status)
	}
	colNames := kanbanColumns(custom, backlog, archived, present)
	cols := make([]kanbanCol, 0, len(colNames))
	for _, st := range colNames {
		col := kanbanCol{Status: st}
		for _, p := range byStatus[st] {
			waiting := gate.EvalDepends(p).WaitingOn
			card := kanbanCard{
				ID:          p.ID,
				ShortID:     p.ShortID,
				Title:       p.Title,
				Href:        "/scope/" + name + "/" + p.ShortID,
				Tags:        p.Tags,
				WaitingOn:   waitLinks(waiting),
				SchemaError: p.SchemaError,
				Next:        nextID != "" && p.ID == nextID,
			}
			card.CanClaim, card.MarkStatuses = ticketWriteControls(schema, p.Status, p.ParseError)
			col.Cards = append(col.Cards, card)
		}
		cols = append(cols, col)
	}

	distinct, err := s.db.ScopeDistinctTags(name)
	if err != nil {
		return err
	}
	ch, err := s.pageChrome(reg, name, "", navBoard, r)
	if err != nil {
		return err
	}
	var createStatuses []string
	if schema != nil {
		createStatuses = knownStatusNames(schema)
	}
	return s.render(w, "kanban", kanbanPage{
		Title:          name,
		Chrome:         ch,
		Name:           name,
		Backlog:        backlog,
		Archived:       archived,
		Tags:           distinct,
		Active:         tags,
		Next:           nextID,
		NextHref:       nextHref,
		Cols:           cols,
		CanWrite:       schema != nil,
		CreateStatuses: createStatuses,
	})
}

func (c kanbanCard) FilterText() string {
	var b strings.Builder
	b.WriteString(c.ID)
	b.WriteByte(' ')
	b.WriteString(c.ShortID)
	b.WriteByte(' ')
	b.WriteString(c.Title)
	for _, t := range c.Tags {
		b.WriteByte(' ')
		b.WriteString(t)
	}
	for _, w := range c.WaitingOn {
		b.WriteByte(' ')
		b.WriteString(w.ID)
	}
	return b.String()
}

func waitLinks(ids []string) []idLink {
	if len(ids) == 0 {
		return nil
	}
	out := make([]idLink, len(ids))
	for i, id := range ids {
		out[i] = idLink{ID: id, Href: inspectHref(id)}
	}
	return out
}

func kanbanColumns(custom map[string]status.Category, backlog, archived bool, present []string) []string {
	seen := map[string]bool{}
	var cols []string
	add := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		cols = append(cols, name)
	}
	if backlog {
		for _, name := range categoryNames(custom, status.CategoryBacklog) {
			add(name)
		}
	}
	add(status.Blocked)
	add(status.Draft)
	add(status.Todo)
	add(status.InProgress)
	add(status.Review)
	var active []string
	for name, cat := range custom {
		if cat == status.CategoryActive {
			active = append(active, name)
		}
	}
	sort.Strings(active)
	for _, name := range active {
		add(name)
	}
	if archived {
		for _, name := range status.TerminalNames(custom) {
			add(name)
		}
	}
	if backlog && archived {
		var unknown []string
		seenPresent := map[string]bool{}
		for _, st := range present {
			if st == "" || seen[st] || seenPresent[st] {
				continue
			}
			seenPresent[st] = true
			unknown = append(unknown, st)
		}
		sort.Strings(unknown)
		for _, name := range unknown {
			add(name)
		}
	}
	return cols
}

func categoryNames(custom map[string]status.Category, cat status.Category) []string {
	var out []string
	for _, name := range status.Builtins() {
		c, ok := status.CategoryOf(name, nil)
		if ok && c == cat {
			out = append(out, name)
		}
	}
	var extra []string
	for name, c := range custom {
		if c == cat {
			extra = append(extra, name)
		}
	}
	sort.Strings(extra)
	return append(out, extra...)
}

type inspectPage struct {
	Title        string
	Chrome       chrome
	ID           string
	Status       string
	Order        string
	TicketTitle  string
	Summary      string
	Tags         []string
	Created      string
	Custom       []customField
	Archived     bool
	SchemaErr    bool
	Path         string
	ParseMsg     string
	RawText      string
	Body         template.HTML
	Depends      []neighbour
	Depended     []neighbour
	Related      []neighbour
	Links        []string
	CanClaim     bool
	CanMeta      bool
	MarkStatuses []string
}

type customField struct {
	Key        string
	Scalar     string
	Values     []string
	Multi      bool
	Writable   bool
	Undeclared bool
}

type metaAddView struct {
	Scope, ID, Key, Placeholder string
}

type metaRmView struct {
	Scope, ID, Key, Value string
}

type metaSetView struct {
	Scope, ID, Key string
}

func (p inspectPage) MetaAdd(key, placeholder string) metaAddView {
	return metaAddView{Scope: p.Chrome.Selected, ID: p.ID, Key: key, Placeholder: placeholder}
}

func (p inspectPage) MetaRm(key, value string) metaRmView {
	return metaRmView{Scope: p.Chrome.Selected, ID: p.ID, Key: key, Value: value}
}

func (p inspectPage) MetaSet(key string) metaSetView {
	return metaSetView{Scope: p.Chrome.Selected, ID: p.ID, Key: key}
}

type neighbourListView struct {
	Scope, ID, Key, Placeholder string
	CanMeta                     bool
	Items                       []neighbour
}

func (v neighbourListView) MetaAdd() metaAddView {
	return metaAddView{Scope: v.Scope, ID: v.ID, Key: v.Key, Placeholder: v.Placeholder}
}

func (v neighbourListView) MetaRm(id string) metaRmView {
	return metaRmView{Scope: v.Scope, ID: v.ID, Key: v.Key, Value: id}
}

func (p inspectPage) neighbourList(key, placeholder string, items []neighbour, writable bool) neighbourListView {
	return neighbourListView{
		Scope:       p.Chrome.Selected,
		ID:          p.ID,
		Key:         key,
		Placeholder: placeholder,
		CanMeta:     p.CanMeta && writable,
		Items:       items,
	}
}

func (p inspectPage) DependsList() neighbourListView {
	return p.neighbourList("depends", "full id", p.Depends, true)
}

func (p inspectPage) RelatedList() neighbourListView {
	return p.neighbourList("related", "full id", p.Related, true)
}

func (p inspectPage) DependedList() neighbourListView {
	return p.neighbourList("", "", p.Depended, false)
}

type neighbour struct {
	ID         string
	Status     string
	Title      string
	Href       string
	Unresolved bool
	Owned      bool
}

func (s *Server) inspect(w http.ResponseWriter, r *http.Request) error {
	name := r.PathValue("name")
	idArg := r.PathValue("id")
	if !id.IsScopeName(name) {
		return errNotFound("unknown scope")
	}
	full, ok := parseIDArg(idArg)
	if !ok {
		return errNotFound(fmt.Sprintf("unknown ticket id %q", idArg))
	}
	if full && id.ScopeOfFullID(idArg) != name {
		return errNotFound(fmt.Sprintf("ticket %q does not belong to scope %q", idArg, name))
	}
	reg, err := s.loadRegistry()
	if err != nil {
		return err
	}
	entry, ok := reg.Scopes[name]
	if !ok {
		return errNotFound(fmt.Sprintf("unknown scope %q", name))
	}
	if _, err := s.rec.Reconcile(map[string]string{name: entry.Dir}, registeredSet(reg), nowNS()); err != nil {
		return err
	}

	var rows []*index.Ticket
	if full {
		rows, err = s.db.TicketsByID(name, idArg)
	} else {
		rows, err = s.db.TicketsByShortID(name, idArg)
	}
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return errNotFound(fmt.Sprintf("unknown ticket id %q", idArg))
	}
	if len(rows) > 1 {
		paths := make([]string, len(rows))
		for i, p := range rows {
			paths[i] = p.Path
		}
		sort.Strings(paths)
		return errDuplicate(rows[0].ID, paths)
	}
	p := rows[0]
	page, err := s.inspectPage(reg, p)
	if err != nil {
		return err
	}
	s.bindChrome(&page.Chrome, r)
	return s.render(w, "inspect", page)
}

func (s *Server) inspectPage(reg *registry.Registry, p *index.Ticket) (inspectPage, error) {
	ch, err := s.chromeFor(reg, p.Scope, "", navBoard)
	if err != nil {
		return inspectPage{}, err
	}
	var schema *scopeconfig.Schema
	if entry, ok := reg.Scopes[p.Scope]; ok {
		schema = s.rec.SchemaCached(p.Scope, entry.Dir)
	}
	out := inspectPage{
		Title:       p.ID,
		Chrome:      ch,
		ID:          p.ID,
		Status:      p.Status,
		Order:       p.OrderKey,
		TicketTitle: p.Title,
		Summary:     p.Summary,
		Tags:        p.Tags,
		Created:     p.Created,
		Custom:      inspectCustoms(schema, p.Custom, schema != nil && !p.ParseError),
		Archived:    p.Archived,
		SchemaErr:   p.SchemaError,
		Path:        p.Path,
		ParseMsg:    p.ParseMsg,
	}
	out.CanClaim, out.MarkStatuses = ticketWriteControls(schema, p.Status, p.ParseError)
	out.CanMeta = schema != nil && !p.ParseError

	raw, err := os.ReadFile(p.Path)
	if err != nil {
		if out.ParseMsg == "" {
			out.ParseMsg = fmt.Sprintf("read %s: %v", p.Path, err)
		}
	} else {
		interior, body, present := frontmatter.Split(raw)
		if present {
			if m, err := frontmatter.Parse(interior); err == nil {
				out.Links = m.Links
			}
		}
		if p.ParseError {
			out.RawText = string(raw)
		} else {
			if !present {
				body = raw
			}
			html, err := renderMarkdown(body)
			if err != nil {
				return inspectPage{}, err
			}
			out.Body = html
		}
	}

	out.Depends, out.Depended, out.Related, err = s.inspectGraph(p.ID)
	if err != nil {
		return inspectPage{}, err
	}
	return out, nil
}

func inspectCustoms(schema *scopeconfig.Schema, present map[string]any, writable bool) []customField {
	declared := map[string]scopeconfig.Field{}
	if schema != nil {
		declared = schema.Fields
	}
	names := make([]string, 0, len(declared))
	for name := range declared {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]customField, 0, len(names)+len(present))
	seen := map[string]bool{}
	for _, name := range names {
		seen[name] = true
		f := declared[name]
		cf := customField{Key: name, Writable: writable}
		v, ok := present[name]
		if f.Type == scopeconfig.FieldStrings {
			cf.Multi = true
			if !ok {
				out = append(out, cf)
				continue
			}
			list, err := frontmatter.StringList(v)
			if err != nil {
				cf.Scalar = formatValue(v)
				cf.Writable = false
				cf.Multi = false
			} else {
				cf.Values = list
			}
			out = append(out, cf)
			continue
		}
		if ok {
			cf.Scalar = formatValue(v)
		}
		out = append(out, cf)
	}
	var extra []string
	for k := range present {
		if !seen[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	for _, k := range extra {
		out = append(out, customField{
			Key:        k,
			Scalar:     formatValue(present[k]),
			Undeclared: true,
		})
	}
	return out
}

func formatValue(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(b)
	}
}

func (s *Server) inspectGraph(fullID string) (depends, depended, related []neighbour, err error) {
	from, err := s.db.EdgesFromID(fullID)
	if err != nil {
		return nil, nil, nil, err
	}
	to, err := s.db.EdgesByTarget(fullID)
	if err != nil {
		return nil, nil, nil, err
	}
	var ids []string
	seenID := map[string]bool{}
	add := func(id string) {
		if id == "" || seenID[id] {
			return
		}
		seenID[id] = true
		ids = append(ids, id)
	}
	for _, e := range from {
		add(e.ToID)
	}
	for _, e := range to {
		add(e.FromID)
	}
	tickets, err := s.db.TicketsByFullIDs(ids)
	if err != nil {
		return nil, nil, nil, err
	}
	byID := map[string]*index.Ticket{}
	for _, t := range tickets {
		if _, ok := byID[t.ID]; !ok {
			byID[t.ID] = t
		}
	}
	seenDep, seenBy, seenRel := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, e := range from {
		switch e.Kind {
		case index.EdgeDepends:
			if seenDep[e.ToID] {
				continue
			}
			seenDep[e.ToID] = true
			depends = append(depends, makeNeighbour(e.ToID, byID, true))
		case index.EdgeRelated:
			if seenRel[e.ToID] {
				continue
			}
			seenRel[e.ToID] = true
			related = append(related, makeNeighbour(e.ToID, byID, true))
		}
	}
	for _, e := range to {
		switch e.Kind {
		case index.EdgeDepends:
			if seenBy[e.FromID] {
				continue
			}
			seenBy[e.FromID] = true
			depended = append(depended, makeNeighbour(e.FromID, byID, false))
		case index.EdgeRelated:
			if seenRel[e.FromID] {
				continue
			}
			seenRel[e.FromID] = true
			related = append(related, makeNeighbour(e.FromID, byID, false))
		}
	}
	return depends, depended, related, nil
}

func makeNeighbour(fullID string, byID map[string]*index.Ticket, owned bool) neighbour {
	n := neighbour{ID: fullID, Href: inspectHref(fullID), Owned: owned}
	if t := byID[fullID]; t != nil {
		n.Status = t.Status
		n.Title = t.Title
		return n
	}
	n.Unresolved = true
	return n
}

type searchPage struct {
	Title  string
	Chrome chrome
	Query  string
	Scope  string
	Hits   []searchHitView
}

type searchHitView struct {
	ID      string
	Status  string
	Title   string
	Summary string
	Scope   string
	Href    string
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) error {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	scope := strings.TrimSpace(r.URL.Query().Get("scope"))
	reg, err := s.loadRegistry()
	if err != nil {
		return err
	}
	if scope != "" {
		if !id.IsScopeName(scope) {
			return errNotFound(fmt.Sprintf("unknown scope %q", scope))
		}
		if _, ok := reg.Scopes[scope]; !ok {
			return errNotFound(fmt.Sprintf("unknown scope %q", scope))
		}
	}
	targets := allTargets(reg)
	if scope != "" {
		targets = map[string]string{scope: reg.Scopes[scope].Dir}
	}
	if _, err := s.rec.Reconcile(targets, registeredSet(reg), nowNS()); err != nil {
		return err
	}
	ch, err := s.pageChrome(reg, scope, q, navSearch, r)
	if err != nil {
		return err
	}
	page := searchPage{Title: "search", Chrome: ch, Query: q, Scope: scope}
	if q == "" {
		return s.render(w, "search", page)
	}
	hits, err := s.db.Search(scope, q)
	if err != nil {
		if errors.Is(err, index.ErrSearchQuery) {
			return errBadRequest(fmt.Sprintf("malformed search query %q", q))
		}
		return err
	}
	page.Hits = make([]searchHitView, 0, len(hits))
	for _, h := range hits {
		p := h.Ticket
		page.Hits = append(page.Hits, searchHitView{
			ID:      p.ID,
			Status:  p.Status,
			Title:   p.Title,
			Summary: p.Summary,
			Scope:   p.Scope,
			Href:    inspectHref(p.ID),
		})
	}
	return s.render(w, "search", page)
}

func searchQuery(q, scope string) string {
	v := url.Values{}
	if q != "" {
		v.Set("q", q)
	}
	if scope != "" {
		v.Set("scope", scope)
	}
	enc := v.Encode()
	if enc == "" {
		return "/search"
	}
	return "/search?" + enc
}

func (p searchPage) AllHref() string { return searchQuery(p.Query, "") }

func (p searchPage) ScopeHref(name string) string { return searchQuery(p.Query, name) }

func (p searchPage) ScopeOn(name string) bool { return p.Scope == name }

func boardQuery(backlog, archived bool, tags []string) string {
	v := url.Values{}
	if backlog {
		v.Set("backlog", "1")
	}
	if archived {
		v.Set("archived", "1")
	}
	for _, t := range tags {
		v.Add("tag", t)
	}
	enc := v.Encode()
	if enc == "" {
		return ""
	}
	return "?" + enc
}

func tagActive(tags []string, tag string) bool {
	for _, t := range tags {
		if t == tag {
			return true
		}
	}
	return false
}

func (p kanbanPage) BacklogHref() string {
	return "/scope/" + p.Name + boardQuery(!p.Backlog, p.Archived, p.Active)
}

func (p kanbanPage) ArchivedHref() string {
	return "/scope/" + p.Name + boardQuery(p.Backlog, !p.Archived, p.Active)
}

func (p kanbanPage) TagHref(tag string) string {
	var next []string
	if tagActive(p.Active, tag) {
		for _, t := range p.Active {
			if t != tag {
				next = append(next, t)
			}
		}
	} else {
		next = append(append([]string{}, p.Active...), tag)
	}
	return "/scope/" + p.Name + boardQuery(p.Backlog, p.Archived, next)
}

func (p kanbanPage) TagOn(tag string) bool { return tagActive(p.Active, tag) }
