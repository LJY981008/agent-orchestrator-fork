// LADO fork: the board's cost line and alert badge (issue #42), and the task
// view a click on a LADO alert's notification opens.
import { act, cleanup, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import * as lado from "../../lib/lado";
import { requestLadoTaskOpen } from "../../lib/lado-alert-notifications";
import { createLadoEventBatch } from "../../lib/lado-events";
import { TooltipProvider } from "../ui/tooltip";
import { LadoLanes } from "./LadoLanes";

vi.mock("@tanstack/react-router", async (original) => ({
	...(await original<typeof import("@tanstack/react-router")>()),
	useNavigate: () => vi.fn(),
}));

vi.mock("../../lib/lado", async (original) => ({
	...(await original<typeof import("../../lib/lado")>()),
	fetchLadoTasks: vi.fn(),
	fetchLadoProjectRepositories: vi.fn(),
	fetchLadoTaskView: vi.fn(),
	fetchLadoProjectUsage: vi.fn(),
	fetchLadoAlerts: vi.fn(),
	fetchLadoUnseenAlertCounts: vi.fn(),
	fetchLadoTaskUsage: vi.fn(),
}));

const task: lado.LadoTask = {
	id: "lado-task-1",
	projectId: "demo",
	repository: "github:acme/demo",
	state: "draft",
	issue: { ref: "github:acme/demo#12", number: 12, title: "Add login", state: "open", url: "https://github.com/acme/demo/issues/12", observedAt: "2026-10-19T09:00:00Z" },
	plan: { requestPending: false },
	createdAt: "2026-10-19T09:00:00Z",
};

const totals = (costNanos: number): lado.LadoUsageTotals => ({
	costNanos,
	inputTokens: 0,
	cachedInputTokens: 0,
	outputTokens: 0,
	subagentCostNanos: 0,
	subagentInputTokens: 0,
	subagentOutputTokens: 0,
});

function summary(taskId: string, costNanos: number): lado.LadoTaskUsageSummary {
	return {
		taskId,
		title: "Add login",
		endReason: "in_progress",
		endedAt: "2026-10-19T10:00:00Z",
		inProgress: true,
		elapsedMs: 3_600_000,
		compactions: 0,
		incomplete: false,
		sessionsMissing: 0,
		totals: totals(costNanos),
	};
}

function unseen(taskId: string, count: number): lado.LadoUnseenAlertCounts {
	return { tasks: [{ taskId, unseen: count }] };
}

function renderLanes() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	render(
		<QueryClientProvider client={client}>
			<TooltipProvider delayDuration={0}>
				<LadoLanes projectId="demo" />
			</TooltipProvider>
		</QueryClientProvider>,
	);
	return client;
}

beforeEach(() => {
	vi.resetAllMocks();
	vi.mocked(lado.fetchLadoProjectRepositories).mockResolvedValue([]);
	vi.mocked(lado.fetchLadoTasks).mockResolvedValue([task, { ...task, id: "lado-task-2", issue: { ...task.issue, number: 13, title: "Fix the build" } }]);
	vi.mocked(lado.fetchLadoProjectUsage).mockResolvedValue({
		projectId: "demo",
		tasks: [summary("lado-task-1", 120.4 * 1e9), summary("lado-task-2", 0)],
	});
	vi.mocked(lado.fetchLadoTaskView).mockResolvedValue({
		task,
		issue: { title: "Add login", body: "Body", state: "open", url: task.issue.url },
		comments: [],
		issueChangedSincePlan: false,
		history: { requests: [], revisions: [], approvals: [], critiques: [] },
	});
	vi.mocked(lado.fetchLadoTaskUsage).mockResolvedValue({ task: summary("lado-task-1", 120.4 * 1e9), sessions: [] });
	vi.mocked(lado.fetchLadoAlerts).mockResolvedValue([]);
	vi.mocked(lado.fetchLadoUnseenAlertCounts).mockResolvedValue({ tasks: [] });
});

afterEach(cleanup);

test("a card shows the task's cost and a badge of its unseen alerts, and a seen alert takes the badge down", async () => {
	vi.mocked(lado.fetchLadoUnseenAlertCounts).mockResolvedValueOnce(unseen("lado-task-1", 2)).mockResolvedValue(unseen("lado-task-1", 1));
	const client = renderLanes();

	const cards = await screen.findAllByTestId("lado-task-card");
	const first = cards.find((card) => card.dataset.taskId === "lado-task-1")!;
	const second = cards.find((card) => card.dataset.taskId === "lado-task-2")!;
	expect(await within(first).findByTestId("lado-card-cost")).toHaveTextContent("$120.40");
	expect(await within(first).findByTestId("lado-card-alerts")).toHaveTextContent("2");
	expect(lado.fetchLadoUnseenAlertCounts).toHaveBeenCalledWith("demo");
	// A task whose sessions spent nothing and have no alerts shows neither.
	expect(within(second).queryByTestId("lado-card-watch")).not.toBeInTheDocument();

	// The seen UPDATE event of alert 7, from this or another window.
	const batch = createLadoEventBatch(client);
	batch.note({ kind: "lado_alert", alertId: 7, op: "update", projectId: "demo", taskId: "lado-task-1", sessionId: "" });
	act(() => batch.flush((queryKey) => void client.invalidateQueries({ queryKey })));

	await waitFor(() => expect(within(first).getByTestId("lado-card-alerts")).toHaveTextContent("1"));
});

test("the badge opens the task on its Usage tab", async () => {
	const user = userEvent.setup();
	vi.mocked(lado.fetchLadoUnseenAlertCounts).mockResolvedValue(unseen("lado-task-1", 1));
	renderLanes();

	const badge = await screen.findByRole("button", { name: "Unseen alerts: 1" });
	await user.click(badge);
	const dialog = await screen.findByTestId("lado-task-dialog");
	expect(await within(dialog).findByTestId("lado-task-usage")).toBeInTheDocument();
});

test("the badge counts every unseen alert, past the alert list's limit", async () => {
	vi.mocked(lado.fetchLadoUnseenAlertCounts).mockResolvedValue(unseen("lado-task-1", 240));
	renderLanes();
	const cards = await screen.findAllByTestId("lado-task-card");
	const first = cards.find((card) => card.dataset.taskId === "lado-task-1")!;
	expect(await within(first).findByTestId("lado-card-alerts")).toHaveTextContent("240");
	expect(lado.fetchLadoAlerts).not.toHaveBeenCalledWith("demo", { unseen: true });
});

test("a click on a LADO alert's notification opens its task on the Usage tab", async () => {
	renderLanes();
	await screen.findAllByTestId("lado-task-card");

	act(() => requestLadoTaskOpen({ projectId: "demo", taskId: "lado-task-1", tab: "usage" }));
	const dialog = await screen.findByTestId("lado-task-dialog");
	expect(lado.fetchLadoTaskView).toHaveBeenCalledWith("lado-task-1");
	expect(await within(dialog).findByTestId("lado-task-usage")).toBeInTheDocument();
});
