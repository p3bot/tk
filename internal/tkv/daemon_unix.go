//go:build linux || darwin

package tkv

import (
	"bufio"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/p3bot/tk/internal/flock"
	"github.com/p3bot/tk/internal/tkvcert"
	"github.com/p3bot/tk/internal/tkvconf"
)

const (
	lockFileName = "tkv.lock"
	logFileName  = "tkv.log"
	// metaSize is the scheme padded to 5 bytes, newline, pid (10 digits),
	// newline, bind address padded to 47 bytes (the longest bracketed IPv6
	// address with port), newline.
	metaSize   = 65
	metaAddrAt = 17
	metaAddrW  = 47
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
// An explicit host or port that is not the running one is refused. If that
// holder exits during the attach, Start tries once more so the dashboard
// comes up.
func (a *App) Start() error {
	dir, err := a.prepareStateDir()
	if err != nil {
		return err
	}
	conf, rb, err := a.resolveServe()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, lockFileName)
	err = a.startOrAttach(dir, path, conf, rb)
	if errors.Is(err, errNotRunning) {
		err = a.startOrAttach(dir, path, conf, rb)
	}
	return err
}

func (a *App) startOrAttach(dir, path string, conf tkvconf.Config, rb resolvedBind) error {
	held, err := lockHeld(path)
	if err != nil {
		return err
	}
	if held {
		return a.attach(dir, path, rb)
	}
	if rb.network() && len(conf.Users) == 0 {
		return errNoUsers
	}
	err = a.spawn(dir, rb.bind)
	if errors.Is(err, errSpawnBusy) {
		return a.attach(dir, path, rb)
	}
	return err
}

// Stop sends SIGTERM to the lock holder and waits until that process drops
// the lock file. A successor that takes the lock is left running. The
// server's own signal path drains in-flight mark and claim work. A dashboard
// that is not running is a successful stop.
func (a *App) Stop() error {
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
	h, err := awaitHolder(path, 5*time.Second)
	if err != nil {
		return err
	}
	pid := h.pid
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
// The fingerprint is read from cert.pem, so it reflects an in-place renewal.
func (a *App) Status() error {
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
	h, err := awaitHolder(path, 5*time.Second)
	if err != nil {
		return err
	}
	w := a.stdout()
	fmt.Fprintf(w, "pid %d\nmode %s\naddress %s\nurl %s/\n", h.pid, h.bind.modeName(), h.bind, h.bind.origin())
	if h.bind.network() {
		if leaf, err := tkvcert.ReadLeaf(tkvcert.Dir(dir)); err == nil {
			fmt.Fprintf(w, "fingerprint %s\n", tkvcert.Fingerprint(leaf.Raw))
		} else {
			fmt.Fprintf(a.stderr(), "tkv: read certificate: %v\n", err)
		}
		conf, err := a.loadConfig()
		if err != nil {
			fmt.Fprintf(a.stderr(), "tkv: %v\n", err)
		}
		for _, u := range remoteURLs(h.bind, a.shortHostname(), conf.Hosts, a.interfaceAddrs()) {
			fmt.Fprintf(w, "remote %s\n", u)
		}
	}
	fmt.Fprintf(w, "log %s\n", filepath.Join(dir, logFileName))
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

// serverLock takes the machine lock and records the scheme, pid, and bind
// address before the index opens and before listen, so stop and attach can
// find the process during a slow index open. A busy lock is
// ErrAlreadyRunning.
func serverLock(stateDir string, b bind) (func(), error) {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return nil, fmt.Errorf("create XDG state directory %s: %w", stateDir, err)
	}
	path := filepath.Join(stateDir, lockFileName)
	lk, acquired, err := flock.TryAcquire(path)
	if err != nil {
		return nil, err
	}
	if !acquired {
		h, err := awaitHolder(path, 5*time.Second)
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("%w at %s/", ErrAlreadyRunning, h.bind.origin())
	}
	if err := lk.WriteHead(formatMeta(os.Getpid(), b)); err != nil {
		_ = lk.Release()
		return nil, err
	}
	return func() { _ = lk.Release() }, nil
}

func (a *App) attach(stateDir, lockPath string, rb resolvedBind) error {
	h, err := awaitHolder(lockPath, 5*time.Second)
	if err != nil {
		return err
	}
	if (rb.hostExplicit && h.bind.Addr() != rb.Addr()) || (rb.portExplicit && h.bind.Port() != rb.Port()) {
		return fmt.Errorf("tkv is already running on %s (%s/); stop it first to listen on %s", h.bind, h.bind.origin(), rb.bind)
	}
	// The lock record is written before listen so stop can signal during index
	// open. Publish the URL only once that address answers.
	if err := waitUntilServing(stateDir, lockPath, h, 5*time.Second); err != nil {
		return err
	}
	url, err := a.LandingURL(h.bind)
	if err != nil {
		return err
	}
	fmt.Fprintln(a.stdout(), url)
	a.announceNetwork(stateDir, h.bind)
	if err := a.maybeOpen(url); err != nil {
		fmt.Fprintln(a.stderr(), err)
	}
	return nil
}

// announceNetwork prints the network-mode banner from the files a running
// dashboard wrote, for a start that did not run the server itself.
func (a *App) announceNetwork(stateDir string, b bind) {
	if !b.network() {
		return
	}
	leaf, err := tkvcert.ReadLeaf(tkvcert.Dir(stateDir))
	if err != nil {
		fmt.Fprintf(a.stderr(), "tkv: read certificate: %v\n", err)
	}
	conf, err := a.loadConfig()
	if err != nil {
		fmt.Fprintf(a.stderr(), "tkv: %v\n", err)
	}
	a.writeNetworkBanner(b, leaf, conf.Hosts)
}

// waitUntilServing polls the stylesheet until it returns 200, or the process
// that held the lock at attach time is gone. There is no deadline: index open
// sits in front of listen and can outlast any short cap. In network mode each
// attempt trusts the certificate on disk, which the server may still be
// writing when the attach begins.
//
// The lock record precedes certificate generation by milliseconds, so a
// certificate still unreadable after certGrace is broken, not pending.
func waitUntilServing(stateDir, lockPath string, h holder, certGrace time.Duration) error {
	ready := h.bind.probeURL()
	var unreadableSince time.Time
	for {
		holds, err := pidHoldsLock(h.pid, lockPath)
		if err != nil {
			return err
		}
		if !holds {
			return errNotRunning
		}
		var leaf *x509.Certificate
		if h.bind.network() {
			var err error
			leaf, err = tkvcert.ReadLeaf(tkvcert.Dir(stateDir))
			switch {
			case err == nil:
				unreadableSince = time.Time{}
			case unreadableSince.IsZero():
				unreadableSince = time.Now()
			case time.Since(unreadableSince) > certGrace:
				return fmt.Errorf("tkv is running on %s but its certificate is unreadable: %w; restart it to regenerate (tkv stop, then tkv start)", h.bind, err)
			}
		}
		if !h.bind.network() || leaf != nil {
			if probeOK(h.bind.probeClient(leaf), ready) {
				return nil
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func probeOK(client *http.Client, url string) bool {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// spawn re-execs this binary in a new session. The parent returns when the
// child writes its landing URL, or when the child exits. On success the child
// is left running; this process is about to exit and init adopts it.
func (a *App) spawn(stateDir string, b bind) error {
	bin, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find tkv executable: %w", err)
	}
	args := foregroundArgs(b, a.ScopeFlag)
	logPath := filepath.Join(stateDir, logFileName)
	logf, err := openLog(logPath)
	if err != nil {
		return err
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
		a.announceNetwork(stateDir, b)
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

// foregroundArgs is the detached child's whole command line: the resolved
// address and the scope. Users, keys, and hashes have no way in.
func foregroundArgs(b bind, scope string) []string {
	args := []string{"foreground", "--listen", b.String(), "--no-open"}
	if scope != "" {
		args = append(args, "--scope", scope)
	}
	return args
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

// openLog opens tkv.log for append at mode 0600, tightening a log an earlier
// tkv created 0644: it records client IPs and usernames.
func openLog(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open log %s: %w", path, err)
	}
	fi, err := f.Stat()
	if err == nil && fi.Mode().Perm()&0o077 != 0 {
		err = f.Chmod(0o600)
	}
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("open log %s: %w", path, err)
	}
	return f, nil
}

// holder is the lock record of the running dashboard.
type holder struct {
	pid  int
	bind bind
}

// formatMeta is the fixed-width lock record. The writer overwrites from
// offset 0 without truncating, so a reader checks fixed positions to reject
// a half-written record.
func formatMeta(pid int, b bind) []byte {
	return []byte(fmt.Sprintf("%-5s\n%010d\n%-*s\n", b.scheme(), pid, metaAddrW, b.String()))
}

// readMeta parses the lock record. legacy reports a record that starts with a
// digit, the pid-first form of an older tkv.
func readMeta(path string) (h holder, ok, legacy bool, err error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return holder{}, false, false, err
	}
	legacy = len(b) > 0 && b[0] >= '0' && b[0] <= '9'
	if len(b) < metaSize {
		return holder{}, false, legacy, nil
	}
	b = b[:metaSize]
	if b[5] != '\n' || b[16] != '\n' || b[metaSize-1] != '\n' {
		return holder{}, false, legacy, nil
	}
	scheme := strings.TrimRight(string(b[:5]), " ")
	pid, err := strconv.Atoi(string(b[6:16]))
	if err != nil || pid <= 0 {
		return holder{}, false, legacy, nil
	}
	ap, err := netip.ParseAddrPort(strings.TrimRight(string(b[metaAddrAt:metaSize-1]), " "))
	if err != nil || ap.Port() == 0 || ap.Addr().Zone() != "" {
		return holder{}, false, legacy, nil
	}
	bd := bind{ap}
	if bd.scheme() != scheme {
		return holder{}, false, legacy, nil
	}
	return holder{pid: pid, bind: bd}, true, false, nil
}

// errLegacyHolder names the one recovery for a dashboard whose lock record
// this tkv cannot read.
var errLegacyHolder = errors.New("tkv lock is held by a dashboard from a different tkv version; stop it with: pkill -f 'tkv foreground'")

// awaitHolder waits until the lock file names a pid that still has it open.
// An unreadable record is retried: the lock file outlives its dashboard, so a
// new holder may not have written its record yet.
func awaitHolder(path string, wait time.Duration) (holder, error) {
	deadline := time.Now().Add(wait)
	var why string
	for {
		h, ok, legacy, err := readMeta(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return holder{}, errNotRunning
			}
			return holder{}, err
		}
		if ok {
			holds, err := pidHoldsLock(h.pid, path)
			if err != nil {
				return holder{}, err
			}
			if holds {
				return h, nil
			}
			why = fmt.Sprintf("pid %d does not hold the lock", h.pid)
		} else {
			why = "no server record"
		}
		held, err := lockHeld(path)
		if err != nil {
			return holder{}, err
		}
		if !held {
			return holder{}, errNotRunning
		}
		if time.Now().After(deadline) {
			if !ok && legacy {
				return holder{}, errLegacyHolder
			}
			return holder{}, fmt.Errorf("tkv lock is held but the server is not identifiable: %s", why)
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
