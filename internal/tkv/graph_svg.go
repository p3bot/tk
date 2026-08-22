package tkv

import (
	"bytes"
	"fmt"
	"html"
	"html/template"
	"strings"
)

func renderDepSVG(l depLayout) template.HTML {
	if len(l.Nodes) == 0 {
		return ""
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, `<svg class="depgraph" viewBox="0 0 %d %d" width="%d" height="%d" role="img" aria-label="depends graph">`,
		l.Width, l.Height, l.Width, l.Height)
	b.WriteString(`<defs><marker id="dep-arrow" viewBox="0 0 10 10" refX="8" refY="5" markerWidth="8" markerHeight="8" orient="auto-start-reverse"><path d="M 0 0 L 10 5 L 0 10 z" fill="#57534e"/></marker><marker id="dep-arrow-cycle" viewBox="0 0 10 10" refX="8" refY="5" markerWidth="8" markerHeight="8" orient="auto-start-reverse"><path d="M 0 0 L 10 5 L 0 10 z" fill="#9f1239"/></marker></defs>`)
	for _, e := range l.Edges {
		cls := "dep-edge"
		marker := "dep-arrow"
		if e.Cycle {
			cls = "dep-edge cycle"
			marker = "dep-arrow-cycle"
		}
		cx := (e.X1 + e.X2) / 2
		fmt.Fprintf(&b, `<path class="%s" d="M %d %d C %d %d, %d %d, %d %d" fill="none" marker-end="url(#%s)"/>`,
			cls, e.X1, e.Y1, cx, e.Y1, cx, e.Y2, e.X2, e.Y2, marker)
	}
	for _, n := range l.Nodes {
		cls := "dep-node status-" + statusClass(n.Status)
		if n.Unresolved {
			cls += " unresolved"
		}
		if n.External {
			cls += " external"
		}
		label := n.ShortID
		if n.External || n.Unresolved {
			label = n.ID
		}
		title := truncRunes(n.Title, 28)
		inner := fmt.Sprintf(
			`<rect x="%d" y="%d" width="%d" height="%d" rx="8"/>`+
				`<text class="dep-id" x="%d" y="%d">%s</text>`+
				`<text class="dep-title" x="%d" y="%d">%s</text>`,
			n.X, n.Y, nodeW, nodeH,
			n.X+12, n.Y+22, html.EscapeString(label),
			n.X+12, n.Y+42, html.EscapeString(title),
		)
		if n.Href != "" && !n.Unresolved {
			fmt.Fprintf(&b, `<a href="%s" class="%s">%s</a>`, html.EscapeString(n.Href), html.EscapeString(cls), inner)
		} else {
			fmt.Fprintf(&b, `<g class="%s">%s</g>`, html.EscapeString(cls), inner)
		}
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

func statusClass(st string) string {
	if st == "" {
		return "unknown"
	}
	var b strings.Builder
	for _, r := range st {
		switch {
		case r == '-':
			b.WriteByte('_')
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		default:
			if b.Len() == 0 {
				return "unknown"
			}
			return b.String()
		}
	}
	return b.String()
}

func truncRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n < 2 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}
