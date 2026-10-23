package ladopolicy

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

// CapabilityClass is something rules need from the agent that runs a task
// (docs/LJY-PRD/HARNESS.md §7).
type CapabilityClass string

// Capability classes.
const (
	// CapabilityPrompt is taking LADO's rule text as standing instructions,
	// which prompt rules need.
	CapabilityPrompt CapabilityClass = "prompt"
	// CapabilityPermissionMode is running at the permission mode LADO picks
	// under the ceiling, which every LADO execution needs.
	CapabilityPermissionMode CapabilityClass = "permission_mode"
)

// CapabilityStatus is what LADO knows about an agent and a capability class.
// Resolution blocks a task on unsupported and unverified and warns on
// degraded.
type CapabilityStatus string

// Capability statuses.
const (
	CapabilitySupported   CapabilityStatus = "supported"
	CapabilityDegraded    CapabilityStatus = "degraded"
	CapabilityUnsupported CapabilityStatus = "unsupported"
	CapabilityUnverified  CapabilityStatus = "unverified"
)

// Capability is the status of one class for the agent that runs a task.
type Capability struct {
	Class  CapabilityClass
	Status CapabilityStatus
}

// capabilities is what LADO verified per agent. A missing agent or class is
// unverified, so LADO blocks what nobody checked.
//
//   - Claude Code takes standing instructions through
//     --append-system-prompt-file in the terminal
//     (adapters/agent/claudecode/claudecode.go) and ACP _meta.systemPrompt in
//     Chat (adapters/chatdriver/claudeacp/driver.go). Codex takes them
//     through -c model_instructions_file (pkg/agentruntime/command.go) and
//     the app server's developerInstructions
//     (adapters/chatdriver/codexappserver/driver.go). AO maps permission
//     modes for both in the terminal (pkg/agentruntime/command.go) and in
//     Chat (the same drivers).
//   - Cursor has no system-prompt flag and reads only workspace rule files
//     (adapters/agent/cursor/cursor.go).
//   - fx has no per-session system prompt, so AO sends the standing
//     instructions as the first user turn (adapters/agent/fx/fx.go).
var capabilities = map[domain.AgentHarness]map[CapabilityClass]CapabilityStatus{
	domain.HarnessClaudeCode: {CapabilityPrompt: CapabilitySupported, CapabilityPermissionMode: CapabilitySupported},
	domain.HarnessCodex:      {CapabilityPrompt: CapabilitySupported, CapabilityPermissionMode: CapabilitySupported},
	domain.HarnessCursor:     {CapabilityPrompt: CapabilityUnsupported},
	domain.HarnessFX:         {CapabilityPrompt: CapabilityDegraded},
}

// CapabilityOf reports what LADO knows about the agent's capability class.
func CapabilityOf(h domain.AgentHarness, c CapabilityClass) CapabilityStatus {
	if status, ok := capabilities[h][c]; ok {
		return status
	}
	return CapabilityUnverified
}
