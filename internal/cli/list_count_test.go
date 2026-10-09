package cli

import (
	"strconv"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/token"
)

func TestListCountMatchesRows(t *testing.T) {
	app := newApp(t)
	aa := initScope(t, app, "aa")
	zz := initScope(t, app, "zz")
	writeCue(t, aa, "name: \"aa\"\nautoCommit: false\nstatuses: {\n  parked: {category: \"active\"}\n}\n")

	addTicket(t, aa, "aa-m2n4", "todo", "todo", "a0", "# Todo\n", false, "")
	addTicket(t, aa, "aa-p5q6", "done", "done", "a1", "# Done\n", true, "")
	addTicket(t, aa, "aa-r7s8", "parked", "parked", "a2", "# Parked\n", false, "")
	addTicket(t, aa, "aa-t9u2", "tagged", "todo", "a3", "# Tagged\n", false, "tags: [ui]\ndepends: [aa-m3ss]\n")
	addTicket(t, zz, "zz-b2c3", "done", "done", "b0", "# Zz done\n", true, "")

	rows, rowErr, err := run(t, app, "list", "--scope", "aa")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	got, gotErr, err := run(t, app, "list", "--count", "--scope", "aa")
	if err != nil {
		t.Fatalf("list --count: %v", err)
	}
	if got != strconv.Itoa(len(lines(rows)))+"\n" {
		t.Fatalf("count = %q want %d rows from %q", got, len(lines(rows)), rows)
	}
	if gotErr != rowErr {
		t.Fatalf("stderr changed\nlist %q\ncount %q", rowErr, gotErr)
	}
	if !strings.Contains(gotErr, "depends_dangling:") {
		t.Fatalf("count dropped depends token: %q", gotErr)
	}

	alias, aliasErr, err := run(t, app, "ls", "--count", "--scope", "aa")
	if err != nil || alias != got || aliasErr != gotErr {
		t.Fatalf("ls --count = %q err %v stderr %q", alias, err, aliasErr)
	}

	doneRows, _, err := run(t, app, "list", "done", "--scope", "aa")
	if err != nil {
		t.Fatal(err)
	}
	doneCount, _, err := run(t, app, "list", "done", "--count", "--scope", "aa")
	if err != nil {
		t.Fatal(err)
	}
	if doneCount != strconv.Itoa(len(lines(doneRows)))+"\n" {
		t.Fatalf("done count = %q rows %q", doneCount, doneRows)
	}

	zzCount, _, err := run(t, app, "list", "--count", "--scope", "zz")
	if err != nil || zzCount != "0\n" {
		t.Fatalf("empty board count = %q err %v", zzCount, err)
	}

	if _, _, err := run(t, app, "lens", "ui", "--scope", "aa"); err != nil {
		t.Fatal(err)
	}
	lensRows, lensErr, err := run(t, app, "list", "--scope", "aa")
	if err != nil {
		t.Fatal(err)
	}
	lensCount, countErr, err := run(t, app, "list", "--count", "--scope", "aa")
	if err != nil {
		t.Fatal(err)
	}
	if lensCount != strconv.Itoa(len(lines(lensRows)))+"\n" || countErr != lensErr {
		t.Fatalf("lens count = %q stderr %q\nrows %q stderr %q", lensCount, countErr, lensRows, lensErr)
	}
	if !strings.Contains(countErr, "lens:") {
		t.Fatalf("lens echo missing: %q", countErr)
	}
	bare, bareErr, err := run(t, app, "list", "--count", "--no-lens", "--scope", "aa")
	if err != nil {
		t.Fatal(err)
	}
	if bare != got || strings.Contains(bareErr, "lens:") {
		t.Fatalf("--no-lens count = %q stderr %q", bare, bareErr)
	}

	everyRows, everyErr, err := run(t, app, "list", "--every-scope")
	if err != nil {
		t.Fatal(err)
	}
	everyCount, countErr, err := run(t, app, "list", "--every-scope", "--count")
	if err != nil {
		t.Fatal(err)
	}
	if countErr != everyErr {
		t.Fatalf("every stderr\nlist %q\ncount %q", everyErr, countErr)
	}
	if strings.Contains(countErr, "lens:") {
		t.Fatalf("--every-scope --count echoed the lens: %q", countErr)
	}
	byScope := map[string]int{}
	for _, row := range lines(everyRows) {
		id := strings.Split(row, "\t")[0]
		scope, _, _ := strings.Cut(id, "-")
		byScope[scope]++
	}
	wantEvery := "aa\t" + strconv.Itoa(byScope["aa"]) + "\nzz\t0\n"
	if everyCount != wantEvery {
		t.Fatalf("every count = %q want %q\nrows %q", everyCount, wantEvery, everyRows)
	}

	parked, _, err := run(t, app, "list", "parked", "--every-scope", "--count")
	if err != nil {
		t.Fatal(err)
	}
	if parked != "aa\t1\nzz\t0\n" {
		t.Fatalf("parked count = %q", parked)
	}

	ghost, ghostErr, err := run(t, app, "list", "--every-scope", "--tag", "ghost", "--count")
	if err != nil {
		t.Fatal(err)
	}
	if ghost != "aa\t0\nzz\t0\n" {
		t.Fatalf("ghost count = %q", ghost)
	}
	if !strings.Contains(ghostErr, token.FormatTagUnknown("ghost")) {
		t.Fatalf("ghost stderr = %q", ghostErr)
	}

	out, _, err := run(t, app, "list", "nope", "--count", "--scope", "aa")
	if ExitCodeFromError(err) != exitUsage || out != "" {
		t.Fatalf("unknown status exit %d out %q err %v", ExitCodeFromError(err), out, err)
	}
	out, _, err = run(t, app, "list", "--count", "--all", "--open", "--scope", "aa")
	if ExitCodeFromError(err) != exitUsage || out != "" {
		t.Fatalf("--all --open exit %d out %q err %v", ExitCodeFromError(err), out, err)
	}

	help, errOut, err := run(t, app, "list", "--help")
	if err != nil {
		t.Fatal(err)
	}
	text := help + errOut
	for _, want := range []string{
		"--count",
		"bare integer, including 0",
		"including zeros",
		"still prints 0",
		"empty registry stays empty",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("help missing %q\n%s", want, text)
		}
	}
}

func TestListCountEmptyRegistry(t *testing.T) {
	app := newApp(t)
	out, _, err := run(t, app, "list", "--every-scope", "--count")
	if err != nil || out != "" {
		t.Fatalf("empty registry out %q err %v", out, err)
	}
}
