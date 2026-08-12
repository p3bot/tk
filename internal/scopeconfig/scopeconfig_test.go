package scopeconfig

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cuelang.org/go/cue/cuecontext"
	"github.com/p3bot/tk/internal/frontmatter"
	"github.com/p3bot/tk/internal/status"
)

func writeCfg(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tk.cue"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadValid(t *testing.T) {
	ctx := cuecontext.New()
	dir := writeCfg(t, `
name: "wc"
autoCommit: true
statuses: {
	shipped: {category: "done"}
	triage:  {category: "backlog"}
}
fields: {
	estimate: {type: "int"}
	area:     {type: "string", values: ["frontend", "backend"]}
	owners:   {type: "strings"}
	jira:     {type: "string", required: true}
	flag:     {type: "bool", required: false}
}
`)
	s, err := Load(ctx, dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.Name != "wc" || !s.AutoCommit {
		t.Errorf("name/autoCommit = %q/%v", s.Name, s.AutoCommit)
	}
	if s.Statuses["shipped"] != status.CategoryDone || s.Statuses["triage"] != status.CategoryBacklog {
		t.Errorf("statuses = %+v", s.Statuses)
	}
	if s.Fields["estimate"].Type != FieldInt {
		t.Errorf("estimate type = %q", s.Fields["estimate"].Type)
	}
	if s.Fields["estimate"].Required {
		t.Error("estimate should default optional when required omitted")
	}
	if got := s.Fields["area"].Values; len(got) != 2 || got[0] != "frontend" {
		t.Errorf("area values = %v", got)
	}
	if s.Fields["owners"].Values != nil {
		t.Errorf("owners should have no enum, got %v", s.Fields["owners"].Values)
	}
	if !s.Fields["jira"].Required || s.Fields["jira"].Type != FieldString {
		t.Errorf("jira = %+v, want required string", s.Fields["jira"])
	}
	if s.Fields["flag"].Required {
		t.Error("flag required: false must stay optional")
	}

	if !s.StatusKnown("shipped") || !s.StatusKnown(status.Todo) {
		t.Error("StatusKnown should accept custom and built-in")
	}
	if s.StatusKnown("bogus") {
		t.Error("StatusKnown should reject unknown")
	}
	if !s.StatusTerminal("shipped") || !s.StatusTerminal(status.Done) {
		t.Error("StatusTerminal should be true for done-category and built-in done")
	}
	if s.StatusTerminal("triage") {
		t.Error("backlog custom is not terminal")
	}
	if f, ok := s.Field("estimate"); !ok || f.Type != FieldInt {
		t.Error("Field(estimate) lookup failed")
	}
}

func TestLoadMinimal(t *testing.T) {
	ctx := cuecontext.New()
	dir := writeCfg(t, "name: \"h\"\nautoCommit: false\n")
	s, err := Load(ctx, dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.Name != "h" || s.AutoCommit {
		t.Errorf("got %+v", s)
	}
}

func TestLoadIgnoresLeftoverKnownTags(t *testing.T) {
	ctx := cuecontext.New()
	dir := writeCfg(t, `
name: "wc"
autoCommit: false
knownTags: ["frontend", "api"]
`)
	s, err := Load(ctx, dir)
	if err != nil {
		t.Fatalf("leftover knownTags must not fail Load: %v", err)
	}
	if s.Name != "wc" || s.AutoCommit {
		t.Errorf("got %+v", s)
	}
}

func TestLoadConfigErrors(t *testing.T) {
	ctx := cuecontext.New()
	cases := []struct {
		name    string
		content string
	}{
		{"uncompilable", `name: "wc" autoCommit:::`},
		{"missing name", `autoCommit: true`},
		{"bad name alphabet", `name: "WC"` + "\nautoCommit: true"},
		{"name too long", `name: "abcdefghijklm"` + "\nautoCommit: true"},
		{"missing autoCommit", `name: "wc"`},
		{"autoCommit wrong type", `name: "wc"` + "\nautoCommit: \"yes\""},
		{"redeclare builtin status", `name: "wc"` + "\nautoCommit: true\nstatuses: {done: {category: \"done\"}}"},
		{"status bad category", `name: "wc"` + "\nautoCommit: true\nstatuses: {foo: {category: \"weird\"}}"},
		{"status bad alphabet", `name: "wc"` + "\nautoCommit: true\nstatuses: {\"Foo\": {category: \"done\"}}"},
		{"field shadows builtin", `name: "wc"` + "\nautoCommit: true\nfields: {status: {type: \"string\"}}"},
		{"field is meta key alias tag", `name: "wc"` + "\nautoCommit: true\nfields: {tag: {type: \"string\"}}"},
		{"field is meta key alias link", `name: "wc"` + "\nautoCommit: true\nfields: {link: {type: \"strings\"}}"},
		{"field bad type", `name: "wc"` + "\nautoCommit: true\nfields: {x: {type: \"float\"}}"},
		{"field bad name alphabet", `name: "wc"` + "\nautoCommit: true\nfields: {\"X\": {type: \"string\"}}"},
		{"values enum on int", `name: "wc"` + "\nautoCommit: true\nfields: {x: {type: \"int\", values: [\"a\"]}}"},
		{"values enum on bool", `name: "wc"` + "\nautoCommit: true\nfields: {x: {type: \"bool\", values: [\"a\"]}}"},
		{"values enum duplicate", `name: "wc"` + "\nautoCommit: true\nfields: {x: {type: \"string\", values: [\"a\", \"a\"]}}"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := writeCfg(t, c.content)
			_, err := Load(ctx, dir)
			if err == nil {
				t.Fatalf("expected a config error")
			}
			if _, ok := AsConfigError(err); !ok {
				t.Fatalf("expected *ConfigError, got %T: %v", err, err)
			}
		})
	}
}

func TestLoadAbsent(t *testing.T) {
	ctx := cuecontext.New()
	_, err := Load(ctx, t.TempDir())
	if _, ok := AsConfigError(err); !ok {
		t.Fatalf("absent tk.cue should be a *ConfigError, got %v", err)
	}
}

func TestReadName(t *testing.T) {
	ctx := cuecontext.New()

	// Name readable even when fuller schema is invalid (bad field type).
	dir := writeCfg(t, `name: "wc"`+"\nautoCommit: true\nfields: {x: {type: \"float\"}}")
	name, err := ReadName(ctx, dir)
	if err != nil {
		t.Fatalf("ReadName on schema-invalid-but-compilable: %v", err)
	}
	if name != "wc" {
		t.Errorf("name = %q", name)
	}

	bad := writeCfg(t, `name: "wc" broken:::`)
	if _, err := ReadName(ctx, bad); err == nil {
		t.Error("expected error on uncompilable tk.cue")
	}

	illegal := writeCfg(t, `name: "WC"`+"\nautoCommit: true")
	if _, err := ReadName(ctx, illegal); err == nil {
		t.Error("expected error on illegal name")
	}
}

func TestWriteMinimalRoundTrip(t *testing.T) {
	ctx := cuecontext.New()
	dir := t.TempDir()
	if err := WriteMinimal(dir, "wc", true); err != nil {
		t.Fatalf("WriteMinimal: %v", err)
	}
	s, err := Load(ctx, dir)
	if err != nil {
		t.Fatalf("Load after write: %v", err)
	}
	if s.Name != "wc" || !s.AutoCommit {
		t.Errorf("round-trip got %+v", s)
	}
}

func TestCueReasonIsSingleLine(t *testing.T) {
	// Multi-line underlying error must collapse so config_unparseable stays one token line.
	got := cueReason(errors.New("line one\nline two\n\tindented three"))
	if strings.ContainsAny(got, "\n\r") {
		t.Errorf("cueReason must not contain a newline, got %q", got)
	}
	if got != "line one line two indented three" {
		t.Errorf("cueReason flattening = %q", got)
	}
}

func TestValidateFieldDecl(t *testing.T) {
	if err := ValidateFieldDecl("jira", Field{Type: FieldString, Required: true}); err != nil {
		t.Fatalf("legal field: %v", err)
	}
	if err := ValidateFieldDecl("status", Field{Type: FieldString}); err == nil {
		t.Error("must refuse builtin shadow")
	}
	if err := ValidateFieldDecl("tag", Field{Type: FieldString}); err == nil {
		t.Error("must refuse meta alias")
	}
	if err := ValidateFieldDecl("x", Field{Type: "float"}); err == nil {
		t.Error("must refuse bad type")
	}
	if err := ValidateFieldDecl("x", Field{Type: FieldInt, Values: []string{"a"}}); err == nil {
		t.Error("must refuse enum on int")
	}
	if err := ValidateFieldDecl("Bad", Field{Type: FieldString}); err == nil {
		t.Error("must refuse bad name alphabet")
	}
	if err := ValidateFieldDecl("area", Field{Type: FieldString, Values: []string{"a", "a"}}); err == nil {
		t.Error("must refuse duplicate enum values")
	}
	if err := ValidateFieldDecl("area", Field{Type: FieldString, Values: []string{"a", "b"}}); err != nil {
		t.Errorf("unique enum values must be legal: %v", err)
	}
}

func TestFieldEqual(t *testing.T) {
	base := Field{Type: FieldString, Required: true, Values: []string{"a", "b"}}
	if !FieldEqual(base, Field{Type: FieldString, Required: true, Values: []string{"a", "b"}}) {
		t.Error("identical should equal")
	}
	if !FieldEqual(Field{Type: FieldString}, Field{Type: FieldString, Values: nil}) {
		t.Error("nil values should equal")
	}
	if !FieldEqual(Field{Type: FieldString, Values: nil}, Field{Type: FieldString, Values: []string{}}) {
		t.Error("nil and empty values should equal")
	}
	if FieldEqual(base, Field{Type: FieldString, Required: false, Values: []string{"a", "b"}}) {
		t.Error("required mismatch")
	}
	if FieldEqual(base, Field{Type: FieldInt, Required: true, Values: []string{"a", "b"}}) {
		t.Error("type mismatch")
	}
	if FieldEqual(base, Field{Type: FieldString, Required: true, Values: []string{"a"}}) {
		t.Error("values mismatch")
	}
}

func TestMissingRequired(t *testing.T) {
	s := &Schema{Fields: map[string]Field{
		"jira":   {Type: FieldString, Required: true},
		"owners": {Type: FieldStrings, Required: true},
		"flag":   {Type: FieldBool, Required: true},
		"pts":    {Type: FieldInt, Required: true},
		"opt":    {Type: FieldString, Required: false},
	}}
	// All absent.
	got := s.MissingRequired(&frontmatter.Model{})
	if strings.Join(got, ",") != "flag,jira,owners,pts" {
		t.Errorf("all absent = %v", got)
	}
	// Empty string / empty list still missing; false and 0 populated.
	m := &frontmatter.Model{Custom: []frontmatter.Field{
		{Key: "jira", Value: ""},
		{Key: "owners", Value: []string{}},
		{Key: "flag", Value: false},
		{Key: "pts", Value: int64(0)},
	}}
	got = s.MissingRequired(m)
	if strings.Join(got, ",") != "jira,owners" {
		t.Errorf("empty string/list = %v", got)
	}
	// Satisfied.
	m = &frontmatter.Model{Custom: []frontmatter.Field{
		{Key: "jira", Value: "TK-1"},
		{Key: "owners", Value: []string{"a"}},
		{Key: "flag", Value: true},
		{Key: "pts", Value: int64(3)},
	}}
	if got = s.MissingRequired(m); len(got) != 0 {
		t.Errorf("satisfied = %v", got)
	}
}

func TestSetFieldUnsetFieldRoundTrip(t *testing.T) {
	ctx := cuecontext.New()
	dir := writeCfg(t, `
name: "wc"
autoCommit: false
// keep this comment
statuses: {
	shipped: {category: "done"}
}
fields: {
	// sibling
	area: {type: "string", values: ["frontend", "backend"]}
}
`)
	if err := SetField(dir, "jira", Field{Type: FieldString, Required: true}); err != nil {
		t.Fatalf("SetField jira: %v", err)
	}
	s, err := Load(ctx, dir)
	if err != nil {
		t.Fatalf("Load after set: %v", err)
	}
	if !s.Fields["jira"].Required || s.Fields["jira"].Type != FieldString {
		t.Errorf("jira = %+v", s.Fields["jira"])
	}
	if got := s.Fields["area"].Values; len(got) != 2 {
		t.Errorf("sibling area lost values: %v", got)
	}
	data, err := os.ReadFile(filepath.Join(dir, "tk.cue"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "keep this comment") {
		t.Errorf("comment not preserved:\n%s", data)
	}
	if !strings.Contains(string(data), "shipped") {
		t.Errorf("statuses not preserved:\n%s", data)
	}

	// Full replace demotes required and clears enum.
	if err := SetField(dir, "jira", Field{Type: FieldString}); err != nil {
		t.Fatalf("demote jira: %v", err)
	}
	if err := SetField(dir, "area", Field{Type: FieldString}); err != nil {
		t.Fatalf("clear area enum: %v", err)
	}
	s, err = Load(ctx, dir)
	if err != nil {
		t.Fatalf("Load after demote: %v", err)
	}
	if s.Fields["jira"].Required {
		t.Error("omit required must demote")
	}
	if s.Fields["area"].Values != nil {
		t.Errorf("omit values must clear enum, got %v", s.Fields["area"].Values)
	}

	// Unset removes declaration only.
	if err := UnsetField(dir, "jira"); err != nil {
		t.Fatalf("UnsetField: %v", err)
	}
	s, err = Load(ctx, dir)
	if err != nil {
		t.Fatalf("Load after unset: %v", err)
	}
	if _, ok := s.Fields["jira"]; ok {
		t.Error("jira should be gone")
	}
	if _, ok := s.Fields["area"]; !ok {
		t.Error("area sibling should remain")
	}
	if err := UnsetField(dir, "area"); err != nil {
		t.Fatalf("unset last field: %v", err)
	}
	s, err = Load(ctx, dir)
	if err != nil {
		t.Fatalf("Load after last unset: %v", err)
	}
	if len(s.Fields) != 0 {
		t.Errorf("fields should be empty, got %+v", s.Fields)
	}
	if err := UnsetField(dir, "ghost"); err == nil {
		t.Error("unset unknown must fail")
	} else if !errors.Is(err, ErrFieldNotDeclared) {
		t.Errorf("unset unknown = %v, want ErrFieldNotDeclared", err)
	}
}
