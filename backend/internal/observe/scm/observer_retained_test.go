package scm

// These tests pin Config.RetainedSessions: a terminated session named by it
// (LADO: an execution not settled yet) keeps its open PRs refreshed, while
// other terminated sessions stay unobserved and no lifecycle reaction runs.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func retainedTestStore() *fakeStore {
	open := knownPR(1)
	open.SessionID = "lado-1"
	merged := knownPR(3)
	merged.SessionID = "lado-1"
	merged.Merged = true
	other := knownPR(2)
	other.SessionID = "other-1"
	return &fakeStore{
		sessions: []domain.SessionRecord{
			{ID: "lado-1", ProjectID: "p", IsTerminated: true, Metadata: domain.SessionMetadata{Branch: "lado/run"}},
			{ID: "other-1", ProjectID: "p", IsTerminated: true, Metadata: domain.SessionMetadata{Branch: "other"}},
		},
		projects: map[string]domain.ProjectRecord{"p": {ID: "p", RepoOriginURL: "https://github.com/o/r.git"}},
		prs: map[domain.SessionID][]domain.PullRequest{
			"lado-1":  {open, merged},
			"other-1": {other},
		},
		checks: map[string][]domain.PullRequestCheck{},
	}
}

func retainedConflictObs(num int) ports.SCMObservation {
	obs := testObs(num)
	obs.PR.HeadSHA = "sha-new"
	obs.CI.HeadSHA = "sha-new"
	obs.Mergeability = ports.SCMMergeabilityObservation{State: string(domain.MergeConflicting), Mergeable: false}
	return obs
}

func retainedProvider() *fakeProvider {
	return &fakeProvider{
		repoGuards: map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "v2"}},
		openPRs:    map[string][]ports.SCMPRObservation{},
		observations: map[string]ports.SCMObservation{
			prKey(testRepo, 1): retainedConflictObs(1),
			prKey(testRepo, 2): retainedConflictObs(2),
			prKey(testRepo, 3): retainedConflictObs(3),
		},
	}
}

func retainOnly(ids ...domain.SessionID) RetainedSessionsFunc {
	return func(context.Context) ([]domain.SessionID, error) { return ids, nil }
}

func retainedFetchCounts(p *fakeProvider) map[int]int {
	out := map[int]int{}
	for _, batch := range p.fetchBatches {
		for _, ref := range batch {
			out[ref.Number]++
		}
	}
	return out
}

func TestPoll_RetainedTerminatedSessionKeepsOpenPRObserved(t *testing.T) {
	store := retainedTestStore()
	provider := retainedProvider()
	lc := &fakeLifecycle{}
	obs := newTestObserver(store, provider, lc, time.Unix(1, 0).UTC())
	obs.retainedSessions = retainOnly("lado-1")

	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	fetched := retainedFetchCounts(provider)
	if fetched[1] != 1 {
		t.Fatalf("retained open PR #1 fetched %d times, want 1 (batches %#v)", fetched[1], provider.fetchBatches)
	}
	if fetched[2] != 0 {
		t.Fatalf("PR #2 of a terminated session that is not retained was fetched: %#v", provider.fetchBatches)
	}
	if fetched[3] != 0 {
		t.Fatalf("merged PR #3 of the retained session was fetched: %#v", provider.fetchBatches)
	}
	if len(lc.observed) != 0 {
		t.Fatalf("lifecycle ran for a terminated session: %#v", lc.observed)
	}
	if len(store.writes) != 1 {
		t.Fatalf("writes = %d, want one acknowledged write for PR #1", len(store.writes))
	}
	w := store.writes[0].pr
	if w.Number != 1 || w.SessionID != "lado-1" {
		t.Fatalf("written PR = #%d session %q, want #1 lado-1", w.Number, w.SessionID)
	}
	if w.HeadSHA != "sha-new" || w.Mergeability != domain.MergeConflicting {
		t.Fatalf("written PR head=%q mergeability=%q, want sha-new conflicting", w.HeadSHA, w.Mergeability)
	}
	if w.MetadataHash == "" || w.MetadataHash == knownPR(1).MetadataHash {
		t.Fatalf("metadata hash %q was not acknowledged to the new observation", w.MetadataHash)
	}

	// The acknowledged hash means an unchanged next observation writes nothing.
	store.prs["lado-1"][0] = w
	before := len(store.writes)
	obs.Cache.LastPRFetchAt = map[string]time.Time{}
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.writes) != before {
		t.Fatalf("unchanged retained observation was written again: %d writes", len(store.writes)-before)
	}
}

func TestPoll_RetainedSessionDoesNotAttachNewPRs(t *testing.T) {
	store := retainedTestStore()
	provider := retainedProvider()
	provider.openPRs[prKey(testRepo, 0)] = []ports.SCMPRObservation{
		{URL: "https://github.com/o/r/pull/5", Number: 5, SourceBranch: "lado/run", HeadRepo: "o/r", BaseRepo: "o/r", TargetBranch: "main", HeadSHA: "sha5"},
		{URL: "https://github.com/o/r/pull/6", Number: 6, SourceBranch: "lado/run/child", HeadRepo: "o/r", BaseRepo: "o/r", TargetBranch: "main", HeadSHA: "sha6"},
	}
	provider.observations[prKey(testRepo, 5)] = testObs(5)
	provider.observations[prKey(testRepo, 6)] = testObs(6)
	lc := &fakeLifecycle{}
	obs := newTestObserver(store, provider, lc, time.Unix(1, 0).UTC())
	obs.retainedSessions = retainOnly("lado-1")

	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, w := range store.writes {
		if w.pr.Number == 5 || w.pr.Number == 6 {
			t.Fatalf("new PR #%d was attached to terminated session %q", w.pr.Number, w.pr.SessionID)
		}
	}
	if retainedFetchCounts(provider)[1] != 1 {
		t.Fatalf("retained PR #1 not refreshed: %#v", provider.fetchBatches)
	}
}

func TestPoll_LiveSessionWinsSharedPRSubjectOverRetained(t *testing.T) {
	store := retainedTestStore()
	live := knownPR(1)
	live.SessionID = "live-1"
	store.sessions = append([]domain.SessionRecord{{ID: "live-1", ProjectID: "p", Metadata: domain.SessionMetadata{Branch: "feat"}}}, store.sessions...)
	// Retained session listed before the live one must still lose the PR.
	store.sessions[0], store.sessions[1] = store.sessions[1], store.sessions[0]
	store.prs["live-1"] = []domain.PullRequest{live}
	provider := retainedProvider()
	lc := &fakeLifecycle{}
	obs := newTestObserver(store, provider, lc, time.Unix(1, 0).UTC())
	obs.retainedSessions = retainOnly("lado-1")

	subjects, _, err := obs.discoverSubjects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	subj := subjects[keyForTrackedPR(live, testRepo)]
	if subj == nil || subj.session.ID != "live-1" || subj.retained {
		t.Fatalf("shared PR subject = %+v, want live-1 not retained", subj)
	}
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(lc.observed) != 1 || lc.observed[0].PR.Number != 1 {
		t.Fatalf("lifecycle observations = %#v, want PR #1 for the live session", lc.observed)
	}
}

func TestPoll_TerminatedSessionsSkippedWithoutRetention(t *testing.T) {
	for name, retained := range map[string]RetainedSessionsFunc{
		"nil":    nil,
		"failed": func(context.Context) ([]domain.SessionID, error) { return nil, errors.New("db down") },
	} {
		t.Run(name, func(t *testing.T) {
			store := retainedTestStore()
			provider := retainedProvider()
			lc := &fakeLifecycle{}
			obs := newTestObserver(store, provider, lc, time.Unix(1, 0).UTC())
			obs.retainedSessions = retained
			if err := obs.Poll(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(provider.fetchBatches) != 0 || len(store.writes) != 0 || len(lc.observed) != 0 {
				t.Fatalf("terminated sessions observed: fetches=%#v writes=%d lifecycle=%d", provider.fetchBatches, len(store.writes), len(lc.observed))
			}
		})
	}
}

func TestNewWiresRetainedSessionsFromConfig(t *testing.T) {
	called := 0
	obs := New(&fakeProvider{}, &fakeStore{}, nil, Config{RetainedSessions: func(context.Context) ([]domain.SessionID, error) {
		called++
		return []domain.SessionID{"s"}, nil
	}})
	if got := obs.retainedSessionIDs(context.Background()); len(got) != 1 || called != 1 {
		t.Fatalf("retained ids = %v (calls %d), want {s} from Config.RetainedSessions", got, called)
	}
}
