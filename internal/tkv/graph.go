package tkv

import (
	"sort"

	"github.com/p3bot/tk/internal/id"
	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/status"
)

const (
	nodeW = 220
	nodeH = 56
	gapX  = 72
	gapY  = 18
	padX  = 28
	padY  = 28
)

type graphNode struct {
	ID         string
	ShortID    string
	Title      string
	Status     string
	Scope      string
	Href       string
	OrderKey   string
	Unresolved bool
	External   bool
}

type layoutNode struct {
	graphNode
	X, Y int
	Rank int
}

type layoutEdge struct {
	From, To       string
	X1, Y1, X2, Y2 int
	Cycle          bool
}

type depLayout struct {
	Nodes         []layoutNode
	Edges         []layoutEdge
	Width, Height int
}

func buildDependsGraph(scope string, all bool, custom map[string]status.Category, tickets []*index.Ticket, outEdges, inEdges []index.Edge) depLayout {
	byID := map[string]*index.Ticket{}
	for _, t := range tickets {
		if t == nil || t.ParseError {
			continue
		}
		if _, ok := byID[t.ID]; !ok {
			byID[t.ID] = t
		}
	}

	focus := map[string]bool{}
	for _, t := range byID {
		if t.Scope != scope {
			continue
		}
		if !all && (t.Archived || !status.InDefaultList(t.Status, custom)) {
			continue
		}
		focus[t.ID] = true
	}

	deps := map[string][]string{}
	seenEdge := map[string]bool{}
	needed := map[string]bool{}
	addEdge := func(from, to string) {
		if from == "" || to == "" || from == to {
			return
		}
		key := from + "\x00" + to
		if seenEdge[key] {
			return
		}
		seenEdge[key] = true
		deps[from] = append(deps[from], to)
		needed[from] = true
		needed[to] = true
	}
	for _, e := range outEdges {
		if e.Kind != index.EdgeDepends {
			continue
		}
		if focus[e.FromID] || focus[e.ToID] {
			addEdge(e.FromID, e.ToID)
		}
	}
	for _, e := range inEdges {
		if e.Kind != index.EdgeDepends {
			continue
		}
		if focus[e.ToID] || focus[e.FromID] {
			addEdge(e.FromID, e.ToID)
		}
	}
	if len(needed) == 0 {
		return depLayout{}
	}

	ids := make([]string, 0, len(needed))
	for id := range needed {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	nodes := map[string]graphNode{}
	for _, full := range ids {
		n := graphNode{ID: full, ShortID: full, Title: full, Scope: scopeOfFullID(full)}
		if t := byID[full]; t != nil {
			n.ShortID = t.ShortID
			n.Title = t.Title
			n.Status = t.Status
			n.Scope = t.Scope
			n.OrderKey = t.OrderKey
			n.Href = inspectHref(t.ID)
			n.External = t.Scope != scope
		} else if id.IsFullTicketID(full) {
			n.Unresolved = true
			n.External = n.Scope != scope
			n.Href = inspectHref(full)
		} else {
			n.Unresolved = true
		}
		nodes[full] = n
	}

	ranks := assignRanks(ids, deps)
	maxRank := 0
	for _, r := range ranks {
		if r > maxRank {
			maxRank = r
		}
	}
	byRank := make([][]string, maxRank+1)
	for _, full := range ids {
		r := ranks[full]
		byRank[r] = append(byRank[r], full)
	}
	for r := range byRank {
		sort.Slice(byRank[r], func(i, j int) bool {
			return nodeLess(byRank[r][i], byRank[r][j], nodes)
		})
	}
	rev := reverseAdj(deps)
	orderInRank(byRank, deps, rev, nodes)

	maxRows := 0
	for _, col := range byRank {
		if len(col) > maxRows {
			maxRows = len(col)
		}
	}
	if maxRows == 0 {
		return depLayout{}
	}

	pos := map[string]layoutNode{}
	outNodes := make([]layoutNode, 0, len(ids))
	for r, col := range byRank {
		for row, full := range col {
			ln := layoutNode{
				graphNode: nodes[full],
				X:         padX + r*(nodeW+gapX),
				Y:         padY + row*(nodeH+gapY),
				Rank:      r,
			}
			pos[full] = ln
			outNodes = append(outNodes, ln)
		}
	}

	var outEdgesLayout []layoutEdge
	for from, tos := range deps {
		a, okA := pos[from]
		if !okA {
			continue
		}
		for _, to := range tos {
			b, okB := pos[to]
			if !okB {
				continue
			}
			outEdgesLayout = append(outEdgesLayout, layoutEdge{
				From:  from,
				To:    to,
				X1:    a.X,
				Y1:    a.Y + nodeH/2,
				X2:    b.X + nodeW,
				Y2:    b.Y + nodeH/2,
				Cycle: ranks[from] <= ranks[to],
			})
		}
	}
	sort.Slice(outEdgesLayout, func(i, j int) bool {
		if outEdgesLayout[i].From != outEdgesLayout[j].From {
			return outEdgesLayout[i].From < outEdgesLayout[j].From
		}
		return outEdgesLayout[i].To < outEdgesLayout[j].To
	})

	w := padX*2 + (maxRank+1)*nodeW + maxRank*gapX
	h := padY*2 + maxRows*nodeH + (maxRows-1)*gapY
	if h < padY*2+nodeH {
		h = padY*2 + nodeH
	}
	return depLayout{Nodes: outNodes, Edges: outEdgesLayout, Width: w, Height: h}
}

func assignRanks(ids []string, deps map[string][]string) map[string]int {
	present := map[string]bool{}
	indeg := map[string]int{}
	rev := map[string][]string{}
	for _, id := range ids {
		present[id] = true
		indeg[id] = 0
	}
	for from, tos := range deps {
		if !present[from] {
			continue
		}
		seen := map[string]bool{}
		for _, to := range tos {
			if !present[to] || seen[to] {
				continue
			}
			seen[to] = true
			indeg[from]++
			rev[to] = append(rev[to], from)
		}
	}
	var q []string
	for _, id := range ids {
		if indeg[id] == 0 {
			q = append(q, id)
		}
	}
	sort.Strings(q)
	rank := map[string]int{}
	layer := 0
	for len(q) > 0 {
		var next []string
		for _, id := range q {
			rank[id] = layer
			for _, dep := range rev[id] {
				indeg[dep]--
				if indeg[dep] == 0 {
					next = append(next, dep)
				}
			}
		}
		sort.Strings(next)
		q = next
		layer++
	}
	var rest []string
	for _, id := range ids {
		if _, ok := rank[id]; !ok {
			rest = append(rest, id)
		}
	}
	sort.Strings(rest)
	for _, id := range rest {
		rank[id] = layer
	}
	return rank
}

func reverseAdj(deps map[string][]string) map[string][]string {
	rev := map[string][]string{}
	for from, tos := range deps {
		for _, to := range tos {
			rev[to] = append(rev[to], from)
		}
	}
	return rev
}

func orderInRank(byRank [][]string, deps, rev map[string][]string, nodes map[string]graphNode) {
	pos := map[string]int{}
	reindex := func() {
		for _, col := range byRank {
			for i, id := range col {
				pos[id] = i
			}
		}
	}
	reindex()
	for range 3 {
		for r := 1; r < len(byRank); r++ {
			byRank[r] = sortByBarycenter(byRank[r], deps, pos, nodes)
			reindex()
		}
		for r := len(byRank) - 2; r >= 0; r-- {
			byRank[r] = sortByBarycenter(byRank[r], rev, pos, nodes)
			reindex()
		}
	}
}

func sortByBarycenter(col []string, neigh map[string][]string, pos map[string]int, nodes map[string]graphNode) []string {
	type item struct {
		id   string
		bary float64
		has  bool
	}
	items := make([]item, len(col))
	for i, id := range col {
		b, ok := avgPos(neigh[id], pos)
		items[i] = item{id: id, bary: b, has: ok}
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].has && items[j].has && items[i].bary != items[j].bary {
			return items[i].bary < items[j].bary
		}
		if items[i].has != items[j].has {
			return items[i].has
		}
		return nodeLess(items[i].id, items[j].id, nodes)
	})
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.id
	}
	return out
}

func avgPos(ids []string, pos map[string]int) (float64, bool) {
	n := 0
	sum := 0
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		p, ok := pos[id]
		if !ok {
			continue
		}
		sum += p
		n++
	}
	if n == 0 {
		return 0, false
	}
	return float64(sum) / float64(n), true
}

func nodeLess(a, b string, nodes map[string]graphNode) bool {
	na, nb := nodes[a], nodes[b]
	if na.OrderKey != nb.OrderKey {
		return na.OrderKey < nb.OrderKey
	}
	return a < b
}

func countIsolated(scope string, all bool, custom map[string]status.Category, tickets []*index.Ticket, layout depLayout) int {
	inGraph := map[string]bool{}
	for _, n := range layout.Nodes {
		inGraph[n.ID] = true
	}
	n := 0
	seen := map[string]bool{}
	for _, t := range tickets {
		if t == nil || t.ParseError || t.Scope != scope || seen[t.ID] {
			continue
		}
		seen[t.ID] = true
		if !all && (t.Archived || !status.InDefaultList(t.Status, custom)) {
			continue
		}
		if !inGraph[t.ID] {
			n++
		}
	}
	return n
}
