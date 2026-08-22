package tkv

import (
	"bytes"
	"html/template"

	"github.com/yuin/goldmark"
)

// Default renderer: CommonMark, raw HTML off (do not pass html.WithUnsafe).
var md = goldmark.New()

func renderMarkdown(src []byte) (template.HTML, error) {
	var buf bytes.Buffer
	if err := md.Convert(src, &buf); err != nil {
		return "", err
	}
	return template.HTML(buf.String()), nil
}
