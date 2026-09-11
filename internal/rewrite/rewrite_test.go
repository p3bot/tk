package rewrite

import (
	"os"
	"path/filepath"
	"testing"
)

func TestApplyInPlaceRewrite(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.md")
	if err := os.WriteFile(p, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	touched, err := Apply([]Op{{OldPath: p, NewPath: p, Content: []byte("new")}})
	if err != nil {
		t.Fatal(err)
	}
	if len(touched) != 1 || touched[0] != p {
		t.Errorf("in-place touched = %v want [%s]", touched, p)
	}
	if data, _ := os.ReadFile(p); string(data) != "new" {
		t.Errorf("content = %q want new", data)
	}
}

func TestApplyMoveWritesNewRemovesOld(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.md")
	newp := filepath.Join(dir, "new.md")
	if err := os.WriteFile(old, []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}
	touched, err := Apply([]Op{{OldPath: old, NewPath: newp, Content: []byte("body2")}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("old path must be removed after a move")
	}
	if data, _ := os.ReadFile(newp); string(data) != "body2" {
		t.Errorf("new content = %q want body2", data)
	}
	if len(touched) != 2 {
		t.Errorf("move should touch new and old, got %v", touched)
	}
}

func TestApplyRefusesCreateOntoDifferentFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "dest.md")
	if err := os.WriteFile(p, []byte("occupant"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Apply([]Op{{NewPath: p, Content: []byte("newcomer")}})
	if err == nil {
		t.Fatal("create-only write onto an occupied destination must refuse")
	}
	if data, _ := os.ReadFile(p); string(data) != "occupant" {
		t.Errorf("destination must be untouched, got %q", data)
	}
}

func TestApplyCreateOntoSameBytesIsOK(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "dest.md")
	if err := os.WriteFile(p, []byte("same"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply([]Op{{NewPath: p, Content: []byte("same")}}); err != nil {
		t.Fatalf("create-only onto identical bytes: %v", err)
	}
	if data, _ := os.ReadFile(p); string(data) != "same" {
		t.Errorf("content = %q want same", data)
	}
}

// Two tickets can compute the same destination basename; a move onto one would erase it.
func TestApplyRefusesMoveOntoDifferentFile(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.md")
	newp := filepath.Join(dir, "new.md")
	if err := os.WriteFile(old, []byte("mover"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newp, []byte("occupant"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Apply([]Op{{OldPath: old, NewPath: newp, Content: []byte("mover")}})
	if err == nil {
		t.Fatal("move onto an occupied destination must refuse")
	}
	if data, _ := os.ReadFile(newp); string(data) != "occupant" {
		t.Errorf("destination must be untouched, got %q", data)
	}
	if data, _ := os.ReadFile(old); string(data) != "mover" {
		t.Errorf("source must be untouched, got %q", data)
	}
}

// Both-present window of an interrupted move (destination holds exactly the op bytes) completes.
func TestApplyCompletesInterruptedMove(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.md")
	newp := filepath.Join(dir, "new.md")
	for _, p := range []string{old, newp} {
		if err := os.WriteFile(p, []byte("same"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Apply([]Op{{OldPath: old, NewPath: newp, Content: []byte("same")}}); err != nil {
		t.Fatalf("completing an interrupted move: %v", err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("old path must be removed once the move completes")
	}
}

// Re-run after crash (new present, old gone) is a no-op.
func TestApplyIdempotentReentry(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.md")
	newp := filepath.Join(dir, "new.md")
	if err := os.WriteFile(newp, []byte("already"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply([]Op{{OldPath: old, NewPath: newp, Content: []byte("would-overwrite")}}); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(newp); string(data) != "already" {
		t.Errorf("idempotent re-entry must not overwrite, got %q", data)
	}
}
