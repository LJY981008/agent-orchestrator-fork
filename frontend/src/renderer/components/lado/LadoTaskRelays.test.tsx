import { cleanup, render, screen, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import * as lado from "../../lib/lado";
import { createLadoEventBatch } from "../../lib/lado-events";
import { TooltipProvider } from "../ui/tooltip";
import { LadoTaskDialog } from "./LadoTaskDialog";
import { LadoTaskRelays } from "./LadoTaskRelays";

vi.mock("../../lib/lado", async (original) => ({
	...(await original<typeof import("../../lib/lado")>()),
	fetchLadoTasks: vi.fn(),
	fetchLadoTaskView: vi.fn(),
	fetchLadoTaskRelays: vi.fn(),
}));

function relay(change: Partial<lado.LadoRelay> = {}): lado.LadoRelay {
	return {
		id: "lado-relay-1",
		taskId: "lado-task-1",
		projectId: "demo",
		orchestratorSessionId: "demo-orchestrator",
		workerSessionId: "demo-7",
		instruction: "Fix the two follow-ups and push",
		evidence: "fix the two follow-ups and push",
		evidenceMessageId: "msg-1",
		evidenceAt: "2026-11-05T09:00:00Z",
		beyondEvidence: false,
		outsidePlan: false,
		outsidePlanReasons: [],
		state: "delivered",
		createdAt: "2026-11-05T09:01:00Z",
		deliveredAt: "2026-11-05T09:01:01Z",
		...change,
	};
}

function renderWithClient(node: React.ReactNode, client = new QueryClient({ defaultOptions: { queries: { retry: false } } })) {
	return {
		client,
		...render(
			<QueryClientProvider client={client}>
				<TooltipProvider delayDuration={0}>{node}</TooltipProvider>
			</QueryClientProvider>,
		),
	};
}

beforeEach(() => {
	vi.resetAllMocks();
});

afterEach(cleanup);

describe("LadoTaskRelays", () => {
	it("shows each relay's worker, instruction, the developer's words and its state", async () => {
		vi.mocked(lado.fetchLadoTaskRelays).mockResolvedValue([
			relay({ id: "lado-relay-2", workerRole: "integration", workerSessionId: "demo-9" }),
			relay({ id: "lado-relay-1", workerRole: "part", partId: "ui", workerSessionId: "demo-8", state: "sending", deliveredAt: null }),
			relay({ id: "lado-relay-0" }),
		]);
		renderWithClient(<LadoTaskRelays taskId="lado-task-1" />);

		const rows = await screen.findAllByTestId("lado-relay");
		expect(lado.fetchLadoTaskRelays).toHaveBeenCalledWith("lado-task-1");
		expect(rows.map((row) => row.dataset.relayId)).toEqual(["lado-relay-2", "lado-relay-1", "lado-relay-0"]);
		expect(within(rows[0]).getByTestId("lado-relay-worker")).toHaveTextContent("Integration worker");
		expect(rows[0]).toHaveTextContent("demo-9");
		expect(within(rows[0]).getByTestId("lado-relay-state")).toHaveTextContent("Delivered");
		expect(within(rows[1]).getByTestId("lado-relay-worker")).toHaveTextContent("Part worker · part ui");
		expect(within(rows[1]).getByTestId("lado-relay-state")).toHaveTextContent("Sending");
		expect(within(rows[2]).getByTestId("lado-relay-worker")).toHaveTextContent("Worker");
		expect(within(rows[2]).getByTestId("lado-relay-instruction")).toHaveTextContent("Fix the two follow-ups and push");
		expect(within(rows[2]).getByTestId("lado-relay-evidence")).toHaveTextContent("fix the two follow-ups and push");
		expect(rows[2]).toHaveTextContent("Developer's words");
		expect(rows[2]).toHaveTextContent("Asked by orchestrator demo-orchestrator");
		// A relay within the developer's words and the plan carries no marks.
		expect(within(rows[2]).queryByTestId("lado-relay-beyond-evidence")).not.toBeInTheDocument();
		expect(within(rows[2]).queryByTestId("lado-relay-outside-plan")).not.toBeInTheDocument();
		expect(within(rows[2]).queryByTestId("lado-relay-failure")).not.toBeInTheDocument();
	});

	it("marks an instruction beyond the developer's words or outside the plan, and says why a relay failed", async () => {
		vi.mocked(lado.fetchLadoTaskRelays).mockResolvedValue([
			relay({
				id: "lado-relay-3",
				beyondEvidence: true,
				outsidePlan: true,
				outsidePlanReasons: ["오케스트레이터가 계획 밖으로 표시", "docs/other.md"],
				state: "failed",
				failure: "the worker is waiting for a decision",
				deliveredAt: null,
			}),
			relay({ id: "lado-relay-4", state: "failed", deliveredAt: null }),
		]);
		renderWithClient(<LadoTaskRelays taskId="lado-task-1" />);

		const [marked, bare] = await screen.findAllByTestId("lado-relay");
		expect(within(marked).getByTestId("lado-relay-beyond-evidence")).toHaveTextContent("Not the same as the developer's words");
		const outside = within(marked).getByTestId("lado-relay-outside-plan");
		expect(outside).toHaveTextContent("May leave the approved plan");
		expect(within(outside).getAllByRole("listitem").map((item) => item.textContent)).toEqual([
			"오케스트레이터가 계획 밖으로 표시",
			"docs/other.md",
		]);
		expect(within(marked).getByTestId("lado-relay-state")).toHaveTextContent("Not delivered");
		expect(within(marked).getByTestId("lado-relay-failure")).toHaveTextContent("Not delivered: the worker is waiting for a decision");
		expect(within(bare).getByTestId("lado-relay-failure")).toHaveTextContent("Not delivered.");
	});

	it("says so when LADO relayed nothing", async () => {
		vi.mocked(lado.fetchLadoTaskRelays).mockResolvedValue([]);
		renderWithClient(<LadoTaskRelays taskId="lado-task-1" />);

		expect(await screen.findByText("LADO has relayed no instruction to this task's workers.")).toBeInTheDocument();
		expect(screen.queryByTestId("lado-relay")).not.toBeInTheDocument();
	});

	it("shows why the relays could not be read", async () => {
		vi.mocked(lado.fetchLadoTaskRelays).mockRejectedValue(new lado.LadoApiError("No task", "LADO_TASK_NOT_FOUND"));
		renderWithClient(<LadoTaskRelays taskId="lado-task-1" />);

		expect(await screen.findByRole("alert")).toBeInTheDocument();
		expect(screen.queryByTestId("lado-task-relays")).not.toBeInTheDocument();
	});

	it("reads again when a task event names the task", async () => {
		vi.mocked(lado.fetchLadoTaskRelays).mockResolvedValueOnce([]).mockResolvedValue([relay()]);
		const { client } = renderWithClient(<LadoTaskRelays taskId="lado-task-1" />);
		await screen.findByText("LADO has relayed no instruction to this task's workers.");

		const batch = createLadoEventBatch(client);
		expect(batch.note({ kind: "lado_task", projectId: "demo", taskId: "lado-task-1" })).toBe(true);
		batch.flush((queryKey) => void client.invalidateQueries({ queryKey }));

		expect(await screen.findByTestId("lado-relay")).toBeInTheDocument();
		expect(lado.fetchLadoTaskRelays).toHaveBeenCalledTimes(2);
	});
});

describe("LadoTaskDialog Relays tab", () => {
	it("reads the task's relays only once the tab opens", async () => {
		const user = userEvent.setup();
		const task: lado.LadoTask = {
			id: "lado-task-1",
			projectId: "demo",
			repository: "github:acme/demo",
			state: "draft",
			issue: { ref: "github:acme/demo#12", number: 12, title: "Add login", state: "open", url: "https://github.com/acme/demo/issues/12", observedAt: "2026-10-20T09:00:00Z" },
			plan: { requestPending: false },
			createdAt: "2026-10-20T09:00:00Z",
		};
		vi.mocked(lado.fetchLadoTasks).mockResolvedValue([]);
		vi.mocked(lado.fetchLadoTaskView).mockResolvedValue({
			task,
			issue: { title: "Add login", body: "Body", state: "open", url: task.issue.url },
			comments: [],
			issueChangedSincePlan: false,
			history: { requests: [], revisions: [], approvals: [], critiques: [] },
		});
		vi.mocked(lado.fetchLadoTaskRelays).mockResolvedValue([relay({ beyondEvidence: true })]);
		renderWithClient(<LadoTaskDialog projectId="demo" taskId="lado-task-1" repositories={[]} onOpenChange={() => undefined} />);

		const dialog = await screen.findByTestId("lado-task-dialog");
		await within(dialog).findByText("Body");
		expect(lado.fetchLadoTaskRelays).not.toHaveBeenCalled();

		await user.click(within(dialog).getByRole("tab", { name: "Relays" }));
		expect(await within(dialog).findByTestId("lado-task-relays")).toBeInTheDocument();
		expect(within(dialog).getByTestId("lado-relay-beyond-evidence")).toBeInTheDocument();
		expect(lado.fetchLadoTaskRelays).toHaveBeenCalledWith("lado-task-1");
	});
});
