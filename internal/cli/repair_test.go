package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/token"
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

func TestRepairAdoptsForeignIDPrefix(t *testing.T) {
	app := newApp(t)
	t.Setenv("TK_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	api := initScope(t, app, "api")
	addTicket(t, dir, "at-a575", "shape", "todo", "a0", "# Shape\n", false, "depends: [at-b999]\n")
	addTicket(t, dir, "at-b999", "old", "done", "a1", "# Old\n", true, "")
	addTicket(t, api, "api-mm22", "ref", "todo", "a0", "# Ref\n", false, "depends: [at-a575]\n")
	if err := os.MkdirAll(filepath.Join(dir, "design"), 0o755); err != nil {
		t.Fatal(err)
	}
	designBody := "---\nid: at-qrst\nstatus: draft\ncreated: 2026-01-01T00:00:00Z\nproduces: [at-a575]\n---\n# Shape\n"
	if err := os.WriteFile(filepath.Join(dir, "design", "at-qrst-shape.md"), []byte(designBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(api, "design"), 0o755); err != nil {
		t.Fatal(err)
	}
	apiDesign := "---\nid: api-xy99\nstatus: draft\ncreated: 2026-01-01T00:00:00Z\nproduces: [at-a575]\n---\n# Note\n"
	if err := os.WriteFile(filepath.Join(api, "design", "api-xy99-note.md"), []byte(apiDesign), 0o644); err != nil {
		t.Fatal(err)
	}

	pulse, _, err := run(t, app, "pulse", "--scope", "wc")
	if err != nil {
		t.Fatal(err)
	}
	if parsePulse(pulse)["total"] != "0" || parsePulse(pulse)["integrity"] != "issues" {
		t.Fatalf("foreign-prefix tickets must be invisible and not integrity-ok: %s", pulse)
	}
	_, _, err = run(t, app, "get", "at-a575", "--scope", "wc")
	if err == nil || !strings.Contains(err.Error(), `scope "at" is not registered`) {
		t.Fatalf("get = %v", err)
	}

	doc, _, err := run(t, app, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"at-a575", "at-b999", "at-qrst"} {
		if !strings.Contains(doc, "id_prefix: "+id+" is in scope wc") || !strings.Contains(doc, "— run tk repair") {
			t.Errorf("doctor missing %s: %s", id, doc)
		}
	}
	if strings.Contains(doc, "non_allowlist:") {
		t.Errorf("a ticket-shaped foreign prefix is not residue: %s", doc)
	}
	if !fileExists(dir, "at-a575-shape.md") {
		t.Fatal("doctor must not rewrite")
	}

	out, errOut, err := run(t, app, "repair")
	if err != nil {
		t.Fatalf("repair: %v\n%s", err, errOut)
	}
	assertNoDoctorCatalogue(t, out, errOut)
	for _, line := range []string{
		"repaired id prefix: at-a575 -> wc-a575",
		"repaired id prefix: at-b999 -> wc-b999",
		"repaired id prefix: at-qrst -> wc-qrst",
		"edge_verify: api-mm22 depends at-a575 — id prefix repaired to wc-a575, verify this reference",
		"edge_verify: api-xy99 produces at-a575 — id prefix repaired to wc-a575, verify this reference",
	} {
		if !strings.Contains(out, line) {
			t.Errorf("missing %q in %s", line, out)
		}
	}
	if strings.Count(out, "edge_verify:") != 2 {
		t.Errorf("edge_verify lines = %s", out)
	}
	if fileExists(dir, "at-a575-shape.md") || !fileExists(dir, "wc-a575-shape.md") {
		t.Fatalf("root files = %v", ticketFiles(t, dir))
	}
	if !fileExists(dir, filepath.Join("archive", "wc-b999-old.md")) {
		t.Fatalf("done ticket must stay archived, files = %v", ticketFiles(t, dir))
	}
	if !fileExists(dir, filepath.Join("design", "wc-qrst-shape.md")) {
		t.Fatal("design file was not renamed")
	}
	shape, err := os.ReadFile(filepath.Join(dir, "wc-a575-shape.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(shape), "id: wc-a575") || !strings.Contains(string(shape), "wc-b999") || strings.Contains(string(shape), "at-") {
		t.Fatalf("adopted ticket = %s", shape)
	}
	if fmValue(t, filepath.Join(dir, "wc-a575-shape.md"), "order") != "a0" {
		t.Fatal("order must be kept")
	}
	old, err := os.ReadFile(filepath.Join(dir, "archive", "wc-b999-old.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(old), "id: wc-b999") || strings.Contains(string(old), "at-") {
		t.Fatalf("archived ticket = %s", old)
	}
	gotDesign, err := os.ReadFile(filepath.Join(dir, "design", "wc-qrst-shape.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(gotDesign), "at-a575") || !strings.Contains(string(gotDesign), "wc-a575") || !strings.Contains(string(gotDesign), "id: wc-qrst") {
		t.Fatalf("design = %s", gotDesign)
	}
	apiBody, err := os.ReadFile(filepath.Join(api, "api-mm22-ref.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(apiBody), "at-a575") {
		t.Fatalf("other scope must not be rewritten: %s", apiBody)
	}

	got, _, err := run(t, app, "get", "a575")
	if err != nil || !strings.Contains(got, "wc-a575-shape.md") {
		t.Fatalf("get = %q %v", got, err)
	}
	listed, _, err := run(t, app, "design", "list")
	if err != nil || !strings.Contains(listed, "wc-qrst") {
		t.Fatalf("design list = %q %v", listed, err)
	}
	board, _, err := run(t, app, "list", "--all")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(board, "at-") || strings.Contains(board, "wc-qrst") || !strings.Contains(board, "wc-a575") || !strings.Contains(board, "wc-b999") {
		t.Fatalf("board = %s", board)
	}
	pulse, _, err = run(t, app, "pulse")
	if err != nil {
		t.Fatal(err)
	}
	if parsePulse(pulse)["total"] != "2" || parsePulse(pulse)["done"] != "1" || parsePulse(pulse)["integrity"] != "ok" {
		t.Fatalf("pulse after repair = %s", pulse)
	}
	doc, docErr, err := run(t, app, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(doc, "id_prefix:") || !strings.Contains(docErr, "no integrity issues found") {
		t.Fatalf("doctor after repair = %s / %s", doc, docErr)
	}
	again, _, err := run(t, app, "repair")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(again, "repaired id prefix:") {
		t.Fatalf("second repair must be a no-op, got %s", again)
	}
}

func TestRepairLeavesLiveForeignID(t *testing.T) {
	app := newApp(t)
	t.Setenv("TK_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	api := initScope(t, app, "api")
	addTicket(t, api, "api-mm22", "real", "todo", "a0", "# Real\n", false, "")
	if err := os.MkdirAll(filepath.Join(api, "design"), 0o755); err != nil {
		t.Fatal(err)
	}
	designBody := "---\nid: api-xy99\nstatus: draft\ncreated: 2026-01-01T00:00:00Z\n---\n# Note\n"
	if err := os.WriteFile(filepath.Join(api, "design", "api-xy99-note.md"), []byte(designBody), 0o644); err != nil {
		t.Fatal(err)
	}
	strayTicket := "---\nid: api-mm22\nstatus: todo\norder: \"a1\"\ncreated: 2026-01-01T00:00:00Z\n---\n# Stray ticket\n"
	strayDesign := "---\nid: api-xy99\nstatus: todo\norder: \"a2\"\ncreated: 2026-01-01T00:00:00Z\n---\n# Stray design\n"
	if err := os.WriteFile(filepath.Join(dir, "at-a575-slug.md"), []byte(strayTicket), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "at-b999-slug.md"), []byte(strayDesign), 0o644); err != nil {
		t.Fatal(err)
	}
	addTicket(t, dir, "wc-cccc", "ref", "todo", "a0", "# Ref\n", false, "depends: [at-a575, api-mm22]\nrelated: [at-b999, api-xy99]\n")

	out, errOut, err := run(t, app, "repair")
	if err != nil {
		t.Fatalf("repair: %v\n%s", err, errOut)
	}
	assertNoDoctorCatalogue(t, out, errOut)
	if strings.Contains(out, "edge_verify:") {
		t.Fatalf("live ids must not be reported as moved: %s", out)
	}
	ref, err := os.ReadFile(filepath.Join(dir, "wc-cccc-ref.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"depends: [wc-mm22, api-mm22]", "related: [wc-xy99, api-xy99]"} {
		if !strings.Contains(string(ref), want) {
			t.Fatalf("ref = %s", ref)
		}
	}
	if strings.Contains(string(ref), "at-") {
		t.Fatalf("filename ids must follow the stray files: %s", ref)
	}
	real, err := os.ReadFile(filepath.Join(api, "api-mm22-real.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(real), "id: api-mm22") {
		t.Fatalf("live ticket = %s", real)
	}
	note, err := os.ReadFile(filepath.Join(api, "design", "api-xy99-note.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(note), "id: api-xy99") {
		t.Fatalf("live design = %s", note)
	}
	adopted, err := os.ReadFile(filepath.Join(dir, "wc-mm22-slug.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(adopted), "id: wc-mm22") || strings.Contains(string(adopted), "api-mm22") {
		t.Fatalf("stray ticket = %s", adopted)
	}
}

func TestRepairStopsWhenOtherDesignUnreadable(t *testing.T) {
	app := newApp(t)
	t.Setenv("TK_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	api := initScope(t, app, "api")
	addTicket(t, dir, "at-a575", "shape", "todo", "a0", "# Shape\n", false, "")
	if err := os.MkdirAll(filepath.Join(api, "design"), 0o755); err != nil {
		t.Fatal(err)
	}
	note := filepath.Join(api, "design", "api-xy99-note.md")
	body := "---\nid: api-xy99\nstatus: draft\ncreated: 2026-01-01T00:00:00Z\nproduces: [at-a575]\n---\n# Note\n"
	if err := os.WriteFile(note, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(note, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(note, 0o644) })

	_, errOut, err := run(t, app, "repair")
	if err == nil {
		t.Fatal("unreadable design must stop repair")
	}
	if !strings.Contains(err.Error(), "permission denied") && !strings.Contains(errOut, "permission denied") {
		t.Fatalf("err = %v %s", err, errOut)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "at-a575-shape.md")); statErr != nil {
		t.Fatal("repair must not write")
	}
}

func TestRepairAdoptsPrefixWhenDesignIsFile(t *testing.T) {
	app := newApp(t)
	t.Setenv("TK_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	if err := os.WriteFile(filepath.Join(dir, "design"), []byte("not a directory\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	addTicket(t, dir, "at-a575", "shape", "todo", "a0", "# Shape\n", false, "")

	doc, docErr, err := run(t, app, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v %s", err, docErr)
	}
	if !strings.Contains(doc, "id_prefix: at-a575 is in scope wc") || !strings.Contains(doc, token.NonAllowlist) {
		t.Fatalf("doctor = %q", doc)
	}
	out, errOut, err := run(t, app, "repair")
	if err != nil {
		t.Fatalf("repair: %v %s", err, errOut)
	}
	if !strings.Contains(out, "repaired id prefix: at-a575 -> wc-a575") {
		t.Fatalf("out = %s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "wc-a575-shape.md")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "design"))
	if err != nil || info.IsDir() {
		t.Fatalf("parked design file = %v %v", info, err)
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
