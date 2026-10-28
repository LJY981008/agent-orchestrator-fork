import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { aoBridge } from "../../lib/bridge";
import * as lado from "../../lib/lado";
import { RulesSection } from "./RulesSection";

vi.mock("../../lib/lado", async (original) => ({
	...(await original<typeof import("../../lib/lado")>()),
	fetchLadoRules: vi.fn(),
	fetchLadoRule: vi.fn(),
	createLadoRule: vi.fn(),
	updateLadoRule: vi.fn(),
	setLadoBinding: vi.fn(),
	deleteLadoRule: vi.fn(),
}));

function rule(fields: Partial<lado.LadoRule> & Pick<lado.LadoRule, "id" | "name">): lado.LadoRule {
	return {
		builtin: false,
		protected: false,
		archived: false,
		category: "",
		instruction: "",
		enforcement: ["prompt"],
		strength: "advisory",
		conflictsWith: [],
		global: "on",
		overrides: [],
		version: 1,
		versionId: `${fields.id}-v${fields.version ?? 1}`,
		sha256: "a".repeat(64),
		createdAt: "2026-10-22T09:00:00Z",
		updatedAt: "2026-10-22T09:00:00Z",
		...fields,
	};
}

const planApproval = rule({
	id: "plan.approval.required",
	name: "Plan approval required",
	builtin: true,
	protected: true,
	category: "workflow",
	enforcement: ["workflow_gate"],
	strength: "prevents",
});
const ceiling = rule({
	id: "agent.permission.ceiling",
	name: "Permission ceiling",
	builtin: true,
	protected: true,
	category: "tools",
	enforcement: ["tool_policy"],
	strength: "prevents",
	ceiling: { mode: "auto" },
});
const korean = rule({
	id: "git.commit.message.korean",
	name: "Commit messages in Korean",
	builtin: true,
	category: "git",
	global: "off",
	instruction: "Write Git commit subjects and bodies primarily in Korean.",
});
const review = rule({
	id: "lado-rule-review",
	name: "Ask for a review",
	category: "process",
	instruction: "Ask a teammate to review the change.",
	version: 2,
	overrides: [
		{ scope: "project", scopeId: "demo", state: "force_on", label: "Demo" },
		{ scope: "task", scopeId: "lado-task-1", state: "force_off", label: "Add login" },
		{ scope: "task", scopeId: "lado-task-2", state: "force_off", label: "Add logout" },
	],
});

// The gates of a pull request (step 8), protected like the two above.
const gate = (id: string, name: string, category: string, enforcement: string[]) =>
	rule({ id, name, builtin: true, protected: true, category, enforcement, strength: "prevents" });
const integrationApproval = gate("integration.approval.required", "Integration approval required", "workflow", ["workflow_gate", "prompt"]);
const noDirectWrite = gate("git.protected_branch.no_direct_write", "No direct writes to the base branch", "git", ["workflow_gate", "prompt"]);
const testsMustPass = gate("test.command.must_pass", "Tests must pass", "workflow", ["validator", "prompt"]);
const DEFAULT_PATHS = [".github/**", ".gitlab-ci.yml", ".gitlab/**", "**/CLAUDE.md", "**/AGENTS.md", ".claude/**", ".codex/**", ".mcp.json", ".ao/**"];
const protectedPaths = {
	...gate("paths.protected", "Protected paths", "git", ["validator", "prompt"]),
	strength: "detects" as const,
	paths: { globs: DEFAULT_PATHS },
};

const noImpact: lado.LadoRuleImpact = { projects: [], tasks: [] };
const impact: lado.LadoRuleImpact = {
	projects: [{ id: "demo", name: "Demo" }],
	tasks: [{ id: "lado-task-3", projectId: "demo", title: "Add the settings page", issueRef: "github:acme/demo#12" }],
};

function row(ruleId: string): HTMLElement | null {
	return document.querySelector<HTMLElement>(`[data-testid="lado-rule-row"][data-rule-id="${ruleId}"]`);
}

async function findRow(ruleId: string): Promise<HTMLElement> {
	return waitFor(() => {
		const element = row(ruleId);
		if (!element) throw new Error(`no row for ${ruleId}`);
		return element;
	});
}

function renderSection() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<RulesSection />
		</QueryClientProvider>,
	);
}

// LADO fork: Settings → Rules (docs/LJY-PRD/HARNESS.md §12).
describe("RulesSection", () => {
	beforeEach(() => {
		vi.resetAllMocks();
		vi.mocked(lado.fetchLadoRules).mockResolvedValue([planApproval, ceiling, korean, review]);
	});

	afterEach(() => {
		cleanup();
		vi.restoreAllMocks();
	});

	it("lists protected rules as always on", async () => {
		renderSection();

		const approvalRow = await findRow(planApproval.id);
		const protectedCard = screen.getByRole("heading", { name: "Protected rules" }).closest("section")!;
		expect(protectedCard).toContainElement(approvalRow);
		expect(protectedCard).toContainElement(row(ceiling.id));
		expect(within(approvalRow).getByText("Plan approval required")).toBeInTheDocument();
		expect(within(approvalRow).getByText("LADO starts work only from a plan you approved.")).toBeInTheDocument();
		expect(within(approvalRow).getByText("Always on")).toBeInTheDocument();

		const ceilingRow = row(ceiling.id)!;
		expect(within(ceilingRow).getByRole("button", { name: "Permission ceiling" })).toHaveTextContent("Auto");
		// Protected rules take no switch; their menu only opens the history.
		for (const protectedRow of [approvalRow, ceilingRow]) {
			expect(within(protectedRow).queryByRole("switch")).not.toBeInTheDocument();
			expect(within(protectedRow).getByRole("button", { name: /^Actions for/ })).toBeInTheDocument();
		}

		// Other rules sit in their category's card with their Global switch.
		const reviewRow = row(review.id)!;
		expect(screen.getByRole("heading", { name: "process" }).closest("section")).toContainElement(reviewRow);
		expect(screen.getByRole("heading", { name: "Git" }).closest("section")).toContainElement(row(korean.id));
		expect(within(reviewRow).getByRole("switch", { name: "Ask for a review for every LADO project" })).toBeChecked();
		expect(within(row(korean.id)!).getByRole("switch", { name: "Commit messages in Korean for every LADO project" })).not.toBeChecked();
		expect(within(reviewRow).getByText("Advisory · On in 1 project · Off in 2 tasks")).toBeInTheDocument();
	});

	it("lists the gates of a pull request with the protected rules", async () => {
		vi.mocked(lado.fetchLadoRules).mockResolvedValue([planApproval, ceiling, integrationApproval, noDirectWrite, testsMustPass, protectedPaths, korean, review]);
		renderSection();

		const protectedCard = (await screen.findByRole("heading", { name: "Protected rules" })).closest("section")!;
		await findRow(protectedPaths.id);
		const expected: [lado.LadoRule, string, string][] = [
			[integrationApproval, "Integration approval required", "Workers never merge their pull requests; you approve each head's integration in LADO."],
			[
				noDirectWrite,
				"No direct writes to the base branch",
				"Workers never push to, reset or update the base branch; their work reaches it only through a pull request.",
			],
			[testsMustPass, "Tests must pass", "A pull request's head is ready only when the project's check commands and the required CI checks pass."],
			[protectedPaths, "Protected paths", "Workers change a protected path only when the approved plan names it."],
		];
		for (const [gateRule, name, description] of expected) {
			const gateRow = row(gateRule.id)!;
			expect(protectedCard).toContainElement(gateRow);
			expect(within(gateRow).getByText(name)).toBeInTheDocument();
			expect(within(gateRow).getByText(description)).toBeInTheDocument();
			expect(within(gateRow).queryByRole("switch")).not.toBeInTheDocument();
		}
		for (const gateRule of [integrationApproval, noDirectWrite, testsMustPass]) {
			expect(within(row(gateRule.id)!).getByText("Always on")).toBeInTheDocument();
		}
		// The protected paths show how many there are and open their editor instead.
		const pathsRow = row(protectedPaths.id)!;
		expect(within(pathsRow).queryByText("Always on")).not.toBeInTheDocument();
		expect(within(pathsRow).getByText("9 paths")).toBeInTheDocument();
		expect(within(pathsRow).getByRole("button", { name: "Edit protected paths" })).toHaveTextContent("Edit");
	});

	it("edits the protected paths through the desktop confirmation", async () => {
		const user = userEvent.setup();
		const changed = { ...protectedPaths, version: 2, paths: { globs: [...DEFAULT_PATHS, "deploy/**"] } };
		vi.mocked(lado.fetchLadoRules)
			.mockResolvedValueOnce([planApproval, ceiling, protectedPaths, korean, review])
			.mockResolvedValue([planApproval, ceiling, changed, korean, review]);
		const changeRule = vi.spyOn(aoBridge.lado.presence, "changeRule").mockResolvedValue({ status: "changed", rule: changed });
		renderSection();

		await user.click(within(await findRow(protectedPaths.id)).getByRole("button", { name: "Edit protected paths" }));
		const dialog = await screen.findByRole("dialog", { name: "Edit protected paths" });
		const field = within(dialog).getByRole("textbox", { name: "Paths and patterns" });
		expect(field).toHaveValue(DEFAULT_PATHS.join("\n"));

		await user.type(field, "{Enter}deploy/**");
		await user.click(within(dialog).getByRole("button", { name: "Save paths" }));

		await waitFor(() => expect(changeRule).toHaveBeenCalledWith("paths.protected", { globs: [...DEFAULT_PATHS, "deploy/**"] }));
		await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
		expect(await within(row(protectedPaths.id)!).findByText("10 paths")).toBeInTheDocument();
		// The desktop app made the change; the renderer sent no rule change of its own.
		expect(lado.updateLadoRule).not.toHaveBeenCalled();
	});

	it("changes the permission ceiling through the desktop confirmation", async () => {
		const user = userEvent.setup();
		const changed = { ...ceiling, version: 2, ceiling: { mode: "bypass-permissions" as const } };
		vi.mocked(lado.updateLadoRule).mockResolvedValue({ applied: false, impact: noImpact, rule: changed });
		const changeRule = vi.spyOn(aoBridge.lado.presence, "changeRule").mockResolvedValue({ status: "changed", rule: changed });
		renderSection();

		await user.click(within(await findRow(ceiling.id)).getByRole("button", { name: "Permission ceiling" }));
		await user.click(await screen.findByRole("menuitem", { name: "Bypass permissions" }));

		await waitFor(() => expect(changeRule).toHaveBeenCalledWith("agent.permission.ceiling", "bypass-permissions"));
		// The renderer only asked what the change reaches; the desktop app made it.
		expect(vi.mocked(lado.updateLadoRule).mock.calls).toEqual([
			["agent.permission.ceiling", { baseVersion: 1, ceiling: { mode: "bypass-permissions" }, dryRun: true }],
		]);
		await waitFor(() => expect(lado.fetchLadoRules).toHaveBeenCalledTimes(2));
	});

	it("keeps the permission ceiling when the desktop confirmation is cancelled", async () => {
		const user = userEvent.setup();
		vi.mocked(lado.updateLadoRule).mockResolvedValue({ applied: false, impact: noImpact, rule: ceiling });
		const changeRule = vi.spyOn(aoBridge.lado.presence, "changeRule").mockResolvedValue({ status: "cancelled" });
		renderSection();

		const trigger = within(await findRow(ceiling.id)).getByRole("button", { name: "Permission ceiling" });
		await user.click(trigger);
		await user.click(await screen.findByRole("menuitem", { name: "Accept edits" }));

		await waitFor(() => expect(changeRule).toHaveBeenCalledWith("agent.permission.ceiling", "accept-edits"));
		await waitFor(() => expect(trigger).toBeEnabled());
		expect(trigger).toHaveTextContent("Auto");
		expect(lado.fetchLadoRules).toHaveBeenCalledTimes(1);
	});

	it("opens the history of a protected rule", async () => {
		const user = userEvent.setup();
		const version = (number: number, mode: lado.LadoCeilingMode, actor: string): lado.LadoRuleVersion => ({
			id: `agent.permission.ceiling-v${number}`,
			number,
			name: "Permission ceiling",
			category: "tools",
			instruction: "",
			enforcement: ["tool_policy"],
			ceiling: { mode },
			conflictsWith: [],
			sha256: String(number).repeat(64),
			actor,
			createdAt: `2026-10-22T0${number}:00:00Z`,
		});
		const changed = { ...ceiling, version: 2, ceiling: { mode: "accept-edits" as const } };
		vi.mocked(lado.fetchLadoRules).mockResolvedValue([planApproval, changed, korean, review]);
		vi.mocked(lado.fetchLadoRule).mockResolvedValue({ rule: changed, versions: [version(1, "auto", "lado"), version(2, "accept-edits", "human")] });
		renderSection();

		await user.click(within(await findRow(ceiling.id)).getByRole("button", { name: "Actions for Permission ceiling" }));
		// A protected rule is not edited, archived or deleted, so History is all its menu holds.
		const menu = await screen.findByRole("menu");
		expect(within(menu).getAllByRole("menuitem").map((item) => item.textContent)).toEqual(["History"]);
		await user.click(within(menu).getByRole("menuitem", { name: "History" }));

		const dialog = await screen.findByRole("dialog", { name: "History of Permission ceiling" });
		expect(lado.fetchLadoRule).toHaveBeenCalledWith(ceiling.id);
		// The ceiling's mode of each version is what its history is for.
		expect(await within(dialog).findByText("Accept edits")).toBeInTheDocument();
		expect(within(dialog).getByText("Version 1")).toBeInTheDocument();
	});

	it("turns a rule off globally after a dry run", async () => {
		const user = userEvent.setup();
		const off = { ...review, global: "off" as const };
		vi.mocked(lado.fetchLadoRules)
			.mockResolvedValueOnce([planApproval, ceiling, korean, review])
			.mockResolvedValue([planApproval, ceiling, korean, off]);
		vi.mocked(lado.setLadoBinding).mockImplementation(async (input) => ({ applied: !input.dryRun, impact: noImpact, rule: off }));
		renderSection();

		const toggle = within(await findRow(review.id)).getByRole("switch", { name: "Ask for a review for every LADO project" });
		await user.click(toggle);

		await waitFor(() => expect(lado.setLadoBinding).toHaveBeenCalledTimes(2));
		const binding = { ruleId: review.id, scope: "global", scopeId: "", state: "off" };
		expect(vi.mocked(lado.setLadoBinding).mock.calls).toEqual([[{ ...binding, dryRun: true }], [binding]]);
		await waitFor(() => expect(toggle).not.toBeChecked());
		expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
	});

	it("asks before a change that affects approved tasks", async () => {
		const user = userEvent.setup();
		vi.mocked(lado.setLadoBinding).mockImplementation(async (input) => ({ applied: !input.dryRun, impact, rule: { ...review, global: "off" } }));
		renderSection();
		const toggle = within(await findRow(review.id)).getByRole("switch", { name: "Ask for a review for every LADO project" });

		await user.click(toggle);
		let dialog = await screen.findByRole("dialog", { name: "Change the rules for approved tasks?" });
		const task = within(dialog).getByTestId("lado-impact-task");
		expect(within(task).getByText("Add the settings page")).toBeInTheDocument();
		expect(within(task).getByText("acme/demo#12")).toHaveClass("font-mono");
		expect(lado.setLadoBinding).toHaveBeenCalledTimes(1);

		// Cancelling drops the change: only the dry run was sent.
		await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
		await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
		expect(lado.setLadoBinding).toHaveBeenCalledTimes(1);

		await user.click(toggle);
		dialog = await screen.findByRole("dialog", { name: "Change the rules for approved tasks?" });
		await user.click(within(dialog).getByRole("button", { name: "Change" }));

		await waitFor(() => expect(lado.setLadoBinding).toHaveBeenCalledTimes(3));
		expect(lado.setLadoBinding).toHaveBeenLastCalledWith({ ruleId: review.id, scope: "global", scopeId: "", state: "off" });
		await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
	});

	it("archives and restores a rule", async () => {
		const user = userEvent.setup();
		const archived = { ...review, archived: true, archivedAt: "2026-10-22T10:00:00Z", version: 3 };
		vi.mocked(lado.fetchLadoRules)
			.mockResolvedValueOnce([planApproval, ceiling, korean, review])
			.mockResolvedValueOnce([planApproval, ceiling, korean, archived])
			.mockResolvedValue([planApproval, ceiling, korean, { ...review, version: 4 }]);
		vi.mocked(lado.updateLadoRule).mockImplementation(async (_ruleId, input) => ({ applied: !input.dryRun, impact: noImpact, rule: archived }));
		renderSection();

		await user.click(within(await findRow(review.id)).getByRole("button", { name: "Actions for Ask for a review" }));
		await user.click(await screen.findByRole("menuitem", { name: "Archive" }));

		await waitFor(() => expect(lado.updateLadoRule).toHaveBeenCalledTimes(2));
		expect(vi.mocked(lado.updateLadoRule).mock.calls).toEqual([
			[review.id, { baseVersion: 2, archived: true, dryRun: true }],
			[review.id, { baseVersion: 2, archived: true }],
		]);

		// Archived rules wait in a collapsed group, without a switch.
		const group = await screen.findByRole("button", { name: /^Archived/ });
		expect(group).toHaveAttribute("aria-expanded", "false");
		expect(row(review.id)).toBeNull();
		await user.click(group);
		const archivedRow = await findRow(review.id);
		expect(within(archivedRow).queryByRole("switch")).not.toBeInTheDocument();
		expect(within(archivedRow).getByText("process · Advisory · On in 1 project · Off in 2 tasks")).toBeInTheDocument();

		await user.click(within(archivedRow).getByRole("button", { name: "Actions for Ask for a review" }));
		expect(screen.queryByRole("menuitem", { name: "Archive" })).not.toBeInTheDocument();
		await user.click(await screen.findByRole("menuitem", { name: "Unarchive" }));

		await waitFor(() => expect(lado.updateLadoRule).toHaveBeenCalledTimes(4));
		expect(lado.updateLadoRule).toHaveBeenLastCalledWith(review.id, { baseVersion: 3, archived: false });
		await waitFor(() => expect(screen.queryByRole("button", { name: /^Archived/ })).not.toBeInTheDocument());
		expect(within(await findRow(review.id)).getByRole("switch")).toBeInTheDocument();
	});

	it("deletes an unused rule after confirmation", async () => {
		const user = userEvent.setup();
		vi.mocked(lado.fetchLadoRules)
			.mockResolvedValueOnce([planApproval, ceiling, korean, review])
			.mockResolvedValue([planApproval, ceiling, korean]);
		vi.mocked(lado.deleteLadoRule).mockResolvedValue(undefined);
		renderSection();

		// LADO's own rules are never deleted, so their menu leaves it out.
		await user.click(within(await findRow(korean.id)).getByRole("button", { name: "Actions for Commit messages in Korean" }));
		expect(await screen.findByRole("menuitem", { name: "Archive" })).toBeInTheDocument();
		expect(screen.queryByRole("menuitem", { name: "Delete" })).not.toBeInTheDocument();
		await user.keyboard("{Escape}");

		await user.click(within(row(review.id)!).getByRole("button", { name: "Actions for Ask for a review" }));
		await user.click(await screen.findByRole("menuitem", { name: "Delete" }));
		const dialog = await screen.findByRole("dialog", { name: "Delete Ask for a review?" });
		expect(lado.deleteLadoRule).not.toHaveBeenCalled();

		await user.click(within(dialog).getByRole("button", { name: "Delete" }));

		await waitFor(() => expect(lado.deleteLadoRule).toHaveBeenCalledWith(review.id));
		await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
		await waitFor(() => expect(row(review.id)).toBeNull());
	});

	it("says to archive a rule an approval used instead of deleting it", async () => {
		const user = userEvent.setup();
		vi.mocked(lado.deleteLadoRule).mockRejectedValue(
			new lado.LadoApiError("a rule snapshot uses the rule", "LADO_RULE_REFERENCED", { rule: review.id }),
		);
		renderSection();

		await user.click(within(await findRow(review.id)).getByRole("button", { name: "Actions for Ask for a review" }));
		await user.click(await screen.findByRole("menuitem", { name: "Delete" }));
		const dialog = await screen.findByRole("dialog", { name: "Delete Ask for a review?" });
		await user.click(within(dialog).getByRole("button", { name: "Delete" }));

		expect(await within(dialog).findByRole("alert")).toHaveTextContent(
			"An approved plan used this rule, so LADO keeps it. Archive it instead.",
		);
		expect(within(dialog).getByRole("button", { name: "Delete" })).toBeDisabled();
		expect(row(review.id)).not.toBeNull();
	});

	it("filters rules by search", async () => {
		const user = userEvent.setup();
		renderSection();
		await findRow(review.id);
		const search = screen.getByRole("searchbox", { name: "Search rules" });

		await user.type(search, "REVIEW");
		expect(row(review.id)).not.toBeNull();
		expect(row(korean.id)).toBeNull();
		expect(row(planApproval.id)).toBeNull();
		expect(screen.queryByRole("heading", { name: "Protected rules" })).not.toBeInTheDocument();

		// Instructions and translated categories match too.
		await user.clear(search);
		await user.type(search, "primarily in korean");
		expect(row(korean.id)).not.toBeNull();
		expect(row(review.id)).toBeNull();
		await user.clear(search);
		await user.type(search, "workflow");
		expect(row(planApproval.id)).not.toBeNull();
		expect(row(ceiling.id)).toBeNull();

		await user.clear(search);
		await user.type(search, "nothing like this");
		expect(screen.getByText("No rules match your search.")).toBeInTheDocument();
		expect(document.querySelectorAll('[data-testid="lado-rule-row"]')).toHaveLength(0);
	});

	it("shows a failed change under its row", async () => {
		const user = userEvent.setup();
		vi.mocked(lado.setLadoBinding).mockRejectedValue(new lado.LadoApiError("archived", "LADO_RULE_ARCHIVED", {}));
		renderSection();

		await user.click(within(await findRow(review.id)).getByRole("switch", { name: "Ask for a review for every LADO project" }));

		expect(await within(row(review.id)!).findByRole("alert")).toHaveTextContent("This rule is archived. Unarchive it first.");
		expect(lado.setLadoBinding).toHaveBeenCalledTimes(1);
	});
});
