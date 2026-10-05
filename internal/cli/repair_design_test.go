package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/token"
)

func writeTicket(t *testing.T, dir, id, slug, status, order, created, extra, body string, archived bool) string {
	t.Helper()
	target := dir
	if archived {
		target = filepath.Join(dir, "archive")
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	fm := "---\nid: " + id + "\nstatus: " + status + "\norder: \"" + order + "\"\ncreated: " + created + "\n" + extra + "---\n" + body
	path := filepath.Join(target, id+"-"+slug+".md")
	if err := os.WriteFile(path, []byte(fm), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeDesignRaw(t *testing.T, dir, base, body string) string {
	t.Helper()
	target := filepath.Join(dir, "design")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(target, base)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// mentionsID reports whether text names full as its own id, not as a prefix of a longer id.
func mentionsID(text, full string) bool {
	rest := text
	for {
		i := strings.Index(rest, full)
		if i < 0 {
			return false
		}
		end := i + len(full)
		if end >= len(rest) || !shortChar(rest[end]) {
			return true
		}
		rest = rest[end:]
	}
}

func shortChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= '2' && b <= '9')
}

func TestRepairTwoDesignsShareShort(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	t.Setenv("TK_SCOPE", "wc")
	writeDesign(t, dir, "wc-ab2c", "older", "draft", "2020-01-01T00:00:00Z", "Older")
	writeDesign(t, dir, "wc-ab2c", "newer", "draft", "2026-01-01T00:00:00Z", "Newer")

	doc, _, err := run(t, app, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc, token.DesignID+" wc-ab2c") {
		t.Fatalf("doctor = %q", doc)
	}
	out, _, err := run(t, app, "repair")
	if err != nil {
		t.Fatalf("repair: %v\n%s", err, out)
	}
	kept, err := os.ReadFile(filepath.Join(dir, "design", "wc-ab2c-older.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(kept), "id: wc-ab2c") {
		t.Fatalf("keeper id: %q", kept)
	}
	if _, err := os.Stat(filepath.Join(dir, "design", "wc-ab2c-newer.md")); !os.IsNotExist(err) {
		t.Fatal("newer design must be renamed")
	}
	loser, err := os.ReadFile(filepath.Join(dir, "design", "wc-ab2ca-newer.md"))
	if err != nil {
		t.Fatalf("loser must stay in design/: %v", err)
	}
	if !strings.Contains(string(loser), "id: wc-ab2ca") || strings.Contains(string(loser), "order:") {
		t.Fatalf("loser fence: %q", loser)
	}
	doc, _, err = run(t, app, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(doc, token.DesignID+" wc-ab2c") || strings.Contains(doc, token.DuplicateID) {
		t.Fatalf("doctor after repair = %q", doc)
	}
}

func TestRepairDesignAndTicketExtendsOneSide(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	t.Setenv("TK_SCOPE", "wc")
	ticketPath := writeTicket(t, dir, "wc-ab2c", "ticket", "todo", "a0", "2026-06-01T00:00:00Z", "", "# Ticket\n", false)
	writeDesign(t, dir, "wc-ab2c", "shape", "draft", "2020-01-01T00:00:00Z", "Shape")

	if _, _, err := run(t, app, "repair"); err != nil {
		t.Fatalf("repair: %v", err)
	}
	if _, err := os.Stat(ticketPath); !os.IsNotExist(err) {
		t.Fatal("ticket must be extended off its old path")
	}
	designText, err := os.ReadFile(filepath.Join(dir, "design", "wc-ab2c-shape.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(designText), "order:") || !strings.Contains(string(designText), "id: wc-ab2c") {
		t.Fatalf("design fence: %q", designText)
	}
	if _, err := os.Stat(filepath.Join(dir, "design", "wc-ab2ca-ticket.md")); !os.IsNotExist(err) {
		t.Fatal("ticket must not move into design/")
	}
	if _, err := os.Stat(filepath.Join(dir, "wc-ab2c-ticket.md")); !os.IsNotExist(err) {
		t.Fatal("ticket must be extended")
	}
	ticket, err := os.ReadFile(filepath.Join(dir, "wc-ab2ca-ticket.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ticket), "id: wc-ab2ca") {
		t.Fatalf("ticket fence: %q", ticket)
	}
}

func TestRepairTicketKeepsSharedIDEdges(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	t.Setenv("TK_SCOPE", "wc")
	writeTicket(t, dir, "wc-ab2c", "ticket", "todo", "a0", "2020-01-01T00:00:00Z", "", "# Ticket\n", false)
	writeDesignRaw(t, dir, "wc-ab2c-shape.md", "---\nid: wc-ab2c\nstatus: draft\ncreated: 2026-01-01T00:00:00Z\nproduces: [wc-ab2c]\n---\n# Shape\n")
	writeTicket(t, dir, "wc-de34", "ref", "todo", "a1", "2026-01-01T00:00:00Z", "depends: [wc-ab2c]\n", "# Ref\n", false)

	out, _, err := run(t, app, "repair")
	if err != nil {
		t.Fatalf("repair: %v\n%s", err, out)
	}
	ref, err := os.ReadFile(filepath.Join(dir, "wc-de34-ref.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !mentionsID(string(ref), "wc-ab2c") || !mentionsID(string(ref), "wc-de34") || strings.Contains(string(ref), "wc-ab2ca") {
		t.Fatalf("depends must stay on the kept id: %q", ref)
	}
	shape, err := os.ReadFile(filepath.Join(dir, "design", "wc-ab2ca-shape.md"))
	if err != nil {
		t.Fatalf("design must be the loser: %v", err)
	}
	if !mentionsID(string(shape), "wc-ab2c") {
		t.Fatalf("produces must stay on the kept id: %q", shape)
	}
	for _, want := range []string{"wc-de34 depends wc-ab2c", "produces wc-ab2c"} {
		if !strings.Contains(out, "edge_verify:") || !strings.Contains(out, want) {
			t.Fatalf("repair stdout missing %q: %q", want, out)
		}
	}
}

func TestRepairDesignKeepsSharedIDOneTicket(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	t.Setenv("TK_SCOPE", "wc")
	writeDesignRaw(t, dir, "wc-ab2c-shape.md", "---\nid: wc-ab2c\nstatus: draft\ncreated: 2020-01-01T00:00:00Z\nproduces: [wc-ab2c, wc-zzzz]\n---\n# Shape\n")
	writeTicket(t, dir, "wc-ab2c", "ticket", "done", "a0", "2026-06-01T00:00:00Z", "", "# Ticket\n", true)
	writeTicket(t, dir, "wc-de34", "ref", "todo", "a1", "2026-01-01T00:00:00Z", "depends: [wc-ab2c]\nrelated: [wc-ab2c]\n", "# Ref\n", false)

	out, _, err := run(t, app, "repair")
	if err != nil {
		t.Fatalf("repair: %v\n%s", err, out)
	}
	if strings.Contains(out, "edge_verify:") {
		t.Fatalf("moved links must not be edge_verify, got %q", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "archive", "wc-ab2ca-ticket.md")); err != nil {
		t.Fatalf("ticket loser must stay in archive/: %v files", err)
	}
	ref, _ := os.ReadFile(filepath.Join(dir, "wc-de34-ref.md"))
	if !strings.Contains(string(ref), "wc-ab2ca") || mentionsID(string(ref), "wc-ab2c") {
		t.Fatalf("depends and related must name the new ticket id: %q", ref)
	}
	shape, _ := os.ReadFile(filepath.Join(dir, "design", "wc-ab2c-shape.md"))
	text := string(shape)
	if !strings.Contains(text, "wc-ab2ca") || !strings.Contains(text, "wc-zzzz") || mentionsID(text, "wc-ab2c") && strings.Count(text, "id: wc-ab2c") != 1 {
		t.Fatalf("produces: %q", text)
	}
	if mentionsID(strings.Replace(text, "id: wc-ab2c", "", 1), "wc-ab2c") {
		t.Fatalf("produces still names the old id: %q", text)
	}
	doc, _, err := run(t, app, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc, token.ProducesDangling) || !strings.Contains(doc, "wc-zzzz") {
		t.Fatalf("dangling entry must stay: %q", doc)
	}
}

func TestRepairDesignKeepsSharedIDTwoTickets(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	t.Setenv("TK_SCOPE", "wc")
	writeDesign(t, dir, "wc-ab2c", "shape", "draft", "2019-01-01T00:00:00Z", "Shape")
	writeTicket(t, dir, "wc-ab2c", "older", "todo", "a0", "2020-01-01T00:00:00Z", "", "# Older\n", false)
	writeTicket(t, dir, "wc-ab2c", "newer", "todo", "a1", "2026-01-01T00:00:00Z", "", "# Newer\n", false)
	writeTicket(t, dir, "wc-de34", "ref", "todo", "a2", "2026-01-01T00:00:00Z", "depends: [wc-ab2c]\n", "# Ref\n", false)
	writeDesignRaw(t, dir, "wc-gh56-list.md", "---\nid: wc-gh56\nstatus: draft\ncreated: 2026-01-01T00:00:00Z\nproduces: [wc-ab2c]\n---\n# List\n")

	out, _, err := run(t, app, "repair")
	if err != nil {
		t.Fatalf("repair: %v\n%s", err, out)
	}
	ref, _ := os.ReadFile(filepath.Join(dir, "wc-de34-ref.md"))
	list, _ := os.ReadFile(filepath.Join(dir, "design", "wc-gh56-list.md"))
	if !mentionsID(string(ref), "wc-ab2c") || strings.Contains(string(ref), "wc-ab2ca") {
		t.Fatalf("depends must stay: %q", ref)
	}
	if !mentionsID(string(list), "wc-ab2c") || strings.Contains(string(list), "wc-ab2ca") {
		t.Fatalf("produces must stay: %q", list)
	}
	if strings.Count(out, "edge_verify:") != 2 {
		t.Fatalf("want edge_verify for depends and produces, got %q", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "design", "wc-ab2c-shape.md")); err != nil {
		t.Fatal("design keeper must stay")
	}
	if _, err := os.Stat(filepath.Join(dir, "wc-ab2ca-older.md")); err != nil {
		t.Fatal("older ticket must be extended first")
	}
	if _, err := os.Stat(filepath.Join(dir, "wc-ab2cb-newer.md")); err != nil {
		t.Fatal("newer ticket must be extended second")
	}
}

func TestRepairSharedIDOtherScopeStays(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	other := initScope(t, app, "api")
	t.Setenv("TK_SCOPE", "wc")
	writeDesign(t, dir, "wc-ab2c", "shape", "draft", "2020-01-01T00:00:00Z", "Shape")
	writeTicket(t, dir, "wc-ab2c", "ticket", "todo", "a0", "2026-06-01T00:00:00Z", "", "# Ticket\n", false)
	writeDesignRaw(t, other, "api-xy99-remote.md", "---\nid: api-xy99\nstatus: draft\ncreated: 2026-01-01T00:00:00Z\nproduces: [wc-ab2c]\n---\n# Remote\n")

	out, _, err := run(t, app, "repair")
	if err != nil {
		t.Fatalf("repair: %v\n%s", err, out)
	}
	remote, err := os.ReadFile(filepath.Join(other, "design", "api-xy99-remote.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !mentionsID(string(remote), "wc-ab2c") || strings.Contains(string(remote), "wc-ab2ca") {
		t.Fatalf("other scope must stay: %q", remote)
	}
	if !strings.Contains(out, "edge_verify: api-xy99 produces wc-ab2c") {
		t.Fatalf("stdout = %q", out)
	}
}

func TestRepairBrokenDesignFenceStays(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	t.Setenv("TK_SCOPE", "wc")
	broken := writeDesignRaw(t, dir, "wc-ab2c-broken.md", "---\nid: wc-ab2c\nstatus: draft\ncreated: 2020-01-01T00:00:00Z\n# Broken\n")
	before, _ := os.ReadFile(broken)
	writeTicket(t, dir, "wc-ab2c", "ticket", "todo", "a0", "2026-01-01T00:00:00Z", "", "# Ticket\n", false)
	ticketBefore, _ := os.ReadFile(filepath.Join(dir, "wc-ab2c-ticket.md"))
	published := writeDesignRaw(t, dir, "wc-gh56-odd.md", "---\nid: wc-gh56\nstatus: published\ncreated: 2026-01-01T00:00:00Z\n---\n# Odd\n")
	publishedBefore, _ := os.ReadFile(published)

	doc, _, err := run(t, app, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc, token.DesignID+" wc-ab2c") || !strings.Contains(doc, token.ParseError) {
		t.Fatalf("doctor = %q", doc)
	}
	if _, _, err := run(t, app, "repair"); err != nil {
		t.Fatalf("repair: %v", err)
	}
	after, _ := os.ReadFile(broken)
	ticketAfter, _ := os.ReadFile(filepath.Join(dir, "wc-ab2c-ticket.md"))
	publishedAfter, _ := os.ReadFile(published)
	if string(after) != string(before) || string(ticketAfter) != string(ticketBefore) || string(publishedAfter) != string(publishedBefore) {
		t.Fatal("broken fence, its pair, and an unknown status must stay")
	}
	doc, _, err = run(t, app, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc, token.DesignID+" wc-ab2c") || !strings.Contains(doc, "unknown status") {
		t.Fatalf("warnings must survive repair: %q", doc)
	}
}

func TestRepairTwoTicketsAndDesignOneKeeper(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	t.Setenv("TK_SCOPE", "wc")
	writeDesign(t, dir, "wc-ab2c", "shape", "draft", "2019-01-01T00:00:00Z", "Shape")
	writeTicket(t, dir, "wc-ab2c", "older", "todo", "a0", "2020-01-01T00:00:00Z", "", "# Older\n", false)
	writeTicket(t, dir, "wc-ab2c", "newer", "todo", "a1", "2026-01-01T00:00:00Z", "", "# Newer\n", false)

	doc, _, err := run(t, app, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc, token.DesignID+" wc-ab2c") || strings.Contains(doc, token.DuplicateID) {
		t.Fatalf("before repair = %q", doc)
	}
	out, _, err := run(t, app, "repair")
	if err != nil {
		t.Fatalf("repair: %v\n%s", err, out)
	}
	if strings.Count(out, "repaired design id:") != 2 {
		t.Fatalf("one repair must extend both tickets, got %q", out)
	}
	if strings.Contains(out, "repaired duplicate id:") {
		t.Fatalf("design holder must not also repair as duplicate_id: %q", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "design", "wc-ab2c-shape.md")); err != nil {
		t.Fatal("older design must be kept over both tickets")
	}
	doc, _, err = run(t, app, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(doc, token.DesignID+" wc-ab2c") || strings.Contains(doc, token.DuplicateID+" wc-ab2c") {
		t.Fatalf("after repair = %q", doc)
	}
}

func TestRepairTicketOnlyCollisionIsDuplicateID(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	t.Setenv("TK_SCOPE", "wc")
	writeTicket(t, dir, "wc-ab2c", "alpha", "todo", "a0", "2020-01-01T00:00:00Z", "", "# A\n", false)
	writeTicket(t, dir, "wc-ab2c", "beta", "todo", "a1", "2026-01-01T00:00:00Z", "", "# B\n", false)
	writeDesign(t, dir, "wc-de34", "shape", "draft", "2026-01-01T00:00:00Z", "Shape")

	doc, _, err := run(t, app, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc, token.DuplicateID+" wc-ab2c") || strings.Contains(doc, token.DesignID) {
		t.Fatalf("before = %q", doc)
	}
	out, _, err := run(t, app, "repair")
	if err != nil {
		t.Fatalf("repair: %v\n%s", err, out)
	}
	if !strings.Contains(out, "repaired duplicate id: wc-ab2c -> wc-ab2ca") {
		t.Fatalf("stdout = %q", out)
	}
	if strings.Contains(out, "repaired design id:") {
		t.Fatalf("ticket-only collision must not use design repair: %q", out)
	}
	doc, _, err = run(t, app, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(doc, token.DuplicateID) || strings.Contains(doc, token.DesignID) {
		t.Fatalf("after = %q", doc)
	}
}
