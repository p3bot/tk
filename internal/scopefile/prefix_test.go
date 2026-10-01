package scopefile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrefixMismatches(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("wc-ab2c-ok.md", "ok")
	write("at-a575-shape.md", "foreign")
	write("archive/at-b999-old.md", "old")
	write("archive/wc-de34-done.md", "done")
	write("design/at-qrst-shape.md", "design")
	write("design/wc-zzzz-mine.md", "mine")
	write("notes/at-abcd.md", "note")
	write("README.md", "nope")
	write("archive/sub/at-gh56-nested.md", "nested")

	got, err := PrefixMismatches(dir, "wc")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		filepath.Join(dir, "archive", "at-b999-old.md"),
		filepath.Join(dir, "at-a575-shape.md"),
		filepath.Join(dir, "design", "at-qrst-shape.md"),
	}
	if len(got) != len(want) {
		t.Fatalf("mismatches = %v want %v", got, want)
	}
	for i, f := range got {
		if f.Path != want[i] {
			t.Errorf("path[%d] = %s want %s", i, f.Path, want[i])
		}
	}
	if got[0].ID != "at-b999" || got[1].ID != "at-a575" || got[2].ID != "at-qrst" {
		t.Fatalf("ids = %s %s %s", got[0].ID, got[1].ID, got[2].ID)
	}
}

func TestPrefixMismatchesParkedOptionalDir(t *testing.T) {
	for _, name := range []string{"archive", "design"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, name), []byte("parked\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "at-a575-shape.md"), []byte("foreign\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			got, err := PrefixMismatches(dir, "wc")
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].ID != "at-a575" {
				t.Fatalf("mismatches = %+v", got)
			}
		})
	}
}
