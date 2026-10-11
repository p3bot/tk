package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/syncengine"
	"github.com/p3bot/tk/internal/testgit"
)

func TestSyncDivergedOverlapRefusesWithoutMerge(t *testing.T) {
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
	editBody(t, filepath.Join(ahead.clone, "xy", "xy-cd3e-beta.md"), "upstream text")
	commitLocal(t, ahead.clone, "upstream moves sibling")
	gitIn(t, ahead.clone, "push")

	sibling := filepath.Join(xy, "xy-cd3e-beta.md")
	editBody(t, sibling, "local dirty")
	beforeBytes := readFile(t, sibling)
	before := headRev(t, local.clone)
	_, errOut, err := local.sync(t, "--scope", "wc")
	if ExitCodeFromError(err) != exitFailure {
		t.Fatalf("overlap must refuse, got %v (stderr %q)", err, errOut)
	}
	if !strings.Contains(errOut, "xy/xy-cd3e-beta.md") {
		t.Errorf("refusal must print the path, got %q", errOut)
	}
	if headRev(t, local.clone) != before {
		t.Fatal("HEAD must stay unmoved")
	}
	if readFile(t, sibling) != beforeBytes {
		t.Fatal("the dirty file must keep its bytes")
	}
	if _, err := os.Stat(filepath.Join(local.clone, ".git", "MERGE_HEAD")); err == nil {
		t.Fatal("refusal must not leave MERGE_HEAD")
	}
	if strings.Contains(gitIn(t, remote, "log", "--format=%s", "main"), "local only") {
		t.Fatal("refusal must not push")
	}
}

func TestSyncDivergedDeletionRefusesWithoutRestoring(t *testing.T) {
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
	wcPath := filepath.Join(wc, "wc-ab2c-alpha.md")
	editBody(t, wcPath, "local commit")
	commitLocal(t, local.clone, "local only")

	ahead := cloneMachine(t, remote)
	editBody(t, filepath.Join(ahead.clone, "xy", "xy-cd3e-beta.md"), "upstream text")
	commitLocal(t, ahead.clone, "upstream moves sibling")
	gitIn(t, ahead.clone, "push")

	sibling := filepath.Join(xy, "xy-cd3e-beta.md")
	if err := os.Remove(sibling); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wcPath, []byte(strings.Replace(readFile(t, wcPath), "local commit", "snapshot me", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	before := headRev(t, local.clone)
	_, errOut, err := local.sync(t, "--scope", "wc")
	if ExitCodeFromError(err) != exitFailure {
		t.Fatalf("deletion must refuse, got %v (stderr %q)", err, errOut)
	}
	if !strings.Contains(errOut, "xy/xy-cd3e-beta.md") {
		t.Errorf("refusal must print the path, got %q", errOut)
	}
	if headRev(t, local.clone) == before {
		t.Fatal("the named-scope snapshot must stay")
	}
	if subject := strings.TrimSpace(gitIn(t, local.clone, "log", "-1", "--format=%s")); subject != "tk: edit wc-ab2c" {
		t.Fatalf("HEAD must be only the snapshot commit, got %q", subject)
	}
	if strings.Contains(gitIn(t, remote, "log", "--format=%s", "main"), "tk: edit wc-ab2c") {
		t.Fatal("the refusal must not push the snapshot")
	}
	if _, err := os.Stat(sibling); !os.IsNotExist(err) {
		t.Fatalf("the deleted file must stay absent, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(local.clone, ".git", "MERGE_HEAD")); err == nil {
		t.Fatal("refusal must not leave MERGE_HEAD")
	}
}

func TestSyncDivergedLocalEditDeletionStillMerges(t *testing.T) {
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
	sibling := filepath.Join(xy, "xy-cd3e-beta.md")
	editBody(t, sibling, "local sibling edit")
	commitLocal(t, local.clone, "local sibling")

	ahead := cloneMachine(t, remote)
	editBody(t, filepath.Join(ahead.clone, "wc", "wc-ab2c-alpha.md"), "upstream text")
	commitLocal(t, ahead.clone, "upstream moves wc")
	gitIn(t, ahead.clone, "push")

	if err := os.Remove(sibling); err != nil {
		t.Fatal(err)
	}
	if _, errOut, err := local.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("a deletion of a local-only edit must merge: %v (stderr %q)", err, errOut)
	}
	if _, err := os.Stat(sibling); !os.IsNotExist(err) {
		t.Fatalf("the deleted file must stay absent, stat err=%v", err)
	}
	assertNotMidRebase(t, local.clone)
	assertMergeParents(t, local.clone)
	if !strings.Contains(gitIn(t, local.clone, "status", "--porcelain"), "xy/xy-cd3e-beta.md") {
		t.Fatal("the deletion must stay uncommitted")
	}
	if !remoteHas(t, remote, "xy/xy-cd3e-beta.md") {
		t.Fatal("the uncommitted deletion must not be pushed")
	}
	pushed := gitIn(t, remote, "show", "main:wc/wc-ab2c-alpha.md")
	if !strings.Contains(pushed, "upstream text") {
		t.Fatalf("the merge must carry the upstream edit:\n%s", pushed)
	}
}

func TestSyncDivergedStagedUnchangedPathRefuses(t *testing.T) {
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

	sibling := filepath.Join(xy, "xy-cd3e-beta.md")
	editBody(t, sibling, "staged locally")
	beforeBytes := readFile(t, sibling)
	gitIn(t, local.clone, "add", "--", "xy/xy-cd3e-beta.md")
	before := headRev(t, local.clone)
	_, errOut, err := local.sync(t, "--scope", "wc")
	if ExitCodeFromError(err) != exitFailure {
		t.Fatalf("staged path must refuse, got %v (stderr %q)", err, errOut)
	}
	if !strings.Contains(errOut, "xy/xy-cd3e-beta.md") {
		t.Errorf("refusal must print the staged path, got %q", errOut)
	}
	if strings.Contains(errOut, "Merge with strategy") {
		t.Errorf("refusal must not list git's closing sentence, got %q", errOut)
	}
	if headRev(t, local.clone) != before {
		t.Fatal("HEAD must stay unmoved")
	}
	if readFile(t, sibling) != beforeBytes {
		t.Fatal("the staged file must keep its bytes")
	}
	if _, err := os.Stat(filepath.Join(local.clone, ".git", "MERGE_HEAD")); err == nil {
		t.Fatal("refusal must not leave MERGE_HEAD")
	}
}

func TestSyncDivergedBodyConflictKeepsUnrelatedAndRefusesWrites(t *testing.T) {
	requireGit(t)
	a, b, remote := twoMachines(t)
	otherA := filepath.Join(a.clone, "other.txt")
	if err := os.WriteFile(otherA, []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commitLocal(t, a.clone, "track other")
	gitIn(t, a.clone, "push")
	if _, _, err := b.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("B fast-forward: %v", err)
	}

	pA := mustSeedTicket(t, a.scopeDir())
	editBody(t, pA, "A version of the body")
	setStatusLine(t, pA, "review")
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A sync: %v", err)
	}

	pB := mustSeedTicket(t, b.scopeDir())
	editBody(t, pB, "B version of the body")
	commitLocal(t, b.clone, "B body")
	otherB := filepath.Join(b.clone, "other.txt")
	if err := os.WriteFile(otherB, []byte("dirty bytes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, errOut, err := b.sync(t, "--scope", "wc")
	if ExitCodeFromError(err) != exitFailure {
		t.Fatalf("body conflict must pause, got %v (stderr %q)", err, errOut)
	}
	if !strings.Contains(errOut, "body conflict") || !strings.Contains(errOut, "merge paused") {
		t.Errorf("pause must name the body conflict and the merge, got %q", errOut)
	}
	if syncengine.FrontmatterHasMarkers(pB) {
		t.Errorf("frontmatter must be field-merged:\n%s", readFile(t, pB))
	}
	if !syncengine.HasConflictMarker([]byte(readFile(t, pB))) {
		t.Errorf("the body should carry conflict markers:\n%s", readFile(t, pB))
	}
	if fmStatus(t, pB) != "review" {
		t.Errorf("one-sided status must land, got %q", fmStatus(t, pB))
	}
	if readFile(t, otherB) != "dirty bytes\n" {
		t.Fatal("unrelated bytes must stay")
	}
	if _, err := os.Stat(filepath.Join(b.clone, ".git", "MERGE_HEAD")); err != nil {
		t.Fatal("the merge must stay paused")
	}
	if strings.Contains(gitIn(t, remote, "log", "--format=%s", "main"), "B body") {
		t.Fatal("a paused merge must not push")
	}

	for _, args := range [][]string{
		{"mark", "blocked", "wc-ab2c", "--scope", "wc"},
		{"next", "--claim", "--scope", "wc"},
		{"create", "Nope", "--scope", "wc"},
		{"meta", "set", "wc-ab2c", "summary", "nope", "--scope", "wc"},
	} {
		_, errOut, err := run(t, b.app, args...)
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		msg += errOut
		if !strings.Contains(msg, "checkout is mid-merge") || !strings.Contains(msg, b.clone) {
			t.Errorf("%v: want mid-merge refusal, got %v stderr %q", args, err, errOut)
		}
	}
	if fmStatus(t, pB) != "review" {
		t.Errorf("refused writes must leave status review, got %q", fmStatus(t, pB))
	}
	t.Setenv("EDITOR", "true")
	if _, errOut, err := run(t, b.app, "edit", "wc-ab2c", "--scope", "wc"); err != nil {
		t.Fatalf("tk edit must open during a merge: %v (stderr %q)", err, errOut)
	}

	resolved := stripConflictMarkers(readFile(t, pB), "resolved body")
	if err := os.WriteFile(pB, []byte(resolved), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, errOut, err := b.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("resume must complete: %v (stderr %q)", err, errOut)
	}
	assertNotMidRebase(t, b.clone)
	if n := gitIn(t, b.clone, "rev-list", "--count", "@{u}..HEAD"); strings.TrimSpace(n) != "0" {
		t.Fatalf("resume must push, unpushed=%s", n)
	}
	if readFile(t, otherB) != "dirty bytes\n" {
		t.Fatal("unrelated bytes must survive the merge commit")
	}
	if !strings.Contains(gitIn(t, b.clone, "status", "--porcelain"), "other.txt") {
		t.Fatal("unrelated file must stay unstaged")
	}
	names := gitIn(t, b.clone, "log", "--merges", "-1", "--name-only", "--pretty=format:")
	if strings.Contains(names, "other.txt") {
		t.Fatalf("unrelated file must not be in the merge commit:\n%s", names)
	}
}

func TestSyncResumeKeepsUnresolvedBodyMarkers(t *testing.T) {
	requireGit(t)
	a, b, remote := twoMachines(t)
	pA := mustSeedTicket(t, a.scopeDir())
	editBody(t, pA, "A version of the body")
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A sync: %v", err)
	}
	pB := mustSeedTicket(t, b.scopeDir())
	editBody(t, pB, "B version of the body")
	commitLocal(t, b.clone, "B body")
	if _, _, err := b.sync(t, "--scope", "wc"); ExitCodeFromError(err) != exitFailure {
		t.Fatalf("body conflict must pause, got %v", err)
	}
	original := readFile(t, pB)
	if !syncengine.HasConflictMarker([]byte(original)) {
		t.Fatalf("setup must leave a body marker:\n%s", original)
	}
	hand := strings.Replace(original, "B version of the body", "hand edit still conflicted", 1)
	if hand == original {
		hand = strings.Replace(original, "A version of the body", "hand edit still conflicted", 1)
	}
	if hand == original {
		t.Fatalf("could not plant a partial edit:\n%s", original)
	}
	if err := os.WriteFile(pB, []byte(hand), 0o644); err != nil {
		t.Fatal(err)
	}

	_, errOut, err := b.sync(t, "--scope", "wc")
	if ExitCodeFromError(err) != exitFailure {
		t.Fatalf("retry must stay paused, got %v (stderr %q)", err, errOut)
	}
	if !strings.Contains(errOut, "body conflict") || !strings.Contains(errOut, "merge paused") {
		t.Errorf("retry must name the body conflict and the merge, got %q", errOut)
	}
	got := readFile(t, pB)
	if !strings.Contains(got, "hand edit still conflicted") || !syncengine.HasConflictMarker([]byte(got)) {
		t.Fatalf("the partial edit and the marker must stay:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(b.clone, ".git", "MERGE_HEAD")); err != nil {
		t.Fatal("the merge must stay paused")
	}
	if strings.Contains(gitIn(t, remote, "log", "--format=%s", "main"), "B body") {
		t.Fatal("the retry must not push")
	}
}

func TestSyncResumesPausedRebaseWithoutConverting(t *testing.T) {
	requireGit(t)
	a, b, _ := twoMachines(t)
	pA := mustSeedTicket(t, a.scopeDir())
	editBody(t, pA, "A version of the body")
	setStatusLine(t, pA, "review")
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A sync: %v", err)
	}
	pB := mustSeedTicket(t, b.scopeDir())
	editBody(t, pB, "B version of the body")
	setStatusLine(t, pB, "blocked")
	commitLocal(t, b.clone, "B body")
	gitIn(t, b.clone, "fetch")
	if err := testgit.AllowFailure(t, b.clone, []string{"GIT_EDITOR=true"}, "rebase", "@{u}"); err == nil {
		t.Fatal("rebase must pause on the body conflict")
	}
	if _, err := os.Stat(filepath.Join(b.clone, ".git", "rebase-merge")); err != nil {
		t.Fatal("expected a paused rebase")
	}

	_, errOut, err := b.sync(t, "--scope", "wc")
	if ExitCodeFromError(err) != exitFailure {
		t.Fatalf("resume must pause the body conflict, got %v (stderr %q)", err, errOut)
	}
	if !strings.Contains(errOut, "rebase paused") || strings.Contains(errOut, "merge paused") {
		t.Errorf("a paused rebase must stay a rebase, got %q", errOut)
	}
	if _, err := os.Stat(filepath.Join(b.clone, ".git", "rebase-merge")); err != nil {
		t.Fatal("sync must not convert the rebase into a merge")
	}
	if _, err := os.Stat(filepath.Join(b.clone, ".git", "MERGE_HEAD")); err == nil {
		t.Fatal("rebase resume must not start a merge")
	}

	_, _, claimErr := run(t, b.app, "next", "--claim", "--scope", "wc")
	if claimErr == nil || !strings.Contains(claimErr.Error(), "mid-sync-conflict") {
		t.Fatalf("claim must refuse the rebase, got %v", claimErr)
	}
	if _, err := os.Stat(filepath.Join(b.clone, ".git", "rebase-merge")); err != nil {
		t.Fatal("claim must not resume the rebase")
	}

	if err := os.WriteFile(pB, []byte(stripConflictMarkers(readFile(t, pB), "resolved body")), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, errOut, err := b.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("rebase continue must finish: %v (stderr %q)", err, errOut)
	}
	assertNotMidRebase(t, b.clone)
	if n := gitIn(t, b.clone, "rev-list", "--count", "@{u}..HEAD"); strings.TrimSpace(n) != "0" {
		t.Fatalf("finished rebase must push, unpushed=%s", n)
	}
}

func TestSyncResumeCommitFailureKeepsResolution(t *testing.T) {
	requireGit(t)
	a, b, remote := twoMachines(t)
	pA := mustSeedTicket(t, a.scopeDir())
	editBody(t, pA, "A version of the body")
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A sync: %v", err)
	}
	pB := mustSeedTicket(t, b.scopeDir())
	editBody(t, pB, "B version of the body")
	commitLocal(t, b.clone, "B body")
	if _, _, err := b.sync(t, "--scope", "wc"); ExitCodeFromError(err) != exitFailure {
		t.Fatalf("body conflict must pause, got %v", err)
	}
	resolved := stripConflictMarkers(readFile(t, pB), "kept by hand")
	if err := os.WriteFile(pB, []byte(resolved), 0o644); err != nil {
		t.Fatal(err)
	}
	hook := installExitHook(t, b.clone, "pre-commit")
	_, errOut, err := b.sync(t, "--scope", "wc")
	if ExitCodeFromError(err) != exitFailure {
		t.Fatalf("commit failure must not succeed, got %v (stderr %q)", err, errOut)
	}
	if !strings.Contains(errOut, "merge left in progress") {
		t.Errorf("stderr must say the merge is still open, got %q", errOut)
	}
	if _, err := os.Stat(filepath.Join(b.clone, ".git", "MERGE_HEAD")); err != nil {
		t.Fatal("a failed merge commit must leave MERGE_HEAD")
	}
	if got := readFile(t, pB); !strings.Contains(got, "kept by hand") || syncengine.HasConflictMarker([]byte(got)) {
		t.Fatalf("the resolved body must stay on disk:\n%s", got)
	}
	if err := os.Remove(hook); err != nil {
		t.Fatal(err)
	}
	if _, errOut, err := b.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("retry must commit the resolution: %v (stderr %q)", err, errOut)
	}
	assertNotMidRebase(t, b.clone)
	pushed := gitIn(t, remote, "show", "main:wc/wc-ab2c-alpha.md")
	if !strings.Contains(pushed, "kept by hand") {
		t.Fatalf("the pushed ticket must carry the resolution:\n%s", pushed)
	}
}

func TestSyncCleanMergeCommitFailureLeavesMerge(t *testing.T) {
	requireGit(t)
	a, b, _ := twoMachines(t)
	editBody(t, mustSeedTicket(t, a.scopeDir()), "upstream body")
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A sync: %v", err)
	}
	side := filepath.Join(b.clone, "side.txt")
	if err := os.WriteFile(side, []byte("local side\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, b.clone, "add", "side.txt")
	gitIn(t, b.clone, "commit", "-m", "local side")
	hook := installExitHook(t, b.clone, "pre-merge-commit")
	_, errOut, err := b.sync(t, "--scope", "wc")
	if ExitCodeFromError(err) != exitFailure {
		t.Fatalf("clean merge commit failure must not succeed, got %v (stderr %q)", err, errOut)
	}
	if !strings.Contains(errOut, "merge left in progress") {
		t.Errorf("stderr must say the merge is still open, got %q", errOut)
	}
	if _, err := os.Stat(filepath.Join(b.clone, ".git", "MERGE_HEAD")); err != nil {
		t.Fatal("a failed clean merge commit must leave MERGE_HEAD")
	}
	if got := readFile(t, side); got != "local side\n" {
		t.Fatalf("the local file must stay, got %q", got)
	}
	if err := os.Remove(hook); err != nil {
		t.Fatal(err)
	}
	if _, errOut, err := b.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("retry must finish the merge: %v (stderr %q)", err, errOut)
	}
	assertNotMidRebase(t, b.clone)
	assertMergeParents(t, b.clone)
}

func installExitHook(t *testing.T, clone, name string) string {
	t.Helper()
	hooksDir := filepath.Join(clone, ".git", "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, clone, "config", "core.hooksPath", hooksDir)
	hook := filepath.Join(hooksDir, name)
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return hook
}
