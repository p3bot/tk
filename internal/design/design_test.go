package design

import (
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/frontmatter"
)

func TestSerializeKeepsPresentChanged(t *testing.T) {
	m := &frontmatter.Model{
		ID: "wc-ab2c", Status: "draft", Changed: "2026-02-02T03:04:05Z",
		Order: "a0", Created: "2026-01-01T00:00:00Z",
	}
	out, err := Serialize(m)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	statusAt := strings.Index(text, "status:")
	changedAt := strings.Index(text, "changed:")
	orderAt := strings.Index(text, "order:")
	if statusAt < 0 || changedAt < 0 || orderAt < 0 || statusAt >= changedAt || changedAt >= orderAt {
		t.Fatalf("changed must follow status and precede order:\n%s", text)
	}
	back, err := frontmatter.Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if back.Changed != m.Changed {
		t.Fatalf("changed = %q", back.Changed)
	}

	m.Changed = ""
	out, err = Serialize(m)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "changed:") {
		t.Fatalf("empty changed was written:\n%s", out)
	}
}
