// Package title extracts the first ATX H1 from a markdown body.
// Pure and never fails: no match yields "", never a slug or summary fallback.
package title

import (
	"bytes"
	"regexp"
	"strings"
)

// atxH1 matches a single-# ATX heading: one '#' then whitespace then text.
// Required whitespace means '##' never matches.
var atxH1 = regexp.MustCompile(`^#\s+.+`)

// Extract returns the text of the first ATX H1 with actual text, or "".
// Skips empty '#   ' headings so a later real H1 is still found; ignores setext.
func Extract(body []byte) string {
	heading, _ := SplitH1(body)
	return heading
}

// SplitH1 returns the first ATX H1 text and the bytes after that heading line.
// No matching H1 yields heading "" and rest equal to body.
func SplitH1(body []byte) (heading string, rest []byte) {
	remaining := body
	for len(remaining) > 0 {
		var line []byte
		if i := bytes.IndexByte(remaining, '\n'); i >= 0 {
			line, remaining = remaining[:i], remaining[i+1:]
		} else {
			line, remaining = remaining, nil
		}
		line = bytes.TrimSuffix(line, []byte("\r"))
		if atxH1.Match(line) {
			if text := strings.TrimSpace(strings.TrimPrefix(string(line), "#")); text != "" {
				return text, remaining
			}
		}
	}
	return "", body
}
