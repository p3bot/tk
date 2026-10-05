package repair

import (
	"bytes"
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/order"
	"github.com/p3bot/tk/internal/rewrite"
)

// writeProj writes a ticket file and returns its path. Empty created omits the key.
func writeProj(t *testing.T, dir, fullID, slug, created, orderKey, body string, archived bool) string {
	t.Helper()
	target := dir
	if archived {
		target = filepath.Join(dir, "archive")
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	fm := "---\nid: " + fullID + "\nstatus: todo\norder: \"" + orderKey + "\"\n"
	if created != "" {
		fm += "created: " + created + "\n"
	}
	fm += "---\n" + body
	path := filepath.Join(target, fullID+"-"+slug+".md")
	if err := os.WriteFile(path, []byte(fm), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func row(path, fullID, orderKey string) Row {
	short := fullID[strings.IndexByte(fullID, '-')+1:]
	return Row{Path: path, FullID: fullID, ShortID: short, OrderKey: orderKey}
}

// Loser pick renames the newer by created; two runs (any input order) agree.
func TestDuplicateIDPicksNewerByCreated(t *testing.T) {
	dir := t.TempDir()
	older := writeProj(t, dir, "wc-ab2c", "alpha", "2026-01-01T00:00:00Z", "a0", "# A\n", false)
	newer := writeProj(t, dir, "wc-ab2c", "beta", "2026-06-01T00:00:00Z", "a1", "# B\n", false)

	for _, in := range [][]Row{
		{row(older, "wc-ab2c", "a0"), row(newer, "wc-ab2c", "a1")},
		{row(newer, "wc-ab2c", "a1"), row(older, "wc-ab2c", "a0")},
	} {
		occ := map[string]string{"ab2c": older}
		ops, renames, err := DuplicateID("wc", in, occ)
		if err != nil {
			t.Fatal(err)
		}
		if len(renames) != 1 {
			t.Fatalf("one loser expected, got %d", len(renames))
		}
		if renames[0].OldPath != newer {
			t.Errorf("newer (by created) must be renamed, got %s", renames[0].OldPath)
		}
		if !strings.HasPrefix(renames[0].NewID, "wc-ab2c") || renames[0].NewID == "wc-ab2c" {
			t.Errorf("loser id must be an extension of ab2c, got %s", renames[0].NewID)
		}
		if len(ops) != 1 || ops[0].OldPath != newer {
			t.Errorf("op should rewrite the loser file: %v", ops)
		}
	}
}

// Degraded (absent) created is not-newer-than-any: kept; valid-created side is renamed.
func TestDuplicateIDDegradedCreatedKept(t *testing.T) {
	dir := t.TempDir()
	degraded := writeProj(t, dir, "wc-ab2c", "alpha", "", "a0", "# A\n", false)
	valid := writeProj(t, dir, "wc-ab2c", "beta", "2020-01-01T00:00:00Z", "a1", "# B\n", false)

	occ := map[string]string{"ab2c": degraded}
	_, renames, err := DuplicateID("wc", []Row{row(degraded, "wc-ab2c", "a0"), row(valid, "wc-ab2c", "a1")}, occ)
	if err != nil {
		t.Fatal(err)
	}
	if renames[0].OldPath != valid {
		t.Errorf("degraded created must be kept; the valid side renamed, got %s renamed", renames[0].OldPath)
	}
}

// Equal created falls to basename tie-break: greater basename is renamed.
func TestDuplicateIDBasenameTieBreak(t *testing.T) {
	dir := t.TempDir()
	alpha := writeProj(t, dir, "wc-ab2c", "alpha", "2026-01-01T00:00:00Z", "a0", "# A\n", false)
	beta := writeProj(t, dir, "wc-ab2c", "beta", "2026-01-01T00:00:00Z", "a1", "# B\n", false)

	occ := map[string]string{"ab2c": alpha}
	_, renames, err := DuplicateID("wc", []Row{row(alpha, "wc-ab2c", "a0"), row(beta, "wc-ab2c", "a1")}, occ)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(renames[0].OldPath) != "wc-ab2c-beta.md" {
		t.Errorf("greater basename must be renamed, got %s", renames[0].OldPath)
	}
}

// Equal created and basename falls to SHA-256: greater digest is renamed.
func TestDuplicateIDSHATieBreak(t *testing.T) {
	dir := t.TempDir()
	rootFile := writeProj(t, dir, "wc-ab2c", "same", "2026-01-01T00:00:00Z", "a0", "# ROOT BODY\n", false)
	archFile := writeProj(t, dir, "wc-ab2c", "same", "2026-01-01T00:00:00Z", "a0", "# ARCHIVE BODY\n", true)

	rootBytes, _ := os.ReadFile(rootFile)
	archBytes, _ := os.ReadFile(archFile)
	rootSum := sha256.Sum256(rootBytes)
	archSum := sha256.Sum256(archBytes)
	wantRenamed := rootFile
	if string(archSum[:]) > string(rootSum[:]) {
		wantRenamed = archFile
	}

	occ := map[string]string{"ab2c": rootFile}
	_, renames, err := DuplicateID("wc", []Row{row(rootFile, "wc-ab2c", "a0"), row(archFile, "wc-ab2c", "a0")}, occ)
	if err != nil {
		t.Fatal(err)
	}
	if renames[0].OldPath != wantRenamed {
		t.Errorf("greater SHA-256 must be renamed, got %s want %s", renames[0].OldPath, wantRenamed)
	}
}

// Cap-8 short-id exhaustion hard-fails rather than inventing a non-prefix id.
func TestDuplicateIDCapExhaustionHardFails(t *testing.T) {
	dir := t.TempDir()
	p1 := writeProj(t, dir, "wc-abcdefgh", "one", "2026-01-01T00:00:00Z", "a0", "# One\n", false)
	p2 := writeProj(t, dir, "wc-abcdefgh", "two", "2026-02-01T00:00:00Z", "a1", "# Two\n", false)

	occ := map[string]string{"abcdefgh": p1}
	_, _, err := DuplicateID("wc", []Row{row(p1, "wc-abcdefgh", "a0"), row(p2, "wc-abcdefgh", "a1")}, occ)
	if err == nil {
		t.Fatal("cap-8 exhaustion must hard-fail")
	}
	if !strings.Contains(err.Error(), p1) && !strings.Contains(err.Error(), p2) {
		t.Errorf("exhaustion error should name the collided paths, got %v", err)
	}
}

// EqualOrder re-spaces only tied files, preserving (order, id) order.
func TestEqualOrderRespacePreservesOrder(t *testing.T) {
	dir := t.TempDir()
	a := writeProj(t, dir, "wc-aaaa", "a", "2026-01-01T00:00:00Z", "a0", "# A\n", false)
	b := writeProj(t, dir, "wc-bbbb", "b", "2026-01-01T00:00:00Z", "a1", "# B\n", false)
	c := writeProj(t, dir, "wc-cccc", "c", "2026-01-01T00:00:00Z", "a1", "# C\n", false)
	e := writeProj(t, dir, "wc-eeee", "e", "2026-01-01T00:00:00Z", "a2", "# E\n", false)

	rows := []Row{row(a, "wc-aaaa", "a0"), row(b, "wc-bbbb", "a1"), row(c, "wc-cccc", "a1"), row(e, "wc-eeee", "a2")}
	ops, err := EqualOrder(rows)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rewrite.Apply(ops); err != nil {
		t.Fatal(err)
	}
	ka, kb, kc, ke := orderOf(t, a), orderOf(t, b), orderOf(t, c), orderOf(t, e)
	if ka != "a0" || ke != "a2" {
		t.Errorf("untied neighbours must not move: a=%q e=%q", ka, ke)
	}
	if ka >= kb || kb >= kc || kc >= ke {
		t.Errorf("re-space must preserve order: %q %q %q %q", ka, kb, kc, ke)
	}
	if kb == kc {
		t.Errorf("tied keys must become distinct, both %q", kb)
	}
}

// LongOrder shortens a pathologically long key while preserving order.
func TestLongOrderShortens(t *testing.T) {
	dir := t.TempDir()
	longKey := "a1" + strings.Repeat("V", 80)
	if !order.Valid(longKey) {
		t.Fatalf("test key not valid: %q", longKey)
	}
	a := writeProj(t, dir, "wc-aaaa", "a", "2026-01-01T00:00:00Z", "a0", "# A\n", false)
	b := writeProj(t, dir, "wc-bbbb", "b", "2026-01-01T00:00:00Z", longKey, "# B\n", false)
	e := writeProj(t, dir, "wc-eeee", "e", "2026-01-01T00:00:00Z", "a2", "# E\n", false)

	rows := []Row{row(a, "wc-aaaa", "a0"), row(b, "wc-bbbb", longKey), row(e, "wc-eeee", "a2")}
	ops, err := LongOrder(rows)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rewrite.Apply(ops); err != nil {
		t.Fatal(err)
	}
	kb := orderOf(t, b)
	if len(kb) > OrderLongThreshold {
		t.Errorf("long key must be shortened, got %d chars", len(kb))
	}
	if kb <= orderOf(t, a) || kb >= orderOf(t, e) {
		t.Errorf("re-space must preserve order, got %q between %q and %q", kb, orderOf(t, a), orderOf(t, e))
	}
}

func orderOf(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "order:") {
			return strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "order:")), `"`)
		}
	}
	return ""
}

// Same-id pair that is byte-identical at root and under archive/ is an interrupted move, not a collision.
func TestInterruptedMove(t *testing.T) {
	tests := []struct {
		name  string
		build func(t *testing.T, dir string) []Row
		want  bool
	}{
		{
			name: "identical copies across the archive boundary resume",
			build: func(t *testing.T, dir string) []Row {
				a := writeProj(t, dir, "wc-ab2c", "alpha", "2026-01-01T00:00:00Z", "a0", "# A\n", false)
				b := writeProj(t, dir, "wc-ab2c", "alpha", "2026-01-01T00:00:00Z", "a0", "# A\n", true)
				return []Row{row(a, "wc-ab2c", "a0"), row(b, "wc-ab2c", "a0")}
			},
			want: true,
		},
		{
			name: "differing bytes across the boundary are a real collision",
			build: func(t *testing.T, dir string) []Row {
				a := writeProj(t, dir, "wc-ab2c", "alpha", "2026-01-01T00:00:00Z", "a0", "# A\n", false)
				b := writeProj(t, dir, "wc-ab2c", "alpha", "2026-01-01T00:00:00Z", "a0", "# Different\n", true)
				return []Row{row(a, "wc-ab2c", "a0"), row(b, "wc-ab2c", "a0")}
			},
			want: false,
		},
		{
			name: "two copies on the same side are a real collision",
			build: func(t *testing.T, dir string) []Row {
				a := writeProj(t, dir, "wc-ab2c", "alpha", "2026-01-01T00:00:00Z", "a0", "# A\n", false)
				b := writeProj(t, dir, "wc-ab2c", "beta", "2026-01-01T00:00:00Z", "a0", "# A\n", false)
				return []Row{row(a, "wc-ab2c", "a0"), row(b, "wc-ab2c", "a0")}
			},
			want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			rows := tc.build(t, dir)
			got, err := InterruptedMove(dir, rows)
			if err != nil {
				t.Fatalf("InterruptedMove: %v", err)
			}
			if got != tc.want {
				t.Errorf("InterruptedMove = %v, want %v", got, tc.want)
			}
		})
	}
}

// Re-entry across the both-present crash window finishes the interrupted extension.
func TestDuplicateIDResumesInterruptedExtension(t *testing.T) {
	dir := t.TempDir()
	kept := writeProj(t, dir, "wc-ab2c", "alpha", "2026-01-01T00:00:00Z", "a0", "# A\n", false)
	loser := writeProj(t, dir, "wc-ab2c", "beta", "2026-01-02T00:00:00Z", "a1", "# B\n", false)
	rows := []Row{row(kept, "wc-ab2c", "a0"), row(loser, "wc-ab2c", "a1")}

	occupied := map[string]string{"ab2c": kept}
	ops, renames, err := DuplicateID("wc", rows, occupied)
	if err != nil {
		t.Fatalf("DuplicateID: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("want one rename op, got %d", len(ops))
	}

	// Crash window: new-id file written, old-id file not yet removed.
	if err := os.WriteFile(ops[0].NewPath, ops[0].Content, 0o644); err != nil {
		t.Fatal(err)
	}
	extended := strings.TrimPrefix(renames[0].NewID, "wc-")

	reOps, reRenames, err := DuplicateID("wc", rows, map[string]string{"ab2c": kept, extended: ops[0].NewPath})
	if err != nil {
		t.Fatalf("re-entry DuplicateID: %v", err)
	}
	if len(reOps) != 1 {
		t.Fatalf("re-entry want one op, got %d", len(reOps))
	}
	if reRenames[0].NewID != renames[0].NewID {
		t.Errorf("re-entry must reuse the first extension %q, minted %q", renames[0].NewID, reRenames[0].NewID)
	}
	if reOps[0].NewPath != ops[0].NewPath {
		t.Errorf("re-entry must target the existing extended file %q, got %q", ops[0].NewPath, reOps[0].NewPath)
	}
	if reOps[0].OldPath != loser {
		t.Errorf("re-entry must remove the stale old-id file %q, got %q", loser, reOps[0].OldPath)
	}
	if !bytes.Equal(reOps[0].Content, ops[0].Content) {
		t.Errorf("re-entry must rewrite the same bytes")
	}
}

// Hand-edited extension is not this loser's content; skip and mint a fresh extension.
func TestDuplicateIDIgnoresUnrelatedExtension(t *testing.T) {
	dir := t.TempDir()
	kept := writeProj(t, dir, "wc-ab2c", "alpha", "2026-01-01T00:00:00Z", "a0", "# A\n", false)
	loser := writeProj(t, dir, "wc-ab2c", "beta", "2026-01-02T00:00:00Z", "a1", "# B\n", false)
	other := writeProj(t, dir, "wc-ab2ca", "gamma", "2026-01-03T00:00:00Z", "a2", "# G\n", false)

	rows := []Row{row(kept, "wc-ab2c", "a0"), row(loser, "wc-ab2c", "a1")}
	_, renames, err := DuplicateID("wc", rows, map[string]string{"ab2c": kept, "ab2ca": other})
	if err != nil {
		t.Fatalf("DuplicateID: %v", err)
	}
	if renames[0].NewID == "wc-ab2ca" {
		t.Errorf("must not adopt an unrelated ticket's id")
	}
}

// Basename/frontmatter-id disagreement: composed name still uses new id + slug tail.
func TestBasename(t *testing.T) {
	cases := []struct{ base, newID, want string }{
		{"wc-ab2c-beta.md", "wc-ab2ca", "wc-ab2ca-beta.md"},
		{"wc-ab2c.md", "wc-ab2ca", "wc-ab2ca.md"},
		{"wc-ab2c-multi-word-slug.md", "wc-ab2ca", "wc-ab2ca-multi-word-slug.md"},
		{"wc-zz9y-beta.md", "wc-ab2ca", "wc-ab2ca-beta.md"},
	}
	for _, tc := range cases {
		if got := Basename(tc.base, tc.newID); got != tc.want {
			t.Errorf("Basename(%q, %q) = %q, want %q", tc.base, tc.newID, got, tc.want)
		}
	}
}

func writeDesignProj(t *testing.T, dir, fullID, slug, created, extra, body string) string {
	t.Helper()
	target := filepath.Join(dir, "design")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	fm := "---\nid: " + fullID + "\nstatus: draft\ncreated: " + created + "\n" + extra + "---\n" + body
	path := filepath.Join(target, fullID+"-"+slug+".md")
	if err := os.WriteFile(path, []byte(fm), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func sharedRow(path, fullID string, design bool) SharedRow {
	short := fullID[strings.IndexByte(fullID, '-')+1:]
	return SharedRow{Row: Row{Path: path, FullID: fullID, ShortID: short}, Design: design}
}

// A design keeps the id, one ticket is extended, and an older design is also
// extended. The ticket takes the first free suffix so the design loser's links
// can name that id. Re-entry while the new files still sit beside the old ones
// reuses those ids.
func TestSharedIDRetargetOrdersTicketFirstAndResumes(t *testing.T) {
	dir := t.TempDir()
	keeper := writeDesignProj(t, dir, "wc-ab2c", "shape", "2019-01-01T00:00:00Z", "produces: [wc-ab2c]\n", "# Shape\n")
	designLoser := writeDesignProj(t, dir, "wc-ab2c", "other", "2020-01-01T00:00:00Z", "produces: [wc-ab2c]\n", "# Other\n")
	ticket := writeProj(t, dir, "wc-ab2c", "ticket", "2026-06-01T00:00:00Z", "a0", "# Ticket\n", false)
	ref := writeProj(t, dir, "wc-de34", "ref", "2026-01-01T00:00:00Z", "a1", "# Ref\n", false)
	refRaw, err := os.ReadFile(ref)
	if err != nil {
		t.Fatal(err)
	}
	refRaw = bytes.Replace(refRaw, []byte("created:"), []byte("depends: [wc-ab2c]\ncreated:"), 1)
	if err := os.WriteFile(ref, refRaw, 0o644); err != nil {
		t.Fatal(err)
	}

	rows := []SharedRow{
		sharedRow(ticket, "wc-ab2c", false),
		sharedRow(designLoser, "wc-ab2c", true),
		sharedRow(keeper, "wc-ab2c", true),
	}
	ops, renames, retarget, err := SharedID("wc", dir, rows, map[string]string{"ab2c": keeper, "de34": ref})
	if err != nil {
		t.Fatalf("SharedID: %v", err)
	}
	byOld := map[string]Rename{}
	for _, rn := range renames {
		byOld[rn.OldPath] = rn
	}
	if byOld[ticket].NewID != "wc-ab2ca" {
		t.Fatalf("ticket extension = %s, want wc-ab2ca", byOld[ticket].NewID)
	}
	if byOld[designLoser].NewID != "wc-ab2cb" {
		t.Fatalf("design extension = %s, want wc-ab2cb", byOld[designLoser].NewID)
	}
	if filepath.Dir(byOld[designLoser].NewPath) != filepath.Join(dir, "design") {
		t.Fatalf("design loser left design/: %s", byOld[designLoser].NewPath)
	}
	if retarget != "wc-ab2ca" {
		t.Fatalf("retarget = %s", retarget)
	}
	content := opContent(t, ops)
	loserRaw := content[byOld[designLoser].NewPath]
	if bytes.Contains(loserRaw, []byte("order:")) || !bytes.Contains(loserRaw, []byte("id: wc-ab2cb")) || !bytes.Contains(loserRaw, []byte("produces: [wc-ab2ca]")) {
		t.Fatalf("design loser = %q", loserRaw)
	}
	keptRaw := content[keeper]
	if !bytes.Contains(keptRaw, []byte("id: wc-ab2c")) || !bytes.Contains(keptRaw, []byte("produces: [wc-ab2ca]")) || bytes.Contains(keptRaw, []byte("order:")) {
		t.Fatalf("keeper = %q", keptRaw)
	}
	refAfter := content[ref]
	if !bytes.Contains(refAfter, []byte("depends: [wc-ab2ca]")) {
		t.Fatalf("depends = %q", refAfter)
	}

	// Crash window: new-id files written, old-id files not yet removed.
	for _, op := range ops {
		if op.OldPath != "" && op.OldPath != op.NewPath {
			if err := os.MkdirAll(filepath.Dir(op.NewPath), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(op.NewPath, op.Content, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	occ := map[string]string{"ab2c": keeper, "de34": ref}
	for _, rn := range renames {
		occ[strings.TrimPrefix(rn.NewID, "wc-")] = rn.NewPath
	}
	reOps, reRenames, reTarget, err := SharedID("wc", dir, rows, occ)
	if err != nil {
		t.Fatalf("re-entry: %v", err)
	}
	if reTarget != retarget {
		t.Fatalf("re-entry retarget = %s, want %s", reTarget, retarget)
	}
	if len(reRenames) != len(renames) {
		t.Fatalf("re-entry renames = %d, want %d", len(reRenames), len(renames))
	}
	reByOld := map[string]Rename{}
	for _, rn := range reRenames {
		reByOld[rn.OldPath] = rn
	}
	reContent := opContent(t, reOps)
	for _, rn := range renames {
		got := reByOld[rn.OldPath]
		if got.NewID != rn.NewID || got.NewPath != rn.NewPath {
			t.Fatalf("re-entry rename = %+v, want %+v", got, rn)
		}
		if !bytes.Equal(reContent[rn.NewPath], content[rn.NewPath]) {
			t.Fatalf("re-entry bytes for %s differ", rn.NewPath)
		}
	}
	if !bytes.Contains(reContent[keeper], []byte("produces: [wc-ab2ca]")) {
		t.Fatalf("re-entry keeper = %q", reContent[keeper])
	}
	if !bytes.Contains(reContent[ref], []byte("depends: [wc-ab2ca]")) {
		t.Fatalf("re-entry depends = %q", reContent[ref])
	}
}

func opContent(t *testing.T, ops []rewrite.Op) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	for _, op := range ops {
		out[op.NewPath] = op.Content
	}
	return out
}
