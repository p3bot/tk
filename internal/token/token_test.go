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

func TestCatalogueIncludesSoftWriteTokens(t *testing.T) {
	got := All()
	want := map[string]bool{TagUnknown: false, TagNew: false, DependsOpen: false}
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
