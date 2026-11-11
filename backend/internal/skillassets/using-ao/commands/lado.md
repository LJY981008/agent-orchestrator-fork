# ao lado

LADO commands for a project in LADO mode. This page covers the two an
orchestrator uses with the developer: `ao lado limits` and `ao lado relay`.
Run `ao lado --help` for the others (`plan`, `knowledge`, `timings`).

## Syntax

```text
ao lado limits [--json]
ao lado limits release <account-key> --reason <text> [--json]
ao lado relay --task <task id or issue number> --message '<instruction>' --evidence-file - [--session <id>] [--outside-plan] [--json]
```

---

### ao lado limits

List the account usage limits LADO recorded, held or released.

While an account is at its usage limit, LADO holds every new worker on that
account until the reset time: plan workers and queued runs wait as
`account_limited`. LADO releases a limit by itself at its reset, when a worker
stopped at the limit completes its next turn, and when it sees the account
logged in to another login (it records only a hash of the login, never the
email). The table shows each account key (such as `claude:device-claude`, the
computer's own Claude login), whether the limit is `active`, `released` or
`expired`, its reset time, and why and when it was released.

### ao lado limits release

Release an active limit by hand, such as after the developer logged in to
another account and LADO did not notice.

```text
ao lado limits release claude:device-claude --reason "the developer logged in to another account"
```

- `<account-key>` is a key from `ao lado limits`.
- `--reason` is required, 1 to 500 characters; LADO keeps it in its audit log.
  Without it, or without the key, the command exits 2.
- LADO checks the `account_limited` waits again, so they start on the
  dispatcher's next pass, and resumes the workers the limit stopped within a
  minute.
- An unknown key is `LADO_ACCOUNT_LIMIT_NOT_FOUND`; a limit already released or
  past its reset is `LADO_ACCOUNT_LIMIT_NOT_ACTIVE`.

Never edit LADO's database to release a limit.

---

### ao lado relay

Have LADO relay the developer's instruction to a running worker of a task, as
a LADO project's orchestrator. `ao send` to a LADO worker is refused; this is
the only way to instruct one.

```text
ao lado relay --task 49 --message '<the instruction>' --evidence-file - <<'LADO_EVIDENCE'
<the developer sentence, exactly as typed>
LADO_EVIDENCE
```

- The evidence is the developer's sentence to you, word for word, from the
  last 24 hours. Keep the heredoc delimiter quoted so the shell changes
  nothing in it.
- `--task` is the task id or the issue number (`49`, or `'#49'` quoted).
- `--session` picks the worker when several of the task's workers run.
- `--outside-plan` marks an instruction that adds a step or a path the
  approved plan does not have.
- `AO_SESSION_ID` names you. When LADO refuses, tell the developer its reason
  as it gave it.
