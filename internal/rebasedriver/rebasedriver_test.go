package rebasedriver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cuelang.org/go/cue/cuecontext"
	"github.com/p3bot/tk/internal/frontmatter"
	"github.com/p3bot/tk/internal/git"
	"github.com/p3bot/tk/internal/scopeconfig"
	"github.com/p3bot/tk/internal/testgit"
)

func requireGit(t *testing.T) {
	t.Helper()
	if !git.Available() {
		t.Skip("git not on PATH")
	}
	testgit.Hermetic(t)
}

func gitRun(t *testing.T, dir string, env []string, args ...string) {
	t.Helper()
	_ = testgit.CombinedEnv(t, dir, env, args...)
}

func gitCapture(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return testgit.Combined(t, dir, args...)
}

func newRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	gitRun(t, repo, nil, "init")
	gitRun(t, repo, nil, "config", "user.email", "a@b.c")
	gitRun(t, repo, nil, "config", "user.name", "tk-test")
	gitRun(t, repo, nil, "config", "commit.gpgsign", "false")
	return repo
}

func writeF(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func dateEnv(iso string) []string {
	if iso == "" {
		return nil
	}
	return []string{"GIT_AUTHOR_DATE=" + iso, "GIT_COMMITTER_DATE=" + iso}
}

func commitAll(t *testing.T, repo, msg, date string) string {
	t.Helper()
	gitRun(t, repo, nil, "add", "-A")
	gitRun(t, repo, dateEnv(date), "commit", "-m", msg)
	return gitCapture(t, repo, "rev-parse", "HEAD")
}

// startRebase runs git rebase onto non-interactively and reports whether it paused.
func startRebase(t *testing.T, repo, onto string) bool {
	t.Helper()
	_ = testgit.AllowFailure(t, repo, []string{"GIT_EDITOR=true"}, "rebase", onto)
	return git.MidRebase(context.Background(), repo)
}

func freshLoader() SchemaLoader {
	cctx := cuecontext.New()
	return func(dir string) (*scopeconfig.Schema, error) {
		s, _, err := scopeconfig.LoadWithClosure(cctx, dir)
		return s, err
	}
}

func tkcue(extra string) string {
	return "name: \"wc\"\nautoCommit: true\n" + extra
}

func proj(fm, body string) string { return "---\n" + fm + "---\n" + body }

// setup builds a paused rebase over a single scope.
// main = ours/:2, feature = theirs/:3. Empty content means the file is absent on that branch.
type fixture struct {
	repo, scopeDir, projRel, oursRev, theirsRev string
}

func setup(t *testing.T, cueBase, cueMain, base, main, feature, mainDate string) fixture {
	t.Helper()
	projRel := "wc/wc-ab2c-x.md"
	const baseDate = "2026-01-01T00:00:00Z"
	const featureDate = "2026-03-01T00:00:00Z"
	repo := newRepo(t)
	scopeDir := filepath.Join(repo, "wc")
	cuePath := filepath.Join(scopeDir, "tk.cue")
	projPath := filepath.Join(repo, projRel)

	writeF(t, cuePath, cueBase)
	if base != "" {
		writeF(t, projPath, base)
	}
	commitAll(t, repo, "base", baseDate)
	mainBranch := gitCapture(t, repo, "rev-parse", "--abbrev-ref", "HEAD")

	gitRun(t, repo, nil, "checkout", "-b", "feature")
	applyBranch(t, projPath, feature)
	featureTip := commitAll(t, repo, "feature", featureDate)

	gitRun(t, repo, nil, "checkout", mainBranch)
	if cueMain != "" {
		writeF(t, cuePath, cueMain)
	}
	applyBranch(t, projPath, main)
	mainTip := commitAll(t, repo, "main", mainDate)

	gitRun(t, repo, nil, "checkout", "feature")
	if !startRebase(t, repo, mainBranch) {
		t.Fatal("setup: expected a paused rebase")
	}
	return fixture{repo: repo, scopeDir: scopeDir, projRel: projRel, oursRev: mainTip, theirsRev: featureTip}
}

// applyBranch writes content, or removes the file when content is empty.
func applyBranch(t *testing.T, path, content string) {
	t.Helper()
	if content == "" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		return
	}
	writeF(t, path, content)
}

func (f fixture) conflict() Conflict {
	return Conflict{Path: f.projRel, ScopeDir: f.scopeDir, OursRev: f.oursRev, TheirsRev: f.theirsRev}
}

func resolve(t *testing.T, f fixture) Outcome {
	t.Helper()
	d := New(f.repo, freshLoader())
	out, err := d.Resolve(context.Background(), f.conflict())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return out
}

func parseFile(t *testing.T, path string) *frontmatter.Model {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	interior, _, present := frontmatter.Split(raw)
	if !present {
		t.Fatalf("no frontmatter fence in %s", path)
	}
	m, err := frontmatter.Parse(interior)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return m
}

// Both sides change frontmatter and bodies merge cleanly: stage a parseable file
// where git's working-tree file would not parse.
func TestCleanTwoSidedFrontmatterStaged(t *testing.T) {
	requireGit(t)
	fmBase := "id: wc-ab2c\nstatus: todo\norder: \"a0\"\nsummary: BASE\ncreated: 2026-01-01T00:00:00Z\n"
	fmMain := "id: wc-ab2c\nstatus: todo\norder: \"a0\"\nsummary: MAIN\ncreated: 2026-01-01T00:00:00Z\n"
	fmFeat := "id: wc-ab2c\nstatus: todo\norder: \"a0\"\nsummary: FEAT\ncreated: 2026-01-01T00:00:00Z\n"
	body := "L1\nL2\nL3\nL4\nL5\nL6\nL7\n"
	mainBody := "M1\nL2\nL3\nL4\nL5\nL6\nL7\n"
	featBody := "L1\nL2\nL3\nL4\nL5\nL6\nF7\n"

	f := setup(t,
		tkcue(""), "",
		proj(fmBase, body), proj(fmMain, mainBody), proj(fmFeat, featBody),
		"2026-06-01T00:00:00Z")

	projPath := filepath.Join(f.repo, f.projRel)
	// Precondition: git's working-tree file should not have parseable frontmatter.
	rawLeft, _ := os.ReadFile(projPath)
	if interior, _, present := frontmatter.Split(rawLeft); present {
		if _, err := frontmatter.Parse(interior); err == nil {
			t.Fatal("precondition: git's working-tree file should not have parseable frontmatter")
		}
	}

	out := resolve(t, f)
	if out.Class != ClassClean || !out.Staged {
		t.Fatalf("want clean+staged, got class=%v staged=%v", out.Class, out.Staged)
	}
	m := parseFile(t, projPath)
	if m.Summary != "MAIN" { // ours (main) is newer → LWW
		t.Errorf("summary = %q, want MAIN (LWW)", m.Summary)
	}
	raw, _ := os.ReadFile(projPath)
	if !strings.Contains(string(raw), "M1") || !strings.Contains(string(raw), "F7") {
		t.Errorf("both clean body edits must survive: %q", raw)
	}
	if u, _ := git.ConflictStages(context.Background(), f.repo, f.projRel); u.Any() {
		t.Error("staged clean file must no longer be unmerged")
	}
}

// Body-only conflict: clean frontmatter, markers only in body, left unstaged.
func TestBodyConflictUnstaged(t *testing.T) {
	requireGit(t)
	fmBase := "id: wc-ab2c\nstatus: todo\norder: \"a0\"\n"
	fmMain := "id: wc-ab2c\nstatus: done\norder: \"a0\"\n"
	base := proj(fmBase, "a\nCOMMON\nc\n")
	main := proj(fmMain, "a\nMAIN\nc\n")
	feat := proj(fmBase, "a\nFEAT\nc\n")

	f := setup(t, tkcue(""), "", base, main, feat,
		"2026-02-01T00:00:00Z")

	out := resolve(t, f)
	if out.Class != ClassBodyConflict || out.Staged {
		t.Fatalf("want body-conflict unstaged, got class=%v staged=%v", out.Class, out.Staged)
	}
	projPath := filepath.Join(f.repo, f.projRel)
	m := parseFile(t, projPath)
	if m.Status != "done" {
		t.Errorf("frontmatter must field-merge cleanly (status done): got %q", m.Status)
	}
	raw, _ := os.ReadFile(projPath)
	if !strings.Contains(string(raw), "<<<<<<<") {
		t.Error("body must carry conflict markers")
	}
}

// Delete/edit: upstream (:2/ours) deletes, replayed side edits — proves :2=ours mapping.
func TestDeleteEditHandoffMapping(t *testing.T) {
	requireGit(t)
	base := proj("id: wc-ab2c\nstatus: todo\norder: \"a0\"\n", "body\n")
	feat := proj("id: wc-ab2c\nstatus: done\norder: \"a0\"\n", "edited\n")

	f := setup(t, tkcue(""), "", base, "", feat,
		"2026-02-01T00:00:00Z")

	before, _ := os.ReadFile(filepath.Join(f.repo, f.projRel))
	out := resolve(t, f)
	if out.Class != ClassDeleteEdit || out.Staged {
		t.Fatalf("want delete/edit unstaged, got class=%v staged=%v", out.Class, out.Staged)
	}
	if out.DeleteEdit.Deleted.String() != "ours" {
		t.Errorf("upstream side deleted; want ours, got %s", out.DeleteEdit.Deleted)
	}
	if out.DeleteEdit.SurvivingStatus != "done" {
		t.Errorf("surviving status = %q, want done", out.DeleteEdit.SurvivingStatus)
	}
	after, _ := os.ReadFile(filepath.Join(f.repo, f.projRel))
	if string(before) != string(after) {
		t.Error("delete/edit must not rewrite the working-tree file")
	}
}

// Fail-closed merge comes back as fail-closed handoff naming the key, not an operational error.
func TestFailClosedImmutableDisagreement(t *testing.T) {
	requireGit(t)
	base := proj("id: wc-ab2c\nstatus: todo\norder: \"a0\"\n", "b\n")
	main := proj("id: wc-zzzz\nstatus: todo\norder: \"a0\"\n", "b\n")
	feat := proj("id: wc-yyyy\nstatus: todo\norder: \"a0\"\n", "b\n")

	f := setup(t, tkcue(""), "", base, main, feat,
		"2026-02-01T00:00:00Z")

	out := resolve(t, f)
	if out.Class != ClassFailClosed || out.Staged {
		t.Fatalf("want fail-closed unstaged, got class=%v staged=%v", out.Class, out.Staged)
	}
	if out.FailClosed.Key != frontmatter.KeyID {
		t.Errorf("fail-closed key = %q, want id", out.FailClosed.Key)
	}
}

// Same-id add/add renames one side into two staged files — never a field-merge.
func TestAddAddRenameTwoFiles(t *testing.T) {
	requireGit(t)
	// main older (kept), feature newer (renamed).
	mainFile := proj("id: wc-ab2c\nstatus: todo\norder: \"a0\"\ncreated: 2026-01-01T00:00:00Z\n", "MAIN body\n")
	featFile := proj("id: wc-ab2c\nstatus: todo\norder: \"a0\"\ncreated: 2026-06-01T00:00:00Z\n", "FEAT body\n")

	f := setup(t, tkcue(""), "", "", mainFile, featFile,
		"2026-02-01T00:00:00Z")

	out := resolve(t, f)
	if out.Class != ClassRename || !out.Staged {
		t.Fatalf("want rename+staged, got class=%v staged=%v", out.Class, out.Staged)
	}
	if out.Rename.OldID != "wc-ab2c" || !strings.HasPrefix(out.Rename.NewID, "wc-ab2c") || out.Rename.NewID == "wc-ab2c" {
		t.Errorf("rename ids = %+v", out.Rename)
	}
	keep := parseFile(t, filepath.Join(f.repo, out.Rename.KeepPath))
	if keep.ID != "wc-ab2c" {
		t.Errorf("keep side id = %q, want wc-ab2c", keep.ID)
	}
	newM := parseFile(t, filepath.Join(f.repo, out.Rename.NewPath))
	if newM.ID != out.Rename.NewID {
		t.Errorf("renamed file id = %q, want %q", newM.ID, out.Rename.NewID)
	}
	keepRaw, _ := os.ReadFile(filepath.Join(f.repo, out.Rename.KeepPath))
	newRaw, _ := os.ReadFile(filepath.Join(f.repo, out.Rename.NewPath))
	if !strings.Contains(string(keepRaw), "MAIN body") {
		t.Errorf("keep path must hold the older (main) body")
	}
	if !strings.Contains(string(newRaw), "FEAT body") {
		t.Errorf("new path must hold the renamed (feature) body")
	}
}

// Both sides archive a short ticket. The changed line drops rename similarity
// under 50%, so git reports add/add with no stage :1. The id still names the
// pre-image file: the dispute keeps that status and changed, and does not mint.
func TestArchiveAddAddRecoversPreImageStatusDispute(t *testing.T) {
	requireGit(t)
	repo := newRepo(t)
	scopeDir := filepath.Join(repo, "wc")
	writeF(t, filepath.Join(scopeDir, "tk.cue"), tkcue(""))
	rootRel := "wc/wc-ab2c-alpha.md"
	archRel := "wc/archive/wc-ab2c-alpha.md"
	base := proj("id: wc-ab2c\nstatus: todo\nchanged: 2026-01-02T00:00:00Z\norder: \"a0\"\ncreated: 2026-01-01T00:00:00Z\n", "# alpha\n")
	writeF(t, filepath.Join(repo, rootRel), base)
	commitAll(t, repo, "base", "2026-01-01T00:00:00Z")
	mainBranch := gitCapture(t, repo, "rev-parse", "--abbrev-ref", "HEAD")

	// Side bodies diverge so the pair stays under the rename limit even when the
	// pre-image already carries changed. The real short-ticket case is covered
	// by the sync dispute test; this one pins a present base value.
	done := proj("id: wc-ab2c\nstatus: done\nchanged: \"2026-05-01T00:00:00Z\"\norder: \"a0\"\ncreated: \"2026-01-01T00:00:00Z\"\n", strings.Repeat("done side\n", 40))
	cancelled := proj("id: wc-ab2c\nstatus: cancelled\nchanged: \"2026-06-01T00:00:00Z\"\norder: \"a0\"\ncreated: \"2026-01-01T00:00:00Z\"\n", strings.Repeat("cancel side\n", 40))

	gitRun(t, repo, nil, "checkout", "-b", "feature")
	if err := os.Remove(filepath.Join(repo, rootRel)); err != nil {
		t.Fatal(err)
	}
	writeF(t, filepath.Join(repo, archRel), cancelled)
	featTip := commitAll(t, repo, "feature cancel", "2026-06-01T00:00:00Z")

	gitRun(t, repo, nil, "checkout", mainBranch)
	if err := os.Remove(filepath.Join(repo, rootRel)); err != nil {
		t.Fatal(err)
	}
	writeF(t, filepath.Join(repo, archRel), done)
	mainTip := commitAll(t, repo, "main done", "2026-05-01T00:00:00Z")

	gitRun(t, repo, nil, "checkout", "feature")
	if !startRebase(t, repo, mainBranch) {
		t.Fatal("expected paused rebase")
	}
	unmerged := git.UnmergedFiles(context.Background(), repo)
	if len(unmerged) != 1 || unmerged[0] != archRel {
		t.Fatalf("unmerged = %v, want [%s]", unmerged, archRel)
	}
	stages, err := git.ConflictStages(context.Background(), repo, archRel)
	if err != nil {
		t.Fatal(err)
	}
	if stages.Base || !stages.Ours || !stages.Theirs {
		t.Fatalf("want add/add stages, got %+v", stages)
	}

	d := New(repo, freshLoader())
	out, err := d.Resolve(context.Background(), Conflict{
		Path: archRel, ScopeDir: scopeDir, OursRev: mainTip, TheirsRev: featTip,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Class != ClassStatusDispute || out.Staged {
		t.Fatalf("want status dispute unstaged, got class=%v staged=%v rename=%+v", out.Class, out.Staged, out.Rename)
	}
	m := parseFile(t, filepath.Join(repo, archRel))
	if m.Status != "todo" {
		t.Errorf("status = %q, want base todo", m.Status)
	}
	if m.Changed != "2026-01-02T00:00:00Z" {
		t.Errorf("changed = %q, want the pre-image timestamp", m.Changed)
	}
	if m.ID != "wc-ab2c" {
		t.Errorf("id = %q, want wc-ab2c", m.ID)
	}
	matches, err := filepath.Glob(filepath.Join(repo, "wc", "archive", "wc-ab2c*.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Errorf("archive files = %v, want the original id only", matches)
	}
}

// Per-file author dates, not branch-tip dates, decide LWW for another ticket's fields.
func TestPerFileAuthorDateNotBranchTip(t *testing.T) {
	requireGit(t)
	repo := newRepo(t)
	scopeDir := filepath.Join(repo, "wc")
	writeF(t, filepath.Join(scopeDir, "tk.cue"), tkcue(""))
	aPath := filepath.Join(repo, "wc", "wc-ab2c-a.md")
	bPath := filepath.Join(repo, "wc", "wc-cd3e-b.md")
	writeF(t, aPath, proj("id: wc-ab2c\nstatus: todo\norder: \"a0\"\nsummary: BASE\ncreated: 2026-01-01T00:00:00Z\n", "x\n"))
	writeF(t, bPath, proj("id: wc-cd3e\nstatus: todo\norder: \"a0\"\n", "y\n"))
	commitAll(t, repo, "base", "2026-01-01T00:00:00Z")
	mainBranch := gitCapture(t, repo, "rev-parse", "--abbrev-ref", "HEAD")

	// feature: edit A's summary at D2 (later of the two A-edits).
	gitRun(t, repo, nil, "checkout", "-b", "feature")
	writeF(t, aPath, proj("id: wc-ab2c\nstatus: todo\norder: \"a0\"\nsummary: FEAT\ncreated: 2026-01-01T00:00:00Z\n", "x\n"))
	featTip := commitAll(t, repo, "feature A edit", "2026-05-01T00:00:00Z")

	// main: edit A at D1, then unrelated B edit at D3 (branch tip).
	gitRun(t, repo, nil, "checkout", mainBranch)
	writeF(t, aPath, proj("id: wc-ab2c\nstatus: todo\norder: \"a0\"\nsummary: MAIN\ncreated: 2026-01-01T00:00:00Z\n", "x\n"))
	commitAll(t, repo, "main A edit", "2026-03-01T00:00:00Z")
	writeF(t, bPath, proj("id: wc-cd3e\nstatus: done\norder: \"a0\"\n", "y\n"))
	mainTip := commitAll(t, repo, "main B edit", "2026-09-01T00:00:00Z")

	gitRun(t, repo, nil, "checkout", "feature")
	if !startRebase(t, repo, mainBranch) {
		t.Fatal("expected paused rebase")
	}

	d := New(repo, freshLoader())
	out, err := d.Resolve(context.Background(), Conflict{
		Path: "wc/wc-ab2c-a.md", ScopeDir: scopeDir, OursRev: mainTip, TheirsRev: featTip,
	})
	if err != nil {
		t.Fatal(err)
	}
	m := parseFile(t, aPath)
	// Per-file: theirs(A)=D2 > ours(A)=D1 → FEAT. Branch-tip would give MAIN (wrong).
	if m.Summary != "FEAT" {
		t.Errorf("summary = %q, want FEAT (per-file date, not branch tip)", m.Summary)
	}
	_ = out
}

// Same new status with differing changed follows the per-file author date.
// A later commit on another file must not decide it.
func TestChangedPerFileAuthorDateNotBranchTip(t *testing.T) {
	requireGit(t)
	repo := newRepo(t)
	scopeDir := filepath.Join(repo, "wc")
	writeF(t, filepath.Join(scopeDir, "tk.cue"), tkcue(""))
	aPath := filepath.Join(repo, "wc", "wc-ab2c-a.md")
	bPath := filepath.Join(repo, "wc", "wc-cd3e-b.md")
	writeF(t, aPath, proj("id: wc-ab2c\nstatus: todo\norder: \"a0\"\nchanged: 2026-01-01T00:00:00Z\ncreated: 2026-01-01T00:00:00Z\n", "x\n"))
	writeF(t, bPath, proj("id: wc-cd3e\nstatus: todo\norder: \"a0\"\n", "y\n"))
	commitAll(t, repo, "base", "2026-01-01T00:00:00Z")
	mainBranch := gitCapture(t, repo, "rev-parse", "--abbrev-ref", "HEAD")

	gitRun(t, repo, nil, "checkout", "-b", "feature")
	writeF(t, aPath, proj("id: wc-ab2c\nstatus: in-progress\norder: \"a0\"\nchanged: 2026-05-01T00:00:00Z\ncreated: 2026-01-01T00:00:00Z\n", "x\n"))
	featTip := commitAll(t, repo, "feature A edit", "2026-05-01T00:00:00Z")

	gitRun(t, repo, nil, "checkout", mainBranch)
	writeF(t, aPath, proj("id: wc-ab2c\nstatus: in-progress\norder: \"a0\"\nchanged: 2026-03-01T00:00:00Z\ncreated: 2026-01-01T00:00:00Z\n", "x\n"))
	commitAll(t, repo, "main A edit", "2026-03-01T00:00:00Z")
	writeF(t, bPath, proj("id: wc-cd3e\nstatus: done\norder: \"a0\"\n", "y\n"))
	mainTip := commitAll(t, repo, "main B edit", "2026-09-01T00:00:00Z")

	gitRun(t, repo, nil, "checkout", "feature")
	if !startRebase(t, repo, mainBranch) {
		t.Fatal("expected paused rebase")
	}

	d := New(repo, freshLoader())
	if _, err := d.Resolve(context.Background(), Conflict{
		Path: "wc/wc-ab2c-a.md", ScopeDir: scopeDir, OursRev: mainTip, TheirsRev: featTip,
	}); err != nil {
		t.Fatal(err)
	}
	m := parseFile(t, aPath)
	if m.Status != "in-progress" {
		t.Fatalf("status = %q, want in-progress", m.Status)
	}
	// Per-file: feature A is May, main A is March. Branch tip is September on B.
	if m.Changed != "2026-05-01T00:00:00Z" {
		t.Errorf("changed = %q, want 2026-05-01T00:00:00Z (per-file date, not branch tip)", m.Changed)
	}
}

// Driver types the merge from on-disk schema: a key only incoming tk.cue declares as strings set-merges.
func TestSchemaFromOnDiskStrings(t *testing.T) {
	requireGit(t)
	fmBase := "id: wc-ab2c\nstatus: todo\norder: \"a0\"\nreviewers: [alice]\n"
	fmMain := "id: wc-ab2c\nstatus: todo\norder: \"a0\"\nreviewers: [alice, bob]\n"
	fmFeat := "id: wc-ab2c\nstatus: todo\norder: \"a0\"\nreviewers: [alice, carol]\n"
	// Only main's tk.cue declares reviewers as strings; it lands in the on-disk schema.
	cueMain := tkcue("fields: reviewers: type: \"strings\"\n")

	f := setup(t, tkcue(""), cueMain,
		proj(fmBase, "b\n"), proj(fmMain, "b\n"), proj(fmFeat, "b\n"),
		"2026-02-01T00:00:00Z")

	out := resolve(t, f)
	if out.Class != ClassClean {
		t.Fatalf("want clean, got %v", out.Class)
	}
	m := parseFile(t, filepath.Join(f.repo, f.projRel))
	var reviewers []string
	for _, fld := range m.Custom {
		if fld.Key == "reviewers" {
			if list, ok := fld.Value.([]any); ok {
				for _, e := range list {
					reviewers = append(reviewers, e.(string))
				}
			}
		}
	}
	if strings.Join(reviewers, ",") != "alice,bob,carol" {
		t.Errorf("strings field must set-merge from on-disk schema: got %v", reviewers)
	}
}

// Across two add/add conflicts, the first mint is in the occupied set the second uses.
func TestOccupiedAccumulatesAcrossAddAdds(t *testing.T) {
	requireGit(t)
	repo := newRepo(t)
	scopeDir := filepath.Join(repo, "wc")
	writeF(t, filepath.Join(scopeDir, "tk.cue"), tkcue(""))
	writeF(t, filepath.Join(repo, "seed.txt"), "seed\n")
	commitAll(t, repo, "seed", "2026-01-01T00:00:00Z")
	mainBranch := gitCapture(t, repo, "rev-parse", "--abbrev-ref", "HEAD")

	pathA := "wc/wc-ab2c-alpha.md"
	pathB := "wc/wc-ab2c-beta.md"
	fm := func(created string) string {
		return "id: wc-ab2c\nstatus: todo\norder: \"a0\"\ncreated: " + created + "\n"
	}

	gitRun(t, repo, nil, "checkout", "-b", "feature")
	writeF(t, filepath.Join(repo, pathA), proj(fm("2026-06-01T00:00:00Z"), "featA\n"))
	writeF(t, filepath.Join(repo, pathB), proj(fm("2026-06-01T00:00:00Z"), "featB\n"))
	featTip := commitAll(t, repo, "feature adds", "2026-06-01T00:00:00Z")

	gitRun(t, repo, nil, "checkout", mainBranch)
	writeF(t, filepath.Join(repo, pathA), proj(fm("2026-01-01T00:00:00Z"), "mainA\n"))
	writeF(t, filepath.Join(repo, pathB), proj(fm("2026-01-01T00:00:00Z"), "mainB\n"))
	mainTip := commitAll(t, repo, "main adds", "2026-01-01T00:00:00Z")

	gitRun(t, repo, nil, "checkout", "feature")
	if !startRebase(t, repo, mainBranch) {
		t.Fatal("expected paused rebase")
	}

	d := New(repo, freshLoader())
	ctx := context.Background()
	outA, err := d.Resolve(ctx, Conflict{Path: pathA, ScopeDir: scopeDir, OursRev: mainTip, TheirsRev: featTip})
	if err != nil {
		t.Fatal(err)
	}
	outB, err := d.Resolve(ctx, Conflict{Path: pathB, ScopeDir: scopeDir, OursRev: mainTip, TheirsRev: featTip})
	if err != nil {
		t.Fatal(err)
	}
	if outA.Class != ClassRename || outB.Class != ClassRename {
		t.Fatalf("both must be renames: %v %v", outA.Class, outB.Class)
	}
	if outA.Rename.NewID == outB.Rename.NewID {
		t.Errorf("second extension must not collide with the first: both %s", outA.Rename.NewID)
	}
}

// Both sides add a second file for an id the original file still has.
// That is a collision rename: both new summaries survive.
func TestSameIDAddAddKeepsBothCopiesWhenOriginalRemains(t *testing.T) {
	requireGit(t)
	repo := newRepo(t)
	scopeDir := filepath.Join(repo, "wc")
	writeF(t, filepath.Join(scopeDir, "tk.cue"), tkcue(""))
	alpha := "wc/wc-ab2c-alpha.md"
	beta := "wc/wc-ab2c-beta.md"
	fm := func(summary string) string {
		return "id: wc-ab2c\nstatus: todo\norder: \"a0\"\nsummary: " + summary + "\ncreated: 2026-01-01T00:00:00Z\n"
	}
	writeF(t, filepath.Join(repo, alpha), proj(fm("OLD"), "original\n"))
	commitAll(t, repo, "base", "2026-01-01T00:00:00Z")
	mainBranch := gitCapture(t, repo, "rev-parse", "--abbrev-ref", "HEAD")

	gitRun(t, repo, nil, "checkout", "-b", "feature")
	writeF(t, filepath.Join(repo, beta), proj(fm("FEAT"), "original\n"))
	featTip := commitAll(t, repo, "feature copy", "2026-06-01T00:00:00Z")

	gitRun(t, repo, nil, "checkout", mainBranch)
	writeF(t, filepath.Join(repo, beta), proj(fm("MAIN"), "original\n"))
	mainTip := commitAll(t, repo, "main copy", "2026-03-01T00:00:00Z")

	gitRun(t, repo, nil, "checkout", "feature")
	if !startRebase(t, repo, mainBranch) {
		t.Fatal("expected paused rebase")
	}

	d := New(repo, freshLoader())
	out, err := d.Resolve(context.Background(), Conflict{
		Path: beta, ScopeDir: scopeDir, OursRev: mainTip, TheirsRev: featTip,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Class != ClassRename || !out.Staged || out.Rename == nil {
		t.Fatalf("want a staged rename, got class=%v staged=%v rename=%+v", out.Class, out.Staged, out.Rename)
	}
	keep := parseFile(t, filepath.Join(repo, beta))
	loser := parseFile(t, filepath.Join(repo, out.Rename.NewPath))
	got := map[string]bool{keep.Summary: true, loser.Summary: true}
	if !got["MAIN"] || !got["FEAT"] {
		t.Fatalf("both new summaries must survive, keep=%q loser=%q", keep.Summary, loser.Summary)
	}
	if keep.ID != "wc-ab2c" || loser.ID == "wc-ab2c" {
		t.Fatalf("loser must leave the shared id, keep=%s loser=%s", keep.ID, loser.ID)
	}
	if parseFile(t, filepath.Join(repo, alpha)).Summary != "OLD" {
		t.Fatal("the original file must be left alone")
	}
}

// Two deleted files share the id, and both sides added one new path.
// Sync must stop without minting a second id.
func TestTwoDeletedAncestorsDoNotMint(t *testing.T) {
	requireGit(t)
	repo := newRepo(t)
	scopeDir := filepath.Join(repo, "wc")
	writeF(t, filepath.Join(scopeDir, "tk.cue"), tkcue(""))
	alpha := "wc/wc-ab2c-alpha.md"
	archived := "wc/archive/wc-ab2c-beta.md"
	gamma := "wc/wc-ab2c-gamma.md"
	writeF(t, filepath.Join(repo, alpha), proj("id: wc-ab2c\nstatus: todo\norder: \"a0\"\n", "# alpha\n"))
	writeF(t, filepath.Join(repo, archived), proj("id: wc-ab2c\nstatus: todo\norder: \"a0\"\n", "# beta\n"))
	commitAll(t, repo, "base", "2026-01-01T00:00:00Z")
	mainBranch := gitCapture(t, repo, "rev-parse", "--abbrev-ref", "HEAD")

	divergent := func(line string) string {
		return proj("id: wc-ab2c\nstatus: todo\norder: \"a0\"\n", strings.Repeat(line, 40))
	}
	gitRun(t, repo, nil, "checkout", "-b", "feature")
	if err := os.Remove(filepath.Join(repo, alpha)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(repo, archived)); err != nil {
		t.Fatal(err)
	}
	writeF(t, filepath.Join(repo, gamma), divergent("feat side\n"))
	featTip := commitAll(t, repo, "feature replace", "2026-06-01T00:00:00Z")

	gitRun(t, repo, nil, "checkout", mainBranch)
	if err := os.Remove(filepath.Join(repo, alpha)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(repo, archived)); err != nil {
		t.Fatal(err)
	}
	writeF(t, filepath.Join(repo, gamma), divergent("main side\n"))
	mainTip := commitAll(t, repo, "main replace", "2026-03-01T00:00:00Z")

	gitRun(t, repo, nil, "checkout", "feature")
	if !startRebase(t, repo, mainBranch) {
		t.Fatal("expected paused rebase")
	}
	stages, err := git.ConflictStages(context.Background(), repo, gamma)
	if err != nil {
		t.Fatal(err)
	}
	if stages.Base || !stages.Ours || !stages.Theirs {
		t.Fatalf("want add/add stages, got %+v", stages)
	}

	d := New(repo, freshLoader())
	out, err := d.Resolve(context.Background(), Conflict{
		Path: gamma, ScopeDir: scopeDir, OursRev: mainTip, TheirsRev: featTip,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Class != ClassAmbiguousBase || out.Staged || out.AmbiguousBase == nil || out.AmbiguousBase.ID != "wc-ab2c" {
		t.Fatalf("want an unstaged ambiguous base for wc-ab2c, got class=%v staged=%v base=%+v", out.Class, out.Staged, out.AmbiguousBase)
	}
	matches, err := filepath.Glob(filepath.Join(repo, "wc", "wc-ab2c*.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || filepath.Base(matches[0]) != "wc-ab2c-gamma.md" {
		t.Fatalf("minted or moved files: %v", matches)
	}
}

// Operational fault (unreadable schema) is an ordinary error, distinct from fail-closed.
func TestOperationalErrorDistinctFromFailClosed(t *testing.T) {
	requireGit(t)
	base := proj("id: wc-ab2c\nstatus: todo\norder: \"a0\"\n", "b\n")
	main := proj("id: wc-ab2c\nstatus: done\norder: \"a0\"\n", "b\n")
	feat := proj("id: wc-ab2c\nstatus: in-progress\norder: \"a0\"\n", "b\n")
	f := setup(t, tkcue(""), "", base, main, feat,
		"2026-02-01T00:00:00Z")

	d := New(f.repo, func(string) (*scopeconfig.Schema, error) {
		return nil, os.ErrPermission
	})
	_, err := d.Resolve(context.Background(), f.conflict())
	if err == nil {
		t.Fatal("an unreadable schema must be an operational error, not a fail-closed outcome")
	}
}
