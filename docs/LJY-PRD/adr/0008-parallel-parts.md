# LADO 8. One task runs as parallel parts

Date: 2026-10-29
Status: Accepted. Decided by the owner in issue #32 (plan revisions 2 and 3, approved 2026-10-29). Extends the dispatcher of TDD §5.3 (step 6) and the run of a task under step 8's verification; a plan without parts runs as before.

## Context

Until #32 one worker ran a whole approved plan, step after step, in one session. Large plans, such as step 8 (#15), took a worker the better part of a day, while most of their steps touched different files and could have run side by side. Several tasks already ran at once (Settings → Runs, cap 4), but a task never did.

The owner's goals for #32, from the revision 2 change request:

- A plan is written for parallel work: parts A, B and C at once, D after them.
- Several tasks run at once, each with its own workers: task 1 with A, B, C and D, task 2 with E and F.
- Every worker starts as a new session.
- Earlier, in the chain runner days: work that touches the same files waits, other work runs in parallel.

Facts at `97e9a7050` (#15 and #27 merged):

- AO starts a worker on an existing local branch when the spawn names it (`adapters/workspace/gitworktree/workspace.go`), and one branch cannot be checked out in two worktrees.
- Workers report through `ao report`; `done` is the last report of a finished worker (`domain/report.go`).
- The worker gate grants a dispatch spawn by the client request id `lado:<entry>/<attempt>` and finds the execution's ceiling and rules by it (TDD §6).
- `lado_executions` held one execution per queue entry (`queue_entry_id` UNIQUE), and step 8's verification tables reference it.
- git 2.38 and later merges two commits without a worktree: `git merge-tree --write-tree` (exit 1 on a conflict) and `git commit-tree`.

## Decision

- **Parts in the plan.** The front matter takes an optional `parts` list (id, title, steps, affected_paths, depends_on); LADO checks it like the rest and a bad list cannot be approved. Steps no part takes are the integration steps. The plan request and the plan worker are told to plan for parallel parts by default, with how to split them. A large plan without parts gets a warning that does not block.
- **Every worker is a new session.** Each part, each retry, each merge and the integration run in fresh AO sessions with their own client request ids (`lado:<entry>/<part>/<n>`, `…/merge-<part>/<n>`, `…/integration/<n>`). LADO never resumes a session or gives it another part. Every one is an execution of the task's queue entry, so the gate, the permission ceiling, the rule snapshot, the local setup, the run time limit, the sweep and recovery all apply. `lado_executions` is rebuilt to allow many executions per entry, keeping one pull request worker per entry.
- **One integration branch, merged without a checkout.** LADO makes `lado/<task>/<entry>/integration` at the approved base and each part's branch at its tip when the part starts. When a part's worker reports done, LADO stops its session and merges its branch with `git merge-tree` and `git commit-tree` (`--no-ff`, a Korean conventional subject), then moves the integration branch with a compare-and-set `update-ref`. It records the merge it is about to make, so a restart never applies it twice. LADO writes only under `refs/heads/lado/`; the base branch and every other branch stay as they are.
- **Conflicts go to a merge worker.** A conflicting merge leaves the integration branch alone and starts a fresh merge worker on its own branch at the integration tip; LADO fast-forwards to it once it reports done and its branch holds both sides.
- **The integration worker reviews and finishes.** Once every part is merged, a fresh integration worker starts on the integration branch. It first reviews the parts against each other for contradictions (the same thing done twice or differently, names, APIs or constants that disagree, duplicate helpers, documents that disagree) and fixes them, or asks the developer when a fix needs a decision the plan does not make; then it does the integration steps, runs every check, pushes and opens the PR. That PR is the task's, and step 8's verification and integration approval take it from there.
- **Scheduling.** A part starts once the parts it depends on are merged and no active part of the task may touch the same files. A part worker that ends without done is retried once; then the task fails and names the part.
- **Caps count tasks and workers apart.** The global and project caps count tasks (a task of any number of parts counts once); a new worker cap (default 6, 1–12) counts LADO worker sessions. Plan workers keep their own cap.
- **Overlap and disk.** In a project with the overlap wait on (default), a task waits while another task's live run may touch the same files; files that merge easily (locale JSON, generated files, documents) are in a per-project ignore list. New runs and parts wait while the free disk space is under a threshold (default 10 GB).

TDD §5.4 has the details.

## Alternatives

- **A PR per part.** Each part would go through verification and integration approval on its own, and a dependent part would wait for the previous PR to merge into the base branch, which needs the owner. One PR keeps one approval per task. Kept as an open question for the owner.
- **Merging in a LADO worktree.** The plan's first design: a detached worktree under `~/.ao` where LADO checks out the integration tip and runs `git merge`. `merge-tree` does the same merge in the object store, without a second checkout of a large repository on a short disk, and without running the repository's hooks in a daemon process.
- **Resuming a part's session for a retry.** A retry after a session ended carries whatever confused the first attempt; a fresh session with the same prompt does not, and matches the owner's rule.
- **LADO builds and tests between parts.** It would need each project's build to run outside any agent sandbox in the daemon after every merge. The part, merge and integration workers run them instead; a project check after each merge is left for later.
- **Separate tables for part sessions.** Keeping `lado_executions` one-per-entry and recording part sessions elsewhere would have left the gate, the deadline, the sweep and recovery to be rebuilt for them.

## Consequences

- A split plan runs in the time of its longest chain of parts instead of all its steps, and costs one session per part attempt plus an integration session, and a merge session per conflict.
- The overlap rule is generous on purpose: tasks or parts that might touch the same files wait even when they would not have conflicted.
- A part's worker sees only its own part's work and what was merged before it started; contradictions between parts surface at the integration worker, which is told to look for them.
- The caps change meaning: an existing `maxConcurrent` of 4 now allows four tasks, which may run up to the worker cap's sessions.
- The base branch check of step 8 reads the integration execution's base facts, which come from the parts run's dispatch.
