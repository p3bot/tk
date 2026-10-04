package index

// SchemaVersion is the on-disk schema version. A mismatch triggers full rebuild (no migrations).
const SchemaVersion = 8

// schemaSQL is the complete DDL for a fresh index. Path is the physical key so
// duplicate ids are two rows and archive moves are delete+insert. Each FTS
// table is contentless-delete and its rowid mirrors one table (fts mirrors
// tickets, design_fts mirrors designs); rebuild drops both, no VACUUM.
// edges.from_path lets edge delete stay precise under duplicate id. It is not
// a ticket foreign key: a produces edge names a design path. Triggers remove
// a ticket's depends and related edges, and a design's produces edges.
const schemaSQL = `
CREATE TABLE meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE tickets (
    path            TEXT PRIMARY KEY,
    scope           TEXT NOT NULL,
    id              TEXT NOT NULL,
    short_id        TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT '',
    order_key       TEXT NOT NULL DEFAULT '',
    title           TEXT NOT NULL DEFAULT '',
    summary         TEXT NOT NULL DEFAULT '',
    created         TEXT NOT NULL DEFAULT '',
    changed         TEXT NOT NULL DEFAULT '',
    custom          TEXT NOT NULL DEFAULT '{}',
    status_conflict TEXT NOT NULL DEFAULT '[]',
    archived        INTEGER NOT NULL DEFAULT 0 CHECK (archived IN (0, 1)),
    parse_error     INTEGER NOT NULL DEFAULT 0 CHECK (parse_error IN (0, 1)),
    parse_msg       TEXT NOT NULL DEFAULT '',
    schema_error    INTEGER NOT NULL DEFAULT 0 CHECK (schema_error IN (0, 1)),
    mtime_ns        INTEGER NOT NULL DEFAULT 0,
    size            INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_tickets_scope_id ON tickets(scope, id);
CREATE INDEX idx_tickets_scope_short_id ON tickets(scope, short_id);
CREATE INDEX idx_tickets_id ON tickets(id);
CREATE INDEX idx_tickets_scope_archived_status_order ON tickets(scope, archived, status, order_key);
CREATE INDEX idx_tickets_scope_order ON tickets(scope, order_key);

CREATE TABLE ticket_tags (
    path TEXT NOT NULL,
    tag  TEXT NOT NULL CHECK (tag <> ''),
    PRIMARY KEY (path, tag),
    FOREIGN KEY (path) REFERENCES tickets(path) ON DELETE CASCADE
);
CREATE INDEX idx_ticket_tags_tag ON ticket_tags(tag, path);

CREATE TABLE designs (
    path        TEXT PRIMARY KEY,
    scope       TEXT NOT NULL,
    id          TEXT NOT NULL,
    short_id    TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT '',
    title       TEXT NOT NULL DEFAULT '',
    summary     TEXT NOT NULL DEFAULT '',
    created     TEXT NOT NULL DEFAULT '',
    changed     TEXT NOT NULL DEFAULT '',
    parse_error INTEGER NOT NULL DEFAULT 0 CHECK (parse_error IN (0, 1)),
    parse_msg   TEXT NOT NULL DEFAULT '',
    mtime_ns    INTEGER NOT NULL DEFAULT 0,
    size        INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_designs_scope_id ON designs(scope, id);
CREATE INDEX idx_designs_id ON designs(id);

CREATE TABLE edges (
    from_path  TEXT NOT NULL,
    from_id    TEXT NOT NULL,
    from_scope TEXT NOT NULL,
    to_id      TEXT NOT NULL,
    to_scope   TEXT NOT NULL,
    kind       TEXT NOT NULL CHECK (kind IN ('depends', 'related', 'produces')),
    PRIMARY KEY (from_path, kind, to_id)
);
CREATE INDEX idx_edges_from ON edges(from_id);
CREATE INDEX idx_edges_to ON edges(to_id);
CREATE INDEX idx_edges_to_scope ON edges(to_scope);
CREATE INDEX idx_edges_from_scope_kind ON edges(from_scope, kind);

CREATE TRIGGER tickets_delete_edges
AFTER DELETE ON tickets
FOR EACH ROW
BEGIN
    DELETE FROM edges WHERE from_path = OLD.path AND kind IN ('depends', 'related');
END;

CREATE TRIGGER designs_delete_edges
AFTER DELETE ON designs
FOR EACH ROW
BEGIN
    DELETE FROM edges WHERE from_path = OLD.path AND kind = 'produces';
END;

CREATE VIRTUAL TABLE fts USING fts5(title, body, content='', contentless_delete=1, tokenize = 'porter unicode61');

CREATE VIRTUAL TABLE design_fts USING fts5(title, body, content='', contentless_delete=1, tokenize = 'porter unicode61');

CREATE TABLE scope_meta (
    scope      TEXT PRIMARY KEY,
    last_index INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE config_cache (
    scope        TEXT PRIMARY KEY,
    closure_json TEXT NOT NULL DEFAULT '',
    schema_json  TEXT NOT NULL DEFAULT '',
    config_error TEXT NOT NULL DEFAULT ''
);
`

// SchemaText is the human-facing description for tk query --schema (not a stable API).
const SchemaText = `tk index schema (version 8)

NOT A STABLE API: the index is a derived cache, rebuilt on any schema_version
bump, and may reshape between releases with no migration. Do not script against
it — agents use tk depends / list / search / next / get / meta instead.

tickets(path, scope, id, short_id, status, order_key, title, summary, created,
         changed, custom, status_conflict, archived, parse_error, parse_msg,
         schema_error, mtime_ns, size)
    One row per ticket file, keyed by absolute path. changed is the fence
    string, or '' when the key is absent. custom is a JSON object
    (empty {}); status_conflict is a JSON array (empty []);
    archived/parse_error/schema_error are 0/1. There is no tags column.
    Design files are not ticket rows.

ticket_tags(path, tag)
    One row per tag on a ticket file. PRIMARY KEY (path, tag); path references
    tickets(path) ON DELETE CASCADE. Empty-string tags are not stored. Join on
    tickets.path.

designs(path, scope, id, short_id, status, title, summary, created, changed,
         parse_error, parse_msg, mtime_ns, size)
    One row per design file under design/, keyed by absolute path. changed is
    the fence string, or '' when the key is absent. parse_error is 0/1. There
    is no order column, no archive bit, no tag table, and no body column.

edges(from_path, from_id, from_scope, to_id, to_scope, kind)
    One row per depends, related, or produces entry (full ids only). PRIMARY KEY
    (from_path, kind, to_id); CHECK kind IN ('depends', 'related', 'produces').
    from_path is not a foreign key, so a produces edge can name a design path.
    Deleting a ticket removes that ticket's depends and related edges. Deleting
    a design removes its produces edges. to_id is not a foreign key and may dangle.
    Cross-scope edges have from_scope != to_scope.

fts(title, body)
    Ticket search index. Contentless-delete FTS5 (content='', contentless_delete=1),
    not a document store. rowid mirrors tickets.rowid. Title and body are
    tokenized at write time and not stored. Query with MATCH and rank by
    bm25(fts). Ticket search joins only this index.

design_fts(title, body)
    The design search index. Contentless-delete FTS5 with the same tokenizer as fts
    (content='', contentless_delete=1), not a document store. The design search
    rowid mirrors the design row (designs.rowid). Indexing or deleting a design
    does not change the ticket search index.

scope_meta(scope, last_index)
    Per-scope last reconcile timestamp (unix nanoseconds).

config_cache(scope, closure_json, schema_json, config_error)
    Cached tk.cue evaluation. closure_json records the (path, mtime, size) of every
    file in the config import closure; a change to any invalidates the cached schema.
`
