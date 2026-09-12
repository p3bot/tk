package cli

import (
	"testing"

	"github.com/p3bot/tk/internal/index"
)

func graphKnown(ids ...string) *dependsGraph {
	g := newDependsGraph()
	for _, id := range ids {
		g.byID[id] = &index.Ticket{ID: id}
	}
	return g
}

func TestFormatForestChain(t *testing.T) {
	g := graphKnown("wc-aa22", "wc-bb33", "wc-cc44")
	g.outDep["wc-aa22"] = []string{"wc-bb33"}
	g.outDep["wc-bb33"] = []string{"wc-cc44"}
	got := g.formatForest([]string{"wc-aa22"}, "wc")
	want := "aa22 ─── bb33 ── cc44\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestFormatForestBushyLeaves(t *testing.T) {
	g := graphKnown("wc-aa22", "wc-dd55", "wc-bb33", "wc-cc44")
	g.outDep["wc-aa22"] = []string{"wc-dd55", "wc-bb33", "wc-cc44"}
	got := g.formatForest([]string{"wc-aa22"}, "wc")
	want := "aa22 ── bb33, cc44, dd55\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestFormatForestBranchAndLeaf(t *testing.T) {
	g := graphKnown("wc-aa22", "wc-dd55", "wc-bb33", "wc-cc44")
	g.outDep["wc-aa22"] = []string{"wc-dd55", "wc-bb33"}
	g.outDep["wc-bb33"] = []string{"wc-cc44"}
	got := g.formatForest([]string{"wc-aa22"}, "wc")
	want := "aa22 ─┬─ bb33 ── cc44\n      └─ dd55\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestFormatForestTwoRoots(t *testing.T) {
	g := graphKnown("wc-aa22", "wc-bb33", "wc-cc44", "wc-dd55")
	g.outDep["wc-aa22"] = []string{"wc-bb33"}
	g.outDep["wc-cc44"] = []string{"wc-dd55"}
	got := g.formatForest([]string{"wc-aa22", "wc-cc44"}, "wc")
	want := "aa22 ── bb33\ncc44 ── dd55\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestFormatForestCycleMark(t *testing.T) {
	g := graphKnown("wc-aa22", "wc-bb33")
	g.outDep["wc-aa22"] = []string{"wc-bb33"}
	g.outDep["wc-bb33"] = []string{"wc-aa22"}
	got := g.formatForest([]string{"wc-aa22"}, "wc")
	want := "aa22 ─── bb33 ── aa22 (cycle)\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestFormatForestDAGJoinReprintsWithoutExpanding(t *testing.T) {
	g := graphKnown("wc-aa22", "wc-bb33", "wc-cc44", "wc-dd55")
	g.outDep["wc-aa22"] = []string{"wc-cc44"}
	g.outDep["wc-bb33"] = []string{"wc-cc44"}
	g.outDep["wc-cc44"] = []string{"wc-dd55"}
	got := g.formatForest([]string{"wc-aa22", "wc-bb33"}, "wc")
	want := "aa22 ─── cc44 ── dd55\nbb33 ─── cc44\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestFormatForestNestedTee(t *testing.T) {
	g := graphKnown("wc-aa22", "wc-bb33", "wc-ee66", "wc-cc44", "wc-dd55", "wc-ff77")
	g.outDep["wc-aa22"] = []string{"wc-bb33", "wc-ee66"}
	g.outDep["wc-bb33"] = []string{"wc-cc44", "wc-dd55"}
	g.outDep["wc-dd55"] = []string{"wc-ff77"}
	got := g.formatForest([]string{"wc-aa22"}, "wc")
	want := "aa22 ─┬─ bb33 ─┬─ cc44\n               └─ dd55 ── ff77\n      └─ ee66\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestFormatForestThreeWayMid(t *testing.T) {
	g := graphKnown("wc-aa22", "wc-bb33", "wc-cc44", "wc-dd55", "wc-ee66")
	g.outDep["wc-aa22"] = []string{"wc-bb33", "wc-cc44", "wc-ee66"}
	g.outDep["wc-bb33"] = []string{"wc-dd55"}
	got := g.formatForest([]string{"wc-aa22"}, "wc")
	want := "aa22 ─┬─ bb33 ── dd55\n      ├─ cc44\n      └─ ee66\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestFormatForestDiamondJoinReprints(t *testing.T) {
	g := graphKnown("wc-aa22", "wc-bb33", "wc-cc44", "wc-dd55")
	g.outDep["wc-aa22"] = []string{"wc-bb33", "wc-cc44"}
	g.outDep["wc-bb33"] = []string{"wc-dd55"}
	g.outDep["wc-cc44"] = []string{"wc-dd55"}
	got := g.formatForest([]string{"wc-aa22"}, "wc")
	want := "aa22 ─┬─ bb33 ── dd55\n      └─ cc44 ── dd55\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestFormatForestForeignNodeUsesFullID(t *testing.T) {
	g := graphKnown("wc-aa22", "up-bb33")
	g.outDep["wc-aa22"] = []string{"up-bb33"}
	got := g.formatForest([]string{"wc-aa22"}, "wc")
	want := "aa22 ── up-bb33\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestFormatForestUnresolvedLeaf(t *testing.T) {
	g := graphKnown("wc-aa22")
	g.outDep["wc-aa22"] = []string{"wc-zz99"}
	got := g.formatForest([]string{"wc-aa22"}, "wc")
	want := "aa22 ── zz99 (unresolved)\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestFormatForestOtherRootIsJoinNotExpanded(t *testing.T) {
	g := graphKnown("wc-aa22", "wc-bb33", "wc-cc44", "wc-dd55")
	g.outDep["wc-aa22"] = []string{"wc-bb33"}
	g.outDep["wc-bb33"] = []string{"wc-cc44"}
	g.outDep["wc-cc44"] = []string{"wc-dd55"}
	got := g.formatForest([]string{"wc-aa22", "wc-cc44"}, "wc")
	want := "aa22 ─── bb33 ─── cc44\ncc44 ── dd55\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestFormatForestUnresolvedAmongLeaves(t *testing.T) {
	g := graphKnown("wc-aa22", "wc-bb33")
	g.outDep["wc-aa22"] = []string{"wc-zz99", "wc-bb33"}
	got := g.formatForest([]string{"wc-aa22"}, "wc")
	want := "aa22 ── bb33, zz99 (unresolved)\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestForestRootsCyclePicksSmallestFullID(t *testing.T) {
	outDep := map[string][]string{
		"wc-bb33": {"wc-aa22"},
		"wc-aa22": {"wc-bb33"},
	}
	got := forestRoots([]string{"wc-bb33", "wc-aa22", "wc-zz99"}, outDep)
	if len(got) != 1 || got[0] != "wc-aa22" {
		t.Fatalf("roots = %v want [wc-aa22]", got)
	}
}

func TestForestRootsCycleSkipsSmallerSink(t *testing.T) {
	outDep := map[string][]string{
		"wc-bb33": {"wc-aa22", "wc-cc44"},
		"wc-cc44": {"wc-bb33"},
	}
	got := forestRoots([]string{"wc-aa22", "wc-bb33", "wc-cc44"}, outDep)
	if len(got) != 1 || got[0] != "wc-bb33" {
		t.Fatalf("roots = %v want [wc-bb33]", got)
	}
}

func TestForestRootsOmitsIsolated(t *testing.T) {
	outDep := map[string][]string{
		"wc-aa22": {"wc-bb33"},
	}
	got := forestRoots([]string{"wc-aa22", "wc-bb33", "wc-zz99"}, outDep)
	if len(got) != 1 || got[0] != "wc-aa22" {
		t.Fatalf("roots = %v want [wc-aa22]", got)
	}
}

func TestForestRootsTwoDAGEntries(t *testing.T) {
	outDep := map[string][]string{
		"wc-aa22": {"wc-cc44"},
		"wc-bb33": {"wc-cc44"},
	}
	got := forestRoots([]string{"wc-aa22", "wc-bb33", "wc-cc44"}, outDep)
	if len(got) != 2 || got[0] != "wc-aa22" || got[1] != "wc-bb33" {
		t.Fatalf("roots = %v want [wc-aa22 wc-bb33]", got)
	}
}
