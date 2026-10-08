package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/p3bot/tk/internal/token"
)

func TestListEveryScope(t *testing.T) {
	app := newApp(t)
	aa := initScope(t, app, "aa")
	zz := initScope(t, app, "zz")
	mm := initScope(t, app, "mm")
	writeCue(t, aa, "name: \"aa\"\nautoCommit: false\nstatuses: {\n  parked: {category: \"active\"}\n  shipped: {category: \"done\"}\n  hold: {category: \"active\"}\n}\n")
	writeCue(t, zz, "name: \"zz\"\nautoCommit: false\nstatuses: {\n  shipped: {category: \"active\"}\n}\n")
	writeCue(t, mm, "name: \"mm\"\nautoCommit: false\nstatuses: {\n  hold: {category: \"active\"}\n}\n")

	addTicket(t, aa, "aa-m2n4", "todo", "todo", "m0", "# Aa todo\n", false, "")
	addTicket(t, aa, "aa-p5q6", "backlog", "backlog", "m1", "# Aa backlog\n", false, "")
	addTicket(t, aa, "aa-r7s8", "old-done", "done", "m2", "# Aa old done\n", true, "")
	addTicket(t, aa, "aa-v3w4", "parked", "parked", "m3", "# Aa parked\n", false, "")
	addTicket(t, aa, "aa-t9u2", "new-done", "done", "m4", "# Aa new done\n", true, "")
	addTicket(t, aa, "aa-x5y6", "old-ship", "shipped", "m5", "# Aa old ship\n", true, "")
	addTicket(t, aa, "aa-z7a8", "new-ship", "shipped", "m6", "# Aa new ship\n", true, "")
	addTicket(t, aa, "aa-b9c2", "tagged", "todo", "m7", "# Aa tagged\n", false, "tags: [ui]\n")
	addTicket(t, aa, "aa-c4d5", "hold", "hold", "m8", "# Aa hold\n", false, "")
	if err := os.WriteFile(filepath.Join(aa, "aa-d3e4-bad.md"), []byte("---\nid: aa-d3e4\nstatus: [\n---\n# Broken\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	addTicket(t, zz, "zz-b2c3", "early", "todo", "a0", "# Zz early\n", false, "")
	addTicket(t, zz, "zz-d4e5", "parked", "parked", "a1", "# Zz parked\n", false, "")
	addTicket(t, zz, "zz-f6g7", "old-done", "done", "a2", "# Zz old done\n", true, "")
	addTicket(t, zz, "zz-h8j9", "new-done", "done", "a3", "# Zz new done\n", true, "")
	addTicket(t, zz, "zz-k2m3", "plain", "todo", "a4", "# Zz plain\n", false, "")
	addTicket(t, zz, "zz-n4p5", "tagged", "todo", "a5", "# Zz tagged\n", false, "tags: [ui]\n")
	addTicket(t, zz, "zz-q6r7", "shipped", "shipped", "a6", "# Zz shipped\n", false, "")
	addTicket(t, zz, "zz-s8t9", "waits", "todo", "a7", "# Zz waits\n", false, "depends: [aa-m2n4]\n")

	addTicket(t, mm, "mm-b2c3", "todo", "todo", "b0", "# Mm todo\n", false, "")
	addTicket(t, mm, "mm-d4e5", "hold", "hold", "b1", "# Mm hold\n", false, "")

	t.Setenv("TK_SCOPE", "aa")
	designOut, _, err := run(t, app, "design", "create", "Queue shape")
	if err != nil {
		t.Fatalf("design create: %v", err)
	}
	designID := designIDFromPath(t, strings.TrimSpace(designOut))
	// Index mm, then drop the directory so later reads keep its rows without a schema.
	if _, _, err := run(t, app, "list", "--scope", "mm"); err != nil {
		t.Fatalf("seed mm: %v", err)
	}
	if err := os.RemoveAll(mm); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TK_SCOPE", "")

	ids := func(out string) []string {
		t.Helper()
		return listRowIDs(out)
	}

	t.Run("ambient and one scope", func(t *testing.T) {
		t.Chdir(aa)
		out, _, err := run(t, app, "list")
		if err != nil {
			t.Fatalf("ambient list: %v", err)
		}
		got := ids(out)
		if slices.Contains(got, "zz-b2c3") || slices.Contains(got, "mm-b2c3") {
			t.Fatalf("ambient list left its scope: %v", got)
		}
		if !slices.Contains(got, "aa-m2n4") {
			t.Fatalf("ambient list = %v", got)
		}
		out, _, err = run(t, app, "list", "--scope", "zz")
		if err != nil {
			t.Fatalf("list --scope zz: %v", err)
		}
		got = ids(out)
		if slices.Contains(got, "aa-m2n4") || !slices.Contains(got, "zz-b2c3") {
			t.Fatalf("list --scope zz = %v", got)
		}
	})

	t.Run("default board groups by scope", func(t *testing.T) {
		out, errOut, err := run(t, app, "list", "--every-scope")
		if err != nil {
			t.Fatalf("list --every-scope: %v\n%s", err, errOut)
		}
		want := []string{
			"aa-m2n4", "aa-v3w4", "aa-b9c2", "aa-c4d5",
			"mm-b2c3",
			"zz-b2c3", "zz-k2m3", "zz-n4p5", "zz-q6r7", "zz-s8t9",
		}
		if !slices.Equal(ids(out), want) {
			t.Fatalf("default = %v\nwant %v\n%s", ids(out), want, out)
		}
		if strings.Contains(out, "aa-d3e4") || strings.Contains(out, designID) || strings.Contains(out, "zz-d4e5") {
			t.Fatalf("default leaked parse_error, design, or undeclared parked:\n%s", out)
		}
		if !strings.Contains(errOut, "unreachable_scope:") {
			t.Fatalf("stderr missing unreachable_scope: %q", errOut)
		}
		if !strings.Contains(errOut, "parse_error:") {
			t.Fatalf("stderr missing parse_error: %q", errOut)
		}
		row := rowByID(t, out, "zz-s8t9")
		if row != "zz-s8t9\ttodo\tZz waits\taa-m2n4" {
			t.Fatalf("waiting-on = %q", row)
		}
		one, _, err := run(t, app, "list", "--scope", "zz", "todo")
		if err != nil {
			t.Fatal(err)
		}
		if rowByID(t, one, "zz-s8t9") != row {
			t.Fatalf("one-scope waiting-on %q != %q", rowByID(t, one, "zz-s8t9"), row)
		}
	})

	t.Run("status filters", func(t *testing.T) {
		out, _, err := run(t, app, "list", "todo", "--every-scope")
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range ids(out) {
			if !strings.HasPrefix(rowByID(t, out, id), id+"\ttodo\t") {
				t.Fatalf("non-todo row %q", rowByID(t, out, id))
			}
		}
		if !slices.Equal(ids(out), []string{"aa-m2n4", "aa-b9c2", "mm-b2c3", "zz-b2c3", "zz-k2m3", "zz-n4p5", "zz-s8t9"}) {
			t.Fatalf("todo = %v", ids(out))
		}

		out, _, err = run(t, app, "list", "done", "--every-scope")
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(ids(out), []string{"aa-t9u2", "aa-r7s8", "zz-h8j9", "zz-f6g7"}) {
			t.Fatalf("done reverse = %v", ids(out))
		}
		allDone, _, err := run(t, app, "list", "done", "--every-scope", "--all")
		if err != nil {
			t.Fatal(err)
		}
		if allDone != out {
			t.Fatalf("done --all =\n%s\ndone =\n%s", allDone, out)
		}

		out, _, err = run(t, app, "list", "--every-scope", "--open")
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(ids(out), "aa-p5q6") || slices.Contains(ids(out), "aa-t9u2") {
			t.Fatalf("--open = %v", ids(out))
		}
		out, _, err = run(t, app, "list", "--every-scope", "--all")
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(ids(out), "aa-t9u2") || !slices.Contains(ids(out), "zz-d4e5") {
			t.Fatalf("--all missing archive done or undeclared status text: %v", ids(out))
		}
	})

	t.Run("custom status", func(t *testing.T) {
		out, _, err := run(t, app, "list", "parked", "--every-scope")
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(ids(out), []string{"aa-v3w4"}) {
			t.Fatalf("parked = %v", ids(out))
		}
		out, _, err = run(t, app, "list", "parked", "todo", "--every-scope")
		if err != nil {
			t.Fatal(err)
		}
		got := ids(out)
		if !slices.Contains(got, "zz-b2c3") || slices.Contains(got, "zz-d4e5") || !slices.Contains(got, "aa-v3w4") {
			t.Fatalf("parked+todo = %v", got)
		}
		out, _, err = run(t, app, "list", "hold", "--every-scope")
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(ids(out), []string{"aa-c4d5"}) {
			t.Fatalf("hold = %v", ids(out))
		}
		out, _, err = run(t, app, "list", "shipped", "--every-scope")
		if err != nil {
			t.Fatal(err)
		}
		// aa shipped is terminal (reverse). zz shipped is active (forward).
		if !slices.Equal(ids(out), []string{"aa-z7a8", "aa-x5y6", "zz-q6r7"}) {
			t.Fatalf("shipped categories = %v", ids(out))
		}
		out, _, err = run(t, app, "list", "nope", "--every-scope")
		if ExitCodeFromError(err) != exitUsage || out != "" {
			t.Fatalf("unknown status exit %d out %q err %v", ExitCodeFromError(err), out, err)
		}
	})

	t.Run("lens and tag", func(t *testing.T) {
		if _, _, err := run(t, app, "lens", "other", "--scope", "aa"); err != nil {
			t.Fatal(err)
		}
		one, errOut, err := run(t, app, "list", "--scope", "aa")
		if err != nil {
			t.Fatal(err)
		}
		if slices.Contains(ids(one), "aa-b9c2") {
			t.Fatalf("lens failed to hide aa-b9c2: %v", ids(one))
		}
		if !strings.Contains(errOut, "lens:") {
			t.Fatalf("one-scope list should echo the lens: %q", errOut)
		}
		out, errOut, err := run(t, app, "list", "--every-scope")
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(ids(out), "aa-b9c2") {
			t.Fatalf("--every-scope hid a lens row: %v", ids(out))
		}
		if strings.Contains(errOut, "lens:") {
			t.Fatalf("lens echo on --every-scope: %q", errOut)
		}
		again, againErr, err := run(t, app, "list", "--every-scope", "--no-lens")
		if err != nil {
			t.Fatal(err)
		}
		if again != out || strings.Contains(againErr, "lens:") {
			t.Fatal("--no-lens changed --every-scope")
		}

		out, _, err = run(t, app, "list", "--every-scope", "--tag", "ui")
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(ids(out), []string{"aa-b9c2", "zz-n4p5"}) {
			t.Fatalf("--tag ui = %v", ids(out))
		}
		out, errOut, err = run(t, app, "list", "--every-scope", "--tag", "ghost")
		if err != nil {
			t.Fatal(err)
		}
		if out != "" {
			t.Fatalf("unused tag rows = %q", out)
		}
		if !strings.Contains(errOut, token.FormatTagUnknown("ghost")) {
			t.Fatalf("stderr = %q", errOut)
		}
	})

	t.Run("usage", func(t *testing.T) {
		out, _, err := run(t, app, "list", "--every-scope", "--scope", "aa")
		if ExitCodeFromError(err) != exitUsage || out != "" {
			t.Fatalf("--every-scope --scope exit %d out %q err %v", ExitCodeFromError(err), out, err)
		}
		out, _, err = run(t, app, "list", "--every-scope", "--all", "--open")
		if ExitCodeFromError(err) != exitUsage || out != "" {
			t.Fatalf("--all --open exit %d out %q err %v", ExitCodeFromError(err), out, err)
		}
		out, _, err = run(t, app, "list", "--scope", "all")
		if err == nil || out != "" || strings.Contains(out, "aa-m2n4") {
			t.Fatalf("--scope all out %q err %v", out, err)
		}
		if !strings.Contains(err.Error(), `unknown scope "all"`) && !strings.Contains(err.Error(), "all") {
			t.Fatalf("err = %v", err)
		}
		t.Setenv("TK_SCOPE", "all")
		out, _, err = run(t, app, "list")
		if err == nil || strings.Contains(out, "zz-b2c3") {
			t.Fatalf("TK_SCOPE=all listed every scope: out %q err %v", out, err)
		}
		out, _, err = run(t, app, "list", "--every-scope")
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(ids(out), "aa-m2n4") || !slices.Contains(ids(out), "zz-b2c3") {
			t.Fatalf("TK_SCOPE=all changed --every-scope: %v", ids(out))
		}

		help, errOut, err := run(t, app, "list", "--help")
		if err != nil {
			t.Fatal(err)
		}
		text := help + errOut
		for _, want := range []string{
			"--every-scope lists every",
			"registered scope",
			"Scopes that do not declare a custom",
			"name contribute no rows for it",
			"one scope applies the lens",
			"--every-scope ignores the lens",
			"--scope stays",
			"one scope",
			"usage error",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("help missing %q\n%s", want, text)
			}
		}
	})
}

func TestListEveryScopeEmptyRegistry(t *testing.T) {
	app := newApp(t)
	out, _, err := run(t, app, "list", "--every-scope")
	if err != nil || out != "" {
		t.Fatalf("empty registry out %q err %v", out, err)
	}
	out, _, err = run(t, app, "list", "todo", "--every-scope")
	if err != nil || out != "" {
		t.Fatalf("empty todo out %q err %v", out, err)
	}
	out, _, err = run(t, app, "ls", "--every-scope")
	if err != nil || out != "" {
		t.Fatalf("ls alias out %q err %v", out, err)
	}
}

func rowByID(t *testing.T, out, id string) string {
	t.Helper()
	for _, line := range lines(out) {
		if strings.HasPrefix(line, id+"\t") {
			return line
		}
	}
	t.Fatalf("missing row %s in %q", id, out)
	return ""
}
