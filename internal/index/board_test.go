package index

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/status"
)

func TestSchemaHasQuerySurface(t *testing.T) {
	db := openTemp(t)
	if SchemaVersion <= 2 {
		t.Fatalf("SchemaVersion = %d, want > 2", SchemaVersion)
	}
	var n int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='ticket_tags'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("ticket_tags table missing: n=%d err=%v", n, err)
	}
	rows, err := db.sql.Query(`PRAGMA table_info(tickets)`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	cols := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		cols[name] = true
	}
	if cols["tags"] {
		t.Fatal("tickets.tags column must not exist")
	}
	if !cols["custom"] || !cols["status_conflict"] {
		t.Fatalf("tickets columns = %v", cols)
	}
	if !strings.Contains(SchemaText, "ticket_tags") || !strings.Contains(SchemaText, "NOT A STABLE API") {
		t.Fatalf("SchemaText must describe ticket_tags and the instability warning:\n%s", SchemaText)
	}
	if strings.Contains(SchemaText, "tags, custom") {
		t.Fatal("SchemaText still documents tickets.tags")
	}
}

func TestEmptyJSONAndTagsRelation(t *testing.T) {
	db := openTemp(t)
	p := proj("wc", "ab2c", "todo", "a0")
	p.Tags = []string{"z", "", "a", "z", "b"}
	if err := db.UpsertTicket(p); err != nil {
		t.Fatal(err)
	}
	var custom, conflict string
	if err := db.sql.QueryRow(`SELECT custom, status_conflict FROM tickets WHERE path = ?`, p.Path).
		Scan(&custom, &conflict); err != nil {
		t.Fatal(err)
	}
	if custom != "{}" || conflict != "[]" {
		t.Fatalf("custom=%q conflict=%q, want {} and []", custom, conflict)
	}
	got, err := db.ScopeTickets("wc")
	if err != nil || len(got) != 1 {
		t.Fatalf("rows=%v err=%v", got, err)
	}
	if !slices.Equal(got[0].Tags, []string{"z", "a", "b"}) {
		t.Fatalf("tags = %v, want first-wins [z a b]", got[0].Tags)
	}
	var n int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM ticket_tags WHERE path = ?`, p.Path).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("ticket_tags rows = %d, want 3", n)
	}

	p.Tags = []string{"only"}
	p.Custom = map[string]any{"area": "net"}
	p.StatusConflict = []string{"done", "cancelled"}
	if err := db.UpsertTicket(p); err != nil {
		t.Fatal(err)
	}
	got, _ = db.ScopeTickets("wc")
	if !slices.Equal(got[0].Tags, []string{"only"}) {
		t.Fatalf("replaced tags = %v", got[0].Tags)
	}
	if err := db.sql.QueryRow(`SELECT custom, status_conflict FROM tickets WHERE path = ?`, p.Path).
		Scan(&custom, &conflict); err != nil {
		t.Fatal(err)
	}
	if custom != `{"area":"net"}` {
		t.Fatalf("custom = %q", custom)
	}
	if conflict != `["done","cancelled"]` {
		t.Fatalf("status_conflict = %q", conflict)
	}
}

func TestScopeTagInventory(t *testing.T) {
	db := openTemp(t)
	a := proj("wc", "ab2c", "todo", "a0")
	a.Tags = []string{"frontend", "shared"}
	b := proj("wc", "de34", "done", "a1")
	b.Archived = true
	b.Tags = []string{"legacy", "shared"}
	c := proj("wc", "gh56", "todo", "a2")
	c.Tags = []string{"", "frontend"}
	other := proj("ui", "jk89", "todo", "a0")
	other.Tags = []string{"ui-only"}
	for _, p := range []*Ticket{a, b, c, other} {
		if err := db.UpsertTicket(p); err != nil {
			t.Fatal(err)
		}
	}
	got, err := db.ScopeDistinctTags("wc")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"frontend", "legacy", "shared"}
	if !slices.Equal(got, want) {
		t.Fatalf("ScopeDistinctTags = %v, want %v", got, want)
	}
	mem, err := db.ScopeTagMembership("wc")
	if err != nil {
		t.Fatal(err)
	}
	if len(mem) != len(want) {
		t.Fatalf("membership %v", mem)
	}
	for _, tag := range want {
		if _, ok := mem[tag]; !ok {
			t.Fatalf("missing %q", tag)
		}
	}
}

func TestBoardTicketsVisibility(t *testing.T) {
	db := openTemp(t)
	seed := []*Ticket{
		tagged(proj("wc", "aa22", "todo", "a0"), "frontend"),
		tagged(proj("wc", "ab23", "todo", "a1"), "backend"),
		tagged(proj("wc", "ac24", "done", "a2"), "legacy"),
		func() *Ticket {
			p := tagged(proj("wc", "ad25", "done", "a3"), "legacy")
			p.Archived = true
			p.Path = filepath.Join("/tmp", "wc", "archive", "ad25.md")
			return p
		}(),
		proj("wc", "ae26", "todo", "a4"), // untagged
		func() *Ticket {
			p := proj("wc", "af27", "todo", "a5")
			p.ParseError = true
			return p
		}(),
		proj("wc", "ag28", "backlog", "a6"),
	}
	seed[2].Path = filepath.Join("/tmp", "wc", "ac24.md") // done at root
	for _, p := range seed {
		if err := db.UpsertTicket(p); err != nil {
			t.Fatal(err)
		}
	}

	ids := func(rows []*Ticket) []string {
		out := make([]string, len(rows))
		for i, p := range rows {
			out[i] = p.ID
		}
		return out
	}

	def := status.DefaultListNames(nil)
	got, err := db.BoardTickets(BoardFilter{Scope: "wc", DefaultStatuses: def})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids(got), []string{"wc-aa22", "wc-ab23", "wc-ae26"}) {
		t.Fatalf("default board = %v", ids(got))
	}
	zero, err := db.BoardTickets(BoardFilter{Scope: "wc"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids(zero), ids(got)) {
		t.Fatalf("zero-value DefaultStatuses = %v, want builtin default board %v", ids(zero), ids(got))
	}

	got, _ = db.BoardTickets(BoardFilter{Scope: "wc", All: true})
	if !slices.Equal(ids(got), []string{"wc-aa22", "wc-ab23", "wc-ac24", "wc-ad25", "wc-ae26", "wc-ag28"}) {
		t.Fatalf("--all board = %v", ids(got))
	}

	got, _ = db.BoardTickets(BoardFilter{Scope: "wc", Statuses: []string{"done"}})
	if !slices.Equal(ids(got), []string{"wc-ac24", "wc-ad25"}) {
		t.Fatalf("list done = %v", ids(got))
	}

	got, _ = db.BoardTickets(BoardFilter{Scope: "wc", DefaultStatuses: def, Tags: []string{"backend"}})
	if !slices.Equal(ids(got), []string{"wc-ab23"}) {
		t.Fatalf("--tag backend = %v", ids(got))
	}

	got, _ = db.BoardTickets(BoardFilter{Scope: "wc", DefaultStatuses: def, Tags: []string{"backend", "frontend"}})
	if !slices.Equal(ids(got), []string{"wc-aa22", "wc-ab23"}) {
		t.Fatalf("--tag OR = %v", ids(got))
	}

	got, _ = db.BoardTickets(BoardFilter{Scope: "wc", DefaultStatuses: def, Lens: []string{"frontend"}})
	if !slices.Equal(ids(got), []string{"wc-aa22", "wc-ae26"}) {
		t.Fatalf("lens frontend (untagged pass) = %v", ids(got))
	}

	got, _ = db.BoardTickets(BoardFilter{Scope: "wc", DefaultStatuses: def, Tags: []string{"backend"}, Lens: []string{"frontend"}})
	if !slices.Equal(ids(got), []string{"wc-ab23"}) {
		t.Fatalf("--tag must ignore lens = %v", ids(got))
	}

	got, _ = db.BoardTickets(BoardFilter{Scope: "wc", DefaultStatuses: def, Tags: []string{""}})
	if len(got) != 0 {
		t.Fatalf("--tag empty string must match nothing, got %v", ids(got))
	}

	got, _ = db.BoardTickets(BoardFilter{Scope: "wc", DefaultStatuses: def, Lens: []string{""}})
	if !slices.Equal(ids(got), []string{"wc-ae26"}) {
		t.Fatalf("lens empty string keeps untagged only, got %v", ids(got))
	}
}

func TestBoardTicketsCustomDefaultStatuses(t *testing.T) {
	db := openTemp(t)
	_ = db.UpsertTicket(proj("wc", "aa22", "todo", "a0"))
	_ = db.UpsertTicket(proj("wc", "ab23", "triaged", "a1"))
	_ = db.UpsertTicket(proj("wc", "ac24", "icebox", "a2"))
	_ = db.UpsertTicket(proj("wc", "ad25", "shipped", "a3"))

	custom := map[string]status.Category{
		"triaged": status.CategoryActive,
		"icebox":  status.CategoryBacklog,
		"shipped": status.CategoryDone,
	}
	got, err := db.BoardTickets(BoardFilter{
		Scope:           "wc",
		DefaultStatuses: status.DefaultListNames(custom),
	})
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, len(got))
	for i, p := range got {
		ids[i] = p.ID
	}
	if !slices.Equal(ids, []string{"wc-aa22", "wc-ab23"}) {
		t.Fatalf("custom default board = %v, want [wc-aa22 wc-ab23] (todo + active custom; not backlog/done custom)", ids)
	}

	got, err = db.BoardTickets(BoardFilter{Scope: "wc"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "wc-aa22" {
		t.Fatalf("zero-value BoardFilter = %v, want only todo (custom statuses unknown)", func() []string {
			out := make([]string, len(got))
			for i, p := range got {
				out[i] = p.ID
			}
			return out
		}())
	}
}

func tagged(p *Ticket, tags ...string) *Ticket {
	p.Tags = tags
	return p
}

func TestNextCandidatesSQL(t *testing.T) {
	db := openTemp(t)
	_ = db.UpsertTicket(proj("wc", "aa22", "todo", "a1"))
	done := proj("wc", "ab23", "done", "a0")
	done.Archived = true
	_ = db.UpsertTicket(done)
	review := proj("wc", "ac24", "review", "a2")
	_ = db.UpsertTicket(review)
	todo2 := proj("wc", "ad25", "todo", "a0")
	_ = db.UpsertTicket(todo2)
	pe := proj("wc", "ae26", "todo", "a3")
	pe.ParseError = true
	_ = db.UpsertTicket(pe)

	got, err := db.NextCandidates("wc")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "wc-ad25" || got[1].ID != "wc-aa22" {
		t.Fatalf("next = %v", func() []string {
			s := make([]string, len(got))
			for i, p := range got {
				s[i] = p.ID
			}
			return s
		}())
	}
}

func TestDependsTargetScopesAndDangling(t *testing.T) {
	db := openTemp(t)
	a := proj("wc", "aa22", "todo", "a0")
	b := proj("ui", "bb22", "todo", "a0")
	_ = db.UpsertTicketWithEdges(a, []Edge{
		{FromPath: a.Path, FromID: a.ID, FromScope: "wc", ToID: "ui-bb22", ToScope: "ui", Kind: EdgeDepends},
		{FromPath: a.Path, FromID: a.ID, FromScope: "wc", ToID: "wc-missing", ToScope: "wc", Kind: EdgeDepends},
		{FromPath: a.Path, FromID: a.ID, FromScope: "wc", ToID: "wc-zz99", ToScope: "wc", Kind: EdgeRelated},
	})
	_ = db.UpsertTicket(b)

	to, err := db.DependsTargetScopes([]string{"wc"})
	if err != nil {
		t.Fatal(err)
	}
	set := map[string]bool{}
	for _, s := range to {
		set[s] = true
	}
	if !set["ui"] || !set["wc"] {
		t.Fatalf("target scopes = %v", to)
	}
	n, err := db.SameScopeDanglingDependsCount("wc")
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("dangling = %d, want 1", n)
	}
	edges, err := db.DependsFromScopes([]string{"wc"})
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 2 {
		t.Fatalf("depends from wc = %+v, want 2 (related excluded)", edges)
	}
}

func TestCollisionHaving(t *testing.T) {
	db := openTemp(t)
	dupA := proj("wc", "ab2c", "todo", "a0")
	dupB := proj("wc", "ab2c", "done", "a1")
	dupB.Path = "/tmp/wc/archive/ab2c.md"
	same1 := proj("wc", "de34", "todo", "b0")
	same2 := proj("wc", "gh56", "todo", "b0")
	lonely := proj("ui", "jk89", "todo", "a0")
	for _, p := range []*Ticket{dupA, dupB, same1, same2, lonely} {
		if err := db.UpsertTicket(p); err != nil {
			t.Fatal(err)
		}
	}
	dups, err := db.DuplicateIDs([]string{"wc", "ui"})
	if err != nil {
		t.Fatal(err)
	}
	if len(dups) != 1 || dups[0].Key != "wc-ab2c" || len(dups[0].Members) != 2 {
		t.Fatalf("duplicate ids = %+v", dups)
	}
	eq, err := db.EqualOrders([]string{"wc"})
	if err != nil {
		t.Fatal(err)
	}
	if len(eq) != 1 || eq[0].Key != "b0" || len(eq[0].Members) != 2 {
		t.Fatalf("equal orders = %+v", eq)
	}
}

func TestScopePulse(t *testing.T) {
	db := openTemp(t)
	_ = db.UpsertTicket(tagged(proj("wc", "aa22", "todo", "a0"), "frontend"))
	_ = db.UpsertTicket(tagged(proj("wc", "ab23", "todo", "a1"), "backend"))
	ip := tagged(proj("wc", "ac24", "in-progress", "a2"), "frontend")
	_ = db.UpsertTicket(ip)
	done := proj("wc", "ad25", "done", "a3")
	done.Archived = true
	_ = db.UpsertTicket(done)

	all, err := db.ScopePulse("wc", nil)
	if err != nil {
		t.Fatal(err)
	}
	if all.Total != 4 || all.Todo != 2 || all.InProgress != 1 || all.Done != 1 {
		t.Fatalf("pulse no lens = %+v", all)
	}
	if len(all.Claimed) != 1 || all.Claimed[0].ID != "wc-ac24" {
		t.Fatalf("claimed = %+v", all.Claimed)
	}

	fe, err := db.ScopePulse("wc", []string{"frontend"})
	if err != nil {
		t.Fatal(err)
	}
	if fe.Total != 4 || fe.Todo != 1 || fe.InProgress != 1 || fe.Done != 1 {
		t.Fatalf("pulse lens = %+v", fe)
	}

	empty, err := db.ScopePulse("wc", []string{""})
	if err != nil {
		t.Fatal(err)
	}
	if empty.Total != 4 || empty.Todo != 0 || empty.InProgress != 0 || len(empty.Claimed) != 0 {
		t.Fatalf("pulse lens empty string must hide tagged working rows, got %+v", empty)
	}
}

func TestSearchAttachesTags(t *testing.T) {
	db := openTemp(t)
	p := tagged(proj("wc", "ab2c", "todo", "a0"), "network")
	p.Title = "Network"
	p.Body = []byte("sockets")
	if err := db.UpsertTicket(p); err != nil {
		t.Fatal(err)
	}
	hits, err := db.Search("wc", "network")
	if err != nil || len(hits) != 1 {
		t.Fatalf("hits=%v err=%v", hits, err)
	}
	if !slices.Equal(hits[0].Ticket.Tags, []string{"network"}) {
		t.Fatalf("search tags = %v", hits[0].Ticket.Tags)
	}
}

func TestDeleteRemovesTags(t *testing.T) {
	db := openTemp(t)
	p := tagged(proj("wc", "ab2c", "todo", "a0"), "x")
	keep := tagged(proj("ui", "de34", "todo", "a0"), "y")
	_ = db.UpsertTicket(p)
	_ = db.UpsertTicket(keep)
	if err := db.DeleteByPath(p.Path); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = db.sql.QueryRow(`SELECT COUNT(*) FROM ticket_tags WHERE path = ?`, p.Path).Scan(&n)
	if n != 0 {
		t.Fatalf("tags survived delete: %d", n)
	}
	_ = db.sql.QueryRow(`SELECT COUNT(*) FROM ticket_tags WHERE path = ?`, keep.Path).Scan(&n)
	if n != 1 {
		t.Fatalf("keeper tags = %d, want 1", n)
	}
}

func TestArchiveDriftExists(t *testing.T) {
	db := openTemp(t)
	term := status.TerminalNames(nil)
	ok, err := db.HasArchiveDrift("wc", term)
	if err != nil || ok {
		t.Fatalf("empty scope drift = %v err=%v, want false", ok, err)
	}

	_ = db.UpsertTicket(proj("wc", "aa22", "todo", "a0"))
	done := proj("wc", "ab23", "done", "a1")
	done.Archived = true
	done.Path = filepath.Join("/tmp", "wc", "archive", "ab23.md")
	_ = db.UpsertTicket(done)
	ok, err = db.HasArchiveDrift("wc", term)
	if err != nil || ok {
		t.Fatalf("healthy layout drift = %v err=%v, want false", ok, err)
	}

	rootDone := proj("wc", "ac24", "done", "a2")
	_ = db.UpsertTicket(rootDone)
	ok, err = db.HasArchiveDrift("wc", term)
	if err != nil || !ok {
		t.Fatalf("terminal at root drift = %v err=%v, want true", ok, err)
	}
	rows, err := db.ArchiveDrift("wc", term)
	if err != nil || len(rows) != 1 || rows[0].ID != "wc-ac24" {
		t.Fatalf("ArchiveDrift = %v err=%v, want wc-ac24", func() []string {
			out := make([]string, len(rows))
			for i, p := range rows {
				out[i] = p.ID
			}
			return out
		}(), err)
	}
}
