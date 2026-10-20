package id

import "testing"

func TestMintBlocked(t *testing.T) {
	blocked := []string{
		"anus", "arse", "cunt", "dyke", "fuck", "gash", "puss", "rape", "shag", "twat", "wank",
		"ass2", "a2ss", "as2s",
		"fuk4", "fu2k", "f2uk",
		"cum7", "fag2", "dyk3",
	}
	for _, s := range blocked {
		if !MintBlocked(s) {
			t.Errorf("MintBlocked(%q) = false, want true", s)
		}
		if len(s) >= ShortIDMin && len(s) <= ShortIDMax && !IsShortID(s) {
			t.Errorf("IsShortID(%q) = false, blocked draws stay legal short ids", s)
		}
	}

	allowed := []string{
		"aaaa", "b333", "ab2c", "pass", "bass", "sass", "crap", "damn", "sexy", "butt",
		"assk", "scum", "rap2", "f2ck", "2345", "FUCK", "",
	}
	for _, s := range allowed {
		if MintBlocked(s) {
			t.Errorf("MintBlocked(%q) = true, want false", s)
		}
	}
}

func TestExtendIgnoresMintBlock(t *testing.T) {
	got, err := Extend("fuck", occupiedSet())
	if err != nil {
		t.Fatalf("Extend: %v", err)
	}
	if got != "fucka" {
		t.Fatalf("Extend(fuck, {}) = %q, want fucka", got)
	}
}
