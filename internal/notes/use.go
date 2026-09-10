package notes

import (
	"github.com/p3bot/tk/internal/registry"
	"github.com/p3bot/tk/internal/scopefile"
	"github.com/p3bot/tk/internal/xdg"
)

// UseInput is one machine-local default-slug operation. Clear or the built-in
// slug `default` unsets the pointer. Empty Slug without Clear is show.
type UseInput struct {
	Scope string
	Slug  string
	Clear bool
}

// Use sets, shows, or clears this machine's default note slug. The pointer is
// XDG only: it never writes a note file and never emits SyncNeeded.
func Use(deps Deps, in UseInput) (Result, error) {
	if in.Clear {
		if err := writePointer(deps, in.Scope, ""); err != nil {
			return Result{}, err
		}
		return Result{}, nil
	}
	if in.Slug == "" {
		name, err := EffectiveSlug(deps.Reg, deps.ConfigDir, in.Scope)
		if err != nil {
			return Result{}, err
		}
		return Result{Slug: name}, nil
	}
	name, err := SelectName(in.Slug, "", false, scopefile.NoteDefaultSlug)
	if err != nil {
		return Result{}, err
	}
	if err := writePointer(deps, in.Scope, name); err != nil {
		return Result{}, err
	}
	return Result{Slug: name}, nil
}

// writePointer: machine-global flock spans load-modify-write. Built-in default deletes the key.
func writePointer(deps Deps, scope, name string) error {
	lock, err := xdg.AcquireConfigLock(deps.ConfigDir)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()

	store := registry.NewStore(deps.Cue, deps.ConfigDir)
	reg, err := store.Load()
	if err != nil {
		return err
	}
	if reg.Note == nil {
		reg.Note = map[string]string{}
	}
	if name == "" || name == scopefile.NoteDefaultSlug {
		delete(reg.Note, scope)
	} else {
		reg.Note[scope] = name
	}
	return store.WriteNote(reg.Note)
}
