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
diagnose plus reindex. Agents keep using tk. The default port is 8736.

By default tkv listens on 127.0.0.1 in loopback mode: plain HTTP, no login,
and it answers only to the hosts localhost, 127.0.0.1, or [::1] on any port.

Listening on any other address (--listen or tkv listen), 0.0.0.0 and ::
included, is network mode: always HTTPS with a self-signed certificate and
a login before any page. Browsers warn about the certificate; compare the
fingerprint tkv prints. Network mode refuses to start until a user exists
(tkv user set NAME). 0.0.0.0 serves IPv4 only and :: serves IPv6 only.
Add names that other machines use for this one with tkv hosts.

With no command, tkv runs start. With no --port or --listen, start attaches
to the dashboard already running, on whatever address it bound.

One dashboard per machine. Launch inside a registered code-root to open that
scope. Otherwise the machine overview is opened.`

const serveFlagsUse = "[--listen HOST|HOST:PORT] [--port N] [--no-open] [--scope NAME]"

// restartNote follows a change that a running dashboard reads only at start.
const restartNote = "tkv: a running dashboard keeps its old setting until restarted (tkv stop, then tkv start)"

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
With no --port or --listen, start attaches to the dashboard already running,
on whatever address it bound. An explicit host or port that differs from the
running one is refused.`,
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
			Short: "Show pid, mode, address, URL, and log path",
			Long: `Show the running dashboard's pid, mode, address, URL, and log path. In
network mode also show the certificate SHA-256 fingerprint and the URLs other
machines may use. Exits 1 when none is running.`,
			Args: noArgs,
			RunE: func(*cobra.Command, []string) error { return a.Status() },
		},
		a.bindServeFlags(&cobra.Command{
			Use:   "foreground " + serveFlagsUse,
			Short: "Run in this terminal until interrupted",
			Long:  "Run the dashboard attached to this terminal until interrupted.",
		}, a.Run),
		a.listenCmd(),
		a.hostsCmd(),
		a.userCmd(),
	)
	return root
}

func noArgs(c *cobra.Command, args []string) error {
	if len(args) > 0 {
		return usageErrorf(c, "too many arguments")
	}
	return nil
}

// bindServeFlags gives c the --listen, --port, --no-open, and --scope flags
// and a run that copies them onto a before calling run. They are
// per-command, not persistent: stop, status, and the setting commands take
// none of them.
func (a *App) bindServeFlags(c *cobra.Command, run func() error) *cobra.Command {
	var listen listenValue
	port := DefaultPort
	var noOpen bool
	var scope string
	fs := c.Flags()
	fs.Var(&listen, "listen", "listen on `HOST|HOST:PORT`, an IP address or localhost; non-loopback is network mode (HTTPS and login)")
	fs.Var((*portValue)(&port), "port", "listen port; overrides only the port of tkv listen")
	fs.BoolVar(&noOpen, "no-open", false, "print the landing URL and do not open a browser")
	fs.StringVar(&scope, "scope", "", "open the board of scope `NAME` (same as tk: wins over TK_SCOPE and cwd)")
	if c.Args == nil {
		c.Args = noArgs
	}
	c.RunE = func(c *cobra.Command, _ []string) error {
		a.Listen = listen.spec
		a.Port = 0
		if c.Flags().Changed("port") {
			a.Port = port
		}
		a.NoOpen = noOpen
		a.ScopeFlag = scope
		return run()
	}
	return c
}

// listenValue is a --listen value, parsed at flag time so a name or a bad
// port is a usage error.
type listenValue struct {
	spec *listenSpec
}

func (l *listenValue) Set(s string) error {
	spec, err := parseListen(s)
	if err != nil {
		return err
	}
	l.spec = &spec
	return nil
}

func (l *listenValue) String() string {
	if l.spec == nil {
		return ""
	}
	return l.spec.String()
}

func (l *listenValue) Type() string { return "HOST|HOST:PORT" }

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
