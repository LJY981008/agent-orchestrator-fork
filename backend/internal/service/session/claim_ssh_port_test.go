package session

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/scm/gitlab"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// gitlabParserSCM records fetches like claimTargetSCM but identifies origins
// with the real GitLab provider, as the daemon's SCM provider does.
type gitlabParserSCM struct {
	claimTargetSCM
	parser *gitlab.Provider
}

func (f *gitlabParserSCM) ParseRepository(remote string) (ports.SCMRepo, bool) {
	return f.parser.ParseRepository(remote)
}

func newGitLabParserSCM(t *testing.T, allowed ...string) *gitlabParserSCM {
	t.Helper()
	parser, err := gitlab.NewProvider(gitlab.ProviderOptions{
		Token:              gitlab.StaticTokenSource("tok"),
		SkipTokenPreflight: true,
		AllowedHosts:       allowed,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &gitlabParserSCM{parser: parser}
}

func TestClaimPRGitLabSSHPortOrigin(t *testing.T) {
	const origin = "ssh://git@host:30022/a/b/c.git"
	const mrURL = "https://host/a/b/c/-/merge_requests/7"
	for _, tc := range []struct {
		name, ref, canonical string
		wantErr              error
	}{
		{name: "number", ref: "7"},
		{name: "hash number", ref: "#7"},
		{name: "full MR URL", ref: mrURL},
		{name: "number with canonical upstream", ref: "7", canonical: "https://host/a/b/c"},
		{name: "MR on SSH port host is not the origin", ref: "https://host:30022/a/b/c/-/merge_requests/7", wantErr: ErrProjectMismatch},
		{name: "other repository", ref: "https://host/a/b/other/-/merge_requests/7", wantErr: ErrProjectMismatch},
		{name: "other host", ref: "https://other/a/b/c/-/merge_requests/7", wantErr: ErrProjectMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newFakeStore()
			st.sessions["demo-1"] = domain.SessionRecord{ID: "demo-1", ProjectID: "demo", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{WorkspacePath: "/ws"}}
			st.projects["demo"] = domain.ProjectRecord{ID: "demo", RepoOriginURL: origin, Config: domain.ProjectConfig{CanonicalRepoURL: tc.canonical}}
			scm, claimer := newGitLabParserSCM(t, "host"), &fakePRClaimer{}
			svc := NewWithDeps(Deps{Store: st, SCM: scm, PRClaimer: claimer})
			_, err := svc.ClaimPR(context.Background(), "demo-1", tc.ref, ClaimPROptions{})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("ClaimPR(%q) error = %v, want %v", tc.ref, err, tc.wantErr)
			}
			if tc.wantErr != nil {
				if len(scm.refs) != 0 || claimer.called {
					t.Fatal("rejected claim reached SCM or storage")
				}
				return
			}
			if len(scm.refs) != 1 {
				t.Fatalf("SCM fetches = %d, want 1", len(scm.refs))
			}
			ref := scm.refs[0]
			if ref.URL != mrURL || ref.Number != 7 {
				t.Fatalf("SCM asked for %q #%d, want %q #7", ref.URL, ref.Number, mrURL)
			}
			if ref.Repo.Provider != "gitlab" || ref.Repo.Host != "host" || ref.Repo.Repo != "a/b/c" {
				t.Fatalf("SCM repo = %+v, want gitlab host a/b/c", ref.Repo)
			}
			if claimer.gotPR.URL != mrURL || claimer.gotPR.Host != "host" {
				t.Fatalf("claimed PR = %q on %q, want %q on host", claimer.gotPR.URL, claimer.gotPR.Host, mrURL)
			}
		})
	}
}

func TestClaimPRGitLabSSHPortWorkspaceChild(t *testing.T) {
	st := &workspaceClaimStore{fakeStore: newFakeStore(), repos: []domain.WorkspaceRepoRecord{
		{RepoOriginURL: "ssh://git@host:30022/a/b/api.git"},
	}}
	st.projects["ws"] = domain.ProjectRecord{ID: "ws", Kind: domain.ProjectKindWorkspace}
	st.sessions["ws-1"] = domain.SessionRecord{ID: "ws-1", ProjectID: "ws", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{WorkspacePath: "/ws"}}
	scm := newGitLabParserSCM(t, "host")
	svc := NewWithDeps(Deps{Store: st, SCM: scm, PRClaimer: &fakePRClaimer{}})
	const mrURL = "https://host/a/b/api/-/merge_requests/3"
	if _, err := svc.ClaimPR(context.Background(), "ws-1", mrURL, ClaimPROptions{}); err != nil {
		t.Fatalf("ClaimPR(%q) = %v", mrURL, err)
	}
	if len(scm.refs) != 1 || scm.refs[0].URL != mrURL || scm.refs[0].Repo.Host != "host" {
		t.Fatalf("SCM refs = %+v, want %s on host", scm.refs, mrURL)
	}
}
