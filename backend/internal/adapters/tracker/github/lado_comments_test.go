package github

import (
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const ladoCommentsPath = "/repos/acme/demo/issues/5/comments"

func ladoTestIssue() domain.TrackerID {
	return domain.TrackerID{Provider: domain.TrackerProviderGitHub, Native: "acme/demo#5"}
}

// ladoCommentJSON is a comment as GitHub sends it.
func ladoCommentJSON(id int, login, association string) string {
	return fmt.Sprintf(`{"id":%d,"user":{"login":%q},"author_association":%q,"body":"comment %d","created_at":"2026-10-19T08:00:00Z","html_url":"https://github.com/acme/demo/issues/5#issuecomment-%d"}`,
		id, login, association, id, id)
}

// ladoPageLog records the page each request asked for, "" for the first.
type ladoPageLog struct {
	mu    sync.Mutex
	pages []string
}

func (l *ladoPageLog) add(r *http.Request) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pages = append(l.pages, r.URL.Query().Get("page"))
}

func (l *ladoPageLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.pages...)
}

// serveLadoCommentPages serves an issue with pages pages of comments, one
// comment per page whose id is the page number. With last false the Link
// headers leave rel="last" out.
func serveLadoCommentPages(f *fakeGH, pages int, last bool) *ladoPageLog {
	log := &ladoPageLog{}
	link := func(page int) string {
		return fmt.Sprintf("<%s%s?per_page=100&page=%d>", f.server.URL, ladoCommentsPath, page)
	}
	f.on("GET", ladoCommentsPath, func(w http.ResponseWriter, r *http.Request) {
		log.add(r)
		page := 1
		if raw := r.URL.Query().Get("page"); raw != "" {
			page, _ = strconv.Atoi(raw)
		}
		if page < pages {
			value := link(page+1) + `; rel="next"`
			if last {
				value += ", " + link(pages) + `; rel="last"`
			}
			w.Header().Set("Link", value)
		}
		_, _ = w.Write([]byte("[" + ladoCommentJSON(page, "alice", "OWNER") + "]"))
	})
	return log
}

func ladoCommentIDs(comments []domain.LadoIssueComment) []string {
	ids := make([]string, 0, len(comments))
	for _, c := range comments {
		ids = append(ids, c.ID)
	}
	return ids
}

func TestLadoIssueCommentsFollowsPagesInOrder(t *testing.T) {
	f := newFakeGH(t)
	// GitHub's Link headers name the repository by its id.
	next := f.server.URL + "/repositories/42/issues/5/comments?per_page=100&page=2"
	f.on("GET", ladoCommentsPath, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tkn-test" {
			t.Errorf("Authorization = %q, want the token", got)
		}
		if got := r.URL.RawQuery; got != "per_page=100" {
			t.Errorf("query = %q, want per_page=100", got)
		}
		w.Header().Set("Link", `<`+next+`>; rel="next", <`+next+`>; rel="last"`)
		_, _ = w.Write([]byte(`[{"id":2412345678,"user":{"login":"alice"},"author_association":"OWNER","body":"Plan:\n1. Add login","created_at":"2026-10-19T17:30:00+09:00","html_url":"https://github.com/acme/demo/issues/5#issuecomment-2412345678"}]`))
	})
	f.on("GET", "/repositories/42/issues/5/comments", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tkn-test" {
			t.Errorf("Authorization = %q on page 2, want the token", got)
		}
		if got := r.URL.Query().Get("page"); got != "2" {
			t.Errorf("page = %q, want 2", got)
		}
		_, _ = w.Write([]byte(`[{"id":2412345999,"user":{"login":"bob"},"author_association":"CONTRIBUTOR","body":"Looks good","created_at":"2026-10-19T09:00:00Z","html_url":"https://github.com/acme/demo/issues/5#issuecomment-2412345999"}]`))
	})
	got, err := newTrackerForTest(t, f).LadoIssues().IssueComments(ctx(), ladoTestIssue())
	if err != nil {
		t.Fatalf("IssueComments: %v", err)
	}
	want := []domain.LadoIssueComment{
		{
			ID: "2412345678", Author: "alice", AuthorTrusted: true,
			CreatedAt: time.Date(2026, 10, 19, 8, 30, 0, 0, time.UTC),
			Body:      "Plan:\n1. Add login",
			URL:       "https://github.com/acme/demo/issues/5#issuecomment-2412345678",
		},
		{
			ID: "2412345999", Author: "bob",
			CreatedAt: time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC),
			Body:      "Looks good",
			URL:       "https://github.com/acme/demo/issues/5#issuecomment-2412345999",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("comments = %+v, want %+v", got, want)
	}
	if calls := f.calls(); len(calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(calls))
	}
}

func TestLadoIssueCommentsTrustsOwnersMembersAndCollaborators(t *testing.T) {
	cases := []struct {
		association string
		trusted     bool
	}{
		{"OWNER", true},
		{"MEMBER", true},
		{"COLLABORATOR", true},
		{"CONTRIBUTOR", false},
		{"FIRST_TIME_CONTRIBUTOR", false},
		{"NONE", false},
		{"", false},
	}
	page := make([]string, 0, len(cases))
	for i, c := range cases {
		page = append(page, ladoCommentJSON(i+1, "user"+strconv.Itoa(i+1), c.association))
	}
	f := newFakeGH(t)
	f.on("GET", ladoCommentsPath, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("[" + strings.Join(page, ",") + "]"))
	})
	got, err := newTrackerForTest(t, f).LadoIssues().IssueComments(ctx(), ladoTestIssue())
	if err != nil {
		t.Fatalf("IssueComments: %v", err)
	}
	if len(got) != len(cases) {
		t.Fatalf("comments = %d, want %d", len(got), len(cases))
	}
	for i, c := range cases {
		if got[i].AuthorTrusted != c.trusted {
			t.Errorf("author_association %q: trusted = %v, want %v", c.association, got[i].AuthorTrusted, c.trusted)
		}
	}
}

func TestLadoIssueCommentsDeletedAccountIsAnUntrustedGhost(t *testing.T) {
	f := newFakeGH(t)
	f.on("GET", ladoCommentsPath, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"id":9,"user":null,"author_association":"MEMBER","body":"Old plan","created_at":"2026-10-19T08:00:00Z","html_url":"https://github.com/acme/demo/issues/5#issuecomment-9"}]`))
	})
	got, err := newTrackerForTest(t, f).LadoIssues().IssueComments(ctx(), ladoTestIssue())
	if err != nil {
		t.Fatalf("IssueComments: %v", err)
	}
	if len(got) != 1 || got[0].Author != "ghost" || got[0].AuthorTrusted {
		t.Fatalf("comments = %+v, want one untrusted comment by ghost", got)
	}
}

func TestLadoIssueCommentsReadsTheNewestPages(t *testing.T) {
	f := newFakeGH(t)
	log := serveLadoCommentPages(f, 12, true)
	got, err := newTrackerForTest(t, f).LadoIssues().IssueComments(ctx(), ladoTestIssue())
	if err != nil {
		t.Fatalf("IssueComments: %v", err)
	}
	// The first page names the last; LADO then reads the newest ten pages.
	wantPages := []string{""}
	var wantIDs []string
	for page := 3; page <= 12; page++ {
		wantPages = append(wantPages, strconv.Itoa(page))
		wantIDs = append(wantIDs, strconv.Itoa(page))
	}
	if pages := log.all(); !reflect.DeepEqual(pages, wantPages) {
		t.Fatalf("requested pages = %q, want %q", pages, wantPages)
	}
	if ids := ladoCommentIDs(got); !reflect.DeepEqual(ids, wantIDs) {
		t.Fatalf("comment ids = %v, want %v", ids, wantIDs)
	}
}

func TestLadoIssueCommentsStopsAfterTenPages(t *testing.T) {
	f := newFakeGH(t)
	log := serveLadoCommentPages(f, 12, false)
	got, err := newTrackerForTest(t, f).LadoIssues().IssueComments(ctx(), ladoTestIssue())
	if err != nil {
		t.Fatalf("IssueComments: %v", err)
	}
	if pages := log.all(); len(pages) != 10 {
		t.Fatalf("requested pages = %q, want 10", pages)
	}
	if len(got) != 10 || got[0].ID != "1" || got[9].ID != "10" {
		t.Fatalf("comment ids = %v, want 1 to 10", ladoCommentIDs(got))
	}
}

func TestLadoIssueCommentsFailedAnswers(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		headers map[string]string
		want    error
	}{
		{"issue not found", http.StatusNotFound, nil, ErrNotFound},
		{"bad token", http.StatusUnauthorized, nil, ErrAuthFailed},
		{"no permission", http.StatusForbidden, nil, ErrAuthFailed},
		{"rate limit", http.StatusForbidden, map[string]string{"X-RateLimit-Remaining": "0"}, ErrRateLimited},
		{"secondary rate limit", http.StatusTooManyRequests, nil, ErrRateLimited},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeGH(t)
			f.on("GET", ladoCommentsPath, func(w http.ResponseWriter, r *http.Request) {
				for k, v := range tc.headers {
					w.Header().Set(k, v)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"message":"refused here"}`))
			})
			_, err := newTrackerForTest(t, f).LadoIssues().IssueComments(ctx(), ladoTestIssue())
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestLadoIssueCommentsUnreadablePageFailsTheCall(t *testing.T) {
	f := newFakeGH(t)
	f.on("GET", ladoCommentsPath, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "" {
			w.Header().Set("Link", `<`+f.server.URL+ladoCommentsPath+`?per_page=100&page=2>; rel="next"`)
			_, _ = w.Write([]byte("[" + ladoCommentJSON(1, "alice", "OWNER") + "]"))
			return
		}
		_, _ = w.Write([]byte(`{"comments":`))
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
	f := newFakeGH(t)
	for _, tc := range []struct {
		id   domain.TrackerID
		want error
	}{
		{domain.TrackerID{Provider: domain.TrackerProviderGitLab, Native: "acme/demo#5"}, ErrWrongProvider},
		{domain.TrackerID{Native: "acme/demo#5"}, ErrWrongProvider},
		{domain.TrackerID{Provider: domain.TrackerProviderGitHub, Native: "acme/demo"}, ErrBadID},
		{domain.TrackerID{Provider: domain.TrackerProviderGitHub, Native: "acme/demo#0"}, ErrBadID},
		{domain.TrackerID{Provider: domain.TrackerProviderGitHub, Native: "acme#5"}, ErrBadID},
	} {
		if _, err := newTrackerForTest(t, f).LadoIssues().IssueComments(ctx(), tc.id); !errors.Is(err, tc.want) {
			t.Errorf("IssueComments(%+v) err = %v, want %v", tc.id, err, tc.want)
		}
	}
	if calls := f.calls(); len(calls) != 0 {
		t.Fatalf("calls = %d, want none", len(calls))
	}
}
