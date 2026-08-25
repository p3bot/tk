package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func exactArgs(names ...string) cobra.PositionalArgs {
	return arity(len(names), len(names), names)
}

func rangeArgs(min, max int, required ...string) cobra.PositionalArgs {
	return arity(min, max, required)
}

func minArgs(names ...string) cobra.PositionalArgs {
	return arity(len(names), -1, names)
}

func maxArgs(n int) cobra.PositionalArgs {
	return arity(0, n, nil)
}

func noArgs() cobra.PositionalArgs {
	return arity(0, 0, nil)
}

func anyArgs() cobra.PositionalArgs {
	return cobra.ArbitraryArgs
}

// arity enforces min/max; required is only the missing-name list, not the count.
func arity(min, max int, required []string) cobra.PositionalArgs {
	return func(c *cobra.Command, args []string) error {
		n := len(args)
		switch {
		case max == 0 && n > 0 && c.HasAvailableSubCommands():
			return &ExitError{Code: exitUsage, Err: fmt.Errorf("unknown command %q for %q", args[0], c.CommandPath())}
		case n < min:
			return usageErrorf("%s", arityMessage(c, args, min, required))
		case max >= 0 && n > max:
			return usageErrorf("%s", arityMessage(c, args, min, required))
		default:
			return nil
		}
	}
}

func arityMessage(c *cobra.Command, args []string, min int, required []string) string {
	usage := commandUsageLine(c)
	if len(args) < min {
		if len(args) < len(required) {
			return "missing " + strings.Join(required[len(args):], " ") + "\nusage: " + usage
		}
		return "missing arguments\nusage: " + usage
	}
	return "too many arguments\nusage: " + usage
}

func flagUsageError(c *cobra.Command, err error) error {
	return usageErrorf("%s\nusage: %s", err.Error(), commandUsageLine(c))
}

func commandUsageLine(c *cobra.Command) string {
	use := strings.TrimSpace(c.Use)
	name := c.Name()
	rest := strings.TrimSpace(strings.TrimPrefix(use, name))
	if rest == "" {
		return c.CommandPath()
	}
	return c.CommandPath() + " " + rest
}
