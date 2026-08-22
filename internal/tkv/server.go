package tkv

import (
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/p3bot/tk/internal/id"
	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/reconcile"
	"github.com/p3bot/tk/internal/registry"
	"github.com/p3bot/tk/internal/scopeadmin"
	"github.com/p3bot/tk/internal/scopeconfig"
	"github.com/p3bot/tk/internal/scopefile"
	"github.com/p3bot/tk/internal/status"
)

//go:embed templates/*.html static/*
var assets embed.FS

var pages = template.Must(template.New("").ParseFS(assets, "templates/*.html"))

// Server is the long-lived HTTP process: one index connection, templates, CSS.
type Server struct {
	app *App
	mu  sync.Mutex
	db  *index.DB
	rec *reconcile.Reconciler
	tpl *template.Template
}

// NewServer opens the same XDG index tk uses.
func (a *App) NewServer() (*Server, error) {
	ctx := a.cue()
	stateDir, err := a.stateDir()
	if err != nil {
		return nil, err
	}
	db, err := index.Open(stateDir)
	if err != nil {
		return nil, err
	}
	return &Server{
		app: a,
		db:  db,
		rec: reconcile.New(db, ctx),
		tpl: pages,
	}, nil
}

// Close releases the index handle.
func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

func (s *Server) runRequest(fn func() error) error {
	err := fn()
	if !isSchemaShaped(err) {
		return err
	}
	if reopenErr := s.reopen(); reopenErr != nil {
		return reopenErr
	}
	return fn()
}

func (s *Server) reopen() error {
	if s.db != nil {
		_ = s.db.Close()
		s.db = nil
	}
	stateDir, err := s.app.stateDir()
	if err != nil {
		return err
	}
	db, err := index.Open(stateDir)
	if err != nil {
		return err
	}
	ctx := s.app.cue()
	s.db = db
	s.rec = reconcile.New(db, ctx)
	return nil
}

// Handler is the multi-page mux. Static files are ordinary /static/... URLs.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	static, err := fs.Sub(assets, "static")
	if err != nil {
		panic(err)
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(static))))
	mux.HandleFunc("GET /{$}", s.wrap(s.overview))
	mux.HandleFunc("GET /search", s.wrap(s.search))
	mux.HandleFunc("GET /graphs", s.wrap(s.graphs))
	mux.HandleFunc("GET /graphs/depends", s.wrap(s.dependsGraph))
	mux.HandleFunc("GET /maintenance", s.wrap(s.maintenance))
	mux.HandleFunc("GET /scope/{name}", s.wrap(s.kanban))
	mux.HandleFunc("GET /scope/{name}/{id}", s.wrap(s.inspect))
	return mux
}

type requestFn func(http.ResponseWriter, *http.Request) error

func (s *Server) wrap(fn requestFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.db == nil {
			s.errorPage(w, r, http.StatusInternalServerError, "index is closed", nil)
			return
		}
		err := s.runRequest(func() error { return fn(w, r) })
		if err == nil {
			return
		}
		if isBusy(err) {
			s.errorPage(w, r, http.StatusServiceUnavailable, "index is busy; retry shortly", nil)
			return
		}
		var he *httpError
		if errors.As(err, &he) {
			s.errorPage(w, r, he.status, he.message, he.paths)
			return
		}
		s.errorPage(w, r, http.StatusInternalServerError, err.Error(), nil)
	}
}

type httpError struct {
	status  int
	message string
	paths   []string
}

func (e *httpError) Error() string { return e.message }

func errNotFound(msg string) error {
	return &httpError{status: http.StatusNotFound, message: msg}
}

func errDuplicate(id string, paths []string) error {
	return &httpError{
		status:  http.StatusConflict,
		message: fmt.Sprintf("%s is claimed by %d files", id, len(paths)),
		paths:   paths,
	}
}

func errBadRequest(msg string) error {
	return &httpError{status: http.StatusBadRequest, message: msg}
}

const (
	navOverview    = "overview"
	navBoard       = "board"
	navSearch      = "search"
	navGraphs      = "graphs"
	navMaintenance = "maintenance"
)

type chrome struct {
	Section   string
	Scopes    []string
	Selected  string
	Mode      string
	Lens      string
	Me        string
	Integrity string
	Query     string
}

// BoardHref is the kanban for the selected scope, or overview when none is selected.
func (c chrome) BoardHref() string {
	if c.Selected != "" {
		return "/scope/" + c.Selected
	}
	if len(c.Scopes) == 1 {
		return "/scope/" + c.Scopes[0]
	}
	return "/"
}

// SearchHref keeps a selected scope as a search bound when one is in chrome.
func (c chrome) SearchHref() string {
	return c.sectionHref("/search")
}

func (c chrome) GraphsHref() string { return c.sectionHref("/graphs") }

func (c chrome) MaintenanceHref() string { return c.sectionHref("/maintenance") }

func (c chrome) sectionHref(path string) string {
	if c.Selected == "" {
		return path
	}
	return path + "?scope=" + url.QueryEscape(c.Selected)
}

func registeredScope(reg *registry.Registry, name string) string {
	if name == "" {
		return ""
	}
	if _, ok := reg.Scopes[name]; ok {
		return name
	}
	return ""
}

func (s *Server) loadRegistry() (*registry.Registry, error) {
	ctx := s.app.cue()
	configDir, err := s.app.configDir()
	if err != nil {
		return nil, err
	}
	return registry.NewStore(ctx, configDir).Load()
}

func registeredSet(reg *registry.Registry) map[string]bool {
	out := make(map[string]bool, len(reg.Scopes))
	for name := range reg.Scopes {
		out[name] = true
	}
	return out
}

func allTargets(reg *registry.Registry) map[string]string {
	out := make(map[string]string, len(reg.Scopes))
	for name, entry := range reg.Scopes {
		out[name] = entry.Dir
	}
	return out
}

func scopeNames(reg *registry.Registry) []string {
	names := make([]string, 0, len(reg.Scopes))
	for name := range reg.Scopes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func nowNS() int64 { return time.Now().UnixNano() }

func (s *Server) chromeFor(reg *registry.Registry, selected, query, section string) (chrome, error) {
	c := chrome{
		Section:   section,
		Scopes:    scopeNames(reg),
		Selected:  selected,
		Integrity: "ok",
		Query:     query,
	}
	if selected == "" {
		ok := true
		for _, name := range c.Scopes {
			entry := reg.Scopes[name]
			st, err := scopeIntegrity(s.db, name, s.rec.SchemaCached(name, entry.Dir))
			if err != nil {
				return c, err
			}
			if st != "ok" {
				ok = false
				break
			}
		}
		if !ok {
			c.Integrity = "issues"
		}
		return c, nil
	}
	entry, ok := reg.Scopes[selected]
	if !ok {
		c.Integrity = "issues"
		return c, nil
	}
	schema := s.rec.SchemaCached(selected, entry.Dir)
	_, hasRoot := scopefile.GitRoot(entry.Dir)
	c.Mode = statusMode(schema, schema == nil, hasRoot)
	c.Lens = strings.Join(reg.Lens[selected], " ")
	c.Me = reg.Me[selected]
	st, err := scopeIntegrity(s.db, selected, schema)
	if err != nil {
		return c, err
	}
	c.Integrity = st
	return c, nil
}

func statusMode(schema *scopeconfig.Schema, configUnusable bool, hasRoot bool) string {
	if configUnusable || schema == nil {
		return scopeadmin.ModePlainFiles
	}
	return scopeadmin.DeriveMode(schema.AutoCommit, hasRoot)
}

func scopeIntegrity(db *index.DB, scope string, schema *scopeconfig.Schema) (string, error) {
	scopes := []string{scope}
	n, err := db.ParseErrorCount(scopes)
	if err != nil {
		return "", err
	}
	if n > 0 {
		return "issues", nil
	}
	dups, err := db.DuplicateIDs(scopes)
	if err != nil {
		return "", err
	}
	if len(dups) > 0 {
		return "issues", nil
	}
	eq, err := db.EqualOrders(scopes)
	if err != nil {
		return "", err
	}
	if len(eq) > 0 {
		return "issues", nil
	}
	drift, err := db.HasArchiveDrift(scope, status.TerminalNames(schemaCustom(schema)))
	if err != nil {
		return "", err
	}
	if drift {
		return "issues", nil
	}
	return "ok", nil
}

func (s *Server) render(w http.ResponseWriter, name string, data any) error {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	return s.tpl.ExecuteTemplate(w, name, data)
}

func (s *Server) errorPage(w http.ResponseWriter, r *http.Request, code int, message string, paths []string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	reg, _ := s.loadRegistry()
	var ch chrome
	if reg != nil {
		ch.Scopes = scopeNames(reg)
	}
	if name := r.PathValue("name"); id.IsScopeName(name) {
		ch.Selected = name
	}
	ch.Section = sectionFromPath(r.URL.Path)
	_ = s.tpl.ExecuteTemplate(w, "error", errorPage{
		Title:   fmt.Sprintf("%d", code),
		Chrome:  ch,
		Status:  code,
		Message: message,
		Paths:   paths,
	})
}

type errorPage struct {
	Title   string
	Chrome  chrome
	Status  int
	Message string
	Paths   []string
}

func sectionFromPath(p string) string {
	switch {
	case p == "/":
		return navOverview
	case strings.HasPrefix(p, "/search"):
		return navSearch
	case strings.HasPrefix(p, "/graphs"):
		return navGraphs
	case strings.HasPrefix(p, "/maintenance"):
		return navMaintenance
	case strings.HasPrefix(p, "/scope/"):
		return navBoard
	default:
		return ""
	}
}

func parseIDArg(tok string) (full bool, ok bool) {
	if strings.ContainsRune(tok, '-') {
		return true, id.IsFullTicketID(tok)
	}
	return false, id.IsShortID(tok)
}
