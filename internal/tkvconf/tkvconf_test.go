package tkvconf

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"cuelang.org/go/cue/cuecontext"
)

func TestLoadMissingIsEmpty(t *testing.T) {
	c, err := Load(cuecontext.New(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c, Config{}) {
		t.Fatalf("config = %+v", c)
	}
}

func TestUpdateRoundTripsAtMode0600(t *testing.T) {
	ctx := cuecontext.New()
	dir := t.TempDir()
	err := Update(ctx, dir, func(c *Config) error {
		c.Listen = Listen{Host: "0.0.0.0", Port: 9000}
		c.Hosts = []string{"nas.lan"}
		c.Users = map[string]string{"grant": "pbkdf2-sha256$1$AAAA$AAAA", "a.b-c_d": "x"}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", fi.Mode())
	}
	c, err := Load(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	want := Config{
		Listen: Listen{Host: "0.0.0.0", Port: 9000},
		Hosts:  []string{"nas.lan"},
		Users:  map[string]string{"grant": "pbkdf2-sha256$1$AAAA$AAAA", "a.b-c_d": "x"},
	}
	if !reflect.DeepEqual(c, want) {
		t.Fatalf("config = %+v", c)
	}
	if got := c.UserNames(); !reflect.DeepEqual(got, []string{"a.b-c_d", "grant"}) {
		t.Fatalf("names = %v", got)
	}
}

func TestUpdateReplacesInode(t *testing.T) {
	ctx := cuecontext.New()
	dir := t.TempDir()
	set := func(hash string) os.FileInfo {
		t.Helper()
		if err := Update(ctx, dir, func(c *Config) error {
			c.Users = map[string]string{"grant": hash}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		fi, err := os.Stat(Path(dir))
		if err != nil {
			t.Fatal(err)
		}
		return fi
	}
	a := set("aaaa")
	b := set("bbbb")
	if a.Size() != b.Size() {
		t.Fatalf("sizes %d %d", a.Size(), b.Size())
	}
	if os.SameFile(a, b) {
		t.Fatal("a same-size rewrite must land on a new inode")
	}
}

func TestUpdateErrorWritesNothing(t *testing.T) {
	dir := t.TempDir()
	err := Update(cuecontext.New(), dir, func(*Config) error { return os.ErrInvalid })
	if err == nil {
		t.Fatal("expected error")
	}
	if _, err := os.Stat(Path(dir)); !os.IsNotExist(err) {
		t.Fatalf("file written: %v", err)
	}
}

func TestEmptyConfigWrites(t *testing.T) {
	ctx := cuecontext.New()
	dir := t.TempDir()
	if err := Update(ctx, dir, func(*Config) error { return nil }); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(b)) != "tkv: {}" {
		t.Fatalf("file = %q", b)
	}
}

func TestParseRefuses(t *testing.T) {
	ctx := cuecontext.New()
	for name, src := range map[string]string{
		"syntax":       "tkv: {",
		"port range":   "tkv: listen: port: 70000",
		"port type":    `tkv: listen: port: "80"`,
		"user name":    `tkv: users: "Grant": hash: "x"`,
		"missing hash": `tkv: users: grant: {}`,
		"unknown key":  `tkv: listn: host: "::"`,
		"bad host":     `tkv: hosts: ["no spaces"]`,
	} {
		if _, err := Parse(ctx, "tkv.cue", []byte(src)); err == nil {
			t.Errorf("%s: %q parsed", name, src)
		}
	}
}

func TestParseHandEdit(t *testing.T) {
	src := "// mine\ntkv: {\n\thosts: [\"NAS.lan\", \"192.168.1.5\"]\n}\n"
	c, err := Parse(cuecontext.New(), "tkv.cue", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.Hosts, []string{"nas.lan", "192.168.1.5"}) {
		t.Fatalf("hosts = %v", c.Hosts)
	}
}

func TestChecks(t *testing.T) {
	for _, n := range []string{"grant", "a", "a.b", "x_y-z", strings.Repeat("a", 32)} {
		if err := CheckUserName(n); err != nil {
			t.Errorf("CheckUserName(%q) = %v", n, err)
		}
	}
	for _, n := range []string{"", "Grant", "a b", strings.Repeat("a", 33), "ü"} {
		if CheckUserName(n) == nil {
			t.Errorf("CheckUserName(%q) accepted", n)
		}
	}
	if CheckPassword("1234567") == nil || CheckPassword("12345678") != nil {
		t.Error("password length floor")
	}
	if CheckPassword("äöüäöüäö") != nil {
		t.Error("floor counts characters, not bytes")
	}
	if CheckPassword(strings.Repeat("a", 1025)) == nil || CheckPassword(strings.Repeat("a", 1024)) != nil {
		t.Error("password byte cap")
	}
	for in, want := range map[string]string{"NAS.lan": "nas.lan", "gdc": "gdc", "::1": "::1", "2001:DB8::1": "2001:db8::1", "192.168.001.5": ""} {
		got, err := CheckHostName(in)
		if want == "" {
			if err == nil {
				t.Errorf("CheckHostName(%q) = %q", in, got)
			}
			continue
		}
		if err != nil || got != want {
			t.Errorf("CheckHostName(%q) = %q %v", in, got, err)
		}
	}
	for _, in := range []string{"", "-a", "a-", "a..b", "fe80::1%eth0", "a_b", "::ffff:10.0.0.1"} {
		if _, err := CheckHostName(in); err == nil {
			t.Errorf("CheckHostName(%q) accepted", in)
		}
	}
	if !LooseMode(0o640) || !LooseMode(0o604) || LooseMode(0o600) {
		t.Error("LooseMode")
	}
}
