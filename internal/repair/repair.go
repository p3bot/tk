// Package repair holds deterministic, bit-identical integrity-repair procedures:
// id-collision loser pick and short-id extension, equal-order and over-long-order
// re-space, and archive-layout move. No crypto/rand, dirent order, mtime, or pointer
// identity enters a decision. Returns rewrite.Op values only — never writes files.
package repair

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/p3bot/tk/internal/collision"
	"github.com/p3bot/tk/internal/design"
	"github.com/p3bot/tk/internal/frontmatter"
	"github.com/p3bot/tk/internal/id"
	"github.com/p3bot/tk/internal/order"
	"github.com/p3bot/tk/internal/rewrite"
	"github.com/p3bot/tk/internal/scopefile"
)

// OrderLongThreshold is the soft length above which an order key is eligible for re-space.
const OrderLongThreshold = 64

// Row is the minimal projection of an indexed ticket a repair procedure needs.
type Row struct {
	Path       string
	FullID     string
	ShortID    string
	OrderKey   string
	ParseError bool
}

// Rename records one collision-loser rename for the operation's report and commit message.
type Rename struct {
	OldID   string
	NewID   string
	OldPath string
	NewPath string
}

// SharedRow is one holder of a short id that a design also holds.
// Design selects the design serializer and keeps the file under design/.
type SharedRow struct {
	Row
	Design bool
}

// member is one file in a same-id collision for the deterministic loser pick.
type member struct {
	path     string
	basename string
	created  string
	shortID  string
	raw      []byte
	model    *frontmatter.Model
	body     []byte
	design   bool
}

// DuplicateID builds ops that resolve one duplicate-id collision: keep the
// deterministically chosen member, rename every other by short-id extension.
// occupied maps short-ids to holding paths (caller-owned; extended with each mint)
// so re-entry can recognise a loser a crashed prior run already extended.
// Edges on losers are left untouched because the kept side retains the original id.
func DuplicateID(scope string, rows []Row, occupied map[string]string) ([]rewrite.Op, []Rename, error) {
	if len(rows) < 2 {
		return nil, nil, fmt.Errorf("duplicate-id repair needs at least two members, got %d", len(rows))
	}
	members := make([]member, 0, len(rows))
	for _, r := range rows {
		m, err := readMember(r)
		if err != nil {
			return nil, nil, err
		}
		members = append(members, m)
	}
	// Kept member sorts first; losers follow.
	sort.SliceStable(members, func(i, j int) bool { return keepBefore(members[i], members[j]) })

	taken := make(map[string]struct{}, len(occupied))
	for short := range occupied {
		taken[short] = struct{}{}
	}

	var ops []rewrite.Op
	var renames []Rename
	for _, loser := range members[1:] {
		op, rn, resumed, err := resumeExtension(loser, scope, occupied, func(newID string) ([]byte, error) {
			m := *loser.model
			m.ID = newID
			return serialize(&m, loser.body)
		})
		if err != nil {
			return nil, nil, err
		}
		if resumed {
			ops = append(ops, op)
			renames = append(renames, rn)
			continue
		}
		newShort, err := id.Extend(loser.shortID, taken)
		if err != nil {
			return nil, nil, fmt.Errorf("collision repair for %s: %w (files %s)", loser.model.ID, err, membersPaths(members))
		}
		taken[newShort] = struct{}{}
		newID := scope + "-" + newShort
		newPath := filepath.Join(filepath.Dir(loser.path), Basename(loser.basename, newID))

		m := *loser.model
		m.ID = newID
		content, err := serialize(&m, loser.body)
		if err != nil {
			return nil, nil, err
		}
		occupied[newShort] = newPath
		ops = append(ops, rewrite.Op{OldPath: loser.path, NewPath: newPath, Content: content})
		renames = append(renames, Rename{OldID: loser.model.ID, NewID: newID, OldPath: loser.path, NewPath: newPath})
	}
	return ops, renames, nil
}

// resumeExtension recognises a loser already extended by a crashed prior run.
// Recognition is by content modulo id (extension rewrites the id, so not byte-identical).
// content renders the bytes that extension would write for newID.
func resumeExtension(loser member, scope string, occupied map[string]string, content func(newID string) ([]byte, error)) (rewrite.Op, Rename, bool, error) {
	var candidates []string
	for short := range occupied {
		if len(short) > len(loser.shortID) && strings.HasPrefix(short, loser.shortID) {
			candidates = append(candidates, short)
		}
	}
	sort.Strings(candidates)

	for _, short := range candidates {
		newID := scope + "-" + short
		rendered, err := content(newID)
		if err != nil {
			return rewrite.Op{}, Rename{}, false, err
		}
		path := occupied[short]
		raw, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return rewrite.Op{}, Rename{}, false, fmt.Errorf("read %s: %w", path, err)
		}
		if !bytes.Equal(raw, rendered) {
			continue
		}
		return rewrite.Op{OldPath: loser.path, NewPath: path, Content: rendered},
			Rename{OldID: loser.model.ID, NewID: newID, OldPath: loser.path, NewPath: path}, true, nil
	}
	return rewrite.Op{}, Rename{}, false, nil
}

// linkPlan is how a shared-id repair rewrites entries that name the old id.
// dest empty and self false leaves every entry in place.
// self means the replacement is the loser's own new id (the one extended ticket).
type linkPlan struct {
	oldID string
	dest  string
	self  bool
}

// SharedID resolves one short id held by at least one design and one other file.
// The keeper is collision.KeepBefore over every holder. Losers are extended in
// keeper order, except when links move: that one ticket is extended first so
// resume can render the replacement id into the other losers' bytes.
// A design loser stays in its directory and uses the design serializer, so an
// absent order key stays absent. A ticket loser stays in its directory.
// retarget is the extended ticket's new id when the keeper is a design and
// exactly one ticket was extended. It is empty when entries stay on the old id.
func SharedID(scope, dir string, rows []SharedRow, occupied map[string]string) ([]rewrite.Op, []Rename, string, error) {
	if len(rows) < 2 {
		return nil, nil, "", fmt.Errorf("shared-id repair needs at least two members, got %d", len(rows))
	}
	members := make([]member, 0, len(rows))
	designs := 0
	for _, r := range rows {
		m, err := readMember(r.Row)
		if err != nil {
			return nil, nil, "", err
		}
		m.design = r.Design
		if r.Design {
			designs++
		}
		members = append(members, m)
	}
	if designs == 0 {
		return nil, nil, "", fmt.Errorf("shared-id repair needs a design holder")
	}
	sort.SliceStable(members, func(i, j int) bool { return keepBefore(members[i], members[j]) })

	oldID := members[0].model.ID
	if rows[0].FullID != "" {
		oldID = rows[0].FullID
	}
	losers := members[1:]
	var ticketLoser *member
	ticketCount := 0
	for i := range losers {
		if !losers[i].design {
			ticketCount++
			ticketLoser = &losers[i]
		}
	}
	// Links move only for this one shape. The ticket's new id has to be known
	// before the other losers are rendered, or a resumed file will not match.
	willRetarget := members[0].design && ticketCount == 1
	ordered := losers
	if willRetarget {
		ordered = make([]member, 0, len(losers))
		ordered = append(ordered, *ticketLoser)
		for _, loser := range losers {
			if loser.path == ticketLoser.path {
				continue
			}
			ordered = append(ordered, loser)
		}
	}

	taken := make(map[string]struct{}, len(occupied))
	for short := range occupied {
		taken[short] = struct{}{}
	}
	var ops []rewrite.Op
	var renames []Rename
	skip := map[string]bool{}
	retarget := ""
	for _, loser := range ordered {
		plan := linkPlan{oldID: oldID}
		if willRetarget && loser.path == ticketLoser.path {
			plan.self = true
		} else if willRetarget {
			plan.dest = retarget
		}
		op, rn, err := planLoser(loser, scope, plan, occupied, taken)
		if err != nil {
			return nil, nil, "", err
		}
		if willRetarget && loser.path == ticketLoser.path {
			retarget = rn.NewID
		}
		ops = append(ops, op)
		renames = append(renames, rn)
		skip[loser.path] = true
		skip[rn.NewPath] = true
	}
	if retarget == "" {
		return ops, renames, "", nil
	}
	rewrites, err := retargetScope(dir, scope, oldID, retarget, skip)
	if err != nil {
		return nil, nil, "", err
	}
	ops = append(ops, rewrites...)
	return ops, renames, retarget, nil
}

// planLoser extends one loser. Resume matches the final bytes, and the id-only
// bytes from a crash before the link rewrite, then overwrites those with the final bytes.
func planLoser(loser member, scope string, plan linkPlan, occupied map[string]string, taken map[string]struct{}) (rewrite.Op, Rename, error) {
	final := func(newID string) ([]byte, error) {
		return renderLoser(loser, newID, plan)
	}
	op, rn, resumed, err := resumeExtension(loser, scope, occupied, final)
	if err != nil {
		return rewrite.Op{}, Rename{}, err
	}
	if !resumed && (plan.self || plan.dest != "") {
		op, rn, resumed, err = resumeExtension(loser, scope, occupied, func(newID string) ([]byte, error) {
			return renderLoser(loser, newID, linkPlan{})
		})
		if err != nil {
			return rewrite.Op{}, Rename{}, err
		}
		if resumed {
			rendered, err := final(rn.NewID)
			if err != nil {
				return rewrite.Op{}, Rename{}, err
			}
			op.Content = rendered
		}
	}
	if resumed {
		return op, rn, nil
	}
	newShort, err := id.Extend(loser.shortID, taken)
	if err != nil {
		return rewrite.Op{}, Rename{}, fmt.Errorf("collision repair for %s: %w (file %s)", loser.model.ID, err, loser.path)
	}
	taken[newShort] = struct{}{}
	newID := scope + "-" + newShort
	rendered, err := final(newID)
	if err != nil {
		return rewrite.Op{}, Rename{}, err
	}
	newPath := filepath.Join(filepath.Dir(loser.path), Basename(loser.basename, newID))
	occupied[newShort] = newPath
	return rewrite.Op{OldPath: loser.path, NewPath: newPath, Content: rendered},
		Rename{OldID: loser.model.ID, NewID: newID, OldPath: loser.path, NewPath: newPath}, nil
}

func renderLoser(loser member, newID string, plan linkPlan) ([]byte, error) {
	m := cloneModel(loser.model)
	m.ID = newID
	dest := plan.dest
	if plan.self {
		dest = newID
	}
	if dest != "" {
		retargetModel(m, plan.oldID, dest)
	}
	return loser.bytes(m)
}

func (m member) bytes(model *frontmatter.Model) ([]byte, error) {
	if m.design {
		interior, err := design.Serialize(model)
		if err != nil {
			return nil, err
		}
		return design.Compose(interior, m.body), nil
	}
	return serialize(model, m.body)
}

// retargetScope rewrites same-scope depends, related, and produces entries
// that name oldID. Paths in skip were already rendered with the replacement.
func retargetScope(dir, scope, oldID, newID string, skip map[string]bool) ([]rewrite.Op, error) {
	tickets, err := scopefile.ListTickets(dir)
	if err != nil {
		return nil, err
	}
	designs, err := design.Files(dir, scope)
	if err != nil {
		return nil, err
	}
	sort.Slice(designs, func(i, j int) bool { return designs[i].Path < designs[j].Path })
	var ops []rewrite.Op
	for _, path := range tickets {
		if skip[path] {
			continue
		}
		op, ok, err := retargetTicket(path, oldID, newID)
		if err != nil {
			return nil, err
		}
		if ok {
			ops = append(ops, op)
		}
	}
	for _, f := range designs {
		if skip[f.Path] || f.Model == nil {
			continue
		}
		op, ok, err := retargetDesign(f, oldID, newID)
		if err != nil {
			return nil, err
		}
		if ok {
			ops = append(ops, op)
		}
	}
	return ops, nil
}

func retargetTicket(path, oldID, newID string) (rewrite.Op, bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return rewrite.Op{}, false, fmt.Errorf("read %s: %w", path, err)
	}
	interior, body, present := frontmatter.Split(raw)
	if !present {
		return rewrite.Op{}, false, nil
	}
	m, err := frontmatter.Parse(interior)
	if err != nil {
		return rewrite.Op{}, false, nil
	}
	c := cloneModel(m)
	if !retargetModel(c, oldID, newID) {
		return rewrite.Op{}, false, nil
	}
	content, err := serialize(c, body)
	if err != nil {
		return rewrite.Op{}, false, err
	}
	return rewrite.Op{OldPath: path, NewPath: path, Content: content}, true, nil
}

func retargetDesign(f design.File, oldID, newID string) (rewrite.Op, bool, error) {
	c := cloneModel(f.Model)
	if !retargetModel(c, oldID, newID) {
		return rewrite.Op{}, false, nil
	}
	interior, err := design.Serialize(c)
	if err != nil {
		return rewrite.Op{}, false, err
	}
	return rewrite.Op{OldPath: f.Path, NewPath: f.Path, Content: design.Compose(interior, f.Body)}, true, nil
}

func retargetModel(m *frontmatter.Model, oldID, newID string) bool {
	changed := rewriteList(&m.Depends, oldID, newID)
	if rewriteList(&m.Related, oldID, newID) {
		changed = true
	}
	for i, f := range m.Custom {
		if f.Key != design.KeyProduces {
			continue
		}
		ids, err := frontmatter.StringList(f.Value)
		if err != nil {
			continue
		}
		if rewriteList(&ids, oldID, newID) {
			m.Custom[i].Value = ids
			changed = true
		}
	}
	return changed
}

func rewriteList(ids *[]string, oldID, newID string) bool {
	changed := false
	for i, id := range *ids {
		if id == oldID {
			(*ids)[i] = newID
			changed = true
		}
	}
	return changed
}

func cloneModel(m *frontmatter.Model) *frontmatter.Model {
	c := *m
	c.Depends = append([]string(nil), m.Depends...)
	c.Related = append([]string(nil), m.Related...)
	c.Tags = append([]string(nil), m.Tags...)
	c.Links = append([]string(nil), m.Links...)
	c.StatusConflict = append([]string(nil), m.StatusConflict...)
	c.Custom = append([]frontmatter.Field(nil), m.Custom...)
	return &c
}

// EqualOrder builds ops that re-space equal (tied) order keys, preserving (order, id) relative order.
func EqualOrder(rows []Row) ([]rewrite.Op, error) {
	valid := validOrderRows(rows)
	counts := map[string]int{}
	for _, r := range valid {
		counts[r.OrderKey]++
	}
	return respace(valid, func(r Row) bool { return counts[r.OrderKey] > 1 })
}

// LongOrder builds ops that re-space pathologically long order keys into shorter legal keys.
func LongOrder(rows []Row) ([]rewrite.Op, error) {
	valid := validOrderRows(rows)
	return respace(valid, func(r Row) bool { return len(r.OrderKey) > OrderLongThreshold })
}

// ArchiveMove builds the op that relocates a ticket file across the archive boundary
// to match terminal-ness. Frontmatter is unchanged (byte-for-byte preservation).
func ArchiveMove(dir string, row Row, terminal bool) (rewrite.Op, error) {
	raw, err := os.ReadFile(row.Path)
	if err != nil {
		return rewrite.Op{}, fmt.Errorf("read %s: %w", row.Path, err)
	}
	base := filepath.Base(row.Path)
	newPath := filepath.Join(dir, base)
	if terminal {
		newPath = filepath.Join(dir, "archive", base)
	}
	return rewrite.Op{OldPath: row.Path, NewPath: newPath, Content: raw}, nil
}

// InterruptedMove reports whether a same-id set is the both-present window of an
// interrupted archive-layout move: two byte-identical copies, one at dir root and one
// under archive/. Extending a short-id here would fork one ticket into two ids.
func InterruptedMove(dir string, rows []Row) (bool, error) {
	archiveDir := filepath.Join(dir, "archive")
	var atRoot, archived []Row
	for _, r := range rows {
		switch filepath.Dir(r.Path) {
		case dir:
			atRoot = append(atRoot, r)
		case archiveDir:
			archived = append(archived, r)
		}
	}
	for _, a := range atRoot {
		for _, b := range archived {
			same, err := sameContent(a.Path, b.Path)
			if err != nil {
				return false, err
			}
			if same {
				return true, nil
			}
		}
	}
	return false, nil
}

func sameContent(a, b string) (bool, error) {
	ra, err := os.ReadFile(a)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", a, err)
	}
	rb, err := os.ReadFile(b)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", b, err)
	}
	return bytes.Equal(ra, rb), nil
}

// respace assigns distinct legal keys to rows needing rewrite, minting each between
// the running previous key and the nearest untied right anchor.
func respace(valid []Row, needsRewrite func(Row) bool) ([]rewrite.Op, error) {
	sort.SliceStable(valid, func(i, j int) bool {
		if valid[i].OrderKey != valid[j].OrderKey {
			return valid[i].OrderKey < valid[j].OrderKey
		}
		return valid[i].FullID < valid[j].FullID
	})
	rewriteAt := make([]bool, len(valid))
	for i, r := range valid {
		rewriteAt[i] = needsRewrite(r)
	}

	var ops []rewrite.Op
	prev := ""
	for i, r := range valid {
		if !rewriteAt[i] {
			prev = r.OrderKey
			continue
		}
		right := ""
		for j := i + 1; j < len(valid); j++ {
			if !rewriteAt[j] {
				right = valid[j].OrderKey
				break
			}
		}
		newKey, err := order.KeyBetween(prev, right)
		if err != nil {
			return nil, fmt.Errorf("re-space order for %s between %q and %q: %w", r.FullID, prev, right, err)
		}
		prev = newKey
		if newKey == r.OrderKey {
			continue
		}
		op, err := orderRewriteOp(r.Path, newKey)
		if err != nil {
			return nil, err
		}
		ops = append(ops, op)
	}
	return ops, nil
}

// orderRewriteOp rewrites only the order key of a ticket file as an in-place op.
func orderRewriteOp(path, newKey string) (rewrite.Op, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return rewrite.Op{}, fmt.Errorf("read %s: %w", path, err)
	}
	interior, body, present := frontmatter.Split(raw)
	if !present {
		return rewrite.Op{}, fmt.Errorf("%s has no frontmatter fence", path)
	}
	m, err := frontmatter.Parse(interior)
	if err != nil {
		return rewrite.Op{}, fmt.Errorf("parse %s: %w", path, err)
	}
	m.Order = newKey
	content, err := serialize(m, body)
	if err != nil {
		return rewrite.Op{}, err
	}
	return rewrite.Op{OldPath: path, NewPath: path, Content: content}, nil
}

// readMember reads one collision member for the loser pick and id rewrite.
func readMember(r Row) (member, error) {
	raw, err := os.ReadFile(r.Path)
	if err != nil {
		return member{}, fmt.Errorf("read %s: %w", r.Path, err)
	}
	interior, body, present := frontmatter.Split(raw)
	if !present {
		return member{}, fmt.Errorf("%s has no frontmatter fence", r.Path)
	}
	m, err := frontmatter.Parse(interior)
	if err != nil {
		return member{}, fmt.Errorf("parse %s: %w", r.Path, err)
	}
	return member{
		path:     r.Path,
		basename: filepath.Base(r.Path),
		created:  m.Created,
		shortID:  r.ShortID,
		raw:      raw,
		model:    m,
		body:     body,
	}, nil
}

// keepBefore adapts disk-backed members onto collision.KeepBefore.
func keepBefore(a, b member) bool {
	return collision.KeepBefore(a.toCollision(), b.toCollision())
}

func (m member) toCollision() collision.Member {
	return collision.Member{Created: m.created, Basename: m.basename, Raw: m.raw, Path: m.path}
}

// Basename is the ticket filename for newID that preserves base's frozen slug.
// Never consults the old id, because filename and frontmatter id can disagree;
// scope names and short-ids contain no hyphen, so the first two segments are the id.
func Basename(base, newID string) string {
	stem := strings.TrimSuffix(base, ".md")
	parts := strings.SplitN(stem, "-", 3)
	if len(parts) < 3 || parts[2] == "" {
		return newID + ".md"
	}
	return newID + "-" + parts[2] + ".md"
}

func serialize(m *frontmatter.Model, body []byte) ([]byte, error) {
	interior, err := frontmatter.Serialize(m)
	if err != nil {
		return nil, err
	}
	return frontmatter.Compose(interior, body), nil
}

func validOrderRows(rows []Row) []Row {
	out := make([]Row, 0, len(rows))
	for _, r := range rows {
		if !r.ParseError && order.Valid(r.OrderKey) {
			out = append(out, r)
		}
	}
	return out
}

func membersPaths(members []member) string {
	paths := make([]string, len(members))
	for i, m := range members {
		paths[i] = m.path
	}
	return strings.Join(paths, ", ")
}
