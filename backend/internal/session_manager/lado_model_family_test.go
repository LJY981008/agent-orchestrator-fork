package sessionmanager

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// The exported wrapper picks the family's newest model with efforts, as the
// spawn check does, and nothing for a concrete model id.
func TestNewestClaudeFamilyModelExported(t *testing.T) {
	models := []ports.AgentModelInfo{
		{ID: "opus", Label: "Opus"},
		{ID: "claude-opus-5-1", Label: "Opus 5.1", Efforts: []string{"low", "high"}},
		{ID: "claude-opus-5-5", Label: "Opus 5.5", Efforts: []string{"low", "high", "max"}},
		{ID: "claude-sonnet-5-5", Label: "Sonnet 5.5", Efforts: []string{"low"}},
	}
	got := NewestClaudeFamilyModel(models, "opus[1m]")
	if got == nil || got.ID != "claude-opus-5-5" {
		t.Fatalf("NewestClaudeFamilyModel(opus[1m]) = %+v, want claude-opus-5-5", got)
	}
	if got := NewestClaudeFamilyModel(models, "claude-opus-5-1"); got != nil {
		t.Fatalf("NewestClaudeFamilyModel(concrete id) = %+v, want nil", got)
	}
}
