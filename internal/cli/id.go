package cli

import (
	"strings"

	"github.com/p3bot/tk/internal/id"
)

type idForm int

const reservedMe = "me"

const (
	idFull idForm = iota
	idShort
	idMe
)

// parseIDArg: malformed → ok false (caller → exit 2); unknown well-formed is lookup's exit 1.
// The reserved token "me" is well-formed and expands in the shared resolver.
func parseIDArg(tok string) (idForm, bool) {
	if tok == reservedMe {
		return idMe, true
	}
	if strings.ContainsRune(tok, '-') {
		return idFull, id.IsFullTicketID(tok)
	}
	return idShort, id.IsShortID(tok)
}
