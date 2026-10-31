// Package tkvcert owns tkv's network-mode key material in the state
// directory: a self-signed ECDSA P-256 leaf certificate with its key, and the
// cookie signing key. The certificate encrypts only; trust comes from the
// login. It persists across restarts, because a browser's accepted-warning
// exception is tied to that exact certificate, and is regenerated only when
// it is unreadable, near expiry, or names a different set of hosts.
package tkvcert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/p3bot/tk/internal/atomicfile"
)

const (
	dirName        = "tkv"
	certFile       = "cert.pem"
	keyFile        = "key.pem"
	sessionKeyFile = "session.key"
	sessionKeyLen  = 32

	// Validity is the longest lifetime Apple platforms accept for any TLS
	// server certificate, self-signed included.
	Validity = 825 * 24 * time.Hour
	// RenewMargin is how close to expiry a certificate is replaced.
	RenewMargin = 30 * 24 * time.Hour
	// backdate absorbs client clock skew without exceeding Validity in total.
	backdate = time.Hour
	// retryAfter spaces in-place renewal attempts after one fails.
	retryAfter = time.Hour
)

// Dir is the key-material directory inside the tk state directory.
func Dir(stateDir string) string {
	return filepath.Join(stateDir, dirName)
}

// CertPath is the certificate file in dir.
func CertPath(dir string) string {
	return filepath.Join(dir, certFile)
}

func ensureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("chmod %s: %w", dir, err)
	}
	return nil
}

// issued is one certificate as served. renewAt is when a handshake must
// replace it.
type issued struct {
	cert    *tls.Certificate
	renewAt time.Time
}

// Manager serves the certificate to the TLS listener and renews it in place.
type Manager struct {
	dir   string
	names []string
	now   func() time.Time
	// OnRenew, when set, is told about each in-place renewal attempt: the new
	// fingerprint, or the error that left the old certificate in service.
	OnRenew func(fingerprint string, err error)

	mu  sync.Mutex
	cur atomic.Pointer[issued]
}

// Open loads the certificate in dir, regenerating it when the files are
// missing or unparseable, it expires within RenewMargin, or its names differ
// from names. names are host names and IP literals in canonical form.
// regenerated reports whether new files were written.
func Open(dir string, names []string, now func() time.Time) (m *Manager, regenerated bool, err error) {
	if err := ensureDir(dir); err != nil {
		return nil, false, err
	}
	m = &Manager{dir: dir, names: sortedUnique(names), now: now}
	cert, err := loadPair(dir)
	if err == nil && m.usable(cert.Leaf) {
		for _, name := range []string{certFile, keyFile} {
			if err := tighten(filepath.Join(dir, name)); err != nil {
				return nil, false, err
			}
		}
		m.cur.Store(m.issue(cert))
		return m, false, nil
	}
	cert, err = m.generate()
	if err != nil {
		return nil, false, err
	}
	m.cur.Store(m.issue(cert))
	return m, true, nil
}

func (m *Manager) usable(leaf *x509.Certificate) bool {
	if leaf == nil || !m.now().Before(leaf.NotAfter.Add(-RenewMargin)) {
		return false
	}
	return slices.Equal(certNames(leaf), m.names)
}

func (m *Manager) issue(cert *tls.Certificate) *issued {
	return &issued{cert: cert, renewAt: cert.Leaf.NotAfter.Add(-RenewMargin)}
}

// GetCertificate is the tls.Config hook. Each handshake compares the cached
// renewal time; the first handshake inside the margin regenerates under the
// mutex, writes the files, and serves the new certificate from then on.
func (m *Manager) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	cur := m.cur.Load()
	if m.now().Before(cur.renewAt) {
		return cur.cert, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cur = m.cur.Load()
	if m.now().Before(cur.renewAt) {
		return cur.cert, nil
	}
	cert, err := m.generate()
	if err != nil {
		// The old certificate is still valid for most of the margin.
		m.cur.Store(&issued{cert: cur.cert, renewAt: m.now().Add(retryAfter)})
		if m.OnRenew != nil {
			m.OnRenew("", err)
		}
		return cur.cert, nil
	}
	m.cur.Store(m.issue(cert))
	if m.OnRenew != nil {
		m.OnRenew(Fingerprint(cert.Leaf.Raw), nil)
	}
	return cert, nil
}

// Leaf is the certificate currently served.
func (m *Manager) Leaf() *x509.Certificate {
	return m.cur.Load().cert.Leaf
}

// TLSConfig is the listener configuration: TLS 1.2 or later, Go's default
// cipher suites, the certificate from this manager.
func (m *Manager) TLSConfig() *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: m.GetCertificate}
}

func (m *Manager) generate() (*tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate certificate key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("certificate serial: %w", err)
	}
	notBefore := m.now().Add(-backdate).UTC().Truncate(time.Second)
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "tkv"},
		NotBefore:             notBefore,
		NotAfter:              notBefore.Add(Validity),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  false,
		SignatureAlgorithm:    x509.ECDSAWithSHA256,
	}
	for _, name := range m.names {
		if addr, err := netip.ParseAddr(name); err == nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, net.IP(addr.AsSlice()))
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, name)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("create certificate: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("encode certificate key: %w", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	if err := ensureDir(m.dir); err != nil {
		return nil, err
	}
	// A crash between the two writes leaves a mismatched pair, which the next
	// start reads as unparseable and regenerates.
	if err := atomicfile.Write(filepath.Join(m.dir, keyFile), keyPEM, 0o600); err != nil {
		return nil, err
	}
	if err := atomicfile.Write(filepath.Join(m.dir, certFile), certPEM, 0o600); err != nil {
		return nil, err
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("load generated certificate: %w", err)
	}
	return &cert, nil
}

func loadPair(dir string) (*tls.Certificate, error) {
	cert, err := tls.LoadX509KeyPair(filepath.Join(dir, certFile), filepath.Join(dir, keyFile))
	if err != nil {
		return nil, err
	}
	if cert.Leaf == nil {
		leaf, err := x509.ParseCertificate(cert.Certificate[0])
		if err != nil {
			return nil, err
		}
		cert.Leaf = leaf
	}
	return &cert, nil
}

// certNames is the leaf's subject alternative names in the canonical sorted
// form the stable name set uses.
func certNames(leaf *x509.Certificate) []string {
	names := make([]string, 0, len(leaf.DNSNames)+len(leaf.IPAddresses))
	for _, n := range leaf.DNSNames {
		names = append(names, strings.ToLower(n))
	}
	for _, ip := range leaf.IPAddresses {
		if addr, ok := netip.AddrFromSlice(ip); ok {
			names = append(names, addr.Unmap().String())
		}
	}
	return sortedUnique(names)
}

func sortedUnique(names []string) []string {
	out := slices.Clone(names)
	slices.Sort(out)
	return slices.Compact(out)
}

// Fingerprint is the SHA-256 of a DER certificate as colon-separated
// upper-case hex, the form browsers display.
func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	var b strings.Builder
	for i, c := range sum {
		if i > 0 {
			b.WriteByte(':')
		}
		fmt.Fprintf(&b, "%02X", c)
	}
	return b.String()
}

// ReadLeaf parses the certificate file in dir. It reads the file, so it
// reflects an in-place renewal by a running dashboard.
func ReadLeaf(dir string) (*x509.Certificate, error) {
	b, err := os.ReadFile(CertPath(dir))
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(b)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("%s: no certificate", CertPath(dir))
	}
	return x509.ParseCertificate(block.Bytes)
}

// Pool is a root pool holding exactly leaf, for a client that trusts this
// dashboard and nothing else.
func Pool(leaf *x509.Certificate) *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return pool
}

// SessionKey loads the cookie signing key in dir, creating it when missing.
// A file of the wrong length is replaced, which signs every browser out.
func SessionKey(dir string) ([]byte, error) {
	if err := ensureDir(dir); err != nil {
		return nil, err
	}
	p := filepath.Join(dir, sessionKeyFile)
	b, err := os.ReadFile(p)
	if err == nil && len(b) == sessionKeyLen {
		if err := tighten(p); err != nil {
			return nil, err
		}
		return b, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", p, err)
	}
	key := make([]byte, sessionKeyLen)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate session key: %w", err)
	}
	if err := atomicfile.Write(p, key, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

func tighten(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if fi.Mode().Perm()&0o077 == 0 {
		return nil
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	return nil
}
