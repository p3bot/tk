package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/depgate"
	"github.com/p3bot/tk/internal/token"
)

func initScope(t *testing.T, app *App, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	out, _, err := run(t, app, "scope", "init", dir, "--name", name)
	if err != nil {
		t.Fatalf("init %s: %v", name, err)
	}
	return strings.TrimSpace(out)
}

func addTicket(t *testing.T, dir, id, slug, status, order, body string, archived bool, extraFM string) {
	t.Helper()
	name := id + "-" + slug + ".md"
	target := dir
	if archived {
		target = filepath.Join(dir, "archive")
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	fm := "---\nid: " + id + "\nstatus: " + status + "\norder: \"" + order + "\"\ncreated: 2026-01-01T00:00:00Z\n" + extraFM + "---\n"
	if err := os.WriteFile(filepath.Join(target, name), []byte(fm+body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func lines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func TestListBoardContract(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "network", "todo", "a0", "# Network redesign\n\nbody", false, "")
	addTicket(t, dir, "wc-de34", "auth", "todo", "a1", "# Auth flow\n", false, "depends: [wc-ab2c]\n")
	addTicket(t, dir, "wc-gh56", "old", "done", "a2", "# Old work\n", true, "")

	out, _, err := run(t, app, "list", "--scope", "wc")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	rows := lines(out)
	if len(rows) != 2 {
		t.Fatalf("default board should show 2 active rows, got %d: %q", len(rows), out)
	}
	if rows[0] != "wc-ab2c\ttodo\tNetwork redesign\t" {
		t.Errorf("row0 = %q", rows[0])
	}
	if rows[1] != "wc-de34\ttodo\tAuth flow\twc-ab2c" {
		t.Errorf("row1 waiting-on wrong: %q", rows[1])
	}
	for _, r := range rows {
		if strings.Contains(r, "\x1b") {
			t.Errorf("TSV must never carry ANSI: %q", r)
		}
	}

	out, _, _ = run(t, app, "list", "--scope", "wc", "--all")
	if len(lines(out)) != 3 {
		t.Errorf("--all should include archived done, got %q", out)
	}

	out, _, err = run(t, app, "list", "--scope", "wc", "done")
	if err != nil {
		t.Fatalf("list done: %v", err)
	}
	doneRows := lines(out)
	if len(doneRows) != 1 || doneRows[0] != "wc-gh56\tdone\tOld work\t" {
		t.Errorf("list done should show archived done without --all, got %q", out)
	}

	// Unknown status positional → exit 2.
	_, _, err = run(t, app, "list", "--scope", "wc", "bogus")
	if got := ExitCodeFromError(err); got != exitUsage {
		t.Errorf("unknown status exit = %d want %d", got, exitUsage)
	}
}

func TestListTSVFlattensControlCharsInFields(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "tv")
	addTicket(t, dir, "tv-ab2c", "note", "todo", "a0", "# col1\tcol2\n", false, "")

	out, _, err := run(t, app, "list", "--scope", "tv")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	rows := lines(out)
	if len(rows) != 1 {
		t.Fatalf("a single ticket must stay one TSV line, got %d: %q", len(rows), out)
	}
	if got := strings.Count(rows[0], "\t"); got != 3 {
		t.Errorf("row must have exactly 4 fields (3 tabs), got %d: %q", got, rows[0])
	}
	if strings.Contains(rows[0], "col1\tcol2") {
		t.Errorf("title tab leaked into the TSV: %q", rows[0])
	}
	if !strings.Contains(rows[0], "col1 col2") {
		t.Errorf("title tab should flatten to a space: %q", rows[0])
	}
}

func TestNextSelectionAndBlocked(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "network", "todo", "a0", "# Network\n", false, "")
	addTicket(t, dir, "wc-de34", "auth", "todo", "a1", "# Auth\n", false, "depends: [wc-ab2c]\n")

	out, _, err := run(t, app, "next", "--scope", "wc")
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if !strings.HasSuffix(strings.TrimSpace(out), "wc-ab2c-network.md") {
		t.Errorf("next should pick ab2c, got %q", out)
	}

	if err := os.Remove(filepath.Join(dir, "wc-ab2c-network.md")); err != nil {
		t.Fatal(err)
	}
	addTicket(t, dir, "wc-ab2c", "network", "done", "a0", "# Network\n", true, "")
	out, _, err = run(t, app, "next", "--scope", "wc")
	if err != nil {
		t.Fatalf("next after unblock: %v", err)
	}
	if !strings.HasSuffix(strings.TrimSpace(out), "wc-de34-auth.md") {
		t.Errorf("next should pick de34 after ab2c done, got %q", out)
	}
}

func TestNextStdoutMatchesDepgateSelectNext(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "network", "todo", "a0", "# Network\n", false, "")
	addTicket(t, dir, "wc-de34", "auth", "todo", "a1", "# Auth\n", false, "depends: [wc-ab2c]\n")

	out, _, err := run(t, app, "next", "--scope", "wc")
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	printed := strings.TrimSpace(out)

	e, err := app.openEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer e.close()
	res, err := e.reconcileResult(map[string]string{"wc": dir})
	if err != nil {
		t.Fatal(err)
	}
	gate, err := depgate.Load(e.gateDeps(), res, []string{"wc"})
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := e.db.NextCandidates("wc")
	if err != nil {
		t.Fatal(err)
	}
	sel := gate.SelectNext(candidates, e.reg.Lens["wc"], false)
	if sel.Chosen == nil {
		t.Fatal("library chose nothing")
	}
	if sel.Chosen.Path != printed {
		t.Fatalf("library path %q vs tk next stdout %q", sel.Chosen.Path, printed)
	}
}

func TestNextEmptyBecauseBlocked(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-de34", "auth", "todo", "a0", "# Auth\n", false, "depends: [wc-zz99]\n")

	out, errOut, err := run(t, app, "next", "--scope", "wc")
	if err == nil {
		t.Fatal("blocked queue should be non-zero")
	}
	if out != "" {
		t.Errorf("blocked next must print no path, got %q", out)
	}
	if !strings.Contains(err.Error(), "waiting on unmet deps") {
		t.Errorf("expected blocked diagnostic, got %v", err)
	}
	if !strings.Contains(errOut, "depends_dangling:") {
		t.Errorf("expected depends_dangling token on stderr, got %q", errOut)
	}
}

func TestGetMetaAndDuplicate(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	body := "# Network redesign\n\nbody"
	addTicket(t, dir, "wc-ab2c", "network", "todo", "a0", body, false, "summary: short one\n")

	out, _, err := run(t, app, "get", "wc-ab2c")
	if err != nil || !strings.HasSuffix(strings.TrimSpace(out), "wc-ab2c-network.md") {
		t.Fatalf("get = %q err=%v", out, err)
	}
	out, _, err = run(t, app, "get", "ab2c", "--scope", "wc")
	if err != nil || strings.TrimSpace(out) == "" {
		t.Fatalf("get short = %q err=%v", out, err)
	}

	wantPath := filepath.Join(dir, "wc-ab2c-network.md")
	wantBytes, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatal(err)
	}
	out, _, err = run(t, app, "get", "wc-ab2c", "--content")
	if err != nil {
		t.Fatalf("get --content: %v", err)
	}
	if out != string(wantBytes) {
		t.Errorf("get --content must print exact file bytes\ngot:\n%s\nwant:\n%s", out, wantBytes)
	}
	if strings.Contains(out, wantPath) && !strings.Contains(string(wantBytes), wantPath) {
		t.Error("get --content must not print the path")
	}

	out, _, err = run(t, app, "meta", "get", "wc-ab2c")
	if err != nil {
		t.Fatalf("meta get: %v", err)
	}
	if !strings.HasPrefix(out, "title: Network redesign\npath: ") {
		t.Errorf("meta get preamble wrong: %q", out)
	}
	if i := strings.Index(out, "\n\n"); i >= 0 {
		if strings.Contains(out[:i], "id:") {
			t.Errorf("meta get preamble must not include id:, got %q", out[:i])
		}
	}
	if !strings.Contains(out, "summary: short one") {
		t.Errorf("meta get should carry raw summary: %q", out)
	}

	addTicket(t, dir, "wc-ab2c", "dup", "todo", "a3", "# Dup\n", false, "")
	_, errOut, err := run(t, app, "get", "wc-ab2c")
	if err == nil {
		t.Fatal("duplicate id must refuse")
	}
	if !strings.Contains(err.Error(), "duplicate_id:") {
		t.Errorf("expected the duplicate_id refusal on the error, got err=%v", err)
	}
	if strings.Contains(errOut, "duplicate_id:") {
		t.Errorf("reconcile's duplicate_id echo must be suppressed when the verb refuses, got stderr=%q", errOut)
	}
}

func TestDuplicateSuppressionKeepsOtherIDs(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "one", "todo", "a0", "# Ab One\n", false, "")
	addTicket(t, dir, "wc-ab2c", "two", "todo", "a1", "# Ab Two\n", false, "")
	addTicket(t, dir, "wc-de34", "one", "todo", "a2", "# De One\n", false, "")
	addTicket(t, dir, "wc-de34", "two", "todo", "a3", "# De Two\n", false, "")

	_, errOut, err := run(t, app, "get", "wc-ab2c")
	if err == nil {
		t.Fatal("duplicate id must refuse")
	}
	if !strings.Contains(err.Error(), "duplicate_id:") || !strings.Contains(err.Error(), "wc-ab2c") {
		t.Errorf("refusal should name wc-ab2c, got %v", err)
	}
	if strings.Contains(errOut, "wc-ab2c claimed by") {
		t.Errorf("ab2c's integrity echo must be suppressed when the verb refuses on it: %q", errOut)
	}
	if !strings.Contains(errOut, "wc-de34 claimed by") {
		t.Errorf("de34's unrelated duplicate_id must still ride: %q", errOut)
	}
}

func TestParseErrorLocatable(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	bad := "---\nid: wc-ab2c\n<<<<<<< HEAD\nstatus: todo\n=======\nstatus: done\n>>>>>>> x\n---\n# T\n"
	if err := os.WriteFile(filepath.Join(dir, "wc-ab2c-broken.md"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}

	// get exits 0 with the path and a parse_error line on stderr.
	out, errOut, err := run(t, app, "get", "wc-ab2c")
	if err != nil {
		t.Fatalf("get on quarantine should exit 0: %v", err)
	}
	if strings.TrimSpace(out) == "" {
		t.Error("get on quarantine should still print the path")
	}
	if !strings.Contains(errOut, "parse_error:") {
		t.Errorf("expected parse_error on stderr, got %q", errOut)
	}
}

func TestSearchAndDeps(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "network", "todo", "a0", "# Network redesign\n\nsockets and buffers", false, "")
	addTicket(t, dir, "wc-de34", "auth", "todo", "a1", "# Auth\n", false, "depends: [wc-ab2c]\nrelated: [wc-ab2c]\n")

	out, _, err := run(t, app, "search", "sockets", "--scope", "wc")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	rows := lines(out)
	if len(rows) != 1 || !strings.HasPrefix(rows[0], "wc-ab2c\ttodo\tNetwork redesign\t\t") || !strings.HasSuffix(rows[0], ".md") {
		t.Errorf("search hit wrong: %q", out)
	}
	findOut, _, err := run(t, app, "find", "sockets", "--scope", "wc")
	if err != nil {
		t.Fatalf("find alias: %v", err)
	}
	if findOut != out {
		t.Errorf("find alias output differs from search:\nsearch=%q\nfind=%q", out, findOut)
	}

	out, _, err = run(t, app, "deps", "wc-de34")
	if err != nil {
		t.Fatalf("deps: %v", err)
	}
	if !strings.Contains(out, "depends on:\n  wc-ab2c\ttodo\tNetwork redesign") {
		t.Errorf("deps depends-on section wrong: %q", out)
	}
	if !strings.Contains(out, "related:\n  wc-ab2c") {
		t.Errorf("deps related section wrong: %q", out)
	}
	// depends and dep are aliases of deps.
	for _, name := range []string{"depends", "dep"} {
		aliasOut, _, aliasErr := run(t, app, name, "wc-de34")
		if aliasErr != nil {
			t.Fatalf("%s alias: %v", name, aliasErr)
		}
		if aliasOut != out {
			t.Errorf("%s alias output differs from deps", name)
		}
	}

	out, _, _ = run(t, app, "deps", "wc-ab2c")
	if !strings.Contains(out, "is depended on by:\n  wc-de34") {
		t.Errorf("deps reverse section wrong: %q", out)
	}
}

func TestListExcludesQuarantinedRows(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "network", "todo", "a0", "# Network\n", false, "")
	bad := "---\nid: wc-de34\n<<<<<<< HEAD\nstatus: todo\n=======\nstatus: done\n>>>>>>> x\n---\n# Broken\n"
	if err := os.WriteFile(filepath.Join(dir, "wc-de34-broken.md"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}

	out, _, err := run(t, app, "list", "--scope", "wc", "--all")
	if err != nil {
		t.Fatalf("list --all: %v", err)
	}
	rows := lines(out)
	if len(rows) != 1 || !strings.HasPrefix(rows[0], "wc-ab2c\t") {
		t.Fatalf("list --all should show only the healthy row, got %q", out)
	}
	for _, r := range rows {
		if strings.HasPrefix(r, "wc-de34\t") {
			t.Errorf("quarantined row must not appear on the board: %q", r)
		}
	}

	got, _, err := run(t, app, "get", "wc-de34")
	if err != nil || strings.TrimSpace(got) == "" {
		t.Errorf("quarantined ticket should still resolve via get: out=%q err=%v", got, err)
	}
}

func TestSchemaErrorHoldsFromNext(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "clean", "todo", "a0", "# Clean\n", false, "")
	addTicket(t, dir, "wc-de34", "broken", "todo", "a1", "# Broken\n", false, "depends: [bogus]\n")

	out, errOut, err := run(t, app, "next", "--scope", "wc")
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if !strings.HasSuffix(strings.TrimSpace(out), "wc-ab2c-clean.md") {
		t.Errorf("next should pick the clean todo, got %q", out)
	}
	if !strings.Contains(errOut, "schema_error:") {
		t.Errorf("expected schema_error on stderr for the malformed depends, got %q", errOut)
	}
}

func TestArchiveDriftTokensRideRead(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "wip", "todo", "a0", "# WIP\n", true, "")
	addTicket(t, dir, "wc-de34", "shipped", "done", "a1", "# Shipped\n", false, "")

	_, errOut, err := run(t, app, "list", "--scope", "wc")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(errOut, "archive_non_terminal:") {
		t.Errorf("expected archive_non_terminal for the todo under archive/, got %q", errOut)
	}
	if !strings.Contains(errOut, "archive_terminal_at_root:") {
		t.Errorf("expected archive_terminal_at_root for the done at root, got %q", errOut)
	}
}

func TestEqualOrderTokenRidesRead(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "one", "todo", "a0", "# One\n", false, "")
	addTicket(t, dir, "wc-de34", "two", "todo", "a0", "# Two\n", false, "")

	_, errOut, err := run(t, app, "list", "--scope", "wc")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(errOut, "equal_order:") {
		t.Errorf("expected equal_order on stderr for the shared order key, got %q", errOut)
	}
}

func TestConfigUnparseableRidesRead(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "network", "todo", "a0", "# Network\n", false, "")

	bad := "name: \"wc\"\nautoCommit: true\nfields: {x: {type: \"float\"}}\n"
	if err := os.WriteFile(filepath.Join(dir, "tk.cue"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}

	out, errOut, err := run(t, app, "list", "--scope", "wc")
	if err != nil {
		t.Fatalf("read under an unusable config should stay exit 0: %v", err)
	}
	if strings.TrimSpace(out) == "" {
		t.Error("reads stay available under config_unparseable; expected the board row")
	}
	if !strings.Contains(errOut, "config_unparseable:") {
		t.Errorf("expected config_unparseable on stderr, got %q", errOut)
	}
}

func TestUnreachableScopeRidesRead(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}

	_, errOut, err := run(t, app, "list", "--scope", "wc")
	if err != nil {
		t.Fatalf("list against an unreachable scope should stay exit 0: %v", err)
	}
	if !strings.Contains(errOut, "unreachable_scope:") {
		t.Errorf("expected unreachable_scope on stderr, got %q", errOut)
	}
	// One token per dir-not-usable mode: an unreachable scope must not also read as a broken config.
	if strings.Contains(errOut, "config_unparseable:") {
		t.Errorf("unreachable scope must not also ride config_unparseable, got %q", errOut)
	}
}

func TestDepsTransitiveAndTree(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-aa22", "top", "todo", "a0", "# Top\n", false, "depends: [wc-bb33]\n")
	addTicket(t, dir, "wc-bb33", "mid", "todo", "a1", "# Mid\n", false, "depends: [wc-cc44]\n")
	addTicket(t, dir, "wc-cc44", "leaf", "todo", "a2", "# Leaf\n", false, "")

	out, _, err := run(t, app, "deps", "wc-aa22", "--transitive")
	if err != nil {
		t.Fatalf("deps --transitive: %v", err)
	}
	if !strings.Contains(out, "depends on (transitive):") {
		t.Errorf("expected the transitive section header, got %q", out)
	}
	if !strings.Contains(out, "wc-bb33") || !strings.Contains(out, "wc-cc44") {
		t.Errorf("transitive depends should include the whole chain, got %q", out)
	}

	out, _, err = run(t, app, "deps", "wc-aa22", "--tree")
	if err != nil {
		t.Fatalf("deps --tree: %v", err)
	}
	if !strings.Contains(out, "depends tree:") {
		t.Errorf("expected the tree header, got %q", out)
	}
	if !strings.Contains(out, "\n    wc-bb33\t") || !strings.Contains(out, "\n      wc-cc44\t") {
		t.Errorf("tree should indent bb33 then cc44 one level deeper, got %q", out)
	}
}

func TestMetaNoFrontmatterFenceIsNonZero(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	if err := os.WriteFile(filepath.Join(dir, "wc-ff44-nofm.md"), []byte("# No frontmatter\n\njust a body\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, errOut, err := run(t, app, "meta", "get", "wc-ff44")
	if err == nil {
		t.Fatal("meta get on wholly-unparseable frontmatter must be non-zero")
	}
	if out != "" {
		t.Errorf("meta get with no readable frontmatter must print no stdout, got %q", out)
	}
	if !strings.Contains(errOut, "parse_error:") || !strings.Contains(errOut, "no extractable frontmatter block") {
		t.Errorf("expected the no-frontmatter parse_error diagnostic, got %q", errOut)
	}
}

func TestSearchMalformedQueryCleanMessage(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "network", "todo", "a0", "# Network\n", false, "")

	out, _, err := run(t, app, "search", `foo"`, "--scope", "wc")
	if err == nil {
		t.Fatal("malformed query must be non-zero")
	}
	if out != "" {
		t.Errorf("malformed query must print no hits, got %q", out)
	}
	if !strings.Contains(err.Error(), "invalid search query") || !strings.Contains(err.Error(), "FTS5") {
		t.Errorf("expected a clean FTS5-hint message, got %v", err)
	}
	if strings.Contains(err.Error(), "SQL logic error") || strings.Contains(err.Error(), "sqlite") {
		t.Errorf("must not leak the raw driver error: %v", err)
	}
}

func TestQueryReadOnly(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "network", "todo", "a0", "# Network\n", false, "")

	out, _, err := run(t, app, "query", "SELECT id, status FROM tickets ORDER BY id")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if !strings.Contains(out, "wc-ab2c\ttodo") {
		t.Errorf("query result wrong: %q", out)
	}
	_, _, err = run(t, app, "query", "DELETE FROM tickets")
	if err == nil {
		t.Error("query must reject a write")
	}
	out, _, err = run(t, app, "query", "--schema")
	if err != nil || !strings.Contains(out, "NOT A STABLE API") {
		t.Errorf("query --schema = %q err=%v", out, err)
	}
	if !strings.Contains(out, "ticket_tags") {
		t.Errorf("query --schema must document ticket_tags, got %q", out)
	}
}

func TestCrossScopeDependsGate(t *testing.T) {
	app := newApp(t)
	up := initScope(t, app, "up")
	wc := initScope(t, app, "wc")

	addTicket(t, up, "up-aa22", "core", "todo", "a0", "# Core\n", false, "")
	addTicket(t, wc, "wc-bb22", "feat", "todo", "a0", "# Feature\n", false, "depends: [up-aa22]\n")
	addTicket(t, wc, "wc-cc33", "ext", "todo", "a1", "# Ext\n", false, "depends: [zzz-zz99]\n")

	// The cross-scope gate holds wc-bb22 and wc-cc33; next is empty-because-blocked.
	out, errOut, err := run(t, app, "next", "--scope", "wc")
	if err == nil || out != "" {
		t.Fatalf("cross-scope-blocked next should be non-zero with no path: out=%q err=%v", out, err)
	}
	if !strings.Contains(errOut, "depends_unresolvable:") {
		t.Errorf("expected depends_unresolvable for the unregistered-scope dep, got %q", errOut)
	}

	out, _, _ = run(t, app, "list", "--scope", "wc")
	if !strings.Contains(out, "wc-bb22\ttodo\tFeature\tup-aa22") {
		t.Errorf("waiting-on should carry the cross-scope dep: %q", out)
	}

	if err := os.Remove(filepath.Join(up, "up-aa22-core.md")); err != nil {
		t.Fatal(err)
	}
	addTicket(t, up, "up-aa22", "core", "done", "a0", "# Core\n", true, "")
	out, _, err = run(t, app, "next", "--scope", "wc")
	if err != nil {
		t.Fatalf("next after cross-scope unblock: %v", err)
	}
	if !strings.HasSuffix(strings.TrimSpace(out), "wc-bb22-feat.md") {
		t.Errorf("wc-bb22 should be runnable once up-aa22 is done, got %q", out)
	}
}

func TestEditOpensEditorAndIsSilent(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "network", "todo", "a0", "# Network\n", false, "")

	t.Setenv("EDITOR", "true")
	out, _, err := run(t, app, "edit", "wc-ab2c")
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if out != "" {
		t.Errorf("edit prints nothing on success, got stdout=%q", out)
	}

	// With $EDITOR unset, edit refuses with guidance and no stdout.
	t.Setenv("EDITOR", "")
	out, _, err = run(t, app, "edit", "wc-ab2c")
	if err == nil {
		t.Fatal("edit with no $EDITOR must be non-zero")
	}
	if out != "" {
		t.Errorf("edit failure must print no stdout, got %q", out)
	}
	if !strings.Contains(err.Error(), "$EDITOR") {
		t.Errorf("expected an $EDITOR-not-set message, got %v", err)
	}
}

func TestQueryRejectsSmuggledWrites(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "network", "todo", "a0", "# Network\n", false, "")

	if _, _, err := run(t, app, "query", "WITH x AS (SELECT 1) DELETE FROM tickets"); err == nil {
		t.Error("query must reject a CTE-smuggled write")
	}
	// A write appended after a SELECT is caught by the statement splitter.
	if _, _, err := run(t, app, "query", "SELECT 1; DROP TABLE tickets"); err == nil {
		t.Error("query must reject a write appended after a SELECT")
	}
	// The tickets table must survive both refusals.
	out, _, err := run(t, app, "query", "SELECT count(*) FROM tickets")
	if err != nil || !strings.Contains(out, "1") {
		t.Errorf("tickets table should be intact after rejected writes: out=%q err=%v", out, err)
	}
}

func TestLensAppliesAndEchoes(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "fe", "todo", "a0", "# Frontend\n", false, "tags: [frontend]\n")
	addTicket(t, dir, "wc-de34", "be", "todo", "a1", "# Backend\n", false, "tags: [backend]\n")
	addTicket(t, dir, "wc-gh56", "un", "todo", "a2", "# Untagged\n", false, "")
	// Off-lens tagged work (neither frontend nor untagged).
	addTicket(t, dir, "wc-jk89", "st", "todo", "a3", "# Style\n", false, "tags: [style]\n")

	if _, _, err := run(t, app, "lens", "frontend", "--scope", "wc"); err != nil {
		t.Fatalf("lens set: %v", err)
	}
	out, errOut, err := run(t, app, "list", "--scope", "wc")
	if err != nil {
		t.Fatalf("list under lens: %v", err)
	}
	rows := lines(out)
	// frontend (tag match) + untagged (never hidden); backend and style filtered out.
	if len(rows) != 2 {
		t.Fatalf("lens should show frontend + untagged, got %q", out)
	}
	for _, r := range rows {
		if strings.HasPrefix(r, "wc-de34") || strings.HasPrefix(r, "wc-jk89") {
			t.Errorf("non-lens ticket should be filtered: %q", r)
		}
	}
	if !strings.Contains(errOut, "lens:") {
		t.Errorf("active lens should echo on stderr, got %q", errOut)
	}

	out, _, _ = run(t, app, "list", "--scope", "wc", "--no-lens")
	if len(lines(out)) != 4 {
		t.Errorf("--no-lens should bypass, got %q", out)
	}

	// --tag is a hard membership filter and supersedes the lens (no lens echo).
	out, errOut, err = run(t, app, "list", "--scope", "wc", "--tag", "backend")
	if err != nil {
		t.Fatalf("list under lens with --tag: %v", err)
	}
	got := listRowIDs(out)
	if !sameStringSet(got, []string{"wc-de34"}) {
		t.Fatalf("--tag backend hard cut = %v want [wc-de34] (out %q)", got, out)
	}
	if strings.Contains(errOut, "lens:") {
		t.Errorf("--tag must suppress lens echo, stderr %q", errOut)
	}

	// --no-lens + --tag: same hard filter (lens already ignored by --tag).
	out, _, err = run(t, app, "list", "--scope", "wc", "--no-lens", "--tag", "backend")
	if err != nil {
		t.Fatalf("list --no-lens --tag: %v", err)
	}
	got = listRowIDs(out)
	if !sameStringSet(got, []string{"wc-de34"}) {
		t.Errorf("--no-lens --tag backend should be backend only, got %v", got)
	}

	if _, _, err := run(t, app, "lens", "--clear", "--scope", "wc"); err != nil {
		t.Fatalf("lens clear: %v", err)
	}
	out, _, _ = run(t, app, "lens", "--scope", "wc")
	if strings.TrimSpace(out) != "" {
		t.Errorf("cleared lens should show empty, got %q", out)
	}
}

// listRowIDs extracts the full-id column from headerless list TSV.
func listRowIDs(out string) []string {
	var ids []string
	for _, line := range lines(out) {
		if line == "" {
			continue
		}
		id, _, _ := strings.Cut(line, "\t")
		ids = append(ids, id)
	}
	return ids
}

func containsID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func sameStringSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	set := map[string]bool{}
	for _, g := range got {
		set[g] = true
	}
	for _, w := range want {
		if !set[w] {
			return false
		}
	}
	return true
}

func TestTagsInventory(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")

	// Empty scope: nothing, exit 0.
	out, _, err := run(t, app, "tags", "--scope", "wc")
	if err != nil {
		t.Fatalf("tags empty: %v", err)
	}
	if out != "" {
		t.Errorf("empty scope must print nothing, got %q", out)
	}

	// Active multi-tag, archive-only tag, backlog-only tag, dedupe across tickets.
	// Short ids use the closed alphabet (no i/l/o/0/1).
	addTicket(t, dir, "wc-ab2c", "fe", "todo", "a0", "# Frontend\n", false, "tags: [frontend, shared]\n")
	addTicket(t, dir, "wc-de34", "be", "todo", "a1", "# Backend\n", false, "tags: [backend, shared]\n")
	addTicket(t, dir, "wc-gh56", "old", "done", "a2", "# Old\n", true, "tags: [legacy]\n")
	addTicket(t, dir, "wc-mn78", "plan", "backlog", "a3", "# Plan\n", false, "tags: [plan]\n")
	addTicket(t, dir, "wc-pq23", "plain", "todo", "a4", "# Plain\n", false, "")
	// Repeated tag on one file collapses to one row and still indexes.
	addTicket(t, dir, "wc-rs45", "dup", "todo", "a5", "# Dup\n", false, "tags: [shared, shared]\n")

	out, _, err = run(t, app, "tags", "--scope", "wc")
	if err != nil {
		t.Fatalf("tags: %v", err)
	}
	want := []string{"backend", "frontend", "legacy", "plan", "shared"}
	got := lines(out)
	if len(got) != len(want) {
		t.Fatalf("tags = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tags[%d] = %q, want %q (full %v)", i, got[i], want[i], got)
		}
	}

	// Alias parity.
	aliasOut, _, err := run(t, app, "tag", "--scope", "wc")
	if err != nil {
		t.Fatalf("tag alias: %v", err)
	}
	if aliasOut != out {
		t.Errorf("tag alias stdout %q != tags %q", aliasOut, out)
	}

	// Active lens must not hide tags on non-matching tickets.
	if _, _, err := run(t, app, "lens", "frontend", "--scope", "wc"); err != nil {
		t.Fatalf("lens set: %v", err)
	}
	out, _, err = run(t, app, "tags", "--scope", "wc")
	if err != nil {
		t.Fatalf("tags under lens: %v", err)
	}
	got = lines(out)
	if len(got) != len(want) {
		t.Fatalf("lens must not shrink tags, got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("under lens tags[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	// Sanity: list under the same lens hides backend-tagged ticket.
	listOut, _, err := run(t, app, "list", "--scope", "wc")
	if err != nil {
		t.Fatalf("list under lens: %v", err)
	}
	for _, r := range lines(listOut) {
		if strings.HasPrefix(r, "wc-de34") {
			t.Errorf("list should filter backend under lens, but tags inventory still shows backend: list=%q tags=%v", listOut, got)
		}
	}

	// --scope selection: second scope is empty and isolated.
	_ = initScope(t, app, "ui")
	out, _, err = run(t, app, "tags", "--scope", "ui")
	if err != nil {
		t.Fatalf("tags ui: %v", err)
	}
	if out != "" {
		t.Errorf("empty other scope must print nothing, got %q", out)
	}
}

func TestTagUnknownOnLensAndList(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "fe", "todo", "a0", "# Frontend\n", false, "tags: [frontend]\n")
	addTicket(t, dir, "wc-de34", "old", "done", "a1", "# Old\n", true, "tags: [legacy]\n")

	// Known active tag: silent.
	_, errOut, err := run(t, app, "lens", "frontend", "--scope", "wc")
	if err != nil {
		t.Fatalf("lens known: %v", err)
	}
	if strings.Contains(errOut, token.TagUnknown) {
		t.Errorf("known lens tag must not warn, got %q", errOut)
	}

	// Known archive-only tag: silent (in-use set includes archive).
	_, errOut, err = run(t, app, "lens", "legacy", "--scope", "wc")
	if err != nil {
		t.Fatalf("lens archive tag: %v", err)
	}
	if strings.Contains(errOut, token.TagUnknown) {
		t.Errorf("archive-present lens tag must not warn, got %q", errOut)
	}

	// Unknown tag: soft warn, still applies.
	_, errOut, err = run(t, app, "lens", "orphan", "frontend", "orphan", "--scope", "wc")
	if err != nil {
		t.Fatalf("lens unknown: %v", err)
	}
	wantUnknown := token.FormatTagUnknown("orphan")
	if !strings.Contains(errOut, wantUnknown) {
		t.Errorf("lens unknown missing %q in stderr %q", wantUnknown, errOut)
	}
	if strings.Count(errOut, token.TagUnknown) != 1 {
		t.Errorf("duplicate unknown tag must warn once, got %q", errOut)
	}
	if strings.Contains(errOut, token.SchemaWarn) {
		t.Errorf("must not reuse schema_warn:, got %q", errOut)
	}
	// Lens still set (show path).
	out, _, err := run(t, app, "lens", "--scope", "wc")
	if err != nil {
		t.Fatalf("lens show: %v", err)
	}
	if !strings.Contains(out, "orphan") || !strings.Contains(out, "frontend") {
		t.Errorf("lens must still apply, got %q", out)
	}

	// Clear and show: no tag_unknown.
	_, errOut, err = run(t, app, "lens", "--clear", "--scope", "wc")
	if err != nil {
		t.Fatalf("lens clear: %v", err)
	}
	if strings.Contains(errOut, token.TagUnknown) {
		t.Errorf("lens --clear must not warn, got %q", errOut)
	}

	// list --tag unknown: warn + empty (or non-matching) list still runs.
	out, errOut, err = run(t, app, "list", "--tag", "missing", "--scope", "wc")
	if err != nil {
		t.Fatalf("list unknown tag: %v", err)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("unknown tag filter should yield empty list, got %q", out)
	}
	wantMissing := token.FormatTagUnknown("missing")
	if !strings.Contains(errOut, wantMissing) {
		t.Errorf("list --tag unknown missing %q in stderr %q", wantMissing, errOut)
	}
	if strings.Contains(errOut, token.SchemaWarn) {
		t.Errorf("list must not reuse schema_warn:, got %q", errOut)
	}

	// list --tag known: silent.
	out, errOut, err = run(t, app, "list", "--tag", "frontend", "--scope", "wc")
	if err != nil {
		t.Fatalf("list known tag: %v", err)
	}
	if !strings.Contains(out, "wc-ab2c") {
		t.Errorf("known tag should keep matching row, got %q", out)
	}
	if strings.Contains(errOut, token.TagUnknown) {
		t.Errorf("known list tag must not warn, got %q", errOut)
	}

	// Multi --tag: one line per distinct unknown; token stays off stdout.
	out, errOut, err = run(t, app, "list", "--tag", "ghost", "--tag", "frontend", "--tag", "ghost", "--scope", "wc")
	if err != nil {
		t.Fatalf("list multi tag: %v", err)
	}
	if strings.Count(errOut, token.TagUnknown) != 1 {
		t.Errorf("want one tag_unknown for ghost, got %q", errOut)
	}
	if !strings.Contains(errOut, token.FormatTagUnknown("ghost")) {
		t.Errorf("want ghost unknown line, got %q", errOut)
	}
	if strings.Contains(out, token.TagUnknown) || strings.Contains(out, "tag_unknown:") {
		t.Errorf("tag_unknown must not ride list stdout, got %q", out)
	}
	if !strings.Contains(out, "wc-ab2c") {
		t.Errorf("known tag in multi filter should keep matching row, got %q", out)
	}
}
