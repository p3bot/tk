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
			mem := TagMembership(tc.in)
			if len(mem) != len(tc.want) {
				t.Fatalf("TagMembership len = %d, want %d", len(mem), len(tc.want))
			}
			for _, tag := range tc.want {
				if _, ok := mem[tag]; !ok {
					t.Fatalf("TagMembership missing %q", tag)
				}
			}
		})
	}
}

func TestAbsentTags(t *testing.T) {
	t.Parallel()
	inUse := map[string]struct{}{"frontend": {}, "backend": {}}
	got := AbsentTags([]string{"orphan", "frontend", "orphan", "", "api"}, inUse)
	want := []string{"orphan", "api"}
	if !slices.Equal(got, want) {
		t.Fatalf("AbsentTags = %v, want %v", got, want)
	}
	if AbsentTags(nil, inUse) != nil {
		t.Fatalf("empty requested must return nil")
	}
	if AbsentTags([]string{"frontend"}, inUse) != nil {
		t.Fatalf("all present must return nil")
	}
}
