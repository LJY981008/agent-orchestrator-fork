import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import * as lado from "../../lib/lado";
import { RunsSection } from "./RunsSection";

vi.mock("../../lib/lado", async (original) => ({
	...(await original<typeof import("../../lib/lado")>()),
	fetchLadoDispatchSettings: vi.fn(),
	updateLadoDispatchSettings: vi.fn(),
}));

// What the daemon answers before anyone saved: 2 at once, 8 hours, hold on.
const defaults: lado.LadoDispatchSettings = { maxConcurrent: 2, maxRunMinutes: 480, holdOnMemoryTight: true };

function renderSection() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<RunsSection />
		</QueryClientProvider>,
	);
}

const concurrent = () => screen.getByRole("button", { name: "Concurrent runs" });
const runTime = () => screen.getByRole("button", { name: "Maximum run time" });
const memory = () => screen.getByRole("switch", { name: "Wait while memory is tight" });
const rowOf = (control: HTMLElement) => control.closest<HTMLElement>(".settings-row-bar")!;

async function choose(trigger: HTMLElement, option: string) {
	const user = userEvent.setup();
	await user.click(trigger);
	await user.click(await screen.findByRole("menuitem", { name: option }));
}

// LADO fork: Settings → Runs (docs/LJY-PRD/TDD.md §5.3).
describe("RunsSection", () => {
	beforeEach(() => {
		vi.resetAllMocks();
		vi.mocked(lado.fetchLadoDispatchSettings).mockResolvedValue(defaults);
		vi.mocked(lado.updateLadoDispatchSettings).mockImplementation(async (input) => ({
			...input,
			updatedAt: "2026-10-23T10:00:00Z",
		}));
	});

	afterEach(cleanup);

	it("shows the saved settings, the daemon's defaults before any save", async () => {
		renderSection();

		expect(await screen.findByRole("button", { name: "Concurrent runs" })).toHaveTextContent("2");
		expect(runTime()).toHaveTextContent("8 hours");
		expect(memory()).toBeChecked();
		expect(screen.getByText("These apply only to approved tasks in projects with LADO mode on.")).toBeInTheDocument();
		expect(screen.getByText("Queued tasks wait to start while the computer is low on memory.")).toBeInTheDocument();
	});

	it("offers 1 to 8 runs at once and the run times from off to 24 hours", async () => {
		const user = userEvent.setup();
		renderSection();

		await user.click(await screen.findByRole("button", { name: "Concurrent runs" }));
		expect((await screen.findAllByRole("menuitem")).map((item) => item.textContent)).toEqual(["1", "2", "3", "4", "5", "6", "7", "8"]);
		await user.keyboard("{Escape}");

		await user.click(runTime());
		expect((await screen.findAllByRole("menuitem")).map((item) => item.textContent)).toEqual([
			"Off",
			"1 hour",
			"2 hours",
			"4 hours",
			"8 hours",
			"12 hours",
			"24 hours",
		]);
	});

	it("saves all three settings together whenever one of them changes", async () => {
		const user = userEvent.setup();
		renderSection();

		await choose(await screen.findByRole("button", { name: "Concurrent runs" }), "4");
		await waitFor(() => expect(concurrent()).toHaveTextContent("4"));
		expect(lado.updateLadoDispatchSettings).toHaveBeenLastCalledWith({ maxConcurrent: 4, maxRunMinutes: 480, holdOnMemoryTight: true });

		await waitFor(() => expect(runTime()).toBeEnabled());
		await choose(runTime(), "Off");
		await waitFor(() => expect(runTime()).toHaveTextContent("Off"));
		expect(lado.updateLadoDispatchSettings).toHaveBeenLastCalledWith({ maxConcurrent: 4, maxRunMinutes: 0, holdOnMemoryTight: true });

		await waitFor(() => expect(memory()).toBeEnabled());
		await user.click(memory());
		await waitFor(() => expect(memory()).not.toBeChecked());
		expect(lado.updateLadoDispatchSettings).toHaveBeenLastCalledWith({ maxConcurrent: 4, maxRunMinutes: 0, holdOnMemoryTight: false });

		await waitFor(() => expect(runTime()).toBeEnabled());
		await choose(runTime(), "24 hours");
		await waitFor(() => expect(lado.updateLadoDispatchSettings).toHaveBeenLastCalledWith({ maxConcurrent: 4, maxRunMinutes: 1440, holdOnMemoryTight: false }));
		expect(lado.updateLadoDispatchSettings).toHaveBeenCalledTimes(4);
	});

	it("does not save a value that is already set", async () => {
		renderSection();

		await choose(await screen.findByRole("button", { name: "Concurrent runs" }), "2");
		await choose(runTime(), "8 hours");
		expect(lado.updateLadoDispatchSettings).not.toHaveBeenCalled();
	});

	it("shows the daemon's refusal under the row that changed and keeps the saved value", async () => {
		vi.mocked(lado.updateLadoDispatchSettings).mockRejectedValue(
			new lado.LadoApiError("maxRunMinutes must be one of the offered values", "LADO_DISPATCH_SETTINGS_INVALID", { min: 1, max: 8 }),
		);
		renderSection();

		await choose(await screen.findByRole("button", { name: "Maximum run time" }), "12 hours");
		const alert = await screen.findByRole("alert");
		expect(alert).toHaveTextContent("Choose 1 to 8 concurrent runs and a maximum run time from the list.");
		expect(rowOf(runTime())).toContainElement(alert);
		expect(runTime()).toHaveTextContent("8 hours");
		expect(runTime()).toBeEnabled();

		// A switch row keeps its own line and adds the refusal under it.
		vi.mocked(lado.updateLadoDispatchSettings).mockRejectedValue(new lado.LadoApiError("internal", "LADO_DISPATCH_SETTINGS_UPDATE_FAILED"));
		await userEvent.setup().click(memory());
		await waitFor(() => expect(rowOf(memory())).toContainElement(screen.getByRole("alert")));
		expect(screen.getAllByRole("alert")).toHaveLength(1);
		expect(rowOf(memory())).toHaveTextContent("Queued tasks wait to start while the computer is low on memory.");
		expect(memory()).toBeChecked();
	});

	it("turns the controls off while a change saves", async () => {
		let finish: (settings: lado.LadoDispatchSettings) => void = () => undefined;
		vi.mocked(lado.updateLadoDispatchSettings).mockImplementation(
			() => new Promise<lado.LadoDispatchSettings>((resolve) => { finish = resolve; }),
		);
		const user = userEvent.setup();
		renderSection();

		await user.click(await screen.findByRole("switch", { name: "Wait while memory is tight" }));
		// The change shows at once, and nothing else can change until it is saved.
		await waitFor(() => expect(memory()).toBeDisabled());
		expect(memory()).not.toBeChecked();
		expect(concurrent()).toBeDisabled();
		expect(runTime()).toBeDisabled();

		finish({ ...defaults, holdOnMemoryTight: false });
		await waitFor(() => expect(memory()).toBeEnabled());
		expect(memory()).not.toBeChecked();
		expect(concurrent()).toBeEnabled();
		expect(runTime()).toBeEnabled();
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
	});

	it("explains settings it could not load", async () => {
		vi.mocked(lado.fetchLadoDispatchSettings).mockRejectedValue(new lado.LadoApiError("internal", "LADO_DISPATCH_SETTINGS_LOAD_FAILED"));
		renderSection();

		expect(await screen.findByRole("alert")).toHaveTextContent("LADO could not read or save its data.");
		expect(screen.queryByRole("switch")).not.toBeInTheDocument();
		expect(within(document.body).queryByRole("button", { name: "Concurrent runs" })).not.toBeInTheDocument();
	});
});
