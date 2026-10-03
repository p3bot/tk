// Package fmmerge is a pure 3-way frontmatter merge over three raw git stage blobs.
// No I/O: dates, scope name, and occupied short-ids are passed in by the driver.
// Stage presence is explicit: absent (no entry) and present-but-empty are different —
// git records a deletion by omitting the stage, not a zero-byte blob.
package fmmerge

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"github.com/p3bot/tk/internal/collision"
	"github.com/p3bot/tk/internal/frontmatter"
	"github.com/p3bot/tk/internal/id"
	"github.com/p3bot/tk/internal/scopeconfig"
)

// Stage is one git merge stage's blob plus whether that stage exists at all.
// Present false is absent; Present true with empty Data is present-but-empty (malformed).
type Stage struct {
	Present bool
	Data    []byte
}

// Side names one of the two non-base stages. Labels carry no rebase meaning —
// the driver decides which stage is Ours (:2) and Theirs (:3).
type Side int

const (
	// SideOurs is the stage the driver passed as ours (git stage :2).
	SideOurs Side = iota
	// SideTheirs is the stage the driver passed as theirs (git stage :3).
	SideTheirs
)

func (s Side) String() string {
	if s == SideTheirs {
		return "theirs"
	}
	return "ours"
}

// MergeMeta carries the non-blob inputs the pure merge needs.
type MergeMeta struct {
	// OursDate and TheirsDate are git author dates for both-sides scalar LWW.
	OursDate   time.Time
	TheirsDate time.Time
	// Scope is used only to mint the loser's new full id in an add/add.
	Scope string
	// Occupied is short-ids already taken, used only for add/add id.Extend.
	Occupied map[string]struct{}
}

// Outcome is the merge result class the driver branches on.
type Outcome int

const (
	// OutcomeMerged is clean field-merged frontmatter (Result.Model).
	OutcomeMerged Outcome = iota
	// OutcomeStatusConflict is a terminal-involved status dispute.
	OutcomeStatusConflict
	// OutcomeRename is a same-id add/add rename directive.
	OutcomeRename
	// OutcomeDeleteEdit is a delete/edit handoff.
	OutcomeDeleteEdit
)

// Result is the merge outcome. Exactly the fields named by Outcome are populated.
type Result struct {
	Outcome Outcome
	// Model is the clean merged frontmatter for OutcomeMerged and OutcomeStatusConflict.
	Model *frontmatter.Model
	// StatusConflict is the disputed pair for OutcomeStatusConflict.
	StatusConflict []string
	// Rename is the add/add rename directive for OutcomeRename.
	Rename *RenameDirective
	// DeleteEdit is the delete/edit handoff for OutcomeDeleteEdit.
	DeleteEdit *DeleteEditSignal
	// Warnings carries doctor-class notes (e.g. immutable both-sides rewrote identically).
	Warnings []string
}

// RenameDirective is the same-id add/add answer: which side loses and the loser's new short-id.
// No path — the driver holds the collided path and composes keep/new paths.
type RenameDirective struct {
	Loser      Side
	CollidedID string
	NewShortID string
}

// DeleteEditSignal is the delete/edit handoff: deleting side and surviving post-edit status.
type DeleteEditSignal struct {
	Deleted         Side
	SurvivingStatus string
}

// MergeError is a fail-closed data condition the human resolves in-file.
// It is the only error type the pure package returns; operational faults stay with the driver.
type MergeError struct {
	Key    string
	Reason string
}

func (e *MergeError) Error() string {
	if e.Key != "" {
		return fmt.Sprintf("frontmatter merge failed on key %q: %s", e.Key, e.Reason)
	}
	return "frontmatter merge failed: " + e.Reason
}

// MergeFrontmatter 3-way merges frontmatter from base/ours/theirs stage blobs and schema.
// A nil schema is refused (schema-before-data).
func MergeFrontmatter(base, ours, theirs Stage, schema *scopeconfig.Schema, meta MergeMeta) (Result, error) {
	if schema == nil {
		return Result{}, &MergeError{Reason: "no readable schema; refusing to field-merge (schema-before-data)"}
	}
	baseM, err := loadStage(base)
	if err != nil {
		return Result{}, err
	}
	oursM, err := loadStage(ours)
	if err != nil {
		return Result{}, err
	}
	theirsM, err := loadStage(theirs)
	if err != nil {
		return Result{}, err
	}

	switch {
	case !base.Present && ours.Present && theirs.Present:
		return addAdd(oursM, theirsM, ours.Data, theirs.Data, meta)
	case base.Present && ours.Present && theirs.Present:
		return threeWay(baseM, oursM, theirsM, ours.Data, theirs.Data, schema, meta)
	case base.Present && ours.Present && !theirs.Present:
		return deleteEdit(SideTheirs, oursM), nil
	case base.Present && !ours.Present && theirs.Present:
		return deleteEdit(SideOurs, theirsM), nil
	default:
		return Result{}, &MergeError{Reason: "malformed conflict: stage shape is neither add/add, a shared-base 3-way, nor delete/edit"}
	}
}

// loadStage splits and parses stage frontmatter.
// Present zero-byte blob is malformed, not a deletion (git omits the stage for deletions).
func loadStage(s Stage) (*frontmatter.Model, error) {
	if !s.Present {
		return nil, nil
	}
	if len(s.Data) == 0 {
		return nil, &MergeError{Reason: "present-but-empty stage blob (a deletion is an absent stage, not a zero-byte one)"}
	}
	interior, _, present := frontmatter.Split(s.Data)
	if !present {
		return nil, &MergeError{Reason: "stage has no frontmatter fence"}
	}
	m, err := frontmatter.Parse(interior)
	if err != nil {
		return nil, &MergeError{Reason: "stage frontmatter is unparseable: " + err.Error()}
	}
	return m, nil
}

// addAdd handles base-absent both-present: same-id is two tickets, kept as two files via rename.
func addAdd(oursM, theirsM *frontmatter.Model, oursRaw, theirsRaw []byte, meta MergeMeta) (Result, error) {
	if oursM.ID == "" || theirsM.ID == "" || oursM.ID != theirsM.ID {
		return Result{}, &MergeError{Key: frontmatter.KeyID, Reason: "add/add conflict without a shared id — cannot field-merge two distinct tickets"}
	}
	// Both stages share one path/basename; only Created and byte hash decide the loser.
	loser := SideTheirs
	if !collision.KeepBefore(
		collision.Member{Created: oursM.Created, Raw: oursRaw},
		collision.Member{Created: theirsM.Created, Raw: theirsRaw},
	) {
		loser = SideOurs
	}

	full := oursM.ID
	if !id.IsFullTicketID(full) {
		return Result{}, &MergeError{Key: frontmatter.KeyID, Reason: "add/add id is not a legal full ticket id: " + full}
	}
	short := full[strings.IndexByte(full, '-')+1:]
	newShort, err := id.Extend(short, meta.Occupied)
	if err != nil {
		return Result{}, &MergeError{Key: frontmatter.KeyID, Reason: err.Error()}
	}
	return Result{Outcome: OutcomeRename, Rename: &RenameDirective{Loser: loser, CollidedID: full, NewShortID: newShort}}, nil
}

func deleteEdit(deleted Side, survivor *frontmatter.Model) Result {
	return Result{Outcome: OutcomeDeleteEdit, DeleteEdit: &DeleteEditSignal{Deleted: deleted, SurvivingStatus: survivor.Status}}
}

// threeWay field-merges a shared-base conflict per typed rules.
func threeWay(base, ours, theirs *frontmatter.Model, oursRaw, theirsRaw []byte, schema *scopeconfig.Schema, meta MergeMeta) (Result, error) {
	res := &frontmatter.Model{}
	var warnings []string

	idVal, warn, err := mergeImmutable(base.ID, ours.ID, theirs.ID, frontmatter.KeyID)
	if err != nil {
		return Result{}, err
	}
	if warn {
		warnings = append(warnings, fmt.Sprintf("both sides rewrote immutable %q identically off base; kept base %q", frontmatter.KeyID, base.ID))
	}
	res.ID = idVal

	createdVal, warn, err := mergeImmutable(base.Created, ours.Created, theirs.Created, frontmatter.KeyCreated)
	if err != nil {
		return Result{}, err
	}
	if warn {
		warnings = append(warnings, fmt.Sprintf("both sides rewrote immutable %q identically off base; kept base %q", frontmatter.KeyCreated, base.Created))
	}
	res.Created = createdVal

	res.Tags = mergeList(base.Tags, ours.Tags, theirs.Tags)
	res.Depends = mergeList(base.Depends, ours.Depends, theirs.Depends)
	res.Related = mergeList(base.Related, ours.Related, theirs.Related)
	res.Links = mergeList(base.Links, ours.Links, theirs.Links)

	res.Order = mergeScalar(base.Order, ours.Order, theirs.Order, oursRaw, theirsRaw, meta)
	res.Summary = mergeScalar(base.Summary, ours.Summary, theirs.Summary, oursRaw, theirsRaw, meta)

	st, err := mergeStatus(base, ours, theirs, oursRaw, theirsRaw, schema, meta)
	if err != nil {
		return Result{}, err
	}
	res.Status = st.status
	res.StatusConflict = st.statusConflict
	res.Changed = mergeChanged(base, ours, theirs, st, oursRaw, theirsRaw, meta)

	mergeCustom(res, base, ours, theirs, oursRaw, theirsRaw, schema, meta)

	out := Result{Outcome: OutcomeMerged, Model: res, Warnings: warnings}
	if st.dispute {
		out.Outcome = OutcomeStatusConflict
		out.StatusConflict = st.statusConflict
	}
	return out, nil
}

// mergeImmutable merges id/created: never scalar LWW. Base is identity source.
// Both sides differing and disagreeing fails closed; both agreeing off base keeps base with a warning.
func mergeImmutable(base, ours, theirs, key string) (val string, warn bool, err error) {
	if base != "" {
		oursDiff := ours != base
		theirsDiff := theirs != base
		if oursDiff && theirsDiff {
			if ours == theirs {
				return base, true, nil
			}
			return "", false, &MergeError{Key: key, Reason: fmt.Sprintf("both sides changed immutable off base to different values (%q vs %q)", ours, theirs)}
		}
		return base, false, nil
	}
	if ours == theirs {
		return ours, false, nil
	}
	return "", false, &MergeError{Key: key, Reason: fmt.Sprintf("base lacks the key and the two sides disagree (%q vs %q)", ours, theirs)}
}

// mergeList 3-way set-merges: in-base kept only if both sides still carry it;
// not-in-base kept if either side added it. Output: base order, then ours-then-theirs additions.
func mergeList(base, ours, theirs []string) []string {
	baseSet := toSet(base)
	oursSet := toSet(ours)
	theirsSet := toSet(theirs)
	seen := map[string]bool{}
	var out []string
	add := func(e string) {
		if !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	for _, e := range base {
		if oursSet[e] && theirsSet[e] {
			add(e)
		}
	}
	for _, e := range ours {
		if !baseSet[e] {
			add(e)
		}
	}
	for _, e := range theirs {
		if !baseSet[e] {
			add(e)
		}
	}
	return out
}

// mergeScalar merges a plain scalar: one-side-changed, both-equal, or LWW by author date
// (tie-break: greater SHA-256 of whole stage bytes).
func mergeScalar(base, ours, theirs string, oursRaw, theirsRaw []byte, meta MergeMeta) string {
	if ours == theirs {
		return ours
	}
	if ours == base {
		return theirs
	}
	if theirs == base {
		return ours
	}
	if pickOurs(meta, oursRaw, theirsRaw) {
		return ours
	}
	return theirs
}

type statusMerge struct {
	status         string
	statusConflict []string
	dispute        bool
}

// mergeStatus merges status with merge-owned status_conflict.
// Fresh terminal-involved both-sides dispute writes merge-base status plus the pair.
func mergeStatus(base, ours, theirs *frontmatter.Model, oursRaw, theirsRaw []byte, schema *scopeconfig.Schema, meta MergeMeta) (statusMerge, error) {
	bs, os, ts := base.Status, ours.Status, theirs.Status
	if os != bs && ts != bs && os != ts {
		if !schema.StatusKnown(os) || !schema.StatusKnown(ts) {
			return statusMerge{}, &MergeError{Key: frontmatter.KeyStatus, Reason: fmt.Sprintf("both sides changed status to different values and %q or %q is not a known status", os, ts)}
		}
		if schema.StatusTerminal(os) || schema.StatusTerminal(ts) {
			return statusMerge{status: bs, statusConflict: sortedPair(os, ts), dispute: true}, nil
		}
		// Pure non-terminal pair: last-writer-wins.
		sc, err := mergeInherited(base.StatusConflict, ours.StatusConflict, theirs.StatusConflict)
		if err != nil {
			return statusMerge{}, err
		}
		return statusMerge{status: mergeScalar(bs, os, ts, oursRaw, theirsRaw, meta), statusConflict: sc}, nil
	}

	sc, err := mergeInherited(base.StatusConflict, ours.StatusConflict, theirs.StatusConflict)
	if err != nil {
		return statusMerge{}, err
	}
	return statusMerge{status: mergeScalar(bs, os, ts, oursRaw, theirsRaw, meta), statusConflict: sc}, nil
}

// mergeChanged follows the status result. It is not its own last-writer-wins
// scalar. A timestamp-only edit and a terminal dispute keep the base value.
// Both sides choosing the same new status with different values, including an
// omitted key, take the later author date, then the greater stage-byte hash.
// An omitted winner stays omitted. The caller supplies per-file dates, so a
// later commit on another file does not decide the tie.
func mergeChanged(base, ours, theirs *frontmatter.Model, st statusMerge, oursRaw, theirsRaw []byte, meta MergeMeta) string {
	if st.dispute {
		return base.Changed
	}
	bs, os, ts := base.Status, ours.Status, theirs.Status
	oursMoved := os != bs
	theirsMoved := ts != bs
	switch {
	case !oursMoved && !theirsMoved:
		return base.Changed
	case oursMoved && !theirsMoved:
		return ours.Changed
	case !oursMoved && theirsMoved:
		return theirs.Changed
	default:
		if os == ts {
			if ours.Changed == theirs.Changed {
				return ours.Changed
			}
			if pickOurs(meta, oursRaw, theirsRaw) {
				return ours.Changed
			}
			return theirs.Changed
		}
		if st.status == os {
			return ours.Changed
		}
		if st.status == ts {
			return theirs.Changed
		}
		return base.Changed
	}
}

// mergeInherited merges inherited status_conflict as one value on the one-side-changed shape.
// Two differing pairs fail closed (set-merge would yield a three-name key no verb can repair).
func mergeInherited(base, ours, theirs []string) ([]string, error) {
	oursChanged := !equalSeq(ours, base)
	theirsChanged := !equalSeq(theirs, base)
	switch {
	case !oursChanged && !theirsChanged:
		return base, nil
	case oursChanged && !theirsChanged:
		return ours, nil
	case !oursChanged && theirsChanged:
		return theirs, nil
	default:
		if equalSeq(ours, theirs) {
			return ours, nil
		}
		return nil, &MergeError{Key: frontmatter.KeyStatusConflict, Reason: "the two sides carry different inherited status_conflict pairs"}
	}
}

// optAny is one custom/undeclared key's value plus whether it is present on a side.
type optAny struct {
	present bool
	value   any
}

// mergeCustom merges custom and undeclared keys. Schema strings fields set-merge;
// everything else is scalar-ish LWW. Output: base-order keys, then ours-only, then theirs-only.
func mergeCustom(res, base, ours, theirs *frontmatter.Model, oursRaw, theirsRaw []byte, schema *scopeconfig.Schema, meta MergeMeta) {
	baseC, oursC, theirsC := customMap(base), customMap(ours), customMap(theirs)
	for _, k := range orderedCustomKeys(base, ours, theirs) {
		field, declared := schema.Field(k)
		if declared && field.Type == scopeconfig.FieldStrings {
			merged := mergeList(toStrings(baseC[k]), toStrings(oursC[k]), toStrings(theirsC[k]))
			if len(merged) > 0 {
				res.Custom = append(res.Custom, frontmatter.Field{Key: k, Value: toAnyList(merged)})
			}
			continue
		}
		if val, present := mergeScalarOpt(baseC[k], oursC[k], theirsC[k], oursRaw, theirsRaw, meta); present {
			res.Custom = append(res.Custom, frontmatter.Field{Key: k, Value: val})
		}
	}
}

// mergeScalarOpt is mergeScalar over optional values; an absent key can win LWW (drop).
func mergeScalarOpt(base, ours, theirs optAny, oursRaw, theirsRaw []byte, meta MergeMeta) (any, bool) {
	sb, so, st := stateKey(base), stateKey(ours), stateKey(theirs)
	switch {
	case so == st:
		return ours.value, ours.present
	case so == sb:
		return theirs.value, theirs.present
	case st == sb:
		return ours.value, ours.present
	default:
		if pickOurs(meta, oursRaw, theirsRaw) {
			return ours.value, ours.present
		}
		return theirs.value, theirs.present
	}
}

// pickOurs reports whether ours wins a both-sides scalar tie: later author date,
// then greater SHA-256 of whole stage bytes — independent of which physical side is labelled ours.
func pickOurs(meta MergeMeta, oursRaw, theirsRaw []byte) bool {
	if meta.OursDate.After(meta.TheirsDate) {
		return true
	}
	if meta.TheirsDate.After(meta.OursDate) {
		return false
	}
	return bytes.Compare(sha(oursRaw), sha(theirsRaw)) >= 0
}

func customMap(m *frontmatter.Model) map[string]optAny {
	out := make(map[string]optAny, len(m.Custom))
	for _, f := range m.Custom {
		out[f.Key] = optAny{present: true, value: f.Value}
	}
	return out
}

func orderedCustomKeys(base, ours, theirs *frontmatter.Model) []string {
	seen := map[string]bool{}
	var keys []string
	for _, m := range []*frontmatter.Model{base, ours, theirs} {
		for _, f := range m.Custom {
			if !seen[f.Key] {
				seen[f.Key] = true
				keys = append(keys, f.Key)
			}
		}
	}
	return keys
}

func toStrings(o optAny) []string {
	if !o.present {
		return nil
	}
	switch v := o.value.(type) {
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			out = append(out, fmt.Sprint(e))
		}
		return out
	case []string:
		return v
	default:
		return nil
	}
}

func toAnyList(list []string) []any {
	out := make([]any, len(list))
	for i, e := range list {
		out[i] = e
	}
	return out
}

func stateKey(o optAny) string {
	if !o.present {
		return "\x00"
	}
	return "\x01" + fmt.Sprint(o.value)
}

func sortedPair(a, b string) []string {
	if a <= b {
		return []string{a, b}
	}
	return []string{b, a}
}

func equalSeq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func toSet(list []string) map[string]bool {
	out := make(map[string]bool, len(list))
	for _, e := range list {
		out[e] = true
	}
	return out
}

func sha(b []byte) []byte {
	sum := sha256.Sum256(b)
	return sum[:]
}
