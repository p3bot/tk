package tkv

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"cuelang.org/go/cue/cuecontext"

	"github.com/p3bot/tk/internal/depgate"
	"github.com/p3bot/tk/internal/id"
	"github.com/p3bot/tk/internal/reconcile"
	"github.com/p3bot/tk/internal/registry"
	"github.com/p3bot/tk/internal/scopeconfig"
	"github.com/p3bot/tk/internal/status"
	"github.com/p3bot/tk/internal/token"
	"github.com/p3bot/tk/internal/writeengine"
)

func (s *Server) wrapEngine(fn requestFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := checkWriteOrigin(r); err != nil {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.serveError(w, r, err)
			return
		}
		err := fn(w, r)
		if err == nil {
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		s.serveError(w, r, err)
	}
}

func checkWriteOrigin(r *http.Request) error {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return errForbidden("cross-site write refused")
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return nil
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" {
		return errForbidden("foreign origin refused")
	}
	if !strings.EqualFold(u.Host, r.Host) {
		return errForbidden("foreign origin refused")
	}
	return nil
}

func (s *Server) withIndexPin(fn func() error) (*indexHandle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cur == nil || s.db == nil {
		return nil, &httpError{status: http.StatusInternalServerError, message: "index is closed"}
	}
	if err := s.runRequest(fn); err != nil {
		return nil, err
	}
	return s.retainLocked(), nil
}

type writeSession struct {
	deps writeengine.Deps
	dir  string
}

func (s *Server) writeDeps(ctx context.Context, reg *registry.Registry, h *indexHandle) (writeengine.Deps, error) {
	stateDir, err := s.app.stateDir()
	if err != nil {
		return writeengine.Deps{}, err
	}
	// Own CUE: wrapEngine has already dropped Server.mu, and GET handlers still
	// compile on the process context under that mutex. cue.Context is not safe
	// for concurrent use. Schema cache stays on the pinned index.
	cueCtx := cuecontext.New()
	return writeengine.Deps{
		Ctx:      ctx,
		Cue:      cueCtx,
		StateDir: stateDir,
		Reg:      reg,
		DB:       h.db,
		Rec:      reconcile.New(h.db, cueCtx),
	}, nil
}

func (s *Server) beginWrite(ctx context.Context, name string) (writeSession, func(), error) {
	// Client disconnect and Shutdown cancel r.Context(); git is CommandContext.
	// A killed rebase leaves mid-sync-conflict. The write must outlive the POST.
	ctx = context.WithoutCancel(ctx)
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
		return writeSession{}, nil, err
	}
	deps, err := s.writeDeps(ctx, reg, h)
	if err != nil {
		s.releaseHandle(h)
		return writeSession{}, nil, err
	}
	if s.afterIndexUnlock != nil {
		s.afterIndexUnlock()
	}
	return writeSession{deps: deps, dir: dir}, func() { s.releaseHandle(h) }, nil
}

func (s *Server) postMark(w http.ResponseWriter, r *http.Request) error {
	if err := r.ParseForm(); err != nil {
		return errBadRequest("malformed form")
	}
	name := r.PathValue("name")
	if !id.IsScopeName(name) {
		return errNotFound("unknown scope")
	}
	newStatus := strings.TrimSpace(r.FormValue("status"))
	if newStatus == "" {
		return errBadRequest("missing status")
	}
	lu, err := lookupFromArg(r.FormValue("id"))
	if err != nil {
		return err
	}
	if lu.ByFull && id.ScopeOfFullID(lu.Arg) != name {
		return errNotFound(fmt.Sprintf("ticket %q does not belong to scope %q", lu.Arg, name))
	}

	sess, release, err := s.beginWrite(r.Context(), name)
	if err != nil {
		return err
	}
	defer release()

	res, err := writeengine.Mark(sess.deps, nil, writeengine.MarkInput{
		Scope:     name,
		Dir:       sess.dir,
		Lookups:   []writeengine.Lookup{lu},
		NewStatus: newStatus,
	})
	return s.finishWrite(w, r, res, err)
}

func (s *Server) postClaim(w http.ResponseWriter, r *http.Request) error {
	if err := r.ParseForm(); err != nil {
		return errBadRequest("malformed form")
	}
	name := r.PathValue("name")
	if !id.IsScopeName(name) {
		return errNotFound("unknown scope")
	}

	in := writeengine.ClaimInput{Kind: writeengine.ClaimNext, Scope: name}
	idArg := strings.TrimSpace(r.FormValue("id"))
	if idArg != "" {
		lu, err := lookupFromArg(idArg)
		if err != nil {
			return err
		}
		if lu.ByFull && id.ScopeOfFullID(lu.Arg) != name {
			return errNotFound(fmt.Sprintf("ticket %q does not belong to scope %q", lu.Arg, name))
		}
		in.Kind = writeengine.ClaimID
		in.Lookup = lu
	}

	sess, release, err := s.beginWrite(r.Context(), name)
	if err != nil {
		return err
	}
	defer release()
	in.Dir = sess.dir

	res, err := writeengine.Claim(sess.deps, nil, in)
	return s.finishWrite(w, r, res, err)
}

func (s *Server) postCreate(w http.ResponseWriter, r *http.Request) error {
	if err := r.ParseForm(); err != nil {
		return errBadRequest("malformed form")
	}
	name := r.PathValue("name")
	if !id.IsScopeName(name) {
		return errNotFound("unknown scope")
	}
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		return errBadRequest("create needs a non-empty title")
	}

	sess, release, err := s.beginWrite(r.Context(), name)
	if err != nil {
		return err
	}
	defer release()

	res, err := writeengine.Create(sess.deps, writeengine.CreateInput{
		Scope:  name,
		Dir:    sess.dir,
		Title:  title,
		Status: strings.TrimSpace(r.FormValue("status")),
		Tags:   formCreateTags(r.Form["tag"]),
	})
	if err != nil {
		return mapWriteError(res, err)
	}
	http.Redirect(w, r, appendNotices(inspectHref(res.ID), res), http.StatusSeeOther)
	return nil
}

func (s *Server) postMeta(w http.ResponseWriter, r *http.Request) error {
	if err := r.ParseForm(); err != nil {
		return errBadRequest("malformed form")
	}
	name := r.PathValue("name")
	if !id.IsScopeName(name) {
		return errNotFound("unknown scope")
	}
	lu, err := lookupFromArg(r.FormValue("id"))
	if err != nil {
		return err
	}
	if lu.ByFull && id.ScopeOfFullID(lu.Arg) != name {
		return errNotFound(fmt.Sprintf("ticket %q does not belong to scope %q", lu.Arg, name))
	}
	op, err := parseMetaOp(r.FormValue("op"))
	if err != nil {
		return err
	}
	key := strings.TrimSpace(r.FormValue("key"))
	if key == "" {
		return errBadRequest("missing key")
	}
	value := strings.TrimSpace(r.FormValue("value"))
	if (op == writeengine.MetaAdd || op == writeengine.MetaRm) && value == "" {
		return errBadRequest(fmt.Sprintf("meta %s value must be non-empty", op))
	}

	sess, release, err := s.beginWrite(r.Context(), name)
	if err != nil {
		return err
	}
	defer release()

	res, err := writeengine.Meta(sess.deps, writeengine.MetaInput{
		Scope:  name,
		Dir:    sess.dir,
		Lookup: lu,
		Op:     op,
		Key:    key,
		Value:  value,
	})
	if err != nil {
		return mapWriteError(res, err)
	}
	http.Redirect(w, r, appendNotices(inspectHref(res.ID), res), http.StatusSeeOther)
	return nil
}

func (s *Server) postOrder(w http.ResponseWriter, r *http.Request) error {
	if err := r.ParseForm(); err != nil {
		return errBadRequest("malformed form")
	}
	name := r.PathValue("name")
	if !id.IsScopeName(name) {
		return errNotFound("unknown scope")
	}
	lu, err := lookupFromArg(r.FormValue("id"))
	if err != nil {
		return err
	}
	if lu.ByFull && id.ScopeOfFullID(lu.Arg) != name {
		return errNotFound(fmt.Sprintf("ticket %q does not belong to scope %q", lu.Arg, name))
	}
	dest, err := destFromForm(r)
	if err != nil {
		return err
	}

	sess, release, err := s.beginWrite(r.Context(), name)
	if err != nil {
		return err
	}
	defer release()

	res, err := writeengine.Order(sess.deps, writeengine.OrderInput{
		Scope:  name,
		Dir:    sess.dir,
		Lookup: lu,
		Dest:   dest,
	})
	return s.finishWrite(w, r, res, err)
}

func destFromForm(r *http.Request) (writeengine.Dest, error) {
	var d writeengine.Dest
	switch v := strings.TrimSpace(r.FormValue("dest")); v {
	case "":
	case "first":
		d.First = true
	case "last":
		d.Last = true
	default:
		return writeengine.Dest{}, errBadRequest(fmt.Sprintf("unknown order dest %q", v))
	}
	if v := strings.TrimSpace(r.FormValue("before")); v != "" {
		lu, err := lookupFromArg(v)
		if err != nil {
			return writeengine.Dest{}, err
		}
		d.Before = lu
	}
	if v := strings.TrimSpace(r.FormValue("after")); v != "" {
		lu, err := lookupFromArg(v)
		if err != nil {
			return writeengine.Dest{}, err
		}
		d.After = lu
	}
	if d.Count() != 1 {
		return writeengine.Dest{}, errBadRequest("order needs exactly one destination")
	}
	return d, nil
}

func parseMetaOp(raw string) (writeengine.MetaOp, error) {
	switch strings.TrimSpace(raw) {
	case "set":
		return writeengine.MetaSet, nil
	case "add":
		return writeengine.MetaAdd, nil
	case "rm":
		return writeengine.MetaRm, nil
	case "":
		return 0, errBadRequest("missing op")
	default:
		return 0, errBadRequest(fmt.Sprintf("unknown meta op %q", raw))
	}
}

// formCreateTags is the HTML dual of repeatable --tag: comma-separated, blanks skipped.
func formCreateTags(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	var out []string
	for _, raw := range values {
		for _, part := range strings.Split(raw, ",") {
			if t := strings.TrimSpace(part); t != "" {
				out = append(out, t)
			}
		}
	}
	return out
}

func (s *Server) scopeForWrite(name string) (*registry.Registry, string, error) {
	reg, err := s.loadRegistry()
	if err != nil {
		return nil, "", err
	}
	entry, ok := reg.Scopes[name]
	if !ok {
		return nil, "", errNotFound(fmt.Sprintf("unknown scope %q", name))
	}
	return reg, entry.Dir, nil
}

func (s *Server) finishWrite(w http.ResponseWriter, r *http.Request, res writeengine.Result, err error) error {
	if err != nil {
		return mapWriteError(res, err)
	}
	http.Redirect(w, r, writeReturnURL(r, res), http.StatusSeeOther)
	return nil
}

func writeReturnURL(r *http.Request, res writeengine.Result) string {
	name := r.PathValue("name")
	ret := r.FormValue("return")
	var loc string
	if ret == "inspect" && res.ID != "" {
		loc = inspectHref(res.ID)
	} else {
		loc = "/scope/" + name + boardQuery(
			r.FormValue("backlog") == "1",
			r.FormValue("archived") == "1",
			r.Form["tag"],
		)
	}
	return appendNotices(loc, res)
}

const (
	noticeDependsOpen     = "depends_open"
	noticeRequiredMissing = "required_missing"
	noticeSyncNeeded      = "sync_needed"
	noticeSyncDisabled    = "sync_disabled"
	noticeTagNew          = "tag_new"
	noticeWarning         = "warning"
)

var noticeQueryKeys = []string{
	noticeDependsOpen,
	noticeRequiredMissing,
	noticeSyncNeeded,
	noticeSyncDisabled,
	noticeTagNew,
	noticeWarning,
}

func appendNotices(loc string, res writeengine.Result) string {
	u, err := url.Parse(loc)
	if err != nil {
		return loc
	}
	q := u.Query()
	if len(res.DependsOpen) > 0 {
		q.Set(noticeDependsOpen, strings.Join(res.DependsOpen, " "))
	}
	if len(res.RequiredMissing) > 0 {
		q.Set(noticeRequiredMissing, strings.Join(res.RequiredMissing, " "))
	}
	if res.SyncNeeded != "" {
		q.Set(noticeSyncNeeded, res.SyncNeeded)
	}
	if res.SyncDisabled != "" {
		q.Set(noticeSyncDisabled, res.SyncDisabled)
	}
	for _, tag := range res.TagNew {
		if tag = strings.TrimSpace(tag); tag != "" {
			q.Add(noticeTagNew, tag)
		}
	}
	for _, w := range res.Warnings {
		if w = strings.TrimSpace(w); w != "" {
			q.Add(noticeWarning, w)
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func noticesFromQuery(q url.Values) []string {
	var out []string
	for _, k := range noticeQueryKeys {
		switch k {
		case noticeWarning:
			for _, w := range q[k] {
				if w = strings.TrimSpace(w); w != "" {
					out = append(out, w)
				}
			}
		case noticeDependsOpen:
			if v := strings.TrimSpace(q.Get(k)); v != "" {
				out = append(out, noticeDependsOpen+": waiting on "+v)
			}
		case noticeTagNew:
			for _, tag := range q[k] {
				if tag = strings.TrimSpace(tag); tag != "" {
					out = append(out, token.FormatTagNew(tag))
				}
			}
		default:
			if v := strings.TrimSpace(q.Get(k)); v != "" {
				out = append(out, k+": "+v)
			}
		}
	}
	return out
}

func stripNoticeQuery(loc string) string {
	u, err := url.Parse(loc)
	if err != nil {
		return loc
	}
	q := u.Query()
	for _, k := range noticeQueryKeys {
		q.Del(k)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func lookupFromArg(arg string) (writeengine.Lookup, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return writeengine.Lookup{}, errBadRequest("missing ticket id")
	}
	full, ok := parseIDArg(arg)
	if !ok {
		return writeengine.Lookup{}, errBadRequest(fmt.Sprintf("unknown ticket id %q", arg))
	}
	return writeengine.Lookup{Arg: arg, Query: arg, ByFull: full}, nil
}

func mapWriteError(res writeengine.Result, err error) error {
	if err == nil {
		return nil
	}
	var he *httpError
	if errors.As(err, &he) {
		return he
	}
	var unk *writeengine.UnknownStatusError
	if errors.As(err, &unk) {
		return errBadRequest(unk.Error())
	}
	var use *writeengine.UsageError
	if errors.As(err, &use) {
		return errBadRequest(use.Error())
	}
	var empty *depgate.EmptyQueueError
	if errors.As(err, &empty) {
		return errConflict(empty.Error())
	}
	var nl *writeengine.NoLongerTodoError
	if errors.As(err, &nl) {
		return errConflict(nl.Error())
	}
	var dup *writeengine.DuplicateError
	if errors.As(err, &dup) {
		return errDuplicate(dup.ID, dup.Paths)
	}
	var pe *writeengine.ParseQuarantineError
	if errors.As(err, &pe) {
		return errConflict(pe.Error())
	}
	var un *writeengine.UnusableError
	if errors.As(err, &un) {
		return errUnavailable(un.Error())
	}
	var mid *writeengine.MidRebaseError
	if errors.As(err, &mid) {
		return errConflict(mid.Error())
	}
	var miss *writeengine.UnknownTicketError
	if errors.As(err, &miss) {
		return errNotFound(miss.Error())
	}
	var self *writeengine.DependsSelfError
	if errors.As(err, &self) {
		return errConflict(self.Error())
	}
	var dang *writeengine.DependsDanglingError
	if errors.As(err, &dang) {
		return errConflict(dang.Error())
	}
	var unres *writeengine.DependsUnresolvableError
	if errors.As(err, &unres) {
		return errConflict(unres.Error())
	}
	var neigh *writeengine.NeighbourOrderError
	if errors.As(err, &neigh) {
		return errConflict(neigh.Error())
	}
	var nlo *writeengine.NoLegalOrderError
	if errors.As(err, &nlo) {
		return errConflict(nlo.Error())
	}
	if errors.Is(err, writeengine.ErrRefreshFailed) {
		return errUnavailable(writeengine.ErrRefreshFailed.Error())
	}
	if errors.Is(err, writeengine.ErrPushFailed) {
		msg := writeengine.ErrPushFailed.Error()
		if res.ID != "" {
			msg += fmt.Sprintf(". Ticket %s is in-progress", res.ID)
		} else {
			msg += ". The ticket is in-progress"
		}
		if res.SyncNeeded != "" {
			msg += "; sync_needed: " + res.SyncNeeded
		}
		return errUnavailable(msg)
	}
	return err
}

func errForbidden(msg string) error {
	return &httpError{status: http.StatusForbidden, message: msg}
}

func errConflict(msg string) error {
	return &httpError{status: http.StatusConflict, message: msg}
}

func errUnavailable(msg string) error {
	return &httpError{status: http.StatusServiceUnavailable, message: msg}
}

func knownStatusNames(schema *scopeconfig.Schema) []string {
	out := status.Builtins()
	var extra []string
	for name := range schema.CustomStatuses() {
		extra = append(extra, name)
	}
	sort.Strings(extra)
	return append(out, extra...)
}

func ticketWritable(schema *scopeconfig.Schema, parseError bool) bool {
	return schema != nil && !parseError
}

// ticketWriteControls is the GET-time write affordance. Unusable config
// (nil schema) and parse-quarantined rows are already known to the page;
// the engine will refuse those POSTs, so the forms stay off.
func ticketWriteControls(schema *scopeconfig.Schema, statusName string, parseError bool) (canClaim bool, mark []string) {
	if !ticketWritable(schema, parseError) {
		return false, nil
	}
	return statusName == status.Todo, markStatuses(knownStatusNames(schema), statusName)
}

func markStatuses(known []string, current string) []string {
	out := make([]string, 0, len(known))
	for _, name := range known {
		if name == current {
			continue
		}
		if current == status.Todo && name == status.InProgress {
			continue
		}
		out = append(out, name)
	}
	return out
}
