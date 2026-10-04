package cli

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/design"
	"github.com/p3bot/tk/internal/id"
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
		Long: "Two modes. Without --tree, id is required and stdout is TSV. A ticket id\n" +
			"prints depends on, is depended on by, related (both directions, non-gating),\n" +
			"then produced by. Each neighbour line carries id, status, and a short label.\n" +
			"produced by carries design id, status, and title. (none) marks an empty side.\n" +
			"Several design files on one id print (ambiguous).\n" +
			"When that id is also one design, produces is appended. When several designs\n" +
			"share it, the ticket report stays and stderr carries design_id. A design id\n" +
			"with no ticket prints one section, produces, using the ticket neighbour lines,\n" +
			"and does not print depends or related. A short id held by two design files\n" +
			"and no ticket refuses and prints no path. --transitive expands depends both\n" +
			"ways and does not walk produces. --tree pretty-prints a box-drawing forest of\n" +
			"short ids (full id when a node is foreign) and does not include designs; not TSV.\n" +
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

	sub, err := e.resolveDependsSubject(c, idArg, scope, tree)
	if err != nil {
		return err
	}
	if len(sub.tickets) > 1 {
		return duplicateRefusal(sub.tickets)
	}
	if len(sub.tickets) == 1 {
		subject := sub.tickets[0].ID
		g, err := e.buildDependsGraph(subject, transitive, tree)
		if err != nil {
			return err
		}
		if g.subjectInCycle(subject) {
			stderrln(c, fmt.Sprintf("%s is in a depends cycle — run tk doctor for detail", subject))
		}
		switch {
		case tree:
			g.printSubtree(c, subject, sub.scope)
		case transitive:
			g.printSection(c, "depends on (transitive)", g.transitiveDepends(subject))
			g.printSection(c, "is depended on by (transitive)", g.transitiveDependedOnBy(subject))
			g.printSection(c, "related", g.relatedBoth(subject))
			g.printProducedBy(c, subject)
		default:
			g.printSection(c, "depends on", g.outDep[subject])
			g.printSection(c, "is depended on by", g.inDep[subject])
			g.printSection(c, "related", g.relatedBoth(subject))
			g.printProducedBy(c, subject)
		}
		return appendDesignProduces(e, c, sub.designs)
	}
	if len(sub.designs) > 1 {
		return sharedDesignRefusal(sub.designs)
	}
	return runDependsOnDesign(e, c, sub.designs[0])
}

type dependsSubject struct {
	scope   string
	tickets []*index.Ticket
	designs []*index.Design
}

// resolveDependsSubject reconciles once. One ticket keeps its report. A single
// design on that id contributes produces. With no ticket, the design is the
// subject. --tree stays ticket-only.
func (e *engine) resolveDependsSubject(c *cobra.Command, idArg, scopeFlag string, tree bool) (*dependsSubject, error) {
	form, ok := parseIDArg(idArg)
	if !ok {
		return nil, usageErrorf("%q is not a valid ticket id", idArg)
	}
	scope, err := e.scopeForID(idArg, form, scopeFlag)
	if err != nil {
		return nil, err
	}
	entry, registered := e.reg.Scopes[scope]
	if !registered {
		return nil, fmt.Errorf("unknown ticket id %q: scope %q is not registered here", idArg, scope)
	}
	res, err := e.reconcileResult(map[string]string{scope: entry.Dir})
	if err != nil {
		return nil, err
	}
	if res.Unreachable[scope] {
		e.printWarnings(c, res.Warnings)
		return nil, fmt.Errorf("cannot resolve %q: scope %q is not reachable", idArg, scope)
	}
	var tickets []*index.Ticket
	switch form {
	case id.FormFull:
		tickets, err = e.db.TicketsByID(scope, idArg)
	default:
		tickets, err = e.db.TicketsByShortID(scope, idArg)
	}
	if err != nil {
		e.printWarnings(c, res.Warnings)
		return nil, err
	}
	if len(tickets) > 0 {
		warnings := res.Warnings
		if len(tickets) > 1 {
			warnings = suppressDuplicateID(warnings, tickets[0].ID)
		}
		e.printWarnings(c, warnings)
		sub := &dependsSubject{scope: scope, tickets: tickets}
		if tree || len(tickets) > 1 {
			return sub, nil
		}
		designs, err := e.designsByForm(scope, idArg, form)
		if err != nil {
			return nil, err
		}
		sub.designs = designs
		return sub, nil
	}
	if tree {
		e.printWarnings(c, res.Warnings)
		return nil, fmt.Errorf("unknown ticket id %q", idArg)
	}
	designs, err := e.designsByForm(scope, idArg, form)
	if err != nil {
		e.printWarnings(c, res.Warnings)
		return nil, err
	}
	e.printWarnings(c, res.Warnings)
	if len(designs) == 0 {
		return nil, fmt.Errorf("unknown ticket id %q", idArg)
	}
	return &dependsSubject{scope: scope, designs: designs}, nil
}

func (e *engine) designsByForm(scope, arg string, form id.Form) ([]*index.Design, error) {
	if form == id.FormFull {
		return e.db.DesignsByID(scope, arg)
	}
	return e.db.DesignsByShortID(scope, arg)
}

// A produces list needs one design file. Several files on the ticket's id are
// design_id on stderr, and the ticket report stays.
func appendDesignProduces(e *engine, c *cobra.Command, designs []*index.Design) error {
	switch len(designs) {
	case 0:
		return nil
	case 1:
		return runDependsOnDesign(e, c, designs[0])
	default:
		err := sharedDesignRefusal(designs)
		var shared *design.SharedIDError
		if !errors.As(err, &shared) {
			return err
		}
		stderrln(c, err.Error())
		return nil
	}
}

func sharedDesignRefusal(rows []*index.Design) error {
	paths := make([]string, len(rows))
	for i, r := range rows {
		abs, err := absPath(r.Path)
		if err != nil {
			return err
		}
		paths[i] = abs
	}
	sort.Strings(paths)
	return &design.SharedIDError{ID: rows[0].ID, Paths: paths}
}

func runDependsOnDesign(e *engine, c *cobra.Command, d *index.Design) error {
	edges, err := e.db.EdgesFromID(d.ID)
	if err != nil {
		return err
	}
	g := newDependsGraph()
	var ids []string
	for _, ed := range edges {
		if ed.Kind != index.EdgeProduces {
			continue
		}
		ids = appendUnique(ids, ed.ToID)
	}
	tickets, err := e.db.TicketsByFullIDs(ids)
	if err != nil {
		return err
	}
	for _, p := range tickets {
		if _, ok := g.byID[p.ID]; !ok {
			g.byID[p.ID] = p
		}
	}
	g.printSection(c, "produces", ids)
	return nil
}

type dependsGraph struct {
	outDep      map[string][]string
	inDep       map[string][]string
	outRel      map[string][]string
	inRel       map[string][]string
	producers   map[string][]string
	byID        map[string]*index.Ticket
	designsByID map[string][]*index.Design
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
		if ed.Kind == index.EdgeProduces {
			g.producers[subject] = appendUnique(g.producers[subject], ed.FromID)
			continue
		}
		g.addEdge(ed)
	}
	if err := e.loadProducerDesigns(g, g.producers[subject]); err != nil {
		return nil, err
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
		producers:   map[string][]string{},
		byID:        map[string]*index.Ticket{},
		designsByID: map[string][]*index.Design{},
	}
}

func (e *engine) loadProducerDesigns(g *dependsGraph, ids []string) error {
	found, err := e.db.DesignsByFullIDs(ids)
	if err != nil {
		return err
	}
	for _, p := range found {
		g.designsByID[p.ID] = append(g.designsByID[p.ID], p)
	}
	return nil
}

func (g *dependsGraph) addEdge(ed index.Edge) {
	if ed.Kind == index.EdgeProduces {
		return
	}
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

func (g *dependsGraph) printProducedBy(c *cobra.Command, subject string) {
	stdoutln(c, "produced by:")
	ids := g.producers[subject]
	if len(ids) == 0 {
		stdoutln(c, "  (none)")
		return
	}
	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)
	for _, id := range sorted {
		stdoutln(c, "  "+g.designLine(id))
	}
}

// designLine labels a producer. Several rows can share the id and disagree on
// status and title, so the line names the id and does not pick one file.
func (g *dependsGraph) designLine(id string) string {
	rows := g.designsByID[id]
	switch len(rows) {
	case 0:
		return id + "\t(unresolved)"
	case 1:
		p := rows[0]
		return id + "\t" + p.Status + "\t" + p.Title
	default:
		return id + "\t(ambiguous)"
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
