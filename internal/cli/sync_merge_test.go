package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/syncengine"
)

// A both-sides terminal status change produces status_conflict in the file (clean YAML, base status)
func TestSyncBothSidesTerminalStatusDispute(t *testing.T) {
	requireGit(t)
	a, b, remote := twoMachines(t)

	a.mark(t, "wc-ab2c", "done")
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A sync done: %v", err)
	}

	b.mark(t, "wc-ab2c", "cancelled")
	_, errOut, err := b.sync(t, "--scope", "wc")
	if ExitCodeFromError(err) != exitFailure {
		t.Fatalf("both-sides terminal dispute must pause non-zero, got %v (stderr %q)", err, errOut)
	}
	if !strings.Contains(errOut, "status_conflict:") {
		t.Fatalf("dispute should ride status_conflict, got %q", errOut)
	}

	path, _ := findTicket(t, b.scopeDir(), "wc-ab2c-alpha.md")
	if path == "" {
		t.Fatal("the disputed ticket file should still exist on B")
	}
	if !syncengine.FrontmatterHasStatusConflict(path) {
		t.Errorf("the file should carry status_conflict, content:\n%s", readFile(t, path))
	}
	if st := fmStatus(t, path); st != "todo" {
		t.Errorf("the merged file should carry the base status todo, got %q", st)
	}

	// A second sync while the key is present still refuses to continue.
	_, errOut, err = b.sync(t, "--scope", "wc")
	if ExitCodeFromError(err) != exitFailure {
		t.Fatalf("sync must keep refusing while status_conflict is present, got %v", err)
	}
	if !strings.Contains(errOut, "status_conflict") {
		t.Errorf("the persistent dispute should still be reported, got %q", errOut)
	}

	resolveStatusConflict(t, path, "done")
	if _, _, err := b.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("post-resolution sync should complete: %v", err)
	}
	if !remoteHas(t, remote, "wc/archive/wc-ab2c-alpha.md") {
		t.Errorf("the resolved terminal ticket should land under archive/ on the remote")
	}
}

func resolveStatusConflict(t *testing.T, path, finalStatus string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		switch {
		case strings.HasPrefix(line, "status_conflict:"):
			continue
		case strings.HasPrefix(line, "status:"):
			out = append(out, "status: "+finalStatus)
		default:
			out = append(out, line)
		}
	}
	if err := os.WriteFile(path, []byte(strings.Join(out, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestSyncCueConflictPausesThenResumeMergesMD(t *testing.T) {
	requireGit(t)
	a, b, remote := twoMachines(t)

	dirA := a.scopeDir()
	writeCue(t, dirA, "name: \"wc\"\nautoCommit: true\nfields: {a: {type: \"string\"}}\n")
	setStatusLine(t, mustSeedTicket(t, dirA), "in-progress")
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A sync: %v", err)
	}

	dirB := b.scopeDir()
	writeCue(t, dirB, "name: \"wc\"\nautoCommit: true\nfields: {b: {type: \"string\"}}\n")
	pB := mustSeedTicket(t, dirB)
	setStatusLine(t, pB, "review")
	commitLocal(t, b.clone, "B cue and status")
	_, errOut, err := b.sync(t, "--scope", "wc")
	if ExitCodeFromError(err) != exitFailure {
		t.Fatalf("a tk.cue conflict must pause non-zero, got %v (stderr %q)", err, errOut)
	}
	if !strings.Contains(errOut, "config_unparseable:") {
		t.Errorf("a conflicted tk.cue should ride config_unparseable, got %q", errOut)
	}
	if !syncengine.FrontmatterHasMarkers(pB) {
		t.Errorf("the ticket .md must be left unmerged (markers in frontmatter) while tk.cue is conflicted:\n%s", readFile(t, pB))
	}

	writeCue(t, dirB, "name: \"wc\"\nautoCommit: true\nfields: {a: {type: \"string\"}, b: {type: \"string\"}}\n")
	if _, _, err := b.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("post-resolution sync should field-merge the .md and complete: %v", err)
	}
	if syncengine.FrontmatterHasMarkers(pB) {
		t.Errorf("after resume the .md frontmatter must be field-merged clean:\n%s", readFile(t, pB))
	}
	if st := fmStatus(t, pB); st == "todo" {
		t.Errorf("the .md status should have merged past the base, got %q", st)
	}
	if n := gitIn(t, b.clone, "rev-list", "--count", "@{u}..HEAD"); n != "0" {
		t.Errorf("B should be fully pushed after the tk.cue resume, unpushed=%s", n)
	}
	_ = remote
}

func TestSyncGitignoreConflictDoesNotBlockTicketMerges(t *testing.T) {
	requireGit(t)
	a, b, _ := twoMachines(t)

	appendLine(t, filepath.Join(a.scopeDir(), ".gitignore"), "a-only/")
	setStatusLine(t, mustSeedTicket(t, a.scopeDir()), "in-progress")
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A sync: %v", err)
	}

	appendLine(t, filepath.Join(b.scopeDir(), ".gitignore"), "b-only/")
	setStatusLine(t, mustSeedTicket(t, b.scopeDir()), "review")
	commitLocal(t, b.clone, "B gitignore and status")
	_, errOut, err := b.sync(t, "--scope", "wc")
	if ExitCodeFromError(err) != exitFailure {
		t.Fatalf("a conflicted .gitignore must pause non-zero, got %v (stderr %q)", err, errOut)
	}
	if !strings.Contains(errOut, ".gitignore") {
		t.Errorf("the conflicted .gitignore should be named, got %q", errOut)
	}
	if strings.Contains(errOut, "config_unparseable:") {
		t.Errorf("a .gitignore conflict is not a schema failure and must not ride config_unparseable, got %q", errOut)
	}

	pB := mustSeedTicket(t, b.scopeDir())
	if syncengine.FrontmatterHasMarkers(pB) {
		t.Errorf("the ticket .md must still be field-merged — .gitignore gates nothing:\n%s", readFile(t, pB))
	}

	if err := os.WriteFile(filepath.Join(b.scopeDir(), ".gitignore"), []byte(".tk.lock\na-only/\nb-only/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("sync after resolving .gitignore should complete: %v", err)
	}
	if n := gitIn(t, b.clone, "rev-list", "--count", "@{u}..HEAD"); n != "0" {
		t.Errorf("B should be fully pushed after the .gitignore resolution, unpushed=%s", n)
	}
}

func TestSyncGitignoreDeleteEditUnactionedThenRemove(t *testing.T) {
	requireGit(t)
	a, b, remote := twoMachines(t)

	if err := os.Remove(filepath.Join(a.scopeDir(), ".gitignore")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A .gitignore delete sync: %v", err)
	}

	appendLine(t, filepath.Join(b.scopeDir(), ".gitignore"), "b-extra/")
	commitLocal(t, b.clone, "B gitignore")
	_, firstOut, err := b.sync(t, "--scope", "wc")
	if ExitCodeFromError(err) != exitFailure {
		t.Fatalf(".gitignore delete/edit must pause non-zero, got %v (stderr %q)", err, firstOut)
	}
	assertConfigDeleteEditHandoff(t, firstOut, "wc/.gitignore", true)
	if strings.Contains(firstOut, "resolve the conflict markers") {
		t.Errorf("first pause must not use the markers line for a delete/edit, got %q", firstOut)
	}

	_, secondOut, err := b.sync(t, "--scope", "wc")
	if ExitCodeFromError(err) != exitFailure {
		t.Fatalf("unactioned .gitignore re-run must stay paused, got %v (stderr %q)", err, secondOut)
	}
	assertConfigDeleteEditHandoff(t, secondOut, "wc/.gitignore", true)
	assertSameDeleteEditLine(t, firstOut, secondOut)
	if remoteHas(t, remote, "wc/.gitignore") {
		t.Errorf("unactioned re-run must not resurrect .gitignore on the remote")
	}

	if err := os.Remove(filepath.Join(b.scopeDir(), ".gitignore")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("remove resolution should complete: %v", err)
	}
	if remoteHas(t, remote, "wc/.gitignore") {
		t.Errorf("deletion should be recorded on the remote")
	}
}

func TestSyncGitignoreDeleteEditModifiedResumes(t *testing.T) {
	requireGit(t)
	a, b, remote := twoMachines(t)

	if err := os.Remove(filepath.Join(a.scopeDir(), ".gitignore")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A .gitignore delete sync: %v", err)
	}

	appendLine(t, filepath.Join(b.scopeDir(), ".gitignore"), "b-extra/")
	commitLocal(t, b.clone, "B gitignore")
	if _, _, err := b.sync(t, "--scope", "wc"); ExitCodeFromError(err) != exitFailure {
		t.Fatalf("expected .gitignore delete/edit pause, got %v", err)
	}

	resolved := ".tk.lock\nb-extra/\nhuman-kept/\n"
	if err := os.WriteFile(filepath.Join(b.scopeDir(), ".gitignore"), []byte(resolved), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("modified .gitignore re-run should complete: %v", err)
	}
	if !remoteHas(t, remote, "wc/.gitignore") {
		t.Errorf("the human's modified .gitignore should land on the remote")
	}
	if got := readFile(t, filepath.Join(b.scopeDir(), ".gitignore")); got != resolved {
		t.Errorf("staged content = %q, want %q", got, resolved)
	}
}

// A .gitignore delete/edit resolved with git add never reaches the resumed stop.
func TestSyncGitignoreDeleteEditGitAddResumes(t *testing.T) {
	requireGit(t)
	a, b, remote := twoMachines(t)

	if err := os.Remove(filepath.Join(a.scopeDir(), ".gitignore")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A .gitignore delete sync: %v", err)
	}

	appendLine(t, filepath.Join(b.scopeDir(), ".gitignore"), "b-extra/")
	commitLocal(t, b.clone, "B gitignore")
	if _, _, err := b.sync(t, "--scope", "wc"); ExitCodeFromError(err) != exitFailure {
		t.Fatalf("expected .gitignore delete/edit pause, got %v", err)
	}

	before := readFile(t, filepath.Join(b.scopeDir(), ".gitignore"))
	gitIn(t, b.clone, "add", "--", "wc/.gitignore")
	if _, _, err := b.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("git-add .gitignore resolution should complete: %v", err)
	}
	if !remoteHas(t, remote, "wc/.gitignore") {
		t.Errorf("the git-add-ed .gitignore should land on the remote")
	}
	if got := readFile(t, filepath.Join(b.scopeDir(), ".gitignore")); got != before {
		t.Errorf("git add keeps exact content; worktree drifted")
	}
}

func TestSyncCueDeleteEditKeepsTicketFailClosed(t *testing.T) {
	requireGit(t)
	a, b, _ := twoMachines(t)

	dirA := a.scopeDir()
	if err := os.Remove(filepath.Join(dirA, "tk.cue")); err != nil {
		t.Fatal(err)
	}
	setStatusLine(t, mustSeedTicket(t, dirA), "in-progress")
	gitIn(t, a.clone, "add", "-A")
	gitIn(t, a.clone, "commit", "-m", "hand: delete tk.cue and edit ticket")
	gitIn(t, a.clone, "push")

	dirB := b.scopeDir()
	writeCue(t, dirB, "name: \"wc\"\nautoCommit: true\nfields: {b: {type: \"string\"}}\n")
	pB := mustSeedTicket(t, dirB)
	setStatusLine(t, pB, "review")
	commitLocal(t, b.clone, "B cue and status")
	_, firstOut, err := b.sync(t, "--scope", "wc")
	if ExitCodeFromError(err) != exitFailure {
		t.Fatalf("tk.cue delete/edit must pause non-zero, got %v (stderr %q)", err, firstOut)
	}
	assertConfigDeleteEditHandoff(t, firstOut, "wc/tk.cue", false)
	if !strings.Contains(firstOut, "config_unparseable:") {
		t.Errorf("tk.cue delete/edit should ride config_unparseable, got %q", firstOut)
	}
	if !syncengine.FrontmatterHasMarkers(pB) {
		t.Errorf("ticket .md must stay unmerged while tk.cue is an open delete/edit:\n%s", readFile(t, pB))
	}

	_, secondOut, err := b.sync(t, "--scope", "wc")
	if ExitCodeFromError(err) != exitFailure {
		t.Fatalf("unactioned tk.cue re-run must stay paused, got %v (stderr %q)", err, secondOut)
	}
	assertConfigDeleteEditHandoff(t, secondOut, "wc/tk.cue", false)
	assertSameDeleteEditLine(t, firstOut, secondOut)
	if !strings.Contains(secondOut, "not merged") || !strings.Contains(secondOut, "tk.cue is conflicted") {
		t.Errorf("ticket pass must report schema fail-closed, got %q", secondOut)
	}
	if !syncengine.FrontmatterHasMarkers(pB) {
		t.Errorf("ticket .md must still carry markers after unactioned re-run:\n%s", readFile(t, pB))
	}
}

// assertConfigDeleteEditHandoff checks a config-file delete/edit line: path, reader-term
func assertConfigDeleteEditHandoff(t *testing.T, errOut, path string, threeWays bool) {
	t.Helper()
	if !strings.Contains(errOut, "delete/edit") {
		t.Errorf("expected delete/edit handoff, got %q", errOut)
	}
	if !strings.Contains(errOut, path) {
		t.Errorf("expected path %q in handoff, got %q", path, errOut)
	}
	if strings.Contains(errOut, "resolve the conflict markers") {
		t.Errorf("delete/edit must not use the markers line, got %q", errOut)
	}
	if !strings.Contains(errOut, "incoming side") && !strings.Contains(errOut, "this machine's replayed commit") {
		t.Errorf("handoff must name the deleting side in reader terms, got %q", errOut)
	}
	if !strings.Contains(errOut, "git add") {
		t.Errorf("handoff must name git add as a way out, got %q", errOut)
	}
	if threeWays {
		for _, way := range []string{"remove " + path, "edit it"} {
			if !strings.Contains(errOut, way) {
				t.Errorf(".gitignore handoff must name %q as a way out, got %q", way, errOut)
			}
		}
		return
	}
	// tk.cue: edit <path> or git add — never offer remove as a resolution.
	if !strings.Contains(errOut, "edit "+path) {
		t.Errorf("tk.cue handoff must name edit %q as a way out, got %q", path, errOut)
	}
	if strings.Contains(errOut, "remove "+path) || strings.Contains(errOut, "— remove") {
		t.Errorf("tk.cue handoff must not offer removal as a way out, got %q", errOut)
	}
}

func appendLine(t *testing.T, path, line string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, []byte(line+"\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSyncAddAddRenameEmitsEdgeVerify(t *testing.T) {
	requireGit(t)
	remote := newBareRemote(t)
	a := cloneMachine(t, remote)
	dirA := a.initScopeAutoCommit(t)
	addTicket(t, dirA, "wc-zz99", "ref", "todo", "a0", "# Ref\n", false, "depends: [wc-ab2c]\n")
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A seed sync: %v", err)
	}
	b := cloneMachine(t, remote)
	dirB := b.importScope(t)

	addTicket(t, dirA, "wc-ab2c", "alpha", "todo", "a1", "# A body\n", false, "")
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A add sync: %v", err)
	}
	addTicket(t, dirB, "wc-ab2c", "alpha", "todo", "a2", "# B body\n", false, "")
	commitLocal(t, b.clone, "B add")
	out, errOut, err := b.sync(t, "--scope", "wc")
	if err != nil {
		t.Fatalf("add/add rename should auto-resolve and complete: %v (stderr %q)", err, errOut)
	}
	if !strings.Contains(out, "repaired add/add") {
		t.Errorf("the rename repair should be reported, got %q", out)
	}
	if !strings.Contains(out+errOut, "edge_verify:") || !strings.Contains(out+errOut, "wc-zz99") {
		t.Errorf("edge_verify should surface the inbound edge from wc-zz99, got %q / %q", out, errOut)
	}
	names := gitIn(t, remote, "ls-tree", "-r", "--name-only", "main")
	n := strings.Count(names, "wc/wc-ab2c")
	if n < 2 {
		t.Errorf("both collided files should be kept (one renamed), got tree:\n%s", names)
	}
	_ = dirB
}

func TestSyncIntegrityRepairsDuplicateFromRebase(t *testing.T) {
	requireGit(t)
	remote := newBareRemote(t)
	a := cloneMachine(t, remote)
	dirA := a.initScopeAutoCommit(t)
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A seed sync: %v", err)
	}
	b := cloneMachine(t, remote)
	dirB := b.importScope(t)

	addTicket(t, dirA, "wc-cd3e", "beta", "todo", "a1", "# Beta\n", false, "")
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A add sync: %v", err)
	}
	addTicket(t, dirB, "wc-cd3e", "gamma", "todo", "a2", "# Gamma\n", false, "")
	out, errOut, err := b.sync(t, "--scope", "wc")
	if err != nil {
		t.Fatalf("integrity repair sync should complete: %v (stderr %q)", err, errOut)
	}
	if !strings.Contains(out, "repaired duplicate id") {
		t.Errorf("the sync integrity step should repair the duplicate id, got %q", out)
	}
	if n := gitIn(t, b.clone, "rev-list", "--count", "@{u}..HEAD"); n != "0" {
		t.Errorf("B should be fully pushed after the integrity repair, unpushed=%s", n)
	}
	_ = dirB
}

func TestSyncAllIsolatesFailingRoot(t *testing.T) {
	requireGit(t)
	app := newApp(t)

	remote := newBareRemote(t)
	base := t.TempDir()
	wcClone := filepath.Join(base, "clone")
	gitIn(t, base, "clone", remote, "clone")
	configIdentity(t, wcClone)
	wcDir := filepath.Join(wcClone, "wc")
	if err := os.MkdirAll(wcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, app, "scope", "init", wcDir, "--name", "wc", "--auto-commit"); err != nil {
		t.Fatalf("init wc: %v", err)
	}
	addTicket(t, wcDir, "wc-ab2c", "alpha", "todo", "a0", "# Alpha\n", false, "")

	xyRepo := t.TempDir()
	gitIn(t, xyRepo, "init", "-b", "main")
	configIdentity(t, xyRepo)
	xyDir := filepath.Join(xyRepo, "xy")
	if err := os.MkdirAll(xyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, app, "scope", "init", xyDir, "--name", "xy", "--auto-commit"); err != nil {
		t.Fatalf("init xy: %v", err)
	}
	addTicket(t, xyDir, "xy-ab2c", "beta", "todo", "a0", "# Beta\n", false, "")

	_, errOut, err := run(t, app, "sync", "--all")
	if ExitCodeFromError(err) != exitFailure {
		t.Fatalf("--all with a failing root must exit non-zero, got %v", err)
	}
	if !strings.Contains(errOut, "sync_disabled:") || !strings.Contains(errOut, "xy") {
		t.Errorf("the failing xy root should ride sync_disabled, got %q", errOut)
	}
	if remoteHas(t, remote, "wc/wc-ab2c-alpha.md") {
		t.Error("--all must not commit the healthy root's dirty ticket")
	}
	st := gitIn(t, wcClone, "status", "--porcelain")
	if !strings.Contains(st, "wc") {
		t.Errorf("wc's dirty ticket must remain, status %q", st)
	}
	if !strings.Contains(errOut, "wc") {
		t.Errorf("--all must still visit the healthy root, got %q", errOut)
	}
}
