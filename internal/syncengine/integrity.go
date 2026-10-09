package syncengine

import (
	"fmt"

	"github.com/p3bot/tk/internal/git"
	"github.com/p3bot/tk/internal/gitstate"
	"github.com/p3bot/tk/internal/integrity"
	"github.com/p3bot/tk/internal/token"
)

// syncIntegrity: only reconcile tk sync runs; uses locks-held repair core over merged tree.
func syncIntegrity(deps Deps, r Reporter, t Target) error {
	targets := make(map[string]string, len(t.Participants))
	for _, p := range t.Participants {
		targets[p.Name] = p.Dir
	}
	res, err := deps.Rec.Reconcile(targets, registeredSet(deps.Reg), nowNS())
	if err != nil {
		return err
	}
	ideps := integrityDeps(deps)
	for _, p := range t.Participants {
		schema := res.Schema(p.Name)
		if schema == nil {
			continue // an unusable config the preflight would already have refused
		}
		target := &integrity.Target{
			Scope:      p.Name,
			Dir:        p.Dir,
			Schema:     schema,
			AutoCommit: schemaAutoCommit(schema),
			Root:       t.Root,
			HasRoot:    true,
		}
		if err := integrity.RunBatches(ideps, r, target, integrity.Flags{Repair: true}); err != nil {
			return err
		}
	}
	return nil
}

type pushResult int

const (
	pushOK pushResult = iota
	pushFailed
	pushPaused
)

// pushIfAhead: fetch→push race re-integrates once (step 2 only, not integrity).
func pushIfAhead(deps Deps, r Reporter, t Target, rep *syncReport) pushResult {
	ctx := deps.Ctx
	ahead, err := git.UnpushedCount(ctx, t.Root)
	if err != nil {
		r.Err(fmt.Sprintf("%s: could not count unpushed commits: %v", rep.label, err))
		return pushFailed
	}
	if ahead == 0 {
		rep.unpushed = 0
		return pushOK // already pulled in step 2; nothing to push
	}

	if err := git.Push(ctx, t.Root); err != nil {
		r.Err(fmt.Sprintf("%s: push rejected — re-integrating and retrying (%v)", rep.label, err))
		switch fetchAndIntegrate(deps, r, t, rep, false) {
		case integratePaused:
			recordPushFailure(deps, r, t, rep, err, "resolve the conflict reported above, then run tk sync")
			return pushPaused
		case integrateError:
			recordPushFailure(deps, r, t, rep, err, "clear the re-integrate error above, then run tk sync")
			return pushFailed
		}
		if err2 := git.Push(ctx, t.Root); err2 != nil {
			recordPushFailure(deps, r, t, rep, err2, "fix the remote/auth, then tk sync")
			return pushFailed
		}
	}

	if err := gitstate.ClearLastPushError(deps.StateDir, t.Root); err != nil {
		r.Err(fmt.Sprintf("%s: could not clear last-push-error marker: %v", rep.label, err))
	}
	rep.pushed = true
	if n, err := git.UnpushedCount(ctx, t.Root); err == nil {
		rep.unpushed = n
	}
	return pushOK
}

// recordPushFailure: best-effort state write must not mask the push failure.
func recordPushFailure(deps Deps, r Reporter, t Target, rep *syncReport, cause error, guidance string) {
	_ = gitstate.WriteLastPushError(deps.StateDir, t.Root, cause.Error())
	r.Err(token.Line(token.LastPushError, fmt.Sprintf(
		"%s: push did not land (%v) — %s", rep.label, cause, guidance)))
}
