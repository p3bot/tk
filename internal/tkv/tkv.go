// Package tkv is the human-facing local web dashboard for browsing tickets
// and scopes. Humans can mark, claim, and create through the same write engine as tk,
// and set or clear the machine-local tag lens from chrome. Metadata comes from
// the machine-wide index; bodies from ticket files. Agents do not use it.
package tkv

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"

	"github.com/p3bot/tk/internal/pathutil"
	"github.com/p3bot/tk/internal/registry"
	"github.com/p3bot/tk/internal/resolve"
	"github.com/p3bot/tk/internal/xdg"
)

// DefaultPort is the localhost listen port when --port is omitted.
const DefaultPort = 8736

const usageText = `tkv is a local web dashboard for tk tickets.

Humans can mark, claim, create, and set or clear the tag lens. Agents keep using
tk. It listens on 127.0.0.1 (never 0.0.0.0). The default port is 8736.

Usage: tkv [--port N] [--no-open] [--scope NAME]

  --port N     listen port (default 8736)
  --no-open    print the landing URL and do not open a browser
  --scope NAME open this scope's board (same as tk: wins over TK_SCOPE and cwd)

Launch inside a registered code-root to open that scope. Otherwise the
machine overview is opened. Agents keep using tk; tkv is for humans.
`

// App is the process-wide viewer configuration. Tests inject dirs, cwd, and I/O.
type App struct {
	Ctx         *cue.Context
	ConfigDir   string
	StateDir    string
	ScopeFlag   string
	EnvScope    string
	Cwd         string
	Port        int
	NoOpen      bool
	Stdout      io.Writer
	Stderr      io.Writer
	OpenBrowser func(string) error
	LookupEnv   func(string) string
	// ServeCtx, when set, replaces the process signal context (tests).
	ServeCtx context.Context
	// afterIndexUnlock is copied onto Server (tests).
	afterIndexUnlock func()
}

// NewApp builds a production App (XDG dirs and cwd resolved at Run).
func NewApp() *App {
	return &App{
		Ctx:         cuecontext.New(),
		Port:        DefaultPort,
		Stdout:      os.Stdout,
		Stderr:      os.Stderr,
		OpenBrowser: openBrowser,
		LookupEnv:   os.Getenv,
	}
}

// Run parses flags, opens the index, listens on 127.0.0.1, starts serving,
// then prints the landing URL. --no-open skips the browser.
func (a *App) Run(args []string) error {
	if !supportedOS() {
		return fmt.Errorf("tkv supports macOS and Linux only; this operating system is unsupported")
	}
	if err := a.parseFlags(args); err != nil {
		return err
	}

	srv, err := a.NewServer()
	if err != nil {
		return err
	}
	defer func() { _ = srv.Close() }()

	ln, err := listen(a.Port)
	if err != nil {
		return err
	}
	defer func() { _ = ln.Close() }()

	url, err := a.LandingURL(a.Port)
	if err != nil {
		return err
	}

	ctx, stop := a.serveContext()
	defer stop()

	httpSrv := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}

	serveErr := make(chan error, 1)
	go func() {
		err := httpSrv.Serve(ln)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	ready := fmt.Sprintf("http://127.0.0.1:%d/static/style.css", a.Port)
	if err := waitServing(ctx, ready); err != nil {
		_ = httpSrv.Close()
		if se := <-serveErr; se != nil {
			return se
		}
		return err
	}

	fmt.Fprintln(a.stdout(), url)
	if err := a.maybeOpen(url); err != nil {
		fmt.Fprintln(a.stderr(), err)
	}

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
		// Shutdown waits for handlers. Serve returning only means the listener
		// closed; claim/mark git work must drain (see beginWrite).
		_ = httpSrv.Shutdown(context.Background())
		return <-serveErr
	}
}

func (a *App) serveContext() (context.Context, context.CancelFunc) {
	if a.ServeCtx != nil {
		return context.WithCancel(a.ServeCtx)
	}
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func waitServing(ctx context.Context, url string) error {
	client := &http.Client{Timeout: 200 * time.Millisecond}
	deadline := time.Now().Add(2 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			if last != nil {
				return fmt.Errorf("server did not become ready: %w", last)
			}
			return ctx.Err()
		default:
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			last = fmt.Errorf("ready check: %s", resp.Status)
		} else {
			last = err
		}
		time.Sleep(10 * time.Millisecond)
	}
	if last != nil {
		return fmt.Errorf("server did not become ready: %w", last)
	}
	return errors.New("server did not become ready")
}

func (a *App) parseFlags(args []string) error {
	fs := flag.NewFlagSet("tkv", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	port := fs.Int("port", DefaultPort, "listen port (always bound on 127.0.0.1)")
	noOpen := fs.Bool("no-open", false, "print the landing URL and do not open a browser")
	scope := fs.String("scope", "", "scope to open (same as tk: wins over TK_SCOPE and cwd)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(a.stderr(), usageText)
			return flag.ErrHelp
		}
		return fmt.Errorf("%w\n\n%s", err, usageText)
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("tkv takes no positional arguments\n\n%s", usageText)
	}
	if *port < 1 || *port > 65535 {
		return fmt.Errorf("invalid --port %d: must be 1–65535", *port)
	}
	a.Port = *port
	a.NoOpen = *noOpen
	a.ScopeFlag = *scope
	return nil
}

func (a *App) maybeOpen(url string) error {
	if a.NoOpen {
		return nil
	}
	open := a.OpenBrowser
	if open == nil {
		open = openBrowser
	}
	return open(url)
}

func listen(port int) (net.Listener, error) {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w (use --port)", addr, err)
	}
	return ln, nil
}

// LandingURL is the browser target after listen: ambient scope board, else /.
func (a *App) LandingURL(port int) (string, error) {
	path, err := a.LandingPath()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("http://127.0.0.1:%d%s", port, path), nil
}

// LandingPath resolves the ambient scope the same way tk does.
// Name-drift is fail-closed. No ambient scope yields "/".
func (a *App) LandingPath() (string, error) {
	ctx := a.cue()
	configDir, err := a.configDir()
	if err != nil {
		return "", err
	}
	reg, err := registry.NewStore(ctx, configDir).Load()
	if err != nil {
		return "", err
	}
	cwd, err := a.cwd()
	if err != nil {
		return "", err
	}
	resolved, err := resolve.Resolve(ctx, reg, resolve.Options{
		ScopeFlag: a.ScopeFlag,
		EnvScope:  a.envScope(),
		Cwd:       cwd,
	})
	if errors.Is(err, resolve.ErrNoScope) {
		return "/", nil
	}
	if err != nil {
		return "", err
	}
	return "/scope/" + resolved.Name, nil
}

func (a *App) cue() *cue.Context {
	if a.Ctx == nil {
		a.Ctx = cuecontext.New()
	}
	return a.Ctx
}

func (a *App) configDir() (string, error) {
	if a.ConfigDir != "" {
		return a.ConfigDir, nil
	}
	return xdg.ConfigDir()
}

func (a *App) stateDir() (string, error) {
	if a.StateDir != "" {
		return a.StateDir, nil
	}
	return xdg.StateDir()
}

func (a *App) cwd() (string, error) {
	if a.Cwd != "" {
		return pathutil.Canonical(a.Cwd), nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve working directory: %w", err)
	}
	return pathutil.Canonical(wd), nil
}

func (a *App) envScope() string {
	if a.EnvScope != "" {
		return a.EnvScope
	}
	if a.LookupEnv != nil {
		return a.LookupEnv("TK_SCOPE")
	}
	return os.Getenv("TK_SCOPE")
}

func (a *App) stdout() io.Writer {
	if a.Stdout != nil {
		return a.Stdout
	}
	return os.Stdout
}

func (a *App) stderr() io.Writer {
	if a.Stderr != nil {
		return a.Stderr
	}
	return os.Stderr
}
