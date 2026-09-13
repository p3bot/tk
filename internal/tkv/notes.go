package tkv

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"path/filepath"
	"strings"

	"cuelang.org/go/cue/cuecontext"

	"github.com/p3bot/tk/internal/gitstate"
	"github.com/p3bot/tk/internal/id"
	"github.com/p3bot/tk/internal/notes"
	"github.com/p3bot/tk/internal/pathutil"
	"github.com/p3bot/tk/internal/reconcile"
	"github.com/p3bot/tk/internal/registry"
	"github.com/p3bot/tk/internal/scopefile"
	"github.com/p3bot/tk/internal/writeengine"
)

type noteListRow struct {
	Slug    string
	Href    string
	Default bool
	Current bool
	Missing bool
}

type noteInspectPage struct {
	Title         string
	Chrome        chrome
	Slug          string
	Path          string
	Default       bool
	CanClear      bool
	Missing       bool
	Editing       bool
	ReturnInspect bool
	CanEdit       bool
	SnapMsg       string
	Body          template.HTML
	EditBody      string
	Base          string
	Rows          []noteListRow
}

func (p noteInspectPage) EditHref() string {
	return noteEditHref(p.Chrome.Selected, p.Slug)
}

func (p noteInspectPage) ViewHref() string {
	def := ""
	if p.Default {
		def = p.Slug
	}
	return noteViewHref(p.Chrome.Selected, p.Slug, def)
}

func (p noteInspectPage) FileAriaCurrent(row noteListRow) string {
	if !row.Current {
		return ""
	}
	if stripQuery(p.Chrome.Return) == row.Href {
		return "page"
	}
	return "true"
}

func noteFileRows(name, def, current string, slugs []string) []noteListRow {
	haveDef := false
	for _, slug := range slugs {
		if slug == def {
			haveDef = true
			break
		}
	}
	out := make([]noteListRow, 0, len(slugs)+1)
	inserted := haveDef
	for _, slug := range slugs {
		if !inserted && def < slug {
			out = append(out, noteFileRow(name, def, current, def, true))
			inserted = true
		}
		out = append(out, noteFileRow(name, def, current, slug, false))
	}
	if !inserted {
		out = append(out, noteFileRow(name, def, current, def, true))
	}
	return out
}

func noteFileRow(name, def, current, slug string, missing bool) noteListRow {
	href := noteHref(name, slug)
	if slug == def {
		href = notesListHref(name)
	}
	return noteListRow{
		Slug:    slug,
		Href:    href,
		Default: slug == def,
		Current: slug == current,
		Missing: missing,
	}
}

type notesPickPage struct {
	Title  string
	Chrome chrome
	Lead   string
}

func (s *Server) notesPick(w http.ResponseWriter, r *http.Request) error {
	reg, err := s.loadRegistry()
	if err != nil {
		return err
	}
	ch, err := s.pageChrome(reg, "", "", navNotes, r)
	if err != nil {
		return err
	}
	return s.render(w, "notes-pick", notesPickPage{
		Title:  "notes",
		Chrome: ch,
		Lead:   "Pick a scope to read its notes.",
	})
}

func (s *Server) notesList(w http.ResponseWriter, r *http.Request) error {
	name := r.PathValue("name")
	if !id.IsScopeName(name) {
		return errNotFound("unknown scope")
	}
	reg, err := s.loadRegistry()
	if err != nil {
		return err
	}
	entry, ok := reg.Scopes[name]
	if !ok {
		return errNotFound(fmt.Sprintf("unknown scope %q", name))
	}
	configDir, err := s.app.configDir()
	if err != nil {
		return err
	}
	def, err := notes.EffectiveSlug(reg, configDir, name)
	if err != nil {
		return err
	}
	slugs, err := notes.List(name, entry.Dir)
	if err != nil {
		return err
	}
	file := scopefile.NoteFile(entry.Dir, def)
	raw, base, err := notes.FileSnapshot(file)
	var snapMsg string
	if err != nil {
		if !isNotesNonRegular(err) {
			return mapNotesError(err)
		}
		// Keep the list so other notes stay reachable; do not pretend
		// the path is missing (inspect/edit of this slug still 409).
		snapMsg = err.Error()
		raw, base = nil, ""
	}
	page, err := s.notePage(reg, name, def, def, slugs, file, raw, base, false)
	if err != nil {
		return err
	}
	if snapMsg != "" {
		page.SnapMsg = snapMsg
		page.CanEdit = false
		if abs, err := filepath.Abs(file); err == nil {
			page.Path = abs
		}
		for i := range page.Rows {
			if page.Rows[i].Default {
				page.Rows[i].Missing = false
			}
		}
	}
	s.bindChrome(&page.Chrome, r)
	return s.render(w, "note", page)
}

func isNotesNonRegular(err error) bool {
	var nr *notes.NonRegularError
	return errors.As(err, &nr)
}

func (s *Server) noteInspect(w http.ResponseWriter, r *http.Request) error {
	return s.serveNote(w, r, false)
}

func (s *Server) noteEdit(w http.ResponseWriter, r *http.Request) error {
	return s.serveNote(w, r, true)
}

func (s *Server) serveNote(w http.ResponseWriter, r *http.Request, edit bool) error {
	name := r.PathValue("name")
	if !id.IsScopeName(name) {
		return errNotFound("unknown scope")
	}
	slug, err := parseNoteSlug(r.PathValue("slug"))
	if err != nil {
		return err
	}
	reg, err := s.loadRegistry()
	if err != nil {
		return err
	}
	entry, ok := reg.Scopes[name]
	if !ok {
		return errNotFound(fmt.Sprintf("unknown scope %q", name))
	}
	configDir, err := s.app.configDir()
	if err != nil {
		return err
	}
	def, err := notes.EffectiveSlug(reg, configDir, name)
	if err != nil {
		return err
	}
	if err := notes.RequireDir(name, entry.Dir); err != nil {
		return err
	}
	slugs, err := notes.List(name, entry.Dir)
	if err != nil {
		return err
	}
	file := scopefile.NoteFile(entry.Dir, slug)
	body, base, err := notes.FileSnapshot(file)
	if err != nil {
		return mapNotesError(err)
	}
	page, err := s.notePage(reg, name, slug, def, slugs, file, body, base, edit)
	if err != nil {
		return err
	}
	page.ReturnInspect = true
	s.bindChrome(&page.Chrome, r)
	if edit {
		return s.render(w, "note-edit", page)
	}
	return s.render(w, "note", page)
}

func (s *Server) notePage(reg *registry.Registry, name, slug, def string, slugs []string, file string, body []byte, base string, edit bool) (noteInspectPage, error) {
	path, err := filepath.Abs(file)
	if err != nil {
		return noteInspectPage{}, err
	}
	ch, err := s.chromeFor(reg, name, "", navNotes)
	if err != nil {
		return noteInspectPage{}, err
	}
	title := slug
	if slug == def && !edit {
		title = "notes"
	}
	page := noteInspectPage{
		Title:    title,
		Chrome:   ch,
		Slug:     slug,
		Path:     pathutil.Canonical(path),
		Default:  slug == def,
		CanClear: slug == def && slug != scopefile.NoteDefaultSlug,
		Missing:  base == notes.MissingClobberKey,
		Editing:  edit,
		CanEdit:  true,
		EditBody: string(body),
		Base:     base,
		Rows:     noteFileRows(name, def, slug, slugs),
	}
	if len(body) > 0 {
		html, _, err := convertMarkdown(body)
		if err != nil {
			return noteInspectPage{}, err
		}
		page.Body = html
	}
	return page, nil
}

func (s *Server) postNoteCreate(w http.ResponseWriter, r *http.Request) error {
	if err := r.ParseForm(); err != nil {
		return errBadRequest("malformed form")
	}
	name := r.PathValue("name")
	if !id.IsScopeName(name) {
		return errNotFound("unknown scope")
	}
	slug, err := parseNoteSlug(r.FormValue("slug"))
	if err != nil {
		return err
	}
	if _, err := s.notesXDG(r.Context(), name); err != nil {
		return err
	}
	http.Redirect(w, r, noteEditHref(name, slug), http.StatusSeeOther)
	return nil
}

func (s *Server) postNoteSet(w http.ResponseWriter, r *http.Request) error {
	if err := r.ParseForm(); err != nil {
		return errBadRequest("malformed form")
	}
	name := r.PathValue("name")
	if !id.IsScopeName(name) {
		return errNotFound("unknown scope")
	}
	slug, err := parseNoteSlug(r.PathValue("slug"))
	if err != nil {
		return err
	}
	base := strings.TrimSpace(r.FormValue("base"))
	if base == "" {
		return errBadRequest("set needs a clobber predicate")
	}
	body := r.FormValue("body")
	if body == "" {
		return errBadRequest("set needs non-empty text")
	}

	sess, release, err := s.beginNotes(r.Context(), name)
	if err != nil {
		return err
	}
	defer release()

	def, err := notes.EffectiveSlug(sess.deps.Reg, sess.deps.ConfigDir, name)
	if err != nil {
		return err
	}

	res, err := notes.Set(sess.deps, notes.Input{
		Scope: name,
		Dir:   sess.dir,
		Slug:  slug,
		Base:  base,
	}, []byte(body))
	return s.finishNotes(w, r, noteViewHref(name, slug, def), res, err)
}

func (s *Server) postNoteDelete(w http.ResponseWriter, r *http.Request) error {
	if err := r.ParseForm(); err != nil {
		return errBadRequest("malformed form")
	}
	name := r.PathValue("name")
	if !id.IsScopeName(name) {
		return errNotFound("unknown scope")
	}
	slug, err := parseNoteSlug(r.PathValue("slug"))
	if err != nil {
		return err
	}

	sess, release, err := s.beginNotes(r.Context(), name)
	if err != nil {
		return err
	}
	defer release()

	res, err := notes.Delete(sess.deps, notes.Input{Scope: name, Dir: sess.dir, Slug: slug})
	return s.finishNotes(w, r, notesListHref(name), res, err)
}

func (s *Server) postNoteUse(w http.ResponseWriter, r *http.Request) error {
	if err := r.ParseForm(); err != nil {
		return errBadRequest("malformed form")
	}
	name := r.PathValue("name")
	if !id.IsScopeName(name) {
		return errNotFound("unknown scope")
	}
	clearUse := r.FormValue("clear") == "1"
	in := notes.UseInput{Scope: name, Clear: clearUse}
	if !clearUse {
		slug, err := parseNoteSlug(r.FormValue("slug"))
		if err != nil {
			return err
		}
		in.Slug = slug
	}

	deps, err := s.notesXDG(r.Context(), name)
	if err != nil {
		return err
	}
	res, err := notes.Use(deps, in)
	return s.finishNotes(w, r, notesReturnHref(r, name), res, err)
}

// notesReturnHref is the inspect-vs-list switch used by ticket writes: only
// return=inspect plus a legal slug leaves inspect; anything else is the list.
func notesReturnHref(r *http.Request, name string) string {
	if strings.TrimSpace(r.FormValue("return")) != "inspect" {
		return notesListHref(name)
	}
	slug, err := parseNoteSlug(r.FormValue("slug"))
	if err != nil {
		return notesListHref(name)
	}
	return noteHref(name, slug)
}

func (s *Server) notesXDG(ctx context.Context, name string) (notes.Deps, error) {
	ctx = context.WithoutCancel(ctx)
	configDir, err := s.app.configDir()
	if err != nil {
		return notes.Deps{}, err
	}
	cueCtx := cuecontext.New()
	reg, err := registry.NewStore(cueCtx, configDir).Load()
	if err != nil {
		return notes.Deps{}, err
	}
	if _, ok := reg.Scopes[name]; !ok {
		return notes.Deps{}, errNotFound(fmt.Sprintf("unknown scope %q", name))
	}
	return notes.Deps{
		Ctx:       ctx,
		Cue:       cueCtx,
		ConfigDir: configDir,
		Reg:       reg,
	}, nil
}

type notesSession struct {
	deps notes.Deps
	dir  string
}

func (s *Server) beginNotes(ctx context.Context, name string) (notesSession, func(), error) {
	ctx = context.WithoutCancel(ctx)
	var (
		reg *registry.Registry
		dir string
	)
	h, err := s.withIndexPin(func() error {
		var e error
		reg, dir, e = s.scopeForWrite(name)
		return e
	})
	if err != nil {
		return notesSession{}, nil, err
	}
	deps, err := s.notesDeps(ctx, reg, h)
	if err != nil {
		s.releaseHandle(h)
		return notesSession{}, nil, err
	}
	if s.afterIndexUnlock != nil {
		s.afterIndexUnlock()
	}
	return notesSession{deps: deps, dir: dir}, func() { s.releaseHandle(h) }, nil
}

func (s *Server) notesDeps(ctx context.Context, reg *registry.Registry, h *indexHandle) (notes.Deps, error) {
	stateDir, err := s.app.stateDir()
	if err != nil {
		return notes.Deps{}, err
	}
	configDir, err := s.app.configDir()
	if err != nil {
		return notes.Deps{}, err
	}
	cueCtx := cuecontext.New()
	return notes.Deps{
		Ctx:       ctx,
		Cue:       cueCtx,
		StateDir:  stateDir,
		ConfigDir: configDir,
		Reg:       reg,
		Rec:       reconcile.New(h.db, cueCtx),
	}, nil
}

func (s *Server) finishNotes(w http.ResponseWriter, r *http.Request, loc string, res notes.Result, err error) error {
	if err != nil {
		return mapNotesError(err)
	}
	http.Redirect(w, r, appendNotices(loc, writeengine.Result{SyncNeeded: res.SyncNeeded}), http.StatusSeeOther)
	return nil
}

func parseNoteSlug(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", errBadRequest("missing note slug")
	}
	name, err := notes.ParseSlug(s)
	if err != nil {
		return "", mapNotesError(err)
	}
	return name, nil
}

func mapNotesError(err error) error {
	if err == nil {
		return nil
	}
	var he *httpError
	if errors.As(err, &he) {
		return he
	}
	var use *notes.UsageError
	if errors.As(err, &use) {
		return errBadRequest(use.Error())
	}
	var cl *notes.ClobberError
	if errors.As(err, &cl) {
		return errConflict(cl.Error())
	}
	var nr *notes.NonRegularError
	if errors.As(err, &nr) {
		return errConflict(nr.Error())
	}
	var miss *notes.MissingError
	if errors.As(err, &miss) {
		return errNotFound(miss.Error())
	}
	var mid *gitstate.MidRebaseError
	if errors.As(err, &mid) {
		return errConflict(mid.Error())
	}
	return err
}
