package writeengine

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/depgate"
	"github.com/p3bot/tk/internal/git"
	"github.com/p3bot/tk/internal/testgit"
	"github.com/p3bot/tk/internal/token"
)

func TestMarkArchiveRelocate(t *testing.T) {
	e := newPlainEnv(t, "wc", "name: \"wc\"\nautoCommit: false\n")
	path := addTicket(t, e.dir, "wc-ab2c", "todo")

	res, err := Mark(e.deps, nil, MarkInput{Scope: "wc", Dir: e.dir, Lookup: fullLookup("wc-ab2c"), NewStatus: "done"})
	if err != nil {
		t.Fatalf("mark done: %v", err)
	}
	if filepath.Dir(res.Path) != filepath.Join(e.dir, "archive") {
		t.Errorf("done path = %q, want archive/", res.Path)
	}
	if !res.Moved {
		t.Error("want Moved")
	}
	if _, err := os.Stat(res.Path); err != nil {
		t.Errorf("archive file missing: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("old root path still present")
	}

	res, err = Mark(e.deps, nil, MarkInput{Scope: "wc", Dir: e.dir, Lookup: fullLookup("wc-ab2c"), NewStatus: "todo"})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if filepath.Dir(res.Path) != e.dir {
		t.Errorf("reopen path = %q, want dir root", res.Path)
	}
}

func TestMarkRefusesUnknownDuplicateParseUnusable(t *testing.T) {
	e := newPlainEnv(t, "wc", "name: \"wc\"\nautoCommit: false\n")
	addTicket(t, e.dir, "wc-ab2c", "todo")

	_, err := Mark(e.deps, nil, MarkInput{Scope: "wc", Dir: e.dir, Lookup: fullLookup("wc-ab2c"), NewStatus: "nope"})
	var unk *UnknownStatusError
	if !errors.As(err, &unk) {
		t.Errorf("unknown status: got %v", err)
	}

	writeFile(t, filepath.Join(e.dir, "wc-abcd-x.md"), "---\nid: wc-abcd\nstatus: [unterminated\n---\n# broke\n")
	_, err = Mark(e.deps, nil, MarkInput{Scope: "wc", Dir: e.dir, Lookup: fullLookup("wc-abcd"), NewStatus: "done"})
	var pe *ParseQuarantineError
	if !errors.As(err, &pe) {
		t.Errorf("parse quarantine: got %v", err)
	}
	if err != nil && !strings.Contains(err.Error(), token.ParseError) {
		t.Errorf("want parse_error token, got %v", err)
	}

	src := filepath.Join(e.dir, "wc-ab2c-work.md")
	data, _ := os.ReadFile(src)
	writeFile(t, filepath.Join(e.dir, "wc-ab2c-dup.md"), string(data))
	_, err = Mark(e.deps, nil, MarkInput{Scope: "wc", Dir: e.dir, Lookup: fullLookup("wc-ab2c"), NewStatus: "done"})
	var dup *DuplicateError
	if !errors.As(err, &dup) {
		t.Errorf("duplicate: got %v", err)
	}

	bad := newPlainEnv(t, "zz", "name: \"zz\"\nthis is not cue {\n")
	_, err = Mark(bad.deps, nil, MarkInput{Scope: "zz", Dir: bad.dir, Lookup: fullLookup("zz-ab2c"), NewStatus: "done"})
	var un *UnusableError
	if !errors.As(err, &un) {
		t.Errorf("unusable: got %v", err)
	}
	if err != nil && !strings.Contains(err.Error(), token.ConfigUnparseable) {
		t.Errorf("want config_unparseable, got %v", err)
	}
}

func TestMidRebaseRefusesMarkAndClaim(t *testing.T) {
	if !git.Available() {
		t.Skip("git not on PATH")
	}
	e, repo := initAutoCommitRepo(t, "wc")
	path := addTicket(t, e.dir, "wc-ab2c", "todo")
	if err := os.MkdirAll(filepath.Join(repo, ".git", "rebase-merge"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := Mark(e.deps, nil, MarkInput{Scope: "wc", Dir: e.dir, Lookup: fullLookup("wc-ab2c"), NewStatus: "done"})
	var mid *MidRebaseError
	if !errors.As(err, &mid) {
		t.Fatalf("mark: want mid-rebase, got %v", err)
	}
	_, err = Claim(e.deps, nil, ClaimInput{Kind: ClaimNext, Scope: "wc", Dir: e.dir})
	if !errors.As(err, &mid) {
		t.Fatalf("claim: want mid-rebase, got %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "status: todo") {
		t.Errorf("must not write, got %s", data)
	}
}

func TestMarkSelfCommitFailKeepsWarnings(t *testing.T) {
	if !git.Available() {
		t.Skip("git not on PATH")
	}
	e, repo := initAutoCommitRepo(t, "wc")
	addTicket(t, e.dir, "wc-ab2c", "todo")
	writeFile(t, filepath.Join(e.dir, "wc-abcd-x.md"), "---\nid: wc-abcd\nstatus: [unterminated\n---\n# broke\n")

	hooks := filepath.Join(repo, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	testgit.Run(t, repo, "config", "core.hooksPath", hooks)
	if err := os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	res, err := Mark(e.deps, nil, MarkInput{Scope: "wc", Dir: e.dir, Lookup: fullLookup("wc-ab2c"), NewStatus: "done"})
	if err == nil {
		t.Fatal("want self-commit failure")
	}
	if res.Path != "" {
		t.Errorf("failed durability must not set path, got %q", res.Path)
	}
	found := false
	for _, w := range res.Warnings {
		if strings.Contains(w, token.ParseError) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("want parse_error warning on failed self-commit, got %v", res.Warnings)
	}
}

func TestClaimNoLongerTodoDoesNotWrite(t *testing.T) {
	e := newPlainEnv(t, "wc", "name: \"wc\"\nautoCommit: false\n")
	path := addTicket(t, e.dir, "wc-ab2c", "review")

	res, err := Claim(e.deps, nil, ClaimInput{Kind: ClaimID, Scope: "wc", Dir: e.dir, Lookup: fullLookup("wc-ab2c")})
	var nl *NoLongerTodoError
	if !errors.As(err, &nl) {
		t.Fatalf("want no-longer-todo, got %v", err)
	}
	if res.Path != "" {
		t.Errorf("no write path on refuse, got %q", res.Path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "status: review") {
		t.Errorf("file must stay review, got %s", data)
	}
}

func TestClaimNextEmptyQueue(t *testing.T) {
	e := newPlainEnv(t, "wc", "name: \"wc\"\nautoCommit: false\n")
	addTicket(t, e.dir, "wc-cd3e", "draft")

	_, err := Claim(e.deps, nil, ClaimInput{Kind: ClaimNext, Scope: "wc", Dir: e.dir})
	var empty *depgate.EmptyQueueError
	if !errors.As(err, &empty) {
		t.Fatalf("want empty queue, got %v", err)
	}
}

func TestMarkTodoInProgressIsClaim(t *testing.T) {
	e := newPlainEnv(t, "wc", "name: \"wc\"\nautoCommit: false\n")
	path := addTicket(t, e.dir, "wc-ab2c", "todo")

	res, err := Mark(e.deps, nil, MarkInput{Scope: "wc", Dir: e.dir, Lookup: fullLookup("wc-ab2c"), NewStatus: "in-progress"})
	if err != nil {
		t.Fatalf("mark claim: %v", err)
	}
	if res.NewStatus != "in-progress" {
		t.Errorf("status = %q", res.NewStatus)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "status: in-progress") {
		t.Errorf("file not claimed: %s", data)
	}
}

func TestClaimRefreshFailedDoesNotWrite(t *testing.T) {
	if !git.Available() {
		t.Skip("git not on PATH")
	}
	e, repo := initAutoCommitRepo(t, "wc")
	path := addTicket(t, e.dir, "wc-ab2c", "todo")

	bare := filepath.Join(t.TempDir(), "remote.git")
	testgit.Run(t, filepath.Dir(bare), "init", "--bare", "-b", "main", filepath.Base(bare))
	testgit.Run(t, repo, "add", "-A")
	testgit.Run(t, repo, "commit", "-m", "seed")
	testgit.Run(t, repo, "remote", "add", "origin", bare)
	testgit.Run(t, repo, "push", "-u", "origin", "HEAD:main")
	if err := os.RemoveAll(bare); err != nil {
		t.Fatal(err)
	}

	res, err := Claim(e.deps, nil, ClaimInput{Kind: ClaimID, Scope: "wc", Dir: e.dir, Lookup: fullLookup("wc-ab2c")})
	if !errors.Is(err, ErrRefreshFailed) {
		t.Fatalf("want refresh failed, got %v", err)
	}
	if res.Path != "" {
		t.Errorf("refresh fail must not write, path %q", res.Path)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "status: todo") {
		t.Errorf("must stay todo, got %s", data)
	}
}

func TestClaimPushFailedKeepsWrite(t *testing.T) {
	if !git.Available() {
		t.Skip("git not on PATH")
	}
	e, repo := initAutoCommitRepo(t, "wc")
	path := addTicket(t, e.dir, "wc-ab2c", "todo")

	bare := filepath.Join(t.TempDir(), "bare.git")
	testgit.Run(t, filepath.Dir(bare), "init", "--bare", "-b", "main", filepath.Base(bare))
	testgit.Run(t, repo, "add", "-A")
	testgit.Run(t, repo, "commit", "-m", "seed")
	testgit.Run(t, repo, "remote", "add", "origin", bare)
	testgit.Run(t, repo, "push", "-u", "origin", "HEAD:main")

	hooks := filepath.Join(repo, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	testgit.Run(t, repo, "config", "core.hooksPath", hooks)
	if err := os.WriteFile(filepath.Join(hooks, "pre-push"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	res, err := Claim(e.deps, nil, ClaimInput{Kind: ClaimID, Scope: "wc", Dir: e.dir, Lookup: fullLookup("wc-ab2c")})
	if !errors.Is(err, ErrPushFailed) {
		t.Fatalf("want push failed, got %v", err)
	}
	if res.Path == "" {
		t.Fatal("push fail must still return path")
	}
	if res.SyncNeeded != "push failed" {
		t.Errorf("SyncNeeded = %q, want push failed", res.SyncNeeded)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "status: in-progress") {
		t.Errorf("write must stand, got %s", data)
	}
}
