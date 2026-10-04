package index

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestDesignSearchDoesNotTouchTicketSearch(t *testing.T) {
	db := openTemp(t)
	ticket := proj("wc", "m4np", "todo", "a0")
	ticket.Title = "Ticket title"
	ticket.Body = []byte("ticketonlyterm in the ticket")
	if err := db.UpsertTicket(ticket); err != nil {
		t.Fatal(err)
	}
	var ticketRow int64
	if err := db.sql.QueryRow(`SELECT rowid FROM tickets WHERE path = ?`, ticket.Path).Scan(&ticketRow); err != nil {
		t.Fatal(err)
	}

	designPath := filepath.Join("/tmp", "wc", "design", "wc-ab2c-shape.md")
	design := &Design{
		Path: designPath, Scope: "wc", ID: "wc-ab2c", ShortID: "ab2c",
		Status: "draft", Title: "Shape", Body: []byte("designonlyterm in the design"),
		MtimeNS: 10, Size: 20,
	}
	edge := Edge{FromPath: designPath, FromID: "wc-ab2c", FromScope: "wc", ToID: "wc-m4np", ToScope: "wc", Kind: EdgeProduces}
	missing := Edge{FromPath: designPath, FromID: "wc-ab2c", FromScope: "wc", ToID: "wc-zzzz", ToScope: "wc", Kind: EdgeProduces}
	if err := db.UpsertDesignWithEdges(design, []Edge{edge, missing}); err != nil {
		t.Fatal(err)
	}

	var designRow int64
	if err := db.sql.QueryRow(`SELECT rowid FROM designs WHERE path = ?`, designPath).Scan(&designRow); err != nil {
		t.Fatal(err)
	}
	var ftsRow int64
	if err := db.sql.QueryRow(`SELECT rowid FROM design_fts WHERE design_fts MATCH 'designonlyterm'`).Scan(&ftsRow); err != nil {
		t.Fatal(err)
	}
	if ftsRow != designRow {
		t.Fatalf("design search rowid = %d, design rowid = %d", ftsRow, designRow)
	}
	if ticketRow == designRow {
		var ticketFTS int
		if err := db.sql.QueryRow(`SELECT COUNT(*) FROM fts WHERE rowid = ? AND fts MATCH 'ticketonlyterm'`, ticketRow).Scan(&ticketFTS); err != nil {
			t.Fatal(err)
		}
		if ticketFTS != 1 {
			t.Fatal("shared row number must not replace the ticket search entry")
		}
	}

	hits, err := db.Search("wc", "designonlyterm")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("ticket search matched a design-only word: %+v", hits)
	}
	found, err := db.SearchDesigns("wc", "designonlyterm")
	if err != nil || len(found) != 1 || found[0].Design.ID != "wc-ab2c" || found[0].Design.Status != "draft" || found[0].Design.Title != "Shape" || found[0].Design.Path != designPath {
		t.Fatalf("design search = %+v err=%v", found, err)
	}
	if ticketHits, err := db.SearchDesigns("", "ticketonlyterm"); err != nil || len(ticketHits) != 0 {
		t.Fatalf("design search matched a ticket-only word: %+v err=%v", ticketHits, err)
	}
	if _, err := db.SearchDesigns("", `foo"`); !errors.Is(err, ErrSearchQuery) {
		t.Fatalf("bad design query = %v", err)
	}
	hits, err = db.Search("wc", "ticketonlyterm")
	if err != nil || len(hits) != 1 || hits[0].Ticket.ID != "wc-m4np" {
		t.Fatalf("ticket search = %+v err=%v", hits, err)
	}

	rows, err := db.ScopeTickets("wc")
	if err != nil || len(rows) != 1 || rows[0].ID != "wc-m4np" {
		t.Fatalf("tickets = %+v err=%v", rows, err)
	}
	designs, err := db.ScopeDesigns("wc")
	if err != nil || len(designs) != 1 || designs[0].ID != "wc-ab2c" || designs[0].Path != designPath {
		t.Fatalf("designs = %+v err=%v", designs, err)
	}
	all, err := db.AllEdges()
	if err != nil || len(all) != 2 {
		t.Fatalf("edges = %+v err=%v", all, err)
	}

	if err := db.DeleteDesignByPath(designPath); err != nil {
		t.Fatal(err)
	}
	if designs, _ := db.ScopeDesigns("wc"); len(designs) != 0 {
		t.Fatalf("design row survived delete: %+v", designs)
	}
	if all, _ := db.AllEdges(); len(all) != 0 {
		t.Fatalf("produces edges survived design delete: %+v", all)
	}
	var n int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM design_fts WHERE design_fts MATCH 'designonlyterm'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("design search entries after delete = %d", n)
	}
	hits, err = db.Search("wc", "ticketonlyterm")
	if err != nil || len(hits) != 1 || hits[0].Ticket.ID != "wc-m4np" {
		t.Fatalf("ticket search after design delete = %+v err=%v", hits, err)
	}
	if found, err := db.SearchDesigns("wc", "designonlyterm"); err != nil || len(found) != 0 {
		t.Fatalf("design search after delete = %+v err=%v", found, err)
	}
}

func TestDeleteTicketKeepsProducesEdge(t *testing.T) {
	db := openTemp(t)
	ticket := proj("wc", "m4np", "todo", "a0")
	if err := db.UpsertTicketWithEdges(ticket, []Edge{{
		FromPath: ticket.Path, FromID: ticket.ID, FromScope: "wc", ToID: "wc-de34", ToScope: "wc", Kind: EdgeDepends,
	}}); err != nil {
		t.Fatal(err)
	}
	designPath := "/tmp/wc/design/wc-ab2c-shape.md"
	if err := db.UpsertDesignWithEdges(&Design{
		Path: designPath, Scope: "wc", ID: "wc-ab2c", ShortID: "ab2c", Status: "draft", MtimeNS: 1,
	}, []Edge{{
		FromPath: designPath, FromID: "wc-ab2c", FromScope: "wc", ToID: ticket.ID, ToScope: "wc", Kind: EdgeProduces,
	}}); err != nil {
		t.Fatal(err)
	}

	if _, err := db.sql.Exec(`DELETE FROM tickets WHERE path = ?`, ticket.Path); err != nil {
		t.Fatal(err)
	}
	all, err := db.AllEdges()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Kind != EdgeProduces || all[0].ToID != ticket.ID {
		t.Fatalf("after ticket delete edges = %+v, want the produces edge", all)
	}

	if err := db.DeleteScope("wc"); err != nil {
		t.Fatal(err)
	}
	if designs, _ := db.ScopeDesigns("wc"); len(designs) != 0 {
		t.Fatalf("forgotten scope kept designs: %+v", designs)
	}
	if all, _ := db.AllEdges(); len(all) != 0 {
		t.Fatalf("forgotten scope kept edges: %+v", all)
	}
	var n int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM design_fts`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("forgotten scope kept design search rows: %d", n)
	}
}

func TestProducesInsertDoesNotRequireTicketRow(t *testing.T) {
	db := openTemp(t)
	designPath := "/tmp/wc/design/wc-ab2c-shape.md"
	if err := db.UpsertDesignWithEdges(&Design{
		Path: designPath, Scope: "wc", ID: "wc-ab2c", ShortID: "ab2c", Status: "draft",
	}, []Edge{{
		FromPath: designPath, FromID: "wc-ab2c", FromScope: "wc", ToID: "wc-m4np", ToScope: "wc", Kind: EdgeProduces,
	}}); err != nil {
		t.Fatalf("produces edge whose target ticket is absent: %v", err)
	}
	if _, err := db.sql.Exec(`INSERT INTO edges(from_path, from_id, from_scope, to_id, to_scope, kind) VALUES (?, ?, ?, ?, ?, ?)`,
		designPath, "wc-ab2c", "wc", "wc-zzzz", "wc", "blocked"); err == nil {
		t.Fatal("kind blocked should fail CHECK")
	}
}
