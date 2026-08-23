package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/depgate"
	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/reconcile"
	"github.com/p3bot/tk/internal/registry"
	"github.com/p3bot/tk/internal/resolve"
	"github.com/p3bot/tk/internal/syncengine"
	"github.com/p3bot/tk/internal/writeengine"
)

type engine struct {
	app *App
	reg *registry.Registry
	db  *index.DB
	rec *reconcile.Reconciler
}

func (a *App) openEngine(_ *cobra.Command) (*engine, error) {
	reg, err := registry.NewStore(a.Ctx, a.ConfigDir).Load()
	if err != nil {
		return nil, err
	}
	db, err := index.Open(a.StateDir)
	if err != nil {
		return nil, err
	}
	return &engine{app: a, reg: reg, db: db, rec: reconcile.New(db, a.Ctx)}, nil
}

func nowNS() int64 { return time.Now().UnixNano() }

func (e *engine) close() {
	if e != nil && e.db != nil {
		_ = e.db.Close()
	}
}

func (e *engine) syncDeps(c *cobra.Command) syncengine.Deps {
	return syncengine.Deps{
		Ctx:      c.Context(),
		Cue:      e.app.Ctx,
		StateDir: e.app.StateDir,
		Reg:      e.reg,
		DB:       e.db,
		Rec:      e.rec,
	}
}

func (e *engine) writeDeps(ctx context.Context) writeengine.Deps {
	return writeengine.Deps{
		Ctx:      ctx,
		Cue:      e.app.Ctx,
		StateDir: e.app.StateDir,
		Reg:      e.reg,
		DB:       e.db,
		Rec:      e.rec,
	}
}

func (e *engine) writeLookup(scope, idArg string, form idForm) (writeengine.Lookup, error) {
	q, f, err := e.expandReservedID(scope, idArg, form)
	if err != nil {
		return writeengine.Lookup{}, err
	}
	return writeengine.Lookup{Arg: idArg, Query: q, ByFull: f == idFull}, nil
}

func (e *engine) gateDeps() depgate.Deps {
	return depgate.Deps{DB: e.db, Rec: e.rec, Reg: e.reg}
}

func (e *engine) registeredSet() map[string]bool {
	out := make(map[string]bool, len(e.reg.Scopes))
	for name := range e.reg.Scopes {
		out[name] = true
	}
	return out
}

func (e *engine) allTargets() map[string]string {
	out := make(map[string]string, len(e.reg.Scopes))
	for name, entry := range e.reg.Scopes {
		out[name] = entry.Dir
	}
	return out
}

func (e *engine) reconcile(c *cobra.Command, targets map[string]string) (*reconcile.Result, error) {
	res, err := e.reconcileResult(targets)
	if err != nil {
		return nil, err
	}
	e.printWarnings(c, res.Warnings)
	return res, nil
}

// reconcileResult omits printing so callers can filter warnings (e.g. suppress duplicate_id echo).
func (e *engine) reconcileResult(targets map[string]string) (*reconcile.Result, error) {
	return e.rec.Reconcile(targets, e.registeredSet(), nowNS())
}

func (e *engine) printWarnings(c *cobra.Command, warnings []string) {
	for _, w := range warnings {
		stderrln(c, w)
	}
}

func ambientOptions(scopeFlag string) (resolve.Options, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return resolve.Options{}, fmt.Errorf("resolve working directory: %w", err)
	}
	canonical, err := absPath(cwd)
	if err != nil {
		return resolve.Options{}, err
	}
	return resolve.Options{
		ScopeFlag: scopeFlag,
		EnvScope:  os.Getenv("TK_SCOPE"),
		Cwd:       canonical,
	}, nil
}

// resolveAmbient: no-scope is generic non-zero; drift is fail-closed via resolve.
func (e *engine) resolveAmbient(scopeFlag string) (*resolve.Resolved, error) {
	opts, err := ambientOptions(scopeFlag)
	if err != nil {
		return nil, err
	}
	return resolve.Resolve(e.app.Ctx, e.reg, opts)
}
