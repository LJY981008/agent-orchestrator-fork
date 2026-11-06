import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { DaemonStatus } from "../../../shared/daemon-status";
import { appI18n } from "../../i18n";
import { aoBridge } from "../../lib/bridge";
import { useUiStore } from "../../stores/ui-store";
import { GeneralSettingsSection } from "./GeneralSettingsSection";

vi.mock("../../lib/bridge", () => ({
	aoBridge: { daemon: { restart: vi.fn() } },
}));

// The other General rows read the daemon or main; here they only hold their place.
vi.mock("../../hooks/useSettings", () => ({
	useSettings: () => ({ settings: undefined, isLoading: false, error: null }),
	useUpdateSessionInterface: () => ({ update: vi.fn(), saving: false, error: null }),
	useUpdateCloudOffering: () => ({ update: vi.fn(), saving: false, error: null }),
}));
vi.mock("../lado/LadoBackgroundSection", () => ({ LadoBackgroundSection: () => null }));
vi.mock("../lado/LadoClaudeChromeSection", () => ({ LadoClaudeChromeSection: () => null }));
vi.mock("./DefaultCloudSandboxRow", () => ({ DefaultCloudSandboxSection: () => null }));
vi.mock("./CliLinkSettingsRow", () => ({ CliLinkSettings: () => null }));

const restart = aoBridge.daemon.restart as ReturnType<typeof vi.fn>;

function advancedSection() {
	return screen.getByRole("heading", { name: "Advanced" }).closest("section") as HTMLElement;
}

function restartButton() {
	return within(advancedSection()).getByRole("button", { name: /^(Restart|Restarting…)$/ });
}

async function confirmRestart(user: ReturnType<typeof userEvent.setup>) {
	await user.click(restartButton());
	const dialog = await screen.findByRole("dialog", { name: "Restart the AO daemon?" });
	expect(dialog).toHaveTextContent("reconnect to the new daemon");
	await user.click(within(dialog).getByRole("button", { name: "Restart" }));
}

beforeEach(async () => {
	await appI18n.changeLanguage("en");
	restart.mockReset();
	useUiStore.setState({ developerMode: false });
});

describe("DaemonRestartRow", () => {
	it("shows in General > Advanced with Developer mode off", () => {
		render(<GeneralSettingsSection />);
		const advanced = advancedSection();
		expect(within(advanced).getByText("Restart daemon")).toBeInTheDocument();
		expect(within(advanced).getByText(/saved tokens take effect without a restart/)).toBeInTheDocument();
		expect(restartButton()).toBeEnabled();
	});

	it("restarts once after confirmation and blocks the button while it runs", async () => {
		let finish: (status: DaemonStatus) => void = () => undefined;
		restart.mockImplementation(() => new Promise<DaemonStatus>((resolve) => { finish = resolve; }));
		const user = userEvent.setup();
		render(<GeneralSettingsSection />);
		await confirmRestart(user);
		expect(restart).toHaveBeenCalledOnce();
		expect(restartButton()).toBeDisabled();
		expect(restartButton()).toHaveTextContent("Restarting…");
		finish({ state: "ready" } as DaemonStatus);
		await waitFor(() => expect(restartButton()).toBeEnabled());
		expect(restartButton()).toHaveTextContent("Restart");
		expect(within(advancedSection()).queryByRole("alert")).not.toBeInTheDocument();
	});

	it("shows the failure when the new daemon does not come up", async () => {
		restart.mockResolvedValue({ state: "error", code: "spawn_failed", message: "port 3001 is busy" } as DaemonStatus);
		const user = userEvent.setup();
		render(<GeneralSettingsSection />);
		await confirmRestart(user);
		expect(await within(advancedSection()).findByRole("alert")).toHaveTextContent("port 3001 is busy");
		expect(restartButton()).toBeEnabled();
	});

	it("does not restart when the confirmation is cancelled", async () => {
		const user = userEvent.setup();
		render(<GeneralSettingsSection />);
		await user.click(restartButton());
		const dialog = await screen.findByRole("dialog", { name: "Restart the AO daemon?" });
		await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
		await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
		expect(restart).not.toHaveBeenCalled();
	});
});
