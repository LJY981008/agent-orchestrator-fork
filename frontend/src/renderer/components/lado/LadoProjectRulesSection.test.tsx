import { act, cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import * as lado from "../../lib/lado";
import { useUiStore } from "../../stores/ui-store";
import { SettingsDialog } from "../SettingsPageTestHarness";
import { LadoProjectRulesSection } from "./LadoProjectRulesSection";

const { cloudProjectsState, localWorkspacesState } = vi.hoisted(() => ({
	cloudProjectsState: {
		data: [] as Array<{ id: string; orgId?: string; displayName: string }> | undefined,
		isLoading: false,
		isError: false,
		error: null as Error | null,
		refetch: () => undefined,
	},
	localWorkspacesState: { ids: [] as string[] },
}));

vi.mock("../../lib/lado", async (original) => ({
	...(await original<typeof import("../../lib/lado")>()),
	fetchLadoProjectSettings: vi.fn(),
	fetchLadoEffective: vi.fn(),
	fetchLadoRules: vi.fn(),
	createLadoRule: vi.fn(),
	setLadoBinding: vi.fn(),
}));

// The settings dialog around the page, as SettingsDialog.test.tsx stubs it.
vi.mock("../ProjectSettingsForm", () => ({
	ProjectSettingsForm: ({ cloudOrgId }: { cloudOrgId?: string }) => (
		<div data-testid="project-settings-form">{cloudOrgId ?? "local"}</div>
	),
}));
vi.mock("../GlobalSettingsForm", () => ({ GlobalSettingsForm: () => <div data-testid="global-settings-section" /> }));
vi.mock("../../hooks/useWorkspaceQuery", () => ({
	useCloudProjectsQuery: () => cloudProjectsState,
	workspaceQueryOptions: {
		queryKey: ["workspaces"],
		queryFn: () => Promise.resolve(localWorkspacesState.ids.map((id: string) => ({ id }))),
	},
}));
vi.mock("../CuesDialog", () => ({ CuesSettings: () => <div data-testid="project-cues-settings" /> }));
vi.mock("../ProjectScriptsSettings", () => ({
	ProjectScriptsSettings: () => <div data-testid="project-scripts-settings" />,
	CloudProjectScriptsSettings: () => <div data-testid="cloud-project-scripts-settings" />,
}));
vi.mock("../../hooks/useCloudGate", () => ({ useCloudGate: () => ({ cloudEnabled: false, localEnabled: true }) }));
vi.mock("../../hooks/useCloudOffering", () => ({ useCloudOffering: () => ({ customCoder: false, selfServeBilling: false }) }));
vi.mock("../../lib/cloud-session", () => ({ useCloudSession: () => ({ status: "unauthenticated", session: null }) }));

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

const planApproval = effectiveRule({
	ruleId: "plan.approval.required",
	name: "Plan approval required",
	builtin: true,
	protected: true,
	category: "workflow",
	enforcement: ["workflow_gate"],
	strength: "prevents",
});
const ceiling = effectiveRule({
	ruleId: "agent.permission.ceiling",
	name: "Permission ceiling",
	builtin: true,
	protected: true,
	category: "tools",
	enforcement: ["tool_policy"],
	strength: "prevents",
});
const korean = effectiveRule({
	ruleId: "git.commit.message.korean",
	name: "Commit messages in Korean",
	builtin: true,
	category: "git",
	global: "off",
	on: false,
	source: { scope: "global", scopeId: "", state: "off" },
});
const review = effectiveRule({
	ruleId: "lado-rule-review",
	name: "Ask for a review",
	category: "process",
	global: "off",
	project: "force_on",
	source: { scope: "project", scopeId: "demo", state: "force_on" },
});

function effective(fields: Partial<lado.LadoEffective> = {}): lado.LadoEffective {
	return {
		projectId: "demo",
		taskId: "",
		agent: { harness: "", model: "", source: "" },
		permissionCeiling: "auto",
		permissionMode: "auto",
		rules: [planApproval, ceiling, korean, review],
		aoRules: [],
		prompt: "",
		capabilities: [],
		problems: [],
		blocking: false,
		snapshotSha256: "e".repeat(64),
		...fields,
	};
}

const noImpact: lado.LadoRuleImpact = { projects: [], tasks: [] };
const impact: lado.LadoRuleImpact = {
	projects: [{ id: "demo", name: "Demo" }],
	tasks: [{ id: "lado-task-1", projectId: "demo", title: "Add the settings page", issueRef: "github:acme/demo#12" }],
};

function renderSection() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<LadoProjectRulesSection projectId="demo" />
		</QueryClientProvider>,
	);
}

function renderSettings() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<SettingsDialog />
		</QueryClientProvider>,
	);
}

async function findRow(ruleId: string): Promise<HTMLElement> {
	return waitFor(() => {
		const row = document.querySelector<HTMLElement>(`[data-testid="lado-effective-rule"][data-rule-id="${ruleId}"]`);
		if (!row) throw new Error(`no row for ${ruleId}`);
		return row;
	});
}

// LADO fork: Project settings → Rules (docs/LJY-PRD/HARNESS.md §12).
describe("LadoProjectRulesSection", () => {
	beforeEach(() => {
		vi.resetAllMocks();
		vi.mocked(lado.fetchLadoEffective).mockResolvedValue(effective());
		vi.mocked(lado.fetchLadoRules).mockResolvedValue([]);
		useUiStore.setState({ developerMode: false, settingsModal: null });
		cloudProjectsState.data = [];
		localWorkspacesState.ids = [];
	});

	afterEach(cleanup);

	it("overrides a rule for this project", async () => {
		const user = userEvent.setup();
		vi.mocked(lado.setLadoBinding).mockImplementation(async (input) => ({ applied: !input.dryRun, impact: noImpact }));
		renderSection();

		const koreanRow = await findRow(korean.ruleId);
		expect(lado.fetchLadoEffective).toHaveBeenCalledWith("demo", undefined);
		expect(within(koreanRow).getByText("Off · from Global")).toBeInTheDocument();
		const select = within(koreanRow).getByRole("button", { name: "Commit messages in Korean for this project" });
		expect(select).toHaveTextContent("Inherit");
		await user.click(select);
		await user.click(await screen.findByRole("menuitem", { name: "On" }));

		const binding = { ruleId: korean.ruleId, scope: "project", scopeId: "demo", state: "force_on" };
		await waitFor(() => expect(lado.setLadoBinding).toHaveBeenCalledTimes(2));
		expect(vi.mocked(lado.setLadoBinding).mock.calls).toEqual([[{ ...binding, dryRun: true }], [binding]]);
		await waitFor(() => expect(lado.fetchLadoEffective).toHaveBeenCalledTimes(2));
		expect(screen.queryByRole("dialog")).not.toBeInTheDocument();

		// Back to Inherit reaches an approved task, which is named before anything changes.
		vi.mocked(lado.setLadoBinding).mockImplementation(async (input) => ({ applied: !input.dryRun, impact }));
		const reviewRow = await findRow(review.ruleId);
		expect(within(reviewRow).getByText("On · set here")).toBeInTheDocument();
		await user.click(within(reviewRow).getByRole("button", { name: "Ask for a review for this project" }));
		await user.click(await screen.findByRole("menuitem", { name: "Inherit" }));
		const dialog = await screen.findByRole("dialog", { name: "Change the rules for approved tasks?" });
		expect(within(dialog).getByText("Add the settings page")).toBeInTheDocument();
		expect(lado.setLadoBinding).toHaveBeenCalledTimes(3);
		await user.click(within(dialog).getByRole("button", { name: "Change" }));
		await waitFor(() => expect(lado.setLadoBinding).toHaveBeenCalledTimes(4));
		expect(lado.setLadoBinding).toHaveBeenLastCalledWith({ ruleId: review.ruleId, scope: "project", scopeId: "demo", state: "inherit" });
	});

	it("shows protected rules as always on", async () => {
		renderSection();

		const approvalRow = await findRow(planApproval.ruleId);
		const ceilingRow = await findRow(ceiling.ruleId);
		expect(within(approvalRow).getByText("Plan approval required")).toBeInTheDocument();
		expect(within(approvalRow).getByText("On · from Global")).toBeInTheDocument();
		// The ceiling's state is its mode; it changes only in Settings → Rules.
		expect(within(ceilingRow).getByText("Auto · from Global")).toBeInTheDocument();
		for (const row of [approvalRow, ceilingRow]) {
			expect(within(row).getByText("Always on")).toHaveClass("text-settings-muted");
			expect(within(row).queryByRole("button")).not.toBeInTheDocument();
		}
		expect(within(await findRow(review.ruleId)).getByRole("button", { name: "Ask for a review for this project" })).toHaveTextContent("On");
	});

	it("lists AO project instructions read-only", async () => {
		vi.mocked(lado.fetchLadoEffective).mockResolvedValue(
			effective({
				aoRules: [
					{ kind: "worker_instructions", path: "", sha256: "1".repeat(64), chars: 1234, readError: "" },
					{ kind: "worker_instructions_file", path: "docs/AGENT_RULES.md", sha256: "", chars: 0, readError: "file not found" },
					{ kind: "orchestrator_instructions", path: "", sha256: "2".repeat(64), chars: 1, readError: "" },
				],
			}),
		);
		renderSection();

		const card = (await screen.findByRole("heading", { name: "From AO project config" })).closest("section")!;
		expect(within(card).getByText("LADO includes these and cannot turn them off; change them in the project's AO config.")).toBeInTheDocument();
		const rows = within(card).getAllByTestId("lado-ao-rule");
		expect(rows.map((row) => row.getAttribute("data-kind"))).toEqual(["worker_instructions", "worker_instructions_file", "orchestrator_instructions"]);
		expect(within(rows[0]).getByText("Worker instructions")).toBeInTheDocument();
		expect(within(rows[0]).getByText("1,234 characters")).toBeInTheDocument();
		expect(within(rows[1]).getByText("docs/AGENT_RULES.md")).toHaveClass("font-mono");
		expect(within(rows[1]).getByText("Could not read: file not found")).toHaveClass("text-error");
		expect(within(rows[2]).getByText("1 character")).toBeInTheDocument();
		// Nothing here changes them.
		expect(within(card).queryByRole("button")).not.toBeInTheDocument();
		expect(within(card).queryByRole("switch")).not.toBeInTheDocument();
	});

	it("creates a rule for this project", async () => {
		const user = userEvent.setup();
		const created: lado.LadoRule = {
			id: "lado-rule-new",
			name: "Run the linter",
			category: "",
			instruction: "Run the linter before every commit.",
			builtin: false,
			protected: false,
			archived: false,
			version: 1,
			versionId: "lado-rule-new-v1",
			sha256: "f".repeat(64),
			enforcement: ["prompt"],
			strength: "advisory",
			conflictsWith: [],
			global: "off",
			overrides: [{ scope: "project", scopeId: "demo", state: "force_on", label: "Demo" }],
			createdAt: "2026-10-22T09:00:00Z",
			updatedAt: "2026-10-22T09:00:00Z",
		};
		vi.mocked(lado.createLadoRule).mockImplementation(async (input) => ({ applied: !input.dryRun, impact: noImpact, rule: created }));
		renderSection();
		await findRow(review.ruleId);

		await user.click(screen.getByRole("button", { name: "New rule for this project" }));
		const dialog = await screen.findByRole("dialog", { name: "New rule for this project" });
		// The rule is on for this project only, so there is no switch for all of them.
		expect(within(dialog).queryByRole("switch")).not.toBeInTheDocument();
		await user.type(within(dialog).getByLabelText("Name"), "Run the linter");
		await user.type(within(dialog).getByLabelText("Instruction"), "Run the linter before every commit.");
		await user.click(within(dialog).getByRole("button", { name: "Create" }));

		const input = {
			name: "Run the linter",
			category: "",
			instruction: "Run the linter before every commit.",
			conflictsWith: [],
			scope: "project",
			scopeId: "demo",
			on: true,
		};
		await waitFor(() => expect(lado.createLadoRule).toHaveBeenCalledTimes(2));
		expect(vi.mocked(lado.createLadoRule).mock.calls).toEqual([[{ ...input, dryRun: true }], [input]]);
		await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
		await waitFor(() => expect(lado.fetchLadoEffective).toHaveBeenCalledTimes(2));
	});

	it("adds a Rules page to local LADO projects only", async () => {
		const user = userEvent.setup();
		localWorkspacesState.ids = ["demo", "plain"];
		vi.mocked(lado.fetchLadoProjectSettings).mockImplementation(async (projectId) => ({ projectId, ladoMode: projectId === "demo" }));

		useUiStore.getState().openProjectSettings("demo");
		renderSettings();
		await user.click(await screen.findByRole("button", { name: "Rules" }));
		expect(await screen.findByRole("heading", { name: "Rules for this project" })).toBeInTheDocument();
		await findRow(review.ruleId);
		expect(lado.fetchLadoEffective).toHaveBeenCalledWith("demo", undefined);
		cleanup();

		// A local project outside LADO mode keeps AO's pages only.
		useUiStore.setState({ settingsModal: null });
		useUiStore.getState().openProjectSettings("plain");
		renderSettings();
		await waitFor(() => expect(lado.fetchLadoProjectSettings).toHaveBeenCalledWith("plain"));
		await act(async () => undefined);
		expect(screen.getByRole("button", { name: "Scripts" })).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Rules" })).not.toBeInTheDocument();
		cleanup();

		// Remote and cloud projects never ask this computer's daemon about LADO.
		vi.mocked(lado.fetchLadoProjectSettings).mockClear();
		useUiStore.setState({ settingsModal: null });
		useUiStore.getState().openProjectSettings("demo", "box-a");
		renderSettings();
		expect(await screen.findByRole("button", { name: "Agents" })).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Rules" })).not.toBeInTheDocument();
		cleanup();

		cloudProjectsState.data = [{ id: "demo", orgId: "org-1", displayName: "Demo" }];
		useUiStore.setState({ settingsModal: null });
		useUiStore.getState().openProjectSettings("demo");
		renderSettings();
		expect(await screen.findByTestId("project-settings-form")).toHaveTextContent("org-1");
		expect(screen.queryByRole("button", { name: "Rules" })).not.toBeInTheDocument();
		expect(lado.fetchLadoProjectSettings).not.toHaveBeenCalled();
	});
});
