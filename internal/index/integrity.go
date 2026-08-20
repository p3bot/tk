package index

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Collision is a group of ticket rows in one scope that share a key that must be
// unique (full id or order key). Members are sorted paths for stable warnings.
type Collision struct {
	Scope   string
	Key     string
	Members []string
}

// DuplicateIDs returns full ids claimed by two or more files in the given scopes.
func (d *DB) DuplicateIDs(scopes []string) ([]Collision, error) {
	return d.collisions(scopes, "id", `1`)
}

// EqualOrders returns non-empty order keys shared by two or more tickets in the given scopes.
func (d *DB) EqualOrders(scopes []string) ([]Collision, error) {
	return d.collisions(scopes, "order_key", `order_key <> ''`)
}

// collisions groups rows by keyCol, keeping groups of size > 1 via HAVING COUNT(*) > 1.
func (d *DB) collisions(scopes []string, keyCol, extraPred string) ([]Collision, error) {
	if len(scopes) == 0 {
		return nil, nil
	}
	var args []any
	inner := inPred("scope", scopes, &args)
	outer := inPred("t.scope", scopes, &args)
	outerExtra := extraPred
	if extraPred != `1` {
		outerExtra = "t." + extraPred
	}
	q := fmt.Sprintf(`
SELECT t.scope, t.%[1]s AS k, t.path
FROM tickets t
INNER JOIN (
    SELECT scope, %[1]s AS k
    FROM tickets
    WHERE %[2]s AND %[3]s
    GROUP BY scope, %[1]s
    HAVING COUNT(*) > 1
) g ON t.scope = g.scope AND t.%[1]s = g.k
WHERE %[4]s AND %[5]s
ORDER BY t.scope, k, t.path`,
		keyCol, inner, extraPred, outer, outerExtra)
	rows, err := d.sql.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("collision aggregate: %w", err)
	}
	defer func() { _ = rows.Close() }()

	type key struct{ scope, k string }
	grouped := map[key][]string{}
	var order []key
	for rows.Next() {
		var scope, k, path string
		if err := rows.Scan(&scope, &k, &path); err != nil {
			return nil, err
		}
		kk := key{scope, k}
		if _, seen := grouped[kk]; !seen {
			order = append(order, kk)
		}
		grouped[kk] = append(grouped[kk], path)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var out []Collision
	for _, kk := range order {
		members := grouped[kk]
		sort.Strings(members)
		out = append(out, Collision{Scope: kk.scope, Key: kk.k, Members: members})
	}
	return out, nil
}

func archiveDriftWhere(terminal []string, args *[]any) string {
	notTerm := inPred("status", terminal, args)
	isTerm := inPred("status", terminal, args)
	return `parse_error = 0 AND ((archived = 1 AND NOT (` + notTerm + `)) OR (archived = 0 AND ` + isTerm + `))`
}

// ArchiveDrift returns parseable tickets whose archive layout disagrees with
// terminal-ness. terminal is the schema-dependent name set (status.TerminalNames).
func (d *DB) ArchiveDrift(scope string, terminal []string) ([]*Ticket, error) {
	args := []any{scope}
	q := `SELECT ` + ticketColumns + ` FROM tickets WHERE scope = ? AND ` + archiveDriftWhere(terminal, &args)
	return d.queryTickets(q, args...)
}

// HasArchiveDrift reports whether any parseable ticket in the scope disagrees on archive layout.
func (d *DB) HasArchiveDrift(scope string, terminal []string) (bool, error) {
	args := []any{scope}
	q := `SELECT 1 FROM tickets WHERE scope = ? AND ` + archiveDriftWhere(terminal, &args) + ` LIMIT 1`
	var one int
	err := d.sql.QueryRow(q, args...).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// ParseErrorCount returns how many parse_error quarantine rows exist across scopes.
func (d *DB) ParseErrorCount(scopes []string) (int, error) {
	if len(scopes) == 0 {
		return 0, nil
	}
	placeholders, args := inClause(scopes)
	var n int
	err := d.sql.QueryRow(fmt.Sprintf(`SELECT COUNT(*) FROM tickets WHERE parse_error = 1 AND scope IN (%s)`, placeholders), args...).Scan(&n)
	return n, err
}

// DuplicateIDSet returns scope-qualified collision keys ("<scope>\x00<id>") for next's skip set.
func (d *DB) DuplicateIDSet(scopes []string) (map[string]bool, error) {
	cols, err := d.DuplicateIDs(scopes)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, c := range cols {
		out[c.Scope+"\x00"+c.Key] = true
	}
	return out, nil
}

func inClause(values []string) (string, []any) {
	sorted := append([]string(nil), values...)
	sort.Strings(sorted)
	marks := make([]string, len(sorted))
	args := make([]any, len(sorted))
	for i, v := range sorted {
		marks[i] = "?"
		args[i] = v
	}
	return strings.Join(marks, ", "), args
}

// inPred returns "col IN (?,?,...)" or "0" if values is empty, appending to args.
func inPred(col string, values []string, args *[]any) string {
	if len(values) == 0 {
		return "0"
	}
	ph, a := inClause(values)
	*args = append(*args, a...)
	return col + " IN (" + ph + ")"
}
