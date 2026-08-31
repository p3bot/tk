package tkv

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/p3bot/tk/internal/id"
	"github.com/p3bot/tk/internal/registry"
	"github.com/p3bot/tk/internal/syncengine"
	"github.com/p3bot/tk/internal/writeengine"
)

func (s *Server) postUnscopedSync(w http.ResponseWriter, r *http.Request) error {
	if err := r.ParseForm(); err != nil {
		return errBadRequest("malformed form")
	}
	return errBadRequest("sync needs a selected scope")
}

func (s *Server) postChromeSync(w http.ResponseWriter, r *http.Request) error {
	if err := r.ParseForm(); err != nil {
		return errBadRequest("malformed form")
	}
	name := r.PathValue("name")
	if !id.IsScopeName(name) {
		return errNotFound("unknown scope")
	}
	return s.runSync(w, r, name, false)
}

func (s *Server) postMaintenanceSync(w http.ResponseWriter, r *http.Request) error {
	if err := r.ParseForm(); err != nil {
		return errBadRequest("malformed form")
	}
	return s.runSync(w, r, "", true)
}

func (s *Server) runSync(w http.ResponseWriter, r *http.Request, name string, all bool) error {
	deps, release, err := s.beginSync(r.Context())
	if err != nil {
		return err
	}
	defer release()

	var (
		in  syncengine.Input
		loc string
	)
	if all {
		in.AllRegistered = true
		loc = "/maintenance"
	} else {
		entry, ok := deps.Reg.Scopes[name]
		if !ok {
			return errNotFound(fmt.Sprintf("unknown scope %q", name))
		}
		in.Ambient = &syncengine.AmbientScope{Name: name, Dir: entry.Dir}
		loc = stripNoticeQuery(strings.TrimSpace(r.FormValue("return")))
		if !validLensReturn(loc, name) {
			loc = "/scope/" + name
		}
	}

	rep := &capturingReporter{}
	result, err := syncengine.Run(deps, rep, in)
	if err != nil {
		return mapSyncError(err)
	}
	lines := rep.lines()
	if result.NeedsAttention && len(lines) == 0 {
		lines = []string{syncengine.ErrNeedsAttention.Error()}
	}
	s.putSyncNotices(loc, syncFlash{lines: lines, attention: result.NeedsAttention})
	http.Redirect(w, r, loc, http.StatusSeeOther)
	return nil
}

func mapSyncError(err error) error {
	if err == nil {
		return nil
	}
	var he *httpError
	if errors.As(err, &he) {
		return he
	}
	if strings.Contains(err.Error(), "sync is for auto-commit") {
		return errBadRequest(err.Error())
	}
	return err
}

func (s *Server) beginSync(ctx context.Context) (syncengine.Deps, func(), error) {
	// Client disconnect and Shutdown cancel r.Context(); git is CommandContext.
	// A killed rebase leaves mid-sync-conflict. The sync must outlive the POST.
	ctx = context.WithoutCancel(ctx)
	var reg *registry.Registry
	h, err := s.withIndexPin(func() error {
		var e error
		reg, e = s.loadRegistry()
		return e
	})
	if err != nil {
		return syncengine.Deps{}, nil, err
	}
	wdeps, err := s.writeDeps(ctx, reg, h)
	if err != nil {
		s.releaseHandle(h)
		return syncengine.Deps{}, nil, err
	}
	if s.afterIndexUnlock != nil {
		s.afterIndexUnlock()
	}
	return toSyncDeps(wdeps), func() { s.releaseHandle(h) }, nil
}

func toSyncDeps(d writeengine.Deps) syncengine.Deps {
	return syncengine.Deps{
		Ctx:      d.Ctx,
		Cue:      d.Cue,
		StateDir: d.StateDir,
		Reg:      d.Reg,
		DB:       d.DB,
		Rec:      d.Rec,
	}
}

type capturingReporter struct {
	mu    sync.Mutex
	saved []string
}

func (r *capturingReporter) Out(line string) { r.add(line) }

func (r *capturingReporter) Err(line string) { r.add(line) }

func (r *capturingReporter) add(line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.saved = append(r.saved, line)
}

func (r *capturingReporter) lines() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.saved...)
}

type syncFlash struct {
	lines     []string
	attention bool
}

func (s *Server) putSyncNotices(path string, n syncFlash) {
	if path == "" || len(n.lines) == 0 {
		return
	}
	n.lines = append([]string(nil), n.lines...)
	s.flashMu.Lock()
	defer s.flashMu.Unlock()
	if s.flash == nil {
		s.flash = make(map[string]syncFlash)
	}
	s.flash[path] = n
}

func (s *Server) takeSyncNotices(path string) (syncFlash, bool) {
	s.flashMu.Lock()
	defer s.flashMu.Unlock()
	if s.flash == nil {
		return syncFlash{}, false
	}
	n, ok := s.flash[path]
	delete(s.flash, path)
	return n, ok
}
