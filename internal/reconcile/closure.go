package reconcile

import "github.com/p3bot/tk/internal/registry"

// ReconcileClosure refreshes ambient plus transitive depends scopes
// (single-pass aggregates). Shared by next, status, and claim-next.
func (r *Reconciler) ReconcileClosure(reg *registry.Registry, ambient, dir string, now int64) (*Result, []string, error) {
	targets := map[string]string{ambient: dir}
	done := map[string]bool{}
	merged := NewResult()
	var reconciled []string
	registered := registeredNames(reg)
	for {
		pending := map[string]string{}
		for name, d := range targets {
			if !done[name] {
				pending[name] = d
			}
		}
		if len(pending) == 0 {
			break
		}
		res, batch, err := r.ReconcileRows(pending, registered, now)
		if err != nil {
			return nil, nil, err
		}
		merged.Merge(res)
		reconciled = append(reconciled, batch...)
		for name := range pending {
			done[name] = true
		}
		toScopes, err := r.db.DependsTargetScopes(batch)
		if err != nil {
			return nil, nil, err
		}
		for _, to := range toScopes {
			if entry, ok := reg.Scopes[to]; ok && !done[to] {
				targets[to] = entry.Dir
			}
		}
	}

	if err := r.AppendAggregates(reconciled, merged); err != nil {
		return nil, nil, err
	}

	names := make([]string, 0, len(targets))
	for name := range targets {
		names = append(names, name)
	}
	return merged, names, nil
}

func registeredNames(reg *registry.Registry) map[string]bool {
	out := make(map[string]bool, len(reg.Scopes))
	for name := range reg.Scopes {
		out[name] = true
	}
	return out
}
