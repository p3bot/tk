// Package index is tk's machine-wide SQLite read model: a derived, rebuildable
// store under XDG state (never inside a scope, never synced). Schema-version
// mismatch or corruption triggers a full drop-and-rebuild, not a migration.
// Authority stays in the files; this package only does SQLite I/O.
// Row agreement with disk is reconcile's job; full Rebuild is a composition-root
// admin op (e.g. doctor --reindex) that empties the cache before reconcile fills it.
package index

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // pure-Go driver, FTS5 compiled in; no cgo
)

// DBName is the fixed index filename under the XDG state dir.
const DBName = "index.db"

// busyTimeoutMS is fixed (not a user knob): fail busy rather than hang an agent.
const busyTimeoutMS = 5000

// DB is an open handle to the machine-wide index.
type DB struct {
	sql  *sql.DB
	path string
}

// Open opens (or creates) the index at <stateDir>/index.db with WAL and busy_timeout,
// ensuring schema currency (rebuild on mismatch/corruption). Non-local directories
// (NFS/CIFS/FUSE on Linux/Darwin) are refused before the SQLite handle is opened.
func Open(stateDir string) (*DB, error) {
	return openIndex(stateDir, classifyNonLocal)
}

func openIndex(stateDir string, classify func(string) string) (*DB, error) {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return nil, fmt.Errorf("create XDG state directory %s: %w", stateDir, err)
	}
	if msg := classify(stateDir); msg != "" {
		return nil, errors.New(msg)
	}
	path := filepath.Join(stateDir, DBName)

	db, err := openAt(path)
	if err != nil {
		return nil, err
	}

	if err := db.ensureSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// openAt applies connection pragmas.
// MaxOpenConns=1: database/sql pooling fights SQLite's locking; one connection
// per process. Multi-process writers are expected and serialised by WAL plus
// busy_timeout.
// foreign_keys(on): SQLite defaults this off; this store wants the engine to
// enforce FKs whenever the schema declares them.
// _txlock=immediate: Begin takes the WAL writer lock before any statement so
// the mtime-guard SELECT cannot observe a snapshot that a later write-through
// would make stale.
func indexDSN(path string, busyMS int) string {
	return fmt.Sprintf("file:%s?_pragma=busy_timeout(%d)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(on)&_txlock=immediate",
		path, busyMS)
}

func openAt(path string) (*DB, error) {
	dsn := indexDSN(path, busyTimeoutMS)
	sqldb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open index %s: %w", path, err)
	}
	sqldb.SetMaxOpenConns(1)
	if err := sqldb.Ping(); err != nil {
		_ = sqldb.Close()
		return nil, fmt.Errorf("open index %s: %w", path, err)
	}
	return &DB{sql: sqldb, path: path}, nil
}

// Close closes the underlying handle.
func (d *DB) Close() error {
	if d == nil || d.sql == nil {
		return nil
	}
	err := d.sql.Close()
	d.sql = nil
	return err
}

// ensureSchema rebuilds when version is missing, mismatched, or unreadable (store is derived).
func (d *DB) ensureSchema() error {
	ver, ok, err := d.readSchemaVersion()
	if err != nil {
		// Corrupt/unreadable meta: rebuild; files remain authoritative.
		return d.rebuildSchema()
	}
	if !ok || ver != SchemaVersion {
		return d.rebuildSchema()
	}
	return nil
}

// readSchemaVersion reads meta.schema_version; ok false means a fresh DB.
func (d *DB) readSchemaVersion() (int, bool, error) {
	var exists string
	err := d.sql.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='meta'`).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	var ver int
	err = d.sql.QueryRow(`SELECT value FROM meta WHERE key='schema_version'`).Scan(&ver)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return ver, true, nil
}

// rebuildSchema drops known objects and recreates the current schema.
func (d *DB) rebuildSchema() error {
	drop := `
DROP TABLE IF EXISTS fts;
DROP TABLE IF EXISTS edges;
DROP TABLE IF EXISTS ticket_tags;
DROP TABLE IF EXISTS tickets;
DROP TABLE IF EXISTS scope_meta;
DROP TABLE IF EXISTS config_cache;
DROP TABLE IF EXISTS meta;
`
	if _, err := d.sql.Exec(drop); err != nil {
		return fmt.Errorf("reset index schema: %w", err)
	}
	if _, err := d.sql.Exec(schemaSQL); err != nil {
		return fmt.Errorf("create index schema: %w", err)
	}
	if _, err := d.sql.Exec(`INSERT INTO meta(key, value) VALUES ('schema_version', ?)`, SchemaVersion); err != nil {
		return fmt.Errorf("stamp schema version: %w", err)
	}
	return nil
}

// Rebuild drops and recreates the schema, discarding every row.
func (d *DB) Rebuild() error {
	return d.rebuildSchema()
}
