package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/p3bot/tk/internal/scopefile"

	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/frontmatter"
	"github.com/p3bot/tk/internal/id"
	"github.com/p3bot/tk/internal/registry"
	"github.com/p3bot/tk/internal/repair"
	"github.com/p3bot/tk/internal/rewrite"
	"github.com/p3bot/tk/internal/scopeconfig"
	"github.com/p3bot/tk/internal/selfcommit"
	"github.com/p3bot/tk/internal/token"
	"github.com/p3bot/tk/internal/xdg"
)

// newScopeRenameCmd: the only name_drift-exempt verb (idempotent re-run of interrupted rename).
func newScopeRenameCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "rename <old> <new>",
		Short: "Rename a scope in place (tk.cue, ids, filenames, in-scope edges)",
		Long: "Rename a scope end to end: rewrite the tk.cue name, the <scope>- prefix of every\n" +
			"ticket id and filename, and every in-scope depends/related edge, then re-key this\n" +
			"machine's registry and lens. The machine-local current-ticket pointer is dropped\n" +
			"(the stored id would go stale). Cross-scope inbound edges live in other repos and are\n" +
			"reported as edge_verify, not rewritten. An interrupted rename re-runs idempotently.",
		Args: usageArgs(cobra.ExactArgs(2)),
		RunE: func(c *cobra.Command, args []string) error {
			return runScopeRename(app, c, args[0], args[1])
		},
	}
}

func runScopeRename(app *App, c *cobra.Command, oldName, newName string) error {
	if !id.IsScopeName(newName) {
		return usageErrorf("%q is not a legal scope name (^[a-z0-9]{1,12}$)", newName)
	}
	if oldName == newName {
		return usageErrorf("scope is already named %q", newName)
	}

	e, err := app.openEngine(c)
	if err != nil {
		return err
	}
	defer e.close()

	entry, ok := e.reg.Scopes[oldName]
	if !ok {
		return fmt.Errorf("unknown scope %q — nothing to rename", oldName)
	}
	if _, taken := e.reg.Scopes[newName]; taken {
		return fmt.Errorf("scope name %q is already registered — names are machine-unique", newName)
	}
	dir := entry.Dir

	// Resolve by registry key <old> (name_drift exemption for interrupted rename).
	cueName, err := scopeconfig.ReadName(app.Ctx, dir)
	if err != nil {
		return fmt.Errorf("cannot rename %q: its tk.cue is unreadable: %w", oldName, err)
	}
	if cueName != oldName && cueName != newName {
		return fmt.Errorf("cannot rename %q to %q: its tk.cue name is %q — this is name drift, recover with tk scope forget %s && tk scope import %s", oldName, newName, cueName, oldName, dir)
	}

	schema, err := scopeconfig.Load(app.Ctx, dir)
	if err != nil {
		if ce, isCfg := scopeconfig.AsConfigError(err); isCfg {
			return fmt.Errorf("%s", token.Line(token.ConfigUnparseable,
				fmt.Sprintf("%s (%s): %s — fix tk.cue before renaming", oldName, ce.Dir, ce.Reason)))
		}
		return err
	}
	autoCommit := schema.AutoCommit
	root, hasRoot := scopefile.GitRoot(dir)
	if err := checkMidRebase(c.Context(), oldName, autoCommit, root, hasRoot); err != nil {
		return err
	}

	lock, err := scopefile.AcquireLock(dir)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()

	// edge_verify for cross-scope inbound edges only (surfaced, never rewritten).
	if _, err := e.reconcileResult(e.allTargets()); err != nil {
		return err
	}
	inbound, err := e.db.EdgesToScope(oldName)
	if err != nil {
		return err
	}

	ops, err := renamePlan(dir, oldName, newName)
	if err != nil {
		return err
	}

	touched, err := rewrite.Apply(ops)
	if err != nil {
		return err
	}
	// tk.cue name last: crash before → re-run finishes leftovers; after → name_drift window.
	if cueName != newName {
		if err := scopeconfig.RewriteName(dir, newName); err != nil {
			return err
		}
	}
	touched = append(touched, filepath.Join(dir, "tk.cue"))

	if autoCommit && hasRoot {
		if err := selfcommit.CommitPaths(c.Context(), selfcommit.BatchRequest{
			StateDir: app.StateDir, GitRoot: root,
			Message: fmt.Sprintf("tk: rename scope %s -> %s", oldName, newName), Paths: touched,
		}); err != nil {
			return err
		}
		e.tkDrivenSyncNeeded(c.Context(), c, dir, root)
	} else if autoCommit && !hasRoot {
		stderrln(c, token.Line(token.SyncDisabled, fmt.Sprintf("%s: no git repository — renamed files written but not committed", newName)))
	}

	if err := e.rekeyRegistry(oldName, newName); err != nil {
		return err
	}
	if err := e.reindexRenamed(newName, dir); err != nil {
		return err
	}

	for _, ed := range inbound {
		if ed.FromScope == oldName {
			continue // in-scope edges were rewritten in place, not reported
		}
		stdoutln(c, token.Line(token.EdgeVerify, fmt.Sprintf("%s %s %s — target scope renamed to %s, update this reference", ed.FromID, ed.Kind, ed.ToID, newName)))
	}
	stderrln(c, fmt.Sprintf("renamed scope %s -> %s", oldName, newName))
	return nil
}

// renamePlan skips already-<new> files (interrupted-rename tail); unparseable refuses all.
func renamePlan(dir, oldName, newName string) ([]rewrite.Op, error) {
	files, err := listScopeTicketFiles(dir, oldName, newName)
	if err != nil {
		return nil, err
	}
	var ops []rewrite.Op
	for _, f := range files {
		base := filepath.Base(f)
		if hasScopePrefix(base, newName) {
			continue // already migrated
		}
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", f, err)
		}
		interior, body, present := frontmatter.Split(data)
		if !present {
			return nil, fmt.Errorf("cannot rename: %s has no frontmatter fence — fix it first", f)
		}
		m, err := frontmatter.Parse(interior)
		if err != nil {
			return nil, fmt.Errorf("cannot rename: %s has unparseable frontmatter — fix it first: %w", f, err)
		}
		// Frontmatter id is authority (filename can disagree); out-of-scope id refuses.
		if !id.IsFullTicketID(m.ID) || scopeOfFullID(m.ID) != oldName {
			return nil, fmt.Errorf("cannot rename: %s declares id %q, which is not a ticket id in scope %q — fix its frontmatter id (tk doctor reports this) then re-run", f, m.ID, oldName)
		}
		newID := newName + strings.TrimPrefix(m.ID, oldName)
		m.ID = newID
		m.Depends = rekeyEdges(m.Depends, oldName, newName)
		m.Related = rekeyEdges(m.Related, oldName, newName)

		newPath := filepath.Join(filepath.Dir(f), repair.Basename(base, newID))
		interiorOut, err := frontmatter.Serialize(m)
		if err != nil {
			return nil, err
		}
		ops = append(ops, rewrite.Op{OldPath: f, NewPath: newPath, Content: frontmatter.Compose(interiorOut, body)})
	}
	return ops, nil
}

// rekeyRegistry judges newName uniqueness under the config lock (pre-lock snapshot races).
func (e *engine) rekeyRegistry(oldName, newName string) error {
	lock, err := xdg.AcquireConfigLock(e.app.ConfigDir)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()

	store := registry.NewStore(e.app.Ctx, e.app.ConfigDir)
	reg, err := store.Load()
	if err != nil {
		return err
	}
	entry, ok := reg.Scopes[oldName]
	if !ok {
		// Already re-keyed (idempotent); leftover lens/me under oldName stay unused.
		return nil
	}
	if taken, exists := reg.Scopes[newName]; exists {
		// In-dir rewrite already committed; re-key abort leaves the known name_drift window.
		return fmt.Errorf("scope name %q was registered to %s while this rename was running — names are machine-unique, so the registry was left unchanged; %s is already renamed on disk and now reads %q, recover with tk scope forget %s && tk scope import %s",
			newName, taken.Dir, entry.Dir, newName, oldName, entry.Dir)
	}
	delete(reg.Scopes, oldName)
	reg.Scopes[newName] = entry
	if err := store.WriteRegistry(reg.Scopes); err != nil {
		return err
	}
	return rekeySessionMaps(store, reg, oldName, newName)
}

func rekeySessionMaps(store *registry.Store, reg *registry.Registry, oldName, newName string) error {
	if lens, ok := reg.Lens[oldName]; ok {
		delete(reg.Lens, oldName)
		reg.Lens[newName] = lens
		if err := store.WriteLens(reg.Lens); err != nil {
			return err
		}
	}
	// me is a bookmark whose value is a full ticket id. Rename rewrites those
	// ids, so the pointer is dropped rather than rewritten — including any
	// leftover already stored under the new name.
	dropped := false
	if _, ok := reg.Me[oldName]; ok {
		delete(reg.Me, oldName)
		dropped = true
	}
	if _, ok := reg.Me[newName]; ok {
		delete(reg.Me, newName)
		dropped = true
	}
	if dropped {
		if err := store.WriteMe(reg.Me); err != nil {
			return err
		}
	}
	return nil
}

func rewriteFullID(full, oldName, newName string) string {
	if id.IsFullTicketID(full) && scopeOfFullID(full) == oldName {
		return newName + strings.TrimPrefix(full, oldName)
	}
	return full
}

// reindexRenamed loads the new name from disk; pruneForgotten drops the old
// registry key's rows once it is absent from registered (no separate DeleteScope).
func (e *engine) reindexRenamed(newName, dir string) error {
	reg, err := registry.NewStore(e.app.Ctx, e.app.ConfigDir).Load()
	if err != nil {
		return err
	}
	registered := make(map[string]bool, len(reg.Scopes))
	for name := range reg.Scopes {
		registered[name] = true
	}
	_, err = e.rec.Reconcile(map[string]string{newName: dir}, registered, nowNS())
	return err
}

// listScopeTicketFiles includes both old and new prefixes (interrupted-rename survivors).
func listScopeTicketFiles(dir, oldName, newName string) ([]string, error) {
	var out []string
	collect := func(root string) error {
		entries, err := os.ReadDir(root)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		for _, ent := range entries {
			if ent.IsDir() {
				continue
			}
			base := ent.Name()
			if hasScopePrefix(base, oldName) || hasScopePrefix(base, newName) {
				out = append(out, filepath.Join(root, base))
			}
		}
		return nil
	}
	if err := collect(dir); err != nil {
		return nil, err
	}
	if err := collect(filepath.Join(dir, "archive")); err != nil {
		return nil, err
	}
	return out, nil
}

func hasScopePrefix(base, scope string) bool {
	stem, ok := strings.CutSuffix(base, ".md")
	if !ok {
		return false
	}
	prefix := scope + "-"
	if !strings.HasPrefix(stem, prefix) {
		return false
	}
	short := shortAfter(stem, scope)
	return id.IsShortID(short)
}

func shortAfter(stem, scope string) string {
	rest := strings.TrimPrefix(stem, scope+"-")
	if i := strings.IndexByte(rest, '-'); i >= 0 {
		return rest[:i]
	}
	return rest
}

func rekeyEdges(list []string, oldName, newName string) []string {
	if len(list) == 0 {
		return list
	}
	out := make([]string, len(list))
	for i, e := range list {
		out[i] = rewriteFullID(e, oldName, newName)
	}
	return out
}
