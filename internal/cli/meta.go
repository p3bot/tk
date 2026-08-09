package cli

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/p3bot/tk/internal/scopefile"

	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/frontmatter"
	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/scopeconfig"
	"github.com/p3bot/tk/internal/title"
	"github.com/p3bot/tk/internal/token"
)

type metaOp int

const (
	metaOpSet metaOp = iota
	metaOpAdd
	metaOpRm
)

func (op metaOp) String() string {
	switch op {
	case metaOpSet:
		return "set"
	case metaOpAdd:
		return "add"
	case metaOpRm:
		return "rm"
	default:
		return "meta"
	}
}

type metaKeyClass int

const (
	metaKeyUnknown metaKeyClass = iota
	metaKeyImmutable
	metaKeyScalar
	metaKeyMulti
)

var builtinMetaKeyOrder = []string{
	frontmatter.KeyID,
	frontmatter.KeyStatus,
	frontmatter.KeyOrder,
	frontmatter.KeyDepends,
	frontmatter.KeyRelated,
	frontmatter.KeyTags,
	frontmatter.KeyCreated,
	frontmatter.KeyLinks,
	frontmatter.KeySummary,
	frontmatter.KeyStatusConflict,
}

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
			"created, and status_conflict are immutable via meta (use mark / reorder where\n" +
			"they apply). depends add enforces write-time integrity: self → depends_self:;\n" +
			"same-scope missing → depends_dangling:; cross-scope unregistered/absent →\n" +
			"depends_unresolvable: (hard refuse, no write). related is soft (no existence\n" +
			"check). Short ids on depends/related normalise to full ids in the subject scope.\n" +
			"Key aliases: tag → tags, link → links (wire keys stay plural).",
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
		Args: usageArgs(cobra.RangeArgs(1, 2)),
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
		Args: usageArgs(cobra.ExactArgs(3)),
		RunE: func(c *cobra.Command, args []string) error {
			return runMetaMutate(app, c, metaOpSet, args[0], args[1], args[2], scope)
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
		Args: usageArgs(cobra.ExactArgs(3)),
		RunE: func(c *cobra.Command, args []string) error {
			return runMetaMutate(app, c, metaOpAdd, args[0], args[1], args[2], scope)
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
		Args: usageArgs(cobra.ExactArgs(3)),
		RunE: func(c *cobra.Command, args []string) error {
			return runMetaMutate(app, c, metaOpRm, args[0], args[1], args[2], scope)
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
	class, field, err := classifyMetaKey(key, schema)
	if err != nil {
		return err
	}
	if class == metaKeyUnknown {
		return unknownMetaKeyError(key, schema)
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
		// Quarantined row but Parse succeeded (race/lag): still ride the token.
		stderrln(c, token.Line(token.ParseError, fmt.Sprintf("%s: %s", p.ID, p.ParseMsg)))
	}

	out, err := metaGetValue(m, key, class, field)
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

func runMetaMutate(app *App, c *cobra.Command, op metaOp, idArg, key, valueArg, scopeFlag string) error {
	key = frontmatter.CanonicalMetaKey(key)

	form, ok := parseIDArg(idArg)
	if !ok {
		return usageErrorf("%q is not a valid ticket id", idArg)
	}

	value, err := loadMetaValue(c, valueArg)
	if err != nil {
		return err
	}
	if strings.Contains(value, "\n") {
		return usageErrorf("meta %s value must not contain embedded newlines", op)
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

	lock, err := scopefile.AcquireLock(dir)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()

	ctx := c.Context()
	res, err := e.reconcileResult(single(scope, dir))
	if err != nil {
		return err
	}
	if err := refuseUnusableScope(res, scope, dir); err != nil {
		return err
	}
	schema := res.Schema(scope)
	autoCommit := schemaAutoCommit(schema)
	root, hasRoot := scopefile.GitRoot(dir)
	if err := checkMidRebase(ctx, scope, autoCommit, root, hasRoot); err != nil {
		return err
	}
	e.printWarnings(c, res.Warnings)

	class, field, err := classifyMetaKey(key, schema)
	if err != nil {
		return err
	}
	if class == metaKeyUnknown {
		return unknownMetaKeyError(key, schema)
	}
	if class == metaKeyImmutable {
		return immutableMetaKeyError(key)
	}
	switch op {
	case metaOpSet:
		if class != metaKeyScalar {
			return usageErrorf("key %q is multi-value; use meta add/rm, not set", key)
		}
	case metaOpAdd, metaOpRm:
		if class != metaKeyMulti {
			return usageErrorf("key %q is scalar; use meta set, not %s", key, op)
		}
	}

	p, err := e.resolveWriteRow(scope, idArg, form)
	if err != nil {
		return err
	}

	if key == frontmatter.KeyDepends || key == frontmatter.KeyRelated {
		value, err = normaliseEdgeValue(scope, value)
		if err != nil {
			return err
		}
	}
	if op == metaOpAdd && key == frontmatter.KeyDepends {
		if err := e.checkDependsAdd(p.ID, scope, value); err != nil {
			return err
		}
	}

	var (
		notifyTagNew bool
		preWriteTags map[string]struct{}
	)
	if op == metaOpAdd && key == frontmatter.KeyTags {
		rows, err := e.db.ScopeTickets(scope)
		if err != nil {
			return err
		}
		notifyTagNew = true
		preWriteTags = index.TagMembership(rows)
	}

	m, body, err := readTicketFile(p.Path)
	if err != nil {
		return err
	}
	if err := applyMetaMutation(m, op, key, value, class, field); err != nil {
		return err
	}
	if err := writeTicketFile(p.Path, m, body); err != nil {
		return err
	}
	if err := e.rec.SyncPaths(scope, writtenPaths(p.Path, "")); err != nil {
		return err
	}

	message := fmt.Sprintf("tk: %s meta %s %s", p.ID, op, key)
	if err := e.completeStateDurability(ctx, c, scope, dir, autoCommit, message, p.Path, "", root, hasRoot); err != nil {
		return err
	}

	out, err := absPath(p.Path)
	if err != nil {
		return err
	}
	stdoutln(c, out)
	if notifyTagNew {
		noticeNewTag(c, value, preWriteTags)
	}
	return nil
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

// normaliseEdgeValue expands short ids in the subject scope; malformed → exit 2.
func normaliseEdgeValue(subjectScope, value string) (string, error) {
	form, ok := parseIDArg(value)
	if !ok {
		return "", usageErrorf("%q is not a valid ticket id", value)
	}
	if form == idShort {
		return subjectScope + "-" + value, nil
	}
	return value, nil
}

// checkDependsAdd: self/same-scope missing hard refuse; cross-scope needs a live row.
func (e *engine) checkDependsAdd(subjectID, subjectScope, targetFull string) error {
	if targetFull == subjectID {
		return fmt.Errorf("%s", token.Line(token.DependsSelf,
			fmt.Sprintf("%s depends on itself — remove the self-edge", subjectID)))
	}
	targetScope := scopeOfFullID(targetFull)
	if targetScope == subjectScope {
		ok, err := e.nonQuarantinedExists(subjectScope, targetFull)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%s", token.Line(token.DependsDangling,
				fmt.Sprintf("%s depends on %s which has no ticket in this scope", subjectID, targetFull)))
		}
		return nil
	}

	entry, registered := e.reg.Scopes[targetScope]
	if !registered {
		return fmt.Errorf("%s", token.Line(token.DependsUnresolvable,
			fmt.Sprintf("%s depends on %s which cannot be resolved here", subjectID, targetFull)))
	}
	res, err := e.reconcileResult(single(targetScope, entry.Dir))
	if err != nil {
		return err
	}
	if res.Unreachable[targetScope] {
		return fmt.Errorf("%s", token.Line(token.DependsUnresolvable,
			fmt.Sprintf("%s depends on %s which cannot be resolved here", subjectID, targetFull)))
	}
	ok, err := e.nonQuarantinedExists(targetScope, targetFull)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s", token.Line(token.DependsUnresolvable,
			fmt.Sprintf("%s depends on %s which cannot be resolved here", subjectID, targetFull)))
	}
	return nil
}

// nonQuarantinedExists: quarantine-only counts as absent for depends write checks.
func (e *engine) nonQuarantinedExists(scope, fullID string) (bool, error) {
	rows, err := e.db.TicketsByID(scope, fullID)
	if err != nil {
		return false, err
	}
	for _, r := range rows {
		if !r.ParseError {
			return true, nil
		}
	}
	return false, nil
}

func classifyMetaKey(key string, schema *scopeconfig.Schema) (metaKeyClass, scopeconfig.Field, error) {
	switch key {
	case frontmatter.KeyID, frontmatter.KeyStatus, frontmatter.KeyOrder,
		frontmatter.KeyCreated, frontmatter.KeyStatusConflict:
		return metaKeyImmutable, scopeconfig.Field{}, nil
	case frontmatter.KeySummary:
		return metaKeyScalar, scopeconfig.Field{Type: scopeconfig.FieldString}, nil
	case frontmatter.KeyDepends, frontmatter.KeyRelated, frontmatter.KeyTags, frontmatter.KeyLinks:
		return metaKeyMulti, scopeconfig.Field{Type: scopeconfig.FieldStrings}, nil
	}
	if schema != nil {
		if f, ok := schema.Fields[key]; ok {
			switch f.Type {
			case scopeconfig.FieldString, scopeconfig.FieldInt, scopeconfig.FieldBool:
				return metaKeyScalar, f, nil
			case scopeconfig.FieldStrings:
				return metaKeyMulti, f, nil
			default:
				return metaKeyUnknown, scopeconfig.Field{}, fmt.Errorf("custom field %q has unsupported type %q", key, f.Type)
			}
		}
	}
	return metaKeyUnknown, scopeconfig.Field{}, nil
}

func unknownMetaKeyError(key string, schema *scopeconfig.Schema) error {
	return usageErrorf("unknown frontmatter key %q; known keys: %s", key, strings.Join(metaKnownKeys(schema), ", "))
}

func immutableMetaKeyError(key string) error {
	switch key {
	case frontmatter.KeyStatus:
		return usageErrorf("key %q is immutable via meta; use tk mark", key)
	case frontmatter.KeyOrder:
		return usageErrorf("key %q is immutable via meta; use tk reorder", key)
	default:
		return usageErrorf("key %q is immutable via meta", key)
	}
}

func metaKnownKeys(schema *scopeconfig.Schema) []string {
	out := append([]string(nil), builtinMetaKeyOrder...)
	if schema == nil || len(schema.Fields) == 0 {
		return out
	}
	customs := make([]string, 0, len(schema.Fields))
	for name := range schema.Fields {
		customs = append(customs, name)
	}
	sort.Strings(customs)
	return append(out, customs...)
}

func metaGetValue(m *frontmatter.Model, key string, class metaKeyClass, field scopeconfig.Field) (string, error) {
	switch key {
	case frontmatter.KeyID:
		return m.ID, nil
	case frontmatter.KeyStatus:
		return m.Status, nil
	case frontmatter.KeyOrder:
		return m.Order, nil
	case frontmatter.KeyCreated:
		return m.Created, nil
	case frontmatter.KeySummary:
		return m.Summary, nil
	case frontmatter.KeyDepends:
		return joinLines(m.Depends), nil
	case frontmatter.KeyRelated:
		return joinLines(m.Related), nil
	case frontmatter.KeyTags:
		return joinLines(m.Tags), nil
	case frontmatter.KeyLinks:
		return joinLines(m.Links), nil
	case frontmatter.KeyStatusConflict:
		return joinLines(m.StatusConflict), nil
	}
	v, ok := customValue(m, key)
	if !ok {
		return "", nil
	}
	if class == metaKeyMulti || field.Type == scopeconfig.FieldStrings {
		list, err := anyStringList(v)
		if err != nil {
			return "", fmt.Errorf("custom field %q: %w", key, err)
		}
		return joinLines(list), nil
	}
	return formatScalar(v), nil
}

func applyMetaMutation(m *frontmatter.Model, op metaOp, key, value string, class metaKeyClass, field scopeconfig.Field) error {
	if op == metaOpSet {
		return applyMetaSet(m, key, value, field)
	}
	return applyMetaListOp(m, op, key, value, field)
}

func applyMetaSet(m *frontmatter.Model, key, value string, field scopeconfig.Field) error {
	if key == frontmatter.KeySummary {
		m.Summary = value
		return nil
	}
	if value == "" {
		removeCustom(m, key)
		return nil
	}
	parsed, err := parseScalarValue(field, value)
	if err != nil {
		return err
	}
	setCustom(m, key, parsed)
	return nil
}

func applyMetaListOp(m *frontmatter.Model, op metaOp, key, value string, field scopeconfig.Field) error {
	var list *[]string
	switch key {
	case frontmatter.KeyDepends:
		list = &m.Depends
	case frontmatter.KeyRelated:
		list = &m.Related
	case frontmatter.KeyTags:
		list = &m.Tags
	case frontmatter.KeyLinks:
		list = &m.Links
	default:
		cur, _ := customValue(m, key)
		existing, err := anyStringList(cur)
		if err != nil {
			return usageErrorf("custom field %q is not a string list", key)
		}
		next, err := mutateStringList(existing, op, value, field)
		if err != nil {
			return err
		}
		if len(next) == 0 {
			removeCustom(m, key)
		} else {
			setCustom(m, key, next)
		}
		return nil
	}
	next, err := mutateStringList(*list, op, value, field)
	if err != nil {
		return err
	}
	*list = next
	return nil
}

func mutateStringList(list []string, op metaOp, value string, field scopeconfig.Field) ([]string, error) {
	switch op {
	case metaOpAdd:
		if len(field.Values) > 0 && !stringIn(field.Values, value) {
			return nil, usageErrorf("value %q is outside the declared values for this field", value)
		}
		for _, e := range list {
			if e == value {
				return list, nil
			}
		}
		return append(list, value), nil
	case metaOpRm:
		out := make([]string, 0, len(list))
		removed := false
		for _, e := range list {
			if !removed && e == value {
				removed = true
				continue
			}
			out = append(out, e)
		}
		return out, nil
	default:
		return list, fmt.Errorf("internal: list op %v", op)
	}
}

func parseScalarValue(field scopeconfig.Field, value string) (any, error) {
	switch field.Type {
	case scopeconfig.FieldString:
		if len(field.Values) > 0 && !stringIn(field.Values, value) {
			return nil, usageErrorf("value %q is outside the declared values for this field", value)
		}
		return value, nil
	case scopeconfig.FieldInt:
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return nil, usageErrorf("%q is not a valid integer", value)
		}
		return n, nil
	case scopeconfig.FieldBool:
		switch value {
		case "true":
			return true, nil
		case "false":
			return false, nil
		default:
			return nil, usageErrorf("%q is not a valid bool (want true or false)", value)
		}
	default:
		return nil, usageErrorf("cannot set field of type %q", field.Type)
	}
}

func customValue(m *frontmatter.Model, key string) (any, bool) {
	for _, f := range m.Custom {
		if f.Key == key {
			return f.Value, true
		}
	}
	return nil, false
}

func setCustom(m *frontmatter.Model, key string, value any) {
	for i := range m.Custom {
		if m.Custom[i].Key == key {
			m.Custom[i].Value = value
			return
		}
	}
	m.Custom = append(m.Custom, frontmatter.Field{Key: key, Value: value})
}

func removeCustom(m *frontmatter.Model, key string) {
	out := make([]frontmatter.Field, 0, len(m.Custom))
	for _, f := range m.Custom {
		if f.Key == key {
			continue
		}
		out = append(out, f)
	}
	if len(out) == 0 {
		m.Custom = nil
		return
	}
	m.Custom = out
}

func anyStringList(v any) ([]string, error) {
	if v == nil {
		return nil, nil
	}
	switch x := v.(type) {
	case []string:
		return append([]string(nil), x...), nil
	case []any:
		out := make([]string, len(x))
		for i, e := range x {
			out[i] = fmt.Sprint(e)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("expected a list, got %T", v)
	}
}

func formatScalar(v any) string {
	switch x := v.(type) {
	case bool:
		if x {
			return "true"
		}
		return "false"
	case string:
		return x
	case nil:
		return ""
	default:
		return fmt.Sprint(x)
	}
}

func joinLines(items []string) string {
	if len(items) == 0 {
		return ""
	}
	return strings.Join(items, "\n") + "\n"
}

func stringIn(list []string, s string) bool {
	for _, e := range list {
		if e == s {
			return true
		}
	}
	return false
}
