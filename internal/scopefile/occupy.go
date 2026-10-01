package scopefile

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/p3bot/tk/internal/frontmatter"
	"github.com/p3bot/tk/internal/id"
)

// DesignDir is the single-level directory of scope design documents.
const DesignDir = "design"

// DesignFullID reports the full id when path is dir/design/<id>-<slug>.md.
func DesignFullID(path, dir string) (string, bool) {
	rel, err := filepath.Rel(dir, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return "", false
	}
	if filepath.Dir(rel) != DesignDir {
		return "", false
	}
	return TicketIDFromBase(filepath.Base(rel))
}

// ShortIDOfBasename returns the short id of a <scope>-<short>[-slug].md name.
// The slug is not validated: a residue name still holds its short id.
func ShortIDOfBasename(base, scope string) (string, bool) {
	full, ok := fullIDOfBasename(base, scope)
	if !ok {
		return "", false
	}
	return strings.TrimPrefix(full, scope+"-"), true
}

func fullIDOfBasename(base, scope string) (string, bool) {
	stem, ok := strings.CutSuffix(base, ".md")
	if !ok {
		return "", false
	}
	prefix := scope + "-"
	if !strings.HasPrefix(stem, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(stem, prefix)
	short := rest
	if i := strings.IndexByte(rest, '-'); i >= 0 {
		short = rest[:i]
	}
	if !id.IsShortID(short) {
		return "", false
	}
	return scope + "-" + short, true
}

// OccupiedShortIDs is the on-disk short-id set for one scope: ticket files at
// the root and under archive/, plus design files under design/. Each file
// contributes its filename short id and, when the fence parses, a same-scope
// fence id. Untracked files count. A missing archive/ or design/, or a file
// parked at either name, is empty.
func OccupiedShortIDs(dir, scope string) (map[string]struct{}, error) {
	paths, err := OccupiedShortPaths(dir, scope)
	if err != nil {
		return nil, err
	}
	out := make(map[string]struct{}, len(paths))
	for short := range paths {
		out[short] = struct{}{}
	}
	return out, nil
}

// OccupiedShortPaths maps each occupied short id to one holding path (the
// lexicographically smallest). Callers that mint an extension use the set of
// keys; the path is for collision-repair resume.
func OccupiedShortPaths(dir, scope string) (map[string]string, error) {
	out := map[string]string{}
	roots := []struct {
		path   string
		design bool
	}{
		{dir, false},
		{filepath.Join(dir, "archive"), false},
		{filepath.Join(dir, DesignDir), true},
	}
	for _, root := range roots {
		optional := root.path != dir
		if err := occupyDir(out, scope, root.path, root.design, optional); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func occupyDir(out map[string]string, scope, root string, design, optional bool) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) || (optional && OptionalDirMiss(root)) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		// Root and archive/ follow the ticket scan: a ticket basename only.
		// design/ follows design.Files: a well-formed design name in this scope.
		// Anything else is not opened.
		if design {
			if !LooksLikeTicket(e.Name()) {
				continue
			}
			if _, ok := ShortIDOfBasename(e.Name(), scope); !ok {
				continue
			}
		} else if _, ok := ShortIDOfBasename(e.Name(), scope); !ok {
			continue
		}
		path := filepath.Join(root, e.Name())
		if err := occupyFile(out, scope, path, e.Name()); err != nil {
			return err
		}
	}
	return nil
}

func occupyFile(out map[string]string, scope, path, base string) error {
	if short, ok := ShortIDOfBasename(base, scope); ok {
		hold(out, short, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	interior, _, present := frontmatter.Split(data)
	if !present {
		return nil
	}
	m, err := frontmatter.Parse(interior)
	if err != nil {
		return nil
	}
	if id.IsFullTicketID(m.ID) && id.ScopeOfFullID(m.ID) == scope {
		hold(out, strings.TrimPrefix(m.ID, scope+"-"), path)
	}
	return nil
}

func hold(out map[string]string, short, path string) {
	if prev, ok := out[short]; !ok || path < prev {
		out[short] = path
	}
}
