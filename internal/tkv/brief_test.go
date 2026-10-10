package tkv

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBriefScope(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	writeBriefTicket(t, dir, "wc-ab2c", "old-draft", "draft", "a0", "2026-01-01T00:00:00Z", "# Old draft\n", "")
	writeBriefTicket(t, dir, "wc-cd3e", "new-draft", "draft", "a1", "2026-06-01T00:00:00Z", "# New draft\n", "")
	addTicket(t, dir, "wc-ef4g", "doing", "in-progress", "a2", "# Doing\n", false, "tags: [layout]\n")
	addTicket(t, dir, "wc-gh56", "stuck", "blocked", "a3", "# Stuck\n", false, "")
	addTicket(t, dir, "wc-jk78", "queued", "todo", "a4", "# Queued\n", false, "tags: [layout]\n")
	addTicket(t, dir, "wc-mn9p", "finished", "done", "a5", "# Finished\n", true, "tags: [layout]\n")
	addTicket(t, dir, "wc-qr2s", "needs-done", "todo", "a6", "# Needs done\n", false, "depends: [wc-mn9p]\n")
	addTicket(t, dir, "wc-tu4v", "needs-draft", "todo", "a7", "# Needs draft\n", false, "depends: [wc-ab2c]\n")
	addTicket(t, dir, "wc-za8b", "shipped", "done", "a8", "# Shipped\n", true, "depends: [wc-ab2c]\n")
	addDesign(t, dir, "wc-vw5x", "open-design", "accepted", "# Open design\n", "")
	addDesign(t, dir, "wc-xy6z", "split-design", "decomposed", "# Split design\n", "produces: [wc-jk78]\n")
	s := mustServer(t, app)

	body := do(s, "/brief?scope=wc").Body.String()
	if !strings.Contains(body, "Doing") || !strings.Contains(body, "Stuck") || !strings.Contains(body, "no open depends") {
		t.Fatalf("now: %s", body)
	}
	if !strings.Contains(body, "3 todo.") {
		t.Fatalf("todo count: %s", body)
	}
	oldAt := strings.Index(body, "Old draft")
	newAt := strings.Index(body, "New draft")
	if oldAt < 0 || newAt < 0 || oldAt > newAt {
		t.Fatalf("drafts should be oldest first:\n%s", body)
	}
	if strings.Contains(body, "Finished") || strings.Contains(body, "Shipped") {
		t.Fatalf("done ticket leaked onto the brief: %s", body)
	}
	wait := sectionText(t, body, "waiting")
	if strings.Contains(wait, "Needs done") || strings.Contains(wait, "Finished") {
		t.Fatalf("waiting showed a depends into done: %s", wait)
	}
	if !strings.Contains(wait, "Needs draft") || !strings.Contains(wait, "Old draft") || !strings.Contains(wait, "<svg") {
		t.Fatalf("waiting: %s", wait)
	}
	if !strings.Contains(wait, `href="/brief/depends?scope=wc"`) {
		t.Fatalf("waiting missing depends graph: %s", wait)
	}
	tags := sectionText(t, body, "tags")
	if !strings.Contains(tags, `layout<span class="n">2</span>`) {
		t.Fatalf("tags: %s", tags)
	}
	designs := sectionText(t, body, "designs")
	if !strings.Contains(designs, "Open design") || strings.Contains(designs, "Split design") {
		t.Fatalf("designs: %s", designs)
	}

	unknown := do(s, "/brief?scope=zz")
	if unknown.Code != 404 {
		t.Fatalf("unknown scope = %d", unknown.Code)
	}
}

func TestBriefCustomStatuses(t *testing.T) {
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	if err := os.WriteFile(filepath.Join(dir, "tk.cue"), []byte(
		"name: \"wc\"\nautoCommit: false\nstatuses: {\n  qa: { category: \"active\" }\n  triaged: { category: \"active\" }\n  icebox: { category: \"backlog\" }\n  shipped: { category: \"done\" }\n}\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	addTicket(t, dir, "wc-ab2c", "doing", "in-progress", "a0", "# Doing\n", false, "")
	addTicket(t, dir, "wc-cd3e", "queue", "qa", "a1", "# Queue check\n", false, "")
	addTicket(t, dir, "wc-ef4g", "check", "triaged", "a2", "# Check layout\n", false, "")
	addTicket(t, dir, "wc-gh56", "later", "icebox", "a3", "# Later\n", false, "")
	addTicket(t, dir, "wc-jk78", "gone", "shipped", "a4", "# Gone\n", true, "")
	addTicket(t, dir, "wc-mn9p", "odd", "weird", "a5", "# Odd\n", false, "")
	s := mustServer(t, app)

	body := do(s, "/brief?scope=wc").Body.String()
	now := sectionText(t, body, "now")
	doing := strings.Index(now, "Doing")
	qa := strings.Index(now, "Queue check")
	triaged := strings.Index(now, "Check layout")
	if doing < 0 || qa < doing || triaged < qa {
		t.Fatalf("now order: %s", now)
	}
	if strings.Contains(now, "Later") || strings.Contains(now, "Gone") || strings.Contains(now, "Odd") || strings.Contains(now, "todo.") {
		t.Fatalf("now mixed in other buckets: %s", now)
	}
	backlog := sectionText(t, body, "backlog")
	if !strings.Contains(backlog, "Later") || strings.Contains(backlog, "Gone") {
		t.Fatalf("backlog: %s", backlog)
	}
	if strings.Contains(body, ">Gone</a>") || strings.Contains(body, ">Odd</a>") {
		t.Fatalf("finished or unknown status leaked: %s", body)
	}
	if !strings.Contains(sectionText(t, body, "waiting"), `href="/brief/depends?scope=wc"`) {
		t.Fatalf("empty waiting missing depends graph: %s", body)
	}
}

func TestBriefBacklogCap(t *testing.T) {
	ids := []string{
		"wc-ab2c", "wc-cd3e", "wc-ef4g", "wc-gh56", "wc-jk78",
		"wc-mn9p", "wc-qr2s", "wc-tu4v", "wc-vw5x",
	}
	if len(ids) != briefBacklogShown+1 {
		t.Fatalf("need one ticket past the cap of %d", briefBacklogShown)
	}
	app := newTestApp(t)
	dir := initScope(t, app, "wc")
	for i, id := range ids {
		title := fmt.Sprintf("Slot %d", i+1)
		created := fmt.Sprintf("2026-%02d-01T00:00:00Z", i+1)
		writeBriefTicket(t, dir, id, fmt.Sprintf("slot-%d", i+1), "backlog", fmt.Sprintf("a%d", i), created, "# "+title+"\n", "")
	}
	s := mustServer(t, app)

	backlog := sectionText(t, do(s, "/brief?scope=wc").Body.String(), "backlog")
	want := fmt.Sprintf("%d backlog, oldest %d", len(ids), briefBacklogShown)
	if !strings.Contains(backlog, want) {
		t.Fatalf("cap sentence: %s", backlog)
	}
	if !strings.Contains(backlog, "Slot 1") || !strings.Contains(backlog, fmt.Sprintf("Slot %d", briefBacklogShown)) {
		t.Fatalf("oldest rows missing: %s", backlog)
	}
	if strings.Contains(backlog, fmt.Sprintf("Slot %d", briefBacklogShown+1)) {
		t.Fatalf("backlog showed past the cap: %s", backlog)
	}
}

func TestBriefCrossScopeFinished(t *testing.T) {
	app := newTestApp(t)
	wc := initScope(t, app, "wc")
	ot := initScope(t, app, "ot")
	if err := os.WriteFile(filepath.Join(wc, "tk.cue"), []byte(
		"name: \"wc\"\nautoCommit: false\nstatuses: { hold: { category: \"done\" } }\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ot, "tk.cue"), []byte(
		"name: \"ot\"\nautoCommit: false\nstatuses: {\n  shipped: { category: \"done\" }\n  hold: { category: \"active\" }\n}\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	addTicket(t, ot, "ot-cd3e", "ship", "shipped", "a0", "# Ship the API\n", true, "")
	addTicket(t, ot, "ot-gh56", "holding", "hold", "a1", "# Still holding\n", false, "")
	addTicket(t, wc, "wc-ab2c", "needs-api", "todo", "a0", "# Needs the API\n", false, "depends: [ot-cd3e]\n")
	addTicket(t, wc, "wc-ef4g", "needs-hold", "todo", "a1", "# Needs a hold\n", false, "depends: [ot-gh56]\n")
	s := mustServer(t, app)

	wait := sectionText(t, do(s, "/brief?scope=wc").Body.String(), "waiting")
	if strings.Contains(wait, "Ship the API") || strings.Contains(wait, "Needs the API") {
		t.Fatalf("arrow into a foreign finished status stayed: %s", wait)
	}
	if !strings.Contains(wait, "Needs a hold") || !strings.Contains(wait, "Still holding") {
		t.Fatalf("arrow into foreign active work dropped: %s", wait)
	}
}

func writeBriefTicket(t *testing.T, dir, id, slug, status, order, created, body, extra string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	fm := "---\nid: " + id + "\nstatus: " + status + "\norder: \"" + order + "\"\ncreated: " + created + "\n" + extra + "---\n"
	if err := os.WriteFile(filepath.Join(dir, id+"-"+slug+".md"), []byte(fm+body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func sectionText(t *testing.T, body, label string) string {
	t.Helper()
	open := `<section aria-label="` + label + `">`
	i := strings.Index(body, open)
	if i < 0 {
		t.Fatalf("missing section %s", label)
	}
	rest := body[i+len(open):]
	j := strings.Index(rest, "</section>")
	if j < 0 {
		t.Fatalf("unclosed section %s", label)
	}
	return rest[:j]
}
