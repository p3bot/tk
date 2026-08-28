package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/order"
)

type orderDest struct {
	before string
	after  string
	first  bool
	last   bool
}

func (d orderDest) count() int {
	n := 0
	if d.before != "" {
		n++
	}
	if d.after != "" {
		n++
	}
	if d.first {
		n++
	}
	if d.last {
		n++
	}
	return n
}

func newOrderCmd(app *App) *cobra.Command {
	var (
		scope string
		dest  orderDest
	)
	cmd := &cobra.Command{
		Use:   "order <id> (--before <id> | --after <id> | --first | --last) [--scope S]",
		Short: "Rewrite a ticket's order key to move it in the board",
		Long: "Move a ticket by writing a new order key strictly between its target\n" +
			"neighbours — a single-file write that never renumbers a band. --first/--last\n" +
			"place against the scope-wide minimum/maximum valid order (every status, dir root\n" +
			"and archive/); --before/--after name an in-scope neighbour that must exist and\n" +
			"carry a valid order. Exactly one destination is required. An auto-commit scope\n" +
			"self-commits the change when a git-root exists.",
		Args: exactArgs("<id>"),
		RunE: func(c *cobra.Command, args []string) error {
			return runOrder(app, c, args[0], dest, scope)
		},
	}
	cmd.Flags().StringVar(&dest.before, "before", "", "place immediately before this neighbour id")
	cmd.Flags().StringVar(&dest.after, "after", "", "place immediately after this neighbour id")
	cmd.Flags().BoolVar(&dest.first, "first", false, "place at the scope-wide front")
	cmd.Flags().BoolVar(&dest.last, "last", false, "place at the scope-wide back")
	cmd.Flags().StringVar(&scope, "scope", "", "ambient scope for a short id")
	return cmd
}

func runOrder(app *App, c *cobra.Command, idArg string, dest orderDest, scopeFlag string) error {
	if dest.count() != 1 {
		return usageErrorf("order needs exactly one of --before, --after, --first, --last")
	}
	form, ok := parseIDArg(idArg)
	if !ok {
		return usageErrorf("%q is not a valid ticket id", idArg)
	}

	e, err := app.openEngine(c)
	if err != nil {
		return err
	}
	defer e.close()

	scope, err := e.scopeForID(idArg, form, scopeFlag)
	if err != nil {
		return err
	}
	entry, registered := e.reg.Scopes[scope]
	if !registered {
		return fmt.Errorf("unknown ticket id %q: scope %q is not registered here", idArg, scope)
	}
	dir := entry.Dir

	sess, err := e.beginWrite(c, scope, dir)
	if err != nil {
		return err
	}
	defer sess.Release()

	subject, err := e.resolveWriteRow(scope, idArg, form)
	if err != nil {
		return err
	}

	rows, err := e.db.ScopeTickets(scope)
	if err != nil {
		return err
	}
	left, right, err := e.orderBounds(scope, subject, rows, dest)
	if err != nil {
		return err
	}
	newKey, err := order.KeyBetween(left, right)
	if err != nil {
		return fmt.Errorf("no legal order between neighbours for %s (%w) — re-space with tk doctor", subject.ID, err)
	}

	m, body, err := readTicketFile(subject.Path)
	if err != nil {
		return err
	}
	m.Order = newKey
	if err := writeTicketFile(subject.Path, m, body); err != nil {
		return err
	}
	if err := e.rec.SyncPaths(scope, writtenPaths(subject.Path, "")); err != nil {
		return err
	}

	message := fmt.Sprintf("tk: %s order", subject.ID)
	if err := e.completeStateDurability(c.Context(), c, sess.Scope, sess.Dir, sess.AutoCommit, message, subject.Path, "", sess.Root, sess.HasRoot); err != nil {
		return err
	}

	out, err := absPath(subject.Path)
	if err != nil {
		return err
	}
	stdoutln(c, out)
	return nil
}

// open bound is ""; subject is excluded from the ordered set.
func (e *engine) orderBounds(scope string, subject *index.Ticket, rows []*index.Ticket, dest orderDest) (left, right string, err error) {
	others := make([]*index.Ticket, 0, len(rows))
	for _, p := range rows {
		if p.Path == subject.Path || p.ParseError || !order.Valid(p.OrderKey) {
			continue
		}
		others = append(others, p)
	}
	index.SortTickets(others)

	switch {
	case dest.first:
		if len(others) == 0 {
			return "", "", nil
		}
		return "", others[0].OrderKey, nil
	case dest.last:
		if len(others) == 0 {
			return "", "", nil
		}
		return others[len(others)-1].OrderKey, "", nil
	case dest.before != "":
		return e.neighbourBounds(scope, subject, others, dest.before, true)
	default:
		return e.neighbourBounds(scope, subject, others, dest.after, false)
	}
}

func (e *engine) neighbourBounds(scope string, subject *index.Ticket, others []*index.Ticket, neighbourArg string, before bool) (left, right string, err error) {
	form, ok := parseIDArg(neighbourArg)
	if !ok {
		return "", "", usageErrorf("%q is not a valid ticket id", neighbourArg)
	}
	neighbour, err := e.resolveSingleRow(scope, neighbourArg, form, "neighbour")
	if err != nil {
		return "", "", err
	}
	if neighbour.Path == subject.Path {
		return "", "", usageErrorf("cannot order %q relative to itself", subject.ID)
	}
	if neighbour.ParseError || !order.Valid(neighbour.OrderKey) {
		return "", "", fmt.Errorf("neighbour %q has no valid order", neighbourArg)
	}

	idx := -1
	for i, p := range others {
		if p.Path == neighbour.Path {
			idx = i
			break
		}
	}
	if idx < 0 {
		return "", "", fmt.Errorf("neighbour %q not found in scope order", neighbourArg)
	}
	if before {
		l := ""
		if idx > 0 {
			l = others[idx-1].OrderKey
		}
		return l, neighbour.OrderKey, nil
	}
	r := ""
	if idx < len(others)-1 {
		r = others[idx+1].OrderKey
	}
	return neighbour.OrderKey, r, nil
}
