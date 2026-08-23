package cli

import (
	"github.com/p3bot/tk/internal/frontmatter"
	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/scopeconfig"
	"github.com/p3bot/tk/internal/writeengine"
)

func atomicWrite(path string, data []byte) error {
	return writeengine.AtomicWrite(path, data)
}

func single(scope, dir string) map[string]string {
	return map[string]string{scope: dir}
}

func writtenPaths(newPath, oldPath string) []string {
	return writeengine.WrittenPaths(newPath, oldPath)
}

func schemaAutoCommit(s *scopeconfig.Schema) bool {
	return writeengine.SchemaAutoCommit(s)
}

func maxValidOrder(rows []*index.Ticket) string {
	return writeengine.MaxValidOrder(rows)
}

func readTicketFile(path string) (*frontmatter.Model, []byte, error) {
	return writeengine.ReadTicketFile(path)
}

func writeTicketFile(path string, m *frontmatter.Model, body []byte) error {
	return writeengine.WriteTicketFile(path, m, body)
}

func (e *engine) resolveSingleRow(scope, idArg string, form idForm, noun string) (*index.Ticket, error) {
	lu, err := e.writeLookup(scope, idArg, form)
	if err != nil {
		return nil, err
	}
	return writeengine.ResolveRow(e.db, scope, lu, noun)
}

func (e *engine) resolveWriteRow(scope, idArg string, form idForm) (*index.Ticket, error) {
	lu, err := e.writeLookup(scope, idArg, form)
	if err != nil {
		return nil, err
	}
	return writeengine.ResolveWriteRow(e.db, scope, lu)
}

func terminalLocation(dir, base string, terminal bool) (string, error) {
	return writeengine.TerminalLocation(dir, base, terminal)
}
