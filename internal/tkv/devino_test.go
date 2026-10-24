package tkv

import (
	"encoding/binary"
	"testing"
)

func TestDarwinVnodeMatch(t *testing.T) {
	buf := make([]byte, darwinVnodeMin)
	binary.LittleEndian.PutUint32(buf[darwinVnodeDevOff:], 0x11)
	binary.LittleEndian.PutUint64(buf[darwinVnodeInoOff:], 0x22)
	if !darwinVnodeMatch(buf, 0x11, 0x22) {
		t.Fatal("same device and inode did not match")
	}
	if darwinVnodeMatch(buf, 0x11, 0x23) {
		t.Fatal("different inode matched")
	}
	if darwinVnodeMatch(buf, 0x12, 0x22) {
		t.Fatal("different device matched")
	}
	if darwinVnodeMatch(buf[:darwinVnodeMin-1], 0x11, 0x22) {
		t.Fatal("short buffer matched")
	}
}
