package token

import (
	"strings"
	"testing"
)

func TestLine(t *testing.T) {
	got := Line(NameDrift, "the message")
	if got != "name_drift: the message" {
		t.Errorf("Line = %q", got)
	}
	if !strings.HasPrefix(got, NameDrift) {
		t.Errorf("token must lead the line: %q", got)
	}
}

func TestFormatTagUnknownAndNew(t *testing.T) {
	unknown := FormatTagUnknown("orphan")
	if unknown != `tag_unknown: "orphan" is not used on any ticket in this scope` {
		t.Errorf("FormatTagUnknown = %q", unknown)
	}
	if !HasKnownPrefix(unknown) {
		t.Errorf("FormatTagUnknown must be catalogue-prefixed: %q", unknown)
	}
	newTag := FormatTagNew("orphan")
	if newTag != `tag_new: "orphan" is new to this scope` {
		t.Errorf("FormatTagNew = %q", newTag)
	}
	if !HasKnownPrefix(newTag) {
		t.Errorf("FormatTagNew must be catalogue-prefixed: %q", newTag)
	}
	if strings.HasPrefix(unknown, SchemaWarn) || strings.HasPrefix(newTag, SchemaWarn) {
		t.Error("tag existence feedback must not reuse schema_warn:")
	}
	changed := FormatChangedNotRFC3339("wc-jk9m", "not-a-time", "/tmp/wc-jk9m-bad.md")
	if changed != `schema_error: wc-jk9m changed "not-a-time" is not RFC3339 (/tmp/wc-jk9m-bad.md)` {
		t.Errorf("FormatChangedNotRFC3339 = %q", changed)
	}
}

func TestFormatDependsOpen(t *testing.T) {
	got := FormatDependsOpen("wc-ab2c", "todo", []string{"wc-de34", "wc-zz99"})
	want := "depends_open: wc-ab2c marked todo with open depends: wc-de34 wc-zz99"
	if got != want {
		t.Errorf("FormatDependsOpen = %q, want %q", got, want)
	}
	if !HasKnownPrefix(got) {
		t.Errorf("FormatDependsOpen must be catalogue-prefixed: %q", got)
	}
}

func TestFormatScopeHold(t *testing.T) {
	cases := []struct {
		holdType, reason, want string
	}{
		{HoldSoft, "some reason", "scope_hold: Soft hold: do not start new implementation. Reason: some reason"},
		{HoldSoft, "", "scope_hold: Soft hold: do not start new implementation."},
		{HoldHard, "some reason", "scope_hold: Hard hold: all work is stopped. Reason: some reason"},
		{HoldHard, "", "scope_hold: Hard hold: all work is stopped."},
	}
	for _, c := range cases {
		got := FormatScopeHold(c.holdType, c.reason)
		if got != c.want {
			t.Errorf("FormatScopeHold(%q, %q) = %q, want %q", c.holdType, c.reason, got, c.want)
		}
		if !HasKnownPrefix(got) {
			t.Errorf("line must be catalogue-prefixed: %q", got)
		}
		if "scope_hold: "+ScopeHoldSentence(c.holdType, c.reason) != got {
			t.Errorf("sentence %q is not the line without its prefix", ScopeHoldSentence(c.holdType, c.reason))
		}
	}
}

func TestFormatScopeHolds(t *testing.T) {
	if got := FormatScopeHolds(nil); got != "" {
		t.Errorf("no rows = %q", got)
	}
	got := FormatScopeHolds([]HoldRow{{Scope: "bar", Type: HoldSoft}})
	want := "scope_hold: Holds on the following scopes:\n  bar: soft: no reason given"
	if got != want {
		t.Errorf("one row = %q, want %q", got, want)
	}
	got = FormatScopeHolds([]HoldRow{{Scope: "baz", Type: HoldHard, Reason: "this is the hold reason"}, {Scope: "bar", Type: HoldSoft}})
	want = "scope_hold: Holds on the following scopes:\n  bar: soft: no reason given\n  baz: hard: this is the hold reason"
	if got != want {
		t.Errorf("rows = %q, want %q", got, want)
	}
}

func TestFormatRequiredMissing(t *testing.T) {
	got := FormatRequiredMissing("wc-ab2c", []string{"jira", "owners"})
	want := "required_missing: wc-ab2c missing required: jira owners"
	if got != want {
		t.Errorf("FormatRequiredMissing = %q, want %q", got, want)
	}
	if !HasKnownPrefix(got) {
		t.Errorf("FormatRequiredMissing must be catalogue-prefixed: %q", got)
	}
}

func TestCatalogueIncludesSoftWriteTokens(t *testing.T) {
	got := All()
	want := map[string]bool{
		TagUnknown: false, TagNew: false, DependsOpen: false, RequiredMissing: false,
		StaleReview: false, StaleBlocked: false,
	}
	for _, tkn := range got {
		if _, ok := want[tkn]; ok {
			want[tkn] = true
		}
	}
	for tkn, seen := range want {
		if !seen {
			t.Errorf("All() missing %q", tkn)
		}
	}
}

func TestHasKnownPrefix(t *testing.T) {
	known := []string{
		NameDrift + " x",
		ConfigUnparseable + " y",
		AutoCommitMismatch + " z",
		UnreachableScope + " w",
		TagUnknown + " x",
		TagNew + " y",
		DependsOpen + " z",
	}
	for _, s := range known {
		if !HasKnownPrefix(s) {
			t.Errorf("HasKnownPrefix(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"", "plain error", "some_other: token", "name_drif"} {
		if HasKnownPrefix(s) {
			t.Errorf("HasKnownPrefix(%q) = true, want false", s)
		}
	}
}
