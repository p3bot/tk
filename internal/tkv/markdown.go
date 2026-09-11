package tkv

import (
	"bytes"
	"html/template"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

// Default renderer: CommonMark, raw HTML off (do not pass html.WithUnsafe).
var md = goldmark.New(
	goldmark.WithParserOptions(
		parser.WithAutoHeadingID(),
	),
)

type tocItem struct {
	Level int
	ID    string
	Text  string
	Kids  []tocItem
}

func convertMarkdown(src []byte) (template.HTML, []tocItem, error) {
	reader := text.NewReader(src)
	doc := md.Parser().Parse(reader)
	toc := outline(doc, src)
	var buf bytes.Buffer
	if err := md.Renderer().Render(&buf, src, doc); err != nil {
		return "", nil, err
	}
	return template.HTML(buf.String()), toc, nil
}

func outline(doc ast.Node, source []byte) []tocItem {
	var flat []tocItem
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		h, ok := n.(*ast.Heading)
		if !ok || h.Level < 2 {
			return ast.WalkContinue, nil
		}
		id := headingID(h)
		if id == "" {
			return ast.WalkSkipChildren, nil
		}
		flat = append(flat, tocItem{
			Level: h.Level,
			ID:    id,
			Text:  headingText(h, source),
		})
		return ast.WalkSkipChildren, nil
	})
	return nestTOC(flat)
}

func headingID(n ast.Node) string {
	v, ok := n.AttributeString("id")
	if !ok {
		return ""
	}
	switch t := v.(type) {
	case []byte:
		return string(t)
	case string:
		return t
	default:
		return ""
	}
}

func headingText(h *ast.Heading, source []byte) string {
	var b strings.Builder
	_ = ast.Walk(h, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch t := n.(type) {
		case *ast.Text:
			b.Write(t.Value(source))
		case *ast.String:
			b.Write(t.Value)
		}
		return ast.WalkContinue, nil
	})
	return strings.TrimSpace(b.String())
}

func nestTOC(flat []tocItem) []tocItem {
	var roots []tocItem
	for _, item := range flat {
		roots = appendTOC(roots, item)
	}
	return roots
}

func appendTOC(nodes []tocItem, item tocItem) []tocItem {
	if len(nodes) == 0 || nodes[len(nodes)-1].Level >= item.Level {
		return append(nodes, item)
	}
	i := len(nodes) - 1
	nodes[i].Kids = appendTOC(nodes[i].Kids, item)
	return nodes
}
