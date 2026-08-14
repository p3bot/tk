package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/index"
	"github.com/p3bot/tk/internal/reconcile"
	"github.com/p3bot/tk/internal/token"
)

type resolution struct {
	scope string
	rows  []*index.Ticket
	res   *reconcile.Result
}

// resolveTicket: malformed id → exit 2; unknown well-formed / no ambient → generic non-zero.
func (e *engine) resolveTicket(c *cobra.Command, idArg, scopeFlag string) (*resolution, error) {
	form, ok := parseIDArg(idArg)
	if !ok {
		return nil, usageErrorf("%q is not a valid ticket id", idArg)
	}

	scope, err := e.scopeForID(idArg, form, scopeFlag)
	if err != nil {
		return nil, err
	}
	entry, registered := e.reg.Scopes[scope]
	if !registered {
		return nil, fmt.Errorf("unknown ticket id %q: scope %q is not registered here", idArg, scope)
	}

	// Defer printing: duplicate refusal has its own line; suppress reconcile's echo for that id.
	res, err := e.reconcileResult(map[string]string{scope: entry.Dir})
	if err != nil {
		return nil, err
	}
	if res.Unreachable[scope] {
		e.printWarnings(c, res.Warnings)
		return nil, fmt.Errorf("cannot resolve %q: scope %q is not reachable", idArg, scope)
	}

	lookupArg, lookupForm, err := e.expandReservedID(scope, idArg, form)
	if err != nil {
		e.printWarnings(c, res.Warnings)
		return nil, err
	}

	var rows []*index.Ticket
	switch lookupForm {
	case idFull:
		rows, err = e.db.TicketsByID(scope, lookupArg)
	default:
		rows, err = e.db.TicketsByShortID(scope, lookupArg)
	}
	if err != nil {
		e.printWarnings(c, res.Warnings)
		return nil, err
	}
	if len(rows) == 0 {
		e.printWarnings(c, res.Warnings)
		return nil, fmt.Errorf("unknown ticket id %q", idArg)
	}

	warnings := res.Warnings
	if len(rows) > 1 {
		warnings = suppressDuplicateID(warnings, rows[0].ID)
	}
	e.printWarnings(c, warnings)
	return &resolution{scope: scope, rows: rows, res: res}, nil
}

// suppressDuplicateID avoids double-echoing when the verb refuses with its own duplicate_id line.
func suppressDuplicateID(warnings []string, id string) []string {
	prefix := token.Line(token.DuplicateID, id+" claimed by ")
	var out []string
	for _, w := range warnings {
		if strings.HasPrefix(w, prefix) {
			continue
		}
		out = append(out, w)
	}
	return out
}

func (e *engine) scopeForID(idArg string, form idForm, scopeFlag string) (string, error) {
	if form == idFull {
		return scopeOfFullID(idArg), nil
	}
	resolved, err := e.resolveAmbient(scopeFlag)
	if err != nil {
		return "", err
	}
	return resolved.Name, nil
}

// expandReservedID turns reserved "me" into the stored full id for scope.
// Unset is an unknown ticket id (generic non-zero), not usage.
func (e *engine) expandReservedID(scope, idArg string, form idForm) (string, idForm, error) {
	if form != idMe {
		return idArg, form, nil
	}
	stored := e.reg.Me[scope]
	if stored == "" {
		return "", 0, fmt.Errorf("unknown ticket id %q", reservedMe)
	}
	return stored, idFull, nil
}

func duplicateRefusal(rows []*index.Ticket) error {
	paths := make([]string, len(rows))
	for i, r := range rows {
		paths[i] = r.Path
	}
	return fmt.Errorf("%s", token.Line(token.DuplicateID,
		fmt.Sprintf("%s is claimed by %d files: %s — resolve with tk doctor --repair", rows[0].ID, len(rows), joinComma(paths))))
}

func scopeOfFullID(fullID string) string {
	for i := 0; i < len(fullID); i++ {
		if fullID[i] == '-' {
			return fullID[:i]
		}
	}
	return fullID
}

func joinComma(items []string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}
