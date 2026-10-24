package tkv

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"cuelang.org/go/cue/cuecontext"

	"github.com/p3bot/tk/internal/flock"
	"github.com/p3bot/tk/internal/registry"
	"github.com/p3bot/tk/internal/scopeconfig"
)

func TestMain(m *testing.M) {
	switch {
	case os.Getenv("TKV_TEST_LOCK_HELPER") == "1":
		runLockHelper()
		os.Exit(0)
	case os.Getenv("TKV_TEST_PARENT_MIDDLE") == "1":
		runParentWatchMiddle()
		os.Exit(0)
	case os.Getenv("TKV_TEST_PARENT_WATCH") == "1":
		runParentWatchChild()
		os.Exit(0)
	}
	code := m.Run()
	if builtBinDir != "" {
		_ = os.RemoveAll(builtBinDir)
	}
	os.Exit(code)
}

func runParentWatchChild() {
	stop := watchParentUntilReady()
	fmt.Println("watching")
	_ = os.Stdout.Sync()
	if os.Getenv("TKV_TEST_PARENT_MODE") == "die" {
		time.Sleep(30 * time.Second)
		fmt.Println("still-alive")
		return
	}
	if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	stop()
	fmt.Println("disarmed")
	_ = os.Stdout.Sync()
	time.Sleep(500 * time.Millisecond)
	path := os.Getenv("TKV_TEST_PARENT_RESULT")
	if err := os.WriteFile(path, []byte("alive"), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	time.Sleep(30 * time.Second)
}

func runParentWatchMiddle() {
	bin, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	cmd := exec.Command(bin)
	cmd.Env = parentWatchChildEnv()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	reader := bufio.NewReader(stdout)
	line, err := reader.ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "watching" {
		fmt.Fprintf(os.Stderr, "child start: %q err=%v\n", line, err)
		os.Exit(1)
	}
	if os.Getenv("TKV_TEST_PARENT_MODE") == "die" {
		fmt.Println(cmd.Process.Pid)
		os.Exit(0)
	}
	if _, err := io.WriteString(stdin, "\n"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	line, err = reader.ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "disarmed" {
		fmt.Fprintf(os.Stderr, "child disarm: %q err=%v\n", line, err)
		os.Exit(1)
	}
	fmt.Println(cmd.Process.Pid)
	os.Exit(0)
}

func parentWatchChildEnv() []string {
	out := make([]string, 0, len(os.Environ())+4)
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "TKV_TEST_PARENT_MIDDLE=") || strings.HasPrefix(e, "TKV_TEST_PARENT_WATCH=") || strings.HasPrefix(e, "TKV_READY_FD=") || strings.HasPrefix(e, "TKV_TEST_LOCK_HELPER=") {
			continue
		}
		out = append(out, e)
	}
	return append(out,
		"TKV_TEST_PARENT_WATCH=1",
		"TKV_READY_FD=3",
		"TKV_TEST_PARENT_MODE="+os.Getenv("TKV_TEST_PARENT_MODE"),
		"TKV_TEST_PARENT_RESULT="+os.Getenv("TKV_TEST_PARENT_RESULT"),
	)
}

func runLockHelper() {
	path := os.Getenv("TKV_TEST_LOCK_PATH")
	port, err := strconv.Atoi(os.Getenv("TKV_TEST_LOCK_PORT"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	lk, acquired, err := flock.TryAcquire(path)
	if err != nil || !acquired {
		fmt.Fprintf(os.Stderr, "helper lock: acquired=%v err=%v\n", acquired, err)
		os.Exit(1)
	}
	if err := lk.WriteHead(formatMeta(os.Getpid(), port)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if os.Getenv("TKV_TEST_LOCK_DELAY_SERVE") == "1" {
		fmt.Println("locked")
		if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/static/style.css", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	go func() { _ = http.Serve(ln, mux) }()
	fmt.Println("ready")
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM)
	<-sig
	_ = ln.Close()
	_ = lk.Release()
}

func TestParentWatchExitsWhenParentDies(t *testing.T) {
	pid := startParentWatch(t, "die", "")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("child kept running after its parent exited")
}

func TestParentWatchSurvivesHandoff(t *testing.T) {
	result := filepath.Join(t.TempDir(), "result")
	pid := startParentWatch(t, "disarm", result)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(result)
		if err == nil && string(b) == "alive" {
			if err := syscall.Kill(pid, 0); err != nil {
				t.Fatal("child exited after the watch was disarmed")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("child did not survive the parent exiting after disarm")
}

func startParentWatch(t *testing.T, mode, result string) int {
	t.Helper()
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(),
		"TKV_TEST_PARENT_MIDDLE=1",
		"TKV_TEST_PARENT_MODE="+mode,
		"TKV_TEST_PARENT_RESULT="+result,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("middle: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	pid, err := strconv.Atoi(strings.TrimSpace(stdout.String()))
	if err != nil {
		t.Fatalf("pid %q: %v stderr=%s", stdout.String(), err, stderr.String())
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	return pid
}

func TestSplitVerb(t *testing.T) {
	verb, rest := splitVerb(nil)
	if verb != "start" || len(rest) != 0 {
		t.Fatalf("bare = %q %q", verb, rest)
	}
	verb, rest = splitVerb([]string{"--port", "9"})
	if verb != "start" || len(rest) != 2 {
		t.Fatalf("flags = %q %q", verb, rest)
	}
	verb, rest = splitVerb([]string{"stop", "--help"})
	if verb != "stop" || len(rest) != 1 || rest[0] != "--help" {
		t.Fatalf("verb = %q %q", verb, rest)
	}
}

func TestExecuteUnknownCommand(t *testing.T) {
	app := newTestApp(t)
	err := app.Execute([]string{"nope"})
	if err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("err = %v", err)
	}
}

func TestMetaNamesTheHolder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, lockFileName)
	lk, acquired, err := flock.TryAcquire(path)
	if err != nil || !acquired {
		t.Fatalf("acquired=%v err=%v", acquired, err)
	}
	defer func() { _ = lk.Release() }()
	if err := lk.WriteHead(formatMeta(os.Getpid(), 8736)); err != nil {
		t.Fatal(err)
	}
	holds, err := pidHoldsLock(os.Getpid(), path)
	if err != nil || !holds {
		t.Fatalf("holds=%v err=%v", holds, err)
	}
	pid, port, err := awaitHolder(path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if pid != os.Getpid() || port != 8736 {
		t.Fatalf("holder = %d %d", pid, port)
	}
	gone, err := pidHoldsLock(1<<30, path)
	if err != nil {
		t.Fatal(err)
	}
	if gone {
		t.Fatal("an unused pid must not look like the holder")
	}
}

func TestStartAttachesAndStopReleases(t *testing.T) {
	app := newTestApp(t)
	path := filepath.Join(app.StateDir, lockFileName)
	port := freePort(t)
	cmd := startLockHelper(t, path, port)
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_, _ = cmd.Process.Wait()
	})

	var out bytes.Buffer
	app.Stdout = &out
	if err := app.Start([]string{"--no-open"}); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != fmt.Sprintf("http://127.0.0.1:%d/", port) {
		t.Fatalf("attach url = %q", out.String())
	}

	out.Reset()
	err := app.Start([]string{"--no-open", "--port", "9000"})
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("port %d", port)) {
		t.Fatalf("mismatch err = %v", err)
	}

	out.Reset()
	if err := app.Status(nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), fmt.Sprintf("pid %d", cmd.Process.Pid)) || !strings.Contains(out.String(), fmt.Sprintf("port %d", port)) {
		t.Fatalf("status = %q", out.String())
	}

	out.Reset()
	if err := app.Run([]string{"--no-open", "--port", "8742"}); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("foreground err = %v", err)
	}

	out.Reset()
	if err := app.Stop(nil); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "stopped" {
		t.Fatalf("stop = %q", out.String())
	}
	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait() }()
	select {
	case <-waitErr:
	case <-time.After(3 * time.Second):
		t.Fatal("helper still running after stop")
	}
	out.Reset()
	err = app.Status(nil)
	if !errors.Is(err, errNotRunning) {
		t.Fatalf("status after stop = %v", err)
	}
	out.Reset()
	if err := app.Stop(nil); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "tkv is not running" {
		t.Fatalf("second stop = %q", out.String())
	}
}

func TestStopReturnsWhileSuccessorHoldsLock(t *testing.T) {
	app := newTestApp(t)
	path := filepath.Join(app.StateDir, lockFileName)
	port := freePort(t)
	first := startLockHelper(t, path, port)
	successorPort := freePort(t)
	var successor *exec.Cmd
	t.Cleanup(func() {
		for _, cmd := range []*exec.Cmd{first, successor} {
			if cmd != nil && cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
		}
	})

	hookReached := make(chan struct{})
	releaseHook := make(chan struct{})
	var releaseOnce sync.Once
	releaseHookFn := func() { releaseOnce.Do(func() { close(releaseHook) }) }
	t.Cleanup(releaseHookFn)
	app.afterStopSignal = func() {
		close(hookReached)
		<-releaseHook
	}
	var out bytes.Buffer
	app.Stdout = &out
	done := make(chan error, 1)
	go func() { done <- app.Stop(nil) }()

	select {
	case <-hookReached:
	case <-time.After(3 * time.Second):
		t.Fatal("stop did not signal the holder")
	}
	waitErr := make(chan error, 1)
	go func() { waitErr <- first.Wait() }()
	select {
	case <-waitErr:
	case <-time.After(3 * time.Second):
		t.Fatal("signalled holder still running")
	}
	successor = startLockHelper(t, path, successorPort)
	releaseHookFn()

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stop waited for the successor")
	}
	if strings.TrimSpace(out.String()) != "stopped" {
		t.Fatalf("stop = %q", out.String())
	}
	if err := syscall.Kill(successor.Process.Pid, 0); err != nil {
		t.Fatalf("successor pid %d is not running: %v", successor.Process.Pid, err)
	}
}

type lockProc struct {
	cmd    *exec.Cmd
	stdout *bufio.Reader
	stdin  io.WriteCloser
	stderr *bytes.Buffer
}

func (p *lockProc) waitLine(t *testing.T, want string) {
	t.Helper()
	line, err := p.stdout.ReadString('\n')
	if err != nil || strings.TrimSpace(line) != want {
		t.Fatalf("helper %s = %q err=%v stderr=%s", want, line, err, p.stderr.String())
	}
}

func startLockHelper(t *testing.T, path string, port int) *exec.Cmd {
	t.Helper()
	p := launchLockHelper(t, path, port, false)
	p.waitLine(t, "ready")
	return p.cmd
}

func launchLockHelper(t *testing.T, path string, port int, delayServe bool) *lockProc {
	t.Helper()
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin)
	env := []string{
		"TKV_TEST_LOCK_HELPER=1",
		"TKV_TEST_LOCK_PATH=" + path,
		"TKV_TEST_LOCK_PORT=" + strconv.Itoa(port),
	}
	if delayServe {
		env = append(env, "TKV_TEST_LOCK_DELAY_SERVE=1")
	}
	cmd.Env = append(os.Environ(), env...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return &lockProc{cmd: cmd, stdout: bufio.NewReader(stdout), stdin: stdin, stderr: &stderr}
}

func TestAttachWaitsUntilServing(t *testing.T) {
	app := newTestApp(t)
	path := filepath.Join(app.StateDir, lockFileName)
	port := freePort(t)
	helper := launchLockHelper(t, path, port, true)
	t.Cleanup(func() {
		if helper.cmd.Process != nil {
			_ = helper.cmd.Process.Kill()
			_, _ = helper.cmd.Process.Wait()
		}
	})
	helper.waitLine(t, "locked")

	out := &concBuf{}
	app.Stdout = out
	errCh := make(chan error, 1)
	go func() { errCh <- app.Start([]string{"--no-open"}) }()

	select {
	case err := <-errCh:
		t.Fatalf("Start returned before the server answered: %v url=%q", err, out.String())
	case <-time.After(200 * time.Millisecond):
	}
	if strings.TrimSpace(out.String()) != "" {
		t.Fatalf("printed %q before the server answered", out.String())
	}

	other := newTestApp(t)
	other.StateDir = app.StateDir
	other.ConfigDir = app.ConfigDir
	other.Cwd = app.Cwd
	mismatch := make(chan error, 1)
	otherPort := freePort(t)
	if otherPort == port {
		otherPort = port - 1
	}
	go func() { mismatch <- other.Start([]string{"--no-open", "--port", strconv.Itoa(otherPort)}) }()
	select {
	case err := <-mismatch:
		if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("port %d", port)) {
			t.Fatalf("mismatch err = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a different --port waited for the server")
	}

	if _, err := helper.stdin.Write([]byte("serve\n")); err != nil {
		t.Fatal(err)
	}
	helper.waitLine(t, "ready")
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Start did not return after the server answered")
	}
	if strings.TrimSpace(out.String()) != fmt.Sprintf("http://127.0.0.1:%d/", port) {
		t.Fatalf("attach url = %q", out.String())
	}
}

func TestStartReplacesExitedHolder(t *testing.T) {
	bin := builtTKV(t)
	state := t.TempDir()
	config := t.TempDir()
	lockDir := filepath.Join(state, "tk")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(lockDir, lockFileName)
	port := freePort(t)
	helper := launchLockHelper(t, path, port, true)
	t.Cleanup(func() {
		if helper.cmd.Process != nil {
			_ = helper.cmd.Process.Kill()
			_, _ = helper.cmd.Process.Wait()
		}
		_, _ = runTKV(bin, state, config, "stop")
	})
	helper.waitLine(t, "locked")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--no-open", "--port", strconv.Itoa(port))
	cmd.Env = tkvEnv(state, config)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		t.Fatalf("start returned before the holder exited: %v\nstderr: %s", err, stderr.String())
	case <-time.After(200 * time.Millisecond):
	}
	if err := helper.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_, _ = helper.cmd.Process.Wait()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("start: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
		}
	case <-time.After(15 * time.Second):
		t.Fatal("start did not replace the exited holder")
	}
	want := fmt.Sprintf("http://127.0.0.1:%d/", port)
	if strings.TrimSpace(stdout.String()) != want {
		t.Fatalf("url = %q stderr=%s", stdout.String(), stderr.String())
	}
	resp, err := http.Get(want)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET = %d", resp.StatusCode)
	}
}

var (
	builtOnce   sync.Once
	builtBin    string
	builtBinDir string
	builtErr    error
)

func builtTKV(t *testing.T) string {
	t.Helper()
	builtOnce.Do(func() {
		wd, err := os.Getwd()
		if err != nil {
			builtErr = err
			return
		}
		root := filepath.Clean(filepath.Join(wd, "..", ".."))
		dir, err := os.MkdirTemp("", "tkv-bin-")
		if err != nil {
			builtErr = err
			return
		}
		builtBinDir = dir
		builtBin = filepath.Join(dir, "tkv")
		cmd := exec.Command("go", "build", "-o", builtBin, "./cmd/tkv")
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			builtErr = fmt.Errorf("build tkv: %w\n%s", err, out)
		}
	})
	if builtErr != nil {
		t.Fatal(builtErr)
	}
	return builtBin
}

func TestBackgroundStartStop(t *testing.T) {
	bin := builtTKV(t)
	state := t.TempDir()
	config := t.TempDir()
	port := freePort(t)
	t.Cleanup(func() {
		_, _ = runTKV(bin, state, config, "stop")
	})

	out, err := runTKV(bin, state, config, "--no-open", "--port", strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("http://127.0.0.1:%d/", port)
	if strings.TrimSpace(out) != want {
		t.Fatalf("start url = %q", out)
	}
	resp, err := http.Get(want)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET = %d", resp.StatusCode)
	}

	attached, err := runTKV(bin, state, config, "--no-open")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(attached) != want {
		t.Fatalf("attach url = %q", attached)
	}

	same, err := runTKV(bin, state, config, "start", "--no-open", "--port", strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(same) != want {
		t.Fatalf("same port url = %q", same)
	}

	other := freePort(t)
	if _, err := runTKV(bin, state, config, "start", "--no-open", "--port", strconv.Itoa(other)); err == nil {
		t.Fatal("a different --port must be refused")
	}

	status, err := runTKV(bin, state, config, "status")
	if err != nil {
		t.Fatal(err)
	}
	pid := statusPid(t, status)
	if !strings.Contains(status, fmt.Sprintf("port %d", port)) {
		t.Fatalf("status = %q", status)
	}
	if runtime.GOOS == "linux" {
		envb, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(envb, []byte("GIT_TERMINAL_PROMPT=0")) || bytes.Contains(envb, []byte("GIT_TERMINAL_PROMPT=1")) {
			t.Fatal("child environ missing forced prompt disable")
		}
		stdin, err := os.Readlink(fmt.Sprintf("/proc/%d/fd/0", pid))
		if err != nil {
			t.Fatal(err)
		}
		if stdin != "/dev/null" && stdin != os.DevNull {
			t.Fatalf("stdin = %s", stdin)
		}
	}
	sidRaw, err := exec.Command("ps", "-o", "sid=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(sidRaw)) != strconv.Itoa(pid) {
		t.Fatalf("sid = %q, want the process to lead its own session", sidRaw)
	}
	logPath := filepath.Join(state, "tk", logFileName)
	logb, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(logb, []byte(want)) {
		t.Fatalf("log = %q", logb)
	}

	fg := exec.Command(bin, "foreground", "--no-open", "--port", strconv.Itoa(port))
	fg.Env = tkvEnv(state, config)
	var fgErr bytes.Buffer
	fg.Stderr = &fgErr
	err = fg.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != ExitAlreadyRunning {
		t.Fatalf("foreground err = %v stderr=%s", err, fgErr.String())
	}

	stopped, err := runTKV(bin, state, config, "stop")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(stopped) != "stopped" {
		t.Fatalf("stop = %q", stopped)
	}
	if _, err := http.Get(want); err == nil {
		t.Fatal("server still accepting after stop")
	}
	if _, err := runTKV(bin, state, config, "status"); err == nil {
		t.Fatal("status should fail once stopped")
	}
}

func TestStartShowsListenError(t *testing.T) {
	bin := builtTKV(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	port := ln.Addr().(*net.TCPAddr).Port

	_, err = runTKV(bin, t.TempDir(), t.TempDir(), "--no-open", "--port", strconv.Itoa(port))
	if err == nil || !strings.Contains(err.Error(), "listen") {
		t.Fatalf("err = %v", err)
	}
}

func TestOverlappingStarts(t *testing.T) {
	bin := builtTKV(t)
	state := t.TempDir()
	config := t.TempDir()
	port := freePort(t)
	t.Cleanup(func() { _, _ = runTKV(bin, state, config, "stop") })

	var wg sync.WaitGroup
	outs := make([]string, 2)
	errs := make([]error, 2)
	wg.Add(2)
	for i := range outs {
		go func() {
			defer wg.Done()
			outs[i], errs[i] = runTKV(bin, state, config, "--no-open", "--port", strconv.Itoa(port))
		}()
	}
	wg.Wait()
	want := fmt.Sprintf("http://127.0.0.1:%d/", port)
	for i := range outs {
		if errs[i] != nil {
			t.Fatalf("start %d: %v", i, errs[i])
		}
		if strings.TrimSpace(outs[i]) != want {
			t.Fatalf("start %d url = %q", i, outs[i])
		}
	}
}

func TestStartScopeInURL(t *testing.T) {
	bin := builtTKV(t)
	state := t.TempDir()
	config := t.TempDir()
	scopeDir := t.TempDir()
	if err := scopeconfig.WriteMinimal(scopeDir, "wc", false); err != nil {
		t.Fatal(err)
	}
	store := registry.NewStore(cuecontext.New(), filepath.Join(config, "tk"))
	if err := store.WriteRegistry(map[string]registry.Entry{
		"wc": {Dir: scopeDir, Root: scopeDir},
	}); err != nil {
		t.Fatal(err)
	}
	port := freePort(t)
	t.Cleanup(func() { _, _ = runTKV(bin, state, config, "stop") })

	out, err := runTKV(bin, state, config, "--no-open", "--port", strconv.Itoa(port), "--scope", "wc")
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("http://127.0.0.1:%d/scope/wc", port)
	if strings.TrimSpace(out) != want {
		t.Fatalf("url = %q", out)
	}
}

func TestBrowserOutlivesStart(t *testing.T) {
	bin := builtTKV(t)
	state := t.TempDir()
	config := t.TempDir()
	stub := t.TempDir()
	pidFile := filepath.Join(t.TempDir(), "browser.pid")
	argFile := filepath.Join(t.TempDir(), "browser.arg")
	name := "xdg-open"
	if runtime.GOOS == "darwin" {
		name = "open"
	}
	script := "#!/bin/sh\nprintf '%s\\n' \"$$\" > " + strconv.Quote(pidFile) +
		"\nprintf '%s\\n' \"$1\" > " + strconv.Quote(argFile) + "\nexec sleep 30\n"
	if err := os.WriteFile(filepath.Join(stub, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	port := freePort(t)
	t.Cleanup(func() {
		if b, err := os.ReadFile(pidFile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				_ = syscall.Kill(pid, syscall.SIGTERM)
			}
		}
		_, _ = runTKV(bin, state, config, "stop")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--port", strconv.Itoa(port))
	cmd.Env = tkvEnv(state, config)
	for i, e := range cmd.Env {
		if strings.HasPrefix(e, "PATH=") {
			cmd.Env[i] = "PATH=" + stub + string(os.PathListSeparator) + strings.TrimPrefix(e, "PATH=")
		}
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("%v\nstderr: %s", err, stderr.String())
	}
	want := fmt.Sprintf("http://127.0.0.1:%d/", port)
	if strings.TrimSpace(stdout.String()) != want {
		t.Fatalf("url = %q", stdout.String())
	}
	var b []byte
	var err error
	deadline := time.Now().Add(2 * time.Second)
	for {
		b, err = os.ReadFile(pidFile)
		if err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		which := exec.Command("sh", "-c", "command -v "+name)
		which.Env = cmd.Env
		wb, _ := which.CombinedOutput()
		t.Fatalf("%v\nwhich: %s\nstderr: %q", err, wb, stderr.String())
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("browser pid %d is not running: %v", pid, err)
	}
	got, err := os.ReadFile(argFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != want {
		t.Fatalf("browser arg = %q", got)
	}
}

func statusPid(t *testing.T, status string) int {
	t.Helper()
	for _, line := range strings.Split(status, "\n") {
		if strings.HasPrefix(line, "pid ") {
			pid, err := strconv.Atoi(strings.TrimPrefix(line, "pid "))
			if err != nil {
				t.Fatal(err)
			}
			return pid
		}
	}
	t.Fatalf("no pid in %q", status)
	return 0
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func tkvEnv(state, config string) []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"XDG_STATE_HOME=" + state,
		"XDG_CONFIG_HOME=" + config,
		"GIT_TERMINAL_PROMPT=1",
		"TERM=xterm",
	}
}

func runTKV(bin, state, config string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = tkvEnv(state, config)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return stdout.String(), fmt.Errorf("%w\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	return stdout.String(), nil
}
