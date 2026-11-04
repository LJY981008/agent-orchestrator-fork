# LADO 9. LADO watches its workers: compact window, usage alerts, wake-up

Date: 2026-11-04
Status: Accepted. Decided by the owner in issue #42 (which absorbed #41), plan revision 2, approved 2026-11-04. Builds on the dispatcher (TDD §5.3), parallel parts ([adr/0008](0008-parallel-parts.md)), plan workers ([adr/0007](0007-plan-workers.md)) and #34's timings (TDD §5.5). Design in TDD §5.7.

## Context

Three problems from real use, all in October 2026:

- **Context.** One worker of a real project filled 956,000 of its 1,000,000-token context over three days. Every turn sends that context again, so each turn cost more. Claude Code's auto-compact threshold follows the model's context window, so it compacted only when nearly full.
- **Cost nobody saw.** The same worker spent about $400 at API prices, more than half of it in subagents. AO already stores each session's usage and cost estimate, but no LADO screen showed it, and since #32 a task runs in many sessions (plan workers, parts, merges, integration, retries).
- **Workers nobody woke.** On 10-29 a part worker of #34 stopped at its subscription usage limit (`You've hit your session limit · resets 8:20pm`). The limit lifted that evening; nobody resumed the worker for three days. On 10-30 Claude Code moved from an npm install to a native one and deleted the old files; AO kept the first path it found, and every new Claude session failed with `native binary not found`, spending the integration worker's three starts.

Facts checked before deciding (Claude Code 2.1.314 to 2.1.317, codex-cli 0.146.0 schema and 0.166.0 binary):

- Claude Code reads `CLAUDE_CODE_AUTO_COMPACT_WINDOW` over `autoCompactWindow` in any settings file, the project's `.claude/settings.json` included; the threshold is the smaller of it and the model's window, 100,000 to 1,000,000. `autoCompactEnabled: false`, `DISABLE_AUTO_COMPACT` and `DISABLE_COMPACT` turn auto-compact off.
- Codex has `model_auto_compact_token_limit` in config.toml, `-c` and the app-server thread config.
- In Chat, the only way LADO runs workers, a usage limit stop ends the turn with the message as its error (`conversation_turns.error_message`).
- AO's own `notifications` table has a CHECK on its type; a new type would mean rebuilding an upstream table.

## Decision

- **A compact window for every session of a LADO project.** Default 200,000 tokens (the owner's choice), set globally and per project, applied to every Claude Code start in a project with LADO mode on (workers, plan workers, Chat and terminal, the orchestrator, AO's reviewers) as `CLAUDE_CODE_AUTO_COMPACT_WINDOW`, and to Codex as `model_auto_compact_token_limit`. It goes on independently of LADO's managed Claude setup, and never on an ordinary AO project. Settings that turn auto-compact off are reported in the effective view and audited at launch.
- **The pinned plan on disk.** `<DataDir>/lado/runs/<revision>/plan.md`, read-only, named in every worker prompt with "read the plan again once your context is compacted", and readable from Chat without a permission prompt. It is a copy: LADO writes it again when it no longer holds the plan, and a run starts without it when it cannot be written.
- **Check the executable before a start.** Adapters resolve a cached Claude or Codex path again when its file is gone; LADO calls `PreflightLaunch` (AO's installation and executable invalidation without its model catalogs, AO's launch readiness, plus `--version`) before it starts a worker, and holds the worker without spending an attempt when it fails.
- **Usage in its own tab.** A task's usage adds up every session of the task, from #34's timing facts plus every earlier plan worker attempt, with #34's roles; the Timings tab keeps the investigation numbers. Costs are labelled as API-price estimates.
- **One-line alerts, once.** Task cost, session cost (once at the threshold, once more past twice it) and session compactions, defaults $100, $30 and 3. One row per (task, session, kind, level), with `''` for a task's own alert. Each row reaches the desktop (only from its INSERT, remembered across windows), the board's badge and the orchestrator's notices. LADO keeps its own `lado_alerts` table instead of AO's notifications.
- **Wake only with two fixed messages.** "사용 한도가 풀렸습니다. 멈춘 곳부터 이어서 진행하세요." 5 minutes after the reset, at most 3 times a session; "지금 상태를 보고하거나 이어서 진행하세요." once for a stall (120 minutes by default), alerts only after that. Only while the worker rests, never after a needs_input or done report or a turn the owner stopped, only where the project allows it; the stall nudge also never when the run's PR is open (CI and review have their own messages), while a limit resume still goes then, as nothing else resumes a worker stopped at its limit. Each limit stop and each stall after the nudge raises its own alert. Every message is audited and alerted. The orchestrator still cannot message workers.
- **Hold new workers while an account is at its limit.** Single, part, merge, integration and plan workers on that account wait without spending an attempt, checked again every minute, until the limit lifts.
- **One loop.** The daemon's one-minute loop counts compactions, then watches limits and stalls, then raises usage alerts, so counts and alerts move with nobody looking.

## Alternatives

- **Write `autoCompactWindow` into LADO's managed settings.** A project's own `.claude/settings.json` would win over it, and projects LADO does not manage would get nothing.
- **Show usage in the Timings tab.** It answers "where did the time go" from settled records; usage answers "what is this spending now" and refreshes every minute with alerts. One tab would grow long and merge two APIs in the renderer.
- **Use AO's notifications table.** Its type CHECK means rebuilding an upstream table on every new kind, which LADO's migration rules forbid (TDD §4.2).
- **Retry a failed start without spending an attempt.** The failed start burns its client request id, which carries the attempt number; changing that would change the id scheme and the worker gate. A check before the start avoids the failure instead.
- **End or replace a stalled worker.** Out of scope: the owner decides from the alert.

## Consequences

- Sessions already running keep their compact window; the settings say "applies from the next run".
- A worker reads its plan again from a file the integration branch never holds, so the workspace stays clean.
- A start that fails because the executable vanished right after a passed check still spends an attempt.
- The watch knows only what AO stores: the last turn and the last report. A Codex usage limit has been seen only in the binary's messages, not in a real session.
- New tables (migration 0012), API routes and enum values (`account_limited`, `agent_unavailable`) to keep in step with the app.
