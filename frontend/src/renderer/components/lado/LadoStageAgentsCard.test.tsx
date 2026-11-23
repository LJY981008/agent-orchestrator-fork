import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AgentModelCatalog } from "../../hooks/useAgentModelsQuery";
import { LadoApiError } from "../../lib/lado";
import * as stageAgents from "../../lib/lado-stage-agents";
import { LadoStageAgentsCard } from "./LadoStageAgentsCard";

vi.mock("../../lib/lado-stage-agents", async (original) => ({
	...(await original<typeof import("../../lib/lado-stage-agents")>()),
	fetchLadoStageAgents: vi.fn(),
	fetchLadoProjectStageAgents: vi.fn(),
	updateLadoStageAgents: vi.fn(),
	updateLadoProjectStageAgents: vi.fn(),
}));

const { catalogs } = vi.hoisted(() => ({ catalogs: {} as Record<string, AgentModelCatalog> }));

// The model lists come from the daemon's catalog; here each harness has a fixed one.
vi.mock("../../hooks/useAgentModelsQuery", async (original) => ({
	...(await original<typeof import("../../hooks/useAgentModelsQuery")>()),
	agentModelsQueryOptions: (agentId: string, projectId: string) => ({
		queryKey: ["agent-models", agentId, projectId],
		queryFn: async () => catalogs[agentId],
		enabled: agentId !== "",
	}),
	refreshAgentModels: vi.fn(),
}));

function catalog(agentId: string, models: AgentModelCatalog["models"], allowCustom = false): AgentModelCatalog {
	return { agentId, allowCustom, customModelEntry: allowCustom ? "direct" : "none", fetchedAt: "2026-11-23T00:00:00Z", models };
}

const global = (stages: stageAgents.LadoStageAgentsByStage): stageAgents.LadoStageAgents => ({ stages });

function projectRow(fields: Partial<stageAgents.LadoProjectStageAgent> & Pick<stageAgents.LadoProjectStageAgent, "stage">): stageAgents.LadoProjectStageAgent {
	return {
		harness: "claude-code",
		projectApplies: false,
		globalApplies: false,
		effective: { harness: "claude-code", model: "opus", effort: "high", source: "orchestrator" },
		...fields,
	};
}

function projectView(rows: Partial<Record<stageAgents.LadoStage, Partial<stageAgents.LadoProjectStageAgent>>> = {}): stageAgents.LadoProjectStageAgents {
	return {
		projectId: "demo",
		stages: stageAgents.LADO_STAGES.map((stage) => projectRow({ stage, ...rows[stage] })),
	};
}

function renderCard(projectId?: string) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<LadoStageAgentsCard projectId={projectId} />
		</QueryClientProvider>,
	);
}

const modelOf = (stage: string) => screen.getByRole("button", { name: `${stage} model` });
const effortOf = (stage: string) => screen.getByRole("button", { name: `${stage} effort` });
const rowOf = (stage: string) => screen.getByTestId(`lado-stage-row-${stage}`);

beforeEach(() => {
	vi.resetAllMocks();
	catalogs["claude-code"] = catalog("claude-code", [
		{ id: "claude-opus-5-5", label: "Claude Opus 5.5", efforts: ["low", "medium", "high"], isDefault: true },
		{ id: "claude-fable-5-2", label: "Claude Fable 5.2", efforts: ["low", "high", "max"] },
	]);
	catalogs.codex = catalog("codex", [{ id: "gpt-6", label: "GPT-6", efforts: ["low", "high"] }], true);
});

afterEach(cleanup);

// LADO fork: the stage models (issue #103).
describe("LadoStageAgentsCard", () => {
	it("splits the stages into the cards before and at an approval, each with what a change does", async () => {
		vi.mocked(stageAgents.fetchLadoStageAgents).mockResolvedValue(global({}));
		renderCard();

		const execution = await screen.findByTestId("lado-stage-agents-execution");
		const plan = screen.getByTestId("lado-stage-agents-plan");
		expect(within(plan).getByTestId("lado-stage-agents-plan-note")).toHaveTextContent("touches no approval and applies from the next plan request");
		// A global value reaches every project's waiting approvals.
		expect(within(execution).getByTestId("lado-stage-agents-execution-note")).toHaveTextContent("global values reach every project");
		expect(within(execution).getByTestId("lado-stage-agents-execution-note")).toHaveTextContent("need a new approval");
		expect(within(plan).getByText("Plan worker")).toBeInTheDocument();
		expect(within(plan).getByText("Plan review")).toBeInTheDocument();
		for (const stage of ["Execution worker", "Integration worker", "Resolve worker"]) {
			expect(within(execution).getByText(stage)).toBeInTheDocument();
		}
	});

	it("shows an unset stage as following the default, with what it inherits, and its effort off", async () => {
		vi.mocked(stageAgents.fetchLadoStageAgents).mockResolvedValue(global({}));
		renderCard();

		await screen.findByTestId("lado-stage-row-plan");
		await waitFor(() => expect(modelOf("Plan worker")).toBeEnabled());
		expect(modelOf("Plan worker")).toHaveTextContent("Follow default");
		expect(rowOf("plan")).toHaveTextContent("Unset, it runs the project orchestrator's model and effort.");
		expect(rowOf("critic")).toHaveTextContent("Unset, it runs the plan stage's value.");
		expect(rowOf("integration")).toHaveTextContent("Unset, it runs the execution stage's value.");
		// An effort goes with a model.
		expect(effortOf("Plan worker")).toBeDisabled();
		expect(screen.queryByRole("button", { name: "Plan worker: use default" })).not.toBeInTheDocument();
	});

	it("shows a model without an effort as AO's default and saves the whole scope on a change", async () => {
		const user = userEvent.setup();
		vi.mocked(stageAgents.fetchLadoStageAgents).mockResolvedValue(
			global({
				plan: { harness: "claude-code", model: "claude-fable-5-2", effort: "" },
				execution: { harness: "codex", model: "gpt-6", effort: "high" },
			}),
		);
		vi.mocked(stageAgents.updateLadoStageAgents).mockImplementation(async (stages) => global(stages));
		renderCard();

		await screen.findByTestId("lado-stage-row-plan");
		await waitFor(() => expect(modelOf("Plan worker")).toHaveTextContent("Fable 5.2"));
		expect(effortOf("Plan worker")).toHaveTextContent("AO default");
		expect(effortOf("Plan worker")).toBeEnabled();
		expect(screen.getByRole("button", { name: "Execution worker harness" })).toHaveTextContent("Codex");

		await user.click(effortOf("Plan worker"));
		await user.click(await screen.findByRole("menuitem", { name: "Max" }));
		await waitFor(() =>
			expect(stageAgents.updateLadoStageAgents).toHaveBeenCalledWith({
				plan: { harness: "claude-code", model: "claude-fable-5-2", effort: "max" },
				execution: { harness: "codex", model: "gpt-6", effort: "high" },
			}),
		);

		// Back to the default: the stage leaves the scope.
		await user.click(await screen.findByRole("button", { name: "Execution worker: use default" }));
		await waitFor(() =>
			expect(stageAgents.updateLadoStageAgents).toHaveBeenLastCalledWith({
				plan: { harness: "claude-code", model: "claude-fable-5-2", effort: "max" },
			}),
		);
	});

	it("picks a model with no effort, and drops an effort the new model does not offer", async () => {
		const user = userEvent.setup();
		vi.mocked(stageAgents.fetchLadoStageAgents).mockResolvedValue(global({ critic: { harness: "claude-code", model: "claude-fable-5-2", effort: "max" } }));
		vi.mocked(stageAgents.updateLadoStageAgents).mockImplementation(async (stages) => global(stages));
		renderCard();

		await screen.findByTestId("lado-stage-row-plan");
		await waitFor(() => expect(modelOf("Plan worker")).toBeEnabled());
		await user.click(modelOf("Plan worker"));
		await user.click(await screen.findByRole("menuitem", { name: /Opus 5\.5/ }));
		await waitFor(() =>
			expect(stageAgents.updateLadoStageAgents).toHaveBeenCalledWith({
				plan: { harness: "claude-code", model: "claude-opus-5-5", effort: "" },
				critic: { harness: "claude-code", model: "claude-fable-5-2", effort: "max" },
			}),
		);

		await user.click(modelOf("Plan review"));
		await user.click(await screen.findByRole("menuitem", { name: /Opus 5\.5/ }));
		await waitFor(() =>
			expect(stageAgents.updateLadoStageAgents).toHaveBeenLastCalledWith({
				plan: { harness: "claude-code", model: "claude-opus-5-5", effort: "" },
				critic: { harness: "claude-code", model: "claude-opus-5-5", effort: "" },
			}),
		);
	});

	it("shows the daemon's refusal under the stage that changed and keeps the saved value", async () => {
		const user = userEvent.setup();
		vi.mocked(stageAgents.fetchLadoStageAgents).mockResolvedValue(global({ resolve: { harness: "claude-code", model: "claude-opus-5-5", effort: "" } }));
		vi.mocked(stageAgents.updateLadoStageAgents).mockRejectedValue(
			new LadoApiError("no", "LADO_STAGE_EFFORT_UNSUPPORTED", { stage: "resolve", model: "claude-opus-5-5", effort: "high" }),
		);
		renderCard();

		await screen.findByTestId("lado-stage-row-execution");
		await waitFor(() => expect(effortOf("Resolve worker")).toBeEnabled());
		await user.click(effortOf("Resolve worker"));
		await user.click(await screen.findByRole("menuitem", { name: "High" }));

		expect(await within(rowOf("resolve")).findByRole("alert")).toHaveTextContent(
			"claude-opus-5-5 does not offer the effort high for the Resolve worker.",
		);
		expect(within(rowOf("plan")).queryByRole("alert")).not.toBeInTheDocument();
		expect(effortOf("Resolve worker")).toHaveTextContent("AO default");
	});

	it("names in a project what each stage runs with now, and why a value does not apply", async () => {
		vi.mocked(stageAgents.fetchLadoProjectStageAgents).mockResolvedValue(
			projectView({
				plan: {
					global: { harness: "claude-code", model: "claude-fable-5-2", effort: "" },
					globalApplies: true,
					effective: { harness: "claude-code", model: "claude-fable-5-2", effort: "", source: "global" },
				},
				critic: { effective: { harness: "claude-code", model: "claude-fable-5-2", effort: "", source: "plan_stage" } },
				execution: {
					harness: "codex",
					global: { harness: "claude-code", model: "claude-opus-5-5", effort: "" },
					globalWarning: "harness_mismatch",
					project: { harness: "claude-code", model: "claude-opus-5-5", effort: "" },
					projectReason: "harness_mismatch",
					effective: { harness: "codex", model: "gpt-6", effort: "", source: "worker" },
				},
				integration: {
					harness: "codex",
					global: { harness: "codex", model: "gpt-5", effort: "" },
					globalApplies: true,
					globalWarning: "model_unavailable",
					effective: { harness: "codex", model: "gpt-5", effort: "", source: "global" },
				},
			}),
		);
		renderCard("demo");

		await screen.findByTestId("lado-stage-row-plan");
		expect(stageAgents.fetchLadoProjectStageAgents).toHaveBeenCalledWith("demo");
		expect(within(rowOf("plan")).getByTestId("lado-stage-effective")).toHaveTextContent("Runs with claude-fable-5-2 · AO default · global default");
		expect(within(rowOf("critic")).getByTestId("lado-stage-effective")).toHaveTextContent("the plan stage's value");
		expect(within(rowOf("execution")).getByTestId("lado-stage-project-mismatch")).toHaveTextContent(
			"This project's value is for Claude Code, but the stage runs Codex",
		);
		expect(within(rowOf("execution")).getByTestId("lado-stage-global-warning")).toHaveTextContent("is for another harness");
		expect(within(rowOf("integration")).getByTestId("lado-stage-global-warning")).toHaveTextContent(
			"does not offer the global value gpt-5",
		);
		// A project's stage runs the project's harness: there is none to pick.
		expect(screen.queryByRole("button", { name: "Plan worker harness" })).not.toBeInTheDocument();
		expect(screen.getByTestId("lado-stage-agents-execution-note")).not.toHaveTextContent("every project");
		await waitFor(() => expect(modelOf("Plan worker")).toHaveTextContent("Follow default"));
	});

	it("saves a project's values with the stage's harness", async () => {
		const user = userEvent.setup();
		vi.mocked(stageAgents.fetchLadoProjectStageAgents).mockResolvedValue(
			projectView({ execution: { harness: "codex", effective: { harness: "codex", model: "gpt-6", effort: "", source: "worker" } } }),
		);
		vi.mocked(stageAgents.updateLadoProjectStageAgents).mockImplementation(async () => projectView());
		renderCard("demo");

		await screen.findByTestId("lado-stage-row-execution");
		await waitFor(() => expect(modelOf("Execution worker")).toBeEnabled());
		await user.click(modelOf("Execution worker"));
		await user.click(await screen.findByRole("menuitem", { name: /GPT-6/ }));
		await waitFor(() =>
			expect(stageAgents.updateLadoProjectStageAgents).toHaveBeenCalledWith("demo", {
				execution: { harness: "codex", model: "gpt-6", effort: "" },
			}),
		);
	});
});
