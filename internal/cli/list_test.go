package cli

import (
	"testing"

	"github.com/p3bot/tk/internal/index"
)

func TestListTagVisible(t *testing.T) {
	ticket := func(tags ...string) *index.Ticket {
		return &index.Ticket{Tags: tags}
	}
	cases := []struct {
		name      string
		p         *index.Ticket
		tags      []string
		applyLens bool
		lens      []string
		want      bool
	}{
		{"neither filter", ticket("x"), nil, false, nil, true},
		{"untagged neither filter", ticket(), nil, false, nil, true},

		{"lens match", ticket("frontend"), nil, true, []string{"frontend"}, true},
		{"lens other tag", ticket("backend"), nil, true, []string{"frontend"}, false},
		{"lens untagged", ticket(), nil, true, []string{"frontend"}, true},
		{"lens multi any", ticket("style"), nil, true, []string{"frontend", "style"}, true},

		{"tag only match", ticket("backend"), []string{"backend"}, false, nil, true},
		{"tag only miss", ticket("frontend"), []string{"backend"}, false, nil, false},
		{"tag only untagged", ticket(), []string{"backend"}, false, nil, false},
		{"tag only multi OR", ticket("style"), []string{"backend", "style"}, false, nil, true},

		{"union lens side", ticket("frontend"), []string{"backend"}, true, []string{"frontend"}, true},
		{"union tag side", ticket("backend"), []string{"backend"}, true, []string{"frontend"}, true},
		{"union untagged via lens", ticket(), []string{"backend"}, true, []string{"frontend"}, true},
		{"union neither side", ticket("style"), []string{"backend"}, true, []string{"frontend"}, false},
		{"union multi tag OR", ticket("style"), []string{"backend", "style"}, true, []string{"frontend"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := listTagVisible(tc.p, tc.tags, tc.applyLens, tc.lens)
			if got != tc.want {
				t.Fatalf("listTagVisible = %v want %v (tags=%v applyLens=%v lens=%v ticketTags=%v)",
					got, tc.want, tc.tags, tc.applyLens, tc.lens, tc.p.Tags)
			}
		})
	}
}
