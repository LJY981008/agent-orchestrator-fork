# LADO 1. Defend against agent mistakes (T1) now; adversarial agents (T2) later

Date: 2026-10-19
Status: Accepted

LADO ADRs are numbered separately from upstream's `docs/adr/`. Paths for Go code under `backend/internal/` omit that prefix.

## Context

LADO adds plan approval, integration approval and rules to AO. How much those controls can promise depends on who they defend against.

Facts at `55b02b848`:

- Agents run as the same OS user as the human.
- The primary listener is loopback-only and unauthenticated by rule (`AGENTS.md:108`). CORS rejects foreign browser origins with 403, but requests without an `Origin` header (CLI, curl, any local process) pass (`httpd/cors.go:13-27`). Hostile web pages are therefore not the problem; local processes are.
- Loopback control routes such as `/shutdown` are recognized by "no `Origin`, loopback `Host`" (`httpd/router.go:385-401`), which every local process satisfies.
- Agents get `ao` first on PATH (`agentlaunch/env.go:74-95`).
- Chat approval resolve has no caller check (`httpd/controllers/conversations.go:121`, handler :912-939).
- Reproduced in a scratch repo: from a linked worktree, `git update-ref refs/heads/main HEAD` moves the main checkout's branch, and `git commit --no-verify` skips a `commit-msg` hook.

## Decision

LADO's target is **T1: agent mistakes and prompt drift**. Examples: the agent leaves the approved plan, starts extra workers, edits outside scope, merges or pushes, writes non-compliant commits, skips tests, or reports success that the checks contradict.

**T2, a prompt-injected agent that deliberately evades controls, is out of scope for now.** With a same-user shell and an unauthenticated loopback API, no daemon-side control can stop it. Defending against T2 needs OS isolation first: a separate OS user, or on Windows an AppContainer or restricted token with a job object. Upstream already names that boundary for interactive reviewers (`docs/adr/0002-secure-interactive-reviewer-gateway.md`).

Every control is labelled with its real strength against T1:

| Control | T1 | T2 |
|---|---|---|
| Worker gate in the daemon | Prevents | Bypassable |
| GitHub ruleset with a required `lado/integration` check | Prevents remote merge | Bypassable: the agent holds the same GitHub token |
| Local base-ref check (SHA and reflog) | Detects | Detects unless the reflog is rewritten |
| Commit and path validators on `base..head` | Detect; block integration | Same |
| Human-presence approval | Prevents approval by an agent | Forgeable: a same-user process can read the key from the app's or daemon's memory |
| Prompt rules | Advisory | None |

The acceptance gates in PRD §8 are T1 tests.

## Consequences

- No LADO surface uses the words "sandboxed", "secure" or "isolated" for these controls. The UI says what each one does: prevents, detects or advises.
- Remote merge protection is the strongest control because GitHub enforces it, not the local machine.
- Work needed before T2 is in scope:
  - Run agents under a separate OS identity, so they cannot read `~/.ao/lado/` (managed setup, backups) or the memory of the app and daemon (presence key).
  - Give agents a GitHub token that cannot post the `lado/integration` status or merge, with LADO holding the only token that can. The repository connection ([0006](0006-issues-as-work-items.md)) already gives LADO a stored token; agents still use the machine's credentials.
  - Make the audit hash chain verifiable outside the machine.
