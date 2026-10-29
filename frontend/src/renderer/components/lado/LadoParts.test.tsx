import { cleanup, render, screen, within } from "@testing-library/react";
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
			{ id: "api", title: "API", steps: [1], affectedPaths: ["api/**"], dependsOn: [], phase: 1 },
			{ id: "web", title: "Page", steps: [2], affectedPaths: ["web/**"], dependsOn: [], phase: 1 },
			{ id: "docs", title: "Docs", steps: [3], affectedPaths: ["docs/**"], dependsOn: ["api", "web"], phase: 2 },
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

describe("LadoPartsProgress", () => {
	beforeEach(() => {
		vi.mocked(lado.fetchLadoTaskParts).mockResolvedValue(partsRun);
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

	it("reads nothing for a task without parts", () => {
		const { parts: _parts, ...single } = runningTask;
		const { container } = renderWithClient(<LadoPartsProgress task={single} onClose={() => undefined} />);
		expect(container).toBeEmptyDOMElement();
		expect(lado.fetchLadoTaskParts).not.toHaveBeenCalled();
	});
});
