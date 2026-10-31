package tkv

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/status"
)

func TestHostAllowlistAllows(t *testing.T) {
	hosts := newHostAllowlist(stableNames(defaultBind(DefaultPort), "", nil))
	tests := []struct {
		host string
		want bool
	}{
		{"127.0.0.1:8736", true},
		{"127.0.0.1", true},
		{"localhost:8736", true},
		{"localhost", true},
		{"[::1]:8736", true},
		{"[::1]", true},
		{"LOCALHOST:8736", true},
		{"LocalHost", true},
		{"localhost:9000", true},
		{"127.0.0.1:9000", true},
		{"rebind.evil.example", false},
		{"rebind.evil.example:8736", false},
		{"REBIND.evil.example:8736", false},
		{"127.0.0.2:8736", false},
		{"localhost.:8736", false},
		{"", false},
		{":8736", false},
		{"::1", false},
		{"::1:8736", false},
		{"[::1", false},
		{"::1]", false},
		{"[::1]]", false},
		{"[]", false},
		{"[localhost]", false},
		{"[localhost]:8736", false},
		{"[127.0.0.1]:8736", false},
	}
	for _, tt := range tests {
		if got := hosts.allows(tt.host, netip.Addr{}); got != tt.want {
			t.Errorf("allows(%q) = %v, want %v", tt.host, got, tt.want)
		}
	}
}

func TestHostGuardRunsNoHandlerForForeignHost(t *testing.T) {
	ran := false
	h := newHostAllowlist(stableNames(defaultBind(DefaultPort), "", nil)).guard(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		ran = true
	}))
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost} {
		ran = false
		req := httptest.NewRequest(method, "/doctor/reindex", strings.NewReader("x=1"))
		req.Host = "rebind.evil.example:8736"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusMisdirectedRequest {
			t.Fatalf("%s: want 421, got %d", method, w.Code)
		}
		if ran {
			t.Fatalf("%s: handler ran for a refused host", method)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/doctor/reindex", nil)
	req.Host = testHost
	h.ServeHTTP(httptest.NewRecorder(), req)
	if !ran {
		t.Fatal("handler did not run for an allowed host")
	}
}

func TestHostGuardRefusesForeignHost(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "")
	s := mustServer(t, app)
	mark := url.Values{"id": {"wc-ab2c"}, "status": {status.Done}}.Encode()

	tests := []struct {
		name   string
		method string
		path   string
		host   string
		body   string
	}{
		{"GET overview", http.MethodGet, "/", "rebind.evil.example:8736", ""},
		{"GET board", http.MethodGet, "/scope/wc", "rebind.evil.example:8736", ""},
		{"GET inspect", http.MethodGet, "/scope/wc/wc-ab2c", "rebind.evil.example", ""},
		{"GET static", http.MethodGet, "/static/style.css", "rebind.evil.example:8736", ""},
		{"GET missing host", http.MethodGet, "/", "", ""},
		{"POST mark", http.MethodPost, "/scope/wc/mark", "rebind.evil.example:8736", mark},
		{"POST mark missing host", http.MethodPost, "/scope/wc/mark", "", mark},
		{"POST reindex", http.MethodPost, "/doctor/reindex", "rebind.evil.example:8736", "x=1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			req.Host = tt.host
			if tt.method == http.MethodPost {
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				req.Header.Set("Sec-Fetch-Site", "same-origin")
				req.Header.Set("Origin", "http://"+tt.host)
			}
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, req)
			if w.Code != http.StatusMisdirectedRequest {
				t.Fatalf("want 421, got %d %s", w.Code, w.Body.String())
			}
			if want := fmt.Sprintf("host %q not allowed", tt.host); !strings.Contains(w.Body.String(), want) {
				t.Fatalf("refusal body %q does not name the host: want %q", w.Body.String(), want)
			}
			if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
				t.Fatalf("refusal Content-Type = %q, want text/plain", ct)
			}
			if strings.Contains(w.Body.String(), "Work") {
				t.Fatalf("refusal leaked ticket content: %s", w.Body.String())
			}
			if !strings.Contains(ticketBody(t, dir, "wc-ab2c"), "status: todo") {
				t.Fatalf("refused request wrote: %s", ticketBody(t, dir, "wc-ab2c"))
			}
		})
	}
}

func TestHostGuardAllowsLoopbackOnOtherPort(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "")
	s := mustServer(t, app)

	get := newRequest(http.MethodGet, "/scope/wc", nil)
	get.Host = "localhost:9000"
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, get)
	if w.Code != http.StatusOK {
		t.Fatalf("forwarded GET: want 200, got %d %s", w.Code, w.Body.String())
	}

	w = doPostHeader(s, "/scope/wc/mark", url.Values{"id": {"wc-ab2c"}, "status": {status.Done}}, http.Header{
		"Host":   {"localhost:9000"},
		"Origin": {"http://localhost:9000"},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("forwarded POST: want 303, got %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(ticketBody(t, dir, "wc-ab2c"), "status: done") {
		t.Fatalf("forwarded POST must write: %s", ticketBody(t, dir, "wc-ab2c"))
	}
}
