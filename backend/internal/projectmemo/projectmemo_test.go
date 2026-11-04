package projectmemo

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const testProject domain.ProjectID = "demo"

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(time.Second)
	return c.t
}

func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	dataDir := t.TempDir()
	clock := &fakeClock{t: time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)}
	return New(dataDir).WithClock(clock.now), dataDir
}

func ptr(s string) *string { return &s }

func mustSave(t *testing.T, s *Store, body string) Memo {
	t.Helper()
	m, err := s.Save(testProject, SaveInput{Body: body, Source: SourceUser})
	if err != nil {
		t.Fatalf("Save(%q): %v", body, err)
	}
	return m
}

func TestSaveGetRoundTrip(t *testing.T) {
	s, dataDir := newTestStore(t)
	if _, err := s.Get(testProject); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get before save = %v, want ErrNotFound", err)
	}
	saved, err := s.Save(testProject, SaveInput{Body: "  범위: 병합은 직접 한다\r\n다음: #50  \n", Source: SourceClear})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if saved.Body != "범위: 병합은 직접 한다\n다음: #50" {
		t.Fatalf("Body = %q", saved.Body)
	}
	if saved.Source != SourceClear || saved.ProjectID != testProject || !validRevision(saved.Revision) || saved.UpdatedAt.IsZero() {
		t.Fatalf("saved = %+v", saved)
	}
	got, err := s.Get(testProject)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != saved {
		t.Fatalf("Get = %+v, want %+v", got, saved)
	}
	// A second Store over the same data dir reads the same memo.
	again, err := New(dataDir).Get(testProject)
	if err != nil || again != saved {
		t.Fatalf("Get from new store = %+v, %v", again, err)
	}
	byRev, err := s.GetRevision(testProject, saved.Revision)
	if err != nil || byRev != saved {
		t.Fatalf("GetRevision(current) = %+v, %v", byRev, err)
	}
	path, err := CurrentPath(dataDir, testProject)
	if err != nil {
		t.Fatalf("CurrentPath: %v", err)
	}
	if want := filepath.Join(dataDir, "project-memos", "demo", "current.json"); path != want {
		t.Fatalf("CurrentPath = %q, want %q", path, want)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("current file: %v", err)
	}
}

func TestSaveRejectsTooLongAndInvalid(t *testing.T) {
	s, _ := newTestStore(t)
	// Exactly the limit, in multi-byte runes, is accepted.
	if _, err := s.Save(testProject, SaveInput{Body: strings.Repeat("가", MaxRunes), Source: SourceUser}); err != nil {
		t.Fatalf("Save at limit: %v", err)
	}
	_, err := s.Save(testProject, SaveInput{Body: strings.Repeat("가", MaxRunes+1), Source: SourceUser})
	if !errors.Is(err, ErrTooLong) {
		t.Fatalf("Save over limit = %v, want ErrTooLong", err)
	}
	for name, in := range map[string]SaveInput{
		"empty":       {Body: " \n\t", Source: SourceUser},
		"bad utf8":    {Body: "a\xffb", Source: SourceUser},
		"bad source":  {Body: "memo", Source: "robot"},
		"zero source": {Body: "memo"},
	} {
		if _, err := s.Save(testProject, in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: Save = %v, want ErrInvalid", name, err)
		}
	}
	// The rejected writes left the accepted memo in place.
	got, err := s.Get(testProject)
	if err != nil || got.Runes() != MaxRunes {
		t.Fatalf("Get = %d runes, %v", got.Runes(), err)
	}
}

func TestInvalidProjectIDRejected(t *testing.T) {
	s, dataDir := newTestStore(t)
	for _, id := range []domain.ProjectID{"", ".", "..", "../x", "a/b", `a\b`, "a..b", ".hidden", "-x", "a b", "한글"} {
		if _, err := s.Get(id); !errors.Is(err, ErrInvalidProjectID) || !errors.Is(err, ErrInvalid) {
			t.Errorf("Get(%q) = %v, want ErrInvalidProjectID", id, err)
		}
		if _, err := s.Save(id, SaveInput{Body: "x", Source: SourceUser}); !errors.Is(err, ErrInvalidProjectID) {
			t.Errorf("Save(%q) = %v, want ErrInvalidProjectID", id, err)
		}
		if _, err := s.ListRevisions(id); !errors.Is(err, ErrInvalidProjectID) {
			t.Errorf("ListRevisions(%q) = %v", id, err)
		}
		if _, err := CurrentPath(dataDir, id); !errors.Is(err, ErrInvalidProjectID) {
			t.Errorf("CurrentPath(%q) = %v", id, err)
		}
	}
	for _, id := range []domain.ProjectID{"demo", "ao-demo", "My.Project_2"} {
		if _, err := s.Save(id, SaveInput{Body: "x", Source: SourceUser}); err != nil {
			t.Errorf("Save(%q): %v", id, err)
		}
	}
	if _, err := s.GetRevision(testProject, "../current"); !errors.Is(err, ErrInvalid) {
		t.Errorf("GetRevision(traversal) = %v, want ErrInvalid", err)
	}
}

func TestHistoryKeepsTenAndPrunes(t *testing.T) {
	s, dataDir := newTestStore(t)
	var saved []Memo
	for i := range HistoryLimit + 5 {
		saved = append(saved, mustSave(t, s, "memo "+string(rune('a'+i))))
	}
	current := saved[len(saved)-1]
	revs, err := s.ListRevisions(testProject)
	if err != nil {
		t.Fatalf("ListRevisions: %v", err)
	}
	if len(revs) != HistoryLimit {
		t.Fatalf("len(revisions) = %d, want %d", len(revs), HistoryLimit)
	}
	// Newest first, the current memo excluded, the oldest pruned.
	for i, m := range revs {
		want := saved[len(saved)-2-i]
		if m != want {
			t.Fatalf("revisions[%d] = %+v, want %+v", i, m, want)
		}
	}
	entries, err := os.ReadDir(filepath.Join(dataDir, "project-memos", "demo", "history"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != HistoryLimit {
		t.Fatalf("history has %d files, want %d", len(entries), HistoryLimit)
	}
	if _, err := s.GetRevision(testProject, saved[0].Revision); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pruned revision = %v, want ErrNotFound", err)
	}
	old, err := s.GetRevision(testProject, saved[5].Revision)
	if err != nil || old != saved[5] {
		t.Fatalf("GetRevision(kept) = %+v, %v", old, err)
	}
	// Restoring a revision is a new save of its body.
	restored, err := s.Save(testProject, SaveInput{Body: old.Body, Source: SourceUser, BaseRevision: ptr(current.Revision)})
	if err != nil || restored.Body != old.Body || restored.Revision == old.Revision {
		t.Fatalf("restore = %+v, %v", restored, err)
	}
}

func TestBaseRevisionConflict(t *testing.T) {
	s, _ := newTestStore(t)
	if _, err := s.Save(testProject, SaveInput{Body: "x", Source: SourceUser, BaseRevision: ptr("20260101T000000.000000000Z-00000000")}); !errors.Is(err, ErrConflict) {
		t.Fatalf("Save with base on empty = %v, want ErrConflict", err)
	}
	first, err := s.Save(testProject, SaveInput{Body: "first", Source: SourceUser, BaseRevision: ptr("")})
	if err != nil {
		t.Fatalf("Save expecting no memo: %v", err)
	}
	if _, err := s.Save(testProject, SaveInput{Body: "again", Source: SourceUser, BaseRevision: ptr("")}); !errors.Is(err, ErrConflict) {
		t.Fatalf("second create = %v, want ErrConflict", err)
	}
	// /clear writes without a base and moves the memo on.
	cleared, err := s.Save(testProject, SaveInput{Body: "from clear", Source: SourceClear})
	if err != nil {
		t.Fatalf("clear save: %v", err)
	}
	if _, err := s.Save(testProject, SaveInput{Body: "stale edit", Source: SourceUser, BaseRevision: ptr(first.Revision)}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale edit = %v, want ErrConflict", err)
	}
	got, _ := s.Get(testProject)
	if got != cleared {
		t.Fatalf("conflict changed the memo: %+v", got)
	}
	if _, err := s.Save(testProject, SaveInput{Body: "fresh edit", Source: SourceUser, BaseRevision: ptr(cleared.Revision)}); err != nil {
		t.Fatalf("fresh edit: %v", err)
	}
}

func TestRedactsKnownSecrets(t *testing.T) {
	// Fake secrets are split so secret scanners (gitleaks) do not flag the fixtures.
	const pemBegin = "-----BEGIN "
	secrets := map[string]string{
		"github classic":  "ghp_" + strings.Repeat("A1b2", 9),
		"github oauth":    "gho_" + strings.Repeat("Z9", 18),
		"github fine":     "github_pat_11ABCDEFG0" + strings.Repeat("x_Y", 20),
		"gitlab":          "glpat-" + strings.Repeat("aB3", 7),
		"anthropic":       "sk-ant-api03-" + strings.Repeat("Qw-_", 10),
		"openai":          "sk-proj-" + strings.Repeat("abc123", 5),
		"aws":             "AKIA" + "IOSFODNN7EXAMPLE",
		"pem":             pemBegin + "OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAA\nAAAABG5vbmU=\n-----END OPENSSH PRIVATE KEY-----",
		"pem cert":        "-----BEGIN CERTIFICATE-----\nMIIBszCCAVmgAwIBAgIU\n-----END CERTIFICATE-----",
		"pem no end line": pemBegin + "RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA\nq83lmwoe+/==",
	}
	s, _ := newTestStore(t)
	for name, secret := range secrets {
		body := "토큰은 " + secret + " 이다. 다음 할 일은 리뷰."
		m, err := s.Save(testProject, SaveInput{Body: body, Source: SourceClear})
		if err != nil {
			t.Fatalf("%s: Save: %v", name, err)
		}
		if strings.Contains(m.Body, secret) || !strings.Contains(m.Body, Redacted) {
			t.Errorf("%s: body not masked: %q", name, m.Body)
		}
		if !strings.HasPrefix(m.Body, "토큰은 ") || !strings.HasSuffix(m.Body, "다음 할 일은 리뷰.") {
			t.Errorf("%s: masking ate surrounding text: %q", name, m.Body)
		}
		got, _ := s.Get(testProject)
		if strings.Contains(got.Body, secret) {
			t.Errorf("%s: stored body holds the secret", name)
		}
	}
	// Ordinary prose with similar prefixes stays.
	plain := "task-runner, disk-usage, sk-short, ghp_tiny, AKIA-docs"
	if got := Redact(plain); got != plain {
		t.Fatalf("Redact(plain) = %q", got)
	}
}

func TestStale(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		age  time.Duration
		want bool
	}{
		{0, false},
		{6 * 24 * time.Hour, false},
		{StaleAfter, false},
		{StaleAfter + time.Minute, true},
		{30 * 24 * time.Hour, true},
	}
	for _, c := range cases {
		m := Memo{UpdatedAt: now.Add(-c.age)}
		if got := m.Stale(now); got != c.want {
			t.Errorf("Stale(age %v) = %v, want %v", c.age, got, c.want)
		}
	}
	if (Memo{}).Stale(now) {
		t.Error("zero memo is stale")
	}
	// A saved memo becomes stale as the clock moves on.
	saved := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	s := New(t.TempDir()).WithClock(func() time.Time { return saved })
	m, err := s.Save(testProject, SaveInput{Body: "old", Source: SourceUser})
	if err != nil {
		t.Fatal(err)
	}
	if !m.Stale(saved.Add(8*24*time.Hour)) || m.Stale(saved.Add(time.Hour)) {
		t.Fatalf("saved memo staleness wrong: %+v", m)
	}
}

func TestFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	s, dataDir := newTestStore(t)
	mustSave(t, s, "one")
	mustSave(t, s, "two")
	root := filepath.Join(dataDir, "project-memos")
	for _, dir := range []string{root, filepath.Join(root, "demo"), filepath.Join(root, "demo", "history")} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o700 {
			t.Errorf("%s mode = %o, want 700", dir, got)
		}
	}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if strings.HasPrefix(d.Name(), ".memo-") {
			t.Errorf("leftover temp file %s", path)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("%s mode = %o, want 600", path, got)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentSavesSerialise(t *testing.T) {
	dataDir := t.TempDir()
	first, err := New(dataDir).Save(testProject, SaveInput{Body: "base", Source: SourceUser})
	if err != nil {
		t.Fatal(err)
	}
	// Writers through different Store values all aim at the same base; the
	// process-wide lock lets exactly one win.
	const writers = 8
	var wg sync.WaitGroup
	results := make(chan error, writers)
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := New(dataDir).Save(testProject, SaveInput{Body: "edit " + string(rune('a'+i)), Source: SourceUser, BaseRevision: ptr(first.Revision)})
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	wins := 0
	for err := range results {
		switch {
		case err == nil:
			wins++
		case !errors.Is(err, ErrConflict):
			t.Fatalf("Save: %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("%d writers won, want 1", wins)
	}
	revs, err := New(dataDir).ListRevisions(testProject)
	if err != nil || len(revs) != 1 || revs[0].Revision != first.Revision {
		t.Fatalf("history = %+v, %v", revs, err)
	}
}

func TestClockStepBackKeepsOrder(t *testing.T) {
	times := []time.Time{
		time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC),
	}
	i := 0
	s := New(t.TempDir()).WithClock(func() time.Time { t := times[i]; i++; return t })
	a := mustSave(t, s, "a")
	b := mustSave(t, s, "b")
	if !b.UpdatedAt.After(a.UpdatedAt) || b.Revision <= a.Revision {
		t.Fatalf("revisions out of order: %+v then %+v", a, b)
	}
}

func TestClearOutcomeHelpers(t *testing.T) {
	m := Memo{Revision: "r", UpdatedAt: time.Unix(10, 0)}
	if got := SavedOutcome(m); got.Status != ClearSaved || got.Revision != "r" || !got.UpdatedAt.Equal(m.UpdatedAt) {
		t.Fatalf("SavedOutcome = %+v", got)
	}
	if got := FailedOutcome("memo_timeout", "late"); got.Status != ClearFailed || got.Code != "memo_timeout" || got.Message != "late" {
		t.Fatalf("FailedOutcome = %+v", got)
	}
	if got := SkippedOutcome(); got.Status != ClearSkipped || got.Code != "" {
		t.Fatalf("SkippedOutcome = %+v", got)
	}
}
