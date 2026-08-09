// Package scopeconfig evaluates a scope's tk.cue into a validated Schema. A
// non-nil *ConfigError from Load means the scope is unusable for writes and
// rides config_unparseable; reads stay available. Compile failures and schema
// violations are the same unusable state.
package scopeconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"cuelang.org/go/cue"
	"github.com/p3bot/tk/internal/frontmatter"
	"github.com/p3bot/tk/internal/id"
	"github.com/p3bot/tk/internal/status"
)

// Field types (closed set for v1).
const (
	FieldString  = "string"
	FieldInt     = "int"
	FieldBool    = "bool"
	FieldStrings = "strings"
)

var (
	// statusNameRe is the custom-status name alphabet: lowercase, hyphen-joined.
	statusNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	// fieldNameRe is the custom-field name alphabet: snake-friendly YAML keys.
	fieldNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
)

// Field is a declared custom frontmatter field. Values is non-nil only with an enum
// (legal only for string/strings).
type Field struct {
	Type   string
	Values []string
}

// Schema is a scope's fully-evaluated, validated tk.cue. Presence means the config is usable.
type Schema struct {
	Name       string
	AutoCommit bool
	// Statuses maps declared custom status names to categories (built-ins live in package status).
	Statuses map[string]status.Category
	// Fields maps declared custom field names to type and optional enum.
	Fields map[string]Field
}

// ConfigError marks a tk.cue that cannot be trusted (absent, uncompilable, or schema-invalid).
type ConfigError struct {
	Dir    string
	Reason string
}

func (e *ConfigError) Error() string {
	return fmt.Sprintf("scope config unparseable at %s: %s", filepath.Join(e.Dir, "tk.cue"), e.Reason)
}

// AsConfigError reports whether err is (or wraps) a *ConfigError.
func AsConfigError(err error) (*ConfigError, bool) {
	var ce *ConfigError
	if errors.As(err, &ce) {
		return ce, true
	}
	return nil, false
}

type rawConfig struct {
	Name       string               `json:"name"`
	AutoCommit *bool                `json:"autoCommit"`
	Statuses   map[string]rawStatus `json:"statuses"`
	Fields     map[string]rawField  `json:"fields"`
}

type rawStatus struct {
	Category string `json:"category"`
}

type rawField struct {
	Type   string   `json:"type"`
	Values []string `json:"values"`
}

// Load reads and validates <dir>/tk.cue. Every unusable state returns *ConfigError.
func Load(ctx *cue.Context, dir string) (*Schema, error) {
	p := filepath.Join(dir, "tk.cue")
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, &ConfigError{Dir: dir, Reason: "tk.cue is absent"}
		}
		return nil, &ConfigError{Dir: dir, Reason: fmt.Sprintf("cannot read tk.cue: %v", err)}
	}

	v := ctx.CompileBytes(data, cue.Filename(p))
	if err := v.Err(); err != nil {
		return nil, &ConfigError{Dir: dir, Reason: cueReason(err)}
	}

	var raw rawConfig
	if err := v.Decode(&raw); err != nil {
		return nil, &ConfigError{Dir: dir, Reason: cueReason(err)}
	}

	return validate(dir, v, &raw)
}

// ReadName extracts only the authoritative name, succeeding even when the fuller
// schema is invalid (provided the file compiles and the name is legal). Used for name-drift.
func ReadName(ctx *cue.Context, dir string) (string, error) {
	p := filepath.Join(dir, "tk.cue")
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return "", &ConfigError{Dir: dir, Reason: "tk.cue is absent"}
		}
		return "", &ConfigError{Dir: dir, Reason: fmt.Sprintf("cannot read tk.cue: %v", err)}
	}
	v := ctx.CompileBytes(data, cue.Filename(p))
	if err := v.Err(); err != nil {
		return "", &ConfigError{Dir: dir, Reason: cueReason(err)}
	}
	name, err := v.LookupPath(cue.MakePath(cue.Str("name"))).String()
	if err != nil {
		return "", &ConfigError{Dir: dir, Reason: "name is missing or not a string"}
	}
	if !id.IsScopeName(name) {
		return "", &ConfigError{Dir: dir, Reason: fmt.Sprintf("name %q is not a legal scope name", name)}
	}
	return name, nil
}

// Evaluate validates an already-built tk.cue value into a Schema (shared tail of Load and LoadWithClosure).
func Evaluate(dir string, v cue.Value) (*Schema, error) {
	if err := v.Err(); err != nil {
		return nil, &ConfigError{Dir: dir, Reason: cueReason(err)}
	}
	var raw rawConfig
	if err := v.Decode(&raw); err != nil {
		return nil, &ConfigError{Dir: dir, Reason: cueReason(err)}
	}
	return validate(dir, v, &raw)
}

func validate(dir string, v cue.Value, raw *rawConfig) (*Schema, error) {
	if !id.IsScopeName(raw.Name) {
		return nil, &ConfigError{Dir: dir, Reason: fmt.Sprintf("name %q is not a legal scope name (^[a-z0-9]{1,12}$)", raw.Name)}
	}
	if raw.AutoCommit == nil {
		return nil, &ConfigError{Dir: dir, Reason: "autoCommit is required"}
	}

	statuses, err := validateStatuses(dir, raw.Statuses)
	if err != nil {
		return nil, err
	}
	fields, err := validateFields(dir, v, raw.Fields)
	if err != nil {
		return nil, err
	}

	return &Schema{
		Name:       raw.Name,
		AutoCommit: *raw.AutoCommit,
		Statuses:   statuses,
		Fields:     fields,
	}, nil
}

func validateStatuses(dir string, raw map[string]rawStatus) (map[string]status.Category, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make(map[string]status.Category, len(raw))
	for name, st := range raw {
		if !statusNameRe.MatchString(name) {
			return nil, &ConfigError{Dir: dir, Reason: fmt.Sprintf("status name %q is outside its alphabet (^[a-z][a-z0-9-]{0,31}$)", name)}
		}
		if status.IsBuiltin(name) {
			return nil, &ConfigError{Dir: dir, Reason: fmt.Sprintf("status %q redeclares a built-in status", name)}
		}
		cat := status.Category(st.Category)
		if !status.ValidCategory(cat) {
			return nil, &ConfigError{Dir: dir, Reason: fmt.Sprintf("status %q has category %q, want active|backlog|done", name, st.Category)}
		}
		out[name] = cat
	}
	return out, nil
}

func validateFields(dir string, v cue.Value, raw map[string]rawField) (map[string]Field, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make(map[string]Field, len(raw))
	for name, f := range raw {
		if !fieldNameRe.MatchString(name) {
			return nil, &ConfigError{Dir: dir, Reason: fmt.Sprintf("field name %q is outside its alphabet (^[a-z][a-z0-9_]{0,31}$)", name)}
		}
		if frontmatter.IsBuiltinKey(name) {
			return nil, &ConfigError{Dir: dir, Reason: fmt.Sprintf("field %q shadows a built-in frontmatter key", name)}
		}
		if wire, ok := frontmatter.MetaKeyAliasTarget(name); ok {
			return nil, &ConfigError{Dir: dir, Reason: fmt.Sprintf("field %q is reserved as a meta key alias for %q", name, wire)}
		}
		switch f.Type {
		case FieldString, FieldInt, FieldBool, FieldStrings:
		default:
			return nil, &ConfigError{Dir: dir, Reason: fmt.Sprintf("field %q has type %q, want string|int|bool|strings", name, f.Type)}
		}
		// values presence from CUE: nil-vs-empty slice cannot tell absent from explicit empty.
		hasEnum := v.LookupPath(cue.MakePath(cue.Str("fields"), cue.Str(name), cue.Str("values"))).Exists()
		if hasEnum && f.Type != FieldString && f.Type != FieldStrings {
			return nil, &ConfigError{Dir: dir, Reason: fmt.Sprintf("field %q has a values enum, legal only for string or strings (not %s)", name, f.Type)}
		}
		field := Field{Type: f.Type}
		if hasEnum {
			field.Values = f.Values
		}
		out[name] = field
	}
	return out, nil
}

// cueReason renders a CUE error as a single-line ConfigError reason (one token line).
func cueReason(err error) string {
	return strings.Join(strings.Fields(err.Error()), " ")
}

func (s *Schema) customCategories() map[string]status.Category {
	return s.Statuses
}

// StatusKnown reports whether name is a built-in or a status this scope declares.
func (s *Schema) StatusKnown(name string) bool {
	return status.IsKnown(name, s.customCategories())
}

// StatusTerminal reports whether name is terminal for this scope.
func (s *Schema) StatusTerminal(name string) bool {
	return status.IsTerminal(name, s.customCategories())
}

// Category returns the category of a status known to this scope.
func (s *Schema) Category(name string) (status.Category, bool) {
	return status.CategoryOf(name, s.customCategories())
}

// Field returns the declared custom field of the given name, if any.
func (s *Schema) Field(name string) (Field, bool) {
	f, ok := s.Fields[name]
	return f, ok
}
