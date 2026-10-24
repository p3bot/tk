//go:build !linux && !darwin

package tkv

import "errors"

// ErrAlreadyRunning is the busy-lock sentinel. On an unsupported OS the
// server never reaches the lock.
var ErrAlreadyRunning = errors.New("tkv is already running")

// ExitAlreadyRunning is the foreground exit status for a busy lock.
const ExitAlreadyRunning = 75

func (a *App) Start(args []string) error {
	_, _ = a, args
	return unsupportedOS()
}

func (a *App) Stop(args []string) error {
	_, _ = a, args
	return unsupportedOS()
}

func (a *App) Status(args []string) error {
	_, _ = a, args
	return unsupportedOS()
}

func serverLock(stateDir string, port int) (func(), error) {
	_, _ = stateDir, port
	return nil, unsupportedOS()
}
