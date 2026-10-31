package tkv

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"cuelang.org/go/cue/cuecontext"

	"github.com/p3bot/tk/internal/passhash"
	"github.com/p3bot/tk/internal/tkvconf"
)

const (
	sessionCookie = "__Host-tkv"
	// sessionMaxAge is the 400-day cap browsers apply. The cookie is
	// re-issued on each authenticated page load; the server checks no expiry.
	sessionMaxAge = 400 * 24 * 60 * 60
	loginBodyMax  = 8 << 10
)

// auth is network mode's login state. A nil *auth on Server is loopback mode.
type auth struct {
	users    *userSource
	key      []byte
	throttle *loginThrottle
	refused  *eventSummary
	log      *slog.Logger
	// verify is passhash.Verify; tests substitute a slow or counting one.
	verify func(stored, password string) (bool, error)
}

func newAuth(configPath string, key []byte, log *slog.Logger) *auth {
	return &auth{
		users:    newUserSource(configPath, log),
		key:      key,
		throttle: newLoginThrottle(time.Now),
		refused:  newEventSummary(log, "login attempts refused by throttle", time.AfterFunc),
		log:      log,
		verify:   passhash.Verify,
	}
}

// userSource is the user map from tkv.cue, reloaded when the file is no
// longer the one last read. tkv writes by atomic rename, so every tkv write
// changes the inode; that catches a password change whose hash keeps the file
// size within one timestamp tick. Modification time and size catch hand edits.
type userSource struct {
	path string
	log  *slog.Logger
	snap atomic.Pointer[userSnapshot]
	// mu serialises reloads so one change is read once.
	mu sync.Mutex
}

type userSnapshot struct {
	fi    os.FileInfo // nil when the file was missing
	users map[string]string
	err   error
}

func newUserSource(path string, log *slog.Logger) *userSource {
	return &userSource{path: path, log: log}
}

func (s *userSnapshot) matches(fi os.FileInfo) bool {
	if s.fi == nil || fi == nil {
		return s.fi == nil && fi == nil
	}
	return os.SameFile(s.fi, fi) && s.fi.ModTime().Equal(fi.ModTime()) && s.fi.Size() == fi.Size()
}

// current is the user map, or the error that keeps the gate closed.
func (u *userSource) current() (map[string]string, error) {
	fi, err := u.stat()
	if err != nil {
		return nil, err
	}
	if snap := u.snap.Load(); snap != nil && snap.matches(fi) {
		return snap.users, snap.err
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if fi, err = u.stat(); err != nil {
		return nil, err
	}
	if snap := u.snap.Load(); snap != nil && snap.matches(fi) {
		return snap.users, snap.err
	}
	snap := &userSnapshot{fi: fi}
	if fi != nil {
		data, err := os.ReadFile(u.path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			snap.fi = nil
		case err != nil:
			snap.err = err
		default:
			// A fresh context per reload: a long-lived one keeps every compilation.
			c, err := tkvconf.Parse(cuecontext.New(), u.path, data)
			snap.users, snap.err = c.Users, err
		}
	}
	if snap.err != nil {
		u.log.Error("tkv.cue reload failed; refusing every request until it evaluates", "err", snap.err)
	} else if prev := u.snap.Load(); prev != nil {
		u.log.Info("tkv.cue reloaded", "users", len(snap.users))
	}
	u.snap.Store(snap)
	return snap.users, snap.err
}

func (u *userSource) stat() (os.FileInfo, error) {
	fi, err := os.Stat(u.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", u.path, err)
	}
	return fi, nil
}

// sessionMAC binds a cookie to the user's stored hash, so a password change
// or a delete revokes every cookie for that user with no server-side state.
func sessionMAC(key []byte, user, hash string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(user))
	m.Write([]byte{0})
	m.Write([]byte(hash))
	return m.Sum(nil)
}

func sessionValue(key []byte, user, hash string) string {
	return user + "." + base64.RawURLEncoding.EncodeToString(sessionMAC(key, user, hash))
}

// sessionUser verifies a cookie value against users. The value splits on the
// last dot: a username may hold dots and the mac encoding never does.
func sessionUser(key []byte, users map[string]string, value string) (string, bool) {
	i := strings.LastIndexByte(value, '.')
	if i < 0 {
		return "", false
	}
	user, enc := value[:i], value[i+1:]
	hash, ok := users[user]
	if !ok {
		return "", false
	}
	mac, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return "", false
	}
	return user, hmac.Equal(mac, sessionMAC(key, user, hash))
}

func (a *auth) sessionCookie(user, hash string) *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookie,
		Value:    sessionValue(a.key, user, hash),
		Path:     "/",
		MaxAge:   sessionMaxAge,
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
}

func clearedSessionCookie() *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
}

func (a *auth) session(r *http.Request, users map[string]string) (string, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return "", false
	}
	return sessionUser(a.key, users, c.Value)
}

// gateExempt is the whole unauthenticated surface: the login form and the
// static assets it is styled with.
func gateExempt(path string) bool {
	return path == "/login" || strings.HasPrefix(path, "/static/")
}

// gate wraps the router in network mode. With no valid cookie a GET is sent
// to the login page and anything else gets 401; no handler runs. A tkv.cue
// that no longer evaluates fails closed.
func (s *Server) gate(next http.Handler) http.Handler {
	a := s.auth
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		static := strings.HasPrefix(r.URL.Path, "/static/")
		users, err := a.users.current()
		if err != nil && !static {
			s.plainPage(w, http.StatusServiceUnavailable, "Unavailable",
				"tkv.cue on the tkv machine does not evaluate. Sign-in is refused until it is fixed; the details are in tkv.log.")
			return
		}
		if gateExempt(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		user, ok := a.session(r, users)
		if !ok {
			if r.Method == http.MethodGet {
				http.Redirect(w, r, "/login?next="+url.QueryEscape(requestPath(r)), http.StatusSeeOther)
				return
			}
			s.plainPage(w, http.StatusUnauthorized, "Sign in required", "Sign in to tkv first.")
			return
		}
		if r.Method == http.MethodGet {
			http.SetCookie(w, a.sessionCookie(user, users[user]))
		}
		next.ServeHTTP(w, r)
	})
}

// localNext keeps a post-login redirect on this origin: a path that starts
// with one slash and that url.Parse accepts. url.Parse refuses the control
// characters browsers strip ("/\t/host" becomes "//host"), and browsers read
// a backslash as a slash.
func localNext(next string) string {
	if _, err := url.Parse(next); err != nil ||
		!strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.Contains(next, `\`) {
		return "/"
	}
	return next
}

type loginPage struct {
	Title string
	Next  string
	User  string
	Error string
}

func (s *Server) getLogin(w http.ResponseWriter, r *http.Request) {
	s.renderLogin(w, http.StatusOK, loginPage{Next: localNext(r.URL.Query().Get("next"))})
}

func (s *Server) renderLogin(w http.ResponseWriter, code int, p loginPage) {
	p.Title = "Sign in"
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = s.tpl.ExecuteTemplate(w, "login", p)
}

func (s *Server) postLogin(w http.ResponseWriter, r *http.Request) {
	a := s.auth
	r.Body = http.MaxBytesReader(w, r.Body, loginBodyMax)
	if err := r.ParseForm(); err != nil {
		s.plainPage(w, http.StatusBadRequest, "Bad request", "The sign-in form could not be read.")
		return
	}
	user := r.PostForm.Get("username")
	password := r.PostForm.Get("password")
	page := loginPage{Next: localNext(r.PostForm.Get("next")), User: user, Error: "Wrong username or password."}
	if len(password) > tkvconf.MaxPasswordBytes {
		s.renderLogin(w, http.StatusUnauthorized, page)
		return
	}
	users, err := a.users.current()
	if err != nil {
		s.plainPage(w, http.StatusServiceUnavailable, "Unavailable", "tkv.cue on the tkv machine does not evaluate.")
		return
	}
	client := clientIP(r)
	admitted, wait := a.throttle.admit()
	if !admitted {
		a.refused.note(client)
		w.Header().Set("Retry-After", fmt.Sprint(int((wait+time.Second-1)/time.Second)))
		page.Error = "Too many failed sign-ins. Wait a few seconds and try again."
		s.renderLogin(w, http.StatusTooManyRequests, page)
		return
	}
	stored, known := users[user]
	if !known {
		stored = passhash.Dummy
	}
	match, verr := a.verify(stored, password)
	ok := known && match && verr == nil
	a.throttle.finish(ok)
	if !ok {
		logged := "unknown"
		if known {
			logged = user
		}
		if verr != nil && known {
			a.log.Error("stored hash is malformed", "user", user, "err", verr)
		}
		a.log.Warn("login failed", "client", client, "user", logged)
		s.renderLogin(w, http.StatusUnauthorized, page)
		return
	}
	http.SetCookie(w, a.sessionCookie(user, stored))
	http.Redirect(w, r, page.Next, http.StatusSeeOther)
}

func (s *Server) postLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, clearedSessionCookie())
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// wrapSessionPost guards the login and logout POSTs. Their refusals render
// without chrome, which would list scopes to a signed-out client.
func (s *Server) wrapSessionPost(fn http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := checkWriteOrigin(r); err != nil {
			s.plainPage(w, http.StatusForbidden, "Forbidden", err.Error())
			return
		}
		fn(w, r)
	}
}

type plainPageData struct {
	Title   string
	Status  int
	Message string
}

// plainPage renders a status page with no chrome and no index read.
func (s *Server) plainPage(w http.ResponseWriter, code int, title, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = s.tpl.ExecuteTemplate(w, "plain", plainPageData{Title: title, Status: code, Message: message})
}

// clientIP is the connection's peer address. tkv sits behind no proxy, so
// forwarding headers are client-chosen and ignored.
func clientIP(r *http.Request) string {
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ap.Addr().Unmap().String()
}
