package cli

import (
	"crypto/rand"
	"fmt"
	"strings"
	"time"

	"github.com/p3bot/tk/internal/scopefile"

	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/frontmatter"
	"github.com/p3bot/tk/internal/id"
	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/order"
	"github.com/p3bot/tk/internal/slug"
	"github.com/p3bot/tk/internal/status"
)

func newCreateCmd(app *App) *cobra.Command {
	var (
		scope string
		tags  []string
	)
	cmd := &cobra.Command{
		Use:   "create <title> [status] [--scope S] [--tag T]...",
		Short: "Scaffold a new ticket (frontmatter + H1) and print its path",
		Long: "Mint an id, write a scaffold — built-in frontmatter with an appended order\n" +
			"key and a single # <title> H1 whose slug is frozen from the title — and print\n" +
			"the cleaned absolute path for the agent to fill the body. The default status is\n" +
			"draft; an optional second positional sets any known status (a terminal status\n" +
			"writes under archive/). Repeatable --tag T sets scaffold tags (deduped, first-seen\n" +
			"order); omit --tag to leave tags absent. Each board-new tag emits on stderr after\n" +
			"a successful write (soft; exit 0):\n" +
			"  tag_new: \"<t>\" is new to this scope\n" +
			"Post-create tag edits remain meta add|rm. create reserves the id and never\n" +
			"self-commits in any mode; git durability is the next tk sync (auto-commit) or\n" +
			"host commit.",
		Args: usageArgs(cobra.RangeArgs(1, 2)),
		RunE: func(c *cobra.Command, args []string) error {
			st := ""
			if len(args) == 2 {
				st = args[1]
			}
			return runCreate(app, c, args[0], st, scope, tags)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "scope to create in (defaults to ambient)")
	cmd.Flags().StringArrayVar(&tags, "tag", nil, "scaffold tag (repeatable; free-form)")
	return cmd
}

func runCreate(app *App, c *cobra.Command, titleArg, statusArg, scopeFlag string, tagArgs []string) error {
	title := strings.TrimSpace(titleArg)
	if title == "" {
		return usageErrorf("create needs a non-empty title")
	}
	tags, err := uniqueCreateTags(tagArgs)
	if err != nil {
		return err
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
	custom := schemaCustom(schema)

	newStatus := status.Draft
	if statusArg != "" {
		newStatus = statusArg
	}
	if !status.IsKnown(newStatus, custom) {
		return usageErrorf("%q is not a known status for scope %q", newStatus, scope)
	}

	autoCommit := schemaAutoCommit(schema)
	root, hasRoot := scopefile.GitRoot(dir)
	if err := checkMidRebase(ctx, scope, autoCommit, root, hasRoot); err != nil {
		return err
	}
	e.printWarnings(c, res.Warnings)

	rows, err := e.db.ScopeTickets(scope)
	if err != nil {
		return err
	}
	preWriteTags, err := e.db.ScopeTagMembership(scope)
	if err != nil {
		return err
	}

	shortID, err := mintUnusedID(rows)
	if err != nil {
		return err
	}
	fullID := scope + "-" + shortID

	orderKey, err := order.KeyBetween(maxValidOrder(rows), "")
	if err != nil {
		return fmt.Errorf("compute append order for %s: %w", fullID, err)
	}

	model := &frontmatter.Model{
		ID:      fullID,
		Status:  newStatus,
		Order:   orderKey,
		Tags:    tags,
		Created: time.Now().Format(time.RFC3339),
	}
	interior, err := frontmatter.Serialize(model)
	if err != nil {
		return err
	}
	file := frontmatter.Compose(interior, []byte("# "+title+"\n"))

	terminal := status.IsTerminal(newStatus, custom)
	base := fullID + "-" + slug.Slugify(title) + ".md"
	target, err := terminalLocation(dir, base, terminal)
	if err != nil {
		return err
	}

	if err := atomicWrite(target, file); err != nil {
		return err
	}
	if err := e.rec.SyncPaths(scope, writtenPaths(target, "")); err != nil {
		return err
	}

	e.createDurability(ctx, c, dir, autoCommit, terminal, fullID, root, hasRoot)

	out, err := absPath(target)
	if err != nil {
		return err
	}
	stdoutln(c, out)
	for _, tag := range tags {
		noticeNewTag(c, tag, preWriteTags)
	}
	return nil
}

// uniqueCreateTags rejects empty --tag values and dedupes preserving first-seen order.
func uniqueCreateTags(tagArgs []string) ([]string, error) {
	if len(tagArgs) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(tagArgs))
	out := make([]string, 0, len(tagArgs))
	for _, tag := range tagArgs {
		if tag == "" {
			return nil, usageErrorf("create --tag must be non-empty")
		}
		if _, dup := seen[tag]; dup {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
	}
	return out, nil
}

// mintUnusedID redraws until unused, including parse_error rows (id from filename).
func mintUnusedID(rows []*index.Ticket) (string, error) {
	taken := make(map[string]struct{}, len(rows))
	for _, p := range rows {
		if p.ShortID != "" {
			taken[p.ShortID] = struct{}{}
		}
	}
	for {
		s, err := id.Mint(rand.Reader)
		if err != nil {
			return "", fmt.Errorf("mint id: %w", err)
		}
		if _, used := taken[s]; !used {
			return s, nil
		}
	}
}
