package index

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
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

func TestSchemaFTSContentlessDelete(t *testing.T) {
	db := openTemp(t)
	if SchemaVersion <= 4 {
		t.Fatalf("SchemaVersion = %d, want > 4", SchemaVersion)
	}

	var ddl string
	if err := db.sql.QueryRow(`SELECT sql FROM sqlite_master WHERE name='fts'`).Scan(&ddl); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"content=''",
		"contentless_delete=1",
		"tokenize = 'porter unicode61'",
	} {
		if !strings.Contains(ddl, want) {
			t.Errorf("fts DDL missing %q:\n%s", want, ddl)
		}
	}

	rows, err := db.sql.Query(`PRAGMA table_info(tickets)`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		if name == "body" {
			t.Fatal("tickets.body must not exist")
		}
	}

	schemaText := strings.ToLower(SchemaText)
	for _, want := range []string{
		"contentless-delete",
		"not a document store",
	} {
		if !strings.Contains(schemaText, want) {
			t.Errorf("SchemaText missing %q:\n%s", want, SchemaText)
		}
	}
}

func TestSearchQuarantineAndDeleteClearsFTS(t *testing.T) {
	db := openTemp(t)
	q := proj("wc", "ab2c", "todo", "a0")
	q.ParseError = true
	q.ParseMsg = "broken frontmatter"
	q.Body = []byte("uniquequarantinetoken xyzzy")
	if err := db.UpsertTicket(q); err != nil {
		t.Fatal(err)
	}

	var rowid int64
	if err := db.sql.QueryRow(`SELECT rowid FROM tickets WHERE path = ?`, q.Path).Scan(&rowid); err != nil {
		t.Fatal(err)
	}

	hits, err := db.Search("wc", "uniquequarantinetoken")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Ticket.ID != q.ID || !hits[0].Ticket.ParseError {
		t.Fatalf("quarantine MATCH = %+v", hits)
	}

	var title, body sql.NullString
	if err := db.sql.QueryRow(`SELECT title, body FROM fts WHERE rowid = ?`, rowid).Scan(&title, &body); err != nil {
		t.Fatal(err)
	}
	if title.Valid || body.Valid {
		t.Fatalf("fts stored document text title=%v body=%v", title, body)
	}

	if err := db.DeleteByPath(q.Path); err != nil {
		t.Fatal(err)
	}
	hits, err = db.Search("wc", "uniquequarantinetoken")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("search after delete = %+v, want none", hits)
	}
	var n int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM fts WHERE rowid = ?`, rowid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("fts rows for deleted rowid %d = %d, want 0", rowid, n)
	}
}

func TestUpsertReplacesFTSTokens(t *testing.T) {
	db := openTemp(t)
	p := proj("wc", "ab2c", "todo", "a0")
	p.Title = "oldtitleterm"
	p.Body = []byte("oldbodyterm")
	if err := db.UpsertTicket(p); err != nil {
		t.Fatal(err)
	}
	hits, err := db.Search("wc", "oldbodyterm")
	if err != nil || len(hits) != 1 {
		t.Fatalf("initial body MATCH = %+v err=%v", hits, err)
	}

	p.Title = "newtitleterm"
	p.Body = []byte("newbodyterm")
	if err := db.UpsertTicket(p); err != nil {
		t.Fatal(err)
	}
	for _, old := range []string{"oldtitleterm", "oldbodyterm"} {
		hits, err = db.Search("wc", old)
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) != 0 {
			t.Fatalf("stale MATCH for %q after rewrite: %+v", old, hits)
		}
	}
	for _, term := range []string{"newtitleterm", "newbodyterm"} {
		hits, err = db.Search("wc", term)
		if err != nil || len(hits) != 1 || hits[0].Ticket.ID != p.ID {
			t.Fatalf("rewrite MATCH for %q = %+v err=%v", term, hits, err)
		}
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

func TestSchemaEdgesRelation(t *testing.T) {
	db := openTemp(t)
	if SchemaVersion <= 3 {
		t.Fatalf("SchemaVersion = %d, want > 3", SchemaVersion)
	}
	var sql string
	if err := db.sql.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='edges'`).Scan(&sql); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"PRIMARY KEY (from_path, kind, to_id)",
		"CHECK (kind IN ('depends', 'related'))",
		"FOREIGN KEY (from_path) REFERENCES tickets(path) ON DELETE CASCADE",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("edges DDL missing %q:\n%s", want, sql)
		}
	}
	if strings.Contains(sql, "FOREIGN KEY (to_id)") || strings.Contains(sql, "REFERENCES tickets(id)") {
		t.Errorf("to_id must not be a foreign key:\n%s", sql)
	}

	rows, err := db.sql.Query(`PRAGMA table_info(edges)`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	pk := map[string]int{}
	for rows.Next() {
		var cid, notnull, pkOrd int
		var name, ctype string
		var dflt any
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pkOrd); err != nil {
			t.Fatal(err)
		}
		if pkOrd > 0 {
			pk[name] = pkOrd
		}
	}
	if pk["from_path"] != 1 || pk["kind"] != 2 || pk["to_id"] != 3 || len(pk) != 3 {
		t.Fatalf("edges PK columns = %v, want from_path=1 kind=2 to_id=3", pk)
	}

	fkRows, err := db.sql.Query(`PRAGMA foreign_key_list(edges)`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fkRows.Close() }()
	var nFK int
	for fkRows.Next() {
		var id, seq int
		var table, from, to, onUpdate, onDelete, match string
		if err := fkRows.Scan(&id, &seq, &table, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			t.Fatal(err)
		}
		nFK++
		if table != "tickets" || from != "from_path" || to != "path" || onDelete != "CASCADE" {
			t.Fatalf("edges FK = table=%s from=%s to=%s on_delete=%s", table, from, to, onDelete)
		}
	}
	if nFK != 1 {
		t.Fatalf("edges foreign keys = %d, want 1", nFK)
	}

	idxRows, err := db.sql.Query(`SELECT name FROM sqlite_master WHERE type='index' AND tbl_name='edges'`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idxRows.Close() }()
	idx := map[string]bool{}
	for idxRows.Next() {
		var name string
		if err := idxRows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		idx[name] = true
	}
	for _, name := range []string{
		"idx_edges_from", "idx_edges_to", "idx_edges_from_path",
		"idx_edges_to_scope", "idx_edges_from_scope_kind",
	} {
		if !idx[name] {
			t.Errorf("missing index %s in %v", name, idx)
		}
	}

	for _, want := range []string{
		"PRIMARY KEY", "CHECK kind IN", "ON DELETE CASCADE", "may dangle",
	} {
		if !strings.Contains(SchemaText, want) {
			t.Errorf("SchemaText missing %q:\n%s", want, SchemaText)
		}
	}
}

func TestEdgesConstraints(t *testing.T) {
	db := openTemp(t)
	from := proj("wc", "ab2c", "todo", "a0")
	if err := db.UpsertTicket(from); err != nil {
		t.Fatal(err)
	}
	insert := func(kind, toID string) error {
		_, err := db.sql.Exec(`INSERT INTO edges(from_path, from_id, from_scope, to_id, to_scope, kind) VALUES (?, ?, ?, ?, ?, ?)`,
			from.Path, from.ID, from.Scope, toID, "wc", kind)
		return err
	}

	if err := insert(EdgeDepends, "wc-missing"); err != nil {
		t.Fatalf("dangling to_id should insert: %v", err)
	}
	if err := insert(EdgeDepends, "wc-missing"); err == nil {
		t.Fatal("duplicate (from_path, kind, to_id) should fail")
	}
	if err := insert(EdgeRelated, "wc-missing"); err != nil {
		t.Fatalf("related to the same target should insert: %v", err)
	}
	if err := insert("blocked", "wc-zz99"); err == nil {
		t.Fatal("kind='blocked' should fail CHECK")
	}

	all, err := db.AllEdges()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("edges = %+v, want depends+related to wc-missing", all)
	}
	var sawDepends, sawRelated bool
	for _, e := range all {
		if e.ToID != "wc-missing" || e.FromPath != from.Path {
			t.Fatalf("unexpected edge %+v", e)
		}
		switch e.Kind {
		case EdgeDepends:
			sawDepends = true
		case EdgeRelated:
			sawRelated = true
		}
	}
	if !sawDepends || !sawRelated {
		t.Fatalf("want both kinds, got %+v", all)
	}
}

func TestDeleteTicketCascadesOutgoingEdges(t *testing.T) {
	db := openTemp(t)
	src := proj("wc", "ab2c", "todo", "a0")
	dst := proj("wc", "de34", "todo", "a1")
	other := proj("ui", "gh56", "todo", "a0")
	if err := db.UpsertTicketWithEdges(src, []Edge{
		{FromPath: src.Path, FromID: src.ID, FromScope: "wc", ToID: dst.ID, ToScope: "wc", Kind: EdgeDepends},
		{FromPath: src.Path, FromID: src.ID, FromScope: "wc", ToID: "wc-gone", ToScope: "wc", Kind: EdgeRelated},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertTicketWithEdges(dst, []Edge{
		{FromPath: dst.Path, FromID: dst.ID, FromScope: "wc", ToID: src.ID, ToScope: "wc", Kind: EdgeDepends},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertTicket(other); err != nil {
		t.Fatal(err)
	}

	if _, err := db.sql.Exec(`DELETE FROM tickets WHERE path = ?`, src.Path); err != nil {
		t.Fatal(err)
	}
	all, err := db.AllEdges()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].FromPath != dst.Path || all[0].ToID != src.ID {
		t.Fatalf("after deleting src, edges = %+v, want dst's dangling depends on src", all)
	}

	if err := db.UpsertTicketWithEdges(other, []Edge{
		{FromPath: other.Path, FromID: other.ID, FromScope: "ui", ToID: "ui-keep", ToScope: "ui", Kind: EdgeRelated},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteByPath(dst.Path); err != nil {
		t.Fatal(err)
	}
	all, err = db.AllEdges()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].FromPath != other.Path {
		t.Fatalf("DeleteByPath edges = %+v, want only ui keeper", all)
	}

	keep := proj("wc", "zz99", "todo", "a2")
	if err := db.UpsertTicketWithEdges(keep, []Edge{
		{FromPath: keep.Path, FromID: keep.ID, FromScope: "wc", ToID: "wc-gone", ToScope: "wc", Kind: EdgeDepends},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteScope("ui"); err != nil {
		t.Fatal(err)
	}
	all, err = db.AllEdges()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].FromPath != keep.Path {
		t.Fatalf("DeleteScope edges = %+v, want only wc keeper", all)
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
