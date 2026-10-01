package repair

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/rewrite"
)

func writeRel(t *testing.T, dir, rel, body string) string {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func ticket(id, created, extra, body string) string {
	return "---\nid: " + id + "\nstatus: todo\norder: \"a0\"\ncreated: " + created + "\n" + extra + "---\n" + body
}

func TestAdoptPrefixRewritesForeignIDAndKeepsBytesWhenFenceAlreadyMatches(t *testing.T) {
	dir := t.TempDir()
	raw := ticket("wc-b999", "2026-01-01T00:00:00Z", "", "# X\n")
	writeRel(t, dir, "at-a575-slug.md", raw)

	ops, renames, err := AdoptPrefix("wc", dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(renames) != 1 || renames[0].NewID != "wc-b999" {
		t.Fatalf("renames = %+v", renames)
	}
	if len(renames[0].Claimed) != 1 || renames[0].Claimed[0] != "at-a575" {
		t.Fatalf("claimed = %v, same-scope fence id must not be retargeted", renames[0].Claimed)
	}
	if string(ops[0].Content) != raw {
		t.Fatalf("fence that already names the new id must be moved unchanged:\n%s", ops[0].Content)
	}
	if filepath.Base(ops[0].NewPath) != "wc-b999-slug.md" {
		t.Fatalf("new path = %s", ops[0].NewPath)
	}
}

func TestAdoptPrefixExtendsWithoutStealingSameScopeID(t *testing.T) {
	dir := t.TempDir()
	keep := ticket("wc-b999", "2020-01-01T00:00:00Z", "", "# Keep\n")
	keepPath := writeRel(t, dir, "wc-b999-keep.md", keep)
	writeRel(t, dir, "at-a575-foreign.md", ticket("wc-b999", "2026-01-01T00:00:00Z", "", "# Foreign\n"))
	writeRel(t, dir, "wc-cccc-ref.md", ticket("wc-cccc", "2026-01-01T00:00:00Z", "depends: [at-a575]\nrelated: [wc-b999]\n", "# Ref\n"))

	ops, renames, err := AdoptPrefix("wc", dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(renames) != 1 || renames[0].NewID != "wc-b999a" {
		t.Fatalf("renames = %+v", renames)
	}
	for _, id := range renames[0].Claimed {
		if id == "wc-b999" {
			t.Fatal("claimed the existing ticket's id")
		}
	}
	var foreign, ref []byte
	for _, op := range ops {
		switch op.OldPath {
		case keepPath:
			t.Fatal("existing ticket was rewritten")
		default:
			if strings.Contains(string(op.Content), "# Foreign") {
				foreign = op.Content
			}
			if strings.Contains(string(op.Content), "# Ref") {
				ref = op.Content
			}
		}
	}
	if !strings.Contains(string(foreign), "id: wc-b999a") {
		t.Fatalf("extended file id = %s", foreign)
	}
	if strings.Contains(string(ref), "at-a575") || !strings.Contains(string(ref), "depends: [wc-b999a]") {
		t.Fatalf("filename id must follow the adopted file: %s", ref)
	}
	if !strings.Contains(string(ref), "related: [wc-b999]") {
		t.Fatalf("edges to the existing ticket must stay: %s", ref)
	}
}

func TestAdoptPrefixKeeperClaimsSharedID(t *testing.T) {
	dir := t.TempDir()
	writeRel(t, dir, "at-a575-beta.md", ticket("at-a575", "2026-06-01T00:00:00Z", "", "# Beta\n"))
	writeRel(t, dir, "at-a575-alpha.md", ticket("at-a575", "2020-01-01T00:00:00Z", "", "# Alpha\n"))
	writeRel(t, dir, "wc-cccc-ref.md", ticket("wc-cccc", "2026-01-01T00:00:00Z", "depends: [at-a575]\n", "# Ref\n"))

	_, renames, err := AdoptPrefix("wc", dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	byOld := map[string]PrefixRename{}
	for _, r := range renames {
		byOld[filepath.Base(r.OldPath)] = r
	}
	if byOld["at-a575-alpha.md"].NewID != "wc-a575" {
		t.Fatalf("older file must keep the short, got %+v", byOld["at-a575-alpha.md"])
	}
	if byOld["at-a575-beta.md"].NewID != "wc-a575a" {
		t.Fatalf("newer file must extend, got %+v", byOld["at-a575-beta.md"])
	}
	if len(byOld["at-a575-alpha.md"].Claimed) != 1 || byOld["at-a575-alpha.md"].Claimed[0] != "at-a575" {
		t.Fatalf("keeper must claim the shared id, got %+v", byOld["at-a575-alpha.md"])
	}
	if len(byOld["at-a575-beta.md"].Claimed) != 0 {
		t.Fatalf("loser must not retarget the shared id, got %+v", byOld["at-a575-beta.md"])
	}
}

func TestAdoptPrefixSharedFilenameFollowsOlderFile(t *testing.T) {
	dir := t.TempDir()
	writeRel(t, dir, "archive/at-a575-old.md", ticket("at-a575", "2020-01-01T00:00:00Z", "", "# Old\n"))
	writeRel(t, dir, "at-a575-new.md", ticket("xy-a574", "2026-06-01T00:00:00Z", "", "# New\n"))
	writeRel(t, dir, "wc-cccc-ref.md", ticket("wc-cccc", "2026-01-01T00:00:00Z", "depends: [at-a575]\nrelated: [xy-a574]\n", "# Ref\n"))

	ops, renames, err := AdoptPrefix("wc", dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	byBase := map[string]PrefixRename{}
	for _, r := range renames {
		byBase[filepath.Base(r.OldPath)] = r
	}
	old, neu := byBase["at-a575-old.md"], byBase["at-a575-new.md"]
	if old.NewID != "wc-a575" || len(old.Claimed) != 1 || old.Claimed[0] != "at-a575" {
		t.Fatalf("older file must inherit at-a575, got %+v", old)
	}
	if neu.NewID != "wc-a574" || len(neu.Claimed) != 1 || neu.Claimed[0] != "xy-a574" {
		t.Fatalf("newer file must keep only its fence id, got %+v", neu)
	}
	var ref []byte
	for _, op := range ops {
		if strings.Contains(string(op.Content), "# Ref") {
			ref = op.Content
		}
	}
	if !strings.Contains(string(ref), "depends: [wc-a575]") || !strings.Contains(string(ref), "related: [wc-a574]") {
		t.Fatalf("edges must follow the older file: %s", ref)
	}
}

func TestAdoptPrefixLeavesLiveFenceID(t *testing.T) {
	dir := t.TempDir()
	writeRel(t, dir, "at-a575-slug.md", ticket("api-mm22", "2026-01-01T00:00:00Z", "", "# Foreign\n"))
	writeRel(t, dir, "wc-cccc-ref.md", ticket("wc-cccc", "2026-01-01T00:00:00Z", "depends: [at-a575]\nrelated: [api-mm22]\n", "# Ref\n"))

	ops, renames, err := AdoptPrefix("wc", dir, map[string]struct{}{"api-mm22": {}})
	if err != nil {
		t.Fatal(err)
	}
	if len(renames) != 1 || renames[0].NewID != "wc-mm22" {
		t.Fatalf("renames = %+v", renames)
	}
	if len(renames[0].Claimed) != 1 || renames[0].Claimed[0] != "at-a575" {
		t.Fatalf("live fence id must not be claimed, got %v", renames[0].Claimed)
	}
	var foreign, ref []byte
	for _, op := range ops {
		if strings.Contains(string(op.Content), "# Foreign") {
			foreign = op.Content
		}
		if strings.Contains(string(op.Content), "# Ref") {
			ref = op.Content
		}
	}
	if !strings.Contains(string(foreign), "id: wc-mm22") || strings.Contains(string(foreign), "api-mm22") {
		t.Fatalf("stray file must take its own id: %s", foreign)
	}
	if !strings.Contains(string(ref), "depends: [wc-mm22]") || !strings.Contains(string(ref), "related: [api-mm22]") {
		t.Fatalf("edges to the live ticket must stay: %s", ref)
	}
}

func TestAdoptPrefixLeavesLiveFilenameID(t *testing.T) {
	dir := t.TempDir()
	writeRel(t, dir, "at-a575-slug.md", ticket("at-a575", "2026-01-01T00:00:00Z", "", "# Copy\n"))
	refPath := writeRel(t, dir, "wc-cccc-ref.md", ticket("wc-cccc", "2026-01-01T00:00:00Z", "depends: [at-a575]\n", "# Ref\n"))

	ops, renames, err := AdoptPrefix("wc", dir, map[string]struct{}{"at-a575": {}})
	if err != nil {
		t.Fatal(err)
	}
	if len(renames) != 1 || renames[0].NewID != "wc-a575" || len(renames[0].Claimed) != 0 {
		t.Fatalf("live filename id must not be claimed, got %+v", renames)
	}
	for _, op := range ops {
		if op.OldPath == refPath {
			t.Fatalf("edges to the live id must stay: %s", op.Content)
		}
		if strings.Contains(string(op.Content), "# Copy") && !strings.Contains(string(op.Content), "id: wc-a575") {
			t.Fatalf("copy must still adopt a new id: %s", op.Content)
		}
	}
}

func TestAdoptPrefixRenamesUnparseableWithoutEditingFence(t *testing.T) {
	dir := t.TempDir()
	raw := "---\nid: at-a575\nstatus: [unterminated\n---\n# broke\n"
	writeRel(t, dir, "at-a575-x.md", raw)

	ops, renames, err := AdoptPrefix("wc", dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 2 || string(ops[0].Content) != raw || string(ops[1].Content) != raw {
		t.Fatalf("unparseable fence must be renamed unchanged: %+v %s", renames, ops[0].Content)
	}
	if ops[0].OldPath != "" || ops[1].OldPath == "" {
		t.Fatalf("plant must keep the old name, unlink removes it: %+v", ops)
	}
	if filepath.Base(ops[0].NewPath) != "wc-a575-x.md" {
		t.Fatalf("new path = %s", ops[0].NewPath)
	}
}

func TestAdoptPrefixParkedOptionalDir(t *testing.T) {
	for _, name := range []string{"archive", "design"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, name), []byte("parked\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			writeRel(t, dir, "at-a575-slug.md", ticket("at-a575", "2026-01-01T00:00:00Z", "", "# Shape\n"))
			ops, renames, err := AdoptPrefix("wc", dir, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(renames) != 1 || renames[0].NewID != "wc-a575" {
				t.Fatalf("renames = %+v", renames)
			}
			if len(ops) != 2 || filepath.Base(ops[0].NewPath) != "wc-a575-slug.md" {
				t.Fatalf("ops = %+v", ops)
			}
		})
	}
}

func TestAdoptPrefixRefusesUnparseableAdopteeMention(t *testing.T) {
	dir := t.TempDir()
	writeRel(t, dir, "at-a575-ok.md", ticket("at-a575", "2026-01-01T00:00:00Z", "", "# Ok\n"))
	raw := "---\nid: at-b999\ndepends: [at-a575]\nstatus: [unterminated\n---\n# broke\n"
	writeRel(t, dir, "at-b999-bad.md", raw)

	_, _, err := AdoptPrefix("wc", dir, nil)
	if err == nil || !strings.Contains(err.Error(), "parse_error:") || !strings.Contains(err.Error(), "at-a575") {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "at-a575-ok.md")); statErr != nil {
		t.Fatal("planning must not write")
	}
}

func TestAdoptPrefixRefusesUnparseableReferrer(t *testing.T) {
	dir := t.TempDir()
	writeRel(t, dir, "at-a575-x.md", ticket("at-a575", "2026-01-01T00:00:00Z", "", "# X\n"))
	writeRel(t, dir, "wc-cccc-ref.md", "---\nid: wc-cccc\ndepends: [at-a575]\nstatus: [unterminated\n---\n# Ref\n")

	if _, _, err := AdoptPrefix("wc", dir, nil); err == nil || !strings.Contains(err.Error(), "parse_error:") || !strings.Contains(err.Error(), "at-a575") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "at-a575-x.md")); err != nil {
		t.Fatal("planning must not write")
	}
}

func TestAdoptPrefixReentryKeepsPlantedID(t *testing.T) {
	dir := t.TempDir()
	writeRel(t, dir, "at-a575-shape.md", ticket("at-a575", "2026-01-01T00:00:00Z", "depends: [at-b999]\n", "# Shape\n"))
	writeRel(t, dir, "at-b999-old.md", ticket("at-b999", "2020-01-01T00:00:00Z", "", "# Old\n"))
	writeRel(t, dir, "wc-cccc-ref.md", ticket("wc-cccc", "2026-01-01T00:00:00Z", "depends: [at-a575]\n", "# Ref\n"))

	ops, renames, err := AdoptPrefix("wc", dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := applyPlants(ops); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "at-a575-shape.md")); err != nil {
		t.Fatal("plant must leave the old name")
	}

	_, again, err := AdoptPrefix("wc", dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if fmtRenames(again) != fmtRenames(renames) {
		t.Fatalf("re-entry ids = %s, want %s", fmtRenames(again), fmtRenames(renames))
	}
}

func TestAdoptPrefixDoesNotResumeDifferentTicket(t *testing.T) {
	dir := t.TempDir()
	kept := writeRel(t, dir, "wc-a575-shape.md", ticket("wc-a575", "2020-01-01T00:00:00Z", "", "# Real\n"))
	writeRel(t, dir, "at-a575-shape.md", ticket("at-a575", "2026-01-01T00:00:00Z", "", "# Stray\n"))

	ops, renames, err := AdoptPrefix("wc", dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(renames) != 1 || renames[0].NewID != "wc-a575a" {
		t.Fatalf("renames = %+v", renames)
	}
	for _, op := range ops {
		if op.NewPath == kept {
			t.Fatalf("existing ticket was reused: %+v", op)
		}
	}
}

func TestAdoptPrefixDoesNotResumeSameBodyDifferentEdges(t *testing.T) {
	dir := t.TempDir()
	kept := writeRel(t, dir, "wc-a575-shape.md", ticket("wc-a575", "2026-01-01T00:00:00Z", "depends: [wc-cccc]\n", "# Shape\n"))
	writeRel(t, dir, "at-a575-shape.md", ticket("at-a575", "2026-01-01T00:00:00Z", "", "# Shape\n"))

	ops, renames, err := AdoptPrefix("wc", dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(renames) != 1 || renames[0].NewID != "wc-a575a" {
		t.Fatalf("renames = %+v", renames)
	}
	for _, op := range ops {
		if op.NewPath == kept {
			t.Fatalf("existing ticket was reused: %+v", op)
		}
	}
}

func applyPlants(ops []rewrite.Op) error {
	for _, op := range ops {
		if op.OldPath != "" && op.OldPath != op.NewPath {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(op.NewPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(op.NewPath, op.Content, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func fmtRenames(renames []PrefixRename) string {
	parts := make([]string, len(renames))
	for i, r := range renames {
		parts[i] = filepath.Base(r.OldPath) + "->" + r.NewID + ":" + strings.Join(r.Claimed, "+")
	}
	return strings.Join(parts, ",")
}
