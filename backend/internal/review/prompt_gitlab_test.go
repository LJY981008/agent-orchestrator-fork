package review

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	testGitLabMR1 = "https://gitlab.example.com/a/b/c/-/merge_requests/1"
	testGitLabMR2 = "https://gitlab.example.com/a/b/c/-/merge_requests/2"
)

func TestGitLabOnlyQueuePromptsUseSubmitComments(t *testing.T) {
	for name, spec := range map[string]LaunchSpec{
		"single": {WorkerID: "mer-1", PRURL: testGitLabMR1, TargetSHA: "sha1", RunID: "run-1"},
		"queue": {
			WorkerID: "mer-1", PRURL: testGitLabMR2, TargetSHA: "sha2", RunID: "run-2", ReviewIndex: 1,
			ReviewQueue: []ports.ReviewTask{
				{RunID: "run-1", PRURL: testGitLabMR1, TargetSHA: "sha1"},
				{RunID: "run-2", PRURL: testGitLabMR2, TargetSHA: "sha2"},
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			prompt, system := reviewTexts(spec)
			for _, text := range []string{prompt, system} {
				if strings.Contains(text, "gh api") || strings.Contains(text, "gh pr review") {
					t.Fatalf("GitLab prompt must not tell the reviewer to post with gh:\n%s", text)
				}
			}
			for _, want := range []string{
				"Do not post anything on GitLab yourself",
				`"comments": [ { "path": "<file>", "line": <n>, "body": "<finding>" } ]`,
				"ao review submit --session mer-1 --reviews -",
				"The worker receives only these comments, never the summary",
				"Omit \"comments\" only when nothing needs to change.",
				"do not use a heredoc",
				testGitLabMR1,
			} {
				if !strings.Contains(prompt, want) {
					t.Fatalf("GitLab prompt missing %q:\n%s", want, prompt)
				}
			}
			if strings.Contains(prompt, "githubReviewId") {
				t.Fatalf("GitLab prompt must not ask for a GitHub review id:\n%s", prompt)
			}
			if !strings.Contains(system, "AO posts the comments of your `ao review submit` on the merge request") {
				t.Fatalf("GitLab system prompt must say AO posts the comments:\n%s", system)
			}
			if strings.Contains(system, "Post your review as a comment on the pull request") {
				t.Fatalf("GitLab system prompt still tells the reviewer to post:\n%s", system)
			}
		})
	}
}

func TestGitHubQueuePromptIsUnchangedByGitLabSupport(t *testing.T) {
	prompt, system := reviewTexts(LaunchSpec{WorkerID: "mer-1", PRURL: "https://github.com/o/r/pull/1", TargetSHA: "sha1", RunID: "run-1"})
	if system != reviewSystemPrompt() {
		t.Fatalf("GitHub system prompt changed:\n%s", system)
	}
	for _, want := range []string{
		"gh api --method POST repos/{owner}/{repo}/pulls/{number}/reviews --input - --jq '.id'",
		`"githubReviewId": "<id-from-step-1-or-empty>"`,
		"* 1. https://github.com/o/r/pull/1 (head commit sha1, run run-1)\n",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("GitHub prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, unwanted := range []string{"GitLab", "merge request", gitLabQueueNote} {
		if strings.Contains(prompt, unwanted) || strings.Contains(system, unwanted) {
			t.Fatalf("GitHub prompt mentions %q:\n%s\n%s", unwanted, prompt, system)
		}
	}
}

func TestMixedQueuePromptMarksEachGitLabMergeRequest(t *testing.T) {
	spec := LaunchSpec{
		WorkerID: "mer-1", PRURL: testGitLabMR1, TargetSHA: "sha2", RunID: "run-2", ReviewIndex: 1,
		ReviewQueue: []ports.ReviewTask{
			{RunID: "run-1", PRURL: "https://github.com/o/r/pull/1", TargetSHA: "sha1"},
			{RunID: "run-2", PRURL: testGitLabMR1, TargetSHA: "sha2"},
		},
	}
	prompt, system := reviewTexts(spec)
	for _, want := range []string{
		"* 1. https://github.com/o/r/pull/1 (head commit sha1, run run-1)\n",
		"* 2. " + testGitLabMR1 + " (head commit sha2, run run-2)" + gitLabQueueNote + "\n",
		"gh api --method POST repos/{owner}/{repo}/pulls/{number}/reviews",
		"Step 1 is for GitHub pull requests only",
		`"comments": [ { "path": "<file>", "line": <n>, "body": "<finding>" } ]`,
		`{ "runId": "<github-run-id>", "verdict": "<approved|changes_requested>", "githubReviewId": "<id-from-step-1-or-empty>", "body": "<your full review markdown>" }`,
		`{ "runId": "<gitlab-run-id>", "verdict": "<approved|changes_requested>", "body": "<your full review markdown>", "comments": [`,
		"| ao review submit --session mer-1 --reviews -",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("mixed prompt missing %q:\n%s", want, prompt)
		}
	}
	if !strings.Contains(system, "For GitHub pull requests, post your review") || !strings.Contains(system, "For GitLab merge requests, do not post on GitLab yourself") {
		t.Fatalf("mixed system prompt must cover both providers:\n%s", system)
	}
}
