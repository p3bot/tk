package cli

import (
	"github.com/p3bot/tk/internal/id"
)

const reservedMe = id.ReservedMe

// parseIDArg: malformed → ok false (caller → exit 2); unknown well-formed is lookup's exit 1.
// The reserved token "me" is well-formed and expands in the shared resolver.
func parseIDArg(tok string) (id.Form, bool) {
	return id.ParseArg(tok)
}
