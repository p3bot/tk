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

// Result is the structured outcome of a write. Path is empty when no write
// landed. Adapters map fields to stdout/stderr; they do not parse tokens out
// of Error() text except for typed failures.
type Result struct {
	Path             string
	ID               string
	OldStatus        string
	NewStatus        string
	Moved            bool
	DependsOpen      []string
	RequiredMissing  []string
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
