---
name: tk
description: >-
  Ticket management with the tk CLI: plain markdown tickets in a scope,
  designs, implementation, or feature work. Use when doing work
  in a repo, or the user mentions tk, scope, the board, next, claim, mark,
  depends, tickets, or ticket files — even if they only say
  "pick up the next task", "what's on the board", "mark it done",
  or "create a ticket".
---

# Ticket management with tk

- A scope is a directory of tickets plus its tk.cue
- tk create, get, next, and rehome print a cleaned absolute path on stdout
- That path is live: call create once, work with it, do not create ticket files yourself
- mark prints one path per unique marked ticket
- A ticket is one markdown file: YAML frontmatter fence, then a single ATX H1, then the body
- Fence (opening `---` through closing `---`): sealed except via tk mutators; parse_error: in-place fence repair (see Recovery)
- H1: live title
- Body under the H1: edit in the file
- Whole-file rewrite of a ticket path is corruption, not authoring
- Paths and table data on stdout; tokens and warnings on stderr
- tk writes take a per-scope flock; prefer `tk next --claim` so agents do not collide
- todo → in-progress (`next --claim` or `mark`) on a tk-driven root with an upstream refreshes that root and pushes; never host-push
- Active files live at the scope dir root; terminal status moves them to archive/
- Built-in statuses: draft, backlog, todo, in-progress, review, blocked, done, cancelled
- The todo status is next-eligible
- Manage ticket status through its states; MUST mark as done when completed

## Frontmatter

- status → tk mark
- order → tk order
- id, created: never invent or "repair"
- changed: the time the current status was entered. Edits and `tk meta` do not move it. Absent on tickets written before the field existed
- status_conflict: not via meta; see Recovery
- summary / scalar customs → tk meta set
- depends, related, tags, links → tk meta add|remove
- related write is one-way on the subject only (no mirror on the target); tk depends shows both directions
- custom fields: declare per scope under `fields:` in tk.cue (CLI: `tk scope field`); meta allowlists built-ins plus declared names only; optional `required: true` is soft-warn policy only
- retire a custom key with `tk scope field unset <name> --strip`: declaration gone and the key gone from all tickets in the scope (including when the declaration was already removed). Without `--strip`, unset is declaration-only and tickets stay untouched. Do not hand-edit fences to drop undeclared keys; `tk repair` does not drop them

## Commands

```
tk create <title> [status] [--scope S] [--tag T]...                 # Scaffold ticket (FM + H1); optional tags; print path
tk get <id> [--content] [--scope S]                                 # Resolve id to path; --content prints full file
tk mark <status> <id> [id...] [--scope S]                           # Set status; done/cancelled move to archive/; soft depends_open: / required_missing: as applicable
tk order <id> (--before <id> | --after <id> | --first | --last) [--scope S]    # Move board order key
tk next [--scope S] [--no-lens] [--claim]                           # First runnable path (todo); --claim sets in-progress
tk rehome <id> <dest-scope> [--scope S]                             # Prefix-rewrite ticket into dest scope; print dest path
tk design create <title> [--scope S] [--edit]                       # Scaffold design/<id>-<slug>.md; print path; no self-commit; --edit opens $EDITOR
tk design list [status...] [--scope S] [--all]                      # Index read; TSV id, status, title, path; default draft and accepted; --all is every parsed design (ls aliases list)
tk design get <id> [--content] [--scope S]                          # Index read; path or file; refuses a short id held by two design files
tk design edit <id> [--scope S]                                     # Open that path in $EDITOR; no stdout; no self-commit
tk design search <terms> [--scope S]                                # Design-only FTS; TSV id, status, title, path
tk design mark <status> <id> [--scope S]                            # draft, accepted, decomposed, superseded; file stays in design/
tk design meta add <id> produces <ticket-id> [--scope S]            # Append one full ticket id
tk design meta remove <id> produces <ticket-id> [--scope S]         # Drop one list entry, including a non-id; last removal drops the key
tk design rehome <id> <dest-scope> [--scope S]                      # Move one design into dest design/; self-commit; no push

tk list [status...] [--scope S] [--tag T]... [--all] [--open] [--no-lens]  # Board inventory (lens default; --open = non-terminal; --tag hard filter, ignores lens). Sorted (order, id); terminal-only status filters reverse that order
tk log [status...] [--all] [--today | --yesterday | --date YYYY-MM-DD | --since YYYY-MM-DD [--until YYYY-MM-DD] | --until YYYY-MM-DD] [--scope S] [--tag T]...  # Tickets that entered their current status on a local day. Default is done, today, every scope. changed is that instant. Create counts. Not git history
tk pulse [key] [--scope S]                                          # Scope pulse; optional key → bare value
tk meta get <id> [key] [--scope S]                                  # Full header (title/path/lines/words/characters + FM) or one key
tk meta set <id> <key> <value> [--scope S]                          # Set scalar frontmatter key; soft required_missing: if gaps remain
tk meta add <id> <key> <value> [--scope S]                          # Append multi-value frontmatter entry; soft required_missing: if gaps remain
tk meta remove <id> <key> <value> [--scope S]                       # Remove multi-value frontmatter entry; soft required_missing: if gaps remain
tk depends [<id>] [--scope S] [--transitive] [--tree] [--no-lens]   # TSV neighbourhood; --tree forest (id optional; lens default)
tk search <terms> [--scope S]                                       # FTS5 search ticket titles and bodies
tk query <sql>                                                      # Ad-hoc read-only SQL; schema unstable
tk query --schema                                                   # Debug only — do not script against it
tk lens [tags...] [--scope S]                                       # Set machine-local default tag view
tk lens --clear [--scope S]                                         # Clear the lens for a scope
tk tags [--scope S]                                                 # Read-only list of existing tags

tk scope init <dir> (--name <name> | --auto-name) [--code-root <path>] [--auto-commit]  # Create and register scope
tk scope import <dir> [--code-root <path>]                          # Register existing on-disk scope
tk scope rebind <dir> --name <name> [--code-root <path>]            # Rewrite registry paths after move/clone
tk scope forget <name>                                              # Unregister scope (registry, lens, and note only)
tk scope list                                                       # List registered scopes (TSV)
tk scope rename <old> <new>                                         # Rename scope end-to-end
tk scope auto-commit [true|false] [--scope S]                       # Read or set git-root-wide autoCommit
tk scope field list [--scope S]                                     # List custom fields: (name type required values)
tk scope field set <name> --type T [--required] [--values V]... [--scope S]  # Upsert field; full replace from flags (omit --required demotes)
tk scope field unset <name> [--strip] [--scope S]                   # Remove field declaration; --strip also drops the key from all tickets
tk sync [--scope S] [--all]                                         # Snapshot/integrate/push auto-commit roots (claim also pushes)
tk doctor                                                           # Diagnose integrity (never mutates files)
tk repair [--re-space-order] [--all]                                # Repair id collisions, id prefix, equal order, archive layout
tk reindex                                                          # Rebuild the machine-wide index from files
```

## Designs

- A design lives at `design/<id>-<slug>.md` in the scope directory. It is not a board item
- A design is its own row in the index. A design does not appear in `tk list`, `tk next`, `tk search`, or `tk log`
- `tk pulse` appends `designs`, `design_draft`, `design_accepted`, `design_decomposed`, and `design_superseded` after `uncommitted`. `designs` counts parseable design rows and ignores the lens. Each status key counts that status. An unknown status increments `designs` only. A broken fence increments none. Ticket `draft` stays the ticket draft count
- `tk design list` and `tk design get` read that index after reconcile
- `tk design search <terms> [--scope S]` searches design titles and bodies (bm25, tie-broken by full id). Empty scope is machine-wide. TSV is id, status, title, path. A parse-error hit has an empty status and a filled path. No lens, no status filter, empty result exits 0. `find` is not an alias. `tk search` still searches tickets only
- `tk depends` on a ticket keeps its three sections and appends `produced by` (design id, status, and title; an empty side prints `(none)`; several design files on one id print `(ambiguous)`). When that id is also one design, a `produces` section is appended. When several designs share it, the ticket report stays and stderr gets `design_id`. `tk depends` on a design id with no ticket prints one section, `produces`, with the ticket neighbour lines, and does not print depends or related. A shared design short id with no ticket refuses and prints no path. `--transitive` does not walk produces. `--tree` does not include designs
- Statuses: draft, accepted, decomposed, superseded. The file stays in `design/`
- `tk design list` defaults to draft and accepted. `--all` includes every parsed design, including a status outside those four. A positional outside those four exits 2. Doctor prints `schema_error: <id> has unknown status "<status>" (<path>)`
- Fence is sealed: status via `tk design mark`; `produces` via `tk design meta add` and `tk design meta remove`. `tk design rehome` rewrites the id prefix and leaves status, created, produces, and changed
- `changed` is the time the design entered its current status. `tk design create` sets `changed` to the same RFC3339 instant as `created`. `tk design mark` updates it only on a status change. A same-status mark does not add the key. `tk design meta`, `tk design rehome`, and `tk scope rename` leave it alone. Ticket stale clocks do not apply to a design. A design written before the field has no key
- `produces` stores full ticket ids, design to tickets only. There is no back-link on the ticket. `tk design meta remove` drops a list entry even when it is not a full ticket id
- The slug is frozen at create. Editing the H1 does not rename the file. Body text under the H1 is a direct file edit
- `tk design edit <id>` resolves like `tk design get`, then opens `$EDITOR` on that path. Success prints nothing. It does not rewrite the fence and does not self-commit. `$EDITOR` may include flags. An unset `$EDITOR` or a non-zero editor exit is non-zero and names `tk design edit`
- `tk design create --edit` prints the absolute path, then opens `$EDITOR` on it. It does not self-commit. An unset `$EDITOR` or a non-zero editor exit is non-zero and leaves the scaffold. Without `--edit`, create does not launch an editor. Agents use the printed path rather than `--edit`
- `tk design get`, `tk design edit`, `tk design mark`, `tk design meta`, and `tk design rehome` refuse a short id held by two design files and print no path. Edit does not launch the editor. Rehome does not write
- A broken fence stays off `tk design list`. List and get print `parse_error: N unparseable`. Get of that file also prints `parse_error: <id>: <message>` and exits 0. `tk design edit` opens that path. Doctor prints `parse_error: <id>: <message> (<path>)`. Mark and meta refuse and do not write
- `tk scope rename` rewrites design filenames and fence ids, and rekeys `produces` entries that use the old scope prefix. Entries that name another scope stay and are reported as `edge_verify`
- tk repair resolves a short id shared with a design. A broken design fence, an unknown design status, a filename that disagrees with the fence id, and a produces entry that does not name that id stay doctor warnings
- `tk design create` does not self-commit. `tk design mark`, `tk design meta add|remove`, and `tk design rehome` self-commit on a tk-driven scope and do not push
- `tk design rehome <id> <dest-scope>` moves that file to `<dest>/design/<new-id>-<slug>.md`. The file stays in `design/`. The slug is unchanged. The short id is kept when the destination does not hold it; tickets and designs both count. A real occupant extends it and is left untouched. A destination design with the same slug and `created`, whose short id is the source short id or an extension of it, is reused instead of minting another id. `produces` is not rekeyed. Same-scope destination exits 2. An unknown destination does not write. A missing or unparseable source fence does not write. A root that contained a written or removed path self-commits when that root is tk-driven. The command does not push. Scopes that share a git root and disagree on autoCommit refuse with `auto_commit_mismatch` and do not write

## Identifiers

- Full id is `<scope>-<short>`
- A filename whose id prefix is not the scope name is invisible to list, pulse, and get. `tk doctor` reports `id_prefix:`; `tk repair` rewrites that prefix in place
- Short ids resolve in the ambient scope
- Full id resolves in any registered scope
- Prefer full ids on depends/related
- Create freezes the filename as `<id>-<slug>.md` from the title at create time
- Do not hand-rename ticket files
- Editing the H1 does not rename the file; leave the frozen slug

## Workflows

Orient: `tk pulse` | `tk pulse [key]` (bare value) -> `tk list` -> `tk next` | `tk get <id>`

Core work loop: `tk next --claim` | `tk get <id>` -> edit body under H1 -> `tk mark <status> <id>` -> Durability

Capture: `tk create <title> [--tag T]...` -> fill body -> optional meta / tk order / mark -> Durability

Board: `tk list` | `tk list --open` | `tk list --all` -> `tk tags` | `tk order` | `tk lens` | `tk search` (`list done` / other terminal-only filters are reverse (order, id); mixed and --all stay forward)

Status entry: `tk log` lists tickets that entered their current status on a local day. The default is status `done`, today, every registered scope. `tk log cancelled --date YYYY-MM-DD` is that status on that day. `changed` is the instant the ticket entered the status it has now. Create counts. This is not git history and not an audit trail. The tag lens is not applied

Dependencies: `tk depends <id>` -> `tk meta add|remove depends|related` -> `tk next` (mark does not enforce depends; may soft-warn depends_open:). A ticket id appends `produced by`. When one design shares the id, it also appends `produces`. A design id with no ticket prints `produces`. `--transitive` does not walk produces. `tk depends --tree` pretty-prints a short-id forest of tickets (omit id for the default board, lens unless `--no-lens`) and does not include designs; not TSV:

    j8dj ─┬─ kv6x ── r345
          └─ h2h7

Manage scopes: `tk scope list` -> `init` | `import` | `rebind` | `forget` | `rename` | `auto-commit` | `field list|set|unset`

Durability (`tk pulse mode`):
- tk-driven: mutators self-commit -> `tk sync` (never host push/rebase)
  - Commands that self commit: mark, order, next --claim, rehome, meta set/add/remove, design mark, design meta add|remove, design rehome, scope field set|unset, scope rename, scope auto-commit (false flip is the last tk-owned commit — allowlisted dirty paths ride it — then host git push if unpushed; later mutators are repo-driven), repair
  - Create, design create, and file edits never commit; requires `tk sync`
  - Call `tk sync` after ticket document changes to commit/push
- repo-driven: host git commit/push (no `tk sync`)
- plain-files: no git step

Integrity: `tk doctor` -> `tk repair` | `tk repair --re-space-order` | `tk repair --all`

Index: `tk reindex` when the index is wrong relative to files

Recovery: `tk pulse` -> `tk doctor` -> `tk repair` -> `tk sync` if tk-driven. parse_error: `tk get` path (exit 0); in-place fence repair; keep the path, id, and created; mutators refuse until parse succeeds; do not cancel+recreate unless a human asks.
