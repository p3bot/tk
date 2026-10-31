package tkv

import (
	"log"
	"log/slog"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	summaryWindow = time.Minute
	// summaryClientsMax bounds one summary line's client list.
	summaryClientsMax = 32
)

// eventSummary folds a repeated client event into one log line per minute
// with the count and the distinct clients, so a flood costs bounded disk
// rather than one write per event.
type eventSummary struct {
	log       *slog.Logger
	msg       string
	afterFunc func(time.Duration, func()) *time.Timer

	mu        sync.Mutex
	count     int
	clients   []string
	truncated bool
	armed     bool
}

func newEventSummary(log *slog.Logger, msg string, afterFunc func(time.Duration, func()) *time.Timer) *eventSummary {
	return &eventSummary{log: log, msg: msg, afterFunc: afterFunc}
}

func (l *eventSummary) note(client string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.count++
	if !slices.Contains(l.clients, client) {
		if len(l.clients) < summaryClientsMax {
			l.clients = append(l.clients, client)
		} else {
			l.truncated = true
		}
	}
	if !l.armed {
		l.armed = true
		l.afterFunc(summaryWindow, l.flush)
	}
}

func (l *eventSummary) flush() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.armed = false
	if l.count == 0 {
		return
	}
	slices.Sort(l.clients)
	attrs := []any{"count", l.count, "clients", strings.Join(l.clients, ",")}
	if l.truncated {
		attrs = append(attrs, "clients_truncated", true)
	}
	l.log.Warn(l.msg, attrs...)
	l.count, l.clients, l.truncated = 0, nil, false
}

// handshakeErrorPrefix is how net/http's server reports a failed TLS
// handshake; the text has been stable across Go releases.
const handshakeErrorPrefix = "http: TLS handshake error from "

// serverErrorLog is the http.Server error log. Failed TLS handshakes, one per
// rejected browser connection or scanner probe, fold into a per-minute
// summary; every other line passes through.
func serverErrorLog(logger *slog.Logger, handshakes *eventSummary) *log.Logger {
	return log.New(serverErrorWriter{log: logger, handshakes: handshakes}, "", 0)
}

type serverErrorWriter struct {
	log        *slog.Logger
	handshakes *eventSummary
}

func (w serverErrorWriter) Write(p []byte) (int, error) {
	line := strings.TrimSpace(string(p))
	if rest, ok := strings.CutPrefix(line, handshakeErrorPrefix); ok {
		addr, _, _ := strings.Cut(rest, ": ")
		client := addr
		if ap, err := netip.ParseAddrPort(addr); err == nil {
			client = ap.Addr().Unmap().String()
		}
		w.handshakes.note(client)
		return len(p), nil
	}
	w.log.Warn(line)
	return len(p), nil
}
