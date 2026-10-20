package ladoblob

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPutAndGetRoundTrip(t *testing.T) {
	state := t.TempDir()
	store := New(state)
	t.Cleanup(func() { _ = store.Close() })

	data := []byte("---\ntask: lado-task-1\n---\n# Plan\n")
	sum, err := store.Put(data)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	want := sha256.Sum256(data)
	if sum != hex.EncodeToString(want[:]) {
		t.Fatalf("Put = %s, want the content's sha256", sum)
	}
	path := filepath.Join(state, "lado", "blobs", sum[:2], sum)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("blob not at %s: %v", path, err)
	}
	if again, err := store.Put(data); err != nil || again != sum {
		t.Fatalf("Put of the same content = (%s, %v), want (%s, nil)", again, err, sum)
	}
	got, err := store.Get(sum, 1<<20)
	if err != nil || string(got) != string(data) {
		t.Fatalf("Get = (%q, %v), want the stored bytes", got, err)
	}
	if runtime.GOOS != "windows" {
		for _, p := range []string{filepath.Join(state, "lado", "blobs"), path} {
			info, err := os.Stat(p)
			if err != nil {
				t.Fatal(err)
			}
			if perm := info.Mode().Perm(); perm&0o077 != 0 {
				t.Fatalf("%s has mode %v, want owner-only", p, perm)
			}
		}
	}
}

func TestGetRefusesMissingCorruptAndLargeBlobs(t *testing.T) {
	state := t.TempDir()
	store := New(state)
	t.Cleanup(func() { _ = store.Close() })

	if _, err := store.Get("nope", 10); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get of a malformed name = %v, want ErrNotFound", err)
	}
	missing := sha256.Sum256([]byte("never stored"))
	if _, err := store.Get(hex.EncodeToString(missing[:]), 10); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get of an unknown blob = %v, want ErrNotFound", err)
	}
	sum, err := store.Put([]byte("approved plan"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(sum, 3); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Get over the limit = %v, want ErrTooLarge", err)
	}
	path := filepath.Join(state, "lado", "blobs", sum[:2], sum)
	if err := os.WriteFile(path, []byte("edited plan!!"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(sum, 1<<20); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Get of an edited blob = %v, want ErrCorrupt", err)
	}
}

func TestOpenSweepsInterruptedWrites(t *testing.T) {
	state := t.TempDir()
	dir := filepath.Join(state, "lado", "blobs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	leftover := filepath.Join(dir, incomingPrefix+"deadbeef")
	if err := os.WriteFile(leftover, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := New(state)
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.Put([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(leftover); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("leftover still present: %v", err)
	}
}
