package tkv

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/p3bot/tk/internal/passhash"
	"github.com/p3bot/tk/internal/tkvconf"
)

// updateConfig is the one write path to tkv.cue: machine flock, regenerate,
// atomic rename at 0600.
func (a *App) updateConfig(fn func(*tkvconf.Config) error) error {
	dir, err := a.configDir()
	if err != nil {
		return err
	}
	return tkvconf.Update(a.cue(), dir, fn)
}

func (a *App) listenCmd() *cobra.Command {
	var clearIt bool
	c := &cobra.Command{
		Use:   "listen [HOST|HOST:PORT] [--clear]",
		Short: "Show, store, or clear the listen address",
		Long: `With no argument, show the stored listen address. With HOST or HOST:PORT,
store it, replacing the whole stored address: a bare HOST clears a stored
port. HOST is an IP address or localhost; IPv6 with a port needs brackets
([::]:9000). --clear removes it, so tkv listens on 127.0.0.1:8736.
--listen and --port on start override the stored address for one run.`,
		Args: func(c *cobra.Command, args []string) error {
			if len(args) > 1 {
				return usageErrorf(c, "too many arguments")
			}
			if clearIt && len(args) > 0 {
				return usageErrorf(c, "--clear takes no address")
			}
			if len(args) == 1 {
				if _, err := parseListen(args[0]); err != nil {
					return usageErrorf(c, "%s", err)
				}
			}
			return nil
		},
		RunE: func(_ *cobra.Command, args []string) error {
			if !clearIt && len(args) == 0 {
				conf, err := a.loadConfig()
				if err != nil {
					return err
				}
				if l := conf.Listen; l.Host != "" || l.Port != 0 {
					port := ""
					if l.Port != 0 {
						port = strconv.Itoa(l.Port)
					}
					if port == "" {
						fmt.Fprintln(a.stdout(), l.Host)
					} else {
						fmt.Fprintln(a.stdout(), net.JoinHostPort(l.Host, port))
					}
				}
				return nil
			}
			var stored tkvconf.Listen
			if !clearIt {
				spec, _ := parseListen(args[0])
				stored = tkvconf.Listen{Host: spec.host, Port: spec.port}
			}
			if err := a.updateConfig(func(conf *tkvconf.Config) error {
				conf.Listen = stored
				return nil
			}); err != nil {
				return err
			}
			fmt.Fprintln(a.stderr(), restartNote)
			return nil
		},
	}
	c.Flags().BoolVar(&clearIt, "clear", false, "remove the stored listen address")
	return c
}

func (a *App) hostsCmd() *cobra.Command {
	var clearIt bool
	c := &cobra.Command{
		Use:   "hosts [NAME...] [--clear]",
		Short: "Show, replace, or clear the extra hostnames",
		Long: `With no argument, show the extra hostnames, one per line. With NAME...,
replace the list. --clear removes it. In network mode the extra hostnames
join localhost, 127.0.0.1, ::1, the bound IP, and this machine's short
hostname in the certificate and in the Host names tkv answers to. Add a
name that other machines use for this one, such as nas.lan or an mDNS name.`,
		Args: func(c *cobra.Command, args []string) error {
			if clearIt && len(args) > 0 {
				return usageErrorf(c, "--clear takes no names")
			}
			for _, name := range args {
				if _, err := tkvconf.CheckHostName(name); err != nil {
					return usageErrorf(c, "%s", err)
				}
			}
			return nil
		},
		RunE: func(_ *cobra.Command, args []string) error {
			if !clearIt && len(args) == 0 {
				conf, err := a.loadConfig()
				if err != nil {
					return err
				}
				for _, h := range conf.Hosts {
					fmt.Fprintln(a.stdout(), h)
				}
				return nil
			}
			var hosts []string
			for _, name := range args {
				h, _ := tkvconf.CheckHostName(name)
				if !slices.Contains(hosts, h) {
					hosts = append(hosts, h)
				}
			}
			if err := a.updateConfig(func(conf *tkvconf.Config) error {
				conf.Hosts = hosts
				return nil
			}); err != nil {
				return err
			}
			fmt.Fprintln(a.stderr(), restartNote)
			return nil
		},
	}
	c.Flags().BoolVar(&clearIt, "clear", false, "remove the extra hostnames")
	return c
}

func (a *App) userCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "user",
		Short: "List, set, or delete network-mode login users",
		Long: `Manage the users who can sign in to a network-mode dashboard. Changes
reach a running dashboard on its next request: a changed password or a
deleted user signs out that user's browsers.`,
		Args: func(c *cobra.Command, args []string) error {
			if len(args) > 0 {
				return usageErrorf(c, "unknown command %q for %q", args[0], c.CommandPath())
			}
			return nil
		},
		RunE: func(c *cobra.Command, _ []string) error { return c.Help() },
	}
	c.AddCommand(
		&cobra.Command{
			Use:   "list",
			Short: "List usernames, one per line",
			Args:  noArgs,
			RunE: func(*cobra.Command, []string) error {
				conf, err := a.loadConfig()
				if err != nil {
					return err
				}
				for _, name := range conf.UserNames() {
					fmt.Fprintln(a.stdout(), name)
				}
				return nil
			},
		},
		&cobra.Command{
			Use:   "set NAME",
			Short: "Add a user or change the password (prompted)",
			Long: `Add the user or change the password. On a terminal the password is
prompted twice without echo; otherwise one line is read from stdin. The
password is never taken from argv or the environment. Usernames match
^[a-z0-9._-]{1,32}$; passwords are 8 characters to 1024 bytes.`,
			Args: userNameArg,
			RunE: func(_ *cobra.Command, args []string) error {
				pw, err := a.readNewPassword()
				if err != nil {
					return err
				}
				hash, err := passhash.Hash(pw)
				if err != nil {
					return err
				}
				return a.updateConfig(func(conf *tkvconf.Config) error {
					if conf.Users == nil {
						conf.Users = map[string]string{}
					}
					conf.Users[args[0]] = hash
					return nil
				})
			},
		},
		&cobra.Command{
			Use:   "delete NAME",
			Short: "Remove a user",
			Args:  userNameArg,
			RunE: func(_ *cobra.Command, args []string) error {
				return a.updateConfig(func(conf *tkvconf.Config) error {
					if _, ok := conf.Users[args[0]]; !ok {
						return fmt.Errorf("no user %q", args[0])
					}
					delete(conf.Users, args[0])
					return nil
				})
			},
		},
	)
	return c
}

func userNameArg(c *cobra.Command, args []string) error {
	if len(args) != 1 {
		return usageErrorf(c, "want one NAME")
	}
	if err := tkvconf.CheckUserName(args[0]); err != nil {
		return usageErrorf(c, "%s", err)
	}
	return nil
}

// readNewPassword prompts twice without echo on a terminal, else reads one
// line from stdin for scripts.
func (a *App) readNewPassword() (string, error) {
	in := a.stdin()
	if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		first, err := a.promptPassword(f, "Password: ")
		if err != nil {
			return "", err
		}
		if err := tkvconf.CheckPassword(first); err != nil {
			return "", err
		}
		again, err := a.promptPassword(f, "Repeat password: ")
		if err != nil {
			return "", err
		}
		if again != first {
			return "", errors.New("passwords do not match")
		}
		return first, nil
	}
	line, err := bufio.NewReader(io.LimitReader(in, tkvconf.MaxPasswordBytes+3)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read password: %w", err)
	}
	if errors.Is(err, io.EOF) && line == "" {
		return "", errors.New("no password on stdin")
	}
	pw := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	if err := tkvconf.CheckPassword(pw); err != nil {
		return "", err
	}
	return pw, nil
}

func (a *App) promptPassword(f *os.File, prompt string) (string, error) {
	fmt.Fprint(a.stderr(), prompt)
	b, err := term.ReadPassword(int(f.Fd()))
	fmt.Fprintln(a.stderr())
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	return string(b), nil
}
