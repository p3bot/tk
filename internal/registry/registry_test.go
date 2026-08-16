package registry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cuelang.org/go/cue/cuecontext"

	"github.com/p3bot/tk/internal/pathutil"
)

func TestLoadEmpty(t *testing.T) {
	s := NewStore(cuecontext.New(), t.TempDir())
	reg, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(reg.Scopes) != 0 || len(reg.Lens) != 0 || len(reg.Me) != 0 || len(reg.Note) != 0 {
		t.Errorf("expected empty registry, got %+v", reg)
	}
}

func TestWriteAndReload(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(cuecontext.New(), dir)

	scopes := map[string]Entry{
		"wc": {Dir: "/home/g/webctl/.agents/tk", Root: "/home/g/webctl"},
		"ta": {Dir: "/org/mono/teamA/.agents/tk", Root: "/org/mono/teamA"},
	}
	if err := s.WriteRegistry(scopes); err != nil {
		t.Fatalf("WriteRegistry: %v", err)
	}
	if err := s.WriteLens(map[string][]string{"wc": {"frontend", "style"}}); err != nil {
		t.Fatalf("WriteLens: %v", err)
	}
	if err := s.WriteMe(map[string]string{"wc": "wc-ab2c"}); err != nil {
		t.Fatalf("WriteMe: %v", err)
	}
	if err := s.WriteNote(map[string]string{"wc": "grant"}); err != nil {
		t.Fatalf("WriteNote: %v", err)
	}

	reg, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if reg.Scopes["wc"].Root != pathutil.Canonical("/home/g/webctl") {
		t.Errorf("wc root = %q", reg.Scopes["wc"].Root)
	}
	if reg.Scopes["ta"].Dir != pathutil.Canonical("/org/mono/teamA/.agents/tk") {
		t.Errorf("ta dir = %q", reg.Scopes["ta"].Dir)
	}
	if got := reg.Lens["wc"]; len(got) != 2 || got[0] != "frontend" {
		t.Errorf("lens = %v", got)
	}
	if got := reg.Me["wc"]; got != "wc-ab2c" {
		t.Errorf("me = %q", got)
	}
	if got := reg.Note["wc"]; got != "grant" {
		t.Errorf("note = %q", got)
	}
}

func TestLoadCanonicalisesSymlinkSpellings(t *testing.T) {
	real := t.TempDir()
	linkParent := t.TempDir()
	link := filepath.Join(linkParent, "scope-link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	dir := t.TempDir()
	s := NewStore(cuecontext.New(), dir)
	if err := s.WriteRegistry(map[string]Entry{
		"wc": {Dir: link, Root: link},
	}); err != nil {
		t.Fatal(err)
	}
	reg, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	want := pathutil.Canonical(real)
	if reg.Scopes["wc"].Dir != want || reg.Scopes["wc"].Root != want {
		t.Errorf("Load should resolve symlink spellings, got %+v want dir/root %q", reg.Scopes["wc"], want)
	}
}

func TestWriteEmptyRegistry(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(cuecontext.New(), dir)
	if err := s.WriteRegistry(map[string]Entry{}); err != nil {
		t.Fatalf("WriteRegistry: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, registryFile))
	if err != nil {
		t.Fatal(err)
	}
	// Must still be valid CUE that reloads to an empty set.
	reg, err := s.Load()
	if err != nil {
		t.Fatalf("reload empty: %v", err)
	}
	if len(reg.Scopes) != 0 {
		t.Errorf("expected empty scopes, file was:\n%s", data)
	}
}

func TestLoadMalformedIsHardError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, registryFile), []byte("scopes: {{{ broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewStore(cuecontext.New(), dir)
	if _, err := s.Load(); err == nil {
		t.Fatal("expected a hard error naming the file")
	}
}

func TestLoadMalformedMeIsHardError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, meFile), []byte("me: {{{ broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewStore(cuecontext.New(), dir)
	_, err := s.Load()
	if err == nil {
		t.Fatal("expected a hard error naming me.cue")
	}
	if !strings.Contains(err.Error(), meFile) {
		t.Errorf("error should name %s, got %v", meFile, err)
	}
}

func TestLoadMalformedNoteIsHardError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, noteFile), []byte("note: {{{ broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewStore(cuecontext.New(), dir)
	_, err := s.Load()
	if err == nil {
		t.Fatal("expected a hard error naming note.cue")
	}
	if !strings.Contains(err.Error(), noteFile) {
		t.Errorf("error should name %s, got %v", noteFile, err)
	}
}
