package syncengine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cuelang.org/go/cue/cuecontext"

	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/pathutil"
	"github.com/p3bot/tk/internal/reconcile"
	"github.com/p3bot/tk/internal/registry"
	"github.com/p3bot/tk/internal/token"
)

func testDeps(t *testing.T, reg *registry.Registry) Deps {
	t.Helper()
	ctx := cuecontext.New()
	db, err := index.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return Deps{
		Ctx:      t.Context(),
		Cue:      ctx,
		StateDir: t.TempDir(),
		Reg:      reg,
		DB:       db,
		Rec:      reconcile.New(db),
	}
}

func writeCue(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tk.cue"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSelectRequiresInput(t *testing.T) {
	deps := testDeps(t, &registry.Registry{Scopes: map[string]registry.Entry{}})
	_, err := Select(deps, Input{})
	if err == nil {
		t.Fatal("Select with neither Ambient nor AllRegistered must error")
	}
}

func TestAllSelectionEmptyRegistry(t *testing.T) {
	deps := testDeps(t, &registry.Registry{Scopes: map[string]registry.Entry{}})
	sel, err := Select(deps, Input{AllRegistered: true})
	if err != nil {
		t.Fatal(err)
	}
	if sel.Candidates != 0 || len(sel.Targets) != 0 {
		t.Fatalf("empty registry: got candidates=%d targets=%d", sel.Candidates, len(sel.Targets))
	}
}

func TestAllSelectionSkipsNonAutoCommit(t *testing.T) {
	dir := pathutil.Canonical(t.TempDir())
	writeCue(t, dir, "name: \"wc\"\nautoCommit: false\n")
	reg := &registry.Registry{Scopes: map[string]registry.Entry{
		"wc": {Dir: dir, Root: dir},
	}}
	deps := testDeps(t, reg)
	sel, err := Select(deps, Input{AllRegistered: true})
	if err != nil {
		t.Fatal(err)
	}
	if sel.Candidates != 0 {
		t.Fatalf("repo-driven scope must not be a sync candidate, got %d", sel.Candidates)
	}
}

func TestAllSelectionUnreachable(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "gone")
	reg := &registry.Registry{Scopes: map[string]registry.Entry{
		"wc": {Dir: missing, Root: missing},
	}}
	deps := testDeps(t, reg)
	sel, err := Select(deps, Input{AllRegistered: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(sel.Unreachable) != 1 {
		t.Fatalf("want 1 unreachable line, got %v", sel.Unreachable)
	}
	if !strings.HasPrefix(sel.Unreachable[0], token.UnreachableScope) {
		t.Fatalf("unreachable line shape: %q", sel.Unreachable[0])
	}
	if sel.Candidates != 0 {
		t.Fatalf("unreachable is not a candidate, got %d", sel.Candidates)
	}
}

func TestAllSelectionAutoCommitWithoutGitRoot(t *testing.T) {
	dir := pathutil.Canonical(t.TempDir())
	writeCue(t, dir, "name: \"wc\"\nautoCommit: true\n")
	reg := &registry.Registry{Scopes: map[string]registry.Entry{
		"wc": {Dir: dir, Root: dir},
	}}
	deps := testDeps(t, reg)
	sel, err := Select(deps, Input{AllRegistered: true})
	if err != nil {
		t.Fatal(err)
	}
	if sel.Candidates != 1 {
		t.Fatalf("auto-commit without git is still a candidate, got %d", sel.Candidates)
	}
	if len(sel.Disabled) != 1 {
		t.Fatalf("want 1 sync_disabled line, got %v", sel.Disabled)
	}
	if !strings.Contains(sel.Disabled[0], token.SyncDisabled) {
		t.Fatalf("disabled line must carry sync_disabled token, got %q", sel.Disabled[0])
	}
	if len(sel.Targets) != 0 {
		t.Fatalf("no git-root → no targets, got %d", len(sel.Targets))
	}
}

func TestAmbientNonAutoCommitPlainFiles(t *testing.T) {
	dir := pathutil.Canonical(t.TempDir())
	writeCue(t, dir, "name: \"wc\"\nautoCommit: false\n")
	reg := &registry.Registry{Scopes: map[string]registry.Entry{
		"wc": {Dir: dir, Root: dir},
	}}
	deps := testDeps(t, reg)
	_, err := Select(deps, Input{Ambient: &AmbientScope{Name: "wc", Dir: dir}})
	if err == nil {
		t.Fatal("ambient plain-files non-auto-commit must refuse")
	}
	if !strings.Contains(err.Error(), "plain-files") {
		t.Fatalf("want plain-files refusal, got %v", err)
	}
}

func TestAmbientAutoCommitWithoutGitRoot(t *testing.T) {
	dir := pathutil.Canonical(t.TempDir())
	writeCue(t, dir, "name: \"wc\"\nautoCommit: true\n")
	reg := &registry.Registry{Scopes: map[string]registry.Entry{
		"wc": {Dir: dir, Root: dir},
	}}
	deps := testDeps(t, reg)
	sel, err := Select(deps, Input{Ambient: &AmbientScope{Name: "wc", Dir: dir}})
	if err != nil {
		t.Fatal(err)
	}
	if sel.Candidates != 1 || len(sel.Disabled) != 1 {
		t.Fatalf("want candidates=1 disabled=1, got candidates=%d disabled=%v", sel.Candidates, sel.Disabled)
	}
}
