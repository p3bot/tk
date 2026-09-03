package writeengine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/p3bot/tk/internal/atomicfile"
	"github.com/p3bot/tk/internal/flock"
	"github.com/p3bot/tk/internal/frontmatter"
	"github.com/p3bot/tk/internal/git"
	"github.com/p3bot/tk/internal/gitstate"
	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/order"
	"github.com/p3bot/tk/internal/reconcile"
	"github.com/p3bot/tk/internal/scopeconfig"
	"github.com/p3bot/tk/internal/scopefile"
	"github.com/p3bot/tk/internal/selfcommit"
	"github.com/p3bot/tk/internal/token"
)

const ticketFileMode = 0o644

// Session is a held scope flock after post-lock reconcile and unusable refuse.
// Verb functions run policy then CompleteState (except create, which never
// self-commits). Release is idempotent.
type Session struct {
	deps       Deps
	lock       *flock.Lock
	Scope      string
	Dir        string
	Res        *reconcile.Result
	Schema     *scopeconfig.Schema
	AutoCommit bool
	Root       string
	HasRoot    bool
}

// Begin acquires the scope flock, reconciles, and refuses unusable config.
// Mid-rebase is a separate CheckMidRebase so mark can reject unknown status first.
func Begin(deps Deps, scope, dir string) (*Session, error) {
	lock, err := scopefile.AcquireLock(dir)
	if err != nil {
		return nil, err
	}
	s := &Session{deps: deps, lock: lock, Scope: scope, Dir: dir}
	res, err := deps.Rec.Reconcile(map[string]string{scope: dir}, registeredSet(deps.Reg), nowNS())
	if err != nil {
		s.Release()
		return nil, err
	}
	if err := RefuseUnusable(res, scope, dir); err != nil {
		s.Release()
		return nil, err
	}
	s.Res = res
	s.Schema = res.Schema(scope)
	s.AutoCommit = SchemaAutoCommit(s.Schema)
	s.Root, s.HasRoot = scopefile.GitRoot(dir)
	return s, nil
}

// Release drops the scope flock. Safe to call twice.
func (s *Session) Release() {
	if s == nil || s.lock == nil {
		return
	}
	_ = s.lock.Release()
	s.lock = nil
}

// Warnings returns reconcile token lines from the post-lock pass.
func (s *Session) Warnings() []string {
	if s == nil || s.Res == nil {
		return nil
	}
	return s.Res.Warnings
}

// CheckMidRebase refuses auto-commit writes on a mid-rebase git-root.
func (s *Session) CheckMidRebase() error {
	return CheckMidRebase(ctxOf(s.deps), s.Scope, s.AutoCommit, s.Root, s.HasRoot)
}

// CompleteState self-commits on tk-driven roots or records sync_disabled.
// Repo-driven stays quiet.
func (s *Session) CompleteState(message, newPath, oldPath string) (syncDisabled, syncNeeded string, err error) {
	return completeState(s.deps, s.Scope, s.Dir, s.AutoCommit, message, newPath, oldPath, s.Root, s.HasRoot)
}

// CompletePaths is CompleteState over every touched path in one commit.
func (s *Session) CompletePaths(message string, paths []string) (syncDisabled, syncNeeded string, err error) {
	return completePaths(s.deps, s.Scope, s.Dir, s.AutoCommit, message, paths, s.Root, s.HasRoot)
}

// RefuseUnusable refuses writes when the dir is unreachable or tk.cue is unusable.
func RefuseUnusable(res *reconcile.Result, scope, dir string) error {
	if res.Unreachable[scope] {
		return &UnusableError{Line: token.Line(token.UnreachableScope,
			fmt.Sprintf("%s: dir %s is not reachable", scope, dir))}
	}
	if cfgErr, ok := res.ConfigErrs[scope]; ok {
		return &UnusableError{Line: token.Line(token.ConfigUnparseable,
			fmt.Sprintf("%s (%s): %s — fix tk.cue before writing", scope, cfgErr.Dir, cfgErr.Reason))}
	}
	return nil
}

// CheckMidRebase refuses auto-commit writes on a mid-rebase git-root (repo-granular).
func CheckMidRebase(ctx context.Context, scope string, autoCommit bool, root string, hasRoot bool) error {
	if !autoCommit || !hasRoot {
		return nil
	}
	if !git.MidRebase(ctx, root) {
		return nil
	}
	where := "the conflicted file"
	if files := git.UnmergedFiles(ctx, root); len(files) > 0 {
		where = strings.Join(files, ", ")
	}
	return &MidRebaseError{Scope: scope, Root: root, Where: where}
}

// SyncNeededReason is at most one catalogue reason after a tk-driven write.
// Priority: push failed, then dirty, then unpushed.
func SyncNeededReason(ctx context.Context, stateDir, dir, root string) string {
	if _, present := gitstate.ReadLastPushError(stateDir, root); present {
		return "push failed"
	}
	if n := scopefile.CountAllowlistedDirty(ctx, dir, root, true); n > 0 {
		return "dirty"
	}
	if n, err := git.UnpushedCount(ctx, root); err == nil && n > 0 {
		return "unpushed"
	}
	return ""
}

func completeState(deps Deps, scope, dir string, autoCommit bool, message, newPath, oldPath, root string, hasRoot bool) (string, string, error) {
	return completePaths(deps, scope, dir, autoCommit, message, WrittenPaths(newPath, oldPath), root, hasRoot)
}

func completePaths(deps Deps, scope, dir string, autoCommit bool, message string, paths []string, root string, hasRoot bool) (string, string, error) {
	if !autoCommit {
		return "", "", nil
	}
	if !hasRoot {
		return fmt.Sprintf("%s: no git repository for %s — files written but not committed", scope, dir), "", nil
	}
	if err := selfcommit.CommitPaths(ctxOf(deps), selfcommit.BatchRequest{
		StateDir: deps.StateDir,
		GitRoot:  root,
		Message:  message,
		Paths:    paths,
	}); err != nil {
		return "", "", fmt.Errorf("self-commit %s: %w", scope, err)
	}
	return "", SyncNeededReason(ctxOf(deps), deps.StateDir, dir, root), nil
}

// WrittenPaths includes the removed old path so SyncPaths deletes its row.
func WrittenPaths(newPath, oldPath string) []string {
	if oldPath == "" || oldPath == newPath {
		return []string{newPath}
	}
	return []string{newPath, oldPath}
}

// MaxValidOrder returns the greatest valid order key, or "" for an empty board
// (open KeyBetween bound). Invalid and quarantined keys are skipped.
func MaxValidOrder(rows []*index.Ticket) string {
	best := ""
	for _, p := range rows {
		if p.ParseError || !order.Valid(p.OrderKey) {
			continue
		}
		if best == "" || p.OrderKey > best {
			best = p.OrderKey
		}
	}
	return best
}

// AtomicWrite is the shared same-dir temp+rename write for ticket files.
func AtomicWrite(path string, data []byte) error {
	return atomicfile.Write(path, data, ticketFileMode)
}

// ReadTicketFile loads and parses a ticket. Parse failure here is a mid-write
// race (quarantine is refused upstream).
func ReadTicketFile(path string) (*frontmatter.Model, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", path, err)
	}
	interior, body, present := frontmatter.Split(data)
	if !present {
		return nil, nil, fmt.Errorf("%s has no frontmatter fence", path)
	}
	m, err := frontmatter.Parse(interior)
	if err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return m, body, nil
}

// WriteTicketFile serializes and atomically replaces path.
func WriteTicketFile(path string, m *frontmatter.Model, body []byte) error {
	interior, err := frontmatter.Serialize(m)
	if err != nil {
		return err
	}
	return AtomicWrite(path, frontmatter.Compose(interior, body))
}

// TerminalLocation relocates only (basename unchanged).
func TerminalLocation(dir, base string, terminal bool) (string, error) {
	if !terminal {
		return filepath.Join(dir, base), nil
	}
	archiveDir := filepath.Join(dir, "archive")
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		return "", fmt.Errorf("create archive dir: %w", err)
	}
	return filepath.Join(archiveDir, base), nil
}

// ResolveRow looks up one ticket. Zero rows is unknown (noun-worded); more than
// one is duplicate_id. It does not apply parse-error quarantine.
func ResolveRow(db *index.DB, scope string, lookup Lookup, noun string) (*index.Ticket, error) {
	q := lookup.query()
	var (
		rows []*index.Ticket
		err  error
	)
	if lookup.ByFull {
		rows, err = db.TicketsByID(scope, q)
	} else {
		rows, err = db.TicketsByShortID(scope, q)
	}
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, &UnknownTicketError{Noun: noun, Arg: lookup.Arg}
	}
	if len(rows) > 1 {
		paths := make([]string, len(rows))
		for i, r := range rows {
			paths[i] = r.Path
		}
		return nil, &DuplicateError{ID: rows[0].ID, Paths: paths}
	}
	return rows[0], nil
}

// ResolveWriteRow also refuses parse_error quarantine.
func ResolveWriteRow(db *index.DB, scope string, lookup Lookup) (*index.Ticket, error) {
	p, err := ResolveRow(db, scope, lookup, "ticket")
	if err != nil {
		return nil, err
	}
	if p.ParseError {
		return nil, &ParseQuarantineError{ID: p.ID, Msg: p.ParseMsg}
	}
	return p, nil
}
