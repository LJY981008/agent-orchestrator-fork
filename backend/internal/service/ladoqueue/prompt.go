package ladoqueue

import (
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladotask"
)

// workerPrompt is the task a dispatched worker starts with (decision n of the
// step 6 plan): a short lead that names the issue and asks to keep to the
// plan, then the approved plan file, front matter and body, unchanged. AO
// appends the pinned issue, which the spawn passes as its issue context, and
// the worker's system prompt carries the snapshot's rule text.
func workerPrompt(task domain.LadoTask, in ladotask.RunInput) string {
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
	return b.String()
}

// shortSHA is the first 12 hex digits of a sha256.
func shortSHA(sum string) string {
	if len(sum) > 12 {
		return sum[:12]
	}
	return sum
}
