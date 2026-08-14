package syncengine

import (
	"errors"
	"fmt"

	"github.com/p3bot/tk/internal/git"
)

// ErrRootFailed is returned when RefreshRoot or PushRootIfAhead cannot complete.
// Diagnostics have already been written to the Reporter.
var ErrRootFailed = errors.New("syncengine: git-root operation did not complete")

// RootTarget builds the sync Target for a git-root's auto-commit participants.
func RootTarget(deps Deps, root string) Target {
	return Target{Root: root, Participants: autoCommitParticipants(deps, root)}
}

// RefreshRoot snapshots dirty allowlisted files, fetches, integrates, and runs
// integrity for one git-root. It does not resume a mid-rebase and does not push.
// Caller must not hold a write-path flock or the git-root commit lock.
func RefreshRoot(deps Deps, r Reporter, t Target) error {
	return withSyncLocks(deps, r, t, func(rep *syncReport) error {
		defer func() {
			if err := drainEdgeVerify(deps, r, rep); err != nil {
				r.Err(fmt.Sprintf("%s: edge_verify query failed: %v", rep.label, err))
			}
		}()
		if git.MidRebase(deps.Ctx, t.Root) {
			r.Err(fmt.Sprintf("%s: git-root is mid-rebase; refresh will not resume it", rep.label))
			return ErrRootFailed
		}
		if !git.HasUpstream(deps.Ctx, t.Root) {
			r.Err(fmt.Sprintf("%s: git-root %s has no upstream", rep.label, t.Root))
			return ErrRootFailed
		}
		if err := snapshot(deps, r, t, rep); err != nil {
			r.Err(fmt.Sprintf("%s: snapshot failed: %v", rep.label, err))
			return ErrRootFailed
		}
		switch fetchAndIntegrate(deps, r, t, rep) {
		case integrateCompleted:
		case integratePaused:
			reportPaused(r, rep)
			return ErrRootFailed
		default:
			return ErrRootFailed
		}
		if err := syncIntegrity(deps, r, t); err != nil {
			r.Err(fmt.Sprintf("%s: integrity step failed: %v", rep.label, err))
			return ErrRootFailed
		}
		if err := drainEdgeVerify(deps, r, rep); err != nil {
			r.Err(fmt.Sprintf("%s: edge_verify query failed: %v", rep.label, err))
			return ErrRootFailed
		}
		return nil
	})
}

// PushRootIfAhead pushes when HEAD is ahead of upstream. It does not re-run
// merge preflight. Caller must not hold a write-path flock or the git-root
// commit lock. Any failure records last-push-error.
func PushRootIfAhead(deps Deps, r Reporter, t Target) error {
	rep := &syncReport{label: participantLabel(t.Participants)}
	release, err := acquireSyncLocks(deps, t)
	if err != nil {
		r.Err(fmt.Sprintf("%s: could not acquire sync locks: %v", rep.label, err))
		recordPushFailure(deps, r, t, rep, err, "retry tk sync")
		return ErrRootFailed
	}
	defer release()
	switch pushIfAhead(deps, r, t, rep) {
	case pushPaused:
		reportPaused(r, rep)
		return ErrRootFailed
	case pushFailed:
		return ErrRootFailed
	}
	return nil
}

func withSyncLocks(deps Deps, r Reporter, t Target, fn func(*syncReport) error) error {
	if !syncPreflight(deps, r, t.Root) {
		return ErrRootFailed
	}
	release, err := acquireSyncLocks(deps, t)
	if err != nil {
		r.Err(fmt.Sprintf("%s: could not acquire sync locks: %v", participantLabel(t.Participants), err))
		return err
	}
	defer release()
	rep := &syncReport{label: participantLabel(t.Participants)}
	return fn(rep)
}
