package depgate

import (
	"os"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/scopeconfig"
	"github.com/p3bot/tk/internal/status"
	"github.com/p3bot/tk/internal/token"
)

func TestEvalDependsUnmet(t *testing.T) {
	prereq := tic("wc-ab2c", "todo", "a0", "/wc/wc-ab2c.md")
	auth := tic("wc-de34", "todo", "a1", "/wc/wc-de34.md")
	g := testGate([]*index.Ticket{prereq, auth}, map[string][]string{
		auth.Path: {prereq.ID},
	}, nil)

	ds := g.EvalDepends(auth)
	if len(ds.WaitingOn) != 1 || ds.WaitingOn[0] != prereq.ID {
		t.Fatalf("waiting-on = %v want [%s]", ds.WaitingOn, prereq.ID)
	}
	if !ds.Held() || g.NextEligible(auth, ds) {
		t.Fatal("unmet depend must hold and exclude from next")
	}
	if g.NextEligible(prereq, g.EvalDepends(prereq)) != true {
		t.Fatal("open prereq with no depends must be next-eligible")
	}
}

func TestEvalDependsDanglingSameScope(t *testing.T) {
	auth := tic("wc-de34", "todo", "a0", "/wc/wc-de34.md")
	g := testGate([]*index.Ticket{auth}, map[string][]string{
		auth.Path: {"wc-zz99"},
	}, nil)

	ds := g.EvalDepends(auth)
	if len(ds.WaitingOn) != 1 || ds.WaitingOn[0] != "wc-zz99" {
		t.Fatalf("waiting-on = %v", ds.WaitingOn)
	}
	if len(ds.Tokens) != 1 || !strings.HasPrefix(ds.Tokens[0], token.DependsDangling) {
		t.Fatalf("tokens = %v want depends_dangling:", ds.Tokens)
	}
	if g.NextEligible(auth, ds) {
		t.Fatal("dangling same-scope depend must exclude from next")
	}
}

func TestEvalDependsUnresolvableCrossScope(t *testing.T) {
	auth := tic("wc-de34", "todo", "a0", "/wc/wc-de34.md")
	g := testGate([]*index.Ticket{auth}, map[string][]string{
		auth.Path: {"other-ab2c"},
	}, nil)

	ds := g.EvalDepends(auth)
	if len(ds.WaitingOn) != 1 || ds.WaitingOn[0] != "other-ab2c" {
		t.Fatalf("waiting-on = %v", ds.WaitingOn)
	}
	if len(ds.Tokens) != 1 || !strings.HasPrefix(ds.Tokens[0], token.DependsUnresolvable) {
		t.Fatalf("tokens = %v want depends_unresolvable:", ds.Tokens)
	}
	if !ds.Held() || g.NextEligible(auth, ds) {
		t.Fatal("unresolvable cross-scope depend must hold and exclude from next")
	}
}

func TestEvalDependsCollisionNonTerminalHolds(t *testing.T) {
	done := tic("wc-ab2c", "done", "a0", "/wc/archive/wc-ab2c.md")
	done.Archived = true
	open := tic("wc-ab2c", "todo", "a1", "/wc/wc-ab2c.md")
	auth := tic("wc-de34", "todo", "a2", "/wc/wc-de34.md")
	g := testGate([]*index.Ticket{done, open, auth}, map[string][]string{
		auth.Path: {open.ID},
	}, nil)

	ds := g.EvalDepends(auth)
	if len(ds.WaitingOn) != 1 || ds.WaitingOn[0] != open.ID {
		t.Fatalf("collision with a non-terminal row must hold, waiting-on=%v", ds.WaitingOn)
	}
	if !ds.Held() || g.NextEligible(auth, ds) {
		t.Fatal("any non-terminal collision row must exclude the dependent from next")
	}
}

func TestEvalDependsSchemaErrorHold(t *testing.T) {
	broken := tic("wc-de34", "todo", "a0", "/wc/wc-de34.md")
	broken.SchemaError = true
	g := testGate([]*index.Ticket{broken}, nil, nil)

	ds := g.EvalDepends(broken)
	if len(ds.WaitingOn) != 0 {
		t.Fatalf("schema-error waiting-on = %v want empty", ds.WaitingOn)
	}
	if !ds.SchemaError || !ds.Held() {
		t.Fatal("schema error must Held even with no waiting-on ids")
	}
	if len(ds.Tokens) != 1 || !strings.HasPrefix(ds.Tokens[0], token.SchemaError) {
		t.Fatalf("tokens = %v want schema_error:", ds.Tokens)
	}
	if g.NextEligible(broken, ds) {
		t.Fatal("schema-error ticket must not be next-eligible")
	}
}

func TestSelectNextDuplicateIDSkip(t *testing.T) {
	dup := tic("wc-ab2c", "todo", "a0", "/wc/wc-ab2c.md")
	clean := tic("wc-de34", "todo", "a1", "/wc/wc-de34.md")
	g := testGate([]*index.Ticket{dup, clean}, nil, map[string]bool{
		"wc\x00wc-ab2c": true,
	})

	sel := g.SelectNext([]*index.Ticket{dup, clean}, nil, false)
	if sel.Chosen == nil || sel.Chosen.ID != clean.ID {
		t.Fatalf("chosen = %v want %s", idOf(sel.Chosen), clean.ID)
	}
	if sel.Blocked != 0 {
		t.Fatalf("duplicate skip must not count as blocked, blocked=%d", sel.Blocked)
	}
}

func TestSelectNextLensHideVsUntaggedVisible(t *testing.T) {
	hidden := tic("wc-ab2c", "todo", "a0", "/wc/wc-ab2c.md", "other")
	untagged := tic("wc-de34", "todo", "a1", "/wc/wc-de34.md")
	tagged := tic("wc-gh56", "todo", "a2", "/wc/wc-gh56.md", "auth")
	g := testGate([]*index.Ticket{hidden, untagged, tagged}, nil, nil)

	sel := g.SelectNext([]*index.Ticket{hidden, untagged, tagged}, []string{"auth"}, false)
	if sel.Chosen == nil || sel.Chosen.ID != untagged.ID {
		t.Fatalf("chosen = %v want untagged %s", idOf(sel.Chosen), untagged.ID)
	}
	if sel.ReadyOutsideLens != 1 {
		t.Fatalf("ready outside lens = %d want 1 (tagged other)", sel.ReadyOutsideLens)
	}

	noLens := g.SelectNext([]*index.Ticket{hidden, untagged, tagged}, []string{"auth"}, true)
	if noLens.Chosen == nil || noLens.Chosen.ID != hidden.ID {
		t.Fatalf("--no-lens chosen = %v want first by order %s", idOf(noLens.Chosen), hidden.ID)
	}
	if noLens.ApplyLens {
		t.Fatal("--no-lens must not apply the stored lens")
	}
}

func TestSelectNextEmptyCandidates(t *testing.T) {
	g := testGate(nil, nil, nil)
	sel := g.SelectNext(nil, []string{"auth"}, false)
	if sel.Chosen != nil {
		t.Fatalf("chosen = %s want nil", sel.Chosen.ID)
	}
	if sel.Blocked != 0 || sel.ReadyOutsideLens != 0 {
		t.Fatalf("empty queue counts blocked=%d outside=%d", sel.Blocked, sel.ReadyOutsideLens)
	}
	if !sel.ApplyLens {
		t.Fatal("empty candidates still apply a non-empty lens")
	}
}

func TestLoadSelectNextFromIndex(t *testing.T) {
	db, err := index.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	open := &index.Ticket{
		Path: "/tmp/wc/wc-ab2c-network.md", Scope: "wc", ID: "wc-ab2c", ShortID: "ab2c",
		Status: status.Todo, OrderKey: "a0", Title: "Network",
	}
	held := &index.Ticket{
		Path: "/tmp/wc/wc-de34-auth.md", Scope: "wc", ID: "wc-de34", ShortID: "de34",
		Status: status.Todo, OrderKey: "a1", Title: "Auth",
	}
	extraDone := &index.Ticket{
		Path: "/tmp/other/archive/other-ab2c-prereq.md", Scope: "other", ID: "other-ab2c", ShortID: "ab2c",
		Status: status.Done, OrderKey: "a0", Title: "Prereq", Archived: true,
	}
	if err := db.UpsertTicket(open); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertTicket(extraDone); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertTicketWithEdges(held, []index.Edge{
		{FromPath: held.Path, FromID: held.ID, FromScope: "wc", ToID: open.ID, ToScope: "wc", Kind: index.EdgeDepends},
		{FromPath: held.Path, FromID: held.ID, FromScope: "wc", ToID: extraDone.ID, ToScope: "other", Kind: index.EdgeDepends},
	}); err != nil {
		t.Fatal(err)
	}

	gate, err := Load(Deps{DB: db}, nil, []string{"wc"})
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := db.NextCandidates("wc")
	if err != nil {
		t.Fatal(err)
	}
	sel := gate.SelectNext(candidates, nil, false)
	if sel.Chosen == nil || sel.Chosen.ID != open.ID {
		t.Fatalf("next id = %v want %s", idOf(sel.Chosen), open.ID)
	}
	if sel.Chosen.Path != open.Path {
		t.Fatalf("chosen path = %q want %q", sel.Chosen.Path, open.Path)
	}

	var auth *index.Ticket
	for _, p := range candidates {
		if p.ID == held.ID {
			auth = p
			break
		}
	}
	if auth == nil {
		t.Fatal("held todo missing from NextCandidates")
	}
	waiting := gate.EvalDepends(auth).WaitingOn
	if len(waiting) != 1 || waiting[0] != open.ID {
		t.Fatalf("waiting-on = %v want [%s]; extra-scope done target must be loaded and must not hold", waiting, open.ID)
	}
	if sel.Blocked != 1 {
		t.Fatalf("blocked = %d want 1", sel.Blocked)
	}
}

func TestEvalDependsTerminalTargetClearsHold(t *testing.T) {
	done := tic("wc-ab2c", "done", "a0", "/wc/archive/wc-ab2c.md")
	done.Archived = true
	auth := tic("wc-de34", "todo", "a1", "/wc/wc-de34.md")
	g := testGate([]*index.Ticket{done, auth}, map[string][]string{
		auth.Path: {done.ID},
	}, nil)

	ds := g.EvalDepends(auth)
	if len(ds.WaitingOn) != 0 || ds.Held() {
		t.Fatalf("terminal target must clear hold, waiting-on=%v", ds.WaitingOn)
	}
	if !g.NextEligible(auth, ds) {
		t.Fatal("auth must be next-eligible once the prereq is done")
	}
}

func TestEvalDependsCustomTerminalFromCachedSchema(t *testing.T) {
	shipped := tic("other-ab2c", "shipped", "a0", "/other/archive/other-ab2c.md")
	auth := tic("wc-de34", "todo", "a0", "/wc/wc-de34.md")
	g := testGate([]*index.Ticket{shipped, auth}, map[string][]string{
		auth.Path: {shipped.ID},
	}, nil)
	g.schemas["other"] = &scopeconfig.Schema{
		Statuses: map[string]status.Category{"shipped": status.CategoryDone},
	}

	ds := g.EvalDepends(auth)
	if len(ds.WaitingOn) != 0 {
		t.Fatalf("custom done target must satisfy, waiting-on=%v", ds.WaitingOn)
	}
}

func TestPackageHasNoCLIOrCobraAndNoAllDump(t *testing.T) {
	src, err := os.ReadFile("depgate.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	if strings.Contains(body, "github.com/spf13/cobra") {
		t.Error("depgate.go imports cobra")
	}
	if strings.Contains(body, "github.com/p3bot/tk/internal/cli") {
		t.Error("depgate.go imports internal/cli")
	}
	if strings.Contains(body, "AllTickets") || strings.Contains(body, "AllEdges") {
		t.Error("depgate.go uses a machine-wide dump")
	}
}

func testGate(tickets []*index.Ticket, depends map[string][]string, dups map[string]bool) *Gate {
	g := &Gate{
		byID:    map[string][]*index.Ticket{},
		depends: depends,
		schemas: map[string]*scopeconfig.Schema{},
		dupSet:  dups,
	}
	if g.depends == nil {
		g.depends = map[string][]string{}
	}
	if g.dupSet == nil {
		g.dupSet = map[string]bool{}
	}
	for _, p := range tickets {
		g.byID[p.ID] = append(g.byID[p.ID], p)
	}
	return g
}

func tic(id, st, order, path string, tags ...string) *index.Ticket {
	scope := id
	if i := strings.IndexByte(id, '-'); i >= 0 {
		scope = id[:i]
	}
	return &index.Ticket{
		Path:     path,
		Scope:    scope,
		ID:       id,
		Status:   st,
		OrderKey: order,
		Tags:     tags,
	}
}

func idOf(p *index.Ticket) string {
	if p == nil {
		return "<nil>"
	}
	return p.ID
}

func TestEmptyQueueErrorWording(t *testing.T) {
	cases := []struct {
		sel  Selection
		want string
	}{
		{Selection{}, "nothing ready"},
		{Selection{Blocked: 2}, "nothing ready; 2 todo(s) waiting on unmet deps"},
		{
			sel:  Selection{ApplyLens: true, Lens: []string{"a", "b"}, ReadyOutsideLens: 3},
			want: "nothing ready under lens [a, b]; 3 ready outside it",
		},
	}
	for _, tc := range cases {
		got := tc.sel.EmptyQueueError().Error()
		if got != tc.want {
			t.Errorf("%+v: %q want %q", tc.sel, got, tc.want)
		}
	}
}
