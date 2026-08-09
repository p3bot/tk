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

// noticeNewTag emits tag_new: when tag is not already in the pre-write in-use set.
// Soft only; emit only after a successful write. Empty tags are ignored (same as
// TagMembership / AbsentTags — they are never part of the in-use set).
func noticeNewTag(c *cobra.Command, tag string, inUse map[string]struct{}) {
	if tag == "" {
		return
	}
	if _, ok := inUse[tag]; ok {
		return
	}
	stderrln(c, token.FormatTagNew(tag))
}
