package id

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestIsScopeName(t *testing.T) {
	valid := []string{"wc", "webctl", "ilili", "a", "abcdefghijkl", "api", "x1", "0a"}
	for _, s := range valid {
		if !IsScopeName(s) {
			t.Errorf("IsScopeName(%q) = false, want true", s)
		}
	}
	invalid := []string{"", "abcdefghijklm", "WC", "web-control", "web_ctl", "wc ", "wc.", "wc-", "über"}
	for _, s := range invalid {
		if IsScopeName(s) {
			t.Errorf("IsScopeName(%q) = true, want false", s)
		}
	}
}

func TestIsShortID(t *testing.T) {
	valid := []string{"ab2c", "ab2c9", "wxyz", "a234", "abcdefgh"}
	for _, s := range valid {
		if !IsShortID(s) {
			t.Errorf("IsShortID(%q) = false, want true", s)
		}
	}
	// Cases a loose ^[a-z0-9]{4,8}$ would wrongly accept.
	invalid := []string{
		"",
		"ab2",
		"abcdefghi",
		"10il",
		"0000",
		"2abc",
		"abio",
		"ab1c",
		"ab0c",
		"able",
		"AB2C",
		"ab-c",
	}
	for _, s := range invalid {
		if IsShortID(s) {
			t.Errorf("IsShortID(%q) = true, want false", s)
		}
	}
}

func TestIsFullTicketID(t *testing.T) {
	valid := []string{"wc-ab2c", "wc-ab2c9", "api-m9k3", "x-wxyz", "webctl-abcdefgh"}
	for _, s := range valid {
		if !IsFullTicketID(s) {
			t.Errorf("IsFullTicketID(%q) = false, want true", s)
		}
	}
	invalid := []string{
		"",
		"wc",
		"api-10il",
		"wc-0000",
		"wc-9k3m",
		"api-3m9k",
		"wc-ab2",
		"WC-ab2c",
		"wc--ab2c",
		"wc-ab-2c",
		"web_c-ab2c",
		"-ab2c",
		"wc-",
	}
	for _, s := range invalid {
		if IsFullTicketID(s) {
			t.Errorf("IsFullTicketID(%q) = true, want false", s)
		}
	}
}

func TestScopeOfFullID(t *testing.T) {
	cases := []struct{ in, want string }{
		{"wc-ab2c", "wc"},
		{"other-ab2c", "other"},
		{"nothyphen", "nothyphen"},
		{"", ""},
		{"wc-", "wc"},
		{"-ab2c", ""},
	}
	for _, tc := range cases {
		if got := ScopeOfFullID(tc.in); got != tc.want {
			t.Errorf("ScopeOfFullID(%q) = %q want %q", tc.in, got, tc.want)
		}
	}
}

func TestMintShape(t *testing.T) {
	for i := 0; i < 2000; i++ {
		got, err := Mint(rand.Reader)
		if err != nil {
			t.Fatalf("Mint: %v", err)
		}
		if len(got) != ShortIDMin {
			t.Fatalf("Mint() = %q, want length %d", got, ShortIDMin)
		}
		if !IsShortID(got) {
			t.Fatalf("Mint() = %q, not a legal short-id", got)
		}
		if strings.IndexByte(LetterAlphabet, got[0]) < 0 {
			t.Fatalf("Mint() = %q, first char not a letter", got)
		}
	}
}

func TestMintDeterministicWithFixedSource(t *testing.T) {
	src := bytes.Repeat([]byte{0x00}, 16)
	a, err := Mint(bytes.NewReader(src))
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	b, err := Mint(bytes.NewReader(src))
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if a != b {
		t.Fatalf("Mint not deterministic for identical source: %q vs %q", a, b)
	}
	if a != "aaaa" {
		t.Fatalf("Mint(all-zero) = %q, want aaaa", a)
	}
}

func TestMintExhaustedSource(t *testing.T) {
	_, err := Mint(bytes.NewReader(nil))
	if err == nil {
		t.Fatal("Mint with an empty source should error")
	}
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Mint empty source err = %v, want io.EOF", err)
	}
}

func TestExtendGrowth(t *testing.T) {
	got, err := Extend("ab2c", occupiedSet())
	if err != nil {
		t.Fatalf("Extend: %v", err)
	}
	if got != "ab2ca" {
		t.Fatalf("Extend(ab2c, {}) = %q, want ab2ca (first alphabet char)", got)
	}
	if !IsShortID(got) {
		t.Fatalf("Extend produced non-short-id %q", got)
	}
}

func TestExtendSkipsOccupied(t *testing.T) {
	occ := occupiedSet("ab2ca", "ab2cb", "ab2cc")
	got, err := Extend("ab2c", occ)
	if err != nil {
		t.Fatalf("Extend: %v", err)
	}
	if got != "ab2cd" {
		t.Fatalf("Extend skipping abc = %q, want ab2cd", got)
	}
}

func TestExtendDeterministic(t *testing.T) {
	occ := occupiedSet("ab2ca", "ab2cb")
	first, _ := Extend("ab2c", occ)
	second, _ := Extend("ab2c", occ)
	if first != second {
		t.Fatalf("Extend not deterministic: %q vs %q", first, second)
	}
}

func TestExtendGrowsLengthWhenBlocked(t *testing.T) {
	occ := occupiedSet()
	for i := 0; i < len(ShortIDAlphabet); i++ {
		occ["ab2c"+string(ShortIDAlphabet[i])] = struct{}{}
	}
	got, err := Extend("ab2c", occ)
	if err != nil {
		t.Fatalf("Extend: %v", err)
	}
	if len(got) != 6 || got != "ab2caa" {
		t.Fatalf("Extend with all length-5 blocked = %q, want ab2caa", got)
	}
}

func TestExtendCapExhaustion(t *testing.T) {
	if _, err := Extend("abcdefgh", occupiedSet()); err == nil {
		t.Fatal("Extend on a max-length prefix should hard-fail")
	}
}

func TestExtendRejectsBadPrefix(t *testing.T) {
	if _, err := Extend("10il", occupiedSet()); err == nil {
		t.Fatal("Extend should reject a prefix that is not a legal short-id")
	}
}

func occupiedSet(ids ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(ids))
	for _, s := range ids {
		m[s] = struct{}{}
	}
	return m
}
