package index

import (
	"slices"
	"testing"
)

func TestDistinctTags(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   []*Ticket
		want []string
	}{
		{name: "empty", in: nil, want: nil},
		{name: "no tags", in: []*Ticket{{ID: "a"}}, want: nil},
		{
			name: "dedupe and sort",
			in: []*Ticket{
				{Tags: []string{"z", "a"}},
				{Tags: []string{"a", "m"}},
			},
			want: []string{"a", "m", "z"},
		},
		{
			name: "empty strings ignored",
			in:   []*Ticket{{Tags: []string{"", "x", ""}}},
			want: []string{"x"},
		},
		{
			name: "nil ticket skipped",
			in:   []*Ticket{nil, {Tags: []string{"b"}}, nil},
			want: []string{"b"},
		},
		{
			name: "archived and all statuses count",
			in: []*Ticket{
				{Status: "done", Archived: true, Tags: []string{"legacy"}},
				{Status: "backlog", Tags: []string{"plan"}},
				{Status: "todo", Tags: []string{"plan"}},
			},
			want: []string{"legacy", "plan"},
		},
		{
			name: "case sensitive",
			in:   []*Ticket{{Tags: []string{"Foo", "foo"}}},
			want: []string{"Foo", "foo"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := DistinctTags(tc.in)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("DistinctTags = %v, want %v", got, tc.want)
			}
		})
	}
}
