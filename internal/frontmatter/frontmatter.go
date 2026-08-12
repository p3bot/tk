// Package frontmatter models a ticket file's leading YAML frontmatter.
// Split returns fence interior bytes verbatim; Parse/Serialize use a typed model.
// Undeclared keys are preserved in declaration order. Serialize always quotes order
// so mixed digit/letter keys are not YAML-coerced to numbers.
package frontmatter

import (
	"bytes"
	"fmt"

	"github.com/goccy/go-yaml"
)

// Built-in frontmatter key names (wire contract).
const (
	KeyID             = "id"
	KeyStatus         = "status"
	KeyOrder          = "order"
	KeyDepends        = "depends"
	KeyRelated        = "related"
	KeyTags           = "tags"
	KeyCreated        = "created"
	KeyLinks          = "links"
	KeySummary        = "summary"
	KeyStatusConflict = "status_conflict"
)

var builtinKeys = map[string]struct{}{
	KeyID: {}, KeyStatus: {}, KeyOrder: {}, KeyDepends: {}, KeyRelated: {},
	KeyTags: {}, KeyCreated: {}, KeyLinks: {}, KeySummary: {}, KeyStatusConflict: {},
}

// IsBuiltinKey reports whether name is a built-in frontmatter key.
// Scope config validation consults this so custom fields cannot shadow built-ins.
func IsBuiltinKey(name string) bool {
	_, ok := builtinKeys[name]
	return ok
}

// metaKeyAliases maps CLI key forms onto wire keys. They are not wire keys
// themselves; scope config refuses them as custom field names so meta aliases
// stay unambiguous.
var metaKeyAliases = map[string]string{
	"tag":  KeyTags,
	"link": KeyLinks,
}

// CanonicalMetaKey maps a CLI key (including singular aliases) onto the wire key.
func CanonicalMetaKey(key string) string {
	if wire, ok := metaKeyAliases[key]; ok {
		return wire
	}
	return key
}

// MetaKeyAliasTarget reports whether name is a reserved meta CLI alias and,
// if so, which wire key it stands for.
func MetaKeyAliasTarget(name string) (wire string, ok bool) {
	wire, ok = metaKeyAliases[name]
	return wire, ok
}

// Field is an undeclared (custom) frontmatter key preserved in declaration order.
type Field struct {
	Key   string
	Value any
}

// Model is the decoded frontmatter: built-in keys as typed fields plus undeclared keys in Custom.
// A nil slice or empty string means the key was absent; Serialize omits absent optional keys.
type Model struct {
	ID             string
	Status         string
	Order          string
	Depends        []string
	Related        []string
	Tags           []string
	Created        string
	Links          []string
	Summary        string
	StatusConflict []string
	Custom         []Field
}

// Split separates a leading ---...--- frontmatter fence from the body.
// Returns the fence interior verbatim (no re-encode). When no fence is present or
// the closing fence is missing, present is false and body is the whole input.
//
// A fence line must be exactly "---" (trailing CR allowed). "--- " with trailing
// whitespace is a CommonMark thematic break, not a fence; tolerating it would
// misread a body thematic break as a closing fence.
func Split(data []byte) (interior, body []byte, present bool) {
	first, rest, ok := firstLine(data)
	if !ok || !isFence(first) {
		return nil, data, false
	}
	start := len(data) - len(rest)
	pos := start
	remaining := rest
	for len(remaining) > 0 {
		line, after, _ := firstLine(remaining)
		if isFence(line) {
			return data[start:pos], after, true
		}
		pos += len(remaining) - len(after)
		remaining = after
	}
	return nil, data, false
}

func firstLine(data []byte) (line, rest []byte, hadNewline bool) {
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		return data[:i], data[i+1:], true
	}
	return data, nil, false
}

func isFence(line []byte) bool {
	return bytes.Equal(bytes.TrimSuffix(line, []byte("\r")), []byte("---"))
}

// Parse decodes fence interior into a Model. Built-in keys go to typed fields;
// others stay in Custom in declaration order. Comments and exact byte layout are
// not preserved — use Split for that.
func Parse(interior []byte) (*Model, error) {
	var items yaml.MapSlice
	if err := yaml.Unmarshal(interior, &items); err != nil {
		return nil, fmt.Errorf("parse frontmatter: %w", err)
	}
	m := &Model{}
	for _, item := range items {
		key := fmt.Sprint(item.Key)
		if _, ok := builtinKeys[key]; !ok {
			m.Custom = append(m.Custom, Field{Key: key, Value: item.Value})
			continue
		}
		if err := m.assignBuiltin(key, item.Value); err != nil {
			return nil, err
		}
	}
	return m, nil
}

func (m *Model) assignBuiltin(key string, value any) error {
	switch key {
	case KeyID:
		m.ID = asScalarString(value)
	case KeyStatus:
		m.Status = asScalarString(value)
	case KeyOrder:
		m.Order = asScalarString(value)
	case KeyCreated:
		m.Created = asScalarString(value)
	case KeySummary:
		m.Summary = asScalarString(value)
	case KeyDepends:
		return assignList(key, value, &m.Depends)
	case KeyRelated:
		return assignList(key, value, &m.Related)
	case KeyTags:
		return assignList(key, value, &m.Tags)
	case KeyLinks:
		return assignList(key, value, &m.Links)
	case KeyStatusConflict:
		return assignList(key, value, &m.StatusConflict)
	}
	return nil
}

func assignList(key string, value any, dst *[]string) error {
	list, err := asStringList(value)
	if err != nil {
		return fmt.Errorf("frontmatter key %q: %w", key, err)
	}
	*dst = list
	return nil
}

// Serialize encodes the Model to clean frontmatter YAML (interior only, no fences).
// Keys emit in canonical order; order is always quoted; empty optional keys are omitted.
func Serialize(m *Model) ([]byte, error) {
	items := yaml.MapSlice{
		{Key: KeyID, Value: m.ID},
		{Key: KeyStatus, Value: m.Status},
		{Key: KeyOrder, Value: quotedString(m.Order)},
	}
	items = appendList(items, KeyDepends, m.Depends)
	items = appendList(items, KeyRelated, m.Related)
	items = appendList(items, KeyTags, m.Tags)
	items = append(items, yaml.MapItem{Key: KeyCreated, Value: m.Created})
	items = appendList(items, KeyLinks, m.Links)
	if m.Summary != "" {
		items = append(items, yaml.MapItem{Key: KeySummary, Value: m.Summary})
	}
	items = appendList(items, KeyStatusConflict, m.StatusConflict)
	for _, f := range m.Custom {
		items = append(items, yaml.MapItem{Key: f.Key, Value: f.Value})
	}

	out, err := yaml.Marshal(items)
	if err != nil {
		return nil, fmt.Errorf("serialize frontmatter: %w", err)
	}
	return out, nil
}

func appendList(items yaml.MapSlice, key string, list []string) yaml.MapSlice {
	if len(list) == 0 {
		return items
	}
	return append(items, yaml.MapItem{Key: key, Value: flowStrings(list)})
}

// Compose assembles a full ticket file from serialized frontmatter interior and body.
func Compose(interior, body []byte) []byte {
	var b bytes.Buffer
	b.Grow(len("---\n---\n") + len(interior) + len(body))
	b.WriteString("---\n")
	b.Write(interior)
	b.WriteString("---\n")
	b.Write(body)
	return b.Bytes()
}

// quotedString forces a double-quoted scalar on marshal so order stays a string.
type quotedString string

func (q quotedString) MarshalYAML() ([]byte, error) {
	return fmt.Appendf(nil, "%q", string(q)), nil
}

// flowStrings marshals a string list in YAML flow style ([a, b]).
type flowStrings []string

func (f flowStrings) MarshalYAML() ([]byte, error) {
	return yaml.MarshalWithOptions([]string(f), yaml.Flow(true))
}

// asScalarString renders a decoded scalar as a string so mistyped non-string
// scalars remain readable rather than dropped.
func asScalarString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

// StringList coerces a decoded list value to []string. Accepts nil, []string,
// and []any (YAML decode). Used by parse, meta mutators, and required-field checks.
func StringList(v any) ([]string, error) {
	if v == nil {
		return nil, nil
	}
	switch x := v.(type) {
	case []string:
		return append([]string(nil), x...), nil
	case []any:
		out := make([]string, len(x))
		for i, e := range x {
			out[i] = asScalarString(e)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("expected a list, got %T", v)
	}
}

func asStringList(v any) ([]string, error) {
	return StringList(v)
}
