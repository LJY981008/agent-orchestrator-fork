import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import * as lado from "../../lib/lado";
import { LadoPartsProgress, LadoPlanParts } from "./LadoParts";

const { navigate } = vi.hoisted(() => ({ navigate: vi.fn() }));

vi.mock("@tanstack/react-router", async (original) => ({
	...(await original<typeof import("@tanstack/react-router")>()),
	useNavigate: () => navigate,
}));

vi.mock("../../lib/lado", async (original) => ({
	...(await original<typeof import("../../lib/lado")>()),
	fetchLadoTaskParts: vi.fn(),
	fetchLadoTaskUsage: vi.fn(),
}));

function planContent(change: Partial<lado.LadoPlan["plan"]> = {}): lado.LadoPlan["plan"] {
	return {
		steps: ["Add the API", "Add the page", "Document it", "Check it all"],
		acceptanceCriteria: ["works"],
		affectedPaths: ["api/**", "web/**", "docs/**"],
		testCommands: [],
		reviewRequired: false,
		changeKinds: [],
		parts: [
			{ id: "api", title: "API", steps: [1], affectedPaths: ["api/**"], dependsOn: [], phase: 1, warnings: [] },
			{ id: "web", title: "Page", steps: [2], affectedPaths: ["web/**"], dependsOn: [], phase: 1, warnings: [] },
			{ id: "docs", title: "Docs", steps: [3], affectedPaths: ["docs/**"], dependsOn: ["api", "web"], phase: 2, warnings: [] },
		],
		integrationSteps: [4],
		warnings: [],
		body: "",
		...change,
	};
}

function renderWithClient(node: React.ReactNode) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(<QueryClientProvider client={client}>{node}</QueryClientProvider>);
}

afterEach(() => {
	cleanup();
	vi.clearAllMocks();
});

describe("LadoPlanParts", () => {
	it("lists the parts by phase and the integration after them", () => {
		renderWithClient(<LadoPlanParts content={planContent()} />);
		const phases = screen.getAllByTestId("lado-plan-phase");
		expect(phases).toHaveLength(2);
		expect(phases[0]).toHaveTextContent("Phase 1 · at once");
		expect(within(phases[0]).getAllByTestId("lado-plan-part").map((row) => row.dataset.partId)).toEqual(["api", "web"]);
		expect(phases[1]).toHaveTextContent("Phase 2");
		expect(phases[1]).not.toHaveTextContent("at once");
		expect(within(phases[1]).getByTestId("lado-plan-part")).toHaveTextContent("After api, web");
		expect(screen.getByTestId("lado-plan-integration")).toHaveTextContent("then steps 4, the checks and the pull request");
	});

	it("warns about a large plan without parts and says nothing about a small one", () => {
		const { rerender } = renderWithClient(<LadoPlanParts content={planContent({ parts: [], integrationSteps: [], warnings: ["unsplit_large_plan"] })} />);
		expect(screen.getByTestId("lado-plan-unsplit")).toHaveTextContent("one worker does it from start to end");
		rerender(
			<QueryClientProvider client={new QueryClient()}>
				<LadoPlanParts content={planContent({ parts: [], integrationSteps: [], warnings: [] })} />
			</QueryClientProvider>,
		);
		expect(screen.queryByTestId("lado-plan-unsplit")).not.toBeInTheDocument();
		expect(screen.queryByTestId("lado-plan-parts")).not.toBeInTheDocument();
	});

	// #69: the sizing warnings, which do not block the approval.
	it("warns above the parts about a small plan split into parts", () => {
		renderWithClient(<LadoPlanParts content={planContent({ warnings: ["split_small_plan"] })} />);
		const warning = screen.getByTestId("lado-plan-split-small");
		expect(warning).toHaveAttribute("role", "note");
		expect(warning).toHaveTextContent("one worker would cost less");
		// Above the list of phases.
		expect(warning.compareDocumentPosition(screen.getAllByTestId("lado-plan-phase")[0]!) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
		expect(screen.queryAllByTestId("lado-plan-part-warning")).toHaveLength(0);
	});

	it("warns under a part about each sizing limit it is over", () => {
		const content = planContent();
		content.parts[0] = {
			...content.parts[0]!,
			warnings: [
				{ code: "part_many_steps", value: 5, limit: 4 },
				{ code: "part_many_files", value: 201, limit: 200 },
			],
		};
		content.parts[1] = { ...content.parts[1]!, warnings: [{ code: "part_many_paths", value: 9, limit: 8 }] };
		renderWithClient(<LadoPlanParts content={content} />);
		const rows = screen.getAllByTestId("lado-plan-part");
		const api = within(rows[0]!).getAllByTestId("lado-plan-part-warning");
		expect(api.map((row) => row.dataset.code)).toEqual(["part_many_steps", "part_many_files"]);
		expect(api[0]).toHaveTextContent("This part has 5 steps; a part should have at most 4.");
		expect(api[1]).toHaveTextContent("201 files sit under this part's paths; a part should cover at most 200.");
		expect(within(rows[1]!).getByTestId("lado-plan-part-warning")).toHaveTextContent("This part has 9 affected paths; a part should have at most 8.");
		expect(within(rows[2]!).queryByTestId("lado-plan-part-warning")).not.toBeInTheDocument();
		expect(screen.queryByTestId("lado-plan-split-small")).not.toBeInTheDocument();
	});
});

const runningTask: lado.LadoTask = {
	id: "lado-task-1",
	projectId: "demo",
	repository: "github:acme/demo",
	state: "running",
	issue: { ref: "github:acme/demo#12", number: 12, title: "Add login", state: "open", url: "https://github.com/acme/demo/issues/12", observedAt: "2026-10-29T09:00:00Z" },
	plan: { requestPending: false },
	parts: { total: 3, merged: 1 },
	createdAt: "2026-10-29T09:00:00Z",
};

const partsRun: lado.LadoTaskParts = {
	branch: "lado/c14c6be2/integration",
	baseSha: "a".repeat(40),
	headSha: "b".repeat(40),
	parts: [
		{ id: "api", title: "API", phase: 1, steps: [1], affectedPaths: ["api/**"], dependsOn: [], state: "merged", attempts: 1, sessionId: "demo-8", startSha: "a".repeat(40), mergedSha: "b".repeat(40), mergeAttempts: 0, updatedAt: "2026-10-29T09:10:00Z" },
		{ id: "web", title: "Page", phase: 1, steps: [2], affectedPaths: ["web/**"], dependsOn: [], state: "conflict", attempts: 1, sessionId: "demo-9", mergeSessionId: "demo-11", mergeAttempts: 1, updatedAt: "2026-10-29T09:12:00Z" },
		{ id: "docs", title: "Docs", phase: 2, steps: [3], affectedPaths: ["docs/**"], dependsOn: ["api", "web"], state: "waiting", waitingReason: "part_dependency", waitingDetail: "web", attempts: 0, mergeAttempts: 0, updatedAt: "2026-10-29T09:00:00Z" },
	],
};

function sessionUsage(sessionId: string, change: Partial<lado.LadoSessionUsage>, costNanos = 0): lado.LadoSessionUsage {
	return {
		sessionId,
		role: "part",
		attempt: 1,
		compactions: 0,
		contextUsed: 0,
		contextWindow: 200_000,
		incomplete: false,
		usageMissing: false,
		totals: {
			inputTokens: 0,
			cachedInputTokens: 0,
			outputTokens: 0,
			costNanos,
			subagentInputTokens: 0,
			subagentOutputTokens: 0,
			subagentCostNanos: 0,
		},
		...change,
	};
}

const usageSummary: lado.LadoTaskUsageSummary = {
	taskId: "lado-task-1",
	title: "Add login",
	compactions: 0,
	elapsedMs: 0,
	endReason: "in_progress",
	endedAt: "2026-10-29T09:30:00Z",
	inProgress: true,
	incomplete: false,
	sessionsMissing: 0,
	totals: sessionUsage("", {}).totals,
};

describe("LadoPartsProgress", () => {
	beforeEach(() => {
		vi.mocked(lado.fetchLadoTaskParts).mockResolvedValue(partsRun);
		vi.mocked(lado.fetchLadoTaskUsage).mockRejectedValue(new Error("no usage"));
	});

	it("shows each part's state, why it waits, its commits and its workers, then the integration", async () => {
		const user = userEvent.setup();
		const onClose = vi.fn();
		renderWithClient(<LadoPartsProgress task={runningTask} onClose={onClose} />);
		const rows = await screen.findAllByTestId("lado-part");
		expect(lado.fetchLadoTaskParts).toHaveBeenCalledWith("lado-task-1");
		expect(screen.getByTestId("lado-parts-progress")).toHaveTextContent("Parts 1/3");
		expect(screen.getByTestId("lado-parts-progress")).toHaveTextContent("lado/c14c6be2/integration bbbbbbb");
		expect(rows[0]).toHaveTextContent("Merged");
		expect(rows[0]).toHaveTextContent("aaaaaaa → bbbbbbb");
		expect(rows[1]).toHaveTextContent("Resolving a conflict");
		expect(rows[2]).toHaveTextContent("Waiting for web to be merged");
		expect(screen.getByTestId("lado-parts-integration")).toHaveTextContent("Starts once every part is merged");

		await user.click(within(rows[1]).getByRole("button", { name: "Merge worker" }));
		expect(onClose).toHaveBeenCalled();
		expect(navigate).toHaveBeenCalledWith({ to: "/projects/$projectId/sessions/$sessionId", params: { projectId: "demo", sessionId: "demo-11" } });
	});

	it("links the integration worker once it runs", async () => {
		const user = userEvent.setup();
		const task = { ...runningTask, parts: { total: 3, merged: 3 }, execution: { sessionId: "demo-12", planRevisionId: "lado-plan-1", startedAt: "2026-10-29T09:30:00Z", timedOut: false, sessionEnded: false } };
		renderWithClient(<LadoPartsProgress task={task} onClose={() => undefined} />);
		await user.click(await screen.findByRole("button", { name: "Integration worker" }));
		expect(navigate).toHaveBeenCalledWith({ to: "/projects/$projectId/sessions/$sessionId", params: { projectId: "demo", sessionId: "demo-12" } });
	});

	// #64: a workspace run with an integration branch in each of two repositories.
	it("shows each repository's integration branch, a part's commits there and where its merge conflicts", async () => {
		const sha = (c: string) => c.repeat(40);
		vi.mocked(lado.fetchLadoTaskParts).mockResolvedValue({
			...partsRun,
			baseSha: sha("1"),
			headSha: sha("2"),
			repos: [
				{ repo: "back", position: 0, baseSha: sha("1"), headSha: sha("2"), baseBranch: "main" },
				{ repo: "front", position: 1, baseSha: sha("3"), headSha: sha("3"), baseBranch: "main" },
			],
			parts: [
				{
					...partsRun.parts[0]!,
					repos: [
						{ repo: "back", empty: false, startSha: sha("1"), mergedSha: sha("2") },
						{ repo: "front", empty: true, startSha: sha("3") },
					],
				},
				{ ...partsRun.parts[1]!, mergeRepo: "front", repos: [{ repo: "back", empty: false, startSha: sha("2") }, { repo: "front", empty: false, startSha: sha("3") }] },
			],
		});
		renderWithClient(<LadoPartsProgress task={runningTask} onClose={() => undefined} />);
		const rows = await screen.findAllByTestId("lado-part");
		const repos = screen.getAllByTestId("lado-parts-repo");
		expect(repos.map((repo) => repo.dataset.repo)).toEqual(["back", "front"]);
		expect(repos[0]).toHaveTextContent("back1111111 → 2222222");
		expect(repos[1]).toHaveTextContent("front3333333unchanged");
		// The branch line no longer names one repository's head.
		expect(screen.getByTestId("lado-parts-progress")).not.toHaveTextContent("lado/c14c6be2/integration 2222222");

		const first = within(rows[0]!).getAllByTestId("lado-part-repo");
		expect(first[0]).toHaveTextContent("back1111111 → 2222222");
		expect(first[1]).toHaveTextContent("frontunchanged");
		expect(within(rows[1]!).getByTestId("lado-part-merge-repo")).toHaveTextContent("Merge conflict in front");
	});

	// #69: the measure each part's row shows, to tune the sizing limits by.
	it("adds up each part's compactions and cost from the sessions of the run it shows", async () => {
		vi.mocked(lado.fetchLadoTaskParts).mockResolvedValue({ ...partsRun, queueEntryId: "lado-queue-2" });
		vi.mocked(lado.fetchLadoTaskUsage).mockResolvedValue({
			task: usageSummary,
			sessions: [
				sessionUsage("demo-1", { role: "designer" }, 9_000_000_000),
				// An earlier approval's run of a part with the same id.
				sessionUsage("demo-2", { partId: "api", queueEntryId: "lado-queue-1", compactions: 7 }, 20_000_000_000),
				sessionUsage("demo-8", { partId: "api", queueEntryId: "lado-queue-2", compactions: 1 }, 1_500_000_000),
				// A retry of the same part in the run shown counts too.
				sessionUsage("demo-10", { partId: "api", queueEntryId: "lado-queue-2", attempt: 2, compactions: 2 }, 1_000_000_000),
				sessionUsage("demo-9", { partId: "web", queueEntryId: "lado-queue-2" }, 2_000_000),
				// The merge worker's cost is not the part's.
				sessionUsage("demo-11", { role: "merge", partId: "web", queueEntryId: "lado-queue-2", compactions: 3 }, 4_000_000_000),
			],
		});
		renderWithClient(<LadoPartsProgress task={runningTask} onClose={() => undefined} />);
		await waitFor(() => expect(screen.getAllByTestId("lado-part-measure")[0]).toHaveTextContent("1 steps · 3 compactions · $2.50"));
		expect(lado.fetchLadoTaskUsage).toHaveBeenCalledWith("lado-task-1");
		const measures = screen.getAllByTestId("lado-part-measure");
		expect(measures[1]).toHaveTextContent("1 steps · 0 compactions · <$0.01");
		// A part no session ran yet.
		expect(measures[2]).toHaveTextContent("1 steps · 0 compactions · $0.00");
	});

	it("shows only the steps without the task's usage or the run's queue entry", async () => {
		renderWithClient(<LadoPartsProgress task={runningTask} onClose={() => undefined} />);
		await screen.findAllByTestId("lado-part");
		await waitFor(() => expect(lado.fetchLadoTaskUsage).toHaveBeenCalled());
		for (const measure of screen.getAllByTestId("lado-part-measure")) {
			expect(measure).toHaveTextContent(/^1 steps$/);
		}
		cleanup();

		// The usage reads, but the parts response names no run.
		vi.mocked(lado.fetchLadoTaskUsage).mockResolvedValue({
			task: usageSummary,
			sessions: [sessionUsage("demo-8", { partId: "api", queueEntryId: "lado-queue-2", compactions: 1 }, 1_500_000_000)],
		});
		renderWithClient(<LadoPartsProgress task={runningTask} onClose={() => undefined} />);
		await screen.findAllByTestId("lado-part");
		await waitFor(() => expect(lado.fetchLadoTaskUsage).toHaveBeenCalledTimes(2));
		expect(screen.getAllByTestId("lado-part-measure")[0]).toHaveTextContent(/^1 steps$/);
	});

	it("reads nothing for a task without parts", () => {
		const { parts: _parts, ...single } = runningTask;
		const { container } = renderWithClient(<LadoPartsProgress task={single} onClose={() => undefined} />);
		expect(container).toBeEmptyDOMElement();
		expect(lado.fetchLadoTaskParts).not.toHaveBeenCalled();
		expect(lado.fetchLadoTaskUsage).not.toHaveBeenCalled();
	});
});
