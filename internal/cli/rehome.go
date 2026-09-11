package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/id"
	"github.com/p3bot/tk/internal/writeengine"
)

func newRehomeCmd(app *App) *cobra.Command {
	var scope string
	cmd := &cobra.Command{
		Use:   "rehome <id> <dest-scope> [--scope S]",
		Short: "Move a ticket into another scope by rewriting its id prefix",
		Long: "Rewrite one ticket's scope prefix and move the file into dest. The short id\n" +
			"is kept when dest is free; a dest collision extends it (same as collision repair).\n" +
			"Depends/related that name this ticket are rewritten in the source and dest scopes;\n" +
			"other scopes ride edge_verify on stderr and are not rewritten. Stdout is the dest\n" +
			"path only. Dest order is a new append key unless a leftover dest file from an\n" +
			"interrupted rehome is reused. Auto-commit git-roots that contain a touched path\n" +
			"self-commit; rehome does not push. If this machine's current-ticket pointer (me)\n" +
			"names the source id, it is dropped rather than rewritten. Same-scope dest is a\n" +
			"usage error; an unknown dest scope is a generic failure. Dest must know the\n" +
			"ticket's status.",
		Args: exactArgs("<id>", "<dest-scope>"),
		RunE: func(c *cobra.Command, args []string) error {
			return runRehome(app, c, args[0], args[1], scope)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "ambient scope for a short id")
	return cmd
}

func runRehome(app *App, c *cobra.Command, idArg, destScope, scopeFlag string) error {
	e, err := app.openEngine(c)
	if err != nil {
		return err
	}
	defer e.close()

	form, ok := parseIDArg(idArg)
	if !ok {
		return usageErrorf("%q is not a valid ticket id", idArg)
	}
	srcScope, err := e.scopeForID(idArg, form, scopeFlag)
	if err != nil {
		return err
	}
	entry, registered := e.reg.Scopes[srcScope]
	if !registered {
		return fmt.Errorf("unknown ticket id %q: scope %q is not registered here", idArg, srcScope)
	}
	lookup, err := e.writeLookup(srcScope, idArg, form)
	if err != nil {
		return err
	}

	in := writeengine.RehomeInput{
		SourceScope: srcScope,
		SourceDir:   entry.Dir,
		DestScope:   destScope,
		Lookup:      lookup,
	}
	if destEntry, ok := e.reg.Scopes[destScope]; ok {
		in.DestDir = destEntry.Dir
	} else if id.IsScopeName(destScope) {
		return fmt.Errorf("unknown scope %q", destScope)
	}

	res, err := writeengine.Rehome(e.writeDeps(c.Context()), in)
	return emitWriteResult(c, res, err)
}
