//go:build linux || darwin

package tkv

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/p3bot/tk/internal/flock"
)

const (
	lockFileName = "tkv.lock"
	logFileName  = "tkv.log"
	// metaSize is pid (10 digits), newline, port (5 digits), newline.
	metaSize = 17
	// ExitAlreadyRunning is the foreground exit status when the machine lock
	// is already held. tkv start treats it as "attach to the server that won".
	ExitAlreadyRunning = 75
)

// ErrAlreadyRunning is returned by the attached server when another tkv holds
// the lock. It may be wrapped with the running origin.
var ErrAlreadyRunning = errors.New("tkv is already running")

// errSpawnBusy means the child lost the lock to a server that is already up.
var errSpawnBusy = errors.New("tkv server is already starting")

var errNotRunning = errors.New("tkv is not running")

// Start detaches a server, or attaches to the one already holding the lock.
// An explicit --port that is not the running port is refused. If that holder
// exits during the attach, Start tries once more so the dashboard comes up.
func (a *App) Start(args []string) error {
	if err := a.parseFlags(args); err != nil {
		return err
	}
	dir, err := a.prepareStateDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, lockFileName)
	err = a.startOrAttach(dir, path)
	if errors.Is(err, errNotRunning) {
		err = a.startOrAttach(dir, path)
	}
	return err
}

func (a *App) startOrAttach(dir, path string) error {
	held, err := lockHeld(path)
	if err != nil {
		return err
	}
	if held {
		return a.attach(path)
	}
	err = a.spawn(dir)
	if errors.Is(err, errSpawnBusy) {
		return a.attach(path)
	}
	return err
}

// Stop sends SIGTERM to the lock holder and waits until that process drops
// the lock file. A successor that takes the lock is left running. The
// server's own signal path drains in-flight mark and claim work. A dashboard
// that is not running is a successful stop.
func (a *App) Stop(args []string) error {
	if err := a.parseFlags(args); err != nil {
		return err
	}
	dir, err := a.stateDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, lockFileName)
	held, err := lockHeld(path)
	if err != nil {
		return err
	}
	if !held {
		fmt.Fprintln(a.stdout(), "tkv is not running")
		return nil
	}
	pid, _, err := awaitHolder(path, 5*time.Second)
	if err != nil {
		return err
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("signal tkv: %w", err)
	}
	if a.afterStopSignal != nil {
		a.afterStopSignal()
	}
	if err := waitHolderGone(path, pid); err != nil {
		return err
	}
	fmt.Fprintln(a.stdout(), "stopped")
	return nil
}

// waitHolderGone polls until pid no longer has path open. The lock may be
// held by a successor; that process was not the one Stop signalled.
func waitHolderGone(path string, pid int) error {
	for {
		holds, err := pidHoldsLock(pid, path)
		if err != nil {
			return err
		}
		if !holds {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Status prints the running server, or returns an error when none is up.
func (a *App) Status(args []string) error {
	if err := a.parseFlags(args); err != nil {
		return err
	}
	dir, err := a.stateDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, lockFileName)
	held, err := lockHeld(path)
	if err != nil {
		return err
	}
	if !held {
		return errNotRunning
	}
	pid, port, err := awaitHolder(path, 5*time.Second)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout(), "pid %d\nport %d\nurl %s/\nlog %s\n",
		pid, port, baseURL(port), filepath.Join(dir, logFileName))
	return nil
}

func (a *App) prepareStateDir() (string, error) {
	dir, err := a.stateDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create XDG state directory %s: %w", dir, err)
	}
	return dir, nil
}

// serverLock takes the machine lock and records pid and port before listen.
// The release function drops the lock. A busy lock is ErrAlreadyRunning.
func serverLock(stateDir string, port int) (func(), error) {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return nil, fmt.Errorf("create XDG state directory %s: %w", stateDir, err)
	}
	path := filepath.Join(stateDir, lockFileName)
	lk, acquired, err := flock.TryAcquire(path)
	if err != nil {
		return nil, err
	}
	if !acquired {
		_, running, err := awaitHolder(path, 5*time.Second)
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("%w at %s/", ErrAlreadyRunning, baseURL(running))
	}
	if err := lk.WriteHead(formatMeta(os.Getpid(), port)); err != nil {
		_ = lk.Release()
		return nil, err
	}
	return func() { _ = lk.Release() }, nil
}

func (a *App) attach(lockPath string) error {
	pid, port, err := awaitHolder(lockPath, 5*time.Second)
	if err != nil {
		return err
	}
	if a.PortSet && a.Port != port {
		return fmt.Errorf("tkv is already running on port %d (%s/)", port, baseURL(port))
	}
	// The lock record is written before listen so stop can signal during index
	// open. Publish the URL only once that port answers.
	if err := waitUntilServing(lockPath, pid, port); err != nil {
		return err
	}
	url, err := a.LandingURL(port)
	if err != nil {
		return err
	}
	fmt.Fprintln(a.stdout(), url)
	if err := a.maybeOpen(url); err != nil {
		fmt.Fprintln(a.stderr(), err)
	}
	return nil
}

// waitUntilServing polls the stylesheet until it returns 200, or the process
// that held the lock at attach time is gone. There is no deadline: index open
// sits in front of listen and can outlast any short cap.
func waitUntilServing(lockPath string, pid, port int) error {
	ready := baseURL(port) + "/static/style.css"
	client := &http.Client{Timeout: 200 * time.Millisecond}
	for {
		holds, err := pidHoldsLock(pid, lockPath)
		if err != nil {
			return err
		}
		if !holds {
			return errNotRunning
		}
		req, err := http.NewRequest(http.MethodGet, ready, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// spawn re-execs this binary in a new session. The parent returns when the
// child writes its landing URL, or when the child exits. On success the child
// is left running; this process is about to exit and init adopts it.
func (a *App) spawn(stateDir string) error {
	bin, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find tkv executable: %w", err)
	}
	args := []string{"foreground", "--port", strconv.Itoa(a.Port), "--no-open"}
	if a.ScopeFlag != "" {
		args = append(args, "--scope", a.ScopeFlag)
	}
	logPath := filepath.Join(stateDir, logFileName)
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open log %s: %w", logPath, err)
	}
	defer func() { _ = logf.Close() }()

	pr, pw, err := os.Pipe()
	if err != nil {
		return err
	}
	defer func() { _ = pr.Close() }()

	cmd := exec.Command(bin, args...)
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.ExtraFiles = []*os.File{pw}
	cmd.Env = childEnv()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		_ = pw.Close()
		return fmt.Errorf("start tkv: %w", err)
	}
	_ = pw.Close()

	line, readErr := bufio.NewReader(pr).ReadString('\n')
	if readErr == nil {
		fmt.Fprint(a.stdout(), line)
		if err := a.maybeOpen(strings.TrimSpace(line)); err != nil {
			fmt.Fprintln(a.stderr(), err)
		}
		return nil
	}
	waitErr := cmd.Wait()
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) && exitErr.ExitCode() == ExitAlreadyRunning {
		return errSpawnBusy
	}
	tail, _ := logTail(logPath, 20)
	if waitErr == nil {
		return fmt.Errorf("tkv exited before it was ready\n%s", tail)
	}
	if tail == "" {
		return fmt.Errorf("tkv did not start: %w", waitErr)
	}
	return fmt.Errorf("tkv did not start: %w\n%s", waitErr, tail)
}

func childEnv() []string {
	src := os.Environ()
	out := make([]string, 0, len(src)+2)
	for _, e := range src {
		if strings.HasPrefix(e, "TKV_READY_FD=") || strings.HasPrefix(e, "GIT_TERMINAL_PROMPT=") {
			continue
		}
		out = append(out, e)
	}
	out = append(out, "TKV_READY_FD=3", "GIT_TERMINAL_PROMPT=0")
	return out
}

func formatMeta(pid, port int) []byte {
	return []byte(fmt.Sprintf("%010d\n%05d\n", pid, port))
}

func readMeta(path string) (pid, port int, ok bool, err error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, false, err
	}
	if len(b) < metaSize {
		return 0, 0, false, nil
	}
	b = b[:metaSize]
	if b[10] != '\n' || b[16] != '\n' {
		return 0, 0, false, nil
	}
	pid, err1 := strconv.Atoi(string(b[:10]))
	port, err2 := strconv.Atoi(string(b[11:16]))
	if err1 != nil || err2 != nil || pid <= 0 || port < 1 || port > 65535 {
		return 0, 0, false, nil
	}
	return pid, port, true, nil
}

// awaitHolder waits until the lock file names a pid that still has it open.
func awaitHolder(path string, wait time.Duration) (int, int, error) {
	deadline := time.Now().Add(wait)
	var why string
	for {
		pid, port, ok, err := readMeta(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return 0, 0, errNotRunning
			}
			return 0, 0, err
		}
		if ok {
			holds, err := pidHoldsLock(pid, path)
			if err != nil {
				return 0, 0, err
			}
			if holds {
				return pid, port, nil
			}
			why = fmt.Sprintf("pid %d does not hold the lock", pid)
		} else {
			why = "no server record"
		}
		held, err := lockHeld(path)
		if err != nil {
			return 0, 0, err
		}
		if !held || time.Now().After(deadline) {
			if !held {
				return 0, 0, errNotRunning
			}
			return 0, 0, fmt.Errorf("tkv lock is held but the server is not identifiable: %s", why)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// lockHeld reports whether some other process holds path. A missing file is
// not held. A free lock is taken only for the probe and then released.
func lockHeld(path string) (bool, error) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	lk, acquired, err := flock.TryAcquire(path)
	if err != nil {
		return false, err
	}
	if acquired {
		return false, lk.Release()
	}
	return true, nil
}

func logTail(path string, n int) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	text := strings.TrimRight(string(b), "\n")
	if text == "" {
		return "", nil
	}
	lines := strings.Split(text, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n"), nil
}
