package repair

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/frontmatter"
)

func TestArchiveMovePreservesChangedBytes(t *testing.T) {
	dir := t.TempDir()
	with := ticket("wc-ab2c", "2026-01-01T00:00:00Z", "changed: 2026-02-02T03:04:05Z\n", "# A\n")
	without := ticket("wc-cd3e", "2026-01-01T00:00:00Z", "", "# B\n")
	withPath := writeRel(t, dir, "wc-ab2c-a.md", with)
	withoutPath := writeRel(t, dir, "wc-cd3e-b.md", without)

	op, err := ArchiveMove(dir, Row{Path: withPath}, true)
	if err != nil {
		t.Fatal(err)
	}
	if string(op.Content) != with {
		t.Fatalf("archive move rewrote a file that already had changed:\n%s", op.Content)
	}
	op, err = ArchiveMove(dir, Row{Path: withoutPath}, true)
	if err != nil {
		t.Fatal(err)
	}
	if string(op.Content) != without || strings.Contains(string(op.Content), "changed:") {
		t.Fatalf("archive move invented changed:\n%s", op.Content)
	}
}

func TestOrderRewriteRoundTripsChanged(t *testing.T) {
	dir := t.TempDir()
	withPath := writeRel(t, dir, "wc-ab2c-a.md", ticket("wc-ab2c", "2026-01-01T00:00:00Z", "changed: 2026-02-02T03:04:05Z\n", "# A\n"))
	withoutPath := writeRel(t, dir, "wc-cd3e-b.md", ticket("wc-cd3e", "2026-01-01T00:00:00Z", "", "# B\n"))

	withOp, err := orderRewriteOp(withPath, "a1")
	if err != nil {
		t.Fatal(err)
	}
	if fenceChanged(t, string(withOp.Content)) != "2026-02-02T03:04:05Z" {
		t.Fatalf("order re-space dropped changed:\n%s", withOp.Content)
	}
	if !strings.Contains(string(withOp.Content), "order: \"a1\"\n") {
		t.Fatalf("order re-space did not write the new key:\n%s", withOp.Content)
	}
	withoutOp, err := orderRewriteOp(withoutPath, "a2")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(withoutOp.Content), "changed:") {
		t.Fatalf("order re-space invented changed:\n%s", withoutOp.Content)
	}
}

func TestAdoptPrefixRoundTripsChanged(t *testing.T) {
	dir := t.TempDir()
	writeRel(t, dir, "at-ab2c-a.md", ticket("at-ab2c", "2026-01-01T00:00:00Z", "changed: 2026-02-02T03:04:05Z\n", "# A\n"))
	writeRel(t, dir, "at-cd3e-b.md", ticket("at-cd3e", "2026-01-01T00:00:00Z", "", "# B\n"))

	ops, renames, err := AdoptPrefix("wc", dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(renames) != 2 {
		t.Fatalf("renames = %+v", renames)
	}
	byBase := map[string]string{}
	for _, op := range ops {
		if op.NewPath == "" {
			continue
		}
		byBase[filepath.Base(op.NewPath)] = string(op.Content)
	}
	with := byBase["wc-ab2c-a.md"]
	if fenceChanged(t, with) != "2026-02-02T03:04:05Z" || !strings.Contains(with, "id: wc-ab2c\n") {
		t.Fatalf("prefix rewrite dropped changed:\n%s", with)
	}
	without := byBase["wc-cd3e-b.md"]
	if strings.Contains(without, "changed:") || !strings.Contains(without, "id: wc-cd3e\n") {
		t.Fatalf("prefix rewrite invented changed:\n%s", without)
	}
}

func fenceChanged(t *testing.T, raw string) string {
	t.Helper()
	interior, _, ok := frontmatter.Split([]byte(raw))
	if !ok {
		t.Fatalf("no frontmatter fence:\n%s", raw)
	}
	m, err := frontmatter.Parse(interior)
	if err != nil {
		t.Fatalf("parse: %v\n%s", err, raw)
	}
	return m.Changed
}
