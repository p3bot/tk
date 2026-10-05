package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/design"
	"github.com/p3bot/tk/internal/id"
	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/token"
)

func newDesignCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "design",
		Short: "Create and update scope design documents",
		Long: "Scope design documents live at <scope-dir>/design/<id>-<slug>.md.\n" +
			"They are not board items: tk list, next, and search ignore them. The index\n" +
			"stores each design as its own row. list and get read those rows. design search\n" +
			"queries the design index and does not join tk search.\n" +
			"Statuses are draft, accepted, decomposed, and superseded. The file stays in design/.\n" +
			"status is set with mark. produces (full ticket ids, design to tickets only) is set\n" +
			"with meta add and meta remove. A short id held by two design files is refused by\n" +
			"get, mark, and meta, with no path printed.\n" +
			"create does not self-commit. mark and meta add|remove self-commit on a tk-driven\n" +
			"scope and do not take the claim push path. Body text under the H1 is a direct file edit.",
		Args: cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			if len(args) > 0 {
				return usageErrorf("unknown design subcommand %q; run `tk design --help`", args[0])
			}
			return c.Help()
		},
	}
	cmd.AddCommand(
		newDesignCreateCmd(app),
		newDesignListCmd(app),
		newDesignGetCmd(app),
		newDesignSearchCmd(app),
		newDesignMarkCmd(app),
		newDesignMetaCmd(app),
	)
	return cmd
}

func newDesignCreateCmd(app *App) *cobra.Command {
	var scope string
	cmd := &cobra.Command{
		Use:   "create <title> [--scope S]",
		Short: "Scaffold a design document and print its path",
		Long: "Mint an id that no ticket or design in the scope already holds, write\n" +
			"design/<id>-<slug>.md with id, status draft, changed, and created, and\n" +
			"print the cleaned absolute path. changed and created are the same RFC3339\n" +
			"instant. The slug is frozen from the title. There is no order key. create\n" +
			"does not self-commit; a tk-driven scope rides sync_needed:.",
		Args: exactArgs("<title>"),
		RunE: func(c *cobra.Command, args []string) error {
			return runDesignCreate(app, c, args[0], scope)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "scope (defaults to ambient)")
	return cmd
}

func newDesignListCmd(app *App) *cobra.Command {
	var scope string
	var all bool
	cmd := &cobra.Command{
		Use:     "list [status...] [--scope S] [--all]",
		Aliases: []string{"ls"},
		Short:   "List design documents",
		Long: "Print headerless TSV: id, status, title, path. Reads the design index after\n" +
			"reconcile. Default rows are draft and accepted, sorted by created as an\n" +
			"RFC3339 instant, then id. A created value that is not RFC3339 sorts first.\n" +
			"--all includes every parsed design, including decomposed, superseded, and a\n" +
			"status outside those four names. Status positionals replace that filter. A\n" +
			"positional outside the four names exits 2. A missing design/ directory prints\n" +
			"nothing and exits 0.",
		Args: cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			return runDesignList(app, c, scope, all, args)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "scope (defaults to ambient)")
	cmd.Flags().BoolVar(&all, "all", false, "every parsed design, including a status outside the four names")
	return cmd
}

func newDesignGetCmd(app *App) *cobra.Command {
	var scope string
	var content bool
	cmd := &cobra.Command{
		Use:   "get <id> [--content] [--scope S]",
		Short: "Resolve a design id to its path or contents",
		Long: "Print the cleaned absolute path. Reads the design index after reconcile.\n" +
			"--content prints the file. A short id resolves in the ambient scope; a full\n" +
			"id resolves in any registered scope. An unparseable fence still prints the\n" +
			"path and exits 0. Two design files sharing the short id refuse and print no path.",
		Args: exactArgs("<id>"),
		RunE: func(c *cobra.Command, args []string) error {
			return runDesignGet(app, c, args[0], scope, content)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "ambient scope for a short id")
	cmd.Flags().BoolVar(&content, "content", false, "print the file instead of the path")
	return cmd
}

func newDesignSearchCmd(app *App) *cobra.Command {
	var scope string
	cmd := &cobra.Command{
		Use:   "search <terms> [--scope S]",
		Short: "Full-text search over design titles and bodies",
		Long: "Search design titles and bodies, machine-wide by default or bounded by\n" +
			"--scope. Results are ranked bm25 (best first), tie-broken by full id.\n" +
			"One TSV line per hit:\n" +
			"  <full-id>\\t<status>\\t<title>\\t<absolute-path>\n" +
			"A parse_error hit has an empty status but a filled path. No lens, no status\n" +
			"filter. Empty result exits 0. Does not join tk search. Pure read.",
		Args: anyArgs(),
		RunE: func(c *cobra.Command, args []string) error {
			return runDesignSearch(app, c, args, scope)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "bound the search to one scope")
	return cmd
}

func newDesignMarkCmd(app *App) *cobra.Command {
	var scope string
	cmd := &cobra.Command{
		Use:   "mark <status> <id> [--scope S]",
		Short: "Set a design's status",
		Long: "Set status to draft, accepted, decomposed, or superseded. Any of the four\n" +
			"may be marked from any of the four. The file stays in design/. changed is\n" +
			"rewritten only when the status changes. A same-status mark leaves the key\n" +
			"alone and does not add it. A ticket status is a usage error. On a tk-driven\n" +
			"scope, mark self-commits and does not push. A short id shared by two design\n" +
			"files refuses and does not write.",
		Args: exactArgs("<status>", "<id>"),
		RunE: func(c *cobra.Command, args []string) error {
			return runDesignMark(app, c, args[0], args[1], scope)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "ambient scope for a short id")
	return cmd
}

func newDesignMetaCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "meta",
		Short: "Record which tickets a design produced",
		Long: "meta add appends one full ticket id to produces. The id must resolve to\n" +
			"an existing ticket. meta remove drops every occurrence of one list entry,\n" +
			"including a value that is not a full ticket id. Removing the last entry\n" +
			"removes the key. A duplicate add, and a remove of an absent value, are\n" +
			"idempotent. There is no other design meta key.\n" +
			"These writes self-commit on a tk-driven scope. A short id shared by two\n" +
			"design files refuses and does not write.",
		Args: cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			if len(args) > 0 {
				return usageErrorf("unknown design meta subcommand %q; run `tk design meta --help`", args[0])
			}
			return c.Help()
		},
	}
	cmd.AddCommand(newDesignMetaAddCmd(app), newDesignMetaRemoveCmd(app))
	return cmd
}

func newDesignMetaAddCmd(app *App) *cobra.Command {
	return newDesignMetaMutCmd(app, true)
}

func newDesignMetaRemoveCmd(app *App) *cobra.Command {
	return newDesignMetaMutCmd(app, false)
}

func newDesignMetaMutCmd(app *App, add bool) *cobra.Command {
	var scope string
	verb := "remove"
	if add {
		verb = "add"
	}
	cmd := &cobra.Command{
		Use:   verb + " <id> produces <ticket-id> [--scope S]",
		Short: verb + " one produces entry",
		Args:  exactArgs("<id>", "produces", "<ticket-id>"),
		RunE: func(c *cobra.Command, args []string) error {
			if args[1] != design.KeyProduces {
				return usageErrorf("design meta key must be produces")
			}
			return runDesignMeta(app, c, args[0], args[2], scope, add)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "ambient scope for a short id")
	return cmd
}

func runDesignCreate(app *App, c *cobra.Command, title, scopeFlag string) error {
	e, err := app.openEngine(c)
	if err != nil {
		return err
	}
	defer e.close()
	resolved, err := e.resolveAmbient(scopeFlag)
	if err != nil {
		return err
	}
	res, err := design.Create(e.designDeps(c), design.CreateInput{
		Scope: resolved.Name,
		Dir:   resolved.Entry.Dir,
		Title: title,
	})
	return emitDesign(c, res, err)
}

func runDesignList(app *App, c *cobra.Command, scopeFlag string, all bool, statuses []string) error {
	e, err := app.openEngine(c)
	if err != nil {
		return err
	}
	defer e.close()
	resolved, err := e.resolveAmbient(scopeFlag)
	if err != nil {
		return err
	}
	res, err := design.List(e.designDeps(c), design.ListInput{
		Scope: resolved.Name, Dir: resolved.Entry.Dir, Statuses: statuses, All: all,
	})
	if err != nil {
		return mapDesignErr(err)
	}
	writeDesignParseCount(c, res.Unparseable)
	for _, row := range res.Rows {
		stdoutln(c, strings.Join([]string{row.ID, row.Status, row.Title, row.Path}, "\t"))
	}
	return nil
}

func runDesignSearch(app *App, c *cobra.Command, args []string, scope string) error {
	terms := strings.TrimSpace(strings.Join(args, " "))
	if terms == "" {
		return usageErrorf("design search needs at least one term")
	}
	e, err := app.openEngine(c)
	if err != nil {
		return err
	}
	defer e.close()

	targets := e.allTargets()
	if scope != "" {
		entry, ok := e.reg.Scopes[scope]
		if !ok {
			return fmt.Errorf("unknown scope %q", scope)
		}
		targets = map[string]string{scope: entry.Dir}
	}
	if _, err := e.reconcile(c, targets); err != nil {
		return err
	}
	hits, err := e.db.SearchDesigns(scope, terms)
	if err != nil {
		if errors.Is(err, index.ErrSearchQuery) {
			return fmt.Errorf("invalid search query %q: terms are FTS5 syntax — balance quotes and operators (e.g. \"exact phrase\", prefix*, a OR b)", terms)
		}
		return fmt.Errorf("design search: %w", err)
	}
	for _, h := range hits {
		p := h.Design
		abs, err := absPath(p.Path)
		if err != nil {
			return err
		}
		stdoutln(c, tsvLine(p.ID, p.Status, p.Title, abs))
	}
	return nil
}

func runDesignGet(app *App, c *cobra.Command, idArg, scopeFlag string, content bool) error {
	e, in, err := eDesignID(app, c, idArg, scopeFlag)
	if err != nil {
		return err
	}
	defer e.close()
	in.Content = content
	res, err := design.Get(e.designDeps(c), in)
	return emitDesign(c, res, err)
}

func runDesignMark(app *App, c *cobra.Command, status, idArg, scopeFlag string) error {
	e, in, err := eDesignID(app, c, idArg, scopeFlag)
	if err != nil {
		return err
	}
	defer e.close()
	res, err := design.Mark(e.designDeps(c), design.MarkInput{IDInput: in, Status: status})
	return emitDesign(c, res, err)
}

func runDesignMeta(app *App, c *cobra.Command, idArg, target, scopeFlag string, add bool) error {
	e, in, err := eDesignID(app, c, idArg, scopeFlag)
	if err != nil {
		return err
	}
	defer e.close()
	res, err := design.MetaAddRemove(e.designDeps(c), design.MetaInput{IDInput: in, Add: add, Target: target})
	return emitDesign(c, res, err)
}

func eDesignID(app *App, c *cobra.Command, idArg, scopeFlag string) (*engine, design.IDInput, error) {
	form, ok := id.ParseArg(idArg)
	if !ok {
		return nil, design.IDInput{}, usageErrorf("%q is not a valid design id", idArg)
	}
	e, err := app.openEngine(c)
	if err != nil {
		return nil, design.IDInput{}, err
	}
	scope, err := e.scopeForID(idArg, form, scopeFlag)
	if err != nil {
		e.close()
		return nil, design.IDInput{}, err
	}
	entry, registered := e.reg.Scopes[scope]
	if !registered {
		e.close()
		return nil, design.IDInput{}, fmt.Errorf("unknown design id %q: scope %q is not registered here", idArg, scope)
	}
	return e, design.IDInput{
		Scope: scope,
		Dir:   entry.Dir,
		Arg:   idArg,
		Full:  form == id.FormFull,
	}, nil
}

func writeDesignParseCount(c *cobra.Command, n int) {
	if n > 0 {
		stderrln(c, token.Line(token.ParseError, fmt.Sprintf("%d unparseable", n)))
	}
}

func emitDesign(c *cobra.Command, res design.Result, err error) error {
	writeDesignParseCount(c, res.Unparseable)
	if res.Parse != nil && err == nil {
		stderrln(c, res.Parse.ReadLine())
	}
	if err == nil {
		if res.Body != nil {
			if _, werr := c.OutOrStdout().Write(res.Body); werr != nil {
				return werr
			}
		} else if res.Path != "" {
			stdoutln(c, res.Path)
		}
		if res.SyncDisabled != "" {
			stderrln(c, token.Line(token.SyncDisabled, res.SyncDisabled))
		}
		if res.SyncNeeded != "" {
			stderrln(c, token.Line(token.SyncNeeded, res.SyncNeeded))
		}
		return nil
	}
	return mapDesignErr(err)
}

func mapDesignErr(err error) error {
	if err == nil {
		return nil
	}
	var use *design.UsageError
	if errors.As(err, &use) {
		return usageErrorf("%s", use.Msg)
	}
	var unk *design.UnknownStatusError
	if errors.As(err, &unk) {
		return usageErrorf("%s", unk.Error())
	}
	return err
}
