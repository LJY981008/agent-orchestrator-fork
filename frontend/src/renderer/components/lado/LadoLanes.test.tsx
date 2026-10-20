import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import * as lado from "../../lib/lado";
import { TooltipProvider } from "../ui/tooltip";
import { LadoPlanLane } from "./LadoPlanLane";

vi.mock("../../lib/lado", async (original) => ({
	...await original<typeof import("../../lib/lado")>(),
	fetchLadoTasks: vi.fn(),
	fetchLadoProjectRepositories: vi.fn(),
	cancelLadoTask: vi.fn(),
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
	createdAt: "2026-10-19T09:00:00Z",
};

function renderLane() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<TooltipProvider delayDuration={0}>
				<LadoPlanLane projectId="demo" />
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
	expect(screen.getByText("0")).toBeInTheDocument();
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
