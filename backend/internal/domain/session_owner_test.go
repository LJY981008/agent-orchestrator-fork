package domain

import "testing"

func TestSessionControllerOwnerActivatedChatBranch(t *testing.T) {
	before := SessionControllerOwner{
		Harness:                AgentHarness("claude-code"),
		Mode:                   SessionModeTUI,
		IsTerminated:           true,
		RuntimeLaunchID:        "launch-1",
		AgentSessionID:         "old-native",
		AgentSessionIDLaunchID: "launch-1",
		ProviderConversationID: "old-provider",
		ControllerGeneration:   "gen-1",
	}

	got := before.ActivatedChatBranch(AgentHarness("codex"), "new-provider", "gen-2")
	want := SessionControllerOwner{
		Harness:                AgentHarness("codex"),
		Mode:                   SessionModeChat,
		IsTerminated:           false,
		RuntimeLaunchID:        "",
		AgentSessionID:         "new-provider",
		AgentSessionIDLaunchID: "",
		ProviderConversationID: "new-provider",
		ControllerGeneration:   "gen-2",
	}
	if got != want {
		t.Fatalf("ActivatedChatBranch() = %+v, want %+v", got, want)
	}
	if before.ProviderConversationID != "old-provider" || before.AgentSessionID != "old-native" {
		t.Fatalf("ActivatedChatBranch mutated its receiver: %+v", before)
	}
}
