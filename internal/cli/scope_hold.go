package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/scopeconfig"
	"github.com/p3bot/tk/internal/scopefile"
	"github.com/p3bot/tk/internal/token"
)

func newScopeHoldCmd(app *App) *cobra.Command {
	var scope string
	cmd := &cobra.Command{
		Use:   "hold [off|soft|hard [reason]] [--scope S]",
		Short: "Read or set this scope's hold type and reason",
		Long: "Read or rewrite hold in tk.cue, per scope: scopes that share a git-root may\n" +
			"disagree. Bare invocation prints off, soft, or hard.\n\n" +
			"soft is for changes larger than a single ticket: no ticket enters in-progress,\n" +
			"and other ticket work continues. hard is a complete stop: tk refuses ticket and\n" +
			"design writes, and agents stop code work too.\n\n" +
			"soft or hard without a reason keeps any existing reason. With a reason, both\n" +
			"are updated. Only off clears a reason. A reason has at most 200 characters and\n" +
			"no control characters, surrounding space is trimmed, and more than one word\n" +
			"needs quotes. A reason that starts with a dash needs --, as in\n" +
			"  tk scope hold soft -- \"-1 on this\"\n" +
			"A reason is committed and pushed with the scope, so keep it free of anything\n" +
			"private.\n\n" +
			"A rewrite prints the tk.cue path and self-commits on a tk-driven scope. Already\n" +
			"in that state is no rewrite: nothing on stdout, and hold is already <state> on\n" +
			"stderr. The scope_hold: line reports the state found when the scope resolved,\n" +
			"before any write, so hold off on a held scope prints it, hold soft on an off\n" +
			"scope does not, and an update shows the previous type and reason.\n" +
			"Target scope uses the ambient chain shared with board verbs:\n" +
			"--scope > TK_SCOPE > cwd code-root (not a positional scope name).",
		Args: cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			if len(args) == 0 {
				return runScopeHoldRead(app, c, scope)
			}
			req, err := parseHoldArgs(args)
			if err != nil {
				return err
			}
			return runScopeHoldSet(app, c, req, scope)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "scope to read or set (defaults to ambient)")
	return cmd
}

// holdRequest is a validated tk scope hold set. Type is token.HoldOff, HoldSoft, or HoldHard.
type holdRequest struct {
	Type   string
	Reason string
}

func parseHoldArgs(args []string) (holdRequest, error) {
	req := holdRequest{Type: args[0]}
	switch req.Type {
	case token.HoldOff:
		if len(args) > 1 {
			return holdRequest{}, usageErrorf("hold off takes no reason")
		}
	case token.HoldSoft, token.HoldHard:
		if len(args) > 2 {
			return holdRequest{}, usageErrorf("too many arguments: quote the reason, as in tk scope hold %s \"two words\"", req.Type)
		}
		if len(args) == 2 {
			reason, err := scopeconfig.NormalizeHoldReason(args[1])
			if err != nil {
				return holdRequest{}, usageErrorf("%s", err)
			}
			req.Reason = reason
		}
	default:
		return holdRequest{}, usageErrorf("%q is not a hold type (off, soft, hard)", req.Type)
	}
	return req, nil
}

// target is the hold state this request asks for from current. A type with no
// reason keeps any current reason, and a reason cannot outlive the hold.
func (r holdRequest) target(current *scopeconfig.Hold) *scopeconfig.Hold {
	if r.Type == token.HoldOff {
		return nil
	}
	next := &scopeconfig.Hold{Type: r.Type, Reason: r.Reason}
	if next.Reason == "" && current != nil {
		next.Reason = current.Reason
	}
	return next
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
	stdoutln(c, scopeconfig.HoldLabel(schema))
	return nil
}

func runScopeHoldSet(app *App, c *cobra.Command, req holdRequest, scopeFlag string) error {
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

	// The lock file lives in dir, so a missing dir must be reported before locking.
	if _, err := loadScopeSchema(app, scope, dir); err != nil {
		return err
	}
	lock, err := scopefile.AcquireLock(dir)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()

	// The kept reason comes from this read, so it must happen under the lock.
	schema, err := loadScopeSchema(app, scope, dir)
	if err != nil {
		return err
	}
	want := req.target(schema.Hold)
	if scopeconfig.HoldEqual(schema.Hold, want) {
		stderrln(c, fmt.Sprintf("hold is already %s", req.Type))
		return nil
	}
	autoCommit := schema.AutoCommit
	root, hasRoot := scopefile.GitRoot(dir)
	if err := checkMidRebase(c.Context(), scope, autoCommit, root, hasRoot); err != nil {
		return err
	}

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
	if !scopeconfig.HoldEqual(confirmed.Hold, want) {
		return withCueRestore(cuePath, prev, usageErrorf(
			"hold was not set (a sibling package file still pins hold); edit that file or move the declaration into tk.cue"))
	}

	if err := e.fieldConfigDurability(c, scope, dir, autoCommit, root, hasRoot,
		fmt.Sprintf("tk: scope hold %s", req.Type), cuePath); err != nil {
		return withCueRestore(cuePath, prev, err)
	}
	out, err := absPath(cuePath)
	if err != nil {
		return err
	}
	stdoutln(c, out)
	return nil
}
