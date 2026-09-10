package title

import "testing"

func TestExtract(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "simple h1", body: "# Network output redesign\n\nBody.", want: "Network output redesign"},
		{name: "first h1 wins", body: "# First\n\n# Second\n", want: "First"},
		{name: "strips surrounding whitespace", body: "#    Padded title   \n", want: "Padded title"},
		{name: "tab after hash", body: "#\tTabbed\n", want: "Tabbed"},
		{name: "h1 after prose", body: "Intro line\n\n# Real title\n", want: "Real title"},
		{name: "ignores h2", body: "## Not h1\n# Yes h1\n", want: "Yes h1"},
		{name: "ignores setext", body: "Setext title\n===========\n", want: ""},
		{name: "hash without space is not h1", body: "#nospace\n", want: ""},
		{name: "hash with no text is not h1", body: "#   \n", want: ""},
		{name: "empty heading skipped for later real h1", body: "#   \n# Real title\n", want: "Real title"},
		{name: "tab-only heading skipped for later real h1", body: "#\t\n# Real title\n", want: "Real title"},
		{name: "no heading", body: "just prose\nmore prose\n", want: ""},
		{name: "empty body", body: "", want: ""},
		{name: "crlf line endings", body: "# Windows title\r\nbody\r\n", want: "Windows title"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Extract([]byte(tt.body)); got != tt.want {
				t.Fatalf("Extract(%q) = %q, want %q", tt.body, got, tt.want)
			}
		})
	}
}

func TestSplitH1(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		heading string
		rest    string
	}{
		{name: "simple", body: "# Network output redesign\n\nBody.", heading: "Network output redesign", rest: "\nBody."},
		{name: "first wins", body: "# First\n\n# Second\n", heading: "First", rest: "\n# Second\n"},
		{name: "h1 only", body: "# Work\n", heading: "Work", rest: ""},
		{name: "no trailing newline", body: "# Work", heading: "Work", rest: ""},
		{name: "after prose", body: "Intro line\n\n# Real title\nrest\n", heading: "Real title", rest: "rest\n"},
		{name: "empty heading skipped", body: "#   \n# Real title\n", heading: "Real title", rest: ""},
		{name: "no heading", body: "just prose\n", heading: "", rest: "just prose\n"},
		{name: "empty", body: "", heading: "", rest: ""},
		{name: "crlf", body: "# Windows title\r\nbody\r\n", heading: "Windows title", rest: "body\r\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			heading, rest := SplitH1([]byte(tt.body))
			if heading != tt.heading {
				t.Fatalf("heading = %q, want %q", heading, tt.heading)
			}
			if string(rest) != tt.rest {
				t.Fatalf("rest = %q, want %q", rest, tt.rest)
			}
		})
	}
}
