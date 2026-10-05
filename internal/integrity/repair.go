package integrity

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/p3bot/tk/internal/design"
	"github.com/p3bot/tk/internal/frontmatter"
	"github.com/p3bot/tk/internal/git"
	"github.com/p3bot/tk/internal/gitstate"
	"github.com/p3bot/tk/internal/id"
	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/reconcile"
	"github.com/p3bot/tk/internal/registry"
	"github.com/p3bot/tk/internal/repair"
	"github.com/p3bot/tk/internal/rewrite"
	"github.com/p3bot/tk/internal/scopeconfig"
	"github.com/p3bot/tk/internal/scopefile"
	"github.com/p3bot/tk/internal/selfcommit"
	"github.com/p3bot/tk/internal/status"
	"github.com/p3bot/tk/internal/token"
)

// Reporter receives progress and diagnostic lines (stdout-class Out, stderr-class Err).
type Reporter interface {
	Out(line string)
	Err(line string)
}

// Flags select which repair batches to run and whether multi-scope isolation applies.
type Flags struct {
	Repair       bool
	ReSpaceOrder bool
	// All: mid-rebase skips instead of hard-refusing (repair --all).
	All bool
}

// Target holds values resolved once under flock for the whole repair run.
// Sync integrity builds this while already holding both locks.
type Target struct {
	Scope      string
	Dir        string
	Schema     *scopeconfig.Schema
	AutoCommit bool
	Root       string
	HasRoot    bool
}

// RepairScopes runs the acquiring repair path for each named scope present in deps.Reg.
func RepairScopes(deps Deps, rep Reporter, scopes []string, f Flags) error {
	for _, scope := range scopes {
		entry, ok := deps.Reg.Scopes[scope]
		if !ok {
			continue
		}
		if err := RepairScope(deps, rep, scope, entry.Dir, f); err != nil {
			return err
		}
	}
	return nil
}

// RepairScope acquires scope flock (+ git-root lock for auto-commit) across reconcile and batches.
func RepairScope(deps Deps, rep Reporter, scope, dir string, f Flags) error {
	// Stat before flock: flock creates a file and would abort --all on one unmounted drive.
	if _, err := os.Stat(dir); err != nil {
		rep.Err(fmt.Sprintf("skipping %s: dir unreachable", scope))
		return nil
	}
	lock, err := scopefile.AcquireLock(dir)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()

	res, err := deps.Rec.Reconcile(map[string]string{scope: dir}, registeredSet(deps.Reg), time.Now().UnixNano())
	if err != nil {
		return err
	}
	t, err := repairPreflight(deps, rep, scope, dir, res, f)
	if err != nil || t == nil {
		return err
	}

	if t.AutoCommit && t.HasRoot {
		gitLock, err := gitstate.AcquireCommitLock(deps.StateDir, t.Root)
		if err != nil {
			return err
		}
		defer func() { _ = gitLock.Release() }()
	}
	return RunBatches(deps, rep, t, f)
}

// RunBatches is the locks-held core (repair acquires; sync already holds both).
// Caller must hold the scope flock and, for auto-commit git-roots, the commit lock.
func RunBatches(deps Deps, rep Reporter, t *Target, f Flags) error {
	if f.Repair {
		// Prefix adoption first, so a file that was invisible becomes a ticket
		// the archive and collision passes can see.
		if err := repairPrefixes(deps, rep, t); err != nil {
			return err
		}
		if err := repairArchive(deps, rep, t, false); err != nil {
			return err
		}
		if err := repairCollisions(deps, rep, t); err != nil {
			return err
		}
		// Second layout pass: land moves deferred until collisions got distinct basenames.
		if err := repairArchive(deps, rep, t, true); err != nil {
			return err
		}
		if err := repairEqualOrder(deps, rep, t); err != nil {
			return err
		}
	}
	if f.ReSpaceOrder {
		if err := repairLongOrder(deps, rep, t); err != nil {
			return err
		}
	}
	return nil
}

// repairPreflight: nil,nil is a skip; mid-rebase hard-refuses ambient, skips under --all.
func repairPreflight(deps Deps, rep Reporter, scope, dir string, res *reconcile.Result, f Flags) (*Target, error) {
	if res.Unreachable[scope] {
		rep.Err(fmt.Sprintf("skipping %s: dir unreachable", scope))
		return nil, nil
	}
	if cueName, err := scopeconfig.ReadName(deps.Cue, dir); err == nil && cueName != scope {
		rep.Err(token.Line(token.NameDrift, fmt.Sprintf("skipping %s: registry key %q but tk.cue name is %q — recover with tk scope forget/import", scope, scope, cueName)))
		return nil, nil
	}
	if _, bad := res.ConfigErrs[scope]; bad {
		rep.Err(token.Line(token.ConfigUnparseable, fmt.Sprintf("skipping %s: fix tk.cue before repairing", scope)))
		return nil, nil
	}

	schema := res.Schema(scope)
	t := &Target{Scope: scope, Dir: dir, Schema: schema, AutoCommit: schemaAutoCommit(schema)}
	t.Root, t.HasRoot = scopefile.GitRoot(dir)
	if t.AutoCommit && t.HasRoot && git.MidRebase(deps.Ctx, t.Root) {
		if !f.All {
			return nil, midRebaseRefusal(deps.Ctx, scope, t.Root)
		}
		rep.Err(fmt.Sprintf("skipping %s: git-root %s is mid-rebase — resolve then re-run", scope, t.Root))
		return nil, nil
	}
	return t, nil
}

func registeredSet(reg *registry.Registry) map[string]bool {
	out := make(map[string]bool, len(reg.Scopes))
	for name := range reg.Scopes {
		out[name] = true
	}
	return out
}

// sharedRepair is one design_id group this run extended.
// retarget is the new ticket id when links moved, or empty when they stayed.
type sharedRepair struct {
	oldID    string
	retarget string
}

// repairCollisions resolves design_id groups first, then ticket-only duplicate ids.
// A short id with a design holder is not also repaired as duplicate_id.
// edge_verify is read after every rename so referrer ids are post-repair.
func repairCollisions(deps Deps, rep Reporter, t *Target) error {
	rows, err := deps.DB.ScopeTickets(t.Scope)
	if err != nil {
		return err
	}
	designs, err := deps.DB.ScopeDesigns(t.Scope)
	if err != nil {
		return err
	}
	occupied := shortIDPaths(rows)
	disk, err := scopefile.OccupiedShortPaths(t.Dir, t.Scope)
	if err != nil {
		return err
	}
	for short, path := range disk {
		if _, ok := occupied[short]; !ok {
			occupied[short] = path
		}
	}

	groups := sharedGroups(t.Scope, rows, designs)
	held := map[string]bool{}
	var shared []sharedRepair
	for _, g := range groups {
		held[g.short] = true
		done, err := repairSharedGroup(deps, rep, t, g, occupied)
		if err != nil {
			return err
		}
		if done != nil {
			shared = append(shared, *done)
		}
	}

	dups, err := deps.DB.DuplicateIDs([]string{t.Scope})
	if err != nil {
		return err
	}
	byPath := map[string]*index.Ticket{}
	if len(dups) > 0 {
		rows, err = deps.DB.ScopeTickets(t.Scope)
		if err != nil {
			return err
		}
		for _, p := range rows {
			byPath[p.Path] = p
		}
	}

	var repaired []string
	for _, col := range dups {
		if held[shortOfFull(col.Key)] {
			continue
		}
		members := rowsForPaths(byPath, col.Members)
		mid, err := repair.InterruptedMove(t.Dir, toRepairRows(members))
		if err != nil {
			return err
		}
		if mid {
			rep.Err(fmt.Sprintf("skipping %s: unfinished archive-layout move, not a collision — re-run tk repair to complete it", col.Key))
			continue
		}
		if anyParseError(members) {
			rep.Err(token.Line(token.ParseError, fmt.Sprintf("%s: collision includes a quarantined file — fix its frontmatter before repair", col.Key)))
			continue
		}
		ops, renames, err := repair.DuplicateID(t.Scope, toRepairRows(members), occupied)
		if err != nil {
			return err
		}
		if err := applyRepairBatch(deps, rep, t, ops, collisionMessage(renames)); err != nil {
			return err
		}
		for _, r := range renames {
			rep.Out(fmt.Sprintf("repaired duplicate id: %s -> %s (%s)", r.OldID, r.NewID, r.NewPath))
		}
		repaired = append(repaired, col.Key)
	}
	if err := ReportEdgeVerify(deps, rep, repaired); err != nil {
		return err
	}
	return reportSharedEdges(deps, rep, t.Scope, shared)
}

type sharedGroup struct {
	short string
	full  string
	rows  []repair.SharedRow
}

func sharedGroups(scope string, tickets []*index.Ticket, designs []*index.Design) []sharedGroup {
	byShort := map[string][]repair.SharedRow{}
	for _, p := range tickets {
		if !id.IsShortID(p.ShortID) {
			continue
		}
		byShort[p.ShortID] = append(byShort[p.ShortID], repair.SharedRow{Row: toRepairRow(p)})
	}
	for _, p := range designs {
		if !id.IsShortID(p.ShortID) {
			continue
		}
		byShort[p.ShortID] = append(byShort[p.ShortID], repair.SharedRow{
			Row:    repair.Row{Path: p.Path, FullID: p.ID, ShortID: p.ShortID, ParseError: p.ParseError},
			Design: true,
		})
	}
	var shorts []string
	for short, rows := range byShort {
		if len(rows) < 2 || !sharedHasDesign(rows) {
			continue
		}
		shorts = append(shorts, short)
	}
	sort.Strings(shorts)
	out := make([]sharedGroup, 0, len(shorts))
	for _, short := range shorts {
		rows := byShort[short]
		sort.Slice(rows, func(i, j int) bool { return rows[i].Path < rows[j].Path })
		out = append(out, sharedGroup{short: short, full: scope + "-" + short, rows: rows})
	}
	return out
}

func sharedHasDesign(rows []repair.SharedRow) bool {
	for _, r := range rows {
		if r.Design {
			return true
		}
	}
	return false
}

// repairSharedGroup extends one design_id group. A quarantined holder or an
// interrupted archive move skips the group and leaves the short out of duplicate_id repair.
// A nil result means the group was skipped.
func repairSharedGroup(deps Deps, rep Reporter, t *Target, g sharedGroup, occupied map[string]string) (*sharedRepair, error) {
	if sharedQuarantined(g.rows) {
		rep.Err(token.Line(token.ParseError, fmt.Sprintf("%s: collision includes a quarantined file — fix its frontmatter before repair", g.full)))
		return nil, nil
	}
	var tickets []repair.Row
	for _, r := range g.rows {
		if !r.Design {
			tickets = append(tickets, r.Row)
		}
	}
	mid, err := repair.InterruptedMove(t.Dir, tickets)
	if err != nil {
		return nil, err
	}
	if mid {
		rep.Err(fmt.Sprintf("skipping %s: unfinished archive-layout move, not a collision — re-run tk repair to complete it", g.full))
		return nil, nil
	}
	ops, renames, retarget, err := repair.SharedID(t.Scope, t.Dir, g.rows, occupied)
	if err != nil {
		return nil, err
	}
	if err := applyRepairBatch(deps, rep, t, ops, designIDMessage(renames)); err != nil {
		return nil, err
	}
	for _, r := range renames {
		rep.Out(fmt.Sprintf("repaired design id: %s -> %s (%s)", r.OldID, r.NewID, r.NewPath))
	}
	return &sharedRepair{oldID: g.full, retarget: retarget}, nil
}

func sharedQuarantined(rows []repair.SharedRow) bool {
	for _, r := range rows {
		if r.ParseError {
			return true
		}
	}
	return false
}

func designIDMessage(renames []repair.Rename) string {
	newIDs := make([]string, len(renames))
	for i, r := range renames {
		newIDs[i] = r.NewID
	}
	return fmt.Sprintf("tk: repair design id %s -> %s", renames[0].OldID, strings.Join(newIDs, ", "))
}

// reportSharedEdges prints edge_verify for references that stayed on a repaired
// design_id. Same-scope entries that moved to the extended ticket are omitted.
// Produces is included. An entry in another scope is never rewritten.
func reportSharedEdges(deps Deps, rep Reporter, scope string, shared []sharedRepair) error {
	if len(shared) == 0 {
		return nil
	}
	if err := reconcileOtherScopes(deps, scope); err != nil {
		return err
	}
	for _, g := range shared {
		inbound, err := deps.DB.EdgesByTarget(g.oldID)
		if err != nil {
			return err
		}
		for _, ed := range inbound {
			if ed.FromScope == scope && g.retarget != "" {
				continue
			}
			rep.Out(token.Line(token.EdgeVerify, fmt.Sprintf("%s %s %s — target was collision-repaired, verify this reference", ed.FromID, ed.Kind, g.oldID)))
		}
	}
	return nil
}

func reconcileOtherScopes(deps Deps, scope string) error {
	targets := map[string]string{}
	for name, entry := range deps.Reg.Scopes {
		if name == scope {
			continue
		}
		targets[name] = entry.Dir
	}
	if len(targets) == 0 {
		return nil
	}
	_, err := deps.Rec.Reconcile(targets, registeredSet(deps.Reg), time.Now().UnixNano())
	return err
}

func shortOfFull(full string) string {
	return strings.TrimPrefix(full, id.ScopeOfFullID(full)+"-")
}

// ReportEdgeVerify emits edge_verify lines for actually-repaired collision ids.
// The kept side still holds the collided id. Shared by tk repair
// and sync integrity drain.
func ReportEdgeVerify(deps Deps, rep Reporter, collidedIDs []string) error {
	for _, collidedID := range collidedIDs {
		inbound, err := deps.DB.EdgesByTarget(collidedID)
		if err != nil {
			return err
		}
		for _, ed := range inbound {
			if ed.Kind == index.EdgeProduces {
				continue
			}
			rep.Out(token.Line(token.EdgeVerify, fmt.Sprintf("%s %s %s — target was collision-repaired, verify this reference", ed.FromID, ed.Kind, collidedID)))
		}
	}
	return nil
}

func repairEqualOrder(deps Deps, rep Reporter, t *Target) error {
	rows, err := deps.DB.ScopeTickets(t.Scope)
	if err != nil {
		return err
	}
	ops, err := repair.EqualOrder(toRepairRows(rows))
	if err != nil {
		return err
	}
	if len(ops) == 0 {
		return nil
	}
	if err := applyRepairBatch(deps, rep, t, ops, "tk: repair equal order"); err != nil {
		return err
	}
	rep.Out(fmt.Sprintf("re-spaced %d equal order key(s) in %s", len(ops), t.Scope))
	return nil
}

// repairArchive defers ids still in genuine collisions (shared basename would clobber).
func repairArchive(deps Deps, rep Reporter, t *Target, reportDeferred bool) error {
	rows, err := deps.DB.ScopeTickets(t.Scope)
	if err != nil {
		return err
	}
	collided, err := genuineCollisionIDs(deps, t.Scope, t.Dir)
	if err != nil {
		return err
	}
	deferred := map[string]bool{}
	custom := t.Schema.CustomStatuses()
	for _, p := range rows {
		if p.ParseError {
			continue
		}
		if collided[p.ID] {
			if reportDeferred && !deferred[p.ID] {
				deferred[p.ID] = true
				rep.Err(fmt.Sprintf("archive layout for %s left as is: its id is still duplicated — repair the collision first", p.ID))
			}
			continue
		}
		terminal := status.IsTerminal(p.Status, custom)
		if p.Archived == terminal {
			continue
		}
		op, err := repair.ArchiveMove(t.Dir, toRepairRow(p), terminal)
		if err != nil {
			return err
		}
		msg := fmt.Sprintf("tk: repair archive layout %s", p.ID)
		if err := applyRepairBatch(deps, rep, t, []rewrite.Op{op}, msg); err != nil {
			return err
		}
		rep.Out(fmt.Sprintf("moved archive layout: %s -> %s", p.ID, op.NewPath))
	}
	return nil
}

func repairLongOrder(deps Deps, rep Reporter, t *Target) error {
	rows, err := deps.DB.ScopeTickets(t.Scope)
	if err != nil {
		return err
	}
	ops, err := repair.LongOrder(toRepairRows(rows))
	if err != nil {
		return err
	}
	if len(ops) == 0 {
		return nil
	}
	if err := applyRepairBatch(deps, rep, t, ops, "tk: re-space order"); err != nil {
		return err
	}
	rep.Out(fmt.Sprintf("re-spaced %d over-long order key(s) in %s", len(ops), t.Scope))
	return nil
}

// applyRepairBatch uses CommitPathsCore — callers already hold the git-root lock (re-acquire deadlocks).
func applyRepairBatch(deps Deps, rep Reporter, t *Target, ops []rewrite.Op, message string) error {
	if len(ops) == 0 {
		return nil
	}
	touched, err := rewrite.Apply(ops)
	if err != nil {
		return err
	}
	return finishRepairBatch(deps, rep, t, touched, message)
}

// finishRepairBatch indexes ticket and design paths and self-commits the touched set.
func finishRepairBatch(deps Deps, rep Reporter, t *Target, touched []string, message string) error {
	if err := deps.Rec.SyncPaths(t.Scope, repairIndexPaths(t.Dir, touched)); err != nil {
		return err
	}
	if !t.AutoCommit {
		return nil
	}
	if !t.HasRoot {
		rep.Err(token.Line(token.SyncDisabled, fmt.Sprintf("%s: no git repository — repaired files written but not committed", t.Scope)))
		return nil
	}
	return selfcommit.CommitPathsCore(deps.Ctx, selfcommit.BatchRequest{
		StateDir: deps.StateDir, GitRoot: t.Root, Message: message, Paths: touched,
	})
}

func midRebaseRefusal(ctx context.Context, scope, root string) error {
	where := "the conflicted file"
	if files := git.UnmergedFiles(ctx, root); len(files) > 0 {
		where = strings.Join(files, ", ")
	}
	return fmt.Errorf("%s is mid-sync-conflict in shared repo %s — resolve %s then run tk sync before repairing", scope, root, where)
}

func collisionMessage(renames []repair.Rename) string {
	newIDs := make([]string, len(renames))
	for i, r := range renames {
		newIDs[i] = r.NewID
	}
	return fmt.Sprintf("tk: repair duplicate id %s -> %s", renames[0].OldID, strings.Join(newIDs, ", "))
}

func toRepairRows(rows []*index.Ticket) []repair.Row {
	out := make([]repair.Row, len(rows))
	for i, p := range rows {
		out[i] = toRepairRow(p)
	}
	return out
}

func toRepairRow(p *index.Ticket) repair.Row {
	return repair.Row{Path: p.Path, FullID: p.ID, ShortID: p.ShortID, OrderKey: p.OrderKey, ParseError: p.ParseError}
}

// shortIDPaths: collided short-id maps to lexicographically smallest path (dirent-stable).
func shortIDPaths(rows []*index.Ticket) map[string]string {
	out := make(map[string]string, len(rows))
	for _, p := range rows {
		if p.ShortID == "" {
			continue
		}
		if prev, ok := out[p.ShortID]; !ok || p.Path < prev {
			out[p.ShortID] = p.Path
		}
	}
	return out
}

// genuineCollisionIDs excludes interrupted archive moves (byte-identical both-present window).
func genuineCollisionIDs(deps Deps, scope, dir string) (map[string]bool, error) {
	dups, err := deps.DB.DuplicateIDs([]string{scope})
	if err != nil || len(dups) == 0 {
		return nil, err
	}
	rows, err := deps.DB.ScopeTickets(scope)
	if err != nil {
		return nil, err
	}
	byPath := make(map[string]*index.Ticket, len(rows))
	for _, p := range rows {
		byPath[p.Path] = p
	}
	out := map[string]bool{}
	for _, col := range dups {
		mid, err := repair.InterruptedMove(dir, toRepairRows(rowsForPaths(byPath, col.Members)))
		if err != nil {
			return nil, err
		}
		if !mid {
			out[col.Key] = true
		}
	}
	return out, nil
}

func rowsForPaths(byPath map[string]*index.Ticket, paths []string) []*index.Ticket {
	var out []*index.Ticket
	for _, p := range paths {
		if row, ok := byPath[p]; ok {
			out = append(out, row)
		}
	}
	return out
}

func repairPrefixes(deps Deps, rep Reporter, t *Target) error {
	// Other scopes are read only when this scope has a foreign prefix. The
	// held set is that guard; failing it must happen before any write.
	mismatched, err := scopefile.PrefixMismatches(t.Dir, t.Scope)
	if err != nil || len(mismatched) == 0 {
		return err
	}
	held, err := heldElsewhere(deps, t.Scope)
	if err != nil {
		return err
	}
	ops, renames, err := repair.AdoptPrefix(t.Scope, t.Dir, held)
	if err != nil || len(ops) == 0 {
		return err
	}
	// Cross-scope produces lines are known before the write. A read error here
	// must stop the repair, or a second run will not print them: the prefix
	// already matches.
	produces, err := prefixProducesLines(deps, t.Scope, claimedIDs(renames))
	if err != nil {
		return err
	}
	// Plant new files and same-scope edges while the old names remain, report
	// from that full set, then delete the old names. One commit covers both.
	plant, unlink := splitPrefixOps(ops)
	plantTouched, err := rewrite.Apply(plant)
	if err != nil {
		return err
	}
	for _, r := range renames {
		rep.Out(fmt.Sprintf("repaired id prefix: %s -> %s (%s)", r.OldID, r.NewID, r.NewPath))
	}
	if err := reportPrefixEdges(deps, rep, renames, produces); err != nil {
		return err
	}
	unlinkTouched, err := rewrite.Apply(unlink)
	if err != nil {
		return err
	}
	return finishRepairBatch(deps, rep, t, append(plantTouched, unlinkTouched...), prefixMessage(renames))
}

// splitPrefixOps separates plants (new files and in-place edge rewrites) from
// the trailing removals of the old names.
func splitPrefixOps(ops []rewrite.Op) (plant, unlink []rewrite.Op) {
	for _, op := range ops {
		if op.OldPath != "" && op.OldPath != op.NewPath {
			unlink = append(unlink, op)
			continue
		}
		plant = append(plant, op)
	}
	return plant, unlink
}

func claimedIDs(renames []repair.PrefixRename) map[string]string {
	mapped := map[string]string{}
	for _, r := range renames {
		for _, old := range r.Claimed {
			mapped[old] = r.NewID
		}
	}
	return mapped
}

// heldElsewhere lists full ids owned by a ticket or design in any registered
// scope other than scope. Tickets are read from disk because this path
// reconciles only the scope being repaired, and a stale index would let a
// stray file claim an id that still has a file. A same-scope filename id and
// a same-scope fence id both count. An unreadable scope fails the repair
// before any write.
func heldElsewhere(deps Deps, scope string) (map[string]struct{}, error) {
	var others []string
	for name := range deps.Reg.Scopes {
		if name != scope {
			others = append(others, name)
		}
	}
	sort.Strings(others)
	held := map[string]struct{}{}
	for _, name := range others {
		entry := deps.Reg.Scopes[name]
		if err := holdTicketIDs(held, entry.Dir, name); err != nil {
			return nil, err
		}
		files, err := design.Files(entry.Dir, name)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			holdID(held, f.ID)
			if full, ok := scopefile.TicketIDFromBase(filepath.Base(f.Path)); ok {
				holdID(held, full)
			}
		}
	}
	return held, nil
}

func holdTicketIDs(held map[string]struct{}, dir, scope string) error {
	paths, err := scopefile.ListTickets(dir)
	if err != nil {
		return err
	}
	for _, p := range paths {
		if full, ok := scopefile.TicketIDFromBase(filepath.Base(p)); ok && id.ScopeOfFullID(full) == scope {
			holdID(held, full)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		interior, _, present := frontmatter.Split(data)
		if !present {
			continue
		}
		m, err := frontmatter.Parse(interior)
		if err != nil {
			continue
		}
		if id.IsFullTicketID(m.ID) && id.ScopeOfFullID(m.ID) == scope {
			holdID(held, m.ID)
		}
	}
	return nil
}

func holdID(held map[string]struct{}, id string) {
	if id != "" {
		held[id] = struct{}{}
	}
}

func prefixMessage(renames []repair.PrefixRename) string {
	parts := make([]string, len(renames))
	for i, r := range renames {
		parts[i] = r.OldID + " -> " + r.NewID
	}
	return "tk: repair id prefix " + strings.Join(parts, ", ")
}

// reportPrefixEdges surfaces inbound references outside this scope. Same-scope
// depends, related, and produces were rewritten in the batch.
func reportPrefixEdges(deps Deps, rep Reporter, renames []repair.PrefixRename, produces []string) error {
	targets := make(map[string]string, len(deps.Reg.Scopes))
	for name, entry := range deps.Reg.Scopes {
		targets[name] = entry.Dir
	}
	if _, err := deps.Rec.Reconcile(targets, registeredSet(deps.Reg), time.Now().UnixNano()); err != nil {
		return err
	}
	mapped := claimedIDs(renames)
	olds := make([]string, 0, len(mapped))
	for old := range mapped {
		olds = append(olds, old)
	}
	sort.Strings(olds)

	var lines []string
	for _, old := range olds {
		inbound, err := deps.DB.EdgesByTarget(old)
		if err != nil {
			return err
		}
		newID := mapped[old]
		for _, ed := range inbound {
			if ed.Kind == index.EdgeProduces {
				continue // prefixProducesLines owns the file-based produces line
			}
			lines = append(lines, token.Line(token.EdgeVerify, fmt.Sprintf("%s %s %s — id prefix repaired to %s, verify this reference", ed.FromID, ed.Kind, old, newID)))
		}
	}
	lines = append(lines, produces...)
	sort.Strings(lines)
	for _, line := range lines {
		rep.Out(line)
	}
	return nil
}

func prefixProducesLines(deps Deps, scope string, mapped map[string]string) ([]string, error) {
	names := make([]string, 0, len(deps.Reg.Scopes))
	for name := range deps.Reg.Scopes {
		if name != scope {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	var lines []string
	for _, name := range names {
		files, err := design.Files(deps.Reg.Scopes[name].Dir, name)
		if err != nil {
			return nil, err
		}
		sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
		for _, f := range files {
			if f.Model == nil {
				continue
			}
			for _, entry := range producesIDs(f.Model) {
				newID, ok := mapped[entry]
				if !ok {
					continue
				}
				lines = append(lines, token.Line(token.EdgeVerify, fmt.Sprintf("%s produces %s — id prefix repaired to %s, verify this reference", f.ID, entry, newID)))
			}
		}
	}
	return lines, nil
}

func producesIDs(m *frontmatter.Model) []string {
	for _, f := range m.Custom {
		if f.Key != design.KeyProduces {
			continue
		}
		ids, err := frontmatter.StringList(f.Value)
		if err != nil {
			return nil
		}
		return ids
	}
	return nil
}

// repairIndexPaths keeps paths the ticket or design index can store:
// the scope root, archive/, and design/.
func repairIndexPaths(dir string, paths []string) []string {
	arch := filepath.Join(dir, "archive")
	designs := filepath.Join(dir, scopefile.DesignDir)
	seen := map[string]bool{}
	var out []string
	for _, p := range paths {
		if p == "" || seen[p] {
			continue
		}
		parent := filepath.Dir(p)
		if parent != dir && parent != arch && parent != designs {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

func anyParseError(rows []*index.Ticket) bool {
	for _, p := range rows {
		if p.ParseError {
			return true
		}
	}
	return false
}
