# LADO 7. Plan workers write plans; the orchestrator only hears about them

Date: 2026-10-29
Status: Accepted. Decided by the owner on 2026-10-28 in issue #27 (its two comments of that day, and #33's body and comments, which #27 absorbed). Replaces the plan request flow of [0006](0006-issues-as-work-items.md) and TDD §5.1 as built in steps 4a–4b, in which the project's orchestrator wrote every plan.

## Context

Until #27 a plan request was a coordination message to the project's orchestrator: it read the pinned issue file and wrote `lado/<task-id>/plan.md` into its artifact directory, which LADO pinned. In real use (issues #17–#25, #27, #33) that made the orchestrator the bottleneck and the place where every plan's text piled up:

- **Context.** One orchestrator conversation carried every task's issue, plan and change request. It grew until it had to be reset, and the reset lost what the owner had told it. #27's first revisions planned work boundary resets, size resets and project notes to cope; the owner dropped all of them once plans left the orchestrator, since an orchestrator that does not write plans stays small.
- **Serial work.** While the orchestrator wrote one plan it answered nothing else, and a second request waited behind the first.
- **Review.** The owner's two projects already ran a plan designer and an adversarial plan critic as Claude Code subagents (`.claude/agents/plan-designer.md`, `plan-critic.md`). Analysing 400 of their sessions (#33): designers took a median of 17–23 minutes, critics 14–24, and the critic named a P1 or major defect with a failure scenario in 14 of 17 reviews. That procedure belonged to each repository, so a project without it got none, and LADO could not see or measure it.
- **Write access.** The orchestrator could edit files and run commands; nothing kept a plan writer from starting the work.

Facts at `7863bfe68` (step 8 merged):

- AO starts a Chat (ACP) worker with a client request id, a prompt and an issue context (`ports/session.go`, `service/session/issue_context.go`); its review conversations already run read-only (`service/chat/service.go`, `ConversationOwnerReview`).
- Claude Code's ACP adapter takes `allowedTools` and `disallowedTools` through the session's `_meta.claudeCode.options`; AO's own Claude Code hooks (`ao hooks claude-code pre-tool-use` and `permission-request`) run for every tool call of a session AO starts.
- AO records an artifact version once a file is stable (`artifactversions/capture.go`), which the plan loop already pins (TDD §5.1).
- LADO's worker gate sees every spawn and send (TDD §6), and its rules are versioned and audited (HARNESS §4).

## Decision

- **A plan worker per request.** Every plan request (a new plan, a comment used as the plan, a change request) starts a read-only Chat worker session of its own, the designer, with the client request id `lado:plan/<request>/design/<attempt>`. The orchestrator is not involved. The designer runs with the project's orchestrator agent and model defaults; a plan setting of its own is out of scope. A change request starts a new designer with the previous revision as a file and the developer's note, rather than resuming an old one, since the old session is stopped once its revision is pinned. A newer request for the task replaces an open one and stops its sessions.
- **Read-only, three ways.** The gate grants a `lado:plan/` id only while LADO's plan loop starts that session, and clamps it to auto, never bypass. Claude Code gets an allow and a deny list through the ACP options, and AO's hooks answer each of its tool calls for a session marked `AO_LADO_PLAN_WORKER=1` in place of a person: reading tools, `git log`/`show`/`diff`/`status`/`ls-files`/`ls-tree`, `gh issue view`/`pr view`/`pr diff` (optionally piped into `head`, `tail`, `wc` or `grep`) and the submission command in LADO's exact form pass; everything else is refused without a prompt. Codex runs in its read-only sandbox with approvals off. Another agent fails the request with `plan_worker_agent_unsupported`.
- **Design, adversarial review, revision.** The designer submits a draft with `ao lado plan submit --draft`. LADO skips the review of a small plan: at most N affected paths (Settings → Runs, default 3) and no database schema, external API, authentication or data deletion change, judged from the plan's `change_kinds` declaration and from path patterns (Settings → Runs). Otherwise LADO starts a separate read-only critic session (`lado:plan/<request>/critique/<attempt>`) that gets the draft only and submits JSON findings (`ao lado plan critique submit`); a critic that fails or runs out of time is tried once more. LADO sends the designer the findings, or the skip or failure with its reason. The final plan answers them in a `## 계획 검수` section: every P1 and major finding as addressed, or not addressed with a reason. The daemon writes the final plan into the designer's artifact directory as `lado/<task-id>/plan.md`, which the plan loop pins as before; a pinned plan whose front matter or review section has problems goes back to the designer once, then the sessions stop.
- **Role instructions are LADO's.** What the designer and the critic are told are two built-in global rules, Plan designer and Plan critic (category Planning, enforcement `plan_role`), edited in Settings → Rules with versions and audit like any rule. They take no binding, archive, category change or conflicts, and never reach a task's rules or snapshot. LADO does not depend on a repository's own plan subagents; removing those from the owner's repositories is each project's own later task.
- **Limits.** "Concurrent plan requests" (Settings → Runs, default 2, 1–8) counts a request's designer and critic as one, apart from the run caps; the memory hold applies too. Design has 60 minutes to a draft, each critic 45, the revision 30, a fix round 15, and a request 2 hours 30 minutes in all. Failures are `plan_worker_spawn_failed`, `plan_worker_no_plan`, `plan_worker_timed_out`, `plan_worker_agent_unsupported` and `replaced`.
- **Measured.** Each revision records the design, review and revision times, each session's tokens (input, output, cache read, cache write) and cost when AO priced it, the findings by severity, how many the plan addressed and whether the review ran, was skipped or failed. The plan view shows them in one line.
- **The orchestrator hears one line.** LADO keeps a notice per event in an outbox (request taken, revision pinned, plan request failed, run started, PR opened, work completed, run failed), each one Korean line without plan text, and sends a project's waiting notices together once its orchestrator is idle. LADO never starts an orchestrator for them; notices nobody takes within a day, or beyond the newest 20, are dropped, since the board shows the same state.
- **Requests from before.** A request stored before plan workers keeps going to the orchestrator that it named, and its plan is pinned as before.

TDD §5.1 has the details, HARNESS §12 the screens.

## Alternatives

- **Keep the orchestrator writing plans, with resets.** #27's first revisions. Resets lose what the owner told the orchestrator, need heuristics for when, and keep plans serial. Dropped by the owner.
- **The repository's own plan subagents.** They run inside one session, so the critic sees the designer's reasoning rather than the draft alone, and LADO can neither enforce nor measure them. A project without them gets no review.
- **A terminal plan worker.** AO's terminal workers take a permission mode but no per-tool decision LADO could make for them; Chat gives the ACP tool lists and AO's hook decision, and Codex's read-only sandbox.
- **Resume the designer for a change request.** Its session is stopped once its revision is pinned, and a new one needs only the previous revision and the note on top of the first request's path.
- **Notices as orchestrator spawns.** Starting an orchestrator to say one line costs a full agent session; the board already shows every state.

## Consequences

- Plans no longer cost the orchestrator's context, and up to the cap run at once. Each costs real agent turns: a reviewed plan is two sessions, about 30–45 minutes by #33's medians.
- A plan worker writes nothing in its worktree, so a plan can only describe the repository at the base AO's worktree starts from.
- The hook decision is the third line of defence for Claude Code: a shell command it cannot read statically is refused, so a plan worker cannot run a build or a test to learn about the code.
- Plan workers cannot see the project's Claude memory (#24); the facts they need come from the prompt, the repository's documents, or the Plan designer rule the owner edits.
- LADO adds two submission routes, an `ao lado` command group and five small upstream hooks (TDD §1.2).
