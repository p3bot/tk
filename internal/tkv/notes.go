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

type notesListPage struct {
	Title          string
	Chrome         chrome
	DefaultSlug    string
	DefaultHref    string
	DefaultMissing bool
	CanClear       bool
	Rows           []noteListRow
}

type noteListRow struct {
	Slug    string
	Href    string
	Default bool
}

type noteInspectPage struct {
	Title    string
	Chrome   chrome
	Slug     string
	Path     string
	Default  bool
	CanClear bool
	Missing  bool
	Body     template.HTML
	EditBody string
	Base     string
	ListHref string
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
	ch, err := s.chromeFor(reg, name, "", navNotes)
	if err != nil {
		return err
	}
	page := notesListPage{
		Title:          "notes",
		Chrome:         ch,
		DefaultSlug:    def,
		DefaultHref:    noteHref(name, def),
		DefaultMissing: true,
		CanClear:       def != scopefile.NoteDefaultSlug,
	}
	page.Rows = make([]noteListRow, 0, len(slugs))
	for _, slug := range slugs {
		row := noteListRow{Slug: slug, Href: noteHref(name, slug), Default: slug == def}
		if row.Default {
			page.DefaultMissing = false
		}
		page.Rows = append(page.Rows, row)
	}
	s.bindChrome(&page.Chrome, r)
	return s.render(w, "notes", page)
}

func (s *Server) noteInspect(w http.ResponseWriter, r *http.Request) error {
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
	file := scopefile.NoteFile(entry.Dir, slug)
	body, base, err := notes.FileSnapshot(file)
	if err != nil {
		return mapNotesError(err)
	}
	path, err := filepath.Abs(file)
	if err != nil {
		return err
	}
	path = pathutil.Canonical(path)
	missing := base == notes.MissingClobberKey
	ch, err := s.chromeFor(reg, name, "", navNotes)
	if err != nil {
		return err
	}
	page := noteInspectPage{
		Title:    slug,
		Chrome:   ch,
		Slug:     slug,
		Path:     path,
		Default:  slug == def,
		CanClear: slug == def && slug != scopefile.NoteDefaultSlug,
		Missing:  missing,
		EditBody: string(body),
		Base:     base,
		ListHref: notesListHref(name),
	}
	if len(body) > 0 {
		html, err := renderMarkdown(body)
		if err != nil {
			return err
		}
		page.Body = html
	}
	s.bindChrome(&page.Chrome, r)
	return s.render(w, "note", page)
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
	http.Redirect(w, r, noteHref(name, slug), http.StatusSeeOther)
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

	res, err := notes.Set(sess.deps, notes.Input{
		Scope: name,
		Dir:   sess.dir,
		Slug:  slug,
		Base:  base,
	}, []byte(body))
	return s.finishNotes(w, r, noteHref(name, slug), res, err)
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
