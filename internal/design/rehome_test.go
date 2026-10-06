package design

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/git"
	"github.com/p3bot/tk/internal/registry"
	"github.com/p3bot/tk/internal/testgit"
	"github.com/p3bot/tk/internal/token"
)

func TestRehomeKeepsShortIDSlugAndProduces(t *testing.T) {
	d := newDesignPair(t)
	src := writeDesignFile(t, d.src, "foo-ab2c", "shape", ""+
		"id: foo-ab2c\nstatus: accepted\nchanged: 2026-02-02T03:04:05Z\n"+
		"order: \"a0\"\ncreated: 2026-01-01T00:00:00Z\nproduces: [zz-m4np, zz-cd3e]\n",
		"# Shape\n\nbody\n")

	res, err := rehomeDesign(d)
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != "bar-ab2c" {
		t.Fatalf("id = %q", res.ID)
	}
	if !strings.HasSuffix(res.Path, filepath.Join("design", "bar-ab2c-shape.md")) {
		t.Fatalf("path = %q", res.Path)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("source still present: %v", err)
	}
	m := fenceModel(t, res.Path)
	if m.Status != StatusAccepted || m.Created != "2026-01-01T00:00:00Z" || m.Changed != "2026-02-02T03:04:05Z" {
		t.Fatalf("fence = %+v", m)
	}
	if m.Order != "" {
		t.Fatalf("order = %q", m.Order)
	}
	ids, err := Produces(m)
	if err != nil || strings.Join(ids, ",") != "zz-m4np,zz-cd3e" {
		t.Fatalf("produces = %v err=%v", ids, err)
	}
	raw, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "# Shape\n\nbody\n") {
		t.Fatalf("body = %q", raw)
	}
	if strings.Contains(string(raw), "order:") {
		t.Fatalf("order key written:\n%s", raw)
	}
	tickets, err := d.deps.DB.ScopeTickets("bar")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range tickets {
		if row.ID == "bar-ab2c" {
			t.Fatalf("design indexed as a ticket: %+v", row)
		}
	}
	designs, err := d.deps.DB.ScopeDesigns("bar")
	if err != nil {
		t.Fatal(err)
	}
	if len(designs) != 1 || designs[0].ID != "bar-ab2c" {
		t.Fatalf("designs = %+v", designs)
	}
}

func TestRehomeAbsentChangedStaysAbsent(t *testing.T) {
	d := newDesignPair(t)
	writeDesignFile(t, d.src, "foo-ab2c", "shape",
		"id: foo-ab2c\nstatus: draft\ncreated: 2026-01-01T00:00:00Z\n", "# Shape\n")
	res, err := rehomeDesign(d)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "changed:") || strings.Contains(string(raw), "order:") {
		t.Fatalf("invented a ticket key:\n%s", raw)
	}
}

func TestRehomeExtendsForTicketAndDesignOccupants(t *testing.T) {
	d := newDesignPair(t)
	writeDesignFile(t, d.src, "foo-ab2c", "shape",
		"id: foo-ab2c\nstatus: draft\ncreated: 2026-01-01T00:00:00Z\n", "# Shape\n")
	ticket := filepath.Join(d.dest, "bar-ab2c-work.md")
	if err := os.WriteFile(ticket, []byte("---\nid: bar-ab2c\nstatus: todo\norder: \"a0\"\ncreated: 2026-03-01T00:00:00Z\n---\n# Work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	other := writeDesignFile(t, d.dest, "bar-cd3e", "other",
		"id: bar-cd3e\nstatus: draft\ncreated: 2026-01-01T00:00:00Z\n", "# Other\n")
	// Same short id, different slug: a real design occupant, not an interrupted move.
	held := writeDesignFile(t, d.dest, "bar-ab2c", "held",
		"id: bar-ab2c\nstatus: draft\ncreated: 2026-04-01T00:00:00Z\n", "# Held\n")
	before, err := os.ReadFile(held)
	if err != nil {
		t.Fatal(err)
	}
	ticketBefore, err := os.ReadFile(ticket)
	if err != nil {
		t.Fatal(err)
	}

	res, err := rehomeDesign(d)
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != "bar-ab2ca" {
		t.Fatalf("id = %q", res.ID)
	}
	if !strings.HasSuffix(res.Path, "bar-ab2ca-shape.md") {
		t.Fatalf("path = %q", res.Path)
	}
	after, err := os.ReadFile(held)
	if err != nil || string(after) != string(before) {
		t.Fatalf("occupant changed: %v %s", err, after)
	}
	ticketAfter, err := os.ReadFile(ticket)
	if err != nil || string(ticketAfter) != string(ticketBefore) {
		t.Fatalf("ticket occupant changed: %v %s", err, ticketAfter)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatal(err)
	}
}

func TestRehomeReusesInterruptedDesign(t *testing.T) {
	d := newDesignPair(t)
	src := writeDesignFile(t, d.src, "foo-ab2c", "shape",
		"id: foo-ab2c\nstatus: accepted\nchanged: 2026-02-02T03:04:05Z\ncreated: 2026-01-01T00:00:00Z\nproduces: [zz-m4np]\n",
		"# Shape\n\nfresh\n")
	writeDesignFile(t, d.dest, "bar-ab2ca", "shape",
		"id: bar-ab2ca\nstatus: draft\ncreated: 2026-01-01T00:00:00Z\n", "# Shape\n\nstale\n")
	// The original short id is held by a ticket, so a fresh mint would extend again.
	if err := os.WriteFile(filepath.Join(d.dest, "bar-ab2c-work.md"), []byte("---\nid: bar-ab2c\nstatus: todo\norder: \"a0\"\ncreated: 2026-03-01T00:00:00Z\n---\n# Work\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := rehomeDesign(d)
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != "bar-ab2ca" {
		t.Fatalf("id = %q", res.ID)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("source = %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(d.dest, "design"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "bar-ab2ca-shape.md" {
		t.Fatalf("dest designs = %v", entries)
	}
	raw, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "fresh") || strings.Contains(string(raw), "stale") {
		t.Fatalf("leftover was not rewritten:\n%s", raw)
	}
	if fenceModel(t, res.Path).Status != StatusAccepted {
		t.Fatalf("status not rewritten:\n%s", raw)
	}
}

func TestRehomeSameScopeAndUnknownDestDoNotWrite(t *testing.T) {
	d := newDesignPair(t)
	src := writeDesignFile(t, d.src, "foo-ab2c", "shape",
		"id: foo-ab2c\nstatus: draft\ncreated: 2026-01-01T00:00:00Z\n", "# Shape\n")
	before, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Rehome(d.deps, RehomeInput{
		IDInput:   IDInput{Scope: "foo", Dir: d.src, Arg: "foo-ab2c", Full: true},
		DestScope: "foo", DestDir: d.src,
	})
	var use *UsageError
	if !errors.As(err, &use) {
		t.Fatalf("same scope: %v", err)
	}
	after, err := os.ReadFile(src)
	if err != nil || string(after) != string(before) {
		t.Fatalf("same scope wrote: %v %s", err, after)
	}

	_, err = Rehome(d.deps, RehomeInput{
		IDInput:   IDInput{Scope: "foo", Dir: d.src, Arg: "foo-ab2c", Full: true},
		DestScope: "ghost",
	})
	if err == nil || errors.As(err, &use) || !strings.Contains(err.Error(), "unknown scope") {
		t.Fatalf("unknown dest: %v", err)
	}
	after, err = os.ReadFile(src)
	if err != nil || string(after) != string(before) {
		t.Fatalf("unknown dest wrote: %v", err)
	}
}

func TestRehomeSharedShortIDPrintsNoPath(t *testing.T) {
	d := newDesignPair(t)
	writeDesignFile(t, d.src, "foo-ab2c", "one",
		"id: foo-ab2c\nstatus: draft\ncreated: 2026-01-01T00:00:00Z\n", "# One\n")
	writeDesignFile(t, d.src, "foo-ab2c", "two",
		"id: foo-ab2c\nstatus: draft\ncreated: 2026-01-01T00:00:00Z\n", "# Two\n")
	res, err := rehomeDesign(d)
	var shared *SharedIDError
	if !errors.As(err, &shared) {
		t.Fatalf("err = %v", err)
	}
	if res.Path != "" {
		t.Fatalf("path = %q", res.Path)
	}
	entries, err := os.ReadDir(filepath.Join(d.src, "design"))
	if err != nil || len(entries) != 2 {
		t.Fatalf("source designs = %v %v", entries, err)
	}
	if _, err := os.Stat(filepath.Join(d.dest, "design")); !os.IsNotExist(err) {
		t.Fatalf("dest design dir = %v", err)
	}
}

func TestRehomeMissingFenceDoesNotWrite(t *testing.T) {
	d := newDesignPair(t)
	target := filepath.Join(d.src, "design")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(target, "foo-ab2c-shape.md")
	if err := os.WriteFile(src, []byte("# Shape\n\nno fence\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := rehomeDesign(d)
	var parse *ParseError
	if !errors.As(err, &parse) {
		t.Fatalf("err = %v", err)
	}
	if res.Path != "" {
		t.Fatalf("path = %q", res.Path)
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatal(err)
	}
}

func TestRehomeUnparseableSourceDoesNotWrite(t *testing.T) {
	d := newDesignPair(t)
	src := writeDesignFile(t, d.src, "foo-ab2c", "shape", "id: [broken\n", "# Shape\n")
	res, err := rehomeDesign(d)
	var parse *ParseError
	if !errors.As(err, &parse) {
		t.Fatalf("err = %v", err)
	}
	if res.Path != "" {
		t.Fatalf("path = %q", res.Path)
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(d.dest, "design")); !os.IsNotExist(err) {
		t.Fatalf("dest written: %v", err)
	}
}

func TestRehomeIgnoresTicketID(t *testing.T) {
	d := newDesignPair(t)
	ticket := filepath.Join(d.src, "foo-ab2c-work.md")
	if err := os.WriteFile(ticket, []byte("---\nid: foo-ab2c\nstatus: todo\norder: \"a0\"\ncreated: 2026-01-01T00:00:00Z\n---\n# Work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := rehomeDesign(d)
	var unknown *UnknownError
	if !errors.As(err, &unknown) {
		t.Fatalf("err = %v", err)
	}
	if res.Path != "" {
		t.Fatalf("path = %q", res.Path)
	}
	if _, err := os.Stat(ticket); err != nil {
		t.Fatal(err)
	}
}

func TestRehomeSharedRootMismatchDoesNotWrite(t *testing.T) {
	requireDesignGit(t)
	repo := t.TempDir()
	testgit.Run(t, repo, "init", "-b", "main")
	testgit.Run(t, repo, "config", "user.email", "a@b.c")
	testgit.Run(t, repo, "config", "user.name", "tk-test")
	testgit.Run(t, repo, "config", "commit.gpgsign", "false")
	src := filepath.Join(repo, "foo")
	dest := filepath.Join(repo, "bar")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "tk.cue"), []byte("name: \"foo\"\nautoCommit: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "tk.cue"), []byte("name: \"bar\"\nautoCommit: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := openDesignPair(t, src, dest)
	path := writeDesignFile(t, src, "foo-ab2c", "shape",
		"id: foo-ab2c\nstatus: draft\ncreated: 2026-01-01T00:00:00Z\n", "# Shape\n")
	_, err := rehomeDesign(d)
	if err == nil || !strings.Contains(err.Error(), token.AutoCommitMismatch) {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatal(statErr)
	}
	if _, statErr := os.Stat(filepath.Join(dest, "design")); !os.IsNotExist(statErr) {
		t.Fatalf("dest design dir = %v", statErr)
	}
}

func TestRehomeSharedRootCommitsOnceAndDoesNotPush(t *testing.T) {
	requireDesignGit(t)
	repo := t.TempDir()
	testgit.Run(t, repo, "init", "-b", "main")
	testgit.Run(t, repo, "config", "user.email", "a@b.c")
	testgit.Run(t, repo, "config", "user.name", "tk-test")
	testgit.Run(t, repo, "config", "commit.gpgsign", "false")
	src := filepath.Join(repo, "foo")
	dest := filepath.Join(repo, "bar")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "tk.cue"), []byte("name: \"foo\"\nautoCommit: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "tk.cue"), []byte("name: \"bar\"\nautoCommit: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := openDesignPair(t, src, dest)
	writeDesignFile(t, src, "foo-ab2c", "shape",
		"id: foo-ab2c\nstatus: draft\ncreated: 2026-01-01T00:00:00Z\n", "# Shape\n")
	remote := t.TempDir()
	testgit.Run(t, remote, "init", "--bare", "-b", "main")
	testgit.Run(t, repo, "add", "-A")
	testgit.Run(t, repo, "commit", "-m", "seed")
	testgit.Run(t, repo, "remote", "add", "origin", remote)
	testgit.Run(t, repo, "push", "-u", "origin", "main")

	res, err := rehomeDesign(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.SyncNeededAll) != 1 || res.SyncNeededAll[0] != "unpushed" {
		t.Fatalf("sync = %v", res.SyncNeededAll)
	}
	log, err := testgit.CombinedAllowFailure(t, repo, "log", "--format=%s")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(log, "tk: design rehome foo-ab2c -> bar-ab2c") != 1 {
		t.Fatalf("log = %q", log)
	}
	remoteLog, err := testgit.CombinedAllowFailure(t, remote, "log", "--format=%s")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(remoteLog, "design rehome") {
		t.Fatalf("pushed = %q", remoteLog)
	}
}

func TestRehomeTwoRootsEachSelfCommit(t *testing.T) {
	requireDesignGit(t)
	srcRepo, src := initDesignRepo(t, "foo")
	destRepo, dest := initDesignRepo(t, "bar")
	d := openDesignPair(t, src, dest)
	writeDesignFile(t, src, "foo-ab2c", "shape",
		"id: foo-ab2c\nstatus: draft\ncreated: 2026-01-01T00:00:00Z\n", "# Shape\n")
	testgit.Run(t, srcRepo, "add", "-A")
	testgit.Run(t, srcRepo, "commit", "-m", "seed")
	testgit.Run(t, destRepo, "add", "-A")
	testgit.Run(t, destRepo, "commit", "-m", "seed")

	if _, err := rehomeDesign(d); err != nil {
		t.Fatal(err)
	}
	for _, repo := range []string{srcRepo, destRepo} {
		log, err := testgit.CombinedAllowFailure(t, repo, "log", "--format=%s")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(log, "tk: design rehome foo-ab2c -> bar-ab2c") != 1 {
			t.Fatalf("log in %s = %q", repo, log)
		}
	}
}

func TestRehomeIndexFailureRestoresSource(t *testing.T) {
	d := newDesignPair(t)
	src := writeDesignFile(t, d.src, "foo-ab2c", "shape",
		"id: foo-ab2c\nstatus: draft\ncreated: 2026-01-01T00:00:00Z\n", "# Shape\n")
	before, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	failFirstIndexSync(d)
	res, err := rehomeDesign(d)
	if err == nil || !strings.Contains(err.Error(), "index down") {
		t.Fatalf("err = %v", err)
	}
	if res.Path != "" {
		t.Fatalf("path = %q", res.Path)
	}
	after, err := os.ReadFile(src)
	if err != nil || string(after) != string(before) {
		t.Fatalf("source = %v %s", err, after)
	}
	if _, statErr := os.Stat(filepath.Join(d.dest, "design", "bar-ab2c-shape.md")); !os.IsNotExist(statErr) {
		t.Fatalf("dest = %v", statErr)
	}
	if !designIndexed(t, d, "foo", "foo-ab2c") {
		t.Fatal("source design missing from the index")
	}
	if designIndexed(t, d, "bar", "bar-ab2c") {
		t.Fatal("destination design stayed in the index")
	}
	d.deps.syncPaths = nil
	res, err = rehomeDesign(d)
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != "bar-ab2c" {
		t.Fatalf("retry id = %q", res.ID)
	}
}

func TestRehomeIndexFailureRestoresLeftover(t *testing.T) {
	d := newDesignPair(t)
	src := writeDesignFile(t, d.src, "foo-ab2c", "shape",
		"id: foo-ab2c\nstatus: accepted\ncreated: 2026-01-01T00:00:00Z\n", "# Shape\n\nfresh\n")
	srcBefore, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	left := writeDesignFile(t, d.dest, "bar-ab2ca", "shape",
		"id: bar-ab2ca\nstatus: draft\ncreated: 2026-01-01T00:00:00Z\n", "# Shape\n\nstale\n")
	leftBefore, err := os.ReadFile(left)
	if err != nil {
		t.Fatal(err)
	}
	failFirstIndexSync(d)
	res, err := rehomeDesign(d)
	if err == nil || !strings.Contains(err.Error(), "index down") {
		t.Fatalf("err = %v", err)
	}
	if res.Path != "" {
		t.Fatalf("path = %q", res.Path)
	}
	srcAfter, err := os.ReadFile(src)
	if err != nil || string(srcAfter) != string(srcBefore) {
		t.Fatalf("source = %v %s", err, srcAfter)
	}
	leftAfter, err := os.ReadFile(left)
	if err != nil || string(leftAfter) != string(leftBefore) {
		t.Fatalf("leftover = %v %s", err, leftAfter)
	}
}

func TestRehomeCommitFailureRestoresSource(t *testing.T) {
	requireDesignGit(t)
	repo, src, dest := initSharedDesignRepo(t)
	d := openDesignPair(t, src, dest)
	path := writeDesignFile(t, src, "foo-ab2c", "shape",
		"id: foo-ab2c\nstatus: draft\ncreated: 2026-01-01T00:00:00Z\n", "# Shape\n")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	testgit.Run(t, repo, "add", "-A")
	testgit.Run(t, repo, "commit", "-m", "seed")
	installFailingHook(t, repo)

	res, err := rehomeDesign(d)
	if err == nil || !strings.Contains(err.Error(), "self-commit") {
		t.Fatalf("err = %v", err)
	}
	if res.Path != "" {
		t.Fatalf("path = %q", res.Path)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatalf("source = %v %s", err, after)
	}
	if _, statErr := os.Stat(filepath.Join(dest, "design", "bar-ab2c-shape.md")); !os.IsNotExist(statErr) {
		t.Fatalf("dest = %v", statErr)
	}
	log := gitLog(t, repo)
	if strings.Contains(log, "design rehome") {
		t.Fatalf("log = %q", log)
	}
	if !designIndexed(t, d, "foo", "foo-ab2c") {
		t.Fatal("source design missing from the index")
	}

	if err := os.Remove(filepath.Join(repo, ".git", "hooks", "pre-commit")); err != nil {
		t.Fatal(err)
	}
	res, err = rehomeDesign(d)
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != "bar-ab2c" {
		t.Fatalf("retry id = %q", res.ID)
	}
}

func TestRehomeUndoCommitsARootThatAlreadyMoved(t *testing.T) {
	requireDesignGit(t)
	srcRepo, src := initDesignRepo(t, "foo")
	destRepo, dest := initDesignRepo(t, "bar")
	d := openDesignPair(t, src, dest)
	path := writeDesignFile(t, src, "foo-ab2c", "shape",
		"id: foo-ab2c\nstatus: draft\ncreated: 2026-01-01T00:00:00Z\n", "# Shape\n")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	testgit.Run(t, srcRepo, "add", "-A")
	testgit.Run(t, srcRepo, "commit", "-m", "seed")
	testgit.Run(t, destRepo, "add", "-A")
	testgit.Run(t, destRepo, "commit", "-m", "seed")
	// bar sorts before foo, so the destination commit lands first.
	// The hook is on the later root, which is the one that fails.
	installFailingHook(t, srcRepo)

	res, err := rehomeDesign(d)
	if err == nil || !strings.Contains(err.Error(), "self-commit") {
		t.Fatalf("err = %v", err)
	}
	if res.Path != "" {
		t.Fatalf("path = %q", res.Path)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatalf("source = %v %s", err, after)
	}
	if _, statErr := os.Stat(filepath.Join(dest, "design", "bar-ab2c-shape.md")); !os.IsNotExist(statErr) {
		t.Fatalf("dest = %v", statErr)
	}
	destLog := gitLog(t, destRepo)
	if strings.Count(destLog, "tk: design rehome foo-ab2c -> bar-ab2c") != 1 || strings.Count(destLog, "tk: undo design rehome foo-ab2c -> bar-ab2c") != 1 {
		t.Fatalf("dest log = %q", destLog)
	}
	if strings.Contains(gitLog(t, srcRepo), "design rehome") {
		t.Fatalf("source log = %q", gitLog(t, srcRepo))
	}
	if !designIndexed(t, d, "foo", "foo-ab2c") {
		t.Fatal("source design missing from the index")
	}
	if designIndexed(t, d, "bar", "bar-ab2c") {
		t.Fatal("destination design stayed in the index")
	}

	if err := os.Remove(filepath.Join(srcRepo, ".git", "hooks", "pre-commit")); err != nil {
		t.Fatal(err)
	}
	res, err = rehomeDesign(d)
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != "bar-ab2c" {
		t.Fatalf("retry id = %q", res.ID)
	}
}

func failFirstIndexSync(d *designPair) {
	calls := 0
	rec := d.deps.Rec
	d.deps.syncPaths = func(scope string, paths []string) error {
		if len(paths) == 0 {
			return nil
		}
		calls++
		if calls == 1 {
			return errors.New("index down")
		}
		return rec.SyncPaths(scope, paths)
	}
}

func designIndexed(t *testing.T, d *designPair, scope, id string) bool {
	t.Helper()
	rows, err := d.deps.DB.ScopeDesigns(scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ID == id {
			return true
		}
	}
	return false
}

func initSharedDesignRepo(t *testing.T) (repo, src, dest string) {
	t.Helper()
	repo = t.TempDir()
	testgit.Run(t, repo, "init", "-b", "main")
	testgit.Run(t, repo, "config", "user.email", "a@b.c")
	testgit.Run(t, repo, "config", "user.name", "tk-test")
	testgit.Run(t, repo, "config", "commit.gpgsign", "false")
	src = filepath.Join(repo, "foo")
	dest = filepath.Join(repo, "bar")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "tk.cue"), []byte("name: \"foo\"\nautoCommit: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "tk.cue"), []byte("name: \"bar\"\nautoCommit: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo, src, dest
}

func installFailingHook(t *testing.T, repo string) {
	t.Helper()
	hooks := filepath.Join(repo, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	testgit.Run(t, repo, "config", "core.hooksPath", hooks)
	if err := os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func gitLog(t *testing.T, repo string) string {
	t.Helper()
	log, err := testgit.CombinedAllowFailure(t, repo, "log", "--format=%s")
	if err != nil {
		t.Fatal(err)
	}
	return log
}

func initDesignRepo(t *testing.T, name string) (repo, dir string) {
	t.Helper()
	repo = t.TempDir()
	testgit.Run(t, repo, "init", "-b", "main")
	testgit.Run(t, repo, "config", "user.email", "a@b.c")
	testgit.Run(t, repo, "config", "user.name", "tk-test")
	testgit.Run(t, repo, "config", "commit.gpgsign", "false")
	dir = filepath.Join(repo, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tk.cue"), []byte("name: \""+name+"\"\nautoCommit: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo, dir
}

func TestRehomeMidRebaseDoesNotWrite(t *testing.T) {
	requireDesignGit(t)
	repo := t.TempDir()
	testgit.Run(t, repo, "init", "-b", "main")
	testgit.Run(t, repo, "config", "user.email", "a@b.c")
	testgit.Run(t, repo, "config", "user.name", "tk-test")
	testgit.Run(t, repo, "config", "commit.gpgsign", "false")
	src := filepath.Join(repo, "foo")
	dest := filepath.Join(repo, "bar")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "tk.cue"), []byte("name: \"foo\"\nautoCommit: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "tk.cue"), []byte("name: \"bar\"\nautoCommit: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := openDesignPair(t, src, dest)
	path := writeDesignFile(t, src, "foo-ab2c", "shape",
		"id: foo-ab2c\nstatus: draft\ncreated: 2026-01-01T00:00:00Z\n", "# Shape\n")
	if err := os.MkdirAll(filepath.Join(repo, ".git", "rebase-merge"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := rehomeDesign(d)
	if err == nil || !strings.Contains(err.Error(), "mid-sync-conflict") {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatal(statErr)
	}
}

type designPair struct {
	deps Deps
	src  string
	dest string
}

func newDesignPair(t *testing.T) *designPair {
	t.Helper()
	base := t.TempDir()
	src := filepath.Join(base, "foo")
	dest := filepath.Join(base, "bar")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "tk.cue"), []byte("name: \"foo\"\nautoCommit: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "tk.cue"), []byte("name: \"bar\"\nautoCommit: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return openDesignPair(t, src, dest)
}

func openDesignPair(t *testing.T, src, dest string) *designPair {
	t.Helper()
	deps, _ := plainDeps(t)
	deps.StateDir = t.TempDir()
	deps.Reg = &registry.Registry{
		Scopes: map[string]registry.Entry{
			"foo": {Dir: src, Root: src},
			"bar": {Dir: dest, Root: dest},
		},
	}
	return &designPair{deps: deps, src: src, dest: dest}
}

func rehomeDesign(d *designPair) (Result, error) {
	return Rehome(d.deps, RehomeInput{
		IDInput:   IDInput{Scope: "foo", Dir: d.src, Arg: "foo-ab2c", Full: true},
		DestScope: "bar",
		DestDir:   d.dest,
	})
}

func writeDesignFile(t *testing.T, dir, id, slug, interior, body string) string {
	t.Helper()
	target := filepath.Join(dir, "design")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(target, id+"-"+slug+".md")
	if err := os.WriteFile(path, []byte("---\n"+interior+"---\n"+body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func requireDesignGit(t *testing.T) {
	t.Helper()
	if !git.Available() {
		t.Skip("git not on PATH")
	}
	testgit.Hermetic(t)
}
