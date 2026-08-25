package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDepsDoesNotDumpAllEdgesOrTickets(t *testing.T) {
	body, err := os.ReadFile("deps.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(body)
	for _, name := range []string{"AllTickets", "AllEdges", "EdgesFromPath"} {
		if strings.Contains(src, name) {
			t.Errorf("deps.go must not call %s", name)
		}
	}
}

func TestDepsCrossScopeOutboundAndInbound(t *testing.T) {
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

func TestDepsDanglingTargetIsUnresolved(t *testing.T) {
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
}

func TestDepsRelatedBothDirectionsStayOutOfDepends(t *testing.T) {
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

func TestDepsThreeCycleWarnsOnDefault(t *testing.T) {
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
	if !strings.Contains(out, "\n    wc-bb33\ttodo\tTwo\n      wc-cc44\ttodo\tThree\n        wc-aa22\t(cycle)\n") {
		t.Errorf("3-cycle --tree must print the hop-2 close as (cycle): %q", out)
	}
}

func TestDepsThreeScopeInboundTransitiveAndStaleNeighbour(t *testing.T) {
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

func TestDepsCrossScopeOutboundStaleNeighbour(t *testing.T) {
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
