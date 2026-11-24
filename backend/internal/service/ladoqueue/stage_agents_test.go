package ladoqueue

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopolicy"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// stagedSnapshot is a lado-policy/4 snapshot of task-1 whose stages each
// have their own model: the execution opus at high, integration sonnet at
// low, resolve haiku with no effort. It keeps the test snapshot's hash.
func stagedSnapshot() domain.LadoPolicySnapshot {
	content, err := json.Marshal(ladopolicy.SnapshotContent{
		Compiler: ladopolicy.Compiler, ProjectID: "proj-1", TaskID: "task-1",
		Agent: ladopolicy.SnapshotAgent{Harness: domain.HarnessClaudeCode, Model: "opus", Effort: "high", PermissionMode: domain.PermissionModeAcceptEdits},
		Stages: ladopolicy.SnapshotStages{
			Integration: ladopolicy.SnapshotStageAgent{Model: "sonnet", Effort: "low"},
			Resolve:     ladopolicy.SnapshotStageAgent{Model: "haiku"},
		},
		PermissionCeiling: domain.PermissionModeAuto, Prompt: "## LADO rules\n\nRun the tests before you push.\n",
	})
	if err != nil {
		panic(err)
	}
	return domain.LadoPolicySnapshot{ID: "snap-1", ApprovalID: "approval-1", SHA256: testSnapshotSHA, Content: content}
}

// The worker configs each role gets under stagedSnapshot.
var (
	stagedExecution   = ports.AgentConfig{Model: "opus", Effort: "high", Permissions: domain.PermissionModeAcceptEdits}
	stagedIntegration = ports.AgentConfig{Model: "sonnet", Effort: "low", Permissions: domain.PermissionModeAcceptEdits}
	stagedResolve     = ports.AgentConfig{Model: "haiku", Permissions: domain.PermissionModeAcceptEdits}
)

// stagePayloadOK reports whether an audit payload names the stage and the
// model and effort of cfg on Claude Code.
func stagePayloadOK(payload json.RawMessage, stage domain.LadoStage, cfg ports.AgentConfig) error {
	var got map[string]any
	if err := json.Unmarshal(payload, &got); err != nil {
		return err
	}
	if got["stage"] != string(stage) || got["harness"] != string(domain.HarnessClaudeCode) || got["model"] != cfg.Model || got["effort"] != cfg.Effort {
		return fmt.Errorf("payload %s, want stage %s, claude-code, model %q, effort %q", payload, stage, cfg.Model, cfg.Effort)
	}
	return nil
}

// storeAudits returns the payloads of the store fixture's audit events of
// action, oldest first.
func storeAudits(t *testing.T, sf *storeFixture, action string) []json.RawMessage {
	t.Helper()
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(sf.dir, "ao.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	rows, err := raw.Query(`SELECT payload_json FROM lado_audit_events WHERE action = ? ORDER BY id`, action)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var payloads []json.RawMessage
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			t.Fatal(err)
		}
		payloads = append(payloads, json.RawMessage(p))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return payloads
}

// A single execution spawns with the snapshot's execution model and effort
// and records them; a lado-policy/3 snapshot, which has neither an effort
// nor stages, spawns as before, with no effort.
func TestASingleExecutionSpawnsWithItsStageAgent(t *testing.T) {
	v3 := testSnapshot(claudeAgent(), false)
	v3.Content = json.RawMessage(`{"compiler":"lado-policy/3","aoBuild":"","projectId":"proj-1","taskId":"task-1",` +
		`"agent":{"harness":"claude-code","model":"claude-sonnet-5","permissionMode":"accept-edits"},"permissionCeiling":"auto",` +
		`"rules":[],"aoRules":[],"setup":[],"aoSystem":{"sha256":""},"acceptedConflicts":[],"prompt":"## LADO rules\n\nRun the tests before you push.\n",` +
		`"workflowGates":[],"validators":[],"capabilities":[],"problems":[]}`)
	for _, c := range []struct {
		name     string
		snapshot domain.LadoPolicySnapshot
		want     ports.AgentConfig
	}{
		{"lado-policy/3", v3, ports.AgentConfig{Model: "claude-sonnet-5", Permissions: domain.PermissionModeAcceptEdits}},
		{"lado-policy/4 without stage settings", testSnapshot(claudeAgent(), false), ports.AgentConfig{Model: "claude-sonnet-5", Permissions: domain.PermissionModeAcceptEdits}},
		{"lado-policy/4 with stage settings", stagedSnapshot(), stagedExecution},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			f.tasks.input.Snapshot = c.snapshot
			f.store.add(testEntry("entry-1", t0.Add(-10*time.Minute)))

			f.tick(t, t0)

			spawned := f.sessions.spawned()
			if len(spawned) != 1 || spawned[0].AgentConfig != c.want || spawned[0].Harness != domain.HarnessClaudeCode {
				t.Fatalf("spawned %+v, want one worker with %+v", spawned, c.want)
			}
			var dispatched []domain.LadoAuditEvent
			for _, ev := range f.store.audited() {
				if ev.Action == "execution.dispatched" {
					dispatched = append(dispatched, ev)
				}
			}
			if len(dispatched) != 1 || dispatched[0].SubjectID != "entry-1" {
				t.Fatalf("execution.dispatched events %+v, want one for entry-1", dispatched)
			}
			if err := stagePayloadOK(dispatched[0].Payload, domain.LadoStageExecution, c.want); err != nil {
				t.Fatal(err)
			}
			var payload map[string]any
			_ = json.Unmarshal(dispatched[0].Payload, &payload)
			if payload["task"] != "task-1" || payload["attempt"] != float64(1) || payload["session"] != "sess-1" {
				t.Fatalf("payload %v, want task-1, attempt 1 and sess-1", payload)
			}
		})
	}
}

// planBlobs holds task-1's pinned plan for the real policy service.
type planBlobs struct{}

func (planBlobs) Get(sum string, _ int64) ([]byte, error) {
	if sum != testPlanSHA {
		return nil, fmt.Errorf("blob %s is missing", sum)
	}
	return []byte(testPlanText), nil
}

// With the real rules service, the execution, integration and resolve stage
// settings an approval's snapshot fixed are what its worker spawns with;
// changing one of them after the approval sends the queued task to a review
// for its rules, while a plan or critic setting does not.
func TestStageSettingsAfterApprovalWithTheRules(t *testing.T) {
	claude := domain.HarnessClaudeCode
	approvedWith := domain.LadoStageAgents{
		domain.LadoStageExecution:   {Harness: claude, Model: "opus", Effort: "high"},
		domain.LadoStageIntegration: {Harness: claude, Model: "sonnet", Effort: "low"},
	}
	for _, c := range []struct {
		name string
		// project and global are the settings once the task is approved.
		project, global domain.LadoStageAgents
		review          bool
	}{
		{name: "unchanged", project: approvedWith},
		{name: "a plan and critic setting", project: approvedWith, global: domain.LadoStageAgents{
			domain.LadoStagePlan:   {Harness: claude, Model: "fable", Effort: "max"},
			domain.LadoStageCritic: {Harness: claude, Model: "haiku"},
		}},
		{name: "a setting of another harness", project: approvedWith, global: domain.LadoStageAgents{
			domain.LadoStageResolve: {Harness: domain.HarnessCodex, Model: "gpt-5.1-codex"},
		}},
		{name: "another project execution effort", review: true, project: domain.LadoStageAgents{
			domain.LadoStageExecution:   {Harness: claude, Model: "opus", Effort: "low"},
			domain.LadoStageIntegration: {Harness: claude, Model: "sonnet", Effort: "low"},
		}},
		{name: "a global resolve model", review: true, project: approvedWith, global: domain.LadoStageAgents{
			domain.LadoStageResolve: {Harness: claude, Model: "haiku"},
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			f := newFixture(t)
			db := sqlitetest.MustOpenAt(t, t.TempDir())
			if err := db.UpsertProject(ctx, domain.ProjectRecord{ID: "proj-1", Path: t.TempDir(), RegisteredAt: t0}); err != nil {
				t.Fatal(err)
			}
			policy := ladopolicy.New(ladopolicy.Deps{Store: db, Projects: db, Blobs: planBlobs{}, AOBuild: "1.0.0",
				Clock: func() time.Time { return t0 }, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
			if err := policy.EnsureBuiltins(ctx); err != nil {
				t.Fatal(err)
			}
			set := func(scope domain.ProjectID, agents domain.LadoStageAgents) {
				t.Helper()
				if err := db.SetLadoStageAgents(ctx, scope, agents, domain.LadoAuditEvent{At: t0, Actor: "human", Action: "stage_agents.changed",
					SubjectKind: "stage_agents", SubjectID: string(scope), Payload: json.RawMessage(`{}`)}); err != nil {
					t.Fatal(err)
				}
			}
			// The developer approves with the settings in place.
			set("proj-1", approvedWith)
			task := testTask()
			snapshot, err := policy.Snapshot(ctx, task, task.Plan.Latest)
			if err != nil {
				t.Fatal(err)
			}
			snapshot.ID, snapshot.ApprovalID = "snap-1", "approval-1"
			task.Plan.Snapshot.SHA256 = snapshot.SHA256
			f.store.tasks["task-1"] = task
			f.tasks.input.Snapshot = snapshot
			f.svc.deps.Policy = policy

			set("proj-1", c.project)
			set(domain.LadoStageGlobalScope, c.global)
			f.store.add(testEntry("entry-1", t0.Add(-10*time.Minute)))
			f.tick(t, t0)

			spawned := f.sessions.spawned()
			if c.review {
				if e := f.store.entry("entry-1"); e.ReviewRequiredReason != domain.LadoReviewRulesChanged || len(spawned) != 0 {
					t.Fatalf("entry %+v with %d spawns, want a review for the rules and no worker", e, len(spawned))
				}
				return
			}
			var content ladopolicy.SnapshotContent
			if err := json.Unmarshal(snapshot.Content, &content); err != nil {
				t.Fatal(err)
			}
			want := ports.AgentConfig{Model: "opus", Effort: "high", Permissions: content.Agent.PermissionMode}
			if len(spawned) != 1 || spawned[0].AgentConfig != want {
				t.Fatalf("spawned %+v, want one worker with %+v", spawned, want)
			}
			wantStages := ladopolicy.SnapshotStages{
				Integration: ladopolicy.SnapshotStageAgent{Model: "sonnet", Effort: "low"},
				Resolve:     ladopolicy.SnapshotStageAgent{Model: "opus", Effort: "high"},
			}
			if !reflect.DeepEqual(content.Stages, wantStages) {
				t.Fatalf("approved stages %+v, want %+v", content.Stages, wantStages)
			}
		})
	}
}
