package lifecycle

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// LADO fork (#53): AO's merge-conflict nudge reaches a LADO run's pull
// request worker — a single run's worker (role '') and a parts run's
// integration worker — in Chat mode, both idle after `ao report --done` and
// at a waiting_input prompt on a harness the urgent gate trusts. Each
// conflicting observation nudges once, a conflict that clears and comes back
// nudges again, and an exited or terminated worker gets nothing: LADO's
// conflict resolver takes that case over.

// ladoRunWorkers are the two LADO run workers that own a pull request.
var ladoRunWorkers = []struct {
	role      string
	requestID string
}{
	{"single", domain.LadoClientRequestID("lado-queue-1", 1)},
	{"integration", domain.LadoRunClientRequestID(domain.LadoRunRequest{Entry: "lado-queue-1", Role: domain.LadoRoleIntegration, Attempt: 1})},
}

// ladoRunWorker is a Chat worker a LADO run started, in the given state.
func ladoRunWorker(requestID string, state domain.ActivityState) domain.SessionRecord {
	rec := working("mer-1")
	rec.Kind = domain.KindWorker
	rec.Mode = domain.SessionModeChat
	rec.Harness = "claude-code"
	rec.ClientRequestID = requestID
	rec.ClientRequestCommitted = true
	rec.Activity.State = state
	return rec
}

func TestLadoRunWorkerGetsEachMergeConflictOnce(t *testing.T) {
	trustsWaitingInput := func(h domain.AgentHarness) bool { return h == "claude-code" }
	states := []struct {
		name  string
		state domain.ActivityState
	}{
		{"idle after done", domain.ActivityIdle},
		{"waiting_input", domain.ActivityWaitingInput},
	}
	for _, worker := range ladoRunWorkers {
		for _, st := range states {
			t.Run(worker.role+"/"+st.name, func(t *testing.T) {
				m, store, msg := newManager(WithUrgentNudgeGate(trustsWaitingInput))
				store.sessions["mer-1"] = ladoRunWorker(worker.requestID, st.state)
				apply := func(state domain.Mergeability) {
					t.Helper()
					if err := m.ApplyPRObservation(ctx, "mer-1", ports.PRObservation{Fetched: true, URL: "pr1", Mergeability: state}); err != nil {
						t.Fatal(err)
					}
				}

				apply(domain.MergeConflicting)
				apply(domain.MergeConflicting)
				if len(msg.msgs) != 1 || !strings.Contains(msg.msgs[0], "merge conflicts") || msg.ids[0] != "mer-1" {
					t.Fatalf("after two conflicting observations got %v to %v, want one merge-conflict nudge to mer-1", msg.msgs, msg.ids)
				}
				apply(domain.MergeMergeable)
				apply(domain.MergeConflicting)
				if len(msg.msgs) != 2 || !strings.Contains(msg.msgs[1], "merge conflicts") {
					t.Fatalf("a conflict back after mergeable got %d nudges %v, want a second one", len(msg.msgs), msg.msgs)
				}
			})
		}
	}
}

// A waiting_input prompt on a harness the urgent gate does not trust may hide
// a permission decision, so the LADO worker waits there like any session.
func TestLadoRunWorkerAtAnUntrustedWaitingInputGetsNoConflictNudge(t *testing.T) {
	for _, worker := range ladoRunWorkers {
		t.Run(worker.role, func(t *testing.T) {
			m, store, msg := newManager(WithUrgentNudgeGate(func(domain.AgentHarness) bool { return false }))
			store.sessions["mer-1"] = ladoRunWorker(worker.requestID, domain.ActivityWaitingInput)
			if err := m.ApplyPRObservation(ctx, "mer-1", ports.PRObservation{Fetched: true, URL: "pr1", Mergeability: domain.MergeConflicting}); err != nil {
				t.Fatal(err)
			}
			if len(msg.msgs) != 0 {
				t.Fatalf("got %v, want no nudge", msg.msgs)
			}
		})
	}
}

func TestLadoRunWorkerExitedOrTerminatedGetsNoConflictNudge(t *testing.T) {
	gone := []struct {
		name string
		mark func(*domain.SessionRecord)
	}{
		{"exited", func(r *domain.SessionRecord) { r.Activity.State = domain.ActivityExited }},
		{"terminated", func(r *domain.SessionRecord) { r.IsTerminated = true }},
	}
	for _, worker := range ladoRunWorkers {
		for _, g := range gone {
			t.Run(worker.role+"/"+g.name, func(t *testing.T) {
				m, store, msg := newManager(WithUrgentNudgeGate(func(domain.AgentHarness) bool { return true }))
				rec := ladoRunWorker(worker.requestID, domain.ActivityIdle)
				g.mark(&rec)
				store.sessions["mer-1"] = rec
				for range 2 {
					if err := m.ApplyPRObservation(ctx, "mer-1", ports.PRObservation{Fetched: true, URL: "pr1", Mergeability: domain.MergeConflicting}); err != nil {
						t.Fatal(err)
					}
				}
				if len(msg.msgs) != 0 {
					t.Fatalf("got %v, want no nudge to a %s worker", msg.msgs, g.name)
				}
			})
		}
	}
}
