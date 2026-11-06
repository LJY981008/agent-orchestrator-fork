package ladotask

import (
	"fmt"
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
		"    allow_empty: <true only for a part that may end without a commit",
		"lists their folders in repos, in the order their pull requests merge", "repos:\n  - <workspace projects only:", "done without a commit stops the run, unless the part sets allow_empty: true",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("plan request lacks %q:\n%s", want, message)
		}
	}
}

// The parts guidance starts with the work that stays in one worker, before
// it asks for parallel parts, and its sizes are the domain's limits (#69).
func TestPartsGuidanceStartsWithTheWorkNotToSplit(t *testing.T) {
	message := planRequestMessage(domain.LadoTask{ID: "lado-task-1"}, domain.LadoPlanRequest{ID: "req-1"},
		domain.LadoIssueSnapshot{ID: "github:acme/demo#1"}, "/artifacts", domain.LadoPlanRevision{})
	for _, want := range []string{
		fmt.Sprintf("Leave parts out when the work has at most %d steps or changes about %d files", domain.LadoPlanSmallSteps, domain.LadoPlanSmallFiles),
		"80,000 to 110,000 tokens", "the integration worker reads the whole work again",
		fmt.Sprintf("Plan for parallel parts by default when the work has more than %d steps", domain.LadoPlanSmallSteps),
		fmt.Sprintf("2 to %d steps in one package or one area (store, service, API, UI)", domain.LadoPlanPartTargetSteps),
		fmt.Sprintf("more than %d steps or more than %d affected_paths", domain.LadoPlanPartMaxSteps, domain.LadoPlanPartMaxPaths),
		"at most 4 steps", "git -C <repository folder> ls-files -- <the fixed folder of the glob> | wc -l",
		"a workspace root may not be a git repository", "single quotes",
		fmt.Sprintf("more than %d files", domain.LadoPlanPartMaxFiles),
		fmt.Sprintf("`git -C <repository folder> ls-files | wc -l` is over %d, aim for 2 steps per part", domain.LadoPlanLargeRepoFiles),
		"Keep a part's own tests, and documents only that part changes, in the part",
		"checking the parts against each other and wiring them together, files several parts change together, the full checks and the pull request",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("plan request lacks %q:\n%s", want, message)
		}
	}
	if strings.Contains(message, "Leave the steps that check, document or publish the whole work") {
		t.Fatalf("plan request still leaves every document and check out of the parts:\n%s", message)
	}
	notSplit, parallel := strings.Index(message, "Leave parts out when"), strings.Index(message, "Plan for parallel parts by default")
	if notSplit < 0 || parallel < 0 || notSplit > parallel {
		t.Fatalf("the work not to split (at %d) does not come before the parallel default (at %d):\n%s", notSplit, parallel, message)
	}
}

// Every part problem reads as a sentence for the plan worker's fix message.
func TestPartProblemsReadAsText(t *testing.T) {
	for _, code := range []string{
		domain.LadoPlanPartCount, domain.LadoPlanPartInvalidID, domain.LadoPlanPartDuplicateID, domain.LadoPlanPartUnknownDependency,
		domain.LadoPlanPartCycle, domain.LadoPlanPartStepOutOfRange, domain.LadoPlanPartStepShared, domain.LadoPlanPartPathOutside,
		domain.LadoPlanPartMissing, domain.LadoPlanPartRepoOutside,
		domain.LadoPlanReposInvalid, domain.LadoPlanReposMissing, domain.LadoPlanRepoUnknown,
	} {
		if text := problemText(domain.LadoPlanProblem{Code: code}); text == code {
			t.Fatalf("problem %s has no text", code)
		}
	}
}
