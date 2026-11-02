import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { appI18n } from "../../i18n";
import { apiClient } from "../../lib/api-client";
import { aoBridge } from "../../lib/bridge";
import * as chrome from "../../lib/lado-chrome";
import { LadoClaudeChromeSection } from "./LadoClaudeChromeSection";

vi.mock("../../lib/lado-chrome", async (original) => ({
	...(await original<typeof import("../../lib/lado-chrome")>()),
	fetchLadoClaudeChrome: vi.fn(),
}));

const defaults: chrome.LadoClaudeChromeSettings = { chatSessions: true, ladoWorkers: false };
// What the daemon holds: a change it accepts is what the next read gets.
let held: chrome.LadoClaudeChromeSettings = defaults;

// The desktop app's confirmation of turning Claude in Chrome on (lib/lado-chrome.ts).
const confirmOn = () => vi.mocked(aoBridge.lado.presence.changeClaudeChrome);
const put = () => vi.mocked(apiClient.PUT);
const chatSwitch = () => screen.findByRole("switch", { name: "Chat sessions" });
const workersSwitch = () => screen.findByRole("switch", { name: "LADO workers" });

function renderSection() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	const invalidate = vi.spyOn(client, "invalidateQueries");
	render(
		<QueryClientProvider client={client}>
			<LadoClaudeChromeSection />
		</QueryClientProvider>,
	);
	return { client, invalidate };
}

// LADO fork: Settings → General → Claude in Chrome (issue #40).
describe("LadoClaudeChromeSection", () => {
	beforeEach(async () => {
		vi.resetAllMocks();
		await appI18n.changeLanguage("en");
		held = defaults;
		vi.mocked(chrome.fetchLadoClaudeChrome).mockImplementation(async () => held);
		vi.spyOn(aoBridge.lado.presence, "changeClaudeChrome").mockResolvedValue({ status: "cancelled" });
		vi.spyOn(apiClient, "PUT").mockImplementation((async (_path: string, init: { body: chrome.LadoClaudeChromeSettings }) => {
			held = init.body;
			return { data: init.body, error: undefined };
		}) as never);
	});

	afterEach(() => {
		cleanup();
	});

	it("draws both switches from the daemon's setting and says when a change applies", async () => {
		renderSection();
		expect(await chatSwitch()).toBeChecked();
		expect(await workersSwitch()).not.toBeChecked();
		expect(screen.getByText("Claude in Chrome")).toBeInTheDocument();
		expect(screen.getByText(/applies to sessions that start afterwards/)).toBeInTheDocument();
	});

	it("turns a part off with a plain call, without the desktop confirmation", async () => {
		const user = userEvent.setup();
		const { invalidate } = renderSection();
		await user.click(await chatSwitch());

		await waitFor(() => expect(put()).toHaveBeenCalledWith("/api/v1/lado/claude-chrome", { body: { chatSessions: false, ladoWorkers: false } }));
		expect(confirmOn()).not.toHaveBeenCalled();
		await waitFor(() => expect(screen.getByRole("switch", { name: "Chat sessions" })).not.toBeChecked());
		// Every session's state reads the setting for its next start.
		expect(invalidate).toHaveBeenCalledWith({ queryKey: ["lado", "claude-chrome"] });
	});

	it("turns a part on through the desktop confirmation", async () => {
		const user = userEvent.setup();
		confirmOn().mockImplementation(async (settings) => {
			held = settings;
			return { status: "changed", settings };
		});
		const { invalidate } = renderSection();
		await user.click(await workersSwitch());

		await waitFor(() => expect(confirmOn()).toHaveBeenCalledWith({ chatSessions: true, ladoWorkers: true }));
		expect(put()).not.toHaveBeenCalled();
		await waitFor(() => expect(screen.getByRole("switch", { name: "LADO workers" })).toBeChecked());
		expect(invalidate).toHaveBeenCalledWith({ queryKey: ["lado", "claude-chrome"] });
	});

	it("keeps the switch off after a cancelled confirmation", async () => {
		const user = userEvent.setup();
		const { invalidate } = renderSection();
		await user.click(await workersSwitch());

		await waitFor(() => expect(confirmOn()).toHaveBeenCalled());
		await waitFor(() => expect(screen.getByRole("switch", { name: "LADO workers" })).toBeEnabled());
		expect(screen.getByRole("switch", { name: "LADO workers" })).not.toBeChecked();
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
		expect(invalidate).not.toHaveBeenCalled();
	});

	it("shows a refusal under the row that changed", async () => {
		const user = userEvent.setup();
		confirmOn().mockResolvedValue({
			status: "error",
			code: "LADO_PRESENCE_REPLAYED",
			message: "This confirmation was already used.",
		});
		renderSection();
		await user.click(await workersSwitch());

		const alert = await screen.findByRole("alert");
		expect(alert.textContent).not.toBe("");
		expect(screen.getByRole("switch", { name: "LADO workers" })).not.toBeChecked();
	});

	it("shows nothing when the setting cannot be read", async () => {
		vi.mocked(chrome.fetchLadoClaudeChrome).mockRejectedValue(new Error("down"));
		renderSection();
		await waitFor(() => expect(chrome.fetchLadoClaudeChrome).toHaveBeenCalled());
		// The app reports a daemon that is down elsewhere; this adds no second alert.
		await new Promise((resolve) => setTimeout(resolve, 20));
		expect(screen.queryByText("Claude in Chrome")).not.toBeInTheDocument();
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
	});
});
