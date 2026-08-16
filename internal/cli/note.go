package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/atomicfile"
	"github.com/p3bot/tk/internal/scopefile"
	"github.com/p3bot/tk/internal/slug"
	"github.com/p3bot/tk/internal/token"
)

const noteFileMode = 0o644

func newNoteCmd(app *App) *cobra.Command {
	var scope, name string
	cmd := &cobra.Command{
		Use:     "note [slug]",
		Aliases: []string{"notes"},
		Short:   "Read and write committed scope notes",
		Long: "Scope worklog documents at <scope-dir>/notes/<slug>.md. Bare `tk note` (or a\n" +
			"slug / --name) prints the file bytes. Missing and empty files are empty stdout,\n" +
			"exit 0. `list` prints addressable slugs, one per line, alphabetical.\n" +
			"`add` appends one line; `set` replaces the file (`-` reads stdin); `edit` opens\n" +
			"$EDITOR; `delete --name <slug>` unlinks. Omit --name to use default\n" +
			"(notes/default.md) except on delete, which requires --name.\n" +
			"\n" +
			"Writes never self-commit. On a tk-driven scope, add, set, and delete ride\n" +
			"sync_needed: when the allowlist is dirty (same as create); edit does not.\n" +
			"Durability is `tk sync` on a tk-driven scope, or a host commit on a\n" +
			"repo-driven scope. Notes are not tickets: they are not indexed, not listed\n" +
			"by `tk list`, and not taught in `tk skill`. Alias: notes.",
		Args: usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(c *cobra.Command, args []string) error {
			return runNoteCat(app, c, args, scope, name, c.Flags().Changed("name"))
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "scope (defaults to ambient; wins over ambient)")
	cmd.Flags().StringVar(&name, "name", "", "note slug (defaults to default)")
	cmd.AddCommand(
		newNoteListCmd(app),
		newNoteAddCmd(app),
		newNoteSetCmd(app),
		newNoteEditCmd(app),
		newNoteDeleteCmd(app),
	)
	return cmd
}

func newNoteListCmd(app *App) *cobra.Command {
	var scope string
	cmd := &cobra.Command{
		Use:   "list [--scope S]",
		Short: "List addressable note slugs",
		Long: "Print addressable note slugs under notes/, one per line, alphabetical.\n" +
			"Reserved verb names, invalid slugs, and nested paths are omitted (doctor owns\n" +
			"that residue). A missing notes/ directory is empty stdout, exit 0.",
		Args: usageArgs(cobra.NoArgs),
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
		Args: usageArgs(cobra.MinimumNArgs(1)),
		RunE: func(c *cobra.Command, args []string) error {
			return runNoteAdd(app, c, args, scope, name, c.Flags().Changed("name"))
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "scope (defaults to ambient; wins over ambient)")
	cmd.Flags().StringVar(&name, "name", "", "note slug (defaults to default)")
	return cmd
}

func newNoteSetCmd(app *App) *cobra.Command {
	var scope, name string
	cmd := &cobra.Command{
		Use:   "set [--name slug] <text...> | -",
		Short: "Replace a note's contents",
		Long: "Replace the whole file with the joined arguments (one line) or, when `-` is\n" +
			"the sole text operand, stdin. The file always ends with a newline. No text,\n" +
			"an empty string, or empty stdin is usage (use delete to clear). Prints the\n" +
			"cleaned absolute path. Never self-commits; a tk-driven scope may ride\n" +
			"sync_needed: dirty. Durability is tk sync (tk-driven) or a host commit\n" +
			"(repo-driven).",
		Args: usageArgs(cobra.MinimumNArgs(1)),
		RunE: func(c *cobra.Command, args []string) error {
			return runNoteSet(app, c, args, scope, name, c.Flags().Changed("name"))
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "scope (defaults to ambient; wins over ambient)")
	cmd.Flags().StringVar(&name, "name", "", "note slug (defaults to default)")
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
		Args: usageArgs(cobra.NoArgs),
		RunE: func(c *cobra.Command, _ []string) error {
			return runNoteEdit(app, c, scope, name, c.Flags().Changed("name"))
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "scope (defaults to ambient; wins over ambient)")
	cmd.Flags().StringVar(&name, "name", "", "note slug (defaults to default)")
	return cmd
}

func newNoteDeleteCmd(app *App) *cobra.Command {
	var scope, name string
	cmd := &cobra.Command{
		Use:   "delete --name <slug>",
		Short: "Remove a note file",
		Long: "Unlink a regular note file. --name is required. Missing is success and silent.\n" +
			"An empty notes/ directory is removed. Prints nothing. Never self-commits;\n" +
			"a tk-driven scope may ride sync_needed: dirty. Durability is tk sync\n" +
			"(tk-driven) or a host commit (repo-driven).",
		Args: usageArgs(cobra.NoArgs),
		RunE: func(c *cobra.Command, _ []string) error {
			return runNoteDelete(app, c, scope, name, c.Flags().Changed("name"))
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "scope (defaults to ambient; wins over ambient)")
	cmd.Flags().StringVar(&name, "name", "", "note slug (required)")
	return cmd
}

func runNoteCat(app *App, c *cobra.Command, args []string, scopeFlag, nameFlag string, nameSet bool) error {
	positional := ""
	if len(args) == 1 {
		positional = args[0]
	}
	name, err := selectNoteName(positional, nameFlag, nameSet)
	if err != nil {
		return err
	}
	n, err := openNote(app, c, scopeFlag)
	if err != nil {
		return err
	}
	defer n.close()
	return catNoteFile(c, n.path(name))
}

func runNoteList(app *App, c *cobra.Command, scopeFlag string) error {
	n, err := openNote(app, c, scopeFlag)
	if err != nil {
		return err
	}
	defer n.close()

	slugs, err := listNoteSlugs(n.dir)
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
	name, err := selectNoteName("", nameFlag, nameSet)
	if err != nil {
		return err
	}
	n, err := openNote(app, c, scopeFlag)
	if err != nil {
		return err
	}
	defer n.close()
	path := n.path(name)
	if err := withNoteLock(n.dir, func() error {
		if err := n.refuseMidRebase(c.Context()); err != nil {
			return err
		}
		if err := refuseNonRegularNote(path); err != nil {
			return err
		}
		existing, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("read %s: %w", path, err)
		}
		var buf []byte
		if len(existing) > 0 {
			buf = existing
			if existing[len(existing)-1] != '\n' {
				buf = append(buf, '\n')
			}
		}
		buf = append(buf, text...)
		buf = append(buf, '\n')
		return atomicfile.Write(path, buf, noteFileMode)
	}); err != nil {
		return err
	}
	n.maybeSyncNeeded(c)
	return printNotePath(c, path)
}

func runNoteSet(app *App, c *cobra.Command, args []string, scopeFlag, nameFlag string, nameSet bool) error {
	payload, err := noteSetPayload(c, args)
	if err != nil {
		return err
	}
	name, err := selectNoteName("", nameFlag, nameSet)
	if err != nil {
		return err
	}
	n, err := openNote(app, c, scopeFlag)
	if err != nil {
		return err
	}
	defer n.close()
	path := n.path(name)
	if err := withNoteLock(n.dir, func() error {
		if err := n.refuseMidRebase(c.Context()); err != nil {
			return err
		}
		if err := refuseNonRegularNote(path); err != nil {
			return err
		}
		return atomicfile.Write(path, payload, noteFileMode)
	}); err != nil {
		return err
	}
	n.maybeSyncNeeded(c)
	return printNotePath(c, path)
}

func runNoteEdit(app *App, c *cobra.Command, scopeFlag, nameFlag string, nameSet bool) error {
	fields, err := editorArgv("tk note edit")
	if err != nil {
		return err
	}
	name, err := selectNoteName("", nameFlag, nameSet)
	if err != nil {
		return err
	}

	n, err := openNote(app, c, scopeFlag)
	if err != nil {
		return err
	}
	defer n.close()
	path := n.path(name)
	notesDir := filepath.Join(n.dir, scopefile.NoteDir)
	if err := withNoteLock(n.dir, func() error {
		if err := os.MkdirAll(notesDir, 0o755); err != nil {
			return fmt.Errorf("create notes dir: %w", err)
		}
		return refuseNonRegularNote(path)
	}); err != nil {
		return err
	}

	edErr := runEditor(fields, path)
	if err := withNoteLock(n.dir, func() error {
		return cleanupEmptyNote(n.dir, path)
	}); err != nil && edErr == nil {
		return err
	}
	if edErr != nil {
		return edErr
	}
	return printNotePath(c, path)
}

func runNoteDelete(app *App, c *cobra.Command, scopeFlag, nameFlag string, nameSet bool) error {
	if !nameSet {
		return usageErrorf("delete requires --name")
	}
	name, err := selectNoteName("", nameFlag, nameSet)
	if err != nil {
		return err
	}
	n, err := openNote(app, c, scopeFlag)
	if err != nil {
		return err
	}
	defer n.close()
	path := n.path(name)
	removed := false
	if err := withNoteLock(n.dir, func() error {
		if err := n.refuseMidRebase(c.Context()); err != nil {
			return err
		}
		st, err := os.Lstat(path)
		if err != nil {
			if os.IsNotExist(err) {
				rmdirNotesIfEmpty(n.dir)
				return nil
			}
			return fmt.Errorf("stat %s: %w", path, err)
		}
		if !st.Mode().IsRegular() {
			return fmt.Errorf("%s is not a regular file", path)
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove %s: %w", path, err)
		}
		rmdirNotesIfEmpty(n.dir)
		removed = true
		return nil
	}); err != nil {
		return err
	}
	if removed {
		n.maybeSyncNeeded(c)
	}
	return nil
}

type noteScope struct {
	e     *engine
	scope string
	dir   string
}

func (n *noteScope) close() { n.e.close() }

func (n *noteScope) path(name string) string {
	return scopefile.NoteFile(n.dir, name)
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
	if err := requireScopeDir(resolved.Name, resolved.Entry.Dir); err != nil {
		e.close()
		return nil, err
	}
	return &noteScope{e: e, scope: resolved.Name, dir: resolved.Entry.Dir}, nil
}

// refuseMidRebase: create-class fence. Quiet when the schema is unusable
// (notes stay usable) or the scope is not tk-driven. Edit does not call this.
func (n *noteScope) refuseMidRebase(ctx context.Context) error {
	root, hasRoot := scopefile.GitRoot(n.dir)
	schema, cfgErr := n.e.rec.SchemaOrError(n.scope, n.dir)
	if cfgErr != nil {
		return nil
	}
	return checkMidRebase(ctx, n.scope, schemaAutoCommit(schema), root, hasRoot)
}

// maybeSyncNeeded: create-class hint after add/set/delete. Quiet when the schema
// is unusable or the scope is not tk-driven. Edit does not call this.
func (n *noteScope) maybeSyncNeeded(c *cobra.Command) {
	root, hasRoot := scopefile.GitRoot(n.dir)
	if !hasRoot {
		return
	}
	schema, cfgErr := n.e.rec.SchemaOrError(n.scope, n.dir)
	if cfgErr != nil || !schemaAutoCommit(schema) {
		return
	}
	n.e.tkDrivenSyncNeeded(c.Context(), c, n.dir, root)
}

func requireScopeDir(scope, dir string) error {
	st, err := os.Lstat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%s", token.Line(token.UnreachableScope,
				fmt.Sprintf("%s: dir %s is not reachable", scope, dir)))
		}
		return fmt.Errorf("stat %s: %w", dir, err)
	}
	if !st.IsDir() {
		return fmt.Errorf("%s", token.Line(token.UnreachableScope,
			fmt.Sprintf("%s: dir %s is not reachable", scope, dir)))
	}
	return nil
}

func selectNoteName(positional, nameFlag string, nameSet bool) (string, error) {
	if positional != "" && nameSet {
		return "", usageErrorf("use a positional slug or --name, not both")
	}
	name := scopefile.NoteDefaultSlug
	switch {
	case positional != "":
		name = positional
	case nameSet:
		name = nameFlag
	}
	if scopefile.IsReservedNoteName(name) {
		return "", usageErrorf("%q is a reserved note name", name)
	}
	if !slug.Valid(name) {
		return "", usageErrorf("%q is not a valid note slug", name)
	}
	return name, nil
}

func catNoteFile(c *cobra.Command, path string) error {
	st, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	if st.Size() == 0 {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	_, err = c.OutOrStdout().Write(data)
	return err
}

func listNoteSlugs(dir string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(dir, scopefile.NoteDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read notes dir: %w", err)
	}
	var slugs []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		stem, ok := strings.CutSuffix(e.Name(), ".md")
		if !ok || !scopefile.IsAddressableNoteSlug(stem) {
			continue
		}
		if !dirEntryRegular(e) {
			continue
		}
		slugs = append(slugs, stem)
	}
	sort.Strings(slugs)
	return slugs, nil
}

func dirEntryRegular(e os.DirEntry) bool {
	mode := e.Type()
	if mode == 0 {
		info, err := e.Info()
		if err != nil {
			return false
		}
		return info.Mode().IsRegular()
	}
	return mode.IsRegular()
}

func noteSetPayload(c *cobra.Command, args []string) ([]byte, error) {
	if len(args) == 1 && args[0] == "-" {
		data, err := io.ReadAll(c.InOrStdin())
		if err != nil {
			return nil, fmt.Errorf("read stdin: %w", err)
		}
		if len(data) == 0 {
			return nil, usageErrorf("set needs non-empty text")
		}
		if data[len(data)-1] != '\n' {
			data = append(data, '\n')
		}
		return data, nil
	}
	text := strings.Join(args, " ")
	if text == "" {
		return nil, usageErrorf("set needs non-empty text")
	}
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return []byte(text), nil
}

func printNotePath(c *cobra.Command, path string) error {
	abs, err := absPath(path)
	if err != nil {
		return err
	}
	stdoutln(c, abs)
	return nil
}

func withNoteLock(dir string, fn func() error) error {
	lock, err := scopefile.AcquireLock(dir)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()
	return fn()
}

func refuseNonRegularNote(path string) error {
	st, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	return nil
}

func cleanupEmptyNote(dir, path string) error {
	st, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			rmdirNotesIfEmpty(dir)
			return nil
		}
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if st.Mode().IsRegular() && st.Size() == 0 {
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove empty %s: %w", path, err)
		}
	}
	rmdirNotesIfEmpty(dir)
	return nil
}

func rmdirNotesIfEmpty(dir string) {
	p := filepath.Join(dir, scopefile.NoteDir)
	st, err := os.Lstat(p)
	if err != nil || !st.IsDir() {
		return
	}
	_ = os.Remove(p)
}
