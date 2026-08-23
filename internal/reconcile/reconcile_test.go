package reconcile

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"cuelang.org/go/cue/cuecontext"

	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/registry"
)

func newReconciler(t *testing.T) (*Reconciler, *index.DB) {
	t.Helper()
	db, err := index.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return New(db, cuecontext.New()), db
}

func mkScope(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "tk.cue"), "name: \""+name+"\"\nautoCommit: false\n")
	return dir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func projFile(id, status, order, body string) string {
	fm := "---\nid: " + id + "\nstatus: " + status + "\norder: \"" + order + "\"\ncreated: 2026-01-01T00:00:00Z\n---\n"
	return fm + body
}

func reconcileOne(t *testing.T, r *Reconciler, name, dir string, now int64) *Result {
	t.Helper()
	res, err := r.Reconcile(map[string]string{name: dir}, map[string]bool{name: true}, now)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	return res
}

func TestReconcileIndexesAndReflectsEdit(t *testing.T) {
	r, db := newReconciler(t)
	dir := mkScope(t, "wc")
	fp := filepath.Join(dir, "wc-ab2c-network.md")
	writeFile(t, fp, projFile("wc-ab2c", "todo", "a0", "# Network redesign\n\nbody"))

	reconcileOne(t, r, "wc", dir, time.Now().UnixNano())
	rows, _ := db.ScopeTickets("wc")
	if len(rows) != 1 || rows[0].Status != "todo" || rows[0].Title != "Network redesign" {
		t.Fatalf("initial row = %+v", rows)
	}

	writeFile(t, fp, projFile("wc-ab2c", "done", "a0", "# Network redesign\n\ndone"))
	future := time.Now().Add(time.Hour)
	_ = os.Chtimes(fp, future, future)
	reconcileOne(t, r, "wc", dir, future.Add(time.Minute).UnixNano())

	rows, _ = db.ScopeTickets("wc")
	if len(rows) != 1 || rows[0].Status != "done" {
		t.Fatalf("edited row = %+v", rows)
	}
}

func TestReconcileClosureWalksDependsTargets(t *testing.T) {
	r, db := newReconciler(t)
	up := mkScope(t, "up")
	wc := mkScope(t, "wc")
	writeFile(t, filepath.Join(up, "up-aa22-core.md"), projFile("up-aa22", "done", "a0", "# Core\n"))
	writeFile(t, filepath.Join(wc, "wc-bb22-feat.md"), "---\nid: wc-bb22\nstatus: todo\norder: \"a0\"\ncreated: 2026-01-01T00:00:00Z\ndepends: [up-aa22]\n---\n# Feature\n")
	reg := &registry.Registry{
		Scopes: map[string]registry.Entry{
			"wc": {Dir: wc},
			"up": {Dir: up},
		},
	}

	res, names, err := r.ReconcileClosure(reg, "wc", wc, time.Now().UnixNano())
	if err != nil {
		t.Fatalf("closure: %v", err)
	}
	got := map[string]bool{}
	for _, n := range names {
		got[n] = true
	}
	if !got["wc"] || !got["up"] {
		t.Errorf("closure names = %v, want wc and up", names)
	}
	if res.Schema("up") == nil {
		t.Error("want up schema after closure walk")
	}
	rows, err := db.TicketsByID("up", "up-aa22")
	if err != nil || len(rows) != 1 {
		t.Fatalf("up ticket indexed: n=%d err=%v", len(rows), err)
	}
}

func TestReconcileNamesScopeIndependently(t *testing.T) {
	r, db := newReconciler(t)
	dir := mkScope(t, "ui")
	writeFile(t, filepath.Join(dir, "ui-ab2c-x.md"), projFile("ui-ab2c", "todo", "a0", "# X"))
	reconcileOne(t, r, "ui", dir, time.Now().UnixNano())
	if rows, _ := db.ScopeTickets("ui"); len(rows) != 1 || rows[0].Scope != "ui" {
		t.Fatalf("ui rows = %+v", rows)
	}
}

func TestForeignScopeFrontmatterIDNotAdopted(t *testing.T) {
	// Foreign-scope FM id must not be adopted (would leave scope vs id-prefix disagreeing).
	r, db := newReconciler(t)
	dir := mkScope(t, "wc")
	fp := filepath.Join(dir, "wc-ab2c-note.md")
	writeFile(t, fp, projFile("sb-cd34", "todo", "a0", "# Note"))

	reconcileOne(t, r, "wc", dir, time.Now().UnixNano())
	rows, _ := db.ScopeTickets("wc")
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d: %+v", len(rows), rows)
	}
	if rows[0].ID != "wc-ab2c" || rows[0].ShortID != "ab2c" {
		t.Fatalf("foreign-scope id should be rejected; row id = %q short = %q", rows[0].ID, rows[0].ShortID)
	}
	if rows[0].Scope != "wc" {
		t.Fatalf("row scope = %q, want wc", rows[0].Scope)
	}
}

func TestSameScopeFrontmatterIDIsAuthoritative(t *testing.T) {
	// Same-scope id/filename drift: frontmatter id wins.
	r, db := newReconciler(t)
	dir := mkScope(t, "wc")
	writeFile(t, filepath.Join(dir, "wc-ab2c-note.md"), projFile("wc-cd34", "todo", "a0", "# Note"))

	reconcileOne(t, r, "wc", dir, time.Now().UnixNano())
	rows, _ := db.ScopeTickets("wc")
	if len(rows) != 1 || rows[0].ID != "wc-cd34" || rows[0].ShortID != "cd34" {
		t.Fatalf("same-scope frontmatter id should win; row = %+v", rows)
	}
}

func TestParseErrorQuarantineVsBodyMarkers(t *testing.T) {
	r, db := newReconciler(t)
	dir := mkScope(t, "wc")

	// Conflict markers inside the frontmatter fence → quarantine.
	bad := filepath.Join(dir, "wc-ab2c-broken.md")
	writeFile(t, bad, "---\nid: wc-ab2c\n<<<<<<< HEAD\nstatus: todo\n=======\nstatus: done\n>>>>>>> other\n---\n# T\n")

	// Conflict markers only in the body → indexed from clean FM.
	bodyOnly := filepath.Join(dir, "wc-de34-body.md")
	writeFile(t, bodyOnly, projFile("wc-de34", "todo", "a1", "# Title\n<<<<<<< HEAD\nmine\n=======\ntheirs\n>>>>>>> x\n"))

	res := reconcileOne(t, r, "wc", dir, time.Now().UnixNano())

	rows, _ := db.TicketsByID("wc", "wc-ab2c")
	if len(rows) != 1 || !rows[0].ParseError || rows[0].Status != "" {
		t.Fatalf("quarantined row = %+v", rows)
	}
	rows, _ = db.TicketsByID("wc", "wc-de34")
	if len(rows) != 1 || rows[0].ParseError || rows[0].Status != "todo" {
		t.Fatalf("body-only-marker row should index normally: %+v", rows)
	}
	if !hasToken(res.Warnings, "parse_error:") {
		t.Errorf("expected a parse_error warning, got %v", res.Warnings)
	}
}

func TestUnreachableScopeKeepsRows(t *testing.T) {
	r, db := newReconciler(t)
	dir := mkScope(t, "wc")
	writeFile(t, filepath.Join(dir, "wc-ab2c-x.md"), projFile("wc-ab2c", "todo", "a0", "# X"))
	reconcileOne(t, r, "wc", dir, time.Now().UnixNano())

	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	res := reconcileOne(t, r, "wc", dir, time.Now().UnixNano())
	if !res.Unreachable["wc"] {
		t.Fatalf("scope should be unreachable")
	}
	if rows, _ := db.ScopeTickets("wc"); len(rows) != 1 {
		t.Fatalf("unreachable scope rows should survive, got %d", len(rows))
	}
	if !hasToken(res.Warnings, "unreachable_scope:") {
		t.Errorf("expected unreachable_scope warning, got %v", res.Warnings)
	}
	// Unreachable rides unreachable_scope only — never also config_unparseable.
	if hasToken(res.Warnings, "config_unparseable:") {
		t.Errorf("unreachable scope must not also ride config_unparseable, got %v", res.Warnings)
	}
}

func TestForgottenScopePruned(t *testing.T) {
	r, db := newReconciler(t)
	dir := mkScope(t, "wc")
	writeFile(t, filepath.Join(dir, "wc-ab2c-x.md"), projFile("wc-ab2c", "todo", "a0", "# X"))
	reconcileOne(t, r, "wc", dir, time.Now().UnixNano())
	if rows, _ := db.ScopeTickets("wc"); len(rows) != 1 {
		t.Fatalf("precondition: wc should have a row")
	}

	other := mkScope(t, "ui")
	if _, err := r.Reconcile(map[string]string{"ui": other}, map[string]bool{"ui": true}, time.Now().UnixNano()); err != nil {
		t.Fatal(err)
	}
	if rows, _ := db.ScopeTickets("wc"); len(rows) != 0 {
		t.Fatalf("forgotten scope rows should be pruned, got %d", len(rows))
	}
}

func TestConfigCacheHitAndInvalidation(t *testing.T) {
	r, db := newReconciler(t)
	// Packaged scope with a sibling schema.cue — multi-file import closure.
	dir := filepath.Join(t.TempDir(), "wc")
	writeFile(t, filepath.Join(dir, "tk.cue"), "package wccfg\nname: \"wc\"\nautoCommit: false\nfields: { area: { type: \"string\", values: tags } }\n")
	writeFile(t, filepath.Join(dir, "schema.cue"), "package wccfg\ntags: [\"frontend\"]\n")

	schema, cfgErr := r.schemaFor("wc", dir)
	if cfgErr != nil || schema == nil {
		t.Fatalf("cold eval = %+v err=%v", schema, cfgErr)
	}
	if f, ok := schema.Field("area"); !ok || len(f.Values) != 1 || f.Values[0] != "frontend" {
		t.Fatalf("cold eval area = %+v", schema.Fields)
	}

	// Sentinel schema with untouched closure stats proves cache hit without re-eval.
	entry, _, _ := db.ConfigCacheGet("wc")
	entry.SchemaJSON = `{"Name":"wc","Fields":{"area":{"Type":"string","Values":["CACHED"]}}}`
	if err := db.ConfigCacheSet("wc", entry); err != nil {
		t.Fatal(err)
	}
	cached, _ := r.schemaFor("wc", dir)
	if cached == nil {
		t.Fatal("expected cache hit")
	}
	if f, ok := cached.Field("area"); !ok || len(f.Values) != 1 || f.Values[0] != "CACHED" {
		t.Fatalf("expected cache hit to serve the sentinel, got %+v", cached.Fields)
	}

	writeFile(t, filepath.Join(dir, "schema.cue"), "package wccfg\ntags: [\"backend\"]\n")
	future := time.Now().Add(time.Hour)
	_ = os.Chtimes(filepath.Join(dir, "schema.cue"), future, future)
	fresh, _ := r.schemaFor("wc", dir)
	if fresh == nil {
		t.Fatal("expected re-eval after closure change")
	}
	if f, ok := fresh.Field("area"); !ok || len(f.Values) != 1 || f.Values[0] != "backend" {
		t.Fatalf("closure change should re-evaluate, got %+v", fresh.Fields)
	}
}

func hasToken(warnings []string, prefix string) bool {
	for _, w := range warnings {
		if len(w) >= len(prefix) && w[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}
