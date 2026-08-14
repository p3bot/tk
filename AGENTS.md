# tk — Agent Ticket Management CLI

Guidance for AI agents working in this repository. `tk` is a single-purpose CLI
that tracks feature work as plain markdown files, one ticket per file, edited in
place. The running code and the embedded skill contract (`tk skill` /
`internal/skill/skill.md`) are the source of truth. Archived design prose is not
live authority and must not override the tree.

## Implementation status

P1 through P7 have landed. `tk` runs as a Cobra CLI with the machine-local CUE
registry, scope `tk.cue` evaluation, ambient resolution, and the full `tk scope`
verb set (`init`, `import`, `rebind`, `forget`, `list`, `rename`, `field`); the machine-wide
SQLite index with reconcile, FTS5 search, and the read/board verbs (`list`,
`status`, `get`, `meta`, `next`, `deps`, `search`, `query`, `lens`); the authoring
hot path (`create`, `mark`, `reorder`, `edit`, `next --claim`) with local git
self-commit, and claim-time refresh/push on a tk-driven root with an upstream;
`tk doctor` with its integrity repairs and the closed token catalogue; P6a's
frontmatter merge package (`internal/fmmerge`), the rebase driver
(`internal/rebasedriver`), and the read/integrate/push half of the git wrapper;
P6b's `tk sync` and claim push — snapshot, fetch-and-integrate, sync-time
integrity, push (claim reuses the refresh and push-if-ahead wrappers), the per-git-root preflight, the layer-4 resume contract, the `--all`
per-root failure isolation, and the reentrant lock span (self-commit and repair
orchestration split into acquiring wrappers over locks-held cores); and P7's
`tk skill` — the agent contract (embedded `skill.md` as the sole runtime source:
Commands, Ticket files, Identifiers, Workflows; structure and hot-path guidance
tests; no design-doc dependency) plus agentdex-backed `skill install`/`list`/
`uninstall` (paths from the agent catalog; no hardcoded product skills dirs).

- Prefer packages, tests, and the embedded skill over prose when they disagree.
- Short-ids are letter-first by construction (the `IsShortID` predicate and the
  mint both forbid a leading digit); any `<scope>-<short-id>` example follows
  that rule.
- Do not invent behaviour that contradicts closed contracts already in code
  (token catalogue, id/order/slug grammars, exit codes, tk-owned push — never
  host push). If behaviour is unclear, flag it rather than guessing from archive prose.

## Ticket documents and archiving

Ticket documents are ordinary markdown files in this repo's scope directory
(`.agents/tk/`, one file per ticket: `<id>-<slug>.md`). Active work lives at the
scope dir root; terminal status moves a file into `archive/` via `tk mark`
(do not hand-move).

- When a ticket is complete, set a terminal status with `tk mark <id> done`
  (or another terminal status). That renames into `archive/` in the same write.
- Cross-ticket references use logical labels (`P1`…`P8`) or full ids
  (`tk-mwtc`, …); path or filename references need rewriting after id/slug
  changes.
- Completed historical tickets (P1–P5, P6a, P6b, P7, P8, and later) live under
  `.agents/tk/archive/` as first-class done tickets. Pre-`tk` design prose is at
  `docs/archive/design.md` (history only; not live authority).
- The sync and merge boundary is split across two tickets: P6a (frontmatter
  merge package, rebase driver, git plumbing) and P6b (`tk sync`). Documents
  written before that split refer to the pair as `P6`; a `P6` reference to the
  merge package, the driver, or `internal/git` plumbing means P6a, and one to
  `tk sync`, its integrity step, or its push means P6b. The labels were kept as
  `P6a`/`P6b` rather than renumbering so every existing `P6` and `P7` reference
  in the archive stays valid.

## Module and layout

- Module path: `github.com/p3bot/tk`
- Go version: 1.26 (pure Go, no cgo)
- `cmd/tk/main.go` — minimal entry point: run, map a signal or error to an exit
  code, exit (all command logic is in `internal/cli`)
- `internal/` — pure wire-contract primitives, then the engines built on them:
  - `id` — scope/short-id/full-id predicates, `crypto/rand` mint, collision-repair extension
  - `collision` — pure same-id keeper total order (`KeepBefore`); shared by repair and fmmerge
  - `slug` — `Slugify` and the closed slug grammar
  - `order` — the fractional-index `order` wire format and `KeyBetween`
  - `frontmatter` — fence split, YAML parse/serialize, raw fence-slice API
  - `status` — built-in statuses, the `Category` set, and the terminal predicate
  - `title` — ATX-H1 title extraction
  - `scope` — `--auto-name` derivation
  - `token` — the closed stderr token strings (`name_drift:`, `config_unparseable:`, …)
  - `pathutil` — boundary-safe path predicates (nesting, disjointness)
  - `xdg` — XDG config dir resolution and the machine-global flock
  - `flock` — the POSIX advisory-lock helper behind the scope and git-root locks
  - `atomicfile` — same-dir temp write plus rename, so no reader sees a half-written file
  - `gitroot` — `git rev-parse` code-root/git-root derivation
  - `scopeconfig` — scope `tk.cue` evaluation into the validated `ScopeSchema`
  - `registry` — the XDG registry/lens/me model, CUE read + atomic regenerate
  - `resolve` — ambient scope resolution and name-drift fail-closed
  - `scopeadmin` — scope verbs and the shared registration checks
  - `index` — the machine-wide SQLite read model (WAL, FTS5, tickets + edges)
  - `reconcile` — git-free read-through that brings the index up to date from the files
  - `git` — the external-git wrapper; full read/integrate/push surface (fetch, rebase,
    stage enumeration and reads, blob merge, author date, push, unpushed count)
  - `gitstate` — per-git-root XDG ops state (`sync.lock`, `last-push-error` read/write/clear)
  - `selfcommit` — the single reusable self-commit step for auto-commit scopes
  - `rewrite` — the shared multi-file rewrite durability engine
  - `repair` — deterministic integrity repairs (collision pick via `collision`, re-space, archive move)
  - `fmmerge` — the pure 3-way frontmatter merge over raw stage blobs (P6a); add/add uses `collision`
  - `rebasedriver` — resolves one conflicted ticket `.md` at a paused rebase (P6a)
  - `scopefile` — scope-dir allowlist classification, dirty counting, and per-scope flock acquire
  - `integrity` — doctor diagnose report and shared repair orchestration (acquiring + locks-held core)
  - `syncengine` — per-root snapshot/integrate/integrity/push; `tk sync` and claim
  - `skill` — embedded agent skill contract (`skill.md`; sole source, no design-doc dependency) (P7)
  - `cli` — Cobra command tree, exit codes, signals, colour/TTY, path hand-off

## Build, test, lint, format

| Task | Command |
|---|---|
| Build | `go build ./...` |
| Test | `go test ./...` |
| Format check | `gofmt -l .` (empty output = clean) |
| Format write | `gofmt -w .` |
| Vet | `go vet ./...` |
| Lint | `golangci-lint run ./...` (config in `.golangci.yml`, schema v2) |

## Intended stack

| Concern | Choice | Notes |
|---|---|---|
| Language | Go | Pure Go, no cgo (a git subprocess is not cgo). |
| Frontmatter/config YAML | `github.com/goccy/go-yaml` | Actively maintained pure Go; AST/style control for the force-quoted `order` and undeclared-key retention. |
| Unicode | `golang.org/x/text` | NFKC normalisation for `slugify` (Go has no stdlib normalisation). |
| Config | CUE (`cuelang.org/go`) | Typed, validated schema for scope config and frontmatter. |
| Index | SQLite (`modernc.org/sqlite`) | Pure Go, FTS5 compiled in, WAL mode. |
| Version control | External `git` binary | Shelled out, owner `tk` scopes only. Full commit and read/integrate/push surface built (P6a); `tk sync` and claim (todo→in-progress) are the tk-owned push paths (P6b). |

TIP: Both `modernc.org/sqlite` and `cuelang.org/go` are pure Go by design. Do not
introduce a cgo-based SQLite driver (e.g. `mattn/go-sqlite3`) — it breaks the
"pure Go, no cgo" invariant.

## Go CLI design guide is advisory

The Go CLI design guide (`start get golang/design/cli`) is advisory only.
Adopt its repo-shape conventions — standard layout (`cmd/tk/main.go` minimal,
`internal/…`), table-driven tests with `testdata/`, and a `.golangci.yml`.
The implemented contracts (code and embedded skill) override it on every conflict;
a later ticket does not restate this. Known override points where the tree wins:

- Exit codes and error classes — usage/bad-id `exit 2`, unknown id generic
  non-zero, `duplicate_id:` refuse — over the guide's mapping.
- Output contract — path-centric with TSV/stdout hand-off, not the guide's
  JSON-envelope-first model.
- Configuration model — per-scope `tk.cue` plus a machine-wide registry, not the
  guide's XDG/profile precedence chain.
- Command semantics — one-op-one-commit and path hand-off, not the guide's
  async-job ledger.
