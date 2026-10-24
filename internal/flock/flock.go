// Package flock provides exclusive, blocking POSIX advisory locks via syscall.Flock.
// Unix-only (macOS/Linux); no cross-clone or networked-filesystem coordination.
package flock

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// Lock is a held exclusive flock, released exactly once.
type Lock struct {
	f *os.File
}

// Acquire opens path (creating if needed) and takes an exclusive flock, blocking until available.
// The parent directory must already exist.
func Acquire(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("acquire lock %s: %w", path, err)
	}
	return &Lock{f: f}, nil
}

// TryAcquire is Acquire with LOCK_NB. acquired is false when another holder
// already has the lock; that is not an error. The file is created either way
// when the open succeeds, then closed again when the lock is not taken.
func TryAcquire(path string) (lock *Lock, acquired bool, err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, false, fmt.Errorf("open lock %s: %w", path, err)
	}
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("acquire lock %s: %w", path, err)
	}
	return &Lock{f: f}, true, nil
}

// WriteHead overwrites the start of the lock file while the lock is held.
// Callers use a fixed-size record so a reader never mixes two lengths.
func (l *Lock) WriteHead(b []byte) error {
	if l == nil || l.f == nil {
		return fmt.Errorf("write lock: not held")
	}
	if _, err := l.f.WriteAt(b, 0); err != nil {
		return fmt.Errorf("write lock: %w", err)
	}
	return nil
}

// Release drops the flock and closes the descriptor. Safe on a nil or already-released lock.
func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	err := l.f.Close()
	l.f = nil
	return err
}
