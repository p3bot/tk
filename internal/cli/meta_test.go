package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/token"
)

func runIn(t *testing.T, app *App, stdin string, args ...string) (string, string, error) {
	t.Helper()
	root := newRootCmd(app)
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), errb.String(), err
}

func writeScopeFields(t *testing.T, dir, name string, fieldsBlock string) {
	t.Helper()
	content := "name: \"" + name + "\"\nautoCommit: false\n"
	if fieldsBlock != "" {
		content += "fields: {\n" + fieldsBlock + "}\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "tk.cue"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fileSnapshot(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestMetaGetFullAndSingleKey(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "network", "todo", "a0", "# Network redesign\n\nbody", false,
		"depends: [wc-de34]\nsummary: short one\ntags: [a, b]\nstatus_conflict: [todo, done]\n")
	addTicket(t, dir, "wc-de34", "auth", "todo", "a1", "# Auth\n", false, "")

	path := filepath.Join(dir, "wc-ab2c-network.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	wantLines, wantWords, wantChars := countFileText(raw)

	out, _, err := run(t, app, "meta", "get", "wc-ab2c")
	if err != nil {
		t.Fatalf("meta get: %v", err)
	}
	if !strings.HasPrefix(out, "title: Network redesign\npath: ") {
		t.Errorf("preamble wrong: %q", out)
	}
	preamble := out
	if i := strings.Index(out, "\n\n"); i >= 0 {
		preamble = out[:i]
	}
	if strings.Contains(preamble, "id:") {
		t.Errorf("preamble must not include id:: %q", preamble)
	}
	for _, key := range []string{"lines:", "words:", "characters:"} {
		if !strings.Contains(preamble, key) {
			t.Errorf("preamble missing %s: %q", key, preamble)
		}
	}
	// Order locked: title, path, lines, words, characters.
	wantOrder := []string{"title:", "path:", "lines:", "words:", "characters:"}
	pos := 0
	for _, key := range wantOrder {
		i := strings.Index(preamble[pos:], key)
		if i < 0 {
			t.Fatalf("preamble key order: missing %s after pos %d in %q", key, pos, preamble)
		}
		pos += i + len(key)
	}
	// preamble is cut before the blank line, so the last field has no trailing
	// newline; search in preamble+"\n" so every key uses an exact "key: N\n"
	// match and digit prefixes cannot false-pass (e.g. characters: 5 vs 50).
	preambleNL := preamble + "\n"
	if !strings.Contains(preambleNL, "lines: "+strconv.Itoa(wantLines)+"\n") ||
		!strings.Contains(preambleNL, "words: "+strconv.Itoa(wantWords)+"\n") ||
		!strings.Contains(preambleNL, "characters: "+strconv.Itoa(wantChars)+"\n") {
		t.Errorf("preamble counts wrong: %q (want lines=%d words=%d characters=%d)",
			preamble, wantLines, wantWords, wantChars)
	}
	if !strings.Contains(out, "summary: short one") {
		t.Errorf("raw interior missing summary: %q", out)
	}

	out, _, err = run(t, app, "meta", "get", "wc-ab2c", "summary")
	if err != nil {
		t.Fatalf("meta get summary: %v", err)
	}
	if out != "short one\n" {
		t.Errorf("summary get = %q", out)
	}

	out, _, err = run(t, app, "meta", "get", "wc-ab2c", "depends")
	if err != nil {
		t.Fatalf("meta get depends: %v", err)
	}
	if out != "wc-de34\n" {
		t.Errorf("depends get = %q", out)
	}

	out, _, err = run(t, app, "meta", "get", "wc-ab2c", "tags")
	if err != nil {
		t.Fatalf("meta get tags: %v", err)
	}
	if out != "a\nb\n" {
		t.Errorf("tags get = %q", out)
	}

	for _, tc := range []struct {
		key, want string
	}{
		{"id", "wc-ab2c\n"},
		{"status", "todo\n"},
		{"order", "a0\n"},
		{"created", "2026-01-01T00:00:00Z\n"},
		{"status_conflict", "todo\ndone\n"},
	} {
		got, _, err := run(t, app, "meta", "get", "wc-ab2c", tc.key)
		if err != nil {
			t.Errorf("get %s: %v", tc.key, err)
			continue
		}
		if got != tc.want {
			t.Errorf("get %s = %q want %q", tc.key, got, tc.want)
		}
	}

	// Absent key: empty stdout, exit 0.
	out, _, err = run(t, app, "meta", "get", "wc-de34", "summary")
	if err != nil {
		t.Fatalf("absent summary: %v", err)
	}
	if out != "" {
		t.Errorf("absent key must be empty stdout, got %q", out)
	}
}

func TestMetaGetUnknownKeyListsCatalogue(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	writeScopeFields(t, dir, "wc", "estimate: {type: \"int\"}\narea: {type: \"string\"}\n")
	addTicket(t, dir, "wc-ab2c", "network", "todo", "a0", "# Network\n", false, "")

	_, _, err := run(t, app, "meta", "get", "wc-ab2c", "nope")
	if ExitCodeFromError(err) != exitUsage {
		t.Fatalf("unknown key exit = %v want 2", err)
	}
	msg := err.Error()
	for _, k := range []string{"id", "status", "order", "depends", "related", "tags", "created", "links", "summary", "status_conflict", "area", "estimate"} {
		if !strings.Contains(msg, k) {
			t.Errorf("catalogue missing %q in %q", k, msg)
		}
	}
	// Customs sorted: area before estimate.
	if ai, ei := strings.Index(msg, "area"), strings.Index(msg, "estimate"); ai < 0 || ei < 0 || ai > ei {
		t.Errorf("customs should be sorted ascending in catalogue: %q", msg)
	}
}

func TestMetaGetParseErrorSingleKey(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	bad := "---\nid: wc-ab2c\nstatus: [unterminated\n---\n# Broken\n"
	if err := os.WriteFile(filepath.Join(dir, "wc-ab2c-broken.md"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}

	out, errOut, err := run(t, app, "meta", "get", "wc-ab2c")
	if err != nil {
		t.Fatalf("full get on extractable broken FM should exit 0: %v", err)
	}
	if !strings.Contains(out, "title:") || !strings.Contains(out, "path:") {
		t.Errorf("full get preamble missing: %q", out)
	}
	if !strings.Contains(errOut, "parse_error:") {
		t.Errorf("full get should ride parse_error, got %q", errOut)
	}

	// Single-key get refuses: non-zero, empty stdout, parse_error token.
	out, errOut, err = run(t, app, "meta", "get", "wc-ab2c", "summary")
	if ExitCodeFromError(err) == exitOK {
		t.Fatal("single-key get on unparseable model must be non-zero")
	}
	if out != "" {
		t.Errorf("single-key get must print no stdout, got %q", out)
	}
	if !strings.Contains(err.Error()+errOut, "parse_error:") {
		t.Errorf("expected parse_error token, got err=%v stderr=%q", err, errOut)
	}
}

func TestMetaSetSummaryArgvAndStdin(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "network", "todo", "a0", "# Network\n", false, "")

	out, _, err := run(t, app, "meta", "set", "wc-ab2c", "summary", "one line")
	if err != nil {
		t.Fatalf("set summary: %v", err)
	}
	path := strings.TrimSpace(out)
	if !filepath.IsAbs(path) || !strings.HasSuffix(path, "wc-ab2c-network.md") {
		t.Errorf("set should print absolute path, got %q", path)
	}
	if got := fmValue(t, path, "summary"); got != "one line" {
		t.Errorf("summary = %q", got)
	}

	out, _, err = runIn(t, app, "from stdin\n", "meta", "set", "wc-ab2c", "summary", "-")
	if err != nil {
		t.Fatalf("set summary stdin: %v", err)
	}
	if got := fmValue(t, strings.TrimSpace(out), "summary"); got != "from stdin" {
		t.Errorf("stdin summary = %q", got)
	}

	out, _, err = runIn(t, app, "crlf value\r\n", "meta", "set", "wc-ab2c", "summary", "-")
	if err != nil {
		t.Fatalf("set summary stdin CRLF: %v", err)
	}
	if got := fmValue(t, strings.TrimSpace(out), "summary"); got != "crlf value" {
		t.Errorf("CRLF stdin summary = %q", got)
	}

	out, _, err = run(t, app, "meta", "set", "wc-ab2c", "summary", "")
	if err != nil {
		t.Fatalf("clear summary: %v", err)
	}
	data, _ := os.ReadFile(strings.TrimSpace(out))
	if strings.Contains(string(data), "summary:") {
		t.Errorf("cleared summary should omit key: %s", data)
	}

	// Embedded newlines refused.
	_, _, err = run(t, app, "meta", "set", "wc-ab2c", "summary", "a\nb")
	if ExitCodeFromError(err) != exitUsage {
		t.Errorf("embedded newline should exit 2, got %v", err)
	}
	_, _, err = runIn(t, app, "line1\nline2\n", "meta", "set", "wc-ab2c", "summary", "-")
	if ExitCodeFromError(err) != exitUsage {
		t.Errorf("stdin embedded newlines should exit 2, got %v", err)
	}
}

func TestMetaCustomScalarAndEnum(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	writeScopeFields(t, dir, "wc", `
estimate: {type: "int"}
blocking: {type: "bool"}
area: {type: "string", values: ["frontend", "backend"]}
owners: {type: "strings", values: ["platform", "design"]}
`)
	addTicket(t, dir, "wc-ab2c", "network", "todo", "a0", "# Network\n", false, "")

	path, _, err := run(t, app, "meta", "set", "wc-ab2c", "estimate", "5")
	if err != nil {
		t.Fatalf("set estimate: %v", err)
	}
	path = strings.TrimSpace(path)
	if got := fmValue(t, path, "estimate"); got != "5" {
		t.Errorf("estimate = %q", got)
	}
	if _, _, err := run(t, app, "meta", "set", "wc-ab2c", "blocking", "true"); err != nil {
		t.Fatalf("set blocking: %v", err)
	}
	if got := fmValue(t, path, "blocking"); got != "true" {
		t.Errorf("blocking = %q", got)
	}
	if _, _, err := run(t, app, "meta", "set", "wc-ab2c", "area", "frontend"); err != nil {
		t.Fatalf("set area: %v", err)
	}
	if _, _, err := run(t, app, "meta", "set", "wc-ab2c", "estimate", ""); err != nil {
		t.Fatalf("clear estimate: %v", err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "estimate:") {
		t.Errorf("cleared estimate should omit key: %s", data)
	}
	if _, _, err := run(t, app, "meta", "set", "wc-ab2c", "blocking", ""); err != nil {
		t.Fatalf("clear blocking: %v", err)
	}
	// Enum refuse.
	if _, _, err := run(t, app, "meta", "set", "wc-ab2c", "area", "mobile"); ExitCodeFromError(err) != exitUsage {
		t.Errorf("enum violation should exit 2, got %v", err)
	}
	if _, _, err := run(t, app, "meta", "set", "wc-ab2c", "estimate", "nope"); ExitCodeFromError(err) != exitUsage {
		t.Errorf("bad int should exit 2, got %v", err)
	}
	if _, _, err := run(t, app, "meta", "add", "wc-ab2c", "owners", "platform"); err != nil {
		t.Fatalf("add owners: %v", err)
	}
	out, _, err := run(t, app, "meta", "get", "wc-ab2c", "owners")
	if err != nil || out != "platform\n" {
		t.Errorf("owners get = %q err=%v", out, err)
	}
	if _, _, err := run(t, app, "meta", "add", "wc-ab2c", "owners", "ops"); ExitCodeFromError(err) != exitUsage {
		t.Errorf("enum strings add should exit 2, got %v", err)
	}
	// Undeclared key refuse.
	if _, _, err := run(t, app, "meta", "set", "wc-ab2c", "undeclared", "x"); ExitCodeFromError(err) != exitUsage {
		t.Errorf("undeclared key should exit 2, got %v", err)
	}
}

func TestMetaAddRmDepends(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "network", "todo", "a0", "# Network\n", false, "")
	addTicket(t, dir, "wc-de34", "auth", "todo", "a1", "# Auth\n", false, "")
	path := filepath.Join(dir, "wc-ab2c-network.md")

	out, _, err := run(t, app, "meta", "add", "wc-ab2c", "depends", "wc-de34")
	if err != nil {
		t.Fatalf("add depends: %v", err)
	}
	if !strings.HasSuffix(strings.TrimSpace(out), "wc-ab2c-network.md") {
		t.Errorf("add should print ticket path, got %q", out)
	}
	// Idempotent.
	if _, _, err := run(t, app, "meta", "add", "wc-ab2c", "depends", "wc-de34"); err != nil {
		t.Fatalf("idempotent add: %v", err)
	}
	depOut, _, _ := run(t, app, "meta", "get", "wc-ab2c", "depends")
	if depOut != "wc-de34\n" {
		t.Errorf("depends after double add = %q", depOut)
	}

	depsOut, _, err := run(t, app, "deps", "wc-ab2c")
	if err != nil {
		t.Fatalf("deps after add: %v", err)
	}
	if !strings.Contains(depsOut, "depends on:\n  wc-de34") {
		t.Errorf("deps should show the new edge, got %q", depsOut)
	}
	listOut, _, err := run(t, app, "list", "--scope", "wc")
	if err != nil {
		t.Fatalf("list after add: %v", err)
	}
	if !strings.Contains(listOut, "wc-ab2c\ttodo\tNetwork\twc-de34") {
		t.Errorf("list waiting-on should carry depends after write-through, got %q", listOut)
	}

	before := fileSnapshot(t, path)

	// Self refuse: no write.
	_, errOut, err := run(t, app, "meta", "add", "wc-ab2c", "depends", "wc-ab2c")
	if ExitCodeFromError(err) == exitOK {
		t.Fatal("self depends must refuse")
	}
	if !strings.Contains(err.Error()+errOut, "depends_self:") {
		t.Errorf("expected depends_self:, got err=%v stderr=%q", err, errOut)
	}
	if fileSnapshot(t, path) != before {
		t.Error("self refuse must not rewrite the file")
	}

	_, errOut, err = run(t, app, "meta", "add", "wc-ab2c", "depends", "wc-zz99")
	if ExitCodeFromError(err) == exitOK {
		t.Fatal("missing depends must refuse")
	}
	if !strings.Contains(err.Error()+errOut, "depends_dangling:") {
		t.Errorf("expected depends_dangling:, got err=%v stderr=%q", err, errOut)
	}
	if fileSnapshot(t, path) != before {
		t.Error("dangling refuse must not rewrite the file")
	}

	// Same-scope target present only as parse_error quarantine → depends_dangling.
	qdir := initScope(t, app, "qq")
	addTicket(t, qdir, "qq-aa22", "subj", "todo", "a0", "# Subj\n", false, "")
	bad := "---\nid: qq-bb33\nstatus: [unterminated\n---\n# Quarantined\n"
	if err := os.WriteFile(filepath.Join(qdir, "qq-bb33-q.md"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	qpath := filepath.Join(qdir, "qq-aa22-subj.md")
	qbefore := fileSnapshot(t, qpath)
	_, errOut, err = run(t, app, "meta", "add", "qq-aa22", "depends", "qq-bb33")
	if ExitCodeFromError(err) == exitOK {
		t.Fatal("quarantined-only depends target must refuse")
	}
	if !strings.Contains(err.Error()+errOut, "depends_dangling:") {
		t.Errorf("expected depends_dangling: for quarantined-only target, got err=%v stderr=%q", err, errOut)
	}
	if fileSnapshot(t, qpath) != qbefore {
		t.Error("quarantined-target refuse must not rewrite the subject file")
	}

	// Malformed target → exit 2, no integrity token; no write.
	_, errOut, err = run(t, app, "meta", "add", "wc-ab2c", "depends", "bad!")
	if ExitCodeFromError(err) != exitUsage {
		t.Errorf("malformed target exit = %v want 2", err)
	}
	if strings.Contains(err.Error()+errOut, "depends_") {
		t.Errorf("malformed must not carry integrity token: %v / %q", err, errOut)
	}
	if fileSnapshot(t, path) != before {
		t.Error("malformed refuse must not rewrite the file")
	}

	if _, _, err := run(t, app, "meta", "rm", "wc-ab2c", "depends", "de34"); err != nil {
		t.Fatalf("rm by short: %v", err)
	}
	depOut, _, _ = run(t, app, "meta", "get", "wc-ab2c", "depends")
	if depOut != "" {
		t.Errorf("depends after rm by short = %q", depOut)
	}
	// Idempotent rm absent.
	if _, _, err := run(t, app, "meta", "rm", "wc-ab2c", "depends", "wc-de34"); err != nil {
		t.Fatalf("rm absent: %v", err)
	}
	if _, _, err := run(t, app, "meta", "add", "wc-ab2c", "depends", "wc-de34"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, app, "meta", "remove", "wc-ab2c", "depends", "wc-de34"); err != nil {
		t.Fatalf("remove alias: %v", err)
	}
}

func TestMetaCrossScopeDepends(t *testing.T) {
	app := newApp(t)
	up := initScope(t, app, "up")
	wc := initScope(t, app, "wc")
	addTicket(t, up, "up-aa22", "core", "todo", "a0", "# Core\n", false, "")
	addTicket(t, wc, "wc-bb22", "feat", "todo", "a0", "# Feature\n", false, "")

	// Target exists in registered scope after reconcile.
	if _, _, err := run(t, app, "meta", "add", "wc-bb22", "depends", "up-aa22"); err != nil {
		t.Fatalf("cross-scope add: %v", err)
	}
	out, _, _ := run(t, app, "meta", "get", "wc-bb22", "depends")
	if out != "up-aa22\n" {
		t.Errorf("cross-scope depends = %q", out)
	}

	_, errOut, err := run(t, app, "meta", "add", "wc-bb22", "depends", "zzz-zz99")
	if ExitCodeFromError(err) == exitOK {
		t.Fatal("unregistered scope depends must refuse")
	}
	if !strings.Contains(err.Error()+errOut, "depends_unresolvable:") {
		t.Errorf("expected depends_unresolvable:, got err=%v stderr=%q", err, errOut)
	}

	_, errOut, err = run(t, app, "meta", "add", "wc-bb22", "depends", "up-zz99")
	if ExitCodeFromError(err) == exitOK {
		t.Fatal("missing cross-scope id must refuse")
	}
	if !strings.Contains(err.Error()+errOut, "depends_unresolvable:") {
		t.Errorf("expected depends_unresolvable:, got err=%v stderr=%q", err, errOut)
	}

	wcPath := filepath.Join(wc, "wc-bb22-feat.md")
	// Drop the successful cross-scope edge so the file is clean for the snapshot.
	if _, _, err := run(t, app, "meta", "rm", "wc-bb22", "depends", "up-aa22"); err != nil {
		t.Fatalf("rm cross edge: %v", err)
	}
	before := fileSnapshot(t, wcPath)
	if err := os.RemoveAll(up); err != nil {
		t.Fatal(err)
	}
	_, errOut, err = run(t, app, "meta", "add", "wc-bb22", "depends", "up-aa22")
	if ExitCodeFromError(err) == exitOK {
		t.Fatal("unreachable cross-scope depends must refuse")
	}
	if !strings.Contains(err.Error()+errOut, "depends_unresolvable:") {
		t.Errorf("expected depends_unresolvable: for unreachable target, got err=%v stderr=%q", err, errOut)
	}
	if fileSnapshot(t, wcPath) != before {
		t.Error("unreachable-target refuse must not rewrite the subject file")
	}
}

func TestMetaRelatedAndTags(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "network", "todo", "a0", "# Network\n", false, "")
	addTicket(t, dir, "wc-de34", "auth", "todo", "a1", "# Auth\n", false, "")

	if _, _, err := run(t, app, "meta", "add", "wc-ab2c", "related", "de34"); err != nil {
		t.Fatalf("related short: %v", err)
	}
	out, _, _ := run(t, app, "meta", "get", "wc-ab2c", "related")
	if out != "wc-de34\n" {
		t.Errorf("related stored full = %q", out)
	}
	if _, _, err := run(t, app, "meta", "add", "wc-ab2c", "related", "ab2c"); err != nil {
		t.Fatalf("self-related should not hard-refuse: %v", err)
	}
	if _, _, err := run(t, app, "meta", "add", "wc-ab2c", "related", "wc-zz99"); err != nil {
		t.Fatalf("missing related should not refuse: %v", err)
	}
	if _, _, err := run(t, app, "meta", "rm", "wc-ab2c", "related", "de34"); err != nil {
		t.Fatalf("rm related short: %v", err)
	}

	// tags idempotent add / absent rm.
	if _, _, err := run(t, app, "meta", "add", "wc-ab2c", "tags", "frontend"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, app, "meta", "add", "wc-ab2c", "tags", "frontend"); err != nil {
		t.Fatal(err)
	}
	out, _, _ = run(t, app, "meta", "get", "wc-ab2c", "tags")
	if out != "frontend\n" {
		t.Errorf("tags after double add = %q", out)
	}
	if _, _, err := run(t, app, "meta", "rm", "wc-ab2c", "tags", "missing"); err != nil {
		t.Fatalf("rm absent tag: %v", err)
	}

	// tag is a CLI alias for the wire key tags.
	if _, _, err := run(t, app, "meta", "add", "wc-ab2c", "tag", "style"); err != nil {
		t.Fatalf("add tag alias: %v", err)
	}
	out, _, err := run(t, app, "meta", "get", "wc-ab2c", "tag")
	if err != nil {
		t.Fatalf("get tag alias: %v", err)
	}
	if out != "frontend\nstyle\n" {
		t.Errorf("get tag alias = %q want frontend\\nstyle\\n", out)
	}
	out, _, _ = run(t, app, "meta", "get", "wc-ab2c", "tags")
	if out != "frontend\nstyle\n" {
		t.Errorf("get tags after tag alias add = %q", out)
	}
	if _, _, err := run(t, app, "meta", "rm", "wc-ab2c", "tag", "style"); err != nil {
		t.Fatalf("rm tag alias: %v", err)
	}
	out, _, _ = run(t, app, "meta", "get", "wc-ab2c", "tags")
	if out != "frontend\n" {
		t.Errorf("tags after rm via tag alias = %q", out)
	}
	if _, _, err := run(t, app, "meta", "set", "wc-ab2c", "tag", "x"); ExitCodeFromError(err) != exitUsage {
		t.Errorf("set tag (multi) should exit 2, got %v", err)
	}

	if _, _, err := runIn(t, app, "https://example.com/doc\n", "meta", "add", "wc-ab2c", "links", "-"); err != nil {
		t.Fatalf("add links stdin: %v", err)
	}
	out, _, err = run(t, app, "meta", "get", "wc-ab2c", "links")
	if err != nil || out != "https://example.com/doc\n" {
		t.Errorf("links get = %q err=%v", out, err)
	}
	if _, _, err := runIn(t, app, "https://example.com/doc", "meta", "rm", "wc-ab2c", "links", "-"); err != nil {
		t.Fatalf("rm links stdin: %v", err)
	}
	out, _, _ = run(t, app, "meta", "get", "wc-ab2c", "links")
	if out != "" {
		t.Errorf("links after stdin rm = %q", out)
	}

	// link is a CLI alias for the wire key links.
	if _, _, err := run(t, app, "meta", "add", "wc-ab2c", "link", "https://example.com/a"); err != nil {
		t.Fatalf("add link alias: %v", err)
	}
	out, _, err = run(t, app, "meta", "get", "wc-ab2c", "link")
	if err != nil || out != "https://example.com/a\n" {
		t.Errorf("get link alias = %q err=%v", out, err)
	}
	out, _, _ = run(t, app, "meta", "get", "wc-ab2c", "links")
	if out != "https://example.com/a\n" {
		t.Errorf("get links after link alias add = %q", out)
	}
	if _, _, err := run(t, app, "meta", "rm", "wc-ab2c", "link", "https://example.com/a"); err != nil {
		t.Fatalf("rm link alias: %v", err)
	}
	out, _, _ = run(t, app, "meta", "get", "wc-ab2c", "links")
	if out != "" {
		t.Errorf("links after rm via link alias = %q", out)
	}
	if _, _, err := run(t, app, "meta", "set", "wc-ab2c", "link", "x"); ExitCodeFromError(err) != exitUsage {
		t.Errorf("set link (multi) should exit 2, got %v", err)
	}
}

func TestMetaAddTagNewNotice(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "a", "todo", "a0", "# A\n", false, "")
	addTicket(t, dir, "wc-de34", "b", "todo", "a1", "# B\n", false, "tags: [shared]\n")
	addTicket(t, dir, "wc-gh56", "old", "done", "a2", "# Old\n", true, "tags: [legacy]\n")

	// Board-new value: notice after successful write; token stays off stdout.
	out, errOut, err := run(t, app, "meta", "add", "wc-ab2c", "tags", "orphan")
	if err != nil {
		t.Fatalf("meta add new tag: %v", err)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatal("meta add must print path on stdout")
	}
	wantNew := token.FormatTagNew("orphan")
	if !strings.Contains(errOut, wantNew) {
		t.Errorf("board-new tag missing %q in stderr %q", wantNew, errOut)
	}
	if strings.Contains(out, token.TagNew) || strings.Contains(out, "tag_new:") {
		t.Errorf("tag_new must not ride meta stdout path line, got %q", out)
	}
	if strings.Contains(errOut, token.SchemaWarn) {
		t.Errorf("must not reuse schema_warn:, got %q", errOut)
	}
	if strings.Contains(errOut, token.TagUnknown) {
		t.Errorf("meta add must use tag_new not tag_unknown, got %q", errOut)
	}

	// Idempotent re-add of same value on same ticket: quiet.
	_, errOut, err = run(t, app, "meta", "add", "wc-ab2c", "tags", "orphan")
	if err != nil {
		t.Fatalf("meta re-add: %v", err)
	}
	if strings.Contains(errOut, token.TagNew) {
		t.Errorf("idempotent re-add must stay quiet, got %q", errOut)
	}

	// Value already on another active ticket: quiet.
	_, errOut, err = run(t, app, "meta", "add", "wc-ab2c", "tags", "shared")
	if err != nil {
		t.Fatalf("meta add existing board tag: %v", err)
	}
	if strings.Contains(errOut, token.TagNew) {
		t.Errorf("already-used board tag must stay quiet, got %q", errOut)
	}

	// Value only on archived ticket: quiet (in-use set includes archive).
	_, errOut, err = run(t, app, "meta", "add", "wc-ab2c", "tag", "legacy")
	if err != nil {
		t.Fatalf("meta add archive tag via alias: %v", err)
	}
	if strings.Contains(errOut, token.TagNew) {
		t.Errorf("archive-present tag must stay quiet, got %q", errOut)
	}

	// Alias path for a brand-new tag still notices.
	_, errOut, err = run(t, app, "meta", "add", "wc-ab2c", "tag", "brand-new")
	if err != nil {
		t.Fatalf("meta add brand-new via alias: %v", err)
	}
	if !strings.Contains(errOut, token.FormatTagNew("brand-new")) {
		t.Errorf("alias board-new missing notice, got %q", errOut)
	}
}

func TestMetaWrongClassAndImmutable(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "network", "todo", "a0", "# Network\n", false, "")

	if _, _, err := run(t, app, "meta", "set", "wc-ab2c", "depends", "x"); ExitCodeFromError(err) != exitUsage {
		t.Errorf("set depends should exit 2, got %v", err)
	}
	if _, _, err := run(t, app, "meta", "add", "wc-ab2c", "summary", "x"); ExitCodeFromError(err) != exitUsage {
		t.Errorf("add summary should exit 2, got %v", err)
	}
	for _, key := range []string{"status", "order", "id", "created", "status_conflict"} {
		_, _, err := run(t, app, "meta", "set", "wc-ab2c", key, "todo")
		if ExitCodeFromError(err) != exitUsage {
			t.Errorf("immutable %s exit = %v want 2", key, err)
		}
		if !strings.Contains(err.Error(), "immutable") {
			t.Errorf("immutable %s message should say immutable: %v", key, err)
		}
	}
	_, _, err := run(t, app, "meta", "set", "wc-ab2c", "status", "todo")
	if !strings.Contains(err.Error(), "tk mark") {
		t.Errorf("status immutable should point at tk mark: %v", err)
	}
	_, _, err = run(t, app, "meta", "set", "wc-ab2c", "order", "a1")
	if !strings.Contains(err.Error(), "tk order") {
		t.Errorf("order immutable should point at tk order: %v", err)
	}
}

func TestMetaSelfCommitAndRepoDrivenQuiet(t *testing.T) {
	requireGit(t)
	app := newApp(t)

	dir, repo := initGitScope(t, app, "ac", true)
	addTicket(t, dir, "ac-ab2c", "work", "todo", "a0", "# Work\n", false, "")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "seed")
	if _, _, err := run(t, app, "meta", "set", "ac-ab2c", "summary", "s"); err != nil {
		t.Fatalf("auto-commit set: %v", err)
	}
	log := gitLog(t, repo)
	if len(log) < 1 || !strings.Contains(log[0], "meta set summary") {
		t.Errorf("expected meta self-commit, log=%v", log)
	}

	dir2, repo2 := initGitScope(t, app, "rd", false)
	addTicket(t, dir2, "rd-ab2c", "work", "todo", "a0", "# Work\n", false, "")
	runGit(t, repo2, "add", ".")
	runGit(t, repo2, "commit", "-m", "seed")
	before := gitLog(t, repo2)
	_, errOut, err := run(t, app, "meta", "add", "rd-ab2c", "tags", "x")
	if err != nil {
		t.Fatalf("repo-driven add: %v", err)
	}
	if strings.Contains(errOut, "uncommitted:") {
		t.Errorf("repo-driven meta write must not ride uncommitted, got %q", errOut)
	}
	if strings.Contains(errOut, "sync_needed:") {
		t.Errorf("repo-driven meta write must not ride sync_needed, got %q", errOut)
	}
	after := gitLog(t, repo2)
	if len(after) != len(before) {
		t.Errorf("repo-driven must not self-commit: before=%v after=%v", before, after)
	}
}

func TestMetaBareShowsFamilyHelp(t *testing.T) {
	app := newApp(t)
	out, _, err := run(t, app, "meta", "--help")
	if err != nil {
		t.Fatalf("meta --help: %v", err)
	}
	for _, needle := range []string{"get", "set", "add", "rm", "depends_self:", "depends_dangling:", "depends_unresolvable:"} {
		if !strings.Contains(out, needle) {
			t.Errorf("meta help missing %q:\n%s", needle, out)
		}
	}
	out, _, err = run(t, app, "meta")
	if err != nil {
		t.Fatalf("bare meta: %v", err)
	}
	if !strings.Contains(out, "get") {
		t.Errorf("bare meta should show family help: %q", out)
	}
}
