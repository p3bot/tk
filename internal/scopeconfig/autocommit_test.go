package scopeconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cuelang.org/go/cue/cuecontext"
)

func TestRewriteAutoCommitPreservesSiblingsAndComments(t *testing.T) {
	dir := writeCfg(t, `name: "wc"
autoCommit: false // keep
fields: {
	jira: {type: "string"}
}
`)
	if err := RewriteAutoCommit(dir, true); err != nil {
		t.Fatalf("RewriteAutoCommit: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "tk.cue"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, "autoCommit: true") {
		t.Errorf("want autoCommit true, got:\n%s", got)
	}
	if strings.Contains(got, "autoCommit: false") {
		t.Errorf("old false must be gone, got:\n%s", got)
	}
	if !strings.Contains(got, `"wc"`) || !strings.Contains(got, "jira") {
		t.Errorf("must preserve other fields, got:\n%s", got)
	}
	if !strings.Contains(got, "keep") {
		t.Errorf("must preserve comment, got:\n%s", got)
	}
	s, err := Load(cuecontext.New(), dir)
	if err != nil {
		t.Fatalf("Load after rewrite: %v", err)
	}
	if !s.AutoCommit {
		t.Errorf("evaluated autoCommit = false, want true")
	}
}

func TestRewriteAutoCommitMissingField(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tk.cue"), []byte("name: \"wc\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RewriteAutoCommit(dir, true); err == nil {
		t.Fatal("missing autoCommit field must refuse")
	}
}

func TestRewriteAutoCommitRoundTrip(t *testing.T) {
	dir := writeCfg(t, "name: \"wc\"\nautoCommit: true\n")
	if err := RewriteAutoCommit(dir, false); err != nil {
		t.Fatal(err)
	}
	s, err := Load(cuecontext.New(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.AutoCommit {
		t.Fatal("want false after rewrite")
	}
	if err := RewriteAutoCommit(dir, true); err != nil {
		t.Fatal(err)
	}
	s, err = Load(cuecontext.New(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if !s.AutoCommit {
		t.Fatal("want true after second rewrite")
	}
}
