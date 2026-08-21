package index

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	sqlite "modernc.org/sqlite"
)

// ErrSearchQuery marks a malformed FTS5 MATCH (the only user-input error Search produces).
var ErrSearchQuery = errors.New("malformed full-text search query")

// sqliteError is SQLITE_ERROR; within Search (static SQL, one MATCH param) it means a bad query.
const sqliteError = 1

// ticketColumns is the fixed list scanTicket expects; Body is never selected.
const ticketColumns = `path, scope, id, short_id, status, order_key, title, summary, created,
    custom, status_conflict, archived, parse_error, parse_msg, schema_error, mtime_ns, size`

func scanTicket(sc interface{ Scan(...any) error }) (*Ticket, error) {
	var (
		p                    Ticket
		custom, conflict     string
		archived, perr, serr int
	)
	if err := sc.Scan(&p.Path, &p.Scope, &p.ID, &p.ShortID, &p.Status, &p.OrderKey, &p.Title, &p.Summary,
		&p.Created, &custom, &conflict, &archived, &perr, &p.ParseMsg, &serr, &p.MtimeNS, &p.Size); err != nil {
		return nil, err
	}
	if err := fillTicket(&p, custom, conflict, archived, perr, serr); err != nil {
		return nil, err
	}
	return &p, nil
}

func fillTicket(p *Ticket, custom, conflict string, archived, perr, serr int) error {
	p.Archived = archived != 0
	p.ParseError = perr != 0
	p.SchemaError = serr != 0
	if err := unmarshalStrings(conflict, &p.StatusConflict); err != nil {
		return err
	}
	if custom == "" {
		return nil
	}
	return json.Unmarshal([]byte(custom), &p.Custom)
}

// AllTickets returns every ticket row machine-wide.
func (d *DB) AllTickets() ([]*Ticket, error) {
	return d.queryTickets(`SELECT ` + ticketColumns + ` FROM tickets`)
}

// ScopeTickets returns every ticket row in one scope.
func (d *DB) ScopeTickets(scope string) ([]*Ticket, error) {
	return d.queryTickets(`SELECT `+ticketColumns+` FROM tickets WHERE scope = ?`, scope)
}

// TicketsByID returns rows in a scope with the given full id (may be >1 under collision).
func (d *DB) TicketsByID(scope, id string) ([]*Ticket, error) {
	return d.queryTickets(`SELECT `+ticketColumns+` FROM tickets WHERE scope = ? AND id = ?`, scope, id)
}

// TicketsByShortID returns rows in a scope with the given short id.
func (d *DB) TicketsByShortID(scope, shortID string) ([]*Ticket, error) {
	return d.queryTickets(`SELECT `+ticketColumns+` FROM tickets WHERE scope = ? AND short_id = ?`, scope, shortID)
}

// TicketsByFullID returns every row machine-wide with the given full id.
func (d *DB) TicketsByFullID(id string) ([]*Ticket, error) {
	return d.queryTickets(`SELECT `+ticketColumns+` FROM tickets WHERE id = ?`, id)
}

// TicketsByFullIDs returns every row whose full id is in ids (may be >1 per id under collision).
func (d *DB) TicketsByFullIDs(ids []string) ([]*Ticket, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var args []any
	q := `SELECT ` + ticketColumns + ` FROM tickets WHERE ` + inPred("id", ids, &args)
	return d.queryTickets(q, args...)
}

// TicketsInScopes returns every ticket row in the given scopes.
func (d *DB) TicketsInScopes(scopes []string) ([]*Ticket, error) {
	if len(scopes) == 0 {
		return nil, nil
	}
	var args []any
	q := `SELECT ` + ticketColumns + ` FROM tickets WHERE ` + inPred("scope", scopes, &args)
	return d.queryTickets(q, args...)
}

func (d *DB) queryTickets(q string, args ...any) ([]*Ticket, error) {
	rows, err := d.sql.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*Ticket
	for rows.Next() {
		p, err := scanTicket(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := d.attachTags(out); err != nil {
		return nil, err
	}
	return out, nil
}

// attachTags fills Ticket.Tags from ticket_tags in insert order (rowid).
func (d *DB) attachTags(tickets []*Ticket) error {
	if len(tickets) == 0 {
		return nil
	}
	byPath := make(map[string]*Ticket, len(tickets))
	paths := make([]string, 0, len(tickets))
	for _, p := range tickets {
		if p == nil {
			continue
		}
		p.Tags = nil
		byPath[p.Path] = p
		paths = append(paths, p.Path)
	}
	if len(paths) == 0 {
		return nil
	}
	var args []any
	q := `SELECT path, tag FROM ticket_tags WHERE ` + inPred("path", paths, &args) + ` ORDER BY rowid`
	rows, err := d.sql.Query(q, args...)
	if err != nil {
		return fmt.Errorf("load ticket tags: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var path, tag string
		if err := rows.Scan(&path, &tag); err != nil {
			return err
		}
		if p := byPath[path]; p != nil {
			p.Tags = append(p.Tags, tag)
		}
	}
	return rows.Err()
}

// SearchHit is one FTS result with its bm25 score (smaller is better).
type SearchHit struct {
	Ticket *Ticket
	Score  float64
}

// Search runs FTS5 MATCH over titles and bodies (bm25, id tie-break). Empty scope is machine-wide.
func (d *DB) Search(scope, match string) ([]SearchHit, error) {
	q := `SELECT ` + prefixed("p.", ticketColumns) + `, bm25(fts) AS score
          FROM fts JOIN tickets p ON p.rowid = fts.rowid
          WHERE fts MATCH ?`
	args := []any{match}
	if scope != "" {
		q += ` AND p.scope = ?`
		args = append(args, scope)
	}
	q += ` ORDER BY score ASC, p.id ASC`

	rows, err := d.sql.Query(q, args...)
	if err != nil {
		if isQuerySyntaxErr(err) {
			return nil, fmt.Errorf("%w: %w", ErrSearchQuery, err)
		}
		return nil, fmt.Errorf("fts search: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []SearchHit
	for rows.Next() {
		hit, err := scanSearchHit(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, hit)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	hits := make([]*Ticket, len(out))
	for i := range out {
		hits[i] = out[i].Ticket
	}
	if err := d.attachTags(hits); err != nil {
		return nil, err
	}
	return out, nil
}

// isQuerySyntaxErr reports FTS5 malformed-query errors (SQLITE_ERROR under Search's static SQL).
func isQuerySyntaxErr(err error) bool {
	var se *sqlite.Error
	return errors.As(err, &se) && se.Code() == sqliteError
}

func scanSearchHit(rows *sql.Rows) (SearchHit, error) {
	var (
		p                    Ticket
		custom, conflict     string
		archived, perr, serr int
		score                float64
	)
	if err := rows.Scan(&p.Path, &p.Scope, &p.ID, &p.ShortID, &p.Status, &p.OrderKey, &p.Title, &p.Summary,
		&p.Created, &custom, &conflict, &archived, &perr, &p.ParseMsg, &serr, &p.MtimeNS, &p.Size, &score); err != nil {
		return SearchHit{}, err
	}
	if err := fillTicket(&p, custom, conflict, archived, perr, serr); err != nil {
		return SearchHit{}, err
	}
	return SearchHit{Ticket: &p, Score: score}, nil
}

// AllEdges returns every edge machine-wide.
func (d *DB) AllEdges() ([]Edge, error) {
	return d.queryEdges(`SELECT from_path, from_id, from_scope, to_id, to_scope, kind FROM edges`)
}

// EdgesByTarget returns every edge pointing at toID (ordered for stable reports).
func (d *DB) EdgesByTarget(toID string) ([]Edge, error) {
	return d.queryEdges(`SELECT from_path, from_id, from_scope, to_id, to_scope, kind
	                     FROM edges WHERE to_id = ? ORDER BY from_id, kind`, toID)
}

// EdgesFromPath returns outgoing edges from one ticket file (the physical key).
func (d *DB) EdgesFromPath(fromPath string) ([]Edge, error) {
	return d.queryEdges(`SELECT from_path, from_id, from_scope, to_id, to_scope, kind
	                     FROM edges WHERE from_path = ? ORDER BY kind, to_id`, fromPath)
}

// EdgesFromID returns outgoing edges from every file claiming fromID (logical graph node).
func (d *DB) EdgesFromID(fromID string) ([]Edge, error) {
	return d.queryEdges(`SELECT from_path, from_id, from_scope, to_id, to_scope, kind
	                     FROM edges WHERE from_id = ? ORDER BY from_path, kind, to_id`, fromID)
}

// EdgesToScope returns every edge whose target lies in toScope (ordered for stable reports).
func (d *DB) EdgesToScope(toScope string) ([]Edge, error) {
	return d.queryEdges(`SELECT from_path, from_id, from_scope, to_id, to_scope, kind
	                     FROM edges WHERE to_scope = ? ORDER BY from_scope, from_id, kind`, toScope)
}

// DependsFromScopes returns depends edges whose from_scope is in scopes.
func (d *DB) DependsFromScopes(scopes []string) ([]Edge, error) {
	if len(scopes) == 0 {
		return nil, nil
	}
	var args []any
	q := `SELECT from_path, from_id, from_scope, to_id, to_scope, kind FROM edges WHERE kind = ? AND ` +
		inPred("from_scope", scopes, &args)
	args = append([]any{EdgeDepends}, args...)
	return d.queryEdges(q, args...)
}

// DependsTargetScopes returns distinct to_scope values of depends edges from fromScopes.
func (d *DB) DependsTargetScopes(fromScopes []string) ([]string, error) {
	if len(fromScopes) == 0 {
		return nil, nil
	}
	var args []any
	q := `SELECT DISTINCT to_scope FROM edges WHERE kind = ? AND ` + inPred("from_scope", fromScopes, &args)
	args = append([]any{EdgeDepends}, args...)
	rows, err := d.sql.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("depends target scopes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// SameScopeDanglingDependsCount is the number of same-scope depends edges whose
// to_id matches no ticket in that scope.
func (d *DB) SameScopeDanglingDependsCount(scope string) (int, error) {
	var n int
	err := d.sql.QueryRow(`
SELECT COUNT(*) FROM edges e
WHERE e.kind = ? AND e.from_scope = ? AND e.to_scope = ?
  AND NOT EXISTS (SELECT 1 FROM tickets t WHERE t.scope = ? AND t.id = e.to_id)`,
		EdgeDepends, scope, scope, scope).Scan(&n)
	return n, err
}

func (d *DB) queryEdges(q string, args ...any) ([]Edge, error) {
	rows, err := d.sql.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Edge
	for rows.Next() {
		var e Edge
		if err := rows.Scan(&e.FromPath, &e.FromID, &e.FromScope, &e.ToID, &e.ToScope, &e.Kind); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func unmarshalStrings(s string, dst *[]string) error {
	if s == "" {
		return nil
	}
	return json.Unmarshal([]byte(s), dst)
}

// prefixed rewrites a comma-separated column list so each bare column gets alias (keeps ticketColumns authoritative).
func prefixed(alias, cols string) string {
	parts := strings.Split(cols, ",")
	for i, c := range parts {
		trimmed := strings.TrimSpace(c)
		lead := c[:len(c)-len(strings.TrimLeft(c, " \t\n"))]
		parts[i] = lead + alias + trimmed
	}
	return strings.Join(parts, ",")
}
