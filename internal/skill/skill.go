// Package skill holds the agent skill contract printed by `tk skill`.
// skill.md is the sole embedded source; this package must not load any external design document.
package skill

import (
	_ "embed"
	"strings"
)

//go:embed skill.md
var embedded string

// requiredHeadings is the locked ## TOC the embedded contract must contain, in order.
var requiredHeadings = []string{
	"Frontmatter",
	"Commands",
	"Designs",
	"Identifiers",
	"Workflows",
}

// Text returns the full agent skill contract as markdown, ready for stdout.
// A single trailing newline is guaranteed so writers do not double-terminate.
func Text() string {
	s := embedded
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	return s
}

// RequiredHeadings returns the ## section titles the contract must include, in order.
func RequiredHeadings() []string {
	out := make([]string, len(requiredHeadings))
	copy(out, requiredHeadings)
	return out
}
