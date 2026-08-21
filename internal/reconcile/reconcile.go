// Package reconcile is tk's read-through: it brings the derived SQLite index into
// agreement with ticket files before a command reads it, git-free. It never
// mutates a ticket file — detection here, repair elsewhere.
// It owns file→row upsert/delete and prune of unregistered scopes. It does not
// own full-cache Rebuild (that discards the store; callers rebuild then Reconcile).
package reconcile

import (
	"fmt"
	"os"
	"sort"

	"cuelang.org/go/cue"

	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/scopeconfig"
	"github.com/p3bot/tk/internal/status"
	"github.com/p3bot/tk/internal/token"
)

// Reconciler reconciles scopes into an open index using a shared CUE context.
type Reconciler struct {
	db  *index.DB
	ctx *cue.Context
}

// New builds a Reconciler over an open index and the process-wide CUE context.
func New(db *index.DB, ctx *cue.Context) *Reconciler {
	return &Reconciler{db: db, ctx: ctx}
}

// Result carries schemas, config errors, unreachable scopes, and stderr token lines.
type Result struct {
	Schemas     map[string]*scopeconfig.Schema
	ConfigErrs  map[string]*scopeconfig.ConfigError
	Unreachable map[string]bool
	Warnings    []string
}

// Schema returns the evaluated schema for a reconciled scope, or nil when unusable.
func (res *Result) Schema(scope string) *scopeconfig.Schema {
	if res == nil {
		return nil
	}
	return res.Schemas[scope]
}

// Merge folds another batch's result into res (cross-scope closure accumulation).
func (res *Result) Merge(other *Result) {
	if other == nil {
		return
	}
	for k, v := range other.Schemas {
		res.Schemas[k] = v
	}
	for k, v := range other.ConfigErrs {
		res.ConfigErrs[k] = v
	}
	for k, v := range other.Unreachable {
		res.Unreachable[k] = v
	}
	res.Warnings = append(res.Warnings, other.Warnings...)
}

// NewResult builds an empty result with maps ready to populate.
func NewResult() *Result {
	return &Result{
		Schemas:     map[string]*scopeconfig.Schema{},
		ConfigErrs:  map[string]*scopeconfig.ConfigError{},
		Unreachable: map[string]bool{},
	}
}

// Reconcile brings targets into agreement with disk, prunes unregistered scopes,
// runs integrity detection, and returns schemas plus stderr token lines.
// now is unix nanoseconds from the CLI edge for a deterministic racy-index rule.
func (r *Reconciler) Reconcile(targets map[string]string, registered map[string]bool, now int64) (*Result, error) {
	res, reconciled, err := r.ReconcileRows(targets, registered, now)
	if err != nil {
		return nil, err
	}
	if err := r.AppendAggregates(reconciled, res); err != nil {
		return nil, err
	}
	return res, nil
}

// ReconcileRows is the row-and-schema half: prune, sync rows, evaluate schemas.
// Skips aggregates so a multi-batch closure walk can aggregate once at the end.
func (r *Reconciler) ReconcileRows(targets map[string]string, registered map[string]bool, now int64) (*Result, []string, error) {
	if err := r.pruneForgotten(registered); err != nil {
		return nil, nil, err
	}

	res := NewResult()
	names := sortedKeys(targets)
	var reconciled []string
	for _, name := range names {
		// Unreachable dirs skip config eval entirely (one token: unreachable_scope only).
		reachable, err := r.reconcileScope(name, targets[name], now)
		if err != nil {
			return nil, nil, err
		}
		if !reachable {
			res.Unreachable[name] = true
			res.Warnings = append(res.Warnings, token.Line(token.UnreachableScope, fmt.Sprintf("%s: dir %s is not reachable — rows left in place", name, targets[name])))
			continue
		}

		schema, cfgErr := r.schemaFor(name, targets[name])
		res.Schemas[name] = schema
		if cfgErr != nil {
			res.ConfigErrs[name] = cfgErr
			res.Warnings = append(res.Warnings, token.Line(token.ConfigUnparseable, fmt.Sprintf("%s: %s", name, cfgErr.Reason)))
		}
		reconciled = append(reconciled, name)
	}
	return res, reconciled, nil
}

// AppendAggregates rides post-reconcile warn-only tokens onto res (reads only; no re-stat).
func (r *Reconciler) AppendAggregates(scopes []string, res *Result) error {
	if err := r.appendParseErrorWarning(scopes, res); err != nil {
		return err
	}
	return r.appendIntegrityWarnings(scopes, res)
}

// pruneForgotten drops indexed scopes the registry no longer knows.
func (r *Reconciler) pruneForgotten(registered map[string]bool) error {
	indexed, err := r.db.IndexedScopes()
	if err != nil {
		return err
	}
	for scope := range indexed {
		if !registered[scope] {
			if err := r.db.DeleteScope(scope); err != nil {
				return err
			}
		}
	}
	return nil
}

type pending struct {
	ticket *index.Ticket
	edges  []index.Edge
}

func diskStat(path string) error {
	_, err := os.Stat(path)
	return err
}

// applyScopeWrite is one scope's SQL writes after the writer lock is held:
// post-lock existence decides whether a path may have a row, then SetLastIndex.
func applyScopeWrite(w *index.WriteTx, name string, now int64, listed map[string]statEntry, existing map[string]index.RowStat, upserts map[string]pending, stat func(string) error) error {
	for _, path := range sortedMapKeys(upserts) {
		err := stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				if err := w.DeleteByPath(path); err != nil {
					return err
				}
				continue
			}
			continue
		}
		item := upserts[path]
		if err := w.UpsertTicketWithEdges(item.ticket, item.edges); err != nil {
			return err
		}
	}
	for path := range existing {
		if _, ok := listed[path]; ok {
			continue
		}
		err := stat(path)
		if err != nil && os.IsNotExist(err) {
			if err := w.DeleteByPath(path); err != nil {
				return err
			}
		}
	}
	return w.SetLastIndex(name, now)
}

// reconcileScope stats dir+archive, reparses changed/new/racy files, deletes vanished rows.
// reachable false leaves rows untouched. Directory listing, LastIndex, ScopeRows, and
// parse run before the scope write transaction; disk existence is re-checked after
// the writer lock is held.
func (r *Reconciler) reconcileScope(name, dir string, now int64) (reachable bool, err error) {
	files, ok := statScope(name, dir)
	if !ok {
		return false, nil
	}

	lastIndex, err := r.db.LastIndex(name)
	if err != nil {
		return false, err
	}
	existing, err := r.db.ScopeRows(name)
	if err != nil {
		return false, err
	}

	upserts := map[string]pending{}
	for path, st := range files {
		prev, seen := existing[path]
		// Racy-index: mtime >= last-index is dirty (same-tick edit looks unchanged otherwise).
		if seen && prev.MtimeNS == st.MtimeNS && prev.Size == st.Size && st.MtimeNS < lastIndex {
			continue
		}
		p, edges, err := parseFile(path, name, st.FullID, st.Archived, st.MtimeNS, st.Size)
		if err != nil {
			// Transient I/O on a listed file: skip this pass, do not drop or quarantine.
			continue
		}
		upserts[path] = pending{ticket: p, edges: edges}
	}

	if err := r.db.RunScopeWrite(func(w *index.WriteTx) error {
		return applyScopeWrite(w, name, now, files, existing, upserts, diskStat)
	}); err != nil {
		return false, err
	}
	return true, nil
}

// appendParseErrorWarning rides a terse quarantine count (board-verb summary).
func (r *Reconciler) appendParseErrorWarning(scopes []string, res *Result) error {
	if len(scopes) == 0 {
		return nil
	}
	n, err := r.db.ParseErrorCount(scopes)
	if err != nil {
		return err
	}
	if n > 0 {
		res.Warnings = append(res.Warnings, token.Line(token.ParseError, fmt.Sprintf("%d unparseable", n)))
	}
	return nil
}

// appendIntegrityWarnings rides warn-only integrity tokens; never mutates files.
func (r *Reconciler) appendIntegrityWarnings(scopes []string, res *Result) error {
	if len(scopes) == 0 {
		return nil
	}
	dups, err := r.db.DuplicateIDs(scopes)
	if err != nil {
		return err
	}
	for _, c := range dups {
		res.Warnings = append(res.Warnings, token.Line(token.DuplicateID,
			fmt.Sprintf("%s claimed by %s", c.Key, joinPaths(c.Members))))
	}

	eq, err := r.db.EqualOrders(scopes)
	if err != nil {
		return err
	}
	for _, c := range eq {
		res.Warnings = append(res.Warnings, token.Line(token.EqualOrder,
			fmt.Sprintf("%s order %q shared by %s", c.Scope, c.Key, joinPaths(c.Members))))
	}

	return r.appendArchiveDrift(scopes, res)
}

// appendArchiveDrift flags location-vs-status disagreement using per-scope terminal-ness.
func (r *Reconciler) appendArchiveDrift(scopes []string, res *Result) error {
	for _, scope := range scopes {
		custom := customCategories(res.Schema(scope))
		rows, err := r.db.ArchiveDrift(scope, status.TerminalNames(custom))
		if err != nil {
			return err
		}
		for _, p := range rows {
			if p.Archived {
				res.Warnings = append(res.Warnings, token.Line(token.ArchiveNonTerminal,
					fmt.Sprintf("%s is %s under archive/ (%s)", p.ID, p.Status, p.Path)))
				continue
			}
			res.Warnings = append(res.Warnings, token.Line(token.ArchiveTerminalAtRoot,
				fmt.Sprintf("%s is %s at dir root (%s)", p.ID, p.Status, p.Path)))
		}
	}
	return nil
}

func customCategories(s *scopeconfig.Schema) map[string]status.Category {
	if s == nil {
		return nil
	}
	return s.Statuses
}

func sortedKeys(m map[string]string) []string {
	return sortedMapKeys(m)
}

func sortedMapKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func joinPaths(paths []string) string {
	s := ""
	for i, p := range paths {
		if i > 0 {
			s += ", "
		}
		s += p
	}
	return s
}
