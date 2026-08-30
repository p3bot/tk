package writeengine

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/frontmatter"
	"github.com/p3bot/tk/internal/registry"
	"github.com/p3bot/tk/internal/token"
)

func TestMetaRefusesImmutableClassNewline(t *testing.T) {
	e := newPlainEnv(t, "wc", "name: \"wc\"\nautoCommit: false\n")
	addTicket(t, e.dir, "wc-ab2c", "todo")
	lu := fullLookup("wc-ab2c")

	_, err := Meta(e.deps, MetaInput{Scope: "wc", Dir: e.dir, Lookup: lu, Op: MetaSet, Key: "status", Value: "done"})
	var use *UsageError
	if !errors.As(err, &use) || !strings.Contains(use.Msg, "immutable") || !strings.Contains(use.Msg, "tk mark") {
		t.Errorf("status immutable: %v", err)
	}
	_, err = Meta(e.deps, MetaInput{Scope: "wc", Dir: e.dir, Lookup: lu, Op: MetaSet, Key: "order", Value: "a1"})
	if !errors.As(err, &use) || !strings.Contains(use.Msg, "tk order") {
		t.Errorf("order immutable: %v", err)
	}
	_, err = Meta(e.deps, MetaInput{Scope: "wc", Dir: e.dir, Lookup: lu, Op: MetaSet, Key: "depends", Value: "x"})
	if !errors.As(err, &use) || !strings.Contains(use.Msg, "multi-value") {
		t.Errorf("set depends: %v", err)
	}
	_, err = Meta(e.deps, MetaInput{Scope: "wc", Dir: e.dir, Lookup: lu, Op: MetaAdd, Key: "summary", Value: "x"})
	if !errors.As(err, &use) || !strings.Contains(use.Msg, "scalar") {
		t.Errorf("add summary: %v", err)
	}
	_, err = Meta(e.deps, MetaInput{Scope: "wc", Dir: e.dir, Lookup: lu, Op: MetaSet, Key: "summary", Value: "a\nb"})
	if !errors.As(err, &use) || !strings.Contains(use.Msg, "embedded newlines") {
		t.Errorf("newline: %v", err)
	}
	_, err = Meta(e.deps, MetaInput{Scope: "wc", Dir: e.dir, Lookup: lu, Op: MetaSet, Key: "nope", Value: "x"})
	if !errors.As(err, &use) || !strings.Contains(use.Msg, "unknown frontmatter key") {
		t.Errorf("unknown key: %v", err)
	}
}

func TestMetaUsageRefuseKeepsReconcileWarnings(t *testing.T) {
	e := newPlainEnv(t, "wc", "name: \"wc\"\nautoCommit: false\n")
	addTicket(t, e.dir, "wc-ab2c", "todo")
	writeFile(t, filepath.Join(e.dir, "wc-abcd-x.md"), "---\nid: wc-abcd\nstatus: [unterminated\n---\n# broke\n")

	res, err := Meta(e.deps, MetaInput{
		Scope: "wc", Dir: e.dir, Lookup: fullLookup("wc-ab2c"),
		Op: MetaSet, Key: "nope", Value: "x",
	})
	var use *UsageError
	if !errors.As(err, &use) {
		t.Fatalf("want usage, got %v", err)
	}
	found := false
	for _, w := range res.Warnings {
		if strings.Contains(w, token.ParseError) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("want parse_error warning on usage refuse, got %v", res.Warnings)
	}
}

func TestMetaDependsAddIntegrity(t *testing.T) {
	e := newPlainEnv(t, "wc", "name: \"wc\"\nautoCommit: false\n")
	addTicket(t, e.dir, "wc-ab2c", "todo")
	addTicket(t, e.dir, "wc-de34", "todo")
	lu := fullLookup("wc-ab2c")
	path := filepath.Join(e.dir, "wc-ab2c-work.md")

	res, err := Meta(e.deps, MetaInput{Scope: "wc", Dir: e.dir, Lookup: lu, Op: MetaAdd, Key: "depends", Value: "de34"})
	if err != nil {
		t.Fatalf("add depends: %v", err)
	}
	m := parseTicket(t, res.Path)
	if !equalStrings(m.Depends, []string{"wc-de34"}) {
		t.Errorf("depends = %v", m.Depends)
	}

	before := fileSnapshot(t, path)
	_, err = Meta(e.deps, MetaInput{Scope: "wc", Dir: e.dir, Lookup: lu, Op: MetaAdd, Key: "depends", Value: "wc-ab2c"})
	var self *DependsSelfError
	if !errors.As(err, &self) || !strings.Contains(err.Error(), token.DependsSelf) {
		t.Errorf("self: %v", err)
	}
	if fileSnapshot(t, path) != before {
		t.Error("self must not write")
	}

	_, err = Meta(e.deps, MetaInput{Scope: "wc", Dir: e.dir, Lookup: lu, Op: MetaAdd, Key: "depends", Value: "wc-zz99"})
	var dang *DependsDanglingError
	if !errors.As(err, &dang) || !strings.Contains(err.Error(), token.DependsDangling) {
		t.Errorf("dangling: %v", err)
	}

	_, err = Meta(e.deps, MetaInput{Scope: "wc", Dir: e.dir, Lookup: lu, Op: MetaAdd, Key: "depends", Value: "zzz-zz99"})
	var unres *DependsUnresolvableError
	if !errors.As(err, &unres) || !strings.Contains(err.Error(), token.DependsUnresolvable) {
		t.Errorf("unresolvable: %v", err)
	}

	_, err = Meta(e.deps, MetaInput{Scope: "wc", Dir: e.dir, Lookup: lu, Op: MetaAdd, Key: "related", Value: "wc-zz99"})
	if err != nil {
		t.Fatalf("related missing is soft: %v", err)
	}

	addTicket(t, e.dir, "wc-gh56", "todo")
	e.deps.Reg.Me["wc"] = "wc-gh56"
	res, err = Meta(e.deps, MetaInput{Scope: "wc", Dir: e.dir, Lookup: lu, Op: MetaAdd, Key: "depends", Value: "me"})
	if err != nil {
		t.Fatalf("depends me: %v", err)
	}
	if !equalStrings(parseTicket(t, res.Path).Depends, []string{"wc-de34", "wc-gh56"}) {
		t.Errorf("depends me = %v", parseTicket(t, res.Path).Depends)
	}

	var use *UsageError
	_, err = Meta(e.deps, MetaInput{Scope: "wc", Dir: e.dir, Lookup: lu, Op: MetaAdd, Key: "related", Value: "bad!"})
	if !errors.As(err, &use) || !strings.Contains(use.Msg, "not a valid ticket id") {
		t.Errorf("malformed related: %v", err)
	}
}

func TestMetaDependsQuarantinedAndCrossScope(t *testing.T) {
	e := newPlainEnv(t, "wc", "name: \"wc\"\nautoCommit: false\n")
	addTicket(t, e.dir, "wc-ab2c", "todo")
	writeFile(t, filepath.Join(e.dir, "wc-bb33-q.md"), "---\nid: wc-bb33\nstatus: [unterminated\n---\n# Q\n")

	_, err := Meta(e.deps, MetaInput{Scope: "wc", Dir: e.dir, Lookup: fullLookup("wc-ab2c"), Op: MetaAdd, Key: "depends", Value: "wc-bb33"})
	var dang *DependsDanglingError
	if !errors.As(err, &dang) {
		t.Errorf("quarantined-only: %v", err)
	}

	up := filepath.Join(t.TempDir(), "up")
	if err := os.MkdirAll(up, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(up, "tk.cue"), "name: \"up\"\nautoCommit: false\n")
	addTicket(t, up, "up-aa22", "todo")
	e.deps.Reg.Scopes["up"] = registry.Entry{Dir: up, Root: up}

	res, err := Meta(e.deps, MetaInput{Scope: "wc", Dir: e.dir, Lookup: fullLookup("wc-ab2c"), Op: MetaAdd, Key: "depends", Value: "up-aa22"})
	if err != nil {
		t.Fatalf("cross-scope add: %v", err)
	}
	if !equalStrings(parseTicket(t, res.Path).Depends, []string{"up-aa22"}) {
		t.Errorf("cross depends = %v", parseTicket(t, res.Path).Depends)
	}

	if err := os.RemoveAll(up); err != nil {
		t.Fatal(err)
	}
	_, err = Meta(e.deps, MetaInput{Scope: "wc", Dir: e.dir, Lookup: fullLookup("wc-ab2c"), Op: MetaAdd, Key: "depends", Value: "up-aa22"})
	var unres *DependsUnresolvableError
	if !errors.As(err, &unres) {
		t.Errorf("unreachable: %v", err)
	}
}

func TestMetaTagNewAndRequiredMissing(t *testing.T) {
	e := newPlainEnv(t, "wc", `name: "wc"
autoCommit: false
fields: { jira: { type: "string", required: true } }
`)
	addTicket(t, e.dir, "wc-ab2c", "todo")
	addTicketExtra(t, e.dir, "wc-de34", "a1", "tags: [shared]\n")

	res, err := Meta(e.deps, MetaInput{Scope: "wc", Dir: e.dir, Lookup: fullLookup("wc-ab2c"), Op: MetaAdd, Key: "tag", Value: "orphan"})
	if err != nil {
		t.Fatalf("tag add: %v", err)
	}
	if !equalStrings(res.TagNew, []string{"orphan"}) {
		t.Errorf("TagNew = %v", res.TagNew)
	}
	if !equalStrings(res.RequiredMissing, []string{"jira"}) {
		t.Errorf("RequiredMissing = %v", res.RequiredMissing)
	}

	res, err = Meta(e.deps, MetaInput{Scope: "wc", Dir: e.dir, Lookup: fullLookup("wc-ab2c"), Op: MetaAdd, Key: "tags", Value: "shared"})
	if err != nil {
		t.Fatalf("existing tag: %v", err)
	}
	if len(res.TagNew) != 0 {
		t.Errorf("board-existing TagNew = %v", res.TagNew)
	}
}

func TestMetaSelfCommit(t *testing.T) {
	e, repo := initAutoCommitRepo(t, "ac")
	addTicket(t, e.dir, "ac-ab2c", "todo")
	if _, err := Meta(e.deps, MetaInput{Scope: "ac", Dir: e.dir, Lookup: fullLookup("ac-ab2c"), Op: MetaSet, Key: "summary", Value: "s"}); err != nil {
		t.Fatalf("meta set: %v", err)
	}
	log := gitLog(t, repo)
	if len(log) < 1 || !strings.Contains(log[0], "meta set summary") {
		t.Errorf("self-commit log=%v", log)
	}
}

func fileSnapshot(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func addTicketExtra(t *testing.T, dir, id, order, extra string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, id+"-work.md"), "---\nid: "+id+"\nstatus: todo\norder: \""+order+"\"\ncreated: 2026-01-01T00:00:00Z\n"+extra+"---\n# Work\n")
}

func TestClassifyKnownKeys(t *testing.T) {
	class, _, err := ClassifyMetaKey(frontmatter.KeySummary, nil)
	if err != nil || class != MetaKeyScalar {
		t.Errorf("summary class = %v err=%v", class, err)
	}
	class, _, err = ClassifyMetaKey(frontmatter.KeyDepends, nil)
	if err != nil || class != MetaKeyMulti {
		t.Errorf("depends class = %v err=%v", class, err)
	}
}
