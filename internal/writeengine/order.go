package writeengine

import (
	"fmt"

	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/order"
)

// Dest is exactly one order placement. Neighbour Lookups use Arg=="" as unset.
type Dest struct {
	First  bool
	Last   bool
	Before Lookup
	After  Lookup
}

// Count is how many placements are set. Order requires exactly one.
func (d Dest) Count() int {
	n := 0
	if d.First {
		n++
	}
	if d.Last {
		n++
	}
	if d.Before.Arg != "" {
		n++
	}
	if d.After.Arg != "" {
		n++
	}
	return n
}

// OrderInput is one tk order. Identity (ambient / --scope / me) stays at the edge.
type OrderInput struct {
	Scope  string
	Dir    string
	Lookup Lookup
	Dest   Dest
}

// Order rewrites one ticket's order key and self-commits on tk-driven roots.
func Order(deps Deps, in OrderInput) (Result, error) {
	if in.Dest.Count() != 1 {
		return Result{}, &UsageError{Msg: "order needs exactly one destination"}
	}

	sess, err := Begin(deps, in.Scope, in.Dir)
	if err != nil {
		return Result{}, err
	}
	defer sess.Release()
	if err := sess.CheckMidRebase(); err != nil {
		return Result{}, err
	}

	out := Result{Warnings: sess.Warnings()}
	subject, err := ResolveWriteRow(deps.DB, in.Scope, in.Lookup)
	if err != nil {
		return out, err
	}

	rows, err := deps.DB.ScopeTickets(in.Scope)
	if err != nil {
		return out, err
	}
	left, right, err := orderBounds(deps, in.Scope, subject, rows, in.Dest)
	if err != nil {
		return out, err
	}
	newKey, err := order.KeyBetween(left, right)
	if err != nil {
		return out, &NoLegalOrderError{ID: subject.ID, Err: err}
	}

	m, body, err := ReadTicketFile(subject.Path)
	if err != nil {
		return out, err
	}
	m.Order = newKey
	if err := WriteTicketFile(subject.Path, m, body); err != nil {
		return out, err
	}
	if err := deps.Rec.SyncPaths(in.Scope, WrittenPaths(subject.Path, "")); err != nil {
		return out, err
	}

	message := fmt.Sprintf("tk: %s order", subject.ID)
	disabled, needed, err := sess.CompleteState(message, subject.Path, "")
	if err != nil {
		return out, err
	}
	out.ID = subject.ID
	out.SyncDisabled = disabled
	out.SyncNeeded = needed

	abs, err := absPath(subject.Path)
	if err != nil {
		return out, err
	}
	out.Path = abs
	return out, nil
}

func orderBounds(deps Deps, scope string, subject *index.Ticket, rows []*index.Ticket, dest Dest) (left, right string, err error) {
	others := make([]*index.Ticket, 0, len(rows))
	for _, p := range rows {
		if p.Path == subject.Path || p.ParseError || !order.Valid(p.OrderKey) {
			continue
		}
		others = append(others, p)
	}
	index.SortTickets(others)

	switch {
	case dest.First:
		if len(others) == 0 {
			return "", "", nil
		}
		return "", others[0].OrderKey, nil
	case dest.Last:
		if len(others) == 0 {
			return "", "", nil
		}
		return others[len(others)-1].OrderKey, "", nil
	case dest.Before.Arg != "":
		return neighbourBounds(deps, scope, subject, others, dest.Before, true)
	default:
		return neighbourBounds(deps, scope, subject, others, dest.After, false)
	}
}

func neighbourBounds(deps Deps, scope string, subject *index.Ticket, others []*index.Ticket, neighbour Lookup, before bool) (left, right string, err error) {
	n, err := ResolveRow(deps.DB, scope, neighbour, "neighbour")
	if err != nil {
		return "", "", err
	}
	if n.Path == subject.Path {
		return "", "", &UsageError{Msg: fmt.Sprintf("cannot order %q relative to itself", subject.ID)}
	}
	if n.ParseError || !order.Valid(n.OrderKey) {
		return "", "", &NeighbourOrderError{Arg: neighbour.Arg}
	}

	idx := -1
	for i, p := range others {
		if p.Path == n.Path {
			idx = i
			break
		}
	}
	if idx < 0 {
		return "", "", fmt.Errorf("neighbour %q not found in scope order", neighbour.Arg)
	}
	if before {
		l := ""
		if idx > 0 {
			l = others[idx-1].OrderKey
		}
		return l, n.OrderKey, nil
	}
	r := ""
	if idx < len(others)-1 {
		r = others[idx+1].OrderKey
	}
	return n.OrderKey, r, nil
}
