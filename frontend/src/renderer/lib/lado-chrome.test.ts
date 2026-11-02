import { beforeEach, describe, expect, it, vi } from "vitest";
import { apiClient } from "./api-client";
import { LadoApiError } from "./lado";
import {
	fetchLadoClaudeChrome,
	ladoClaudeChromeQueryRoot,
	ladoClaudeChromeSessionQueryKey,
	ladoClaudeChromeSettingsQueryKey,
	ladoClaudeChromeTurnsOn,
	updateLadoClaudeChrome,
} from "./lado-chrome";

const { changeClaudeChromeMock } = vi.hoisted(() => ({ changeClaudeChromeMock: vi.fn() }));

vi.mock("./api-client", () => ({
	apiClient: { GET: vi.fn(), PUT: vi.fn() },
	apiErrorCode: (error: unknown) => (error as { code?: string } | undefined)?.code,
	apiErrorDetails: (error: unknown) => (error as { details?: Record<string, unknown> } | undefined)?.details,
	apiErrorMessage: (error: unknown, fallback: string) => (error as { message?: string } | undefined)?.message ?? fallback,
}));

vi.mock("./bridge", () => ({
	aoBridge: { lado: { presence: { changeClaudeChrome: changeClaudeChromeMock } } },
}));

const get = apiClient.GET as unknown as ReturnType<typeof vi.fn>;
const put = apiClient.PUT as unknown as ReturnType<typeof vi.fn>;
const defaults = { chatSessions: true, ladoWorkers: false };
const allOff = { chatSessions: false, ladoWorkers: false };
const allOn = { chatSessions: true, ladoWorkers: true };

// LADO fork: the Claude in Chrome setting (issue #40).
describe("lado-chrome", () => {
	beforeEach(() => {
		vi.clearAllMocks();
	});

	it("shares one prefix for the setting and the sessions' state", () => {
		expect(ladoClaudeChromeQueryRoot).toEqual(["lado", "claude-chrome"]);
		expect(ladoClaudeChromeSettingsQueryKey).toEqual(["lado", "claude-chrome", "settings"]);
		expect(ladoClaudeChromeSessionQueryKey("s-1")).toEqual(["lado", "claude-chrome", "session", "s-1"]);
	});

	it("reads the setting", async () => {
		get.mockResolvedValue({ data: defaults, error: undefined });
		await expect(fetchLadoClaudeChrome()).resolves.toEqual(defaults);
		expect(get).toHaveBeenCalledWith("/api/v1/lado/claude-chrome");
	});

	it("keeps the code of a failed read", async () => {
		get.mockResolvedValue({ data: undefined, error: { code: "NOT_IMPLEMENTED", message: "Not implemented" } });
		await expect(fetchLadoClaudeChrome()).rejects.toMatchObject({ code: "NOT_IMPLEMENTED" });
	});

	it("tells a change that turns something on", () => {
		expect(ladoClaudeChromeTurnsOn(defaults, allOn)).toBe(true);
		expect(ladoClaudeChromeTurnsOn(allOff, defaults)).toBe(true);
		expect(ladoClaudeChromeTurnsOn(defaults, allOff)).toBe(false);
		expect(ladoClaudeChromeTurnsOn(defaults, defaults)).toBe(false);
		// One on and one off still turns something on.
		expect(ladoClaudeChromeTurnsOn(defaults, { chatSessions: false, ladoWorkers: true })).toBe(true);
	});

	it("turns parts off with a plain call", async () => {
		put.mockResolvedValue({ data: allOff, error: undefined });
		await expect(updateLadoClaudeChrome(defaults, allOff)).resolves.toEqual(allOff);
		expect(put).toHaveBeenCalledWith("/api/v1/lado/claude-chrome", { body: allOff });
		expect(changeClaudeChromeMock).not.toHaveBeenCalled();
	});

	it("turns something on through the desktop app", async () => {
		changeClaudeChromeMock.mockResolvedValue({ status: "changed", settings: allOn });
		await expect(updateLadoClaudeChrome(defaults, allOn)).resolves.toEqual(allOn);
		// Only the two switches cross.
		expect(changeClaudeChromeMock).toHaveBeenCalledWith(allOn);
		expect(put).not.toHaveBeenCalled();
	});

	it("changes nothing when the developer cancels", async () => {
		changeClaudeChromeMock.mockResolvedValue({ status: "cancelled" });
		await expect(updateLadoClaudeChrome(defaults, allOn)).resolves.toBeNull();
		expect(put).not.toHaveBeenCalled();
	});

	it("keeps the code of a refusal from the desktop app", async () => {
		changeClaudeChromeMock.mockResolvedValue({
			status: "error",
			code: "LADO_PRESENCE_REPLAYED",
			message: "This confirmation was already used.",
		});
		const error = await updateLadoClaudeChrome(defaults, allOn).catch((caught: unknown) => caught);
		expect(error).toBeInstanceOf(LadoApiError);
		expect(error).toMatchObject({ code: "LADO_PRESENCE_REPLAYED" });
		expect(put).not.toHaveBeenCalled();
	});

	it("turns something on directly in a browser", async () => {
		changeClaudeChromeMock.mockResolvedValue(null);
		put.mockResolvedValue({ data: allOn, error: undefined });
		await expect(updateLadoClaudeChrome(defaults, allOn)).resolves.toEqual(allOn);
		expect(put).toHaveBeenCalledWith("/api/v1/lado/claude-chrome", { body: allOn });
	});

	it("keeps the daemon's refusal of a plain call", async () => {
		changeClaudeChromeMock.mockResolvedValue(null);
		put.mockResolvedValue({
			data: undefined,
			error: { code: "LADO_PRESENCE_UNAVAILABLE", message: "Turn it on in the AO desktop app." },
		});
		await expect(updateLadoClaudeChrome(defaults, allOn)).rejects.toMatchObject({ code: "LADO_PRESENCE_UNAVAILABLE" });
	});
});
