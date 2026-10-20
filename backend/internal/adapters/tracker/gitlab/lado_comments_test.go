package gitlab

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const ladoNotesPath = "/projects/group/project/issues/7/notes"

func ladoTestIssue() domain.TrackerID {
	return domain.TrackerID{Provider: domain.TrackerProviderGitLab, Native: "group/project#7"}
}

// ladoNoteJSON is a note as GitLab sends it.
func ladoNoteJSON(id, authorID int, username string, system bool) string {
	return fmt.Sprintf(`{"id":%d,"body":"note %d","system":%t,"created_at":"2026-10-19T08:00:00.000Z","author":{"id":%d,"username":%q}}`,
		id, id, system, authorID, username)
}

// ladoNoteList is a page of notes.
func ladoNoteList(notes ...string) []byte {
	return []byte("[" + strings.Join(notes, ",") + "]")
}

// serveLadoMembers answers member lookups on group/project under apiPrefix.
// A user with level 0 is not a member; the others have that access level.
func serveLadoMembers(f *fakeGL, apiPrefix string, levels map[int]int) {
	for id, level := range levels {
		f.on("GET", fmt.Sprintf("%s/projects/group/project/members/all/%d", apiPrefix, id), func(w http.ResponseWriter, r *http.Request) {
			if level == 0 {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"404 Not found"}`))
				return
			}
			_, _ = fmt.Fprintf(w, `{"id":%d,"username":"user%d","access_level":%d,"state":"active"}`, id, id, level)
		})
	}
}

// ladoQueryLog records the query of each request a handler served.
type ladoQueryLog struct {
	mu      sync.Mutex
	queries []url.Values
}

func (l *ladoQueryLog) add(r *http.Request) url.Values {
	q := r.URL.Query()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.queries = append(l.queries, q)
	return q
}

func (l *ladoQueryLog) all() []url.Values {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]url.Values(nil), l.queries...)
}

func ladoCommentIDs(comments []domain.LadoIssueComment) []string {
	ids := make([]string, 0, len(comments))
	for _, c := range comments {
		ids = append(ids, c.ID)
	}
	return ids
}

func TestLadoIssueCommentsFollowsPagesAndListsOldestFirst(t *testing.T) {
	f := newFakeGL(t)
	var log ladoQueryLog
	f.on("GET", ladoNotesPath, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tkn-test" {
			t.Errorf("Authorization = %q, want the token", got)
		}
		if log.add(r).Get("page") == "" {
			w.Header().Set("Link", `<`+f.server.URL+`/projects/group%2Fproject/issues/7/notes?order_by=created_at&page=2&per_page=100&sort=desc>; rel="next"`)
			_, _ = w.Write(ladoNoteList(
				`{"id":104,"body":"Plan:\n1. Add login","system":false,"created_at":"2026-10-19T17:30:00.123+09:00","author":{"id":11,"username":"dev"}}`,
				ladoNoteJSON(103, 11, "dev", true),
				ladoNoteJSON(102, 11, "dev", false),
			))
			return
		}
		_, _ = w.Write(ladoNoteList(ladoNoteJSON(101, 11, "dev", false)))
	})
	serveLadoMembers(f, "", map[int]int{11: 30})
	got, err := newTrackerForTest(t, f).LadoIssues().IssueComments(ctx(), ladoTestIssue())
	if err != nil {
		t.Fatalf("IssueComments: %v", err)
	}
	if ids, want := ladoCommentIDs(got), []string{"101", "102", "104"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("comment ids = %v, want %v: oldest first, without the system note", ids, want)
	}
	want := domain.LadoIssueComment{
		ID: "104", Author: "dev", AuthorTrusted: true,
		CreatedAt: time.Date(2026, 10, 19, 8, 30, 0, 123_000_000, time.UTC),
		Body:      "Plan:\n1. Add login",
		URL:       f.server.URL + "/group/project/-/issues/7#note_104",
	}
	if !reflect.DeepEqual(got[2], want) {
		t.Fatalf("newest comment = %+v, want %+v", got[2], want)
	}
	queries := log.all()
	if len(queries) != 2 {
		t.Fatalf("note requests = %d, want 2", len(queries))
	}
	if q := queries[0]; q.Get("sort") != "desc" || q.Get("order_by") != "created_at" || q.Get("per_page") != "100" {
		t.Fatalf("first query = %v, want the newest 100 notes", q)
	}
	if q := queries[1]; q.Get("page") != "2" {
		t.Fatalf("second query = %v, want page 2", q)
	}
}

func TestLadoIssueCommentsTrustsDevelopers(t *testing.T) {
	f := newFakeGL(t)
	f.on("GET", ladoNotesPath, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(ladoNoteList(
			ladoNoteJSON(7, 11, "dev", false),
			ladoNoteJSON(6, 14, "maintainer", false),
			ladoNoteJSON(5, 12, "reporter", false),
			ladoNoteJSON(4, 13, "stranger", false),
			`{"id":3,"body":"note 3","system":false,"created_at":"2026-10-19T08:00:00.000Z"}`,
			ladoNoteJSON(2, 15, "bot", true),
			ladoNoteJSON(1, 11, "dev", false),
		))
	})
	serveLadoMembers(f, "", map[int]int{11: 30, 12: 20, 13: 0, 14: 40})
	got, err := newTrackerForTest(t, f).LadoIssues().IssueComments(ctx(), ladoTestIssue())
	if err != nil {
		t.Fatalf("IssueComments: %v", err)
	}
	trusted := map[string]bool{}
	for _, c := range got {
		trusted[c.ID] = c.AuthorTrusted
	}
	if want := map[string]bool{"1": true, "3": false, "4": false, "5": false, "6": true, "7": true}; !reflect.DeepEqual(trusted, want) {
		t.Fatalf("trusted by comment = %v, want %v", trusted, want)
	}
	lookups := map[string]int{}
	for _, call := range f.calls() {
		if user, ok := strings.CutPrefix(call.Path, "/projects/group/project/members/all/"); ok {
			lookups[user]++
		}
	}
	if want := map[string]int{"11": 1, "12": 1, "13": 1, "14": 1}; !reflect.DeepEqual(lookups, want) {
		t.Fatalf("member lookups = %v, want one per author", lookups)
	}
}

func TestLadoIssueCommentsOnASelfManagedHost(t *testing.T) {
	defaultSrv := newFakeGL(t)
	selfManaged := newFakeGL(t)
	tr := newHostAwareTrackerForTest(t, defaultSrv, map[string]struct {
		server *fakeGL
		token  string
	}{"gitlab.example.com": {server: selfManaged, token: "tkn-host"}})
	// Serve the API under /api/v4, as a real host does.
	entry := tr.hosts["gitlab.example.com"]
	entry.baseURL = selfManaged.server.URL + "/api/v4"
	tr.hosts["gitlab.example.com"] = entry

	selfManaged.on("GET", "/api/v4"+ladoNotesPath, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tkn-host" {
			t.Errorf("Authorization = %q, want the host token", got)
		}
		if r.URL.Query().Get("page") == "" {
			// A next link naming another host is still read from this one.
			w.Header().Set("Link", `<https://elsewhere.invalid/api/v4/projects/group%2Fproject/issues/7/notes?order_by=created_at&page=2&per_page=100&sort=desc>; rel="next"`)
			_, _ = w.Write(ladoNoteList(ladoNoteJSON(202, 11, "dev", false)))
			return
		}
		_, _ = w.Write(ladoNoteList(ladoNoteJSON(201, 11, "dev", false)))
	})
	serveLadoMembers(selfManaged, "/api/v4", map[int]int{11: 30})
	id := ladoTestIssue()
	id.Host = "gitlab.example.com"
	got, err := tr.LadoIssues().IssueComments(ctx(), id)
	if err != nil {
		t.Fatalf("IssueComments: %v", err)
	}
	wantURLs := []string{
		selfManaged.server.URL + "/group/project/-/issues/7#note_201",
		selfManaged.server.URL + "/group/project/-/issues/7#note_202",
	}
	if len(got) != 2 || got[0].URL != wantURLs[0] || got[1].URL != wantURLs[1] {
		t.Fatalf("comments = %+v, want links %v", got, wantURLs)
	}
	if calls := defaultSrv.calls(); len(calls) != 0 {
		t.Fatalf("gitlab.com got %d calls, want none", len(calls))
	}
}

func TestLadoIssueCommentsFollowsXNextPage(t *testing.T) {
	f := newFakeGL(t)
	var log ladoQueryLog
	f.on("GET", ladoNotesPath, func(w http.ResponseWriter, r *http.Request) {
		if log.add(r).Get("page") == "" {
			w.Header().Set("X-Next-Page", "2")
			_, _ = w.Write(ladoNoteList(ladoNoteJSON(2, 11, "dev", false)))
			return
		}
		w.Header().Set("X-Next-Page", "")
		_, _ = w.Write(ladoNoteList(ladoNoteJSON(1, 11, "dev", false)))
	})
	serveLadoMembers(f, "", map[int]int{11: 30})
	got, err := newTrackerForTest(t, f).LadoIssues().IssueComments(ctx(), ladoTestIssue())
	if err != nil {
		t.Fatalf("IssueComments: %v", err)
	}
	if ids, want := ladoCommentIDs(got), []string{"1", "2"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("comment ids = %v, want %v", ids, want)
	}
	queries := log.all()
	if len(queries) != 2 {
		t.Fatalf("note requests = %d, want 2", len(queries))
	}
	if q := queries[1]; q.Get("page") != "2" || q.Get("sort") != "desc" || q.Get("order_by") != "created_at" || q.Get("per_page") != "100" {
		t.Fatalf("second query = %v, want page 2 of the same listing", q)
	}
}

func TestLadoIssueCommentsKeepsANoteOnce(t *testing.T) {
	f := newFakeGL(t)
	f.on("GET", ladoNotesPath, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "" {
			w.Header().Set("X-Next-Page", "2")
			_, _ = w.Write(ladoNoteList(ladoNoteJSON(3, 11, "dev", false), ladoNoteJSON(2, 11, "dev", false)))
			return
		}
		// A comment posted between the reads pushed note 2 onto this page.
		_, _ = w.Write(ladoNoteList(ladoNoteJSON(2, 11, "dev", false), ladoNoteJSON(1, 11, "dev", false)))
	})
	serveLadoMembers(f, "", map[int]int{11: 30})
	got, err := newTrackerForTest(t, f).LadoIssues().IssueComments(ctx(), ladoTestIssue())
	if err != nil {
		t.Fatalf("IssueComments: %v", err)
	}
	if ids, want := ladoCommentIDs(got), []string{"1", "2", "3"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("comment ids = %v, want %v", ids, want)
	}
}

func TestLadoIssueCommentsStopsAfterTenPages(t *testing.T) {
	f := newFakeGL(t)
	var log ladoQueryLog
	f.on("GET", ladoNotesPath, func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(log.add(r).Get("page"))
		page = max(page, 1)
		if page < 12 {
			w.Header().Set("X-Next-Page", strconv.Itoa(page+1))
		}
		_, _ = w.Write(ladoNoteList(ladoNoteJSON(1000-page, 11, "dev", false)))
	})
	serveLadoMembers(f, "", map[int]int{11: 30})
	got, err := newTrackerForTest(t, f).LadoIssues().IssueComments(ctx(), ladoTestIssue())
	if err != nil {
		t.Fatalf("IssueComments: %v", err)
	}
	if queries := log.all(); len(queries) != 10 {
		t.Fatalf("note requests = %d, want 10", len(queries))
	}
	if len(got) != 10 || got[0].ID != "990" || got[9].ID != "999" {
		t.Fatalf("comment ids = %v, want the newest ten, oldest first", ladoCommentIDs(got))
	}
}

func TestLadoIssueCommentsFailedAnswers(t *testing.T) {
	for _, tc := range []struct {
		name         string
		notesStatus  int
		memberStatus int
		want         error
	}{
		{"issue not found", http.StatusNotFound, 0, ErrNotFound},
		{"bad token", http.StatusUnauthorized, 0, ErrAuthFailed},
		{"rate limit", http.StatusTooManyRequests, 0, ErrRateLimited},
		{"member lookup refused", 0, http.StatusForbidden, ErrAuthFailed},
		{"member lookup rate limit", 0, http.StatusTooManyRequests, ErrRateLimited},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeGL(t)
			f.on("GET", ladoNotesPath, func(w http.ResponseWriter, r *http.Request) {
				if tc.notesStatus != 0 {
					w.WriteHeader(tc.notesStatus)
					_, _ = w.Write([]byte(`{"message":"refused here"}`))
					return
				}
				_, _ = w.Write(ladoNoteList(ladoNoteJSON(1, 11, "dev", false)))
			})
			if tc.memberStatus != 0 {
				f.on("GET", "/projects/group/project/members/all/11", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(tc.memberStatus)
					_, _ = w.Write([]byte(`{"message":"refused here"}`))
				})
			}
			_, err := newTrackerForTest(t, f).LadoIssues().IssueComments(ctx(), ladoTestIssue())
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestLadoIssueCommentsUnreadablePageFailsTheCall(t *testing.T) {
	f := newFakeGL(t)
	f.on("GET", ladoNotesPath, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "" {
			w.Header().Set("X-Next-Page", "2")
			_, _ = w.Write(ladoNoteList(ladoNoteJSON(2, 11, "dev", false)))
			return
		}
		_, _ = w.Write([]byte(`{"notes":`))
	})
	got, err := newTrackerForTest(t, f).LadoIssues().IssueComments(ctx(), ladoTestIssue())
	if err == nil || !strings.Contains(err.Error(), "decode comments") {
		t.Fatalf("err = %v, want a decode error", err)
	}
	if got != nil {
		t.Fatalf("comments = %+v, want none from a failed call", got)
	}
}

func TestLadoIssueCommentsRefusesOtherIssues(t *testing.T) {
	f := newFakeGL(t)
	for _, tc := range []struct {
		id   domain.TrackerID
		want error
	}{
		{domain.TrackerID{Provider: domain.TrackerProviderGitHub, Native: "group/project#7"}, ErrWrongProvider},
		{domain.TrackerID{Native: "group/project#7"}, ErrWrongProvider},
		{domain.TrackerID{Provider: domain.TrackerProviderGitLab, Native: "group/project"}, ErrBadID},
		{domain.TrackerID{Provider: domain.TrackerProviderGitLab, Native: "group/project#0"}, ErrBadID},
		{domain.TrackerID{Provider: domain.TrackerProviderGitLab, Native: "group/project#7", Host: "not-allowed.example.com"}, ErrHostNotAllowed},
	} {
		if _, err := newTrackerForTest(t, f).LadoIssues().IssueComments(ctx(), tc.id); !errors.Is(err, tc.want) {
			t.Errorf("IssueComments(%+v) err = %v, want %v", tc.id, err, tc.want)
		}
	}
	if calls := f.calls(); len(calls) != 0 {
		t.Fatalf("calls = %d, want none", len(calls))
	}
}

func TestLadoWebBase(t *testing.T) {
	for apiBase, want := range map[string]string{
		"https://gitlab.com/api/v4":          "https://gitlab.com",
		"https://gitlab.example.com/api/v4/": "https://gitlab.example.com",
		"https://example.com/gitlab/api/v4":  "https://example.com/gitlab",
		"http://127.0.0.1:8080":              "http://127.0.0.1:8080",
	} {
		if got := ladoWebBase(apiBase); got != want {
			t.Errorf("ladoWebBase(%q) = %q, want %q", apiBase, got, want)
		}
	}
}
