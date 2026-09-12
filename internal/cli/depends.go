package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/status"
)

func newDependsCmd(app *App) *cobra.Command {
	var (
		scope      string
		transitive bool
		tree       bool
		noLens     bool
	)
	cmd := &cobra.Command{
		Use:     "depends [<id>] [--scope S] [--transitive] [--tree] [--no-lens]",
		Aliases: []string{"deps", "dep"},
		Short:   "TSV neighbourhood, or --tree forest (id optional)",
		Long: "Two modes. Without --tree, id is required and stdout is TSV: three sections —\n" +
			"depends on, is depended on by, related (both directions, non-gating) — each\n" +
			"neighbour line carrying id, status, and a short label, with (none) for empty\n" +
			"sides. --transitive expands depends both ways as a flat list. --tree pretty-prints\n" +
			"a box-drawing forest of short ids (full id when a node is foreign); not TSV.\n" +
			"With --tree and no id, print the scope forest: roots are the default board\n" +
			"(lens unless --no-lens) tickets that have outbound depends and no inbound depends\n" +
			"from that board; a cycle cluster with no entry starts at the lexicographically\n" +
			"smallest full id with outbound. Isolated tickets are omitted. Related is printed\n" +
			"only when an id is given. Walks are cycle-safe and warn (pointing at doctor) on a\n" +
			"cycle. Pure read; never runs git. Forest membership is the default board, not\n" +
			"--all/--open.",
		Args: dependsArgs,
		RunE: func(c *cobra.Command, args []string) error {
			idArg := ""
			if len(args) > 0 {
				idArg = args[0]
			}
			return runDepends(app, c, idArg, scope, transitive, tree, noLens)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "ambient scope for a short id")
	cmd.Flags().BoolVar(&transitive, "transitive", false, "expand depends both ways as a flat list")
	cmd.Flags().BoolVar(&tree, "tree", false, "pretty-print the depends forest (id optional)")
	cmd.Flags().BoolVar(&noLens, "no-lens", false, "with --tree and no id: ignore the active lens")
	return cmd
}

func dependsArgs(c *cobra.Command, args []string) error {
	tree, err := c.Flags().GetBool("tree")
	if err != nil {
		return err
	}
	if tree {
		return rangeArgs(0, 1, "<id>")(c, args)
	}
	// Without --tree the id is required; usage on this path must not show [<id>].
	n := len(args)
	if n == 1 {
		return nil
	}
	usage := strings.Replace(commandUsageLine(c), "[<id>]", "<id>", 1)
	if n < 1 {
		return usageErrorf("missing <id>\nusage: %s", usage)
	}
	return usageErrorf("too many arguments\nusage: %s", usage)
}

func runDepends(app *App, c *cobra.Command, idArg, scope string, transitive, tree, noLens bool) error {
	e, err := app.openEngine(c)
	if err != nil {
		return err
	}
	defer e.close()

	if tree && idArg == "" {
		return runDependsForest(e, c, scope, noLens)
	}

	r, err := e.resolveTicket(c, idArg, scope)
	if err != nil {
		return err
	}
	if len(r.rows) > 1 {
		return duplicateRefusal(r.rows)
	}
	subject := r.rows[0].ID

	g, err := e.buildDependsGraph(subject, transitive, tree)
	if err != nil {
		return err
	}

	if g.subjectInCycle(subject) {
		stderrln(c, fmt.Sprintf("%s is in a depends cycle — run tk doctor for detail", subject))
	}

	switch {
	case tree:
		g.printSubtree(c, subject, r.scope)
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

type dependsGraph struct {
	outDep map[string][]string
	inDep  map[string][]string
	outRel map[string][]string
	inRel  map[string][]string
	byID   map[string]*index.Ticket
}

func (e *engine) buildDependsGraph(subject string, transitive, tree bool) (*dependsGraph, error) {
	g := newDependsGraph()
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

func runDependsForest(e *engine, c *cobra.Command, scopeFlag string, noLens bool) error {
	resolved, err := e.resolveAmbient(scopeFlag)
	if err != nil {
		return err
	}
	scope := resolved.Name
	res, err := e.reconcile(c, map[string]string{scope: resolved.Entry.Dir})
	if err != nil {
		return err
	}
	schema := res.Schema(scope)
	lens := e.reg.Lens[scope]
	applyLens := !noLens && len(lens) > 0
	filter := index.BoardFilter{Scope: scope, DefaultStatuses: status.DefaultListNames(schema.CustomStatuses())}
	if applyLens {
		filter.Lens = lens
	}
	board, err := e.db.BoardTickets(filter)
	if err != nil {
		return err
	}
	boardIDs := make([]string, 0, len(board))
	boardSet := make(map[string]bool, len(board))
	for _, p := range board {
		boardIDs = append(boardIDs, p.ID)
		boardSet[p.ID] = true
	}

	g, err := e.buildForestGraph(scope, boardSet)
	if err != nil {
		return err
	}
	roots := forestRoots(boardIDs, g.outDep)
	for _, root := range roots {
		if g.subjectInCycle(root) {
			stderrln(c, fmt.Sprintf("%s is in a depends cycle — run tk doctor for detail", root))
		}
	}
	g.printForest(c, roots, scope)
	if applyLens {
		stderrln(c, lensEcho(lens))
	}
	return nil
}

func (e *engine) buildForestGraph(scope string, boardSet map[string]bool) (*dependsGraph, error) {
	g := newDependsGraph()
	edges, err := e.db.DependsFromScopes([]string{scope})
	if err != nil {
		return nil, err
	}
	for _, ed := range edges {
		if boardSet[ed.FromID] {
			g.addEdge(ed)
		}
	}
	var boardIDs []string
	for id := range boardSet {
		boardIDs = append(boardIDs, id)
	}
	for _, root := range forestRoots(boardIDs, g.outDep) {
		if err := e.expandOutboundDepends(g, root, g.outDep[root]); err != nil {
			return nil, err
		}
	}
	tickets, err := e.db.TicketsByFullIDs(g.dependsNodeIDs())
	if err != nil {
		return nil, err
	}
	for _, p := range tickets {
		g.byID[p.ID] = p
	}
	return g, nil
}

func (g *dependsGraph) dependsNodeIDs() []string {
	seen := map[string]bool{}
	var ids []string
	add := func(id string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		ids = append(ids, id)
	}
	for from, tos := range g.outDep {
		add(from)
		for _, to := range tos {
			add(to)
		}
	}
	return ids
}

func newDependsGraph() *dependsGraph {
	return &dependsGraph{
		outDep: map[string][]string{}, inDep: map[string][]string{},
		outRel: map[string][]string{}, inRel: map[string][]string{},
		byID: map[string]*index.Ticket{},
	}
}

func (g *dependsGraph) addEdge(ed index.Edge) {
	if ed.Kind == index.EdgeDepends {
		g.outDep[ed.FromID] = appendUnique(g.outDep[ed.FromID], ed.ToID)
		g.inDep[ed.ToID] = appendUnique(g.inDep[ed.ToID], ed.FromID)
		return
	}
	g.outRel[ed.FromID] = appendUnique(g.outRel[ed.FromID], ed.ToID)
	g.inRel[ed.ToID] = appendUnique(g.inRel[ed.ToID], ed.FromID)
}

func (e *engine) expandOutboundDepends(g *dependsGraph, subject string, seeds []string) error {
	return e.expandDepends(g, subject, seeds, true)
}

func (e *engine) expandInboundDepends(g *dependsGraph, subject string, seeds []string) error {
	return e.expandDepends(g, subject, seeds, false)
}

func (e *engine) expandDepends(g *dependsGraph, subject string, seeds []string, outbound bool) error {
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

func (g *dependsGraph) idsToPrint(subject string, transitive, tree bool) []string {
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
func (g *dependsGraph) printSection(c *cobra.Command, title string, ids []string) {
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
func (g *dependsGraph) neighbourLine(id string) string {
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

func (g *dependsGraph) relatedBoth(subject string) []string {
	var out []string
	for _, id := range g.outRel[subject] {
		out = appendUnique(out, id)
	}
	for _, id := range g.inRel[subject] {
		out = appendUnique(out, id)
	}
	return out
}

func (g *dependsGraph) transitiveDepends(subject string) []string {
	return g.reachable(subject, g.outDep)
}

func (g *dependsGraph) transitiveDependedOnBy(subject string) []string {
	return g.reachable(subject, g.inDep)
}

func (g *dependsGraph) reachable(start string, adj map[string][]string) []string {
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

func (g *dependsGraph) subjectInCycle(subject string) bool {
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

func (g *dependsGraph) printSubtree(c *cobra.Command, subject, homeScope string) {
	g.printForest(c, []string{subject}, homeScope)
	g.printSection(c, "related", g.relatedBoth(subject))
}

func (g *dependsGraph) printForest(c *cobra.Command, roots []string, homeScope string) {
	s := g.formatForest(roots, homeScope)
	if s == "" {
		return
	}
	fmt.Fprint(c.OutOrStdout(), s)
}

func appendUnique(list []string, v string) []string {
	for _, e := range list {
		if e == v {
			return list
		}
	}
	return append(list, v)
}
