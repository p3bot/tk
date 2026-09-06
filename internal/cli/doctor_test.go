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
	if !strings.Contains(out, "run tk repair") {
		t.Errorf("duplicate_id tail should name tk repair, got %q", out)
	}
	if strings.Contains(out, "\x1b") {
		t.Errorf("token report must never carry ANSI: %q", out)
	}
	after := ticketFiles(t, dir)
	if len(before) != len(after) || !fileExists(dir, "wc-ab2c-beta.md") {
		t.Errorf("bare doctor must mutate nothing: before=%v after=%v", before, after)
	}
}

func TestDoctorUnknownMutatingFlags(t *testing.T) {
	app := newApp(t)
	for _, args := range [][]string{
		{"doctor", "--repair"},
		{"doctor", "--re-space-order"},
		{"doctor", "--all"},
	} {
		_, _, err := run(t, app, args...)
		if ExitCodeFromError(err) != exitUsage {
			t.Errorf("%v should be unknown (exit 2), got %v", args, err)
		}
	}
}

func TestDoctorHelpIsDiagnoseOnly(t *testing.T) {
	app := newApp(t)
	out, _, err := run(t, app, "doctor", "--help")
	if err != nil {
		t.Fatalf("doctor --help: %v", err)
	}
	if strings.Contains(out, "--reindex") {
		t.Errorf("doctor --help must not mention --reindex:\n%s", out)
	}
	for _, flag := range []string{"--repair", "--re-space-order", "--all"} {
		if strings.Contains(out, flag) {
			t.Errorf("doctor --help must not mention %s:\n%s", flag, out)
		}
	}
	if !strings.Contains(out, "tk reindex") {
		t.Errorf("doctor --help should point at tk reindex:\n%s", out)
	}
	if !strings.Contains(out, "tk repair") {
		t.Errorf("doctor --help should point at tk repair:\n%s", out)
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

func TestDoctorReportsOrderLong(t *testing.T) {
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
	if !strings.Contains(out, "run tk repair --re-space-order") {
		t.Errorf("order_long tail should name tk repair --re-space-order, got %q", out)
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
