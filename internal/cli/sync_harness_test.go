package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cuelang.org/go/cue/cuecontext"

	"github.com/p3bot/tk/internal/pathutil"
	"github.com/p3bot/tk/internal/testgit"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return testgit.Combined(t, dir, args...)
}

func configIdentity(t *testing.T, dir string) {
	t.Helper()
	gitIn(t, dir, "config", "user.email", "a@b.c")
	gitIn(t, dir, "config", "user.name", "tk-test")
	gitIn(t, dir, "config", "commit.gpgsign", "false")
}

func newBareRemote(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	remote := filepath.Join(base, "remote.git")
	gitIn(t, base, "init", "--bare", "-b", "main", "remote.git")
	seed := filepath.Join(base, "seed")
	gitIn(t, base, "clone", remote, "seed")
	configIdentity(t, seed)
	if err := os.WriteFile(filepath.Join(seed, ".keep"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, seed, "checkout", "-B", "main")
	gitIn(t, seed, "add", "-A")
	gitIn(t, seed, "commit", "-m", "seed")
	gitIn(t, seed, "push", "-u", "origin", "main")
	gitIn(t, remote, "symbolic-ref", "HEAD", "refs/heads/main")
	return remote
}

type machine struct {
	app   *App
	clone string
}

func cloneMachine(t *testing.T, remote string) *machine {
	t.Helper()
	base := t.TempDir()
	clone := filepath.Join(base, "clone")
	gitIn(t, base, "clone", remote, "clone")
	configIdentity(t, clone)
	// Canonical so gitstate keys, gitroot, and path assertions agree on macOS.
	clone = pathutil.Canonical(clone)
	app := &App{Ctx: cuecontext.New(), ConfigDir: t.TempDir(), StateDir: t.TempDir()}
	return &machine{app: app, clone: clone}
}

func (m *machine) scopeDir() string { return filepath.Join(m.clone, "wc") }

// initScopeAutoCommit registers a fresh auto-commit scope named wc at <clone>/wc. The sync
func (m *machine) initScopeAutoCommit(t *testing.T) string {
	t.Helper()
	dir := m.scopeDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, m.app, "scope", "init", dir, "--name", "wc", "--auto-commit"); err != nil {
		t.Fatalf("init auto-commit scope wc: %v", err)
	}
	return dir
}

// importScope registers the already-on-disk wc scope (after a clone brought its files).
func (m *machine) importScope(t *testing.T) string {
	t.Helper()
	dir := m.scopeDir()
	if _, _, err := run(t, m.app, "scope", "import", dir); err != nil {
		t.Fatalf("import scope wc: %v", err)
	}
	return dir
}

func (m *machine) sync(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	return run(t, m.app, append([]string{"sync"}, args...)...)
}

func (m *machine) mark(t *testing.T, id, newStatus string) {
	t.Helper()
	if _, _, err := run(t, m.app, "mark", newStatus, id); err != nil {
		t.Fatalf("mark %s %s: %v", newStatus, id, err)
	}
}

func fmStatus(t *testing.T, path string) string {
	t.Helper()
	return fmValue(t, path, "status")
}

func findTicket(t *testing.T, dir, base string) (string, bool) {
	t.Helper()
	if p := filepath.Join(dir, base); fileExistsPath(p) {
		return p, false
	}
	if p := filepath.Join(dir, "archive", base); fileExistsPath(p) {
		return p, true
	}
	return "", false
}

func mustSeedTicket(t *testing.T, dir string) string {
	t.Helper()
	p, _ := findTicket(t, dir, "wc-ab2c-alpha.md")
	if p == "" {
		t.Fatalf("seed ticket wc-ab2c-alpha.md not found under %s", dir)
	}
	return p
}

func fileExistsPath(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func setStatusLine(t *testing.T, path, status string) {
	t.Helper()
	replaceLinePrefix(t, path, "status:", "status: "+status)
}

func editBody(t *testing.T, path, newText string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := strings.Replace(string(data), "body line", newText, 1)
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func replaceLinePrefix(t *testing.T, path, prefix, replacement string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, prefix) {
			out = append(out, replacement)
		} else {
			out = append(out, line)
		}
	}
	if err := os.WriteFile(path, []byte(strings.Join(out, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeCue(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "tk.cue"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func topCommit(t *testing.T, clone string) string {
	t.Helper()
	return gitIn(t, clone, "log", "-1", "--format=%s")
}

func twoMachines(t *testing.T) (a, b *machine, remote string) {
	t.Helper()
	remote = newBareRemote(t)
	a = cloneMachine(t, remote)
	dirA := a.initScopeAutoCommit(t)
	addTicket(t, dirA, "wc-ab2c", "alpha", "todo", "a0", "# alpha\n\nbody line\n", false, "")
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("machine A initial sync: %v", err)
	}
	b = cloneMachine(t, remote)
	b.importScope(t)
	return a, b, remote
}

func remoteHas(t *testing.T, remote, repoRelPath string) bool {
	t.Helper()
	out := gitIn(t, remote, "ls-tree", "-r", "--name-only", "main")
	for _, l := range lines(out) {
		if l == repoRelPath {
			return true
		}
	}
	return false
}
