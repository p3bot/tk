package writeengine

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCreateChangedMatchesCreated(t *testing.T) {
	e := newPlainEnv(t, "wc", "name: \"wc\"\nautoCommit: false\n")
	now := time.Date(2026, 3, 4, 5, 6, 7, 0, time.FixedZone("AEST", 10*3600))
	want := now.Format(time.RFC3339)

	res, err := Create(e.deps, CreateInput{Scope: "wc", Dir: e.dir, Title: "Network redesign", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	assertChangedStamp(t, res.Path, want, want)

	term, err := Create(e.deps, CreateInput{Scope: "wc", Dir: e.dir, Title: "Already done", Status: "done", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(term.Path) != filepath.Join(e.dir, "archive") {
		t.Fatalf("terminal path = %q", term.Path)
	}
	assertChangedStamp(t, term.Path, want, want)
}

func TestMarkAndClaimSetChangedOnlyOnStatusChange(t *testing.T) {
	e := newPlainEnv(t, "wc", "name: \"wc\"\nautoCommit: false\n")
	first := time.Date(2026, 4, 5, 6, 7, 8, 0, time.UTC)
	second := time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC)
	e.deps.Now = first

	path := filepath.Join(e.dir, "wc-ab2c-work.md")
	writeFile(t, path, "---\nid: wc-ab2c\nstatus: todo\nchanged: 2026-01-01T00:00:00Z\norder: \"a0\"\ncreated: 2026-01-01T00:00:00Z\n---\n# Work\n")
	if _, err := Mark(e.deps, nil, MarkInput{Scope: "wc", Dir: e.dir, Lookups: []Lookup{fullLookup("wc-ab2c")}, NewStatus: "todo"}); err != nil {
		t.Fatal(err)
	}
	if got := parseTicket(t, path).Changed; got != "2026-01-01T00:00:00Z" {
		t.Fatalf("same-status mark moved changed to %q", got)
	}

	if _, err := Mark(e.deps, nil, MarkInput{Scope: "wc", Dir: e.dir, Lookups: []Lookup{fullLookup("wc-ab2c")}, NewStatus: "review"}); err != nil {
		t.Fatal(err)
	}
	if got := parseTicket(t, path).Changed; got != first.Format(time.RFC3339) {
		t.Fatalf("status change changed = %q, want %s", got, first.Format(time.RFC3339))
	}

	e.deps.Now = second
	if _, err := Mark(e.deps, nil, MarkInput{Scope: "wc", Dir: e.dir, Lookups: []Lookup{fullLookup("wc-ab2c")}, NewStatus: "review"}); err != nil {
		t.Fatal(err)
	}
	if got := parseTicket(t, path).Changed; got != first.Format(time.RFC3339) {
		t.Fatalf("second same-status mark moved changed to %q", got)
	}

	bare := filepath.Join(e.dir, "wc-cd3e-work.md")
	writeFile(t, bare, "---\nid: wc-cd3e\nstatus: draft\norder: \"a1\"\ncreated: 2026-01-01T00:00:00Z\n---\n# Bare\n")
	if _, err := Mark(e.deps, nil, MarkInput{Scope: "wc", Dir: e.dir, Lookups: []Lookup{fullLookup("wc-cd3e")}, NewStatus: "draft"}); err != nil {
		t.Fatal(err)
	}
	if changedKeyPresent(t, bare) {
		t.Fatal("same-status mark invented changed")
	}
	if _, err := Mark(e.deps, nil, MarkInput{Scope: "wc", Dir: e.dir, Lookups: []Lookup{fullLookup("wc-cd3e")}, NewStatus: "backlog"}); err != nil {
		t.Fatal(err)
	}
	if got := parseTicket(t, bare).Changed; got != second.Format(time.RFC3339) {
		t.Fatalf("first status change on a bare ticket = %q", got)
	}

	claimPath := filepath.Join(e.dir, "wc-ef4g-work.md")
	writeFile(t, claimPath, "---\nid: wc-ef4g\nstatus: todo\nchanged: 2026-01-01T00:00:00Z\norder: \"a2\"\ncreated: 2026-01-01T00:00:00Z\n---\n# Claim\n")
	claimAt := time.Date(2026, 6, 7, 8, 9, 10, 0, time.UTC)
	e.deps.Now = claimAt
	if _, err := Claim(e.deps, nil, ClaimInput{Kind: ClaimID, Scope: "wc", Dir: e.dir, Lookup: fullLookup("wc-ef4g")}); err != nil {
		t.Fatal(err)
	}
	if got := parseTicket(t, claimPath).Changed; got != claimAt.Format(time.RFC3339) {
		t.Fatalf("claim changed = %q, want %s", got, claimAt.Format(time.RFC3339))
	}
	if parseTicket(t, claimPath).Status != "in-progress" {
		t.Fatal("claim did not set in-progress")
	}
}

func TestOrderMetaRehomeLeaveChanged(t *testing.T) {
	e := newPlainEnv(t, "wc", "name: \"wc\"\nautoCommit: false\n")
	kept := filepath.Join(e.dir, "wc-ab2c-work.md")
	bare := filepath.Join(e.dir, "wc-cd3e-work.md")
	writeFile(t, kept, "---\nid: wc-ab2c\nstatus: todo\nchanged: 2026-02-02T03:04:05Z\norder: \"a0\"\ncreated: 2026-01-01T00:00:00Z\n---\n# Kept\n")
	writeFile(t, bare, "---\nid: wc-cd3e\nstatus: todo\norder: \"a1\"\ncreated: 2026-01-01T00:00:00Z\n---\n# Bare\n")

	if _, err := Order(e.deps, OrderInput{Scope: "wc", Dir: e.dir, Lookup: fullLookup("wc-ab2c"), Dest: Dest{Last: true}}); err != nil {
		t.Fatal(err)
	}
	if _, err := Order(e.deps, OrderInput{Scope: "wc", Dir: e.dir, Lookup: fullLookup("wc-cd3e"), Dest: Dest{First: true}}); err != nil {
		t.Fatal(err)
	}
	if got := parseTicket(t, kept).Changed; got != "2026-02-02T03:04:05Z" {
		t.Fatalf("order moved changed to %q", got)
	}
	if changedKeyPresent(t, bare) {
		t.Fatal("order invented changed")
	}

	if _, err := Meta(e.deps, MetaInput{Scope: "wc", Dir: e.dir, Lookup: fullLookup("wc-ab2c"), Op: MetaSet, Key: "summary", Value: "note"}); err != nil {
		t.Fatal(err)
	}
	m := parseTicket(t, kept)
	if m.Changed != "2026-02-02T03:04:05Z" || m.Summary != "note" {
		t.Fatalf("meta set summary changed=%q summary=%q", m.Changed, m.Summary)
	}
	before, err := os.ReadFile(bare)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Meta(e.deps, MetaInput{Scope: "wc", Dir: e.dir, Lookup: fullLookup("wc-cd3e"), Op: MetaSet, Key: "changed", Value: "2026-08-08T00:00:00Z"})
	var use *UsageError
	if !errors.As(err, &use) || !strings.Contains(use.Msg, "immutable") {
		t.Fatalf("meta set changed: %v", err)
	}
	after, err := os.ReadFile(bare)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("refused meta set wrote the file:\n%s", after)
	}

	d := newDualPlain(t, "foo", "bar", "", "")
	writeRehomeTicket(t, d.srcDir, "foo-ab2c", "todo", "a0", "changed: 2026-02-02T03:04:05Z\n", "# Moved\n")
	writeRehomeTicket(t, d.srcDir, "foo-cd3e", "todo", "a1", "", "# Bare\n")
	keptRes, err := rehomeFoo(d, "foo-ab2c")
	if err != nil {
		t.Fatal(err)
	}
	bareRes, err := rehomeFoo(d, "foo-cd3e")
	if err != nil {
		t.Fatal(err)
	}
	if got := parseTicket(t, keptRes.Path).Changed; got != "2026-02-02T03:04:05Z" {
		t.Fatalf("rehome moved changed to %q", got)
	}
	if changedKeyPresent(t, bareRes.Path) {
		t.Fatal("rehome invented changed")
	}
}

func assertChangedStamp(t *testing.T, path, created, changed string) {
	t.Helper()
	m := parseTicket(t, path)
	if m.Created != created || m.Changed != changed {
		t.Fatalf("created=%q changed=%q, want %q and %q", m.Created, m.Changed, created, changed)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	statusAt := strings.Index(text, "\nstatus:")
	changedAt := strings.Index(text, "\nchanged:")
	orderAt := strings.Index(text, "\norder:")
	if statusAt < 0 || changedAt < 0 || orderAt < 0 || !(statusAt < changedAt && changedAt < orderAt) {
		t.Fatalf("changed must follow status:\n%s", text)
	}
}

func changedKeyPresent(t *testing.T, path string) bool {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Contains(raw, []byte("\nchanged:"))
}
