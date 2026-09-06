package index

import (
	"context"
	"fmt"
	"strings"
)

// QueryResult is the tabular result of a read-only tk query (cells as strings for TSV).
type QueryResult struct {
	Columns []string
	Rows    [][]string
}

// RunReadOnlyQuery executes ad-hoc SQL after proving it is read-only. The static
// classifier is a friendly first pass; PRAGMA query_only is the authority (CTE-hidden
// writes, function-form pragmas).
func (d *DB) RunReadOnlyQuery(sqlText string) (*QueryResult, error) {
	if err := ensureReadOnly(sqlText); err != nil {
		return nil, err
	}

	ctx := context.Background()
	conn, err := d.sql.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, `PRAGMA query_only = ON`); err != nil {
		return nil, fmt.Errorf("arm read-only guard: %w", err)
	}
	// Reset before the connection returns to the pool so later writers are not frozen out.
	defer func() { _, _ = conn.ExecContext(ctx, `PRAGMA query_only = OFF`) }()

	rows, err := conn.QueryContext(ctx, sqlText)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	res := &QueryResult{Columns: cols}
	for rows.Next() {
		cells := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range cells {
			ptrs[i] = &cells[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		out := make([]string, len(cols))
		for i, c := range cells {
			out[i] = renderCell(c)
		}
		res.Rows = append(res.Rows, out)
	}
	return res, rows.Err()
}

// writeVerbs are leading keywords that mutate the store or schema.
var writeVerbs = map[string]bool{
	"INSERT": true, "UPDATE": true, "DELETE": true, "REPLACE": true,
	"DROP": true, "ALTER": true, "CREATE": true, "TRUNCATE": true,
	"ATTACH": true, "DETACH": true, "REINDEX": true, "VACUUM": true,
	"BEGIN": true, "COMMIT": true, "SAVEPOINT": true, "RELEASE": true,
}

// ensureReadOnly admits SELECT/WITH/VALUES, EXPLAIN, and non-assignment PRAGMA; splits on ';'.
func ensureReadOnly(sqlText string) error {
	statements := splitStatements(sqlText)
	if len(statements) == 0 {
		return fmt.Errorf("empty query")
	}
	for _, stmt := range statements {
		lead := leadingKeyword(stmt)
		switch lead {
		case "SELECT", "WITH", "VALUES":
		case "EXPLAIN":
		case "PRAGMA":
			if strings.ContainsRune(stmt, '=') {
				return readOnlyRefusal("a PRAGMA that sets a value")
			}
		default:
			if writeVerbs[lead] {
				return readOnlyRefusal(strings.ToLower(lead))
			}
			return readOnlyRefusal("a non-read-only statement")
		}
	}
	return nil
}

func readOnlyRefusal(what string) error {
	return fmt.Errorf("tk query is read-only: refusing %s — the index is a derived cache; durable change is the ticket files or tk repair, not the DB", what)
}

// splitStatements is literal-aware: ';' inside '…' or "…" is not a separator;
// doubled quotes net out so escapes never open a phantom separator.
func splitStatements(sqlText string) []string {
	var out []string
	var b strings.Builder
	var inSingle, inDouble bool
	flush := func() {
		if strings.TrimSpace(b.String()) != "" {
			out = append(out, b.String())
		}
		b.Reset()
	}
	for i := 0; i < len(sqlText); i++ {
		c := sqlText[i]
		switch {
		case c == '\'' && !inDouble:
			inSingle = !inSingle
		case c == '"' && !inSingle:
			inDouble = !inDouble
		case c == ';' && !inSingle && !inDouble:
			flush()
			continue
		}
		b.WriteByte(c)
	}
	flush()
	return out
}

func leadingKeyword(stmt string) string {
	s := stripLeading(stmt)
	i := 0
	for i < len(s) && (isWordByte(s[i])) {
		i++
	}
	return strings.ToUpper(s[:i])
}

func stripLeading(s string) string {
	for {
		s = strings.TrimLeft(s, " \t\r\n")
		switch {
		case strings.HasPrefix(s, "--"):
			if nl := strings.IndexByte(s, '\n'); nl >= 0 {
				s = s[nl+1:]
			} else {
				return ""
			}
		case strings.HasPrefix(s, "/*"):
			if end := strings.Index(s, "*/"); end >= 0 {
				s = s[end+2:]
			} else {
				return ""
			}
		default:
			return s
		}
	}
}

func isWordByte(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

func renderCell(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case []byte:
		return string(t)
	case string:
		return t
	default:
		return fmt.Sprint(t)
	}
}
