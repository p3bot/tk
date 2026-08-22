package index

import (
	"fmt"
	"sort"

	"github.com/p3bot/tk/internal/status"
)

// BoardFilter selects parseable tickets for a scope board (list). Statuses, when
// non-empty, is a hard status IN filter (includes archive/). All, when Statuses
// is empty, includes archived and every non-quarantined status. Otherwise the
// default board is archived=0 AND status IN DefaultStatuses. Empty
// DefaultStatuses means the built-in default list (status.DefaultListNames(nil));
// pass custom active names when the scope declares them. Tags is a hard OR
// membership filter (untagged out) and ignores Lens. Lens (when Tags is empty):
// untagged rows still pass.
type BoardFilter struct {
	Scope           string
	Statuses        []string
	All             bool
	DefaultStatuses []string
	Tags            []string
	Lens            []string
}

// BoardTickets returns the filtered board rows for a scope, ordered by (order_key, id).
func (d *DB) BoardTickets(f BoardFilter) ([]*Ticket, error) {
	q := `SELECT ` + ticketColumns + ` FROM tickets WHERE scope = ? AND parse_error = 0`
	args := []any{f.Scope}
	switch {
	case len(f.Statuses) > 0:
		q += ` AND ` + inPred("status", f.Statuses, &args)
	case f.All:
	default:
		def := f.DefaultStatuses
		if len(def) == 0 {
			def = status.DefaultListNames(nil)
		}
		q += ` AND archived = 0 AND ` + inPred("status", def, &args)
	}
	switch {
	case len(f.Tags) > 0:
		q += ` AND EXISTS (SELECT 1 FROM ticket_tags tt WHERE tt.path = tickets.path AND ` +
			inPred("tt.tag", f.Tags, &args) + `)`
	case len(f.Lens) > 0:
		if pred := lensSQL("tickets.path", f.Lens, &args); pred != "" {
			q += ` AND ` + pred
		}
	}
	q += ` ORDER BY order_key, id`
	return d.queryTickets(q, args...)
}

// SortTickets orders rows by (order_key, id), matching BoardTickets and NextCandidates.
func SortTickets(rows []*Ticket) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].OrderKey != rows[j].OrderKey {
			return rows[i].OrderKey < rows[j].OrderKey
		}
		return rows[i].ID < rows[j].ID
	})
}

// NextCandidates returns todo, non-archived, non-quarantined rows in a scope,
// ordered by (order_key, id). Duplicate-id skip and depends hold stay in Go.
func (d *DB) NextCandidates(scope string) ([]*Ticket, error) {
	return d.queryTickets(`SELECT `+ticketColumns+` FROM tickets
WHERE scope = ? AND status = ? AND archived = 0 AND parse_error = 0
ORDER BY order_key, id`, scope, status.Todo)
}

// ScopePulse is the SQL-backed status pulse for one scope (except next, which
// reuses NextCandidates plus the depends gate).
type ScopePulse struct {
	Total      int
	Todo       int
	Review     int
	InProgress int
	Blocked    int
	Draft      int
	Backlog    int
	Done       int
	Cancelled  int
	Claimed    []*Ticket
	BlockedIDs []*Ticket
}

// ScopePulse loads full-scope totals and the working-board (non-archive, lens) pulse.
func (d *DB) ScopePulse(scope string, lens []string) (ScopePulse, error) {
	var p ScopePulse
	err := d.sql.QueryRow(`
SELECT COUNT(*),
       COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0),
       COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0)
FROM tickets WHERE scope = ? AND parse_error = 0`,
		status.Done, status.Cancelled, scope).Scan(&p.Total, &p.Done, &p.Cancelled)
	if err != nil {
		return ScopePulse{}, fmt.Errorf("pulse totals for %q: %w", scope, err)
	}

	q := `SELECT status, COUNT(*) FROM tickets WHERE scope = ? AND parse_error = 0 AND archived = 0`
	args := []any{scope}
	if pred := lensSQL("tickets.path", lens, &args); pred != "" {
		q += ` AND ` + pred
	}
	q += ` GROUP BY status`
	rows, err := d.sql.Query(q, args...)
	if err != nil {
		return ScopePulse{}, fmt.Errorf("pulse working counts for %q: %w", scope, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return ScopePulse{}, err
		}
		switch st {
		case status.Todo:
			p.Todo = n
		case status.Review:
			p.Review = n
		case status.InProgress:
			p.InProgress = n
		case status.Blocked:
			p.Blocked = n
		case status.Draft:
			p.Draft = n
		case status.Backlog:
			p.Backlog = n
		}
	}
	if err := rows.Err(); err != nil {
		return ScopePulse{}, err
	}

	claimed, err := d.workingByStatus(scope, status.InProgress, lens)
	if err != nil {
		return ScopePulse{}, err
	}
	blocked, err := d.workingByStatus(scope, status.Blocked, lens)
	if err != nil {
		return ScopePulse{}, err
	}
	p.Claimed = claimed
	p.BlockedIDs = blocked
	return p, nil
}

func (d *DB) workingByStatus(scope, st string, lens []string) ([]*Ticket, error) {
	q := `SELECT ` + ticketColumns + ` FROM tickets
WHERE scope = ? AND parse_error = 0 AND archived = 0 AND status = ?`
	args := []any{scope, st}
	if pred := lensSQL("tickets.path", lens, &args); pred != "" {
		q += ` AND ` + pred
	}
	q += ` ORDER BY order_key, id`
	return d.queryTickets(q, args...)
}

func lensSQL(pathCol string, lens []string, args *[]any) string {
	if len(lens) == 0 {
		return ""
	}
	return `(NOT EXISTS (SELECT 1 FROM ticket_tags tt0 WHERE tt0.path = ` + pathCol + `)` +
		` OR EXISTS (SELECT 1 FROM ticket_tags tt1 WHERE tt1.path = ` + pathCol +
		` AND ` + inPred("tt1.tag", lens, args) + `))`
}
