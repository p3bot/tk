package reconcile

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/p3bot/tk/internal/id"
)

// archiveDir is the only subdirectory reconcile scans (immediate children only).
// design/ and notes/ are scope-owned and stay out of the ticket index.
const archiveDir = "archive"

type statEntry struct {
	FullID   string
	Archived bool
	MtimeNS  int64
	Size     int64
}

// statScope lists ticket files at the dir root and archive/ children. reachable
// false means the dir cannot be listed (rows stay; unreachable_scope). Missing archive/ is normal.
func statScope(scope, dir string) (files map[string]statEntry, reachable bool) {
	files = map[string]statEntry{}
	if !collectDir(scope, dir, false, files) {
		return nil, false
	}
	// archive/ optional; failure there does not mark the whole scope unreachable.
	collectDir(scope, filepath.Join(dir, archiveDir), true, files)
	return files, true
}

// collectDir adds ticket files under root. ok is false only when root itself cannot be read.
func collectDir(scope, root string, archived bool, files map[string]statEntry) bool {
	entries, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		fullID, ok := ticketID(scope, e.Name())
		if !ok {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		path := filepath.Join(root, e.Name())
		files[path] = statEntry{
			FullID:   fullID,
			Archived: archived,
			MtimeNS:  fi.ModTime().UnixNano(),
			Size:     fi.Size(),
		}
	}
	return true
}

// ticketID extracts <scope>-<short-id> from <scope>-<short-id>[-slug].md so
// parse_error files remain locatable from the filename alone.
func ticketID(scope, name string) (string, bool) {
	rest, ok := strings.CutSuffix(name, ".md")
	if !ok {
		return "", false
	}
	prefix := scope + "-"
	if !strings.HasPrefix(rest, prefix) {
		return "", false
	}
	tail := rest[len(prefix):]
	short := tail
	if i := strings.IndexByte(tail, '-'); i >= 0 {
		short = tail[:i]
	}
	if !id.IsShortID(short) {
		return "", false
	}
	return scope + "-" + short, true
}
