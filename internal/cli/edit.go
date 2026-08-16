package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

func newEditCmd(app *App) *cobra.Command {
	var scope string
	cmd := &cobra.Command{
		Use:   "edit <id> [--scope S]",
		Short: "Open a ticket's file in $EDITOR",
		Long: "Resolve an id to its path and open it in $EDITOR for human editing. It prints\n" +
			"nothing on success (not a path-hand-off verb), never rewrites frontmatter, and\n" +
			"never self-commits — an editor save is an ordinary direct edit. It may open a\n" +
			"parse_error path for repair.",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(c *cobra.Command, args []string) error {
			return runEdit(app, c, args[0], scope)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "ambient scope for a short id")
	return cmd
}

func runEdit(app *App, c *cobra.Command, idArg, scope string) error {
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

	fields, err := editorArgv("tk edit")
	if err != nil {
		return err
	}
	return runEditor(fields, p.Path)
}

// editorArgv splits $EDITOR (which may carry flags) into program + args.
func editorArgv(verb string) ([]string, error) {
	fields := strings.Fields(os.Getenv("EDITOR"))
	if len(fields) == 0 {
		return nil, fmt.Errorf("$EDITOR is not set — set it to your editor to use %s", verb)
	}
	return fields, nil
}

// runEditor shares stdio so the human-in-the-loop verb can use the terminal.
func runEditor(fields []string, path string) error {
	args := append(append([]string(nil), fields[1:]...), path)
	ed := exec.Command(fields[0], args...)
	ed.Stdin, ed.Stdout, ed.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := ed.Run(); err != nil {
		return fmt.Errorf("editor exited with an error: %w", err)
	}
	return nil
}
