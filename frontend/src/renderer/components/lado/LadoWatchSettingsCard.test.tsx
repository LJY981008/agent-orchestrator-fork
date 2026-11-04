import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import * as lado from "../../lib/lado";
import { TooltipProvider } from "../ui/tooltip";
import { LadoWatchSettingsCard } from "./LadoWatchSettingsCard";

vi.mock("../../lib/lado", async (original) => ({
	...(await original<typeof import("../../lib/lado")>()),
	fetchLadoWatchSettings: vi.fn(),
	updateLadoWatchSettings: vi.fn(),
}));

const defaults: lado.LadoWatchSettingsResolved = {
	compactWindow: 200_000,
	taskCostCents: 100_00,
	sessionCostCents: 30_00,
	sessionCompactions: 3,
	stallMinutes: 120,
	wakeEnabled: true,
};

const empty: lado.LadoWatchSettingsValues = {
	compactWindow: null,
	taskCostCents: null,
	sessionCostCents: null,
	sessionCompactions: null,
	stallMinutes: null,
	wakeEnabled: null,
};

function settings(projectId: string, values: Partial<lado.LadoWatchSettingsValues>, inherited = defaults): lado.LadoWatchSettings {
	const row = { ...empty, ...values };
	const effective = { ...inherited };
	for (const [key, value] of Object.entries(row)) {
		if (value !== null) (effective as Record<string, unknown>)[key] = value;
	}
	return { projectId, defaults, inherited, effective, values: row };
}

function renderCard(projectId?: string) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<TooltipProvider delayDuration={0}>
				<LadoWatchSettingsCard projectId={projectId} />
			</TooltipProvider>
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	vi.resetAllMocks();
});

afterEach(cleanup);

describe("LadoWatchSettingsCard", () => {
	it("says a change applies from the next run and shows the global row over the defaults", async () => {
		vi.mocked(lado.fetchLadoWatchSettings).mockResolvedValue(settings("", { taskCostCents: 50_00 }));
		renderCard();

		const card = await screen.findByTestId("lado-watch-settings");
		expect(within(card).getByTestId("lado-watch-note")).toHaveTextContent("Applies from the next run");
		expect(lado.fetchLadoWatchSettings).toHaveBeenCalledWith(undefined);
		expect(await within(card).findByRole("button", { name: "Compact window" })).toHaveTextContent("Default (200K tokens)");
		expect(within(card).getByRole("button", { name: "Task cost alert" })).toHaveTextContent("$50");
		expect(within(card).getByRole("button", { name: "Stall time" })).toHaveTextContent("Default (2 h)");
		// Without a project there is no scope to pick.
		expect(within(card).queryByRole("button", { name: "Settings for" })).not.toBeInTheDocument();
	});

	it("saves the whole row, a picked value with the others as they are", async () => {
		const user = userEvent.setup();
		vi.mocked(lado.fetchLadoWatchSettings).mockResolvedValue(settings("", { taskCostCents: 50_00 }));
		vi.mocked(lado.updateLadoWatchSettings).mockImplementation(async (_projectId, values) => settings("", values));
		renderCard();

		await user.click(await screen.findByRole("button", { name: "Compact window" }));
		await user.click(await screen.findByRole("menuitem", { name: "100K tokens" }));
		await waitFor(() =>
			expect(lado.updateLadoWatchSettings).toHaveBeenCalledWith(undefined, { ...empty, taskCostCents: 50_00, compactWindow: 100_000 }),
		);
	});

	it("edits the project's overrides, which follow the global values until set, and switches to the global defaults", async () => {
		const user = userEvent.setup();
		const global = settings("", { compactWindow: 300_000 });
		vi.mocked(lado.fetchLadoWatchSettings).mockImplementation(async (projectId) =>
			projectId ? settings(projectId, { wakeEnabled: false }, global.effective) : global,
		);
		vi.mocked(lado.updateLadoWatchSettings).mockImplementation(async (projectId, values) =>
			settings(projectId ?? "", values, global.effective),
		);
		renderCard("demo");

		const card = await screen.findByTestId("lado-watch-settings");
		expect(within(card).getByTestId("lado-watch-note")).toHaveTextContent("Applies from the next run");
		expect(await within(card).findByRole("button", { name: "Compact window" })).toHaveTextContent("Global (300K tokens)");
		expect(within(card).getByRole("button", { name: "Wake workers" })).toHaveTextContent("Off");
		expect(lado.fetchLadoWatchSettings).toHaveBeenCalledWith("demo");

		// Back to following the global value.
		await user.click(within(card).getByRole("button", { name: "Wake workers" }));
		await user.click(await screen.findByRole("menuitem", { name: "Global (On)" }));
		await waitFor(() => expect(lado.updateLadoWatchSettings).toHaveBeenCalledWith("demo", empty));

		await user.click(within(card).getByRole("button", { name: "Settings for" }));
		await user.click(await screen.findByRole("menuitem", { name: "Global defaults" }));
		expect(await within(card).findByRole("button", { name: "Compact window" })).toHaveTextContent("300K tokens");
		expect(lado.fetchLadoWatchSettings).toHaveBeenCalledWith(undefined);
	});

	it("shows the daemon's refusal under the row", async () => {
		const user = userEvent.setup();
		vi.mocked(lado.fetchLadoWatchSettings).mockResolvedValue(settings("", {}));
		vi.mocked(lado.updateLadoWatchSettings).mockRejectedValue(
			new lado.LadoApiError("compactWindow out of range", "LADO_WATCH_SETTING_OUT_OF_RANGE"),
		);
		renderCard();

		await user.click(await screen.findByRole("button", { name: "Session cost alert" }));
		await user.click(await screen.findByRole("menuitem", { name: "$50" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("That value is out of range. Pick one from the list.");
	});
});
