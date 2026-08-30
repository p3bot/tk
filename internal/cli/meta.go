package cli

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/frontmatter"
	"github.com/p3bot/tk/internal/title"
	"github.com/p3bot/tk/internal/token"
	"github.com/p3bot/tk/internal/writeengine"
)

func newMetaCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "meta",
		Short: "Read and mutate ticket frontmatter fields",
		Long: "Read and mutate ticket frontmatter without $EDITOR.\n\n" +
			"  get  <id> [key]           full header or a single key value\n" +
			"  set  <id> <key> <value>   scalar keys (summary, custom string/int/bool)\n" +
			"  add  <id> <key> <value>   multi-value keys (depends, related, tags, links, custom strings)\n" +
			"  rm   <id> <key> <value>   remove one multi-value entry (alias: remove)\n\n" +
			"Full get prints title, path, whole-file lines/words/characters, a blank line,\n" +
			"and the raw frontmatter interior (never the body). Single-key get prints only\n" +
			"the value (multi-value: one entry per line). A trailing value of - reads the\n" +
			"value from stdin (one optional final newline stripped).\n\n" +
			"meta set refuses multi-value keys; meta add/rm refuse scalars. id, status, order,\n" +
			"created, and status_conflict are immutable via meta (use mark / order where\n" +
			"they apply). depends add enforces write-time integrity: self → depends_self:;\n" +
			"same-scope missing → depends_dangling:; cross-scope unregistered/absent →\n" +
			"depends_unresolvable: (hard refuse, no write). related is soft (no existence\n" +
			"check). Short ids on depends/related normalise to full ids in the subject scope.\n" +
			"After a successful set|add|rm, missing or empty scope-required custom fields\n" +
			"emit required_missing: on stderr (soft; exit 0). Key aliases: tag → tags,\n" +
			"link → links (wire keys stay plural).",
		Args: cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			if len(args) > 0 {
				return usageErrorf("unknown meta subcommand %q; run `tk meta --help` for get, set, add, rm", args[0])
			}
			return c.Help()
		},
	}
	cmd.AddCommand(
		newMetaGetCmd(app),
		newMetaSetCmd(app),
		newMetaAddCmd(app),
		newMetaRmCmd(app),
	)
	return cmd
}

func newMetaGetCmd(app *App) *cobra.Command {
	var scope string
	cmd := &cobra.Command{
		Use:   "get <id> [key] [--scope S]",
		Short: "Print ticket frontmatter (full header or one key)",
		Long: "Without a key: print title, path, whole-file lines/words/characters (wc-style\n" +
			"order), a blank line, and the raw frontmatter interior exactly as stored\n" +
			"(key order, quoting, comments, customs preserved). Counts cover the entire\n" +
			"file (frontmatter + body). lines is newline count; words are whitespace-\n" +
			"separated runs; characters is Unicode code points (not bytes).\n" +
			"Extractable frontmatter exits 0 even under parse_error (token on stderr);\n" +
			"wholly unparseable frontmatter is non-zero with empty stdout.\n\n" +
			"With a key: print only the decoded value — scalars as one line, multi-value\n" +
			"keys one entry per line. Absent key or empty list: empty stdout, exit 0.\n" +
			"If the typed model cannot be parsed: non-zero, empty stdout, parse_error token.\n" +
			"Immutable keys are readable. Unknown key is usage exit 2 listing known keys.\n" +
			"Key aliases: tag → tags, link → links (wire keys stay plural).\n" +
			"Pure read; never runs git.",
		Args: rangeArgs(1, 2, "<id>"),
		RunE: func(c *cobra.Command, args []string) error {
			key := ""
			if len(args) == 2 {
				key = args[1]
			}
			return runMetaGet(app, c, args[0], key, scope)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "ambient scope for a short id")
	return cmd
}

func newMetaSetCmd(app *App) *cobra.Command {
	var scope string
	cmd := &cobra.Command{
		Use:   "set <id> <key> <value> [--scope S]",
		Short: "Set a scalar frontmatter key (summary or custom string/int/bool)",
		Long: "Rewrite one scalar frontmatter key. Legal keys: summary and custom fields of\n" +
			"type string, int, or bool. Empty value omits the key (clear). Multi-value keys\n" +
			"(depends, related, tags/tag, links/link, custom strings) require meta add/rm.\n" +
			"Value - reads stdin (optional final newline stripped). Embedded newlines are\n" +
			"usage exit 2. Prints the absolute ticket path on success.",
		Args: exactArgs("<id>", "<key>", "<value>"),
		RunE: func(c *cobra.Command, args []string) error {
			return runMetaMutate(app, c, writeengine.MetaSet, args[0], args[1], args[2], scope)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "ambient scope for a short id")
	return cmd
}

func newMetaAddCmd(app *App) *cobra.Command {
	var scope string
	cmd := &cobra.Command{
		Use:   "add <id> <key> <value> [--scope S]",
		Short: "Append one entry to a multi-value frontmatter key",
		Long: "Append value to a multi-value key if not already present (idempotent).\n" +
			"Legal keys: depends, related, tags (alias: tag), links (alias: link), and\n" +
			"custom fields of type strings. Scalar keys require meta set. depends/related\n" +
			"short ids normalise to full ids in the subject scope. depends add refuses self\n" +
			"(depends_self:), same-scope missing (depends_dangling:), and cross-scope\n" +
			"unresolvable targets (depends_unresolvable:). related has no existence check.\n" +
			"tags/tag: when the value is new to the scope (not yet on any ticket, including\n" +
			"archive), emits on stderr after a successful write (soft; exit 0):\n" +
			"  tag_new: \"<t>\" is new to this scope\n" +
			"Re-add and already-used values stay quiet on that channel.\n" +
			"Value - reads stdin. Prints the absolute ticket path on success.",
		Args: exactArgs("<id>", "<key>", "<value>"),
		RunE: func(c *cobra.Command, args []string) error {
			return runMetaMutate(app, c, writeengine.MetaAdd, args[0], args[1], args[2], scope)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "ambient scope for a short id")
	return cmd
}

func newMetaRmCmd(app *App) *cobra.Command {
	var scope string
	cmd := &cobra.Command{
		Use:     "rm <id> <key> <value> [--scope S]",
		Aliases: []string{"remove"},
		Short:   "Remove one entry from a multi-value frontmatter key",
		Long: "Remove one matching entry from a multi-value key if present (idempotent when\n" +
			"absent). Legal keys: depends, related, tags (alias: tag), links (alias: link),\n" +
			"and custom fields of type strings. Value is required — this removes one list\n" +
			"entry, not the whole key. depends/related short ids normalise to full ids\n" +
			"before compare. Value - reads stdin. Prints the absolute ticket path on success.",
		Args: exactArgs("<id>", "<key>", "<value>"),
		RunE: func(c *cobra.Command, args []string) error {
			return runMetaMutate(app, c, writeengine.MetaRm, args[0], args[1], args[2], scope)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "ambient scope for a short id")
	return cmd
}

func runMetaGet(app *App, c *cobra.Command, idArg, key, scope string) error {
	key = frontmatter.CanonicalMetaKey(key)

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
	p := r.rows[0]
	if err := ensureFileExists(p); err != nil {
		return err
	}

	data, err := os.ReadFile(p.Path)
	if err != nil {
		return fmt.Errorf("read %s: %w", p.Path, err)
	}
	interior, body, present := frontmatter.Split(data)
	if !present {
		stderrln(c, token.Line(token.ParseError, fmt.Sprintf("%s: no extractable frontmatter block", p.ID)))
		return fmt.Errorf("ticket %s has no readable frontmatter", p.ID)
	}

	if key == "" {
		lines, words, characters := countFileText(data)
		stdoutln(c, "title: "+title.Extract(body))
		stdoutln(c, "path: "+p.Path)
		stdoutln(c, "lines: "+strconv.Itoa(lines))
		stdoutln(c, "words: "+strconv.Itoa(words))
		stdoutln(c, "characters: "+strconv.Itoa(characters))
		stdoutln(c, "")
		if _, err := c.OutOrStdout().Write(interior); err != nil {
			return err
		}
		if p.ParseError {
			stderrln(c, token.Line(token.ParseError, fmt.Sprintf("%s: %s", p.ID, p.ParseMsg)))
		}
		if len(p.StatusConflict) > 0 {
			stderrln(c, fmt.Sprintf("status_conflict: %s disputes %s", p.ID, joinComma(p.StatusConflict)))
		}
		return nil
	}

	schema := r.res.Schema(r.scope)
	class, field, err := writeengine.ClassifyMetaKey(key, schema)
	if err != nil {
		return err
	}
	if class == writeengine.MetaKeyUnknown {
		return mapWriteErr(writeengine.UnknownMetaKeyError(key, schema))
	}

	// Single-key get: parse failure is not "key absent".
	m, err := frontmatter.Parse(interior)
	if err != nil {
		msg := err.Error()
		if p.ParseError && p.ParseMsg != "" {
			msg = p.ParseMsg
		}
		return fmt.Errorf("%s", token.Line(token.ParseError,
			fmt.Sprintf("%s: %s — cannot decode frontmatter for key get", p.ID, msg)))
	}
	if p.ParseError {
		stderrln(c, token.Line(token.ParseError, fmt.Sprintf("%s: %s", p.ID, p.ParseMsg)))
	}

	out, err := writeengine.MetaGetValue(m, key, class, field)
	if err != nil {
		return err
	}
	if out != "" {
		if _, err := c.OutOrStdout().Write([]byte(out)); err != nil {
			return err
		}
		if !strings.HasSuffix(out, "\n") {
			stdoutln(c, "")
		}
	}
	return nil
}

func runMetaMutate(app *App, c *cobra.Command, op writeengine.MetaOp, idArg, key, valueArg, scopeFlag string) error {
	form, ok := parseIDArg(idArg)
	if !ok {
		return usageErrorf("%q is not a valid ticket id", idArg)
	}

	value, err := loadMetaValue(c, valueArg)
	if err != nil {
		return err
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
	lu, err := e.writeLookup(scope, idArg, form)
	if err != nil {
		return err
	}
	res, err := writeengine.Meta(e.writeDeps(c.Context()), writeengine.MetaInput{
		Scope:  scope,
		Dir:    entry.Dir,
		Lookup: lu,
		Op:     op,
		Key:    key,
		Value:  value,
	})
	return emitWriteResult(c, res, err)
}

// loadMetaValue: "-" reads stdin and strips one optional final line ending.
func loadMetaValue(c *cobra.Command, valueArg string) (string, error) {
	if valueArg != "-" {
		return valueArg, nil
	}
	data, err := io.ReadAll(c.InOrStdin())
	if err != nil {
		return "", fmt.Errorf("read stdin: %w", err)
	}
	s := string(data)
	switch {
	case strings.HasSuffix(s, "\r\n"):
		s = s[:len(s)-2]
	case strings.HasSuffix(s, "\n"), strings.HasSuffix(s, "\r"):
		s = s[:len(s)-1]
	}
	return s, nil
}
