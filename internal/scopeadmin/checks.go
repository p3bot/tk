package scopeadmin

import (
	"fmt"
	"sort"

	"github.com/p3bot/tk/internal/gitroot"
	"github.com/p3bot/tk/internal/pathutil"
	"github.com/p3bot/tk/internal/registry"
	"github.com/p3bot/tk/internal/scopeconfig"
	"github.com/p3bot/tk/internal/token"
)

// checkNameCollision rejects a name already registered. No rename-on-import: the
// name is baked into every id, filename, and in-scope reference.
func checkNameCollision(reg *registry.Registry, name string, derived bool) error {
	if _, ok := reg.Scopes[name]; !ok {
		return nil
	}
	if derived {
		return fmt.Errorf("derived scope name %q is already registered — pass --name to choose another", name)
	}
	return fmt.Errorf("scope name %q is already registered — names are machine-unique; rename at the source (tk scope rename) rather than re-registering", name)
}

// checkCodeRootCollision rejects a code-root identical to another scope's.
// Nested code-roots are fine; only identical ones are rejected.
func checkCodeRootCollision(reg *registry.Registry, root, exclude string) error {
	for name, e := range reg.Scopes {
		if name == exclude {
			continue
		}
		if e.Root == root {
			return fmt.Errorf("code-root %s is already registered to scope %q — nested code-roots are fine, identical ones are not", root, name)
		}
	}
	return nil
}

// checkDirDisjoint rejects a dir identical to, nested within, or containing any
// other scope's dir. Dirs must be mutually disjoint (sync treats everything under a dir as that scope's).
func checkDirDisjoint(reg *registry.Registry, dir, exclude string) error {
	for name, e := range reg.Scopes {
		if name == exclude {
			continue
		}
		if pathutil.Overlap(dir, e.Dir) {
			return fmt.Errorf("scope dir %s overlaps scope %q's dir %s — dirs must be mutually disjoint; choose a sibling path (e.g. .agents/tk-teamB), not one nested under an existing scope", dir, name, e.Dir)
		}
	}
	return nil
}

// GitRootScope is one registered scope whose derived git-root was considered.
type GitRootScope struct {
	Name string
	Dir  string
}

// GitRootScopes returns registered scopes whose gitroot.RepoRoot equals gitRoot,
// sorted by name. Directories git cannot resolve drop out (gone dirs).
func GitRootScopes(reg *registry.Registry, gitRoot string) []GitRootScope {
	out, _ := gitRootScopes(reg, gitRoot, false)
	return out
}

// GitRootScopesRefuseUnreachable is GitRootScopes, except a registered dir nested
// under gitRoot whose RepoRoot cannot be derived is unreachable_scope.
func GitRootScopesRefuseUnreachable(reg *registry.Registry, gitRoot string) ([]GitRootScope, error) {
	return gitRootScopes(reg, gitRoot, true)
}

func gitRootScopes(reg *registry.Registry, gitRoot string, refuseUnreachable bool) ([]GitRootScope, error) {
	var out []GitRootScope
	for name, entry := range reg.Scopes {
		sgr, sok := gitroot.RepoRoot(entry.Dir)
		if sok {
			if sgr == gitRoot {
				out = append(out, GitRootScope{Name: name, Dir: entry.Dir})
			}
			continue
		}
		if refuseUnreachable && pathutil.UnderOrEqual(entry.Dir, gitRoot) {
			return nil, fmt.Errorf("%s", token.Line(token.UnreachableScope,
				fmt.Sprintf("%s: dir %s is not reachable", name, entry.Dir)))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// consensus is the autoCommit agreement among registered scopes sharing a git-root.
type consensus struct {
	hasGitRoot bool
	gitRoot    string
	// found reports whether at least one sibling shares the git-root; value is their agreed autoCommit.
	found bool
	value bool
}

// siblingConsensus evaluates autoCommit of every registered scope sharing the
// candidate's git-root (pre-derived — init may not have created the dir yet).
// An unusable sibling tk.cue refuses with config_unparseable; gone dirs drop out.
func siblingConsensus(a *Admin, reg *registry.Registry, gitRoot string, inRepo bool, excludeName string) (consensus, error) {
	c := consensus{hasGitRoot: inRepo, gitRoot: gitRoot}
	if !inRepo {
		return c, nil
	}
	for _, s := range GitRootScopes(reg, gitRoot) {
		if s.Name == excludeName {
			continue
		}
		schema, err := scopeconfig.Load(a.ctx, s.Dir)
		if err != nil {
			if _, isCfg := scopeconfig.AsConfigError(err); isCfg {
				return c, fmt.Errorf("%s", token.Line(token.ConfigUnparseable,
					fmt.Sprintf("sibling scope at %s sharing git-root %s has an unparseable tk.cue — fix it before registering here", s.Dir, gitRoot)))
			}
			return c, err
		}
		if !c.found {
			c.found = true
			c.value = schema.AutoCommit
		} else if c.value != schema.AutoCommit {
			return c, autoCommitMismatch(gitRoot, c.value, schema.AutoCommit)
		}
	}
	return c, nil
}

func autoCommitMismatch(gitRoot string, existing, offered bool) error {
	return fmt.Errorf("%s", token.Line(token.AutoCommitMismatch,
		fmt.Sprintf("scopes sharing git-root %s use autoCommit=%v but this scope offers autoCommit=%v — every scope in a repo must agree; an isolated auto-commit scope belongs in its own repo", gitRoot, existing, offered)))
}

// resolveInitAutoCommit inherits siblings when the flag is omitted, uses the flag
// when no siblings exist, and rejects an explicit flag that contradicts siblings.
func resolveInitAutoCommit(c consensus, flagGiven, flagVal bool) (bool, error) {
	if c.found {
		if flagGiven && flagVal != c.value {
			return false, autoCommitMismatch(c.gitRoot, c.value, flagVal)
		}
		return c.value, nil
	}
	if flagGiven {
		return flagVal, nil
	}
	return false, nil
}
