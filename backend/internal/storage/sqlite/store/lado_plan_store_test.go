package store_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

var ladoPlanAt = ladoTaskCreatedAt.Add(time.Hour)

func ladoSHA(c byte) string {
	return strings.Repeat(string(c), 64)
}

func mustLadoPlanRequest(t *testing.T, s *sqlite.Store, id, task string, at time.Time) domain.LadoPlanRequest {
	t.Helper()
	req, err := s.CreateLadoPlanRequest(context.Background(), domain.LadoPlanRequest{
		ID:                  domain.LadoPlanRequestID(id),
		TaskID:              domain.LadoTaskID(task),
		SessionID:           "mer-orchestrator-1",
		IssueSnapshotSHA256: ladoSHA('1'),
		SourceCommentID:     "30",
		RequestedAt:         at,
	})
	if err != nil {
		t.Fatalf("CreateLadoPlanRequest %s: %v", id, err)
	}
	return req
}

func ladoRevision(id, task, request string, version int, sha string) domain.LadoPlanRevision {
	return domain.LadoPlanRevision{
		ID:                  domain.LadoPlanRevisionID(id),
		TaskID:              domain.LadoTaskID(task),
		RequestID:           domain.LadoPlanRequestID(request),
		SessionID:           "mer-orchestrator-1",
		Path:                domain.LadoPlanPath(domain.LadoTaskID(task)),
		ArtifactVersion:     version,
		Source:              "agent",
		SHA256:              sha,
		BaseRef:             "main",
		BaseSHA:             strings.Repeat("c", 40),
		IssueSnapshotSHA256: ladoSHA('1'),
		CreatedAt:           ladoPlanAt.Add(time.Duration(version) * time.Minute),
	}
}

func mustPin(t *testing.T, s *sqlite.Store, rev domain.LadoPlanRevision) domain.LadoPlanRevision {
	t.Helper()
	pinned, created, err := s.PinLadoPlanRevision(context.Background(), rev)
	if err != nil || !created {
		t.Fatalf("PinLadoPlanRevision %s = (%v, %v), want a new revision", rev.ID, created, err)
	}
	return pinned
}

func ladoProof(nonce string, subject string) domain.LadoPresenceProof {
	return domain.LadoPresenceProof{
		Nonce:     nonce,
		Action:    domain.LadoPresencePlanApprove,
		SubjectID: subject,
		ExpiresAt: ladoPlanAt.Add(2 * time.Minute),
		UsedAt:    ladoPlanAt.Add(time.Minute),
	}
}

func ladoApprovalEvent(revision string) domain.LadoAuditEvent {
	return domain.LadoAuditEvent{
		At:          ladoPlanAt.Add(time.Minute),
		Actor:       "human",
		Action:      "plan.approved",
		SubjectKind: "plan_revision",
		SubjectID:   revision,
		Payload:     json.RawMessage(`{"task":"lado-task-1"}`),
	}
}

func seedLadoTaskWithPlan(t *testing.T, s *sqlite.Store) {
	t.Helper()
	seedProject(t, s, "mer")
	mustCreateLadoTask(t, s, importedLadoTask("lado-task-1", "github:acme/demo#12"))
	mustLadoPlanRequest(t, s, "req-1", "lado-task-1", ladoPlanAt)
}

func TestLadoPlanRequestDeliveryStates(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedLadoTaskWithPlan(t, s)

	if err := s.RecordLadoPlanRequestAttempt(ctx, "req-1", "orchestrator starting"); err != nil {
		t.Fatal(err)
	}
	req, ok, err := s.LadoPlanRequest(ctx, "req-1")
	if err != nil || !ok || req.DeliveryAttempts != 1 || req.DeliveryError != "orchestrator starting" || !req.DeliveredAt.IsZero() {
		t.Fatalf("after a failed attempt: %+v %v %v", req, ok, err)
	}
	delivered := ladoPlanAt.Add(10 * time.Second)
	if err := s.MarkLadoPlanRequestDelivered(ctx, "req-1", delivered); err != nil {
		t.Fatal(err)
	}
	if err := s.FailLadoPlanRequest(ctx, "req-1", delivered.Add(time.Hour), "late"); err != nil {
		t.Fatal(err)
	}
	req, _, _ = s.LadoPlanRequest(ctx, "req-1")
	if !req.DeliveredAt.Equal(delivered) || req.DeliveryAttempts != 2 || req.DeliveryError != "" || !req.FailedAt.IsZero() {
		t.Fatalf("delivered request = %+v, want delivered once and never failed", req)
	}

	mustLadoPlanRequest(t, s, "req-2", "lado-task-1", ladoPlanAt.Add(time.Minute))
	if err := s.FailLadoPlanRequest(ctx, "req-2", ladoPlanAt.Add(20*time.Minute), "orchestrator exited"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkLadoPlanRequestDelivered(ctx, "req-2", ladoPlanAt.Add(21*time.Minute)); err != nil {
		t.Fatal(err)
	}
	req, _, _ = s.LadoPlanRequest(ctx, "req-2")
	if req.FailedAt.IsZero() || !req.DeliveredAt.IsZero() || req.DeliveryError != "orchestrator exited" {
		t.Fatalf("given-up request = %+v, want failed and never delivered", req)
	}
	live, err := s.ListLadoPlanRequestsForLiveTasks(ctx)
	if err != nil || len(live) != 2 || live[0].ID != "req-1" || live[1].ID != "req-2" {
		t.Fatalf("ListLadoPlanRequestsForLiveTasks = %+v, %v", live, err)
	}
	if _, _, err := s.CancelLadoTask(ctx, "lado-task-1", ladoPlanAt.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if live, _ := s.ListLadoPlanRequestsForLiveTasks(ctx); len(live) != 0 {
		t.Fatalf("a cancelled task still has %d live requests", len(live))
	}
}

func TestPinLadoPlanRevisionNumbersAndDeduplicates(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedLadoTaskWithPlan(t, s)

	first := mustPin(t, s, ladoRevision("lado-plan-1", "lado-task-1", "req-1", 1, ladoSHA('a')))
	if first.Number != 1 {
		t.Fatalf("first revision number = %d", first.Number)
	}
	if _, created, err := s.PinLadoPlanRevision(ctx, ladoRevision("lado-plan-dup", "lado-task-1", "req-1", 1, ladoSHA('a'))); err != nil || created {
		t.Fatalf("pinning the same artifact version again = (%v, %v), want no new revision", created, err)
	}
	withProblems := ladoRevision("lado-plan-2", "lado-task-1", "req-1", 2, ladoSHA('b'))
	withProblems.Source = "user"
	withProblems.Problems = []domain.LadoPlanProblem{{Code: domain.LadoPlanMissingField, Field: "steps"}}
	second := mustPin(t, s, withProblems)
	if second.Number != 2 || second.Valid() || second.Source != "user" {
		t.Fatalf("second revision = %+v", second)
	}
	pinned, err := s.LadoArtifactVersionPinned(ctx, "mer-orchestrator-1", domain.LadoPlanPath("lado-task-1"), 2, ladoSHA('b'))
	if err != nil || !pinned {
		t.Fatalf("LadoArtifactVersionPinned = (%v, %v)", pinned, err)
	}
	got, ok, err := s.LadoPlanRevision(ctx, "lado-plan-2")
	if err != nil || !ok || len(got.Problems) != 1 || got.Problems[0].Field != "steps" {
		t.Fatalf("LadoPlanRevision = %+v %v %v", got, ok, err)
	}
	all, err := s.ListLadoPlanRevisions(ctx, "lado-task-1")
	if err != nil || len(all) != 2 || all[0].ID != "lado-plan-1" || len(all[0].Problems) != 0 {
		t.Fatalf("ListLadoPlanRevisions = %+v, %v", all, err)
	}
}

func TestLadoTaskReadsCarryPlanState(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedLadoTaskWithPlan(t, s)
	mustCreateLadoTask(t, s, importedLadoTask("lado-task-2", "github:acme/demo#13"))

	state := func(id domain.LadoTaskID) domain.LadoTaskState {
		t.Helper()
		task, ok, err := s.LadoTask(ctx, id)
		if err != nil || !ok {
			t.Fatalf("LadoTask %s: %v %v", id, ok, err)
		}
		tasks, err := s.ListLadoTasks(ctx, "mer", false)
		if err != nil {
			t.Fatal(err)
		}
		for _, listed := range tasks {
			if listed.ID == id && listed.State() != task.State() {
				t.Fatalf("list state %q != get state %q", listed.State(), task.State())
			}
		}
		return task.State()
	}
	task, _, _ := s.LadoTask(ctx, "lado-task-1")
	if state("lado-task-1") != domain.LadoTaskDraft || !task.Plan.RequestPending() {
		t.Fatalf("requested task: state %q pending %v, want a pending Draft", task.State(), task.Plan.RequestPending())
	}
	rev := mustPin(t, s, ladoRevision("lado-plan-1", "lado-task-1", "req-1", 1, ladoSHA('a')))
	if got := state("lado-task-1"); got != domain.LadoTaskPlanned {
		t.Fatalf("state with a revision = %q, want planned", got)
	}
	if _, _, err := s.ApproveLadoPlan(ctx, domain.LadoPlanApproval{
		ID: "lado-approval-1", RevisionID: rev.ID, TaskID: "lado-task-1", SHA256: rev.SHA256, ApprovedAt: ladoPlanAt.Add(time.Minute),
	}, ladoProof("nonce-1", string(rev.ID)), ladoApprovalEvent(string(rev.ID)), nil); err != nil {
		t.Fatalf("ApproveLadoPlan: %v", err)
	}
	if got := state("lado-task-1"); got != domain.LadoTaskApproved {
		t.Fatalf("state after approval = %q, want approved", got)
	}
	updated, ok, err := s.SetLadoTaskPreferences(ctx, "lado-task-1", "codex", "", ladoPlanAt.Add(2*time.Minute))
	if err != nil || !ok || updated.State() != domain.LadoTaskApproved {
		t.Fatalf("SetLadoTaskPreferences state = %q (%v %v), want approved", updated.State(), ok, err)
	}
	// Gate 2: an edit after approval is a new revision; the old approval no
	// longer counts.
	mustPin(t, s, ladoRevision("lado-plan-2", "lado-task-1", "req-1", 2, ladoSHA('b')))
	if got := state("lado-task-1"); got != domain.LadoTaskPlanned {
		t.Fatalf("state after an edit = %q, want planned", got)
	}
	if got := state("lado-task-2"); got != domain.LadoTaskDraft {
		t.Fatalf("task without plans = %q, want draft", got)
	}
}

func TestApproveLadoPlanRecordsProofAndAuditTogether(t *testing.T) {
	dir := t.TempDir()
	s, err := sqlitetest.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	seedLadoTaskWithPlan(t, s)
	rev := mustPin(t, s, ladoRevision("lado-plan-1", "lado-task-1", "req-1", 1, ladoSHA('a')))
	approval := domain.LadoPlanApproval{ID: "lado-approval-1", RevisionID: rev.ID, TaskID: "lado-task-1", SHA256: rev.SHA256, ApprovedAt: ladoPlanAt.Add(time.Minute)}
	stored, event, err := s.ApproveLadoPlan(ctx, approval, ladoProof("nonce-1", string(rev.ID)), ladoApprovalEvent(string(rev.ID)), nil)
	if err != nil {
		t.Fatalf("ApproveLadoPlan: %v", err)
	}
	if stored.ProofNonce != "nonce-1" || stored.SHA256 != rev.SHA256 || event.ID == 0 || event.Action != "plan.approved" {
		t.Fatalf("approval %+v, event %+v", stored, event)
	}
	if broken, err := s.VerifyLadoAuditChain(ctx); err != nil || broken != 0 {
		t.Fatalf("VerifyLadoAuditChain = (%d, %v)", broken, err)
	}

	again := approval
	again.ID = "lado-approval-2"
	if _, _, err := s.ApproveLadoPlan(ctx, again, ladoProof("nonce-1", string(rev.ID)), ladoApprovalEvent(string(rev.ID)), nil); !errors.Is(err, domain.ErrLadoPresenceReplayed) {
		t.Fatalf("a replayed nonce = %v, want ErrLadoPresenceReplayed", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// Gate 10: the nonce stays used after a daemon restart.
	reopened, err := sqlite.OpenPreMigrated(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if _, _, err := reopened.ApproveLadoPlan(ctx, again, ladoProof("nonce-1", string(rev.ID)), ladoApprovalEvent(string(rev.ID)), nil); !errors.Is(err, domain.ErrLadoPresenceReplayed) {
		t.Fatalf("a nonce replayed after a restart = %v, want ErrLadoPresenceReplayed", err)
	}
	approvals, err := reopened.ListLadoPlanApprovals(ctx, "lado-task-1")
	if err != nil || len(approvals) != 1 {
		t.Fatalf("approvals = %+v, %v; want only the first", approvals, err)
	}
}

func TestApproveLadoPlanRefusesASupersededRevision(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedLadoTaskWithPlan(t, s)
	first := mustPin(t, s, ladoRevision("lado-plan-1", "lado-task-1", "req-1", 1, ladoSHA('a')))
	mustPin(t, s, ladoRevision("lado-plan-2", "lado-task-1", "req-1", 2, ladoSHA('b')))

	for name, approval := range map[string]domain.LadoPlanApproval{
		"older revision":   {ID: "a-1", RevisionID: first.ID, TaskID: "lado-task-1", SHA256: first.SHA256, ApprovedAt: ladoPlanAt},
		"other bytes":      {ID: "a-2", RevisionID: "lado-plan-2", TaskID: "lado-task-1", SHA256: ladoSHA('a'), ApprovedAt: ladoPlanAt},
		"unknown revision": {ID: "a-3", RevisionID: "lado-plan-9", TaskID: "lado-task-1", SHA256: ladoSHA('b'), ApprovedAt: ladoPlanAt},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := s.ApproveLadoPlan(ctx, approval, ladoProof("nonce-"+name, string(approval.RevisionID)), ladoApprovalEvent(string(approval.RevisionID)), nil); !errors.Is(err, domain.ErrLadoPlanSuperseded) {
				t.Fatalf("ApproveLadoPlan = %v, want ErrLadoPlanSuperseded", err)
			}
		})
	}
	// Nothing of the refused approvals was kept: not the proofs, not the
	// audit events.
	if approvals, _ := s.ListLadoPlanApprovals(ctx, "lado-task-1"); len(approvals) != 0 {
		t.Fatalf("refused approvals left %d rows", len(approvals))
	}
	ok := domain.LadoPlanApproval{ID: "a-4", RevisionID: "lado-plan-2", TaskID: "lado-task-1", SHA256: ladoSHA('b'), ApprovedAt: ladoPlanAt}
	_, event, err := s.ApproveLadoPlan(ctx, ok, ladoProof("nonce-older revision", "lado-plan-2"), ladoApprovalEvent("lado-plan-2"), nil)
	if err != nil {
		t.Fatalf("approving the latest revision with a nonce a refused call carried = %v, want success", err)
	}
	if event.ID != 1 {
		t.Fatalf("audit event id = %d, want 1: refused approvals must not append", event.ID)
	}
}

func TestSetLadoModeWithProof(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	if _, err := s.SetLadoMode(ctx, "mer", true, ladoPlanAt); err != nil {
		t.Fatal(err)
	}
	proof := domain.LadoPresenceProof{Nonce: "nonce-off", Action: domain.LadoPresenceModeOff, SubjectID: "mer", ExpiresAt: ladoPlanAt.Add(time.Minute), UsedAt: ladoPlanAt}
	event := domain.LadoAuditEvent{At: ladoPlanAt, Actor: "human", Action: "project.lado_mode_off", SubjectKind: "project", SubjectID: "mer"}
	settings, err := s.SetLadoModeWithProof(ctx, "mer", false, ladoPlanAt, proof, event)
	if err != nil || settings.LadoMode {
		t.Fatalf("SetLadoModeWithProof = (%+v, %v), want mode off", settings, err)
	}
	if _, err := s.SetLadoModeWithProof(ctx, "mer", false, ladoPlanAt, proof, event); !errors.Is(err, domain.ErrLadoPresenceReplayed) {
		t.Fatalf("a replayed proof = %v, want ErrLadoPresenceReplayed", err)
	}
}

func TestLadoPlanChangesPublishTaskChangeEvents(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	mustCreateLadoTask(t, s, importedLadoTask("lado-task-1", "github:acme/demo#12"))
	baseline, err := s.EventsAfter(ctx, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var seq int64
	if len(baseline) > 0 {
		seq = baseline[len(baseline)-1].Seq
	}
	expectEvents := func(step string, want int) {
		t.Helper()
		events, err := s.EventsAfter(ctx, seq, 1000)
		if err != nil {
			t.Fatalf("%s: %v", step, err)
		}
		if len(events) != want {
			t.Fatalf("%s: %d change events, want %d", step, len(events), want)
		}
		for _, ev := range events {
			seq = ev.Seq
			var payload struct {
				Kind   string `json:"kind"`
				TaskID string `json:"taskId"`
			}
			_ = json.Unmarshal(ev.Payload, &payload)
			if ev.ProjectID != "mer" || payload.Kind != "lado_task" || payload.TaskID != "lado-task-1" {
				t.Fatalf("%s: event %+v payload %+v", step, ev, payload)
			}
		}
	}
	mustLadoPlanRequest(t, s, "req-1", "lado-task-1", ladoPlanAt)
	expectEvents("request", 1)
	if err := s.RecordLadoPlanRequestAttempt(ctx, "req-1", "starting"); err != nil {
		t.Fatal(err)
	}
	expectEvents("failed attempt", 0)
	if err := s.MarkLadoPlanRequestDelivered(ctx, "req-1", ladoPlanAt); err != nil {
		t.Fatal(err)
	}
	expectEvents("delivered", 1)
	rev := mustPin(t, s, ladoRevision("lado-plan-1", "lado-task-1", "req-1", 1, ladoSHA('a')))
	expectEvents("revision", 1)
	if _, _, err := s.ApproveLadoPlan(ctx, domain.LadoPlanApproval{ID: "a-1", RevisionID: rev.ID, TaskID: "lado-task-1", SHA256: rev.SHA256, ApprovedAt: ladoPlanAt},
		ladoProof("nonce-1", string(rev.ID)), ladoApprovalEvent(string(rev.ID)), nil); err != nil {
		t.Fatal(err)
	}
	expectEvents("approval", 1)
}

// ladoPolicySnapshot is a rule snapshot over the active versions of rules,
// with the sha256 of its content. The store gives it its approval's id and
// time.
func ladoPolicySnapshot(id, content string, blocking bool, rules ...domain.LadoRule) *domain.LadoPolicySnapshot {
	used := make([]domain.LadoPolicySnapshotRule, 0, len(rules))
	for _, rule := range rules {
		used = append(used, domain.LadoPolicySnapshotRule{RuleID: rule.ID, VersionID: rule.Version.ID})
	}
	sum := sha256.Sum256([]byte(content))
	return &domain.LadoPolicySnapshot{
		ID:       domain.LadoPolicySnapshotID(id),
		SHA256:   hex.EncodeToString(sum[:]),
		Content:  []byte(content),
		Blocking: blocking,
		Rules:    used,
	}
}

// mustApproveLadoPlan approves rev a minute after it was pinned.
func mustApproveLadoPlan(t *testing.T, s *sqlite.Store, id string, rev domain.LadoPlanRevision, nonce string, snapshot *domain.LadoPolicySnapshot) domain.LadoPlanApproval {
	t.Helper()
	approval, _, err := s.ApproveLadoPlan(context.Background(), domain.LadoPlanApproval{
		ID: domain.LadoPlanApprovalID(id), RevisionID: rev.ID, TaskID: rev.TaskID, SHA256: rev.SHA256, ApprovedAt: rev.CreatedAt.Add(time.Minute),
	}, ladoProof(nonce, string(rev.ID)), ladoApprovalEvent(string(rev.ID)), snapshot)
	if err != nil {
		t.Fatalf("ApproveLadoPlan %s: %v", id, err)
	}
	return approval
}

// countLadoTables counts the rows of each table.
func countLadoTables(t *testing.T, raw *sql.DB, tables ...string) map[string]int {
	t.Helper()
	counts := make(map[string]int, len(tables))
	for _, table := range tables {
		var n int
		if err := raw.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		counts[table] = n
	}
	return counts
}

func TestApproveLadoPlanStoresTheRuleSnapshot(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedLadoTaskWithPlan(t, s)
	first := mustCreateLadoRule(t, s, ladoUserRule("lado-rule-b", ladoRuleAt))
	second := mustCreateLadoRule(t, s, ladoUserRule("lado-rule-a", ladoRuleAt.Add(time.Minute)))
	rev := mustPin(t, s, ladoRevision("lado-plan-1", "lado-task-1", "req-1", 1, ladoSHA('a')))

	// Spacing, an escape and text any re-encoding would change.
	content := `{"compiler":"lado-policy/1",  "prompt":"<b>커밋</b> & a\/b", "rules":[ ]}`
	snapshot := ladoPolicySnapshot("lado-snapshot-1", content, true, first, second)
	snapshot.ApprovalID = "lado-approval-other"
	approval := mustApproveLadoPlan(t, s, "lado-approval-1", rev, "nonce-1", snapshot)

	got, ok, err := s.LadoPolicySnapshot(ctx, approval.ID)
	if err != nil || !ok {
		t.Fatalf("LadoPolicySnapshot = (%v, %v), want the approval's snapshot", ok, err)
	}
	if string(got.Content) != content {
		t.Fatalf("snapshot content = %s, want the approved bytes %s", got.Content, content)
	}
	want := domain.LadoPolicySnapshot{
		ID:         "lado-snapshot-1",
		ApprovalID: approval.ID,
		SHA256:     snapshot.SHA256,
		Content:    []byte(content),
		Blocking:   true,
		// Ordered by rule id.
		Rules: []domain.LadoPolicySnapshotRule{
			{RuleID: "lado-rule-a", VersionID: "lado-rule-a-v1"},
			{RuleID: "lado-rule-b", VersionID: "lado-rule-b-v1"},
		},
		CreatedAt: approval.ApprovedAt,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LadoPolicySnapshot = %+v, want %+v", got, want)
	}
	if _, ok, err := s.LadoPolicySnapshot(ctx, "lado-approval-9"); err != nil || ok {
		t.Fatalf("LadoPolicySnapshot of an unknown approval = (%v, %v), want none", ok, err)
	}
	if broken, err := s.VerifyLadoAuditChain(ctx); err != nil || broken != 0 {
		t.Fatalf("VerifyLadoAuditChain = (%d, %v)", broken, err)
	}
}

func TestApproveLadoPlanRollsBackWhenTheSnapshotFails(t *testing.T) {
	s, raw := openLadoAuditStore(t)
	ctx := context.Background()
	seedLadoTaskWithPlan(t, s)
	mustCreateLadoTask(t, s, importedLadoTask("lado-task-2", "github:acme/demo#13"))
	mustLadoPlanRequest(t, s, "req-2", "lado-task-2", ladoPlanAt)
	rule := mustCreateLadoRule(t, s, ladoUserRule("lado-rule-1", ladoRuleAt))
	first := mustPin(t, s, ladoRevision("lado-plan-1", "lado-task-1", "req-1", 1, ladoSHA('a')))
	mustApproveLadoPlan(t, s, "lado-approval-1", first, "nonce-1", ladoPolicySnapshot("lado-snapshot-1", `{}`, false, rule))
	rev := mustPin(t, s, ladoRevision("lado-plan-2", "lado-task-2", "req-2", 1, ladoSHA('b')))

	tables := []string{"lado_plan_approvals", "lado_presence_proofs", "lado_audit_events", "lado_policy_snapshots", "lado_policy_snapshot_rules"}
	before := countLadoTables(t, raw, tables...)
	unknownVersion := ladoPolicySnapshot("lado-snapshot-2", `{}`, false)
	unknownVersion.Rules = []domain.LadoPolicySnapshotRule{{RuleID: rule.ID, VersionID: "lado-rule-1-v9"}}
	approval := domain.LadoPlanApproval{ID: "lado-approval-2", RevisionID: rev.ID, TaskID: rev.TaskID, SHA256: rev.SHA256, ApprovedAt: ladoPlanAt.Add(time.Hour)}
	for name, snapshot := range map[string]*domain.LadoPolicySnapshot{
		"reused snapshot id":   ladoPolicySnapshot("lado-snapshot-1", `{}`, false, rule),
		"unknown rule version": unknownVersion,
	} {
		_, _, err := s.ApproveLadoPlan(ctx, approval, ladoProof("nonce-2", string(rev.ID)), ladoApprovalEvent(string(rev.ID)), snapshot)
		if err == nil || errors.Is(err, domain.ErrLadoPresenceReplayed) || errors.Is(err, domain.ErrLadoPlanSuperseded) {
			t.Fatalf("%s: ApproveLadoPlan = %v, want the snapshot's insert error", name, err)
		}
		if after := countLadoTables(t, raw, tables...); !reflect.DeepEqual(after, before) {
			t.Fatalf("%s: rows = %v, want %v: nothing of a failed approval stays", name, after, before)
		}
	}

	// The nonce was not used up, so the same approval succeeds with it.
	_, event, err := s.ApproveLadoPlan(ctx, approval, ladoProof("nonce-2", string(rev.ID)), ladoApprovalEvent(string(rev.ID)), ladoPolicySnapshot("lado-snapshot-2", `{}`, false, rule))
	if err != nil {
		t.Fatalf("ApproveLadoPlan after the failures = %v, want success", err)
	}
	if want := int64(before["lado_audit_events"] + 1); event.ID != want {
		t.Fatalf("audit event id = %d, want %d: failed approvals must not append", event.ID, want)
	}
	if broken, err := s.VerifyLadoAuditChain(ctx); err != nil || broken != 0 {
		t.Fatalf("VerifyLadoAuditChain = (%d, %v)", broken, err)
	}
}

func TestLadoTaskReadsCarryTheRuleSnapshot(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedLadoTaskWithPlan(t, s)
	mustCreateLadoTask(t, s, importedLadoTask("lado-task-2", "github:acme/demo#13"))

	// read returns the task's snapshot and state, and checks that the project
	// list carries the same snapshot.
	read := func(id domain.LadoTaskID) (domain.LadoPolicySnapshotRef, domain.LadoTaskState) {
		t.Helper()
		task, ok, err := s.LadoTask(ctx, id)
		if err != nil || !ok {
			t.Fatalf("LadoTask %s: %v %v", id, ok, err)
		}
		tasks, err := s.ListLadoTasks(ctx, "mer", false)
		if err != nil {
			t.Fatal(err)
		}
		listed := false
		for _, other := range tasks {
			if other.ID == id {
				listed = true
				if other.Plan.Snapshot != task.Plan.Snapshot {
					t.Fatalf("list snapshot %+v != get snapshot %+v", other.Plan.Snapshot, task.Plan.Snapshot)
				}
			}
		}
		if !listed {
			t.Fatalf("ListLadoTasks left out %s", id)
		}
		return task.Plan.Snapshot, task.State()
	}

	first := ladoPolicySnapshot("lado-snapshot-1", `{"n":1}`, false)
	mustApproveLadoPlan(t, s, "lado-approval-1", mustPin(t, s, ladoRevision("lado-plan-1", "lado-task-1", "req-1", 1, ladoSHA('a'))), "nonce-1", first)
	if ref, state := read("lado-task-1"); ref != (domain.LadoPolicySnapshotRef{ID: first.ID, SHA256: first.SHA256}) || state != domain.LadoTaskApproved {
		t.Fatalf("after the first approval: snapshot %+v state %q, want %s and approved", ref, state, first.ID)
	}
	blocking := ladoPolicySnapshot("lado-snapshot-2", `{"n":2}`, true)
	mustApproveLadoPlan(t, s, "lado-approval-2", mustPin(t, s, ladoRevision("lado-plan-2", "lado-task-1", "req-1", 2, ladoSHA('b'))), "nonce-2", blocking)
	if ref, state := read("lado-task-1"); ref != (domain.LadoPolicySnapshotRef{ID: blocking.ID, SHA256: blocking.SHA256, Blocking: true}) || state != domain.LadoTaskBlocked {
		t.Fatalf("after a blocking approval: snapshot %+v state %q, want %s and blocked", ref, state, blocking.ID)
	}
	// The newest approval decides, and one given without rules has no
	// snapshot even after approvals that had one.
	mustApproveLadoPlan(t, s, "lado-approval-3", mustPin(t, s, ladoRevision("lado-plan-3", "lado-task-1", "req-1", 3, ladoSHA('c'))), "nonce-3", nil)
	if ref, state := read("lado-task-1"); ref != (domain.LadoPolicySnapshotRef{}) || state != domain.LadoTaskApproved {
		t.Fatalf("after an approval without rules: snapshot %+v state %q, want none and approved", ref, state)
	}
	if ref, _ := read("lado-task-2"); ref != (domain.LadoPolicySnapshotRef{}) {
		t.Fatalf("task without approvals: snapshot %+v, want none", ref)
	}
}
