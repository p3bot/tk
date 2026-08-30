package writeengine

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/frontmatter"
	"github.com/p3bot/tk/internal/id"
	"github.com/p3bot/tk/internal/status"
)

func TestCreateScaffoldAndTerminalPath(t *testing.T) {
	e := newPlainEnv(t, "wc", "name: \"wc\"\nautoCommit: false\n")

	res, err := Create(e.deps, CreateInput{Scope: "wc", Dir: e.dir, Title: "Network redesign"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if res.Path == "" || !strings.HasSuffix(res.Path, "-network-redesign.md") {
		t.Errorf("path = %q", res.Path)
	}
	if !id.IsFullTicketID(res.ID) {
		t.Errorf("id = %q", res.ID)
	}
	if res.ScaffoldCue != res.ID+" scaffolded with frontmatter" {
		t.Errorf("scaffold = %q", res.ScaffoldCue)
	}
	if res.ArchiveNote != "" {
		t.Errorf("non-terminal archive note = %q", res.ArchiveNote)
	}
	if res.NewStatus != status.Draft {
		t.Errorf("status = %q", res.NewStatus)
	}
	data, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	if !strings.Contains(body, "status: draft") {
		t.Errorf("want draft, got %s", body)
	}
	if !strings.Contains(body, "\norder: \"a0\"\n") {
		t.Errorf("want quoted a0, got %s", body)
	}
	if !strings.HasSuffix(body, "# Network redesign\n") {
		t.Errorf("H1: %q", body)
	}
	if strings.Contains(body, "tags:") {
		t.Errorf("no-tag create must omit tags, got %s", body)
	}

	term, err := Create(e.deps, CreateInput{Scope: "wc", Dir: e.dir, Title: "Already done", Status: "done"})
	if err != nil {
		t.Fatalf("terminal create: %v", err)
	}
	if filepath.Dir(term.Path) != filepath.Join(e.dir, "archive") {
		t.Errorf("terminal path = %q, want archive/", term.Path)
	}
	if !strings.Contains(term.ArchiveNote, "not git-durable") {
		t.Errorf("archive note = %q", term.ArchiveNote)
	}
}

func TestCreateMintSkipsTakenAndTags(t *testing.T) {
	e := newPlainEnv(t, "wc", "name: \"wc\"\nautoCommit: false\n")
	addTicket(t, e.dir, "wc-ab2c", "todo")
	writeFile(t, filepath.Join(e.dir, "wc-de34-old.md"), "---\nid: wc-de34\nstatus: done\norder: \"a1\"\ncreated: 2026-01-01T00:00:00Z\ntags: [legacy]\n---\n# Old\n")

	res, err := Create(e.deps, CreateInput{
		Scope: "wc", Dir: e.dir, Title: "Tagged", Status: "todo",
		Tags: []string{"alpha", "legacy", "alpha", "beta"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if res.ID == "wc-ab2c" || res.ID == "wc-de34" {
		t.Errorf("mint collided with taken id %s", res.ID)
	}
	m := parseTicket(t, res.Path)
	if !equalStrings(m.Tags, []string{"alpha", "legacy", "beta"}) {
		t.Errorf("tags = %v", m.Tags)
	}
	if !equalStrings(res.TagNew, []string{"alpha", "beta"}) {
		t.Errorf("TagNew = %v", res.TagNew)
	}

	_, err = Create(e.deps, CreateInput{Scope: "wc", Dir: e.dir, Title: "X", Tags: []string{""}})
	var use *UsageError
	if !errors.As(err, &use) || !strings.Contains(use.Msg, "create tag must be non-empty") {
		t.Errorf("empty tag: %v", err)
	}
	_, err = Create(e.deps, CreateInput{Scope: "wc", Dir: e.dir, Title: ""})
	if !errors.As(err, &use) || !strings.Contains(use.Msg, "non-empty title") {
		t.Errorf("empty title: %v", err)
	}
	_, err = Create(e.deps, CreateInput{Scope: "wc", Dir: e.dir, Title: "   "})
	if !errors.As(err, &use) || !strings.Contains(use.Msg, "non-empty title") {
		t.Errorf("whitespace title: %v", err)
	}
}

func TestCreateMintRedrawsTaken(t *testing.T) {
	e := newPlainEnv(t, "wc", "name: \"wc\"\nautoCommit: false\n")
	addTicket(t, e.dir, "wc-aaaa", "todo")

	// One Mint from all-zero bytes is "aaaa"; the next draw from 0x01 is "b333".
	src := io.MultiReader(
		bytes.NewReader(bytes.Repeat([]byte{0x00}, 7)),
		bytes.NewReader(bytes.Repeat([]byte{0x01}, 16)),
	)
	res, err := Create(e.deps, CreateInput{Scope: "wc", Dir: e.dir, Title: "Redraw", Rand: src})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if res.ID == "wc-aaaa" {
		t.Fatal("mint reused the taken short-id")
	}
	if res.ID != "wc-b333" {
		t.Errorf("id = %q, want wc-b333 after redraw", res.ID)
	}
}

func TestCreateNeverSelfCommits(t *testing.T) {
	e, repo := initAutoCommitRepo(t, "wc")
	res, err := Create(e.deps, CreateInput{Scope: "wc", Dir: e.dir, Title: "Work"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if res.SyncNeeded != "dirty" {
		t.Errorf("SyncNeeded = %q, want dirty", res.SyncNeeded)
	}
	log := gitLog(t, repo)
	if len(log) != 0 {
		t.Errorf("create must not self-commit, log=%v", log)
	}
}

func parseTicket(t *testing.T, path string) *frontmatter.Model {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	interior, _, present := frontmatter.Split(data)
	if !present {
		t.Fatalf("no fence in %s", path)
	}
	m, err := frontmatter.Parse(interior)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
