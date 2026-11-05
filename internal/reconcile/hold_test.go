package reconcile

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/p3bot/tk/internal/scopeconfig"
	"github.com/p3bot/tk/internal/token"
)

func TestHoldChangeWithSameSizeAndMtimeIsSeen(t *testing.T) {
	r, _ := newReconciler(t)
	dir := mkScope(t, "wc")
	cue := filepath.Join(dir, "tk.cue")
	stamp := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	write := func(holdType string) {
		t.Helper()
		writeFile(t, cue, "name: \"wc\"\nautoCommit: false\nhold: {type: \""+holdType+"\"}\n")
		if err := os.Chtimes(cue, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}

	write(token.HoldSoft)
	if s, cfgErr := r.SchemaOrError(dir); cfgErr != nil || scopeconfig.HoldLabel(s) != token.HoldSoft {
		t.Fatalf("soft read = %+v %v", s, cfgErr)
	}
	write(token.HoldHard)
	s, cfgErr := r.SchemaOrError(dir)
	if cfgErr != nil {
		t.Fatal(cfgErr)
	}
	if scopeconfig.RefuseHard("wc", s) == nil {
		t.Fatalf("hold = %q, a hard hold must refuse", scopeconfig.HoldLabel(s))
	}
}

func TestHoldInANewSiblingFileIsSeen(t *testing.T) {
	r, _ := newReconciler(t)
	dir := mkScope(t, "wc")
	writeFile(t, filepath.Join(dir, "tk.cue"), "package wccfg\nname: \"wc\"\nautoCommit: false\n")
	if s, cfgErr := r.SchemaOrError(dir); cfgErr != nil || scopeconfig.HoldOf(s) != nil {
		t.Fatalf("first read = %+v %v", s, cfgErr)
	}
	writeFile(t, filepath.Join(dir, "extra.cue"), "package wccfg\nhold: {type: \"hard\", reason: \"R\"}\n")
	s, cfgErr := r.SchemaOrError(dir)
	if cfgErr != nil {
		t.Fatal(cfgErr)
	}
	if !scopeconfig.HoldEqual(s.Hold, &scopeconfig.Hold{Type: token.HoldHard, Reason: "R"}) {
		t.Fatalf("hold = %+v", s.Hold)
	}
}

func TestLegacyBoolHoldReadsAsSoft(t *testing.T) {
	r, _ := newReconciler(t)
	dir := mkScope(t, "wc")
	writeFile(t, filepath.Join(dir, "tk.cue"), "name: \"wc\"\nautoCommit: false\nhold: true\n")
	s, cfgErr := r.SchemaOrError(dir)
	if cfgErr != nil || !scopeconfig.HoldEqual(s.Hold, &scopeconfig.Hold{Type: token.HoldSoft}) {
		t.Fatalf("legacy read = %+v %v", s, cfgErr)
	}
}
