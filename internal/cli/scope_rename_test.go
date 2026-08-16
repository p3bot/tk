package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/registry"
)

func TestScopeRenameEndToEnd(t *testing.T) {
	app := newApp(t)
	wcDir := initScope(t, app, "wc")
	apiDir := initScope(t, app, "api")
	addTicket(t, wcDir, "wc-ab2c", "target", "todo", "a0", "# Target\n", false, "")
	addTicket(t, wcDir, "wc-de34", "dep", "todo", "a1", "# Dep\n", false, "depends: [wc-ab2c]\n")
	addTicket(t, apiDir, "api-mm22", "x", "todo", "a0", "# X\n", false, "depends: [wc-ab2c]\n")

	out, _, err := run(t, app, "scope", "rename", "wc", "core")
	if err != nil {
		t.Fatalf("rename: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(wcDir, "tk.cue"))
	if !strings.Contains(string(data), `"core"`) || strings.Contains(string(data), `"wc"`) {
		t.Errorf("tk.cue name not rewritten to core: %q", data)
	}
	if !fileExists(wcDir, "core-ab2c-target.md") || !fileExists(wcDir, "core-de34-dep.md") {
		t.Errorf("filenames not rewritten to the new scope: %v", ticketFiles(t, wcDir))
	}
	if fileExists(wcDir, "wc-ab2c-target.md") {
		t.Errorf("old-prefixed file must be removed")
	}
	if id := fmValue(t, filepath.Join(wcDir, "core-ab2c-target.md"), "id"); id != "core-ab2c" {
		t.Errorf("frontmatter id not rewritten, got %q", id)
	}
	dep, _ := os.ReadFile(filepath.Join(wcDir, "core-de34-dep.md"))
	if !strings.Contains(string(dep), "core-ab2c") || strings.Contains(string(dep), "wc-ab2c") {
		t.Errorf("in-scope edge not re-keyed: %q", dep)
	}
	if !strings.Contains(out, "edge_verify:") || !strings.Contains(out, "api-mm22") {
		t.Errorf("cross-scope inbound edge should be reported, got %q", out)
	}
	if strings.Count(out, "edge_verify:") != 1 {
		t.Errorf("only the one cross-scope inbound edge should be reported, got %q", out)
	}
	apiFile, _ := os.ReadFile(filepath.Join(apiDir, "api-mm22-x.md"))
	if !strings.Contains(string(apiFile), "wc-ab2c") {
		t.Errorf("cross-scope edge must not be rewritten: %q", apiFile)
	}
	list, _, _ := run(t, app, "scope", "list")
	for _, row := range strings.Split(strings.TrimRight(list, "\n"), "\n") {
		if strings.HasPrefix(row, "wc\t") {
			t.Errorf("old scope name must be gone from the registry listing: %q", row)
		}
	}
	if !strings.Contains(list, "core\t") {
		t.Errorf("new scope name must appear in the registry listing: %q", list)
	}
	if _, _, err := run(t, app, "get", "core-ab2c"); err != nil {
		t.Errorf("ordinary verb must resolve under the new name, got %v", err)
	}
	if _, _, err := run(t, app, "get", "wc-ab2c"); err == nil {
		t.Errorf("the old name must no longer resolve")
	}
}

func TestScopeRenameRePrefixesTheFrontmatterID(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "auth", "todo", "a0", "# Auth\n", false, "")
	if err := os.Rename(filepath.Join(dir, "wc-ab2c-auth.md"), filepath.Join(dir, "wc-zz9y-auth.md")); err != nil {
		t.Fatal(err)
	}

	if _, _, err := run(t, app, "scope", "rename", "wc", "core"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if !fileExists(dir, "core-ab2c-auth.md") {
		t.Fatalf("the filename must follow the declared id, files=%v", ticketFiles(t, dir))
	}
	if got := fmValue(t, filepath.Join(dir, "core-ab2c-auth.md"), "id"); got != "core-ab2c" {
		t.Errorf("declared id must be re-prefixed, got %q want core-ab2c", got)
	}
}

func TestScopeRenameRefusesForeignFrontmatterID(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "other-ab2c", "auth", "todo", "a0", "# Auth\n", false, "")
	if err := os.Rename(filepath.Join(dir, "other-ab2c-auth.md"), filepath.Join(dir, "wc-ab2c-auth.md")); err != nil {
		t.Fatal(err)
	}

	_, _, err := run(t, app, "scope", "rename", "wc", "core")
	if err == nil {
		t.Fatal("a foreign frontmatter id must refuse the rename")
	}
	if !strings.Contains(err.Error(), "other-ab2c") {
		t.Errorf("the refusal must name the offending id, got %v", err)
	}
	if !fileExists(dir, "wc-ab2c-auth.md") {
		t.Errorf("a refused rename must not have moved anything, files=%v", ticketFiles(t, dir))
	}
}

func TestScopeRenameValidation(t *testing.T) {
	app := newApp(t)
	initScope(t, app, "wc")
	initScope(t, app, "api")

	cases := []struct {
		name string
		args []string
		want int
	}{
		{"bad new name", []string{"scope", "rename", "wc", "BAD"}, exitUsage},
		{"same name", []string{"scope", "rename", "wc", "wc"}, exitUsage},
		{"unknown old", []string{"scope", "rename", "ghost", "core"}, exitFailure},
		{"taken new", []string{"scope", "rename", "wc", "api"}, exitFailure},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := run(t, app, c.args...)
			if got := ExitCodeFromError(err); got != c.want {
				t.Errorf("exit = %d want %d (err=%v)", got, c.want, err)
			}
		})
	}
}

func TestScopeRenameIdempotentReentry(t *testing.T) {
	app := newApp(t)
	wcDir := initScope(t, app, "wc")
	addTicket(t, wcDir, "wc-ab2c", "target", "todo", "a0", "# Target\n", false, "")

	if err := os.Rename(filepath.Join(wcDir, "wc-ab2c-target.md"), filepath.Join(wcDir, "core-ab2c-target.md")); err != nil {
		t.Fatal(err)
	}
	migrated := "---\nid: core-ab2c\nstatus: todo\norder: \"a0\"\ncreated: 2026-01-01T00:00:00Z\n---\n# Target\n"
	if err := os.WriteFile(filepath.Join(wcDir, "core-ab2c-target.md"), []byte(migrated), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wcDir, "tk.cue"), []byte("name: \"core\"\nautoCommit: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Ordinary verbs on the drifted scope fail closed until the re-run completes.
	if _, _, err := run(t, app, "list", "--scope", "wc"); err == nil {
		t.Errorf("drifted scope should fail closed for ordinary verbs")
	}
	// The re-run is the exempt path that finishes the tail.
	if _, _, err := run(t, app, "scope", "rename", "wc", "core"); err != nil {
		t.Fatalf("idempotent re-run should complete: %v", err)
	}
	if _, _, err := run(t, app, "get", "core-ab2c"); err != nil {
		t.Errorf("scope should resolve under the new name after re-run, got %v", err)
	}
}

func TestScopeRenameRefusesNameTakenUnderLock(t *testing.T) {
	app := newApp(t)
	wcDir := initScope(t, app, "wc")
	addTicket(t, wcDir, "wc-ab2c", "x", "todo", "a0", "# X\n", false, "")

	e, err := app.openEngine(newRootCmd(app))
	if err != nil {
		t.Fatal(err)
	}
	defer e.close()

	// "core" is registered against a different dir before the re-key runs.
	victimIn := filepath.Join(t.TempDir(), "core")
	if err := os.MkdirAll(victimIn, 0o755); err != nil {
		t.Fatal(err)
	}
	out, _, err := run(t, app, "scope", "init", victimIn, "--name", "core")
	if err != nil {
		t.Fatalf("concurrent init: %v", err)
	}
	// Registry stores the canonical dir (macOS /var vs /private/var).
	victim := strings.TrimSpace(out)

	err = e.rekeyRegistry("wc", "core")
	if err == nil {
		t.Fatal("re-key must refuse a name registered under the lock, not clobber it")
	}
	// The refusal names the split state, since the in-dir rewrite cannot be rolled back.
	for _, want := range []string{"machine-unique", "already renamed on disk", "forget"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal should mention %q, got %v", want, err)
		}
	}

	list, _, _ := run(t, app, "scope", "list")
	if !strings.Contains(list, victim) {
		t.Errorf("the concurrently registered scope must keep its dir binding, got %q", list)
	}
	if strings.Contains(list, "core\t"+wcDir) {
		t.Errorf("the renamed scope must not have taken over the core key, got %q", list)
	}
}

func TestScopeRenameUnknownOldLeavesSessionMaps(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "one", "todo", "a0", "# One\n", false, "")
	if _, _, err := run(t, app, "me", "wc-ab2c", "--scope", "wc"); err != nil {
		t.Fatalf("set me: %v", err)
	}
	if _, _, err := run(t, app, "lens", "frontend", "--scope", "wc"); err != nil {
		t.Fatalf("set lens: %v", err)
	}
	if _, _, err := run(t, app, "note", "use", "grant", "--scope", "wc"); err != nil {
		t.Fatalf("set note: %v", err)
	}

	// Crash after WriteRegistry: scopes already on core, maps still on wc.
	if err := os.WriteFile(filepath.Join(dir, "tk.cue"), []byte("name: \"core\"\nautoCommit: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := registry.NewStore(app.Ctx, app.ConfigDir)
	reg, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	entry := reg.Scopes["wc"]
	delete(reg.Scopes, "wc")
	reg.Scopes["core"] = entry
	if err := store.WriteRegistry(reg.Scopes); err != nil {
		t.Fatal(err)
	}

	_, _, err = run(t, app, "scope", "rename", "wc", "core")
	if err == nil {
		t.Fatal("rename of an unregistered name must refuse, not attach leftover maps")
	}
	if !strings.Contains(err.Error(), `unknown scope "wc"`) {
		t.Errorf("want unknown scope, got %v", err)
	}
	reg, err = store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := reg.Me["wc"]; got != "wc-ab2c" {
		t.Errorf("leftover me must stay under the old key, got %q", got)
	}
	if _, ok := reg.Me["core"]; ok {
		t.Error("leftover me must not be attached to the live scope")
	}
	if got := reg.Lens["wc"]; len(got) != 1 || got[0] != "frontend" {
		t.Errorf("leftover lens must stay under the old key, got %v", got)
	}
	if _, ok := reg.Lens["core"]; ok {
		t.Error("leftover lens must not be attached to the live scope")
	}
	if got := reg.Note["wc"]; got != "grant" {
		t.Errorf("leftover note must stay under the old key, got %q", got)
	}
	if _, ok := reg.Note["core"]; ok {
		t.Error("leftover note must not be attached to the live scope")
	}
}

func TestScopeRenameUnknownOldDoesNotClobberLiveSessionMaps(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "one", "todo", "a0", "# One\n", false, "")
	if _, _, err := run(t, app, "me", "wc-ab2c", "--scope", "wc"); err != nil {
		t.Fatalf("set me: %v", err)
	}
	if _, _, err := run(t, app, "lens", "frontend", "--scope", "wc"); err != nil {
		t.Fatalf("set lens: %v", err)
	}
	if _, _, err := run(t, app, "note", "use", "grant", "--scope", "wc"); err != nil {
		t.Fatalf("set note: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "tk.cue"), []byte("name: \"core\"\nautoCommit: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := registry.NewStore(app.Ctx, app.ConfigDir)
	reg, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	entry := reg.Scopes["wc"]
	delete(reg.Scopes, "wc")
	reg.Scopes["core"] = entry
	if err := store.WriteRegistry(reg.Scopes); err != nil {
		t.Fatal(err)
	}

	reg.Me["core"] = "core-de34"
	reg.Lens["core"] = []string{"backend"}
	reg.Note["core"] = "alice"
	if err := store.WriteMe(reg.Me); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteLens(reg.Lens); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteNote(reg.Note); err != nil {
		t.Fatal(err)
	}

	_, _, err = run(t, app, "scope", "rename", "wc", "core")
	if err == nil {
		t.Fatal("rename of an unregistered name must refuse")
	}
	reg, err = store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := reg.Me["core"]; got != "core-de34" {
		t.Errorf("live me must be untouched, got %q", got)
	}
	if got := reg.Me["wc"]; got != "wc-ab2c" {
		t.Errorf("leftover me must stay under the old key, got %q", got)
	}
	if got := reg.Lens["core"]; len(got) != 1 || got[0] != "backend" {
		t.Errorf("live lens must be untouched, got %v", got)
	}
	if got := reg.Lens["wc"]; len(got) != 1 || got[0] != "frontend" {
		t.Errorf("leftover lens must stay under the old key, got %v", got)
	}
	if got := reg.Note["core"]; got != "alice" {
		t.Errorf("live note must be untouched, got %q", got)
	}
	if got := reg.Note["wc"]; got != "grant" {
		t.Errorf("leftover note must stay under the old key, got %q", got)
	}
}

func TestScopeRenameLeftoverMapsDoNotAttachToUnrelatedScope(t *testing.T) {
	app := newApp(t)
	initScope(t, app, "api")
	store := registry.NewStore(app.Ctx, app.ConfigDir)
	if err := store.WriteMe(map[string]string{"ghost": "ghost-ab2c"}); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteLens(map[string][]string{"ghost": {"frontend"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteNote(map[string]string{"ghost": "grant"}); err != nil {
		t.Fatal(err)
	}

	_, _, err := run(t, app, "scope", "rename", "ghost", "api")
	if err == nil {
		t.Fatal("leftover maps must not make rename of an unknown name succeed")
	}
	if !strings.Contains(err.Error(), `unknown scope "ghost"`) {
		t.Errorf("want unknown scope, got %v", err)
	}
	reg, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := reg.Me["ghost"]; got != "ghost-ab2c" {
		t.Errorf("ghost me leftover = %q", got)
	}
	if _, ok := reg.Me["api"]; ok {
		t.Error("api must not inherit leftover me")
	}
	if got := reg.Lens["ghost"]; len(got) != 1 || got[0] != "frontend" {
		t.Errorf("ghost lens leftover = %v", got)
	}
	if _, ok := reg.Lens["api"]; ok {
		t.Error("api must not inherit leftover lens")
	}
	if got := reg.Note["ghost"]; got != "grant" {
		t.Errorf("ghost note leftover = %q", got)
	}
	if _, ok := reg.Note["api"]; ok {
		t.Error("api must not inherit leftover note")
	}
}

func TestScopeRenameClearsOrphanedTargetMeWhenSourceHasNone(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "one", "todo", "a0", "# One\n", false, "")
	store := registry.NewStore(app.Ctx, app.ConfigDir)
	if err := store.WriteMe(map[string]string{"core": "core-zzzz"}); err != nil {
		t.Fatal(err)
	}

	if _, _, err := run(t, app, "scope", "rename", "wc", "core"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	reg, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Me["core"]; ok {
		t.Errorf("orphaned target me must be dropped, got %q", reg.Me["core"])
	}
	if _, ok := reg.Me["wc"]; ok {
		t.Error("old me key must stay absent")
	}
}

func TestScopeRenameLeavesOrphanedTargetNoteWhenSourceHasNone(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "one", "todo", "a0", "# One\n", false, "")
	store := registry.NewStore(app.Ctx, app.ConfigDir)
	if err := store.WriteNote(map[string]string{"core": "alice"}); err != nil {
		t.Fatal(err)
	}

	if _, _, err := run(t, app, "scope", "rename", "wc", "core"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	reg, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := reg.Note["core"]; got != "alice" {
		t.Errorf("orphaned target note must stay when source has none, got %q", got)
	}
	if _, ok := reg.Note["wc"]; ok {
		t.Error("old note key must stay absent")
	}
}

func TestScopeRenameOverwritesOrphanedTargetSessionMaps(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "one", "todo", "a0", "# One\n", false, "")
	if _, _, err := run(t, app, "me", "wc-ab2c", "--scope", "wc"); err != nil {
		t.Fatalf("set me: %v", err)
	}
	if _, _, err := run(t, app, "lens", "frontend", "--scope", "wc"); err != nil {
		t.Fatalf("set lens: %v", err)
	}
	if _, _, err := run(t, app, "note", "use", "grant", "--scope", "wc"); err != nil {
		t.Fatalf("set note: %v", err)
	}
	store := registry.NewStore(app.Ctx, app.ConfigDir)
	reg, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	reg.Me["core"] = "core-zzzz"
	reg.Lens["core"] = []string{"stale"}
	reg.Note["core"] = "stale"
	if err := store.WriteMe(reg.Me); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteLens(reg.Lens); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteNote(reg.Note); err != nil {
		t.Fatal(err)
	}

	if _, _, err := run(t, app, "scope", "rename", "wc", "core"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	reg, err = store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Me["core"]; ok {
		t.Errorf("rename must drop me, including an orphaned target, got %q", reg.Me["core"])
	}
	if _, ok := reg.Me["wc"]; ok {
		t.Error("old me key must be gone")
	}
	if got := reg.Lens["core"]; len(got) != 1 || got[0] != "frontend" {
		t.Errorf("rename must overwrite orphaned target lens, got %v", got)
	}
	if got := reg.Note["core"]; got != "grant" {
		t.Errorf("rename must overwrite orphaned target note, got %q", got)
	}
	if _, ok := reg.Note["wc"]; ok {
		t.Error("old note key must be gone")
	}
}

func TestScopeRenameRekeyStaysIdempotentAfterCompletion(t *testing.T) {
	app := newApp(t)
	initScope(t, app, "wc")

	e, err := app.openEngine(newRootCmd(app))
	if err != nil {
		t.Fatal(err)
	}
	defer e.close()

	if err := e.rekeyRegistry("wc", "core"); err != nil {
		t.Fatalf("first re-key: %v", err)
	}
	if err := e.rekeyRegistry("wc", "core"); err != nil {
		t.Errorf("re-key of a completed rename must stay a no-op, got %v", err)
	}
}

func TestScopeRenameRejectsGenuineDrift(t *testing.T) {
	app := newApp(t)
	wcDir := initScope(t, app, "wc")
	if err := os.WriteFile(filepath.Join(wcDir, "tk.cue"), []byte("name: \"other\"\nautoCommit: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := run(t, app, "scope", "rename", "wc", "core")
	if err == nil {
		t.Fatalf("rename must refuse genuine drift")
	}
	if !strings.Contains(err.Error(), "drift") && !strings.Contains(err.Error(), "forget") {
		t.Errorf("refusal should point at forget+import recovery, got %v", err)
	}
}
