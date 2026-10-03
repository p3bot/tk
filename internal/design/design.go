// Package design is cobra-free scope design documents under design/.
// Designs are not tickets: they are absent from the ticket index and the board.
package design

import (
	"fmt"
	"strings"
	"time"

	"github.com/goccy/go-yaml"

	"github.com/p3bot/tk/internal/frontmatter"
	"github.com/p3bot/tk/internal/token"
)

const (
	// StatusDraft is the scaffold status.
	StatusDraft = "draft"
	// StatusAccepted is an accepted design still on the default list.
	StatusAccepted = "accepted"
	// StatusDecomposed is a design that has been split into tickets.
	StatusDecomposed = "decomposed"
	// StatusSuperseded is a design replaced by a later one.
	StatusSuperseded = "superseded"

	// KeyProduces is the only design meta key: full ticket ids, one way.
	KeyProduces = "produces"

	fileMode = 0o644
)

// KnownStatus reports whether s is one of the four closed design statuses.
func KnownStatus(s string) bool {
	switch s {
	case StatusDraft, StatusAccepted, StatusDecomposed, StatusSuperseded:
		return true
	default:
		return false
	}
}

// DefaultListed reports whether s is shown by list without --all or a positional filter.
func DefaultListed(s string) bool {
	return s == StatusDraft || s == StatusAccepted
}

// UsageError is argv-class policy the CLI adapter maps to exit 2.
type UsageError struct {
	Msg string
}

func (e *UsageError) Error() string { return e.Msg }

// UnknownStatusError is a status outside the closed design set.
type UnknownStatusError struct {
	Status string
}

func (e *UnknownStatusError) Error() string {
	return fmt.Sprintf("%q is not a design status (draft, accepted, decomposed, superseded)", e.Status)
}

// UnknownError is a well-formed id with no design file.
type UnknownError struct {
	Arg string
}

func (e *UnknownError) Error() string {
	return fmt.Sprintf("unknown design id %q", e.Arg)
}

// SharedIDError is two design files holding one short id. No path is printed.
type SharedIDError struct {
	ID    string
	Paths []string
}

func (e *SharedIDError) Error() string {
	return token.Line(token.DesignID, fmt.Sprintf("%s is claimed by %d design files: %s",
		e.ID, len(e.Paths), strings.Join(e.Paths, ", ")))
}

// ParseError is an unparseable design fence. Get still returns the path.
type ParseError struct {
	ID  string
	Msg string
}

func (e *ParseError) Error() string {
	return token.Line(token.ParseError, fmt.Sprintf("%s: %s — cannot rewrite quarantined frontmatter", e.ID, e.Msg))
}

// ReadLine is the get diagnostic. The rewrite sentence stays on write refusals.
func (e *ParseError) ReadLine() string {
	return token.Line(token.ParseError, fmt.Sprintf("%s: %s", e.ID, e.Msg))
}

// UnresolvedError is a produces value that is not an existing ticket.
type UnresolvedError struct {
	Target string
}

func (e *UnresolvedError) Error() string {
	return fmt.Sprintf("produces target %q does not resolve to a ticket", e.Target)
}

// Row is one design list line.
type Row struct {
	ID      string
	Status  string
	Title   string
	Path    string
	Created string
}

// Result is a design command outcome. Adapters print Path and tokens.
type Result struct {
	ID           string
	Path         string
	Body         []byte
	Rows         []Row
	SyncDisabled string
	SyncNeeded   string
	Parse        *ParseError
	// Unparseable is how many design fences in the scope failed to parse.
	// List and get print it as parse_error: N unparseable. Writes leave it zero.
	Unparseable int
	// Unchanged means the file was already in the requested state. No commit.
	Unchanged bool
}

// File is one design document read from disk.
type File struct {
	Path     string
	ID       string
	Model    *frontmatter.Model
	Body     []byte
	Raw      []byte
	ParseErr error
}

func (f File) parseError() *ParseError {
	if f.ParseErr == nil {
		return nil
	}
	return &ParseError{ID: f.ID, Msg: f.ParseErr.Error()}
}

// flowStrings marshals a string list in YAML flow style.
type flowStrings []string

func (f flowStrings) MarshalYAML() ([]byte, error) {
	return yaml.MarshalWithOptions([]string(f), yaml.Flow(true))
}

// Serialize encodes a design fence. Empty order and changed are omitted so a
// design does not gain those ticket keys. A present changed value is kept
// because the parser stores it as a builtin rather than an extra key.
// A produces list is flow-styled. A produces value that is not a list is
// emitted unchanged so a later write does not drop residue doctor already reports.
func Serialize(m *frontmatter.Model) ([]byte, error) {
	if m == nil {
		return nil, fmt.Errorf("serialize design: nil model")
	}
	items := yaml.MapSlice{
		{Key: frontmatter.KeyID, Value: m.ID},
		{Key: frontmatter.KeyStatus, Value: m.Status},
	}
	if m.Changed != "" {
		items = append(items, yaml.MapItem{Key: frontmatter.KeyChanged, Value: m.Changed})
	}
	if m.Order != "" {
		items = append(items, yaml.MapItem{Key: frontmatter.KeyOrder, Value: quoted(m.Order)})
	}
	items = appendList(items, frontmatter.KeyDepends, m.Depends)
	items = appendList(items, frontmatter.KeyRelated, m.Related)
	items = appendList(items, frontmatter.KeyTags, m.Tags)
	items = append(items, yaml.MapItem{Key: frontmatter.KeyCreated, Value: m.Created})
	items = appendList(items, frontmatter.KeyLinks, m.Links)
	if m.Summary != "" {
		items = append(items, yaml.MapItem{Key: frontmatter.KeySummary, Value: m.Summary})
	}
	items = appendList(items, frontmatter.KeyStatusConflict, m.StatusConflict)
	for _, f := range m.Custom {
		value := f.Value
		if f.Key == KeyProduces {
			ids, err := frontmatter.StringList(f.Value)
			if err != nil {
				items = append(items, yaml.MapItem{Key: f.Key, Value: f.Value})
				continue
			}
			if len(ids) == 0 {
				continue
			}
			value = flowStrings(ids)
		}
		items = append(items, yaml.MapItem{Key: f.Key, Value: value})
	}
	out, err := yaml.Marshal(items)
	if err != nil {
		return nil, fmt.Errorf("serialize design: %w", err)
	}
	return out, nil
}

type quoted string

func (q quoted) MarshalYAML() ([]byte, error) {
	return fmt.Appendf(nil, "%q", string(q)), nil
}

func appendList(items yaml.MapSlice, key string, list []string) yaml.MapSlice {
	if len(list) == 0 {
		return items
	}
	return append(items, yaml.MapItem{Key: key, Value: flowStrings(list)})
}

// Compose joins a serialized interior and body into a design file.
func Compose(interior, body []byte) []byte {
	return frontmatter.Compose(interior, body)
}

func producesIDs(m *frontmatter.Model) ([]string, bool, error) {
	for _, f := range m.Custom {
		if f.Key != KeyProduces {
			continue
		}
		ids, err := frontmatter.StringList(f.Value)
		return ids, true, err
	}
	return nil, false, nil
}

func setProduces(m *frontmatter.Model, ids []string) {
	m.RemoveCustom(KeyProduces)
	if len(ids) == 0 {
		return
	}
	m.Custom = append(m.Custom, frontmatter.Field{Key: KeyProduces, Value: append([]string(nil), ids...)})
}

func containsID(ids []string, id string) bool {
	for _, e := range ids {
		if e == id {
			return true
		}
	}
	return false
}

func dropID(ids []string, id string) []string {
	out := make([]string, 0, len(ids))
	for _, e := range ids {
		if e != id {
			out = append(out, e)
		}
	}
	return out
}

func rfc3339(now time.Time) string {
	if now.IsZero() {
		now = time.Now()
	}
	return now.Format(time.RFC3339)
}
