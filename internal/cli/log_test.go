package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/p3bot/tk/internal/token"
)

func localDay(offset int) time.Time {
	now := time.Now()
	y, m, d := now.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, now.Location()).AddDate(0, 0, offset)
}

func localStamp(day time.Time, hour, min, sec int) string {
	y, m, d := day.Date()
	return time.Date(y, m, d, hour, min, sec, 0, day.Location()).Format(time.RFC3339)
}

func logIDs(out string) []string {
	var ids []string
	for _, row := range lines(out) {
		fields := strings.Split(row, "\t")
		if len(fields) < 2 {
			ids = append(ids, row)
			continue
		}
		ids = append(ids, fields[1])
	}
	return ids
}

func TestLogDefaultIsDoneTodayAcrossScopes(t *testing.T) {
	app := newApp(t)
	wc := initScope(t, app, "wc")
	ot := initScope(t, app, "ot")
	today := localStamp(localDay(0), 12, 0, 0)
	yesterday := localStamp(localDay(-1), 12, 0, 0)

	addTicket(t, wc, "wc-ab2c", "done-today", "done", "a0", "# Done today\n", true, "changed: "+today+"\n")
	addTicket(t, wc, "wc-de34", "done-yday", "done", "a1", "# Done yesterday\n", true, "changed: "+yesterday+"\n")
	addTicket(t, wc, "wc-jk9m", "todo-today", "todo", "a2", "# Todo today\n", false, "changed: "+today+"\n")
	addTicket(t, ot, "ot-ab2c", "done-today", "done", "a0", "# Other done\n", true, "changed: "+today+"\n")
	if _, _, err := run(t, app, "design", "create", "Not a ticket", "--scope", "wc"); err != nil {
		t.Fatalf("design create: %v", err)
	}
	if _, _, err := run(t, app, "lens", "ui", "--scope", "wc"); err != nil {
		t.Fatalf("lens: %v", err)
	}

	out, errOut, err := run(t, app, "log")
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	todayOut, todayErr, todayErrRun := run(t, app, "log", "--today")
	if todayErrRun != nil {
		t.Fatalf("log --today: %v", todayErrRun)
	}
	if out != todayOut || errOut != todayErr {
		t.Fatalf("log and log --today differ\nout %q\ntoday %q\nerr %q / %q", out, todayOut, errOut, todayErr)
	}
	if strings.Contains(errOut, "lens:") {
		t.Fatalf("log applied the lens: %q", errOut)
	}
	if strings.Contains(out, "/design/") || strings.Contains(out, "Todo today") || strings.Contains(out, "Done yesterday") {
		t.Fatalf("default log = %q", out)
	}
	got := logIDs(out)
	if len(got) != 2 || got[0] == got[1] {
		t.Fatalf("ids = %v, want both scopes' done-today rows", got)
	}
	want := map[string]bool{"wc-ab2c": true, "ot-ab2c": true}
	for _, id := range got {
		if !want[id] {
			t.Fatalf("ids = %v", got)
		}
	}
	for _, row := range lines(out) {
		fields := strings.Split(row, "\t")
		if len(fields) != 5 || fields[1] == "" || fields[2] != "done" || fields[4] == "" {
			t.Fatalf("row = %q", row)
		}
		if !filepath.IsAbs(fields[4]) {
			t.Fatalf("path = %q", fields[4])
		}
	}
}

func TestLogDateStatusScopeAndOrder(t *testing.T) {
	app := newApp(t)
	wc := initScope(t, app, "wc")
	ot := initScope(t, app, "ot")
	day := time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local)
	onDay := localStamp(day, 12, 0, 0)
	later := localStamp(day, 18, 0, 0)
	same := localStamp(day, 18, 0, 0)
	next := localStamp(day.AddDate(0, 0, 1), 12, 0, 0)
	before := localStamp(day, 0, 0, 0)
	justBefore := day.Add(-time.Minute).Format(time.RFC3339)

	addTicket(t, wc, "wc-zz99", "late", "cancelled", "a0", "# Late\n", true, "changed: "+later+"\n")
	addTicket(t, wc, "wc-ab2c", "same", "cancelled", "a1", "# Same instant\n", true, "changed: "+same+"\n")
	addTicket(t, wc, "wc-de34", "noon", "cancelled", "a2", "# Noon\n", true, "changed: "+onDay+"\n")
	addTicket(t, wc, "wc-jk9m", "edge", "cancelled", "a3", "# Edge\n", true, "changed: "+before+"\n")
	addTicket(t, wc, "wc-np23", "before", "cancelled", "a4", "# Before\n", true, "changed: "+justBefore+"\n")
	addTicket(t, wc, "wc-qr45", "done-that-day", "done", "a5", "# Done that day\n", true, "changed: "+onDay+"\n")
	addTicket(t, ot, "ot-ab2c", "other", "cancelled", "a0", "# Other\n", true, "changed: "+onDay+"\n")
	addTicket(t, wc, "wc-st67", "next", "cancelled", "a6", "# Next day\n", true, "changed: "+next+"\n")

	out, _, err := run(t, app, "log", "cancelled", "--date", "2026-09-01")
	if err != nil {
		t.Fatalf("log cancelled --date: %v", err)
	}
	if got, want := logIDs(out), []string{"wc-ab2c", "wc-zz99", "ot-ab2c", "wc-de34", "wc-jk9m"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("ids = %v, want %v\n%s", got, want, out)
	}
	if strings.Contains(out, "wc-np23") || strings.Contains(out, "wc-st67") || strings.Contains(out, "wc-qr45") {
		t.Fatalf("date filter leaked rows:\n%s", out)
	}

	scoped, _, err := run(t, app, "log", "--date", "2026-09-01", "--scope", "wc")
	if err != nil {
		t.Fatalf("log --date --scope: %v", err)
	}
	if got := logIDs(scoped); len(got) != 1 || got[0] != "wc-qr45" {
		t.Fatalf("scoped done = %v\n%s", got, scoped)
	}
}

func TestLogAllAndPositionalReplacesAll(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	today := localStamp(localDay(0), 12, 0, 0)
	yesterday := localStamp(localDay(-1), 12, 0, 0)
	addTicket(t, dir, "wc-ab2c", "todo", "todo", "a0", "# Todo today\n", false, "changed: "+today+"\n")
	addTicket(t, dir, "wc-de34", "old", "done", "a1", "# Done yesterday\n", true, "changed: "+yesterday+"\n")
	addTicket(t, dir, "wc-jk9m", "new", "done", "a2", "# Done today\n", true, "changed: "+today+"\n")

	out, _, err := run(t, app, "log", "--all", "--today")
	if err != nil {
		t.Fatalf("log --all --today: %v", err)
	}
	got := logIDs(out)
	if strings.Join(got, ",") != "wc-ab2c,wc-jk9m" {
		t.Fatalf("--all --today = %v, want today's todo then done by id", got)
	}
	if strings.Contains(out, "wc-de34") {
		t.Fatalf("--all --today included yesterday:\n%s", out)
	}

	bare, _, err := run(t, app, "log", "done")
	if err != nil {
		t.Fatalf("log done: %v", err)
	}
	withAll, _, err := run(t, app, "log", "done", "--all")
	if err != nil {
		t.Fatalf("log done --all: %v", err)
	}
	if bare != withAll {
		t.Fatalf("done --all = %q, done = %q", withAll, bare)
	}
	if !strings.Contains(bare, "wc-jk9m") || strings.Contains(bare, "wc-ab2c") || strings.Contains(bare, "wc-de34") {
		t.Fatalf("log done = %q", bare)
	}
}

func TestLogMissingAndBadChanged(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	today := localStamp(localDay(0), 12, 0, 0)
	addTicket(t, dir, "wc-ab2c", "good", "done", "a0", "# Good\n", true, "changed: "+today+"\n")
	addTicket(t, dir, "wc-de34", "missing", "done", "a1", "# Missing\n", true, "")
	badPath := filepath.Join(dir, "archive", "wc-jk9m-bad.md")
	addTicket(t, dir, "wc-jk9m", "bad", "done", "a2", "# Bad\n", true, "changed: not-a-time\n")
	addTicket(t, dir, "wc-np23", "todo-bad", "todo", "a3", "# Todo bad\n", false, "changed: not-a-time\n")
	addTicket(t, dir, "wc-qr45", "broken", "done", "a4", "# Broken\n", true, "changed: "+today+"\n")
	if err := os.WriteFile(filepath.Join(dir, "archive", "wc-qr45-broken.md"), []byte("---\nid: [\n---\n# Broken\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, errOut, err := run(t, app, "log")
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	rows := lines(out)
	if len(rows) != 1 || !strings.Contains(rows[0], "wc-ab2c") {
		t.Fatalf("rows = %q", out)
	}
	if strings.Contains(out, "wc-de34") || strings.Contains(out, "wc-jk9m") || strings.Contains(out, "wc-qr45") || strings.Contains(out, "wc-np23") {
		t.Fatalf("excluded ticket leaked:\n%s", out)
	}
	want := token.FormatChangedNotRFC3339("wc-jk9m", "not-a-time", badPath)
	if !strings.Contains(errOut, want) {
		t.Fatalf("stderr = %q, want %q", errOut, want)
	}
	if strings.Contains(errOut, "wc-np23") {
		t.Fatalf("bad stamp outside the status filter was reported:\n%s", errOut)
	}
}

func TestLogUsageExits(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	day := localStamp(time.Date(2026, 10, 1, 0, 0, 0, 0, time.Local), 12, 0, 0)
	other := localStamp(time.Date(2026, 10, 2, 0, 0, 0, 0, time.Local), 12, 0, 0)
	addTicket(t, dir, "wc-ab2c", "one", "done", "a0", "# One\n", true, "changed: "+day+"\n")
	addTicket(t, dir, "wc-de34", "two", "done", "a1", "# Two\n", true, "changed: "+other+"\n")

	usage := [][]string{
		{"log", "--today", "--date", "2026-10-01"},
		{"log", "--since", "2026-10-07", "--until", "2026-10-01"},
		{"log", "--until", "2026-10-01", "--since", "2026-10-07"},
		{"log", "--today", "--yesterday"},
		{"log", "--since", "2026-10-01", "--date", "2026-10-01"},
		{"log", "--date", "2026/10/01"},
		{"log", "--date", "2026-10-7"},
		{"log", "nope"},
		{"log", "nope", "--scope", "wc"},
	}
	for _, args := range usage {
		out, _, err := run(t, app, args...)
		if ExitCodeFromError(err) != exitUsage {
			t.Errorf("%v exit = %d want 2 (err=%v)", args, ExitCodeFromError(err), err)
		}
		if out != "" {
			t.Errorf("%v printed TSV %q", args, out)
		}
	}

	out, _, err := run(t, app, "log", "--since", "2026-10-01", "--until", "2026-10-01")
	if err != nil {
		t.Fatalf("same day: %v", err)
	}
	if got := lines(out); len(got) != 1 || !strings.Contains(got[0], "wc-ab2c") {
		t.Fatalf("same day = %q", out)
	}
	flipped, _, err := run(t, app, "log", "--until", "2026-10-01", "--since", "2026-10-01")
	if err != nil {
		t.Fatalf("flipped same day: %v", err)
	}
	if flipped != out {
		t.Fatalf("flag order changed the range\n%q\n%q", out, flipped)
	}
}

func TestLogSinceUntilAndFutureSince(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	oldDay := localDay(-40)
	sinceDay := localDay(-10)
	yday := localDay(-1)
	today := localDay(0)

	addTicket(t, dir, "wc-ab2c", "old", "done", "a0", "# Old\n", true, "changed: "+localStamp(oldDay, 12, 0, 0)+"\n")
	addTicket(t, dir, "wc-de34", "today", "done", "a1", "# Today\n", true, "changed: "+localStamp(today, 12, 0, 0)+"\n")
	addTicket(t, dir, "wc-z234", "today-start", "done", "a2", "# Today start\n", true, "changed: "+localStamp(today, 0, 0, 0)+"\n")
	addTicket(t, dir, "wc-xy23", "yday-end", "done", "a3", "# Yesterday end\n", true, "changed: "+today.Add(-time.Minute).Format(time.RFC3339)+"\n")
	addTicket(t, dir, "wc-jk9m", "yday", "done", "a4", "# Yesterday\n", true, "changed: "+localStamp(yday, 12, 0, 0)+"\n")
	addTicket(t, dir, "wc-vw89", "yday-start", "done", "a5", "# Yesterday start\n", true, "changed: "+localStamp(yday, 0, 0, 0)+"\n")
	addTicket(t, dir, "wc-np23", "before-yday", "done", "a6", "# Before yesterday\n", true, "changed: "+yday.Add(-time.Minute).Format(time.RFC3339)+"\n")
	addTicket(t, dir, "wc-qr45", "since-start", "done", "a7", "# Since start\n", true, "changed: "+localStamp(sinceDay, 0, 0, 0)+"\n")
	addTicket(t, dir, "wc-st67", "before-since", "done", "a8", "# Before since\n", true, "changed: "+sinceDay.Add(-time.Minute).Format(time.RFC3339)+"\n")

	out, _, err := run(t, app, "log", "--since", sinceDay.Format("2006-01-02"))
	if err != nil {
		t.Fatalf("since: %v", err)
	}
	if got, want := logIDs(out), []string{"wc-de34", "wc-z234", "wc-xy23", "wc-jk9m", "wc-vw89", "wc-np23", "wc-qr45"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("since = %v, want %v\n%s", got, want, out)
	}

	out, _, err = run(t, app, "log", "--until", oldDay.Format("2006-01-02"))
	if err != nil {
		t.Fatalf("until: %v", err)
	}
	if got := logIDs(out); len(got) != 1 || got[0] != "wc-ab2c" {
		t.Fatalf("until = %v\n%s", got, out)
	}

	tomorrow := localDay(1).Format("2006-01-02")
	out, _, err = run(t, app, "log", "--since", tomorrow)
	if err != nil {
		t.Fatalf("future since: %v", err)
	}
	if out != "" {
		t.Fatalf("future since printed %q", out)
	}

	out, _, err = run(t, app, "log", "--yesterday")
	if err != nil {
		t.Fatalf("yesterday: %v", err)
	}
	if got, want := logIDs(out), []string{"wc-xy23", "wc-jk9m", "wc-vw89"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("yesterday = %v, want %v\n%s", got, want, out)
	}
}

func TestLogTagsIgnoreLens(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	today := localStamp(localDay(0), 12, 0, 0)
	addTicket(t, dir, "wc-ab2c", "api", "done", "a0", "# Api\n", true, "changed: "+today+"\ntags: [api]\n")
	addTicket(t, dir, "wc-de34", "bare", "done", "a1", "# Bare\n", true, "changed: "+today+"\n")
	if _, _, err := run(t, app, "lens", "ui", "--scope", "wc"); err != nil {
		t.Fatalf("lens: %v", err)
	}

	out, errOut, err := run(t, app, "log", "--scope", "wc")
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if !strings.Contains(out, "wc-ab2c") || !strings.Contains(out, "wc-de34") {
		t.Fatalf("lens hid a row:\n%s", out)
	}
	if strings.Contains(errOut, "lens:") {
		t.Fatalf("lens echo on log: %q", errOut)
	}

	tagged, errOut, err := run(t, app, "log", "--tag", "api")
	if err != nil {
		t.Fatalf("log --tag: %v", err)
	}
	if strings.Contains(tagged, "wc-de34") || !strings.Contains(tagged, "wc-ab2c") {
		t.Fatalf("--tag = %q", tagged)
	}
	if errOut != "" {
		t.Fatalf("known tag stderr = %q", errOut)
	}

	empty, errOut, err := run(t, app, "log", "--tag", "ghost", "--tag", "api")
	if err != nil {
		t.Fatalf("log --tag ghost: %v", err)
	}
	if !strings.Contains(empty, "wc-ab2c") || strings.Contains(empty, "wc-de34") {
		t.Fatalf("OR tag filter = %q", empty)
	}
	if !strings.Contains(errOut, token.FormatTagUnknown("ghost")) {
		t.Fatalf("stderr = %q", errOut)
	}
}

func TestLogScopeAllIsAName(t *testing.T) {
	app := newApp(t)
	wc := initScope(t, app, "wc")
	today := localStamp(localDay(0), 12, 0, 0)
	addTicket(t, wc, "wc-ab2c", "done", "done", "a0", "# Wc\n", true, "changed: "+today+"\n")

	out, _, err := run(t, app, "log", "--scope", "all")
	if err == nil || ExitCodeFromError(err) == exitUsage {
		t.Fatalf("missing scope all: exit %d err %v", ExitCodeFromError(err), err)
	}
	if !strings.Contains(err.Error(), `unknown scope "all"`) {
		t.Fatalf("err = %v", err)
	}
	if out != "" {
		t.Fatalf("stdout = %q", out)
	}

	allDir := initScope(t, app, "all")
	addTicket(t, allDir, "all-ab2c", "done", "done", "a0", "# All\n", true, "changed: "+today+"\n")
	out, _, err = run(t, app, "log", "--scope", "all")
	if err != nil {
		t.Fatalf("scope all: %v", err)
	}
	if strings.Contains(out, "wc-ab2c") || !strings.Contains(out, "all-ab2c") {
		t.Fatalf("scope all = %q", out)
	}
}

func TestLogCustomStatusAcrossScopes(t *testing.T) {
	app := newApp(t)
	wc := initScope(t, app, "wc")
	ot := initScope(t, app, "ot")
	writeCue(t, wc, "name: \"wc\"\nautoCommit: false\nstatuses: {\n  parked: {category: \"active\"}\n}\n")
	today := localStamp(localDay(0), 12, 0, 0)
	addTicket(t, wc, "wc-ab2c", "parked", "parked", "a0", "# Parked here\n", false, "changed: "+today+"\n")
	addTicket(t, ot, "ot-ab2c", "parked", "parked", "a0", "# Parked there\n", false, "changed: "+today+"\n")

	out, _, err := run(t, app, "log", "parked", "--today")
	if err != nil {
		t.Fatalf("log parked: %v", err)
	}
	if !strings.Contains(out, "wc-ab2c") || strings.Contains(out, "ot-ab2c") {
		t.Fatalf("cross-scope custom = %q", out)
	}

	out, _, err = run(t, app, "log", "parked", "--scope", "ot")
	if ExitCodeFromError(err) != exitUsage || out != "" {
		t.Fatalf("undeclared scope: exit %d out %q err %v", ExitCodeFromError(err), out, err)
	}
	if !strings.Contains(err.Error(), "for this scope") {
		t.Fatalf("err = %v", err)
	}
}

func TestLogHelpAndTSV(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	today := localStamp(localDay(0), 12, 0, 0)
	addTicket(t, dir, "wc-ab2c", "note", "done", "a0", "# col1\tcol2\n", true, "changed: "+today+"\n")

	out, _, err := run(t, app, "log")
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	row := lines(out)
	if len(row) != 1 || strings.Count(row[0], "\t") != 4 {
		t.Fatalf("tsv = %q", out)
	}
	if strings.Contains(row[0], "col1\tcol2") || !strings.Contains(row[0], "col1 col2") {
		t.Fatalf("title tab was not flattened: %q", row[0])
	}

	help, errOut, err := run(t, app, "log", "--help")
	if err != nil {
		t.Fatalf("help: %v", err)
	}
	text := help + errOut
	for _, want := range []string{
		"entered the status they have now",
		"Create counts",
		"not git history",
		"not an audit trail",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("help missing %q\n%s", want, text)
		}
	}
}

func TestResolveLogWindow(t *testing.T) {
	loc := time.FixedZone("NPT", 5*3600+45*60)
	now := time.Date(2026, 10, 7, 15, 0, 0, 0, loc)
	today := logDays{}
	w, err := resolveLogWindow(now, today)
	if err != nil {
		t.Fatal(err)
	}
	// 2026-10-06T18:20:00Z is 2026-10-07 00:05 in +05:45, and the UTC date is the previous day.
	at, include, bad := classifyChanged("2026-10-06T18:20:00Z", w)
	if bad || !include {
		t.Fatalf("local today from a UTC previous-day stamp: at %s include %v bad %v", at, include, bad)
	}
	_, include, bad = classifyChanged("2026-10-07T18:20:00Z", w)
	if bad || include {
		t.Fatalf("UTC same-day stamp is the next local day: include %v", include)
	}
	_, include, bad = classifyChanged("", w)
	if bad || include {
		t.Fatalf("missing changed include %v bad %v", include, bad)
	}
	_, include, bad = classifyChanged("not-a-time", w)
	if !bad || include {
		t.Fatalf("bad stamp include %v bad %v", include, bad)
	}

	future, err := resolveLogWindow(now, logDays{since: "2026-10-08", sinceSet: true})
	if err != nil {
		t.Fatal(err)
	}
	_, include, _ = classifyChanged("2026-10-07T09:15:00Z", future)
	if include {
		t.Fatal("since after today matched a row")
	}

	_, err = resolveLogWindow(now, logDays{since: "2026-10-07", sinceSet: true, until: "2026-10-01", untilSet: true})
	if ExitCodeFromError(err) != exitUsage {
		t.Fatalf("inverted range err = %v", err)
	}
	_, err = resolveLogWindow(now, logDays{today: true, date: "2026-10-07", dateSet: true})
	if ExitCodeFromError(err) != exitUsage {
		t.Fatalf("conflicting flags err = %v", err)
	}

	same, err := resolveLogWindow(now, logDays{since: "2026-10-01", sinceSet: true, until: "2026-10-01", untilSet: true})
	if err != nil {
		t.Fatal(err)
	}
	one, err := resolveLogWindow(now, logDays{date: "2026-10-01", dateSet: true})
	if err != nil {
		t.Fatal(err)
	}
	if same != one {
		t.Fatalf("same-day range %+v, date %+v", same, one)
	}

	yest, err := resolveLogWindow(now, logDays{yesterday: true})
	if err != nil {
		t.Fatal(err)
	}
	yestStart := time.Date(2026, 10, 6, 0, 0, 0, 0, loc)
	todayStart := time.Date(2026, 10, 7, 0, 0, 0, 0, loc)
	for _, tc := range []struct {
		stamp   string
		include bool
	}{
		{yestStart.Format(time.RFC3339), true},
		{yestStart.Add(-time.Minute).Format(time.RFC3339), false},
		{todayStart.Add(-time.Minute).Format(time.RFC3339), true},
		{todayStart.Format(time.RFC3339), false},
	} {
		_, include, bad = classifyChanged(tc.stamp, yest)
		if bad || include != tc.include {
			t.Fatalf("yesterday %s include %v bad %v, want include %v", tc.stamp, include, bad, tc.include)
		}
	}

	sinceW, err := resolveLogWindow(now, logDays{since: "2026-10-01", sinceSet: true})
	if err != nil {
		t.Fatal(err)
	}
	sinceStart := time.Date(2026, 10, 1, 0, 0, 0, 0, loc)
	_, include, bad = classifyChanged(sinceStart.Format(time.RFC3339), sinceW)
	if bad || !include {
		t.Fatalf("since start include %v bad %v", include, bad)
	}
	_, include, bad = classifyChanged(sinceStart.Add(-time.Minute).Format(time.RFC3339), sinceW)
	if bad || include {
		t.Fatalf("minute before since include %v", include)
	}
}
