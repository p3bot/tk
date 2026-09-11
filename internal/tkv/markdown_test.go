package tkv

import (
	"reflect"
	"strings"
	"testing"
)

func TestConvertMarkdownOutline(t *testing.T) {
	src := []byte("# Title\n\n## Setup\n\ntext\n\n### Details\n\nmore\n\n## Finish\n")
	html, toc, err := convertMarkdown(src)
	if err != nil {
		t.Fatal(err)
	}
	got := string(html)
	if !strings.Contains(got, `<h1 id="title">Title</h1>`) {
		t.Fatalf("h1 id: %s", got)
	}
	if !strings.Contains(got, `<h2 id="setup">Setup</h2>`) || !strings.Contains(got, `<h3 id="details">Details</h3>`) || !strings.Contains(got, `<h2 id="finish">Finish</h2>`) {
		t.Fatalf("heading ids: %s", got)
	}
	want := []tocItem{
		{
			Level: 2, ID: "setup", Text: "Setup",
			Kids: []tocItem{{Level: 3, ID: "details", Text: "Details"}},
		},
		{Level: 2, ID: "finish", Text: "Finish"},
	}
	if !reflect.DeepEqual(toc, want) {
		t.Fatalf("toc = %#v, want %#v", toc, want)
	}
}

func TestConvertMarkdownH1OnlyHasNoOutline(t *testing.T) {
	html, toc, err := convertMarkdown([]byte("# Title\n\njust a paragraph\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(toc) != 0 {
		t.Fatalf("toc = %#v, want none", toc)
	}
	if !strings.Contains(string(html), `<h1 id="title">Title</h1>`) {
		t.Fatalf("h1 id: %s", html)
	}
}

func TestConvertMarkdownSkippedLevels(t *testing.T) {
	_, toc, err := convertMarkdown([]byte("# T\n\n## A\n\n#### C\n\n## B\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := []tocItem{
		{
			Level: 2, ID: "a", Text: "A",
			Kids: []tocItem{{Level: 4, ID: "c", Text: "C"}},
		},
		{Level: 2, ID: "b", Text: "B"},
	}
	if !reflect.DeepEqual(toc, want) {
		t.Fatalf("toc = %#v, want %#v", toc, want)
	}
}

func TestConvertMarkdownInlineHeading(t *testing.T) {
	html, toc, err := convertMarkdown([]byte("# Title\n\n## Use **bold** and `code` and [docs](https://example.com)\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(toc) != 1 {
		t.Fatalf("toc = %#v, want 1 entry", toc)
	}
	item := toc[0]
	if item.Text != "Use bold and code and docs" {
		t.Fatalf("text = %q", item.Text)
	}
	if item.ID == "" {
		t.Fatal("empty heading id")
	}
	got := string(html)
	if !strings.Contains(got, `<h2 id="`+item.ID+`"`) {
		t.Fatalf("html missing id %q: %s", item.ID, got)
	}
	if !strings.Contains(got, "<strong>bold</strong>") || !strings.Contains(got, "<code>code</code>") {
		t.Fatalf("inline render: %s", got)
	}
}

func TestConvertMarkdownDoesNotRenderRawHTML(t *testing.T) {
	html, _, err := convertMarkdown([]byte("# Safe\n\n<script>alert(1)</script>\n"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(html)
	if strings.Contains(got, "<script>") || strings.Contains(got, "<script ") {
		t.Fatalf("raw script leaked: %s", got)
	}
}
