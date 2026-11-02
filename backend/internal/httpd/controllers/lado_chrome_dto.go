package controllers

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

// LadoClaudeChromeSettingsResponse is the Claude in Chrome setting.
type LadoClaudeChromeSettingsResponse struct {
	// ChatSessions covers Claude Code Chat sessions a person works in.
	ChatSessions bool `json:"chatSessions"`
	// LadoWorkers covers the workers LADO starts itself.
	LadoWorkers bool `json:"ladoWorkers"`
}

// UpdateLadoClaudeChromeRequest replaces the setting. Both fields are
// required. Turning either on needs a human-presence proof over these exact
// bytes; turning off needs none.
type UpdateLadoClaudeChromeRequest struct {
	ChatSessions *bool `json:"chatSessions" nullable:"false"`
	LadoWorkers  *bool `json:"ladoWorkers" nullable:"false"`
}

// LadoSessionClaudeChromeResponse is one session's Claude in Chrome state.
// Supported is false for anything but a Claude Code Chat session.
type LadoSessionClaudeChromeResponse struct {
	SessionID string `json:"sessionId"`
	Supported bool   `json:"supported"`
	// Enabled says whether the session has (basis launch) or will get
	// (basis setting) Claude in Chrome.
	Enabled bool `json:"enabled"`
	// Basis is launch when the running controller started with this value,
	// setting when the next start gets it.
	Basis string `json:"basis,omitempty" enum:"launch,setting"`
	// Kind says which setting covers the session.
	Kind string `json:"kind,omitempty" enum:"chat,lado_worker"`
	// SettingOn is the current setting for the session's kind.
	SettingOn bool `json:"settingOn"`
}

func ladoClaudeChromeSettingsResponse(s domain.LadoClaudeChromeSettings) LadoClaudeChromeSettingsResponse {
	return LadoClaudeChromeSettingsResponse{ChatSessions: s.ChatSessions, LadoWorkers: s.LadoWorkers}
}

func ladoSessionClaudeChromeResponse(id domain.SessionID, s domain.LadoSessionChrome) LadoSessionClaudeChromeResponse {
	return LadoSessionClaudeChromeResponse{
		SessionID: string(id), Supported: s.Supported, Enabled: s.Enabled,
		Basis: string(s.Basis), Kind: string(s.Kind), SettingOn: s.SettingOn,
	}
}
