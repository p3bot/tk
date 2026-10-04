package reconcile

import (
	"bytes"
	"os"
	"strings"

	"github.com/p3bot/tk/internal/frontmatter"
	"github.com/p3bot/tk/internal/id"
	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/title"
)

// conflictMarkers force parse_error quarantine when inside the frontmatter fence
// (body-only markers leave FM indexing intact).
var conflictMarkers = [][]byte{[]byte("<<<<<<<"), []byte("======="), []byte(">>>>>>>")}

// parseFile materializes one ticket file. Bad content yields a parse_error quarantine
// row (id from filename, body FTS-indexed), not an error. Real I/O faults still error.
func parseFile(path, scope, fullID string, archived bool, mtimeNS, size int64) (*index.Ticket, []index.Edge, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}

	interior, body, present := frontmatter.Split(data)
	base := &index.Ticket{
		Path: path, Scope: scope, ID: fullID, ShortID: shortOf(fullID),
		Archived: archived, MtimeNS: mtimeNS, Size: size, Body: body,
	}

	if !present || containsConflictMarker(interior) {
		return quarantine(base, data, "frontmatter fence missing, broken, or carries conflict markers"), nil, nil
	}
	model, err := frontmatter.Parse(interior)
	if err != nil {
		return quarantine(base, data, err.Error()), nil, nil
	}

	return indexFromModel(base, model)
}

// quarantine finishes a parse_error row: no trusted FM fields; raw file goes to FTS.
func quarantine(base *index.Ticket, raw []byte, msg string) *index.Ticket {
	base.ParseError = true
	base.ParseMsg = msg
	base.Status = ""
	base.Body = raw
	return base
}

// indexFromModel fills a healthy row and materializes edges. Malformed depends/related
// set SchemaError and are omitted from edges.
func indexFromModel(base *index.Ticket, m *frontmatter.Model) (*index.Ticket, []index.Edge, error) {
	base.Status = m.Status
	base.OrderKey = m.Order
	base.Summary = m.Summary
	base.Created = m.Created
	base.Changed = m.Changed
	base.Tags = m.Tags
	base.StatusConflict = m.StatusConflict
	base.Title = title.Extract(base.Body)
	if len(m.Custom) > 0 {
		base.Custom = map[string]any{}
		for _, f := range m.Custom {
			base.Custom[f.Key] = f.Value
		}
	}
	// Frontmatter id is authoritative when same-scope; foreign-scope ids are not
	// adopted (would leave scope vs id-prefix disagreeing and break get/meta).
	if id.IsFullTicketID(m.ID) && scopeOf(m.ID) == base.Scope {
		base.ID = m.ID
		base.ShortID = shortOf(m.ID)
	}

	edges, schemaErr := edgesFrom(base, m)
	base.SchemaError = schemaErr
	return base, edges, nil
}

// edgesFrom skips (and flags) any depends/related entry that is not a legal full id.
func edgesFrom(base *index.Ticket, m *frontmatter.Model) ([]index.Edge, bool) {
	var edges []index.Edge
	schemaErr := false
	add := func(list []string, kind string) {
		for _, target := range list {
			if !id.IsFullTicketID(target) {
				schemaErr = true
				continue
			}
			edges = append(edges, index.Edge{
				FromPath: base.Path, FromID: base.ID, FromScope: base.Scope,
				ToID: target, ToScope: scopeOf(target), Kind: kind,
			})
		}
	}
	add(m.Depends, index.EdgeDepends)
	add(m.Related, index.EdgeRelated)
	return edges, schemaErr
}

// parseDesign materializes one design file. Bad content yields a parse_error
// row (id from the filename, raw file indexed), not an error. An unknown
// status is a normal row. Real I/O faults still error.
func parseDesign(path, scope, fullID string, mtimeNS, size int64) (*index.Design, []index.Edge, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	base := &index.Design{
		Path: path, Scope: scope, ID: fullID, ShortID: shortOf(fullID),
		MtimeNS: mtimeNS, Size: size,
	}
	interior, body, present := frontmatter.Split(data)
	if !present || containsConflictMarker(interior) {
		base.ParseError = true
		base.ParseMsg = "frontmatter fence missing, broken, or carries conflict markers"
		base.Body = data
		return base, nil, nil
	}
	model, err := frontmatter.Parse(interior)
	if err != nil {
		base.ParseError = true
		base.ParseMsg = err.Error()
		base.Body = data
		return base, nil, nil
	}
	base.Status = model.Status
	base.Summary = model.Summary
	base.Created = model.Created
	base.Changed = model.Changed
	base.Body = body
	base.Title = title.Extract(body)
	if id.IsFullTicketID(model.ID) && scopeOf(model.ID) == base.Scope {
		base.ID = model.ID
		base.ShortID = shortOf(model.ID)
	}
	return base, producesEdges(base, model), nil
}

// producesEdges keeps produces entries that are full ticket ids. A value that
// is not a list, and an entry that is not a full ticket id, are not edges.
func producesEdges(base *index.Design, m *frontmatter.Model) []index.Edge {
	var edges []index.Edge
	for _, f := range m.Custom {
		if f.Key != index.EdgeProduces {
			continue
		}
		ids, err := frontmatter.StringList(f.Value)
		if err != nil {
			continue
		}
		for _, target := range ids {
			if !id.IsFullTicketID(target) {
				continue
			}
			edges = append(edges, index.Edge{
				FromPath: base.Path, FromID: base.ID, FromScope: base.Scope,
				ToID: target, ToScope: scopeOf(target), Kind: index.EdgeProduces,
			})
		}
	}
	return edges
}

func containsConflictMarker(interior []byte) bool {
	for len(interior) > 0 {
		var line []byte
		if i := bytes.IndexByte(interior, '\n'); i >= 0 {
			line, interior = interior[:i], interior[i+1:]
		} else {
			line, interior = interior, nil
		}
		for _, marker := range conflictMarkers {
			if bytes.HasPrefix(line, marker) {
				return true
			}
		}
	}
	return false
}

func shortOf(fullID string) string {
	if !id.IsFullTicketID(fullID) {
		return ""
	}
	return fullID[strings.IndexByte(fullID, '-')+1:]
}

func scopeOf(fullID string) string {
	if i := strings.IndexByte(fullID, '-'); i >= 0 {
		return fullID[:i]
	}
	return ""
}
