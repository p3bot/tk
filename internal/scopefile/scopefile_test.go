package scopefile

import (
	"path/filepath"
	"testing"
)

func TestLooksLikeTicket(t *testing.T) {
	cases := []struct {
		base string
		want bool
	}{
		{"wc-ab2c-network-redesign.md", true},
		{"wc-ab2c-x.md", true},
		{"wc-ab2c.md", true},
		{"wc-ab2c-.md", false},     // empty slug tail is not a valid slug
		{"wc-ab2c-Bad.md", false},  // slug must be the closed lowercase grammar
		{"wc-abcdefgh-x.md", true}, // 8-char short id (post-repair length)
		{"wc-9b2c-x.md", false},    // short id must be letter-first
		{"wc-ab2c-x.txt", false},
		{"tk.cue", false},
		{"AGENTS.md", false},
		{"random.md", false},
		{"wc.md", false},
	}
	for _, c := range cases {
		if got := LooksLikeTicket(c.base); got != c.want {
			t.Errorf("LooksLikeTicket(%q) = %v want %v", c.base, got, c.want)
		}
	}
}

func TestIsAllowlisted(t *testing.T) {
	dir := filepath.FromSlash("/scope/wc")
	cases := []struct {
		rel  string
		want bool
	}{
		{"wc-ab2c-x.md", true},
		{"tk.cue", true},
		{".gitignore", true},
		{"archive/wc-ab2c-x.md", true},
		{"archive/tk.cue", false},           // only tickets under archive/
		{"archive/sub/wc-ab2c-x.md", false}, // deeper than archive/ is residue
		{"random.txt", false},
		{"AGENTS.md", false},
		{"sub/wc-ab2c-x.md", false}, // no scanned subdirectory other than archive/
		{"notes/default.md", true},
		{"notes/decisions.md", true},
		{"notes/list.md", false},   // reserved verb name
		{"notes/add.md", false},    // reserved verb name
		{"notes/set.md", false},    // reserved verb name
		{"notes/edit.md", false},   // reserved verb name
		{"notes/delete.md", false}, // reserved verb name
		{"notes/help.md", false},   // cobra help command
		{"notes/Not A Slug.md", false},
		{"notes/foo/bar.md", false}, // nested residue
		{"note.md", false},          // root *.md stays residue
		{"archive/note.md", false},
	}
	for _, c := range cases {
		path := filepath.Join(dir, filepath.FromSlash(c.rel))
		if got := IsAllowlisted(path, dir); got != c.want {
			t.Errorf("IsAllowlisted(%q) = %v want %v", c.rel, got, c.want)
		}
	}
	// Dir itself and paths outside dir are never allowlisted.
	if IsAllowlisted(dir, dir) {
		t.Error("the scope dir itself must not be allowlisted")
	}
	if IsAllowlisted(filepath.FromSlash("/other/wc-ab2c-x.md"), dir) {
		t.Error("a path outside the scope dir must not be allowlisted")
	}
}

func TestNoteSlugAndAddressable(t *testing.T) {
	if !IsAddressableNoteSlug("default") || !IsAddressableNoteSlug("decisions") {
		t.Fatal("default and decisions must be addressable")
	}
	for _, name := range []string{"list", "add", "set", "edit", "delete", "help"} {
		if !IsReservedNoteName(name) || IsAddressableNoteSlug(name) {
			t.Errorf("%q must be reserved and not addressable", name)
		}
	}
	if IsAddressableNoteSlug("default.md") || IsAddressableNoteSlug("Not A Slug") {
		t.Error("filenames and invalid slugs must not be addressable")
	}

	dir := filepath.FromSlash("/scope/wc")
	got, ok := NoteSlug(filepath.FromSlash("/scope/wc/notes/decisions.md"), dir)
	if !ok || got != "decisions" {
		t.Errorf("NoteSlug(decisions) = %q %v", got, ok)
	}
	if _, ok := NoteSlug(filepath.FromSlash("/scope/wc/notes/list.md"), dir); ok {
		t.Error("reserved list.md must not yield a slug")
	}
	if _, ok := NoteSlug(filepath.FromSlash("/scope/wc/note.md"), dir); ok {
		t.Error("root note.md must not yield a slug")
	}
	if _, ok := NoteSlug(filepath.FromSlash("/scope/wc/notes/foo/bar.md"), dir); ok {
		t.Error("nested notes path must not yield a slug")
	}
	notesDir := filepath.FromSlash("/tmp/notes")
	if _, ok := NoteSlug(filepath.FromSlash("/tmp/notes/proj-ab2c-alpha.md"), notesDir); ok {
		t.Error("a ticket at the root of a scope dir named notes must not yield a slug")
	}
	got, ok = NoteSlug(filepath.FromSlash("/tmp/notes/notes/decisions.md"), notesDir)
	if !ok || got != "decisions" {
		t.Errorf("NoteSlug under a scope dir named notes = %q %v", got, ok)
	}
	if NoteFile(filepath.FromSlash("/scope/wc"), "default") != filepath.FromSlash("/scope/wc/notes/default.md") {
		t.Errorf("NoteFile default = %q", NoteFile(filepath.FromSlash("/scope/wc"), "default"))
	}
}
