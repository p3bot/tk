package tkv

import (
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/p3bot/tk/internal/bodyedit"
	"github.com/p3bot/tk/internal/design"
	"github.com/p3bot/tk/internal/frontmatter"
	"github.com/p3bot/tk/internal/gitstate"
	"github.com/p3bot/tk/internal/id"
	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/registry"
	"github.com/p3bot/tk/internal/scopeconfig"
	"github.com/p3bot/tk/internal/title"
	"github.com/p3bot/tk/internal/writeengine"
)

var designMarkStatuses = []string{
	design.StatusDraft,
	design.StatusAccepted,
	design.StatusDecomposed,
	design.StatusSuperseded,
}

type designsPickPage struct {
	Title  string
	Chrome chrome
	Lead   string
}

type designListPage struct {
	Title  string
	Chrome chrome
	Name   string
	All    bool
	Hidden int
	Rows   []designListRow
	Broken []designListRow
}

type designListRow struct {
	ID         string
	Status     string
	Title      string
	Href       string
	Dwell      string
	DwellStamp string
}

func (p designListPage) AllHref() string {
	base := designsListHref(p.Name)
	if p.All {
		return base
	}
	return base + "?all=1"
}

// EmptyNote is the sentence for an empty status table. Broken files have their
// own section, so they are not an empty scope.
func (p designListPage) EmptyNote() string {
	if len(p.Rows) > 0 {
		return ""
	}
	if p.Hidden > 0 {
		return "No draft or accepted designs."
	}
	if len(p.Broken) > 0 {
		return ""
	}
	return "No designs."
}

type designPage struct {
	Title        string
	Chrome       chrome
	ID           string
	Status       string
	Created      string
	Changed      string
	Path         string
	ParseMsg     string
	RawText      string
	Body         template.HTML
	TOC          []tocItem
	Produces     []designProduce
	ProducesMsg  string
	CanProduces  bool
	CanEdit      bool
	Base         string
	EditTitle    string
	EditLead     string
	EditBody     string
	MarkStatuses []string
}

type designProduce struct {
	ID     string
	Href   string
	Status string
	Title  string
}

func (p designPage) EditHref() string { return designEditHref(p.ID) }

func (p designPage) ViewHref() string { return designHref(p.ID) }

func (s *Server) designsPick(w http.ResponseWriter, r *http.Request) error {
	reg, err := s.loadRegistry()
	if err != nil {
		return err
	}
	ch, err := s.pageChrome(reg, "", "", navDesigns, r)
	if err != nil {
		return err
	}
	return s.render(w, "designs-pick", designsPickPage{
		Title:  "designs",
		Chrome: ch,
		Lead:   "Pick a scope to read its designs.",
	})
}

func (s *Server) designsList(w http.ResponseWriter, r *http.Request) error {
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
	if res.Unreachable[name] {
		return errNotFound(fmt.Sprintf("scope %q is not reachable", name))
	}
	rows, err := s.db.ScopeDesigns(name)
	if err != nil {
		return err
	}
	all := r.URL.Query().Get("all") == "1"
	now := time.Now()
	page := designListPage{
		Title:  "designs",
		Name:   name,
		All:    all,
		Rows:   []designListRow{},
		Broken: []designListRow{},
	}
	var listed []*index.Design
	var broken []*index.Design
	hidden := 0
	for _, p := range rows {
		if p.ParseError {
			broken = append(broken, p)
			continue
		}
		if !designListed(p.Status, all) {
			hidden++
			continue
		}
		listed = append(listed, p)
	}
	page.Hidden = hidden
	sort.Slice(listed, func(i, j int) bool { return designLess(listed[i], listed[j]) })
	sort.Slice(broken, func(i, j int) bool { return broken[i].ID < broken[j].ID })
	for _, p := range listed {
		row := designListRow{ID: p.ID, Status: p.Status, Title: p.Title, Href: designHref(p.ID)}
		if label, ok := statusDwell(p.Changed, now); ok {
			row.Dwell = label
			row.DwellStamp = p.Changed
		}
		page.Rows = append(page.Rows, row)
	}
	for _, p := range broken {
		page.Broken = append(page.Broken, designListRow{ID: p.ID, Href: designHref(p.ID)})
	}
	ch, err := s.pageChrome(reg, name, "", navDesigns, r)
	if err != nil {
		return err
	}
	page.Chrome = ch
	return s.render(w, "designs", page)
}

func (s *Server) designInspect(w http.ResponseWriter, r *http.Request) error {
	return s.serveDesign(w, r, false)
}

func (s *Server) designEdit(w http.ResponseWriter, r *http.Request) error {
	return s.serveDesign(w, r, true)
}

func (s *Server) serveDesign(w http.ResponseWriter, r *http.Request, edit bool) error {
	name := r.PathValue("name")
	idArg := r.PathValue("id")
	if !id.IsScopeName(name) {
		return errNotFound("unknown scope")
	}
	in, err := designIDInput(name, idArg)
	if err != nil {
		return err
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
	if res.Unreachable[name] {
		return errNotFound(fmt.Sprintf("scope %q is not reachable", name))
	}
	rows, err := designRows(s.db, in)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return errNotFound(fmt.Sprintf("unknown design id %q", idArg))
	}
	if len(rows) > 1 {
		paths := make([]string, len(rows))
		for i, p := range rows {
			paths[i] = p.Path
		}
		sort.Strings(paths)
		return errDuplicate(rows[0].ID, paths)
	}
	page, err := s.designView(reg, res.Schema(name), rows[0])
	if err != nil {
		return err
	}
	s.bindChrome(&page.Chrome, r)
	if edit {
		if !page.CanEdit {
			return errBadRequest("design body is not editable")
		}
		return s.render(w, "design-edit", page)
	}
	return s.render(w, "design", page)
}

func (s *Server) designView(reg *registry.Registry, schema *scopeconfig.Schema, p *index.Design) (designPage, error) {
	ch, err := s.chromeFor(reg, p.Scope, "", navDesigns)
	if err != nil {
		return designPage{}, err
	}
	writable := rowWritable(schema, p.ParseError)
	out := designPage{
		Title:    p.ID,
		Chrome:   ch,
		ID:       p.ID,
		Status:   p.Status,
		Created:  p.Created,
		Changed:  p.Changed,
		Path:     p.Path,
		ParseMsg: p.ParseMsg,
	}
	if writable {
		out.MarkStatuses = designMarkStatuses
	}
	raw, key, err := bodyedit.Snapshot(p.Path)
	if err != nil {
		if out.ParseMsg == "" {
			out.ParseMsg = err.Error()
		}
		return out, nil
	}
	interior, body, present := frontmatter.Split(raw)
	if p.ParseError || !present {
		out.RawText = string(raw)
		return out, nil
	}
	html, toc, err := convertMarkdown(body)
	if err != nil {
		return designPage{}, err
	}
	out.Body = html
	out.TOC = toc
	var model *frontmatter.Model
	if m, err := frontmatter.Parse(interior); err == nil {
		model = m
	}
	ids, err := design.Produces(model)
	if err != nil {
		out.ProducesMsg = "produces is not a string list"
	} else {
		out.CanProduces = writable
		out.Produces, err = s.designProduces(ids)
		if err != nil {
			return designPage{}, err
		}
	}
	if writable {
		prefix, heading, rest := title.SplitH1Parts(body)
		out.CanEdit = true
		out.Base = key
		out.EditTitle = heading
		out.EditLead = string(prefix)
		out.EditBody = string(rest)
	}
	return out, nil
}

func (s *Server) designProduces(ids []string) ([]designProduce, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	tickets, err := s.db.TicketsByFullIDs(ids)
	if err != nil {
		return nil, err
	}
	byID := map[string]*index.Ticket{}
	for _, t := range tickets {
		if _, ok := byID[t.ID]; !ok {
			byID[t.ID] = t
		}
	}
	out := make([]designProduce, len(ids))
	for i, id := range ids {
		item := designProduce{ID: id}
		if t := byID[id]; t != nil {
			item.Href = inspectHref(t.ID)
			item.Status = t.Status
			item.Title = t.Title
		}
		out[i] = item
	}
	return out, nil
}

func (s *Server) postDesignMark(w http.ResponseWriter, r *http.Request) error {
	if err := r.ParseForm(); err != nil {
		return errBadRequest("malformed form")
	}
	name := r.PathValue("name")
	if !id.IsScopeName(name) {
		return errNotFound("unknown scope")
	}
	statusName := strings.TrimSpace(r.FormValue("status"))
	if statusName == "" {
		return errBadRequest("missing status")
	}
	in, err := designIDInput(name, r.FormValue("id"))
	if err != nil {
		return err
	}
	sess, release, err := s.beginWrite(r.Context(), name)
	if err != nil {
		return err
	}
	defer release()
	in.Dir = sess.dir
	res, err := design.Mark(designDeps(sess.deps), design.MarkInput{IDInput: in, Status: statusName})
	return s.finishDesign(w, r, res, err)
}

func (s *Server) postDesignProduces(w http.ResponseWriter, r *http.Request) error {
	if err := r.ParseForm(); err != nil {
		return errBadRequest("malformed form")
	}
	name := r.PathValue("name")
	if !id.IsScopeName(name) {
		return errNotFound("unknown scope")
	}
	add, err := designProduceOp(r.FormValue("op"))
	if err != nil {
		return err
	}
	in, err := designIDInput(name, r.FormValue("id"))
	if err != nil {
		return err
	}
	sess, release, err := s.beginWrite(r.Context(), name)
	if err != nil {
		return err
	}
	defer release()
	in.Dir = sess.dir
	res, err := design.MetaAddRemove(designDeps(sess.deps), design.MetaInput{
		IDInput: in,
		Add:     add,
		Target:  strings.TrimSpace(r.FormValue("target")),
	})
	return s.finishDesign(w, r, res, err)
}

func (s *Server) postDesignBody(w http.ResponseWriter, r *http.Request) error {
	if err := r.ParseForm(); err != nil {
		return errBadRequest("malformed form")
	}
	name := r.PathValue("name")
	if !id.IsScopeName(name) {
		return errNotFound("unknown scope")
	}
	in, err := designIDInput(name, r.FormValue("id"))
	if err != nil {
		return err
	}
	sess, release, err := s.beginWrite(r.Context(), name)
	if err != nil {
		return err
	}
	defer release()
	in.Dir = sess.dir
	res, err := design.Splice(designDeps(sess.deps), design.SpliceInput{
		IDInput: in,
		Title:   r.FormValue("title"),
		Lead:    r.FormValue("lead"),
		Body:    r.FormValue("body"),
		Base:    strings.TrimSpace(r.FormValue("base")),
	})
	return s.finishDesign(w, r, res, err)
}

func (s *Server) finishDesign(w http.ResponseWriter, r *http.Request, res design.Result, err error) error {
	if err != nil {
		return mapDesignError(err)
	}
	http.Redirect(w, r, appendNotices(designHref(res.ID), designNotices(res)), http.StatusSeeOther)
	return nil
}

func designDeps(deps writeengine.Deps) design.Deps {
	return design.Deps{
		Ctx:      deps.Ctx,
		StateDir: deps.StateDir,
		Reg:      deps.Reg,
		DB:       deps.DB,
		Rec:      deps.Rec,
	}
}

func designNotices(res design.Result) writeengine.Result {
	out := writeengine.Result{}
	if lines := res.NeededLines(); len(lines) > 0 {
		out.SyncNeeded = strings.Join(lines, "; ")
	}
	if lines := res.DisabledLines(); len(lines) > 0 {
		out.SyncDisabled = strings.Join(lines, "; ")
	}
	return out
}

func designProduceOp(raw string) (bool, error) {
	switch strings.TrimSpace(raw) {
	case "add":
		return true, nil
	case "remove", "rm":
		return false, nil
	case "":
		return false, errBadRequest("missing op")
	default:
		return false, errBadRequest(fmt.Sprintf("unknown produces op %q", raw))
	}
}

func designIDInput(scope, arg string) (design.IDInput, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return design.IDInput{}, errBadRequest("missing design id")
	}
	full, ok := parseIDArg(arg)
	if !ok {
		return design.IDInput{}, errBadRequest(fmt.Sprintf("unknown design id %q", arg))
	}
	if full && id.ScopeOfFullID(arg) != scope {
		return design.IDInput{}, errNotFound(fmt.Sprintf("design %q does not belong to scope %q", arg, scope))
	}
	return design.IDInput{Scope: scope, Arg: arg, Full: full}, nil
}

func designRows(db *index.DB, in design.IDInput) ([]*index.Design, error) {
	if in.Full {
		return db.DesignsByID(in.Scope, in.Arg)
	}
	return db.DesignsByShortID(in.Scope, in.Arg)
}

func mapDesignError(err error) error {
	if err == nil {
		return nil
	}
	var he *httpError
	if errors.As(err, &he) {
		return he
	}
	var unk *design.UnknownStatusError
	if errors.As(err, &unk) {
		return errBadRequest(unk.Error())
	}
	var use *design.UsageError
	if errors.As(err, &use) {
		return errBadRequest(use.Error())
	}
	var unresolved *design.UnresolvedError
	if errors.As(err, &unresolved) {
		return errBadRequest(unresolved.Error())
	}
	var unknown *design.UnknownError
	if errors.As(err, &unknown) {
		return errNotFound(unknown.Error())
	}
	var shared *design.SharedIDError
	if errors.As(err, &shared) {
		return errDuplicate(shared.ID, shared.Paths)
	}
	var pe *design.ParseError
	if errors.As(err, &pe) {
		return errConflict(pe.Error())
	}
	var cl *design.ClobberError
	if errors.As(err, &cl) {
		return errConflict(cl.Error())
	}
	var mid *gitstate.MidRebaseError
	if errors.As(err, &mid) {
		return errConflict(mid.Error())
	}
	return err
}

func designListed(status string, all bool) bool {
	if all {
		return true
	}
	return design.DefaultListed(status)
}

func designLess(a, b *index.Design) bool {
	if design.CreatedBefore(a.Created, b.Created) {
		return true
	}
	if design.CreatedBefore(b.Created, a.Created) {
		return false
	}
	return a.ID < b.ID
}
