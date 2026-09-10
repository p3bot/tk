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

func (e *env) input(slug string) Input {
	if slug == "" {
		slug = scopefile.NoteDefaultSlug
	}
	return Input{Scope: e.name, Dir: e.dir, Slug: slug}
}

func defaultPath(dir string) string {
	return filepath.Join(dir, scopefile.NoteDir, scopefile.NoteDefaultSlug+".md")
}

func namedPath(dir, name string) string {
	return filepath.Join(dir, scopefile.NoteDir, name+".md")
}

func TestReadMissingEmptyAndNamed(t *testing.T) {
	e := newPlainEnv(t)

	_, err := Read(e.input(""))
	var miss *MissingError
	if !errors.As(err, &miss) {
		t.Fatalf("missing default: %v", err)
	}

	_, err = Read(e.input("foo"))
	if !errors.As(err, &miss) {
		t.Fatalf("named missing: %v", err)
	}
	if !strings.Contains(miss.Path, namedPath(e.dir, "foo")) && !strings.HasSuffix(miss.Path, "foo.md") {
		t.Errorf("missing path = %q", miss.Path)
	}

	writeFile(t, defaultPath(e.dir), "")
	res, err := Read(e.input(""))
	if err != nil {
		t.Fatalf("empty file: %v", err)
	}
	if len(res.Body) != 0 {
		t.Errorf("empty file body = %q", res.Body)
	}

	writeFile(t, namedPath(e.dir, "decisions"), "hello\n")
	res, err = Read(e.input("decisions"))
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

func TestRefuseNonRegular(t *testing.T) {
	e := newPlainEnv(t)
	if err := os.MkdirAll(defaultPath(e.dir), 0o755); err != nil {
		t.Fatal(err)
	}
	var nr *NonRegularError
	_, err := Read(e.input(""))
	if !errors.As(err, &nr) {
		t.Fatalf("directory cat: %v", err)
	}
	_, err = Set(e.deps, e.input(""), []byte("x"))
	if !errors.As(err, &nr) {
		t.Fatalf("directory set: %v", err)
	}
	_, err = Add(e.deps, e.input(""), "x")
	if !errors.As(err, &nr) {
		t.Fatalf("directory add: %v", err)
	}
	_, err = Delete(e.deps, e.input(""))
	if !errors.As(err, &nr) {
		t.Fatalf("directory delete: %v", err)
	}
}

func TestListAddressable(t *testing.T) {
	e := newPlainEnv(t)

	got, err := List(e.name, e.dir)
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

	got, err = List(e.name, e.dir)
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

	if _, err := Set(e.deps, e.input(""), []byte("a\r\nb\rc\n")); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(defaultPath(e.dir))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "\r") {
		t.Errorf("CR on disk: %q", got)
	}
	if string(got) != "a\nb\nc\n" {
		t.Errorf("crlf set = %q", got)
	}

	if _, err := Set(e.deps, e.input(""), []byte("a\nb\nc\n")); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(defaultPath(e.dir))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "\r") {
		t.Errorf("LF no-op introduced CR: %q", got)
	}
	if string(got) != "a\nb\nc\n" {
		t.Errorf("LF no-op = %q", got)
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

func TestFileSnapshotMissingAndPresent(t *testing.T) {
	e := newPlainEnv(t)
	data, key, err := FileSnapshot(namedPath(e.dir, "ghost"))
	if err != nil {
		t.Fatal(err)
	}
	if data != nil || key != MissingClobberKey {
		t.Fatalf("missing snapshot data=%q key=%q", data, key)
	}
	writeFile(t, namedPath(e.dir, "pad"), "hello\n")
	data, key, err = FileSnapshot(namedPath(e.dir, "pad"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello\n" {
		t.Fatalf("body = %q", data)
	}
	got, err := FileClobberKey(namedPath(e.dir, "pad"))
	if err != nil {
		t.Fatal(err)
	}
	if got != key {
		t.Fatalf("key %q vs FileClobberKey %q", key, got)
	}
}

func TestFileSnapshotNonRegular(t *testing.T) {
	e := newPlainEnv(t)
	if err := os.MkdirAll(filepath.Join(e.dir, scopefile.NoteDir), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "secret")
	writeFile(t, target, "secret\n")
	path := namedPath(e.dir, "pad")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	data, key, err := FileSnapshot(path)
	var nr *NonRegularError
	if !errors.As(err, &nr) {
		t.Fatalf("symlink: data=%q key=%q err=%v", data, key, err)
	}
	if strings.Contains(string(data), "secret") {
		t.Fatalf("symlink snapshot read the target: %q", data)
	}

	dangling := namedPath(e.dir, "ghost")
	if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), dangling); err != nil {
		t.Fatal(err)
	}
	data, key, err = FileSnapshot(dangling)
	if !errors.As(err, &nr) {
		t.Fatalf("dangling: data=%q key=%q err=%v", data, key, err)
	}
	if key == MissingClobberKey {
		t.Fatal("dangling symlink treated as missing")
	}

	dirPath := namedPath(e.dir, "dirnote")
	if err := os.Mkdir(dirPath, 0o755); err != nil {
		t.Fatal(err)
	}
	data, key, err = FileSnapshot(dirPath)
	if !errors.As(err, &nr) {
		t.Fatalf("directory: data=%q key=%q err=%v", data, key, err)
	}
}

func TestSetClobber(t *testing.T) {
	e := newPlainEnv(t)
	in := e.input("pad")
	if _, err := Set(e.deps, in, []byte("one\n")); err != nil {
		t.Fatal(err)
	}
	key, err := FileClobberKey(namedPath(e.dir, "pad"))
	if err != nil {
		t.Fatal(err)
	}
	in.Base = key
	if _, err := Set(e.deps, in, []byte("two\n")); err != nil {
		t.Fatalf("matching base: %v", err)
	}
	in.Base = key
	_, err = Set(e.deps, in, []byte("three\n"))
	var cl *ClobberError
	if !errors.As(err, &cl) {
		t.Fatalf("stale base: %v", err)
	}
	got, err := os.ReadFile(namedPath(e.dir, "pad"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "two\n" {
		t.Errorf("stale set wrote %q", got)
	}

	miss := e.input("ghost")
	miss.Base = MissingClobberKey
	if _, err := Set(e.deps, miss, []byte("new\n")); err != nil {
		t.Fatalf("create with missing key: %v", err)
	}
	miss.Base = MissingClobberKey
	_, err = Set(e.deps, miss, []byte("again\n"))
	if !errors.As(err, &cl) {
		t.Fatalf("create after file exists: %v", err)
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
	missing := filepath.Join(t.TempDir(), "missing")
	err := RequireDir("wc", missing)
	if err == nil {
		t.Fatal("expected unreachable")
	}
	if !strings.Contains(err.Error(), token.UnreachableScope) {
		t.Errorf("want unreachable_scope:, got %v", err)
	}
	_, err = List("wc", missing)
	if err == nil || !strings.Contains(err.Error(), token.UnreachableScope) {
		t.Errorf("list of missing dir: %v", err)
	}
}

func TestPrepareAndFinishEdit(t *testing.T) {
	e := newPlainEnv(t)
	res, err := PrepareEdit(e.input("scratch"))
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

	res, err = PrepareEdit(e.input(""))
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
	if !errors.As(err, &use) || use.Msg != `"list" is a reserved note name` {
		t.Errorf("reserved: %v", err)
	}
	_, err = SelectName("Not_Valid", "", false, "default")
	if !errors.As(err, &use) || use.Msg != `"Not_Valid" is not a valid note slug` {
		t.Errorf("invalid: %v", err)
	}
	got, err := ParseSlug("pad")
	if err != nil || got != "pad" {
		t.Errorf("parse pad: got %q %v", got, err)
	}
}
