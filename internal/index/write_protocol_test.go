package index

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

func TestUpsertClobberGuard(t *testing.T) {
	db := openTemp(t)
	p := proj("wc", "ab2c", "todo", "a0")
	p.MtimeNS = 200
	p.Title = "newtitleterm"
	p.Body = []byte("newbodyterm")
	p.Tags = []string{"keep"}
	keep := Edge{FromPath: p.Path, FromID: p.ID, FromScope: "wc", ToID: "wc-de34", ToScope: "wc", Kind: EdgeDepends}
	if err := db.UpsertTicketWithEdges(p, []Edge{keep}); err != nil {
		t.Fatal(err)
	}

	stale := proj("wc", "ab2c", "done", "a0")
	stale.Path = p.Path
	stale.MtimeNS = 100
	stale.Title = "oldtitleterm"
	stale.Body = []byte("oldbodyterm")
	stale.Tags = []string{"stale"}
	staleEdge := Edge{FromPath: p.Path, FromID: p.ID, FromScope: "wc", ToID: "wc-gh56", ToScope: "wc", Kind: EdgeRelated}
	if err := db.UpsertTicketWithEdges(stale, []Edge{staleEdge}); err != nil {
		t.Fatal(err)
	}

	got, err := db.TicketsByID("wc", "wc-ab2c")
	if err != nil || len(got) != 1 {
		t.Fatalf("rows = %+v err=%v", got, err)
	}
	if got[0].Status != "todo" || got[0].Title != "newtitleterm" {
		t.Fatalf("older upsert clobbered the stored row: %+v", got[0])
	}
	if len(got[0].Tags) != 1 || got[0].Tags[0] != "keep" {
		t.Fatalf("older upsert clobbered tags: %+v", got[0].Tags)
	}
	all, _ := db.AllEdges()
	if len(all) != 1 || all[0].Kind != EdgeDepends || all[0].ToID != "wc-de34" {
		t.Fatalf("older upsert clobbered edges: %+v", all)
	}
	if hits, _ := db.Search("wc", "newbodyterm"); len(hits) != 1 {
		t.Fatal("FTS should keep the newer body")
	}
	if hits, _ := db.Search("wc", "oldbodyterm"); len(hits) != 0 {
		t.Fatal("FTS should not gain the stale body")
	}

	eq := *p
	eq.Status = "in-progress"
	eq.MtimeNS = 200
	eq.Title = "eqtitleterm"
	eq.Body = []byte("eqbodyterm")
	if err := db.UpsertTicketWithEdges(&eq, nil); err != nil {
		t.Fatal(err)
	}
	got, _ = db.TicketsByID("wc", "wc-ab2c")
	if got[0].Status != "in-progress" || got[0].Title != "eqtitleterm" {
		t.Fatalf("equal mtime should overwrite: %+v", got[0])
	}
	if all, _ := db.AllEdges(); len(all) != 0 {
		t.Fatalf("equal mtime should replace edges: %+v", all)
	}

	newer := eq
	newer.Status = "done"
	newer.MtimeNS = 300
	newer.Title = "newertitleterm"
	newer.Body = []byte("newerbodyterm")
	if err := db.UpsertTicketWithEdges(&newer, nil); err != nil {
		t.Fatal(err)
	}
	got, _ = db.TicketsByID("wc", "wc-ab2c")
	if got[0].Status != "done" || got[0].MtimeNS != 300 {
		t.Fatalf("newer mtime should overwrite: %+v", got[0])
	}
}

func TestRunScopeWriteRollback(t *testing.T) {
	db := openTemp(t)
	p := proj("wc", "ab2c", "todo", "a0")
	p.MtimeNS = 1
	bad := Edge{FromPath: p.Path, FromID: p.ID, FromScope: "wc", ToID: "wc-de34", ToScope: "wc", Kind: "bogus"}
	err := db.RunScopeWrite(func(w *WriteTx) error {
		if err := w.UpsertTicketWithEdges(p, []Edge{bad}); err != nil {
			return err
		}
		return w.SetLastIndex("wc", 999)
	})
	if err == nil {
		t.Fatal("invalid edge kind should fail the file write")
	}
	if rows, _ := db.ScopeTickets("wc"); len(rows) != 0 {
		t.Fatalf("failed file write persisted a ticket: %+v", rows)
	}
	if ns, _ := db.LastIndex("wc"); ns != 0 {
		t.Fatalf("failed file write advanced last_index to %d", ns)
	}

	err = db.RunScopeWrite(func(w *WriteTx) error {
		if err := w.UpsertTicketWithEdges(p, nil); err != nil {
			return err
		}
		if err := w.SetLastIndex("wc", 999); err != nil {
			return err
		}
		return errors.New("injected")
	})
	if err == nil {
		t.Fatal("expected injected failure")
	}
	if rows, _ := db.ScopeTickets("wc"); len(rows) != 0 {
		t.Fatalf("rolled-back upsert persisted: %+v", rows)
	}
	if ns, _ := db.LastIndex("wc"); ns != 0 {
		t.Fatalf("rolled-back SetLastIndex persisted: %d", ns)
	}
}

func TestScopeWriteHoldsWriterLock(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	other, err := sql.Open("sqlite", indexDSN(db.path, 0))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	other.SetMaxOpenConns(1)
	if err := other.Ping(); err != nil {
		t.Fatal(err)
	}

	err = db.RunScopeWrite(func(*WriteTx) error {
		tx, err := other.Begin()
		if err == nil {
			_ = tx.Rollback()
			t.Fatal("concurrent Begin succeeded while the scope transaction was open")
		}
		if !isBusy(err) {
			t.Fatalf("concurrent Begin error = %v, want SQLITE_BUSY", err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RunScopeWrite: %v", err)
	}
	tx, err := other.Begin()
	if err != nil {
		t.Fatalf("Begin after commit: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit after lock released: %v", err)
	}
}

func isBusy(err error) bool {
	var se *sqlite.Error
	return errors.As(err, &se) && se.Code() == sqlite3.SQLITE_BUSY
}

func TestOpenRefusesNonLocalBeforeHandle(t *testing.T) {
	dir := t.TempDir()
	_, err := openIndex(dir, func(d string) string {
		return nonLocalMsg(d, "NFS")
	})
	if err == nil {
		t.Fatal("expected non-local refuse")
	}
	msg := err.Error()
	for _, want := range []string{dir, "NFS", "XDG_STATE_HOME", "local disk"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refuse error missing %q: %s", want, msg)
		}
	}
	if _, statErr := os.Stat(filepath.Join(dir, DBName)); !os.IsNotExist(statErr) {
		t.Fatalf("openAt must not create the DB on a non-local refuse: %v", statErr)
	}
}

func TestOpenLocalUsesWAL(t *testing.T) {
	db := openTemp(t)
	var mode string
	if err := db.sql.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(mode, "wal") {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
}

func TestLocalTempDirIsNotRefused(t *testing.T) {
	if msg := classifyNonLocal(t.TempDir()); msg != "" {
		t.Fatalf("temp dir classified non-local: %s", msg)
	}
}

func TestNonLocalMsgPointsAtXDGStateHome(t *testing.T) {
	msg := nonLocalMsg("/mnt/nfs", "NFS")
	for _, want := range []string{"/mnt/nfs", "NFS", "XDG_STATE_HOME", "local disk"} {
		if !strings.Contains(msg, want) {
			t.Errorf("nonLocalMsg missing %q: %s", want, msg)
		}
	}
}
