package tkv

import "encoding/binary"

// Offsets of vinfo_stat.vst_dev and vst_ino inside a vnode_fdinfo buffer.
// proc_fileinfo is 24 bytes; the inode follows the 32-bit device and the
// 16-bit mode and link count (Apple bsd/sys/proc_info.h). Both Mac
// architectures are little-endian.
const (
	darwinVnodeDevOff = 24
	darwinVnodeInoOff = 32
	darwinVnodeMin    = 40
)

// darwinVnodeMatch reports whether a PROC_PIDFDVNODEINFO buffer describes the
// file with this device and inode.
func darwinVnodeMatch(buf []byte, dev uint64, ino uint64) bool {
	if len(buf) < darwinVnodeMin {
		return false
	}
	gotDev := uint64(binary.LittleEndian.Uint32(buf[darwinVnodeDevOff:]))
	gotIno := binary.LittleEndian.Uint64(buf[darwinVnodeInoOff:])
	return gotDev == dev && gotIno == ino
}
