import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import * as lado from "../../lib/lado";
import { TooltipProvider } from "../ui/tooltip";
import { LadoLanes } from "./LadoLanes";

const { navigate } = vi.hoisted(() => ({ navigate: vi.fn() }));

vi.mock("@tanstack/react-router", async (original) => ({
	...await original<typeof import("@tanstack/react-router")>(),
	useNavigate: () => navigate,
}));

vi.mock("../../lib/lado", async (original) => ({
	...await original<typeof import("../../lib/lado")>(),
	fetchLadoTasks: vi.fn(),
	fetchLadoProjectRepositories: vi.fn(),
	cancelLadoTask: vi.fn(),
	fetchLadoTaskView: vi.fn(),
	fetchLadoTaskParts: vi.fn(),
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
	vi.mocked(lado.fetchLadoTaskParts).mockResolvedValue({ parts: [] });
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
	const review = screen.getByTestId("lado-plan-review-lane");
	expect(within(review).getByText("0")).toBeInTheDocument();
	expect(within(review).getByText("Tasks wait here from the plan request until you approve the plan.")).toBeInTheDocument();
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

test("shows rules that need review and blocked tasks in the Plan review lane", async () => {
	const approvedPlan = { requestPending: false, latest: revision, approvedRevisionId: revision.id };
	vi.mocked(lado.fetchLadoTasks).mockResolvedValue([
		{ ...task, id: "lado-task-review", state: "review_required", plan: approvedPlan, issue: { ...task.issue, title: "Rules changed" } },
		{ ...task, id: "lado-task-blocked", state: "blocked", plan: approvedPlan, createdAt: "2026-10-19T08:00:00Z", issue: { ...task.issue, title: "Blocked run" } },
		{ ...task, id: "lado-task-approved", state: "queued", plan: approvedPlan, createdAt: "2026-10-19T07:00:00Z", issue: { ...task.issue, title: "Ship it" } },
	]);
	renderLane();

	const lane = await screen.findByTestId("lado-plan-review-lane");
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

test("a starting task stays in Queued in the working color, and a failed run comes back to Plan review", async () => {
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

	const review = screen.getByTestId("lado-plan-review-lane");
	const failed = within(review).getByText("Timed out").closest("[data-testid='lado-task-card']") as HTMLElement;
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

	const lane = await screen.findByTestId("lado-plan-review-lane");
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

	const lane = await screen.findByTestId("lado-plan-review-lane");
	await within(lane).findByText("Rules changed");
	for (const title of ["Rules changed", "Blocked run"]) {
		const card = within(lane).getByText(title).closest("[data-testid='lado-task-card']") as HTMLElement;
		const state = within(card).getByTestId("lado-task-state");
		expect(state, title).toHaveTextContent("Plan requested");
		expect(state.previousElementSibling, title).toHaveClass("bg-[var(--color-status-idle)]");
	}
});

/** A task titled and identified by its id, created at 09:00 unless said otherwise. */
function laneTask(id: string, state: lado.LadoTask["state"], plan: lado.LadoTask["plan"], fields: Partial<lado.LadoTask> = {}): lado.LadoTask {
	return { ...task, id, state, plan, ...fields, issue: { ...task.issue, title: id, ...fields.issue } };
}

const laneIds = (testId: string) => within(screen.getByTestId(testId)).queryAllByTestId("lado-task-card").map((card) => card.getAttribute("data-task-id"));
const laneCount = (testId: string) => within(screen.getByTestId(testId)).getByText(/^\d+$/, { selector: "span.text-passive" }).textContent;

// Issue #30: Plan holds the drafts, Plan review the tasks from the plan
// request until the approval, Queued the approved ones.
test("each lane holds its tasks: drafts in Plan, plans on their way or to review in Plan review, approvals in Queued", async () => {
	const approvedPlan = { requestPending: false, latest: revision, approvedRevisionId: revision.id };
	const worker = (phase: lado.LadoPlanWorkerPhase): NonNullable<lado.LadoTask["plan"]["worker"]> => ({ requestId: `r-${phase}`, phase });
	vi.mocked(lado.fetchLadoTasks).mockResolvedValue([
		// Plan: no request, a request that failed, and an issue closed before any run.
		laneTask("plan-draft", "draft", { requestPending: false }),
		laneTask("plan-request-error", "draft", { requestPending: false, requestError: "the orchestrator has ended" }),
		laneTask("plan-worker-failed", "draft", { requestPending: false, worker: { ...worker("failed"), failureCode: "plan_worker_timed_out" } }),
		laneTask("plan-closed", "closed_unmerged", { requestPending: true }, { createdAt: "2026-10-19T10:00:00Z", issue: { ...task.issue, state: "done" } }),
		// Plan review: a plan on its way, a plan to review, and approvals that need the human again.
		laneTask("review-requested", "draft", { requestPending: true, requestedAt: "2026-10-19T09:10:00Z" }),
		laneTask("review-waiting", "draft", { requestPending: true, worker: { ...worker("waiting"), waitingReason: "plan_worker_capacity" } }),
		laneTask("review-designing", "draft", { requestPending: true, worker: { ...worker("designing"), designerSessionId: "demo-plan-1" } }),
		laneTask("review-critiquing", "draft", { requestPending: true, worker: { ...worker("critiquing"), review: "running" } }),
		laneTask("review-ready", "planned", { requestPending: false, latest: revision }),
		laneTask("review-invalid", "planned", { requestPending: false, latest: { ...revision, valid: false } }),
		laneTask("review-fixing", "planned", { requestPending: false, latest: { ...revision, valid: false }, worker: worker("fixing") }),
		laneTask("review-rules", "review_required", approvedPlan, { stateReason: "rules_changed" }),
		laneTask("review-blocked", "blocked", approvedPlan, { stateReason: "base_ref_mismatch" }),
		laneTask("review-blocked-revising", "blocked", { ...approvedPlan, requestPending: true, worker: worker("revising") }, { stateReason: "base_ref_mismatch" }),
		laneTask("review-failed", "failed", approvedPlan, { stateReason: "attempts_exhausted" }),
		// Queued: approvals LADO starts next, and a task running its parts.
		laneTask("queued-queued", "queued", approvedPlan, { queue: entry({ enqueuedAt: "2026-10-19T09:30:00Z" }) }),
		laneTask("queued-dispatching", "dispatching", approvedPlan, { queue: entry({ state: "dispatching", enqueuedAt: "2026-10-19T09:31:00Z" }) }),
		laneTask("queued-approved", "approved", approvedPlan, { queue: entry({ enqueuedAt: "2026-10-19T09:32:00Z" }) }),
		laneTask("queued-parts", "running", approvedPlan, { parts: { total: 3, merged: 1 }, queue: entry({ state: "running_parts", enqueuedAt: "2026-10-19T09:33:00Z" }) }),
	]);
	renderLane();

	await screen.findByText("plan-draft");
	expect(new Set(laneIds("lado-plan-lane"))).toEqual(new Set(["plan-draft", "plan-request-error", "plan-worker-failed", "plan-closed"]));
	expect(laneIds("lado-plan-lane").at(-1)).toBe("plan-closed");
	expect(laneCount("lado-plan-lane")).toBe("4");
	for (const id of ["plan-request-error", "plan-worker-failed"]) {
		expect(within(screen.getByText(id).closest("[data-testid='lado-task-card']") as HTMLElement).getByTestId("lado-task-state"), id).toHaveTextContent("Plan request failed");
	}
	expect(new Set(laneIds("lado-plan-review-lane"))).toEqual(new Set([
		"review-requested",
		"review-waiting",
		"review-designing",
		"review-critiquing",
		"review-ready",
		"review-invalid",
		"review-fixing",
		"review-rules",
		"review-blocked",
		"review-blocked-revising",
		"review-failed",
	]));
	expect(laneCount("lado-plan-review-lane")).toBe("11");
	expect(laneIds("lado-queued-lane")).toEqual(["queued-queued", "queued-dispatching", "queued-approved", "queued-parts"]);
	expect(laneCount("lado-queued-lane")).toBe("4");
});

// The plans ready for approval first, then the oldest request first; a task
// with no request time counts from its creation.
test("the Plan review lane lists plans ready for review first, then the oldest request first", async () => {
	const at = (time: string) => `2026-10-19T${time}:00Z`;
	const late = { createdAt: at("12:00") };
	vi.mocked(lado.fetchLadoTasks).mockResolvedValue([
		laneTask("draft-0830", "draft", { requestPending: true, requestedAt: at("08:30") }, late),
		laneTask("ready-10", "planned", { requestPending: false, requestedAt: at("10:00"), latest: revision }, late),
		laneTask("invalid-0845", "planned", { requestPending: false, requestedAt: at("08:45"), latest: { ...revision, valid: false } }, late),
		laneTask("failed-08", "failed", { requestPending: false, requestedAt: at("08:00"), latest: revision, approvedRevisionId: revision.id }, late),
		laneTask("fixing-06", "planned", { requestPending: false, requestedAt: at("06:00"), latest: { ...revision, valid: false }, worker: { requestId: "r1", phase: "fixing" } }, late),
		laneTask("rules-07", "review_required", { requestPending: false, latest: revision, approvedRevisionId: revision.id }, { createdAt: at("07:00") }),
		laneTask("ready-09", "planned", { requestPending: false, requestedAt: "2026-10-19T09:00:00.5Z", latest: revision }, late),
	]);
	renderLane();

	await screen.findByText("ready-09");
	expect(laneIds("lado-plan-review-lane")).toEqual(["ready-09", "ready-10", "fixing-06", "rules-07", "failed-08", "draft-0830", "invalid-0845"]);
	for (const id of ["ready-09", "ready-10"]) {
		expect(within(screen.getByText(id).closest("[data-testid='lado-task-card']") as HTMLElement).getByTestId("lado-task-state"), id).toHaveTextContent("Plan ready for review");
	}
	expect(within(screen.getByText("fixing-06").closest("[data-testid='lado-task-card']") as HTMLElement).getByTestId("lado-task-state")).toHaveTextContent("Plan worker writing");
});

test("the lanes read Plan, Plan review, then Queued", async () => {
	vi.mocked(lado.fetchLadoTasks).mockResolvedValue([]);
	renderLane();

	await screen.findByText("Create an issue from New task, or import open issues.");
	expect(screen.getAllByRole("region").map((lane) => lane.getAttribute("aria-label"))).toEqual(["Plan lane", "Plan review lane", "Queued lane"]);
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

// LADO fork: plan workers (docs/LJY-PRD/TDD.md §5.1).
test("a plan worker's card says what it does and links the session that works now", async () => {
	vi.mocked(lado.fetchLadoTasks).mockResolvedValue([
		{ ...task, plan: { requestPending: true, worker: { requestId: "r1", phase: "designing", designerSessionId: "demo-plan-1" } } },
		{
			...task,
			id: "lado-task-2",
			createdAt: "2026-10-19T08:00:00Z",
			issue: { ...task.issue, ref: "github:acme/demo#13", number: 13, title: "Fix the build", url: "https://github.com/acme/demo/issues/13" },
			plan: {
				requestPending: true,
				worker: { requestId: "r2", phase: "critiquing", review: "running", designerSessionId: "demo-plan-2", criticSessionId: "demo-plan-3" },
			},
		},
		{
			...task,
			id: "lado-task-3",
			createdAt: "2026-10-19T07:00:00Z",
			issue: { ...task.issue, ref: "github:acme/demo#14", number: 14, title: "Tidy the docs", url: "https://github.com/acme/demo/issues/14" },
			plan: { requestPending: true, worker: { requestId: "r3", phase: "waiting", waitingReason: "plan_worker_capacity" } },
		},
	]);
	renderLane();

	const lane = await screen.findByTestId("lado-plan-review-lane");
	await within(lane).findByText("Add login");
	// The lane orders cards by their plan request, so they are found by id.
	const card = (id: string) => within(lane).getAllByTestId("lado-task-card").find((item) => item.getAttribute("data-task-id") === id) as HTMLElement;
	const [designing, critiquing, waiting] = [card("lado-task-1"), card("lado-task-2"), card("lado-task-3")];
	expect(within(designing).getByTestId("lado-task-state")).toHaveTextContent("Plan worker writing");
	expect(within(designing).getByTestId("lado-task-reason")).toHaveTextContent("Designing the plan");
	expect(within(critiquing).getByTestId("lado-task-reason")).toHaveTextContent("Reviewing the draft");
	expect(within(waiting).getByTestId("lado-task-state")).toHaveTextContent("Waiting for a plan worker");
	expect(within(waiting).getByTestId("lado-task-reason")).toHaveTextContent("Every plan worker slot is in use");
	// No session before the designer starts.
	expect(within(waiting).queryByRole("button", { name: /Open (plan worker|critic)/ })).not.toBeInTheDocument();

	// The link opens the session, not the task view.
	fireEvent.click(within(designing).getByRole("button", { name: "Open plan worker" }));
	expect(navigate).toHaveBeenCalledWith({ to: "/projects/$projectId/sessions/$sessionId", params: { projectId: "demo", sessionId: "demo-plan-1" } });
	expect(lado.fetchLadoTaskView).not.toHaveBeenCalled();
	fireEvent.click(within(critiquing).getByRole("button", { name: "Open critic" }));
	expect(navigate).toHaveBeenLastCalledWith({ to: "/projects/$projectId/sessions/$sessionId", params: { projectId: "demo", sessionId: "demo-plan-3" } });
});

test("opening a card shows the task view", async () => {
	vi.mocked(lado.fetchLadoTasks).mockResolvedValue([task]);
	vi.mocked(lado.fetchLadoTaskView).mockResolvedValue({
		task,
		issue: { title: "Add login", body: "Use the plan below.", state: "open", url: task.issue.url },
		comments: [],
		issueChangedSincePlan: false,
		history: { requests: [], revisions: [], approvals: [], critiques: [] },
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

test("a task running its parts stays in Queued and says how many are merged", async () => {
	const approvedPlan = { requestPending: false, latest: revision, approvedRevisionId: revision.id };
	vi.mocked(lado.fetchLadoTasks).mockResolvedValue([
		{ ...task, id: "lado-task-parts", state: "running", plan: approvedPlan, parts: { total: 4, merged: 2 }, issue: { ...task.issue, title: "Split work" } },
	]);
	renderLane();
	const queued = await screen.findByTestId("lado-queued-lane");
	await within(queued).findByText("Split work");
	expect(within(queued).getByTestId("lado-task-state")).toHaveTextContent("Running");
	expect(within(queued).getByTestId("lado-task-reason")).toHaveTextContent("Parts 2/4");
	expect(within(screen.getByTestId("lado-plan-lane")).queryByText("Split work")).not.toBeInTheDocument();
});

// Issue #61: a task approved again after an earlier run ended. The daemon may
// still send that earlier run as the execution; the queue entry says the parts run.
test("a parts run stays in Queued whatever run came before it, and a started worker still leaves the lanes", async () => {
	const approvedPlan = { requestPending: false, latest: revision, approvedRevisionId: revision.id };
	const earlier: lado.LadoTaskExecution = { sessionId: "demo-3", planRevisionId: "lado-plan-0", startedAt: "2026-10-18T08:30:00Z", sessionEnded: true, timedOut: false };
	const integration: lado.LadoTaskExecution = { sessionId: "demo-12", planRevisionId: revision.id, startedAt: "2026-10-19T10:30:00Z", sessionEnded: false, timedOut: false };
	vi.mocked(lado.fetchLadoTasks).mockResolvedValue([
		{ ...task, id: "lado-task-again", state: "running", plan: approvedPlan, parts: { total: 6, merged: 1 }, queue: entry({ state: "running_parts" }), execution: earlier, issue: { ...task.issue, title: "Approved again" } },
		// One worker, no parts: its session card carries it.
		{ ...task, id: "lado-task-single", state: "running", plan: approvedPlan, queue: entry({ state: "dispatched" }), execution: { ...integration, sessionId: "demo-7" }, issue: { ...task.issue, title: "Single run" } },
		// Every part merged and the integration worker started.
		{ ...task, id: "lado-task-integrating", state: "running", plan: approvedPlan, parts: { total: 3, merged: 3 }, queue: entry({ state: "dispatched" }), execution: integration, issue: { ...task.issue, title: "Integration started" } },
	]);
	renderLane();

	const queued = await screen.findByTestId("lado-queued-lane");
	const card = (await within(queued).findByText("Approved again")).closest("[data-testid='lado-task-card']") as HTMLElement;
	expect(within(card).getByTestId("lado-task-state")).toHaveTextContent("Running");
	expect(within(card).getByTestId("lado-task-reason")).toHaveTextContent("Parts 1/6");
	expect(within(queued).getAllByTestId("lado-task-card").map((item) => item.getAttribute("data-task-id"))).toEqual(["lado-task-again"]);
	expect(within(screen.getByTestId("lado-plan-lane")).queryAllByTestId("lado-task-card")).toHaveLength(0);
	for (const title of ["Single run", "Integration started"]) expect(screen.queryByText(title), title).not.toBeInTheDocument();
});

/** A part as the parts API sends it; waiting unless said otherwise. */
function part(id: string, fields: Partial<lado.LadoTaskPart> = {}): lado.LadoTaskPart {
	return { id, title: id.toUpperCase(), phase: 1, steps: [1], affectedPaths: [`${id}/**`], dependsOn: [], state: "waiting", attempts: 0, mergeAttempts: 0, updatedAt: "2026-10-19T10:00:00Z", ...fields };
}

const partsTask: lado.LadoTask = {
	...task,
	id: "lado-task-parts",
	state: "running",
	plan: { requestPending: false, latest: revision, approvedRevisionId: revision.id },
	parts: { total: 3, merged: 1 },
	queue: entry({ state: "running_parts" }),
	issue: { ...task.issue, title: "Split work" },
};

const sessionRoute = (sessionId: string) => ({ to: "/projects/$projectId/sessions/$sessionId", params: { projectId: "demo", sessionId } });

test("a parts run's card opens its one live part worker, not the task view", async () => {
	vi.mocked(lado.fetchLadoTasks).mockResolvedValue([partsTask]);
	vi.mocked(lado.fetchLadoTaskParts).mockResolvedValue({ parts: [
		part("api", { state: "merged", sessionId: "demo-8" }),
		part("web", { state: "running", sessionId: "demo-9" }),
		part("docs"),
	] });
	renderLane();

	const link = await screen.findByRole("button", { name: "Open part worker web" });
	expect(link).toHaveTextContent("Open part worker");
	expect(lado.fetchLadoTaskParts).toHaveBeenCalledWith("lado-task-parts");
	fireEvent.click(link);
	expect(navigate).toHaveBeenCalledWith(sessionRoute("demo-9"));
	expect(lado.fetchLadoTaskView).not.toHaveBeenCalled();
});

test("a parts run's card lists its live part and merge workers in a menu", async () => {
	const user = userEvent.setup();
	vi.mocked(lado.fetchLadoTasks).mockResolvedValue([partsTask]);
	vi.mocked(lado.fetchLadoTaskParts).mockResolvedValue({ parts: [
		part("api", { state: "running", sessionId: "demo-8" }),
		part("web", { state: "conflict", sessionId: "demo-9", mergeSessionId: "demo-11", mergeAttempts: 1 }),
		part("docs", { state: "merged", sessionId: "demo-10" }),
	] });
	renderLane();

	await user.click(await screen.findByRole("button", { name: "Part workers 2" }));
	expect((await screen.findAllByRole("menuitem")).map((item) => item.textContent)).toEqual(["api · API", "web · WEB · Merge worker"]);
	await user.click(screen.getByRole("menuitem", { name: "web · WEB · Merge worker" }));
	expect(navigate).toHaveBeenCalledWith(sessionRoute("demo-11"));
	expect(lado.fetchLadoTaskView).not.toHaveBeenCalled();
	expect(screen.queryByTestId("lado-task-dialog")).not.toBeInTheDocument();
});

test("a parts run's card links no worker while none runs", async () => {
	vi.mocked(lado.fetchLadoTasks).mockResolvedValue([partsTask]);
	vi.mocked(lado.fetchLadoTaskParts).mockResolvedValue({ parts: [
		part("api", { state: "merged", sessionId: "demo-8" }),
		// A conflict whose merge worker has not started yet.
		part("web", { state: "conflict", sessionId: "demo-9" }),
		part("docs"),
	] });
	renderLane();

	const card = (await screen.findByText("Split work")).closest("[data-testid='lado-task-card']") as HTMLElement;
	await waitFor(() => expect(lado.fetchLadoTaskParts).toHaveBeenCalledWith("lado-task-parts"));
	await waitFor(() => expect(within(card).getByTestId("lado-task-reason")).toHaveTextContent("Parts 1/3"));
	expect(within(card).queryByRole("button", { name: /Open part worker|Part workers/ })).not.toBeInTheDocument();
});

test("a card not running parts does not read the parts", async () => {
	vi.mocked(lado.fetchLadoTasks).mockResolvedValue([
		{ ...task, id: "lado-task-queued", state: "queued", plan: partsTask.plan, parts: { total: 3, merged: 0 }, queue: entry(), issue: { ...task.issue, title: "Next" } },
		{ ...task, plan: { requestPending: true, worker: { requestId: "r1", phase: "designing", designerSessionId: "demo-plan-1" } } },
	]);
	renderLane();

	await screen.findByText("Next");
	await screen.findByRole("button", { name: "Open plan worker" });
	expect(lado.fetchLadoTaskParts).not.toHaveBeenCalled();
});
