package domain_test

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/trackerintake"
)

func TestLadoIssueRefRoundTrips(t *testing.T) {
	for _, tc := range []struct {
		name string
		id   domain.TrackerID
		ref  domain.IssueID
	}{
		{"github", domain.TrackerID{Provider: domain.TrackerProviderGitHub, Native: "acme/demo#12"}, "github:acme/demo#12"},
		{"gitlab.com", domain.TrackerID{Provider: domain.TrackerProviderGitLab, Native: "group/project#7"}, "gitlab:group/project#7"},
		{"nested group", domain.TrackerID{Provider: domain.TrackerProviderGitLab, Native: "group/sub/project#7"}, "gitlab:group/sub/project#7"},
		{"self-managed", domain.TrackerID{Provider: domain.TrackerProviderGitLab, Native: "group/project#7", Host: "gitlab.example.com"}, "gitlab:group/project#7@gitlab.example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := domain.LadoIssueRef(tc.id); got != tc.ref {
				t.Fatalf("LadoIssueRef = %q, want %q", got, tc.ref)
			}
			// AO stores this form in sessions.issue_id; LADO must not drift from it.
			if got := trackerintake.CanonicalIssueID(tc.id); got != tc.ref {
				t.Fatalf("AO's CanonicalIssueID = %q, want %q", got, tc.ref)
			}
			parsed, err := domain.ParseLadoIssueRef(tc.ref)
			if err != nil {
				t.Fatalf("ParseLadoIssueRef: %v", err)
			}
			if parsed != tc.id {
				t.Fatalf("ParseLadoIssueRef = %+v, want %+v", parsed, tc.id)
			}
		})
	}
}

func TestParseLadoIssueRefRejectsMalformedRefs(t *testing.T) {
	for _, ref := range []domain.IssueID{
		"",
		" github:acme/demo#12",
		"acme/demo#12",
		"github:",
		"github:acme/demo",
		"github:acme/demo#0",
		"github:acme/demo#x",
		"github:acme#12",
		"github:acme/demo/extra#12",
		"github:acme/demo#12@ghe.example.com",
		"gitlab:project#7",
		"gitlab:group/project#7@",
		"jira:ABC/DEF#2",
	} {
		if _, err := domain.ParseLadoIssueRef(ref); err == nil {
			t.Errorf("ParseLadoIssueRef(%q) = nil error, want an error", ref)
		}
	}
}

func TestLadoIssueNumber(t *testing.T) {
	if got := domain.LadoIssueNumber(domain.TrackerID{Provider: domain.TrackerProviderGitLab, Native: "group/sub/project#41"}); got != 41 {
		t.Fatalf("LadoIssueNumber = %d, want 41", got)
	}
	if got := domain.LadoIssueNumber(domain.TrackerID{Native: "acme/demo"}); got != 0 {
		t.Fatalf("LadoIssueNumber without a number = %d, want 0", got)
	}
}

func TestLadoIssueSnapshotIsCanonicalJSON(t *testing.T) {
	snapshot := domain.LadoIssueSnapshot{
		ID:    "github:acme/demo#12",
		Title: "Add <b>bold</b> & more",
		Body:  "line 1\r\nline 2\n<!-- lado-task:lado-task-1 -->",
	}
	want := `{"id":"github:acme/demo#12","title":"Add <b>bold</b> & more","body":"line 1\r\nline 2\n<!-- lado-task:lado-task-1 -->"}`
	if got := string(snapshot.CanonicalJSON()); got != want {
		t.Fatalf("CanonicalJSON = %s\nwant %s", got, want)
	}
	sum := sha256.Sum256([]byte(want))
	if got := snapshot.SHA256(); got != hex.EncodeToString(sum[:]) {
		t.Fatalf("SHA256 = %s, want the sha256 of the canonical JSON", got)
	}
	for name, edit := range map[string]func(*domain.LadoIssueSnapshot){
		"id":    func(s *domain.LadoIssueSnapshot) { s.ID = "github:acme/demo#13" },
		"title": func(s *domain.LadoIssueSnapshot) { s.Title += "!" },
		"body":  func(s *domain.LadoIssueSnapshot) { s.Body += " " },
	} {
		edited := snapshot
		edit(&edited)
		if edited.SHA256() == snapshot.SHA256() {
			t.Errorf("editing the %s kept the snapshot hash", name)
		}
	}
}

func TestSplitLadoTaskText(t *testing.T) {
	for _, tc := range []struct {
		name, text, title, body string
	}{
		{"title and body", "Add login\nDetails here\nMore", "Add login", "Details here\nMore"},
		{"crlf and blank lines", "  Add login  \r\n\r\n\r\nBody line\r\n  indented\r\n\r\n", "Add login", "Body line\n  indented"},
		{"leading blank lines", "\n\n  First real line\nrest", "First real line", "rest"},
		{"title only", "Only a title", "Only a title", ""},
		{"blank", "   \n \n", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			title, body := domain.SplitLadoTaskText(tc.text)
			if title != tc.title || body != tc.body {
				t.Fatalf("SplitLadoTaskText = (%q, %q), want (%q, %q)", title, body, tc.title, tc.body)
			}
		})
	}
}

func TestLadoTaskMarker(t *testing.T) {
	if got := domain.LadoTaskMarker("lado-task-1"); got != "<!-- lado-task:lado-task-1 -->" {
		t.Fatalf("LadoTaskMarker = %q", got)
	}
}

func TestLadoTaskStatePrecedence(t *testing.T) {
	cancelledAt := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	task := domain.LadoTask{IssueRef: "github:acme/demo#1", Issue: domain.LadoObservedIssue{State: domain.IssueOpen}}
	for _, tc := range []struct {
		name      string
		issue     domain.NormalizedIssueState
		cancelled bool
		want      domain.LadoTaskState
	}{
		{"open issue", domain.IssueOpen, false, domain.LadoTaskDraft},
		{"issue labelled in progress", domain.IssueInProgress, false, domain.LadoTaskDraft},
		{"issue closed", domain.IssueDone, false, domain.LadoTaskClosedUnmerged},
		{"issue closed as not planned", domain.IssueCancelled, false, domain.LadoTaskClosedUnmerged},
		{"cancelled task with a closed issue", domain.IssueDone, true, domain.LadoTaskCancelled},
		{"cancelled task with an open issue", domain.IssueOpen, true, domain.LadoTaskCancelled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := task
			got.Issue.State = tc.issue
			if tc.cancelled {
				got.CancelledAt = cancelledAt
			}
			if state := got.State(); state != tc.want {
				t.Fatalf("State = %q, want %q", state, tc.want)
			}
		})
	}
}

func TestLadoTrackerRepoFromOrigin(t *testing.T) {
	for _, tc := range []struct {
		name, origin string
		want         domain.TrackerRepo
	}{
		{"github scp", "git@github.com:Acme/Demo.git", domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "Acme/Demo"}},
		{"github ssh url", "ssh://git@github.com/acme/demo.git", domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "acme/demo"}},
		{"github https", "https://github.com/acme/demo", domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "acme/demo"}},
		{"gitlab.com nested", "git@gitlab.com:group/sub/project.git", domain.TrackerRepo{Provider: domain.TrackerProviderGitLab, Native: "group/sub/project"}},
		{"self-managed ssh port", "ssh://git@GitLab.Example.com:2222/group/project.git", domain.TrackerRepo{Provider: domain.TrackerProviderGitLab, Native: "group/project", Host: "gitlab.example.com"}},
		{"self-managed https", "https://gitlab.example.com/group/project", domain.TrackerRepo{Provider: domain.TrackerProviderGitLab, Native: "group/project", Host: "gitlab.example.com"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := domain.LadoTrackerRepoFromOrigin(tc.origin)
			if err != nil {
				t.Fatalf("LadoTrackerRepoFromOrigin: %v", err)
			}
			if got != tc.want {
				t.Fatalf("LadoTrackerRepoFromOrigin = %+v, want %+v", got, tc.want)
			}
		})
	}
	for _, origin := range []string{"", "not a url", "git@acme.ghe.io:acme/demo.git", "https://api.github.com/acme/demo"} {
		if got, err := domain.LadoTrackerRepoFromOrigin(origin); err == nil {
			t.Errorf("LadoTrackerRepoFromOrigin(%q) = %+v, want an error", origin, got)
		}
	}
}

func TestLadoSameRepo(t *testing.T) {
	a := domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "Acme/Demo"}
	if !domain.LadoSameRepo(a, domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "acme/demo"}) {
		t.Fatal("repositories differing only in case are the same repository")
	}
	if domain.LadoSameRepo(a, domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "acme/other"}) {
		t.Fatal("different repositories matched")
	}
	gitlab := domain.TrackerRepo{Provider: domain.TrackerProviderGitLab, Native: "acme/demo"}
	if domain.LadoSameRepo(a, gitlab) {
		t.Fatal("different providers matched")
	}
	if domain.LadoSameRepo(gitlab, domain.TrackerRepo{Provider: domain.TrackerProviderGitLab, Native: "acme/demo", Host: "gitlab.example.com"}) {
		t.Fatal("different GitLab hosts matched")
	}
}

func TestLadoRepoRefRoundTrips(t *testing.T) {
	for _, tc := range []struct {
		repo domain.TrackerRepo
		ref  string
	}{
		{domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "acme/back"}, "github:acme/back"},
		{domain.TrackerRepo{Provider: domain.TrackerProviderGitLab, Native: "group/sub/front"}, "gitlab:group/sub/front"},
		{domain.TrackerRepo{Provider: domain.TrackerProviderGitLab, Native: "group/back", Host: "gitlab.example.com"}, "gitlab:group/back@gitlab.example.com"},
	} {
		if got := domain.LadoRepoRef(tc.repo); got != tc.ref {
			t.Fatalf("LadoRepoRef(%+v) = %q, want %q", tc.repo, got, tc.ref)
		}
		parsed, err := domain.ParseLadoRepoRef(tc.ref)
		if err != nil || parsed != tc.repo {
			t.Fatalf("ParseLadoRepoRef(%q) = %+v, %v; want %+v", tc.ref, parsed, err, tc.repo)
		}
	}
	for _, ref := range []string{"", "acme/back", "github:acme/back#12", "github:acme", "jira:a/b", "github:acme/back@ghe.example.com"} {
		if _, err := domain.ParseLadoRepoRef(ref); err == nil {
			t.Errorf("ParseLadoRepoRef(%q) = nil error, want an error", ref)
		}
	}
}

func TestLadoIssueRepo(t *testing.T) {
	id := domain.TrackerID{Provider: domain.TrackerProviderGitLab, Native: "group/back#7", Host: "gitlab.example.com"}
	want := domain.TrackerRepo{Provider: domain.TrackerProviderGitLab, Native: "group/back", Host: "gitlab.example.com"}
	if got := domain.LadoIssueRepo(id); got != want {
		t.Fatalf("LadoIssueRepo = %+v, want %+v", got, want)
	}
}
