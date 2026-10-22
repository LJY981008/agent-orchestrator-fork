package ladopolicy_test

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopolicy"
)

func TestCapabilityTable(t *testing.T) {
	const (
		prompt     = ladopolicy.CapabilityPrompt
		permission = ladopolicy.CapabilityPermissionMode
	)
	cases := []struct {
		harness domain.AgentHarness
		class   ladopolicy.CapabilityClass
		want    ladopolicy.CapabilityStatus
	}{
		{domain.HarnessClaudeCode, prompt, ladopolicy.CapabilitySupported},
		{domain.HarnessClaudeCode, permission, ladopolicy.CapabilitySupported},
		{domain.HarnessCodex, prompt, ladopolicy.CapabilitySupported},
		{domain.HarnessCodex, permission, ladopolicy.CapabilitySupported},
		{domain.HarnessCursor, prompt, ladopolicy.CapabilityUnsupported},
		{domain.HarnessCursor, permission, ladopolicy.CapabilityUnverified},
		{domain.HarnessFX, prompt, ladopolicy.CapabilityDegraded},
		{domain.HarnessFX, permission, ladopolicy.CapabilityUnverified},
		{domain.HarnessGemini, prompt, ladopolicy.CapabilityUnverified},
		{domain.HarnessGemini, permission, ladopolicy.CapabilityUnverified},
		{"", prompt, ladopolicy.CapabilityUnverified},
		{"", permission, ladopolicy.CapabilityUnverified},
		// A class nobody verified fails closed even for a verified agent.
		{domain.HarnessClaudeCode, "network", ladopolicy.CapabilityUnverified},
	}
	for _, c := range cases {
		if got := ladopolicy.CapabilityOf(c.harness, c.class); got != c.want {
			t.Errorf("CapabilityOf(%q, %q) = %q, want %q", c.harness, c.class, got, c.want)
		}
	}

	// Every other agent AO supports is unverified for both classes.
	listed := map[domain.AgentHarness]bool{
		domain.HarnessClaudeCode: true, domain.HarnessCodex: true, domain.HarnessCursor: true, domain.HarnessFX: true,
	}
	for _, harness := range domain.AllHarnesses {
		if listed[harness] {
			continue
		}
		for _, class := range []ladopolicy.CapabilityClass{prompt, permission} {
			if got := ladopolicy.CapabilityOf(harness, class); got != ladopolicy.CapabilityUnverified {
				t.Errorf("CapabilityOf(%q, %q) = %q, want unverified", harness, class, got)
			}
		}
	}

	// Rule snapshots and the API carry these names, so they never change.
	names := map[string]string{
		string(prompt):                           "prompt",
		string(permission):                       "permission_mode",
		string(ladopolicy.CapabilitySupported):   "supported",
		string(ladopolicy.CapabilityDegraded):    "degraded",
		string(ladopolicy.CapabilityUnsupported): "unsupported",
		string(ladopolicy.CapabilityUnverified):  "unverified",
	}
	for got, want := range names {
		if got != want {
			t.Errorf("name %q, want %q", got, want)
		}
	}
}
