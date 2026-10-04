package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/token"
)

func TestDesignIndexStaysOffBoard(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	t.Setenv("TK_SCOPE", "wc")
	addTicket(t, dir, "wc-m4np", "target", "todo", "a0", "# Target\n\nticketonlyterm\n", false, "")
	writeDesign(t, dir, "wc-ab2c", "shape", "draft", "2026-01-01T00:00:00Z", "Shape")
	raw := "---\nid: wc-ab2c\nstatus: draft\ncreated: 2026-01-01T00:00:00Z\nproduces: [wc-m4np, wc-zzzz]\n---\n# Shape\n\ndesignonlyterm\n"
	designPath := filepath.Join(dir, "design", "wc-ab2c-shape.md")
	if err := os.WriteFile(designPath, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := run(t, app, "reindex"); err != nil {
		t.Fatalf("reindex: %v", err)
	}

	got, _, err := run(t, app, "query", "SELECT id FROM designs ORDER BY id")
	if err != nil {
		t.Fatalf("query designs: %v", err)
	}
	if !strings.Contains(got, "wc-ab2c") {
		t.Fatalf("designs query = %q", got)
	}
	paths, _, err := run(t, app, "query", "SELECT path FROM designs WHERE id = 'wc-ab2c'")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(paths, designPath) {
		t.Fatalf("design path query = %q, want %s", paths, designPath)
	}
	tickets, _, err := run(t, app, "query", "SELECT id FROM tickets")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(tickets, "wc-ab2c") || !strings.Contains(tickets, "wc-m4np") {
		t.Fatalf("tickets query = %q", tickets)
	}
	edges, _, err := run(t, app, "query", "SELECT to_id FROM edges WHERE kind = 'produces' ORDER BY to_id")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(edges, "wc-m4np") || !strings.Contains(edges, "wc-zzzz") {
		t.Fatalf("produces edges = %q", edges)
	}

	list, _, err := run(t, app, "list", "--all")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(list, "wc-ab2c") || !strings.Contains(list, "wc-m4np") {
		t.Fatalf("list = %q", list)
	}
	next, _, err := run(t, app, "next")
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if !strings.Contains(next, "wc-m4np-target.md") || strings.Contains(next, "wc-ab2c") {
		t.Fatalf("next = %q", next)
	}
	depends, _, err := run(t, app, "depends", "wc-m4np")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(depends, "produced by:\n  wc-ab2c\tdraft\tShape") {
		t.Fatalf("produced by = %q", depends)
	}
	if strings.Contains(depends, "depends on:\n  wc-ab2c") || strings.Contains(depends, "is depended on by:\n  wc-ab2c") || strings.Contains(depends, "related:\n  wc-ab2c") {
		t.Fatalf("design leaked into ticket sections: %q", depends)
	}
	onDesign, _, err := run(t, app, "depends", "wc-ab2c")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(onDesign, "depends on:") || strings.Contains(onDesign, "related:") || strings.Contains(onDesign, "produced by:") {
		t.Fatalf("design depends must be produces only: %q", onDesign)
	}
	if !strings.Contains(onDesign, "produces:\n  wc-m4np\ttodo\tTarget\n  wc-zzzz\t(unresolved)") {
		t.Fatalf("produces = %q", onDesign)
	}
	tree, _, err := run(t, app, "depends", "wc-m4np", "--tree")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(tree, "ab2c") || strings.Contains(tree, "produced by") {
		t.Fatalf("tree included the design: %q", tree)
	}
	searchDesign, _, err := run(t, app, "search", "designonlyterm")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(searchDesign) != "" || strings.Contains(searchDesign, "wc-ab2c") {
		t.Fatalf("design word changed ticket search: %q", searchDesign)
	}
	searchTicket, _, err := run(t, app, "search", "ticketonlyterm")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(searchTicket, "wc-m4np") || strings.Contains(searchTicket, "wc-ab2c") {
		t.Fatalf("ticket search = %q", searchTicket)
	}

	doc, _, err := run(t, app, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(doc, token.DependsDangling) || strings.Contains(doc, token.DependsUnresolvable) {
		t.Fatalf("produces edge changed doctor depends tokens: %q", doc)
	}

	schema, _, err := run(t, app, "query", "--schema")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"designs", "produces", "design search index", "NOT A STABLE API"} {
		if !strings.Contains(schema, want) {
			t.Errorf("schema text missing %q", want)
		}
	}

	if err := os.Remove(designPath); err != nil {
		t.Fatal(err)
	}
	searchTicket, _, err = run(t, app, "search", "ticketonlyterm")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(searchTicket, "wc-m4np") {
		t.Fatalf("ticket search after design delete = %q", searchTicket)
	}
	searchDesign, _, err = run(t, app, "search", "designonlyterm")
	if err != nil || strings.TrimSpace(searchDesign) != "" {
		t.Fatalf("design word after delete = %q err=%v", searchDesign, err)
	}
	edges, _, err = run(t, app, "query", "SELECT to_id FROM edges WHERE kind = 'produces'")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(edges, "wc-m4np") || strings.Contains(edges, "wc-zzzz") {
		t.Fatalf("produces edges survived delete: %q", edges)
	}
	left, _, err := run(t, app, "query", "SELECT id FROM designs")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(left, "wc-ab2c") {
		t.Fatalf("design row survived delete: %q", left)
	}
}

func TestRehomeIgnoresProducesEdge(t *testing.T) {
	app := newApp(t)
	foo := initScope(t, app, "foo")
	initScope(t, app, "bar")
	api := initScope(t, app, "api")
	addTicket(t, foo, "foo-ab2c", "moved", "todo", "a0", "# Moved\n", false, "")
	writeDesign(t, api, "api-xy99", "remote", "draft", "2026-01-01T00:00:00Z", "Remote")
	raw := "---\nid: api-xy99\nstatus: draft\ncreated: 2026-01-01T00:00:00Z\nproduces: [foo-ab2c]\n---\n# Remote\n"
	designPath := filepath.Join(api, "design", "api-xy99-remote.md")
	if err := os.WriteFile(designPath, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	_, errOut, err := run(t, app, "rehome", "foo-ab2c", "bar")
	if err != nil {
		t.Fatalf("rehome: %v (%s)", err, errOut)
	}
	if strings.Contains(errOut, "edge_verify:") {
		t.Fatalf("produces edge added an edge_verify line: %q", errOut)
	}
	body, err := os.ReadFile(designPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "foo-ab2c") {
		t.Fatalf("third-scope design was rewritten: %s", body)
	}
}
