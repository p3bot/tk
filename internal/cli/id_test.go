package cli

import "testing"

func TestParseIDArgClassification(t *testing.T) {
	cases := []struct {
		tok      string
		wantForm idForm
		wantOK   bool
	}{
		{"wc-ab2c", idFull, true},    // well-formed full id
		{"ab2c", idShort, true},      // well-formed short id
		{"wc-ABCD", idFull, false},   // full form, illegal short-id chars
		{"wc-a", idFull, false},      // full form, short-id too short
		{"wc-ab2c-x", idFull, false}, // full form, extra '-'
		{"2abc", idShort, false},     // short form, leading digit
		{"ab", idShort, false},       // short form, too short
		{"AB2C", idShort, false},     // short form, uppercase
		{"me", idMe, true},           // reserved resolver token
		{"ME", idShort, false},       // reserved token is lowercase only
	}
	for _, c := range cases {
		form, ok := parseIDArg(c.tok)
		if form != c.wantForm || ok != c.wantOK {
			t.Errorf("parseIDArg(%q) = (%v,%v) want (%v,%v)", c.tok, form, ok, c.wantForm, c.wantOK)
		}
	}
}
