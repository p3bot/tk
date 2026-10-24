package flock

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAcquireReleaseRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".tk.lock")
	lock, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("lock file should be created: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	if err := lock.Release(); err != nil {
		t.Errorf("second Release must be a no-op, got %v", err)
	}
}

func TestExclusiveSerialisesHolders(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".tk.lock")
	first, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}

	got := make(chan struct{})
	go func() {
		second, err := Acquire(path)
		if err == nil {
			_ = second.Release()
		}
		close(got)
	}()

	select {
	case <-got:
		t.Fatal("a second exclusive Acquire must block while the first is held")
	case <-time.After(50 * time.Millisecond):
	}

	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("the blocked Acquire should proceed once the first lock releases")
	}
}

func TestTryAcquireDoesNotBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".tk.lock")
	first, acquired, err := TryAcquire(path)
	if err != nil || !acquired {
		t.Fatalf("first acquired=%v err=%v", acquired, err)
	}
	defer func() { _ = first.Release() }()

	second, acquired, err := TryAcquire(path)
	if err != nil {
		t.Fatal(err)
	}
	if acquired || second != nil {
		_ = second.Release()
		t.Fatal("TryAcquire must report the lock busy without taking it")
	}

	if err := first.WriteHead([]byte("abcdefghijklmnopq")); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "abcdefghijklmnopq" {
		t.Fatalf("lock file = %q", got)
	}
}
