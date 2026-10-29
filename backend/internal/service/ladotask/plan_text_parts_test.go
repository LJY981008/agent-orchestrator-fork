package ladotask

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// The orchestrator's plan request asks for parallel parts by default and
// says how to split them, and its format names the parts list.
func TestPlanRequestAsksForParallelParts(t *testing.T) {
	message := planRequestMessage(domain.LadoTask{ID: "lado-task-1"}, domain.LadoPlanRequest{ID: "req-1"},
		domain.LadoIssueSnapshot{ID: "github:acme/demo#1"}, "/artifacts", domain.LadoPlanRevision{})
	for _, want := range []string{
		"Plan for parallel parts by default", "same phase, which run at once", "depends_on", "integration steps or to one part",
		"narrow affected_paths", "2 to 8 parts", "leave parts out and say why", "parts:\n  - id:",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("plan request lacks %q:\n%s", want, message)
		}
	}
}

// Every part problem reads as a sentence for the plan worker's fix message.
func TestPartProblemsReadAsText(t *testing.T) {
	for _, code := range []string{
		domain.LadoPlanPartCount, domain.LadoPlanPartInvalidID, domain.LadoPlanPartDuplicateID, domain.LadoPlanPartUnknownDependency,
		domain.LadoPlanPartCycle, domain.LadoPlanPartStepOutOfRange, domain.LadoPlanPartStepShared, domain.LadoPlanPartPathOutside,
		domain.LadoPlanPartMissing,
	} {
		if text := problemText(domain.LadoPlanProblem{Code: code}); text == code {
			t.Fatalf("problem %s has no text", code)
		}
	}
}
