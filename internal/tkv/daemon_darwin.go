//go:build darwin

package tkv

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

const (
	procInfoCallPID    = 2
	procInfoCallPIDFD  = 3
	procPIDListFDs     = 1
	procPIDFDVnodeInfo = 1
	proxFDTypeVnode    = 1
	procFDInfoSize     = 8
)

// pidHoldsLock reports whether pid has lockPath open. The open file's device
// and inode are the identity, the same fact Linux reads from /proc.
func pidHoldsLock(pid int, lockPath string) (bool, error) {
	if pid <= 0 {
		return false, nil
	}
	fi, err := os.Stat(lockPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return false, fmt.Errorf("stat %s: missing system stat", lockPath)
	}
	dev := uint64(uint32(st.Dev))
	ino := st.Ino

	fds, err := procFDList(pid)
	if err != nil {
		if errors.Is(err, syscall.ESRCH) || errors.Is(err, syscall.ENOENT) {
			return false, nil
		}
		return false, err
	}
	info := make([]byte, 512)
	for off := 0; off+procFDInfoSize <= len(fds); off += procFDInfoSize {
		fd := int32(binary.LittleEndian.Uint32(fds[off:]))
		kind := binary.LittleEndian.Uint32(fds[off+4:])
		if kind != proxFDTypeVnode {
			continue
		}
		n, err := procInfo(procInfoCallPIDFD, pid, procPIDFDVnodeInfo, uint64(fd), info)
		if err != nil {
			if errors.Is(err, syscall.EBADF) || errors.Is(err, syscall.ESRCH) || errors.Is(err, syscall.ENOENT) {
				continue
			}
			return false, err
		}
		if darwinVnodeMatch(info[:n], dev, ino) {
			return true, nil
		}
	}
	return false, nil
}

// procFDList returns the process fd records. A null buffer asks the kernel
// for the byte count, which already includes spare entries.
func procFDList(pid int) ([]byte, error) {
	hint, err := procInfo(procInfoCallPID, pid, procPIDListFDs, 0, nil)
	if err != nil {
		return nil, err
	}
	if hint <= 0 {
		return nil, nil
	}
	buf := make([]byte, hint)
	n, err := procInfo(procInfoCallPID, pid, procPIDListFDs, 0, buf)
	if err != nil {
		return nil, err
	}
	if n < 0 || n > len(buf) {
		return nil, fmt.Errorf("proc_info fd list: %d bytes", n)
	}
	return buf[:n], nil
}

func procInfo(callnum, pid, flavor int, arg uint64, buf []byte) (int, error) {
	var ptr unsafe.Pointer
	if len(buf) > 0 {
		ptr = unsafe.Pointer(&buf[0])
	}
	r1, _, e := syscall.Syscall6(
		syscall.SYS_PROC_INFO,
		uintptr(callnum),
		uintptr(pid),
		uintptr(flavor),
		uintptr(arg),
		uintptr(ptr),
		uintptr(len(buf)),
	)
	if e != 0 {
		return 0, e
	}
	return int(r1), nil
}
