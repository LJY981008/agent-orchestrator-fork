package ladoqueue

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopolicy"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladotask"
)

// workerPrompt is the task a dispatched worker starts with (decision n of the
// step 6 plan): a short lead that names the issue and asks to keep to the
// plan, then the approved plan file, front matter and body, unchanged, and
// last what LADO verifies on the pull request and the evidence file it asks
// for (decision k of the step 8 plan). AO appends the pinned issue, which
// the spawn passes as its issue context, and the worker's system prompt
// carries the snapshot's rule text. base is the branch the dispatch resolved
// as the task's base, which a GitLab merge request targets (#20). workspace
// says the task's project is a workspace, whose child repositories each have
// their own session branch (#71).
func workerPrompt(task domain.LadoTask, in ladotask.RunInput, base string, workspace bool, checks []domain.LadoCheckCommand) string {
	example := ""
	if id, err := domain.ParseLadoIssueRef(task.IssueRef); err == nil {
		if n := domain.LadoIssueNumber(id); n > 0 {
			example = fmt.Sprintf(" (for example \"Closes #%d\")", n)
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "You are implementing a plan the developer approved in LADO for %s: %s.\n", task.IssueRef, oneLine(task.Issue.Title))
	fmt.Fprintf(&b, "Follow the approved plan below and keep to its scope. When you open the pull request, link the issue in its description%s.\n", example)
	cr := changeRequest{target: base, refspec: "HEAD"}
	if plan, _ := domain.ParseLadoPlan([]byte(in.PlanText), domain.LadoPlanExpectations{}); len(plan.Repos) > 0 {
		b.WriteString(reposParagraph(plan.Repos))
		// The prompt does not know each repository's base branch.
		cr = changeRequest{refspec: "HEAD", multiRepo: true}
	}
	b.WriteString(sessionBranchParagraph(sessionBranch{workspace: workspace}))
	b.WriteString(changeRequestParagraph(task, cr))
	b.WriteString(reviewThreadsParagraph(task))
	b.WriteString("\n")
	fmt.Fprintf(&b, "## Approved plan (revision %d, sha256 %s)\n\n", in.Revision.Number, shortSHA(in.Revision.SHA256))
	b.WriteString(in.PlanText)
	if !strings.HasSuffix(in.PlanText, "\n") {
		b.WriteString("\n")
	}
	b.WriteString(planFileNote(in))
	b.WriteString("\n## Verification and evidence\n\n")
	b.WriteString(verificationParagraph(in.Snapshot, checks))
	b.WriteString("\n\n" + evidenceForm)
	return b.String()
}

// reviewThreadsParagraph tells a worker that owns a pull request how to
// close the review threads it addressed (#85): with `ao pr resolve-comments`,
// which the AO daemon runs on GitHub and GitLab alike, naming only the
// threads it addressed, since the command without ids resolves every open
// thread. A thread it leaves is reported, not resolved, and a failure is
// handed to the developer rather than retried with another tool. The
// provider tool it names is the task's own, so a GitHub prompt does not talk
// of glab. It ends with a line break.
func reviewThreadsParagraph(task domain.LadoTask) string {
	provider := task.Repo.Provider
	if id, err := domain.ParseLadoIssueRef(task.IssueRef); err == nil {
		provider = id.Provider
	}
	tool := "`gh`"
	if provider == domain.TrackerProviderGitLab {
		tool = "`glab`"
	}
	return "When review comments arrive on your pull request, address each one and push. " +
		"Then resolve every thread you addressed with `ao pr resolve-comments <PR number> <thread id...>`: " +
		"the AO daemon resolves the threads on GitHub and GitLab alike, so it needs no " + tool + " and no token. " +
		"Name only the thread ids you addressed: without ids the command resolves every open thread. " +
		"Do not resolve a thread you did not address; leave it open and report why with `ao report --checkpoint --note \"<thread id>: <why>\"`. " +
		"If the command fails (for example because AO tracks the same number in several repositories), do not try another tool: " +
		"run `ao report --needs-input --note \"<PR URL> <thread id...>: <the error>\"`. " +
		"An open review thread keeps the pull request from being ready to merge, and LADO reminds you once when one is still open after you push and stop or report done.\n"
}

// reposParagraph tells a worker whose plan names the workspace repositories
// it changes (#64) to open one pull request in each it changes, in the
// plan's order, which is the order they merge in, and to say in its done
// report which it had nothing to change in: LADO waits for a pull request in
// each until the worker reports done.
func reposParagraph(repos []string) string {
	folders := make([]string, len(repos))
	for i, repo := range repos {
		folders[i] = "`" + strings.Trim(repo, "/") + "/`"
	}
	return fmt.Sprintf("The plan changes the workspace repositories %s, in this order. "+
		"In each of them that you change, open one pull request, in that order, which is the order they merge in, and link the issue in each. "+
		"A repository you have nothing to change in gets no pull request: say so in your done report. "+
		"Open every pull request before you run `ao report --done`.\n", strings.Join(folders, ", then "))
}

// planFileNote names the read-only copy of the approved plan and asks the
// worker to read it again once its context is summarized, when the plan,
// which the prompt carries, is gone from it (issue #42). It is empty without
// a plan file.
func planFileNote(in ladotask.RunInput) string {
	if in.PlanFile == "" {
		return ""
	}
	return fmt.Sprintf("\nThe approved plan above is also kept read-only at `%s`, byte for byte. "+
		"When your context is summarized (compacted), read that file again before you go on, so you keep to the whole plan.\n", in.PlanFile)
}

// evidenceForm is the front matter of the evidence file a worker writes, the
// form ladoverify.ParseEvidence reads.
const evidenceForm = "```markdown\n" +
	"---\n" +
	"head: <the full SHA of the commit this evidence describes>\n" +
	"criteria:\n" +
	"  - criterion: 1            # the 1-based number of the acceptance criterion in the approved plan\n" +
	"    evidence:\n" +
	"      - check: <a CI check name on the pull request>\n" +
	"      - command: <a LADO check command name>\n" +
	"      - commit: <a commit SHA in this branch>\n" +
	"    note: <one line>\n" +
	"---\n" +
	"```\n"

// verificationParagraph tells the worker what LADO verifies on its pull
// request's head, so a failure report does not come as a surprise: the
// required CI checks, the project's check commands by name, the commit
// messages and protected paths when the snapshot holds their validators,
// and the changed paths. It then asks for the evidence file.
func verificationParagraph(snapshot domain.LadoPolicySnapshot, checks []domain.LadoCheckCommand) string {
	validators := snapshotValidators(snapshot)
	var b strings.Builder
	b.WriteString("Before the developer approves the integration, LADO verifies your pull request's head. ")
	b.WriteString("The CI checks its base branch requires must pass, or every check reported on the head when it requires none. ")
	if len(checks) > 0 {
		names := make([]string, len(checks))
		for i, check := range checks {
			names[i] = "`" + check.Name + "`"
		}
		noun, them := "command", "it"
		if len(names) > 1 {
			noun, them = "commands", "them"
		}
		fmt.Fprintf(&b, "The project's check %s %s must pass; LADO runs %s in this worktree, so leave it clean at the commit you pushed. ", noun, joinAnd(names), them)
	}
	if validators[ladopolicy.ValidatorCommitMessageLocale] {
		b.WriteString("Your commit messages and the pull request title must follow the commit message rule. ")
	}
	b.WriteString("A changed path outside the plan's affected_paths needs the developer's confirmation")
	if validators[ladopolicy.ValidatorProtectedPaths] {
		b.WriteString(", and a changed protected path fails unless the plan names it")
	}
	b.WriteString(". When a check fails, you get a message: push a fix, and LADO checks the new head. ")
	fmt.Fprintf(&b, "Before you run `ao report --done`, write `%s` in your session artifact directory, not in the repository, in the form below, "+
		"listing for each acceptance criterion of the approved plan the CI checks, LADO check commands or commits that show it is met. "+
		"LADO matches every entry against its records, shows the developer the entries it cannot match as claims, "+
		"and marks evidence written for another head as stale, so write the file again whenever you push again.", domain.LadoEvidencePath)
	return b.String()
}

// snapshotValidators returns the kinds of the validators a rule snapshot
// fixed. A snapshot from before lado-policy/3 has none.
func snapshotValidators(snapshot domain.LadoPolicySnapshot) map[ladopolicy.ValidatorKind]bool {
	var content ladopolicy.SnapshotContent
	kinds := map[ladopolicy.ValidatorKind]bool{}
	if err := json.Unmarshal(snapshot.Content, &content); err != nil {
		return kinds
	}
	for _, v := range content.Validators {
		kinds[v.Kind] = true
	}
	return kinds
}

// joinAnd lists items as "a, b and c".
func joinAnd(items []string) string {
	if len(items) <= 1 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// shortSHA is the first 12 hex digits of a sha256.
func shortSHA(sum string) string {
	if len(sum) > 12 {
		return sum[:12]
	}
	return sum
}
