package tkv

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// DefaultPort is the localhost listen port when --port is omitted.
const DefaultPort = 8736

// ExitUsage is the process exit status for a UsageError.
const ExitUsage = 2

// UsageError is a command-line usage error: an unknown command or flag, a bad
// flag value, or a stray positional argument. Its message carries one usage line.
type UsageError struct {
	Err error
}

func (e *UsageError) Error() string { return e.Err.Error() }
func (e *UsageError) Unwrap() error { return e.Err }

func usageErrorf(c *cobra.Command, format string, a ...any) error {
	return &UsageError{Err: fmt.Errorf("%s\nusage: %s", fmt.Sprintf(format, a...), usageLine(c))}
}

// usageLine is the one-line synopsis printed under a usage error.
func usageLine(c *cobra.Command) string {
	path := c.CommandPath()
	if c.HasAvailableSubCommands() {
		path += " [command]"
	}
	rest := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(c.Use), c.Name()))
	if rest == "" {
		return path
	}
	return path + " " + rest
}

const rootLong = `tkv is a local web dashboard for tk tickets.

Humans can mark, claim, create, meta, order, edit title and body, edit scope
notes, set or clear the tag lens, sync tk-driven git-roots, and run doctor
diagnose plus reindex. Agents keep using tk. It listens on 127.0.0.1 (never
0.0.0.0) and answers only to the hosts localhost, 127.0.0.1, or [::1] on any
port. The default port is 8736.

With no command, tkv runs start. With no --port, start attaches to the
dashboard already running, on whatever port it bound.

One dashboard per machine. Launch inside a registered code-root to open that
scope. Otherwise the machine overview is opened.`

const serveFlagsUse = "[--port N] [--no-open] [--scope NAME]"

// Help lists commands in workflow order. Cobra only offers a process-wide
// switch, so it is set once here rather than per tree, where concurrent
// Execute calls would race on it.
func init() {
	cobra.EnableCommandSorting = false
}

// Execute is the CLI entry: start (the default), stop, status, or foreground.
func (a *App) Execute(args []string) error {
	if !supportedOS() {
		return unsupportedOS()
	}
	root := a.newRootCmd()
	if args == nil {
		// Cobra reads os.Args when args is nil.
		args = []string{}
	}
	root.SetArgs(args)
	return root.Execute()
}

// newRootCmd builds a fresh tree so flag state cannot leak across invocations.
func (a *App) newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:               "tkv " + serveFlagsUse,
		Short:             "Local web dashboard for tk tickets",
		Long:              rootLong,
		SilenceUsage:      true,
		SilenceErrors:     true,
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
		Args: func(c *cobra.Command, args []string) error {
			if len(args) > 0 {
				return usageErrorf(c, "unknown command %q for %q", args[0], c.CommandPath())
			}
			return nil
		},
	}
	root.SetOut(a.stdout())
	root.SetErr(a.stderr())
	root.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		return usageErrorf(c, "%s", err)
	})
	a.bindServeFlags(root, a.Start)
	root.AddCommand(
		a.bindServeFlags(&cobra.Command{
			Use:   "start " + serveFlagsUse,
			Short: "Start in the background and return (default)",
			Long: `Start the dashboard in the background and return once it answers.
With no --port, start attaches to the dashboard already running, on whatever
port it bound. An explicit --port that differs from the running port is refused.`,
		}, a.Start),
		&cobra.Command{
			Use:   "stop",
			Short: "Stop the running dashboard (SIGTERM, wait for writes)",
			Long: `Stop the running dashboard: send SIGTERM and wait for in-flight writes to
drain. A dashboard that is not running is a successful stop.`,
			Args: noArgs,
			RunE: func(*cobra.Command, []string) error { return a.Stop() },
		},
		&cobra.Command{
			Use:   "status",
			Short: "Show pid, port, URL, and log path",
			Long:  "Show the running dashboard's pid, port, URL, and log path. Exits 1 when none is running.",
			Args:  noArgs,
			RunE:  func(*cobra.Command, []string) error { return a.Status() },
		},
		a.bindServeFlags(&cobra.Command{
			Use:   "foreground " + serveFlagsUse,
			Short: "Run in this terminal until interrupted",
			Long:  "Run the dashboard attached to this terminal until interrupted.",
		}, a.Run),
	)
	return root
}

func noArgs(c *cobra.Command, args []string) error {
	if len(args) > 0 {
		return usageErrorf(c, "too many arguments")
	}
	return nil
}

// bindServeFlags gives c the --port, --no-open, and --scope flags and a run
// that copies them onto a before calling run. They are per-command, not
// persistent: stop and status take none of them.
func (a *App) bindServeFlags(c *cobra.Command, run func() error) *cobra.Command {
	port := DefaultPort
	var noOpen bool
	var scope string
	fs := c.Flags()
	fs.Var((*portValue)(&port), "port", "listen port")
	fs.BoolVar(&noOpen, "no-open", false, "print the landing URL and do not open a browser")
	fs.StringVar(&scope, "scope", "", "open the board of scope `NAME` (same as tk: wins over TK_SCOPE and cwd)")
	if c.Args == nil {
		c.Args = noArgs
	}
	c.RunE = func(c *cobra.Command, _ []string) error {
		a.Port = port
		a.PortSet = c.Flags().Changed("port")
		a.NoOpen = noOpen
		a.ScopeFlag = scope
		return run()
	}
	return c
}

// portValue is a --port value, validated at parse time so a bad port is a
// flag error.
type portValue int

var errPortRange = errors.New("must be a port number 1–65535")

func (p *portValue) Set(s string) error {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > 65535 {
		return errPortRange
	}
	*p = portValue(n)
	return nil
}

func (p *portValue) String() string { return strconv.Itoa(int(*p)) }
func (p *portValue) Type() string   { return "N" }
