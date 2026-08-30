package writeengine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cuelang.org/go/cue/cuecontext"

	"github.com/p3bot/tk/internal/git"
	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/reconcile"
	"github.com/p3bot/tk/internal/registry"
	"github.com/p3bot/tk/internal/testgit"
)

type env struct {
	deps Deps
	dir  string
	root string
}

func newPlainEnv(t *testing.T, name, cueBody string) *env {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "tk.cue"), cueBody)
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
	reg := &registry.Registry{
		Scopes: map[string]registry.Entry{name: {Dir: dir, Root: root}},
		Lens:   map[string][]string{},
		Me:     map[string]string{},
		Note:   map[string]string{},
	}
	return &env{
		deps: Deps{
			Ctx:      context.Background(),
			Cue:      cue,
			StateDir: t.TempDir(),
			Reg:      reg,
			DB:       db,
			Rec:      reconcile.New(db, cue),
		},
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

func addTicket(t *testing.T, dir, id, status string) string {
	t.Helper()
	path := filepath.Join(dir, id+"-work.md")
	writeFile(t, path, "---\nid: "+id+"\nstatus: "+status+"\norder: \"a0\"\ncreated: 2026-01-01T00:00:00Z\n---\n# Work\n")
	return path
}

func fullLookup(id string) Lookup {
	return Lookup{Arg: id, Query: id, ByFull: true}
}

func requireGit(t *testing.T) {
	t.Helper()
	if !git.Available() {
		t.Skip("git not on PATH")
	}
	testgit.Hermetic(t)
}

func gitLog(t *testing.T, repo string) []string {
	t.Helper()
	out, err := testgit.CombinedAllowFailure(t, repo, "log", "--format=%s")
	if err != nil {
		return nil
	}
	if strings.TrimSpace(out) == "" {
		return nil
	}
	return strings.Split(strings.TrimSpace(out), "\n")
}

func initAutoCommitRepo(t *testing.T, name string) (e *env, repo string) {
	t.Helper()
	requireGit(t)
	repo = t.TempDir()
	testgit.Run(t, repo, "init", "-b", "main")
	testgit.Run(t, repo, "config", "user.email", "a@b.c")
	testgit.Run(t, repo, "config", "user.name", "tk-test")
	testgit.Run(t, repo, "config", "commit.gpgsign", "false")
	dir := filepath.Join(repo, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "tk.cue"), "name: \""+name+"\"\nautoCommit: true\n")
	return openEnv(t, name, dir, repo), repo
}
