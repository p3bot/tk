// Package writeengine is the cobra-free ticket-file write session: create, meta
// mutate, order, mark, and claim. Callers map structured results to process
// edges; this package does not import cobra or internal/cli.
package writeengine

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"cuelang.org/go/cue"

	"github.com/p3bot/tk/internal/depgate"
	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/pathutil"
	"github.com/p3bot/tk/internal/reconcile"
	"github.com/p3bot/tk/internal/registry"
	"github.com/p3bot/tk/internal/scopeconfig"
	"github.com/p3bot/tk/internal/syncengine"
)

// Deps are machine-local services the write engine needs.
type Deps struct {
	Ctx      context.Context
	Cue      *cue.Context
	StateDir string
	Reg      *registry.Registry
	DB       *index.DB
	Rec      *reconcile.Reconciler
}

// Reporter receives progress lines (stdout-class Out, stderr-class Err).
// Claim refresh/push stream here; structured diagnostics live on Result.
type Reporter interface {
	Out(line string)
	Err(line string)
}

type nopReporter struct{}

func (nopReporter) Out(string) {}
func (nopReporter) Err(string) {}

func reporterOrNop(r Reporter) Reporter {
	if r == nil {
		return nopReporter{}
	}
	return r
}

// Lookup is a post-CLI ticket identity: Arg is the original token (error wording),
// Query is the index key after reserved-me expansion, ByFull selects id vs short-id.
type Lookup struct {
	Arg    string
	Query  string
	ByFull bool
}

func (l Lookup) query() string {
	if l.Query != "" {
		return l.Query
	}
	return l.Arg
}

// Member is one ticket in a batch write (tk mark with several ids).
type Member struct {
	Path            string
	ID              string
	OldStatus       string
	NewStatus       string
	Moved           bool
	DependsOpen     []string
	RequiredMissing []string
}

// Result is the structured outcome of a write. Path is empty when no write
// landed. Adapters map fields to stdout/stderr; they do not parse tokens out
// of Error() text except for typed failures.
//
// Members is the per-ticket outcome of a batch mark. When it is non-empty,
// Path, ID, OldStatus, NewStatus, Moved, DependsOpen, and RequiredMissing are
// the first member, so one-id adapters (tkv, create, claim-next, meta) keep
// reading the scalars. Walk Tickets() for paths and per-ticket warnings; the
// scalars are not a summary of the whole argv.
type Result struct {
	Path             string
	ID               string
	OldStatus        string
	NewStatus        string
	Moved            bool
	DependsOpen      []string
	RequiredMissing  []string
	Members          []Member
	SyncNeeded       string
	SyncDisabled     string
	Warnings         []string
	SelectionTokens  []string
	ApplyLens        bool
	Lens             []string
	ReadyOutsideLens int
	Blocked          int
	ScaffoldCue      string
	ArchiveNote      string
	TagNew           []string
}

// Tickets is the per-ticket outcome of this write. When Members is set it is
// the batch; otherwise a one-id result is a single-element list so emitters
// do not fork on the scalar fields.
func (r Result) Tickets() []Member {
	if len(r.Members) > 0 {
		return r.Members
	}
	if r.Path == "" && r.ID == "" && len(r.DependsOpen) == 0 && len(r.RequiredMissing) == 0 {
		return nil
	}
	return []Member{{
		Path:            r.Path,
		ID:              r.ID,
		OldStatus:       r.OldStatus,
		NewStatus:       r.NewStatus,
		Moved:           r.Moved,
		DependsOpen:     r.DependsOpen,
		RequiredMissing: r.RequiredMissing,
	}}
}

// SchemaAutoCommit reports whether the schema enables tk-driven self-commit.
// A nil schema is false; writers refuse unusable config first.
func SchemaAutoCommit(s *scopeconfig.Schema) bool {
	return s != nil && s.AutoCommit
}

func nowNS() int64 { return time.Now().UnixNano() }

func registeredSet(reg *registry.Registry) map[string]bool {
	out := make(map[string]bool, len(reg.Scopes))
	for name := range reg.Scopes {
		out[name] = true
	}
	return out
}

func syncDeps(d Deps) syncengine.Deps {
	return syncengine.Deps{
		Ctx:      d.Ctx,
		Cue:      d.Cue,
		StateDir: d.StateDir,
		Reg:      d.Reg,
		DB:       d.DB,
		Rec:      d.Rec,
	}
}

func gateDeps(d Deps) depgate.Deps {
	return depgate.Deps{DB: d.DB, Rec: d.Rec, Reg: d.Reg}
}

func absPath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path for %q: %w", p, err)
	}
	return pathutil.Canonical(abs), nil
}

func ctxOf(d Deps) context.Context {
	if d.Ctx != nil {
		return d.Ctx
	}
	return context.Background()
}
