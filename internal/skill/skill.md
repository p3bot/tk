---
name: tk
description: >-
  Ticket management with the tk CLI: plain markdown tickets in a scope
  (plans, specs, or feature work). Use when doing feature or ticket work
  in a repo, or the user mentions tk, scope, the board, next, claim, mark,
  depends, tickets, or ticket files — even if they only say
  "pick up the next task", "what's on the board", "mark it done",
  or "create a ticket".
---

# Ticket management with tk

- A scope is a directory of tickets plus its tk.cue
- tk create, get, next, and mark print a cleaned absolute path on stdout
- Call create once only, work with the returned path
- Use the path, do not create ticket files yourself
- A ticket is one markdown file: YAML frontmatter fence, then a single ATX H1, then the body.
- Edit the ticket document body under the H1
- Preserve the YAML frontmatter, never overwrite the document
- Do not hand-edit frontmatter keys and values
- Paths and table data on stdout; tokens and warnings on stderr
- tk writes take a per-scope flock; prefer `tk next --claim` so agents do not collide
- todo → in-progress (`next --claim` or `mark`) on a tk-driven root with an upstream refreshes that root and pushes; never host-push
- Active files live at the scope dir root; terminal status moves them to archive/
- Built-in statuses: draft, backlog, todo, in-progress, review, blocked, done, cancelled
- The todo status is next-eligible
- Manage ticket status through its states; move to done when completed

## Frontmatter

- status → tk mark
- order → tk reorder
- id, created: never invent or "repair"
- status_conflict: not via meta; see Recovery
- summary / scalar customs → tk meta set
- depends, related, tags, links → tk meta add|rm
- related write is one-way on the subject only (no mirror on the target); deps shows both directions
- custom fields: declare per scope under `fields:` in tk.cue (CLI: `tk scope field`); meta allowlists built-ins plus declared names only; optional `required: true` is soft-warn policy only

## Commands

```
tk create <title> [status] [--scope S] [--tag T]...                 # Scaffold ticket (FM + H1); optional tags; print path
tk get <id> [--content] [--scope S]                                 # Resolve id to path; --content prints full file
tk mark <id> <status> [--scope S]                                   # Set status; done/cancelled move to archive/; soft depends_open: / required_missing: as applicable
tk reorder <id> (--before <id> | --after <id> | --first | --last) [--scope S]  # Move board order key
tk next [--scope S] [--no-lens] [--claim]                           # First runnable path (todo); --claim sets in-progress

tk list [status...] [--scope S] [--tag T]... [--all] [--no-lens]    # Board inventory (lens default; --tag hard filter, ignores lens)
tk status [key] [--scope S]                                         # Scope pulse; optional key → bare value
tk meta get <id> [key] [--scope S]                                  # Full header (title/path/lines/words/characters + FM) or one key
tk meta set <id> <key> <value> [--scope S]                          # Set scalar frontmatter key; soft required_missing: if gaps remain
tk meta add <id> <key> <value> [--scope S]                          # Append multi-value frontmatter entry; soft required_missing: if gaps remain
tk meta rm <id> <key> <value> [--scope S]                           # Remove multi-value frontmatter entry; soft required_missing: if gaps remain
tk deps <id> [--scope S] [--transitive] [--tree]                    # Depends/related neighbourhood
tk search <terms> [--scope S]                                       # FTS5 search titles and bodies
tk query <sql>                                                      # Ad-hoc read-only SQL; schema unstable
tk query --schema                                                   # Debug only — do not script against it
tk lens [tags...] [--scope S]                                       # Set machine-local default tag view
tk lens --clear [--scope S]                                         # Clear the lens for a scope
tk tags [--scope S]                                                 # Read-only list of existing tags

tk scope init <dir> (--name <name> | --auto-name) [--code-root <path>] [--auto-commit]  # Create and register scope
tk scope import <dir> [--code-root <path>]                          # Register existing on-disk scope
tk scope rebind <dir> --name <name> [--code-root <path>]            # Rewrite registry paths after move/clone
tk scope forget <name>                                              # Unregister scope (registry, lens, and me only)
tk scope list                                                       # List registered scopes (TSV)
tk scope rename <old> <new>                                         # Rename scope end-to-end
tk scope field list [--scope S]                                     # List custom fields: (name type required values)
tk scope field set <name> --type T [--required] [--values V]... [--scope S]  # Upsert field; full replace from flags (omit --required demotes)
tk scope field unset <name> [--scope S]                             # Remove field declaration only (tickets untouched)
tk sync [--scope S] [--all]                                         # Snapshot/integrate/push auto-commit roots (claim also pushes)
tk doctor [--reindex] [--repair] [--re-space-order] [--all]         # Diagnose integrity; optional repair
tk skill                                                            # Print this agent skill contract
tk skill install [agents...] [--local]                              # Install into agentdex skills roots
tk skill list [--local]                                             # List installed skill copies (default agent set)
tk skill uninstall [agents...] [--local]                            # Remove owned pure skill installs
```

## Identifiers

- Full id is `<scope>-<short>`
- Short ids resolve in the ambient scope
- Full id resolves in any registered scope
- Prefer full ids on depends/related
- Create freezes the filename as `<id>-<slug>.md` from the title at create time
- Do not hand-rename ticket files
- Editing the H1 does not rename the file; leave the frozen slug

## Workflows

Orient: `tk status` | `tk status [key]` (bare value) -> `tk list` -> `tk next` | `tk get <id>`

Core work loop: `tk next --claim` | `tk get <id>` -> edit body under H1 -> `tk mark <id> <status>` -> Durability

Capture: `tk create <title> [--tag T]...` -> fill body -> optional meta/reorder/mark -> Durability

Board: `tk list` -> `tk tags` | `tk reorder` | `tk lens` | `tk search`

Dependencies: `tk deps <id>` -> `tk meta add|rm depends|related` -> `tk next` (mark does not enforce depends; may soft-warn depends_open:)

Manage scopes: `tk scope list` -> `init` | `import` | `rebind` | `forget` | `rename` | `field list|set|unset`

Durability (`tk status mode`):
- tk-driven: mutators self-commit -> `tk sync` (never host push/rebase)
  - Commands that self commit: mark, reorder, next --claim, meta set/add/rm, scope field set|unset, scope rename
  - Create and file edits never commit; requires `tk sync`
  - Call `tk sync` after ticket document changes to commit/push
- repo-driven: host git commit/push (no `tk sync`)
- plain-files: no git step

Integrity: `tk doctor` -> optional `--repair` | `--re-space-order` | `--reindex` | `--all`

Recovery: `tk status` -> `tk doctor` -> fix residue -> `tk sync` if tk-driven
