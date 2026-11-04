package sessionmanager

// LADO fork: the standing instructions of an orchestrator in a LADO project
// (docs/LJY-PRD/TDD.md §5.1). LADO's section defines its role, so AO's
// orchestrator role and delegation rules ("spawn or redirect a worker for
// every task") are replaced, not sent alongside it. The rules from AO's role
// that still hold are kept here.

import (
	"fmt"
	"strings"
)

// ladoOrchestratorBasePrompt replaces orchestratorSystemPrompt for an
// orchestrator in a LADO project.
func ladoOrchestratorBasePrompt(project promptProject) string {
	return fmt.Sprintf(`## AO Orchestrator Role (LADO mode)

You are the human-facing orchestrator for project %s. This project runs in LADO mode: the LADO project section below defines your role, and it replaces AO's default orchestrator rules for delegating work to worker sessions.

## Operating Rules

- Do not bypass AO or LADO by writing to tmux, PTY, pipes or runtime internals, or by calling the daemon's session send API to reach a worker.
- Do not use the agent runtime's built-in subagent or task-delegation tools for implementation work.
- Never claim a PR into the orchestrator session.

## Core Commands

- `+"`ao status`"+` - inspect project, session, PR, and review state.
- `+"`ao session ls --project %s`"+` - list sessions for this project.
- `+"`ao session get <worker-session-id>`"+` - inspect a worker session's details.
- `+"`ao review ls <worker-session-id>`"+` - inspect a worker's review state and verdicts.
- `+"`ao review trigger <worker-session-id>`"+` - when the developer asks for it, start AO's native reviewer on a worker's PR heads.

## Reviews

- An AO review approval is internal. It is not a GitHub approval and does not satisfy required or independent-account reviews or branch protection; report what is still required instead of working around it.

%s

%s`, projectName(project), project.ID, orchestratorSessionLinksSection(), projectContextSection(project))
}

// ladoSystemPromptGuard is systemPromptGuard for a LADO project's
// orchestrator: its high-level role summary must not tell it to spawn
// workers.
func ladoSystemPromptGuard() string {
	return strings.Replace(systemPromptGuard(),
		"orchestrators coordinate work and spawn or redirect workers",
		"a LADO project's orchestrator assists the developer, and code changes go through LADO tasks", 1)
}

// workspaceSpawnSentence is the one sentence of AO's workspace orchestrator
// paragraph (workspaceOrchestratorPrompt) about spawning workers.
const workspaceSpawnSentence = "When spawning workers, name the repository path or paths they should work in. "

// ladoWorkspaceOrchestratorPrompt is AO's workspace orchestrator paragraph
// without its sentence about spawning workers, which a LADO project's
// orchestrator does not do.
func ladoWorkspaceOrchestratorPrompt(prompt string) string {
	return strings.Replace(prompt, workspaceSpawnSentence, "", 1)
}
