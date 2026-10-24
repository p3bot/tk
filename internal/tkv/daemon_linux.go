//go:build linux

package tkv

import (
	"fmt"
	"os"
	"path/filepath"
)

// pidHoldsLock reports whether pid has lockPath open. The lock fd is the
// identity of the server: a recycled pid that does not have the file open
// must not be signalled.
func pidHoldsLock(pid int, lockPath string) (bool, error) {
	if pid <= 0 {
		return false, nil
	}
	lockInfo, err := os.Stat(lockPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	fdDir := fmt.Sprintf("/proc/%d/fd", pid)
	entries, err := os.ReadDir(fdDir)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	for _, e := range entries {
		target, err := os.Readlink(filepath.Join(fdDir, e.Name()))
		if err != nil {
			continue
		}
		st, err := os.Stat(target)
		if err != nil {
			continue
		}
		if os.SameFile(lockInfo, st) {
			return true, nil
		}
	}
	return false, nil
}
