import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { LadoDesktopSettings } from "../../../shared/lado-desktop";
import { appI18n } from "../../i18n";
import { aoBridge } from "../../lib/bridge";
import { LadoBackgroundSection } from "./LadoBackgroundSection";

vi.mock("../../lib/bridge", () => ({
	aoBridge: { lado: { desktop: { get: vi.fn(), set: vi.fn() } } },
}));

const get = aoBridge.lado.desktop.get as ReturnType<typeof vi.fn>;
const set = aoBridge.lado.desktop.set as ReturnType<typeof vi.fn>;

function view(overrides: Partial<LadoDesktopSettings> = {}): LadoDesktopSettings {
	return {
		startAtLogin: { supported: true, enabled: false },
		keepRunning: { enabled: true, setByEnvironment: false },
		...overrides,
	};
}

beforeEach(async () => {
	await appI18n.changeLanguage("en");
	get.mockReset();
	set.mockReset();
});

describe("LadoBackgroundSection", () => {
	it("renders nothing outside the desktop app", async () => {
		get.mockResolvedValue(null);
		const { container } = render(<LadoBackgroundSection />);
		await waitFor(() => expect(get).toHaveBeenCalled());
		expect(container).toBeEmptyDOMElement();
	});

	it("shows both switches and saves a change", async () => {
		const user = userEvent.setup();
		get.mockResolvedValue(view());
		set.mockResolvedValue(view({ startAtLogin: { supported: true, enabled: true } }));
		render(<LadoBackgroundSection />);

		const login = await screen.findByRole("switch", { name: "Start at login" });
		expect(login).not.toBeChecked();
		expect(screen.getByRole("switch", { name: "Keep agents running after quit" })).toBeChecked();

		await user.click(login);
		expect(set).toHaveBeenCalledWith({ startAtLogin: true });
		await waitFor(() => expect(login).toBeChecked());
	});

	it("hides Start at login where it is unsupported", async () => {
		get.mockResolvedValue(view({ startAtLogin: { supported: false, enabled: false } }));
		render(<LadoBackgroundSection />);
		expect(await screen.findByRole("switch", { name: "Keep agents running after quit" })).toBeInTheDocument();
		expect(screen.queryByRole("switch", { name: "Start at login" })).not.toBeInTheDocument();
	});

	it("locks Keep agents running when AO_KEEP_DAEMON sets it", async () => {
		get.mockResolvedValue(view({ keepRunning: { enabled: false, setByEnvironment: true } }));
		render(<LadoBackgroundSection />);
		expect(await screen.findByRole("switch", { name: "Keep agents running after quit" })).toBeDisabled();
		expect(screen.getByText("Set by AO_KEEP_DAEMON")).toBeInTheDocument();
	});

	it("explains a failed save next to the setting and keeps the old value", async () => {
		const user = userEvent.setup();
		get.mockResolvedValue(view());
		set.mockRejectedValue(new Error("denied"));
		render(<LadoBackgroundSection />);

		await user.click(await screen.findByRole("switch", { name: "Keep agents running after quit" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("Could not save this setting.");
		expect(screen.getByRole("switch", { name: "Keep agents running after quit" })).toBeChecked();
	});
});
