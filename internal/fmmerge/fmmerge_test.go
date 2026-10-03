package fmmerge

import (
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/p3bot/tk/internal/frontmatter"
	"github.com/p3bot/tk/internal/scopeconfig"
	"github.com/p3bot/tk/internal/status"
)

// testSchema has a custom done-category status, active status, strings field, and int field.
func testSchema() *scopeconfig.Schema {
	return &scopeconfig.Schema{
		Name: "wc",
		Statuses: map[string]status.Category{
			"shipped": status.CategoryDone,
			"paused":  status.CategoryActive,
		},
		Fields: map[string]scopeconfig.Field{
			"stakeholders": {Type: scopeconfig.FieldStrings},
			"estimate":     {Type: scopeconfig.FieldInt},
		},
	}
}

func blob(fm, body string) []byte { return []byte("---\n" + fm + "---\n" + body) }

func present(data []byte) Stage { return Stage{Present: true, Data: data} }

func absent() Stage { return Stage{} }

func date(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

// oursNewer makes LWW favour ours.
func oursNewer() MergeMeta {
	return MergeMeta{OursDate: date("2026-06-01T00:00:00Z"), TheirsDate: date("2026-01-01T00:00:00Z")}
}

// equalDates forces the SHA-256 residual path.
func equalDates() MergeMeta {
	return MergeMeta{OursDate: date("2026-01-01T00:00:00Z"), TheirsDate: date("2026-01-01T00:00:00Z")}
}

func mustMerge(t *testing.T, base, ours, theirs Stage, meta MergeMeta) Result {
	t.Helper()
	res, err := MergeFrontmatter(base, ours, theirs, testSchema(), meta)
	if err != nil {
		t.Fatalf("unexpected merge error: %v", err)
	}
	return res
}

func mustMergeErr(t *testing.T, base, ours, theirs Stage, meta MergeMeta) *MergeError {
	t.Helper()
	_, err := MergeFrontmatter(base, ours, theirs, testSchema(), meta)
	var me *MergeError
	if !errors.As(err, &me) {
		t.Fatalf("want *MergeError, got %v", err)
	}
	return me
}

func wantStatus(t *testing.T, r Result, want string) {
	t.Helper()
	if r.Model == nil || r.Model.Status != want {
		got := "<nil model>"
		if r.Model != nil {
			got = r.Model.Status
		}
		t.Errorf("status = %q, want %q", got, want)
	}
}

func TestListAddRemoveClashKeeps(t *testing.T) {
	base := blob("id: wc-ab2c\nstatus: todo\norder: \"a0\"\ntags: [core]\n", "b\n")
	ours := blob("id: wc-ab2c\nstatus: todo\norder: \"a0\"\ntags: [core, x]\n", "b\n")
	theirs := blob("id: wc-ab2c\nstatus: todo\norder: \"a0\"\ntags: [core]\n", "b\n")
	r := mustMerge(t, present(base), present(ours), present(theirs), oursNewer())
	if strings.Join(r.Model.Tags, ",") != "core,x" {
		t.Errorf("added tag must be kept: got %v", r.Model.Tags)
	}
}

func TestDependsPruneVsAdd(t *testing.T) {
	base := blob("id: wc-ab2c\nstatus: todo\norder: \"a0\"\ndepends: [wc-p111]\n", "b\n")
	ours := blob("id: wc-ab2c\nstatus: todo\norder: \"a0\"\ndepends: [wc-n222]\n", "b\n")
	theirs := blob("id: wc-ab2c\nstatus: todo\norder: \"a0\"\ndepends: [wc-p111]\n", "b\n")
	r := mustMerge(t, present(base), present(ours), present(theirs), oursNewer())
	if strings.Join(r.Model.Depends, ",") != "wc-n222" {
		t.Errorf("pruned id must stay pruned and new id kept: got %v", r.Model.Depends)
	}
}

func TestScalarOneSideTakesDone(t *testing.T) {
	base := blob("id: wc-ab2c\nstatus: todo\norder: \"a0\"\n", "b\n")
	ours := blob("id: wc-ab2c\nstatus: done\norder: \"a0\"\n", "b\n")
	theirs := blob("id: wc-ab2c\nstatus: todo\norder: \"a0\"\n", "body edit\n")
	r := mustMerge(t, present(base), present(ours), present(theirs), oursNewer())
	if r.Outcome != OutcomeMerged {
		t.Fatalf("outcome = %v, want merged", r.Outcome)
	}
	wantStatus(t, r, "done")
}

func TestScalarBothNonTerminalLWW(t *testing.T) {
	base := blob("id: wc-ab2c\nstatus: draft\norder: \"a0\"\n", "b\n")
	ours := blob("id: wc-ab2c\nstatus: todo\norder: \"a0\"\n", "b\n")
	theirs := blob("id: wc-ab2c\nstatus: in-progress\norder: \"a0\"\n", "b\n")
	r := mustMerge(t, present(base), present(ours), present(theirs), oursNewer())
	if r.Outcome != OutcomeMerged {
		t.Fatalf("non-terminal both-sides must not dispute, got %v", r.Outcome)
	}
	wantStatus(t, r, "todo")
}

// Equal author dates fall to greater SHA-256 of whole stage bytes, polarity-pinned both ways.
func TestEqualDatesSHAResidualPolarity(t *testing.T) {
	base := blob("id: wc-ab2c\nstatus: todo\norder: \"a0\"\n", "b\n")
	aRaw := blob("id: wc-ab2c\nstatus: todo\norder: \"a1\"\n", "body A\n")
	bRaw := blob("id: wc-ab2c\nstatus: todo\norder: \"a2\"\n", "body B\n")
	aSum := sha256.Sum256(aRaw)
	bSum := sha256.Sum256(bRaw)
	greaterOrder := "a1"
	if string(bSum[:]) > string(aSum[:]) {
		greaterOrder = "a2"
	}

	r1 := mustMerge(t, present(base), present(aRaw), present(bRaw), equalDates())
	if r1.Model.Order != greaterOrder {
		t.Errorf("greater-hash order must win (a ours): got %q want %q", r1.Model.Order, greaterOrder)
	}
	r2 := mustMerge(t, present(base), present(bRaw), present(aRaw), equalDates())
	if r2.Model.Order != greaterOrder {
		t.Errorf("greater-hash order must win (b ours): got %q want %q", r2.Model.Order, greaterOrder)
	}
}

func TestStatusDisputeBothTerminal(t *testing.T) {
	base := blob("id: wc-ab2c\nstatus: todo\norder: \"a0\"\n", "b\n")
	ours := blob("id: wc-ab2c\nstatus: done\norder: \"a0\"\n", "b\n")
	theirs := blob("id: wc-ab2c\nstatus: cancelled\norder: \"a0\"\n", "b\n")
	r := mustMerge(t, present(base), present(ours), present(theirs), oursNewer())
	if r.Outcome != OutcomeStatusConflict {
		t.Fatalf("outcome = %v, want status conflict", r.Outcome)
	}
	wantStatus(t, r, "todo")
	if strings.Join(r.StatusConflict, ",") != "cancelled,done" {
		t.Errorf("status_conflict = %v, want [cancelled done]", r.StatusConflict)
	}
	if strings.Join(r.Model.StatusConflict, ",") != "cancelled,done" {
		t.Errorf("model status_conflict = %v", r.Model.StatusConflict)
	}
}

func TestStatusDisputeTerminalVsNonTerminal(t *testing.T) {
	base := blob("id: wc-ab2c\nstatus: todo\norder: \"a0\"\n", "b\n")
	ours := blob("id: wc-ab2c\nstatus: done\norder: \"a0\"\n", "b\n")
	theirs := blob("id: wc-ab2c\nstatus: in-progress\norder: \"a0\"\n", "b\n")
	r := mustMerge(t, present(base), present(ours), present(theirs), oursNewer())
	if r.Outcome != OutcomeStatusConflict {
		t.Fatalf("terminal vs non-terminal must dispute, not LWW: got %v", r.Outcome)
	}
	if strings.Join(r.StatusConflict, ",") != "done,in-progress" {
		t.Errorf("status_conflict = %v, want [done in-progress]", r.StatusConflict)
	}
}

func TestStatusBothChangedOneUnknownFailsClosed(t *testing.T) {
	base := blob("id: wc-ab2c\nstatus: todo\norder: \"a0\"\n", "b\n")
	ours := blob("id: wc-ab2c\nstatus: done\norder: \"a0\"\n", "b\n")
	theirs := blob("id: wc-ab2c\nstatus: frobnicate\norder: \"a0\"\n", "b\n")
	me := mustMergeErr(t, present(base), present(ours), present(theirs), oursNewer())
	if me.Key != frontmatter.KeyStatus {
		t.Errorf("fail-closed key = %q, want status", me.Key)
	}
}

func TestCustomDoneCategoryDispute(t *testing.T) {
	base := blob("id: wc-ab2c\nstatus: todo\norder: \"a0\"\n", "b\n")
	ours := blob("id: wc-ab2c\nstatus: shipped\norder: \"a0\"\n", "b\n")
	theirs := blob("id: wc-ab2c\nstatus: in-progress\norder: \"a0\"\n", "b\n")
	r := mustMerge(t, present(base), present(ours), present(theirs), oursNewer())
	if r.Outcome != OutcomeStatusConflict {
		t.Fatalf("custom done-category must dispute: got %v", r.Outcome)
	}
	if strings.Join(r.StatusConflict, ",") != "in-progress,shipped" {
		t.Errorf("status_conflict = %v", r.StatusConflict)
	}
}

func TestTypedCustomStringsVsScalar(t *testing.T) {
	base := blob("id: wc-ab2c\nstatus: todo\norder: \"a0\"\nstakeholders: [alice]\nestimate: 3\n", "b\n")
	ours := blob("id: wc-ab2c\nstatus: todo\norder: \"a0\"\nstakeholders: [alice, bob]\nestimate: 5\n", "b\n")
	theirs := blob("id: wc-ab2c\nstatus: todo\norder: \"a0\"\nstakeholders: [alice, carol]\nestimate: 3\n", "b\n")
	r := mustMerge(t, present(base), present(ours), present(theirs), oursNewer())
	custom := map[string]any{}
	for _, f := range r.Model.Custom {
		custom[f.Key] = f.Value
	}
	sh, _ := custom["stakeholders"].([]any)
	var names []string
	for _, e := range sh {
		names = append(names, e.(string))
	}
	if strings.Join(names, ",") != "alice,bob,carol" {
		t.Errorf("strings field must set-merge: got %v", names)
	}
	if got := custom["estimate"]; got != 5 && got != int64(5) && got != uint64(5) {
		t.Errorf("scalar int field one-side-changed must take 5: got %v (%T)", got, got)
	}
}

func TestUndeclaredKeyBothSidesLWWNotDropped(t *testing.T) {
	base := blob("id: wc-ab2c\nstatus: todo\norder: \"a0\"\narea: frontend\n", "b\n")
	ours := blob("id: wc-ab2c\nstatus: todo\norder: \"a0\"\narea: backend\n", "b\n")
	theirs := blob("id: wc-ab2c\nstatus: todo\norder: \"a0\"\narea: platform\n", "b\n")
	r := mustMerge(t, present(base), present(ours), present(theirs), oursNewer())
	var area any
	found := false
	for _, f := range r.Model.Custom {
		if f.Key == "area" {
			area, found = f.Value, true
		}
	}
	if !found {
		t.Fatal("undeclared key must not be dropped")
	}
	if area != "backend" {
		t.Errorf("undeclared LWW must take ours (newer): got %v", area)
	}
}

func TestSameIDAddAddRenameDirective(t *testing.T) {
	ours := blob("id: wc-ab2c\nstatus: todo\norder: \"a0\"\ncreated: 2026-01-01T00:00:00Z\n", "ours\n")
	theirs := blob("id: wc-ab2c\nstatus: todo\norder: \"a0\"\ncreated: 2026-06-01T00:00:00Z\n", "theirs\n")
	meta := MergeMeta{Scope: "wc", Occupied: map[string]struct{}{"ab2c": {}}}
	r, err := MergeFrontmatter(absent(), present(ours), present(theirs), testSchema(), meta)
	if err != nil {
		t.Fatal(err)
	}
	if r.Outcome != OutcomeRename || r.Rename == nil {
		t.Fatalf("add/add must be a rename directive: got %v", r.Outcome)
	}
	if r.Rename.Loser != SideTheirs {
		t.Errorf("newer created loses: got %v", r.Rename.Loser)
	}
	if !strings.HasPrefix(r.Rename.NewShortID, "ab2c") || r.Rename.NewShortID == "ab2c" {
		t.Errorf("loser id must extend ab2c: got %q", r.Rename.NewShortID)
	}
	if r.Rename.CollidedID != "wc-ab2c" {
		t.Errorf("collided id = %q", r.Rename.CollidedID)
	}
}

func TestMissingSchemaErrors(t *testing.T) {
	b := blob("id: wc-ab2c\nstatus: todo\norder: \"a0\"\n", "x\n")
	_, err := MergeFrontmatter(present(b), present(b), present(b), nil, oursNewer())
	var me *MergeError
	if !errors.As(err, &me) {
		t.Fatalf("nil schema must fail closed: got %v", err)
	}
}

func TestImmutableKeepBase(t *testing.T) {
	base := blob("id: wc-ab2c\nstatus: todo\norder: \"a0\"\ncreated: 2026-01-01T00:00:00Z\n", "b\n")
	ours := blob("id: wc-ab2c\nstatus: todo\norder: \"a0\"\ncreated: 2026-01-01T00:00:00Z\n", "b\n")
	theirs := blob("id: wc-ab2c\nstatus: todo\norder: \"a0\"\ncreated: 2030-12-31T00:00:00Z\n", "b\n")
	r := mustMerge(t, present(base), present(ours), present(theirs), oursNewer())
	if r.Model.Created != "2026-01-01T00:00:00Z" {
		t.Errorf("one-side immutable change must keep base: got %q", r.Model.Created)
	}
}

func TestImmutableBothDisagreeFailsClosed(t *testing.T) {
	base := blob("id: wc-ab2c\nstatus: todo\norder: \"a0\"\n", "b\n")
	ours := blob("id: wc-zzzz\nstatus: todo\norder: \"a0\"\n", "b\n")
	theirs := blob("id: wc-yyyy\nstatus: todo\norder: \"a0\"\n", "b\n")
	me := mustMergeErr(t, present(base), present(ours), present(theirs), oursNewer())
	if me.Key != frontmatter.KeyID {
		t.Errorf("both-sides immutable disagreement must name the key: got %q", me.Key)
	}
}

func TestImmutableBothAgreeNonBaseWarns(t *testing.T) {
	base := blob("id: wc-ab2c\nstatus: todo\norder: \"a0\"\ncreated: 2026-01-01T00:00:00Z\n", "b\n")
	ours := blob("id: wc-ab2c\nstatus: todo\norder: \"a0\"\ncreated: 2020-01-01T00:00:00Z\n", "b\n")
	theirs := blob("id: wc-ab2c\nstatus: todo\norder: \"a0\"\ncreated: 2020-01-01T00:00:00Z\n", "b\n")
	r := mustMerge(t, present(base), present(ours), present(theirs), oursNewer())
	if r.Model.Created != "2026-01-01T00:00:00Z" {
		t.Errorf("identical illegal rewrite must still keep base: got %q", r.Model.Created)
	}
	if len(r.Warnings) == 0 {
		t.Error("identical off-base immutable rewrite must emit a doctor-class warning")
	}
}

func TestImmutableBaseMissingKey(t *testing.T) {
	// Base lacks created; sides agree → take it.
	base := blob("id: wc-ab2c\nstatus: todo\norder: \"a0\"\n", "b\n")
	ours := blob("id: wc-ab2c\nstatus: todo\norder: \"a0\"\ncreated: 2026-01-01T00:00:00Z\n", "b\n")
	theirs := blob("id: wc-ab2c\nstatus: todo\norder: \"a0\"\ncreated: 2026-01-01T00:00:00Z\n", "b\n")
	r := mustMerge(t, present(base), present(ours), present(theirs), oursNewer())
	if r.Model.Created != "2026-01-01T00:00:00Z" {
		t.Errorf("base-missing agreed immutable must be taken: got %q", r.Model.Created)
	}
	// Base lacks created; sides disagree → fail closed.
	theirs2 := blob("id: wc-ab2c\nstatus: todo\norder: \"a0\"\ncreated: 2027-01-01T00:00:00Z\n", "b\n")
	me := mustMergeErr(t, present(base), present(ours), present(theirs2), oursNewer())
	if me.Key != frontmatter.KeyCreated {
		t.Errorf("base-missing immutable disagreement must name the key: got %q", me.Key)
	}
}

func TestDeleteEditHandoffEachSide(t *testing.T) {
	base := blob("id: wc-ab2c\nstatus: todo\norder: \"a0\"\n", "b\n")
	edit := blob("id: wc-ab2c\nstatus: done\norder: \"a0\"\n", "edited\n")

	r := mustMerge(t, present(base), present(edit), absent(), oursNewer())
	if r.Outcome != OutcomeDeleteEdit || r.DeleteEdit == nil {
		t.Fatalf("outcome = %v, want delete/edit", r.Outcome)
	}
	if r.DeleteEdit.Deleted != SideTheirs || r.DeleteEdit.SurvivingStatus != "done" {
		t.Errorf("delete/edit = %+v, want theirs deleted, surviving done", r.DeleteEdit)
	}

	r2 := mustMerge(t, present(base), absent(), present(edit), oursNewer())
	if r2.DeleteEdit == nil || r2.DeleteEdit.Deleted != SideOurs {
		t.Errorf("delete/edit = %+v, want ours deleted", r2.DeleteEdit)
	}
}

func TestPresentButEmptyStageIsMalformed(t *testing.T) {
	base := blob("id: wc-ab2c\nstatus: todo\norder: \"a0\"\n", "b\n")
	edit := blob("id: wc-ab2c\nstatus: done\norder: \"a0\"\n", "b\n")
	_, err := MergeFrontmatter(present(base), Stage{Present: true, Data: []byte{}}, present(edit), testSchema(), oursNewer())
	var me *MergeError
	if !errors.As(err, &me) {
		t.Fatalf("present-but-empty stage must be a malformed error, not a deletion: got %v", err)
	}
}

func TestUnparseableStageErrors(t *testing.T) {
	base := blob("id: wc-ab2c\nstatus: todo\norder: \"a0\"\n", "b\n")
	bad := present([]byte("---\n: : :\nnot yaml\n---\nx\n"))
	edit := blob("id: wc-ab2c\nstatus: done\norder: \"a0\"\n", "b\n")
	_, err := MergeFrontmatter(present(base), bad, present(edit), testSchema(), oursNewer())
	var me *MergeError
	if !errors.As(err, &me) {
		t.Fatalf("unparseable stage must fail closed: got %v", err)
	}
}

func TestEqualBothSidesScalarCleanTake(t *testing.T) {
	base := blob("id: wc-ab2c\nstatus: todo\norder: \"a0\"\n", "b\n")
	ours := blob("id: wc-ab2c\nstatus: done\norder: \"a0\"\n", "b\n")
	theirs := blob("id: wc-ab2c\nstatus: done\norder: \"a0\"\n", "b\n")
	r := mustMerge(t, present(base), present(ours), present(theirs), oursNewer())
	if r.Outcome != OutcomeMerged {
		t.Fatalf("identical both-sides status change must not dispute: got %v", r.Outcome)
	}
	wantStatus(t, r, "done")
}

func TestInheritedStatusConflictCarried(t *testing.T) {
	base := blob("id: wc-ab2c\nstatus: todo\norder: \"a0\"\n", "b\n")
	ours := blob("id: wc-ab2c\nstatus: todo\norder: \"a0\"\nstatus_conflict: [cancelled, done]\n", "b\n")
	theirs := blob("id: wc-ab2c\nstatus: todo\norder: \"a0\"\n", "b\n")
	r := mustMerge(t, present(base), present(ours), present(theirs), oursNewer())
	if r.Outcome != OutcomeMerged {
		t.Fatalf("inherited-only must not be a fresh dispute: got %v", r.Outcome)
	}
	if strings.Join(r.Model.StatusConflict, ",") != "cancelled,done" {
		t.Errorf("inherited status_conflict must carry through: got %v", r.Model.StatusConflict)
	}
}

func TestInheritedStatusConflictSupersededByFreshDispute(t *testing.T) {
	base := blob("id: wc-ab2c\nstatus: todo\norder: \"a0\"\n", "b\n")
	ours := blob("id: wc-ab2c\nstatus: done\norder: \"a0\"\nstatus_conflict: [blocked, review]\n", "b\n")
	theirs := blob("id: wc-ab2c\nstatus: cancelled\norder: \"a0\"\n", "b\n")
	r := mustMerge(t, present(base), present(ours), present(theirs), oursNewer())
	if r.Outcome != OutcomeStatusConflict {
		t.Fatalf("fresh dispute expected: got %v", r.Outcome)
	}
	if strings.Join(r.Model.StatusConflict, ",") != "cancelled,done" {
		t.Errorf("fresh pair only, inherited discarded: got %v", r.Model.StatusConflict)
	}
}

func TestTwoDifferingInheritedPairsFailClosed(t *testing.T) {
	base := blob("id: wc-ab2c\nstatus: todo\norder: \"a0\"\n", "b\n")
	ours := blob("id: wc-ab2c\nstatus: todo\norder: \"a0\"\nstatus_conflict: [cancelled, done]\n", "b\n")
	theirs := blob("id: wc-ab2c\nstatus: todo\norder: \"a0\"\nstatus_conflict: [done, in-progress]\n", "b\n")
	me := mustMergeErr(t, present(base), present(ours), present(theirs), oursNewer())
	if me.Key != frontmatter.KeyStatusConflict {
		t.Errorf("two differing inherited pairs must name status_conflict: got %q", me.Key)
	}
}

func TestBaseAbsentDifferentIDAddAddFailsClosed(t *testing.T) {
	ours := blob("id: wc-ab2c\nstatus: todo\norder: \"a0\"\n", "b\n")
	theirs := blob("id: wc-cd3e\nstatus: todo\norder: \"a0\"\n", "b\n")
	me := mustMergeErr(t, absent(), present(ours), present(theirs), MergeMeta{Scope: "wc", Occupied: map[string]struct{}{}})
	if me.Key != frontmatter.KeyID {
		t.Errorf("different-id add/add must fail closed on id: got %q", me.Key)
	}
}

func TestBothSidesDeletedIsMalformed(t *testing.T) {
	base := blob("id: wc-ab2c\nstatus: todo\norder: \"a0\"\n", "b\n")
	_, err := MergeFrontmatter(present(base), absent(), absent(), testSchema(), oursNewer())
	var me *MergeError
	if !errors.As(err, &me) {
		t.Fatalf("base present with both sides absent must be malformed: got %v", err)
	}
}

func fmStatus(status, changed string) string {
	s := "id: wc-ab2c\nstatus: " + status + "\norder: \"a0\"\n"
	if changed != "" {
		s += "changed: " + changed + "\n"
	}
	return s
}

func wantChanged(t *testing.T, r Result, want string) {
	t.Helper()
	got := "<nil model>"
	if r.Model != nil {
		got = r.Model.Changed
	}
	if got != want {
		t.Errorf("changed = %q, want %q", got, want)
	}
}

func TestChangedFollowsStatusOutcome(t *testing.T) {
	const (
		baseC = "2026-01-01T00:00:00Z"
		oursC = "2026-04-01T00:00:00Z"
		thC   = "2026-07-01T00:00:00Z"
	)
	oursOld := MergeMeta{OursDate: date("2026-02-01T00:00:00Z"), TheirsDate: date("2026-09-01T00:00:00Z")}
	tests := []struct {
		name            string
		base, ours, th  string
		meta            MergeMeta
		status, changed string
		dispute         bool
	}{
		{
			name: "one side status takes that changed even when the other timestamp is newer",
			base: fmStatus("todo", baseC), ours: fmStatus("done", oursC), th: fmStatus("todo", thC),
			meta: oursOld, status: "done", changed: oursC,
		},
		{
			name: "the other side status wins the same way",
			base: fmStatus("todo", baseC), ours: fmStatus("todo", oursC), th: fmStatus("done", thC),
			meta: oursNewer(), status: "done", changed: thC,
		},
		{
			name: "one side status with the key omitted clears it",
			base: fmStatus("todo", baseC), ours: fmStatus("done", ""), th: fmStatus("todo", thC),
			meta: oursOld, status: "done", changed: "",
		},
		{
			name: "different non-terminal statuses take the kept side",
			base: fmStatus("draft", baseC), ours: fmStatus("todo", oursC), th: fmStatus("in-progress", thC),
			meta: oursNewer(), status: "todo", changed: oursC,
		},
		{
			name: "different non-terminal omitted winner stays omitted",
			base: fmStatus("draft", baseC), ours: fmStatus("todo", ""), th: fmStatus("in-progress", thC),
			meta: oursNewer(), status: "todo", changed: "",
		},
		{
			name: "same new status and same changed takes that value",
			base: fmStatus("todo", baseC), ours: fmStatus("in-progress", oursC), th: fmStatus("in-progress", oursC),
			meta: oursNewer(), status: "in-progress", changed: oursC,
		},
		{
			name: "same new status both omitted stays omitted",
			base: fmStatus("todo", baseC), ours: fmStatus("in-progress", ""), th: fmStatus("in-progress", ""),
			meta: oursNewer(), status: "in-progress", changed: "",
		},
		{
			name: "same new status differing changed takes the later author date",
			base: fmStatus("todo", baseC), ours: fmStatus("in-progress", oursC), th: fmStatus("in-progress", thC),
			meta: oursNewer(), status: "in-progress", changed: oursC,
		},
		{
			name: "same new status later omitted side stays omitted",
			base: fmStatus("todo", baseC), ours: fmStatus("in-progress", ""), th: fmStatus("in-progress", thC),
			meta: oursNewer(), status: "in-progress", changed: "",
		},
		{
			name: "terminal dispute keeps base changed",
			base: fmStatus("todo", baseC), ours: fmStatus("done", oursC), th: fmStatus("cancelled", thC),
			meta: oursNewer(), status: "todo", changed: baseC, dispute: true,
		},
		{
			name: "timestamp rewrite without a status change keeps base",
			base: fmStatus("todo", baseC), ours: fmStatus("todo", oursC), th: fmStatus("todo", thC),
			meta: oursNewer(), status: "todo", changed: baseC,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := mustMerge(t, present(blob(tc.base, "b\n")), present(blob(tc.ours, "b\n")), present(blob(tc.th, "b\n")), tc.meta)
			if tc.dispute && r.Outcome != OutcomeStatusConflict {
				t.Fatalf("outcome = %v, want status conflict", r.Outcome)
			}
			wantStatus(t, r, tc.status)
			wantChanged(t, r, tc.changed)
		})
	}
}

func TestChangedSameStatusHashTie(t *testing.T) {
	base := blob(fmStatus("todo", "2026-01-01T00:00:00Z"), "b\n")
	a := blob(fmStatus("in-progress", "2026-02-02T00:00:00Z"), "body A\n")
	b := blob(fmStatus("in-progress", "2026-03-03T00:00:00Z"), "body B\n")
	aSum := sha256.Sum256(a)
	bSum := sha256.Sum256(b)
	want := "2026-02-02T00:00:00Z"
	if string(bSum[:]) > string(aSum[:]) {
		want = "2026-03-03T00:00:00Z"
	}
	r1 := mustMerge(t, present(base), present(a), present(b), equalDates())
	wantChanged(t, r1, want)
	r2 := mustMerge(t, present(base), present(b), present(a), equalDates())
	wantChanged(t, r2, want)

	omitted := blob(fmStatus("in-progress", ""), "body A\n")
	presentSide := blob(fmStatus("in-progress", "2026-03-03T00:00:00Z"), "body B\n")
	oSum := sha256.Sum256(omitted)
	pSum := sha256.Sum256(presentSide)
	want = ""
	if string(pSum[:]) > string(oSum[:]) {
		want = "2026-03-03T00:00:00Z"
	}
	r3 := mustMerge(t, present(base), present(omitted), present(presentSide), equalDates())
	wantChanged(t, r3, want)
	r4 := mustMerge(t, present(base), present(presentSide), present(omitted), equalDates())
	wantChanged(t, r4, want)
}
