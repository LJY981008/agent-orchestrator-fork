import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import * as lado from "../../lib/lado";
import { TooltipProvider } from "../ui/tooltip";
import { LadoLanes } from "./LadoLanes";

vi.mock("../../lib/lado", async (original) => ({
	...await original<typeof import("../../lib/lado")>(),
	fetchLadoTasks: vi.fn(),
	fetchLadoProjectRepositories: vi.fn(),
	cancelLadoTask: vi.fn(),
	fetchLadoTaskView: vi.fn(),
}));

const demo: lado.LadoRepository = { ref: "github:acme/demo", name: "demo", provider: "github", host: "github.com", path: "acme/demo" };

const task: lado.LadoTask = {
	id: "lado-task-1",
	projectId: "demo",
	repository: "github:acme/demo",
	state: "draft",
	issue: {
		ref: "github:acme/demo#12",
		number: 12,
		title: "Add login",
		state: "open",
		url: "https://github.com/acme/demo/issues/12",
		observedAt: "2026-10-19T09:00:00Z",
	},
	plan: { requestPending: false },
	createdAt: "2026-10-19T09:00:00Z",
};

/** A queue entry as the task list sends it; approved at 09:30 unless said otherwise. */
function entry(fields: Partial<lado.LadoTaskQueue> = {}): lado.LadoTaskQueue {
	return { state: "queued", attempts: 0, enqueuedAt: "2026-10-19T09:30:00Z", ...fields };
}

function renderLane() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<TooltipProvider delayDuration={0}>
				<LadoLanes projectId="demo" />
			</TooltipProvider>
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	vi.resetAllMocks();
	vi.mocked(lado.fetchLadoProjectRepositories).mockResolvedValue([demo]);
});

afterEach(cleanup);

test("lists the project's tasks with their issue number and state", async () => {
	vi.mocked(lado.fetchLadoTasks).mockResolvedValue([task, {
		...task,
		id: "lado-task-2",
		state: "closed_unmerged",
		issue: { ...task.issue, ref: "github:acme/demo#13", number: 13, title: "Fix the build", state: "done", url: "https://github.com/acme/demo/issues/13" },
	}]);
	renderLane();

	const lane = await screen.findByTestId("lado-plan-lane");
	await within(lane).findByText("Add login");
	expect(lado.fetchLadoTasks).toHaveBeenCalledWith("demo");
	expect(within(lane).getByText("Plan")).toBeInTheDocument();
	expect(within(lane).getByText("2")).toBeInTheDocument();
	const cards = within(lane).getAllByTestId("lado-task-card");
	expect(within(cards[0]).getByRole("link", { name: "Open issue #12" })).toHaveAttribute("href", "https://github.com/acme/demo/issues/12");
	expect(within(cards[0]).getByTestId("lado-task-state")).toHaveTextContent("Draft");
	expect(within(cards[1]).getByTestId("lado-task-state")).toHaveTextContent("Issue closed");
});

test("names each issue with its repository in a workspace", async () => {
	vi.mocked(lado.fetchLadoProjectRepositories).mockResolvedValue([
		{ ref: "github:acme/back", name: "back", provider: "github", host: "github.com", path: "acme/back" },
		{ ref: "github:acme/front", name: "front", provider: "github", host: "github.com", path: "acme/front" },
	]);
	vi.mocked(lado.fetchLadoTasks).mockResolvedValue([
		{ ...task, repository: "github:acme/back", issue: { ...task.issue, ref: "github:acme/back#12", url: "https://github.com/acme/back/issues/12" } },
		{ ...task, id: "lado-task-2", repository: "github:acme/front", createdAt: "2026-10-19T08:00:00Z", issue: { ...task.issue, ref: "github:acme/front#12", title: "Fix the form", url: "https://github.com/acme/front/issues/12" } },
	]);
	renderLane();

	const cards = await screen.findAllByTestId("lado-task-card");
	await within(cards[0]).findByRole("link", { name: "Open issue back#12" });
	expect(within(cards[0]).getByText("back#12")).toBeInTheDocument();
	expect(within(cards[1]).getByRole("link", { name: "Open issue front#12" })).toHaveAttribute("href", "https://github.com/acme/front/issues/12");
});

test("orders the lane newest first, with closed issues last", async () => {
	const closed: lado.LadoTask = { ...task, id: "lado-task-closed", state: "closed_unmerged", createdAt: "2026-10-19T11:00:00Z", issue: { ...task.issue, title: "Closed newest", state: "done" } };
	const older: lado.LadoTask = { ...task, id: "lado-task-older", createdAt: "2026-10-18T09:00:00Z", issue: { ...task.issue, title: "Older draft" } };
	const newer: lado.LadoTask = { ...task, id: "lado-task-newer", createdAt: "2026-10-19T10:00:00Z", issue: { ...task.issue, title: "Newer draft" } };
	vi.mocked(lado.fetchLadoTasks).mockResolvedValue([closed, older, newer]);
	renderLane();

	await screen.findByText("Newer draft");
	expect(screen.getAllByTestId("lado-task-card").map((card) => card.getAttribute("data-task-id"))).toEqual([
		"lado-task-newer",
		"lado-task-older",
		"lado-task-closed",
	]);
});

test("an empty lane says where tasks come from", async () => {
	vi.mocked(lado.fetchLadoTasks).mockResolvedValue([]);
	renderLane();

	expect(await screen.findByText("Create an issue from New task, or import open issues.")).toBeInTheDocument();
	expect(within(screen.getByTestId("lado-plan-lane")).getByText("0")).toBeInTheDocument();
	expect(within(screen.getByTestId("lado-queued-lane")).getByText("Approved tasks wait here until LADO starts their work.")).toBeInTheDocument();
});

const revision: NonNullable<lado.LadoTask["plan"]["latest"]> = {
	id: "lado-plan-1",
	number: 1,
	sha256: "a".repeat(64),
	source: "agent",
	valid: true,
	createdAt: "2026-10-19T09:30:00Z",
};

test("approved tasks wait in the Queued lane", async () => {
	vi.mocked(lado.fetchLadoTasks).mockResolvedValue([
		task,
		{ ...task, id: "lado-task-approved", state: "queued", plan: { requestPending: false, latest: revision, approvedRevisionId: revision.id }, queue: entry(), issue: { ...task.issue, title: "Ship it" } },
	]);
	renderLane();

	const queued = await screen.findByTestId("lado-queued-lane");
	await within(queued).findByText("Ship it");
	expect(within(queued).getByText("Queued", { selector: "span.text-xs" })).toBeInTheDocument();
	const state = within(queued).getByTestId("lado-task-state");
	expect(state).toHaveTextContent("Queued");
	// Approved and in line: nothing waits for the human.
	expect(state.previousElementSibling).toHaveClass("bg-[var(--color-status-ready)]");
	// Nothing tried yet and nothing to wait for: no second line.
	expect(within(queued).queryByTestId("lado-task-reason")).not.toBeInTheDocument();
	expect(within(screen.getByTestId("lado-plan-lane")).queryByText("Ship it")).not.toBeInTheDocument();
});

test("shows rules that need review and blocked tasks in the Plan lane", async () => {
	const approvedPlan = { requestPending: false, latest: revision, approvedRevisionId: revision.id };
	vi.mocked(lado.fetchLadoTasks).mockResolvedValue([
		{ ...task, id: "lado-task-review", state: "review_required", plan: approvedPlan, issue: { ...task.issue, title: "Rules changed" } },
		{ ...task, id: "lado-task-blocked", state: "blocked", plan: approvedPlan, createdAt: "2026-10-19T08:00:00Z", issue: { ...task.issue, title: "Blocked run" } },
		{ ...task, id: "lado-task-approved", state: "queued", plan: approvedPlan, createdAt: "2026-10-19T07:00:00Z", issue: { ...task.issue, title: "Ship it" } },
	]);
	renderLane();

	const lane = await screen.findByTestId("lado-plan-lane");
	await within(lane).findByText("Rules changed");
	for (const [title, label] of [["Rules changed", "Rules need review"], ["Blocked run", "Blocked"]]) {
		const card = within(lane).getByText(title).closest("[data-testid='lado-task-card']") as HTMLElement;
		const state = within(card).getByTestId("lado-task-state");
		expect(state, title).toHaveTextContent(label);
		// One dot and one line, in the needs-you color: the human acts next.
		expect(state.previousElementSibling, title).toHaveClass("bg-[var(--color-status-needs-you)]");
	}
	const queued = screen.getByTestId("lado-queued-lane");
	expect(within(queued).getAllByTestId("lado-task-card").map((card) => card.getAttribute("data-task-id"))).toEqual(["lado-task-approved"]);
});

test("a starting task stays in Queued in the working color, and a failed run comes back to Plan", async () => {
	const approvedPlan = { requestPending: false, latest: revision, approvedRevisionId: revision.id };
	vi.mocked(lado.fetchLadoTasks).mockResolvedValue([
		{ ...task, id: "lado-task-starting", state: "dispatching", plan: approvedPlan, queue: entry({ state: "dispatching", attempts: 2 }), issue: { ...task.issue, title: "Starting" } },
		{
			...task,
			id: "lado-task-failed",
			state: "failed",
			stateReason: "timed_out",
			plan: approvedPlan,
			createdAt: "2026-10-19T08:00:00Z",
			execution: { sessionId: "demo-7", planRevisionId: revision.id, startedAt: "2026-10-19T08:30:00Z", sessionEnded: true, timedOut: true },
			issue: { ...task.issue, title: "Timed out" },
		},
	]);
	renderLane();

	const queued = await screen.findByTestId("lado-queued-lane");
	const starting = (await within(queued).findByText("Starting", { selector: "span.text-sm-md" })).closest("[data-testid='lado-task-card']") as HTMLElement;
	expect(within(starting).getByTestId("lado-task-state")).toHaveTextContent("Starting");
	expect(within(starting).getByTestId("lado-task-state").previousElementSibling).toHaveClass("bg-[var(--color-status-working)]");
	expect(within(starting).getByTestId("lado-task-reason")).toHaveTextContent("Attempt 2 of 3");

	const plan = screen.getByTestId("lado-plan-lane");
	const failed = within(plan).getByText("Timed out").closest("[data-testid='lado-task-card']") as HTMLElement;
	expect(within(failed).getByTestId("lado-task-state")).toHaveTextContent("Failed");
	expect(within(failed).getByTestId("lado-task-state").previousElementSibling).toHaveClass("bg-[var(--color-status-needs-you)]");
	expect(within(failed).getByTestId("lado-task-reason")).toHaveTextContent("Stopped at the maximum run time");
});

test("the second line says what a queued task waits for and which attempt starts it", async () => {
	const approvedPlan = { requestPending: false, latest: revision, approvedRevisionId: revision.id };
	const cases: [string, lado.LadoTask["stateReason"], lado.LadoTaskQueue, string][] = [
		["slot", "slot_global", entry({ state: "waiting", reason: "slot_global" }), "Waiting for a free run slot"],
		["project slot", "slot_project", entry({ state: "waiting", reason: "slot_project" }), "Waiting for a free run slot in this project"],
		["memory", "memory_tight", entry({ state: "waiting", reason: "memory_tight" }), "Waiting while memory is tight"],
		["issue", "issue_unreadable", entry({ state: "waiting", reason: "issue_unreadable", nextAttemptAt: new Date(Date.now() + 60_000).toISOString() }), "Waiting to read the issue again"],
		["mode off", "lado_mode_off", entry({ state: "waiting", reason: "lado_mode_off" }), "Waiting until LADO mode is on"],
		["retry due", "retry", entry({ state: "waiting", attempts: 2, reason: "retry" }), "Waiting to try again · Attempt 2 of 3"],
		["held after a try", "slot_global", entry({ state: "waiting", attempts: 1, reason: "slot_global" }), "Waiting for a free run slot · Attempt 1 of 3"],
		// The task's worker of an earlier approval still runs; LADO looks again in a minute.
		["earlier run", "run_live", entry({ state: "waiting", reason: "run_live", nextAttemptAt: new Date(Date.now() + 60_000).toISOString() }), "An earlier run of this task is still going"],
	];
	vi.mocked(lado.fetchLadoTasks).mockResolvedValue(cases.map(([name, stateReason, queue], index) => ({
		...task,
		id: `lado-task-${index}`,
		state: "queued",
		stateReason,
		queue,
		plan: approvedPlan,
		createdAt: `2026-10-19T0${9 - index}:00:00Z`,
		issue: { ...task.issue, title: name },
	})));
	renderLane();

	const lane = await screen.findByTestId("lado-queued-lane");
	await within(lane).findByText("slot");
	for (const [name, , , line] of cases) {
		const card = within(lane).getByText(name).closest("[data-testid='lado-task-card']") as HTMLElement;
		expect(within(card).getByTestId("lado-task-state"), name).toHaveTextContent("Queued");
		// The time of a project slot or an issue read is the next look, not a retry.
		expect(within(card).getByTestId("lado-task-reason"), name).toHaveTextContent(line);
	}
});

// The line names the clock time of the next try: a relative time would stand
// still on a board nobody refreshes.
test("a retry names the clock time it tries again at", async () => {
	vi.useFakeTimers({ toFake: ["Date"] });
	try {
		vi.setSystemTime(new Date(2026, 9, 26, 14, 30));
		const approvedPlan = { requestPending: false, latest: revision, approvedRevisionId: revision.id };
		vi.mocked(lado.fetchLadoTasks).mockResolvedValue([{
			...task,
			id: "lado-task-retry",
			state: "queued",
			stateReason: "retry",
			queue: entry({ state: "waiting", attempts: 1, reason: "retry", nextAttemptAt: new Date(2026, 9, 26, 14, 32).toISOString() }),
			plan: approvedPlan,
			issue: { ...task.issue, title: "Retry" },
		}]);
		renderLane();

		const card = (await screen.findByText("Retry")).closest("[data-testid='lado-task-card']") as HTMLElement;
		expect(within(card).getByTestId("lado-task-reason")).toHaveTextContent("Trying again at 02:32 PM · Attempt 1 of 3");
	} finally {
		vi.useRealTimers();
	}
});

// The dispatcher takes the oldest approval first, so the lane shows them in
// that order, whatever order the issues came in.
test("the Queued lane lists tasks in the order LADO starts them", async () => {
	const approvedPlan = { requestPending: false, latest: revision, approvedRevisionId: revision.id };
	const queued = (id: string, createdAt: string, enqueuedAt: string): lado.LadoTask => ({
		...task,
		id,
		state: "queued",
		plan: approvedPlan,
		createdAt,
		queue: entry({ enqueuedAt }),
		issue: { ...task.issue, title: id },
	});
	vi.mocked(lado.fetchLadoTasks).mockResolvedValue([
		queued("lado-task-newest-issue", "2026-10-19T09:00:00Z", "2026-10-19T10:00:00.5Z"),
		queued("lado-task-first-approved", "2026-10-18T09:00:00Z", "2026-10-19T09:30:00Z"),
		queued("lado-task-second-approved", "2026-10-17T09:00:00Z", "2026-10-19T10:00:00Z"),
	]);
	renderLane();

	const lane = await screen.findByTestId("lado-queued-lane");
	await within(lane).findByText("lado-task-first-approved");
	expect(within(lane).getAllByTestId("lado-task-card").map((card) => card.getAttribute("data-task-id"))).toEqual([
		"lado-task-first-approved",
		"lado-task-second-approved",
		"lado-task-newest-issue",
	]);
});

test("the second line says why an approval needs the human again", async () => {
	const approvedPlan = { requestPending: false, latest: revision, approvedRevisionId: revision.id };
	const cases: [string, lado.LadoTask["state"], lado.LadoTask["stateReason"], string, string | null][] = [
		["rules", "review_required", "rules_changed", "Rules need review", null],
		["issue", "review_required", "issue_edited", "Needs approval again", "The issue changed since the plan was made"],
		["before queue", "review_required", "approved_before_queue", "Needs approval again", "Not in the queue — approve again to queue it"],
		["base", "blocked", "base_ref_mismatch", "Blocked", "The plan's base is not the branch workers start from"],
		["no start", "failed", "attempts_exhausted", "Failed", "The worker did not start after 3 attempts"],
	];
	vi.mocked(lado.fetchLadoTasks).mockResolvedValue(cases.map(([name, state, stateReason], index) => ({
		...task,
		id: `lado-task-${index}`,
		state,
		stateReason,
		plan: approvedPlan,
		createdAt: `2026-10-19T0${9 - index}:00:00Z`,
		issue: { ...task.issue, title: name },
	})));
	renderLane();

	const lane = await screen.findByTestId("lado-plan-lane");
	await within(lane).findByText("rules");
	for (const [name, , , label, line] of cases) {
		const card = within(lane).getByText(name).closest("[data-testid='lado-task-card']") as HTMLElement;
		expect(within(card).getByTestId("lado-task-state"), name).toHaveTextContent(label);
		expect(within(card).getByTestId("lado-task-state").previousElementSibling, name).toHaveClass("bg-[var(--color-status-needs-you)]");
		if (line) expect(within(card).getByTestId("lado-task-reason"), name).toHaveTextContent(line);
		else expect(within(card).queryByTestId("lado-task-reason"), name).not.toBeInTheDocument();
	}
});

test("a task whose worker started leaves the lanes; its session card carries it", async () => {
	const approvedPlan = { requestPending: false, latest: revision, approvedRevisionId: revision.id };
	const execution: lado.LadoTaskExecution = { sessionId: "demo-7", planRevisionId: revision.id, startedAt: "2026-10-19T08:30:00Z", sessionEnded: false, timedOut: false };
	vi.mocked(lado.fetchLadoTasks).mockResolvedValue([
		{ ...task, id: "lado-task-running", state: "running", plan: approvedPlan, execution, issue: { ...task.issue, title: "Running" } },
		// A run whose pull request LADO verifies or integrates is on its session card too.
		{ ...task, id: "lado-task-validating", state: "validating", plan: approvedPlan, execution: { ...execution, sessionId: "demo-5" }, issue: { ...task.issue, title: "Validating" } },
		{ ...task, id: "lado-task-pending", state: "integration_pending", plan: approvedPlan, execution: { ...execution, sessionId: "demo-6" }, issue: { ...task.issue, title: "Pending" } },
		{ ...task, id: "lado-task-integrating", state: "integrating", plan: approvedPlan, execution: { ...execution, sessionId: "demo-4" }, issue: { ...task.issue, title: "Integrating" } },
		{ ...task, id: "lado-task-merged", state: "completed", plan: approvedPlan, execution: { ...execution, sessionId: "demo-8", sessionEnded: true }, issue: { ...task.issue, title: "Merged" } },
		{ ...task, id: "lado-task-unmerged", state: "closed_unmerged", plan: approvedPlan, execution: { ...execution, sessionId: "demo-9", sessionEnded: true }, issue: { ...task.issue, title: "Closed PR", state: "done" } },
		// Closed before any run: still the human's to cancel.
		{ ...task, id: "lado-task-closed", state: "closed_unmerged", createdAt: "2026-10-19T08:00:00Z", issue: { ...task.issue, title: "Closed issue", state: "done" } },
		{ ...task, id: "lado-task-queued", state: "queued", plan: approvedPlan, createdAt: "2026-10-19T07:00:00Z", issue: { ...task.issue, title: "Next" } },
	]);
	renderLane();

	const plan = await screen.findByTestId("lado-plan-lane");
	await within(plan).findByText("Closed issue");
	const queued = screen.getByTestId("lado-queued-lane");
	expect(within(plan).getAllByTestId("lado-task-card").map((card) => card.getAttribute("data-task-id"))).toEqual(["lado-task-closed"]);
	expect(within(queued).getAllByTestId("lado-task-card").map((card) => card.getAttribute("data-task-id"))).toEqual(["lado-task-queued"]);
	expect(within(plan).getByText("1")).toBeInTheDocument();
	for (const title of ["Running", "Validating", "Pending", "Integrating", "Merged", "Closed PR"]) expect(screen.queryByText(title), title).not.toBeInTheDocument();
});

test("shows plan requested for a task whose rules need review", async () => {
	// Changes were requested since: the task waits for the new plan, not for the human.
	const requested = { requestPending: true, requestedAt: "2026-10-19T09:10:00Z", latest: revision, approvedRevisionId: revision.id };
	vi.mocked(lado.fetchLadoTasks).mockResolvedValue([
		{ ...task, id: "lado-task-review", state: "review_required", plan: requested, issue: { ...task.issue, title: "Rules changed" } },
		{ ...task, id: "lado-task-blocked", state: "blocked", plan: requested, createdAt: "2026-10-19T08:00:00Z", issue: { ...task.issue, title: "Blocked run" } },
	]);
	renderLane();

	const lane = await screen.findByTestId("lado-plan-lane");
	await within(lane).findByText("Rules changed");
	for (const title of ["Rules changed", "Blocked run"]) {
		const card = within(lane).getByText(title).closest("[data-testid='lado-task-card']") as HTMLElement;
		const state = within(card).getByTestId("lado-task-state");
		expect(state, title).toHaveTextContent("Plan requested");
		expect(state.previousElementSibling, title).toHaveClass("bg-[var(--color-status-idle)]");
	}
});

test("the status line follows the task's plans", async () => {
	const cases: [string, lado.LadoTask["state"], lado.LadoTask["plan"], string][] = [
		["draft", "draft", { requestPending: false }, "Draft"],
		["requested", "draft", { requestPending: true, requestedAt: "2026-10-19T09:10:00Z" }, "Plan requested"],
		["request failed", "draft", { requestPending: false, requestError: "the orchestrator has ended" }, "Plan request failed"],
		["ready for review", "planned", { requestPending: false, latest: revision }, "Plan ready for review"],
		["with problems", "planned", { requestPending: false, latest: { ...revision, valid: false } }, "Plan has problems"],
		["change requested", "planned", { requestPending: true, latest: revision }, "Plan requested"],
	];
	vi.mocked(lado.fetchLadoTasks).mockResolvedValue(cases.map(([name, state, plan], index) => ({
		...task,
		id: `lado-task-${index}`,
		state,
		plan,
		createdAt: `2026-10-19T0${9 - index}:00:00Z`,
		issue: { ...task.issue, title: name },
	})));
	renderLane();

	await screen.findByText("draft");
	for (const [name, , , label] of cases) {
		const card = screen.getByText(name).closest("[data-testid='lado-task-card']") as HTMLElement;
		expect(within(card).getByTestId("lado-task-state"), name).toHaveTextContent(label);
	}
});

test("opening a card shows the task view", async () => {
	vi.mocked(lado.fetchLadoTasks).mockResolvedValue([task]);
	vi.mocked(lado.fetchLadoTaskView).mockResolvedValue({
		task,
		issue: { title: "Add login", body: "Use the plan below.", state: "open", url: task.issue.url },
		comments: [],
		issueChangedSincePlan: false,
		history: { requests: [], revisions: [], approvals: [] },
	});
	renderLane();
	fireEvent.click(await screen.findByRole("button", { name: "Open task #12" }));

	const dialog = await screen.findByTestId("lado-task-dialog");
	await within(dialog).findByText("Use the plan below.");
	expect(lado.fetchLadoTaskView).toHaveBeenCalledWith("lado-task-1");
	expect(within(dialog).getByRole("button", { name: "Request a plan" })).toBeEnabled();
});

test("cancelling a task asks first, then cancels it and refreshes the lane", async () => {
	vi.mocked(lado.fetchLadoTasks).mockResolvedValue([task]);
	vi.mocked(lado.cancelLadoTask).mockResolvedValue({ ...task, state: "cancelled", cancelledAt: "2026-10-19T10:00:00Z" });
	renderLane();
	await screen.findByText("Add login");

	fireEvent.click(screen.getByRole("button", { name: "Cancel task" }));
	const dialog = await screen.findByRole("dialog");
	expect(within(dialog).getByText("Cancel this task?")).toBeInTheDocument();
	expect(lado.cancelLadoTask).not.toHaveBeenCalled();

	fireEvent.click(within(dialog).getByRole("button", { name: "Cancel task" }));
	await waitFor(() => expect(lado.cancelLadoTask).toHaveBeenCalledWith("lado-task-1"));
	await waitFor(() => expect(lado.fetchLadoTasks).toHaveBeenCalledTimes(2));
	await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
});

test("a failed cancel keeps the dialog open with the reason", async () => {
	vi.mocked(lado.fetchLadoTasks).mockResolvedValue([task]);
	vi.mocked(lado.cancelLadoTask).mockRejectedValue(new lado.LadoApiError("Unknown task", "LADO_TASK_NOT_FOUND"));
	renderLane();
	await screen.findByText("Add login");

	fireEvent.click(screen.getByRole("button", { name: "Cancel task" }));
	const dialog = await screen.findByRole("dialog");
	fireEvent.click(within(dialog).getByRole("button", { name: "Cancel task" }));
	expect(await within(dialog).findByText("This task no longer exists.")).toBeInTheDocument();
});
