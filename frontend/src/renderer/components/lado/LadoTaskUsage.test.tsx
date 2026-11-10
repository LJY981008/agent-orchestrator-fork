import { act, cleanup, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import * as lado from "../../lib/lado";
import { TooltipProvider } from "../ui/tooltip";
import { LadoTaskDialog } from "./LadoTaskDialog";
import { LadoTaskUsage } from "./LadoTaskUsage";

vi.mock("../../lib/lado", async (original) => ({
	...(await original<typeof import("../../lib/lado")>()),
	fetchLadoTasks: vi.fn(),
	fetchLadoTaskView: vi.fn(),
	fetchLadoTaskTimings: vi.fn(),
	fetchLadoTaskUsage: vi.fn(),
	fetchLadoAlerts: vi.fn(),
	markLadoAlertSeen: vi.fn(),
	fetchLadoAccountLimits: vi.fn(),
}));

const DOLLAR = 1_000_000_000;
const MINUTE = 60_000;

function totals(change: Partial<lado.LadoUsageTotals> = {}): lado.LadoUsageTotals {
	return {
		costNanos: 0,
		inputTokens: 0,
		cachedInputTokens: 0,
		outputTokens: 0,
		subagentCostNanos: 0,
		subagentInputTokens: 0,
		subagentOutputTokens: 0,
		...change,
	};
}

function session(change: Partial<lado.LadoSessionUsage> = {}): lado.LadoSessionUsage {
	return {
		sessionId: "demo-7",
		role: "worker",
		attempt: 1,
		compactions: 0,
		contextUsed: 0,
		contextWindow: 0,
		incomplete: false,
		usageMissing: false,
		totals: totals(),
		...change,
	};
}

function usage(change: Partial<lado.LadoTaskUsageSummary> = {}, sessions?: lado.LadoSessionUsage[]): lado.LadoTaskUsage {
	return {
		task: {
			taskId: "lado-task-1",
			title: "Add login",
			issueRef: "github:acme/demo#12",
			startedAt: "2026-11-03T09:00:00Z",
			endedAt: "2026-11-03T11:00:00Z",
			endReason: "in_progress",
			inProgress: true,
			elapsedMs: 125 * MINUTE,
			compactions: 4,
			incomplete: false,
			sessionsMissing: 0,
			totals: totals({
				costNanos: 42.5 * DOLLAR,
				inputTokens: 3_000_000,
				cachedInputTokens: 2_500_000,
				outputTokens: 200_000,
				subagentCostNanos: 21.25 * DOLLAR,
			}),
			...change,
		},
		sessions: sessions ?? [
			session({ sessionId: "demo-5", role: "designer", attempt: 1, totals: totals({ costNanos: 2 * DOLLAR, inputTokens: 90_000, outputTokens: 10_000 }) }),
			session({ sessionId: "demo-6", role: "designer", attempt: 0, usageMissing: true }),
			session({
				sessionId: "demo-8",
				role: "part",
				partId: "store",
				attempt: 2,
				compactions: 3,
				contextUsed: 150_000,
				contextWindow: 1_000_000,
				totals: totals({ costNanos: 40.5 * DOLLAR, inputTokens: 2_910_000, outputTokens: 190_000, subagentCostNanos: 21.25 * DOLLAR }),
			}),
		],
	};
}

function alert(change: Partial<lado.LadoAlert> = {}): lado.LadoAlert {
	const id = change.id ?? 7;
	return {
		id,
		projectId: "demo",
		taskId: "lado-task-1",
		sessionId: "",
		issueRef: "github:acme/demo#12",
		kind: "task_cost",
		level: 1,
		value: 42_50,
		threshold: 40_00,
		line: "LADO 알림",
		notificationId: `lado-alert:${id}`,
		seenAt: null,
		createdAt: "2026-11-03T10:30:00Z",
		...change,
	};
}

function renderWithClient(node: React.ReactNode) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
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
	vi.mocked(lado.fetchLadoAlerts).mockResolvedValue([]);
});

afterEach(() => {
	cleanup();
	vi.useRealTimers();
});

describe("LadoTaskUsage", () => {
	it("shows the totals, the estimate note and each session's cost under the Timings tab's role labels", async () => {
		vi.mocked(lado.fetchLadoTaskUsage).mockResolvedValue(usage());
		renderWithClient(<LadoTaskUsage projectId="demo" taskId="lado-task-1" />);

		await screen.findByTestId("lado-task-usage");
		expect(lado.fetchLadoTaskUsage).toHaveBeenCalledWith("lado-task-1");
		expect(screen.getByTestId("lado-usage-cost")).toHaveTextContent("$42.50");
		expect(screen.getByTestId("lado-usage-tokens")).toHaveTextContent("3.2M");
		expect(screen.getByTestId("lado-usage-subagents")).toHaveTextContent("$21.25");
		expect(screen.getByText("50% of the cost")).toBeInTheDocument();
		expect(screen.getByTestId("lado-usage-elapsed")).toHaveTextContent("2 h 5 min");
		expect(screen.getByTestId("lado-usage-end")).toHaveTextContent("In progress");
		expect(screen.getByTestId("lado-usage-estimate-note")).toHaveTextContent(
			"Estimated at API prices; the actual bill (subscription) can differ.",
		);

		const rows = screen.getAllByTestId("lado-session-usage");
		expect(rows.map((row) => row.dataset.role)).toEqual(["designer", "designer", "part"]);
		expect(rows[0]).toHaveTextContent("Plan designer");
		expect(rows[0]).toHaveTextContent("$2.00");
		expect(within(rows[1]).getByTestId("lado-session-usage-missing")).toHaveTextContent("No record");
		expect(rows[2]).toHaveTextContent("Part worker");
		expect(rows[2]).toHaveTextContent("store");
		expect(rows[2]).toHaveTextContent("$40.50");
		expect(rows[2]).toHaveTextContent("150K / 1M");
		expect(within(rows[2]).getByTestId("lado-session-compactions")).toHaveTextContent("3");
		// The investigation metrics stay on the Timings tab.
		expect(screen.queryByText(/Tool calls/)).not.toBeInTheDocument();
		expect(screen.queryByText(/Investigation/)).not.toBeInTheDocument();
	});

	it("ends the elapsed time at the merge and counts the sessions AO no longer has", async () => {
		vi.mocked(lado.fetchLadoTaskUsage).mockResolvedValue(
			usage({ endReason: "merged", inProgress: false, elapsedMs: 28 * 60 * MINUTE, sessionsMissing: 1 }),
		);
		renderWithClient(<LadoTaskUsage projectId="demo" taskId="lado-task-1" />);

		expect(await screen.findByTestId("lado-usage-end")).toHaveTextContent("Intake to merge");
		expect(screen.getByTestId("lado-usage-elapsed")).toHaveTextContent("1 d 4 h");
		expect(screen.queryByText("Still running")).not.toBeInTheDocument();
		expect(screen.getByText("Sessions AO no longer has count as nothing: 1.")).toBeInTheDocument();
	});

	it("lists the task's alerts and marks them seen", async () => {
		const user = userEvent.setup();
		vi.mocked(lado.fetchLadoTaskUsage).mockResolvedValue(usage());
		vi.mocked(lado.fetchLadoAlerts).mockResolvedValue([
			alert({ id: 8, kind: "session_compactions", sessionId: "demo-8", value: 3, threshold: 3 }),
			alert({ id: 7 }),
			alert({ id: 6, kind: "stalled", value: 120, seenAt: "2026-11-03T10:00:00Z" }),
		]);
		vi.mocked(lado.markLadoAlertSeen).mockImplementation(async (id) => alert({ id, seenAt: "2026-11-03T11:00:00Z" }));
		renderWithClient(<LadoTaskUsage projectId="demo" taskId="lado-task-1" />);

		const rows = await screen.findAllByTestId("lado-usage-alert");
		expect(lado.fetchLadoAlerts).toHaveBeenCalledWith("demo", { taskId: "lado-task-1" });
		expect(rows.map((row) => row.dataset.seen)).toEqual(["false", "false", "true"]);
		expect(rows[0]).toHaveTextContent("check whether to split the parts again");
		expect(rows[1]).toHaveTextContent("Task cost estimate $42.50, past the $40 threshold");
		expect(within(rows[2]).queryByRole("button")).not.toBeInTheDocument();

		await user.click(within(rows[1]).getByRole("button", { name: /Mark seen/ }));
		expect(lado.markLadoAlertSeen).toHaveBeenCalledWith(7);
		await user.click(screen.getByRole("button", { name: "Mark all seen" }));
		await waitFor(() => expect(lado.markLadoAlertSeen).toHaveBeenCalledWith(8));
	});

	it("offers to release the account limit on the newest limited alert while a limit holds", async () => {
		vi.mocked(lado.fetchLadoTaskUsage).mockResolvedValue(usage());
		vi.mocked(lado.fetchLadoAlerts).mockResolvedValue([
			alert({ id: 9, kind: "limited", sessionId: "demo-8", value: 1_793_732_400 }),
			alert({ id: 8, kind: "limit_resumed", sessionId: "demo-8", value: 1 }),
			alert({ id: 7, kind: "limited", sessionId: "demo-8", value: 1_793_732_400, seenAt: "2026-11-03T10:00:00Z" }),
		]);
		vi.mocked(lado.fetchLadoAccountLimits).mockResolvedValue([{
			accountKey: "claude:device-claude", provider: "claude", accountId: "device-claude", active: true, identityKnown: true,
			detectedAt: "2026-11-03T10:30:00Z", resetAt: "2026-11-03T19:00:00Z", releasedWaiting: 0, resetGuessed: false, updatedAt: "2026-11-10T14:00:00Z",
		}]);
		renderWithClient(<LadoTaskUsage projectId="demo" taskId="lado-task-1" />);

		const rows = await screen.findAllByTestId("lado-usage-alert");
		expect(await within(rows[0]).findByRole("button", { name: "Release limit (I switched accounts)" })).toBeInTheDocument();
		expect(within(rows[1]).queryByTestId("lado-account-limit-release")).not.toBeInTheDocument();
		expect(within(rows[2]).queryByTestId("lado-account-limit-release")).not.toBeInTheDocument();
	});

	it("shows no limit release on a limited alert once no limit holds", async () => {
		vi.mocked(lado.fetchLadoTaskUsage).mockResolvedValue(usage());
		vi.mocked(lado.fetchLadoAlerts).mockResolvedValue([alert({ id: 9, kind: "limited", sessionId: "demo-8", value: 1_793_732_400 })]);
		vi.mocked(lado.fetchLadoAccountLimits).mockResolvedValue([]);
		renderWithClient(<LadoTaskUsage projectId="demo" taskId="lado-task-1" />);

		await screen.findByTestId("lado-usage-alert");
		await waitFor(() => expect(lado.fetchLadoAccountLimits).toHaveBeenCalled());
		expect(screen.queryByTestId("lado-account-limit-release")).not.toBeInTheDocument();
	});

	it("reads no account limits without a limited alert", async () => {
		vi.mocked(lado.fetchLadoTaskUsage).mockResolvedValue(usage());
		vi.mocked(lado.fetchLadoAlerts).mockResolvedValue([alert({ id: 7 })]);
		renderWithClient(<LadoTaskUsage projectId="demo" taskId="lado-task-1" />);

		await screen.findByTestId("lado-usage-alert");
		expect(lado.fetchLadoAccountLimits).not.toHaveBeenCalled();
	});

	it("reads the usage again every minute while it shows", async () => {
		vi.useFakeTimers({ shouldAdvanceTime: true });
		vi.mocked(lado.fetchLadoTaskUsage).mockResolvedValue(usage());
		const { unmount } = renderWithClient(<LadoTaskUsage projectId="demo" taskId="lado-task-1" />);

		await screen.findByTestId("lado-task-usage");
		expect(lado.fetchLadoTaskUsage).toHaveBeenCalledTimes(1);
		await act(async () => {
			await vi.advanceTimersByTimeAsync(60_000);
		});
		await waitFor(() => expect(lado.fetchLadoTaskUsage).toHaveBeenCalledTimes(2));

		unmount();
		await act(async () => {
			await vi.advanceTimersByTimeAsync(120_000);
		});
		expect(lado.fetchLadoTaskUsage).toHaveBeenCalledTimes(2);
	});

	it("shows why the usage could not be read", async () => {
		vi.mocked(lado.fetchLadoTaskUsage).mockRejectedValue(new lado.LadoApiError("No task", "LADO_TASK_NOT_FOUND"));
		renderWithClient(<LadoTaskUsage projectId="demo" taskId="lado-task-1" />);

		expect(await screen.findByRole("alert")).toHaveTextContent("This task no longer exists.");
		expect(screen.queryByTestId("lado-task-usage")).not.toBeInTheDocument();
	});
});

describe("LadoTaskDialog Usage tab", () => {
	const task: lado.LadoTask = {
		id: "lado-task-1",
		projectId: "demo",
		repository: "github:acme/demo",
		state: "draft",
		issue: { ref: "github:acme/demo#12", number: 12, title: "Add login", state: "open", url: "https://github.com/acme/demo/issues/12", observedAt: "2026-10-20T09:00:00Z" },
		plan: { requestPending: false },
		createdAt: "2026-10-20T09:00:00Z",
	};

	beforeEach(() => {
		vi.mocked(lado.fetchLadoTasks).mockResolvedValue([]);
		vi.mocked(lado.fetchLadoTaskView).mockResolvedValue({
			task,
			issue: { title: "Add login", body: "Body", state: "open", url: task.issue.url },
			comments: [],
			issueChangedSincePlan: false,
			history: { requests: [], revisions: [], approvals: [], critiques: [] },
		});
		vi.mocked(lado.fetchLadoTaskUsage).mockResolvedValue(usage());
	});

	it("is its own tab next to Timings and reads the usage only once it opens", async () => {
		const user = userEvent.setup();
		renderWithClient(<LadoTaskDialog projectId="demo" taskId="lado-task-1" repositories={[]} onOpenChange={() => undefined} />);

		const dialog = await screen.findByTestId("lado-task-dialog");
		await within(dialog).findByText("Body");
		const tabs = within(dialog).getAllByRole("tab").map((tab) => tab.textContent);
		expect(tabs.slice(-2)).toEqual(["Timings", "Usage"]);
		expect(lado.fetchLadoTaskUsage).not.toHaveBeenCalled();

		await user.click(within(dialog).getByRole("tab", { name: "Usage" }));
		expect(await within(dialog).findByTestId("lado-task-usage")).toBeInTheDocument();
		expect(within(dialog).queryByTestId("lado-task-timings")).not.toBeInTheDocument();
		expect(lado.fetchLadoTaskTimings).not.toHaveBeenCalled();
	});

	it("opens on the Usage tab when asked to", async () => {
		renderWithClient(
			<LadoTaskDialog projectId="demo" taskId="lado-task-1" repositories={[]} initialTab="usage" onOpenChange={() => undefined} />,
		);

		const dialog = await screen.findByTestId("lado-task-dialog");
		expect(await within(dialog).findByTestId("lado-task-usage")).toBeInTheDocument();
		expect(within(dialog).getByRole("tab", { name: "Usage" })).toHaveAttribute("aria-selected", "true");
	});
});
