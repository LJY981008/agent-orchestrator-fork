package daemon

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopolicy"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoproject"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/ladoblob"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

type ladoGateSink struct {
	gate   ports.WorkGate
	prompt sessionmanager.LadoPromptSection
}

func (s *ladoGateSink) SetWorkGate(gate ports.WorkGate) { s.gate = gate }

func (s *ladoGateSink) SetLadoPrompt(section sessionmanager.LadoPromptSection) { s.prompt = section }

// SetWorkGate and SetLadoPrompt keep wiring_test.go's fake a sessionLifecycle.
func (f *fakeSessionLifecycle) SetWorkGate(ports.WorkGate) {}

func (f *fakeSessionLifecycle) SetLadoPrompt(sessionmanager.LadoPromptSection) {}

func TestLadoDevModeReadsAOToggleValues(t *testing.T) {
	cases := map[string]bool{
		"": false, "on": true, "ON": true, " true ": true, "1": true, "yes": true,
		"off": false, "0": false, "no": false, "maybe": false,
	}
	for raw, want := range cases {
		if got := ladoDevMode(raw); got != want {
			t.Errorf("ladoDevMode(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestWireLadoInstallsOneGateOnEverySendAndSpawnPath(t *testing.T) {
	t.Setenv(ladoDevEnv, "on")
	ctx := context.Background()
	store := sqlitetest.MustOpen(t)
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "mer", Path: "/tmp/mer", RegisteredAt: time.Now().UTC().Truncate(time.Second)}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	sessions, chat := &ladoGateSink{}, &ladoGateSink{}

	projects := wireLado(store, sessions, chat, slog.New(slog.DiscardHandler))

	if sessions.gate == nil || sessions.gate != chat.gate {
		t.Fatalf("session manager gate %v, chat gate %v; want one gate on both", sessions.gate, chat.gate)
	}
	on := true
	if _, err := projects.UpdateSettings(ctx, "mer", ladoproject.UpdateInput{LadoMode: &on}); err != nil {
		t.Fatalf("turn LADO mode on: %v", err)
	}
	err := sessions.gate.AuthorizeSpawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker})
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "LADO_TASK_REQUIRED" {
		t.Fatalf("worker spawn after LADO mode on = %v, want LADO_TASK_REQUIRED", err)
	}
	if sessions.prompt == nil {
		t.Fatal("wireLado installed no LADO prompt section")
	}
	if section, err := sessions.prompt(ctx, "mer"); err != nil || !strings.Contains(section, "## LADO project") {
		t.Fatalf("LADO prompt section for a LADO project = %q, %v", section, err)
	}
}

func TestWireLadoKeepsTheModeSwitchOffByDefault(t *testing.T) {
	t.Setenv(ladoDevEnv, "")
	ctx := context.Background()
	store := sqlitetest.MustOpen(t)
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "mer", Path: "/tmp/mer", RegisteredAt: time.Now().UTC().Truncate(time.Second)}); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	projects := wireLado(store, &ladoGateSink{}, &ladoGateSink{}, slog.New(slog.DiscardHandler))

	on := true
	_, err := projects.UpdateSettings(ctx, "mer", ladoproject.UpdateInput{LadoMode: &on})
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "LADO_MODE_UNAVAILABLE" {
		t.Fatalf("turn LADO mode on without %s = %v, want LADO_MODE_UNAVAILABLE", ladoDevEnv, err)
	}
}

func TestWireLadoWorkSeedsBuiltinRules(t *testing.T) {
	ctx := context.Background()
	useLadoStoredTokens(t, nil)
	cfg := config.Config{DataDir: t.TempDir(), StateDir: t.TempDir()}
	cfg.Telemetry.AppVersion = "0.12.0"
	store := sqlitetest.MustOpen(t)
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "mer", Path: t.TempDir(), RegisteredAt: time.Now().UTC().Truncate(time.Second)}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	log := slog.New(slog.DiscardHandler)

	_, tasks, rules := wireLadoWork(ctx, cfg, store, &recordingProjectCloner{}, ladoWorkDeps{}, log)

	if tasks == nil || rules == nil {
		t.Fatalf("wireLadoWork returned tasks %v, rules %v", tasks, rules)
	}
	// stored maps each rule in the store to its active version.
	stored := func() map[domain.LadoRuleID]int {
		t.Helper()
		list, err := store.ListLadoRules(ctx, true)
		if err != nil {
			t.Fatalf("ListLadoRules: %v", err)
		}
		got := map[domain.LadoRuleID]int{}
		for _, rule := range list {
			if !rule.Builtin {
				t.Fatalf("rule %s is not built-in", rule.ID)
			}
			got[rule.ID] = rule.Version.Number
		}
		return got
	}
	want := map[domain.LadoRuleID]int{domain.LadoRulePlanApproval: 1, domain.LadoRulePermissionCeiling: 1, domain.LadoRuleKoreanCommits: 1}
	if got := stored(); !reflect.DeepEqual(got, want) {
		t.Fatalf("rules after wiring = %v, want the three built-ins at version 1", got)
	}

	// A restart seeds nothing again.
	wireLadoWork(ctx, cfg, store, &recordingProjectCloner{}, ladoWorkDeps{}, log)
	if got := stored(); !reflect.DeepEqual(got, want) {
		t.Fatalf("rules after a second wiring = %v, want %v", got, want)
	}

	// The returned service resolves the seeded rules and stamps the running
	// AO build into every snapshot.
	view, err := rules.Effective(ctx, ladopolicy.EffectiveInput{ProjectID: "mer"})
	if err != nil || len(view.Entries) != 3 || view.Ceiling != domain.PermissionModeAuto || view.Content.AOBuild != "0.12.0" {
		t.Fatalf("Effective = %d rules, ceiling %q, AO build %q, err %v", len(view.Entries), view.Ceiling, view.Content.AOBuild, err)
	}
}

// TestWireLadoWorkGivesTasksTheRules checks that tasks read the rules: an
// approval given before LADO had rules carries no snapshot, so once the rules
// apply the task needs a new approval.
func TestWireLadoWorkGivesTasksTheRules(t *testing.T) {
	ctx := context.Background()
	useLadoStoredTokens(t, nil)
	cfg := config.Config{DataDir: t.TempDir(), StateDir: t.TempDir()}
	store := sqlitetest.MustOpen(t)
	at := time.Now().UTC().Truncate(time.Second)
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "mer", Path: t.TempDir(), RegisteredAt: at}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	issue := strings.Repeat("1", 64)
	blobs := ladoblob.New(cfg.StateDir)
	t.Cleanup(func() { _ = blobs.Close() })
	sum, err := blobs.Put([]byte("---\npermission_ceiling: accept-edits\n---\n\nPlan.\n"))
	if err != nil {
		t.Fatalf("pin the plan: %v", err)
	}
	if _, err := store.CreateLadoTask(ctx, domain.LadoTask{
		ID: "lado-task-1", ProjectID: "mer", Repo: domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "acme/demo"},
		IssueRef: "github:acme/demo#12", Agent: "claude-code", CreatedAt: at, UpdatedAt: at,
		Issue: domain.LadoObservedIssue{Title: "Add login", State: domain.IssueOpen, URL: "https://github.com/acme/demo/issues/12", SnapshotSHA256: issue, ObservedAt: at},
	}); err != nil {
		t.Fatalf("CreateLadoTask: %v", err)
	}
	if _, err := store.CreateLadoPlanRequest(ctx, domain.LadoPlanRequest{ID: "req-1", TaskID: "lado-task-1", SessionID: "mer-orchestrator-1", IssueSnapshotSHA256: issue, RequestedAt: at}); err != nil {
		t.Fatalf("CreateLadoPlanRequest: %v", err)
	}
	rev, _, err := store.PinLadoPlanRevision(ctx, domain.LadoPlanRevision{
		ID: "lado-plan-1", TaskID: "lado-task-1", RequestID: "req-1", SessionID: "mer-orchestrator-1", Path: domain.LadoPlanPath("lado-task-1"),
		ArtifactVersion: 1, Source: "agent", SHA256: sum, BaseRef: "main", BaseSHA: strings.Repeat("c", 40), IssueSnapshotSHA256: issue, CreatedAt: at,
	})
	if err != nil {
		t.Fatalf("PinLadoPlanRevision: %v", err)
	}
	proof := domain.LadoPresenceProof{Nonce: "AAAAAAAAAAAAAAAAAAAAAA", Action: domain.LadoPresencePlanApprove, SubjectID: string(rev.ID), ExpiresAt: at.Add(time.Minute), UsedAt: at}
	event := domain.LadoAuditEvent{At: at, Actor: "human", Action: "plan.approved", SubjectKind: "plan_revision", SubjectID: string(rev.ID), Payload: []byte("{}")}
	if _, _, err := store.ApproveLadoPlan(ctx, domain.LadoPlanApproval{ID: "approval-1", RevisionID: rev.ID, TaskID: rev.TaskID, SHA256: sum, ApprovedAt: at}, proof, event, nil); err != nil {
		t.Fatalf("ApproveLadoPlan: %v", err)
	}

	_, tasks, rules := wireLadoWork(ctx, cfg, store, &recordingProjectCloner{}, ladoWorkDeps{}, slog.New(slog.DiscardHandler))

	listed, err := tasks.List(ctx, "mer", false)
	if err != nil || len(listed) != 1 {
		t.Fatalf("List = %v, %v", listed, err)
	}
	want, err := rules.CurrentSHA256(ctx, listed[0])
	if err != nil {
		t.Fatalf("CurrentSHA256: %v", err)
	}
	if got := listed[0]; got.Plan.PolicySHA256 != want || got.State() != domain.LadoTaskReviewRequired {
		t.Fatalf("task rules %q state %q, want %q and %q", got.Plan.PolicySHA256, got.State(), want, domain.LadoTaskReviewRequired)
	}
}
