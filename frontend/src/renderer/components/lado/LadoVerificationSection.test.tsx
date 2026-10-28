import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import * as lado from "../../lib/lado";
import { LadoVerificationSection } from "./LadoVerificationSection";

vi.mock("../../lib/lado", async (original) => ({
	...await original<typeof import("../../lib/lado")>(),
	fetchLadoPlan: vi.fn(),
	fetchLadoCheckLog: vi.fn(),
	runLadoChecks: vi.fn(),
	approveLadoIntegration: vi.fn(),
	mergeLadoPR: vi.fn(),
	repostLadoIntegrationStatus: vi.fn(),
}));

const HEAD = "1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b";
const PR_URL = "https://github.com/acme/demo/pull/45";
const pr: lado.LadoExecutionPR = { url: PR_URL, number: 45, merged: false, closed: false, headSha: HEAD, targetBranch: "main", title: "로그인 추가" };

type Item = lado.LadoVerifyItem;

// One item per kind, in the daemon's order; the head is ready to integrate.
function items(overrides: Partial<Record<lado.LadoVerifyKind, Partial<Item>>> = {}): Item[] {
	const base: Item[] = [
		{
			kind: "required_checks",
			outcome: "pass",
			details: {
				required: ["unit", "lint", "e2e"],
				rulesetRead: true,
				checks: [
					{ name: "unit", status: "passed", required: true },
					{ name: "lint", status: "passed", required: true },
					{ name: "e2e", status: "skipped", required: true },
					{ name: "docs", status: "failed", required: false },
				],
			},
		},
		{
			kind: "commands",
			outcome: "pass",
			details: {
				runs: [
					{ name: "unit", checkRunId: "run-1", status: "passed", exitCode: 0, durationMs: 38_000 },
					{ name: "lint", checkRunId: "run-2", status: "passed", exitCode: 0, durationMs: 250_000 },
				],
			},
		},
		{ kind: "commit_messages", outcome: "info", code: "not_required", details: {} },
		{ kind: "paths", outcome: "confirm", code: "outside_plan", details: { protected: [], outsidePlan: ["docs/login.md", "README.md"], outsideCount: 2 } },
		{ kind: "base_branch", outcome: "pass", details: { branch: "main", remote: "origin", moved: [] } },
		{ kind: "protection", outcome: "confirm", code: "unprotected", details: { provider: "github", repo: "acme/demo", branch: "main", missing: ["required_status_checks:lado/integration", "deletion"] } },
		{ kind: "review", outcome: "pass", details: { rounds: 1, verdict: "approved", runId: "review-1", harness: "claude-code" } },
		{ kind: "evidence", outcome: "info", details: { fileVersion: 2, fileSha256: "f".repeat(64), fileHead: HEAD } },
	];
	return base.map((item) => ({ ...item, ...overrides[item.kind] }));
}

function verification(fields: Partial<lado.LadoVerification> = {}): lado.LadoVerification {
	return {
		headSha: HEAD,
		prUrl: PR_URL,
		outcome: "confirm",
		sha256: "9".repeat(64),
		items: items(),
		evidence: [
			{ index: 1, state: "linked", refs: [{ kind: "pr_check", key: "unit", sha: HEAD }, { kind: "lado_check_run", key: "run-2", sha: HEAD }] },
			{ index: 2, state: "claimed", refs: [{ kind: "claim", key: "command: smoke", sha: "" }] },
			{ index: 3, state: "inferred", refs: [] },
		],
		computedAt: "2026-10-27T09:00:00Z",
		...fields,
	};
}

/**
 * The state the daemon derives from a run's facts (backend/internal/domain
 * lado_task.go taskState) and sends with it, for fixtures that name none.
 */
function daemonState(run: lado.LadoExecution): Pick<lado.LadoExecution, "state" | "stateReason"> {
	const { pr, verification, integration } = run;
	if (!pr) return { state: "running" };
	if (pr.merged) return { state: "completed", stateReason: integration ? undefined : "merged_without_approval" };
	if (pr.closed) return { state: "closed_unmerged" };
	if (integration) return { state: "integrating", stateReason: integration.statusState === "failed" ? "status_not_posted" : undefined };
	if (!verification) return { state: "validating", stateReason: "verifying" };
	if (verification.outcome === "pass" || verification.outcome === "confirm") return { state: "integration_pending" };
	return { state: "validating", stateReason: verification.reason || "verifying" };
}

function execution(fields: Partial<lado.LadoExecution> = {}): lado.LadoExecution {
	const run = executionFacts(fields);
	return { ...run, ...daemonState(run), ...(fields.state ? { state: fields.state, stateReason: fields.stateReason } : {}) };
}

function executionFacts(fields: Partial<lado.LadoExecution>): lado.LadoExecution {
	return {
		state: "running",
		sessionId: "demo-7",
		attempt: 1,
		task: { id: "lado-task-1", title: "Add login", issueRef: "github:acme/demo#12", issueUrl: "https://github.com/acme/demo/issues/12" },
		plan: { revisionId: "lado-plan-3", number: 3, sha256: "a".repeat(64), approvedAt: "2026-10-27T07:00:00Z" },
		snapshot: {
			sha256: "1a2b3c4d".padEnd(64, "0"),
			agent: "claude-code",
			permissionMode: "accept-edits",
			permissionCeiling: "auto",
			rules: [],
			prompt: "",
			setup: [],
		},
		base: { planSha: "c".repeat(40), commitsAhead: 0 },
		startedAt: "2026-10-27T08:00:00Z",
		sessionEnded: true,
		pr,
		verification: verification(),
		...fields,
	};
}

const approval: lado.LadoIntegrationApproval = {
	id: "approval-1",
	headSha: HEAD,
	targetBranch: "main",
	verificationSha256: "9".repeat(64),
	acknowledged: ["outside_plan", "unprotected"],
	actor: "dev",
	approvedAt: "2026-10-27T09:30:00Z",
	statusState: "posted",
	statusAttempts: 1,
	statusPostedAt: "2026-10-27T09:30:01Z",
};

function renderSection(target: lado.LadoExecution = execution()) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<LadoVerificationSection execution={target} projectId="demo" />
		</QueryClientProvider>,
	);
}

function rows(): HTMLElement[] {
	return within(screen.getByTestId("lado-verify-items")).getAllByTestId("lado-verify-item");
}

function row(kind: lado.LadoVerifyKind): HTMLElement {
	const found = rows().find((candidate) => candidate.dataset.kind === kind);
	if (!found) throw new Error(`no ${kind} row`);
	return found;
}

function dot(element: HTMLElement): Element | null {
	return element.querySelector("span.rounded-full");
}

beforeEach(() => {
	vi.resetAllMocks();
});

afterEach(cleanup);

test("lists the eight items with a dot each, and the ones that do not apply quieter", () => {
	renderSection(
		execution({
			verification: verification({
				outcome: "fail",
				reason: "commands_failed",
				items: items({
					required_checks: {
						outcome: "wait",
						code: "checks_pending",
						details: {
							required: ["unit", "e2e"],
							checks: [
								{ name: "unit", status: "passed", required: true },
								{ name: "e2e", status: "in_progress", required: true },
							],
						},
					},
					commands: {
						outcome: "fail",
						code: "commands_failed",
						details: {
							runs: [
								{ name: "unit", checkRunId: "run-1", status: "passed", exitCode: 0, durationMs: 38_000 },
								{ name: "lint", checkRunId: "run-2", status: "failed", exitCode: 1, durationMs: 4_000 },
							],
						},
					},
				}),
			}),
		}),
	);

	expect(screen.getByRole("button", { name: "Verification" })).toBeInTheDocument();
	// Where the integration stands, and the head it is about in mono.
	const state = screen.getByTestId("lado-verify-state");
	expect(state).toHaveTextContent("Validating · A check command failed");
	// Validating has a color of its own (DESIGN.md §5, U8 of #15).
	expect(dot(state)).toHaveClass("bg-[var(--color-status-validating)]");
	expect(screen.getByText("1a2b3c4")).toHaveClass("font-mono");
	expect(screen.getByTitle(HEAD)).toHaveTextContent("Head 1a2b3c4");

	expect(rows().map((item) => item.dataset.kind)).toEqual([
		"required_checks",
		"commands",
		"commit_messages",
		"paths",
		"base_branch",
		"protection",
		"review",
		"evidence",
	]);
	const expected: [lado.LadoVerifyKind, string, string, string][] = [
		["required_checks", "Required checks", "Waiting for e2e", "bg-[var(--color-status-validating)]"],
		["commands", "Check commands", "Failed: lint", "bg-[var(--color-status-exited)]"],
		["commit_messages", "Commit messages", "Not required for this run", "bg-[var(--color-status-idle)]"],
		["paths", "Changed paths", "2 paths outside the plan: docs/login.md, README.md", "bg-[var(--color-status-needs-you)]"],
		["base_branch", "Base branch", "No local writes to main", "bg-[var(--color-status-ready)]"],
		// The parts by name, as the project's protection card names them (U1 of #15).
		["protection", "Remote protection", "Not protected: rulesets missing lado/integration required, Deletion blocked", "bg-[var(--color-status-needs-you)]"],
		["review", "Review", "The review approved this head", "bg-[var(--color-status-ready)]"],
		["evidence", "Evidence", "1 of 3 criteria linked to records", "bg-[var(--color-status-idle)]"],
	];
	for (const [kind, name, detail, dotClass] of expected) {
		const item = row(kind);
		expect(within(item).getByText(name)).toBeInTheDocument();
		expect(within(item).getByText(detail)).toBeInTheDocument();
		expect(dot(item)).toHaveClass(dotClass);
	}
	// Each row names its outcome in words too, not by its dot alone (U5 of #15).
	expect(row("commands")).toHaveTextContent("Check commands, failed");
	expect(row("paths")).toHaveTextContent("Changed paths, needs your confirmation");
	// An item that does not apply keeps its place, quieter than the rest.
	expect(within(row("commit_messages")).getByText("Commit messages")).toHaveClass("text-passive");
	expect(within(row("commit_messages")).getByText("Not required for this run")).toHaveClass("text-passive");
	expect(within(row("paths")).getByText("Changed paths")).toHaveClass("text-foreground");
	expect(within(row("paths")).getByText(/paths outside the plan/)).toHaveClass("text-muted-foreground");
	// The command's state, time and exit code.
	const commands = within(row("commands")).getAllByTestId("lado-verify-command");
	expect(commands.map((command) => command.textContent)).toEqual(["unit·Passed·38sOutput", "lint·Exited with 1·4sOutput"]);
	// No decision while the head fails.
	expect(screen.queryByRole("button", { name: "Approve integration…" })).not.toBeInTheDocument();
	expect(screen.queryByRole("button", { name: "Merge" })).not.toBeInTheDocument();
});

test("words passes, waits and confirms from the item's details", () => {
	renderSection(
		execution({
			verification: verification({
				items: items({
					required_checks: { outcome: "pass", code: "none_reported", details: {} },
					commands: { outcome: "confirm", code: "no_tests", details: { runs: [] } },
					commit_messages: {
						outcome: "confirm",
						code: "commit_messages_unclear",
						details: {
							commits: [
								{ sha: "b".repeat(40), subject: "feat: 로그인 추가", result: "ok" },
								{ sha: "c".repeat(40), subject: "wip", result: "unclear" },
							],
							prTitle: { text: "로그인 추가", result: "ok" },
						},
					},
					paths: { outcome: "confirm", code: "outside_plan", details: { outsidePlan: ["a", "b", "c", "d"], outsideCount: 60 } },
					base_branch: { outcome: "confirm", code: "base_moved", details: { branch: "main", remote: "origin", moved: [{ selector: "main@{0}", commit: "d".repeat(40), subject: "oops" }] } },
					protection: { outcome: "confirm", code: "protection_unknown", details: { error: "403 Forbidden" } },
					review: { outcome: "wait", code: "review_pending", details: { rounds: 2, note: "unknown reviewer gpt; AO chose" } },
					evidence: { outcome: "info", details: {} },
				}),
			}),
		}),
	);

	expect(within(row("required_checks")).getByText("No checks reported on this head")).toBeInTheDocument();
	expect(within(row("commands")).getByText("Nothing tests this head: no check commands and no required checks")).toBeInTheDocument();
	expect(within(row("commit_messages")).getByText("LADO cannot tell the language of 1 message")).toBeInTheDocument();
	expect(within(row("paths")).getByText("60 paths outside the plan: a, b, c and 57 more")).toBeInTheDocument();
	expect(within(row("base_branch")).getByText("1 local change to main is not on origin")).toBeInTheDocument();
	expect(within(row("protection")).getByText("LADO could not read the branch rules: 403 Forbidden")).toBeInTheDocument();
	expect(within(row("review")).getByText("Waiting for the review · Round 2 of 3 · Unknown reviewer gpt, so AO chose one")).toBeInTheDocument();
	expect(within(row("evidence")).getByText("The worker wrote no evidence file")).toBeInTheDocument();
	// A command list only where commands ran.
	expect(within(row("commands")).queryByTestId("lado-verify-commands")).not.toBeInTheDocument();
	expect(screen.getByTestId("lado-verify-state")).toHaveTextContent(/^Integration pending$/);
});

test("opens a command's output in mono and runs the commands again", async () => {
	vi.mocked(lado.fetchLadoCheckLog).mockResolvedValue("ok 12 tests\nPASS");
	let started: (result: lado.LadoRunChecksResult) => void = () => undefined;
	vi.mocked(lado.runLadoChecks).mockReturnValue(new Promise((resolve) => (started = resolve)));
	renderSection();

	const commands = row("commands");
	const toggle = within(commands).getByRole("button", { name: "Output of lint" });
	expect(toggle).toHaveAttribute("aria-expanded", "false");
	expect(lado.fetchLadoCheckLog).not.toHaveBeenCalled();
	fireEvent.click(toggle);
	expect(toggle).toHaveAttribute("aria-expanded", "true");
	const log = await within(commands).findByTestId("lado-verify-log");
	expect(lado.fetchLadoCheckLog).toHaveBeenCalledWith("demo-7", "run-2");
	expect(log.textContent).toBe("ok 12 tests\nPASS");
	expect(log).toHaveClass("font-mono", "max-h-48", "overflow-auto");

	const again = within(commands).getByRole("button", { name: "Run again" });
	fireEvent.click(again);
	await waitFor(() => expect(lado.runLadoChecks).toHaveBeenCalledWith("demo-7"));
	// Busy while the daemon starts them.
	expect(await within(commands).findByRole("button", { name: "Starting…" })).toBeDisabled();
	started({ headSha: HEAD, commands: ["unit", "lint"] });
	expect(await within(commands).findByRole("button", { name: "Run again" })).toBeEnabled();
});

test("a command run without output says so", async () => {
	vi.mocked(lado.fetchLadoCheckLog).mockResolvedValue("");
	renderSection();

	fireEvent.click(within(row("commands")).getByRole("button", { name: "Output of unit" }));
	expect(await within(row("commands")).findByText("No output")).toBeInTheDocument();
});

test("folds the evidence, then names each criterion with its state and what backs it", async () => {
	vi.mocked(lado.fetchLadoPlan).mockResolvedValue({
		plan: { acceptanceCriteria: ["A user can sign in", "A wrong password is refused", "The session survives a reload"], affectedPaths: ["backend/**"], steps: [], testCommands: [], reviewRequired: false, body: "" },
	} as unknown as lado.LadoPlan);
	renderSection();

	const evidence = row("evidence");
	const toggle = within(evidence).getByRole("button", { name: "Evidence by criterion" });
	expect(toggle).toHaveAttribute("aria-expanded", "false");
	expect(within(evidence).queryByTestId("lado-verify-evidence")).not.toBeInTheDocument();
	// The plan's words load only once the list opens.
	expect(lado.fetchLadoPlan).not.toHaveBeenCalled();
	fireEvent.click(toggle);

	await waitFor(() => expect(lado.fetchLadoPlan).toHaveBeenCalledWith("lado-plan-3"));
	const criteria = within(evidence).getAllByTestId("lado-verify-criterion");
	await waitFor(() => expect(criteria[0]).toHaveTextContent("1. A user can sign in"));
	expect(criteria.map((criterion) => criterion.dataset.state)).toEqual(["linked", "claimed", "inferred"]);
	// A command's check run reads as the command's name; names are mono.
	expect(criteria[0]).toHaveTextContent("Linked · CI check unit · Check command lint");
	expect(within(criteria[0]).getByText("lint")).toHaveClass("font-mono");
	expect(criteria[1]).toHaveTextContent("2. A wrong password is refused");
	expect(criteria[1]).toHaveTextContent("Claimed only · Claim command: smoke");
	expect(criteria[2]).toHaveTextContent("3. The session survives a reload");
	expect(criteria[2]).toHaveTextContent(/Inferred$/);
});

test("evidence about another head reads as stale", () => {
	renderSection(
		execution({
			verification: verification({
				items: items({ evidence: { outcome: "info", details: { fileVersion: 1, fileHead: "e".repeat(40) } } }),
				evidence: [{ index: 1, state: "stale", refs: [] }],
			}),
		}),
	);

	expect(within(row("evidence")).getByText("The evidence file describes another head")).toBeInTheDocument();
});

test("offers the integration approval only while it is pending; a cancel says nothing, a refusal says why", async () => {
	let answer: (result: lado.LadoIntegrationResult | null) => void = () => undefined;
	vi.mocked(lado.approveLadoIntegration).mockReturnValueOnce(new Promise((resolve) => (answer = resolve)));
	renderSection();

	expect(screen.getByTestId("lado-verify-state")).toHaveTextContent("Integration pending");
	expect(dot(screen.getByTestId("lado-verify-state"))).toHaveClass("bg-[var(--color-status-needs-you)]");
	expect(screen.queryByRole("button", { name: "Merge" })).not.toBeInTheDocument();
	fireEvent.click(screen.getByRole("button", { name: "Approve integration…" }));
	await waitFor(() => expect(lado.approveLadoIntegration).toHaveBeenCalledWith("demo-7"));
	// Busy while the desktop app asks.
	expect(await screen.findByRole("button", { name: "Approving…" })).toBeDisabled();
	// The developer cancelled the desktop confirmation.
	answer(null);
	const approve = await screen.findByRole("button", { name: "Approve integration…" });
	expect(approve).toBeEnabled();
	expect(screen.queryByRole("alert")).not.toBeInTheDocument();

	vi.mocked(lado.approveLadoIntegration).mockRejectedValueOnce(
		new lado.LadoApiError("LADO's verification changed", "LADO_VERIFICATION_CHANGED", {}),
	);
	fireEvent.click(approve);
	expect(await screen.findByRole("alert")).toHaveTextContent("LADO's verification of this head changed after you saw it. Check it again, then approve.");
	cleanup();

	// A head LADO still verifies, and a closed PR, offer no decision.
	for (const target of [
		execution({ verification: verification({ outcome: "wait", reason: "checks_pending" }) }),
		execution({ pr: { ...pr, closed: true } }),
	]) {
		renderSection(target);
		expect(screen.queryByRole("button", { name: "Approve integration…" })).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Merge" })).not.toBeInTheDocument();
		cleanup();
	}
});

test("merges the approved head and shows the daemon's refusal under the section", async () => {
	vi.mocked(lado.mergeLadoPR).mockRejectedValueOnce(
		new lado.LadoApiError("Approve the integration first", "LADO_INTEGRATION_APPROVAL_REQUIRED", { prUrl: PR_URL, headSha: HEAD, sessionId: "demo-7" }),
	);
	renderSection(execution({ integration: approval }));

	const state = screen.getByTestId("lado-verify-state");
	expect(state).toHaveTextContent(/^Integrating$/);
	expect(dot(state)).toHaveClass("bg-[var(--color-status-ready)]");
	expect(screen.queryByRole("button", { name: "Approve integration…" })).not.toBeInTheDocument();
	fireEvent.click(screen.getByRole("button", { name: "Merge" }));

	await waitFor(() => expect(lado.mergeLadoPR).toHaveBeenCalledWith({ number: 45, url: PR_URL, headSha: HEAD }));
	const error = await screen.findByRole("alert");
	expect(error).toHaveTextContent("Approve the integration of this head in LADO before you merge.");
	// Below the rows, at the section's end.
	expect(screen.getByTestId("lado-verify-items").compareDocumentPosition(error) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
});

test("an approved head whose status LADO could not post says so with the host's error", () => {
	renderSection(execution({ integration: { ...approval, statusState: "failed", statusError: "403 Resource not accessible by integration" } }));

	expect(screen.getByTestId("lado-verify-state")).toHaveTextContent("Integrating · LADO could not post the integration status yet");
	expect(screen.getByText("403 Resource not accessible by integration")).toHaveClass("truncate");
	expect(screen.getByRole("button", { name: "Merge" })).toBeEnabled();
});

test("says it checks the head while it has no record of it", () => {
	renderSection(execution({ verification: undefined }));

	expect(screen.getByRole("status")).toHaveTextContent(/^Checking the head…$/);
	expect(screen.getByTitle(HEAD)).toHaveTextContent("Head 1a2b3c4");
	expect(screen.queryByTestId("lado-verify-items")).not.toBeInTheDocument();
	expect(screen.queryByRole("button", { name: "Approve integration…" })).not.toBeInTheDocument();
});

test("a merged PR reads as completed, and as needing attention without an approval", () => {
	renderSection(execution({ pr: { ...pr, merged: true }, integration: approval }));
	expect(screen.getByTestId("lado-verify-state")).toHaveTextContent(/^Completed$/);
	expect(dot(screen.getByTestId("lado-verify-state"))).toHaveClass("bg-[var(--color-status-merged)]");
	expect(within(row("commands")).queryByRole("button", { name: "Run again" })).not.toBeInTheDocument();
	cleanup();

	renderSection(execution({ pr: { ...pr, merged: true } }));
	expect(screen.getByTestId("lado-verify-state")).toHaveTextContent("Completed · Merged without an integration approval");
	expect(dot(screen.getByTestId("lado-verify-state"))).toHaveClass("bg-[var(--color-status-needs-you)]");
});

test("a run without a PR has no verification section", () => {
	const { container } = renderSection(execution({ pr: undefined, verification: undefined }));
	expect(container).toBeEmptyDOMElement();
});

// Review fixes of #15.

test("shows the state and reason the daemon sends, not one of its own", () => {
	// A run stopped at its deadline while its PR opened shows its PR (V9);
	// the daemon says so, and the panel does not work it out again.
	renderSection(
		execution({
			timedOutAt: "2026-10-27T09:30:00Z",
			state: "validating",
			stateReason: "head_unavailable",
			verification: verification({ outcome: "wait", reason: "head_unavailable" }),
		}),
	);
	expect(screen.getByTestId("lado-verify-state")).toHaveTextContent(
		"Validating · The PR head is not in the local repository, and LADO could not fetch it",
	);
	cleanup();

	// A state the daemon gives a run without a PR has no section here.
	const { container } = renderSection(execution({ state: "failed", stateReason: "timed_out" }));
	expect(container).toBeEmptyDOMElement();
});

test("offers a command's output only once it ended, and reads it again for each state", async () => {
	vi.mocked(lado.fetchLadoCheckLog).mockResolvedValue("ok");
	const running = verification({
		items: items({
			commands: {
				outcome: "wait",
				code: "commands_running",
				details: { runs: [{ name: "unit", checkRunId: "lado-check-1", status: "running", exitCode: null, durationMs: 0 }] },
			},
		}),
	});
	renderSection(execution({ verification: running }));
	expect(within(row("commands")).queryByRole("button", { name: "Output of unit" })).not.toBeInTheDocument();
	cleanup();

	renderSection();
	fireEvent.click(within(row("commands")).getAllByRole("button", { name: /^Output of / })[0]!);
	await waitFor(() => expect(lado.fetchLadoCheckLog).toHaveBeenCalled());
});

test("posts a status that could not be posted again", async () => {
	vi.mocked(lado.repostLadoIntegrationStatus).mockResolvedValue({ ...approval, statusState: "posted", statusAttempts: 1 });
	renderSection(execution({ integration: { ...approval, statusState: "failed", statusAttempts: 12, statusError: "403 Resource not accessible" } }));

	fireEvent.click(screen.getByRole("button", { name: "Post status again" }));
	await waitFor(() => expect(lado.repostLadoIntegrationStatus).toHaveBeenCalledWith("demo-7"));
	cleanup();

	// A head whose status went up has nothing to post again.
	renderSection(execution({ integration: approval }));
	expect(screen.queryByRole("button", { name: "Post status again" })).not.toBeInTheDocument();
});

test("keeps Merge off once the merge went through, until the PR shows it", async () => {
	vi.mocked(lado.mergeLadoPR).mockResolvedValue(undefined);
	renderSection(execution({ integration: approval }));

	fireEvent.click(screen.getByRole("button", { name: "Merge" }));
	const sent = await screen.findByRole("button", { name: "Merged; waiting for the PR to update" });
	expect(sent).toBeDisabled();
	expect(lado.mergeLadoPR).toHaveBeenCalledTimes(1);
});

test("names a deleted local base branch and a check taken from one app only", () => {
	renderSection(
		execution({
			verification: verification({
				items: items({
					base_branch: {
						outcome: "confirm",
						code: "base_deleted",
						details: { branch: "main", remote: "origin", moved: [], alreadyOnBase: false, dispatchCommit: "c".repeat(40), deleted: true },
					},
					protection: {
						outcome: "confirm",
						code: "unprotected",
						details: { provider: "github", repo: "acme/demo", branch: "main", missing: ["required_status_checks:lado/integration:any_source"] },
					},
				}),
			}),
		}),
	);
	expect(within(row("base_branch")).getByText("The local main that was there when the run started is gone")).toBeInTheDocument();
	expect(within(row("protection")).getByText("Not protected: rulesets missing lado/integration from any source")).toBeInTheDocument();
	expect(within(row("protection")).getByText(/set the check's source to “Any source”/)).toBeInTheDocument();
});
