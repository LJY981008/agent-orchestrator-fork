package scm

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/scm/gitlab"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// gitlabParsedProvider serves fake GitLab responses but identifies origins with
// the real GitLab provider, so the observer sees the host the daemon would.
type gitlabParsedProvider struct {
	*fakeProvider
	parser *gitlab.Provider
}

func (p *gitlabParsedProvider) ParseRepository(remote string) (ports.SCMRepo, bool) {
	return p.parser.ParseRepository(remote)
}

func gitlabSSHObservation(repo ports.SCMRepo, number int, branch string) ports.SCMObservation {
	obs := testObs(number)
	obs.Provider, obs.Host, obs.Repo = repo.Provider, repo.Host, repo.Repo
	obs.PR.URL = "https://" + repo.Host + "/" + repo.Repo + "/-/merge_requests/1"
	obs.PR.HTMLURL = obs.PR.URL
	obs.PR.SourceBranch = branch
	obs.PR.HeadRepo = repo.Repo
	obs.PR.BaseRepo = repo.Repo
	return obs
}

func TestPoll_GitLabSSHPortOriginRecordsSessionMR(t *testing.T) {
	parser, err := gitlab.NewProvider(gitlab.ProviderOptions{
		Token:              gitlab.StaticTokenSource("tok"),
		SkipTokenPreflight: true,
		AllowedHosts:       []string{"host"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, origin, childOrigin, branch, source string
		kind                                      domain.ProjectKind
		repo                                      ports.SCMRepo
	}{
		{
			name:   "single repository",
			origin: "ssh://git@host:30022/a/b/c.git",
			branch: "ao/p-1/root", source: "ao/p-1/root",
			kind: domain.ProjectKindSingleRepo,
			repo: ports.SCMRepo{Provider: "gitlab", Host: "host", Owner: "a/b", Name: "c", Repo: "a/b/c"},
		},
		{
			name:        "workspace child repository",
			childOrigin: "ssh://git@host:30022/a/b/api.git",
			branch:      "ao/p-1-2", source: "ao/p-1-2-fix",
			kind: domain.ProjectKindWorkspace,
			repo: ports.SCMRepo{Provider: "gitlab", Host: "host", Owner: "a/b", Name: "api", Repo: "a/b/api"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := testStoreWithSession()
			store.sessions[0].Metadata.Branch = tc.branch
			store.projects["p"] = domain.ProjectRecord{ID: "p", Kind: tc.kind, RepoOriginURL: tc.origin}
			if tc.childOrigin != "" {
				store.workspaceRepos = map[string][]domain.WorkspaceRepoRecord{"p": {{Name: "api", RelativePath: "api", RepoOriginURL: tc.childOrigin}}}
			}
			mr := gitlabSSHObservation(tc.repo, 1, tc.source)
			fake := &fakeProvider{
				identity:     ports.SCMIdentity{Login: "alice", Human: true},
				repoGuards:   map[string]ports.SCMGuardResult{prKey(tc.repo, 0): {ETag: "v1"}},
				openPRs:      map[string][]ports.SCMPRObservation{prKey(tc.repo, 0): {mr.PR}},
				observations: map[string]ports.SCMObservation{prKey(tc.repo, 1): mr},
			}
			for i := range fake.openPRs[prKey(tc.repo, 0)] {
				fake.openPRs[prKey(tc.repo, 0)][i].Author = "alice"
			}
			provider := &gitlabParsedProvider{fakeProvider: fake, parser: parser}
			obs := New(provider, store, &fakeLifecycle{}, Config{Clock: func() time.Time { return time.Unix(1, 0).UTC() }, Tick: time.Hour, Logger: quietSlog(), CacheMax: 128, ScopedIdentityResolver: provider})
			if err := obs.Poll(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(store.writes) == 0 {
				t.Fatalf("MR from the ssh-port origin was not recorded (fetches %+v)", fake.fetchBatches)
			}
			for _, write := range store.writes {
				pr := write.pr
				if pr.SessionID != "p-1" || pr.Provider != "gitlab" || pr.Host != "host" || pr.Repo != tc.repo.Repo || pr.URL != mr.PR.URL {
					t.Fatalf("recorded PR = %+v, want session p-1 MR %s on host", pr, mr.PR.URL)
				}
			}
		})
	}
}
