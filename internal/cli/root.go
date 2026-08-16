// Package cli is tk's Cobra command tree, exit codes, signals, and output rules.
// Handlers return errors; cmd/tk/main.go is the sole place that formats and exits.
package cli

import (
	"fmt"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"github.com/p3bot/agentdex"
	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/scopeadmin"
	"github.com/p3bot/tk/internal/xdg"
)

// Build-time variables set via ldflags (same pattern as start).
var (
	cliVersion = "dev"
	repoURL    = "https://github.com/p3bot/tk"
)

var versionTemplate = fmt.Sprintf(`tk version %s
%s
%s/issues/new
`, cliVersion, repoURL, repoURL)

// App carries process-wide CUE context and XDG config/state directories.
type App struct {
	Ctx       *cue.Context
	ConfigDir string
	StateDir  string
	// AgentdexOpts are extra Open options for skill install/list/uninstall
	// (tests inject catalog dir, look path, env; production leaves nil).
	AgentdexOpts []agentdex.Option
}

func (a *App) admin() *scopeadmin.Admin {
	return scopeadmin.New(a.Ctx, a.ConfigDir)
}

// Execute builds the command tree and runs it.
func Execute() error {
	if !supportedOS() {
		return &ExitError{Code: exitFailure, Err: fmt.Errorf("tk supports macOS and Linux only; this operating system is unsupported")}
	}

	configDir, err := xdg.ConfigDir()
	if err != nil {
		return err
	}
	stateDir, err := xdg.StateDir()
	if err != nil {
		return err
	}
	app := &App{Ctx: cuecontext.New(), ConfigDir: configDir, StateDir: stateDir}

	root := newRootCmd(app)
	ctx, stop := signalContext()
	defer stop()
	return root.ExecuteContext(ctx)
}

// Root help group IDs/Titles (AddGroup order = section order).
const (
	groupWorkID     = "work"
	groupWorkTitle  = "Work:"
	groupBoardID    = "board"
	groupBoardTitle = "Board:"
	groupAdminID    = "admin"
	groupAdminTitle = "Admin:"
)

// newRootCmd builds a fresh tree so flag state cannot leak across test invocations.
func newRootCmd(app *App) *cobra.Command {
	root := &cobra.Command{
		Use:           "tk",
		Short:         "Agent ticket management CLI",
		Long:          "tk tracks feature work as plain markdown files, one ticket per file.",
		Version:       cliVersion,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          usageArgs(cobra.NoArgs),
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	root.SetVersionTemplate(versionTemplate)
	// Flag-parse failures → exit 2.
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &ExitError{Code: exitUsage, Err: err}
	})

	// Locked within-group order (mini workflow), not pure alpha.
	cobra.EnableCommandSorting = false

	root.AddGroup(
		&cobra.Group{ID: groupWorkID, Title: groupWorkTitle},
		&cobra.Group{ID: groupBoardID, Title: groupBoardTitle},
		&cobra.Group{ID: groupAdminID, Title: groupAdminTitle},
	)

	create := newCreateCmd(app)
	get := newGetCmd(app)
	edit := newEditCmd(app)
	mark := newMarkCmd(app)
	reorder := newReorderCmd(app)
	next := newNextCmd(app)
	list := newListCmd(app)
	status := newStatusCmd(app)
	meta := newMetaCmd(app)
	deps := newDepsCmd(app)
	search := newSearchCmd(app)
	query := newQueryCmd(app)
	lens := newLensCmd(app)
	me := newMeCmd(app)
	note := newNoteCmd(app)
	tags := newTagsCmd(app)
	scope := newScopeCmd(app)
	sync := newSyncCmd(app)
	doctor := newDoctorCmd(app)
	skill := newSkillCmd(app)

	create.GroupID = groupWorkID
	get.GroupID = groupWorkID
	edit.GroupID = groupWorkID
	mark.GroupID = groupWorkID
	reorder.GroupID = groupWorkID
	next.GroupID = groupWorkID

	list.GroupID = groupBoardID
	status.GroupID = groupBoardID
	meta.GroupID = groupBoardID
	deps.GroupID = groupBoardID
	search.GroupID = groupBoardID
	query.GroupID = groupBoardID
	lens.GroupID = groupBoardID
	me.GroupID = groupBoardID
	note.GroupID = groupBoardID
	tags.GroupID = groupBoardID

	scope.GroupID = groupAdminID
	sync.GroupID = groupAdminID
	doctor.GroupID = groupAdminID
	skill.GroupID = groupAdminID

	root.AddCommand(
		create, get, edit, mark, reorder, next,
		list, status, meta, deps, search, query, lens, me, note, tags,
		scope, sync, doctor, skill,
	)
	return root
}

func usageArgs(v cobra.PositionalArgs) cobra.PositionalArgs {
	return func(c *cobra.Command, args []string) error {
		if err := v(c, args); err != nil {
			return &ExitError{Code: exitUsage, Err: err}
		}
		return nil
	}
}

func stdoutln(c *cobra.Command, s string) {
	fmt.Fprintln(c.OutOrStdout(), s)
}

func stderrln(c *cobra.Command, s string) {
	fmt.Fprintln(c.ErrOrStderr(), s)
}
