package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func assertNoDoctorCatalogue(t *testing.T, out, errOut string) {
	t.Helper()
	combined := out + errOut
	if strings.Contains(combined, "tk doctor:") {
		t.Errorf("repair must not print doctor status, got %q", combined)
	}
	if strings.Contains(combined, "no integrity issues found") {
		t.Errorf("repair must not print the doctor empty-catalogue line, got %q", combined)
	}
	if strings.Contains(combined, " — run tk repair") {
		t.Errorf("repair must not print diagnose tails, got %q", combined)
	}
}

func TestRepairDuplicateID(t *testing.T) {
	app := newApp(t)
	t.Setenv("TK_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "alpha", "todo", "a0", "# Alpha\n", false, "")
	addTicket(t, dir, "wc-ab2c", "beta", "todo", "a1", "# Beta\n", false, "")
	addTicket(t, dir, "wc-de34", "ref", "todo", "a2", "# Ref\n", false, "depends: [wc-ab2c]\n")

	out, errOut, err := run(t, app, "repair")
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	assertNoDoctorCatalogue(t, out, errOut)
	if !fileExists(dir, "wc-ab2c-alpha.md") {
		t.Errorf("kept side must retain its id/filename")
	}
	if fileExists(dir, "wc-ab2c-beta.md") {
		t.Errorf("loser file must be renamed away")
	}
	if !fileExists(dir, "wc-ab2ca-beta.md") {
		t.Errorf("loser must take the deterministic extension ab2ca, files=%v", ticketFiles(t, dir))
	}
	if !strings.Contains(out, "repaired duplicate id: wc-ab2c -> wc-ab2ca") {
		t.Errorf("repair should report the rename, got %q", out)
	}
	if !strings.Contains(out, "edge_verify:") || !strings.Contains(out, "wc-de34") {
		t.Errorf("repair should emit edge_verify for the referrer, got %q", out)
	}
	ref, _ := os.ReadFile(filepath.Join(dir, "wc-de34-ref.md"))
	if !strings.Contains(string(ref), "wc-ab2c") {
		t.Errorf("depends edge must be left untouched, got %q", ref)
	}
}

func TestRepairEdgeVerifyNamesPostRepairReferrers(t *testing.T) {
	app := newApp(t)
	t.Setenv("TK_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "alpha", "todo", "a0", "# A\n", false, "")
	addTicket(t, dir, "wc-ab2c", "beta", "todo", "a1", "# B\n", false, "")
	addTicket(t, dir, "wc-de34", "gamma", "todo", "a2", "# G\n", false, "depends: [wc-ab2c]\n")
	addTicket(t, dir, "wc-de34", "delta", "todo", "a3", "# D\n", false, "depends: [wc-ab2c]\n")

	out, _, err := run(t, app, "repair")
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	for _, want := range []string{
		"edge_verify: wc-de34 depends wc-ab2c",
		"edge_verify: wc-de34a depends wc-ab2c",
	} {
		if strings.Count(out, want+" ") != 1 {
			t.Errorf("want exactly one %q, got %q", want, out)
		}
	}
	if strings.Count(out, "edge_verify:") != 2 {
		t.Errorf("expected exactly two edge_verify lines, got %q", out)
	}
	for _, id := range []string{"wc-de34", "wc-de34a"} {
		if _, _, err := run(t, app, "get", id); err != nil {
			t.Errorf("edge_verify named %s, which does not resolve: %v", id, err)
		}
	}
}

func TestRepairSeesUnindexedCollision(t *testing.T) {
	app := newApp(t)
	t.Setenv("TK_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "alpha", "todo", "a0", "# Alpha\n", false, "")
	addTicket(t, dir, "wc-ab2c", "beta", "todo", "a1", "# Beta\n", false, "")

	out, _, err := run(t, app, "repair")
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if !strings.Contains(out, "repaired duplicate id: wc-ab2c -> wc-ab2ca") {
		t.Fatalf("an on-disk collision absent from the index must still be repaired, got %q", out)
	}
	if !fileExists(dir, "wc-ab2ca-beta.md") {
		t.Errorf("the loser must be renamed on disk, files=%v", ticketFiles(t, dir))
	}
}

func TestRepairEqualOrder(t *testing.T) {
	app := newApp(t)
	t.Setenv("TK_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-aaaa", "a", "todo", "a0", "# A\n", false, "")
	addTicket(t, dir, "wc-bbbb", "b", "todo", "a1", "# B\n", false, "")
	addTicket(t, dir, "wc-cccc", "c", "todo", "a1", "# C\n", false, "")
	addTicket(t, dir, "wc-dddd", "d", "todo", "a2", "# D\n", false, "")

	if _, _, err := run(t, app, "repair"); err != nil {
		t.Fatalf("repair: %v", err)
	}
	ka := fmValue(t, filepath.Join(dir, "wc-aaaa-a.md"), "order")
	kb := fmValue(t, filepath.Join(dir, "wc-bbbb-b.md"), "order")
	kc := fmValue(t, filepath.Join(dir, "wc-cccc-c.md"), "order")
	kd := fmValue(t, filepath.Join(dir, "wc-dddd-d.md"), "order")
	if ka != "a0" || kd != "a2" {
		t.Errorf("untied anchors must not move: a=%q d=%q", ka, kd)
	}
	if ka >= kb || kb >= kc || kc >= kd || kb == kc {
		t.Errorf("tied keys must become distinct and ordered: %q %q %q %q", ka, kb, kc, kd)
	}
}

func TestRepairArchiveLayoutBothWays(t *testing.T) {
	app := newApp(t)
	t.Setenv("TK_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-aaaa", "done1", "done", "a0", "# Done\n", false, "")
	addTicket(t, dir, "wc-bbbb", "todo1", "todo", "a1", "# Todo\n", true, "")

	if _, _, err := run(t, app, "repair"); err != nil {
		t.Fatalf("repair: %v", err)
	}
	if !fileExists(dir, filepath.Join("archive", "wc-aaaa-done1.md")) {
		t.Errorf("terminal ticket must move under archive/, files=%v", ticketFiles(t, dir))
	}
	if !fileExists(dir, "wc-bbbb-todo1.md") {
		t.Errorf("non-terminal ticket must move to dir root, files=%v", ticketFiles(t, dir))
	}
}

func TestRepairCollisionAcrossArchiveBoundaryKeepsBothTickets(t *testing.T) {
	app := newApp(t)
	t.Setenv("TK_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "alpha", "done", "a0", "# Root copy\n", false, "")
	addTicket(t, dir, "wc-ab2c", "alpha", "todo", "a1", "# Archive copy\n", true, "")

	if _, _, err := run(t, app, "repair"); err != nil {
		t.Fatalf("repair: %v", err)
	}
	bodies := map[string]bool{}
	for _, root := range []string{dir, filepath.Join(dir, "archive")} {
		for _, base := range ticketFilesIn(t, root) {
			data, err := os.ReadFile(filepath.Join(root, base))
			if err != nil {
				t.Fatal(err)
			}
			bodies[strings.TrimSpace(string(data[strings.LastIndex(string(data), "---\n")+4:]))] = true
		}
	}
	if !bodies["# Root copy"] || !bodies["# Archive copy"] {
		t.Fatalf("repair must keep both tickets, found bodies %v (files %v)", bodies, ticketFiles(t, dir))
	}
	if !fileExists(dir, filepath.Join("archive", "wc-ab2c-alpha.md")) {
		t.Errorf("the done ticket must end under archive/, files=%v", ticketFiles(t, dir))
	}
	if !fileExists(dir, "wc-ab2ca-alpha.md") {
		t.Errorf("the todo loser must be renamed and left at dir root, files=%v", ticketFiles(t, dir))
	}
}

func TestRepairReSpaceOrder(t *testing.T) {
	app := newApp(t)
	t.Setenv("TK_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	longKey := "a1" + strings.Repeat("V", 80)
	addTicket(t, dir, "wc-aaaa", "a", "todo", "a0", "# A\n", false, "")
	addTicket(t, dir, "wc-bbbb", "b", "todo", longKey, "# B\n", false, "")
	addTicket(t, dir, "wc-cccc", "c", "todo", "a2", "# C\n", false, "")

	if _, _, err := run(t, app, "repair"); err != nil {
		t.Fatalf("repair: %v", err)
	}
	if fmValue(t, filepath.Join(dir, "wc-bbbb-b.md"), "order") != longKey {
		t.Errorf("bare repair must not re-space an over-long key")
	}
	if _, _, err := run(t, app, "repair", "--re-space-order"); err != nil {
		t.Fatalf("repair --re-space-order: %v", err)
	}
	got := fmValue(t, filepath.Join(dir, "wc-bbbb-b.md"), "order")
	if len(got) > 64 {
		t.Errorf("--re-space-order must shorten the key, got %d chars", len(got))
	}
	if got <= "a0" || got >= "a2" {
		t.Errorf("re-space must preserve order, got %q", got)
	}
}

func TestRepairReSpaceOrderIsAdditive(t *testing.T) {
	app := newApp(t)
	t.Setenv("TK_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	longKey := "a1" + strings.Repeat("V", 80)
	addTicket(t, dir, "wc-ab2c", "alpha", "todo", "a0", "# Alpha\n", false, "")
	addTicket(t, dir, "wc-ab2c", "beta", "todo", longKey, "# Beta\n", false, "")

	out, errOut, err := run(t, app, "repair", "--re-space-order")
	if err != nil {
		t.Fatalf("repair --re-space-order: %v", err)
	}
	assertNoDoctorCatalogue(t, out, errOut)
	if !fileExists(dir, "wc-ab2ca-beta.md") {
		t.Errorf("default classes must still run under --re-space-order, files=%v", ticketFiles(t, dir))
	}
	got := fmValue(t, filepath.Join(dir, "wc-ab2ca-beta.md"), "order")
	if len(got) > 64 {
		t.Errorf("--re-space-order must shorten the loser's key, got %d chars", len(got))
	}
}

func TestRepairScopeSelection(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "alpha", "todo", "a0", "# A\n", false, "")
	addTicket(t, dir, "wc-ab2c", "beta", "todo", "a1", "# B\n", false, "")

	_ = os.Unsetenv("TK_SCOPE")
	_, errOut, err := run(t, app, "repair")
	if ExitCodeFromError(err) != exitUsage {
		t.Errorf("repair with no scope should exit 2, got %v", err)
	}
	if !strings.Contains(err.Error()+errOut, "tk repair") {
		t.Errorf("missing-scope error must name tk repair, got %v / %q", err, errOut)
	}
	if strings.Contains(err.Error()+errOut, "tk doctor --repair") {
		t.Errorf("missing-scope error must not name tk doctor --repair, got %v / %q", err, errOut)
	}
	if _, _, err := run(t, app, "repair", "--all"); err != nil {
		t.Errorf("repair --all should run, got %v", err)
	}
	if !fileExists(dir, "wc-ab2ca-beta.md") {
		t.Errorf("--all should have repaired the collision, files=%v", ticketFiles(t, dir))
	}
}

func TestRepairResumesInterruptedArchiveMove(t *testing.T) {
	app := newApp(t)
	t.Setenv("TK_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "ship", "done", "a0", "# Ship\n", false, "")
	addTicket(t, dir, "wc-ab2c", "ship", "done", "a0", "# Ship\n", true, "")

	out, _, err := run(t, app, "repair")
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if strings.Contains(out, "repaired duplicate id:") {
		t.Fatalf("interrupted move must not be repaired as a collision, got %q", out)
	}
	files := ticketFiles(t, dir)
	if len(files) != 1 {
		t.Fatalf("interrupted move must resolve to a single file, got %v", files)
	}
	if !fileExists(dir, filepath.Join("archive", "wc-ab2c-ship.md")) {
		t.Errorf("terminal ticket must end under archive/ with its id intact, got %v", files)
	}
	if _, _, err := run(t, app, "repair"); err != nil {
		t.Fatalf("second repair: %v", err)
	}
	if got := ticketFiles(t, dir); len(got) != 1 {
		t.Errorf("re-run must stay idempotent, got %v", got)
	}
}

func TestRepairResumesInterruptedExtension(t *testing.T) {
	app := newApp(t)
	t.Setenv("TK_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "alpha", "todo", "a0", "# Alpha\n", false, "")
	addTicket(t, dir, "wc-ab2c", "beta", "todo", "a1", "# Beta\n", false, "")

	stale, err := os.ReadFile(filepath.Join(dir, "wc-ab2c-beta.md"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, app, "repair"); err != nil {
		t.Fatalf("repair: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wc-ab2c-beta.md"), stale, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := run(t, app, "repair"); err != nil {
		t.Fatalf("re-entry repair: %v", err)
	}
	files := ticketFiles(t, dir)
	if len(files) != 2 {
		t.Fatalf("re-entry must leave two files, got %v", files)
	}
	if fileExists(dir, "wc-ab2c-beta.md") {
		t.Errorf("stale old-id file must be removed, got %v", files)
	}
	if !fileExists(dir, "wc-ab2ca-beta.md") {
		t.Errorf("loser must stay under its first extension, got %v", files)
	}
	if fileExists(dir, "wc-ab2cb-beta.md") {
		t.Errorf("re-entry must not mint a second extension, got %v", files)
	}
}

func TestRepairAllSkipsUnreachableScope(t *testing.T) {
	app := newApp(t)
	gone := initScope(t, app, "gone")
	live := initScope(t, app, "wc")
	addTicket(t, live, "wc-ab2c", "alpha", "todo", "a0", "# A\n", false, "")
	addTicket(t, live, "wc-ab2c", "beta", "todo", "a1", "# B\n", false, "")
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}

	_ = os.Unsetenv("TK_SCOPE")
	_, errOut, err := run(t, app, "repair", "--all")
	if err != nil {
		t.Fatalf("--all must survive an unreachable scope, got %v", err)
	}
	if !strings.Contains(errOut, "skipping gone: dir unreachable") {
		t.Errorf("the unreachable scope should be reported as skipped, got %q", errOut)
	}
	if !fileExists(live, "wc-ab2ca-beta.md") {
		t.Errorf("the reachable scope must still be repaired, files=%v", ticketFiles(t, live))
	}
}

func TestRepairDoesNotPrintDoctorCatalogue(t *testing.T) {
	app := newApp(t)
	t.Setenv("TK_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	longKey := "a1" + strings.Repeat("V", 80)
	addTicket(t, dir, "wc-ab2c", "alpha", "todo", "a0", "# Alpha\n", false, "")
	addTicket(t, dir, "wc-ab2c", "beta", "todo", longKey, "# Beta\n", false, "")

	out, errOut, err := run(t, app, "repair")
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	assertNoDoctorCatalogue(t, out, errOut)
	if strings.Contains(out+errOut, "order_long:") {
		t.Errorf("repair must not print remaining diagnose classes, got %q", out+errOut)
	}
	if !fileExists(dir, "wc-ab2ca-beta.md") {
		t.Errorf("default repair must still fix the collision, files=%v", ticketFiles(t, dir))
	}
	if fmValue(t, filepath.Join(dir, "wc-ab2ca-beta.md"), "order") != longKey {
		t.Errorf("bare repair must leave the over-long key for doctor")
	}
}
