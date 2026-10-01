package scopefile

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/p3bot/tk/internal/id"
)

// PrefixMismatch is a ticket-shaped file at the scope root, under archive/, or
// under design/ whose filename id prefix is not the scope name. The allowlist
// accepts any legal prefix, and the indexer accepts only this scope's, so the
// file is otherwise invisible: not a ticket, not a design, and not residue.
type PrefixMismatch struct {
	Path string
	ID   string
}

// PrefixMismatches lists those files, sorted by path. A missing archive/ or
// design/, or a file parked at either name, is empty. A missing scope dir is
// an error.
func PrefixMismatches(dir, scope string) ([]PrefixMismatch, error) {
	var out []PrefixMismatch
	roots := []string{dir, filepath.Join(dir, "archive"), filepath.Join(dir, DesignDir)}
	for i, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			if i > 0 && OptionalDirMiss(root) {
				continue
			}
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			full, ok := TicketIDFromBase(e.Name())
			if !ok || id.ScopeOfFullID(full) == scope {
				continue
			}
			out = append(out, PrefixMismatch{Path: filepath.Join(root, e.Name()), ID: full})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}
