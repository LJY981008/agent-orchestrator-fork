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

// A part may say it can end without a commit; others leave allow_empty out.
func TestParseLadoPlanReadsAllowEmpty(t *testing.T) {
	parts := strings.Replace(validParts, "    depends_on: [backend, frontend]\n", "    depends_on: [backend, frontend]\n    allow_empty: true\n", 1)
	plan, problems := parse(t, partsPlan(parts))
	if len(problems) != 0 {
		t.Fatalf("problems = %+v, want none", problems)
	}
	if plan.Parts[0].AllowEmpty || plan.Parts[1].AllowEmpty || !plan.Parts[2].AllowEmpty {
		t.Fatalf("parts = %+v, want only docs to allow an empty result", plan.Parts)
	}
}

// A workspace plan with parts keeps the plan's paths and every part's inside
// the folder of the repository that holds the issue.
func TestLadoPlanOutsideRepo(t *testing.T) {
	split := func(top []string, parts ...domain.LadoPlanPart) domain.LadoPlan {
		return domain.LadoPlan{AffectedPaths: top, Parts: parts}
	}
	part := func(id string, paths ...string) domain.LadoPlanPart {
		return domain.LadoPlanPart{ID: id, AffectedPaths: paths}
	}
	cases := []struct {
		name     string
		plan     domain.LadoPlan
		relative string
		want     []domain.LadoPlanProblem
	}{
		{
			name: "inside the repository", relative: "back",
			plan: split([]string{"back/**"}, part("a", "back/internal/**"), part("b", "back/go.mod")),
		},
		{
			name: "another repository in a part", relative: "back",
			plan: split([]string{"back/**"}, part("a", "back/internal/**"), part("b", "front/**")),
			want: []domain.LadoPlanProblem{{Code: domain.LadoPlanPartRepoOutside, Field: "parts", Detail: "b: front/**"}},
		},
		{
			name: "another repository in the plan's paths", relative: "back",
			plan: split([]string{"back/**", "front/src/**"}, part("a", "back/**"), part("b", "back/x/**")),
			want: []domain.LadoPlanProblem{{Code: domain.LadoPlanPartRepoOutside, Field: "affected_paths", Detail: "front/src/**"}},
		},
		{
			name: "a glob of the whole workspace", relative: "back",
			plan: split([]string{"**"}, part("a", "**/*.go"), part("b", "back/**")),
			want: []domain.LadoPlanProblem{
				{Code: domain.LadoPlanPartRepoOutside, Field: "affected_paths", Detail: "**"},
				{Code: domain.LadoPlanPartRepoOutside, Field: "parts", Detail: "a: **/*.go"},
			},
		},
		{
			name: "a nested repository folder", relative: "services/app",
			plan: split([]string{"services/app/**", "services/**"}, part("a", "services/app/api/**"), part("b", "services/web/**")),
			want: []domain.LadoPlanProblem{
				{Code: domain.LadoPlanPartRepoOutside, Field: "affected_paths", Detail: "services/**"},
				{Code: domain.LadoPlanPartRepoOutside, Field: "parts", Detail: "b: services/web/**"},
			},
		},
		{
			name: "a sibling folder with the repository's name as a prefix", relative: "back",
			plan: split([]string{"back/**"}, part("a", "back/**"), part("b", "backup/**")),
			want: []domain.LadoPlanProblem{{Code: domain.LadoPlanPartRepoOutside, Field: "parts", Detail: "b: backup/**"}},
		},
		{
			name: "case does not matter", relative: "Back/",
			plan: split([]string{"back/**"}, part("a", "BACK/internal/**"), part("b", "back/README.md")),
		},
		{
			name: "a plan without parts is not checked", relative: "back",
			plan: split([]string{"front/**", "**"}),
		},
		{
			name: "the project's root holds every path", relative: "",
			plan: split([]string{"front/**"}, part("a", "front/**"), part("b", "**")),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := domain.LadoPlanOutsideRepo(tc.plan, tc.relative); !slices.Equal(got, tc.want) {
				t.Fatalf("problems = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// sizedPlan is a plan of n steps with the given parts, built in code.
func sizedPlan(steps int, parts ...domain.LadoPlanPart) domain.LadoPlan {
	plan := domain.LadoPlan{Parts: parts}
	for i := range steps {
		plan.Steps = append(plan.Steps, "step "+string(rune('a'+i)))
	}
	return plan
}

func pathsOf(n int) []string {
	var paths []string
	for i := range n {
		paths = append(paths, "backend/p"+string(rune('a'+i))+"/**")
	}
	return paths
}

func TestLadoPlanWarnsOfASmallPlanSplit(t *testing.T) {
	split := sizedPlan(3,
		domain.LadoPlanPart{ID: "a", Steps: []int{1}, AffectedPaths: pathsOf(1)},
		domain.LadoPlanPart{ID: "b", Steps: []int{2}, AffectedPaths: pathsOf(1)})
	if w := split.Warnings(); !slices.Equal(w, []string{domain.LadoPlanWarningSmallSplit}) {
		t.Fatalf("three steps in two parts: warnings = %v, want split_small_plan", w)
	}
	four := split
	four.Steps = sizedPlan(domain.LadoPlanSmallSteps).Steps
	if w := four.Warnings(); !slices.Equal(w, []string{domain.LadoPlanWarningSmallSplit}) {
		t.Fatalf("four steps in two parts: warnings = %v, want split_small_plan", w)
	}
	five := split
	five.Steps = sizedPlan(domain.LadoPlanSmallSteps + 1).Steps
	if w := five.Warnings(); len(w) != 0 {
		t.Fatalf("five steps in two parts: warnings = %v, want none", w)
	}
	if w := sizedPlan(4).Warnings(); len(w) != 0 {
		t.Fatalf("four steps without parts: warnings = %v, want none", w)
	}
	if w := sizedPlan(6).Warnings(); !slices.Equal(w, []string{domain.LadoPlanWarningUnsplit}) {
		t.Fatalf("six steps without parts: warnings = %v, want unsplit_large_plan", w)
	}
}

func TestLadoPlanPartWarnings(t *testing.T) {
	plan := sizedPlan(20,
		domain.LadoPlanPart{ID: "steps4", Steps: []int{1, 2, 3, 4}, AffectedPaths: pathsOf(1)},
		domain.LadoPlanPart{ID: "steps5", Steps: []int{5, 6, 7, 8, 9}, AffectedPaths: pathsOf(1)},
		domain.LadoPlanPart{ID: "paths8", Steps: []int{10}, AffectedPaths: pathsOf(8)},
		domain.LadoPlanPart{ID: "paths9", Steps: []int{11}, AffectedPaths: pathsOf(9)},
		domain.LadoPlanPart{ID: "files200", Steps: []int{12}, AffectedPaths: pathsOf(1)},
		domain.LadoPlanPart{ID: "files201", Steps: []int{13}, AffectedPaths: pathsOf(1)},
		domain.LadoPlanPart{ID: "uncounted", Steps: []int{14}, AffectedPaths: pathsOf(1)},
	)
	files := map[string]int{"steps4": 1, "steps5": 1, "paths8": 1, "paths9": 1, "files200": 200, "files201": 201}
	got := plan.PartWarnings(files)
	want := map[string][]domain.LadoPlanPartWarning{
		"steps5":   {{Code: domain.LadoPlanPartWarningSteps, Value: 5, Limit: 4}},
		"paths9":   {{Code: domain.LadoPlanPartWarningPaths, Value: 9, Limit: 8}},
		"files201": {{Code: domain.LadoPlanPartWarningFiles, Value: 201, Limit: 200}},
	}
	if len(got) != len(want) {
		t.Fatalf("part warnings = %+v, want %+v", got, want)
	}
	for id, w := range want {
		if !slices.Equal(got[id], w) {
			t.Fatalf("part %s: warnings = %+v, want %+v", id, got[id], w)
		}
	}
	if got := plan.PartWarnings(nil); len(got) != 2 || got["files201"] != nil {
		t.Fatalf("without file counts: %+v, want the steps and paths warnings only", got)
	}
	if got := sizedPlan(6).PartWarnings(map[string]int{"x": 500}); got != nil {
		t.Fatalf("a plan without parts: %+v, want none", got)
	}
}

func TestParseLadoPlanKeepsSizeOutOfProblems(t *testing.T) {
	big := `parts:
  - id: backend
    title: Backend
    steps: [1, 2, 3, 4, 5]
    affected_paths: ["backend/a/**", "backend/b/**", "backend/c/**", "backend/d/**", "backend/e/**", "backend/f/**", "backend/g/**", "backend/h/**", "backend/i/**"]
  - id: frontend
    title: Frontend
    steps: [6]
    affected_paths: ["frontend/src/**"]
`
	plan, problems := parse(t, partsPlan(big))
	if len(problems) != 0 {
		t.Fatalf("problems = %+v, want none: size is a warning", problems)
	}
	w := plan.PartWarnings(nil)["backend"]
	if len(w) != 2 || w[0].Code != domain.LadoPlanPartWarningSteps || w[1].Code != domain.LadoPlanPartWarningPaths {
		t.Fatalf("backend warnings = %+v", w)
	}
}
