package tkv

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// executeCapture runs args through the command tree with stdout and stderr captured.
func executeCapture(t *testing.T, app *App, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errBuf bytes.Buffer
	app.Stdout = &out
	app.Stderr = &errBuf
	err = app.Execute(args)
	return out.String(), errBuf.String(), err
}

// blockStateDir makes every serve fail before it binds, after the tree has
// copied its flags onto app.
func blockStateDir(t *testing.T, app *App) {
	t.Helper()
	blocked := filepath.Join(t.TempDir(), "notdir")
	if err := os.WriteFile(blocked, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	app.StateDir = blocked
}

func TestServeFlagsReachApp(t *testing.T) {
	for _, verb := range [][]string{nil, {"start"}, {"foreground"}} {
		name := "root"
		if verb != nil {
			name = verb[0]
		}
		t.Run(name, func(t *testing.T) {
			app := newTestApp(t)
			blockStateDir(t, app)
			app.NoOpen = false
			args := append(append([]string{}, verb...), "--no-open", "--port", "9001", "--scope", "wc")
			if _, _, err := executeCapture(t, app, args...); err == nil {
				t.Fatal("expected the blocked state dir to fail the serve")
			}
			if !app.NoOpen || app.Port != 9001 || app.ScopeFlag != "wc" {
				t.Fatalf("set: %+v", app)
			}

			omitted := newTestApp(t)
			blockStateDir(t, omitted)
			omitted.Port = 1
			omitted.ScopeFlag = "stale"
			args = append(append([]string{}, verb...), "--no-open")
			if _, _, err := executeCapture(t, omitted, args...); err == nil {
				t.Fatal("expected the blocked state dir to fail the serve")
			}
			if omitted.Port != 0 || omitted.ScopeFlag != "" {
				t.Fatalf("omitted: port=%d scope=%q", omitted.Port, omitted.ScopeFlag)
			}
		})
	}
}

func TestHelpGoesToStdout(t *testing.T) {
	cases := []struct {
		args []string
		want []string
		deny []string
	}{
		{[]string{"--help"}, []string{"tkv is a local web dashboard", "127.0.0.1", "start", "stop", "status", "foreground", "--port N", "--no-open", "--scope NAME", "(default 8736)", "--listen HOST|HOST:PORT", "network mode", "fingerprint", "listen", "hosts", "user"}, []string{"completion", "never"}},
		{[]string{"start", "--help"}, []string{"tkv start [--listen HOST|HOST:PORT] [--port N]", "--port N", "--no-open", "--scope NAME"}, nil},
		{[]string{"foreground", "-h"}, []string{"tkv foreground", "--port N"}, []string{"attaches"}},
		{[]string{"help", "start"}, []string{"tkv start [--listen HOST|HOST:PORT] [--port N]", "attaches"}, nil},
		{[]string{"stop", "--help"}, []string{"tkv stop"}, []string{"--port", "--no-open", "--scope"}},
		{[]string{"status", "--help"}, []string{"tkv status"}, []string{"--port", "--no-open", "--scope"}},
		{[]string{"user", "--help"}, []string{"list", "set", "delete"}, []string{"--port", "--listen"}},
		{[]string{"user", "set", "--help"}, []string{"tkv user set NAME", "never taken from argv"}, nil},
		{[]string{"listen", "--help"}, []string{"tkv listen [HOST|HOST:PORT] [--clear]", "--clear"}, []string{"--no-open"}},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			stdout, stderr, err := executeCapture(t, newTestApp(t), tc.args...)
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if stderr != "" {
				t.Fatalf("stderr = %q", stderr)
			}
			for _, w := range tc.want {
				if !strings.Contains(stdout, w) {
					t.Errorf("help lacks %q:\n%s", w, stdout)
				}
			}
			for _, d := range tc.deny {
				if strings.Contains(stdout, d) {
					t.Errorf("help has %q:\n%s", d, stdout)
				}
			}
		})
	}
}

func TestUsageErrors(t *testing.T) {
	cases := []struct {
		args  []string
		msg   string
		usage string
	}{
		{[]string{"--port", "0"}, `invalid argument "0" for "--port" flag`, "usage: tkv [command] [--listen HOST|HOST:PORT] [--port N]"},
		{[]string{"start", "--port", "65536"}, "1–65535", "usage: tkv start [--listen HOST|HOST:PORT] [--port N]"},
		{[]string{"foreground", "--port", "x"}, "1–65535", "usage: tkv foreground [--listen HOST|HOST:PORT] [--port N]"},
		{[]string{"nope"}, `unknown command "nope" for "tkv"`, "usage: tkv [command]"},
		{[]string{"completion"}, `unknown command "completion"`, "usage: tkv [command]"},
		{[]string{"--version"}, "unknown flag: --version", "usage: tkv [command]"},
		{[]string{"start", "extra"}, "too many arguments", "usage: tkv start [--listen HOST|HOST:PORT] [--port N]"},
		{[]string{"stop", "extra"}, "too many arguments", "usage: tkv stop"},
		{[]string{"-port", "9001"}, "unknown shorthand flag", "usage: tkv [command]"},
		{[]string{"-no-open"}, "unknown shorthand flag", "usage: tkv [command]"},
		{[]string{"stop", "--port", "9001"}, "unknown flag: --port", "usage: tkv stop"},
		{[]string{"status", "--scope", "x"}, "unknown flag: --scope", "usage: tkv status"},
		{[]string{"status", "--no-open"}, "unknown flag: --no-open", "usage: tkv status"},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			app := newTestApp(t)
			stdout, stderr, err := executeCapture(t, app, tc.args...)
			var usage *UsageError
			if !errors.As(err, &usage) {
				t.Fatalf("err = %v (%T), want *UsageError", err, err)
			}
			msg := err.Error()
			if !strings.Contains(msg, tc.msg) || !strings.Contains(msg, tc.usage) {
				t.Fatalf("msg = %q, want %q and %q", msg, tc.msg, tc.usage)
			}
			if lines := strings.Count(msg, "\n"); lines != 1 {
				t.Fatalf("msg has %d newlines, want the error and one usage line: %q", lines, msg)
			}
			if stdout != "" || stderr != "" {
				t.Fatalf("tree printed: stdout=%q stderr=%q", stdout, stderr)
			}
		})
	}
}

func TestRuntimeErrorIsNotUsage(t *testing.T) {
	app := newTestApp(t)
	_, _, err := executeCapture(t, app, "status")
	if !errors.Is(err, errNotRunning) {
		t.Fatalf("err = %v", err)
	}
	var usage *UsageError
	if errors.As(err, &usage) {
		t.Fatal("a runtime error must not map to the usage exit")
	}
}
