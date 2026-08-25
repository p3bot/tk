package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/atomicfile"
	"github.com/p3bot/tk/internal/scopeconfig"
	"github.com/p3bot/tk/internal/scopefile"
	"github.com/p3bot/tk/internal/selfcommit"
	"github.com/p3bot/tk/internal/token"
)

func newScopeFieldCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "field",
		Aliases: []string{"fields"},
		Short:   "Declare custom frontmatter fields in the scope's tk.cue (fields:)",
		Long: "Read and rewrite custom field declarations under fields: in the target\n" +
			"scope's tk.cue. The CLI noun is field (alias: fields); the CUE key is fields.\n\n" +
			"  list                 print declared fields (name, type, required, values)\n" +
			"  set <name> --type T  create or fully replace one declaration\n" +
			"  unset <name>         remove one declaration (ticket files untouched)\n\n" +
			"Target scope uses the ambient chain shared with board verbs: --scope >\n" +
			"TK_SCOPE > cwd code-root (not a positional scope name). set fully replaces\n" +
			"the named declaration from this invocation's flags: omit --required demotes\n" +
			"to optional; omit --values clears any prior enum. required is soft policy\n" +
			"only (meta/mark may warn; they never refuse solely for a missing required).",
		Args: cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			if len(args) > 0 {
				return usageErrorf("unknown field subcommand %q; run `tk scope field --help` for list, set, unset", args[0])
			}
			return c.Help()
		},
	}
	cmd.AddCommand(
		newScopeFieldListCmd(app),
		newScopeFieldSetCmd(app),
		newScopeFieldUnsetCmd(app),
	)
	return cmd
}

func newScopeFieldListCmd(app *App) *cobra.Command {
	var scope string
	cmd := &cobra.Command{
		Use:   "list [--scope S]",
		Short: "List custom field declarations for the ambient scope",
		Long: "Print one TSV line per declared custom field, sorted by name:\n" +
			"  <name>\\t<type>\\t<required>\\t<values>\n" +
			"required is true or false. values is a JSON array when the field has an enum\n" +
			"(including empty []), or empty when no enum. Headerless. Empty fields: exits\n" +
			"0 with empty stdout.",
		Args: noArgs(),
		RunE: func(c *cobra.Command, _ []string) error {
			return runScopeFieldList(app, c, scope)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "scope to list fields for (defaults to ambient)")
	return cmd
}

func newScopeFieldSetCmd(app *App) *cobra.Command {
	var (
		scope    string
		typ      string
		required bool
		values   []string
	)
	cmd := &cobra.Command{
		Use:   "set <name> --type <string|int|bool|strings> [--required] [--values V]... [--scope S]",
		Short: "Create or fully replace one custom field declaration",
		Long: "Upsert one field under fields: in the ambient scope's tk.cue. --type is\n" +
			"required. Each set fully replaces that field's declaration from the flags on\n" +
			"this invocation: --required present → required true; omitted → optional even\n" +
			"if the previous declaration was required. --values (repeatable) sets the enum\n" +
			"(string/strings only); omit --values to clear any prior enum. Legal names,\n" +
			"types, and enum rules match schema validation. Prints the absolute tk.cue\n" +
			"path on success. Auto-commit scopes self-commit the config change when a\n" +
			"git-root exists.",
		Args: exactArgs("<name>"),
		RunE: func(c *cobra.Command, args []string) error {
			return runScopeFieldSet(app, c, args[0], typ, required, values, scope)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "scope to edit (defaults to ambient)")
	cmd.Flags().StringVar(&typ, "type", "", "field type: string, int, bool, or strings (required)")
	cmd.Flags().BoolVar(&required, "required", false, "mark the field required (soft warn only when missing)")
	cmd.Flags().StringArrayVar(&values, "values", nil, "enum value (repeatable; string/strings only)")
	_ = cmd.MarkFlagRequired("type")
	return cmd
}

func newScopeFieldUnsetCmd(app *App) *cobra.Command {
	var scope string
	cmd := &cobra.Command{
		Use:   "unset <name> [--scope S]",
		Short: "Remove one custom field declaration from tk.cue",
		Long: "Remove the named field from fields: in the ambient scope's tk.cue only.\n" +
			"Does not open, rewrite, or strip keys from any ticket markdown — existing\n" +
			"values stay on disk; meta no longer allowlists the key until re-declared.\n" +
			"Prints the absolute tk.cue path on success.",
		Args: exactArgs("<name>"),
		RunE: func(c *cobra.Command, args []string) error {
			return runScopeFieldUnset(app, c, args[0], scope)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "scope to edit (defaults to ambient)")
	return cmd
}

func runScopeFieldList(app *App, c *cobra.Command, scopeFlag string) error {
	e, err := app.openEngine(c)
	if err != nil {
		return err
	}
	defer e.close()

	resolved, err := e.resolveAmbient(scopeFlag)
	if err != nil {
		return err
	}
	schema, err := loadScopeSchema(app, resolved.Name, resolved.Entry.Dir)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(schema.Fields))
	for name := range schema.Fields {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		f := schema.Fields[name]
		req := "false"
		if f.Required {
			req = "true"
		}
		vals := ""
		if f.Values != nil {
			b, err := json.Marshal(f.Values)
			if err != nil {
				return fmt.Errorf("encode values for field %q: %w", name, err)
			}
			vals = string(b)
		}
		stdoutln(c, tsvLine(name, f.Type, req, vals))
	}
	return nil
}

func runScopeFieldSet(app *App, c *cobra.Command, name, typ string, required bool, values []string, scopeFlag string) error {
	// Full replace: only flags on this invocation. --values unset → nil (clear enum).
	var enum []string
	if len(values) > 0 {
		enum = append([]string(nil), values...)
	}
	f := scopeconfig.Field{Type: typ, Required: required, Values: enum}
	if err := scopeconfig.ValidateFieldDecl(name, f); err != nil {
		return usageErrorf("%s", err.Error())
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
	scope := resolved.Name
	dir := resolved.Entry.Dir

	schema, err := loadScopeSchema(app, scope, dir)
	if err != nil {
		return err
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
	if err := scopeconfig.SetField(dir, name, f); err != nil {
		return err
	}
	// Package-unified outcome must match full-replace intent; restore on mismatch
	// or load failure so multi-file siblings cannot leave a silent or dirty write.
	if err := confirmFieldSet(app, scope, dir, name, f, cuePath, prev); err != nil {
		return err
	}

	if err := e.fieldConfigDurability(c, scope, dir, autoCommit, root, hasRoot,
		fmt.Sprintf("tk: scope field set %s", name), cuePath); err != nil {
		return err
	}
	out, err := absPath(cuePath)
	if err != nil {
		return err
	}
	stdoutln(c, out)
	return nil
}

func runScopeFieldUnset(app *App, c *cobra.Command, name, scopeFlag string) error {
	e, err := app.openEngine(c)
	if err != nil {
		return err
	}
	defer e.close()

	resolved, err := e.resolveAmbient(scopeFlag)
	if err != nil {
		return err
	}
	scope := resolved.Name
	dir := resolved.Entry.Dir

	schema, err := loadScopeSchema(app, scope, dir)
	if err != nil {
		return err
	}
	if _, ok := schema.Fields[name]; !ok {
		return usageErrorf("field %q is not declared", name)
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
	if err := scopeconfig.UnsetField(dir, name); err != nil {
		// Schema had the name (package-unified) but tk.cue does not own a copy.
		if errors.Is(err, scopeconfig.ErrFieldNotDeclared) {
			return usageErrorf("field %q is not declared in tk.cue (remove it from the sibling package file that defines it, or move the declaration into tk.cue first)", name)
		}
		return err
	}
	if err := confirmFieldUnset(app, scope, dir, name, cuePath, prev); err != nil {
		return err
	}

	if err := e.fieldConfigDurability(c, scope, dir, autoCommit, root, hasRoot,
		fmt.Sprintf("tk: scope field unset %s", name), cuePath); err != nil {
		return err
	}
	out, err := absPath(cuePath)
	if err != nil {
		return err
	}
	stdoutln(c, out)
	return nil
}

// loadScopeSchema evaluates the scope the same way reconcile does (package
// import closure), not single-file CompileBytes. Multi-file package configs
// with cross-file refs (e.g. values: tags in a sibling .cue) must work here.
func loadScopeSchema(app *App, scope, dir string) (*scopeconfig.Schema, error) {
	schema, _, err := scopeconfig.LoadWithClosure(app.Ctx, dir)
	if err != nil {
		if ce, ok := scopeconfig.AsConfigError(err); ok {
			return nil, fmt.Errorf("%s", token.Line(token.ConfigUnparseable,
				fmt.Sprintf("%s (%s): %s — fix tk.cue before continuing", scope, ce.Dir, ce.Reason)))
		}
		return nil, err
	}
	return schema, nil
}

func restoreCueFile(path string, prev []byte) error {
	return atomicfile.Write(path, prev, 0o600)
}

func withCueRestore(cuePath string, prev []byte, err error) error {
	if rerr := restoreCueFile(cuePath, prev); rerr != nil {
		return fmt.Errorf("%w (also failed to restore tk.cue: %w)", err, rerr)
	}
	return err
}

// confirmFieldSet loads the package after SetField and requires the unified
// field to equal the full-replace intent. Restores prev on load or mismatch.
func confirmFieldSet(app *App, scope, dir, name string, want scopeconfig.Field, cuePath string, prev []byte) error {
	schema, err := loadScopeSchema(app, scope, dir)
	if err != nil {
		return withCueRestore(cuePath, prev, err)
	}
	got, ok := schema.Fields[name]
	if !ok || !scopeconfig.FieldEqual(got, want) {
		return withCueRestore(cuePath, prev, usageErrorf(
			"field %q was not fully replaced (a sibling package file still constrains type, required, or values); edit that file or move the declaration into tk.cue", name))
	}
	return nil
}

// confirmFieldUnset requires the package-unified schema to drop name after
// UnsetField. Restores prev when a sibling definition keeps the field alive.
func confirmFieldUnset(app *App, scope, dir, name, cuePath string, prev []byte) error {
	schema, err := loadScopeSchema(app, scope, dir)
	if err != nil {
		return withCueRestore(cuePath, prev, err)
	}
	if _, ok := schema.Fields[name]; ok {
		return withCueRestore(cuePath, prev, usageErrorf(
			"field %q remains declared outside tk.cue; remove it from the sibling package file that defines it", name))
	}
	return nil
}

// fieldConfigDurability matches scope rename: self-commit tk.cue on auto-commit roots.
func (e *engine) fieldConfigDurability(c *cobra.Command, scope, dir string, autoCommit bool, root string, hasRoot bool, message, cuePath string) error {
	if !autoCommit {
		return nil
	}
	if !hasRoot {
		stderrln(c, token.Line(token.SyncDisabled,
			fmt.Sprintf("%s: no git repository — field config written but not committed", scope)))
		return nil
	}
	if err := selfcommit.CommitPaths(c.Context(), selfcommit.BatchRequest{
		StateDir: e.app.StateDir, GitRoot: root,
		Message: message, Paths: []string{cuePath},
	}); err != nil {
		return err
	}
	e.tkDrivenSyncNeeded(c.Context(), c, dir, root)
	return nil
}
