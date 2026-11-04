// Package projectmemo stores the orchestrator's project memo: a short handoff
// note the orchestrator writes before /clear and every new orchestrator
// conversation reads. It lives in files under the AO data dir, one directory
// per project:
//
//	<dataDir>/project-memos/<projectId>/current.json
//	<dataDir>/project-memos/<projectId>/history/<revision>.json
//
// current.json is the memo in force. history keeps the HistoryLimit revisions
// it replaced, newest kept, so a person can look back and restore one. Files
// are 0600 in 0700 directories and every write is an atomic rename.
//
// The chat service and the HTTP API each build their own Store over the same
// data dir. Writers for one project serialise on a process-wide lock, so they
// never interleave even through different Store values.
package projectmemo

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	// MaxRunes caps a memo body, counted in runes after secrets are masked.
	MaxRunes = 4000
	// HistoryLimit is how many replaced revisions history keeps.
	HistoryLimit = 10
	// StaleAfter is the age past which a memo counts as old.
	StaleAfter = 7 * 24 * time.Hour

	dirName         = "project-memos"
	currentFileName = "current.json"
	historyDirName  = "history"
	fileVersion     = 1
	// maxFileBytes bounds a memo file read so a damaged file cannot use
	// unbounded memory. A full memo is at most 16 KiB of UTF-8 plus metadata.
	maxFileBytes = 1 << 20
)

// Source says who wrote a revision.
type Source string

const (
	// SourceClear is a memo the orchestrator wrote before /clear.
	SourceClear Source = "clear"
	// SourceUser is a memo a person saved, including a restored old revision.
	SourceUser Source = "user"
)

// Valid reports whether s is a known source.
func (s Source) Valid() bool { return s == SourceClear || s == SourceUser }

var (
	// ErrNotFound means the project has no memo, or the revision does not exist.
	ErrNotFound = errors.New("project memo not found")
	// ErrTooLong means the body is over MaxRunes.
	ErrTooLong = errors.New("project memo is too long")
	// ErrInvalid means the input is malformed: empty or non-UTF-8 body, unknown
	// source, bad project id or bad revision id.
	ErrInvalid = errors.New("invalid project memo")
	// ErrInvalidProjectID means the project id is not a safe path segment. It
	// also matches ErrInvalid.
	ErrInvalidProjectID = fmt.Errorf("%w: project id is not a safe path segment", ErrInvalid)
	// ErrConflict means the base revision a writer read is no longer current.
	ErrConflict = errors.New("project memo changed since it was read")
)

// Memo is one revision of a project memo.
type Memo struct {
	ProjectID domain.ProjectID `json:"projectId"`
	Revision  string           `json:"revision"`
	Source    Source           `json:"source"`
	UpdatedAt time.Time        `json:"updatedAt"`
	Body      string           `json:"body"`
}

// Stale reports whether the memo is older than StaleAfter at now.
func (m Memo) Stale(now time.Time) bool {
	return !m.UpdatedAt.IsZero() && now.Sub(m.UpdatedAt) > StaleAfter
}

// Runes is the body length the MaxRunes limit counts.
func (m Memo) Runes() int { return utf8.RuneCountInString(m.Body) }

// SaveInput is one write.
type SaveInput struct {
	Body   string
	Source Source
	// BaseRevision, when set, is the revision the writer read. The save fails
	// with ErrConflict unless it is still current; a pointer to "" expects no
	// memo yet. Nil skips the check (the /clear path writes unconditionally).
	BaseRevision *string
}

// fileRecord is the on-disk shape of current.json and history files.
type fileRecord struct {
	Version int `json:"version"`
	Memo
}

// Store reads and writes project memos under one AO data dir.
type Store struct {
	root string
	now  func() time.Time
}

// New returns a Store rooted at <dataDir>/project-memos.
func New(dataDir string) *Store {
	return &Store{root: filepath.Join(dataDir, dirName), now: time.Now}
}

// WithClock replaces the clock; tests use it to pin UpdatedAt.
func (s *Store) WithClock(now func() time.Time) *Store {
	s.now = now
	return s
}

// CurrentPath returns the path of the project's current memo file under
// dataDir. The file may not exist yet.
func CurrentPath(dataDir string, projectID domain.ProjectID) (string, error) {
	return New(dataDir).CurrentPath(projectID)
}

// CurrentPath returns the path of the project's current memo file. The file
// may not exist yet.
func (s *Store) CurrentPath(projectID domain.ProjectID) (string, error) {
	dir, err := s.projectDir(projectID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, currentFileName), nil
}

// Get returns the project's current memo, or ErrNotFound.
func (s *Store) Get(projectID domain.ProjectID) (Memo, error) {
	dir, err := s.projectDir(projectID)
	if err != nil {
		return Memo{}, err
	}
	return readMemo(filepath.Join(dir, currentFileName))
}

// GetRevision returns one revision: the current memo or a kept history entry.
func (s *Store) GetRevision(projectID domain.ProjectID, revision string) (Memo, error) {
	dir, err := s.projectDir(projectID)
	if err != nil {
		return Memo{}, err
	}
	if !validRevision(revision) {
		return Memo{}, fmt.Errorf("%w: revision id %q", ErrInvalid, revision)
	}
	current, err := readMemo(filepath.Join(dir, currentFileName))
	switch {
	case err == nil && current.Revision == revision:
		return current, nil
	case err != nil && !errors.Is(err, ErrNotFound):
		return Memo{}, err
	}
	m, err := readMemo(filepath.Join(dir, historyDirName, revision+".json"))
	if err != nil {
		return Memo{}, err
	}
	if m.Revision != revision {
		return Memo{}, ErrNotFound
	}
	return m, nil
}

// ListRevisions returns the kept earlier revisions, newest first. It does not
// include the current memo. Unreadable history files are skipped.
func (s *Store) ListRevisions(projectID domain.ProjectID) ([]Memo, error) {
	dir, err := s.projectDir(projectID)
	if err != nil {
		return nil, err
	}
	names, err := historyNames(filepath.Join(dir, historyDirName))
	if err != nil {
		return nil, err
	}
	out := make([]Memo, 0, len(names))
	for i := len(names) - 1; i >= 0; i-- {
		m, err := readMemo(filepath.Join(dir, historyDirName, names[i]+".json"))
		if err != nil || m.Revision != names[i] {
			continue
		}
		out = append(out, m)
	}
	return out, nil
}

// Save masks known secrets in the body, checks it, and makes it the current
// memo. The memo it replaces moves to history; history older than the newest
// HistoryLimit entries is deleted.
func (s *Store) Save(projectID domain.ProjectID, in SaveInput) (Memo, error) {
	dir, err := s.projectDir(projectID)
	if err != nil {
		return Memo{}, err
	}
	body, err := normalizeBody(in.Body)
	if err != nil {
		return Memo{}, err
	}
	if !in.Source.Valid() {
		return Memo{}, fmt.Errorf("%w: source %q", ErrInvalid, in.Source)
	}

	mu := projectLock(dir)
	mu.Lock()
	defer mu.Unlock()

	currentPath := filepath.Join(dir, currentFileName)
	previous, err := readMemo(currentPath)
	hasPrevious := err == nil
	if err != nil && !errors.Is(err, ErrNotFound) {
		return Memo{}, err
	}
	if in.BaseRevision != nil && *in.BaseRevision != previous.Revision {
		return Memo{}, ErrConflict
	}

	historyDir := filepath.Join(dir, historyDirName)
	if err := ensurePrivateDir(historyDir); err != nil {
		return Memo{}, err
	}
	now := s.now().UTC()
	if hasPrevious && !now.After(previous.UpdatedAt) {
		// Keep revisions ordered even if the clock steps back.
		now = previous.UpdatedAt.Add(time.Nanosecond)
	}
	revision, err := newRevision(now)
	if err != nil {
		return Memo{}, err
	}
	next := Memo{ProjectID: projectID, Revision: revision, Source: in.Source, UpdatedAt: now, Body: body}

	// Copy the old memo into history before replacing it: a crash between the
	// two writes leaves a duplicate, never a lost revision.
	if hasPrevious && validRevision(previous.Revision) {
		if err := writeMemo(filepath.Join(historyDir, previous.Revision+".json"), previous); err != nil {
			return Memo{}, err
		}
	}
	if err := writeMemo(currentPath, next); err != nil {
		return Memo{}, err
	}
	// Pruning is cleanup: the new memo is already saved, and the next save
	// retries whatever this one could not delete.
	_ = pruneHistory(historyDir)
	return next, nil
}

func (s *Store) projectDir(projectID domain.ProjectID) (string, error) {
	if !validProjectID(string(projectID)) {
		return "", ErrInvalidProjectID
	}
	return filepath.Join(s.root, string(projectID)), nil
}

// projectIDPattern matches the project service's id rule, so every id a
// project can have is accepted and nothing else is.
var projectIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func validProjectID(id string) bool {
	return id != "" && len(id) <= 255 && !strings.Contains(id, "..") && projectIDPattern.MatchString(id)
}

// Revision ids are a fixed-width UTC timestamp and a random suffix, so the
// lexical order of history file names is their age order.
const revisionTimeLayout = "20060102T150405.000000000Z"

var revisionPattern = regexp.MustCompile(`^\d{8}T\d{6}\.\d{9}Z-[0-9a-f]{8}$`)

func validRevision(rev string) bool { return revisionPattern.MatchString(rev) }

func newRevision(at time.Time) (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("make project memo revision id: %w", err)
	}
	return at.UTC().Format(revisionTimeLayout) + "-" + hex.EncodeToString(b[:]), nil
}

func normalizeBody(raw string) (string, error) {
	if !utf8.ValidString(raw) {
		return "", fmt.Errorf("%w: body is not valid UTF-8", ErrInvalid)
	}
	body := strings.TrimSpace(strings.ReplaceAll(raw, "\r\n", "\n"))
	if body == "" {
		return "", fmt.Errorf("%w: body is empty", ErrInvalid)
	}
	body = Redact(body)
	if n := utf8.RuneCountInString(body); n > MaxRunes {
		return "", fmt.Errorf("%w: %d characters, limit %d", ErrTooLong, n, MaxRunes)
	}
	return body, nil
}

// Redacted replaces every masked secret.
const Redacted = "[REDACTED]"

// secretPatterns are well-known credential shapes. Matching is a second line
// of defence behind the memo request telling the agent not to write secrets;
// it cannot catch every secret.
var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`-{5}BEGIN [A-Z0-9 ]+-{5}[\s\S]*?-{5}END [A-Z0-9 ]+-{5}`),
	// A private key header with no END line: mask the header and its body.
	regexp.MustCompile(`-{5}BEGIN [A-Z0-9 ]*PRIVATE KEY-{5}(?:\s*[A-Za-z0-9+/=]+)*`),
	regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}`),
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`\bglpat-[A-Za-z0-9_.\-]{20,}`),
	regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_\-]{20,}`),
	regexp.MustCompile(`\bsk-[A-Za-z0-9_\-]{20,}`),
	regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`),
	regexp.MustCompile(`\bxox[abposr]-[A-Za-z0-9\-]{10,}`),
}

// Redact replaces known secret patterns in s with Redacted.
func Redact(s string) string {
	for _, re := range secretPatterns {
		s = re.ReplaceAllString(s, Redacted)
	}
	return s
}

var locks sync.Map // project dir -> *sync.Mutex

func projectLock(dir string) *sync.Mutex {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	mu, _ := locks.LoadOrStore(dir, &sync.Mutex{})
	return mu.(*sync.Mutex) //nolint:errcheck // locks only holds *sync.Mutex.
}

func readMemo(path string) (Memo, error) {
	f, err := os.Open(path) //nolint:gosec // G304: path is built from a validated project id under the AO data dir.
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Memo{}, ErrNotFound
		}
		return Memo{}, fmt.Errorf("read project memo: %w", err)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil {
		return Memo{}, fmt.Errorf("read project memo: %w", err)
	}
	if len(data) > maxFileBytes {
		return Memo{}, fmt.Errorf("read project memo %s: file is larger than %d bytes", path, maxFileBytes)
	}
	var rec fileRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return Memo{}, fmt.Errorf("decode project memo %s: %w", path, err)
	}
	return rec.Memo, nil
}

func writeMemo(path string, m Memo) error {
	data, err := json.MarshalIndent(fileRecord{Version: fileVersion, Memo: m}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode project memo: %w", err)
	}
	return writeFileAtomic(path, append(data, '\n'))
}

func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := ensurePrivateDir(dir); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".memo-*.tmp")
	if err != nil {
		return fmt.Errorf("write project memo: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("protect project memo: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write project memo: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write project memo: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write project memo: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace project memo: %w", err)
	}
	return nil
}

// ensurePrivateDir creates dir and its memo parents as 0700. Chmod after
// MkdirAll because the process umask can widen new directories.
func ensurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create project memo directory: %w", err)
	}
	for d := dir; ; d = filepath.Dir(d) {
		if err := os.Chmod(d, 0o700); err != nil { //nolint:gosec // G302: a directory needs its execute bit; 0700 keeps it to the user.
			return fmt.Errorf("protect project memo directory: %w", err)
		}
		if filepath.Base(d) == dirName || filepath.Dir(d) == d {
			return nil
		}
	}
}

// historyNames returns the history revision ids, oldest first.
func historyNames(historyDir string) ([]string, error) {
	entries, err := os.ReadDir(historyDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("list project memo history: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		name, ok := strings.CutSuffix(e.Name(), ".json")
		if ok && validRevision(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}

// pruneHistory deletes history entries beyond the newest HistoryLimit.
func pruneHistory(historyDir string) error {
	names, err := historyNames(historyDir)
	if err != nil {
		return err
	}
	var errs []error
	for len(names) > HistoryLimit {
		if err := os.Remove(filepath.Join(historyDir, names[0]+".json")); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, fmt.Errorf("prune project memo history: %w", err))
		}
		names = names[1:]
	}
	return errors.Join(errs...)
}
