# LADO 2. Human-presence channel for approvals

Date: 2026-10-19
Status: Accepted (2026-10-19). The owner chose a proof key derived in memory from the desktop app's per-launch token, in place of the key file proposed in the first draft.

## Context

LADO approvals (plan revisions, integrations, rule and setup changes) must come from the human. An agent making a mistake (T1, [0001](0001-threat-model-t1-first.md)) must not be able to approve its own work.

Facts at `55b02b848`:

- The loopback listener must stay bound to `127.0.0.1` and unauthenticated (`AGENTS.md:108`).
- Any local process can call it: requests without `Origin` pass CORS (`httpd/cors.go:13-27`), and agents have `ao` on PATH (`agentlaunch/env.go:74-95`).
- Chat approval resolve records no actor and has no caller check (`httpd/controllers/conversations.go:912-939`, `service/chat/controller.go:2257-2268`).
- The packaged renderer has a distinct origin, `app://renderer`, that browsers cannot forge (`httpd/cors.go:13-27`). Any local process can still send that header.
- AO already uses a per-session capability token with a stored verifier for the browser bridge (`AO_BROWSER_CAPABILITY`, `session_manager/manager.go:235,6107-6121`; `httpd/controllers/browser.go:18`).
- **The desktop app and the daemon already share a memory-only secret.** Electron main mints 32 random bytes once per launch (`frontend/src/main.ts:1397`) and hands them to the daemon it spawns over an inherited stdin pipe; the daemon environment carries only a marker (`frontend/src/main.ts:1451-1452,2108-2113`; `daemon/daemon.go:199-213`). The token is never placed in a file, command line or environment, and the stdin handle is not passed to worker processes (`browserruntime/broker.go:41-43`). A daemon started without the app (`ao start`) generates its own random token (`daemon/daemon.go:206-211`).
- **The app replaces daemons from other launches.** A daemon accepts only the launch that handed it its token; a new launch replaces a daemon started by an earlier launch or by `ao start`, keeping its persistence mode (`frontend/src/shared/daemon-takeover.ts:44-57`). While the desktop app runs, it and the daemon always share one token, and the token changes with every launch.
- AO already runs local sockets: the supervisor watchdog (`daemon/supervisor/listen_unix.go`, `listen_windows.go`) and the browser bridge, a `0600` Unix socket or a named pipe with a protected owner-and-SYSTEM DACL (`browserruntime/listen_windows.go:29-32`).
- The Connect Mobile LAN listener serves the app API unless a route prefix is blocked (`httpd/lan_listener.go:72-85`).

## Decision

**Actions that need presence:** plan approval, integration approval, protected gate changes, setup changes that can run code or widen access, local setup adoption and restore, and turning LADO mode off for a project.

**Flow:**

1. The renderer asks Electron main over the preload bridge (`frontend/src/preload.ts`).
2. Main shows a native modal confirmation (`dialog.showMessageBox`) naming the exact subject: task title, plan revision and sha256 prefix, or PR and head SHA.
3. On confirm, main signs the request and sends it to the daemon itself over the loopback listener. The renderer never holds the key.

**Early refusals (amended 2026-10-28, #15).** Before it asks, main reads the subject from the daemon and refuses what the daemon would refuse anyway, so the person is never asked to confirm a request that cannot succeed. For an integration, reading the run already fails for a session that is not a run (`LADO_EXECUTION_NOT_FOUND`), and main refuses a run without an open PR (`LADO_EXECUTION_NO_PR`), a head not verified yet or whose dry run waits or fails (`LADO_INTEGRATION_NOT_READY`), and a head already approved. For check commands it applies the daemon's checks first, with the same details; for protected paths only the count, blank globs and control or direction characters, and the daemon checks the path rules after the confirmation. In a browser, without main, the renderer itself refuses a session that is not a run (`LADO_EXECUTION_NOT_FOUND`) and a run without an open PR (`LADO_EXECUTION_NO_PR`); everything else is the daemon's answer (`frontend/src/renderer/lib/lado.ts`).

**Integration confirmation (amended 2026-10-28, #15).** It names the task, the PR with its branch, the head, how many items passed, and one line for each confirm code; the lines for paths outside the plan, unclear commit messages, a moved base and an unprotected branch carry a count and at most three examples, and an unprotected branch names the parts its rules lack. It ends with what happens next: "On approval, LADO posts the lado/integration status on this head; LADO itself never merges." for a repository that is not on GitLab, and "This repository is not protected: LADO only detects a merge without approval." when the protection item is `unprotected` or `unprotected_gitlab`, so an unprotected GitHub branch shows both (`frontend/src/main/lado-presence.ts`).

**Proof:** header `X-LADO-Presence: v1.<nonce>.<expires>.<mac>`, where `mac = HMAC-SHA256(key, method ‖ path ‖ sha256(body) ‖ nonce ‖ expires)`. Expiry is at most 120 s. Each nonce is accepted once: it is stored with a unique constraint and referenced by the audit event, so a daemon restart does not reopen it. The body carries the subject hash, so a proof for one plan revision cannot approve another.

**Key:** `key = HMAC-SHA256(desktop token, "lado-presence-v1")`, computed in memory by Electron main and by the daemon from the token they already share. The derivation keeps the presence key separate from the bridge token itself.

- Nothing is written to disk, `running.json` (which the CLI and agents read through `AO_RUN_FILE`), the environment or the CLI.
- The key rotates with every app launch, because the launch replaces the daemon and mints a new token.
- A daemon whose token did not come from the app cannot verify any proof. Presence routes then answer "open the desktop app" and approve nothing. The same fail-closed answer applies if upstream changes the token handoff; a LADO test pins the dependency.
- Hook lines: `daemon/daemon.go` passes the token and its origin into LADO wiring; `frontend/src/main.ts` passes it to `frontend/src/main/lado-presence.ts`.

**Frequent approvals:** Chat tool-approval resolves in LADO execution sessions would be unusable behind a modal. For those, LADO requires the `app://renderer` origin, refuses requests without `Origin`, and records the actor. This stops the `ao` CLI and plain curl, which is enough for T1.

**Also:**

- `/api/v1/lado/` is blocked on the LAN listener. There is no mobile approval until mobile has a presence mechanism.
- The CLI gets no approval commands.
- `dev:web` (renderer in a browser, no Electron main) has no presence. Approvals there need `AO_LADO_DEV_PRESENCE=1` and are labelled dev-only.

**Reading of `AGENTS.md:108`:** the listener keeps its bind address and stays unauthenticated, and no middleware is added to it. Outside LADO projects no existing route changes behavior. The one existing route that changes is Chat approval resolve, and only for LADO execution sessions (the origin rule above). The proof is a precondition of new LADO operations, the way `expectedHeadSha` is a precondition of the merge route (`httpd/controllers/prs.go:45-47`); it is not authentication of the listener.

## Alternatives

- **Key file** `<state>/lado/presence.key` (first draft): any same-user process, an agent included, can read the file. It also needs creation, permissions and rotation. The memory-only key needs none of these.
- **Separate local socket for approvals**: AO's socket patterns above make the transport moderate work. But any same-user process can connect to the socket, so it would still need the same secret, and it adds a second transport and a contract outside OpenAPI.
- **Renderer origin for every approval**: no human presence at all, since any local process can send the header.
- **OS authentication (Windows Hello)**: stronger, but needs a native module. Possible later on top of this design.

## Consequences

- Approvals need the desktop app to be running.
- An agent's mistaken `ao` or curl call cannot approve anything, and neither can reading files under `~/.ao`.
- A T2 agent can still read the app's or daemon's memory, or drive the confirmation dialog, and forge approvals. That is accepted until OS isolation exists ([0001](0001-threat-model-t1-first.md)).
- LADO depends on upstream's desktop token handoff. If it changes, approvals fail closed until LADO adapts.
- Upstream PR candidate: record the actor on approval resolve, with an option to refuse resolves that have no `Origin`.
