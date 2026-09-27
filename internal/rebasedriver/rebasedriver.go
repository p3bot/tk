// Package rebasedriver resolves one conflicted ticket .md at a paused rebase.
// Per-path only: does not run rebases or decide whether to continue. Enumerates
// stages, re-evaluates on-disk schema, calls pure frontmatter merge, 3-way merges
// bodies, and either stages the path or leaves it unstaged.
//
// Data conditions (body conflict, status dispute, delete/edit, fail-closed merge)
// return as Outcome with path unstaged. Operational faults return as error.
// Takes no lock — runs inside a caller-owned lock span.
package rebasedriver

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/p3bot/tk/internal/atomicfile"
	"github.com/p3bot/tk/internal/fmmerge"
	"github.com/p3bot/tk/internal/frontmatter"
	"github.com/p3bot/tk/internal/git"
	"github.com/p3bot/tk/internal/id"
	"github.com/p3bot/tk/internal/repair"
	"github.com/p3bot/tk/internal/rewrite"
	"github.com/p3bot/tk/internal/scopeconfig"
	"github.com/p3bot/tk/internal/scopefile"
)

const fileMode = 0o644

// SchemaLoader returns a scope's evaluated schema from on-disk tk.cue at call time.
// Called per conflicted file so merges see fields/statuses the incoming commit just added —
// never a schema snapshot captured earlier in the run.
type SchemaLoader func(scopeDir string) (*scopeconfig.Schema, error)

// Driver resolves conflicted ticket files across one rebase.
// Minted short-ids live on the receiver so later files see earlier add/add extensions.
type Driver struct {
	gitRoot string
	load    SchemaLoader
	minted  map[string]struct{}
}

// New builds a Driver for one rebase in gitRoot, typing merges through load.
func New(gitRoot string, load SchemaLoader) *Driver {
	return &Driver{gitRoot: gitRoot, load: load, minted: map[string]struct{}{}}
}

// Conflict names one conflicted ticket .md and the two side revs.
// OursRev is stage :2 (HEAD/upstream); TheirsRev is stage :3 (REBASE_HEAD).
// Mapping is inverted from everyday during-rebase "ours"/"theirs".
type Conflict struct {
	Path      string // repo-relative path to the conflicted ticket .md
	ScopeDir  string // absolute scope dir holding tk.cue
	OursRev   string // stage :2 side rev
	TheirsRev string // stage :3 side rev
}

// Class is the resolution class of a conflicted file.
type Class int

const (
	// ClassClean is a fully merged, parseable file the driver staged.
	ClassClean Class = iota
	// ClassBodyConflict is clean field-merged frontmatter with markers only in the body; unstaged.
	ClassBodyConflict
	// ClassStatusDispute is merge-base status plus status_conflict; unstaged.
	ClassStatusDispute
	// ClassDeleteEdit is a delete/edit handoff; nothing written or staged.
	ClassDeleteEdit
	// ClassRename is a same-id add/add resolved into two staged files.
	ClassRename
	// ClassFailClosed is a fail-closed merge; unstaged, key named.
	ClassFailClosed
)

// Outcome is the driver's report for one conflicted file.
type Outcome struct {
	Path           string
	Class          Class
	Staged         bool
	Warnings       []string
	StatusConflict []string    // ClassStatusDispute: the disputed pair
	DeleteEdit     *DeleteEdit // ClassDeleteEdit
	Rename         *Rename     // ClassRename
	FailClosed     *FailClosed // ClassFailClosed
}

// DeleteEdit reports which side deleted and the surviving post-edit status.
// Deleted uses the pure merge's side labels (ours = :2; theirs = :3).
type DeleteEdit struct {
	Deleted         fmmerge.Side
	SurvivingStatus string
}

// Rename reports a repaired same-id add/add: kept side at KeepPath with OldID,
// loser at NewPath with NewID. Edge verification is caller's index work.
type Rename struct {
	OldID    string
	NewID    string
	KeepPath string
	NewPath  string
}

// FailClosed reports a fail-closed merge as a human-resolvable pause (distinct from error return).
type FailClosed struct {
	Key    string
	Reason string
}

// Resolve merges one conflicted ticket file. Error is reserved for operational faults;
// human-resolvable data conditions are Outcome with Staged=false.
func (d *Driver) Resolve(ctx context.Context, c Conflict) (Outcome, error) {
	stages, err := git.ConflictStages(ctx, d.gitRoot, c.Path)
	if err != nil {
		return Outcome{}, fmt.Errorf("enumerate conflict stages for %s: %w", c.Path, err)
	}
	if !stages.Any() {
		return Outcome{}, fmt.Errorf("%s has no conflict stages", c.Path)
	}

	base, err := d.readStage(ctx, stages.Base, 1, c.Path)
	if err != nil {
		return Outcome{}, err
	}
	ours, err := d.readStage(ctx, stages.Ours, 2, c.Path)
	if err != nil {
		return Outcome{}, err
	}
	theirs, err := d.readStage(ctx, stages.Theirs, 3, c.Path)
	if err != nil {
		return Outcome{}, err
	}

	schema, err := d.load(c.ScopeDir)
	if err != nil {
		return Outcome{}, fmt.Errorf("evaluate scope schema for %s: %w", c.Path, err)
	}

	var oursDate, theirsDate time.Time
	if stages.Ours {
		if oursDate, err = git.AuthorDate(ctx, d.gitRoot, c.OursRev, c.Path); err != nil {
			return Outcome{}, fmt.Errorf("author date (ours) for %s: %w", c.Path, err)
		}
	}
	if stages.Theirs {
		if theirsDate, err = git.AuthorDate(ctx, d.gitRoot, c.TheirsRev, c.Path); err != nil {
			return Outcome{}, fmt.Errorf("author date (theirs) for %s: %w", c.Path, err)
		}
	}

	occupied, err := d.occupiedShortIDs(ctx, c.ScopeDir, schema.Name)
	if err != nil {
		return Outcome{}, err
	}

	res, mErr := fmmerge.MergeFrontmatter(base, ours, theirs, schema, fmmerge.MergeMeta{
		OursDate:   oursDate,
		TheirsDate: theirsDate,
		Scope:      schema.Name,
		Occupied:   occupied,
	})
	if mErr != nil {
		var me *fmmerge.MergeError
		if errors.As(mErr, &me) {
			return Outcome{Path: c.Path, Class: ClassFailClosed, FailClosed: &FailClosed{Key: me.Key, Reason: me.Reason}}, nil
		}
		return Outcome{}, mErr
	}

	switch res.Outcome {
	case fmmerge.OutcomeMerged:
		return d.compose(ctx, c, base, ours, theirs, res, false)
	case fmmerge.OutcomeStatusConflict:
		return d.compose(ctx, c, base, ours, theirs, res, true)
	case fmmerge.OutcomeDeleteEdit:
		return Outcome{
			Path:       c.Path,
			Class:      ClassDeleteEdit,
			Warnings:   res.Warnings,
			DeleteEdit: &DeleteEdit{Deleted: res.DeleteEdit.Deleted, SurvivingStatus: res.DeleteEdit.SurvivingStatus},
		}, nil
	case fmmerge.OutcomeRename:
		return d.applyRename(ctx, c, ours, theirs, schema.Name, res)
	default:
		return Outcome{}, fmt.Errorf("unknown merge outcome %d for %s", res.Outcome, c.Path)
	}
}

// readStage loads one stage blob when present. Non-zero exit is a genuine fault —
// never reinterpreted as absent (would silently reclassify as a deletion).
func (d *Driver) readStage(ctx context.Context, present bool, stage int, path string) (fmmerge.Stage, error) {
	if !present {
		return fmmerge.Stage{}, nil
	}
	data, err := git.ShowStage(ctx, d.gitRoot, stage, path)
	if err != nil {
		return fmmerge.Stage{}, fmt.Errorf("read stage :%d: for %s: %w", stage, path, err)
	}
	return fmmerge.Stage{Present: true, Data: data}, nil
}

// compose builds the merged file from stage blobs — never the conflicted working-tree
// file, whose whole-file text merge can span the fence and leave unparseable frontmatter.
// Clean body is staged; body conflict or status dispute is left unstaged.
func (d *Driver) compose(ctx context.Context, c Conflict, base, ours, theirs fmmerge.Stage, res fmmerge.Result, dispute bool) (Outcome, error) {
	_, baseBody, _ := frontmatter.Split(base.Data)
	_, oursBody, _ := frontmatter.Split(ours.Data)
	_, theirsBody, _ := frontmatter.Split(theirs.Data)
	mergedBody, bodyConflicted, err := git.MergeBlobs(ctx, baseBody, oursBody, theirsBody)
	if err != nil {
		return Outcome{}, fmt.Errorf("body merge for %s: %w", c.Path, err)
	}

	interior, err := frontmatter.Serialize(res.Model)
	if err != nil {
		return Outcome{}, fmt.Errorf("serialize merged frontmatter for %s: %w", c.Path, err)
	}
	content := frontmatter.Compose(interior, mergedBody)
	abs := filepath.Join(d.gitRoot, c.Path)
	if err := atomicfile.Write(abs, content, fileMode); err != nil {
		return Outcome{}, fmt.Errorf("write merged %s: %w", c.Path, err)
	}

	out := Outcome{Path: c.Path, Warnings: res.Warnings}
	switch {
	case dispute:
		out.Class = ClassStatusDispute
		out.StatusConflict = res.StatusConflict
	case bodyConflicted:
		out.Class = ClassBodyConflict
	default:
		if err := git.Add(ctx, d.gitRoot, []string{c.Path}); err != nil {
			return Outcome{}, fmt.Errorf("stage merged %s: %w", c.Path, err)
		}
		out.Class = ClassClean
		out.Staged = true
	}
	return out, nil
}

// applyRename repairs same-id add/add: writes kept blob and id-rewritten loser, stages both.
// Uses the shared loser pick already in res; does not re-run disk-backed duplicate-id repair.
func (d *Driver) applyRename(ctx context.Context, c Conflict, ours, theirs fmmerge.Stage, scope string, res fmmerge.Result) (Outcome, error) {
	keepBlob, loserBlob := ours.Data, theirs.Data
	if res.Rename.Loser == fmmerge.SideOurs {
		keepBlob, loserBlob = theirs.Data, ours.Data
	}
	newID := scope + "-" + res.Rename.NewShortID
	loserContent, err := rewriteID(loserBlob, newID)
	if err != nil {
		return Outcome{}, fmt.Errorf("rewrite loser id for %s: %w", c.Path, err)
	}

	abs := filepath.Join(d.gitRoot, c.Path)
	newPath := filepath.Join(filepath.Dir(abs), repair.Basename(filepath.Base(abs), newID))
	newRel, err := filepath.Rel(d.gitRoot, newPath)
	if err != nil {
		return Outcome{}, fmt.Errorf("relativise new path for %s: %w", c.Path, err)
	}

	if _, err := rewrite.Apply([]rewrite.Op{
		{OldPath: abs, NewPath: abs, Content: keepBlob},
		{NewPath: newPath, Content: loserContent},
	}); err != nil {
		return Outcome{}, fmt.Errorf("write add/add files for %s: %w", c.Path, err)
	}
	if err := git.Add(ctx, d.gitRoot, []string{c.Path, newRel}); err != nil {
		return Outcome{}, fmt.Errorf("stage add/add files for %s: %w", c.Path, err)
	}

	d.minted[res.Rename.NewShortID] = struct{}{}
	return Outcome{
		Path:   c.Path,
		Class:  ClassRename,
		Staged: true,
		Rename: &Rename{OldID: res.Rename.CollidedID, NewID: newID, KeepPath: c.Path, NewPath: newRel},
	}, nil
}

// occupiedShortIDs derives short-ids taken in a scope for add/add extension:
// tracked files under scope, on-disk ticket and design files, plus minted this rebase.
// Re-derived per file — a pre-fetch set is blind to incoming ids; a once-mid-rebase set is blind to later mints.
func (d *Driver) occupiedShortIDs(ctx context.Context, scopeDir, scope string) (map[string]struct{}, error) {
	occ := map[string]struct{}{}
	tracked, err := git.ListFiles(ctx, d.gitRoot, scopeDir)
	if err != nil {
		return nil, fmt.Errorf("list tracked files under %s: %w", scopeDir, err)
	}
	for _, f := range tracked {
		addShortID(occ, filepath.Base(f), scope)
	}
	disk, err := scopefile.OccupiedShortIDs(scopeDir, scope)
	if err != nil {
		return nil, fmt.Errorf("scan on-disk ids under %s: %w", scopeDir, err)
	}
	for s := range disk {
		occ[s] = struct{}{}
	}
	for s := range d.minted {
		occ[s] = struct{}{}
	}
	return occ, nil
}

// addShortID records a ticket basename's short-id when it is a ticket file for scope.
// Short-id is the second "-" segment; scope names and short-ids carry no hyphen.
func addShortID(occ map[string]struct{}, basename, scope string) {
	stem := strings.TrimSuffix(basename, ".md")
	if stem == basename {
		return // not a .md file
	}
	parts := strings.SplitN(stem, "-", 3)
	if len(parts) < 2 || parts[0] != scope || !id.IsShortID(parts[1]) {
		return
	}
	occ[parts[1]] = struct{}{}
}

// rewriteID re-serialises a stage blob with frontmatter id replaced, preserving body.
func rewriteID(blob []byte, newID string) ([]byte, error) {
	interior, body, present := frontmatter.Split(blob)
	if !present {
		return nil, fmt.Errorf("loser blob has no frontmatter fence")
	}
	m, err := frontmatter.Parse(interior)
	if err != nil {
		return nil, fmt.Errorf("parse loser blob: %w", err)
	}
	m.ID = newID
	ni, err := frontmatter.Serialize(m)
	if err != nil {
		return nil, err
	}
	return frontmatter.Compose(ni, body), nil
}
