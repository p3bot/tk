// Package scopeadmin orchestrates the scope-administration verbs (init, import,
// rebind, forget, list) over the machine-local registry. Mutating verbs run as
// one critical section under the machine-global flock (load-validate-write) so
// registration invariants stay concurrency-safe. Paths arrive already cleaned,
// absolute, and symlink-resolved from the CLI edge.
package scopeadmin

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"cuelang.org/go/cue"
	"github.com/p3bot/tk/internal/atomicfile"
	"github.com/p3bot/tk/internal/gitroot"
	"github.com/p3bot/tk/internal/pathutil"
	"github.com/p3bot/tk/internal/registry"
	"github.com/p3bot/tk/internal/resolve"
	"github.com/p3bot/tk/internal/scope"
	"github.com/p3bot/tk/internal/scopeconfig"
	"github.com/p3bot/tk/internal/token"
	"github.com/p3bot/tk/internal/xdg"
)

// Admin performs scope administration against a fixed XDG config directory.
type Admin struct {
	ctx       *cue.Context
	store     *registry.Store
	configDir string
}

// New builds an Admin over configDir using the process-wide CUE context.
func New(ctx *cue.Context, configDir string) *Admin {
	return &Admin{ctx: ctx, store: registry.NewStore(ctx, configDir), configDir: configDir}
}

// InitParams are the resolved inputs to tk scope init.
// Exactly one of Name / AutoName is set (CLI enforces usage before calling).
type InitParams struct {
	Dir             string
	Name            string
	AutoName        bool
	CodeRoot        string
	CodeRootGiven   bool
	AutoCommit      bool
	AutoCommitGiven bool
}

// Init creates and registers a new scope: authors tk.cue and .gitignore, applies
// the code-root default matrix, runs registration checks, and records the entry.
// Never prompts and never runs git. Returns the registered scope dir.
func (a *Admin) Init(p InitParams) (string, error) {
	if p.AutoName == (p.Name != "") {
		return "", fmt.Errorf("exactly one of --name or --auto-name is required")
	}
	// Enforce the CLI edge contract: cleaned absolute symlink-resolved paths.
	p.Dir = pathutil.Canonical(p.Dir)
	if p.CodeRootGiven {
		p.CodeRoot = pathutil.Canonical(p.CodeRoot)
	}

	// Pre-write guard: existing tk.cue means adopt-not-author.
	if _, err := os.Stat(filepath.Join(p.Dir, "tk.cue")); err == nil {
		return "", fmt.Errorf("%s already contains a tk.cue — that scope already exists on disk; adopt it with tk scope import %s, or choose a different dir", p.Dir, p.Dir)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("stat %s: %w", filepath.Join(p.Dir, "tk.cue"), err)
	}

	// Derive git-root without creating the dir so a failed init leaves nothing behind.
	gitRoot, inRepo := gitroot.RepoRootForNew(p.Dir)
	codeRoot := resolveCodeRoot(p.Dir, p.CodeRoot, p.CodeRootGiven, gitRoot, inRepo)

	name := p.Name
	if p.AutoName {
		var err error
		name, err = scope.AutoName(filepath.Base(codeRoot))
		if err != nil {
			return "", fmt.Errorf("--auto-name: %w", err)
		}
	}

	lock, err := xdg.AcquireConfigLock(a.configDir)
	if err != nil {
		return "", err
	}
	defer func() { _ = lock.Release() }()

	reg, err := a.store.Load()
	if err != nil {
		return "", err
	}

	if err := checkNameCollision(reg, name, p.AutoName); err != nil {
		return "", err
	}
	if err := checkCodeRootCollision(reg, codeRoot, ""); err != nil {
		return "", err
	}
	if err := checkDirDisjoint(reg, p.Dir, ""); err != nil {
		return "", err
	}
	cons, err := siblingConsensus(a, reg, gitRoot, inRepo, "")
	if err != nil {
		return "", err
	}
	autoCommit, err := resolveInitAutoCommit(cons, p.AutoCommitGiven, p.AutoCommit)
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(p.Dir, 0o755); err != nil {
		return "", fmt.Errorf("create scope dir %s: %w", p.Dir, err)
	}
	if err := scopeconfig.WriteMinimal(p.Dir, name, autoCommit); err != nil {
		return "", err
	}
	if err := ensureGitignore(p.Dir); err != nil {
		return "", err
	}

	reg.Scopes[name] = registry.Entry{Dir: p.Dir, Root: codeRoot}
	if err := a.store.WriteRegistry(reg.Scopes); err != nil {
		// Files written but registration failed: recover via import, not re-init.
		return "", fmt.Errorf("wrote the scope files at %s but failed to register it — adopt the on-disk scope with tk scope import %s: %w", p.Dir, p.Dir, err)
	}
	return p.Dir, nil
}

// ImportParams are the resolved inputs to tk scope import.
// Name and autoCommit come from on-disk tk.cue.
type ImportParams struct {
	Dir           string
	CodeRoot      string
	CodeRootGiven bool
}

// Import registers an existing on-disk scope. Unusable tk.cue is a hard fail.
func (a *Admin) Import(p ImportParams) (string, error) {
	p.Dir = pathutil.Canonical(p.Dir)
	if p.CodeRootGiven {
		p.CodeRoot = pathutil.Canonical(p.CodeRoot)
	}

	schema, err := scopeconfig.Load(a.ctx, p.Dir)
	if err != nil {
		if ce, ok := scopeconfig.AsConfigError(err); ok {
			return "", fmt.Errorf("%s", token.Line(token.ConfigUnparseable,
				fmt.Sprintf("cannot import %s: %s", p.Dir, ce.Reason)))
		}
		return "", err
	}

	gitRoot, inRepo := gitroot.RepoRoot(p.Dir)
	codeRoot := resolveCodeRoot(p.Dir, p.CodeRoot, p.CodeRootGiven, gitRoot, inRepo)

	lock, err := xdg.AcquireConfigLock(a.configDir)
	if err != nil {
		return "", err
	}
	defer func() { _ = lock.Release() }()

	reg, err := a.store.Load()
	if err != nil {
		return "", err
	}

	if err := checkNameCollision(reg, schema.Name, false); err != nil {
		return "", err
	}
	if err := checkCodeRootCollision(reg, codeRoot, ""); err != nil {
		return "", err
	}
	if err := checkDirDisjoint(reg, p.Dir, ""); err != nil {
		return "", err
	}
	cons, err := siblingConsensus(a, reg, gitRoot, inRepo, "")
	if err != nil {
		return "", err
	}
	if cons.found && cons.value != schema.AutoCommit {
		return "", autoCommitMismatch(cons.gitRoot, cons.value, schema.AutoCommit)
	}

	reg.Scopes[schema.Name] = registry.Entry{Dir: p.Dir, Root: codeRoot}
	if err := a.store.WriteRegistry(reg.Scopes); err != nil {
		return "", err
	}
	return p.Dir, nil
}

// RebindParams are the resolved inputs to tk scope rebind.
// Dir always updates; CodeRoot updates root only when CodeRootGiven.
type RebindParams struct {
	Dir           string
	Name          string
	CodeRoot      string
	CodeRootGiven bool
}

// Rebind rewrites machine-local registry paths for an already-registered scope.
// Dir always; root only when --code-root given (dir-only move leaves root untouched).
// Not name repair. changed reports whether anything moved.
func (a *Admin) Rebind(p RebindParams) (dir string, changed bool, err error) {
	p.Dir = pathutil.Canonical(p.Dir)
	if p.CodeRootGiven {
		p.CodeRoot = pathutil.Canonical(p.CodeRoot)
	}

	lock, err := xdg.AcquireConfigLock(a.configDir)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = lock.Release() }()

	reg, err := a.store.Load()
	if err != nil {
		return "", false, err
	}

	entry, ok := reg.Scopes[p.Name]
	if !ok {
		return "", false, fmt.Errorf("unknown scope %q — nothing to rebind; register it first with tk scope init or import", p.Name)
	}

	newRoot := entry.Root
	if p.CodeRootGiven {
		newRoot = p.CodeRoot
	}

	// New dir must hold a tk.cue whose name equals --name (rebind is not name repair).
	cueName, err := scopeconfig.ReadName(a.ctx, p.Dir)
	if err != nil {
		return "", false, fmt.Errorf("cannot rebind %q to %s: %w", p.Name, p.Dir, err)
	}
	if cueName != p.Name {
		return "", false, fmt.Errorf("refusing to rebind %q to %s: its tk.cue name is %q, not %q — rebind moves paths, it does not repair a name", p.Name, p.Dir, cueName, p.Name)
	}

	if p.CodeRootGiven {
		if err := checkCodeRootCollision(reg, newRoot, p.Name); err != nil {
			return "", false, err
		}
	}
	if err := checkDirDisjoint(reg, p.Dir, p.Name); err != nil {
		return "", false, err
	}

	if entry.Dir == p.Dir && entry.Root == newRoot {
		return p.Dir, false, nil
	}

	reg.Scopes[p.Name] = registry.Entry{Dir: p.Dir, Root: newRoot}
	if err := a.store.WriteRegistry(reg.Scopes); err != nil {
		return "", false, err
	}
	return p.Dir, true, nil
}

// Forget unregisters a scope (registry, lens, and me only; never touches files).
// Unreachable dirs stay registered until forget.
func (a *Admin) Forget(name string) error {
	lock, err := xdg.AcquireConfigLock(a.configDir)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()

	reg, err := a.store.Load()
	if err != nil {
		return err
	}
	if _, ok := reg.Scopes[name]; !ok {
		return fmt.Errorf("unknown scope %q — nothing to forget", name)
	}

	delete(reg.Scopes, name)
	hadLens := false
	if _, ok := reg.Lens[name]; ok {
		delete(reg.Lens, name)
		hadLens = true
	}
	hadMe := false
	if _, ok := reg.Me[name]; ok {
		delete(reg.Me, name)
		hadMe = true
	}

	if err := a.store.WriteRegistry(reg.Scopes); err != nil {
		return err
	}
	if hadLens {
		if err := a.store.WriteLens(reg.Lens); err != nil {
			return err
		}
	}
	if hadMe {
		if err := a.store.WriteMe(reg.Me); err != nil {
			return err
		}
	}
	return nil
}

// ListRow is one registered scope's parse-stable listing fields.
type ListRow struct {
	Name string
	Dir  string
	Root string
	Mode string
}

// Mode labels for tk scope list.
const (
	ModeTkDriven   = "tk-driven"
	ModeRepoDriven = "repo-driven"
	ModePlainFiles = "plain-files"
	ModeUnknown    = "unknown"
)

// Listing is the result of tk scope list: TSV rows plus soft stderr diagnostics.
type Listing struct {
	Rows        []ListRow
	Diagnostics []string
}

// List enumerates every registered scope sorted by name. One bad scope never fails
// the listing; drift, unreachable dirs, and unparseable configs are soft diagnostics.
func (a *Admin) List() (*Listing, error) {
	reg, err := a.store.Load()
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(reg.Scopes))
	for name := range reg.Scopes {
		names = append(names, name)
	}
	sort.Strings(names)

	out := &Listing{}
	for _, name := range names {
		entry := reg.Scopes[name]
		row := ListRow{Name: name, Dir: filepath.Clean(entry.Dir), Root: filepath.Clean(entry.Root)}

		if fi, err := os.Stat(entry.Dir); err != nil || !fi.IsDir() {
			row.Mode = ModeUnknown
			out.Diagnostics = append(out.Diagnostics, token.Line(token.UnreachableScope,
				fmt.Sprintf("%s: dir %s is gone", name, entry.Dir)))
			out.Rows = append(out.Rows, row)
			continue
		}

		// Reuse one git-root derivation and one tk.cue compile on the healthy path.
		gitRoot, inRepo := gitroot.RepoRoot(entry.Dir)
		schema, cfgErr := scopeconfig.Load(a.ctx, entry.Dir)

		// Healthy path uses schema.Name; unusable config falls back to ReadName for drift.
		driftName := ""
		if cfgErr == nil {
			driftName = schema.Name
		} else if cueName, nameErr := scopeconfig.ReadName(a.ctx, entry.Dir); nameErr == nil {
			driftName = cueName
		}
		if driftName != "" && driftName != name {
			out.Diagnostics = append(out.Diagnostics,
				resolve.DriftLine(name, driftName, entry.Dir,
					resolve.SuggestCodeRootWith(entry.Dir, entry.Root, gitRoot, inRepo)))
		}

		if cfgErr != nil {
			row.Mode = ModeUnknown
			if ce, ok := scopeconfig.AsConfigError(cfgErr); ok {
				out.Diagnostics = append(out.Diagnostics, token.Line(token.ConfigUnparseable,
					fmt.Sprintf("%s: %s", name, ce.Reason)))
			} else {
				out.Diagnostics = append(out.Diagnostics, token.Line(token.ConfigUnparseable,
					fmt.Sprintf("%s: %v", name, cfgErr)))
			}
			out.Rows = append(out.Rows, row)
			continue
		}

		row.Mode = DeriveMode(schema.AutoCommit, inRepo)
		out.Rows = append(out.Rows, row)
	}
	return out, nil
}

// DeriveMode maps autoCommit and git-root presence to the closed mode label.
// autoCommit true is always tk-driven (including no-repo layouts).
func DeriveMode(autoCommit, inRepo bool) string {
	if autoCommit {
		return ModeTkDriven
	}
	if inRepo {
		return ModeRepoDriven
	}
	return ModePlainFiles
}

// resolveCodeRoot applies the init/import code-root default matrix.
// Explicit --code-root is stored as-is: ambient cwd match only. Git durability
// always derives from dir, so a foreign code-root (tickets repo ≠ product tree)
// is valid and deliberate — same as rebind --code-root.
func resolveCodeRoot(dir, codeRoot string, given bool, gitRoot string, inRepo bool) string {
	if given {
		return codeRoot
	}
	if inRepo {
		return gitRoot
	}
	return dir
}

// ensureGitignore makes sure <dir>/.gitignore ignores .tk.lock without disturbing other entries.
func ensureGitignore(dir string) error {
	p := filepath.Join(dir, ".gitignore")
	existing, err := os.ReadFile(p)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", p, err)
	}
	for _, line := range strings.Split(string(existing), "\n") {
		if strings.TrimSpace(line) == ".tk.lock" {
			return nil
		}
	}
	buf := existing
	if len(buf) > 0 && !bytes.HasSuffix(buf, []byte("\n")) {
		buf = append(buf, '\n')
	}
	buf = append(buf, []byte(".tk.lock\n")...)
	return atomicfile.Write(p, buf, 0o644)
}
