// Package id implements the ticket-id wire contract: predicates, argv-form
// classification (full / short / reserved me), crypto/rand short-id mint, and
// deterministic collision-repair extension. No I/O; the mint takes its
// randomness source as an argument for testability.
//
// A ticket id is <scope>-<short-id>. Create always mints length 4; collision
// repair may lengthen append-only up to ShortIDMax.
package id

import (
	"fmt"
	"io"
	"strings"
)

const (
	// ShortIDMin is the shortest legal short-id (create always mints this).
	ShortIDMin = 4
	// ShortIDMax is the longest legal short-id; collision repair never exceeds it.
	ShortIDMax = 8
	// ScopeNameMax is the longest legal scope name.
	ScopeNameMax = 12

	// LetterAlphabet is the 23-letter typeable subset (drops i, l, o).
	// The first character of every short-id is drawn from this set.
	LetterAlphabet = "abcdefghjkmnpqrstuvwxyz"
	// DigitAlphabet is the 8-digit typeable subset (drops 0, 1).
	DigitAlphabet = "23456789"
	// ShortIDAlphabet is the fixed 31-character alphabet; order is load-bearing
	// so two machines repairing the same collision enumerate identically.
	ShortIDAlphabet = LetterAlphabet + DigitAlphabet

	// ReservedMe is the well-formed resolver token; expansion is the caller's job.
	ReservedMe = "me"
)

// Form is the argv shape of a ticket-id token. Malformed tokens still carry a
// form (hyphen → full, otherwise short) so usage wording can name the class.
type Form int

const (
	// FormFull is <scope>-<short> (one hyphen).
	FormFull Form = iota
	// FormShort is a bare short-id.
	FormShort
	// FormMe is ReservedMe.
	FormMe
)

// ParseArg classifies a ticket-id token. ok is false when the token is the
// right shape but fails the grammar. ReservedMe is always ok; lookup of the
// stored id happens at the call site.
func ParseArg(tok string) (Form, bool) {
	if tok == ReservedMe {
		return FormMe, true
	}
	if strings.ContainsRune(tok, '-') {
		return FormFull, IsFullTicketID(tok)
	}
	return FormShort, IsShortID(tok)
}

// IsScopeName reports whether s is a legal scope name: ^[a-z0-9]{1,12}$.
// Ambiguous characters that short-ids drop (i/l/o/0/1) are permitted here.
func IsScopeName(s string) bool {
	if len(s) < 1 || len(s) > ScopeNameMax {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isLowerAlnum(s[i]) {
			return false
		}
	}
	return true
}

// IsShortID reports whether s is a legal short-id: length ShortIDMin–ShortIDMax,
// every character in ShortIDAlphabet, first character a letter (never a digit).
func IsShortID(s string) bool {
	if len(s) < ShortIDMin || len(s) > ShortIDMax {
		return false
	}
	if !isShortIDLetter(s[0]) {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isShortIDChar(s[i]) {
			return false
		}
	}
	return true
}

// IsFullTicketID reports whether s is a legal <scope>-<short-id> with exactly one '-'.
func IsFullTicketID(s string) bool {
	i := strings.IndexByte(s, '-')
	if i < 0 {
		return false
	}
	scope, short := s[:i], s[i+1:]
	if strings.IndexByte(short, '-') >= 0 {
		return false
	}
	return IsScopeName(scope) && IsShortID(short)
}

// ScopeOfFullID returns the text before the first '-'. No hyphen returns s
// unchanged. This does not validate the scope or short-id; use IsFullTicketID.
func ScopeOfFullID(s string) string {
	i := strings.IndexByte(s, '-')
	if i < 0 {
		return s
	}
	return s[:i]
}

// Mint draws a fresh length-4 short-id from r.
// First char is a uniform letter; positions 2–4 are a 50/50 letter/digit class flip.
// Collision checking lives at the call site under the scope flock, not here.
func Mint(r io.Reader) (string, error) {
	br := byteReader{r: r}
	out := make([]byte, ShortIDMin)

	first, err := br.uniform(len(LetterAlphabet))
	if err != nil {
		return "", fmt.Errorf("mint short-id: %w", err)
	}
	out[0] = LetterAlphabet[first]

	for i := 1; i < ShortIDMin; i++ {
		coin, err := br.uniform(2)
		if err != nil {
			return "", fmt.Errorf("mint short-id: %w", err)
		}
		if coin == 0 {
			n, err := br.uniform(len(LetterAlphabet))
			if err != nil {
				return "", fmt.Errorf("mint short-id: %w", err)
			}
			out[i] = LetterAlphabet[n]
		} else {
			n, err := br.uniform(len(DigitAlphabet))
			if err != nil {
				return "", fmt.Errorf("mint short-id: %w", err)
			}
			out[i] = DigitAlphabet[n]
		}
	}
	return string(out), nil
}

// Extend deterministically repairs a colliding short-id by appending characters
// from ShortIDAlphabet in lex order at each length up to ShortIDMax.
// No randomness so two machines repairing the same collision mint the same id.
func Extend(prefix string, occupied map[string]struct{}) (string, error) {
	if !IsShortID(prefix) {
		return "", fmt.Errorf("extend short-id: prefix %q is not a legal short-id", prefix)
	}
	n := len(prefix)
	for target := n + 1; target <= ShortIDMax; target++ {
		odometer := make([]int, target-n)
		for {
			var b strings.Builder
			b.Grow(target)
			b.WriteString(prefix)
			for _, d := range odometer {
				b.WriteByte(ShortIDAlphabet[d])
			}
			candidate := b.String()
			if _, taken := occupied[candidate]; !taken {
				return candidate, nil
			}
			if !incOdometer(odometer) {
				break
			}
		}
	}
	return "", fmt.Errorf("extend short-id: no free id for prefix %q within length %d", prefix, ShortIDMax)
}

func incOdometer(digits []int) bool {
	base := len(ShortIDAlphabet)
	for i := len(digits) - 1; i >= 0; i-- {
		digits[i]++
		if digits[i] < base {
			return true
		}
		digits[i] = 0
	}
	return false
}

// byteReader draws uniform indices via rejection sampling so results are unbiased.
type byteReader struct {
	r io.Reader
}

func (br byteReader) uniform(n int) (int, error) {
	limit := 256 - (256 % n)
	var buf [1]byte
	for {
		if _, err := io.ReadFull(br.r, buf[:]); err != nil {
			return 0, err
		}
		if int(buf[0]) < limit {
			return int(buf[0]) % n, nil
		}
	}
}

func isLowerAlnum(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
}

func isShortIDChar(c byte) bool {
	return strings.IndexByte(ShortIDAlphabet, c) >= 0
}

func isShortIDLetter(c byte) bool {
	return strings.IndexByte(LetterAlphabet, c) >= 0
}
