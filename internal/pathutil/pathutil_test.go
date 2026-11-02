package pathutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalResolvesSymlinksAndMissingTail(t *testing.T) {
	realDir := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(realDir, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	got := Canonical(filepath.Join(link, "sub", "missing"))
	wantReal, err := filepath.EvalSymlinks(realDir)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(wantReal, "sub", "missing")
	if got != want {
		t.Errorf("Canonical through symlink = %q want %q", got, want)
	}
}

func TestCanonicalIdempotentOnCleanAbs(t *testing.T) {
	dir := t.TempDir()
	got := Canonical(dir)
	if Canonical(got) != got {
		t.Errorf("Canonical not idempotent: %q -> %q", got, Canonical(got))
	}
	// Cleaning alone: trailing slash.
	if Canonical(dir+"/") != got {
		t.Errorf("Canonical should clean trailing separator, got %q want %q", Canonical(dir+"/"), got)
	}
}

func TestUnderOrEqual(t *testing.T) {
	cases := []struct {
		child, ancestor string
		want            bool
	}{
		{"/a/b", "/a/b", true},
		{"/a/b/c", "/a/b", true},
		{"/a/b", "/a/b/c", false},
		{"/a/bc", "/a/b", false}, // separator boundary: not nested
		{"/a/b", "/a/bc", false},
		{"/a/b/c", "/a", true},
		{"/x", "/a", false},
	}
	for _, c := range cases {
		if got := UnderOrEqual(c.child, c.ancestor); got != c.want {
			t.Errorf("UnderOrEqual(%q,%q)=%v want %v", c.child, c.ancestor, got, c.want)
		}
	}
}

func TestOverlap(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"/a/b", "/a/b", true},
		{"/a/b", "/a/b/c", true},
		{"/a/b/c", "/a/b", true},
		{"/a/b", "/a/c", false},
		{"/a/bc", "/a/b", false},
		{"/a", "/b", false},
	}
	for _, c := range cases {
		if got := Overlap(c.a, c.b); got != c.want {
			t.Errorf("Overlap(%q,%q)=%v want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestUnderOrEqualCollapsesSymlinkSpellings(t *testing.T) {
	target := t.TempDir()
	linkParent := t.TempDir()
	link := filepath.Join(linkParent, "repo")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	// child via link, ancestor via target (or the reverse): same tree.
	child := filepath.Join(link, "scope")
	if !UnderOrEqual(child, target) {
		t.Errorf("UnderOrEqual(%q, %q) = false, want true after canonicalisation", child, target)
	}
	if !UnderOrEqual(filepath.Join(target, "scope"), link) {
		t.Errorf("UnderOrEqual via target child and link ancestor should hold")
	}
	if !Overlap(link, target) {
		t.Errorf("Overlap(%q, %q) = false, want true", link, target)
	}
	// Distinct trees still reject.
	other := t.TempDir()
	if UnderOrEqual(child, other) {
		t.Errorf("UnderOrEqual must not treat unrelated dirs as nested")
	}
}
