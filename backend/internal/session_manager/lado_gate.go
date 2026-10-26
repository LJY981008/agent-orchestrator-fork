package sessionmanager

// LADO fork: the worker gate's hook points in the session manager
// (docs/LJY-PRD/TDD.md §6), the LADO section of a session's standing
// instructions (§5.1, §6.4), and the branch a worker starts from, which LADO's
// dispatcher checks and records (§5.3).

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// SetWorkGate installs LADO's worker gate. The daemon calls it before anything
// can spawn or send; a nil gate keeps upstream behavior.
func (m *Manager) SetWorkGate(gate ports.WorkGate) {
	m.workGate = gate
}

// LadoPromptSection returns LADO's section of a session's standing
// instructions, or "" when LADO adds none: an orchestrator's in a project in
// LADO mode, and the rules text of a worker LADO dispatched. An error fails the
// spawn or restore rather than starting the session without its section.
type LadoPromptSection func(ctx context.Context, kind domain.SessionKind, projectID domain.ProjectID, sessionID domain.SessionID) (string, error)

// SetLadoPrompt installs LADO's section. The daemon calls it before any
// session starts or restores; nil keeps upstream behavior.
func (m *Manager) SetLadoPrompt(section LadoPromptSection) {
	m.ladoPrompt = section
}

// ladoPromptSection is the LADO section for a session of a project; the
// source decides by kind. Spawn, restore, resume and agent switches all build
// the system prompt through here, so a rebuilt worker keeps its section.
func (m *Manager) ladoPromptSection(ctx context.Context, kind domain.SessionKind, projectID domain.ProjectID, sessionID domain.SessionID) (string, error) {
	if m.ladoPrompt == nil || projectID == "" {
		return "", nil
	}
	return m.ladoPrompt(ctx, kind, projectID, sessionID)
}

// ResolveDefaultBranch resolves the branch a repository's worktrees start
// from, given the branch its project configures ("" for the repository's own
// default), the way a spawn resolves it. A workspace that cannot resolve
// branches reports ports.ErrWorkspaceDefaultBranchUnresolved.
func (m *Manager) ResolveDefaultBranch(ctx context.Context, repoPath, configuredBranch string) (ports.WorkspaceDefaultBranch, error) {
	refresher, ok := m.workspace.(ports.WorkspaceDefaultBranchRefresher)
	if !ok {
		return ports.WorkspaceDefaultBranch{}, ports.ErrWorkspaceDefaultBranchUnresolved
	}
	return refresher.ResolveDefaultBranch(ctx, repoPath, configuredBranch)
}

func (m *Manager) authorizeLadoSpawn(ctx context.Context, cfg ports.SpawnConfig) error {
	if m.workGate == nil {
		return nil
	}
	return m.workGate.AuthorizeSpawn(ctx, cfg)
}

// authorizeLadoExecutionChange asks the gate before an agent switch or an
// interface transition of a session.
func (m *Manager) authorizeLadoExecutionChange(ctx context.Context, id domain.SessionID) error {
	if m.workGate == nil {
		return nil
	}
	return m.workGate.AuthorizeExecutionChange(ctx, id)
}

// authorizeLadoRestore asks the gate before a person restores or resumes a
// session.
func (m *Manager) authorizeLadoRestore(ctx context.Context, id domain.SessionID) error {
	if m.workGate == nil {
		return nil
	}
	return m.workGate.AuthorizeRestore(ctx, id)
}

func (m *Manager) authorizeLadoSend(ctx context.Context, id domain.SessionID, message string, options ports.MessageDeliveryOptions) error {
	if m.workGate == nil {
		return nil
	}
	return m.workGate.AuthorizeSend(ctx, id, ladoSendOrigin(message, options))
}

// ladoSendOrigin reads who sent a message from its delivery facts. A named
// sender session wins over every other fact, so a message from another session
// cannot pass as a person's or as a report relay. `ao send` names its session
// whenever it runs inside one (cli/send.go).
func ladoSendOrigin(message string, options ports.MessageDeliveryOptions) ports.SendOrigin {
	switch {
	case options.SenderSessionID != "":
		return ports.SendOriginSession
	case options.AuthoredByUser:
		return ports.SendOriginHuman
	}
	if _, ok := domain.CoordinationDeliveryID(message); ok {
		return ports.SendOriginCoordination
	}
	return ports.SendOriginDaemon
}
