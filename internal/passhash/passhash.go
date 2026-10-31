// Package passhash hashes and verifies login passwords with PBKDF2-SHA256.
// The stored form is self-describing, pbkdf2-sha256$<iter>$<salt>$<key> in
// unpadded standard base64, so the iteration count can rise later without
// invalidating existing hashes.
package passhash

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Iterations is the PBKDF2 work factor for new hashes (OWASP, PBKDF2-HMAC-SHA256).
const Iterations = 600_000

const (
	scheme  = "pbkdf2-sha256"
	saltLen = 16
	keyLen  = 32
	// maxIterations stops a hand-edited hash from turning one login into a
	// CPU sink.
	maxIterations = 10_000_000
)

var b64 = base64.RawStdEncoding

// Dummy is a well-formed hash at the production work factor that matches no
// password anyone can type. Verifying an unknown username against it costs
// the same as a real check, so timing does not reveal which names exist.
const Dummy = "pbkdf2-sha256$600000$dGt2LWR1bW15LXNhbHQhIQ$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

// Hash derives a new stored hash for password with a fresh random salt.
func Hash(password string) (string, error) {
	return hash(rand.Reader, Iterations, password)
}

func hash(random io.Reader, iter int, password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := io.ReadFull(random, salt); err != nil {
		return "", fmt.Errorf("password salt: %w", err)
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, iter, keyLen)
	if err != nil {
		return "", fmt.Errorf("password hash: %w", err)
	}
	return scheme + "$" + strconv.Itoa(iter) + "$" + b64.EncodeToString(salt) + "$" + b64.EncodeToString(key), nil
}

// ErrMalformed means a stored hash is not in the pbkdf2-sha256 form.
var ErrMalformed = errors.New("malformed password hash")

// Verify reports whether password matches stored, reading the work factor
// from stored. The key comparison is constant time.
func Verify(stored, password string) (bool, error) {
	parts := strings.Split(stored, "$")
	if len(parts) != 4 || parts[0] != scheme {
		return false, ErrMalformed
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter < 1 || iter > maxIterations {
		return false, ErrMalformed
	}
	salt, err := b64.DecodeString(parts[2])
	if err != nil || len(salt) == 0 {
		return false, ErrMalformed
	}
	want, err := b64.DecodeString(parts[3])
	if err != nil || len(want) == 0 {
		return false, ErrMalformed
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iter, len(want))
	if err != nil {
		return false, fmt.Errorf("password hash: %w", err)
	}
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
