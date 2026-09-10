// Package gitstate manages per-git-root operational state in machine-local XDG
// state — never under <git-root>/.git/. Each auto-commit git-root has a directory
// keyed by SHA-256 of the cleaned, symlink-resolved absolute path, holding
// sync.lock and last-push-error. Mid-rebase write refuse and the sync_needed:
// reason catalogue live here so ticket writes and notes share one definition.
package gitstate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/p3bot/tk/internal/flock"
	"github.com/p3bot/tk/internal/pathutil"
)

const lastPushErrorFile = "last-push-error"

// Key is the lowercase hex SHA-256 of the canonical (cleaned, symlink-resolved)
// absolute git-root path. Symlink spellings of the same directory share one key.
// There is no dual-hash fallback: pre-canonical Clean-only keys were never a
// released on-disk contract for auto-commit state.
func Key(gitRoot string) string {
	sum := sha256.Sum256([]byte(pathutil.Canonical(gitRoot)))
	return hex.EncodeToString(sum[:])
}

// Dir is the operational-state directory for gitRoot under stateDir (not created here).
func Dir(stateDir, gitRoot string) string {
	return filepath.Join(stateDir, "git-roots", Key(gitRoot))
}

// AcquireCommitLock takes the exclusive flock at git-roots/<key>/sync.lock,
// creating the directory if needed. Serialises self-commits (and rebase/push)
// on a shared git index across scopes in one repo.
func AcquireCommitLock(stateDir, gitRoot string) (*flock.Lock, error) {
	dir := Dir(stateDir, gitRoot)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create git-root state dir %s: %w", dir, err)
	}
	return flock.Acquire(filepath.Join(dir, "sync.lock"))
}

// ReadLastPushError returns the last-push-error marker detail when a non-empty marker is present.
func ReadLastPushError(stateDir, gitRoot string) (detail string, ok bool) {
	data, err := os.ReadFile(filepath.Join(Dir(stateDir, gitRoot), lastPushErrorFile))
	if err != nil {
		return "", false
	}
	s := strings.TrimSpace(string(data))
	if s == "" {
		return "", false
	}
	return s, true
}

// WriteLastPushError records a failed-push detail under gitRoot's state dir.
func WriteLastPushError(stateDir, gitRoot, detail string) error {
	dir := Dir(stateDir, gitRoot)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create git-root state dir %s: %w", dir, err)
	}
	p := filepath.Join(dir, lastPushErrorFile)
	if err := os.WriteFile(p, []byte(strings.TrimSpace(detail)+"\n"), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", p, err)
	}
	return nil
}

// ClearLastPushError removes the last-push-error marker. Absent is not an error.
func ClearLastPushError(stateDir, gitRoot string) error {
	p := filepath.Join(Dir(stateDir, gitRoot), lastPushErrorFile)
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", p, err)
	}
	return nil
}
