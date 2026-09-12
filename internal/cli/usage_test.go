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
		{"depends none", []string{"depends"}, "missing <id>", "tk depends <id> [--scope S] [--transitive] [--tree] [--no-lens]"},
		{"deps alias none", []string{"deps"}, "missing <id>", "tk depends <id> [--scope S] [--transitive] [--tree] [--no-lens]"},
		{"get none", []string{"get"}, "missing <id>", "tk get <id> [--content] [--scope S]"},
		{"rehome none", []string{"rehome"}, "missing <id> <dest-scope>", "tk rehome <id> <dest-scope> [--scope S]"},
		{"rehome one", []string{"rehome", "ab2c"}, "missing <dest-scope>", "tk rehome <id> <dest-scope> [--scope S]"},
		{"mark none", []string{"mark"}, "missing <status> <id>", "tk mark <status> <id> [id...] [--scope S]"},
		{"mark one", []string{"mark", "todo"}, "missing <id>", "tk mark <status> <id> [id...] [--scope S]"},
		{"create none", []string{"create"}, "missing <title>", "tk create <title> [status] [--scope S] [--tag T]... [--edit]"},
		{"meta set none", []string{"meta", "set"}, "missing <id> <key> <value>", "tk meta set <id> <key> <value> [--scope S]"},
		{"meta set two", []string{"meta", "set", "ab2c", "summary"}, "missing <value>", "tk meta set <id> <key> <value> [--scope S]"},
		{"meta remove none", []string{"meta", "remove"}, "missing <id> <key> <value>", "tk meta remove <id> <key> <value> [--scope S]"},
		{"meta rm alias none", []string{"meta", "rm"}, "missing <id> <key> <value>", "tk meta remove <id> <key> <value> [--scope S]"},
		{"forget none", []string{"scope", "forget"}, "missing <name>", "tk scope forget <name>"},
		{"rename one", []string{"scope", "rename", "old"}, "missing <new>", "tk scope rename <old> <new>"},
		{"note add none", []string{"note", "add"}, "missing <text...>", "tk note add [--name slug] <text...>"},
		{"field set none", []string{"scope", "field", "set"}, "missing <name>", "tk scope field set <name> --type <string|int|bool|strings> [--required] [--values V]... [--scope S]"},
		{"field unset none", []string{"scope", "field", "unset"}, "missing <name>", "tk scope field unset <name> [--strip] [--scope S]"},
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
		{"depends extra", []string{"depends", "ab2c", "extra"}, "tk depends <id> [--scope S] [--transitive] [--tree] [--no-lens]"},
		{"depends tree extra", []string{"depends", "--tree", "ab2c", "extra"}, "tk depends [<id>] [--scope S] [--transitive] [--tree] [--no-lens]"},
		{"forget extra", []string{"scope", "forget", "a", "b"}, "tk scope forget <name>"},
		{"next extra", []string{"next", "ab2c"}, "tk next [--scope S] [--no-lens] [--claim]"},
		{"rehome extra", []string{"rehome", "ab2c", "bar", "extra"}, "tk rehome <id> <dest-scope> [--scope S]"},
		{"pulse extra", []string{"pulse", "mode", "extra"}, "tk pulse [key] [--scope S]"},
		{"create extra", []string{"create", "one", "todo", "three"}, "tk create <title> [status] [--scope S] [--tag T]... [--edit]"},
		{"reindex extra", []string{"reindex", "x"}, "tk reindex"},
		{"auto-commit extra", []string{"scope", "auto-commit", "true", "false"}, "tk scope auto-commit [true|false] [--scope S]"},
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

func TestStatusIsNotACommand(t *testing.T) {
	app := newApp(t)
	want := `unknown command "status" for "tk"`
	for _, args := range [][]string{{"status"}, {"status", "mode"}, {"status", "mode", "extra"}} {
		out, _, err := run(t, app, args...)
		if got := ExitCodeFromError(err); got != exitUsage {
			t.Errorf("%v exit = %d want 2 (err=%v)", args, got, err)
		}
		if err == nil {
			t.Errorf("%v: expected usage error", args)
			continue
		}
		if err.Error() != want {
			t.Errorf("%v message = %q, want %q", args, err.Error(), want)
		}
		if out != "" {
			t.Errorf("%v must leave stdout empty, got %q", args, out)
		}
		if looksLikePulse(out) {
			t.Errorf("%v must not run the pulse, got %q", args, out)
		}
	}

	for _, args := range [][]string{{"status", "--scope", "wc"}, {"status", "mode", "--scope", "wc"}} {
		out, _, err := run(t, app, args...)
		if got := ExitCodeFromError(err); got != exitUsage {
			t.Errorf("%v exit = %d want 2 (err=%v)", args, got, err)
		}
		if err == nil || !strings.Contains(err.Error(), "unknown flag: --scope") {
			t.Errorf("%v want unknown flag --scope, got %v", args, err)
		}
		if looksLikePulse(out) {
			t.Errorf("%v must not run the pulse, got %q", args, out)
		}
	}

	out, _, err := run(t, app, "status", "--help")
	if err != nil {
		t.Fatalf("status --help: %v", err)
	}
	if looksLikePulse(out) {
		t.Errorf("status --help must not run the pulse, got %q", out)
	}
	if !strings.Contains(out, groupBoardTitle) || !strings.Contains(out, "pulse") {
		t.Errorf("status --help should be root help listing pulse, got:\n%s", out)
	}
}

func TestMarkHelpPointsAtPulse(t *testing.T) {
	app := newApp(t)
	out, _, err := run(t, app, "mark", "--help")
	if err != nil {
		t.Fatalf("mark --help: %v", err)
	}
	if !strings.Contains(out, "`tk pulse`") {
		t.Errorf("mark help must point at tk pulse, got:\n%s", out)
	}
	if strings.Contains(out, "`tk status`") {
		t.Errorf("mark help must not point at tk status, got:\n%s", out)
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
	_, _, err := run(t, app, "depends", "--bogus")
	if got := ExitCodeFromError(err); got != exitUsage {
		t.Fatalf("exit code = %d want %d (err=%v)", got, exitUsage, err)
	}
	msg := err.Error()
	if !strings.HasPrefix(msg, "unknown flag: --bogus\n") {
		t.Errorf("message %q, want cobra flag error first", msg)
	}
	if !strings.Contains(msg, "usage: tk depends [<id>] [--scope S] [--transitive] [--tree] [--no-lens]") {
		t.Errorf("message %q, want usage line", msg)
	}
}

func TestArityPrintedError(t *testing.T) {
	app := newApp(t)
	_, _, err := run(t, app, "depends")
	if err == nil {
		t.Fatal("expected usage error")
	}
	var buf strings.Builder
	fprintError(&buf, err, true)
	got := buf.String()
	want := ansiRed + "error:" + ansiReset + " missing <id>\nusage: tk depends <id> [--scope S] [--transitive] [--tree] [--no-lens]\n"
	if got != want {
		t.Errorf("printed %q, want %q", got, want)
	}
}
