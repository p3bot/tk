package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Stages reports which of a conflicted path's three merge stages exist in the index.
// A missing stage is normal (add/add has no :1; delete/modify lacks the deleting side);
// enumerating before reading keeps genuine git-show failures from looking like deletions.
type Stages struct {
	Base   bool
	Ours   bool
	Theirs bool
}

// Any reports whether any conflict stage is present.
func (s Stages) Any() bool { return s.Base || s.Ours || s.Theirs }

// ConflictStages enumerates which merge stages exist for path via git ls-files -u.
// Empty result is not an error — path is not considered conflicted.
func ConflictStages(ctx context.Context, gitRoot, path string) (Stages, error) {
	out, err := run(ctx, gitRoot, "ls-files", "-u", "--", path)
	if err != nil {
		return Stages{}, err
	}
	var s Stages
	for _, line := range nonEmptyLines(string(out)) {
		// "<mode> <sha> <stage>\t<path>"; stage number is the field before the tab.
		meta := line
		if tab := strings.IndexByte(line, '\t'); tab >= 0 {
			meta = line[:tab]
		}
		fields := strings.Fields(meta)
		if len(fields) < 3 {
			continue
		}
		switch fields[len(fields)-1] {
		case "1":
			s.Base = true
		case "2":
			s.Ours = true
		case "3":
			s.Theirs = true
		}
	}
	return s, nil
}

// ShowStage returns the raw blob of one conflict stage via git show :<stage>:<path>.
// Callers must enumerate with ConflictStages first; a non-zero exit is a genuine fault.
func ShowStage(ctx context.Context, gitRoot string, stage int, path string) ([]byte, error) {
	if stage < 1 || stage > 3 {
		return nil, fmt.Errorf("git show stage: stage %d out of range", stage)
	}
	return run(ctx, gitRoot, "show", fmt.Sprintf(":%d:%s", stage, path))
}

// MergeBlobs 3-way text-merges three in-memory blobs with git merge-file.
// Returns the merged bytes and whether conflict markers were left.
func MergeBlobs(ctx context.Context, base, ours, theirs []byte) (merged []byte, conflicted bool, err error) {
	dir, err := os.MkdirTemp("", "tk-merge-*")
	if err != nil {
		return nil, false, fmt.Errorf("merge-file temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	oursPath := filepath.Join(dir, "ours")
	basePath := filepath.Join(dir, "base")
	theirsPath := filepath.Join(dir, "theirs")
	for _, f := range []struct {
		path string
		data []byte
	}{{oursPath, ours}, {basePath, base}, {theirsPath, theirs}} {
		if err := os.WriteFile(f.path, f.data, 0o644); err != nil {
			return nil, false, fmt.Errorf("merge-file write: %w", err)
		}
	}

	// -p writes to stdout; exit is conflict-hunk count (0 clean, >0 conflicted).
	// Trouble-exit or signal (-1) is operational fault, not conflict.
	cmd := exec.CommandContext(ctx, "git", "merge-file", "-p",
		"-L", "ours", "-L", "base", "-L", "theirs",
		oursPath, basePath, theirsPath)
	out, runErr := cmd.Output()
	if runErr == nil {
		return out, false, nil
	}
	var exit *exec.ExitError
	if errors.As(runErr, &exit) {
		if exit.ExitCode() > 0 && len(out) > 0 {
			return out, true, nil
		}
		// cmd.Output() populates exit.Stderr; surface git's diagnostic on trouble-exit.
		if msg := strings.TrimSpace(string(exit.Stderr)); msg != "" {
			return nil, false, fmt.Errorf("git merge-file: %s", msg)
		}
	}
	return nil, false, fmt.Errorf("git merge-file: %w", runErr)
}

// ListFiles lists index-tracked paths under dir via git ls-files, repo-relative.
func ListFiles(ctx context.Context, gitRoot, dir string) ([]string, error) {
	out, err := run(ctx, gitRoot, "ls-files", "--", dir)
	if err != nil {
		return nil, err
	}
	return nonEmptyLines(string(out)), nil
}

// ListTree lists repo-relative paths in rev under rel.
// An empty rel, or ".", lists the whole tree. A path absent from rev is an empty list.
func ListTree(ctx context.Context, gitRoot, rev, rel string) ([]string, error) {
	args := []string{"ls-tree", "-r", "--name-only", rev}
	rel = filepath.ToSlash(filepath.Clean(rel))
	if rel != "" && rel != "." {
		args = append(args, "--", rel)
	}
	out, err := run(ctx, gitRoot, args...)
	if err != nil {
		return nil, err
	}
	return nonEmptyLines(string(out)), nil
}

// ShowBlob returns the raw blob of path in rev. path is repo-relative.
func ShowBlob(ctx context.Context, gitRoot, rev, path string) ([]byte, error) {
	return run(ctx, gitRoot, "show", rev+":"+filepath.ToSlash(path))
}

// TreeContains reports whether path is present in rev.
// A missing path is false. A bad rev is an error.
func TreeContains(ctx context.Context, gitRoot, rev, path string) (bool, error) {
	out, err := run(ctx, gitRoot, "ls-tree", "--name-only", rev, "--", filepath.ToSlash(path))
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) != "", nil
}

// PathChanged reports whether path's committed tree entry differs between revA and revB.
// A missing path on one side is a change. Exit 1 is a difference, not an operational fault.
func PathChanged(ctx context.Context, gitRoot, revA, revB, path string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "diff", "--quiet", revA, revB, "--", filepath.ToSlash(path))
	cmd.Dir = gitRoot
	out, err := cmd.CombinedOutput()
	if err == nil {
		return false, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return true, nil
	}
	msg := strings.TrimSpace(string(out))
	if msg == "" {
		msg = err.Error()
	}
	return false, fmt.Errorf("git diff --quiet: %s", msg)
}

// FirstParent returns the first parent of rev.
// ok is false when rev is a root commit.
func FirstParent(ctx context.Context, gitRoot, rev string) (string, bool, error) {
	out, err := run(ctx, gitRoot, "log", "-1", "--format=%P", rev)
	if err != nil {
		return "", false, err
	}
	parents := strings.Fields(strings.TrimSpace(string(out)))
	if len(parents) == 0 {
		return "", false, nil
	}
	return parents[0], true, nil
}

// AuthorDate returns the author date of the last commit on rev that touched path.
// Per-file, never branch-tip, so an unrelated later commit cannot decide another
// ticket's fields. Empty when no commit touched path (zero time).
func AuthorDate(ctx context.Context, gitRoot, rev, path string) (time.Time, error) {
	out, err := run(ctx, gitRoot, "log", "-1", "--format=%aI", rev, "--", path)
	if err != nil {
		return time.Time{}, err
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse author date %q: %w", s, err)
	}
	return t, nil
}

// RebaseSides resolves the two side revs of a paused rebase: HEAD (stage :2) and
// REBASE_HEAD (stage :3). Mapping is inverted from everyday "ours"/"theirs" during rebase.
func RebaseSides(ctx context.Context, gitRoot string) (head, rebaseHead string, err error) {
	h, err := run(ctx, gitRoot, "rev-parse", "HEAD")
	if err != nil {
		return "", "", err
	}
	rh, err := run(ctx, gitRoot, "rev-parse", "REBASE_HEAD")
	if err != nil {
		return "", "", err
	}
	return strings.TrimSpace(string(h)), strings.TrimSpace(string(rh)), nil
}

// UpstreamRelation is how HEAD sits against the fetched upstream.
type UpstreamRelation int

const (
	// RelBased means @{u} is an ancestor of HEAD, including equal.
	RelBased UpstreamRelation = iota
	// RelBehind means HEAD is a strict ancestor of @{u}.
	RelBehind
	// RelDiverged means both sides have commits the other lacks.
	RelDiverged
)

// IsAncestor reports whether ancestor is an ancestor of descendant.
// Equal revisions count as ancestors. Exit 1 is false, not an operational fault.
func IsAncestor(ctx context.Context, gitRoot, ancestor, descendant string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "merge-base", "--is-ancestor", ancestor, descendant)
	cmd.Dir = gitRoot
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("git merge-base --is-ancestor: %w", err)
}

// CompareUpstream classifies HEAD against @{u}. Fetch first so @{u} is current.
func CompareUpstream(ctx context.Context, gitRoot string) (UpstreamRelation, error) {
	based, err := IsAncestor(ctx, gitRoot, "@{u}", "HEAD")
	if err != nil {
		return 0, err
	}
	if based {
		return RelBased, nil
	}
	behind, err := IsAncestor(ctx, gitRoot, "HEAD", "@{u}")
	if err != nil {
		return 0, err
	}
	if behind {
		return RelBehind, nil
	}
	return RelDiverged, nil
}

// MergeFFOnly fast-forwards HEAD to upstream. It refuses when the update is not
// a fast-forward, or when local changes would be overwritten, and then leaves HEAD unmoved.
func MergeFFOnly(ctx context.Context, gitRoot, upstream string) error {
	return runEnv(ctx, gitRoot, nil, "merge", "--ff-only", upstream)
}

// Fetch updates remote-tracking refs (git fetch).
func Fetch(ctx context.Context, gitRoot string) error {
	_, err := run(ctx, gitRoot, "fetch")
	return err
}

// Rebase starts git rebase <upstream>. paused is true when the rebase stopped on a conflict.
func Rebase(ctx context.Context, gitRoot, upstream string) (paused bool, err error) {
	return rebaseStep(ctx, gitRoot, "rebase", upstream)
}

// RebaseContinue runs git rebase --continue. paused is true on a later conflict.
// Editor is disabled so the commit message is reused non-interactively.
func RebaseContinue(ctx context.Context, gitRoot string) (paused bool, err error) {
	return rebaseStep(ctx, gitRoot, "rebase", "--continue")
}

// RebaseAbort runs git rebase --abort.
func RebaseAbort(ctx context.Context, gitRoot string) error {
	return runEnv(ctx, gitRoot, editorEnv(), "rebase", "--abort")
}

// rebaseStep runs a rebase-family command with editor disabled; mid-rebase with unmerged files is a pause.
func rebaseStep(ctx context.Context, gitRoot string, args ...string) (bool, error) {
	err := runEnv(ctx, gitRoot, editorEnv(), args...)
	if err == nil {
		return false, nil
	}
	if MidRebase(ctx, gitRoot) && len(UnmergedFiles(ctx, gitRoot)) > 0 {
		return true, nil
	}
	return false, err
}

// Push runs git push to the current branch's upstream.
func Push(ctx context.Context, gitRoot string) error {
	_, err := run(ctx, gitRoot, "push")
	return err
}

// UnpushedCount returns how many commits the current branch is ahead of its upstream.
func UnpushedCount(ctx context.Context, gitRoot string) (int, error) {
	out, err := run(ctx, gitRoot, "rev-list", "--count", "@{u}..HEAD")
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, fmt.Errorf("parse unpushed count %q: %w", strings.TrimSpace(string(out)), err)
	}
	return n, nil
}

// DirtyEntry is one dirty path under a scope dir plus its two-character porcelain status code.
type DirtyEntry struct {
	Path string
	Code string
}

// DirtyEntries returns dirty paths under dir with porcelain status codes.
// Rename/copy entries contribute their destination path.
func DirtyEntries(ctx context.Context, gitRoot, dir string) ([]DirtyEntry, error) {
	out, err := run(ctx, gitRoot, "status", "--porcelain", "-z", "-uall", "--", dir)
	if err != nil {
		return nil, err
	}
	fields := strings.Split(string(out), "\x00")
	var entries []DirtyEntry
	for i := 0; i < len(fields); i++ {
		entry := fields[i]
		if len(entry) < 4 {
			continue
		}
		code := entry[:2]
		path := entry[3:]
		if strings.ContainsAny(code, "RC") {
			i++ // consume rename/copy source path in the next NUL field
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(gitRoot, path)
		}
		entries = append(entries, DirtyEntry{Path: path, Code: code})
	}
	return entries, nil
}

// editorEnv disables interactive editors so rebase never blocks non-interactively.
func editorEnv() []string {
	return []string{"GIT_EDITOR=true", "GIT_SEQUENCE_EDITOR=true"}
}

// runEnv runs git with extraEnv, discarding stdout.
func runEnv(ctx context.Context, gitRoot string, extraEnv []string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = gitRoot
	cmd.Env = append(os.Environ(), extraEnv...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("git %s: %s", args[0], msg)
	}
	return nil
}
