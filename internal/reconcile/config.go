package reconcile

import (
	"cuelang.org/go/cue/cuecontext"

	"github.com/p3bot/tk/internal/scopeconfig"
)

// SchemaOrError evaluates a scope's config from its files on every call, so a
// write gate never acts on an old hold. A *ConfigError means the config is
// unusable (reads work; writes blocked). The context is new each time because a
// shared one retains memory per evaluation, which a long-lived tkv would accumulate.
func (r *Reconciler) SchemaOrError(dir string) (*scopeconfig.Schema, *scopeconfig.ConfigError) {
	schema, err := scopeconfig.Load(cuecontext.New(), dir)
	if err == nil {
		return schema, nil
	}
	if ce, ok := scopeconfig.AsConfigError(err); ok {
		return nil, ce
	}
	return nil, &scopeconfig.ConfigError{Dir: dir, Reason: err.Error()}
}

// SchemaOrNil returns a scope's schema, or nil when unusable.
func (r *Reconciler) SchemaOrNil(dir string) *scopeconfig.Schema {
	s, _ := r.SchemaOrError(dir)
	return s
}
