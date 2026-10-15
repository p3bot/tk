package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/term"

	"github.com/p3bot/tk/internal/pathutil"
	"github.com/p3bot/tk/internal/token"
)

const (
	ansiRed   = "\x1b[31m"
	ansiReset = "\x1b[0m"
)

// wantColor: TTY-only; NO_COLOR (any value) disables. No FORCE_COLOR.
func wantColor(isTTY bool) bool {
	if _, disabled := os.LookupEnv("NO_COLOR"); disabled {
		return false
	}
	return isTTY
}

func isTerminal(f *os.File) bool {
	return term.IsTerminal(int(f.Fd()))
}

// PrintError writes a fatal error to stderr.
func PrintError(err error) {
	fprintError(os.Stderr, err, wantColor(isTerminal(os.Stderr)))
}

// fprintError: closed tokens and Plain diagnostics print verbatim (no "error:" label).
func fprintError(w io.Writer, err error, colorAllowed bool) {
	var ee *ExitError
	if errors.As(err, &ee) && ee.AlreadyPrinted {
		return
	}
	msg := err.Error()
	plain := errors.As(err, &ee) && ee.Plain
	if plain || token.HasKnownPrefix(msg) || !colorAllowed {
		fmt.Fprintln(w, msg)
		return
	}
	fmt.Fprintln(w, ansiRed+"error:"+ansiReset+" "+msg)
}

// absPath cleans, absolutises, and symlink-resolves so path comparisons match git's spelling.
func absPath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path for %q: %w", p, err)
	}
	return pathutil.Canonical(abs), nil
}
