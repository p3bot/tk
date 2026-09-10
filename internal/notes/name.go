package notes

import (
	"fmt"
	"path/filepath"

	"github.com/p3bot/tk/internal/registry"
	"github.com/p3bot/tk/internal/scopefile"
	"github.com/p3bot/tk/internal/slug"
)

// EffectiveSlug is the stored machine-local default, or the built-in `default`.
// A stored value that is not an addressable slug is a hard error naming note.cue.
func EffectiveSlug(reg *registry.Registry, configDir, scope string) (string, error) {
	var stored string
	var ok bool
	if reg != nil {
		stored, ok = reg.Note[scope]
	}
	if !ok || stored == scopefile.NoteDefaultSlug {
		return scopefile.NoteDefaultSlug, nil
	}
	if !scopefile.IsAddressableNoteSlug(stored) {
		return "", fmt.Errorf("%s stores %q for scope %q — not an addressable note slug",
			filepath.Join(configDir, "note.cue"), stored, scope)
	}
	return stored, nil
}

// ResolveName applies a one-shot selector, or the stored default when neither
// positional nor --name is set.
func ResolveName(deps Deps, scope string, sel Selector) (string, error) {
	fallback := scopefile.NoteDefaultSlug
	if sel.Positional == "" && !sel.NameSet {
		var err error
		fallback, err = EffectiveSlug(deps.Reg, deps.ConfigDir, scope)
		if err != nil {
			return "", err
		}
	}
	return SelectName(sel.Positional, sel.Name, sel.NameSet, fallback)
}

// SelectName is positional vs --name vs fallback. Both positional and --name is usage.
func SelectName(positional, nameFlag string, nameSet bool, fallback string) (string, error) {
	if positional != "" && nameSet {
		return "", &UsageError{Msg: "use a positional slug or --name, not both"}
	}
	name := fallback
	switch {
	case positional != "":
		name = positional
	case nameSet:
		name = nameFlag
	}
	if scopefile.IsReservedNoteName(name) {
		return "", &UsageError{Msg: fmt.Sprintf("%q is a reserved note name", name)}
	}
	if !slug.Valid(name) {
		return "", &UsageError{Msg: fmt.Sprintf("%q is not a valid note slug", name)}
	}
	return name, nil
}
