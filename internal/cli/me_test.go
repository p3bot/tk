package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/registry"
	"github.com/p3bot/tk/internal/testgit"
)

func loadMeMap(t *testing.T, app *App) map[string]string {
	t.Helper()
	reg, err := registry.NewStore(app.Ctx, app.ConfigDir).Load()
	if err != nil {
		t.Fatalf("load me: %v", err)
	}
	return reg.Me
}

func meCueExists(app *App) bool {
	_, err := os.Stat(filepath.Join(app.ConfigDir, "me.cue"))
	return err == nil
}

func TestMeSetShowClear(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "one", "todo", "a0", "# One\n", false, "")

	out, _, err := run(t, app, "me", "--scope", "wc")
	if err != nil {
		t.Fatalf("unset show: %v", err)
	}
	if out != "" {
		t.Errorf("unset show must be empty stdout, got %q", out)
	}

	if _, _, err := run(t, app, "me", "wc-ab2c", "--scope", "wc"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if loadMeMap(t, app)["wc"] != "wc-ab2c" {
		t.Errorf("stored me = %q want wc-ab2c", loadMeMap(t, app)["wc"])
	}

	got, _, err := run(t, app, "me", "--scope", "wc")
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	if got != "wc-ab2c\n" {
		t.Errorf("show = %q want wc-ab2c\\n", got)
	}

	if _, _, err := run(t, app, "me", "--clear", "--scope", "wc"); err != nil {
		t.Fatalf("clear: %v", err)
	}
	out, _, err = run(t, app, "me", "--scope", "wc")
	if err != nil {
		t.Fatalf("show after clear: %v", err)
	}
	if out != "" {
		t.Errorf("cleared show must be empty, got %q", out)
	}
	if _, ok := loadMeMap(t, app)["wc"]; ok {
		t.Error("cleared me entry must be gone")
	}

	_, _, err = run(t, app, "mark", "me", "done", "--scope", "wc")
	if ExitCodeFromError(err) == 0 {
		t.Fatal("mark me after clear must fail as unknown")
	}
}

func TestMeShowFollowsArchiveMove(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "one", "todo", "a0", "# One\n", false, "")

	if _, _, err := run(t, app, "me", "wc-ab2c", "--scope", "wc"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if _, _, err := run(t, app, "mark", "wc-ab2c", "done", "--scope", "wc"); err != nil {
		t.Fatalf("mark done: %v", err)
	}
	want, _, err := run(t, app, "get", "wc-ab2c")
	if err != nil {
		t.Fatalf("get archived: %v", err)
	}
	if !strings.Contains(want, string(filepath.Separator)+"archive"+string(filepath.Separator)) {
		t.Fatalf("get after done should be under archive/, got %q", want)
	}
	got, _, err := run(t, app, "me", "--scope", "wc")
	if err != nil {
		t.Fatalf("show archived: %v", err)
	}
	if got != "wc-ab2c\n" {
		t.Errorf("show after archive = %q want wc-ab2c\\n", got)
	}
}

func TestMeAliasThroughVerbs(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "one", "todo", "a0", "# One\n", false, "")
	addTicket(t, dir, "wc-de34", "two", "todo", "a1", "# Two\n", false, "")

	if _, _, err := run(t, app, "me", "wc-ab2c", "--scope", "wc"); err != nil {
		t.Fatalf("set: %v", err)
	}

	want, _, err := run(t, app, "get", "wc-ab2c")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	got, _, err := run(t, app, "get", "me", "--scope", "wc")
	if err != nil {
		t.Fatalf("get me: %v", err)
	}
	if got != want {
		t.Errorf("get me = %q want %q", got, want)
	}

	out, _, err := run(t, app, "meta", "get", "me", "id", "--scope", "wc")
	if err != nil {
		t.Fatalf("meta get me id: %v", err)
	}
	if strings.TrimSpace(out) != "wc-ab2c" {
		t.Errorf("meta get me id = %q want wc-ab2c", out)
	}

	if _, _, err := run(t, app, "mark", "me", "review", "--scope", "wc"); err != nil {
		t.Fatalf("mark me: %v", err)
	}
	if got := fmValue(t, strings.TrimSpace(want), "status"); got != "review" {
		t.Errorf("mark me status = %q want review", got)
	}

	dePath := filepath.Join(dir, "wc-de34-two.md")
	abPath := filepath.Join(dir, "wc-ab2c-one.md")
	if _, _, err := run(t, app, "order", "wc-de34", "--before", "me", "--scope", "wc"); err != nil {
		t.Fatalf("order --before me: %v", err)
	}
	if got, neighbour := fmValue(t, dePath, "order"), fmValue(t, abPath, "order"); got == "" || got >= neighbour {
		t.Errorf("order --before me: subject order %q should sort before neighbour %q", got, neighbour)
	}
}

func TestMeExpandsInDependsAndRelated(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "one", "todo", "a0", "# One\n", false, "")
	addTicket(t, dir, "wc-de34", "two", "todo", "a1", "# Two\n", false, "")

	if _, _, err := run(t, app, "me", "wc-ab2c", "--scope", "wc"); err != nil {
		t.Fatalf("set: %v", err)
	}

	if _, _, err := run(t, app, "meta", "add", "wc-de34", "depends", "me", "--scope", "wc"); err != nil {
		t.Fatalf("depends me: %v", err)
	}
	got, _, err := run(t, app, "meta", "get", "wc-de34", "depends", "--scope", "wc")
	if err != nil {
		t.Fatalf("get depends: %v", err)
	}
	if got != "wc-ab2c\n" {
		t.Errorf("depends me must store the expanded full id, got %q", got)
	}

	if _, _, err := run(t, app, "meta", "add", "wc-de34", "related", "me", "--scope", "wc"); err != nil {
		t.Fatalf("related me: %v", err)
	}
	got, _, err = run(t, app, "meta", "get", "wc-de34", "related", "--scope", "wc")
	if err != nil {
		t.Fatalf("get related: %v", err)
	}
	if got != "wc-ab2c\n" {
		t.Errorf("related me must store the expanded full id, got %q", got)
	}

	if _, _, err := run(t, app, "meta", "rm", "wc-de34", "depends", "me", "--scope", "wc"); err != nil {
		t.Fatalf("rm depends me: %v", err)
	}
	got, _, err = run(t, app, "meta", "get", "wc-de34", "depends", "--scope", "wc")
	if err != nil {
		t.Fatalf("get depends after rm: %v", err)
	}
	if got != "" {
		t.Errorf("rm depends me must match the expanded id, got %q", got)
	}

	if _, _, err := run(t, app, "me", "--clear", "--scope", "wc"); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, _, err := run(t, app, "meta", "add", "wc-de34", "related", "me", "--scope", "wc"); ExitCodeFromError(err) == 0 {
		t.Error("related me with unset pointer must fail")
	}
}

func TestMeRefusePaths(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	other := initScope(t, app, "ot")
	addTicket(t, dir, "wc-ab2c", "one", "todo", "a0", "# One\n", false, "")
	addTicket(t, other, "ot-aa22", "x", "todo", "a0", "# X\n", false, "")

	out, _, err := run(t, app, "me", "not-an-id", "--scope", "wc")
	if ExitCodeFromError(err) != exitUsage {
		t.Errorf("malformed id exit = %v want 2", err)
	}
	if out != "" {
		t.Errorf("malformed refuse must leave stdout empty, got %q", out)
	}
	if meCueExists(app) {
		t.Error("malformed set must not write me.cue")
	}

	if _, _, err := run(t, app, "me", "zzzz", "--scope", "wc"); ExitCodeFromError(err) == 0 {
		t.Error("unknown well-formed short id must fail")
	}
	if meCueExists(app) {
		t.Error("unknown set must not write me.cue")
	}

	if _, _, err := run(t, app, "me", "ot-aa22", "--scope", "wc"); ExitCodeFromError(err) == 0 {
		t.Error("foreign-scope full id must refuse")
	}
	if meCueExists(app) {
		t.Error("cross-scope set must not write me.cue")
	}

	if _, _, err := run(t, app, "me", "me", "--scope", "wc"); ExitCodeFromError(err) != exitUsage {
		t.Errorf("set me to me exit = %v want 2", err)
	}
	if meCueExists(app) {
		t.Error("set me to me must not write me.cue")
	}

	if _, _, err := run(t, app, "me", "--clear", "wc-ab2c", "--scope", "wc"); ExitCodeFromError(err) != exitUsage {
		t.Errorf("--clear plus id exit = %v want 2", err)
	}

	if _, _, err := run(t, app, "me", "wc-ab2c", "wc-de34", "--scope", "wc"); ExitCodeFromError(err) != exitUsage {
		t.Errorf("extra positionals exit = %v want 2", err)
	}

	addTicket(t, dir, "wc-ab2c", "dup", "todo", "a2", "# Dup\n", false, "")
	if _, _, err := run(t, app, "me", "wc-ab2c", "--scope", "wc"); ExitCodeFromError(err) == 0 {
		t.Error("duplicate id must refuse set")
	}
	if meCueExists(app) {
		t.Error("duplicate set must not write me.cue")
	}
}

func TestMeAllowsParseErrorAndArchived(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	bad := "---\nid: wc-ab2c\n<<<<<<< HEAD\nstatus: todo\n=======\nstatus: done\n>>>>>>> x\n---\n# T\n"
	if err := os.WriteFile(filepath.Join(dir, "wc-ab2c-broken.md"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, app, "me", "wc-ab2c", "--scope", "wc"); err != nil {
		t.Fatalf("parse_error is a legal set target: %v", err)
	}
	if loadMeMap(t, app)["wc"] != "wc-ab2c" {
		t.Errorf("stored parse_error id = %q", loadMeMap(t, app)["wc"])
	}
	wantGet, getErr, err := run(t, app, "get", "wc-ab2c")
	if err != nil {
		t.Fatalf("get parse_error: %v", err)
	}
	if !strings.Contains(getErr, "parse_error:") {
		t.Errorf("get should ride parse_error, stderr=%q", getErr)
	}
	gotGet, getMeErr, err := run(t, app, "get", "me", "--scope", "wc")
	if err != nil {
		t.Fatalf("get me parse_error: %v", err)
	}
	if gotGet != wantGet {
		t.Errorf("get me = %q want %q", gotGet, wantGet)
	}
	if !strings.Contains(getMeErr, "parse_error:") {
		t.Errorf("get me should ride parse_error, stderr=%q", getMeErr)
	}
	gotShow, _, err := run(t, app, "me", "--scope", "wc")
	if err != nil {
		t.Fatalf("show parse_error: %v", err)
	}
	if gotShow != "wc-ab2c\n" {
		t.Errorf("show parse_error = %q want wc-ab2c\\n", gotShow)
	}

	app2 := newApp(t)
	dir2 := initScope(t, app2, "wc")
	addTicket(t, dir2, "wc-de34", "old", "done", "a0", "# Old\n", true, "")
	if _, _, err := run(t, app2, "me", "wc-de34", "--scope", "wc"); err != nil {
		t.Fatalf("archived ticket is a legal set target: %v", err)
	}
	if loadMeMap(t, app2)["wc"] != "wc-de34" {
		t.Errorf("stored archived id = %q", loadMeMap(t, app2)["wc"])
	}
	wantArch, _, err := run(t, app2, "get", "wc-de34")
	if err != nil {
		t.Fatalf("get archived: %v", err)
	}
	gotArch, _, err := run(t, app2, "get", "me", "--scope", "wc")
	if err != nil {
		t.Fatalf("get me archived: %v", err)
	}
	if gotArch != wantArch {
		t.Errorf("get me archived = %q want %q", gotArch, wantArch)
	}
	showArch, _, err := run(t, app2, "me", "--scope", "wc")
	if err != nil {
		t.Fatalf("show archived: %v", err)
	}
	if showArch != "wc-de34\n" {
		t.Errorf("show archived = %q want wc-de34\\n", showArch)
	}
}

func TestMeStalePointerIsUnknown(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "one", "todo", "a0", "# One\n", false, "")
	if _, _, err := run(t, app, "me", "wc-ab2c", "--scope", "wc"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, "wc-ab2c-one.md")); err != nil {
		t.Fatal(err)
	}

	_, _, err := run(t, app, "get", "me", "--scope", "wc")
	if ExitCodeFromError(err) == 0 {
		t.Fatal("stale get me must fail")
	}
	_, _, err = run(t, app, "me", "--scope", "wc")
	if ExitCodeFromError(err) == 0 {
		t.Fatal("stale show must fail like get")
	}
}

func TestMeNotWrittenByClaimOrMark(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "one", "todo", "a0", "# One\n", false, "")
	addTicket(t, dir, "wc-de34", "two", "todo", "a1", "# Two\n", false, "")

	if _, _, err := run(t, app, "next", "--claim", "--scope", "wc"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if meCueExists(app) {
		t.Error("next --claim must not create me.cue")
	}

	if _, _, err := run(t, app, "me", "wc-de34", "--scope", "wc"); err != nil {
		t.Fatalf("set: %v", err)
	}
	before := loadMeMap(t, app)["wc"]
	dataBefore, err := os.ReadFile(filepath.Join(app.ConfigDir, "me.cue"))
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := run(t, app, "mark", "wc-de34", "in-progress", "--scope", "wc"); err != nil {
		t.Fatalf("mark in-progress: %v", err)
	}
	dataAfter, err := os.ReadFile(filepath.Join(app.ConfigDir, "me.cue"))
	if err != nil {
		t.Fatal(err)
	}
	if string(dataAfter) != string(dataBefore) || loadMeMap(t, app)["wc"] != before {
		t.Error("mark must not change me.cue")
	}
}

func TestMeNotWrittenByCreate(t *testing.T) {
	app := newApp(t)
	initScope(t, app, "wc")
	if _, _ = createID(t, app, "wc", "Fresh ticket"); meCueExists(app) {
		t.Error("create must not write me.cue")
	}
}

func TestMeRebindDoesNotInvent(t *testing.T) {
	app := newApp(t)
	base := t.TempDir()
	dir := filepath.Join(base, "orig")
	if _, _, err := run(t, app, "scope", "init", dir, "--name", "wc"); err != nil {
		t.Fatalf("init: %v", err)
	}
	addTicket(t, dir, "wc-ab2c", "one", "todo", "a0", "# One\n", false, "")

	moved := filepath.Join(base, "moved")
	if err := os.Rename(dir, moved); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, app, "scope", "rebind", moved, "--name", "wc"); err != nil {
		t.Fatalf("rebind: %v", err)
	}
	if _, ok := loadMeMap(t, app)["wc"]; ok {
		t.Error("rebind must not invent a me pointer")
	}

	if _, _, err := run(t, app, "me", "wc-ab2c", "--scope", "wc"); err != nil {
		t.Fatalf("set after rebind: %v", err)
	}
	moved2 := filepath.Join(base, "moved2")
	if err := os.Rename(moved, moved2); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, app, "scope", "rebind", moved2, "--name", "wc"); err != nil {
		t.Fatalf("second rebind: %v", err)
	}
	if got := loadMeMap(t, app)["wc"]; got != "wc-ab2c" {
		t.Errorf("rebind must preserve an existing pointer, got %q", got)
	}
}

func TestMePulseKey(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "one", "todo", "a0", "# One\n", false, "")

	out, _, err := run(t, app, "status", "--scope", "wc")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	keys := pulseKeys(out)
	if !slicesEqual(keys, statusKeys) {
		t.Fatalf("key order = %v want %v", keys, statusKeys)
	}
	lensIdx, meIdx := -1, -1
	for i, k := range keys {
		if k == "lens" {
			lensIdx = i
		}
		if k == "me" {
			meIdx = i
		}
	}
	if meIdx != lensIdx+1 {
		t.Fatalf("me must sit immediately after lens, lens=%d me=%d", lensIdx, meIdx)
	}
	if parsePulse(out)["me"] != "" {
		t.Errorf("unset pulse me = %q want empty", parsePulse(out)["me"])
	}

	bare, _, err := run(t, app, "status", "me", "--scope", "wc")
	if err != nil {
		t.Fatalf("status me unset: %v", err)
	}
	if bare != "" {
		t.Errorf("unset status me must be empty stdout, got %q", bare)
	}

	if _, _, err := run(t, app, "me", "wc-ab2c", "--scope", "wc"); err != nil {
		t.Fatalf("set: %v", err)
	}
	out, _, err = run(t, app, "status", "--scope", "wc")
	if err != nil {
		t.Fatalf("status after set: %v", err)
	}
	if parsePulse(out)["me"] != "wc-ab2c" {
		t.Errorf("pulse me = %q want wc-ab2c", parsePulse(out)["me"])
	}
	bare, _, err = run(t, app, "status", "me", "--scope", "wc")
	if err != nil {
		t.Fatalf("status me: %v", err)
	}
	if bare != "wc-ab2c\n" {
		t.Errorf("status me = %q want wc-ab2c\\n", bare)
	}
	if strings.Contains(bare, "/") || strings.Contains(bare, "\t") {
		t.Errorf("pulse must carry the full id, not a path: %q", bare)
	}
}

func TestMeForgetDropsEntry(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "one", "todo", "a0", "# One\n", false, "")
	if _, _, err := run(t, app, "me", "wc-ab2c", "--scope", "wc"); err != nil {
		t.Fatalf("set: %v", err)
	}

	if _, _, err := run(t, app, "scope", "forget", "wc"); err != nil {
		t.Fatalf("forget: %v", err)
	}
	if _, ok := loadMeMap(t, app)["wc"]; ok {
		t.Error("forget must drop the me entry")
	}

	if _, _, err := run(t, app, "scope", "import", dir); err != nil {
		t.Fatalf("import: %v", err)
	}
	out, _, err := run(t, app, "me", "--scope", "wc")
	if err != nil {
		t.Fatalf("show after forget+import: %v", err)
	}
	if out != "" {
		t.Errorf("forget+import must start with no pointer, got %q", out)
	}
}

func TestMeRenameClearsPointer(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	other := initScope(t, app, "ot")
	addTicket(t, dir, "wc-ab2c", "one", "todo", "a0", "# One\n", false, "")
	addTicket(t, other, "ot-aa22", "x", "todo", "a0", "# X\n", false, "")
	if _, _, err := run(t, app, "me", "wc-ab2c", "--scope", "wc"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if _, _, err := run(t, app, "me", "ot-aa22", "--scope", "ot"); err != nil {
		t.Fatalf("set other: %v", err)
	}

	if _, _, err := run(t, app, "scope", "rename", "wc", "core"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	me := loadMeMap(t, app)
	if _, ok := me["core"]; ok {
		t.Errorf("rename must drop the pointer, got %q", me["core"])
	}
	if _, ok := me["wc"]; ok {
		t.Error("old scope key must be gone from me")
	}
	if got := me["ot"]; got != "ot-aa22" {
		t.Errorf("other scope pointer must be untouched, got %q", got)
	}

	out, _, err := run(t, app, "status", "me", "--scope", "core")
	if err != nil {
		t.Fatalf("status me: %v", err)
	}
	if out != "" {
		t.Errorf("status me after rename must be empty, got %q", out)
	}
	if _, _, err := run(t, app, "get", "me", "--scope", "core"); ExitCodeFromError(err) == 0 {
		t.Fatal("get me after rename must fail as unknown")
	}
}

func TestMeDoesNotCommitOrDirtyScope(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	dir, repo := initGitScope(t, app, "wc", true)
	addTicket(t, dir, "wc-ab2c", "one", "todo", "a0", "# One\n", false, "")
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-m", "tickets")
	before := gitLog(t, repo)

	if _, _, err := run(t, app, "me", "wc-ab2c", "--scope", "wc"); err != nil {
		t.Fatalf("set: %v", err)
	}
	after := gitLog(t, repo)
	if !slicesEqual(after, before) {
		t.Errorf("tk me must not create a commit, log %v -> %v", before, after)
	}
	if _, err := os.Stat(filepath.Join(dir, "me.cue")); !os.IsNotExist(err) {
		t.Error("tk me must not write me.cue into the scope dir")
	}
	cue, err := os.ReadFile(filepath.Join(dir, "tk.cue"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cue), "\nme:") || strings.HasPrefix(string(cue), "me:") {
		t.Errorf("tk.cue must not carry a me field, got %s", cue)
	}
	if porcelain := testgit.Combined(t, repo, "status", "--porcelain"); porcelain != "" {
		t.Errorf("scope repo must stay clean, porcelain=%q", porcelain)
	}
}
