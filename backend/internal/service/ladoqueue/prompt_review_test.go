package ladoqueue

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// reviewThreadGuidance is what every prompt of a worker that owns a pull
// request says about its review threads (#85).
var reviewThreadGuidance = []string{
	"`ao pr resolve-comments <PR number> <thread id...>`",
	"on GitHub and GitLab alike",
	"and no token",
	"Name only the thread ids you addressed: without ids the command resolves every open thread.",
	"Do not resolve a thread you did not address; leave it open and report why with `ao report --checkpoint",
	"do not try another tool: run `ao report --needs-input --note \"<PR URL> <thread id...>: <the error>\"`",
	"LADO reminds you once",
}

// The worker of a single or several-repository plan, and the integration
// worker of a run in one or several repositories, all learn to resolve the
// threads they addressed with ao, before the plan, naming the provider tool
// of their own task only.
func TestOwnerPromptsTellHowToResolveReviewThreads(t *testing.T) {
	github := domain.LadoTask{ID: "task-1", IssueRef: "github:acme/app#12", Repo: domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "acme/app"}}
	gitlab := gitlabTask(t)
	run := domain.LadoPartRun{Branch: "lado/abc/root", BaseBranch: "main", HeadSHA: "def"}
	part := domain.LadoTaskPart{PartID: "a", Title: "A", Steps: []int{1}}
	repos := []domain.LadoPartRunRepo{
		{Relative: "back", BaseBranch: "main", BaseSHA: "1", HeadSHA: "2"},
		{Relative: "front", BaseSHA: "3", HeadSHA: "4"},
	}
	for _, tc := range []struct {
		task   domain.LadoTask
		tool   string
		absent string
	}{
		{github, "needs no `gh` and no token", "glab"},
		{gitlab, "needs no `glab` and no token", "needs no `gh`"},
	} {
		for name, prompt := range map[string]string{
			"worker":             workerPrompt(tc.task, mrRunInput(mrPlanText), "main", false, nil),
			"repos worker":       workerPrompt(tc.task, mrRunInput(mrMultiRepoPlanText), "main", true, nil),
			"single integration": integrationPrompt(tc.task, mrRunInput(mrPlanText), nil, run, []domain.LadoTaskPart{part}, nil),
			"multi integration":  integrationPrompt(tc.task, mrRunInput(mrMultiRepoPlanText), nil, run, []domain.LadoTaskPart{part}, repos),
		} {
			name = string(tc.task.Repo.Provider) + " " + name
			wantAll(t, name, prompt, append([]string{tc.tool}, reviewThreadGuidance...), []string{tc.absent})
			if strings.Count(prompt, "ao pr resolve-comments") != 1 {
				t.Errorf("%s prompt names the command %d times, want once:\n%s", name, strings.Count(prompt, "ao pr resolve-comments"), prompt)
			}
			if strings.Index(prompt, "ao pr resolve-comments") > strings.Index(prompt, "## Approved plan") {
				t.Errorf("%s: the review thread paragraph follows the plan:\n%s", name, prompt)
			}
		}
	}
}

// A resolve worker owns no pull request: it leaves the review threads alone
// and hears nothing of resolving them.
func TestResolvePromptLeavesReviewThreadsAlone(t *testing.T) {
	task := domain.LadoTask{ID: "task-1", IssueRef: "github:acme/app#12", Repo: domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "acme/app"}}
	c := domain.LadoResolveCandidate{PRSourceBranch: "feature"}
	c.Execution.PR.URL = "https://github.com/acme/app/pull/3"
	c.Execution.PR.Mergeability = domain.MergeConflicting
	repo := resolveRepo{task: task, remote: "origin", baseBranch: "main"}
	prompt := resolvePrompt(task, mrRunInput(mrPlanText), nil, c, repo, "lado/resolve", "abc", "def")
	wantAll(t, "resolve", prompt,
		[]string{"- Do not resolve or reply to the pull request's review threads: the worker that owns the pull request does that.\n"},
		[]string{"ao pr resolve-comments", "LADO reminds you once"})
}
