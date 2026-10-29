package domain_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// partsPlan is a valid plan with six steps, two broad paths and the parts
// block given, which replaces the whole parts key.
func partsPlan(parts string) string {
	return `---
task: lado-task-1
issue: "github:acme/demo#12"
base_ref: main
steps:
  - one
  - two
  - three
  - four
  - five
  - six
acceptance_criteria:
  - works
affected_paths:
  - "backend/**"
  - "frontend/src/**"
  - "DESIGN.md"
test_commands: []
risk: low
permission_ceiling: auto
review:
  required: false
plan_source: issue
` + parts + `---

# Plan
`
}

const validParts = `parts:
  - id: backend
    title: Backend
    steps: [1, 2]
    affected_paths: ["backend/internal/service/**"]
    depends_on: []
  - id: frontend
    title: Frontend
    steps: [3]
    affected_paths: ["frontend/src/renderer/**"]
  - id: docs
    title: Design notes
    steps: [4]
    affected_paths: ["DESIGN.md"]
    depends_on: [backend, frontend]
`

func parse(t *testing.T, content string) (domain.LadoPlan, []domain.LadoPlanProblem) {
	t.Helper()
	return domain.ParseLadoPlan([]byte(content), domain.LadoPlanExpectations{Task: "lado-task-1", Issue: "github:acme/demo#12"})
}

func TestParseLadoPlanReadsParts(t *testing.T) {
	plan, problems := parse(t, partsPlan(validParts))
	if len(problems) != 0 {
		t.Fatalf("problems = %+v, want none", problems)
	}
	if len(plan.Parts) != 3 || plan.Parts[2].ID != "docs" || !slices.Equal(plan.Parts[2].DependsOn, []string{"backend", "frontend"}) {
		t.Fatalf("parts = %+v", plan.Parts)
	}
	if got := plan.IntegrationSteps(); !slices.Equal(got, []int{5, 6}) {
		t.Fatalf("integration steps = %v, want [5 6]", got)
	}
	if w := plan.Warnings(); len(w) != 0 {
		t.Fatalf("warnings = %v, want none for a split plan", w)
	}
	phases := domain.LadoPartPhases(plan.Parts)
	if phases["backend"] != 1 || phases["frontend"] != 1 || phases["docs"] != 2 {
		t.Fatalf("phases = %v, want backend and frontend in 1, docs in 2", phases)
	}
}

func TestParseLadoPlanWithoutPartsStaysAsItWas(t *testing.T) {
	plan, problems := parse(t, partsPlan(""))
	if len(problems) != 0 || plan.Parts != nil || plan.IntegrationSteps() != nil {
		t.Fatalf("plan without parts: parts %+v, problems %+v", plan.Parts, problems)
	}
	if w := plan.Warnings(); !slices.Equal(w, []string{domain.LadoPlanWarningUnsplit}) {
		t.Fatalf("warnings = %v, want the unsplit warning for six steps", w)
	}
	small := strings.Replace(partsPlan(""), "  - five\n  - six\n", "", 1)
	if plan, _ := parse(t, small); len(plan.Warnings()) != 0 {
		t.Fatal("a four-step plan without parts was warned about")
	}
}

func TestParseLadoPlanRefusesBadParts(t *testing.T) {
	part := func(id, steps, paths, deps string) string {
		return "  - id: " + id + "\n    title: T\n    steps: " + steps + "\n    affected_paths: " + paths + "\n    depends_on: " + deps + "\n"
	}
	ok := part("b", "[2]", `["frontend/src/x/**"]`, "[]")
	cases := []struct {
		name  string
		parts string
		code  string
	}{
		{"one part", "parts:\n" + part("a", "[1]", `["backend/**"]`, "[]"), domain.LadoPlanPartCount},
		{"nine parts", "parts:\n" + func() string {
			var b strings.Builder
			for _, id := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"} {
				b.WriteString(part(id, "[1]", `["backend/`+id+`/**"]`, "[]"))
			}
			return b.String()
		}(), domain.LadoPlanPartCount},
		{"bad id", "parts:\n" + part("Bad_ID", "[1]", `["backend/**"]`, "[]") + ok, domain.LadoPlanPartInvalidID},
		{"reserved id", "parts:\n" + part("integration", "[1]", `["backend/**"]`, "[]") + ok, domain.LadoPlanPartInvalidID},
		{"merge prefix", "parts:\n" + part("merge-a", "[1]", `["backend/**"]`, "[]") + ok, domain.LadoPlanPartInvalidID},
		{"duplicate id", "parts:\n" + part("b", "[1]", `["backend/**"]`, "[]") + ok, domain.LadoPlanPartDuplicateID},
		{"unknown dependency", "parts:\n" + part("a", "[1]", `["backend/**"]`, "[nope]") + ok, domain.LadoPlanPartUnknownDependency},
		{"cycle", "parts:\n" + part("a", "[1]", `["backend/**"]`, "[b]") + part("b", "[2]", `["frontend/src/x/**"]`, "[a]"), domain.LadoPlanPartCycle},
		{"step out of range", "parts:\n" + part("a", "[7]", `["backend/**"]`, "[]") + ok, domain.LadoPlanPartStepOutOfRange},
		{"step zero", "parts:\n" + part("a", "[0]", `["backend/**"]`, "[]") + ok, domain.LadoPlanPartStepOutOfRange},
		{"shared step", "parts:\n" + part("a", "[2]", `["backend/**"]`, "[]") + ok, domain.LadoPlanPartStepShared},
		{"path outside", "parts:\n" + part("a", "[1]", `["scripts/**"]`, "[]") + ok, domain.LadoPlanPartPathOutside},
		{"path outside by case-sensitive literal", "parts:\n" + part("a", "[1]", `["DESIGN.md.bak"]`, "[]") + ok, domain.LadoPlanPartPathOutside},
		{"no steps", "parts:\n" + part("a", "[]", `["backend/**"]`, "[]") + ok, domain.LadoPlanPartMissing},
		{"not a list", "parts: yes\n", domain.LadoPlanInvalidField},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rev := domain.LadoPlanRevision{}
			_, rev.Problems = parse(t, partsPlan(tc.parts))
			found := false
			for _, p := range rev.Problems {
				found = found || p.Code == tc.code
			}
			if !found {
				t.Fatalf("problems = %+v, want %s", rev.Problems, tc.code)
			}
			if rev.Valid() {
				t.Fatal("a plan with bad parts could be approved")
			}
		})
	}
}

func TestLadoPathsOverlap(t *testing.T) {
	cases := []struct {
		a, b   []string
		ignore []string
		want   bool
	}{
		{[]string{"backend/internal/a/**"}, []string{"backend/internal/b/**"}, nil, false},
		{[]string{"backend/internal/**"}, []string{"backend/internal/b/x.go"}, nil, true},
		{[]string{"backend/x.go"}, []string{"backend/x.go"}, nil, true},
		{[]string{"backend/x.go"}, []string{"backend/y.go"}, nil, false},
		{[]string{"Backend/X.go"}, []string{"backend/x.go"}, nil, true},
		{[]string{"backend/*.go"}, []string{"backend/sub/deep.go"}, nil, true},
		{[]string{"frontend/src/renderer/i18n/*.json"}, []string{"frontend/src/renderer/i18n/*.json"}, domain.LadoDefaultOverlapIgnore, false},
		{[]string{"frontend/**"}, []string{"frontend/src/renderer/i18n/*.json"}, domain.LadoDefaultOverlapIgnore, false},
		{[]string{"docs/**"}, []string{"docs/LJY-PRD/TDD.md"}, domain.LadoDefaultOverlapIgnore, false},
		{[]string{"DESIGN.md"}, []string{"design.md"}, domain.LadoDefaultOverlapIgnore, false},
		{[]string{"frontend/**"}, []string{"frontend/src/renderer/components/x.tsx"}, domain.LadoDefaultOverlapIgnore, true},
		{[]string{"backend/internal/storage/sqlite/**"}, []string{"backend/internal/storage/sqlite/gen/**"}, domain.LadoDefaultOverlapIgnore, false},
		{[]string{"backend/internal/storage/sqlite/**"}, []string{"backend/internal/storage/sqlite/queries/x.sql"}, domain.LadoDefaultOverlapIgnore, true},
	}
	for i, tc := range cases {
		if _, _, got := domain.LadoPathsOverlap(tc.a, tc.b, tc.ignore); got != tc.want {
			t.Errorf("case %d: overlap(%v, %v) = %v, want %v", i, tc.a, tc.b, got, tc.want)
		}
	}
}

func TestLadoRunClientRequestIDsRoundTrip(t *testing.T) {
	for _, r := range []domain.LadoRunRequest{
		{Entry: "lado-queue-1", Attempt: 2},
		{Entry: "lado-queue-1", Role: domain.LadoRolePart, Part: "backend", Attempt: 1},
		{Entry: "lado-queue-1", Role: domain.LadoRoleMerge, Part: "backend", Attempt: 2},
		{Entry: "lado-queue-1", Role: domain.LadoRoleIntegration, Attempt: 1},
	} {
		id := domain.LadoRunClientRequestID(r)
		got, ok := domain.ParseLadoRunClientRequestID(id)
		if !ok || got != r {
			t.Errorf("%s parsed to %+v, %v; want %+v", id, got, ok, r)
		}
	}
	if id := domain.LadoRunClientRequestID(domain.LadoRunRequest{Entry: "e", Role: domain.LadoRolePart, Part: "a", Attempt: 1}); id != "lado:e/a/1" {
		t.Fatalf("part id = %s", id)
	}
	for _, bad := range []string{"lado:e/a/0", "lado:e/A/1", "lado:e/a/01", "lado:/a/1", "lado:e/a/b/1", "lado:plan/r/design/1", "x:e/a/1"} {
		if _, ok := domain.ParseLadoRunClientRequestID(bad); ok {
			t.Errorf("%s parsed", bad)
		}
	}
	if _, _, ok := domain.ParseLadoClientRequestID("lado:e/a/1"); ok {
		t.Fatal("the single worker parser read a part id")
	}
}

func TestLadoPartBranches(t *testing.T) {
	task := domain.LadoTaskID("lado-task-c14c6be2-d106-48d5")
	entry := domain.LadoQueueEntryID("lado-queue-500878f7-5c3b")
	if got := domain.LadoIntegrationBranch(task, entry); got != "lado/c14c6be2/500878f7/integration" {
		t.Fatalf("integration branch = %s", got)
	}
	if got := domain.LadoPartBranch(task, entry, "api", 1); got != "lado/c14c6be2/500878f7/api" {
		t.Fatalf("part branch = %s", got)
	}
	if got := domain.LadoPartBranch(task, entry, "api", 2); got != "lado/c14c6be2/500878f7/api.2" {
		t.Fatalf("retry branch = %s", got)
	}
	if got := domain.LadoMergeBranch(task, entry, "api", 1); got != "lado/c14c6be2/500878f7/merge-api" {
		t.Fatalf("merge branch = %s", got)
	}
	// A retry's branch is never another part's, and a later approval's run
	// never reuses an earlier run's branches.
	if domain.LadoPartBranch(task, entry, "api", 2) == domain.LadoPartBranch(task, entry, "api-2", 1) {
		t.Fatal("the second attempt of api shares part api-2's branch")
	}
	if domain.LadoMergeBranch(task, entry, "api", 2) == domain.LadoMergeBranch(task, entry, "api-2", 1) {
		t.Fatal("the second merge of api shares api-2's merge branch")
	}
	if domain.LadoIntegrationBranch(task, entry) == domain.LadoIntegrationBranch(task, "lado-queue-71aa0c3e-0000") {
		t.Fatal("two approvals of the task share an integration branch")
	}
}

func TestATaskRunningPartsIsRunning(t *testing.T) {
	at := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	task := queuedLadoTask(at)
	task.Queue.PartsStartedAt = at
	if state, reason := task.StateAt(at), task.StateReason(); state != domain.LadoTaskRunning || reason != "" {
		t.Fatalf("state = %s (%s), want running", state, reason)
	}
	task.Plan.PolicySHA256 = "changed"
	if state := task.StateAt(at); state != domain.LadoTaskRunning {
		t.Fatalf("a rule change while parts run made the task %s", state)
	}
	task.Plan.PolicySHA256 = task.Plan.Snapshot.SHA256
	task.Queue.FailedAt = at
	if state, reason := task.StateAt(at), task.StateReason(); state != domain.LadoTaskFailed || reason != domain.LadoFailedAttemptsExhausted {
		t.Fatalf("failed parts run: %s (%s), want failed attempts_exhausted", state, reason)
	}
	task.Queue.FailedAt = time.Time{}
	task.Queue.DispatchedSessionID = "s-int"
	task.Execution = domain.LadoExecution{SessionID: "s-int", PlanApprovalID: task.Plan.Approval.ID, Role: domain.LadoRoleIntegration,
		QueueEntryID: task.Queue.ID, IntegrationGated: true}
	if state := task.StateAt(at); state != domain.LadoTaskRunning {
		t.Fatalf("integration run without a PR: %s, want running", state)
	}
	task.Execution.PR = domain.LadoExecutionPR{URL: "https://github.com/acme/demo/pull/3", Number: 3, HeadSHA: "abc"}
	if state := task.StateAt(at); state != domain.LadoTaskValidating {
		t.Fatalf("integration run with a PR: %s, want validating", state)
	}
	task.Execution.PR.Merged = true
	task.Execution.Integration.MergedHeadApproved = true
	if state := task.StateAt(at); state != domain.LadoTaskCompleted {
		t.Fatalf("integration PR merged: %s, want completed", state)
	}
}
