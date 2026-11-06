# ao pr

Act on a pull request (GitHub) or merge request (GitLab) that AO tracks. The
AO daemon talks to the provider with the credentials AO already holds, so these
commands need no `glab`, no `gh` and no token in the worker.

## Syntax

```text
ao pr resolve-comments <pr-number> [thread-or-comment-id...]
ao pr merge <pr-number>
```

`<pr-number>` is the PR or MR number (`42` or `#42`).

---

### ao pr resolve-comments

Resolve review threads on a pull request or merge request. The daemon resolves
the threads on GitHub and GitLab alike and marks them resolved in AO's local
state, so the review-comment check and merge readiness see the change at once.

**Syntax:**
```text
ao pr resolve-comments <pr-number> [thread-or-comment-id...]
```

Each id is a review thread id (the `Thread ID` AO shows with a forwarded review
comment) or, for a comment without one, the comment id.

Rules:

- Address the comment and push first, then resolve the thread.
- Name only the threads you addressed. Without ids the command resolves every
  unresolved thread on the PR, including the ones you did not address.
- Leave unaddressed threads open: do not resolve a thread you decided not to
  change, and report why with `ao report --checkpoint --note "<thread id>: <why>"`.
- An unresolved review thread keeps the PR from being ready to merge.

AO finds the number across every project and repository it tracks. When more
than one open PR/MR has the same number, the command fails with "tracked in
multiple active repositories". If the command fails, for that or any other
reason, do not try another tool (`glab`, `gh`, the provider's API): run
`ao report --needs-input --note "<PR URL> <thread id...>: <the error>"` and let
the developer resolve the threads.

**Example:**

```bash
# Resolve the two threads this push addressed on PR #42
ao pr resolve-comments 42 PRRT_kwDOabc123 PRRT_kwDOdef456
```

---

### ao pr merge

Merge a pull request through the daemon, which re-reads the provider first and
merges only the head it saw.

**Syntax:**
```text
ao pr merge <pr-number>
```

Merge only when the user asks for it. A worker of a LADO project never merges
its pull request and never asks anyone to merge it: the developer approves the
integration in LADO.

**Example:**

```bash
ao pr merge 42
```
