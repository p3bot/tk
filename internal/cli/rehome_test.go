package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/token"
)

func TestRehomePrintsDestPathAndRewritesEdges(t *testing.T) {
	app := newApp(t)
	foo := initScope(t, app, "foo")
	initScope(t, app, "bar")
	api := initScope(t, app, "api")
	addTicket(t, foo, "foo-kv6x", "dep", "todo", "a0", "# Dep\n", false, "")
	addTicket(t, foo, "foo-ab2c", "moved", "todo", "a1", "# Moved\n", false, "depends: [foo-kv6x]\n")
	addTicket(t, foo, "foo-de34", "ref", "todo", "a2", "# Ref\n", false, "depends: [foo-ab2c]\n")
	addTicket(t, api, "api-mm22", "x", "todo", "a0", "# X\n", false, "depends: [foo-ab2c]\n")

	out, errOut, err := run(t, app, "rehome", "foo-ab2c", "bar")
	if err != nil {
		t.Fatalf("rehome: %v (%s)", err, errOut)
	}
	path := strings.TrimSpace(out)
	if !strings.HasSuffix(path, "bar-ab2c-moved.md") {
		t.Errorf("stdout path = %q", out)
	}
	if strings.Count(strings.TrimSpace(out), "\n") != 0 {
		t.Errorf("stdout must be exactly one path, got %q", out)
	}
	if strings.Contains(out, "edge_verify:") || strings.Contains(out, token.EdgeVerify) {
		t.Errorf("edge_verify must not be on stdout, got %q", out)
	}
	if !strings.Contains(errOut, "edge_verify:") || !strings.Contains(errOut, "api-mm22") {
		t.Errorf("cross-scope inbound should ride stderr, got %q", errOut)
	}
	if strings.Count(errOut, "edge_verify:") != 1 {
		t.Errorf("one inbound report, got %q", errOut)
	}

	if fileExists(foo, "foo-ab2c-moved.md") {
		t.Errorf("source file must be gone")
	}
	moved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(moved), "foo-kv6x") || strings.Contains(string(moved), "bar-kv6x") {
		t.Errorf("sibling dep must stay foo-kv6x: %s", moved)
	}
	ref, _ := os.ReadFile(filepath.Join(foo, "foo-de34-ref.md"))
	if !strings.Contains(string(ref), "bar-ab2c") || strings.Contains(string(ref), "foo-ab2c") {
		t.Errorf("source inbound not rewritten: %s", ref)
	}
	apiFile, _ := os.ReadFile(filepath.Join(api, "api-mm22-x.md"))
	if !strings.Contains(string(apiFile), "foo-ab2c") {
		t.Errorf("other-scope edge must not be rewritten: %s", apiFile)
	}

	if _, _, err := run(t, app, "get", "bar-ab2c"); err != nil {
		t.Errorf("dest id must resolve: %v", err)
	}
	if _, _, err := run(t, app, "get", "foo-ab2c"); err == nil {
		t.Errorf("old id must be unknown after a completed rehome")
	}
}

func TestRehomeSameScopeUsage(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "foo")
	addTicket(t, dir, "foo-ab2c", "x", "todo", "a0", "# X\n", false, "")
	_, _, err := run(t, app, "rehome", "foo-ab2c", "foo")
	if got := ExitCodeFromError(err); got != exitUsage {
		t.Fatalf("same-scope exit = %d want 2 (err=%v)", got, err)
	}
}

func TestRehomeUnknownDest(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "foo")
	addTicket(t, dir, "foo-ab2c", "x", "todo", "a0", "# X\n", false, "")
	_, _, err := run(t, app, "rehome", "foo-ab2c", "ghost")
	if got := ExitCodeFromError(err); got != exitFailure {
		t.Fatalf("unknown dest exit = %d want 1 (err=%v)", got, err)
	}
}

func TestRehomeBadID(t *testing.T) {
	app := newApp(t)
	initScope(t, app, "foo")
	initScope(t, app, "bar")
	_, _, err := run(t, app, "rehome", "NOT-AN-ID", "bar")
	if got := ExitCodeFromError(err); got != exitUsage {
		t.Fatalf("bad id exit = %d want 2 (err=%v)", got, err)
	}
}

func TestRehomeBadDestName(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "foo")
	addTicket(t, dir, "foo-ab2c", "x", "todo", "a0", "# X\n", false, "")
	_, _, err := run(t, app, "rehome", "foo-ab2c", "BAD")
	if got := ExitCodeFromError(err); got != exitUsage {
		t.Fatalf("bad dest name exit = %d want 2 (err=%v)", got, err)
	}
}

func TestRehomeUnparseableInboundLeavesSource(t *testing.T) {
	app := newApp(t)
	foo := initScope(t, app, "foo")
	initScope(t, app, "bar")
	addTicket(t, foo, "foo-ab2c", "moved", "todo", "a0", "# Moved\n", false, "")
	if err := os.WriteFile(filepath.Join(foo, "foo-de34-broke.md"), []byte("---\nid: foo-de34\nstatus: [x\ndepends: [foo-ab2c]\n---\n# broke\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, errOut, err := run(t, app, "rehome", "foo-ab2c", "bar")
	if err == nil {
		t.Fatal("unparseable inbound must refuse")
	}
	if !strings.Contains(err.Error(), token.ParseError) && !strings.Contains(errOut, token.ParseError) {
		t.Errorf("want parse_error, err=%v stderr=%q", err, errOut)
	}
	if !fileExists(foo, "foo-ab2c-moved.md") {
		t.Errorf("source must still be in place")
	}
}

func TestRehomeMidRebaseLeavesSource(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	foo, repo := initGitScope(t, app, "foo", true)
	initScope(t, app, "bar")
	addTicket(t, foo, "foo-ab2c", "x", "todo", "a0", "# X\n", false, "")
	if err := os.MkdirAll(filepath.Join(repo, ".git", "rebase-merge"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, _, err := run(t, app, "rehome", "foo-ab2c", "bar")
	if ExitCodeFromError(err) != exitFailure {
		t.Errorf("mid-rebase rehome should refuse non-zero, got %v", err)
	}
	if !fileExists(foo, "foo-ab2c-x.md") {
		t.Errorf("source must still be in place")
	}
}

func TestRehomeTwoRootsSyncNeededStdoutIsDestPath(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	foo, fooRepo := initGitScope(t, app, "foo", true)
	_, barRepo := initGitScope(t, app, "bar", true)
	addTicket(t, foo, "foo-ab2c", "moved", "todo", "a0", "# Moved\n", false, "")
	seedPushedRemote(t, fooRepo)
	seedPushedRemote(t, barRepo)

	out, errOut, err := run(t, app, "rehome", "foo-ab2c", "bar")
	if err != nil {
		t.Fatalf("rehome: %v (%s)", err, errOut)
	}
	path := strings.TrimSpace(out)
	if strings.Contains(path, "\n") {
		t.Errorf("stdout must be exactly one dest path, got %q", out)
	}
	if !strings.HasSuffix(path, "bar-ab2c-moved.md") {
		t.Errorf("stdout = %q", out)
	}
	if strings.Contains(out, "sync_needed:") {
		t.Errorf("sync_needed must not be on stdout, got %q", out)
	}
	if n := strings.Count(errOut, "sync_needed:"); n != 2 {
		t.Errorf("want one sync_needed per root, got %d in %q", n, errOut)
	}
}

func seedPushedRemote(t *testing.T, repo string) {
	t.Helper()
	remote := t.TempDir()
	runGit(t, remote, "init", "--bare", "-b", "main")
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-m", "seed")
	runGit(t, repo, "checkout", "-B", "main")
	runGit(t, repo, "remote", "add", "origin", remote)
	runGit(t, repo, "push", "-u", "origin", "main")
}
