package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/index"
)

func newDepsCmd(app *App) *cobra.Command {
	var (
		scope      string
		transitive bool
		tree       bool
	)
	cmd := &cobra.Command{
		Use:     "deps <id> [--scope S] [--transitive] [--tree]",
		Aliases: []string{"depends", "dep"},
		Short:   "Show a ticket's edge neighbourhood (depends + related)",
		Long: "Print three sections — depends on, is depended on by, related (both\n" +
			"directions, non-gating) — each neighbour line carrying id, status, and a short\n" +
			"label, with (none) for empty sides. --transitive expands depends both ways as\n" +
			"a flat list; --tree pretty-prints the depends graph. Walks are cycle-safe and\n" +
			"warn once (pointing at doctor) on a cycle. Pure read; never runs git.\n" +
			"Aliases: depends, dep.",
		Args: exactArgs("<id>"),
		RunE: func(c *cobra.Command, args []string) error {
			return runDeps(app, c, args[0], scope, transitive, tree)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "ambient scope for a short id")
	cmd.Flags().BoolVar(&transitive, "transitive", false, "expand depends both ways as a flat list")
	cmd.Flags().BoolVar(&tree, "tree", false, "pretty-print the depends graph")
	return cmd
}

func runDeps(app *App, c *cobra.Command, idArg, scope string, transitive, tree bool) error {
	e, err := app.openEngine(c)
	if err != nil {
		return err
	}
	defer e.close()

	r, err := e.resolveTicket(c, idArg, scope)
	if err != nil {
		return err
	}
	if len(r.rows) > 1 {
		return duplicateRefusal(r.rows)
	}
	subject := r.rows[0].ID

	g, err := e.buildDepsGraph(subject, transitive, tree)
	if err != nil {
		return err
	}

	if g.subjectInCycle(subject) {
		stderrln(c, fmt.Sprintf("%s is in a depends cycle — run tk doctor for detail", subject))
	}

	switch {
	case tree:
		g.printTree(c, subject)
	case transitive:
		g.printSection(c, "depends on (transitive)", g.transitiveDepends(subject))
		g.printSection(c, "is depended on by (transitive)", g.transitiveDependedOnBy(subject))
		g.printSection(c, "related", g.relatedBoth(subject))
	default:
		g.printSection(c, "depends on", g.outDep[subject])
		g.printSection(c, "is depended on by", g.inDep[subject])
		g.printSection(c, "related", g.relatedBoth(subject))
	}
	return nil
}

type depsGraph struct {
	outDep map[string][]string
	inDep  map[string][]string
	outRel map[string][]string
	inRel  map[string][]string
	byID   map[string]*index.Ticket
}

func (e *engine) buildDepsGraph(subject string, transitive, tree bool) (*depsGraph, error) {
	g := &depsGraph{
		outDep: map[string][]string{}, inDep: map[string][]string{},
		outRel: map[string][]string{}, inRel: map[string][]string{},
		byID: map[string]*index.Ticket{},
	}
	from, err := e.db.EdgesFromID(subject)
	if err != nil {
		return nil, err
	}
	to, err := e.db.EdgesByTarget(subject)
	if err != nil {
		return nil, err
	}
	for _, ed := range from {
		g.addEdge(ed)
	}
	for _, ed := range to {
		g.addEdge(ed)
	}
	// Cycle and --tree walk outbound; a 3-cycle's close is hop-2, not inbound at hop 1.
	if err := e.expandOutboundDepends(g, subject, g.outDep[subject]); err != nil {
		return nil, err
	}
	if transitive && !tree {
		if err := e.expandInboundDepends(g, subject, g.inDep[subject]); err != nil {
			return nil, err
		}
	}
	tickets, err := e.db.TicketsByFullIDs(g.idsToPrint(subject, transitive, tree))
	if err != nil {
		return nil, err
	}
	for _, p := range tickets {
		if _, ok := g.byID[p.ID]; !ok {
			g.byID[p.ID] = p
		}
	}
	return g, nil
}

func (g *depsGraph) addEdge(ed index.Edge) {
	if ed.Kind == index.EdgeDepends {
		g.outDep[ed.FromID] = appendUnique(g.outDep[ed.FromID], ed.ToID)
		g.inDep[ed.ToID] = appendUnique(g.inDep[ed.ToID], ed.FromID)
		return
	}
	g.outRel[ed.FromID] = appendUnique(g.outRel[ed.FromID], ed.ToID)
	g.inRel[ed.ToID] = appendUnique(g.inRel[ed.ToID], ed.FromID)
}

func (e *engine) expandOutboundDepends(g *depsGraph, subject string, seeds []string) error {
	return e.expandDepends(g, subject, seeds, true)
}

func (e *engine) expandInboundDepends(g *depsGraph, subject string, seeds []string) error {
	return e.expandDepends(g, subject, seeds, false)
}

func (e *engine) expandDepends(g *depsGraph, subject string, seeds []string, outbound bool) error {
	visited := map[string]bool{subject: true}
	queue := make([]string, 0, len(seeds))
	for _, id := range seeds {
		if visited[id] {
			continue
		}
		visited[id] = true
		queue = append(queue, id)
	}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		var (
			edges []index.Edge
			err   error
		)
		if outbound {
			edges, err = e.db.EdgesFromID(id)
		} else {
			edges, err = e.db.EdgesByTarget(id)
		}
		if err != nil {
			return err
		}
		for _, ed := range edges {
			if ed.Kind != index.EdgeDepends {
				continue
			}
			g.addEdge(ed)
			next := ed.ToID
			if !outbound {
				next = ed.FromID
			}
			if visited[next] {
				continue
			}
			visited[next] = true
			queue = append(queue, next)
		}
	}
	return nil
}

func (g *depsGraph) idsToPrint(subject string, transitive, tree bool) []string {
	seen := map[string]bool{}
	var ids []string
	add := func(id string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		ids = append(ids, id)
	}
	switch {
	case tree:
		add(subject)
		for _, id := range g.transitiveDepends(subject) {
			add(id)
		}
	case transitive:
		for _, id := range g.transitiveDepends(subject) {
			add(id)
		}
		for _, id := range g.transitiveDependedOnBy(subject) {
			add(id)
		}
	default:
		for _, id := range g.outDep[subject] {
			add(id)
		}
		for _, id := range g.inDep[subject] {
			add(id)
		}
	}
	for _, id := range g.relatedBoth(subject) {
		add(id)
	}
	return ids
}

// printSection always emits a title and (none) for empty sides so section structure is stable.
func (g *depsGraph) printSection(c *cobra.Command, title string, ids []string) {
	stdoutln(c, title+":")
	if len(ids) == 0 {
		stdoutln(c, "  (none)")
		return
	}
	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)
	for _, id := range sorted {
		stdoutln(c, "  "+g.neighbourLine(id))
	}
}

// neighbourLine annotates unresolved targets rather than dropping them.
func (g *depsGraph) neighbourLine(id string) string {
	p, ok := g.byID[id]
	if !ok {
		return id + "\t(unresolved)"
	}
	label := p.Title
	if label == "" {
		label = p.Summary
	}
	status := p.Status
	if p.ParseError {
		status = "(parse_error)"
	}
	return id + "\t" + status + "\t" + label
}

func (g *depsGraph) relatedBoth(subject string) []string {
	var out []string
	for _, id := range g.outRel[subject] {
		out = appendUnique(out, id)
	}
	for _, id := range g.inRel[subject] {
		out = appendUnique(out, id)
	}
	return out
}

func (g *depsGraph) transitiveDepends(subject string) []string {
	return g.reachable(subject, g.outDep)
}

func (g *depsGraph) transitiveDependedOnBy(subject string) []string {
	return g.reachable(subject, g.inDep)
}

func (g *depsGraph) reachable(start string, adj map[string][]string) []string {
	visited := map[string]bool{start: true}
	var out []string
	var walk func(string)
	walk = func(node string) {
		for _, next := range adj[node] {
			if visited[next] {
				continue
			}
			visited[next] = true
			out = append(out, next)
			walk(next)
		}
	}
	walk(start)
	return out
}

func (g *depsGraph) subjectInCycle(subject string) bool {
	visited := map[string]bool{}
	var walk func(string) bool
	walk = func(node string) bool {
		for _, next := range g.outDep[node] {
			if next == subject {
				return true
			}
			if visited[next] {
				continue
			}
			visited[next] = true
			if walk(next) {
				return true
			}
		}
		return false
	}
	return walk(subject)
}

// printTree stops a branch on revisit so a cycle cannot expand forever.
func (g *depsGraph) printTree(c *cobra.Command, subject string) {
	stdoutln(c, "depends tree:")
	stdoutln(c, "  "+g.neighbourLine(subject))
	onPath := map[string]bool{subject: true}
	g.printTreeChildren(c, subject, 2, onPath)
	g.printSection(c, "related", g.relatedBoth(subject))
}

func (g *depsGraph) printTreeChildren(c *cobra.Command, node string, depth int, onPath map[string]bool) {
	children := append([]string(nil), g.outDep[node]...)
	sort.Strings(children)
	indent := strings.Repeat("  ", depth)
	for _, child := range children {
		if onPath[child] {
			stdoutln(c, indent+child+"\t(cycle)")
			continue
		}
		stdoutln(c, indent+g.neighbourLine(child))
		onPath[child] = true
		g.printTreeChildren(c, child, depth+1, onPath)
		delete(onPath, child)
	}
}

func appendUnique(list []string, v string) []string {
	for _, e := range list {
		if e == v {
			return list
		}
	}
	return append(list, v)
}
