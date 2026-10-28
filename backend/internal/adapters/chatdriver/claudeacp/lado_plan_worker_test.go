package claudeacp

import (
	"slices"
	"testing"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// A plan worker's session gets the read-only tool lists next to AO's
// standing instructions; any other session's meta stays as it was.
func TestClaudeSessionMetaGivesPlanWorkersTheirToolLists(t *testing.T) {
	cfg := acpdriver.LaunchConfig{SystemPrompt: "Plan only.", Env: map[string]string{domain.LadoPlanWorkerEnv: "1"}}
	meta := claudeSessionMeta(cfg)
	options, _ := meta["claudeCode"].(map[string]any)["options"].(map[string]any)
	allowed, _ := options["allowedTools"].([]string)
	disallowed, _ := options["disallowedTools"].([]string)
	if !slices.Contains(allowed, "Read") || !slices.Contains(allowed, "Bash(git log:*)") || slices.Contains(allowed, "Edit") {
		t.Fatalf("allowed = %v", allowed)
	}
	for _, tool := range []string{"Edit", "Write", "NotebookEdit", "Bash(git commit:*)", "Bash(git push:*)", "Bash(gh pr create:*)", "Bash(gh pr merge:*)"} {
		if !slices.Contains(disallowed, tool) {
			t.Fatalf("disallowed = %v, want %s", disallowed, tool)
		}
	}
	if prompt, _ := meta["systemPrompt"].(map[string]any); prompt["append"] != "Plan only." {
		t.Fatalf("system prompt = %v", meta["systemPrompt"])
	}
	if meta := claudeSessionMeta(acpdriver.LaunchConfig{Env: map[string]string{domain.LadoPlanWorkerEnv: "1"}}); meta["claudeCode"] == nil {
		t.Fatal("a plan worker without standing instructions got no tool lists")
	}
	other := claudeSessionMeta(acpdriver.LaunchConfig{SystemPrompt: "Work.", Env: map[string]string{}})
	if _, ok := other["claudeCode"]; ok || other["systemPrompt"] == nil {
		t.Fatalf("another session's meta = %v", other)
	}
	if claudeSessionMeta(acpdriver.LaunchConfig{}) != nil {
		t.Fatal("a session without instructions got meta")
	}
}
