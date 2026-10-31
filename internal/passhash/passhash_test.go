package passhash

import (
	"bytes"
	"errors"
	"strconv"
	"strings"
	"testing"
)

func TestHashFormatAndVerify(t *testing.T) {
	stored, err := Hash("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(stored, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" || parts[1] != strconv.Itoa(Iterations) {
		t.Fatalf("stored = %q", stored)
	}
	if salt, _ := b64.DecodeString(parts[2]); len(salt) != saltLen {
		t.Fatalf("salt %q is %d bytes", parts[2], len(salt))
	}
	if key, _ := b64.DecodeString(parts[3]); len(key) != keyLen {
		t.Fatalf("key %q is %d bytes", parts[3], len(key))
	}
	ok, err := Verify(stored, "correct horse")
	if err != nil || !ok {
		t.Fatalf("verify = %v %v", ok, err)
	}
	ok, err = Verify(stored, "correct horsf")
	if err != nil || ok {
		t.Fatalf("wrong password verify = %v %v", ok, err)
	}
}

func TestHashSaltsEachCall(t *testing.T) {
	a, err := hash(bytes.NewReader(bytes.Repeat([]byte{1}, saltLen)), 10, "password1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := hash(bytes.NewReader(bytes.Repeat([]byte{2}, saltLen)), 10, "password1")
	if err != nil {
		t.Fatal(err)
	}
	if a == b || len(a) != len(b) {
		t.Fatalf("hashes %q %q: salts must differ and length must not", a, b)
	}
}

func TestVerifyReadsIterationsFromStored(t *testing.T) {
	stored, err := hash(bytes.NewReader(make([]byte, saltLen)), 3, "password1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stored, "pbkdf2-sha256$3$") {
		t.Fatalf("stored = %q", stored)
	}
	ok, err := Verify(stored, "password1")
	if err != nil || !ok {
		t.Fatalf("verify = %v %v", ok, err)
	}
}

func TestVerifyMalformed(t *testing.T) {
	for _, s := range []string{
		"",
		"bcrypt$10$abc$def",
		"pbkdf2-sha256$x$AAAA$AAAA",
		"pbkdf2-sha256$0$AAAA$AAAA",
		"pbkdf2-sha256$99999999$AAAA$AAAA",
		"pbkdf2-sha256$10$!!$AAAA",
		"pbkdf2-sha256$10$AAAA$",
		"pbkdf2-sha256$10$AAAA",
	} {
		if _, err := Verify(s, "password1"); !errors.Is(err, ErrMalformed) {
			t.Errorf("Verify(%q) err = %v", s, err)
		}
	}
}

func TestDummyIsWellFormedAtProductionCost(t *testing.T) {
	if !strings.HasPrefix(Dummy, "pbkdf2-sha256$"+strconv.Itoa(Iterations)+"$") {
		t.Fatalf("dummy must cost the same as a real hash: %q", Dummy)
	}
	ok, err := Verify(Dummy, "password1")
	if err != nil || ok {
		t.Fatalf("dummy verify = %v %v", ok, err)
	}
}
