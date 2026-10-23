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
	expect(fact(panel, "Deadline")).toHaveTextContent("Stops in 5 hr.");

	// The worker's rule text stays folded until asked for.
	const toggle = within(panel).getByRole("button", { name: "Rule text for the worker" });
	expect(toggle).toHaveAttribute("aria-expanded", "false");
	expect(within(panel).queryByText("Never force push.")).not.toBeInTheDocument();
	fireEvent.click(toggle);
	expect(toggle).toHaveAttribute("aria-expanded", "true");
	expect(within(panel).getByTestId("lado-run-worker-text")).toHaveTextContent("Never force push.");
});

test("renders nothing for a session LADO did not start", async () => {
	vi.mocked(lado.fetchLadoExecution).mockResolvedValue(null);
	const { container } = renderPanel();

	await waitFor(() => expect(lado.fetchLadoExecution).toHaveBeenCalledWith("demo-7"));
	await waitFor(() => expect(container).toBeEmptyDOMElement());
	expect(screen.queryByText("LADO run")).not.toBeInTheDocument();
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
