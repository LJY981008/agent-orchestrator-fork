package ladotask

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopolicy"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladosetup"
)

// HARNESS §15 item 12: with the real rule and setup services, an approval
// pins the setup it took, and a setup change before the worker starts sends
// the task back to review with rules_changed, as the dispatcher's third check
// finds by the same hash. Undoing the change gives the approved hash again.
func TestSetupChangeAfterApprovalNeedsReview(t *testing.T) {
	pf := newPlanFixture(t)
	ctx := context.Background()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	home := t.TempDir()
	setup := ladosetup.New(ladosetup.Deps{
		Store: pf.store, Blobs: pf.blobs, StateDir: filepath.Join(home, "state"), ClaudeDir: filepath.Join(home, ".claude"),
		GOOS: "linux", Clock: func() time.Time { return pf.now }, Log: quiet,
	})
	policy := ladopolicy.New(ladopolicy.Deps{
		Store: pf.store, Projects: pf.store, Blobs: pf.blobs, AOBuild: "1.0.0", Setup: setup,
		Clock: func() time.Time { return pf.now }, Log: quiet,
	})
	if err := policy.EnsureBuiltins(ctx); err != nil {
		t.Fatal(err)
	}
	deps := pf.svc.deps
	deps.Policy = policy
	pf.svc = New(deps)
	task, err := pf.svc.Create(ctx, CreateInput{ProjectID: "demo", Text: "Add login\nThe plan is in the comments.", Agent: "claude-code"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	pf.task, pf.native = task, strings.TrimPrefix(string(task.IssueRef), "github:")

	event := func(action string) domain.LadoAuditEvent {
		return domain.LadoAuditEvent{At: pf.now, Actor: "api", Action: action, SubjectKind: "setup_item", SubjectID: "setup-claude-md", Payload: json.RawMessage(`{}`)}
	}
	version := func(number int, content string) domain.LadoSetupItemVersion {
		sum, err := pf.blobs.Put([]byte(content))
		if err != nil {
			t.Fatal(err)
		}
		return domain.LadoSetupItemVersion{
			ID: domain.LadoSetupItemVersionID("setup-claude-md-v" + strconv.Itoa(number)), Version: number,
			ContentKind: domain.LadoSetupContentFile, SHA256: sum, Size: int64(len(content)), Actor: "api", CreatedAt: pf.now,
		}
	}
	item := domain.LadoSetupItem{ID: "setup-claude-md", Tool: domain.LadoSetupToolClaudeCode, Kind: domain.LadoSetupKindInstructions, Name: "CLAUDE.md", CreatedAt: pf.now}
	v1 := version(1, "Be brief.\n")
	if err := pf.store.CreateLadoSetupItem(ctx, item, v1, event("setup.item_created"), nil); err != nil {
		t.Fatal(err)
	}
	bind := func(state domain.LadoBindingState) {
		t.Helper()
		b := domain.LadoSetupBinding{ItemID: item.ID, Scope: domain.LadoScopeProject, ScopeID: "demo", State: state, UpdatedAt: pf.now}
		if err := pf.store.SetLadoSetupBinding(ctx, b, state == domain.LadoBindingInherit, event("setup.binding_set"), nil); err != nil {
			t.Fatal(err)
		}
	}

	rev := pf.planned(t)
	view, err := policy.Effective(ctx, ladopolicy.EffectiveInput{TaskID: task.ID})
	if err != nil {
		t.Fatal(err)
	}
	if view.Blocking() || len(view.Content.Setup) != 1 || view.Content.Setup[0].VersionID != v1.ID {
		t.Fatalf("effective setup %+v, problems %+v; want CLAUDE.md version 1 and nothing blocking", view.Content.Setup, view.Problems)
	}
	approval, err := pf.svc.ApprovePlan(ctx, rev.ID, rev.SHA256, view.SHA256, pf.proof(rev.ID))
	if err != nil {
		t.Fatalf("ApprovePlan: %v", err)
	}
	// The approval pins the version it took.
	snapshot, ok, err := pf.store.LadoPolicySnapshot(ctx, approval.ID)
	if err != nil || !ok || snapshot.SHA256 != view.SHA256 || !slices.Equal(snapshot.SetupVersionIDs, []domain.LadoSetupItemVersionID{v1.ID}) {
		t.Fatalf("snapshot = %+v, %v, %v; want %s pinning %s", snapshot, ok, err, view.SHA256, v1.ID)
	}

	// state reads the task as the app does, and checks the hash the
	// dispatcher compares before it spawns (ladoqueue's third check).
	state := func(t *testing.T, want domain.LadoTaskState, reason string) {
		t.Helper()
		got, err := pf.svc.Get(ctx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.State() != want || got.StateReason() != reason {
			t.Fatalf("task is %s (%q), want %s (%q)", got.State(), got.StateReason(), want, reason)
		}
		current, err := policy.CurrentSHA256(ctx, got)
		if err != nil {
			t.Fatal(err)
		}
		if changed := current != got.Plan.Snapshot.SHA256; changed != (reason == domain.LadoReviewRulesChanged) {
			t.Fatalf("CurrentSHA256 %s against the approval's %s: changed = %v, want %v", current, got.Plan.Snapshot.SHA256, changed, !changed)
		}
	}
	state(t, domain.LadoTaskQueued, "")

	// Turning the item off for the project changes what the worker would get.
	bind(domain.LadoBindingForceOff)
	state(t, domain.LadoTaskReviewRequired, domain.LadoReviewRulesChanged)
	// Undoing it gives the approved hash again.
	bind(domain.LadoBindingInherit)
	state(t, domain.LadoTaskQueued, "")

	// A new version of the item needs a new approval.
	if err := pf.store.AddLadoSetupItemVersion(ctx, item.ID, 1, version(2, "Be briefer.\n"), event("setup.item_updated"), nil); err != nil {
		t.Fatal(err)
	}
	state(t, domain.LadoTaskReviewRequired, domain.LadoReviewRulesChanged)
	snapshot, _, err = pf.store.LadoPolicySnapshot(ctx, approval.ID)
	if err != nil || !slices.Equal(snapshot.SetupVersionIDs, []domain.LadoSetupItemVersionID{v1.ID}) {
		t.Fatalf("snapshot pins %v, %v; want version 1 still", snapshot.SetupVersionIDs, err)
	}
}
