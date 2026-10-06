// Package bodyedit replaces the H1 and body after a frontmatter fence and
// names a file revision so a stale form does not save over a newer file.
package bodyedit

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/p3bot/tk/internal/title"
)

// ClobberKey is mtime nanoseconds and a SHA-256 of the file bytes.
func ClobberKey(mtimeNS int64, data []byte) string {
	sum := sha256.Sum256(data)
	return strconv.FormatInt(mtimeNS, 10) + ":" + hex.EncodeToString(sum[:])
}

// Snapshot reads path and returns its bytes plus ClobberKey.
func Snapshot(path string) (data []byte, key string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, "", fmt.Errorf("read %s: %w", path, err)
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, "", fmt.Errorf("stat %s: %w", path, err)
	}
	data, err = io.ReadAll(f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, "", fmt.Errorf("read %s: %w", path, err)
	}
	return data, ClobberKey(fi.ModTime().UnixNano(), data), nil
}

// NormalizeTitle returns a single-line title. A pasted ATX H1 is the heading text.
func NormalizeTitle(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", errors.New("splice needs a non-empty title")
	}
	if strings.ContainsAny(s, "\r\n") {
		return "", errors.New("splice title must be a single line")
	}
	if h, rest := title.SplitH1([]byte(s)); h != "" && len(rest) == 0 {
		s = h
	}
	return s, nil
}

// Replace writes lead, title, and body after fence. fence is copied unchanged.
// lead is the text that sat above the heading. Empty lead writes the heading
// first. Lead and body newlines are LF.
func Replace(fence []byte, title, lead, body string) []byte {
	lead = posixLF(lead)
	body = posixLF(body)
	var b bytes.Buffer
	b.Grow(len(fence) + 2 + len(lead) + 1 + len(title) + 1 + len(body) + 2)
	b.Write(fence)
	if !bytes.HasSuffix(fence, []byte("\n")) {
		b.WriteByte('\n')
	}
	b.WriteString(lead)
	if lead != "" && !strings.HasSuffix(lead, "\n") {
		b.WriteByte('\n')
	}
	b.WriteString("# ")
	b.WriteString(title)
	b.WriteByte('\n')
	b.WriteString(body)
	if body != "" && !strings.HasSuffix(body, "\n") {
		b.WriteByte('\n')
	}
	return b.Bytes()
}

func posixLF(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}
