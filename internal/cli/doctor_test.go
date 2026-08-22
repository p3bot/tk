package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/token"
)

func ticketFiles(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	for _, root := range []string{dir, filepath.Join(dir, "archive")} {
		out = append(out, ticketFilesIn(t, root)...)
	}
	return out
}

func ticketFilesIn(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") {
			out = append(out, e.Name())
		}
	}
	return out
}

func fileExists(dir, base string) bool {
	_, err := os.Stat(filepath.Join(dir, base))
	return err == nil
}

func TestDoctorBareReportsAndMutatesNothing(t *testing.T) {
	app := newApp(t)
	t.Setenv("TK_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "alpha", "todo", "a0", "# Alpha\n", false, "")
	addTicket(t, dir, "wc-ab2c", "beta", "todo", "a1", "# Beta\n", false, "")

	before := ticketFiles(t, dir)
	out, _, err := run(t, app, "doctor")
	if err != nil {
		t.Fatalf("bare doctor: %v", err)
	}
	if !strings.Contains(out, "duplicate_id:") {
		t.Errorf("bare doctor should report duplicate_id, got %q", out)
	}
	if strings.Contains(out, "\x1b") {
		t.Errorf("token report must never carry ANSI: %q", out)
	}
	after := ticketFiles(t, dir)
	if len(before) != len(after) || !fileExists(dir, "wc-ab2c-beta.md") {
		t.Errorf("bare doctor must mutate nothing: before=%v after=%v", before, after)
	}
}

func TestDoctorRepairDuplicateID(t *testing.T) {
	app := newApp(t)
	t.Setenv("TK_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "alpha", "todo", "a0", "# Alpha\n", false, "")
	addTicket(t, dir, "wc-ab2c", "beta", "todo", "a1", "# Beta\n", false, "")
	addTicket(t, dir, "wc-de34", "ref", "todo", "a2", "# Ref\n", false, "depends: [wc-ab2c]\n")

	out, _, err := run(t, app, "doctor", "--repair")
	if err != nil {
		t.Fatalf("doctor --repair: %v", err)
	}
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
	// The referrer's depends entry is never rewritten.
	ref, _ := os.ReadFile(filepath.Join(dir, "wc-de34-ref.md"))
	if !strings.Contains(string(ref), "wc-ab2c") {
		t.Errorf("depends edge must be left untouched, got %q", ref)
	}
}

func TestDoctorFlagsMalformedSlugTail(t *testing.T) {
	app := newApp(t)
	t.Setenv("TK_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "good", "todo", "a0", "# Good\n", false, "")
	bad := "---\nid: wc-de34\nstatus: todo\norder: \"a1\"\ncreated: 2026-01-01T00:00:00Z\n---\n# Bad\n"
	if err := os.WriteFile(filepath.Join(dir, "wc-de34-Bad__Slug!.md"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}

	out, _, err := run(t, app, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if !strings.Contains(out, "filename/id mismatch: wc-de34-Bad__Slug!.md is not a ticket file shape") {
		t.Errorf("malformed slug tail must ride the structural check, got %q", out)
	}
	if strings.Contains(out, "wc-ab2c-good.md") {
		t.Errorf("a valid ticket filename must not be flagged, got %q", out)
	}
}

func TestDoctorFlagsDuplicateInCustomStringsField(t *testing.T) {
	app := newApp(t)
	t.Setenv("TK_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	cfg := "name: \"wc\"\nautoCommit: false\nfields: {areas: {type: \"strings\"}}\n"
	if err := os.WriteFile(filepath.Join(dir, "tk.cue"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	addTicket(t, dir, "wc-ab2c", "x", "todo", "a0", "# X\n", false, "areas: [api, api]\n")
	addTicket(t, dir, "wc-de34", "y", "todo", "a1", "# Y\n", false, "areas: [api, ui]\n")

	out, _, err := run(t, app, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if !strings.Contains(out, `schema_warn: wc-ab2c has a duplicate areas entry "api"`) {
		t.Errorf("duplicate in a custom strings field must ride schema_warn, got %q", out)
	}
	if strings.Contains(out, "wc-de34 has a duplicate") {
		t.Errorf("a distinct-valued strings field must not be flagged, got %q", out)
	}
}

func TestDoctorFlagsNonStringInStringsField(t *testing.T) {
	app := newApp(t)
	t.Setenv("TK_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	cfg := "name: \"wc\"\nautoCommit: false\nfields: {areas: {type: \"strings\"}}\n"
	if err := os.WriteFile(filepath.Join(dir, "tk.cue"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	addTicket(t, dir, "wc-ab2c", "x", "todo", "a0", "# X\n", false, "areas: [api, 7]\n")

	out, _, err := run(t, app, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if !strings.Contains(out, `schema_error: wc-ab2c field "areas" has a non-string entry (7)`) {
		t.Errorf("non-string element must ride schema_error, got %q", out)
	}
}

func TestDoctorSelfDependsIsNotAlsoACycle(t *testing.T) {
	app := newApp(t)
	t.Setenv("TK_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "x", "todo", "a0", "# X\n", false, "depends: [wc-ab2c]\n")
	addTicket(t, dir, "wc-de34", "y", "todo", "a1", "# Y\n", false, "depends: [wc-fg56]\n")
	addTicket(t, dir, "wc-fg56", "z", "todo", "a2", "# Z\n", false, "depends: [wc-de34, wc-fg56]\n")

	out, _, err := run(t, app, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if !strings.Contains(out, "depends_self: wc-ab2c") {
		t.Errorf("self-depends must ride depends_self, got %q", out)
	}
	if strings.Contains(out, "depends_cycle: wc-ab2c") {
		t.Errorf("a pure self-depends must not also ride depends_cycle, got %q", out)
	}
	for _, id := range []string{"wc-de34", "wc-fg56"} {
		if !strings.Contains(out, "depends_cycle: "+id) {
			t.Errorf("real cycle member %s must still ride depends_cycle, got %q", id, out)
		}
	}
	if !strings.Contains(out, "depends_self: wc-fg56") {
		t.Errorf("a cycle member that also self-depends still rides depends_self, got %q", out)
	}
}

func TestDoctorFlagsInvalidOrderKeys(t *testing.T) {
	app := newApp(t)
	t.Setenv("TK_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	write := func(pid, name, orderLine string) {
		body := "---\nid: " + pid + "\nstatus: todo\n" + orderLine + "created: 2026-01-01T00:00:00Z\n---\n# " + name + "\n"
		if err := os.WriteFile(filepath.Join(dir, pid+"-"+name+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("wc-ab2c", "valid", "order: \"a0\"\n")
	write("wc-de34", "leaddigit", "order: \"0abc\"\n") // head must be a letter
	write("wc-fg56", "trailzero", "order: \"ab0\"\n")  // fraction must not end in 0
	write("wc-hj78", "nonstring", "order: 5\n")        // unquoted scalar
	write("wc-mm22", "emptykey", "order: \"\"\n")      // explicit empty
	write("wc-nn45", "absent", "")                     // no order key at all

	out, _, err := run(t, app, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	for _, want := range []string{
		`schema_error: wc-de34 has an invalid order key "0abc"`,
		`schema_error: wc-fg56 has an invalid order key "ab0"`,
		`schema_error: wc-hj78 has an invalid order key "5"`,
		`schema_error: wc-mm22 has a missing or empty order key`,
		`schema_error: wc-nn45 has a missing or empty order key`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
	if strings.Contains(out, "wc-ab2c has an invalid order") || strings.Contains(out, "wc-ab2c has a missing") {
		t.Errorf("a valid key must not be flagged, got %q", out)
	}
}

func TestDoctorRepairEdgeVerifyNamesPostRepairReferrers(t *testing.T) {
	app := newApp(t)
	t.Setenv("TK_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "alpha", "todo", "a0", "# A\n", false, "")
	addTicket(t, dir, "wc-ab2c", "beta", "todo", "a1", "# B\n", false, "")
	addTicket(t, dir, "wc-de34", "gamma", "todo", "a2", "# G\n", false, "depends: [wc-ab2c]\n")
	addTicket(t, dir, "wc-de34", "delta", "todo", "a3", "# D\n", false, "depends: [wc-ab2c]\n")

	out, _, err := run(t, app, "doctor", "--repair")
	if err != nil {
		t.Fatalf("doctor --repair: %v", err)
	}
	// One line per distinct referrer, each naming the id that referrer holds afterwards.
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
	// Every reported referrer id resolves to a real ticket after the run.
	for _, id := range []string{"wc-de34", "wc-de34a"} {
		if _, _, err := run(t, app, "get", id); err != nil {
			t.Errorf("edge_verify named %s, which does not resolve: %v", id, err)
		}
	}
}

func TestDoctorRepairSeesUnindexedCollision(t *testing.T) {
	app := newApp(t)
	t.Setenv("TK_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "alpha", "todo", "a0", "# Alpha\n", false, "")
	addTicket(t, dir, "wc-ab2c", "beta", "todo", "a1", "# Beta\n", false, "")

	out, _, err := run(t, app, "doctor", "--repair")
	if err != nil {
		t.Fatalf("doctor --repair: %v", err)
	}
	if !strings.Contains(out, "repaired duplicate id: wc-ab2c -> wc-ab2ca") {
		t.Fatalf("an on-disk collision absent from the index must still be repaired, got %q", out)
	}
	if !fileExists(dir, "wc-ab2ca-beta.md") {
		t.Errorf("the loser must be renamed on disk, files=%v", ticketFiles(t, dir))
	}
}

func TestDoctorRepairEqualOrder(t *testing.T) {
	app := newApp(t)
	t.Setenv("TK_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-aaaa", "a", "todo", "a0", "# A\n", false, "")
	addTicket(t, dir, "wc-bbbb", "b", "todo", "a1", "# B\n", false, "")
	addTicket(t, dir, "wc-cccc", "c", "todo", "a1", "# C\n", false, "")
	addTicket(t, dir, "wc-dddd", "d", "todo", "a2", "# D\n", false, "")

	if _, _, err := run(t, app, "doctor", "--repair"); err != nil {
		t.Fatalf("doctor --repair: %v", err)
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

func TestDoctorRepairArchiveLayoutBothWays(t *testing.T) {
	app := newApp(t)
	t.Setenv("TK_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-aaaa", "done1", "done", "a0", "# Done\n", false, "")
	addTicket(t, dir, "wc-bbbb", "todo1", "todo", "a1", "# Todo\n", true, "")

	if _, _, err := run(t, app, "doctor", "--repair"); err != nil {
		t.Fatalf("doctor --repair: %v", err)
	}
	if !fileExists(dir, filepath.Join("archive", "wc-aaaa-done1.md")) {
		t.Errorf("terminal ticket must move under archive/, files=%v", ticketFiles(t, dir))
	}
	if !fileExists(dir, "wc-bbbb-todo1.md") {
		t.Errorf("non-terminal ticket must move to dir root, files=%v", ticketFiles(t, dir))
	}
}

func TestDoctorRepairCollisionAcrossArchiveBoundaryKeepsBothTickets(t *testing.T) {
	app := newApp(t)
	t.Setenv("TK_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "alpha", "done", "a0", "# Root copy\n", false, "")
	addTicket(t, dir, "wc-ab2c", "alpha", "todo", "a1", "# Archive copy\n", true, "")

	if _, _, err := run(t, app, "doctor", "--repair"); err != nil {
		t.Fatalf("doctor --repair: %v", err)
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

// --re-space-order shortens an over-long band and is never triggered by --repair.
func TestDoctorReSpaceOrder(t *testing.T) {
	app := newApp(t)
	t.Setenv("TK_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	longKey := "a1" + strings.Repeat("V", 80)
	addTicket(t, dir, "wc-aaaa", "a", "todo", "a0", "# A\n", false, "")
	addTicket(t, dir, "wc-bbbb", "b", "todo", longKey, "# B\n", false, "")
	addTicket(t, dir, "wc-cccc", "c", "todo", "a2", "# C\n", false, "")

	out, _, err := run(t, app, "doctor")
	if err != nil {
		t.Fatalf("bare doctor: %v", err)
	}
	if !strings.Contains(out, "order_long: wc-bbbb") || !strings.Contains(out, filepath.Join(dir, "wc-bbbb-b.md")) {
		t.Errorf("order_long line should name the ticket and its path, got %q", out)
	}
	// --repair must NOT touch the long key.
	if _, _, err := run(t, app, "doctor", "--repair"); err != nil {
		t.Fatalf("doctor --repair: %v", err)
	}
	if fmValue(t, filepath.Join(dir, "wc-bbbb-b.md"), "order") != longKey {
		t.Errorf("--repair must not re-space an over-long key")
	}
	if _, _, err := run(t, app, "doctor", "--re-space-order"); err != nil {
		t.Fatalf("doctor --re-space-order: %v", err)
	}
	got := fmValue(t, filepath.Join(dir, "wc-bbbb-b.md"), "order")
	if len(got) > 64 {
		t.Errorf("--re-space-order must shorten the key, got %d chars", len(got))
	}
	if got <= "a0" || got >= "a2" {
		t.Errorf("re-space must preserve order, got %q", got)
	}
}

func TestDoctorMutatingScopeSelection(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "alpha", "todo", "a0", "# A\n", false, "")
	addTicket(t, dir, "wc-ab2c", "beta", "todo", "a1", "# B\n", false, "")

	// No ambient and no --all: usage error (exit 2) naming the three ways to select.
	_ = os.Unsetenv("TK_SCOPE")
	if _, _, err := run(t, app, "doctor", "--repair"); ExitCodeFromError(err) != exitUsage {
		t.Errorf("mutating doctor with no scope should exit 2, got %v", err)
	}
	// --all repairs without an ambient scope.
	if _, _, err := run(t, app, "doctor", "--repair", "--all"); err != nil {
		t.Errorf("doctor --repair --all should run, got %v", err)
	}
	if !fileExists(dir, "wc-ab2ca-beta.md") {
		t.Errorf("--all should have repaired the collision, files=%v", ticketFiles(t, dir))
	}
}

func TestDoctorStructuralAndCreatedClasses(t *testing.T) {
	app := newApp(t)
	t.Setenv("TK_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	// A file whose frontmatter created is date-only (non-RFC3339) and legal otherwise.
	fm := "---\nid: wc-ab2c\nstatus: todo\norder: \"a0\"\ncreated: 2026-06-20\n---\n# X\n"
	if err := os.WriteFile(filepath.Join(dir, "wc-ab2c-x.md"), []byte(fm), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, err := run(t, app, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if !strings.Contains(out, "created value missing or not RFC3339 in wc-ab2c") {
		t.Errorf("doctor should flag a non-RFC3339 created, got %q", out)
	}
	for _, line := range lines(out) {
		if word, _, found := strings.Cut(line, ": "); found && !strings.ContainsAny(word, " /") {
			if !token.HasKnownPrefix(word + ":") {
				t.Errorf("token-shaped prefix %q is not in the closed catalogue: %q", word+":", line)
			}
		}
	}
	if fmValue(t, filepath.Join(dir, "wc-ab2c-x.md"), "created") != "2026-06-20" {
		t.Errorf("bare doctor must not rewrite created")
	}
}

func TestDoctorSchemaWarnClasses(t *testing.T) {
	app := newApp(t)
	t.Setenv("TK_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	extra := "related: [wc-ab2c]\nlinks: [wc-de34]\ntags: [x, x]\n"
	addTicket(t, dir, "wc-ab2c", "x", "todo", "a0", "# X\n", false, extra)

	out, _, err := run(t, app, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if strings.Count(out, "schema_warn:") < 2 {
		t.Errorf("doctor should ride multiple schema_warn lines, got %q", out)
	}
}

func TestDoctorIgnoresLeftoverKnownTags(t *testing.T) {
	app := newApp(t)
	t.Setenv("TK_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	cfg := "name: \"wc\"\nautoCommit: false\nknownTags: [\"frontend\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "tk.cue"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	addTicket(t, dir, "wc-ab2c", "x", "todo", "a0", "# X\n", false, "tags: [orphan]\n")

	out, errOut, err := run(t, app, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	combined := out + errOut
	if strings.Contains(combined, "knownTags") || strings.Contains(combined, "schema_warn:") {
		t.Errorf("leftover knownTags must not surface and free-form tags must not warn, got %q", combined)
	}
}

func TestLensIgnoresLeftoverKnownTags(t *testing.T) {
	app := newApp(t)
	t.Setenv("TK_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	cfg := "name: \"wc\"\nautoCommit: false\nknownTags: [\"frontend\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "tk.cue"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	addTicket(t, dir, "wc-ab2c", "x", "todo", "a0", "# X\n", false, "")

	_, errOut, err := run(t, app, "lens", "orphan", "--scope", "wc")
	if err != nil {
		t.Fatalf("lens: %v", err)
	}
	if strings.Contains(errOut, "knownTags") || strings.Contains(errOut, "schema_warn:") {
		t.Errorf("lens must not warn on free-form tags or leftover knownTags, got %q", errOut)
	}
}

func TestDoctorRepairResumesInterruptedArchiveMove(t *testing.T) {
	app := newApp(t)
	t.Setenv("TK_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "ship", "done", "a0", "# Ship\n", false, "")
	addTicket(t, dir, "wc-ab2c", "ship", "done", "a0", "# Ship\n", true, "")

	out, _, err := run(t, app, "doctor", "--repair")
	if err != nil {
		t.Fatalf("doctor --repair: %v", err)
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
	if _, _, err := run(t, app, "doctor", "--repair"); err != nil {
		t.Fatalf("second doctor --repair: %v", err)
	}
	if got := ticketFiles(t, dir); len(got) != 1 {
		t.Errorf("re-run must stay idempotent, got %v", got)
	}
}

func TestDoctorRepairResumesInterruptedExtension(t *testing.T) {
	app := newApp(t)
	t.Setenv("TK_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "alpha", "todo", "a0", "# Alpha\n", false, "")
	addTicket(t, dir, "wc-ab2c", "beta", "todo", "a1", "# Beta\n", false, "")

	stale, err := os.ReadFile(filepath.Join(dir, "wc-ab2c-beta.md"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, app, "doctor", "--repair"); err != nil {
		t.Fatalf("doctor --repair: %v", err)
	}
	// Recreate the crash window: the extended file stands, the old-id file never went.
	if err := os.WriteFile(filepath.Join(dir, "wc-ab2c-beta.md"), stale, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := run(t, app, "doctor", "--repair"); err != nil {
		t.Fatalf("re-entry doctor --repair: %v", err)
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

func TestDoctorRepairAllSkipsUnreachableScope(t *testing.T) {
	app := newApp(t)
	gone := initScope(t, app, "gone")
	live := initScope(t, app, "wc")
	addTicket(t, live, "wc-ab2c", "alpha", "todo", "a0", "# A\n", false, "")
	addTicket(t, live, "wc-ab2c", "beta", "todo", "a1", "# B\n", false, "")
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}

	_ = os.Unsetenv("TK_SCOPE")
	_, errOut, err := run(t, app, "doctor", "--repair", "--all")
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
