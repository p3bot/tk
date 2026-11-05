package scopeconfig

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"cuelang.org/go/cue/cuecontext"

	"github.com/p3bot/tk/internal/status"
	"github.com/p3bot/tk/internal/token"
)

const holdBase = "name: \"wc\"\nautoCommit: false\n"

func TestLoadHoldForms(t *testing.T) {
	soft := &Hold{Type: token.HoldSoft}
	hard := &Hold{Type: token.HoldHard}
	cases := []struct {
		name string
		src  string
		want *Hold
		bad  bool
	}{
		{name: "absent", src: ""},
		{name: "soft", src: `hold: {type: "soft"}`, want: soft},
		{name: "soft reason", src: `hold: {type: "soft", reason: "x"}`, want: &Hold{Type: token.HoldSoft, Reason: "x"}},
		{name: "hard", src: `hold: {type: "hard"}`, want: hard},
		{name: "hard reason", src: `hold: {type: "hard", reason: "x"}`, want: &Hold{Type: token.HoldHard, Reason: "x"}},
		{name: "legacy true", src: `hold: true`, want: soft},
		{name: "legacy false", src: `hold: false`},
		{name: "null", src: `hold: null`, bad: true},
		{name: "empty struct", src: `hold: {}`, bad: true},
		{name: "type off", src: `hold: {type: "off"}`, bad: true},
		{name: "unknown type", src: `hold: {type: "stop"}`, bad: true},
		{name: "string form", src: `hold: "soft"`, bad: true},
		{name: "list", src: `hold: ["soft"]`, bad: true},
		{name: "number", src: `hold: 1`, bad: true},
		{name: "reason only", src: `hold: {reason: "x"}`, bad: true},
		{name: "unknown key", src: `hold: {type: "soft", who: "me"}`, bad: true},
		{name: "non-concrete bool", src: `hold: bool`, bad: true},
		{name: "non-concrete type", src: `hold: {type: string}`, bad: true},
		{name: "non-concrete reason", src: `hold: {type: "soft", reason: string}`, bad: true},
		{name: "non-string reason", src: `hold: {type: "soft", reason: 1}`, bad: true},
		{name: "empty reason", src: `hold: {type: "soft", reason: ""}`, bad: true},
		{name: "optional type", src: `hold: {type?: "soft"}`, bad: true},
		{name: "optional reason", src: `hold: {type: "soft", reason?: "x"}`, bad: true},
		{name: "required type", src: `hold: {type!: "soft"}`, bad: true},
		{name: "hidden key", src: `hold: {type: "soft", _x: 1}`, bad: true},
		{name: "definition key", src: `hold: {type: "soft", #d: 1}`, bad: true},
		{name: "hidden definition key", src: `hold: {type: "soft", _#d: 1}`, bad: true},
		{name: "conflict", src: `hold: {type: "soft" & "hard"}`, bad: true},
		{name: "scalar conflict", src: `hold: 1 & 2`, bad: true},
		{name: "optional hold", src: `hold?: true`, bad: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := writeCfg(t, holdBase+c.src+"\n")
			s, err := Load(cuecontext.New(), dir)
			if c.bad {
				if _, ok := AsConfigError(err); !ok {
					t.Fatalf("want ConfigError, got schema %+v err %v", s, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if !HoldEqual(s.Hold, c.want) {
				t.Fatalf("hold = %+v, want %+v", s.Hold, c.want)
			}
		})
	}
}

func TestHoldReasonRule(t *testing.T) {
	multibyte200 := strings.Repeat("é", 200)
	cases := []struct {
		name  string
		in    string
		want  string
		wantE bool
	}{
		{name: "plain", in: "x", want: "x"},
		{name: "interior spaces stay", in: "a   b", want: "a   b"},
		{name: "padded trims", in: "  x y\t", want: "x y"},
		{name: "empty", in: "", wantE: true},
		{name: "whitespace only", in: " \t\n", wantE: true},
		{name: "200 multibyte runes", in: multibyte200, want: multibyte200},
		{name: "200 runes with padding", in: "  " + multibyte200 + "  ", want: multibyte200},
		{name: "201 multibyte runes", in: multibyte200 + "é", wantE: true},
		{name: "interior newline", in: "a\nb", wantE: true},
		{name: "interior tab", in: "a\tb", wantE: true},
		{name: "escape", in: "a\x1bb", wantE: true},
		{name: "interior U+2028", in: "a\u2028b", wantE: true},
		{name: "interior U+2029", in: "a\u2029b", wantE: true},
		{name: "bidi U+202A", in: "a\u202Ab", wantE: true},
		{name: "bidi U+202E", in: "a\u202Eb", wantE: true},
		{name: "bidi U+2066", in: "a\u2066b", wantE: true},
		{name: "bidi U+2069", in: "a\u2069b", wantE: true},
		{name: "bidi mark U+061C", in: "a\u061Cb", wantE: true},
		{name: "bidi mark U+200E", in: "a\u200Eb", wantE: true},
		{name: "bidi mark U+200F", in: "a\u200Fb", wantE: true},
		{name: "zero-width joiner stays allowed", in: "a\u200Db", want: "a\u200Db"},
		{name: "invalid utf-8", in: "a\xffb", wantE: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := NormalizeHoldReason(c.in)
			if c.wantE {
				if err == nil {
					t.Fatalf("want refusal, got %q", got)
				}
			} else if err != nil || got != c.want {
				t.Fatalf("got %q err %v, want %q", got, err, c.want)
			}
			if c.in == "a\xffb" {
				return // A CUE source string cannot carry invalid UTF-8.
			}
			dir := writeCfg(t, holdBase+"hold: {type: \"soft\", reason: "+strconv.Quote(c.in)+"}\n")
			s, loadErr := Load(cuecontext.New(), dir)
			if c.wantE {
				if _, ok := AsConfigError(loadErr); !ok {
					t.Fatalf("tk.cue form: want ConfigError, got %v", loadErr)
				}
				return
			}
			if loadErr != nil || s.Hold.Reason != c.want {
				t.Fatalf("tk.cue form: schema %+v err %v", s, loadErr)
			}
		})
	}
}

func TestHoldNilSafeAccessors(t *testing.T) {
	if HoldOf(nil) != nil || HoldLabel(nil) != token.HoldOff || HoldReason(nil) != "" {
		t.Fatal("a nil schema must read as off")
	}
	if RefuseHard("wc", nil) != nil || RefuseStart("wc", nil) != nil {
		t.Fatal("a nil schema must not refuse")
	}
}

func TestRefusals(t *testing.T) {
	soft := &Schema{Hold: &Hold{Type: token.HoldSoft, Reason: "r"}}
	hard := &Schema{Hold: &Hold{Type: token.HoldHard}}
	if err := RefuseHard("wc", soft); err != nil {
		t.Fatalf("soft must pass the hard check: %v", err)
	}
	var held *HoldError
	if err := RefuseStart("wc", soft); !errors.As(err, &held) || held.Scope != "wc" || held.Hold.Reason != "r" {
		t.Fatalf("soft start = %v", err)
	}
	if err := RefuseHard("wc", hard); !errors.As(err, &held) || held.Hold.Type != token.HoldHard {
		t.Fatalf("hard = %v", err)
	}
	if err := RefuseStart("wc", hard); !errors.As(err, &held) {
		t.Fatalf("hard start = %v", err)
	}
	if got := (&HoldError{Hold: Hold{Type: token.HoldHard, Reason: "r"}}).Error(); got != token.FormatScopeHold(token.HoldHard, "r") {
		t.Fatalf("error = %q", got)
	}
}

func TestSchemaHoldSurvivesJSONRoundTrip(t *testing.T) {
	for _, h := range []*Hold{nil, {Type: token.HoldSoft}, {Type: token.HoldHard, Reason: "é \"x\""}} {
		b, err := json.Marshal(&Schema{Name: "wc", Hold: h})
		if err != nil {
			t.Fatal(err)
		}
		var got Schema
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatal(err)
		}
		if !HoldEqual(got.Hold, h) {
			t.Fatalf("round trip %s = %+v, want %+v", b, got.Hold, h)
		}
	}
}

func TestRewriteHoldLiteralText(t *testing.T) {
	cases := []struct {
		name string
		src  string
		hold *Hold
		want string
	}{
		{
			name: "insert soft after autoCommit",
			src:  "name: \"wc\"\nautoCommit: false\nstatuses: {shipped: {category: \"done\"}}\n",
			hold: &Hold{Type: token.HoldSoft},
			want: "name:       \"wc\"\nautoCommit: false\nhold: {type: \"soft\"}\nstatuses: {shipped: {category: \"done\"}}\n",
		},
		{
			name: "insert hard with reason",
			src:  holdBase,
			hold: &Hold{Type: token.HoldHard, Reason: "x y"},
			want: "name:       \"wc\"\nautoCommit: false\nhold: {type: \"hard\", reason: \"x y\"}\n",
		},
		{
			name: "replace legacy bool keeps the comments",
			src:  "name: \"wc\"\nautoCommit: false\n// above\nhold: true // trail\n",
			hold: &Hold{Type: token.HoldSoft, Reason: "r"},
			want: "name:       \"wc\"\nautoCommit: false\n// above\nhold: {type: \"soft\", reason: \"r\"} // trail\n",
		},
		{
			name: "replace a multi-line struct with one line",
			src:  "name: \"wc\"\nautoCommit: false\nhold: {\n\ttype: \"soft\"\n}\n",
			hold: &Hold{Type: token.HoldHard},
			want: "name:       \"wc\"\nautoCommit: false\nhold: {type: \"hard\"}\n",
		},
		{
			name: "split declarations become one field",
			src:  "name: \"wc\"\nautoCommit: false\nhold: {type: \"soft\"}\nhold: {reason: \"r\"}\n",
			hold: &Hold{Type: token.HoldHard, Reason: "r"},
			want: "name:       \"wc\"\nautoCommit: false\nhold: {type: \"hard\", reason: \"r\"}\n",
		},
		{
			name: "off removes the field and its comments",
			src:  "// top\nname: \"wc\"\nautoCommit: false\n// above\nhold: {type: \"soft\"} // trail\n\n// other\nstatuses: {a: {category: \"done\"}}\n",
			want: "// top\nname:       \"wc\"\nautoCommit: false\n\n// other\nstatuses: {a: {category: \"done\"}}\n",
		},
		{
			name: "escapes a quote, a backslash, and an interpolation",
			src:  holdBase,
			hold: &Hold{Type: token.HoldSoft, Reason: `"a" \(b) é`},
			want: "name:       \"wc\"\nautoCommit: false\nhold: {type: \"soft\", reason: \"\\\"a\\\" \\\\(b) é\"}\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := writeCfg(t, c.src)
			if err := RewriteHold(dir, c.hold); err != nil {
				t.Fatal(err)
			}
			if got := readCue(t, dir); got != c.want {
				t.Fatalf("tk.cue:\n%s\nwant:\n%s", got, c.want)
			}
			s, err := Load(cuecontext.New(), dir)
			if err != nil {
				t.Fatal(err)
			}
			if !HoldEqual(s.Hold, c.hold) {
				t.Fatalf("reload hold = %+v, want %+v", s.Hold, c.hold)
			}
			if err := RewriteHold(dir, c.hold); err != nil {
				t.Fatal(err)
			}
			if got := readCue(t, dir); got != c.want {
				t.Fatalf("second rewrite is not stable:\n%s", got)
			}
		})
	}
}

func TestRewriteHoldKeepsOtherFields(t *testing.T) {
	dir := writeCfg(t, "name: \"wc\"\nautoCommit: false\nstatuses: {shipped: {category: \"done\"}}\n")
	if err := RewriteHold(dir, &Hold{Type: token.HoldHard}); err != nil {
		t.Fatal(err)
	}
	s, err := Load(cuecontext.New(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.AutoCommit || s.Name != "wc" || s.Statuses["shipped"] != status.CategoryDone {
		t.Fatalf("schema after rewrite = %+v", s)
	}
}

func TestLaterRewritesLeaveTheHoldLineIntact(t *testing.T) {
	const line = `hold: {type: "hard", reason: "x y"}`
	dir := writeCfg(t, holdBase)
	if err := RewriteHold(dir, &Hold{Type: token.HoldHard, Reason: "x y"}); err != nil {
		t.Fatal(err)
	}
	if err := RewriteAutoCommit(dir, true); err != nil {
		t.Fatal(err)
	}
	raw := readCue(t, dir)
	if !strings.Contains(raw, "\n"+line+"\n") || !strings.Contains(raw, "autoCommit: true") {
		t.Fatalf("after RewriteAutoCommit:\n%s", raw)
	}
	if err := SetField(dir, "owner", Field{Type: FieldString}); err != nil {
		t.Fatal(err)
	}
	if raw = readCue(t, dir); !strings.Contains(raw, "\n"+line+"\n") {
		t.Fatalf("after SetField:\n%s", raw)
	}
}

func readCue(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "tk.cue"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
