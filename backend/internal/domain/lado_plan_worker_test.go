package domain_test

import (
	"path"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestLadoPlanWorkerClientRequestIDRoundTrips(t *testing.T) {
	id := domain.LadoPlanWorkerClientRequestID("lado-plan-request-7", domain.LadoPlanRoleCritic, 2)
	if id != "lado:plan/lado-plan-request-7/critique/2" {
		t.Fatalf("id = %q", id)
	}
	req, role, attempt, ok := domain.ParseLadoPlanWorkerClientRequestID(id)
	if !ok || req != "lado-plan-request-7" || role != domain.LadoPlanRoleCritic || attempt != 2 {
		t.Fatalf("parse = %q %q %d %v", req, role, attempt, ok)
	}
	// A plan worker's id is never a dispatch attempt's, which the worker
	// gate grants by a queue lease.
	if _, _, dispatch := domain.ParseLadoClientRequestID(id); dispatch {
		t.Fatalf("%q parses as a dispatch attempt", id)
	}
	for _, bad := range []string{
		"lado:plan/req/design/0", "lado:plan/req/design/01", "lado:plan/req/other/1", "lado:plan//design/1",
		"lado:plan/req/design", "lado:plan/req/design/1/x", "lado:entry/1", "plan/req/design/1",
	} {
		if domain.IsLadoPlanWorkerClientRequestID(bad) {
			t.Errorf("%q parses as a plan worker id", bad)
		}
	}
	if domain.LadoPlanWorkerHash(id) == domain.LadoPlanWorkerHash(domain.LadoPlanWorkerClientRequestID("lado-plan-request-7", domain.LadoPlanRoleCritic, 1)) {
		t.Fatal("two attempts share a hash")
	}
}

func TestLadoPlanRequestWorkerPhase(t *testing.T) {
	at := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	base := domain.LadoPlanRequest{ID: "r", Kind: domain.LadoPlanRequestWorker, SessionID: domain.LadoPlanRequestPlaceholder("r")}
	cases := []struct {
		name string
		edit func(*domain.LadoPlanRequest)
		want domain.LadoPlanWorkerPhase
	}{
		{"waiting", func(*domain.LadoPlanRequest) {}, domain.LadoPlanPhaseWaiting},
		{"designing", func(r *domain.LadoPlanRequest) { r.WorkerStartedAt = at }, domain.LadoPlanPhaseDesigning},
		{"critiquing", func(r *domain.LadoPlanRequest) { r.WorkerStartedAt, r.DraftSubmittedAt = at, at }, domain.LadoPlanPhaseCritiquing},
		{"revising", func(r *domain.LadoPlanRequest) { r.WorkerStartedAt, r.DraftSubmittedAt, r.FeedbackSentAt = at, at, at }, domain.LadoPlanPhaseRevising},
		{"submitted", func(r *domain.LadoPlanRequest) { r.WorkerStartedAt, r.DraftSubmittedAt, r.SubmittedAt = at, at, at }, domain.LadoPlanPhaseSubmitted},
		{"fixing", func(r *domain.LadoPlanRequest) { r.WorkerStartedAt, r.SubmittedAt, r.FixRequestedAt = at, at, at }, domain.LadoPlanPhaseFixing},
		{"done", func(r *domain.LadoPlanRequest) { r.WorkerStartedAt, r.SubmittedAt, r.CleanedAt = at, at, at }, domain.LadoPlanPhaseDone},
		{"failed", func(r *domain.LadoPlanRequest) { r.WorkerStartedAt, r.FailedAt, r.FailureCode = at, at, domain.LadoPlanWorkerNoPlan }, domain.LadoPlanPhaseFailed},
		{"replaced", func(r *domain.LadoPlanRequest) { r.FailedAt, r.FailureCode = at, domain.LadoPlanRequestReplaced }, domain.LadoPlanPhaseReplaced},
	}
	for _, tc := range cases {
		req := base
		tc.edit(&req)
		if got := req.WorkerPhase(); got != tc.want {
			t.Errorf("%s: phase = %q, want %q", tc.name, got, tc.want)
		}
	}
	if req := (domain.LadoPlanRequest{Kind: domain.LadoPlanRequestOrchestrator}); req.WorkerPhase() != "" || req.Open() {
		t.Fatalf("an orchestrator request reads as a plan worker request")
	}
	if base.WorkerSession() != "" {
		t.Fatal("the placeholder reads as a session")
	}
	started := base
	started.SessionID = "demo-3"
	if started.WorkerSession() != "demo-3" {
		t.Fatalf("WorkerSession = %q", started.WorkerSession())
	}
}

func TestValidateLadoPlanFindings(t *testing.T) {
	good := []domain.LadoPlanFinding{
		{ID: "C1", Severity: domain.LadoFindingP1, Title: "Migration order", FailureScenario: "fresh install fails"},
		{ID: "C2", Severity: domain.LadoFindingMinor, Title: "Typo"},
	}
	if err := domain.ValidateLadoPlanFindings(good); err != nil {
		t.Fatalf("good findings: %v", err)
	}
	if err := domain.ValidateLadoPlanFindings(nil); err != nil {
		t.Fatalf("no findings: %v", err)
	}
	bad := map[string][]domain.LadoPlanFinding{
		"id":       {{ID: "1 bad", Severity: domain.LadoFindingP1, Title: "x"}},
		"repeat":   {{ID: "C1", Severity: domain.LadoFindingP1, Title: "x"}, {ID: "C1", Severity: domain.LadoFindingMinor, Title: "y"}},
		"severity": {{ID: "C1", Severity: "critical", Title: "x"}},
		"title":    {{ID: "C1", Severity: domain.LadoFindingMajor, Title: "  "}},
		"long":     {{ID: "C1", Severity: domain.LadoFindingMajor, Title: "x", Suggestion: strings.Repeat("a", 4001)}},
	}
	for name, findings := range bad {
		if err := domain.ValidateLadoPlanFindings(findings); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	critique := domain.LadoPlanCritique{Findings: append(good, domain.LadoPlanFinding{ID: "C3", Severity: domain.LadoFindingMajor, Title: "x"})}
	if p1, major, minor := critique.Counts(); p1 != 1 || major != 1 || minor != 1 {
		t.Fatalf("counts = %d %d %d", p1, major, minor)
	}
	if blocking := critique.BlockingFindings(); len(blocking) != 2 || blocking[0].ID != "C1" || blocking[1].ID != "C3" {
		t.Fatalf("blocking = %+v", blocking)
	}
}

// globMatch is a stand-in for the services' matcher: path.Match per segment,
// "**" for any number of segments.
func globMatch(pattern, name string) bool {
	var match func(p, n []string) bool
	match = func(p, n []string) bool {
		if len(p) == 0 {
			return len(n) == 0
		}
		if p[0] == "**" {
			for i := 0; i <= len(n); i++ {
				if match(p[1:], n[i:]) {
					return true
				}
			}
			return false
		}
		if len(n) == 0 {
			return false
		}
		ok, _ := path.Match(p[0], n[0])
		return ok && match(p[1:], n[1:])
	}
	return match(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

func TestDecideLadoPlanReview(t *testing.T) {
	settings := domain.DefaultLadoPlanReviewSettings()
	small := domain.LadoPlan{AffectedPaths: []string{"frontend/src/a.ts", "frontend/src/b.ts", "docs/x.md"}}
	if d := domain.DecideLadoPlanReview(small, settings, globMatch); !d.Skip || d.Reason == "" || len(d.Kinds) != 0 {
		t.Fatalf("three plain paths: %+v, want a skip with a reason", d)
	}
	four := small
	four.AffectedPaths = append(slices.Clone(small.AffectedPaths), "frontend/src/c.ts")
	if d := domain.DecideLadoPlanReview(four, settings, globMatch); d.Skip {
		t.Fatalf("four paths skip the review: %+v", d)
	}
	declared := small
	declared.ChangeKinds = []string{domain.LadoChangeDataDeletion}
	if d := domain.DecideLadoPlanReview(declared, settings, globMatch); d.Skip || !slices.Equal(d.Kinds, []string{domain.LadoChangeDataDeletion}) {
		t.Fatalf("a declared deletion: %+v", d)
	}
	inferred := domain.LadoPlan{AffectedPaths: []string{"backend/internal/storage/sqlite/migrations_lado/**"}}
	if d := domain.DecideLadoPlanReview(inferred, settings, globMatch); d.Skip || !slices.Equal(d.Kinds, []string{domain.LadoChangeDBSchema}) {
		t.Fatalf("a migration path: %+v", d)
	}
	off := settings
	off.Skip = false
	if d := domain.DecideLadoPlanReview(small, off, globMatch); d.Skip {
		t.Fatalf("the skip turned off still skips: %+v", d)
	}
	wider := settings
	wider.MaxPaths = 4
	if d := domain.DecideLadoPlanReview(four, wider, globMatch); !d.Skip {
		t.Fatalf("four paths under a limit of four: %+v", d)
	}
}

func TestLadoPlanReviewSettingsValidate(t *testing.T) {
	if err := domain.DefaultLadoPlanReviewSettings().Validate(); err != nil {
		t.Fatalf("defaults: %v", err)
	}
	for name, s := range map[string]domain.LadoPlanReviewSettings{
		"zero paths": {MaxPaths: 0},
		"too many":   {MaxPaths: 21},
		"kind":       {MaxPaths: 3, Patterns: map[string][]string{"secrets": {"**/x"}}},
		"glob":       {MaxPaths: 3, Patterns: map[string][]string{domain.LadoChangeAuth: {"/abs/**"}}},
	} {
		if err := s.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	settings := domain.DefaultLadoDispatchSettings()
	settings.MaxPlanWorkers = 9
	if err := settings.Validate(); err == nil {
		t.Fatal("nine plan workers accepted")
	}
}

func TestCheckLadoPlanReview(t *testing.T) {
	findings := []domain.LadoPlanFinding{
		{ID: "C1", Severity: domain.LadoFindingP1, Title: "a"},
		{ID: "C2", Severity: domain.LadoFindingMajor, Title: "b"},
		{ID: "C3", Severity: domain.LadoFindingMinor, Title: "c"},
	}
	if problems, _, _ := domain.CheckLadoPlanReview("# Plan\n\nNo section.", findings); len(problems) != 1 || problems[0].Code != domain.LadoPlanReviewMissing {
		t.Fatalf("missing section: %+v", problems)
	}
	answered := "# Plan\n\n## 계획 검수\n\n- C1: 반영 — 마이그레이션 순서를 고쳤다\n- C2: 미반영 — 범위 밖이라 #40 에서 한다\n\n## 다음\n- C9 반영\n"
	problems, addressed, notAddressed := domain.CheckLadoPlanReview(answered, findings)
	if len(problems) != 0 || addressed != 1 || notAddressed != 1 {
		t.Fatalf("answered: problems %+v, addressed %d, not addressed %d", problems, addressed, notAddressed)
	}
	unanswered := "## 계획 검수\n\n- C1 반영\n- C2: 미반영\n- C10 반영\n"
	problems, _, _ = domain.CheckLadoPlanReview(unanswered, findings)
	if len(problems) != 1 || problems[0].Code != domain.LadoPlanReviewUnaddressed || problems[0].Detail != "C2" {
		t.Fatalf("a not addressed answer without a reason: %+v", problems)
	}
	english := "## Plan review\n\n- C1: addressed\n- C2: not addressed, out of scope\n"
	if problems, _, _ := domain.CheckLadoPlanReview(english, findings); len(problems) != 0 {
		t.Fatalf("English answers: %+v", problems)
	}
	if problems, _, _ := domain.CheckLadoPlanReview("## 계획 검수\n\n검수를 건너뜀: 작은 작업\n", nil); len(problems) != 0 {
		t.Fatalf("a skipped review's section: %+v", problems)
	}
}

func TestParseLadoPlanReadsChangeKinds(t *testing.T) {
	plan := func(kinds string) string {
		return "---\ntask: lado-task-1\nissue: github:acme/demo#1\nbase_ref: main\nsteps: [a]\nacceptance_criteria: [b]\n" +
			"affected_paths: [c]\ntest_commands: []\nrisk: low\npermission_ceiling: auto\nreview:\n  required: false\n" +
			"plan_source: issue\n" + kinds + "---\nbody\n"
	}
	want := domain.LadoPlanExpectations{Task: "lado-task-1", Issue: "github:acme/demo#1"}
	parsed, problems := domain.ParseLadoPlan([]byte(plan("change_kinds: [db_schema, auth, db_schema]\n")), want)
	if len(problems) != 0 || !slices.Equal(parsed.ChangeKinds, []string{domain.LadoChangeDBSchema, domain.LadoChangeAuth}) {
		t.Fatalf("change kinds = %v, problems %+v", parsed.ChangeKinds, problems)
	}
	if parsed, problems := domain.ParseLadoPlan([]byte(plan("")), want); len(problems) != 0 || len(parsed.ChangeKinds) != 0 {
		t.Fatalf("no change kinds: %v %+v", parsed.ChangeKinds, problems)
	}
	_, problems = domain.ParseLadoPlan([]byte(plan("change_kinds: [secrets]\n")), want)
	if len(problems) != 1 || problems[0].Field != "change_kinds" || problems[0].Detail != "secrets" {
		t.Fatalf("an unknown kind: %+v", problems)
	}
}

func TestLadoNoticeLines(t *testing.T) {
	lines := []string{
		domain.LadoNoticeRequested(27),
		domain.LadoNoticeRevision(27, 2),
		domain.LadoNoticePlanFailed(27, domain.LadoPlanWorkerTimedOut),
		domain.LadoNoticeStarted(27),
		domain.LadoNoticePROpened(27, "https://github.com/acme/demo/pull/41"),
		domain.LadoNoticeCompleted(27),
		domain.LadoNoticeRunFailed(27, domain.LadoFailedEndedWithoutPR),
	}
	want := []string{
		"LADO 알림: #27 계획 요청 접수 — 계획 워커가 씁니다",
		"LADO 알림: #27 계획 리비전 2 — 칸반에서 확인",
		"LADO 알림: #27 계획 실패: 시간 초과",
		"LADO 알림: #27 실행 시작",
		"LADO 알림: #27 PR #41 열림",
		"LADO 알림: #27 완료",
		"LADO 알림: #27 실행 실패: PR 없이 끝남",
	}
	if !slices.Equal(lines, want) {
		t.Fatalf("lines =\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	if got := domain.LadoNoticePROpened(3, "https://gitlab.com/g/p/-/merge_requests/9"); got != "LADO 알림: #3 PR #9 열림" {
		t.Fatalf("GitLab MR line = %q", got)
	}
	if got := domain.LadoNoticePROpened(3, "https://example.com/review"); got != "LADO 알림: #3 PR 열림" {
		t.Fatalf("URL without a number = %q", got)
	}
	if got := domain.LadoNoticePlanFailed(1, "something_new"); !strings.HasSuffix(got, "알 수 없는 이유") {
		t.Fatalf("unknown code = %q", got)
	}
	for _, line := range lines {
		if utf8.RuneCountInString(line) > domain.LadoNoticeMaxRunes || strings.Contains(line, "\n") {
			t.Errorf("line %q is longer than one notice", line)
		}
	}
}
