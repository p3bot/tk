package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/git"
	"github.com/p3bot/tk/internal/testgit"
	"github.com/p3bot/tk/internal/token"
)

func TestScopeAutoCommitRead(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")

	out, errOut, err := run(t, app, "scope", "auto-commit", "--scope", "wc")
	if err != nil {
		t.Fatalf("read false: %v (%s)", err, errOut)
	}
	if out != "false\n" {
		t.Errorf("want false\\n, got %q", out)
	}

	if err := os.WriteFile(filepath.Join(dir, "tk.cue"), []byte("name: \"wc\"\nautoCommit: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, errOut, err = run(t, app, "scope", "auto-commit", "--scope", "wc")
	if err != nil {
		t.Fatalf("read true: %v (%s)", err, errOut)
	}
	if out != "true\n" {
		t.Errorf("want true\\n, got %q", out)
	}

	t.Setenv("TK_SCOPE", "wc")
	out, errOut, err = run(t, app, "scope", "auto-commit")
	if err != nil {
		t.Fatalf("read via TK_SCOPE: %v (%s)", err, errOut)
	}
	if out != "true\n" {
		t.Errorf("ambient read want true\\n, got %q", out)
	}
}

func TestScopeAutoCommitReadUnparseable(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	if err := os.WriteFile(filepath.Join(dir, "tk.cue"), []byte("name: \"wc\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, errOut, err := run(t, app, "scope", "auto-commit", "--scope", "wc")
	if err == nil {
		t.Fatal("unreadable tk.cue must refuse")
	}
	if out != "" {
		t.Errorf("stdout must stay empty, got %q", out)
	}
	msg := err.Error() + errOut
	if !strings.Contains(msg, token.ConfigUnparseable) {
		t.Errorf("want config_unparseable, err=%v errOut=%q", err, errOut)
	}
}

func TestScopeAutoCommitSetTrueFalseDurability(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	dir, repo := initGitScope(t, app, "wc", false)
	ticket, id := createID(t, app, "wc", "Work")

	out, errOut, err := run(t, app, "scope", "auto-commit", "true", "--scope", "wc")
	if err != nil {
		t.Fatalf("set true: %v (%s)", err, errOut)
	}
	wantPath := filepath.Join(dir, "tk.cue")
	if strings.TrimSpace(out) != wantPath {
		t.Errorf("stdout = %q want %q", strings.TrimSpace(out), wantPath)
	}
	log := gitLog(t, repo)
	if len(log) != 1 || log[0] != "tk: scope auto-commit true" {
		t.Fatalf("true set should self-commit once, got %v", log)
	}
	trueNames := testgit.Combined(t, repo, "show", "--name-only", "--pretty=format:", "HEAD")
	if strings.Contains(trueNames, filepath.Base(ticket)) {
		t.Errorf("true set must not snapshot leftover dirt, names:\n%s", trueNames)
	}
	mode, errOut, err := run(t, app, "pulse", "mode", "--scope", "wc")
	if err != nil {
		t.Fatalf("pulse mode: %v (%s)", err, errOut)
	}
	if strings.TrimSpace(mode) != "tk-driven" {
		t.Errorf("mode after true = %q want tk-driven", mode)
	}

	out, errOut, err = run(t, app, "scope", "auto-commit", "false", "--scope", "wc")
	if err != nil {
		t.Fatalf("set false: %v (%s)", err, errOut)
	}
	if strings.TrimSpace(out) != wantPath {
		t.Errorf("false stdout = %q want %q", strings.TrimSpace(out), wantPath)
	}
	if strings.Contains(errOut, token.SyncNeeded) {
		t.Errorf("false set must not emit sync_needed, got %q", errOut)
	}
	if strings.Contains(errOut, "host git push") {
		t.Errorf("false set with no upstream must not emit a host-push diagnostic, got %q", errOut)
	}
	log = gitLog(t, repo)
	if len(log) != 2 || log[0] != "tk: scope auto-commit false" {
		t.Fatalf("false set should self-commit last tk-owned commit, got %v", log)
	}
	mode, _, err = run(t, app, "pulse", "mode", "--scope", "wc")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(mode) != "repo-driven" {
		t.Errorf("mode after false = %q want repo-driven", mode)
	}

	before := gitLog(t, repo)
	if _, errOut, err = run(t, app, "mark", "in-progress", id); err != nil {
		t.Fatalf("mark after false flip: %v (%s)", err, errOut)
	}
	after := gitLog(t, repo)
	if strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Errorf("later mark must not self-commit on repo-driven, before=%v after=%v", before, after)
	}
}

func TestScopeAutoCommitFalseFlipSnapshotsAllowlistedDirty(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	dir, repo := initGitScope(t, app, "wc", false)
	if _, _, err := run(t, app, "scope", "auto-commit", "true", "--scope", "wc"); err != nil {
		t.Fatal(err)
	}
	ticket, _ := createID(t, app, "wc", "Work")
	junk := filepath.Join(dir, "scratch.txt")
	if err := os.WriteFile(junk, []byte("nope\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, errOut, err := run(t, app, "scope", "auto-commit", "false", "--scope", "wc")
	if err != nil {
		t.Fatalf("set false: %v (%s)", err, errOut)
	}
	if strings.TrimSpace(out) != filepath.Join(dir, "tk.cue") {
		t.Errorf("stdout must stay the tk.cue path, got %q", out)
	}
	if !strings.Contains(errOut, token.NonAllowlist) {
		t.Errorf("non-allowlist residue must ride non_allowlist:, got %q", errOut)
	}
	if !strings.Contains(errOut, "scratch.txt") {
		t.Errorf("residue paths must be named, got %q", errOut)
	}
	if strings.Contains(errOut, token.SyncNeeded) {
		t.Errorf("false set must not emit sync_needed, got %q", errOut)
	}

	log := gitLog(t, repo)
	if len(log) < 1 || log[0] != "tk: scope auto-commit false" {
		t.Fatalf("false set should self-commit, got %v", log)
	}
	names := testgit.Combined(t, repo, "show", "--name-only", "--pretty=format:", "HEAD")
	if !strings.Contains(names, filepath.Base(ticket)) {
		t.Errorf("last tk-owned commit must include dirty ticket %s, names:\n%s", ticket, names)
	}
	if strings.Contains(names, "scratch.txt") {
		t.Errorf("non-allowlist residue must not be committed, names:\n%s", names)
	}
}

func TestScopeAutoCommitFalseFlipLeavesStagedResidue(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	dir, repo := initGitScope(t, app, "wc", false)
	if _, _, err := run(t, app, "scope", "auto-commit", "true", "--scope", "wc"); err != nil {
		t.Fatal(err)
	}
	ticket, _ := createID(t, app, "wc", "Work")
	junk := filepath.Join(dir, "scratch.txt")
	if err := os.WriteFile(junk, []byte("nope\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", junk)

	out, errOut, err := run(t, app, "scope", "auto-commit", "false", "--scope", "wc")
	if err != nil {
		t.Fatalf("set false: %v (%s)", err, errOut)
	}
	if strings.TrimSpace(out) != filepath.Join(dir, "tk.cue") {
		t.Errorf("stdout must stay the tk.cue path, got %q", out)
	}
	if !strings.Contains(errOut, token.NonAllowlist) {
		t.Errorf("staged residue must still ride non_allowlist:, got %q", errOut)
	}
	names := testgit.Combined(t, repo, "show", "--name-only", "--pretty=format:", "HEAD")
	if !strings.Contains(names, filepath.Base(ticket)) {
		t.Errorf("last tk-owned commit must include dirty ticket, names:\n%s", names)
	}
	if strings.Contains(names, "scratch.txt") {
		t.Errorf("staged non-allowlist residue must not be committed, names:\n%s", names)
	}
	staged, err := git.HasStagedChanges(context.Background(), repo, junk)
	if err != nil {
		t.Fatal(err)
	}
	if !staged {
		t.Error("staged residue must stay staged for the host")
	}
}

func TestScopeAutoCommitFalseFlipHostPushDiagnostic(t *testing.T) {
	requireGit(t)
	remote := newBareRemote(t)
	m := cloneMachine(t, remote)
	dir := m.initScopeAutoCommit(t)
	addTicket(t, dir, "wc-ab2c", "x", "todo", "a0", "# X\n", false, "")
	gitIn(t, m.clone, "add", "-A")
	gitIn(t, m.clone, "commit", "-m", "seed scope")
	gitIn(t, m.clone, "push", "-u", "origin", "main")

	out, errOut, err := run(t, m.app, "scope", "auto-commit", "false", "--scope", "wc")
	if err != nil {
		t.Fatalf("set false: %v (%s)", err, errOut)
	}
	if !strings.Contains(strings.TrimSpace(out), "tk.cue") {
		t.Errorf("want tk.cue path, got %q", out)
	}
	if strings.Contains(errOut, token.SyncNeeded) {
		t.Errorf("false set must not emit sync_needed, got %q", errOut)
	}
	if !strings.Contains(errOut, "host git push") {
		t.Errorf("unpushed last commit must name host git push, got %q", errOut)
	}
	if token.HasKnownPrefix(strings.TrimSpace(errOut)) {
		t.Errorf("host-push line must be non-token, got %q", errOut)
	}
}

func TestScopeAutoCommitTrueSyncNeededUnpushed(t *testing.T) {
	requireGit(t)
	remote := newBareRemote(t)
	m := cloneMachine(t, remote)
	dir := filepath.Join(m.clone, "wc")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, m.app, "scope", "init", dir, "--name", "wc"); err != nil {
		t.Fatalf("init repo-driven: %v", err)
	}
	gitIn(t, m.clone, "add", "-A")
	gitIn(t, m.clone, "commit", "-m", "seed scope")
	gitIn(t, m.clone, "push", "-u", "origin", "main")

	_, errOut, err := run(t, m.app, "scope", "auto-commit", "true", "--scope", "wc")
	if err != nil {
		t.Fatalf("set true: %v (%s)", err, errOut)
	}
	if !strings.Contains(errOut, "sync_needed: unpushed") {
		t.Errorf("true set ahead of upstream must ride sync_needed: unpushed, got %q", errOut)
	}
}

func TestScopeAutoCommitGitRootSiblings(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "a@b.c")
	runGit(t, repo, "config", "user.name", "tk-test")
	runGit(t, repo, "config", "commit.gpgsign", "false")

	dirA := filepath.Join(repo, "dir-z")
	dirZ := filepath.Join(repo, "dir-a")
	if err := os.MkdirAll(dirA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dirZ, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, app, "scope", "init", dirA, "--name", "aa", "--code-root", dirA); err != nil {
		t.Fatalf("init aa: %v", err)
	}
	if _, _, err := run(t, app, "scope", "init", dirZ, "--name", "zz", "--code-root", dirZ); err != nil {
		t.Fatalf("init zz: %v", err)
	}

	otherDir, otherRepo := initGitScope(t, app, "ot", false)
	otherBefore, err := os.ReadFile(filepath.Join(otherDir, "tk.cue"))
	if err != nil {
		t.Fatal(err)
	}

	out, errOut, err := run(t, app, "scope", "auto-commit", "true", "--scope", "aa")
	if err != nil {
		t.Fatalf("set true: %v (%s)", err, errOut)
	}
	got := lines(out)
	want := []string{filepath.Join(dirZ, "tk.cue"), filepath.Join(dirA, "tk.cue")}
	if len(got) != 2 {
		t.Fatalf("want 2 paths, got %q", out)
	}
	if got[0] != want[0] || got[1] != want[1] {
		t.Errorf("stdout order = %v want sorted path %v", got, want)
	}

	for _, dir := range []string{dirA, dirZ} {
		data, err := os.ReadFile(filepath.Join(dir, "tk.cue"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "autoCommit: true") {
			t.Errorf("%s not rewritten to true:\n%s", dir, data)
		}
	}
	otherAfter, err := os.ReadFile(filepath.Join(otherDir, "tk.cue"))
	if err != nil {
		t.Fatal(err)
	}
	if string(otherBefore) != string(otherAfter) {
		t.Errorf("other git-root must be left alone\nbefore:\n%s\nafter:\n%s", otherBefore, otherAfter)
	}
	log := gitLog(t, repo)
	if len(log) != 1 || log[0] != "tk: scope auto-commit true" {
		t.Errorf("one commit on the target root, got %v", log)
	}
	if n := gitLog(t, otherRepo); len(n) != 0 {
		t.Errorf("other repo must stay uncommitted, got %v", n)
	}
}

func TestScopeAutoCommitAlreadyThatValue(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	dir, _ := initGitScope(t, app, "wc", true)
	cuePath := filepath.Join(dir, "tk.cue")
	before, err := os.ReadFile(cuePath)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(cuePath)
	if err != nil {
		t.Fatal(err)
	}

	out, errOut, err := run(t, app, "scope", "auto-commit", "true", "--scope", "wc")
	if err != nil {
		t.Fatalf("already true: %v (%s)", err, errOut)
	}
	if ExitCodeFromError(err) != exitOK {
		t.Errorf("exit = %d want 0", ExitCodeFromError(err))
	}
	if out != "" {
		t.Errorf("stdout must be empty, got %q", out)
	}
	if !strings.Contains(errOut, "already true") {
		t.Errorf("stderr must name already true, got %q", errOut)
	}
	if token.HasKnownPrefix(strings.TrimSpace(errOut)) {
		t.Errorf("already-that-value must be non-token, got %q", errOut)
	}
	after, err := os.ReadFile(cuePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("files must be untouched")
	}
	info2, err := os.Stat(cuePath)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(info2.ModTime()) {
		t.Error("mtime must be untouched")
	}
}

func TestScopeAutoCommitMismatchIsRewriteNotNoop(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	dir, repo := initGitScope(t, app, "aa", false)
	dirB := filepath.Join(repo, "bb")
	if err := os.MkdirAll(dirB, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, app, "scope", "init", dirB, "--name", "bb", "--code-root", dirB); err != nil {
		t.Fatalf("init bb: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dirB, "tk.cue"), []byte("name: \"bb\"\nautoCommit: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, errOut, err := run(t, app, "scope", "auto-commit", "true", "--scope", "aa")
	if err != nil {
		t.Fatalf("mismatch set: %v (%s)", err, errOut)
	}
	if len(lines(out)) != 2 {
		t.Errorf("mismatched pair must rewrite both, got %q", out)
	}
	for _, d := range []string{dir, dirB} {
		data, err := os.ReadFile(filepath.Join(d, "tk.cue"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "autoCommit: true") {
			t.Errorf("%s want true:\n%s", d, data)
		}
	}
}

func TestScopeAutoCommitUnreadableSiblingRefuses(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	dir, repo := initGitScope(t, app, "aa", false)
	dirB := filepath.Join(repo, "bb")
	if err := os.MkdirAll(dirB, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, app, "scope", "init", dirB, "--name", "bb", "--code-root", dirB); err != nil {
		t.Fatalf("init bb: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dirB, "tk.cue"), []byte("name: \"bb\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "tk.cue"))
	if err != nil {
		t.Fatal(err)
	}

	out, errOut, err := run(t, app, "scope", "auto-commit", "true", "--scope", "aa")
	if err == nil {
		t.Fatal("unreadable sibling must refuse")
	}
	if out != "" {
		t.Errorf("stdout must stay empty, got %q", out)
	}
	msg := err.Error() + errOut
	if !strings.Contains(msg, token.ConfigUnparseable) {
		t.Errorf("want config_unparseable, err=%v errOut=%q", err, errOut)
	}
	after, err := os.ReadFile(filepath.Join(dir, "tk.cue"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("refused set must leave every tk.cue\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestScopeAutoCommitUnreachableSiblingRefuses(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	dir, repo := initGitScope(t, app, "aa", false)
	dirB := filepath.Join(repo, "bb")
	if err := os.MkdirAll(dirB, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, app, "scope", "init", dirB, "--name", "bb", "--code-root", dirB); err != nil {
		t.Fatalf("init bb: %v", err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "tk.cue"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dirB); err != nil {
		t.Fatal(err)
	}

	out, errOut, err := run(t, app, "scope", "auto-commit", "true", "--scope", "aa")
	if err == nil {
		t.Fatal("unreachable sibling must refuse")
	}
	if out != "" {
		t.Errorf("stdout must stay empty, got %q", out)
	}
	msg := err.Error() + errOut
	if !strings.Contains(msg, token.UnreachableScope) {
		t.Errorf("want unreachable_scope, err=%v errOut=%q", err, errOut)
	}
	after, err := os.ReadFile(filepath.Join(dir, "tk.cue"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("refused set must leave remaining tk.cue")
	}
}

func TestScopeAutoCommitSiblingPackagePin(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	dir, _ := initGitScope(t, app, "wc", false)
	if err := os.WriteFile(filepath.Join(dir, "tk.cue"), []byte(
		"package wccfg\nname: \"wc\"\nautoCommit: false\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "schema.cue"), []byte(
		"package wccfg\nautoCommit: false\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "tk.cue"))
	if err != nil {
		t.Fatal(err)
	}
	sibBefore, err := os.ReadFile(filepath.Join(dir, "schema.cue"))
	if err != nil {
		t.Fatal(err)
	}

	out, errOut, err := run(t, app, "scope", "auto-commit", "true", "--scope", "wc")
	if err == nil {
		t.Fatal("sibling pin must refuse")
	}
	if out != "" {
		t.Errorf("stdout must stay empty, got %q", out)
	}
	msg := err.Error() + errOut
	if !strings.Contains(msg, token.ConfigUnparseable) && !strings.Contains(msg, "pins autoCommit") {
		t.Errorf("want sibling-pin refuse (config_unparseable or pins autoCommit), err=%v errOut=%q", err, errOut)
	}
	after, err := os.ReadFile(filepath.Join(dir, "tk.cue"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("must restore tk.cue\nbefore:\n%s\nafter:\n%s", before, after)
	}
	sibAfter, err := os.ReadFile(filepath.Join(dir, "schema.cue"))
	if err != nil {
		t.Fatal(err)
	}
	if string(sibBefore) != string(sibAfter) {
		t.Errorf("must not edit sibling package file")
	}
}

func TestScopeAutoCommitMidRebaseRefusesFalseCurrent(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	dir, repo := initGitScope(t, app, "wc", false)
	if err := os.MkdirAll(filepath.Join(repo, ".git", "rebase-merge"), 0o755); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "tk.cue"))
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := run(t, app, "scope", "auto-commit", "true", "--scope", "wc")
	if ExitCodeFromError(err) != exitFailure {
		t.Errorf("mid-rebase set should refuse non-zero, got %v", err)
	}
	if out != "" {
		t.Errorf("stdout must stay empty, got %q", out)
	}
	after, err := os.ReadFile(filepath.Join(dir, "tk.cue"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("mid-rebase must not rewrite tk.cue")
	}
}

func TestScopeAutoCommitMidRebaseRefusesTrueCurrent(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	dir, repo := initGitScope(t, app, "wc", true)
	if err := os.MkdirAll(filepath.Join(repo, ".git", "rebase-merge"), 0o755); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "tk.cue"))
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := run(t, app, "scope", "auto-commit", "false", "--scope", "wc")
	if ExitCodeFromError(err) != exitFailure {
		t.Errorf("mid-rebase false flip should refuse non-zero, got %v", err)
	}
	if out != "" {
		t.Errorf("stdout must stay empty, got %q", out)
	}
	after, err := os.ReadFile(filepath.Join(dir, "tk.cue"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("mid-rebase must not rewrite tk.cue")
	}
}

func TestScopeAutoCommitCommitFailRestores(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	dir, repo := initGitScope(t, app, "wc", false)
	cuePath := filepath.Join(dir, "tk.cue")
	before, err := os.ReadFile(cuePath)
	if err != nil {
		t.Fatal(err)
	}

	hooks := filepath.Join(repo, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "config", "core.hooksPath", hooks)
	if err := os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	out, _, err := run(t, app, "scope", "auto-commit", "true", "--scope", "wc")
	if err == nil {
		t.Fatal("want self-commit failure")
	}
	if out != "" {
		t.Errorf("stdout must stay empty, got %q", out)
	}
	after, err := os.ReadFile(cuePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("failed commit must restore tk.cue\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if n := gitLog(t, repo); len(n) != 0 {
		t.Errorf("failed commit must not leave a commit, got %v", n)
	}
	staged, err := git.HasStagedChanges(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if staged {
		t.Error("failed commit must not leave tk.cue staged")
	}

	if err := os.Remove(filepath.Join(hooks, "pre-commit")); err != nil {
		t.Fatal(err)
	}
	out, errOut, err := run(t, app, "scope", "auto-commit", "true", "--scope", "wc")
	if err != nil {
		t.Fatalf("retry after restore: %v (%s)", err, errOut)
	}
	if strings.Contains(errOut, "already true") {
		t.Errorf("retry must not take the ensure path, got %q", errOut)
	}
	data, err := os.ReadFile(cuePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "autoCommit: true") {
		t.Errorf("retry want true:\n%s", data)
	}
	log := gitLog(t, repo)
	if len(log) != 1 || log[0] != "tk: scope auto-commit true" {
		t.Errorf("retry should self-commit, got %v", log)
	}
}

func TestScopeAutoCommitNoGitRoot(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")

	out, errOut, err := run(t, app, "scope", "auto-commit", "true", "--scope", "wc")
	if err != nil {
		t.Fatalf("true without git-root: %v (%s)", err, errOut)
	}
	if !strings.HasSuffix(strings.TrimSpace(out), "tk.cue") {
		t.Errorf("want tk.cue path, got %q", out)
	}
	if !strings.Contains(errOut, token.SyncDisabled) {
		t.Errorf("true with no git-root must emit sync_disabled, got %q", errOut)
	}
	data, err := os.ReadFile(filepath.Join(dir, "tk.cue"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "autoCommit: true") {
		t.Errorf("want true written:\n%s", data)
	}

	out, errOut, err = run(t, app, "scope", "auto-commit", "false", "--scope", "wc")
	if err != nil {
		t.Fatalf("false without git-root: %v (%s)", err, errOut)
	}
	if strings.Contains(errOut, token.SyncDisabled) {
		t.Errorf("false with no git-root must not emit sync_disabled, got %q", errOut)
	}
	if !strings.HasSuffix(strings.TrimSpace(out), "tk.cue") {
		t.Errorf("want tk.cue path, got %q", out)
	}
}

func TestScopeAutoCommitUsage(t *testing.T) {
	app := newApp(t)
	initScope(t, app, "wc")

	cases := []struct {
		name string
		args []string
	}{
		{"unknown value", []string{"scope", "auto-commit", "maybe", "--scope", "wc"}},
		{"mode name", []string{"scope", "auto-commit", "tk-driven", "--scope", "wc"}},
		{"two positionals", []string{"scope", "auto-commit", "true", "false", "--scope", "wc"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, _, err := run(t, app, c.args...)
			if ExitCodeFromError(err) != exitUsage {
				t.Errorf("exit = %d want 2 (err=%v)", ExitCodeFromError(err), err)
			}
			if out != "" {
				t.Errorf("stdout must stay empty, got %q", out)
			}
		})
	}
}

func TestScopeHelpMentionsAutoCommit(t *testing.T) {
	app := newApp(t)
	out, _, err := run(t, app, "scope", "--help")
	if err != nil {
		t.Fatalf("scope --help: %v", err)
	}
	if !strings.Contains(out, "auto-commit") {
		t.Errorf("scope --help must mention auto-commit, got:\n%s", out)
	}
	fieldOut, _, err := run(t, app, "scope", "field", "--help")
	if err != nil {
		t.Fatalf("scope field --help: %v", err)
	}
	if strings.Contains(fieldOut, "auto-commit") {
		t.Errorf("scope field help must not claim auto-commit, got:\n%s", fieldOut)
	}

	_, _, err = run(t, app, "scope", "mode")
	if ExitCodeFromError(err) != exitUsage {
		t.Errorf("scope mode must stay unknown, got %v", err)
	}
}
