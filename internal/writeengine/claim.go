package writeengine

import (
	"fmt"

	"github.com/p3bot/tk/internal/depgate"
	"github.com/p3bot/tk/internal/git"
	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/scopefile"
	"github.com/p3bot/tk/internal/selfcommit"
	"github.com/p3bot/tk/internal/status"
	"github.com/p3bot/tk/internal/syncengine"
)

// ClaimKind selects next-eligible vs mark-by-id claim.
type ClaimKind int

const (
	// ClaimNext claims the first runnable todo (tk next --claim).
	ClaimNext ClaimKind = iota
	// ClaimID claims a named ticket (tk mark in-progress of a todo).
	ClaimID
)

// ClaimInput is one claim. Identity resolution (ambient, --scope, me) stays at the edge.
type ClaimInput struct {
	Kind   ClaimKind
	Scope  string
	Dir    string
	NoLens bool
	Lookup Lookup
}

type claimMarksInput struct {
	Scope       string
	Dir         string
	Lookups     []Lookup
	WasTodo     map[string]bool
	NewStatus   string
	RequireTodo bool
}

type networkClaimFn func(autoCommit bool, root string, hasRoot, network bool) (Result, error)

// Claim is the single todo→in-progress orchestration. Network steps (refresh,
// push) run only on tk-driven roots with an upstream; lock spans never nest
// with syncengine.
func Claim(deps Deps, r Reporter, in ClaimInput) (Result, error) {
	return runNetworkClaim(deps, r, in.Scope, in.Dir, func(autoCommit bool, root string, hasRoot, network bool) (Result, error) {
		return claimUnderLock(deps, in, autoCommit, root, hasRoot, network)
	})
}

func claimMarks(deps Deps, r Reporter, in claimMarksInput) (Result, error) {
	return runNetworkClaim(deps, r, in.Scope, in.Dir, func(autoCommit bool, root string, hasRoot, network bool) (Result, error) {
		return claimMarksUnderLock(deps, in, autoCommit, root, hasRoot, network)
	})
}

func runNetworkClaim(deps Deps, r Reporter, scope, dir string, underLock networkClaimFn) (Result, error) {
	r = reporterOrNop(r)
	ctx := ctxOf(deps)
	schema, _ := deps.Rec.SchemaOrError(scope, dir)
	autoCommit := SchemaAutoCommit(schema)
	root, hasRoot := scopefile.GitRoot(dir)
	network := autoCommit && hasRoot && git.HasUpstream(ctx, root)

	attachNeeded := func(out *Result) {
		out.SyncNeeded = SyncNeededReason(ctx, deps.StateDir, dir, root)
	}

	if network {
		if err := CheckMidRebase(ctx, scope, autoCommit, root, hasRoot); err != nil {
			return Result{}, err
		}
		sd := syncDeps(deps)
		if err := syncengine.RefreshRoot(sd, r, syncengine.RootTarget(sd, root)); err != nil {
			var out Result
			attachNeeded(&out)
			return out, ErrRefreshFailed
		}
	}

	out, err := func() (Result, error) {
		lock, err := scopefile.AcquireLock(dir)
		if err != nil {
			return Result{}, err
		}
		defer func() { _ = lock.Release() }()
		return underLock(autoCommit, root, hasRoot, network)
	}()
	if err != nil {
		if network {
			attachNeeded(&out)
		}
		return out, err
	}

	if network {
		sd := syncDeps(deps)
		if err := syncengine.PushRootIfAhead(sd, r, syncengine.RootTarget(sd, root)); err != nil {
			attachNeeded(&out)
			return out, ErrPushFailed
		}
		attachNeeded(&out)
	}

	return out, nil
}

func claimUnderLock(deps Deps, in ClaimInput, autoCommit bool, root string, hasRoot, network bool) (Result, error) {
	if in.Kind == ClaimID {
		return claimMarksUnderLock(deps, claimMarksInput{
			Scope:       in.Scope,
			Dir:         in.Dir,
			Lookups:     []Lookup{in.Lookup},
			RequireTodo: true,
			NewStatus:   status.InProgress,
		}, autoCommit, root, hasRoot, network)
	}
	return claimNextUnderLock(deps, in, autoCommit, root, hasRoot, network)
}

func claimNextUnderLock(deps Deps, in ClaimInput, autoCommit bool, root string, hasRoot, network bool) (Result, error) {
	res, targets, err := deps.Rec.ReconcileClosure(deps.Reg, in.Scope, in.Dir, nowNS())
	if err != nil {
		return Result{}, err
	}
	if err := RefuseUnusable(res, in.Scope, in.Dir); err != nil {
		return Result{}, err
	}
	if err := CheckMidRebase(ctxOf(deps), in.Scope, autoCommit, root, hasRoot); err != nil {
		return Result{}, err
	}

	out := Result{Warnings: res.Warnings}
	gate, err := depgate.Load(gateDeps(deps), res, targets)
	if err != nil {
		return out, err
	}
	candidates, err := deps.DB.NextCandidates(in.Scope)
	if err != nil {
		return out, err
	}
	sel := gate.SelectNext(candidates, deps.Reg.Lens[in.Scope], in.NoLens)
	out.SelectionTokens = sel.Tokens
	out.ApplyLens = sel.ApplyLens
	out.Lens = sel.Lens
	out.Blocked = sel.Blocked
	out.ReadyOutsideLens = sel.ReadyOutsideLens
	if sel.Chosen == nil {
		return out, sel.EmptyQueueError()
	}

	path, err := writeInProgress(deps, in, root, sel.Chosen, network)
	if err != nil {
		return out, err
	}
	out.ID = sel.Chosen.ID
	out.OldStatus = status.Todo
	out.NewStatus = status.InProgress
	if !network {
		out.SyncDisabled, out.SyncNeeded, err = completeState(deps, in.Scope, in.Dir, autoCommit,
			fmt.Sprintf("tk: %s -> %s", sel.Chosen.ID, status.InProgress), sel.Chosen.Path, "", root, hasRoot)
		if err != nil {
			return out, err
		}
	}
	out.Path = path
	return out, nil
}

func claimMarksUnderLock(deps Deps, in claimMarksInput, autoCommit bool, root string, hasRoot, network bool) (Result, error) {
	res, err := deps.Rec.Reconcile(map[string]string{in.Scope: in.Dir}, registeredSet(deps.Reg), nowNS())
	if err != nil {
		return Result{}, err
	}
	if err := RefuseUnusable(res, in.Scope, in.Dir); err != nil {
		return Result{}, err
	}
	if err := CheckMidRebase(ctxOf(deps), in.Scope, autoCommit, root, hasRoot); err != nil {
		return Result{}, err
	}

	out := Result{Warnings: res.Warnings, NewStatus: in.NewStatus}
	schema := res.Schema(in.Scope)
	custom := map[string]status.Category{}
	if schema != nil {
		custom = schema.CustomStatuses()
	}

	prepared, err := preflightMarks(deps, in.Scope, in.Lookups)
	if err != nil {
		return out, err
	}
	for _, p := range prepared {
		if (in.RequireTodo || in.WasTodo[p.ticket.ID]) && p.oldStatus != status.Todo {
			return out, &NoLongerTodoError{ID: p.ticket.ID, Status: p.oldStatus}
		}
	}

	members, paths, err := writePreparedMarks(deps, res, schema, in.Scope, in.Dir, in.NewStatus, custom, prepared)
	if err != nil {
		return out, err
	}
	ids := make([]string, len(members))
	for i, m := range members {
		ids[i] = m.ID
	}
	message := markCommitMessage(ids, in.NewStatus)
	if network {
		if err := selfcommit.CommitPaths(ctxOf(deps), selfcommit.BatchRequest{
			StateDir: deps.StateDir,
			GitRoot:  root,
			Message:  message,
			Paths:    paths,
		}); err != nil {
			return out, fmt.Errorf("self-commit %s: %w", in.Scope, err)
		}
	} else {
		out.SyncDisabled, out.SyncNeeded, err = completePaths(deps, in.Scope, in.Dir, autoCommit, message, paths, root, hasRoot)
		if err != nil {
			return out, err
		}
	}
	finishMarkResult(&out, members)
	return out, nil
}

func writeInProgress(deps Deps, in ClaimInput, root string, p *index.Ticket, network bool) (string, error) {
	m, body, err := ReadTicketFile(p.Path)
	if err != nil {
		return "", err
	}
	if m.Status != status.Todo {
		return "", &NoLongerTodoError{ID: p.ID, Status: m.Status}
	}
	if _, _, err := relocateAndWrite(deps, in.Scope, in.Dir, status.InProgress, nil, p.Path, m, body); err != nil {
		return "", err
	}
	if network {
		if err := selfcommit.Commit(ctxOf(deps), selfcommit.Request{
			StateDir: deps.StateDir,
			GitRoot:  root,
			Message:  fmt.Sprintf("tk: %s -> %s", p.ID, status.InProgress),
			NewPath:  p.Path,
		}); err != nil {
			return "", fmt.Errorf("self-commit %s: %w", in.Scope, err)
		}
	}
	return absPath(p.Path)
}
