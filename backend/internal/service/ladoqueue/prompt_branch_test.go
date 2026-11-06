package ladoqueue

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// branchRules is what every worker prompt says about its session branch
// (#71): no new branch, and LADO's rule before AO's sibling-branch advice.
var branchRules = []string{
	"Do not make a new branch or switch to another one",
	"in a LADO run these instructions come first",
	"one opened from another branch stays untracked",
}

// A single worker reads its session branch when it starts, since AO names it
// at the spawn, and pushes and opens the pull request from it.
func TestWorkerPromptKeepsTheWorkerOnItsSessionBranch(t *testing.T) {
	for name, task := range map[string]domain.LadoTask{
		"github": {ID: "task-1", IssueRef: "github:acme/app#12", Repo: domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "acme/app"}},
		"gitlab": gitlabTask(t),
	} {
		prompt := workerPrompt(task, mrRunInput(mrPlanText), "main", false, nil)
		wantAll(t, name, prompt, append([]string{
			"read it with `git branch --show-current` before you change anything",
			"Commit on that branch, push it under the same name (`git push -u origin <that branch>`",
			"open the pull request from it",
		}, branchRules...), []string{"git -C <folder>"})
		// The branch comes before how to open the pull request, which pushes
		// the same branch, and before the plan.
		branch := strings.Index(prompt, "git branch --show-current")
		if push := strings.Index(prompt, "push the branch you work on"); task.Repo.Provider == domain.TrackerProviderGitHub && branch > push {
			t.Errorf("%s: the session branch follows the pull request paragraph:\n%s", name, prompt)
		}
		if push := strings.Index(prompt, "merge_request.create"); task.Repo.Provider == domain.TrackerProviderGitLab && branch > push {
			t.Errorf("%s: the session branch follows the merge request paragraph:\n%s", name, prompt)
		}
		if branch > strings.Index(prompt, "## Approved plan") {
			t.Errorf("%s: the session branch follows the plan:\n%s", name, prompt)
		}
	}
}

// A workspace worker reads the session branch in each child repository it
// changes, with or without the plan's repos list.
func TestWorkerPromptOfAWorkspaceReadsEachRepositorysSessionBranch(t *testing.T) {
	for name, plan := range map[string]string{"one repo": mrPlanText, "repos": mrMultiRepoPlanText} {
		prompt := workerPrompt(gitlabTask(t), mrRunInput(plan), "develop", true, nil)
		wantAll(t, name, prompt, append([]string{
			"each repository folder has its own session branch",
			"read its session branch with `git -C <folder> branch --show-current`",
			"`git -C <folder> push -u origin <that branch>`",
			"open that repository's pull request from it",
		}, branchRules...), []string{"Your session branch is the branch your worktree is on"})
	}
}

// The integration worker gets its branch by name, in one repository and in
// several, with the same rule against another branch.
func TestIntegrationPromptKeepsTheWorkerOnTheIntegrationBranch(t *testing.T) {
	run := domain.LadoPartRun{Branch: "ao/sess-9/root", BaseBranch: "main", HeadSHA: "def"}
	part := domain.LadoTaskPart{PartID: "a", Title: "A", Steps: []int{1}}
	github := domain.LadoTask{ID: "task-1", IssueRef: "github:acme/app#12", Repo: domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "acme/app"}}
	repos := []domain.LadoPartRunRepo{
		{Relative: "back", BaseBranch: "main", BaseSHA: "1", HeadSHA: "2"},
		{Relative: "front", BaseSHA: "3", HeadSHA: "3"},
	}

	for name, task := range map[string]domain.LadoTask{"github": github, "gitlab": gitlabTask(t)} {
		single := integrationPrompt(task, mrRunInput(mrPlanText), nil, run, []domain.LadoTaskPart{part}, nil)
		wantAll(t, name+" single-repo", single, append([]string{
			"Commit, push and open the pull request from the branch `ao/sess-9/root` only.",
			"LADO tracks only a pull request opened from `ao/sess-9/root`",
		}, branchRules...), []string{"git branch --show-current", "In each repository, commit"})

		multi := integrationPrompt(task, mrRunInput(mrMultiRepoPlanText), nil, run, []domain.LadoTaskPart{part}, repos)
		wantAll(t, name+" multi-repo", multi, append([]string{
			"In each repository, commit, push and open the pull request from the branch `ao/sess-9/root` only.",
			"LADO tracks only a pull request opened from `ao/sess-9/root`",
		}, branchRules...), []string{"git branch --show-current"})
		if strings.Index(multi, "In each repository, commit") > strings.Index(multi, "## Approved plan") {
			t.Errorf("%s: the branch rule follows the plan:\n%s", name, multi)
		}
	}
}

// Part and merge workers do not push, so their prompts carry no branch rule
// for a pull request.
func TestPartAndMergePromptsCarryNoPullRequestBranchRule(t *testing.T) {
	task := gitlabTask(t)
	part := domain.LadoTaskPart{PartID: "a", Title: "A", Steps: []int{1}, Branch: "lado/x/a"}
	for name, prompt := range map[string]string{
		"part":  partPrompt(task, mrRunInput(mrPlanText), part, []domain.LadoTaskPart{part}, "lado/x/a", nil),
		"merge": mergePrompt(task, mrRunInput(mrPlanText), part, "lado/x/a-merge", "", "abc"),
	} {
		wantAll(t, name, prompt, nil, branchRules)
	}
}
