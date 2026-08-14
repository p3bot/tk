package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/p3bot/tk/internal/atomicfile"
	"github.com/p3bot/tk/internal/frontmatter"
	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/order"
	"github.com/p3bot/tk/internal/scopeconfig"
	"github.com/p3bot/tk/internal/token"
)

const ticketFileMode = 0o644

func atomicWrite(path string, data []byte) error {
	return atomicfile.Write(path, data, ticketFileMode)
}

func single(scope, dir string) map[string]string {
	return map[string]string{scope: dir}
}

// writtenPaths includes the removed old path so SyncPaths deletes its row.
func writtenPaths(newPath, oldPath string) []string {
	if oldPath == "" || oldPath == newPath {
		return []string{newPath}
	}
	return []string{newPath, oldPath}
}

// schemaAutoCommit: nil schema is false; writers refuse unusable config first.
func schemaAutoCommit(s *scopeconfig.Schema) bool {
	return s != nil && s.AutoCommit
}

// maxValidOrder: "" means empty board (open KeyBetween bound); skips invalid/quarantine.
func maxValidOrder(rows []*index.Ticket) string {
	best := ""
	for _, p := range rows {
		if p.ParseError || !order.Valid(p.OrderKey) {
			continue
		}
		if best == "" || p.OrderKey > best {
			best = p.OrderKey
		}
	}
	return best
}

// readTicketFile: parse failure here is a mid-write race (quarantine refused upstream).
func readTicketFile(path string) (*frontmatter.Model, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", path, err)
	}
	interior, body, present := frontmatter.Split(data)
	if !present {
		return nil, nil, fmt.Errorf("%s has no frontmatter fence", path)
	}
	m, err := frontmatter.Parse(interior)
	if err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return m, body, nil
}

func writeTicketFile(path string, m *frontmatter.Model, body []byte) error {
	interior, err := frontmatter.Serialize(m)
	if err != nil {
		return err
	}
	return atomicWrite(path, frontmatter.Compose(interior, body))
}

// resolveSingleRow: 0 → unknown (noun-worded); >1 → duplicate_id; no row-level policy.
func (e *engine) resolveSingleRow(scope, idArg string, form idForm, noun string) (*index.Ticket, error) {
	lookupArg, lookupForm, err := e.expandReservedID(scope, idArg, form)
	if err != nil {
		return nil, err
	}
	var rows []*index.Ticket
	if lookupForm == idFull {
		rows, err = e.db.TicketsByID(scope, lookupArg)
	} else {
		rows, err = e.db.TicketsByShortID(scope, lookupArg)
	}
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("unknown %s id %q", noun, idArg)
	}
	if len(rows) > 1 {
		return nil, duplicateRefusal(rows)
	}
	return rows[0], nil
}

// resolveWriteRow also refuses parse_error quarantine.
func (e *engine) resolveWriteRow(scope, idArg string, form idForm) (*index.Ticket, error) {
	p, err := e.resolveSingleRow(scope, idArg, form, "ticket")
	if err != nil {
		return nil, err
	}
	if p.ParseError {
		return nil, fmt.Errorf("%s", token.Line(token.ParseError,
			fmt.Sprintf("%s: %s — cannot rewrite quarantined frontmatter", p.ID, p.ParseMsg)))
	}
	return p, nil
}

// terminalLocation relocates only (basename unchanged).
func terminalLocation(dir, base string, terminal bool) (string, error) {
	if !terminal {
		return filepath.Join(dir, base), nil
	}
	archiveDir := filepath.Join(dir, "archive")
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		return "", fmt.Errorf("create archive dir: %w", err)
	}
	return filepath.Join(archiveDir, base), nil
}
