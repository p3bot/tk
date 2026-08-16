package syncengine

import (
	"path/filepath"
	"testing"
)

func TestHasConflictMarkerStartOnly(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"opener", "<<<<<<< HEAD\nmine\n", true},
		{"full hunk", "<<<<<<< HEAD\nmine\n=======\ntheirs\n>>>>>>> x\n", true},
		{"setext underline", "Decision\n=======\nShip it.\n", false},
		{"closer only", "keep this\n>>>>>>> x\n", false},
		{"empty", "", false},
	}
	for _, c := range cases {
		if got := HasConflictMarker([]byte(c.in)); got != c.want {
			t.Errorf("%s = %v want %v", c.name, got, c.want)
		}
	}
}

func TestClassifyConflictNoteKind(t *testing.T) {
	p := Participant{Name: "wc", Dir: filepath.FromSlash("/repo/wc")}
	cases := []struct {
		rel  string
		want conflictKind
	}{
		{"notes/default.md", kindNote},
		{"notes/decisions.md", kindNote},
		{"notes/list.md", kindOther},
		{"notes/foo/bar.md", kindOther},
		{"note.md", kindOther},
		{"tk.cue", kindSchema},
		{".gitignore", kindIgnore},
		{"wc-ab2c-alpha.md", kindTicket},
		{"archive/wc-ab2c-alpha.md", kindTicket},
	}
	for _, c := range cases {
		got := classifyConflict(filepath.Join(p.Dir, filepath.FromSlash(c.rel)), p)
		if got != c.want {
			t.Errorf("classifyConflict(%q) = %v want %v", c.rel, got, c.want)
		}
	}

	namedNotes := Participant{Name: "proj", Dir: filepath.FromSlash("/tmp/notes")}
	got := classifyConflict(filepath.FromSlash("/tmp/notes/proj-ab2c-alpha.md"), namedNotes)
	if got != kindTicket {
		t.Errorf("ticket in scope dir named notes = %v want %v", got, kindTicket)
	}
	got = classifyConflict(filepath.FromSlash("/tmp/notes/notes/decisions.md"), namedNotes)
	if got != kindNote {
		t.Errorf("note under scope dir named notes = %v want %v", got, kindNote)
	}
}
