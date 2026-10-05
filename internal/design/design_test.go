package design

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cuelang.org/go/cue/cuecontext"

	"github.com/p3bot/tk/internal/frontmatter"
	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/reconcile"
)

func TestSerializeKeepsPresentChanged(t *testing.T) {
	m := &frontmatter.Model{
		ID: "wc-ab2c", Status: "draft", Changed: "2026-02-02T03:04:05Z",
		Order: "a0", Created: "2026-01-01T00:00:00Z",
	}
	out, err := Serialize(m)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	statusAt := strings.Index(text, "status:")
	changedAt := strings.Index(text, "changed:")
	orderAt := strings.Index(text, "order:")
	if statusAt < 0 || changedAt < 0 || orderAt < 0 || statusAt >= changedAt || changedAt >= orderAt {
		t.Fatalf("changed must follow status and precede order:\n%s", text)
	}
	back, err := frontmatter.Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if back.Changed != m.Changed {
		t.Fatalf("changed = %q", back.Changed)
	}

	m.Changed = ""
	out, err = Serialize(m)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "changed:") {
		t.Fatalf("empty changed was written:\n%s", out)
	}
}

func TestCreateAndMarkStampChangedFromNow(t *testing.T) {
	deps, dir := plainDeps(t)
	createdAt := time.Date(2026, 3, 4, 5, 6, 7, 0, time.FixedZone("AEST", 10*3600))
	markAt := time.Date(2026, 4, 5, 6, 7, 8, 0, time.UTC)
	againAt := time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC)
	bareAt := time.Date(2026, 6, 7, 8, 9, 10, 0, time.UTC)
	created := createdAt.Format(time.RFC3339)

	res, err := Create(deps, CreateInput{
		Scope: "wc", Dir: dir, Title: "Stamp the status", Now: createdAt, Rand: bytes.NewReader(make([]byte, 64)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, gotCreated := fenceChanged(t, res.Path); got != created || gotCreated != created {
		t.Fatalf("create changed=%q created=%q, want %q", got, gotCreated, created)
	}

	if _, err := Mark(deps, MarkInput{IDInput: IDInput{Scope: "wc", Dir: dir, Arg: res.ID, Full: true}, Status: StatusAccepted, Now: markAt}); err != nil {
		t.Fatal(err)
	}
	if got, gotCreated := fenceChanged(t, res.Path); got != markAt.Format(time.RFC3339) || gotCreated != created {
		t.Fatalf("mark changed=%q created=%q", got, gotCreated)
	}
	if status := fenceStatus(t, res.Path); status != StatusAccepted {
		t.Fatalf("status = %q", status)
	}

	if _, err := Mark(deps, MarkInput{IDInput: IDInput{Scope: "wc", Dir: dir, Arg: res.ID, Full: true}, Status: StatusAccepted, Now: againAt}); err != nil {
		t.Fatal(err)
	}
	if got, _ := fenceChanged(t, res.Path); got != markAt.Format(time.RFC3339) {
		t.Fatalf("same-status mark moved changed to %q", got)
	}

	bare := filepath.Join(dir, "design", "wc-cd3e-bare.md")
	body := "---\nid: wc-cd3e\nstatus: draft\ncreated: 2026-01-01T00:00:00Z\n---\n# Bare\n"
	if err := os.WriteFile(bare, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Mark(deps, MarkInput{IDInput: IDInput{Scope: "wc", Dir: dir, Arg: "wc-cd3e", Full: true}, Status: StatusDraft, Now: againAt}); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(bare); err != nil {
		t.Fatal(err)
	} else if strings.Contains(string(raw), "changed:") {
		t.Fatalf("same-status mark invented changed:\n%s", raw)
	}
	if _, err := Mark(deps, MarkInput{IDInput: IDInput{Scope: "wc", Dir: dir, Arg: "wc-cd3e", Full: true}, Status: StatusAccepted, Now: bareAt}); err != nil {
		t.Fatal(err)
	}
	if got, gotCreated := fenceChanged(t, bare); got != bareAt.Format(time.RFC3339) || gotCreated != "2026-01-01T00:00:00Z" {
		t.Fatalf("bare mark changed=%q created=%q", got, gotCreated)
	}
}

func plainDeps(t *testing.T) (Deps, string) {
	t.Helper()
	db, err := index.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	dir := filepath.Join(t.TempDir(), "wc")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cue := "name: \"wc\"\nautoCommit: false\n"
	if err := os.WriteFile(filepath.Join(dir, "tk.cue"), []byte(cue), 0o644); err != nil {
		t.Fatal(err)
	}
	return Deps{DB: db, Rec: reconcile.New(db, cuecontext.New())}, dir
}

func fenceChanged(t *testing.T, path string) (changed, created string) {
	t.Helper()
	m := fenceModel(t, path)
	return m.Changed, m.Created
}

func fenceStatus(t *testing.T, path string) string {
	t.Helper()
	return fenceModel(t, path).Status
}

func fenceModel(t *testing.T, path string) *frontmatter.Model {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	interior, _, ok := frontmatter.Split(raw)
	if !ok {
		t.Fatalf("no fence in %s", path)
	}
	m, err := frontmatter.Parse(interior)
	if err != nil {
		t.Fatal(err)
	}
	return m
}
