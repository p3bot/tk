package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/token"
)

func TestScopeFieldListSetUnset(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")

	out, errOut, err := run(t, app, "scope", "field", "list", "--scope", "wc")
	if err != nil {
		t.Fatalf("list empty: %v (%s)", err, errOut)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("empty fields should print nothing, got %q", out)
	}

	out, errOut, err = run(t, app, "scope", "field", "set", "jira", "--type", "string", "--required", "--scope", "wc")
	if err != nil {
		t.Fatalf("set jira: %v (%s)", err, errOut)
	}
	if !strings.HasSuffix(strings.TrimSpace(out), "tk.cue") {
		t.Errorf("set should print tk.cue path, got %q", out)
	}

	_, errOut, err = run(t, app, "scope", "field", "set", "jira_epic", "--type", "string", "--scope", "wc")
	if err != nil {
		t.Fatalf("set jira_epic: %v (%s)", err, errOut)
	}
	_, errOut, err = run(t, app, "scope", "field", "set", "area", "--type", "string",
		"--values", "frontend", "--values", "backend", "--scope", "wc")
	if err != nil {
		t.Fatalf("set area: %v (%s)", err, errOut)
	}
	// Enum value containing a comma must stay one JSON string, not split columns.
	_, errOut, err = run(t, app, "scope", "field", "set", "label", "--type", "string",
		"--values", "a,b", "--values", "c", "--scope", "wc")
	if err != nil {
		t.Fatalf("set label: %v (%s)", err, errOut)
	}

	out, errOut, err = run(t, app, "scope", "field", "list", "--scope", "wc")
	if err != nil {
		t.Fatalf("list: %v (%s)", err, errOut)
	}
	rows := lines(out)
	want := map[string]string{
		"area":      "area\tstring\tfalse\t[\"frontend\",\"backend\"]",
		"jira":      "jira\tstring\ttrue\t",
		"jira_epic": "jira_epic\tstring\tfalse\t",
		"label":     "label\tstring\tfalse\t[\"a,b\",\"c\"]",
	}
	if len(rows) != len(want) {
		t.Fatalf("list rows = %v", rows)
	}
	for _, r := range rows {
		name := strings.SplitN(r, "\t", 2)[0]
		if want[name] != r {
			t.Errorf("row %q want %q", r, want[name])
		}
	}

	// Full-replace demotion: clear required and enum.
	_, errOut, err = run(t, app, "scope", "field", "set", "jira", "--type", "string", "--scope", "wc")
	if err != nil {
		t.Fatalf("demote jira: %v (%s)", err, errOut)
	}
	_, errOut, err = run(t, app, "scope", "field", "set", "area", "--type", "string", "--scope", "wc")
	if err != nil {
		t.Fatalf("clear area enum: %v (%s)", err, errOut)
	}
	out, _, err = run(t, app, "scope", "field", "list", "--scope", "wc")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range lines(out) {
		if strings.HasPrefix(r, "jira\t") && !strings.Contains(r, "\tfalse\t") {
			t.Errorf("jira should be optional after demote: %q", r)
		}
		if strings.HasPrefix(r, "area\t") && strings.Contains(r, "frontend") {
			t.Errorf("area enum should be cleared: %q", r)
		}
	}

	// Seed a ticket with jira value, then unset declaration — ticket file intact.
	path, id := createID(t, app, "wc", "Has jira")
	_, errOut, err = run(t, app, "scope", "field", "set", "jira", "--type", "string", "--scope", "wc")
	if err != nil {
		t.Fatalf("redeclare jira: %v (%s)", err, errOut)
	}
	_, errOut, err = run(t, app, "meta", "set", id, "jira", "ABC-1", "--scope", "wc")
	if err != nil {
		t.Fatalf("meta set jira: %v (%s)", err, errOut)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(before), "jira:") {
		t.Fatalf("ticket should carry jira before unset:\n%s", before)
	}

	_, errOut, err = run(t, app, "scope", "field", "unset", "jira", "--scope", "wc")
	if err != nil {
		t.Fatalf("unset jira: %v (%s)", err, errOut)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("unset must not modify ticket files\nbefore:\n%s\nafter:\n%s", before, after)
	}
	// Undeclared key is now a usage error for meta.
	_, errOut, err = run(t, app, "meta", "set", id, "jira", "XYZ", "--scope", "wc")
	if err == nil {
		t.Fatal("meta set undeclared jira must fail")
	}
	if ExitCodeFromError(err) != exitUsage {
		t.Errorf("exit = %d want usage; errOut=%q", ExitCodeFromError(err), errOut)
	}

	// Validation refusals.
	_, _, err = run(t, app, "scope", "field", "set", "status", "--type", "string", "--scope", "wc")
	if err == nil || ExitCodeFromError(err) != exitUsage {
		t.Errorf("shadow builtin should be usage, got %v", err)
	}
	_, _, err = run(t, app, "scope", "field", "set", "x", "--type", "float", "--scope", "wc")
	if err == nil || ExitCodeFromError(err) != exitUsage {
		t.Errorf("bad type should be usage, got %v", err)
	}
	_, _, err = run(t, app, "scope", "field", "unset", "ghost", "--scope", "wc")
	if err == nil || ExitCodeFromError(err) != exitUsage {
		t.Errorf("unset unknown should be usage, got %v", err)
	}

	// Ambient chain: TK_SCOPE and cwd code-root (no positional scope name on field verbs).
	t.Setenv("TK_SCOPE", "wc")
	out, errOut, err = run(t, app, "scope", "field", "list")
	if err != nil {
		t.Fatalf("list via TK_SCOPE: %v (%s)", err, errOut)
	}
	if !strings.Contains(out, "jira_epic") {
		t.Errorf("ambient env list should show declared fields, got %q", out)
	}

	t.Setenv("TK_SCOPE", "")
	t.Chdir(dir)
	out, errOut, err = run(t, app, "scope", "field", "list")
	if err != nil {
		t.Fatalf("list via cwd: %v (%s)", err, errOut)
	}
	if !strings.Contains(out, "jira_epic") {
		t.Errorf("ambient cwd list should show declared fields, got %q", out)
	}

	// No ambient and no --scope: same failure class as other board verbs.
	t.Chdir(t.TempDir())
	_, _, err = run(t, app, "scope", "field", "list")
	if err == nil {
		t.Fatal("list with no ambient scope must fail")
	}
}

func TestScopeFieldAutoCommitDurability(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	dir, repo := initGitScope(t, app, "wc", true)

	out, errOut, err := run(t, app, "scope", "field", "set", "jira", "--type", "string", "--required", "--scope", "wc")
	if err != nil {
		t.Fatalf("set under auto-commit: %v (%s)", err, errOut)
	}
	if !strings.HasSuffix(strings.TrimSpace(out), "tk.cue") {
		t.Errorf("set should print tk.cue path, got %q", out)
	}
	log := gitLog(t, repo)
	if len(log) != 1 || log[0] != "tk: scope field set jira" {
		t.Fatalf("field set should self-commit once, got %v", log)
	}
	tree := gitTree(t, repo)
	rel, _ := filepath.Rel(repo, filepath.Join(dir, "tk.cue"))
	if !containsPath(tree, rel) {
		t.Errorf("tk.cue must be in the commit tree, got %v", tree)
	}

	_, errOut, err = run(t, app, "scope", "field", "unset", "jira", "--scope", "wc")
	if err != nil {
		t.Fatalf("unset under auto-commit: %v (%s)", err, errOut)
	}
	log = gitLog(t, repo)
	if len(log) != 2 || log[0] != "tk: scope field unset jira" {
		t.Fatalf("field unset should self-commit, got %v", log)
	}

	// auto-commit scope with no git-root: write succeeds, sync_disabled rides stderr.
	app2 := newApp(t)
	plain := initScope(t, app2, "ac")
	// Force autoCommit true without a git root by rewriting tk.cue.
	if err := os.WriteFile(filepath.Join(plain, "tk.cue"), []byte("name: \"ac\"\nautoCommit: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, errOut, err = run(t, app2, "scope", "field", "set", "jira", "--type", "string", "--scope", "ac")
	if err != nil {
		t.Fatalf("set without git-root: %v (%s)", err, errOut)
	}
	if !strings.Contains(errOut, token.SyncDisabled) {
		t.Errorf("auto-commit without git-root must emit sync_disabled, got %q", errOut)
	}
}

// Field set/unset self-commit must ride sync_needed: like other tk-driven durability paths.
func TestScopeFieldSyncNeededUnpushed(t *testing.T) {
	requireGit(t)
	remote := newBareRemote(t)
	m := cloneMachine(t, remote)
	dir := m.initScopeAutoCommit(t)
	addTicket(t, dir, "wc-ab2c", "x", "todo", "a0", "# X\n", false, "")
	gitIn(t, m.clone, "add", "-A")
	gitIn(t, m.clone, "commit", "-m", "seed scope")
	gitIn(t, m.clone, "push", "-u", "origin", "main")

	_, errOut, err := run(t, m.app, "scope", "field", "set", "jira", "--type", "string", "--scope", "wc")
	if err != nil {
		t.Fatalf("field set: %v (%s)", err, errOut)
	}
	if !strings.Contains(errOut, "sync_needed: unpushed") {
		t.Errorf("field set self-commit ahead of upstream must ride sync_needed: unpushed, got %q", errOut)
	}

	_, errOut, err = run(t, m.app, "scope", "field", "unset", "jira", "--scope", "wc")
	if err != nil {
		t.Fatalf("field unset: %v (%s)", err, errOut)
	}
	if !strings.Contains(errOut, "sync_needed: unpushed") {
		t.Errorf("field unset self-commit ahead of upstream must ride sync_needed: unpushed, got %q", errOut)
	}
}

// Multi-file package scopes (sibling .cue in the import closure) must evaluate
// the same way reconcile does — single-file Load would refuse values: tags.
func TestScopeFieldMultiFilePackageClosure(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	// Replace minimal tk.cue with a package that references a sibling file.
	if err := os.WriteFile(filepath.Join(dir, "tk.cue"), []byte(
		"package wccfg\nname: \"wc\"\nautoCommit: false\nfields: { area: { type: \"string\", values: tags } }\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "schema.cue"), []byte(
		"package wccfg\ntags: [\"frontend\", \"backend\"]\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}

	out, errOut, err := run(t, app, "scope", "field", "list", "--scope", "wc")
	if err != nil {
		t.Fatalf("list packaged fields: %v (%s)", err, errOut)
	}
	if !strings.Contains(out, "area\tstring\tfalse\t[\"frontend\",\"backend\"]") {
		t.Errorf("list must resolve values: tags from sibling, got %q", out)
	}

	// Sibling field set must preserve the area values: tags reference and re-validate package.
	_, errOut, err = run(t, app, "scope", "field", "set", "jira", "--type", "string", "--required", "--scope", "wc")
	if err != nil {
		t.Fatalf("set jira on package scope: %v (%s)", err, errOut)
	}
	out, errOut, err = run(t, app, "scope", "field", "list", "--scope", "wc")
	if err != nil {
		t.Fatalf("list after set: %v (%s)", err, errOut)
	}
	if !strings.Contains(out, "area\t") || !strings.Contains(out, "jira\tstring\ttrue\t") {
		t.Errorf("list after set want area + jira, got %q", out)
	}
	data, err := os.ReadFile(filepath.Join(dir, "tk.cue"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "values: tags") {
		t.Errorf("set sibling must not flatten area's values: tags ref:\n%s", data)
	}

	// Full-replace area: clearing the enum is a successful package-unified write.
	_, errOut, err = run(t, app, "scope", "field", "set", "area", "--type", "string", "--scope", "wc")
	if err != nil {
		t.Fatalf("demote area enum: %v (%s)", err, errOut)
	}
	out, _, err = run(t, app, "scope", "field", "list", "--scope", "wc")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range lines(out) {
		if strings.HasPrefix(r, "area\t") && strings.Contains(r, "frontend") {
			t.Errorf("area enum should be cleared after full replace: %q", r)
		}
	}
}

// Post-write re-validate must use package unify: a dual fields: definition that
// conflicts across files must refuse, not exit 0 after single-file Load success.
func TestScopeFieldSetRefusesPackageUnifyConflict(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	if err := os.WriteFile(filepath.Join(dir, "tk.cue"), []byte(
		"package wccfg\nname: \"wc\"\nautoCommit: false\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fields.cue"), []byte(
		"package wccfg\nfields: { jira: { type: \"string\", required: true } }\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}

	// List sees jira via package closure.
	out, errOut, err := run(t, app, "scope", "field", "list", "--scope", "wc")
	if err != nil {
		t.Fatalf("list: %v (%s)", err, errOut)
	}
	if !strings.Contains(out, "jira\tstring\ttrue\t") {
		t.Errorf("list must see fields.cue declaration, got %q", out)
	}

	// Set rewrites only tk.cue; type conflict with fields.cue must refuse and roll back.
	before, err := os.ReadFile(filepath.Join(dir, "tk.cue"))
	if err != nil {
		t.Fatal(err)
	}
	_, errOut, err = run(t, app, "scope", "field", "set", "jira", "--type", "int", "--scope", "wc")
	if err == nil {
		t.Fatal("set that conflicts with sibling fields.cue must fail re-validate")
	}
	if !strings.Contains(errOut, token.ConfigUnparseable) && !strings.Contains(err.Error(), token.ConfigUnparseable) {
		t.Errorf("want config_unparseable on unify conflict, err=%v errOut=%q", err, errOut)
	}
	after, err := os.ReadFile(filepath.Join(dir, "tk.cue"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("failed set must restore tk.cue\nbefore:\n%s\nafter:\n%s", before, after)
	}
	// Scope must remain usable after rollback.
	out, errOut, err = run(t, app, "scope", "field", "list", "--scope", "wc")
	if err != nil {
		t.Fatalf("list after rolled-back conflict: %v (%s)", err, errOut)
	}
	if !strings.Contains(out, "jira\tstring\ttrue\t") {
		t.Errorf("list after rollback must still see fields.cue jira, got %q", out)
	}
}

// Sibling package files that still constrain a field must not yield silent
// full-replace success: demote/unset either fully apply or refuse and leave tk.cue intact.
func TestScopeFieldMultiFileFullReplaceSemantics(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	if err := os.WriteFile(filepath.Join(dir, "tk.cue"), []byte(
		"package wccfg\nname: \"wc\"\nautoCommit: false\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fields.cue"), []byte(
		"package wccfg\nfields: { jira: { type: \"string\", required: true } }\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}

	before, err := os.ReadFile(filepath.Join(dir, "tk.cue"))
	if err != nil {
		t.Fatal(err)
	}

	// Demote without --required cannot clear sibling required: true.
	_, errOut, err := run(t, app, "scope", "field", "set", "jira", "--type", "string", "--scope", "wc")
	if err == nil {
		t.Fatal("demote blocked by sibling required must fail")
	}
	if ExitCodeFromError(err) != exitUsage {
		t.Errorf("exit = %d want usage; err=%v errOut=%q", ExitCodeFromError(err), err, errOut)
	}
	after, err := os.ReadFile(filepath.Join(dir, "tk.cue"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("failed demote must not leave dual definition on disk\nbefore:\n%s\nafter:\n%s", before, after)
	}
	out, _, err := run(t, app, "scope", "field", "list", "--scope", "wc")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "jira\tstring\ttrue\t") {
		t.Errorf("jira must remain required from sibling, got %q", out)
	}

	// Matching full replace (same type + required) may dual-define and succeed.
	_, errOut, err = run(t, app, "scope", "field", "set", "jira", "--type", "string", "--required", "--scope", "wc")
	if err != nil {
		t.Fatalf("matching set: %v (%s)", err, errOut)
	}
	out, _, err = run(t, app, "scope", "field", "list", "--scope", "wc")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "jira\tstring\ttrue\t") {
		t.Errorf("matching set list, got %q", out)
	}

	// Unset removes only tk.cue copy; sibling keeps the field — refuse and restore.
	before, err = os.ReadFile(filepath.Join(dir, "tk.cue"))
	if err != nil {
		t.Fatal(err)
	}
	_, errOut, err = run(t, app, "scope", "field", "unset", "jira", "--scope", "wc")
	if err == nil {
		t.Fatal("unset while sibling still declares jira must fail")
	}
	if ExitCodeFromError(err) != exitUsage {
		t.Errorf("unset dual exit = %d want usage; err=%v errOut=%q", ExitCodeFromError(err), err, errOut)
	}
	after, err = os.ReadFile(filepath.Join(dir, "tk.cue"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("failed unset must restore tk.cue\nbefore:\n%s\nafter:\n%s", before, after)
	}
	out, _, err = run(t, app, "scope", "field", "list", "--scope", "wc")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "jira\t") {
		t.Errorf("jira must remain after refused unset, got %q", out)
	}

	// Sibling-only declaration (no tk.cue copy): unset is usage, not a silent no-op.
	if err := os.WriteFile(filepath.Join(dir, "tk.cue"), []byte(
		"package wccfg\nname: \"wc\"\nautoCommit: false\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}
	_, errOut, err = run(t, app, "scope", "field", "unset", "jira", "--scope", "wc")
	if err == nil {
		t.Fatal("unset sibling-only must fail")
	}
	if ExitCodeFromError(err) != exitUsage {
		t.Errorf("sibling-only unset exit = %d want usage; err=%v errOut=%q", ExitCodeFromError(err), err, errOut)
	}
	if !strings.Contains(err.Error(), "tk.cue") && !strings.Contains(errOut, "tk.cue") {
		t.Errorf("sibling-only unset should mention tk.cue, err=%v errOut=%q", err, errOut)
	}
}

// fields: as a reference is package-valid but not AST-rewritable: UnsetField must
// surface that error, not the sibling-only usage rewrite reserved for ErrFieldNotDeclared.
func TestScopeFieldUnsetPropagatesNonNotDeclaredErrors(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	if err := os.WriteFile(filepath.Join(dir, "tk.cue"), []byte(
		"package wccfg\nname: \"wc\"\nautoCommit: false\nfields: fieldDefs\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "defs.cue"), []byte(
		"package wccfg\nfieldDefs: { jira: { type: \"string\", required: true } }\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}

	out, errOut, err := run(t, app, "scope", "field", "list", "--scope", "wc")
	if err != nil {
		t.Fatalf("list ref fields: %v (%s)", err, errOut)
	}
	if !strings.Contains(out, "jira\t") {
		t.Fatalf("schema must see jira via fields: ref, got %q", out)
	}

	_, errOut, err = run(t, app, "scope", "field", "unset", "jira", "--scope", "wc")
	if err == nil {
		t.Fatal("unset with fields: ref must fail")
	}
	msg := err.Error() + errOut
	if strings.Contains(msg, "sibling package") {
		t.Errorf("must not rewrite as sibling-only usage; err=%v errOut=%q", err, errOut)
	}
	if !strings.Contains(msg, "struct literal") {
		t.Errorf("want non-struct-literal rewrite error, err=%v errOut=%q", err, errOut)
	}
}

func TestMetaMarkRequiredMissingWarnMatrix(t *testing.T) {
	app := newApp(t)
	_ = initScope(t, app, "wc")

	_, errOut, err := run(t, app, "scope", "field", "set", "jira", "--type", "string", "--required", "--scope", "wc")
	if err != nil {
		t.Fatalf("set jira: %v (%s)", err, errOut)
	}
	_, errOut, err = run(t, app, "scope", "field", "set", "pts", "--type", "int", "--required", "--scope", "wc")
	if err != nil {
		t.Fatalf("set pts: %v (%s)", err, errOut)
	}
	_, errOut, err = run(t, app, "scope", "field", "set", "owners", "--type", "strings", "--required", "--scope", "wc")
	if err != nil {
		t.Fatalf("set owners: %v (%s)", err, errOut)
	}

	path, id := createID(t, app, "wc", "Required matrix")
	_, errOut, err = run(t, app, "mark", id, "todo", "--scope", "wc")
	if err != nil {
		t.Fatalf("mark todo: %v (%s)", err, errOut)
	}

	// meta set of unrelated key still soft-warns for missing required (closed line shape).
	_, errOut, err = run(t, app, "meta", "set", id, "summary", "hello", "--scope", "wc")
	if err != nil {
		t.Fatalf("meta set summary: %v (%s)", err, errOut)
	}
	wantLine := token.FormatRequiredMissing(id, []string{"jira", "owners", "pts"})
	if !strings.Contains(errOut, wantLine) {
		t.Errorf("meta warn want %q in stderr, got %q", wantLine, errOut)
	}

	// Empty string still missing.
	_, errOut, err = run(t, app, "meta", "set", id, "jira", "", "--scope", "wc")
	if err != nil {
		t.Fatalf("meta set empty jira: %v (%s)", err, errOut)
	}
	if !strings.Contains(errOut, token.FormatRequiredMissing(id, []string{"jira", "owners", "pts"})) {
		t.Errorf("empty jira still missing, got %q", errOut)
	}

	// int 0 satisfies required int.
	_, errOut, err = run(t, app, "meta", "set", id, "pts", "0", "--scope", "wc")
	if err != nil {
		t.Fatalf("meta set pts 0: %v (%s)", err, errOut)
	}
	if strings.Contains(errOut, "pts") {
		t.Errorf("present int 0 must not be missing, got %q", errOut)
	}
	if !strings.Contains(errOut, token.FormatRequiredMissing(id, []string{"jira", "owners"})) {
		t.Errorf("other required still missing, got %q", errOut)
	}

	// Populate jira; leave owners for add/rm path.
	_, errOut, err = run(t, app, "meta", "set", id, "jira", "ABC-9", "--scope", "wc")
	if err != nil {
		t.Fatalf("meta set jira: %v (%s)", err, errOut)
	}
	if !strings.Contains(errOut, token.FormatRequiredMissing(id, []string{"owners"})) {
		t.Errorf("only owners should remain missing, got %q", errOut)
	}

	_, errOut, err = run(t, app, "meta", "add", id, "owners", "ada", "--scope", "wc")
	if err != nil {
		t.Fatalf("meta add owners: %v (%s)", err, errOut)
	}
	if strings.Contains(errOut, token.RequiredMissing) {
		t.Errorf("all required satisfied must stay quiet, got %q", errOut)
	}

	// meta rm last required strings entry re-opens the gap.
	_, errOut, err = run(t, app, "meta", "rm", id, "owners", "ada", "--scope", "wc")
	if err != nil {
		t.Fatalf("meta rm owners: %v (%s)", err, errOut)
	}
	if !strings.Contains(errOut, token.FormatRequiredMissing(id, []string{"owners"})) {
		t.Errorf("meta rm emptying required list must warn, got %q", errOut)
	}
	_, errOut, err = run(t, app, "meta", "add", id, "owners", "ada", "--scope", "wc")
	if err != nil {
		t.Fatalf("re-add owners: %v (%s)", err, errOut)
	}
	if strings.Contains(errOut, token.RequiredMissing) {
		t.Errorf("re-satisfied must stay quiet, got %q", errOut)
	}

	// mark done with all satisfied: no required_missing.
	_, errOut, err = run(t, app, "mark", id, "done", "--scope", "wc")
	if err != nil {
		t.Fatalf("mark done satisfied: %v (%s)", err, errOut)
	}
	if strings.Contains(errOut, token.RequiredMissing) {
		t.Errorf("satisfied done must not warn required, got %q", errOut)
	}
	// same-status re-mark done: quiet.
	_, errOut, err = run(t, app, "mark", id, "done", "--scope", "wc")
	if err != nil {
		t.Fatalf("re-mark done: %v (%s)", err, errOut)
	}
	if strings.Contains(errOut, token.RequiredMissing) {
		t.Errorf("same-status done must not warn required, got %q", errOut)
	}

	// Second ticket: transition into done with gaps warns; cancelled does not.
	path2, id2 := createID(t, app, "wc", "Gaps ticket")
	_, _, err = run(t, app, "mark", id2, "todo", "--scope", "wc")
	if err != nil {
		t.Fatal(err)
	}
	out, errOut, err := run(t, app, "mark", id2, "done", "--scope", "wc")
	if err != nil {
		t.Fatalf("mark done missing: %v (%s)", err, errOut)
	}
	wantDone := token.FormatRequiredMissing(id2, []string{"jira", "owners", "pts"})
	if !strings.Contains(errOut, wantDone) {
		t.Errorf("transition into done with gaps must warn %q, got %q", wantDone, errOut)
	}
	if !strings.Contains(out, "archive") {
		// mark stdout is the post-move path.
		got, _, gerr := run(t, app, "get", id2, "--scope", "wc")
		if gerr != nil {
			t.Fatal(gerr)
		}
		if !strings.Contains(got, "archive") {
			t.Errorf("done must archive, mark out %q get %q", out, got)
		}
	}

	path3, id3 := createID(t, app, "wc", "Cancel ticket")
	_, _, err = run(t, app, "mark", id3, "todo", "--scope", "wc")
	if err != nil {
		t.Fatal(err)
	}
	_, errOut, err = run(t, app, "mark", id3, "cancelled", "--scope", "wc")
	if err != nil {
		t.Fatalf("mark cancelled: %v (%s)", err, errOut)
	}
	if strings.Contains(errOut, token.RequiredMissing) {
		t.Errorf("cancelled must not emit required_missing, got %q", errOut)
	}
	_ = path
	_ = path2
	_ = path3
}
