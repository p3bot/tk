package design

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/p3bot/tk/internal/frontmatter"
	"github.com/p3bot/tk/internal/gitstate"
	"github.com/p3bot/tk/internal/id"
	"github.com/p3bot/tk/internal/pathutil"
	"github.com/p3bot/tk/internal/rewrite"
	"github.com/p3bot/tk/internal/scopeconfig"
	"github.com/p3bot/tk/internal/scopefile"
	"github.com/p3bot/tk/internal/selfcommit"
	"github.com/p3bot/tk/internal/token"
)

// RehomeInput moves one design into DestScope. The source is IDInput.
type RehomeInput struct {
	IDInput
	DestScope string
	DestDir   string
}

type rehomeSide struct {
	name       string
	dir        string
	autoCommit bool
	root       string
	hasRoot    bool
}

// Rehome moves one design file into dest's design directory. The slug stays.
// A free short id is kept; a real occupant is extended and left untouched.
// An interrupted dest design with the same slug and created is reused.
// Touched tk-driven roots self-commit. Rehome does not push.
func Rehome(deps Deps, in RehomeInput) (Result, error) {
	if err := validateDesignRehome(in); err != nil {
		return Result{}, err
	}
	destDir, err := destRehomeDir(deps, &in)
	if err != nil {
		return Result{}, err
	}

	release, err := scopefile.LockScopes(map[string]string{
		in.Scope:     in.Dir,
		in.DestScope: destDir,
	})
	if err != nil {
		return Result{}, err
	}
	defer release()

	src, err := deps.rehomeSide(in.Scope, in.Dir)
	if err != nil {
		return Result{}, err
	}
	dest, err := deps.rehomeSide(in.DestScope, destDir)
	if err != nil {
		return Result{}, err
	}
	if err := refuseDesignAutoCommitMismatch(src, dest); err != nil {
		return Result{}, err
	}

	f, err := resolveOne(in.IDInput)
	if err != nil {
		return Result{}, err
	}
	if f.ParseErr != nil {
		return Result{}, f.parseError()
	}

	sourceShort := strings.TrimPrefix(f.ID, in.Scope+"-")
	slug := designSlug(filepath.Base(f.Path))
	files, err := Files(destDir, in.DestScope)
	if err != nil {
		return Result{}, err
	}
	left, ok := findDesignLeftover(files, sourceShort, slug, f.Model.Created)

	var destID, destPath string
	if ok {
		destID = left.ID
		destPath = left.Path
	} else {
		occupied, err := scopefile.OccupiedShortIDs(destDir, in.DestScope)
		if err != nil {
			return Result{}, err
		}
		destShort := sourceShort
		if _, taken := occupied[destShort]; taken {
			destShort, err = id.Extend(sourceShort, occupied)
			if err != nil {
				return Result{}, fmt.Errorf("rehome %s into %s: %w", f.ID, in.DestScope, err)
			}
		}
		destID = in.DestScope + "-" + destShort
		destPath = filepath.Join(destDir, scopefile.DesignDir, designBasename(slug, destID))
	}

	content, err := designContent(f.Model, f.Body, destID)
	if err != nil {
		return Result{}, err
	}
	ops := []rewrite.Op{{OldPath: f.Path, NewPath: destPath, Content: content}}
	if ok && destPath != f.Path {
		// Dest already holds this design. Rewrite it in place, then drop the source.
		// A create-only move would refuse the different leftover bytes.
		ops = []rewrite.Op{
			{OldPath: destPath, NewPath: destPath, Content: content},
			{OldPath: f.Path, NewPath: destPath, Content: content},
		}
	}
	prior, err := captureRehome(f.Path, destPath)
	if err != nil {
		return Result{}, err
	}
	touched, err := rewrite.Apply(ops)
	if err != nil {
		return Result{}, err
	}

	srcPaths, destPaths := partitionRehome(in.Dir, destDir, touched)
	if err := deps.syncRehome(in.Scope, srcPaths, in.DestScope, destPaths); err != nil {
		return Result{}, deps.undoRehome(in.Scope, in.DestScope, prior, srcPaths, destPaths, nil, f.ID, destID, err)
	}
	abs, err := absPath(destPath)
	if err != nil {
		return Result{}, deps.undoRehome(in.Scope, in.DestScope, prior, srcPaths, destPaths, nil, f.ID, destID, err)
	}
	disabled, needed, done, err := deps.commitRehome(src, dest, srcPaths, destPaths, f.ID, destID)
	if err != nil {
		return Result{}, deps.undoRehome(in.Scope, in.DestScope, prior, srcPaths, destPaths, done, f.ID, destID, err)
	}
	return Result{ID: destID, Path: abs, SyncDisabledAll: disabled, SyncNeededAll: needed}, nil
}

func validateDesignRehome(in RehomeInput) error {
	if !id.IsScopeName(in.DestScope) {
		return &UsageError{Msg: fmt.Sprintf("%q is not a legal scope name (^[a-z0-9]{1,12}$)", in.DestScope)}
	}
	if in.DestScope == in.Scope {
		return &UsageError{Msg: fmt.Sprintf("destination scope %q is the design's current scope", in.DestScope)}
	}
	return nil
}

func destRehomeDir(deps Deps, in *RehomeInput) (string, error) {
	if deps.Reg == nil {
		return "", fmt.Errorf("unknown scope %q", in.DestScope)
	}
	entry, ok := deps.Reg.Scopes[in.DestScope]
	if !ok {
		return "", fmt.Errorf("unknown scope %q", in.DestScope)
	}
	if in.DestDir == "" {
		in.DestDir = entry.Dir
	}
	return in.DestDir, nil
}

func (d Deps) rehomeSide(scope, dir string) (rehomeSide, error) {
	if err := d.refuseUnusable(scope, dir); err != nil {
		return rehomeSide{}, err
	}
	schema, cfgErr := d.Rec.SchemaOrError(scope, dir)
	if cfgErr != nil {
		return rehomeSide{}, fmt.Errorf("%s", token.Line(token.ConfigUnparseable, fmt.Sprintf("%s (%s): %s — fix tk.cue before writing", scope, cfgErr.Dir, cfgErr.Reason)))
	}
	root, hasRoot := scopefile.GitRoot(dir)
	auto := scopeconfig.SchemaAutoCommit(schema)
	if err := gitstate.CheckMidRebase(d.ctx(), scope, auto, root, hasRoot); err != nil {
		return rehomeSide{}, err
	}
	return rehomeSide{name: scope, dir: dir, autoCommit: auto, root: root, hasRoot: hasRoot}, nil
}

func refuseDesignAutoCommitMismatch(src, dest rehomeSide) error {
	if !src.hasRoot || !dest.hasRoot || src.root != dest.root {
		return nil
	}
	if src.autoCommit == dest.autoCommit {
		return nil
	}
	return fmt.Errorf("%s", token.Line(token.AutoCommitMismatch,
		fmt.Sprintf("scopes sharing git-root %s disagree on autoCommit — split the divergent scope into its own repo", src.root)))
}

func findDesignLeftover(files []File, sourceShort, slug, created string) (File, bool) {
	if created == "" {
		return File{}, false
	}
	var best File
	bestShort := ""
	found := false
	for _, f := range files {
		if f.Model == nil || f.ParseErr != nil || f.Model.Created != created {
			continue
		}
		if designSlug(filepath.Base(f.Path)) != slug {
			continue
		}
		short := strings.TrimPrefix(f.ID, id.ScopeOfFullID(f.ID)+"-")
		if !shortIsSourceOrExtension(short, sourceShort) {
			continue
		}
		if !found || len(short) < len(bestShort) || (len(short) == len(bestShort) && f.Path < best.Path) {
			best = f
			bestShort = short
			found = true
		}
	}
	return best, found
}

func shortIsSourceOrExtension(short, source string) bool {
	if short == source {
		return true
	}
	return len(short) > len(source) && strings.HasPrefix(short, source)
}

func designSlug(base string) string {
	stem := strings.TrimSuffix(base, ".md")
	parts := strings.SplitN(stem, "-", 3)
	if len(parts) < 3 {
		return ""
	}
	return parts[2]
}

func designBasename(slug, newID string) string {
	if slug == "" {
		return newID + ".md"
	}
	return newID + "-" + slug + ".md"
}

func designContent(src *frontmatter.Model, body []byte, destID string) ([]byte, error) {
	next := *src
	next.ID = destID
	// Designs have no order key. Drop a stray one instead of carrying it across.
	next.Order = ""
	return writeModelBytes(&next, body)
}

func writeModelBytes(m *frontmatter.Model, body []byte) ([]byte, error) {
	interior, err := Serialize(m)
	if err != nil {
		return nil, err
	}
	return Compose(interior, body), nil
}

func partitionRehome(srcDir, destDir string, paths []string) (src, dest []string) {
	srcRoot := pathutil.Canonical(srcDir)
	destRoot := pathutil.Canonical(destDir)
	seen := map[string]bool{}
	for _, p := range paths {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		inSrc := pathutil.UnderOrEqual(p, srcDir)
		inDest := pathutil.UnderOrEqual(p, destDir)
		switch {
		case inSrc && inDest && len(destRoot) > len(srcRoot):
			dest = append(dest, p)
		case inSrc && inDest:
			src = append(src, p)
		case inSrc:
			src = append(src, p)
		case inDest:
			dest = append(dest, p)
		}
	}
	return src, dest
}

type rehomeBatch struct {
	scope string
	dir   string
	root  string
	paths []string
}

type rehomePrior struct {
	srcPath    string
	srcBytes   []byte
	destPath   string
	destBytes  []byte
	destExists bool
}

func captureRehome(srcPath, destPath string) (rehomePrior, error) {
	srcBytes, err := os.ReadFile(srcPath)
	if err != nil {
		return rehomePrior{}, err
	}
	prior := rehomePrior{srcPath: srcPath, srcBytes: srcBytes, destPath: destPath}
	destBytes, err := os.ReadFile(destPath)
	if err != nil {
		if os.IsNotExist(err) {
			return prior, nil
		}
		return rehomePrior{}, err
	}
	prior.destBytes = destBytes
	prior.destExists = true
	return prior, nil
}

func (p rehomePrior) restore() error {
	if err := writeFile(p.srcPath, p.srcBytes); err != nil {
		return err
	}
	if p.destPath == "" || p.destPath == p.srcPath {
		return nil
	}
	if !p.destExists {
		if err := os.Remove(p.destPath); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return writeFile(p.destPath, p.destBytes)
}

func (d Deps) syncRehome(srcScope string, srcPaths []string, destScope string, destPaths []string) error {
	if err := d.syncOne(srcScope, srcPaths); err != nil {
		return err
	}
	return d.syncOne(destScope, destPaths)
}

func (d Deps) syncOne(scope string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	if d.syncPaths != nil {
		return d.syncPaths(scope, paths)
	}
	if d.Rec == nil {
		return nil
	}
	return d.Rec.SyncPaths(scope, paths)
}

// undoRehome puts the previous files back. Roots that already committed the
// move commit this restoration so the failure is not their latest history.
func (d Deps) undoRehome(srcScope, destScope string, prior rehomePrior, srcPaths, destPaths []string, done []rehomeBatch, oldID, destID string, err error) error {
	if rerr := prior.restore(); rerr != nil {
		return fmt.Errorf("%w (also failed to restore rehome: %w)", err, rerr)
	}
	if cerr := d.commitUndo(done, oldID, destID); cerr != nil {
		if serr := d.syncRehome(srcScope, srcPaths, destScope, destPaths); serr != nil {
			return fmt.Errorf("%w (also failed to commit rehome undo: %w; also failed to resync rehome: %w)", err, cerr, serr)
		}
		return fmt.Errorf("%w (also failed to commit rehome undo: %w)", err, cerr)
	}
	if serr := d.syncRehome(srcScope, srcPaths, destScope, destPaths); serr != nil {
		return fmt.Errorf("%w (also failed to resync rehome: %w)", err, serr)
	}
	return err
}

func (d Deps) commitRehome(src, dest rehomeSide, srcPaths, destPaths []string, oldID, destID string) (disabled, needed []string, done []rehomeBatch, err error) {
	byRoot := map[string]*rehomeBatch{}
	var rootOrder []string
	add := func(side rehomeSide, paths []string) {
		if !side.autoCommit || len(paths) == 0 {
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
		byRoot[side.root] = &rehomeBatch{scope: side.name, dir: side.dir, root: side.root, paths: append([]string{}, paths...)}
	}
	if src.name < dest.name {
		add(src, srcPaths)
		add(dest, destPaths)
	} else {
		add(dest, destPaths)
		add(src, srcPaths)
	}
	message := fmt.Sprintf("tk: design rehome %s -> %s", oldID, destID)
	for _, root := range rootOrder {
		b := byRoot[root]
		if err := selfcommit.CommitPaths(d.ctx(), selfcommit.BatchRequest{
			StateDir: d.StateDir,
			GitRoot:  b.root,
			Message:  message,
			Paths:    b.paths,
		}); err != nil {
			return disabled, needed, done, fmt.Errorf("self-commit %s: %w", b.scope, err)
		}
		done = append(done, *b)
		if reason := gitstate.SyncNeededReason(d.ctx(), d.StateDir, b.dir, b.root); reason != "" {
			needed = append(needed, reason)
		}
	}
	return disabled, needed, done, nil
}

func (d Deps) commitUndo(done []rehomeBatch, oldID, destID string) error {
	if len(done) == 0 {
		return nil
	}
	message := fmt.Sprintf("tk: undo design rehome %s -> %s", oldID, destID)
	for _, b := range done {
		if err := selfcommit.CommitPaths(d.ctx(), selfcommit.BatchRequest{
			StateDir: d.StateDir,
			GitRoot:  b.root,
			Message:  message,
			Paths:    b.paths,
		}); err != nil {
			return fmt.Errorf("self-commit %s: %w", b.scope, err)
		}
	}
	return nil
}
