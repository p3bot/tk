package cli

import (
	"testing"

	"github.com/p3bot/tk/internal/id"
)

func TestParseIDArgClassification(t *testing.T) {
	cases := []struct {
		tok      string
		wantForm id.Form
		wantOK   bool
	}{
		{"wc-ab2c", id.FormFull, true},    // well-formed full id
		{"ab2c", id.FormShort, true},      // well-formed short id
		{"wc-ABCD", id.FormFull, false},   // full form, illegal short-id chars
		{"wc-a", id.FormFull, false},      // full form, short-id too short
		{"wc-ab2c-x", id.FormFull, false}, // full form, extra '-'
		{"2abc", id.FormShort, false},     // short form, leading digit
		{"ab", id.FormShort, false},       // short form, too short
		{"AB2C", id.FormShort, false},     // short form, uppercase
		{"me", id.FormMe, true},           // reserved resolver token
		{"ME", id.FormShort, false},       // reserved token is lowercase only
	}
	for _, c := range cases {
		form, ok := parseIDArg(c.tok)
		if form != c.wantForm || ok != c.wantOK {
			t.Errorf("parseIDArg(%q) = (%v,%v) want (%v,%v)", c.tok, form, ok, c.wantForm, c.wantOK)
		}
	}
}
