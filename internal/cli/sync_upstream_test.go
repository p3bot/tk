package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func headRev(t *testing.T, clone string) string {
	t.Helper()
	return strings.TrimSpace(gitIn(t, clone, "rev-parse", "HEAD"))
}

func assertNotMidRebase(t *testing.T, clone string) {
	t.Helper()
	for _, name := range []string{"rebase-merge", "rebase-apply"} {
		if _, err := os.Stat(filepath.Join(clone, ".git", name)); err == nil {
			t.Fatalf("checkout is mid-rebase (%s)", name)
		}
	}
}

func initSiblingScope(t *testing.T, m *machine) string {
	t.Helper()
	dir := filepath.Join(m.clone, "xy")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, m.app, "scope", "init", dir, "--name", "xy", "--code-root", dir, "--auto-commit"); err != nil {
		t.Fatalf("init xy: %v", err)
	}
	return dir
}

func TestSyncHelpDescribesScopedSnapshot(t *testing.T) {
	out, _, err := run(t, newApp(t), "sync", "--help")
	if err != nil {
		t.Fatalf("sync --help: %v", err)
	}
	for _, want := range []string{
		"tk sync [--scope S] [--all]",
		"does not rebase",
		"allowlisted dirty files",
		"commits nothing",
		"fast-forward",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("sync help missing %q:\n%s", want, out)
		}
	}
}

func TestSyncAheadPushesNamedScopeBesideDirtySibling(t *testing.T) {
	requireGit(t)
	remote := newBareRemote(t)
	m := cloneMachine(t, remote)
	wc := m.initScopeAutoCommit(t)
	xy := initSiblingScope(t, m)
	addTicket(t, wc, "wc-ab2c", "alpha", "todo", "a0", "# Alpha\n\nbody line\n", false, "")
	addTicket(t, xy, "xy-cd3e", "beta", "todo", "a0", "# Beta\n\nbody line\n", false, "")
	if _, _, err := m.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("wc publish: %v", err)
	}
	if _, _, err := m.sync(t, "--scope", "xy"); err != nil {
		t.Fatalf("xy publish: %v", err)
	}

	m.mark(t, "wc-ab2c", "review")
	editBody(t, filepath.Join(xy, "xy-cd3e-beta.md"), "sibling dirty")
	editBody(t, filepath.Join(wc, "wc-ab2c-alpha.md"), "named dirty")

	_, errOut, err := m.sync(t, "--scope", "wc")
	if err != nil {
		t.Fatalf("ahead sync: %v (stderr %q)", err, errOut)
	}
	assertNotMidRebase(t, m.clone)
	st := gitIn(t, m.clone, "status", "--porcelain")
	if !strings.Contains(st, "xy-cd3e-beta.md") {
		t.Errorf("sibling file must stay unstaged, status %q", st)
	}
	if strings.Contains(st, "wc-ab2c-alpha.md") {
		t.Errorf("named scope file must be committed, status %q", st)
	}
	sib := gitIn(t, m.clone, "show", "origin/main:xy/xy-cd3e-beta.md")
	if strings.Contains(sib, "sibling dirty") {
		t.Errorf("sibling edit must not be in the pushed tree:\n%s", sib)
	}
	named := gitIn(t, m.clone, "show", "origin/main:wc/wc-ab2c-alpha.md")
	if !strings.Contains(named, "named dirty") {
		t.Errorf("named scope edit must be in the pushed commit:\n%s", named)
	}
}

func TestSyncBehindFastForwardKeepsUnrelatedDirty(t *testing.T) {
	requireGit(t)
	remote := newBareRemote(t)
	local := cloneMachine(t, remote)
	wc := local.initScopeAutoCommit(t)
	xy := initSiblingScope(t, local)
	addTicket(t, wc, "wc-ab2c", "alpha", "todo", "a0", "# Alpha\n\nbody line\n", false, "")
	addTicket(t, xy, "xy-cd3e", "beta", "todo", "a0", "# Beta\n\nbody line\n", false, "")
	if _, _, err := local.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("wc publish: %v", err)
	}
	if _, _, err := local.sync(t, "--scope", "xy"); err != nil {
		t.Fatalf("xy publish: %v", err)
	}

	ahead := cloneMachine(t, remote)
	ahead.importScope(t)
	addTicket(t, ahead.scopeDir(), "wc-zz99", "extra", "todo", "a1", "# Extra\n", false, "")
	if _, _, err := ahead.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("upstream advance: %v", err)
	}

	editBody(t, filepath.Join(xy, "xy-cd3e-beta.md"), "sibling stays")
	editBody(t, filepath.Join(wc, "wc-ab2c-alpha.md"), "named after ff")
	_, errOut, err := local.sync(t, "--scope", "wc")
	if err != nil {
		t.Fatalf("behind sync: %v (stderr %q)", err, errOut)
	}
	if !strings.Contains(readFile(t, filepath.Join(xy, "xy-cd3e-beta.md")), "sibling stays") {
		t.Fatal("unrelated dirty file must keep its contents through the fast-forward")
	}
	if !strings.Contains(gitIn(t, local.clone, "status", "--porcelain"), "xy-cd3e-beta.md") {
		t.Fatal("unrelated dirty file must stay unstaged")
	}
	named := gitIn(t, local.clone, "show", "origin/main:wc/wc-ab2c-alpha.md")
	if !strings.Contains(named, "named after ff") {
		t.Errorf("named scope edit must be committed on the fast-forwarded tip:\n%s", named)
	}
	if !remoteHas(t, remote, "wc/wc-zz99-extra.md") {
		t.Error("the incoming commit must be on the remote after the fast-forward and push")
	}
}

func TestSyncBehindOverlapRefusesWithoutCommit(t *testing.T) {
	requireGit(t)
	remote := newBareRemote(t)
	local := cloneMachine(t, remote)
	wc := local.initScopeAutoCommit(t)
	xy := initSiblingScope(t, local)
	addTicket(t, wc, "wc-ab2c", "alpha", "todo", "a0", "# Alpha\n\nbody line\n", false, "")
	addTicket(t, xy, "xy-cd3e", "beta", "todo", "a0", "# Beta\n\nbody line\n", false, "")
	if _, _, err := local.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("wc publish: %v", err)
	}
	if _, _, err := local.sync(t, "--scope", "xy"); err != nil {
		t.Fatalf("xy publish: %v", err)
	}

	ahead := cloneMachine(t, remote)
	editBody(t, filepath.Join(ahead.clone, "xy", "xy-cd3e-beta.md"), "upstream edit")
	gitIn(t, ahead.clone, "add", "-A")
	gitIn(t, ahead.clone, "commit", "-m", "upstream xy")
	gitIn(t, ahead.clone, "push")

	editBody(t, filepath.Join(xy, "xy-cd3e-beta.md"), "local edit")
	before := headRev(t, local.clone)
	beforeBytes := readFile(t, filepath.Join(xy, "xy-cd3e-beta.md"))
	_, errOut, err := local.sync(t, "--scope", "wc")
	if ExitCodeFromError(err) != exitFailure {
		t.Fatalf("overlapping fast-forward must fail, got %v (stderr %q)", err, errOut)
	}
	if !strings.Contains(errOut, "xy/xy-cd3e-beta.md") {
		t.Errorf("refusal must print the path, got %q", errOut)
	}
	if headRev(t, local.clone) != before {
		t.Fatal("HEAD must stay unmoved")
	}
	if readFile(t, filepath.Join(xy, "xy-cd3e-beta.md")) != beforeBytes {
		t.Fatal("the dirty file must stay unmoved")
	}
	remoteBlob := gitIn(t, remote, "show", "main:xy/xy-cd3e-beta.md")
	if strings.Contains(remoteBlob, "local edit") {
		t.Errorf("local edit must not be pushed:\n%s", remoteBlob)
	}
	assertNotMidRebase(t, local.clone)
}

func TestSyncBehindMatchingDeletionFastForwards(t *testing.T) {
	requireGit(t)
	remote := newBareRemote(t)
	local := cloneMachine(t, remote)
	wc := local.initScopeAutoCommit(t)
	addTicket(t, wc, "wc-ab2c", "alpha", "todo", "a0", "# Alpha\n\nbody line\n", false, "")
	if _, _, err := local.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("publish: %v", err)
	}

	ahead := cloneMachine(t, remote)
	ahead.importScope(t)
	if err := os.Remove(mustSeedTicket(t, ahead.scopeDir())); err != nil {
		t.Fatal(err)
	}
	commitLocal(t, ahead.clone, "upstream delete")
	gitIn(t, ahead.clone, "push")

	path := mustSeedTicket(t, local.scopeDir())
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	_, errOut, err := local.sync(t, "--scope", "wc")
	if err != nil {
		t.Fatalf("matching deletion must fast-forward: %v (stderr %q)", err, errOut)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the file must stay gone, stat err=%v", err)
	}
	if headRev(t, local.clone) != strings.TrimSpace(gitIn(t, local.clone, "rev-parse", "@{u}")) {
		t.Fatal("HEAD must fast-forward onto upstream")
	}
	if remoteHas(t, remote, "wc/wc-ab2c-alpha.md") {
		t.Error("the deleted ticket must stay off the remote")
	}
}

func TestSyncBehindDeletionUpstreamStillHasRefuses(t *testing.T) {
	requireGit(t)
	remote := newBareRemote(t)
	local := cloneMachine(t, remote)
	wc := local.initScopeAutoCommit(t)
	addTicket(t, wc, "wc-ab2c", "alpha", "todo", "a0", "# Alpha\n\nbody line\n", false, "")
	if _, _, err := local.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("publish: %v", err)
	}

	ahead := cloneMachine(t, remote)
	ahead.importScope(t)
	editBody(t, mustSeedTicket(t, ahead.scopeDir()), "upstream edit")
	commitLocal(t, ahead.clone, "upstream edit")
	gitIn(t, ahead.clone, "push")

	path := mustSeedTicket(t, local.scopeDir())
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	before := headRev(t, local.clone)
	_, errOut, err := local.sync(t, "--scope", "wc")
	if ExitCodeFromError(err) != exitFailure {
		t.Fatalf("a deletion upstream would restore must fail, got %v (stderr %q)", err, errOut)
	}
	if !strings.Contains(errOut, "wc/wc-ab2c-alpha.md") {
		t.Errorf("refusal must print the path, got %q", errOut)
	}
	if headRev(t, local.clone) != before {
		t.Fatal("HEAD must stay unmoved")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the local deletion must stay, stat err=%v", err)
	}
	if !remoteHas(t, remote, "wc/wc-ab2c-alpha.md") {
		t.Error("upstream's file must stay on the remote")
	}
}

func TestSyncBehindUnchangedDeletionFastForwardsAndCommits(t *testing.T) {
	requireGit(t)
	remote := newBareRemote(t)
	local := cloneMachine(t, remote)
	wc := local.initScopeAutoCommit(t)
	addTicket(t, wc, "wc-ab2c", "alpha", "todo", "a0", "# Alpha\n\nbody line\n", false, "")
	if _, _, err := local.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("publish: %v", err)
	}

	ahead := cloneMachine(t, remote)
	ahead.importScope(t)
	addTicket(t, ahead.scopeDir(), "wc-zz99", "extra", "todo", "a1", "# Extra\n", false, "")
	if _, _, err := ahead.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("upstream advance: %v", err)
	}

	path := mustSeedTicket(t, local.scopeDir())
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	_, errOut, err := local.sync(t, "--scope", "wc")
	if err != nil {
		t.Fatalf("unchanged deletion must fast-forward and commit: %v (stderr %q)", err, errOut)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the file must stay gone, stat err=%v", err)
	}
	if headRev(t, local.clone) != strings.TrimSpace(gitIn(t, local.clone, "rev-parse", "@{u}")) {
		t.Fatal("HEAD must be pushed onto the fast-forwarded upstream")
	}
	if remoteHas(t, remote, "wc/wc-ab2c-alpha.md") {
		t.Error("the local deletion must be committed and pushed")
	}
	if !remoteHas(t, remote, "wc/wc-zz99-extra.md") {
		t.Error("the incoming ticket must be on the remote")
	}
	log := gitIn(t, remote, "log", "--format=%s", "main")
	if !strings.Contains(log, "tk: remove wc-ab2c") {
		t.Fatalf("snapshot must record the deletion, log:\n%s", log)
	}
}

func TestSyncPushRejectRetryDoesNotSnapshotAgain(t *testing.T) {
	requireGit(t)
	remote := newBareRemote(t)
	local := cloneMachine(t, remote)
	wc := local.initScopeAutoCommit(t)
	addTicket(t, wc, "wc-ab2c", "alpha", "todo", "a0", "# Alpha\n\nbody line\n", false, "")
	if _, _, err := local.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	editBody(t, filepath.Join(wc, "wc-ab2c-alpha.md"), "local only")
	commitLocal(t, local.clone, "local only")

	side := cloneMachine(t, remote)
	if err := os.WriteFile(filepath.Join(side.clone, "side.txt"), []byte("from side\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, side.clone, "add", "side.txt")
	gitIn(t, side.clone, "commit", "-m", "upstream moves")

	marker := filepath.Join(t.TempDir(), "push-once")
	extra := filepath.Join(wc, "wc-zz99-extra.md")
	hook := fmt.Sprintf(`#!/bin/sh
if [ -f %q ]; then
  exit 0
fi
touch %q
cat > %q << 'EOF'
---
id: wc-zz99
status: todo
order: "a1"
created: 2026-01-01T00:00:00Z
---
# Extra
EOF
unset GIT_DIR GIT_WORK_TREE GIT_PREFIX GIT_INDEX_FILE
git -C %q push
exit 1
`, marker, marker, extra, side.clone)
	hooksDir := filepath.Join(local.clone, ".git", "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, local.clone, "config", "core.hooksPath", hooksDir)
	if err := os.WriteFile(filepath.Join(hooksDir, "pre-push"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}

	_, errOut, err := local.sync(t, "--scope", "wc")
	if err != nil {
		t.Fatalf("retry must finish the push: %v (stderr %q)", err, errOut)
	}
	st := gitIn(t, local.clone, "status", "--porcelain")
	if !strings.Contains(st, "wc-zz99-extra.md") {
		t.Fatalf("the file written during the rejected push must stay uncommitted, status %q", st)
	}
	if remoteHas(t, remote, "wc/wc-zz99-extra.md") {
		t.Error("the retry must not push the new file")
	}
	log := gitIn(t, remote, "log", "--format=%s", "main")
	if !strings.Contains(log, "local only") || !strings.Contains(log, "upstream moves") {
		t.Fatalf("retry must rebase the local commit onto the moved upstream, log:\n%s", log)
	}
	assertNotMidRebase(t, local.clone)
}

func TestSyncDivergedDirtySiblingBlocksRebaseThenRetries(t *testing.T) {
	requireGit(t)
	remote := newBareRemote(t)
	local := cloneMachine(t, remote)
	wc := local.initScopeAutoCommit(t)
	xy := initSiblingScope(t, local)
	addTicket(t, wc, "wc-ab2c", "alpha", "todo", "a0", "# Alpha\n\nbody line\n", false, "")
	addTicket(t, xy, "xy-cd3e", "beta", "todo", "a0", "# Beta\n\nbody line\n", false, "")
	if _, _, err := local.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("wc publish: %v", err)
	}
	if _, _, err := local.sync(t, "--scope", "xy"); err != nil {
		t.Fatalf("xy publish: %v", err)
	}

	editBody(t, filepath.Join(wc, "wc-ab2c-alpha.md"), "local commit")
	commitLocal(t, local.clone, "local only")

	ahead := cloneMachine(t, remote)
	ahead.importScope(t)
	addTicket(t, ahead.scopeDir(), "wc-zz99", "extra", "todo", "a1", "# Extra\n", false, "")
	if _, _, err := ahead.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("upstream advance: %v", err)
	}

	editBody(t, filepath.Join(xy, "xy-cd3e-beta.md"), "blocks rebase")
	wcPath := filepath.Join(wc, "wc-ab2c-alpha.md")
	if err := os.WriteFile(wcPath, []byte(strings.Replace(readFile(t, wcPath), "local commit", "snapshot me", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	_, errOut, err := local.sync(t, "--scope", "wc")
	if ExitCodeFromError(err) != exitFailure {
		t.Fatalf("blocked rebase must fail, got %v (stderr %q)", err, errOut)
	}
	if !strings.Contains(errOut, "xy/xy-cd3e-beta.md") {
		t.Errorf("refusal must print the blocking path, got %q", errOut)
	}
	assertNotMidRebase(t, local.clone)
	if log := gitIn(t, local.clone, "log", "--format=%s"); !strings.Contains(log, "tk: edit wc-ab2c") {
		t.Fatalf("the named-scope snapshot commit must stay on the local branch\nlog:\n%s", log)
	}
	if strings.Contains(gitIn(t, remote, "log", "--format=%s", "main"), "tk: edit wc-ab2c") {
		t.Fatal("blocked rebase must not push")
	}

	gitIn(t, local.clone, "checkout", "--", "xy/xy-cd3e-beta.md")
	if _, errOut, err := local.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("retry after the blocker is gone: %v (stderr %q)", err, errOut)
	}
	assertNotMidRebase(t, local.clone)
	if !strings.Contains(gitIn(t, remote, "log", "--format=%s", "main"), "tk: edit wc-ab2c") {
		t.Fatal("the later sync must rebase and push the snapshot commit")
	}
	if strings.Contains(gitIn(t, local.clone, "status", "--porcelain"), "xy-cd3e-beta.md") {
		t.Fatal("the cleared sibling file must not be recommitted")
	}
}

func TestSyncDivergedCleanSiblingRebasesAndPushes(t *testing.T) {
	requireGit(t)
	remote := newBareRemote(t)
	local := cloneMachine(t, remote)
	wc := local.initScopeAutoCommit(t)
	initSiblingScope(t, local)
	addTicket(t, wc, "wc-ab2c", "alpha", "todo", "a0", "# Alpha\n\nbody line\n", false, "")
	if _, _, err := local.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("wc publish: %v", err)
	}
	editBody(t, filepath.Join(wc, "wc-ab2c-alpha.md"), "local only")
	commitLocal(t, local.clone, "local only")

	ahead := cloneMachine(t, remote)
	ahead.importScope(t)
	addTicket(t, ahead.scopeDir(), "wc-zz99", "extra", "todo", "a1", "# Extra\n", false, "")
	if _, _, err := ahead.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("upstream advance: %v", err)
	}
	if _, errOut, err := local.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("clean diverged sync: %v (stderr %q)", err, errOut)
	}
	if n := gitIn(t, local.clone, "rev-list", "--count", "@{u}..HEAD"); strings.TrimSpace(n) != "0" {
		t.Errorf("clean diverged sync must push, unpushed=%s", n)
	}
}

func TestSyncAllCommitsNothingAndPushesExisting(t *testing.T) {
	requireGit(t)
	remote := newBareRemote(t)
	m := cloneMachine(t, remote)
	wc := m.initScopeAutoCommit(t)
	addTicket(t, wc, "wc-ab2c", "alpha", "todo", "a0", "# Alpha\n\nbody line\n", false, "")
	if _, _, err := m.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("publish: %v", err)
	}

	addTicket(t, wc, "wc-zz99", "extra", "todo", "a1", "# Extra\n", false, "")
	if _, errOut, err := m.sync(t, "--all"); err != nil {
		t.Fatalf("--all: %v (stderr %q)", err, errOut)
	}
	if remoteHas(t, remote, "wc/wc-zz99-extra.md") {
		t.Error("--all must not commit the dirty ticket")
	}
	if !strings.Contains(gitIn(t, m.clone, "status", "--porcelain"), "wc-zz99-extra.md") {
		t.Fatal("dirty ticket must remain after --all")
	}

	if _, errOut, err := m.sync(t); err != nil {
		t.Fatalf("bare sync: %v (stderr %q)", err, errOut)
	}
	if remoteHas(t, remote, "wc/wc-zz99-extra.md") {
		t.Error("bare sync with no ambient scope must not commit the dirty ticket")
	}

	m.mark(t, "wc-ab2c", "review")
	if _, errOut, err := m.sync(t, "--all"); err != nil {
		t.Fatalf("--all with an unpushed commit: %v (stderr %q)", err, errOut)
	}
	if !strings.Contains(gitIn(t, remote, "log", "--format=%s", "main"), "wc-ab2c -> review") {
		t.Fatal("--all must push the commit that was already made")
	}
	if !strings.Contains(gitIn(t, m.clone, "status", "--porcelain"), "wc-zz99-extra.md") {
		t.Fatal("the dirty ticket must still be dirty after the push")
	}
}

func TestSyncAllResumeDoesNotCommit(t *testing.T) {
	requireGit(t)
	a, b, remote := twoMachines(t)
	editBody(t, mustSeedTicket(t, a.scopeDir()), "A version")
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A sync: %v", err)
	}
	editBody(t, mustSeedTicket(t, b.scopeDir()), "B version")
	commitLocal(t, b.clone, "B body")
	if _, _, err := b.sync(t, "--scope", "wc"); ExitCodeFromError(err) != exitFailure {
		t.Fatalf("expected a paused rebase")
	}

	pB := mustSeedTicket(t, b.scopeDir())
	if err := os.WriteFile(pB, []byte(stripConflictMarkers(readFile(t, pB), "resolved body")), 0o644); err != nil {
		t.Fatal(err)
	}
	addTicket(t, b.scopeDir(), "wc-ff88", "extra", "todo", "a5", "# Extra\n", false, "")
	if _, errOut, err := b.sync(t, "--all"); err != nil {
		t.Fatalf("--all resume: %v (stderr %q)", err, errOut)
	}
	if remoteHas(t, remote, "wc/wc-ff88-extra.md") {
		t.Error("--all must not commit the named scope after resume")
	}
	if !strings.Contains(gitIn(t, b.clone, "status", "--porcelain"), "wc-ff88-extra.md") {
		t.Fatal("the extra ticket must stay dirty")
	}
}

func TestClaimLeavesSiblingDirty(t *testing.T) {
	requireGit(t)
	remote := newBareRemote(t)
	m := cloneMachine(t, remote)
	wc := m.initScopeAutoCommit(t)
	xy := initSiblingScope(t, m)
	addTicket(t, wc, "wc-ab2c", "alpha", "todo", "a0", "# Alpha\n\nbody line\n", false, "")
	addTicket(t, xy, "xy-cd3e", "beta", "todo", "a0", "# Beta\n\nbody line\n", false, "")
	if _, _, err := m.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("wc publish: %v", err)
	}
	if _, _, err := m.sync(t, "--scope", "xy"); err != nil {
		t.Fatalf("xy publish: %v", err)
	}
	editBody(t, filepath.Join(xy, "xy-cd3e-beta.md"), "sibling dirty")

	out, errOut, err := run(t, m.app, "mark", "in-progress", "wc-ab2c")
	if err != nil {
		t.Fatalf("claim: %v (stderr %q)", err, errOut)
	}
	if st := fmStatus(t, strings.TrimSpace(out)); st != "in-progress" {
		t.Fatalf("status: got %q", st)
	}
	names := gitIn(t, m.clone, "show", "--name-only", "--pretty=format:", "HEAD")
	if !strings.Contains(names, "wc-ab2c-alpha.md") {
		t.Errorf("claim commit must contain the marked path, names:\n%s", names)
	}
	if strings.Contains(names, "xy-cd3e-beta.md") {
		t.Errorf("claim commit must not contain the sibling, names:\n%s", names)
	}
	if !strings.Contains(gitIn(t, m.clone, "status", "--porcelain"), "xy-cd3e-beta.md") {
		t.Fatal("sibling ticket must stay dirty")
	}
	pushed := gitIn(t, m.clone, "show", "origin/main:xy/xy-cd3e-beta.md")
	if strings.Contains(pushed, "sibling dirty") {
		t.Fatal("sibling edit must not be pushed")
	}
}

func TestClaimRefreshRefusesWhenFastForwardWouldOverwrite(t *testing.T) {
	requireGit(t)
	a, b, _ := twoMachines(t)
	editBody(t, mustSeedTicket(t, a.scopeDir()), "upstream body")
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A sync: %v", err)
	}
	pB := mustSeedTicket(t, b.scopeDir())
	editBody(t, pB, "local body")
	before := headRev(t, b.clone)
	beforeBytes := readFile(t, pB)
	out, errOut, err := run(t, b.app, "mark", "in-progress", "wc-ab2c")
	if ExitCodeFromError(err) != exitFailure {
		t.Fatalf("claim must fail, got %v (stderr %q)", err, errOut)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("failed claim must not print a path, got %q", out)
	}
	if st := fmStatus(t, pB); st != "todo" {
		t.Errorf("status must stay todo, got %q", st)
	}
	if headRev(t, b.clone) != before {
		t.Fatal("HEAD must stay unmoved")
	}
	if readFile(t, pB) != beforeBytes {
		t.Fatal("the dirty ticket must stay unmoved")
	}
}

func TestClaimRefreshRefusesWhenDirtyPathBlocksRebase(t *testing.T) {
	requireGit(t)
	remote := newBareRemote(t)
	local := cloneMachine(t, remote)
	wc := local.initScopeAutoCommit(t)
	xy := initSiblingScope(t, local)
	addTicket(t, wc, "wc-ab2c", "alpha", "todo", "a0", "# Alpha\n\nbody line\n", false, "")
	addTicket(t, xy, "xy-cd3e", "beta", "todo", "a0", "# Beta\n\nbody line\n", false, "")
	if _, _, err := local.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("wc publish: %v", err)
	}
	if _, _, err := local.sync(t, "--scope", "xy"); err != nil {
		t.Fatalf("xy publish: %v", err)
	}
	editBody(t, filepath.Join(wc, "wc-ab2c-alpha.md"), "local commit")
	commitLocal(t, local.clone, "local ticket")

	ahead := cloneMachine(t, remote)
	ahead.importScope(t)
	addTicket(t, ahead.scopeDir(), "wc-zz99", "extra", "todo", "a1", "# Extra\n", false, "")
	if _, _, err := ahead.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("upstream advance: %v", err)
	}
	editBody(t, filepath.Join(xy, "xy-cd3e-beta.md"), "blocks rebase")
	before := fmStatus(t, filepath.Join(wc, "wc-ab2c-alpha.md"))
	out, errOut, err := run(t, local.app, "next", "--claim", "--scope", "wc")
	if ExitCodeFromError(err) != exitFailure {
		t.Fatalf("claim must fail when rebase cannot start, got %v (stderr %q)", err, errOut)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("failed claim must not print a path, got %q", out)
	}
	if st := fmStatus(t, filepath.Join(wc, "wc-ab2c-alpha.md")); st != before {
		t.Errorf("status must stay %s, got %q", before, st)
	}
	assertNotMidRebase(t, local.clone)
	if !strings.Contains(errOut, "xy/xy-cd3e-beta.md") {
		t.Errorf("refusal must print the blocking path, got %q", errOut)
	}
}

func TestFalseFlipScoopsSiblingAllowlist(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	_, repo := initGitScope(t, app, "wc", true)
	xy := filepath.Join(repo, "xy")
	if err := os.MkdirAll(xy, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, app, "scope", "init", xy, "--name", "xy", "--code-root", xy, "--auto-commit"); err != nil {
		t.Fatalf("init xy: %v", err)
	}
	wcTicket, _ := createID(t, app, "wc", "Work")
	xyTicket, _ := createID(t, app, "xy", "Other")
	if _, _, err := run(t, app, "scope", "auto-commit", "false", "--scope", "wc"); err != nil {
		t.Fatalf("false flip: %v", err)
	}
	names := gitIn(t, repo, "show", "--name-only", "--pretty=format:", "HEAD")
	if !strings.Contains(names, filepath.Base(wcTicket)) || !strings.Contains(names, filepath.Base(xyTicket)) {
		t.Errorf("false flip must commit allowlisted dirt in every scope on the root, names:\n%s", names)
	}
}
