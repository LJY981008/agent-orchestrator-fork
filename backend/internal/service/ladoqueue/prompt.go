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
// carries the snapshot's rule text.
func workerPrompt(task domain.LadoTask, in ladotask.RunInput, checks []domain.LadoCheckCommand) string {
	example := ""
	if id, err := domain.ParseLadoIssueRef(task.IssueRef); err == nil {
		if n := domain.LadoIssueNumber(id); n > 0 {
			example = fmt.Sprintf(" (for example \"Closes #%d\")", n)
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "You are implementing a plan the developer approved in LADO for %s: %s.\n", task.IssueRef, oneLine(task.Issue.Title))
	fmt.Fprintf(&b, "Follow the approved plan below and keep to its scope. When you open the pull request, link the issue in its description%s.\n\n", example)
	fmt.Fprintf(&b, "## Approved plan (revision %d, sha256 %s)\n\n", in.Revision.Number, shortSHA(in.Revision.SHA256))
	b.WriteString(in.PlanText)
	if !strings.HasSuffix(in.PlanText, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("\n## Verification and evidence\n\n")
	b.WriteString(verificationParagraph(in.Snapshot, checks))
	b.WriteString("\n\n" + evidenceForm)
	return b.String()
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
