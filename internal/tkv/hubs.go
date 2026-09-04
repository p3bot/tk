package tkv

import (
	"net/http"
)

type hubPage struct {
	Title  string
	Chrome chrome
	Lead   string
	Items  []hubItem
	Rows   []overviewRow
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

func (s *Server) maintenance(w http.ResponseWriter, r *http.Request) error {
	reg, err := s.loadRegistry()
	if err != nil {
		return err
	}
	res, err := s.rec.Reconcile(allTargets(reg), registeredSet(reg), nowNS())
	if err != nil {
		return err
	}
	ch, err := s.pageChrome(reg, registeredScope(reg, r.URL.Query().Get("scope")), "", navMaintenance, r)
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
	return s.render(w, "hub", hubPage{
		Title:  "maintenance",
		Chrome: ch,
		Lead:   "Health for every registered scope. Sync all is on this page. Repairs, index rebuild, and scope admin stay on the tk CLI (tk doctor, tk reindex, tk scope).",
		Items: []hubItem{
			{
				Title: "Integrity",
				Blurb: "ok/issues per scope from the same checks as tk status: parse errors, duplicate ids, equal order keys, archive layout drift.",
				Ready: true,
			},
			{
				Title:      "Sync all",
				Blurb:      "Snapshot, fetch, integrate, and push every auto-commit git-root. One root's failure does not skip the others. Same as tk sync --all.",
				PostAction: "/maintenance/sync",
				PostLabel:  "Sync all",
			},
			{
				Title: "More",
				Blurb: "Doctor and scope registration will get surfaces here later. They will call the same Go functions tk uses — never a tk subprocess.",
			},
		},
		Rows: rows,
	})
}
