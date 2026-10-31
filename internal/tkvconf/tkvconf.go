// Package tkvconf is tkv's machine-local configuration, ~/.config/tk/tkv.cue:
// the stored listen address, extra hostnames, and login users with their
// password hashes. The file is tool-owned: tkv regenerates it wholesale under
// the machine flock and installs it by atomic rename at mode 0600. Hand edits
// that still evaluate are honoured; comments do not survive a regenerate.
package tkvconf

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"cuelang.org/go/cue"

	"github.com/p3bot/tk/internal/atomicfile"
	"github.com/p3bot/tk/internal/registry"
	"github.com/p3bot/tk/internal/xdg"
)

// FileName is the config file's base name inside the tk XDG config dir.
const FileName = "tkv.cue"

// Password bounds. The byte cap bounds the hashing cost of one login.
const (
	MinPasswordChars = 8
	MaxPasswordBytes = 1024
)

const userNamePattern = `^[a-z0-9._-]{1,32}$`

var userNameRE = regexp.MustCompile(userNamePattern)

const schemaSrc = `
#File: {
	tkv?: {
		listen?: {
			host?: string
			port?: int & >=1 & <=65535
		}
		hosts?: [...string]
		users?: [=~"` + userNamePattern + `"]: {hash: string}
	}
}
`

// Config is the decoded file. The zero value is the empty configuration.
type Config struct {
	// Listen is the stored listen address; an empty Host or a zero Port is unset.
	Listen Listen
	// Hosts are extra hostnames added to the certificate and the Host allowlist.
	Hosts []string
	// Users maps a username to its stored password hash.
	Users map[string]string
}

// Listen is a stored listen address. The host is kept as typed; tkv
// validates it as a listen host when it resolves the bind.
type Listen struct {
	Host string
	Port int
}

// Path is the config file inside configDir.
func Path(configDir string) string {
	return filepath.Join(configDir, FileName)
}

// Load reads and validates the file in configDir. A missing file is the empty
// configuration; a file that does not evaluate is an error.
func Load(ctx *cue.Context, configDir string) (Config, error) {
	p := Path(configDir)
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read %s: %w", p, err)
	}
	return Parse(ctx, p, data)
}

// Parse validates data against the schema and decodes it. path names the
// file in error messages.
func Parse(ctx *cue.Context, path string, data []byte) (Config, error) {
	schema := ctx.CompileString(schemaSrc).LookupPath(cue.ParsePath("#File"))
	if err := schema.Err(); err != nil {
		return Config{}, fmt.Errorf("tkv.cue schema: %w", err)
	}
	v := ctx.CompileBytes(data, cue.Filename(path))
	if err := v.Err(); err != nil {
		return Config{}, fmt.Errorf("%s will not parse — fix or remove it: %w", path, err)
	}
	v = schema.Unify(v)
	if err := v.Validate(cue.Concrete(true)); err != nil {
		return Config{}, fmt.Errorf("%s is invalid: %w", path, err)
	}
	var raw struct {
		Tkv struct {
			Listen struct {
				Host string `json:"host"`
				Port int    `json:"port"`
			} `json:"listen"`
			Hosts []string `json:"hosts"`
			Users map[string]struct {
				Hash string `json:"hash"`
			} `json:"users"`
		} `json:"tkv"`
	}
	if err := v.Decode(&raw); err != nil {
		return Config{}, fmt.Errorf("%s is malformed: %w", path, err)
	}
	c := Config{
		Listen: Listen{Host: raw.Tkv.Listen.Host, Port: raw.Tkv.Listen.Port},
	}
	for _, h := range raw.Tkv.Hosts {
		name, err := CheckHostName(h)
		if err != nil {
			return Config{}, fmt.Errorf("%s: hosts: %w", path, err)
		}
		c.Hosts = append(c.Hosts, name)
	}
	if len(raw.Tkv.Users) > 0 {
		c.Users = make(map[string]string, len(raw.Tkv.Users))
		for name, u := range raw.Tkv.Users {
			c.Users[name] = u.Hash
		}
	}
	return c, nil
}

// Update runs fn on the current configuration under the machine flock and
// installs the result by atomic rename at mode 0600. The rename gives every
// write a new inode, which is how a running dashboard detects a password
// change that leaves the file's size unchanged. fn returning an error writes
// nothing.
func Update(ctx *cue.Context, configDir string, fn func(*Config) error) error {
	lock, err := xdg.AcquireConfigLock(configDir)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()
	c, err := Load(ctx, configDir)
	if err != nil {
		return err
	}
	if err := fn(&c); err != nil {
		return err
	}
	data, err := registry.FormatOwned(ctx, c.model())
	if err != nil {
		return fmt.Errorf("%s: %w", FileName, err)
	}
	return atomicfile.Write(Path(configDir), data, 0o600)
}

func (c Config) model() map[string]any {
	inner := map[string]any{}
	listen := map[string]any{}
	if c.Listen.Host != "" {
		listen["host"] = c.Listen.Host
	}
	if c.Listen.Port != 0 {
		listen["port"] = c.Listen.Port
	}
	if len(listen) > 0 {
		inner["listen"] = listen
	}
	if len(c.Hosts) > 0 {
		inner["hosts"] = c.Hosts
	}
	if len(c.Users) > 0 {
		users := make(map[string]any, len(c.Users))
		for name, hash := range c.Users {
			users[name] = map[string]any{"hash": hash}
		}
		inner["users"] = users
	}
	return map[string]any{"tkv": inner}
}

// UserNames is the sorted list of configured usernames.
func (c Config) UserNames() []string {
	names := make([]string, 0, len(c.Users))
	for name := range c.Users {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// LooseMode reports whether mode grants group or other any access.
func LooseMode(mode os.FileMode) bool {
	return mode.Perm()&0o077 != 0
}

// CheckUserName refuses a username outside the grammar.
func CheckUserName(name string) error {
	if !userNameRE.MatchString(name) {
		return fmt.Errorf("username %q must match %s", name, userNamePattern)
	}
	return nil
}

// CheckPassword refuses a password outside the length bounds.
func CheckPassword(pw string) error {
	if utf8.RuneCountInString(pw) < MinPasswordChars {
		return fmt.Errorf("password must be at least %d characters", MinPasswordChars)
	}
	if len(pw) > MaxPasswordBytes {
		return fmt.Errorf("password must be at most %d bytes", MaxPasswordBytes)
	}
	return nil
}

// CheckHostName validates an extra hostname and returns it lower-cased: an IP
// literal without a zone, or a DNS name of letters, digits, and hyphens whose
// last label is not all digits.
func CheckHostName(name string) (string, error) {
	if addr, err := netip.ParseAddr(name); err == nil {
		if addr.Zone() != "" {
			return "", fmt.Errorf("host %q: a zoned address cannot be a hostname", name)
		}
		if addr.Is4In6() {
			return "", fmt.Errorf("host %q: use the IPv4 form %s", name, addr.Unmap())
		}
		return addr.String(), nil
	}
	lower := strings.ToLower(name)
	if lower == "" || len(lower) > 253 {
		return "", fmt.Errorf("host %q is not a hostname", name)
	}
	labels := strings.Split(lower, ".")
	for _, label := range labels {
		if !validLabel(label) {
			return "", fmt.Errorf("host %q is not a hostname", name)
		}
	}
	// An all-digit last label is a mistyped address, never a DNS name.
	if strings.Trim(labels[len(labels)-1], "0123456789") == "" {
		return "", fmt.Errorf("host %q is neither an IP address nor a hostname", name)
	}
	return lower, nil
}

func validLabel(l string) bool {
	if l == "" || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
		return false
	}
	for i := 0; i < len(l); i++ {
		c := l[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}
