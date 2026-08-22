package tkv

import (
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/index"
)

func TestAssignRanksPrereqOnTheLeft(t *testing.T) {
	ids := []string{"wc-aaaa", "wc-bbbb", "wc-cccc"}
	deps := map[string][]string{
		"wc-bbbb": {"wc-aaaa"},
		"wc-cccc": {"wc-bbbb"},
	}
	rank := assignRanks(ids, deps)
	if rank["wc-aaaa"] != 0 || rank["wc-bbbb"] != 1 || rank["wc-cccc"] != 2 {
		t.Fatalf("ranks = %v", rank)
	}
}

func TestAssignRanksCycleDoesNotHang(t *testing.T) {
	ids := []string{"a", "b"}
	deps := map[string][]string{"a": {"b"}, "b": {"a"}}
	rank := assignRanks(ids, deps)
	if _, ok := rank["a"]; !ok {
		t.Fatal("missing a")
	}
	if _, ok := rank["b"]; !ok {
		t.Fatal("missing b")
	}
}

func TestBuildDependsGraphLayoutAndEscape(t *testing.T) {
	tickets := []*index.Ticket{
		{ID: "wc-ab2c", ShortID: "ab2c", Scope: "wc", Status: `todo"><script>alert(1)</script><x class="`, OrderKey: "a0", Title: "Root <script>alert(1)</script>"},
		{ID: "wc-de34", ShortID: "de34", Scope: "wc", Status: "todo", OrderKey: "a1", Title: "Child"},
		{ID: "wc-gh56", ShortID: "gh56", Scope: "wc", Status: "todo", OrderKey: "a2", Title: "Alone"},
	}
	edges := []index.Edge{
		{FromID: "wc-de34", ToID: "wc-ab2c", FromScope: "wc", ToScope: "wc", Kind: index.EdgeDepends},
	}
	layout := buildDependsGraph("wc", false, nil, tickets, edges, nil)
	if len(layout.Nodes) != 2 {
		t.Fatalf("nodes = %d, want 2 (isolated omitted)", len(layout.Nodes))
	}
	if len(layout.Edges) != 1 {
		t.Fatalf("edges = %d", len(layout.Edges))
	}
	byID := map[string]layoutNode{}
	for _, n := range layout.Nodes {
		byID[n.ID] = n
	}
	if byID["wc-ab2c"].X >= byID["wc-de34"].X {
		t.Fatalf("prereq should sit to the left: root x=%d child x=%d", byID["wc-ab2c"].X, byID["wc-de34"].X)
	}
	iso := countIsolated("wc", false, nil, tickets, layout)
	if iso != 1 {
		t.Fatalf("isolated = %d, want 1", iso)
	}
	svg := string(renderDepSVG(layout))
	if strings.Contains(svg, "<script") {
		t.Fatalf("raw HTML leaked into svg: %s", svg)
	}
	if !strings.Contains(svg, "&lt;script&gt;") {
		t.Fatalf("title should be escaped: %s", svg)
	}
	if !strings.Contains(svg, `href="/scope/wc/wc-ab2c"`) {
		t.Fatalf("missing inspect link: %s", svg)
	}
	if !strings.Contains(svg, `class="dep-node status-todo"`) {
		t.Fatalf("status class should keep only the leading safe token: %s", svg)
	}
}

func TestStatusClassStopsAtUnsafe(t *testing.T) {
	if g := statusClass(`todo"><script>alert(1)</script><x class="`); g != "todo" {
		t.Fatalf("crafted status = %q, want todo", g)
	}
	if g := statusClass("in-progress"); g != "in_progress" {
		t.Fatalf("in-progress = %q", g)
	}
	if g := statusClass(`"><x`); g != "unknown" {
		t.Fatalf("leading junk = %q, want unknown", g)
	}
}

func TestBuildDependsGraphUnresolvedEndpoint(t *testing.T) {
	tickets := []*index.Ticket{
		{ID: "wc-de34", ShortID: "de34", Scope: "wc", Status: "todo", OrderKey: "a0", Title: "Child"},
	}
	edges := []index.Edge{
		{FromID: "wc-de34", ToID: "wc-zz99", FromScope: "wc", ToScope: "wc", Kind: index.EdgeDepends},
	}
	layout := buildDependsGraph("wc", false, nil, tickets, edges, nil)
	if len(layout.Nodes) != 2 {
		t.Fatalf("nodes = %d want 2", len(layout.Nodes))
	}
	var unresolved bool
	for _, n := range layout.Nodes {
		if n.ID == "wc-zz99" && n.Unresolved {
			unresolved = true
		}
	}
	if !unresolved {
		t.Fatal("missing unresolved node")
	}
	svg := string(renderDepSVG(layout))
	if strings.Contains(svg, `href="/scope/wc/wc-zz99"`) {
		t.Fatalf("unresolved node should not be a link: %s", svg)
	}
}
