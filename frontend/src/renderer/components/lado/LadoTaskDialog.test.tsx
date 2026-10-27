import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import * as lado from "../../lib/lado";
import { TooltipProvider } from "../ui/tooltip";
import { LadoTaskDialog } from "./LadoTaskDialog";

const { navigate } = vi.hoisted(() => ({ navigate: vi.fn() }));

vi.mock("@tanstack/react-router", async (original) => ({
	...await original<typeof import("@tanstack/react-router")>(),
	useNavigate: () => navigate,
}));

vi.mock("../../lib/lado", async (original) => ({
	...await original<typeof import("../../lib/lado")>(),
	fetchLadoTasks: vi.fn(),
	fetchLadoTaskView: vi.fn(),
	fetchLadoPlan: vi.fn(),
	requestLadoPlan: vi.fn(),
	approveLadoPlan: vi.fn(),
	fetchLadoEffective: vi.fn(),
	fetchLadoRules: vi.fn(),
	approveLadoIntegration: vi.fn(),
	mergeLadoPR: vi.fn(),
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

// The rules that apply to the task; an approval sends their snapshot hash.
function rules(snapshotSha256: string, fields: Partial<lado.LadoEffective> = {}): lado.LadoEffective {
	return {
		projectId: "demo",
		taskId: "lado-task-1",
		agent: { harness: "claude-code", model: "", source: "project" },
		permissionCeiling: "auto",
		permissionMode: "accept-edits",
		rules: [],
		aoRules: [],
		prompt: "",
		capabilities: [],
		problems: [],
		blocking: false,
		snapshotSha256,
		setup: { items: [], aoSystemSha256: "", conflicts: [], accepted: [] },
		...fields,
	};
}

const approval: lado.LadoPlanApproval = { id: "a-1", revisionId: "lado-plan-1", taskId: "lado-task-1", sha256: revision.sha256, approvedAt: "2026-10-20T10:00:00Z" };

function renderDialog(onOpenChange: (open: boolean) => void = () => undefined) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<TooltipProvider delayDuration={0}>
				<LadoTaskDialog projectId="demo" taskId="lado-task-1" repositories={[]} onOpenChange={onOpenChange} />
			</TooltipProvider>
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	vi.resetAllMocks();
	vi.mocked(lado.requestLadoPlan).mockResolvedValue({
		id: "req-2", taskId: "lado-task-1", sessionId: "demo-orch-1", requestedAt: "2026-10-20T10:00:00Z", deliveryAttempts: 1,
	});
	vi.mocked(lado.fetchLadoEffective).mockResolvedValue(rules("e".repeat(64)));
	vi.mocked(lado.fetchLadoRules).mockResolvedValue([]);
	vi.mocked(lado.fetchLadoTasks).mockResolvedValue([]);
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
	vi.mocked(lado.approveLadoPlan).mockResolvedValue(approval);
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
	await waitFor(() => expect(lado.approveLadoPlan).toHaveBeenCalledWith("lado-plan-1", revision.sha256, "e".repeat(64)));
});

test("approves with the rule snapshot hash", async () => {
	const user = userEvent.setup();
	vi.mocked(lado.fetchLadoTaskView).mockResolvedValue(plannedView());
	vi.mocked(lado.fetchLadoPlan).mockResolvedValue(planned());
	vi.mocked(lado.approveLadoPlan).mockResolvedValue(approval);
	// The Rules tab saw one snapshot; the rules changed before the approval.
	vi.mocked(lado.fetchLadoEffective).mockResolvedValueOnce(rules("d".repeat(64))).mockResolvedValue(rules("e".repeat(64)));
	renderDialog();
	const dialog = await screen.findByTestId("lado-task-dialog");
	await within(dialog).findByTestId("lado-plan");

	await user.click(within(dialog).getByRole("tab", { name: "Rules" }));
	expect(await within(dialog).findByText("Runs with Claude Code · at most Accept edits")).toBeInTheDocument();
	expect(lado.fetchLadoEffective).toHaveBeenCalledWith("demo", "lado-task-1");
	await user.click(within(dialog).getByRole("button", { name: "Approve plan" }));

	// The approval reads the rules again and sends the hash of what applies now.
	await waitFor(() => expect(lado.approveLadoPlan).toHaveBeenCalledWith("lado-plan-1", revision.sha256, "e".repeat(64)));
	expect(vi.mocked(lado.fetchLadoEffective).mock.invocationCallOrder[1]).toBeLessThan(vi.mocked(lado.approveLadoPlan).mock.invocationCallOrder[0]);
});

test("offers approval again when the rules need review", async () => {
	const approved = planned({
		approved: true,
		task: { ...task, state: "review_required", plan: { requestPending: false, latest: revision, approvedRevisionId: revision.id } },
	});
	vi.mocked(lado.fetchLadoTaskView).mockResolvedValue(plannedView(approved));
	vi.mocked(lado.fetchLadoPlan).mockResolvedValue(approved);
	vi.mocked(lado.approveLadoPlan).mockResolvedValue(approval);
	renderDialog();
	const dialog = await screen.findByTestId("lado-task-dialog");
	await within(dialog).findByTestId("lado-plan");

	expect(within(dialog).getByText("The rules or local setup changed since this plan was approved. Check the Rules tab, then approve again.")).toHaveAttribute("role", "status");
	expect(within(dialog).queryByText("Approved")).not.toBeInTheDocument();
	const approve = within(dialog).getByRole("button", { name: "Approve plan" });
	expect(approve).toBeEnabled();
	fireEvent.click(approve);
	await waitFor(() => expect(lado.approveLadoPlan).toHaveBeenCalledWith("lado-plan-1", revision.sha256, "e".repeat(64)));
});

test("does not offer approval again for a blocked task", async () => {
	const blocked = planned({
		approved: true,
		task: { ...task, state: "blocked", plan: { requestPending: false, latest: revision, approvedRevisionId: revision.id } },
	});
	vi.mocked(lado.fetchLadoTaskView).mockResolvedValue(plannedView(blocked));
	vi.mocked(lado.fetchLadoPlan).mockResolvedValue(blocked);
	renderDialog();
	const dialog = await screen.findByTestId("lado-task-dialog");
	await within(dialog).findByTestId("lado-plan");

	// Approving again would sign the same rules. Fixing the problems changes
	// them, and the task then needs review, which can be approved again.
	expect(
		within(dialog).getByText("The rules this plan was approved with block the run. Once you fix the problems on the Rules tab, you can approve it again."),
	).toHaveAttribute("role", "status");
	// Not shown as approved and ready either.
	expect(within(dialog).queryByText("Approved")).not.toBeInTheDocument();
	const approve = within(dialog).getByRole("button", { name: "Approve plan" });
	expect(approve).toBeDisabled();
	fireEvent.click(approve);
	expect(lado.fetchLadoEffective).not.toHaveBeenCalled();
	expect(lado.approveLadoPlan).not.toHaveBeenCalled();
});

test("says the rules just changed when an approval meets newer rules", async () => {
	vi.mocked(lado.fetchLadoTaskView).mockResolvedValue(plannedView());
	vi.mocked(lado.fetchLadoPlan).mockResolvedValue(planned());
	vi.mocked(lado.approveLadoPlan).mockRejectedValue(
		new lado.LadoApiError("The rules or local setup changed while you were approving. Review them again, then approve.", "LADO_RULES_CHANGED"),
	);
	renderDialog();
	const dialog = await screen.findByTestId("lado-task-dialog");
	await within(dialog).findByTestId("lado-plan");

	fireEvent.click(within(dialog).getByRole("button", { name: "Approve plan" }));
	const notice = await within(dialog).findByText("The rules or local setup just changed. Check them on the Rules tab, then approve again.");
	expect(notice).toHaveAttribute("role", "status");
	expect(within(dialog).queryByRole("alert")).not.toBeInTheDocument();
	// The rules are read again for the developer to check.
	await waitFor(() => expect(lado.fetchLadoEffective).toHaveBeenCalledTimes(2));
	expect(within(dialog).getByRole("button", { name: "Approve plan" })).toBeEnabled();
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

// An approved task, as the view and the project's task list send it.
function approvedTask(fields: Partial<lado.LadoTask>): lado.LadoTask {
	return { ...task, state: "queued", plan: { requestPending: false, latest: revision, approvedRevisionId: revision.id }, ...fields };
}

const enqueuedAt = "2026-10-20T10:00:00Z";

const execution: lado.LadoTaskExecution = {
	sessionId: "demo-7",
	planRevisionId: revision.id,
	planRevisionNumber: 1,
	policySha256: "e".repeat(64),
	startedAt: "2026-10-20T11:00:00Z",
	sessionEnded: false,
	timedOut: false,
};

/** The view as it opened, and the task as the project's task list has it now. */
function mockApproved(viewTask: lado.LadoTask, listed: lado.LadoTask = viewTask) {
	const approved = planned({ approved: true, task: viewTask });
	vi.mocked(lado.fetchLadoTaskView).mockResolvedValue(plannedView(approved));
	vi.mocked(lado.fetchLadoPlan).mockResolvedValue(approved);
	vi.mocked(lado.fetchLadoTasks).mockResolvedValue([listed]);
}

test("a running task shows its run with the session and PR, from the project's task list", async () => {
	const onOpenChange = vi.fn();
	// The view opened before the worker started; the list follows the run.
	mockApproved(approvedTask({}), approvedTask({
		state: "running",
		execution: { ...execution, pr: { url: "https://github.com/acme/demo/pull/45", number: 45, merged: false, closed: false } },
	}));
	renderDialog(onOpenChange);
	const dialog = await screen.findByTestId("lado-task-dialog");

	const run = await within(dialog).findByTestId("lado-run-status");
	expect(run).toHaveTextContent("Running");
	expect(run.querySelector("span.rounded-full")).toHaveClass("bg-[var(--color-status-working)]");
	expect(within(run).getByRole("link", { name: "Open PR #45" })).toHaveAttribute("href", "https://github.com/acme/demo/pull/45");
	expect(within(run).getByText("PR #45")).toBeInTheDocument();
	// The live region reads the run's state; the session and the PR are controls, not status text.
	const status = within(run).getByRole("status");
	expect(status).toHaveTextContent("Running");
	expect(status).not.toContainElement(within(run).getByRole("button", { name: "Open session" }));
	expect(status).not.toContainElement(within(run).getByRole("link", { name: "Open PR #45" }));
	// Approved and running: nothing to approve, no queue to show.
	expect(within(dialog).queryByRole("button", { name: "Approve plan" })).not.toBeInTheDocument();
	expect(within(dialog).queryByTestId("lado-queue-status")).not.toBeInTheDocument();
	expect(within(dialog).getByText("Approved")).toBeInTheDocument();

	fireEvent.click(within(run).getByRole("button", { name: "Open session" }));
	expect(onOpenChange).toHaveBeenCalledWith(false);
	expect(navigate).toHaveBeenCalledWith({ to: "/projects/$projectId/sessions/$sessionId", params: { projectId: "demo", sessionId: "demo-7" } });
});

test("a failed run says why and can be approved again", async () => {
	mockApproved(approvedTask({ state: "failed", stateReason: "timed_out", execution: { ...execution, sessionEnded: true, timedOut: true } }));
	vi.mocked(lado.approveLadoPlan).mockResolvedValue(approval);
	renderDialog();
	const dialog = await screen.findByTestId("lado-task-dialog");
	await within(dialog).findByTestId("lado-plan");

	const run = await within(dialog).findByTestId("lado-run-status");
	expect(run).toHaveTextContent("Failed");
	expect(run).toHaveTextContent("Stopped at the maximum run time");
	expect(run.querySelector("span.rounded-full")).toHaveClass("bg-[var(--color-status-needs-you)]");
	expect(within(run).getByRole("button", { name: "Open session" })).toBeInTheDocument();
	expect(within(dialog).queryByText("Approved")).not.toBeInTheDocument();
	const approve = within(dialog).getByRole("button", { name: "Approve plan" });
	await waitFor(() => expect(approve).toBeEnabled());
	fireEvent.click(approve);
	await waitFor(() => expect(lado.approveLadoPlan).toHaveBeenCalledWith("lado-plan-1", revision.sha256, "e".repeat(64)));
});

test("a task that failed before any worker started says why, with no session to open", async () => {
	mockApproved(approvedTask({ state: "failed", stateReason: "attempts_exhausted", queue: { state: "failed", attempts: 3, enqueuedAt, error: "LADO_SPAWN_FAILED: no agent" } }));
	renderDialog();
	const dialog = await screen.findByTestId("lado-task-dialog");

	const run = await within(dialog).findByTestId("lado-run-status");
	expect(run).toHaveTextContent("Failed");
	expect(run).toHaveTextContent("The worker did not start after 3 attempts");
	expect(within(run).queryByRole("button", { name: "Open session" })).not.toBeInTheDocument();
	// What the last attempt ran into, as the daemon put it, under the status line.
	const error = within(run).getByTestId("lado-run-error");
	expect(error).toHaveTextContent("LADO_SPAWN_FAILED: no agent");
	expect(error).toHaveAttribute("title", "LADO_SPAWN_FAILED: no agent");
	expect(error).toHaveClass("truncate", "text-settings-muted");
	expect(within(run).getByRole("status")).not.toContainElement(error);
	await within(dialog).findByTestId("lado-plan");
	expect(within(dialog).getByRole("button", { name: "Approve plan" })).toBeEnabled();
});

test("a task whose issue the tracker no longer has says so in the developer's language", async () => {
	mockApproved(approvedTask({
		state: "failed",
		stateReason: "attempts_exhausted",
		queue: { state: "failed", attempts: 1, enqueuedAt, error: "LADO_ISSUE_NOT_FOUND: the issue no longer exists on the tracker" },
	}));
	renderDialog();
	const dialog = await screen.findByTestId("lado-task-dialog");

	const error = within(await within(dialog).findByTestId("lado-run-status")).getByTestId("lado-run-error");
	expect(error).toHaveTextContent(/^The issue no longer exists on the tracker\.$/);
});

test("a failed task whose worker still runs cannot be approved, and says why", async () => {
	mockApproved(approvedTask({ state: "failed", stateReason: "timed_out", execution: { ...execution, timedOut: true } }));
	renderDialog();
	const dialog = await screen.findByTestId("lado-task-dialog");
	await within(dialog).findByTestId("lado-plan");

	await within(dialog).findByTestId("lado-run-status");
	expect(within(dialog).getByRole("button", { name: "Approve plan" })).toBeDisabled();
	expect(
		within(dialog).getByText("This task's worker still runs, LADO is verifying or integrating its work, or its work merged. LADO runs one worker per task, so no plan can be approved now."),
	).toHaveAttribute("role", "status");
});

test("a queued task shows where it waits instead of the approve button", async () => {
	mockApproved(approvedTask({ stateReason: "slot_global", queue: { state: "waiting", attempts: 1, enqueuedAt, reason: "slot_global" } }));
	renderDialog();
	const dialog = await screen.findByTestId("lado-task-dialog");
	await within(dialog).findByTestId("lado-plan");

	const queue = await within(dialog).findByTestId("lado-queue-status");
	expect(queue).toHaveTextContent("Queued");
	expect(queue).toHaveTextContent("Waiting for a free run slot · Attempt 1 of 3");
	expect(within(dialog).queryByRole("button", { name: "Approve plan" })).not.toBeInTheDocument();
	expect(within(dialog).queryByText("Approved")).not.toBeInTheDocument();
	// No run yet.
	expect(within(dialog).queryByTestId("lado-run-status")).not.toBeInTheDocument();
});

test("a starting task shows that LADO starts its worker", async () => {
	// The older run of a task approved again stays its newest execution.
	mockApproved(approvedTask({ state: "dispatching", queue: { state: "dispatching", attempts: 1, enqueuedAt }, execution: { ...execution, sessionEnded: true } }));
	renderDialog();
	const dialog = await screen.findByTestId("lado-task-dialog");
	await within(dialog).findByTestId("lado-plan");

	const queue = await within(dialog).findByTestId("lado-queue-status");
	expect(queue).toHaveTextContent("Starting");
	expect(queue.querySelector("span.rounded-full")).toHaveClass("bg-[var(--color-status-working)]");
	expect(within(dialog).queryByRole("button", { name: "Approve plan" })).not.toBeInTheDocument();
	expect(within(dialog).queryByTestId("lado-run-status")).not.toBeInTheDocument();
});

test("an approval that was never queued says so and can be approved again", async () => {
	mockApproved(approvedTask({ state: "review_required", stateReason: "approved_before_queue" }));
	renderDialog();
	const dialog = await screen.findByTestId("lado-task-dialog");
	await within(dialog).findByTestId("lado-plan");

	expect(within(dialog).getByText("Not in the queue — approve again to queue it")).toHaveAttribute("role", "status");
	// Not a rules review.
	expect(within(dialog).queryByText("The rules or local setup changed since this plan was approved. Check the Rules tab, then approve again.")).not.toBeInTheDocument();
	expect(within(dialog).getByRole("button", { name: "Approve plan" })).toBeEnabled();
});

test("a plan whose base is not the workers' base says so instead of pointing at the rules", async () => {
	mockApproved(approvedTask({ state: "blocked", stateReason: "base_ref_mismatch" }));
	renderDialog();
	const dialog = await screen.findByTestId("lado-task-dialog");
	await within(dialog).findByTestId("lado-plan");

	expect(
		within(dialog).getByText("The plan's base is not the branch workers start from. Use Request changes to have the plan fixed."),
	).toHaveAttribute("role", "status");
	expect(within(dialog).getByRole("button", { name: "Approve plan" })).toBeDisabled();
	expect(within(dialog).getByRole("button", { name: "Request changes" })).toBeEnabled();
});

test("work that closed without a merge shows the run and takes no decisions", async () => {
	mockApproved(approvedTask({
		state: "closed_unmerged",
		execution: { ...execution, sessionEnded: true, pr: { url: "https://github.com/acme/demo/pull/45", number: 45, merged: false, closed: true } },
	}));
	renderDialog();
	const dialog = await screen.findByTestId("lado-task-dialog");
	await within(dialog).findByTestId("lado-plan");

	const run = await within(dialog).findByTestId("lado-run-status");
	expect(run).toHaveTextContent("Closed without merge");
	expect(within(run).getByRole("button", { name: "Open session" })).toBeInTheDocument();
	expect(within(run).getByRole("link", { name: "Open PR #45" })).toBeInTheDocument();
	for (const name of ["Approve plan", "Request changes"]) expect(within(dialog).queryByRole("button", { name })).not.toBeInTheDocument();
});

const prHead = "1a2b3c4".padEnd(40, "0");
const openPR: lado.LadoExecutionPR = { url: "https://github.com/acme/demo/pull/45", number: 45, merged: false, closed: false, headSha: prHead };

test("a task whose PR LADO verifies says what it waits for, and offers no decision yet", async () => {
	mockApproved(approvedTask({ state: "validating", stateReason: "checks_pending", execution: { ...execution, sessionEnded: true, pr: openPR } }));
	renderDialog();
	const dialog = await screen.findByTestId("lado-task-dialog");
	await within(dialog).findByTestId("lado-plan");

	const run = await within(dialog).findByTestId("lado-run-status");
	expect(within(run).getByRole("status")).toHaveTextContent("Validating · Waiting for required checks");
	expect(run.querySelector("span.rounded-full")).toHaveClass("bg-[var(--color-status-working)]");
	expect(within(run).getByRole("link", { name: "Open PR #45" })).toBeInTheDocument();
	for (const name of ["Approve integration…", "Merge", "Approve plan"]) expect(within(dialog).queryByRole("button", { name })).not.toBeInTheDocument();
	expect(within(dialog).getByText("Approved")).toBeInTheDocument();
});

test("a head waiting for its integration approval is approved from the footer; a cancel leaves it as it was", async () => {
	vi.mocked(lado.approveLadoIntegration).mockResolvedValue(null);
	mockApproved(approvedTask({ state: "integration_pending", execution: { ...execution, sessionEnded: true, pr: openPR } }));
	renderDialog();
	const dialog = await screen.findByTestId("lado-task-dialog");

	const run = await within(dialog).findByTestId("lado-run-status");
	expect(within(run).getByRole("status")).toHaveTextContent(/^Integration pending$/);
	expect(run.querySelector("span.rounded-full")).toHaveClass("bg-[var(--color-status-needs-you)]");
	expect(within(run).queryByRole("button", { name: "Merge" })).not.toBeInTheDocument();
	const approve = within(run).getByRole("button", { name: "Approve integration…" });
	// The decision is a control, not status text.
	expect(within(run).getByRole("status")).not.toContainElement(approve);
	fireEvent.click(approve);
	await waitFor(() => expect(lado.approveLadoIntegration).toHaveBeenCalledWith("demo-7"));
	await waitFor(() => expect(within(run).getByRole("button", { name: "Approve integration…" })).toBeEnabled());
	expect(within(run).queryByRole("alert")).not.toBeInTheDocument();
});

test("an approved head merges from the footer, and one LADO could not post a status for says so in the caution color", async () => {
	vi.mocked(lado.mergeLadoPR).mockRejectedValueOnce(new lado.LadoApiError("LADO could not check this merge", "LADO_MERGE_GATE_FAILED"));
	mockApproved(approvedTask({ state: "integrating", stateReason: "status_not_posted", execution: { ...execution, sessionEnded: true, pr: openPR } }));
	renderDialog();
	const dialog = await screen.findByTestId("lado-task-dialog");

	const run = await within(dialog).findByTestId("lado-run-status");
	expect(within(run).getByRole("status")).toHaveTextContent(/^Integrating$/);
	expect(run.querySelector("span.rounded-full")).toHaveClass("bg-[var(--color-status-ready)]");
	const line = within(run).getByTestId("lado-run-not-posted");
	expect(line).toHaveTextContent("LADO could not post the integration status yet");
	expect(line).toHaveClass("text-[var(--color-status-in-review)]");
	expect(within(run).getByRole("status")).not.toContainElement(line);
	expect(within(run).queryByRole("button", { name: "Approve integration…" })).not.toBeInTheDocument();
	fireEvent.click(within(run).getByRole("button", { name: "Merge" }));
	await waitFor(() => expect(lado.mergeLadoPR).toHaveBeenCalledWith({ number: 45, url: openPR.url, headSha: prHead }));
	expect(await within(run).findByRole("alert")).toHaveTextContent("LADO could not check this merge's approval, so it refused the merge. Try again.");
	cleanup();

	// A posted status needs no line.
	mockApproved(approvedTask({ state: "integrating", execution: { ...execution, sessionEnded: true, pr: openPR } }));
	renderDialog();
	const posted = await within(await screen.findByTestId("lado-task-dialog")).findByTestId("lado-run-status");
	expect(within(posted).queryByTestId("lado-run-not-posted")).not.toBeInTheDocument();
	expect(within(posted).getByRole("button", { name: "Merge" })).toBeEnabled();
});

test("work merged without an integration approval says so and takes no decision", async () => {
	mockApproved(approvedTask({ state: "completed", stateReason: "merged_without_approval", execution: { ...execution, sessionEnded: true, pr: { ...openPR, merged: true } } }));
	renderDialog();
	const dialog = await screen.findByTestId("lado-task-dialog");

	const run = await within(dialog).findByTestId("lado-run-status");
	expect(within(run).getByRole("status")).toHaveTextContent("Completed · Merged without an integration approval");
	expect(run.querySelector("span.rounded-full")).toHaveClass("bg-[var(--color-status-needs-you)]");
	for (const name of ["Approve integration…", "Merge"]) expect(within(run).queryByRole("button", { name })).not.toBeInTheDocument();
});
