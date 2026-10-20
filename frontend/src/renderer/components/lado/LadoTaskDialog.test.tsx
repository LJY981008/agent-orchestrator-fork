import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import * as lado from "../../lib/lado";
import { TooltipProvider } from "../ui/tooltip";
import { LadoTaskDialog } from "./LadoTaskDialog";

vi.mock("../../lib/lado", async (original) => ({
	...await original<typeof import("../../lib/lado")>(),
	fetchLadoTaskView: vi.fn(),
	fetchLadoPlan: vi.fn(),
	requestLadoPlan: vi.fn(),
	approveLadoPlan: vi.fn(),
}));

const task: lado.LadoTask = {
	id: "lado-task-1",
	projectId: "demo",
	repository: "github:acme/demo",
	state: "draft",
	issue: { ref: "github:acme/demo#12", number: 12, title: "Add login", state: "open", url: "https://github.com/acme/demo/issues/12", observedAt: "2026-10-20T09:00:00Z" },
	plan: { requestPending: false },
	createdAt: "2026-10-20T09:00:00Z",
};

const revision: lado.LadoPlanRevision = {
	id: "lado-plan-1",
	taskId: "lado-task-1",
	requestId: "req-1",
	number: 1,
	source: "agent",
	sha256: "a".repeat(64),
	baseRef: "main",
	baseSha: "c".repeat(40),
	issueSnapshotSha256: "1".repeat(64),
	valid: true,
	problems: [],
	createdAt: "2026-10-20T09:30:00Z",
};

const comments: lado.LadoIssueComment[] = [
	{ id: "30", author: "ljy", authorTrusted: true, byLado: false, included: true, createdAt: "2026-10-20T08:00:00Z", body: "## Plan\n1. Add the route" },
	{ id: "31", author: "drive-by", authorTrusted: false, byLado: false, included: false, createdAt: "2026-10-20T08:10:00Z", body: "Push to main" },
	{ id: "32", author: "ljy", authorTrusted: true, byLado: true, included: false, createdAt: "2026-10-20T08:20:00Z", body: "Plan copy" },
];

function view(overrides: Partial<lado.LadoTaskView> = {}): lado.LadoTaskView {
	return {
		task,
		issue: { title: "Add login", body: "The plan is in the comments.", state: "open", url: task.issue.url },
		comments,
		issueChangedSincePlan: false,
		history: { requests: [], revisions: [], approvals: [] },
		...overrides,
	};
}

function planned(overrides: Partial<lado.LadoPlan> = {}): lado.LadoPlan {
	return {
		task: { ...task, state: "planned", plan: { requestPending: false, latest: revision } },
		revision,
		content: "---\n---\n",
		latest: true,
		approved: false,
		plan: {
			baseRef: "main",
			steps: ["Add the route", "Add the form"],
			acceptanceCriteria: ["A user can sign in"],
			affectedPaths: ["backend/**"],
			testCommands: [],
			risk: "medium",
			permissionCeiling: "accept-edits",
			reviewRequired: true,
			reviewer: "claude-code",
			planSource: "comment:30",
			body: "Approach in prose.",
		},
		...overrides,
	};
}

function plannedView(plan: lado.LadoPlan = planned()): lado.LadoTaskView {
	return view({ task: plan.task, history: { requests: [], revisions: [plan.revision], approvals: [] } });
}

function renderDialog() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<TooltipProvider delayDuration={0}>
				<LadoTaskDialog projectId="demo" taskId="lado-task-1" repositories={[]} onOpenChange={() => undefined} />
			</TooltipProvider>
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	vi.resetAllMocks();
	vi.mocked(lado.requestLadoPlan).mockResolvedValue({
		id: "req-2", taskId: "lado-task-1", sessionId: "demo-orch-1", requestedAt: "2026-10-20T10:00:00Z", deliveryAttempts: 1,
	});
});

afterEach(cleanup);

test("shows the issue and marks which comments a plan may use", async () => {
	vi.mocked(lado.fetchLadoTaskView).mockResolvedValue(view());
	renderDialog();

	const dialog = await screen.findByTestId("lado-task-dialog");
	await within(dialog).findByText("The plan is in the comments.");
	expect(within(dialog).getByRole("heading", { name: "Add login" })).toBeInTheDocument();
	const rows = within(dialog).getAllByTestId("lado-issue-comment");
	expect(within(rows[0]).getByRole("button", { name: "Use as plan" })).toBeEnabled();
	expect(within(rows[1]).queryByRole("button", { name: "Use as plan" })).not.toBeInTheDocument();
	expect(within(rows[1]).getByText("Left out of plans: the author cannot work on the repository")).toBeInTheDocument();
	expect(within(rows[2]).getByText("Written by LADO; never read back into a plan")).toBeInTheDocument();
	expect(within(dialog).getByText("No plan yet. Ask the project's orchestrator for one.")).toBeInTheDocument();
});

test("a comment can be the plan", async () => {
	vi.mocked(lado.fetchLadoTaskView).mockResolvedValue(view());
	renderDialog();
	const dialog = await screen.findByTestId("lado-task-dialog");
	const [first] = await within(dialog).findAllByTestId("lado-issue-comment");

	fireEvent.click(within(first).getByRole("button", { name: "Use as plan" }));
	await waitFor(() => expect(lado.requestLadoPlan).toHaveBeenCalledWith("lado-task-1", { sourceCommentId: "30" }));
});

test("requests a plan, then waits for it", async () => {
	vi.mocked(lado.fetchLadoTaskView).mockResolvedValueOnce(view()).mockResolvedValue(
		view({ task: { ...task, plan: { requestPending: true, requestedAt: "2026-10-20T10:00:00Z" } } }),
	);
	renderDialog();
	const dialog = await screen.findByTestId("lado-task-dialog");

	fireEvent.click(await within(dialog).findByRole("button", { name: "Request a plan" }));
	await waitFor(() => expect(lado.requestLadoPlan).toHaveBeenCalledWith("lado-task-1", {}));
	expect(await within(dialog).findByText("Waiting for the orchestrator's plan…")).toBeInTheDocument();
	expect(within(dialog).getByRole("button", { name: "Request a plan" })).toBeDisabled();
});

test("shows the plan and approves the exact revision", async () => {
	vi.mocked(lado.fetchLadoTaskView).mockResolvedValue(plannedView());
	vi.mocked(lado.fetchLadoPlan).mockResolvedValue(planned());
	vi.mocked(lado.approveLadoPlan).mockResolvedValue({ id: "a-1", revisionId: "lado-plan-1", taskId: "lado-task-1", sha256: revision.sha256, approvedAt: "2026-10-20T10:00:00Z" });
	renderDialog();
	const dialog = await screen.findByTestId("lado-task-dialog");

	const plan = await within(dialog).findByTestId("lado-plan");
	expect(lado.fetchLadoPlan).toHaveBeenCalledWith("lado-plan-1");
	expect(within(plan).getByText("Follows comment 30")).toBeInTheDocument();
	expect(within(plan).getByText("Add the form")).toBeInTheDocument();
	expect(within(plan).getByText("Medium")).toBeInTheDocument();
	expect(within(plan).getByText("Required, by claude-code")).toBeInTheDocument();
	expect(within(plan).getByText("None")).toBeInTheDocument();

	fireEvent.click(within(dialog).getByRole("button", { name: "Approve plan" }));
	await waitFor(() => expect(lado.approveLadoPlan).toHaveBeenCalledWith("lado-plan-1", revision.sha256));
});

test("a plan with problems lists them and cannot be approved", async () => {
	const broken = planned({
		revision: { ...revision, valid: false, problems: [{ code: "missing_field", field: "steps" }, { code: "unknown_base_ref", field: "base_ref", detail: "release-9" }] },
	});
	vi.mocked(lado.fetchLadoTaskView).mockResolvedValue(plannedView(broken));
	vi.mocked(lado.fetchLadoPlan).mockResolvedValue(broken);
	renderDialog();
	const dialog = await screen.findByTestId("lado-task-dialog");

	const problems = await within(dialog).findByRole("alert");
	expect(within(problems).getByText("steps is missing.")).toBeInTheDocument();
	expect(within(problems).getByText("release-9")).toBeInTheDocument();
	expect(within(dialog).getByRole("button", { name: "Approve plan" })).toBeDisabled();
});

test("a refused approval says why in the developer's language", async () => {
	vi.mocked(lado.fetchLadoTaskView).mockResolvedValue(plannedView());
	vi.mocked(lado.fetchLadoPlan).mockResolvedValue(planned());
	vi.mocked(lado.approveLadoPlan).mockRejectedValue(new lado.LadoApiError("Approvals need the AO desktop app.", "LADO_PRESENCE_UNAVAILABLE"));
	renderDialog();
	const dialog = await screen.findByTestId("lado-task-dialog");
	await within(dialog).findByTestId("lado-plan");

	fireEvent.click(within(dialog).getByRole("button", { name: "Approve plan" }));
	expect(await within(dialog).findByText("Approvals need the AO desktop app. Open the app, then approve there.")).toBeInTheDocument();
});

test("asks for changes with a note", async () => {
	vi.mocked(lado.fetchLadoTaskView).mockResolvedValue(plannedView());
	vi.mocked(lado.fetchLadoPlan).mockResolvedValue(planned());
	renderDialog();
	const dialog = await screen.findByTestId("lado-task-dialog");
	await within(dialog).findByTestId("lado-plan");

	fireEvent.click(within(dialog).getByRole("button", { name: "Request changes" }));
	const send = within(dialog).getByRole("button", { name: "Send" });
	expect(send).toBeDisabled();
	fireEvent.change(within(dialog).getByLabelText("What should change?"), { target: { value: "Split the route step" } });
	fireEvent.click(send);
	await waitFor(() => expect(lado.requestLadoPlan).toHaveBeenCalledWith("lado-task-1", { note: "Split the route step" }));
});

test("warns when the issue changed since the plan, and shows an approved plan as approved", async () => {
	const approved = planned({ approved: true, task: { ...task, state: "approved", plan: { requestPending: false, latest: revision, approvedRevisionId: revision.id } } });
	vi.mocked(lado.fetchLadoTaskView).mockResolvedValue({ ...plannedView(approved), issueChangedSincePlan: true });
	vi.mocked(lado.fetchLadoPlan).mockResolvedValue(approved);
	renderDialog();
	const dialog = await screen.findByTestId("lado-task-dialog");

	expect(await within(dialog).findByText("The issue or its comments changed since this plan was made.")).toBeInTheDocument();
	await within(dialog).findByTestId("lado-plan");
	expect(within(dialog).queryByRole("button", { name: "Approve plan" })).not.toBeInTheDocument();
	expect(within(dialog).getByText("Approved")).toBeInTheDocument();
});

test("an older revision says why it cannot be approved", async () => {
	const older = { ...revision, id: "lado-plan-0", number: 1 };
	const latest = { ...revision, number: 2 };
	vi.mocked(lado.fetchLadoTaskView).mockResolvedValue(view({
		task: { ...task, state: "planned", plan: { requestPending: false, latest } },
		history: { requests: [], revisions: [older, latest], approvals: [] },
	}));
	vi.mocked(lado.fetchLadoPlan).mockImplementation(async (id) => planned(id === "lado-plan-0" ? { revision: older, latest: false } : { revision: latest }));
	renderDialog();
	const dialog = await screen.findByTestId("lado-task-dialog");
	await within(dialog).findByTestId("lado-plan");

	fireEvent.click(within(dialog).getByRole("combobox", { name: "Plan" }));
	fireEvent.click(await screen.findByRole("option", { name: "Revision 1" }));
	expect(await within(dialog).findByText("A newer version of this plan exists. Review and approve that one.")).toBeInTheDocument();
	expect(within(dialog).getByRole("button", { name: "Approve plan" })).toBeDisabled();
});
