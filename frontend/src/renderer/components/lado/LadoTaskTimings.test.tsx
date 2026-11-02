import { cleanup, render, screen, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import userEvent from "@testing-library/user-event";
import type { TFunction } from "i18next";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { appI18n } from "../../i18n";
import * as lado from "../../lib/lado";
import { TooltipProvider } from "../ui/tooltip";
import { LadoTaskDialog } from "./LadoTaskDialog";
import { ladoDurationText, LadoTaskTimings } from "./LadoTaskTimings";

vi.mock("../../lib/lado", async (original) => ({
	...(await original<typeof import("../../lib/lado")>()),
	fetchLadoTasks: vi.fn(),
	fetchLadoTaskView: vi.fn(),
	fetchLadoTaskTimings: vi.fn(),
}));

const MINUTE = 60_000;
const HOUR = 60 * MINUTE;

function session(change: Partial<lado.LadoSessionMetrics> = {}): lado.LadoSessionMetrics {
	return {
		sessionId: "ao-7",
		role: "worker",
		toolCalls: 42,
		readCalls: 12,
		searchCalls: 8,
		investigationCalls: 20,
		commandCalls: 10,
		editCalls: 12,
		investigateMs: 18 * MINUTE,
		firstActivityAt: "2026-10-20T11:00:00Z",
		firstEditAt: "2026-10-20T11:18:00Z",
		lastActivityAt: "2026-10-20T12:00:00Z",
		stored: true,
		updatedAt: "2026-10-20T13:00:00Z",
		...change,
	};
}

function timings(change: Partial<lado.LadoTaskTimings> = {}): lado.LadoTaskTimings {
	return {
		taskId: "lado-task-1",
		issueRef: "github:acme/demo#12",
		title: "Add login",
		timeline: {
			points: [
				{ stage: "received", at: "2026-10-20T09:00:00Z", sincePrevMs: 0, estimated: false },
				{ stage: "plan_requested", at: "2026-10-20T09:05:00Z", sincePrevMs: 5 * MINUTE, estimated: false },
				{ stage: "revision", at: "2026-10-20T09:35:00Z", sincePrevMs: 30 * MINUTE, estimated: false },
				{ stage: "approved", at: "2026-10-20T10:00:00Z", sincePrevMs: 25 * MINUTE, estimated: false },
				{ stage: "run_started", at: "2026-10-20T10:00:30Z", sincePrevMs: 30_000, estimated: false },
				{ stage: "pr_opened", at: "2026-10-20T12:10:00Z", sincePrevMs: 2 * HOUR + 9.5 * MINUTE, estimated: true },
				{ stage: "verified", at: "2026-10-20T12:40:00Z", sincePrevMs: 30 * MINUTE, estimated: true },
				{ stage: "merged", at: "2026-10-21T13:00:00Z", sincePrevMs: 24 * HOUR + 20 * MINUTE, estimated: false },
			],
			totalMs: 28 * HOUR,
			planRequests: 2,
			revisions: 3,
			approvals: 1,
			runs: 1,
		},
		sessions: [
			session({ sessionId: "ao-5", role: "designer", editCalls: 0, firstEditAt: null, investigateMs: 25 * MINUTE }),
			session({ sessionId: "ao-6", role: "critic", stored: false, updatedAt: null, investigateMs: 40_000 }),
			session(),
		],
		...change,
	};
}

function renderWithClient(node: React.ReactNode) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<TooltipProvider delayDuration={0}>{node}</TooltipProvider>
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	vi.resetAllMocks();
});

afterEach(cleanup);

describe("ladoDurationText", () => {
	const t = appI18n.t.bind(appI18n) as unknown as TFunction;

	it("keeps the two largest units", () => {
		expect(ladoDurationText(t, 0)).toBe("0 s");
		expect(ladoDurationText(t, 44_600)).toBe("45 s");
		expect(ladoDurationText(t, 12 * MINUTE + 59_000)).toBe("12 min");
		expect(ladoDurationText(t, 3 * HOUR + 5 * MINUTE)).toBe("3 h 5 min");
		expect(ladoDurationText(t, 52 * HOUR)).toBe("2 d 4 h");
		expect(ladoDurationText(t, -5)).toBe("0 s");
	});
});

describe("LadoTaskTimings", () => {
	it("lists the stages with the time since the one before, the total and the counts", async () => {
		vi.mocked(lado.fetchLadoTaskTimings).mockResolvedValue(timings());
		renderWithClient(<LadoTaskTimings taskId="lado-task-1" />);

		const rows = await screen.findAllByTestId("lado-timing-point");
		expect(lado.fetchLadoTaskTimings).toHaveBeenCalledWith("lado-task-1");
		expect(rows.map((row) => row.dataset.stage)).toEqual([
			"received", "plan_requested", "revision", "approved", "run_started", "pr_opened", "verified", "merged",
		]);
		expect(rows[0]).toHaveTextContent("Received");
		expect(rows[0]).not.toHaveTextContent("+");
		expect(rows[1]).toHaveTextContent("Plan requested");
		expect(rows[1]).toHaveTextContent("+5 min");
		expect(rows[4]).toHaveTextContent("+30 s");
		expect(rows[5]).toHaveTextContent("PR opened");
		expect(rows[5]).toHaveTextContent("+2 h 9 min");
		expect(rows[7]).toHaveTextContent("+1 d 0 h");
		expect(screen.getByTestId("lado-timings-total")).toHaveTextContent("Total 1 d 4 h");
		expect(screen.getByTestId("lado-timings-counts")).toHaveTextContent("Plan requests 2 · revisions 3 · approvals 1 · runs 1");
	});

	it("marks estimated times and says what an estimate means", async () => {
		vi.mocked(lado.fetchLadoTaskTimings).mockResolvedValue(timings());
		renderWithClient(<LadoTaskTimings taskId="lado-task-1" />);

		const rows = await screen.findAllByTestId("lado-timing-point");
		expect(within(rows[5]).getByTestId("lado-timing-estimated")).toHaveTextContent("Estimated");
		expect(within(rows[6]).getByTestId("lado-timing-estimated")).toBeInTheDocument();
		expect(within(rows[3]).queryByTestId("lado-timing-estimated")).not.toBeInTheDocument();
		expect(screen.getByText(/An estimated verified time can be later than the real one/)).toBeInTheDocument();
	});

	it("says a task is not merged yet and leaves the estimate note out without estimates", async () => {
		const open = timings();
		vi.mocked(lado.fetchLadoTaskTimings).mockResolvedValue({
			...open,
			timeline: { ...open.timeline, points: open.timeline.points.slice(0, 4), totalMs: 0 },
		});
		renderWithClient(<LadoTaskTimings taskId="lado-task-1" />);

		expect(await screen.findByTestId("lado-timings-total")).toHaveTextContent("Not merged yet");
		expect(screen.getAllByTestId("lado-timing-point")).toHaveLength(4);
		expect(screen.queryByText(/An estimated verified time/)).not.toBeInTheDocument();
	});

	it("shows each session's investigation and marks metrics computed at read time", async () => {
		vi.mocked(lado.fetchLadoTaskTimings).mockResolvedValue(timings());
		renderWithClient(<LadoTaskTimings taskId="lado-task-1" />);

		const rows = await screen.findAllByTestId("lado-session-metrics");
		expect(rows.map((row) => row.dataset.role)).toEqual(["designer", "critic", "worker"]);
		expect(rows[0]).toHaveTextContent("Plan designer");
		expect(rows[0]).toHaveTextContent("ao-5");
		expect(rows[0]).toHaveTextContent("Investigation 25 min");
		expect(rows[1]).toHaveTextContent("Plan reviewer");
		expect(rows[1]).toHaveTextContent("Investigation 40 s");
		expect(rows[2]).toHaveTextContent("Worker");
		expect(rows[2]).toHaveTextContent("Tool calls 42 · reads 12 · searches 8 · commands 10 · edits 12");
		expect(within(rows[1]).getByTestId("lado-session-computed")).toHaveTextContent("Computed");
		expect(within(rows[1]).getByTestId("lado-session-computed")).toHaveAttribute("title", expect.stringContaining("Not stored yet"));
		expect(within(rows[0]).queryByTestId("lado-session-computed")).not.toBeInTheDocument();
		expect(within(rows[2]).queryByTestId("lado-session-computed")).not.toBeInTheDocument();
	});

	it("says so when LADO has no stages or sessions", async () => {
		vi.mocked(lado.fetchLadoTaskTimings).mockResolvedValue(
			timings({ timeline: { points: [], totalMs: 0, planRequests: 0, revisions: 0, approvals: 0, runs: 0 }, sessions: [] }),
		);
		renderWithClient(<LadoTaskTimings taskId="lado-task-1" />);

		expect(await screen.findByText("LADO has no times for this task yet.")).toBeInTheDocument();
		expect(screen.getByText("No plan or run session yet.")).toBeInTheDocument();
		expect(screen.queryByTestId("lado-timing-point")).not.toBeInTheDocument();
		expect(screen.queryByTestId("lado-timings-total")).not.toBeInTheDocument();
		expect(screen.queryByTestId("lado-timings-counts")).not.toBeInTheDocument();
	});

	it("shows why the timings could not be read", async () => {
		vi.mocked(lado.fetchLadoTaskTimings).mockRejectedValue(new lado.LadoApiError("No task", "LADO_TASK_NOT_FOUND"));
		renderWithClient(<LadoTaskTimings taskId="lado-task-1" />);

		expect(await screen.findByRole("alert")).toBeInTheDocument();
		expect(screen.queryByTestId("lado-task-timings")).not.toBeInTheDocument();
	});
});

describe("LadoTaskDialog Timings tab", () => {
	it("reads the task's timings only once the tab opens", async () => {
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
		vi.mocked(lado.fetchLadoTaskTimings).mockResolvedValue(timings());
		renderWithClient(<LadoTaskDialog projectId="demo" taskId="lado-task-1" repositories={[]} onOpenChange={() => undefined} />);

		const dialog = await screen.findByTestId("lado-task-dialog");
		await within(dialog).findByText("Body");
		expect(lado.fetchLadoTaskTimings).not.toHaveBeenCalled();

		await user.click(within(dialog).getByRole("tab", { name: "Timings" }));
		expect(await within(dialog).findByTestId("lado-task-timings")).toBeInTheDocument();
		expect(lado.fetchLadoTaskTimings).toHaveBeenCalledWith("lado-task-1");
	});
});
