package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/gitstate"
	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/reconcile"
	"github.com/p3bot/tk/internal/registry"
	"github.com/p3bot/tk/internal/syncengine"
	"github.com/p3bot/tk/internal/token"
)

func TestClaimMarkRemoteAlreadyTaken(t *testing.T) {
	requireGit(t)
	a, b, _ := twoMachines(t)

	a.mark(t, "wc-ab2c", "review")
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A sync: %v", err)
	}

	out, errOut, err := run(t, b.app, "mark", "wc-ab2c", "in-progress")
	if ExitCodeFromError(err) != exitFailure {
		t.Fatalf("mark after remote took the ticket must fail, got %v (stderr %q)", err, errOut)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("failed claim must not print a path, got %q", out)
	}
	if !strings.Contains(err.Error()+errOut, "no longer todo") {
		t.Errorf("want no-longer-todo after refresh, got err=%v stderr=%q", err, errOut)
	}
	path := mustSeedTicket(t, b.scopeDir())
	if st := fmStatus(t, path); st != "review" {
		t.Errorf("local status must match integrated remote review, got %q", st)
	}
}

func TestClaimHappyPathPushesInProgress(t *testing.T) {
	requireGit(t)
	a, b, _ := twoMachines(t)

	out, errOut, err := run(t, a.app, "next", "--claim", "--scope", "wc")
	if err != nil {
		t.Fatalf("claim: %v (stderr %q)", err, errOut)
	}
	assertClaimStdoutPath(t, out, "wc-ab2c-alpha.md")
	if strings.Contains(errOut, "sync_needed: unpushed") {
		t.Errorf("successful claim push must not emit sync_needed: unpushed, got %q", errOut)
	}
	if got := fmStatus(t, strings.TrimSpace(out)); got != "in-progress" {
		t.Errorf("local status: got %q", got)
	}

	if _, _, err := b.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("B sync: %v", err)
	}
	if st := fmStatus(t, mustSeedTicket(t, b.scopeDir())); st != "in-progress" {
		t.Errorf("peer should see in-progress after claim push, got %q", st)
	}
}

func TestClaimMarkHappyPathPushesInProgress(t *testing.T) {
	requireGit(t)
	a, b, _ := twoMachines(t)

	out, errOut, err := run(t, a.app, "mark", "wc-ab2c", "in-progress")
	if err != nil {
		t.Fatalf("mark claim: %v (stderr %q)", err, errOut)
	}
	assertClaimStdoutPath(t, out, "wc-ab2c-alpha.md")
	if strings.Contains(errOut, "sync_needed: unpushed") {
		t.Errorf("successful claim push must not emit sync_needed: unpushed, got %q", errOut)
	}

	if _, _, err := b.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("B sync: %v", err)
	}
	if st := fmStatus(t, mustSeedTicket(t, b.scopeDir())); st != "in-progress" {
		t.Errorf("peer should see in-progress after mark claim push, got %q", st)
	}
}

func TestClaimMarkFullIDFromOtherCwdUsesTicketRoot(t *testing.T) {
	requireGit(t)
	remoteWC := newBareRemote(t)
	remoteXY := newBareRemote(t)

	a := cloneMachine(t, remoteWC)
	dirA := a.initScopeAutoCommit(t)
	addTicket(t, dirA, "wc-ab2c", "alpha", "todo", "a0", "# alpha\n", false, "")
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A seed sync: %v", err)
	}

	bWC := cloneMachine(t, remoteWC)
	bWC.importScope(t)

	xyClone := filepath.Join(t.TempDir(), "xy-clone")
	gitIn(t, filepath.Dir(xyClone), "clone", remoteXY, filepath.Base(xyClone))
	configIdentity(t, xyClone)
	xyDir := filepath.Join(xyClone, "xy")
	if err := os.MkdirAll(xyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, bWC.app, "scope", "init", xyDir, "--name", "xy", "--auto-commit"); err != nil {
		t.Fatalf("init xy: %v", err)
	}
	writeCue(t, xyDir, "name: \"xy\"\nautoCommit: true\nfields: {x: {type: \"float\"}}\n")

	a.mark(t, "wc-ab2c", "review")
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A review sync: %v", err)
	}

	t.Chdir(xyClone)
	out, errOut, err := run(t, bWC.app, "mark", "wc-ab2c", "in-progress")
	if ExitCodeFromError(err) != exitFailure {
		t.Fatalf("expected not-claimable after ticket-root refresh, got %v (stderr %q)", err, errOut)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("failed claim must not print a path, got %q", out)
	}
	if strings.Contains(errOut, "config_unparseable:") {
		t.Errorf("must refresh the ticket git-root, not ambient xy / --all; got %q", errOut)
	}
	if !strings.Contains(err.Error()+errOut, "no longer todo") {
		t.Errorf("want no-longer-todo after ticket-root refresh, got err=%v stderr=%q", err, errOut)
	}
}

func TestClaimNextSelectsAfterRemoteStoleFormerNext(t *testing.T) {
	requireGit(t)
	a, b, _ := twoMachines(t)
	addTicket(t, a.scopeDir(), "wc-cd3e", "beta", "todo", "a1", "# beta\n", false, "")
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A add-second sync: %v", err)
	}
	if _, _, err := b.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("B catch-up: %v", err)
	}

	if _, _, err := run(t, a.app, "next", "--claim", "--scope", "wc"); err != nil {
		t.Fatalf("A claim: %v", err)
	}

	out, errOut, err := run(t, b.app, "next", "--claim", "--scope", "wc")
	if err != nil {
		t.Fatalf("B claim after A stole former next: %v (stderr %q)", err, errOut)
	}
	assertClaimStdoutPath(t, out, "wc-cd3e-beta.md")
	if st := fmStatus(t, strings.TrimSpace(out)); st != "in-progress" {
		t.Errorf("B should claim the new next, got status %q", st)
	}
}

func TestClaimEmptyAfterRefreshEmitsUnpushed(t *testing.T) {
	requireGit(t)
	a, b, _ := twoMachines(t)
	_ = a

	b.mark(t, "wc-ab2c", "review")
	_, errOut, err := run(t, b.app, "next", "--claim", "--scope", "wc")
	if ExitCodeFromError(err) != exitFailure {
		t.Fatalf("empty post-refresh queue must fail, got %v (stderr %q)", err, errOut)
	}
	if !strings.Contains(errOut, "sync_needed: unpushed") {
		t.Errorf("refresh that left HEAD ahead must emit sync_needed: unpushed, got %q", errOut)
	}
}

func TestClaimNextRefreshesWhenLocalQueueEmpty(t *testing.T) {
	requireGit(t)
	a, b, _ := twoMachines(t)

	b.mark(t, "wc-ab2c", "review")
	addTicket(t, a.scopeDir(), "wc-cd3e", "beta", "todo", "a1", "# beta\n", false, "")
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A new-todo sync: %v", err)
	}

	out, errOut, err := run(t, b.app, "next", "--claim", "--scope", "wc")
	if err != nil {
		t.Fatalf("claim with empty local queue should refresh and take remote todo: %v (stderr %q)", err, errOut)
	}
	assertClaimStdoutPath(t, out, "wc-cd3e-beta.md")
}

func TestClaimNonTodoInProgressDoesNotRefresh(t *testing.T) {
	requireGit(t)
	a, b, _ := twoMachines(t)

	a.mark(t, "wc-ab2c", "review")
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A review sync: %v", err)
	}
	if _, _, err := b.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("B catch-up: %v", err)
	}

	a.mark(t, "wc-ab2c", "done")
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A done sync: %v", err)
	}

	tracking := strings.TrimSpace(gitIn(t, b.clone, "rev-parse", "origin/main"))
	remoteHEAD := strings.Fields(gitIn(t, b.clone, "ls-remote", "origin", "HEAD"))[0]
	if remoteHEAD == tracking {
		t.Fatal("test setup: remote should have A's done commit that B has not fetched")
	}
	out, _, err := run(t, b.app, "mark", "wc-ab2c", "in-progress")
	if err != nil {
		t.Fatalf("review → in-progress must stay a local mark: %v", err)
	}
	assertClaimStdoutPath(t, out, "wc-ab2c-alpha.md")
	if got := strings.TrimSpace(gitIn(t, b.clone, "rev-parse", "origin/main")); got != tracking {
		t.Errorf("non-claim mark must not fetch; origin/main moved from %s to %s", tracking, got)
	}
	if st := fmStatus(t, strings.TrimSpace(out)); st != "in-progress" {
		t.Errorf("local status should be in-progress, got %q", st)
	}
}

func TestClaimRepoDrivenDoesNotRefresh(t *testing.T) {
	requireGit(t)
	remote := newBareRemote(t)
	a := cloneMachine(t, remote)
	dirA := filepath.Join(a.clone, "rd")
	if err := os.MkdirAll(dirA, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, a.app, "scope", "init", dirA, "--name", "rd"); err != nil {
		t.Fatalf("init repo-driven: %v", err)
	}
	addTicket(t, dirA, "rd-ab2c", "alpha", "todo", "a0", "# alpha\n", false, "")
	gitIn(t, a.clone, "add", "-A")
	gitIn(t, a.clone, "commit", "-m", "seed")
	gitIn(t, a.clone, "push", "-u", "origin", "main")

	b := cloneMachine(t, remote)
	dirB := filepath.Join(b.clone, "rd")
	if _, _, err := run(t, b.app, "scope", "import", dirB); err != nil {
		t.Fatalf("B import: %v", err)
	}

	setStatusLine(t, filepath.Join(dirA, "rd-ab2c-alpha.md"), "done")
	gitIn(t, a.clone, "add", "-A")
	gitIn(t, a.clone, "commit", "-m", "A done")
	gitIn(t, a.clone, "push")

	tracking := strings.TrimSpace(gitIn(t, b.clone, "rev-parse", "origin/main"))
	if remoteHEAD := strings.Fields(gitIn(t, b.clone, "ls-remote", "origin", "HEAD"))[0]; remoteHEAD == tracking {
		t.Fatal("test setup: remote should have A's done commit that B has not fetched")
	}
	out, errOut, err := run(t, b.app, "mark", "rd-ab2c", "in-progress")
	if err != nil {
		t.Fatalf("repo-driven mark must not require network: %v (stderr %q)", err, errOut)
	}
	assertClaimStdoutPath(t, out, "rd-ab2c-alpha.md")
	if got := strings.TrimSpace(gitIn(t, b.clone, "rev-parse", "origin/main")); got != tracking {
		t.Errorf("repo-driven mark must not fetch; origin/main moved from %s to %s", tracking, got)
	}
	if strings.Contains(errOut, "sync_needed:") {
		t.Errorf("repo-driven mark must stay quiet, got %q", errOut)
	}
}

func TestClaimNoUpstreamStaysLocal(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	dir, repo := initGitScope(t, app, "wc", true)
	addTicket(t, dir, "wc-ab2c", "one", "todo", "a0", "# One\n", false, "")

	out, errOut, err := run(t, app, "mark", "wc-ab2c", "in-progress")
	if err != nil {
		t.Fatalf("no-upstream mark claim: %v (stderr %q)", err, errOut)
	}
	if strings.Contains(errOut, "sync_disabled:") {
		t.Errorf("no-upstream claim must not fail-closed as sync_disabled, got %q", errOut)
	}
	if got := fmStatus(t, strings.TrimSpace(out)); got != "in-progress" {
		t.Errorf("status: got %q", got)
	}
	log := gitLog(t, repo)
	if len(log) != 1 || log[0] != "tk: wc-ab2c -> in-progress" {
		t.Fatalf("no-upstream claim should self-commit, got %v", log)
	}
}

func TestClaimMidRebaseRefusesWithoutResume(t *testing.T) {
	requireGit(t)
	a, b, _ := twoMachines(t)

	editBody(t, mustSeedTicket(t, a.scopeDir()), "A version of the body")
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A body sync: %v", err)
	}
	editBody(t, mustSeedTicket(t, b.scopeDir()), "B version of the body")
	if _, _, err := b.sync(t, "--scope", "wc"); ExitCodeFromError(err) != exitFailure {
		t.Fatalf("expected paused body conflict")
	}

	for _, args := range [][]string{
		{"next", "--claim", "--scope", "wc"},
		{"mark", "wc-ab2c", "in-progress"},
	} {
		out, errOut, err := run(t, b.app, args...)
		if ExitCodeFromError(err) != exitFailure {
			t.Errorf("%v: want mid-rebase refuse, got %v", args, err)
		}
		if strings.TrimSpace(out) != "" {
			t.Errorf("%v: failed claim must not print a path, got %q", args, out)
		}
		if !strings.Contains(err.Error()+errOut, "mid-sync-conflict") {
			t.Errorf("%v: want mid-sync-conflict, got err=%v stderr=%q", args, err, errOut)
		}
		if st := fmStatus(t, mustSeedTicket(t, b.scopeDir())); st == "in-progress" {
			t.Errorf("%v: must not write in-progress", args)
		}
	}
	if _, err := os.Stat(filepath.Join(b.clone, ".git", "rebase-merge")); err != nil {
		t.Errorf("claim must not resume the rebase: %v", err)
	}
}

func TestClaimPreflightFailureDoesNotWrite(t *testing.T) {
	requireGit(t)
	a, b, _ := twoMachines(t)
	_ = a

	xyDir := filepath.Join(b.clone, "xy")
	if err := os.MkdirAll(xyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, b.app, "scope", "init", xyDir, "--name", "xy", "--code-root", xyDir, "--auto-commit"); err != nil {
		t.Fatalf("init sibling: %v", err)
	}
	writeCue(t, xyDir, "name: \"xy\"\nautoCommit: false\n")

	out, errOut, err := run(t, b.app, "mark", "wc-ab2c", "in-progress")
	if ExitCodeFromError(err) != exitFailure {
		t.Fatalf("auto-commit mismatch preflight must fail, got %v (stderr %q)", err, errOut)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("failed claim must not print a path, got %q", out)
	}
	if !strings.Contains(errOut, token.AutoCommitMismatch) && !strings.Contains(err.Error(), token.AutoCommitMismatch) {
		t.Errorf("want auto_commit_mismatch, got err=%v stderr=%q", err, errOut)
	}
	if st := fmStatus(t, mustSeedTicket(t, b.scopeDir())); st != "todo" {
		t.Errorf("preflight failure must leave todo, got %q", st)
	}
}

func TestClaimPushFailureKeepsWrite(t *testing.T) {
	requireGit(t)
	a, b, _ := twoMachines(t)
	_ = a
	denyPushes(t, b.clone)

	out, errOut, err := run(t, b.app, "mark", "wc-ab2c", "in-progress")
	if ExitCodeFromError(err) != exitFailure {
		t.Fatalf("post-claim push failure must be non-zero, got %v (stderr %q)", err, errOut)
	}
	assertClaimStdoutPath(t, out, "wc-ab2c-alpha.md")
	if st := fmStatus(t, strings.TrimSpace(out)); st != "in-progress" {
		t.Errorf("local write must stand, got %q", st)
	}
	if !strings.Contains(errOut, "sync_needed: push failed") {
		t.Errorf("want sync_needed: push failed, got %q", errOut)
	}
	if strings.Contains(errOut, "sync_needed: unpushed") {
		t.Errorf("must not emit unpushed before/with push failed, got %q", errOut)
	}
	if _, ok := gitstate.ReadLastPushError(b.app.StateDir, b.clone); !ok {
		t.Error("last-push-error marker must be set")
	}
}

func TestClaimNextPushFailureKeepsWrite(t *testing.T) {
	requireGit(t)
	a, b, _ := twoMachines(t)
	_ = a
	denyPushes(t, b.clone)

	out, errOut, err := run(t, b.app, "next", "--claim", "--scope", "wc")
	if ExitCodeFromError(err) != exitFailure {
		t.Fatalf("post-claim push failure must be non-zero, got %v (stderr %q)", err, errOut)
	}
	assertClaimStdoutPath(t, out, "wc-ab2c-alpha.md")
	if st := fmStatus(t, strings.TrimSpace(out)); st != "in-progress" {
		t.Errorf("local write must stand, got %q", st)
	}
	if !strings.Contains(errOut, "sync_needed: push failed") {
		t.Errorf("want sync_needed: push failed, got %q", errOut)
	}
	if strings.Contains(errOut, "sync_needed: unpushed") {
		t.Errorf("must not emit unpushed, got %q", errOut)
	}
}

func assertClaimStdoutPath(t *testing.T, out, wantSuffix string) {
	t.Helper()
	got := strings.TrimSpace(out)
	if strings.Count(got, "\n") != 0 {
		t.Errorf("stdout must be exactly one path line, got %q", out)
	}
	if !strings.HasSuffix(got, wantSuffix) {
		t.Errorf("stdout path: got %q, want suffix %q", got, wantSuffix)
	}
}

func TestPushRootIfAheadDoesNotRePreflight(t *testing.T) {
	requireGit(t)
	remote := newBareRemote(t)
	m := cloneMachine(t, remote)
	dir := m.initScopeAutoCommit(t)
	addTicket(t, dir, "wc-ab2c", "alpha", "todo", "a0", "# Alpha\n", false, "")
	gitIn(t, m.clone, "add", "-A")
	gitIn(t, m.clone, "commit", "-m", "seed")
	gitIn(t, m.clone, "push", "-u", "origin", "main")
	m.mark(t, "wc-ab2c", "review")

	sib := filepath.Join(m.clone, "sib")
	if err := os.MkdirAll(sib, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, m.app, "scope", "init", sib, "--name", "sib", "--code-root", sib, "--auto-commit"); err != nil {
		t.Fatalf("init sibling: %v", err)
	}
	// autoCommit false on a same-root sibling would fail syncPreflight
	// (auto_commit_mismatch). PushRootIfAhead must still push.
	writeCue(t, sib, "name: \"sib\"\nautoCommit: false\n")

	reg, err := registry.NewStore(m.app.Ctx, m.app.ConfigDir).Load()
	if err != nil {
		t.Fatal(err)
	}
	db, err := index.Open(m.app.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	deps := syncengine.Deps{
		Ctx: t.Context(), Cue: m.app.Ctx, StateDir: m.app.StateDir,
		Reg: reg, DB: db, Rec: reconcile.New(db, m.app.Ctx),
	}
	var errb strings.Builder
	if err := syncengine.PushRootIfAhead(deps, pushTestRep{err: &errb}, syncengine.RootTarget(deps, m.clone)); err != nil {
		t.Fatalf("push must not re-run merge preflight: %v (stderr %s)", err, errb.String())
	}
	if n := strings.TrimSpace(gitIn(t, m.clone, "rev-list", "--count", "@{u}..HEAD")); n != "0" {
		t.Errorf("should be fully pushed, unpushed=%s", n)
	}
}

type pushTestRep struct{ err *strings.Builder }

func (r pushTestRep) Out(string) {}
func (r pushTestRep) Err(line string) {
	r.err.WriteString(line)
	r.err.WriteByte('\n')
}

func denyPushes(t *testing.T, clone string) {
	t.Helper()
	// Hermetic clears templates, so .git/hooks is often absent after init.
	hooksDir := filepath.Join(clone, ".git", "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, clone, "config", "core.hooksPath", hooksDir)
	if err := os.WriteFile(filepath.Join(hooksDir, "pre-push"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}
