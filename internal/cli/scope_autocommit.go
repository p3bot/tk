package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/flock"
	"github.com/p3bot/tk/internal/git"
	"github.com/p3bot/tk/internal/gitroot"
	"github.com/p3bot/tk/internal/registry"
	"github.com/p3bot/tk/internal/scopeadmin"
	"github.com/p3bot/tk/internal/scopeconfig"
	"github.com/p3bot/tk/internal/scopefile"
	"github.com/p3bot/tk/internal/selfcommit"
	"github.com/p3bot/tk/internal/token"
)

func newScopeAutoCommitCmd(app *App) *cobra.Command {
	var scope string
	cmd := &cobra.Command{
		Use:   "auto-commit [true|false] [--scope S]",
		Short: "Read or set the stored autoCommit bool (git-root-wide on set)",
		Long: "Read or rewrite autoCommit in tk.cue. Bare invocation prints the\n" +
			"evaluated bool of the ambient scope as true or false. true or false\n" +
			"rewrites every registered scope that shares that dir's derived git-root\n" +
			"(or that one scope when there is no git-root). Mode labels are derived,\n" +
			"not stored — this verb does not accept tk-driven / repo-driven /\n" +
			"plain-files. Already-that-value is ensure (exit 0, no rewrite). A false\n" +
			"set snapshots leftover allowlisted dirty paths into that last tk-owned\n" +
			"commit. Target scope uses the ambient chain shared with board verbs:\n" +
			"--scope > TK_SCOPE > cwd code-root (not a positional scope name).",
		Args: rangeArgs(0, 1),
		RunE: func(c *cobra.Command, args []string) error {
			if len(args) == 0 {
				return runScopeAutoCommitRead(app, c, scope)
			}
			want, err := parseAutoCommitArg(args[0])
			if err != nil {
				return err
			}
			return runScopeAutoCommitSet(app, c, want, scope)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "scope to read or set (defaults to ambient)")
	return cmd
}

func parseAutoCommitArg(s string) (bool, error) {
	switch s {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, usageErrorf("%q is not true or false", s)
	}
}

func runScopeAutoCommitRead(app *App, c *cobra.Command, scopeFlag string) error {
	e, err := app.openEngine(c)
	if err != nil {
		return err
	}
	defer e.close()

	resolved, err := e.resolveAmbient(scopeFlag)
	if err != nil {
		return err
	}
	schema, err := loadScopeSchema(app, resolved.Name, resolved.Entry.Dir)
	if err != nil {
		return err
	}
	stdoutln(c, strconv.FormatBool(schema.AutoCommit))
	return nil
}

func runScopeAutoCommitSet(app *App, c *cobra.Command, want bool, scopeFlag string) error {
	e, err := app.openEngine(c)
	if err != nil {
		return err
	}
	defer e.close()

	resolved, err := e.resolveAmbient(scopeFlag)
	if err != nil {
		return err
	}
	target := resolved.Name
	dir := resolved.Entry.Dir

	peers, gitRoot, hasRoot, err := collectAutoCommitPeers(e.reg, target, dir)
	if err != nil {
		return err
	}

	locks, err := acquireAutoCommitLocks(peers)
	if err != nil {
		return err
	}
	defer releaseLocks(locks)

	already := true
	for _, p := range peers {
		schema, err := loadScopeSchema(app, p.Name, p.Dir)
		if err != nil {
			return err
		}
		if schema.AutoCommit != want {
			already = false
		}
	}
	if already {
		stderrln(c, fmt.Sprintf("autoCommit is already %s", strconv.FormatBool(want)))
		return nil
	}

	if err := checkGitRootMidRebase(c.Context(), target, gitRoot, hasRoot); err != nil {
		return err
	}

	prevs := make(map[string][]byte, len(peers))
	paths := make([]string, 0, len(peers))
	for _, p := range peers {
		cuePath := filepath.Join(p.Dir, "tk.cue")
		prev, err := os.ReadFile(cuePath)
		if err != nil {
			return err
		}
		prevs[cuePath] = prev
		paths = append(paths, cuePath)
	}

	for _, p := range peers {
		if err := scopeconfig.RewriteAutoCommit(p.Dir, want); err != nil {
			return restoreCueFiles(prevs, err)
		}
	}
	for _, p := range peers {
		schema, err := loadScopeSchema(app, p.Name, p.Dir)
		if err != nil {
			return restoreCueFiles(prevs, err)
		}
		if schema.AutoCommit != want {
			return restoreCueFiles(prevs, usageErrorf(
				"autoCommit was not set (a sibling package file still pins autoCommit); edit that file or move the declaration into tk.cue"))
		}
	}

	sort.Strings(paths)
	commitPaths := paths
	if !want && hasRoot {
		extra, err := falseFlipSnapshotPaths(c.Context(), c, gitRoot, peers)
		if err != nil {
			return restoreCueFiles(prevs, err)
		}
		if len(extra) > 0 {
			commitPaths = append(append([]string(nil), paths...), extra...)
		}
	}
	if err := e.autoCommitDurability(c, target, dir, want, gitRoot, hasRoot, commitPaths); err != nil {
		return restoreCueFiles(prevs, err)
	}
	for _, p := range paths {
		out, err := absPath(p)
		if err != nil {
			return err
		}
		stdoutln(c, out)
	}
	return nil
}

// falseFlipSnapshotPaths is the last tk-owned snapshot: allowlisted dirty paths
// under every peer (tickets, notes, .gitignore) ride the false-flip commit.
// tk.cue is omitted here because the rewrite set already names it. Non-allowlist
// residue is warned, not committed — same closed allowlist as tk sync.
func falseFlipSnapshotPaths(ctx context.Context, c *cobra.Command, gitRoot string, peers []scopeadmin.GitRootScope) ([]string, error) {
	var extra []string
	for _, p := range peers {
		entries, err := git.DirtyEntries(ctx, gitRoot, p.Dir)
		if err != nil {
			return nil, err
		}
		var residue []string
		for _, ent := range entries {
			if filepath.Base(ent.Path) == scopefile.LockName {
				continue
			}
			if scopefile.IsAllowlisted(ent.Path, p.Dir) {
				if filepath.Base(ent.Path) == "tk.cue" {
					continue
				}
				extra = append(extra, ent.Path)
			} else {
				residue = append(residue, ent.Path)
			}
		}
		if len(residue) > 0 {
			stderrln(c, token.Line(token.NonAllowlist, fmt.Sprintf(
				"%d path(s) under %s not committed — move or remove; see tk doctor", len(residue), p.Dir)))
			for _, path := range residue {
				stderrln(c, "  "+path)
			}
		}
	}
	return extra, nil
}

func collectAutoCommitPeers(reg *registry.Registry, target, dir string) ([]scopeadmin.GitRootScope, string, bool, error) {
	gitRoot, hasRoot := gitroot.RepoRoot(dir)
	if !hasRoot {
		return []scopeadmin.GitRootScope{{Name: target, Dir: dir}}, "", false, nil
	}
	peers, err := scopeadmin.GitRootScopesRefuseUnreachable(reg, gitRoot)
	if err != nil {
		return nil, "", false, err
	}
	return peers, gitRoot, true, nil
}

func acquireAutoCommitLocks(peers []scopeadmin.GitRootScope) ([]*flock.Lock, error) {
	var locks []*flock.Lock
	for _, p := range peers {
		l, err := scopefile.AcquireLock(p.Dir)
		if err != nil {
			releaseLocks(locks)
			return nil, err
		}
		locks = append(locks, l)
	}
	return locks, nil
}

func releaseLocks(locks []*flock.Lock) {
	for i := len(locks) - 1; i >= 0; i-- {
		_ = locks[i].Release()
	}
}

func restoreCueFiles(prevs map[string][]byte, err error) error {
	var restoreErr error
	for path, prev := range prevs {
		if rerr := restoreCueFile(path, prev); rerr != nil {
			restoreErr = rerr
		}
	}
	if restoreErr != nil {
		return fmt.Errorf("%w (also failed to restore tk.cue: %w)", err, restoreErr)
	}
	return err
}

func (e *engine) autoCommitDurability(c *cobra.Command, scope, dir string, want bool, root string, hasRoot bool, paths []string) error {
	if !hasRoot {
		if want {
			stderrln(c, token.Line(token.SyncDisabled,
				fmt.Sprintf("%s: no git repository — files written but not committed", scope)))
		}
		return nil
	}
	if err := selfcommit.CommitPaths(c.Context(), selfcommit.BatchRequest{
		StateDir: e.app.StateDir, GitRoot: root,
		Message: fmt.Sprintf("tk: scope auto-commit %s", strconv.FormatBool(want)),
		Paths:   paths,
	}); err != nil {
		return err
	}
	if want {
		e.tkDrivenSyncNeeded(c.Context(), c, dir, root)
		return nil
	}
	if n, err := git.UnpushedCount(c.Context(), root); err == nil && n > 0 {
		stderrln(c, "autoCommit is now false; the last tk-owned commit is local — host git push is required")
	}
	return nil
}
