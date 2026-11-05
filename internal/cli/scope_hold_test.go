package cli

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/scopeconfig"
	"github.com/p3bot/tk/internal/skill"
	"github.com/p3bot/tk/internal/testgit"
	"github.com/p3bot/tk/internal/token"
)

const (
	holdCueOff     = "name: \"wc\"\nautoCommit: false\n"
	holdCueSoft    = "name:       \"wc\"\nautoCommit: false\nhold: {type: \"soft\"}\n"
	holdCueSoftR   = "name:       \"wc\"\nautoCommit: false\nhold: {type: \"soft\", reason: \"R\"}\n"
	holdCueHard    = "name:       \"wc\"\nautoCommit: false\nhold: {type: \"hard\"}\n"
	holdCueHardR   = "name:       \"wc\"\nautoCommit: false\nhold: {type: \"hard\", reason: \"R\"}\n"
	holdCueSoftR2  = "name:       \"wc\"\nautoCommit: false\nhold: {type: \"soft\", reason: \"R2\"}\n"
	holdCueCleared = "name:       \"wc\"\nautoCommit: false\n"
)

func holdLine(holdType, reason string) string {
	return token.FormatScopeHold(holdType, reason) + "\n"
}

func TestScopeHoldBehaviourTable(t *testing.T) {
	const path = "<path>"
	soft, hard := token.HoldSoft, token.HoldHard
	cases := []struct {
		name    string
		start   string
		args    []string
		wantOut string
		wantErr string
		wantCue string
		exit    int
	}{
		{name: "read off", start: holdCueOff, wantOut: "off\n", wantCue: holdCueOff},
		{name: "read soft", start: holdCueSoft, wantOut: "soft\n", wantErr: holdLine(soft, ""), wantCue: holdCueSoft},
		{name: "read hard with reason", start: holdCueHardR, wantOut: "hard\n", wantErr: holdLine(hard, "R"), wantCue: holdCueHardR},
		{name: "type when off", start: holdCueOff, args: []string{"soft"}, wantOut: path, wantCue: holdCueSoft},
		{name: "hard when off", start: holdCueOff, args: []string{"hard"}, wantOut: path, wantCue: holdCueHard},
		{name: "type and reason when off", start: holdCueOff, args: []string{"soft", "R"}, wantOut: path, wantCue: holdCueSoftR},
		{name: "same type no reason", start: holdCueSoft, args: []string{"soft"}, wantErr: holdLine(soft, "") + "hold is already soft\n", wantCue: holdCueSoft},
		{name: "same type keeps reason", start: holdCueSoftR, args: []string{"soft"}, wantErr: holdLine(soft, "R") + "hold is already soft\n", wantCue: holdCueSoftR},
		{name: "other type keeps reason", start: holdCueSoftR, args: []string{"hard"}, wantOut: path, wantErr: holdLine(soft, "R"), wantCue: holdCueHardR},
		{name: "other type no reason", start: holdCueHard, args: []string{"soft"}, wantOut: path, wantErr: holdLine(hard, ""), wantCue: holdCueSoft},
		{name: "same type and reason", start: holdCueSoftR, args: []string{"soft", "R"}, wantErr: holdLine(soft, "R") + "hold is already soft\n", wantCue: holdCueSoftR},
		{name: "same trimmed reason", start: holdCueSoftR, args: []string{"soft", "  R "}, wantErr: holdLine(soft, "R") + "hold is already soft\n", wantCue: holdCueSoftR},
		{name: "different reason", start: holdCueSoftR, args: []string{"soft", "R2"}, wantOut: path, wantErr: holdLine(soft, "R"), wantCue: holdCueSoftR2},
		{name: "different type and reason", start: holdCueHardR, args: []string{"soft", "R2"}, wantOut: path, wantErr: holdLine(hard, "R"), wantCue: holdCueSoftR2},
		{name: "off when held", start: holdCueHardR, args: []string{"off"}, wantOut: path, wantErr: holdLine(hard, "R"), wantCue: holdCueCleared},
		{name: "off when off", start: holdCueOff, args: []string{"off"}, wantErr: "hold is already off\n", wantCue: holdCueOff},
		{name: "off with reason", start: holdCueSoft, args: []string{"off", "R"}, wantCue: holdCueSoft, exit: exitUsage},
		{name: "not a type", start: holdCueOff, args: []string{"x"}, wantCue: holdCueOff, exit: exitUsage},
		{name: "bool true", start: holdCueOff, args: []string{"true"}, wantCue: holdCueOff, exit: exitUsage},
		{name: "bool false", start: holdCueSoft, args: []string{"false"}, wantCue: holdCueSoft, exit: exitUsage},
		{name: "empty reason", start: holdCueOff, args: []string{"soft", ""}, wantCue: holdCueOff, exit: exitUsage},
		{name: "blank reason", start: holdCueOff, args: []string{"hard", "  "}, wantCue: holdCueOff, exit: exitUsage},
		{name: "reason with U+2028", start: holdCueOff, args: []string{"soft", "a\u2028b"}, wantCue: holdCueOff, exit: exitUsage},
		{name: "reason with a bidi control", start: holdCueOff, args: []string{"soft", "a\u202Eb"}, wantCue: holdCueOff, exit: exitUsage},
		{name: "reason over the limit", start: holdCueOff, args: []string{"soft", strings.Repeat("é", 201)}, wantCue: holdCueOff, exit: exitUsage},
		{
			name: "200 runes with padding", start: holdCueOff, args: []string{"soft", "  " + strings.Repeat("é", 200) + "  "}, wantOut: path,
			wantCue: "name:       \"wc\"\nautoCommit: false\nhold: {type: \"soft\", reason: \"" + strings.Repeat("é", 200) + "\"}\n",
		},
		{name: "unquoted reason", start: holdCueOff, args: []string{"soft", "a", "b"}, wantCue: holdCueOff, exit: exitUsage},
		{
			name: "dash reason after --", start: holdCueOff, args: []string{"soft", "--", "-1 on this"}, wantOut: path,
			wantCue: "name:       \"wc\"\nautoCommit: false\nhold: {type: \"soft\", reason: \"-1 on this\"}\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			app := newApp(t)
			dir := initScope(t, app, "wc")
			writeCue(t, dir, c.start)
			t.Chdir(dir)

			out, errOut, err := run(t, app, append([]string{"scope", "hold"}, c.args...)...)
			if got := ExitCodeFromError(err); got != c.exit {
				t.Fatalf("exit = %d want %d (%v)", got, c.exit, err)
			}
			if c.wantOut == path {
				if strings.TrimSpace(out) != filepath.Join(dir, "tk.cue") {
					t.Fatalf("stdout = %q, want the tk.cue path", out)
				}
			} else if out != c.wantOut {
				t.Fatalf("stdout = %q, want %q", out, c.wantOut)
			}
			if errOut != c.wantErr {
				t.Fatalf("stderr = %q, want %q", errOut, c.wantErr)
			}
			if got := readCueFile(t, dir); got != c.wantCue {
				t.Fatalf("tk.cue:\n%s\nwant:\n%s", got, c.wantCue)
			}
		})
	}
}

func TestScopeHoldUnquotedReasonSaysToQuote(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	t.Chdir(dir)
	_, _, err := run(t, app, "scope", "hold", "soft", "a", "b")
	if err == nil || !strings.Contains(err.Error(), "quote the reason") {
		t.Fatalf("err = %v", err)
	}
}

func TestScopeHoldReportsAMissingDirLikeARead(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	for _, args := range [][]string{{"scope", "hold", "--scope", "wc"}, {"scope", "hold", "soft", "--scope", "wc"}} {
		_, _, err := run(t, app, args...)
		if err == nil || !strings.HasPrefix(err.Error(), token.ConfigUnparseable) {
			t.Fatalf("%v err = %v", args, err)
		}
	}
}

func TestScopeHoldSiblingPinRestoresTkCue(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	writeCue(t, dir, "package wc\n\nname: \"wc\"\nautoCommit: false\n")
	if err := os.WriteFile(filepath.Join(dir, "pin.cue"), []byte("package wc\n\nhold: {type: \"soft\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	before := readCueFile(t, dir)

	_, _, err := run(t, app, "scope", "hold", "off")
	if ExitCodeFromError(err) != exitUsage {
		t.Fatalf("off exit = %d (%v)", ExitCodeFromError(err), err)
	}
	if got := readCueFile(t, dir); got != before {
		t.Fatalf("off must restore tk.cue:\n%s", got)
	}

	_, _, err = run(t, app, "scope", "hold", "hard")
	if err == nil {
		t.Fatal("hard against a soft sibling pin must fail")
	}
	if got := readCueFile(t, dir); got != before {
		t.Fatalf("hard must restore tk.cue:\n%s", got)
	}
}

func TestScopeHoldSelfCommitsEachRewrite(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	_, repo := initGitScope(t, app, "wc", true)

	steps := []struct {
		args []string
		want string
	}{
		{[]string{"soft", "R"}, "tk: scope hold soft"},
		{[]string{"soft", "R"}, ""},
		{[]string{"hard"}, "tk: scope hold hard"},
		{[]string{"off"}, "tk: scope hold off"},
		{[]string{"off"}, ""},
	}
	want := 0
	for _, s := range steps {
		if _, errOut, err := run(t, app, append(append([]string{"scope", "hold"}, s.args...), "--scope", "wc")...); err != nil {
			t.Fatalf("%v: %v (%s)", s.args, err, errOut)
		}
		log := gitLog(t, repo)
		if s.want != "" {
			want++
			if log[0] != s.want {
				t.Fatalf("%v: latest commit = %q, want %q", s.args, log[0], s.want)
			}
		}
		if len(log) != want {
			t.Fatalf("%v: commits = %v, want %d", s.args, log, want)
		}
	}
}

func TestInvalidHoldInTkCueIsUnparseable(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "")
	writeCue(t, dir, "name: \"wc\"\nautoCommit: false\nhold: {type: \"hard\", reason: \"a\\u2028b\"}\n")
	t.Chdir(dir)

	_, errOut, err := run(t, app, "mark", "done", "wc-ab2c")
	if err == nil || !strings.Contains(err.Error(), token.ConfigUnparseable) {
		t.Fatalf("mark err = %v (%s)", err, errOut)
	}
	if strings.Contains(errOut, token.ScopeHold) {
		t.Fatalf("the hold line must give way to config_unparseable: %q", errOut)
	}
	if !strings.Contains(fileBody(t, filepath.Join(dir, "wc-ab2c-work.md")), "status: todo") {
		t.Fatal("mark wrote despite the unusable config")
	}

	out, _, err := run(t, app, "pulse", "hold")
	if err != nil || strings.TrimSpace(out) != "off" {
		t.Fatalf("pulse hold = %q (%v)", out, err)
	}
	out, _, err = run(t, app, "pulse", "hold_reason")
	if err != nil || out != "" {
		t.Fatalf("pulse hold_reason = %q (%v)", out, err)
	}
	out, _, err = run(t, app, "scope", "list")
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Split(strings.TrimRight(out, "\n"), "\t")
	if len(fields) != 6 || fields[4] != "" || fields[5] != "" {
		t.Fatalf("list = %q", out)
	}
}

func TestLegacyBoolHold(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "")
	legacy := "name: \"wc\"\nautoCommit: false\nhold: true\n"
	writeCue(t, dir, legacy)
	t.Chdir(dir)

	out, errOut, err := run(t, app, "scope", "hold")
	if err != nil || out != "soft\n" || errOut != holdLine(token.HoldSoft, "") {
		t.Fatalf("read out %q err %q (%v)", out, errOut, err)
	}
	_, _, err = run(t, app, "mark", "in-progress", "wc-ab2c")
	if ExitCodeFromError(err) != exitFailure {
		t.Fatalf("mark in-progress exit = %d (%v)", ExitCodeFromError(err), err)
	}
	out, _, err = run(t, app, "scope", "hold", "soft")
	if err != nil || out != "" {
		t.Fatalf("hold soft out %q (%v)", out, err)
	}
	if got := readCueFile(t, dir); got != legacy {
		t.Fatalf("hold soft must keep the file:\n%s", got)
	}
	if _, _, err = run(t, app, "scope", "hold", "off"); err != nil {
		t.Fatal(err)
	}
	if got := readCueFile(t, dir); strings.Contains(got, "hold") {
		t.Fatalf("hold off must delete the field:\n%s", got)
	}

	writeCue(t, dir, "name: \"wc\"\nautoCommit: false\nhold: false\n")
	out, errOut, err = run(t, app, "scope", "hold")
	if err != nil || out != "off\n" || errOut != "" {
		t.Fatalf("hold: false out %q err %q (%v)", out, errOut, err)
	}
}

func TestLegacyFormsStayUndocumented(t *testing.T) {
	readme, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	app := newApp(t)
	help, _, err := run(t, app, "scope", "hold", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"skill": skill.Text(), "README": string(readme), "help": help} {
		for _, form := range []string{"hold: true", "hold: false"} {
			if strings.Contains(text, form) {
				t.Errorf("%s mentions %q", name, form)
			}
		}
	}
}

func TestHoldReadFields(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	t.Chdir(dir)

	out, _, err := run(t, app, "pulse")
	if err != nil {
		t.Fatal(err)
	}
	keys := parsePulseKeys(out)
	at := slices.Index(keys, "hold")
	if at < 0 || keys[at+1] != "hold_reason" {
		t.Fatalf("pulse keys = %v", keys)
	}

	assertRead := func(hold, reason string) {
		t.Helper()
		out, _, err := run(t, app, "pulse", "hold")
		if err != nil || strings.TrimSpace(out) != hold {
			t.Fatalf("pulse hold = %q (%v), want %q", out, err, hold)
		}
		out, _, err = run(t, app, "pulse", "hold_reason")
		if err != nil || strings.TrimSpace(out) != reason {
			t.Fatalf("pulse hold_reason = %q (%v), want %q", out, err, reason)
		}
		out, errOut, err := run(t, app, "scope", "list")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(errOut, token.ScopeHold) {
			t.Fatalf("scope list printed the line: %q", errOut)
		}
		fields := strings.Split(strings.TrimRight(out, "\n"), "\t")
		if len(fields) != 6 || fields[4] != hold || fields[5] != reason {
			t.Fatalf("scope list = %q, want hold %q reason %q", out, hold, reason)
		}
	}
	assertRead("off", "")
	for _, step := range [][]string{{"soft"}, {"hard", "a reason"}} {
		if _, _, err := run(t, app, append([]string{"scope", "hold"}, step...)...); err != nil {
			t.Fatal(err)
		}
	}
	assertRead("hard", "a reason")
	if _, _, err := run(t, app, "scope", "hold", "soft"); err != nil {
		t.Fatal(err)
	}
	assertRead("soft", "a reason")
	if _, _, err := run(t, app, "scope", "hold", "off"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, app, "scope", "hold", "hard"); err != nil {
		t.Fatal(err)
	}
	assertRead("hard", "")

	out, _, err = run(t, app, "pulse")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "no reason given") {
		t.Fatalf("display text leaked into pulse: %s", out)
	}
}

func TestMultiScopeCommandsPrintOneBlock(t *testing.T) {
	const header = "scope_hold: Holds on the following scopes:\n"
	commands := map[string][]string{
		"doctor":     {"doctor"},
		"repair all": {"repair", "--all"},
		"reindex":    {"reindex"},
		"search":     {"search", "term"},
		"design":     {"design", "search", "term"},
		"list every": {"list", "--every-scope"},
		"log":        {"log"},
		"sync all":   {"sync", "--all"},
	}
	holds := []struct {
		name  string
		holds map[string]string
		want  string
	}{
		{
			name:  "one held",
			holds: map[string]string{"bar": `{type: "soft"}`},
			want:  header + "  bar: soft: no reason given\n",
		},
		{
			name:  "several held",
			holds: map[string]string{"baz": `{type: "soft"}`, "bar": `{type: "soft"}`},
			want:  header + "  bar: soft: no reason given\n  baz: soft: no reason given\n",
		},
		{
			name:  "mixed types and reasons",
			holds: map[string]string{"bar": `{type: "soft"}`, "baz": `{type: "hard", reason: "this is the hold reason"}`},
			want:  header + "  bar: soft: no reason given\n  baz: hard: this is the hold reason\n",
		},
		{
			name:  "token shaped scope name stays indented",
			holds: map[string]string{"uncommitted": `{type: "hard"}`},
			want:  header + "  uncommitted: hard: no reason given\n",
		},
	}
	for _, h := range holds {
		for cmdName, args := range commands {
			t.Run(h.name+"/"+cmdName, func(t *testing.T) {
				app := newApp(t)
				for _, name := range []string{"bar", "baz", "uncommitted"} {
					dir := initScope(t, app, name)
					body := "name: \"" + name + "\"\nautoCommit: false\n"
					if v, ok := h.holds[name]; ok {
						body += "hold: " + v + "\n"
					}
					writeCue(t, dir, body)
				}
				t.Chdir(t.TempDir())
				_, errOut, _ := run(t, app, args...)
				if !strings.Contains(errOut, h.want) {
					t.Fatalf("stderr = %q, want block %q", errOut, h.want)
				}
				if got := strings.Count(errOut, token.ScopeHold); got != 1 {
					t.Fatalf("scope_hold: count = %d in %q", got, errOut)
				}
				if strings.Contains(errOut, "hold: Soft hold") || strings.Contains(errOut, "hold: Hard hold") {
					t.Fatalf("multi-scope command reached the single-scope line: %q", errOut)
				}
			})
		}
	}
}

func TestOneScopeOfAMultiScopeCommandPrintsTheLine(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	writeCue(t, dir, "name: \"wc\"\nautoCommit: false\nhold: {type: \"hard\", reason: \"R\"}\n")
	t.Chdir(t.TempDir())
	_, errOut, err := run(t, app, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v (%s)", err, errOut)
	}
	if !strings.HasPrefix(errOut, holdLine(token.HoldHard, "R")) {
		t.Fatalf("stderr = %q", errOut)
	}
}

func TestLinePrintingExclusions(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	writeCue(t, dir, "name: \"wc\"\nautoCommit: false\nhold: {type: \"hard\"}\n")
	t.Chdir(dir)
	for _, args := range [][]string{{"scope", "list"}, {"skill"}, {"query", "SELECT 1"}} {
		_, errOut, err := run(t, app, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if strings.Contains(errOut, token.ScopeHold) {
			t.Fatalf("%v printed the line: %q", args, errOut)
		}
	}
}

func TestSoftHoldRefusesEnteringInProgress(t *testing.T) {
	const reason = "migrating the board"
	soft := "name: \"wc\"\nautoCommit: false\nhold: {type: \"soft\", reason: \"" + reason + "\"}\n"
	setup := func(t *testing.T) (*App, string) {
		t.Helper()
		app := newApp(t)
		dir := initScope(t, app, "wc")
		writeCue(t, dir, soft)
		addTicket(t, dir, "wc-ab2c", "todo", "todo", "a0", "# Todo\n", false, "")
		addTicket(t, dir, "wc-bc2d", "blocked", "blocked", "a1", "# Blocked\n", false, "")
		addTicket(t, dir, "wc-fg2c", "active", "in-progress", "a2", "# Active\n", false, "")
		addTicket(t, dir, "wc-re2c", "review", "review", "a3", "# Review\n", false, "")
		t.Chdir(dir)
		return app, dir
	}

	refused := map[string][]string{
		"mark":               {"mark", "in-progress", "wc-ab2c"},
		"mark from blocked":  {"mark", "in-progress", "wc-bc2d"},
		"mark from review":   {"mark", "in-progress", "wc-re2c"},
		"mark same status":   {"mark", "in-progress", "wc-fg2c"},
		"claim":              {"next", "--claim"},
		"create in-progress": {"create", "Started", "in-progress"},
	}
	for name, args := range refused {
		t.Run("refused/"+name, func(t *testing.T) {
			app, dir := setup(t)
			before := snapshotDir(t, dir)
			out, errOut, err := run(t, app, args...)
			if ExitCodeFromError(err) != exitFailure {
				t.Fatalf("exit = %d (%v)", ExitCodeFromError(err), err)
			}
			if out != "" {
				t.Fatalf("stdout = %q", out)
			}
			if errOut != holdLine(token.HoldSoft, reason) {
				t.Fatalf("stderr = %q", errOut)
			}
			assertSnapshot(t, dir, before)
		})
	}

	allowed := map[string][]string{
		"done from blocked":     {"mark", "done", "wc-bc2d"},
		"done from in-progress": {"mark", "done", "wc-fg2c"},
		"todo to review":        {"mark", "review", "wc-ab2c"},
		"create todo":           {"create", "New work", "todo"},
		"create default":        {"create", "New draft"},
		"order":                 {"order", "wc-ab2c", "--last"},
		"meta":                  {"meta", "add", "wc-ab2c", "tags", "x"},
	}
	for name, args := range allowed {
		t.Run("allowed/"+name, func(t *testing.T) {
			app, _ := setup(t)
			if _, errOut, err := run(t, app, args...); err != nil {
				t.Fatalf("%v: %v (%s)", args, err, errOut)
			}
		})
	}

	t.Run("custom active status stays reachable", func(t *testing.T) {
		app := newApp(t)
		dir := initScope(t, app, "wc")
		writeCue(t, dir, "name: \"wc\"\nautoCommit: false\nhold: {type: \"soft\"}\nstatuses: {working: {category: \"active\"}}\n")
		addTicket(t, dir, "wc-ab2c", "todo", "todo", "a0", "# Todo\n", false, "")
		t.Chdir(dir)
		if _, errOut, err := run(t, app, "mark", "working", "wc-ab2c"); err != nil {
			t.Fatalf("%v (%s)", err, errOut)
		}
	})

	t.Run("unknown status stays usage", func(t *testing.T) {
		app, _ := setup(t)
		_, _, err := run(t, app, "mark", "bogus", "wc-ab2c")
		if ExitCodeFromError(err) != exitUsage {
			t.Fatalf("exit = %d (%v)", ExitCodeFromError(err), err)
		}
	})
}

func TestRehomeUnderHold(t *testing.T) {
	const softCue = "hold: {type: \"soft\", reason: \"R\"}\n"
	const hardCue = "hold: {type: \"hard\", reason: \"R\"}\n"
	block := func(scope, holdType string) string {
		return token.FormatScopeHolds([]token.HoldRow{{Scope: scope, Type: holdType, Reason: "R"}}) + "\n"
	}
	cases := []struct {
		name        string
		srcHold     string
		destHold    string
		status      string
		wantRefused bool
		wantStderr  string
	}{
		{name: "in-progress into soft", destHold: softCue, status: "in-progress", wantRefused: true, wantStderr: block("bar", "soft")},
		{name: "todo into soft", destHold: softCue, status: "todo", wantStderr: block("bar", "soft")},
		{name: "in-progress out of soft into off", srcHold: softCue, status: "in-progress", wantStderr: block("foo", "soft")},
		{name: "todo out of soft", srcHold: softCue, status: "todo", wantStderr: block("foo", "soft")},
		{name: "in-progress soft to soft", srcHold: softCue, destHold: softCue, status: "in-progress", wantRefused: true},
		{name: "todo into hard", destHold: hardCue, status: "todo", wantRefused: true, wantStderr: block("bar", "hard")},
		{name: "todo out of hard", srcHold: hardCue, status: "todo", wantRefused: true, wantStderr: block("foo", "hard")},
		{name: "done into hard", destHold: hardCue, status: "done", wantRefused: true, wantStderr: block("bar", "hard")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			app := newApp(t)
			foo := initScope(t, app, "foo")
			bar := initScope(t, app, "bar")
			writeCue(t, foo, "name: \"foo\"\nautoCommit: false\n"+c.srcHold)
			writeCue(t, bar, "name: \"bar\"\nautoCommit: false\n"+c.destHold)
			archived := c.status == "done"
			addTicket(t, foo, "foo-ab2c", "moved", c.status, "a0", "# Moved\n", archived, "")
			fooBefore, barBefore := snapshotDir(t, foo), snapshotDir(t, bar)

			out, errOut, err := run(t, app, "rehome", "foo-ab2c", "bar")
			if c.wantStderr != "" && errOut != c.wantStderr {
				t.Fatalf("stderr = %q, want %q", errOut, c.wantStderr)
			}
			if got := strings.Count(errOut, token.ScopeHold); got != 1 {
				t.Fatalf("scope_hold count = %d in %q", got, errOut)
			}
			if c.wantRefused {
				if ExitCodeFromError(err) != exitFailure {
					t.Fatalf("exit = %d (%v)", ExitCodeFromError(err), err)
				}
				if out != "" {
					t.Fatalf("stdout = %q", out)
				}
				assertSnapshot(t, foo, fooBefore)
				assertSnapshot(t, bar, barBefore)
				return
			}
			if err != nil {
				t.Fatalf("rehome: %v (%s)", err, errOut)
			}
			if !strings.Contains(out, "bar-ab2c-moved.md") {
				t.Fatalf("stdout = %q", out)
			}
		})
	}

	t.Run("design into hard", func(t *testing.T) {
		app := newApp(t)
		foo := initScope(t, app, "foo")
		bar := initScope(t, app, "bar")
		writeCue(t, bar, "name: \"bar\"\nautoCommit: false\n"+hardCue)
		writeDesign(t, foo, "foo-ab2c", "shape", "draft", "2026-01-01T00:00:00Z", "Shape")
		fooBefore, barBefore := snapshotDir(t, foo), snapshotDir(t, bar)
		_, errOut, err := run(t, app, "design", "rehome", "foo-ab2c", "bar")
		if ExitCodeFromError(err) != exitFailure {
			t.Fatalf("exit = %d (%v)", ExitCodeFromError(err), err)
		}
		if errOut != block("bar", "hard") {
			t.Fatalf("stderr = %q", errOut)
		}
		assertSnapshot(t, foo, fooBefore)
		assertSnapshot(t, bar, barBefore)
	})

	t.Run("design out of hard", func(t *testing.T) {
		app := newApp(t)
		foo := initScope(t, app, "foo")
		initScope(t, app, "bar")
		writeCue(t, foo, "name: \"foo\"\nautoCommit: false\n"+hardCue)
		writeDesign(t, foo, "foo-ab2c", "shape", "draft", "2026-01-01T00:00:00Z", "Shape")
		before := snapshotDir(t, foo)
		_, errOut, err := run(t, app, "design", "rehome", "foo-ab2c", "bar")
		if ExitCodeFromError(err) != exitFailure {
			t.Fatalf("exit = %d (%v)", ExitCodeFromError(err), err)
		}
		if errOut != block("foo", "hard") {
			t.Fatalf("stderr = %q", errOut)
		}
		assertSnapshot(t, foo, before)
	})

	t.Run("design moves under soft", func(t *testing.T) {
		app := newApp(t)
		foo := initScope(t, app, "foo")
		bar := initScope(t, app, "bar")
		writeCue(t, bar, "name: \"bar\"\nautoCommit: false\n"+softCue)
		writeDesign(t, foo, "foo-ab2c", "shape", "draft", "2026-01-01T00:00:00Z", "Shape")
		if _, errOut, err := run(t, app, "design", "rehome", "foo-ab2c", "bar"); err != nil {
			t.Fatalf("%v (%s)", err, errOut)
		}
	})
}

func TestRehomeRefusalsLeaveGitHistoryAlone(t *testing.T) {
	requireGit(t)
	hardCue := func(name string) string {
		return "name: \"" + name + "\"\nautoCommit: true\nhold: {type: \"hard\", reason: \"R\"}\n"
	}
	softCue := func(name string) string {
		return "name: \"" + name + "\"\nautoCommit: true\nhold: {type: \"soft\", reason: \"R\"}\n"
	}
	cases := []struct {
		name           string
		srcCue, dstCue func(string) string
		ticketStatus   string
		args           []string
	}{
		{name: "ticket into hard", dstCue: hardCue, ticketStatus: "todo", args: []string{"rehome", "foo-ab2c", "bar"}},
		{name: "ticket out of hard", srcCue: hardCue, ticketStatus: "todo", args: []string{"rehome", "foo-ab2c", "bar"}},
		{name: "in-progress into soft", dstCue: softCue, ticketStatus: "in-progress", args: []string{"rehome", "foo-ab2c", "bar"}},
		{name: "design into hard", dstCue: hardCue, ticketStatus: "todo", args: []string{"design", "rehome", "foo-cd3e", "bar"}},
		{name: "design out of hard", srcCue: hardCue, ticketStatus: "todo", args: []string{"design", "rehome", "foo-cd3e", "bar"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			app := newApp(t)
			foo, fooRepo := initGitScope(t, app, "foo", true)
			bar, barRepo := initGitScope(t, app, "bar", true)
			if c.srcCue != nil {
				writeCue(t, foo, c.srcCue("foo"))
			}
			if c.dstCue != nil {
				writeCue(t, bar, c.dstCue("bar"))
			}
			addTicket(t, foo, "foo-ab2c", "moved", c.ticketStatus, "a0", "# Moved\n", false, "")
			writeDesign(t, foo, "foo-cd3e", "shape", "draft", "2026-01-01T00:00:00Z", "Shape")
			for _, repo := range []string{fooRepo, barRepo} {
				runGit(t, repo, "add", "-A")
				runGit(t, repo, "commit", "-m", "seed")
			}
			fooFiles, barFiles := snapshotDir(t, foo), snapshotDir(t, bar)
			fooLog, barLog := gitLog(t, fooRepo), gitLog(t, barRepo)

			_, errOut, err := run(t, app, c.args...)
			if ExitCodeFromError(err) != exitFailure {
				t.Fatalf("exit = %d (%v) stderr %q", ExitCodeFromError(err), err, errOut)
			}
			var hold *scopeconfig.HoldError
			if !errors.As(err, &hold) {
				t.Fatalf("err = %T %v", err, err)
			}
			assertSnapshot(t, foo, fooFiles)
			assertSnapshot(t, bar, barFiles)
			if got := gitLog(t, fooRepo); !slices.Equal(got, fooLog) {
				t.Fatalf("source log = %v, want %v", got, fooLog)
			}
			if got := gitLog(t, barRepo); !slices.Equal(got, barLog) {
				t.Fatalf("dest log = %v, want %v", got, barLog)
			}
		})
	}
}

func TestHardHoldRefusesWrites(t *testing.T) {
	requireGit(t)
	const reason = "stop everything"
	setup := func(t *testing.T) (*App, string, string, string) {
		t.Helper()
		app := newApp(t)
		dir, repo := initGitScope(t, app, "wc", true)
		addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "")
		addTicket(t, dir, "wc-cd3e", "other", "in-progress", "a1", "# Other\n", false, "")
		writeDesign(t, dir, "wc-ef4g", "shape", "draft", "2026-01-01T00:00:00Z", "Shape")
		if _, errOut, err := run(t, app, "scope", "hold", "hard", reason, "--scope", "wc"); err != nil {
			t.Fatalf("hold: %v (%s)", err, errOut)
		}
		runGit(t, repo, "add", "-A")
		runGit(t, repo, "commit", "-m", "seed")
		sentinel := filepath.Join(t.TempDir(), "editor-ran")
		editor := filepath.Join(t.TempDir(), "editor.sh")
		if err := os.WriteFile(editor, []byte("#!/bin/sh\ntouch "+sentinel+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("EDITOR", editor)
		t.Chdir(dir)
		return app, dir, repo, sentinel
	}

	refused := map[string][]string{
		"create":           {"create", "New"},
		"create edit":      {"create", "New", "--edit"},
		"create terminal":  {"create", "New", "done"},
		"design create":    {"design", "create", "New"},
		"design create ed": {"design", "create", "New", "--edit"},
		"mark done":        {"mark", "done", "wc-cd3e"},
		"mark draft":       {"mark", "draft", "wc-ab2c"},
		"mark in-progress": {"mark", "in-progress", "wc-ab2c"},
		"order":            {"order", "wc-ab2c", "--last"},
		"claim":            {"next", "--claim"},
		"meta set":         {"meta", "set", "wc-ab2c", "summary", "x"},
		"meta add":         {"meta", "add", "wc-ab2c", "tags", "x"},
		"meta remove":      {"meta", "remove", "wc-ab2c", "tags", "x"},
		"design mark":      {"design", "mark", "accepted", "wc-ef4g"},
		"design mark same": {"design", "mark", "draft", "wc-ef4g"},
		"design meta add":  {"design", "meta", "add", "wc-ef4g", "produces", "wc-ab2c"},
		"design meta rm":   {"design", "meta", "remove", "wc-ef4g", "produces", "wc-ab2c"},
		"edit":             {"edit", "wc-ab2c"},
		"design edit":      {"design", "edit", "wc-ef4g"},
	}
	for name, args := range refused {
		t.Run("refused/"+name, func(t *testing.T) {
			app, dir, repo, sentinel := setup(t)
			before, log := snapshotDir(t, dir), gitLog(t, repo)
			out, errOut, err := run(t, app, args...)
			if ExitCodeFromError(err) != exitFailure {
				t.Fatalf("exit = %d (%v) stderr %q", ExitCodeFromError(err), err, errOut)
			}
			if out != "" {
				t.Fatalf("stdout = %q", out)
			}
			if errOut != holdLine(token.HoldHard, reason) {
				t.Fatalf("stderr = %q", errOut)
			}
			var hold *scopeconfig.HoldError
			if !errors.As(err, &hold) {
				t.Fatalf("err = %T %v", err, err)
			}
			assertSnapshot(t, dir, before)
			if got := gitLog(t, repo); !slices.Equal(got, log) {
				t.Fatalf("git log = %v, want %v", got, log)
			}
			if _, statErr := os.Stat(sentinel); statErr == nil {
				t.Fatal("the editor ran")
			}
		})
	}

	allowed := map[string][]string{
		"list":        {"list"},
		"get":         {"get", "wc-ab2c"},
		"next":        {"next"},
		"pulse":       {"pulse"},
		"meta get":    {"meta", "get", "wc-ab2c"},
		"depends":     {"depends", "wc-ab2c"},
		"design list": {"design", "list"},
		"design get":  {"design", "get", "wc-ef4g"},
		"note set":    {"note", "set", "scratch"},
		"lens":        {"lens", "x"},
		"repair":      {"repair"},
		"doctor":      {"doctor"},
		"reindex":     {"reindex"},
		"scope field": {"scope", "field", "set", "owner", "--type", "string"},
	}
	for name, args := range allowed {
		t.Run("allowed/"+name, func(t *testing.T) {
			app, _, _, _ := setup(t)
			_, errOut, err := run(t, app, args...)
			if err != nil {
				t.Fatalf("%v: %v (%s)", args, err, errOut)
			}
			if !strings.Contains(errOut, holdLine(token.HoldHard, reason)) {
				t.Fatalf("stderr = %q", errOut)
			}
		})
	}

	t.Run("allowed/sync pushes earlier commits", func(t *testing.T) {
		app, _, repo, _ := setup(t)
		bare := filepath.Join(t.TempDir(), "remote.git")
		runGit(t, filepath.Dir(bare), "init", "--bare", filepath.Base(bare))
		runGit(t, repo, "remote", "add", "origin", bare)
		runGit(t, repo, "push", "-u", "origin", "HEAD")
		runGit(t, repo, "commit", "--allow-empty", "-m", "earlier work")
		_, errOut, err := run(t, app, "sync")
		if err != nil {
			t.Fatalf("sync: %v (%s)", err, errOut)
		}
		if !strings.Contains(errOut, holdLine(token.HoldHard, reason)) {
			t.Fatalf("stderr = %q", errOut)
		}
		ahead, err := testgit.CombinedAllowFailure(t, repo, "rev-list", "--count", "@{u}..HEAD")
		if err != nil || strings.TrimSpace(ahead) != "0" {
			t.Fatalf("unpushed = %q (%v)", ahead, err)
		}
	})

	t.Run("allowed/scope list keeps quiet", func(t *testing.T) {
		app, _, _, _ := setup(t)
		_, errOut, err := run(t, app, "scope", "list")
		if err != nil || strings.Contains(errOut, token.ScopeHold) {
			t.Fatalf("%v (%q)", err, errOut)
		}
	})

	t.Run("allowed/scope field unset strip", func(t *testing.T) {
		app, dir, _, _ := setup(t)
		if _, errOut, err := run(t, app, "scope", "field", "set", "owner", "--type", "string"); err != nil {
			t.Fatalf("set: %v (%s)", err, errOut)
		}
		addTicket(t, dir, "wc-gh5j", "owned", "todo", "a2", "# Owned\n", false, "owner: me\n")
		_, errOut, err := run(t, app, "scope", "field", "unset", "owner", "--strip")
		if err != nil {
			t.Fatalf("unset: %v (%s)", err, errOut)
		}
		if !strings.Contains(errOut, holdLine(token.HoldHard, reason)) {
			t.Fatalf("stderr = %q", errOut)
		}
		if strings.Contains(fileBody(t, filepath.Join(dir, "wc-gh5j-owned.md")), "owner:") {
			t.Fatal("--strip left the key behind")
		}
	})

	t.Run("allowed/scope auto-commit", func(t *testing.T) {
		app, _, _, _ := setup(t)
		for _, args := range [][]string{{"scope", "auto-commit"}, {"scope", "auto-commit", "false"}} {
			_, errOut, err := run(t, app, args...)
			if err != nil {
				t.Fatalf("%v: %v (%s)", args, err, errOut)
			}
			if !strings.Contains(errOut, holdLine(token.HoldHard, reason)) {
				t.Fatalf("%v stderr = %q", args, errOut)
			}
		}
	})

	t.Run("allowed/scope forget import rebind", func(t *testing.T) {
		app, dir, _, _ := setup(t)
		t.Chdir(t.TempDir())
		steps := [][]string{
			{"scope", "rebind", dir, "--name", "wc"},
			{"scope", "forget", "wc"},
			{"scope", "import", dir},
		}
		for _, args := range steps {
			_, errOut, err := run(t, app, args...)
			if err != nil {
				t.Fatalf("%v: %v (%s)", args, err, errOut)
			}
			if !strings.Contains(errOut, holdLine(token.HoldHard, reason)) {
				t.Fatalf("%v stderr = %q", args, errOut)
			}
		}
	})

	t.Run("allowed/scope rename", func(t *testing.T) {
		app, _, _, _ := setup(t)
		if _, errOut, err := run(t, app, "scope", "rename", "wc", "wd"); err != nil {
			t.Fatalf("%v (%s)", err, errOut)
		}
	})

	t.Run("hold off lifts the stop", func(t *testing.T) {
		app, dir, _, _ := setup(t)
		if _, errOut, err := run(t, app, "scope", "hold", "off"); err != nil {
			t.Fatalf("%v (%s)", err, errOut)
		}
		if strings.Contains(readCueFile(t, dir), "hold") {
			t.Fatalf("tk.cue:\n%s", readCueFile(t, dir))
		}
		if _, errOut, err := run(t, app, "mark", "done", "wc-cd3e"); err != nil {
			t.Fatalf("mark after off: %v (%s)", err, errOut)
		}
	})
}

func TestEditRefusalIsOnlyHard(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "")
	writeDesign(t, dir, "wc-ef4g", "shape", "draft", "2026-01-01T00:00:00Z", "Shape")
	writeCue(t, dir, "name: \"wc\"\nautoCommit: false\nhold: {type: \"soft\"}\n")
	t.Setenv("EDITOR", "true")
	t.Chdir(dir)
	for _, args := range [][]string{{"edit", "wc-ab2c"}, {"design", "edit", "wc-ef4g"}} {
		if _, errOut, err := run(t, app, args...); err != nil {
			t.Fatalf("%v under soft: %v (%s)", args, err, errOut)
		}
	}
}

func TestEditStillOpensWithAnUnusableConfig(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addTicket(t, dir, "wc-ab2c", "work", "todo", "a0", "# Work\n", false, "")
	writeCue(t, dir, "name: \"wc\"\nautoCommit: false\nhold: {type: \"stop\"}\n")
	t.Setenv("EDITOR", "true")
	t.Chdir(dir)
	if _, errOut, err := run(t, app, "edit", "wc-ab2c"); err != nil {
		t.Fatalf("%v (%s)", err, errOut)
	}
}

func TestImportAndRebindWarnHeldDir(t *testing.T) {
	heldCue := "name: \"wc\"\nautoCommit: false\nhold: {type: \"hard\", reason: \"R\"}\n"
	want := holdLine(token.HoldHard, "R")
	newDir := func(t *testing.T, name, cue string) string {
		t.Helper()
		dir := filepath.Join(t.TempDir(), name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		writeCue(t, dir, cue)
		return dir
	}

	app := newApp(t)
	out, errOut, err := run(t, app, "scope", "import", newDir(t, "held", heldCue))
	if err != nil {
		t.Fatalf("import: %v (%s)", err, errOut)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatal("import printed no path")
	}
	if errOut != want {
		t.Fatalf("import stderr = %q", errOut)
	}
	_, errOut, err = run(t, app, "scope", "import", newDir(t, "plain", "name: \"ok\"\nautoCommit: false\n"))
	if err != nil {
		t.Fatalf("import plain: %v (%s)", err, errOut)
	}
	if strings.Contains(errOut, token.ScopeHold) {
		t.Fatalf("plain import warned: %q", errOut)
	}

	app = newApp(t)
	initScope(t, app, "wc")
	_, errOut, err = run(t, app, "scope", "rebind", newDir(t, "next", heldCue), "--name", "wc")
	if err != nil {
		t.Fatalf("rebind onto held dir: %v (%s)", err, errOut)
	}
	if errOut != want {
		t.Fatalf("rebind stderr = %q", errOut)
	}

	app = newApp(t)
	old := initScope(t, app, "wc")
	writeCue(t, old, heldCue)
	_, errOut, err = run(t, app, "scope", "rebind", newDir(t, "next", heldCue), "--name", "wc")
	if err != nil {
		t.Fatalf("rebind both held: %v (%s)", err, errOut)
	}
	if errOut != want {
		t.Fatalf("rebind both held stderr = %q", errOut)
	}
}

func TestLateHoldRefusalPrintsTheLine(t *testing.T) {
	refusal := &scopeconfig.HoldError{Scope: "wc", Hold: scopeconfig.Hold{Type: token.HoldHard, Reason: "R"}}
	e := &engine{}
	late := mapWriteErr(holdExit(e, refusal))
	var buf bytes.Buffer
	fprintError(&buf, late, true)
	if strings.TrimSpace(buf.String()) != token.FormatScopeHold(token.HoldHard, "R") {
		t.Fatalf("late hold stderr = %q", buf.String())
	}
	if ExitCodeFromError(late) != exitFailure {
		t.Fatalf("late hold exit = %d", ExitCodeFromError(late))
	}

	e.holdSeen = map[string]scopeconfig.Hold{"wc": refusal.Hold}
	warned := mapWriteErr(holdExit(e, refusal))
	buf.Reset()
	fprintError(&buf, warned, true)
	if buf.Len() != 0 {
		t.Fatalf("hold already warned must stay quiet, got %q", buf.String())
	}
	if ExitCodeFromError(warned) != exitFailure {
		t.Fatalf("warned hold exit = %d", ExitCodeFromError(warned))
	}
	var hold *scopeconfig.HoldError
	if !errors.As(warned, &hold) || !errors.As(late, &hold) {
		t.Fatal("hold refusal must still be a hold error")
	}

	for name, reported := range map[string]scopeconfig.Hold{
		"type changed":   {Type: token.HoldSoft, Reason: "R"},
		"reason changed": {Type: token.HoldHard, Reason: "older"},
	} {
		e.holdSeen = map[string]scopeconfig.Hold{"wc": reported}
		changed := mapWriteErr(holdExit(e, refusal))
		buf.Reset()
		fprintError(&buf, changed, true)
		if strings.TrimSpace(buf.String()) != token.FormatScopeHold(token.HoldHard, "R") {
			t.Fatalf("%s: stderr = %q", name, buf.String())
		}
		if ExitCodeFromError(changed) != exitFailure {
			t.Fatalf("%s: exit = %d", name, ExitCodeFromError(changed))
		}
	}
}

func TestCommandHelpStatesHoldRefusals(t *testing.T) {
	app := newApp(t)
	out, _, err := run(t, app, "scope", "--help")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "hold bool") {
		t.Fatalf("scope help: %s", out)
	}
	const writeRefused = "A hard hold refuses this write (exit 1)"
	const editorRefused = "A hard hold refuses before the editor opens"
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"mark"}, "A soft or hard hold refuses in-progress, and a hard hold refuses every status"},
		{[]string{"next"}, "A soft or hard hold refuses --claim"},
		{[]string{"create"}, "A soft hold refuses status in-progress, and a hard hold refuses every create, including --edit"},
		{[]string{"rehome"}, "A hard hold on the source or dest refuses, and so does a soft hold on the dest for an in-progress ticket"},
		{[]string{"order"}, writeRefused},
		{[]string{"edit"}, editorRefused},
		{[]string{"meta", "set"}, writeRefused},
		{[]string{"meta", "add"}, writeRefused},
		{[]string{"meta", "remove"}, writeRefused},
		{[]string{"design"}, "A hard hold refuses create, edit, mark, meta add|remove, and rehome"},
		{[]string{"design", "create"}, writeRefused},
		{[]string{"design", "edit"}, editorRefused},
		{[]string{"design", "mark"}, writeRefused},
		{[]string{"design", "meta"}, "A hard hold refuses these writes"},
		{[]string{"design", "rehome"}, "A hard hold on the source or dest refuses"},
	} {
		out, _, err := run(t, app, append(c.args, "--help")...)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(strings.Join(strings.Fields(out), " "), c.want) {
			t.Errorf("%v help does not state %q:\n%s", c.args, c.want, out)
		}
	}
}

func readCueFile(t *testing.T, dir string) string {
	t.Helper()
	return fileBody(t, filepath.Join(dir, "tk.cue"))
}

func fileBody(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// snapshotDir maps every file under dir to its bytes, skipping the lock file.
func snapshotDir(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() == ".tk.lock" {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[p] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func assertSnapshot(t *testing.T, dir string, before map[string]string) {
	t.Helper()
	after := snapshotDir(t, dir)
	for p, want := range before {
		got, ok := after[p]
		if !ok {
			t.Errorf("%s was removed", p)
		} else if got != want {
			t.Errorf("%s changed:\n%s", p, got)
		}
	}
	for p := range after {
		if _, ok := before[p]; !ok {
			t.Errorf("%s was created", p)
		}
	}
}

func TestParseHoldArgsReportsTheFirstProblem(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"off", "R"}, "hold off takes no reason"},
		{[]string{"off", "some", "reason"}, "hold off takes no reason"},
		{[]string{"x", "a", "b", "c"}, `"x" is not a hold type`},
		{[]string{"soft", "a", "b"}, `tk scope hold soft "two words"`},
		{[]string{"hard", "a", "b"}, `tk scope hold hard "two words"`},
	}
	for _, c := range cases {
		_, err := parseHoldArgs(c.args)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("parseHoldArgs(%q) = %v, want %q", c.args, err, c.want)
		}
	}
}

func TestHoldExitNamesTheScopeOnAMultiScopeCommand(t *testing.T) {
	refusal := &scopeconfig.HoldError{Scope: "other", Hold: scopeconfig.Hold{Type: token.HoldHard, Reason: "R"}}
	cases := []struct {
		name  string
		multi bool
		want  string
	}{
		{"one scope", false, token.FormatScopeHold(token.HoldHard, "R")},
		{"two scopes", true, "scope_hold: Holds on the following scopes:\n  other: hard: R"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := holdExit(&engine{holdMulti: c.multi}, refusal)
			var ee *ExitError
			if !errors.As(err, &ee) || ee.Code != exitFailure || ee.AlreadyPrinted {
				t.Fatalf("holdExit = %#v, want an unprinted exit 1", err)
			}
			if got := err.Error(); got != c.want {
				t.Errorf("refusal = %q, want %q", got, c.want)
			}
			var held *scopeconfig.HoldError
			if !errors.As(err, &held) || held != refusal {
				t.Errorf("refusal does not unwrap to the hold error")
			}
		})
	}
}
