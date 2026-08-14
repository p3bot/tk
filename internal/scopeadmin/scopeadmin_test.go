package scopeadmin

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"github.com/p3bot/tk/internal/pathutil"
	"github.com/p3bot/tk/internal/registry"
	"github.com/p3bot/tk/internal/resolve"
	"github.com/p3bot/tk/internal/scopeconfig"
	"github.com/p3bot/tk/internal/testgit"
	"github.com/p3bot/tk/internal/token"
)

type harness struct {
	ctx       *cue.Context
	admin     *Admin
	configDir string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := cuecontext.New()
	cfg := t.TempDir()
	return &harness{ctx: ctx, admin: New(ctx, cfg), configDir: cfg}
}

func (h *harness) reg(t *testing.T) *registry.Registry {
	t.Helper()
	r, err := registry.NewStore(h.ctx, h.configDir).Load()
	if err != nil {
		t.Fatalf("reload registry: %v", err)
	}
	return r
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	testgit.Run(t, dir, "init")
}

func TestInitPlainFiles(t *testing.T) {
	h := newHarness(t)
	dir := filepath.Join(t.TempDir(), "standalone")
	want := pathutil.Canonical(dir)
	got, err := h.admin.Init(InitParams{Dir: dir, Name: "home"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if got != want {
		t.Errorf("registered dir = %q want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(got, "tk.cue")); err != nil {
		t.Errorf("tk.cue not written: %v", err)
	}
	gi, err := os.ReadFile(filepath.Join(got, ".gitignore"))
	if err != nil || !strings.Contains(string(gi), ".tk.lock") {
		t.Errorf(".gitignore missing .tk.lock: %v %q", err, gi)
	}
	if h.reg(t).Scopes["home"].Root != want {
		t.Errorf("plain-files root should default to dir")
	}
}

func TestInitRepoDefaultsAndAutoName(t *testing.T) {
	h := newHarness(t)
	repo := filepath.Join(t.TempDir(), "webctl")
	gitInit(t, repo)
	dir := filepath.Join(repo, ".agents", "tk")

	got, err := h.admin.Init(InitParams{Dir: dir, AutoName: true, AutoCommit: true, AutoCommitGiven: true})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	_ = got
	entry := h.reg(t).Scopes["we"] // webctl -> "we"
	if entry.Dir != pathutil.Canonical(dir) {
		t.Fatalf("auto-name did not register 'we': %+v", h.reg(t).Scopes)
	}
	// Code-root defaults to the repo root, resolved for symlinked temp roots.
	if entry.Root != pathutil.Canonical(repo) {
		t.Errorf("code-root = %q want repo root %q", entry.Root, pathutil.Canonical(repo))
	}
}

func TestInitExactlyOneName(t *testing.T) {
	h := newHarness(t)
	dir := filepath.Join(t.TempDir(), "s")
	if _, err := h.admin.Init(InitParams{Dir: dir}); err == nil {
		t.Error("expected error when neither --name nor --auto-name is set")
	}
	if _, err := h.admin.Init(InitParams{Dir: dir, Name: "a", AutoName: true}); err == nil {
		t.Error("expected error when both --name and --auto-name are set")
	}
}

func TestInitPreexistingTkCue(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tk.cue"), []byte("name: \"x\"\nautoCommit: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := h.admin.Init(InitParams{Dir: dir, Name: "x"})
	if err == nil || !strings.Contains(err.Error(), "import") {
		t.Fatalf("expected an error pointing at import, got %v", err)
	}
}

func TestInitForeignCodeRoot(t *testing.T) {
	// Tickets dir in its own git repo; ambient code-root may be a foreign product tree.
	h := newHarness(t)
	repo := filepath.Join(t.TempDir(), "tickets")
	gitInit(t, repo)
	dir := filepath.Join(repo, ".agents", "tk")
	outside := filepath.Join(t.TempDir(), "product")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := h.admin.Init(InitParams{Dir: dir, Name: "x", CodeRoot: outside, CodeRootGiven: true}); err != nil {
		t.Fatalf("Init with foreign code-root: %v", err)
	}
	reg := h.reg(t)
	if got := reg.Scopes["x"].Root; got != pathutil.Canonical(outside) {
		t.Errorf("code-root = %q want foreign product tree %q", got, pathutil.Canonical(outside))
	}
	if got := reg.Scopes["x"].Dir; got != pathutil.Canonical(dir) {
		t.Errorf("dir = %q want tickets dir %q", got, pathutil.Canonical(dir))
	}
	cwd := pathutil.Canonical(filepath.Join(outside, "pkg"))
	got, err := resolve.Resolve(h.ctx, reg, resolve.Options{Cwd: cwd})
	if err != nil {
		t.Fatalf("ambient resolve under product tree: %v", err)
	}
	if got.Name != "x" || got.Source != resolve.SourceCwd {
		t.Errorf("ambient = name=%q source=%q want name=x source=%s", got.Name, got.Source, resolve.SourceCwd)
	}
}

func TestInitFailedLeavesNoStrayDir(t *testing.T) {
	h := newHarness(t)
	base := t.TempDir()
	if _, err := h.admin.Init(InitParams{Dir: filepath.Join(base, "one"), Name: "dup"}); err != nil {
		t.Fatal(err)
	}
	// Checks run before mkdir: a rejected init must leave no dir.
	target := filepath.Join(base, "two")
	if _, err := h.admin.Init(InitParams{Dir: target, Name: "dup"}); err == nil {
		t.Fatal("expected name collision")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("failed init must leave no dir; stat(%s) err = %v", target, err)
	}
}

func TestInitFreshDirInRepoDefaultsToRepoRoot(t *testing.T) {
	h := newHarness(t)
	repo := filepath.Join(t.TempDir(), "repo")
	gitInit(t, repo)
	// Dir that does not yet exist still derives repo root — derivation must not create it.
	dir := filepath.Join(repo, ".agents", "tk")
	if _, err := h.admin.Init(InitParams{Dir: dir, Name: "rr"}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if got := h.reg(t).Scopes["rr"].Root; got != pathutil.Canonical(repo) {
		t.Errorf("code-root = %q want repo root %q", got, pathutil.Canonical(repo))
	}
}

func TestInitOutsideRepoExplicitCodeRoot(t *testing.T) {
	h := newHarness(t)
	base := t.TempDir()
	dir := filepath.Join(base, "scope")
	codeRoot := filepath.Join(base, "ticket")
	if _, err := h.admin.Init(InitParams{Dir: dir, Name: "cr", CodeRoot: codeRoot, CodeRootGiven: true}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if got := h.reg(t).Scopes["cr"].Root; got != pathutil.Canonical(codeRoot) {
		t.Errorf("outside-repo explicit code-root = %q want %q", got, pathutil.Canonical(codeRoot))
	}
}

func TestInitAutoCommitInheritAndContradict(t *testing.T) {
	h := newHarness(t)
	repo := filepath.Join(t.TempDir(), "repo")
	gitInit(t, repo)

	a := filepath.Join(repo, "a", ".agents", "tk")
	if _, err := h.admin.Init(InitParams{Dir: a, Name: "aa", CodeRoot: filepath.Join(repo, "a"), CodeRootGiven: true, AutoCommit: true, AutoCommitGiven: true}); err != nil {
		t.Fatalf("first scope: %v", err)
	}

	// Sibling omitting the flag inherits autoCommit=true.
	b := filepath.Join(repo, "b", ".agents", "tk")
	if _, err := h.admin.Init(InitParams{Dir: b, Name: "bb", CodeRoot: filepath.Join(repo, "b"), CodeRootGiven: true}); err != nil {
		t.Fatalf("inherit sibling: %v", err)
	}
	if !mustAutoCommit(t, h.ctx, b) {
		t.Error("sibling should have inherited autoCommit=true")
	}

	// Sibling with a contradicting explicit flag errors with the token.
	c := filepath.Join(repo, "c", ".agents", "tk")
	_, err := h.admin.Init(InitParams{Dir: c, Name: "cc", CodeRoot: filepath.Join(repo, "c"), CodeRootGiven: true, AutoCommit: false, AutoCommitGiven: true})
	if err == nil || !strings.HasPrefix(err.Error(), token.AutoCommitMismatch) {
		t.Fatalf("expected auto_commit_mismatch, got %v", err)
	}
}

func TestInitCollisions(t *testing.T) {
	h := newHarness(t)
	base := t.TempDir()
	first := filepath.Join(base, "one")
	if _, err := h.admin.Init(InitParams{Dir: first, Name: "dup"}); err != nil {
		t.Fatal(err)
	}

	if _, err := h.admin.Init(InitParams{Dir: filepath.Join(base, "two"), Name: "dup"}); err == nil {
		t.Error("expected name collision")
	}
	if _, err := h.admin.Init(InitParams{Dir: filepath.Join(base, "three"), Name: "cr", CodeRoot: first, CodeRootGiven: true}); err == nil {
		t.Error("expected code-root collision")
	}
	nested := filepath.Join(first, "nested")
	if _, err := h.admin.Init(InitParams{Dir: nested, Name: "nd", CodeRoot: filepath.Join(base, "x"), CodeRootGiven: true}); err == nil {
		t.Error("expected dir disjointness rejection")
	}
}

func TestImportReadsConfigAndGuards(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tk.cue"), []byte("name: \"im\"\nautoCommit: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := h.admin.Import(ImportParams{Dir: dir}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if _, ok := h.reg(t).Scopes["im"]; !ok {
		t.Error("import did not register under tk.cue name")
	}

	bad := t.TempDir()
	if err := os.WriteFile(filepath.Join(bad, "tk.cue"), []byte("name: \"b\" broken:::"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := h.admin.Import(ImportParams{Dir: bad})
	if err == nil || !strings.HasPrefix(err.Error(), token.ConfigUnparseable) {
		t.Fatalf("expected config_unparseable on import, got %v", err)
	}
}

func TestImportForeignCodeRoot(t *testing.T) {
	// External ticket store (own git repo) + product tree ambient — single-step import.
	h := newHarness(t)
	tickets := filepath.Join(t.TempDir(), "tickets")
	gitInit(t, tickets)
	dir := filepath.Join(tickets, "fm")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tk.cue"), []byte("name: \"fm\"\nautoCommit: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	product := filepath.Join(t.TempDir(), "fieldmonkeys")
	if err := os.MkdirAll(product, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := h.admin.Import(ImportParams{Dir: dir, CodeRoot: product, CodeRootGiven: true}); err != nil {
		t.Fatalf("Import with foreign code-root: %v", err)
	}
	reg := h.reg(t)
	entry := reg.Scopes["fm"]
	if entry.Root != pathutil.Canonical(product) {
		t.Errorf("root = %q want product %q", entry.Root, pathutil.Canonical(product))
	}
	if entry.Dir != pathutil.Canonical(dir) {
		t.Errorf("dir = %q want tickets dir %q", entry.Dir, pathutil.Canonical(dir))
	}
	cwd := pathutil.Canonical(filepath.Join(product, "cmd", "app"))
	got, err := resolve.Resolve(h.ctx, reg, resolve.Options{Cwd: cwd})
	if err != nil {
		t.Fatalf("ambient resolve under product tree: %v", err)
	}
	if got.Name != "fm" || got.Source != resolve.SourceCwd {
		t.Errorf("ambient = name=%q source=%q want name=fm source=%s", got.Name, got.Source, resolve.SourceCwd)
	}
}

func TestImportAutoCommitMismatch(t *testing.T) {
	h := newHarness(t)
	repo := filepath.Join(t.TempDir(), "repo")
	gitInit(t, repo)

	a := filepath.Join(repo, "a", ".agents", "tk")
	if _, err := h.admin.Init(InitParams{Dir: a, Name: "aa", CodeRoot: filepath.Join(repo, "a"), CodeRootGiven: true, AutoCommit: true, AutoCommitGiven: true}); err != nil {
		t.Fatal(err)
	}

	// Sibling on disk with autoCommit=false disagrees; import cannot inherit.
	b := filepath.Join(repo, "b", ".agents", "tk")
	if err := os.MkdirAll(b, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b, "tk.cue"), []byte("name: \"bb\"\nautoCommit: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := h.admin.Import(ImportParams{Dir: b, CodeRoot: filepath.Join(repo, "b"), CodeRootGiven: true})
	if err == nil || !strings.HasPrefix(err.Error(), token.AutoCommitMismatch) {
		t.Fatalf("expected auto_commit_mismatch, got %v", err)
	}
}

func TestSiblingConfigUnparseableRefusesRegistration(t *testing.T) {
	h := newHarness(t)
	repo := filepath.Join(t.TempDir(), "repo")
	gitInit(t, repo)

	// Registered sibling with broken tk.cue supplies no trustworthy autoCommit.
	a := filepath.Join(repo, "a", ".agents", "tk")
	if _, err := h.admin.Init(InitParams{Dir: a, Name: "aa", CodeRoot: filepath.Join(repo, "a"), CodeRootGiven: true, AutoCommit: true, AutoCommitGiven: true}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a, "tk.cue"), []byte("name: \"aa\" broken:::"), 0o644); err != nil {
		t.Fatal(err)
	}

	b := filepath.Join(repo, "b", ".agents", "tk")
	_, err := h.admin.Init(InitParams{Dir: b, Name: "bb", CodeRoot: filepath.Join(repo, "b"), CodeRootGiven: true, AutoCommit: true, AutoCommitGiven: true})
	if err == nil || !strings.HasPrefix(err.Error(), token.ConfigUnparseable) {
		t.Fatalf("expected config_unparseable naming the broken sibling, got %v", err)
	}
}

func TestRebind(t *testing.T) {
	h := newHarness(t)
	base := t.TempDir()
	orig := filepath.Join(base, "orig")
	if _, err := h.admin.Init(InitParams{Dir: orig, Name: "rb"}); err != nil {
		t.Fatal(err)
	}
	store := registry.NewStore(h.ctx, h.configDir)
	if err := store.WriteLens(map[string][]string{"rb": {"tagx"}}); err != nil {
		t.Fatal(err)
	}

	// Move the dir; keep the same tk.cue (name rb). Root unchanged (no --code-root).
	moved := filepath.Join(base, "moved")
	if err := os.Rename(orig, moved); err != nil {
		t.Fatal(err)
	}
	wantMoved := pathutil.Canonical(moved)
	wantOrig := pathutil.Canonical(orig)
	dir, changed, err := h.admin.Rebind(RebindParams{Dir: moved, Name: "rb"})
	if err != nil || !changed || dir != wantMoved {
		t.Fatalf("rebind: dir=%q changed=%v err=%v", dir, changed, err)
	}
	reg := h.reg(t)
	if reg.Scopes["rb"].Dir != wantMoved {
		t.Errorf("dir not updated: %+v", reg.Scopes["rb"])
	}
	if reg.Scopes["rb"].Root != wantOrig {
		t.Errorf("root should be unchanged on a dir-only move, got %q", reg.Scopes["rb"].Root)
	}
	if got := reg.Lens["rb"]; len(got) != 1 || got[0] != "tagx" {
		t.Errorf("lens not preserved: %v", got)
	}

	if _, changed, err := h.admin.Rebind(RebindParams{Dir: moved, Name: "rb"}); err != nil || changed {
		t.Errorf("expected idempotent no-op, changed=%v err=%v", changed, err)
	}

	if _, _, err := h.admin.Rebind(RebindParams{Dir: moved, Name: "ghost"}); err == nil {
		t.Error("expected unknown-name error")
	}

	wrong := t.TempDir()
	if err := os.WriteFile(filepath.Join(wrong, "tk.cue"), []byte("name: \"other\"\nautoCommit: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.admin.Rebind(RebindParams{Dir: wrong, Name: "rb"}); err == nil {
		t.Error("expected wrong-tree refusal")
	}
}

func TestForget(t *testing.T) {
	h := newHarness(t)
	dir := filepath.Join(t.TempDir(), "f")
	if _, err := h.admin.Init(InitParams{Dir: dir, Name: "fg"}); err != nil {
		t.Fatal(err)
	}
	store := registry.NewStore(h.ctx, h.configDir)
	if err := store.WriteLens(map[string][]string{"fg": {"t"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteMe(map[string]string{"fg": "fg-aa22"}); err != nil {
		t.Fatal(err)
	}

	if err := h.admin.Forget("fg"); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	reg := h.reg(t)
	if _, ok := reg.Scopes["fg"]; ok {
		t.Error("scope still registered after forget")
	}
	if _, ok := reg.Lens["fg"]; ok {
		t.Error("lens still present after forget")
	}
	if _, ok := reg.Me["fg"]; ok {
		t.Error("me still present after forget")
	}
	if _, err := os.Stat(filepath.Join(dir, "tk.cue")); err != nil {
		t.Error("forget must not touch scope files")
	}
	if err := h.admin.Forget("ghost"); err == nil {
		t.Error("expected unknown-scope error")
	}
}

func TestListModesAndDiagnostics(t *testing.T) {
	h := newHarness(t)
	base := t.TempDir()

	plain := filepath.Join(base, "plain")
	if _, err := h.admin.Init(InitParams{Dir: plain, Name: "pl"}); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(base, "repo")
	gitInit(t, repo)
	pdDir := filepath.Join(repo, ".agents", "tk")
	if _, err := h.admin.Init(InitParams{Dir: pdDir, Name: "pd", AutoCommit: true, AutoCommitGiven: true}); err != nil {
		t.Fatal(err)
	}
	repo2 := filepath.Join(base, "repo2")
	gitInit(t, repo2)
	rpd := filepath.Join(repo2, ".agents", "tk")
	if _, err := h.admin.Init(InitParams{Dir: rpd, Name: "rd"}); err != nil {
		t.Fatal(err)
	}

	listing, err := h.admin.List()
	if err != nil {
		t.Fatal(err)
	}
	modes := map[string]string{}
	for _, r := range listing.Rows {
		modes[r.Name] = r.Mode
	}
	if modes["pl"] != ModePlainFiles || modes["pd"] != ModeTkDriven || modes["rd"] != ModeRepoDriven {
		t.Errorf("modes = %v", modes)
	}
	var names []string
	for _, r := range listing.Rows {
		names = append(names, r.Name)
	}
	if strings.Join(names, ",") != "pd,pl,rd" {
		t.Errorf("rows not sorted ascending: %v", names)
	}

	if err := os.WriteFile(filepath.Join(plain, "tk.cue"), []byte("name: \"plnew\"\nautoCommit: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pdDir, "tk.cue"), []byte("name: \"pd\" broken:::"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(rpd); err != nil {
		t.Fatal(err)
	}

	listing, err = h.admin.List()
	if err != nil {
		t.Fatal(err)
	}
	diag := strings.Join(listing.Diagnostics, "\n")
	if !strings.Contains(diag, token.NameDrift) {
		t.Errorf("expected name_drift diagnostic, got:\n%s", diag)
	}
	if !strings.Contains(diag, token.ConfigUnparseable) {
		t.Errorf("expected config_unparseable diagnostic, got:\n%s", diag)
	}
	if !strings.Contains(diag, token.UnreachableScope) {
		t.Errorf("expected unreachable_scope diagnostic, got:\n%s", diag)
	}
	modes = map[string]string{}
	for _, r := range listing.Rows {
		modes[r.Name] = r.Mode
	}
	if modes["pd"] != ModeUnknown || modes["rd"] != ModeUnknown {
		t.Errorf("broken/gone scopes should be unknown: %v", modes)
	}
	if modes["pl"] != ModePlainFiles {
		t.Errorf("drifted scope keeps a real mode from its readable tk.cue: %v", modes["pl"])
	}
}

func TestListDriftAndUnparseableCoEmit(t *testing.T) {
	h := newHarness(t)
	dir := filepath.Join(t.TempDir(), "co")
	if _, err := h.admin.Init(InitParams{Dir: dir, Name: "co"}); err != nil {
		t.Fatal(err)
	}
	// Compiles under a legal name but fails schema validation (autoCommit missing)
	// and drifts: co-emit name_drift + config_unparseable via ReadName fallback.
	if err := os.WriteFile(filepath.Join(dir, "tk.cue"), []byte("name: \"conew\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	listing, err := h.admin.List()
	if err != nil {
		t.Fatal(err)
	}

	var mode string
	for _, r := range listing.Rows {
		if r.Name == "co" {
			mode = r.Mode
		}
	}
	if mode != ModeUnknown {
		t.Errorf("co-emit scope mode = %q want %q", mode, ModeUnknown)
	}

	diag := strings.Join(listing.Diagnostics, "\n")
	if !strings.Contains(diag, token.NameDrift) {
		t.Errorf("expected name_drift diagnostic, got:\n%s", diag)
	}
	if !strings.Contains(diag, token.ConfigUnparseable) {
		t.Errorf("expected config_unparseable diagnostic, got:\n%s", diag)
	}
	if !strings.Contains(diag, "conew") {
		t.Errorf("drift line should name the recovered tk.cue name %q, got:\n%s", "conew", diag)
	}
}

func TestListEmpty(t *testing.T) {
	h := newHarness(t)
	listing, err := h.admin.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(listing.Rows) != 0 || len(listing.Diagnostics) != 0 {
		t.Errorf("empty registry should list nothing, got %+v", listing)
	}
}

func mustAutoCommit(t *testing.T, ctx *cue.Context, dir string) bool {
	t.Helper()
	s, err := scopeconfig.Load(ctx, dir)
	if err != nil {
		t.Fatalf("load tk.cue at %s: %v", dir, err)
	}
	return s.AutoCommit
}
