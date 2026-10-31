package tkvcert

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"
)

var names = []string{"localhost", "127.0.0.1", "::1", "gdc", "192.168.1.5"}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newClock() *clock { return &clock{t: time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)} }

func mode(t *testing.T, p string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

func TestOpenGeneratesProfile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tkv")
	c := newClock()
	m, regen, err := Open(dir, names, c.now)
	if err != nil {
		t.Fatal(err)
	}
	if !regen {
		t.Fatal("first open must generate")
	}
	leaf := m.Leaf()
	if got := leaf.NotAfter.Sub(leaf.NotBefore); got != Validity {
		t.Fatalf("validity = %v", got)
	}
	if leaf.IsCA || !leaf.BasicConstraintsValid {
		t.Fatal("leaf must carry CA:FALSE")
	}
	if leaf.KeyUsage != x509.KeyUsageDigitalSignature {
		t.Fatalf("key usage = %v", leaf.KeyUsage)
	}
	if !slices.Equal(leaf.ExtKeyUsage, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}) {
		t.Fatalf("ext key usage = %v", leaf.ExtKeyUsage)
	}
	if leaf.SignatureAlgorithm != x509.ECDSAWithSHA256 || leaf.PublicKeyAlgorithm != x509.ECDSA {
		t.Fatalf("alg = %v %v", leaf.SignatureAlgorithm, leaf.PublicKeyAlgorithm)
	}
	if leaf.SerialNumber.BitLen() > 128 || leaf.SerialNumber.Sign() <= 0 {
		t.Fatalf("serial = %v", leaf.SerialNumber)
	}
	if !slices.Equal(leaf.DNSNames, []string{"gdc", "localhost"}) || len(leaf.IPAddresses) != 3 {
		t.Fatalf("names = %v %v", leaf.DNSNames, leaf.IPAddresses)
	}
	if mode(t, dir) != 0o700 || mode(t, filepath.Join(dir, certFile)) != 0o600 || mode(t, filepath.Join(dir, keyFile)) != 0o600 {
		t.Fatal("modes must be 0700 dir and 0600 files")
	}
	for _, host := range []string{"localhost", "127.0.0.1", "::1", "gdc", "192.168.1.5"} {
		if err := leaf.VerifyHostname(host); err != nil {
			t.Errorf("verify %s: %v", host, err)
		}
	}
	if err := leaf.VerifyHostname("nas.lan"); err == nil {
		t.Error("nas.lan is not a name")
	}
}

func TestOpenPersistsAndRegenerates(t *testing.T) {
	dir := t.TempDir()
	c := newClock()
	m1, _, err := Open(dir, names, c.now)
	if err != nil {
		t.Fatal(err)
	}
	fp1 := Fingerprint(m1.Leaf().Raw)

	shuffled := []string{"::1", "gdc", "192.168.1.5", "localhost", "127.0.0.1"}
	m2, regen, err := Open(dir, shuffled, c.now)
	if err != nil || regen || Fingerprint(m2.Leaf().Raw) != fp1 {
		t.Fatalf("unchanged names must persist: regen=%v err=%v", regen, err)
	}

	m3, regen, err := Open(dir, append(slices.Clone(names), "nas.lan"), c.now)
	if err != nil || !regen || Fingerprint(m3.Leaf().Raw) == fp1 {
		t.Fatalf("a new name must regenerate: regen=%v err=%v", regen, err)
	}
	fp3 := Fingerprint(m3.Leaf().Raw)

	c.add(Validity - RenewMargin - 2*time.Hour)
	_, regen, err = Open(dir, append(slices.Clone(names), "nas.lan"), c.now)
	if err != nil || regen {
		t.Fatalf("outside the margin must persist: regen=%v err=%v", regen, err)
	}
	c.add(2 * time.Hour)
	m4, regen, err := Open(dir, append(slices.Clone(names), "nas.lan"), c.now)
	if err != nil || !regen || Fingerprint(m4.Leaf().Raw) == fp3 {
		t.Fatalf("inside the margin must regenerate: regen=%v err=%v", regen, err)
	}

	if err := os.WriteFile(filepath.Join(dir, certFile), []byte("junk"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, regen, err = Open(dir, names, c.now)
	if err != nil || !regen {
		t.Fatalf("an unparseable file must regenerate: regen=%v err=%v", regen, err)
	}
	disk, err := ReadLeaf(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := disk.VerifyHostname("gdc"); err != nil {
		t.Fatal(err)
	}
}

func TestOpenTightensLooseFiles(t *testing.T) {
	dir := t.TempDir()
	c := newClock()
	if _, _, err := Open(dir, names, c.now); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, keyFile), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Open(dir, names, c.now); err != nil {
		t.Fatal(err)
	}
	if mode(t, filepath.Join(dir, keyFile)) != 0o600 {
		t.Fatal("key must be tightened")
	}
}

func TestGetCertificateRenewsInPlace(t *testing.T) {
	dir := t.TempDir()
	c := newClock()
	m, _, err := Open(dir, names, c.now)
	if err != nil {
		t.Fatal(err)
	}
	var renewed []string
	m.OnRenew = func(fp string, err error) {
		if err != nil {
			t.Error(err)
		}
		renewed = append(renewed, fp)
	}
	first, err := m.GetCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := m.GetCertificate(nil)
	if again != first || len(renewed) != 0 {
		t.Fatal("outside the margin a handshake serves the cached certificate")
	}
	c.add(Validity - RenewMargin)
	var wg sync.WaitGroup
	got := make([]*tls.Certificate, 8)
	for i := range got {
		wg.Add(1)
		go func() { defer wg.Done(); got[i], _ = m.GetCertificate(nil) }()
	}
	wg.Wait()
	if len(renewed) != 1 {
		t.Fatalf("renewed %d times", len(renewed))
	}
	for _, g := range got {
		if g == first || g != got[0] {
			t.Fatal("every handshake inside the margin serves the one new certificate")
		}
	}
	disk, err := ReadLeaf(dir)
	if err != nil {
		t.Fatal(err)
	}
	if Fingerprint(disk.Raw) != renewed[0] || !bytes.Equal(disk.Raw, got[0].Leaf.Raw) {
		t.Fatal("the renewal must be written to cert.pem")
	}
	if !slices.Equal(certNames(disk), sortedUnique(names)) {
		t.Fatalf("renewal keeps names: %v", certNames(disk))
	}
}

func TestTLSClientTrustsPoolByIP(t *testing.T) {
	c := newClock()
	c.t = time.Now()
	m, _, err := Open(t.TempDir(), names, c.now)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp4", "127.0.0.1:0", m.TLSConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.(*tls.Conn).Handshake()
			_ = conn.Close()
		}
	}()
	conn, err := tls.Dial("tcp4", ln.Addr().String(), &tls.Config{RootCAs: Pool(m.Leaf()), ServerName: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if _, err := tls.Dial("tcp4", ln.Addr().String(), &tls.Config{RootCAs: x509.NewCertPool(), ServerName: "127.0.0.1"}); err == nil {
		t.Fatal("an empty pool must not verify")
	}
}

func TestFingerprintForm(t *testing.T) {
	fp := Fingerprint([]byte("x"))
	if len(fp) != 32*3-1 || fp[2] != ':' || fp != "2D:71:16:42:B7:26:B0:44:01:62:7C:A9:FB:AC:32:F5:C8:53:0F:B1:90:3C:C4:DB:02:25:87:17:92:1A:48:81" {
		t.Fatalf("fingerprint = %q", fp)
	}
}

func TestSessionKey(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tkv")
	k1, err := SessionKey(dir)
	if err != nil || len(k1) != sessionKeyLen {
		t.Fatalf("key %d %v", len(k1), err)
	}
	if mode(t, filepath.Join(dir, sessionKeyFile)) != 0o600 || mode(t, dir) != 0o700 {
		t.Fatal("modes")
	}
	k2, err := SessionKey(dir)
	if err != nil || !bytes.Equal(k1, k2) {
		t.Fatal("key must persist")
	}
	if err := os.WriteFile(filepath.Join(dir, sessionKeyFile), []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	k3, err := SessionKey(dir)
	if err != nil || len(k3) != sessionKeyLen || bytes.Equal(k3, k1) {
		t.Fatal("a bad key file must be replaced")
	}
}
