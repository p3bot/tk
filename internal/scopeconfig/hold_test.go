package scopeconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cuelang.org/go/cue/cuecontext"
	"github.com/p3bot/tk/internal/status"
)

func TestLoadHoldOmittedIsFalse(t *testing.T) {
	ctx := cuecontext.New()
	dir := writeCfg(t, "name: \"wc\"\nautoCommit: false\n")
	s, err := Load(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.Hold {
		t.Fatal("omitted hold must be false")
	}
}

func TestLoadHoldExplicit(t *testing.T) {
	ctx := cuecontext.New()
	dir := writeCfg(t, "name: \"wc\"\nautoCommit: false\nhold: true\n")
	s, err := Load(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Hold {
		t.Fatal("hold true must load")
	}
	dir = writeCfg(t, "name: \"wc\"\nautoCommit: true\nhold: false\n")
	s, err = Load(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.Hold {
		t.Fatal("hold false must load as false")
	}
}

func TestLoadHoldWrongType(t *testing.T) {
	ctx := cuecontext.New()
	dir := writeCfg(t, "name: \"wc\"\nautoCommit: false\nhold: \"yes\"\n")
	if _, err := Load(ctx, dir); err == nil {
		t.Fatal("non-bool hold must be unparseable")
	} else if _, ok := AsConfigError(err); !ok {
		t.Fatalf("want ConfigError, got %v", err)
	}
}

func TestRewriteHoldInsertsAfterAutoCommit(t *testing.T) {
	dir := writeCfg(t, "name: \"wc\"\nautoCommit: false\nstatuses: {shipped: {category: \"done\"}}\n")
	if err := RewriteHold(dir, true); err != nil {
		t.Fatal(err)
	}
	s, err := Load(cuecontext.New(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Hold || s.AutoCommit || s.Name != "wc" {
		t.Fatalf("schema after insert = %+v", s)
	}
	if s.Statuses["shipped"] != status.CategoryDone {
		t.Fatal("rewrite must keep statuses")
	}
	raw := readCue(t, dir)
	nameAt := strings.Index(raw, "name:")
	autoAt := strings.Index(raw, "autoCommit:")
	holdAt := strings.Index(raw, "hold:")
	if nameAt < 0 || autoAt < nameAt || holdAt < autoAt {
		t.Fatalf("hold must follow autoCommit:\n%s", raw)
	}
	if err := RewriteHold(dir, false); err != nil {
		t.Fatal(err)
	}
	s, err = Load(cuecontext.New(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.Hold {
		t.Fatal("rewrite false must clear hold")
	}
}

func readCue(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "tk.cue"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
