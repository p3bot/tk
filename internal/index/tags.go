package index

import "sort"

// TagMembership is the closed in-use tag set for a scope: every row counts
// (active and archived, all statuses). Empty strings are ignored. Shared by
// tk tags inventory and tag-existence feedback (lens, list --tag, meta add).
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
	seen := TagMembership(tickets)
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
