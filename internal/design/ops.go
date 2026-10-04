package design

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/p3bot/tk/internal/atomicfile"
	"github.com/p3bot/tk/internal/frontmatter"
	"github.com/p3bot/tk/internal/gitstate"
	"github.com/p3bot/tk/internal/id"
	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/pathutil"
	"github.com/p3bot/tk/internal/reconcile"
	"github.com/p3bot/tk/internal/registry"
	"github.com/p3bot/tk/internal/scopeconfig"
	"github.com/p3bot/tk/internal/scopefile"
	"github.com/p3bot/tk/internal/selfcommit"
	"github.com/p3bot/tk/internal/slug"
	"github.com/p3bot/tk/internal/token"
)

// Deps are the machine-local services design operations need.
type Deps struct {
	Ctx      context.Context
	StateDir string
	Reg      *registry.Registry
	DB       *index.DB
	Rec      *reconcile.Reconciler
}

// CreateInput is one design create. Identity stays at the edge.
type CreateInput struct {
	Scope string
	Dir   string
	Title string
	Now   time.Time
	Rand  io.Reader
}

// IDInput is one design addressed by a resolved scope.
type IDInput struct {
	Scope   string
	Dir     string
	Arg     string
	Full    bool
	Content bool
}

// ListInput is one design list.
type ListInput struct {
	Scope    string
	Dir      string
	Statuses []string
	All      bool
}

// MarkInput sets one design status.
type MarkInput struct {
	IDInput
	Status string
}

// MetaInput adds or removes one produces entry.
type MetaInput struct {
	IDInput
	Add    bool
	Target string
}

// Create scaffolds design/<id>-<slug>.md and does not self-commit.
func Create(deps Deps, in CreateInput) (Result, error) {
	titleText := strings.TrimSpace(in.Title)
	if titleText == "" || strings.Contains(titleText, "\n") {
		return Result{}, &UsageError{Msg: "design create needs a non-empty title"}
	}
	release, err := lock(in.Dir)
	if err != nil {
		return Result{}, err
	}
	defer release()
	if err := deps.refuseUnusable(in.Scope, in.Dir); err != nil {
		return Result{}, err
	}
	if err := deps.refuseMidRebase(in.Scope, in.Dir); err != nil {
		return Result{}, err
	}

	taken, err := scopefile.OccupiedShortIDs(in.Dir, in.Scope)
	if err != nil {
		return Result{}, err
	}
	src := in.Rand
	if src == nil {
		src = rand.Reader
	}
	shortID, err := mintUnused(taken, src)
	if err != nil {
		return Result{}, err
	}
	fullID := in.Scope + "-" + shortID
	model := &frontmatter.Model{
		ID:      fullID,
		Status:  StatusDraft,
		Created: rfc3339(in.Now),
	}
	interior, err := Serialize(model)
	if err != nil {
		return Result{}, err
	}
	body := []byte("# " + titleText + "\n")
	target := filepath.Join(in.Dir, scopefile.DesignDir, fullID+"-"+slug.Slugify(titleText)+".md")
	if err := writeFile(target, Compose(interior, body)); err != nil {
		return Result{}, err
	}
	abs, err := absPath(target)
	if err != nil {
		return Result{}, err
	}
	return Result{ID: fullID, Path: abs, SyncNeeded: deps.syncNeeded(in.Scope, in.Dir)}, nil
}

// List prints design rows from the index after reconcile. A missing design/
// directory is an empty result. A positional outside the closed status set is
// a usage error before any row.
func List(deps Deps, in ListInput) (Result, error) {
	for _, status := range in.Statuses {
		if !KnownStatus(status) {
			return Result{}, &UnknownStatusError{Status: status}
		}
	}
	if err := scopefileRequire(in.Scope, in.Dir); err != nil {
		return Result{}, err
	}
	indexed, err := deps.designRows(in.Scope, in.Dir)
	if err != nil {
		return Result{}, err
	}
	var rows []Row
	n := 0
	for _, p := range indexed {
		if p.ParseError {
			n++
			continue
		}
		if !listKeeps(p.Status, in.Statuses, in.All) {
			continue
		}
		abs, err := absPath(p.Path)
		if err != nil {
			return Result{}, err
		}
		rows = append(rows, Row{
			ID:      p.ID,
			Status:  p.Status,
			Title:   p.Title,
			Path:    abs,
			Created: p.Created,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if createdBefore(rows[i].Created, rows[j].Created) {
			return true
		}
		if createdBefore(rows[j].Created, rows[i].Created) {
			return false
		}
		return rows[i].ID < rows[j].ID
	})
	return Result{Rows: rows, Unparseable: n}, nil
}

// createdBefore reports whether a is older than b. A value that is not RFC3339
// sorts first: not-newer-than-any, the same rule as collision keeper order.
func createdBefore(a, b string) bool {
	ta, aok := parseCreated(a)
	tb, bok := parseCreated(b)
	if aok != bok {
		return !aok
	}
	if !aok {
		return false
	}
	return ta.Before(tb)
}

func parseCreated(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// Get resolves one design from the index after reconcile. A shared short id
// refuses with no path. An unparseable fence still returns the path.
func Get(deps Deps, in IDInput) (Result, error) {
	if err := scopefileRequire(in.Scope, in.Dir); err != nil {
		return Result{}, err
	}
	indexed, err := deps.designRows(in.Scope, in.Dir)
	if err != nil {
		return Result{}, err
	}
	n := 0
	var hits []*index.Design
	for _, p := range indexed {
		if p.ParseError {
			n++
		}
		if designMatches(p, in) {
			hits = append(hits, p)
		}
	}
	if len(hits) == 0 {
		return Result{Unparseable: n}, &UnknownError{Arg: in.Arg}
	}
	if len(hits) > 1 {
		paths := make([]string, len(hits))
		for i, p := range hits {
			paths[i], err = absPath(p.Path)
			if err != nil {
				return Result{}, err
			}
		}
		sort.Strings(paths)
		return Result{Unparseable: n}, &SharedIDError{ID: hits[0].ID, Paths: paths}
	}
	p := hits[0]
	abs, err := absPath(p.Path)
	if err != nil {
		return Result{}, err
	}
	res := Result{ID: p.ID, Path: abs, Unparseable: n}
	if p.ParseError {
		res.Parse = &ParseError{ID: p.ID, Msg: p.ParseMsg}
	}
	if in.Content {
		body, err := os.ReadFile(p.Path)
		if err != nil {
			return Result{}, err
		}
		res.Body = body
	}
	return res, nil
}

func designMatches(p *index.Design, in IDInput) bool {
	if in.Full {
		return p.ID == in.Arg
	}
	return p.ShortID == in.Arg
}

// Mark sets status. The file stays in design/. It self-commits on a tk-driven scope.
func Mark(deps Deps, in MarkInput) (Result, error) {
	if !KnownStatus(in.Status) {
		return Result{}, &UnknownStatusError{Status: in.Status}
	}
	release, err := lock(in.Dir)
	if err != nil {
		return Result{}, err
	}
	defer release()
	if err := deps.refuseUnusable(in.Scope, in.Dir); err != nil {
		return Result{}, err
	}
	if err := deps.refuseMidRebase(in.Scope, in.Dir); err != nil {
		return Result{}, err
	}
	f, err := resolveOne(in.IDInput)
	if err != nil {
		return Result{}, err
	}
	if f.ParseErr != nil {
		return Result{}, f.parseError()
	}
	abs, err := absPath(f.Path)
	if err != nil {
		return Result{}, err
	}
	if f.Model.Status == in.Status {
		return Result{ID: f.ID, Path: abs, Unchanged: true}, nil
	}
	before := append([]byte(nil), f.Raw...)
	f.Model.Status = in.Status
	if err := writeModel(f.Path, f.Model, f.Body); err != nil {
		return Result{}, err
	}
	disabled, needed, err := commitWritten(deps, in.Scope, in.Dir, fmt.Sprintf("tk: %s -> %s", f.ID, in.Status), f.Path, before)
	if err != nil {
		return Result{}, err
	}
	return Result{ID: f.ID, Path: abs, SyncDisabled: disabled, SyncNeeded: needed}, nil
}

// MetaAddRemove appends one produces full id, or drops one list entry.
// Add requires a full ticket id that resolves. Remove drops every occurrence
// of the value, including one that is not a full ticket id.
func MetaAddRemove(deps Deps, in MetaInput) (Result, error) {
	if strings.TrimSpace(in.Target) == "" {
		return Result{}, &UsageError{Msg: "design meta needs a ticket id"}
	}
	release, err := lock(in.Dir)
	if err != nil {
		return Result{}, err
	}
	defer release()
	if err := deps.refuseUnusable(in.Scope, in.Dir); err != nil {
		return Result{}, err
	}
	if err := deps.refuseMidRebase(in.Scope, in.Dir); err != nil {
		return Result{}, err
	}
	f, err := resolveOne(in.IDInput)
	if err != nil {
		return Result{}, err
	}
	if f.ParseErr != nil {
		return Result{}, f.parseError()
	}
	// Same refusal as ticket meta on a custom multi-value field: a non-list
	// is usage, and the argument is not consulted.
	ids, _, err := producesIDs(f.Model)
	if err != nil {
		return Result{}, &UsageError{Msg: fmt.Sprintf("custom field %q is not a string list", KeyProduces)}
	}
	abs, err := absPath(f.Path)
	if err != nil {
		return Result{}, err
	}
	op := "remove"
	switch {
	case in.Add && !id.IsFullTicketID(in.Target):
		return Result{}, &UsageError{Msg: fmt.Sprintf("%q is not a full ticket id", in.Target)}
	case in.Add && containsID(ids, in.Target):
		return Result{ID: f.ID, Path: abs, Unchanged: true}, nil
	case in.Add:
		op = "add"
		ok, resolveErr := deps.ticketResolves(in.Target)
		if resolveErr != nil {
			return Result{}, resolveErr
		}
		if !ok {
			return Result{}, &UnresolvedError{Target: in.Target}
		}
		ids = append(ids, in.Target)
	case !containsID(ids, in.Target):
		return Result{ID: f.ID, Path: abs, Unchanged: true}, nil
	default:
		ids = dropID(ids, in.Target)
	}
	before := append([]byte(nil), f.Raw...)
	setProduces(f.Model, ids)
	if err := writeModel(f.Path, f.Model, f.Body); err != nil {
		return Result{}, err
	}
	message := fmt.Sprintf("tk: %s meta %s %s", f.ID, op, KeyProduces)
	disabled, needed, err := commitWritten(deps, in.Scope, in.Dir, message, f.Path, before)
	if err != nil {
		return Result{}, err
	}
	return Result{ID: f.ID, Path: abs, SyncDisabled: disabled, SyncNeeded: needed}, nil
}

// listKeeps: positionals match the status string exactly. With none, draft and
// accepted are the default, and --all keeps every other parsed status,
// including one outside the closed set.
func listKeeps(status string, filters []string, all bool) bool {
	if len(filters) > 0 {
		for _, f := range filters {
			if f == status {
				return true
			}
		}
		return false
	}
	if all {
		return true
	}
	return DefaultListed(status)
}

func mintUnused(taken map[string]struct{}, r io.Reader) (string, error) {
	for {
		s, err := id.Mint(r)
		if err != nil {
			return "", fmt.Errorf("mint id: %w", err)
		}
		if _, used := taken[s]; !used {
			return s, nil
		}
	}
}

func resolveFiles(in IDInput) ([]File, error) {
	if err := scopefileRequire(in.Scope, in.Dir); err != nil {
		return nil, err
	}
	return Files(in.Dir, in.Scope)
}

func resolveOne(in IDInput) (File, error) {
	files, err := resolveFiles(in)
	if err != nil {
		return File{}, err
	}
	return pickDesign(files, in)
}

func pickDesign(files []File, in IDInput) (File, error) {
	var hits []File
	var err error
	for _, f := range files {
		if in.Full {
			if f.ID == in.Arg {
				hits = append(hits, f)
			}
			continue
		}
		if strings.TrimPrefix(f.ID, in.Scope+"-") == in.Arg {
			hits = append(hits, f)
		}
	}
	if len(hits) == 0 {
		return File{}, &UnknownError{Arg: in.Arg}
	}
	if len(hits) > 1 {
		paths := make([]string, len(hits))
		for i, f := range hits {
			paths[i], err = absPath(f.Path)
			if err != nil {
				return File{}, err
			}
		}
		sort.Strings(paths)
		return File{}, &SharedIDError{ID: hits[0].ID, Paths: paths}
	}
	return hits[0], nil
}

// Files lists design documents in dir for scope. ID is resolved like a ticket
// id: the filename id, replaced by a same-scope fence id when the fence parses.
// A broken fence keeps the filename id. A missing design directory, and a
// design path that is not a directory, are an empty list.
func Files(dir, scope string) ([]File, error) {
	root := filepath.Join(dir, scopefile.DesignDir)
	st, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if !st.IsDir() {
		return nil, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []File
	for _, e := range entries {
		if e.IsDir() || !scopefile.LooksLikeTicket(e.Name()) {
			continue
		}
		short, ok := scopefile.ShortIDOfBasename(e.Name(), scope)
		if !ok {
			continue
		}
		path := filepath.Join(root, e.Name())
		f, err := readFile(path, scope+"-"+short, scope)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, nil
}

func readFile(path, filenameID, scope string) (File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return File{}, err
	}
	f := File{Path: path, ID: filenameID, Raw: data}
	interior, body, present := frontmatter.Split(data)
	if !present || fenceConflict(interior) {
		f.ParseErr = fmt.Errorf("frontmatter fence missing, broken, or carries conflict markers")
		f.Body = data
		return f, nil
	}
	m, err := frontmatter.Parse(interior)
	if err != nil {
		f.ParseErr = err
		f.Body = body
		return f, nil
	}
	f.Model = m
	f.Body = body
	// A same-scope fence id is the design id. A foreign id is not adopted.
	if id.IsFullTicketID(m.ID) && id.ScopeOfFullID(m.ID) == scope {
		f.ID = m.ID
	}
	return f, nil
}

func fenceConflict(interior []byte) bool {
	markers := [][]byte{[]byte("<<<<<<<"), []byte("======="), []byte(">>>>>>>")}
	for len(interior) > 0 {
		var line []byte
		if i := bytes.IndexByte(interior, '\n'); i >= 0 {
			line, interior = interior[:i], interior[i+1:]
		} else {
			line, interior = interior, nil
		}
		for _, marker := range markers {
			if bytes.HasPrefix(line, marker) {
				return true
			}
		}
	}
	return false
}

func writeModel(path string, m *frontmatter.Model, body []byte) error {
	interior, err := Serialize(m)
	if err != nil {
		return err
	}
	return writeFile(path, Compose(interior, body))
}

func writeFile(path string, data []byte) error {
	return atomicfile.Write(path, data, fileMode)
}

func lock(dir string) (func(), error) {
	l, err := scopefile.AcquireLock(dir)
	if err != nil {
		return nil, err
	}
	return func() { _ = l.Release() }, nil
}

func scopefileRequire(scope, dir string) error {
	st, err := os.Lstat(dir)
	if err != nil || !st.IsDir() {
		return fmt.Errorf("%s", token.Line(token.UnreachableScope, fmt.Sprintf("%s: dir %s is not reachable", scope, dir)))
	}
	return nil
}

func (d Deps) ctx() context.Context {
	if d.Ctx != nil {
		return d.Ctx
	}
	return context.Background()
}

func (d Deps) refuseUnusable(scope, dir string) error {
	if err := scopefileRequire(scope, dir); err != nil {
		return err
	}
	_, cfgErr := d.Rec.SchemaOrError(scope, dir)
	if cfgErr == nil {
		return nil
	}
	return fmt.Errorf("%s", token.Line(token.ConfigUnparseable, fmt.Sprintf("%s (%s): %s — fix tk.cue before writing", scope, cfgErr.Dir, cfgErr.Reason)))
}

func (d Deps) refuseMidRebase(scope, dir string) error {
	root, hasRoot := scopefile.GitRoot(dir)
	schema, cfgErr := d.Rec.SchemaOrError(scope, dir)
	if cfgErr != nil {
		return nil
	}
	return gitstate.CheckMidRebase(d.ctx(), scope, scopeconfig.SchemaAutoCommit(schema), root, hasRoot)
}

func (d Deps) syncNeeded(scope, dir string) string {
	root, hasRoot := scopefile.GitRoot(dir)
	if !hasRoot {
		return ""
	}
	schema, cfgErr := d.Rec.SchemaOrError(scope, dir)
	if cfgErr != nil || !scopeconfig.SchemaAutoCommit(schema) {
		return ""
	}
	return gitstate.SyncNeededReason(d.ctx(), d.StateDir, dir, root)
}

// commitWritten commits a file this command just wrote. A commit failure
// restores before, so the command did not happen and a retry still has work.
func commitWritten(deps Deps, scope, dir, message, path string, before []byte) (string, string, error) {
	disabled, needed, err := deps.commit(scope, dir, message, []string{path})
	if err == nil {
		return disabled, needed, nil
	}
	if rerr := writeFile(path, before); rerr != nil {
		return "", "", fmt.Errorf("%w (also failed to restore %s: %w)", err, path, rerr)
	}
	return "", "", err
}

func (d Deps) commit(scope, dir, message string, paths []string) (string, string, error) {
	root, hasRoot := scopefile.GitRoot(dir)
	schema, cfgErr := d.Rec.SchemaOrError(scope, dir)
	if cfgErr != nil {
		return "", "", fmt.Errorf("%s", token.Line(token.ConfigUnparseable, fmt.Sprintf("%s (%s): %s — fix tk.cue before writing", scope, cfgErr.Dir, cfgErr.Reason)))
	}
	if !scopeconfig.SchemaAutoCommit(schema) {
		return "", "", nil
	}
	if !hasRoot {
		return fmt.Sprintf("%s: no git repository for %s — files written but not committed", scope, dir), "", nil
	}
	if err := selfcommit.CommitPaths(d.ctx(), selfcommit.BatchRequest{
		StateDir: d.StateDir,
		GitRoot:  root,
		Message:  message,
		Paths:    paths,
	}); err != nil {
		return "", "", fmt.Errorf("self-commit %s: %w", scope, err)
	}
	return "", gitstate.SyncNeededReason(d.ctx(), d.StateDir, dir, root), nil
}

// designRows reconciles one scope and returns its design rows. An unreachable
// scope is an error so a successful list is not a stale index.
func (d Deps) designRows(scope, dir string) ([]*index.Design, error) {
	if d.Rec == nil || d.DB == nil {
		return nil, fmt.Errorf("design index is not available")
	}
	res, err := d.Rec.Reconcile(map[string]string{scope: dir}, d.registered(), time.Now().UnixNano())
	if err != nil {
		return nil, err
	}
	if res.Unreachable[scope] {
		return nil, fmt.Errorf("%s", token.Line(token.UnreachableScope, fmt.Sprintf("%s: dir %s is not reachable", scope, dir)))
	}
	return d.DB.ScopeDesigns(scope)
}

func (d Deps) registered() map[string]bool {
	registered := map[string]bool{}
	if d.Reg == nil {
		return registered
	}
	for name := range d.Reg.Scopes {
		registered[name] = true
	}
	return registered
}

func (d Deps) ticketResolves(full string) (bool, error) {
	if !id.IsFullTicketID(full) {
		return false, &UsageError{Msg: fmt.Sprintf("%q is not a full ticket id", full)}
	}
	scope := id.ScopeOfFullID(full)
	entry, ok := d.Reg.Scopes[scope]
	if !ok || d.DB == nil || d.Rec == nil {
		return false, nil
	}
	registered := map[string]bool{}
	for name := range d.Reg.Scopes {
		registered[name] = true
	}
	if _, err := d.Rec.Reconcile(map[string]string{scope: entry.Dir}, registered, time.Now().UnixNano()); err != nil {
		return false, err
	}
	rows, err := d.DB.TicketsByID(scope, full)
	if err != nil {
		return false, err
	}
	return len(rows) > 0, nil
}

func absPath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path for %q: %w", p, err)
	}
	return pathutil.Canonical(abs), nil
}
