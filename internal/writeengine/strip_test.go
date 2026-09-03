package writeengine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/token"
)

func TestStripCustomKeyRootAndArchive(t *testing.T) {
	e := newPlainEnv(t, "wc", "name: \"wc\"\nautoCommit: false\n")
	root := writeTicketFM(t, e.dir, "wc-ab2c-root.md", "jira: ABC-1\narea: frontend\n")
	arch := writeTicketFM(t, filepath.Join(e.dir, "archive"), "wc-de34-old.md", "jira: ABC-2\n")
	clean := writeTicketFM(t, e.dir, "wc-gh56-clean.md", "area: backend\n")
	cleanBefore := readFile(t, clean)
	note := filepath.Join(e.dir, "notes", "default.md")
	writeFile(t, note, "---\njira: NOTE-1\n---\n# Note\n")
	nested := filepath.Join(e.dir, "nested", "wc-jk89-x.md")
	writeFile(t, nested, "---\nid: wc-jk89\nstatus: todo\norder: \"a0\"\ncreated: 2026-01-01T00:00:00Z\njira: NEST-1\n---\n# Nested\n")

	rewritten, skips, err := StripCustomKey(e.dir, "jira")
	if err != nil {
		t.Fatalf("StripCustomKey: %v", err)
	}
	if len(skips) != 0 {
		t.Fatalf("skips = %+v", skips)
	}
	if len(rewritten) != 2 {
		t.Fatalf("rewritten = %v, want root+archive", rewritten)
	}
	rootData := readFile(t, root)
	if strings.Contains(rootData, "jira:") {
		t.Errorf("root still has jira:\n%s", rootData)
	}
	if !strings.Contains(rootData, "area:") {
		t.Errorf("root dropped unrelated custom key:\n%s", rootData)
	}
	if strings.Contains(readFile(t, arch), "jira:") {
		t.Error("archive still has jira")
	}
	if readFile(t, clean) != cleanBefore {
		t.Error("ticket without the key must not be rewritten")
	}
	if !strings.Contains(readFile(t, note), "jira: NOTE-1") {
		t.Error("notes must not be stripped")
	}
	if !strings.Contains(readFile(t, nested), "jira: NEST-1") {
		t.Error("nested residue must not be stripped")
	}

	rewritten, skips, err = StripCustomKey(e.dir, "jira")
	if err != nil {
		t.Fatal(err)
	}
	if len(rewritten) != 0 || len(skips) != 0 {
		t.Errorf("idempotent strip rewritten=%v skips=%+v", rewritten, skips)
	}
}

func TestStripCustomKeyParseErrorSkip(t *testing.T) {
	e := newPlainEnv(t, "wc", "name: \"wc\"\nautoCommit: false\n")
	good := writeTicketFM(t, e.dir, "wc-ab2c-good.md", "jira: ABC-1\n")
	bad := filepath.Join(e.dir, "wc-de34-broke.md")
	writeFile(t, bad, "---\nid: wc-de34\nstatus: [unterminated\n---\n# broke\n")
	before := readFile(t, bad)

	rewritten, skips, err := StripCustomKey(e.dir, "jira")
	if err != nil {
		t.Fatalf("StripCustomKey: %v", err)
	}
	if len(rewritten) != 1 || filepath.Base(rewritten[0]) != "wc-ab2c-good.md" {
		t.Fatalf("rewritten = %v", rewritten)
	}
	if len(skips) != 1 || skips[0].ID != "wc-de34" {
		t.Fatalf("skips = %+v", skips)
	}
	line := skips[0].TokenLine()
	if !strings.HasPrefix(line, token.ParseError) || !strings.Contains(line, "wc-de34") {
		t.Errorf("skip line = %q", line)
	}
	if !strings.Contains(line, "skipped") {
		t.Errorf("skip line must say skipped, got %q", line)
	}
	if readFile(t, bad) != before {
		t.Error("parse_error ticket must be left untouched")
	}
	if strings.Contains(readFile(t, good), "jira:") {
		t.Error("good ticket should be stripped")
	}
}

func writeTicketFM(t *testing.T, dir, base, extra string) string {
	t.Helper()
	parts := strings.SplitN(strings.TrimSuffix(base, ".md"), "-", 3)
	id := parts[0] + "-" + parts[1]
	path := filepath.Join(dir, base)
	writeFile(t, path, "---\nid: "+id+"\nstatus: todo\norder: \"a0\"\ncreated: 2026-01-01T00:00:00Z\n"+extra+"---\n# Work\n")
	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
