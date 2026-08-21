package reconcile

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/p3bot/tk/internal/index"
)

func runScopePass(t *testing.T, db *index.DB, now int64, listed map[string]statEntry, existing map[string]index.RowStat, upserts map[string]pending, stat func(string) error) {
	t.Helper()
	if err := db.RunScopeWrite(func(w *index.WriteTx) error {
		return applyScopeWrite(w, "wc", now, listed, existing, upserts, stat)
	}); err != nil {
		t.Fatalf("applyScopeWrite: %v", err)
	}
}

func existStat(exists map[string]bool) func(string) error {
	return func(path string) error {
		if exists[path] {
			return nil
		}
		return os.ErrNotExist
	}
}

func permStat(paths map[string]bool) func(string) error {
	return func(path string) error {
		if paths[path] {
			return os.ErrPermission
		}
		return os.ErrNotExist
	}
}

func seedTicket(t *testing.T, db *index.DB, p *index.Ticket, edges []index.Edge) {
	t.Helper()
	if err := db.UpsertTicketWithEdges(p, edges); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func ticketAt(path, short, status string, mtime int64, archived bool) *index.Ticket {
	return &index.Ticket{
		Path: path, Scope: "wc", ID: "wc-" + short, ShortID: short,
		Status: status, OrderKey: "a0", Title: short, Archived: archived,
		MtimeNS: mtime, Size: 10, Body: []byte("body of " + short),
	}
}

func TestApplyScopeWriteClobberGuardSeesWriteThrough(t *testing.T) {
	_, db := newReconciler(t)
	fp := "/tmp/wc/wc-ab2c-x.md"
	newer := ticketAt(fp, "ab2c", "done", 200, false)
	seedTicket(t, db, newer, nil)

	stale := ticketAt(fp, "ab2c", "todo", 100, false)
	listed := map[string]statEntry{fp: {}}
	existing, err := db.ScopeRows("wc")
	if err != nil {
		t.Fatal(err)
	}
	upserts := map[string]pending{fp: {ticket: stale}}
	runScopePass(t, db, 300, listed, existing, upserts, existStat(map[string]bool{fp: true}))

	rows, _ := db.ScopeTickets("wc")
	if len(rows) != 1 || rows[0].Status != "done" || rows[0].MtimeNS != 200 {
		t.Fatalf("stale parse must not clobber write-through, got %+v", rows)
	}
}

func TestApplyScopeWriteUpsertPathGoneDropsLeftover(t *testing.T) {
	_, db := newReconciler(t)
	fp := "/tmp/wc/wc-ab2c-x.md"
	leftover := ticketAt(fp, "ab2c", "todo", 100, false)
	seedTicket(t, db, leftover, nil)

	parsed := ticketAt(fp, "ab2c", "done", 150, false)
	listed := map[string]statEntry{fp: {}}
	existing, _ := db.ScopeRows("wc")
	upserts := map[string]pending{fp: {ticket: parsed}}
	runScopePass(t, db, 300, listed, existing, upserts, existStat(nil))

	if rows, _ := db.ScopeTickets("wc"); len(rows) != 0 {
		t.Fatalf("gone path must not re-insert and leftover must drop, got %+v", rows)
	}
}

func TestApplyScopeWriteArchiveWriteThroughKeepsNewPath(t *testing.T) {
	_, db := newReconciler(t)
	old := "/tmp/wc/wc-ab2c-x.md"
	arch := "/tmp/wc/archive/wc-ab2c-x.md"
	seedTicket(t, db, ticketAt(old, "ab2c", "todo", 100, false), nil)
	seedTicket(t, db, ticketAt(arch, "ab2c", "done", 200, true), nil)
	if err := db.DeleteByPath(old); err != nil {
		t.Fatal(err)
	}

	stale := ticketAt(old, "ab2c", "todo", 100, false)
	listed := map[string]statEntry{old: {}}
	existing, _ := db.ScopeRows("wc")
	upserts := map[string]pending{old: {ticket: stale}}
	runScopePass(t, db, 300, listed, existing, upserts, existStat(map[string]bool{arch: true}))

	rows, _ := db.ScopeTickets("wc")
	if len(rows) != 1 {
		t.Fatalf("want only the archive row, got %+v", rows)
	}
	if rows[0].Path != arch || !rows[0].Archived || rows[0].Status != "done" {
		t.Fatalf("archive write-through row = %+v", rows[0])
	}
}

func TestApplyScopeWriteUpsertStatOtherErrorSkips(t *testing.T) {
	_, db := newReconciler(t)
	fp := "/tmp/wc/wc-ab2c-x.md"
	leftover := ticketAt(fp, "ab2c", "todo", 100, false)
	seedTicket(t, db, leftover, nil)

	parsed := ticketAt(fp, "ab2c", "done", 150, false)
	listed := map[string]statEntry{fp: {}}
	existing, _ := db.ScopeRows("wc")
	upserts := map[string]pending{fp: {ticket: parsed}}
	runScopePass(t, db, 300, listed, existing, upserts, permStat(map[string]bool{fp: true}))

	rows, _ := db.ScopeTickets("wc")
	if len(rows) != 1 || rows[0].Status != "todo" || rows[0].MtimeNS != 100 {
		t.Fatalf("stat other-error must skip write and leave the leftover row, got %+v", rows)
	}
}

func TestApplyScopeWriteVanishedStatOtherErrorKeepsRow(t *testing.T) {
	_, db := newReconciler(t)
	fp := "/tmp/wc/wc-ab2c-x.md"
	seedTicket(t, db, ticketAt(fp, "ab2c", "todo", 100, false), nil)

	listed := map[string]statEntry{}
	existing, _ := db.ScopeRows("wc")
	runScopePass(t, db, 300, listed, existing, nil, permStat(map[string]bool{fp: true}))

	rows, _ := db.ScopeTickets("wc")
	if len(rows) != 1 || rows[0].Path != fp || rows[0].Status != "todo" {
		t.Fatalf("stat other-error on a listing-missed path must leave the row, got %+v", rows)
	}
}

func TestApplyScopeWriteVanishedPathStillOnDiskKeepsRow(t *testing.T) {
	_, db := newReconciler(t)
	kept := "/tmp/wc/wc-ab2c-x.md"
	extra := "/tmp/wc/wc-de34-extra.md"
	seedTicket(t, db, ticketAt(kept, "ab2c", "todo", 100, false), nil)
	seedTicket(t, db, ticketAt(extra, "de34", "todo", 100, false), nil)

	listed := map[string]statEntry{kept: {}}
	existing, _ := db.ScopeRows("wc")
	runScopePass(t, db, 300, listed, existing, nil, existStat(map[string]bool{kept: true, extra: true}))

	rows, _ := db.ScopeTickets("wc")
	if len(rows) != 2 {
		t.Fatalf("listing-missed path still on disk must keep its row, got %+v", rows)
	}
	found := false
	for _, row := range rows {
		if row.Path == extra && row.ID == "wc-de34" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing write-through row for %s: %+v", extra, rows)
	}
}

func TestReconcileVanishedPathGoneDeletesRow(t *testing.T) {
	r, db := newReconciler(t)
	dir := mkScope(t, "wc")
	fp := filepath.Join(dir, "wc-ab2c-x.md")
	writeFile(t, fp, projFile("wc-ab2c", "todo", "a0", "# X"))
	reconcileOne(t, r, "wc", dir, time.Now().UnixNano())
	if err := os.Remove(fp); err != nil {
		t.Fatal(err)
	}
	reconcileOne(t, r, "wc", dir, time.Now().UnixNano())
	if rows, _ := db.ScopeTickets("wc"); len(rows) != 0 {
		t.Fatalf("gone vanished path should delete the row, got %+v", rows)
	}
}

func TestApplyScopeWriteFailedUpsertDoesNotAdvanceWatermark(t *testing.T) {
	_, db := newReconciler(t)
	if err := db.SetLastIndex("wc", 100); err != nil {
		t.Fatal(err)
	}
	fp := "/tmp/wc/wc-ab2c-x.md"
	p := ticketAt(fp, "ab2c", "todo", 1, false)
	bad := index.Edge{FromPath: fp, FromID: p.ID, FromScope: "wc", ToID: "wc-de34", ToScope: "wc", Kind: "bogus"}
	listed := map[string]statEntry{fp: {}}
	upserts := map[string]pending{fp: {ticket: p, edges: []index.Edge{bad}}}
	err := db.RunScopeWrite(func(w *index.WriteTx) error {
		return applyScopeWrite(w, "wc", 999, listed, nil, upserts, existStat(map[string]bool{fp: true}))
	})
	if err == nil {
		t.Fatal("invalid edge kind should fail the file write")
	}
	if ns, _ := db.LastIndex("wc"); ns != 100 {
		t.Fatalf("failed file write advanced last_index from 100 to %d", ns)
	}
	if rows, _ := db.ScopeTickets("wc"); len(rows) != 0 {
		t.Fatalf("failed file write persisted a ticket: %+v", rows)
	}
}
