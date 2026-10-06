package design

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/p3bot/tk/internal/bodyedit"
	"github.com/p3bot/tk/internal/frontmatter"
)

func TestSpliceLeavesFenceBytes(t *testing.T) {
	deps, dir := plainDeps(t)
	raw := "---\nid: wc-ab2c\nstatus: draft\nchanged: 2026-02-02T03:04:05Z\ncreated: 2026-01-01T00:00:00Z\nproduces: [wc-m4np]\n---\n# Shape\nhello\n"
	path := filepath.Join(dir, "design", "wc-ab2c-shape.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	_, base, err := bodyedit.Snapshot(path)
	if err != nil {
		t.Fatal(err)
	}

	res, err := Splice(deps, SpliceInput{
		IDInput: IDInput{Scope: "wc", Dir: dir, Arg: "wc-ab2c", Full: true},
		Title:   "# Renamed shape",
		Body:    "new body\r\nline\n",
		Base:    base,
	})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(res.Path) != "wc-ab2c-shape.md" {
		t.Fatalf("path = %s, H1 must not rename the file", res.Path)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	prefix := []byte(raw)[:len(raw)-len("# Shape\nhello\n")]
	if !bytes.HasPrefix(got, prefix) {
		t.Fatalf("fence slice changed:\n%q\n---\n%q", prefix, got)
	}
	_, body, ok := frontmatter.Split(got)
	if !ok {
		t.Fatal("fence")
	}
	if string(body) != "# Renamed shape\nnew body\nline\n" {
		t.Fatalf("body = %q", body)
	}
	if bytes.Contains(got[len(prefix):], []byte{'\r'}) {
		t.Fatalf("body region has CR: %q", got[len(prefix):])
	}

	_, err = Splice(deps, SpliceInput{
		IDInput: IDInput{Scope: "wc", Dir: dir, Arg: "wc-ab2c", Full: true},
		Title:   "Renamed shape",
		Body:    "new body\nline\n",
		Base:    "stale",
	})
	var cl *ClobberError
	if !errors.As(err, &cl) {
		t.Fatalf("stale base: %v", err)
	}
	again, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, again) {
		t.Fatal("stale base wrote")
	}
}

func TestSpliceKeepsTextBeforeHeading(t *testing.T) {
	deps, dir := plainDeps(t)
	raw := "---\nid: wc-ab2c\nstatus: draft\ncreated: 2026-01-01T00:00:00Z\n---\nSee the notes below.\n\n# Shape\n\nThe sockets.\n"
	path := filepath.Join(dir, "design", "wc-ab2c-shape.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	_, base, err := bodyedit.Snapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Splice(deps, SpliceInput{
		IDInput: IDInput{Scope: "wc", Dir: dir, Arg: "wc-ab2c", Full: true},
		Title:   "Shape",
		Lead:    "See the notes below.\n\n",
		Body:    "\nThe sockets.\n",
		Base:    base,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != raw {
		t.Fatalf("file = %q", got)
	}
}

func TestMarkRejectsTicketStatus(t *testing.T) {
	deps, dir := plainDeps(t)
	raw := "---\nid: wc-ab2c\nstatus: draft\ncreated: 2026-01-01T00:00:00Z\n---\n# Shape\n"
	path := filepath.Join(dir, "design", "wc-ab2c-shape.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Mark(deps, MarkInput{
		IDInput: IDInput{Scope: "wc", Dir: dir, Arg: "wc-ab2c", Full: true},
		Status:  "todo",
	})
	var unk *UnknownStatusError
	if !errors.As(err, &unk) {
		t.Fatalf("mark todo: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != raw {
		t.Fatalf("file changed:\n%s", got)
	}
}
