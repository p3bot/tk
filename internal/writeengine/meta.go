package writeengine

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/p3bot/tk/internal/frontmatter"
	"github.com/p3bot/tk/internal/id"
	"github.com/p3bot/tk/internal/scopeconfig"
)

// MetaOp is set, add, or rm.
type MetaOp int

const (
	// MetaSet rewrites a scalar key.
	MetaSet MetaOp = iota
	// MetaAdd appends one multi-value entry.
	MetaAdd
	// MetaRm removes one multi-value entry.
	MetaRm
)

func (op MetaOp) String() string {
	switch op {
	case MetaSet:
		return "set"
	case MetaAdd:
		return "add"
	case MetaRm:
		return "rm"
	default:
		return "meta"
	}
}

// MetaKeyClass is the mutate/get class of a frontmatter key.
type MetaKeyClass int

const (
	// MetaKeyUnknown is an undeclared or unsupported key.
	MetaKeyUnknown MetaKeyClass = iota
	// MetaKeyImmutable is id, status, order, created, or status_conflict.
	MetaKeyImmutable
	// MetaKeyScalar is summary or a custom string/int/bool field.
	MetaKeyScalar
	// MetaKeyMulti is depends, related, tags, links, or a custom strings field.
	MetaKeyMulti
)

var builtinMetaKeyOrder = []string{
	frontmatter.KeyID,
	frontmatter.KeyStatus,
	frontmatter.KeyOrder,
	frontmatter.KeyDepends,
	frontmatter.KeyRelated,
	frontmatter.KeyTags,
	frontmatter.KeyCreated,
	frontmatter.KeyLinks,
	frontmatter.KeySummary,
	frontmatter.KeyStatusConflict,
}

// MetaInput is one meta set|add|rm. Value is already loaded (stdin stays at the edge).
type MetaInput struct {
	Scope  string
	Dir    string
	Lookup Lookup
	Op     MetaOp
	Key    string
	Value  string
}

// Meta rewrites one frontmatter key and self-commits on tk-driven roots.
func Meta(deps Deps, in MetaInput) (Result, error) {
	key := frontmatter.CanonicalMetaKey(in.Key)
	if strings.Contains(in.Value, "\n") {
		return Result{}, &UsageError{Msg: fmt.Sprintf("meta %s value must not contain embedded newlines", in.Op)}
	}

	sess, err := Begin(deps, in.Scope, in.Dir)
	if err != nil {
		return Result{}, err
	}
	defer sess.Release()
	if err := sess.CheckMidRebase(); err != nil {
		return Result{}, err
	}

	out := Result{Warnings: sess.Warnings()}
	schema := sess.Schema
	class, field, err := ClassifyMetaKey(key, schema)
	if err != nil {
		return out, err
	}
	if class == MetaKeyUnknown {
		return out, UnknownMetaKeyError(key, schema)
	}
	if class == MetaKeyImmutable {
		return out, immutableMetaKeyError(key)
	}
	switch in.Op {
	case MetaSet:
		if class != MetaKeyScalar {
			return out, &UsageError{Msg: fmt.Sprintf("key %q is multi-value; use meta add/rm, not set", key)}
		}
	case MetaAdd, MetaRm:
		if class != MetaKeyMulti {
			return out, &UsageError{Msg: fmt.Sprintf("key %q is scalar; use meta set, not %s", key, in.Op)}
		}
	default:
		return out, fmt.Errorf("internal: meta op %v", in.Op)
	}
	p, err := ResolveWriteRow(deps.DB, in.Scope, in.Lookup)
	if err != nil {
		return out, err
	}

	value := in.Value
	if key == frontmatter.KeyDepends || key == frontmatter.KeyRelated {
		value, err = normaliseEdgeValue(deps, in.Scope, value)
		if err != nil {
			return out, err
		}
	}
	if in.Op == MetaAdd && key == frontmatter.KeyDepends {
		if err := checkDependsAdd(deps, p.ID, in.Scope, value); err != nil {
			return out, err
		}
	}

	var (
		notifyTagNew bool
		preWriteTags map[string]struct{}
	)
	if in.Op == MetaAdd && key == frontmatter.KeyTags {
		inUse, err := deps.DB.ScopeTagMembership(in.Scope)
		if err != nil {
			return out, err
		}
		notifyTagNew = true
		preWriteTags = inUse
	}

	m, body, err := ReadTicketFile(p.Path)
	if err != nil {
		return out, err
	}
	if err := applyMetaMutation(m, in.Op, key, value, field); err != nil {
		return out, err
	}
	if err := WriteTicketFile(p.Path, m, body); err != nil {
		return out, err
	}
	if err := deps.Rec.SyncPaths(in.Scope, WrittenPaths(p.Path, "")); err != nil {
		return out, err
	}

	message := fmt.Sprintf("tk: %s meta %s %s", p.ID, in.Op, key)
	disabled, needed, err := sess.CompleteState(message, p.Path, "")
	if err != nil {
		return out, err
	}
	out.ID = p.ID
	out.SyncDisabled = disabled
	out.SyncNeeded = needed
	if notifyTagNew {
		out.TagNew = newTagValues([]string{value}, preWriteTags)
	}
	if missing := schema.MissingRequired(m); len(missing) > 0 {
		out.RequiredMissing = missing
	}

	abs, err := absPath(p.Path)
	if err != nil {
		return out, err
	}
	out.Path = abs
	return out, nil
}

func normaliseEdgeValue(deps Deps, subjectScope, value string) (string, error) {
	lookup, byFull, err := parseEdgeID(deps, subjectScope, value)
	if err != nil {
		return "", err
	}
	if !byFull {
		return subjectScope + "-" + lookup, nil
	}
	return lookup, nil
}

func parseEdgeID(deps Deps, subjectScope, value string) (query string, byFull bool, err error) {
	form, ok := id.ParseArg(value)
	if !ok {
		return "", false, &UsageError{Msg: fmt.Sprintf("%q is not a valid ticket id", value)}
	}
	switch form {
	case id.FormMe:
		stored := ""
		if deps.Reg != nil {
			stored = deps.Reg.Me[subjectScope]
		}
		if stored == "" {
			return "", false, &UnknownTicketError{Noun: "ticket", Arg: id.ReservedMe}
		}
		return stored, true, nil
	case id.FormFull:
		return value, true, nil
	default:
		return value, false, nil
	}
}

func checkDependsAdd(deps Deps, subjectID, subjectScope, targetFull string) error {
	if targetFull == subjectID {
		return &DependsSelfError{ID: subjectID}
	}
	targetScope := id.ScopeOfFullID(targetFull)
	if targetScope == subjectScope {
		ok, err := nonQuarantinedExists(deps, subjectScope, targetFull)
		if err != nil {
			return err
		}
		if !ok {
			return &DependsDanglingError{ID: subjectID, Target: targetFull}
		}
		return nil
	}

	entry, registered := deps.Reg.Scopes[targetScope]
	if !registered {
		return &DependsUnresolvableError{ID: subjectID, Target: targetFull}
	}
	res, err := deps.Rec.Reconcile(map[string]string{targetScope: entry.Dir}, registeredSet(deps.Reg), nowNS())
	if err != nil {
		return err
	}
	if res.Unreachable[targetScope] {
		return &DependsUnresolvableError{ID: subjectID, Target: targetFull}
	}
	ok, err := nonQuarantinedExists(deps, targetScope, targetFull)
	if err != nil {
		return err
	}
	if !ok {
		return &DependsUnresolvableError{ID: subjectID, Target: targetFull}
	}
	return nil
}

func nonQuarantinedExists(deps Deps, scope, fullID string) (bool, error) {
	rows, err := deps.DB.TicketsByID(scope, fullID)
	if err != nil {
		return false, err
	}
	for _, r := range rows {
		if !r.ParseError {
			return true, nil
		}
	}
	return false, nil
}

// ClassifyMetaKey reports the mutate/get class of a canonical frontmatter key.
func ClassifyMetaKey(key string, schema *scopeconfig.Schema) (MetaKeyClass, scopeconfig.Field, error) {
	switch key {
	case frontmatter.KeyID, frontmatter.KeyStatus, frontmatter.KeyOrder,
		frontmatter.KeyCreated, frontmatter.KeyStatusConflict:
		return MetaKeyImmutable, scopeconfig.Field{}, nil
	case frontmatter.KeySummary:
		return MetaKeyScalar, scopeconfig.Field{Type: scopeconfig.FieldString}, nil
	case frontmatter.KeyDepends, frontmatter.KeyRelated, frontmatter.KeyTags, frontmatter.KeyLinks:
		return MetaKeyMulti, scopeconfig.Field{Type: scopeconfig.FieldStrings}, nil
	}
	if schema != nil {
		if f, ok := schema.Fields[key]; ok {
			switch f.Type {
			case scopeconfig.FieldString, scopeconfig.FieldInt, scopeconfig.FieldBool:
				return MetaKeyScalar, f, nil
			case scopeconfig.FieldStrings:
				return MetaKeyMulti, f, nil
			default:
				return MetaKeyUnknown, scopeconfig.Field{}, fmt.Errorf("custom field %q has unsupported type %q", key, f.Type)
			}
		}
	}
	return MetaKeyUnknown, scopeconfig.Field{}, nil
}

// KnownMetaKeys is the unknown-key catalogue: built-ins then sorted customs.
func KnownMetaKeys(schema *scopeconfig.Schema) []string {
	out := append([]string(nil), builtinMetaKeyOrder...)
	if schema == nil || len(schema.Fields) == 0 {
		return out
	}
	customs := make([]string, 0, len(schema.Fields))
	for name := range schema.Fields {
		customs = append(customs, name)
	}
	sort.Strings(customs)
	return append(out, customs...)
}

// UnknownMetaKeyError is usage for an undeclared frontmatter key, listing the catalogue.
func UnknownMetaKeyError(key string, schema *scopeconfig.Schema) error {
	return &UsageError{Msg: fmt.Sprintf("unknown frontmatter key %q; known keys: %s", key, strings.Join(KnownMetaKeys(schema), ", "))}
}

func immutableMetaKeyError(key string) error {
	switch key {
	case frontmatter.KeyStatus:
		return &UsageError{Msg: fmt.Sprintf("key %q is immutable via meta; use tk mark", key)}
	case frontmatter.KeyOrder:
		return &UsageError{Msg: fmt.Sprintf("key %q is immutable via meta; use tk order", key)}
	default:
		return &UsageError{Msg: fmt.Sprintf("key %q is immutable via meta", key)}
	}
}

// MetaGetValue is the decoded single-key get payload (scalars one line; lists joined).
func MetaGetValue(m *frontmatter.Model, key string, class MetaKeyClass, field scopeconfig.Field) (string, error) {
	switch key {
	case frontmatter.KeyID:
		return m.ID, nil
	case frontmatter.KeyStatus:
		return m.Status, nil
	case frontmatter.KeyOrder:
		return m.Order, nil
	case frontmatter.KeyCreated:
		return m.Created, nil
	case frontmatter.KeySummary:
		return m.Summary, nil
	case frontmatter.KeyDepends:
		return joinLines(m.Depends), nil
	case frontmatter.KeyRelated:
		return joinLines(m.Related), nil
	case frontmatter.KeyTags:
		return joinLines(m.Tags), nil
	case frontmatter.KeyLinks:
		return joinLines(m.Links), nil
	case frontmatter.KeyStatusConflict:
		return joinLines(m.StatusConflict), nil
	}
	v, ok := customValue(m, key)
	if !ok {
		return "", nil
	}
	if class == MetaKeyMulti || field.Type == scopeconfig.FieldStrings {
		list, err := frontmatter.StringList(v)
		if err != nil {
			return "", fmt.Errorf("custom field %q: %w", key, err)
		}
		return joinLines(list), nil
	}
	return formatScalar(v), nil
}

func applyMetaMutation(m *frontmatter.Model, op MetaOp, key, value string, field scopeconfig.Field) error {
	if op == MetaSet {
		return applyMetaSet(m, key, value, field)
	}
	return applyMetaListOp(m, op, key, value, field)
}

func applyMetaSet(m *frontmatter.Model, key, value string, field scopeconfig.Field) error {
	if key == frontmatter.KeySummary {
		m.Summary = value
		return nil
	}
	if value == "" {
		m.RemoveCustom(key)
		return nil
	}
	parsed, err := parseScalarValue(field, value)
	if err != nil {
		return err
	}
	setCustom(m, key, parsed)
	return nil
}

func applyMetaListOp(m *frontmatter.Model, op MetaOp, key, value string, field scopeconfig.Field) error {
	var list *[]string
	switch key {
	case frontmatter.KeyDepends:
		list = &m.Depends
	case frontmatter.KeyRelated:
		list = &m.Related
	case frontmatter.KeyTags:
		list = &m.Tags
	case frontmatter.KeyLinks:
		list = &m.Links
	default:
		cur, _ := customValue(m, key)
		existing, err := frontmatter.StringList(cur)
		if err != nil {
			return &UsageError{Msg: fmt.Sprintf("custom field %q is not a string list", key)}
		}
		next, err := mutateStringList(existing, op, value, field)
		if err != nil {
			return err
		}
		if len(next) == 0 {
			m.RemoveCustom(key)
		} else {
			setCustom(m, key, next)
		}
		return nil
	}
	next, err := mutateStringList(*list, op, value, field)
	if err != nil {
		return err
	}
	*list = next
	return nil
}

func mutateStringList(list []string, op MetaOp, value string, field scopeconfig.Field) ([]string, error) {
	switch op {
	case MetaAdd:
		if len(field.Values) > 0 && !stringIn(field.Values, value) {
			return nil, &UsageError{Msg: fmt.Sprintf("value %q is outside the declared values for this field", value)}
		}
		for _, e := range list {
			if e == value {
				return list, nil
			}
		}
		return append(list, value), nil
	case MetaRm:
		out := make([]string, 0, len(list))
		removed := false
		for _, e := range list {
			if !removed && e == value {
				removed = true
				continue
			}
			out = append(out, e)
		}
		return out, nil
	default:
		return list, fmt.Errorf("internal: list op %v", op)
	}
}

func parseScalarValue(field scopeconfig.Field, value string) (any, error) {
	switch field.Type {
	case scopeconfig.FieldString:
		if len(field.Values) > 0 && !stringIn(field.Values, value) {
			return nil, &UsageError{Msg: fmt.Sprintf("value %q is outside the declared values for this field", value)}
		}
		return value, nil
	case scopeconfig.FieldInt:
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return nil, &UsageError{Msg: fmt.Sprintf("%q is not a valid integer", value)}
		}
		return n, nil
	case scopeconfig.FieldBool:
		switch value {
		case "true":
			return true, nil
		case "false":
			return false, nil
		default:
			return nil, &UsageError{Msg: fmt.Sprintf("%q is not a valid bool (want true or false)", value)}
		}
	default:
		return nil, &UsageError{Msg: fmt.Sprintf("cannot set field of type %q", field.Type)}
	}
}

func customValue(m *frontmatter.Model, key string) (any, bool) {
	for _, f := range m.Custom {
		if f.Key == key {
			return f.Value, true
		}
	}
	return nil, false
}

func setCustom(m *frontmatter.Model, key string, value any) {
	for i := range m.Custom {
		if m.Custom[i].Key == key {
			m.Custom[i].Value = value
			return
		}
	}
	m.Custom = append(m.Custom, frontmatter.Field{Key: key, Value: value})
}

func formatScalar(v any) string {
	switch x := v.(type) {
	case bool:
		if x {
			return "true"
		}
		return "false"
	case string:
		return x
	case nil:
		return ""
	default:
		return fmt.Sprint(x)
	}
}

func joinLines(items []string) string {
	if len(items) == 0 {
		return ""
	}
	return strings.Join(items, "\n") + "\n"
}

func stringIn(list []string, s string) bool {
	for _, e := range list {
		if e == s {
			return true
		}
	}
	return false
}
