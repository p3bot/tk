package tkv

import (
	"fmt"
	"os/exec"
	"runtime"
	"syscall"
)

func openBrowser(url string) error {
	cmd, err := browserCmd(url)
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("open browser: %w", err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func browserCmd(url string) (*exec.Cmd, error) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	default:
		return nil, fmt.Errorf("open browser: unsupported OS %s", runtime.GOOS)
	}
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	// New session so a terminal SIGINT/SIGHUP to tkv does not kill the browser.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd, nil
}
