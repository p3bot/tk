//go:build linux

package index

import (
	"syscall"
)

// nonLocalMagic maps statfs f_type magics where WAL is unsafe (network/FUSE can
// separate the DB from -wal/-shm or lack a real fsync).
var nonLocalMagic = map[int64]string{
	0x6969:     "NFS",
	0xFF534D42: "CIFS/SMB",
	0x517B:     "SMB",
	0xFE534D42: "SMB2",
	0x65735546: "FUSE",
}

// classifyNonLocal is best-effort: unstatfs-able or unrecognised type yields no refuse.
func classifyNonLocal(dir string) string {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return ""
	}
	if label, ok := nonLocalMagic[st.Type]; ok {
		return nonLocalMsg(dir, label)
	}
	return ""
}
