package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func exactArgs(names ...string) cobra.PositionalArgs {
	return arity(len(names), len(names), names)
}

func rangeArgs(minCount, maxCount int, required ...string) cobra.PositionalArgs {
	return arity(minCount, maxCount, required)
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

// arity enforces minCount and maxCount; required is only the missing-name list, not the count.
func arity(minCount, maxCount int, required []string) cobra.PositionalArgs {
	return func(c *cobra.Command, args []string) error {
		n := len(args)
		switch {
		case maxCount == 0 && n > 0 && c.HasAvailableSubCommands():
			return &ExitError{Code: exitUsage, Err: fmt.Errorf("unknown command %q for %q", args[0], c.CommandPath())}
		case n < minCount:
			return usageErrorf("%s", arityMessage(c, args, minCount, required))
		case maxCount >= 0 && n > maxCount:
			return usageErrorf("%s", arityMessage(c, args, minCount, required))
		default:
			return nil
		}
	}
}

func arityMessage(c *cobra.Command, args []string, minCount int, required []string) string {
	usage := commandUsageLine(c)
	if len(args) < minCount {
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
