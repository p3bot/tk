// Package scopefile holds scope-directory file policy shared by write verbs,
// doctor, and sync: allowlist classification, dirty counting, and the per-scope
// flock path. One definition so snapshot, uncommitted:, non_allowlist:, and
// pulse stay on the same product rule.
package scopefile

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/p3bot/tk/internal/flock"
	"github.com/p3bot/tk/internal/git"
	"github.com/p3bot/tk/internal/gitroot"
	"github.com/p3bot/tk/internal/id"
	"github.com/p3bot/tk/internal/slug"
)

// LockName is the per-scope advisory lock file at the scope dir root.
const LockName = ".tk.lock"

// AcquireLock takes the exclusive per-scope flock at dir/LockName.
// The scope directory must already exist.
func AcquireLock(dir string) (*flock.Lock, error) {
	return flock.Acquire(filepath.Join(dir, LockName))
}

// GitRoot resolves the enclosing git repository once so durability helpers agree.
// Returns ok=false when git is unavailable or dir is outside any repo.
func GitRoot(dir string) (root string, ok bool) {
	if !git.Available() {
		return "", false
	}
	return gitroot.RepoRoot(dir)
}

// CountAllowlistedDirty counts dirty paths under dir that pass IsAllowlisted.
// Returns 0 when there is no git root or DirtyPaths fails.
func CountAllowlistedDirty(ctx context.Context, dir, root string, hasRoot bool) int {
	if !hasRoot {
		return 0
	}
	dirty, err := git.DirtyPaths(ctx, root, dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, p := range dirty {
		if IsAllowlisted(p, dir) {
			n++
		}
	}
	return n
}

// NoteDir is the single-level directory of scope worklog documents.
const NoteDir = "notes"

// NoteDefaultSlug is the built-in fallback document when no machine-local
// default is set (notes/default.md).
const NoteDefaultSlug = "default"

// IsReservedNoteName reports whether name is a reserved note verb or cobra
// command (help) and must never be a document.
func IsReservedNoteName(name string) bool {
	switch name {
	case "list", "ls", "add", "set", "edit", "remove", "rm", "help", "use":
		return true
	default:
		return false
	}
}

// IsAddressableNoteSlug reports whether name may be a notes/<name>.md document.
func IsAddressableNoteSlug(name string) bool {
	return slug.Valid(name) && !IsReservedNoteName(name)
}

// NoteFile is the path of notes/<name>.md under dir. name is not validated.
func NoteFile(dir, name string) string {
	return filepath.Join(dir, NoteDir, name+".md")
}

// NoteSlug reports the addressable slug if path is dir/notes/<slug>.md (one level).
func NoteSlug(path, dir string) (string, bool) {
	rel, err := filepath.Rel(dir, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return "", false
	}
	if filepath.Dir(rel) != NoteDir {
		return "", false
	}
	stem, ok := strings.CutSuffix(filepath.Base(rel), ".md")
	if !ok || !IsAddressableNoteSlug(stem) {
		return "", false
	}
	return stem, true
}

// IsAllowlisted reports whether path is a ticket .md at dir root or archive/,
// tk.cue/.gitignore at root, notes/<addressable-slug>.md, or a design file at
// design/<id>-<slug>.md (one level). Nested paths and other names stay residue.
func IsAllowlisted(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return false
	}
	base := filepath.Base(rel)
	switch filepath.Dir(rel) {
	case ".":
		return base == "tk.cue" || base == ".gitignore" || LooksLikeTicket(base)
	case "archive":
		return LooksLikeTicket(base)
	case NoteDir:
		stem, ok := strings.CutSuffix(base, ".md")
		return ok && IsAddressableNoteSlug(stem)
	case DesignDir:
		return LooksLikeTicket(base)
	default:
		return false
	}
}

// LooksLikeTicket reports whether base is a ticket filename: full-id stem
// with optional valid slug tail.
func LooksLikeTicket(base string) bool {
	_, ok := TicketIDFromBase(base)
	return ok
}

// TicketIDFromBase extracts the full id from an allowlisted ticket filename.
func TicketIDFromBase(base string) (string, bool) {
	stem, ok := strings.CutSuffix(base, ".md")
	if !ok {
		return "", false
	}
	parts := strings.SplitN(stem, "-", 3)
	if len(parts) < 2 {
		return "", false
	}
	full := parts[0] + "-" + parts[1]
	if !id.IsFullTicketID(full) {
		return "", false
	}
	if len(parts) == 3 && !slug.Valid(parts[2]) {
		return "", false
	}
	return full, true
}

// OptionalDirMiss reports whether path cannot be scanned as an optional
// directory. archive/ and design/ are optional: missing, or a file parked at
// that name, is an empty root. Any other error, including permission, is not
// a miss — the caller returns it.
func OptionalDirMiss(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return os.IsNotExist(err)
	}
	return !info.IsDir()
}

// ListTickets returns allowlisted ticket markdown at dir root and archive/,
// sorted. A missing archive/, or a file parked at that name, is empty.
// Nested residue and notes are omitted.
func ListTickets(dir string) ([]string, error) {
	var out []string
	if err := collectTickets(dir, dir, &out); err != nil {
		return nil, err
	}
	arch := filepath.Join(dir, "archive")
	if err := collectTickets(dir, arch, &out); err != nil && !OptionalDirMiss(arch) {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

func collectTickets(scopeDir, root string, out *[]string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(root, e.Name())
		if !IsAllowlisted(path, scopeDir) || !LooksLikeTicket(e.Name()) {
			continue
		}
		*out = append(*out, path)
	}
	return nil
}
