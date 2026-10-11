package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/p3bot/tk/internal/testgit"
)

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return testgit.Combined(t, dir, args...)
}

func gitCmdEnv(t *testing.T, dir string, env []string, args ...string) {
	t.Helper()
	_ = testgit.CombinedEnv(t, dir, env, args...)
}

// conflictRepo builds a repo paused on a rebase with one 3-way-conflicted file.
func conflictRepo(t *testing.T) (repo, path, mainTip, featureTip, mainBody, featureBody string) {
	t.Helper()
	repo = newRepo(t)
	path = "wc/wc-ab2c-x.md"
	abs := filepath.Join(repo, path)

	write(t, abs, "a\nCOMMON\nc\n")
	gitCmd(t, repo, "add", "-A")
	gitCmd(t, repo, "commit", "-m", "base")
	main := gitOut(t, repo, "rev-parse", "--abbrev-ref", "HEAD")

	gitCmd(t, repo, "checkout", "-b", "feature")
	featureBody = "a\nFEATURE\nc\n"
	write(t, abs, featureBody)
	gitCmd(t, repo, "commit", "-am", "feature edit")
	featureTip = gitOut(t, repo, "rev-parse", "HEAD")

	gitCmd(t, repo, "checkout", main)
	mainBody = "a\nMAIN\nc\n"
	write(t, abs, mainBody)
	gitCmd(t, repo, "commit", "-am", "main edit")
	mainTip = gitOut(t, repo, "rev-parse", "HEAD")

	gitCmd(t, repo, "checkout", "feature")
	// Rebase feature onto main: replaying feature's commit conflicts.
	_ = testgit.AllowFailure(t, repo, []string{"GIT_EDITOR=true"}, "rebase", main) // expected non-zero: paused on conflict
	if !MidRebase(context.Background(), repo) {
		t.Fatal("setup: expected a paused rebase")
	}
	return repo, path, mainTip, featureTip, mainBody, featureBody
}

func TestConflictStagesAndShowMapping(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo, path, _, _, mainBody, featureBody := conflictRepo(t)

	stages, err := ConflictStages(ctx, repo, path)
	if err != nil {
		t.Fatal(err)
	}
	if !stages.Base || !stages.Ours || !stages.Theirs {
		t.Fatalf("3-way conflict must list all stages: %+v", stages)
	}

	// Stage :2 is the rebase target (main); stage :3 is the commit being replayed.
	ours, err := ShowStage(ctx, repo, 2, path)
	if err != nil {
		t.Fatal(err)
	}
	if string(ours) != mainBody {
		t.Errorf("stage :2 = %q, want main body %q", ours, mainBody)
	}
	theirs, err := ShowStage(ctx, repo, 3, path)
	if err != nil {
		t.Fatal(err)
	}
	if string(theirs) != featureBody {
		t.Errorf("stage :3 = %q, want feature body %q", theirs, featureBody)
	}
}

func TestConflictStagesAddAdd(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := newRepo(t)
	seed := filepath.Join(repo, "seed.txt")
	write(t, seed, "seed\n")
	gitCmd(t, repo, "add", "-A")
	gitCmd(t, repo, "commit", "-m", "seed")
	main := gitOut(t, repo, "rev-parse", "--abbrev-ref", "HEAD")

	path := "wc/wc-ab2c-x.md"
	abs := filepath.Join(repo, path)
	gitCmd(t, repo, "checkout", "-b", "feature")
	write(t, abs, "feature add\n")
	gitCmd(t, repo, "add", "-A")
	gitCmd(t, repo, "commit", "-m", "feature add")

	gitCmd(t, repo, "checkout", main)
	write(t, abs, "main add\n")
	gitCmd(t, repo, "add", "-A")
	gitCmd(t, repo, "commit", "-m", "main add")

	gitCmd(t, repo, "checkout", "feature")
	_ = testgit.AllowFailure(t, repo, []string{"GIT_EDITOR=true"}, "rebase", main)

	stages, err := ConflictStages(ctx, repo, path)
	if err != nil {
		t.Fatal(err)
	}
	if stages.Base {
		t.Error("add/add must have no base stage")
	}
	if !stages.Ours || !stages.Theirs {
		t.Errorf("add/add must have both side stages: %+v", stages)
	}
}

func TestConflictStagesDeleteModify(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := newRepo(t)
	path := "wc/wc-ab2c-x.md"
	abs := filepath.Join(repo, path)
	write(t, abs, "base\n")
	gitCmd(t, repo, "add", "-A")
	gitCmd(t, repo, "commit", "-m", "base")
	main := gitOut(t, repo, "rev-parse", "--abbrev-ref", "HEAD")

	gitCmd(t, repo, "checkout", "-b", "feature")
	if err := os.Remove(abs); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, repo, "commit", "-am", "delete")

	gitCmd(t, repo, "checkout", main)
	write(t, abs, "modified\n")
	gitCmd(t, repo, "commit", "-am", "modify")

	gitCmd(t, repo, "checkout", "feature")
	_ = testgit.AllowFailure(t, repo, []string{"GIT_EDITOR=true"}, "rebase", main)

	stages, err := ConflictStages(ctx, repo, path)
	if err != nil {
		t.Fatal(err)
	}
	if !stages.Base {
		t.Error("delete/modify must keep a base stage")
	}
	// Exactly one side's stage is absent (the deleting side).
	if stages.Ours == stages.Theirs {
		t.Errorf("delete/modify must have exactly one side stage: %+v", stages)
	}
}

func TestMergeBlobsCleanAndConflict(t *testing.T) {
	requireGit(t)
	ctx := context.Background()

	base := []byte("a\nb\nc\nd\ne\nf\ng\n")
	ours := []byte("X\nb\nc\nd\ne\nf\ng\n")
	theirs := []byte("a\nb\nc\nd\ne\nf\nY\n")
	merged, conflicted, err := MergeBlobs(ctx, base, ours, theirs)
	if err != nil {
		t.Fatal(err)
	}
	if conflicted {
		t.Errorf("well-separated edits must merge clean: %q", merged)
	}
	if string(merged) != "X\nb\nc\nd\ne\nf\nY\n" {
		t.Errorf("clean merge = %q", merged)
	}

	out, conflicted, err := MergeBlobs(ctx, []byte("a\nb\nc\n"), []byte("a\nX\nc\n"), []byte("a\nZ\nc\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !conflicted {
		t.Error("overlapping edits must conflict")
	}
	if !strings.Contains(string(out), "<<<<<<<") {
		t.Errorf("conflicted merge must carry markers: %q", out)
	}
}

// Trouble-exit (non-zero, empty stdout) must surface as an error, never a conflict.
// Binary blobs make git merge-file refuse deterministically.
func TestMergeBlobsTroubleExitIsError(t *testing.T) {
	requireGit(t)
	ctx := context.Background()

	base := []byte("a\x00b\n")
	ours := []byte("a\x00X\n")
	theirs := []byte("a\x00Z\n")
	merged, conflicted, err := MergeBlobs(ctx, base, ours, theirs)
	if err == nil {
		t.Fatalf("binary blobs must fault, got merged=%q conflicted=%v", merged, conflicted)
	}
	if conflicted {
		t.Error("a trouble-exit must not be reported as a conflict")
	}
	if merged != nil {
		t.Errorf("a faulted merge must return nil bytes, got %q", merged)
	}
	if !strings.Contains(err.Error(), "git merge-file") {
		t.Errorf("error must name the failing command: %v", err)
	}
}

func TestListFiles(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := newRepo(t)
	write(t, filepath.Join(repo, "wc", "wc-ab2c-x.md"), "x\n")
	write(t, filepath.Join(repo, "wc", "wc-cd3e-y.md"), "y\n")
	write(t, filepath.Join(repo, "other", "z.md"), "z\n")
	gitCmd(t, repo, "add", "-A")
	gitCmd(t, repo, "commit", "-m", "files")

	files, err := ListFiles(ctx, repo, "wc")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("want 2 files under wc, got %v", files)
	}
}

func TestAuthorDatePerFile(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := newRepo(t)
	path := "wc/wc-ab2c-x.md"
	write(t, filepath.Join(repo, path), "v1\n")
	gitCmd(t, repo, "add", "-A")
	when := "2021-05-05T10:00:00+00:00"
	gitCmdEnv(t, repo, []string{"GIT_AUTHOR_DATE=" + when, "GIT_COMMITTER_DATE=" + when}, "commit", "-m", "v1")

	// A later, unrelated commit that does not touch path must not move path's date.
	write(t, filepath.Join(repo, "other.txt"), "u\n")
	gitCmd(t, repo, "add", "-A")
	gitCmdEnv(t, repo, []string{"GIT_AUTHOR_DATE=2099-01-01T00:00:00+00:00", "GIT_COMMITTER_DATE=2099-01-01T00:00:00+00:00"}, "commit", "-m", "unrelated")

	got, err := AuthorDate(ctx, repo, "HEAD", path)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := time.Parse(time.RFC3339, when)
	if !got.Equal(want) {
		t.Errorf("author date = %v, want %v (per-file, not branch tip)", got, want)
	}
}

func TestRebaseSidesResolvesHeadAndRebaseHead(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo, _, mainTip, featureTip, _, _ := conflictRepo(t)

	head, rebaseHead, err := RebaseSides(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if head != mainTip {
		t.Errorf("HEAD (stage :2 side) = %s, want main tip %s", head, mainTip)
	}
	if rebaseHead != featureTip {
		t.Errorf("REBASE_HEAD (stage :3 side) = %s, want feature tip %s", rebaseHead, featureTip)
	}
}

func TestRebaseAbortRestores(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo, _, _, _, _, _ := conflictRepo(t)

	if err := RebaseAbort(ctx, repo); err != nil {
		t.Fatal(err)
	}
	if MidRebase(ctx, repo) {
		t.Error("abort must leave the rebase state clean")
	}
}

func TestMergeKeepsUnrelatedDirty(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := newRepo(t)
	write(t, filepath.Join(repo, "f.txt"), "base\n")
	gitCmd(t, repo, "add", "-A")
	gitCmd(t, repo, "commit", "-m", "base")
	main := gitOut(t, repo, "rev-parse", "--abbrev-ref", "HEAD")

	gitCmd(t, repo, "checkout", "-b", "feature")
	write(t, filepath.Join(repo, "f.txt"), "feature\n")
	gitCmd(t, repo, "commit", "-am", "feature")
	gitCmd(t, repo, "checkout", main)
	write(t, filepath.Join(repo, "g.txt"), "main\n")
	gitCmd(t, repo, "add", "-A")
	gitCmd(t, repo, "commit", "-m", "main")
	gitCmd(t, repo, "checkout", "feature")
	write(t, filepath.Join(repo, "other.txt"), "dirty\n")
	gitCmd(t, repo, "add", "-A")
	gitCmd(t, repo, "commit", "-m", "other")
	write(t, filepath.Join(repo, "other.txt"), "still dirty\n")

	paused, err := Merge(ctx, repo, main)
	if err != nil || paused {
		t.Fatalf("clean merge must finish, paused=%v err=%v", paused, err)
	}
	if MidMerge(ctx, repo) || MidRebase(ctx, repo) {
		t.Fatal("a finished merge must not stay mid-integrate")
	}
	got, err := os.ReadFile(filepath.Join(repo, "other.txt"))
	if err != nil || string(got) != "still dirty\n" {
		t.Fatalf("unrelated dirty bytes = %q err=%v", got, err)
	}
	st := gitOut(t, repo, "status", "--porcelain")
	if !strings.Contains(st, "other.txt") {
		t.Fatalf("unrelated file must stay unstaged, status %q", st)
	}
	if _, err := os.Stat(filepath.Join(repo, ".git", "HEAD")); err != nil {
		t.Fatal(err)
	}
	second := gitOut(t, repo, "rev-parse", "--verify", "HEAD^2")
	if second == "" {
		t.Fatal("merge commit must have two parents")
	}
}

func TestMergeOverridesFastForwardOnly(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := newRepo(t)
	write(t, filepath.Join(repo, "base.txt"), "base\n")
	gitCmd(t, repo, "add", "-A")
	gitCmd(t, repo, "commit", "-m", "base")
	main := strings.TrimSpace(gitOut(t, repo, "rev-parse", "--abbrev-ref", "HEAD"))
	gitCmd(t, repo, "checkout", "-b", "feature")
	write(t, filepath.Join(repo, "feature.txt"), "feature\n")
	gitCmd(t, repo, "add", "-A")
	gitCmd(t, repo, "commit", "-m", "feature")
	gitCmd(t, repo, "checkout", main)
	write(t, filepath.Join(repo, "main.txt"), "main\n")
	gitCmd(t, repo, "add", "-A")
	gitCmd(t, repo, "commit", "-m", "main")
	gitCmd(t, repo, "checkout", "feature")
	gitCmd(t, repo, "config", "merge.ff", "only")
	gitCmd(t, repo, "config", "branch.feature.mergeoptions", "--ff-only")

	paused, err := Merge(ctx, repo, main)
	if err != nil || paused {
		t.Fatalf("ff-only config must not block a diverged merge, paused=%v err=%v", paused, err)
	}
	if MidMerge(ctx, repo) {
		t.Fatal("a finished merge must not leave MERGE_HEAD")
	}
	if strings.TrimSpace(gitOut(t, repo, "rev-parse", "--verify", "HEAD^2")) == "" {
		t.Fatal("merge commit must have two parents")
	}
}

func TestMergeRefusesOverlapBeforeStart(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := newRepo(t)
	path := filepath.Join(repo, "f.txt")
	write(t, path, "base\n")
	gitCmd(t, repo, "add", "-A")
	gitCmd(t, repo, "commit", "-m", "base")
	main := gitOut(t, repo, "rev-parse", "--abbrev-ref", "HEAD")
	gitCmd(t, repo, "checkout", "-b", "feature")
	write(t, path, "feature\n")
	gitCmd(t, repo, "commit", "-am", "feature")
	gitCmd(t, repo, "checkout", main)
	write(t, path, "main\n")
	gitCmd(t, repo, "commit", "-am", "main")
	gitCmd(t, repo, "checkout", "feature")
	write(t, path, "dirty\n")

	paused, err := Merge(ctx, repo, main)
	if err == nil || paused {
		t.Fatalf("overlap must refuse before start, paused=%v err=%v", paused, err)
	}
	if MidMerge(ctx, repo) {
		t.Fatal("refusal must not leave MERGE_HEAD")
	}
	got, errRead := os.ReadFile(path)
	if errRead != nil || string(got) != "dirty\n" {
		t.Fatalf("bytes = %q err=%v", got, errRead)
	}
}

func TestMergeCommitLeavesUnstagedPathsOut(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := newRepo(t)
	path := filepath.Join(repo, "f.txt")
	write(t, path, "base\n")
	gitCmd(t, repo, "add", "-A")
	gitCmd(t, repo, "commit", "-m", "base")
	main := gitOut(t, repo, "rev-parse", "--abbrev-ref", "HEAD")
	gitCmd(t, repo, "checkout", "-b", "feature")
	write(t, path, "feature\n")
	gitCmd(t, repo, "commit", "-am", "feature")
	gitCmd(t, repo, "checkout", main)
	write(t, path, "main\n")
	gitCmd(t, repo, "commit", "-am", "main")
	gitCmd(t, repo, "checkout", "feature")

	paused, err := Merge(ctx, repo, main)
	if err != nil || !paused {
		t.Fatalf("merge must pause, paused=%v err=%v", paused, err)
	}
	write(t, filepath.Join(repo, "other.txt"), "side\n")
	gitCmd(t, repo, "add", "other.txt")
	gitCmd(t, repo, "reset", "HEAD", "--", "other.txt")
	write(t, path, "resolved\n")
	gitCmd(t, repo, "add", "f.txt")
	if err := MergeCommit(ctx, repo); err != nil {
		t.Fatal(err)
	}
	if MidMerge(ctx, repo) {
		t.Fatal("commit must finish the merge")
	}
	names := gitOut(t, repo, "show", "--name-only", "--pretty=format:", "HEAD")
	if strings.Contains(names, "other.txt") {
		t.Fatalf("unstaged path must stay out of the merge commit:\n%s", names)
	}
	if !strings.Contains(gitOut(t, repo, "status", "--porcelain"), "other.txt") {
		t.Fatal("unstaged path must remain dirty")
	}
}

func TestRebasePausesOnConflict(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := newRepo(t)
	path := filepath.Join(repo, "f.txt")
	write(t, path, "a\nCOMMON\nc\n")
	gitCmd(t, repo, "add", "-A")
	gitCmd(t, repo, "commit", "-m", "base")
	main := gitOut(t, repo, "rev-parse", "--abbrev-ref", "HEAD")

	gitCmd(t, repo, "checkout", "-b", "feature")
	write(t, path, "a\nFEATURE\nc\n")
	gitCmd(t, repo, "commit", "-am", "feature")
	gitCmd(t, repo, "checkout", main)
	write(t, path, "a\nMAIN\nc\n")
	gitCmd(t, repo, "commit", "-am", "main")
	gitCmd(t, repo, "checkout", "feature")

	paused, err := Rebase(ctx, repo, main)
	if err != nil {
		t.Fatalf("rebase must pause, not error: %v", err)
	}
	if !paused {
		t.Fatal("rebase must report paused on conflict")
	}
	if err := RebaseAbort(ctx, repo); err != nil {
		t.Fatal(err)
	}
}

func TestFetchPushUnpushed(t *testing.T) {
	requireGit(t)
	ctx := context.Background()

	remote := t.TempDir()
	gitCmd(t, remote, "init", "--bare")

	repo := newRepo(t)
	write(t, filepath.Join(repo, "f.txt"), "one\n")
	gitCmd(t, repo, "add", "-A")
	gitCmd(t, repo, "commit", "-m", "one")
	gitCmd(t, repo, "remote", "add", "origin", remote)
	gitCmd(t, repo, "push", "-u", "origin", "HEAD")

	n, err := UnpushedCount(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("unpushed after push = %d, want 0", n)
	}

	write(t, filepath.Join(repo, "f.txt"), "two\n")
	gitCmd(t, repo, "commit", "-am", "two")
	if n, _ := UnpushedCount(ctx, repo); n != 1 {
		t.Errorf("unpushed after new commit = %d, want 1", n)
	}

	if err := Push(ctx, repo); err != nil {
		t.Fatal(err)
	}
	if n, _ := UnpushedCount(ctx, repo); n != 0 {
		t.Errorf("unpushed after Push = %d, want 0", n)
	}
	if err := Fetch(ctx, repo); err != nil {
		t.Fatal(err)
	}
}

func TestDirtyEntriesCarryCode(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := newRepo(t)
	tracked := "wc/wc-ab2c-x.md"
	write(t, filepath.Join(repo, tracked), "v1\n")
	gitCmd(t, repo, "add", "-A")
	gitCmd(t, repo, "commit", "-m", "base")

	write(t, filepath.Join(repo, tracked), "v2\n")
	write(t, filepath.Join(repo, "wc", "wc-cd3e-new.md"), "n\n")

	entries, err := DirtyEntries(ctx, repo, "wc")
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]string{}
	for _, e := range entries {
		codes[filepath.Base(e.Path)] = e.Code
	}
	if !strings.Contains(codes["wc-ab2c-x.md"], "M") {
		t.Errorf("modified file code = %q, want an M", codes["wc-ab2c-x.md"])
	}
	if codes["wc-cd3e-new.md"] != "??" {
		t.Errorf("untracked file code = %q, want ??", codes["wc-cd3e-new.md"])
	}
}
