package syncengine

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/rebasedriver"
	"github.com/p3bot/tk/internal/token"
)

type capture struct {
	err []string
}

func (c *capture) Out(string) {}

func (c *capture) Err(line string) { c.err = append(c.err, line) }

func TestAmbiguousBasePausesInProse(t *testing.T) {
	var rep capture
	cont := applyDriverOutcome(&rep, rebasedriver.Outcome{
		Path:          "wc/wc-ab2c-gamma.md",
		Class:         rebasedriver.ClassAmbiguousBase,
		AmbiguousBase: &rebasedriver.AmbiguousBase{ID: "wc-ab2c"},
	}, &syncReport{})
	if cont {
		t.Fatal("ambiguous base must pause the sync")
	}
	if len(rep.err) != 1 {
		t.Fatalf("stderr lines = %v", rep.err)
	}
	got := rep.err[0]
	for _, want := range []string{"wc-ab2c", "wc/wc-ab2c-gamma.md", "tk sync"} {
		if !strings.Contains(got, want) {
			t.Errorf("message %q lacks %q", got, want)
		}
	}
	if strings.Contains(got, token.ConfigUnparseable) || strings.HasPrefix(got, token.SchemaError) {
		t.Errorf("ambiguous base must not use an error token, got %q", got)
	}
}

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
		{"design/wc-ab2c-shape.md", kindDesign},
		{"design/nested/wc-ab2c-shape.md", kindOther},
		{"design/notes.md", kindOther},
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
