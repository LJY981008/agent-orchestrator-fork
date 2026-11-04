package review

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Once the SCM observer tracks a GitLab merge request on the worker (an ssh
// origin with a custom SSH port included), `ao review trigger` starts a pass
// on it instead of failing with REVIEW_INVALID, and the reviewer is told to
// hand its comments to AO.
func TestTriggerStartsRunForTrackedGitLabMergeRequest(t *testing.T) {
	store := &fakeStore{}
	launcher := &fakeLauncher{handle: "review-mer-1"}
	prs := fakePRs{prs: []domain.PullRequest{{
		URL: testGitLabMR1, HTMLURL: testGitLabMR1, Number: 1, HeadSHA: "glsha1",
		Provider: "gitlab", Host: "gitlab.example.com", Repo: "a/b/c",
	}}}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prs, fakeProjects{}, launcher)

	res, err := eng.TriggerWithOptions(context.Background(), "mer-1", TriggerOptions{Source: domain.ReviewTriggerAgent, PRURL: testGitLabMR1})
	if errors.Is(err, ErrInvalid) {
		t.Fatalf("Trigger returned REVIEW_INVALID for a tracked GitLab MR: %v", err)
	}
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if !res.Created || res.Run.PRURL != testGitLabMR1 || res.Run.TargetSHA != "glsha1" || res.Run.Status != domain.ReviewRunRunning {
		t.Fatalf("result = %+v, want a running run on the merge request head", res)
	}
	if len(store.runs) != 1 || !launcher.spawned {
		t.Fatalf("runs=%+v spawned=%v, want one recorded run and a reviewer", store.runs, launcher.spawned)
	}
	prompt, _ := reviewTexts(launcher.gotSpec)
	if strings.Contains(prompt, "gh api") || !strings.Contains(prompt, `"comments"`) {
		t.Fatalf("reviewer prompt for a GitLab MR must use submit comments:\n%s", prompt)
	}
}
