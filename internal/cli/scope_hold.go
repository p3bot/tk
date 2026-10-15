package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/scopeconfig"
	"github.com/p3bot/tk/internal/scopefile"
)

func newScopeHoldCmd(app *App) *cobra.Command {
	var scope string
	cmd := &cobra.Command{
		Use:   "hold [true|false] [--scope S]",
		Short: "Read or set this scope's hold bool",
		Long: "Read or rewrite hold in tk.cue. Bare invocation prints the evaluated\n" +
			"bool of the ambient scope as true or false. true or false rewrites that\n" +
			"one scope. Hold is per scope: scopes that share a git-root may disagree.\n" +
			"Omitted hold is false. Already-that-value is ensure (exit 0, no rewrite).\n" +
			"A tk-driven scope self-commits the rewrite. A held scope prints\n" +
			"scope_hold: on stderr and still reads or sets the bool.\n" +
			"Target scope uses the ambient chain shared with board verbs:\n" +
			"--scope > TK_SCOPE > cwd code-root (not a positional scope name).",
		Args: rangeArgs(0, 1),
		RunE: func(c *cobra.Command, args []string) error {
			if len(args) == 0 {
				return runScopeHoldRead(app, c, scope)
			}
			want, err := parseAutoCommitArg(args[0])
			if err != nil {
				return err
			}
			return runScopeHoldSet(app, c, want, scope)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "scope to read or set (defaults to ambient)")
	return cmd
}

func runScopeHoldRead(app *App, c *cobra.Command, scopeFlag string) error {
	e, err := app.openEngine(c)
	if err != nil {
		return err
	}
	defer e.close()

	resolved, err := e.resolveAmbient(c, scopeFlag)
	if err != nil {
		return err
	}
	schema, err := loadScopeSchema(app, resolved.Name, resolved.Entry.Dir)
	if err != nil {
		return err
	}
	stdoutln(c, strconv.FormatBool(schema.Hold))
	return nil
}

func runScopeHoldSet(app *App, c *cobra.Command, want bool, scopeFlag string) error {
	e, err := app.openEngine(c)
	if err != nil {
		return err
	}
	defer e.close()

	resolved, err := e.resolveAmbient(c, scopeFlag)
	if err != nil {
		return err
	}
	scope := resolved.Name
	dir := resolved.Entry.Dir

	schema, err := loadScopeSchema(app, scope, dir)
	if err != nil {
		return err
	}
	if schema.Hold == want {
		stderrln(c, fmt.Sprintf("hold is already %s", strconv.FormatBool(want)))
		return nil
	}
	autoCommit := schema.AutoCommit
	root, hasRoot := scopefile.GitRoot(dir)
	if err := checkMidRebase(c.Context(), scope, autoCommit, root, hasRoot); err != nil {
		return err
	}

	lock, err := scopefile.AcquireLock(dir)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()

	cuePath := filepath.Join(dir, "tk.cue")
	prev, err := os.ReadFile(cuePath)
	if err != nil {
		return err
	}
	if err := scopeconfig.RewriteHold(dir, want); err != nil {
		return err
	}
	confirmed, err := loadScopeSchema(app, scope, dir)
	if err != nil {
		return withCueRestore(cuePath, prev, err)
	}
	if confirmed.Hold != want {
		return withCueRestore(cuePath, prev, usageErrorf(
			"hold was not set (a sibling package file still pins hold); edit that file or move the declaration into tk.cue"))
	}

	if err := e.fieldConfigDurability(c, scope, dir, autoCommit, root, hasRoot,
		fmt.Sprintf("tk: scope hold %s", strconv.FormatBool(want)), cuePath); err != nil {
		return withCueRestore(cuePath, prev, err)
	}
	out, err := absPath(cuePath)
	if err != nil {
		return err
	}
	stdoutln(c, out)
	return nil
}
