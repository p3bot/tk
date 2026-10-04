package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/token"
)

func TestDesignSearchReadsIndexNotTicketSearch(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	other := initScope(t, app, "api")
	t.Setenv("TK_SCOPE", "wc")
	addTicket(t, dir, "wc-m4np", "target", "todo", "a0", "# Target\n\nticketonlyterm\n", false, "")
	writeDesign(t, dir, "wc-de34", "later", "accepted", "2026-01-02T00:00:00Z", "Shape")
	writeDesign(t, dir, "wc-ab2c", "shape", "draft", "2026-01-01T00:00:00Z", "Shape")
	shape := filepath.Join(dir, "design", "wc-ab2c-shape.md")
	if err := os.WriteFile(shape, []byte("---\nid: wc-ab2c\nstatus: draft\ncreated: 2026-01-01T00:00:00Z\n---\n# Shape\n\ndesignonlyterm\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	later := filepath.Join(dir, "design", "wc-de34-later.md")
	if err := os.WriteFile(later, []byte("---\nid: wc-de34\nstatus: accepted\ncreated: 2026-01-02T00:00:00Z\n---\n# Shape\n\ndesignonlyterm\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeDesign(t, other, "api-xy99", "remote", "draft", "2026-01-01T00:00:00Z", "Remote")
	remote := filepath.Join(other, "design", "api-xy99-remote.md")
	if err := os.WriteFile(remote, []byte("---\nid: api-xy99\nstatus: draft\ncreated: 2026-01-01T00:00:00Z\n---\n# Remote\n\ndesignonlyterm\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(dir, "design", "wc-gh56-broken.md")
	if err := os.WriteFile(broken, []byte("# no fence\nuniquebrokenword\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, _, err := run(t, app, "design", "search", "designonlyterm", "--scope", "wc")
	if err != nil {
		t.Fatal(err)
	}
	rows := lines(out)
	if len(rows) != 2 || rows[0] != "wc-ab2c\tdraft\tShape\t"+shape || rows[1] != "wc-de34\taccepted\tShape\t"+later {
		t.Fatalf("scoped search = %q", out)
	}
	wide, _, err := run(t, app, "design", "search", "designonlyterm")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(wide, "api-xy99\tdraft\tRemote\t"+remote) || !strings.Contains(wide, "wc-ab2c\t") {
		t.Fatalf("machine-wide = %q", wide)
	}
	ticketSearch, _, err := run(t, app, "search", "designonlyterm", "--scope", "wc")
	if err != nil || strings.TrimSpace(ticketSearch) != "" {
		t.Fatalf("tk search = %q err %v", ticketSearch, err)
	}
	hit, _, err := run(t, app, "design", "search", "uniquebrokenword", "--scope", "wc")
	if err != nil {
		t.Fatal(err)
	}
	if hit != "wc-gh56\t\t\t"+broken+"\n" {
		t.Fatalf("parse-error hit = %q", hit)
	}
	empty, _, err := run(t, app, "design", "search", "nosuchword", "--scope", "wc")
	if err != nil || empty != "" {
		t.Fatalf("empty = %q err %v", empty, err)
	}
	if _, _, err := run(t, app, "design", "find", "designonlyterm"); err == nil {
		t.Fatal("find must not alias design search")
	}
}

func TestDependsProducedByAndDesignProduces(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	t.Setenv("TK_SCOPE", "wc")
	addTicket(t, dir, "wc-gh56", "alone", "todo", "a0", "# Alone\n", false, "")
	addTicket(t, dir, "wc-m4np", "target", "todo", "a1", "# Target\n", false, "depends: [wc-de34]\n")
	addTicket(t, dir, "wc-de34", "dep", "todo", "a2", "# Dep\n", false, "")
	addTicket(t, dir, "wc-mn89", "plain", "todo", "a3", "# Plain\n", false, "")
	raw := "---\nid: wc-ab2c\nstatus: accepted\ncreated: 2026-01-01T00:00:00Z\nproduces: [wc-m4np, wc-gh56]\n---\n# Shape\n"
	if err := os.MkdirAll(filepath.Join(dir, "design"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "design", "wc-ab2c-shape.md"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	writeDesign(t, dir, "wc-jk89", "one", "draft", "2026-01-01T00:00:00Z", "One")
	writeDesign(t, dir, "wc-jk89", "two", "draft", "2026-01-02T00:00:00Z", "Two")

	out, _, err := run(t, app, "depends", "wc-m4np")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "depends on:\n  wc-de34\ttodo\tDep") || !strings.Contains(out, "produced by:\n  wc-ab2c\taccepted\tShape") {
		t.Fatalf("ticket depends = %q", out)
	}
	trans, _, err := run(t, app, "depends", "wc-m4np", "--transitive")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(trans, "depends on (transitive):\n  wc-de34\ttodo\tDep") || !strings.Contains(trans, "produced by:\n  wc-ab2c\taccepted\tShape") {
		t.Fatalf("transitive = %q", trans)
	}
	if strings.Contains(trans, "wc-ab2c\taccepted") && strings.Contains(strings.Split(trans, "produced by:")[0], "wc-ab2c") {
		t.Fatalf("transitive walked produces: %q", trans)
	}
	tree, _, err := run(t, app, "depends", "wc-m4np", "--tree")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(tree, "ab2c") || strings.Contains(tree, "produced by") || strings.Contains(tree, "produces:") {
		t.Fatalf("tree = %q", tree)
	}
	onDesign, _, err := run(t, app, "depends", "ab2c")
	if err != nil {
		t.Fatal(err)
	}
	if onDesign != "produces:\n  wc-gh56\ttodo\tAlone\n  wc-m4np\ttodo\tTarget\n" {
		t.Fatalf("design depends = %q", onDesign)
	}
	plain, _, err := run(t, app, "depends", "wc-mn89")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plain, "produced by:\n  (none)") {
		t.Fatalf("empty produced by = %q", plain)
	}
	next, _, err := run(t, app, "next")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(next, "wc-gh56-alone.md") {
		t.Fatalf("next skipped the todo that is only linked by produces: %q", next)
	}
	shared, _, err := run(t, app, "depends", "jk89")
	if err == nil || shared != "" || !strings.Contains(err.Error(), token.DesignID) {
		t.Fatalf("shared design depends out %q err %v", shared, err)
	}
}

func TestDesignDoctorResidueFromRowFile(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	t.Setenv("TK_SCOPE", "wc")
	if err := os.MkdirAll(filepath.Join(dir, "design"), 0o755); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(dir, "design", "wc-ab2c-foreign.md")
	if err := os.WriteFile(foreign, []byte("---\nid: api-xy99\nstatus: draft\ncreated: 2026-01-01T00:00:00Z\n---\n# Foreign\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scalar := filepath.Join(dir, "design", "wc-de34-scalar.md")
	if err := os.WriteFile(scalar, []byte("---\nid: wc-de34\nstatus: draft\ncreated: 2026-01-01T00:00:00Z\nproduces: hello\n---\n# Scalar\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	partial := filepath.Join(dir, "design", "wc-gh56-partial.md")
	if err := os.WriteFile(partial, []byte("---\nid: wc-gh56\nstatus: draft\ncreated: 2026-01-01T00:00:00Z\nproduces: [nope]\n---\n# Partial\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	doc, _, err := run(t, app, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	mismatch := `filename/id mismatch: wc-ab2c-foreign.md does not begin with its frontmatter id "api-xy99"`
	if !strings.Contains(doc, mismatch) {
		t.Fatalf("doctor = %q", doc)
	}
	if strings.Contains(doc, token.DesignID+" wc-ab2c") || strings.Contains(doc, token.DesignID+" api-xy99") {
		t.Fatalf("foreign fence must not be design_id: %q", doc)
	}
	hello := "produces_dangling: wc-de34 produces is not a list of ticket ids (" + scalar + ")"
	nope := "produces_dangling: wc-gh56 produces nope which has no ticket (" + partial + ")"
	if !strings.Contains(doc, hello) || !strings.Contains(doc, nope) {
		t.Fatalf("doctor = %q", doc)
	}
	if strings.Contains(doc, token.DuplicateID) {
		t.Fatalf("design path must not be duplicate_id: %q", doc)
	}
}

func TestDependsSharedTicketAndDesign(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	t.Setenv("TK_SCOPE", "wc")
	addTicket(t, dir, "wc-ab2c", "ticket", "todo", "a0", "# Ticket\n", false, "")
	addTicket(t, dir, "wc-gh56", "made", "todo", "a1", "# Made\n", false, "")
	if err := os.MkdirAll(filepath.Join(dir, "design"), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := "---\nid: wc-ab2c\nstatus: accepted\ncreated: 2026-01-01T00:00:00Z\nproduces: [wc-gh56]\n---\n# Shape\n"
	if err := os.WriteFile(filepath.Join(dir, "design", "wc-ab2c-shape.md"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	out, errOut, err := run(t, app, "depends", "ab2c")
	if err != nil {
		t.Fatal(err)
	}
	want := "depends on:\n  (none)\nis depended on by:\n  (none)\nrelated:\n  (none)\nproduced by:\n  (none)\nproduces:\n  wc-gh56\ttodo\tMade\n"
	if out != want || errOut != "" {
		t.Fatalf("shared id out %q stderr %q", out, errOut)
	}
	trans, _, err := run(t, app, "depends", "ab2c", "--transitive")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(trans, "produces:\n  wc-gh56\ttodo\tMade") || strings.Contains(strings.Split(trans, "produced by:")[0], "wc-gh56") {
		t.Fatalf("transitive = %q", trans)
	}
	tree, _, err := run(t, app, "depends", "ab2c", "--tree")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(tree, "produces:") || strings.Contains(tree, "gh56") || strings.Contains(tree, "produced by") {
		t.Fatalf("tree = %q", tree)
	}

	addTicket(t, dir, "wc-jk89", "plain", "todo", "a2", "# Plain\n", false, "")
	writeDesign(t, dir, "wc-jk89", "one", "draft", "2026-01-01T00:00:00Z", "One")
	writeDesign(t, dir, "wc-jk89", "two", "draft", "2026-01-02T00:00:00Z", "Two")
	several, errOut, err := run(t, app, "depends", "jk89")
	if err != nil || strings.Contains(several, "produces:") || !strings.Contains(several, "produced by:\n  (none)") {
		t.Fatalf("several designs out %q err %v", several, err)
	}
	if !strings.Contains(errOut, token.DesignID) || !strings.Contains(errOut, "2 design files") {
		t.Fatalf("several designs stderr %q", errOut)
	}
}

func TestDependsProducedByAmbiguousSharedDesign(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	t.Setenv("TK_SCOPE", "wc")
	addTicket(t, dir, "wc-gh56", "made", "todo", "a0", "# Made\n", false, "")
	if err := os.MkdirAll(filepath.Join(dir, "design"), 0o755); err != nil {
		t.Fatal(err)
	}
	one := "---\nid: wc-ab2c\nstatus: draft\ncreated: 2026-01-01T00:00:00Z\nproduces: [wc-gh56]\n---\n# One\n"
	two := "---\nid: wc-ab2c\nstatus: accepted\ncreated: 2026-01-02T00:00:00Z\nproduces: [wc-gh56]\n---\n# Two\n"
	if err := os.WriteFile(filepath.Join(dir, "design", "wc-ab2c-one.md"), []byte(one), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "design", "wc-ab2c-two.md"), []byte(two), 0o644); err != nil {
		t.Fatal(err)
	}

	out, _, err := run(t, app, "depends", "gh56")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "produced by:\n  wc-ab2c\t(ambiguous)\n") {
		t.Fatalf("produced by = %q", out)
	}
	if strings.Contains(out, "One") || strings.Contains(out, "Two") || strings.Contains(out, "draft") || strings.Contains(out, "accepted") {
		t.Fatalf("produced by picked a file: %q", out)
	}
	shared, _, err := run(t, app, "depends", "ab2c")
	if err == nil || shared != "" || !strings.Contains(err.Error(), token.DesignID) {
		t.Fatalf("shared design depends out %q err %v", shared, err)
	}
}
