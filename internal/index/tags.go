package index

import "sort"

// DistinctTags returns the sorted unique non-empty tags across tickets.
// Every row counts (active and archived, all statuses). Empty strings are
// ignored. Order is case-sensitive Go string sort. This is the closed in-use
// set shared by tk tags and later tag-existence feedback.
func DistinctTags(tickets []*Ticket) []string {
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
