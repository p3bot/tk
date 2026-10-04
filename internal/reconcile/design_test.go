package reconcile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReconcileIndexesDesignBesideTickets(t *testing.T) {
	r, db := newReconciler(t)
	dir := mkScope(t, "wc")
	writeFile(t, filepath.Join(dir, "wc-m4np-target.md"), projFile("wc-m4np", "todo", "a0", "# Target\n\nticketonlyterm\n"))
	writeFile(t, filepath.Join(dir, "design", "wc-ab2c-shape.md"), ""+
		"---\nid: wc-ab2c\nstatus: draft\ncreated: 2026-01-01T00:00:00Z\nsummary: shape note\nproduces: [wc-m4np, nope, wc-zzzz]\n---\n# Shape\n\ndesignonlyterm\n")
	writeFile(t, filepath.Join(dir, "design", "wc-cd3e-kept.md"), ""+
		"---\nid: wc-cd3e\nstatus: weird\ncreated: 2026-01-02T00:00:00Z\nchanged: 2026-02-02T03:04:05Z\n---\n# Kept\n")
	writeFile(t, filepath.Join(dir, "design", "nested", "wc-gh56-nest.md"), projFile("wc-gh56", "draft", "a0", "# Nested\n"))
	writeFile(t, filepath.Join(dir, "design", "notes.md"), "# notes\n")
	writeFile(t, filepath.Join(dir, "notes", "default.md"), "# note\n")

	reconcileOne(t, r, "wc", dir, time.Now().UnixNano())

	tickets, err := db.ScopeTickets("wc")
	if err != nil {
		t.Fatal(err)
	}
	if len(tickets) != 1 || tickets[0].ID != "wc-m4np" {
		t.Fatalf("tickets = %+v", tickets)
	}
	designs, err := db.ScopeDesigns("wc")
	if err != nil {
		t.Fatal(err)
	}
	if len(designs) != 2 {
		t.Fatalf("designs = %+v", designs)
	}
	byID := map[string]*struct {
		status, changed, summary, title string
		parse                           bool
	}{}
	for _, d := range designs {
		byID[d.ID] = &struct {
			status, changed, summary, title string
			parse                           bool
		}{d.Status, d.Changed, d.Summary, d.Title, d.ParseError}
		if d.ID == "wc-ab2c" && d.Path != filepath.Join(dir, "design", "wc-ab2c-shape.md") {
			t.Fatalf("design path = %s", d.Path)
		}
	}
	shape := byID["wc-ab2c"]
	if shape == nil || shape.status != "draft" || shape.summary != "shape note" || shape.title != "Shape" || shape.changed != "" || shape.parse {
		t.Fatalf("shape row = %+v", shape)
	}
	kept := byID["wc-cd3e"]
	if kept == nil || kept.status != "weird" || kept.changed != "2026-02-02T03:04:05Z" || kept.parse {
		t.Fatalf("unknown status must be a normal row, got %+v", kept)
	}

	edges, err := db.AllEdges()
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 2 {
		t.Fatalf("edges = %+v, want produces to wc-m4np and wc-zzzz", edges)
	}
	got := map[string]bool{}
	for _, e := range edges {
		if e.Kind != "produces" || e.FromID != "wc-ab2c" || e.FromPath != filepath.Join(dir, "design", "wc-ab2c-shape.md") {
			t.Fatalf("edge = %+v", e)
		}
		got[e.ToID] = true
	}
	if !got["wc-m4np"] || !got["wc-zzzz"] {
		t.Fatalf("produces targets = %v", got)
	}

	res, err := db.RunReadOnlyQuery(`SELECT rowid FROM design_fts WHERE design_fts MATCH 'designonlyterm'`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("design search rows = %+v", res.Rows)
	}
	ticketHits, err := db.Search("", "designonlyterm")
	if err != nil || len(ticketHits) != 0 {
		t.Fatalf("ticket search = %+v err=%v", ticketHits, err)
	}
	ticketHits, err = db.Search("", "ticketonlyterm")
	if err != nil || len(ticketHits) != 1 || ticketHits[0].Ticket.ID != "wc-m4np" {
		t.Fatalf("ticket term = %+v err=%v", ticketHits, err)
	}

	if err := os.Remove(filepath.Join(dir, "design", "wc-ab2c-shape.md")); err != nil {
		t.Fatal(err)
	}
	reconcileOne(t, r, "wc", dir, time.Now().Add(time.Second).UnixNano())
	designs, _ = db.ScopeDesigns("wc")
	if len(designs) != 1 || designs[0].ID != "wc-cd3e" {
		t.Fatalf("after delete designs = %+v", designs)
	}
	edges, _ = db.AllEdges()
	if len(edges) != 0 {
		t.Fatalf("produces edges survived file delete: %+v", edges)
	}
	res, err = db.RunReadOnlyQuery(`SELECT rowid FROM design_fts WHERE design_fts MATCH 'designonlyterm'`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 0 {
		t.Fatalf("design search after delete = %+v", res.Rows)
	}
	ticketHits, err = db.Search("", "ticketonlyterm")
	if err != nil || len(ticketHits) != 1 {
		t.Fatalf("ticket search after design delete = %+v err=%v", ticketHits, err)
	}
}

func TestReconcileDesignParseErrorAndMissingDir(t *testing.T) {
	r, db := newReconciler(t)
	dir := mkScope(t, "wc")
	writeFile(t, filepath.Join(dir, "design", "wc-ab2c-broken.md"), "---\nid: wc-ab2c\n<<<<<<< HEAD\nstatus: draft\n---\n# Broken\nrawtoken\n")
	reconcileOne(t, r, "wc", dir, time.Now().UnixNano())
	designs, err := db.ScopeDesigns("wc")
	if err != nil || len(designs) != 1 || !designs[0].ParseError || designs[0].ID != "wc-ab2c" || designs[0].Status != "" {
		t.Fatalf("quarantine = %+v err=%v", designs, err)
	}
	res, err := db.RunReadOnlyQuery(`SELECT rowid FROM design_fts WHERE design_fts MATCH 'rawtoken'`)
	if err != nil || len(res.Rows) != 1 {
		t.Fatalf("raw file should be indexed, rows=%+v err=%v", res, err)
	}
	if n, _ := db.ParseErrorCount([]string{"wc"}); n != 0 {
		t.Fatalf("design quarantine must not count as a ticket parse_error, n=%d", n)
	}

	other := mkScope(t, "ui")
	writeFile(t, filepath.Join(other, "design"), "not a directory\n")
	reconcileOne(t, r, "ui", other, time.Now().UnixNano())
	if designs, _ := db.ScopeDesigns("ui"); len(designs) != 0 {
		t.Fatalf("file parked at design/ indexed: %+v", designs)
	}

	plain := mkScope(t, "api")
	reconcileOne(t, r, "api", plain, time.Now().UnixNano())
	if designs, _ := db.ScopeDesigns("api"); len(designs) != 0 {
		t.Fatalf("missing design/ indexed: %+v", designs)
	}
}

func TestReconcileDesignIDAndProducesRules(t *testing.T) {
	r, db := newReconciler(t)
	dir := mkScope(t, "wc")
	writeFile(t, filepath.Join(dir, "design", "wc-ab2c-moved.md"), ""+
		"---\nid: wc-cd3e\nstatus: draft\ncreated: 2026-01-01T00:00:00Z\n---\n# Moved\n")
	writeFile(t, filepath.Join(dir, "design", "wc-gh56-foreign.md"), ""+
		"---\nid: api-xy99\nstatus: draft\ncreated: 2026-01-01T00:00:00Z\n---\n# Foreign\n")
	writeFile(t, filepath.Join(dir, "design", "api-xy99-other.md"), ""+
		"---\nid: api-xy99\nstatus: draft\ncreated: 2026-01-01T00:00:00Z\n---\n# Other scope name\n")
	writeFile(t, filepath.Join(dir, "design", "wc-jk89-scalar.md"), ""+
		"---\nid: wc-jk89\nstatus: draft\ncreated: 2026-01-01T00:00:00Z\nproduces: hello\n---\n# Scalar\n")
	reconcileOne(t, r, "wc", dir, time.Now().UnixNano())

	designs, err := db.ScopeDesigns("wc")
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]string{}
	for _, d := range designs {
		byID[d.ID] = d.Path
	}
	if _, ok := byID["api-xy99"]; ok {
		t.Fatalf("foreign filename or foreign fence id was indexed: %+v", byID)
	}
	if !strings.HasSuffix(byID["wc-cd3e"], "wc-ab2c-moved.md") {
		t.Fatalf("same-scope fence id should win, got %v", byID)
	}
	if !strings.HasSuffix(byID["wc-gh56"], "wc-gh56-foreign.md") {
		t.Fatalf("foreign fence id should keep the filename id, got %v", byID)
	}
	if !strings.HasSuffix(byID["wc-jk89"], "wc-jk89-scalar.md") {
		t.Fatalf("scalar produces should still be a row, got %v", byID)
	}
	if edges, _ := db.AllEdges(); len(edges) != 0 {
		t.Fatalf("non-list produces became edges: %+v", edges)
	}
}

func TestForgottenScopeDropsDesigns(t *testing.T) {
	r, db := newReconciler(t)
	dir := mkScope(t, "wc")
	writeFile(t, filepath.Join(dir, "design", "wc-ab2c-shape.md"), ""+
		"---\nid: wc-ab2c\nstatus: draft\ncreated: 2026-01-01T00:00:00Z\nproduces: [wc-m4np]\n---\n# Shape\n")
	reconcileOne(t, r, "wc", dir, time.Now().UnixNano())
	if designs, _ := db.ScopeDesigns("wc"); len(designs) != 1 {
		t.Fatal("precondition")
	}
	other := mkScope(t, "ui")
	if _, err := r.Reconcile(map[string]string{"ui": other}, map[string]bool{"ui": true}, time.Now().UnixNano()); err != nil {
		t.Fatal(err)
	}
	if designs, _ := db.ScopeDesigns("wc"); len(designs) != 0 {
		t.Fatal("forgotten scope kept design rows")
	}
	if edges, _ := db.AllEdges(); len(edges) != 0 {
		t.Fatalf("forgotten scope kept produces edges: %+v", edges)
	}
}
