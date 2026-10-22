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
		{ ...task, id: "lado-task-approved", state: "approved", plan: { requestPending: false, latest: revision, approvedRevisionId: revision.id }, issue: { ...task.issue, title: "Ship it" } },
	]);
	renderLane();

	const queued = await screen.findByTestId("lado-queued-lane");
	await within(queued).findByText("Ship it");
	expect(within(queued).getByText("Queued")).toBeInTheDocument();
	expect(within(queued).getByTestId("lado-task-state")).toHaveTextContent("Approved");
	expect(within(screen.getByTestId("lado-plan-lane")).queryByText("Ship it")).not.toBeInTheDocument();
});

test("shows rules that need review and blocked tasks in the Plan lane", async () => {
	const approvedPlan = { requestPending: false, latest: revision, approvedRevisionId: revision.id };
	vi.mocked(lado.fetchLadoTasks).mockResolvedValue([
		{ ...task, id: "lado-task-review", state: "review_required", plan: approvedPlan, issue: { ...task.issue, title: "Rules changed" } },
		{ ...task, id: "lado-task-blocked", state: "blocked", plan: approvedPlan, createdAt: "2026-10-19T08:00:00Z", issue: { ...task.issue, title: "Blocked run" } },
		{ ...task, id: "lado-task-approved", state: "approved", plan: approvedPlan, createdAt: "2026-10-19T07:00:00Z", issue: { ...task.issue, title: "Ship it" } },
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
