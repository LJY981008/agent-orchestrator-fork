import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import * as lado from "../../lib/lado";
import { LadoProjectOverlapCard } from "./LadoProjectOverlapCard";

vi.mock("../../lib/lado", async (original) => ({
	...(await original<typeof import("../../lib/lado")>()),
	updateLadoProjectOverlap: vi.fn(),
}));

const settings: lado.LadoProjectSettings = {
	projectId: "demo",
	ladoMode: true,
	maxConcurrent: 2,
	checkCommands: [],
	checkCommandsSha256: "4f53cda18c2baa0c0354bb5f9a3ecbe5ed12ab4d8e11ba873c2f11161202b945",
	checkTimeoutMinutes: 30,
	overlapWait: true,
	overlapIgnore: ["docs/**", "DESIGN.md"],
	overlapIgnoreIsDefault: true,
};

function renderCard(current = settings) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<LadoProjectOverlapCard projectId="demo" settings={current} />
		</QueryClientProvider>,
	);
}

afterEach(() => {
	cleanup();
	vi.clearAllMocks();
});

beforeEach(() => {
	vi.mocked(lado.updateLadoProjectOverlap).mockImplementation(async (_projectId, input) => ({
		...settings,
		...(input.overlapWait !== undefined ? { overlapWait: input.overlapWait } : {}),
		...(input.overlapIgnore ? { overlapIgnore: input.overlapIgnore, overlapIgnoreIsDefault: false } : {}),
	}));
});

describe("LadoProjectOverlapCard", () => {
	it("turns the overlap wait off at once", async () => {
		const user = userEvent.setup();
		renderCard();
		expect(screen.getByTestId("lado-overlap-ignore-count")).toHaveTextContent("LADO's 2 default patterns.");
		await user.click(screen.getByRole("switch", { name: "Wait for overlapping tasks" }));
		await waitFor(() => expect(lado.updateLadoProjectOverlap).toHaveBeenCalledWith("demo", { overlapWait: false }));
	});

	it("saves the edited patterns, one per line, and goes back to the defaults", async () => {
		const user = userEvent.setup();
		renderCard();
		await user.click(screen.getByRole("button", { name: "Files that may overlap" }));
		const field = await screen.findByRole("textbox", { name: "Patterns" });
		expect(field).toHaveValue("docs/**\nDESIGN.md");
		await user.clear(field);
		await user.type(field, "docs/**{enter}{enter}  locales/*.json  ");
		await user.click(screen.getByRole("button", { name: "Save" }));
		await waitFor(() => expect(lado.updateLadoProjectOverlap).toHaveBeenCalledWith("demo", { overlapIgnore: ["docs/**", "locales/*.json"] }));
		await waitFor(() => expect(screen.queryByTestId("lado-overlap-ignore")).not.toBeInTheDocument());

		cleanup();
		renderCard({ ...settings, overlapIgnore: ["x/**"], overlapIgnoreIsDefault: false });
		expect(screen.getByTestId("lado-overlap-ignore-count")).toHaveTextContent("1 pattern");
		await user.click(screen.getByRole("button", { name: "Files that may overlap" }));
		await user.click(await screen.findByRole("button", { name: "Use the defaults" }));
		await waitFor(() => expect(lado.updateLadoProjectOverlap).toHaveBeenLastCalledWith("demo", { overlapIgnoreDefaults: true }));
	});
});
