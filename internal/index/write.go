package index

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// RowStat is the minimal per-file record for reconcile's change detection.
type RowStat struct {
	ID      string
	MtimeNS int64
	Size    int64
}

// ScopeRows returns (mtime, size, id) of every indexed file in a scope, keyed by path.
func (d *DB) ScopeRows(scope string) (map[string]RowStat, error) {
	rows, err := d.sql.Query(`SELECT path, id, mtime_ns, size FROM tickets WHERE scope = ?`, scope)
	if err != nil {
		return nil, fmt.Errorf("read scope rows for %q: %w", scope, err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]RowStat{}
	for rows.Next() {
		var path string
		var rs RowStat
		if err := rows.Scan(&path, &rs.ID, &rs.MtimeNS, &rs.Size); err != nil {
			return nil, err
		}
		out[path] = rs
	}
	return out, rows.Err()
}

// UpsertTicket writes one ticket row and refreshes FTS with no edges.
// Callers with depends/related must use UpsertTicketWithEdges or edges are cleared.
func (d *DB) UpsertTicket(p *Ticket) error {
	return d.UpsertTicketWithEdges(p, nil)
}

// upsertTicketTx writes ticket/edges/fts inside a transaction. Atomicity is per-file
// only: the store is rebuildable, so a crash mid-scope self-heals on the next reconcile.
func upsertTicketTx(tx *sql.Tx, p *Ticket) error {
	conflictJSON, err := marshalStrings(p.StatusConflict)
	if err != nil {
		return err
	}
	customJSON, err := marshalCustom(p.Custom)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`
INSERT INTO tickets (path, scope, id, short_id, status, order_key, title, summary, created,
                      custom, status_conflict, archived, parse_error, parse_msg, schema_error, mtime_ns, size)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(path) DO UPDATE SET
    scope=excluded.scope, id=excluded.id, short_id=excluded.short_id, status=excluded.status,
    order_key=excluded.order_key, title=excluded.title, summary=excluded.summary, created=excluded.created,
    custom=excluded.custom, status_conflict=excluded.status_conflict,
    archived=excluded.archived, parse_error=excluded.parse_error, parse_msg=excluded.parse_msg,
    schema_error=excluded.schema_error, mtime_ns=excluded.mtime_ns, size=excluded.size`,
		p.Path, p.Scope, p.ID, p.ShortID, p.Status, p.OrderKey, p.Title, p.Summary, p.Created,
		customJSON, conflictJSON, boolToInt(p.Archived), boolToInt(p.ParseError),
		p.ParseMsg, boolToInt(p.SchemaError), p.MtimeNS, p.Size)
	if err != nil {
		return fmt.Errorf("upsert ticket %s: %w", p.Path, err)
	}

	var rowid int64
	if err := tx.QueryRow(`SELECT rowid FROM tickets WHERE path = ?`, p.Path).Scan(&rowid); err != nil {
		return fmt.Errorf("resolve rowid for %s: %w", p.Path, err)
	}

	// Contentless-delete stores no document, so this DELETE is valid; ON CONFLICT UPDATE keeps tickets.rowid.
	if _, err := tx.Exec(`DELETE FROM fts WHERE rowid = ?`, rowid); err != nil {
		return fmt.Errorf("clear fts for %s: %w", p.Path, err)
	}
	if _, err := tx.Exec(`INSERT INTO fts(rowid, title, body) VALUES (?, ?, ?)`, rowid, p.Title, string(p.Body)); err != nil {
		return fmt.Errorf("index fts for %s: %w", p.Path, err)
	}

	if _, err := tx.Exec(`DELETE FROM ticket_tags WHERE path = ?`, p.Path); err != nil {
		return fmt.Errorf("clear tags for %s: %w", p.Path, err)
	}
	for _, tag := range uniqueTags(p.Tags) {
		if _, err := tx.Exec(`INSERT INTO ticket_tags(path, tag) VALUES (?, ?)`, p.Path, tag); err != nil {
			return fmt.Errorf("insert tag %s for %s: %w", tag, p.Path, err)
		}
	}

	if _, err := tx.Exec(`DELETE FROM edges WHERE from_path = ?`, p.Path); err != nil {
		return fmt.Errorf("clear edges for %s: %w", p.Path, err)
	}
	return nil
}

// UpsertTicketWithEdges upserts a ticket and its full edge list in one transaction.
func (d *DB) UpsertTicketWithEdges(p *Ticket, edges []Edge) error {
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if err := upsertTicketTx(tx, p); err != nil {
		return err
	}
	for _, e := range uniqueEdges(edges) {
		if _, err := tx.Exec(`INSERT INTO edges(from_path, from_id, from_scope, to_id, to_scope, kind) VALUES (?, ?, ?, ?, ?, ?)`,
			e.FromPath, e.FromID, e.FromScope, e.ToID, e.ToScope, e.Kind); err != nil {
			return fmt.Errorf("insert edge %s->%s: %w", e.FromID, e.ToID, err)
		}
	}
	return tx.Commit()
}

// uniqueTags keeps the first non-empty tag. A ticket's tag list is a set;
// duplicates and empty strings must not become insert failures.
func uniqueTags(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(tags))
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		if tag == "" {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
	}
	return out
}

// uniqueEdges keeps the first (from_path, kind, to_id). The PK also enforces
// the set; collapsing here so a duplicate frontmatter entry does not fail the
// whole file as a unique-violation.
func uniqueEdges(edges []Edge) []Edge {
	if len(edges) < 2 {
		return edges
	}
	seen := make(map[string]struct{}, len(edges))
	out := make([]Edge, 0, len(edges))
	for _, e := range edges {
		key := e.FromPath + "\x00" + e.Kind + "\x00" + e.ToID
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, e)
	}
	return out
}

// DeleteByPath removes the ticket row and FTS entry for a vanished file.
// FTS has no FK to tickets, so the FTS row is deleted first while rowid is still known.
func (d *DB) DeleteByPath(path string) error {
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var rowid int64
	err = tx.QueryRow(`SELECT rowid FROM tickets WHERE path = ?`, path).Scan(&rowid)
	if errors.Is(err, sql.ErrNoRows) {
		return tx.Commit()
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM fts WHERE rowid = ?`, rowid); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM tickets WHERE path = ?`, path); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteScope drops every trace of a scope (rows, FTS, edges, timestamps, config cache).
func (d *DB) DeleteScope(scope string) error {
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`DELETE FROM fts WHERE rowid IN (SELECT rowid FROM tickets WHERE scope = ?)`, scope); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM tickets WHERE scope = ?`, scope); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM scope_meta WHERE scope = ?`, scope); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM config_cache WHERE scope = ?`, scope); err != nil {
		return err
	}
	return tx.Commit()
}

// IndexedScopes returns scopes that currently have index rows, meta, or cache entries.
func (d *DB) IndexedScopes() (map[string]bool, error) {
	rows, err := d.sql.Query(`SELECT DISTINCT scope FROM tickets
                              UNION SELECT scope FROM scope_meta
                              UNION SELECT scope FROM config_cache`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]bool{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out[s] = true
	}
	return out, rows.Err()
}

// LastIndex returns the scope's last reconcile timestamp (unix ns), or 0 if never reconciled.
func (d *DB) LastIndex(scope string) (int64, error) {
	var ns int64
	err := d.sql.QueryRow(`SELECT last_index FROM scope_meta WHERE scope = ?`, scope).Scan(&ns)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return ns, err
}

// SetLastIndex records the scope's reconcile timestamp.
func (d *DB) SetLastIndex(scope string, ns int64) error {
	_, err := d.sql.Exec(`INSERT INTO scope_meta(scope, last_index) VALUES (?, ?)
                          ON CONFLICT(scope) DO UPDATE SET last_index = excluded.last_index`, scope, ns)
	return err
}

// ConfigCacheEntry is a cached tk.cue evaluation (negative results cached too).
type ConfigCacheEntry struct {
	ClosureJSON string
	SchemaJSON  string
	ConfigError string
}

// ConfigCacheGet returns the cached config evaluation for a scope, if any.
func (d *DB) ConfigCacheGet(scope string) (ConfigCacheEntry, bool, error) {
	var e ConfigCacheEntry
	err := d.sql.QueryRow(`SELECT closure_json, schema_json, config_error FROM config_cache WHERE scope = ?`, scope).
		Scan(&e.ClosureJSON, &e.SchemaJSON, &e.ConfigError)
	if errors.Is(err, sql.ErrNoRows) {
		return ConfigCacheEntry{}, false, nil
	}
	if err != nil {
		return ConfigCacheEntry{}, false, err
	}
	return e, true, nil
}

// ConfigCacheSet stores a scope's config evaluation keyed by its closure.
func (d *DB) ConfigCacheSet(scope string, e ConfigCacheEntry) error {
	_, err := d.sql.Exec(`INSERT INTO config_cache(scope, closure_json, schema_json, config_error) VALUES (?, ?, ?, ?)
                          ON CONFLICT(scope) DO UPDATE SET closure_json=excluded.closure_json,
                              schema_json=excluded.schema_json, config_error=excluded.config_error`,
		scope, e.ClosureJSON, e.SchemaJSON, e.ConfigError)
	return err
}

func marshalStrings(v []string) (string, error) {
	if len(v) == 0 {
		return "[]", nil
	}
	b, err := json.Marshal(v)
	return string(b), err
}

func marshalCustom(v map[string]any) (string, error) {
	if len(v) == 0 {
		return "{}", nil
	}
	b, err := json.Marshal(v)
	return string(b), err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
