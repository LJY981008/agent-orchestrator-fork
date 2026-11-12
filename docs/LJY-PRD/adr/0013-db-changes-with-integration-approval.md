# LADO 13. The integration approval applies a PR's production database SQL, and LADO then merges it

Date: 2026-11-11
Status: Accepted. Decided in issue #97, plan revision 1, approved 2026-11-11. Design in TDD §7.6. Amends [adr/0002](0002-human-presence-approval-channel.md) and [adr/0003](0003-enforcement-points.md): LADO merges the PRs of a database change task.

## Context

Some projects keep SQL files that a person applies to the production database by hand (`scripts/migrations/00N_*.sql`). A LADO task's PR could add such a file, but the integration approval and the merge only covered the code. The orchestrator asked "apply it to production?" after the push, and when the developer missed the question, CI deployed the merged code against the old schema. Irreversible SQL (`DROP`) went the same way.

Issue #97 asks that the integration approval be the approval to apply the SQL and merge, so that approving does both and code never ships without its SQL.

Facts checked before deciding:

- The integration approval signs the sha256 of the head's verification record (TDD §7.1), and an optional item with `omitempty` details keeps old records' hashes (step 8 precedent).
- AO's merge route (`service/pr/action_service.go` `Merge`) asks LADO's merge gate first, then reads the host and refuses on `mergeBlockers`. GitHub reports `mergeable_state` `blocked` while a required check is missing, as `lado/integration` is before LADO posts it.
- The status retries (`ListLadoIntegrationStatusRetries`) list pending and failed approvals; the approvals table has CHECK constraints a new state would have to change.
- The check command runner (`ladoverify.RunCommand`, `CommandEnv`) already runs a shell command outside any agent sandbox with AO's variables and the tokens taken out.
- Secret files have precedents with 0700/0600 and atomic replace (`ladoconnect/credentials.go`, `ladologwatch/certs.go`).

## Decision

- **Per-repository settings, off by default.** A project, or each child repository of a workspace, can turn on "production database changes": 1–10 path globs (no `..`), a one-line command of at most 2000 characters with `{file}`, a time limit of 1–120 minutes (10 by default) and up to 10 secret names. A secret name matches `^[A-Z_][A-Z0-9_]{0,63}$` and is not a variable LADO protects (`domain.LadoDBChangeReservedEnv`: AO's and the token variables `ladoverify.CommandEnv` withholds, git's credential and SSH variables, `PATH`, `HOME` and the like). A project without settings behaves exactly as before.
- **Secrets are files, shown by name only.** Values live in `<StateDir>/lado/credentials/db/<16 hex>.json` (0700 folder, 0600 file, atomic replace) and reach the command as environment variables. The API, the audit log, the stored outputs and the logs carry names only; command output and error lines are redacted (each value of 4 or more characters, DSN and URL passwords) and cut to their last 64 KiB.
- **A baseline when the settings turn on.** Turning them on, or changing the paths, fetches the default branch (`refs/remotes/origin/HEAD`, else the configured base branch) and records its matching files as `baseline`; LADO never applies them. A fetch that fails refuses the save (`LADO_DB_CHANGE_BASELINE_FAILED`).
- **The approval signs the SQL.** A head that adds matching files gets the optional `db_changes` item after the eight items, with each file's path, blob and phase. The approval signs the record, so it signs the blobs; LADO applies the approved blob (`git cat-file`), never the working file. Changing an applied file is a confirm (`db_change_applied_modified`), so is SQL the plan did not announce with `db_schema` (`db_change_unplanned`); a missing secret fails (`db_change_secrets_missing`).
- **LADO merges database change tasks.** For an approved head with pre-merge SQL, LADO holds `lado/integration`, checks that the merge could go through, applies the SQL in path order, then posts the status and merges through AO's merge route, which runs the same gate and blockers as the Merge button. Every other task merges as before: LADO only posts the status. A merge is the only way to keep "approve" and "apply and merge" one act.
- **A preflight before any SQL.** The live head must be the approved head; the merge gate's approval, base (#53) and merge order (#64) must hold (`PreflightMerge`); and the PR's merge blockers, read without merging (`LadoMergeReadiness`), must hold none of `merged`, `closed`, `draft`, `ci_failing`, `changes_requested`, `unresolved_comments` and a conflicting `not_mergeable`, and the host's review decision must not be `review_required` (a required approval is missing, which GitHub reports only as a blocked merge state) (`domain.LadoDBChangeMergeStoppers`). What LADO's missing status explains (`ci_pending`, `ci_unknown`, `ci_no_pipeline`, `head_mismatch`, `review_partial`, another `not_mergeable`) passes. A blocked run applies nothing and waits for the developer, with an orchestrator notice.
- **The status hold.** An approved head whose pre-merge SQL is not applied gets no `lado/integration`: GitHub's ruleset then blocks it, and GitLab keeps LADO's `pending`. The hold is a row in a new table (`lado_db_change_status_holds`), which the retries skip; the approvals table is not changed. AO's merge route refuses such a head last with 409 `LADO_DB_CHANGE_PENDING`.
- **Failure stops the merge.** The first file that fails stops the run as `failed`; LADO merges nothing and tells the orchestrator. A retry needs presence and skips the files that applied. A daemon that stopped while a command ran marks that file `interrupted` and the run `failed`: whether the SQL is in the database is the developer's to check.
- **A refused merge after the status.** LADO reads the PR again: merged already means done; nothing but what the status explains means it tries again for 15 minutes; anything else is `merge_failed` with a notice. The applied SQL stays applied, and the Merge button or a retry finishes the merge.
- **After-deploy SQL waits for a button.** A file whose first non-empty line is `-- lado:after-deploy` is not applied before the merge. Once the PR merged, LADO makes a run that waits for the developer, who applies it from the app with presence; LADO cannot tell when a deploy finished.
- **A merge outside LADO is seen.** The verification loop tells the service of every merged PR before it settles it. Pre-merge SQL left unapplied makes a `merged_unapplied` run, a red card and a notice; the app applies it with presence. An error leaves the PR unsettled until the next pass, and after an hour of errors LADO says it cannot tell and lets the PR settle.
- **Notices, not alerts.** The orchestrator hears through `AddLadoOrchestratorNotice` (keys `db_*`); `lado_alerts` is not used, since its CHECK would have to change.
- **Instructions.** In a project with settings on, the orchestrator does not ask about or apply production SQL, workers add SQL as new files under the configured paths without applying them or editing applied ones, and plan workers list the files and `db_schema`.

## Consequences

- "LADO never merges" ([adr/0002](0002-human-presence-approval-channel.md), [adr/0003](0003-enforcement-points.md), PRD MERGE-001, TDD §7.1 and §7.2) gets one exception: an approved head with pre-merge SQL, merged by LADO through AO's merge route after the SQL applied.
- The command runs with the developer's database credentials outside any agent sandbox. Changing the settings or the secrets, and applying by hand, need the desktop app's presence (`project.db_changes`, `db_change.apply`).
- Between the preflight and the merge the PR can change (a new review comment); the run then ends `merge_failed` with the SQL applied, and the database is ahead of the code until the merge goes through.
- Not built, left to follow-up issues: reviewing or rewriting SQL, database backup and rollback, an automatic time for after-deploy SQL, and marking a file applied by hand.
- Not checked: the `{file}` quoting and `psql` on a real Windows install.
