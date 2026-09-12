package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDependsDoesNotDumpAllEdgesOrTickets(t *testing.T) {
	body, err := os.ReadFile("depends.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(body)
	for _, name := range []string{"AllTickets", "AllEdges", "EdgesFromPath"} {
		if strings.Contains(src, name) {
			t.Errorf("depends.go must not call %s", name)
		}
	}
}

func TestDependsCrossScopeOutboundAndInbound(t *testing.T) {
	app := newApp(t)
	up := initScope(t, app, "up")
	wc := initScope(t, app, "wc")
	addTicket(t, up, "up-aa22", "core", "todo", "a0", "# Core\n", false, "")
	addTicket(t, wc, "wc-bb22", "feat", "todo", "a0", "# Feature\n", false, "depends: [up-aa22]\n")
	indexScopes(t, app, "up", "wc")

	out, _, err := run(t, app, "deps", "wc-bb22")
	if err != nil {
		t.Fatalf("deps outbound: %v", err)
	}
	if !strings.Contains(out, "depends on:\n  up-aa22\ttodo\tCore") {
		t.Errorf("cross-scope outbound missing on depends on: %q", out)
	}

	out, _, err = run(t, app, "deps", "up-aa22")
	if err != nil {
		t.Fatalf("deps inbound: %v", err)
	}
	if !strings.Contains(out, "is depended on by:\n  wc-bb22\ttodo\tFeature") {
		t.Errorf("cross-scope inbound missing on depended on by: %q", out)
	}
}

func TestDependsDanglingTargetIsUnresolved(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "network", "todo", "a0", "# Network\n", false, "depends: [wc-zz99]\n")

	out, _, err := run(t, app, "deps", "wc-ab2c")
	if err != nil {
		t.Fatalf("deps dangling: %v", err)
	}
	if !strings.Contains(out, "depends on:\n  wc-zz99\t(unresolved)") {
		t.Errorf("dangling target should print (unresolved): %q", out)
	}
	if strings.Contains(out, "is depended on by:\n  wc-zz99") {
		t.Errorf("dangling target must not appear as a reverse depender: %q", out)
	}

	out, _, err = run(t, app, "deps", "wc-ab2c", "--tree")
	if err != nil {
		t.Fatalf("deps --tree dangling: %v", err)
	}
	if !strings.Contains(out, "ab2c ── zz99 (unresolved)\n") {
		t.Errorf("subtree --tree should mark dangling (unresolved): %q", out)
	}

	out, _, err = run(t, app, "depends", "--tree", "--scope", "wc")
	if err != nil {
		t.Fatalf("forest dangling: %v", err)
	}
	if out != "ab2c ── zz99 (unresolved)\n" {
		t.Errorf("forest --tree should mark dangling (unresolved): %q", out)
	}
}

func TestDependsRelatedBothDirectionsStayOutOfDepends(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "network", "todo", "a0", "# Network\n", false, "related: [wc-de34]\n")
	addTicket(t, dir, "wc-de34", "auth", "todo", "a1", "# Auth\n", false, "")
	addTicket(t, dir, "wc-mn89", "note", "todo", "a2", "# Note\n", false, "related: [wc-ab2c]\n")
	addTicket(t, dir, "wc-gh56", "gate", "todo", "a3", "# Gate\n", false, "depends: [wc-ab2c]\n")

	out, _, err := run(t, app, "deps", "wc-ab2c")
	if err != nil {
		t.Fatalf("deps related: %v", err)
	}
	if !strings.Contains(out, "depends on:\n  (none)") {
		t.Errorf("related-only must not fill depends on: %q", out)
	}
	if !strings.Contains(out, "is depended on by:\n  wc-gh56\ttodo\tGate") {
		t.Errorf("real depends inbound missing: %q", out)
	}
	if !strings.Contains(out, "related:\n  wc-de34\ttodo\tAuth\n  wc-mn89\ttodo\tNote") {
		t.Errorf("related must be outgoing de34 and inbound-only mn89: %q", out)
	}
	if strings.Count(out, "wc-de34") != 1 || strings.Count(out, "wc-mn89") != 1 {
		t.Errorf("related ids must appear once, only in related: %q", out)
	}
}

func TestDependsThreeCycleWarnsOnDefault(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-aa22", "one", "todo", "a0", "# One\n", false, "depends: [wc-bb33]\n")
	addTicket(t, dir, "wc-bb33", "two", "todo", "a1", "# Two\n", false, "depends: [wc-cc44]\n")
	addTicket(t, dir, "wc-cc44", "three", "todo", "a2", "# Three\n", false, "depends: [wc-aa22]\n")

	out, errOut, err := run(t, app, "deps", "wc-aa22")
	if err != nil {
		t.Fatalf("deps cycle: %v", err)
	}
	if !strings.Contains(errOut, "wc-aa22 is in a depends cycle — run tk doctor for detail") {
		t.Errorf("expected cycle warning on default deps, stderr=%q", errOut)
	}
	if !strings.Contains(out, "depends on:\n  wc-bb33\ttodo\tTwo") {
		t.Errorf("cycle default still prints one-hop outbound: %q", out)
	}
	if !strings.Contains(out, "is depended on by:\n  wc-cc44\ttodo\tThree") {
		t.Errorf("cycle default still prints one-hop inbound: %q", out)
	}

	out, errOut, err = run(t, app, "deps", "wc-aa22", "--tree")
	if err != nil {
		t.Fatalf("deps --tree cycle: %v", err)
	}
	if !strings.Contains(errOut, "wc-aa22 is in a depends cycle — run tk doctor for detail") {
		t.Errorf("expected cycle warning on --tree, stderr=%q", errOut)
	}
	if !strings.Contains(out, "aa22 ─── bb33 ─── cc44 ── aa22 (cycle)\n") {
		t.Errorf("3-cycle --tree must print the hop-2 close as (cycle): %q", out)
	}
	if !strings.Contains(out, "related:\n  (none)\n") {
		t.Errorf("subtree --tree must still print related: %q", out)
	}
}

func TestDependsTreeForestAndSubtree(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-aa22", "a", "todo", "a0", "# A\n", false, "depends: [wc-bb33]\nrelated: [wc-zz99]\n")
	addTicket(t, dir, "wc-bb33", "b", "todo", "a1", "# B\n", false, "depends: [wc-cc44]\n")
	addTicket(t, dir, "wc-cc44", "c", "todo", "a2", "# C\n", false, "")
	addTicket(t, dir, "wc-dd55", "d", "todo", "a3", "# D\n", false, "depends: [wc-ee66, wc-ff77, wc-gg88]\n")
	addTicket(t, dir, "wc-ee66", "e", "todo", "a4", "# E\n", false, "")
	addTicket(t, dir, "wc-ff77", "f", "todo", "a5", "# F\n", false, "")
	addTicket(t, dir, "wc-gg88", "g", "todo", "a6", "# G\n", false, "")
	addTicket(t, dir, "wc-hh99", "h", "todo", "a7", "# Isolated\n", false, "")

	out, _, err := run(t, app, "depends", "--tree", "--scope", "wc")
	if err != nil {
		t.Fatalf("depends --tree forest: %v", err)
	}
	want := "aa22 ─── bb33 ── cc44\ndd55 ── ee66, ff77, gg88\n"
	if out != want {
		t.Errorf("forest stdout = %q want %q", out, want)
	}
	if strings.Contains(out, "related:") || strings.Contains(out, "hh99") || strings.Contains(out, "\t") {
		t.Errorf("forest must omit related, isolated, and TSV tabs: %q", out)
	}

	out, _, err = run(t, app, "depends", "wc-aa22", "--tree", "--scope", "wc")
	if err != nil {
		t.Fatalf("depends id --tree: %v", err)
	}
	if !strings.HasPrefix(out, "aa22 ─── bb33 ── cc44\nrelated:\n  wc-zz99\t(unresolved)\n") {
		t.Errorf("subtree --tree should root at aa22 then related TSV: %q", out)
	}

	out, _, err = run(t, app, "depends", "wc-aa22", "--scope", "wc")
	if err != nil {
		t.Fatalf("depends TSV: %v", err)
	}
	if !strings.Contains(out, "depends on:\n  wc-bb33\ttodo\tB") {
		t.Errorf("default TSV unchanged: %q", out)
	}
}

func TestDependsTreeForestTwoTicketCycle(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-bb33", "b", "todo", "a1", "# B\n", false, "depends: [wc-aa22]\n")
	addTicket(t, dir, "wc-aa22", "a", "todo", "a0", "# A\n", false, "depends: [wc-bb33]\n")

	out, errOut, err := run(t, app, "depends", "--tree", "--scope", "wc")
	if err != nil {
		t.Fatalf("cycle forest: %v", err)
	}
	if out != "aa22 ─── bb33 ── aa22 (cycle)\n" {
		t.Errorf("cycle forest = %q", out)
	}
	if !strings.Contains(errOut, "wc-aa22 is in a depends cycle — run tk doctor for detail") {
		t.Errorf("cycle forest should warn on the start id, stderr=%q", errOut)
	}
	if strings.Contains(out, "related:") {
		t.Errorf("forest must not print related: %q", out)
	}
}

func TestDependsTreeForestWalksArchivedAndForeign(t *testing.T) {
	app := newApp(t)
	wc := initScope(t, app, "wc")
	up := initScope(t, app, "up")
	addTicket(t, wc, "wc-aa22", "a", "todo", "a0", "# A\n", false, "depends: [wc-bb33]\n")
	addTicket(t, wc, "wc-bb33", "b", "done", "a1", "# B\n", true, "depends: [wc-cc44]\n")
	addTicket(t, wc, "wc-cc44", "c", "done", "a2", "# C\n", true, "")
	addTicket(t, wc, "wc-dd55", "d", "todo", "a3", "# D\n", false, "depends: [up-ee66]\n")
	addTicket(t, up, "up-ee66", "e", "todo", "a0", "# E\n", false, "depends: [up-ff77]\n")
	addTicket(t, up, "up-ff77", "f", "todo", "a1", "# F\n", false, "")
	indexScopes(t, app, "wc", "up")

	out, _, err := run(t, app, "depends", "--tree", "--scope", "wc")
	if err != nil {
		t.Fatalf("forest walk: %v", err)
	}
	want := "aa22 ─── bb33 ── cc44\ndd55 ─── up-ee66 ── up-ff77\n"
	if out != want {
		t.Errorf("forest = %q want %q", out, want)
	}
}

func TestDependsTreeForestLiveRootAfterArchivedIsJoin(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-aa22", "a", "todo", "a0", "# A\n", false, "depends: [wc-bb33]\n")
	addTicket(t, dir, "wc-bb33", "b", "done", "a1", "# B\n", true, "depends: [wc-cc44]\n")
	addTicket(t, dir, "wc-cc44", "c", "todo", "a2", "# C\n", false, "depends: [wc-dd55]\n")
	addTicket(t, dir, "wc-dd55", "d", "todo", "a3", "# D\n", false, "")

	out, _, err := run(t, app, "depends", "--tree", "--scope", "wc")
	if err != nil {
		t.Fatalf("archived-middle forest: %v", err)
	}
	want := "aa22 ─── bb33 ─── cc44\ncc44 ── dd55\n"
	if out != want {
		t.Errorf("forest = %q want %q", out, want)
	}
}

func TestDependsTreeForestCycleClusterSkipsSmallerSink(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-aa22", "sink", "todo", "a0", "# Sink\n", false, "")
	addTicket(t, dir, "wc-bb33", "b", "todo", "a1", "# B\n", false, "depends: [wc-aa22, wc-cc44]\n")
	addTicket(t, dir, "wc-cc44", "c", "todo", "a2", "# C\n", false, "depends: [wc-bb33]\n")

	out, _, err := run(t, app, "depends", "--tree", "--scope", "wc")
	if err != nil {
		t.Fatalf("cycle+sink forest: %v", err)
	}
	want := "bb33 ─┬─ aa22\n      └─ cc44 ── bb33 (cycle)\n"
	if out != want {
		t.Errorf("forest = %q want %q", out, want)
	}
}

func TestDependsTreeForestHonoursLens(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-aa22", "a", "todo", "a0", "# A\n", false, "depends: [wc-bb33]\ntags: [frontend]\n")
	addTicket(t, dir, "wc-bb33", "b", "todo", "a1", "# B\n", false, "tags: [frontend]\n")
	addTicket(t, dir, "wc-cc44", "c", "todo", "a2", "# C\n", false, "depends: [wc-dd55]\ntags: [backend]\n")
	addTicket(t, dir, "wc-dd55", "d", "todo", "a3", "# D\n", false, "tags: [backend]\n")
	if _, _, err := run(t, app, "lens", "frontend", "--scope", "wc"); err != nil {
		t.Fatalf("lens: %v", err)
	}

	out, errOut, err := run(t, app, "depends", "--tree", "--scope", "wc")
	if err != nil {
		t.Fatalf("lensed forest: %v", err)
	}
	if out != "aa22 ── bb33\n" {
		t.Errorf("lens forest = %q want aa22 chain only", out)
	}
	if !strings.Contains(errOut, "lens:") {
		t.Errorf("lensed forest should echo lens on stderr, got %q", errOut)
	}

	out, errOut, err = run(t, app, "depends", "--tree", "--no-lens", "--scope", "wc")
	if err != nil {
		t.Fatalf("no-lens forest: %v", err)
	}
	if out != "aa22 ── bb33\ncc44 ── dd55\n" {
		t.Errorf("no-lens forest = %q", out)
	}
	if strings.Contains(errOut, "lens:") {
		t.Errorf("--no-lens forest must not echo lens, stderr %q", errOut)
	}

	out, errOut, err = run(t, app, "depends", "wc-aa22", "--tree", "--scope", "wc")
	if err != nil {
		t.Fatalf("subtree under lens: %v", err)
	}
	if !strings.HasPrefix(out, "aa22 ── bb33\n") {
		t.Errorf("subtree --tree stdout = %q", out)
	}
	if strings.Contains(errOut, "lens:") {
		t.Errorf("depends <id> --tree must not echo lens, stderr %q", errOut)
	}
}

func TestDependsWithoutIDRequiresTree(t *testing.T) {
	app := newApp(t)
	_ = initScope(t, app, "wc")
	_, _, err := run(t, app, "depends", "--scope", "wc")
	if got := ExitCodeFromError(err); got != exitUsage {
		t.Fatalf("depends without id or --tree exit = %d want %d (err=%v)", got, exitUsage, err)
	}
	if err == nil || !strings.Contains(err.Error(), "missing <id>") {
		t.Errorf("want missing <id>, got %v", err)
	}
}

func TestDependsThreeScopeInboundTransitiveAndStaleNeighbour(t *testing.T) {
	app := newApp(t)
	aa := initScope(t, app, "aa")
	bb := initScope(t, app, "bb")
	cc := initScope(t, app, "cc")
	addTicket(t, aa, "aa-aa22", "root", "todo", "a0", "# Root\n", false, "")
	addTicket(t, bb, "bb-bb33", "mid", "todo", "a0", "# Mid\n", false, "depends: [aa-aa22]\n")
	addTicket(t, cc, "cc-cc44", "leaf", "todo", "a0", "# Leaf\n", false, "depends: [bb-bb33]\n")
	indexScopes(t, app, "aa", "bb", "cc")

	out, _, err := run(t, app, "deps", "aa-aa22", "--transitive")
	if err != nil {
		t.Fatalf("deps --transitive inbound: %v", err)
	}
	if !strings.Contains(out, "depends on (transitive):\n  (none)") {
		t.Errorf("inbound-only chain must not fill depends on (transitive): %q", out)
	}
	if !strings.Contains(out, "is depended on by (transitive):\n  bb-bb33\ttodo\tMid\n  cc-cc44\ttodo\tLeaf") {
		t.Errorf("3-scope inbound transitive should list B then C under depended on by: %q", out)
	}

	rewriteTicket(t, cc, "cc-cc44-leaf.md", "cc-cc44", "done", "a0", "# Leaf renamed\n", "depends: [bb-bb33]\n")
	out, _, err = run(t, app, "deps", "aa-aa22", "--transitive")
	if err != nil {
		t.Fatalf("deps after cc edit: %v", err)
	}
	if !strings.Contains(out, "cc-cc44\ttodo\tLeaf") {
		t.Errorf("neighbour status must stay the indexed row, got %q", out)
	}
	if strings.Contains(out, "cc-cc44\tdone") || strings.Contains(out, "Leaf renamed") {
		t.Errorf("must not reconcile cc on deps of aa, got %q", out)
	}
}

func TestDependsCrossScopeOutboundStaleNeighbour(t *testing.T) {
	app := newApp(t)
	aa := initScope(t, app, "aa")
	bb := initScope(t, app, "bb")
	addTicket(t, aa, "aa-aa22", "root", "todo", "a0", "# Root\n", false, "depends: [bb-bb33]\n")
	addTicket(t, bb, "bb-bb33", "mid", "todo", "a0", "# Mid\n", false, "")
	indexScopes(t, app, "aa", "bb")

	out, _, err := run(t, app, "deps", "aa-aa22")
	if err != nil {
		t.Fatalf("deps outbound: %v", err)
	}
	if !strings.Contains(out, "depends on:\n  bb-bb33\ttodo\tMid") {
		t.Errorf("cross-scope outbound missing: %q", out)
	}

	rewriteTicket(t, bb, "bb-bb33-mid.md", "bb-bb33", "done", "a0", "# Mid shipped\n", "")
	out, _, err = run(t, app, "deps", "aa-aa22")
	if err != nil {
		t.Fatalf("deps after bb edit: %v", err)
	}
	if !strings.Contains(out, "bb-bb33\ttodo\tMid") {
		t.Errorf("outbound neighbour status must stay the indexed row, got %q", out)
	}
	if strings.Contains(out, "bb-bb33\tdone") || strings.Contains(out, "Mid shipped") {
		t.Errorf("must not reconcile bb on deps of aa, got %q", out)
	}
}

func indexScopes(t *testing.T, app *App, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, _, err := run(t, app, "list", "--scope", name); err != nil {
			t.Fatalf("list --scope %s: %v", name, err)
		}
	}
}

func rewriteTicket(t *testing.T, dir, name, id, status, order, body, extraFM string) {
	t.Helper()
	fm := "---\nid: " + id + "\nstatus: " + status + "\norder: \"" + order + "\"\ncreated: 2026-01-01T00:00:00Z\n" + extraFM + "---\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(fm+body), 0o644); err != nil {
		t.Fatal(err)
	}
}
