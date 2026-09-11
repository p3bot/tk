package writeengine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cuelang.org/go/cue/cuecontext"

	"github.com/p3bot/tk/internal/frontmatter"
	"github.com/p3bot/tk/internal/gitstate"
	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/reconcile"
	"github.com/p3bot/tk/internal/registry"
	"github.com/p3bot/tk/internal/testgit"
	"github.com/p3bot/tk/internal/token"
)

type dualEnv struct {
	deps    Deps
	srcDir  string
	destDir string
}

func newDualPlain(t *testing.T, srcName, destName, srcCue, destCue string) *dualEnv {
	t.Helper()
	base := t.TempDir()
	srcDir := filepath.Join(base, srcName)
	destDir := filepath.Join(base, destName)
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if srcCue == "" {
		srcCue = "name: \"" + srcName + "\"\nautoCommit: false\n"
	}
	if destCue == "" {
		destCue = "name: \"" + destName + "\"\nautoCommit: false\n"
	}
	writeFile(t, filepath.Join(srcDir, "tk.cue"), srcCue)
	writeFile(t, filepath.Join(destDir, "tk.cue"), destCue)
	return openDual(t, srcName, srcDir, destName, destDir)
}

func openDual(t *testing.T, srcName, srcDir, destName, destDir string) *dualEnv {
	t.Helper()
	cue := cuecontext.New()
	db, err := index.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &dualEnv{
		deps: Deps{
			Ctx:       context.Background(),
			Cue:       cue,
			StateDir:  t.TempDir(),
			ConfigDir: t.TempDir(),
			Reg: &registry.Registry{
				Scopes: map[string]registry.Entry{
					srcName:  {Dir: srcDir, Root: srcDir},
					destName: {Dir: destDir, Root: destDir},
				},
				Lens: map[string][]string{},
				Me:   map[string]string{},
				Note: map[string]string{},
			},
			DB:  db,
			Rec: reconcile.New(db, cue),
		},
		srcDir:  srcDir,
		destDir: destDir,
	}
}

func writeRehomeTicket(t *testing.T, dir, id, status, orderKey, extra, body string) string {
	t.Helper()
	path := filepath.Join(dir, id+"-work.md")
	if body == "" {
		body = "# Work\n"
	}
	writeFile(t, path, "---\nid: "+id+"\nstatus: "+status+"\norder: \""+orderKey+"\"\ncreated: 2026-01-01T00:00:00Z\n"+extra+"---\n"+body)
	return path
}

func rehomeFoo(d *dualEnv, id string) (Result, error) {
	return Rehome(d.deps, RehomeInput{
		SourceScope: "foo",
		SourceDir:   d.srcDir,
		DestScope:   "bar",
		DestDir:     d.destDir,
		Lookup:      fullLookup(id),
	})
}

func TestRehomeKeepsShortIDAndRewritesNeighbourhood(t *testing.T) {
	d := newDualPlain(t, "foo", "bar", "", "")
	writeRehomeTicket(t, d.srcDir, "foo-kv6x", "todo", "a0", "", "# Dep target\n")
	src := writeRehomeTicket(t, d.srcDir, "foo-ab2c", "todo", "a1", "depends: [foo-kv6x]\ncustom_jira: X-1\n", "# Moved\n")
	writeRehomeTicket(t, d.srcDir, "foo-de34", "todo", "a2", "depends: [foo-ab2c]\n", "# Referrer\n")
	writeRehomeTicket(t, d.srcDir, "foo-gh56", "todo", "a3", "related: [foo-ab2c]\n", "# Related referrer\n")
	writeRehomeTicket(t, d.destDir, "bar-mm22", "todo", "a0", "depends: [foo-ab2c]\n", "# Dest referrer\n")
	writeRehomeTicket(t, d.destDir, "bar-np23", "todo", "a1", "related: [foo-ab2c]\n", "# Dest related\n")

	res, err := rehomeFoo(d, "foo-ab2c")
	if err != nil {
		t.Fatalf("rehome: %v", err)
	}
	if !strings.HasSuffix(res.Path, "bar-ab2c-work.md") {
		t.Errorf("dest path = %q", res.Path)
	}
	if res.ID != "bar-ab2c" {
		t.Errorf("id = %q", res.ID)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("source file must be gone")
	}
	data, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	if !strings.Contains(body, "id: bar-ab2c") {
		t.Errorf("dest id missing: %s", body)
	}
	if !strings.Contains(body, "foo-kv6x") {
		t.Errorf("sibling dep must stay dest-unprefixed: %s", body)
	}
	if strings.Contains(body, "bar-kv6x") {
		t.Errorf("must not rekey other edges: %s", body)
	}
	if !strings.Contains(body, "custom_jira: X-1") {
		t.Errorf("undeclared key must be retained: %s", body)
	}

	referrer, _ := os.ReadFile(filepath.Join(d.srcDir, "foo-de34-work.md"))
	if !strings.Contains(string(referrer), "bar-ab2c") || strings.Contains(string(referrer), "foo-ab2c") {
		t.Errorf("source inbound not rewritten: %s", referrer)
	}
	destRef, _ := os.ReadFile(filepath.Join(d.destDir, "bar-mm22-work.md"))
	if !strings.Contains(string(destRef), "bar-ab2c") || strings.Contains(string(destRef), "foo-ab2c") {
		t.Errorf("dest inbound not rewritten: %s", destRef)
	}
	related, _ := os.ReadFile(filepath.Join(d.srcDir, "foo-gh56-work.md"))
	if !strings.Contains(string(related), "bar-ab2c") || strings.Contains(string(related), "foo-ab2c") {
		t.Errorf("source related inbound not rewritten: %s", related)
	}
	destRelated, _ := os.ReadFile(filepath.Join(d.destDir, "bar-np23-work.md"))
	if !strings.Contains(string(destRelated), "bar-ab2c") || strings.Contains(string(destRelated), "foo-ab2c") {
		t.Errorf("dest related inbound not rewritten: %s", destRelated)
	}
}

func TestRehomeRefusesForeignFrontmatterID(t *testing.T) {
	d := newDualPlain(t, "foo", "bar", "", "")
	src := writeRehomeTicket(t, d.srcDir, "foo-ab2c", "todo", "a0", "", "")
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	patched := strings.Replace(string(raw), "id: foo-ab2c", "id: other-kv6x", 1)
	if err := os.WriteFile(src, []byte(patched), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = rehomeFoo(d, "foo-ab2c")
	if err == nil {
		t.Fatal("foreign frontmatter id must refuse")
	}
	if !strings.Contains(err.Error(), "other-kv6x") || !strings.Contains(err.Error(), "foo") {
		t.Errorf("refuse must name the offending id and source scope, got %v", err)
	}
	if _, statErr := os.Stat(src); statErr != nil {
		t.Errorf("source must remain, %v", statErr)
	}
}

func TestRehomeSameScopeAndUnknownDest(t *testing.T) {
	d := newDualPlain(t, "foo", "bar", "", "")
	writeRehomeTicket(t, d.srcDir, "foo-ab2c", "todo", "a0", "", "")

	_, err := Rehome(d.deps, RehomeInput{
		SourceScope: "foo", SourceDir: d.srcDir, DestScope: "foo", DestDir: d.srcDir,
		Lookup: fullLookup("foo-ab2c"),
	})
	var use *UsageError
	if !errors.As(err, &use) {
		t.Fatalf("same-scope: want usage, got %v", err)
	}

	_, err = Rehome(d.deps, RehomeInput{
		SourceScope: "foo", SourceDir: d.srcDir, DestScope: "ghost",
		Lookup: fullLookup("foo-ab2c"),
	})
	if err == nil || !strings.Contains(err.Error(), "unknown scope") {
		t.Fatalf("unknown dest: got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(d.srcDir, "foo-ab2c-work.md")); statErr != nil {
		t.Errorf("refused rehome must leave source, %v", statErr)
	}
}

func TestRehomeUnknownDestStatus(t *testing.T) {
	d := newDualPlain(t, "foo", "bar",
		"name: \"foo\"\nautoCommit: false\nstatuses: { shipped: {category: \"done\"} }\n",
		"")
	src := writeRehomeTicket(t, d.srcDir, "foo-ab2c", "shipped", "a0", "", "")
	_, err := rehomeFoo(d, "foo-ab2c")
	var unk *UnknownStatusError
	if !errors.As(err, &unk) {
		t.Fatalf("want unknown dest status, got %v", err)
	}
	if unk.Status != "shipped" || unk.Scope != "bar" {
		t.Errorf("error = %+v", unk)
	}
	if _, statErr := os.Stat(src); statErr != nil {
		t.Errorf("source must remain, %v", statErr)
	}
}

func TestRehomeUnparseableInboundRefuses(t *testing.T) {
	d := newDualPlain(t, "foo", "bar", "", "")
	src := writeRehomeTicket(t, d.srcDir, "foo-ab2c", "todo", "a0", "", "")
	writeFile(t, filepath.Join(d.srcDir, "foo-de34-broke.md"), "---\nid: foo-de34\nstatus: [x\ndepends: [foo-ab2c]\n---\n# broke\n")

	_, err := rehomeFoo(d, "foo-ab2c")
	if err == nil || !strings.Contains(err.Error(), token.ParseError) {
		t.Fatalf("want parse_error refuse, got %v", err)
	}
	if !strings.Contains(err.Error(), "foo-ab2c") {
		t.Errorf("refuse must name the old id, got %v", err)
	}
	if _, statErr := os.Stat(src); statErr != nil {
		t.Errorf("source must remain, %v", statErr)
	}
	entries, _ := os.ReadDir(d.destDir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "bar-ab2c") {
			t.Errorf("dest must not have been written, files=%v", entries)
		}
	}
}

func TestRehomeUnparseableDestInboundRefuses(t *testing.T) {
	d := newDualPlain(t, "foo", "bar", "", "")
	src := writeRehomeTicket(t, d.srcDir, "foo-ab2c", "todo", "a0", "", "")
	writeFile(t, filepath.Join(d.destDir, "bar-de34-broke.md"), "---\nid: bar-de34\nstatus: [x\ndepends: [foo-ab2c]\n---\n# broke\n")

	_, err := rehomeFoo(d, "foo-ab2c")
	if err == nil || !strings.Contains(err.Error(), token.ParseError) {
		t.Fatalf("want parse_error refuse, got %v", err)
	}
	if !strings.Contains(err.Error(), "foo-ab2c") {
		t.Errorf("refuse must name the old id, got %v", err)
	}
	if _, statErr := os.Stat(src); statErr != nil {
		t.Errorf("source must remain, %v", statErr)
	}
}

func TestRehomeUnparseableUnrelatedSiblingAllowed(t *testing.T) {
	d := newDualPlain(t, "foo", "bar", "", "")
	writeRehomeTicket(t, d.srcDir, "foo-ab2c", "todo", "a0", "", "")
	writeFile(t, filepath.Join(d.srcDir, "foo-de34-broke.md"), "---\nid: foo-de34\nstatus: [x\n---\n# broke\n")

	res, err := rehomeFoo(d, "foo-ab2c")
	if err != nil {
		t.Fatalf("unrelated unparseable sibling must not refuse: %v", err)
	}
	if res.ID != "bar-ab2c" {
		t.Errorf("id = %q", res.ID)
	}
}

func TestRehomeDestFilenameIDMismatchExtends(t *testing.T) {
	d := newDualPlain(t, "foo", "bar", "", "")
	src := writeRehomeTicket(t, d.srcDir, "foo-ab2c", "todo", "a0", "", "# Moved\n")
	writeFile(t, filepath.Join(d.destDir, "bar-ab2c-work.md"),
		"---\nid: bar-xyzw\nstatus: todo\norder: \"a0\"\ncreated: 2026-02-01T00:00:00Z\n---\n# Occupant\n")

	res, err := rehomeFoo(d, "foo-ab2c")
	if err != nil {
		t.Fatalf("rehome: %v", err)
	}
	if res.ID != "bar-ab2ca" {
		t.Errorf("id = %q want bar-ab2ca (filename short occupied)", res.ID)
	}
	if !strings.HasSuffix(res.Path, "bar-ab2ca-work.md") {
		t.Errorf("path = %q", res.Path)
	}
	occ, err := os.ReadFile(filepath.Join(d.destDir, "bar-ab2c-work.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(occ), "id: bar-xyzw") || strings.Contains(string(occ), "id: bar-ab2c") {
		t.Errorf("occupant must remain, got %s", occ)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("source must be gone")
	}
}

func TestRehomeExtendsCollidingShort(t *testing.T) {
	d := newDualPlain(t, "foo", "bar", "", "")
	writeRehomeTicket(t, d.srcDir, "foo-ab2c", "todo", "a0", "", "")
	writeFile(t, filepath.Join(d.destDir, "bar-ab2c-work.md"),
		"---\nid: bar-ab2c\nstatus: todo\norder: \"a0\"\ncreated: 2026-02-01T00:00:00Z\n---\n# Occupant\n")

	res, err := rehomeFoo(d, "foo-ab2c")
	if err != nil {
		t.Fatalf("rehome: %v", err)
	}
	if res.ID != "bar-ab2ca" {
		t.Errorf("extended id = %q want bar-ab2ca", res.ID)
	}
	if !strings.HasSuffix(res.Path, "bar-ab2ca-work.md") {
		t.Errorf("path = %q", res.Path)
	}
	if _, err := os.Stat(filepath.Join(d.destDir, "bar-ab2c-work.md")); err != nil {
		t.Errorf("occupant must remain: %v", err)
	}
}

func TestRehomeLeftoverReusesDestIDAndOrder(t *testing.T) {
	d := newDualPlain(t, "foo", "bar", "", "")
	writeRehomeTicket(t, d.srcDir, "foo-kv6x", "todo", "a0", "", "")
	src := writeRehomeTicket(t, d.srcDir, "foo-ab2c", "todo", "a1", "depends: [foo-kv6x]\n", "# Moved\n")
	writeRehomeTicket(t, d.destDir, "bar-mm22", "todo", "a0", "", "# Anchor\n")

	backup, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	first, err := rehomeFoo(d, "foo-ab2c")
	if err != nil {
		t.Fatalf("first rehome: %v", err)
	}
	if first.ID != "bar-ab2c" {
		t.Fatalf("first id = %q", first.ID)
	}
	destBytes, err := os.ReadFile(first.Path)
	if err != nil {
		t.Fatal(err)
	}
	interior, _, _ := frontmatter.Split(destBytes)
	m, err := frontmatter.Parse(interior)
	if err != nil {
		t.Fatal(err)
	}
	keptOrder := m.Order
	if keptOrder == "" {
		t.Fatal("dest order missing")
	}

	if err := os.WriteFile(src, backup, 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := rehomeFoo(d, "foo-ab2c")
	if err != nil {
		t.Fatalf("resume rehome: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("resume id = %q want %q", second.ID, first.ID)
	}
	if second.Path != first.Path {
		t.Errorf("resume path = %q want %q", second.Path, first.Path)
	}
	again, err := os.ReadFile(second.Path)
	if err != nil {
		t.Fatal(err)
	}
	interior2, _, _ := frontmatter.Split(again)
	m2, err := frontmatter.Parse(interior2)
	if err != nil {
		t.Fatal(err)
	}
	if m2.Order != keptOrder {
		t.Errorf("resume order = %q want %q (no second append)", m2.Order, keptOrder)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("source must be gone after resume")
	}

	_, err = rehomeFoo(d, "foo-ab2c")
	var unk *UnknownTicketError
	if !errors.As(err, &unk) {
		t.Fatalf("completed rehome must be unknown id, got %v", err)
	}
}

func TestRehomeLeftoverAfterSourceEditReusesDest(t *testing.T) {
	d := newDualPlain(t, "foo", "bar", "", "")
	src := writeRehomeTicket(t, d.srcDir, "foo-ab2c", "todo", "a0", "", "# Moved\n")
	backup, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}

	first, err := rehomeFoo(d, "foo-ab2c")
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if first.ID != "bar-ab2c" {
		t.Fatalf("first id = %q", first.ID)
	}

	edited := strings.Replace(string(backup), "# Moved\n", "# Moved and edited\n", 1)
	if err := os.WriteFile(src, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := rehomeFoo(d, "foo-ab2c")
	if err != nil {
		t.Fatalf("resume after source edit: %v", err)
	}
	if second.ID != first.ID || second.Path != first.Path {
		t.Errorf("resume id/path = %q %q want %q %q", second.ID, second.Path, first.ID, first.Path)
	}
	got, err := os.ReadFile(second.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "# Moved and edited") {
		t.Errorf("dest must take the edited source body, got %s", got)
	}
	entries, err := os.ReadDir(d.destDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "bar-ab2ca") {
			t.Errorf("source edit must not mint a second dest id, files=%v", namesOf(entries))
		}
	}
}

func namesOf(entries []os.DirEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Name()
	}
	return out
}

func TestRehomeLeftoverExtendedNoSecondExtend(t *testing.T) {
	d := newDualPlain(t, "foo", "bar", "", "")
	src := writeRehomeTicket(t, d.srcDir, "foo-ab2c", "todo", "a0", "", "")
	writeFile(t, filepath.Join(d.destDir, "bar-ab2c-work.md"),
		"---\nid: bar-ab2c\nstatus: todo\norder: \"a0\"\ncreated: 2026-02-01T00:00:00Z\n---\n# Occupant\n")
	backup, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}

	first, err := rehomeFoo(d, "foo-ab2c")
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if first.ID != "bar-ab2ca" {
		t.Fatalf("first id = %q", first.ID)
	}
	if err := os.WriteFile(src, backup, 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := rehomeFoo(d, "foo-ab2c")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if second.ID != "bar-ab2ca" {
		t.Errorf("second extend must not run, id = %q", second.ID)
	}
}

func TestRehomeTerminalGoesToDestArchive(t *testing.T) {
	d := newDualPlain(t, "foo", "bar", "", "")
	writeRehomeTicket(t, d.srcDir, "foo-ab2c", "done", "a0", "", "")
	res, err := rehomeFoo(d, "foo-ab2c")
	if err != nil {
		t.Fatalf("rehome: %v", err)
	}
	if filepath.Base(filepath.Dir(res.Path)) != "archive" {
		t.Errorf("terminal dest path = %q, want archive/", res.Path)
	}
}

func TestRehomeParseErrorSourceRefuses(t *testing.T) {
	d := newDualPlain(t, "foo", "bar", "", "")
	writeFile(t, filepath.Join(d.srcDir, "foo-ab2c-broke.md"), "---\nid: foo-ab2c\nstatus: [x\n---\n# broke\n")
	_, err := rehomeFoo(d, "foo-ab2c")
	var q *ParseQuarantineError
	if !errors.As(err, &q) {
		t.Fatalf("want parse quarantine, got %v", err)
	}
}

func TestRehomeMeDropFailureStillReturnsDest(t *testing.T) {
	d := newDualPlain(t, "foo", "bar", "", "")
	src := writeRehomeTicket(t, d.srcDir, "foo-ab2c", "todo", "a0", "", "")
	cfg := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(cfg, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	d.deps.ConfigDir = cfg

	res, err := rehomeFoo(d, "foo-ab2c")
	if err == nil {
		t.Fatal("want config-lock failure after the files moved")
	}
	if res.ID != "bar-ab2c" {
		t.Errorf("id = %q want bar-ab2c", res.ID)
	}
	if !strings.HasSuffix(res.Path, "bar-ab2c-work.md") {
		t.Errorf("path = %q", res.Path)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("source must be gone")
	}
	if _, err := os.Stat(res.Path); err != nil {
		t.Errorf("dest must exist: %v", err)
	}
	srcRows, err := d.deps.DB.TicketsByID("foo", "foo-ab2c")
	if err != nil {
		t.Fatal(err)
	}
	if len(srcRows) != 0 {
		t.Errorf("index must drop source after files moved, got %d rows", len(srcRows))
	}
	destRows, err := d.deps.DB.TicketsByID("bar", "bar-ab2c")
	if err != nil {
		t.Fatal(err)
	}
	if len(destRows) != 1 {
		t.Errorf("index must have dest after files moved, got %d rows", len(destRows))
	}
}

func TestRehomeDropsSourceMe(t *testing.T) {
	d := newDualPlain(t, "foo", "bar", "", "")
	writeRehomeTicket(t, d.srcDir, "foo-ab2c", "todo", "a0", "", "")
	store := registry.NewStore(d.deps.Cue, d.deps.ConfigDir)
	if err := store.WriteMe(map[string]string{"foo": "foo-ab2c", "bar": "bar-mm22"}); err != nil {
		t.Fatal(err)
	}
	if _, err := rehomeFoo(d, "foo-ab2c"); err != nil {
		t.Fatalf("rehome: %v", err)
	}
	reg, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := reg.Me["foo"]; got != "" {
		t.Errorf("source me must be dropped, got %q", got)
	}
	if got := reg.Me["bar"]; got != "bar-mm22" {
		t.Errorf("dest me must be left alone, got %q", got)
	}
}

func TestRehomeMidRebaseRefuses(t *testing.T) {
	requireGit(t)
	src, srcRepo := initAutoCommitRepo(t, "foo")
	destDir := filepath.Join(t.TempDir(), "bar")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(destDir, "tk.cue"), "name: \"bar\"\nautoCommit: true\n")
	src.deps.Reg.Scopes["bar"] = registry.Entry{Dir: destDir, Root: destDir}
	path := writeRehomeTicket(t, src.dir, "foo-ab2c", "todo", "a0", "", "")
	if err := os.MkdirAll(filepath.Join(srcRepo, ".git", "rebase-merge"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := Rehome(src.deps, RehomeInput{
		SourceScope: "foo", SourceDir: src.dir, DestScope: "bar", DestDir: destDir,
		Lookup: fullLookup("foo-ab2c"),
	})
	var mid *gitstate.MidRebaseError
	if !errors.As(err, &mid) {
		t.Fatalf("want mid-rebase, got %v", err)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Errorf("source must remain, %v", statErr)
	}
}

func TestRehomeDestMidRebaseRefuses(t *testing.T) {
	requireGit(t)
	dest, destRepo := initAutoCommitRepo(t, "bar")
	srcDir := filepath.Join(t.TempDir(), "foo")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(srcDir, "tk.cue"), "name: \"foo\"\nautoCommit: false\n")
	dest.deps.Reg.Scopes["foo"] = registry.Entry{Dir: srcDir, Root: srcDir}
	path := writeRehomeTicket(t, srcDir, "foo-ab2c", "todo", "a0", "", "")
	if err := os.MkdirAll(filepath.Join(destRepo, ".git", "rebase-merge"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := Rehome(dest.deps, RehomeInput{
		SourceScope: "foo", SourceDir: srcDir, DestScope: "bar", DestDir: dest.dir,
		Lookup: fullLookup("foo-ab2c"),
	})
	var mid *gitstate.MidRebaseError
	if !errors.As(err, &mid) {
		t.Fatalf("want dest mid-rebase, got %v", err)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Errorf("source must remain, %v", statErr)
	}
}

func TestRehomeSyncDisabledNoGitRoot(t *testing.T) {
	base := t.TempDir()
	srcDir := filepath.Join(base, "foo")
	destDir := filepath.Join(base, "bar")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(srcDir, "tk.cue"), "name: \"foo\"\nautoCommit: true\n")
	writeFile(t, filepath.Join(destDir, "tk.cue"), "name: \"bar\"\nautoCommit: true\n")
	d := openDual(t, "foo", srcDir, "bar", destDir)
	writeRehomeTicket(t, srcDir, "foo-ab2c", "todo", "a0", "", "")

	res, err := rehomeFoo(d, "foo-ab2c")
	if err != nil {
		t.Fatalf("rehome: %v", err)
	}
	if len(res.SyncDisabledAll) != 2 {
		t.Errorf("want two sync_disabled, got %v", res.SyncDisabledAll)
	}
	joined := strings.Join(res.SyncDisabledAll, "\n")
	if !strings.Contains(joined, "foo:") || !strings.Contains(joined, "bar:") {
		t.Errorf("each side must be named, got %v", res.SyncDisabledAll)
	}
	if _, err := os.Stat(res.Path); err != nil {
		t.Errorf("files still written: %v", err)
	}
}

func TestRehomeTwoRootsSyncNeeded(t *testing.T) {
	requireGit(t)
	src, srcRepo := initAutoCommitRepo(t, "foo")
	dest, destRepo := initAutoCommitRepo(t, "bar")
	d := openDual(t, "foo", src.dir, "bar", dest.dir)
	writeRehomeTicket(t, src.dir, "foo-ab2c", "todo", "a0", "", "")
	seedAndPush(t, srcRepo)
	seedAndPush(t, destRepo)

	res, err := Rehome(d.deps, RehomeInput{
		SourceScope: "foo", SourceDir: src.dir, DestScope: "bar", DestDir: dest.dir,
		Lookup: fullLookup("foo-ab2c"),
	})
	if err != nil {
		t.Fatalf("rehome: %v", err)
	}
	if len(res.SyncNeededAll) != 2 {
		t.Errorf("want one sync_needed per root, got %v", res.SyncNeededAll)
	}
	for _, line := range res.SyncNeededAll {
		if line != "unpushed" {
			t.Errorf("reason = %q", line)
		}
	}
	log := gitLog(t, srcRepo)
	if len(log) == 0 || !strings.HasPrefix(log[0], "tk: rehome foo-ab2c ->") {
		t.Errorf("source commit = %v", log)
	}
	dlog := gitLog(t, destRepo)
	if len(dlog) == 0 || !strings.HasPrefix(dlog[0], "tk: rehome foo-ab2c ->") {
		t.Errorf("dest commit = %v", dlog)
	}
}

func TestRehomeSharedRootAutoCommitMismatchRefuses(t *testing.T) {
	requireGit(t)
	repo := t.TempDir()
	testgit.Run(t, repo, "init", "-b", "main")
	testgit.Run(t, repo, "config", "user.email", "a@b.c")
	testgit.Run(t, repo, "config", "user.name", "tk-test")
	testgit.Run(t, repo, "config", "commit.gpgsign", "false")
	srcDir := filepath.Join(repo, "foo")
	destDir := filepath.Join(repo, "bar")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(srcDir, "tk.cue"), "name: \"foo\"\nautoCommit: false\n")
	writeFile(t, filepath.Join(destDir, "tk.cue"), "name: \"bar\"\nautoCommit: true\n")
	d := openDual(t, "foo", srcDir, "bar", destDir)
	src := writeRehomeTicket(t, srcDir, "foo-ab2c", "todo", "a0", "", "")

	_, err := Rehome(d.deps, RehomeInput{
		SourceScope: "foo", SourceDir: srcDir, DestScope: "bar", DestDir: destDir,
		Lookup: fullLookup("foo-ab2c"),
	})
	if err == nil || !strings.Contains(err.Error(), token.AutoCommitMismatch) {
		t.Fatalf("want auto_commit_mismatch, got %v", err)
	}
	if _, statErr := os.Stat(src); statErr != nil {
		t.Errorf("source must remain, %v", statErr)
	}
	entries, _ := os.ReadDir(destDir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "bar-ab2c") {
			t.Errorf("dest must not have been written, files=%v", entries)
		}
	}
}

func TestRehomeSharedRootOneCommit(t *testing.T) {
	requireGit(t)
	repo := t.TempDir()
	testgit.Run(t, repo, "init", "-b", "main")
	testgit.Run(t, repo, "config", "user.email", "a@b.c")
	testgit.Run(t, repo, "config", "user.name", "tk-test")
	testgit.Run(t, repo, "config", "commit.gpgsign", "false")
	srcDir := filepath.Join(repo, "foo")
	destDir := filepath.Join(repo, "bar")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(srcDir, "tk.cue"), "name: \"foo\"\nautoCommit: true\n")
	writeFile(t, filepath.Join(destDir, "tk.cue"), "name: \"bar\"\nautoCommit: true\n")
	d := openDual(t, "foo", srcDir, "bar", destDir)
	writeRehomeTicket(t, srcDir, "foo-ab2c", "todo", "a0", "", "")
	seedAndPush(t, repo)

	res, err := Rehome(d.deps, RehomeInput{
		SourceScope: "foo", SourceDir: srcDir, DestScope: "bar", DestDir: destDir,
		Lookup: fullLookup("foo-ab2c"),
	})
	if err != nil {
		t.Fatalf("rehome: %v", err)
	}
	log := gitLog(t, repo)
	rehomes := 0
	for _, line := range log {
		if strings.HasPrefix(line, "tk: rehome ") {
			rehomes++
		}
	}
	if rehomes != 1 {
		t.Errorf("shared root must self-commit once, log=%v", log)
	}
	if len(res.SyncNeededAll) != 1 || res.SyncNeededAll[0] != "unpushed" {
		t.Errorf("one sync_needed for the shared root, got %v", res.SyncNeededAll)
	}
}

func seedAndPush(t *testing.T, repo string) {
	t.Helper()
	remote := t.TempDir()
	testgit.Run(t, remote, "init", "--bare", "-b", "main")
	testgit.Run(t, repo, "add", "-A")
	testgit.Run(t, repo, "commit", "-m", "seed")
	testgit.Run(t, repo, "remote", "add", "origin", remote)
	testgit.Run(t, repo, "push", "-u", "origin", "main")
}
