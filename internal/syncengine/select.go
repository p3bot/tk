// Package syncengine is tk's push machinery: selection policy (auto-commit-only
// filter, unreachable/disabled/config-error reporting, participants grouped by
// git-root) and the per-root flow (preflight, lock order, snapshot, fetch/integrate,
// mid-rebase resume, sync-time integrity via integrity.RunBatches, push-if-ahead).
// RefreshRoot and PushRootIfAhead are acquiring wrappers for the claim workflow
// (refresh does not resume a mid-rebase and does not push).
// Cobra-free; the composition root supplies ambient or all-registered inputs.
package syncengine

import (
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/p3bot/tk/internal/gitroot"
	"github.com/p3bot/tk/internal/scopefile"
	"github.com/p3bot/tk/internal/token"
)

// Participant is one auto-commit scope under a git-root.
type Participant struct {
	Name string
	Dir  string
}

// Target is one git-root with its auto-commit participants.
type Target struct {
	Root         string
	Participants []Participant
}

// Selection is the structured outcome of auto-commit git-root target selection.
type Selection struct {
	Targets     []Target
	Disabled    []string
	ConfigErrs  []string
	Unreachable []string
	Candidates  int
}

// Input is either one ambient scope (already resolved at the composition root)
// or all-registered mode. The package does not read env, cwd, or Cobra.
type Input struct {
	// AllRegistered: every registered scope (from --all or ambient ErrNoScope).
	AllRegistered bool
	// Ambient is set when AllRegistered is false.
	Ambient *AmbientScope
}

// AmbientScope is one successfully resolved ambient scope.
type AmbientScope struct {
	Name string
	Dir  string
}

// Select applies auto-commit git-root selection policy and returns structured results.
func Select(deps Deps, in Input) (Selection, error) {
	if in.AllRegistered {
		return allSelection(deps), nil
	}
	if in.Ambient == nil {
		return Selection{}, errors.New("syncengine: Input requires Ambient or AllRegistered")
	}
	return ambientSelection(deps, in.Ambient.Name, in.Ambient.Dir)
}

func ambientSelection(deps Deps, scope, dir string) (Selection, error) {
	root, hasRoot := scopefile.GitRoot(dir)

	res, err := deps.Rec.Reconcile(map[string]string{scope: dir}, registeredSet(deps.Reg), nowNS())
	if err != nil {
		return Selection{}, err
	}
	if res.Unreachable[scope] {
		return Selection{}, fmt.Errorf("%s", token.Line(token.UnreachableScope,
			fmt.Sprintf("%s: dir %s is not reachable — cannot sync", scope, dir)))
	}
	cfgErr, badConfig := res.ConfigErrs[scope]
	switch {
	case badConfig && !hasRoot:
		return Selection{}, fmt.Errorf("%s", token.Line(token.ConfigUnparseable, fmt.Sprintf(
			"%s (%s): %s — fix tk.cue before sync can evaluate this scope", scope, cfgErr.Dir, cfgErr.Reason)))
	case badConfig:
	case !schemaAutoCommit(res.Schema(scope)):
		return Selection{}, nonAutoCommitRefusal(scope, hasRoot)
	}
	if !hasRoot {
		return Selection{Candidates: 1, Disabled: []string{syncDisabledLine(scope, dir)}}, nil
	}
	parts := autoCommitParticipants(deps, root)
	return Selection{Candidates: 1, Targets: []Target{{Root: root, Participants: parts}}}, nil
}

func allSelection(deps Deps) Selection {
	var sel Selection
	byRoot := map[string][]Participant{}
	type badConfig struct {
		scope, dir, reason, root string
		hasRoot                  bool
	}
	var badConfigs []badConfig
	for _, scope := range sortedRegistered(deps.Reg) {
		dir := deps.Reg.Scopes[scope].Dir
		if _, err := os.Stat(dir); err != nil {
			sel.Unreachable = append(sel.Unreachable, token.Line(token.UnreachableScope,
				fmt.Sprintf("%s: dir %s is not reachable — skipped", scope, dir)))
			continue
		}
		schema, cfgErr := deps.Rec.SchemaOrError(scope, dir)
		if cfgErr != nil {
			root, hasRoot := scopefile.GitRoot(dir)
			badConfigs = append(badConfigs, badConfig{scope: scope, dir: cfgErr.Dir, reason: cfgErr.Reason, root: root, hasRoot: hasRoot})
			continue
		}
		if schema == nil || !schema.AutoCommit {
			continue // non-auto-commit: not this command's business
		}
		sel.Candidates++
		root, hasRoot := scopefile.GitRoot(dir)
		if !hasRoot {
			sel.Disabled = append(sel.Disabled, syncDisabledLine(scope, dir))
			continue
		}
		byRoot[root] = append(byRoot[root], Participant{Name: scope, Dir: dir})
	}
	roots := make([]string, 0, len(byRoot))
	for root := range byRoot {
		roots = append(roots, root)
	}
	sort.Strings(roots)
	for _, root := range roots {
		parts := byRoot[root]
		sort.Slice(parts, func(i, j int) bool { return parts[i].Name < parts[j].Name })
		sel.Targets = append(sel.Targets, Target{Root: root, Participants: parts})
	}
	for _, bc := range badConfigs {
		if bc.hasRoot {
			if _, covered := byRoot[bc.root]; covered {
				continue // the per-root preflight already refuses this root by name
			}
		}
		sel.Candidates++
		sel.ConfigErrs = append(sel.ConfigErrs, token.Line(token.ConfigUnparseable,
			fmt.Sprintf("%s (%s): %s — fix tk.cue before sync can evaluate this scope", bc.scope, bc.dir, bc.reason)))
	}
	return sel
}

func autoCommitParticipants(deps Deps, root string) []Participant {
	var parts []Participant
	for _, scope := range sortedRegistered(deps.Reg) {
		dir := deps.Reg.Scopes[scope].Dir
		sgr, ok := gitroot.RepoRoot(dir)
		if !ok || sgr != root {
			continue
		}
		schema, cfgErr := deps.Rec.SchemaOrError(scope, dir)
		if cfgErr != nil || schema == nil || !schema.AutoCommit {
			continue
		}
		parts = append(parts, Participant{Name: scope, Dir: dir})
	}
	return parts
}

func nonAutoCommitRefusal(scope string, hasRoot bool) error {
	if hasRoot {
		return fmt.Errorf("sync is for auto-commit scopes only — %s is repo-driven; commit its ticket files with the host repo", scope)
	}
	return fmt.Errorf("sync is for auto-commit scopes only — %s is plain-files; there is no tk sync — run tk doctor if integrity warnings appear", scope)
}

func syncDisabledLine(scope, dir string) string {
	return token.Line(token.SyncDisabled,
		fmt.Sprintf("%s: no git repository with a remote for %s — set one up, then tk sync", scope, dir))
}
