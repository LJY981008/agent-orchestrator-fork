import { act, cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { apiClient } from "../../lib/api-client";
import * as lado from "../../lib/lado";
import { useUiStore } from "../../stores/ui-store";
import { SettingsDialog } from "../SettingsPageTestHarness";
import { LadoProjectSection } from "./LadoProjectSection";

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

const off: lado.LadoProjectSettings = { projectId: "demo", ladoMode: false, maxConcurrent: 1, checkCommands: [], checkTimeoutMinutes: 30 };
const on: lado.LadoProjectSettings = { projectId: "demo", ladoMode: true, maxConcurrent: 1, checkCommands: [], checkTimeoutMinutes: 30 };

const put = () => vi.mocked(apiClient.PUT);
// The desktop app's confirmation of turning LADO mode off (lib/lado.ts setLadoMode).
const confirmModeOff = () => vi.mocked(window.ao!.lado.presence.setLadoMode);

function renderSection() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<LadoProjectSection projectId="demo" />
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

const modeSwitch = () => screen.getByRole("switch", { name: "LADO mode" });
const capSelect = () => screen.getByRole("button", { name: "Concurrent runs in this project" });
const rowOf = (control: HTMLElement) => control.closest<HTMLElement>(".settings-row-bar")!;

// LADO fork: Project settings → LADO (docs/LJY-PRD/TDD.md §9).
describe("LadoProjectSection", () => {
	beforeEach(() => {
		vi.resetAllMocks();
		vi.mocked(lado.fetchLadoProjectSettings).mockResolvedValue(off);
		vi.spyOn(apiClient, "PUT").mockImplementation((async (_path: string, init: { body: Partial<lado.LadoProjectSettings> }) => ({
			data: { ...off, ...init.body },
			error: undefined,
		})) as never);
		vi.spyOn(window.ao!.lado.presence, "setLadoMode").mockResolvedValue(null);
		useUiStore.setState({ developerMode: false, settingsModal: null });
		cloudProjectsState.data = [];
		localWorkspacesState.ids = [];
	});

	afterEach(cleanup);

	it("shows the mode and the project's cap", async () => {
		vi.mocked(lado.fetchLadoProjectSettings).mockResolvedValue({ ...on, maxConcurrent: 3 });
		renderSection();

		expect(await screen.findByRole("switch", { name: "LADO mode" })).toBeChecked();
		expect(capSelect()).toHaveTextContent("3");
		expect(rowOf(modeSwitch())).toHaveTextContent("When on, this project's workers start only from plans you approved.");
		expect(lado.fetchLadoProjectSettings).toHaveBeenCalledWith("demo");
	});

	it("turns LADO mode on with a plain call", async () => {
		const user = userEvent.setup();
		renderSection();

		await user.click(await screen.findByRole("switch", { name: "LADO mode" }));
		await waitFor(() => expect(modeSwitch()).toBeChecked());
		expect(put()).toHaveBeenCalledWith("/api/v1/lado/projects/{projectId}/settings", {
			params: { path: { projectId: "demo" } },
			body: { ladoMode: true },
		});
		expect(confirmModeOff()).not.toHaveBeenCalled();
	});

	it("turns it off only through the desktop app's confirmation, and stays on when it is cancelled", async () => {
		vi.mocked(lado.fetchLadoProjectSettings).mockResolvedValue(on);
		confirmModeOff().mockResolvedValue({ status: "cancelled" });
		const user = userEvent.setup();
		renderSection();

		await user.click(await screen.findByRole("switch", { name: "LADO mode" }));
		await waitFor(() => expect(confirmModeOff()).toHaveBeenCalledWith("demo", false));
		await waitFor(() => expect(modeSwitch()).toBeEnabled());
		expect(modeSwitch()).toBeChecked();
		expect(put()).not.toHaveBeenCalled();
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();

		confirmModeOff().mockResolvedValue({ status: "changed", settings: off });
		await user.click(modeSwitch());
		await waitFor(() => expect(modeSwitch()).not.toBeChecked());
		expect(confirmModeOff()).toHaveBeenCalledTimes(2);
		expect(put()).not.toHaveBeenCalled();
	});

	it("keeps the switch on while the desktop app asks", async () => {
		vi.mocked(lado.fetchLadoProjectSettings).mockResolvedValue(on);
		let answer: (result: { status: "cancelled" }) => void = () => undefined;
		confirmModeOff().mockImplementation(() => new Promise((resolve) => { answer = resolve; }));
		const user = userEvent.setup();
		renderSection();

		await user.click(await screen.findByRole("switch", { name: "LADO mode" }));
		await waitFor(() => expect(modeSwitch()).toBeDisabled());
		expect(modeSwitch()).toBeChecked();
		expect(capSelect()).toBeDisabled();

		await act(async () => answer({ status: "cancelled" }));
		await waitFor(() => expect(modeSwitch()).toBeEnabled());
		expect(modeSwitch()).toBeChecked();
	});

	it("shows the daemon's refusal of turning it on while tracker intake is on", async () => {
		put().mockResolvedValue({
			data: undefined,
			error: { code: "LADO_TRACKER_INTAKE_ENABLED", message: "tracker intake is on for this project" },
		} as never);
		const user = userEvent.setup();
		renderSection();

		await user.click(await screen.findByRole("switch", { name: "LADO mode" }));
		const alert = await screen.findByRole("alert");
		expect(alert).toHaveTextContent("Turn off tracker intake for this project first: LADO projects take issues by import.");
		expect(rowOf(modeSwitch())).toContainElement(alert);
		expect(modeSwitch()).not.toBeChecked();
		expect(modeSwitch()).toBeEnabled();
	});

	// In a browser the daemon answers itself; without the desktop app it has
	// no way to confirm, and the words must be about turning the mode off.
	it("says in a browser that turning the mode off needs the desktop app", async () => {
		vi.mocked(lado.fetchLadoProjectSettings).mockResolvedValue(on);
		put().mockResolvedValue({
			data: undefined,
			error: { code: "LADO_PRESENCE_UNAVAILABLE", message: "Approvals need the AO desktop app." },
		} as never);
		const user = userEvent.setup();
		renderSection();

		await user.click(await screen.findByRole("switch", { name: "LADO mode" }));
		const alert = await screen.findByRole("alert");
		expect(alert).toHaveTextContent("Turning LADO mode off needs the AO desktop app. Open the app, then turn it off there.");
		expect(alert).not.toHaveTextContent(/approve/i);
		expect(rowOf(modeSwitch())).toContainElement(alert);
		expect(modeSwitch()).toBeChecked();
	});

	it("saves the project's cap, also while LADO mode is off", async () => {
		const user = userEvent.setup();
		renderSection();

		await user.click(await screen.findByRole("button", { name: "Concurrent runs in this project" }));
		expect((await screen.findAllByRole("menuitem")).map((item) => item.textContent)).toEqual(["1", "2", "3", "4", "5", "6", "7", "8"]);
		await user.click(screen.getByRole("menuitem", { name: "3" }));
		await waitFor(() => expect(capSelect()).toHaveTextContent("3"));
		expect(put()).toHaveBeenCalledWith("/api/v1/lado/projects/{projectId}/settings", {
			params: { path: { projectId: "demo" } },
			body: { maxConcurrent: 3 },
		});
		expect(modeSwitch()).not.toBeChecked();
		expect(confirmModeOff()).not.toHaveBeenCalled();
	});

	it("shows a refused cap under its row and keeps the saved one", async () => {
		put().mockResolvedValue({
			data: undefined,
			error: { code: "LADO_DISPATCH_SETTINGS_INVALID", message: "maxConcurrent must be 1 to 8", details: { min: 1, max: 8 } },
		} as never);
		const user = userEvent.setup();
		renderSection();

		await user.click(await screen.findByRole("button", { name: "Concurrent runs in this project" }));
		await user.click(await screen.findByRole("menuitem", { name: "5" }));
		const alert = await screen.findByRole("alert");
		// The page has no run time, so the refusal names only the cap's range.
		expect(alert).toHaveTextContent(/^Choose 1 to 8 concurrent runs\.$/);
		expect(rowOf(capSelect())).toContainElement(alert);
		expect(capSelect()).toHaveTextContent("1");
	});

	it("adds a LADO page to every local project, before Rules, and to no remote or cloud one", async () => {
		const user = userEvent.setup();
		localWorkspacesState.ids = ["demo", "plain"];
		vi.mocked(lado.fetchLadoProjectSettings).mockImplementation(async (projectId) => ({ projectId, ladoMode: projectId === "demo", maxConcurrent: 1, checkCommands: [], checkTimeoutMinutes: 30 }));
		const navNames = () => within(screen.getByRole("navigation", { name: "Settings sections" })).getAllByRole("button").map((button) => button.textContent);

		// A local project outside LADO mode turns it on here; Rules follows.
		useUiStore.getState().openProjectSettings("plain");
		renderSettings();
		await user.click(await screen.findByRole("button", { name: "LADO" }));
		expect(navNames()).not.toContain("Rules");
		put().mockResolvedValue({ data: { projectId: "plain", ladoMode: true, maxConcurrent: 1 }, error: undefined } as never);
		await user.click(await screen.findByRole("switch", { name: "LADO mode" }));
		await waitFor(() => expect(navNames().slice(-2)).toEqual(["LADO", "Rules"]));
		cleanup();

		useUiStore.setState({ settingsModal: null });
		useUiStore.getState().openProjectSettings("demo");
		renderSettings();
		await waitFor(() => expect(navNames().slice(-2)).toEqual(["LADO", "Rules"]));
		cleanup();

		// Remote and cloud projects never ask this computer's daemon about LADO.
		vi.mocked(lado.fetchLadoProjectSettings).mockClear();
		useUiStore.setState({ settingsModal: null });
		useUiStore.getState().openProjectSettings("demo", "box-a");
		renderSettings();
		expect(await screen.findByRole("button", { name: "Agents" })).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "LADO" })).not.toBeInTheDocument();
		cleanup();

		cloudProjectsState.data = [{ id: "demo", orgId: "org-1", displayName: "Demo" }];
		useUiStore.setState({ settingsModal: null });
		useUiStore.getState().openProjectSettings("demo");
		renderSettings();
		expect(await screen.findByTestId("project-settings-form")).toHaveTextContent("org-1");
		expect(screen.queryByRole("button", { name: "LADO" })).not.toBeInTheDocument();
		expect(lado.fetchLadoProjectSettings).not.toHaveBeenCalled();
	});
});
