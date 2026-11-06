package ladoqueue

import (
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// changeRequest says how a worker pushes and opens its pull request or merge
// request (#20).
type changeRequest struct {
	// target is the base branch the merge request targets; "" leaves the
	// option out, so GitLab targets the project's default branch.
	target string
	// targetPerRepo asks for each repository's base branch the prompt lists
	// above as the target, in place of target.
	targetPerRepo bool
	// refspec is what the worker pushes, such as "HEAD".
	refspec string
	// multiRepo links the issue by its full reference, so a merge request
	// in a repository other than the issue's links it too.
	multiRepo bool
}

// changeRequestParagraph tells a worker how to open its pull request on the
// provider of the task's issue, which is the provider of the repository
// LADO took the issue from. On GitLab it opens the merge request with push
// options, which need no glab; on GitHub it asks for the developer's help
// when gh cannot open the pull request. It ends with a line break.
func changeRequestParagraph(task domain.LadoTask, cr changeRequest) string {
	provider := task.Repo.Provider
	description := "<the issue reference>"
	if id, err := domain.ParseLadoIssueRef(task.IssueRef); err == nil {
		provider = id.Provider
		if n := domain.LadoIssueNumber(id); n > 0 {
			description = fmt.Sprintf("Closes #%d", n)
			if cr.multiRepo {
				description = "Closes " + id.Native
			}
		}
	}
	if provider != domain.TrackerProviderGitLab {
		return "If `gh` is not installed or not logged in, push the branch you work on and run " +
			"`ao report --needs-input --note \"<branch> pushed; gh unavailable, please open the pull request\"`: " +
			"a pull request the developer opens from that branch is tracked as this session's.\n"
	}
	target := ""
	switch {
	case cr.targetPerRepo:
		target = " -o merge_request.target=<its base branch>"
	case cr.target != "":
		target = " -o merge_request.target=" + cr.target
	}
	where := "The repository is on GitLab. Open the merge request"
	if cr.multiRepo {
		where = "The repositories are on GitLab. Open each merge request"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s with push options, which need no `glab`: "+
		"`git push -o merge_request.create%s -o merge_request.title=\"<title>\" -o merge_request.description=\"%s\" origin %s`. ",
		where, target, description, cr.refspec)
	switch {
	case cr.targetPerRepo:
		b.WriteString("In each repository, set the target to its base branch listed above, and leave the target option out where it says its default branch. ")
	case cr.target == "":
		b.WriteString("Leave the target option out, so the merge request targets the project's default branch. ")
	}
	if cr.multiRepo {
		b.WriteString("The description names the issue in full, so the merge request in every repository links it. ")
	}
	b.WriteString("Push from the branch you work on; do not make another branch for the merge request. ")
	b.WriteString("GitLab reads the options only from a push that updates the branch: when the remote branch is up to date already, git prints \"Everything up-to-date\" and no merge request opens, so add the options to the next push that carries a commit. ")
	b.WriteString("Git refuses a push option with a line break, so keep the title and the description on one line. ")
	b.WriteString("Do not add `merge_request.merge_when_pipeline_succeeds` or `merge_request.auto_merge`: the developer approves the merge in LADO. ")
	b.WriteString("If the push output shows no merge request URL (\"View merge request\"), run `ao report --needs-input --note \"<the push output>\"`. ")
	b.WriteString("If `glab` is installed and logged in, you may use it instead.\n")
	return b.String()
}
