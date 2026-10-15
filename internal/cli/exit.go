package cli

import (
	"errors"
	"fmt"
)

// Exit codes: 0 success, 2 usage, else generic non-zero. Tokens carry machine signal.
const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

// ExitError is the process exit code a handler wants main to use.
type ExitError struct {
	Code int
	Err  error
	// Plain: non-fault diagnostic (e.g. empty next queue); printed without "error:" label.
	Plain bool
	// AlreadyPrinted: the diagnostic is already on stderr. PrintError stays quiet.
	AlreadyPrinted bool
}

func (e *ExitError) Error() string { return e.Err.Error() }
func (e *ExitError) Unwrap() error { return e.Err }

// ExitCodeFromError maps err to a process exit code (nil → 0).
func ExitCodeFromError(err error) int {
	if err == nil {
		return exitOK
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	return exitFailure
}

func usageErrorf(format string, a ...any) error {
	return &ExitError{Code: exitUsage, Err: fmt.Errorf(format, a...)}
}
