// Package atomicfile writes a file via same-directory temp + rename so readers
// never see a half-written file and an interrupted write cannot truncate an
// existing one. Same-directory keeps rename atomic on one filesystem.
//
// Shared write primitive for the CUE registry, scope tk.cue authoring,
// scope .gitignore, and scope notes.
package atomicfile

import (
	"fmt"
	"os"
	"path/filepath"
)

// Write atomically writes data to path with the given mode, creating parents if
// needed. Mode is set on the temp file explicitly (independent of umask); on
// failure the temp is removed and path is left untouched.
func Write(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temp for %s: %w", path, err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp for %s: %w", path, err)
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp for %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp for %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("install %s: %w", path, err)
	}
	return nil
}
