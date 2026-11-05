package scopeconfig

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/parser"
	cuetoken "cuelang.org/go/cue/token"

	"github.com/p3bot/tk/internal/atomicfile"
	"github.com/p3bot/tk/internal/token"
)

const holdReasonMaxRunes = 200

// Hold is a scope's pause: its type, token.HoldSoft or token.HoldHard, and an
// optional reason that NormalizeHoldReason accepted.
type Hold struct {
	Type   string
	Reason string `json:",omitempty"`
}

// HoldOf returns the schema's hold, or nil when off. A nil schema is off.
func HoldOf(s *Schema) *Hold {
	if s == nil {
		return nil
	}
	return s.Hold
}

// HoldLabel is off, soft, or hard. A nil schema is off.
func HoldLabel(s *Schema) string {
	if h := HoldOf(s); h != nil {
		return h.Type
	}
	return token.HoldOff
}

// HoldReason is the hold reason, empty when off or there is none.
func HoldReason(s *Schema) string {
	if h := HoldOf(s); h != nil {
		return h.Reason
	}
	return ""
}

// HoldEqual reports whether two hold states match, with nil as off.
func HoldEqual(a, b *Hold) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// HoldError refuses a write under a scope's hold. No write, refresh, or push.
type HoldError struct {
	Scope string
	Hold  Hold
}

func (e *HoldError) Error() string {
	return token.FormatScopeHold(e.Hold.Type, e.Hold.Reason)
}

// RefuseHard returns a *HoldError when the scope has a hard hold.
func RefuseHard(scope string, s *Schema) error {
	if h := HoldOf(s); h != nil && h.Type == token.HoldHard {
		return &HoldError{Scope: scope, Hold: *h}
	}
	return nil
}

// RefuseStart returns a *HoldError when a ticket cannot enter in-progress, which
// is under either hold type.
func RefuseStart(scope string, s *Schema) error {
	if h := HoldOf(s); h != nil {
		return &HoldError{Scope: scope, Hold: *h}
	}
	return nil
}

// NormalizeHoldReason trims a reason and refuses one that is empty, longer than
// 200 runes, or carries a character that could forge or reorder a line of output.
func NormalizeHoldReason(reason string) (string, error) {
	trimmed := strings.TrimSpace(reason)
	if trimmed == "" {
		return "", errors.New("hold reason is empty")
	}
	if !utf8.ValidString(trimmed) {
		return "", errors.New("hold reason is not valid UTF-8")
	}
	if n := utf8.RuneCountInString(trimmed); n > holdReasonMaxRunes {
		return "", fmt.Errorf("hold reason is %d characters, over the limit of %d", n, holdReasonMaxRunes)
	}
	for _, r := range trimmed {
		if unicode.IsControl(r) || isLineOrBidiControl(r) {
			return "", fmt.Errorf("hold reason contains the control character %U", r)
		}
	}
	return trimmed, nil
}

func isLineOrBidiControl(r rune) bool {
	return r == '\u2028' || r == '\u2029' || unicode.Is(unicode.Bidi_Control, r)
}

// decodeHold reads hold from the CUE value rather than a decoded struct, because
// a nil decode cannot tell an absent field from hold: null.
func decodeHold(dir string, v cue.Value) (*Hold, error) {
	invalid := func(format string, a ...any) error {
		return &ConfigError{Dir: dir, Reason: "hold " + fmt.Sprintf(format, a...)}
	}
	f := v.LookupPath(cue.MakePath(cue.Str("hold")))
	if !f.Exists() {
		// A conflicting hold never gets here: the whole-value Decode reports it first.
		if v.LookupPath(cue.MakePath(cue.Str("hold").Optional())).Exists() {
			return nil, invalid("must be a regular field")
		}
		return nil, nil
	}
	if !f.IsConcrete() {
		return nil, invalid("must be a concrete struct with type soft or hard")
	}
	switch f.Kind() {
	case cue.BoolKind:
		// Legacy form. A bool hold enforced what soft enforces.
		on, err := f.Bool()
		if err != nil {
			return nil, invalid("is invalid: %s", cueReason(err))
		}
		if !on {
			return nil, nil
		}
		return &Hold{Type: token.HoldSoft}, nil
	case cue.StructKind:
		return decodeHoldStruct(f, invalid)
	default:
		return nil, invalid("must be a struct with type soft or hard")
	}
}

func decodeHoldStruct(f cue.Value, invalid func(string, ...any) error) (*Hold, error) {
	iter, err := f.Fields(cue.All())
	if err != nil {
		return nil, invalid("is invalid: %s", cueReason(err))
	}
	for iter.Next() {
		sel := iter.Selector()
		if sel.LabelType() != cue.StringLabel || sel.IsConstraint() {
			return nil, invalid("has the unsupported key %s", sel.String())
		}
		if name := sel.Unquoted(); name != "type" && name != "reason" {
			return nil, invalid("has the unknown key %q", name)
		}
	}
	typeValue := f.LookupPath(cue.MakePath(cue.Str("type")))
	if !typeValue.Exists() || !typeValue.IsConcrete() {
		return nil, invalid("needs a concrete type of soft or hard")
	}
	holdType, err := typeValue.String()
	if err != nil || (holdType != token.HoldSoft && holdType != token.HoldHard) {
		return nil, invalid("type must be soft or hard")
	}
	h := &Hold{Type: holdType}
	reasonValue := f.LookupPath(cue.MakePath(cue.Str("reason")))
	if !reasonValue.Exists() {
		return h, nil
	}
	if !reasonValue.IsConcrete() {
		return nil, invalid("reason must be a concrete string")
	}
	reason, err := reasonValue.String()
	if err != nil {
		return nil, invalid("reason must be a string")
	}
	h.Reason, err = NormalizeHoldReason(reason)
	if err != nil {
		return nil, invalid("is invalid: %s", err)
	}
	return h, nil
}

// RewriteHold makes <dir>/tk.cue hold exactly h through the CUE AST, as one field
// on one line. Nil deletes the field and the comments attached to it. Other
// fields, comments, and formatting are preserved. A missing field is inserted
// after autoCommit when that field exists, otherwise appended.
func RewriteHold(dir string, h *Hold) error {
	p := filepath.Join(dir, "tk.cue")
	file, err := parser.ParseFile(p, nil, parser.ParseComments)
	if err != nil {
		return fmt.Errorf("parse %s: %w", p, err)
	}

	decls := make([]ast.Decl, 0, len(file.Decls)+1)
	placed := false
	insertAt := -1
	for _, decl := range file.Decls {
		field, isField := decl.(*ast.Field)
		name := ""
		if isField {
			name = labelName(field.Label)
		}
		if name == "hold" {
			if h == nil || placed {
				continue
			}
			value := holdLiteral(h)
			ast.SetComments(value, ast.Comments(field.Value))
			field.Value = value
			placed = true
		}
		decls = append(decls, decl)
		if name == "autoCommit" {
			insertAt = len(decls)
		}
	}
	if h != nil && !placed {
		added := &ast.Field{Label: ast.NewIdent("hold"), Value: holdLiteral(h)}
		if insertAt >= 0 {
			decls = slices.Insert(decls, insertAt, ast.Decl(added))
		} else {
			decls = append(decls, added)
		}
	}
	file.Decls = decls

	data, err := format.Node(file)
	if err != nil {
		return fmt.Errorf("format %s: %w", p, err)
	}
	return atomicfile.Write(p, data, 0o600)
}

func holdLiteral(h *Hold) *ast.StructLit {
	elts := []ast.Decl{holdElt("type", h.Type)}
	if h.Reason != "" {
		elts = append(elts, holdElt("reason", h.Reason))
	}
	return &ast.StructLit{Lbrace: cuetoken.NoSpace.Pos(), Elts: elts, Rbrace: cuetoken.Blank.Pos()}
}

func holdElt(name, value string) *ast.Field {
	label := ast.NewIdent(name)
	ast.SetRelPos(label, cuetoken.Blank)
	return &ast.Field{Label: label, Value: ast.NewString(value)}
}
