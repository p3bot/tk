package cli

import (
	"github.com/p3bot/tk/internal/id"
)

// parseIDArg: malformed → ok false (caller → exit 2); unknown well-formed is lookup's exit 1.
func parseIDArg(tok string) (id.Form, bool) {
	return id.ParseArg(tok)
}
