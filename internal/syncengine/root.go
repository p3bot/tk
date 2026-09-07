package syncengine

import (
	"fmt"
	"strings"

	"github.com/p3bot/tk/internal/flock"
	"github.com/p3bot/tk/internal/git"
	"github.com/p3bot/tk/internal/gitstate"
	"github.com/p3bot/tk/internal/integrity"
	"github.com/p3bot/tk/internal/scopeadmin"
	"github.com/p3bot/tk/internal/scopeconfig"
	"github.com/p3bot/tk/internal/scopefile"
	"github.com/p3bot/tk/internal/token"
)

// rootOutcome: only NeedsAttention makes the run exit non-zero.
type rootOutcome int

const (
	outcomeSynced rootOutcome = iota
	outcomeNeedsAttention
)

type syncReport struct {
	label       string   // participating scope names, for human-facing lines
	snapshotN   int      // allowlisted paths committed in the snapshot
	residueN    int      // non-allowlist paths left uncommitted
	collidedIDs []string // add/add collided ids awaiting an edge_verify sweep
	pushed      bool
	unpushed    int
}

// syncRoot isolates one git-root so --all continues past a bad sibling.
func syncRoot(deps Deps, r Reporter, t Target) rootOutcome {
	ctx := deps.Ctx
	rep := &syncReport{label: participantLabel(t.Participants)}

	if !syncPreflight(deps, r, t.Root) {
		return outcomeNeedsAttention
	}

	release, err := acquireSyncLocks(deps, t)
	if err != nil {
		r.Err(fmt.Sprintf("%s: could not acquire sync locks: %v", rep.label, err))
		return outcomeNeedsAttention
	}
	defer release()

	defer func() {
		if err := drainEdgeVerify(deps, r, rep); err != nil {
			r.Err(fmt.Sprintf("%s: edge_verify query failed: %v", rep.label, err))
		}
	}()

	var res integrateResult
	snapshotted := false
	// Mid-rebase entry: skip snapshot (no commit on temporary HEAD), resume, then fall through.
	if git.MidRebase(ctx, t.Root) {
		res = resumeRebase(deps, r, t, rep)
	} else {
		if !git.HasUpstream(ctx, t.Root) {
			r.Err(token.Line(token.SyncDisabled,
				fmt.Sprintf("%s: git-root %s has no upstream — add a remote, then tk sync", rep.label, t.Root)))
			return outcomeNeedsAttention
		}
		if err := snapshot(deps, r, t, rep); err != nil {
			r.Err(fmt.Sprintf("%s: snapshot failed: %v", rep.label, err))
			return outcomeNeedsAttention
		}
		snapshotted = true
		res = fetchAndIntegrate(deps, r, t, rep)
	}

	switch res {
	case integrateCompleted:
		if !snapshotted {
			if err := snapshot(deps, r, t, rep); err != nil {
				r.Err(fmt.Sprintf("%s: snapshot failed: %v", rep.label, err))
				return outcomeNeedsAttention
			}
		}
		return finishSynced(deps, r, t, rep)
	case integratePaused:
		reportPaused(r, rep)
		return outcomeNeedsAttention
	default: // integrateError
		return outcomeNeedsAttention
	}
}

func finishSynced(deps Deps, r Reporter, t Target, rep *syncReport) rootOutcome {
	if err := syncIntegrity(deps, r, t); err != nil {
		r.Err(fmt.Sprintf("%s: integrity step failed: %v", rep.label, err))
		return outcomeNeedsAttention
	}
	if err := drainEdgeVerify(deps, r, rep); err != nil {
		r.Err(fmt.Sprintf("%s: edge_verify query failed: %v", rep.label, err))
		return outcomeNeedsAttention
	}
	switch pushIfAhead(deps, r, t, rep) {
	case pushPaused:
		reportPaused(r, rep)
		return outcomeNeedsAttention
	case pushFailed:
		return outcomeNeedsAttention
	}
	reportSuccess(r, rep)
	return outcomeSynced
}

// syncPreflight refuses the whole root on unparseable/drifted/mismatched siblings.
func syncPreflight(deps Deps, r Reporter, root string) bool {
	refuse := false
	seenTrue, seenFalse := false, false
	for _, name := range siblingScopeNames(deps, root) {
		dir := deps.Reg.Scopes[name].Dir
		if cueName, err := scopeconfig.ReadName(deps.Cue, dir); err == nil && cueName != name {
			r.Err(token.Line(token.NameDrift, fmt.Sprintf(
				"%s (%s): registry key %q but tk.cue name is %q — recover with tk scope forget %s then tk scope import; the whole git-root %s is refused",
				name, dir, name, cueName, name, root)))
			refuse = true
			continue
		}
		schema, cfgErr := deps.Rec.SchemaOrError(name, dir)
		if cfgErr != nil {
			r.Err(token.Line(token.ConfigUnparseable, fmt.Sprintf(
				"%s (%s): %s — fix tk.cue before sync can merge this git-root", name, cfgErr.Dir, cfgErr.Reason)))
			refuse = true
			continue
		}
		if schema.AutoCommit {
			seenTrue = true
		} else {
			seenFalse = true
		}
	}
	if seenTrue && seenFalse {
		r.Err(token.Line(token.AutoCommitMismatch, fmt.Sprintf(
			"scopes sharing git-root %s disagree on autoCommit — split the divergent scope into its own repo", root)))
		refuse = true
	}
	return !refuse
}

func siblingScopeNames(deps Deps, root string) []string {
	peers := scopeadmin.GitRootScopes(deps.Reg, root)
	out := make([]string, len(peers))
	for i, p := range peers {
		out[i] = p.Name
	}
	return out
}

// acquireSyncLocks: scope locks first (name order), then git-root — reverse would deadlock write verbs.
func acquireSyncLocks(deps Deps, t Target) (func(), error) {
	var locks []*flock.Lock
	release := func() {
		for i := len(locks) - 1; i >= 0; i-- {
			_ = locks[i].Release()
		}
	}
	for _, p := range t.Participants {
		l, err := scopefile.AcquireLock(p.Dir)
		if err != nil {
			release()
			return nil, err
		}
		locks = append(locks, l)
	}
	gl, err := gitstate.AcquireCommitLock(deps.StateDir, t.Root)
	if err != nil {
		release()
		return nil, err
	}
	locks = append(locks, gl)
	return release, nil
}

// drainEdgeVerify: report-and-clear so deferred backstop and step-3 are mutually no-op.
func drainEdgeVerify(deps Deps, r Reporter, rep *syncReport) error {
	if len(rep.collidedIDs) == 0 {
		return nil
	}
	ids := rep.collidedIDs
	rep.collidedIDs = nil
	return integrity.ReportEdgeVerify(integrityDeps(deps), r, ids)
}

func reportPaused(r Reporter, rep *syncReport) {
	r.Err(fmt.Sprintf("%s: rebase paused for a human — resolve the file(s) above in place, then run tk sync again", rep.label))
}

func reportSuccess(r Reporter, rep *syncReport) {
	var parts []string
	if rep.snapshotN > 0 {
		parts = append(parts, fmt.Sprintf("snapshot %d path(s)", rep.snapshotN))
	}
	// Test unpushed before pushed so a non-zero re-count is not flattened to "pushed".
	switch {
	case rep.unpushed > 0:
		parts = append(parts, fmt.Sprintf("%d commit(s) still unpushed", rep.unpushed))
	case rep.pushed:
		parts = append(parts, "pushed")
	default:
		parts = append(parts, "up to date")
	}
	if rep.residueN > 0 {
		parts = append(parts, fmt.Sprintf("%d non-allowlist path(s) left", rep.residueN))
	}
	r.Err(fmt.Sprintf("tk sync %s: %s", rep.label, strings.Join(parts, ", ")))
}

func participantLabel(parts []Participant) string {
	names := make([]string, len(parts))
	for i, p := range parts {
		names[i] = p.Name
	}
	return strings.Join(names, ", ")
}
