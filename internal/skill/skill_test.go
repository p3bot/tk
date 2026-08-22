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
		"never host push",
		"stdout",
		"stderr",
		"frontmatter fence",
		"depends_open:",
		"required_missing:",
		"scope field",
		"mark does not enforce depends",
		"tk reindex",
	}
	for _, n := range needles {
		if !strings.Contains(text, n) {
			t.Errorf("skill missing required guidance %q", n)
		}
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
