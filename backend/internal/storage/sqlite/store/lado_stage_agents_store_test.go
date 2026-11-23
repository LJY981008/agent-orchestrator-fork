package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// A scope's settings are replaced as a whole with their audit event, the
// global scope and each project stay apart, and an empty map clears a scope.
func TestLadoStageAgentsReplaceAScope(t *testing.T) {
	s, raw := openLadoAuditStore(t)
	ctx := context.Background()
	at := time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)
	event := func(scope string, at time.Time) domain.LadoAuditEvent {
		return domain.LadoAuditEvent{At: at, Actor: "human", Action: "stage_agents.changed", SubjectKind: "stage_agents",
			SubjectID: scope, Payload: json.RawMessage(`{"scope":"` + scope + `"}`)}
	}
	read := func(project domain.ProjectID) domain.LadoStageAgents {
		t.Helper()
		got, err := s.LadoStageAgents(ctx, project)
		if err != nil {
			t.Fatalf("LadoStageAgents(%q): %v", project, err)
		}
		if got == nil {
			t.Fatalf("LadoStageAgents(%q) = nil, want a map", project)
		}
		return got
	}
	audits := func() int {
		t.Helper()
		var n int
		if err := raw.QueryRow(`SELECT COUNT(*) FROM lado_audit_events WHERE action = 'stage_agents.changed'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	if got := read(domain.LadoStageGlobalScope); len(got) != 0 {
		t.Fatalf("global settings before any save = %v, want none", got)
	}
	global := domain.LadoStageAgents{
		domain.LadoStagePlan:      {Harness: domain.HarnessClaudeCode, Model: "fable", Effort: "high"},
		domain.LadoStageExecution: {Harness: domain.HarnessCodex, Model: "gpt-5.1-codex"},
	}
	if err := s.SetLadoStageAgents(ctx, domain.LadoStageGlobalScope, global, event("global", at)); err != nil {
		t.Fatalf("set global: %v", err)
	}
	project := domain.LadoStageAgents{domain.LadoStageCritic: {Harness: domain.HarnessClaudeCode, Model: "haiku"}}
	if err := s.SetLadoStageAgents(ctx, "mer", project, event("mer", at)); err != nil {
		t.Fatalf("set project: %v", err)
	}
	if got := read(domain.LadoStageGlobalScope); !maps.Equal(got, global) {
		t.Fatalf("global = %v, want %v", got, global)
	}
	if got := read("mer"); !maps.Equal(got, project) {
		t.Fatalf("project = %v, want %v", got, project)
	}
	if got := read("other"); len(got) != 0 {
		t.Fatalf("another project = %v, want none", got)
	}

	// A new save drops the stages it leaves out and takes the event's time.
	later := at.Add(time.Hour)
	replaced := domain.LadoStageAgents{domain.LadoStageResolve: {Harness: domain.HarnessClaudeCode, Model: "opus", Effort: "medium"}}
	if err := s.SetLadoStageAgents(ctx, domain.LadoStageGlobalScope, replaced, event("global", later)); err != nil {
		t.Fatalf("replace global: %v", err)
	}
	if got := read(domain.LadoStageGlobalScope); !maps.Equal(got, replaced) {
		t.Fatalf("global after the replace = %v, want %v", got, replaced)
	}
	var updated time.Time
	if err := raw.QueryRow(`SELECT updated_at FROM lado_stage_agents WHERE project_id = '' AND stage = 'resolve'`).Scan(&updated); err != nil ||
		!updated.Equal(later) {
		t.Fatalf("updated_at = %s, %v; want %s", updated, err, later)
	}
	if got := read("mer"); !maps.Equal(got, project) {
		t.Fatalf("project after the global replace = %v, want %v", got, project)
	}

	if err := s.SetLadoStageAgents(ctx, "mer", domain.LadoStageAgents{}, event("mer", later)); err != nil {
		t.Fatalf("clear project: %v", err)
	}
	if got := read("mer"); len(got) != 0 {
		t.Fatalf("project after the clear = %v, want none", got)
	}
	if got := audits(); got != 4 {
		t.Fatalf("audit events = %d, want one per save (4)", got)
	}
	if broken, err := s.VerifyLadoAuditChain(ctx); err != nil || broken != 0 {
		t.Fatalf("VerifyLadoAuditChain = %d, %v", broken, err)
	}
}

// A refused save keeps the scope's rows and writes no audit event: an
// invalid setting before the transaction, an incomplete event inside it.
func TestLadoStageAgentsRefusedSaveKeepsTheScope(t *testing.T) {
	s, raw := openLadoAuditStore(t)
	ctx := context.Background()
	at := time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)
	event := domain.LadoAuditEvent{At: at, Actor: "human", Action: "stage_agents.changed", SubjectKind: "stage_agents", SubjectID: "mer"}
	kept := domain.LadoStageAgents{domain.LadoStagePlan: {Harness: domain.HarnessClaudeCode, Model: "fable", Effort: "high"}}
	if err := s.SetLadoStageAgents(ctx, "mer", kept, event); err != nil {
		t.Fatalf("set: %v", err)
	}
	for name, tc := range map[string]struct {
		agents domain.LadoStageAgents
		event  domain.LadoAuditEvent
		want   error
	}{
		"effort alone":      {domain.LadoStageAgents{domain.LadoStagePlan: {Harness: domain.HarnessClaudeCode, Effort: "high"}}, event, domain.ErrLadoStageEffortWithoutModel},
		"an unknown stage":  {domain.LadoStageAgents{"review": {Harness: domain.HarnessClaudeCode, Model: "opus"}}, event, domain.ErrLadoStageInvalid},
		"another harness":   {domain.LadoStageAgents{domain.LadoStagePlan: {Harness: domain.HarnessAider, Model: "x"}}, event, domain.ErrLadoStageHarnessUnsupported},
		"an event untimed":  {domain.LadoStageAgents{}, domain.LadoAuditEvent{Actor: "human", Action: "stage_agents.changed", SubjectKind: "stage_agents"}, nil},
		"an event unnamed":  {domain.LadoStageAgents{domain.LadoStageCritic: {Harness: domain.HarnessCodex, Model: "gpt-5"}}, domain.LadoAuditEvent{At: at, Actor: "human", SubjectKind: "stage_agents"}, nil},
		"a model with tabs": {domain.LadoStageAgents{domain.LadoStagePlan: {Harness: domain.HarnessClaudeCode, Model: "op\tus"}}, event, domain.ErrLadoStageInvalid},
	} {
		err := s.SetLadoStageAgents(ctx, "mer", tc.agents, tc.event)
		if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) {
			t.Errorf("%s: %v, want %v", name, err, tc.want)
		}
		got, err := s.LadoStageAgents(ctx, "mer")
		if err != nil || !maps.Equal(got, kept) {
			t.Fatalf("%s: settings after the refusal = %v, %v; want %v", name, got, err, kept)
		}
	}
	var n int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM lado_audit_events`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("audit events = %d, %v; want only the first save's", n, err)
	}
}
