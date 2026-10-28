import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useLadoProjectSettings } from "../../hooks/useLado";
import { apiClient } from "../../lib/api-client";
import { aoBridge } from "../../lib/bridge";
import * as lado from "../../lib/lado";
import { LadoProjectChecksCard } from "./LadoProjectChecksCard";

vi.mock("../../lib/lado", async (original) => ({
	...(await original<typeof import("../../lib/lado")>()),
	fetchLadoProjectSettings: vi.fn(),
}));

const commands: lado.LadoCheckCommand[] = [
	{ name: "lint", command: "npm run lint" },
	{ name: "unit tests", command: "npm test" },
];
const settings: lado.LadoProjectSettings = { projectId: "demo", ladoMode: true, maxConcurrent: 1, checkCommands: commands, checkCommandsSha256: "0000000000000000000000000000000000000000000000000000000000000000", checkTimeoutMinutes: 30 };

// The desktop app's confirmation of a change of the check commands (lib/lado.ts updateLadoProjectChecks).
const confirmChecks = () => vi.mocked(aoBridge.lado.presence.changeProjectChecks);
const put = () => vi.mocked(apiClient.PUT);

// The card as the project's LADO page shows it: with the settings the daemon holds.
function Page() {
	const query = useLadoProjectSettings("demo");
	return query.data ? <LadoProjectChecksCard projectId="demo" settings={query.data} /> : null;
}

function renderCard() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<Page />
		</QueryClientProvider>,
	);
}

const rows = () => screen.queryAllByTestId("lado-check-command");
const nameInput = (number: number) => screen.getByRole("textbox", { name: `Name ${number}` });
const commandInput = (number: number) => screen.getByRole("textbox", { name: `Command ${number}` });
const saveButton = () => screen.getByRole("button", { name: "Save commands" });
const addButton = () => screen.getByRole("button", { name: "Add command" });
const values = () => rows().map((row) => within(row).getAllByRole("textbox").map((input) => (input as HTMLInputElement).value));

// LADO fork: Project settings → LADO → Checks (docs/LJY-PRD/TDD.md §8.2).
describe("LadoProjectChecksCard", () => {
	beforeEach(() => {
		vi.resetAllMocks();
		vi.mocked(lado.fetchLadoProjectSettings).mockResolvedValue(settings);
		vi.spyOn(aoBridge.lado.presence, "changeProjectChecks").mockResolvedValue({ status: "cancelled" });
		vi.spyOn(apiClient, "PUT").mockImplementation((async (_path: string, init: { body: Partial<lado.LadoProjectSettings> }) => ({
			data: { ...settings, ...init.body },
			error: undefined,
		})) as never);
	});

	afterEach(() => {
		cleanup();
		vi.restoreAllMocks();
	});

	it("edits, adds and removes command rows, up to 10", async () => {
		const user = userEvent.setup();
		renderCard();

		expect(await screen.findByRole("heading", { name: "Checks" })).toBeInTheDocument();
		expect(
			screen.getByText(
				"LADO runs these commands in each run's worktree at its pull request's head, outside any agent sandbox, so the desktop app asks you to confirm a change.",
			),
		).toBeInTheDocument();
		expect(values()).toEqual([
			["lint", "npm run lint"],
			["unit tests", "npm test"],
		]);
		// Nothing changed yet, so there is nothing to save.
		expect(saveButton()).toBeDisabled();

		await user.clear(commandInput(2));
		await user.type(commandInput(2), "npm run test:unit");
		expect(saveButton()).toBeEnabled();

		// A new row takes the focus; a row left blank changes nothing.
		await user.click(addButton());
		expect(rows()).toHaveLength(3);
		expect(nameInput(3)).toHaveFocus();
		await user.click(screen.getByRole("button", { name: "Remove unit tests" }));
		expect(values()).toEqual([
			["lint", "npm run lint"],
			["", ""],
		]);
		expect(addButton()).toHaveFocus();

		// LADO runs at most 10 commands.
		for (let count = rows().length; count < 10; count++) await user.click(addButton());
		expect(rows()).toHaveLength(10);
		expect(addButton()).toBeDisabled();
		expect(screen.getByText("LADO runs at most 10 commands.")).toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Remove command 10" }));
		expect(rows()).toHaveLength(9);
		expect(addButton()).toBeEnabled();
		expect(screen.queryByText("LADO runs at most 10 commands.")).not.toBeInTheDocument();
		expect(confirmChecks()).not.toHaveBeenCalled();
	});

	it("saves through the desktop app's confirmation and keeps the edits when it is cancelled", async () => {
		const user = userEvent.setup();
		renderCard();

		await user.click(await screen.findByRole("button", { name: "Remove lint" }));
		await user.click(addButton());
		await user.type(nameInput(2), "  build ");
		await user.type(commandInput(2), "npm run build");
		await user.click(addButton());
		await user.click(saveButton());

		// Names and commands lose surrounding space; a blank row is not sent.
		const sent = [
			{ name: "unit tests", command: "npm test" },
			{ name: "build", command: "npm run build" },
		];
		await waitFor(() => expect(confirmChecks()).toHaveBeenCalledWith("demo", sent));
		await waitFor(() => expect(saveButton()).toBeEnabled());
		expect(values()).toEqual([
			["unit tests", "npm test"],
			["  build ", "npm run build"],
			["", ""],
		]);
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();

		confirmChecks().mockResolvedValue({ status: "changed", settings: { ...settings, checkCommands: sent } });
		await user.click(saveButton());
		await waitFor(() => expect(saveButton()).toBeDisabled());
		expect(values()).toEqual([
			["unit tests", "npm test"],
			["build", "npm run build"],
		]);
		expect(confirmChecks()).toHaveBeenCalledTimes(2);
		// The desktop app sent the change; the renderer did not.
		expect(put()).not.toHaveBeenCalled();
	});

	it("shows a refused command under its row and a refused list under the list", async () => {
		const user = userEvent.setup();
		renderCard();

		await user.click(await screen.findByRole("button", { name: "Add command" }));
		await user.type(nameInput(3), "Lint");
		await user.type(commandInput(3), "npm run lint -- --fix");
		confirmChecks().mockResolvedValue({
			status: "error",
			code: "LADO_CHECK_COMMANDS_INVALID",
			message: "Give at most 10 check commands, each with a unique name and a command.",
			details: { index: 2, field: "name", reason: "duplicate" },
		});
		await user.click(saveButton());

		const alert = await screen.findByRole("alert");
		expect(alert).toHaveTextContent(/^Another command has this name\.$/);
		expect(rows()[2]).toContainElement(alert);
		expect(nameInput(3)).toHaveAttribute("aria-invalid", "true");
		expect(commandInput(3)).not.toHaveAttribute("aria-invalid");
		// The edits stay for the developer to fix; fixing one clears the problem.
		expect(values()[2]).toEqual(["Lint", "npm run lint -- --fix"]);
		await user.type(nameInput(3), " fix");
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();

		confirmChecks().mockResolvedValue({
			status: "error",
			code: "LADO_CHECK_COMMANDS_INVALID",
			message: "Give at most 10 check commands, each with a unique name and a command.",
			details: { index: 10, field: "", reason: "too_many" },
		});
		await user.click(saveButton());
		const listAlert = await screen.findByRole("alert");
		expect(listAlert).toHaveTextContent(/^Add at most 10 check commands\.$/);
		for (const row of rows()) expect(row).not.toContainElement(listAlert);
	});

	// In a browser the daemon answers itself; without the desktop app it has
	// no way to confirm, and the words must be about the check commands.
	it("says in a browser that changing the commands needs the desktop app", async () => {
		const user = userEvent.setup();
		confirmChecks().mockResolvedValue(null);
		put().mockResolvedValue({
			data: undefined,
			error: { code: "LADO_PRESENCE_UNAVAILABLE", message: "Approvals need the AO desktop app." },
		} as never);
		renderCard();

		await user.click(await screen.findByRole("button", { name: "Remove unit tests" }));
		await user.click(saveButton());

		expect(await screen.findByRole("alert")).toHaveTextContent(
			"Changing the check commands needs the AO desktop app. Open the app, then change them there.",
		);
		expect(put()).toHaveBeenCalledWith("/api/v1/lado/projects/{projectId}/settings", {
			params: { path: { projectId: "demo" } },
			// The change names the list it started from (U7 of #15).
			body: { checkCommands: [{ name: "lint", command: "npm run lint" }], checkCommandsBase: settings.checkCommandsSha256 },
		});
		expect(values()).toEqual([["lint", "npm run lint"]]);
	});

	it("saves the time limit at once, without the desktop app", async () => {
		const user = userEvent.setup();
		renderCard();

		const limit = await screen.findByRole("button", { name: "Time limit" });
		expect(limit).toHaveTextContent("30 minutes");
		await user.click(limit);
		expect((await screen.findAllByRole("menuitem")).map((item) => item.textContent)).toEqual([
			"5 minutes",
			"10 minutes",
			"15 minutes",
			"30 minutes",
			"60 minutes",
			"90 minutes",
			"120 minutes",
		]);
		await user.click(screen.getByRole("menuitem", { name: "60 minutes" }));

		await waitFor(() => expect(limit).toHaveTextContent("60 minutes"));
		expect(put()).toHaveBeenCalledWith("/api/v1/lado/projects/{projectId}/settings", {
			params: { path: { projectId: "demo" } },
			body: { checkTimeoutMinutes: 60 },
		});
		expect(confirmChecks()).not.toHaveBeenCalled();
	});
});
