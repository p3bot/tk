package cli

import (
	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/token"
)

// warnUnknownTags emits tag_unknown: on stderr for each distinct requested tag
// absent from the scope's in-use set. Soft only; callers keep their success path.
func warnUnknownTags(c *cobra.Command, requested []string, inUse map[string]struct{}) {
	for _, tag := range index.AbsentTags(requested, inUse) {
		stderrln(c, token.FormatTagUnknown(tag))
	}
}
