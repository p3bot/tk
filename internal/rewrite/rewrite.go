// Package rewrite is the shared multi-file in-scope rewrite durability engine.
// Best-effort under a caller-held scope flock: each file is written to its new
// path atomically before the old is removed; re-entry after a crash is idempotent.
// File I/O only — caller owns flock, plan ordering, and commit.
package rewrite

import (
	"bytes"
	"fmt"
	"os"

	"github.com/p3bot/tk/internal/atomicfile"
)

const fileMode = 0o644

// Op is one file's rewrite: write Content at NewPath, then remove OldPath when it differs.
type Op struct {
	OldPath string
	NewPath string
	Content []byte
}

// Apply executes ops in order and returns every touched path. A move already
// completed (old gone, new present) is skipped. A destination that holds different
// content is a hard error so two tickets computing the same basename cannot erase
// each other — including create-only writes (empty OldPath). In-place rewrites
// (OldPath == NewPath) replace the file they name.
func Apply(ops []Op) ([]string, error) {
	var touched []string
	for _, op := range ops {
		moved := op.OldPath != "" && op.OldPath != op.NewPath
		if moved && alreadyDone(op) {
			touched = append(touched, op.NewPath, op.OldPath)
			continue
		}
		if op.OldPath != op.NewPath {
			if err := checkFreeDestination(op); err != nil {
				return touched, err
			}
		}
		if err := atomicfile.Write(op.NewPath, op.Content, fileMode); err != nil {
			return touched, err
		}
		touched = append(touched, op.NewPath)
		if moved {
			if err := os.Remove(op.OldPath); err != nil && !os.IsNotExist(err) {
				return touched, fmt.Errorf("remove old path %s: %w", op.OldPath, err)
			}
			touched = append(touched, op.OldPath)
		}
	}
	return touched, nil
}

// checkFreeDestination errors when NewPath holds content other than what the op writes.
// Same bytes is the interrupted-write window: write is a no-op, and a move can still
// remove OldPath. In-place rewrites never call this.
func checkFreeDestination(op Op) error {
	existing, err := os.ReadFile(op.NewPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read destination %s: %w", op.NewPath, err)
	}
	if bytes.Equal(existing, op.Content) {
		return nil
	}
	if op.OldPath == "" {
		return fmt.Errorf("refusing to write %s: the destination already holds a different file — resolve the two by hand", op.NewPath)
	}
	return fmt.Errorf("refusing to move %s onto %s: the destination already holds a different file — resolve the two by hand", op.OldPath, op.NewPath)
}

// alreadyDone reports whether a move completed on a prior run (old gone, new present).
func alreadyDone(op Op) bool {
	if _, err := os.Stat(op.OldPath); !os.IsNotExist(err) {
		return false
	}
	_, err := os.Stat(op.NewPath)
	return err == nil
}
