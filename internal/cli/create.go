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
		edit  bool
	)
	cmd := &cobra.Command{
		Use:   "create <title> [status] [--scope S] [--tag T]... [--edit]",
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
			"Post-create tag edits remain meta add|remove. --edit is human $EDITOR convenience:\n" +
			"after a successful write it opens that path with the same editor contract as\n" +
			"tk edit (process stdio; unset $EDITOR or a non-zero editor exit is non-zero and\n" +
			"does not roll back the scaffold). create reserves the id and never self-commits\n" +
			"in any mode; an editor save is an ordinary direct file edit. Git durability is\n" +
			"the next tk sync (auto-commit) or host commit.",
		Args: rangeArgs(1, 2, "<title>"),
		RunE: func(c *cobra.Command, args []string) error {
			st := ""
			if len(args) == 2 {
				st = args[1]
			}
			return runCreate(app, c, args[0], st, scope, tags, edit)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "scope to create in (defaults to ambient)")
	cmd.Flags().StringArrayVar(&tags, "tag", nil, "scaffold tag (repeatable; free-form)")
	cmd.Flags().BoolVar(&edit, "edit", false, "open the new ticket in $EDITOR after a successful write")
	return cmd
}

func runCreate(app *App, c *cobra.Command, titleArg, statusArg, scopeFlag string, tagArgs []string, edit bool) error {
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
	if emitErr := emitWriteResult(c, res, err); emitErr != nil {
		return emitErr
	}
	if !edit {
		return nil
	}
	path := res.Path
	e.close()
	fields, err := editorArgv("tk create --edit")
	if err != nil {
		return err
	}
	return runEditor(fields, path)
}
