// Package resolve implements ambient scope resolution. Precedence is
// --scope > TK_SCOPE > longest-prefix code-root, against the registry only —
// never a filesystem probe for an unregistered tk.cue. An explicit override
// naming an unregistered scope fails closed; a resolved scope whose registry
// key disagrees with on-disk tk.cue name is name-drift.
package resolve

import (
	"errors"
	"fmt"

	"cuelang.org/go/cue"
	"github.com/p3bot/tk/internal/gitroot"
	"github.com/p3bot/tk/internal/pathutil"
	"github.com/p3bot/tk/internal/registry"
	"github.com/p3bot/tk/internal/scopeconfig"
	"github.com/p3bot/tk/internal/token"
)

// How a scope was chosen — closed labels for the status dashboard's `resolved` field.
const (
	SourceFlag = "flag" // --scope
	SourceEnv  = "env"  // TK_SCOPE
	SourceCwd  = "cwd"  // longest-prefix code-root of cwd
)

// Resolved is a successfully resolved ambient scope.
type Resolved struct {
	Name   string
	Entry  registry.Entry
	Source string // SourceFlag, SourceEnv, or SourceCwd
}

// Options carries ambient inputs, read once at the CLI edge so resolution is pure.
type Options struct {
	// ScopeFlag is the --scope override (highest precedence). Empty when unset.
	ScopeFlag string
	// EnvScope is the TK_SCOPE override. Empty when unset.
	EnvScope string
	// Cwd must be canonical (absolute, symlink-resolved) to match stored roots.
	Cwd string
}

// ErrNoScope is returned when no override is set and no code-root matches cwd.
var ErrNoScope = errors.New("no ambient scope: pass --scope <name>, set TK_SCOPE, or run inside a registered code-root (see tk scope init/import)")

// UnknownScopeError is returned when an explicit override names an unregistered scope.
type UnknownScopeError struct {
	Name string
}

func (e *UnknownScopeError) Error() string {
	return fmt.Sprintf("unknown scope: %q is not registered (see tk scope import)", e.Name)
}

// DriftError is returned when a resolved scope's registry key disagrees with on-disk tk.cue name.
type DriftError struct {
	Key     string
	CueName string
	Dir     string
	line    string
}

func (e *DriftError) Error() string { return e.line }

// Resolve resolves the ambient scope by the precedence chain.
func Resolve(ctx *cue.Context, reg *registry.Registry, opts Options) (*Resolved, error) {
	if opts.ScopeFlag != "" {
		entry, ok := reg.Scopes[opts.ScopeFlag]
		if !ok {
			return nil, &UnknownScopeError{Name: opts.ScopeFlag}
		}
		return resolvedOrDrift(ctx, opts.ScopeFlag, entry, SourceFlag)
	}
	if opts.EnvScope != "" {
		entry, ok := reg.Scopes[opts.EnvScope]
		if !ok {
			return nil, &UnknownScopeError{Name: opts.EnvScope}
		}
		return resolvedOrDrift(ctx, opts.EnvScope, entry, SourceEnv)
	}

	name, entry, ok := longestPrefix(reg, opts.Cwd)
	if !ok {
		return nil, ErrNoScope
	}
	return resolvedOrDrift(ctx, name, entry, SourceCwd)
}

// longestPrefix returns the registered scope whose code-root is the longest prefix of cwd.
func longestPrefix(reg *registry.Registry, cwd string) (string, registry.Entry, bool) {
	var bestName string
	var bestEntry registry.Entry
	bestLen := -1
	for name, entry := range reg.Scopes {
		if pathutil.UnderOrEqual(cwd, entry.Root) && len(entry.Root) > bestLen {
			bestName, bestEntry, bestLen = name, entry, len(entry.Root)
		}
	}
	if bestLen < 0 {
		return "", registry.Entry{}, false
	}
	return bestName, bestEntry, true
}

// CheckName reports name-drift when the registry key disagrees with on-disk
// tk.cue name. Unreadable tk.cue is not drift (reads stay available).
func CheckName(ctx *cue.Context, name string, entry registry.Entry) error {
	_, err := resolvedOrDrift(ctx, name, entry, SourceFlag)
	return err
}

// resolvedOrDrift returns the scope unless on-disk name disagrees with the registry key.
// Unreadable tk.cue still resolves (reads stay available; write path gates config).
func resolvedOrDrift(ctx *cue.Context, name string, entry registry.Entry, source string) (*Resolved, error) {
	cueName, err := scopeconfig.ReadName(ctx, entry.Dir)
	if err == nil && cueName != name {
		return nil, &DriftError{
			Key:     name,
			CueName: cueName,
			Dir:     entry.Dir,
			line:    DriftLine(name, cueName, entry.Dir, SuggestCodeRoot(entry.Dir, entry.Root)),
		}
	}
	return &Resolved{Name: name, Entry: entry, Source: source}, nil
}

// DriftLine formats the name_drift stderr line with forget+import recovery.
// Shared by the resolver and scope list so recovery wording never forks.
func DriftLine(key, cueName, dir, codeRoot string) string {
	rec := fmt.Sprintf("tk scope forget %s && tk scope import %s", key, dir)
	if codeRoot != "" {
		rec += " --code-root " + codeRoot
	}
	return token.Line(token.NameDrift, fmt.Sprintf("registry key %q but tk.cue name is %q at %s — run: %s", key, cueName, dir, rec))
}

// SuggestCodeRoot returns the code-root for a recovery suggestion, or "" when root is the default for dir.
func SuggestCodeRoot(dir, root string) string {
	gitRoot, inRepo := gitroot.RepoRoot(dir)
	return SuggestCodeRootWith(dir, root, gitRoot, inRepo)
}

// SuggestCodeRootWith is SuggestCodeRoot over a pre-derived git-root.
func SuggestCodeRootWith(dir, root, gitRoot string, inRepo bool) string {
	def := dir
	if inRepo {
		def = gitRoot
	}
	if root == def {
		return ""
	}
	return root
}
