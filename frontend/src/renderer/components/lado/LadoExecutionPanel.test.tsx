import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { appI18n } from "../../i18n";
import * as lado from "../../lib/lado";
import type { WorkspaceSession } from "../../types/workspace";
import { LadoExecutionPanel } from "./LadoExecutionPanel";

vi.mock("../../lib/lado", async (original) => ({
	...await original<typeof import("../../lib/lado")>(),
	fetchLadoExecution: vi.fn(),
}));

const session: WorkspaceSession = {
	id: "demo-7",
	workspaceId: "demo",
	workspaceName: "Demo",
	title: "Add login",
	provider: "claude-code",
	kind: "worker",
	status: "working",
	updatedAt: "2026-10-23T09:00:00Z",
	prs: [],
};

function execution(fields: Partial<lado.LadoExecution> = {}): lado.LadoExecution {
	return {
		sessionId: "demo-7",
		attempt: 1,
		task: { id: "lado-task-1", title: "Add login", issueRef: "github:acme/demo#12", issueUrl: "https://github.com/acme/demo/issues/12" },
		plan: { revisionId: "lado-plan-3", number: 3, sha256: "a".repeat(64), approvedAt: new Date(Date.now() - 2 * 3_600_000).toISOString() },
		snapshot: {
			sha256: "1a2b3c4d".padEnd(64, "0"),
			agent: "claude-code",
			model: "opus",
			permissionMode: "accept-edits",
			permissionCeiling: "auto",
			rules: [
				{ id: "plan.approval.required", name: "Plan approval required", version: 1 },
				{ id: "lado-rule-9", name: "No force push", version: 2 },
			],
			prompt: "Never force push.",
			setup: [],
		},
		base: { planSha: "c".repeat(40), dispatchSha: "d".repeat(40), commitsAhead: 2 },
		startedAt: new Date(Date.now() - 3_600_000).toISOString(),
		deadlineAt: new Date(Date.now() + 5 * 3_600_000).toISOString(),
		sessionEnded: false,
		...fields,
	};
}

function renderPanel(target: WorkspaceSession = session, hostId?: string) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<LadoExecutionPanel session={target} hostId={hostId} />
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	vi.resetAllMocks();
});

afterEach(async () => {
	cleanup();
	await act(() => appI18n.changeLanguage("en"));
});

/** The value beside a label in the panel's facts. */
function fact(panel: HTMLElement, label: string): HTMLElement {
	const term = within(panel).getByText(label, { selector: "dt" });
	return term.nextElementSibling as HTMLElement;
}

test("shows the snapshot the run works under", async () => {
	vi.mocked(lado.fetchLadoExecution).mockResolvedValue(execution());
	renderPanel();

	const panel = await screen.findByTestId("lado-execution-panel");
	expect(lado.fetchLadoExecution).toHaveBeenCalledWith("demo-7");
	expect(screen.getByRole("button", { name: "LADO run" })).toBeInTheDocument();
	expect(fact(panel, "Agent")).toHaveTextContent("Claude Code opus");
	expect(fact(panel, "Mode")).toHaveTextContent("Accept edits");
	expect(fact(panel, "Permission ceiling")).toHaveTextContent("Auto");
	expect(fact(panel, "Plan")).toHaveTextContent("Revision 3 · approved 2h ago");
	expect(fact(panel, "Rules")).toHaveTextContent("Plan approval required, No force push");
	// The hash the run's board card names.
	expect(within(fact(panel, "Rules")).getByText("1a2b3c4")).toHaveClass("font-mono");
	expect(fact(panel, "Base")).toHaveTextContent("Moved 2 commits since the plan");
	expect(fact(panel, "Deadline")).toHaveTextContent(/^Stops at /);

	// The worker's rule text stays folded until asked for.
	const toggle = within(panel).getByRole("button", { name: "Rule text for the worker" });
	expect(toggle).toHaveAttribute("aria-expanded", "false");
	expect(within(panel).queryByText("Never force push.")).not.toBeInTheDocument();
	fireEvent.click(toggle);
	expect(toggle).toHaveAttribute("aria-expanded", "true");
	expect(within(panel).getByTestId("lado-run-worker-text")).toHaveTextContent("Never force push.");
});

test("lists the local setup items the run got, folded", async () => {
	vi.mocked(lado.fetchLadoExecution).mockResolvedValue(
		execution({
			snapshot: {
				...execution().snapshot,
				setup: [
					{ itemId: "item-claude-md", tool: "claude-code", kind: "instructions", name: "CLAUDE.md", version: 2 },
					{ itemId: "item-review", tool: "claude-code", kind: "skill", name: "review", version: 1 },
				],
			},
		}),
	);
	renderPanel();

	const panel = await screen.findByTestId("lado-execution-panel");
	const toggle = within(fact(panel, "Local setup")).getByRole("button", { name: "2 items" });
	expect(toggle).toHaveAttribute("aria-expanded", "false");
	expect(within(panel).queryByText("CLAUDE.md")).not.toBeInTheDocument();
	fireEvent.click(toggle);
	expect(toggle).toHaveAttribute("aria-expanded", "true");
	const items = within(panel).getByTestId("lado-run-setup");
	expect([...items.querySelectorAll("li")].map((item) => item.textContent)).toEqual(["CLAUDE.md · Instructions", "review · Skill"]);
	cleanup();

	// A run that got no local setup says nothing about it.
	vi.mocked(lado.fetchLadoExecution).mockResolvedValue(execution());
	renderPanel();
	const plain = await screen.findByTestId("lado-execution-panel");
	expect(within(plain).queryByText("Local setup", { selector: "dt" })).not.toBeInTheDocument();
});

test("renders nothing for a session LADO did not start", async () => {
	vi.mocked(lado.fetchLadoExecution).mockResolvedValue(null);
	const { container } = renderPanel();

	await waitFor(() => expect(lado.fetchLadoExecution).toHaveBeenCalledWith("demo-7"));
	await waitFor(() => expect(container).toBeEmptyDOMElement());
	expect(screen.queryByText("LADO run")).not.toBeInTheDocument();
});

// A clock time stays true in a panel nobody refreshes, where "in 5 hr." would not.
test("names the deadline's clock time, with the date when it is not today", async () => {
	vi.useFakeTimers({ toFake: ["Date"] });
	try {
		vi.setSystemTime(new Date(2026, 9, 26, 14, 30));
		vi.mocked(lado.fetchLadoExecution).mockResolvedValue(execution({ deadlineAt: new Date(2026, 9, 26, 18, 5).toISOString() }));
		renderPanel();
		const today = await screen.findByTestId("lado-execution-panel");
		expect(fact(today, "Deadline")).toHaveTextContent("Stops at 06:05 PM");
		cleanup();

		vi.mocked(lado.fetchLadoExecution).mockResolvedValue(execution({ deadlineAt: new Date(2026, 9, 27, 8, 0).toISOString() }));
		renderPanel();
		const tomorrow = await screen.findByTestId("lado-execution-panel");
		expect(fact(tomorrow, "Deadline")).toHaveTextContent("Stops at Oct 27, 08:00 AM");
	} finally {
		vi.useRealTimers();
	}
});

// Once the worker opened a PR, LADO no longer stops it at the deadline.
test("says the time limit no longer applies once a PR was opened", async () => {
	const pr = { url: "https://github.com/acme/demo/pull/45", number: 45, merged: false, closed: false };
	for (const deadlineAt of [new Date(Date.now() + 3_600_000).toISOString(), new Date(Date.now() - 60_000).toISOString()]) {
		vi.mocked(lado.fetchLadoExecution).mockResolvedValue(execution({ pr, deadlineAt }));
		renderPanel();
		const panel = await screen.findByTestId("lado-execution-panel");
		expect(fact(panel, "Deadline")).toHaveTextContent("A PR was opened, so the time limit no longer applies");
		cleanup();
	}
});

test("says when LADO stopped the run at its deadline", async () => {
	vi.mocked(lado.fetchLadoExecution).mockResolvedValue(
		execution({ deadlineAt: new Date(Date.now() - 60_000).toISOString(), timedOutAt: new Date().toISOString(), sessionEnded: true }),
	);
	renderPanel();

	const panel = await screen.findByTestId("lado-execution-panel");
	expect(fact(panel, "Deadline")).toHaveTextContent("Stopped at the maximum run time");
});

test("leaves out a base that did not move or is unknown, and a deadline that does not apply", async () => {
	vi.mocked(lado.fetchLadoExecution).mockResolvedValue(execution({ base: { planSha: "c".repeat(40), commitsAhead: 0 }, deadlineAt: null }));
	renderPanel();
	const panel = await screen.findByTestId("lado-execution-panel");
	expect(within(panel).queryByText("Base", { selector: "dt" })).not.toBeInTheDocument();
	expect(within(panel).queryByText("Deadline", { selector: "dt" })).not.toBeInTheDocument();
	cleanup();

	// Unknown count; a session that ended on its own has no deadline left.
	vi.mocked(lado.fetchLadoExecution).mockResolvedValue(execution({ base: {}, sessionEnded: true }));
	renderPanel();
	const ended = await screen.findByTestId("lado-execution-panel");
	expect(within(ended).queryByText("Base", { selector: "dt" })).not.toBeInTheDocument();
	expect(within(ended).queryByText("Deadline", { selector: "dt" })).not.toBeInTheDocument();
});

test("names built-in rules in the developer's language and keeps renamed ones", async () => {
	await act(() => appI18n.changeLanguage("ko"));
	vi.mocked(lado.fetchLadoExecution).mockResolvedValue(
		execution({
			snapshot: {
				...execution().snapshot,
				rules: [
					{ id: "plan.approval.required", name: "Plan approval required", version: 1 },
					{ id: "git.commit.message.korean", name: "Commits in Korean, please", version: 2 },
				],
			},
		}),
	);
	renderPanel();

	const panel = await screen.findByTestId("lado-execution-panel");
	expect(screen.getByRole("button", { name: "LADO 실행" })).toBeInTheDocument();
	expect(fact(panel, "규칙")).toHaveTextContent("계획 승인 필수, Commits in Korean, please");
	expect(fact(panel, "모드")).toHaveTextContent("편집 수락");
});

test("says why when the daemon could not read the run", async () => {
	vi.mocked(lado.fetchLadoExecution).mockRejectedValue(new lado.LadoApiError("Could not load the run", "LADO_EXECUTION_LOAD_FAILED"));
	renderPanel();

	expect(await screen.findByRole("alert")).toHaveTextContent("LADO could not read or save its data.");
	expect(screen.getByRole("button", { name: "LADO run" })).toBeInTheDocument();
});

test("asks only this computer's daemon, and only for workers", async () => {
	vi.mocked(lado.fetchLadoExecution).mockResolvedValue(execution());
	const remote = renderPanel(session, "host-2");
	const orchestrator = renderPanel({ ...session, id: "demo-orchestrator", kind: "orchestrator" });
	const cloud = renderPanel({ ...session, id: "cloud-1", cloud: { orgId: "org-1" } as WorkspaceSession["cloud"] });

	await new Promise((resolve) => setTimeout(resolve, 20));
	expect(lado.fetchLadoExecution).not.toHaveBeenCalled();
	for (const view of [remote, orchestrator, cloud]) expect(view.container).toBeEmptyDOMElement();
});

test("shows what LADO verified on the PR's head under the snapshot, and nothing for a run without a PR", async () => {
	const head = "1a2b3c4".padEnd(40, "0");
	const pr = { url: "https://github.com/acme/demo/pull/45", number: 45, merged: false, closed: false, headSha: head };
	vi.mocked(lado.fetchLadoExecution).mockResolvedValue(
		execution({
			pr,
			verification: {
				headSha: head,
				prUrl: pr.url,
				outcome: "pass",
				sha256: "9".repeat(64),
				items: [{ kind: "required_checks", outcome: "pass", details: { checks: [{ name: "unit", status: "passed", required: true }] } }],
				evidence: [],
				computedAt: new Date().toISOString(),
			},
		}),
	);
	renderPanel();

	const snapshot = await screen.findByTestId("lado-execution-panel");
	const section = screen.getByTestId("lado-verification");
	expect(screen.getByRole("button", { name: "Verification" })).toBeInTheDocument();
	// Its own inspector section, under the run's snapshot.
	expect(snapshot).not.toContainElement(section);
	expect(snapshot.compareDocumentPosition(section) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
	expect(within(section).getByTestId("lado-verify-state")).toHaveTextContent("Integration pending");
	expect(within(section).getByText("1 of 1 required check passed")).toBeInTheDocument();
	expect(within(section).getByRole("button", { name: "Approve integration…" })).toBeEnabled();
	cleanup();

	vi.mocked(lado.fetchLadoExecution).mockResolvedValue(execution());
	renderPanel();
	await screen.findByTestId("lado-execution-panel");
	expect(screen.queryByTestId("lado-verification")).not.toBeInTheDocument();
	expect(screen.queryByRole("button", { name: "Verification" })).not.toBeInTheDocument();
});
