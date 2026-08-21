//go:build darwin

package index

import (
	"strings"
	"syscall"
)

// nonLocalFSType maps macOS f_fstypename values where WAL is unsafe (name match;
// macOS reports FS by name, not magic).
var nonLocalFSType = map[string]string{
	"nfs":     "NFS",
	"smbfs":   "SMB",
	"webdav":  "WebDAV",
	"afpfs":   "AFP",
	"osxfuse": "FUSE",
	"macfuse": "FUSE",
}

// classifyNonLocal is best-effort: unstatfs-able or unrecognised type yields no refuse.
func classifyNonLocal(dir string) string {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return ""
	}
	name := int8SliceToString(st.Fstypename[:])
	if label, ok := nonLocalFSType[name]; ok {
		return nonLocalMsg(dir, label)
	}
	return ""
}

func int8SliceToString(b []int8) string {
	buf := make([]byte, 0, len(b))
	for _, c := range b {
		if c == 0 {
			break
		}
		buf = append(buf, byte(c))
	}
	return strings.ToLower(string(buf))
}
