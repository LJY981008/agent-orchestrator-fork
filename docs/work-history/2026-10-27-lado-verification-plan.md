# LADO step 8 — Verification, evidence and integration approval: plan (contracts)

> 승인: 2026-10-27 소유자 — 설계 승인. 소유자 결정 2026-10-27: 범위 "전부 한 번에", 계획 밖 경로는 "경고 + 승인 때 확인"(보호 경로는 계획이 이름을 댄 경우만), 근거는 "워커 근거 파일 + LADO 대조", 리뷰는 "계획이 요구할 때만". 체인 러너로 실행(단계 수·예산은 TSV 머리말). 이 문서가 단계 엔트리의 공통 계약이다. 단계 엔트리가 이 문서보다 구체적이면 엔트리를 따른다.
> 근거 스펙: `docs/LJY-PRD/TDD.md` §1.2, §3.1-§3.3, §4.2-§4.5, §5.3, §7, §8, §9, §13, §14 (gates 6-10), §15 step 8; `docs/LJY-PRD/HARNESS.md` §4-§6, §8, §10, §12, §14, §15 item 5; `docs/LJY-PRD/PRD.md` MERGE-001, MERGE-002, VER-001..003, EVD-001, EVD-002, EXEC-004; `docs/LJY-PRD/adr/0002-human-presence-approval-channel.md`, `adr/0003-enforcement-points.md`.

## 0. Rules for every step

- Read `AGENTS.md` and `CLAUDE.md`; UI steps also read `DESIGN.md`. Code comments in English, matching nearby density. New code goes in LADO files (`lado_*`, `ladoverify`, `ladopolicy`, `ladoqueue`, `ladoproject`, `ladosetup`) inside AO's layers; shared upstream files get only small hook edits (TDD §1.2), counted by the `diff-budget` gates. Never use the word "harness" for LADO concepts.
- **No network, no real repositories.** No step calls GitHub or GitLab or runs `gh`; adapter tests use `httptest` servers, service tests use fakes. Git tests use temporary repositories (`git init` in `t.TempDir()`, `user.name`/`user.email` set per repository, `commit.gpgsign=false`) and run through the gate helper's `*-isolated` subcommands, which point HOME and the XDG folders into a temp dir and set `GIT_CONFIG_NOSYSTEM=1`, so the developer's git config (signing, hooks, aliases) never applies. Never touch the real `~/.ao`, `~/.claude` or `~/.codex`, and never run a daemon against them.
- Query files `backend/internal/storage/sqlite/queries/lado_*.sql` stay ASCII only (sqlc v1.31.1 cuts SQL after non-ASCII); read the generated `gen/*.sql.go` after `npm run sqlc`; qualify every subquery column with its table; pass times in UTC. Never hand-edit `gen/`, `openapi.yaml`, `frontend/src/api/schema.ts`, `routeTree.gen.ts`, `tray-catalog.generated.ts`.
- Store tests use `sqlitetest.MustOpen(t)`; no fresh `sqlite.Open` per test. LADO tables never get FOREIGN KEYs to upstream tables (`sessions`, `projects`, `pr`, `pr_checks`, `review_run`); reading them in LADO queries is fine.
- Renderer strings go in all 8 locale files (`frontend/src/renderer/i18n/{en,ko,ja,zh-CN,es,fr,de,pt-BR}.json`, flat keys, appended after the existing LADO keys) with real translations. Korean glossary: 검증, 검증 중, 통합 대기, 통합 중, 통합 승인, 필수 검사, 검사 명령, 커밋 메시지, 변경 경로, 보호 경로, 계획 밖, 기준 브랜치, 원격 보호, 보호 안 됨, 근거, 추론, 리뷰, 결론 없음, 병합. "head" (a PR's head commit) stays the English word in Korean too (`head: {{head}}`, not 헤드; changed in the review fixes of #15). No particle right after a placeholder in Korean (rephrase instead of `을(를)`). The coverage test fails on hardcoded English in JSX.
- `<button>` ignores Tailwind font classes (unlayered button rule): style an inner `<span>`. The Write tool turns `\u` escapes into real characters; write such strings with python if they must stay escaped.
- No `git push`, `gh`, PRs, publishing or writes outside the worktree (the guard refuses them), except the temp dirs tests create.
- Subagents and long commands only in the foreground (`run_in_background: false`): when a `claude -p` session ends its last turn, it waits a bounded time for background work and then kills it, and the step ends without its commit.
- The design and its approvals are done: skip brainstorming and planning gates of any skill and do not ask the user questions — implement this step, commit, rerun the gates, fill After/회고.
- Every step leaves `go build ./... && go vet ./...` green, and from S16 on also `npm --prefix frontend run typecheck`. Before the `tree-clean` gate, check `git status`: revert stray `go.work.sum` lines with `git checkout -- go.work.sum`.
- Step 0 of every entry: read the earlier entries' `## 비교/회고` and their "다음 단계 메모" (the runner only hands over the previous commit). An entry edited mid-chain by the main session is committed at once with a pathspec.

## 1. Scope and decisions

In: verification of a LADO execution's PR head (required checks, local check commands, commit-message and path validators, review policy, base-branch movement, remote protection, evidence), the derived states Validating, Integration pending and Integrating and the new meaning of Completed (TDD §3.2), the integration approval through the desktop app with the `lado/integration` commit status (TDD §7.1), the integration check in AO's merge route (TDD §7.2), the four protected gates of HARNESS §14 and the Korean commit rule's validator, the snapshot format `lado-policy/3`, the setup conflict for allow rules against protected gates (HARNESS §10), the adopted instructions for AO's reviewer, fix requests to the worker, routes, the desktop confirmations, the screens, docs.

Out (later, recorded in docs at S19): creating rulesets from the app; GitLab's protected branches with external status checks (GitLab projects stay "unprotected: detection only"); cancelling runs on an emergency change to a protected gate (HARNESS §8); the origin rule for Chat tool-approval resolves (ADR-0002 "Frequent approvals"); integration runs as their own tasks from a button (MERGE-002: a follow-up is a new task, as today); more than one PR per execution (only the session's newest PR is verified, as step 6 reads it); a desktop notification for Integration pending; Windows Job Objects for check commands (step 9 measures; AO's tree kill is used); the real check against a GitHub repository with a ruleset (PRD gate 7's manual half: the main session's lab, after asking the owner, because it writes to GitHub).

Owner decisions (2026-10-27):

1. Build the whole step at once.
2. A changed path outside the plan's `affected_paths` needs the developer's confirmation in the integration dialog and does not block; a protected path (the `paths.protected` gate) blocks unless the approved plan names it.
3. Evidence: the worker writes `lado/evidence.md` in its session artifact folder, and LADO checks every entry against real records (PR checks, LADO check runs, commits); unmatched entries are claims, unlisted criteria inferred.
4. AO code review runs only when the plan says `review: required`.

Facts found while designing (2026-10-27; do not measure again):

- GitHub `GET /repos/{o}/{r}/rules/branches/{branch}` returns the active rules that apply to the branch from every level, never those of rulesets in `evaluate` or `disabled`; it needs only Metadata read. Rule types: `required_status_checks` (`parameters.required_status_checks[]` of `{context, integration_id?}`, `strict_required_status_checks_policy`), `pull_request`, `non_fast_forward`, `deletion`. `POST /repos/{o}/{r}/statuses/{sha}` takes `{state, target_url, description, context}`, needs Commit statuses write (classic: `repo:status` or `public_repo`/`repo`), contexts are case-insensitive, 1000 statuses per sha and context. Private repositories of personal accounts on GitHub Free probably cannot enforce rulesets, so "unprotected" is a common, first-class case. The fork itself is public and has no ruleset.
- LADO's GitHub requests go through `adapters/tracker/github/lado_issues.go` (`LadoIssues`, with its own `send` that keeps status codes and escapes a branch as `(&url.URL{Path: b}).EscapedPath()`), reached as `ports.LadoRepoClient` from `daemon/lado_repo_clients.go` `ForRepo(ctx, repo, token)`. `CheckAccess` already probes the rules endpoint and the status permission. AO's SCM provider double-encodes a branch with `/` (`repoPath` + `restURL`); do not use it for these calls.
- AO's merge (`service/pr/action_service.go:95-155`) checks the stored PR, then the live PR and readiness, then calls the SCM merger with the expected head; nothing can veto it. `httpd/controllers/prs.go` `writePRError` turns any unknown error into 500, while `envelope.WriteError` renders an `*apierr.Error` with its kind, code and details.
- AO stores commit statuses in `pr_checks` (name = context, `commit_hash` = the PR head when observed, no "required" flag); a status-only change does not make the PR observer refresh, and a new `head_sha` publishes no `pr_updated`. So LADO reads the live head itself before an approval, and its own Merge goes through the route, which fetches live.
- AO reviews: `reviewsvc.Manager.TriggerRequested(ctx, workerID, TriggerRequest{Harness, Source, PRURL, RejectReviewedHead, ...})` (`service/review/review.go:428-505`; `Source` empty, `manual` or `agent`); a run reviews the PR head AO last saw (`review_run.target_sha`), has no timeout, and its verdict (`approved`, `changes_requested`) never reaches the worker; `Cancel` is per worker session; `ListReviewRunsBySession` reads runs. The reviewer is launched by `review/launcher.go`, which writes the reviewer's `system.md` in `prepareInvocation` and `prepareIdleInvocation`; the launcher is built before LADO is wired.
- AO captures every live session's artifact folder (`<data>/artifacts/<session>/`, polled every 30 s, a file versioned once stable across two polls); `Capturer.List`/`ReadVersion` read the versions, as LADO's plan watcher does. A worker learns its folder from the `## Session Artifacts` system-prompt section.
- `session_manager.Manager.Send` with no options counts as the daemon's message; the LADO gate allows it for executions; in Chat it is queued mid-turn. LADO's plan loop already sends through the same manager (`daemon/lado_plans_wiring.go` `ladoMessageSender`).
- `process.ConfigureTreeCancellation(cmd)` (`backend/internal/process`, imported as `aoprocess`) sets up a process group and kills the tree on cancel (Unix SIGKILL to the group; Windows `taskkill /T /F`). AO runs PostCreate steps with `sh -c` (Unix) or `cmd /c` (Windows) in the worktree, with `os.Environ()` plus the project's env, 10 minutes per step.
- The PR row's `base_sha` is GitHub's base tip, not a merge-base (GitLab's is a merge-base); LADO computes the merge-base itself. `domain.PullRequest` has `HeadSHA`, `BaseSHA`, `TargetBranch`, `Title`, `CI`; `Store.ListChecks(prURL)` returns every commit's checks.
- The plan's front matter is parsed (`domain/lado_plan.go`): `AcceptanceCriteria`, `AffectedPaths` (non-empty; `path.Match` syntax, relative, no leading `/`, no `..`), `TestCommands` (may be empty), `ReviewRequired`, `Reviewer` (free text, unchecked). Nothing reads `AffectedPaths`, `TestCommands` or `Reviewer` yet.
- `lado_presence_proofs` is keyed by `nonce`; approvals refer to it through a UNIQUE `proof_nonce` (`0004_lado_plans.sql`). The proof's HMAC covers method, path and body, not the action or subject, so path and body must name what is approved.
- The snapshot's `workflowGates` is written and read by nothing; there is no `validators` field. `EnsureBuiltins` seeds only missing built-ins.
- Electron main's `confirm(deps, t, message, detail, label)` passes a multi-line `detail`; `oneLine` and `excerpt` strip newlines, so clean each line on its own and join with `\n`.

My decisions (record in docs at S19):

a. **A loop computes verification and stores it per head.** `service/ladoverify` runs a pass every 15 s, when the daemon starts it, and right after its own writes. For each execution whose session's newest PR is open, or merged or closed and not yet settled, it reads the PR's current head and target branch, evaluates the items (b), starts what is missing (validators, check commands, a review, the evidence pin), and upserts the verification record for (execution, head). Reads use the record whose head equals the PR's current head; without one the task is Validating with `verifying`. Task states stay derived (TDD §3.2): the record is evidence about one head, like a check run. At most 50 executions per pass, oldest first.
b. **Items, in this fixed order, each with an outcome** `pass`, `wait`, `fail`, `confirm` or `info`, a code and its details: `required_checks`, `commands`, `commit_messages`, `paths`, `base_branch`, `protection`, `review`, `evidence`. An item that does not apply has outcome `info` and code `not_required`. The record's overall outcome: `fail` if any item fails, else `wait` if any waits, else `confirm` if any needs confirming, else `pass`. Its reason is the first failing or waiting item's code (or empty).
c. **The record's hash.** `verificationSha256` = sha256 of the canonical JSON `lado-verify/1` (Go struct field order is the format): `format`, `prUrl`, `headSha`, `items` with kind, outcome, code and details, never times. The integration approval signs it, so any change in what the developer saw refuses the approval.
d. **Required checks** (`test.command.must_pass`, remote part). The base branch's ruleset contexts in `required_status_checks`, minus `lado/integration` (compared without case). When the ruleset names some, each needs a `pr_checks` row for (PR, head) that `passed` or was `skipped` → pass; missing, `unknown`, `queued`, `in_progress` → wait `checks_pending`; `failed`, `cancelled` → fail `checks_failed` (AO already nudges the worker). When none are named (no ruleset, unreadable, GitLab): every check recorded for the head must end `passed` or `skipped`; any still running → wait; any failed → fail; none recorded → pass with `none_reported`. Checks that are not required are listed in the details. An unreadable ruleset uses the last good read of the cache; without one, the "none named" rule.
e. **Check commands** (`test.command.must_pass`, local part; TDD §8.2). Project settings `checkCommands` (0-10 of `{name, command}`: name 1-40 characters `^[A-Za-z0-9][A-Za-z0-9 ._-]*$`, unique; command 1-2000 characters, no NUL) and `checkTimeoutMinutes` (5-120, default 30). Changing the commands needs the desktop app's confirmation (presence action `project.check_commands`), because the daemon runs them outside any agent sandbox; the timeout does not. For each head, the loop runs the commands in order in the execution's worktree, one run at a time for the whole daemon, only while `git status --porcelain` is empty and `HEAD` is the head SHA (otherwise wait `worktree_not_at_head`); a missing worktree fails `worktree_missing`. `sh -c` (Unix) or `cmd /c` (Windows), the daemon's environment without `AO_*` and the token variables (`GITHUB_TOKEN`, `GH_TOKEN`, `GITLAB_TOKEN`, `GL_TOKEN`, `AO_GITHUB_TOKEN`, `AO_GITLAB_TOKEN`, `AO_GITLAB_HOST_TOKENS`) plus the project's env, `process.ConfigureTreeCancellation`, the timeout (then fail `command_timed_out`; the task Failed reason `timed_out` is another thing). A command still running or not started for the head → wait `commands_running`; a non-zero exit or a start error → fail `commands_failed`. Combined output, its last 1 MiB, goes to a blob; exit code, duration and blob into `lado_check_runs`. A daemon stop cancels running runs; at start, `running` rows become `cancelled` and run again. "Run again" (route) starts a new run of every command for the current head. No commands and no required checks → `commands` is confirm `no_tests`; no commands but required checks → pass `none_configured`.
f. **Commit messages** (`git.commit.message.korean` with validator enforcement on in the snapshot; else `not_required`). The commits `git rev-list --no-merges <merge-base>..<head>`, the merge-base taken with `refs/remotes/<remote>/<target>` (falling back to the PR's `base_sha` when that object exists). Each subject: strip an allowed Conventional Commit prefix `^(type)(\([^)]*\))?!?:\s*` (types from the rule config, default `feat, fix, docs, refactor, test, chore, style, perf, build, ci, revert`), then issue refs (`#123`, `ABC-123`), backtick spans, URLs, paths and file names, identifiers with `_`, `.` or inner capitals, and numbers. A Hangul syllable left → ok; at least two Latin words of three or more letters and no Hangul → `not_korean`; else `unclear`. A body (lines after the first, without trailers such as `Co-Authored-By:`) with no Hangul and at least five Latin words is `unclear`. The PR title is checked like a subject (it becomes the squash commit's subject). Any `not_korean` → fail `commit_messages_failed`; any `unclear` → confirm `commit_messages_unclear`; else pass. HARNESS §5: uncertain cases go to the developer.
g. **Paths** (`paths.protected` and the plan's `affected_paths`). Changed paths: `git diff --name-status -M -z <merge-base> <head>`, both sides of a rename or copy, relative to the repository root and, in a workspace project, prefixed with the repository's folder in the workspace (the plan's paths are relative to the project root). Matching: `path.Match` per segment, `**` matching zero or more segments, and a pattern without a wildcard also matching everything below it. A changed path that a protected glob matches fails `protected_path` unless the plan names it: an `affected_paths` entry matches the path and its fixed prefix (up to the first wildcard character) is at least as long as the protected glob's. A changed path no `affected_paths` entry matches is listed under confirm `outside_plan` (the first 50, and the count). Snapshots without the gate (`lado-policy/2`) check only `outside_plan`.
h. **Base branch** (`git.protected_branch.no_direct_write`, local part; TDD §7.3). The dispatcher records, with `dispatch_base_sha`, the base branch and remote it resolved and the local `refs/heads/<branch>` commit (empty without a local branch). The check reads `git reflog show --date=unix --format=%H%x1f%gd%x1f%gs refs/heads/<branch>` in the repository that holds the issue and takes the entries after the execution's `started_at`: each entry's commit must be reachable from `refs/remotes/<remote>/<branch>`; one that is not → confirm `base_moved` with its selector, commit and subject; the local tip itself counts too. No reflog for the branch → confirm `reflog_off`; no local branch → pass `no_local_branch`. A head already reachable from the remote base before any approval → confirm `already_on_base` (the work reached the base without LADO). The loop does not fetch; the approval and its dry run fetch `<remote> <branch>` first (prompts off, 30 s).
i. **Remote protection** (`integration.approval.required` and `no_direct_write`, remote part; TDD §7.1). GitHub: the branch rules through the LADO repo client with the stored token. Pass when they hold `pull_request`, `required_status_checks` naming `lado/integration`, `non_fast_forward` and `deletion`; otherwise confirm `unprotected` with the missing parts; unreadable → confirm `protection_unknown` with the error (redacted). GitLab → confirm `unprotected_gitlab`. A cache per (repository, branch) keeps reads for 10 minutes; it is read again before each approval and its dry run, when LADO mode is turned on, and on "Check again".
j. **Review** (VER-002, VER-003; only when the approved plan says `review: required`, else `not_required`). It starts once `required_checks`, `commands`, `commit_messages` and `paths` have no wait or fail on the head. LADO calls the review service with the PR's URL, `RejectReviewedHead: true`, source `manual`, and the plan's `reviewer` when it is a known reviewer harness (else AO's choice, noted in the details), and records each created run with its round, 1 to 3 per execution. The newest finished run on the head, from any source, decides: `approved` → pass; `changes_requested` → fail `changes_requested` (the worker gets the review's body once, r); a failed or cancelled run lets the next pass trigger again while rounds remain; a LADO run still running after 30 minutes is cancelled through the review service and counts. No approval on the head after three rounds → confirm `review_inconclusive`. While waiting → wait `review_pending`.
k. **Evidence** (EVD-001, EVD-002; outcome always `info`, never blocks). The worker prompt's lead gains a paragraph: before `ao report --done`, write `lado/evidence.md` in the session artifact folder, Markdown whose YAML front matter has `head` (the commit it describes) and `criteria`: a list of `{criterion: <1-based index into acceptance_criteria>, evidence: [{check: <CI check name>} | {command: <LADO check command name>} | {commit: <sha>}], note}`. The loop pins the newest captured version (bytes re-hashed against AO's record, copied to the blob store) and resolves each entry: `check` → a passed `pr_checks` row of the head with that name (`pr_check`), `command` → a passed check run of the head (`lado_check_run`), `commit` → a commit in merge-base..head (`commit`), anything unmatched → `claim`. A criterion is `linked` (at least one resolved ref), `stale` (the file's `head` is not the PR's head), `claimed` (entries, none resolved) or `inferred` (not listed). Links are stored per head.
l. **States** (`domain/lado_task.go` `taskState`; API enum `validating`, `integration_pending`, `integrating`). With an open PR: no record for the head, or one whose overall outcome is `wait` or `fail` → Validating, reason the record's (or `verifying`); `pass` or `confirm` → Integration pending; an integration approval for the current head → Integrating (reason `status_not_posted` while the GitHub status could not be posted). Merged → Completed, with reason `merged_without_approval` when the merged head has no approval (attention tone; audit `execution.merged_without_approval` once, by `lado`). Closed → Closed unmerged. No PR → as today (Running, Failed). `LadoExecutionPR` gains `HeadSHA`, `TargetBranch` and `Title`.
m. **Integration approval** (TDD §7.1, §13). `POST /lado/executions/{sessionId}/integration-approve` with `{prUrl, headSha, verificationSha256, acknowledge, dryRun}`, presence action `integration.approve`, subject `<sessionId>@<headSha>`. The dry run reads the PR's live head (AO's SCM reader), the protection fresh and the base after a fetch, recomputes and stores the record, and answers it with its hash and the codes that need confirming; it writes no approval and needs no proof. The real request checks the proof first, then repeats those reads: another live head → 409 `LADO_INTEGRATION_HEAD_CHANGED`; another hash → 409 `LADO_VERIFICATION_CHANGED`; overall `wait` or `fail` → 409 `LADO_INTEGRATION_NOT_READY` with the reasons; `acknowledge` not equal, as a set, to the record's confirm codes → 400 `LADO_INTEGRATION_ACK_MISMATCH`; an approval for the head exists → 409 `LADO_INTEGRATION_ALREADY_APPROVED`. One transaction stores the proof's nonce, the approval and the audit event `integration.approved` (task, session, PR, head, verification hash, acknowledged codes, proof, dev). Then, for GitHub, LADO posts `success` with context `lado/integration` and description "Approved in LADO" on the head; the approval's status becomes `posted`, or `failed` with the redacted error, retried by the loop at most every 5 minutes for 12 attempts; GitLab is `not_applicable`. LADO never merges. Approvals are per head and never deleted.
n. **Merge gate** (TDD §7.2). `service/pr/action_service.go` gets `SetMergeGate(ports.LadoMergeGate)` (nil keeps upstream), whose `AuthorizeMerge(ctx, pr domain.PullRequest, headSHA string) error` runs right after the stored head check, before the live fetch. LADO's gate refuses unless an integration approval exists for that head when the PR's session is a LADO execution, or belongs to a project with LADO mode on: 409 `LADO_INTEGRATION_APPROVAL_REQUIRED` with `{prUrl, headSha, sessionId}`; a read error refuses too (500 `LADO_MERGE_GATE_FAILED`). `writePRError` renders an `*apierr.Error` through `envelope.WriteError` (three lines at its top). The mobile app and the desktop Merge button meet the same gate.
o. **Protected rules** (HARNESS §14). Four built-ins, protected, Global on, version 1 by `lado`:
   - `integration.approval.required` (category `workflow`, `[workflow_gate, prompt]`): "Never merge your pull request or ask anyone to merge it; the developer approves the integration in LADO."
   - `git.protected_branch.no_direct_write` (category `git`, `[workflow_gate, prompt]`): "Never push to, reset or update the base branch, locally or on the remote; work only on your own branch and open a pull request."
   - `test.command.must_pass` (category `workflow`, `[validator, prompt]`): "Before you push, run the project's tests and checks; LADO runs the project's check commands and reads the required CI checks on your pull request's head."
   - `paths.protected` (category `git`, `[validator, prompt]`, config `{"globs": [...]}` with the defaults `.github/**`, `.gitlab-ci.yml`, `.gitlab/**`, `**/CLAUDE.md`, `**/AGENTS.md`, `.claude/**`, `.codex/**`, `.mcp.json`, `.ao/**`): "Do not change protected paths unless the approved plan names them: {globs}." (the prompt text lists the configured globs). Its globs change only with the desktop app's confirmation: `PATCH /lado/rules/paths.protected` with `{"baseVersion": n, "paths": {"globs": [...]}}` (1-50 globs, each valid as an `affected_paths` entry), presence `rule.protected_change` as for the ceiling.
   The Korean commit rule gets built-in version 2: version 1's content plus `validator` enforcement and the config `{"language":"ko","allowPrefixes":[...]}`. `EnsureBuiltins` appends a built-in's newer version, by `lado` with the audit event `rule.builtin_upgraded`, only when the rule's active version is byte-identical to an earlier built-in version; a rule the developer edited keeps its versions, and its validator runs only while its enforcement holds `validator`.
p. **Snapshot `lado-policy/3`**: after `workflowGates` comes `validators`, one entry per on rule with validator enforcement: `{ruleId, kind, config}` with kinds `checks_must_pass`, `protected_paths` (`{globs}`) and `commit_message_locale` (`{language, allowPrefixes}`). Every approval made before step 8 shows Review required once (the compiler name and the new protected rules change the hash), as step 7's did. Executions whose snapshot is `lado-policy/2` get every item except `commit_messages` and the protected half of `paths`.
q. **Setup conflict** (HARNESS §10): warning `LADO_SETUP_ALLOWS_PROTECTED` for an adopted `permissions.allow` rule that covers `git push`, `git update-ref`, `git merge`, `git reset` or `gh pr merge` (`Bash(...)` prefix rules, read as `bashRuleCovers` reads deny rules); acceptable like the other warnings: "LADO enforces the gate elsewhere".
r. **Fix requests** (HARNESS §5, `block_and_request_fix`). When `commands`, `commit_messages`, `paths` (protected) or `review` (changes requested) first fails for a head, the loop sends the worker one daemon message through the session manager: the head's 7-character SHA, the item, what failed (at most 4 KiB, check-run log tails cut to 1 KiB each, review body cut to 2 KiB) and "Push a fix; LADO checks the new head." Once per (execution, head, item), recorded in the record's `notified` list (outside the hash). Only while the session lives. CI failures and review comments stay AO's nudges.
s. **Reviewer instructions** (step 7's deferral). AO's reviewer of a LADO execution gets a `## User instructions (managed by LADO)` section in its `system.md`: the adopted instructions of the reviewer's own tool (Claude Code: the on top-level `*.md` items; Codex: the first of `AGENTS.override.md` and `AGENTS.md` with text), at the versions the execution's snapshot pinned when the snapshot's tool is the reviewer's, else the current versions on for the project. No settings, hooks, skills, agents, commands or MCP servers reach a reviewer, so its read-only tool policy holds. Hook: one late-bound setter in `review/launcher.go`, called from `prepareInvocation` and `prepareIdleInvocation`.
t. **Merge after approval.** The task view and the run panel offer Merge once a head is approved: AO's `POST /api/v1/prs/{id}/merge` with that head, which the gate lets through. GitHub's own merge works once the status is there.

## 2. Storage — LADO migration `0008_lado_verify.sql`

```sql
-- +goose Up
ALTER TABLE lado_executions ADD COLUMN base_branch TEXT NOT NULL DEFAULT '';
ALTER TABLE lado_executions ADD COLUMN base_remote TEXT NOT NULL DEFAULT '';
ALTER TABLE lado_executions ADD COLUMN dispatch_local_base_sha TEXT NOT NULL DEFAULT '';
-- When the loop settled the execution's merged or closed PR; NULL while it still verifies.
ALTER TABLE lado_executions ADD COLUMN settled_at TIMESTAMP;

ALTER TABLE lado_project_settings ADD COLUMN check_commands TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(check_commands));
ALTER TABLE lado_project_settings ADD COLUMN check_timeout_minutes INTEGER NOT NULL DEFAULT 30 CHECK (check_timeout_minutes BETWEEN 5 AND 120);

CREATE TABLE lado_check_runs (
    id                   TEXT PRIMARY KEY CHECK (id <> ''),
    execution_session_id TEXT NOT NULL REFERENCES lado_executions (session_id),
    head_sha             TEXT NOT NULL CHECK (length(head_sha) IN (40, 64)),
    name                 TEXT NOT NULL CHECK (name <> ''),
    command              TEXT NOT NULL CHECK (command <> ''),
    status               TEXT NOT NULL CHECK (status IN ('running', 'passed', 'failed', 'timed_out', 'cancelled', 'error')),
    exit_code            INTEGER,
    log_sha256           TEXT NOT NULL DEFAULT '',
    error_message        TEXT NOT NULL DEFAULT '',
    started_at           TIMESTAMP NOT NULL,
    finished_at          TIMESTAMP
);
CREATE INDEX lado_check_runs_by_head ON lado_check_runs (execution_session_id, head_sha, name, started_at);

CREATE TABLE lado_verifications (
    execution_session_id TEXT NOT NULL REFERENCES lado_executions (session_id),
    head_sha             TEXT NOT NULL CHECK (length(head_sha) IN (40, 64)),
    pr_url               TEXT NOT NULL CHECK (pr_url <> ''),
    outcome              TEXT NOT NULL CHECK (outcome IN ('pass', 'wait', 'fail', 'confirm')),
    reason               TEXT NOT NULL DEFAULT '',
    content_json         TEXT NOT NULL CHECK (json_valid(content_json)),
    sha256               TEXT NOT NULL CHECK (length(sha256) = 64),
    notified_json        TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(notified_json)),
    computed_at          TIMESTAMP NOT NULL,
    PRIMARY KEY (execution_session_id, head_sha)
);

CREATE TABLE lado_execution_reviews (
    review_run_id        TEXT PRIMARY KEY CHECK (review_run_id <> ''),
    execution_session_id TEXT NOT NULL REFERENCES lado_executions (session_id),
    head_sha             TEXT NOT NULL,
    round                INTEGER NOT NULL CHECK (round BETWEEN 1 AND 3),
    created_at           TIMESTAMP NOT NULL,
    cancelled_at         TIMESTAMP
);

CREATE TABLE lado_evidence_links (
    id                   TEXT PRIMARY KEY CHECK (id <> ''),
    execution_session_id TEXT NOT NULL REFERENCES lado_executions (session_id),
    head_sha             TEXT NOT NULL,
    criterion            INTEGER NOT NULL CHECK (criterion >= 0),
    state                TEXT NOT NULL CHECK (state IN ('linked', 'stale', 'claimed', 'inferred', 'file')),
    ref_kind             TEXT NOT NULL CHECK (ref_kind IN ('pr_check', 'review_run', 'artifact_version', 'lado_check_run', 'commit', 'claim', 'none')),
    ref_key              TEXT NOT NULL DEFAULT '',
    sha                  TEXT NOT NULL DEFAULT '',
    created_at           TIMESTAMP NOT NULL
);
CREATE INDEX lado_evidence_links_by_head ON lado_evidence_links (execution_session_id, head_sha, criterion);

CREATE TABLE lado_integration_approvals (
    id                   TEXT PRIMARY KEY CHECK (id <> ''),
    execution_session_id TEXT NOT NULL REFERENCES lado_executions (session_id),
    pr_url               TEXT NOT NULL CHECK (pr_url <> ''),
    head_sha             TEXT NOT NULL CHECK (length(head_sha) IN (40, 64)),
    verification_sha256  TEXT NOT NULL CHECK (length(verification_sha256) = 64),
    acknowledged_json    TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(acknowledged_json)),
    proof_nonce          TEXT NOT NULL UNIQUE REFERENCES lado_presence_proofs (nonce),
    actor                TEXT NOT NULL CHECK (actor <> ''),
    approved_at          TIMESTAMP NOT NULL,
    status_state         TEXT NOT NULL CHECK (status_state IN ('pending', 'posted', 'failed', 'not_applicable')),
    status_attempts      INTEGER NOT NULL DEFAULT 0,
    status_posted_at     TIMESTAMP,
    status_error         TEXT NOT NULL DEFAULT '',
    UNIQUE (execution_session_id, head_sha)
);
-- +goose Down: drop the five tables, then the six columns (SQLite DROP COLUMN), in reverse order.
```

Evidence: criterion 0 with state `file` and ref `artifact_version` (`<session>/<path>@<version>`, sha256) records the pinned evidence file itself.

Change events (`ladoCDCTriggers`, `storage/sqlite/lado_migrate.go`; a variant builder that takes the session id from `execution_session_id` and the task and project through `lado_executions`): `lado_execution` with `{sessionId, taskId, projectId}` on insert of `lado_verifications` and on update of its `outcome`, `reason` or `sha256`; on insert of `lado_integration_approvals` and on update of its `status_state`; on insert of `lado_check_runs` and on update of its `status`. Evidence links and reviews publish nothing (the record covers them). The settings table keeps zero triggers.

## 3. Domain (`domain/lado_verify.go`, `domain/lado_task.go`, `domain/lado_queue.go`, `domain/lado_rule.go`, `domain/lado_presence.go`)

Types for the record (`LadoVerification` with `Content()` canonical bytes and `SHA256()`), its items and their detail structs (one per kind, fields named in §1 b-k), outcomes, codes and reasons, `LadoCheckRun`, `LadoCheckCommand` with `ValidateLadoCheckCommands`, `LadoIntegrationApproval` and its status states, `LadoEvidenceLink`, the review round cap (3), the task states `validating`, `integration_pending`, `integrating` and the reasons, the rule ids of the four gates and their configs (`LadoProtectedPathsConfig`, `LadoCommitLocaleConfig`), the presence actions `integration.approve` and `project.check_commands`, and `LadoExecution`'s new facts (base branch and remote, local base SHA, the PR's head, target and title, the record's head, outcome and reason for the current head, the approval for the current head and whether the merged head has one).

Item codes (the API enum; empty for a plain pass): `required_checks` — `checks_pending` (wait), `checks_failed` (fail), `none_reported` (pass); `commands` — `commands_running`, `worktree_not_at_head` (wait), `commands_failed`, `command_timed_out`, `worktree_missing` (fail), `no_tests` (confirm), `none_configured` (pass); `commit_messages` — `commit_messages_failed` (fail), `commit_messages_unclear` (confirm); `paths` — `protected_path` (fail), `outside_plan` (confirm); `base_branch` — `base_moved`, `reflog_off`, `already_on_base` (confirm), `no_local_branch` (pass); `protection` — `unprotected`, `protection_unknown`, `unprotected_gitlab` (confirm); `review` — `review_pending` (wait), `changes_requested` (fail), `review_inconclusive` (confirm); `evidence` — none (info); every kind — `not_required` (info). Task reasons added: Validating — `verifying` and every wait or fail code above; Integrating — `status_not_posted`; Completed — `merged_without_approval`.

## 4. Store (`store/lado_verify_store.go`, `queries/lado_verify.sql`)

Check runs (start, finish, cancel running at start, list per head), the record (upsert keeping `notified_json` unless given, read per head), reviews (insert with round, count, mark cancelled), evidence links (replace for a head), checks of a commit (`pr_checks` by `pr_url` and `commit_hash`), the executions to verify (with their newest PR and project), integration approvals (the approval transaction with proof and audit, status updates, read per head, read for a merged head), project check settings (with their audit events: `project.check_commands_changed` with presence actor, `project.check_timeout_changed` by `api`), and the execution reads extended (`fillLadoExecutionPRs` and a new `fillLadoVerification`) so task lists and the execution view carry the facts of §3.

## 5. Base branch and git (`service/ladoverify/git.go`, `base.go`; `service/ladoqueue/dispatch.go`)

Git helpers with prompts off (`GIT_TERMINAL_PROMPT=0`, `GCM_INTERACTIVE=Never`), `--end-of-options`, 10 s each (fetch 30 s): merge-base, `rev-list --no-merges`, commit messages (`%H%x1f%B%x1e`), `diff --name-status -M -z`, reflog, `merge-base --is-ancestor`, `status --porcelain`, `rev-parse HEAD`, fetch of one branch. The dispatcher's link records the base branch, remote and local commit (§1 h). The base item (§1 h) reads the repository folder the way the dispatcher resolves it (single repository: the project path; workspace: the child holding the issue).

## 6. Validators (`service/ladoverify/commits.go`, `paths.go`, `glob.go`)

§1 f and g; pure functions over git output, tested with tables and temporary repositories (a commit made with `--no-verify` is judged like any other: PRD gate 9).

## 7. Check commands (`service/ladoverify/runner.go`; `service/ladoproject`)

§1 e. The runner owns one slot, runs asynchronously from the pass and records each run; the project service stores and validates the commands and the timeout, asks presence for command changes, and audits.

## 8. Branch rules and commit status (`ports/lado_issues.go`, `adapters/tracker/github/lado_integration.go`, `adapters/tracker/gitlab/lado_integration.go`, `service/ladoverify/protection.go`)

`LadoRepoClient` gains `BranchRules(ctx, repo, branch) (ports.LadoBranchRules, error)` and `PostCommitStatus(ctx, repo, sha string, status ports.LadoCommitStatus) error`; GitLab answers `ports.ErrLadoUnsupported`. Errors carry the HTTP status and never the token. The protection evaluation and its cache follow §1 i; LADO mode's turn-on refreshes it in the background.

## 9. Required checks and review (`service/ladoverify/checks.go`, `review.go`)

§1 d and j. The review port is LADO's own interface (`Trigger`, `Runs`, `Cancel`), adapted in the daemon to `reviewsvc.Manager` and the store.

## 10. Evidence (`service/ladoverify/evidence.go`; `service/ladoqueue/prompt.go`)

§1 k. The prompt's new paragraph also names what LADO checks (required checks, the project's check command names, commit messages when the rule is on, changed paths) so the worker knows a failure report will come.

## 11. The loop (`service/ladoverify/service.go`, `record.go`, `notify.go`)

`Deps` (store, blobs, git, runner, repo clients and token source, PR reader for live heads, review port, artifacts, sender, clock, ids, log), `New`, `Start`/`Tick`/`Wait` like the dispatcher, the record assembly and hash (§1 b, c), fix requests (§1 r), the status retries (§1 m), the settling of merged and closed PRs (§1 l).

## 12. States, reads and wiring (`domain/lado_task.go`; `daemon/lado_verify_wiring.go`, `daemon/lado_wiring.go`, `daemon/daemon.go`)

`taskState` per §1 l; the task DTO's state and reason enums; the execution view's verification; `wireLadoWork` builds the service and starts it after the dispatcher, gives it the review manager (`reviewSvc`, in scope at `daemon.go:686`), the session manager as sender and AO's SCM reader; the shutdown waits.

## 13. Integration approval and merge gate (`service/ladoverify/approve.go`, `mergegate.go`; `ports/lado_merge_gate.go`; hooks in `service/pr/action_service.go`, `httpd/controllers/prs.go`, `daemon/daemon.go`)

§1 m, n.

## 14. Policy, setup conflict, reviewer (`service/ladopolicy`, `service/ladosetup/conflicts.go`, `review/launcher.go`)

§1 o, p, q, s.

## 15. HTTP (`httpd/controllers/lado_verify.go`, `lado_verify_dto.go`, `specgen/lado_operations.go`; LAN-blocked by the existing prefix)

| Route | |
|---|---|
| `GET /lado/executions/{sessionId}` | adds `verification` (head, outcome, reason, sha256, items, evidence, computed time) and `integration` (the current head's approval: time, actor, acknowledged, status state, error) |
| `POST /lado/executions/{sessionId}/integration-approve` (P unless `dryRun`) | §1 m |
| `POST /lado/executions/{sessionId}/checks/run` | run the check commands again for the current head; 202; 409 `LADO_EXECUTION_NO_PR` |
| `GET /lado/executions/{sessionId}/checks/{checkRunId}/log` | the log as `text/plain`; 404 `LADO_CHECK_RUN_NOT_FOUND` |
| `GET /lado/projects/{projectId}/protection` (`refresh` optional) | each repository's base branch with state, missing parts, required checks, check time and error |
| `GET, PUT /lado/projects/{projectId}/settings` | adds `checkCommands` (P when it changes) and `checkTimeoutMinutes`; 400 `LADO_CHECK_COMMANDS_INVALID` `{index, field, reason}` |
| `PATCH /lado/rules/{ruleId}` | accepts `paths` for `paths.protected` (P); 400 `LADO_PROTECTED_PATHS_INVALID` |
| `POST /api/v1/prs/{id}/merge` (AO) | 409 `LADO_INTEGRATION_APPROVAL_REQUIRED`, 500 `LADO_MERGE_GATE_FAILED` |

Other codes: 409 `LADO_INTEGRATION_NOT_READY` `{reasons}`, `LADO_INTEGRATION_HEAD_CHANGED` `{headSha, currentHeadSha}`, `LADO_VERIFICATION_CHANGED`, `LADO_INTEGRATION_ALREADY_APPROVED`; 400 `LADO_INTEGRATION_ACK_MISMATCH` `{required, given}`; 500 `LADO_VERIFY_FAILED`; 503 `LADO_VERIFY_UNAVAILABLE`. `TestRouteSpecParity` needs every route mounted with a nil service (`apispec.NotImplemented`). Regenerate with `npm run api`.

## 16. Electron main (`frontend/src/main/lado-presence.ts`, `frontend/src/shared/lado-presence.ts`, `frontend/src/preload.ts`, bridges, `frontend/scripts/sync-tray-catalog.mjs`)

- `lado:presence:approveIntegration(sessionId)`: main reads the execution and sends the dry run itself, refuses without asking when the outcome is `wait` or `fail` (with the reason) or the head is approved already, and asks "Approve the integration of PR #<n> into <branch>?" with the task, the head's first 12 characters, one line per item (passed counts; each confirm code with its count and up to three examples, each line cleaned on its own), and what happens next (the GitHub status, or "this repository is not protected: LADO only detects"). It signs `{prUrl, headSha, verificationSha256, acknowledge}` with the dry run's values.
- `lado:presence:changeProjectChecks(projectId, commands)`: main reads the project's settings, asks with the commands that would be added, changed or removed (each line cleaned, at most 300 characters), and signs the settings `PUT` with `checkCommands` only.
- `lado:presence:changeRule` also takes the protected paths: main reads the rule, names the globs added and removed, and signs the `PATCH`.
- Strings in main's catalog (`sync-tray-catalog.mjs` KEYS); preload and the three bridges (`lib/bridge.ts`, `test/setup.ts`, `e2e/support/fake-bridge.ts`) gain the two new calls.

## 17. Renderer

- Data: `lib/lado.ts` (types from `schema.ts`, `approveLadoIntegration`, `runLadoChecks`, `fetchLadoCheckLog`, `fetchLadoProtection`, `mergeLadoPR` through AO's merge route, check settings), `hooks/useLado.ts`, `lib/lado-errors.ts` (codes and reasons), `components/lado/lado-task-status.ts` (labels and tones: Validating working, Integration pending needs-you, Integrating ready, Completed without approval attention).
- `LadoExecutionPanel.tsx`: a **Verification** section under the run's snapshot: the head (7 characters), the overall line, one row per item with its dot and one muted detail line, the check commands with their status, duration and a collapsed log with Run again, the evidence per criterion (collapsed), and the actions Approve integration… (Integration pending) and Merge (Integrating).
- `LadoTaskDialog.tsx` `RunStatus`: the new states and reasons, with the same two actions.
- `LadoSessionLine.tsx`: after "LADO · plan r<n> · rules <7 hex>", the state word for Validating, Integration pending, Integrating and Completed without approval.
- `LadoProjectSection.tsx` (Project settings → LADO): a **Checks** card (the commands as editable rows of name and command, add and remove, Save through the desktop confirmation; the time limit select) and a **Protection** card (each repository's base branch: Protected, or Not protected with the missing parts and one muted line on what to set in GitHub's rulesets, the check time and Check again).
- `RulesSection.tsx` (Settings → Rules): the protected card lists the four new gates with their descriptions; `paths.protected` shows its glob count and an Edit dialog (one glob per line) that saves through the desktop confirmation.
- `lib/lado-rules.ts`: the four gates in the built-in table (`seedName` = the seeded English names) with i18n names and descriptions.
- DESIGN.md decisions log: one row for the verification screens (S17) and one for the project checks, protection and protected paths (S18).

## 18. Steps

| Step | Slug | Content |
|---|---|---|
| S01 | storage | migration 0008, queries, sqlc, CDC triggers |
| S02 | domain | record, items, hash, states, reasons, configs, presence actions |
| S03 | store | check runs, records, reviews, evidence, approvals, check settings, execution reads |
| S04 | policy | four gates, the Korean rule's v2 upgrade, `lado-policy/3`, protected paths change |
| S05 | base | git helpers, the dispatch base record, the base item |
| S06 | validators | commit messages and paths |
| S07 | commands | the runner and the project's check commands |
| S08 | protection | branch rules and commit status ports, adapters, the protection item and cache |
| S09 | checks-review | required checks and the review policy |
| S10 | evidence | the prompt paragraph, pinning and resolving evidence |
| S11 | loop | the service, records, hash, fix requests, status retries, settling |
| S12 | states-wiring | task states and reads, the daemon wiring |
| S13 | approval | integration approval, status posting, the merge gate |
| S14 | conflict-reviewer | `LADO_SETUP_ALLOWS_PROTECTED`, the reviewer's instructions |
| S15 | http | routes, DTOs, specgen, `npm run api` |
| S16 | client-presence | renderer data layer, desktop channels, preload, bridges, catalog, locales |
| S17 | run-ui | run panel verification, task footer, board line, DESIGN.md row |
| S18 | project-ui | project checks and protection, protected rules and paths, DESIGN.md row |
| S19 | docs | TDD, HARNESS, PRD, ADR-0003, LADO.md, LADO.ko.md |

## 19. Acceptance tests (PRD gates 6-10, HARNESS §15 item 5)

| Gate | Tests |
|---|---|
| 6 Agent claims | `service/ladoverify` `TestFailingRequiredCheckKeepsValidatingDespiteReport` (a worker report `done` with a failing required check: Validating, `checks_failed`) |
| 7 Merge | `service/pr` `TestMergeAsksTheGate` (nil gate unchanged; a refusal stops before the live fetch and the merge); `service/ladoverify` `TestMergeGateNeedsAnApprovalForTheHead`; controller `TestPRMergeRendersLadoRefusal`; adapter `TestPostCommitStatus` (the request GitHub gets). The real ruleset check is manual (§1 Out) |
| 8 Local base | `service/ladoverify` `TestBaseMovedFromAWorktreeNeedsConfirming` (temp repo with a linked worktree; `git update-ref refs/heads/main HEAD` there → `base_moved`; the approval without that acknowledgement is refused) |
| 9 Commit validator | `TestNoVerifyCommitIsJudged` (temp repo, an English commit made with `--no-verify` → fail, and the approval is refused while it fails) |
| 10 Presence | `TestIntegrationApprovalNeedsPresence`, `TestIntegrationApprovalRefusesReplayedProof`; `TestCheckCommandsChangeNeedsPresence`; `TestProtectedPathsChangeNeedsPresence`; `lado-presence.test.ts` "approving an integration", "changing check commands", "changing protected paths" |
| HARNESS §15 item 5 | `TestKoreanCommitValidator` (clearly English → fail; Korean with `feat:`/`fix(scope):` prefixes → ok; short or code-only → unclear; the PR title is checked) |

## 20. Changes after review (2026-10-28, #15)

Four reviews of the chain's result found defects and doc mismatches; the owner approved a fix plan (LADO task `lado-task-7d4b432f`, plan revision 2) that changes these decisions. Details and tests: `docs/work-history/2026-10-28-lado-verification-review-fixes.md`.

- **a. The loop takes turns.** It reads executions without a record for the PR's head first, then the oldest `computed_at`; an execution it could not verify rests 5 minutes, and a pass reads that many more. An open PR whose head AO does not know yet is not listed. Merged and closed rows are settled per PR URL and state.
- **e and §16. Check commands.** A command with a control character (Cc, U+2028, U+2029) or a direction control character is refused, 400 `LADO_CHECK_COMMANDS_INVALID` with `control_character` or `bidi_control`, `position` and `character`; main refuses it before asking. The confirmation shows each changed command whole, not cut to 300 characters. A change names the list it started from (`checkCommandsBase`, the settings' `checkCommandsSha256`); another list is 409 `LADO_CHECK_COMMANDS_CHANGED`. A run matches its command by name and text (`commandSha256` in the record), and after the first run the worktree's `git status --porcelain` must match the last run's (`worktree_changed`). The runner withholds the environment's credentials (Q4 below) and runs `cmd /d /s /c "<command>"` on Windows.
- **h. Base branch.** The dispatcher's local commit is the reference: the local tip counts only when it differs, and a local branch deleted since dispatch is the confirm `base_deleted`. A head not in the local repository is fetched from the host's PR ref (`refs/pull/<n>/head`, `refs/merge-requests/<iid>/head`, without writing a ref), at most every 5 minutes per head by the loop and every time by an approval or dry run; failing that, the item waits `head_unavailable`, and a worktree behind the host's head gets one message to pull.
- **l. States.** `timed_out` makes a task Failed only while its run has no PR; an open PR's state wins. Settling and `merged_without_approval` apply only to executions dispatched with integration gating (`integration_gated`, 0 for the executions migration 0008 finds, which it settles from their newest merged or closed PR).
- **Q1.** Protected globs and the plan's naming of them match without case.
- **Q2.** A path that several protected globs match needs the plan to name each (kept, now pinned by a test).
- **Q3.** A required `lado/integration` that names an app (`integration_id`) is not protection: missing part `required_status_checks:lado/integration:any_source`, with a hint to allow any source.
- **Q4.** Check commands run without the daemon's tokens and git credentials (`AO_*`, `GH_*`, `GITHUB_*`, `GITLAB_*`, `GCM_*`, `*ASKPASS`, `*_TOKEN`, `*_API_KEY`, `SSH_AUTH_SOCK`, `GIT_SSH*`, `GIT_CONFIG_*`; empty `credential.helper` and `core.askPass`, `GIT_TERMINAL_PROMPT=0`, SSH refused, an empty `GH_CONFIG_DIR`); a T2 command can still read files the developer can (TDD §8.2).
- **Decisions of the fix plan.** Migration 0008 is fixed in place and the hash format stays `lado-verify/1`, because step 8 has not shipped. An integration approval is keyed by (session, PR URL, head, target branch); the merge gate refuses an approval into another branch with 409 `LADO_INTEGRATION_TARGET_CHANGED` (S5), and gates a PR that any LADO record names, whichever session holds it (S7). `POST /lado/executions/{sessionId}/integration-status` posts a status that could not be posted again, without presence (S8). The fork's CI runs the Windows check command test in `windows-workspace` (S1). The run panel shows the state the daemon sends (`state`, `stateReason` on the execution).
