package index

import (
	"errors"
	"path/filepath"
	"testing"
)

func openTemp(t *testing.T) *DB {
	t.Helper()
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func proj(scope, id, status, order string) *Ticket {
	return &Ticket{
		Path: filepath.Join("/tmp", scope, id+".md"), Scope: scope, ID: scope + "-" + id,
		ShortID: id, Status: status, OrderKey: order, Title: id + " title",
		Body: []byte("body of " + id),
	}
}

func TestOpenStampsVersionAndRebuildsOnMismatch(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ver, ok, err := db.readSchemaVersion()
	if err != nil || !ok || ver != SchemaVersion {
		t.Fatalf("version = %d ok=%v err=%v, want %d", ver, ok, err, SchemaVersion)
	}
	if err := db.UpsertTicket(proj("wc", "ab2c", "todo", "a0")); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	db2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db2.Close() }()
	rows, err := db2.ScopeTickets("wc")
	if err != nil || len(rows) != 1 {
		t.Fatalf("after reopen rows = %d err=%v, want 1", len(rows), err)
	}

	if _, err := db2.sql.Exec(`UPDATE meta SET value = 999 WHERE key='schema_version'`); err != nil {
		t.Fatal(err)
	}
	if err := db2.ensureSchema(); err != nil {
		t.Fatal(err)
	}
	rows, _ = db2.ScopeTickets("wc")
	if len(rows) != 0 {
		t.Fatalf("rebuild should drop rows, got %d", len(rows))
	}
}

func TestUpsertReplacesRowAndEdges(t *testing.T) {
	db := openTemp(t)
	p := proj("wc", "ab2c", "todo", "a0")
	edges := []Edge{{FromPath: p.Path, FromID: "wc-ab2c", FromScope: "wc", ToID: "wc-de34", ToScope: "wc", Kind: EdgeDepends}}
	if err := db.UpsertTicketWithEdges(p, edges); err != nil {
		t.Fatal(err)
	}
	got, _ := db.TicketsByID("wc", "wc-ab2c")
	if len(got) != 1 || got[0].Status != "todo" {
		t.Fatalf("row = %+v", got)
	}
	all, _ := db.AllEdges()
	if len(all) != 1 || all[0].ToID != "wc-de34" {
		t.Fatalf("edges = %+v", all)
	}

	p.Status = "in-progress"
	if err := db.UpsertTicketWithEdges(p, nil); err != nil {
		t.Fatal(err)
	}
	got, _ = db.TicketsByID("wc", "wc-ab2c")
	if got[0].Status != "in-progress" {
		t.Fatalf("status not updated: %v", got[0].Status)
	}
	if all, _ := db.AllEdges(); len(all) != 0 {
		t.Fatalf("edges should be replaced to empty, got %+v", all)
	}
}

func TestSearchBM25AndScopeBound(t *testing.T) {
	db := openTemp(t)
	a := proj("wc", "ab2c", "todo", "a0")
	a.Title = "Network redesign"
	a.Body = []byte("sockets and network buffers")
	b := proj("wc", "de34", "todo", "a1")
	b.Title = "Unrelated"
	b.Body = []byte("mentions network once")
	c := proj("ui", "gh56", "todo", "a0")
	c.Title = "Network in ui"
	c.Body = []byte("network network")
	for _, p := range []*Ticket{a, b, c} {
		if err := db.UpsertTicket(p); err != nil {
			t.Fatal(err)
		}
	}
	hits, err := db.Search("", "network")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 3 {
		t.Fatalf("machine-wide hits = %d, want 3", len(hits))
	}
	hits, _ = db.Search("ui", "network")
	if len(hits) != 1 || hits[0].Ticket.Scope != "ui" {
		t.Fatalf("scope-bound search = %+v", hits)
	}
}

func TestSearchMalformedQueryIsTyped(t *testing.T) {
	db := openTemp(t)
	// Unbalanced quote is a malformed FTS5 query, not infrastructure.
	_, err := db.Search("", `foo"`)
	if err == nil {
		t.Fatal("malformed query should error")
	}
	if !errors.Is(err, ErrSearchQuery) {
		t.Fatalf("malformed query should be ErrSearchQuery, got %v", err)
	}
	if _, err := db.Search("", "network"); err != nil {
		t.Fatalf("valid query should not error: %v", err)
	}
}

func TestDuplicateAndEqualOrderAggregates(t *testing.T) {
	db := openTemp(t)
	dupA := proj("wc", "ab2c", "todo", "a0")
	dupB := proj("wc", "ab2c", "done", "a1")
	dupB.Path = "/tmp/wc/archive/ab2c.md" // same id, different file
	same1 := proj("wc", "de34", "todo", "b0")
	same2 := proj("wc", "gh56", "todo", "b0") // equal order key
	for _, p := range []*Ticket{dupA, dupB, same1, same2} {
		if err := db.UpsertTicket(p); err != nil {
			t.Fatal(err)
		}
	}
	dups, _ := db.DuplicateIDs([]string{"wc"})
	if len(dups) != 1 || dups[0].Key != "wc-ab2c" || len(dups[0].Members) != 2 {
		t.Fatalf("duplicate ids = %+v", dups)
	}
	eq, _ := db.EqualOrders([]string{"wc"})
	if len(eq) != 1 || eq[0].Key != "b0" || len(eq[0].Members) != 2 {
		t.Fatalf("equal orders = %+v", eq)
	}
	set, _ := db.DuplicateIDSet([]string{"wc"})
	if !set["wc\x00wc-ab2c"] {
		t.Fatalf("duplicate set missing collision: %+v", set)
	}
}

func TestDeleteScopePrunes(t *testing.T) {
	db := openTemp(t)
	_ = db.UpsertTicket(proj("wc", "ab2c", "todo", "a0"))
	_ = db.UpsertTicket(proj("ui", "de34", "todo", "a0"))
	_ = db.SetLastIndex("wc", 123)
	if err := db.DeleteScope("wc"); err != nil {
		t.Fatal(err)
	}
	if rows, _ := db.ScopeTickets("wc"); len(rows) != 0 {
		t.Fatalf("wc rows survived delete: %d", len(rows))
	}
	if rows, _ := db.ScopeTickets("ui"); len(rows) != 1 {
		t.Fatalf("ui rows should be untouched: %d", len(rows))
	}
	if ns, _ := db.LastIndex("wc"); ns != 0 {
		t.Fatalf("scope_meta not pruned: %d", ns)
	}
}

func TestReadOnlyQueryGuard(t *testing.T) {
	db := openTemp(t)
	_ = db.UpsertTicket(proj("wc", "ab2c", "todo", "a0"))

	res, err := db.RunReadOnlyQuery(`SELECT id, status FROM tickets ORDER BY id`)
	if err != nil {
		t.Fatalf("select rejected: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0][0] != "wc-ab2c" {
		t.Fatalf("select result = %+v", res)
	}

	for _, bad := range []string{
		`INSERT INTO tickets(path,scope,id) VALUES('x','wc','wc-zz22')`,
		`UPDATE tickets SET status='done'`,
		`DELETE FROM tickets`,
		`DROP TABLE tickets`,
		`SELECT 1; DELETE FROM tickets`,
		`PRAGMA journal_mode = DELETE`,
		`replace into tickets(path,scope,id) values('x','wc','wc-zz22')`,
		// Static classifier sees leading WITH; only PRAGMA query_only catches this.
		`WITH t AS (SELECT 1) DELETE FROM tickets`,
		`WITH t AS (SELECT 1) UPDATE tickets SET status='done'`,
	} {
		if _, err := db.RunReadOnlyQuery(bad); err == nil {
			t.Errorf("read-only guard admitted a write: %q", bad)
		}
	}
	if _, err := db.RunReadOnlyQuery(`WITH t AS (SELECT id FROM tickets) SELECT * FROM t`); err != nil {
		t.Errorf("read-only CTE select rejected: %v", err)
	}
	if _, err := db.RunReadOnlyQuery(`PRAGMA table_info(tickets)`); err != nil {
		t.Errorf("read-only pragma rejected: %v", err)
	}
	if _, err := db.RunReadOnlyQuery(`EXPLAIN QUERY PLAN SELECT * FROM tickets`); err != nil {
		t.Errorf("explain rejected: %v", err)
	}
	// Quoted ';' is not a batch separator.
	for _, ok := range []string{
		`SELECT id FROM tickets WHERE id LIKE '%;%'`,
		`SELECT ';' AS sep FROM tickets`,
		`SELECT id AS "a;b" FROM tickets`,
	} {
		if _, err := db.RunReadOnlyQuery(ok); err != nil {
			t.Errorf("read-only query with a quoted ';' rejected: %q: %v", ok, err)
		}
	}
	if _, err := db.RunReadOnlyQuery(`SELECT ';' FROM tickets; DELETE FROM tickets`); err == nil {
		t.Error("read-only guard admitted a write following a quoted-';' select")
	}
	if rows, _ := db.ScopeTickets("wc"); len(rows) != 1 {
		t.Fatalf("a rejected write still mutated the store: %d rows", len(rows))
	}
}

func TestForeignKeysPragmaIsOn(t *testing.T) {
	db := openTemp(t)
	var v int
	if err := db.sql.QueryRow(`PRAGMA foreign_keys`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != 1 {
		t.Fatalf("foreign_keys = %d, want 1", v)
	}
}

func TestUpsertDedupesEdges(t *testing.T) {
	db := openTemp(t)
	p := proj("wc", "ab2c", "todo", "a0")
	dup := Edge{FromPath: p.Path, FromID: "wc-ab2c", FromScope: "wc", ToID: "wc-de34", ToScope: "wc", Kind: EdgeDepends}
	rel := Edge{FromPath: p.Path, FromID: "wc-ab2c", FromScope: "wc", ToID: "wc-de34", ToScope: "wc", Kind: EdgeRelated}
	other := Edge{FromPath: p.Path, FromID: "wc-ab2c", FromScope: "wc", ToID: "wc-gh56", ToScope: "wc", Kind: EdgeDepends}
	if err := db.UpsertTicketWithEdges(p, []Edge{dup, dup, rel, other, dup}); err != nil {
		t.Fatal(err)
	}
	all, err := db.AllEdges()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("edges = %+v, want 3 (depends+related to de34, depends to gh56)", all)
	}
}

func TestConfigCache(t *testing.T) {
	db := openTemp(t)
	if _, ok, _ := db.ConfigCacheGet("wc"); ok {
		t.Fatal("empty cache should miss")
	}
	e := ConfigCacheEntry{ClosureJSON: "k1", SchemaJSON: `{"name":"wc"}`}
	if err := db.ConfigCacheSet("wc", e); err != nil {
		t.Fatal(err)
	}
	got, ok, err := db.ConfigCacheGet("wc")
	if err != nil || !ok || got.ClosureJSON != "k1" || got.SchemaJSON != `{"name":"wc"}` {
		t.Fatalf("cache get = %+v ok=%v err=%v", got, ok, err)
	}
}
