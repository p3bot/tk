// Package notes is cobra-free scope note file I/O and the machine-local use
// pointer. Callers map structured results to process edges; this package does
// not import cobra, internal/cli, or internal/tkv.
package notes

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"cuelang.org/go/cue"

	"github.com/p3bot/tk/internal/gitstate"
	"github.com/p3bot/tk/internal/pathutil"
	"github.com/p3bot/tk/internal/reconcile"
	"github.com/p3bot/tk/internal/registry"
	"github.com/p3bot/tk/internal/scopeconfig"
	"github.com/p3bot/tk/internal/scopefile"
	"github.com/p3bot/tk/internal/token"
)

const noteFileMode = 0o644

// Deps are machine-local services note operations need.
type Deps struct {
	Ctx       context.Context
	Cue       *cue.Context
	StateDir  string
	ConfigDir string
	Reg       *registry.Registry
	Rec       *reconcile.Reconciler
}

// Selector is a one-shot slug choice: positional, --name, or the stored default.
// Resolve it at the edge with ResolveName; file operations take Input.Slug.
type Selector struct {
	Positional string
	Name       string
	NameSet    bool
}

// Input is one note file operation after identity is resolved at the edge.
type Input struct {
	Scope string
	Dir   string
	Slug  string
	// Base is the clobber predicate from GET; empty skips the check.
	Base string
}

// Result is the structured outcome of a note operation. Path is empty when the
// verb prints nothing. Adapters map fields to stdout/stderr; they do not parse
// tokens out of Error() text except for typed failures.
type Result struct {
	Path       string
	Slug       string
	Body       []byte
	SyncNeeded string
}

// UsageError is argv-class policy the CLI adapter maps to exit 2.
type UsageError struct {
	Msg string
}

func (e *UsageError) Error() string { return e.Msg }

// MissingError is a named note path that does not exist.
type MissingError struct {
	Path string
}

func (e *MissingError) Error() string { return e.Path + " does not exist" }

// NonRegularError refuses a path that is not a regular file.
type NonRegularError struct {
	Path string
}

func (e *NonRegularError) Error() string { return e.Path + " is not a regular file" }

// ClobberError is a stale editor predicate: the file changed, no write.
type ClobberError struct {
	Slug string
}

func (e *ClobberError) Error() string {
	return fmt.Sprintf("%s changed since this form was rendered — reload and save again", e.Slug)
}

// MissingClobberKey is FileClobberKey when notes/<slug>.md does not exist.
const MissingClobberKey = "0:missing"

// RequireDir refuses when the scope directory is missing or not a directory.
func RequireDir(scope, dir string) error {
	st, err := os.Lstat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%s", token.Line(token.UnreachableScope,
				fmt.Sprintf("%s: dir %s is not reachable", scope, dir)))
		}
		return fmt.Errorf("stat %s: %w", dir, err)
	}
	if !st.IsDir() {
		return fmt.Errorf("%s", token.Line(token.UnreachableScope,
			fmt.Sprintf("%s: dir %s is not reachable", scope, dir)))
	}
	return nil
}

func (d Deps) ctx() context.Context {
	if d.Ctx != nil {
		return d.Ctx
	}
	return context.Background()
}

func absPath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path for %q: %w", p, err)
	}
	return pathutil.Canonical(abs), nil
}

func withLock(dir string, fn func() error) error {
	lock, err := scopefile.AcquireLock(dir)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()
	return fn()
}

func refuseNonRegular(path string) error {
	st, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if !st.Mode().IsRegular() {
		return &NonRegularError{Path: path}
	}
	return nil
}

func cleanupEmpty(dir, path string) error {
	st, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			rmdirNotesIfEmpty(dir)
			return nil
		}
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if st.Mode().IsRegular() && st.Size() == 0 {
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove empty %s: %w", path, err)
		}
	}
	rmdirNotesIfEmpty(dir)
	return nil
}

func rmdirNotesIfEmpty(dir string) {
	p := filepath.Join(dir, scopefile.NoteDir)
	st, err := os.Lstat(p)
	if err != nil || !st.IsDir() {
		return
	}
	_ = os.Remove(p)
}

// Quiet when tk.cue is unusable (notes stay usable) or the scope is not tk-driven.
func (d Deps) refuseMidRebase(scope, dir string) error {
	root, hasRoot := scopefile.GitRoot(dir)
	schema, cfgErr := d.Rec.SchemaOrError(scope, dir)
	if cfgErr != nil {
		return nil
	}
	return gitstate.CheckMidRebase(d.ctx(), scope, scopeconfig.SchemaAutoCommit(schema), root, hasRoot)
}

func (d Deps) syncNeeded(scope, dir string) string {
	root, hasRoot := scopefile.GitRoot(dir)
	if !hasRoot {
		return ""
	}
	schema, cfgErr := d.Rec.SchemaOrError(scope, dir)
	if cfgErr != nil || !scopeconfig.SchemaAutoCommit(schema) {
		return ""
	}
	return gitstate.SyncNeededReason(d.ctx(), d.StateDir, dir, root)
}

func resultPath(path, slug string) (Result, error) {
	abs, err := absPath(path)
	if err != nil {
		return Result{}, err
	}
	return Result{Path: abs, Slug: slug}, nil
}
