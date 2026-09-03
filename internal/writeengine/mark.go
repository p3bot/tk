package writeengine

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/p3bot/tk/internal/depgate"
	"github.com/p3bot/tk/internal/frontmatter"
	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/reconcile"
	"github.com/p3bot/tk/internal/scopeconfig"
	"github.com/p3bot/tk/internal/status"
)

// MarkInput is one tk mark: identities already resolved to a registered scope dir.
type MarkInput struct {
	Scope     string
	Dir       string
	Lookups   []Lookup
	NewStatus string
}

type preparedMark struct {
	lookup    Lookup
	ticket    *index.Ticket
	model     *frontmatter.Model
	body      []byte
	oldStatus string
}

// Mark rewrites status on one or more tickets, relocates across the terminal
// boundary, and self-commits on tk-driven roots in one commit. todo →
// in-progress (any named member currently todo) is the claim workflow once
// for the argv, not once per id.
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
	if len(in.Lookups) == 0 {
		return Result{}, &UsageError{Msg: "mark needs at least one id"}
	}

	prepared, err := preflightMarks(deps, in.Scope, in.Lookups)
	if err != nil {
		return Result{}, err
	}

	wasTodo := map[string]bool{}
	anyTodo := false
	for _, p := range prepared {
		if p.oldStatus == status.Todo {
			wasTodo[p.ticket.ID] = true
			anyTodo = true
		}
	}
	if in.NewStatus == status.InProgress && anyTodo {
		lookups := make([]Lookup, len(prepared))
		for i, p := range prepared {
			lookups[i] = p.lookup
		}
		sess.Release()
		return claimMarks(deps, r, claimMarksInput{
			Scope:     in.Scope,
			Dir:       in.Dir,
			Lookups:   lookups,
			WasTodo:   wasTodo,
			NewStatus: in.NewStatus,
		})
	}

	out := Result{
		Warnings:  sess.Warnings(),
		NewStatus: in.NewStatus,
	}
	members, paths, err := writePreparedMarks(deps, sess.Res, sess.Schema, in.Scope, in.Dir, in.NewStatus, custom, prepared)
	if err != nil {
		return out, err
	}
	ids := make([]string, len(members))
	for i, m := range members {
		ids[i] = m.ID
	}
	disabled, needed, err := sess.CompletePaths(markCommitMessage(ids, in.NewStatus), paths)
	if err != nil {
		return out, err
	}
	out.SyncDisabled = disabled
	out.SyncNeeded = needed
	finishMarkResult(&out, members)
	return out, nil
}

func preflightMarks(deps Deps, scope string, lookups []Lookup) ([]preparedMark, error) {
	var out []preparedMark
	seen := map[string]bool{}
	for _, lu := range lookups {
		p, err := ResolveWriteRow(deps.DB, scope, lu)
		if err != nil {
			return nil, err
		}
		if seen[p.ID] {
			continue
		}
		seen[p.ID] = true
		m, body, err := ReadTicketFile(p.Path)
		if err != nil {
			return nil, err
		}
		out = append(out, preparedMark{
			lookup:    lu,
			ticket:    p,
			model:     m,
			body:      body,
			oldStatus: m.Status,
		})
	}
	return out, nil
}

func writePreparedMarks(deps Deps, res *reconcile.Result, schema *scopeconfig.Schema, scope, dir, newStatus string, custom map[string]status.Category, prepared []preparedMark) ([]Member, []string, error) {
	members := make([]Member, 0, len(prepared))
	var paths []string
	for i := range prepared {
		p := &prepared[i]
		newPath, oldPath, err := relocateAndWrite(deps, scope, dir, newStatus, custom, p.ticket.Path, p.model, p.body)
		if err != nil {
			return members, paths, err
		}
		p.ticket.Path = newPath
		abs, err := absPath(newPath)
		if err != nil {
			return members, paths, err
		}
		members = append(members, assembleMember(schema, p.ticket, p.model, p.oldStatus, newStatus, abs, oldPath != "" && oldPath != newPath))
		paths = append(paths, WrittenPaths(newPath, oldPath)...)
	}
	attachDependsOpen(deps, res, scope, members, prepared)
	return members, paths, nil
}

func relocateAndWrite(deps Deps, scope, dir, newStatus string, custom map[string]status.Category, path string, m *frontmatter.Model, body []byte) (newPath, oldPath string, err error) {
	wasTerminal := status.IsTerminal(m.Status, custom)
	nowTerminal := status.IsTerminal(newStatus, custom)
	m.Status = newStatus

	newPath, oldPath = path, ""
	if wasTerminal != nowTerminal {
		newPath, err = TerminalLocation(dir, filepath.Base(path), nowTerminal)
		if err != nil {
			return "", "", err
		}
		oldPath = path
	}

	// Write then rename: crash never leaves two same-id files (layout drift, not a collision).
	if err := WriteTicketFile(path, m, body); err != nil {
		return "", "", err
	}
	if oldPath != "" && oldPath != newPath {
		if err := os.Rename(oldPath, newPath); err != nil {
			return "", "", fmt.Errorf("move %s to %s: %w", oldPath, newPath, err)
		}
	}
	if err := deps.Rec.SyncPaths(scope, WrittenPaths(newPath, oldPath)); err != nil {
		return "", "", err
	}
	return newPath, oldPath, nil
}

func assembleMember(schema *scopeconfig.Schema, p *index.Ticket, m *frontmatter.Model, oldStatus, newStatus, abs string, moved bool) Member {
	mem := Member{
		Path:      abs,
		ID:        p.ID,
		OldStatus: oldStatus,
		NewStatus: newStatus,
		Moved:     moved,
	}
	if oldStatus != newStatus && newStatus == status.Done && schema != nil {
		if missing := schema.MissingRequired(m); len(missing) > 0 {
			mem.RequiredMissing = missing
		}
	}
	return mem
}

// attachDependsOpen sets DependsOpen from the post-write index so a batch that
// reopens a ticket together with its dependency is argv-order stable.
func attachDependsOpen(deps Deps, res *reconcile.Result, scope string, members []Member, prepared []preparedMark) {
	need := false
	for i := range members {
		if members[i].OldStatus != members[i].NewStatus && markReadyActive(members[i].NewStatus) {
			need = true
			break
		}
	}
	if !need {
		return
	}
	gate, err := depgate.Load(gateDeps(deps), res, []string{scope})
	if err != nil {
		return
	}
	for i := range members {
		if members[i].OldStatus == members[i].NewStatus || !markReadyActive(members[i].NewStatus) {
			continue
		}
		if waiting := gate.EvalDepends(prepared[i].ticket).WaitingOn; len(waiting) > 0 {
			members[i].DependsOpen = waiting
		}
	}
}

// finishMarkResult stores Members and mirrors the first ticket onto the
// one-id scalar fields. Emitters walk Tickets().
func finishMarkResult(out *Result, members []Member) {
	out.Members = members
	if len(members) == 0 {
		return
	}
	first := members[0]
	out.Path = first.Path
	out.ID = first.ID
	out.OldStatus = first.OldStatus
	out.NewStatus = first.NewStatus
	out.Moved = first.Moved
	out.DependsOpen = first.DependsOpen
	out.RequiredMissing = first.RequiredMissing
}

func markCommitMessage(ids []string, newStatus string) string {
	if len(ids) == 1 {
		return fmt.Sprintf("tk: %s -> %s", ids[0], newStatus)
	}
	return fmt.Sprintf("tk: %d tickets -> %s", len(ids), newStatus)
}

func markReadyActive(s string) bool {
	switch s {
	case status.Todo, status.InProgress, status.Review:
		return true
	default:
		return false
	}
}
