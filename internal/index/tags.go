package index

import (
	"fmt"
	"sort"
)

// ScopeTagMembership is the closed in-use tag set for a scope from ticket_tags:
// every row counts (active and archived, all statuses). Empty strings are not stored.
func (d *DB) ScopeTagMembership(scope string) (map[string]struct{}, error) {
	rows, err := d.sql.Query(`
SELECT DISTINCT tt.tag FROM ticket_tags tt
JOIN tickets t ON t.path = tt.path
WHERE t.scope = ?`, scope)
	if err != nil {
		return nil, fmt.Errorf("tag membership for %q: %w", scope, err)
	}
	defer func() { _ = rows.Close() }()
	seen := map[string]struct{}{}
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return nil, err
		}
		seen[tag] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return seen, nil
}

// ScopeDistinctTags returns the sorted unique tags in a scope (case-sensitive Go string sort).
func (d *DB) ScopeDistinctTags(scope string) ([]string, error) {
	seen, err := d.ScopeTagMembership(scope)
	if err != nil {
		return nil, err
	}
	return sortedTags(seen), nil
}

func sortedTags(seen map[string]struct{}) []string {
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for t := range seen {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// TagMembership is the closed in-use tag set for a slice of tickets already
// loaded from the index (Tags populated from ticket_tags). Empty strings ignored.
func TagMembership(tickets []*Ticket) map[string]struct{} {
	seen := map[string]struct{}{}
	for _, p := range tickets {
		if p == nil {
			continue
		}
		for _, t := range p.Tags {
			if t == "" {
				continue
			}
			seen[t] = struct{}{}
		}
	}
	return seen
}

// DistinctTags returns the sorted unique non-empty tags across tickets.
// Order is case-sensitive Go string sort. Derived from TagMembership.
func DistinctTags(tickets []*Ticket) []string {
	return sortedTags(TagMembership(tickets))
}

// AbsentTags returns the distinct non-empty tags from requested that are not
// in inUse, in first-seen order. Empty strings and duplicates are skipped.
func AbsentTags(requested []string, inUse map[string]struct{}) []string {
	if len(requested) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	var out []string
	for _, tag := range requested {
		if tag == "" {
			continue
		}
		if _, dup := seen[tag]; dup {
			continue
		}
		seen[tag] = struct{}{}
		if _, ok := inUse[tag]; ok {
			continue
		}
		out = append(out, tag)
	}
	return out
}
