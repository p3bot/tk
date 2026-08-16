package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/frontmatter"
	"github.com/p3bot/tk/internal/git"
	"github.com/p3bot/tk/internal/gitstate"
	"github.com/p3bot/tk/internal/pathutil"
	"github.com/p3bot/tk/internal/testgit"
	"github.com/p3bot/tk/internal/token"
)

// requireGit skips when git is missing and hermeticises env for production git under test.
func requireGit(t *testing.T) {
	t.Helper()
	if !git.Available() {
		t.Skip("git not on PATH")
	}
	testgit.Hermetic(t)
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	testgit.Run(t, dir, args...)
}

func gitLog(t *testing.T, repo string) []string {
	t.Helper()
	// Empty repos have no commits yet; treat non-zero log as "no messages".
	out, err := testgit.CombinedAllowFailure(t, repo, "log", "--format=%s")
	if err != nil {
		return nil
	}
	return lines(out)
}

// initGitScope creates a git repo, registers a scope dir inside it with the given
// name/autoCommit, and returns the registered scope dir and git-root (both canonical).
func initGitScope(t *testing.T, app *App, name string, autoCommit bool) (string, string) {
	t.Helper()
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "a@b.c")
	runGit(t, repo, "config", "user.name", "tk-test")
	runGit(t, repo, "config", "commit.gpgsign", "false")
	dir := filepath.Join(repo, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	args := []string{"scope", "init", dir, "--name", name}
	if autoCommit {
		args = append(args, "--auto-commit")
	}
	out, _, err := run(t, app, args...)
	if err != nil {
		t.Fatalf("init git scope %s: %v", name, err)
	}
	return strings.TrimSpace(out), pathutil.Canonical(repo)
}

func createID(t *testing.T, app *App, scope string, args ...string) (string, string) {
	t.Helper()
	out, _, err := run(t, app, append([]string{"create"}, append(args, "--scope", scope)...)...)
	if err != nil {
		t.Fatalf("create %v: %v", args, err)
	}
	path := strings.TrimSpace(out)
	base := filepath.Base(path)
	fields := strings.SplitN(base, "-", 3)
	return path, fields[0] + "-" + fields[1]
}

func fmValue(t *testing.T, path, key string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range lines(string(data)) {
		if strings.HasPrefix(line, key+":") {
			v := strings.TrimSpace(strings.TrimPrefix(line, key+":"))
			return strings.Trim(v, `"`)
		}
	}
	return ""
}

func TestCreateScaffoldPlainFiles(t *testing.T) {
	app := newApp(t)
	initScope(t, app, "wc")

	path, id := createID(t, app, "wc", "Network redesign")
	if !strings.HasSuffix(path, ".md") || !filepath.IsAbs(path) {
		t.Fatalf("create should print an absolute .md path, got %q", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	if fmValue(t, path, "id") != id {
		t.Errorf("frontmatter id = %q want %q", fmValue(t, path, "id"), id)
	}
	if got := fmValue(t, path, "status"); got != "draft" {
		t.Errorf("default status = %q want draft", got)
	}
	if got := fmValue(t, path, "order"); got != "a0" {
		t.Errorf("first order = %q want a0", got)
	}
	if fmValue(t, path, "created") == "" {
		t.Error("created must be set")
	}
	if !strings.Contains(body, "\norder: \"a0\"\n") {
		t.Errorf("order must be quoted in the file: %q", body)
	}
	if !strings.HasSuffix(body, "# Network redesign\n") {
		t.Errorf("body must be a single H1 from the title: %q", body)
	}
	if !strings.HasSuffix(path, id+"-network-redesign.md") {
		t.Errorf("filename slug not frozen from title: %q", path)
	}
}

func TestCreateEmptyTitleAndUnknownStatus(t *testing.T) {
	app := newApp(t)
	initScope(t, app, "wc")

	if _, _, err := run(t, app, "create", "   ", "--scope", "wc"); ExitCodeFromError(err) != exitUsage {
		t.Errorf("empty title should exit 2, got %v", err)
	}
	if _, _, err := run(t, app, "create", "X", "nope", "--scope", "wc"); ExitCodeFromError(err) != exitUsage {
		t.Errorf("unknown status should exit 2, got %v", err)
	}
}

func TestCreateTags(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "shared", "todo", "a0", "# Shared\n", false, "tags: [shared]\n")
	addTicket(t, dir, "wc-de34", "old", "done", "a1", "# Old\n", true, "tags: [legacy]\n")

	// No --tag: tags key absent; create stderr must not carry tag_new.
	out, errOut, err := run(t, app, "create", "Plain", "--scope", "wc")
	if err != nil {
		t.Fatalf("create without tags: %v", err)
	}
	path := strings.TrimSpace(out)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "tags:") {
		t.Errorf("create without --tag must omit tags key, got %q", data)
	}
	if strings.Contains(errOut, token.TagNew) {
		t.Errorf("no-tag create must not emit tag_new, got %q", errOut)
	}
	base := filepath.Base(path)
	fields := strings.SplitN(base, "-", 3)
	id := fields[0] + "-" + fields[1]
	out, _, err = run(t, app, "meta", "get", id, "tags")
	if err != nil {
		t.Fatalf("meta get tags: %v", err)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("no-tag create tags get = %q want empty", out)
	}

	// One and many --tag, including a board-existing and board-new; status positional still works.
	out, errOut, err = run(t, app, "create", "Tagged", "todo",
		"--tag", "alpha", "--tag", "shared", "--tag", "beta", "--scope", "wc")
	if err != nil {
		t.Fatalf("create with tags: %v", err)
	}
	path = strings.TrimSpace(out)
	if !filepath.IsAbs(path) || !strings.HasSuffix(path, ".md") {
		t.Fatalf("create must print absolute .md path, got %q", path)
	}
	if strings.Contains(out, token.TagNew) || strings.Contains(out, "tag_new:") {
		t.Errorf("tag_new must not ride create stdout, got %q", out)
	}
	if got := fmValue(t, path, "status"); got != "todo" {
		t.Errorf("status with --tag = %q want todo", got)
	}
	gotTags := createTagsFromFile(t, path)
	wantTags := []string{"alpha", "shared", "beta"}
	if !reflect.DeepEqual(gotTags, wantTags) {
		t.Errorf("scaffold tags = %v want %v", gotTags, wantTags)
	}
	if !strings.Contains(errOut, token.FormatTagNew("alpha")) {
		t.Errorf("board-new alpha missing notice in %q", errOut)
	}
	if !strings.Contains(errOut, token.FormatTagNew("beta")) {
		t.Errorf("board-new beta missing notice in %q", errOut)
	}
	if strings.Contains(errOut, token.FormatTagNew("shared")) {
		t.Errorf("already-used board tag must stay quiet, got %q", errOut)
	}
	if strings.Contains(errOut, token.TagUnknown) {
		t.Errorf("create must use tag_new not tag_unknown, got %q", errOut)
	}

	// Dedupe preserves first-seen order; one notice per board-new string.
	out, errOut, err = run(t, app, "create", "Dedupe",
		"--tag", "gamma", "--tag", "alpha", "--tag", "gamma", "--scope", "wc")
	if err != nil {
		t.Fatalf("create dedupe: %v", err)
	}
	path = strings.TrimSpace(out)
	gotTags = createTagsFromFile(t, path)
	if !reflect.DeepEqual(gotTags, []string{"gamma", "alpha"}) {
		t.Errorf("deduped tags = %v want [gamma alpha]", gotTags)
	}
	if strings.Count(errOut, token.FormatTagNew("gamma")) != 1 {
		t.Errorf("want one gamma notice, got %q", errOut)
	}
	if strings.Contains(errOut, token.FormatTagNew("alpha")) {
		t.Errorf("alpha already on board from prior create; must stay quiet, got %q", errOut)
	}

	// Archive-present tag is in-use; quiet. Empty --tag is usage.
	_, errOut, err = run(t, app, "create", "Archive tag", "--tag", "legacy", "--scope", "wc")
	if err != nil {
		t.Fatalf("create with archive tag: %v", err)
	}
	if strings.Contains(errOut, token.TagNew) {
		t.Errorf("archive-present tag must stay quiet, got %q", errOut)
	}
	if _, _, err := run(t, app, "create", "Empty tag", "--tag", "", "--scope", "wc"); ExitCodeFromError(err) != exitUsage {
		t.Errorf("empty --tag should exit 2, got %v", err)
	}

	// Terminal status + --tag: scaffold under archive/ with tags on the model.
	out, errOut, err = run(t, app, "create", "Already shipped", "done",
		"--tag", "shipped", "--scope", "wc")
	if err != nil {
		t.Fatalf("terminal create with tags: %v", err)
	}
	path = strings.TrimSpace(out)
	if !strings.Contains(path, string(os.PathSeparator)+"archive"+string(os.PathSeparator)) {
		t.Errorf("terminal tagged create must live under archive/, got %q", path)
	}
	if got := fmValue(t, path, "status"); got != "done" {
		t.Errorf("terminal tagged status = %q want done", got)
	}
	if !reflect.DeepEqual(createTagsFromFile(t, path), []string{"shipped"}) {
		t.Errorf("terminal scaffold tags = %v want [shipped]", createTagsFromFile(t, path))
	}
	if !strings.Contains(errOut, token.FormatTagNew("shipped")) {
		t.Errorf("board-new shipped missing notice on terminal create, got %q", errOut)
	}
	if !strings.Contains(errOut, "not git-durable") {
		t.Errorf("terminal create should ride scaffold-durability note, got %q", errOut)
	}
}

func TestCreateTagsNoSelfCommit(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	_, repo := initGitScope(t, app, "wc", true)

	out, errOut, err := run(t, app, "create", "Tagged work",
		"--tag", "backend", "--scope", "wc")
	if err != nil {
		t.Fatalf("tagged create: %v", err)
	}
	path := strings.TrimSpace(out)
	if !filepath.IsAbs(path) {
		t.Fatalf("create must print absolute path, got %q", path)
	}
	if !reflect.DeepEqual(createTagsFromFile(t, path), []string{"backend"}) {
		t.Errorf("scaffold tags = %v want [backend]", createTagsFromFile(t, path))
	}
	if n := len(gitLog(t, repo)); n != 0 {
		t.Fatalf("tagged create must not self-commit, got %d commits", n)
	}
	if strings.Contains(out, token.TagNew) {
		t.Errorf("tag_new must not ride stdout, got %q", out)
	}
	if !strings.Contains(errOut, token.FormatTagNew("backend")) {
		t.Errorf("board-new tag missing notice, got %q", errOut)
	}
}

func createTagsFromFile(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	interior, _, present := frontmatter.Split(data)
	if !present {
		t.Fatalf("missing frontmatter in %s", path)
	}
	m, err := frontmatter.Parse(interior)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return m.Tags
}

func TestCreateAppendsAfterScopeWideMax(t *testing.T) {
	app := newApp(t)
	initScope(t, app, "wc")

	pA, _ := createID(t, app, "wc", "A")
	pB, _ := createID(t, app, "wc", "B")
	pC, _ := createID(t, app, "wc", "C")
	a, b, c := fmValue(t, pA, "order"), fmValue(t, pB, "order"), fmValue(t, pC, "order")
	if a >= b || b >= c {
		t.Errorf("append orders must strictly increase, got %q %q %q", a, b, c)
	}
}

func TestCreateTerminalUnderArchive(t *testing.T) {
	app := newApp(t)
	initScope(t, app, "wc")

	out, errOut, err := run(t, app, "create", "Already done", "done", "--scope", "wc")
	if err != nil {
		t.Fatalf("terminal create: %v", err)
	}
	path := strings.TrimSpace(out)
	if !strings.Contains(path, string(os.PathSeparator)+"archive"+string(os.PathSeparator)) {
		t.Errorf("terminal create must live under archive/, got %q", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("terminal scaffold file must exist: %v", err)
	}
	// A terse durability note (not a closed token) rides stderr.
	if !strings.Contains(errOut, "not git-durable") {
		t.Errorf("terminal create should ride a scaffold-durability note, got %q", errOut)
	}
}

func TestMarkTerminalBoundaryMovePlainFiles(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	_, id := createID(t, app, "wc", "Work")

	if _, _, err := run(t, app, "mark", id, "todo"); err != nil {
		t.Fatalf("mark todo: %v", err)
	}
	out, _, err := run(t, app, "mark", id, "done")
	if err != nil {
		t.Fatalf("mark done: %v", err)
	}
	movedPath := strings.TrimSpace(out)
	if filepath.Dir(movedPath) != filepath.Join(dir, "archive") {
		t.Errorf("done should print the post-move archive path, got %q", movedPath)
	}
	if _, err := os.Stat(movedPath); err != nil {
		t.Errorf("moved file must exist under archive/: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.Base(movedPath))); !os.IsNotExist(err) {
		t.Errorf("old root path must be removed after the terminal move")
	}
	if got := fmValue(t, movedPath, "status"); got != "done" {
		t.Errorf("status not rewritten: %q", got)
	}

	out, _, err = run(t, app, "mark", id, "todo")
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	reopened := strings.TrimSpace(out)
	if filepath.Dir(reopened) != dir {
		t.Errorf("reopen should move the file back to the dir root, got %q", reopened)
	}
}

func TestMarkRefusals(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	_, id := createID(t, app, "wc", "Work")

	// Unknown status → exit 2.
	if _, _, err := run(t, app, "mark", id, "nope"); ExitCodeFromError(err) != exitUsage {
		t.Errorf("unknown status should exit 2, got %v", err)
	}
	// Malformed id → exit 2.
	if _, _, err := run(t, app, "mark", "bad!", "done", "--scope", "wc"); ExitCodeFromError(err) != exitUsage {
		t.Errorf("malformed id should exit 2, got %v", err)
	}
	if _, _, err := run(t, app, "mark", "wc-zzzz", "done"); ExitCodeFromError(err) != exitFailure {
		t.Errorf("unknown well-formed id should exit 1, got %v", err)
	}
	// parse_error quarantine → refuse, no write.
	bad := "---\nid: wc-abcd\nstatus: [unterminated\n---\n# broke\n"
	if err := os.WriteFile(filepath.Join(dir, "wc-abcd-x.md"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	_, errOut, err := run(t, app, "mark", "wc-abcd", "done")
	if ExitCodeFromError(err) != exitFailure {
		t.Errorf("parse_error mark should be non-zero, got %v", err)
	}
	if !strings.Contains(err.Error()+errOut, "parse_error:") {
		t.Errorf("expected parse_error token, got err=%v stderr=%q", err, errOut)
	}
	// Duplicate id → refuse, no write to either side.
	first := filepath.Join(dir, "wc-"+strings.SplitN(id, "-", 2)[1]+"-work.md")
	if _, err := os.Stat(first); err == nil {
		if err := copyFile(first, filepath.Join(dir, id+"-dup.md")); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := run(t, app, "mark", id, "done"); ExitCodeFromError(err) != exitFailure {
		t.Errorf("duplicate id should refuse non-zero, got %v", err)
	}
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

func TestMarkOpenDependsWarnMatrix(t *testing.T) {
	app := newApp(t)
	initScope(t, app, "wc")

	// Dep stays draft (non-terminal) so subject waiting-on is unmet without making dep next-eligible.
	_, depID := createID(t, app, "wc", "Dependency")

	// Subject starts blocked with an open depend (primary agent story: blocked → ready).
	_, subID := createID(t, app, "wc", "Subject")
	if _, _, err := run(t, app, "meta", "add", subID, "depends", depID); err != nil {
		t.Fatalf("meta add depends: %v", err)
	}
	if _, _, err := run(t, app, "mark", subID, "blocked"); err != nil {
		t.Fatalf("mark blocked: %v", err)
	}

	wantWarn := func(t *testing.T, id, newStatus string, waiting []string) {
		t.Helper()
		out, errOut, err := run(t, app, "mark", id, newStatus)
		if err != nil {
			t.Fatalf("mark %s: %v", newStatus, err)
		}
		path := strings.TrimSpace(out)
		if path == "" || !strings.HasSuffix(path, ".md") {
			t.Errorf("mark %s must print path on stdout, got %q", newStatus, out)
		}
		if strings.Contains(out, token.DependsOpen) {
			t.Errorf("depends_open must not ride stdout, got %q", out)
		}
		want := token.FormatDependsOpen(id, newStatus, waiting)
		if !strings.Contains(errOut, want) {
			t.Errorf("mark %s stderr want %q, got %q", newStatus, want, errOut)
		}
	}
	wantSilence := func(t *testing.T, id, newStatus string) {
		t.Helper()
		out, errOut, err := run(t, app, "mark", id, newStatus)
		if err != nil {
			t.Fatalf("mark %s: %v", newStatus, err)
		}
		if strings.TrimSpace(out) == "" || !strings.HasSuffix(strings.TrimSpace(out), ".md") {
			t.Errorf("mark %s must print path on stdout, got %q", newStatus, out)
		}
		if strings.Contains(errOut, token.DependsOpen) {
			t.Errorf("mark %s must not emit depends_open, got %q", newStatus, errOut)
		}
	}

	// Warn: status change into each ready/active built-in with open depends.
	wantWarn(t, subID, "todo", []string{depID})
	wantWarn(t, subID, "in-progress", []string{depID})
	wantWarn(t, subID, "review", []string{depID})

	// blocked → in-progress and blocked → review (primary human/agent stories).
	wantSilence(t, subID, "blocked")
	wantWarn(t, subID, "in-progress", []string{depID})
	wantSilence(t, subID, "blocked")
	wantWarn(t, subID, "review", []string{depID})

	// Silence: same-status mark (already review).
	wantSilence(t, subID, "review")

	// Warn: terminal reopen (archive → root) with open depends. Edges are keyed by
	// path; without the post-move path hand-off the warn would silently vanish.
	wantSilence(t, subID, "done")
	wantWarn(t, subID, "todo", []string{depID})

	// Silence: enter statuses that do not imply ready/active work, despite open depends.
	for _, s := range []string{"blocked", "draft", "backlog", "done", "cancelled"} {
		// prep todo may soft-warn; only the enter-s mark is checked for silence.
		if _, _, err := run(t, app, "mark", subID, "todo"); err != nil {
			t.Fatalf("prep todo before %s: %v", s, err)
		}
		wantSilence(t, subID, s)
	}

	// Silence: no depends at all.
	_, aloneID := createID(t, app, "wc", "Alone")
	wantSilence(t, aloneID, "blocked")
	wantSilence(t, aloneID, "todo")

	// Silence: all depends terminal — both reopen into todo and further ready/active moves.
	if _, _, err := run(t, app, "mark", depID, "done"); err != nil {
		t.Fatalf("close dep: %v", err)
	}
	// Subject ended the silence loop as cancelled.
	wantSilence(t, subID, "todo")
	wantSilence(t, subID, "in-progress")
}

func TestMarkOpenDependsDanglingAndMulti(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")

	// meta add refuses same-scope missing targets; plant dangling via on-disk FM (list waiting-on path).
	missing := "wc-zz99"
	hangID := "wc-ab2c"
	addTicket(t, dir, hangID, "hanging", "blocked", "a0", "# Hanging\n", false, "depends: ["+missing+"]\n")
	out, errOut, err := run(t, app, "mark", hangID, "todo")
	if err != nil {
		t.Fatalf("hanging todo: %v", err)
	}
	if strings.TrimSpace(out) == "" {
		t.Error("dangling mark must print path")
	}
	wantDangle := token.FormatDependsOpen(hangID, "todo", []string{missing})
	if !strings.Contains(errOut, wantDangle) {
		t.Errorf("dangling mark stderr want %q, got %q", wantDangle, errOut)
	}

	// Multi open depends: warning lists sorted full ids (evalDepends sort order).
	_, aID := createID(t, app, "wc", "DepA")
	_, bID := createID(t, app, "wc", "DepB")
	_, multiID := createID(t, app, "wc", "Multi")
	if _, _, err := run(t, app, "meta", "add", multiID, "depends", bID); err != nil {
		t.Fatalf("depends b: %v", err)
	}
	if _, _, err := run(t, app, "meta", "add", multiID, "depends", aID); err != nil {
		t.Fatalf("depends a: %v", err)
	}
	if _, _, err := run(t, app, "mark", multiID, "blocked"); err != nil {
		t.Fatalf("multi blocked: %v", err)
	}
	out, errOut, err = run(t, app, "mark", multiID, "todo")
	if err != nil {
		t.Fatalf("multi todo: %v", err)
	}
	if strings.TrimSpace(out) == "" {
		t.Error("multi mark must print path")
	}
	// Sorted lexicographically: aID and bID are mint-order dependent; sort for the assertion.
	waiting := []string{aID, bID}
	if waiting[0] > waiting[1] {
		waiting[0], waiting[1] = waiting[1], waiting[0]
	}
	wantMulti := token.FormatDependsOpen(multiID, "todo", waiting)
	if !strings.Contains(errOut, wantMulti) {
		t.Errorf("multi mark stderr want %q, got %q", wantMulti, errOut)
	}
}

func TestMarkOpenDependsDoesNotAffectNextGate(t *testing.T) {
	app := newApp(t)
	initScope(t, app, "wc")

	// Leave dep as draft: non-terminal (holds subject) but not next-eligible itself.
	_, depID := createID(t, app, "wc", "Dep")
	_, subID := createID(t, app, "wc", "Held")
	if _, _, err := run(t, app, "meta", "add", subID, "depends", depID); err != nil {
		t.Fatalf("depends: %v", err)
	}
	// Mark into todo with open depends: soft-warns but still not next-eligible.
	_, errOut, err := run(t, app, "mark", subID, "todo")
	if err != nil {
		t.Fatalf("mark todo: %v", err)
	}
	if !strings.Contains(errOut, token.DependsOpen) {
		t.Fatalf("expected depends_open warn, got %q", errOut)
	}

	listOut, _, err := run(t, app, "list", "todo", "--scope", "wc")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(listOut, depID) {
		t.Errorf("list waiting-on should still show open dep %s, got %q", depID, listOut)
	}

	_, nextErr, err := run(t, app, "next", "--scope", "wc")
	if err == nil {
		t.Fatal("next must refuse while only todos are held on open depends")
	}
	if !strings.Contains(err.Error()+nextErr, "waiting on unmet deps") {
		t.Errorf("next empty-queue message wrong: err=%v stderr=%q", err, nextErr)
	}
}

func TestReorderPlacements(t *testing.T) {
	app := newApp(t)
	initScope(t, app, "wc")
	pA, idA := createID(t, app, "wc", "A")
	pB, idB := createID(t, app, "wc", "B")
	pC, idC := createID(t, app, "wc", "C")

	// --first: C sorts before everyone.
	if _, _, err := run(t, app, "reorder", idC, "--first"); err != nil {
		t.Fatalf("reorder --first: %v", err)
	}
	if fmValue(t, pC, "order") >= fmValue(t, pA, "order") {
		t.Errorf("--first should place C before A")
	}
	// --last: A sorts after everyone.
	if _, _, err := run(t, app, "reorder", idA, "--last"); err != nil {
		t.Fatalf("reorder --last: %v", err)
	}
	if fmValue(t, pA, "order") <= fmValue(t, pB, "order") {
		t.Errorf("--last should place A after B")
	}
	// --before C: B lands before C.
	if _, _, err := run(t, app, "reorder", idB, "--before", idC); err != nil {
		t.Fatalf("reorder --before: %v", err)
	}
	if fmValue(t, pB, "order") >= fmValue(t, pC, "order") {
		t.Errorf("--before C should place B before C")
	}
	// --after C: B lands after C (and after A, the current back).
	if _, _, err := run(t, app, "reorder", idB, "--after", idC); err != nil {
		t.Fatalf("reorder --after: %v", err)
	}
	if fmValue(t, pB, "order") <= fmValue(t, pC, "order") {
		t.Errorf("--after C should place B after C")
	}
}

func TestReorderArgErrors(t *testing.T) {
	app := newApp(t)
	initScope(t, app, "wc")
	_, id := createID(t, app, "wc", "A")

	cases := []struct {
		name string
		args []string
		want int
	}{
		{"no destination", []string{"reorder", id}, exitUsage},
		{"two destinations", []string{"reorder", id, "--first", "--last"}, exitUsage},
		{"malformed neighbour", []string{"reorder", id, "--before", "bad!"}, exitUsage},
		{"unknown neighbour", []string{"reorder", id, "--before", "wc-zzzz"}, exitFailure},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := run(t, app, c.args...)
			if got := ExitCodeFromError(err); got != c.want {
				t.Errorf("exit = %d want %d (err=%v)", got, c.want, err)
			}
		})
	}
}

func TestClaimWritesInProgress(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "one", "todo", "a0", "# One\n", false, "")
	addTicket(t, dir, "wc-de34", "two", "todo", "a1", "# Two\n", false, "")

	out, _, err := run(t, app, "next", "--claim", "--scope", "wc")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	claimed := strings.TrimSpace(out)
	if !strings.HasSuffix(claimed, "wc-ab2c-one.md") {
		t.Errorf("claim should take the first todo, got %q", claimed)
	}
	if got := fmValue(t, claimed, "status"); got != "in-progress" {
		t.Errorf("claim must set in-progress, got %q", got)
	}
	// The next claim serialises and takes the next todo — never the same id twice.
	out2, _, err := run(t, app, "next", "--claim", "--scope", "wc")
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if strings.TrimSpace(out2) == claimed {
		t.Errorf("a second claim must not hand off the same id")
	}
	if !strings.HasSuffix(strings.TrimSpace(out2), "wc-de34-two.md") {
		t.Errorf("second claim should take de34, got %q", out2)
	}
	out3, _, err := run(t, app, "next", "--claim", "--scope", "wc")
	if err == nil || out3 != "" {
		t.Errorf("empty claim queue should be non-zero with no path: out=%q err=%v", out3, err)
	}
}

func TestClaimSkipsParseErrorCandidate(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	bad := "---\nid: wc-ab2c\nstatus: [x\n---\n# broke\n"
	if err := os.WriteFile(filepath.Join(dir, "wc-ab2c-broke.md"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	addTicket(t, dir, "wc-de34", "ok", "todo", "a1", "# Ok\n", false, "")

	out, _, err := run(t, app, "next", "--claim", "--scope", "wc")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if !strings.HasSuffix(strings.TrimSpace(out), "wc-de34-ok.md") {
		t.Errorf("claim should skip the parse_error candidate and take de34, got %q", out)
	}
}

func TestWriteVerbsRefuseUnparseableConfig(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	_, id := createID(t, app, "wc", "Work")
	addTicket(t, dir, "wc-nb01", "n", "todo", "a5", "# N\n", false, "")

	bad := "name: \"wc\"\nautoCommit: false\nfields: {x: {type: \"float\"}}\n"
	if err := os.WriteFile(filepath.Join(dir, "tk.cue"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}

	checks := [][]string{
		{"create", "New", "--scope", "wc"},
		{"mark", id, "done"},
		{"reorder", id, "--first"},
		{"next", "--claim", "--scope", "wc"},
	}
	for _, args := range checks {
		_, errOut, err := run(t, app, args...)
		if ExitCodeFromError(err) != exitFailure {
			t.Errorf("%v under unparseable config should be non-zero, got %v", args, err)
		}
		if !strings.Contains(err.Error()+errOut, "config_unparseable:") {
			t.Errorf("%v should ride config_unparseable, got err=%v stderr=%q", args, err, errOut)
		}
	}

	if _, _, err := run(t, app, "list", "--scope", "wc"); err != nil {
		t.Errorf("reads must stay available under an unusable config: %v", err)
	}
}

func TestWriteVerbsSurfaceIntegrityWarnings(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-de34", "ok", "todo", "a1", "# Ok\n", false, "")
	bad := "---\nid: wc-ab2c\nstatus: [x\n---\n# broke\n"
	if err := os.WriteFile(filepath.Join(dir, "wc-ab2c-broke.md"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := [][]string{
		{"create", "New thing", "--scope", "wc"},
		{"mark", "wc-de34", "review"},
		{"reorder", "wc-de34", "--first"},
	}
	for _, args := range cases {
		_, errOut, err := run(t, app, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if !strings.Contains(errOut, "parse_error:") {
			t.Errorf("%v should ride the parse_error integrity warning, got stderr=%q", args, errOut)
		}
	}
}

func TestAutoCommitSelfCommitLifecycle(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	dir, repo := initGitScope(t, app, "wc", true)

	_, id := createID(t, app, "wc", "Work")
	// create never self-commits.
	if n := len(gitLog(t, repo)); n != 0 {
		t.Fatalf("create must not self-commit, got %d commits", n)
	}
	if _, _, err := run(t, app, "mark", id, "todo"); err != nil {
		t.Fatalf("mark todo: %v", err)
	}
	out, _, err := run(t, app, "mark", id, "done")
	if err != nil {
		t.Fatalf("mark done: %v", err)
	}
	log := gitLog(t, repo)
	if len(log) != 2 || log[0] != "tk: "+id+" -> done" || log[1] != "tk: "+id+" -> todo" {
		t.Fatalf("unexpected commit log: %v", log)
	}
	moved := strings.TrimSpace(out)
	rel, _ := filepath.Rel(repo, moved)
	tree := gitTree(t, repo)
	if !containsPath(tree, rel) {
		t.Errorf("archive path %q must be committed, tree=%v", rel, tree)
	}
	oldRel, _ := filepath.Rel(repo, filepath.Join(dir, filepath.Base(moved)))
	if containsPath(tree, oldRel) {
		t.Errorf("old root path %q must not remain in the committed tree", oldRel)
	}
	// Only the ticket file was staged: tk.cue stays untracked (committed later by sync).
	if containsPath(tree, filepath.Join("wc", "tk.cue")) {
		t.Errorf("self-commit must stage only the touched ticket path, not tk.cue")
	}
}

func gitTree(t *testing.T, repo string) []string {
	t.Helper()
	return lines(testgit.Combined(t, repo, "ls-tree", "-r", "--name-only", "HEAD"))
}

func containsPath(tree []string, p string) bool {
	p = filepath.ToSlash(p)
	for _, e := range tree {
		if e == p {
			return true
		}
	}
	return false
}

func TestClaimSelfCommitsInProgress(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	dir, repo := initGitScope(t, app, "wc", true)
	addTicket(t, dir, "wc-ab2c", "one", "todo", "a0", "# One\n", false, "")

	out, _, err := run(t, app, "next", "--claim", "--scope", "wc")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if got := fmValue(t, strings.TrimSpace(out), "status"); got != "in-progress" {
		t.Errorf("claim must set in-progress, got %q", got)
	}
	log := gitLog(t, repo)
	if len(log) != 1 || log[0] != "tk: wc-ab2c -> in-progress" {
		t.Fatalf("claim should self-commit one in-progress commit, got %v", log)
	}
}

func TestAutoCommitTerminalCreateNeverCommits(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	_, repo := initGitScope(t, app, "wc", true)

	out, errOut, err := run(t, app, "create", "Already done", "done", "--scope", "wc")
	if err != nil {
		t.Fatalf("terminal create: %v", err)
	}
	if !strings.Contains(strings.TrimSpace(out), string(os.PathSeparator)+"archive"+string(os.PathSeparator)) {
		t.Errorf("terminal create must scaffold under archive/, got %q", out)
	}
	// Even on an auto-commit scope with a git-root, a terminal create never self-commits.
	if n := len(gitLog(t, repo)); n != 0 {
		t.Errorf("terminal create must not self-commit, got %d commits", n)
	}
	if !strings.Contains(errOut, "not git-durable") {
		t.Errorf("terminal create should ride the scaffold-durability note, got %q", errOut)
	}
}

func TestAutoCommitPlannedRidesSyncDisabled(t *testing.T) {
	app := newApp(t)
	dir := filepath.Join(t.TempDir(), "wc")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, app, "scope", "init", dir, "--name", "wc", "--auto-commit"); err != nil {
		t.Fatalf("init planned auto-commit: %v", err)
	}
	_, id := createID(t, app, "wc", "Work")
	_, errOut, err := run(t, app, "mark", id, "todo")
	if err != nil {
		t.Fatalf("planned mark should still land: %v", err)
	}
	if !strings.Contains(errOut, "sync_disabled:") {
		t.Errorf("planned auto-commit write should ride sync_disabled, got %q", errOut)
	}
}

func TestRepoDrivenWriteQuiet(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	dir, _ := initGitScope(t, app, "rd", false)

	_, id := createID(t, app, "rd", "Host thing")
	_, errOut, err := run(t, app, "mark", id, "todo")
	if err != nil {
		t.Fatalf("repo-driven mark: %v", err)
	}
	if strings.Contains(errOut, "uncommitted:") {
		t.Errorf("repo-driven write must not ride uncommitted, got %q", errOut)
	}
	if strings.Contains(errOut, "sync_needed:") {
		t.Errorf("repo-driven write must not ride sync_needed, got %q", errOut)
	}
	// Pure reads never carry durability tokens.
	if _, readErr, _ := run(t, app, "get", id); strings.Contains(readErr, "uncommitted:") || strings.Contains(readErr, "sync_needed:") {
		t.Errorf("reads must never ride durability tokens, got %q", readErr)
	}
	// Status pulse still surfaces host dirty (opt-in visibility).
	out, _, err := run(t, app, "status", "--scope", "rd")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if p := parsePulse(out); p["uncommitted"] == "" || p["uncommitted"] == "0" {
		t.Fatalf("status pulse should report host dirty, uncommitted=%q in %q", p["uncommitted"], out)
	}
	// Non-allowlist residue must not invent a write-side signal either.
	if err := os.WriteFile(filepath.Join(dir, "residue.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, errOut2, _ := run(t, app, "mark", id, "review")
	if strings.Contains(errOut2, "uncommitted:") || strings.Contains(errOut2, "residue.txt") {
		t.Errorf("write must stay quiet with residue present, got %q", errOut2)
	}
}

func TestTkDrivenCreateSyncNeededDirty(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	initGitScope(t, app, "wc", true)

	_, errOut, err := run(t, app, "create", "Sync needed dirty", "--scope", "wc")
	if err != nil {
		t.Fatalf("tk-driven create: %v", err)
	}
	if !strings.Contains(errOut, "sync_needed: dirty") {
		t.Errorf("tk-driven create should ride sync_needed: dirty, got %q", errOut)
	}
	if strings.Contains(errOut, "uncommitted:") {
		t.Errorf("tk-driven must not overload uncommitted, got %q", errOut)
	}
	if strings.Contains(errOut, "run tk sync") || strings.Contains(errOut, "commit with the host") {
		t.Errorf("token body must not prescribe the action, got %q", errOut)
	}
}

func TestTkDrivenSelfCommitSyncNeededUnpushed(t *testing.T) {
	requireGit(t)
	remote := newBareRemote(t)
	m := cloneMachine(t, remote)
	dir := m.initScopeAutoCommit(t)
	addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "")
	// Snapshot scope files so the self-commit is the only local advance.
	gitIn(t, m.clone, "add", "-A")
	gitIn(t, m.clone, "commit", "-m", "seed scope")
	gitIn(t, m.clone, "push", "-u", "origin", "main")

	_, errOut, err := run(t, m.app, "mark", "wc-ab2c", "review")
	if err != nil {
		t.Fatalf("tk-driven mark: %v", err)
	}
	if !strings.Contains(errOut, "sync_needed: unpushed") {
		t.Errorf("self-commit ahead of upstream should ride sync_needed: unpushed, got %q", errOut)
	}
	if strings.Contains(errOut, "run tk sync") || strings.Contains(errOut, "git push") {
		t.Errorf("token body must not prescribe push/sync wording, got %q", errOut)
	}
	// Pure read stays silent.
	if _, readErr, _ := run(t, m.app, "get", "wc-ab2c"); strings.Contains(readErr, "sync_needed:") {
		t.Errorf("reads must never ride sync_needed, got %q", readErr)
	}
}

func TestTkDrivenWriteSyncNeededPushFailed(t *testing.T) {
	requireGit(t)
	remote := newBareRemote(t)
	m := cloneMachine(t, remote)
	dir := m.initScopeAutoCommit(t)
	addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "")
	gitIn(t, m.clone, "add", "-A")
	gitIn(t, m.clone, "commit", "-m", "seed scope")
	gitIn(t, m.clone, "push", "-u", "origin", "main")

	if err := gitstate.WriteLastPushError(m.app.StateDir, m.clone, "auth failed"); err != nil {
		t.Fatal(err)
	}
	_, errOut, err := run(t, m.app, "mark", "wc-ab2c", "review")
	if err != nil {
		t.Fatalf("tk-driven mark: %v", err)
	}
	if !strings.Contains(errOut, "sync_needed: push failed") {
		t.Errorf("last-push-error should ride sync_needed: push failed, got %q", errOut)
	}
	if strings.Contains(errOut, "note:") && strings.Contains(errOut, "failed push") {
		t.Errorf("freeform failed-push note must be retired, got %q", errOut)
	}
	if strings.Count(errOut, "sync_needed:") != 1 {
		t.Errorf("exactly one sync_needed line (priority over unpushed), got %q", errOut)
	}
}

func TestMidRebaseRefusesWrites(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	dir, repo := initGitScope(t, app, "wc", true)
	_, id := createID(t, app, "wc", "Work")

	if err := os.MkdirAll(filepath.Join(repo, ".git", "rebase-merge"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, app, "mark", id, "todo"); ExitCodeFromError(err) != exitFailure {
		t.Errorf("mid-rebase mark should refuse non-zero, got %v", err)
	}
	if _, _, err := run(t, app, "next", "--claim", "--scope", "wc"); ExitCodeFromError(err) != exitFailure {
		t.Errorf("mid-rebase next --claim should refuse non-zero, got %v", err)
	}
	if _, _, err := run(t, app, "create", "New", "--scope", "wc"); ExitCodeFromError(err) != exitFailure {
		t.Errorf("mid-rebase create should refuse non-zero, got %v", err)
	}
	if _, _, err := run(t, app, "meta", "set", id, "summary", "x"); ExitCodeFromError(err) != exitFailure {
		t.Errorf("mid-rebase meta set should refuse non-zero, got %v", err)
	}
	if _, _, err := run(t, app, "note", "add", "follow-up", "--scope", "wc"); ExitCodeFromError(err) != exitFailure {
		t.Errorf("mid-rebase note add should refuse non-zero, got %v", err)
	}
	if _, _, err := run(t, app, "note", "set", "replaced", "--scope", "wc"); ExitCodeFromError(err) != exitFailure {
		t.Errorf("mid-rebase note set should refuse non-zero, got %v", err)
	}
	if _, _, err := run(t, app, "note", "delete", "--name", "default", "--scope", "wc"); ExitCodeFromError(err) != exitFailure {
		t.Errorf("mid-rebase note delete should refuse non-zero, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "notes", "default.md")); !os.IsNotExist(err) {
		t.Errorf("refused note add/set must not create the file, stat err=%v", err)
	}
	if _, _, err := run(t, app, "get", id); err != nil {
		t.Errorf("reads must stay allowed mid-rebase, got %v", err)
	}
	if _, _, err := run(t, app, "meta", "get", id); err != nil {
		t.Errorf("meta get must stay allowed mid-rebase, got %v", err)
	}
	if _, _, err := run(t, app, "note", "--scope", "wc"); err != nil {
		t.Errorf("note cat must stay allowed mid-rebase, got %v", err)
	}
	if _, _, err := run(t, app, "note", "list", "--scope", "wc"); err != nil {
		t.Errorf("note list must stay allowed mid-rebase, got %v", err)
	}
	t.Setenv("EDITOR", "true")
	if _, _, err := run(t, app, "note", "edit", "--scope", "wc"); err != nil {
		t.Errorf("note edit must stay allowed mid-rebase, got %v", err)
	}
}
