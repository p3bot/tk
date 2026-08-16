package syncengine

import (
	"path/filepath"
	"testing"
)

func TestSnapshotMessageNoteIsNotTicketShaped(t *testing.T) {
	dir := filepath.FromSlash("/repo/wc")
	path := filepath.FromSlash("/repo/wc/notes/decisions.md")
	got := snapshotMessage([]dirtyPath{{path: path, code: "??", scope: "wc", dir: dir}})
	if got != "tk: note wc decisions" {
		t.Errorf("add note = %q", got)
	}
	got = snapshotMessage([]dirtyPath{{path: path, code: " M", scope: "wc", dir: dir}})
	if got != "tk: note wc decisions" {
		t.Errorf("edit note = %q", got)
	}
	got = snapshotMessage([]dirtyPath{{path: path, code: "D ", scope: "wc", dir: dir}})
	if got != "tk: note wc decisions" {
		t.Errorf("delete note = %q", got)
	}

	ticket := filepath.FromSlash("/repo/wc/wc-ab2c-alpha.md")
	got = snapshotMessage([]dirtyPath{{path: ticket, code: "??", scope: "wc", dir: dir}})
	if got != "tk: add wc-ab2c alpha" {
		t.Errorf("ticket add = %q", got)
	}
}

func TestSnapshotMessageTicketInScopeDirNamedNotes(t *testing.T) {
	dir := filepath.FromSlash("/tmp/notes")
	ticket := filepath.FromSlash("/tmp/notes/proj-ab2c-alpha.md")
	got := snapshotMessage([]dirtyPath{{path: ticket, code: "??", scope: "proj", dir: dir}})
	if got != "tk: add proj-ab2c alpha" {
		t.Errorf("ticket in scope dir named notes = %q", got)
	}
	note := filepath.FromSlash("/tmp/notes/notes/decisions.md")
	got = snapshotMessage([]dirtyPath{{path: note, code: "??", scope: "proj", dir: dir}})
	if got != "tk: note proj decisions" {
		t.Errorf("real note under scope dir named notes = %q", got)
	}
}

func TestSnapshotMessageMultiPathUnchanged(t *testing.T) {
	got := snapshotMessage([]dirtyPath{
		{path: filepath.FromSlash("/repo/wc/notes/decisions.md"), scope: "wc"},
		{path: filepath.FromSlash("/repo/wc/tk.cue"), scope: "wc"},
	})
	if got != "tk: sync 2 path(s)" {
		t.Errorf("multi-path = %q", got)
	}
}
