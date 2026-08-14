package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/git"
	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/reconcile"
	"github.com/p3bot/tk/internal/scopefile"
	"github.com/p3bot/tk/internal/selfcommit"
	"github.com/p3bot/tk/internal/status"
	"github.com/p3bot/tk/internal/syncengine"
)

type claimKind int

const (
	claimSelectNext claimKind = iota
	claimMarkID
)

type claimReq struct {
	kind       claimKind
	scope      string
	dir        string
	autoCommit bool
	root       string
	hasRoot    bool
	noLens     bool
	idArg      string
	form       idForm
}

// claimReporter sends every syncengine line to stderr so claim stdout stays the path.
type claimReporter struct{ c *cobra.Command }

func (r claimReporter) Out(line string) { stderrln(r.c, line) }
func (r claimReporter) Err(line string) { stderrln(r.c, line) }

func claimNetworkApplies(ctx context.Context, autoCommit bool, root string, hasRoot bool) bool {
	return autoCommit && hasRoot && git.HasUpstream(ctx, root)
}

func runClaim(app *App, c *cobra.Command, scopeFlag string, noLens bool) error {
	e, err := app.openEngine(c)
	if err != nil {
		return err
	}
	defer e.close()

	resolved, err := e.resolveAmbient(scopeFlag)
	if err != nil {
		return err
	}
	dir := resolved.Entry.Dir
	schema, _ := e.rec.SchemaOrError(resolved.Name, dir)
	root, hasRoot := scopefile.GitRoot(dir)
	return e.runClaimWorkflow(c, claimReq{
		kind:       claimSelectNext,
		scope:      resolved.Name,
		dir:        dir,
		autoCommit: schemaAutoCommit(schema),
		root:       root,
		hasRoot:    hasRoot,
		noLens:     noLens,
	})
}

// runClaimWorkflow is the single todo→in-progress orchestration for next --claim
// and mark. Network steps (refresh, push) run only on tk-driven roots with an
// upstream; lock spans never nest with syncengine.
func (e *engine) runClaimWorkflow(c *cobra.Command, req claimReq) error {
	ctx := c.Context()
	network := claimNetworkApplies(ctx, req.autoCommit, req.root, req.hasRoot)

	if network {
		if err := checkMidRebase(ctx, req.scope, req.autoCommit, req.root, req.hasRoot); err != nil {
			return err
		}
		if err := e.refreshClaimRoot(c, req); err != nil {
			e.tkDrivenSyncNeeded(ctx, c, req.dir, req.root)
			return errClaimRefreshFailed
		}
	}

	out, err := func() (claimOut, error) {
		lock, err := scopefile.AcquireLock(req.dir)
		if err != nil {
			return claimOut{}, err
		}
		defer func() { _ = lock.Release() }()
		return e.claimUnderLock(ctx, c, req, network)
	}()
	if err != nil {
		if network {
			e.tkDrivenSyncNeeded(ctx, c, req.dir, req.root)
		}
		return err
	}

	if network {
		if err := e.pushClaimRoot(c, req); err != nil {
			e.tkDrivenSyncNeeded(ctx, c, req.dir, req.root)
			e.finishClaimStdout(c, req, out)
			return errClaimPushFailed
		}
		e.tkDrivenSyncNeeded(ctx, c, req.dir, req.root)
	}

	e.finishClaimStdout(c, req, out)
	return nil
}

type claimOut struct {
	path   string
	ticket *index.Ticket
	res    *reconcile.Result
}

func (e *engine) finishClaimStdout(c *cobra.Command, req claimReq, out claimOut) {
	stdoutln(c, out.path)
	if req.kind != claimMarkID || out.ticket == nil || out.res == nil {
		return
	}
	if line, werr := e.openDependsWarnLine(out.res, req.scope, out.ticket, status.InProgress); werr == nil && line != "" {
		stderrln(c, line)
	}
}

func (e *engine) refreshClaimRoot(c *cobra.Command, req claimReq) error {
	deps := e.syncDeps(c)
	return syncengine.RefreshRoot(deps, claimReporter{c: c}, syncengine.RootTarget(deps, req.root))
}

func (e *engine) pushClaimRoot(c *cobra.Command, req claimReq) error {
	deps := e.syncDeps(c)
	return syncengine.PushRootIfAhead(deps, claimReporter{c: c}, syncengine.RootTarget(deps, req.root))
}

func (e *engine) claimUnderLock(ctx context.Context, c *cobra.Command, req claimReq, network bool) (claimOut, error) {
	if req.kind == claimMarkID {
		return e.claimMarkUnderLock(ctx, c, req, network)
	}
	return e.claimNextUnderLock(ctx, c, req, network)
}

func (e *engine) claimNextUnderLock(ctx context.Context, c *cobra.Command, req claimReq, network bool) (claimOut, error) {
	res, targets, err := e.reconcileClosureResult(req.scope, req.dir)
	if err != nil {
		return claimOut{}, err
	}
	if err := refuseUnusableScope(res, req.scope, req.dir); err != nil {
		return claimOut{}, err
	}
	if err := checkMidRebase(ctx, req.scope, req.autoCommit, req.root, req.hasRoot); err != nil {
		return claimOut{}, err
	}
	e.printWarnings(c, res.Warnings)

	gate, err := e.buildGate(res, targets)
	if err != nil {
		return claimOut{}, err
	}
	rows, err := e.db.ScopeTickets(req.scope)
	if err != nil {
		return claimOut{}, err
	}
	sel := selectNext(gate, rows, e.reg.Lens[req.scope], req.noLens)
	sel.writeDiagnostics(c)
	if sel.Chosen == nil {
		return claimOut{}, emptyQueueError(sel.ApplyLens, sel.Lens, sel.Blocked, sel.ReadyOutsideLens)
	}

	path, err := e.writeInProgress(ctx, c, req, sel.Chosen, network)
	if err != nil {
		return claimOut{}, err
	}
	return claimOut{path: path, ticket: sel.Chosen, res: res}, nil
}

func (e *engine) claimMarkUnderLock(ctx context.Context, c *cobra.Command, req claimReq, network bool) (claimOut, error) {
	res, err := e.reconcileResult(single(req.scope, req.dir))
	if err != nil {
		return claimOut{}, err
	}
	if err := refuseUnusableScope(res, req.scope, req.dir); err != nil {
		return claimOut{}, err
	}
	if err := checkMidRebase(ctx, req.scope, req.autoCommit, req.root, req.hasRoot); err != nil {
		return claimOut{}, err
	}
	e.printWarnings(c, res.Warnings)

	p, err := e.resolveWriteRow(req.scope, req.idArg, req.form)
	if err != nil {
		return claimOut{}, err
	}
	path, err := e.writeInProgress(ctx, c, req, p, network)
	if err != nil {
		return claimOut{}, err
	}
	return claimOut{path: path, ticket: p, res: res}, nil
}

func (e *engine) writeInProgress(ctx context.Context, c *cobra.Command, req claimReq, p *index.Ticket, network bool) (string, error) {
	m, body, err := readTicketFile(p.Path)
	if err != nil {
		return "", err
	}
	if m.Status != status.Todo {
		return "", errNoLongerTodo(p.ID, m.Status)
	}
	m.Status = status.InProgress
	if err := writeTicketFile(p.Path, m, body); err != nil {
		return "", err
	}
	if err := e.rec.SyncPaths(req.scope, writtenPaths(p.Path, "")); err != nil {
		return "", err
	}
	message := fmt.Sprintf("tk: %s -> %s", p.ID, status.InProgress)
	if network {
		if err := selfcommit.Commit(ctx, selfcommit.Request{
			StateDir: e.app.StateDir,
			GitRoot:  req.root,
			Message:  message,
			NewPath:  p.Path,
		}); err != nil {
			return "", fmt.Errorf("self-commit %s: %w", req.scope, err)
		}
	} else if err := e.completeStateDurability(ctx, c, req.scope, req.dir, req.autoCommit, message, p.Path, "", req.root, req.hasRoot); err != nil {
		return "", err
	}
	return absPath(p.Path)
}

func errNoLongerTodo(id, got string) error {
	return plainDiagnostic("%s is no longer todo (status is %s) — not claimed", id, got)
}

var (
	errClaimRefreshFailed = &ExitError{
		Code:  exitFailure,
		Plain: true,
		Err:   errors.New("claim aborted: board refresh did not complete"),
	}
	errClaimPushFailed = &ExitError{
		Code:  exitFailure,
		Plain: true,
		Err:   errors.New("claim landed locally; push did not"),
	}
)
