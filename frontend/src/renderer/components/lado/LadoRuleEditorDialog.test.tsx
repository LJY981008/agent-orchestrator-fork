import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ComponentProps } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import * as lado from "../../lib/lado";
import { LadoRuleEditorDialog } from "./LadoRuleEditorDialog";

vi.mock("../../lib/lado", async (original) => ({
	...(await original<typeof import("../../lib/lado")>()),
	fetchLadoRule: vi.fn(),
	createLadoRule: vi.fn(),
	updateLadoRule: vi.fn(),
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
});
const noImpact: lado.LadoRuleImpact = { projects: [], tasks: [] };

function renderEditor(props: Partial<ComponentProps<typeof LadoRuleEditorDialog>> = {}) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<LadoRuleEditorDialog open rules={[planApproval, korean, review]} onOpenChange={() => undefined} {...props} />
		</QueryClientProvider>,
	);
}

// LADO fork: creating and editing a rule (docs/LJY-PRD/HARNESS.md §12).
describe("LadoRuleEditorDialog", () => {
	beforeEach(() => {
		vi.resetAllMocks();
	});

	afterEach(cleanup);

	it("creates a rule with its instruction", async () => {
		const user = userEvent.setup();
		const onOpenChange = vi.fn();
		vi.mocked(lado.createLadoRule).mockImplementation(async (input) => ({ applied: !input.dryRun, impact: noImpact, rule: review }));
		renderEditor({ rules: [planApproval, korean], onOpenChange });

		const dialog = await screen.findByRole("dialog", { name: "New rule" });
		await user.type(within(dialog).getByLabelText("Name"), "Ask for a review");
		await user.type(within(dialog).getByLabelText("Category"), "process");
		await user.type(within(dialog).getByLabelText("Instruction"), "Ask a teammate to review the change.");
		await user.click(within(dialog).getByRole("button", { name: "Conflicts with" }));
		// A protected rule is always on, so a rule cannot conflict with it.
		expect(screen.queryByRole("menuitemcheckbox", { name: "Plan approval required" })).not.toBeInTheDocument();
		const conflict = await screen.findByRole("menuitemcheckbox", { name: "Commit messages in Korean" });
		await user.click(conflict);
		expect(conflict).toHaveAttribute("aria-checked", "true");
		await user.keyboard("{Escape}");
		await waitFor(() => expect(screen.queryByRole("menu")).not.toBeInTheDocument());
		expect(within(dialog).getByRole("button", { name: "Conflicts with" })).toHaveTextContent("Commit messages in Korean");
		expect(within(dialog).getByRole("switch", { name: "On for every LADO project" })).toBeChecked();

		await user.click(within(dialog).getByRole("button", { name: "Create" }));

		await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false));
		const input = {
			name: "Ask for a review",
			category: "process",
			instruction: "Ask a teammate to review the change.",
			conflictsWith: ["git.commit.message.korean"],
			scope: "global",
			scopeId: "",
			on: true,
		};
		expect(vi.mocked(lado.createLadoRule).mock.calls).toEqual([[{ ...input, dryRun: true }], [input]]);
	});

	it("shows the daemon validation message next to the field", async () => {
		const user = userEvent.setup();
		const onOpenChange = vi.fn();
		vi.mocked(lado.createLadoRule).mockRejectedValue(
			new lado.LadoApiError("the instruction is too long", "LADO_RULE_INSTRUCTION_TOO_LONG", { max: 8000 }),
		);
		renderEditor({ onOpenChange });

		const dialog = await screen.findByRole("dialog", { name: "New rule" });
		await user.type(within(dialog).getByLabelText("Name"), "Explain every change");
		const instruction = within(dialog).getByLabelText("Instruction");
		await user.type(instruction, "Write a long explanation.");
		await user.click(within(dialog).getByRole("button", { name: "Create" }));

		const message = "Keep the instruction within 8000 characters.";
		await waitFor(() => expect(instruction).toHaveAccessibleDescription(message));
		expect(instruction).toHaveAttribute("aria-invalid", "true");
		// Right under its own field, and nowhere else.
		expect(instruction.nextElementSibling).toHaveTextContent(message);
		expect(within(dialog).getAllByRole("alert")).toHaveLength(1);
		expect(within(dialog).getByLabelText("Name")).not.toHaveAttribute("aria-invalid");
		// The dry run refused it, so nothing was created and the dialog stays.
		expect(lado.createLadoRule).toHaveBeenCalledTimes(1);
		expect(onOpenChange).not.toHaveBeenCalled();

		await user.type(instruction, " Shorter.");
		expect(within(dialog).queryByRole("alert")).not.toBeInTheDocument();
	});

	it("edits only what changed and reloads a newer version", async () => {
		const user = userEvent.setup();
		const newer = { ...review, version: 3, instruction: "Ask two teammates to review the change." };
		vi.mocked(lado.updateLadoRule)
			.mockRejectedValueOnce(new lado.LadoApiError("the rule has a newer version", "LADO_RULE_CHANGED", {}))
			.mockImplementation(async (_ruleId, input) => ({ applied: !input.dryRun, impact: noImpact, rule: newer }));
		vi.mocked(lado.fetchLadoRule).mockResolvedValue({ rule: newer, versions: [] });
		const onOpenChange = vi.fn();
		renderEditor({ rule: review, onOpenChange });

		const dialog = await screen.findByRole("dialog", { name: "Edit rule" });
		expect(within(dialog).getByRole("button", { name: "Save" })).toBeDisabled();
		expect(within(dialog).queryByRole("switch")).not.toBeInTheDocument();
		const name = within(dialog).getByLabelText("Name");
		await user.clear(name);
		await user.type(name, "Ask for reviews");
		await user.click(within(dialog).getByRole("button", { name: "Save" }));

		expect(await within(dialog).findByRole("status")).toHaveTextContent(
			"The rule changed since you opened it. Review the latest version and try again.",
		);
		expect(lado.updateLadoRule).toHaveBeenCalledWith(review.id, { baseVersion: 2, name: "Ask for reviews", dryRun: true });

		await user.click(within(dialog).getByRole("button", { name: "Reload" }));
		await waitFor(() => expect(within(dialog).getByLabelText("Instruction")).toHaveValue(newer.instruction));
		expect(name).toHaveValue("Ask for a review");
		expect(within(dialog).queryByRole("status")).not.toBeInTheDocument();

		await user.type(name, "s");
		await user.click(within(dialog).getByRole("button", { name: "Save" }));
		await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false));
		expect(lado.updateLadoRule).toHaveBeenLastCalledWith(review.id, { baseVersion: 3, name: "Ask for a reviews" });
	});
});
