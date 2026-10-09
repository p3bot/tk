package syncengine

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/p3bot/tk/internal/fmmerge"
	"github.com/p3bot/tk/internal/frontmatter"
	"github.com/p3bot/tk/internal/git"
	"github.com/p3bot/tk/internal/rebasedriver"
	"github.com/p3bot/tk/internal/scopeconfig"
	"github.com/p3bot/tk/internal/scopefile"
	"github.com/p3bot/tk/internal/token"
)

type integrateResult int

const (
	integrateCompleted integrateResult = iota
	integratePaused
	integrateError
)

// conflictStart is git's conflict-hunk opener. The separator (=======) and
// closer (>>>>>>>) are not sufficient: ======= is a markdown Setext underline.
var conflictStart = []byte("<<<<<<<")

// conflictKind: mutually exclusive; only kindSchema gates field-merge.
type conflictKind int

const (
	kindOther conflictKind = iota
	kindSchema
	kindIgnore
	kindTicket
	kindNote
	kindDesign
)

// isConfig: tk.cue or .gitignore; only kindSchema gates .md merges.
func (k conflictKind) isConfig() bool { return k == kindSchema || k == kindIgnore }

// skipDriveMD: human-resolved kinds reported on the first loop; never the ticket driver.
func (k conflictKind) skipDriveMD() bool {
	return k.isConfig() || k == kindNote || k == kindDesign
}

type conflictItem struct {
	path  string // repo-relative
	abs   string
	owner Participant
	kind  conflictKind
}

// fetchAndIntegrate fetches, classifies HEAD against @{u}, then fast-forwards,
// rebases, or leaves the branch. snap commits SnapshotScope at the class's moment:
// after a fast-forward, before a diverged rebase, and instead of a rebase when
// the checkout is already based on upstream. snap false takes no snapshot (claim
// refresh and the push-reject retry).
//
// Rebase only when both sides already have commits. A checkout already based on
// upstream, including equal, is a push: git rebase refuses to start while any
// tracked file is dirty, so another scope's unsaved file would block an update
// that replays nothing. Do not commit the named scope before a fast-forward
// either, including when the fast-forward refuses an overwrite. That commit
// turns a strictly-behind checkout into a diverged one, and the rebase then
// refuses because of the other scope's dirty file. Leave HEAD unmoved and
// commit nothing.
func fetchAndIntegrate(deps Deps, r Reporter, t Target, rep *syncReport, snap bool) integrateResult {
	ctx := deps.Ctx
	if err := git.Fetch(ctx, t.Root); err != nil {
		r.Err(fmt.Sprintf("%s: fetch failed: %v", rep.label, err))
		return integrateError
	}
	rel, err := git.CompareUpstream(ctx, t.Root)
	if err != nil {
		r.Err(fmt.Sprintf("%s: could not classify upstream: %v", rep.label, err))
		return integrateError
	}
	switch rel {
	case git.RelBased:
		return snapshotThen(deps, r, t, rep, snap, integrateCompleted)
	case git.RelBehind:
		if err := fastForward(deps, r, t, rep); err != nil {
			return integrateError
		}
		return snapshotThen(deps, r, t, rep, snap, integrateCompleted)
	default:
		if res := snapshotThen(deps, r, t, rep, snap, integrateCompleted); res != integrateCompleted {
			return res
		}
		return rebaseOntoUpstream(deps, r, t, rep)
	}
}

func snapshotThen(deps Deps, r Reporter, t Target, rep *syncReport, snap bool, done integrateResult) integrateResult {
	if !snap {
		return done
	}
	if err := snapshot(deps, r, t, rep); err != nil {
		r.Err(fmt.Sprintf("%s: snapshot failed: %v", rep.label, err))
		return integrateError
	}
	return done
}

func fastForward(deps Deps, r Reporter, t Target, rep *syncReport) error {
	// git merge --ff-only writes a deleted worktree file back when upstream changed that path.
	// An unchanged path keeps the local deletion.
	blocked, err := deletionsUpstreamWouldRestore(deps.Ctx, t.Root)
	if err != nil {
		r.Err(fmt.Sprintf("%s: fast-forward failed: %v", rep.label, err))
		return err
	}
	if len(blocked) > 0 {
		r.Err(fmt.Sprintf("%s: fast-forward refused; local changes would be overwritten:", rep.label))
		for _, path := range blocked {
			r.Err("  " + path)
		}
		return fmt.Errorf("fast-forward refused")
	}
	err = git.MergeFFOnly(deps.Ctx, t.Root, "@{u}")
	if err == nil {
		return nil
	}
	paths := overwrittenPaths(err.Error())
	if len(paths) > 0 {
		r.Err(fmt.Sprintf("%s: fast-forward refused; local changes would be overwritten:", rep.label))
		for _, path := range paths {
			r.Err("  " + path)
		}
		return err
	}
	r.Err(fmt.Sprintf("%s: fast-forward failed: %v", rep.label, err))
	return err
}

func rebaseOntoUpstream(deps Deps, r Reporter, t Target, rep *syncReport) integrateResult {
	ctx := deps.Ctx
	paused, err := git.Rebase(ctx, t.Root, "@{u}")
	if err != nil {
		if git.MidRebase(ctx, t.Root) {
			if abortErr := git.RebaseAbort(ctx, t.Root); abortErr != nil {
				r.Err(fmt.Sprintf("%s: rebase failed to start and could not abort: %v", rep.label, abortErr))
			}
		}
		if rebaseBlockedByDirt(err) {
			paths := rebaseBlockers(ctx, t.Root, err)
			r.Err(fmt.Sprintf("%s: rebase did not start; dirty path blocks it:", rep.label))
			if len(paths) == 0 {
				r.Err(fmt.Sprintf("  %v", err))
			}
			for _, path := range paths {
				r.Err("  " + path)
			}
			return integrateError
		}
		r.Err(fmt.Sprintf("%s: rebase failed: %v", rep.label, err))
		return integrateError
	}
	if !paused {
		return integrateCompleted
	}
	driver := newDriver(deps, t)
	return runStops(deps, r, t, driver, rep, func() (bool, error) {
		return driveStop(deps, r, t, driver, rep)
	})
}

func rebaseBlockedByDirt(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "cannot rebase") ||
		strings.Contains(msg, "would be overwritten")
}

func rebaseBlockers(ctx context.Context, gitRoot string, cause error) []string {
	seen := map[string]bool{}
	var paths []string
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		paths = append(paths, path)
	}
	for _, path := range overwrittenPaths(cause.Error()) {
		add(path)
	}
	if strings.Contains(cause.Error(), "cannot rebase") {
		entries, err := git.DirtyEntries(ctx, gitRoot, ".")
		if err == nil {
			for _, ent := range entries {
				if ent.Code == "??" || ent.Code == "!!" {
					continue
				}
				add(repoRel(gitRoot, ent.Path))
			}
		}
	}
	sort.Strings(paths)
	return paths
}

// overwrittenPaths reads the path list git prints between a "would be overwritten"
// line and the following instruction.
func overwrittenPaths(msg string) []string {
	var paths []string
	capture := false
	for _, line := range strings.Split(msg, "\n") {
		trim := strings.TrimSpace(line)
		if strings.Contains(trim, "would be overwritten") {
			capture = true
			continue
		}
		if !capture {
			continue
		}
		if trim == "" || strings.HasPrefix(trim, "Please ") || strings.HasPrefix(trim, "Aborting") ||
			strings.HasPrefix(trim, "Updating") || strings.HasPrefix(trim, "error:") {
			capture = false
			continue
		}
		paths = append(paths, trim)
	}
	return paths
}

// deletionsUpstreamWouldRestore lists worktree deletions that a fast-forward would write back.
// That happens when @{u} still has the path and its tree entry differs from HEAD.
// A same entry keeps the local deletion. A deletion upstream also made is absent
// there, so the fast-forward leaves the file gone.
func deletionsUpstreamWouldRestore(ctx context.Context, gitRoot string) ([]string, error) {
	entries, err := git.DirtyEntries(ctx, gitRoot, ".")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, ent := range entries {
		if !worktreeGone(ent.Code) {
			continue
		}
		rel := repoRel(gitRoot, ent.Path)
		present, err := git.TreeContains(ctx, gitRoot, "@{u}", rel)
		if err != nil {
			return nil, err
		}
		if !present {
			continue
		}
		changed, err := git.PathChanged(ctx, gitRoot, "HEAD", "@{u}", rel)
		if err != nil {
			return nil, err
		}
		if changed {
			paths = append(paths, rel)
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func worktreeGone(code string) bool {
	if len(code) < 2 {
		return false
	}
	return code[1] == 'D' || (code[0] == 'D' && code[1] == ' ')
}

func repoRel(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

// resumeRebase: mid-rebase entry skips snapshot (no commit on temporary HEAD).
func resumeRebase(deps Deps, r Reporter, t Target, rep *syncReport) integrateResult {
	driver := newDriver(deps, t)
	return runStops(deps, r, t, driver, rep, func() (bool, error) {
		return resolveResumeStop(deps, r, t, driver, rep)
	})
}

func runStops(deps Deps, r Reporter, t Target, driver *rebasedriver.Driver, rep *syncReport, first func() (bool, error)) integrateResult {
	ctx := deps.Ctx
	allStaged, err := first()
	if err != nil {
		r.Err(fmt.Sprintf("%s: %v", rep.label, err))
		return integrateError
	}
	for {
		if !allStaged {
			return integratePaused
		}
		paused, err := git.RebaseContinue(ctx, t.Root)
		if err != nil {
			r.Err(fmt.Sprintf("%s: rebase --continue failed: %v", rep.label, err))
			return integrateError
		}
		if !paused {
			return integrateCompleted
		}
		allStaged, err = driveStop(deps, r, t, driver, rep)
		if err != nil {
			r.Err(fmt.Sprintf("%s: %v", rep.label, err))
			return integrateError
		}
	}
}

// driveStop: schema-before-data; only conflicted tk.cue fail-closes ticket .md.
func driveStop(deps Deps, r Reporter, t Target, driver *rebasedriver.Driver, rep *syncReport) (bool, error) {
	ctx := deps.Ctx
	items := classifyStop(ctx, t)
	allStaged := true

	schemaConflicted := map[string]bool{}
	for _, it := range items {
		if !it.kind.skipDriveMD() {
			continue
		}
		if it.kind == kindSchema {
			schemaConflicted[it.owner.Dir] = true
		}
		stages, err := git.ConflictStages(ctx, t.Root, it.path)
		if err != nil {
			return false, fmt.Errorf("enumerate conflict stages for %s: %w", it.path, err)
		}
		reportHumanConflict(r, it, configDeleteEditSide(stages))
		allStaged = false
	}

	head, rebaseHead, err := git.RebaseSides(ctx, t.Root)
	if err != nil {
		return false, fmt.Errorf("resolve rebase sides: %w", err)
	}
	for _, it := range items {
		if mdItemBlocked(r, it, schemaConflicted, &allStaged) {
			continue
		}
		if err := driveMD(ctx, r, driver, it, head, rebaseHead, rep, &allStaged); err != nil {
			return false, err
		}
	}
	return allStaged, nil
}

// reportHumanConflict: first-loop kinds (schema, ignore, notes). config_unparseable only for tk.cue.
func reportHumanConflict(r Reporter, it conflictItem, deletedSide string) {
	if deletedSide != "" {
		if it.kind == kindSchema {
			r.Err(token.Line(token.ConfigUnparseable, fmt.Sprintf(
				"%s: delete/edit conflict: %s was deleted on %s while the other side edited it — edit %s or git add it to keep as-is, then run tk sync (making the deletion win takes the scope out of tk's hands first: while a registered scope has no schema, sync refuses the root)",
				it.owner.Name, it.path, deletedSide, it.path)))
			return
		}
		r.Err(fmt.Sprintf(
			"delete/edit conflict: %s was deleted on %s while the other side edited it — remove %s, edit it, or git add it to keep as-is, then run tk sync",
			it.path, deletedSide, it.path))
		return
	}
	if it.kind == kindSchema {
		r.Err(token.Line(token.ConfigUnparseable, fmt.Sprintf(
			"%s: conflicted tk.cue — resolve %s in place, then run tk sync", it.owner.Name, it.path)))
		return
	}
	if it.kind == kindNote {
		r.Err(fmt.Sprintf(
			"conflicted note: resolve the conflict markers in %s, then run tk sync", it.path))
		return
	}
	if it.kind == kindDesign {
		r.Err(fmt.Sprintf(
			"conflicted design: resolve the conflict markers in %s, then run tk sync", it.path))
		return
	}
	r.Err(fmt.Sprintf(
		"conflicted .gitignore: resolve the conflict markers in %s, then run tk sync", it.path))
}

func mdItemBlocked(r Reporter, it conflictItem, schemaConflicted map[string]bool, allStaged *bool) bool {
	switch it.kind {
	case kindOther:
		// A path tk cannot classify or own: leave the rebase paused and name it, so the closing "resolve the file(s)
		r.Err(fmt.Sprintf(
			"unresolvable conflict: resolve the conflict markers in %s, then run tk sync", it.path))
		*allStaged = false
		return true
	case kindSchema, kindIgnore, kindNote, kindDesign:
		return true
	}
	if schemaConflicted[it.owner.Dir] {
		r.Err(token.Line(token.ConfigUnparseable, fmt.Sprintf(
			"%s: %s not merged — its scope's tk.cue is conflicted; resolve tk.cue first", it.owner.Name, it.path)))
		*allStaged = false
		return true
	}
	return false
}

func driveMD(ctx context.Context, r Reporter, driver *rebasedriver.Driver, it conflictItem, head, rebaseHead string, rep *syncReport, allStaged *bool) error {
	outcome, derr := driver.Resolve(ctx, rebasedriver.Conflict{
		Path: it.path, ScopeDir: it.owner.Dir, OursRev: head, TheirsRev: rebaseHead,
	})
	if derr != nil {
		return derr
	}
	if !applyDriverOutcome(r, outcome, rep) {
		*allStaged = false
	}
	return nil
}

func resolveResumeStop(deps Deps, r Reporter, t Target, driver *rebasedriver.Driver, rep *syncReport) (bool, error) {
	ctx := deps.Ctx
	items := classifyStop(ctx, t)
	allStaged := true

	schemaConflicted := map[string]bool{}
	for _, it := range items {
		if !it.kind.skipDriveMD() {
			continue
		}
		stages, err := git.ConflictStages(ctx, t.Root, it.path)
		if err != nil {
			return false, fmt.Errorf("enumerate conflict stages for %s: %w", it.path, err)
		}
		if isDeleteEditStages(stages) {
			acted, err := stageDeleteEditIfActed(ctx, t.Root, it.path, it.abs, stages)
			if err != nil {
				return false, err
			}
			if acted {
				continue
			}
			// Unactioned: re-report and, for tk.cue, keep the scope's ticket .md fail-closed.
			if it.kind == kindSchema {
				schemaConflicted[it.owner.Dir] = true
			}
			reportHumanConflict(r, it, configDeleteEditSide(stages))
			allStaged = false
			continue
		}
		if fileHasConflictMarkers(it.abs) {
			if it.kind == kindSchema {
				schemaConflicted[it.owner.Dir] = true
			}
			reportHumanConflict(r, it, "")
			allStaged = false
			continue
		}
		if err := git.Add(ctx, t.Root, []string{it.path}); err != nil {
			return false, fmt.Errorf("stage resolved %s: %w", it.path, err)
		}
	}

	head, rebaseHead, err := git.RebaseSides(ctx, t.Root)
	if err != nil {
		return false, fmt.Errorf("resolve rebase sides: %w", err)
	}
	for _, it := range items {
		if mdItemBlocked(r, it, schemaConflicted, &allStaged) {
			continue
		}
		stages, err := git.ConflictStages(ctx, t.Root, it.path)
		if err != nil {
			return false, fmt.Errorf("enumerate conflict stages for %s: %w", it.path, err)
		}
		if isDeleteEditStages(stages) {
			acted, err := stageDeleteEditIfActed(ctx, t.Root, it.path, it.abs, stages)
			if err != nil {
				return false, err
			}
			if acted {
				continue
			}
			if err := driveMD(ctx, r, driver, it, head, rebaseHead, rep, &allStaged); err != nil {
				return false, err
			}
			continue
		}
		if FrontmatterHasMarkers(it.abs) {
			if err := driveMD(ctx, r, driver, it, head, rebaseHead, rep, &allStaged); err != nil {
				return false, err
			}
			continue
		}
		if FrontmatterHasStatusConflict(it.abs) {
			r.Err(token.Line(token.StatusConflict, fmt.Sprintf(
				"%s: unresolved status_conflict — set status to one value and delete status_conflict in %s, then run tk sync", it.owner.Name, it.path)))
			allStaged = false
			continue
		}
		if err := git.Add(ctx, t.Root, []string{it.path}); err != nil {
			return false, fmt.Errorf("stage resolved %s: %w", it.path, err)
		}
	}
	return allStaged, nil
}

func applyDriverOutcome(r Reporter, o rebasedriver.Outcome, rep *syncReport) bool {
	for _, w := range o.Warnings {
		r.Err(w)
	}
	switch o.Class {
	case rebasedriver.ClassClean:
		return true
	case rebasedriver.ClassRename:
		rep.collidedIDs = append(rep.collidedIDs, o.Rename.OldID)
		r.Out(fmt.Sprintf("repaired add/add duplicate: %s kept, renamed to %s (%s)",
			o.Rename.OldID, o.Rename.NewID, o.Rename.NewPath))
		return true
	case rebasedriver.ClassBodyConflict:
		r.Err(fmt.Sprintf("body conflict: resolve the merge markers in the body of %s, then run tk sync", o.Path))
		return false
	case rebasedriver.ClassStatusDispute:
		r.Err(token.Line(token.StatusConflict, fmt.Sprintf(
			"%s: %s — set status to one value and delete status_conflict in %s, then run tk sync",
			o.Path, strings.Join(o.StatusConflict, " vs "), o.Path)))
		return false
	case rebasedriver.ClassDeleteEdit:
		r.Err(fmt.Sprintf(
			"delete/edit conflict: %s was deleted on %s while the other side edited it (status %q) — remove %s, edit it, or git add it to keep as-is, then run tk sync",
			o.Path, deleteEditStageLabel(sideToStage(o.DeleteEdit.Deleted)), o.DeleteEdit.SurvivingStatus, o.Path))
		return false
	case rebasedriver.ClassFailClosed:
		r.Err(token.Line(token.ConfigUnparseable, fmt.Sprintf(
			"%s: merge failed closed on key %q (%s) — resolve %s in place, then run tk sync",
			o.Path, o.FailClosed.Key, o.FailClosed.Reason, o.Path)))
		return false
	case rebasedriver.ClassAmbiguousBase:
		r.Err(fmt.Sprintf(
			"ambiguous base: more than one deleted file has id %s for %s — resolve %s in place, then run tk sync",
			o.AmbiguousBase.ID, o.Path, o.Path))
		return false
	default:
		return false
	}
}

// newDriver loads schema from on-disk tk.cue at call time, never a pre-fetch snapshot.
func newDriver(deps Deps, t Target) *rebasedriver.Driver {
	dirToScope := make(map[string]string, len(t.Participants))
	for _, p := range t.Participants {
		dirToScope[p.Dir] = p.Name
	}
	load := func(scopeDir string) (*scopeconfig.Schema, error) {
		name, ok := dirToScope[scopeDir]
		if !ok {
			return scopeconfig.Load(deps.Cue, scopeDir)
		}
		schema, cfgErr := deps.Rec.SchemaOrError(name, scopeDir)
		if cfgErr != nil {
			return nil, cfgErr
		}
		return schema, nil
	}
	return rebasedriver.New(t.Root, load)
}

func classifyStop(ctx context.Context, t Target) []conflictItem {
	var items []conflictItem
	for _, path := range git.UnmergedFiles(ctx, t.Root) {
		it := conflictItem{path: path, abs: filepath.Join(t.Root, path)}
		if p, ok := owningParticipant(path, t); ok {
			it.owner = p
			it.kind = classifyConflict(it.abs, p)
		}
		items = append(items, it)
	}
	return items
}

func classifyConflict(abs string, p Participant) conflictKind {
	if !scopefile.IsAllowlisted(abs, p.Dir) {
		return kindOther
	}
	switch filepath.Base(abs) {
	case "tk.cue":
		return kindSchema
	case ".gitignore":
		return kindIgnore
	}
	if _, ok := scopefile.NoteSlug(abs, p.Dir); ok {
		return kindNote
	}
	if _, ok := scopefile.DesignFullID(abs, p.Dir); ok {
		return kindDesign
	}
	return kindTicket
}

func owningParticipant(repoRelPath string, t Target) (Participant, bool) {
	for _, p := range t.Participants {
		rel, err := filepath.Rel(t.Root, p.Dir)
		if err != nil {
			continue
		}
		if rel == "." || repoRelPath == rel || strings.HasPrefix(repoRelPath, rel+string(filepath.Separator)) {
			return p, true
		}
	}
	return Participant{}, false
}

// isDeleteEditStages: stage-set separates delete/edit from driver output (both sides still in index).
func isDeleteEditStages(s git.Stages) bool {
	return s.Base && s.Ours != s.Theirs
}

func survivingStageNumber(s git.Stages) int {
	if s.Ours {
		return 2
	}
	return 3
}

func configDeleteEditSide(s git.Stages) string {
	if !isDeleteEditStages(s) {
		return ""
	}
	// Survivor present ⇒ the other stage deleted.
	if s.Ours {
		return deleteEditStageLabel(3)
	}
	return deleteEditStageLabel(2)
}

func sideToStage(s fmmerge.Side) int {
	if s == fmmerge.SideTheirs {
		return 3
	}
	return 2
}

// deleteEditStageLabel: stage 2 = incoming upstream; stage 3 = local replay.
func deleteEditStageLabel(stage int) string {
	switch stage {
	case 2:
		return "the incoming side (fetched from the remote)"
	case 3:
		return "this machine's replayed commit"
	default:
		return fmt.Sprintf("stage %d", stage)
	}
}

// stageDeleteEditIfActed: unactioned (worktree == survivor blob) does not stage.
func stageDeleteEditIfActed(ctx context.Context, gitRoot, repoPath, abs string, s git.Stages) (acted bool, err error) {
	blob, err := git.ShowStage(ctx, gitRoot, survivingStageNumber(s), repoPath)
	if err != nil {
		return false, fmt.Errorf("read surviving stage for %s: %w", repoPath, err)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			if err := git.Add(ctx, gitRoot, []string{repoPath}); err != nil {
				return false, fmt.Errorf("stage deletion of %s: %w", repoPath, err)
			}
			return true, nil
		}
		return false, fmt.Errorf("read worktree %s: %w", repoPath, err)
	}
	if bytes.Equal(data, blob) {
		return false, nil
	}
	if err := git.Add(ctx, gitRoot, []string{repoPath}); err != nil {
		return false, fmt.Errorf("stage resolved %s: %w", repoPath, err)
	}
	return true, nil
}

func fileHasConflictMarkers(abs string) bool {
	data, err := os.ReadFile(abs)
	if err != nil {
		return false
	}
	return HasConflictMarker(data)
}

// FrontmatterHasMarkers reports whether path's YAML fence (or whole file if no
// fence) still carries git conflict markers. Shared by resume and CLI merge tests.
func FrontmatterHasMarkers(abs string) bool {
	data, err := os.ReadFile(abs)
	if err != nil {
		return false
	}
	interior, _, present := frontmatter.Split(data)
	if !present {
		return true
	}
	return HasConflictMarker(interior)
}

// FrontmatterHasStatusConflict reports whether path's frontmatter still lists
// status_conflict. Shared by resume and CLI merge tests.
func FrontmatterHasStatusConflict(abs string) bool {
	data, err := os.ReadFile(abs)
	if err != nil {
		return false
	}
	interior, _, present := frontmatter.Split(data)
	if !present {
		return false
	}
	m, err := frontmatter.Parse(interior)
	if err != nil {
		return false
	}
	return len(m.StatusConflict) > 0
}

// HasConflictMarker reports whether data contains a line starting with git's
// conflict opener (<<<<<<<). Shared by resume and CLI merge tests.
func HasConflictMarker(data []byte) bool {
	for len(data) > 0 {
		var line []byte
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			line, data = data[:i], data[i+1:]
		} else {
			line, data = data, nil
		}
		if bytes.HasPrefix(line, conflictStart) {
			return true
		}
	}
	return false
}
