package notes

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/p3bot/tk/internal/atomicfile"
	"github.com/p3bot/tk/internal/scopefile"
)

// List returns addressable note slugs under notes/, alphabetical. A missing
// notes/ directory is empty success. Reserved names, invalid slugs, nested
// paths, and non-regular entries are omitted.
func List(scope, dir string) ([]string, error) {
	if err := RequireDir(scope, dir); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(dir, scopefile.NoteDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read notes dir: %w", err)
	}
	var slugs []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		stem, ok := strings.CutSuffix(e.Name(), ".md")
		if !ok || !scopefile.IsAddressableNoteSlug(stem) {
			continue
		}
		if !dirEntryRegular(e) {
			continue
		}
		slugs = append(slugs, stem)
	}
	sort.Strings(slugs)
	return slugs, nil
}

func dirEntryRegular(e os.DirEntry) bool {
	mode := e.Type()
	if mode == 0 {
		info, err := e.Info()
		if err != nil {
			return false
		}
		return info.Mode().IsRegular()
	}
	return mode.IsRegular()
}

// Read cats a note file. A missing path is MissingError. Empty files are empty
// success. Non-regular files refuse. Bare-default-as-empty is CLI policy.
func Read(in Input) (Result, error) {
	if err := RequireDir(in.Scope, in.Dir); err != nil {
		return Result{}, err
	}
	path := scopefile.NoteFile(in.Dir, in.Slug)
	out, err := resultPath(path, in.Slug)
	if err != nil {
		return Result{}, err
	}

	st, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Result{}, &MissingError{Path: out.Path}
		}
		return Result{}, fmt.Errorf("stat %s: %w", path, err)
	}
	if !st.Mode().IsRegular() {
		return Result{}, &NonRegularError{Path: path}
	}
	if st.Size() == 0 {
		return out, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Result{}, fmt.Errorf("read %s: %w", path, err)
	}
	out.Body = data
	return out, nil
}

// Add appends one line. Creates notes/ and the file if needed. If the file
// exists and does not end in a newline, a newline is written first. Never
// self-commits; a tk-driven scope may ride SyncNeeded dirty.
func Add(deps Deps, in Input, text string) (Result, error) {
	if text == "" {
		return Result{}, &UsageError{Msg: "add needs non-empty text"}
	}
	return mutate(deps, in, func(path string) error {
		if err := refuseNonRegular(path); err != nil {
			return err
		}
		existing, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("read %s: %w", path, err)
		}
		return atomicfile.Write(path, appendLine(existing, text), noteFileMode)
	})
}

func appendLine(existing []byte, text string) []byte {
	var buf []byte
	if len(existing) > 0 {
		buf = existing
		if existing[len(existing)-1] != '\n' {
			buf = append(buf, '\n')
		}
	}
	buf = append(buf, text...)
	buf = append(buf, '\n')
	return buf
}

// Set replaces the whole file. The file always ends with a newline. Empty
// payload is usage. Never self-commits; a tk-driven scope may ride SyncNeeded.
func Set(deps Deps, in Input, payload []byte) (Result, error) {
	data, err := normalizeContents(payload)
	if err != nil {
		return Result{}, err
	}
	return mutate(deps, in, func(path string) error {
		if err := refuseNonRegular(path); err != nil {
			return err
		}
		return atomicfile.Write(path, data, noteFileMode)
	})
}

func normalizeContents(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, &UsageError{Msg: "set needs non-empty text"}
	}
	if data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}
	return data, nil
}

// Delete unlinks a regular note file. Missing is success. An empty notes/
// directory is removed. Never self-commits; a tk-driven scope may ride
// SyncNeeded when a file was actually removed.
func Delete(deps Deps, in Input) (Result, error) {
	if err := RequireDir(in.Scope, in.Dir); err != nil {
		return Result{}, err
	}
	path := scopefile.NoteFile(in.Dir, in.Slug)
	removed := false
	if err := withLock(in.Dir, func() error {
		if err := deps.refuseMidRebase(in.Scope, in.Dir); err != nil {
			return err
		}
		st, err := os.Lstat(path)
		if err != nil {
			if os.IsNotExist(err) {
				rmdirNotesIfEmpty(in.Dir)
				return nil
			}
			return fmt.Errorf("stat %s: %w", path, err)
		}
		if !st.Mode().IsRegular() {
			return &NonRegularError{Path: path}
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove %s: %w", path, err)
		}
		rmdirNotesIfEmpty(in.Dir)
		removed = true
		return nil
	}); err != nil {
		return Result{}, err
	}
	out := Result{Slug: in.Slug}
	if removed {
		out.SyncNeeded = deps.syncNeeded(in.Scope, in.Dir)
	}
	return out, nil
}

func mutate(deps Deps, in Input, fn func(path string) error) (Result, error) {
	if err := RequireDir(in.Scope, in.Dir); err != nil {
		return Result{}, err
	}
	path := scopefile.NoteFile(in.Dir, in.Slug)
	if err := withLock(in.Dir, func() error {
		if err := deps.refuseMidRebase(in.Scope, in.Dir); err != nil {
			return err
		}
		return fn(path)
	}); err != nil {
		return Result{}, err
	}
	out, err := resultPath(path, in.Slug)
	if err != nil {
		return Result{}, err
	}
	out.SyncNeeded = deps.syncNeeded(in.Scope, in.Dir)
	return out, nil
}

// PrepareEdit creates notes/ if needed and refuses a non-regular path. It does
// not create the file and does not refuse mid-rebase.
func PrepareEdit(in Input) (Result, error) {
	if err := RequireDir(in.Scope, in.Dir); err != nil {
		return Result{}, err
	}
	path := scopefile.NoteFile(in.Dir, in.Slug)
	notesDir := filepath.Join(in.Dir, scopefile.NoteDir)
	if err := withLock(in.Dir, func() error {
		if err := os.MkdirAll(notesDir, 0o755); err != nil {
			return fmt.Errorf("create notes dir: %w", err)
		}
		return refuseNonRegular(path)
	}); err != nil {
		return Result{}, err
	}
	return resultPath(path, in.Slug)
}

// FinishEdit removes a zero-byte file left by the editor and an empty notes/.
func FinishEdit(dir, path string) error {
	return withLock(dir, func() error {
		return cleanupEmpty(dir, path)
	})
}
