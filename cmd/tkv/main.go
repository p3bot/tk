// Command tkv is a localhost web dashboard for humans browsing tk tickets.
// Entry point only: run, map error to exit code, exit. Logic lives in internal/tkv.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/p3bot/tk/internal/tkv"
)

func main() {
	err := tkv.NewApp().Execute(os.Args[1:])
	if err == nil {
		return
	}
	fmt.Fprintln(os.Stderr, err)
	var usage *tkv.UsageError
	switch {
	case errors.As(err, &usage):
		os.Exit(tkv.ExitUsage)
	case errors.Is(err, tkv.ErrAlreadyRunning):
		os.Exit(tkv.ExitAlreadyRunning)
	default:
		os.Exit(1)
	}
}
