package domain

import "strings"

// LadoClaudeChromeSettings says which Claude Code Chat sessions start with
// Claude in Chrome (the browser extension's mcp__claude-in-chrome__* tools).
// It is one daemon-wide setting, not per project.
type LadoClaudeChromeSettings struct {
	// ChatSessions covers every Claude Code Chat session a person works in,
	// including the orchestrator and `ao spawn` workers.
	ChatSessions bool `json:"chatSessions"`
	// LadoWorkers covers the sessions LADO starts itself: dispatch, part,
	// merge, integration and plan workers. Off by default because they act
	// in the signed-in, real Chrome without a person watching.
	LadoWorkers bool `json:"ladoWorkers"`
}

// DefaultLadoClaudeChromeSettings is the value before anyone changed it.
func DefaultLadoClaudeChromeSettings() LadoClaudeChromeSettings {
	return LadoClaudeChromeSettings{ChatSessions: true, LadoWorkers: false}
}

// LadoClaudeChromeEnv turns Claude in Chrome on ("1") or off ("0") for a
// Claude Code process. SDK sessions are non-interactive, so Claude Code
// ignores its own "enabled by default" choice there and reads only this.
const LadoClaudeChromeEnv = "CLAUDE_CODE_ENABLE_CFC"

// LadoChromeBasis says where a session's Claude in Chrome answer comes from.
type LadoChromeBasis string

const (
	// LadoChromeBasisLaunch is the value the running controller started with.
	LadoChromeBasisLaunch LadoChromeBasis = "launch"
	// LadoChromeBasisSetting is the value the next controller start will get.
	LadoChromeBasisSetting LadoChromeBasis = "setting"
)

// LadoChromeSessionKind says which setting covers a session.
type LadoChromeSessionKind string

// The session kinds: a person's Chat session, or a worker LADO started.
const (
	LadoChromeKindChat       LadoChromeSessionKind = "chat"
	LadoChromeKindLadoWorker LadoChromeSessionKind = "lado_worker"
)

// LadoSessionChrome is one session's Claude in Chrome state. Supported is
// false for anything but a Claude Code Chat session; the other fields are
// then empty. SettingOn is the current setting for the session's kind, so a
// client can tell "off in Settings" from "started before it was turned on".
type LadoSessionChrome struct {
	Supported bool
	Enabled   bool
	Basis     LadoChromeBasis
	Kind      LadoChromeSessionKind
	SettingOn bool
}

// IsLadoWorkerClientRequestID reports whether a session's client request id
// marks a worker LADO started: every one of them begins with "lado:".
func IsLadoWorkerClientRequestID(id string) bool {
	return strings.HasPrefix(id, ladoClientRequestPrefix)
}
