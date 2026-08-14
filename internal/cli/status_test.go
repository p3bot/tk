package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func parsePulse(out string) map[string]string {
	m := map[string]string{}
	for _, line := range lines(out) {
		key, val, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		// Keys are left-justified to statusKeyWidth before the tab.
		m[strings.TrimRight(key, " ")] = val
	}
	return m
}

func pulseKeys(out string) []string {
	var keys []string
	for _, line := range lines(out) {
		key, _, ok := strings.Cut(line, "\t")
		if ok {
			keys = append(keys, strings.TrimRight(key, " "))
		}
	}
	return keys
}

func TestStatusDashboardKeyOrderAndCounts(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-aa22", "todo", "todo", "a1", "# T\n", false, "")
	addTicket(t, dir, "wc-ab23", "review", "review", "a2", "# R\n", false, "")
	addTicket(t, dir, "wc-ac24", "ip", "in-progress", "a3", "# I\n", false, "")
	addTicket(t, dir, "wc-ad25", "blocked", "blocked", "a4", "# B\n", false, "")
	addTicket(t, dir, "wc-ae26", "draft", "draft", "a5", "# D\n", false, "")
	addTicket(t, dir, "wc-af27", "backlog", "backlog", "a6", "# L\n", false, "")
	addTicket(t, dir, "wc-ag28", "done", "done", "a7", "# Done\n", true, "")
	addTicket(t, dir, "wc-ah29", "cancel", "cancelled", "a8", "# X\n", true, "")

	out, errOut, err := run(t, app, "status", "--scope", "wc")
	if err != nil {
		t.Fatalf("status: %v stderr=%q", err, errOut)
	}
	if keys := pulseKeys(out); !slicesEqual(keys, statusKeys) {
		t.Fatalf("key order = %v, want %v\nout=%q", keys, statusKeys, out)
	}
	for _, line := range lines(out) {
		tab := strings.IndexByte(line, '\t')
		if tab != statusKeyWidth {
			t.Errorf("tab at col %d want %d on line %q", tab, statusKeyWidth, line)
		}
	}
	for _, tok := range []string{"duplicate_id:", "parse_error:", "uncommitted:", "lens:"} {
		if strings.Contains(out, tok) {
			t.Errorf("stdout must not carry token %q: %q", tok, out)
		}
	}

	p := parsePulse(out)
	if p["scope"] != "wc" {
		t.Errorf("scope = %q", p["scope"])
	}
	if p["dir"] != dir {
		t.Errorf("dir = %q want %q", p["dir"], dir)
	}
	if p["resolved"] != "flag" {
		t.Errorf("resolved = %q want flag", p["resolved"])
	}
	if p["mode"] != "plain-files" {
		t.Errorf("mode = %q want plain-files", p["mode"])
	}
	if p["lens"] != "" {
		t.Errorf("lens should be empty, got %q", p["lens"])
	}
	if p["me"] != "" {
		t.Errorf("me should be empty, got %q", p["me"])
	}
	if p["total"] != "8" {
		t.Errorf("total = %q want 8 (all parseable tickets, not bare-list only)", p["total"])
	}
	if p["todo"] != "1" || p["review"] != "1" || p["in-progress"] != "1" ||
		p["blocked"] != "1" || p["draft"] != "1" || p["backlog"] != "1" {
		t.Errorf("working-board counts wrong: todo=%s review=%s in-progress=%s blocked=%s draft=%s backlog=%s",
			p["todo"], p["review"], p["in-progress"], p["blocked"], p["draft"], p["backlog"])
	}
	if p["done"] != "1" || p["cancelled"] != "1" {
		t.Errorf("terminal tallies wrong: done=%s cancelled=%s", p["done"], p["cancelled"])
	}
	if p["next"] != "wc-aa22" {
		t.Errorf("next = %q want wc-aa22", p["next"])
	}
	if p["claimed"] != "wc-ac24" {
		t.Errorf("claimed = %q want wc-ac24", p["claimed"])
	}
	if p["blocked_ids"] != "wc-ad25" {
		t.Errorf("blocked_ids = %q want wc-ad25", p["blocked_ids"])
	}
	if p["dangling"] != "0" {
		t.Errorf("dangling = %q want 0", p["dangling"])
	}
	if p["integrity"] != "ok" {
		t.Errorf("integrity = %q want ok", p["integrity"])
	}
	if p["uncommitted"] != "0" {
		t.Errorf("uncommitted = %q want 0", p["uncommitted"])
	}

	listOut, _, err := run(t, app, "list", "--all", "--no-lens", "--scope", "wc")
	if err != nil {
		t.Fatalf("list --all --no-lens: %v", err)
	}
	if got := len(lines(listOut)); got != 8 {
		t.Errorf("list --all --no-lens rows = %d want 8 (must match total)", got)
	}
}

func TestStatusEmptyNextExitsZero(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	// Only a blocked ticket — nothing next-eligible.
	addTicket(t, dir, "wc-aa22", "b", "blocked", "a0", "# B\n", false, "")

	out, _, err := run(t, app, "status", "--scope", "wc")
	if err != nil {
		t.Fatalf("status with empty next must exit 0: %v", err)
	}
	if keys := pulseKeys(out); !slicesEqual(keys, statusKeys) {
		t.Fatalf("must still emit full key block, got %v", keys)
	}
	p := parsePulse(out)
	if p["next"] != "" {
		t.Errorf("next should be empty, got %q", p["next"])
	}
	nextOut, _, nextErr := run(t, app, "next", "--scope", "wc")
	if nextErr == nil || nextOut != "" {
		t.Fatalf("bare next should fail empty-queue, out=%q err=%v", nextOut, nextErr)
	}
}

func TestStatusPositionalsAreUsage(t *testing.T) {
	app := newApp(t)
	initScope(t, app, "wc")
	// At most one optional key; two or more positionals remain usage exit 2.
	out, _, err := run(t, app, "status", "wc-aa22", "blocked")
	if ExitCodeFromError(err) != exitUsage {
		t.Errorf("status with two positionals should exit 2, got %v", err)
	}
	if out != "" {
		t.Errorf("multi-arg refuse must leave stdout empty, got %q", out)
	}
}

func TestStatusAttributeKeyBareValue(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-aa22", "todo", "todo", "a1", "# T\n", false, "")
	addTicket(t, dir, "wc-ac24", "ip", "in-progress", "a3", "# I\n", false, "")

	full, _, err := run(t, app, "status", "--scope", "wc")
	if err != nil {
		t.Fatalf("full status: %v", err)
	}
	want := parsePulse(full)

	// Representative locked keys: bare value matches full pulse; no key name or tab.
	for _, key := range []string{"scope", "mode", "total", "todo", "in-progress", "next", "claimed", "integrity", "uncommitted"} {
		out, _, err := run(t, app, "status", key, "--scope", "wc")
		if err != nil {
			t.Errorf("status %s: %v", key, err)
			continue
		}
		if strings.Contains(out, "\t") {
			t.Errorf("status %s must not emit full-pulse key\\tvalue lines, got %q", key, out)
		}
		if want[key] == "" {
			if out != "" {
				t.Errorf("status %s: empty value should be empty stdout, got %q", key, out)
			}
			continue
		}
		if out != want[key]+"\n" {
			t.Errorf("status %s = %q want %q", key, out, want[key]+"\n")
		}
	}
	if want["mode"] != "plain-files" {
		t.Fatalf("fixture mode = %q want plain-files", want["mode"])
	}
	modeOut, _, err := run(t, app, "status", "mode", "--scope", "wc")
	if err != nil {
		t.Fatalf("status mode: %v", err)
	}
	if modeOut != "plain-files\n" {
		t.Errorf("status mode = %q want plain-files\\n", modeOut)
	}
}

func TestStatusAttributeEmptyValue(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	// Only blocked — next empty; no claimed/in-progress.
	addTicket(t, dir, "wc-aa22", "b", "blocked", "a0", "# B\n", false, "")

	out, _, err := run(t, app, "status", "next", "--scope", "wc")
	if err != nil {
		t.Fatalf("status next empty must exit 0: %v", err)
	}
	if out != "" {
		t.Errorf("empty next must be empty stdout, got %q", out)
	}
	out, _, err = run(t, app, "status", "claimed", "--scope", "wc")
	if err != nil {
		t.Fatalf("status claimed empty: %v", err)
	}
	if out != "" {
		t.Errorf("empty claimed must be empty stdout, got %q", out)
	}
	out, _, err = run(t, app, "status", "lens", "--scope", "wc")
	if err != nil {
		t.Fatalf("status lens empty: %v", err)
	}
	if out != "" {
		t.Errorf("empty lens must be empty stdout, got %q", out)
	}
	// Full pulse still emits every locked key line, including empty next.
	full, _, err := run(t, app, "status", "--scope", "wc")
	if err != nil {
		t.Fatalf("full status: %v", err)
	}
	if keys := pulseKeys(full); !slicesEqual(keys, statusKeys) {
		t.Fatalf("full pulse must still emit every key line, got %v", keys)
	}
	if p := parsePulse(full); p["next"] != "" {
		t.Errorf("full pulse next should be empty, got %q", p["next"])
	}
}

func TestStatusAttributeUnknownKey(t *testing.T) {
	app := newApp(t)
	initScope(t, app, "wc")

	out, _, err := run(t, app, "status", "nope", "--scope", "wc")
	if ExitCodeFromError(err) != exitUsage {
		t.Fatalf("unknown key exit = %v want 2", err)
	}
	if out != "" {
		t.Errorf("unknown key must leave stdout empty, got %q", out)
	}
	msg := err.Error()
	if !strings.Contains(msg, `unknown status key "nope"`) {
		t.Errorf("message should name bad key, got %q", msg)
	}
	for _, k := range statusKeys {
		if !strings.Contains(msg, k) {
			t.Errorf("catalogue missing %q in %q", k, msg)
		}
	}
}

func TestStatusAttributeKeepsStderrDiagnostics(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-aa22", "fe", "todo", "a0", "# FE\n", false, "tags: [frontend]\n")
	addTicket(t, dir, "wc-ab23", "be", "todo", "a1", "# BE\n", false, "tags: [backend]\n")

	if _, _, err := run(t, app, "lens", "frontend", "--scope", "wc"); err != nil {
		t.Fatalf("lens: %v", err)
	}
	out, errOut, err := run(t, app, "status", "next", "--scope", "wc")
	if err != nil {
		t.Fatalf("status next: %v", err)
	}
	if !strings.Contains(errOut, "lens:") {
		t.Errorf("attribute path must still echo lens on stderr, got %q", errOut)
	}
	if out != "wc-aa22\n" {
		t.Errorf("status next under lens = %q want wc-aa22\\n", out)
	}
	if strings.Contains(out, "\t") {
		t.Errorf("attribute stdout must not be full pulse, got %q", out)
	}
}

func TestStatusLensFiltersWorkingBoard(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-aa22", "fe", "todo", "a0", "# FE\n", false, "tags: [frontend]\n")
	addTicket(t, dir, "wc-ab23", "be", "todo", "a1", "# BE\n", false, "tags: [backend]\n")
	addTicket(t, dir, "wc-ac24", "ip", "in-progress", "a2", "# IP\n", false, "tags: [backend]\n")
	addTicket(t, dir, "wc-ad25", "bl", "blocked", "a3", "# BL\n", false, "tags: [frontend]\n")
	addTicket(t, dir, "wc-ae26", "done", "done", "a4", "# D\n", true, "tags: [backend]\n")

	if _, _, err := run(t, app, "lens", "frontend", "--scope", "wc"); err != nil {
		t.Fatalf("lens: %v", err)
	}
	out, errOut, err := run(t, app, "status", "--scope", "wc")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(errOut, "lens:") {
		t.Errorf("active lens should echo on stderr, got %q", errOut)
	}
	p := parsePulse(out)
	if p["lens"] != "frontend" {
		t.Errorf("lens field = %q", p["lens"])
	}
	if p["todo"] != "1" || p["blocked"] != "1" || p["in-progress"] != "0" {
		t.Errorf("lens counts: todo=%s blocked=%s in-progress=%s", p["todo"], p["blocked"], p["in-progress"])
	}
	if p["total"] != "5" {
		t.Errorf("total ignores lens = %q want 5 (full scope)", p["total"])
	}
	if p["next"] != "wc-aa22" {
		t.Errorf("next under lens = %q want wc-aa22", p["next"])
	}
	if p["claimed"] != "" {
		t.Errorf("claimed should be empty under lens (backend in-progress filtered), got %q", p["claimed"])
	}
	if p["blocked_ids"] != "wc-ad25" {
		t.Errorf("blocked_ids = %q", p["blocked_ids"])
	}
	if p["done"] != "1" {
		t.Errorf("done should ignore lens, got %q", p["done"])
	}

	nextOut, _, err := run(t, app, "next", "--scope", "wc")
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if !strings.Contains(nextOut, "wc-aa22") {
		t.Errorf("tk next should match status next, got %q", nextOut)
	}
}

func TestStatusNextUsesReconcileClosure(t *testing.T) {
	app := newApp(t)
	up := initScope(t, app, "up")
	wc := initScope(t, app, "wc")
	// Ambient wc depends on up; only after up is terminal is wc next-eligible.
	addTicket(t, up, "up-aa22", "core", "done", "a0", "# Core\n", true, "")
	addTicket(t, wc, "wc-bb22", "feat", "todo", "a0", "# Feature\n", false, "depends: [up-aa22]\n")

	out, _, err := run(t, app, "status", "--scope", "wc")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	p := parsePulse(out)
	if p["next"] != "wc-bb22" {
		t.Errorf("next via closure = %q want wc-bb22", p["next"])
	}
	nextOut, _, err := run(t, app, "next", "--scope", "wc")
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if !strings.Contains(nextOut, "wc-bb22") {
		t.Errorf("tk next should agree, got %q", nextOut)
	}
}

func TestStatusNextTokensMatchBareNext(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-aa22", "ready", "todo", "a0", "# Ready\n", false, "")
	addTicket(t, dir, "wc-ab23", "held", "todo", "a1", "# Held\n", false, "depends: [wc-zz99]\n")

	_, statusErr, err := run(t, app, "status", "--scope", "wc")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	_, nextErr, err := run(t, app, "next", "--scope", "wc")
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if !strings.Contains(statusErr, "depends_dangling:") {
		t.Errorf("status must walk past the chosen next and emit later tokens, stderr=%q", statusErr)
	}
	if !strings.Contains(nextErr, "depends_dangling:") {
		t.Errorf("next baseline missing depends_dangling, stderr=%q", nextErr)
	}
	statusToks := tokenLines(statusErr)
	nextToks := tokenLines(nextErr)
	if !slicesEqual(statusToks, nextToks) {
		t.Errorf("status tokens %v != next tokens %v", statusToks, nextToks)
	}
}

func tokenLines(errOut string) []string {
	var out []string
	for _, line := range lines(errOut) {
		if i := strings.IndexByte(line, ':'); i > 0 && !strings.Contains(line[:i], " ") {
			out = append(out, line)
		}
	}
	return out
}

func TestStatusDanglingEdgeCount(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-aa22", "a", "todo", "a0", "# A\n", false, "depends: [wc-zz99]\n")
	addTicket(t, dir, "wc-ab23", "b", "todo", "a1", "# B\n", false, "depends: [wc-zz99]\n")
	addTicket(t, dir, "wc-ac24", "c", "todo", "a2", "# C\n", false, "depends: [other-xx00]\n")

	out, _, err := run(t, app, "status", "--scope", "wc")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	p := parsePulse(out)
	if p["dangling"] != "2" {
		t.Errorf("dangling = %q want 2", p["dangling"])
	}
}

func TestStatusIntegrityAmbientOnly(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	other := initScope(t, app, "ot")
	addTicket(t, dir, "wc-aa22", "ok", "todo", "a0", "# Ok\n", false, "depends: [ot-bb22]\n")
	addTicket(t, other, "ot-bb22", "one", "done", "a0", "# One\n", true, "")
	addTicket(t, other, "ot-bb22", "two", "done", "a1", "# Two\n", true, "")

	out, _, err := run(t, app, "status", "--scope", "wc")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	p := parsePulse(out)
	if p["integrity"] != "ok" {
		t.Errorf("depended-on duplicate must not flip ambient integrity, got %q", p["integrity"])
	}

	addTicket(t, dir, "wc-aa22", "dup", "todo", "a2", "# Dup\n", false, "")
	out, _, err = run(t, app, "status", "--scope", "wc")
	if err != nil {
		t.Fatalf("status after ambient dup: %v", err)
	}
	p = parsePulse(out)
	if p["integrity"] != "issues" {
		t.Errorf("ambient duplicate_id should flip integrity to issues, got %q", p["integrity"])
	}
}

func TestStatusIntegrityHardClasses(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")

	bad := "---\nid: wc-aa22\nstatus: [unterminated\n---\n# broke\n"
	if err := os.WriteFile(filepath.Join(dir, "wc-aa22-x.md"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, err := run(t, app, "status", "--scope", "wc")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if parsePulse(out)["integrity"] != "issues" {
		t.Errorf("parse_error should flip integrity, got %q", parsePulse(out)["integrity"])
	}
	if err := os.Remove(filepath.Join(dir, "wc-aa22-x.md")); err != nil {
		t.Fatal(err)
	}

	addTicket(t, dir, "wc-ab23", "a", "todo", "a0", "# A\n", false, "")
	addTicket(t, dir, "wc-ac24", "b", "todo", "a0", "# B\n", false, "")
	out, _, err = run(t, app, "status", "--scope", "wc")
	if err != nil {
		t.Fatalf("status equal_order: %v", err)
	}
	if parsePulse(out)["integrity"] != "issues" {
		t.Errorf("equal_order should flip integrity, got %q", parsePulse(out)["integrity"])
	}
	if err := os.Remove(filepath.Join(dir, "wc-ab23-a.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "wc-ac24-b.md")); err != nil {
		t.Fatal(err)
	}

	addTicket(t, dir, "wc-ad25", "done", "done", "a1", "# Done\n", false, "")
	out, _, err = run(t, app, "status", "--scope", "wc")
	if err != nil {
		t.Fatalf("status archive drift: %v", err)
	}
	if parsePulse(out)["integrity"] != "issues" {
		t.Errorf("archive_terminal_at_root should flip integrity, got %q", parsePulse(out)["integrity"])
	}
	if err := os.Remove(filepath.Join(dir, "wc-ad25-done.md")); err != nil {
		t.Fatal(err)
	}

	addTicket(t, dir, "wc-ae26", "todo", "todo", "a2", "# Todo\n", true, "")
	out, _, err = run(t, app, "status", "--scope", "wc")
	if err != nil {
		t.Fatalf("status archive_non_terminal: %v", err)
	}
	if parsePulse(out)["integrity"] != "issues" {
		t.Errorf("archive_non_terminal should flip integrity, got %q", parsePulse(out)["integrity"])
	}
}

func TestStatusIntegrityIgnoresSoftSchemaWarn(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	// Self-related is a soft doctor schema_warn class, not a post-reconcile integrity class.
	addTicket(t, dir, "wc-aa22", "t", "todo", "a0", "# T\n", false, "related: [wc-aa22]\n")

	out, _, err := run(t, app, "status", "--scope", "wc")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if parsePulse(out)["integrity"] != "ok" {
		t.Errorf("soft schema_warn class alone must leave integrity ok, got %q", parsePulse(out)["integrity"])
	}
	// Same fixture: doctor must surface schema_warn so the soft class is real, not assumed.
	t.Setenv("TK_SCOPE", "wc")
	docOut, _, err := run(t, app, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if !strings.Contains(docOut, "schema_warn:") {
		t.Fatalf("fixture must produce doctor schema_warn, got %q", docOut)
	}
}

func TestStatusTotalIncludesBacklogAndCustom(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	cue := "name: \"wc\"\nautoCommit: false\nstatuses: {\n  polishing: {category: \"active\"}\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "tk.cue"), []byte(cue), 0o644); err != nil {
		t.Fatal(err)
	}
	addTicket(t, dir, "wc-aa22", "p", "polishing", "a0", "# P\n", false, "")
	addTicket(t, dir, "wc-ab23", "b", "backlog", "a1", "# B\n", false, "")

	out, _, err := run(t, app, "status", "--scope", "wc")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	p := parsePulse(out)
	if p["total"] != "2" {
		t.Errorf("total = %q want 2 (custom active + backlog)", p["total"])
	}
	if p["backlog"] != "1" {
		t.Errorf("backlog = %q want 1", p["backlog"])
	}
	listOut, _, err := run(t, app, "list", "--scope", "wc")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if got := len(lines(listOut)); got != 1 {
		t.Errorf("bare list rows = %d want 1 (still default-list only)", got)
	}
}

func TestStatusClaimedSorted(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	// Higher order first on disk; claimed must sort (order, id).
	addTicket(t, dir, "wc-zz99", "late", "in-progress", "a2", "# Late\n", false, "")
	addTicket(t, dir, "wc-aa22", "early", "in-progress", "a0", "# Early\n", false, "")
	addTicket(t, dir, "wc-ab23", "mid", "in-progress", "a1", "# Mid\n", false, "")

	out, _, err := run(t, app, "status", "--scope", "wc")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if got := parsePulse(out)["claimed"]; got != "wc-aa22 wc-ab23 wc-zz99" {
		t.Errorf("claimed sort = %q want order then id", got)
	}
}

func TestStatusLensEmptiedNextExitsZero(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	// Only backend-tagged todos; frontend lens empties the ready queue.
	addTicket(t, dir, "wc-aa22", "be", "todo", "a0", "# BE\n", false, "tags: [backend]\n")
	if _, _, err := run(t, app, "lens", "frontend", "--scope", "wc"); err != nil {
		t.Fatalf("lens: %v", err)
	}

	out, _, err := run(t, app, "status", "--scope", "wc")
	if err != nil {
		t.Fatalf("status lens-empty next must exit 0: %v", err)
	}
	if keys := pulseKeys(out); !slicesEqual(keys, statusKeys) {
		t.Fatalf("full key block required, got %v", keys)
	}
	if parsePulse(out)["next"] != "" {
		t.Errorf("next under emptied lens should be empty, got %q", parsePulse(out)["next"])
	}
	if _, _, nextErr := run(t, app, "next", "--scope", "wc"); nextErr == nil {
		t.Error("bare next should fail when lens empties the queue")
	}
}

func TestStatusModeUnparseableIsPlainFiles(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-aa22", "t", "todo", "a0", "# T\n", false, "")
	bad := "name: \"wc\"\nautoCommit: false\nfields: {x: {type: \"float\"}}\n"
	if err := os.WriteFile(filepath.Join(dir, "tk.cue"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}

	out, errOut, err := run(t, app, "status", "--scope", "wc")
	if err != nil {
		t.Fatalf("status under unusable config should still pulse: %v", err)
	}
	p := parsePulse(out)
	if p["mode"] != "plain-files" {
		t.Errorf("unparseable schema mode = %q want plain-files", p["mode"])
	}
	if p["uncommitted"] != "0" {
		t.Errorf("unparseable must not surface uncommitted, got %q", p["uncommitted"])
	}
	if !strings.Contains(errOut, "config_unparseable:") {
		t.Errorf("expected config_unparseable on stderr, got %q", errOut)
	}
}

func TestStatusModeTkDrivenUncommittedZero(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	dir, _ := initGitScope(t, app, "wc", true)
	addTicket(t, dir, "wc-aa22", "t", "todo", "a0", "# T\n", false, "")
	if err := os.WriteFile(filepath.Join(dir, "tk.cue"),
		[]byte("name: \"wc\"\nautoCommit: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, _, err := run(t, app, "status", "--scope", "wc")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	p := parsePulse(out)
	if p["mode"] != "tk-driven" {
		t.Errorf("mode = %q want tk-driven", p["mode"])
	}
	if p["uncommitted"] != "0" {
		t.Errorf("tk-driven uncommitted must be 0, got %q", p["uncommitted"])
	}
}

func TestStatusModeTkDrivenPlanned(t *testing.T) {
	app := newApp(t)
	dir := filepath.Join(t.TempDir(), "wc")
	if _, _, err := run(t, app, "scope", "init", dir, "--name", "wc", "--auto-commit"); err != nil {
		t.Fatalf("init planned auto-commit: %v", err)
	}
	addTicket(t, dir, "wc-aa22", "t", "todo", "a0", "# T\n", false, "")

	out, _, err := run(t, app, "status", "--scope", "wc")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	p := parsePulse(out)
	if p["mode"] != "tk-driven" {
		t.Errorf("planned auto-commit mode = %q want tk-driven", p["mode"])
	}
	if p["uncommitted"] != "0" {
		t.Errorf("planned tk-driven uncommitted must be 0, got %q", p["uncommitted"])
	}
}

func TestStatusModeRepoDrivenDirtyCount(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	dir, _ := initGitScope(t, app, "rd", false)
	addTicket(t, dir, "rd-aa22", "t", "todo", "a0", "# T\n", false, "")

	out, _, err := run(t, app, "status", "--scope", "rd")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	p := parsePulse(out)
	if p["mode"] != "repo-driven" {
		t.Errorf("mode = %q want repo-driven", p["mode"])
	}
	if p["uncommitted"] == "0" {
		t.Errorf("repo-driven dirty board should report uncommitted > 0, got %q", p["uncommitted"])
	}
}

func TestStatusResolvedSources(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-aa22", "t", "todo", "a0", "# T\n", false, "")

	out, _, err := run(t, app, "status", "--scope", "wc")
	if err != nil {
		t.Fatal(err)
	}
	if parsePulse(out)["resolved"] != "flag" {
		t.Errorf("want flag, got %q", parsePulse(out)["resolved"])
	}

	t.Setenv("TK_SCOPE", "wc")
	out, _, err = run(t, app, "status")
	if err != nil {
		t.Fatal(err)
	}
	if parsePulse(out)["resolved"] != "env" {
		t.Errorf("want env, got %q", parsePulse(out)["resolved"])
	}

	t.Setenv("TK_SCOPE", "")
	t.Chdir(dir)
	out, _, err = run(t, app, "status")
	if err != nil {
		t.Fatalf("status via cwd: %v", err)
	}
	if parsePulse(out)["resolved"] != "cwd" {
		t.Errorf("want cwd, got %q", parsePulse(out)["resolved"])
	}
}
