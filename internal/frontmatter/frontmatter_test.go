package frontmatter

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

const sampleFile = `---
id: wc-ab2c
status: in-progress
order: "a0"
depends: [wc-k3m9]
related: [wc-x4p7, api-m9k3]
tags: [network, cdp]
created: 2026-06-20T14:30:00+10:00
links: ["PR#142", issue#88]
summary: One-line what/why.
# a human comment
estimate: 3
area: frontend
---

# Network output redesign

Body text here.
`

func TestSplitVerbatimInterior(t *testing.T) {
	interior, body, present := Split([]byte(sampleFile))
	if !present {
		t.Fatal("Split present = false, want true")
	}
	wantInterior := `id: wc-ab2c
status: in-progress
order: "a0"
depends: [wc-k3m9]
related: [wc-x4p7, api-m9k3]
tags: [network, cdp]
created: 2026-06-20T14:30:00+10:00
links: ["PR#142", issue#88]
summary: One-line what/why.
# a human comment
estimate: 3
area: frontend
`
	if string(interior) != wantInterior {
		t.Fatalf("Split interior not byte-for-byte.\n got: %q\nwant: %q", interior, wantInterior)
	}
	wantBody := "\n# Network output redesign\n\nBody text here.\n"
	if string(body) != wantBody {
		t.Fatalf("Split body = %q, want %q", body, wantBody)
	}
}

func TestSplitPreservesCommentsAndConflict(t *testing.T) {
	src := "---\nid: wc-ab2c\nstatus: done\norder: \"a0\"\nstatus_conflict: [cancelled, done]\n# keep me\ncreated: 2026-01-01T00:00:00Z\n---\nbody\n"
	interior, _, present := Split([]byte(src))
	if !present {
		t.Fatal("present = false")
	}
	for _, want := range []string{"# keep me", "status_conflict: [cancelled, done]"} {
		if !bytes.Contains(interior, []byte(want)) {
			t.Errorf("interior missing verbatim %q", want)
		}
	}
}

func TestSplitNoFence(t *testing.T) {
	data := []byte("# Just a body\n\nNo frontmatter.\n")
	interior, body, present := Split(data)
	if present {
		t.Fatal("present = true, want false for a file with no fence")
	}
	if interior != nil {
		t.Fatalf("interior = %q, want nil", interior)
	}
	if !bytes.Equal(body, data) {
		t.Fatalf("body = %q, want the whole input", body)
	}
}

func TestSplitNoClosingFence(t *testing.T) {
	data := []byte("---\nid: wc-ab2c\nno closing fence here\n")
	_, body, present := Split(data)
	if present {
		t.Fatal("present = true, want false when the closing fence is missing")
	}
	if !bytes.Equal(body, data) {
		t.Fatalf("body = %q, want the whole input", body)
	}
}

func TestSplitFenceMustBeExact(t *testing.T) {
	// "--- " is a thematic break, not a fence — whole file is body.
	data := []byte("--- \nid: wc-ab2c\n---\nbody\n")
	interior, body, present := Split(data)
	if present {
		t.Fatal("present = true, want false for a fence line with trailing whitespace")
	}
	if interior != nil {
		t.Fatalf("interior = %q, want nil", interior)
	}
	if !bytes.Equal(body, data) {
		t.Fatalf("body = %q, want the whole input", body)
	}

	crlf := []byte("---\r\nid: wc-ab2c\r\n---\r\nbody\r\n")
	if _, _, ok := Split(crlf); !ok {
		t.Fatal("Split(CRLF fences) present = false, want true")
	}
}

func TestParseBuiltinsAndCustoms(t *testing.T) {
	interior, _, _ := Split([]byte(sampleFile))
	m, err := Parse(interior)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.ID != "wc-ab2c" || m.Status != "in-progress" || m.Order != "a0" {
		t.Errorf("scalars wrong: id=%q status=%q order=%q", m.ID, m.Status, m.Order)
	}
	if m.Created != "2026-06-20T14:30:00+10:00" {
		t.Errorf("created = %q", m.Created)
	}
	if m.Summary != "One-line what/why." {
		t.Errorf("summary = %q", m.Summary)
	}
	if !reflect.DeepEqual(m.Depends, []string{"wc-k3m9"}) {
		t.Errorf("depends = %#v", m.Depends)
	}
	if !reflect.DeepEqual(m.Related, []string{"wc-x4p7", "api-m9k3"}) {
		t.Errorf("related = %#v", m.Related)
	}
	if !reflect.DeepEqual(m.Tags, []string{"network", "cdp"}) {
		t.Errorf("tags = %#v", m.Tags)
	}
	if !reflect.DeepEqual(m.Links, []string{"PR#142", "issue#88"}) {
		t.Errorf("links = %#v", m.Links)
	}
	if len(m.Custom) != 2 || m.Custom[0].Key != "estimate" || m.Custom[1].Key != "area" {
		t.Fatalf("custom = %#v, want estimate then area", m.Custom)
	}
}

func TestParseStatusConflictTolerated(t *testing.T) {
	interior := []byte("id: wc-ab2c\nstatus: done\norder: \"a0\"\nstatus_conflict: [cancelled, done]\ncreated: 2026-01-01T00:00:00Z\n")
	m, err := Parse(interior)
	if err != nil {
		t.Fatalf("Parse should tolerate status_conflict: %v", err)
	}
	if !reflect.DeepEqual(m.StatusConflict, []string{"cancelled", "done"}) {
		t.Fatalf("StatusConflict = %#v", m.StatusConflict)
	}
	for _, f := range m.Custom {
		if f.Key == KeyStatusConflict {
			t.Fatal("status_conflict leaked into Custom")
		}
	}
}

func TestParseRejectsMistypedList(t *testing.T) {
	if _, err := Parse([]byte("id: wc-ab2c\ndepends: not-a-list\n")); err == nil {
		t.Fatal("Parse should reject a scalar where a list is required")
	}
}

func TestSerializeQuotesOrder(t *testing.T) {
	m := &Model{ID: "wc-ab2c", Status: "draft", Order: "a0", Created: "2026-01-01T00:00:00Z"}
	out, err := Serialize(m)
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	if !bytes.Contains(out, []byte(`order: "a0"`)) {
		t.Fatalf("Serialize did not quote order:\n%s", out)
	}
	back, err := Parse(out)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if back.Order != "a0" {
		t.Fatalf("round-tripped order = %q, want a0", back.Order)
	}
}

func TestSerializeOmitsEmptyOptionalKeys(t *testing.T) {
	m := &Model{ID: "wc-ab2c", Status: "draft", Order: "a0", Created: "2026-01-01T00:00:00Z"}
	out, _ := Serialize(m)
	for _, absent := range []string{"depends", "related", "tags", "links", "summary", "status_conflict"} {
		if bytes.Contains(out, []byte(absent+":")) {
			t.Errorf("Serialize emitted empty optional key %q:\n%s", absent, out)
		}
	}
}

func TestRoundTripModel(t *testing.T) {
	interior, _, _ := Split([]byte(sampleFile))
	first, err := Parse(interior)
	if err != nil {
		t.Fatalf("first parse: %v", err)
	}
	out, err := Serialize(first)
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	second, err := Parse(out)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("model changed across round-trip:\n first: %#v\nsecond: %#v", first, second)
	}
}

func TestComposeReconstructsFile(t *testing.T) {
	interior, body, _ := Split([]byte(sampleFile))
	got := Compose(interior, body)
	if !bytes.Equal(got, []byte(sampleFile)) {
		t.Fatalf("Compose did not reconstruct the file:\n%s", got)
	}
}

func TestSerializeCustomTypesRoundTrip(t *testing.T) {
	interior := []byte(strings.Join([]string{
		"id: wc-ab2c",
		"status: draft",
		`order: "a0"`,
		"created: 2026-01-01T00:00:00Z",
		"estimate: 5",
		"blocking: true",
		"stakeholders: [platform, design]",
	}, "\n") + "\n")
	first, err := Parse(interior)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	out, err := Serialize(first)
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	second, err := Parse(out)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if !reflect.DeepEqual(first.Custom, second.Custom) {
		t.Fatalf("custom fields changed across round-trip:\n first: %#v\nsecond: %#v", first.Custom, second.Custom)
	}
}

func TestRemoveCustom(t *testing.T) {
	m := &Model{Custom: []Field{
		{Key: "jira", Value: "ABC-1"},
		{Key: "area", Value: "frontend"},
	}}
	if !m.RemoveCustom("jira") {
		t.Fatal("RemoveCustom jira should report present")
	}
	if len(m.Custom) != 1 || m.Custom[0].Key != "area" {
		t.Fatalf("after remove jira: %#v", m.Custom)
	}
	if m.RemoveCustom("ghost") {
		t.Fatal("RemoveCustom ghost should report absent")
	}
	if !m.RemoveCustom("area") {
		t.Fatal("RemoveCustom last key should report present")
	}
	if m.Custom != nil {
		t.Fatalf("empty Custom should be nil, got %#v", m.Custom)
	}
}
