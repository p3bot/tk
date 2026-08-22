package integrity

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/p3bot/tk/internal/git"
	"github.com/p3bot/tk/internal/gitstate"
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
	// All: mid-rebase skips instead of hard-refusing (doctor --all).
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

// RunBatches is the locks-held core (doctor acquires; sync already holds both).
// Caller must hold the scope flock and, for auto-commit git-roots, the commit lock.
func RunBatches(deps Deps, rep Reporter, t *Target, f Flags) error {
	if f.Repair {
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

// repairCollisions: edge_verify is read after all collisions so referrer ids are post-repair.
func repairCollisions(deps Deps, rep Reporter, t *Target) error {
	dups, err := deps.DB.DuplicateIDs([]string{t.Scope})
	if err != nil {
		return err
	}
	if len(dups) == 0 {
		return nil
	}
	rows, err := deps.DB.ScopeTickets(t.Scope)
	if err != nil {
		return err
	}
	occupied := shortIDPaths(rows)
	byPath := map[string]*index.Ticket{}
	for _, p := range rows {
		byPath[p.Path] = p
	}

	var repaired []string
	for _, col := range dups {
		members := rowsForPaths(byPath, col.Members)
		mid, err := repair.InterruptedMove(t.Dir, toRepairRows(members))
		if err != nil {
			return err
		}
		if mid {
			rep.Err(fmt.Sprintf("skipping %s: unfinished archive-layout move, not a collision — re-run tk doctor --repair to complete it", col.Key))
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
	return ReportEdgeVerify(deps, rep, repaired)
}

// ReportEdgeVerify emits edge_verify lines for actually-repaired collision ids.
// The kept side still holds the collided id. Shared by doctor collision repair
// and sync integrity drain.
func ReportEdgeVerify(deps Deps, rep Reporter, collidedIDs []string) error {
	for _, collidedID := range collidedIDs {
		inbound, err := deps.DB.EdgesByTarget(collidedID)
		if err != nil {
			return err
		}
		for _, ed := range inbound {
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
	if err := deps.Rec.SyncPaths(t.Scope, touched); err != nil {
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

func anyParseError(rows []*index.Ticket) bool {
	for _, p := range rows {
		if p.ParseError {
			return true
		}
	}
	return false
}
