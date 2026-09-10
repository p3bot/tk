package writeengine

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/frontmatter"
	"github.com/p3bot/tk/internal/token"
)

func TestSpliceFenceUnchangedH1AndBody(t *testing.T) {
	e := newPlainEnv(t, "wc", "name: \"wc\"\nautoCommit: false\n")
	path := filepath.Join(e.dir, "wc-ab2c-work.md")
	raw := "---\nid: wc-ab2c\nstatus: todo\norder: \"a0\"\ncreated: 2026-01-01T00:00:00Z\nfoo: bar\n---\n# Work\nhello\n"
	writeFile(t, path, raw)
	_, base, err := FileSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}

	res, err := Splice(e.deps, SpliceInput{
		Scope:  "wc",
		Dir:    e.dir,
		Lookup: fullLookup("wc-ab2c"),
		Title:  "Renamed title",
		Body:   "new body\n\nparagraph\n",
		Base:   base,
	})
	if err != nil {
		t.Fatalf("splice: %v", err)
	}
	if res.Path != path && !strings.HasSuffix(res.Path, "wc-ab2c-work.md") {
		t.Errorf("path = %q, want frozen slug", res.Path)
	}
	if filepath.Base(res.Path) != "wc-ab2c-work.md" {
		t.Errorf("basename = %q, H1 must not rename", filepath.Base(res.Path))
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	wantFence, _, ok := frontmatter.Split([]byte(raw))
	if !ok {
		t.Fatal("fixture fence")
	}
	gotFence, gotBody, ok := frontmatter.Split(got)
	if !ok {
		t.Fatal("written fence")
	}
	if !bytes.Equal(wantFence, gotFence) {
		t.Errorf("fence interior changed:\n%s\n---\n%s", wantFence, gotFence)
	}
	prefix := []byte(raw)[:len(raw)-len("# Work\nhello\n")]
	if !bytes.HasPrefix(got, prefix) {
		t.Errorf("fence slice changed:\n%q\n---\n%q", prefix, got)
	}
	if string(gotBody) != "# Renamed title\nnew body\n\nparagraph\n" {
		t.Errorf("body = %q", gotBody)
	}
	m := parseTicket(t, path)
	if m.ID != "wc-ab2c" || m.Created != "2026-01-01T00:00:00Z" || m.Order != "a0" || m.Status != "todo" {
		t.Errorf("builtin keys mutated: %+v", m)
	}
	if len(m.Custom) != 1 || m.Custom[0].Key != "foo" {
		t.Errorf("custom keys: %+v", m.Custom)
	}
}

func TestSpliceCRLFAndBareCRBecomeLF(t *testing.T) {
	e := newPlainEnv(t, "wc", "name: \"wc\"\nautoCommit: false\n")
	path := filepath.Join(e.dir, "wc-ab2c-work.md")
	raw := "---\r\nid: wc-ab2c\r\nstatus: todo\r\norder: \"a0\"\r\ncreated: 2026-01-01T00:00:00Z\r\n---\r\n# Work\nhello\n"
	writeFile(t, path, raw)
	_, base, err := FileSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}

	_, err = Splice(e.deps, SpliceInput{
		Scope:  "wc",
		Dir:    e.dir,
		Lookup: fullLookup("wc-ab2c"),
		Title:  "Work",
		Body:   "line1\r\nline2\rline3\n",
		Base:   base,
	})
	if err != nil {
		t.Fatalf("splice: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	wantPrefix := []byte("---\r\nid: wc-ab2c\r\nstatus: todo\r\norder: \"a0\"\r\ncreated: 2026-01-01T00:00:00Z\r\n---\r\n")
	if !bytes.HasPrefix(got, wantPrefix) {
		t.Fatalf("fence slice lost CR:\n%q\n---\n%q", wantPrefix, got)
	}
	rest := got[len(wantPrefix):]
	if bytes.Contains(rest, []byte{'\r'}) {
		t.Errorf("body region has CR: %q", rest)
	}
	if string(rest) != "# Work\nline1\nline2\nline3\n" {
		t.Errorf("body = %q", rest)
	}

	_, base, err = FileSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Splice(e.deps, SpliceInput{
		Scope:  "wc",
		Dir:    e.dir,
		Lookup: fullLookup("wc-ab2c"),
		Title:  "Work",
		Body:   "line1\nline2\nline3\n",
		Base:   base,
	})
	if err != nil {
		t.Fatalf("noop splice: %v", err)
	}
	again, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(again[len(wantPrefix):], []byte{'\r'}) {
		t.Errorf("noop introduced CR: %q", again)
	}
	if !bytes.Equal(got, again) {
		t.Errorf("noop mutated file:\n%q\n---\n%q", got, again)
	}
}

func TestSplicePastedATXH1IsTitle(t *testing.T) {
	e := newPlainEnv(t, "wc", "name: \"wc\"\nautoCommit: false\n")
	path := addTicket(t, e.dir, "wc-ab2c", "todo")
	_, base, err := FileSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Splice(e.deps, SpliceInput{
		Scope: "wc", Dir: e.dir, Lookup: fullLookup("wc-ab2c"),
		Title: "# Renamed title", Body: "keep\n", Base: base,
	})
	if err != nil {
		t.Fatalf("splice: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_, body, ok := frontmatter.Split(got)
	if !ok {
		t.Fatal("fence")
	}
	if string(body) != "# Renamed title\nkeep\n" {
		t.Fatalf("body = %q, want H1 not H2", body)
	}
}

func TestSpliceEmptyBodyLegal(t *testing.T) {
	e := newPlainEnv(t, "wc", "name: \"wc\"\nautoCommit: false\n")
	path := addTicket(t, e.dir, "wc-ab2c", "todo")
	_, base, err := FileSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Splice(e.deps, SpliceInput{
		Scope: "wc", Dir: e.dir, Lookup: fullLookup("wc-ab2c"),
		Title: "Work", Body: "", Base: base,
	})
	if err != nil {
		t.Fatalf("empty body: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(got, []byte("# Work\n")) {
		t.Errorf("got %q", got)
	}
}

func TestSpliceRefusesEmptyTitleParseClobber(t *testing.T) {
	e := newPlainEnv(t, "wc", "name: \"wc\"\nautoCommit: false\n")
	path := addTicket(t, e.dir, "wc-ab2c", "todo")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_, base, err := FileSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}

	_, err = Splice(e.deps, SpliceInput{
		Scope: "wc", Dir: e.dir, Lookup: fullLookup("wc-ab2c"),
		Title: "   ", Body: "x", Base: base,
	})
	var use *UsageError
	if !errors.As(err, &use) || !strings.Contains(use.Msg, "non-empty title") {
		t.Errorf("empty title: %v", err)
	}

	_, err = Splice(e.deps, SpliceInput{
		Scope: "wc", Dir: e.dir, Lookup: fullLookup("wc-ab2c"),
		Title: "Work", Body: "x", Base: "",
	})
	if !errors.As(err, &use) || !strings.Contains(use.Msg, "clobber") {
		t.Errorf("missing base: %v", err)
	}

	writeFile(t, filepath.Join(e.dir, "wc-abcd-x.md"), "---\nid: wc-abcd\nstatus: [unterminated\n---\n# broke\n")
	_, err = Splice(e.deps, SpliceInput{
		Scope: "wc", Dir: e.dir, Lookup: fullLookup("wc-abcd"),
		Title: "broke", Body: "x", Base: "0:dead",
	})
	var pe *ParseQuarantineError
	if !errors.As(err, &pe) {
		t.Errorf("parse quarantine: got %v", err)
	}
	if err != nil && !strings.Contains(err.Error(), token.ParseError) {
		t.Errorf("want parse_error token, got %v", err)
	}

	if err := os.WriteFile(path, append(before, []byte("changed\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = Splice(e.deps, SpliceInput{
		Scope: "wc", Dir: e.dir, Lookup: fullLookup("wc-ab2c"),
		Title: "Work", Body: "nope", Base: base,
	})
	var cl *ClobberError
	if !errors.As(err, &cl) {
		t.Fatalf("stale base: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, append(before, []byte("changed\n")...)) {
		t.Errorf("clobber wrote: %q", got)
	}
}

func TestSpliceNeverSelfCommits(t *testing.T) {
	e, repo := initAutoCommitRepo(t, "wc")
	path := addTicket(t, e.dir, "wc-ab2c", "todo")
	_, base, err := FileSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Splice(e.deps, SpliceInput{
		Scope: "wc", Dir: e.dir, Lookup: fullLookup("wc-ab2c"),
		Title: "Work", Body: "edited\n", Base: base,
	})
	if err != nil {
		t.Fatalf("splice: %v", err)
	}
	if res.SyncNeeded != "dirty" {
		t.Errorf("SyncNeeded = %q, want dirty", res.SyncNeeded)
	}
	log := gitLog(t, repo)
	if len(log) != 0 {
		t.Errorf("splice must not self-commit, log=%v", log)
	}
}
