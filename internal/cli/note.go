package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/notes"
	"github.com/p3bot/tk/internal/token"
)

func newNoteCmd(app *App) *cobra.Command {
	var scope, name string
	cmd := &cobra.Command{
		Use:     "note [slug]",
		Aliases: []string{"notes"},
		Short:   "Read and write committed scope notes",
		Long: "Scope worklog documents at <scope-dir>/notes/<slug>.md. Bare `tk note` prints\n" +
			"this machine's default note. A missing default is empty stdout, exit 0 (same as\n" +
			"an empty file). A slug / --name prints that file; a missing named file is\n" +
			"non-zero with the path on stderr and empty stdout. `list` prints addressable\n" +
			"slugs, one per line, alphabetical.\n" +
			"`add` appends one line; `set` replaces the file (`-` reads stdin); `edit` opens\n" +
			"$EDITOR; `remove` unlinks the default (`--name` is one-shot). `use` sets this\n" +
			"machine's default slug. Omit --name and a positional slug to use that\n" +
			"machine-local default (built-in `default` when unset). --name and a positional\n" +
			"slug are one-shot selectors and never write the stored default.\n" +
			"Personal slugs (`grant`, `alice`) with `default` as the shared pad are a\n" +
			"convention, not a CLI rule.\n" +
			"\n" +
			"Writes never self-commit. On a tk-driven scope, add, set, and remove ride\n" +
			"sync_needed: when the allowlist is dirty (same as create); edit does not.\n" +
			"`use` is XDG-only and never emits sync_needed:. Durability is `tk sync` on a\n" +
			"tk-driven scope, or a host commit on a repo-driven scope. Notes are not\n" +
			"tickets: they are not indexed, not listed by `tk list`, and not taught in\n" +
			"`tk skill`.",
		Args: maxArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return runNoteCat(app, c, args, scope, name, c.Flags().Changed("name"))
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "scope (defaults to ambient; wins over ambient)")
	cmd.Flags().StringVar(&name, "name", "", "note slug (one-shot; defaults to this machine's default)")
	cmd.AddCommand(
		newNoteListCmd(app),
		newNoteAddCmd(app),
		newNoteSetCmd(app),
		newNoteEditCmd(app),
		newNoteRemoveCmd(app),
		newNoteUseCmd(app),
	)
	return cmd
}

func newNoteListCmd(app *App) *cobra.Command {
	var scope string
	cmd := &cobra.Command{
		Use:     "list [--scope S]",
		Aliases: []string{"ls"},
		Short:   "List addressable note slugs",
		Long: "Print addressable note slugs under notes/, one per line, alphabetical.\n" +
			"Reserved verb names, invalid slugs, and nested paths are omitted (doctor owns\n" +
			"that residue). A missing notes/ directory is empty stdout, exit 0.",
		Args: noArgs(),
		RunE: func(c *cobra.Command, _ []string) error {
			return runNoteList(app, c, scope)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "scope (defaults to ambient; wins over ambient)")
	return cmd
}

func newNoteAddCmd(app *App) *cobra.Command {
	var scope, name string
	cmd := &cobra.Command{
		Use:   "add [--name slug] <text...>",
		Short: "Append one line to a note",
		Long: "Join remaining arguments with spaces and append that as one line. Creates\n" +
			"notes/ and the file if needed. If the file exists and does not end in a\n" +
			"newline, a newline is written first so lines do not glue. No text is usage\n" +
			"and does not create the file. Prints the cleaned absolute path. Never\n" +
			"self-commits; a tk-driven scope may ride sync_needed: dirty. Durability is\n" +
			"tk sync (tk-driven) or a host commit (repo-driven).",
		Args: minArgs("<text...>"),
		RunE: func(c *cobra.Command, args []string) error {
			return runNoteAdd(app, c, args, scope, name, c.Flags().Changed("name"))
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "scope (defaults to ambient; wins over ambient)")
	cmd.Flags().StringVar(&name, "name", "", "note slug (one-shot; defaults to this machine's default)")
	return cmd
}

func newNoteSetCmd(app *App) *cobra.Command {
	var scope, name string
	cmd := &cobra.Command{
		Use:   "set [--name slug] <text...> | -",
		Short: "Replace a note's contents",
		Long: "Replace the whole file with the joined arguments (one line) or, when `-` is\n" +
			"the sole text operand, stdin. The file always ends with a newline. No text,\n" +
			"an empty string, or empty stdin is usage (use remove to clear). Prints the\n" +
			"cleaned absolute path. Never self-commits; a tk-driven scope may ride\n" +
			"sync_needed: dirty. Durability is tk sync (tk-driven) or a host commit\n" +
			"(repo-driven).",
		Args: minArgs("<text...>"),
		RunE: func(c *cobra.Command, args []string) error {
			return runNoteSet(app, c, args, scope, name, c.Flags().Changed("name"))
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "scope (defaults to ambient; wins over ambient)")
	cmd.Flags().StringVar(&name, "name", "", "note slug (one-shot; defaults to this machine's default)")
	return cmd
}

func newNoteEditCmd(app *App) *cobra.Command {
	var scope, name string
	cmd := &cobra.Command{
		Use:   "edit [--name slug]",
		Short: "Open a note in $EDITOR",
		Long: "Open the note path in $EDITOR (same split-and-stdio contract as tk edit).\n" +
			"Creates notes/ if needed but does not create the file. Quit without write\n" +
			"leaves no file. A zero-byte file is removed after the editor returns, and an\n" +
			"empty notes/ is removed. Prints the cleaned absolute path even if the file is\n" +
			"still missing. Never self-commits; durability is tk sync (tk-driven) or a\n" +
			"host commit (repo-driven).",
		Args: noArgs(),
		RunE: func(c *cobra.Command, _ []string) error {
			return runNoteEdit(app, c, scope, name, c.Flags().Changed("name"))
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "scope (defaults to ambient; wins over ambient)")
	cmd.Flags().StringVar(&name, "name", "", "note slug (one-shot; defaults to this machine's default)")
	return cmd
}

func newNoteRemoveCmd(app *App) *cobra.Command {
	var scope, name string
	cmd := &cobra.Command{
		Use:     "remove [--name slug]",
		Aliases: []string{"rm"},
		Short:   "Remove a note file",
		Long: "Unlink a regular note file. Omit --name to unlink this machine's default\n" +
			"(built-in `default` when unset). --name is a one-shot selector and never\n" +
			"writes the stored default. Missing is success and silent. An empty notes/\n" +
			"directory is removed. Prints nothing. Never self-commits; a tk-driven scope\n" +
			"may ride sync_needed: dirty. Durability is tk sync (tk-driven) or a host\n" +
			"commit (repo-driven).",
		Args: noArgs(),
		RunE: func(c *cobra.Command, _ []string) error {
			return runNoteRemove(app, c, scope, name, c.Flags().Changed("name"))
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "scope (defaults to ambient; wins over ambient)")
	cmd.Flags().StringVar(&name, "name", "", "note slug (one-shot; defaults to this machine's default)")
	return cmd
}

func newNoteUseCmd(app *App) *cobra.Command {
	var (
		scope    string
		clearUse bool
	)
	cmd := &cobra.Command{
		Use:   "use [slug] | --clear [--scope S]",
		Short: "Set, show, or clear this machine's default note slug",
		Long: "A per-scope, machine-local default note slug. With a slug, it sets the pointer;\n" +
			"with --clear (or the built-in slug `default`) it removes it; with no arguments\n" +
			"it prints the effective slug (`default` when unset). The pointer is XDG only:\n" +
			"it never writes tk.cue, me.cue, or lens.cue, never creates or deletes a note\n" +
			"file, never self-commits, and never emits sync_needed:. --name remains a\n" +
			"one-shot override on the other note verbs and is not accepted here. Personal\n" +
			"slugs (`grant`, `alice`) with `default` as the shared pad are a convention,\n" +
			"not a CLI rule. --clear takes no slug.",
		Args: maxArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return runNoteUse(app, c, args, scope, clearUse)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "scope (defaults to ambient; wins over ambient)")
	cmd.Flags().BoolVar(&clearUse, "clear", false, "revert to the built-in default for the scope")
	return cmd
}

func runNoteCat(app *App, c *cobra.Command, args []string, scopeFlag, nameFlag string, nameSet bool) error {
	positional := ""
	if len(args) == 1 {
		positional = args[0]
	}
	n, err := openNote(app, c, scopeFlag)
	if err != nil {
		return err
	}
	defer n.close()
	in, err := n.resolve(c, positional, nameFlag, nameSet)
	if err != nil {
		return mapNotesErr(err)
	}
	res, err := notes.Read(in)
	if err != nil {
		var miss *notes.MissingError
		if errors.As(err, &miss) && positional == "" && !nameSet {
			return nil
		}
		return mapNotesErr(err)
	}
	_, err = c.OutOrStdout().Write(res.Body)
	return err
}

func runNoteList(app *App, c *cobra.Command, scopeFlag string) error {
	n, err := openNote(app, c, scopeFlag)
	if err != nil {
		return err
	}
	defer n.close()
	slugs, err := notes.List(n.scope, n.dir)
	if err != nil {
		return err
	}
	for _, s := range slugs {
		stdoutln(c, s)
	}
	return nil
}

func runNoteAdd(app *App, c *cobra.Command, args []string, scopeFlag, nameFlag string, nameSet bool) error {
	text := strings.Join(args, " ")
	if text == "" {
		return usageErrorf("add needs non-empty text")
	}
	n, err := openNote(app, c, scopeFlag)
	if err != nil {
		return err
	}
	defer n.close()
	in, err := n.resolve(c, "", nameFlag, nameSet)
	if err != nil {
		return mapNotesErr(err)
	}
	res, err := notes.Add(n.deps(c), in, text)
	return emitNoteWrite(c, res, err)
}

func runNoteSet(app *App, c *cobra.Command, args []string, scopeFlag, nameFlag string, nameSet bool) error {
	payload, err := noteSetPayload(c, args)
	if err != nil {
		return err
	}
	if len(payload) == 0 {
		return usageErrorf("set needs non-empty text")
	}
	n, err := openNote(app, c, scopeFlag)
	if err != nil {
		return err
	}
	defer n.close()
	in, err := n.resolve(c, "", nameFlag, nameSet)
	if err != nil {
		return mapNotesErr(err)
	}
	res, err := notes.Set(n.deps(c), in, payload)
	return emitNoteWrite(c, res, err)
}

func runNoteEdit(app *App, c *cobra.Command, scopeFlag, nameFlag string, nameSet bool) error {
	fields, err := editorArgv("tk note edit")
	if err != nil {
		return err
	}
	n, err := openNote(app, c, scopeFlag)
	if err != nil {
		return err
	}
	defer n.close()
	in, err := n.resolve(c, "", nameFlag, nameSet)
	if err != nil {
		return mapNotesErr(err)
	}
	res, err := notes.PrepareEdit(in)
	if err != nil {
		return mapNotesErr(err)
	}
	edErr := runEditor(fields, res.Path)
	if err := notes.FinishEdit(n.dir, res.Path); err != nil && edErr == nil {
		return err
	}
	if edErr != nil {
		return edErr
	}
	stdoutln(c, res.Path)
	return nil
}

func runNoteRemove(app *App, c *cobra.Command, scopeFlag, nameFlag string, nameSet bool) error {
	n, err := openNote(app, c, scopeFlag)
	if err != nil {
		return err
	}
	defer n.close()
	in, err := n.resolve(c, "", nameFlag, nameSet)
	if err != nil {
		return mapNotesErr(err)
	}
	res, err := notes.Delete(n.deps(c), in)
	if err != nil {
		return mapNotesErr(err)
	}
	emitNoteSyncNeeded(c, res)
	return nil
}

func runNoteUse(app *App, c *cobra.Command, args []string, scopeFlag string, clearUse bool) error {
	if clearUse && len(args) > 0 {
		return usageErrorf("--clear takes no slug")
	}

	e, err := app.openEngine(c)
	if err != nil {
		return err
	}
	defer e.close()

	resolved, err := e.resolveAmbient(scopeFlag)
	if err != nil {
		return err
	}

	in := notes.UseInput{Scope: resolved.Name, Clear: clearUse}
	if !clearUse && len(args) == 1 {
		in.Slug = args[0]
	}
	res, err := notes.Use(e.notesDeps(c.Context()), in)
	if err != nil {
		return mapNotesErr(err)
	}
	if !clearUse && len(args) == 0 {
		stdoutln(c, res.Slug)
	}
	return nil
}

type noteScope struct {
	e     *engine
	scope string
	dir   string
}

func (n *noteScope) close() { n.e.close() }

func (n *noteScope) deps(c *cobra.Command) notes.Deps {
	return n.e.notesDeps(c.Context())
}

func (n *noteScope) resolve(c *cobra.Command, positional, nameFlag string, nameSet bool) (notes.Input, error) {
	slug, err := notes.ResolveName(n.deps(c), n.scope, notes.Selector{
		Positional: positional,
		Name:       nameFlag,
		NameSet:    nameSet,
	})
	if err != nil {
		return notes.Input{}, err
	}
	return notes.Input{Scope: n.scope, Dir: n.dir, Slug: slug}, nil
}

func openNote(app *App, c *cobra.Command, scopeFlag string) (*noteScope, error) {
	e, err := app.openEngine(c)
	if err != nil {
		return nil, err
	}
	resolved, err := e.resolveAmbient(scopeFlag)
	if err != nil {
		e.close()
		return nil, err
	}
	if err := notes.RequireDir(resolved.Name, resolved.Entry.Dir); err != nil {
		e.close()
		return nil, err
	}
	return &noteScope{e: e, scope: resolved.Name, dir: resolved.Entry.Dir}, nil
}

func noteSetPayload(c *cobra.Command, args []string) ([]byte, error) {
	if len(args) == 1 && args[0] == "-" {
		data, err := io.ReadAll(c.InOrStdin())
		if err != nil {
			return nil, fmt.Errorf("read stdin: %w", err)
		}
		return data, nil
	}
	return []byte(strings.Join(args, " ")), nil
}

func emitNoteWrite(c *cobra.Command, res notes.Result, err error) error {
	if err != nil {
		return mapNotesErr(err)
	}
	if res.Path != "" {
		stdoutln(c, res.Path)
	}
	emitNoteSyncNeeded(c, res)
	return nil
}

func emitNoteSyncNeeded(c *cobra.Command, res notes.Result) {
	if res.SyncNeeded != "" {
		stderrln(c, token.Line(token.SyncNeeded, res.SyncNeeded))
	}
}

func mapNotesErr(err error) error {
	if err == nil {
		return nil
	}
	var use *notes.UsageError
	if errors.As(err, &use) {
		return usageErrorf("%s", use.Msg)
	}
	return err
}
