import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { appCatalogs } from "../../i18n/all-messages";
import * as lado from "../../lib/lado";
import * as dbChanges from "../../lib/lado-db-changes";
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

// The run's production SQL (issue #97).
vi.mock("../../lib/lado-db-changes", async (original) => ({
	...await original<typeof import("../../lib/lado-db-changes")>(),
	fetchLadoExecutionDBChanges: vi.fn(),
	fetchLadoDBChangeFile: vi.fn(),
	applyLadoDBChanges: vi.fn(),
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
	cleanup();

	// Nor does a merged PR, whose status no longer matters (lab check of #15).
	renderSection(execution({ pr: { ...pr, merged: true }, integration: { ...approval, statusState: "failed", statusAttempts: 12, statusError: "403 Resource not accessible" } }));
	expect(screen.getByTestId("lado-verify-state")).toHaveTextContent("Completed");
	expect(screen.queryByText("403 Resource not accessible")).not.toBeInTheDocument();
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

// #21: a GitLab branch lacks protected-branch and project settings, not rulesets.
test("names what a GitLab base branch lacks in GitLab's words", () => {
	renderSection(
		execution({
			verification: verification({
				items: items({
					protection: {
						outcome: "confirm",
						code: "unprotected",
						details: { provider: "gitlab", repo: "group/app", branch: "main", missing: ["push_access", "force_push", "integration_status"] },
					},
				}),
			}),
		}),
	);
	expect(
		within(row("protection")).getByText(
			"Not protected: GitLab settings missing Pushes allowed to no one, Force pushes blocked, LADO's lado/integration status",
		),
	).toBeInTheDocument();
});

// #21: AO's merge route says why it refused.
test("says a merge was refused because the head has no CI pipeline, or names the blockers", async () => {
	vi.mocked(lado.mergeLadoPR).mockRejectedValueOnce(
		new lado.LadoApiError("PR merge preconditions are not met", "PR_PRECONDITIONS_UNMET", { blockers: ["ci_no_pipeline"] }),
	);
	renderSection(execution({ integration: approval }));
	fireEvent.click(screen.getByRole("button", { name: "Merge" }));
	expect(await screen.findByRole("alert")).toHaveTextContent(
		"This commit has no CI pipeline and no integration approval in LADO, so it cannot be merged.",
	);
	cleanup();

	vi.mocked(lado.mergeLadoPR).mockRejectedValueOnce(
		new lado.LadoApiError("PR merge preconditions are not met", "PR_PRECONDITIONS_UNMET", { blockers: ["ci_no_pipeline", "not_mergeable"] }),
	);
	renderSection(execution({ integration: approval }));
	fireEvent.click(screen.getByRole("button", { name: "Merge" }));
	expect(await screen.findByRole("alert")).toHaveTextContent(
		"The pull request cannot be merged yet: this commit has no CI pipeline; the host is blocking the merge.",
	);
});

// #59: the PR is open in another repository than the task's issue.
test("names a PR repository outside the workspace, and a base that is not its default branch", () => {
	const { unmount } = renderSection(
		execution({
			verification: verification({
				outcome: "fail",
				reason: "pr_repo_not_in_workspace",
				items: items({
					base_branch: {
						outcome: "fail",
						code: "pr_repo_not_in_workspace",
						details: { branch: "main", remote: "", moved: [], alreadyOnBase: false, prRepo: "github:acme/other" },
					},
				}),
			}),
		}),
	);
	const outside = row("base_branch");
	expect(within(outside).getByText("The PR is open in github:acme/other, which is none of this workspace's repositories")).toBeInTheDocument();
	expect(outside).toHaveAttribute("data-outcome", "fail");
	expect(screen.getByTestId("lado-verify-state")).toHaveTextContent("The PR is open in a repository outside this workspace");
	unmount();

	renderSection(
		execution({
			verification: verification({
				items: items({
					base_branch: {
						outcome: "confirm",
						code: "base_not_default",
						details: { branch: "develop", remote: "origin", moved: [], alreadyOnBase: false, defaultBranch: "main", dispatchBranch: "master" },
					},
				}),
			}),
		}),
	);
	expect(
		within(row("base_branch")).getByText("The PR targets develop and the run started from master, but the default branch of the PR's repository is main"),
	).toBeInTheDocument();
});

test("names a remote base that moved past the head, and the files it overlaps", () => {
	const { unmount } = renderSection(
		execution({
			verification: verification({
				items: items({
					base_branch: {
						outcome: "confirm",
						code: "base_advanced",
						details: { branch: "main", remote: "origin", moved: [], alreadyOnBase: false, advanced: true, advancedTip: "e".repeat(40) },
					},
				}),
			}),
		}),
	);
	expect(within(row("base_branch")).getByText("The remote main moved without touching this head's files")).toBeInTheDocument();
	expect(within(row("base_branch")).queryByTestId("lado-verify-overlap")).not.toBeInTheDocument();
	unmount();

	renderSection(
		execution({
			verification: verification({
				outcome: "fail",
				reason: "base_overlap",
				items: items({
					base_branch: {
						outcome: "fail",
						code: "base_overlap",
						details: {
							branch: "main",
							remote: "origin",
							moved: [],
							alreadyOnBase: false,
							advanced: true,
							advancedTip: "e".repeat(40),
							overlap: ["frontend/src/a.ts", "backend/b.go"],
							overlapCount: 5,
						},
					},
				}),
			}),
		}),
	);
	const base = row("base_branch");
	expect(within(base).getByText("5 files changed on the remote main overlap this head")).toBeInTheDocument();
	const files = within(base).getByTestId("lado-verify-overlap");
	expect(within(files).getByText("frontend/src/a.ts")).toHaveClass("font-mono");
	expect(within(files).getByText("backend/b.go")).toBeInTheDocument();
	expect(within(files).getByText("and 3 more files")).toBeInTheDocument();
	expect(base).toHaveAttribute("data-outcome", "fail");
	expect(screen.getByTestId("lado-verify-state")).toHaveTextContent("Base moved");
});

// The worktree is at the head with uncommitted or untracked files (#22).
test("names the uncommitted files the check commands wait for", () => {
	const dirty = (details: Item["details"]) =>
		renderSection(
			execution({
				verification: verification({
					outcome: "wait",
					reason: "worktree_dirty",
					items: items({ commands: { outcome: "wait", code: "worktree_dirty", details } }),
				}),
			}),
		);
	const { unmount } = dirty({ dirty: [".husky/_/post-checkout", "notes with space <- old.txt"], dirtyCount: 7 });
	const commands = row("commands");
	expect(commands).toHaveAttribute("data-outcome", "wait");
	expect(
		within(commands).getByText("Checks wait for 7 uncommitted files: commit or delete them, or add them to .gitignore, and they start"),
	).toBeInTheDocument();
	const files = within(commands).getByTestId("lado-verify-dirty");
	expect(within(files).getByText(".husky/_/post-checkout")).toHaveClass("font-mono");
	expect(within(files).getByText("notes with space <- old.txt")).toBeInTheDocument();
	expect(within(files).getByText("and 5 more files")).toBeInTheDocument();
	unmount();

	// A daemon that could not list the files sends only the code.
	dirty({});
	expect(within(row("commands")).getByText("Waiting for the worktree's uncommitted files to be cleaned up")).toBeInTheDocument();
	expect(within(row("commands")).queryByTestId("lado-verify-dirty")).not.toBeInTheDocument();
});

// The PR's review threads that are still unresolved (#85): the execution
// view counts them next to the head's record.
test("counts the unresolved review threads above the items, and hides the line without any", () => {
	renderSection(execution({ verification: verification({ unresolvedReviewThreads: 2 }) }));
	const threads = screen.getByTestId("lado-verify-review-threads");
	expect(threads).toHaveTextContent("2 unresolved review threads");
	// Above the items, in the section.
	expect(threads.compareDocumentPosition(screen.getByTestId("lado-verify-items")) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
	cleanup();

	renderSection(execution({ verification: verification({ unresolvedReviewThreads: 1 }) }));
	expect(screen.getByTestId("lado-verify-review-threads")).toHaveTextContent("1 unresolved review thread");
	cleanup();

	renderSection(execution({ verification: verification({ unresolvedReviewThreads: 0 }) }));
	expect(screen.queryByTestId("lado-verify-review-threads")).not.toBeInTheDocument();
	cleanup();

	// A daemon that sends none leaves the field out.
	renderSection(execution());
	expect(screen.getByTestId("lado-verify-items")).toBeInTheDocument();
	expect(screen.queryByTestId("lado-verify-review-threads")).not.toBeInTheDocument();
});

test("every locale words the unresolved review threads with its plural forms", () => {
	for (const [locale, catalog] of Object.entries(appCatalogs)) {
		const messages = catalog as Record<string, string>;
		const forms = ["es", "fr", "pt-BR"].includes(locale) ? ["one", "many", "other"] : ["one", "other"];
		for (const form of forms) {
			expect(messages[`lado.verify.reviewThreads_${form}`], `${locale} ${form}`).toContain("{{count}}");
		}
	}
	expect((appCatalogs.ko as Record<string, string>)["lado.verify.reviewThreads_other"]).toBe("미해결 리뷰 스레드 {{count}}개");
});

// A workspace task with a pull request in each of two repositories (#64).
const BACK_URL = "https://github.com/acme/back/pull/3";
const FRONT_URL = "https://github.com/acme/front/pull/5";
const BACK_HEAD = "b".repeat(40);
const FRONT_HEAD = "c".repeat(40);

function trackedPR(fields: Partial<lado.LadoExecutionTrackedPR> & Pick<lado.LadoExecutionTrackedPR, "url" | "repo" | "position" | "number">): lado.LadoExecutionTrackedPR {
	return {
		closed: false,
		merged: false,
		settled: false,
		state: "integration_pending",
		targetBranch: "main",
		integration: { headApproved: false },
		...fields,
	};
}

function twoPRs(back: Partial<lado.LadoExecutionTrackedPR> = {}, front: Partial<lado.LadoExecutionTrackedPR> = {}, fields: Partial<lado.LadoExecution> = {}) {
	const backPR = trackedPR({ url: BACK_URL, repo: "back", position: 0, number: 3, headSha: BACK_HEAD, record: verification({ headSha: BACK_HEAD, prUrl: BACK_URL }), ...back });
	const frontPR = trackedPR({
		url: FRONT_URL,
		repo: "front",
		position: 1,
		number: 5,
		headSha: FRONT_HEAD,
		record: verification({ headSha: FRONT_HEAD, prUrl: FRONT_URL, items: items({ paths: { outcome: "pass", code: undefined, details: { protected: [] } } }) }),
		...front,
	});
	return execution({
		pr: { url: BACK_URL, number: 3, merged: backPR.merged, closed: false, headSha: BACK_HEAD, targetBranch: "main" },
		verification: backPR.record,
		// The daemon lists them in merge order; the app does not rely on it.
		trackedPrs: [frontPR, backPR],
		prRepos: ["back", "front"],
		state: "integration_pending",
		...fields,
	});
}

test("shows each tracked PR with its repository, state and own verification, and one approval for the task", async () => {
	vi.mocked(lado.approveLadoIntegration).mockResolvedValueOnce(null);
	renderSection(twoPRs());

	expect(screen.getByTestId("lado-verify-state")).toHaveTextContent("Integration pending");
	const prs = screen.getAllByTestId("lado-verify-pr");
	expect(prs.map((element) => element.dataset.repo)).toEqual(["back", "front"]);
	expect(prs[0]).toHaveTextContent("backPR #3");
	expect(within(prs[0]!).getByText(BACK_HEAD.slice(0, 7))).toBeInTheDocument();
	expect(within(prs[1]!).getByText(FRONT_HEAD.slice(0, 7))).toBeInTheDocument();
	// Each PR's own record: front changed nothing outside the plan.
	const outcomes = prs.map((element) =>
		within(element).getAllByTestId("lado-verify-item").find((item) => item.dataset.kind === "paths")?.dataset.outcome,
	);
	expect(outcomes).toEqual(["confirm", "pass"]);
	// The check commands run again for the first open PR only.
	expect(within(prs[0]!).getAllByRole("button", { name: "Run again" })).toHaveLength(1);
	expect(within(prs[1]!).queryByRole("button", { name: "Run again" })).not.toBeInTheDocument();

	expect(screen.getAllByRole("button", { name: /Approve/ })).toHaveLength(1);
	fireEvent.click(screen.getByRole("button", { name: "Approve task integration…" }));
	await waitFor(() => expect(lado.approveLadoIntegration).toHaveBeenCalledWith("demo-7"));
});

test("offers the merge of the next PR in the plan's order only, and names a withdrawn approval", () => {
	const approvedBack = { state: "integrating" as const, integration: { headApproved: true, statusState: "posted" as const }, approval: { ...approval, headSha: BACK_HEAD } };
	const approvedFront = { state: "integrating" as const, integration: { headApproved: true, statusState: "posted" as const }, approval: { ...approval, headSha: FRONT_HEAD } };
	renderSection(twoPRs(approvedBack, approvedFront, { state: "integrating" }));
	let prs = screen.getAllByTestId("lado-verify-pr");
	expect(within(prs[0]!).getByRole("button", { name: "Merge" })).toBeInTheDocument();
	expect(within(prs[1]!).queryByRole("button", { name: "Merge" })).not.toBeInTheDocument();
	expect(screen.queryByRole("button", { name: /Approve/ })).not.toBeInTheDocument();
	cleanup();

	// back merged: front merges next.
	renderSection(twoPRs({ ...approvedBack, merged: true, state: "completed" }, approvedFront, { state: "integrating" }));
	prs = screen.getAllByTestId("lado-verify-pr");
	expect(within(prs[0]!).queryByRole("button", { name: "Merge" })).not.toBeInTheDocument();
	expect(within(prs[1]!).getByRole("button", { name: "Merge" })).toBeInTheDocument();
	cleanup();

	// back's base moved: LADO withdrew front's approval with it.
	renderSection(
		twoPRs(
			{ state: "validating", stateReason: "base_overlap", integration: { headApproved: false, revokedReason: "base_overlap" } },
			{ integration: { headApproved: false, revokedReason: "sibling_revoked" } },
			{ state: "validating", stateReason: "base_overlap" },
		),
	);
	prs = screen.getAllByTestId("lado-verify-pr");
	expect(within(prs[1]!).getByTestId("lado-verify-revoked")).toHaveTextContent(
		"LADO withdrew this head's approval because the base of another pull request of the task moved. Approve the task again.",
	);
	expect(within(prs[0]!).getByTestId("lado-verify-revoked")).toHaveTextContent("its base moved with a change to the same paths");
	expect(within(prs[0]!).getByText("Base moved")).toBeInTheDocument();
});

test("reposts the status of the PR whose post failed, and names a repository still without a PR", async () => {
	vi.mocked(lado.repostLadoIntegrationStatus).mockResolvedValue({ ...approval, statusState: "posted" });
	renderSection(
		twoPRs(
			{},
			{
				state: "integrating",
				integration: { headApproved: true, statusState: "failed" },
				approval: { ...approval, headSha: FRONT_HEAD, statusState: "failed", statusError: "token expired" },
			},
		),
	);
	const front = screen.getAllByTestId("lado-verify-pr")[1]!;
	expect(within(front).getByText("token expired")).toBeInTheDocument();
	fireEvent.click(within(front).getByTestId("lado-repost-status"));
	await waitFor(() => expect(lado.repostLadoIntegrationStatus).toHaveBeenCalledWith("demo-7", FRONT_URL));
	cleanup();

	// One PR so far, and the task waits for front's.
	renderSection(
		execution({
			trackedPrs: [trackedPR({ url: PR_URL, repo: "back", position: 0, number: 45, headSha: HEAD, state: "integration_pending" })],
			prRepos: ["back", "front"],
			state: "validating",
			stateReason: "pr_missing",
		}),
	);
	expect(screen.queryByTestId("lado-verify-prs")).not.toBeInTheDocument();
	expect(screen.getByTestId("lado-verify-state")).toHaveTextContent("Waiting for a pull request in each repository");
	expect(screen.getByTestId("lado-verify-missing")).toHaveTextContent("No pull request yet in: front");
});

test("counts each tracked PR's unresolved review threads under that PR", () => {
	renderSection(
		twoPRs(
			{ record: verification({ headSha: BACK_HEAD, prUrl: BACK_URL, unresolvedReviewThreads: 3 }) },
			{ record: verification({ headSha: FRONT_HEAD, prUrl: FRONT_URL }) },
		),
	);
	const [back, front] = screen.getAllByTestId("lado-verify-pr");
	expect(within(back!).getByTestId("lado-verify-review-threads")).toHaveTextContent("3 unresolved review threads");
	expect(within(front!).queryByTestId("lado-verify-review-threads")).not.toBeInTheDocument();
	cleanup();

	renderSection(
		twoPRs(
			{ record: verification({ headSha: BACK_HEAD, prUrl: BACK_URL, unresolvedReviewThreads: 0 }) },
			{ record: verification({ headSha: FRONT_HEAD, prUrl: FRONT_URL, unresolvedReviewThreads: 1 }) },
		),
	);
	const prs = screen.getAllByTestId("lado-verify-pr");
	expect(within(prs[0]!).queryByTestId("lado-verify-review-threads")).not.toBeInTheDocument();
	expect(within(prs[1]!).getByTestId("lado-verify-review-threads")).toHaveTextContent("1 unresolved review thread");
});

// LADO fork: the production SQL a head adds (issue #97). The db_changes item
// follows evidence only in a repository whose settings are on; the panel
// under the items shows the run's SQL files from the daemon, never the
// record, and its buttons ask the desktop app to apply a phase.
const dbItem = (fields: Partial<Item> = {}): Item => ({
	kind: "db_changes",
	outcome: "pass",
	details: {
		files: [
			{ path: "scripts/migrations/001_add.sql", blobSha: "b1", phase: "pre_merge" },
			{ path: "scripts/migrations/002_drop.sql", blobSha: "b2", phase: "after_deploy" },
		],
		planned: true,
	},
	...fields,
});

function dbRun(fields: Partial<dbChanges.LadoDBChangeRun>): dbChanges.LadoDBChangeRun {
	return {
		id: "run-1",
		kind: "pre_merge",
		trigger: "approval",
		state: "failed",
		attempts: 1,
		createdAt: "2026-11-10T00:00:00Z",
		updatedAt: "2026-11-10T00:00:00Z",
		headSha: HEAD,
		prUrl: PR_URL,
		repo: "",
		sessionId: "demo-7",
		taskId: "lado-task-1",
		...fields,
	};
}

function dbView(fields: Partial<dbChanges.LadoDBChangePR> = {}): dbChanges.LadoDBChangeExecution {
	return {
		sessionId: "demo-7",
		taskId: "lado-task-1",
		prs: [
			{
				prUrl: PR_URL,
				headSha: HEAD,
				repo: "",
				files: [
					{ path: "scripts/migrations/001_add.sql", blobSha: "b1", phase: "pre_merge", status: "failed", attempts: 1, lastMessage: "ERROR: relation exists [REDACTED]" },
					{ path: "scripts/migrations/002_drop.sql", blobSha: "b2", phase: "after_deploy", status: "pending", attempts: 0 },
				],
				modified: [],
				runs: [dbRun({ error: "psql exited 3" })],
				...fields,
			},
		],
	};
}

const withDB = (item: Item = dbItem()) => execution({ verification: verification({ items: [...items(), item] }) });

test("names the db_changes item after evidence, counts its files and shows the run's SQL with a retry", async () => {
	vi.mocked(dbChanges.fetchLadoExecutionDBChanges).mockResolvedValue(dbView());
	vi.mocked(dbChanges.applyLadoDBChanges).mockResolvedValue(dbRun({ state: "queued" }));
	renderSection(withDB());

	expect(rows().map((candidate) => candidate.dataset.kind).slice(-2)).toEqual(["evidence", "db_changes"]);
	expect(row("db_changes")).toHaveTextContent("Database changes");
	expect(row("db_changes")).toHaveTextContent("2 SQL files, 1 after deploy");

	const panel = await screen.findByTestId("lado-db-change-pr");
	expect(dbChanges.fetchLadoExecutionDBChanges).toHaveBeenCalledWith("demo-7");
	const files = within(panel).getAllByTestId("lado-db-change-file");
	expect(files.map((file) => [file.dataset.status, file.dataset.phase])).toEqual([
		["failed", "pre_merge"],
		["pending", "after_deploy"],
	]);
	expect(files[0]).toHaveTextContent("scripts/migrations/001_add.sql · before merge · failed");
	expect(within(files[0]).getByTestId("lado-db-change-message")).toHaveTextContent("ERROR: relation exists [REDACTED]");
	expect(within(panel).getByTestId("lado-db-change-run")).toHaveTextContent("Applying the SQL failed; LADO did not merge");
	expect(within(panel).getByTestId("lado-db-change-run-error")).toHaveTextContent("psql exited 3");

	fireEvent.click(within(panel).getByRole("button", { name: "Try again" }));
	await waitFor(() => expect(dbChanges.applyLadoDBChanges).toHaveBeenCalledTimes(1));
	expect(vi.mocked(dbChanges.applyLadoDBChanges).mock.calls[0]?.[0]).toBe("demo-7");
	expect(vi.mocked(dbChanges.applyLadoDBChanges).mock.calls[0]?.[1]).toMatchObject({ prUrl: PR_URL, headSha: HEAD });
	expect(vi.mocked(dbChanges.applyLadoDBChanges).mock.calls[0]?.[2]).toBe("pre_merge");
});

test("offers the after-deploy SQL and the SQL a merge outside LADO left, and names what the plan and head say", async () => {
	vi.mocked(dbChanges.fetchLadoExecutionDBChanges).mockResolvedValue(
		dbView({
			files: [
				{ path: "scripts/migrations/001_add.sql", blobSha: "b1", phase: "pre_merge", status: "pending", attempts: 0 },
				{ path: "scripts/migrations/002_drop.sql", blobSha: "b2", phase: "after_deploy", status: "interrupted", attempts: 1 },
			],
			modified: [{ path: "scripts/migrations/000_init.sql", appliedBlobSha: "z0", headBlobSha: "", deleted: true }],
			runs: [
				dbRun({ id: "r-1", trigger: "merged_unapplied", state: "awaiting_human" }),
				dbRun({ id: "r-2", kind: "after_deploy", trigger: "after_merge", state: "awaiting_human" }),
			],
		}),
	);
	vi.mocked(dbChanges.applyLadoDBChanges).mockResolvedValue(null);
	renderSection(withDB(dbItem({ outcome: "confirm", code: "db_change_unplanned", details: { ...dbItem().details, planned: false } })));

	expect(row("db_changes")).toHaveTextContent("2 SQL files, but the plan lists no database change");
	const panel = await screen.findByTestId("lado-db-change-pr");
	expect(within(panel).getByTestId("lado-db-change-unplanned")).toHaveTextContent("The plan does not list a database change (db_schema).");
	expect(within(panel).getByTestId("lado-db-change-modified")).toHaveTextContent("000_init.sql · deleted");
	expect(within(panel).getAllByTestId("lado-db-change-run").map((entry) => entry.textContent)).toEqual([
		"Merged outside LADO without its SQL: apply it now",
		"After-deploy SQL waits: apply it once the code is deployed",
	]);
	expect(panel).toHaveTextContent("Check the database before you apply it again.");

	fireEvent.click(within(panel).getByRole("button", { name: "Apply after-deploy SQL" }));
	await waitFor(() => expect(dbChanges.applyLadoDBChanges).toHaveBeenCalledTimes(1));
	expect(vi.mocked(dbChanges.applyLadoDBChanges).mock.calls[0]?.[2]).toBe("after_deploy");
	fireEvent.click(within(panel).getByRole("button", { name: "Apply now (merged outside LADO)" }));
	await waitFor(() => expect(dbChanges.applyLadoDBChanges).toHaveBeenCalledTimes(2));
	expect(vi.mocked(dbChanges.applyLadoDBChanges).mock.calls[1]?.[2]).toBe("pre_merge");
});

test("shows a SQL file's approved content on request, and a run that LADO holds without a button", async () => {
	vi.mocked(dbChanges.fetchLadoExecutionDBChanges).mockResolvedValue(dbView({ runs: [dbRun({ state: "queued" })] }));
	vi.mocked(dbChanges.fetchLadoDBChangeFile).mockResolvedValue({ path: "scripts/migrations/001_add.sql", blobSha: "b1", content: "ALTER TABLE users ADD COLUMN age int;", truncated: true });
	renderSection(withDB());

	const panel = await screen.findByTestId("lado-db-change-pr");
	expect(within(panel).queryByRole("button", { name: "Try again" })).toBeNull();
	fireEvent.click(within(panel).getByRole("button", { name: "Show scripts/migrations/001_add.sql" }));
	expect(await within(panel).findByTestId("lado-db-change-content")).toHaveTextContent("ALTER TABLE users ADD COLUMN age int;");
	expect(dbChanges.fetchLadoDBChangeFile).toHaveBeenCalledWith("demo-7", PR_URL, "b1");
	expect(panel).toHaveTextContent("Showing the first 256 KiB.");
});

test("words the db_changes codes, and never reads the database changes of a head without SQL", async () => {
	renderSection(withDB(dbItem({ outcome: "fail", code: "db_change_secrets_missing" })));
	expect(row("db_changes")).toHaveTextContent("A secret has no stored value, so LADO cannot apply the SQL");
	cleanup();

	renderSection(withDB(dbItem({ outcome: "confirm", code: "db_change_applied_modified", details: { files: [], modified: [{ path: "a.sql", appliedBlobSha: "x", headBlobSha: "y", deleted: false }] } })));
	expect(row("db_changes")).toHaveTextContent("Changes 1 applied SQL file: confirm before you approve");
	cleanup();
	vi.mocked(dbChanges.fetchLadoExecutionDBChanges).mockClear();

	renderSection(withDB(dbItem({ code: "no_db_changes", details: {} })));
	expect(row("db_changes")).toHaveTextContent("No SQL files");
	expect(screen.queryByTestId("lado-db-change-panel")).toBeNull();
	cleanup();

	renderSection();
	expect(rows()).toHaveLength(8);
	expect(screen.queryByTestId("lado-db-change-panel")).toBeNull();
	expect(dbChanges.fetchLadoExecutionDBChanges).not.toHaveBeenCalled();
});

test("every locale names the db_changes item and its codes", () => {
	for (const [locale, catalog] of Object.entries(appCatalogs)) {
		const messages = catalog as Record<string, string>;
		for (const key of ["lado.verify.item.dbChanges", "lado.verify.db.none", "lado.verify.db.secretsMissing", "lado.reason.db_change_secrets_missing"]) {
			expect(messages[key], `${locale} ${key}`).toBeTruthy();
		}
	}
	expect((appCatalogs.ko as Record<string, string>)["lado.verify.item.dbChanges"]).toBe("DB 변경");
});
