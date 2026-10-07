package cli

import (
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/reconcile"
	"github.com/p3bot/tk/internal/status"
	"github.com/p3bot/tk/internal/token"
)

func newLogCmd(app *App) *cobra.Command {
	var (
		scope     string
		tags      []string
		all       bool
		today     bool
		yesterday bool
		date      string
		since     string
		until     string
	)
	cmd := &cobra.Command{
		Use:   "log [status...] [--all] [--today | --yesterday | --date YYYY-MM-DD | --since YYYY-MM-DD [--until YYYY-MM-DD] | --until YYYY-MM-DD] [--scope S] [--tag T]...",
		Short: "Tickets that entered their current status on a local day",
		Long: "List tickets by the local day they entered the status they have now.\n" +
			"Each row is that instant. Create counts: a new ticket enters its first status\n" +
			"at create. A later status change replaces the instant. This is not git history\n" +
			"and not an audit trail.\n" +
			"\n" +
			"Bare `tk log` is status done, on the machine's local today, in every registered\n" +
			"scope. `tk log --today` is the same listing. Status positionals are a union and\n" +
			"replace done. With no positional, --all is every non-quarantined status. A\n" +
			"positional replaces --all, so `tk log done --all` is `tk log done`.\n" +
			"\n" +
			"For one --scope, an unknown status exits 2 (that scope's built-ins plus its\n" +
			"custom statuses). Across scopes, a positional is legal when it is a built-in or\n" +
			"declared in at least one scope being read. Scopes that do not declare a custom\n" +
			"name contribute no rows for it.\n" +
			"\n" +
			"--today, --yesterday, and --date each select one local calendar day. --since D\n" +
			"runs from the start of D through the end of today. A --since later than today\n" +
			"prints no rows and exits 0. --until D runs through the end of D with no lower\n" +
			"bound. --since and --until together are an inclusive range of local days, in\n" +
			"either flag order. The same day for both is that one day. A since day later\n" +
			"than the until day exits 2 and prints no TSV. Those day forms are mutually\n" +
			"exclusive. A value that is not YYYY-MM-DD exits 2.\n" +
			"\n" +
			"Omitting --scope reads every registered scope. --scope S reads that scope.\n" +
			"The value all is a scope name when that scope exists, and an unknown scope\n" +
			"otherwise.\n" +
			"\n" +
			"One headerless TSV line per row, newest changed first, then full id:\n" +
			"  <changed>\\t<full-id>\\t<status>\\t<title>\\t<absolute-path>\n" +
			"Compare the changed instant to the machine's local day, not the UTC date prefix\n" +
			"of the stored string. A missing changed matches no day and prints no warning.\n" +
			"A non-RFC3339 changed is not a row. Stderr reuses the doctor line and the\n" +
			"command still exits 0:\n" +
			"  schema_error: <id> changed \"<value>\" is not RFC3339 (<path>)\n" +
			"--tag repeats as OR and is a hard membership filter (untagged rows are out).\n" +
			"The tag lens is not applied. There is no --no-lens. An unused tag still filters\n" +
			"and emits tag_unknown: on stderr. Empty stdout exits 0. Pure read.",
		Args: anyArgs(),
		RunE: func(c *cobra.Command, args []string) error {
			days := logDays{
				today:     today,
				yesterday: yesterday,
				date:      date,
				dateSet:   c.Flags().Changed("date"),
				since:     since,
				sinceSet:  c.Flags().Changed("since"),
				until:     until,
				untilSet:  c.Flags().Changed("until"),
			}
			return runLog(app, c, logParams{
				statuses: args,
				all:      all,
				scope:    scope,
				tags:     tags,
				days:     days,
				now:      time.Now(),
			})
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "with no status filter: every non-quarantined status")
	cmd.Flags().BoolVar(&today, "today", false, "the machine's local calendar today (the default)")
	cmd.Flags().BoolVar(&yesterday, "yesterday", false, "the local calendar day before today")
	cmd.Flags().StringVar(&date, "date", "", "one local calendar day (YYYY-MM-DD)")
	cmd.Flags().StringVar(&since, "since", "", "from the start of this local day through the end of today, or through --until")
	cmd.Flags().StringVar(&until, "until", "", "through the end of this local day; no lower bound unless --since is set")
	cmd.Flags().StringVar(&scope, "scope", "", "read one scope (default: every registered scope)")
	cmd.Flags().StringArrayVar(&tags, "tag", nil, "match any of these tags (repeatable; OR; hard filter)")
	return cmd
}

type logParams struct {
	statuses []string
	all      bool
	scope    string
	tags     []string
	days     logDays
	now      time.Time
}

// logDays is the day-flag set as the CLI saw it. dateSet, sinceSet, and untilSet
// are flag presence, so an explicit empty value is still a usage error.
type logDays struct {
	today     bool
	yesterday bool
	date      string
	dateSet   bool
	since     string
	sinceSet  bool
	until     string
	untilSet  bool
}

// logWindow is an instant range in the clock's location. start is inclusive,
// end is exclusive. A missing bound is open on that side.
type logWindow struct {
	start    time.Time
	hasStart bool
	end      time.Time
	hasEnd   bool
}

func runLog(app *App, c *cobra.Command, p logParams) error {
	if p.now.IsZero() {
		p.now = time.Now()
	}
	window, err := resolveLogWindow(p.now, p.days)
	if err != nil {
		return err
	}

	e, err := app.openEngine(c)
	if err != nil {
		return err
	}
	defer e.close()

	targets, err := logTargets(e, p.scope)
	if err != nil {
		return err
	}
	res, err := e.reconcile(c, targets)
	if err != nil {
		return err
	}
	scopes := scopeNames(targets)
	if err := checkLogStatuses(p.statuses, p.scope != "", scopes, res); err != nil {
		return err
	}
	if len(p.tags) > 0 {
		inUse, err := tagMembership(e.db, scopes)
		if err != nil {
			return err
		}
		warnUnknownTags(c, p.tags, inUse)
	}

	tickets, err := e.db.TicketsInScopes(scopes)
	if err != nil {
		return err
	}
	hits, bad := selectLogRows(tickets, res, p, window)
	sortLogHits(hits)
	sort.Slice(bad, func(i, j int) bool {
		if bad[i].ID != bad[j].ID {
			return bad[i].ID < bad[j].ID
		}
		return bad[i].Path < bad[j].Path
	})
	for _, row := range bad {
		stderrln(c, token.FormatChangedNotRFC3339(row.ID, row.Changed, row.Path))
	}
	for _, hit := range hits {
		row := hit.ticket
		stdoutln(c, tsvLine(row.Changed, row.ID, row.Status, row.Title, row.Path))
	}
	return nil
}

func scopeNames(targets map[string]string) []string {
	names := make([]string, 0, len(targets))
	for name := range targets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func logTargets(e *engine, scope string) (map[string]string, error) {
	if scope == "" {
		return e.allTargets(), nil
	}
	entry, ok := e.reg.Scopes[scope]
	if !ok {
		return nil, fmt.Errorf("unknown scope %q", scope)
	}
	return map[string]string{scope: entry.Dir}, nil
}

func checkLogStatuses(names []string, oneScope bool, scopes []string, res *reconcile.Result) error {
	if len(names) == 0 {
		return nil
	}
	if oneScope {
		scope := ""
		if len(scopes) == 1 {
			scope = scopes[0]
		}
		_, err := parseStatusFilter(names, res.Schema(scope))
		return err
	}
	for _, n := range names {
		if status.IsBuiltin(n) {
			continue
		}
		if !statusKnownInAny(n, scopes, res) {
			return usageErrorf("unknown status %q", n)
		}
	}
	return nil
}

func statusKnownInAny(name string, scopes []string, res *reconcile.Result) bool {
	for _, scope := range scopes {
		if status.IsKnown(name, res.Schema(scope).CustomStatuses()) {
			return true
		}
	}
	return false
}

func tagMembership(db *index.DB, scopes []string) (map[string]struct{}, error) {
	inUse := map[string]struct{}{}
	for _, scope := range scopes {
		part, err := db.ScopeTagMembership(scope)
		if err != nil {
			return nil, err
		}
		for tag := range part {
			inUse[tag] = struct{}{}
		}
	}
	return inUse, nil
}

type logHit struct {
	ticket *index.Ticket
	at     time.Time
}

func selectLogRows(tickets []*index.Ticket, res *reconcile.Result, p logParams, w logWindow) ([]logHit, []*index.Ticket) {
	var hits []logHit
	var bad []*index.Ticket
	for _, row := range tickets {
		if row == nil || row.ParseError {
			continue
		}
		if !logStatusMatch(row, p, res) || !logTagMatch(row.Tags, p.tags) {
			continue
		}
		at, include, badStamp := classifyChanged(row.Changed, w)
		if badStamp {
			bad = append(bad, row)
			continue
		}
		if include {
			hits = append(hits, logHit{ticket: row, at: at})
		}
	}
	return hits, bad
}

func logStatusMatch(row *index.Ticket, p logParams, res *reconcile.Result) bool {
	if len(p.statuses) > 0 {
		if !slices.Contains(p.statuses, row.Status) {
			return false
		}
		if status.IsBuiltin(row.Status) {
			return true
		}
		return status.IsKnown(row.Status, res.Schema(row.Scope).CustomStatuses())
	}
	if p.all {
		return true
	}
	return row.Status == status.Done
}

func logTagMatch(have, want []string) bool {
	if len(want) == 0 {
		return true
	}
	set := make(map[string]struct{}, len(have))
	for _, tag := range have {
		set[tag] = struct{}{}
	}
	for _, tag := range want {
		if _, ok := set[tag]; ok {
			return true
		}
	}
	return false
}

func sortLogHits(hits []logHit) {
	sort.Slice(hits, func(i, j int) bool {
		if !hits[i].at.Equal(hits[j].at) {
			return hits[i].at.After(hits[j].at)
		}
		if hits[i].ticket.ID != hits[j].ticket.ID {
			return hits[i].ticket.ID < hits[j].ticket.ID
		}
		return hits[i].ticket.Path < hits[j].ticket.Path
	})
}

func resolveLogWindow(now time.Time, days logDays) (logWindow, error) {
	mode, err := logDayMode(days)
	if err != nil {
		return logWindow{}, err
	}
	loc := now.Location()
	today := startOfLocalDay(now)
	switch mode {
	case logDayToday:
		return dayWindow(today), nil
	case logDayYesterday:
		return dayWindow(today.AddDate(0, 0, -1)), nil
	case logDayDate:
		day, err := parseLogDay(days.date, loc)
		if err != nil {
			return logWindow{}, err
		}
		return dayWindow(day), nil
	case logDaySince:
		day, err := parseLogDay(days.since, loc)
		if err != nil {
			return logWindow{}, err
		}
		// A since day later than today yields an empty range and exits 0.
		return logWindow{start: day, hasStart: true, end: today.AddDate(0, 0, 1), hasEnd: true}, nil
	case logDayUntil:
		day, err := parseLogDay(days.until, loc)
		if err != nil {
			return logWindow{}, err
		}
		return logWindow{end: day.AddDate(0, 0, 1), hasEnd: true}, nil
	default:
		since, err := parseLogDay(days.since, loc)
		if err != nil {
			return logWindow{}, err
		}
		until, err := parseLogDay(days.until, loc)
		if err != nil {
			return logWindow{}, err
		}
		if since.After(until) {
			return logWindow{}, usageErrorf("--since %s is after --until %s", days.since, days.until)
		}
		return logWindow{start: since, hasStart: true, end: until.AddDate(0, 0, 1), hasEnd: true}, nil
	}
}

const (
	logDayToday     = "today"
	logDayYesterday = "yesterday"
	logDayDate      = "date"
	logDaySince     = "since"
	logDayUntil     = "until"
	logDayRange     = "range"
)

func logDayMode(days logDays) (string, error) {
	n := 0
	if days.today {
		n++
	}
	if days.yesterday {
		n++
	}
	if days.dateSet {
		n++
	}
	if days.sinceSet || days.untilSet {
		n++
	}
	if n > 1 {
		return "", usageErrorf("day flags are mutually exclusive")
	}
	switch {
	case n == 0 || days.today:
		return logDayToday, nil
	case days.yesterday:
		return logDayYesterday, nil
	case days.dateSet:
		return logDayDate, nil
	case days.sinceSet && days.untilSet:
		return logDayRange, nil
	case days.sinceSet:
		return logDaySince, nil
	default:
		return logDayUntil, nil
	}
}

func parseLogDay(value string, loc *time.Location) (time.Time, error) {
	t, err := time.ParseInLocation("2006-01-02", value, loc)
	if err != nil {
		return time.Time{}, usageErrorf("date %q must be YYYY-MM-DD", value)
	}
	return t, nil
}

func startOfLocalDay(now time.Time) time.Time {
	y, m, d := now.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, now.Location())
}

func dayWindow(start time.Time) logWindow {
	return logWindow{start: start, hasStart: true, end: start.AddDate(0, 0, 1), hasEnd: true}
}

// classifyChanged reports whether a fence changed string falls in the window.
// An empty string matches nothing and is not a warning. A non-RFC3339 value
// matches nothing and is a schema error.
func classifyChanged(changed string, w logWindow) (at time.Time, include, bad bool) {
	if changed == "" {
		return time.Time{}, false, false
	}
	at, err := time.Parse(time.RFC3339, changed)
	if err != nil {
		return time.Time{}, false, true
	}
	if w.hasStart && at.Before(w.start) {
		return at, false, false
	}
	if w.hasEnd && !at.Before(w.end) {
		return at, false, false
	}
	return at, true, false
}
