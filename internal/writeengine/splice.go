package writeengine

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/p3bot/tk/internal/frontmatter"
	"github.com/p3bot/tk/internal/title"
)

// SpliceInput is one H1+body splice. Base is ClobberKey from GET.
type SpliceInput struct {
	Scope  string
	Dir    string
	Lookup Lookup
	Title  string
	Body   string
	Base   string
}

// Splice replaces the post-fence H1 and body, leaving fence bytes unchanged.
// It never self-commits; durability matches create.
func Splice(deps Deps, in SpliceInput) (Result, error) {
	title, err := spliceTitle(in.Title)
	if err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(in.Base) == "" {
		return Result{}, &UsageError{Msg: "splice needs a clobber predicate"}
	}

	sess, err := Begin(deps, in.Scope, in.Dir)
	if err != nil {
		return Result{}, err
	}
	defer sess.Release()

	if err := sess.CheckMidRebase(); err != nil {
		return Result{}, err
	}

	out := Result{Warnings: sess.Warnings()}

	p, err := ResolveWriteRow(deps.DB, in.Scope, in.Lookup)
	if err != nil {
		return out, err
	}

	data, key, err := FileSnapshot(p.Path)
	if err != nil {
		return out, err
	}
	if key != in.Base {
		return out, &ClobberError{ID: p.ID}
	}

	_, body, present := frontmatter.Split(data)
	if !present {
		return out, &ParseQuarantineError{ID: p.ID, Msg: "no frontmatter fence"}
	}
	fence := data[:len(data)-len(body)]
	file := spliceBytes(fence, title, in.Body)
	if err := AtomicWrite(p.Path, file); err != nil {
		return out, err
	}
	if err := deps.Rec.SyncPaths(in.Scope, WrittenPaths(p.Path, "")); err != nil {
		return out, err
	}

	out.ID = p.ID
	out.OldStatus = p.Status
	out.NewStatus = p.Status
	if sess.AutoCommit && sess.HasRoot {
		out.SyncNeeded = SyncNeededReason(ctxOf(deps), deps.StateDir, in.Dir, sess.Root)
	}
	abs, err := absPath(p.Path)
	if err != nil {
		return out, err
	}
	out.Path = abs
	return out, nil
}

// FileSnapshot reads path and returns its bytes plus ClobberKey.
func FileSnapshot(path string) (data []byte, key string, err error) {
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

// ClobberKey is mtime nanoseconds and a SHA-256 of the file bytes.
func ClobberKey(mtimeNS int64, data []byte) string {
	sum := sha256.Sum256(data)
	return strconv.FormatInt(mtimeNS, 10) + ":" + hex.EncodeToString(sum[:])
}

func spliceTitle(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", &UsageError{Msg: "splice needs a non-empty title"}
	}
	if strings.ContainsAny(s, "\r\n") {
		return "", &UsageError{Msg: "splice title must be a single line"}
	}
	if h, rest := title.SplitH1([]byte(s)); h != "" && len(rest) == 0 {
		s = h
	}
	return s, nil
}

func spliceBytes(fence []byte, title, body string) []byte {
	body = posixLF(body)
	var b bytes.Buffer
	b.Grow(len(fence) + 2 + len(title) + 1 + len(body) + 2)
	b.Write(fence)
	if !bytes.HasSuffix(fence, []byte("\n")) {
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

// HTML textareas submit CRLF; some editors use bare CR. Ticket bodies are POSIX LF.
func posixLF(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}
