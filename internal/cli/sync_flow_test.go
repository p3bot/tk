package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/syncengine"
)

// A one-sided tk mark done lands uncontested on the other machine after sync.
func TestSyncOneSidedStatusLandsUncontested(t *testing.T) {
	requireGit(t)
	a, b, _ := twoMachines(t)

	a.mark(t, "wc-ab2c", "done")
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A sync: %v", err)
	}

	if _, _, err := b.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("B sync: %v", err)
	}
	path, archived := findTicket(t, b.scopeDir(), "wc-ab2c-alpha.md")
	if path == "" || !archived {
		t.Fatalf("the completed ticket should have landed under archive/ on B")
	}
	if st := fmStatus(t, path); st != "done" {
		t.Errorf("B should see status done, got %q", st)
	}
}

func TestSyncMultiStopRebaseCompletes(t *testing.T) {
	requireGit(t)
	remote := newBareRemote(t)
	a := cloneMachine(t, remote)
	dirA := a.initScopeAutoCommit(t)
	addTicket(t, dirA, "wc-ab2c", "one", "todo", "a0", "# One\n", false, "")
	addTicket(t, dirA, "wc-cd3e", "two", "todo", "a1", "# Two\n", false, "")
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A seed sync: %v", err)
	}
	b := cloneMachine(t, remote)
	dirB := b.importScope(t)

	a.mark(t, "wc-ab2c", "review")
	a.mark(t, "wc-cd3e", "review")
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A sync: %v", err)
	}

	b.mark(t, "wc-ab2c", "blocked")
	b.mark(t, "wc-cd3e", "blocked")
	_, errOut, err := b.sync(t, "--scope", "wc")
	if err != nil {
		t.Fatalf("B multi-stop sync should complete in one invocation: %v (stderr %q)", err, errOut)
	}
	if strings.Contains(errOut, "paused") {
		t.Errorf("an auto-resolvable multi-stop rebase must not report a human handoff, got %q", errOut)
	}
	p1, _ := findTicket(t, dirB, "wc-ab2c-one.md")
	p2, _ := findTicket(t, dirB, "wc-cd3e-two.md")
	if fmStatus(t, p1) == "todo" || fmStatus(t, p2) == "todo" {
		t.Errorf("both tickets should have merged past the base status")
	}
	if n := gitIn(t, b.clone, "rev-list", "--count", "@{u}..HEAD"); n != "0" {
		t.Errorf("B should be fully pushed after the multi-stop sync, unpushed=%s", n)
	}
}

func TestSyncBodyConflictPausesThenResumes(t *testing.T) {
	requireGit(t)
	a, b, remote := twoMachines(t)

	pA, _ := findTicket(t, a.scopeDir(), "wc-ab2c-alpha.md")
	editBody(t, pA, "A version of the body")
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A body edit sync: %v", err)
	}

	pB, _ := findTicket(t, b.scopeDir(), "wc-ab2c-alpha.md")
	editBody(t, pB, "B version of the body")
	_, errOut, err := b.sync(t, "--scope", "wc")
	if ExitCodeFromError(err) != exitFailure {
		t.Fatalf("body conflict must pause non-zero, got %v (stderr %q)", err, errOut)
	}
	if !strings.Contains(errOut, "body conflict") {
		t.Errorf("a body conflict should be reported, got %q", errOut)
	}
	if syncengine.FrontmatterHasMarkers(pB) {
		t.Errorf("frontmatter must be clean field-merged, not carry markers:\n%s", readFile(t, pB))
	}
	if !syncengine.HasConflictMarker([]byte(readFile(t, pB))) {
		t.Errorf("the body should carry conflict markers awaiting the human:\n%s", readFile(t, pB))
	}

	resolved := stripConflictMarkers(readFile(t, pB), "resolved body")
	if err := os.WriteFile(pB, []byte(resolved), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("post-resolution sync should complete: %v", err)
	}
	if n := gitIn(t, b.clone, "rev-list", "--count", "@{u}..HEAD"); n != "0" {
		t.Errorf("B should be fully pushed after resolving the body, unpushed=%s", n)
	}
	_ = remote
}

func TestSyncMidRebaseEntryMakesNoCommitBeforeResume(t *testing.T) {
	requireGit(t)
	a, b, remote := twoMachines(t)

	pA, _ := findTicket(t, a.scopeDir(), "wc-ab2c-alpha.md")
	editBody(t, pA, "A version")
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A sync: %v", err)
	}
	pB, _ := findTicket(t, b.scopeDir(), "wc-ab2c-alpha.md")
	editBody(t, pB, "B version")
	if _, _, err := b.sync(t, "--scope", "wc"); ExitCodeFromError(err) != exitFailure {
		t.Fatalf("expected a paused body conflict, got %v", err)
	}

	resolved := stripConflictMarkers(readFile(t, pB), "resolved body")
	if err := os.WriteFile(pB, []byte(resolved), 0o644); err != nil {
		t.Fatal(err)
	}
	addTicket(t, b.scopeDir(), "wc-ff88", "extra", "todo", "a5", "# Extra\n", false, "")

	if _, _, err := b.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("resume + snapshot sync should complete: %v", err)
	}
	if !remoteHas(t, remote, "wc/wc-ff88-extra.md") {
		t.Errorf("the unrelated file must be snapshotted after the rebase completed and pushed (never orphaned on temp HEAD)")
	}
}

func TestSyncDeleteEditPausesThenResumes(t *testing.T) {
	requireGit(t)
	a, b, remote := twoMachines(t)

	pA, _ := findTicket(t, a.scopeDir(), "wc-ab2c-alpha.md")
	if err := os.Remove(pA); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A delete sync: %v", err)
	}

	b.mark(t, "wc-ab2c", "review")
	_, errOut, err := b.sync(t, "--scope", "wc")
	if ExitCodeFromError(err) != exitFailure {
		t.Fatalf("delete/edit must pause non-zero, got %v (stderr %q)", err, errOut)
	}
	if !strings.Contains(errOut, "delete/edit") || !strings.Contains(errOut, "review") {
		t.Errorf("delete/edit should report the surviving status, got %q", errOut)
	}
	assertDeleteEditHandoff(t, errOut, "wc/wc-ab2c-alpha.md", "review")

	pB, _ := findTicket(t, b.scopeDir(), "wc-ab2c-alpha.md")
	if pB != "" {
		if err := os.Remove(pB); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := b.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("post-resolution delete sync should complete: %v", err)
	}
	if remoteHas(t, remote, "wc/wc-ab2c-alpha.md") {
		t.Errorf("the removed ticket should not be on the remote after resolution")
	}
}

func TestSyncDeleteEditUnactionedRerunPauses(t *testing.T) {
	requireGit(t)
	a, b, remote := twoMachines(t)

	pA, _ := findTicket(t, a.scopeDir(), "wc-ab2c-alpha.md")
	if err := os.Remove(pA); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A delete sync: %v", err)
	}

	b.mark(t, "wc-ab2c", "review")
	_, firstOut, err := b.sync(t, "--scope", "wc")
	if ExitCodeFromError(err) != exitFailure {
		t.Fatalf("delete/edit must pause non-zero, got %v (stderr %q)", err, firstOut)
	}
	assertDeleteEditHandoff(t, firstOut, "wc/wc-ab2c-alpha.md", "review")

	// Nothing touched — the next tk sync must re-pause, not silently resurrect the file.
	_, secondOut, err := b.sync(t, "--scope", "wc")
	if ExitCodeFromError(err) != exitFailure {
		t.Fatalf("unactioned re-run must stay paused, got %v (stderr %q)", err, secondOut)
	}
	assertDeleteEditHandoff(t, secondOut, "wc/wc-ab2c-alpha.md", "review")
	assertSameDeleteEditLine(t, firstOut, secondOut)
	if remoteHas(t, remote, "wc/wc-ab2c-alpha.md") {
		t.Errorf("unactioned re-run must not push the resurrected survivor; remote still carries the deletion")
	}
}

func TestSyncDeleteEditModifiedResumes(t *testing.T) {
	requireGit(t)
	a, b, remote := twoMachines(t)

	pA, _ := findTicket(t, a.scopeDir(), "wc-ab2c-alpha.md")
	if err := os.Remove(pA); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A delete sync: %v", err)
	}

	b.mark(t, "wc-ab2c", "review")
	if _, _, err := b.sync(t, "--scope", "wc"); ExitCodeFromError(err) != exitFailure {
		t.Fatalf("expected delete/edit pause, got %v", err)
	}

	pB := mustSeedTicket(t, b.scopeDir())
	editBody(t, pB, "human kept and edited the survivor")
	if _, _, err := b.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("modified-survivor re-run should complete: %v", err)
	}
	if !remoteHas(t, remote, "wc/wc-ab2c-alpha.md") {
		t.Errorf("the human's modified survivor should land on the remote")
	}
	if !strings.Contains(readFile(t, mustSeedTicket(t, b.scopeDir())), "human kept and edited the survivor") {
		t.Errorf("the human's content should be what was staged")
	}
}

func TestSyncDeleteEditGitAddResumes(t *testing.T) {
	requireGit(t)
	a, b, remote := twoMachines(t)

	pA, _ := findTicket(t, a.scopeDir(), "wc-ab2c-alpha.md")
	if err := os.Remove(pA); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A delete sync: %v", err)
	}

	b.mark(t, "wc-ab2c", "review")
	if _, _, err := b.sync(t, "--scope", "wc"); ExitCodeFromError(err) != exitFailure {
		t.Fatalf("expected delete/edit pause, got %v", err)
	}

	pB := mustSeedTicket(t, b.scopeDir())
	before := readFile(t, pB)
	gitIn(t, b.clone, "add", "--", filepath.Join("wc", filepath.Base(pB)))
	if _, _, err := b.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("git-add resolution re-run should complete: %v", err)
	}
	if !remoteHas(t, remote, "wc/wc-ab2c-alpha.md") {
		t.Errorf("the git-add-ed survivor should land on the remote")
	}
	if got := readFile(t, mustSeedTicket(t, b.scopeDir())); got != before {
		t.Errorf("git add keeps exact content; worktree drifted")
	}
}

func TestSyncDeleteEditMirroredUnactionedRerunPauses(t *testing.T) {
	requireGit(t)
	a, b, remote := twoMachines(t)

	a.mark(t, "wc-ab2c", "review")
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A edit sync: %v", err)
	}

	pB, _ := findTicket(t, b.scopeDir(), "wc-ab2c-alpha.md")
	if err := os.Remove(pB); err != nil {
		t.Fatal(err)
	}
	_, firstOut, err := b.sync(t, "--scope", "wc")
	if ExitCodeFromError(err) != exitFailure {
		t.Fatalf("mirrored delete/edit must pause non-zero, got %v (stderr %q)", err, firstOut)
	}
	assertDeleteEditHandoff(t, firstOut, "wc/wc-ab2c-alpha.md", "review")
	if !strings.Contains(firstOut, "this machine's replayed commit") {
		t.Errorf("mirrored case should name this machine as the deleting side, got %q", firstOut)
	}

	_, secondOut, err := b.sync(t, "--scope", "wc")
	if ExitCodeFromError(err) != exitFailure {
		t.Fatalf("unactioned mirrored re-run must stay paused, got %v (stderr %q)", err, secondOut)
	}
	assertDeleteEditHandoff(t, secondOut, "wc/wc-ab2c-alpha.md", "review")
	assertSameDeleteEditLine(t, firstOut, secondOut)
	// Remote still has A's edit (the survivor was never pushed from B's resurrection path).
	if !remoteHas(t, remote, "wc/wc-ab2c-alpha.md") {
		t.Errorf("remote should still carry A's edit while B's delete/edit is paused")
	}
}

func TestSyncDeleteEditUnparseableSurvivorFailClosed(t *testing.T) {
	requireGit(t)
	a, b, _ := twoMachines(t)

	pA, _ := findTicket(t, a.scopeDir(), "wc-ab2c-alpha.md")
	if err := os.Remove(pA); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A delete sync: %v", err)
	}

	// B's concurrent "edit" is a mangled frontmatter blob the merge cannot parse.
	pB := mustSeedTicket(t, b.scopeDir())
	broken := "---\nid: wc-ab2c\nstatus: [unterminated\norder: a0\n---\n# alpha\n\nbody line\n"
	if err := os.WriteFile(pB, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	_, firstOut, err := b.sync(t, "--scope", "wc")
	if ExitCodeFromError(err) != exitFailure {
		t.Fatalf("unparseable survivor must pause non-zero, got %v (stderr %q)", err, firstOut)
	}
	if !strings.Contains(firstOut, "config_unparseable:") || !strings.Contains(firstOut, "unparseable") {
		t.Errorf("first pause must fail-closed naming the parse fault, got %q", firstOut)
	}
	if strings.Contains(firstOut, "delete/edit") {
		t.Errorf("unparseable survivor must not be reported as delete/edit, got %q", firstOut)
	}

	_, secondOut, err := b.sync(t, "--scope", "wc")
	if ExitCodeFromError(err) != exitFailure {
		t.Fatalf("unactioned re-run must stay paused, got %v (stderr %q)", err, secondOut)
	}
	if !strings.Contains(secondOut, "config_unparseable:") || !strings.Contains(secondOut, "unparseable") {
		t.Errorf("re-run must re-report the same fail-closed handoff, got %q", secondOut)
	}
	if strings.Contains(secondOut, "delete/edit") {
		t.Errorf("re-run must not reclassify as delete/edit, got %q", secondOut)
	}
}

func assertDeleteEditHandoff(t *testing.T, errOut, path, survivingStatus string) {
	t.Helper()
	if !strings.Contains(errOut, "delete/edit") {
		t.Errorf("expected delete/edit handoff, got %q", errOut)
	}
	if !strings.Contains(errOut, path) {
		t.Errorf("expected path %q in handoff, got %q", path, errOut)
	}
	if !strings.Contains(errOut, survivingStatus) {
		t.Errorf("expected surviving status %q in handoff, got %q", survivingStatus, errOut)
	}
	if strings.Contains(errOut, " ours") || strings.Contains(errOut, " theirs") ||
		strings.Contains(errOut, "the ours") || strings.Contains(errOut, "the theirs") {
		t.Errorf("handoff must not name sides as ours/theirs, got %q", errOut)
	}
	if !strings.Contains(errOut, "incoming side") && !strings.Contains(errOut, "this machine's replayed commit") {
		t.Errorf("handoff must name the deleting side in reader terms, got %q", errOut)
	}
	for _, way := range []string{"remove " + path, "edit it", "git add"} {
		if !strings.Contains(errOut, way) {
			t.Errorf("handoff must name %q as a way out, got %q", way, errOut)
		}
	}
	if strings.Contains(errOut, "restore or remove") {
		t.Errorf("handoff must not use the old restore-or-remove wording, got %q", errOut)
	}
}

func assertSameDeleteEditLine(t *testing.T, firstOut, secondOut string) {
	t.Helper()
	first, second := extractDeleteEditLine(firstOut), extractDeleteEditLine(secondOut)
	if first == "" || second == "" {
		t.Fatalf("missing delete/edit line: first=%q second=%q", first, second)
	}
	if first != second {
		t.Errorf("first pause and re-run must print the same delete/edit line:\n  first:  %s\n  second: %s", first, second)
	}
}

func extractDeleteEditLine(errOut string) string {
	for _, line := range strings.Split(errOut, "\n") {
		if strings.Contains(line, "delete/edit") {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

func TestSyncSnapshotMessageClasses(t *testing.T) {
	requireGit(t)
	remote := newBareRemote(t)
	m := cloneMachine(t, remote)
	dir := m.initScopeAutoCommit(t)

	addTicket(t, dir, "wc-ab2c", "alpha", "todo", "a0", "# Alpha\n", false, "")
	if _, _, err := m.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("multi-path sync: %v", err)
	}
	if got := topCommit(t, m.clone); !strings.HasPrefix(got, "tk: sync ") || !strings.HasSuffix(got, "path(s)") {
		t.Errorf("a multi-path snapshot should use the summary message, got %q", got)
	}

	if err := os.Remove(filepath.Join(dir, "wc-ab2c-alpha.md")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("delete sync: %v", err)
	}
	if got := topCommit(t, m.clone); got != "tk: remove wc-ab2c" {
		t.Errorf("a lone hand-deletion should commit as tk: remove wc-ab2c, got %q", got)
	}
}

func TestSyncUnownableConflictNamesThePath(t *testing.T) {
	requireGit(t)
	a, b, _ := twoMachines(t)

	if err := os.WriteFile(filepath.Join(a.clone, "shared.txt"), []byte("A version\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, a.clone, "add", "shared.txt")
	gitIn(t, a.clone, "commit", "-m", "add shared (A)")
	if _, _, err := a.sync(t, "--scope", "wc"); err != nil {
		t.Fatalf("A sync: %v", err)
	}

	if err := os.WriteFile(filepath.Join(b.clone, "shared.txt"), []byte("B version\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, b.clone, "add", "shared.txt")
	gitIn(t, b.clone, "commit", "-m", "add shared (B)")

	_, errOut, err := b.sync(t, "--scope", "wc")
	if ExitCodeFromError(err) != exitFailure {
		t.Fatalf("an unownable conflict must pause non-zero, got %v (stderr %q)", err, errOut)
	}
	if !strings.Contains(errOut, "unresolvable conflict") || !strings.Contains(errOut, "shared.txt") {
		t.Errorf("the paused rebase must name the unresolvable path, got %q", errOut)
	}
}

func stripConflictMarkers(content, resolvedBody string) string {
	var out []string
	skip := false
	replaced := false
	for _, line := range strings.Split(content, "\n") {
		switch {
		case strings.HasPrefix(line, "<<<<<<<"):
			skip = true
			if !replaced {
				out = append(out, resolvedBody)
				replaced = true
			}
		case strings.HasPrefix(line, "======="):
		case strings.HasPrefix(line, ">>>>>>>"):
			skip = false
		case !skip:
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}
