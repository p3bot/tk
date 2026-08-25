package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestArityNamesComeFromValidator(t *testing.T) {
	cmd := &cobra.Command{
		Use:           "ghost",
		Args:          exactArgs("<id>"),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          func(*cobra.Command, []string) error { return nil },
	}
	cmd.SetArgs(nil)
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected usage error")
	}
	want := "missing <id>\nusage: ghost"
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
}

func TestArityUsageMessages(t *testing.T) {
	app := newApp(t)
	cases := []struct {
		name    string
		args    []string
		missing string
		usage   string
	}{
		{"deps none", []string{"deps"}, "missing <id>", "tk deps <id> [--scope S] [--transitive] [--tree]"},
		{"get none", []string{"get"}, "missing <id>", "tk get <id> [--content] [--scope S]"},
		{"mark none", []string{"mark"}, "missing <id> <status>", "tk mark <id> <status> [--scope S]"},
		{"mark one", []string{"mark", "ab2c"}, "missing <status>", "tk mark <id> <status> [--scope S]"},
		{"create none", []string{"create"}, "missing <title>", "tk create <title> [status] [--scope S] [--tag T]..."},
		{"meta set none", []string{"meta", "set"}, "missing <id> <key> <value>", "tk meta set <id> <key> <value> [--scope S]"},
		{"meta set two", []string{"meta", "set", "ab2c", "summary"}, "missing <value>", "tk meta set <id> <key> <value> [--scope S]"},
		{"forget none", []string{"scope", "forget"}, "missing <name>", "tk scope forget <name>"},
		{"rename one", []string{"scope", "rename", "old"}, "missing <new>", "tk scope rename <old> <new>"},
		{"note add none", []string{"note", "add"}, "missing <text...>", "tk note add [--name slug] <text...>"},
		{"field set none", []string{"scope", "field", "set"}, "missing <name>", "tk scope field set <name> --type <string|int|bool|strings> [--required] [--values V]... [--scope S]"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := run(t, app, c.args...)
			if got := ExitCodeFromError(err); got != exitUsage {
				t.Fatalf("exit code = %d want %d (err=%v)", got, exitUsage, err)
			}
			msg := err.Error()
			if !strings.HasPrefix(msg, c.missing+"\n") {
				t.Errorf("message %q, want prefix %q", msg, c.missing)
			}
			if !strings.Contains(msg, "usage: "+c.usage) {
				t.Errorf("message %q, want usage %q", msg, c.usage)
			}
			if strings.Contains(msg, "accepts") || strings.Contains(msg, "received") {
				t.Errorf("still leaking cobra arity text: %q", msg)
			}
		})
	}
}

func TestArityTooManyArguments(t *testing.T) {
	app := newApp(t)
	cases := []struct {
		name  string
		args  []string
		usage string
	}{
		{"deps extra", []string{"deps", "ab2c", "extra"}, "tk deps <id> [--scope S] [--transitive] [--tree]"},
		{"forget extra", []string{"scope", "forget", "a", "b"}, "tk scope forget <name>"},
		{"next extra", []string{"next", "ab2c"}, "tk next [--scope S] [--no-lens] [--claim]"},
		{"status extra", []string{"status", "mode", "extra"}, "tk status [key] [--scope S]"},
		{"create extra", []string{"create", "one", "todo", "three"}, "tk create <title> [status] [--scope S] [--tag T]..."},
		{"reindex extra", []string{"reindex", "x"}, "tk reindex"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := run(t, app, c.args...)
			if got := ExitCodeFromError(err); got != exitUsage {
				t.Fatalf("exit code = %d want %d (err=%v)", got, exitUsage, err)
			}
			msg := err.Error()
			want := "too many arguments\nusage: " + c.usage
			if msg != want {
				t.Errorf("message = %q, want %q", msg, want)
			}
		})
	}
}

func TestUnknownCommandKeptForParents(t *testing.T) {
	app := newApp(t)
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"root", []string{"nosuch"}, `unknown command "nosuch" for "tk"`},
		{"skill", []string{"skill", "nosuch"}, `unknown command "nosuch" for "tk skill"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := run(t, app, c.args...)
			if got := ExitCodeFromError(err); got != exitUsage {
				t.Fatalf("exit code = %d want %d (err=%v)", got, exitUsage, err)
			}
			msg := err.Error()
			if msg != c.want {
				t.Errorf("message = %q, want %q", msg, c.want)
			}
			if strings.Contains(msg, "too many arguments") {
				t.Errorf("parent leftover must not be rewritten as arity: %q", msg)
			}
		})
	}
}

func TestFlagErrorIncludesUsage(t *testing.T) {
	app := newApp(t)
	_, _, err := run(t, app, "deps", "--bogus")
	if got := ExitCodeFromError(err); got != exitUsage {
		t.Fatalf("exit code = %d want %d (err=%v)", got, exitUsage, err)
	}
	msg := err.Error()
	if !strings.HasPrefix(msg, "unknown flag: --bogus\n") {
		t.Errorf("message %q, want cobra flag error first", msg)
	}
	if !strings.Contains(msg, "usage: tk deps <id> [--scope S] [--transitive] [--tree]") {
		t.Errorf("message %q, want usage line", msg)
	}
}

func TestArityPrintedError(t *testing.T) {
	app := newApp(t)
	_, _, err := run(t, app, "deps")
	if err == nil {
		t.Fatal("expected usage error")
	}
	var buf strings.Builder
	fprintError(&buf, err, true)
	got := buf.String()
	want := ansiRed + "error:" + ansiReset + " missing <id>\nusage: tk deps <id> [--scope S] [--transitive] [--tree]\n"
	if got != want {
		t.Errorf("printed %q, want %q", got, want)
	}
}
