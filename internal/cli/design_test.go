package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/token"
)

func TestDesignCreateListMarkMetaAndBoardIsolation(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	t.Setenv("TK_SCOPE", "wc")
	addTicket(t, dir, "wc-m4np", "target", "todo", "a0", "# Target\n", false, "")

	out, errOut, err := run(t, app, "design", "create", "Shape the queue")
	if err != nil {
		t.Fatalf("create: %v stderr %q", err, errOut)
	}
	path := strings.TrimSpace(out)
	if filepath.Dir(path) != filepath.Join(dir, "design") {
		t.Fatalf("path = %q", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	if !strings.Contains(body, "status: draft") || !strings.Contains(body, "created:") {
		t.Fatalf("scaffold = %q", body)
	}
	if strings.Contains(body, "order:") || strings.Contains(body, "tags:") || strings.Contains(body, "produces:") {
		t.Fatalf("scaffold must omit order, tags, and produces: %q", body)
	}
	if !strings.HasSuffix(body, "# Shape the queue\n") {
		t.Fatalf("H1: %q", body)
	}
	fullID := designIDFromPath(t, path)
	if !strings.Contains(body, "id: "+fullID+"\n") {
		t.Fatalf("fence id = %q body %q", fullID, body)
	}

	// An untracked design occupies its short id for a later ticket create.
	short := strings.TrimPrefix(fullID, "wc-")
	held := filepath.Join(dir, "design", "wc-"+short+"-shape-the-queue.md")
	if held != path {
		t.Fatalf("slug path = %q want %q", path, held)
	}

	list, _, err := run(t, app, "design", "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(list, fullID+"\tdraft\tShape the queue\t"+path) {
		t.Fatalf("list = %q", list)
	}
	if _, _, err := run(t, app, "design", "ls"); err != nil {
		t.Fatalf("ls: %v", err)
	}

	marked, _, err := run(t, app, "design", "mark", "decomposed", fullID)
	if err != nil {
		t.Fatalf("mark: %v", err)
	}
	if strings.TrimSpace(marked) != path {
		t.Fatalf("mark path = %q", marked)
	}
	if filepath.Dir(strings.TrimSpace(marked)) != filepath.Join(dir, "design") {
		t.Fatal("mark must leave the file in design/")
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "status: decomposed") {
		t.Fatalf("status not updated: %q", got)
	}
	def, _, err := run(t, app, "design", "list")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(def, fullID) {
		t.Fatalf("default list must hide decomposed: %q", def)
	}
	all, _, err := run(t, app, "design", "list", "--all")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(all, fullID+"\tdecomposed\t") {
		t.Fatalf("--all = %q", all)
	}

	_, _, err = run(t, app, "design", "mark", "todo", fullID)
	if ExitCodeFromError(err) != exitUsage {
		t.Fatalf("ticket status must be usage, got %v", err)
	}
	got, _ = os.ReadFile(path)
	if !strings.Contains(string(got), "status: decomposed") {
		t.Fatal("usage error must not write")
	}

	added, _, err := run(t, app, "design", "meta", "add", fullID, "produces", "wc-m4np")
	if err != nil {
		t.Fatalf("meta add: %v", err)
	}
	if strings.TrimSpace(added) != path {
		t.Fatalf("meta path = %q", added)
	}
	if _, _, err := run(t, app, "design", "meta", "add", fullID, "produces", "wc-m4np"); err != nil {
		t.Fatalf("duplicate add: %v", err)
	}
	got, _ = os.ReadFile(path)
	if strings.Count(string(got), "wc-m4np") != 1 {
		t.Fatalf("duplicate produces: %q", got)
	}
	_, _, err = run(t, app, "design", "meta", "add", fullID, "produces", "wc-zzzz")
	if err == nil {
		t.Fatal("unknown ticket must refuse")
	}
	_, _, err = run(t, app, "design", "meta", "add", fullID, "produces", "m4np")
	if ExitCodeFromError(err) != exitUsage {
		t.Fatalf("short produces id must be usage, got %v", err)
	}
	if _, _, err := run(t, app, "design", "meta", "remove", fullID, "produces", "wc-m4np"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	got, _ = os.ReadFile(path)
	if strings.Contains(string(got), "produces") {
		t.Fatalf("last remove must drop the key: %q", got)
	}

	board, _, err := run(t, app, "list", "--all")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(board, fullID) {
		t.Fatalf("design must not appear on the board: %q", board)
	}
	search, _, err := run(t, app, "search", "Shape")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(search, fullID) || strings.Contains(search, "Shape the queue") {
		t.Fatalf("design must not be searchable: %q", search)
	}
	doc, _, err := run(t, app, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(doc, token.NonAllowlist) {
		t.Fatalf("well-formed design must not be residue: %q", doc)
	}
	q, _, err := run(t, app, "query", "select id from tickets")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(q, fullID) {
		t.Fatalf("design must not be indexed: %q", q)
	}
}

func TestDesignListSortAndMissingDir(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	t.Setenv("TK_SCOPE", "wc")
	out, _, err := run(t, app, "design", "list")
	if err != nil || out != "" {
		t.Fatalf("missing design/ list = %q err %v", out, err)
	}
	writeDesign(t, dir, "wc-ab2c", "later", "accepted", "2026-02-01T00:00:00Z", "Later")
	writeDesign(t, dir, "wc-de34", "earlier", "draft", "2026-01-01T00:00:00Z", "Earlier")
	writeDesign(t, dir, "wc-gh56", "same", "draft", "2026-01-01T00:00:00Z", "Same")
	writeDesign(t, dir, "wc-jk89", "done", "superseded", "2026-01-01T00:00:00Z", "Done")

	list, _, err := run(t, app, "design", "list")
	if err != nil {
		t.Fatal(err)
	}
	rows := lines(list)
	if len(rows) != 3 {
		t.Fatalf("default rows = %q", list)
	}
	if !strings.HasPrefix(rows[0], "wc-de34\t") || !strings.HasPrefix(rows[1], "wc-gh56\t") || !strings.HasPrefix(rows[2], "wc-ab2c\t") {
		t.Fatalf("sort = %q", list)
	}
	filtered, _, err := run(t, app, "design", "list", "superseded")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(filtered, "wc-jk89") || strings.Contains(filtered, "wc-de34") {
		t.Fatalf("positional filter = %q", filtered)
	}
}

func TestDesignUnknownStatusOnAllAndDoctor(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	t.Setenv("TK_SCOPE", "wc")
	writeDesign(t, dir, "wc-ab2c", "ghost", "published", "2026-01-01T00:00:00Z", "Ghost")
	writeDesign(t, dir, "wc-de34", "live", "draft", "2026-01-02T00:00:00Z", "Live")
	writeDesign(t, dir, "wc-gh56", "old", "superseded", "2026-01-03T00:00:00Z", "Old")

	def, _, err := run(t, app, "design", "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(def, "wc-de34") || strings.Contains(def, "wc-ab2c") || strings.Contains(def, "wc-gh56") {
		t.Fatalf("default list = %q", def)
	}

	all, _, err := run(t, app, "design", "list", "--all")
	if err != nil {
		t.Fatal(err)
	}
	rows := lines(all)
	if len(rows) != 3 ||
		!strings.HasPrefix(rows[0], "wc-ab2c\tpublished\t") ||
		!strings.HasPrefix(rows[1], "wc-de34\tdraft\t") ||
		!strings.HasPrefix(rows[2], "wc-gh56\tsuperseded\t") {
		t.Fatalf("--all = %q", all)
	}

	doc, errOut, err := run(t, app, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if strings.Contains(errOut, "no integrity issues") {
		t.Fatalf("doctor stderr = %q stdout %q", errOut, doc)
	}
	want := `schema_error: wc-ab2c has unknown status "published" (`
	if strings.Count(doc, token.SchemaError) != 1 || !strings.Contains(doc, want) || !strings.Contains(doc, "wc-ab2c-ghost.md") {
		t.Fatalf("doctor = %q", doc)
	}

	path := filepath.Join(dir, "design", "wc-ab2c-ghost.md")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_, stderr, err := run(t, app, "design", "mark", "published", "wc-ab2c")
	if ExitCodeFromError(err) != exitUsage {
		t.Fatalf("mark published = %v stderr %q", err, stderr)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("rejected mark must leave the design unchanged")
	}
}

func TestDesignListUnknownStatusExitsUsage(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	t.Setenv("TK_SCOPE", "wc")
	writeDesign(t, dir, "wc-ab2c", "live", "draft", "2026-01-01T00:00:00Z", "Live")
	writeDesign(t, dir, "wc-de34", "ghost", "published", "2026-01-02T00:00:00Z", "Ghost")

	cases := []struct {
		args []string
		word string
	}{
		{[]string{"design", "list", "bogus"}, "bogus"},
		{[]string{"design", "list", "draft", "nosuch"}, "nosuch"},
		{[]string{"design", "list", "--all", "published"}, "published"},
	}
	for _, c := range cases {
		out, _, err := run(t, app, c.args...)
		if ExitCodeFromError(err) != exitUsage {
			t.Fatalf("%v exit = %v", c.args, err)
		}
		if out != "" {
			t.Fatalf("%v stdout = %q", c.args, out)
		}
		want := fmt.Sprintf("%q is not a design status (draft, accepted, decomposed, superseded)", c.word)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%v err = %v", c.args, err)
		}
	}

	ok, _, err := run(t, app, "design", "list", "draft")
	if err != nil || !strings.Contains(ok, "wc-ab2c") || strings.Contains(ok, "wc-de34") {
		t.Fatalf("draft list = %q err %v", ok, err)
	}
}

func TestDesignListSortsCreatedByInstant(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	t.Setenv("TK_SCOPE", "wc")
	writeDesign(t, dir, "wc-ab2c", "utc", "draft", "2026-01-01T00:00:00Z", "UTC")
	writeDesign(t, dir, "wc-de34", "offset", "draft", "2026-01-01T08:00:00+10:00", "Offset")
	writeDesign(t, dir, "wc-gh56", "same", "draft", "2026-01-01T10:00:00+10:00", "Same")
	writeDesign(t, dir, "wc-jk89", "bad", "draft", "yesterday", "Bad")

	list, _, err := run(t, app, "design", "list")
	if err != nil {
		t.Fatal(err)
	}
	rows := lines(list)
	if len(rows) != 4 {
		t.Fatalf("rows = %q", list)
	}
	want := []string{"wc-jk89\t", "wc-de34\t", "wc-ab2c\t", "wc-gh56\t"}
	for i, prefix := range want {
		if !strings.HasPrefix(rows[i], prefix) {
			t.Fatalf("row %d = %q, list %q", i, rows[i], list)
		}
	}
}

func TestDesignGetUsesFenceIDLikeTickets(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	t.Setenv("TK_SCOPE", "wc")
	if err := os.MkdirAll(filepath.Join(dir, "design"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nid: wc-ab2c\nstatus: draft\ncreated: 2026-01-01T00:00:00Z\n---\n# Shape\n"
	if err := os.WriteFile(filepath.Join(dir, "design", "wc-zz9y-shape.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	out, _, err := run(t, app, "design", "get", "wc-ab2c")
	if err != nil {
		t.Fatalf("fence id must resolve, got %v", err)
	}
	if strings.TrimSpace(out) != filepath.Join(dir, "design", "wc-zz9y-shape.md") {
		t.Fatalf("get fence id = %q", out)
	}
	if _, _, err := run(t, app, "design", "get", "zz9y"); err == nil {
		t.Fatal("filename short must not resolve once the fence id replaces it")
	}
	list, _, err := run(t, app, "design", "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(list, "wc-ab2c\t") || strings.Contains(list, "wc-zz9y\t") {
		t.Fatalf("list id = %q", list)
	}

	// Two filenames, one fence id: the same refusal as two tickets with one id.
	other := "---\nid: wc-ab2c\nstatus: draft\ncreated: 2026-01-02T00:00:00Z\n---\n# Other\n"
	if err := os.WriteFile(filepath.Join(dir, "design", "wc-de34-other.md"), []byte(other), 0o644); err != nil {
		t.Fatal(err)
	}
	got, _, err := run(t, app, "design", "get", "ab2c")
	if err == nil || got != "" {
		t.Fatalf("shared fence id must refuse with no path, out %q err %v", got, err)
	}
	doc, _, err := run(t, app, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc, token.DesignID+" wc-ab2c claimed by") {
		t.Fatalf("doctor must report the shared fence id, got %q", doc)
	}

	// A filename short is replaced by a different fence id, as with a ticket.
	if err := os.Remove(filepath.Join(dir, "design", "wc-de34-other.md")); err != nil {
		t.Fatal(err)
	}
	alt := "---\nid: wc-qrst\nstatus: accepted\ncreated: 2026-01-03T00:00:00Z\n---\n# Alt\n"
	altPath := filepath.Join(dir, "design", "wc-ab2c-alt.md")
	if err := os.WriteFile(altPath, []byte(alt), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, err = run(t, app, "design", "get", "qrst")
	if err != nil || strings.TrimSpace(out) != altPath {
		t.Fatalf("get qrst = %q err %v", out, err)
	}
	out, _, err = run(t, app, "design", "get", "ab2c")
	if err != nil || strings.TrimSpace(out) != filepath.Join(dir, "design", "wc-zz9y-shape.md") {
		t.Fatalf("get ab2c must follow the fence id, got %q err %v", out, err)
	}
}

func TestDesignSharedIDRefusesAndUnparseableGet(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	t.Setenv("TK_SCOPE", "wc")
	writeDesign(t, dir, "wc-ab2c", "one", "draft", "2026-01-01T00:00:00Z", "One")
	writeDesign(t, dir, "wc-ab2c", "two", "draft", "2026-01-02T00:00:00Z", "Two")

	out, errOut, err := run(t, app, "design", "get", "ab2c")
	if err == nil || out != "" {
		t.Fatalf("shared get must refuse with empty stdout, out %q err %v", out, err)
	}
	if !strings.Contains(err.Error(), token.DesignID) {
		t.Fatalf("err = %v stderr %q", err, errOut)
	}
	if _, _, err := run(t, app, "design", "mark", "accepted", "ab2c"); err == nil {
		t.Fatal("shared mark must refuse")
	}
	before, _ := os.ReadFile(filepath.Join(dir, "design", "wc-ab2c-one.md"))
	if _, _, err := run(t, app, "design", "meta", "add", "wc-ab2c", "produces", "wc-zzzz"); err == nil {
		t.Fatal("shared meta must refuse")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "design", "wc-ab2c-one.md"))
	if string(before) != string(after) {
		t.Fatal("shared meta must not write")
	}

	if err := os.WriteFile(filepath.Join(dir, "design", "wc-jk89-broken.md"), []byte("---\nid: [\n---\n# Broken pair\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeDesign(t, dir, "wc-jk89", "good", "draft", "2026-01-03T00:00:00Z", "Good")
	out, _, err = run(t, app, "design", "get", "jk89")
	if err == nil || out != "" {
		t.Fatalf("shared id must refuse even when one fence is unparseable, out %q err %v", out, err)
	}

	broken := filepath.Join(dir, "design", "wc-de34-broken.md")
	if err := os.WriteFile(broken, []byte("---\nid: [\n---\n# Broken\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, errOut, err = run(t, app, "design", "get", "de34")
	if err != nil {
		t.Fatalf("unparseable get must exit 0, got %v", err)
	}
	if strings.TrimSpace(out) != broken {
		t.Fatalf("path = %q", out)
	}
	if !strings.Contains(errOut, "parse_error: wc-de34:") || strings.Contains(errOut, "cannot rewrite") {
		t.Fatalf("get stderr = %q", errOut)
	}
	_, markErr, err := run(t, app, "design", "mark", "accepted", "de34")
	if err == nil || !strings.Contains(err.Error(), "cannot rewrite quarantined frontmatter") {
		t.Fatalf("unparseable mark = %v", err)
	}
	if strings.Contains(markErr, "unparseable") {
		t.Fatalf("mark stderr = %q", markErr)
	}
}

func TestDesignParseErrorMatchesTicketSplit(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	t.Setenv("TK_SCOPE", "wc")
	writeDesign(t, dir, "wc-ab2c", "ok", "draft", "2026-01-01T00:00:00Z", "Ok")
	broken := filepath.Join(dir, "design", "wc-de34-broken.md")
	if err := os.WriteFile(broken, []byte("# no fence\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	list, errOut, err := run(t, app, "design", "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(list, "wc-ab2c\t") || strings.Contains(list, "wc-de34") {
		t.Fatalf("list = %q", list)
	}
	if errOut != "parse_error: 1 unparseable\n" {
		t.Fatalf("list stderr = %q", errOut)
	}

	_, errOut, err = run(t, app, "design", "get", "ab2c")
	if err != nil {
		t.Fatal(err)
	}
	if errOut != "parse_error: 1 unparseable\n" {
		t.Fatalf("healthy get stderr = %q", errOut)
	}

	out, errOut, err := run(t, app, "design", "get", "de34")
	if err != nil {
		t.Fatalf("broken get: %v", err)
	}
	if strings.TrimSpace(out) != broken {
		t.Fatalf("path = %q", out)
	}
	wantErr := "parse_error: 1 unparseable\nparse_error: wc-de34: frontmatter fence missing, broken, or carries conflict markers\n"
	if errOut != wantErr {
		t.Fatalf("broken get stderr = %q", errOut)
	}

	doc, docErr, err := run(t, app, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	want := "parse_error: wc-de34: frontmatter fence missing, broken, or carries conflict markers (" + broken + ")"
	if !strings.Contains(doc, want) {
		t.Fatalf("doctor = %q stderr %q", doc, docErr)
	}
	if strings.Contains(doc, "unparseable") || strings.Contains(docErr, "unparseable") {
		t.Fatalf("doctor count leaked: stdout %q stderr %q", doc, docErr)
	}

	board, boardErr, err := run(t, app, "list", "--all")
	if err != nil {
		t.Fatal(err)
	}
	if board != "" || strings.Contains(boardErr, "unparseable") || strings.Contains(boardErr, "wc-de34") {
		t.Fatalf("ticket list = %q stderr %q", board, boardErr)
	}
	searchOut, searchErr, err := run(t, app, "search", "fence", "--scope", "wc")
	if err != nil {
		t.Fatal(err)
	}
	if searchOut != "" || strings.Contains(searchErr, "unparseable") {
		t.Fatalf("search = %q stderr %q", searchOut, searchErr)
	}
}

func TestDesignFilenameIDMismatchAndRepairLeavesIt(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	t.Setenv("TK_SCOPE", "wc")
	addTicket(t, dir, "wc-zz9y", "ticket", "todo", "a0", "# Ticket\n", false, "")
	writeDesign(t, dir, "wc-ab2c", "ok", "draft", "2026-01-01T00:00:00Z", "Ok")
	path := filepath.Join(dir, "design", "wc-zz9y-shape.md")
	body := "---\nid: wc-qrst\nstatus: draft\ncreated: 2026-01-01T00:00:00Z\n---\n# Shape\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ticketPath := filepath.Join(dir, "wc-zz9y-ticket.md")
	ticketBefore, err := os.ReadFile(ticketPath)
	if err != nil {
		t.Fatal(err)
	}

	doc, _, err := run(t, app, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	want := `filename/id mismatch: wc-zz9y-shape.md does not begin with its frontmatter id "wc-qrst"`
	if !strings.Contains(doc, want) {
		t.Fatalf("doctor = %q", doc)
	}
	if strings.Contains(doc, "wc-ab2c-ok.md") {
		t.Fatalf("aligned design reported: %q", doc)
	}
	if strings.Contains(doc, token.DesignID+" wc-qrst") {
		t.Fatalf("fence-only short must not be a collision: %q", doc)
	}
	if !strings.Contains(doc, token.DesignID+" wc-zz9y claimed by") || !strings.Contains(doc, "wc-zz9y-ticket.md") || !strings.Contains(doc, "wc-zz9y-shape.md") {
		t.Fatalf("filename short collision = %q", doc)
	}
	if strings.Contains(doc, "run tk repair") {
		t.Fatalf("mismatch must not send the user to repair: %q", doc)
	}

	if _, _, err := run(t, app, "repair"); err != nil {
		t.Fatalf("repair: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("repair rewrote the design: %q", after)
	}
	ticketAfter, err := os.ReadFile(ticketPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(ticketAfter) != string(ticketBefore) {
		t.Fatalf("repair rewrote the ticket: %q", ticketAfter)
	}
}

func TestDesignDoctorTokensAndRepairLeavesDesign(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	t.Setenv("TK_SCOPE", "wc")
	addTicket(t, dir, "wc-ab2c", "alpha", "todo", "a0", "# Alpha\n", false, "")
	writeDesign(t, dir, "wc-ab2c", "shape", "draft", "2026-01-01T00:00:00Z", "Shape")
	writeDesign(t, dir, "wc-de34", "pair", "draft", "2026-01-01T00:00:00Z", "Pair")
	writeDesign(t, dir, "wc-de34", "other", "draft", "2026-01-02T00:00:00Z", "Other")
	if err := os.WriteFile(filepath.Join(dir, "design", "wc-gh56-dangling.md"), []byte("---\nid: wc-gh56\nstatus: draft\ncreated: 2026-01-01T00:00:00Z\nproduces: [wc-zzzz, other-ab2c]\n---\n# Dangling\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	doc, _, err := run(t, app, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(doc, token.DesignID) != 2 {
		t.Fatalf("want one design_id for ticket+design and one for two designs, got %q", doc)
	}
	if strings.Contains(doc, token.DuplicateID) {
		t.Fatalf("design pairing must not reuse duplicate_id: %q", doc)
	}
	if strings.Count(doc, token.ProducesDangling) != 2 {
		t.Fatalf("dangling produces = %q", doc)
	}
	for _, line := range lines(doc) {
		if strings.HasPrefix(line, token.DesignID) && strings.Contains(line, "run tk repair") {
			t.Fatalf("design_id must not prescribe repair: %q", line)
		}
	}

	designPath := filepath.Join(dir, "design", "wc-ab2c-shape.md")
	before, _ := os.ReadFile(designPath)
	if _, _, err := run(t, app, "repair"); err != nil {
		t.Fatalf("repair: %v", err)
	}
	after, err := os.ReadFile(designPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("repair must not rewrite the design file")
	}
	if _, err := os.Stat(filepath.Join(dir, "design", "wc-de34-pair.md")); err != nil {
		t.Fatal("repair must not rename a design pair")
	}
}

func TestRepairSkipsDesignOccupiedExtension(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	t.Setenv("TK_SCOPE", "wc")
	addTicket(t, dir, "wc-ab2c", "alpha", "todo", "a0", "# Alpha\n", false, "")
	addTicket(t, dir, "wc-ab2c", "beta", "todo", "a1", "# Beta\n", false, "")
	writeDesign(t, dir, "wc-ab2ca", "shape", "draft", "2026-01-01T00:00:00Z", "Shape")

	if _, _, err := run(t, app, "repair"); err != nil {
		t.Fatalf("repair: %v", err)
	}
	if !fileExists(dir, "wc-ab2cb-beta.md") && !fileExists(dir, "wc-ab2cb-alpha.md") {
		t.Fatalf("loser must skip the design short ab2ca, files=%v", ticketFiles(t, dir))
	}
	if _, err := os.Stat(filepath.Join(dir, "design", "wc-ab2ca-shape.md")); err != nil {
		t.Fatal("design file must stay")
	}
}

func TestScopeRenameRewritesDesigns(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "old")
	other := initScope(t, app, "api")
	addTicket(t, dir, "old-m4np", "target", "todo", "a0", "# Target\n", false, "")
	addTicket(t, other, "api-zz22", "x", "todo", "a0", "# X\n", false, "")
	writeDesign(t, dir, "old-ab2c", "shape", "draft", "2026-01-01T00:00:00Z", "Shape")
	raw := "---\nid: old-ab2c\nstatus: draft\ncreated: 2026-01-01T00:00:00Z\nproduces: [old-m4np, api-zz22]\n---\n# Shape\n"
	if err := os.WriteFile(filepath.Join(dir, "design", "old-ab2c-shape.md"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	writeDesign(t, dir, "new-gh56", "kept", "accepted", "2026-01-02T00:00:00Z", "Kept")
	// A second scope's design that names a ticket here stays, and rename reports it.
	writeDesign(t, other, "api-xy99", "remote", "draft", "2026-01-01T00:00:00Z", "Remote")
	remote := "---\nid: api-xy99\nstatus: draft\ncreated: 2026-01-01T00:00:00Z\nproduces: [old-m4np]\n---\n# Remote\n"
	if err := os.WriteFile(filepath.Join(other, "design", "api-xy99-remote.md"), []byte(remote), 0o644); err != nil {
		t.Fatal(err)
	}

	out, _, err := run(t, app, "scope", "rename", "old", "new")
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	want := "edge_verify: api-xy99 produces old-m4np — target scope renamed to new, update this reference"
	if !strings.Contains(out, want) || strings.Count(out, "edge_verify:") != 1 {
		t.Fatalf("rename stdout = %q", out)
	}
	renamed := filepath.Join(dir, "design", "new-ab2c-shape.md")
	data, err := os.ReadFile(renamed)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "id: new-ab2c") {
		t.Fatalf("fence id: %q", text)
	}
	if !strings.Contains(text, "new-m4np") || !strings.Contains(text, "api-zz22") {
		t.Fatalf("produces rekey: %q", text)
	}
	if strings.Contains(text, "old-m4np") || strings.Contains(text, "old-ab2c") {
		t.Fatalf("old prefix remains: %q", text)
	}
	if _, err := os.Stat(filepath.Join(dir, "design", "old-ab2c-shape.md")); !os.IsNotExist(err) {
		t.Fatal("old design filename must be gone")
	}
	kept, err := os.ReadFile(filepath.Join(dir, "design", "new-gh56-kept.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(kept), "id: new-gh56") {
		t.Fatalf("already-new design must stay: %q", kept)
	}
	remoteAfter, _ := os.ReadFile(filepath.Join(other, "design", "api-xy99-remote.md"))
	if !strings.Contains(string(remoteAfter), "old-m4np") {
		t.Fatalf("other scope produces must stay: %q", remoteAfter)
	}
	if !fileExists(dir, "new-m4np-target.md") {
		t.Fatal("ticket rename must still run")
	}
}

func TestDesignFileNotDirIsResidue(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	other := initScope(t, app, "api")
	designPath := filepath.Join(dir, "design")
	if err := os.WriteFile(designPath, []byte("not a directory\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeDesign(t, other, "api-ab2c", "ghost", "published", "2026-01-01T00:00:00Z", "Ghost")

	out, _, err := run(t, app, "design", "list", "--scope", "wc")
	if err != nil || out != "" {
		t.Fatalf("list = %q err %v", out, err)
	}

	doc, errOut, err := run(t, app, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v stderr %q stdout %q", err, errOut, doc)
	}
	if strings.Contains(errOut, "not a directory") {
		t.Fatalf("stderr = %q", errOut)
	}
	if !strings.Contains(doc, token.NonAllowlist) || !strings.Contains(doc, designPath) {
		t.Fatalf("doctor = %q", doc)
	}
	if !strings.Contains(doc, `schema_error: api-ab2c has unknown status "published"`) {
		t.Fatalf("later scope dropped: %q", doc)
	}
}

func TestDesignListFollowsDesignSymlink(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	t.Setenv("TK_SCOPE", "wc")
	writeDesign(t, dir, "wc-ab2c", "live", "draft", "2026-01-01T00:00:00Z", "Live")
	if err := os.Rename(filepath.Join(dir, "design"), filepath.Join(dir, "design-real")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("design-real", filepath.Join(dir, "design")); err != nil {
		t.Fatal(err)
	}
	out, _, err := run(t, app, "design", "list")
	if err != nil || !strings.Contains(out, "wc-ab2c") {
		t.Fatalf("list through symlink = %q err %v", out, err)
	}
}

func TestDesignMetaRemoveDropsNonID(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	t.Setenv("TK_SCOPE", "wc")
	addTicket(t, dir, "wc-m4np", "target", "todo", "a0", "# Target\n", false, "")
	writeDesign(t, dir, "wc-de34", "shape", "draft", "2026-01-01T00:00:00Z", "Shape")
	path := filepath.Join(dir, "design", "wc-de34-shape.md")
	raw := "---\nid: wc-de34\nstatus: draft\ncreated: 2026-01-01T00:00:00Z\nproduces: [nope, wc-m4np, nope]\n---\n# Shape\n"
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = run(t, app, "design", "meta", "add", "wc-de34", "produces", "nope")
	if ExitCodeFromError(err) != exitUsage || err == nil || !strings.Contains(err.Error(), `"nope" is not a full ticket id`) {
		t.Fatalf("add nope = %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(before) {
		t.Fatalf("add nope wrote the file: %q", got)
	}

	if _, _, err := run(t, app, "design", "meta", "remove", "wc-de34", "produces", "nope"); err != nil {
		t.Fatalf("remove nope: %v", err)
	}
	got, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	if strings.Contains(text, "nope") || !strings.Contains(text, "wc-m4np") {
		t.Fatalf("remove nope = %q", text)
	}

	if _, _, err := run(t, app, "design", "meta", "remove", "wc-de34", "produces", "nope"); err != nil {
		t.Fatalf("second remove: %v", err)
	}
	again, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != text {
		t.Fatalf("absent remove rewrote the file: %q", again)
	}

	doc, _, err := run(t, app, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(doc, token.ProducesDangling) || strings.Contains(doc, "nope") {
		t.Fatalf("doctor = %q", doc)
	}
}

func TestDesignMetaNonListProducesIsUsage(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	t.Setenv("TK_SCOPE", "wc")
	addTicket(t, dir, "wc-m4np", "target", "todo", "a0", "# Target\n", false, "")
	writeDesign(t, dir, "wc-ab2c", "shape", "draft", "2026-01-01T00:00:00Z", "Shape")
	path := filepath.Join(dir, "design", "wc-ab2c-shape.md")
	raw := "---\nid: wc-ab2c\nstatus: draft\ncreated: 2026-01-01T00:00:00Z\nproduces: hello\n---\n# Shape\n"
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cases := [][]string{
		{"design", "meta", "remove", "ab2c", "produces", "hello"},
		{"design", "meta", "remove", "ab2c", "produces", "wc-m4np"},
		{"design", "meta", "add", "ab2c", "produces", "wc-m4np"},
	}
	for _, args := range cases {
		_, stderr, err := run(t, app, args...)
		if ExitCodeFromError(err) != exitUsage {
			t.Fatalf("%v exit = %v, stderr %q", args, err, stderr)
		}
		if !strings.Contains(err.Error(), `custom field "produces" is not a string list`) {
			t.Fatalf("%v message = %v", args, err)
		}
		got, rerr := os.ReadFile(path)
		if rerr != nil {
			t.Fatal(rerr)
		}
		if string(got) != string(before) {
			t.Fatalf("%v wrote the file: %q", args, got)
		}
	}
}

func TestScopeRenameKeepsNonListProduces(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "old")
	addTicket(t, dir, "old-m4np", "target", "todo", "a0", "# Target\n", false, "")
	if err := os.MkdirAll(filepath.Join(dir, "design"), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := "---\nid: old-ab2c\nstatus: draft\ncreated: 2026-01-01T00:00:00Z\nproduces: hello\n---\n# Shape\n"
	if err := os.WriteFile(filepath.Join(dir, "design", "old-ab2c-shape.md"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := run(t, app, "scope", "rename", "old", "new"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if !fileExists(dir, "new-m4np-target.md") {
		t.Fatal("ticket must be renamed")
	}
	if fileExists(dir, "old-m4np-target.md") {
		t.Fatal("old ticket filename must be gone")
	}
	data, err := os.ReadFile(filepath.Join(dir, "design", "new-ab2c-shape.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "id: new-ab2c") {
		t.Fatalf("fence id: %q", text)
	}
	if !strings.Contains(text, "produces: hello") && !strings.Contains(text, "produces: \"hello\"") {
		t.Fatalf("non-list produces must remain: %q", text)
	}
	if _, err := os.Stat(filepath.Join(dir, "design", "old-ab2c-shape.md")); !os.IsNotExist(err) {
		t.Fatal("old design filename must be gone")
	}
}

func TestScopeRenameRefusesDesignWithoutFence(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "old")
	addTicket(t, dir, "old-m4np", "target", "todo", "a0", "# Target\n", false, "")
	if err := os.MkdirAll(filepath.Join(dir, "design"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "design", "old-ab2c-shape.md"), []byte("# no fence\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, app, "scope", "rename", "old", "new"); err == nil {
		t.Fatal("missing design fence must refuse")
	}
	if !fileExists(dir, "old-m4np-target.md") {
		t.Fatal("ticket must be unchanged when design rename refuses")
	}
	if fileExists(dir, "new-m4np-target.md") {
		t.Fatal("ticket must not be renamed")
	}
}

func designIDFromPath(t *testing.T, path string) string {
	t.Helper()
	stem := strings.TrimSuffix(filepath.Base(path), ".md")
	parts := strings.SplitN(stem, "-", 3)
	if len(parts) < 2 {
		t.Fatalf("path %q has no id", path)
	}
	return parts[0] + "-" + parts[1]
}

func writeDesign(t *testing.T, dir, id, slug, status, created, heading string) {
	t.Helper()
	target := filepath.Join(dir, "design")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nid: " + id + "\nstatus: " + status + "\ncreated: " + created + "\n---\n# " + heading + "\n"
	if err := os.WriteFile(filepath.Join(target, id+"-"+slug+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
