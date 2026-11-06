package ladoqueue

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopolicy"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladotask"
)

// gitlabTask is a task from an issue of a self-managed GitLab project.
func gitlabTask(t *testing.T) domain.LadoTask {
	t.Helper()
	ref := domain.IssueID("gitlab:group/proj#7@gitlab.example.com")
	if id, err := domain.ParseLadoIssueRef(ref); err != nil || id.Provider != domain.TrackerProviderGitLab || id.Native != "group/proj#7" {
		t.Fatalf("ParseLadoIssueRef(%q) = %+v, %v", ref, id, err)
	}
	return domain.LadoTask{
		ID: "task-1", ProjectID: "proj-1", IssueRef: ref,
		Repo:  domain.TrackerRepo{Provider: domain.TrackerProviderGitLab, Native: "group/proj", Host: "gitlab.example.com"},
		Issue: domain.LadoObservedIssue{Title: "Open merge requests"},
	}
}

func mrRunInput(plan string) ladotask.RunInput {
	return ladotask.RunInput{
		PlanText: plan,
		Revision: domain.LadoPlanRevision{ID: "rev-1", Number: 1, SHA256: strings.Repeat("b", 64)},
	}
}

const mrPlanText = "---\ntask: task-1\n---\n\n# Plan\n"

const mrMultiRepoPlanText = "---\ntask: task-1\nrepos: [back, front]\n---\n\n# Plan\n"

// mrGuidance is what every GitLab prompt says about the push options.
var mrGuidance = []string{
	"which need no `glab`",
	"GitLab reads the options only from a push that updates the branch",
	"\"Everything up-to-date\"",
	"Git refuses a push option with a line break, so keep the title and the description on one line.",
	"Do not add `merge_request.merge_when_pipeline_succeeds` or `merge_request.auto_merge`",
	"If the push output shows no merge request URL (\"View merge request\"), run `ao report --needs-input",
	"do not make another branch for the merge request",
}

func wantAll(t *testing.T, name, prompt string, want, absent []string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(prompt, w) {
			t.Errorf("%s prompt lacks %q:\n%s", name, w, prompt)
		}
	}
	for _, a := range absent {
		if strings.Contains(prompt, a) {
			t.Errorf("%s prompt holds %q:\n%s", name, a, prompt)
		}
	}
}

// A worker of a GitLab task learns to open its merge request with push
// options against the base branch the dispatch resolved (#20).
func TestSpawnConfigTellsAGitLabWorkerHowToOpenTheMergeRequest(t *testing.T) {
	agent := ladopolicy.SnapshotAgent{Harness: "claude-code"}
	cfg := spawnConfig(gitlabTask(t), domain.LadoQueueEntry{ID: "entry-1"}, mrRunInput(mrPlanText), agent, "develop", false, nil)
	wantAll(t, "worker", cfg.Prompt, append([]string{
		"`git push -o merge_request.create -o merge_request.target=develop -o merge_request.title=\"<title>\" -o merge_request.description=\"Closes #7\" origin HEAD`",
	}, mrGuidance...), []string{"commit message rule", "gh` is not installed"})

	// The paragraph belongs to the lead, before the plan.
	if strings.Index(cfg.Prompt, "merge_request.create") > strings.Index(cfg.Prompt, "## Approved plan") {
		t.Errorf("the merge request paragraph follows the plan:\n%s", cfg.Prompt)
	}

	// Without a base branch the target option is left out.
	prompt := workerPrompt(gitlabTask(t), mrRunInput(mrPlanText), "", false, nil)
	wantAll(t, "no base", prompt, []string{"`git push -o merge_request.create -o merge_request.title="}, []string{"merge_request.target="})
}

// A task whose issue does not parse falls back to the task's repository.
func TestChangeRequestParagraphFallsBackToTheTaskRepo(t *testing.T) {
	task := gitlabTask(t)
	task.IssueRef = "not a ref"
	got := changeRequestParagraph(task, changeRequest{target: "main", refspec: "HEAD"})
	wantAll(t, "fallback", got, []string{"merge_request.target=main", "merge_request.description=\"<the issue reference>\""}, nil)

	task.Repo = domain.TrackerRepo{}
	got = changeRequestParagraph(task, changeRequest{target: "main", refspec: "HEAD"})
	wantAll(t, "no repo", got, []string{"gh` is not installed"}, []string{"merge_request."})
}

// A GitHub task's worker hears nothing of push options, and asks the
// developer for the pull request when gh cannot open it.
func TestWorkerPromptOfAGitHubTaskHasNoMergeRequestOptions(t *testing.T) {
	task := domain.LadoTask{ID: "task-1", IssueRef: "github:acme/app#12", Repo: domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "acme/app"}}
	in := mrRunInput(mrPlanText)
	run := domain.LadoPartRun{Branch: "lado/run", BaseBranch: "main", HeadSHA: "def"}
	part := domain.LadoTaskPart{PartID: "a", Title: "A", Steps: []int{1}}
	want := []string{"If `gh` is not installed or not logged in, push the branch you work on and run " +
		"`ao report --needs-input --note \"<branch> pushed; gh unavailable, please open the pull request\"`"}
	for name, prompt := range map[string]string{
		"worker":      workerPrompt(task, in, "main", false, nil),
		"integration": integrationPrompt(task, in, nil, run, []domain.LadoTaskPart{part}, nil),
	} {
		wantAll(t, name, prompt, want, []string{"merge_request.", "glab"})
	}
}

// A worker in several repositories does not know each one's base branch, so
// it leaves the target out, and names the issue in full so every repository's
// merge request links it.
func TestWorkerPromptInSeveralGitLabReposLeavesTheMergeRequestTargetOut(t *testing.T) {
	prompt := workerPrompt(gitlabTask(t), mrRunInput(mrMultiRepoPlanText), "develop", false, nil)
	wantAll(t, "multi-repo worker", prompt, append([]string{
		"`back/`, then `front/`",
		"-o merge_request.description=\"Closes group/proj#7\" origin HEAD`",
		"Leave the target option out, so the merge request targets the project's default branch.",
	}, mrGuidance...), []string{"merge_request.target="})
}

// The integration worker pushes the integration branch with the options: in
// one repository against the run's base branch, in several against each
// repository's own.
func TestIntegrationPromptTellsHowToOpenTheMergeRequest(t *testing.T) {
	task := gitlabTask(t)
	run := domain.LadoPartRun{Branch: "lado/abc/root", BaseBranch: "develop", HeadSHA: "def"}
	part := domain.LadoTaskPart{PartID: "a", Title: "A", Steps: []int{1}}

	single := integrationPrompt(task, mrRunInput(mrPlanText), nil, run, []domain.LadoTaskPart{part}, nil)
	wantAll(t, "single-repo integration", single, append([]string{
		"`git push -o merge_request.create -o merge_request.target=develop -o merge_request.title=\"<title>\" " +
			"-o merge_request.description=\"Closes #7\" origin HEAD:refs/heads/lado/abc/root`",
	}, mrGuidance...), []string{"Closes group/proj#7"})

	run.BaseBranch = ""
	noBase := integrationPrompt(task, mrRunInput(mrPlanText), nil, run, []domain.LadoTaskPart{part}, nil)
	wantAll(t, "no-base integration", noBase, []string{"origin HEAD:refs/heads/lado/abc/root`"}, []string{"merge_request.target="})

	run.BaseBranch = "develop"
	repos := []domain.LadoPartRunRepo{
		{Relative: "back", BaseBranch: "develop", BaseSHA: "1", HeadSHA: "2"},
		{Relative: "front", BaseSHA: "3", HeadSHA: "4"},
	}
	multi := integrationPrompt(task, mrRunInput(mrMultiRepoPlanText), nil, run, []domain.LadoTaskPart{part}, repos)
	wantAll(t, "multi-repo integration", multi, append([]string{
		"`back/` against `develop`, then `front/` against its default branch",
		"-o merge_request.target=<its base branch>",
		"-o merge_request.description=\"Closes group/proj#7\" origin HEAD:refs/heads/lado/abc/root`",
		"set the target to its base branch listed above, and leave the target option out where it says its default branch",
	}, mrGuidance...), nil)
	if strings.Index(multi, "merge_request.create") > strings.Index(multi, "## Approved plan") {
		t.Errorf("the merge request paragraph follows the plan:\n%s", multi)
	}
}
