# Agent Project Management CLI (tk) — Design

Archived. Historical design notes only. The running code and the embedded skill
contract (`tk skill` / `internal/skill/skill.md`) are the source of truth; this
file may lag the tree.

This document states the landed design after a few iterations and
rework: scopes are the unit, a machine-local registry resolves them, and one
machine-wide index serves reads.

Status legend:
- DECISION: agreed at design time; treat as historical context, not a live override of the code.

## Problem

Feature work is tracked in numbered markdown files (e.g. `01-architecture-review.md`
through `64-console-index-and-drilldown.md`). After the work completes the files
either rot in the repo or get deleted, losing the record. Wanted:

- Clear-text markdown files, edited in place.
- Mark work done so it is known but not read unless needed.
- Order execution; express status (pending, in progress, blocked, waiting on a
  dependency).
- Discoverable by an AI agent without tk inventing ceremony in the tree (no auto-written
  AGENTS.md, no auto-register on clone). Bootstrap is the user's choice: run `tk skill`
  on demand, install the skill when that ships, or their own handoff — see Discovery.
- Usable across a distributed environment: many machines, many repos.

The numbered-filename scheme worked except that the number coupled identity with
order — renumbering on insert/order was the weak point. `beans` was rejected for
forcing a temp-file hand-off (double handling). `beads` was digested in depth: a
Dolt-backed issue tracker whose storage is overkill here, but whose interface
(`ready`, status categories, path-centric CLI, one-op-one-commit, onboarding dump) is worth
borrowing. See "Borrowed from beads" and "Anti-goals".

## Vocabulary

DECISION: two levels.

- scope: one unit of tracked work with a machine-unique name — a repo's project set,
  or a personal/cross-cutting set. A scope is one directory of markdown files plus a
  `tk.cue`. It is the addressing unit and the version-control unit at once
  (store and scope are the same thing; there is no separate "store" container).
- project: one unit of work = one markdown file. "Add feature X to app/website Y."
- task: a checkbox or section inside a project file. The CLI does not model tasks.

A repo is not a "project"; it hosts a scope. "project" is kept for the unit of work
(deliberated at length previously; nothing fit feature work better), on the condition
the container is always "scope" or "repo", never "project".

## Core model

DECISION: a project is one markdown file. Tasks live inside it as checkboxes/sections
the agent edits directly. The CLI never models tasks.

DECISION: the markdown files are always the source of truth. No database is the source
of truth. The CLI edits files in place; there is no import/export or temp-file round
trip (the beans anti-pattern is explicitly avoided). Everything else — the registry
and the index — is derived or machine-local convenience, and can be rebuilt or
re-declared from the files and the user's choices.

### Project ids

DECISION: ids are `<scope>-<short-id>`, e.g. `wc-ab2c`. The scope is the scope name;
the short-id is not a content hash. `tk create` always mints a random 4-character
short-id. Collision repair may lengthen it (see Uniqueness and collisions) up to
`SHORT_ID_MAX = 8`. Ids are typed by a human, so they stay short and unambiguous.

Short-id alphabet (typeability):
- Letters (23): `abcdefghjkmnpqrstuvwxyz` (drops i, l, o).
- Digits (8): `23456789` (drops 0, 1).
- Append / enumeration order for repair (31 chars, fixed):
  `abcdefghjkmnpqrstuvwxyz23456789`.
- First character is always a letter (create and every repaired id, since repair only
  appends). Create positions 2–4 are a 50/50 coin-flip between a letter and a digit;
  repair appends use the full 31-char ordered alphabet above.
- Lowercase, alphanumeric, no symbols. Legal short-id length: 4 through 8 inclusive
  (create = 4; longer only via collision repair).

DECISION (closed id predicates — one contract, shared by create, edges, allowlist,
filename shape, CLI parse, links soft-warn):
- `IsScopeName(s)`: `^[a-z0-9]{1,12}$` (scope names may use `i`/`l`/`o`/`0`/`1`; see
  Scope names).
- `IsShortID(s)`: length 4 through `SHORT_ID_MAX` (8) inclusive; every character in
  `abcdefghjkmnpqrstuvwxyz23456789`; first character is a letter from that alphabet
  (not a digit). Not equivalent to `^[a-z0-9]{4,8}$`.
- `IsFullProjectID(s)`: exactly one `-` separating scope and short-id — scope =
  `IsScopeName` on the substring before the first `-`, short-id = `IsShortID` on the
  remainder (remainder must not contain `-`). There is **no** authoritative standalone
  regex of the form `^[a-z0-9]{1,12}-[a-z0-9]{4,8}$`; that pattern accepts illegal short
  ids (`api-10il`, `wc-0000`, digit-leading short parts, dropped alphabet chars) and must
  not be used as a validator. Implementation: one shared pure helper for all call sites.

Generation and stability:
- Drawn from `crypto/rand` at `tk create`, which checks the ids already present in the
  scope and redraws on any local hit. The draw -> check -> write runs under the scope
  `flock`, so two concurrent local `tk create`s serialise and cannot both reserve the
  same id. Online creation therefore never collides; only offline-concurrent creation
  across machines can. Repair of those duplicates is file-mutating and auto-commit-aware (see
  Uniqueness and collisions below) — never implicit on the read path.
- Not derived from the title or content, so editing a title never changes the id. The
  id is stable by construction; other projects reference it by that value (`depends`,
  `related`).
- Canonical in frontmatter (`id: wc-ab2c`); the filename mirrors it as `<id>-<slug>.md`
  (e.g. `wc-ab2c-network-output-redesign.md`) for human browsing. DECISION: the slug is
  derived once at `tk create` by a pure function `slugify(title)` from the create title
  argument and is frozen for the life of the file. Retitling the work (H1 or body prose)
  does not rename the file and does not change the id. There is no frontmatter `title`
  key and no `tk retitle` verb — the human-facing name lives in the markdown H1; the path
  stays the create-time name. `tk doctor` flags a structural filename/id mismatch
  (filename does not start with the frontmatter `id` plus `-`, or the id/slug shape is
  not a project file shape), not "slug no longer matches the H1". Agents must not rename
  project files to chase a new title; paths handed off by `get`/`create`/`status` remain
  valid across retitles.

  DECISION: `slugify` is create-time wire contract (synced filenames, allowlist,
  same-title add/add), same class as the `order` grammar — not best-effort display text.
  Empty create title is a usage error (exit 2) before slugify runs; after trimming
  surrounding whitespace, title must be non-empty.

  Closed grammar for a legal slug (allowlist and doctor structural checks):
  ```
  slug = token ( "-" token )*
  token = [a-z0-9]+
  ```
  Equivalent: `^[a-z0-9]+(-[a-z0-9]+)*$`. Length 1 through `SLUG_MAX = 48` inclusive
  (UTF-8 bytes of the final ASCII slug string). No leading/trailing `-`, no `--`, no
  uppercase, no characters outside `a-z0-9-`.

  `slugify(title)` (deterministic; pure; no locale, no filesystem probe, no uniqueness
  pass — two same-title creates in one scope get the same slug and different ids):
  1. Interpret title as Unicode; normalise with NFKC.
  2. Map each character: ASCII `A-Z` → `a-z`; ASCII `a-z` and `0-9` kept; every other
     character (whitespace, punctuation, symbols, non-ASCII letters after NFKC that are
     not ASCII alnum) is a separator.
  3. Split on separator runs; join non-empty tokens with a single `-`.
  4. If the result is empty (title was only separators/symbols), use the single token `x`.
  5. If the result's byte length exceeds `SLUG_MAX` (48): truncate to at most 48 bytes.
     Prefer cutting at the last `-` whose index is ≤ 48 so the result stays a valid slug
     grammar (no trailing `-`); if no such `-` exists (one long token), hard-cut to 48
     bytes (ASCII-only at this stage, so byte = char). After truncate, strip any trailing
     `-` if a hard cut left one (should not with the prefer-`-` rule). If truncation
     yields empty, use `x`.
  6. Validate against the closed grammar; the steps above must always produce a legal
     slug — treat a failed validate as an implementation bug, not a user-facing soft
     path.

  Ship as a pure package with table-driven fixtures (empty→usage error before call;
  `"Network Output Redesign"` → `network-output-redesign`; punctuation/emoji → separators;
  CJK-only → `x` or NFKC-ASCII if any; long title → ≤48 and valid grammar; identical
  titles → identical slugs). Do not unique-ify the slug when another file already uses
  it; the id prefix makes the basename unique. Concurrent or sequential same-title
  creates in one scope are **normal** (same slug, different short-ids, distinct basenames
  `<id>-<slug>.md`); flock only serialises the id draw, not title uniqueness.
- DECISION (three human-facing names): frozen filename slug, live H1, and frontmatter
  `summary` may diverge by design. They are three labels, not one identity: slug is a
  create-time path fragment; H1 is the current human title; `summary` is optional
  one-line what/why for lists. No automatic rename, no doctor soft-warn on
  slug≠slugify(H1) or H1≠summary, no requirement that they match. Agents update H1 and
  `summary` in-file when useful; they never rename the file to chase wording.

Uniqueness and collisions:
- Because scope names are machine-unique (see "Scope names"), ids are machine-unique
  too — `wc-ab2c` addresses exactly one project anywhere.
- Create always mints length **4** (repair may extend through `SHORT_ID_MAX` 8). Do not
  bump the mint length without evidence (routine multi-machine `duplicate_id:` noise, or
  scopes routinely in the multi-thousand range offline) — a few hundred projects per
  scope (e.g. ~260) is still well inside 4-char space under the real collision model.
- Distinct strings: 23 × 31³ ≈ 685k (first char letter; positions 2–4 any of the 31
  typeable chars). Create draws positions 2–4 with a 50/50 letter|digit coin-flip then
  uniform within that class, so the draw is **non-uniform**. Collision-effective size
  under that draw is ≈ 1/∑p² ≈ 3×10⁵ (order-of-magnitude ~306k) — use that for birthday
  intuition, not the raw 685k count. Approximate uncoordinated-draw birthday odds at that
  effective N: ~1.6% at ~100 ids, ~0.15% at ~30. Those figures are illustrative only:
  `tk create` redraws on any **local** hit under the scope flock, so online creation never
  keeps a collision. The only uncoordinated path is offline/stale multi-machine create
  (ids present on another peer but not yet on disk here) — a small window for a single-
  user fleet, so a real collision is near-never; when it happens, detect + refuse
  id-taking verbs + repair (not a longer default mint).
- Resolution never rewrites references: the kept side retains the collided id, so no
  edge dangles, and every inbound edge is surfaced for verification (in-scope and
  cross-scope alike — see the repair procedure). An offline-concurrent duplicate with distinct titles produces no git
  conflict: the filename is `<id>-<slug>.md` and the slug derives from each create-time
  title, so two same-id projects with different titles land at different paths and the
  rebase merges them clean.
- Detect vs repair (all scopes): after reconcile, tk runs a cheap index query over the
  scopes just reconciled (duplicate `id` rows; equal `order` keys). Hits ride a terse
  warning on the command (stderr) — they never rewrite files on a read
  command. File-mutating repair is confined to:
  - auto-commit: the integrity step at the end of `tk sync` (automatic after integrate), and
  - every autoCommit mode: `tk doctor --repair` off-sync (identical automatic repairs;
    the only repair path for non-auto-commit scopes, which have no `tk sync` seam).
    Mutating target set is ambient / `TK_SCOPE` or explicit `--all` — see doctor scope
    selection (no ambient + mutating flag without `--all` is usage error). Bare
    `tk doctor` is report-only (see CLI surface).
  Plain-files multi-machine (Dropbox/Syncthing/NFS) is supported on that basis: no sync
  engine, but the same disk-visible duplicates are detected every command and repaired
  when the user or agent runs `tk doctor --repair`. `tk skill` tells agents to run bare
  `tk doctor` when warnings appear (and periodically for plain-files), then `--repair`
  when acting on `duplicate_id:` / `equal_order:` / archive layout tokens. External sync
  may also drop
  vendor conflict-copy names that do not match `<id>-<slug>.md`; those never enter the id
  namespace — reconcile leaves them unindexed (or `parse_error` if they look like projects),
  and `tk doctor` flags non-allowlist residue under the dir for human cleanup (auto-commit
  snapshot never commits them either; see "tk sync").
- Repair procedure (sync integrity and `tk doctor --repair`):
  - Choose the side to rename by a closed total order that never consults references.
    Inbound edges cannot discriminate the sides — both carry the identical id, so a
    `depends`/`related` entry names the id string, not a file — which is also why they
    are surfaced via `edge_verify:` rather than weighed. Pick: rename the newer by
    `created:` (RFC3339 timestamp set at
    `tk create`; see Metadata). If the timestamps are equal — same second, or clock skew
    that lands on the same instant — fall through a residual total-order that does not use
    the id string (both sides share it by definition of the collision): rename the side
    whose basename (`<id>-<slug>.md`) is lexicographically greater; if basenames are equal
    (same-title add/add: one path, two git stages), rename the side whose SHA-256 of the
    raw stage bytes is lexicographically greater. Basename and content digest need no new
    frontmatter, are available for both the dual-file and add/add cases, and always
    total-order the pair, so the repair never stalls or picks non-deterministically.
    Do not use machine-local bias (dirent order, "ours", mtime, pointer identity) — two
    machines repairing the same collision must pick the same loser.
  - Rename by a deterministic short-id extension that stays unique in-scope and
    bit-identical across machines (no `crypto/rand` on the repair path — plain-files
    dual `tk doctor --repair` must mint the same new id). Procedure:
    - Let `prefix` be the loser's current short-id (length N, normally 4; may already
      be longer after a prior repair). Keep the recognisable prefix; only append.
    - Occupied set: every short-id already present in the scope (both collision sides
      and every other project), taken from frontmatter/index before any rename write.
    - Short-id alphabet for appended characters (ordered, closed): the same 31 typeable
      chars as create positions 2–4, in this fixed order:
      `abcdefghjkmnpqrstuvwxyz23456789`.
    - Cap: `SHORT_ID_MAX = 8` (create always mints length 4; repair may grow through 8).
    - For target length L = N+1, N+2, …, `SHORT_ID_MAX`: enumerate every extension string
      of length `L-N` over that alphabet in lexicographic order; candidate short-id is
      `prefix + extension`. Take the first candidate not in the occupied set.
    - Normal case: N=4, first free single-char extension → length 5. If that length is
      fully blocked (all 31 one-char extensions taken — absurd outside tests), grow L.
    - Same procedure when the loser is already length 5+ (second collision or clash with
      an existing extended id): grow again under the same rules.
    - If no free candidate exists at any L ≤ `SHORT_ID_MAX`, hard-fail the repair with a
      doctor-class error naming both paths — do not invent a non-prefix id, do not use
      max+1, do not silently skip. Exhaustion is a compound near-never of near-never;
      the fail-closed path is the budget, not a human renumber scheme.
    - New full id is `<scope>-<new-short-id>`; filename becomes
      `<new-full-id>-<same-frozen-slug>.md`.
  - Never rewrite `depends`/`related` entries — in-scope or cross-scope. The kept side
    retains the original id, so no edge dangles; and because both sides carried that id,
    which side an existing reference meant is unknowable, so an automatic rewrite could
    silently repoint a keeper-reference at the renamed loser. Inbound references are
    surfaced instead (next bullet). File-write durability and auto-commit
    commit-after-all-writes: see multi-file rewrite durability under Scope lifecycle
    (scope rename). Re-entry after crash is idempotent where possible (skip already-
    extended ids).
  - Every inbound edge to the collided id — in-scope and cross-scope alike — carries the
    same hazard: because the kept side retains the original id, a reference that
    meant the kept side stays correct, but one that meant the renamed side now resolves to
    a different project — a silent mispoint, not a visible dangle. So the repairing
    operation reports **every** inbound edge to the collided id — both `depends` and
    `related`, read from the machine-wide `edges` table at repair time — as `edge_verify:`
    lines in its own output ("target was collision-repaired — verify this reference"),
    converting a silent wrong-edge into a surfaced check. The signal is operation-time
    only: nothing is persisted — the `edges` table is derived and rebuilt from files,
    and the files cannot record that a repair happened — so a later bare `tk doctor`
    cannot re-derive this class; read the sync / `--repair` report when a duplicate id
    is repaired. Inbound edges play no part in
    the loser pick (they cannot discriminate the sides — see the pick bullet); verify
    covers `related` too: a mispointed "see also" is still wrong provenance.
    Detection is best-effort (it sees referrers the index knows at repair time), which
    is proportionate to a compound near-never event: a newborn duplicate id that also
    acquired a reference before its first sync.
  - Report the rename and any flagged references. Other external references it
    cannot reach (PR text, agent memory) are a small, surfaced cost — the id is newborn at
    first sync, so its reference blast radius is minimal.
  No counter, no max+1, no renumber-the-loser.

Same-id, same-title sub-case (no clean merge): when the two colliding projects also share
a title they share a slug, so both write the identical path and the rebase produces a git
add/add conflict on one file rather than two clean files. This must not be field-merged —
the two stages are distinct pieces of work that happen to collide on id and path, and
folding their frontmatter together would silently collapse two projects into one and lose
one. The merge handler detects it (an add/add conflict, no base stage, both sides carrying
the same `id`) and resolves it automatically with the same deterministic short-id
extension repair above — keeping both files, renaming one side, staging both so the
rebase continues, and reporting the duplicate — never a field-merge (see "Merge conflict
handling", layer 3).
The id draw is from `crypto/rand`, independent of the title, so this compound stays gated
by the near-never id collision; the guard exists so that when it does occur the outcome is
two preserved projects, not one silently merged.

DECISION: auto-repair budget (closed set). Near-never multi-machine events still need an
explicit policy so the design does not grow beads-scale recovery machinery. Detect-on-
reconcile warnings stay universal and never mutate files. File mutation is only on the
seams already named (`tk sync` integrity for auto-commit; `tk doctor --repair` for every
scope). Bare `tk doctor` never mutates files. Each mutating path is either automatic
(deterministic; bit-identical across machines) or surfaced-for-human — no third tier of
silent heuristics; no mutate-by-default on the diagnose verb.

| Path | Policy | Why this tier |
|---|---|---|
| Post-reconcile duplicate-id / equal-order detect | Warn only (every command) | Read path must not rewrite files; agents learn without waiting for sync |
| Offline-concurrent id collision (dual file or `doctor --repair`) | Automatic deterministic short-id extension (unique in-scope, bit-identical, cap 8); bit-identical loser pick; never an edge rewrite (inbound edges surfaced via `edge_verify:`) | Flat id namespace is load-bearing; no edge dangles (kept side retains the id) and reference intent is side-ambiguous, so a rewrite could silently mispoint; plain-files dual `--repair` is not serialised by git, so machine-local bias ("ours", mtime, dirent) or `crypto/rand` on the new id would diverge |
| Same-id same-title add/add mid-rebase | Automatic same extension repair (never field-merge) | Rebase must progress; field-merge would collapse two projects into one |
| Residual loser tie-break (basename, then SHA-256 of raw stage/file bytes) | Keep; residual only after `created` | Cheap total order with no new frontmatter; required for same-basename add/add and for dual `--repair` agreement. Auto-commit integrate is usually single-writer, but the rule is shared with plain-files repair and must not fork |
| Equal-`order` re-space | Automatic at sync integrity / `doctor --repair` (tied files only) | Reads still sort via `(order, id)`, but hot-path `tk order` into an equal slot has no legal between-key until keys differ |
| Archive layout drift (terminal not under `archive/`, or non-terminal under it) | Soft warn every command / bare doctor; automatic move both ways at sync integrity / `doctor --repair` | Location is a projection of terminal-ness; hot-path `status` already moves on the terminal boundary; repair heals hand-edit and residue drift |
| Pathologically long `order` | Bare doctor soft report; file rewrite only via explicit `tk doctor --re-space-order` | Never implicit on `tk order` or bare doctor; not part of `--repair` (equal-key only) |
| Both-sides status dispute (at least one terminal) | Automatic write of `status_conflict`; human picks; sync refuses `--continue` while present | Any terminal-involved both-sides disagree is semantic (complete vs reopen, done vs cancelled) — LWW would silently erase a terminal; pure non-terminal pairs stay LWW |
| Inbound edges after collision rename (any scope), and cross-scope inbound after scope rename | Operation-time `edge_verify:` report (possible silent mispoint); never auto-rewrite; not persisted and not re-derivable by later doctor | Collision reference intent is side-ambiguous; another scope is another git-root; a derived index cannot durably record that a repair happened; best-effort report is proportionate to compound near-never |
| Registry key ≠ `tk.cue` name (post-share rename drift) | Fail closed on that scope until `forget` + `import`; `name_drift:`; never half-work ambient | Correct full id would fail under pure registry lookup; degraded mode is agent-hostile; registration stays deliberate (no auto-rekey) |
| Non-allowlist residue under dir / vendor conflict-copy names | Flag only; never commit | Human cleanup; outside id namespace |

Out of budget (do not add without a new DECISION): auto-rewriting inbound edges after an
id collision (any scope), auto-picking terminal status, auto-healing registry/tk.cue name drift, renumber-the-loser
or max+1 id schemes, multi-file renumber on hot-path `tk order`, or any repair on pure reads.
Bit-identical loser selection is justified by plain-files dual doctor and shared add/add
code paths — not by a fantasy of two auto-commit machines racing the same integrity step
(they usually serialise through fetch/push). Serialised sync does not license dropping
determinism on the shared repair procedure.

DECISION (plain-files repair concurrency): bit-identical repair makes two machines that
repair the **same quiescent collision set** produce the same renames and file rewrites —
it does **not** make concurrent dual `--repair` (or repair while another machine is still
writing the same files) safe. Scope `flock` is **machine-local** (POSIX flock on
`<dir>/.tk.lock`); it does not coordinate across Syncthing/Dropbox/NFS peers. v1 contract:

- **One repair at a time** against a given on-disk scope tree. Operators serialise: one
  machine runs `tk doctor --repair` (or auto-commit sync integrity), external file sync
  settles, then other machines reconcile / doctor. Concurrent dual repair is
  **unsupported** — not a second deterministic mode.
- No distributed lock, lease, or repair journal in v1 (out of budget with multi-file
  rewrite durability).
- If a concurrent race or crash leaves partial renames / half-rewritten edges: bare
  `tk doctor`, then re-run `--repair` (or the same integrity path). Re-entry stays
  **idempotent where possible** (skip already-extended ids; continue remaining work) —
  same heal path as single-machine crash mid-repair.
- `tk skill` plain-files end-of-turn: when acting on `duplicate_id:` / `equal_order:`,
  prefer a single writer machine for `--repair`; do not race two agents/machines on the
  same collision.

Ergonomics / id resolution (shared by every id-taking verb: `get`, `meta`, `deps`,
`status`, `edit`, `order`, …):
- Full id `<scope>-<short-id>`: registry lookup by scope name, then exact short-id match
  in that scope. No ambient scope required. Short-id length is 4 through `SHORT_ID_MAX`
  (8) per the closed grammar (create-minted or collision-repaired).
- Short form (no scope prefix): exact match of the whole token against a project's
  short-id in the ambient scope (`--scope` / `TK_SCOPE` / longest-prefix code-root).
  Accept any legal short-id length 4–8 — not create-only length 4 — so a repaired id
  such as `ab2c9` resolves the same way as `ab2c`. Match is exact on the short-id
  string, not a prefix search.
- Zero hits on a **well-formed** id (full id passes `IsFullProjectID`, or short form is a
  legal short-id shape in ambient scope) → **unknown id**: generic non-zero exit
  (conventionally `1`); empty path stdout; stderr e.g. `unknown project id: <token>`
  (no closed integrity token required).
- **More than one hit (duplicate id):** after offline-concurrent create, two files can
  share the same full id until repair. Short form and full form use the **same refuse**:
  non-zero exit; **no path on stdout** (do not pick a winner); stderr carries
  `duplicate_id:` naming the id and both paths (or path count + `tk doctor`), matching
  the post-reconcile warning token. Applies to every id-taking verb above — mutators must
  not edit an arbitrary side; `get`/`meta`/`deps` must not hand off an ambiguous path or
  neighbourhood. Resolution returns after `tk doctor --repair` or auto-commit `tk sync`
  integrity renames one side. Board/search verbs that do not take an id (`list`,
  `search`, `query`) still run; they may surface both rows and the same stderr warning.
  `tk next` — with or without `--claim` — skips any candidate whose id is in a collision
  set: a handed-off path must be actionable by the id-taking verbs the work loop feeds
  it to; the `duplicate_id:` warning still rides stderr.
- A token that contains `-` is always parsed as a full id: apply `IsFullProjectID` (scope
  before the first `-`, short-id the remainder). If the token contains `-` but fails
  `IsFullProjectID` → **malformed id**: **exit 2** (usage / bad id shape); empty path
  stdout; stderr e.g. `malformed project id: <token>` — not as a bare short-id, and not
  the same class as unknown. Do not accept bare short-ids that include `-`. A short form
  that is not a legal `IsShortID` shape (wrong length/alphabet/digit-leading) is also
  **malformed** (exit 2), not unknown.

Rationale for random over content-hash: a title hash would collide on same-titled
projects and change the id when the title is edited, breaking id stability; mixing in
a timestamp/salt to avoid that is just a random id with ceremony. Rationale for random
over sequential: avoids collisions under offline-concurrent creation.

### Scope names

DECISION: a scope has one identifier, its name. It is the cross-scope address
(`--scope wc`) and the id prefix (`wc-ab2c`). It is not a directory name — where the
files live is a separate, user-chosen path (see "Storage").

- Value constraint: `^[a-z0-9]{1,12}$` — lowercase alphanumeric, no hyphen (reserved as
  the `<scope>-<short-id>` separator), no symbols/spaces, max 12. Ambiguous chars are
  allowed because the human picks the name deliberately (`wc`, `webctl`, `ilili` are
  all legal). Lowercase is forced; the id crosses case-insensitive (macOS) and
  case-sensitive (Linux) machines.
- DECISION: unique across every scope registered on the machine. Everything is visible
  (see "Visibility"), and the id namespace is flat, so `tk get wc-ab2c` must resolve
  to exactly one scope. A name collision at `tk scope init`/`tk scope import` is a hard error (see
  "Scope lifecycle").
- Typical is 2-4 chars for ergonomics; a readable word up to 12 (`webctl`) is fine.
- Never silently defaulted (the beads mistake: an auto-assigned junk name to rename
  later). Always a conscious choice at init.

Names are fleet-global in effect, enforced per machine. A synced file carrying
`depends: [api-m9k3]` assumes `api` names the same scope on every machine that resolves
it, but uniqueness is checked only against the local registry — nothing stops machine
A's `api` and machine B's `api` being different scopes, which would resolve a
cross-scope gate against the wrong project, silently. Accepted as a stated assumption
for the single-user fleet tk targets: one person registers names consistently across
their machines. A genuine clash or divergence is repaired with `tk scope rename` (see
"Scope lifecycle") — rename is a tooled operation, never a manual multi-file rewrite;
prefer renaming before other machines register. After share, those machines re-register
with forget then import (lens not preserved).

Auto-derivation of a proposed name (what `--auto-name` proposes). Closed procedure
(deterministic; pure over the code-root basename string):

1. Take the code-root basename (last path element only).
2. Split on `[-_. ]+` and camelCase boundaries into tokens; lowercase ASCII letters;
   keep digits in tokens for now.
3. Proposal seed: two or more tokens → concatenation of each token's first character
   (initials: `web-control` → `wc`); one opaque token → its first two characters
   (`webctl` → `we`); zero tokens (empty basename) → empty seed.
4. **Auto-name alphabet** (strict subset of legal scope names): letters
   `abcdefghjkmnpqrstuvwxyz` (drops `i`/`l`/`o`) and digits `23456789` (drops `0`/`1`) —
   same typeability bias as short-ids. Legal `--name` still allows full
   `^[a-z0-9]{1,12}$` including `i`/`l`/`o`/`0`/`1` (`ilili` is fine when chosen
   deliberately).
5. Sanitize the seed: drop every character not in the auto-name alphabet; then ensure
   the first character is a letter from that alphabet (drop leading digits / remaining
   non-letters until a letter leads, or become empty). Truncate to 12 characters max.
6. Accept only if the result matches `^[a-z][a-z0-9]{0,11}$` **and** every character is
   in the auto-name alphabet **and** length ≥ 1. Otherwise **hard error**: name cannot
   be auto-derived from this code-root — pass `--name` (same class of message as a
   derived name that collides with an existing registration). **Never** auto-suffix,
   never invent `x` / `tk` / `aa`, never fall back to the unrestricted scope alphabet.
7. If the accepted name is already registered → hard error naming the clash and
   telling the user to pass `--name` (unchanged — no auto-suffix).

A single opaque token cannot yield true initials, which is why `--name` exists. Examples:
`web-control` → `wc`; `webctl` → `we`; `ill` / `101` / empty → fail closed to `--name`.

## Metadata (frontmatter)

DECISION: per-project metadata lives in the file's own YAML frontmatter — not a
separate index, not a database. The file is the single source of truth for content and
state, eliminating index-vs-files drift.

```markdown
---
id: wc-ab2c                # <scope>-<short-id>, canonical; filename mirrors it
status: in-progress        # draft|backlog|todo|review|in-progress|blocked|done|cancelled (+ CUE customs)
order: "a0"                # integer+fraction rank key (quoted string); execution order
depends: [wc-k3m9]         # full project ids only (same- or cross-scope); never bare short-ids
related: [wc-x4p7, api-m9k3] # full project ids only; soft "see also"; never gates
tags: [network, cdp]
created: 2026-06-20T14:30:00+10:00  # RFC3339, set once at tk create, immutable
links: [PR#142, issue#88, branch:network-redesign] # external artefacts only, never project ids
summary: One-line what/why.
# Optional keys declared in tk.cue fields: (see Configuration) — e.g.
# estimate: 3
# area: frontend
# stakeholders: [platform, design]
---

# Network output redesign

Tasks as checkboxes below...
```

Built-in frontmatter keys (closed, immutable set): `id`, `status`, `order`, `depends`,
`related`, `tags`, `created`, `links`, `summary`, and the transient merge-only key
`status_conflict` (see "Merge conflict handling"). A scope may declare additional keys
via `tk.cue` `fields` (see "Configuration"); those sit beside the built-ins in the same
YAML map. There is no nested `fields:` key in the file — declaration is in CUE, presence
is flat in frontmatter — so a human reading the markdown sees one metadata block.

DECISION: `status_conflict` is a transient built-in, not normal project metadata. It is
written only by the auto-commit frontmatter merge when both sides change `status` from
the merge base to two different values and **at least one** of those values is terminal
(see Merge conflict handling); it is never set by `tk create`, `tk status`, or ordinary
authoring. Shape: a YAML sequence of exactly two distinct known status names (the two
post-edit values — not necessarily both terminal), e.g. `status_conflict: [cancelled, done]`
or `status_conflict: [done, in-progress]`. Order of the two names is deterministic:
**lexicographic ascending** by the status name string (byte-wise), so both machines write
the same sequence for the same pair (`[cancelled, done]` not `[done, cancelled]` when
those are the two values). While present, the project is mid-status
dispute involving a terminal: `status` holds the merge-base (last-agreed) value,
`tk get`/`tk meta`/`tk doctor` surface the choice, and `tk sync` refuses to continue the
rebase until the key is gone. Resolution is in-file — set `status` to either listed value,
or another known status (including a non-terminal reopen), and remove `status_conflict` —
the same class of direct edit as resolving a body conflict. The file remains the source
of truth, so an index rebuild still sees the dispute. A `status_conflict` present when
the git-root is not mid-rebase is doctor-hard residue (stale hand-edit or interrupted
cleanup); clear it. Custom `fields` must not shadow the name.

DECISION: `created` is an RFC3339 timestamp written once at `tk create` and never updated.
It is provenance for humans and the primary loser-pick key for id-collision repair
(see "Project ids"). Local wall-clock is
fine — the single-user fleet accepts clock skew as a near-never residual, closed when two
timestamps compare equal by lexicographic basename then SHA-256 of the raw file/stage
bytes (not by the id string: both sides share it). `tk doctor` flags a missing or
non-RFC3339 `created` (date-only values included) so a hand-edited file cannot silently
weaken the repair order.

DECISION: `order` is the single sequencing key; there is no separate `priority`.
`tk next` and default `tk list` sort by `(order, id)`. Urgency is expressed by moving
a project earlier with `tk order`, not by a second sort axis. Banded triage, if ever
wanted, returns as a tag or a CUE custom field, not a built-in.

DECISION: `order` is an integer-plus-fraction rank key (fractional indexing), not a dense
integer and not a free-form letter string. The value is one string under a closed grammar;
board order is ordinary byte-wise string compare (memcmp / Go string `<` / SQLite
`ORDER BY`). No custom collation and no multi-segment parse at compare time — the
encoding is chosen so string order is rank order. Keys are durable source-of-truth data
(synced markdown, git history) — the on-disk format is a wire contract, not an internal
cache. A Go package implements generation (prefer a faithful port of the Rocicorp /
Figma fractional-indexing construction); it must emit this format. Swapping libraries
must not change the alphabet, integer grammar, sort order, or the meaning of existing
keys. Changing the format is a conscious, versioned migration of every `order` value,
never a quiet dependency bump.

Rationale for integer+fraction over pure `a`–`z`: a pure lowercase alphabet with free-form
non-empty keys has a least element `"a"`. `keyBetween(null, "a")` is impossible, so
`tk order --first` fails after a handful of uses. Prefix pairs such as `"a"` / `"aa"`
also leave an empty open interval. Integer heads (`Z`…`A` negative, `a`…`z` non-negative)
give practical unbounded open-end insert under the same byte-wise sort; the theoretical
floor (`SMALLEST_INTEGER`) is not an everyday failure mode.

Wire format (frozen for v1; treat as append-only protocol):

Alphabet (62 chars, ascending = rank order = ASCII byte order):
```
0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz
```
Only these characters are legal. Digit index 0 is `0` (min / pad digit). No spaces,
hyphens, or other symbols.

Key shape:
```
order_key = integer_part + fractional_part
integer_part   = head + digits   # fixed width from head
fractional_part = digit*         # may be empty; must not end with 0
```

Integer part — head encodes the total length of the integer part (head + its digits):

| Head | Integer length | Form | Role |
|---|---|---|---|
| `a` | 2 | `a` + 1 digit | non-negative, width 1 |
| `b` | 3 | `b` + 2 digits | non-negative, width 2 |
| … | … | … | … |
| `z` | 27 | `z` + 26 digits | non-negative, width 26 |
| `Z` | 2 | `Z` + 1 digit | negative, width 1 |
| `Y` | 3 | `Y` + 2 digits | negative, width 2 |
| … | … | … | … |
| `A` | 27 | `A` + 26 digits | negative, width 26 |

Length formula: head in `a`–`z` → `(head - 'a') + 2`; head in `A`–`Z` → `('Z' - head) + 2`;
any other head is invalid. The integer part is exactly `key[0:length]`; the remainder is
the fractional part. Because `'Z' < 'a'` in ASCII, negative heads sort before
`INTEGER_ZERO` without a custom comparator.

Special integer constants:
- `INTEGER_ZERO` = `a0` — empty board / `keyBetween(null, null)`; first `tk create` writes
  `order: "a0"`.
- `SMALLEST_INTEGER` = `A` + 26 × `0` — least legal integer part; cannot decrement further.

Fractional part: may be empty (`a0`, `Z9`, `a1` are valid). Must not end with `0` (no
trailing min digit). That forbids pad-style tails and avoids prefix-style empty intervals
between keys.

Validity (closed grammar): non-empty; every character in the base-62 alphabet; first
character `A`–`Z` or `a`–`z`; `len(key) >= integer_length(head)`; fractional part does not
end with `0`. `tk doctor` and every mutating write that sets `order` reject invalid keys
(hard). Missing, non-string, or empty `order` is also hard. Hand-edited garbage must not
enter the rank space silently.

`keyBetween(left, right)` (each bound a valid key or null):
- Precondition when both non-null: `left < right` (byte-wise). `left == right` is
  undefined — equal keys have no strict between; see equal-key repair (never invent a
  between on the hot path).
- null,null → `a0`.
- left,null → a key strictly after left (append path; `tk create` and `tk order --last`
  use this). DECISION (order rank domain — intentional UX cost, not oversight): `last` is
  the maximum valid `order` key among **all projects in the target scope** that have a
  valid `order` (every status; dir root and `archive/`; not filtered to the default list
  or non-terminal set). Projects in `parse_error` quarantine or otherwise lacking a valid
  `order` are omitted from the max. Empty scope / no valid keys → treat as no left bound
  (`keyBetween(null, null)` for create → `a0`). Rationale: `order` is one global rank
  space per scope, not a board filter; list/next filters change visibility, not the
  append domain. Cost: `tk create` / `--last` place after historical done work until
  `tk order` — that is accepted, not a bug to "fix" by scoping append to active-only
  or splitting rank spaces. "End of the board" means end of that rank space; place on
  the active queue with `tk order` after promote when needed. Prefer
  `incrementInteger(integer_part(left))` when that yields a key; else grow the fraction
  via `midpoint` under left's integer.
- null,right → a key strictly before right (`tk order --first` / before-head order).
  Symmetrically, `first` / right open-bound neighbour is the **minimum** valid `order`
  among all projects in the target scope (same set as `last`: every status, root and
  `archive/`, valid keys only). Prefer bare `integer_part(right)` when right has a
  non-empty fraction and that integer is strictly less than right; else
  `decrementInteger(integer_part(right))`; if already at `SMALLEST_INTEGER`, densify under
  that integer with `midpoint` against right's fraction. Exhaustion of the theoretical
  floor is an error (doctor/migration), not a multi-file renumber on the hot path.
- left,right with left < right → same-integer `midpoint` on the fractions when integer
  parts match; otherwise try `incrementInteger(left's integer)` when it sorts before
  right, else `left's integer + midpoint(left's fraction, null)`.
- Integer inc/dec use base-62 digit arithmetic on the integer part only; overflow widens
  the positive head (`a9` → `b00`, …); underflow widens the negative head (`Z0` →
  wider `Y…`, …). Return failure only past max positive width or `SMALLEST_INTEGER`.
- Midpoint (fractional strings only): prefer short results; lengthen when neighbours are
  adjacent in the digit space; never end with `0`. Exact midpoint among legal shorts may
  vary only if the full key remains valid and strictly between the bounds — prefer one
  shared algorithm (Rocicorp port) so tests and fleets match.

DECISION: `keyBetween` (and the grammar) ship as a pure package with table-driven unit
tests before any CLI `tk order` wiring. Do not only test mid-board inserts. Minimum fixtures:

| Case | Expect |
|---|---|
| `keyBetween(null, null)` | `a0` (`INTEGER_ZERO`) |
| Repeated append (`left, null`) from empty board | strictly increasing keys; all valid |
| Repeated prepend (`null, right`) from `a0` / mid board | strictly decreasing keys; all valid; many steps without error |
| Same-integer adjacent densify (no single-digit fraction midpoint) | length growth; result strictly between; never ends with `0` |
| Open interval across integer boundary | valid key with left < k < right (byte-wise) |
| Equal keys (`left == right`) | no between on hot path — error / undefined, never invent |
| Invalid keys (trailing `0` fraction, bad head, empty, non-alphabet) | reject on validate / order-setting path |
| Integer overflow widen (`a9` → `b00` class) | append still succeeds after widen |
| Integer underflow widen (`Z0` → wider negative head) | prepend still succeeds after widen |
| Theoretical floor at `SMALLEST_INTEGER` densify then exhaust | densify while fraction room remains; hard error at true exhaustion — never multi-file renumber |
| Theoretical max positive integer width exhaust | hard error on further append past max width |
| Byte-wise sort == rank order | random valid key pairs: string `<` matches intended order |
| Round-trip validate | every key emitted by `keyBetween` passes the closed grammar |

Prefer one shared algorithm (Rocicorp-style port) so fixture strings stay stable across
implementations and agent tests.

Sort interoperability: two implementations are compatible if they accept the same
grammar, emit only valid keys, and preserve byte-wise order as rank order. Identical
midpoint strings are not required for sort correctness; identical generation is required
for boring fixtures and agent predictability, hence the shared-port preference.

Evolution: existing keys keep their sort meaning forever under this grammar. A future
format change requires a designed re-space/migration of all `order` values in a scope
(or a new key with a version discriminator) — not a silent alphabet or head-rule edit.

Inserting or moving computes a new key strictly between the two neighbours
(`keyBetween(left, right)`), so `tk order` writes only the subject's file — no
neighbour is renumbered. `tk create` always appends with `keyBetween(last, null)` where
`last` is the scope-wide max valid `order` (see `keyBetween` left,null DECISION) — no
create-time order flags (`--first` / `--before` / … live only on `tk order`).
`tk order --last` / `--first` use the same global max / min. A new scaffold is not yet
queue-committed (`draft` by default); place it on the active board with `tk order`
after promote when order matters.

Invariants (load-bearing for merge avoidance):
- Single-file write: `tk order` and `tk create` never rewrite a neighbour's `order`. There is
  no multi-file renumber on the hot path.
- Open ends and between-keys: for any two valid keys with left < right, and for null open
  bounds short of the theoretical integer floor/ceiling, `keyBetween` returns a valid key
  strictly between them (integer step and/or fraction length growth). An implementation
  that renumbers a band when "no space" remains is non-conforming — that would reintroduce
  multi-file conflicts on `tk order`, undoing layer 1 of "Merge conflict handling". The only
  hot-path hard failures are invalid existing keys or true exhaustion of
  `SMALLEST_INTEGER` / max positive integer width (not reached in normal use).
- Equal keys are the only ordinary "no value strictly between" case: two machines offline can
  compute the same key for the same slot. For reads the tie breaks deterministically by
  id (`(order, id)` sort). Generation still has no string strictly between two equal
  keys, so a later `tk order` into that slot would have nothing legal to write. Detect vs
  repair matches ids: reconcile-time index detection warns on equal keys (all scopes);
  file-mutating re-space is confined to the `tk sync` integrity step (auto-commit) and
  `tk doctor --repair` off-sync (ambient / `TK_SCOPE` or `--all` per doctor scope
  selection; including non-auto-commit with no sync seam), rewriting only the tied files.
  This keeps
  `tk order` a single-file write on the
  hot path and never renames or rewrites ranks from a pure read. Re-space assigns distinct
  legal keys that preserve the pre-repair `(order, id)` relative order among the tied set
  (and relative to untied neighbours), using only this grammar.
- Pathological length (optional escape): repeated inserts into the same microscopic gap
  can grow a fractional tail long. Bare `tk doctor` reports over-long `order` values (soft
  threshold: length > 64). File rewrite is only `tk doctor --re-space-order` (explicit;
  same shape as equal-key re-space: only the rewritten files; auto-commit self-commits when
  a git-root exists — see doctor CLI). Never implicit on `tk order`, bare doctor, or
  `--repair`.
- Why not dense integers: no value between 3 and 4, so an insert rewrites every
  displaced project — reintroducing the identity/order coupling the id scheme escaped
  and turning every offline `tk order` into a conflict source. Integer+fraction rank keys
  keep `tk order` a single-file edit for normal use.
- Always quoted (`order: "a0"`). Keys mix digits and letters; a bare YAML scalar can be
  coerced (e.g. bare numbers, or legacy letter-only forms such as `n` / `y` / `no` under
  YAML 1.1). Quoting keeps the value a string. `tk doctor` flags an unquoted/non-string
  `order`.

Derived, never in frontmatter: task counts, percent done, next runnable project, blocked
count. Materialized in the index, recomputed on reconcile, so they never go stale and
never pollute the source of truth.

## Storage

DECISION: a scope is a directory holding `tk.cue` plus the project `.md` files,
flat. That directory is the scope directory (`dir`) — where the markdown and `tk.cue`
live; not the code-root (ambient cwd match) and not the git-root (derived). No
subdirectory per scope — the directory is the scope. The one exception is `archive/`,
the storage location for **terminal** projects (see "Done and archive"). Reconcile
scans dir root and **immediate children only** of that one `archive/` subdirectory;
both stay indexed and resolvable. DECISION (archive nesting depth): only immediate
children of `archive/` whose basename matches the project filename shape
(`<id>-<slug>.md`) are project files (indexed, allowlisted, layout-repair targets).
Nested paths under `archive/` (e.g. `archive/foo/<id>-<slug>.md`, `archive/2024/…`,
any deeper tree) are **non-allowlist residue** — not indexed as projects; doctor /
`non_allowlist:` / sync residue warnings; never committed by snapshot. Location is a
projection of terminal-ness, not an optional second lifecycle or nested taxonomy.

```
<dir>/
  tk.cue                          # scope name, schema, auto-commit setting, knownTags
  .gitignore                      # ignores .tk.lock; written by tk scope init
  wc-ab2c-network-output-redesign.md
  wc-k3m9-cdp-session-pool.md
  archive/                        # terminal projects only (location follows status)
    wc-h2n7-legacy-cleanup.md
  ...
```

- `tk.cue` (renamed from the old `config.cue`) is namespaced so it cannot
  collide with a repo's own `config.cue` or another tool's, now that the dir may
  be any directory the user points at, not a tk-dedicated one.
- The dir is intended to hold only tk scope files (source of truth + the small allowlist
  below). Source code never belongs in it. That rule is not trusted as an informal
  convention alone: auto-commit snapshot commits only an explicit allowlist (see
  "tk sync"), and non-allowlist paths under the dir are residue — never committed by
  tk, warned on `tk sync` stderr, and flagged by `tk doctor` for human cleanup. Do not
  put secrets, dumps, or unrelated trees here; even with the allowlist, residue can
  still sit on disk and confuse humans. It is typically a subdirectory of the code it
  tracks (`<repo>/.agents/tk/`), or a standalone directory for personal/cross-cutting work.
- Recommended dir (scope directory): `.agents/tk/` (beside other agent tooling) or
  `.agents/projects/`. Not enforced — the user names the path at init.
- A git repo may host several scopes, each rooted at a distinct code-root — a large
  monorepo carries one scope per team/area (`/org/mono/teamA`, `/org/mono/teamB`), and a
  personal tk repo carries several scopes as sibling subdirectories. The only per-repo
  constraint is autoCommit consistency: every scope sharing a repo agrees on its
  autoCommit (see "Auto-commit"). The unit is the scope (a dir), not the repo.
  (Superseding the earlier "one repo = one scope", which was an artefact of welding
  code-root to the repo root; the dir/code-root decoupling in "Resolution" removes it.)

## Visibility

DECISION: every registered scope is visible from anywhere on the machine. There is no
private/local class. `tk scope list`, cross-scope `tk search`, `tk list --scope`, and
`tk get <scope>-<id>` reach any registered scope. This is the payoff of the flat id
namespace and the machine-wide index: an agent in one repo answers "what is in flight
in wc?" without leaving.

Consequences accepted:
- Scope names must be machine-unique (see "Scope names").
- Registering an existing scope is a deliberate act (`tk scope import`), never automatic, so
  cloning a repo does not silently pull its scope into the machine-wide view.

## Resolution

DECISION: resolution is a registry lookup, not a filesystem walk. There is no up-scan
for a marker and no blessed default location. A scope becomes known only by `tk scope init`
or `tk scope import`, which records it in the machine-local registry.

The registry (see "Registry") records, per scope: its name, its dir (where the
`.md` and `tk.cue` live), and its code-root (the directory tree under which the
scope is ambient).

Ambient scope (bare `tk list`, `tk next`, `tk create`): the scope whose code-root is the
longest prefix of cwd. Precedence, most-specific first:
1. `--scope` flag.
2. `TK_SCOPE` env.
3. longest-prefix code-root match against cwd (the registry).
4. none -> scope-requiring commands error with guidance; discovery commands still work.

DECISION (optional `--scope` on ambient verbs, v1 closed): every command that resolves
an **ambient write or inventory scope** accepts optional `--scope <name>` as the top
precedence ambient override. When set, that name **is** ambient for the invocation —
short-id resolution, create target, list/next/lens/order domain, sync target (when
not `--all`), and complete-state mutators all use it. Closed surface (all take
`--scope` unless noted):

| Verb | `--scope` | Notes |
|---|---|---|
| `list`, `next`, `create`, `status`, `order`, `edit`, `lens` | yes | Ambient write/inventory/path-open |
| `get`, `meta`, `deps` | yes | Only needed for **short-form** id (ambient override); full id is registry lookup by id prefix and never requires ambient |
| `sync` | yes | Bounds ambient sync target; `--all` still means every auto-commit git-root (flag not required) |
| `search` | yes | Bounds FTS (search default is machine-wide when omitted — same as today) |
| `query` | no separate ambient invent | SQL against the index; scope filters live in the SQL if needed |
| `doctor` | **no** | Discovery; ambient via `TK_SCOPE` / cwd only (doctor scope selection) |
| `scope *`, `skill`, `help` | n/a | Not ambient project verbs |

Do not invent a second ambient channel. Skill and hot-path tables must show `--scope`
on every row that accepts it — not only `list`/`search`.

- Longest-prefix means nested code-roots resolve deterministically: register `<repo>/`
  -> scope A and `<repo>/frontend/` -> scope B, and cwd under `frontend/` picks B,
  elsewhere in the repo picks A.
- No two scopes may register the identical code-root (bare `tk list` could not choose).
  Nested is fine; identical is rejected at init/import.
- Cross-scope addressing: full id `tk get wc-ab2c` is a direct registry lookup by name
  in the id, independent of cwd; `--scope` is the ambient override for short form and
  scope-requiring verbs without an id.

The lookup is cheap (an in-memory prefix match over a handful of registry entries), and
because it is a registry read rather than a stat-walk, it never depends on filesystem
markers or git.

## Registry

DECISION: the registry is machine-local durable state in the XDG config directory
(`${XDG_CONFIG_HOME:-~/.config}/tk/`). It is not synced and never lives in a
repo. Each machine registers its own scopes: the scope's files travel (git clone,
Dropbox), but "this machine knows about this scope, at these paths" is a per-machine
fact.

It is the one thing the index cannot rebuild — a scope's files record their content and
state, not the fact of registration or the code-root binding — so it lives in a real
config file, not the derived index. Drop the index and it rebuilds by walking the
registry; drop the registry and the scopes are simply unknown until re-`init`/`import`.

Shape (one CUE package, one file per concern):

```cue
// registry.cue — written by tk scope init|import|rebind|rename|forget
scopes: {
    wc: {                                             // single-scope repo, files under root
        dir: "/home/grant/projects/webctl/.agents/tk"
        root:  "/home/grant/projects/webctl"
    }
    ta: {                                             // one of several scopes in a monorepo
        dir: "/org/mono/teamA/.agents/tk"
        root:  "/org/mono/teamA"
    }
    home: {                                           // standalone, files == root
        dir: "/home/grant/notes/home"
        root:  "/home/grant/notes/home"
    }
}

// lens.cue — written by tk lens
lens: {wc: ["frontend", "style"]}   // machine-local default tag view, per scope
```

DECISION: the XDG config directory is machine-written, owned by tk — the user never
needs to hand-edit it, because every mutation has a verb. It is one CUE package split
into per-concern files, each owned wholesale by the verb family that writes it:
`registry.cue` (`tk scope init|import|rebind|rename|forget`) and `lens.cue` (`tk lens`,
plus `tk scope rename`, which re-keys its per-scope entries, and `tk scope forget`, which
drops them — every `lens.cue` writer, not `tk lens` alone).
There is no `editor` key: `tk edit` resolves the editor from `$EDITOR` at point of use
(already the CLI-surface behaviour), so no setting exists that would require
hand-editing; a `settings.cue` appears only when a real setting and its verb do. Reads
and writes go only through the CUE Go modules (`cuelang.org/go`): load/compile the
package, mutate via the API, regenerate the whole owned file with `cue/ast` +
`cue/format`, write to a temp file in the same directory, atomically rename. No
string-built CUE and no non-CUE codec for these paths (see Configuration). Wholesale
per-file regeneration is safe precisely because the files are machine-owned — there is
no hand-authored formatting to preserve. All XDG-config writes serialize under one
machine-global flock (`${XDG_CONFIG_HOME:-~/.config}/tk/.tk.lock`); the per-scope flock
protects scope files, not this machine-global state, so without it two concurrent
`tk scope init`s could silently drop a registration. Hand-editing still works (it is
plain CUE, read back through the same CUE load path), but an XDG file that will not
parse is a hard error naming the file — the registry is the bootstrap that locates every
scope, so unlike a scope's `tk.cue` there is nothing to degrade to.

Each scope records exactly two paths, and they are independent:
- `dir`: where the `.md` and `tk.cue` physically live; what reconcile
  stats. Must be distinct per scope.
- `root` (code-root): a single path — where the scope is ambient for bare-`tk`
  resolution. Not a list (a scope has one root); `tk scope rebind` re-points
  `dir` and/or `root` (see Scope lifecycle). `dir` need not live under `root` —
  they are matched in different steps and never interact. Relative-dir-under-root
  is not a registry requirement; both fields are absolute paths on the wire.

The git repo is not recorded. It is derived on demand from `dir`
(`git rev-parse --show-toplevel`), so moving or renaming the repo never staples the
registry; several scopes whose `dir` derive the same repo share that repo as their sync
unit. The scope name is cached here for fast `--scope` lookup; the authoritative name is
in each scope's `tk.cue`. `tk doctor` reconciles the two and flags drift (a scope whose
`tk.cue` name no longer matches its registry key — typically a remote `tk scope rename`
absorbed as ordinary file changes — or a registry entry whose dir is gone). Drift
is not auto-healed: registration is deliberate, so the recovery is unregister then
re-import (see `tk scope rename`), not a silent re-key.

DECISION (registry / `tk.cue` name drift — fail closed): when a registered scope's
registry key and the on-disk `tk.cue` `name` for that `dir` disagree, that scope is
**unusable** until deliberate re-registration. Do not document or implement a half-working
ambient window (short-id works, correct full id fails, wrong full id fails). That mode is
agent-hostile and invites working through a broken name binding.

While drift is present for a scope entry:
- Hard-error every command that would use that scope: ambient code-root hit, `--scope`
  / `TK_SCOPE` naming the registry key, short-form or full-id resolution that would land
  in that dir (including full ids whose prefix is the new `tk.cue` name — the registry
  has no such key yet, and ambient must not quietly adopt the new name either).
- Error **must** name both names, the dir, and the exact recovery command (never a
  vague "re-register"): e.g. `scope name drift: registry key "old" but tk.cue name is
  "new" at <dir> — run: tk scope forget old && tk scope import <dir> [--code-root …]`.
  Include `--code-root` in the suggested line when the registry entry had a non-default
  code-root. Stable token: `name_drift:` (doctor and command refuse share it).
- Still allowed (so recovery and diagnosis work): bare `tk doctor` / doctor flags,
  `tk scope list`, `tk scope forget`, `tk scope import`, `tk skill`, `help`, and other
  discovery commands that do not open project files under the drifted scope. `tk sync`
  that would touch the drifted scope's git-root refuses that root for the same reason
  (name binding unverifiable for allowlist messages / integrity attribution) until fixed —
  or at minimum refuses operations scoped to that entry; sibling scopes in the same
  git-root without drift keep their ordinary rules (sync preflight still applies for
  autoCommit consistency across the root).
- Index: do not pretend the drifted entry is a healthy namespace. Reconcile may skip
  project materialization for that scope (or keep last rows but never serve them for
  `next`/`get`/`list`/`deps`) while the refuse stands; after forget+import, normal
  reconcile rebuilds under the new key. Prefer skip-or-drop-on-doctor over serving
  mixed old-key / new-id rows.
- Never auto-rekey the registry to match `tk.cue` (out of auto-repair budget).

## Scope lifecycle

DECISION: `tk scope init <dir> (--name <name> | --auto-name) [--code-root <path>]
[--auto-commit]` creates a new scope and registers it. `tk scope import <dir>
[--code-root <path>]` registers an existing on-disk scope (post-clone), files in place.
They are symmetric entrances to the registered state; init writes a fresh `tk.cue` and
a `.gitignore` covering `.tk.lock` (authoring its own dir, not managing the
repo), import reads an existing scope as it ships (name and autoCommit come from its
`tk.cue`, so import takes neither `--name` nor `--auto-commit`).

DECISION: `tk scope rebind <dir> --name <name> [--code-root <path>]` rewrites the
machine-local registry paths for an **already registered** scope. It is the only path
rebind verb. There is no `tk scope use`. Argument shape matches init/import: positional
`<dir>` is where the files live; `--code-root` is the ambient root flag (not a second
`--root` spelling).

Shape and rules:
- `<dir>` is required (never defaulted). Resolved against cwd if relative, then stored
  as an absolute path — always updates the registry `dir` for that name.
- `--name <name>` is required. Selects the registry entry to update. Not inferred from
  `tk.cue` alone; not optional. Unknown name → error (import/init first). No
  `--auto-name` (rebind does not author a name).
- `--code-root <path>` is optional. If set, re-points registry `root` (relative → cwd,
  store absolute). If **omitted, leave `root` unchanged** — do **not** re-run the init
  code-root default matrix (that would silently rewrite ambient on a dir-only move).
- A scope has one `root` and one `dir`; rebind moves them, it does not accumulate.
- Registry wire: both paths absolute. Relative-dir-under-root is not required and is not
  the encoding.
- Does not touch scope files, `tk.cue`, the index content model, or the lens — same
  registry key, so the lens survives. Never implemented as forget+import under the hood.
- Not name repair: `name_drift:` still requires forget+import (path rebind ≠ identity).
- Idempotent: same effective `dir` and (when `--code-root` given) same `root` already
  stored → success; may emit a short stderr note that nothing changed.

Validation (same family as init/import, on the **post-rebind** effective paths):
- New `dir` must open; must contain a parseable `tk.cue` whose `name` equals `--name`
  and the registry key (else refuse — do not half-bind a wrong tree).
- Code-root collision: if `--code-root` is set, reject a `root` identical to another
  scope's (nested roots still fine; longest-prefix resolves).
- Dir disjointness: reject overlapping/equal dirs against other scopes on effective
  absolute paths.

Common patterns:
```text
# Ambient only: re-point code-root; pass current dir again
tk scope rebind /path/to/.agents/tk --name wc --code-root .

# Whole-tree move / reclone
tk scope rebind /new/clone/.agents/tk --name wc --code-root /new/clone

# Scope folder moved inside the same ambient tree (root unchanged)
tk scope rebind /same/root/.agents/projects --name wc
```

tk is non-interactive — it never prompts. Everything it needs is a flag or a deterministic
default; the only TTY-sensitive behaviour anywhere in tk is colour (below). So init takes
the name and auto-commit choice as flags, not prompts.

DECISION (colour / TTY, v1 closed — agent-safe):
- Colour is **optional human polish**, never part of a machine contract. Default is
  **auto**: colour may be used only when the stream being written is a TTY **and** colour
  is not disabled.
- **Disable:** if the environment variable `NO_COLOR` is set (any value, including empty —
  presence alone), emit **no** ANSI on any stream. v1 has no `--color` flag and does not
  honour `FORCE_COLOR` (add later only if concrete human pain appears).
- **Non-TTY:** when stdout is not a TTY, stdout has no ANSI. When stderr is not a TTY,
  stderr has no ANSI. Agent pipes and redirected logs therefore never see colour.
- **Stdout purity:** absolute path hand-off lines and every TSV field on `list` / `search`
  (and any other structured stdout) are always plain text — **no ANSI**, even on a TTY.
  Colour on stdout is not used in v1 (path/TSV stability beats decorative board colour).
- **Token prefix purity:** closed integrity / doctor tokens (`duplicate_id:`,
  `schema_error:`, `parse_error:`, …) are always emitted as the literal ASCII prefix at
  the start of the line (after optional leading whitespace only if the design already
  allows it — prefer column 0). **Never** wrap, colour, or interrupt the token characters
  with ANSI. If a human stderr line is coloured, decoration applies only to text **after**
  the token and its following space (or to non-token diagnostic lines).
- Agents match tokens with prefix rules (e.g. line starts with `duplicate_id:`); implementers
  must not make that depend on stripping ANSI first.

Name (init only): exactly one of `--name <name>` or `--auto-name` is required; supplying
neither is an error (the name is never silently defaulted — "always a conscious choice"
survives, and `--auto-name` is that conscious choice to accept derivation). `--name` is
validated against `^[a-z0-9]{1,12}$` (full scope alphabet, including `i`/`l`/`o`/`0`/`1`).
`--auto-name` runs the closed derivation in "Scope names" (typeable subset alphabet;
hard error if sanitize yields empty/illegal — pass `--name`; never auto-suffix). Because
code-root may be a subdirectory, `--auto-name` reads well for monorepos
(`/org/mono/teamA` -> `ta`). A derived name that is already registered is a hard error
naming the clash and telling you to pass `--name` — never an auto-suffix (the beads
junk-name mistake).

The dir is required (never defaulted). The code-root defaults from git or is given
explicitly, by this matrix — `--code-root` is always allowed (it is what makes several
scopes share a repo), and defaults are just conveniences:

| dir in a git repo? | `--code-root` given? | result |
|---|---|---|
| yes | no | code-root = the repo root (`git rev-parse --show-toplevel`) — single-scope default |
| yes | yes | code-root = the given path — the sub-scoping case (monorepo team, sibling in a tk repo) |
| no | no | code-root = the dir — standalone, ambient in its own tree |
| no | yes | code-root = the given path |

Errors teach the fix, e.g.:

```
--code-root /elsewhere is not inside the git repository /foo/bar that holds the
dir. A code-root is where the scope is ambient; keep it inside the repo, or omit
it to default to the repo root:
  tk scope init /foo/bar/.agents/tk --name <n> --code-root /foo/bar/teamA
```

Registration checks (both commands):
- Scope-name collision: DECISION hard fail. If the name is already registered, refuse.
  There is no rename-on-import — the name is baked into every id, filename, and
  in-scope reference, so a rename applied only locally would diverge from every other
  clone. The remedy is `tk scope rename` (below), run at the source before other machines
  register — the cheap path. Renaming after other machines already have the scope is
  rare recovery: those machines re-register with forget then import (see rename), not an
  auto-absorb. A same-store re-clone (same name, same ids at a new path) is refused too,
  with guidance to `--code-root`/re-point rather than double-register.
- Code-root collision: reject a code-root identical to an existing scope's (see
  "Resolution"). Nested code-roots are fine (longest-prefix resolves).
- Dir disjointness: reject a dir identical to, nested within, or containing
  any registered scope's dir. Two scopes cannot share one dir, and — unlike
  code-roots, where nesting resolves cleanly by longest prefix — dirs must be
  mutually disjoint, never nested. This is a load-bearing invariant, not a nicety: the
  `tk sync` snapshot (step 1) treats everything inside a scope's dir as that scope's
  to commit, and reconcile scans a dir flat (plus its one `archive/`); if scope B's
  dir nested inside scope A's, A's sync would sweep and commit B's files under A's
  repo while A's flat reconcile ignored them — cross-scope attribution and double-handling
  the git-root lock cannot see, because it guards the shared git index, not file ownership.
  The error teaches the fix (choose a sibling path, e.g. `.agents/tk-teamB`, not a path
  under an existing scope). Nested code-roots stay fine — only dirs carry the
  disjointness rule.
- autoCommit consistency per repo: every scope sharing a derived git-root has the same
  `autoCommit` value. Auto-commit is a property of the branch/remote path, not a
  subdirectory, so one repo cannot mix auto-commit (tk pushes) with non-auto-commit
  (repo-driven PR commits or plain files). A scope added to a repo that already hosts
  scopes inherits their `autoCommit` (so `--auto-commit` is optional there: omit means
  inherit, not re-default to false; an explicit flag that contradicts siblings errors);
  the first scope in a repo sets it. An auto-commit scope that must live beside a
  non-auto-commit tree belongs in its own repo (sibling or submodule), which derives a
  different git-root. The error names the existing autoCommit and points at that fix.
  Same remedy when the user wants **write/sync isolation** between auto-commit scopes:
  multi-scope-per-repo is one-push convenience, not fault isolation (see Auto-commit).
  This check is not init-only: the git-root is derived at runtime (never stored — see
  "Registry"), so a later git-topology change can bring divergent-autoCommit scopes under
  one git-root after both were registered. `tk sync` re-derives and re-validates
  autoCommit consistency across the scopes sharing its git-root as a preflight (refusing
  rather than pushing under a violated invariant), and `tk doctor` runs the same check
  off-sync; sync safety does not rely on the invariant silently persisting (see
  "tk sync", step 1).
- Malformed `tk.cue` (import only): fail the import cleanly, naming the parse
  error, rather than registering a scope whose schema will not load. This is the one
  place an untrusted scope's config is first read.

`--auto-commit` records `autoCommit: true` in `tk.cue` (init only; see "Auto-commit").
Omitted records `autoCommit: false`. It is never prompted. Doc/error labels only (not
stored): "tk-driven" / "auto-commit" when true; "repo-driven" when false and inside git;
"plain files" when false and outside git.

Init matrix:

| Situation | `--auto-commit`? | Result |
|---|---|---|
| Outside git | omit | plain files (`autoCommit: false`) |
| Outside git | set | tk-driven planned: file writes succeed; self-commit and `tk sync` disabled until repo/remote exist (`sync_disabled:` on writes/sync). After a git-root appears, complete-state verbs may self-commit; **`tk create` still never self-commits** (scaffold rule) — first git durability of a create is still the next `tk sync` snapshot |
| First scope in a git repo | omit | repo-driven (`autoCommit: false`) |
| First scope in a git repo | set | tk-driven (`autoCommit: true`) |
| Repo already has scopes | omit | inherit siblings' `autoCommit` |
| Repo already has scopes | set | must match siblings; contradict → error |

Accepted tradeoff: first scope in a git repo + omit flag = repo-driven, with no separate
"I meant that on purpose" signal. With a single positive flag it is the only coherent
rule: off is default; on is opt-in. Wrong omit on a dedicated tk repo → files change on
disk, no self-commit, no sync warnings. Mitigate in docs / `tk skill` / init help ("in a
dedicated tk repo, pass `--auto-commit`"), not a second flag.

Import reads `autoCommit` from the on-disk `tk.cue` (no flag). There is no separate
host/none gate: false outside git is plain files; false inside git is repo-driven.

Discoverability without auto-slurping: tk never probes the filesystem for an unregistered
scope. Resolution is registry-only (see "Resolution") — no up-scan, no candidate-path
check for `tk.cue`, no "unimported scope here" inference from cwd. A scope-requiring
command with no ambient scope errors with the generic no-scope guidance only (see "CLI
surface"). Post-clone registration is a deliberate `tk scope import <dir>` by the
user (or by an agent that already knows the path from `tk skill` / human instruction);
cloning never auto-registers, and v1 does not discover an on-disk `tk.cue` for you. The
planned `tk skill install` is the consented way to leave that path in-tree for a cold
agent.

DECISION: `tk scope rename <old> <new>` renames a scope in place — the tooled remedy for
a name clash. The name is baked into every id, filename, and in-scope reference, so
rename must be an operation, not an instruction. It validates `<new>`
(`^[a-z0-9]{1,12}$`, machine-unique), then under the scope flock rewrites everything
in-scope in one operation: the name in `tk.cue`, the `<scope>-` prefix of every project
id in frontmatter, every filename (the filename mirrors the id), and every in-scope
`depends`/`related` edge; for auto-commit, one commit **after** all file writes succeed
(see multi-file rewrite durability below). Cross-scope inbound edges live in other
scopes' repos and cannot be rewritten here — exactly as in the id-collision repair, the rename reports each
(read from the machine-wide `edges` table at rename time) as an `edge_verify:` line in
its own output ("target scope was renamed — update this reference") — operation-time
only, not persisted; a later doctor sees such an edge only as unresolvable/dangling. The authoring
machine's registry and lens entries are re-keyed (both machine-written XDG files key by
scope name) only after the in-dir rewrite completes successfully.

DECISION (multi-file in-scope rewrite durability — scope rename, id-collision repair,
equal-order re-space, and any other multi-file integrity rewrite): logical “one
operation” is not a crash-proof filesystem transaction. v1 contract is best-effort under
the scope flock plus doctor heal — no rewrite journal, no fsync-all protocol beyond
normal file writes.

- Hold the scope flock for the whole plan.
- Per project file prefer **write new path then remove old** (or atomic rename into place
  after writing a temp in the same directory) so a kill mid-file does not leave a
  truncated sole copy. Rewrite frontmatter before or with the path change so id and
  basename stay paired.
- Order the plan so partial progress is **detectable**: e.g. `tk.cue` name last on scope
  rename (or first with a hard doctor rule for mixed filename prefixes vs `tk.cue` name);
  bare doctor already flags structural id/filename mismatch and registry/`tk.cue` drift.
- Auto-commit: **do not** `git commit` until every intended file write for that plan has
  succeeded. Partial disk state may exist uncommitted; the next successful re-run or
  `tk doctor --repair` (for integrity repairs) / re-run of `tk scope rename` (for rename)
  finishes the plan. Non-auto-commit: same disk rules; host commits when clean.
- On interrupt: do not invent a background healer. Operator/agent runs bare `tk doctor`,
  then the same mutating command again or `--repair` as appropriate. Re-entry must be
  **idempotent where possible**: skip files already at the target id/path; continue
  remaining work; never double-extend a short-id that was already repaired.
- Out of budget for v1: persistent `.tk-rewrite-journal`, multi-phase commit protocols,
  or automatic resume on every unrelated command.

Cheap path: rename at the source before other machines register the scope, so clones
import under the final name and never see drift.

Post-share recovery (rare): another machine that already registered the old name receives
the rewrite as ordinary file changes at its next sync. Its registry still keys the old
name; `tk.cue` and all project ids now use the new name. That is **name drift**: the
scope is fail-closed unusable (see Registry) until re-registration — not a degraded
operable mode. `tk doctor` reports `name_drift:`; project verbs and ambient use of that
code-root hard-error with the same recovery. There is no auto-rekey and no silent absorb
— registration is deliberate, and a bare `tk scope import` of the same dir would hit the
dir disjointness guard while the old registration still exists. The recovery is conscious
re-registration:

```
tk scope forget <old>
tk scope import <dir> [--code-root <path>]
```

`forget` drops the old registry and lens entries and the index rows; `import` registers
under the name now in `tk.cue`. The machine-local lens is not preserved across that
boundary — re-set with `tk lens` if wanted. That cost is accepted: post-share rename is
the expensive path, not a multi-machine operation the registry tries to heal. Prefer
renaming before other machines register so this window never appears.

DECISION: `tk scope forget <name>` unregisters a scope: removes its registry and lens
entries and drops its index rows. It never touches the scope's files or repo — the files simply
become unknown to this machine until re-registered with `tk scope import`. This is the
deliberate permanent exit; a merely unreachable dir (unmounted drive) stays
registered, is reported by reconcile/doctor as `unreachable_scope:`, and keeps its
index rows until forget or a successful remount reconcile (see "Invalidation and
reconcile").

## Configuration (CUE)

DECISION (owner hard lock-in — not under review): config is CUELang end to end.
Both tiers — machine-written XDG (`registry.cue`, `lens.cue`) and scope `tk.cue` — are
CUE. No alternate on-disk format (JSON, TOML, gob, hand-rolled text) for either tier in
v1 or as a reserved escape. CUE is the product config language; latency of
`cuecontext.New()` is accepted operational cost, not a reason to split formats.

DECISION: every CUE config file is read and written only through the CUE Go modules
(`cuelang.org/go` — load/compile/unify/evaluate, and `cue/ast` + `cue/format` for
writes). Forbidden: string-templating `.cue` files, `fmt.Fprintf` of CUE syntax,
`encoding/json`/`yaml` round-trips of the same paths, or a second hand-rolled parser.
Human/agent hand-edits remain plain CUE on disk; tk always re-enters via the CUE API.
Malformed CUE is reported as CUE errors, never half-parsed by a fallback.

Two **independent** config tiers (different concerns — **not** a least-to-most-specific
key-merge stack where “later overrides earlier”):

1. XDG config directory `${XDG_CONFIG_HOME:-~/.config}/tk/` — machine-local and
   machine-written by tk (see "Registry"): one CUE package, per-concern files
   (`registry.cue`, `lens.cue`). Optional; tk runs on built-in defaults when absent.
   Answers: which scopes are registered here, and this machine’s per-scope lens.
   (No configurable default autoCommit — omit is false (with inheritance when siblings
   exist), `--auto-commit` is true; see Scope lifecycle, so there is nothing else to
   configure.)
2. Scope config `<dir>/tk.cue` — the scope name, auto-commit setting, optional custom
   statuses, optional custom frontmatter fields, and the optional controlled tag
   vocabulary (`knownTags`). Answers: this scope’s schema and sync mode. This is the
   tier that validates each project's frontmatter.

These tiers do not override each other’s keys (registry does not set `autoCommit`;
`tk.cue` does not register the scope on a machine). **Ambient scope resolution** is a
separate precedence chain (`--scope` > `TK_SCOPE` > longest-prefix code-root) under
Resolution — not a third config file tier.

Why CUE: the custom statuses and fields a scope declares become the schema `tk doctor`
(and every mutating write) validates every project's frontmatter against. CUE is a typed,
validated schema language — chosen on purpose, not as a heavier TOML.

### Scope `tk.cue` shape

DECISION: `tk.cue` is a single concrete CUE value per scope. `tk scope init` writes a
minimal valid file (name + autoCommit); everything else is optional and additive. Shape:

```cue
// <dir>/tk.cue — synced with the scope; humans/agents may edit after init
name:  "wc"    // required; ^[a-z0-9]{1,12}$; authoritative (registry caches a copy)
autoCommit: true  // required bool; true only with --auto-commit (or inherited true)

// Optional controlled tag vocabulary. Free-form tags remain allowed; doctor warns on
// values not in this list (typos), it does not reject them.
knownTags?: [...string]

// Additive custom statuses. Built-ins are immutable and must not be redeclared.
// Name: lowercase, hyphen-joined, ^[a-z][a-z0-9-]{0,31}$, not a built-in status name.
// category drives default list filters and terminal-status merge dispute / depends
// satisfaction — not membership in tk next (only built-in todo is ever next-eligible).
// See "Status and dependencies", "Merge conflict handling".
statuses?: {
	[name=string]: {
		category: "active" | "backlog" | "done"
	}
}

// Optional custom frontmatter fields. Keys sit flat beside built-ins in project YAML.
// Name: ^[a-z][a-z0-9_]{0,31}$ (snake-friendly YAML keys); must not shadow a built-in
// frontmatter key (id|status|order|depends|related|tags|created|links|summary|
// status_conflict).
fields?: {
	[name=string]: #Field
}

#Field: {
	type: "string" | "int" | "bool" | "strings"
	// Optional closed enum. Legal only when type is string or strings; every value
	// present in frontmatter must be in this list (doctor hard-flags unknowns).
	values?: [...string]
}
```

Example (custom status + fields):

```cue
name:  "wc"
autoCommit: true
knownTags: ["frontend", "backend", "style", "api", "network", "cdp"]
statuses: {
	shipped:  {category: "done"}
	wontfix: {category: "done"}
}
fields: {
	estimate:     {type: "int"}
	area:         {type: "string", values: ["frontend", "backend", "cross"]}
	stakeholders: {type: "strings"}
}
```

Field types (closed set for v1):
- `string` — YAML string scalar. Merge: scalar rules.
- `int` — YAML integer scalar. Merge: scalar rules.
- `bool` — YAML bool scalar. Merge: scalar rules.
- `strings` — YAML sequence of strings. Merge: 3-way set merge (same as `tags`/`depends`/
  `related`/`links`). Not a free-form sequence of mixed types.

Validation (writes and `tk doctor`):
- `status` must be a built-in or a name declared under `statuses`.
- Each declared field, when present in frontmatter, must match its `type` (and `values`
  when set). Absent is always legal — fields are optional on every project; there is no
  required-field flag in v1.
- A frontmatter key that is neither a built-in nor declared under `fields` is a doctor
  warning (hand-edit / forward-compat residue), not a write blocker and not silently
  dropped from the file. The raw file still carries it so nothing is hidden.
- Redeclaring a built-in status or shadowing a built-in frontmatter key in `tk.cue` is a
  hard config error (scope read-only until fixed), same class as a malformed file.
- Custom status names that collide with built-ins, or field names outside the name
  alphabet, are hard config errors at evaluate time.

DECISION: there is no dedicated `tk field` / `tk set` verb in v1. Custom fields are
authored by direct frontmatter edit (`tk edit` or the agent's file tool), the same path
as body and tags. tk validates on the next reconcile/write; it does not intermediate
field mutation. A verb family can return later without schema change. Read-side inspect
of the header is `tk meta` (see CLI surface) — not a write API and not a substitute for
opening the body.

Custom fields live in the project file (flat frontmatter) and are materialized in the
index for `tk query` / filters. Agents read them from the file via path from `get`/`next`
/ `create`/`status`, or inspect the header with `tk meta` — there is no nested JSON
`fields` object to document. The index implementation may use a JSON column; `tk query`
schema is not a stable API either way.

Cost note (not a format escape hatch): CUE is heavier than decoding TOML —
`cuecontext.New()` plus compile/unify on a command an agent may call dozens of times a
session. That cost is accepted under the hard lock-in above. Steady-state scope config
cost is reduced by caching (below); the XDG registry remains a fixed per-command CUE load
because it is the bootstrap.

DECISION: the CUE evaluation of each scope's `tk.cue` is cached in the index,
keyed by the `(path, mtime, size)` of every file in that config's import closure — not
just the entry file, so an edit to an imported `schema.cue` or a `cue.mod` module
invalidates the cache rather than validating against a stale schema. A steady-state
command re-evaluates a scope's config only when a file in its closure changed; otherwise
it deserializes the cached values. The XDG tier is small and optional and is evaluated
in-process each command (it holds the registry, so it must be read before any scope's
files are located — caching it in the index would be a bootstrap circle). Cache hits
still originated from a prior CUE evaluation; cold paths always use the CUE modules.

DECISION (accepted cost): because the registry cannot be cached in the index — it is the
bootstrap that locates every scope — every invocation, the hot `tk next` included, loads
XDG config through CUE. `cuecontext.New()` is instantiated once per process and amortizes
across the registry read and any cache-miss scope-config evaluation in the same command.
There is no reserved JSON (or other) split of `registry.cue`/`lens.cue` — both tiers stay
CUE, read and written only via `cuelang.org/go`, for the life of the design.

DECISION: a malformed `tk.cue` makes its scope read-only until fixed — fail fast on
write, never a silent degrade. A `tk.cue` that will not compile cannot be trusted for
either the custom schema a write validates against or autoCommit, which decides how a
write commits; autoCommit lives only in `tk.cue` (the registry caches the name, not
autoCommit), so there is no safe value to fall back to. Writing under a guessed schema
or, worse, a guessed autoCommit is exactly the quiet failure the Scope-lifecycle
autoCommit rule refuses to risk — a silently-wrong false fallback would let an
auto-commit scope pile up uncommitted, unpushed work with no warning. So tk refuses every
mutating command on the affected scope with a clear error (`scope config unparseable —
fix tk.cue before writing`) rather than degrade the write.

Reads need neither the custom schema nor autoCommit, so they stay fully available:
`tk get`/`meta`/`next`/`list`/`deps`/`search` work against the scope, and because only
that one scope's writes are blocked, machine-wide commands that reconcile many scopes
(cross-scope `search`/`list`) are never bricked by one broken config. Per-scope file mutations on a
sibling scope that still parses keep working. This is the isolation property that matters
for ordinary commands — one bad edit degrades nothing machine-wide and loses no work; it
just gates that scope's writes. It is distinct from the per-project `parse_error`
quarantine in "Invalidation": a project `.md` is data, so a bad one is a flagged row the
rest of the scope reads past; `tk.cue` is the scope's write contract, so a bad one blocks
writes rather than being written past. The block is loud — `tk doctor` reports it
prominently and a terse warning rides the scope's reads. Fix the config and the next
command re-evaluates it (cache keyed by the import closure's `(path, mtime, size)`) and
restores writes.

TRADEOFF (accepted): `tk sync` is the exception to that per-scope isolation. Sync is
repo-granular and its preflight must re-verify autoCommit consistency across every scope that
shares the derived git-root (see "tk sync", step 1). An unreadable autoCommit is the same class
of failure as a disagreeing autoCommit — there is no safe value to assume — so if any scope
sharing that git-root has an unparseable `tk.cue`, `tk sync` refuses the entire git-root
until it is fixed (`scope <x> config unparseable — fix <dir>/tk.cue before sync`),
rather than omitting the broken sibling and pushing under an incomplete proof. Same shape
as the mid-rebase freeze among auto-commit siblings: availability couples at the repo
boundary only where the shared invariant is checked before network mutation. Ordinary
per-scope writes on a healthy sibling remain unblocked.

## Read interface (SQLite index)

DECISION: SQLite is the read/query interface in v1, via `modernc.org/sqlite` (pure Go,
no cgo; FTS5 is compiled in by default). It is a materialized view derived from the
files, not a second source of truth.

DECISION: one machine-wide index at a fixed, machine-local path:

```
${XDG_STATE_HOME:-~/.local/state}/tk/index.db
```

- It lives in XDG state, never inside any scope's dir, so no version-control or
  filesystem-sync mechanism (git, Dropbox, Syncthing, NFS) ever carries it. The
  "derived, never synced, rebuildable" invariant is true by construction for every
  autoCommit mode, and WAL always runs on a local disk (WAL is unsafe on a network/synced
  filesystem — separating the `.db` from its `-wal`/`-shm` corrupts it; keeping the DB
  in XDG state removes that hazard entirely).
- One DB, all scopes. Rows are namespaced by a `scope` column, so cross-scope queries
  are a single `SELECT` and full-text search is one FTS corpus (bm25 ranks are
  corpus-relative and cannot be merged correctly across separate indexes — one corpus
  is the only way to rank a machine-wide search honestly).
- Authority stays in the files: tk writes the file first, then the row (write-through);
  the file mtime is the arbiter, so the view cannot durably diverge. Damaged or deleted,
  the DB rebuilds from the files. A schema change is a rebuild, not a migration — bump
  `schema_version`, drop, repopulate; no `ALTER`, no dead columns (the beads failure
  this avoids).

DECISION (owner hard requirement): SQLite is the v1 index, not contingent on any later
component. It stands on v1's own query surface: one machine-wide FTS5 corpus so cross-scope
`tk search` ranks honestly (bm25 is corpus-relative and cannot be merged across separate
indexes), `WITH RECURSIVE` for transitive `depends`/rollup traversal, and ad-hoc
`tk query` — capabilities a per-command in-memory scan can only hand-roll piecemeal, and
never as one durable store. That the corpus is "only tens of files" today does not unwind
the choice: the store, not the row count, is the point, and the write-through/reconcile/
`edges` plumbing is woven through the write path, so landing SQLite later would be a
rearchitecture rather than an addition.

DECISION (v1 concurrency): WAL mode and a connection `busy_timeout` are on from day one —
cheap, correct defaults even for a single writer (crash-safe commits, future readers).
v1 has one intentional writer class: the CLI process (command-scoped reconcile and
write-through). Concurrent CLI invocations serialize on SQLite's single-writer lock;
`busy_timeout` makes contention wait rather than error immediately. Locked default:
`BUSY_TIMEOUT_MS = 5000` (5 seconds) on every connection — enough for a contending CLI
reconcile+write on local disk; after that, fail with a clear database-busy error rather
than hanging an agent for tens of seconds. Not a user-facing config knob in v1 (constant
in code / tests). No v1 multi-writer protocol beyond that (no file-watch reconcile
daemon, no viewer write path, no cross-process ownership of reconcile).

The planned tk viewer — a web-based project monitor as a second, long-lived process — is a
real future consumer the same machine-wide DB already fits, and it reinforces SQLite
without carrying the v1 choice. Viewer design is deferred: when it lands it will need its
own change-observation (file watcher or poll) and an explicit second-writer design
(how its reconcile coexists with CLI reconcile). Until then do not invent that protocol
in the CLI. Even if the viewer never ships, the v1 query surface above already earns
SQLite.

Alternatives rejected: scan-only, and a gob/json snapshot cache. Both serve simple reads
but neither provides FTS5 search, ad-hoc SQL, or `WITH RECURSIVE` dependency/rollup, and
neither is a durable store a second process can later attach to — each would have to be
rebuilt into SQLite the moment the query surface or a viewer pressed on it.

### Invalidation and reconcile

DECISION: tk reconciles the index at the start of each command, scoped to what the
command reads (`tk next` in `wc` reconciles only `wc`; a cross-scope query reconciles
all registered scopes it reads). Git-free — reconcile never runs a git subprocess.

Two layers:
1. mtime + size per file. The DB stores each file's nanosecond mtime and size; reconcile
   stats the scope's dir root and **immediate children only** of its one `archive/`
   subdirectory (no recursive walk under `archive/` — nested paths are residue), reparses
   only files whose `(mtime, size)` changed, deletes rows for files gone from disk,
   indexes new files.
   A file moved between dir root and `archive/` (terminal layout) is re-keyed by its
   (unchanged) id and flagged `archived` when under `archive/`, not treated as a deletion,
   so the record survives the move. The last-index timestamp is
   stored and any file with `mtime >= that` treated as dirty (git's racy-index rule),
   closing the same-tick hole. A reparse that fails is quarantined, not fatal: reconcile
   writes a minimal error-row — id from the filename prefix, a `parse_error` flag with
   the parser message, `(mtime, size)` recorded so a fix re-indexes it, raw body still
   FTS-indexed (so `tk search` may surface the file — see Query surface search /
   `parse_error` DECISION). The project stays **locatable for repair**: `tk get` prints
   the absolute path, **exit 0**, with `parse_error:` on stderr (path hand-off succeeds
   even when the header is broken — see CLI `tk get` DECISION), `tk meta` prints
   extractable raw frontmatter when possible (or non-zero when FM is wholly unparseable),
   `tk doctor` lists it, a terse `N unparseable` warning rides affected reads — rather
   than being silently dropped or triggering a scope-wide rebuild loop.
   DECISION (body-aware conflict markers vs `parse_error`): git conflict markers are
   **not** a whole-file quarantine signal. Parse splits frontmatter fence from body first;
   then:
   - **Frontmatter:** if the FM fence is missing/broken, YAML fails, or conflict markers
     (`<<<<<<<`, `=======`, `>>>>>>>` lines) appear **inside** the frontmatter block →
     `parse_error` quarantine (same as malformed YAML / unquoted-`order` coercion).
   - **Body only:** after a successful frontmatter parse, conflict markers confined to the
     body do **not** set `parse_error`. Index the project from clean FM (status, order,
     edges, tags, …) exactly as for a normal file. Body text is not trusted for agent
     work until markers are gone — skill Conflicts and Merge layer 4 already require
     stop-and-report / human resolve, not auto-pick.
   Rationale: layer 4 deliberately field-merges FM and leaves markers only in the body so
   the index can read the project mid-handoff; treating any marker as `parse_error` would
   contradict that. Host-repo markers that land in FM still quarantine (unparseable
   metadata). **Complete-state mutators refuse** only while the file is in `parse_error`
   quarantine (`status`, `order`, `next --claim`, and any other verb that rewrites
   trusted frontmatter): non-zero exit, no file write, stderr `parse_error:` (same token).
   They need a parseable frontmatter document to change one field safely; partial write
   over broken FM or markers inside the FM fence is out of budget. Body-only markers do
   not block `status` / `order` / `next --claim` by parse quarantine — on auto-commit
   scopes mid-rebase, those verbs still refuse via the mid-rebase freeze (see Sync model),
   not via `parse_error`. Recovery for
   true quarantine: open the path from `get` (file tools / `$EDITOR` / human fix), fix the
   file, next reconcile clears the flag when parse succeeds. `edit` may still open
   `$EDITOR` on the path (human convenience) — it does not rewrite frontmatter itself.
   Mid-body-conflict board behaviour (no `parse_error`): `list` / `next` / `search` use
   FM-derived fields as usual; the file may still appear on the board from its status.
   Agents must not treat body prose as resolved work while markers remain (skill Conflicts).
   An unreachable dir (unmounted drive, deleted path, permission/I/O error) is
   likewise isolated to its own scope, not escalated: reconcile cannot stat/open the
   directory, so it skips that scope, leaves its existing rows in place (a transient
   unmount must not drop rows a remount would restore), and rides a terse
   `unreachable_scope:` warning on affected reads. **One token** for every “dir not
   usable” failure mode — v1 does not split “gone forever” vs “transient unmount” into
   separate machine tokens (that distinction is unreliable across mounts/network FS and
   the agent action is the same: report, do not auto-forget). Doctor may include the OS
   error string for humans. It is not a full-rebuild trigger. Bare `tk doctor` **reports**
   the same condition and does **not** drop index rows — diagnose-by-default; wiping the
   cache on a routine doctor run would punish transient unmounts and invite mistaken
   `forget`. Stale rows for an offline scope are acceptable (derived; may be slightly
   wrong until remount). When the path is reachable again, the next reconcile refreshes
   or repopulates that scope from disk. Permanent removal of a gone registration is only
   `tk scope forget` (drops registry, lens, and index rows). No
   `tk doctor --drop-unreachable-index` flag — forget is the deliberate exit.
2. Full rebuild. DB missing, failing an integrity check, `schema_version` mismatch, or an
   error reading the DB itself -> walk the registry and repopulate every reachable scope
   from its dir (an unreachable scope is skipped, per layer 1, not allowed to error
   the rebuild). Layer 1 is the optimization; layer 2 is always safe (derived). Neither a
   per-file parse failure nor an unreachable dir is a rebuild trigger — both are
   handled in layer 1, so one bad file or one offline scope never taxes a machine-wide
   command with a full rebuild.

Write-through: a `tk` mutation upserts its own row right after writing the file
(including `tk create`, so a just-scaffolded project is queryable before its body exists).
Direct agent edits are the read-through half, caught by reconcile via mtime.

DECISION: after reconcile (still git-free, still no file writes), tk runs cheap index
queries over the scopes just reconciled for integrity signals: duplicate project ids,
equal `order` keys within a scope, and archive layout drift (terminal at root /
non-terminal under `archive/`). Cost is a few aggregates over already-materialized rows
— not a re-stat or re-parse of the dir. Hits surface as a terse warning on the command
(stderr) using the stable tokens `duplicate_id:` / `equal_order:` /
`archive_non_terminal:` / `archive_terminal_at_root:` (see Agent skill contract, Doctor
and integrity warnings); they do not auto-repair. File-mutating repair stays on `tk sync`
(auto-commit integrity step) and `tk doctor --repair` (the off-sync path available for
every autoCommit mode — mutating flags need ambient / `TK_SCOPE` / `--all`, never silent
machine-wide; see doctor scope selection). Rationale: the read path must stay free of
multi-file rewrites (a `tk next`
must not rename projects), while plain-files multi-machine still learns about collisions
and layout drift without waiting for a sync that does not exist. See "Project ids",
"Metadata", and "Done and archive".

DECISION: `tk doctor` is diagnose-by-default. Bare `tk doctor` (and `tk doctor --reindex`)
never mutates project files or `tk.cue`. It reports every integrity class (text on
stderr/stdout; stable tokens where defined). File-mutating integrity is opt-in.

DECISION (doctor scope selection, v1 closed):
- Doctor is a **discovery command**: no ambient scope is not an error for **report**.
- **No `--scope` flag** on `tk doctor` in v1. Do not invent `tk doctor --scope S`.
- **Ambient resolution** for doctor uses the same precedence as other ambient verbs
  **minus the `--scope` flag**: `TK_SCOPE` env, then longest-prefix code-root of cwd.
  Doctor is the deliberate exception to the optional-`--scope` DECISION under Resolution
  (ordinary ambient verbs accept `--scope`; doctor does not).
- **Report target set** (bare `tk doctor`, and non-mutating parts of a doctor run):
  - Ambient resolved → report **that scope only**.
  - No ambient → report **every registered** scope (machine-wide discovery).
- **Mutating target set** (`--repair`, `--re-space-order` only — not bare report):
  1. `--all` present → act on **every registered** scope (explicit machine-wide mutation).
  2. Else ambient resolved (`TK_SCOPE` or cwd code-root) → act on **that scope only**.
  3. Else → **usage error** (exit 2): mutating doctor requires ambient, `TK_SCOPE=<name>`,
     or `--all`. Message names all three. Never silently rewrite every scope.
  When both ambient and `--all` are present, `--all` wins (operator asked for machine-wide).
- **One non-ambient scope without machine-wide repair:** set `TK_SCOPE=<name>` for the
  invocation, or `cd` under that scope's code-root so longest-prefix ambient resolves.
  Example: `TK_SCOPE=wc tk doctor --repair`. Machine-wide: `tk doctor --repair --all`.
- `--reindex` rebuilds the machine-wide index from files regardless (derived cache); it
  does not mutate project files. When combined with ambient-scoped report/repair, reindex
  still covers the whole DB after the scoped file work.

- `tk doctor --repair` — run the same automatic repairs as the auto-commit `tk sync`
  integrity step: offline-concurrent id collision (deterministic extension; inbound
  edges never rewritten, surfaced via `edge_verify:`), equal-`order` re-space (tied
  files only), and **archive layout**
  (terminal projects under `archive/`, non-terminal at dir root — both directions; see
  "Done and archive"). Identical bit-identical procedures where applicable. Idempotent
  re-entry after a crashed partial repair (multi-file rewrite durability). Scope set per
  mutating target set above. Does not run pathological long-`order` band re-space
  (that is `--re-space-order` only). Mid-rebase (auto-commit + git-root mid-rebase):
  **refuse** with the same class of error as complete-state mutators — no multi-file
  rewrite / self-commit onto temporary HEAD (mid-rebase command classes DECISION).
- `tk doctor --re-space-order` — explicit local-band re-space for pathologically long
  `order` keys (soft threshold length > 64, or any keys the implementation selects in
  the reported band). Never combined silently into `--repair`. Same mutating target-set
  rule as `--repair`. Same mid-rebase refuse as `--repair`.
- Durability of mutations: when the affected scope is auto-commit and a git-root exists,
  each repair batch self-commits the touched files (fixed messages, e.g.
  `tk: repair duplicate id <old> -> <new>`, `tk: repair equal order`,
  `tk: repair archive layout <id>`, `tk: re-space order`)
  — same class as `status`/`order`/`next --claim`, no push. Without a git-root, files are written and
  `sync_disabled:` may ride if auto-commit planned. Non-auto-commit: write files only
  (host or plain-files durability).
- `--reindex` — full index rebuild from files; never touches the files. Combinable with
  bare doctor; not a repair flag. Does not require ambient or `--all`.
- `--repair` and `--re-space-order` may be combined with `--reindex` (reindex after
  repair is wise); both mutating flags may be passed together when the operator wants
  both classes in one invocation. Either still requires ambient / `TK_SCOPE` / `--all`.
Rationale: agents are told to run doctor on warnings; diagnose-by-default prevents
surprise multi-file renames; `--repair` is the explicit off-sync twin of sync integrity;
self-commit on auto-commit closes dirty-repair holes; machine-wide **mutation** is
opt-in via `--all` (or intentional ambient/`TK_SCOPE`), never the no-ambient default —
without inventing `doctor --scope`.

### Query surface

- `tk search <terms> [--scope S]` — full-text over titles and bodies via FTS5 (bm25;
  phrase/prefix/boolean). Machine-wide by default, `--scope` to bound. DECISION (search
  stdout, v1 closed): find-then-open contract for agents.
  - **Terms:** one argv remainder after flags (quote multi-word); after trim must be
    non-empty → else usage exit 2. FTS5 query syntax as supported by the index (phrase /
    prefix / boolean per SQLite FTS5); no separate flag surface in v1.
  - **Scope:** default every registered scope; `--scope S` bounds to that scope only.
    No lens. No status filter flags in v1 (search is not the board — use `list` for
    status cuts). Includes terminal projects under `archive/` and all statuses.
    DECISION (search and `parse_error`): search may return quarantined hits — FTS keeps
    the raw body so repair stays discoverable. For a `parse_error` row: `status` TSV
    field is **empty** (frontmatter untrusted; do not invent a synthetic status name);
    `absolute-path` still filled so the hit is openable; `title`/`summary` may be empty
    or best-effort from extractable fragments. Open via the path field or `tk get`
    (exit 0 + path under quarantine). Treat body as untrusted until parse succeeds;
    mutators still refuse. Body-only conflict markers (no quarantine) appear as normal
    hits from clean FM — still do not treat body prose as resolved work (skill Conflicts).
  - **Order:** bm25 rank descending (best first); tie-break full id ascending for stable
    output. No score column on stdout (order is the rank signal; scores are corpus-
    relative and noisy for agents).
  - **One line per hit**, tab-separated fields (parse-stable; empty fields stay empty,
    still tab-delimited):
    ```text
    <full-id>\t<status>\t<title>\t<summary-or-empty>\t<absolute-path>
    ```
    - `full-id`: always `<scope>-<short-id>` (even when `--scope` bounds).
    - `status`: frontmatter status as stored.
    - `title`: body H1 via the shared title-extraction helper (list/meta/search); empty
      string if none.
    - `summary`: frontmatter `summary` if set, else empty (never fall back to title/slug).
    - `absolute-path`: cleaned absolute path (same path hand-off rules as `get`).
  - **Empty result:** exit 0, empty stdout (not an error). Non-zero only for usage /
    failed reconcile class errors; then empty stdout + stderr message.
  - **Duplicate id rows:** board/search may surface both rows (same post-reconcile
    `duplicate_id:` stderr warning); each line carries its own path.
  - Pure read; git-free.
- `tk query <sql>` — **read-only** SQL over the index for ad-hoc inspection. The index is
  ephemeral and derived: it is **not** the source of truth. v1 accepts only read-only
  statements (`SELECT`, and read-only utilities such as `EXPLAIN` / `PRAGMA` that do not
  mutate). Reject `INSERT`/`UPDATE`/`DELETE`/`DROP`/`ALTER`/… (and any multi-statement
  batch that includes a write) with a clear error: durable change is files /
  `tk doctor --repair`, not the local DB. Rationale: index mutation cannot update project
  files, confuses agents, and vanishes on reconcile/rebuild — a footgun with no durable
  upside. Power users who need to poke the file may open
  `${XDG_STATE_HOME:-~/.local/state}/tk/index.db` with an external `sqlite3`; the CLI does
  not invite that path. The schema is explicitly not a stable API: derived, rebuilt on
  any `schema_version` bump, may reshape between releases with no migration. `--help`
  says so; `tk query --schema` prints the current shape. Not for saved queries or
  external tooling. DECISION (query audience): debug / human ad-hoc only — not an agent
  automation contract. Agents prefer `deps` / `list` / `search` / `next` / `get` / `meta`
  (stable path + short text). Do not script against schema shape across releases.
- Rich `tk list` filters (status union positionals against the single target scope's
  known statuses — built-ins plus that scope's CUE customs; `--tag`, `--scope`, `--all`,
  `--no-lens`) compile to index queries. No date filters on list in v1 — use
  `tk query` for ad-hoc date cuts.
- Dependency and rollup queries — transitive `depends` via `WITH RECURSIVE`, counts by
  status/scope — come from the index. The first-class CLI for edge inspection is
  `tk deps` (see "Status and dependencies" and "CLI surface"); `tk query` remains the
  ad-hoc escape hatch (schema not stable — agents prefer `deps`).
- `depends` and `related` are materialized as rows in one shared `edges` table
  (`from_id, from_scope, to_id, to_scope, kind`, `kind` in `depends|related`), populated
  by reconcile from frontmatter. `from_id` and `to_id` are always full project ids
  (`<scope>-<short-id>`); short-only frontmatter entries never enter the table (doctor
  hard — see Status and dependencies). One table backs `tk deps` (direct and transitive
  walks, reverse lookup), `WITH RECURSIVE` for ad-hoc `tk query`, and the planned
  viewer's project graph. Cross-scope edges are just rows where `from_scope != to_scope`
  — one machine-wide index, no special casing. An edge whose `to_id` matches no project
  row (unregistered scope, or a not-yet-synced target) is kept as a dangling row so the
  viewer can render it as an external node and `tk doctor` can surface the unresolvable
  ones.

WAL mode and `BUSY_TIMEOUT_MS = 5000` from day one (see v1 concurrency DECISION above).
v1 writers are CLI processes only; SQLite's single-writer lock plus that timeout covers
concurrent CLI invocations. A future viewer that both reads and writes is out of v1
scope — its second-writer coordination is designed when that process exists, not as
latent CLI complexity today.

## Sync model

Applies only to scopes with `autoCommit: true` (tk-driven). Repo-driven scopes
(`autoCommit: false` inside git) ride the surrounding repo's own git (human/PR-managed);
plain-files scopes (`autoCommit: false` outside git) do no syncing.

DECISION: durability and sync split along the commit/push seam.
- Durable-local: a mutating command commits its own change synchronously to the scope's
  repo; a direct agent/human edit is committed at the next `tk sync`. Work is never lost
  and no command blocks on the network.
- Remote sync: happens only in `tk sync`, whose push is synchronous and reported.
  Ordinary commands never push.

### Reads never touch git

DECISION: read commands (`next` without `--claim`, `list`/`get`/`meta`/`deps`/
`search`/`query`) are git-free. A read reconciles the index from the files and answers;
it does not commit, push, or run any git subprocess. A direct agent edit is reflected
because reconcile stats the files. (`tk next --claim` is not a read — see complete-state
writes and Status and dependencies.)
Consequence: a cross-machine read can be stale until the next `tk sync` fetch —
acceptable for a single user working mostly one machine at a time. (Repo-driven
`uncommitted:` is intentionally not on this path — see Auto-commit dirty health.)

### Writes commit their own change

DECISION: a mutating command that produces a complete state (`status` / `order` /
`next --claim`) writes the file (and, for `status` crossing the terminal boundary,
renames between dir root and `archive/` — see "Done and archive"), write-throughs its
row, then — when git self-commit is available — commits the touched path(s) (`git add`
the new path and any removed path + `git commit -m "tk: wc-ab2c -> done"`). Adding the
specific path(s) (not `-A`) leaves unrelated dirty files untouched. Synchronous, tens of
milliseconds, no push. The `-> done` example is the terminal `status` case — `status` is
the only complete-state verb that crosses the archive boundary (rename); `order` and
`next --claim` rewrite a single in-place path, message class `tk: <id> order` and
`tk: <id> -> in-progress` respectively.

DECISION (autoCommit true, git not ready): `autoCommit: true` means tk *owns* the commit
path when a git-root exists; it does not require git at every write. Init may set
`--auto-commit` outside git ("planned" repo). Complete-state mutations always:
1. write the file and write-through the index (exit 0 on success of that write);
2. attempt self-commit only if a git-root is derived from `dir` (`git rev-parse
   --show-toplevel` succeeds). Self-commit does **not** require an upstream — local
   commits are fine; only `tk sync` needs a remote.
3. if no git-root: skip the commit without failing the mutation; emit a terse stderr
   line with stable token `sync_disabled:` (files landed; no git durability until the
   user creates a repo with plain git). Never `git init` / never invent a remote.
4. Mid-rebase refuse and git-root flock apply only when a git-root exists; without one
   there is no rebase state to corrupt and no commit span to lock.
When a git-root exists but has no upstream, complete-state mutations **do** self-commit;
`tk sync` alone reports `sync_disabled:` until upstream exists (same token, sync path).
Once the user adds git (and later upstream), the next write that can commit does so and
the next ready `tk sync` runs the full path. Distinct from `autoCommit: false` outside
git (plain files): that mode never self-commits and never runs `tk sync` by policy;
planned auto-commit without a git-root is temporarily commit-skipped with
`sync_disabled:` on writes until a git-root exists, then complete-state verbs get full
local self-commit and (with upstream) full sync — **`tk create` remains non-committing**
in every mode (scaffold rule above).

`tk create` is the deliberate exception to the complete-state self-commit rule: it always
scaffolds frontmatter plus an H1 from the title argument and returns the path for the
agent to fill the rest of the body directly. The artifact is body-incomplete by design
and **never self-commits**, regardless of the optional status positional (including
terminal `done` / `cancelled` / custom done-class). Writing the skeleton reserves the id
and gives list/search a title; git durability is the next `tk sync` snapshot (when
auto-commit git is ready) or the host commit (repo-driven). Principle: self-commit when
the verb yields a complete state and git self-commit is available (`status` / `order` /
`next --claim` only); defer to sync when the verb is `create` (always a scaffold); never
block the file write on missing git for auto-commit scopes.

Concurrent writes in a scope serialize through a scope-level `flock` on
`<dir>/.tk.lock`, held for the whole reconcile -> write span. `tk create` takes it too
(without committing) so its draw -> check-local-ids -> write-skeleton is atomic and two
concurrent adds cannot draw the same id. `tk next --claim` likewise holds the flock across
its whole select-and-write span (see the claim procedure DECISION) so no other writer can
race between candidate selection and the `in-progress` write. Because a scope's ids and files live under its
own dir, this per-scope lock is sufficient for the file write and id draw even when
several scopes share a repo.

The git commit is the part that is repo-granular, not scope-granular: `git add`/`commit`
mutate the one shared index of the derived git-root, so for auto-commit the committing span
additionally takes a **git-root lock** (path under XDG state — see DECISION below). Two
scopes in the same repo therefore serialize their commits (and `tk sync`'s rebase/push)
instead of corrupting each other's index, while non-auto-commit never commit and need only
the per-dir lock. The locks cannot coordinate every index writer (a read command
reconciles without the git-root lock; concurrent CLI processes each open the DB), so index
cross-writer coordination is SQLite's single-writer WAL lock plus `busy_timeout`, not the
flock. A viewer process is not a v1 writer.

DECISION (git-root operational state — XDG, not under `.git/`):
- tk **does not create or write third-party files under `<git-root>/.git/`**. That tree is
  Git's namespace. tk may **read** Git-owned paths (e.g. mid-rebase via
  `.git/rebase-merge` / `rebase-apply`, `rev-parse`, status) but must not invent lockfiles,
  markers, or a `.git/tk/` directory.
- Per-repo operational state for auto-commit lives in machine-local XDG state, keyed by
  the cleaned absolute git-root path:
  ```text
  ${XDG_STATE_HOME:-~/.local/state}/tk/git-roots/<key>/
  ```
  - `<key>`: lowercase hex SHA-256 of the cleaned absolute git-root path (UTF-8 bytes of
    the absolute path string after `filepath.Clean` / equivalent). Full 64 hex chars;
    stable for the same path on the same machine.
  - `sync.lock` — flock target for the auto-commit commit/rebase/push span (same role as
    the former `.git/sync.lock` idea).
  - `last-push-error` — last failed push marker for doctor / write-command warnings; plain
    text detail; cleared on the next successful push.
- Create the directory on first need. Never committed (outside any work tree). Not the
  rebuildable project index; not project metadata. Orphan dirs after a moved/deleted repo
  are harmless residue (no v1 GC required).
- Same local-disk preference as `index.db` (not on NFS/synced FS when practical).

DECISION (mid-rebase command classes — auto-commit + git-root only): when the derived
git-root is mid-rebase (a stat of `.git/rebase-merge`|`rebase-apply`), classify every
CLI entry. "Mutating command" in this design means the **self-commit / complete-state
path** class below — not every verb that can open a path. The freeze protects git's
temporary HEAD from tk-authored commits; it is not a ban on human/agent file repair.

| Class | Examples | Mid-rebase (auto-commit scope on that git-root) |
|---|---|---|
| Self-commit / complete-state | `status`, `order`, `next --claim`; any future verb that self-commits or rewrites trusted project/config as a complete-state write | **Refuse** at startup |
| Scaffold write (no self-commit) | `create` | **Refuse** — still a new path under the scope dir during an inconsistent base; not a repair path |
| Path open / direct-edit convenience | `edit` | **Allow** — resolve id, open `$EDITOR` only; no self-commit (same durability class as agent file tools and raw editor on a `get` path). Humans use this (or file tools) to clear body markers and `status_conflict` |
| Reads / diagnose | `list`, `next` (no `--claim`), `get`, `meta`, `deps`, `search`, `query`, bare `tk doctor`, `skill`, `help`, scope list | **Allow** |
| Sync resume | `tk sync` | **Allow** to resume after resolution (refuses `rebase --continue` while `status_conflict` remains, or while a delete/edit path still stands unactioned — see the delete/edit DECISION in Merge conflict handling; body markers resolved by human before continue) |
| Integrity rewrite | `tk doctor --repair`, `--re-space-order` | **Refuse** — same class as complete-state (multi-file rewrite and/or self-commit must not land on temporary HEAD). Bare doctor report still **Allow**. Resume path: resolve body markers / `status_conflict`, `tk sync`, then repair if still needed |

Non-auto-commit never self-commits, so their complete-state verbs keep writing files even
when the surrounding host repo is mid-rebase (a host monorepo mid-PR-rebase must not
block `tk status`). Conflict markers that land in a project file are handled by the
body-aware parse rule in Invalidation (FM markers → `parse_error`; body-only markers
index from FM, no quarantine), not by freezing non-auto-commit writes. For auto-commit
the refuse fails fast, naming the scope and file that paused the rebase so the block is
legible even from a sibling scope: `teamA-ab2c is mid-sync-conflict in shared repo
<git-root> — resolve <file> then run tk sync`. Reads are git-free and unaffected for
every scope.

TRADEOFF (accepted): this mid-rebase refusal is repo-granular among auto-commit scopes,
not scope-granular, and that is the one place the per-scope isolation the design
otherwise holds does not reach. A paused rebase is git state on the shared `.git`, so a
conflict left unresolved in one auto-commit scope freezes the **self-commit path** for
every sibling auto-commit scope sharing that git-root until the rebase resolves — the
same coupling that makes "one `tk sync` pushes the whole repo" convenient. Path-open
(`edit`) and reads stay allowed (command classes table). It does not freeze
non-auto-commit scopes (they share no self-commit path with the paused rebase). It is
bounded (reads stay git-free and fully available for every scope, including the
conflicted one; the freeze ends the moment the rebase is resolved and lasts only while
a human leaves it paused) and it never risks data (the refusal is fail-fast, not a
silent degrade). But it means the multi-scope-per-repo layout the design recommends for
auto-commit (a central tk repo of siblings) couples self-commit availability at the repo
level during a conflict — same repo-level coupling as an unparseable sibling `tk.cue`
refusing `tk sync` for the whole git-root (see "Configuration"), and distinct from a
malformed `tk.cue` or an unreachable dir for ordinary per-scope mutations, which still
isolate to their own scope. The error naming the blocking scope and file is what keeps
it from being a mystery to whoever hits it from another scope. A scope that must never
be frozen by a sibling's conflict or broken config belongs in its own repo (a different
git-root), the same remedy the autoCommit-consistency rule already points at.

### tk sync: the sole push boundary

DECISION: `tk sync` is the "done for now / reconcile now" command and the only place tk
pushes. It targets the ambient scope's repo; `tk sync --all` syncs every auto-commit
scope, and with no ambient scope `tk sync` syncs all. Because sync is repo-granular, both
the ambient case and `--all` operate per derived git-root, deduplicated: syncing the
ambient scope syncs its whole repo (every scope sharing it), and `--all` visits each
git-root once rather than re-fetching a shared repo per scope. Ambient-only is deliberate:
it matches the end-of-turn pattern (push where you worked), keeps the hot path to one repo,
and `--all` covers the world when wanted. It is bidirectional by construction — always
fetch, then push only if ahead — because reads are git-free, so sync is the sole point a
machine learns of another's work. Steps:

Caveat, cross-scope freshness: because bare `tk sync` fetches only the ambient scope, a
cross-scope `depends` target living in another auto-commit scope is only as fresh as that
scope's last fetch on this machine. Its status can lag until that scope is synced
(`tk sync --all`, or syncing it directly). This is the same "a cross-machine read can be
stale until the next sync" limitation reads already accept — documented here so a
cross-scope gate reading a stale target is a known bound, not a surprise. Not worth
splitting sync into ambient-push/all-fetch in v1.

1. Snapshot: `git status --porcelain -- <dir>...` — scoped to the registered
   auto-commit dirs sharing this git-root, never the whole working tree, and never a
   co-located non-auto-commit scope's dir even when it sits under the same git-root —
   then stages every dirty path that matches the scope-file allowlist (below) and makes
   **one git commit for the whole snapshot** on that git-root (not one commit per file).
   Message: a single summary, e.g. `tk: sync <n> path(s)` or, when a single path,
   the same class-specific form as below (`tk: add <id> <slug>`, `tk: edit <id>`,
   `tk: remove <id>`, `tk: config <scope>`, …). Direct edits, `$EDITOR` edits, and filled `create`
   skeletons are included when they are allowlisted project files. Rationale: complete-
   state verbs (`status` / `order` / `next --claim`) already self-commit their touched
   path(s) each;
   snapshot is the end-of-turn scoop of leftover dirt — batching avoids N tiny commits
   and rebase noise after a long agent turn. Per-file messages remain available when the
   scoop is a single path.
   DECISION: snapshot allowlist (closed for v1; expand only when a new first-class
   scope file is designed in). A dirty path under a scanned dir is committed only if it
   is one of:
   - A project `.md` at the dir root or as an **immediate child** of `archive/` (not
     nested deeper — see Storage archive nesting depth DECISION), whose basename matches
     the project filename shape (`<id>-<slug>.md`). The id prefix is a legal scope name,
     `-`, and a short-id of length 4 through `SHORT_ID_MAX` (8) inclusive (create always
     4; longer only after id-collision repair — see "Project ids"), first character a
     letter from the short-id alphabet, remaining characters from that alphabet, then
     `-` and the frozen create-time slug matching the closed slug grammar
     (`^[a-z0-9]+(-[a-z0-9]+)*$`, length 1–48; see Project ids `slugify`). Message:
     `??` -> `tk: add <id> <slug>`, modified -> `tk: edit <id>`,
     deleted -> `tk: remove <id>`.
     Parseability of frontmatter is not required to commit (an unparseable project
     file still needs to travel; reconcile already quarantines it as `parse_error`).
     DECISION (deleted allowlisted path): `git status --porcelain` reports a removed
     project file as dirty; the snapshot stages that deletion like any other dirty
     allowlisted path and commits it as `tk: remove <id>` (id parsed from the deleted
     basename), so the committed tree converges with disk. The files are the source of
     truth: refusing to commit a hand-deletion would leave permanent dirt that every
     later sync re-warns on. The reconcile row deletion already drops the project from
     the index, so nothing else is needed. This is not a sync-time delete guard —
     "never delete — the record persists" stays the agent/skill instruction it already
     is (see "Done and archive"); sync mirrors whatever the human left on disk, it does
     not author deletions or police them.
   - `tk.cue` at the dir root. Message: `tk: config <scope>`. Must sync so a second
     machine validates against the same schema.
   - `.gitignore` at the dir root (written by `tk scope init` for `.tk.lock`). Message:
     `tk: gitignore <scope>`.
   Explicit non-members (never committed by tk, even if dirty under the dir):
   - `.tk.lock` (also gitignored; skipped defensively).
   - `AGENTS.md` (any path under the dir, including dir root) and any other human handoff
     / agent bootstrap docs — host-managed; not a first-class scope file. tk never
     auto-writes them and never auto-commits them (repo-root `AGENTS.md` is outside the
     scanned dir entirely and is always host-only).
   - Any other path: vendor conflict copies, editor swap files, secrets, dumps,
     nested trees, `archive/` non-project files, random `.md` that does not match
     `<id>-<slug>.md`, and anything else. These are non-allowlist residue.
   Residue handling: leave uncommitted and unstaged; do not delete; emit a terse
   stderr warning naming each path, prefixed with the closed token (`non_allowlist: N
   path(s) under <dir> not committed — move or remove; see tk doctor`). `tk doctor` lists the same residue
   for human cleanup (same class as external-sync conflict-copy names). Sync still
   proceeds for allowlisted dirty files and continues to fetch/integrate/push — residue
   is a hygiene warning, not a hard stop (a hard stop would block legitimate work when
   a conflict-copy or editor junk is present). There is no `tk sync --force-unknown`
   in v1: unknown bytes never ride the auto-commit push path. Blast radius accepted
   only for the allowlist itself (project bodies and config can still hold secrets if
   the author puts them there — ordinary git discipline).
   TRADEOFF: catch-all "commit everything under the dir" was rejected. Dir disjointness
   still prevents sweeping another scope's files or unrelated repo source outside the
   dir; it does not prove every byte inside the dir is safe to publish. "tk-only" is
   therefore enforced by membership, not by trusting the directory label.
   Scoping the snapshot to the auto-commit dirs remains what makes the repo-wide push
   safe against non-tk trees: such a dir is disjoint from every other scope's dir (the
   disjointness invariant enforced at registration; see "Scope lifecycle"), so an
   allowlisted path inside it cannot be another scope's file, while anything outside
   every auto-commit dir — unrelated source in a shared repo, a co-located
   non-auto-commit scope's tree — is never touched. The disjointness invariant is what
   forbids the one case that would break this — a sibling scope's dir nested inside
   this one, whose files a recursive `git status` would otherwise sweep under the wrong
   scope. A repo holding several tk scopes snapshots the union of their auto-commit
   dirs, so "one `tk sync` pushes the whole repo" still means every auto-commit scope
   in it (allowlisted paths only), not the non-tk remainder.
   Crucially, the snapshot's candidate dirs are defined by autoCommit, not by the
   autoCommit-consistency invariant continuing to hold: the safety does not assume
   every scope under this git-root is auto-commit. That invariant (enforced at init; see
   "Scope lifecycle") keys on a git-root that is derived at runtime, so a later
   git-topology change — a `git init` at a parent, a moved dir, a new remote — can bring
   a non-auto-commit scope under an auto-commit scope's git-root after both were
   registered. Sync must therefore not sweep by git-root membership alone. As a
   preflight, `tk sync` re-derives the git-root of every scope sharing this root and
   refuses to proceed if (a) any of those scopes has an unparseable `tk.cue` —
   autoCommit unreadable, same fail-closed class as a mismatch; see "Configuration" —
   or (b) their declared autoCommit values disagree (`scope <x> (autoCommit false)
   shares this git repository with auto-commit scopes — split it into its own repo or
   re-declare autoCommit`), rather than pushing under a silently violated or
   unverifiable invariant; `tk doctor` runs the same per-git-root checks off-sync
   (unparseable sibling + autoCommit divergence) and flags both.
   The index lives in XDG state; the scope lock is covered by the `.gitignore` that
   `tk scope init` writes into the dir, and the snapshot skips `.tk.lock`
   defensively regardless — so neither ever appears here.
2. Fetch and integrate, unconditionally. Always fetch; if the remote advanced, rebase
   local commits onto it, running the frontmatter merge on any conflicted file. Conflicted
   `tk.cue` files are resolved before any project `.md` field-merge in the same integrate
   (see "Merge conflict handling") so custom-field typing uses one post-integration schema.
   This runs whether or not step 1 produced a commit, so a read-only machine still pulls
   others' work. An unresolvable body conflict leaves the store in a paused rebase,
   marked and reported, never discarded — nothing is pushed until it resolves, auto-commit
   self-commit / complete-state verbs refuse meanwhile (mid-rebase command classes), and
   a later `tk sync` resumes the paused rebase.
3. Integrity repair over the merged tree, per scope touched: duplicate ids, tied
   `order` keys, and archive layout drift — offline-concurrent artefacts and hand-edit
   residue. For auto-commit, sync owns the automatic repair here (rename the loser per
   the deterministic pick in "Project ids"; never rewrite `depends`/`related` — every
   inbound edge to the collided id is reported as `edge_verify:` in the sync output;
   re-space tied `order` keys; move terminal projects into `archive/` and non-terminal
   projects out to dir root) rather than deferring to `tk doctor --repair`. Both write
   only the files they touch and commit under a fixed message. (Detection of the same
   conditions is cheaper and universal — every command's post-reconcile index check, all
   scopes — and never mutates files; see "Invalidation and reconcile". Non-auto-commit
   repair only via `tk doctor --repair`.)
4. Push synchronously (blocking) if ahead. Step 2 already integrated the remote, so an
   ordinary sync fast-forwards; a reject means the remote moved in the fetch->push race,
   handled by looping to step 2 once more. A sync with nothing to push (a read-only
   machine) skips the push — it already pulled in step 2.
5. Report unpushed count, conflicts, repairs, and any non-allowlist residue warnings.

DECISION (`--all` per-git-root failure isolation): under `--all` the git-roots are
independent units of work. Visit every eligible root, isolate each root's outcome, and
continue past a root that could not complete rather than returning at the first error. A
refused preflight, a `sync_disabled:` root, a rebase paused for a human, and a failed push
each report their own line against the root they belong to, and the run moves on; the
closing report names which roots synced and which need attention. Exit non-zero when any
root ended needing a human or a retry; exit 0 when every visited root either synced or had
nothing to do. Rationale: one stale repo must not strand every other repo's push, and
aborting at the first failure would make *which* roots got synced depend on iteration
order — an unstated ordering becoming user-visible state. This is `--all` only: an ambient
sync has exactly one root, so that root's failure is the command's failure, and the ambient
non-auto-commit refuse stays non-zero (empty-set exit 0 is unaffected — it is the case
where there was no work, not the case where work failed).

Blocking on the push (~100ms-1.5s, dropped toward ~100ms by SSH `ControlMaster` reuse)
is negligible against LLM latency and is what makes sync reliable: when it returns, the
remote has the work and any conflict has surfaced in sync's output. `tk skill` tells
agents to run `tk sync` at end of turn only for tk-driven (`autoCommit: true`) scopes —
not on repo-driven or plain-files (see Agent skill contract, End of turn). Forgetting
sync on a tk-driven scope costs a delayed push, never data. No `tk save`/`tk end` verb —
`tk sync` is that boundary for auto-commit only.

This replaces any background/detached push: such machinery is inert under an agent
harness that reaps the command's process group before a child completes, and cannot
reliably report a merge conflict from a reaped child. Blocking `tk sync` puts conflict
resolution where it can be seen.

Health: `git rev-list --count @{u}..HEAD` gives the unpushed count. A last-push-error
marker records failures for `tk doctor` and write-command warnings — pure operational
state keyed by git-root, not project metadata. Path:
`${XDG_STATE_HOME:-~/.local/state}/tk/git-roots/<key>/last-push-error` (see git-root
operational state DECISION). Cleared on the next successful push. This is distinct from
terminal-status dispute, which is recorded in the project file via `status_conflict` (see
"Merge conflict handling"), not in XDG git-root state. Before the unpushed count is
meaningful there is the precondition tk does not create — the repo itself and the
autoCommit mode:
- Non-auto-commit ambient scope (repo-driven or plain-files): `tk sync` refuses with a
  clear error naming the mode (`sync is for auto-commit scopes only — this scope is
  repo-driven; commit project files with the host repo` / `… plain-files; there is no
  tk sync — run tk doctor if integrity warnings appear`). `tk sync --all` skips
  non-auto-commit scopes (or visits only auto-commit git-roots); it does not error solely
  because other registered scopes are non-auto-commit.
- DECISION (empty auto-commit eligible set): when the selected set has **zero** scopes
  eligible to sync — `tk sync --all` or bare `tk sync` with no ambient, and every
  registered scope is non-auto-commit, or no scopes are registered — **exit 0** with a
  terse stderr note that nothing was synced (e.g. no auto-commit git-roots / nothing to
  sync). Discovery-friendly: empty fleet is not failure. Does **not** override ambient
  non-auto-commit refuse (that remains non-zero with the mode-named error). Distinct from
  `sync_disabled:` on an eligible auto-commit scope that lacks git-root/upstream (that
  path still reports the token; see below).
- Auto-commit: sync first checks the scope is a git repo with an upstream (a `.git` stat,
  then `git rev-parse --abbrev-ref @{u}`), and if not, reports sync disabled with stable
  token `sync_disabled:` and a professional message (`sync is disabled until this scope
  is a git repository with a remote; set one up with git, then tk sync`) rather than a
  raw git error. The same token rides complete-state write commands that skip self-commit
  for the same reason (see Writes). Reads stay git-free and do not carry it.

### Auto-commit

DECISION: each scope declares `autoCommit: bool` in `tk.cue`. It is a scope property,
identical on every machine, so it is synced, not machine-local. The useful control is
one bit: whether tk commits. Host vs plain-files is derived from "is the dir inside a
git repository?", not a third stored choice.

| `autoCommit` | In a git repo? | Behaviour |
|---|---|---|
| `true` | yes, with upstream | tk-driven: complete mutations self-commit; `tk sync` is the fetch/push boundary |
| `true` | yes, no upstream yet | tk-driven local: complete mutations self-commit; `tk sync` reports `sync_disabled:` until upstream exists |
| `true` | no git-root | tk-driven planned: file + index writes succeed; self-commit skipped with `sync_disabled:` on those writes; `tk sync` same token; tk never creates the repo |
| `false` | yes | repo-driven: host commits; tk never commits/pushes; detect-only `uncommitted:` on writes + doctor when allowlisted scope files are dirty |
| `false` | no | plain files: no VC (or external Dropbox/Syncthing/NFS); tk never runs git; no `uncommitted:` |

Help-text honesty: "auto-commit" means tk owns the commit path when a git-root exists, not
every keystroke and not "fail closed without git":
- `status` / `order` / `next --claim` → write always (`status` may rename root ↔
  `archive/`; `--claim` only sets `in-progress`); self-commit when `autoCommit: true` and
  a git-root exists (upstream not required); if no git-root, `sync_disabled:` and no commit
- `create` → always scaffold only (frontmatter + H1; any status including terminal;
  terminal create writes under `archive/`); never self-commits; first git durability at
  `tk sync` snapshot (when git-root exists) or host commit (repo-driven). On **terminal**
  create, emit a terse stderr note (not a closed integrity token) that the scaffold is
  not git-durable until that boundary — path under `archive/` + status label must not be
  read as a completed self-commit
- direct agent / `$EDITOR` edits (including after `tk edit`) → never self-commit on
  editor exit; committed at `tk sync` (when git-root exists) or host commit (repo-driven)
- push only in `tk sync`, never automatic; `tk sync` reports `sync_disabled:` until
  repo+upstream

When `autoCommit: false` inside git (repo-driven): a single host repo may carry many
scopes (a monorepo, one scope per team/area). tk never commits or pushes; it may run
read-only `git status` for the dirty-health signal below.

DECISION (repo-driven dirty health): tk still must not own the host commit path, but it
must not leave agents silent when allowlisted scope files are uncommitted. Detect-only:

- When a scope is repo-driven (autoCommit false and a git-root exists for its dir), after
  a complete-state write (`status` / `order` / `next --claim`) and after `tk create`
  (scaffold is disk-dirty too), and on bare `tk doctor` for that scope, run a cheap
  `git status --porcelain -- <dir>` scoped to the scope dir (same path bounding as auto-commit
  snapshot — never whole-tree).
- If any path under that dir is dirty **and** matches the auto-commit allowlist shape
  (project `<id>-<slug>.md` at dir root or `archive/`, `tk.cue`, `.gitignore` only),
  emit stable token `uncommitted:` on stderr with a short count/summary (doctor may list
  paths). Do not stage, commit, or push. Non-allowlist residue stays `non_allowlist:` /
  doctor residue, not this token.
- Pure reads (`next` without `--claim`, `list` / `get` / `meta` / `deps` / `search` / `query`) stay git-free
  and do **not** carry `uncommitted:` — avoid tax on every agent poll. Skill end-of-turn
  for repo-driven: run bare `tk doctor` (or rely on write-side warnings from the turn) and
  if `uncommitted:` is present, stop and hand host commit/PR to the human or host
  workflow — do not invent `tk sync` or `tk save`.
- If `git` is missing or status fails, skip the signal (no hard fail of the write); doctor
  may note git unavailable. Distinct from `sync_disabled:` (auto-commit only).

When `autoCommit: false` outside git (plain files): single machine, or cross-machine
handled externally. The index still serves reads; only sync is absent. Multi-machine
integrity is detect-on-reconcile + repair-via-`tk doctor --repair` (no automatic repair
seam — there is no `tk sync`). No `uncommitted:` (no git). External sync conflict-copy
filenames are doctor-flagged residue, not merged. Prefer `autoCommit: true` when
git-shaped multi-machine merge and automatic
integrity matter.

`autoCommit` is a per-repo fact: every scope sharing a derived git-root must declare the
same value (enforced at init). Because auto-commit sync operates on the git-root,
syncing any scope in a multi-scope auto-commit repo fetches/rebases/pushes that repo once
and its snapshot step commits every scope's dirty files under the one push — the "one
push syncs everything" behaviour a central tk repo wants.

DECISION (init help / skill messaging): multi-scope under one auto-commit git-root
optimises **one-push sync**, not isolation. Coupling that is intentional and must be
stated plainly in `tk scope init` help, errors, and `tk skill`:

- One unparseable sibling `tk.cue` → `tk sync` refuses the whole git-root.
- One mid-rebase / terminal dispute / body conflict → auto-commit self-commit /
  complete-state verbs (and integrity rewrites) refuse for every auto-commit sibling
  sharing that root; `edit` and reads stay allowed (mid-rebase command classes).
- autoCommit divergence across the derived root → sync preflight refuse.

Isolation (one scope must never freeze another) requires a **separate git-root** —
sibling repository or submodule — not merely a separate scope dir in the same repo.
Init help for `--auto-commit` and multi-scope layouts must say this; do not market
central multi-scope as free fault isolation.

DECISION: tk never creates or manages the git repo — no `git init`/`git remote`/
`git clone`. For an auto-commit scope the user creates the repo and its remote with plain
git first, then runs `tk scope init` inside it, and clones onto other machines themselves
(then `tk scope import`). tk shells out to git for commit/fetch/push but owns none of the
repo's lifecycle. When the repo or upstream is missing, sync is reported disabled (the
warning above); the file writes still land on disk.

### Git dependency

DECISION: tk shells out to the external `git` binary rather than driving git in-process.
It uses the user's git version, credential helpers, and SSH config for free — including
`ControlMaster` connection reuse — and carries no git library. git is required only for
auto-commit scopes and only on the write and `tk sync` paths; reads and reconcile are
git-free. This satisfies "pure Go, no cgo" (a subprocess is not cgo). When `git` is not
on `PATH`, treat like missing git-root for self-commit: complete-state writes still land;
self-commit skipped with `sync_disabled:`; `tk sync` fails closed with the same token;
repo-driven dirty check skips with doctor note (see Auto-commit).

### Platform and portability

DECISION: v1 supports **macOS and Linux only**. Windows is out of scope (no portability
work, no CI matrix, no flock/path substitutes). Document in README/help: unsupported OS
→ clear startup error preferred over silent half-behaviour.

Unix constraints that still apply (not Windows work):

- **Locks:** scope-dir flock, XDG git-root `sync.lock`, and other XDG flocks are POSIX
  flock semantics as already named in this design. Fine on macOS and Linux.
  **Machine-local only** — they do not serialise plain-files peers on
  Syncthing/Dropbox/NFS; see plain-files repair concurrency (one repair at a time). No
  third-party lockfiles under `.git/`.
- **mtime:** use the finest mtime the OS/FS provides; keep the racy-index rule. Coarse
  filesystems and some sync tools can miss edits — `tk doctor --reindex` remains the
  escape; not a reason to invent a watcher in v1.
- **Index and git-root state location:**
  `${XDG_STATE_HOME:-~/.local/state}/tk/index.db` and
  `${XDG_STATE_HOME:-~/.local/state}/tk/git-roots/<key>/` must live on a **local** disk.
  WAL is unsafe on NFS and many synced/network filesystems. If the parent of the DB (or
  git-roots dir) is detected as non-local when practical, refuse or hard-warn with a clear
  message pointing at XDG_STATE_HOME; never put the DB or git-root ops state inside a
  scope dir or under `.git/`.
- **git:** external binary as above; not bundled. Read Git-owned paths under `.git/` as
  needed; do not write app state there.

## Merge conflict handling

Auto-commit only (where tk drives the rebase). Repo-driven defers to git plus the human
on the PR; plain-files never merges (external filesystem sync clobbers whole-file).
Four layers, lightest first.

1. Structure to avoid: one file per project means edits to different projects never
   touch the same file. Ordering holds too, because `order` is an integer+fraction rank
   key — `tk order` rewrites only the subject file, and `keyBetween` steps the integer
   part and/or grows the fraction rather than renumbering neighbours (see "Metadata").
   There is no registry inside the repo to contend on.
2. Shrink the window: `tk sync` fetches and rebases inline before pushing, so git
   auto-merges non-overlapping text and any conflict surfaces in sync's own output.
3. Semantic merge of frontmatter, by post-rebase stage parsing (not a git merge driver —
   a driver fires on every merge in the repo, including a host PR, and would require the
   tk binary there). tk lets the rebase produce standard conflicts, then field-merges.
   DECISION: schema-before-data ordering. Custom field merge typing reads each scope's
   on-disk `tk.cue` after that file is integrated, not a mix of base/ours/theirs mid-loop.
   Within one integrate (and when resuming a paused rebase), process conflicted paths in
   this order:
   1. Every conflicted `tk.cue` under an auto-commit dir sharing this git-root.
      `tk.cue` is config, not project frontmatter: resolve it with ordinary git text merge
      when auto-merge succeeds; if it conflicts, pause the rebase on that file for a human
      (no silent field-merge of CUE — a wrong autoCommit/schema guess is the failure the
      unparseable-config rule already refuses). Do not field-merge any project `.md` in a
      scope whose `tk.cue` is still conflicted or unreadable after this step — fail closed
      with the same class of error as an unparseable `tk.cue` (`scope <x> config unparseable
      — fix <dir>/tk.cue before sync can merge projects`).
   2. Conflicted project `.md` files: load the now-current on-disk `tk.cue` for that
      scope (cached evaluation still keyed by import-closure mtime/size) and use its
      `fields` / `statuses` declarations for typed list vs scalar rules and terminal
      predicates. Keys absent from that declaration stay on the undeclared scalar path.
   Steady-state and merge-time therefore share one rule: the declaration is whatever
   `tk.cue` currently says on disk. Concurrent schema+data evolution is serialized by
   resolving config first; a human stuck on a conflicted `tk.cue` must finish that before
   project merges run — the same availability coupling as sync preflight when a sibling
   config will not parse.
   For each conflicted project file, read the three stages (`git show :1/:2/:3:<f>`),
   split each into frontmatter and body, and field-merge the frontmatter.
   - Same-id add/add guard (checked first): if there is no base stage (`:1` empty — an
     add/add conflict) and both sides carry the same `id`, the two stages are distinct
     projects that collided on both id and slug (the same-title sub-case in "Project ids"),
     not two edits to one project. Do not field-merge — that would collapse two projects
     into one and lose one. Resolve it automatically with the id-collision repair from
     "Project ids": rename the loser per the deterministic pick (deterministic short-id
     extension, new path), keep the other at its path, report every inbound edge to the
     collided id as `edge_verify:` (never rewrite edges), then stage both files
     so the rebase continues, and report a repaired duplicate. This is not a layer-4 human
     handoff — it is the same automatic repair the sync integrity step runs, triggered here
     because the shared slug made the paths coincide instead of landing as two clean files.
   Otherwise (a shared base stage — the ordinary case of two edits to one project) field-merge
   the frontmatter:
   Every field is merged 3-way against the base stage (`:1`), scalars included, so
   "changed on one side" is never confused with "changed on both". The base stage is
   already parsed and already trusted for the list merge; the scalar rules use the same
   term rather than comparing only the two sides.
   - List fields (`tags`, `depends`, `related`, `links`, and every custom field whose
     declared `type` is `strings`): 3-way set merge against the base stage — base plus
     either side's additions, minus either side's removals; an add/remove clash keeps.
     This honours a concurrent removal (a pruned `depends` stays pruned, not resurrected).
   - Immutables (`id`, `created`) — separate class, never ordinary scalar LWW. These are
     identity/provenance: set at create, stable by construction. Merge rules:
     - If base has a value: keep the base value always when either side still matches base
       or only one side differs (treat the differing side as a bad/hand edit — do not
       adopt it). If **both** sides differ from base and from each other, fail the
       frontmatter merge for that file (quarantine / pause with a clear error naming the
       key) — do not pick a winner by timestamp. If both sides differ from base but agree
       with each other (identical illegal rewrite on both machines), still keep base and
       report a doctor-class warning; do not adopt the rewrite (base remains source of
       identity for the merge).
     - If base lacks the key (should not happen for normal projects): if both sides agree,
       take that value; if they disagree, fail closed as above.
     - Never invent `id` or `created` from thin air. Same-id add/add is handled before
       field-merge (rename repair), not by immutable LWW.
   - Scalars (`status`, `order`, `summary`, and every custom field whose declared `type`
     is `string`/`int`/`bool`) — not `id`/`created`. One side changed: when exactly one of
     the two stages differs from the base, that side changed and the other did not, so take
     the changed value uncontested — no tiebreaker, no handoff. This is the common
     cross-machine case (one machine runs `tk status <id> done` while the other edits the
     body or an unrelated field): the completion or order lands cleanly and is never
     reverted by the other side's later commit timestamp.
   - Scalars, both sides changed and not a status dispute (below): a genuine two-sided
     edit, so last-writer-wins by git commit timestamp (author date). Equal author dates
     (same second — routine for agent-driven commits) fall to a residual with no
     machine-local bias: take the side whose SHA-256 of its raw stage bytes is
     lexicographically greater (same residual flavour as the id-collision repair; no new
     metadata). `order` follows
     this; a tied key resolves at read time by `(order, id)`. Both-sides `status` pairs
     where **neither** value is terminal (e.g. `todo` vs `in-progress`) use this path.
     Custom scalars use this path only — there is no dispute key for custom fields.
   - A frontmatter key that is undeclared (not a built-in and not in `tk.cue` `fields`) is
     merged as a scalar string-ish last-writer-wins when both sides touch it, otherwise
     one-side-changed wins; doctor already warns on it. Prefer declaring the field so the
     typed list/scalar rule applies.
   - Scalar `status`, both sides changed from base to two different values, and **at least
     one** of those values is terminal: do not auto-merge, do not pick a winner. Terminal
     is one definition shared with `depends` satisfaction, done-class list filters, and
     merge dispute: a value is terminal when it is built-in `done` or `cancelled`, or a
     CUE custom status whose declared `category` is `done` (e.g. `shipped`, `wontfix`).
     Dispute examples: `done` vs `cancelled`, `done` vs `shipped`, `shipped` vs `wontfix`,
     **and** `done` vs `in-progress`, `cancelled` vs `todo`, `shipped` vs `review` —
     any terminal-involved both-sides disagree, so LWW cannot silently erase a completion
     or abandonment under concurrent multi-machine edit. Pure non-terminal pairs stay on
     the LWW path above. Keep the frontmatter clean YAML — never conflict markers in it —
     so the file stays parseable and indexable: write `status` to the merge-base
     (last-agreed) value and write the disputed pair into the built-in
     `status_conflict: [a, b]` key on the same file (see Metadata; `a` and `b` are the two
     post-edit status names). Route the file into layer 4's paused-rebase handoff for a
     human to decide (complete, abandon, or reopen). The dispute lives in the project
     file — source of truth — not in out-of-band "sync-state" or as index-only memory;
     reconcile materializes it like any other frontmatter, so rebuilds cannot drop the
     choice. One-sided completion (only one side changed `status`) still takes the
     changed value uncontested via the one-side-changed rule above.
     Both post-edit names must be **known** statuses for either path to run (the
     `status_conflict` shape is two known names — see Metadata). If both sides changed
     `status` and either post-edit value is unknown to the scope's schema (typo, hand
     edit), fail the frontmatter merge for that file naming the key — the same fail-closed
     class as a both-sides immutable disagreement. An unknown name cannot be written into
     the dispute key, and its terminality is undecidable, so the LWW path cannot assert
     "neither side is terminal" without risking a silently erased completion; both onward
     paths are closed, so the file goes to a human. One-sided changes are unaffected: an
     unknown value arriving via one-side-changed is taken uncontested (the merge does not
     validate frontmatter; `tk doctor` already flags an unknown status).
   - DECISION: `status_conflict` **in the stages** is its own merge-owned class — never
     set-merged, never scalar LWW. It is the merge's own previous output rather than user
     data, and it is reachable in a stage: a key left behind by an interrupted cleanup
     (a hand `git rebase --abort`/`--skip`, a manual resolve) sits on disk while the
     git-root is not mid-rebase — the doctor-hard residue Metadata already names — and that
     file is an allowlisted project `.md`, so the next snapshot commits and pushes it. Under
     the list rule two different inherited pairs would set-merge into three names, which no
     verb can repair and the shape forbids. Rules: if **this** merge yields a status dispute,
     write that pair and discard whatever the stages carried. Otherwise treat the whole
     sequence as one value on the one-side-changed shape — keep it when the sides agree or
     only one side carries it — and if the two sides carry different pairs, fail the merge
     for that file naming the key (fail-closed class as above). Never drop an unresolved
     inherited pair while writing a `status` no human chose.
   - DECISION: delete/edit — a present base stage with exactly one side's stage **absent**
     is a deletion on that side against an edit on the other, and is neither malformed input
     nor auto-resolvable. It is reachable by construction: the snapshot allowlist stages and
     commits a hand-deletion (see "tk sync", step 1), so a deleted project file on one
     machine meets a concurrent edit on the other as a delete/modify conflict. Neither side
     is resolved automatically — resurrecting a deliberate deletion and discarding a
     completion are both silent data decisions — so the merge emits a handoff signal naming
     which side deleted and the surviving side's post-edit `status`, and the rebase pauses
     for the human exactly as a body conflict does (layer 4). The human restores or removes
     the file and re-runs `tk sync`.
     Absent is not empty. Git records a deletion by omitting the stage entry, not by writing
     a zero-byte stage: `git ls-files -u` lists only the stages that exist and
     `git show :3:<path>` exits fatal for a side that deleted. The merge package's stage
     inputs must therefore distinguish absent from present-but-empty (a present zero-byte
     stage is a truncated or mangled file — malformed input), and a driver must enumerate
     the conflict's stages before reading them rather than treating a failed `git show` as
     absence, which would reclassify a genuine git fault as a deletion. Read the "`:1` may be
     empty for add/add" phrasing in the package shape below as `:1` **absent**.
     DECISION (unactioned re-run): a re-run where the human neither restored nor removed the
     file must not stage the surviving side. The resumed stop tells a delete/edit apart from
     driver output by the unmerged path's **stage set** (base present, exactly one side
     absent), not by frontmatter marker-freeness — driver output and a delete/edit both carry
     clean frontmatter, so the marker test alone stages the survivor, silently overrules the
     deletion, and leaves nothing behind to show a choice was made. Every other handoff class
     either blocks (`status_conflict`) or pushes something visibly unresolved (body markers);
     a delete/edit that resolves itself pushes an ordinary-looking file, so nobody learns the
     deletion was overruled. Four outcomes, each with its own signal: remove the file and the
     deletion wins; modify it and the surviving edit wins as written; `git add` it and it is
     kept exactly as it stands (the path stops being unmerged, so the resumed stop never sees
     it); do none of these and the stop pauses again, re-reporting the same handoff detail —
     path, deleting side, surviving `status` — and naming all three ways out. The
     discriminator is whether the worktree content still equals the surviving side's stage
     blob: matching means nothing was decided, absent or differing means the human acted. No
     state is persisted across invocations; the stage set is read from git at the moment it is
     needed. The resumed stop never re-drives a file the driver already field-merged —
     re-reading its still-present stages would overwrite a body the human just resolved.
   tk always resolves the frontmatter to clean YAML (never leaves markers in it, so the
   file stays parseable and indexable); the body is layer 4's concern, resolved
   independently within the one file.
   The arbiter is the git commit timestamp, not a frontmatter timestamp — which is why the
   schema carries no `updated:` field. Such a field could only stay honest if every edit
   stamped it, but a direct agent edit never goes through tk and reconcile is git-free and
   must not write files, so it would sit stale on the common edit path and judging a merge
   by it would keep the older edit. Git records a commit timestamp for every change, direct
   or tk-authored, so the arbiter is always present with no maintained metadata. The merge
   base is git's stage-1, never an in-frontmatter `previous:` snapshot (which would go stale
   for the same reason and reintroduce the dead metadata `updated:` would have been).
   Residual: for a direct edit the timestamp is the commit time, not the keystroke time,
   so two machines editing the same project offline can invert if their snapshot order
   disagrees with their edit order — the same bounded, single-user, concurrent-offline
   window the id analysis treats as near-never.
4. Surface, never hide — three handoffs, and none of them ever puts a conflict marker in
   the frontmatter. (The third is delete/edit, layer 3's DECISION above: tk stages nothing,
   leaves the rebase paused, and reports the path, which side deleted it, and the surviving
   edit's `status`. It is listed here because it lands in the same paused-rebase, human-
   resolves-in-file, re-run-`tk sync` shape as the other two.)
   A body (prose) conflict git could not merge: tk writes the file with its
   frontmatter already field-merged and git markers confined to the body region, and leaves
   it unstaged so the rebase stays paused; the human edits the body to resolve, and the next
   `tk sync` resumes the rebase (`git rebase --continue`) and pushes.
   DECISION (accepted tradeoff): on resume tk does not scan the body for marker-like
   lines before `git rebase --continue` — it stages and pushes whatever body the human
   left. Marker detection in free prose is unreliable: legitimate content can carry
   `<<<<<<<` / `=======` / `>>>>>>>` lines (documentation of a merge, sample diffs), so a
   scan would both false-refuse real prose and miss residue that does not match the
   pattern. Body-marker resolution is the human's say-so per the skill Conflicts section;
   only `status_conflict` — a structured key sync can test reliably — gates `--continue`.
   Body-only markers do
   **not** put the file in `parse_error` quarantine (body-aware parse — see Invalidation):
   status/order/edges stay indexed from the clean FM; list/next/search still see the
   project; complete-state mutators on auto-commit scopes still refuse via mid-rebase
   freeze, not via parse quarantine. Agents and humans resolve body text in-file; they do
   not treat marked body prose as trusted work product until markers are gone. A status
   dispute layer 3 declined (both sides changed `status`, values differ, at least one
   terminal): there is no body conflict and tk writes no markers at all — the frontmatter
   carries merge-base `status` plus `status_conflict: [a, b]` (the two post-edit names),
   clean and indexable, so `tk get`/`tk meta`/`tk doctor` surface "status conflict — set
   status to one of: <a>, <b> (or another known status) and remove status_conflict in
   <file>". The path is left unstaged, so the rebase stays paused at the git level; the
   fail-fast that closes the silent-erasure hole is that `tk sync` refuses to
   `git rebase --continue` while `status_conflict` is still present on the file, rather
   than sailing past a file that only looks resolved. The human makes the call by editing
   `status:` to either disputed value or another known status (including a non-terminal
   reopen) and deleting `status_conflict` — a direct file edit, exactly as a body conflict
   is resolved in-file, and correct because a `tk status` mutation on an auto-commit scope
   is refused mid-rebase; the next `tk sync` sees the key gone, stages the file, continues
   the rebase, and pushes. Common to both: nothing is pushed; auto-commit self-commit /
   complete-state verbs refuse while the rebase is in progress (fail fast — mid-rebase
   command classes); `tk edit` and agent file tools stay available for the in-file fix;
   the file is reported via `tk doctor`. Reads stay git-free, so `tk next`/`get`/`search`
   keep working (`next --claim` is refused mid-rebase with the other complete-state verbs)
   — only the self-commit path is blocked, correct while the base is inconsistent. Because the frontmatter is resolved to clean YAML before the file is
   written (and body markers alone do not quarantine), the index can read the project
   throughout — whether a body or a status decision awaits a human.

DECISION: frontmatter merge is a pure-function package, tested before live rebase wiring.
The rebase driver only loads stages, calls the package, and stages/writes results — it
does not embed field rules. Package shape (names illustrative):

```
MergeFrontmatter(base, ours, theirs []byte, schema ScopeSchema, meta MergeMeta) (Result, error)
```

- Inputs: raw stage blobs, carrying stage presence explicitly so **absent** (no `:1` on an
  add/add; no entry for the deleting side of a delete/modify) is distinguishable from
  present-but-empty (see the delete/edit DECISION above), the scope's post-integration
  schema (built-ins + `fields`/`statuses` from on-disk `tk.cue`), and merge metadata the
  pure core needs (e.g. git author dates for both-sides scalar LWW; the same-id add/add
  loser pick needs only the stages themselves — `created`, basename, raw stage bytes).
- Outputs: one of clean merged frontmatter YAML; `status_conflict` dispute payload;
  same-id add/add rename directive (which side loses + the loser's new id; the driver
  composes both paths, since a stage blob is content and cannot name a file); a delete/edit
  handoff signal (which side deleted + the surviving side's `status`); or error. Body is
  out of scope — the driver attaches body/markers separately.
- No git subprocess, no filesystem, no index, no flock inside the package. Deterministic
  for fixed inputs (including residual SHA-256 of stage bytes where the id-repair path
  needs it).

Implementation order: land the package with table-driven tests on canned `:1/:2/:3`
blobs and only then wire `tk sync` rebase. Do not discover merge bugs only under live git.

Required adversarial fixtures (minimum; extend freely):

| Case | Expect |
|---|---|
| List: base+ours add, theirs remove same tag | set-merge: add/remove clash keeps (or documented rule) |
| List: concurrent depends prune vs add | pruned id stays pruned; new id kept |
| Scalar one-side: ours `status: done`, theirs body-only / unrelated field | take `done` uncontested |
| Scalar both-sides non-terminal: `todo` vs `in-progress` | LWW by commit timestamp meta |
| Scalar both-sides with equal author dates | residual SHA-256 pick of stage bytes; deterministic; never "ours"/machine-local bias |
| Status dispute both terminal: `done` vs `cancelled` | base status + `status_conflict: [cancelled, done]`; no markers in FM |
| Status dispute terminal vs non-terminal: `done` vs `in-progress` | same dispute path — not LWW (must not erase completion) |
| Status dispute with custom done-category | e.g. `shipped` vs `done` or `shipped` vs `review` same path |
| Custom `strings` field vs custom scalar | typed rules from schema, not both treated as tags |
| Undeclared key both sides | scalar-ish LWW; not dropped |
| Same-id add/add (`:1` absent, same id both sides) | rename repair directive, never field-merge |
| Schema-before-data: call without readable schema | error / refuse — not guess types |
| `created` / `id` immutables | separate class: keep base; never LWW; both-sides disagree vs base → fail closed; never invent |
| Empty/malformed stage YAML | error or quarantine signal; no silent half-merge |
| Equal both-sides scalar change (identical new value) | clean take; not false dispute |
| Both-sides `status` change, one post-edit name unknown to the schema | fail closed naming the key — never a dispute key carrying an unknown name, never LWW |
| Inherited `status_conflict` on one side, no new dispute | carried through unchanged; never set-merged |
| Inherited `status_conflict` plus a fresh dispute | the fresh pair only; inherited discarded |
| Two differing inherited `status_conflict` pairs, no new dispute | fail closed — never a three-name key |
| Delete/edit: base present, one side's stage absent | handoff signal (which side deleted + surviving `status`); not an error, not auto-resolved |
| Present-but-empty stage blob | malformed-input error — a deletion is an absent stage, not a zero-byte one |

Honest boundary: this trades beads' automatic Dolt cell-merge for a small custom
frontmatter merge plus human resolution of bodies. Good trade because one file per
project keeps same-file collisions rare and the frontmatter surface is tiny.

## Status and dependencies

DECISION: eight flat built-in statuses. Lowercase, hyphen-joined; no spaces or
underscores. `tk doctor` rejects a space in a status.

| status | meaning | in `tk next`? | in default `tk list`? |
|---|---|---|---|
| draft | authoring / not implementable yet | no | yes |
| backlog | someday/maybe, not committed | no | no (`--all`) |
| todo | committed + ordered, ready | yes (if `depends` all terminal) | yes |
| review | under review (plan or result) | no | yes |
| in-progress | actively worked | no | yes |
| blocked | manually set; reason in body | no | yes, flagged |
| done | complete (terminal) | no | no (`--all`) |
| cancelled | abandoned (terminal) | no | no (`--all`) |

DECISION: `draft` closes the gap between `backlog` (someday, not committed to the queue)
and `todo` (committed, ordered, next-eligible). `tk create` produces an incomplete scaffold
by design: valid frontmatter, an H1 from the title argument, and an otherwise empty body
to fill next. A bare scaffold is not ready work, so the create default is `draft`, not
`todo`. `draft` is not backlog: intent is often already committed, only the body is
unfinished. It is a built-in (not a CUE custom) so every scope gets an honest create
default without declaring customs; customs never enter `tk next` and cannot be the create
default without every scope opting in. Not a draft workflow engine — one label, clear
next/list rules, promote with ordinary `tk status`. Empty-body skip in `tk next` is not
required while `draft` is the default (optional defence in depth later, not v1).

DECISION: statuses are labels, not a workflow. No enforced transition graph; any jump is
legal (`todo -> done` directly). tk validates only that a value is known (built-in or
CUE custom); it never rejects a transition.

DECISION: `review` is one position-agnostic status meaning "the project itself is under
review" — before implementation (plan review) or after (result review). Not split into
`plan-review`/`review`, which would smuggle workflow position into a status name.

DECISION: `blocked` is manually set, human-owned, meaning "stalled, reason in the body."
tk never auto-writes it.

DECISION: `depends` is a separate runnability filter, not a status. A `todo` whose
dependencies are not all terminal is skipped by `tk next` and carries those unmet full
ids in the list TSV `waiting-on` field; its status stays `todo`. This keeps
dependency-gating derived (never stale) while status stays manual.

DECISION: `depends` may be cross-scope. A `depends` id whose scope prefix differs from
the project's own addresses another registered scope; because one machine-wide index
holds every scope's rows (namespaced by a `scope` column), resolving the target's status
is a single query, not a second reconcile boundary. `tk next` extends its reconcile to
the transitive closure of the depended-on scopes so a cross-scope gate reads those
scopes' **on-disk / local-index** state (mtime reconcile), not a row left stale because
`next` only refreshed the ambient scope. This is **not** a network fetch: remote work
is only as fresh as the last `tk sync` (or host/external sync) for that target scope on
this machine. Use `tk sync --all` (or sync the target scope) when the gate must see
other machines' latest commits. (The earlier same-scope-only restriction was justified
by "keep `tk next` a single-scope reconcile"; the single-index architecture dissolves
that, so it is lifted.)

DECISION: an unresolvable `depends` target is held, not surfaced. When a `depends` id's
scope is not registered on this machine (the repo was never cloned here), or the id
matches no project row, its terminal-ness cannot be confirmed, so the gate treats it as
unsatisfied: the dependent stays out of `tk next` (human empty-queue / diagnostic text
may name `waiting on <id> (scope <s> not registered here)`), and the list TSV
`waiting-on` field still includes that full id among unmet depends. Held-not-
surfaced is deliberate — for a work queue, telling the agent to start work whose
prerequisite cannot be confirmed done is a worse error than a false hold, and the
signal is self-correcting (register or clone the scope, or clear the edge). Never
silent. `tk doctor` reports an unresolvable cross-scope `depends` informationally, not as
a hard error: it cannot distinguish "scope not cloned here" from "target never existed",
so it names the gap rather than condemning the config. A same-scope dangling `depends`
stays a hard flag (the scope is present, so the id is genuinely wrong).

DECISION (on-disk edge id form): every entry in frontmatter `depends` and `related` is a
**full project id** only — validated by the shared `IsFullProjectID` predicate (Project
ids). Same-scope and cross-scope use the same form; there is no bare short-id in the
file. Short form remains a CLI ergonomic for id-taking verbs (`get`, `status`, …) only —
never for edge lists. Rationale: edges are durable synced data; full ids make collision
repair, cross-scope edges, and the `edges` table one string equality; ambient short-ids
would need scope resolution at every reconcile and would diverge under rename repair.

Validation (writes that touch edges if any, reconcile materialization, and `tk doctor`):
- An entry that fails `IsFullProjectID` (bare short-id, wrong alphabet, digit-leading
  short-id, empty, …) is a hard schema violation: token `schema_error:`; do not
  materialize that entry into `edges`. For the `depends` gate it counts as **unmet** —
  the same held-not-surfaced treatment as an unresolvable target: a prerequisite that
  cannot be confirmed done must hold, and a malformed one cannot even be resolved — so
  the project stays out of `tk next` until the list is fixed. The hold is never silent:
  `schema_error:` rides the affected reads and doctor. The malformed entry is not a full
  id, so it never appears in the list TSV `waiting-on` field (full ids only) — the token
  is the diagnostic. A malformed `related` entry is the same `schema_error:` to fix but
  gates nothing (`related` never gates). Agent skill: always
  write full ids when editing `depends`/`related`.
- Legal full id whose target is missing: existing dangling / unresolvable rules apply
  (same-scope hard `depends_dangling:`; cross-scope informational `depends_unresolvable:`
  / soft related).

DECISION: edge/list hygiene (doctor; no auto-rewrite of author lists on the read path):
- **Self-depends:** a project's `depends` list must not contain its own full id
  (`id` frontmatter value). Hard flag, token `depends_self:` — a self-edge permanently
  unsatisfies the gate (or is a trivial cycle). Fix by removing the entry. Compare full
  ids only (short-only entries are already `schema_error:`, not self-depends).
- **Self-related:** a project's `related` list containing its own full id is soft only —
  token **`schema_warn:`** (same family as duplicate list noise). Pointless but non-gating;
  doctor does not refuse mutators. There is **no** `related_self:` token in v1 — do not
  invent one in implementation or agent folklore.
- **Duplicate entries** in any frontmatter list (`depends`, `related`, `tags`, `links`,
  custom `strings` fields): soft `schema_warn:` (noise; set-merge already de-dupes in
  spirit). Doctor does not auto-dedupe files.
- **`links` vs project ids:** `links` is external strings only. If an entry passes
  `IsFullProjectID` (same predicate as edge validation — not a looser regex), soft
  `schema_warn:` suggesting `related` / `depends` instead. Never hard-reject — free-form
  links may still look id-like without matching the real grammar (e.g. many ticket keys).
  Not validated as a live project reference.

DECISION: inbound edges are surfaced-not-auto-repaired by the id-collision repair —
in-scope and cross-scope alike. The repair never rewrites a `depends`/`related` entry:
the kept side retains the original id, so nothing dangles, and because both sides carried
that id, which side an existing reference meant is unknowable — a rewrite could silently
repoint a reference at the wrong project. Instead the repairing operation reports
**every** inbound edge to the collided id (`depends` and `related`) as `edge_verify:` in
its own output — operation-time only, not persisted (see "Project ids"). The subtlety it
flags is a silent mispoint, not a dangle: a reference
meaning the kept side stays correct, but one meaning the renamed side now resolves to a
different project. The report surfaces it for human confirmation rather than letting it
resolve wrong silently. The loser pick never consults inbound edges (they cannot
discriminate the sides — both carry the id). Full
mechanics in "Project ids". This is a compound near-never — a newborn duplicate id that
also acquired a reference before its first sync — so best-effort detection is
proportionate.

DECISION: `related` is a soft, non-gating project-to-project link that ships in v1. It
carries the same on-disk shape as `depends` (a list of **full** project ids, same- or
cross-scope) but gates nothing — it is pure "see also"/provenance ("this project exists
because of <id>"). None of the `depends` runnability machinery (the terminal check, the
reconcile-closure extension, the held-not-surfaced trichotomy) applies to it; the only
difference that matters between the two edge kinds is that `depends` gates `tk next` and
`related` does not. It is distinct from `links`: `links` holds external artefacts as
free-form strings (PRs, issues, branches, URLs), `related` holds full project ids, so the
project graph stays queryable separately from external references. It is a first-class
indexed edge, not frontmatter-only, so reverse lookup ("what relates to <id>?") and the
planned viewer's graph both read it from the index (see "Read interface"). An
unresolvable `related` target is cosmetic — it never gates — so `tk doctor` notes it only
in passing.

DECISION: `tk deps <id>` (alias `tk depends <id>`) is the first-class read verb for a
project's edge neighbourhood. Pure read, git-free, after reconcile over the machine-wide
`edges` table (and project rows for status/label). It does not mutate frontmatter —
authoring `depends`/`related` remains a direct file edit. Id resolution matches `tk get`
(short form: exact short-id length 4–8 in ambient scope; full `<scope>-<short-id>` any
registered scope — same resolution as `tk get`). Unknown id →
non-zero exit, message on stderr, no neighbourhood on stdout.

Default output is flat, direct neighbours only (for `a → b → c` meaning "depends on",
`tk deps b` shows prerequisites **c** and dependents **a**). Always three sections:

1. **depends on** — direct outbound `depends` (prerequisites of the subject).
2. **is depended on by** — direct inbound `depends` (impact / who is waiting on the subject).
3. **related** — soft links both directions, clearly non-gating: outbound (→ target listed
   on the subject's `related`) and inbound (← other projects that list the subject). Related
   never participates in `tk next` gating and is never expanded into a depends tree.

Each neighbour line: id, status, short label (title or `summary`). Unresolvable or
unregistered cross-scope targets stay listed with an annotation (same spirit as list's
held-not-surfaced notes) — never silently dropped. Empty sides print a quiet one-liner
`(none)` so section structure stays stable for agents.

Flags (v1 closed set):

| Flag | Audience | Effect |
|---|---|---|
| *(none)* | agents and humans | direct edges only (above) |
| `--transitive` | mainly agents | expand **depends** both directions over the full chain; still **flat** lines (easy to scan). **Related stays direct** — soft links are not a runnability DAG |
| `--tree` | mainly humans | pretty-print the **depends** graph (implies transitive depth); related remains a flat section after the tree, not mixed into tree nodes |
| both | — | tree presentation; transitive expansion is already implied |

Walks are cycle-safe: on revisit, stop that branch (no infinite expansion). If the subject
is in a `depends` cycle, print one warning (stderr) pointing at `tk doctor` for detail —
`deps` does not dump a second cycle diagram. Full cycle reporting stays on doctor (and
`tk next`'s empty-because-blocked diagnostic). No mutation flags, no `--related` toggle,
no `--json`. Open a neighbour for edit with `tk get <id>` (path hand-off unchanged).

Rationale: outbound `depends` alone is already in the file; the index earns reverse impact
and transitive expansion. A dedicated verb beats teaching agents free-form `tk query` over
an unstable schema for a core graph question. Flat `--transitive` serves agents; `--tree`
serves human multi-level browsing without forcing agents to parse box-drawing.

DECISION: every terminal status satisfies a `depends` gate — built-in `done`/`cancelled`
and any custom with `category: done`. A `cancelled` (or custom done-category) dependency
satisfies the gate exactly as `done` — otherwise cancelling one project would permanently
strand every dependent (cancelled never becomes done). Because auto-unblocking a dependent
whose prerequisite was abandoned may be wrong, `tk doctor` flags any project depending on a
`cancelled` project so the human decides whether it still applies. (Custom done-category
abandonments are the same shape if the scope uses them; doctor can flag those the same
way once the category is known.)

DECISION: `tk next` diagnoses an empty-because-blocked queue: when no project is runnable
while `todo`s wait on unmet `depends` or a cycle, it reports `nothing ready; N todo(s)
waiting on unmet deps` rather than a bare "nothing ready", so a dependency-blocked scope
is not mistaken for a finished one.

DECISION: claiming is a complete-state status write. `tk next` without `--claim` remains
a pure read (git-free; never mutates). Starting work uses `tk next --claim`: same
selection, then set `status: in-progress` under the scope flock with a conditional write
(local CAS). `in-progress` is excluded from later `next` / `next --claim` selection.
Auto-commit self-commits the claim when a git-root exists (same class as `tk status`).
`tk skill` states `next --claim` as the required start-work path; `next` without
`--claim` is inspect-only ("what would I get"). Manual claim remains valid: `tk status
<id> in-progress` after `tk next` or when the id is already known.

DECISION (`tk next --claim` — closed v1 semantics):
- **Flag only:** `tk next --claim` (with optional `--scope` / `--no-lens` as on bare
  `next`). No separate `tk claim` / `tk take` verb in v1.
- **`next` without `--claim`:** pure read; unchanged eligibility and path hand-off;
  never a writer. The collision-set skip is shared: a candidate whose id is in a
  `duplicate_id:` set is skipped with or without `--claim` (see Project ids id
  resolution).
- **Command class:** `--claim` is complete-state (self-commit / mid-rebase refuse /
  `parse_error` refuse / `sync_disabled:` when auto-commit has no git-root) — same class
  as `status` / `order`, not the read class.
- **Procedure (under scope flock for the whole span):**
  1. Reconcile as for unclaimed `next` (ambient scope + transitive depended-on scopes for
     gates).
  2. Walk next-eligible projects in the same order as unclaimed `next` (built-in `todo`, deps
     terminal, order, lens unless `--no-lens`, file at dir root not under `archive/`).
  3. For each candidate: re-validate still next-eligible from trusted state (index row
     and/or on-disk frontmatter under the same flock — implementation may read the file;
     must not claim a project that is no longer eligible). On failed re-validation (lost
     race: already claimed, status changed, deps no longer terminal, under `archive/`,
     …), skip to the next candidate. Also skip any candidate whose full id is in a
     `duplicate_id:` collision set — like id-taking mutators, claim writes no path for a
     colliding id until `tk doctor --repair`; skip to the next eligible candidate (step 5
     refuses empty only if every remaining candidate collides). Likewise skip a candidate
     whose on-disk frontmatter is unparseable / in `parse_error` quarantine — claim cannot
     safely rewrite untrusted frontmatter; skip the candidate, do not abort the walk.
  4. On first candidate that still validates: write `status: in-progress` only
     (non-terminal → non-terminal: no archive layout move). Write-through the index row.
     Self-commit when auto-commit + git-root (message class e.g.
     `tk: <id> -> in-progress`). Print the absolute path (exit 0).
  5. If no candidate remains: non-zero exit; empty stdout; stderr in the same spirit as
     the `next` empty queue (`nothing ready` / blocked-deps diagnostic when applicable).
     No file write.
- **Not in v1 on this path:** push-on-claim; network fetch before claim; claim leases;
  auto-steal of stale `in-progress`; new frontmatter keys (`claim_term`, `claimed_by`,
  `claimed_at`); assignee fields. Cross-clone / multi-machine double-claim remains
  possible until each side `tk sync`s (or host/external sync) — local CAS serialises one
  working tree only; visibility is the sync boundary (same as other complete-state
  writes). Abandoned claims: existing `stale_in_progress:` doctor soft-warn (72h); recovery
  is deliberate `tk status` back to `todo` / `blocked`, never automatic.
- **Same-tree concurrency:** two `next --claim` on one scope dir serialise on flock; the
  later call skips ids the earlier call already claimed and takes the next eligible
  project (or fails empty) — no double path hand-off for the same id from this verb.

DECISION: abandoned claim detection (v1). An agent that claims then dies leaves
`in-progress` forever out of `tk next`. No auto-status change, no lease, no assignee
field — statuses stay labels. Hygiene is detect-and-surface only:
- Bare `tk doctor` soft-warns with stable token `stale_in_progress:` when a project's
  `status` is the built-in `in-progress` (customs stay out of this signal — claim is
  built-in only) and the project file's mtime is older than
  `STALE_IN_PROGRESS = 72h` wall-clock relative to doctor run time.
- Predicate uses file mtime (same class of cheap stat as reconcile), not a frontmatter
  `updated:` stamp (reconcile must not write; direct edits would leave stamps stale).
  Clock skew and restore-from-backup can false-positive or false-negative; doctor text
  may note the age; agents treat the token as "inspect and decide," not auto-reopen.
- Never auto-set `todo` / never clear claim. Recovery is an ordinary status write after
  human/agent review of the body: typically `tk status <id> todo` (return to queue) or
  `blocked` with a body reason, or continue work if still active.
- Optional human list affordance: `tk list in-progress` (or default board) may annotate
  the same stale predicate in the summary line; not required for agents who match the
  doctor token.

DECISION: CUE custom statuses are additive; the eight built-ins are immutable (including
`draft` — must not be redeclared under `statuses`). Each custom status declares a
`category` so list filters and terminal/`depends` treat it correctly without knowing its
name. Closed set for v1 — one category per distinct CLI behaviour (no inert duplicate):

`active` | `backlog` | `done`

There is no `wip` category: it would match `active` for every v1 rule (list yes, next no,
not terminal). In-flight claim stays the built-in status `in-progress`, not a custom
category. Declaration form is `statuses: { <name>: { category: <cat> } }` in `tk.cue`
(see "Configuration").

Category matrix for customs (locked — implementers do not invent view behaviour):

| category | in `tk next`? | in default `tk list`? | terminal (`depends` + merge dispute)? |
|---|---|---|---|
| active | no | yes | no |
| backlog | no | no (`--all`) | no |
| done | no | no (`--all`) | yes |

DECISION (next-eligible — non-goal for custom ready synonyms): Only built-in `todo` is
ever next-eligible (and only when its `depends` are all terminal). Customs never appear
in `tk next` regardless of category — including `category: active` customs named `ready`,
`triaged`, etc. The agent queue stays one status (`todo` → claim `in-progress`), matching
"statuses are labels, not a workflow". v1 non-goal: no custom "ready" synonym for
`next`, no `nextEligible` CUE flag, no multi-status ready queue. Built-in `draft` is
view-equivalent to category `active` (show in default list, not next, not terminal) but
remains a built-in name, not a custom category. `backlog` hides like built-in `backlog`;
`done` hides like built-in `done`/`cancelled`.

Terminal is one predicate shared by `depends` satisfaction, default list exclusion for
done-class statuses, and merge dispute: built-in `done` or `cancelled`, or any custom
whose `category` is `done` (e.g. `shipped`, `wontfix`). There is no separate
`cancelled` category — abandonment-shaped customs use `category: done` (same as
`wontfix`). A custom done-category status satisfies `depends` and is excluded from
default `tk list` like `done`/`cancelled`; two machines that both-sides-change `status`
to different values where at least one is terminal dispute rather than last-writer-wins
(see "Merge conflict handling") — including done vs in-progress, not only done vs cancelled.

DECISION: CUE custom frontmatter fields ship in v1. A scope declares them under
`fields` in `tk.cue` with a closed type set (`string`/`int`/`bool`/`strings`) and an
optional `values` enum for string kinds. Keys are flat in project YAML (no nested
wrapper in the file). Agents read customs from the file. Merge typing follows the declaration
(list vs scalar). No required-field flag and no `tk set` verb in v1 — optional on every
project, authored by direct edit; header inspect is `tk meta` (read-only). Full shape
and validation rules in "Configuration".

DECISION: `tk create` defaults new projects to `draft`; optional second positional sets
**any known status** (built-in or CUE custom). Statuses remain labels, not a workflow —
create does not invent a narrower membership set than `tk status`. Intended uses of the
positional (not an exhaustive policy engine):

- omit / `draft` — authoring scaffold; fill body, then promote
- `todo` — body already known in the same turn; ready for the queue
- `backlog` — capture without intent to author soon
- `done` / `cancelled` / custom `category: done` — set a terminal **status label** at
  create so finished work can be written down without a fake draft→todo→done ceremony.
  This is a label shortcut, not a complete-state mutator: scaffold is still frontmatter +
  H1 (written under `archive/` because location follows terminal-ness), create still does
  not self-commit, and durability remains the mode-appropriate sync/host boundary (same
  as any other create). Stderr notes scaffold-only durability on terminal create (see
  Auto-commit help-text honesty). Agent may fill a short body the same turn
- `in-progress` / `blocked` / `review` / other actives — allowed; uncommon at create; for
  normal queue work start via `tk next --claim` (manual: `tk next` then `tk status <id>
  in-progress`)

Promote to the ready queue with `tk status <id> todo` when a draft/backlog item becomes
implementable. Scaffold contents (locked): built-in frontmatter with `id`, `status`
(default `draft`), `order` (append key), `created` (RFC3339 now), and empty or omitted
list keys (`depends`, `related`, `tags`, `links`) and empty/omitted `summary`; body is
exactly one H1 line `# <title>` from the create argument (slug frozen from that title); no
other body sections. Agent fills the project writing-guide shape under that H1 (or a short
note when recording already-done work). Two project-to-project edge kinds — `depends`
(blocks, gates `tk next`) and `related` (soft "see also", gates nothing) — not beads'
~15 types.

## Tags and lens

DECISION: the term is `tags` (free-form keywords), not `labels`. Projects carry
`tags: [...]` in frontmatter.

DECISION: a lens is a machine-local default tag view, per scope, in the XDG config,
keyed by scope name (scope names are machine-unique, so the name alone is a sufficient
key). It ships in v1. It shapes what you see; it is never a wall.

Motivating scenario: a monorepo scope `wc` holds frontend, backend, and style work. A
frontend developer sets `tk lens style frontend`; then `tk list`/`tk next` default to
projects whose tags intersect `[style, frontend]`. They can still manage everything else
(`tk list --tag backend`, `tk list --all`). A backend developer on the same scope sets
their own lens — same files, different default views, no per-user state in the shared
scope.

Safe by construction on `tk next` (the agent's work queue):
- An untagged project is never hidden by a lens (unclassified is not off-topic), so it
  stays runnable regardless of the lens.
- The active lens is echoed on stderr when a lens is in effect for `list`/`next` (never
  as a TSV stdout field or prefix on list rows — agents parse list lines as pure TSV).
- When the lens filters the ready queue to empty while unlensed ready work exists,
  `tk next` reports it (`nothing ready under lens [style, frontend]; N ready outside
  it`).
- `tk next --no-lens` / `tk list --no-lens` bypass it entirely (`--all` on list also
  includes done/backlog). The lens changes the default, never the reachable set.

A scope's `tk.cue` may declare a controlled tag vocabulary
(`knownTags: [frontend, backend, style, api]`), so `tk doctor` warns on typos
(`front-end` vs `frontend`) while free-form tags remain allowed (warn, not reject).

## Done and archive

DECISION: "done" is a filter, not a fate — and **location follows terminal-ness**.

Terminal predicate (same everywhere: archive layout, `depends` satisfaction, merge
dispute): built-in `done` or `cancelled`, or a CUE custom with `category: done`.

- Status still drives the board: `status: done` (and other done-class / backlog-class)
  drops a project from default `tk list`; `--all` brings it back. There is no
  `--archived` list flag — `archive/` is storage for terminal files, not a second board
  axis or a second status.
- DECISION: filesystem layout is a projection of that terminal predicate, not an optional
  declutter verb:
  - **Terminal** project files live under `<dir>/archive/`.
  - **Non-terminal** project files live at `<dir>/` root (flat board).
  - `archive/` is the lone tool-managed exception to the flat-scope rule; no other
    subdirectory is scanned. Reconcile indexes both locations; projects stay get / meta /
    search / deps resolvable after the move. Never delete — the record persists as the
    still-present file and in git history.
  - DECISION (who creates `archive/`): the writing operation creates `<dir>/archive/` on
    demand (mkdir if missing) the first time it must place a terminal file there —
    terminal `tk create`, a `tk status` crossing into a terminal status, `tk doctor
    --repair`, and the auto-commit `tk sync` integrity step. Its absence is never an
    error and is never doctor-flagged: a scope with no terminal projects legitimately has
    no `archive/`, which is correct state, not layout drift. Git tracks files, not empty
    directories, so `archive/` is never committed on its own — it rides in as the parent
    of the first archived `.md` (the allowlisted, committed unit is the file, not the
    directory).
- Who moves the file:
  - `tk status <id> <status>`: when the new status crosses the terminal boundary
    (non-terminal → terminal or terminal → non-terminal), rewrite frontmatter **and**
    rename the file to the correct location in the same complete-state mutation. Prints
    the **post-move** absolute path. Self-commit (when available) stages both the new
    path and the removal of the old path. Same-side changes (todo → in-progress, done →
    cancelled) leave the file where it is.
  - `tk create <title> [status]`: write the scaffold at the correct location for that
    status (terminal → under `archive/`; otherwise dir root). Still never self-commits.
  - Direct frontmatter edit of `status` (file tools / `$EDITOR`) does **not** move the
    file — that can leave layout drift. Prefer `tk status` is **load-bearing**, not style:
    free edit of the `status` key alone is an incomplete status change until layout
    matches (drift tokens until repair / status verb). Agents and humans must not
    hand-move project files between root and `archive/` to "fix" layout; use
    `tk status` or repair.
- Layout drift (status and path disagree):
  - Non-terminal under `archive/` → soft token `archive_non_terminal:`.
  - Terminal still at dir root → soft token `archive_terminal_at_root:`.
  - Bare `tk doctor` reports only. `tk doctor --repair` and the auto-commit `tk sync`
    integrity step move files both ways until layout matches status (fixed messages,
    e.g. `tk: repair archive layout <id>`). Idempotent.
- Queue safety during drift: `tk next` never returns a project whose file is under
  `archive/` (even if status was hand-edited to `todo`). After `--repair` or a proper
  `tk status` reopen, the file is at root and next-eligibility follows normal todo + deps
  rules. Default list is the active board at dir root; `--all` and search still surface
  terminal projects under `archive/`.
- No `tk archive` and no `tk unarchive` verb. Finishing is `tk status … done` (or create
  with a terminal label). Reopening is `tk status … todo` (or another non-terminal) —
  labels, not a workflow — and the status verb moves the file back to the flat dir.

## CLI surface

DECISION: single-purpose CLI named `tk`. Project management only. Text on stdout —
locate/mutate verbs print a path (one line); `list` prints closed TSV board rows (no
path column — open via `get`/`next`/`status`). tk does not support `--json`. No flag,
no stable JSON envelope, on any command.
Warnings, doctor, empty-queue diagnostics: stderr text (and human stdout where
appropriate). Revisit only if concrete text pain appears later (not a v1 pillar).

DECISION (path hand-off): every command that prints a project file path on stdout prints
a **cleaned absolute filesystem path** (resolved via `filepath.Abs` / equivalent — no
cwd-relative forms, no `~` prefix). Applies to `get`, `next`, `create`, `status`,
`order` (one path line), to the path field on each `search` hit line, and to the
`path:` field in `meta`'s preamble (same absolute path `get` would print for that id).
After a status-driven layout move, `status` prints the post-move path. No `--relative`
flag in v1. Relative paths would break agents that change directory between locate and
open; absolute is the only safe hand-off.

DECISION (exit codes, v1 minimal): `0` success; non-zero failure. The only distinguished
code locked for v1 is **exit 2** for **usage / bad CLI input**, including:
- unknown status name (e.g. `tk list` / `tk status` / `tk create` with a status not known
  for the target scope — see list known-status rule);
- **malformed project id** on an id-taking verb (token with `-` that fails
  `IsFullProjectID`, or short form that fails `IsShortID` — see Project ids ergonomics);
- other clear usage errors (empty create title, missing required flags, …) as already
  implied by each verb.
**Unknown but well-formed** project id (grammar ok, no row) is **not** exit 2 — generic
non-zero (conventionally `1`) with an `unknown project id:` style message. All other
failures also use generic non-zero with the error message and, where applicable, a stable
stderr token. No broader multi-code map (conflict / config / …) in v1 — agents already get
path hand-off, stderr text, and closed warning tokens. Expand the map later only if
concrete script/agent pain appears; do not invent sysexits-style tables pre-implementation.

Product cut: tk indexes, queues, and locates; the filesystem is the editor. No "print
full project markdown" verb — the body is the file. `tk meta` is the allowed header
inspect (frontmatter + a fixed preamble; never the body). Filenames already embed the id
(`<id>-<slug>.md`). Agents edit with file tools; humans may use `$EDITOR` via `tk edit`.

DECISION: project verbs are top-level — the unit of work is the CLI's purpose, and
`list`/`next`/`get`/`meta`/`deps`/`create`/`status`/`edit`/`order`/`search`/`sync` are
the hot path. Scope administration (container management, not work; each command runs about
once per scope per machine) groups under `tk scope`: `init`, `import`, `rebind`, `rename`,
`forget`, `list`. `tk scopes` is accepted as an alias of `tk scope`, and the bare noun
with no subcommand runs `list`.

Hot path stdout contract:

| Command | Job | stdout |
|---|---|---|
| `list [status…] [--scope] [--tag]… [--all] [--no-lens]` | Board / inventory | One TSV line per row: `full-id`, `status`, `title`, `summary`, `waiting-on` (no path) |
| `next [--scope] [--no-lens] [--claim]` | First ready `todo` (deps ok, order, lens, dir root); `--claim` also sets `in-progress` | Absolute path |
| `get <id> [--scope]` | Resolve short or full id | Absolute path |
| `meta <id> [--scope]` | Project header (frontmatter) | Preamble + raw frontmatter YAML (not body, not path-only) |
| `deps <id> [--scope] [--transitive] [--tree]` | Edge neighbourhood (depends + related) | Summary (not paths) |
| `search <terms> [--scope]` | FTS find-then-open (bm25) | One TSV line per hit: `full-id`, `status`, `title`, `summary`, `absolute-path` |
| `create <title> [status] [--scope]` | Scaffold (default `draft`; frontmatter + H1) | Absolute path |
| `status <id> <status> [--scope]` | Set status (promote / claim / done / …) | Absolute path (after write / layout move) |
| `edit <id> [--scope]` | Open in `$EDITOR` | empty stdout on success (no path); no self-commit |
| `order <id> … [--scope]` | Rewrite integer+fraction `order` key | Absolute path (after write) |

Optional `--scope` on every ambient project verb above (Resolution DECISION); doctor is
the exception (no `--scope`). `sync [--scope]` / `sync --all` documented under sync.

Errors: non-zero exit, message on stderr, no path on stdout when there is nothing to hand
off (unknown id, nothing ready, …).

Agent loop:

```text
tk create "Title"               → path (status: draft; frontmatter + H1)
# file tools: write body under H1 (skill Body conventions)
tk status <id> todo             → path (ready for the queue)
tk next --claim                 → path (select + claim in-progress)
# file tools on that path
tk status <id> done             → path under archive/ (location follows terminal)
# end of turn: tk sync only when auto-commit (see skill End of turn)
```

Known id: `tk get ab2c` → path; `tk meta ab2c` → header. Capture without authoring soon:
`tk create "Later" backlog`. Already-ready body in one shot: `tk create "Title" todo`.

- `tk scope init <dir> (--name <name> | --auto-name) [--code-root <path>]
  [--auto-commit]` — create and register a scope. Dir required; exactly one of
  `--name`/`--auto-name`; code-root by the matrix (`--code-root` always allowed, defaults to
  repo root in a repo else dir); `--auto-commit` writes `autoCommit: true`, omit writes
  false (or inherits siblings when the repo already has scopes). Never prompts, never
  runs git. In a dedicated tk repo, pass `--auto-commit` (omit = repo-driven).
- `tk scope import <dir> [--code-root <path>]` — register an existing on-disk scope,
  files in place; name and autoCommit come from its `tk.cue`. Hard-fails on a scope-name
  collision or malformed `tk.cue`. Symmetric errors with init.
- `tk scope rebind <dir> --name <name> [--code-root <path>]` — rewrite machine-local
  registry paths for an already registered scope. Positional `<dir>` always updates
  `dir`; `--name` selects the registry entry (required); `--code-root` updates `root`
  when set, and when omitted leaves `root` unchanged (does not re-run init's code-root
  defaults). Absolute paths on the wire; lens and registry key preserved. No
  `tk scope use`. See Scope lifecycle.
- `tk scope rename <old> <new>` — rename a scope in place: rewrites `tk.cue`, every
  project id, filename, and in-scope edge in one operation (auto-commit: one commit);
  reports cross-scope inbound edges as `edge_verify:` in its own output; re-keys this machine's
  registry and lens. Cheap path: rename before other machines register. Post-share:
  other machines `tk scope forget <old>` then `tk scope import` (lens not preserved).
- `tk scope forget <name>` — unregister a scope (registry and lens entries, index
  rows); never touches the files.
- `tk scope list` — list every registered scope (all visible). Bare `tk scope` (or the
  alias `tk scopes`) runs `list`. DECISION (scope list stdout, v1 closed): parse-stable
  TSV, one line per registered scope, sorted by name (registry key) ascending, same
  hand-off discipline as `list`/`search`:
  ```text
  <name>\t<dir>\t<root>\t<mode>
  ```
  - `name`: the registry key (what `--scope` / an id prefix names).
  - `dir`, `root`: cleaned absolute paths (the path hand-off form), read straight from
    the registry — the two paths it stores per scope; no derivation, no filesystem probe.
  - `mode`: closed label set derived like sync/doctor (read `tk.cue` `autoCommit`, then
    git-root presence) — `tk-driven` (autoCommit true), `repo-driven` (false, `dir` inside
    a git repo), `plain-files` (false, `dir` outside git), or `unknown` when `autoCommit`
    is unreadable (absent/unparseable `tk.cue`) or the dir is gone. `unknown` keeps the
    command total and available for recovery/diagnosis (name drift, broken config,
    unreachable dir) rather than failing the whole listing on one bad scope.
  - Empty registry → exit 0, empty stdout. Soft tokens (`name_drift:`,
    `unreachable_scope:`, `config_unparseable:`) still ride stderr; they never wrap or
    interleave the TSV stdout fields.
  - Not git-free: the `mode` field stats each dir / derives its git-root. `name`/`dir`/
    `root` alone are pure registry reads. No `--json`; no path column beyond `dir`/`root`.
- `tk lens [tags...] | --clear [--scope S]` — set/show the machine-local default tag view
  for the resolved ambient scope (optional `--scope` override).
- `tk list [status…] [--scope S] [--tag T]… [--all] [--no-lens]` — board / inventory for
  **one scope**. DECISION (list stdout, v1 closed): parse-stable TSV rows; open via
  `tk get` (or path-hand-off mutators), not a path column.
  - **Scope:** ambient code-root scope, or exactly the scope named by `--scope` when
    given (`--scope` wins over ambient). Not machine-wide multi-scope inventory (use
    `tk search` / `tk query` / per-scope list for that).
  - **Filters** change the **row set only**, never the column set. Zero or more
    space-separated status name positionals = union filter over that target scope. Bare
    `tk list` (no status positionals) keeps the default active set (not a status name).
    **Known status** for those positionals (and for `tk create` / `tk status`
    membership): a name is known if it is a built-in **or** a custom declared under the
    **target scope's** `tk.cue` `statuses` (for list: the ambient or `--scope` scope; for
    create/status: the ambient write scope). Unknown name → exit 2. A custom declared
    only in another scope is unknown when listing this scope. If the target scope's
    `tk.cue` is unparseable, customs are not loadable — only built-ins are known for CLI
    membership checks until the config is fixed (reads of project rows still work). No
    CSV. No `--status` flag. `--tag T` may repeat; multiple tags are OR (match any listed
    tag). Lens still applies unless `--no-lens` (lens and `--tag` combine as AND between
    the two filters: project must pass the lens, and if any `--tag` is given must match
    at least one). `--all` remains "include done/backlog/…" board-wide, not a status
    token — also the way terminal projects under `archive/` reappear (no `--archived`).
    No date filters on list in v1. No `--archived`. No path column in v1.
  - **Sort:** `(order, id)` ascending (order key, then full id). The `order` key itself
    is not a stdout column.
  - **One line per project**, tab-separated fields (parse-stable; empty fields stay
    empty, still tab-delimited):
    ```text
    <full-id>\t<status>\t<title>\t<summary-or-empty>\t<waiting-on-or-empty>
    ```
    - `full-id`: always `<scope>-<short-id>`.
    - `status`: frontmatter status as stored.
    - `title`: body H1 via the shared title-extraction helper (list/meta/search); empty
      string if none. Never fall back to slug or summary.
    - `summary`: frontmatter `summary` if set, else empty (never fall back to title/slug).
    - `waiting-on`: space-separated full ids of unmet direct `depends` (non-terminal,
      unresolvable, or missing row), sorted full-id ascending for stable output; empty
      when every direct depend is terminal or the list is empty. Not free-text reasons —
      detail for unregistered scopes lives on `tk next` diagnostics / `tk doctor` /
      `tk deps`. Multiple unmet deps stay one field (spaces inside the last TSV column).
      Malformed `depends` entries never appear here (not full ids); they still hold the
      gate and ride `schema_error:` (see Status and dependencies).
  - **Not columns in v1:** absolute path, order key, tags, archived flag, bm25/score,
    scope name (redundant with full-id prefix on a single-scope verb).
  - **Empty result:** exit 0, empty stdout. Non-zero only for usage / failed reconcile
    class errors; then empty stdout + stderr message.
  - **Duplicate id rows:** board may surface both rows (same post-reconcile
    `duplicate_id:` stderr warning as other board verbs).
  - **Lens / soft integrity:** active lens echo and tokens such as `stale_in_progress:`
    go on stderr; they must not wrap, prefix, or interleave TSV stdout fields.
  - Pure read; git-free.
  - Examples: `tk list`, `tk list todo`, `tk list todo backlog`,
    `tk list in-progress blocked review`, `tk list --tag backend`,
    `tk list todo --tag api --tag network`, `tk list shipped --scope wc` (custom must be
    declared in `wc`).
- `tk get <id> [--scope S]` — resolve short or full id to the project file path; print that
  absolute path (see path hand-off DECISION). Resolution: see Project ids ergonomics
  (short form = exact short-id length 4–8 in ambient scope; full id anywhere registered).
  Optional `--scope` is ambient override for short form (Resolution DECISION); full id
  does not need ambient. Read/locate only — no `--status` or other mutation flags
  (mutation stays on `status` / `create`). Optional later (not v1): path column on
  `list`; aliases `show`→`get`.
  DECISION (`get` exit under `parse_error` / path hand-off success): `get`'s job is
  locate-for-repair, not "header is healthy." When the id resolves and the project file
  exists on disk, print the absolute path and **exit 0** even if the row is in
  `parse_error` quarantine. Ride `parse_error: <id>: <message>` on stderr (and any scope
  unparseable count). Exit 0 + path is success of hand-off; agents must still treat the
  project as broken until reconcile parses again (mutators refuse with non-zero + same
  token). Non-zero only when no path is handed off:
  - Unknown id, short id with no scope, unreachable scope for this id, usage/unknown flags:
    non-zero (usage → exit 2); empty stdout.
  - File missing while an index row still exists: non-zero; empty stdout; suggest doctor /
    reindex.
  Contrast `meta`: extractable FM → exit 0 with YAML; wholly unparseable FM → non-zero
  with empty stdout. `get` does not split on extractability — path is enough for repair.
- `tk meta <id> [--scope S]` — inspect one project's header without opening the body. Pure
  read, git-free; never mutates files, index, or commits. Id resolution matches `tk get`
  (short id needs ambient / `--scope` / `TK_SCOPE`; full id any registered scope). Only
  ambient flag in v1 is optional `--scope`; no aliases (`metadata` / `header` / `show`
  not accepted). No mutation form — inventing `tk meta … set` is the rejected `tk set`
  surface, not this verb.
  Reconcile the scope(s) needed for resolution (same class as `get`/`deps`); post-reconcile
  integrity warnings ride stderr. Malformed `tk.cue` does not block (reads stay available).
  Stdout (fixed shape):
  1. Preamble lines, then one blank line:
     ```
     id: <full-id>
     title: <H1 text or empty>
     path: <absolute path>
     ```
     `id` is always the full `<scope>-<short-id>` even when the user typed a short id.
     `title` is extracted like `list` (DECISION below), empty if missing.
     `path` is the same absolute path `get` would print (path hand-off DECISION) so a
     human glance does not require a second command; agents still use
     `get`/`next`/`status`/`create` for path hand-off and must not parse `path:` out of
     `meta` when `get` exists.
  2. Frontmatter YAML **exactly as stored** in the file: extract the interior of the first
     `---` … `---` document (file must open with a `---` fence line); print that interior
     without re-encoding and without the fence lines. Key order, quoting, comments, blank
     lines, built-ins, customs, undeclared keys, and `status_conflict` are all preserved
     when present. Do not print the markdown body after the closing fence.
  Not printed as synthetic frontmatter (derived / index-only): waiting-on, task counts,
  lens match, percent done — use `list` / `next` / `deps` / `query`. Edge neighbourhood
  status labels stay on `tk deps`; `meta` shows raw `depends`/`related` lists only.
  Exit and edge cases:
  - Unknown id, short id with no scope, unreachable scope for this id, usage/unknown flags:
    non-zero (usage → exit 2); empty stdout; message on stderr.
  - File missing while an index row still exists: non-zero; empty stdout; suggest doctor /
    reindex.
  - `parse_error` quarantine with an extractable frontmatter block: exit 0; preamble + raw
    YAML; stderr `parse_error: <id>: <message>` (and any scope unparseable count).
  - `parse_error` with no well-formed frontmatter block: non-zero; empty stdout; stderr
    `parse_error: …`.
  - `status_conflict` present: exit 0; key appears in the YAML dump; one stderr line naming
    the two terminals (same spirit as `get`/`doctor`).
  - Project under `archive/` (terminal, or layout drift): exit 0; normal output; `path`
    under `…/archive/…`.
  Explicit non-goals (v1): mutation; key filter (`tk meta <id> status`); `--json`; body
  dump; deps graph; lens filtering; re-serialize round-trip as the success path.
  Implementation note: share id resolution with `get`; prefer a raw fence-slice API so the
  success path does not YAML-decode for stdout; tests must preserve exact interior YAML
  (comments, order, customs, `status_conflict`).
- `tk deps <id> [--scope S] [--transitive] [--tree]` — edge neighbourhood for a project
  (alias `tk depends`). Pure read over the index after reconcile; summary on stdout (id,
  status, short label per neighbour), not paths. Id resolution matches `get` (optional
  `--scope` for short form). Default: direct **depends on**, **is depended on by**, and
  **related** (both directions, non-gating). `--transitive` expands depends both ways as
  a flat list (agent-friendly); related stays direct. `--tree` pretty-prints the depends
  graph for humans (implies transitive depth); related stays a flat section after the
  tree. Cycle-safe walks; if the subject is in a depends cycle, one stderr warning
  pointing at `tk doctor`. No edge mutation (author `depends`/`related` by direct
  frontmatter edit). Full rules in "Status and dependencies".
- `tk search <terms> [--scope S]` — full-text search (FTS5, bm25) over titles and bodies.
  Machine-wide by default; `--scope S` to bound. Pure read; no lens; includes archive and
  all statuses. Stdout: one tab-separated line per hit, bm25 desc then full id asc —
  `full-id`, `status`, `title` (H1 helper), `summary` (or empty), `absolute-path` (path
  hand-off). Empty result → exit 0, empty stdout. Terms required (trim non-empty) or
  usage exit 2. No score column. Open a hit via the path field (or `tk get <id>`). Full
  contract: Query surface.
- `tk query <sql>` — **read-only** SQL over the index (`SELECT` / read-only explain).
  Index is ephemeral/derived — file mutations are the durable path; mutating SQL is
  rejected (not a silent no-op). Schema is not a stable API (derived, rebuilt on any
  `schema_version` bump, may reshape between releases). `--help` says so;
  `tk query --schema` prints the current shape. Not for saved queries or tooling. No
  ambient `--scope` flag (filter in SQL if needed). Audience: debug / human ad-hoc;
  agents use `deps` / `list` / `search` / `next` / `get` / `meta` instead (query audience
  DECISION).
- `tk next [--scope S] [--no-lens] [--claim]` — first runnable project by `order` with
  dependencies satisfied; prints its absolute path. The primary agent entry point (beads'
  `ready`, renamed). Optional `--scope` selects the ambient inventory scope. Eligibility:
  built-in `todo`, deps terminal, order, lens, **file at dir root** (never under
  `archive/` — layout drift or terminal storage), and id not in a `duplicate_id:`
  collision set (skipped with or without `--claim`). Honours the lens by default and
  diagnoses an empty-because-blocked queue. **`next` without `--claim` is a pure read**
  (git-free; not a mutator). **`tk next --claim`** is the start-work path: same selection, then
  conditional write to `in-progress` under the scope flock (local CAS), complete-state
  self-commit when available, path on stdout — full semantics in "Status and
  dependencies". Prefer `--claim` when beginning implementation; use `next` without
  `--claim` to inspect the queue. Manual alternative: `tk next` then
  `tk status <id> in-progress`.
- `tk create <title> [status] [--scope S]` — scaffold a project: mint the id, write valid
  frontmatter (see Status and dependencies create scaffold), write H1 `# <title>`,
  write-through the index row, print the absolute path for the agent to fill the rest of
  the body. Optional `--scope` selects the write ambient. Optional second positional is a
  known status if present; omitted → `draft` (authoring reserved,
  not next-eligible). Second positional is any known status: `todo` when body is already
  known; `backlog` for capture; `done`/`cancelled` as a terminal **label shortcut** for
  finished work (not a self-commit); other labels allowed. Write location follows
  terminal-ness: terminal status → under `archive/`; otherwise dir root (see "Done and
  archive"). Title is one argv (quote multi-word); after trim it must be non-empty
  (usage exit 2); it sets the H1 and the frozen filename slug via `slugify(title)`
  (Project ids). No `--status` flag. No status-first order. Never self-commits for any
  status (explicit exception to complete-state self-commit; skeleton reserves the id;
  first git durability at the next `tk sync` when auto-commit, or host commit when
  repo-driven). Terminal create: absolute path still on stdout; terse stderr note that the
  scaffold is not git-durable until that boundary (do not invent a closed token for this
  human/agent cue). Always appends on `order` (`keyBetween(last, null)` with scope-wide
  max valid `order` — every status and `archive/`; see order rank domain); no create order
  flags — use `tk order` for placement on the active board. The one create call; every
  edit after is direct file access. Promote with `tk status <id> todo` when implementable.
  Optional later (not v1): alias `add`→`create`.
- `tk status <id> <status> [--scope S]` — set status (word is status, not state). Optional
  `--scope` for short-form id ambient. A complete-state write: rewrite frontmatter; when
  the new status crosses the terminal boundary, rename the file between dir root and
  `archive/` in the same mutation (see "Done and archive"). Auto-commit commits the
  touched path(s) synchronously (no push); non-auto-commit just writes/moves the file.
  Prints the **post-move** absolute path after success. Refuses if the project is in
  `parse_error` quarantine (fix the file via `get` path first — see Invalidation).
- `tk edit <id> [--scope S]` — resolve id to path and open in `$EDITOR`. Human convenience
  only; agents use `get` / `meta` / `next` / `status` / `create` and their own file tools
  (`meta` for header inspect; path hand-off remains `get`/`next`/`status`/`create`).
  Optional `--scope` for short-form ambient. DECISION (edit stdout / durability): on
  success, **empty stdout** (no path line — not a path-hand-off verb). Errors → non-zero
  exit, message on stderr, empty stdout. Does **not** self-commit when the editor exits;
  editor saves are ordinary direct file edits, durable at `tk sync` (auto-commit +
  git-root) or host commit (repo-driven), same as agent file-tool writes. May open
  `parse_error` paths for human repair; does not rewrite frontmatter itself. Mid-rebase:
  **allowed** (path-open class — see mid-rebase command classes); not a complete-state
  mutator and not refused with `status`/`order`/`next --claim`.
- `tk order <id> (--before <id> | --after <id> | --first | --last) [--scope S]` — rewrite
  the integer+fraction `order` key to an explicit slot; the destination flag is required
  (no bare `tk order <id>`). Optional `--scope` for short-form ambient. tk reads the
  target neighbours from the index and writes `keyBetween(left, right)` into the
  ordered project's frontmatter only (integer step and/or fraction growth; never
  renumbers a band). `--before` / `--after` use the named neighbour's key (any in-scope
  project with a valid `order`). `--first` / `--last` use open bounds against the
  scope-wide **min** / **max** valid `order` (same set as create append: all statuses,
  root and `archive/`, valid keys only) and remain single-file under normal headroom
  (negative/positive integer heads). No relative counters, swap, or batch. Complete-state
  mutation: self-commits when auto-commit is on (same class as `status`). Prints absolute
  path after success; errors → stderr, no path. Refuses on `parse_error` quarantine (same
  as `status`). Not cross-scope relocation and not archive layout (id embeds scope;
  location follows terminal via `status` / doctor — do not overload this verb). No v1
  alias `move`→`order`.
- `tk sync [--scope S] [--all]` — reconcile now / done-for-now and the sole push boundary
  (auto-commit scopes only). Targets the ambient scope (`--scope` / `TK_SCOPE` / cwd);
  refuses with a mode-named error if ambient is non-auto-commit. `--all` (or no ambient
  scope) syncs every auto-commit scope / git-root; skips non-auto-commit. When that
  eligible set is empty (no registered auto-commit scopes / git-roots): **exit 0**, terse
  stderr that nothing was synced — not an error (empty-set DECISION under Sync model).
  Snapshot commits only the allowlist (project files, `tk.cue`, `.gitignore`); non-allowlist
  residue (including any `AGENTS.md`) is warned, never force-committed. Skill: end-of-turn
  only when tk-driven.
- `tk doctor [--reindex] [--repair] [--re-space-order] [--all]` — diagnose (default) and
  optional integrity repair. **No `--scope` in v1** (use `TK_SCOPE` or cwd ambient —
  doctor scope selection DECISION). Bare `tk doctor` **never mutates files**: report conflicts,
  same-scope dangling `depends` (hard), self-`depends` (hard — `depends_self:`),
  unresolvable cross-scope `depends`/`related` (informational — scope not registered here
  vs target gone are indistinguishable), `depends`
  cycles, depends-on-cancelled, registry/config drift (including remote rename: tk.cue
  name ≠ registry key — `name_drift:`; that scope is fail-closed unusable until
  `tk scope forget` then `tk scope import`, not auto-rekey; project verbs hard-error —
  see Registry), unparseable `tk.cue` (scope read-only; blocks `tk sync` for the whole
  shared git-root), autoCommit divergence across scopes sharing a derived git-root,
  frontmatter schema violations (unknown status, custom field type/`values` mismatch,
  `depends`/`related` entry not a legal full project id — hard; undeclared frontmatter
  keys and `knownTags` typos — warn), terminal-status dispute (`status_conflict` present —
  mid-rebase: resolve in-file; not mid-rebase: hard residue to clear), last-push error and
  sync health (repo/upstream not set up; last-push-error under XDG `git-roots/<key>/`;
  `sync_disabled:` when applicable), unparseable project files, non-allowlist residue under
  the dir, archive layout drift (soft — `archive_non_terminal:` and
  `archive_terminal_at_root:`), pathologically long `order` keys (soft threshold length >
  64 — report only), stale built-in `in-progress` when the project file mtime is older
  than 72h (soft — `stale_in_progress:`; no auto-status change), repo-driven allowlisted
  dirty files under the scope dir (soft — `uncommitted:`; never commit), and index health.
  Report coverage: ambient scope only when ambient resolves; every registered scope when
  no ambient (report stays discovery-friendly).
  - `--repair` — file-mutating twin of the auto-commit `tk sync` integrity step: id
    collision rename (edges never rewritten; inbound edges surfaced via `edge_verify:`), equal-`order`
    re-space, and archive layout (terminal ↔ `archive/`, both directions). Sole mutating
    path for non-auto-commit. Target set: ambient / `TK_SCOPE`, or explicit `--all` for
    every registered scope; without ambient and without `--all` → usage error (exit 2).
    Auto-commit + git-root: self-commits touched files (no push). Mid-rebase on that
    git-root: refuse (same class as `status`/`order`/`next --claim`). See Invalidation / Project ids /
    Done and archive / mid-rebase command classes.
  - `--re-space-order` — explicit band re-space for over-long `order` keys only; not
    implied by `--repair`. Same mutating target set, self-commit rule, and mid-rebase
    refuse when auto-commit + git-root.
  - `--all` — with `--repair` / `--re-space-order` only: machine-wide mutation. Not
    required for bare report (no ambient already reports all) or `--reindex`.
  - `--reindex` — full index rebuild from the files; never touches project files
    (machine-wide DB rebuild even when ambient is set). Does not require ambient/`--all`.
  Text on stderr/stdout — no JSON envelope.
- `tk skill` — print the Agent skill contract (below) to stdout as agent-facing workflow
  markdown. Discovery command: no ambient scope required. Never auto-writes into a tree.
  The contract section is the authoritative body; this bullet only names the verb.
- `tk skill install` / `tk skill list` / `tk skill uninstall` — reserved placeholders until
  agentdex-backed install. Appear in help and the command tree so agents do not invent
  paths. Each exits non-zero with a clear message (hard refuse, not a success no-op),
  e.g. `not implemented in v1 — use 'tk skill' to print the workflow; persistent install
  is planned via agentdex skills directories`. Same message for all three; no fake empty
  list. No install targets, no write into AGENTS.md / skill dirs, no agentdex dependency
  in the first build of these subcommands.

DECISION: no-scope error. When resolution yields no scope, scope-requiring commands
error with guidance (`no scope here — cd under a registered code-root, 'tk scope rebind
<dir> --name <scope> --code-root .', 'tk scope import <dir>', or pass --scope`). The
message does not probe the tree for an unregistered `tk.cue` — registry only (see
Scope lifecycle).
Discovery commands (every `tk scope` subcommand, `list --scope`, `search`, `query`,
`doctor`, `help`, `skill` and skill placeholders) never error on no-scope. `tk get`,
`tk meta`, and `tk deps` need no ambient scope when the id is full (`<scope>-<short-id>`);
a short form (exact short-id, length 4–8) still requires ambient scope, `--scope`, or
`TK_SCOPE` to resolve.

## Discovery

DECISION: discovery is opt-in and user-initiated — tk never auto-writes a discovery
artifact into any tree, and never scans the tree for an unregistered scope. After a scope
is registered, ambient use is ceremony-free (cwd → registry → work). Getting an agent
onto that path is **not** a single forced product flow. Three supported bootstrap styles
(user picks one or combines them):

1. **Use the skill directly** — `tk skill` prints the full Agent skill contract to stdout
   (v1 real). No persistent install. An agent (or human) that can run the CLI primes from
   that dump; the contract teaches `tk scope import <dir>` and the work loop. No
   pipe-to-jq.
2. **Install the skill** — `tk skill install|list|uninstall` (planned via agentdex; v1 is
   hard-refuse placeholders so agents do not invent paths). When landed: user-initiated
   install into the agent's skills directory; never automatic on `tk scope init`. See
   placeholders below.
3. **Own bootstrap** — human-authored handoff outside tk (repo-root or host `AGENTS.md`
   one-liner with the scope dir, repo docs, team runbook, agent memory). tk does not
   write these; does not forbid them; and does **not** auto-commit them (not on the
   snapshot allowlist — durability is the host repo / human process). Cold start still
   ends in `tk scope import` (or `rebind` / `--scope`) before project verbs work.

Mechanisms that apply regardless of bootstrap style:
- The CLI auto-resolves the ambient scope from cwd via the registry only, so an agent
  just runs `tk` in a registered tree. An unregistered on-disk scope is invisible until
  `tk scope import` (no filesystem probe — see Scope lifecycle).
- `tk skill install|list|uninstall` are v1 hard-refuse placeholders (see CLI surface).
  Persistent install needs each agent's skills directory; that lookup is owned by
  agentdex (`agentdex get <id>` reports `skills_dir` / local skills paths; catalog is
  provider-agnostic). tk will use agentdex rather than hardcoding Claude/OpenCode/etc.
  paths. Until that integration exists, install must not ship half-baked. Deferred with
  real install (not v1): agentdex integration, concrete targets (global/local skills dirs
  per agent, optional AGENTS.md block), and list/uninstall semantics against what was
  installed. Installation remains user-initiated, never automatic: `tk scope init` writes
  no AGENTS.md block.

"No ceremony" in the Problem sense means: tk does not force its own onboarding into the
repo and does not auto-slurp scopes. It does **not** mean "clone alone registers and
teaches the agent with zero user choice."

## Agent skill contract

DECISION: `tk skill` stdout is this contract — not a free-form help essay and not a
second source of truth that can drift from the rest of the design. Implementation may
render the section (or a maintained extract of it) as markdown; every required subsection
below must appear with its locked body. Rules that live elsewhere in this document are
referenced, not duplicated in conflicting form. The contract is complete for v1: do not
omit subsections, invent interim agent folklore, or reintroduce skeleton placeholders.
**Authority:** when a skill subsection and an earlier `DECISION:` / section prose disagree,
the earlier body rule wins and the skill extract must be fixed — not the reverse. When
changing a locked rule, update every skill subsection that restates it (or switch that
subsection to a pure reference) in the same edit so `tk skill` cannot ship a stale
extract.

DECISION: path-centric interface. Locate/mutate verbs print one **absolute** path line;
agents open that path with file tools. Never assume a cwd-relative path. There is no
`--json` and no "print full project markdown" verb — the body is the file.
`tk meta <id>` is the read-only header inspect (preamble + raw frontmatter); it is not
path hand-off and not a body dump (`path:` in the preamble is absolute for humans only).

### Required sections (locked TOC)

Skill output must include these headings, in this order:

1. Core work loop
2. Capture
3. Frontmatter mutation
4. Body conventions
5. Title, slug, and filename
6. Ordering
7. List and filters
8. Search
9. Dependencies and impact
10. Archive
11. End of turn (by autoCommit mode)
12. Conflicts and paused sync
13. Concurrent agents
14. Cold start and import
15. Cross-scope work
16. Waiting and external blockers
17. Unsupported operations
18. Doctor and integrity warnings

### Core work loop (locked)

Primary agent loop for project work in a registered ambient scope:

```text
tk create "Title"               → path (status: draft; frontmatter + H1)
# file tools: fill body under the H1 (skill Body conventions)
tk status <id> todo             → path (promote when implementable)
tk next --claim                 → path (select + claim in-progress)
# file tools on that path
tk status <id> done             → path under archive/ (or review / blocked / cancelled)
# end of turn: see End of turn (by autoCommit mode) — not always tk sync
```

Rules:
- Prefer `tk next --claim` when starting implementation (select + claim under flock).
  `tk next` without `--claim` is a pure read (inspect only) — never assume a claim from
  it. Manual claim remains valid: `tk status <id> in-progress` after `tk next` or
  when the id is known.
- Only built-in `todo` is next-eligible. Custom statuses (even `category: active` or
  names like `ready`) never enter `next` — promote to `todo` when work should be queued.
- Do not `tk next`-expect or claim a `draft`. Promote with `tk status <id> todo` only when
  the body is implementable.
- After claim, edit the file at the printed path. Do not re-resolve by guessing filenames.
- Known id: `tk get <id>` → path. Short form is an exact short-id match (length 4–8) in
  the ambient scope (`--scope` / `TK_SCOPE` / cwd) — including collision-repaired ids, not
  only create-length 4; full `<scope>-<short-id>` addresses any registered scope (no
  ambient needed for full id). Every ambient project verb accepts optional `--scope`
  (Resolution); doctor does not.
- Inspect header without opening the body: `tk meta <id>` (read-only; preamble + raw
  frontmatter). Do not parse `path:` from `meta` for hand-off — use `get`/`next`/`status`/
  `create`. Do not invent `tk meta` mutation.
- Status values are labels, not a workflow graph: any known status jump is legal
  (`draft -> todo`, `draft -> done`, `todo -> draft`, …); tk validates membership only
  (built-in or CUE custom).
- End of turn is mode-dependent (End of turn section). Do not cargo-cult `tk sync` on
  repo-driven or plain-files scopes. Any `tk create` this turn makes the mode-appropriate
  durability boundary **mandatory** (sync / host commit / disk-only) — see Capture.
- When stderr carries integrity or doctor-class warnings, run bare `tk doctor` first
  (report only). For `duplicate_id:` / `equal_order:` / `archive_non_terminal:` /
  `archive_terminal_at_root:`, run `tk doctor --repair` when ready to mutate **with
  ambient or `TK_SCOPE` set** (or prefer `tk status` for archive layout when the intended
  status is clear); use `--all` only when the human wants every registered scope repaired.
  Without ambient, do not invent machine-wide `--repair`. For over-long order,
  `tk doctor --re-space-order` only if the report calls for it (same target rules).
  Escalate human-only classes (conflicts, name drift, residue). Skill body: Doctor and
  integrity warnings.

### Capture (locked)

- `tk create "<title>"` scaffolds frontmatter (default status `draft`) plus H1 `# <title>`,
  prints path; never self-commits (any status). Fill the rest of the body via file tools.
- Durability after create (locked — do not assume more): on disk and in the index only.
  Not durable-in-git and not durable-remote until the mode-appropriate boundary:
  tk-driven → `tk sync` (snapshot commits the allowlisted scaffold); repo-driven → host
  repo commit/PR; plain-files → disk is the whole story (no git). Same durability class
  for every create status, including terminal. A path under `archive/` with status `done`
  is **not** proof of a git commit. A crashed session after create without that boundary
  can leave an orphan scaffold on one machine only.
- **Any create this turn makes the mode-appropriate durability boundary mandatory** before
  the agent ends the turn (not optional hygiene). Tk-driven: `tk sync` when git is ready
  (or report `sync_disabled:` and that files are disk-only until setup). Repo-driven: ensure
  host commit/PR includes the create (heed `uncommitted:`). Plain-files: disk is enough;
  still report the new path. Do not leave a create this turn unsynced / uncommitted when
  the mode has a git boundary.
- Optional second positional: any known status. `todo` when the body is already known in
  the same turn; `backlog` for capture without authoring soon; `done` / `cancelled` (or
  custom done-class) as a terminal status **label shortcut** (no fake queue ceremony) —
  not a complete-state self-commit and not proof of git durability. Terminal create writes
  under `archive/` and may print a stderr durability cue; non-terminal create writes at
  dir root.
- After create: write the project writing-guide sections under the H1, then
  `tk status <id> todo` when implementable. Leaving a bare scaffold as `todo` is a misuse
  — that is what `draft` is for. Note: promoting with `tk status` *does* self-commit when
  auto-commit is available; create never does.
- Summary, depends, related, tags, links: set by direct frontmatter edit after create (no
  create flags for those in v1).
- Create always appends on `order` after the scope-wide max key; placement on the active
  board is `tk order` after promote when needed.

### Frontmatter mutation (locked)

Inspect (read-only): `tk meta <id>` prints a fixed preamble (`id`, `title` from H1,
`path`) and the project's frontmatter YAML exactly as stored — never the body, never a
write. Use after direct edits to confirm; agents still locate for edit via `get`/`next`/
`status`/`create` paths.

| Key | How to change |
|---|---|
| `id` | Never. Minted at create; stable forever. |
| `created` | Never. Set once at create. |
| `order` | Only via `tk order`. Never hand-edit. |
| `status` | Prefer `tk status <id> <status>` when the file parses (load-bearing: verb moves root ↔ `archive/` on the terminal boundary). Claim-from-queue: `tk next --claim` sets `in-progress` only. Free FM edit of `status` alone does not move the file — layout drift until `tk status` or `--repair`. If `parse_error:`, do not use the verb — fix the file at the `get` path first. Direct edit when resolving `status_conflict` or mid-file repair under human direction. |
| `status_conflict` | Only when resolving a status dispute (at least one terminal involved): set `status`, remove this key. Never invent it. |
| `depends`, `related` | Direct frontmatter edit; each entry a **full** `<scope>-<short-id>` (never bare short-id). Inspect lists in `tk meta`; neighbourhood with `tk deps` (read-only). |
| `tags`, `links`, `summary` | Direct frontmatter edit. Inspect with `tk meta`. |
| Custom fields (`tk.cue` `fields`) | Direct frontmatter edit. No `tk set`. Inspect with `tk meta`. Absent is always legal. |
| Undeclared keys | Avoid; doctor warns. Do not invent schema. Still visible in `tk meta`. |

After direct edits on auto-commit scopes, end-of-turn `tk sync` commits them. Prefer verbs
for status/order so self-commit and validation run on the write path.

### Body conventions (locked)

The markdown body is the project document handed to a fresh implementer session. CLI does
not model tasks or sections — convention only. This skill is self-contained: the section
list below is the primary contract. An org writing guide (e.g. `start get
project/writing` or equivalent) is an **optional** equivalent when present — not a
required dependency for open `tk`.

`tk create` writes the H1 (`# <title>` from the create argument). Retitle that H1 freely
afterward (slug stays frozen). Under the H1, use this section order when authoring for a
fresh implementer:

1. Goal
2. Scope
3. Current State
4. References (omit if none)
5. Requirements
6. Constraints
7. Implementation Plan
8. Implementation Guidance (omit if nothing non-obvious)
9. Acceptance Criteria

Right-size: omit sections that do not apply. Bias the document at the principled
long-term solution; define what/why, not keystrokes; no conversation references
("as discussed").

Also:
- Optional checkbox tasks under Implementation Plan or a Tasks subsection for local
  progress — never via CLI.
- When `status` is `blocked`, put the reason in the body (a short Blocked note is fine).
- `summary` frontmatter: one-line what/why for list/search; keep in sync with Goal when
  practical.
- List/search/meta "title" is the body H1 per closed extraction (below). Create provides
  an ATX H1 immediately. Fill the guide sections under the H1 before `tk status <id> todo`
  when authoring for the queue.

DECISION: title extraction for `list` / `meta` / search display (shared pure helper):
- Scan the markdown **body only** (after the closing frontmatter fence).
- Recognise **ATX H1 only**: a line matching `^#\s+.+` (one `#`, not `##`). Strip the
  leading `#` and surrounding whitespace; that text is the title.
- **First** such line wins; later H1s are ignored for the title field.
- **Setext** H1 (underline `===`) is **not** recognised — treat as body prose.
- No matching line → empty title string (not an error; list/meta still succeed).
- Do not use filename slug or `summary` as a fallback title in v1 (summary is its own
  field when shown).

### Title, slug, and filename (locked)

- Filename shape: `<id>-<slug>.md`. Slug is `slugify(create-title)` once at create
  (closed grammar and algorithm in design Project ids) and never updated. Empty title
  after trim is a usage error; do not invent a slug by hand.
- Retitle freely in the H1/body. Do not rename the file; do not edit `id`.
- `tk doctor` reports structural id/filename/slug-shape mismatch only — not H1/slug drift.
- Always reopen via `tk get`/`next`/`status` paths, not by reconstructing a slug from the
  current title or re-running `slugify` on the H1.

### Ordering (locked)

- Never hand-edit `order`. Use `tk order <id> (--before <id> | --after <id> | --first |
  --last)` only (destination flag required).
- `tk create` always appends (`keyBetween(last, null)`). `last` / `--last` / `--first` use
  the scope-wide max / min valid `order` among **all** projects in the scope (every
  status, including under `archive/`), not the default list set — intentional global
  domain (see Metadata order rank domain DECISION), not active-board append. No
  create-time `--first` / `--before` / `--after` / `--last`.
- Typical flow: create (draft) → fill body → `tk status <id> todo` → `tk order …` if the
  new work should not sit after historical max (e.g. past done projects at the rank
  tail).

### List and filters (locked)

- Default `tk list`: active board set (includes `draft`, `todo`, `review`, `in-progress`,
  `blocked`, and custom `category: active`; excludes `backlog` / done-class unless filtered).
- Single-scope: ambient or `--scope S` (not machine-wide). Status filter: zero or more
  status name positionals = union. A name is **known** only as built-in or custom in the
  **target scope's** `tk.cue`; unknown → exit 2. No `--status`, no CSV.
- Flags: `--scope S`, repeatable `--tag T` (OR across tags), `--all`, `--no-lens`.
- Lens applies by default; `--no-lens` bypasses it. Lens AND `--tag` when both apply.
  Untagged projects are never hidden by a lens. If `tk next` reports nothing ready under
  the lens while N ready outside it, heed that stderr: try `--no-lens`, retag, or clear
  the lens — do not invent work or ignore ready projects outside the filter.
- No `--archived` (terminal storage is `archive/`; status filters and `--all` are enough).
  No date filters on list — use read-only `tk query` for ad-hoc cuts.
- Sort: `(order, id)`.
- One TSV line per project (filters change rows only, not columns):
  `full-id`, `status`, `title`, `summary`, `waiting-on`. No path column — open with
  `tk get <full-id>` (or `tk next` / mutators that print paths).
- `title` = body H1 (shared helper); `summary` = frontmatter or empty; `waiting-on` =
  space-separated unmet direct depend full ids (sorted) or empty. Empty result = exit 0,
  no lines. Lens echo and integrity tokens on stderr only — never ANSI or free-text
  inside TSV fields (colour / TTY: no ANSI on stdout TSV or path lines at all).

### Search (locked)

- `tk search <terms> [--scope S]` — machine-wide FTS by default; bound with `--scope`.
- One TSV line per hit (best bm25 first): `full-id`, `status`, `title`, `summary`,
  `absolute-path`. Open via the path field; do not invent filenames from titles.
- Empty result is success (exit 0, no lines). No lens. Includes archived terminals.
- `parse_error` hits may appear (status field empty; path still openable via path column
  or `tk get`). Treat body as untrusted until fixed; do not use `status`/`order`/
  `next --claim` until parse succeeds.
- No score column. No status filter flags — use `list` for board cuts.
- Terms required; empty terms → usage exit 2.

### Dependencies and impact (locked)

- Author `depends` and `related` by direct frontmatter edit after create (no `tk deps add`
  / remove; no create flags for edges in v1). Every list entry is a **full**
  `<scope>-<short-id>` — same-scope and cross-scope alike; never a bare short-id in the
  file (CLI short form is only for verbs like `get`/`status`). `depends` gates
  runnability; `related` is soft "see also" and never gates.
- Inspect edges with `tk deps <id>` (alias `tk depends <id>`). Default: direct neighbours
  in three sections — depends on, is depended on by, related (both directions). Prefer this
  over free-form `tk query` (schema not stable; query is debug/human ad-hoc, not agent
  automation).
- Before a large claim, cancel, or hub `tk order`: `tk deps <id> --transitive` for the full
  flat prerequisite and dependent sets. Humans browsing structure: `tk deps <id> --tree`.
- `tk next` skips a `todo` whose `depends` are not all terminal; `tk list` puts those
  unmet full ids in the TSV `waiting-on` field. Claiming is `tk next --claim` (preferred)
  or `tk status <id> in-progress` — never via `deps`.
- Open a neighbour to edit: `tk get <dep-id>` → absolute path → file tools. Do not invent
  filenames from titles.
- If `tk deps` warns of a depends cycle, run `tk doctor`. Do not ignore cycle or integrity
  warnings.
- Unresolvable targets stay listed and annotated (held for gates — see design Status and
  dependencies).

### Archive (locked)

- Location follows terminal-ness (see design "Done and archive"). There is no `tk archive`
  or `tk unarchive` verb.
- Finish work with `tk status <id> done` (or `cancelled` / custom done-class). The status
  verb moves the file under `archive/` and prints the post-move path. Create with a
  terminal status writes the scaffold under `archive/` already.
- Reopen with `tk status <id> todo` (or another non-terminal): the status verb moves the
  file back to the dir root. Labels, not a workflow — legal; rare for agents.
- Do not hand-move project files between dir root and `archive/`. Layout drift from
  hand-edited status is reported as `archive_non_terminal:` /
  `archive_terminal_at_root:`; fix with `tk status` (preferred) or `tk doctor --repair`.
- `tk next` never hands a path under `archive/`. Terminal projects stay get / meta /
  search / deps resolvable; default list hides done-class; `--all` brings them back.

### End of turn (by autoCommit mode) (locked)

Branch on the ambient scope's mode (from `tk.cue` `autoCommit` + whether the dir is in
git — labels: tk-driven / repo-driven / plain-files):

| Mode | End of turn |
|---|---|
| tk-driven (`autoCommit: true`) | **Mandatory** when this turn ran `tk create` or other allowlisted dirty work needs the push boundary: `tk sync` when git+upstream exist (use `tk sync --all` when cross-scope gates need fresh remotes). If stderr shows `sync_disabled:`, set up the repo/remote with plain git first — file writes already landed; no inventing `git init` via tk. Sync is the first git/remote durability for any create scaffold (including terminal under `archive/`). |
| repo-driven (`false` inside git) | Do **not** call `tk sync` (it refuses). **Mandatory** host commit/PR for any create or complete-state write this turn. After the turn, bare `tk doctor` (or heed write-side warnings): if `uncommitted:` appears, stop and ensure host commit/PR — do not invent tk commit. |
| plain-files (`false` outside git) | Do **not** call `tk sync` (it refuses). Run bare `tk doctor` if integrity warnings appeared or after multi-machine file sync; `tk doctor --repair` under ambient/`TK_SCOPE` when acting on `duplicate_id:` / `equal_order:` — **one machine at a time** (flock is not cross-host); `--all` only if every local scope should be repaired; let external sync settle before another peer repairs the same tree. Creates are disk-only (no git boundary). |

Never invent `tk save` / `tk end`. Mode is a property of the scope, not a per-command flag.
Never treat `tk create`'s printed path (or `archive/` + `done`) as proof of a git commit
or remote push. Any create this turn → durability boundary is mandatory (Capture).

### Conflicts and paused sync (locked)

Fail fast. Do not keep authoring on a conflicted or mid-rebase auto-commit git-root.

| Signal | Agent action |
|---|---|
| Body conflict markers in a project file | Stop. Report path. Do not pick a side or delete markers unless the human already directed the resolution. Body-only markers do **not** set `parse_error` (FM still indexed); do not treat body prose as trusted until markers are gone. Human edits body → `tk sync` to resume. |
| Markers inside frontmatter / broken YAML | `parse_error:` quarantine — open `tk get` path; fix FM; `status`/`order`/`next --claim` refuse until parse succeeds. |
| `status_conflict` in frontmatter | Stop. Report path and the two disputed statuses (`tk meta` / `tk get` / doctor). Do not choose unless the human (or explicit task) already picked one; then set `status` (either listed value or another known status), remove `status_conflict`, `tk sync`. |
| `tk sync` reports a delete/edit handoff | Stop. One machine deleted the project file while the other edited it; sync resolves neither. Report the path, which side deleted, and the surviving edit's `status`. Do not restore or re-delete on your own judgement — the human decides, then `tk sync` resumes. Re-running `tk sync` without acting re-pauses on the same handoff **by design**: it is not a transient failure and retrying is not progress. The three resolutions are the human's — remove the file (deletion wins), edit it (surviving edit wins), or `git add` it (kept as-is). |
| Self-commit / complete-state verb refuses mid-rebase | Stop. Do not retry `status`/`order`/`next --claim`/`create`/`doctor --repair`. Report the refused command and named file/scope. Resolve body markers / `status_conflict` via `tk edit` or file tools, then `tk sync`. Do not invent alternate write verbs. |
| `tk sync` pauses / reports unresolvable conflict | Stop the turn's project work on that repo. Surface sync output. No parallel "fix it in the background". |

Never invent merge resolution heuristics (prefer-done, LWW body, etc.). Non-auto-commit scopes have no tk rebase seam; host-repo markers still stop-and-report — body-only: resolve text in-file (no `parse_error`); FM markers / broken YAML: `parse_error` / doctor.

### Concurrent agents (locked)

`tk next` without `--claim` is a pure read (two inspects can see the same ready
project). **Same working
tree:** `tk next --claim` serialises under the scope flock with local CAS — two claim
attempts cannot both hand off the same id. **Cross-clone / multi-machine:** no distributed
lock; claims become visible after `tk sync` (or host/external sync). No assignee field,
no claim lease, no push-on-claim in v1. Scope `flock` does not cover body edits via file
tools. No extra file-lock machinery in v1.

Safe practice:
- Prefer one writer agent per scope working tree when possible.
- Start work with `tk next --claim` (not `tk next` then a delayed status write).
- Do not file-tool write (or `$EDITOR` via `tk edit`) on a project body without a claim
  (`in-progress` via `--claim` or `tk status`) — flock covers tk verbs only, not
  concurrent body edits.
- If the project is already `in-progress` (or another agent clearly owns it), do not take
  it: run `tk next --claim` again or stop and report. Do not double-edit the same path.
- Multi-user / multi-clone: after claim, `tk sync` when others may take queue work so the
  claim is visible; do not invent a second lock in the project file.
- Abandoned claim: if work stops mid-claim, leave a clear body note when possible; doctor
  soft-warns `stale_in_progress:` after 72h without file mtime activity. Recovery is a
  deliberate `tk status` back to `todo` (or `blocked`), never automatic.

### Cold start and import (locked)

Registry only — tk never scans the tree for an unregistered `tk.cue` and never
auto-registers on clone. When importing on a second machine, use the same scope **name**
already in that scope's `tk.cue` (import reads it); do not invent a different local name
for the same work set.

Bootstrap (user/org choice — see Discovery): `tk skill` on demand, skill install when
available, or own handoff (AGENTS.md / docs). All paths still need registration before
project verbs.

When there is no ambient scope:
- Do not probe for scope dirs or invent paths.
- Do not treat `tk skill install` as available in v1 (hard-refuse placeholder).
- Prefer `tk skill` if the agent can run the CLI and needs the contract; otherwise use a
  path from the human or from project docs (e.g. a one-liner in AGENTS.md naming the
  scope dir). Then: `tk scope import <dir> [--code-root <path>]` or
  `tk scope rebind <dir> --name <scope> [--code-root …]` / `--scope` / `TK_SCOPE` as
  appropriate.
- `tk skill` itself needs no scope — print the contract, then fix registration before
  project verbs.

Own bootstrap (human-authored, never written by tk, never auto-committed by tk): document
the scope dir in the host repo (repo-root AGENTS.md or equivalent) so a cold agent can
import without guessing. Durability of that handoff is host git, not `tk sync`.

### Cross-scope work (locked)

- Address other scopes with full ids (`<scope>-<short-id>`). Never invent a scope name;
  only use names from `tk scope list` / registered registry.
- Scope names are fleet-global **by convention**, enforced only per machine. On every
  machine that resolves a cross-scope id, register the **same** name for the same scope
  dir. Do not reuse a short name (e.g. `api`) for a different scope on another machine —
  tk cannot detect that clash and a `depends` gate would hit the wrong project.
- If `name_drift:` appears (registry key ≠ `tk.cue` name after a remote scope rename):
  that scope is fail-closed — forget+import before any project verb. Do not rely on
  short ids under the old registration.
- Author `depends` / `related` by direct frontmatter edit (same- or cross-scope). Inspect
  with `tk deps <id>` — read-only; do not invent edge verbs.
- If `tk next` / `list` / `deps` annotate that a depended-on scope is not registered here:
  stop and ask for import/clone of that scope. Do **not** clear the edge to “unblock”
  yourself — the hold is intentional.
- Cross-scope gate freshness: `tk next` / list only reconcile **local disk** for
  depended-on scopes — they do not fetch remotes. Bare `tk sync` only fetches the ambient
  auto-commit repo. When work depends on status in another auto-commit scope (especially
  after multi-machine edits), run `tk sync --all` or sync that scope before trusting the
  gate. Repo-driven / plain-files: no tk sync — freshness is the host/external sync of
  those trees.
- Shared git-root coupling: several auto-commit scopes in one repo share one push and one
  freeze domain. A conflict, `status_conflict`, or unparseable sibling `tk.cue` can block
  sync/writes for **all** those scopes until fixed. That is the price of one-push sync —
  not a bug. Need isolation → separate git-root (do not invent per-scope sync isolation
  flags). See Auto-commit DECISION on multi-scope messaging.

### Waiting and external blockers (locked)

Use the right mechanism — do not overload one label for every kind of wait:

| Situation | Use | Do not |
|---|---|---|
| This project cannot start until another **project** is terminal | `depends: [<scope>-<short-id>]` full id only (same- or cross-scope) | `blocked` alone for a project dependency; bare short-id in the list |
| Stalled on a **human or external** factor with no project id | `tk status <id> blocked` and write the reason in the body; put PR/issue/URL in `links` | Fake a `depends` on a non-project |
| The **work product** is under review (plan or result) | `tk status <id> review` | `blocked` unless review is stuck on a person/process outside the review itself |
| Soft “see also” / provenance | `related: [<scope>-<short-id>]` full id only | Using `related` or tags as a runnability gate; bare short-id in the list |
| Topic / area only | `tags` | Encoding wait state in tags |

`depends` is the only project-to-project gate for `tk next`. `blocked` is manual and
human-owned — tk never auto-sets it. Inspect edges with `tk deps`; edit edges in
frontmatter.

### Unsupported operations (locked)

Do not invent verbs or flags. v1 does not support:

| Do not | Instead |
|---|---|
| Transfer / split / merge / copy a project across scopes | `tk create` in the target scope; `related` or `depends` as needed; `cancelled` or leave the old one |
| Task-level CLI (checkboxes as objects) | Edit body checkboxes/sections with file tools |
| `--json` or machine envelopes | Paths + short text; open the file |
| `tk deps` mutation (`add`/`rm`) | Edit `depends` / `related` in frontmatter; `deps` is read-only |
| `tk set` / `tk field` / `tk meta` mutation | Direct frontmatter edit (customs per `tk.cue`); `meta` is read-only inspect |
| `tk query` mutating SQL (`INSERT`/`UPDATE`/`DELETE`/`DROP`/…) | Read-only `SELECT` (and read-only explain); durable change is files / doctor |
| Agent automation via free-form `tk query` / index schema | Prefer `deps` / `list` / `search` / `next` / `get` / `meta` — query is debug/human ad-hoc only; schema not stable |
| `tk archive` / `tk unarchive` | Location follows terminal: `tk status … done` moves into `archive/`; `tk status` to non-terminal moves out; doctor `--repair` fixes drift |
| `tk next` (no `--claim`) as a claim | `tk next --claim`, or `tk next` then `tk status <id> in-progress` |
| Claim leases, push-on-claim, `claim_term` / assignee fields | Local CAS on `--claim` only; multi-clone visibility via sync; stale → doctor + deliberate status |
| `tk skill install` (v1) | `tk skill` print; human AGENTS.md path for import |
| Hand-edit `id`, `created`, or `order` | Verbs: create/status/order only for those concerns |
| Hand-rename `<id>-<slug>.md` to chase a new title | Slug frozen; retitle H1 (and optional `summary`) only — three names may diverge |

If a need is not on the CLI surface, stop and ask — do not improvise a parallel tool.

### Doctor and integrity warnings (locked)

DECISION: machine-actionable integrity and doctor-class signals use a **closed set of
stable tokens** as line prefixes (stderr on ordinary commands; doctor report lines too).
No `--json` envelope. Human-readable detail may follow the token on the same line (or
subsequent indented lines). Adding a token is a conscious design change; do not invent
ad-hoc prefixes in implementation. Token characters themselves are never ANSI-coloured
or interrupted (see colour / TTY DECISION); agents may match prefixes without an ANSI
strip step.

Two consumption rules (both required — tokens alone are not the whole doctor UX):

1. **Command stderr:** when a line prefix is in the closed set, never ignore it — act per
   the agent rules below or run bare `tk doctor`.
2. **Bare `tk doctor`:** read the **full report** (token lines and any short human
   summary). Do not claim “tokens only, skip the rest of doctor.” Doctor may still use
   free prose for rare or purely informational notes; those without a token are
   human-priority, not agent-automation keys.

Closed v1 token set (prefix form, including the trailing colon). Every class that should
drive agent action has a token; implementers emit these from doctor and from hot-path
warnings where the design already rides stderr.

| Token | Meaning / agent action class |
|---|---|
| `duplicate_id:` | Two or more projects share an id — bare doctor then `--repair` when ready; id-taking verbs refuse (no path) until unique |
| `equal_order:` | Two or more projects share an `order` key — bare doctor then `--repair` when ready |
| `order_long:` | Pathologically long `order` key(s) — report; optional `--re-space-order` |
| `parse_error:` | Frontmatter unparseable (broken fence/YAML, markers **inside** FM, bad `order`, …) — quarantined; `get` hands path **exit 0** + this token on stderr; `status`/`order`/`next --claim` refuse (non-zero) until parse succeeds. Body-only conflict markers do **not** emit this token |
| `unreachable_scope:` | Registered dir could not be stated/opened (missing, unmounted, permission, I/O) — report only; keep index rows; do not auto-forget; doctor may include the OS error string; human decides wait vs `tk scope forget` |
| `non_allowlist:` | Path under scope dir outside allowlist — move/remove; never force-commit |
| `config_unparseable:` | `tk.cue` or XDG CUE will not load — fix config; writes/sync may be blocked |
| `status_conflict:` | Status dispute key present — resolve in-file; mid-rebase then sync |
| `depends_cycle:` | Depends cycle — fix edges; do not ignore |
| `depends_dangling:` | Same-scope `depends` target missing — hard; fix or remove edge |
| `depends_self:` | Project lists its own id in `depends` — hard; remove self-edge |
| `depends_unresolvable:` | Cross-scope `depends` target not resolvable here — informational hold; import/clone or clear edge only with human intent |
| `depends_on_cancelled:` | Depends on a cancelled (or done-class abandoned) project — human decide if still valid |
| `edge_verify:` | Edge (`depends` or `related`) may mispoint — inbound edge to a collision-repaired id, or cross-scope inbound after scope rename. Emitted only in the repairing operation's output (sync integrity / `doctor --repair` / `scope rename`), never by later bare doctor (not persisted, not re-derivable) — human verify from that report |
| `related_unresolvable:` | Soft related target missing — cosmetic; note only |
| `auto_commit_mismatch:` | autoCommit disagree across shared git-root — fix before sync |
| `archive_non_terminal:` | Non-terminal status under `archive/` — layout drift; `status` or `--repair` moves to root |
| `archive_terminal_at_root:` | Terminal status still at dir root — layout drift; `status` or `--repair` moves to `archive/` |
| `sync_disabled:` | Auto-commit: no git-root and/or no upstream for sync — see Writes / Sync |
| `last_push_error:` | Last auto-commit push failed — XDG `git-roots/<key>/last-push-error`; fix remote/auth, sync again |
| `stale_in_progress:` | Built-in `in-progress`, mtime older than 72h — inspect; maybe reopen to todo |
| `name_drift:` | Registry key ≠ `tk.cue` name — forget+import; scope unusable until then |
| `uncommitted:` | Repo-driven allowlisted dirty files — host commit |
| `schema_error:` | Frontmatter hard schema violation (unknown status, bad field type/`values`, `depends`/`related` entry not a legal full project id) — fix file; a malformed `depends` entry counts as unmet and holds the project out of `tk next` |
| `schema_warn:` | Soft schema (undeclared key, `knownTags` typo, self-`related`, duplicate list entries, id-shaped `links`) — fix or ignore deliberately; no separate `related_self:` |

Example shape (illustrative):

```text
duplicate_id: wc-ab2c in scope wc (2 files) — run tk doctor
equal_order: 2 projects in scope wc share order "a1" — run tk doctor
depends_dangling: wc-ab2c depends on wc-zzzz (missing)
```

Agent rules:
- Never ignore a closed-set token on stderr. Prefer bare `tk doctor` for the full picture,
  then act. On bare doctor, read the whole report, not only lines you regex for tokens.
- `duplicate_id:` / `equal_order:` → after reviewing the report, `tk doctor --repair`
  under ambient or `TK_SCOPE` for that scope (mutates; auto-commit self-commits when a
  git-root exists). Machine-wide only with explicit `tk doctor --repair --all` when the
  human intends every scope. Do not assume bare doctor rewrote files. While
  `duplicate_id:` stands, do not expect `get`/`status`/… on that id to return a path —
  they refuse until repair. No `tk doctor --scope` — use ambient cwd, `TK_SCOPE=<name>`,
  or `--all` for mutating flags; bare `--repair` with no ambient is a usage error.
- `order_long:` → `tk doctor --re-space-order` only when chosen; not part of `--repair`
  (same mutating target set: ambient / `TK_SCOPE` / `--all`).
- Plain-files multi-machine: no `tk sync` seam — bare doctor when tokens appear and
  periodically after external file sync; `--repair` when acting on collision/equal-order
  on **one** machine at a time under ambient/`TK_SCOPE` (do not race dual `--repair`
  across peers; do not use `--all` unless every local scope should be repaired).
- After human conflict resolution (body markers / `status_conflict`), run bare `tk doctor`
  if unsure, then `tk sync` on tk-driven scopes to resume.
- `tk doctor --reindex` only when the mtime heuristic is fooled (restore, clock skew) —
  rare escape hatch, never routine; never mutates project files.
- After `--repair` / `--re-space-order`, read the report; do not re-introduce hand-fixed
  ids that fight the repair.
- `stale_in_progress:` → open the path, check whether work is still live; if abandoned,
  `tk status <id> todo` (or `blocked` + body reason). Never invent auto-reopen or
  auto-steal on `next --claim`.
- `name_drift:` → stop project work on that scope. Run exactly
  `tk scope forget <old>` then `tk scope import <dir> [--code-root …]` (names/paths from
  the doctor/error text). Re-set `tk lens` if needed. Do not invent ambient short-id
  workarounds or auto-rekey.
- `uncommitted:` → repo-driven only. Do not call `tk sync`. Hand off to host git/PR (or
  human). Writes already landed on disk; durability needs the host commit.
- `unreachable_scope:` → registered dir could not be stated/opened (missing, unmount,
  permission, I/O — one token; no separate “path gone forever” token). Do **not**
  `tk scope forget` solely for this. Retry when the path is back; forget only when the
  human decides the registration should end permanently.
- `parse_error:` → open the path from `tk get` (or doctor); `get` exit 0 + path is
  hand-off success, not a healthy project. Fix frontmatter (YAML, fence, markers inside
  FM). Do not expect `tk status`/`order`/`next --claim` to succeed until reconcile
  parses again. Body-only markers without this token are a conflict handoff, not
  quarantine — stop-and-report per Conflicts; do not invent FM repair.
- `depends_dangling:` / `depends_self:` / `schema_error:` / `config_unparseable:` /
  `auto_commit_mismatch:` / `last_push_error:` / `edge_verify:` / `depends_cycle:` → fix
  or escalate from the report; do not silence by ignoring the token.
- `archive_non_terminal:` / `archive_terminal_at_root:` → prefer `tk status` to the
  intended status (moves layout); or `tk doctor --repair` to reconcile layout to status.
  Do not hand-move files between root and `archive/`.
- `depends_unresolvable:` / `related_unresolvable:` / `schema_warn:` → note; do not clear
  edges or invent fixes without intent.

## Borrowed from beads

beads got the interface right even though its Dolt storage is overkill here.
- `ready` as the primary verb -> `tk next` (prints path).
- Dependency-gating derived rather than a hand-set flag.
- `StatusCategory` -> CUE custom-status categories.
- A `prime`/`onboard` context dump -> `tk skill`.
- An agent-facing integration artefact -> user-initiated `tk skill install` (planned via
  agentdex; beads auto-maintained an AGENTS.md block; tk makes installation a deliberate
  user act, never an auto-write into a tree it does not own). v1 ships print + hard-refuse
  install family placeholders.
- One logical operation = one auto-commit, auto-messaged (when `autoCommit: true`).
- beads' `--json` everywhere: not adopted — path + short text is the agent interface.

Shrunk ruthlessly: ~15 dependency types become two (blocking `depends`, soft `related`);
`pinned`/`hooked` orchestration
statuses dropped (a closed set of eight, no lifecycle machinery).

## Anti-goals (avoid becoming beads)

Honest bound: complexity is pushed into the CLI and the skill dump so **markdown stays
dumb** (one file per project, closed frontmatter, no lifecycle machinery in the tree).
"Small" applies to the **file model and the verb count**, not to implementation size,
sync/merge surface, token catalogue, or skill section count. A large operational surface
with a **closed** repair budget is intentional — not beads-scale open-ended recovery,
and not a promise that the CLI source or skill text is tiny.

- No database as source of truth. Frontmatter makes adding a field free and removing one
  a grep-and-delete — no migrations, no dead columns. The index is a derived view; the
  registry is small machine-local config; authority stays in the files.
- No scope explosion. beads grew molecules, swarms, gates, wisps, federation, and
  GitHub/Jira/Linear/Notion sync. Anchor: a small closed built-in set (eight statuses,
  two project-to-project edge kinds, a compact verb surface) over one file per project,
  no lifecycle machinery; CUE customs for anything beyond. Multi-machine recovery is a
  closed auto-repair budget (see "Project ids"), not an open-ended resilience layer.
- No double handling (the beans sin): files are edited in place; the CLI never asks for
  a temp file to be handed to it.

## Open questions

None outstanding for product open design. Historical resolutions live in the body under
`DECISION:` markers; use the Decisions index below only as a section map (not a second
copy of the rules).

## Decisions index

The body is the only source of truth for locked rules (`DECISION:` markers and section
prose). This index is a map only — no restated policy. If an index line and the body
disagree, the body wins; fix the index.

| Topic | Section |
|---|---|
| Vocabulary (scope / project / task) | Vocabulary |
| Core model; files as source of truth | Core model |
| Project ids, `IsFullProjectID` / short-id grammar, slugify, mint length 4, collisions, repair, id resolution | Core model → Project ids |
| Id-taking refuse on duplicate id | Core model → Project ids |
| Plain-files repair concurrency (one machine at a time) | Core model → Project ids |
| Scope names; `--auto-name` closed procedure | Core model → Scope names |
| Frontmatter keys; `order` rank domain (global incl. archive — intentional cost); `status_conflict`; `created` | Metadata (frontmatter) |
| Scope directory layout; archive/ flat only (immediate children; nested = residue) | Storage |
| Everything visible | Visibility |
| Ambient resolution; optional `--scope` on ambient verbs (not doctor); registry lookup | Resolution |
| Registry XDG; name drift fail-closed | Registry |
| init / import / rebind / rename / forget | Scope lifecycle |
| CUE config: independent XDG vs tk.cue tiers (not key-merge); ambient separate | Configuration (CUE); Resolution |
| Machine-wide SQLite index; WAL; `BUSY_TIMEOUT_MS = 5000` | Read interface (SQLite index) |
| Reconcile; doctor scope (report ambient-or-all; mutate ambient/`TK_SCOPE`/`--all`; no --scope); --repair; unreachable_scope | Invalidation and reconcile |
| parse_error quarantine; body-aware conflict markers; mutators refuse; get path exit 0 for repair | Invalidation and reconcile; CLI `tk get` |
| search stdout (TSV path hand-off); parse_error hits allowed (empty status); query human/debug; edges full ids | Query surface; Search (skill) |
| list stdout (TSV: id/status/title/summary/waiting-on); single-scope filters | CLI surface; List and filters (skill) |
| scope list stdout (TSV: name/dir/root/mode; mode tk-driven/repo-driven/plain-files/unknown) | CLI surface |
| Absolute path hand-off (get/next/create/status/…); edit empty stdout, no self-commit | CLI surface |
| Exit codes (exit 2 = usage / malformed id / unknown status; unknown id = exit 1; get exit 0 under parse_error when path printed) | CLI surface; Project ids |
| Sync model; mid-rebase command classes (incl. doctor --repair refuse); empty auto-commit set exit 0; `--all` per-git-root failure isolation; allowlist (deleted path → `tk: remove`); XDG git-root ops state | Sync model; Discovery |
| Merge conflict handling; frontmatter merge package (stage absent ≠ empty); `status_conflict` in the stages; delete/edit handoff; unknown status name fails closed; body markers not scanned at `--continue` | Merge conflict handling |
| Statuses; only built-in todo next-eligible (no custom ready); depends/related full ids; deps; `next --claim` (local CAS; `next` without `--claim` read-only) | Status and dependencies |
| Tags and lens | Tags and lens |
| Done and archive; layout follows terminal; status verb load-bearing (not free status-key only); writer creates archive/ on demand | Done and archive |
| Discovery; skill install placeholders | Discovery |
| Agent skill contract (locked TOC); skill extract subordinate to body DECISIONs | Agent skill contract |
| Borrowed from beads / anti-goals | Borrowed from beads; Anti-goals |
| Platform: macOS/Linux; pure Go; flock (machine-local) | Platform and portability; Sync/Git |
| Colour / TTY: auto + NO_COLOR; no ANSI on paths/TSV/token prefixes | Scope lifecycle (init flags); Doctor tokens |
