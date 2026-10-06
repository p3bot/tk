package bodyedit

import (
	"bytes"
	"testing"

	"github.com/p3bot/tk/internal/frontmatter"
)

func TestReplaceKeepsCRLFFence(t *testing.T) {
	raw := []byte("---\r\nid: wc-ab2c\r\nstatus: draft\r\n---\r\n# Work\nhello\n")
	_, body, ok := frontmatter.Split(raw)
	if !ok {
		t.Fatal("fence")
	}
	fence := raw[:len(raw)-len(body)]
	got := Replace(fence, "Renamed", "", "line1\r\nline2\r")
	if !bytes.HasPrefix(got, fence) {
		t.Fatalf("fence changed:\n%q\n---\n%q", fence, got)
	}
	rest := got[len(fence):]
	if bytes.Contains(rest, []byte{'\r'}) {
		t.Fatalf("body region has CR: %q", rest)
	}
	if string(rest) != "# Renamed\nline1\nline2\n" {
		t.Fatalf("body = %q", rest)
	}
}

func TestReplaceKeepsLeadingText(t *testing.T) {
	fence := []byte("---\nid: wc-ab2c\nstatus: draft\n---\n")
	got := Replace(fence, "Shape", "See the notes below.\r\n\r\n", "\nThe sockets.\n")
	rest := got[len(fence):]
	if bytes.Contains(rest, []byte{'\r'}) {
		t.Fatalf("body region has CR: %q", rest)
	}
	if string(rest) != "See the notes below.\n\n# Shape\n\nThe sockets.\n" {
		t.Fatalf("body = %q", rest)
	}
}

func TestNormalizeTitleStripsPastedH1(t *testing.T) {
	got, err := NormalizeTitle("  # Renamed title  ")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Renamed title" {
		t.Fatalf("title = %q", got)
	}
	if _, err := NormalizeTitle("two\nlines"); err == nil {
		t.Fatal("newline title accepted")
	}
}
