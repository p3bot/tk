package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/token"
)

func TestSyncDesignSnapshotSubject(t *testing.T) {
	requireGit(t)
	remote := newBareRemote(t)
	m := cloneMachine(t, remote)
	dir := m.initScopeAutoCommit(t)
	if _, _, err := m.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("initial sync: %v", err)
	}

	writeDesign(t, dir, "wc-ab2c", "shape", "draft", "2026-01-01T00:00:00Z", "Shape")
	if _, _, err := m.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("design add sync: %v", err)
	}
	if got := topCommit(t, m.clone); got != "tk: design wc wc-ab2c" {
		t.Errorf("add snapshot = %q", got)
	}

	path := filepath.Join(dir, "design", "wc-ab2c-shape.md")
	if err := os.WriteFile(path, []byte("---\nid: wc-ab2c\nstatus: accepted\ncreated: 2026-01-01T00:00:00Z\n---\n# Shape\n\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("design edit sync: %v", err)
	}
	if got := topCommit(t, m.clone); got != "tk: design wc wc-ab2c" {
		t.Errorf("edit snapshot = %q", got)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("design delete sync: %v", err)
	}
	if got := topCommit(t, m.clone); got != "tk: design wc wc-ab2c" {
		t.Errorf("delete snapshot = %q", got)
	}
}

func TestDesignCreateDoesNotSelfCommit(t *testing.T) {
	requireGit(t)
	remote := newBareRemote(t)
	m := cloneMachine(t, remote)
	dir := m.initScopeAutoCommit(t)
	if _, _, err := m.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("initial sync: %v", err)
	}
	before := topCommit(t, m.clone)
	_, errOut, err := run(t, m.app, "design", "create", "Queue shape", "--scope", "wc")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.Contains(errOut, token.SyncNeeded) {
		t.Fatalf("tk-driven create stderr = %q", errOut)
	}
	if got := topCommit(t, m.clone); got != before {
		t.Fatalf("create must not commit, log head %q was %q", got, before)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "design"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("design file missing: %v %v", entries, err)
	}
}

func TestDesignMarkAndMetaSelfCommitMessages(t *testing.T) {
	requireGit(t)
	remote := newBareRemote(t)
	m := cloneMachine(t, remote)
	dir := m.initScopeAutoCommit(t)
	if _, _, err := m.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("initial sync: %v", err)
	}
	addTicket(t, dir, "wc-m4np", "target", "todo", "a0", "# Target\n", false, "")
	writeDesign(t, dir, "wc-ab2c", "shape", "draft", "2026-01-01T00:00:00Z", "Shape")
	if _, _, err := m.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("seed sync: %v", err)
	}
	if got := topCommit(t, m.clone); got != "tk: sync 2 path(s)" {
		t.Fatalf("multi-path snapshot = %q", got)
	}

	if _, _, err := run(t, m.app, "design", "mark", "accepted", "wc-ab2c", "--scope", "wc"); err != nil {
		t.Fatalf("mark: %v", err)
	}
	if got := topCommit(t, m.clone); got != "tk: wc-ab2c -> accepted" {
		t.Errorf("mark commit = %q", got)
	}
	if _, _, err := run(t, m.app, "design", "meta", "add", "wc-ab2c", "produces", "wc-m4np", "--scope", "wc"); err != nil {
		t.Fatalf("meta: %v", err)
	}
	if got := topCommit(t, m.clone); got != "tk: wc-ab2c meta add produces" {
		t.Errorf("meta commit = %q", got)
	}
}

func TestSyncDesignConflictPauses(t *testing.T) {
	requireGit(t)
	a, b, _ := twoMachines(t)
	writeDesign(t, a.scopeDir(), "wc-gh56", "shape", "draft", "2026-01-01T00:00:00Z", "Shape")
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A add design: %v", err)
	}
	if _, _, err := b.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("B pull design: %v", err)
	}
	writeDesignBody(t, a.scopeDir(), "wc-gh56-shape.md", "a-edit\n")
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A edit: %v", err)
	}
	writeDesignBody(t, b.scopeDir(), "wc-gh56-shape.md", "b-edit\n")
	commitLocal(t, b.clone, "B design")
	_, errOut, err := b.sync(t, "--scope", "wc")
	if ExitCodeFromError(err) != exitFailure {
		t.Fatalf("conflicted design must pause, got %v stderr %q", err, errOut)
	}
	if !strings.Contains(errOut, "conflicted design:") {
		t.Errorf("want conflicted design:, got %q", errOut)
	}
	if strings.Contains(errOut, "body conflict:") || strings.Contains(errOut, token.StatusConflict) {
		t.Errorf("design must not go through ticket merge, got %q", errOut)
	}
	before, _ := os.ReadFile(filepath.Join(b.scopeDir(), "design", "wc-gh56-shape.md"))
	if !strings.Contains(string(before), "<<<<<<<") {
		t.Fatalf("conflict markers should remain for a human: %q", before)
	}
}

func TestDesignWriteRestoresWhenCommitFails(t *testing.T) {
	requireGit(t)
	remote := newBareRemote(t)
	m := cloneMachine(t, remote)
	dir := m.initScopeAutoCommit(t)
	if _, _, err := m.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("initial sync: %v", err)
	}
	addTicket(t, dir, "wc-m4np", "target", "todo", "a0", "# Target\n", false, "")
	writeDesign(t, dir, "wc-ab2c", "shape", "draft", "2026-01-01T00:00:00Z", "Shape")
	if _, _, err := m.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("seed sync: %v", err)
	}
	path := filepath.Join(dir, "design", "wc-ab2c-shape.md")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	hook := installRefusingHook(t, m.clone)

	_, _, err = run(t, m.app, "design", "mark", "accepted", "wc-ab2c", "--scope", "wc")
	if err == nil || !strings.Contains(err.Error(), "self-commit") {
		t.Fatalf("refused mark = %v", err)
	}
	if got := readFile(t, path); got != string(before) {
		t.Fatalf("failed mark must restore the design:\n%s", got)
	}
	if status := gitIn(t, m.clone, "status", "--short"); status != "" {
		t.Fatalf("failed mark must leave a clean tree, status %q", status)
	}

	if err := os.Remove(hook); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, m.app, "design", "mark", "accepted", "wc-ab2c", "--scope", "wc"); err != nil {
		t.Fatalf("mark retry: %v", err)
	}
	if !strings.Contains(readFile(t, path), "status: accepted") {
		t.Fatalf("retry must set status:\n%s", readFile(t, path))
	}
	if got := topCommit(t, m.clone); got != "tk: wc-ab2c -> accepted" {
		t.Fatalf("retry commit = %q", got)
	}

	before = []byte(readFile(t, path))
	installRefusingHook(t, m.clone)
	_, _, err = run(t, m.app, "design", "meta", "add", "wc-ab2c", "produces", "wc-m4np", "--scope", "wc")
	if err == nil || !strings.Contains(err.Error(), "self-commit") {
		t.Fatalf("refused meta = %v", err)
	}
	if got := readFile(t, path); got != string(before) {
		t.Fatalf("failed meta must restore the design:\n%s", got)
	}

	if err := os.Remove(hook); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, m.app, "design", "meta", "add", "wc-ab2c", "produces", "wc-m4np", "--scope", "wc"); err != nil {
		t.Fatalf("meta retry: %v", err)
	}
	if !strings.Contains(readFile(t, path), "wc-m4np") {
		t.Fatalf("retry must record produces:\n%s", readFile(t, path))
	}
	if got := topCommit(t, m.clone); got != "tk: wc-ab2c meta add produces" {
		t.Fatalf("meta commit = %q", got)
	}

	// A repeat that changes nothing must not commit a waiting body edit.
	if err := os.WriteFile(path, []byte(readFile(t, path)+"pending body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	head := topCommit(t, m.clone)
	if _, _, err := run(t, m.app, "design", "mark", "accepted", "wc-ab2c", "--scope", "wc"); err != nil {
		t.Fatalf("unchanged mark: %v", err)
	}
	if got := topCommit(t, m.clone); got != head {
		t.Fatalf("unchanged mark committed %q, head was %q", got, head)
	}
	if !strings.Contains(gitIn(t, m.clone, "status", "--short"), "wc-ab2c-shape.md") {
		t.Fatal("body edit must stay uncommitted for tk sync")
	}
}

func installRefusingHook(t *testing.T, repo string) string {
	t.Helper()
	hooks := filepath.Join(repo, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "config", "core.hooksPath", hooks)
	hook := filepath.Join(hooks, "pre-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return hook
}

func writeDesignBody(t *testing.T, dir, base, body string) {
	t.Helper()
	path := filepath.Join(dir, "design", base)
	text := "---\nid: wc-gh56\nstatus: draft\ncreated: 2026-01-01T00:00:00Z\n---\n# Shape\n\n" + body
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}
