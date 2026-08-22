// Package status defines built-in ticket statuses, the closed Category set, and
// terminal/list/next predicates. Custom statuses come from the caller's
// name→category map; this package does no I/O or CUE.
package status

import "sort"

// Category is the closed set of behaviours a status can have.
type Category string

const (
	// CategoryActive statuses appear in the default list; neither next-eligible nor terminal.
	CategoryActive Category = "active"
	// CategoryBacklog statuses are hidden from the default list and not terminal.
	CategoryBacklog Category = "backlog"
	// CategoryDone statuses are hidden from the default list and terminal (satisfy depends).
	CategoryDone Category = "done"
)

// Built-in status names (immutable; may not be redeclared as custom statuses).
const (
	Draft      = "draft"
	Backlog    = "backlog"
	Todo       = "todo"
	InProgress = "in-progress"
	Review     = "review"
	Blocked    = "blocked"
	Done       = "done"
	Cancelled  = "cancelled"
)

// Only Todo is ever next-eligible among built-ins.
type builtin struct {
	category      Category
	inDefaultList bool
	nextEligible  bool
}

var builtins = map[string]builtin{
	Draft:      {category: CategoryActive, inDefaultList: true, nextEligible: false},
	Backlog:    {category: CategoryBacklog, inDefaultList: false, nextEligible: false},
	Todo:       {category: CategoryActive, inDefaultList: true, nextEligible: true},
	InProgress: {category: CategoryActive, inDefaultList: true, nextEligible: false},
	Review:     {category: CategoryActive, inDefaultList: true, nextEligible: false},
	Blocked:    {category: CategoryActive, inDefaultList: true, nextEligible: false},
	Done:       {category: CategoryDone, inDefaultList: false, nextEligible: false},
	Cancelled:  {category: CategoryDone, inDefaultList: false, nextEligible: false},
}

var builtinOrder = []string{Draft, Backlog, Todo, InProgress, Review, Blocked, Done, Cancelled}

// Builtins returns the eight built-in status names in canonical order.
func Builtins() []string {
	out := make([]string, len(builtinOrder))
	copy(out, builtinOrder)
	return out
}

// IsBuiltin reports whether name is one of the eight built-in statuses.
func IsBuiltin(name string) bool {
	_, ok := builtins[name]
	return ok
}

// ValidCategory reports whether c is one of the three closed categories.
func ValidCategory(c Category) bool {
	return c == CategoryActive || c == CategoryBacklog || c == CategoryDone
}

// CategoryOf returns the category of a built-in or custom status. ok is false for unknown.
func CategoryOf(name string, custom map[string]Category) (Category, bool) {
	if b, ok := builtins[name]; ok {
		return b.category, true
	}
	if c, ok := custom[name]; ok {
		return c, true
	}
	return "", false
}

// IsKnown reports whether name is a built-in or a declared custom status.
func IsKnown(name string, custom map[string]Category) bool {
	_, ok := CategoryOf(name, custom)
	return ok
}

// IsTerminal reports whether a status satisfies a depends gate (category done). Unknown is not terminal.
func IsTerminal(name string, custom map[string]Category) bool {
	c, ok := CategoryOf(name, custom)
	return ok && c == CategoryDone
}

// IsNextEligible reports whether a status can appear in tk next. Only built-in todo qualifies.
func IsNextEligible(name string) bool {
	b, ok := builtins[name]
	return ok && b.nextEligible
}

// InDefaultList reports whether a status appears in the default list (no --all).
// Customs: active shown, backlog and done hidden. Unknown is not shown.
func InDefaultList(name string, custom map[string]Category) bool {
	if b, ok := builtins[name]; ok {
		return b.inDefaultList
	}
	if c, ok := custom[name]; ok {
		return c == CategoryActive
	}
	return false
}

// DefaultListNames returns built-in then custom status names that appear in the
// default list. Pass the set into SQL IN filters rather than denormalising category.
func DefaultListNames(custom map[string]Category) []string {
	out := make([]string, 0, 8+len(custom))
	for _, name := range builtinOrder {
		if builtins[name].inDefaultList {
			out = append(out, name)
		}
	}
	var extra []string
	for name, cat := range custom {
		if cat == CategoryActive {
			extra = append(extra, name)
		}
	}
	sort.Strings(extra)
	return append(out, extra...)
}

// TerminalNames returns built-in then custom status names in category done.
func TerminalNames(custom map[string]Category) []string {
	out := make([]string, 0, 2+len(custom))
	for _, name := range builtinOrder {
		if builtins[name].category == CategoryDone {
			out = append(out, name)
		}
	}
	var extra []string
	for name, cat := range custom {
		if cat == CategoryDone {
			extra = append(extra, name)
		}
	}
	sort.Strings(extra)
	return append(out, extra...)
}
