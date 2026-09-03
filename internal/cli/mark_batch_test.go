package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/token"
)

func TestMarkStatusFirstAndOldOrderUsage(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	_, id := createID(t, app, "wc", "Work")

	out, _, err := run(t, app, "mark", "todo", id)
	if err != nil {
		t.Fatalf("status-first mark: %v", err)
	}
	if got := fmValue(t, strings.TrimSpace(out), "status"); got != "todo" {
		t.Errorf("status = %q", got)
	}

	wantUnknown := fmt.Sprintf("%q is not a known status", id)
	_, _, err = run(t, app, "mark", id, "todo")
	if ExitCodeFromError(err) != exitUsage {
		t.Fatalf("id as status must be usage 2, got %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), wantUnknown) {
		t.Errorf("id as status: got %v, want %q", err, wantUnknown)
	}
	if err != nil && strings.Contains(err.Error(), "not a valid ticket id") {
		t.Errorf("must not diagnose the status slot as an id: %v", err)
	}

	_, _, err = run(t, app, "mark", id)
	if ExitCodeFromError(err) != exitUsage {
		t.Fatalf("mark <id> must be usage 2, got %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), wantUnknown) {
		t.Errorf("mark <id>: got %v, want %q", err, wantUnknown)
	}

	_, _, err = run(t, app, "mark", id, "draft")
	if ExitCodeFromError(err) != exitUsage {
		t.Fatalf("id then draft must be usage 2, got %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), wantUnknown) {
		t.Errorf("id then draft: got %v, want %q", err, wantUnknown)
	}
	if err != nil && strings.Contains(err.Error(), "unknown ticket") {
		t.Errorf("must not look up draft as a ticket: %v", err)
	}

	root := filepath.Join(dir, filepath.Base(strings.TrimSpace(out)))
	if got := fmValue(t, root, "status"); got != "todo" {
		t.Errorf("bad argv must not write, status %q", got)
	}
}

func TestMarkCustomStatusThatLooksLikeID(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	path, id := createID(t, app, "wc", "Work")

	_, _, err := run(t, app, "mark", "parked", id)
	if ExitCodeFromError(err) != exitUsage {
		t.Fatalf("undeclared parked: want usage 2, got %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), `"parked" is not a known status`) {
		t.Errorf("undeclared parked: got %v", err)
	}
	_, _, err = run(t, app, "mark", "parked", "--scope", "wc")
	if ExitCodeFromError(err) != exitUsage {
		t.Fatalf("undeclared parked one-arg: want usage 2, got %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), `"parked" is not a known status`) {
		t.Errorf("undeclared parked one-arg: got %v", err)
	}

	writeCue(t, dir, "name: \"wc\"\nautoCommit: false\nstatuses: {\n  parked: {category: \"backlog\"}\n  \"qa-pass\": {category: \"active\"}\n}\n")

	out, _, err := run(t, app, "mark", "parked", id)
	if err != nil {
		t.Fatalf("declared parked: %v", err)
	}
	if got := fmValue(t, strings.TrimSpace(out), "status"); got != "parked" {
		t.Errorf("parked status = %q", got)
	}

	out, _, err = run(t, app, "mark", "qa-pass", id)
	if err != nil {
		t.Fatalf("declared qa-pass: %v", err)
	}
	if got := fmValue(t, strings.TrimSpace(out), "status"); got != "qa-pass" {
		t.Errorf("qa-pass status = %q", got)
	}

	for _, st := range []string{"parked", "qa-pass"} {
		_, _, err = run(t, app, "mark", st, "--scope", "wc")
		if ExitCodeFromError(err) != exitUsage {
			t.Fatalf("declared %s one-arg: want usage 2, got %v", st, err)
		}
		if err == nil || !strings.HasPrefix(err.Error(), "missing <id>\n") {
			t.Errorf("declared %s one-arg: got %v, want missing <id>", st, err)
		}
		if err != nil && strings.Contains(err.Error(), "not a known status") {
			t.Errorf("declared %s one-arg must not be unknown status: %v", st, err)
		}
	}

	_, _, err = run(t, app, "mark", id, "todo")
	if ExitCodeFromError(err) != exitUsage {
		t.Fatalf("old argv still usage 2, got %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("%q is not a known status", id)) {
		t.Errorf("old argv: got %v", err)
	}
	if got := fmValue(t, path, "status"); got != "qa-pass" {
		t.Errorf("old argv must not write, status %q", got)
	}
}

func TestMarkCustomStatusUnparseableConfig(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	_, id := createID(t, app, "wc", "Work")
	writeCue(t, dir, "name: \"wc\"\nautoCommit: false\nstatuses: {\n  parked: {category: \"backlog\"}\n}\n")

	if _, _, err := run(t, app, "mark", "parked", id); err != nil {
		t.Fatalf("declared parked: %v", err)
	}

	bad := "name: \"wc\"\nautoCommit: false\nfields: {x: {type: \"float\"}}\n"
	if err := os.WriteFile(filepath.Join(dir, "tk.cue"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}

	_, errOut, err := run(t, app, "mark", "parked", id)
	if ExitCodeFromError(err) != exitFailure {
		t.Fatalf("want exit 1, got %v (stderr %q)", err, errOut)
	}
	if err == nil || !strings.Contains(err.Error()+errOut, "config_unparseable:") {
		t.Errorf("want config_unparseable, got err=%v stderr=%q", err, errOut)
	}
	if err != nil && strings.Contains(err.Error(), "not a known status") {
		t.Errorf("must not diagnose as unknown status: %v", err)
	}

	_, _, err = run(t, app, "mark", "parked", "--scope", "wc")
	if ExitCodeFromError(err) != exitUsage {
		t.Fatalf("one-arg unusable: want usage 2, got %v", err)
	}
	if err == nil || !strings.HasPrefix(err.Error(), "missing <id>\n") {
		t.Errorf("one-arg unusable: got %v, want missing <id>", err)
	}
}

func TestMarkBatchWritesOnceAndPrintsArgvOrder(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	_, repo := initGitScope(t, app, "wc", true)

	_, a := createID(t, app, "wc", "Alpha")
	_, b := createID(t, app, "wc", "Beta")
	_, c := createID(t, app, "wc", "Gamma")

	out, _, err := run(t, app, "mark", "todo", a, b, c)
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	paths := lines(out)
	if len(paths) != 3 {
		t.Fatalf("stdout lines = %d, want 3: %q", len(paths), out)
	}
	for i, id := range []string{a, b, c} {
		if !strings.Contains(paths[i], id) {
			t.Errorf("stdout[%d] = %q, want id %s", i, paths[i], id)
		}
		if got := fmValue(t, paths[i], "status"); got != "todo" {
			t.Errorf("%s status = %q", id, got)
		}
	}
	log := gitLog(t, repo)
	if len(log) != 1 || log[0] != "tk: 3 tickets -> todo" {
		t.Fatalf("want one batch commit, got %v", log)
	}
}

func TestMarkBatchRefusesUnknownAndCrossScope(t *testing.T) {
	app := newApp(t)
	initScope(t, app, "wc")
	initScope(t, app, "xy")
	_, a := createID(t, app, "wc", "Alpha")
	_, b := createID(t, app, "wc", "Beta")
	_, other := createID(t, app, "xy", "Other")

	pathA, _, err := run(t, app, "get", a)
	if err != nil {
		t.Fatal(err)
	}
	pathA = strings.TrimSpace(pathA)

	_, _, err = run(t, app, "mark", "todo", a, "wc-zzzz")
	if ExitCodeFromError(err) != exitFailure {
		t.Fatalf("unknown id: want exit 1, got %v", err)
	}
	if got := fmValue(t, pathA, "status"); got != "draft" {
		t.Errorf("unknown id must not write, status %q", got)
	}

	_, _, err = run(t, app, "mark", "todo", a, other)
	if ExitCodeFromError(err) != exitUsage {
		t.Fatalf("cross-scope: want usage 2, got %v", err)
	}
	if got := fmValue(t, pathA, "status"); got != "draft" {
		t.Errorf("cross-scope must not write, status %q", got)
	}

	shortB := strings.SplitN(b, "-", 2)[1]
	out, _, err := run(t, app, "mark", "todo", a, shortB, "--scope", "xy")
	if err != nil {
		t.Fatalf("full then short in first-id scope: %v", err)
	}
	paths := lines(out)
	if len(paths) != 2 {
		t.Fatalf("want 2 paths, got %q", out)
	}
	if got := fmValue(t, paths[0], "status"); got != "todo" {
		t.Errorf("a status = %q", got)
	}
	if got := fmValue(t, paths[1], "status"); got != "todo" {
		t.Errorf("b status = %q", got)
	}
	otherPath, _, err := run(t, app, "get", other)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmValue(t, strings.TrimSpace(otherPath), "status"); got != "draft" {
		t.Errorf("xy ticket must stay draft, got %q", got)
	}
}

func TestMarkBatchCollapsesRepeats(t *testing.T) {
	app := newApp(t)
	initScope(t, app, "wc")
	_, a := createID(t, app, "wc", "Alpha")
	_, b := createID(t, app, "wc", "Beta")
	shortA := strings.SplitN(a, "-", 2)[1]

	if _, _, err := run(t, app, "me", a, "--scope", "wc"); err != nil {
		t.Fatalf("me: %v", err)
	}

	out, _, err := run(t, app, "mark", "todo", a, a, shortA, "me", b, "--scope", "wc")
	if err != nil {
		t.Fatalf("collapse: %v", err)
	}
	paths := lines(out)
	if len(paths) != 2 {
		t.Fatalf("want 2 unique paths, got %d: %q", len(paths), out)
	}
	if !strings.Contains(paths[0], a) || !strings.Contains(paths[1], b) {
		t.Errorf("first-seen order: %q", out)
	}
}

func TestMarkBatchOnDiskDuplicateStillRefuses(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	path, id := createID(t, app, "wc", "Work")
	_, other := createID(t, app, "wc", "Other")
	if err := copyFile(path, filepath.Join(dir, id+"-dup.md")); err != nil {
		t.Fatal(err)
	}

	_, _, err := run(t, app, "mark", "todo", id, other)
	if ExitCodeFromError(err) != exitFailure {
		t.Fatalf("duplicate_id batch must refuse, got %v", err)
	}
	if got := fmValue(t, path, "status"); got != "draft" {
		t.Errorf("must not write, status %q", got)
	}
}

func TestMarkBatchDraftToTodoDoesNotRefreshOrPush(t *testing.T) {
	requireGit(t)
	a, b, _ := twoMachines(t)

	addTicket(t, a.scopeDir(), "wc-cd3e", "beta", "draft", "a1", "# beta\n\nbody line\n", false, "")
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A sync: %v", err)
	}
	if _, _, err := b.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("B pull: %v", err)
	}

	editBody(t, mustSeedTicket(t, a.scopeDir()), "A remote body")
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A body sync: %v", err)
	}

	addTicket(t, b.scopeDir(), "wc-fg4h", "gamma", "draft", "a2", "# gamma\n", false, "")
	tracking := strings.TrimSpace(gitIn(t, b.clone, "rev-parse", "origin/main"))
	out, _, err := run(t, b.app, "mark", "todo", "wc-cd3e", "wc-fg4h")
	if err != nil {
		t.Fatalf("draft→todo: %v", err)
	}
	if strings.TrimSpace(gitIn(t, b.clone, "rev-parse", "origin/main")) != tracking {
		t.Error("non-claim batch must not fetch")
	}
	if n := gitIn(t, b.clone, "rev-list", "--count", "@{u}..HEAD"); n == "0" {
		t.Error("local self-commit should be unpushed")
	}
	for _, p := range lines(out) {
		if got := fmStatus(t, p); got != "todo" {
			t.Errorf("%s status = %q", p, got)
		}
	}
	body, err := os.ReadFile(mustSeedTicket(t, b.scopeDir()))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "A remote body") {
		t.Error("draft→todo must not refresh remote body into the named todo")
	}
}

func TestMarkInProgressZeroTodosDoesNotRefresh(t *testing.T) {
	requireGit(t)
	a, b, _ := twoMachines(t)

	addTicket(t, a.scopeDir(), "wc-cd3e", "beta", "draft", "a1", "# beta\n", false, "")
	addTicket(t, a.scopeDir(), "wc-fg4h", "gamma", "draft", "a2", "# gamma\n", false, "")
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A sync: %v", err)
	}
	if _, _, err := b.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("B pull: %v", err)
	}

	a.mark(t, "wc-ab2c", "review")
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A review sync: %v", err)
	}

	tracking := strings.TrimSpace(gitIn(t, b.clone, "rev-parse", "origin/main"))
	out, _, err := run(t, b.app, "mark", "in-progress", "wc-cd3e", "wc-fg4h")
	if err != nil {
		t.Fatalf("zero-todo in-progress: %v", err)
	}
	if strings.TrimSpace(gitIn(t, b.clone, "rev-parse", "origin/main")) != tracking {
		t.Error("in-progress with no todos must not refresh")
	}
	paths := lines(out)
	if len(paths) != 2 {
		t.Fatalf("stdout = %q", out)
	}
	for _, p := range paths {
		if got := fmStatus(t, p); got != "in-progress" {
			t.Errorf("%s status = %q", p, got)
		}
	}
}

func TestMarkBatchDependsOpenUsesPostWriteIndex(t *testing.T) {
	app := newApp(t)
	initScope(t, app, "wc")

	_, depID := createID(t, app, "wc", "Dep")
	_, subID := createID(t, app, "wc", "Sub")
	if _, _, err := run(t, app, "meta", "add", subID, "depends", depID); err != nil {
		t.Fatalf("depends: %v", err)
	}

	want := token.FormatDependsOpen(subID, "todo", []string{depID})
	for _, args := range [][]string{
		{"mark", "todo", subID, depID},
		{"mark", "todo", depID, subID},
	} {
		if _, _, err := run(t, app, "mark", "done", subID, depID); err != nil {
			t.Fatalf("archive before %v: %v", args[2:], err)
		}
		out, errOut, err := run(t, app, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if strings.Contains(out, token.DependsOpen) {
			t.Errorf("%v depends_open on stdout: %q", args, out)
		}
		if !strings.Contains(errOut, want) {
			t.Errorf("%v stderr want %q, got %q", args, want, errOut)
		}
	}
}
