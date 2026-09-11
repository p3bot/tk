package writeengine

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/p3bot/tk/internal/flock"
	"github.com/p3bot/tk/internal/frontmatter"
	"github.com/p3bot/tk/internal/gitstate"
	"github.com/p3bot/tk/internal/id"
	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/order"
	"github.com/p3bot/tk/internal/pathutil"
	"github.com/p3bot/tk/internal/registry"
	"github.com/p3bot/tk/internal/repair"
	"github.com/p3bot/tk/internal/rewrite"
	"github.com/p3bot/tk/internal/scopeconfig"
	"github.com/p3bot/tk/internal/scopefile"
	"github.com/p3bot/tk/internal/selfcommit"
	"github.com/p3bot/tk/internal/status"
	"github.com/p3bot/tk/internal/token"
	"github.com/p3bot/tk/internal/xdg"
)

// RehomeInput is one tk rehome: source identity already classified, dest scope name.
type RehomeInput struct {
	SourceScope string
	SourceDir   string
	DestScope   string
	DestDir     string
	Lookup      Lookup
}

type rehomeSide struct {
	name       string
	dir        string
	schema     *scopeconfig.Schema
	autoCommit bool
	root       string
	hasRoot    bool
}

type leftoverDest struct {
	id      string
	order   string
	path    string
	content []byte
}

// Rehome prefix-rewrites one ticket into dest, rewrites source/dest inbound
// edges, and self-commits each tk-driven git-root that was touched.
func Rehome(deps Deps, in RehomeInput) (Result, error) {
	if err := validateRehomeArgs(in); err != nil {
		return Result{}, err
	}
	srcDir, destDir, err := resolveRehomeDirs(deps, &in)
	if err != nil {
		return Result{}, err
	}

	locks, err := lockScopes(map[string]string{
		in.SourceScope: srcDir,
		in.DestScope:   destDir,
	})
	if err != nil {
		return Result{}, err
	}
	defer releaseLocks(locks)

	res, err := deps.Rec.Reconcile(allScopeDirs(deps.Reg), registeredSet(deps.Reg), nowNS())
	if err != nil {
		return Result{}, err
	}
	if err := RefuseUnusable(res, in.SourceScope, srcDir); err != nil {
		return Result{}, err
	}
	if err := RefuseUnusable(res, in.DestScope, destDir); err != nil {
		return Result{}, err
	}

	src := sideFrom(in.SourceScope, srcDir, res.Schema(in.SourceScope))
	dest := sideFrom(in.DestScope, destDir, res.Schema(in.DestScope))
	if err := gitstate.CheckMidRebase(ctxOf(deps), src.name, src.autoCommit, src.root, src.hasRoot); err != nil {
		return Result{}, err
	}
	if err := gitstate.CheckMidRebase(ctxOf(deps), dest.name, dest.autoCommit, dest.root, dest.hasRoot); err != nil {
		return Result{}, err
	}
	if err := refuseSharedRootAutoCommitMismatch(src, dest); err != nil {
		return Result{}, err
	}

	row, err := ResolveWriteRow(deps.DB, in.SourceScope, in.Lookup)
	if err != nil {
		return Result{}, err
	}
	srcModel, srcBody, err := ReadTicketFile(row.Path)
	if err != nil {
		return Result{}, err
	}
	oldID := srcModel.ID
	if !id.IsFullTicketID(oldID) || id.ScopeOfFullID(oldID) != in.SourceScope {
		return Result{}, fmt.Errorf("cannot rehome: %s declares id %q, which is not a ticket id in scope %q — fix its frontmatter id (tk doctor reports this) then re-run", row.Path, oldID, in.SourceScope)
	}
	sourceShort := strings.TrimPrefix(oldID, in.SourceScope+"-")

	destCustom := dest.schema.CustomStatuses()
	if !status.IsKnown(srcModel.Status, destCustom) {
		return Result{}, &UnknownStatusError{Status: srcModel.Status, Scope: in.DestScope}
	}

	destRows, err := deps.DB.ScopeTickets(in.DestScope)
	if err != nil {
		return Result{}, err
	}
	left, err := findLeftoverDest(srcModel, srcBody, oldID, sourceShort, in.DestScope, destDir)
	if err != nil {
		return Result{}, err
	}

	var destID, destOrder, destPath string
	var destContent []byte
	if left != nil {
		destID = left.id
		destOrder = left.order
		destPath = left.path
		destContent = left.content
	} else {
		occupied := destOccupiedShorts(destRows)
		destShort := sourceShort
		if _, taken := occupied[destShort]; taken {
			destShort, err = id.Extend(sourceShort, occupied)
			if err != nil {
				return Result{}, fmt.Errorf("rehome %s into %s: %w", oldID, in.DestScope, err)
			}
		}
		destID = in.DestScope + "-" + destShort
		destOrder, err = order.KeyBetween(MaxValidOrder(destRows), "")
		if err != nil {
			return Result{}, fmt.Errorf("compute append order for %s: %w", destID, err)
		}
		destContent, err = reconstruct(srcModel, srcBody, destID, destOrder, oldID, destID)
		if err != nil {
			return Result{}, err
		}
		base := repair.Basename(filepath.Base(row.Path), destID)
		destPath, err = TerminalLocation(destDir, base, status.IsTerminal(srcModel.Status, destCustom))
		if err != nil {
			return Result{}, err
		}
	}

	inbound, err := deps.DB.EdgesByTarget(oldID)
	if err != nil {
		return Result{}, err
	}

	edgeOps, err := planEdgeRewrites(srcDir, destDir, row.Path, oldID, destID)
	if err != nil {
		return Result{}, err
	}

	ops := make([]rewrite.Op, 0, 2+len(edgeOps))
	if left != nil {
		// Dest already holds this ticket; in-place so an edited source can
		// replace leftover bytes. Create-only would refuse a different file.
		ops = append(ops, rewrite.Op{OldPath: destPath, NewPath: destPath, Content: destContent})
	} else {
		ops = append(ops, rewrite.Op{NewPath: destPath, Content: destContent})
	}
	ops = append(ops, edgeOps...)
	ops = append(ops, rewrite.Op{OldPath: row.Path, NewPath: destPath, Content: destContent})

	touched, err := rewrite.Apply(ops)
	if err != nil {
		return Result{}, err
	}

	out := Result{
		Warnings:   res.Warnings,
		ID:         destID,
		Moved:      true,
		EdgeVerify: edgeVerifyOthers(inbound, in.SourceScope, in.DestScope, destID),
	}
	abs, err := absPath(destPath)
	if err != nil {
		return out, err
	}
	out.Path = abs

	srcPaths, destPaths := partitionPaths(srcDir, destDir, touched)
	if err := deps.Rec.SyncPaths(in.SourceScope, srcPaths); err != nil {
		return out, err
	}
	if err := deps.Rec.SyncPaths(in.DestScope, destPaths); err != nil {
		return out, err
	}

	disabled, needed, err := completeRehome(deps, src, dest, srcPaths, destPaths, oldID, destID)
	if err != nil {
		return out, err
	}
	out.SyncDisabledAll = disabled
	out.SyncNeededAll = needed
	if len(disabled) == 1 {
		out.SyncDisabled = disabled[0]
	}
	if len(needed) == 1 {
		out.SyncNeeded = needed[0]
	}

	if err := dropSourceMe(deps, in.SourceScope, oldID); err != nil {
		return out, err
	}
	return out, nil
}

func validateRehomeArgs(in RehomeInput) error {
	if !id.IsScopeName(in.DestScope) {
		return &UsageError{Msg: fmt.Sprintf("%q is not a legal scope name (^[a-z0-9]{1,12}$)", in.DestScope)}
	}
	if in.DestScope == in.SourceScope {
		return &UsageError{Msg: fmt.Sprintf("destination scope %q is the ticket's current scope", in.DestScope)}
	}
	return nil
}

func resolveRehomeDirs(deps Deps, in *RehomeInput) (srcDir, destDir string, err error) {
	srcDir = in.SourceDir
	if srcDir == "" {
		entry, ok := deps.Reg.Scopes[in.SourceScope]
		if !ok {
			return "", "", fmt.Errorf("unknown ticket id %q: scope %q is not registered here", in.Lookup.Arg, in.SourceScope)
		}
		srcDir = entry.Dir
		in.SourceDir = srcDir
	}
	destDir = in.DestDir
	if destDir == "" {
		entry, ok := deps.Reg.Scopes[in.DestScope]
		if !ok {
			return "", "", fmt.Errorf("unknown scope %q", in.DestScope)
		}
		destDir = entry.Dir
		in.DestDir = destDir
	} else if _, ok := deps.Reg.Scopes[in.DestScope]; !ok {
		return "", "", fmt.Errorf("unknown scope %q", in.DestScope)
	}
	return srcDir, destDir, nil
}

func sideFrom(name, dir string, schema *scopeconfig.Schema) rehomeSide {
	root, hasRoot := scopefile.GitRoot(dir)
	return rehomeSide{
		name:       name,
		dir:        dir,
		schema:     schema,
		autoCommit: scopeconfig.SchemaAutoCommit(schema),
		root:       root,
		hasRoot:    hasRoot,
	}
}

func lockScopes(dirs map[string]string) (map[string]*flock.Lock, error) {
	names := make([]string, 0, len(dirs))
	for name := range dirs {
		names = append(names, name)
	}
	sort.Strings(names)
	locks := make(map[string]*flock.Lock, len(names))
	for _, name := range names {
		lock, err := scopefile.AcquireLock(dirs[name])
		if err != nil {
			releaseLocks(locks)
			return nil, err
		}
		locks[name] = lock
	}
	return locks, nil
}

func releaseLocks(locks map[string]*flock.Lock) {
	for _, lock := range locks {
		if lock != nil {
			_ = lock.Release()
		}
	}
}

func allScopeDirs(reg *registry.Registry) map[string]string {
	out := make(map[string]string, len(reg.Scopes))
	for name, entry := range reg.Scopes {
		out[name] = entry.Dir
	}
	return out
}

func destOccupiedShorts(rows []*index.Ticket) map[string]struct{} {
	taken := make(map[string]struct{}, len(rows)*2)
	for _, p := range rows {
		if p.ShortID != "" {
			taken[p.ShortID] = struct{}{}
		}
		// Filename short occupies even when frontmatter id disagrees, so dest
		// cannot land on that basename.
		if full, ok := scopefile.TicketIDFromBase(filepath.Base(p.Path)); ok {
			if i := strings.IndexByte(full, '-'); i >= 0 {
				taken[full[i+1:]] = struct{}{}
			}
		}
	}
	return taken
}

func findLeftoverDest(src *frontmatter.Model, body []byte, oldID, sourceShort, destScope, destDir string) (*leftoverDest, error) {
	paths, err := scopefile.ListTickets(destDir)
	if err != nil {
		return nil, err
	}
	for _, p := range paths {
		full, ok := scopefile.TicketIDFromBase(filepath.Base(p))
		if !ok || id.ScopeOfFullID(full) != destScope {
			continue
		}
		short := strings.TrimPrefix(full, destScope+"-")
		if short != sourceShort && !(len(short) > len(sourceShort) && strings.HasPrefix(short, sourceShort)) {
			continue
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", p, err)
		}
		interior, _, present := frontmatter.Split(raw)
		if !present {
			continue
		}
		destM, err := frontmatter.Parse(interior)
		if err != nil {
			continue
		}
		if src.Created == "" || destM.Created == "" || destM.Created != src.Created {
			continue
		}
		content, err := reconstruct(src, body, destM.ID, destM.Order, oldID, destM.ID)
		if err != nil {
			return nil, err
		}
		return &leftoverDest{id: destM.ID, order: destM.Order, path: p, content: content}, nil
	}
	return nil, nil
}

func reconstruct(src *frontmatter.Model, body []byte, destID, destOrder, oldID, newID string) ([]byte, error) {
	m := *src
	m.ID = destID
	m.Order = destOrder
	m.Depends, _ = rewriteEdgeList(src.Depends, oldID, newID)
	m.Related, _ = rewriteEdgeList(src.Related, oldID, newID)
	return composeTicket(&m, body)
}

func composeTicket(m *frontmatter.Model, body []byte) ([]byte, error) {
	interior, err := frontmatter.Serialize(m)
	if err != nil {
		return nil, err
	}
	return frontmatter.Compose(interior, body), nil
}

func planEdgeRewrites(srcDir, destDir, srcPath, oldID, newID string) ([]rewrite.Op, error) {
	var paths []string
	for _, dir := range []string{srcDir, destDir} {
		listed, err := scopefile.ListTickets(dir)
		if err != nil {
			return nil, err
		}
		paths = append(paths, listed...)
	}
	sort.Strings(paths)
	var ops []rewrite.Op
	for _, p := range paths {
		if p == srcPath {
			continue
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", p, err)
		}
		interior, body, present := frontmatter.Split(raw)
		sibID, _ := scopefile.TicketIDFromBase(filepath.Base(p))
		if !present {
			if containsExactFullID(raw, oldID) {
				return nil, unparseableInbound(sibID, p, oldID)
			}
			continue
		}
		m, err := frontmatter.Parse(interior)
		if err != nil {
			if containsExactFullID(interior, oldID) {
				return nil, unparseableInbound(sibID, p, oldID)
			}
			continue
		}
		dep, depCh := rewriteEdgeList(m.Depends, oldID, newID)
		rel, relCh := rewriteEdgeList(m.Related, oldID, newID)
		if !depCh && !relCh {
			continue
		}
		m.Depends = dep
		m.Related = rel
		content, err := composeTicket(m, body)
		if err != nil {
			return nil, err
		}
		ops = append(ops, rewrite.Op{OldPath: p, NewPath: p, Content: content})
	}
	return ops, nil
}

func unparseableInbound(sibID, path, oldID string) error {
	if sibID == "" {
		sibID = filepath.Base(path)
	}
	return fmt.Errorf("%s", token.Line(token.ParseError,
		fmt.Sprintf("%s: unparseable frontmatter — cannot rehome %s while this inbound reference is unparseable", sibID, oldID)))
}

func rewriteEdgeList(list []string, oldID, newID string) ([]string, bool) {
	if len(list) == 0 {
		return list, false
	}
	out := make([]string, 0, len(list))
	seen := make(map[string]struct{}, len(list))
	changed := false
	for _, e := range list {
		v := e
		if v == oldID {
			v = newID
		}
		if _, dup := seen[v]; dup {
			if v != e {
				changed = true
			}
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
		if v != e {
			changed = true
		}
	}
	if !changed {
		return list, false
	}
	return out, true
}

func containsExactFullID(b []byte, full string) bool {
	needle := []byte(full)
	start := 0
	for {
		i := bytes.Index(b[start:], needle)
		if i < 0 {
			return false
		}
		i += start
		beforeOK := i == 0 || !isIdentByte(b[i-1])
		after := i + len(needle)
		afterOK := after == len(b) || strings.IndexByte(id.ShortIDAlphabet, b[after]) < 0
		if beforeOK && afterOK {
			return true
		}
		start = i + 1
	}
}

func isIdentByte(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
}

func partitionPaths(srcDir, destDir string, paths []string) (src, dest []string) {
	seen := make(map[string]bool, len(paths))
	for _, p := range paths {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		switch {
		case pathutil.UnderOrEqual(p, srcDir):
			src = append(src, p)
		case pathutil.UnderOrEqual(p, destDir):
			dest = append(dest, p)
		}
	}
	return src, dest
}

func dropSourceMe(deps Deps, sourceScope, oldID string) error {
	if deps.ConfigDir == "" || deps.Cue == nil {
		return nil
	}
	lock, err := xdg.AcquireConfigLock(deps.ConfigDir)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()

	store := registry.NewStore(deps.Cue, deps.ConfigDir)
	reg, err := store.Load()
	if err != nil {
		return err
	}
	if reg.Me == nil {
		return nil
	}
	if reg.Me[sourceScope] != oldID {
		return nil
	}
	delete(reg.Me, sourceScope)
	return store.WriteMe(reg.Me)
}

func refuseSharedRootAutoCommitMismatch(src, dest rehomeSide) error {
	if !src.hasRoot || !dest.hasRoot || src.root != dest.root {
		return nil
	}
	if src.autoCommit == dest.autoCommit {
		return nil
	}
	return fmt.Errorf("%s", token.Line(token.AutoCommitMismatch,
		fmt.Sprintf("scopes sharing git-root %s disagree on autoCommit — split the divergent scope into its own repo", src.root)))
}

func completeRehome(deps Deps, src, dest rehomeSide, srcPaths, destPaths []string, oldID, destID string) (disabled, needed []string, err error) {
	type batch struct {
		scope string
		dir   string
		root  string
		paths []string
	}
	byRoot := map[string]*batch{}
	var rootOrder []string
	add := func(side rehomeSide, paths []string) {
		if !side.autoCommit {
			return
		}
		if len(paths) == 0 {
			return
		}
		if !side.hasRoot {
			disabled = append(disabled, fmt.Sprintf("%s: no git repository for %s — files written but not committed", side.name, side.dir))
			return
		}
		if existing, ok := byRoot[side.root]; ok {
			existing.paths = append(existing.paths, paths...)
			return
		}
		rootOrder = append(rootOrder, side.root)
		byRoot[side.root] = &batch{scope: side.name, dir: side.dir, root: side.root, paths: append([]string{}, paths...)}
	}
	if src.name < dest.name {
		add(src, srcPaths)
		add(dest, destPaths)
	} else {
		add(dest, destPaths)
		add(src, srcPaths)
	}

	message := fmt.Sprintf("tk: rehome %s -> %s", oldID, destID)
	for _, root := range rootOrder {
		b := byRoot[root]
		if err := selfcommit.CommitPaths(ctxOf(deps), selfcommit.BatchRequest{
			StateDir: deps.StateDir,
			GitRoot:  b.root,
			Message:  message,
			Paths:    b.paths,
		}); err != nil {
			return disabled, needed, fmt.Errorf("self-commit %s: %w", b.scope, err)
		}
		if reason := gitstate.SyncNeededReason(ctxOf(deps), deps.StateDir, b.dir, b.root); reason != "" {
			needed = append(needed, reason)
		}
	}
	return disabled, needed, nil
}

func edgeVerifyOthers(inbound []index.Edge, srcScope, destScope, newID string) []string {
	var out []string
	for _, ed := range inbound {
		if ed.FromScope == srcScope || ed.FromScope == destScope {
			continue
		}
		out = append(out, fmt.Sprintf("%s %s %s — target was rehomed to %s, update this reference",
			ed.FromID, ed.Kind, ed.ToID, newID))
	}
	return out
}
