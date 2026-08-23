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
	// ClaimID claims a named ticket (tk mark todo → in-progress).
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

// Claim is the single todo→in-progress orchestration. Network steps (refresh,
// push) run only on tk-driven roots with an upstream; lock spans never nest
// with syncengine.
func Claim(deps Deps, r Reporter, in ClaimInput) (Result, error) {
	r = reporterOrNop(r)
	ctx := ctxOf(deps)
	schema, _ := deps.Rec.SchemaOrError(in.Scope, in.Dir)
	autoCommit := SchemaAutoCommit(schema)
	root, hasRoot := scopefile.GitRoot(in.Dir)
	network := autoCommit && hasRoot && git.HasUpstream(ctx, root)

	attachNeeded := func(out *Result) {
		out.SyncNeeded = SyncNeededReason(ctx, deps.StateDir, in.Dir, root)
	}

	if network {
		if err := CheckMidRebase(ctx, in.Scope, autoCommit, root, hasRoot); err != nil {
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
		lock, err := scopefile.AcquireLock(in.Dir)
		if err != nil {
			return Result{}, err
		}
		defer func() { _ = lock.Release() }()
		return claimUnderLock(deps, in, autoCommit, root, hasRoot, network)
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
		return claimMarkUnderLock(deps, in, autoCommit, root, hasRoot, network)
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

func claimMarkUnderLock(deps Deps, in ClaimInput, autoCommit bool, root string, hasRoot, network bool) (Result, error) {
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

	out := Result{Warnings: res.Warnings}
	p, err := ResolveWriteRow(deps.DB, in.Scope, in.Lookup)
	if err != nil {
		return out, err
	}
	path, err := writeInProgress(deps, in, root, p, network)
	if err != nil {
		return out, err
	}
	out.ID = p.ID
	out.OldStatus = status.Todo
	out.NewStatus = status.InProgress
	if waiting := openDependsWaiting(deps, res, in.Scope, p); len(waiting) > 0 {
		out.DependsOpen = waiting
	}
	if !network {
		out.SyncDisabled, out.SyncNeeded, err = completeState(deps, in.Scope, in.Dir, autoCommit,
			fmt.Sprintf("tk: %s -> %s", p.ID, status.InProgress), p.Path, "", root, hasRoot)
		if err != nil {
			return out, err
		}
	}
	out.Path = path
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
	m.Status = status.InProgress
	if err := WriteTicketFile(p.Path, m, body); err != nil {
		return "", err
	}
	if err := deps.Rec.SyncPaths(in.Scope, WrittenPaths(p.Path, "")); err != nil {
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
