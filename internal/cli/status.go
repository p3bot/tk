package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/p3bot/tk/internal/scopefile"

	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/depgate"
	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/scopeadmin"
	"github.com/p3bot/tk/internal/scopeconfig"
	"github.com/p3bot/tk/internal/status"
)

// statusKeys is the locked stdout key order for tk status (pad + single tab).
var statusKeys = []string{
	"scope",
	"dir",
	"resolved",
	"mode",
	"lens",
	"me",
	"note",
	"total",
	"todo",
	"in-progress",
	"review",
	"blocked",
	"draft",
	"backlog",
	"done",
	"cancelled",
	"next",
	"claimed",
	"blocked_ids",
	"dangling",
	"integrity",
	"uncommitted",
}

// statusKeyWidth is the longest locked key (shared with tests).
var statusKeyWidth = maxStatusKeyWidth()

func maxStatusKeyWidth() int {
	w := 0
	for _, k := range statusKeys {
		if n := len(k); n > w {
			w = n
		}
	}
	return w
}

func newStatusCmd(app *App) *cobra.Command {
	var scope string
	cmd := &cobra.Command{
		Use:   "status [key] [--scope S]",
		Short: "Scope pulse: key/value counts, next, claimed, integrity",
		Long: "Print a pure-read orientation block for one scope as parse-stable key\\tvalue\n" +
			"lines (no header). Keys are left-justified to a fixed column (longest key width)\n" +
			"before the tab so values align for humans; parse by splitting on the first tab\n" +
			"and trimming trailing spaces from the key. Empty values still emit the key line\n" +
			"(`key\\t` with padding) so agents can rely on key presence. Exit 0 whenever\n" +
			"ambient resolve succeeds — including when next is empty (an empty queue is a\n" +
			"pulse value, not a failure).\n" +
			"\n" +
			"Optional [key]: with exactly one positional matching a locked pulse key, print\n" +
			"only that field's bare value (no key name, padding, or tab), followed by a\n" +
			"newline when non-empty. Empty values write empty stdout and exit 0. Unknown\n" +
			"keys are usage exit 2 listing the locked catalogue. Two or more positionals\n" +
			"are usage exit 2. The attribute path builds the same pulse map as the full\n" +
			"dashboard (same reconcile, next selection, counts, integrity, stderr tokens).\n" +
			"\n" +
			"Locked keys (order fixed): scope, dir, resolved, mode, lens, me, note, total,\n" +
			"todo, in-progress, review, blocked, draft, backlog, done, cancelled, next,\n" +
			"claimed, blocked_ids, dangling, integrity, uncommitted.\n" +
			"\n" +
			"resolved is how the scope was chosen: flag (--scope), env (TK_SCOPE), or cwd\n" +
			"(longest-prefix code-root).\n" +
			"\n" +
			"mode is one of tk-driven | repo-driven | plain-files only. With a known schema:\n" +
			"autoCommit true → tk-driven (with or without a git-root — the planned no-repo\n" +
			"layout stays tk-driven); autoCommit false + git-root → repo-driven; false and\n" +
			"no git-root → plain-files. When the schema is unusable, mode is plain-files and\n" +
			"config_unparseable: rides stderr — do not read plain-files as healthy host files\n" +
			"without checking stderr. uncommitted is non-zero only in repo-driven mode.\n" +
			"\n" +
			"me is the stored full ticket id of this machine's current-ticket pointer, or\n" +
			"empty if unset. It is never a path.\n" +
			"\n" +
			"note is the cleaned absolute path of this machine's default note\n" +
			"(notes/<slug>.md from `tk note use`, or notes/default.md when unset), whether\n" +
			"or not the file exists. --name and a positional slug stay one-shot selectors.\n" +
			"Read and write notes with `tk note`.\n" +
			"\n" +
			"The active lens filters the working board (non-terminal status counts, claimed,\n" +
			"blocked_ids) the same way bare list and tk next do. total is the full-scope\n" +
			"count of parseable tickets (dir root and archive/, every status including\n" +
			"backlog and terminals) and ignores the lens. Working-board built-in counts\n" +
			"include backlog (unlike bare list). Terminal tallies (done, cancelled) are full-scope\n" +
			"including archive/ and ignore the lens. Identity and health keys (dangling,\n" +
			"integrity, uncommitted) ignore the lens.\n" +
			"\n" +
			"next reuses tk next selection (reconcileClosure + depends gate + lens) but never\n" +
			"surfaces next's empty-queue diagnostic: empty next still exits 0 with the full\n" +
			"key block. dangling is the edge-count of same-scope depends targets missing a\n" +
			"ticket in this scope (matches doctor's per-edge depends_dangling findings).\n" +
			"integrity is ok or issues for the ambient scope only (parse_error rows,\n" +
			"duplicate_id, equal_order, archive layout drift) — not soft doctor classes or\n" +
			"depended-on scopes from the next closure.\n" +
			"\n" +
			"To change a ticket's status, use `tk mark <status> <id> [id...]`.",
		Args: maxArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			key := ""
			if len(args) == 1 {
				key = args[0]
			}
			return runStatus(app, c, scope, key)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "scope to pulse (defaults to ambient; wins over ambient)")
	return cmd
}

func knownStatusKey(key string) bool {
	for _, k := range statusKeys {
		if k == key {
			return true
		}
	}
	return false
}

func runStatus(app *App, c *cobra.Command, scopeFlag, key string) error {
	if key != "" && !knownStatusKey(key) {
		return usageErrorf("unknown status key %q; known keys: %s", key, strings.Join(statusKeys, ", "))
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
	absDir, err := absPath(dir)
	if err != nil {
		return err
	}

	// Same closure + gate as tk next for cross-scope depends freshness.
	res, targets, err := e.reconcileClosure(c, scope, dir)
	if err != nil {
		return err
	}
	gate, err := depgate.Load(e.gateDeps(), res, targets)
	if err != nil {
		return err
	}

	schema := res.Schema(scope)
	lens := e.reg.Lens[scope]

	root, hasRoot := scopefile.GitRoot(dir)
	mode := statusMode(schema, res.ConfigErrs[scope] != nil, hasRoot)

	noteSlug, err := effectiveNoteSlug(e, scope)
	if err != nil {
		return err
	}
	notePath, err := absPath(scopefile.NoteFile(dir, noteSlug))
	if err != nil {
		return err
	}

	pulse := map[string]string{
		"scope":    scope,
		"dir":      absDir,
		"resolved": resolved.Source,
		"mode":     mode,
		"lens":     strings.Join(lens, " "),
		"me":       e.reg.Me[scope],
		"note":     notePath,
	}

	sp, err := e.db.ScopePulse(scope, lens)
	if err != nil {
		return err
	}

	candidates, err := e.db.NextCandidates(scope)
	if err != nil {
		return err
	}
	// Same next walk as unclaimed tk next; empty next is still a pulse value.
	sel := gate.SelectNext(candidates, lens, false)
	nextID := ""
	if sel.Chosen != nil {
		nextID = sel.Chosen.ID
	}

	dangling, err := e.db.SameScopeDanglingDependsCount(scope)
	if err != nil {
		return err
	}
	integrity, err := ambientIntegrity(e, scope, schema)
	if err != nil {
		return err
	}
	uncommitted := 0
	if mode == scopeadmin.ModeRepoDriven {
		uncommitted = scopefile.CountAllowlistedDirty(c.Context(), dir, root, hasRoot)
	}

	pulse["total"] = strconv.Itoa(sp.Total)
	pulse["todo"] = strconv.Itoa(sp.Todo)
	pulse["review"] = strconv.Itoa(sp.Review)
	pulse["in-progress"] = strconv.Itoa(sp.InProgress)
	pulse["blocked"] = strconv.Itoa(sp.Blocked)
	pulse["draft"] = strconv.Itoa(sp.Draft)
	pulse["backlog"] = strconv.Itoa(sp.Backlog)
	pulse["done"] = strconv.Itoa(sp.Done)
	pulse["cancelled"] = strconv.Itoa(sp.Cancelled)
	pulse["next"] = nextID
	pulse["claimed"] = joinIDs(sp.Claimed)
	pulse["blocked_ids"] = joinIDs(sp.BlockedIDs)
	pulse["dangling"] = strconv.Itoa(dangling)
	pulse["integrity"] = integrity
	pulse["uncommitted"] = strconv.Itoa(uncommitted)

	writeNextDiagnostics(c, sel)
	if key != "" {
		if v := pulse[key]; v != "" {
			stdoutln(c, v)
		}
		return nil
	}
	for _, k := range statusKeys {
		stdoutln(c, fmt.Sprintf("%-*s\t%s", statusKeyWidth, k, pulse[k]))
	}
	return nil
}

// statusMode: unusable schema → plain-files (never guess repo-driven).
func statusMode(schema *scopeconfig.Schema, configUnusable bool, hasRoot bool) string {
	if configUnusable || schema == nil {
		return scopeadmin.ModePlainFiles
	}
	return scopeadmin.DeriveMode(schema.AutoCommit, hasRoot)
}

// ambientIntegrity: parse_error/duplicate/equal_order/archive drift → issues.
func ambientIntegrity(e *engine, scope string, schema *scopeconfig.Schema) (string, error) {
	scopes := []string{scope}
	n, err := e.db.ParseErrorCount(scopes)
	if err != nil {
		return "", err
	}
	if n > 0 {
		return "issues", nil
	}
	dups, err := e.db.DuplicateIDs(scopes)
	if err != nil {
		return "", err
	}
	if len(dups) > 0 {
		return "issues", nil
	}
	eq, err := e.db.EqualOrders(scopes)
	if err != nil {
		return "", err
	}
	if len(eq) > 0 {
		return "issues", nil
	}
	drift, err := e.db.HasArchiveDrift(scope, status.TerminalNames(schema.CustomStatuses()))
	if err != nil {
		return "", err
	}
	if drift {
		return "issues", nil
	}
	return "ok", nil
}

func joinIDs(rows []*index.Ticket) string {
	if len(rows) == 0 {
		return ""
	}
	ids := make([]string, len(rows))
	for i, p := range rows {
		ids[i] = p.ID
	}
	return strings.Join(ids, " ")
}
