// Package ladoblob is LADO's content store: immutable files named by the
// sha256 of their bytes, at <state>/lado/blobs/<sha[0:2]>/<sha>
// (docs/LJY-PRD/TDD.md §4.5). Pinned plans and issue snapshots live here, so
// AO pruning or deleting artifact versions cannot remove what a human
// approved. The layout copies AO's artifact blobs
// (internal/artifactversions/blobs.go), with owner-only permissions because
// issue text may be private.
package ladoblob

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// incomingPrefix names a blob being written. A file with this prefix is the
// remainder of an interrupted write.
const incomingPrefix = "incoming-"

// ErrNotFound reports a blob the store does not hold.
var ErrNotFound = errors.New("lado blob not found")

// ErrCorrupt reports a blob whose bytes no longer hash to its name.
var ErrCorrupt = errors.New("lado blob does not match its sha256")

// ErrTooLarge reports a blob over the caller's limit.
var ErrTooLarge = errors.New("lado blob exceeds the size limit")

// Store reads and writes blobs under one directory.
type Store struct {
	dir string

	mu   sync.Mutex
	root *os.Root
}

// New returns a store at <stateDir>/lado/blobs. The directory is created on
// first use.
func New(stateDir string) *Store {
	return &Store{dir: filepath.Join(stateDir, "lado", "blobs")}
}

// open creates the directory and opens it as an os.Root, so no name can
// lead outside it. Leftovers of interrupted writes are removed once.
func (s *Store) open() (*os.Root, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.root != nil {
		return s.root, nil
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return nil, fmt.Errorf("lado blobs: %w", err)
	}
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return nil, fmt.Errorf("lado blobs: %w", err)
	}
	sweep(root)
	s.root = root
	return root, nil
}

// Close releases the directory handle.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.root == nil {
		return nil
	}
	err := s.root.Close()
	s.root = nil
	return err
}

// Put stores data and returns its hex sha256. Storing content the store
// already holds is a no-op.
func (s *Store) Put(data []byte) (string, error) {
	root, err := s.open()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	name := hex.EncodeToString(sum[:])
	target := blobPath(name)
	if _, err := root.Stat(target); err == nil {
		return name, nil
	}
	tmp, f, err := createIncoming(root)
	if err != nil {
		return "", fmt.Errorf("lado blobs: %w", err)
	}
	_, err = f.Write(data)
	if syncErr := f.Sync(); err == nil {
		err = syncErr
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = root.MkdirAll(filepath.Dir(target), 0o700)
	}
	if err == nil {
		err = root.Rename(tmp, target)
	}
	if err != nil {
		_ = root.Remove(tmp)
		return "", fmt.Errorf("lado blobs: %w", err)
	}
	return name, nil
}

// Get returns the blob named by sum after checking that its bytes still hash
// to that name. A blob larger than limit is refused.
func (s *Store) Get(sum string, limit int64) ([]byte, error) {
	if !validSum(sum) {
		return nil, ErrNotFound
	}
	root, err := s.open()
	if err != nil {
		return nil, err
	}
	f, err := root.Open(blobPath(sum))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lado blobs: %w", err)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, fmt.Errorf("lado blobs: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, ErrTooLarge
	}
	got := sha256.Sum256(data)
	if hex.EncodeToString(got[:]) != sum {
		return nil, ErrCorrupt
	}
	return data, nil
}

func blobPath(sum string) string {
	return filepath.Join(sum[:2], sum)
}

func validSum(sum string) bool {
	if len(sum) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(sum)
	return err == nil && strings.ToLower(sum) == sum
}

func createIncoming(root *os.Root) (string, *os.File, error) {
	for range 3 {
		var suffix [8]byte
		if _, err := rand.Read(suffix[:]); err != nil {
			return "", nil, err
		}
		name := incomingPrefix + hex.EncodeToString(suffix[:])
		f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		return name, f, err
	}
	return "", nil, fs.ErrExist
}

// sweep removes temporary files left by interrupted writes. A failure only
// leaves them for the next start.
func sweep(root *os.Root) {
	dir, err := root.Open(".")
	if err != nil {
		return
	}
	entries, err := dir.ReadDir(-1)
	_ = dir.Close()
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.HasPrefix(entry.Name(), incomingPrefix) {
			_ = root.Remove(entry.Name())
		}
	}
}
