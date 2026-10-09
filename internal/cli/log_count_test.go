package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/token"
)

func TestLogCount(t *testing.T) {
	app := newApp(t)
	wc := initScope(t, app, "wc")
	ot := initScope(t, app, "ot")
	today := localStamp(localDay(0), 12, 0, 0)
	yesterday := localStamp(localDay(-1), 12, 0, 0)
	badPath := filepath.Join(wc, "archive", "wc-jk9m-bad.md")

	addTicket(t, wc, "wc-ab2c", "done-today", "done", "a0", "# Done today\n", true, "changed: "+today+"\n")
	addTicket(t, wc, "wc-de34", "done-yday", "done", "a1", "# Done yesterday\n", true, "changed: "+yesterday+"\n")
	addTicket(t, wc, "wc-jk9m", "bad", "done", "a2", "# Bad\n", true, "changed: not-a-time\n")
	addTicket(t, wc, "wc-np23", "todo-today", "todo", "a3", "# Todo\n", false, "changed: "+today+"\n")
	addTicket(t, ot, "ot-ab2c", "done-today", "done", "a0", "# Other\n", true, "changed: "+today+"\n")
	if _, _, err := run(t, app, "lens", "ui", "--scope", "wc"); err != nil {
		t.Fatal(err)
	}

	rows, rowErr, err := run(t, app, "log")
	if err != nil {
		t.Fatal(err)
	}
	got, gotErr, err := run(t, app, "log", "--count")
	if err != nil {
		t.Fatal(err)
	}
	if got != "2\n" || len(lines(rows)) != 2 {
		t.Fatalf("count = %q rows %q", got, rows)
	}
	if gotErr != rowErr || !strings.Contains(gotErr, token.FormatChangedNotRFC3339("wc-jk9m", "not-a-time", badPath)) {
		t.Fatalf("stderr\nlog %q\ncount %q", rowErr, gotErr)
	}
	if strings.Contains(got, "wc-ab2c") || strings.Contains(gotErr, "lens:") {
		t.Fatalf("count leaked rows or lens: out %q err %q", got, gotErr)
	}

	alias, aliasErr, err := run(t, app, "logs", "--count")
	if err != nil || alias != got || aliasErr != gotErr {
		t.Fatalf("logs --count = %q err %v stderr %q", alias, err, aliasErr)
	}

	one, _, err := run(t, app, "log", "--count", "--scope", "wc")
	if err != nil || one != "1\n" {
		t.Fatalf("scope count = %q err %v", one, err)
	}
	yday, _, err := run(t, app, "log", "--count", "--yesterday")
	if err != nil || yday != "1\n" {
		t.Fatalf("yesterday = %q err %v", yday, err)
	}
	none, _, err := run(t, app, "log", "--count", "--scope", "ot", "--yesterday")
	if err != nil || none != "0\n" {
		t.Fatalf("empty selection = %q err %v", none, err)
	}

	out, _, err := run(t, app, "log", "--count", "--date", "2026/10/01")
	if ExitCodeFromError(err) != exitUsage || out != "" {
		t.Fatalf("bad date exit %d out %q err %v", ExitCodeFromError(err), out, err)
	}

	help, errOut, err := run(t, app, "log", "--help")
	if err != nil {
		t.Fatal(err)
	}
	text := help + errOut
	if !strings.Contains(text, "--count") || !strings.Contains(text, "one integer, including 0") {
		t.Fatalf("help missing count contract:\n%s", text)
	}
}

func TestLogCountEmptyRegistry(t *testing.T) {
	app := newApp(t)
	out, _, err := run(t, app, "log", "--count")
	if err != nil || out != "0\n" {
		t.Fatalf("empty registry count = %q err %v", out, err)
	}
}
