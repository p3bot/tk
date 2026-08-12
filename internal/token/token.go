// Package token defines the closed set of machine-readable stderr tokens tk
// emits at line start. Frozen wire contract: never reword or colour these strings.
// All() is the complete catalogue for completeness checks.
package token

import (
	"fmt"
	"strings"
)

const (
	// NameDrift marks a scope whose registry key disagrees with on-disk tk.cue name.
	NameDrift = "name_drift:"

	// ConfigUnparseable marks a scope whose tk.cue cannot be trusted; writes refuse, reads stay available.
	ConfigUnparseable = "config_unparseable:"

	// AutoCommitMismatch marks divergent autoCommit values under one derived git-root.
	AutoCommitMismatch = "auto_commit_mismatch:"

	// UnreachableScope marks a registered scope whose dir is gone from disk.
	UnreachableScope = "unreachable_scope:"

	// ParseError marks a ticket whose frontmatter could not be parsed; row stays locatable.
	ParseError = "parse_error:"

	// DuplicateID marks two or more ticket files in one scope claiming the same full id.
	DuplicateID = "duplicate_id:"

	// EqualOrder marks two or more tickets in one scope sharing an order key.
	EqualOrder = "equal_order:"

	// ArchiveNonTerminal marks a non-terminal ticket stored under archive/.
	ArchiveNonTerminal = "archive_non_terminal:"

	// ArchiveTerminalAtRoot marks a terminal ticket still at the dir root.
	ArchiveTerminalAtRoot = "archive_terminal_at_root:"

	// DependsDangling marks a same-scope depends target with no matching ticket.
	DependsDangling = "depends_dangling:"

	// DependsUnresolvable marks a cross-scope depends target that cannot be resolved here (informational).
	DependsUnresolvable = "depends_unresolvable:"

	// SchemaError marks a hard frontmatter schema violation (e.g. depends entry not a full ticket id).
	SchemaError = "schema_error:"

	// SchemaWarn marks a soft schema issue (undeclared key, self-related, duplicates, id-shaped links).
	SchemaWarn = "schema_warn:"

	// SyncDisabled marks an auto-commit scope that could not self-commit (no git-root / no git).
	SyncDisabled = "sync_disabled:"

	// Uncommitted marks a host-owned repo-driven scope with dirty allowlisted files
	// (status pulse / bare doctor; write path stays quiet).
	Uncommitted = "uncommitted:"

	// SyncNeeded marks a tk-driven scope whose durability still requires tk sync
	// (dirty allowlist, unpushed self-commits, or a recorded push failure).
	SyncNeeded = "sync_needed:"

	// OrderLong marks a pathologically long order key (soft threshold length > 64); report only.
	OrderLong = "order_long:"

	// StatusConflict marks a ticket carrying a status_conflict merge-dispute key.
	StatusConflict = "status_conflict:"

	// DependsCycle marks a ticket participating in a depends cycle.
	DependsCycle = "depends_cycle:"

	// DependsSelf marks a ticket listing its own id in depends.
	DependsSelf = "depends_self:"

	// DependsOnCancelled marks a depends edge onto a cancelled (or abandoned) target.
	DependsOnCancelled = "depends_on_cancelled:"

	// DependsOpen marks a successful mark into todo/in-progress/review while one
	// or more depends targets remain unmet (same notion as list waiting-on).
	// Soft only: the mark still succeeds; depends continue to gate next/claim only.
	DependsOpen = "depends_open:"

	// RelatedUnresolvable marks a soft related target that cannot be resolved (cosmetic).
	RelatedUnresolvable = "related_unresolvable:"

	// StaleInProgress marks built-in in-progress with mtime older than 72h; never auto-reopened.
	StaleInProgress = "stale_in_progress:"

	// LastPushError marks a git-root whose last auto-commit push failed; cleared on next success.
	LastPushError = "last_push_error:"

	// EdgeVerify marks an inbound edge that may mispoint; operation-time only, not persisted.
	EdgeVerify = "edge_verify:"

	// NonAllowlist marks a path under a scope dir outside the closed snapshot allowlist.
	NonAllowlist = "non_allowlist:"

	// TagUnknown marks a lens or list --tag operand not present on any ticket in the scope.
	// Soft only: the operation still proceeds.
	TagUnknown = "tag_unknown:"

	// TagNew marks a board-new tag value on a successful write: create --tag or
	// meta add tags|tag when the value was not on any ticket in the scope before
	// the write. Soft only: the write still succeeds.
	TagNew = "tag_new:"
)

// Line prefixes msg with tok and a space, forming a stderr diagnostic agents match by prefix.
func Line(tok, msg string) string {
	return tok + " " + msg
}

// FormatTagUnknown is the fixed tag_unknown: shape for lens and list --tag.
func FormatTagUnknown(tag string) string {
	return Line(TagUnknown, fmt.Sprintf("%q is not used on any ticket in this scope", tag))
}

// FormatTagNew is the fixed tag_new: shape for a board-new tag value on write.
func FormatTagNew(tag string) string {
	return Line(TagNew, fmt.Sprintf("%q is new to this scope", tag))
}

// FormatDependsOpen is the fixed depends_open: shape for mark into a ready/active
// built-in while depends remain unmet. waitingOn must be non-empty full ids.
func FormatDependsOpen(id, newStatus string, waitingOn []string) string {
	return Line(DependsOpen, fmt.Sprintf("%s marked %s with open depends: %s",
		id, newStatus, strings.Join(waitingOn, " ")))
}

var all = []string{
	NameDrift, ConfigUnparseable, AutoCommitMismatch, UnreachableScope,
	ParseError, DuplicateID, EqualOrder, ArchiveNonTerminal, ArchiveTerminalAtRoot,
	DependsDangling, DependsUnresolvable, SchemaError, SchemaWarn,
	SyncDisabled, Uncommitted, SyncNeeded, OrderLong, StatusConflict, DependsCycle,
	DependsSelf, DependsOnCancelled, DependsOpen, RelatedUnresolvable, StaleInProgress,
	LastPushError, EdgeVerify, NonAllowlist, TagUnknown, TagNew,
}

// All returns the closed token catalogue in definition order.
func All() []string {
	out := make([]string, len(all))
	copy(out, all)
	return out
}

// HasKnownPrefix reports whether s begins with one of the closed tokens.
// Used so token lines stay plain (never coloured or labelled).
func HasKnownPrefix(s string) bool {
	for _, t := range all {
		if strings.HasPrefix(s, t) {
			return true
		}
	}
	return false
}
