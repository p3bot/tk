// Command tkv is a localhost web dashboard for humans browsing tk tickets.
// Entry point only: run, map error to exit code, exit. Logic lives in internal/tkv.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/p3bot/tk/internal/tkv"
)

func main() {
	err := tkv.NewApp().Run(os.Args[1:])
	if err == nil || errors.Is(err, flag.ErrHelp) {
		return
	}
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
