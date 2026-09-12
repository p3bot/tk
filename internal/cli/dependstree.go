package cli

import (
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/p3bot/tk/internal/id"
)

const (
	boxTee    = "─┬─"
	boxMid    = "├─"
	boxEnd    = "└─"
	boxOne    = "───"
	boxInline = "──"
)

// formatForest prints roots as a box-drawing outbound forest. Cycle-safe: a
// back edge is marked (cycle) and not expanded. A DAG join reprints the id
// without expanding it a second time. Another forest root reached as a
// descendant is a join (not expanded); that root still prints its own subtree.
// A target missing from byID is marked (unresolved) and not expanded.
func (g *dependsGraph) formatForest(roots []string, homeScope string) string {
	if len(roots) == 0 {
		return ""
	}
	rootSet := make(map[string]bool, len(roots))
	for _, r := range roots {
		rootSet[r] = true
	}
	var b strings.Builder
	onPath := map[string]bool{}
	expanded := map[string]bool{}
	for _, root := range roots {
		g.writeSubtree(&b, root, "", homeScope, onPath, expanded, rootSet)
	}
	return b.String()
}

func (g *dependsGraph) writeSubtree(b *strings.Builder, node, linePrefix, homeScope string, onPath, expanded, rootSet map[string]bool) {
	expanded[node] = true
	onPath[node] = true
	defer delete(onPath, node)

	label := displayTicketID(node, homeScope)
	children := g.sortedChildren(node)

	if len(children) == 0 {
		b.WriteString(linePrefix)
		b.WriteString(label)
		b.WriteByte('\n')
		return
	}

	if g.allLeafLike(children, onPath) {
		b.WriteString(linePrefix)
		b.WriteString(label)
		b.WriteString(" ")
		b.WriteString(boxInline)
		b.WriteString(" ")
		b.WriteString(g.formatLeafList(children, homeScope, onPath))
		b.WriteByte('\n')
		return
	}

	if len(children) == 1 {
		g.writeChild(b, children[0], linePrefix+label+" "+boxOne+" ", homeScope, onPath, expanded, rootSet)
		return
	}

	g.writeChild(b, children[0], linePrefix+label+" "+boxTee+" ", homeScope, onPath, expanded, rootSet)

	cont := strings.Repeat(" ", visLen(linePrefix)+visLen(label)+2)
	for i, ch := range children[1:] {
		conn := boxMid
		if i == len(children)-2 {
			conn = boxEnd
		}
		g.writeChild(b, ch, cont+conn+" ", homeScope, onPath, expanded, rootSet)
	}
}

func (g *dependsGraph) writeChild(b *strings.Builder, child, linePrefix, homeScope string, onPath, expanded, rootSet map[string]bool) {
	if onPath[child] {
		b.WriteString(linePrefix)
		b.WriteString(g.treeLabel(child, homeScope, true))
		b.WriteByte('\n')
		return
	}
	if g.unresolved(child) {
		b.WriteString(linePrefix)
		b.WriteString(g.treeLabel(child, homeScope, false))
		b.WriteByte('\n')
		return
	}
	if expanded[child] || rootSet[child] {
		b.WriteString(linePrefix)
		b.WriteString(g.treeLabel(child, homeScope, false))
		b.WriteByte('\n')
		return
	}
	g.writeSubtree(b, child, linePrefix, homeScope, onPath, expanded, rootSet)
}

func (g *dependsGraph) allLeafLike(children []string, onPath map[string]bool) bool {
	for _, ch := range children {
		if onPath[ch] {
			continue
		}
		if len(g.outDep[ch]) > 0 {
			return false
		}
	}
	return true
}

func (g *dependsGraph) formatLeafList(children []string, homeScope string, onPath map[string]bool) string {
	parts := make([]string, 0, len(children))
	for _, ch := range children {
		parts = append(parts, g.treeLabel(ch, homeScope, onPath[ch]))
	}
	return strings.Join(parts, ", ")
}

func (g *dependsGraph) unresolved(id string) bool {
	_, ok := g.byID[id]
	return !ok
}

func (g *dependsGraph) treeLabel(full, homeScope string, onPath bool) string {
	s := displayTicketID(full, homeScope)
	switch {
	case onPath:
		return s + " (cycle)"
	case g.unresolved(full):
		return s + " (unresolved)"
	default:
		return s
	}
}

func (g *dependsGraph) sortedChildren(node string) []string {
	children := append([]string(nil), g.outDep[node]...)
	sort.Strings(children)
	return children
}

func displayTicketID(full, homeScope string) string {
	if homeScope != "" && id.ScopeOfFullID(full) == homeScope {
		return strings.TrimPrefix(full, homeScope+"-")
	}
	return full
}

func visLen(s string) int {
	return utf8.RuneCountInString(s)
}

// forestRoots returns outbound-walk starts for a board-restricted depends
// graph: in-degree 0 from the board set, plus one start per cycle cluster
// that has no such entry (lexicographically smallest full id with outbound).
func forestRoots(boardIDs []string, outDep map[string][]string) []string {
	boardSet := make(map[string]bool, len(boardIDs))
	for _, id := range boardIDs {
		boardSet[id] = true
	}

	inDeg := map[string]int{}
	hasOut := map[string]bool{}
	undirected := map[string][]string{}
	addU := func(a, b string) {
		undirected[a] = appendUnique(undirected[a], b)
		undirected[b] = appendUnique(undirected[b], a)
	}

	for from, tos := range outDep {
		if !boardSet[from] || len(tos) == 0 {
			continue
		}
		hasOut[from] = true
		for _, to := range tos {
			if !boardSet[to] {
				continue
			}
			inDeg[to]++
			addU(from, to)
		}
	}

	seen := map[string]bool{}
	var roots []string
	sortedBoard := append([]string(nil), boardIDs...)
	sort.Strings(sortedBoard)
	for _, start := range sortedBoard {
		if !hasOut[start] && inDeg[start] == 0 {
			continue
		}
		if seen[start] {
			continue
		}
		var comp []string
		q := []string{start}
		seen[start] = true
		for len(q) > 0 {
			n := q[0]
			q = q[1:]
			if boardSet[n] {
				comp = append(comp, n)
			}
			for _, nb := range undirected[n] {
				if seen[nb] {
					continue
				}
				seen[nb] = true
				q = append(q, nb)
			}
		}
		var local []string
		for _, cid := range comp {
			if hasOut[cid] && inDeg[cid] == 0 {
				local = append(local, cid)
			}
		}
		if len(local) == 0 {
			sort.Strings(comp)
			for _, cid := range comp {
				if hasOut[cid] {
					local = []string{cid}
					break
				}
			}
		}
		roots = append(roots, local...)
	}
	sort.Strings(roots)
	return roots
}
