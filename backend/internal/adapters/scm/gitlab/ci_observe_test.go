package gitlab

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// LADO fork (#21): tests for NoPipeline detection and for reading the real CI
// pipeline under an "external" (commit-status-only) pipeline.

const ciObserveHead = "abc123"

var ciObserveRepo = ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "myorg", Name: "myrepo", Repo: "myorg/myrepo"}

type ciObserveServer struct {
	mu            sync.Mutex
	mux           *http.ServeMux
	pipelines     func(perPage string) any
	versions      func() (int, any)
	versionCalls  atomic.Int32
	pipelineCalls map[string]*atomic.Int32
}

func newCIObserveServer(t *testing.T) (*ciObserveServer, *Provider) {
	t.Helper()
	s := &ciObserveServer{
		mux:           http.NewServeMux(),
		pipelines:     func(string) any { return []any{} },
		pipelineCalls: map[string]*atomic.Int32{"1": {}, "20": {}},
	}
	s.mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"iid": 1, "title": "Fix", "state": "opened",
			"web_url":       "https://gitlab.com/myorg/myrepo/-/merge_requests/1",
			"source_branch": "fix", "target_branch": "main", "sha": ciObserveHead,
			"detailed_merge_status": "mergeable", "author": map[string]any{"username": "alice"},
			"diff_refs": map[string]any{"base_sha": "base1", "head_sha": ciObserveHead},
		})
	})
	s.mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1/approvals", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"approved": false, "approvals_required": 0, "approved_by": []any{}})
	})
	s.mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/merge_requests/1/versions", func(w http.ResponseWriter, r *http.Request) {
		s.versionCalls.Add(1)
		if got := r.URL.Query().Get("per_page"); got != "1" {
			t.Errorf("versions per_page = %q, want 1", got)
		}
		status, body := http.StatusNotFound, any(map[string]any{"message": "404 Not Found"})
		s.mu.Lock()
		versions := s.versions
		s.mu.Unlock()
		if versions != nil {
			status, body = versions()
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	})
	s.mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/pipelines", func(w http.ResponseWriter, r *http.Request) {
		perPage := r.URL.Query().Get("per_page")
		if c := s.pipelineCalls[perPage]; c != nil {
			c.Add(1)
		}
		if r.URL.Query().Get("sha") != ciObserveHead {
			t.Errorf("pipelines sha = %q", r.URL.Query().Get("sha"))
		}
		s.mu.Lock()
		pipelines := s.pipelines
		s.mu.Unlock()
		_ = json.NewEncoder(w).Encode(pipelines(perPage))
	})
	_, p := testServer(t, s.mux)
	return s, p
}

func (s *ciObserveServer) setPipelines(f func(perPage string) any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pipelines = f
}

func (s *ciObserveServer) setVersions(f func() (int, any)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.versions = f
}

func (s *ciObserveServer) jobs(pipelineID string, jobs []map[string]any) {
	s.mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/pipelines/"+pipelineID+"/jobs", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(jobs)
	})
}

func versionAt(head string, at time.Time) func() (int, any) {
	return func() (int, any) {
		return http.StatusOK, []map[string]any{{"id": 9, "head_commit_sha": head, "created_at": at.Format(time.RFC3339)}}
	}
}

func fetchOne(t *testing.T, p *Provider) ports.SCMObservation {
	t.Helper()
	ref := ports.SCMPRRef{Repo: ciObserveRepo, Number: 1, URL: "https://gitlab.com/myorg/myrepo/-/merge_requests/1"}
	obs, err := p.FetchPullRequests(context.Background(), []ports.SCMPRRef{ref})
	if err != nil {
		t.Fatalf("FetchPullRequests: %v", err)
	}
	if len(obs) != 1 || !obs[0].Fetched {
		t.Fatalf("observation not fetched: %+v", obs)
	}
	return obs[0]
}

func fixClock(p *Provider, now time.Time) {
	p.cache.mu.Lock()
	p.cache.now = func() time.Time { return now }
	p.cache.mu.Unlock()
}

func TestFetchCI_NoPipeline(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		versions func() (int, any)
		want     bool
	}{
		{"head matches and grace elapsed", versionAt(ciObserveHead, now.Add(-gitlabNoPipelineGrace)), true},
		{"head matches long ago", versionAt(ciObserveHead, now.Add(-2*time.Hour)), true},
		{"grace not elapsed", versionAt(ciObserveHead, now.Add(-4*time.Minute)), false},
		{"head mismatch", versionAt("other999", now.Add(-time.Hour)), false},
		{"versions read fails", func() (int, any) { return http.StatusForbidden, map[string]any{"message": "403 Forbidden"} }, false},
		{"versions empty", func() (int, any) { return http.StatusOK, []any{} }, false},
		{"versions malformed", func() (int, any) { return http.StatusOK, map[string]any{"oops": true} }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, p := newCIObserveServer(t)
			s.setVersions(tt.versions)
			fixClock(p, now)
			obs := fetchOne(t, p)
			if obs.CI.NoPipeline != tt.want {
				t.Fatalf("NoPipeline = %v, want %v", obs.CI.NoPipeline, tt.want)
			}
			if obs.CI.Summary != string(domain.CIUnknown) {
				t.Fatalf("Summary = %q, want unknown", obs.CI.Summary)
			}
			if obs.CI.HeadSHA != ciObserveHead || len(obs.CI.Checks) != 0 {
				t.Fatalf("CI = %+v", obs.CI)
			}
			if got := s.versionCalls.Load(); got != 1 {
				t.Fatalf("versions calls = %d, want 1", got)
			}
		})
	}
}

func TestFetchCI_SkippedPipelineIsAPipeline(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	s, p := newCIObserveServer(t)
	s.setVersions(versionAt(ciObserveHead, now.Add(-time.Hour)))
	s.setPipelines(func(string) any {
		return []map[string]any{{"id": 7, "status": "skipped", "sha": ciObserveHead, "source": "push"}}
	})
	s.jobs("7", []map[string]any{{"id": 70, "name": "build", "status": "skipped"}})
	fixClock(p, now)
	obs := fetchOne(t, p)
	if obs.CI.NoPipeline {
		t.Fatal("NoPipeline = true for a skipped pipeline")
	}
	if obs.CI.Summary != string(domain.CIUnknown) {
		t.Fatalf("Summary = %q, want unknown", obs.CI.Summary)
	}
	if got := s.versionCalls.Load(); got != 0 {
		t.Fatalf("versions calls = %d, want 0", got)
	}
}

func TestFetchCI_NoPipelineVersionCachedPerHead(t *testing.T) {
	start := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	s, p := newCIObserveServer(t)
	s.setVersions(versionAt(ciObserveHead, start.Add(-time.Minute)))
	fixClock(p, start)
	if obs := fetchOne(t, p); obs.CI.NoPipeline {
		t.Fatal("NoPipeline = true before the grace period")
	}
	fixClock(p, start.Add(gitlabNoPipelineGrace))
	if obs := fetchOne(t, p); !obs.CI.NoPipeline {
		t.Fatal("NoPipeline = false after the grace period")
	}
	if got := s.versionCalls.Load(); got != 1 {
		t.Fatalf("versions calls = %d, want 1 (cached per head)", got)
	}
}

func TestFetchCI_HeadMismatchIsNotCached(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	s, p := newCIObserveServer(t)
	s.setVersions(versionAt("other999", now.Add(-time.Hour)))
	fixClock(p, now)
	fetchOne(t, p)
	s.setVersions(versionAt(ciObserveHead, now.Add(-time.Hour)))
	if obs := fetchOne(t, p); !obs.CI.NoPipeline {
		t.Fatal("NoPipeline = false once the newest version matches the head")
	}
	if got := s.versionCalls.Load(); got != 2 {
		t.Fatalf("versions calls = %d, want 2", got)
	}
}

func TestFetchCI_ExternalPipelineDoesNotHideRealCI(t *testing.T) {
	s, p := newCIObserveServer(t)
	s.setPipelines(func(perPage string) any {
		external := map[string]any{"id": 12, "status": "success", "sha": ciObserveHead, "source": "external"}
		if perPage == "1" {
			return []map[string]any{external}
		}
		return []map[string]any{
			external,
			{"id": 11, "status": "failed", "sha": ciObserveHead, "source": "merge_request_event"},
			{"id": 10, "status": "success", "sha": ciObserveHead, "source": "push"},
		}
	})
	s.jobs("12", []map[string]any{})
	s.jobs("11", []map[string]any{
		{"id": 110, "name": "test", "status": "failed", "web_url": "https://gitlab.com/jobs/110"},
		{"id": 111, "name": "lint", "status": "success"},
	})
	obs := fetchOne(t, p)
	if obs.CI.Summary != string(domain.CIFailing) {
		t.Fatalf("Summary = %q, want failing", obs.CI.Summary)
	}
	if len(obs.CI.FailedChecks) != 1 || obs.CI.FailedChecks[0].Name != "test" || obs.CI.FailedChecks[0].ProviderID != "110" {
		t.Fatalf("FailedChecks = %+v, want the MR pipeline's failed job", obs.CI.FailedChecks)
	}
	if len(obs.CI.Checks) != 2 {
		t.Fatalf("Checks = %+v, want the MR pipeline's jobs", obs.CI.Checks)
	}
	if obs.CI.FailedFingerprint == "" {
		t.Fatal("FailedFingerprint empty")
	}
	if obs.CI.NoPipeline || s.versionCalls.Load() != 0 {
		t.Fatal("pipelines exist; versions must not be read")
	}
	if got := s.pipelineCalls["20"].Load(); got != 1 {
		t.Fatalf("lookback pipeline reads = %d, want 1", got)
	}
}

func TestFetchCI_ExternalPipelineRollsUpToWorse(t *testing.T) {
	tests := []struct {
		external, ci string
		want         domain.CIState
	}{
		{"pending", "success", domain.CIPending},  // LADO pending before approval
		{"success", "success", domain.CIPassing},  // approved
		{"success", "running", domain.CIPending},  // CI still running
		{"failed", "success", domain.CIFailing},   // another tool failed
		{"pending", "failed", domain.CIFailing},   // CI failure wins
		{"success", "skipped", domain.CIUnknown},  // unknown beats passing
		{"pending", "manual", domain.CIPending},   // pending beats unknown
		{"canceled", "pending", domain.CIFailing}, // failing beats pending
	}
	for _, tt := range tests {
		t.Run(tt.external+"+"+tt.ci, func(t *testing.T) {
			s, p := newCIObserveServer(t)
			s.setPipelines(func(perPage string) any {
				external := map[string]any{"id": 12, "status": tt.external, "sha": ciObserveHead, "source": "external"}
				if perPage == "1" {
					return []map[string]any{external}
				}
				return []map[string]any{external, {"id": 11, "status": tt.ci, "sha": ciObserveHead, "source": "push"}}
			})
			s.jobs("11", []map[string]any{{"id": 110, "name": "build", "status": "success"}})
			if got := fetchOne(t, p).CI.Summary; got != string(tt.want) {
				t.Fatalf("Summary = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFetchCI_OnlyExternalPipeline(t *testing.T) {
	s, p := newCIObserveServer(t)
	s.setPipelines(func(perPage string) any {
		return []map[string]any{{"id": 12, "status": "pending", "sha": ciObserveHead, "source": "external"}}
	})
	var externalJobs atomic.Int32
	s.mux.HandleFunc("/api/v4/projects/myorg%2Fmyrepo/pipelines/12/jobs", func(w http.ResponseWriter, r *http.Request) {
		externalJobs.Add(1)
		_ = json.NewEncoder(w).Encode([]any{})
	})
	obs := fetchOne(t, p)
	if obs.CI.Summary != string(domain.CIPending) {
		t.Fatalf("Summary = %q, want pending", obs.CI.Summary)
	}
	if externalJobs.Load() != 1 {
		t.Fatal("expected the external pipeline's jobs to be read when no CI pipeline exists")
	}
	if obs.CI.NoPipeline {
		t.Fatal("NoPipeline = true with an external pipeline")
	}
}

func TestFetchCI_NonExternalLatestSkipsLookback(t *testing.T) {
	s, p := newCIObserveServer(t)
	s.setPipelines(func(string) any {
		return []map[string]any{{"id": 11, "status": "success", "sha": ciObserveHead, "source": "merge_request_event"}}
	})
	s.jobs("11", []map[string]any{{"id": 110, "name": "build", "status": "success"}})
	if got := fetchOne(t, p).CI.Summary; got != string(domain.CIPassing) {
		t.Fatalf("Summary = %q, want passing", got)
	}
	if got := s.pipelineCalls["20"].Load(); got != 0 {
		t.Fatalf("lookback pipeline reads = %d, want 0", got)
	}
}

func TestCommitChecksGuard_ExternalLatestWatchesRealCI(t *testing.T) {
	var ciStatus atomic.Value
	ciStatus.Store("running")
	s, p := newCIObserveServer(t)
	s.setPipelines(func(perPage string) any {
		external := map[string]any{"id": 12, "status": "pending", "sha": ciObserveHead, "source": "external"}
		if perPage == "1" {
			return []map[string]any{external}
		}
		return []map[string]any{external, {"id": 11, "status": ciStatus.Load(), "sha": ciObserveHead, "source": "merge_request_event"}}
	})
	ctx := context.Background()
	first, err := p.CommitChecksGuard(ctx, ciObserveRepo, ciObserveHead, "")
	if err != nil {
		t.Fatal(err)
	}
	same, err := p.CommitChecksGuard(ctx, ciObserveRepo, ciObserveHead, first.ETag)
	if err != nil {
		t.Fatal(err)
	}
	if !same.NotModified {
		t.Fatal("guard changed with nothing changed")
	}
	ciStatus.Store("failed")
	changed, err := p.CommitChecksGuard(ctx, ciObserveRepo, ciObserveHead, first.ETag)
	if err != nil {
		t.Fatal(err)
	}
	if changed.NotModified || changed.ETag == first.ETag {
		t.Fatal("guard did not change when the CI pipeline under the external pipeline changed")
	}
}

func TestCommitChecksGuard_NonExternalSingleRead(t *testing.T) {
	s, p := newCIObserveServer(t)
	s.setPipelines(func(string) any {
		return []map[string]any{{"id": 11, "status": "running", "sha": ciObserveHead, "source": "push"}}
	})
	if _, err := p.CommitChecksGuard(context.Background(), ciObserveRepo, ciObserveHead, ""); err != nil {
		t.Fatal(err)
	}
	if got := s.pipelineCalls["20"].Load(); got != 0 {
		t.Fatalf("lookback pipeline reads = %d, want 0", got)
	}
}

func TestMRVersionCacheBounded(t *testing.T) {
	c := newCache()
	base := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	tick := base
	c.now = func() time.Time { return tick }
	for i := 0; i < mrVersionCacheLimit+10; i++ {
		tick = base.Add(time.Duration(i) * time.Second)
		c.setMRVersion("gitlab.com", "o/r", i+1, "head", base)
	}
	if got := len(c.mrVersion); got != mrVersionCacheLimit {
		t.Fatalf("cache size = %d, want %d", got, mrVersionCacheLimit)
	}
	if _, ok := c.getMRVersion("gitlab.com", "o/r", 1, "head"); ok {
		t.Fatal("oldest entry was not evicted")
	}
	if _, ok := c.getMRVersion("GitLab.com", "O/R", mrVersionCacheLimit+10, "HEAD"); !ok {
		t.Fatal("newest entry missing (keys must be case-insensitive)")
	}
}
