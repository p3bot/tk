package syncengine

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/p3bot/tk/internal/git"
	"github.com/p3bot/tk/internal/scopefile"
	"github.com/p3bot/tk/internal/selfcommit"
	"github.com/p3bot/tk/internal/token"
)

type dirtyPath struct {
	path  string
	code  string
	scope string
	dir   string
}

// snapshot commits allowlisted dirty paths under SnapshotScope only.
// An empty SnapshotScope commits nothing and reports no residue.
// Non-allowlist paths under that scope are warned, not committed.
func snapshot(deps Deps, r Reporter, t Target, rep *syncReport) error {
	parts, err := snapshotParts(t)
	if err != nil || len(parts) == 0 {
		return err
	}
	ctx := deps.Ctx
	var staged []dirtyPath
	var allowlisted []string
	for _, p := range parts {
		entries, err := git.DirtyEntries(ctx, t.Root, p.Dir)
		if err != nil {
			return err
		}
		var residue []string
		for _, ent := range entries {
			if skipSnapshotPath(ent.Path) {
				continue // .tk.lock: gitignored, skipped defensively regardless
			}
			if scopefile.IsAllowlisted(ent.Path, p.Dir) {
				allowlisted = append(allowlisted, ent.Path)
				staged = append(staged, dirtyPath{path: ent.Path, code: ent.Code, scope: p.Name, dir: p.Dir})
			} else {
				residue = append(residue, ent.Path)
			}
		}
		if len(residue) > 0 {
			rep.residueN += len(residue)
			r.Err(token.Line(token.NonAllowlist, fmt.Sprintf(
				"%d path(s) under %s not committed — move or remove; see tk doctor", len(residue), p.Dir)))
			for _, path := range residue {
				r.Err("  " + path)
			}
		}
	}

	if len(allowlisted) == 0 {
		return nil // an empty allowlist is success, not a missed commit
	}
	rep.snapshotN = len(allowlisted)
	return selfcommit.CommitPathsCore(ctx, selfcommit.BatchRequest{
		StateDir: deps.StateDir,
		GitRoot:  t.Root,
		Message:  snapshotMessage(staged),
		Paths:    allowlisted,
	})
}

func snapshotParts(t Target) ([]Participant, error) {
	if t.SnapshotScope == "" {
		return nil, nil
	}
	for _, p := range t.Participants {
		if p.Name == t.SnapshotScope {
			return []Participant{p}, nil
		}
	}
	return nil, fmt.Errorf("snapshot scope %s is not an auto-commit participant", t.SnapshotScope)
}

func skipSnapshotPath(path string) bool {
	return filepath.Base(path) == scopefile.LockName
}

// snapshotMessage: one commit for the whole snapshot (avoids tiny-commit replay piles).
func snapshotMessage(staged []dirtyPath) string {
	if len(staged) != 1 {
		return fmt.Sprintf("tk: sync %d path(s)", len(staged))
	}
	d := staged[0]
	base := filepath.Base(d.path)
	switch base {
	case "tk.cue":
		return "tk: config " + d.scope
	case ".gitignore":
		return "tk: gitignore " + d.scope
	}
	if slug, ok := scopefile.NoteSlug(d.path, d.dir); ok {
		return "tk: note " + d.scope + " " + slug
	}
	if fullID, ok := scopefile.DesignFullID(d.path, d.dir); ok {
		return "tk: design " + d.scope + " " + fullID
	}
	fullID, slug := parseTicketBasename(base)
	switch {
	case strings.ContainsRune(d.code, 'D'):
		return "tk: remove " + fullID
	case d.code == "??":
		if slug != "" {
			return "tk: add " + fullID + " " + slug
		}
		return "tk: add " + fullID
	default:
		return "tk: edit " + fullID
	}
}

func parseTicketBasename(base string) (fullID, slug string) {
	stem := strings.TrimSuffix(base, ".md")
	parts := strings.SplitN(stem, "-", 3)
	if len(parts) < 2 {
		return stem, ""
	}
	fullID = parts[0] + "-" + parts[1]
	if len(parts) == 3 {
		slug = parts[2]
	}
	return fullID, slug
}
