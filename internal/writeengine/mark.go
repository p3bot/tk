package writeengine

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/p3bot/tk/internal/depgate"
	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/reconcile"
	"github.com/p3bot/tk/internal/status"
)

// MarkInput is one tk mark: identity already resolved to a registered scope dir.
type MarkInput struct {
	Scope     string
	Dir       string
	Lookup    Lookup
	NewStatus string
}

// Mark rewrites status, relocates across the terminal boundary, and self-commits
// on tk-driven roots. todo → in-progress is the claim workflow (one writer).
func Mark(deps Deps, r Reporter, in MarkInput) (Result, error) {
	sess, err := Begin(deps, in.Scope, in.Dir)
	if err != nil {
		return Result{}, err
	}
	defer sess.Release()

	custom := sess.Schema.CustomStatuses()
	if !status.IsKnown(in.NewStatus, custom) {
		return Result{}, &UnknownStatusError{Status: in.NewStatus, Scope: in.Scope}
	}
	if err := sess.CheckMidRebase(); err != nil {
		return Result{}, err
	}

	p, err := ResolveWriteRow(deps.DB, in.Scope, in.Lookup)
	if err != nil {
		return Result{}, err
	}
	m, body, err := ReadTicketFile(p.Path)
	if err != nil {
		return Result{}, err
	}
	oldStatus := m.Status
	if oldStatus == status.Todo && in.NewStatus == status.InProgress {
		sess.Release()
		return Claim(deps, r, ClaimInput{
			Kind:   ClaimID,
			Scope:  in.Scope,
			Dir:    in.Dir,
			Lookup: in.Lookup,
		})
	}

	out := Result{
		ID:        p.ID,
		OldStatus: oldStatus,
		NewStatus: in.NewStatus,
		Warnings:  sess.Warnings(),
	}

	wasTerminal := status.IsTerminal(oldStatus, custom)
	nowTerminal := status.IsTerminal(in.NewStatus, custom)
	m.Status = in.NewStatus

	newPath, oldPath := p.Path, ""
	if wasTerminal != nowTerminal {
		newPath, err = TerminalLocation(in.Dir, filepath.Base(p.Path), nowTerminal)
		if err != nil {
			return out, err
		}
		oldPath = p.Path
		out.Moved = newPath != oldPath
	}

	// Write then rename: crash never leaves two same-id files (layout drift, not a collision).
	if err := WriteTicketFile(p.Path, m, body); err != nil {
		return out, err
	}
	if oldPath != "" && oldPath != newPath {
		if err := os.Rename(oldPath, newPath); err != nil {
			return out, fmt.Errorf("move %s to %s: %w", oldPath, newPath, err)
		}
	}
	if err := deps.Rec.SyncPaths(in.Scope, WrittenPaths(newPath, oldPath)); err != nil {
		return out, err
	}

	message := fmt.Sprintf("tk: %s -> %s", p.ID, in.NewStatus)
	disabled, needed, err := sess.CompleteState(message, newPath, oldPath)
	if err != nil {
		return out, err
	}
	out.SyncDisabled = disabled
	out.SyncNeeded = needed

	abs, err := absPath(newPath)
	if err != nil {
		return out, err
	}
	out.Path = abs

	if oldStatus != in.NewStatus && markReadyActive(in.NewStatus) {
		p.Path = newPath
		if waiting := openDependsWaiting(deps, sess.Res, in.Scope, p); len(waiting) > 0 {
			out.DependsOpen = waiting
		}
	}
	if oldStatus != in.NewStatus && in.NewStatus == status.Done {
		if missing := sess.Schema.MissingRequired(m); len(missing) > 0 {
			out.RequiredMissing = missing
		}
	}
	return out, nil
}

func markReadyActive(s string) bool {
	switch s {
	case status.Todo, status.InProgress, status.Review:
		return true
	default:
		return false
	}
}

func openDependsWaiting(deps Deps, res *reconcile.Result, scope string, p *index.Ticket) []string {
	gate, err := depgate.Load(gateDeps(deps), res, []string{scope})
	if err != nil {
		return nil
	}
	ds := gate.EvalDepends(p)
	return ds.WaitingOn
}
