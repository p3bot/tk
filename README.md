# tk — Agent Ticket Management CLI

`tk` tracks feature work as plain markdown files, one ticket per file, edited in
place. It indexes, queues, and locates tickets; the filesystem is the editor.

The implementation is the source of truth.

## Supported platforms

macOS and Linux only. Windows is not supported — there is no flock or path
substitute, and `tk` fails with a clear startup error on an unsupported OS rather
than half-running. This is a deliberate v1 scope limit.

## Installation

### Homebrew (Linux/macOS)

```bash
brew tap p3bot/tap
brew trust p3bot/tap
brew install p3bot/tap/tk
```

### Go Install

```bash
go install github.com/p3bot/tk/cmd/tk@latest
```

### Build from Source

```bash
go build ./...
go build -o tk ./cmd/tk
```

Requires Go 1.26. Pure Go, no cgo. The external `git` binary is used only to
derive a code-root / git-root; it is shelled out, never linked.

## Scopes

A scope is a directory of ticket markdown files plus its `tk.cue` (the scope's
name, auto-commit mode, and schema). Scopes are registered per machine in the XDG
config directory (`${XDG_CONFIG_HOME:-~/.config}/tk/`).

```sh
tk scope init <dir> (--name <name> | --auto-name) [--code-root <path>] [--auto-commit]
tk scope import <dir> [--code-root <path>]
tk scope rebind <dir> --name <name> [--code-root <path>]
tk scope forget <name>
tk scope list          # bare `tk scope` and `tk scopes` also run list
tk scope rename <old> <new>
tk scope field list|set|unset [--scope S]   # declare custom frontmatter fields in tk.cue
```

- `init` creates and registers a new scope, writing a minimal `tk.cue` and a
  `.gitignore` covering `.tk.lock`. Exactly one of `--name` / `--auto-name` is
  required. In a dedicated tk repo, pass `--auto-commit` (omitting it registers
  repo-driven).
- `import` registers an existing on-disk scope, files in place; its name and
  auto-commit mode come from the on-disk `tk.cue`.
- `rebind` rewrites a registered scope's paths after a move or clone.
- `forget` unregisters a scope (registry, lens, me, and note entries only); it never
  touches the scope's files.
- `list` prints parse-stable TSV, one line per scope: `name\tdir\troot\tmode`,
  where `mode` is `tk-driven`, `repo-driven`, `plain-files`, or `unknown`.
- `rename` renames a scope end-to-end (registry, lens, note default, `tk.cue` name,
  ticket ids) and drops this machine's current-ticket pointer for that scope.
- `field` reads and rewrites custom field declarations under `fields:` in the
  ambient scope's `tk.cue` (`list`, `set`, `unset`). Optional `required` is soft
  policy only (`required_missing:` on meta/mark; never a hard refuse).

## Notes

Each scope can keep committed markdown worklogs at `<scope-dir>/notes/<slug>.md`.
They are not tickets: `tk list` does not show them, they are not indexed, and
they are not part of the agent skill. Humans (or an agent that needs session
context) use `tk note` (`notes` is an alias).

```sh
tk note                              # print this machine's default note
tk note [slug]                       # print notes/<slug>.md (one-shot)
tk note --name <slug>                # same as tk note [slug]; never writes the default
tk note list                         # addressable slugs, alphabetical
tk note use                          # print the effective default slug
tk note use <slug>                   # set this machine's default for the scope
tk note use --clear                  # revert to built-in default

tk note add <text...>                # append one line to the default
tk note add --name <slug> <text...>  # append one line to a named note
tk note set <text...>                # replace the default
tk note set --name <slug> -          # replace named from stdin
tk note edit                         # $EDITOR on the default
tk note delete                       # unlink the default
tk note delete --name <slug>         # unlink named (one-shot)
```

`use` is machine-local (XDG `note.cue`, keyed by scope name). Documents stay
committed under `notes/`; the pointer is not stored in `tk.cue`, `me.cue`, or
`lens.cue`. Unset (or `use default` / `--clear`) keeps the built-in slug
`default`. `--name` and a positional slug stay one-shot selectors. Personal
slugs (`grant`, `alice`) with `default` as the shared pad are a convention,
not a CLI rule.

`add`, `set`, `edit`, and `delete` never self-commit. On a tk-driven scope,
`add`, `set`, and `delete` ride `sync_needed: dirty` (same as `tk create`);
`edit` and `use` do not. Durability is `tk sync` on a tk-driven scope, or a host
commit on a repo-driven scope. Slugs follow the
existing ticket-slug grammar (`a-z0-9` and hyphens, 1–48). `list`, `add`,
`set`, `edit`, `delete`, `help`, and `use` are reserved names and cannot be
document slugs. `tk status note` prints the path of `notes/<effective-slug>.md`
whether or not the file exists.

## Output and exit codes

- stdout is a path or closed TSV; diagnostics and closed tokens go to stderr.
- Exit `0` success; `2` for usage / bad CLI input; other failures are generic
  non-zero. There is no `--json` and no colour on stdout. `NO_COLOR` suppresses
  all ANSI.

## Development

| Task | Command |
|---|---|
| Build | `go build ./...` |
| Test | `go test ./...` |
| Format | `gofmt -w .` |
| Vet | `go vet ./...` |
| Lint | `golangci-lint run ./...` |
