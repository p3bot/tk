package notes

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cuelang.org/go/cue/cuecontext"

	"github.com/p3bot/tk/internal/git"
	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/reconcile"
	"github.com/p3bot/tk/internal/registry"
	"github.com/p3bot/tk/internal/scopefile"
	"github.com/p3bot/tk/internal/testgit"
	"github.com/p3bot/tk/internal/token"
)

type env struct {
	deps Deps
	name string
	dir  string
	root string
}

func newPlainEnv(t *testing.T) *env {
	t.Helper()
	const name = "wc"
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "tk.cue"), "name: \"wc\"\nautoCommit: false\n")
	return openEnv(t, name, dir, dir)
}

func openEnv(t *testing.T, name, dir, root string) *env {
	t.Helper()
	cue := cuecontext.New()
	db, err := index.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	configDir := t.TempDir()
	reg := &registry.Registry{
		Scopes: map[string]registry.Entry{name: {Dir: dir, Root: root}},
		Lens:   map[string][]string{},
		Me:     map[string]string{},
		Note:   map[string]string{},
	}
	return &env{
		deps: Deps{
			Ctx:       context.Background(),
			Cue:       cue,
			StateDir:  t.TempDir(),
			ConfigDir: configDir,
			Reg:       reg,
			Rec:       reconcile.New(db, cue),
		},
		name: name,
		dir:  dir,
		root: root,
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (e *env) input(name string) Input {
	sel := Selector{}
	if name != "" {
		sel.Name = name
		sel.NameSet = true
	}
	return Input{Scope: e.name, Dir: e.dir, Selector: sel}
}

func defaultPath(dir string) string {
	return filepath.Join(dir, scopefile.NoteDir, scopefile.NoteDefaultSlug+".md")
}

func namedPath(dir, name string) string {
	return filepath.Join(dir, scopefile.NoteDir, name+".md")
}

func TestReadMissingEmptyAndNamed(t *testing.T) {
	e := newPlainEnv(t)

	res, err := Read(e.deps, e.input(""), true)
	if err != nil {
		t.Fatalf("missing default: %v", err)
	}
	if len(res.Body) != 0 {
		t.Errorf("missing default body = %q", res.Body)
	}

	_, err = Read(e.deps, e.input("foo"), false)
	var miss *MissingError
	if !errors.As(err, &miss) {
		t.Fatalf("named missing: %v", err)
	}
	if !strings.Contains(miss.Path, namedPath(e.dir, "foo")) && !strings.HasSuffix(miss.Path, "foo.md") {
		t.Errorf("missing path = %q", miss.Path)
	}

	writeFile(t, defaultPath(e.dir), "")
	res, err = Read(e.deps, e.input(""), true)
	if err != nil {
		t.Fatalf("empty file: %v", err)
	}
	if len(res.Body) != 0 {
		t.Errorf("empty file body = %q", res.Body)
	}

	writeFile(t, namedPath(e.dir, "decisions"), "hello\n")
	res, err = Read(e.deps, e.input("decisions"), false)
	if err != nil {
		t.Fatalf("named cat: %v", err)
	}
	if string(res.Body) != "hello\n" {
		t.Errorf("body = %q", res.Body)
	}
	if res.Slug != "decisions" {
		t.Errorf("slug = %q", res.Slug)
	}
}

func TestReadNonRegular(t *testing.T) {
	e := newPlainEnv(t)
	if err := os.MkdirAll(defaultPath(e.dir), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := Read(e.deps, e.input(""), true)
	var nr *NonRegularError
	if !errors.As(err, &nr) {
		t.Fatalf("directory cat: %v", err)
	}
}

func TestListAddressable(t *testing.T) {
	e := newPlainEnv(t)

	got, err := List(e.dir)
	if err != nil {
		t.Fatalf("missing list: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("missing list = %v", got)
	}

	if _, err := Add(e.deps, e.input("zeta"), "z"); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(e.deps, e.input("alpha"), "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(e.deps, e.input(""), "d"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, namedPath(e.dir, "list"), "residue\n")
	writeFile(t, namedPath(e.dir, "Not A Slug"), "bad\n")

	got, err = List(e.dir)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if strings.Join(got, ",") != "alpha,default,zeta" {
		t.Errorf("list = %v", got)
	}
}

func TestSetAndAdd(t *testing.T) {
	e := newPlainEnv(t)

	_, err := Set(e.deps, e.input(""), nil)
	var use *UsageError
	if !errors.As(err, &use) || use.Msg != "set needs non-empty text" {
		t.Errorf("empty set: %v", err)
	}

	res, err := Set(e.deps, e.input(""), []byte("first"))
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	if res.Slug != "default" {
		t.Errorf("slug = %q", res.Slug)
	}
	got, err := os.ReadFile(defaultPath(e.dir))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "first\n" {
		t.Errorf("set body = %q", got)
	}

	if _, err := Set(e.deps, e.input(""), []byte("replaced body\n")); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(defaultPath(e.dir))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "replaced body\n" {
		t.Errorf("replace = %q", got)
	}

	if err := os.WriteFile(defaultPath(e.dir), []byte("no-nl"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(e.deps, e.input(""), "next"); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(defaultPath(e.dir))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "no-nl\nnext\n" {
		t.Errorf("glue = %q", got)
	}

	_, err = Add(e.deps, e.input(""), "")
	if !errors.As(err, &use) || use.Msg != "add needs non-empty text" {
		t.Errorf("empty add: %v", err)
	}
}

func TestDeleteMissingAndEmptyDir(t *testing.T) {
	e := newPlainEnv(t)

	res, err := Delete(e.deps, e.input(""))
	if err != nil {
		t.Fatalf("delete missing: %v", err)
	}
	if res.Path != "" || res.SyncNeeded != "" {
		t.Errorf("missing delete result = %+v", res)
	}

	if _, err := Add(e.deps, e.input("keep"), "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(e.deps, e.input(""), "y"); err != nil {
		t.Fatal(err)
	}
	if _, err := Delete(e.deps, e.input("")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.dir, scopefile.NoteDir)); err != nil {
		t.Fatalf("notes/ should remain: %v", err)
	}
	if _, err := Delete(e.deps, e.input("keep")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.dir, scopefile.NoteDir)); !os.IsNotExist(err) {
		t.Errorf("last delete should rmdir notes/, stat err=%v", err)
	}
}

func TestUsePointer(t *testing.T) {
	e := newPlainEnv(t)

	res, err := Use(e.deps, UseInput{Scope: "wc"})
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	if res.Slug != "default" {
		t.Errorf("fresh show = %q", res.Slug)
	}

	res, err = Use(e.deps, UseInput{Scope: "wc", Slug: "grant"})
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	if res.SyncNeeded != "" {
		t.Errorf("use must not set SyncNeeded, got %q", res.SyncNeeded)
	}
	if _, err := os.Stat(namedPath(e.dir, "grant")); !os.IsNotExist(err) {
		t.Errorf("use must not create the file, stat err=%v", err)
	}
	store := registry.NewStore(e.deps.Cue, e.deps.ConfigDir)
	reg, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if reg.Note["wc"] != "grant" {
		t.Errorf("stored = %q", reg.Note["wc"])
	}

	e.deps.Reg = reg
	res, err = Use(e.deps, UseInput{Scope: "wc"})
	if err != nil {
		t.Fatalf("show after set: %v", err)
	}
	if res.Slug != "grant" {
		t.Errorf("show = %q", res.Slug)
	}

	if _, err := Use(e.deps, UseInput{Scope: "wc", Slug: "default"}); err != nil {
		t.Fatal(err)
	}
	reg, err = store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Note["wc"]; ok {
		t.Error("use default must delete the key")
	}

	if _, err := Use(e.deps, UseInput{Scope: "wc", Slug: "grant"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Use(e.deps, UseInput{Scope: "wc", Clear: true}); err != nil {
		t.Fatal(err)
	}
	reg, err = store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Note["wc"]; ok {
		t.Error("--clear must delete the key")
	}

	_, err = Use(e.deps, UseInput{Scope: "wc", Slug: "use"})
	var use *UsageError
	if !errors.As(err, &use) {
		t.Errorf("reserved: %v", err)
	}
}

func TestEffectiveSlugInvalidStored(t *testing.T) {
	e := newPlainEnv(t)
	e.deps.Reg.Note["wc"] = "Grant"
	_, err := EffectiveSlug(e.deps.Reg, e.deps.ConfigDir, "wc")
	if err == nil {
		t.Fatal("expected hard error")
	}
	if !strings.Contains(err.Error(), "note.cue") {
		t.Errorf("error should name note.cue, got %v", err)
	}
}

func TestRequireDirUnreachable(t *testing.T) {
	err := RequireDir("wc", filepath.Join(t.TempDir(), "missing"))
	if err == nil {
		t.Fatal("expected unreachable")
	}
	if !strings.Contains(err.Error(), token.UnreachableScope) {
		t.Errorf("want unreachable_scope:, got %v", err)
	}
}

func TestPrepareAndFinishEdit(t *testing.T) {
	e := newPlainEnv(t)
	res, err := PrepareEdit(e.deps, e.input("scratch"))
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.dir, scopefile.NoteDir)); err != nil {
		t.Fatalf("notes/ should exist: %v", err)
	}
	if _, err := os.Stat(namedPath(e.dir, "scratch")); !os.IsNotExist(err) {
		t.Errorf("prepare must not create the file, stat err=%v", err)
	}
	if err := FinishEdit(e.dir, res.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.dir, scopefile.NoteDir)); !os.IsNotExist(err) {
		t.Errorf("empty notes/ should be removed, stat err=%v", err)
	}

	res, err = PrepareEdit(e.deps, e.input(""))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(res.Path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := FinishEdit(e.dir, res.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(defaultPath(e.dir)); !os.IsNotExist(err) {
		t.Errorf("zero-byte should be unlinked, stat err=%v", err)
	}
}

func TestWritesNeverSelfCommit(t *testing.T) {
	if !git.Available() {
		t.Skip("git not on PATH")
	}
	testgit.Hermetic(t)
	repo := t.TempDir()
	testgit.Run(t, repo, "init", "-b", "main")
	testgit.Run(t, repo, "config", "user.email", "a@b.c")
	testgit.Run(t, repo, "config", "user.name", "tk-test")
	testgit.Run(t, repo, "config", "commit.gpgsign", "false")
	dir := filepath.Join(repo, "wc")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "tk.cue"), "name: \"wc\"\nautoCommit: true\n")
	e := openEnv(t, "wc", dir, repo)
	testgit.Run(t, repo, "add", "-A")
	testgit.Run(t, repo, "commit", "-m", "init")
	head := testgit.Combined(t, repo, "rev-parse", "HEAD")

	res, err := Add(e.deps, e.input("decisions"), "hello")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if res.SyncNeeded != "dirty" {
		t.Errorf("tk-driven add SyncNeeded = %q want dirty", res.SyncNeeded)
	}
	if testgit.Combined(t, repo, "rev-parse", "HEAD") != head {
		t.Errorf("add must not commit")
	}

	res, err = Use(e.deps, UseInput{Scope: "wc", Slug: "grant"})
	if err != nil {
		t.Fatal(err)
	}
	if res.SyncNeeded != "" {
		t.Errorf("use SyncNeeded = %q", res.SyncNeeded)
	}
}

func TestSelectNameUsage(t *testing.T) {
	_, err := SelectName("a", "b", true, "default")
	var use *UsageError
	if !errors.As(err, &use) || use.Msg != "use a positional slug or --name, not both" {
		t.Errorf("mix: %v", err)
	}
	_, err = SelectName("list", "", false, "default")
	if !errors.As(err, &use) {
		t.Errorf("reserved: %v", err)
	}
}
