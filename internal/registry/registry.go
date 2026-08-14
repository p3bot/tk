// Package registry is the machine-local XDG config tier: which scopes are
// registered, at which paths, plus the per-scope lens and current-ticket
// pointer. Reads/writes use the CUE Go modules only; owned files are
// regenerated wholesale and installed by atomic same-directory rename. An
// unparseable XDG file is a hard error (nothing to degrade to). Callers hold
// the machine-global flock; this package does not lock.
package registry

import (
	"fmt"
	"os"
	"path/filepath"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/format"

	"github.com/p3bot/tk/internal/atomicfile"
	"github.com/p3bot/tk/internal/pathutil"
)

const (
	registryFile = "registry.cue"
	lensFile     = "lens.cue"
	meFile       = "me.cue"
)

// Entry is one scope's registration: two independent absolute paths (git repo is derived from Dir).
// After Load, Dir and Root are canonical (symlink-resolved); they may differ from the
// spellings still stored in registry.cue until the next WriteRegistry.
type Entry struct {
	// Dir is where the scope's .md files and tk.cue live.
	Dir string `json:"dir"`
	// Root is the code-root under which the scope is ambient.
	Root string `json:"root"`
}

// Registry is the loaded XDG config tier, keyed by scope name.
type Registry struct {
	Scopes map[string]Entry
	Lens   map[string][]string
	// Me is the per-scope current-ticket pointer: one full ticket id, or absent.
	Me map[string]string
}

// Store performs CUE I/O for the XDG config tier under a fixed directory.
type Store struct {
	ctx *cue.Context
	dir string
}

// NewStore builds a Store over configDir using the process-wide CUE context.
func NewStore(ctx *cue.Context, configDir string) *Store {
	return &Store{ctx: ctx, dir: configDir}
}

// Load reads registry.cue, lens.cue, and me.cue. Missing files yield empty
// sections; uncompilable files hard-error. Scope Dir/Root are returned
// canonical for path matching; Load never rewrites the file (list may show
// physical paths while registry.cue still has a pre-heal spelling).
func (s *Store) Load() (*Registry, error) {
	reg := &Registry{Scopes: map[string]Entry{}, Lens: map[string][]string{}, Me: map[string]string{}}

	if v, ok, err := s.compileFile(registryFile); err != nil {
		return nil, err
	} else if ok {
		var rc struct {
			Scopes map[string]Entry `json:"scopes"`
		}
		if err := v.Decode(&rc); err != nil {
			return nil, fmt.Errorf("%s is malformed: %w", filepath.Join(s.dir, registryFile), err)
		}
		if rc.Scopes != nil {
			reg.Scopes = make(map[string]Entry, len(rc.Scopes))
			for name, e := range rc.Scopes {
				e.Dir = pathutil.Canonical(e.Dir)
				e.Root = pathutil.Canonical(e.Root)
				reg.Scopes[name] = e
			}
		}
	}

	if v, ok, err := s.compileFile(lensFile); err != nil {
		return nil, err
	} else if ok {
		var lc struct {
			Lens map[string][]string `json:"lens"`
		}
		if err := v.Decode(&lc); err != nil {
			return nil, fmt.Errorf("%s is malformed: %w", filepath.Join(s.dir, lensFile), err)
		}
		if lc.Lens != nil {
			reg.Lens = lc.Lens
		}
	}

	if v, ok, err := s.compileFile(meFile); err != nil {
		return nil, err
	} else if ok {
		var mc struct {
			Me map[string]string `json:"me"`
		}
		if err := v.Decode(&mc); err != nil {
			return nil, fmt.Errorf("%s is malformed: %w", filepath.Join(s.dir, meFile), err)
		}
		if mc.Me != nil {
			reg.Me = mc.Me
		}
	}

	return reg, nil
}

// compileFile returns ok false when the file is absent; compile failure is a hard error.
func (s *Store) compileFile(name string) (cue.Value, bool, error) {
	p := filepath.Join(s.dir, name)
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return cue.Value{}, false, nil
		}
		return cue.Value{}, false, fmt.Errorf("read %s: %w", p, err)
	}
	v := s.ctx.CompileBytes(data, cue.Filename(p))
	if err := v.Err(); err != nil {
		return cue.Value{}, false, fmt.Errorf("%s will not parse — fix or remove it: %w", p, err)
	}
	return v, true, nil
}

// WriteRegistry regenerates registry.cue from scopes and installs it atomically.
func (s *Store) WriteRegistry(scopes map[string]Entry) error {
	if scopes == nil {
		scopes = map[string]Entry{}
	}
	return s.writeOwned(registryFile, map[string]any{"scopes": scopes})
}

// WriteLens regenerates lens.cue from lens and installs it atomically.
func (s *Store) WriteLens(lens map[string][]string) error {
	if lens == nil {
		lens = map[string][]string{}
	}
	return s.writeOwned(lensFile, map[string]any{"lens": lens})
}

// WriteMe regenerates me.cue from me and installs it atomically.
func (s *Store) WriteMe(me map[string]string) error {
	if me == nil {
		me = map[string]string{}
	}
	return s.writeOwned(meFile, map[string]any{"me": me})
}

// writeOwned encodes model to CUE, formats as a top-level file, and installs via atomic rename.
// Map keys serialize sorted for deterministic output.
func (s *Store) writeOwned(name string, model any) error {
	v := s.ctx.Encode(model)
	if err := v.Err(); err != nil {
		return fmt.Errorf("encode %s: %w", name, err)
	}
	node := v.Syntax(cue.Concrete(true), cue.Final())
	st, ok := node.(*ast.StructLit)
	if !ok {
		return fmt.Errorf("encode %s: unexpected CUE syntax %T", name, node)
	}
	file := &ast.File{Decls: st.Elts}
	data, err := format.Node(file)
	if err != nil {
		return fmt.Errorf("format %s: %w", name, err)
	}
	return atomicfile.Write(filepath.Join(s.dir, name), data, 0o600)
}
