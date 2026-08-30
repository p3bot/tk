package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/writeengine"
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
			"order); omit --tag to leave tags absent. After a successful write, stderr includes:\n" +
			"  <id> scaffolded with frontmatter\n" +
			"Each board-new tag also emits (soft; exit 0):\n" +
			"  tag_new: \"<t>\" is new to this scope\n" +
			"Post-create tag edits remain meta add|rm. create reserves the id and never\n" +
			"self-commits in any mode; git durability is the next tk sync (auto-commit) or\n" +
			"host commit.",
		Args: rangeArgs(1, 2, "<title>"),
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
	tags, err := writeengine.NormalizeCreateTags(tagArgs)
	if err != nil {
		return mapWriteErr(err)
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
	res, err := writeengine.Create(e.writeDeps(c.Context()), writeengine.CreateInput{
		Scope:  resolved.Name,
		Dir:    resolved.Entry.Dir,
		Title:  title,
		Status: statusArg,
		Tags:   tags,
	})
	return emitWriteResult(c, res, err)
}
