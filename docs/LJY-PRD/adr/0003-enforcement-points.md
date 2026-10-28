# LADO 3. Where LADO enforces approval

Date: 2026-10-19
Status: Accepted. Item 6 amended 2026-10-19 after measurement; item 5 amended the same day while building step 3; items 1, 2, 3 and 7 amended 2026-10-27 while building step 8 (end of this record).

## Context

LADO needs two guarantees against agent mistakes (T1): no worker runs without an approved plan, and nothing reaches the base branch without an approved integration.

Facts at `55b02b848`:

- **Who opens and merges PRs.** AO never opens PRs; agents do, as told by the prompt (`session_manager/prompt.go:273,284`). AO's merge route checks head SHA, CI, comments and mergeability (`service/pr/action_service.go:95-155`, `domain/pr.go:248-264`), but not approval.
- **Branch protection.** AO reads none for GitHub. When GitHub reports a merge as `BLOCKED`, AO's mergeability fact already shows it (`adapters/scm/github/observer_provider.go:631-633,652-667`).
- **Local Git.** A linked worktree can move the local base branch, and `--no-verify` skips commit hooks ([0001](0001-threat-model-t1-first.md)).
- **Where workers start.** Every worker start goes through `Manager.Spawn` (`session_manager/manager.go:945`). Callers include the sessions route, `ao spawn` from an orchestrator (`ParentSessionID`, `ports/session.go:27-30`), `/orchestrators/delegate` (`service/session/delegation.go:66-123`), automations (`service/automation/scheduler.go`), tracker intake (`observe/trackerintake/observer.go:206-211`) and cues (`service/cue/invoke.go`).
- **Where messages go.** Messages reach agents through `Manager.send` (`session_manager/manager.go:4899`) and the chat service (`service/chat/service.go:1213,1304`). CI and review feedback is forwarded automatically by default (`session_manager/manager.go:5473-5475`) and reaches `Manager.send` through the lifecycle messenger (`daemon/lifecycle_wiring.go:445-463`).
- **Intake and automations.** Tracker intake is off by default (`config/config.go:169-171`). The automation scheduler always runs (`observe/automations/observer.go:14`).
- **Codex permissions.** Codex `default` maps to bypass: `--dangerously-bypass-approvals-and-sandbox` in the TUI (`backend/pkg/agentruntime/command.go:198-208`), and `never` with `danger-full-access` in the app-server (`adapters/chatdriver/codexappserver/driver.go:602-654`).

## Decision

1. **Remote merge: GitHub ruleset.** The base branch of a LADO project requires a pull request and these status checks: the CI checks and `lado/integration`. Force pushes and deletion are blocked. After an integration approval, LADO posts a `success` status `lado/integration` on the approved head SHA. A new push has no status, so the merge is blocked again. LADO reads the ruleset when LADO mode is turned on and before each approval. Without it, the project is "unprotected: detection only", and each integration needs an explicit acknowledgement.
2. **AO merge route.** In LADO projects it also requires an integration approval for `expectedHeadSha`.
3. **Local base ref: detection.** LADO records the base SHAs at dispatch. In VALIDATING and before approval it compares them again and reads the reflog. Unexpected movement blocks the execution.
4. **Worker gate.**
   - A new worker session in a LADO project needs a dispatch grant: `ClientRequestID = lado:<queue-entry>` matching a lease the dispatcher holds. That rejects every other spawn path listed above.
   - Messages are judged by origin: human, daemon nudges about the session's own PR, and report relays are allowed; sends from other sessions and automations are refused.
   - Per-turn approval changes are capped at the snapshot's ceiling.
   - Every LADO execution gets an explicit permission mode.
5. **LADO mode defaults.** Tracker intake and automations stay off for LADO projects: the gate rejects their spawns, and LADO mode cannot be turned on while the project's intake is on. Issues come in through import instead ([0006](0006-issues-as-work-items.md)). The orchestrator only writes plans; its prompt says so and the gate enforces it.
6. **Codex modes.** The global Codex `default` keeps upstream's mapping, so sessions outside LADO projects are unchanged. LADO executions get an explicit Codex mode from the snapshot. Amended 2026-10-19: the first version mapped `default` to `workspace-write` with network on and the common git directory writable, but Codex keeps `.git` read-only in that sandbox whatever the writable roots, so commits fail there (TDD §6.6).
7. **Commit and path rules.** These are checked over `base..head` in VALIDATING. Git hooks are advisory only.

## Consequences

- **Why a status check.** A solo developer cannot use GitHub's "required approvals": the PR author cannot approve their own PR. The `lado/integration` status gives the same effect.
- **Agents still push.** Branch pushes and PR creation keep working. Only merging into the base needs the approval.
- **Cost.** LADO makes GitHub API calls for rulesets and statuses with the host token from the repository connection ([0006](0006-issues-as-work-items.md)), which AO's own GitHub calls then use too.
- **GitLab later.** AO reads GitLab `approvals_required` today (`adapters/scm/gitlab/observer_provider.go:739,762-779`). LADO's GitLab support would use protected branches and external status checks. Until then, GitLab LADO projects get issues and tasks but are "unprotected: detection only" (TDD §7.1).
- **Upstream PR candidates.** Codex `accept-edits` and `auto` cannot commit (TDD §6.6, §12), and an optional spawn/send policy hook in `Manager` with a nil default.

## Amended 2026-10-27 (step 8)

Built as decided, with these changes (TDD §7, §8; owner's decisions of 2026-10-27 in TDD §15, item 8):

- **Item 1: who posts the status.** LADO reads the branch rules and posts `lado/integration` through its own repository client on the github.com token chain, `AO_GITHUB_TOKEN` when set, else the stored host token, else `gh auth token` (`adapters/tracker/github/lado_integration.go`, reached as `ports.LadoRepoClient`), not through AO's SCM adapter, whose GitHub provider double-encodes a branch name with `/`. The status is `success` with the description "Approved in LADO", posted right after the approval and retried by the verification loop at most every 5 minutes, up to 12 attempts in all. LADO never merges; GitHub's own merge works once the status is there.
- **Item 1: unprotected repositories.** "Each integration needs an explicit acknowledgement" holds per approval, not once per project: a base branch whose rules lack a pull request, `lado/integration` among the required checks, the force push block or the deletion block is the confirm code `unprotected`, an unreadable one `protection_unknown`, and a GitLab one `unprotected_gitlab`. The approval must name exactly the confirm codes of the head's record, no fewer and no more (400 `LADO_INTEGRATION_ACK_MISMATCH` otherwise), and the desktop confirmation says "This repository is not protected: LADO only detects a merge without approval." The rules are read fresh before each approval and its dry run, when LADO mode is turned on and on Check again, and kept 10 minutes otherwise.
- **Item 2: where the merge gate sits.** `service/pr/action_service.go` `Merge` asks the gate (`SetMergeGate(ports.LadoMergeGate)`, nil keeps upstream) right after it checks the stored head and before it reads the PR from the host. A PR is LADO's when its session is an execution or when a LADO verification or integration approval names its URL, so a PR claimed into another session stays gated (S7 of #15). Such a PR merges only with an approval of that PR at that head, of an execution it belongs to, into the branch it merges into now: no approval is 409 `LADO_INTEGRATION_APPROVAL_REQUIRED`, and an approval into another branch, as after the PR's base was changed on the host, is 409 `LADO_INTEGRATION_TARGET_CHANGED` (S5). A PR of any other session of a project in LADO mode is always refused with `LADO_INTEGRATION_APPROVAL_REQUIRED`, because only executions get approvals. A read that fails is 500 `LADO_MERGE_GATE_FAILED`. The desktop app's Merge, the mobile app and any other caller of the route meet the same gate.
- **Item 3: detection is a confirmation.** A local base branch move after dispatch that the remote base does not have, a reflog that is off, or work that reached the base without LADO is a confirm code the approval acknowledges (`base_moved`, `reflog_off`, `already_on_base`), not a block of the execution. The approval and its dry run fetch the base first; the loop never fetches.
- **Item 7: what blocks.** A failing commit message validator and a changed protected path that the approved plan does not name fail the head, and the worker gets one fix request; a changed path outside the plan's `affected_paths` and an unclear commit message are confirm codes. The validators run over merge-base..head in the verification loop, so a commit made with `--no-verify` is judged like any other.
- **Not built yet:** GitLab's protected branches with external status checks, and creating rulesets from the app.
- **Checked against GitHub (2026-10-28, #15).** On the fork, a temporary ruleset on a temporary base branch held a PR at `BLOCKED` until LADO's approval posted `lado/integration`, which made it `CLEAN` (not merged). A ruleset that takes the check from one app only kept the PR blocked after LADO's status, which is why LADO reads it as unprotected (TDD §7.1, §15 item 8). Everything made for the check was deleted.
