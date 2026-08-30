package tkv

import (
	"errors"
	"net/http"
	"net/url"
	"path"
	"strings"

	"cuelang.org/go/cue/cuecontext"

	"github.com/p3bot/tk/internal/id"
	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/reconcile"
	"github.com/p3bot/tk/internal/registry"
	"github.com/p3bot/tk/internal/resolve"
	"github.com/p3bot/tk/internal/token"
	"github.com/p3bot/tk/internal/xdg"
)

func (s *Server) postLensSet(w http.ResponseWriter, r *http.Request) error {
	if err := r.ParseForm(); err != nil {
		return errBadRequest("malformed form")
	}
	name := r.PathValue("name")
	if !id.IsScopeName(name) {
		return errNotFound("unknown scope")
	}
	return s.finishLens(w, r, name, registry.CompactTags(r.Form["tag"]), false)
}

func (s *Server) postLensClear(w http.ResponseWriter, r *http.Request) error {
	if err := r.ParseForm(); err != nil {
		return errBadRequest("malformed form")
	}
	name := r.PathValue("name")
	if !id.IsScopeName(name) {
		return errNotFound("unknown scope")
	}
	return s.finishLens(w, r, name, nil, true)
}

func (s *Server) finishLens(w http.ResponseWriter, r *http.Request, name string, tags []string, clearing bool) error {
	cueCtx := cuecontext.New()
	configDir, err := s.app.configDir()
	if err != nil {
		return err
	}

	var (
		reg *registry.Registry
		dir string
	)
	h, err := s.withIndexPin(func() error {
		var e error
		reg, dir, e = s.scopeForWrite(name)
		return e
	})
	if err != nil {
		return err
	}
	defer s.releaseHandle(h)

	if err := resolve.CheckName(cueCtx, name, reg.Scopes[name]); err != nil {
		var de *resolve.DriftError
		if errors.As(err, &de) {
			return errConflict(de.Error())
		}
		return err
	}

	if !clearing && len(tags) == 0 {
		http.Redirect(w, r, lensReturnURL(r, name, nil), http.StatusSeeOther)
		return nil
	}

	var inUse map[string]struct{}
	if !clearing {
		rec := reconcile.New(h.db, cueCtx)
		if _, err := rec.Reconcile(map[string]string{name: dir}, registeredSet(reg), nowNS()); err != nil {
			return err
		}
		inUse, err = h.db.ScopeTagMembership(name)
		if err != nil {
			return err
		}
	}

	lock, err := xdg.AcquireConfigLock(configDir)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()

	store := registry.NewStore(cueCtx, configDir)
	if err := store.SetLens(name, tags); err != nil {
		return err
	}

	var warnings []string
	if !clearing {
		for _, tag := range index.AbsentTags(tags, inUse) {
			warnings = append(warnings, token.FormatTagUnknown(tag))
		}
	}
	http.Redirect(w, r, lensReturnURL(r, name, warnings), http.StatusSeeOther)
	return nil
}

func lensReturnURL(r *http.Request, name string, warnings []string) string {
	loc := stripNoticeQuery(strings.TrimSpace(r.FormValue("return")))
	if !validLensReturn(loc, name) {
		loc = "/scope/" + name
	}
	if len(warnings) == 0 {
		return loc
	}
	u, err := url.Parse(loc)
	if err != nil {
		return loc
	}
	q := u.Query()
	for _, w := range warnings {
		if w = strings.TrimSpace(w); w != "" {
			q.Add("warning", w)
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func validLensReturn(loc, name string) bool {
	if loc == "" || !strings.HasPrefix(loc, "/") || strings.HasPrefix(loc, "//") {
		return false
	}
	u, err := url.Parse(loc)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return false
	}
	p := path.Clean(u.Path)
	switch p {
	case "/search", "/graphs", "/graphs/depends", "/maintenance":
		return true
	case "/scope/" + name:
		return true
	}
	prefix := "/scope/" + name + "/"
	if strings.HasPrefix(p, prefix) {
		rest := strings.TrimPrefix(p, prefix)
		return rest != "" && !strings.Contains(rest, "/")
	}
	return false
}
