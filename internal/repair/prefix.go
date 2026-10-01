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
	"github.com/p3bot/tk/internal/rewrite"
	"github.com/p3bot/tk/internal/scopefile"
	"github.com/p3bot/tk/internal/token"
)

// PrefixRename is one file adopted into scope by rewriting its id prefix.
// OldID is the filename id (the id doctor reported). Claimed are the foreign
// ids whose inbound references now mean NewID; a same-scope id is never claimed,
// so an existing ticket keeps the edges that already name it.
type PrefixRename struct {
	OldID   string
	NewID   string
	OldPath string
	NewPath string
	Claimed []string
}

// AdoptPrefix plans in-place rewrites for ticket and design files whose filename
// id prefix is not scope. The frontmatter short is kept when that id is a full
// ticket id; otherwise the filename short is kept. A short the scope already
// holds is extended, and when several files share a short the collision keeper
// keeps it. A shared old id is inherited by the keeper among the files that
// bear it, not by short order. Same-scope depends, related, and produces that
// name a claimed foreign id are rewritten. An unparseable adopted file is renamed and its
// fence is left untouched. An unparseable file that names a claimed id refuses
// the batch, so that reference is not dropped. An adopted file may name the ids
// it itself bears.
// held are full ids already owned by a ticket or design in another scope.
// Claiming one would retarget edges that name that file. A nil map holds nothing.
//
// Returned ops plant every new file and same-scope edge first (a plant has an
// empty OldPath, or rewrites a file in place). Trailing ops then remove the old
// names. A destination already on disk is reused only when its bytes are the
// bytes this plan writes, so a rerun while the old names remain builds the same ids.
func AdoptPrefix(scope, dir string, held map[string]struct{}) ([]rewrite.Op, []PrefixRename, error) {
	files, err := scopefile.PrefixMismatches(dir, scope)
	if err != nil || len(files) == 0 {
		return nil, nil, err
	}
	adoptees, err := loadAdoptees(dir, files)
	if err != nil {
		return nil, nil, err
	}
	ordered, mapped, claimedByPath, err := planPrefixIDs(scope, dir, adoptees, held)
	if err != nil {
		return nil, nil, err
	}
	renames := make([]PrefixRename, 0, len(ordered))
	skip := map[string]bool{}
	for _, a := range ordered {
		claimed := claimedByPath[a.path]
		sort.Strings(claimed)
		renames = append(renames, PrefixRename{
			OldID: a.filenameID, NewID: a.newID, OldPath: a.path, NewPath: a.newPath, Claimed: claimed,
		})
		skip[a.path] = true
	}
	sort.Slice(renames, func(i, j int) bool { return renames[i].OldPath < renames[j].OldPath })

	byPath := map[string]*adoptee{}
	for _, a := range ordered {
		byPath[a.path] = a
	}
	plant := make([]rewrite.Op, 0, len(ordered))
	unlink := make([]rewrite.Op, 0, len(ordered))
	for _, r := range renames {
		content, err := byPath[r.OldPath].content(mapped)
		if err != nil {
			return nil, nil, err
		}
		// Empty OldPath writes the new file and leaves the old name in place.
		plant = append(plant, rewrite.Op{NewPath: r.NewPath, Content: content})
		unlink = append(unlink, rewrite.Op{OldPath: r.OldPath, NewPath: r.NewPath, Content: content})
	}
	borne := make(map[string]map[string]struct{}, len(ordered))
	for _, a := range ordered {
		set := make(map[string]struct{}, 2)
		for _, old := range foreignIDs(scope, a) {
			set[old] = struct{}{}
		}
		borne[a.path] = set
	}
	referrers, err := planPrefixReferrers(dir, mapped, skip, borne)
	if err != nil {
		return nil, nil, err
	}
	ops := make([]rewrite.Op, 0, len(plant)+len(referrers)+len(unlink))
	ops = append(ops, plant...)
	ops = append(ops, referrers...)
	ops = append(ops, unlink...)
	return ops, renames, nil
}

type adoptee struct {
	path       string
	filenameID string
	base       string
	design     bool
	raw        []byte
	model      *frontmatter.Model
	body       []byte
	short      string
	newID      string
	newPath    string
}

func loadAdoptees(dir string, files []scopefile.PrefixMismatch) ([]*adoptee, error) {
	out := make([]*adoptee, 0, len(files))
	for _, f := range files {
		a, err := readAdoptee(dir, f)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

func readAdoptee(dir string, f scopefile.PrefixMismatch) (*adoptee, error) {
	raw, err := os.ReadFile(f.Path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", f.Path, err)
	}
	a := &adoptee{
		path: f.Path, filenameID: f.ID, base: filepath.Base(f.Path), raw: raw,
		design: filepath.Dir(f.Path) == filepath.Join(dir, scopefile.DesignDir),
		short:  strings.TrimPrefix(f.ID, id.ScopeOfFullID(f.ID)+"-"),
	}
	interior, body, present := frontmatter.Split(raw)
	if !present {
		return a, nil
	}
	m, err := frontmatter.Parse(interior)
	if err != nil {
		return a, nil
	}
	a.model = m
	a.body = body
	if id.IsFullTicketID(m.ID) {
		a.short = strings.TrimPrefix(m.ID, id.ScopeOfFullID(m.ID)+"-")
	}
	return a, nil
}

// resumeHit is a same-scope file in the adoptee's directory with the same slug
// and body, whose short is the adoptee's short or an extension of it.
type resumeHit struct {
	path  string
	short string
	raw   []byte
}

// planPrefixIDs assigns new ids, reusing a destination already written for an
// adoptee when that file's bytes are what this plan would write. Other files
// that share the slug stay in the occupied set, so their short stays taken.
func planPrefixIDs(scope, dir string, adoptees []*adoptee, held map[string]struct{}) ([]*adoptee, map[string]string, map[string][]string, error) {
	occupied, err := scopefile.OccupiedShortPaths(dir, scope)
	if err != nil {
		return nil, nil, nil, err
	}
	cands := make(map[string][]resumeHit, len(adoptees))
	var candPaths []string
	for _, a := range adoptees {
		hits, err := resumeCandidates(scope, a)
		if err != nil {
			return nil, nil, nil, err
		}
		cands[a.path] = hits
		for _, h := range hits {
			candPaths = append(candPaths, h.path)
		}
	}

	accepted := map[string]resumeHit{}
	rejected := map[string]map[string]bool{}
	// Each round accepts a destination or retires one that matched only while
	// sibling candidates were hidden. Retiring stops a pin from being retried.
	for round := 0; round < len(adoptees)*2+1; round++ {
		progressed := false
		for _, a := range adoptees {
			if _, ok := accepted[a.path]; ok {
				continue
			}
			for _, hit := range cands[a.path] {
				if rejected[a.path][hit.path] {
					continue
				}
				trial := copyPins(accepted)
				trial[a.path] = hit
				ord, err := assignPinned(scope, adoptees, occupied, trial, candPaths)
				if err != nil {
					return nil, nil, nil, err
				}
				trialMapped, _ := claimKeptIDs(scope, ord, held)
				got := adopteeByPath(ord, a.path)
				want, err := got.content(trialMapped)
				if err != nil {
					return nil, nil, nil, err
				}
				if got.newID == scope+"-"+hit.short && bytes.Equal(want, hit.raw) {
					accepted[a.path] = hit
					progressed = true
					break
				}
			}
		}
		ordered, err := assignPinned(scope, adoptees, occupied, accepted, nil)
		if err != nil {
			return nil, nil, nil, err
		}
		mapped, _ := claimKeptIDs(scope, ordered, held)
		mismatch := false
		for _, a := range ordered {
			hit, ok := accepted[a.path]
			if !ok {
				continue
			}
			want, err := a.content(mapped)
			if err != nil {
				return nil, nil, nil, err
			}
			if a.newID == scope+"-"+hit.short && bytes.Equal(want, hit.raw) {
				continue
			}
			if rejected[a.path] == nil {
				rejected[a.path] = map[string]bool{}
			}
			rejected[a.path][hit.path] = true
			delete(accepted, a.path)
			mismatch = true
		}
		if mismatch || progressed {
			continue
		}
		break
	}
	ordered, err := assignPinned(scope, adoptees, occupied, accepted, nil)
	if err != nil {
		return nil, nil, nil, err
	}
	mapped, claimedByPath := claimKeptIDs(scope, ordered, held)
	return ordered, mapped, claimedByPath, nil
}

func copyPins(in map[string]resumeHit) map[string]resumeHit {
	out := make(map[string]resumeHit, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	return out
}

func adopteeByPath(adoptees []*adoptee, path string) *adoptee {
	for _, a := range adoptees {
		if a.path == path {
			return a
		}
	}
	return nil
}

// assignPinned gives each pinned adoptee that destination's id. exclude lists
// paths dropped from occupancy for this assignment: nil keeps every file, and
// a trial passes every resume candidate so sibling destinations do not look
// like strangers. Pinned shorts stay taken either way.
func assignPinned(scope string, adoptees []*adoptee, occupied map[string]string, pins map[string]resumeHit, exclude []string) ([]*adoptee, error) {
	skip := map[string]bool{}
	for _, h := range pins {
		skip[h.path] = true
	}
	for _, p := range exclude {
		skip[p] = true
	}
	taken := make(map[string]struct{}, len(occupied))
	for short, path := range occupied {
		if skip[path] {
			continue
		}
		taken[short] = struct{}{}
	}
	for _, h := range pins {
		taken[h.short] = struct{}{}
	}

	groups := map[string][]*adoptee{}
	for _, a := range adoptees {
		groups[a.short] = append(groups[a.short], a)
	}
	shorts := make([]string, 0, len(groups))
	for short := range groups {
		shorts = append(shorts, short)
	}
	sort.Strings(shorts)

	var ordered []*adoptee
	for _, short := range shorts {
		group := groups[short]
		sort.SliceStable(group, func(i, j int) bool {
			return collision.KeepBefore(group[i].member(), group[j].member())
		})
		for _, a := range group {
			if hit, ok := pins[a.path]; ok {
				a.newID = scope + "-" + hit.short
				a.newPath = hit.path
				ordered = append(ordered, a)
				continue
			}
			got, err := takeShort(a.short, taken)
			if err != nil {
				return nil, fmt.Errorf("repair id prefix for %s: %w", a.path, err)
			}
			a.newID = scope + "-" + got
			a.newPath = filepath.Join(filepath.Dir(a.path), Basename(a.base, a.newID))
			ordered = append(ordered, a)
		}
	}
	return ordered, nil
}

func resumeCandidates(scope string, a *adoptee) ([]resumeHit, error) {
	root := filepath.Dir(a.path)
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var hits []resumeHit
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		full, ok := scopefile.TicketIDFromBase(e.Name())
		if !ok || id.ScopeOfFullID(full) != scope {
			continue
		}
		short := strings.TrimPrefix(full, scope+"-")
		if !shortInFamily(short, a.short) || Basename(a.base, full) != e.Name() {
			continue
		}
		path := filepath.Join(root, e.Name())
		if path == a.path {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		if !sameBody(a, raw) {
			continue
		}
		hits = append(hits, resumeHit{path: path, short: short, raw: raw})
	}
	sort.Slice(hits, func(i, j int) bool {
		if len(hits[i].short) != len(hits[j].short) {
			return len(hits[i].short) < len(hits[j].short)
		}
		if hits[i].short != hits[j].short {
			return hits[i].short < hits[j].short
		}
		return hits[i].path < hits[j].path
	})
	return hits, nil
}

func shortInFamily(short, base string) bool {
	if !id.IsShortID(short) || !id.IsShortID(base) {
		return false
	}
	return short == base || (len(short) > len(base) && strings.HasPrefix(short, base))
}

func sameBody(a *adoptee, raw []byte) bool {
	if a.model == nil {
		return bytes.Equal(raw, a.raw)
	}
	interior, body, present := frontmatter.Split(raw)
	if !present {
		return false
	}
	if _, err := frontmatter.Parse(interior); err != nil {
		return false
	}
	return bytes.Equal(body, a.body)
}

func takeShort(short string, taken map[string]struct{}) (string, error) {
	if _, ok := taken[short]; !ok {
		taken[short] = struct{}{}
		return short, nil
	}
	ext, err := id.Extend(short, taken)
	if err != nil {
		return "", err
	}
	taken[ext] = struct{}{}
	return ext, nil
}

func (a *adoptee) member() collision.Member {
	created := ""
	if a.model != nil {
		created = a.model.Created
	}
	return collision.Member{Created: created, Basename: a.base, Raw: a.raw, Path: a.path}
}

// claimKeptIDs maps each foreign id to the new id of the KeepBefore winner
// among adoptees that bear it on the filename or in the fence. Short order
// assigns new ids; it does not choose who inherits an old one. A same-scope
// id and an id held elsewhere are not claimed.
func claimKeptIDs(scope string, adoptees []*adoptee, held map[string]struct{}) (map[string]string, map[string][]string) {
	bearers := map[string][]*adoptee{}
	for _, a := range adoptees {
		for _, old := range foreignIDs(scope, a) {
			if _, ok := held[old]; ok {
				continue
			}
			bearers[old] = append(bearers[old], a)
		}
	}
	olds := make([]string, 0, len(bearers))
	for old := range bearers {
		olds = append(olds, old)
	}
	sort.Strings(olds)

	mapped := map[string]string{}
	claimedByPath := map[string][]string{}
	for _, old := range olds {
		group := bearers[old]
		sort.SliceStable(group, func(i, j int) bool {
			return collision.KeepBefore(group[i].member(), group[j].member())
		})
		winner := group[0]
		mapped[old] = winner.newID
		claimedByPath[winner.path] = append(claimedByPath[winner.path], old)
	}
	return mapped, claimedByPath
}

func foreignIDs(scope string, a *adoptee) []string {
	var out []string
	seen := map[string]bool{}
	add := func(old string) {
		if !id.IsFullTicketID(old) || id.ScopeOfFullID(old) == scope || seen[old] {
			return
		}
		seen[old] = true
		out = append(out, old)
	}
	add(a.filenameID)
	if a.model != nil {
		add(a.model.ID)
	}
	return out
}

func (a *adoptee) content(mapped map[string]string) ([]byte, error) {
	if a.model == nil {
		return a.raw, nil
	}
	m := *a.model
	m.Depends = append([]string(nil), a.model.Depends...)
	m.Related = append([]string(nil), a.model.Related...)
	m.Custom = append([]frontmatter.Field(nil), a.model.Custom...)
	idChanged := false
	if id.IsFullTicketID(m.ID) && m.ID != a.newID {
		m.ID = a.newID
		idChanged = true
	}
	dep, depCh := applyIDMap(m.Depends, mapped)
	rel, relCh := applyIDMap(m.Related, mapped)
	m.Depends, m.Related = dep, rel
	prodCh := rewriteProduces(&m, mapped)
	if !idChanged && !depCh && !relCh && !prodCh {
		return a.raw, nil
	}
	if a.design {
		interior, err := design.Serialize(&m)
		if err != nil {
			return nil, err
		}
		return design.Compose(interior, a.body), nil
	}
	interior, err := frontmatter.Serialize(&m)
	if err != nil {
		return nil, err
	}
	return frontmatter.Compose(interior, a.body), nil
}

func planPrefixReferrers(dir string, mapped map[string]string, skip map[string]bool, borne map[string]map[string]struct{}) ([]rewrite.Op, error) {
	if len(mapped) == 0 {
		return nil, nil
	}
	olds := make([]string, 0, len(mapped))
	for old := range mapped {
		olds = append(olds, old)
	}
	sort.Strings(olds)

	tickets, err := scopefile.ListTickets(dir)
	if err != nil {
		return nil, err
	}
	var ops []rewrite.Op
	ticketOps, err := rewriteReferrers(tickets, olds, mapped, skip, borne, false)
	if err != nil {
		return nil, err
	}
	ops = append(ops, ticketOps...)
	paths, err := designPaths(dir)
	if err != nil {
		return nil, err
	}
	designOps, err := rewriteReferrers(paths, olds, mapped, skip, borne, true)
	if err != nil {
		return nil, err
	}
	return append(ops, designOps...), nil
}

func designPaths(dir string) ([]string, error) {
	root := filepath.Join(dir, scopefile.DesignDir)
	entries, err := os.ReadDir(root)
	if err != nil {
		if scopefile.OptionalDirMiss(root) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !scopefile.LooksLikeTicket(e.Name()) {
			continue
		}
		out = append(out, filepath.Join(root, e.Name()))
	}
	sort.Strings(out)
	return out, nil
}

func rewriteReferrers(paths, olds []string, mapped map[string]string, skip map[string]bool, borne map[string]map[string]struct{}, designFile bool) ([]rewrite.Op, error) {
	var ops []rewrite.Op
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", p, err)
		}
		interior, body, present := frontmatter.Split(raw)
		m, parsed := (*frontmatter.Model)(nil), false
		if present {
			parsedM, err := frontmatter.Parse(interior)
			if err == nil {
				m, parsed = parsedM, true
			}
		}
		if !parsed {
			// An adopted file's own filename and parsed fence id are not references.
			if old := firstMention(present, interior, raw, olds, borne[p]); old != "" {
				label, _ := scopefile.TicketIDFromBase(filepath.Base(p))
				if label == "" {
					label = filepath.Base(p)
				}
				return nil, fmt.Errorf("%s", token.Line(token.ParseError, fmt.Sprintf(
					"%s: unparseable frontmatter — cannot repair the id prefix of %s while this reference is unparseable",
					label, old)))
			}
			continue
		}
		if skip[p] {
			continue
		}
		dep, depCh := applyIDMap(m.Depends, mapped)
		rel, relCh := applyIDMap(m.Related, mapped)
		m.Depends, m.Related = dep, rel
		prodCh := rewriteProduces(m, mapped)
		if !depCh && !relCh && !prodCh {
			continue
		}
		var content []byte
		if designFile {
			interiorOut, err := design.Serialize(m)
			if err != nil {
				return nil, err
			}
			content = design.Compose(interiorOut, body)
		} else {
			interiorOut, err := frontmatter.Serialize(m)
			if err != nil {
				return nil, err
			}
			content = frontmatter.Compose(interiorOut, body)
		}
		ops = append(ops, rewrite.Op{OldPath: p, NewPath: p, Content: content})
	}
	return ops, nil
}

func firstMention(present bool, interior, raw []byte, olds []string, own map[string]struct{}) string {
	hay := raw
	if present {
		hay = interior
	}
	for _, old := range olds {
		if _, ok := own[old]; ok {
			continue
		}
		if containsExactFullID(hay, old) {
			return old
		}
	}
	return ""
}

func applyIDMap(list []string, mapped map[string]string) ([]string, bool) {
	if len(list) == 0 {
		return list, false
	}
	out := make([]string, 0, len(list))
	seen := make(map[string]struct{}, len(list))
	changed := false
	for _, e := range list {
		v := e
		if n, ok := mapped[e]; ok {
			v = n
		}
		if _, dup := seen[v]; dup {
			if v != e {
				changed = true
			}
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
		if v != e {
			changed = true
		}
	}
	if !changed {
		return list, false
	}
	return out, true
}

func rewriteProduces(m *frontmatter.Model, mapped map[string]string) bool {
	for i, f := range m.Custom {
		if f.Key != design.KeyProduces {
			continue
		}
		ids, err := frontmatter.StringList(f.Value)
		if err != nil {
			return false
		}
		next, changed := applyIDMap(ids, mapped)
		if !changed {
			return false
		}
		if len(next) == 0 {
			m.RemoveCustom(design.KeyProduces)
			return true
		}
		m.Custom[i].Value = next
		return true
	}
	return false
}

func containsExactFullID(b []byte, full string) bool {
	needle := []byte(full)
	start := 0
	for {
		i := bytes.Index(b[start:], needle)
		if i < 0 {
			return false
		}
		i += start
		beforeOK := i == 0 || !isIdentByte(b[i-1])
		after := i + len(needle)
		afterOK := after == len(b) || strings.IndexByte(id.ShortIDAlphabet, b[after]) < 0
		if beforeOK && afterOK {
			return true
		}
		start = i + 1
	}
}

func isIdentByte(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
}
