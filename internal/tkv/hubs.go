package tkv

import (
	"net/http"

	"cuelang.org/go/cue/cuecontext"

	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/integrity"
	"github.com/p3bot/tk/internal/reconcile"
	"github.com/p3bot/tk/internal/registry"
)

type hubPage struct {
	Title    string
	Chrome   chrome
	Lead     string
	Diagnose bool
	Lines    []string
	Items    []hubItem
	Rows     []overviewRow
}

type hubItem struct {
	Title      string
	Blurb      string
	Href       string
	Ready      bool
	PostAction string
	PostLabel  string
}

func (s *Server) graphs(w http.ResponseWriter, r *http.Request) error {
	reg, err := s.loadRegistry()
	if err != nil {
		return err
	}
	if _, err := s.rec.Reconcile(allTargets(reg), registeredSet(reg), nowNS()); err != nil {
		return err
	}
	ch, err := s.pageChrome(reg, registeredScope(reg, r.URL.Query().Get("scope")), "", navGraphs, r)
	if err != nil {
		return err
	}
	return s.render(w, "hub", hubPage{
		Title:  "graphs",
		Chrome: ch,
		Lead:   "Machine-level and whole-scope pictures. Ticket inspect already shows one-hop depends, depended-on-by, and related.",
		Items: []hubItem{
			{
				Title: "Depends",
				Blurb: "Layered graph for one scope. Arrows point at what a ticket is waiting on. Cross-scope endpoints and done prerequisites are included when a shown ticket needs them.",
				Href:  ch.sectionHref("/graphs/depends"),
				Ready: true,
			},
			{
				Title: "More graphs",
				Blurb: "Related-only, blocked-by, and other layouts land here. This page is the list; each graph gets its own URL when it ships.",
			},
		},
	})
}

func (s *Server) doctor(w http.ResponseWriter, r *http.Request) error {
	reg, err := s.loadRegistry()
	if err != nil {
		return err
	}
	res, err := s.rec.Reconcile(allTargets(reg), registeredSet(reg), nowNS())
	if err != nil {
		return err
	}
	ch, err := s.pageChrome(reg, registeredScope(reg, r.URL.Query().Get("scope")), "", navDoctor, r)
	if err != nil {
		return err
	}
	names := scopeNames(reg)
	stateDir, err := s.app.stateDir()
	if err != nil {
		return err
	}
	lines, err := integrity.Diagnose(integrity.Deps{
		Ctx:      r.Context(),
		Cue:      s.app.cue(),
		StateDir: stateDir,
		Reg:      reg,
		DB:       s.db,
		Rec:      s.rec,
	}, names, res)
	if err != nil {
		return err
	}
	rows := make([]overviewRow, 0, len(names))
	for _, name := range names {
		row, err := s.overviewRow(reg, res, name)
		if err != nil {
			return err
		}
		rows = append(rows, row)
	}
	return s.render(w, "hub", hubPage{
		Title:    "doctor",
		Chrome:   ch,
		Lead:     "Diagnose every registered scope with the same integrity tokens as tk doctor. Reindex rebuilds the machine-wide index from files. Sync all is on this page. Repairs stay on the CLI (tk repair).",
		Diagnose: true,
		Lines:    lines,
		Items: []hubItem{
			{
				Title: "Diagnose",
				Blurb: "Integrity tokens for every registered scope, the same classes as tk doctor. The scope selected in the header does not hide the others.",
				Ready: true,
			},
			{
				Title:      "Reindex",
				Blurb:      "Drop the derived SQLite index and refill it from every registered scope's ticket files. Does not mutate tickets, tk.cue, the registry, or git.",
				PostAction: "/doctor/reindex",
				PostLabel:  "Reindex",
			},
			{
				Title:      "Sync all",
				Blurb:      "Snapshot, fetch, integrate, and push every auto-commit git-root. One root's failure does not skip the others. Same as tk sync --all.",
				PostAction: "/doctor/sync",
				PostLabel:  "Sync all",
			},
		},
		Rows: rows,
	})
}

func (s *Server) postDoctorReindex(w http.ResponseWriter, r *http.Request) error {
	if err := r.ParseForm(); err != nil {
		return errBadRequest("malformed form")
	}
	stateDir, err := s.app.stateDir()
	if err != nil {
		return err
	}
	configDir, err := s.app.configDir()
	if err != nil {
		return err
	}
	cueCtx := cuecontext.New()
	reg, err := registry.NewStore(cueCtx, configDir).Load()
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cur == nil || s.db == nil {
		return &httpError{status: http.StatusInternalServerError, message: "index is closed"}
	}
	// DROP TABLE is file-wide; extra pins are in-flight writes on this file.
	if s.cur.n > 1 {
		return errUnavailable("index is busy; retry shortly")
	}

	db, err := index.Open(stateDir)
	if err != nil {
		return err
	}
	if err := db.Rebuild(); err != nil {
		_ = db.Close()
		return err
	}
	rec := reconcile.New(db, cueCtx)
	if _, err := rec.Reconcile(allTargets(reg), registeredSet(reg), nowNS()); err != nil {
		_ = db.Close()
		return err
	}
	if err := s.installLocked(db, rec); err != nil {
		return err
	}
	http.Redirect(w, r, "/doctor", http.StatusSeeOther)
	return nil
}
