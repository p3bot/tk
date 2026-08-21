package index

import "fmt"

func nonLocalMsg(dir, label string) string {
	return fmt.Sprintf("index directory %s looks like a %s (non-local) filesystem; WAL is unsafe there — set XDG_STATE_HOME to a local disk", dir, label)
}
