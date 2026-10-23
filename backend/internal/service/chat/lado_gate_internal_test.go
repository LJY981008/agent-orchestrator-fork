package chat

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestLadoMessageOriginReadsTheMessage(t *testing.T) {
	cases := []struct {
		name string
		msg  ports.ChatUserMessage
		want ports.SendOrigin
	}{
		{"person", ports.ChatUserMessage{Origin: domain.MessageOriginHuman}, ports.SendOriginHuman},
		{"another session through a human route", ports.ChatUserMessage{Origin: domain.MessageOriginHuman, SenderSessionID: "orchestrator-1"}, ports.SendOriginSession},
		{"another session marked automation", ports.ChatUserMessage{Origin: domain.MessageOriginAutomation, SenderSessionID: "orchestrator-1"}, ports.SendOriginSession},
		{"daemon", ports.ChatUserMessage{Origin: domain.MessageOriginDaemon}, ports.SendOriginDaemon},
		{"automation", ports.ChatUserMessage{Origin: domain.MessageOriginAutomation}, ports.SendOriginAutomation},
		{"no origin", ports.ChatUserMessage{}, ports.SendOriginAutomation},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ladoMessageOrigin(tc.msg); got != tc.want {
				t.Fatalf("ladoMessageOrigin = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestLadoConfigModeReadsEveryApprovalControl pins the mode the gate is asked
// about, before the provider call, to the mode settingsFromConfigOptions
// records from the provider's answer.
func TestLadoConfigModeReadsEveryApprovalControl(t *testing.T) {
	cases := []struct {
		harness  domain.AgentHarness
		configID string
		value    string
		want     domain.PermissionMode
	}{
		{domain.HarnessClaudeCode, "mode", "manual", domain.PermissionModeDefault},
		{domain.HarnessClaudeCode, "mode", "default", domain.PermissionModeDefault},
		{domain.HarnessClaudeCode, "mode", "acceptEdits", domain.PermissionModeAcceptEdits},
		{domain.HarnessClaudeCode, "mode", "auto", domain.PermissionModeAuto},
		{domain.HarnessClaudeCode, "mode", "bypassPermissions", domain.PermissionModeBypassPermissions},
		{domain.HarnessClaudeCode, "mode", "plan", ""},
		{domain.HarnessClaudeCode, "model", "auto", ""},
		{domain.HarnessOpenCode, "mode", "ao-bypass", domain.PermissionModeBypassPermissions},
		{domain.HarnessOpenCodeV2, "mode", "ao-accept-edits", domain.PermissionModeAcceptEdits},
		{domain.HarnessOpenCode, "mode", "build", ""},
		{domain.HarnessAgy, "mode", "yolo", domain.PermissionModeAuto},
		{domain.HarnessAgy, "mode", "default", domain.PermissionModeDefault},
		{domain.HarnessCommandCode, "mode", "bypass", domain.PermissionModeBypassPermissions},
		{domain.HarnessCommandCode, "mode", "auto-accept", domain.PermissionModeAuto},
		{domain.HarnessCommandCode, "mode", "plan", ""},
		{domain.HarnessCopilot, "mode", "agent", ""},
		{domain.HarnessCodex, "mode", "full-access", ""},
	}
	for _, tc := range cases {
		t.Run(string(tc.harness)+" "+tc.configID+" "+tc.value, func(t *testing.T) {
			value := ports.ChatConfigOptionValue{Select: tc.value}
			got := ladoConfigMode(tc.harness, tc.configID, value)
			if got != tc.want {
				t.Fatalf("ladoConfigMode = %q, want %q", got, tc.want)
			}
			answer := []ports.ChatConfigOption{{
				ID: tc.configID, Type: ports.ChatConfigOptionSelect, Current: value,
				Choices: []ports.ChatConfigOptionChoice{{Value: "other"}, {Value: tc.value}},
			}}
			recorded, _ := settingsFromConfigOptions(domain.ConversationSettings{}, permissionConfigOptions(tc.harness, answer))
			if recorded.ApprovalMode != got {
				t.Fatalf("recorded approval = %q, gate asked about %q", recorded.ApprovalMode, got)
			}
		})
	}
}
