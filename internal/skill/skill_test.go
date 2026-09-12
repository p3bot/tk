package skill_test

import (
	"os"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/skill"
)

func TestRequiredHeadingsInOrder(t *testing.T) {
	text := skill.Text()
	var positions []int
	for _, h := range skill.RequiredHeadings() {
		marker := "\n## " + h + "\n"
		i := strings.Index(text, marker)
		if i < 0 {
			t.Fatalf("missing heading %q", h)
		}
		positions = append(positions, i)
	}
	for i := 1; i < len(positions); i++ {
		if positions[i] <= positions[i-1] {
			t.Fatalf("heading %q appears before %q", skill.RequiredHeadings()[i], skill.RequiredHeadings()[i-1])
		}
	}
	if !strings.HasPrefix(text, "---\nname: tk\n") {
		t.Fatal("skill must open with Agent Skills frontmatter (name: tk)")
	}
	if !strings.Contains(text, "description:") {
		t.Fatal("skill frontmatter must include description")
	}
	if !strings.Contains(text, "Ticket management") {
		t.Fatal("skill description must lead with Ticket management")
	}
	if !strings.Contains(text, "\n# Ticket management with tk\n") {
		t.Fatal("skill must have H1 # Ticket management with tk after frontmatter")
	}
	for _, bad := range []string{"(locked)", "TODO:", "TBD", "skeleton placeholder"} {
		if strings.Contains(text, bad) {
			t.Errorf("skill must not contain %q", bad)
		}
	}
}

func TestRequiredGuidancePresent(t *testing.T) {
	// Hot-path contracts the body must keep; not a full doctor token catalogue.
	text := skill.Text()
	needles := []string{
		"tk-driven",
		"repo-driven",
		"plain-files",
		"tk sync",
		"status_conflict",
		"next --claim",
		"tk mark <status> <id> [id...]",
		"one path per unique marked ticket",
		"tk order",
		"never host push",
		"stdout",
		"stderr",
		"frontmatter fence",
		"depends_open:",
		"required_missing:",
		"scope field",
		"tk scope auto-commit [true|false] [--scope S]",
		"[--strip]",
		"Do not hand-edit fences to drop undeclared keys",
		"tk repair` does not drop them",
		"mark does not enforce depends",
		"tk reindex",
		"sealed except via tk mutators",
		"H1: live title",
		"Body under the H1: edit in the file",
		"Whole-file rewrite of a ticket path is corruption",
		"parse_error:",
		"mutators refuse until parse succeeds",
		"do not cancel+recreate unless a human asks",
		"never invent",
		"--open",
		"terminal-only status filters reverse that order",
		"tk rehome <id> <dest-scope> [--scope S]",
		"tk create, get, next, and rehome print a cleaned absolute path",
		"next --claim, rehome, meta set/add/remove",
	}
	for _, n := range needles {
		if !strings.Contains(text, n) {
			t.Errorf("skill missing required guidance %q", n)
		}
	}
}

func TestSkillPulseCommand(t *testing.T) {
	text := skill.Text()
	if !strings.Contains(text, "tk pulse [key] [--scope S]") {
		t.Error("skill Commands must list tk pulse")
	}
	if !strings.Contains(text, "Orient: `tk pulse`") {
		t.Error("skill Orient must use tk pulse")
	}
	if !strings.Contains(text, "Durability (`tk pulse mode`)") {
		t.Error("skill Durability must use tk pulse mode")
	}
	if !strings.Contains(text, "Recovery: `tk pulse`") {
		t.Error("skill Recovery must use tk pulse")
	}
	if strings.Contains(text, "tk status [key]") || strings.Contains(text, "`tk status`") || strings.Contains(text, "`tk status mode`") {
		t.Error("skill must not list tk status as a command")
	}
	if !strings.Contains(text, "tk mark <status> <id> [id...]") {
		t.Error("skill must still teach ticket-field status via tk mark")
	}
}

func TestSkillCanonicalVerbNames(t *testing.T) {
	text := skill.Text()
	for _, n := range []string{
		"tk depends [<id>] [--scope S] [--transitive] [--tree] [--no-lens]",
		"tk meta remove <id> <key> <value> [--scope S]",
		"tk meta add|remove",
		"meta set/add/remove",
		"tk depends shows both directions",
	} {
		if !strings.Contains(text, n) {
			t.Errorf("skill missing canonical verb %q", n)
		}
	}
	for _, bad := range []string{
		"tk deps",
		"tk meta rm",
		"deps shows both directions",
		"meta set/add/rm",
		"meta add|rm",
	} {
		if strings.Contains(text, bad) {
			t.Errorf("skill must not teach alias or short verb %q", bad)
		}
	}
	if !strings.Contains(text, "tk depends --tree") {
		t.Error("skill must document depends --tree")
	}
	if !strings.Contains(text, "─┬─") || !strings.Contains(text, "pretty-print") {
		t.Error("skill must show the --tree box-drawing shape as pretty-print, not TSV")
	}
	if !strings.Contains(text, "lens unless `--no-lens`") {
		t.Error("skill must teach depends --tree as the default board, lens unless --no-lens")
	}
}

func TestFileProtocolDoesNotTeachRejectedAuthoring(t *testing.T) {
	text := skill.Text()
	if strings.Contains(text, "tk body") {
		t.Error("skill must not document a tk body verb")
	}
	if strings.Contains(text, "create --body") || strings.Contains(text, "[--body]") {
		t.Error("skill must not document create --body")
	}
	if strings.Contains(text, "tk mark <id> <status>") {
		t.Error("skill must not teach the old mark argv; status is first")
	}
}

func TestRequiredSectionsOnly(t *testing.T) {
	text := skill.Text()
	count := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "## ") {
			count++
		}
	}
	want := len(skill.RequiredHeadings())
	if count != want {
		t.Fatalf("want %d ## sections, got %d", want, count)
	}
}

func TestSkillListsScopeAutoCommit(t *testing.T) {
	text := skill.Text()
	if !strings.Contains(text, "tk scope auto-commit [true|false] [--scope S]") {
		t.Error("skill Commands must list tk scope auto-commit")
	}
	manage := ""
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "Manage scopes:") {
			manage = line
			break
		}
	}
	if manage == "" {
		t.Error("skill must have a Manage scopes line")
	} else if !strings.Contains(manage, "auto-commit") {
		t.Errorf("skill Manage scopes must name auto-commit, got %q", manage)
	}
	if !strings.Contains(text, "scope auto-commit") {
		t.Error("skill self-commit list must name scope auto-commit")
	}
	if !strings.Contains(text, "later mutators are repo-driven") {
		t.Error("skill must teach the false-flip last-commit boundary")
	}
	if !strings.Contains(text, "allowlisted dirty") {
		t.Error("skill must teach that false flip snapshots leftover allowlisted dirt")
	}
	if strings.Contains(text, "tk scope mode") {
		t.Error("skill must not list a tk scope mode writer")
	}
}

func TestSkillDoesNotTeachMe(t *testing.T) {
	text := skill.Text()
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "tk me" || strings.HasPrefix(trimmed, "tk me ") {
			t.Errorf("skill must not teach tk me as a verb: %q", line)
		}
	}
	if !strings.Contains(text, "registry, lens, and me only") {
		t.Error("skill forget line must name the me entry alongside registry and lens")
	}
}

func TestSkillDoesNotTeachDoctorReindex(t *testing.T) {
	text := skill.Text()
	if strings.Contains(text, "tk doctor --reindex") || strings.Contains(text, "[--reindex]") {
		t.Error("skill must not list --reindex on doctor; cache rebuild is tk reindex")
	}
	if !strings.Contains(text, "tk reindex") {
		t.Error("skill must teach tk reindex")
	}
}

func TestSkillListsRepair(t *testing.T) {
	text := skill.Text()
	if !strings.Contains(text, "tk repair [--re-space-order] [--all]") {
		t.Error("skill Commands must list tk repair")
	}
	if strings.Contains(text, "tk doctor [--repair]") || strings.Contains(text, "tk doctor --repair") || strings.Contains(text, "tk doctor --re-space-order") {
		t.Error("skill must not list mutating flags on tk doctor")
	}
	if !strings.Contains(text, "Integrity: `tk doctor` -> `tk repair` | `tk repair --re-space-order` | `tk repair --all`") {
		t.Error("skill Integrity must name tk repair --all as a full command")
	}
	if !strings.Contains(text, "Recovery: `tk pulse` -> `tk doctor` -> `tk repair`") {
		t.Error("skill Recovery must go pulse then doctor then repair")
	}
}

func TestNoDesignDependency(t *testing.T) {
	// skill.md is sole runtime contract; body and production sources must not load design.md.
	text := skill.Text()
	if strings.Contains(text, "design.md") {
		t.Error("skill body must not tell agents to read design.md as a runtime dependency")
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	loadMarkers := []string{
		`"design.md"`,
		"`design.md`",
		"//go:embed design.md",
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		for _, m := range loadMarkers {
			if strings.Contains(src, m) {
				t.Errorf("%s must not load design.md (found %s)", name, m)
			}
		}
	}
}
