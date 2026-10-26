import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { apiClient } from "../../lib/api-client";
import * as lado from "../../lib/lado";
import { daemonCalls, routeDaemon, setupChangeResult, setupConflict, setupItem } from "../../test/lado-setup-fixtures";
import { LadoTaskRules } from "./LadoTaskRules";

vi.mock("../../lib/lado", async (original) => ({
	...(await original<typeof import("../../lib/lado")>()),
	fetchLadoEffective: vi.fn(),
	fetchLadoRules: vi.fn(),
	setLadoBinding: vi.fn(),
}));

// Local setup goes through the daemon's setup routes (lib/lado-setup.ts).
vi.mock("../../lib/api-client", async (original) => ({
	...(await original<typeof import("../../lib/api-client")>()),
	apiClient: { GET: vi.fn(), POST: vi.fn(), PUT: vi.fn(), PATCH: vi.fn(), DELETE: vi.fn() },
}));

const revision = { id: "lado-plan-1", number: 1, sha256: "a".repeat(64), source: "agent" as const, valid: true, createdAt: "2026-10-22T09:30:00Z" };

const task: lado.LadoTask = {
	id: "lado-task-1",
	projectId: "demo",
	repository: "github:acme/demo",
	state: "approved",
	agent: "codex",
	issue: { ref: "github:acme/demo#12", number: 12, title: "Add login", state: "open", url: "https://github.com/acme/demo/issues/12", observedAt: "2026-10-22T09:00:00Z" },
	plan: { requestPending: false, latest: revision, approvedRevisionId: revision.id },
	createdAt: "2026-10-22T09:00:00Z",
};

function effectiveRule(fields: Partial<lado.LadoEffectiveRule> & Pick<lado.LadoEffectiveRule, "ruleId" | "name">): lado.LadoEffectiveRule {
	return {
		category: "",
		builtin: false,
		protected: false,
		version: 1,
		on: true,
		strength: "advisory",
		enforcement: ["prompt"],
		global: "on",
		project: "inherit",
		task: "inherit",
		source: { scope: "global", scopeId: "", state: "on" },
		...fields,
	};
}

const planApproval = effectiveRule({ ruleId: "plan.approval.required", name: "Plan approval required", builtin: true, protected: true, strength: "prevents" });
const ceiling = effectiveRule({ ruleId: "agent.permission.ceiling", name: "Permission ceiling", builtin: true, protected: true, strength: "prevents" });
const korean = effectiveRule({
	ruleId: "git.commit.message.korean",
	name: "Commit messages in Korean",
	builtin: true,
	global: "off",
	on: false,
	source: { scope: "global", scopeId: "", state: "off" },
});
const review = effectiveRule({
	ruleId: "lado-rule-review",
	name: "Ask for a review",
	project: "force_on",
	source: { scope: "project", scopeId: "demo", state: "force_on" },
});

function effective(fields: Partial<lado.LadoEffective> = {}): lado.LadoEffective {
	return {
		projectId: "demo",
		taskId: "lado-task-1",
		agent: { harness: "codex", model: "", source: "task" },
		permissionCeiling: "auto",
		permissionMode: "auto",
		rules: [planApproval, ceiling, korean, review],
		aoRules: [],
		prompt: "## LADO rules\n\nThe developer approved these rules for this run.\n\n### Ask for a review\n\nAsk a teammate to review the change.",
		capabilities: [
			{ class: "prompt", status: "supported" },
			{ class: "permission_mode", status: "supported" },
		],
		problems: [],
		blocking: false,
		snapshotSha256: "e".repeat(64),
		setup: { items: [], aoSystemSha256: "", conflicts: [], accepted: [] },
		approved: {
			snapshotSha256: "e".repeat(64),
			approvedAt: "2026-10-22T10:00:00Z",
			revision: 1,
			matches: true,
			blocking: false,
			added: [],
			removed: [],
			changed: [],
			setup: [],
			setupAdded: [],
			setupRemoved: [],
			setupChanged: [],
			aoSystemSha256: "",
			accepted: [],
		},
		...fields,
	};
}

function renderRules(shown: lado.LadoTask = task) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<LadoTaskRules projectId="demo" task={shown} />
		</QueryClientProvider>,
	);
}

function row(ruleId: string): HTMLElement {
	const found = document.querySelector<HTMLElement>(`[data-testid="lado-effective-rule"][data-rule-id="${ruleId}"]`);
	if (!found) throw new Error(`no row for ${ruleId}`);
	return found;
}

// LADO fork: the task view's Rules tab (docs/LJY-PRD/HARNESS.md §12, PRD HAR-005).
describe("LadoTaskRules", () => {
	beforeEach(() => {
		vi.resetAllMocks();
		vi.mocked(lado.fetchLadoEffective).mockResolvedValue(effective());
		vi.mocked(lado.fetchLadoRules).mockResolvedValue([]);
	});

	afterEach(cleanup);

	it("shows the problems that block the run", async () => {
		vi.mocked(lado.fetchLadoEffective).mockResolvedValue(
			effective({
				agent: { harness: "", model: "", source: "" },
				blocking: true,
				problems: [
					{ code: "LADO_AGENT_NOT_CHOSEN", severity: "blocking", rules: [], details: {} },
					{ code: "LADO_PLAN_CEILING_CLAMPED", severity: "warning", rules: [], details: { plan: "bypass-permissions", ceiling: "auto" } },
					{ code: "LADO_CODEX_ASKS_APPROVAL", severity: "info", rules: [], details: { mode: "auto" } },
				],
			}),
		);
		renderRules({ ...task, agent: undefined });

		expect(await screen.findByText("No agent chosen · at most Auto")).toBeInTheDocument();
		expect(lado.fetchLadoEffective).toHaveBeenCalledWith("demo", "lado-task-1");
		const problems = within(screen.getByRole("list", { name: "Problems" })).getAllByTestId("lado-policy-problem");
		expect(problems.map((problem) => problem.getAttribute("data-severity"))).toEqual(["blocking", "warning", "info"]);
		// Each says its severity in words as well as by its dot's color.
		expect(problems[0]).toHaveTextContent("Blocks the run · No agent is chosen for this task. Choose one so LADO can check it against the rules.");
		expect(problems[0].querySelector("[aria-hidden='true']")).toHaveClass("bg-[var(--color-status-needs-you)]");
		expect(problems[1]).toHaveTextContent("Warning · The plan asks for Bypass permissions; the permission ceiling lowers it to Auto.");
		expect(problems[1].querySelector("[aria-hidden='true']")).toHaveClass("bg-[var(--color-status-in-review)]");
		expect(problems[2]).toHaveTextContent("Note · Codex runs at Auto with approvals on request");
		expect(problems[2].querySelector("[aria-hidden='true']")).toHaveClass("bg-[var(--color-status-idle)]");
	});

	it("overrides a rule for this task", async () => {
		const user = userEvent.setup();
		vi.mocked(lado.setLadoBinding).mockImplementation(async (input) => ({
			applied: !input.dryRun,
			impact: { projects: [], tasks: [{ id: task.id, projectId: "demo", title: "Add login", issueRef: task.issue.ref }] },
		}));
		renderRules();

		expect(await screen.findByText("Runs with Codex · at most Auto")).toBeInTheDocument();
		expect(within(row(review.ruleId)).getByText("On · from the project")).toBeInTheDocument();
		expect(within(row(planApproval.ruleId)).getByText("Always on")).toBeInTheDocument();
		await user.click(within(row(korean.ruleId)).getByRole("button", { name: "Commit messages in Korean for this task" }));
		await user.click(await screen.findByRole("menuitem", { name: "On" }));

		// The task is approved under the rules now, so the change asks first.
		const dialog = await screen.findByRole("dialog", { name: "Change the rules for approved tasks?" });
		expect(within(dialog).getByText("Add login")).toBeInTheDocument();
		await user.click(within(dialog).getByRole("button", { name: "Change" }));

		const binding = { ruleId: korean.ruleId, scope: "task", scopeId: "lado-task-1", state: "force_on" };
		await waitFor(() => expect(lado.setLadoBinding).toHaveBeenCalledTimes(2));
		expect(vi.mocked(lado.setLadoBinding).mock.calls).toEqual([[{ ...binding, dryRun: true }], [binding]]);
		await waitFor(() => expect(lado.fetchLadoEffective).toHaveBeenCalledTimes(2));
	});

	it("shows what changed since approval", async () => {
		vi.mocked(lado.fetchLadoRules).mockResolvedValue([
			{
				id: "lado-rule-old",
				name: "Old rule",
				category: "",
				instruction: "Do the old thing.",
				builtin: false,
				protected: false,
				archived: true,
				version: 2,
				versionId: "lado-rule-old-v2",
				sha256: "c".repeat(64),
				enforcement: ["prompt"],
				strength: "advisory",
				conflictsWith: [],
				global: "on",
				overrides: [],
				createdAt: "2026-10-21T09:00:00Z",
				updatedAt: "2026-10-22T11:00:00Z",
				archivedAt: "2026-10-22T11:00:00Z",
			},
		]);
		vi.mocked(lado.fetchLadoEffective).mockResolvedValue(
			effective({
				snapshotSha256: "d".repeat(64),
				approved: {
					snapshotSha256: "e".repeat(64),
					approvedAt: "2026-10-22T10:00:00Z",
					revision: 1,
					matches: false,
					blocking: false,
					added: [review.ruleId],
					removed: ["lado-rule-old"],
					changed: [ceiling.ruleId],
					setup: [],
					setupAdded: [],
					setupRemoved: [],
					setupChanged: [],
					aoSystemSha256: "",
					accepted: [],
				},
			}),
		);
		renderRules({ ...task, state: "review_required" });

		const comparison = await screen.findByTestId("lado-approval-rules");
		expect(comparison).toHaveTextContent("Rules or local setup changed since approval");
		expect(comparison).toHaveAttribute("data-tone", "attention");
		expect(screen.getByText("Added: Ask for a review")).toBeInTheDocument();
		expect(screen.getByText("Changed: Permission ceiling")).toBeInTheDocument();
		// The archived rule is not among the rules now, so its name comes from the full list.
		expect(await screen.findByText("Removed: Old rule")).toBeInTheDocument();
		expect(lado.fetchLadoRules).toHaveBeenCalledWith(true);
		cleanup();

		vi.mocked(lado.fetchLadoEffective).mockResolvedValue(effective());
		renderRules();
		expect(await screen.findByTestId("lado-approval-rules")).toHaveTextContent("Approved with these rules");
		expect(screen.getByTestId("lado-approval-rules")).toHaveAttribute("data-tone", "ready");
		cleanup();

		// The snapshot differs although no rule did: the agent, its mode, the AO instructions or AO changed.
		vi.mocked(lado.fetchLadoEffective).mockResolvedValue(
			effective({ approved: { ...effective().approved!, matches: false } }),
		);
		renderRules({ ...task, state: "review_required" });
		expect(await screen.findByTestId("lado-approval-rules")).toHaveTextContent("Rules or local setup changed since approval");
		expect(screen.getByText("No rule or local setup item changed; the agent, its mode, the AO instructions or AO itself did.")).toBeInTheDocument();
		cleanup();

		// An approval given before LADO had rules has no snapshot to compare.
		vi.mocked(lado.fetchLadoEffective).mockResolvedValue(effective({ approved: undefined }));
		renderRules({ ...task, state: "review_required" });
		expect(await screen.findByTestId("lado-approval-rules")).toHaveTextContent("Approved before rules existed");
		cleanup();

		// Without an approval there is nothing to compare.
		vi.mocked(lado.fetchLadoEffective).mockResolvedValue(effective({ approved: undefined }));
		renderRules({ ...task, state: "planned", plan: { requestPending: false, latest: revision } });
		await screen.findByText("Runs with Codex · at most Auto");
		expect(screen.queryByTestId("lado-approval-rules")).not.toBeInTheDocument();
	});

	it("compares the local setup with the approval and overrides an item for this task", async () => {
		const user = userEvent.setup();
		const claude = setupItem({
			itemId: "item-claude-md",
			kind: "instructions",
			name: "CLAUDE.md",
			project: "force_on",
			source: { scope: "project", scopeId: "demo", state: "force_on" },
		});
		const plugins = setupItem({ itemId: "item-plugins", kind: "setting", name: "enabledPlugins", installedSha256: "2".repeat(64) });
		const permissions = setupItem({ itemId: "item-permissions", kind: "setting", name: "permissions" });
		const approvedPlugins = {
			itemId: plugins.itemId,
			tool: plugins.tool,
			kind: plugins.kind,
			name: plugins.name,
			versionId: plugins.versionId,
			version: plugins.version,
			sha256: plugins.sha256,
			installedSha256: "1".repeat(64),
			source: plugins.source,
		};
		const oldAgent = { ...approvedPlugins, itemId: "item-old", kind: "agent" as const, name: "old-agent.md", versionId: "item-old-v1", installedSha256: "" };
		vi.mocked(lado.fetchLadoEffective).mockResolvedValue(
			effective({
				snapshotSha256: "d".repeat(64),
				setup: {
					items: [claude, plugins, permissions],
					aoSystemSha256: "a".repeat(64),
					conflicts: [],
					accepted: [{ acceptanceId: "acc-1", code: "LADO_SETUP_DENIES_GIT", detail: "Bash(git push:*)", itemId: permissions.itemId }],
				},
				approved: {
					...effective().approved!,
					matches: false,
					setup: [approvedPlugins, oldAgent],
					setupAdded: [claude.itemId],
					setupRemoved: [oldAgent.itemId],
					setupChanged: [plugins.itemId],
				},
			}),
		);
		routeDaemon(apiClient, {
			"GET /api/v1/lado/setup/conflicts": () => ({
				data: {
					conflicts: [
						setupConflict({
							code: "LADO_SETUP_DENIES_GIT",
							severity: "warning",
							itemId: permissions.itemId,
							detail: "Bash(git push:*)",
							details: { rule: "Bash(git push:*)" },
							acceptanceId: "acc-1",
						}),
					],
				},
			}),
			"PUT /api/v1/lado/setup/bindings": ({ body }) => ({
				data: setupChangeResult({
					applied: !(body as { dryRun?: boolean }).dryRun,
					impact: { projects: [], tasks: [{ id: task.id, projectId: "demo", title: "Add login", issueRef: task.issue.ref }] },
				}),
			}),
		});
		renderRules({ ...task, state: "review_required" });

		// What the approval fixed and what is on now, by name.
		expect(await screen.findByTestId("lado-approval-rules")).toHaveTextContent("Rules or local setup changed since approval");
		expect(screen.getAllByTestId("lado-approval-setup").map((line) => line.textContent)).toEqual([
			"Local setup added: CLAUDE.md",
			"Local setup removed: old-agent.md",
			"Local setup changed: enabledPlugins (installed plugins changed)",
		]);
		// Something changed, so the line about the agent and AO stays away.
		expect(screen.queryByText(/^No rule or local setup item changed/)).not.toBeInTheDocument();
		// The accepted warning has left the problems, and says so.
		expect(await screen.findByText("Accepted · The permission rule Bash(git push:*) denies git or gh, which workers need for branches and pull requests. · permissions")).toBeInTheDocument();
		expect(apiClient.GET).toHaveBeenCalledWith("/api/v1/lado/setup/conflicts", { params: { query: { projectId: "demo", taskId: "lado-task-1" } } });

		// The worker tool's items, with where each state comes from.
		const card = screen.getByRole("heading", { name: "Local setup for this task" }).closest("section")!;
		const rows = within(card).getAllByTestId("lado-setup-scope-item");
		expect(rows.map((row) => row.getAttribute("data-item-id"))).toEqual(["item-claude-md", "item-plugins", "item-permissions"]);
		expect(rows[0]).toHaveTextContent("Instructions · On · from the project");
		expect(rows[1]).toHaveTextContent("Setting · On · from Global");

		await user.click(within(rows[0]).getByRole("button", { name: "CLAUDE.md for this task" }));
		await user.click(await screen.findByRole("menuitem", { name: "Off" }));
		// The task is approved under the setup now, so the change asks first.
		const dialog = await screen.findByRole("dialog", { name: "Change the local setup for approved tasks?" });
		expect(within(dialog).getByText("Add login")).toBeInTheDocument();
		await user.click(within(dialog).getByRole("button", { name: "Change" }));

		const binding = { itemId: "item-claude-md", scope: "task", scopeId: "lado-task-1", state: "force_off" };
		await waitFor(() => expect(daemonCalls(apiClient, "PUT", "/api/v1/lado/setup/bindings")).toHaveLength(2));
		expect(daemonCalls(apiClient, "PUT", "/api/v1/lado/setup/bindings").map((call) => call.body)).toEqual([{ ...binding, dryRun: true }, binding]);
		await waitFor(() => expect(lado.fetchLadoEffective).toHaveBeenCalledTimes(2));
	});

	it("shows that the approved rules block the run", async () => {
		const problem = { code: "LADO_AGENT_CAPABILITY_UNSUPPORTED", severity: "blocking" as const, rules: [], details: { agent: "cursor", capability: "prompt" } };
		vi.mocked(lado.fetchLadoEffective).mockResolvedValue(
			effective({
				agent: { harness: "cursor", model: "", source: "task" },
				blocking: true,
				problems: [problem],
				approved: { ...effective().approved!, blocking: true },
			}),
		);
		renderRules({ ...task, agent: "cursor", state: "blocked" });

		// The rules are the approved ones, but they keep the run from starting:
		// not the ready color, and the problems follow.
		const comparison = await screen.findByTestId("lado-approval-rules");
		expect(comparison).toHaveTextContent("Approved with these rules, but they stop the run until the problems below are fixed");
		expect(comparison).toHaveAttribute("data-tone", "attention");
		const problems = screen.getByRole("list", { name: "Problems" });
		expect(comparison.compareDocumentPosition(problems) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
		// The agent line, the comparison and the problems sit at the rows' inset.
		expect(screen.getByTestId("lado-task-rules-summary")).toHaveClass("px-(--settings-row-inset)");
		expect(screen.getByTestId("lado-task-rules-summary")).toContainElement(problems);
	});

	it("shows the rule text the worker gets, and the AO instructions", async () => {
		const user = userEvent.setup();
		vi.mocked(lado.fetchLadoEffective).mockResolvedValue(
			effective({ aoRules: [{ kind: "worker_instructions", path: "", sha256: "1".repeat(64), chars: 42, readError: "" }] }),
		);
		renderRules();

		const toggle = await screen.findByRole("button", { name: "Rule text for the worker" });
		expect(toggle).toHaveAttribute("aria-expanded", "false");
		expect(screen.queryByTestId("lado-worker-text")).not.toBeInTheDocument();
		await user.click(toggle);
		const text = screen.getByTestId("lado-worker-text");
		expect(text).toHaveTextContent("Ask a teammate to review the change.");
		expect(text).not.toHaveClass("font-mono");
		expect(within(screen.getByTestId("lado-ao-rule")).getByText("42 characters")).toBeInTheDocument();
	});

	it("takes no new overrides for a cancelled task", async () => {
		vi.mocked(lado.fetchLadoEffective).mockResolvedValue(
			effective({ setup: { items: [setupItem({ itemId: "item-claude-md", kind: "instructions", name: "CLAUDE.md" })], aoSystemSha256: "", conflicts: [], accepted: [] } }),
		);
		renderRules({ ...task, state: "cancelled", cancelledAt: "2026-10-22T12:00:00Z" });

		await screen.findByText("Runs with Codex · at most Auto");
		expect(within(row(korean.ruleId)).getByRole("button", { name: "Commit messages in Korean for this task" })).toBeDisabled();
		expect(screen.getByRole("button", { name: "CLAUDE.md for this task" })).toBeDisabled();
	});
});
