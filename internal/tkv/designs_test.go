package tkv

import (
	"bytes"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/frontmatter"
	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/scopefile"
	"github.com/p3bot/tk/internal/testgit"
)

func addDesign(t *testing.T, dir, id, slug, status, body, extra string) string {
	t.Helper()
	target := filepath.Join(dir, scopefile.DesignDir)
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(target, id+"-"+slug+".md")
	fm := "---\nid: " + id + "\nstatus: " + status + "\ncreated: 2026-01-01T00:00:00Z\n" + extra + "---\n"
	if err := os.WriteFile(path, []byte(fm+body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDesignsListInspectAndWrites(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	initScope(t, app, "aa")
	addTicket(t, dir, "wc-m4np", "ticketfish", "todo", "a0", "# Ticketfish\n\nboard work\n", false, "")
	draft := addDesign(t, dir, "wc-ab2c", "shape", "draft", "# Designfish draft\n\nsockets **live**\n", "changed: 2026-01-01T00:00:00Z\n")
	addDesign(t, dir, "wc-cd3e", "old", "decomposed", "# Designfish old\n", "changed: 2026-01-02T00:00:00Z\n")
	addDesign(t, dir, "wc-ef4g", "plain", "draft", "# Designfish plain\n\nno stamp\n", "")
	broken := "---\nid: wc-k2mp\n<<<<<<< HEAD\nstatus: draft\n---\n# Broken\nrawtoken\n"
	if err := os.WriteFile(filepath.Join(dir, scopefile.DesignDir, "wc-k2mp-broken.md"), []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	s := mustServer(t, app)

	empty := do(s, "/scope/aa/designs")
	if empty.Code != http.StatusOK {
		t.Fatalf("empty scope = %d %s", empty.Code, empty.Body.String())
	}
	if !strings.Contains(empty.Body.String(), "No designs.") {
		t.Fatalf("empty list: %s", empty.Body.String())
	}

	onlyOld := initScope(t, app, "bb")
	addDesign(t, onlyOld, "bb-cd3e", "old", "decomposed", "# Old only\n", "")
	hidden := do(s, "/scope/bb/designs")
	if hidden.Code != http.StatusOK {
		t.Fatalf("hidden statuses = %d %s", hidden.Code, hidden.Body.String())
	}
	hb := hidden.Body.String()
	if !strings.Contains(hb, "bb-cd3e") || strings.Contains(hb, "No draft or accepted designs.") || strings.Contains(hb, "No designs.") {
		t.Fatalf("default list hid the only design: %s", hb)
	}
	if !strings.Contains(hb, "Draft and accepted") || !strings.Contains(hb, `aria-checked="false"`) {
		t.Fatalf("default switch: %s", hb)
	}
	narrow := do(s, "/scope/bb/designs?listed=1").Body.String()
	if strings.Contains(narrow, "bb-cd3e") || !strings.Contains(narrow, "No draft or accepted designs.") || strings.Contains(narrow, "No designs.") {
		t.Fatalf("draft and accepted filter: %s", narrow)
	}
	if !strings.Contains(narrow, `aria-checked="true"`) || !strings.Contains(narrow, `href="/scope/bb/designs"`) {
		t.Fatalf("filter switch: %s", narrow)
	}

	onlyBroken := initScope(t, app, "zz")
	if err := os.MkdirAll(filepath.Join(onlyBroken, scopefile.DesignDir), 0o755); err != nil {
		t.Fatal(err)
	}
	brokenOnly := "---\nid: zz-k2mp\n<<<<<<< HEAD\nstatus: draft\n---\n# Broken\nrawtoken\n"
	if err := os.WriteFile(filepath.Join(onlyBroken, scopefile.DesignDir, "zz-k2mp-broken.md"), []byte(brokenOnly), 0o644); err != nil {
		t.Fatal(err)
	}
	broke := do(s, "/scope/zz/designs")
	if broke.Code != http.StatusOK {
		t.Fatalf("broken only = %d %s", broke.Code, broke.Body.String())
	}
	if strings.Contains(broke.Body.String(), "No designs.") || strings.Contains(broke.Body.String(), "No draft or accepted") {
		t.Fatalf("broken-only list claimed it was empty: %s", broke.Body.String())
	}
	if !strings.Contains(broke.Body.String(), `href="/scope/zz/designs/zz-k2mp"`) {
		t.Fatalf("broken-only list did not link the file: %s", broke.Body.String())
	}

	list := do(s, "/scope/wc/designs")
	if list.Code != http.StatusOK {
		t.Fatalf("list = %d %s", list.Code, list.Body.String())
	}
	lb := list.Body.String()
	table, brokenSection, ok := strings.Cut(lb, `<section class="broken">`)
	if !ok {
		t.Fatalf("broken designs are not reachable from the list: %s", lb)
	}
	if !strings.Contains(table, "wc-ab2c") || !strings.Contains(table, "Designfish draft") {
		t.Fatalf("default list missing draft: %s", table)
	}
	if !strings.Contains(table, "wc-cd3e") || !strings.Contains(table, "Designfish old") {
		t.Fatalf("default list hid decomposed: %s", table)
	}
	if strings.Contains(table, "wc-k2mp") {
		t.Fatalf("parse error mixed into the status table: %s", table)
	}
	if !strings.Contains(brokenSection, `href="/scope/wc/designs/wc-k2mp"`) {
		t.Fatalf("broken file is not linked: %s", brokenSection)
	}
	if strings.Count(lb, `class="dwell"`) != 2 || !strings.Contains(lb, `title="2026-01-01T00:00:00Z"`) || !strings.Contains(lb, `title="2026-01-02T00:00:00Z"`) {
		t.Fatalf("dwell labels: %s", lb)
	}

	narrowed := do(s, "/scope/wc/designs?listed=1")
	if narrowed.Code != http.StatusOK {
		t.Fatalf("listed = %d %s", narrowed.Code, narrowed.Body.String())
	}
	narrowTable, _, _ := strings.Cut(narrowed.Body.String(), `<section class="broken">`)
	if strings.Contains(narrowTable, "wc-cd3e") || strings.Contains(narrowTable, "Designfish old") {
		t.Fatalf("draft and accepted filter showed decomposed: %s", narrowTable)
	}
	if !strings.Contains(narrowTable, "wc-ab2c") || !strings.Contains(narrowTable, "Designfish draft") {
		t.Fatalf("draft and accepted filter hid a draft: %s", narrowTable)
	}
	if strings.Contains(narrowTable, "wc-k2mp") {
		t.Fatalf("filter mixed in the broken file: %s", narrowTable)
	}
	if strings.Count(narrowed.Body.String(), `class="dwell"`) != 1 || strings.Contains(narrowed.Body.String(), `title="2026-01-02T00:00:00Z"`) {
		t.Fatalf("filtered design still has a dwell label: %s", narrowed.Body.String())
	}

	ins := do(s, "/scope/wc/designs/ab2c")
	if ins.Code != http.StatusOK {
		t.Fatalf("inspect = %d %s", ins.Code, ins.Body.String())
	}
	ib := ins.Body.String()
	if !strings.Contains(ib, "<strong>live</strong>") {
		t.Fatalf("inspect did not render the body: %s", ib)
	}
	if !strings.Contains(ib, `href="/scope/aa/designs">aa</a>`) {
		t.Fatalf("inspect dropped the scope switcher off designs: %s", ib)
	}
	if !strings.Contains(ib, "draft") || !strings.Contains(ib, "2026-01-01T00:00:00Z") {
		t.Fatalf("inspect meta: %s", ib)
	}
	plain := do(s, "/scope/wc/designs/wc-ef4g").Body.String()
	if !strings.Contains(plain, "<dt>changed</dt><dd><span class=\"empty\">none</span></dd>") {
		t.Fatalf("missing changed key should show none: %s", plain)
	}

	brokenPage := do(s, "/scope/wc/designs/wc-k2mp")
	if brokenPage.Code != http.StatusOK || !strings.Contains(brokenPage.Body.String(), "rawtoken") {
		t.Fatalf("broken inspect = %d %s", brokenPage.Code, brokenPage.Body.String())
	}
	if strings.Contains(brokenPage.Body.String(), `action="/scope/wc/designs/mark"`) || strings.Contains(brokenPage.Body.String(), "/edit") {
		t.Fatalf("broken design is editable: %s", brokenPage.Body.String())
	}
	if code := do(s, "/scope/wc/designs/wc-k2mp/edit").Code; code != http.StatusBadRequest {
		t.Fatalf("broken edit = %d, want 400", code)
	}

	marked := mustFollow(t, s, doPost(s, "/scope/wc/designs/mark", url.Values{
		"id": {"wc-ab2c"}, "status": {"accepted"},
	}))
	mb := marked.Body.String()
	if !strings.Contains(mb, "<dt>status</dt><dd>accepted</dd>") {
		t.Fatalf("mark did not update the page: %s", mb)
	}
	if !strings.Contains(mb, "/design/") {
		t.Fatalf("mark left design/: %s", mb)
	}
	got, err := os.ReadFile(draft)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "status: accepted\n") {
		t.Fatalf("file status: %s", got)
	}
	arch, err := filepath.Glob(filepath.Join(dir, "archive", "wc-ab2c-*.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(arch) != 0 {
		t.Fatalf("mark moved the design into archive: %v", arch)
	}

	refused := doPost(s, "/scope/wc/designs/mark", url.Values{"id": {"wc-ab2c"}, "status": {"todo"}})
	if refused.Code != http.StatusBadRequest {
		t.Fatalf("ticket status = %d %s", refused.Code, refused.Body.String())
	}
	afterRefuse, err := os.ReadFile(draft)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterRefuse) != string(got) {
		t.Fatalf("ticket status wrote:\n%s", afterRefuse)
	}

	added := mustFollow(t, s, doPost(s, "/scope/wc/designs/produces", url.Values{
		"id": {"wc-ab2c"}, "op": {"add"}, "target": {"wc-m4np"},
	}))
	if !strings.Contains(added.Body.String(), `href="/scope/wc/wc-m4np"`) {
		t.Fatalf("produces did not link the ticket: %s", added.Body.String())
	}
	if strings.Contains(added.Body.String(), `href="/scope/wc/designs/wc-m4np"`) {
		t.Fatalf("produces linked a design inspect: %s", added.Body.String())
	}

	beforeUnknown, err := os.ReadFile(draft)
	if err != nil {
		t.Fatal(err)
	}
	unknown := doPost(s, "/scope/wc/designs/produces", url.Values{
		"id": {"wc-ab2c"}, "op": {"add"}, "target": {"wc-k7hn"},
	})
	if unknown.Code != http.StatusBadRequest {
		t.Fatalf("unknown produces = %d %s", unknown.Code, unknown.Body.String())
	}
	afterUnknown, err := os.ReadFile(draft)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterUnknown) != string(beforeUnknown) {
		t.Fatalf("unknown produces wrote:\n%s", afterUnknown)
	}

	removed := mustFollow(t, s, doPost(s, "/scope/wc/designs/produces", url.Values{
		"id": {"wc-ab2c"}, "op": {"remove"}, "target": {"wc-m4np"},
	}))
	if !strings.Contains(removed.Body.String(), `<h2>produces</h2>`) || !strings.Contains(removed.Body.String(), "none") {
		t.Fatalf("remove did not clear produces: %s", removed.Body.String())
	}
	if strings.Contains(fenceText(t, draft), "produces:") {
		t.Fatalf("last produces removal kept the key:\n%s", fenceText(t, draft))
	}

	edit := do(s, "/scope/wc/designs/wc-ab2c/edit")
	if edit.Code != http.StatusOK {
		t.Fatalf("edit = %d %s", edit.Code, edit.Body.String())
	}
	base := inspectBase(t, edit.Body.String())
	beforeBody, err := os.ReadFile(draft)
	if err != nil {
		t.Fatal(err)
	}
	_, oldBody, ok := frontmatter.Split(beforeBody)
	if !ok {
		t.Fatal("fence")
	}
	prefix := beforeBody[:len(beforeBody)-len(oldBody)]
	saved := mustFollow(t, s, doPost(s, "/scope/wc/designs/body", url.Values{
		"id": {"wc-ab2c"}, "title": {"Renamed shape"}, "body": {"fresh **copy**\n"}, "base": {base},
	}))
	if !strings.Contains(saved.Body.String(), `<h1 id="renamed-shape">Renamed shape</h1>`) || !strings.Contains(saved.Body.String(), "<strong>copy</strong>") {
		t.Fatalf("saved body: %s", saved.Body.String())
	}
	written, err := os.ReadFile(draft)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(written, prefix) {
		t.Fatalf("fence bytes changed:\n%q\n---\n%q", prefix, written)
	}
	matches, err := filepath.Glob(filepath.Join(dir, scopefile.DesignDir, "wc-ab2c-shape.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("body edit renamed the file: %v", matches)
	}

	board := do(s, "/scope/wc").Body.String()
	if strings.Contains(board, "Designfish") || strings.Contains(board, "wc-ab2c") || strings.Contains(board, "Renamed shape") {
		t.Fatalf("kanban listed a design: %s", board)
	}
	if !strings.Contains(board, "Ticketfish") {
		t.Fatalf("kanban lost the ticket: %s", board)
	}
	hits := do(s, "/search?q=Designfish&scope=wc")
	if hits.Code != http.StatusOK || !strings.Contains(hits.Body.String(), "No matches.") {
		t.Fatalf("search hit a design: %d %s", hits.Code, hits.Body.String())
	}
	graph := do(s, "/brief/depends?scope=wc").Body.String()
	if strings.Contains(graph, "Designfish") || strings.Contains(graph, "wc-ab2c") || strings.Contains(graph, "Renamed shape") {
		t.Fatalf("depends forest listed a design: %s", graph)
	}
}

func TestDesignEditKeepsTextBeforeTitle(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	addDesign(t, dir, "wc-cd3e", "plain", "draft", "# Plain\n\nbody\n", "")
	path := addDesign(t, dir, "wc-ab2c", "shape", "draft", "See the notes below.\n\n# Shape\n\nThe sockets.\n", "")
	s := mustServer(t, app)

	plain := do(s, "/scope/wc/designs/wc-cd3e/edit")
	if plain.Code != http.StatusOK || strings.Contains(plain.Body.String(), `name="lead"`) {
		t.Fatalf("heading-first edit = %d %s", plain.Code, plain.Body.String())
	}

	edit := do(s, "/scope/wc/designs/wc-ab2c/edit")
	if edit.Code != http.StatusOK {
		t.Fatalf("edit = %d %s", edit.Code, edit.Body.String())
	}
	page := edit.Body.String()
	if !strings.Contains(page, `name="lead"`) || !strings.Contains(page, "See the notes below.") {
		t.Fatalf("edit hid the text above the heading: %s", page)
	}
	if strings.Contains(textareaBrowserValue(t, page), "See the notes below.") {
		t.Fatalf("body field swallowed the lead: %s", page)
	}
	base := inspectBase(t, page)
	saved := mustFollow(t, s, doPost(s, "/scope/wc/designs/body", url.Values{
		"id": {"wc-ab2c"}, "title": {"Shape"}, "lead": {"See the notes below.\n\n"}, "body": {"\nThe sockets.\n"}, "base": {base},
	}))
	if !strings.Contains(saved.Body.String(), "See the notes below.") || !strings.Contains(saved.Body.String(), "The sockets.") {
		t.Fatalf("saved page dropped the lead: %s", saved.Body.String())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "See the notes below.\n\n# Shape\n\nThe sockets.\n") {
		t.Fatalf("file = %s", got)
	}
}

func TestDesignProducesUnresolvedIsNotALink(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	addDesign(t, dir, "wc-ab2c", "shape", "draft", "# Shape\n", "produces: [wc-k7hn]\n")
	s := mustServer(t, app)
	body := do(s, "/scope/wc/designs/wc-ab2c").Body.String()
	if strings.Contains(body, `href="/scope/wc/wc-k7hn"`) {
		t.Fatalf("unresolved produces is a link: %s", body)
	}
	if !strings.Contains(body, `class="unresolved">wc-k7hn</span>`) {
		t.Fatalf("unresolved produces is not shown: %s", body)
	}
}

func TestDesignProducesScalarIsNotCalledEmpty(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	addDesign(t, dir, "wc-ab2c", "shape", "draft", "# Shape\n", "produces: wc-m4np\n")
	addDesign(t, dir, "wc-cd3e", "plain", "draft", "# Plain\n", "")
	s := mustServer(t, app)

	broken := do(s, "/scope/wc/designs/wc-ab2c").Body.String()
	if !strings.Contains(broken, "produces is not a string list") {
		t.Fatalf("missing banner: %s", broken)
	}
	if strings.Contains(broken, `class="empty">none</p>`) {
		t.Fatalf("scalar produces called empty: %s", broken)
	}
	if strings.Contains(broken, `name="op" value="add"`) {
		t.Fatalf("scalar produces can be rewritten from the page: %s", broken)
	}

	empty := do(s, "/scope/wc/designs/wc-cd3e").Body.String()
	if !strings.Contains(empty, `class="empty">none</p>`) {
		t.Fatalf("absent produces did not say none: %s", empty)
	}
}

func TestDesignBodyStaleBase(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	path := addDesign(t, dir, "wc-ab2c", "shape", "draft", "# Shape\n\nhello\n", "")
	s := mustServer(t, app)
	base := inspectBase(t, do(s, "/scope/wc/designs/wc-ab2c/edit").Body.String())
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(before, []byte("changed\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	w := doPost(s, "/scope/wc/designs/body", url.Values{
		"id": {"wc-ab2c"}, "title": {"Other"}, "body": {"nope\n"}, "base": {base},
	})
	if w.Code != http.StatusConflict {
		t.Fatalf("stale base = %d %s", w.Code, w.Body.String())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(before)+"changed\n" {
		t.Fatalf("stale base wrote: %s", got)
	}
}

func TestDesignListOrdersByIDWhenChangedIsAbsent(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	writeDesign(t, dir, "wc-ab2c", "newer", "draft", "2026-02-01T00:00:00Z", "# Newer\n")
	writeDesign(t, dir, "wc-m4np", "older", "draft", "2026-01-01T00:00:00Z", "# Older\n")
	s := mustServer(t, app)

	body := do(s, "/scope/wc/designs").Body.String()
	olderAt := strings.Index(body, "wc-m4np")
	newerAt := strings.Index(body, "wc-ab2c")
	if olderAt < 0 || newerAt < 0 || newerAt > olderAt {
		t.Fatalf("absent changed must tie-break by id, not created:\n%s", body)
	}
}

func TestDesignDrivenMarkAndBodyNotice(t *testing.T) {
	app := newTestApp(t)
	dir, repo := initDrivenScope(t, app, "wc")
	path := writeDesign(t, dir, "wc-ab2c", "shape", "draft", "2026-01-01T00:00:00Z", "# Shape\n\nhello\n")
	pushOrigin(t, repo)
	before := testgit.Combined(t, repo, "rev-parse", "HEAD")
	s := mustServer(t, app)

	marked := doPost(s, "/scope/wc/designs/mark", url.Values{"id": {"wc-ab2c"}, "status": {"accepted"}})
	if marked.Code != http.StatusSeeOther {
		t.Fatalf("mark: %d %s", marked.Code, marked.Body.String())
	}
	loc := marked.Header().Get("Location")
	if !strings.Contains(loc, "sync_needed=") {
		t.Fatalf("mark location missing sync_needed: %s", loc)
	}
	afterMark := testgit.Combined(t, repo, "rev-parse", "HEAD")
	if afterMark == before {
		t.Fatal("mark did not self-commit")
	}
	page := do(s, loc)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "sync_needed:") {
		t.Fatalf("mark banner: %d %s", page.Code, page.Body.String())
	}

	base := inspectBase(t, do(s, "/scope/wc/designs/wc-ab2c/edit").Body.String())
	saved := doPost(s, "/scope/wc/designs/body", url.Values{
		"id": {"wc-ab2c"}, "title": {"Renamed"}, "body": {"edited\n"}, "base": {base},
	})
	if saved.Code != http.StatusSeeOther {
		t.Fatalf("body: %d %s", saved.Code, saved.Body.String())
	}
	loc = saved.Header().Get("Location")
	if !strings.Contains(loc, "sync_needed=") {
		t.Fatalf("body location missing sync_needed: %s", loc)
	}
	if testgit.Combined(t, repo, "rev-parse", "HEAD") != afterMark {
		t.Fatal("body edit self-committed")
	}
	page = do(s, loc)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "sync_needed:") {
		t.Fatalf("body banner: %d %s", page.Code, page.Body.String())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "# Renamed\n") {
		t.Fatalf("body was not saved: %s", got)
	}
}

func TestDesignListOrdersByStatusThenChanged(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "ds")
	addDesign(t, dir, "ds-ab2c", "old-draft", "draft", "# Old draft\n", "changed: 2026-01-01T00:00:00Z\n")
	addDesign(t, dir, "ds-cd3e", "new-draft", "draft", "# New draft\n", "changed: 2026-06-01T00:00:00Z\n")
	addDesign(t, dir, "ds-ef4g", "accepted", "accepted", "# Accepted\n", "changed: 2026-12-01T00:00:00Z\n")
	addDesign(t, dir, "ds-gh56", "split", "decomposed", "# Split\n", "changed: 2026-09-01T00:00:00Z\n")
	addDesign(t, dir, "ds-jk78", "replaced", "superseded", "# Replaced\n", "changed: 2026-02-01T00:00:00Z\n")
	addDesign(t, dir, "ds-mn9p", "hold", "hold", "# Hold\n", "changed: 2026-01-01T00:00:00Z\n")
	addDesign(t, dir, "ds-qr2s", "parked", "parked", "# Parked\n", "changed: 2026-12-01T00:00:00Z\n")
	s := mustServer(t, app)

	body := do(s, "/scope/ds/designs").Body.String()
	table, _, _ := strings.Cut(body, `<section class="broken">`)
	want := []string{"ds-cd3e", "ds-ab2c", "ds-ef4g", "ds-gh56", "ds-jk78", "ds-mn9p", "ds-qr2s"}
	prev := -1
	for _, id := range want {
		at := strings.Index(table, id)
		if at < 0 || at < prev {
			t.Fatalf("%s out of order in %s", id, table)
		}
		prev = at
	}
}

func TestDesignLess(t *testing.T) {
	newer := &index.Design{ID: "wc-cd3e", Status: "draft", Changed: "2026-06-01T00:00:00Z"}
	older := &index.Design{ID: "wc-ab2c", Status: "draft", Changed: "2026-01-01T00:00:00Z"}
	bare := &index.Design{ID: "wc-ef4g", Status: "draft"}
	bad := &index.Design{ID: "wc-gh56", Status: "draft", Changed: "not-a-time"}
	same := &index.Design{ID: "wc-jk78", Status: "draft", Changed: "2026-06-01T00:00:00Z"}
	accepted := &index.Design{ID: "wc-mn9p", Status: "accepted", Changed: "2026-12-01T00:00:00Z"}
	hold := &index.Design{ID: "wc-qr2s", Status: "hold", Changed: "2026-12-01T00:00:00Z"}
	parked := &index.Design{ID: "wc-tu4v", Status: "parked", Changed: "2026-01-01T00:00:00Z"}
	if !designLess(newer, older) || designLess(older, newer) {
		t.Fatal("newer changed should lead the same status")
	}
	if !designLess(older, bare) || !designLess(older, bad) {
		t.Fatal("a real changed instant should lead a missing or unparseable one")
	}
	if !designLess(newer, same) || designLess(same, newer) {
		t.Fatal("equal changed should break ties by id")
	}
	if !designLess(older, accepted) || designLess(accepted, newer) {
		t.Fatal("draft should lead accepted")
	}
	if !designLess(hold, parked) || designLess(parked, hold) {
		t.Fatal("unknown statuses should clump by name after the catalogue")
	}
	if designLess(hold, accepted) {
		t.Fatal("an unknown status should follow the catalogue")
	}
}

func writeDesign(t *testing.T, dir, id, slug, status, created, body string) string {
	t.Helper()
	target := filepath.Join(dir, scopefile.DesignDir)
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(target, id+"-"+slug+".md")
	fm := "---\nid: " + id + "\nstatus: " + status + "\ncreated: " + created + "\n---\n"
	if err := os.WriteFile(path, []byte(fm+body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func fenceText(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	interior, _, ok := frontmatter.Split(raw)
	if !ok {
		t.Fatalf("no fence in %s", path)
	}
	return string(interior)
}
