package writeengine

import (
	"errors"
	"fmt"
	"strings"

	"github.com/p3bot/tk/internal/token"
)

// Sentinel claim failures. Refresh failed means no write; push failed means
// the write and self-commit already landed (Result.Path is set).
var (
	ErrRefreshFailed = errors.New("claim aborted: board refresh did not complete")
	ErrPushFailed    = errors.New("claim landed locally; push did not")
)

// UnknownStatusError is a domain error for a status not in the scope's set.
// The CLI adapter maps it to usage (exit 2).
type UnknownStatusError struct {
	Status string
	Scope  string
}

func (e *UnknownStatusError) Error() string {
	return fmt.Sprintf("%q is not a known status for scope %q", e.Status, e.Scope)
}

// UnusableError is unreachable_scope: or config_unparseable: refuse.
type UnusableError struct {
	Line string
}

func (e *UnusableError) Error() string { return e.Line }

// MidRebaseError refuses auto-commit writes on a mid-rebase git-root.
type MidRebaseError struct {
	Scope, Root, Where string
}

func (e *MidRebaseError) Error() string {
	return fmt.Sprintf("%s is mid-sync-conflict in shared repo %s — resolve %s then run tk sync",
		e.Scope, e.Root, e.Where)
}

// DuplicateError is duplicate_id: refuse (no write).
type DuplicateError struct {
	ID    string
	Paths []string
}

func (e *DuplicateError) Error() string {
	return token.Line(token.DuplicateID, fmt.Sprintf("%s is claimed by %d files: %s — resolve with tk doctor --repair",
		e.ID, len(e.Paths), strings.Join(e.Paths, ", ")))
}

// ParseQuarantineError is parse_error: refuse on a write.
type ParseQuarantineError struct {
	ID  string
	Msg string
}

func (e *ParseQuarantineError) Error() string {
	return token.Line(token.ParseError,
		fmt.Sprintf("%s: %s — cannot rewrite quarantined frontmatter", e.ID, e.Msg))
}

// UnknownTicketError is a well-formed id with no row (generic non-zero).
type UnknownTicketError struct {
	Noun string
	Arg  string
}

func (e *UnknownTicketError) Error() string {
	return fmt.Sprintf("unknown %s id %q", e.Noun, e.Arg)
}

// NoLongerTodoError is claim's re-check: the ticket is not todo under lock.
type NoLongerTodoError struct {
	ID     string
	Status string
}

func (e *NoLongerTodoError) Error() string {
	return fmt.Sprintf("%s is no longer todo (status is %s) — not claimed", e.ID, e.Status)
}

// UsageError is argv-class policy the CLI adapter maps to exit 2.
type UsageError struct {
	Msg string
}

func (e *UsageError) Error() string { return e.Msg }

// DependsSelfError is depends_self: refuse on depends add.
type DependsSelfError struct {
	ID string
}

func (e *DependsSelfError) Error() string {
	return token.Line(token.DependsSelf,
		fmt.Sprintf("%s depends on itself — remove the self-edge", e.ID))
}

// DependsDanglingError is depends_dangling: same-scope missing target.
type DependsDanglingError struct {
	ID, Target string
}

func (e *DependsDanglingError) Error() string {
	return token.Line(token.DependsDangling,
		fmt.Sprintf("%s depends on %s which has no ticket in this scope", e.ID, e.Target))
}

// DependsUnresolvableError is depends_unresolvable: cross-scope refuse.
type DependsUnresolvableError struct {
	ID, Target string
}

func (e *DependsUnresolvableError) Error() string {
	return token.Line(token.DependsUnresolvable,
		fmt.Sprintf("%s depends on %s which cannot be resolved here", e.ID, e.Target))
}

// NeighbourOrderError is a neighbour that exists but has no valid order key.
type NeighbourOrderError struct {
	Arg string
}

func (e *NeighbourOrderError) Error() string {
	return fmt.Sprintf("neighbour %q has no valid order", e.Arg)
}

// NoLegalOrderError is KeyBetween failure; equal slots need doctor re-space.
type NoLegalOrderError struct {
	ID  string
	Err error
}

func (e *NoLegalOrderError) Error() string {
	return fmt.Sprintf("no legal order between neighbours for %s (%s) — re-space with tk doctor", e.ID, e.Err)
}

func (e *NoLegalOrderError) Unwrap() error { return e.Err }
