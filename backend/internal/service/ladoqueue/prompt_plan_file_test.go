package ladoqueue

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladotask"
)

// Every worker of a run learns where the approved plan stays, read-only, and
// to read it again once its context is summarized (issue #42).
func TestWorkerPromptsNameThePlanFileToReadAgain(t *testing.T) {
	const planFile = "/data/lado/runs/rev-1/plan.md"
	task := domain.LadoTask{ID: "task-1", IssueRef: "github:o/r#42", Issue: domain.LadoObservedIssue{Title: "Watch workers"}}
	plan := "---\ntask: task-1\n---\n\n# Plan\n"
	in := ladotask.RunInput{
		PlanText: plan,
		PlanFile: planFile,
		Revision: domain.LadoPlanRevision{ID: "rev-1", Number: 2, SHA256: strings.Repeat("a", 64)},
	}
	part := domain.LadoTaskPart{PartID: "store", Title: "Store", Steps: []int{1}, AffectedPaths: []string{"backend/**"}, Branch: "b/store", MergeTipSHA: "abc"}
	run := domain.LadoPartRun{Branch: "b/root", HeadSHA: "def"}
	prompts := map[string]string{
		"single":      workerPrompt(task, in, nil),
		"part":        partPrompt(task, in, part, []domain.LadoTaskPart{part}, "b/store", nil),
		"merge":       mergePrompt(task, in, part, "b/merge", "", part.MergeTipSHA),
		"integration": integrationPrompt(task, in, nil, run, []domain.LadoTaskPart{part}, nil),
	}
	for role, prompt := range prompts {
		if !strings.Contains(prompt, "kept read-only at `"+planFile+"`, byte for byte") {
			t.Errorf("%s prompt does not name the plan file:\n%s", role, prompt)
		}
		if !strings.Contains(prompt, "When your context is summarized (compacted), read that file again before you go on") {
			t.Errorf("%s prompt does not ask to read the plan again", role)
		}
		// The plan the prompt carries stays as it was pinned, and the note
		// follows it.
		at := strings.Index(prompt, plan)
		if at < 0 || strings.Index(prompt, planFile) < at+len(plan) {
			t.Errorf("%s prompt: the note must follow the unchanged plan", role)
		}
	}

	// Without a plan file, as in a run with no data dir, nothing is said.
	in.PlanFile = ""
	for role, prompt := range map[string]string{
		"single": workerPrompt(task, in, nil),
		"part":   partPrompt(task, in, part, []domain.LadoTaskPart{part}, "b/store", nil),
	} {
		if strings.Contains(prompt, "read that file again") {
			t.Errorf("%s prompt names a plan file it does not have", role)
		}
	}
}
